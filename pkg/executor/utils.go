package executor

import (
	"os"
	"path/filepath"
)

// resolvePath resolves a path relative to the working directory
func (e *Executor) resolvePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(e.workingDir, path)
}

// toRelativePath converts an absolute path to a path relative to the working directory
func (e *Executor) toRelativePath(path string) string {
	relPath, err := filepath.Rel(e.workingDir, path)
	if err != nil {
		return path
	}
	return relPath
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
