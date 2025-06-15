package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/denysvitali/openhands-runtime-go/internal/models"
	"github.com/denysvitali/openhands-runtime-go/pkg/config"
	"github.com/denysvitali/openhands-runtime-go/pkg/executor"
	"github.com/denysvitali/openhands-runtime-go/pkg/telemetry"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

// Server represents the HTTP server
type Server struct {
	config   *config.Config
	logger   *logrus.Logger
	executor *executor.Executor
	engine   *gin.Engine
	server   *http.Server
}

// New creates a new server instance
func New(cfg *config.Config, logger *logrus.Logger) (*Server, error) {
	// Create executor
	exec, err := executor.New(cfg, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create executor: %w", err)
	}

	// Set gin mode based on log level
	if logger.Level == logrus.DebugLevel {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	// Create gin engine
	engine := gin.New()

	// Add middleware
	engine.Use(gin.Recovery())
	engine.Use(ginLogger(logger))

	// Add OpenTelemetry middleware if telemetry is enabled
	if cfg.Telemetry.Enabled {
		engine.Use(otelgin.Middleware("openhands-runtime"))
	}

	// Add CORS middleware
	engine.Use(corsMiddleware())

	// Add authentication middleware if API key is configured
	if cfg.Server.SessionAPIKey != "" {
		engine.Use(authMiddleware(cfg.Server.SessionAPIKey))
	}

	server := &Server{
		config:   cfg,
		logger:   logger,
		executor: exec,
		engine:   engine,
	}

	// Setup routes
	server.setupRoutes()

	return server, nil
}

// Start starts the HTTP server
func (s *Server) Start() error {
	s.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.config.Server.Port),
		Handler: s.engine,
	}

	s.logger.Infof("Starting server on port %d", s.config.Server.Port)
	return s.server.ListenAndServe()
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown(ctx context.Context) error {
	if s.server == nil {
		return nil
	}
	// Also close the executor
	if s.executor != nil {
		s.logger.Info("Closing executor...")
		if err := s.executor.Close(); err != nil {
			s.logger.Errorf("Error closing executor: %v", err)
			// We still want to try shutting down the HTTP server, so don't return here
			// but log the error.
		}
	}
	return s.server.Shutdown(ctx)
}

// Engine returns the gin engine for testing purposes
func (s *Server) Engine() *gin.Engine {
	return s.engine
}

// Executor returns the underlying executor instance.
// This is useful for tasks like graceful shutdown of executor resources.
func (s *Server) Executor() *executor.Executor {
	return s.executor
}

// setupRoutes configures all HTTP routes
func (s *Server) setupRoutes() {
	// Health check
	s.engine.GET("/alive", s.handleAlive)

	// Server info
	s.engine.GET("/server_info", s.handleServerInfo)

	// Action execution
	s.engine.POST("/execute_action", s.handleExecuteAction)
	s.engine.POST("/execute_action_stream", s.handleExecuteActionStream)

	// File operations
	s.engine.POST("/upload_file", s.handleUploadFile)
	s.engine.GET("/download_files", s.handleDownloadFiles)
	s.engine.POST("/list_files", s.handleListFiles)

	// VSCode integration
	s.engine.GET("/vscode/connection_token", s.handleVSCodeToken)

	// MCP server management (placeholder)
	s.engine.POST("/update_mcp_server", s.handleUpdateMCPServer)

	// SSE endpoint for streaming communication
	s.engine.GET("/sse", s.handleSSE)
}

// handleAlive handles health check requests
func (s *Server) handleAlive(c *gin.Context) {
	// Check if executor is initialized (similar to Python version)
	if s.executor == nil {
		c.JSON(http.StatusOK, gin.H{"status": "not initialized"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// handleServerInfo handles server info requests
func (s *Server) handleServerInfo(c *gin.Context) {
	// Get current time for uptime/idle calculations
	currentTime := time.Now()

	// Get server info from executor
	info := s.executor.GetServerInfo()

	// Calculate uptime and idle time (in seconds, as floats)
	uptime := currentTime.Sub(info.StartTime).Seconds()
	idleTime := currentTime.Sub(info.LastExecTime).Seconds()

	// Get system stats and convert to Python format
	systemStats := s.executor.GetSystemStats()
	resources := models.SystemResources{
		CPUCount:      runtime.NumCPU(),
		CPUPercent:    systemStats.CPUPercent,
		MemoryTotal:   int64(systemStats.Memory.RSS + systemStats.Memory.VMS), // Use RSS + VMS as total
		MemoryUsed:    int64(systemStats.Memory.RSS),                          // Use RSS as used
		MemoryPercent: float64(systemStats.Memory.Percent),
		DiskTotal:     int64(systemStats.Disk.Total),
		DiskUsed:      int64(systemStats.Disk.Used),
		DiskPercent:   systemStats.Disk.Percent,
	}

	// Create response matching Python format
	response := models.ServerInfoResponse{
		Uptime:    uptime,
		IdleTime:  idleTime,
		Resources: resources,
	}

	s.logger.Infof("Server info endpoint response: uptime=%.2fs, idle_time=%.2fs", uptime, idleTime)
	c.JSON(http.StatusOK, response)
}

// handleExecuteAction handles action execution requests
func (s *Server) handleExecuteAction(c *gin.Context) {
	tracer := otel.Tracer("openhands-runtime")
	ctx, span := tracer.Start(c.Request.Context(), "handle_execute_action")
	defer span.End()

	var req models.ActionRequest
	// Read the request body
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		span.RecordError(err)
		s.logger.Errorf("Failed to read request body: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
		return
	}

	// Log the raw request body
	s.logger.Infof("Received command: %s", string(bodyBytes))

	// -----------------------------------------------------------------------
	// Tool Compatibility Layer
	// -----------------------------------------------------------------------
	// This section handles compatibility with different AI tool calling formats.
	// OpenHands can work with various frontends that might use OpenAI, Claude,
	// or other LLM APIs. Each system has different tool calling formats:
	//
	// 1. Claude: Uses "tool_call_metadata" with tools like "str_replace_editor"
	// 2. OpenAI: Uses "tool_calls" array with function name and arguments
	//
	// We map these external formats to our internal action formats before processing.
	// -----------------------------------------------------------------------
	var bodyMap map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &bodyMap); err == nil {
		// Check if this is a tool call with specific formats we need to map
		if action, hasAction := bodyMap["action"].(map[string]interface{}); hasAction {
			// Handle Claude str_replace_editor used for reading files
			if toolMeta, hasToolMeta := action["tool_call_metadata"].(map[string]interface{}); hasToolMeta {
				if toolName, hasToolName := toolMeta["function_name"].(string); hasToolName {
					s.logger.Infof("Detected tool call: %s", toolName)

					// Handle str_replace_editor for file viewing
					if toolName == "str_replace_editor" {
						args, hasArgs := action["args"].(map[string]interface{})
						if hasArgs {
							command, hasCommand := args["command"].(string)
							if hasCommand && command == "view" {
								// This is a file read request using str_replace_editor
								// Remap it to a standard read action
								s.logger.Infof("Remapping str_replace_editor view to read action")
								bodyMap["action"] = "read"
								if path, hasPath := args["path"].(string); hasPath {
									bodyMap["path"] = path
								}
								// Re-encode the modified request
								modifiedBody, _ := json.Marshal(bodyMap)
								bodyBytes = modifiedBody
							}
						}
					}
				}
			}

			// Handle OpenAI tool calls
			if tool_calls, hasToolCalls := action["tool_calls"].([]interface{}); hasToolCalls && len(tool_calls) > 0 {
				s.logger.Infof("Detected OpenAI format tool calls")

				// Process the first tool call
				if toolCall, ok := tool_calls[0].(map[string]interface{}); ok {
					if function, hasFunction := toolCall["function"].(map[string]interface{}); hasFunction {
						name, hasName := function["name"].(string)
						arguments, hasArguments := function["arguments"].(string)

						if hasName && hasArguments {
							s.logger.Infof("Processing OpenAI tool call: %s", name)

							// Parse arguments
							var args map[string]interface{}
							if err := json.Unmarshal([]byte(arguments), &args); err == nil {
								// Map to our internal actions
								switch name {
								case "read_file":
									if filePath, ok := args["target_file"].(string); ok {
										bodyMap["action"] = "read"
										bodyMap["path"] = filePath
										s.logger.Infof("Remapped read_file to read action for %s", filePath)
									}
								case "run_terminal_cmd":
									if cmd, ok := args["command"].(string); ok {
										bodyMap["action"] = "run"
										bodyMap["command"] = cmd
										s.logger.Infof("Remapped run_terminal_cmd to run action: %s", cmd)
									}
								}

								// Re-encode the modified request
								modifiedBody, _ := json.Marshal(bodyMap)
								bodyBytes = modifiedBody
							} else {
								s.logger.Warnf("Failed to parse OpenAI tool arguments: %v", err)
							}
						}
					}
				}
			}
		}
	} else {
		s.logger.Warnf("Failed to parse request for tool compatibility check: %v", err)
	}
	// End of Tool Compatibility Layer
	// -----------------------------------------------------------------------

	// Unmarshal the body into the request object
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		span.RecordError(err)
		s.logger.Errorf("Failed to unmarshal request: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Add action type to span if available
	if actionType, ok := req.Action["action"].(string); ok {
		span.SetAttributes(attribute.String("action.type", actionType))
		s.logger.Infof("Processing action type: %s", actionType)
	}

	// Report action request JSON in traces and logs
	if s.config.Telemetry.Enabled {
		telemetry.ReportJSON(ctx, s.logger, "action_request", req.Action)
	}

	// Execute action
	observation, err := s.executor.ExecuteAction(ctx, req.Action)
	if err != nil {
		span.RecordError(err)
		s.logger.Errorf("Failed to execute action: %v", err)
		errorObs := models.NewErrorObservation(
			fmt.Sprintf("Failed to execute action: %v", err),
			"ExecutionError",
		)

		// Report error observation JSON in traces and logs
		if s.config.Telemetry.Enabled {
			telemetry.ReportJSON(ctx, s.logger, "action_error", errorObs)
		}

		c.JSON(http.StatusInternalServerError, errorObs)
		return
	}

	// Report successful observation JSON in traces and logs
	if s.config.Telemetry.Enabled {
		telemetry.ReportJSON(ctx, s.logger, "action_response", observation)
	}

	// Log the response
	responseBytes, _ := json.Marshal(observation)
	s.logger.Infof("Sending reply: %s", string(responseBytes))

	c.JSON(http.StatusOK, observation)
}

// handleExecuteActionStream handles streaming action execution requests
func (s *Server) handleExecuteActionStream(c *gin.Context) {
	tracer := otel.Tracer("openhands-runtime")
	ctx, span := tracer.Start(c.Request.Context(), "handle_execute_action_stream")
	defer span.End()

	var req models.ActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		span.RecordError(err)
		s.logger.Errorf("Failed to unmarshal streaming request: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Check if this is a command run action
	actionType, ok := req.Action["action"].(string)
	if !ok || actionType != "run" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "streaming is only supported for 'run' actions"})
		return
	}

	// Parse the command run action
	command, ok := req.Action["command"].(string)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing or invalid 'command' field"})
		return
	}

	// Set headers for streaming
	setSSEHeaders(c)

	// Create a channel for streaming output
	outputChan := make(chan string, 100)

	// Create the action
	action := models.CmdRunAction{
		Command: command,
	}

	// Handle optional fields
	if cwd, ok := req.Action["cwd"].(string); ok {
		action.Cwd = cwd
	}
	if isStatic, ok := req.Action["is_static"].(bool); ok {
		action.IsStatic = isStatic
	}
	if hardTimeout, ok := req.Action["hard_timeout"].(float64); ok {
		action.HardTimeout = int(hardTimeout)
	}

	// Start streaming command execution in a goroutine
	go func() {
		err := s.executor.StreamCommandExecution(ctx, action, outputChan)
		if err != nil {
			s.logger.Errorf("Streaming command execution failed: %v", err)
		}
	}()

	// Stream the output
	s.logger.Infof("Starting streaming execution for command: %s", command)

	// Send initial message
	c.SSEvent("start", gin.H{
		"command": command,
		"timestamp": time.Now().Unix(),
	})
	c.Writer.Flush()

	// Stream output lines
	for line := range outputChan {
		c.SSEvent("output", gin.H{
			"data": line,
			"timestamp": time.Now().Unix(),
		})
		c.Writer.Flush()
	}

	// Send completion message
	c.SSEvent("complete", gin.H{
		"command": command,
		"timestamp": time.Now().Unix(),
	})
	c.Writer.Flush()

	s.logger.Infof("Completed streaming execution for command: %s", command)
}

// handleUploadFile handles file upload requests
func (s *Server) handleUploadFile(c *gin.Context) {
	tracer := otel.Tracer("openhands-runtime")
	ctx, span := tracer.Start(c.Request.Context(), "handle_upload_file")
	defer span.End()

	path := c.Query("path")
	if path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path query parameter is required"})
		return
	}

	content, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to read request body: %v", err)})
		return
	}

	// Report upload request JSON in traces and logs
	if s.config.Telemetry.Enabled {
		uploadData := map[string]interface{}{
			"path":         path,
			"content_size": len(content),
		}
		telemetry.ReportJSON(ctx, s.logger, "file_upload_request", uploadData)
	}

	if err := s.executor.UploadFile(ctx, path, content); err != nil {
		errorData := map[string]interface{}{
			"path":  path,
			"error": err.Error(),
		}
		if s.config.Telemetry.Enabled {
			telemetry.ReportJSON(ctx, s.logger, "file_upload_error", errorData)
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to upload file: %v", err)})
		return
	}

	// Report successful upload
	if s.config.Telemetry.Enabled {
		successData := map[string]interface{}{
			"path":         path,
			"content_size": len(content),
			"status":       "success",
		}
		telemetry.ReportJSON(ctx, s.logger, "file_upload_success", successData)
	}

	c.Status(http.StatusOK)
}

// handleDownloadFiles handles file download requests
func (s *Server) handleDownloadFiles(c *gin.Context) {
	tracer := otel.Tracer("openhands-runtime")
	ctx, span := tracer.Start(c.Request.Context(), "handle_download_file")
	defer span.End()

	// Support both single path and multiple paths parameters
	path := c.Query("path")
	paths := c.QueryArray("paths")

	// If no paths specified, fall back to single path parameter
	if len(paths) == 0 && path != "" {
		paths = []string{path}
	}

	if len(paths) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path or paths query parameter is required"})
		return
	}

	s.logger.Debugf("Downloading files: %v", paths)

	// Validate that all paths are absolute and secure
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Path must be an absolute path: %s", p)})
			return
		}

		// Security validation happens in the executor methods, but we can do basic checks here
		// Check if path exists
		if _, err := os.Stat(p); os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("File not found: %s", p)})
			return
		}
	}

	// Determine filename for the zip
	var filename string
	if len(paths) == 1 {
		filename = fmt.Sprintf("%s.zip", filepath.Base(paths[0]))
	} else {
		filename = "download.zip"
	}

	// Set headers for file download
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))
	c.Header("Content-Type", "application/zip")

	// Stream the zip file directly to the response writer
	if err := s.executor.StreamZipArchiveMultiple(ctx, paths, c.Writer); err != nil {
		s.logger.Errorf("Error streaming zip file: %v", err)
		// At this point headers are already sent, so we can't send a JSON error
		// The client will see a truncated/corrupted zip file
		return
	}
}

// handleListFiles handles file listing requests
func (s *Server) handleListFiles(c *gin.Context) {
	tracer := otel.Tracer("openhands-runtime")
	ctx, span := tracer.Start(c.Request.Context(), "handle_list_files")
	defer span.End()

	var req models.ListFilesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Use the new ListFileNames function to match Python implementation
	fileNames, err := s.executor.ListFileNames(ctx, req.Path)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to list files: %v", err)})
		return
	}

	c.JSON(http.StatusOK, fileNames)
}

// handleVSCodeToken handles VSCode connection token requests
func (s *Server) handleVSCodeToken(c *gin.Context) {
	// This is a placeholder implementation
	c.JSON(http.StatusOK, models.VSCodeConnectionToken{
		Token: "placeholder-token",
	})
}

// handleUpdateMCPServer handles MCP server update requests
func (s *Server) handleUpdateMCPServer(c *gin.Context) {
	tracer := otel.Tracer("openhands-runtime")
	ctx, span := tracer.Start(c.Request.Context(), "handle_update_mcp_server")
	defer span.End()

	// Parse request body as list of MCP tools to sync
	var mcpToolsToSync []interface{}
	if err := c.ShouldBindJSON(&mcpToolsToSync); err != nil {
		span.RecordError(err)
		c.JSON(http.StatusBadRequest, gin.H{"detail": "Request must be a list of MCP tools to sync"})
		return
	}

	// Log the MCP update request
	if s.config.Telemetry.Enabled {
		telemetry.ReportJSON(ctx, s.logger, "mcp_update_request", mcpToolsToSync)
	}

	s.logger.Infof("Updating MCP server with %d tools", len(mcpToolsToSync))

	// TODO: Implement actual MCP profile update logic here
	// For now, we'll just acknowledge the request
	// In the Python version, this:
	// 1. Reads the current profile from config.json
	// 2. Updates the 'default' key with the new tools list
	// 3. Writes back to the profile file
	// 4. Reloads the profile and updates servers

	resp := gin.H{
		"detail":           "MCP server updated successfully",
		"router_error_log": "",
	}

	if s.config.Telemetry.Enabled {
		telemetry.ReportJSON(ctx, s.logger, "mcp_update_response", resp)
	}
	c.JSON(http.StatusOK, resp)
}

// setSSEHeaders sets the standard headers required for Server-Sent Events
func setSSEHeaders(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Access-Control-Allow-Headers", "Cache-Control")
}

// handleSSE handles Server-Sent Events for streaming communication
func (s *Server) handleSSE(c *gin.Context) {
	// Authentication is handled by middleware
	
	// Set headers for SSE
	setSSEHeaders(c)

	// For now, this is a basic implementation that keeps the connection alive
	// In a full implementation, this would handle MCP protocol messages
	s.logger.Info("SSE connection established")

	// Send initial connection message
	c.SSEvent("message", gin.H{
		"type": "connection",
		"data": gin.H{
			"status": "connected",
			"timestamp": time.Now().Unix(),
		},
	})

	// Keep connection alive with periodic heartbeat
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Create a channel to handle client disconnect
	clientGone := c.Request.Context().Done()

	for {
		select {
		case <-clientGone:
			s.logger.Info("SSE client disconnected")
			return
		case <-ticker.C:
			// Send heartbeat
			c.SSEvent("heartbeat", gin.H{
				"timestamp": time.Now().Unix(),
			})
			c.Writer.Flush()
		}
	}
}

// ginLogger creates a gin logger middleware using logrus
func ginLogger(logger *logrus.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Don't log /alive requests
		if c.Request.URL.Path == "/alive" {
			c.Next()
			return
		}

		start := time.Now()
		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery

		// Process request
		c.Next()

		// Calculate latency
		latency := time.Since(start)

		// Get status code
		statusCode := c.Writer.Status()

		// Build log entry
		entry := logger.WithFields(logrus.Fields{
			"status":     statusCode,
			"method":     c.Request.Method,
			"path":       path,
			"ip":         c.ClientIP(),
			"latency":    latency,
			"user_agent": c.Request.UserAgent(),
		})

		if raw != "" {
			entry = entry.WithField("query", raw)
		}

		// Log based on status code
		if statusCode >= 500 {
			entry.Error("Server error")
		} else if statusCode >= 400 {
			entry.Warn("Client error")
		} else {
			entry.Info("Request completed")
		}
	}
}

// corsMiddleware adds CORS headers
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, X-Session-API-Key")
		c.Header("Access-Control-Allow-Credentials", "true")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

// authMiddleware validates API key
func authMiddleware(expectedAPIKey string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Skip authentication for certain endpoints
		path := c.Request.URL.Path
		if path == "/alive" || path == "/server_info" {
			c.Next()
			return
		}

		apiKey := c.GetHeader("X-Session-API-Key")
		
		// For SSE endpoints, also check query parameters as fallback
		if apiKey == "" && path == "/sse" {
			apiKey = c.Query("api_key")
		}
		
		if apiKey != expectedAPIKey {
			c.JSON(http.StatusForbidden, gin.H{"error": "Invalid API Key"})
			c.Abort()
			return
		}
		c.Next()
	}
}
