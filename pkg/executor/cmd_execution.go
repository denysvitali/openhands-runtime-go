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

// executeCmdRun executes a command in the persistent bash session
func (e *Executor) executeCmdRun(ctx context.Context, action models.CmdRunAction) (interface{}, error) {
	ctx, span := e.tracer.Start(ctx, "cmd_run_persistent_bash")
	defer span.End()

	e.bashMutex.Lock()
	defer e.bashMutex.Unlock()

	targetWd := e.currentBashCwd
	if action.Cwd != "" {
		if filepath.IsAbs(action.Cwd) {
			targetWd = action.Cwd
		} else {
			targetWd = filepath.Join(e.currentBashCwd, action.Cwd)
		}
		targetWd = filepath.Clean(targetWd)
	}
	targetWdQuoted := QuotePathForBash(targetWd)

	span.SetAttributes(
		attribute.String("command", action.Command),
		attribute.String("resolved_cwd", targetWd),
	)

	e.logger.Infof("Executing in bash (targetWD: %s): %s", targetWd, action.Command)

	var actualCommandToRunInBash string
	if action.Command == "" {
		actualCommandToRunInBash = "true"
	} else {
		actualCommandToRunInBash = action.Command
	}

	wrappedCommand := fmt.Sprintf(
		"cd %s; CD_EXIT_CODE=$?; if [ $CD_EXIT_CODE -eq 0 ]; then (%s); CMD_EXIT_CODE=$?; else CMD_EXIT_CODE=$CD_EXIT_CODE; fi; NEW_PWD=$(pwd); echo %s $CMD_EXIT_CODE $NEW_PWD\n",
		targetWdQuoted,
		actualCommandToRunInBash,
		bashCommandTerminatorPrefix,
	)

	cmdCtx := ctx
	var cancelCmd context.CancelFunc
	if action.HardTimeout > 0 {
		cmdCtx, cancelCmd = context.WithTimeout(ctx, time.Duration(action.HardTimeout)*time.Second)
		defer cancelCmd()
	}

	outputChan := make(chan string, 128)
	errChan := make(chan error, 2)
	doneChan := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(2)

	var cmdExitCode int = -1
	var newPwdFromTerminator string

	// Handle stdout
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				e.logger.Errorf("Panic recovered in stdout reader: %v", r)
				errChan <- fmt.Errorf("panic in stdout reader: %v", r)
			}
		}()
		stdoutScanner := bufio.NewScanner(e.bashStdout)
		for stdoutScanner.Scan() {
			line := stdoutScanner.Text()
			if strings.HasPrefix(line, bashCommandTerminatorPrefix) {
				trimmedLine := strings.TrimSpace(strings.TrimPrefix(line, bashCommandTerminatorPrefix))
				parts := strings.Fields(trimmedLine)
				if len(parts) >= 1 {
					if _, err := fmt.Sscanf(parts[0], "%d", &cmdExitCode); err != nil {
						e.logger.Errorf("Failed to parse exit code from terminator '%s': %v", parts[0], err)
					}
					if len(parts) >= 2 {
						newPwdFromTerminator = strings.Join(parts[1:], " ")
					} else {
						e.logger.Warnf("Terminator line for command '%s' did not include PWD. Line: '%s'", action.Command, line)
					}
				} else {
					e.logger.Errorf("Could not parse exit code from terminator line: '%s'", line)
				}
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
			errChan <- fmt.Errorf("stdout scan error: %w", err)
		}
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
			errChan <- fmt.Errorf("stderr scan error: %w", err)
		}
	}()

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

	var outputBuffer strings.Builder

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
			if newPwdFromTerminator != "" {
				e.currentBashCwd = newPwdFromTerminator
				span.SetAttributes(attribute.String("bash.new_cwd", e.currentBashCwd))
				e.logger.Infof("Bash CWD updated to: %s", e.currentBashCwd)
			} else if cmdExitCode == 0 {
				e.logger.Warnf("PWD not read from terminator for successful command '%s'. CWD may be stale.", action.Command)
			}
		case <-cmdCtx.Done():
			err := cmdCtx.Err()
			e.logger.Warnf("Command '%s' context done (timeout/cancelled): %v", action.Command, err)
			outputBuffer.WriteString(fmt.Sprintf("\n[Command timed out after %s or was cancelled.]\n", time.Duration(action.HardTimeout)*time.Second))
			span.SetAttributes(attribute.Bool("command.timed_out", true))

			if killErr := e.killBashProcessGroup(); killErr != nil {
				outputBuffer.WriteString(fmt.Sprintf("\n[Failed to send SIGINT: %v]\n", killErr))
			}
			cmdExitCode = -1
			break mainLoop
		}
	}

	if cancelCmd != nil {
		cancelCmd()
	}

	wg.Wait()

	span.SetAttributes(attribute.Int("exit_code", cmdExitCode))

	return models.CmdOutputObservation{
		Observation: "run",
		Content:     outputBuffer.String(),
		Timestamp:   time.Now(),
		Extras: map[string]interface{}{
			"command":   action.Command,
			"exit_code": cmdExitCode,
		},
	}, nil
}
