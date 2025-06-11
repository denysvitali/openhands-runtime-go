package executor

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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

// handleMediaType checks if the file is a media file and handles it appropriately
func (e *Executor) handleMediaType(ctx context.Context, path string, action models.FileReadAction) (models.Observation[models.FileReadExtras], bool, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".gif" || ext == ".bmp" {
		// Read the image file
		imgData, err := os.ReadFile(path)
		if err != nil {
			return models.Observation[models.FileReadExtras]{}, true, err
		}

		// Encode to base64
		encoded := base64.StdEncoding.EncodeToString(imgData)

		// Determine mime type
		mimeType := ""
		switch ext {
		case ".png":
			mimeType = "image/png"
		case ".jpg", ".jpeg":
			mimeType = "image/jpeg"
		case ".gif":
			mimeType = "image/gif"
		case ".bmp":
			mimeType = "image/bmp"
		}

		// Format as data URL
		mediaContent := fmt.Sprintf("data:%s;base64,%s", mimeType, encoded)

		return models.NewFileReadObservation(mediaContent, action.Path), true, nil
	}
	return models.Observation[models.FileReadExtras]{}, false, nil
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
		return models.NewErrorObservation(errorMsg, "FileReadError"), nil
	}

	// Check if it's a directory
	if fileInfo.IsDir() {
		errorMsg := fmt.Sprintf("Path is a directory: %s. You can only read files", path)
		e.logger.Errorf(errorMsg)
		return models.NewErrorObservation(errorMsg, "FileReadError"), nil
	}

	// Handle media files (images, videos, PDFs)
	mediaObservation, isHandled, mediaErr := e.handleMediaType(ctx, path, action)
	if isHandled {
		if mediaErr != nil {
			span.RecordError(mediaErr)
			return models.NewErrorObservation(fmt.Sprintf("Error reading media file: %v", mediaErr), "FileReadError"), nil
		}
		return mediaObservation, nil
	}

	// Check if the file is binary (for non-media files)
	buffer, n, chunkReadErr := e.readFileInitialChunk(path)
	if chunkReadErr != nil {
		errorMsg := fmt.Sprintf("Error reading file %s: %v", path, chunkReadErr)
		e.logger.Errorf(errorMsg)
		span.RecordError(chunkReadErr)
		return models.NewErrorObservation(errorMsg, "FileReadError"), nil
	}

	if isChunkPotentiallyBinary(buffer, n) {
		e.logger.Warnf("Binary file detected: %s", path)
		span.SetAttributes(attribute.Bool("is_binary_file", true))
		return models.NewErrorObservation("ERROR_BINARY_FILE", "BinaryFileError"), nil
	}

	// Read the entire file
	content, err := os.ReadFile(path)
	if err != nil {
		errorMsg := fmt.Sprintf("Error reading file %s: %v", path, err)
		e.logger.Errorf(errorMsg)
		span.RecordError(err)
		return models.NewErrorObservation(errorMsg, "FileReadError"), nil
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
	return models.NewFileReadObservation(contentStr, action.Path), nil
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
		return models.NewErrorObservation(errorMsg, "FileWriteError"), nil
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
			return models.NewErrorObservation(errorMsg, "FileWriteError"), nil
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
		return models.NewErrorObservation(errorMsg, "FileWriteError"), nil
	}

	// Restore original permissions and ownership if the file existed before
	if fileExists {
		if chmodErr := os.Chmod(path, fileMode); chmodErr != nil {
			e.logger.Warnf("Failed to restore permissions for %s: %v", path, chmodErr)
		}

		// Here we would restore ownership (UID/GID) if implemented
	}

	e.logger.Infof("Successfully wrote to file: %s", path)
	return models.NewFileWriteObservation("", action.Path), nil
}

// executeFileCreate creates a new file and returns FileEditObservation
func (e *Executor) executeFileCreate(ctx context.Context, path, content string) (interface{}, error) {
	_, span := e.tracer.Start(ctx, "file_create")
	defer span.End()

	span.SetAttributes(attribute.String("path", path))

	resolvedPath := e.resolvePath(path)

	// Check if file already exists
	if _, err := os.Stat(resolvedPath); err == nil {
		return models.NewErrorObservation(fmt.Sprintf("File already exists: %s", path), "FileExistsError"), nil
	}

	// Create directory if needed
	if err := os.MkdirAll(filepath.Dir(resolvedPath), 0755); err != nil {
		span.RecordError(err)
		return models.NewErrorObservation(fmt.Sprintf("Failed to create directory for %s: %v", path, err), "DirectoryCreationError"), nil
	}

	// Write file
	if err := os.WriteFile(resolvedPath, []byte(content), 0644); err != nil {
		span.RecordError(err)
		return models.NewErrorObservation(fmt.Sprintf("Failed to create file %s: %v", path, err), "FileCreateError"), nil
	}

	return models.NewFileEditObservation(fmt.Sprintf("File created successfully at: %s", path), path, "", content, "oh_aci"), nil
}

// executeFileEdit performs file edits using different approaches based on the action command
func (e *Executor) executeFileEdit(ctx context.Context, action models.FileEditAction) (interface{}, error) {
	_, span := e.tracer.Start(ctx, "file_edit")
	defer span.End()

	span.SetAttributes(attribute.String("path", action.Path))
	span.SetAttributes(attribute.String("command", action.Command))

	path := e.resolvePath(action.Path)

	switch action.Command {
	case "view":
		// Remap to file read action
		return e.executeFileRead(ctx, models.FileReadAction{
			Action: "read",
			Path:   action.Path,
			Start:  0,
			End:    0,
		})
	case "create":
		// Create a new file with the provided content
		return e.executeFileCreate(ctx, action.Path, action.FileText)
	case "str_replace":
		if action.OldStr == "" || action.NewStr == "" {
			return models.NewErrorObservation("String replace requires non-empty old_str and new_str", "FileEditError"), nil
		}
		e.logger.Infof("Replacing string in %s", action.Path)
		return e.executeStringReplace(ctx, path, action.OldStr, action.NewStr)
	default:
		// Other edit commands could be implemented here
		return models.NewErrorObservation(fmt.Sprintf("Unsupported file edit command: %s", action.Command), "UnsupportedEditCommand"), nil
	}
}

// executeStringReplace implements string replacement in a file
func (e *Executor) executeStringReplace(ctx context.Context, path, oldStr, newStr string) (interface{}, error) {
	_, span := e.tracer.Start(ctx, "string_replace")
	defer span.End()

	resolvedPath := e.resolvePath(path)

	// Check if file exists
	if _, err := os.Stat(resolvedPath); os.IsNotExist(err) {
		return models.NewErrorObservation(fmt.Sprintf("File not found: %s", path), "FileEditError"), nil
	}

	// Read file content
	content, err := os.ReadFile(resolvedPath)
	if err != nil {
		span.RecordError(err)
		return models.NewErrorObservation(fmt.Sprintf("Failed to read file %s: %v", path, err), "FileEditError"), nil
	}

	oldContent := string(content)

	// Replace string
	newContent := strings.ReplaceAll(oldContent, oldStr, newStr)

	// Check if content changed
	if oldContent == newContent {
		return models.NewErrorObservation(fmt.Sprintf("String '%s' not found in %s", oldStr, path), "StringNotFound"), nil
	}

	// Write modified content back to file
	if err := os.WriteFile(resolvedPath, []byte(newContent), 0644); err != nil {
		span.RecordError(err)
		return models.NewErrorObservation(fmt.Sprintf("Failed to write changes to %s: %v", path, err), "FileEditError"), nil
	}

	editMsg := fmt.Sprintf("Successfully replaced '%s' with '%s' in %s", oldStr, newStr, path)
	e.logger.Infof(editMsg)

	return models.NewFileEditObservation(editMsg, path, oldContent, newContent, "str_replace"), nil
}
