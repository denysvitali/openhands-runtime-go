package executor

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
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

	// Bash session management
	bashCmd        *exec.Cmd
	bashStdin      io.WriteCloser
	bashStdout     *bufio.Reader
	bashStderr     *bufio.Reader
	bashMutex      sync.Mutex
	currentBashCwd string
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

	if err := executor.initBashSession(); err != nil {
		return nil, fmt.Errorf("failed to initialize bash session: %w", err)
	}

	return executor, nil
}

// Close cleans up resources, including the persistent bash session
func (e *Executor) Close() error {
	return e.closeBashSession()
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
