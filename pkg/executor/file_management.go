package executor

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.opentelemetry.io/otel/attribute"

	"github.com/denysvitali/openhands-runtime-go/internal/models"
)

// validatePathSecurity checks for directory traversal attacks and other security issues
func (e *Executor) validatePathSecurity(path string) error {
	// TODO: Implement something meaningful considering that the runtime environment is already sandboxed
	return nil
}

// ListFiles lists files in a directory
func (e *Executor) ListFiles(ctx context.Context, path string, recursive bool) ([]models.FileInfo, error) {
	_, span := e.tracer.Start(ctx, "list_files")
	defer span.End()

	span.SetAttributes(
		attribute.String("path", path),
		attribute.Bool("recursive", recursive),
	)

	if err := e.validatePathSecurity(path); err != nil {
		span.RecordError(err)
		return nil, err
	}

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

	if err := e.validatePathSecurity(path); err != nil {
		span.RecordError(err)
		return nil, err
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

	if err := e.validatePathSecurity(path); err != nil {
		span.RecordError(err)
		return err
	}

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

	if err := e.validatePathSecurity(path); err != nil {
		span.RecordError(err)
		return nil, err
	}

	resolvedPath := e.resolvePath(path)

	content, err := os.ReadFile(resolvedPath)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	return content, nil
}

// StreamZipArchive creates a zip archive of the specified path and streams it to the writer
func (e *Executor) StreamZipArchive(ctx context.Context, path string, writer io.Writer) error {
	_, span := e.tracer.Start(ctx, "stream_zip_archive")
	defer span.End()

	span.SetAttributes(attribute.String("path", path))

	if err := e.validatePathSecurity(path); err != nil {
		span.RecordError(err)
		return err
	}

	// Create a new zip writer that writes directly to the provided writer
	zipWriter := zip.NewWriter(writer)
	defer func() {
		if err := zipWriter.Close(); err != nil {
			span.RecordError(fmt.Errorf("failed to close zip writer: %w", err))
		}
	}()

	// Walk through the directory/file and add to zip
	err := filepath.Walk(path, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Create a relative path for the archive
		relativePath, err := filepath.Rel(path, filePath)
		if err != nil {
			return err
		}

		// Skip the root directory entry
		if relativePath == "." {
			return nil
		}

		// Create a file header
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}

		// Update the header name to use the relative path
		header.Name = relativePath

		// Set compression method
		header.Method = zip.Deflate

		// Set modification time
		header.Modified = info.ModTime()

		// If it's a directory, add trailing slash
		if info.IsDir() {
			header.Name += "/"
			// Create directory entry
			_, err := zipWriter.CreateHeader(header)
			return err
		}

		// Create file entry
		zipFileWriter, err := zipWriter.CreateHeader(header)
		if err != nil {
			return err
		}

		// Open the file to copy its contents
		file, err := os.Open(filePath)
		if err != nil {
			return err
		}
		defer func() {
			if closeErr := file.Close(); closeErr != nil {
				// Log error but don't override the main error
				span.RecordError(fmt.Errorf("failed to close file %s: %w", filePath, closeErr))
			}
		}()

		// Copy file contents to zip
		_, err = io.Copy(zipFileWriter, file)
		return err
	})

	if err != nil {
		span.RecordError(err)
		return err
	}

	return nil
}

// StreamZipArchiveMultiple creates a zip archive from multiple paths and streams it to the writer
func (e *Executor) StreamZipArchiveMultiple(ctx context.Context, paths []string, writer io.Writer) error {
	_, span := e.tracer.Start(ctx, "stream_zip_archive_multiple")
	defer span.End()

	span.SetAttributes(attribute.StringSlice("paths", paths))

	// Create a new zip writer that writes directly to the provided writer
	zipWriter := zip.NewWriter(writer)
	defer func() {
		if err := zipWriter.Close(); err != nil {
			span.RecordError(fmt.Errorf("failed to close zip writer: %w", err))
		}
	}()

	// Process each path
	for _, path := range paths {
		if err := e.validatePathSecurity(path); err != nil {
			span.RecordError(err)
			return err
		}

		// Get the base name for this path to avoid conflicts
		baseName := filepath.Base(path)

		// Walk through each path and add to zip
		err := filepath.Walk(path, func(filePath string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			// Create a relative path for the archive
			relativePath, err := filepath.Rel(path, filePath)
			if err != nil {
				return err
			}

			// Skip the root directory entry
			if relativePath == "." {
				// For the root, use the base name instead
				if info.IsDir() {
					header := &zip.FileHeader{
						Name:     baseName + "/",
						Modified: info.ModTime(),
					}
					_, err := zipWriter.CreateHeader(header)
					return err
				}
				// For single file, use the base name
				relativePath = baseName
			} else {
				// Prefix with the base name to avoid conflicts
				relativePath = filepath.Join(baseName, relativePath)
			}

			// Create a file header
			header, err := zip.FileInfoHeader(info)
			if err != nil {
				return err
			}

			// Update the header name to use the relative path
			header.Name = filepath.ToSlash(relativePath) // Use forward slashes in zip paths

			// Set compression method
			header.Method = zip.Deflate

			// Set modification time
			header.Modified = info.ModTime()

			// If it's a directory, add trailing slash
			if info.IsDir() {
				if !strings.HasSuffix(header.Name, "/") {
					header.Name += "/"
				}
				// Create directory entry
				_, err := zipWriter.CreateHeader(header)
				return err
			}

			// Create file entry
			zipFileWriter, err := zipWriter.CreateHeader(header)
			if err != nil {
				return err
			}

			// Open the file to copy its contents
			file, err := os.Open(filePath)
			if err != nil {
				return err
			}
			defer func() {
				if closeErr := file.Close(); closeErr != nil {
					// Log error but don't override the main error
					span.RecordError(fmt.Errorf("failed to close file %s: %w", filePath, closeErr))
				}
			}()

			// Copy file contents to zip
			_, err = io.Copy(zipFileWriter, file)
			return err
		})

		if err != nil {
			span.RecordError(err)
			return err
		}
	}

	return nil
}
