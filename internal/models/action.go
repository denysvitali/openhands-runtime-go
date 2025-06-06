package models

import (
	"encoding/json"
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
	Action string `json:"action"`
	Code   string `json:"code"`
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

// ParseAction parses a generic action map into a specific action type
func ParseAction(actionMap map[string]interface{}) (interface{}, error) {
	actionType, ok := actionMap["action"].(string)
	if !ok {
		return nil, json.Unmarshal([]byte("{}"), &Action{})
	}

	// Convert map to JSON and then to specific struct
	jsonData, err := json.Marshal(actionMap)
	if err != nil {
		return nil, err
	}

	switch actionType {
	case "run":
		var action CmdRunAction
		err = json.Unmarshal(jsonData, &action)
		return action, err
	case "read":
		var action FileReadAction
		err = json.Unmarshal(jsonData, &action)
		return action, err
	case "write":
		var action FileWriteAction
		err = json.Unmarshal(jsonData, &action)
		return action, err
	case "str_replace_editor":
		var action FileEditAction
		err = json.Unmarshal(jsonData, &action)
		return action, err
	case "ipython":
		var action IPythonRunCellAction
		err = json.Unmarshal(jsonData, &action)
		return action, err
	case "browse":
		var action BrowseURLAction
		err = json.Unmarshal(jsonData, &action)
		return action, err
	case "browse_interactive":
		var action BrowseInteractiveAction
		err = json.Unmarshal(jsonData, &action)
		return action, err
	default:
		var action Action
		err = json.Unmarshal(jsonData, &action)
		return action, err
	}
}
