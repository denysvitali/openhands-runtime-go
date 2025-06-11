package executor

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/denysvitali/openhands-runtime-go/internal/models"
	"go.opentelemetry.io/otel/attribute"
)

// executeCmdRun executes a shell command and returns its output
func (e *Executor) executeCmdRun(ctx context.Context, action models.CmdRunAction) (interface{}, error) {
	ctx, span := e.tracer.Start(ctx, "cmd_run")
	defer span.End()

	span.SetAttributes(
		attribute.String("command", action.Command),
		attribute.String("cwd", action.Cwd),
		attribute.Bool("is_static", action.IsStatic),
		attribute.Int("hard_timeout", action.HardTimeout),
	)

	// Create a new context with timeout if specified
	if action.HardTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(action.HardTimeout)*time.Second)
		defer cancel()
	}

	// Create and configure the command
	cmd, stdout, stderr, err := e.createCommand(ctx, action)
	if err != nil {
		return err, nil
	}

	// Execute the command and collect output
	output, exitCode, err := e.executeCommand(cmd, stdout, stderr)
	if err != nil {
		return err, nil
	}

	return models.CmdOutputObservation{
		Observation: "run",
		Content:     output,
		Timestamp:   time.Now(),
		Extras: map[string]interface{}{
			"command":   action.Command,
			"exit_code": exitCode,
			"cwd":       cmd.Dir,
		},
	}, nil
}

// createCommand creates and configures a new command
func (e *Executor) createCommand(ctx context.Context, action models.CmdRunAction) (*exec.Cmd, io.ReadCloser, io.ReadCloser, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", action.Command)

	// Set working directory if specified
	if action.Cwd != "" {
		cmd.Dir = e.resolvePath(action.Cwd)
	} else {
		cmd.Dir = e.workingDir
	}

	// Create pipes for stdout and stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, fmt.Errorf("failed to start command: %w", err)
	}

	return cmd, stdout, stderr, nil
}

// executeCommand executes the command and returns its output and exit code
func (e *Executor) executeCommand(cmd *exec.Cmd, stdout, stderr io.ReadCloser) (string, int, error) {
	// Create buffers for output with reasonable initial size
	var stdoutBuf, stderrBuf strings.Builder
	stdoutBuf.Grow(1024) // 1KB initial capacity
	stderrBuf.Grow(1024) // 1KB initial capacity

	// Create channels for goroutine completion
	stdoutDone := make(chan struct{})
	stderrDone := make(chan struct{})

	// Read stdout in a goroutine
	go func() {
		defer close(stdoutDone)
		_, err := io.Copy(&stdoutBuf, stdout)
		if err != nil && err != io.EOF {
			e.logger.Warnf("Error reading stdout: %v", err)
		}
	}()

	// Read stderr in a goroutine
	go func() {
		defer close(stderrDone)
		_, err := io.Copy(&stderrBuf, stderr)
		if err != nil && err != io.EOF {
			e.logger.Warnf("Error reading stderr: %v", err)
		}
	}()

	// Wait for command completion
	err := cmd.Wait()

	// Wait for output reading to complete
	<-stdoutDone
	<-stderrDone

	// Combine outputs
	outputStr := stdoutBuf.String()
	if stderrBuf.Len() > 0 {
		if outputStr != "" {
			outputStr += "\n"
		}
		outputStr += stderrBuf.String()
	}

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			// Handle other errors (like context cancellation)
			exitCode = -1
			if err == context.DeadlineExceeded {
				outputStr = "Command timed out"
			} else if err == context.Canceled {
				outputStr = "Command was cancelled"
			} else {
				outputStr = fmt.Sprintf("Command failed: %v", err)
			}
		}
	}

	return outputStr, exitCode, nil
}
