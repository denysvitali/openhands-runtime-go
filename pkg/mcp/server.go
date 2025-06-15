package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/sirupsen/logrus"

	"github.com/denysvitali/openhands-runtime-go/pkg/executor"
)

// Server wraps the mcp-go server with OpenHands-specific functionality
type Server struct {
	logger    *logrus.Logger
	executor  *executor.Executor
	mcpServer *server.MCPServer
}

// NewServer creates a new MCP server using the mcp-go library
func NewServer(logger *logrus.Logger, exec *executor.Executor) *Server {
	// Create MCP server with OpenHands tools
	mcpServer := server.NewMCPServer(
		"openhands-runtime",
		"1.0.0",
		server.WithToolCapabilities(false),
		server.WithRecovery(),
	)

	s := &Server{
		logger:    logger,
		executor:  exec,
		mcpServer: mcpServer,
	}

	// Register OpenHands-specific tools
	s.registerTools()

	return s
}

// registerTools registers OpenHands-specific MCP tools
func (s *Server) registerTools() {
	// File read tool
	fileReadTool := mcp.NewTool("file_read",
		mcp.WithDescription("Read the contents of a file"),
		mcp.WithString("path",
			mcp.Required(),
			mcp.Description("Path to the file to read"),
		),
	)
	s.mcpServer.AddTool(fileReadTool, s.handleFileRead)

	// File write tool
	fileWriteTool := mcp.NewTool("file_write",
		mcp.WithDescription("Write content to a file"),
		mcp.WithString("path",
			mcp.Required(),
			mcp.Description("Path to the file to write"),
		),
		mcp.WithString("content",
			mcp.Required(),
			mcp.Description("Content to write to the file"),
		),
	)
	s.mcpServer.AddTool(fileWriteTool, s.handleFileWrite)

	// Command execution tool
	cmdRunTool := mcp.NewTool("cmd_run",
		mcp.WithDescription("Execute a shell command"),
		mcp.WithString("command",
			mcp.Required(),
			mcp.Description("Command to execute"),
		),
	)
	s.mcpServer.AddTool(cmdRunTool, s.handleCmdRun)

	// List files tool
	listFilesTool := mcp.NewTool("list_files",
		mcp.WithDescription("List files in a directory"),
		mcp.WithString("path",
			mcp.Required(),
			mcp.Description("Path to the directory to list"),
		),
	)
	s.mcpServer.AddTool(listFilesTool, s.handleListFiles)
}

// HandleSSE handles MCP communication over Server-Sent Events using mcp-go library
func (s *Server) HandleSSE(c *gin.Context) {
	// Set SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Access-Control-Allow-Headers", "Cache-Control,Authorization,X-OpenHands-Conversation-ID")

	s.logger.Info("MCP SSE connection established")

	// For SSE, we need to implement a custom transport
	// The mcp-go library primarily supports stdio, so we'll create a simple wrapper
	// that handles JSON-RPC messages over SSE
	ctx := c.Request.Context()

	// Send initial connection message
	s.sendSSEMessage(c, map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "server/initialized",
		"params": map[string]interface{}{
			"server": map[string]interface{}{
				"name":    "openhands-runtime",
				"version": "1.0.0",
			},
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{
					"listChanged": false,
				},
			},
		},
	})

	// Keep connection alive with heartbeat
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("MCP SSE client disconnected")
			return
		case <-ticker.C:
			// Send heartbeat
			s.sendSSEMessage(c, map[string]interface{}{
				"jsonrpc": "2.0",
				"method":  "heartbeat",
				"params": map[string]interface{}{
					"timestamp": time.Now().Unix(),
				},
			})
		}
	}
}

// sendSSEMessage sends a JSON-RPC message over SSE
func (s *Server) sendSSEMessage(c *gin.Context, message interface{}) {
	data, err := json.Marshal(message)
	if err != nil {
		s.logger.Errorf("Failed to marshal MCP message: %v", err)
		return
	}

	// Send as SSE message event with JSON-RPC data
	c.SSEvent("message", string(data))
	if flusher, ok := c.Writer.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Tool handler methods

// handleFileRead handles file read tool calls
func (s *Server) handleFileRead(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	pathStr, err := request.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("path parameter error: %v", err)), nil
	}

	content, err := os.ReadFile(pathStr)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to read file: %v", err)), nil
	}

	return mcp.NewToolResultText(string(content)), nil
}

// handleFileWrite handles file write tool calls
func (s *Server) handleFileWrite(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	pathStr, err := request.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("path parameter error: %v", err)), nil
	}

	content, err := request.RequireString("content")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("content parameter error: %v", err)), nil
	}

	// Create directory if it doesn't exist
	dir := filepath.Dir(pathStr)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to create directory: %v", err)), nil
	}

	// Write file
	if err := os.WriteFile(pathStr, []byte(content), 0644); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to write file: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Successfully wrote %d bytes to %s", len(content), pathStr)), nil
}

// handleCmdRun handles command execution tool calls
func (s *Server) handleCmdRun(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	command, err := request.RequireString("command")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("command parameter error: %v", err)), nil
	}

	// Use the executor to run the command
	result, err := s.executor.RunCommand(command)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("command execution failed: %v", err)), nil
	}

	response := fmt.Sprintf("Command: %s\nExit Code: %d\nOutput:\n%s",
		command, result.Extras.ExitCode, result.Content)

	if result.Extras.ExitCode == 0 {
		return mcp.NewToolResultText(response), nil
	} else {
		return mcp.NewToolResultError(response), nil
	}
}

// handleListFiles handles file listing tool calls
func (s *Server) handleListFiles(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	pathStr, err := request.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("path parameter error: %v", err)), nil
	}

	entries, err := os.ReadDir(pathStr)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to list directory: %v", err)), nil
	}

	var fileList []string
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			s.logger.Warnf("Failed to get info for %s: %v", entry.Name(), err)
			continue
		}

		fileType := "file"
		if entry.IsDir() {
			fileType = "directory"
		}

		fileList = append(fileList, fmt.Sprintf("%s (%s, %d bytes, %s)",
			entry.Name(), fileType, info.Size(), info.Mode().String()))
	}

	result := fmt.Sprintf("Contents of %s:\n%s", pathStr,
		fmt.Sprintf("- %s", fmt.Sprintf("\n- %s", fileList)))

	return mcp.NewToolResultText(result), nil
}
