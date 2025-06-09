package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/denysvitali/openhands-runtime-go/internal/models"
)

// executeIPython executes IPython code using Python subprocess
func (e *Executor) executeIPython(ctx context.Context, action models.IPythonRunCellAction) (interface{}, error) {
	_, span := e.tracer.Start(ctx, "ipython_run")
	defer span.End()

	e.logger.Infof("Executing IPython with code: %s", action.Code)

	tmpFile, err := os.CreateTemp("", "ipython_*.py")
	if err != nil {
		return models.ErrorObservation{
			Observation: "error",
			Content:     fmt.Sprintf("Failed to create temporary file: %v", err),
			ErrorType:   "TempFileCreationError",
			Timestamp:   time.Now(),
		}, nil
	}
	defer func(name string) {
		_ = os.Remove(name)
	}(tmpFile.Name())

	wrapperCode := fmt.Sprintf(`
import sys
import io
import traceback
import json
import base64
import matplotlib
import matplotlib.pyplot as plt
from contextlib import redirect_stdout, redirect_stderr

matplotlib.use('Agg')

stdout_capture = io.StringIO()
stderr_capture = io.StringIO()
result = {"stdout": "", "stderr": "", "images": [], "error": False}

try:
    with redirect_stdout(stdout_capture), redirect_stderr(stderr_capture):
        exec('''%s''')
    
    if plt.get_fignums():
        import tempfile
        images = []
        for fig_num in plt.get_fignums():
            fig = plt.figure(fig_num)
            with tempfile.NamedTemporaryFile(suffix='.png', delete=False) as tmp:
                fig.savefig(tmp.name, format='png', bbox_inches='tight', dpi=150)
                with open(tmp.name, 'rb') as img_file:
                    img_data = base64.b64encode(img_file.read()).decode('utf-8')
                    images.append(f"data:image/png;base64,{img_data}")
                import os
                os.unlink(tmp.name)
            plt.close(fig)
        result["images"] = images
    
    result["stdout"] = stdout_capture.getvalue()
    result["stderr"] = stderr_capture.getvalue()
    
except Exception as e:
    result["error"] = True
    result["stderr"] = stderr_capture.getvalue() + "\n" + traceback.format_exc()
    result["stdout"] = stdout_capture.getvalue()

print("###IPYTHON_RESULT###")
print(json.dumps(result))
print("###IPYTHON_END###")
`, action.Code)

	if _, err := tmpFile.WriteString(wrapperCode); err != nil {
		_ = tmpFile.Close()
		return models.ErrorObservation{
			Observation: "error",
			Content:     fmt.Sprintf("Failed to write to temporary file: %v", err),
			ErrorType:   "TempFileWriteError",
			Timestamp:   time.Now(),
		}, nil
	}
	_ = tmpFile.Close()

	cmd := exec.CommandContext(ctx, "python3", tmpFile.Name())
	if e.workingDir != "" {
		cmd.Dir = e.workingDir
	}

	output, err := cmd.CombinedOutput()
	outputStr := string(output)

	e.logger.Infof("Python execution output: %s", outputStr)

	resultStartIdx := strings.Index(outputStr, "###IPYTHON_RESULT###")
	resultEndIdx := strings.Index(outputStr, "###IPYTHON_END###")

	var result struct {
		Stdout string   `json:"stdout"`
		Stderr string   `json:"stderr"`
		Images []string `json:"images"`
		Error  bool     `json:"error"`
	}

	var content string
	var imageURLs []string

	if resultStartIdx != -1 && resultEndIdx != -1 {
		jsonStr := outputStr[resultStartIdx+len("###IPYTHON_RESULT###") : resultEndIdx]
		jsonStr = strings.TrimSpace(jsonStr)

		if parseErr := json.Unmarshal([]byte(jsonStr), &result); parseErr == nil {
			content = result.Stdout
			if result.Stderr != "" {
				if content != "" {
					content += "\n"
				}
				content += result.Stderr
			}
			imageURLs = result.Images
		} else {
			content = outputStr
			e.logger.Warnf("Failed to parse IPython result JSON: %v", parseErr)
		}
	} else {
		content = outputStr
		if err != nil {
			content += fmt.Sprintf("\nError: %v", err)
		}
	}

	if action.IncludeExtra {
		if wd, wdErr := os.Getwd(); wdErr == nil {
			content += fmt.Sprintf("\n[Current working directory: %s]", wd)
		}
		if pythonPath, pathErr := exec.LookPath("python3"); pathErr == nil {
			content += fmt.Sprintf("\n[Python interpreter: %s]", pythonPath)
		}
	}

	observation := models.IPythonRunCellObservation{
		Observation: "run_ipython",
		Content:     content,
		Timestamp:   time.Now(),
		Extras: map[string]interface{}{
			"code": action.Code,
		},
	}

	if len(imageURLs) > 0 {
		observation.Extras["image_urls"] = imageURLs
	}

	e.logger.Infof("Created IPython observation: %+v", observation)
	return observation, nil
}
