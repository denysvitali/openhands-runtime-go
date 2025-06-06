package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/denysvitali/openhands-runtime-go/internal/models"
	"github.com/denysvitali/openhands-runtime-go/pkg/config"
	"github.com/denysvitali/openhands-runtime-go/pkg/executor"
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
	return s.server.Shutdown(ctx)
}

// setupRoutes configures all HTTP routes
func (s *Server) setupRoutes() {
	// Health check
	s.engine.GET("/alive", s.handleAlive)

	// Server info
	s.engine.GET("/server_info", s.handleServerInfo)

	// Action execution
	s.engine.POST("/execute_action", s.handleExecuteAction)

	// File operations
	s.engine.POST("/upload_file", s.handleUploadFile)
	s.engine.GET("/download_files", s.handleDownloadFiles)
	s.engine.POST("/list_files", s.handleListFiles)

	// VSCode integration
	s.engine.GET("/vscode/connection_token", s.handleVSCodeToken)

	// MCP server management (placeholder)
	s.engine.POST("/update_mcp_server", s.handleUpdateMCPServer)
}

// handleAlive handles health check requests
func (s *Server) handleAlive(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "alive"})
}

// handleServerInfo handles server info requests
func (s *Server) handleServerInfo(c *gin.Context) {
	info := s.executor.GetServerInfo()
	c.JSON(http.StatusOK, info)
}

// handleExecuteAction handles action execution requests
func (s *Server) handleExecuteAction(c *gin.Context) {
	tracer := otel.Tracer("openhands-runtime")
	ctx, span := tracer.Start(c.Request.Context(), "handle_execute_action")
	defer span.End()

	var req models.ActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		span.RecordError(err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Add action type to span if available
	if actionType, ok := req.Action["action"].(string); ok {
		span.SetAttributes(attribute.String("action.type", actionType))
	}

	// Execute action
	observation, err := s.executor.ExecuteAction(ctx, req.Action)
	if err != nil {
		span.RecordError(err)
		s.logger.Errorf("Failed to execute action: %v", err)
		c.JSON(http.StatusInternalServerError, models.ErrorObservation{
			Observation: "error",
			Content:     fmt.Sprintf("Failed to execute action: %v", err),
			Timestamp:   time.Now(),
		})
		return
	}

	c.JSON(http.StatusOK, observation)
}

// handleUploadFile handles file upload requests
func (s *Server) handleUploadFile(c *gin.Context) {
	ctx, span := s.tracer.Start(c.Request.Context(), "handle_upload_file")
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

	if err := s.executor.UploadFile(ctx, path, content); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to upload file: %v", err)})
		return
	}

	c.Status(http.StatusOK)
}

// handleDownloadFiles handles file download requests
func (s *Server) handleDownloadFiles(c *gin.Context) {
	ctx, span := s.tracer.Start(c.Request.Context(), "handle_download_file")
	defer span.End()

	path := c.Query("path")
	if path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path query parameter is required"})
		return
	}

	content, err := s.executor.DownloadFile(ctx, path)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to download file: %v", err)})
		return
	}

	c.Data(http.StatusOK, "application/octet-stream", content)
}

// handleListFiles handles file listing requests
func (s *Server) handleListFiles(c *gin.Context) {
	ctx, span := s.tracer.Start(c.Request.Context(), "handle_list_files")
	defer span.End()

	var req models.ListFilesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	files, err := s.executor.ListFiles(ctx, req.Path, req.Recursive)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to list files: %v", err)})
		return
	}

	c.JSON(http.StatusOK, files)
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
	// This is a placeholder implementation
	c.JSON(http.StatusNotImplemented, gin.H{"error": "MCP server management not implemented"})
}

// ginLogger creates a gin logger middleware using logrus
func ginLogger(logger *logrus.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
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
		apiKey := c.GetHeader("X-Session-API-Key")
		if apiKey != expectedAPIKey {
			c.JSON(http.StatusForbidden, gin.H{"error": "Invalid API Key"})
			c.Abort()
			return
		}
		c.Next()
	}
}
