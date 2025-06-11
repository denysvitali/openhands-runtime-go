package models

import "time"

// Observation represents a base observation with generic extras
type Observation[T any] struct {
	Observation string    `json:"observation"`
	Content     string    `json:"content"`
	Timestamp   time.Time `json:"timestamp"`
	Extras      T         `json:"extras,omitempty"`
}

// BasicObservation is an observation with no specialized extras
type BasicObservation struct {
	Observation string                 `json:"observation"`
	Content     string                 `json:"content"`
	Timestamp   time.Time              `json:"timestamp"`
	Extras      map[string]interface{} `json:"extras,omitempty"`
}

// CmdOutputExtras contains extra fields for command output observations
type CmdOutputExtras struct {
	ExitCode  int    `json:"exit_code"`
	CommandID string `json:"command_id,omitempty"`
}

// FileReadExtras contains extra fields for file read observations
type FileReadExtras struct {
	Path string `json:"path"`
}

// FileWriteExtras contains extra fields for file write observations
type FileWriteExtras struct {
	Path string `json:"path"`
}

// FileEditExtras contains extra fields for file edit observations
type FileEditExtras struct {
	Path       string `json:"path"`
	OldContent string `json:"old_content,omitempty"`
	NewContent string `json:"new_content,omitempty"`
	ImplSource string `json:"impl_source,omitempty"`
}

// BrowserExtras contains extra fields for browser observations
type BrowserExtras struct {
	URL        string `json:"url,omitempty"`
	Screenshot string `json:"screenshot,omitempty"`
}

// ErrorExtras contains extra fields for error observations
type ErrorExtras struct {
	ErrorID string `json:"error_id,omitempty"`
}

// IPythonExtras contains extra fields for IPython observations
type IPythonExtras struct {
	// Any additional metadata for IPython observations
}

// NewCmdOutputObservation creates a new command execution output observation
func NewCmdOutputObservation(content string, exitCode int, commandID string) Observation[CmdOutputExtras] {
	return Observation[CmdOutputExtras]{
		Observation: "cmd_output",
		Content:     content,
		Timestamp:   time.Now(),
		Extras: CmdOutputExtras{
			ExitCode:  exitCode,
			CommandID: commandID,
		},
	}
}

// NewFileReadObservation creates a new file read observation
func NewFileReadObservation(content string, path string) Observation[FileReadExtras] {
	return Observation[FileReadExtras]{
		Observation: "read",
		Content:     content,
		Timestamp:   time.Now(),
		Extras: FileReadExtras{
			Path: path,
		},
	}
}

// NewFileWriteObservation creates a new file write observation
func NewFileWriteObservation(content string, path string) Observation[FileWriteExtras] {
	return Observation[FileWriteExtras]{
		Observation: "write",
		Content:     content,
		Timestamp:   time.Now(),
		Extras: FileWriteExtras{
			Path: path,
		},
	}
}

// NewFileEditObservation creates a new file edit observation
func NewFileEditObservation(content string, path string, oldContent string, newContent string, implSource string) Observation[FileEditExtras] {
	return Observation[FileEditExtras]{
		Observation: "edit",
		Content:     content,
		Timestamp:   time.Now(),
		Extras: FileEditExtras{
			Path:       path,
			OldContent: oldContent,
			NewContent: newContent,
			ImplSource: implSource,
		},
	}
}

// NewErrorObservation creates a new error observation
func NewErrorObservation(content string, errorID string) Observation[ErrorExtras] {
	return Observation[ErrorExtras]{
		Observation: "error",
		Content:     content,
		Timestamp:   time.Now(),
		Extras: ErrorExtras{
			ErrorID: errorID,
		},
	}
}

// NewBrowserObservation creates a new browser interaction output observation
func NewBrowserObservation(content string, url string, screenshot string) Observation[BrowserExtras] {
	return Observation[BrowserExtras]{
		Observation: "browser_output",
		Content:     content,
		Timestamp:   time.Now(),
		Extras: BrowserExtras{
			URL:        url,
			Screenshot: screenshot,
		},
	}
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

// NewIPythonRunCellObservation creates a new IPython cell execution output observation
func NewIPythonRunCellObservation(content string) Observation[IPythonExtras] {
	return Observation[IPythonExtras]{
		Observation: "ipython_output",
		Content:     content,
		Timestamp:   time.Now(),
		Extras:      IPythonExtras{},
	}
}
