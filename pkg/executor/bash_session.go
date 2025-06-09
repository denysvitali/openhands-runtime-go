package executor

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

const (
	bashCommandTerminatorPrefix = "__OPENHANDS_COMMAND_DONE__"
)

// initBashSession initializes a persistent bash session
func (e *Executor) initBashSession() error {
	e.bashMutex.Lock()
	defer e.bashMutex.Unlock()

	cmd := exec.Command("bash", "-i")
	cmd.Dir = e.workingDir
	e.currentBashCwd = e.workingDir

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdin pipe for bash: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdout pipe for bash: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to get stderr pipe for bash: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start bash process: %w", err)
	}

	e.bashCmd = cmd
	e.bashStdin = stdin
	e.bashStdout = bufio.NewReader(stdout)
	e.bashStderr = bufio.NewReader(stderr)

	e.logger.Info("Persistent bash session initialized.")

	// Initialize bash environment
	initCommands := []string{
		"git config --global user.name \"openhands\"",
		"git config --global user.email \"openhands@all-hands.dev\"",
		"alias git='git --no-pager'",
		"PS1='OPENHANDS_PROMPT>'",
	}

	for _, initCmd := range initCommands {
		if _, err := e.bashStdin.Write([]byte(initCmd + "\n")); err != nil {
			e.logger.Errorf("Failed to write init command '%s' to bash: %v. Session may be unstable.", initCmd, err)
		}
	}

	finalInitCmd := fmt.Sprintf("echo 'Bash init complete.'; echo %s $? $(pwd)\n", bashCommandTerminatorPrefix)
	if _, err := e.bashStdin.Write([]byte(finalInitCmd)); err != nil {
		e.logger.Errorf("Failed to write final init command to bash: %v. Session may be unstable.", err)
	}

	// Start goroutines to handle stdout and stderr
	go e.handleBashStdout()
	go e.handleBashStderr()
	go e.stdoutDistributor()

	return nil
}

// handleBashStdout handles stdout from the bash session continuously
func (e *Executor) handleBashStdout() {
	scanner := bufio.NewScanner(e.bashStdout)
	for scanner.Scan() {
		line := scanner.Text()
		select {
		case e.stdoutLines <- line:
			// Line sent to distributor
		default:
			// Channel is full, log and discard to prevent blocking
			e.logger.Warnf("Stdout channel full, discarding line: %s", line)
		}
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		e.logger.Errorf("Error reading bash stdout: %v", err)
	}
	close(e.stdoutLines)
}

// stdoutDistributor distributes stdout lines to appropriate handlers
func (e *Executor) stdoutDistributor() {
	initializationComplete := false

	for line := range e.stdoutLines {
		if !initializationComplete {
			e.logger.Debugf("Bash init stdout: %s", line)
			if strings.HasPrefix(line, bashCommandTerminatorPrefix) {
				fields := strings.Fields(strings.TrimPrefix(line, bashCommandTerminatorPrefix))
				if len(fields) >= 2 {
					e.currentBashCwd = strings.Join(fields[1:], " ")
					e.logger.Infof("Bash initial CWD set to: %s", e.currentBashCwd)
				}
				initializationComplete = true
				continue
			}
		} else {
			// After initialization, distribute to active command channels or discard
			e.outputMutex.RLock()
			hasActiveChannels := len(e.cmdOutputChans) > 0
			if hasActiveChannels {
				for cmdID, outputChan := range e.cmdOutputChans {
					select {
					case outputChan <- line:
						// Line sent to command handler
					default:
						// Command channel is full, log but continue
						e.logger.Warnf("Command %s output channel full, discarding line: %s", cmdID, line)
					}
				}
			} else {
				// No active commands, just discard to keep pipe drained
				e.logger.Debugf("Background bash stdout (discarded): %s", line)
			}
			e.outputMutex.RUnlock()
		}
	}
}

// handleBashStderr handles stderr from the bash session during initialization
func (e *Executor) handleBashStderr() {
	scanner := bufio.NewScanner(e.bashStderr)
	for scanner.Scan() {
		line := scanner.Text()
		e.logger.Debugf("Bash init stderr: %s", line)
		if strings.HasPrefix(line, bashCommandTerminatorPrefix) {
			e.logger.Warnf("Bash init stderr contained terminator line: %s", line)
			return
		}
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		e.logger.Warnf("Error reading bash init stderr: %v", err)
	}
}

// closeBashSession closes the persistent bash session
func (e *Executor) closeBashSession() error {
	e.bashMutex.Lock()
	defer e.bashMutex.Unlock()

	if e.bashCmd != nil {
		e.logger.Info("Closing persistent bash session...")
		if e.bashStdin != nil {
			_, err := e.bashStdin.Write([]byte("exit\n"))
			if err != nil {
				e.logger.Warnf("Failed to send exit command to bash: %v", err)
			}
			if err := e.bashStdin.Close(); err != nil {
				e.logger.Warnf("Failed to close bash stdin: %v", err)
			}
		}

		if e.bashCmd.Process != nil {
			done := make(chan error, 1)
			go func() {
				done <- e.bashCmd.Wait()
			}()
			select {
			case <-time.After(5 * time.Second):
				e.logger.Warn("Timeout waiting for bash process to exit. Attempting to kill...")
				if killErr := e.bashCmd.Process.Kill(); killErr != nil {
					e.logger.Errorf("Failed to kill bash process: %v", killErr)
				} else {
					e.logger.Info("Bash process killed.")
				}
			case err := <-done:
				if err != nil {
					e.logger.Infof("Bash process exited with status: %v", err)
				} else {
					e.logger.Info("Bash process exited gracefully.")
				}
			}
		}
		e.bashCmd = nil
	}
	return nil
}

// QuotePathForBash quotes a path for safe use in a bash command, especially with cd.
// It handles paths with spaces and single quotes.
func QuotePathForBash(path string) string {
	return "'" + strings.ReplaceAll(path, "'", "'\\''") + "'"
}

// killBashProcessGroup sends SIGINT to the bash process group
func (e *Executor) killBashProcessGroup() error {
	if e.bashCmd != nil && e.bashCmd.Process != nil {
		e.logger.Info("Attempting to send SIGINT to bash process group...")
		if err := syscall.Kill(-e.bashCmd.Process.Pid, syscall.SIGINT); err != nil {
			e.logger.Errorf("Failed to send SIGINT to bash process group: %v. Trying individual process...", err)
			if sigErr := e.bashCmd.Process.Signal(os.Interrupt); sigErr != nil {
				e.logger.Errorf("Failed to send SIGINT to bash process: %v", sigErr)
				return sigErr
			}
		} else {
			e.logger.Info("SIGINT sent to bash process group.")
		}
	}
	return nil
}

// registerCommandOutputChannel registers a channel to receive stdout for a specific command
func (e *Executor) registerCommandOutputChannel(cmdID string, outputChan chan string) {
	e.outputMutex.Lock()
	defer e.outputMutex.Unlock()
	e.cmdOutputChans[cmdID] = outputChan
}

// unregisterCommandOutputChannel removes a command's output channel
func (e *Executor) unregisterCommandOutputChannel(cmdID string) {
	e.outputMutex.Lock()
	defer e.outputMutex.Unlock()
	delete(e.cmdOutputChans, cmdID)
}
