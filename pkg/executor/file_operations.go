package executor

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/denysvitali/openhands-runtime-go/internal/models"
)

// readFileInitialChunk reads the first chunk (up to 1024 bytes) of a file.
// It is used to perform initial checks, such as binary detection, without reading the entire file.
func (e *Executor) readFileInitialChunk(path string) ([]byte, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to open file %s: %w", path, err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			e.logger.Warnf("Failed to close file %s: %v", path, closeErr)
		}
	}()

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
