package executor

import (
	"fmt"
	"path/filepath"
	"strings"
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

// SecurityCheck performs security validation on file paths
func (e *Executor) SecurityCheck(path string) error {
	// Check for path traversal attacks
	if strings.Contains(path, "..") {
		return fmt.Errorf("path traversal detected: %s", path)
	}

	// Check for absolute paths outside workspace
	if filepath.IsAbs(path) && !strings.HasPrefix(path, e.workingDir) {
		return fmt.Errorf("access denied: path outside workspace: %s", path)
	}

	// Check for suspicious patterns
	suspiciousPatterns := []string{"/etc/", "/proc/", "/sys/", "/dev/"}
	for _, pattern := range suspiciousPatterns {
		if strings.Contains(path, pattern) {
			return fmt.Errorf("access denied: suspicious path pattern: %s", path)
		}
	}

	return nil
}

// sanitizeCommand performs basic command sanitization
func (e *Executor) sanitizeCommand(command string) error {
	// Check for dangerous command patterns
	dangerousPatterns := []string{
		"rm -rf /",
		"sudo rm",
		"chmod -R 777",
		"dd if=",
		":(){ :|: & };:", // Fork bomb
		"mkfs.",
		"fdisk",
	}

	lowerCmd := strings.ToLower(command)
	for _, pattern := range dangerousPatterns {
		if strings.Contains(lowerCmd, pattern) {
			return fmt.Errorf("potentially dangerous command detected: %s", pattern)
		}
	}

	return nil
}
