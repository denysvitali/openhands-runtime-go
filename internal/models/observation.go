package models

import "time"

// Observation represents a base observation
type Observation struct {
	Observation string                 `json:"observation"`
	Content     string                 `json:"content"`
	Timestamp   time.Time              `json:"timestamp"`
	Extras      map[string]interface{} `json:"extras,omitempty"`
}

// CmdOutputObservation represents command execution output
type CmdOutputObservation struct {
	Observation string    `json:"observation"`
	Content     string    `json:"content"`
	Command     string    `json:"command"`
	ExitCode    int       `json:"exit_code"`
	Timestamp   time.Time `json:"timestamp"`
}

// FileReadObservation represents file read output
type FileReadObservation struct {
	Observation string    `json:"observation"`
	Content     string    `json:"content"`
	Path        string    `json:"path"`
	Timestamp   time.Time `json:"timestamp"`
}

// FileWriteObservation represents file write output
type FileWriteObservation struct {
	Observation string    `json:"observation"`
	Content     string    `json:"content"`
	Path        string    `json:"path"`
	Timestamp   time.Time `json:"timestamp"`
}

// FileEditObservation represents file edit output
type FileEditObservation struct {
	Observation string    `json:"observation"`
	Content     string    `json:"content"`
	Path        string    `json:"path"`
	Timestamp   time.Time `json:"timestamp"`
}

// IPythonRunCellObservation represents IPython execution output
type IPythonRunCellObservation struct {
	Observation string    `json:"observation"`
	Code        string    `json:"code"`
	Content     string    `json:"content"`
	ImageURLs   []string  `json:"image_urls,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
}

// ErrorObservation represents an error
type ErrorObservation struct {
	Observation string    `json:"observation"`
	Content     string    `json:"content"`
	Timestamp   time.Time `json:"timestamp"`
}

// BrowserObservation represents browser interaction output
type BrowserObservation struct {
	Observation string    `json:"observation"`
	Content     string    `json:"content"`
	URL         string    `json:"url,omitempty"`
	Screenshot  string    `json:"screenshot,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
}

// ServerInfo represents server information
type ServerInfo struct {
	RuntimeID     string      `json:"runtime_id"`
	StartTime     time.Time   `json:"start_time"`
	LastExecTime  time.Time   `json:"last_execution_time"`
	WorkingDir    string      `json:"working_directory"`
	Plugins       []string    `json:"plugins"`
	Username      string      `json:"username"`
	UserID        int         `json:"user_id"`
	FileViewerURL string      `json:"file_viewer_url"`
	VSCodeURL     string      `json:"vscode_url,omitempty"`
	JupyterURL    string      `json:"jupyter_url,omitempty"`
	SystemStats   SystemStats `json:"system_stats"`
}

// SystemStats represents system statistics
type SystemStats struct {
	CPUPercent    float64 `json:"cpu_percent"`
	MemoryPercent float64 `json:"memory_percent"`
	MemoryUsedMB  float64 `json:"memory_used_mb"`
	MemoryTotalMB float64 `json:"memory_total_mb"`
	DiskUsedMB    float64 `json:"disk_used_mb"`
	DiskTotalMB   float64 `json:"disk_total_mb"`
}

// UploadResponse represents file upload response
type UploadResponse struct {
	Message string `json:"message"`
	Path    string `json:"path"`
}

// VSCodeConnectionToken represents VSCode connection token
type VSCodeConnectionToken struct {
	Token string `json:"token"`
}
