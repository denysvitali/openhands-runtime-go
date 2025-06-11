package executor

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/denysvitali/openhands-runtime-go/internal/models"
	"go.opentelemetry.io/otel/attribute"
)

// Default timeout for commands that don't specify one
const defaultCommandTimeout = 300 // seconds

// executeCmdRun executes a shell command and returns its output
func (e *Executor) executeCmdRun(ctx context.Context, action models.CmdRunAction) (interface{}, error) {
	ctx, span := e.tracer.Start(ctx, "cmd_run")
	defer span.End()

	// Log command execution
	e.logger.Infof("Executing command: %s", action.Command)

	span.SetAttributes(
		attribute.String("command", action.Command),
		attribute.String("cwd", action.Cwd),
		attribute.Bool("is_static", action.IsStatic),
		attribute.Int("hard_timeout", action.HardTimeout),
	)

	// For static commands, use a simpler execution path
	if action.IsStatic {
		return e.executeStaticCommand(ctx, action)
	}

	// Create a new context with timeout
	timeout := defaultCommandTimeout
	if action.HardTimeout > 0 {
		timeout = action.HardTimeout
	}
	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	// Create and configure the command
	cmd, outputChan, errChan := e.createCommandWithStreaming(execCtx, action)
	if cmd == nil {
		return models.ErrorObservation{
			Observation: "error",
			Content:     "Failed to create command",
			ErrorType:   "CommandCreationError",
			Timestamp:   time.Now(),
		}, nil
	}

	// Execute the command and collect output
	output, exitCode := e.executeCommandWithStreaming(cmd, outputChan, errChan, execCtx.Done())

	// Check if the command was killed due to timeout
	if exitCode == -1 && execCtx.Err() == context.DeadlineExceeded {
		e.logger.Warnf("Command timed out after %d seconds: %s", timeout, action.Command)
		output += "\n[Command timed out]"
	}

	// Special handling for git commands - add changes field for compatibility with OpenHands
	extras := map[string]interface{}{
		"command":   action.Command,
		"exit_code": exitCode,
		"cwd":       cmd.Dir,
	}

	// Detect git commands and add changes field to ensure frontend compatibility
	if strings.Contains(action.Command, "git ") {
		// Common git commands that need to report changes
		if strings.Contains(action.Command, "git status") ||
			strings.Contains(action.Command, "git diff") ||
			strings.Contains(action.Command, "git add") ||
			strings.Contains(action.Command, "git commit") ||
			strings.Contains(action.Command, "git checkout") {

			e.logger.Debug("Adding changes field for git command compatibility")

			// Add a changes field with the output to ensure frontend displays it correctly
			extras["changes"] = output
		}
	}

	return models.CmdOutputObservation{
		Observation: "run",
		Content:     output,
		Timestamp:   time.Now(),
		Extras:      extras,
	}, nil
}

// executeStaticCommand executes a command in a static context (not using the persistent bash session)
func (e *Executor) executeStaticCommand(ctx context.Context, action models.CmdRunAction) (interface{}, error) {
	workDir := action.Cwd
	if workDir == "" {
		workDir = e.workingDir
	}

	e.logger.Infof("Executing static command in %s: %s", workDir, action.Command)

	// Create command
	cmd := exec.CommandContext(ctx, "sh", "-c", action.Command)
	cmd.Dir = workDir

	// Capture output
	output, err := cmd.CombinedOutput()
	outputStr := string(output)

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
			e.logger.Errorf("Static command error: %v", err)
		}
	}

	// Special handling for git commands - add changes field for compatibility with OpenHands
	extras := map[string]interface{}{
		"command":   action.Command,
		"exit_code": exitCode,
		"cwd":       workDir,
	}

	// Detect git commands and add changes field to ensure frontend compatibility
	if strings.Contains(action.Command, "git ") {
		// Common git commands that need to report changes
		if strings.Contains(action.Command, "git status") ||
			strings.Contains(action.Command, "git diff") ||
			strings.Contains(action.Command, "git add") ||
			strings.Contains(action.Command, "git commit") ||
			strings.Contains(action.Command, "git checkout") {

			e.logger.Debug("Adding changes field for git command compatibility")

			// Add a changes field with the output to ensure frontend displays it correctly
			extras["changes"] = outputStr
		}
	}

	return models.CmdOutputObservation{
		Observation: "run",
		Content:     outputStr,
		Timestamp:   time.Now(),
		Extras:      extras,
	}, nil
}

// createCommandWithStreaming creates a command with real-time output streaming
func (e *Executor) createCommandWithStreaming(ctx context.Context, action models.CmdRunAction) (*exec.Cmd, chan string, chan error) {
	outputChan := make(chan string, 100) // Buffer to prevent blocking
	errChan := make(chan error, 1)

	// Set working directory
	workDir := action.Cwd
	if workDir == "" {
		workDir = e.workingDir
	}

	// Create the command with proper process group for clean termination
	cmd := exec.CommandContext(ctx, "sh", "-c", action.Command)
	cmd.Dir = workDir
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true, // Create a new process group for clean termination
	}

	// Create pipes for stdout and stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		e.logger.Errorf("Failed to create stdout pipe: %v", err)
		close(outputChan)
		errChan <- fmt.Errorf("failed to create stdout pipe: %w", err)
		close(errChan)
		return nil, outputChan, errChan
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		e.logger.Errorf("Failed to create stderr pipe: %v", err)
		close(outputChan)
		errChan <- fmt.Errorf("failed to create stderr pipe: %w", err)
		close(errChan)
		return nil, outputChan, errChan
	}

	// Start command
	if err := cmd.Start(); err != nil {
		e.logger.Errorf("Failed to start command: %v", err)
		close(outputChan)
		errChan <- fmt.Errorf("failed to start command: %w", err)
		close(errChan)
		return nil, outputChan, errChan
	}

	// Start goroutines to stream output
	var wg sync.WaitGroup
	wg.Add(2)

	// Stream stdout
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			outputChan <- line + "\n"
		}
		if err := scanner.Err(); err != nil && err != io.EOF && !strings.Contains(err.Error(), "file already closed") {
			e.logger.Warnf("Error reading stdout: %v", err)
		}
	}()

	// Stream stderr
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			outputChan <- line + "\n"
		}
		if err := scanner.Err(); err != nil && err != io.EOF && !strings.Contains(err.Error(), "file already closed") {
			e.logger.Warnf("Error reading stderr: %v", err)
		}
	}()

	// Wait for both streams to complete, then close channels
	go func() {
		wg.Wait()
		close(outputChan)

		// Wait for the command to finish and send any error
		err := cmd.Wait()
		errChan <- err
		close(errChan)
	}()

	return cmd, outputChan, errChan
}

// executeCommandWithStreaming executes a command with streaming output and handles termination
func (e *Executor) executeCommandWithStreaming(cmd *exec.Cmd, outputChan chan string, errChan chan error, done <-chan struct{}) (string, int) {
	var output strings.Builder
	output.Grow(4096) // Preallocate 4KB

	// Process output until both streams are closed
	for {
		select {
		case line, ok := <-outputChan:
			if !ok {
				// Channel closed, all output received
				outputChan = nil
				if errChan == nil {
					// Both channels are closed, we're done
					goto processExitCode
				}
				continue
			}
			output.WriteString(line)
		case err, ok := <-errChan:
			if !ok {
				// Error channel closed
				errChan = nil
				if outputChan == nil {
					// Both channels are closed, we're done
					goto processExitCode
				}
				continue
			}
			if err != nil {
				e.logger.Debugf("Command error: %v", err)
			}
		case <-done:
			// Context cancelled or timed out
			e.killProcess(cmd)
			return output.String(), -1
		}
	}

processExitCode:
	exitCode := 0
	if cmd.ProcessState == nil {
		e.logger.Warn("Command process state is nil")
		return output.String(), -1
	}

	if !cmd.ProcessState.Success() {
		if status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok {
			exitCode = status.ExitStatus()
		} else {
			exitCode = 1 // Generic error
		}
	}

	return output.String(), exitCode
}

// killProcess forcibly terminates a process and its children
func (e *Executor) killProcess(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}

	// Try to kill the entire process group
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err == nil {
		e.logger.Infof("Killing process group %d", pgid)
		if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil {
			e.logger.Warnf("Failed to kill process group: %v", err)
		}
	}

	// Also try to kill just this process as a fallback
	if err := cmd.Process.Kill(); err != nil {
		e.logger.Warnf("Failed to kill process: %v", err)
	}
}
