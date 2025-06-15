package models

// MCPUpdateResponse represents a response from the MCP server update.
// This matches the Python implementation response format.
type MCPUpdateResponse struct {
	Detail         string `json:"detail"`
	RouterErrorLog string `json:"router_error_log"`
}

// JSONRPCMessage represents a JSON-RPC 2.0 message
type JSONRPCMessage[T any] struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      *int          `json:"id,omitempty"`     // Optional for notifications
	Method  string        `json:"method,omitempty"` // Required for requests/notifications
	Params  *T            `json:"params,omitempty"` // Optional parameters
	Result  *T            `json:"result,omitempty"` // Required for successful responses
	Error   *JSONRPCError `json:"error,omitempty"`  // Required for error responses
}

// JSONRPCError represents a JSON-RPC 2.0 error object
type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// MCPConnectionParams represents connection status parameters
type MCPConnectionParams struct {
	Status    string `json:"status"`
	Timestamp int64  `json:"timestamp"`
}

// MCPActionParams represents action-related parameters
type MCPActionParams struct {
	ActionType string `json:"action_type"`
	ActionID   string `json:"action_id"`
	Status     string `json:"status"`
	Timestamp  int64  `json:"timestamp"`
}

// MCPHeartbeatParams represents heartbeat parameters
type MCPHeartbeatParams struct {
	Timestamp int64 `json:"timestamp"`
}

// Common JSON-RPC methods for MCP
const (
	MCPMethodConnection = "connection"
	MCPMethodAction     = "action"
	MCPMethodHeartbeat  = "heartbeat"
)
