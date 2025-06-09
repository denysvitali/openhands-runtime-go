package executor

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.opentelemetry.io/otel/attribute"

	"github.com/denysvitali/openhands-runtime-go/internal/models"
)

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
