package executor

import (
	"context"
	"encoding/base64"
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
	// Count the number of non-printable characters
	nonPrintableCount := 0
	totalCount := 0

	for i := 0; i < n; i++ {
		char := chunk[i]
		// Skip common non-printable characters that are acceptable in text files
		if char == '\t' || char == '\n' || char == '\r' {
			continue
		}

		totalCount++
		// Consider non-ASCII and control characters as indicators of binary content
		if char < 32 || char > 126 {
			nonPrintableCount++
		}
	}

	// If more than 10% of characters are non-printable (excluding tabs/newlines),
	// consider it a binary file
	if totalCount > 0 && float64(nonPrintableCount)/float64(totalCount) > 0.1 {
		return true
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
			// Instead of returning an empty FileReadObservation, return a zero value and let the caller handle the error
			return models.FileReadObservation{}, false, err
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
	e.logger.Infof("Reading file: %s", action.Path)

	path := e.resolvePath(action.Path)
	cwd, _ := os.Getwd()

	// Check if the file exists and is not a directory
	fileInfo, statErr := os.Stat(path)
	if statErr != nil {
		errorMsg := fmt.Sprintf("File not found: %s. Your current working directory is %s.", path, cwd)
		e.logger.Errorf(errorMsg)
		span.RecordError(statErr)
		return models.ErrorObservation{
			Observation: "error",
			Content:     errorMsg,
			ErrorType:   "FileReadError",
			Timestamp:   time.Now(),
		}, nil
	}

	// Check if it's a directory
	if fileInfo.IsDir() {
		errorMsg := fmt.Sprintf("Path is a directory: %s. You can only read files", path)
		e.logger.Errorf(errorMsg)
		return models.ErrorObservation{
			Observation: "error",
			Content:     errorMsg,
			ErrorType:   "FileReadError",
			Timestamp:   time.Now(),
		}, nil
	}

	// Handle media files (images, videos, PDFs)
	mediaObservation, isHandled, mediaErr := e.handleMediaType(ctx, path, action)
	if isHandled {
		if mediaErr != nil {
			span.RecordError(mediaErr)
			return models.ErrorObservation{
				Observation: "error",
				Content:     fmt.Sprintf("Error reading media file: %v", mediaErr),
				ErrorType:   "FileReadError",
				Timestamp:   time.Now(),
			}, nil
		}
		return mediaObservation, nil
	}

	// Check if the file is binary (for non-media files)
	buffer, n, chunkReadErr := e.readFileInitialChunk(path)
	if chunkReadErr != nil {
		errorMsg := fmt.Sprintf("Error reading file %s: %v", path, chunkReadErr)
		e.logger.Errorf(errorMsg)
		span.RecordError(chunkReadErr)
		return models.ErrorObservation{
			Observation: "error",
			Content:     errorMsg,
			ErrorType:   "FileReadError",
			Timestamp:   time.Now(),
		}, nil
	}

	if isChunkPotentiallyBinary(buffer, n) {
		e.logger.Warnf("Binary file detected: %s", path)
		span.SetAttributes(attribute.Bool("is_binary_file", true))
		return models.ErrorObservation{
			Observation: "error",
			Content:     "ERROR_BINARY_FILE",
			ErrorType:   "BinaryFileError",
			Timestamp:   time.Now(),
		}, nil
	}

	// Read the entire file
	content, err := os.ReadFile(path)
	if err != nil {
		errorMsg := fmt.Sprintf("Error reading file %s: %v", path, err)
		e.logger.Errorf(errorMsg)
		span.RecordError(err)
		return models.ErrorObservation{
			Observation: "error",
			Content:     errorMsg,
			ErrorType:   "FileReadError",
			Timestamp:   time.Now(),
		}, nil
	}

	// Convert to string and handle line ranges
	contentStr := string(content)
	if action.Start > 0 || action.End > 0 {
		lines := strings.Split(contentStr, "\n")
		start := action.Start
		end := action.End

		// Adjust bounds
		if start < 1 {
			start = 1
		}
		if end < 1 || end > len(lines) {
			end = len(lines)
		}

		// Extract the requested lines
		if start <= end && start <= len(lines) {
			if start > 1 {
				e.logger.Debugf("Reading lines %d-%d of %d total lines", start, end, len(lines))
			}
			contentStr = strings.Join(lines[start-1:end], "\n")
		} else {
			e.logger.Warnf("Invalid line range: start=%d, end=%d, total lines=%d", start, end, len(lines))
		}
	}

	e.logger.Debugf("Successfully read file: %s (%d bytes)", path, len(contentStr))
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
	e.logger.Infof("Writing to file: %s", action.Path)

	path := e.resolvePath(action.Path)

	// Create directories if they don't exist
	dirPath := filepath.Dir(path)
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		errorMsg := fmt.Sprintf("Failed to create directory %s: %v", dirPath, err)
		e.logger.Errorf(errorMsg)
		span.RecordError(err)
		return models.ErrorObservation{
			Observation: "error",
			Content:     errorMsg,
			ErrorType:   "FileWriteError",
			Timestamp:   time.Now(),
		}, nil
	}

	// Check if the file exists and get its permissions
	var fileMode os.FileMode = 0644
	fileExists := false

	if fileInfo, err := os.Stat(path); err == nil {
		fileExists = true
		fileMode = fileInfo.Mode().Perm()

		// Get file ownership info (UID/GID)
		// This requires syscall functions and varies by OS
		// For simplicity, we're skipping the ownership handling here
		// but it would be implemented with syscall.Stat_t on Linux
	}

	// Handle the different write modes
	var err error
	content := action.Contents

	if fileExists {
		// For existing files, we need to handle insert/replace logic
		_, readErr := os.ReadFile(path)
		if readErr != nil {
			errorMsg := fmt.Sprintf("Failed to read existing file %s for modification: %v", path, readErr)
			e.logger.Errorf(errorMsg)
			span.RecordError(readErr)
			return models.ErrorObservation{
				Observation: "error",
				Content:     errorMsg,
				ErrorType:   "FileWriteError",
				Timestamp:   time.Now(),
			}, nil
		}

		// Simple file overwrite
		// In a more complex implementation, we could add support for line-based
		// insertions and replacements using Start/End fields if added to the model
	}

	// Write the content to the file
	err = os.WriteFile(path, []byte(content), fileMode)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to write to file %s: %v", path, err)
		e.logger.Errorf(errorMsg)
		span.RecordError(err)
		return models.ErrorObservation{
			Observation: "error",
			Content:     errorMsg,
			ErrorType:   "FileWriteError",
			Timestamp:   time.Now(),
		}, nil
	}

	// Restore original permissions and ownership if the file existed before
	if fileExists {
		if chmodErr := os.Chmod(path, fileMode); chmodErr != nil {
			e.logger.Warnf("Failed to restore permissions for %s: %v", path, chmodErr)
		}

		// Here we would restore ownership (UID/GID) if implemented
	}

	e.logger.Infof("Successfully wrote to file: %s", path)
	return models.FileWriteObservation{
		Observation: "write",
		Content:     "",
		Path:        action.Path,
		Timestamp:   time.Now(),
	}, nil
}

// executeFileCreate creates a new file and returns FileEditObservation
func (e *Executor) executeFileCreate(ctx context.Context, path, content string) (interface{}, error) {
	_, span := e.tracer.Start(ctx, "file_create")
	defer span.End()

	span.SetAttributes(attribute.String("path", path))

	resolvedPath := e.resolvePath(path)

	// Check if file already exists
	if _, err := os.Stat(resolvedPath); err == nil {
		return models.ErrorObservation{
			Observation: "error",
			Content:     fmt.Sprintf("File already exists: %s", path),
			ErrorType:   "FileExistsError",
			Timestamp:   time.Now(),
		}, nil
	}

	// Create directory if needed
	if err := os.MkdirAll(filepath.Dir(resolvedPath), 0755); err != nil {
		span.RecordError(err)
		return models.ErrorObservation{
			Observation: "error",
			Content:     fmt.Sprintf("Failed to create directory for %s: %v", path, err),
			ErrorType:   "DirectoryCreationError",
			Timestamp:   time.Now(),
		}, nil
	}

	// Write file
	if err := os.WriteFile(resolvedPath, []byte(content), 0644); err != nil {
		span.RecordError(err)
		return models.ErrorObservation{
			Observation: "error",
			Content:     fmt.Sprintf("Failed to create file %s: %v", path, err),
			ErrorType:   "FileCreateError",
			Timestamp:   time.Now(),
		}, nil
	}

	return models.FileEditObservation{
		Observation: "edit",
		Content:     fmt.Sprintf("File created successfully at: %s", path),
		Timestamp:   time.Now(),
		Extras: map[string]interface{}{
			"path":        path,
			"prev_exist":  false,
			"old_content": "",
			"new_content": content,
			"impl_source": "oh_aci",
			"diff":        nil,
			"_diff_cache": nil,
		},
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
		return e.executeFileCreate(ctx, action.Path, action.FileText)
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
			Timestamp:   time.Now(),
		}, nil
	}

	return models.FileEditObservation{
		Observation: "edit",
		Content:     "File edited successfully",
		Timestamp:   time.Now(),
		Extras: map[string]interface{}{
			"path":        path,
			"prev_exist":  true,
			"old_content": contentStr,
			"new_content": newContent,
			"impl_source": "oh_aci",
			"diff":        nil,
			"_diff_cache": nil,
		},
	}, nil
}
