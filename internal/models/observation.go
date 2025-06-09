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
	Observation string                 `json:"observation"`
	Content     string                 `json:"content"`
	Timestamp   time.Time              `json:"timestamp"`
	Extras      map[string]interface{} `json:"extras"`
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
	Observation string                 `json:"observation"`
	Content     string                 `json:"content"`
	Timestamp   time.Time              `json:"timestamp"`
	Extras      map[string]interface{} `json:"extras,omitempty"`
}

// ErrorObservation represents an error
type ErrorObservation struct {
	Observation string                 `json:"observation"`
	Content     string                 `json:"content"`
	ErrorType   string                 `json:"error_type,omitempty"`
	Extras      map[string]interface{} `json:"extras,omitempty"`
	Timestamp   time.Time              `json:"timestamp"`
}

// BrowserObservation represents browser interaction output
type BrowserObservation struct {
	Observation string    `json:"observation"`
	Content     string    `json:"content"`
	URL         string    `json:"url,omitempty"`
	Screenshot  string    `json:"screenshot,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
}

// SystemResources represents system resource information from Python get_system_stats()
type SystemResources struct {
	CPUCount      int     `json:"cpu_count"`
	CPUPercent    float64 `json:"cpu_percent"`
	MemoryTotal   int64   `json:"memory_total"`
	MemoryUsed    int64   `json:"memory_used"`
	MemoryPercent float64 `json:"memory_percent"`
	DiskTotal     int64   `json:"disk_total"`
	DiskUsed      int64   `json:"disk_used"`
	DiskPercent   float64 `json:"disk_percent"`
}

// ServerInfoResponse represents the server info response that matches Python implementation
type ServerInfoResponse struct {
	Uptime    float64         `json:"uptime"`
	IdleTime  float64         `json:"idle_time"`
	Resources SystemResources `json:"resources"`
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

// SystemStats represents system statistics that match Python's get_system_stats output
type SystemStats struct {
	CPUPercent float64     `json:"cpu_percent"`
	Memory     MemoryStats `json:"memory"`
	Disk       DiskStats   `json:"disk"`
	IO         IOStats     `json:"io"`
}

// MemoryStats represents memory usage statistics
type MemoryStats struct {
	RSS     uint64  `json:"rss"`     // Resident Set Size in bytes
	VMS     uint64  `json:"vms"`     // Virtual Memory Size in bytes
	Percent float32 `json:"percent"` // Memory usage percentage
}

// DiskStats represents disk usage statistics
type DiskStats struct {
	Total   uint64  `json:"total"`   // Total disk space in bytes
	Used    uint64  `json:"used"`    // Used disk space in bytes
	Free    uint64  `json:"free"`    // Free disk space in bytes
	Percent float64 `json:"percent"` // Disk usage percentage
}

// IOStats represents I/O statistics
type IOStats struct {
	ReadBytes  uint64 `json:"read_bytes"`  // Total bytes read
	WriteBytes uint64 `json:"write_bytes"` // Total bytes written
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
