package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/denysvitali/openhands-runtime-go/internal/models"
	"github.com/denysvitali/openhands-runtime-go/pkg/config"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
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

	// Initialize working directory
	if err := executor.initWorkingDirectory(); err != nil {
		return nil, fmt.Errorf("failed to initialize working directory: %w", err)
	}

	// Initialize user if needed
	if err := executor.initUser(); err != nil {
		logger.Warnf("Failed to initialize user: %v", err)
	}

	return executor, nil
}

// ExecuteAction executes an action and returns an observation
func (e *Executor) ExecuteAction(ctx context.Context, actionMap map[string]interface{}) (interface{}, error) {
	ctx, span := e.tracer.Start(ctx, "execute_action")
	defer span.End()

	e.mu.Lock()
	e.lastExecTime = time.Now()
	e.mu.Unlock()

	// Parse action
	action, err := models.ParseAction(actionMap)
	if err != nil {
		span.RecordError(err)
		return models.ErrorObservation{
			Observation: "error",
			Content:     fmt.Sprintf("Failed to parse action: %v", err),
			Timestamp:   time.Now(),
		}, nil
	}

	// Add action type to span
	if actionType, ok := actionMap["action"].(string); ok {
		span.SetAttributes(attribute.String("action.type", actionType))
	}

	// Execute based on action type
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
			Timestamp:   time.Now(),
		}, nil
	}
}

// executeCmdRun executes a command
func (e *Executor) executeCmdRun(ctx context.Context, action models.CmdRunAction) (interface{}, error) {
	ctx, span := e.tracer.Start(ctx, "cmd_run")
	defer span.End()

	span.SetAttributes(
		attribute.String("command", action.Command),
		attribute.String("cwd", action.Cwd),
		attribute.Bool("is_static", action.IsStatic),
	)

	// Determine working directory
	workDir := e.workingDir
	if action.Cwd != "" {
		if filepath.IsAbs(action.Cwd) {
			workDir = action.Cwd
		} else {
			workDir = filepath.Join(e.workingDir, action.Cwd)
		}
	}

	// Create command
	cmd := exec.CommandContext(ctx, "bash", "-c", action.Command)
	cmd.Dir = workDir

	// Set up timeout if specified
	if action.HardTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(action.HardTimeout)*time.Second)
		defer cancel()
		cmd = exec.CommandContext(ctx, "bash", "-c", action.Command)
		cmd.Dir = workDir
	}

	// Execute command
	output, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		} else {
			exitCode = 1
		}
	}

	span.SetAttributes(attribute.Int("exit_code", exitCode))

	return models.CmdOutputObservation{
		Observation: "run",
		Content:     string(output),
		Command:     action.Command,
		ExitCode:    exitCode,
		Timestamp:   time.Now(),
	}, nil
}

// executeFileRead reads a file
func (e *Executor) executeFileRead(ctx context.Context, action models.FileReadAction) (interface{}, error) {
	_, span := e.tracer.Start(ctx, "file_read")
	defer span.End()

	span.SetAttributes(attribute.String("path", action.Path))

	// Resolve path
	path := e.resolvePath(action.Path)

	// Read file
	content, err := os.ReadFile(path)
	if err != nil {
		span.RecordError(err)
		return models.ErrorObservation{
			Observation: "error",
			Content:     fmt.Sprintf("Failed to read file %s: %v", path, err),
			Timestamp:   time.Now(),
		}, nil
	}

	// Handle line range if specified
	contentStr := string(content)
	if action.Start > 0 || action.End > 0 {
		lines := strings.Split(contentStr, "\n")
		start := action.Start
		end := action.End
		if start < 1 {
			start = 1
		}
		if end < 1 || end > len(lines) {
			end = len(lines)
		}
		if start <= end {
			contentStr = strings.Join(lines[start-1:end], "\n")
		}
	}

	return models.FileReadObservation{
		Observation: "read",
		Content:     contentStr,
		Path:        action.Path,
		Timestamp:   time.Now(),
	}, nil
}

// executeFileWrite writes to a file
func (e *Executor) executeFileWrite(ctx context.Context, action models.FileWriteAction) (interface{}, error) {
	_, span := e.tracer.Start(ctx, "file_write")
	defer span.End()

	span.SetAttributes(attribute.String("path", action.Path))

	// Resolve path
	path := e.resolvePath(action.Path)

	// Create directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		span.RecordError(err)
		return models.ErrorObservation{
			Observation: "error",
			Content:     fmt.Sprintf("Failed to create directory for %s: %v", path, err),
			Timestamp:   time.Now(),
		}, nil
	}

	// Write file
	if err := os.WriteFile(path, []byte(action.Contents), 0644); err != nil {
		span.RecordError(err)
		return models.ErrorObservation{
			Observation: "error",
			Content:     fmt.Sprintf("Failed to write file %s: %v", path, err),
			Timestamp:   time.Now(),
		}, nil
	}

	return models.FileWriteObservation{
		Observation: "write",
		Content:     fmt.Sprintf("File written successfully to %s", action.Path),
		Path:        action.Path,
		Timestamp:   time.Now(),
	}, nil
}

// executeFileEdit performs file editing operations
func (e *Executor) executeFileEdit(ctx context.Context, action models.FileEditAction) (interface{}, error) {
	ctx, span := e.tracer.Start(ctx, "file_edit")
	defer span.End()

	span.SetAttributes(
		attribute.String("path", action.Path),
		attribute.String("command", action.Command),
	)

	// This is a simplified implementation
	// In a full implementation, you'd want to integrate with a proper editor
	path := e.resolvePath(action.Path)

	switch action.Command {
	case "view":
		return e.executeFileRead(ctx, models.FileReadAction{
			Action: "read",
			Path:   action.Path,
			Start:  0,
			End:    0,
		})
	case "create":
		return e.executeFileWrite(ctx, models.FileWriteAction{
			Action:   "write",
			Path:     action.Path,
			Contents: action.FileText,
		})
	case "str_replace":
		return e.executeStringReplace(ctx, path, action.OldStr, action.NewStr)
	default:
		return models.ErrorObservation{
			Observation: "error",
			Content:     fmt.Sprintf("Unsupported file edit command: %s", action.Command),
			Timestamp:   time.Now(),
		}, nil
	}
}

// executeStringReplace performs string replacement in a file
func (e *Executor) executeStringReplace(ctx context.Context, path, oldStr, newStr string) (interface{}, error) {
	// Read file
	content, err := os.ReadFile(path)
	if err != nil {
		return models.ErrorObservation{
			Observation: "error",
			Content:     fmt.Sprintf("Failed to read file %s: %v", path, err),
			Timestamp:   time.Now(),
		}, nil
	}

	// Replace string
	contentStr := string(content)
	newContent := strings.ReplaceAll(contentStr, oldStr, newStr)

	// Write back
	if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
		return models.ErrorObservation{
			Observation: "error",
			Content:     fmt.Sprintf("Failed to write file %s: %v", path, err),
			Timestamp:   time.Now(),
		}, nil
	}

	return models.FileEditObservation{
		Observation: "edit",
		Content:     "File edited successfully",
		Path:        path,
		Timestamp:   time.Now(),
	}, nil
}

// executeIPython executes IPython code (placeholder implementation)
func (e *Executor) executeIPython(ctx context.Context, action models.IPythonRunCellAction) (interface{}, error) {
	_, span := e.tracer.Start(ctx, "ipython_run")
	defer span.End()

	e.logger.Infof("Executing IPython with code: %s", action.Args.Code)

	// This is a placeholder - in a real implementation you'd integrate with Jupyter
	observation := models.IPythonRunCellObservation{
		Observation: "run_ipython",
		Content:     "IPython execution not implemented in Go runtime",
		Code:        action.Args.Code,
		Timestamp:   time.Now(),
	}

	e.logger.Infof("Created IPython observation: %+v", observation)
	return observation, nil
}

// executeBrowseURL navigates to a URL (placeholder implementation)
func (e *Executor) executeBrowseURL(ctx context.Context, action models.BrowseURLAction) (interface{}, error) {
	_, span := e.tracer.Start(ctx, "browse_url")
	defer span.End()

	// This is a placeholder - in a real implementation you'd integrate with a browser
	return models.BrowserObservation{
		Observation: "browse",
		Content:     "Browser navigation not implemented in Go runtime",
		URL:         action.URL,
		Timestamp:   time.Now(),
	}, nil
}

// executeBrowseInteractive performs browser interaction (placeholder implementation)
func (e *Executor) executeBrowseInteractive(ctx context.Context, action models.BrowseInteractiveAction) (interface{}, error) {
	_, span := e.tracer.Start(ctx, "browse_interactive")
	defer span.End()

	// This is a placeholder - in a real implementation you'd integrate with a browser
	return models.BrowserObservation{
		Observation: "browse",
		Content:     "Browser interaction not implemented in Go runtime",
		Timestamp:   time.Now(),
	}, nil
}

// GetServerInfo returns server information
func (e *Executor) GetServerInfo() models.ServerInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()

	return models.ServerInfo{
		RuntimeID:     "go-runtime",
		StartTime:     e.startTime,
		LastExecTime:  e.lastExecTime,
		WorkingDir:    e.workingDir,
		Plugins:       e.config.Server.Plugins,
		Username:      e.username,
		UserID:        e.userID,
		FileViewerURL: fmt.Sprintf("http://localhost:%d", e.config.Server.FileViewerPort),
		SystemStats:   e.getSystemStats(),
	}
}

// getSystemStats returns system statistics (simplified implementation)
func (e *Executor) getSystemStats() models.SystemStats {
	// This is a simplified implementation
	// In a real implementation, you'd use proper system monitoring libraries
	return models.SystemStats{
		CPUPercent:    0.0,
		MemoryPercent: 0.0,
		MemoryUsedMB:  0.0,
		MemoryTotalMB: 0.0,
		DiskUsedMB:    0.0,
		DiskTotalMB:   0.0,
	}
}

// resolvePath resolves a path relative to the working directory
func (e *Executor) resolvePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(e.workingDir, path)
}

// initWorkingDirectory initializes the working directory
func (e *Executor) initWorkingDirectory() error {
	// Ensure working directory exists
	if err := os.MkdirAll(e.workingDir, 0755); err != nil {
		return err
	}

	// Change to working directory
	if err := os.Chdir(e.workingDir); err != nil {
		return err
	}

	return nil
}

// initUser initializes the user (simplified implementation)
func (e *Executor) initUser() error {
	// This is a simplified implementation
	// In a real implementation, you might need to handle user switching
	currentUser, err := user.Current()
	if err != nil {
		return err
	}

	e.logger.Infof("Running as user: %s (UID: %s)", currentUser.Username, currentUser.Uid)
	return nil
}

// toRelativePath converts an absolute path to a path relative to the working directory
func (e *Executor) toRelativePath(path string) string {
	relPath, err := filepath.Rel(e.workingDir, path)
	if err != nil {
		// If there's an error (e.g., different volumes on Windows), return the original path
		return path
	}
	return relPath
}

// ListFiles lists files in a directory
func (e *Executor) ListFiles(ctx context.Context, path string, recursive bool) ([]models.FileInfo, error) {
	_, span := e.tracer.Start(ctx, "list_files")
	defer span.End()

	span.SetAttributes(
		attribute.String("path", path),
		attribute.Bool("recursive", recursive),
	)

	resolvedPath := e.resolvePath(path)
	var files []models.FileInfo

	if recursive {
		err := filepath.Walk(resolvedPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			files = append(files, models.FileInfo{
				Path:  e.toRelativePath(path),
				IsDir: info.IsDir(),
				Size:  info.Size(),
			})
			return nil
		})
		if err != nil {
			span.RecordError(err)
			return nil, err
		}
	} else {
		dirEntries, err := os.ReadDir(resolvedPath)
		if err != nil {
			span.RecordError(err)
			return nil, err
		}

		for _, entry := range dirEntries {
			info, err := entry.Info()
			if err != nil {
				span.RecordError(err)
				return nil, err
			}
			files = append(files, models.FileInfo{
				Path:  e.toRelativePath(filepath.Join(resolvedPath, entry.Name())),
				IsDir: entry.IsDir(),
				Size:  info.Size(),
			})
		}
	}

	return files, nil
}

// UploadFile handles file uploads
func (e *Executor) UploadFile(ctx context.Context, path string, content []byte) error {
	_, span := e.tracer.Start(ctx, "upload_file")
	defer span.End()

	span.SetAttributes(attribute.String("path", path))

	resolvedPath := e.resolvePath(path)

	// Create directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(resolvedPath), 0755); err != nil {
		span.RecordError(err)
		return err
	}

	// Write file
	if err := os.WriteFile(resolvedPath, content, 0644); err != nil {
		span.RecordError(err)
		return err
	}

	return nil
}

// DownloadFile handles file downloads
func (e *Executor) DownloadFile(ctx context.Context, path string) ([]byte, error) {
	_, span := e.tracer.Start(ctx, "download_file")
	defer span.End()

	span.SetAttributes(attribute.String("path", path))

	resolvedPath := e.resolvePath(path)

	// Read file
	content, err := os.ReadFile(resolvedPath)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	return content, nil
}

// getShell returns the appropriate shell for the current OS
//func getShell() (string, string) {
//	if runtime.GOOS == "windows" {
//		return "cmd", "/c"
//	}
//	return "bash", "-c"
//}
