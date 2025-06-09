package executor

import (
	"bufio"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/denysvitali/openhands-runtime-go/internal/models"
)

// resolveTargetWorkingDirectory resolves the target working directory for command execution
func (e *Executor) resolveTargetWorkingDirectory(cwd string) string {
	targetWd := e.currentBashCwd
	if cwd != "" {
		if filepath.IsAbs(cwd) {
			targetWd = cwd
		} else {
			targetWd = filepath.Join(e.currentBashCwd, cwd)
		}
		targetWd = filepath.Clean(targetWd)
	}
	return targetWd
}

// buildWrappedCommand creates the bash command with proper error handling and termination
func (e *Executor) buildWrappedCommand(targetWdQuoted, command string) string {
	actualCommand := command
	if command == "" {
		actualCommand = "true"
	}

	return fmt.Sprintf(
		"cd %s; CD_EXIT_CODE=$?; if [ $CD_EXIT_CODE -eq 0 ]; then (%s); CMD_EXIT_CODE=$?; else CMD_EXIT_CODE=$CD_EXIT_CODE; fi; NEW_PWD=$(pwd); echo %s $CMD_EXIT_CODE $NEW_PWD\n",
		targetWdQuoted,
		actualCommand,
		bashCommandTerminatorPrefix,
	)
}

// cmdExecutionResult holds the result of command execution
type cmdExecutionResult struct {
	exitCode     int
	newPwd       string
	outputBuffer strings.Builder
	timedOut     bool
}

// setupCommandExecution sets up the context and channels for command execution
func (e *Executor) setupCommandExecution(ctx context.Context, hardTimeout int) (context.Context, context.CancelFunc, chan string, chan error, chan struct{}) {
	cmdCtx := ctx
	var cancelCmd context.CancelFunc
	if hardTimeout > 0 {
		cmdCtx, cancelCmd = context.WithTimeout(ctx, time.Duration(hardTimeout)*time.Second)
	}

	outputChan := make(chan string, 128)
	errChan := make(chan error, 2)
	doneChan := make(chan struct{})

	return cmdCtx, cancelCmd, outputChan, errChan, doneChan
}

// startOutputReaders starts the goroutines for reading stdout and stderr
func (e *Executor) startOutputReaders(cmdCtx context.Context, outputChan chan string, errChan chan error, doneChan chan struct{}, cmdExitCode *int, newPwdFromTerminator *string) *sync.WaitGroup {
	var wg sync.WaitGroup
	wg.Add(2)

	// Handle stdout
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				e.logger.Errorf("Panic recovered in stdout reader: %v", r)
				errChan <- fmt.Errorf("panic in stdout reader: %v", r)
			}
		}()
		e.handleStdoutReader(cmdCtx, outputChan, doneChan, cmdExitCode, newPwdFromTerminator)
	}()

	// Handle stderr
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				e.logger.Errorf("Panic recovered in stderr reader: %v", r)
				errChan <- fmt.Errorf("panic in stderr reader: %v", r)
			}
		}()
		e.handleStderrReader(cmdCtx, outputChan, doneChan)
	}()

	return &wg
}

// handleStdoutReader processes stdout and looks for command termination
func (e *Executor) handleStdoutReader(cmdCtx context.Context, outputChan chan string, doneChan chan struct{}, cmdExitCode *int, newPwdFromTerminator *string) {
	stdoutScanner := bufio.NewScanner(e.bashStdout)
	for stdoutScanner.Scan() {
		line := stdoutScanner.Text()
		if strings.HasPrefix(line, bashCommandTerminatorPrefix) {
			e.parseTerminatorLine(line, cmdExitCode, newPwdFromTerminator)
			close(doneChan)
			return
		}
		select {
		case outputChan <- line + "\n":
		case <-cmdCtx.Done():
			return
		}
	}
	if err := stdoutScanner.Err(); err != nil {
		e.logger.Errorf("stdout scan error: %v", err)
	}
}

// handleStderrReader processes stderr output
func (e *Executor) handleStderrReader(cmdCtx context.Context, outputChan chan string, doneChan chan struct{}) {
	stderrScanner := bufio.NewScanner(e.bashStderr)
	for stderrScanner.Scan() {
		line := stderrScanner.Text()
		select {
		case outputChan <- line + "\n":
		case <-cmdCtx.Done():
			return
		case <-doneChan:
			return
		}
	}
	if err := stderrScanner.Err(); err != nil {
		e.logger.Errorf("stderr scan error: %v", err)
	}
}

// parseTerminatorLine parses the command termination line to extract exit code and pwd
func (e *Executor) parseTerminatorLine(line string, cmdExitCode *int, newPwdFromTerminator *string) {
	trimmedLine := strings.TrimSpace(strings.TrimPrefix(line, bashCommandTerminatorPrefix))
	parts := strings.Fields(trimmedLine)
	if len(parts) >= 1 {
		if _, err := fmt.Sscanf(parts[0], "%d", cmdExitCode); err != nil {
			e.logger.Errorf("Failed to parse exit code from terminator '%s': %v", parts[0], err)
		}
		if len(parts) >= 2 {
			*newPwdFromTerminator = strings.Join(parts[1:], " ")
		} else {
			e.logger.Warnf("Terminator line did not include PWD. Line: '%s'", line)
		}
	} else {
		e.logger.Errorf("Could not parse exit code from terminator line: '%s'", line)
	}
}

// processCommandLoop handles the main command execution loop
func (e *Executor) processCommandLoop(cmdCtx context.Context, outputChan chan string, errChan chan error, doneChan chan struct{}, action models.CmdRunAction, cmdExitCode *int, newPwdFromTerminator *string) cmdExecutionResult {
	var outputBuffer strings.Builder
	result := cmdExecutionResult{
		exitCode: *cmdExitCode,
	}

mainLoop:
	for {
		select {
		case line, ok := <-outputChan:
			if ok {
				outputBuffer.WriteString(line)
			} else {
				break mainLoop
			}
		case err := <-errChan:
			e.logger.Errorf("Error from bash I/O goroutine: %v", err)
			outputBuffer.WriteString(fmt.Sprintf("\n[EXECUTOR_IO_ERROR: %v]\n", err))
		case <-doneChan:
			if *newPwdFromTerminator != "" {
				e.currentBashCwd = *newPwdFromTerminator
				e.logger.Infof("Bash CWD updated to: %s", e.currentBashCwd)
			} else if *cmdExitCode == 0 {
				e.logger.Warnf("PWD not read from terminator for successful command '%s'. CWD may be stale.", action.Command)
			}
			break mainLoop
		case <-cmdCtx.Done():
			err := cmdCtx.Err()
			e.logger.Warnf("Command '%s' context done (timeout/cancelled): %v", action.Command, err)
			outputBuffer.WriteString(fmt.Sprintf("\n[Command timed out after %s or was cancelled.]\n", time.Duration(action.HardTimeout)*time.Second))

			if killErr := e.killBashProcessGroup(); killErr != nil {
				outputBuffer.WriteString(fmt.Sprintf("\n[Failed to send SIGINT: %v]\n", killErr))
			}
			result.exitCode = -1
			result.timedOut = true
			break mainLoop
		}
	}

	result.exitCode = *cmdExitCode
	result.newPwd = *newPwdFromTerminator
	result.outputBuffer = outputBuffer
	return result
}

// executeCmdRun executes a command in the persistent bash session
func (e *Executor) executeCmdRun(ctx context.Context, action models.CmdRunAction) (interface{}, error) {
	ctx, span := e.tracer.Start(ctx, "cmd_run_persistent_bash")
	defer span.End()

	e.bashMutex.Lock()
	defer e.bashMutex.Unlock()

	// Resolve target working directory
	targetWd := e.resolveTargetWorkingDirectory(action.Cwd)
	targetWdQuoted := QuotePathForBash(targetWd)

	span.SetAttributes(
		attribute.String("command", action.Command),
		attribute.String("resolved_cwd", targetWd),
	)

	e.logger.Infof("Executing in bash (targetWD: %s): %s", targetWd, action.Command)

	// Prepare command for execution
	wrappedCommand := e.buildWrappedCommand(targetWdQuoted, action.Command)

	cmdCtx, cancelCmd, outputChan, errChan, doneChan := e.setupCommandExecution(ctx, action.HardTimeout)
	defer func() {
		if cancelCmd != nil {
			cancelCmd()
		}
	}()

	var cmdExitCode int = -1
	var newPwdFromTerminator string

	wg := e.startOutputReaders(cmdCtx, outputChan, errChan, doneChan, &cmdExitCode, &newPwdFromTerminator)

	if _, err := e.bashStdin.Write([]byte(wrappedCommand)); err != nil {
		span.RecordError(err)
		if cancelCmd != nil {
			cancelCmd()
		}
		wg.Wait()
		close(outputChan)
		return models.ErrorObservation{
			Observation: "error",
			Content:     fmt.Sprintf("Failed to write command to bash stdin: %v", err),
			ErrorType:   "BashIOError",
			Timestamp:   time.Now(),
		}, nil
	}

	result := e.processCommandLoop(cmdCtx, outputChan, errChan, doneChan, action, &cmdExitCode, &newPwdFromTerminator)

	wg.Wait()

	span.SetAttributes(attribute.Int("exit_code", result.exitCode))

	return models.CmdOutputObservation{
		Observation: "run",
		Content:     result.outputBuffer.String(),
		Timestamp:   time.Now(),
		Extras: map[string]interface{}{
			"command":   action.Command,
			"exit_code": result.exitCode,
		},
	}, nil
}
