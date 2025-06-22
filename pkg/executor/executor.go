package executor

import (
        "context"
        "fmt"
        "os"
        "sync"
        "time"

        "github.com/sirupsen/logrus"
        "go.opentelemetry.io/otel"
        "go.opentelemetry.io/otel/attribute"
        "go.opentelemetry.io/otel/trace"

        "github.com/denysvitali/openhands-runtime-go/internal/models"
        "github.com/denysvitali/openhands-runtime-go/pkg/executor/tmux_session"
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
                tmuxSession  *tmux_session.TmuxSession
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

        // Initialize TmuxSession
        tmuxSession, err := tmux_session.NewTmuxSession("openhands-session", logger)
        if err != nil {
                return nil, fmt.Errorf("failed to create tmux session: %w", err)
        }
        executor.tmuxSession = tmuxSession

        return executor, nil
}

// initWorkingDirectory initializes the working directory
func (e *Executor) initWorkingDirectory() error {
        // Check if the working directory exists, create it if it doesn't
        if e.workingDir == "" {
                return fmt.Errorf("working directory is not specified")
        }

        // Create the directory if it doesn't exist
        if err := os.MkdirAll(e.workingDir, 0755); err != nil {
                return fmt.Errorf("failed to create working directory %s: %w", e.workingDir, err)
        }

        return nil
}

// initUser initializes the user for running commands
func (e *Executor) initUser() error {
        // No-op for now - in a more sophisticated implementation, this would
        // create the user if it doesn't exist or validate permissions
        return nil
}

// Close cleans up resources, including the persistent bash session
func (e *Executor) Close() error {
        e.mu.Lock()
        defer e.mu.Unlock()
        if e.tmuxSession != nil {
                e.tmuxSession.Close()
        }
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
                return models.NewErrorObservation(
                        fmt.Sprintf("Failed to parse action: %v", err),
                        "ActionParsingError",
                ), nil
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
                return models.NewErrorObservation(
                        err.Error(),
                        "UnsupportedActionError",
                ), nil
        }
}

// RunCommand executes a command and returns the result
// This is a simplified wrapper for MCP usage
func (e *Executor) RunCommand(command string) (*models.Observation[models.CmdOutputExtras], error) {
        ctx := context.Background()

        // Create a CmdRunAction
        action := models.CmdRunAction{
                Command: command,
                Cwd:     e.workingDir,
        }

        // Execute the action
        result, err := e.executeCmdRun(ctx, action)
        if err != nil {
                return nil, err
        }

        // Convert result to CmdOutputObservation
        if obs, ok := result.(models.Observation[models.CmdOutputExtras]); ok {
                return &obs, nil
        }

        return nil, fmt.Errorf("unexpected result type: %T", result)
}
