package mcp

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// MCPConnection represents an active MCP SSE connection
type MCPConnection struct {
	ID            string
	Context       *gin.Context
	Connected     bool
	LastHeartbeat time.Time
	mu            sync.RWMutex
}

// MCPManager manages MCP connections and message routing
type MCPManager struct {
	connections map[string]*MCPConnection
	logger      *logrus.Logger
	mu          sync.RWMutex
}

// NewMCPManager creates a new MCP connection manager
func NewMCPManager(logger *logrus.Logger) *MCPManager {
	return &MCPManager{
		connections: make(map[string]*MCPConnection),
		logger:      logger,
	}
}

// AddConnection adds a new MCP connection
func (m *MCPManager) AddConnection(id string, ctx *gin.Context) *MCPConnection {
	m.mu.Lock()
	defer m.mu.Unlock()

	conn := &MCPConnection{
		ID:            id,
		Context:       ctx,
		Connected:     true,
		LastHeartbeat: time.Now(),
	}

	m.connections[id] = conn
	m.logger.Infof("Added MCP connection: %s", id)
	return conn
}

// RemoveConnection removes an MCP connection
func (m *MCPManager) RemoveConnection(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.connections, id)
	m.logger.Infof("Removed MCP connection: %s", id)
}

// GetConnection gets an MCP connection by ID
func (m *MCPManager) GetConnection(id string) (*MCPConnection, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	conn, exists := m.connections[id]
	return conn, exists
}

// SendMessage sends a JSON-RPC message to a specific connection
func (conn *MCPConnection) SendMessage(message interface{}) error {
	conn.mu.Lock()
	defer conn.mu.Unlock()

	if !conn.Connected {
		return nil // Connection is closed
	}

	data, err := json.Marshal(message)
	if err != nil {
		return err
	}

	// Send as SSE message event with JSON-RPC data
	conn.Context.SSEvent("message", string(data))
	if flusher, ok := conn.Context.Writer.(gin.ResponseWriter); ok {
		if f, hasFlusher := flusher.(interface{ Flush() }); hasFlusher {
			f.Flush()
		}
	}

	return nil
}

// UpdateHeartbeat updates the last heartbeat time
func (conn *MCPConnection) UpdateHeartbeat() {
	conn.mu.Lock()
	defer conn.mu.Unlock()
	conn.LastHeartbeat = time.Now()
}

// Close marks the connection as closed
func (conn *MCPConnection) Close() {
	conn.mu.Lock()
	defer conn.mu.Unlock()
	conn.Connected = false
}
