package server_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/denysvitali/openhands-runtime-go/internal/models"
	"github.com/denysvitali/openhands-runtime-go/pkg/config"
	"github.com/denysvitali/openhands-runtime-go/pkg/server"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestServer(t *testing.T) *server.Server {
	// Create a temporary directory for testing
	tempDir := t.TempDir()

	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:           8080, // Use a different port for testing
			SessionAPIKey:  "test-key",
			FileViewerPort: 8081,
			WorkingDir:     tempDir,
			Username:       "testuser",
			UserID:         1000,
		},
		Telemetry: config.TelemetryConfig{
			Enabled: false,
		},
	}
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)

	srv, err := server.New(cfg, logger)
	require.NoError(t, err, "Failed to create server")
	return srv
}

func createAuthenticatedRequest(method, url string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Session-API-Key", "test-key")
	return req, nil
}

func TestHandleAlive_Success(t *testing.T) {
	srv := setupTestServer(t)

	req, err := createAuthenticatedRequest(http.MethodGet, "/alive", nil)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code, "Handler returned wrong status code")

	var resp map[string]interface{}
	err = json.Unmarshal(rr.Body.Bytes(), &resp)
	require.NoError(t, err, "Failed to unmarshal response")

	// Should return either "ok" or "not initialized" based on executor state
	status := resp["status"].(string)
	assert.Contains(t, []string{"ok", "not initialized"}, status)
}

func TestHandleServerInfo_Success(t *testing.T) {
	srv := setupTestServer(t)

	req, err := createAuthenticatedRequest(http.MethodGet, "/server_info", nil)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code, "Handler returned wrong status code")

	// Debug: print the response body
	t.Logf("Response body: %s", rr.Body.String())

	var resp models.ServerInfoResponse
	err = json.Unmarshal(rr.Body.Bytes(), &resp)
	require.NoError(t, err, "Failed to unmarshal response")

	// Verify response structure matches Python format
	assert.GreaterOrEqual(t, resp.Uptime, 0.0)
	assert.GreaterOrEqual(t, resp.IdleTime, 0.0)
	assert.NotNil(t, resp.Resources)
	assert.GreaterOrEqual(t, resp.Resources.CPUCount, 1)
}

func TestHandleExecuteAction_CmdRun_Success(t *testing.T) {
	srv := setupTestServer(t)

	actionReq := models.ActionRequest{
		Action: map[string]interface{}{
			"action":  "run",
			"command": "echo 'hello world'",
		},
	}

	payloadBytes, err := json.Marshal(actionReq)
	require.NoError(t, err)

	req, err := createAuthenticatedRequest(http.MethodPost, "/execute_action", bytes.NewBuffer(payloadBytes))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code, "Handler returned wrong status code")

	var resp models.CmdOutputObservation
	err = json.Unmarshal(rr.Body.Bytes(), &resp)
	require.NoError(t, err, "Failed to unmarshal response")

	assert.Equal(t, "run", resp.Observation)
	assert.Contains(t, resp.Content, "hello world")
	assert.Equal(t, "echo 'hello world'", resp.Extras["command"])
	assert.Equal(t, 0.0, resp.Extras["exit_code"])
}

func TestHandleExecuteAction_InvalidJSON(t *testing.T) {
	srv := setupTestServer(t)

	req, err := createAuthenticatedRequest(http.MethodPost, "/execute_action", bytes.NewBuffer([]byte("invalid-json")))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code, "Handler returned wrong status code for invalid JSON")
}

func TestHandleExecuteAction_MissingAPIKey(t *testing.T) {
	srv := setupTestServer(t)

	actionReq := models.ActionRequest{
		Action: map[string]interface{}{
			"action":  "run",
			"command": "echo 'hello world'",
		},
	}

	payloadBytes, err := json.Marshal(actionReq)
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, "/execute_action", bytes.NewBuffer(payloadBytes))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code, "Handler returned wrong status code for missing API Key")
}

func TestHandleUpdateMCPServer_Success(t *testing.T) {
	srv := setupTestServer(t)

	mcpTools := []interface{}{
		map[string]interface{}{
			"name": "test-tool",
			"type": "mcp",
		},
	}

	payloadBytes, err := json.Marshal(mcpTools)
	require.NoError(t, err)

	req, err := createAuthenticatedRequest(http.MethodPost, "/update_mcp_server", bytes.NewBuffer(payloadBytes))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code, "Handler returned wrong status code")

	var resp map[string]interface{}
	err = json.Unmarshal(rr.Body.Bytes(), &resp)
	require.NoError(t, err, "Failed to unmarshal response")

	assert.Equal(t, "MCP server updated successfully", resp["detail"])
	assert.Equal(t, "", resp["router_error_log"])
}

func TestHandleUpdateMCPServer_InvalidPayload(t *testing.T) {
	srv := setupTestServer(t)

	req, err := createAuthenticatedRequest(http.MethodPost, "/update_mcp_server", bytes.NewBuffer([]byte("invalid-json")))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code, "Handler returned wrong status code for invalid JSON")
}

func TestHandleListFiles_Success(t *testing.T) {
	srv := setupTestServer(t)

	listReq := models.ListFilesRequest{
		Path:      "/tmp",
		Recursive: false,
	}

	payloadBytes, err := json.Marshal(listReq)
	require.NoError(t, err)

	req, err := createAuthenticatedRequest(http.MethodPost, "/list_files", bytes.NewBuffer(payloadBytes))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	rr := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code, "Handler returned wrong status code")

	var resp []string
	err = json.Unmarshal(rr.Body.Bytes(), &resp)
	require.NoError(t, err, "Failed to unmarshal response")

	// Should return a list of files (may be empty)
	assert.NotNil(t, resp)
}

func TestHandleVSCodeToken_Success(t *testing.T) {
	srv := setupTestServer(t)

	req, err := createAuthenticatedRequest(http.MethodGet, "/vscode/connection_token", nil)
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	srv.Engine().ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code, "Handler returned wrong status code")

	var resp models.VSCodeConnectionToken
	err = json.Unmarshal(rr.Body.Bytes(), &resp)
	require.NoError(t, err, "Failed to unmarshal response")

	// Should return a token (even if placeholder)
	assert.NotEmpty(t, resp.Token)
}
