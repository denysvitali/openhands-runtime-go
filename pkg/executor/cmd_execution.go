package executor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/denysvitali/openhands-runtime-go/internal/models"
	"go.opentelemetry.io/otel/attribute"
)

// executeCmdRun executes a command in the bash shell
func (e *Executor) executeCmdRun(ctx context.Context, action models.CmdRunAction) (interface{}, error) {
	_, span := e.tracer.Start(ctx, "cmd_run")
	defer span.End()

	// Set span attributes for tracing
	span.SetAttributes(
		attribute.String("command", action.Command),
		attribute.Bool("is_static", action.IsStatic),
	)

	// Log the command execution
	e.logger.Infof("Executing command: %s", action.Command)

	// Set working directory if specified
	cwd := e.workingDir
	if action.Cwd != "" {
		// Make sure the path is resolved if it's relative
		if !filepath.IsAbs(action.Cwd) {
			cwd = filepath.Join(e.workingDir, action.Cwd)
		} else {
			cwd = action.Cwd
		}
	}

	// Create a new context with timeout if hardTimeout is specified
	execCtx := ctx
	var cancel context.CancelFunc
	if action.HardTimeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, time.Duration(action.HardTimeout)*time.Second)
		defer cancel()
	}

	// Prepare command options
	cmd := exec.CommandContext(execCtx, "bash", "-c", action.Command)
	cmd.Dir = cwd

	// Set up environment variables
	// This is just a basic implementation - in a real scenario, you would
	// likely want to preserve certain environment variables from the parent process
	cmd.Env = []string{
		fmt.Sprintf("PATH=%s", os.Getenv("PATH")),
		fmt.Sprintf("HOME=%s", os.Getenv("HOME")),
	}

	// Capture stdout and stderr
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Run the command
	err := cmd.Run()

	// Get the command exit code
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if execCtx.Err() == context.DeadlineExceeded {
			// If the context deadline was exceeded, it's a timeout
			exitCode = 124 // Standard timeout exit code
			e.logger.Warnf("Command timed out: %s", action.Command)
		} else {
			// Command failed to start or other error
			return models.NewErrorObservation(
				fmt.Sprintf("Failed to execute command: %v", err),
				"CommandExecutionError",
			), nil
		}
	}

	// Combine stdout and stderr
	output := stdout.String()
	if stderr.Len() > 0 {
		if output != "" {
			output += "\n"
		}
		output += stderr.String()
	}

	// If the command timed out, add a message to the output
	if execCtx.Err() == context.DeadlineExceeded {
		if output != "" {
			output += "\n"
		}
		output += fmt.Sprintf("[Command timed out after %d seconds]", action.HardTimeout)
		exitCode = 124 // Make sure exit code is set for timeout
	}

	e.logger.Debugf("Command executed with exit code: %d in directory: %s", exitCode, cwd)

	// Create the CmdOutputObservation with command ID (process ID)
	commandID := ""
	if cmd.Process != nil {
		commandID = fmt.Sprintf("%d", cmd.Process.Pid)
	}

	return models.NewCmdOutputObservation(output, exitCode, commandID), nil
}
