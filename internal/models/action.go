package models

import (
	"encoding/json"
	"errors" // Added for errors.New
	"fmt"    // Added for fmt.Errorf
	"time"
)

// ActionRequest represents an incoming action request
type ActionRequest struct {
	Action map[string]interface{} `json:"action" binding:"required"`
}

// Action represents a base action
type Action struct {
	Action    string                 `json:"action"`
	Timestamp time.Time              `json:"timestamp,omitempty"`
	Args      map[string]interface{} `json:"args,omitempty"`
}

// CmdRunAction represents a command execution action
type CmdRunAction struct {
	Action      string `json:"action"`
	Command     string `json:"command"`
	Cwd         string `json:"cwd,omitempty"`
	IsStatic    bool   `json:"is_static,omitempty"`
	HardTimeout int    `json:"hard_timeout,omitempty"`
}

// FileReadAction represents a file read action
type FileReadAction struct {
	Action string `json:"action"`
	Path   string `json:"path"`
	Start  int    `json:"start,omitempty"`
	End    int    `json:"end,omitempty"`
}

// FileWriteAction represents a file write action
type FileWriteAction struct {
	Action   string `json:"action"`
	Path     string `json:"path"`
	Contents string `json:"contents"`
}

// FileEditAction represents a file edit action
type FileEditAction struct {
	Action     string `json:"action"`
	Path       string `json:"path"`
	Command    string `json:"command"`
	FileText   string `json:"file_text,omitempty"`
	ViewRange  []int  `json:"view_range,omitempty"`
	OldStr     string `json:"old_str,omitempty"`
	NewStr     string `json:"new_str,omitempty"`
	InsertLine int    `json:"insert_line,omitempty"`
}

// IPythonRunCellAction represents an IPython cell execution action
type IPythonRunCellAction struct {
	Action         string `json:"action"`
	Code           string `json:"code"`
	Thought        string `json:"thought,omitempty"`
	IncludeExtra   bool   `json:"include_extra,omitempty"`
	KernelInitCode string `json:"kernel_init_code,omitempty"`
}

// BrowseURLAction represents a browser URL navigation action
type BrowseURLAction struct {
	Action string `json:"action"`
	URL    string `json:"url"`
}

// BrowseInteractiveAction represents a browser interaction action
type BrowseInteractiveAction struct {
	Action           string `json:"action"`
	BrowserID        string `json:"browser_id"`
	Coordinate       []int  `json:"coordinate,omitempty"`
	Text             string `json:"text,omitempty"`
	ElementID        string `json:"element_id,omitempty"`
	ScrollDirection  string `json:"scroll_direction,omitempty"`
	WaitBeforeAction int    `json:"wait_before_action,omitempty"`
}

// genericUnmarshalAction is a helper function to unmarshal JSON data into a specific action type.
// It is unexported as it's intended for use only within this package.
func genericUnmarshalAction[T any](jsonData []byte) (T, error) {
	var action T
	if err := json.Unmarshal(jsonData, &action); err != nil {
		// Return zero value of T and the error
		var zero T
		return zero, fmt.Errorf("failed to unmarshal json to %T: %w", zero, err)
	}
	return action, nil
}

// ParseAction parses a generic action map into a specific action type
func ParseAction(actionMap map[string]interface{}) (interface{}, error) {
	actionTypeVal, ok := actionMap["action"]
	if !ok {
		return nil, errors.New("action map is missing 'action' field")
	}

	actionType, ok := actionTypeVal.(string)
	if !ok {
		return nil, fmt.Errorf("'action' field is not a string, got %T", actionTypeVal)
	}

	// Convert map to JSON to leverage struct tags for unmarshalling
	jsonData, err := json.Marshal(actionMap)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal actionMap to JSON: %w", err)
	}

	switch actionType {
	case "run":
		return genericUnmarshalAction[CmdRunAction](jsonData)
	case "read":
		return genericUnmarshalAction[FileReadAction](jsonData)
	case "write":
		return genericUnmarshalAction[FileWriteAction](jsonData)
	case "edit": // Changed from "str_replace_editor"
		return genericUnmarshalAction[FileEditAction](jsonData)
	case "run_ipython":
		return genericUnmarshalAction[IPythonRunCellAction](jsonData)
	case "browse":
		return genericUnmarshalAction[BrowseURLAction](jsonData)
	case "browse_interactive":
		return genericUnmarshalAction[BrowseInteractiveAction](jsonData)
	default:
		// For unknown action types, parse as a base Action struct.
		// The calling Executor will then handle it as an unsupported action.
		return genericUnmarshalAction[Action](jsonData)
	}
}
