package models

// FileInfo represents basic information about a file
type FileInfo struct {
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
}

// ListFilesRequest represents the request to list files
type ListFilesRequest struct {
	Path      string `json:"path"`
	Recursive bool   `json:"recursive"`
}
