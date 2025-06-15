package mcp

import (
	"encoding/json"
	"fmt"

	"github.com/denysvitali/openhands-runtime-go/internal/models"
	"github.com/sirupsen/logrus"
)

// MCPProtocolHandler handles MCP protocol messages
type MCPProtocolHandler struct {
	logger *logrus.Logger
}

// NewMCPProtocolHandler creates a new MCP protocol handler
func NewMCPProtocolHandler(logger *logrus.Logger) *MCPProtocolHandler {
	return &MCPProtocolHandler{
		logger: logger,
	}
}

// HandleJSONRPCMessage processes incoming JSON-RPC messages
func (h *MCPProtocolHandler) HandleJSONRPCMessage(conn *MCPConnection, data []byte) error {
	var message models.JSONRPCMessage[json.RawMessage]
	if err := json.Unmarshal(data, &message); err != nil {
		h.logger.Errorf("Failed to unmarshal JSON-RPC message: %v", err)
		return h.sendErrorResponse(conn, nil, -32700, "Parse error", nil)
	}

	h.logger.Debugf("Received JSON-RPC message: method=%s, id=%v", message.Method, message.ID)

	switch message.Method {
	case "initialize":
		return h.handleInitialize(conn, &message)
	case "tools/list":
		return h.handleListTools(conn, &message)
	case "tools/call":
		return h.handleCallTool(conn, &message)
	case "ping":
		return h.handlePing(conn, &message)
	default:
		return h.sendErrorResponse(conn, message.ID, -32601, "Method not found", nil)
	}
}

// handleInitialize handles the MCP initialize request
func (h *MCPProtocolHandler) handleInitialize(conn *MCPConnection, message *models.JSONRPCMessage[json.RawMessage]) error {
	// MCP initialize response
	initResult := map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]interface{}{
			"tools": map[string]interface{}{},
		},
		"serverInfo": map[string]interface{}{
			"name":    "openhands-runtime-go",
			"version": "1.0.0",
		},
	}

	response := models.JSONRPCMessage[map[string]interface{}]{
		JSONRPC: "2.0",
		ID:      message.ID,
		Result:  &initResult,
	}

	return conn.SendMessage(response)
}

// handleListTools handles the MCP tools/list request
func (h *MCPProtocolHandler) handleListTools(conn *MCPConnection, message *models.JSONRPCMessage[json.RawMessage]) error {
	// For now, return an empty list of tools
	// In a full implementation, this would return the actual available tools
	tools := map[string]interface{}{
		"tools": []interface{}{},
	}

	response := models.JSONRPCMessage[map[string]interface{}]{
		JSONRPC: "2.0",
		ID:      message.ID,
		Result:  &tools,
	}

	return conn.SendMessage(response)
}

// handleCallTool handles the MCP tools/call request
func (h *MCPProtocolHandler) handleCallTool(conn *MCPConnection, message *models.JSONRPCMessage[json.RawMessage]) error {
	// Parse the call parameters
	var callParams struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}

	if message.Params != nil {
		if err := json.Unmarshal(*message.Params, &callParams); err != nil {
			return h.sendErrorResponse(conn, message.ID, -32602, "Invalid params", nil)
		}
	}

	h.logger.Infof("MCP tool call: %s with args %v", callParams.Name, callParams.Arguments)

	// For now, return a placeholder response
	// In a full implementation, this would execute the actual tool
	result := map[string]interface{}{
		"content": []map[string]interface{}{
			{
				"type": "text",
				"text": fmt.Sprintf("Tool %s called with arguments: %v", callParams.Name, callParams.Arguments),
			},
		},
	}

	response := models.JSONRPCMessage[map[string]interface{}]{
		JSONRPC: "2.0",
		ID:      message.ID,
		Result:  &result,
	}

	return conn.SendMessage(response)
}

// handlePing handles ping requests
func (h *MCPProtocolHandler) handlePing(conn *MCPConnection, message *models.JSONRPCMessage[json.RawMessage]) error {
	conn.UpdateHeartbeat()

	// If it's a request (has ID), send a response
	if message.ID != nil {
		response := models.JSONRPCMessage[map[string]interface{}]{
			JSONRPC: "2.0",
			ID:      message.ID,
			Result:  &map[string]interface{}{"pong": true},
		}
		return conn.SendMessage(response)
	}

	// If it's a notification (no ID), just acknowledge
	return nil
}

// sendErrorResponse sends a JSON-RPC error response
func (h *MCPProtocolHandler) sendErrorResponse(conn *MCPConnection, id *int, code int, message string, data interface{}) error {
	errorResponse := models.JSONRPCMessage[interface{}]{
		JSONRPC: "2.0",
		ID:      id,
		Error: &models.JSONRPCError{
			Code:    code,
			Message: message,
			Data:    data,
		},
	}

	return conn.SendMessage(errorResponse)
}
