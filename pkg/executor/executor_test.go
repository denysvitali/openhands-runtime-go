package executor

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/denysvitali/openhands-runtime-go/internal/models"
	"github.com/denysvitali/openhands-runtime-go/pkg/config"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func newTestExecutor(t *testing.T) *Executor {
	cfg := &config.Config{
		Server: config.ServerConfig{
			WorkingDir: t.TempDir(),
			Username:   "testuser",
			UserID:     os.Getuid(),
		},
	}
	logger := logrus.New()
	logger.SetOutput(io.Discard) // Discard logs during tests

	executor, err := New(cfg, logger)
	assert.NoError(t, err)
	return executor
}

func TestExecuteCmdRun(t *testing.T) {
	executor := newTestExecutor(t)
	ctx := context.Background()

	t.Run("simple command", func(t *testing.T) {
		action := models.CmdRunAction{
			Command: "echo hello",
		}
		obs, err := executor.executeCmdRun(ctx, action)
		assert.NoError(t, err)

		cmdObs, ok := obs.(models.CmdOutputObservation)
		assert.True(t, ok)
		assert.Equal(t, "run", cmdObs.Observation)
		assert.Contains(t, cmdObs.Content, "hello")
		assert.Equal(t, 0, cmdObs.Extras["exit_code"])
	})

	t.Run("command with cwd", func(t *testing.T) {
		// Create a subdirectory and a file in it
		subDir := "test_subdir"
		err := os.Mkdir(filepath.Join(executor.workingDir, subDir), 0755)
		assert.NoError(t, err)
		err = os.WriteFile(filepath.Join(executor.workingDir, subDir, "testfile.txt"), []byte("content"), 0644)
		assert.NoError(t, err)

		action := models.CmdRunAction{
			Command: "ls",
			Cwd:     subDir,
		}
		obs, err := executor.executeCmdRun(ctx, action)
		assert.NoError(t, err)

		cmdObs, ok := obs.(models.CmdOutputObservation)
		assert.True(t, ok)
		assert.Contains(t, cmdObs.Content, "testfile.txt")
		assert.Equal(t, 0, cmdObs.Extras["exit_code"])
	})

	t.Run("command with absolute cwd", func(t *testing.T) {
		// Create a temporary directory and a file in it
		absTestDir := t.TempDir()
		err := os.WriteFile(filepath.Join(absTestDir, "abs_testfile.txt"), []byte("content"), 0644)
		assert.NoError(t, err)

		action := models.CmdRunAction{
			Command: "ls",
			Cwd:     absTestDir,
		}
		obs, err := executor.executeCmdRun(ctx, action)
		assert.NoError(t, err)

		cmdObs, ok := obs.(models.CmdOutputObservation)
		assert.True(t, ok)
		assert.Contains(t, cmdObs.Content, "abs_testfile.txt")
		assert.Equal(t, 0, cmdObs.Extras["exit_code"])
	})

	t.Run("command with timeout", func(t *testing.T) {
		action := models.CmdRunAction{
			Command:     "sleep 5",
			HardTimeout: 1, // 1 second timeout
		}
		obs, err := executor.executeCmdRun(ctx, action)
		assert.NoError(t, err) // The function itself shouldn't error, the command should timeout

		cmdObs, ok := obs.(models.CmdOutputObservation)
		assert.True(t, ok)
		// Exit code might vary depending on OS for timeout, so we don't assert it strictly
		// but it should be non-zero if the process was killed due to timeout.
		// On Linux, it's often -1 (SIGKILL) or 1 (if context cancelled before cmd starts fully)
		// or 137 (128 + 9 for SIGKILL)
		// For now, we check that the content indicates an issue or is empty
		// and that the exit code is not 0.
		assert.NotEqual(t, 0, cmdObs.Extras["exit_code"], "Exit code should be non-zero for a timed-out command")
	})

	t.Run("command error", func(t *testing.T) {
		action := models.CmdRunAction{
			Command: "command_that_does_not_exist_qwertyuiop",
		}
		obs, err := executor.executeCmdRun(ctx, action)
		assert.NoError(t, err)

		cmdObs, ok := obs.(models.CmdOutputObservation)
		assert.True(t, ok)
		assert.NotEqual(t, 0, cmdObs.Extras["exit_code"])
		// Shells usually return 127 for "command not found"
		assert.Contains(t, cmdObs.Content, "not found") // or similar error message
	})

	t.Run("command with background (is_static ignored by executeCmdRun)", func(t *testing.T) {
		action := models.CmdRunAction{
			Command:  "echo background",
			IsStatic: true, // This field is for informational purposes in the span, not for execution logic here
		}
		obs, err := executor.executeCmdRun(ctx, action)
		assert.NoError(t, err)

		cmdObs, ok := obs.(models.CmdOutputObservation)
		assert.True(t, ok)
		assert.Contains(t, cmdObs.Content, "background")
		assert.Equal(t, 0, cmdObs.Extras["exit_code"])
	})
}

func TestExecuteAction_CmdRun(t *testing.T) {
	executor := newTestExecutor(t)
	ctx := context.Background()

	jsonData := `{"action":"run","args":{"blocking":false,"command":"id","confirmation_state":"confirmed","cwd":null,"hidden":false,"is_input":false,"is_static":false,"thought":""},"id":4,"message":"Running command: id","source":"user","timeout":120,"timestamp":"2025-06-09T16:32:56.649078"}`
	var actionMap map[string]interface{} // This map will be the direct unmarshalling of jsonData, retaining the nested "args" structure.
	err := json.Unmarshal([]byte(jsonData), &actionMap)
	assert.NoError(t, err)

	// Call ExecuteAction with the original actionMap, which contains the nested "args" structure.
	// This ensures that ParseAction's logic for handling nested "args" is tested.
	obs, err := executor.ExecuteAction(ctx, actionMap)
	assert.NoError(t, err)

	cmdObs, ok := obs.(models.CmdOutputObservation)
	assert.True(t, ok, "Observation should be CmdOutputObservation")

	assert.Equal(t, "run", cmdObs.Observation)
	assert.Contains(t, cmdObs.Content, "uid=") // "id" command output typically contains "uid="
	assert.Equal(t, 0, cmdObs.Extras["exit_code"])
	assert.Equal(t, "id", cmdObs.Extras["command"]) // Should correctly parse "id"
}
