package models

// MCPUpdateResponse represents a response from the MCP server update.
// This matches the Python implementation response format.
type MCPUpdateResponse struct {
	Detail         string `json:"detail"`
	RouterErrorLog string `json:"router_error_log"`
}
