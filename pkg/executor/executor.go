package executor

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/denysvitali/openhands-runtime-go/internal/models"
	"github.com/denysvitali/openhands-runtime-go/pkg/config"
)

// Executor handles action execution
type Executor struct {
	config       *config.Config
	logger       *logrus.Logger
	workingDir   string
	username     string
	userID       int
	startTime    time.Time
	lastExecTime time.Time
	mu           sync.RWMutex
	tracer       trace.Tracer
}

// New creates a new executor
func New(cfg *config.Config, logger *logrus.Logger) (*Executor, error) {
	executor := &Executor{
		config:       cfg,
		logger:       logger,
		workingDir:   cfg.Server.WorkingDir,
		username:     cfg.Server.Username,
		userID:       cfg.Server.UserID,
		startTime:    time.Now(),
		lastExecTime: time.Now(),
		tracer:       otel.Tracer("openhands-runtime"),
	}

	if err := executor.initWorkingDirectory(); err != nil {
		return nil, fmt.Errorf("failed to initialize executor working directory: %w", err)
	}

	if err := executor.initUser(); err != nil {
		logger.Warnf("Failed to initialize user: %v", err)
	}

	return executor, nil
}

// Close cleans up resources, including the persistent bash session
func (e *Executor) Close() error {
	return nil
}

// ExecuteAction executes an action and returns an observation
func (e *Executor) ExecuteAction(ctx context.Context, actionMap map[string]interface{}) (interface{}, error) {
	ctx, span := e.tracer.Start(ctx, "execute_action")
	defer span.End()

	e.mu.Lock()
	e.lastExecTime = time.Now()
	e.mu.Unlock()

	action, err := models.ParseAction(actionMap)
	if err != nil {
		span.RecordError(err)
		return models.ErrorObservation{
			Observation: "error",
			Content:     fmt.Sprintf("Failed to parse action: %v", err),
			ErrorType:   "ActionParsingError",
			Timestamp:   time.Now(),
		}, nil
	}

	if actionType, ok := actionMap["action"].(string); ok {
		span.SetAttributes(attribute.String("action.type", actionType))
	}

	switch a := action.(type) {
	case models.CmdRunAction:
		return e.executeCmdRun(ctx, a)
	case models.FileReadAction:
		return e.executeFileRead(ctx, a)
	case models.FileWriteAction:
		return e.executeFileWrite(ctx, a)
	case models.FileEditAction:
		return e.executeFileEdit(ctx, a)
	case models.IPythonRunCellAction:
		return e.executeIPython(ctx, a)
	case models.BrowseURLAction:
		return e.executeBrowseURL(ctx, a)
	case models.BrowseInteractiveAction:
		return e.executeBrowseInteractive(ctx, a)
	default:
		err := fmt.Errorf("unsupported action type: %T", action)
		span.RecordError(err)
		return models.ErrorObservation{
			Observation: "error",
			Content:     err.Error(),
			ErrorType:   "UnsupportedActionError",
			Timestamp:   time.Now(),
		}, nil
	}
}

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

	// Create the command
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
		return models.ErrorObservation{
			Observation: "error",
			Content:     fmt.Sprintf("Failed to create stdout pipe: %v", err),
			ErrorType:   "CommandExecutionError",
			Timestamp:   time.Now(),
		}, nil
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return models.ErrorObservation{
			Observation: "error",
			Content:     fmt.Sprintf("Failed to create stderr pipe: %v", err),
			ErrorType:   "CommandExecutionError",
			Timestamp:   time.Now(),
		}, nil
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		return models.ErrorObservation{
			Observation: "error",
			Content:     fmt.Sprintf("Failed to start command: %v", err),
			ErrorType:   "CommandExecutionError",
			Timestamp:   time.Now(),
		}, nil
	}

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
	err = cmd.Wait()

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
			if ctx.Err() == context.DeadlineExceeded {
				outputStr = "Command timed out"
			} else if ctx.Err() == context.Canceled {
				outputStr = "Command was cancelled"
			} else {
				outputStr = fmt.Sprintf("Command failed: %v", err)
			}
		}
	}

	return models.CmdOutputObservation{
		Observation: "run",
		Content:     outputStr,
		Timestamp:   time.Now(),
		Extras: map[string]interface{}{
			"command":   action.Command,
			"exit_code": exitCode,
			"cwd":       cmd.Dir,
		},
	}, nil
}
