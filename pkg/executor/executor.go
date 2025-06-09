package executor

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/process"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/denysvitali/openhands-runtime-go/internal/models"
	"github.com/denysvitali/openhands-runtime-go/pkg/config"
)

const (
	bashCommandTerminatorPrefix = "__OPENHANDS_COMMAND_DONE__"
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

	bashCmd        *exec.Cmd
	bashStdin      io.WriteCloser
	bashStdout     *bufio.Reader
	bashStderr     *bufio.Reader
	bashMutex      sync.Mutex
	currentBashCwd string
}

// QuotePathForBash quotes a path for safe use in a bash command, especially with cd.
// It handles paths with spaces and single quotes.
func QuotePathForBash(path string) string {
	return "'" + strings.ReplaceAll(path, "'", "'\\''") + "'"
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

	go func() {
		scanner := bufio.NewScanner(e.bashStdout)
		for scanner.Scan() {
			line := scanner.Text()
			e.logger.Debugf("Bash init stdout: %s", line)
			if strings.HasPrefix(line, bashCommandTerminatorPrefix) {
				fields := strings.Fields(strings.TrimPrefix(line, bashCommandTerminatorPrefix))
				if len(fields) >= 2 {
					e.currentBashCwd = strings.Join(fields[1:], " ")
					e.logger.Infof("Bash initial CWD set to: %s", e.currentBashCwd)
				}
				return
			}
		}
		if err := scanner.Err(); err != nil && err != io.EOF {
			e.logger.Warnf("Error reading bash init stdout: %v", err)
		}
	}()
	go func() {
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
	}()

	return nil
}

// Close cleans up resources, including the persistent bash session.
func (e *Executor) Close() error {
	e.bashMutex.Lock()
	defer e.bashMutex.Unlock()

	if e.bashCmd != nil {
		e.logger.Info("Closing persistent bash session...")
		if e.bashStdin != nil {
			_, err := e.bashStdin.Write([]byte("exit\n"))
			if err != nil {
				e.logger.Warnf("Failed to send exit command to bash: %v", err)
			}
			e.bashStdin.Close()
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

// executeCmdRun executes a command in the persistent bash session.
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
		if err := stdoutScanner.Err(); err != nil && err != io.EOF {
			errChan <- fmt.Errorf("stdout scan error: %w", err)
		}
	}()

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
		if err := stderrScanner.Err(); err != nil && err != io.EOF {
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

			if e.bashCmd != nil && e.bashCmd.Process != nil {
				e.logger.Info("Attempting to send SIGINT to bash process group...")
				if err := syscall.Kill(-e.bashCmd.Process.Pid, syscall.SIGINT); err != nil {
					e.logger.Errorf("Failed to send SIGINT to bash process group: %v. Trying individual process...", err)
					if sigErr := e.bashCmd.Process.Signal(os.Interrupt); sigErr != nil {
						e.logger.Errorf("Failed to send SIGINT to bash process: %v", sigErr)
						outputBuffer.WriteString(fmt.Sprintf("\n[Failed to send SIGINT: %v]\n", sigErr))
					}
				} else {
					e.logger.Info("SIGINT sent to bash process group.")
				}
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

// readFileInitialChunk reads the first chunk (up to 1024 bytes) of a file.
// It is used to perform initial checks, such as binary detection, without reading the entire file.
func (e *Executor) readFileInitialChunk(path string) ([]byte, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to open file %s: %w", path, err)
	}
	defer file.Close()

	buffer := make([]byte, 1024)
	n, readErr := file.Read(buffer)
	if readErr != nil && readErr != io.EOF {
		return nil, 0, fmt.Errorf("error reading file %s: %w", path, readErr)
	}
	return buffer, n, nil
}

// isChunkPotentiallyBinary checks if a given byte slice (chunk) is potentially binary.
// It does this by looking for non-printable ASCII characters, excluding tab, newline, and carriage return.
func isChunkPotentiallyBinary(chunk []byte, n int) bool {
	for i := 0; i < n; i++ {
		char := chunk[i]
		if char < 32 && char != '\t' && char != '\n' && char != '\r' {
			return true
		}
	}
	return false
}

// handleMediaType checks if the file at the given path is a known media type
// (e.g., PNG, JPG, PDF, MP4). If so, it reads the file, encodes its content
// as a base64 data URI, and returns a FileReadObservation.
// It returns true if the media type was handled, otherwise false.
func (e *Executor) handleMediaType(ctx context.Context, path string, action models.FileReadAction) (models.FileReadObservation, bool, error) {
	lowerPath := strings.ToLower(path)
	if strings.HasSuffix(lowerPath, ".png") || strings.HasSuffix(lowerPath, ".jpg") ||
		strings.HasSuffix(lowerPath, ".jpeg") || strings.HasSuffix(lowerPath, ".bmp") ||
		strings.HasSuffix(lowerPath, ".gif") || strings.HasSuffix(lowerPath, ".pdf") ||
		strings.HasSuffix(lowerPath, ".mp4") || strings.HasSuffix(lowerPath, ".webm") ||
		strings.HasSuffix(lowerPath, ".ogg") {

		fileData, err := os.ReadFile(path)
		if err != nil {
			return models.FileReadObservation{}, true, fmt.Errorf("failed to read media file %s: %w", path, err)
		}

		mimeType := mime.TypeByExtension(filepath.Ext(path))
		if mimeType == "" {
			if strings.HasSuffix(lowerPath, ".pdf") {
				mimeType = "application/pdf"
			} else if strings.HasSuffix(lowerPath, ".mp4") {
				mimeType = "video/mp4"
			} else {
				mimeType = "image/png"
			}
		}
		mediaContent := fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(fileData))
		return models.FileReadObservation{
			Observation: "read",
			Content:     mediaContent,
			Path:        action.Path,
			Timestamp:   time.Now(),
		}, true, nil
	}
	return models.FileReadObservation{}, false, nil
}

// executeFileRead reads a file
func (e *Executor) executeFileRead(ctx context.Context, action models.FileReadAction) (interface{}, error) {
	_, span := e.tracer.Start(ctx, "file_read")
	defer span.End()

	span.SetAttributes(attribute.String("path", action.Path))

	path := e.resolvePath(action.Path)

	cwd, _ := os.Getwd()

	mediaObservation, isHandled, mediaErr := e.handleMediaType(ctx, path, action)
	if mediaErr != nil {
		span.RecordError(mediaErr)
		return models.ErrorObservation{
			Observation: "error",
			Content:     fmt.Sprintf("File not found: %s. Your current working directory is %s.", path, cwd),
			ErrorType:   "FileReadError",
			Extras:      map[string]interface{}{"path": path},
			Timestamp:   time.Now(),
		}, nil
	}
	if isHandled {
		return mediaObservation, nil
	}

	buffer, n, chunkReadErr := e.readFileInitialChunk(path)
	if chunkReadErr != nil {
		content := fmt.Sprintf("Error reading file %s: %v", path, chunkReadErr)
		if os.IsNotExist(errors.Unwrap(chunkReadErr)) {
			content = fmt.Sprintf("File not found: %s. Your current working directory is %s.", path, cwd)
		} else if stat, statErr := os.Stat(path); statErr == nil && stat.IsDir() {
			content = fmt.Sprintf("Path is a directory: %s. You can only read files", path)
		}
		span.RecordError(chunkReadErr)
		return models.ErrorObservation{
			Observation: "error",
			Content:     content,
			ErrorType:   "FileReadError",
			Extras:      map[string]interface{}{"path": path},
			Timestamp:   time.Now(),
		}, nil
	}

	if isChunkPotentiallyBinary(buffer, n) {
		span.SetAttributes(attribute.Bool("is_binary_heuristic", true))
		return models.ErrorObservation{
			Observation: "error",
			Content:     "ERROR_BINARY_FILE",
			ErrorType:   "BinaryFileError",
			Extras:      map[string]interface{}{"path": path},
			Timestamp:   time.Now(),
		}, nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		span.RecordError(err)
		errorContent := fmt.Sprintf("File not found: %s.", path)
		if cwd != "" {
			errorContent = fmt.Sprintf("File not found: %s. Your current working directory is %s.", path, cwd)
		}
		if os.IsNotExist(err) {
		} else if _, ok := err.(*os.PathError); ok && strings.Contains(err.Error(), "is a directory") {
			errorContent = fmt.Sprintf("Path is a directory: %s. You can only read files", path)
		}

		return models.ErrorObservation{
			Observation: "error",
			Content:     errorContent,
			ErrorType:   "FileReadError",
			Extras:      map[string]interface{}{"path": path},
			Timestamp:   time.Now(),
		}, nil
	}

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

	path := e.resolvePath(action.Path)

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		span.RecordError(err)
		return models.ErrorObservation{
			Observation: "error",
			Content:     fmt.Sprintf("Failed to create directory for %s: %v", path, err),
			ErrorType:   "DirectoryCreationError",
			Extras:      map[string]interface{}{"path": path},
			Timestamp:   time.Now(),
		}, nil
	}

	if err := os.WriteFile(path, []byte(action.Contents), 0664); err != nil {
		span.RecordError(err)
		return models.ErrorObservation{
			Observation: "error",
			Content:     fmt.Sprintf("Failed to write file %s: %v", path, err),
			ErrorType:   "FileWriteError",
			Extras:      map[string]interface{}{"path": path},
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
			ErrorType:   "UnsupportedFileEditCommandError",
			Timestamp:   time.Now(),
		}, nil
	}
}

// executeStringReplace performs string replacement in a file
func (e *Executor) executeStringReplace(ctx context.Context, path, oldStr, newStr string) (interface{}, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return models.ErrorObservation{
			Observation: "error",
			Content:     fmt.Sprintf("Failed to read file %s: %v", path, err),
			ErrorType:   "FileReadError",
			Extras:      map[string]interface{}{"path": path},
			Timestamp:   time.Now(),
		}, nil
	}

	contentStr := string(content)
	newContent := strings.ReplaceAll(contentStr, oldStr, newStr)

	if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
		return models.ErrorObservation{
			Observation: "error",
			Content:     fmt.Sprintf("Failed to write file %s: %v", path, err),
			ErrorType:   "FileWriteError",
			Extras:      map[string]interface{}{"path": path},
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

// executeIPython executes IPython code using Python subprocess
func (e *Executor) executeIPython(ctx context.Context, action models.IPythonRunCellAction) (interface{}, error) {
	_, span := e.tracer.Start(ctx, "ipython_run")
	defer span.End()

	e.logger.Infof("Executing IPython with code: %s", action.Code)

	tmpFile, err := os.CreateTemp("", "ipython_*.py")
	if err != nil {
		return models.ErrorObservation{
			Observation: "error",
			Content:     fmt.Sprintf("Failed to create temporary file: %v", err),
			ErrorType:   "TempFileCreationError",
			Timestamp:   time.Now(),
		}, nil
	}
	defer func(name string) {
		_ = os.Remove(name)
	}(tmpFile.Name())

	wrapperCode := fmt.Sprintf(`
import sys
import io
import traceback
import json
import base64
import matplotlib
import matplotlib.pyplot as plt
from contextlib import redirect_stdout, redirect_stderr

matplotlib.use('Agg')

stdout_capture = io.StringIO()
stderr_capture = io.StringIO()
result = {"stdout": "", "stderr": "", "images": [], "error": False}

try:
    with redirect_stdout(stdout_capture), redirect_stderr(stderr_capture):
        exec('''%s''')
    
    if plt.get_fignums():
        import tempfile
        images = []
        for fig_num in plt.get_fignums():
            fig = plt.figure(fig_num)
            with tempfile.NamedTemporaryFile(suffix='.png', delete=False) as tmp:
                fig.savefig(tmp.name, format='png', bbox_inches='tight', dpi=150)
                with open(tmp.name, 'rb') as img_file:
                    img_data = base64.b64encode(img_file.read()).decode('utf-8')
                    images.append(f"data:image/png;base64,{img_data}")
                import os
                os.unlink(tmp.name)
            plt.close(fig)
        result["images"] = images
    
    result["stdout"] = stdout_capture.getvalue()
    result["stderr"] = stderr_capture.getvalue()
    
except Exception as e:
    result["error"] = True
    result["stderr"] = stderr_capture.getvalue() + "\n" + traceback.format_exc()
    result["stdout"] = stdout_capture.getvalue()

print("###IPYTHON_RESULT###")
print(json.dumps(result))
print("###IPYTHON_END###")
`, action.Code)

	if _, err := tmpFile.WriteString(wrapperCode); err != nil {
		_ = tmpFile.Close()
		return models.ErrorObservation{
			Observation: "error",
			Content:     fmt.Sprintf("Failed to write to temporary file: %v", err),
			ErrorType:   "TempFileWriteError",
			Timestamp:   time.Now(),
		}, nil
	}
	_ = tmpFile.Close()

	cmd := exec.CommandContext(ctx, "python3", tmpFile.Name())
	if e.workingDir != "" {
		cmd.Dir = e.workingDir
	}

	output, err := cmd.CombinedOutput()
	outputStr := string(output)

	e.logger.Infof("Python execution output: %s", outputStr)

	resultStartIdx := strings.Index(outputStr, "###IPYTHON_RESULT###")
	resultEndIdx := strings.Index(outputStr, "###IPYTHON_END###")

	var result struct {
		Stdout string   `json:"stdout"`
		Stderr string   `json:"stderr"`
		Images []string `json:"images"`
		Error  bool     `json:"error"`
	}

	var content string
	var imageURLs []string

	if resultStartIdx != -1 && resultEndIdx != -1 {
		jsonStr := outputStr[resultStartIdx+len("###IPYTHON_RESULT###") : resultEndIdx]
		jsonStr = strings.TrimSpace(jsonStr)

		if parseErr := json.Unmarshal([]byte(jsonStr), &result); parseErr == nil {
			content = result.Stdout
			if result.Stderr != "" {
				if content != "" {
					content += "\n"
				}
				content += result.Stderr
			}
			imageURLs = result.Images
		} else {
			content = outputStr
			e.logger.Warnf("Failed to parse IPython result JSON: %v", parseErr)
		}
	} else {
		content = outputStr
		if err != nil {
			content += fmt.Sprintf("\nError: %v", err)
		}
	}

	if action.IncludeExtra {
		if wd, wdErr := os.Getwd(); wdErr == nil {
			content += fmt.Sprintf("\n[Current working directory: %s]", wd)
		}
		if pythonPath, pathErr := exec.LookPath("python3"); pathErr == nil {
			content += fmt.Sprintf("\n[Python interpreter: %s]", pythonPath)
		}
	}

	observation := models.IPythonRunCellObservation{
		Observation: "run_ipython",
		Content:     content,
		Timestamp:   time.Now(),
		Extras: map[string]interface{}{
			"code": action.Code,
		},
	}

	if len(imageURLs) > 0 {
		observation.Extras["image_urls"] = imageURLs
	}

	e.logger.Infof("Created IPython observation: %+v", observation)
	return observation, nil
}

// executeBrowseURL navigates to a URL (placeholder implementation)
func (e *Executor) executeBrowseURL(ctx context.Context, action models.BrowseURLAction) (interface{}, error) {
	_, span := e.tracer.Start(ctx, "browse_url")
	defer span.End()

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
		SystemStats:   e.GetSystemStats(),
	}
}

// GetSystemStats returns system statistics using gopsutil
func (e *Executor) GetSystemStats() models.SystemStats {
	pid := int32(os.Getpid())
	proc, err := process.NewProcess(pid)
	if err != nil {
		e.logger.Warnf("Failed to get process info: %v", err)
		return models.SystemStats{
			CPUPercent: 0.0,
			Memory: models.MemoryStats{
				RSS:     0,
				VMS:     0,
				Percent: 0.0,
			},
			Disk: models.DiskStats{
				Total:   0,
				Used:    0,
				Free:    0,
				Percent: 0.0,
			},
			IO: models.IOStats{
				ReadBytes:  0,
				WriteBytes: 0,
			},
		}
	}

	cpuPercent, err := proc.CPUPercent()
	if err != nil {
		e.logger.Warnf("Failed to get CPU percent: %v", err)
		cpuPercent = 0.0
	}

	memInfo, err := proc.MemoryInfo()
	if err != nil {
		e.logger.Warnf("Failed to get memory info: %v", err)
		memInfo = &process.MemoryInfoStat{RSS: 0, VMS: 0}
	}

	memPercent, err := proc.MemoryPercent()
	if err != nil {
		e.logger.Warnf("Failed to get memory percent: %v", err)
		memPercent = 0.0
	}

	workingDir := e.workingDir
	if workingDir == "" {
		workingDir = "/"
	}
	diskUsage, err := disk.Usage(workingDir)
	if err != nil {
		e.logger.Warnf("Failed to get disk usage: %v", err)
		diskUsage = &disk.UsageStat{Total: 0, Used: 0, Free: 0, UsedPercent: 0.0}
	}

	ioCounters, err := proc.IOCounters()
	if err != nil {
		e.logger.Warnf("Failed to get IO counters: %v", err)
		ioCounters = &process.IOCountersStat{ReadBytes: 0, WriteBytes: 0}
	}

	return models.SystemStats{
		CPUPercent: cpuPercent,
		Memory: models.MemoryStats{
			RSS:     memInfo.RSS,
			VMS:     memInfo.VMS,
			Percent: memPercent,
		},
		Disk: models.DiskStats{
			Total:   diskUsage.Total,
			Used:    diskUsage.Used,
			Free:    diskUsage.Free,
			Percent: diskUsage.UsedPercent,
		},
		IO: models.IOStats{
			ReadBytes:  ioCounters.ReadBytes,
			WriteBytes: ioCounters.WriteBytes,
		},
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
	if err := os.MkdirAll(e.workingDir, 0755); err != nil {
		return err
	}

	if err := os.Chdir(e.workingDir); err != nil {
		return err
	}

	return nil
}

// initUser initializes the user (simplified implementation)
func (e *Executor) initUser() error {
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

// ListFileNames lists file names in a directory as strings (matching Python implementation)
func (e *Executor) ListFileNames(ctx context.Context, path string) ([]string, error) {
	_, span := e.tracer.Start(ctx, "list_file_names")
	defer span.End()

	span.SetAttributes(attribute.String("path", path))

	if path == "" {
		path = e.workingDir
	}

	resolvedPath := e.resolvePath(path)

	if _, err := os.Stat(resolvedPath); os.IsNotExist(err) {
		return []string{}, nil
	}

	dirEntries, err := os.ReadDir(resolvedPath)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	var directories []string
	var files []string

	for _, entry := range dirEntries {
		name := entry.Name()
		if entry.IsDir() {
			directories = append(directories, name+"/")
		} else {
			files = append(files, name)
		}
	}

	sort.Slice(directories, func(i, j int) bool {
		return strings.ToLower(directories[i]) < strings.ToLower(directories[j])
	})
	sort.Slice(files, func(i, j int) bool {
		return strings.ToLower(files[i]) < strings.ToLower(files[j])
	})

	result := append(directories, files...)
	return result, nil
}

// UploadFile handles file uploads
func (e *Executor) UploadFile(ctx context.Context, path string, content []byte) error {
	_, span := e.tracer.Start(ctx, "upload_file")
	defer span.End()

	span.SetAttributes(attribute.String("path", path))

	resolvedPath := e.resolvePath(path)

	if err := os.MkdirAll(filepath.Dir(resolvedPath), 0755); err != nil {
		span.RecordError(err)
		return err
	}

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

	content, err := os.ReadFile(resolvedPath)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	return content, nil
}
