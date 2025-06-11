package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/denysvitali/openhands-runtime-go/internal/models"
)

// executeIPython executes code in an IPython kernel
func (e *Executor) executeIPython(ctx context.Context, action models.IPythonRunCellAction) (interface{}, error) {
	_, span := e.tracer.Start(ctx, "ipython_run")
	defer span.End()

	e.logger.Infof("Executing IPython cell: %s", action.Code)

	// Check if Jupyter is installed
	checkCmd := exec.Command("which", "jupyter")
	err := checkCmd.Run()
	if err != nil {
		errorMsg := "Jupyter is not installed. Please install it with: pip install jupyter"
		e.logger.Error(errorMsg)
		return models.NewErrorObservation(errorMsg, "JupyterNotInstalledError"), nil
	}

	// Create a temporary notebook file
	tempDir, err := os.MkdirTemp("", "jupyter")
	if err != nil {
		e.logger.Errorf("Failed to create temp directory: %v", err)
		return models.NewErrorObservation(
			fmt.Sprintf("Failed to create temp directory: %v", err),
			"IPythonError",
		), nil
	}
	defer os.RemoveAll(tempDir)

	// Create a simple notebook with the code
	notebookPath := filepath.Join(tempDir, "notebook.ipynb")
	notebook := createNotebookWithCode(action.Code)

	notebookJSON, err := json.Marshal(notebook)
	if err != nil {
		e.logger.Errorf("Failed to marshal notebook: %v", err)
		return models.NewErrorObservation(
			fmt.Sprintf("Failed to marshal notebook: %v", err),
			"IPythonError",
		), nil
	}

	err = os.WriteFile(notebookPath, notebookJSON, 0644)
	if err != nil {
		e.logger.Errorf("Failed to write notebook file: %v", err)
		return models.NewErrorObservation(
			fmt.Sprintf("Failed to write notebook file: %v", err),
			"IPythonError",
		), nil
	}

	// Execute the notebook
	outputPath := filepath.Join(tempDir, "output.ipynb")
	cmd := exec.Command(
		"jupyter", "nbconvert", "--to", "notebook", "--execute",
		"--ExecutePreprocessor.timeout=60",
		"--output", outputPath,
		notebookPath,
	)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errorMsg := fmt.Sprintf("Failed to execute notebook: %v\n%s", err, stderr.String())
		e.logger.Error(errorMsg)
		return models.NewErrorObservation(errorMsg, "IPythonExecutionError"), nil
	}

	// Read the output notebook
	outputJSON, err := os.ReadFile(outputPath)
	if err != nil {
		e.logger.Errorf("Failed to read output notebook: %v", err)
		return models.NewErrorObservation(
			fmt.Sprintf("Failed to read output notebook: %v", err),
			"IPythonError",
		), nil
	}

	// Parse the output notebook
	var outputNotebook map[string]interface{}
	if err := json.Unmarshal(outputJSON, &outputNotebook); err != nil {
		e.logger.Errorf("Failed to parse output notebook: %v", err)
		return models.NewErrorObservation(
			fmt.Sprintf("Failed to parse output notebook: %v", err),
			"IPythonError",
		), nil
	}

	// Extract the outputs
	result := extractNotebookOutputs(outputNotebook)

	return models.NewIPythonRunCellObservation(result), nil
}

// Utility function to create a notebook with a single code cell
func createNotebookWithCode(code string) map[string]interface{} {
	return map[string]interface{}{
		"cells": []map[string]interface{}{
			{
				"cell_type":       "code",
				"execution_count": nil,
				"metadata":        map[string]interface{}{},
				"source":          []string{code},
				"outputs":         []interface{}{},
			},
		},
		"metadata": map[string]interface{}{
			"kernelspec": map[string]interface{}{
				"display_name": "Python 3",
				"language":     "python",
				"name":         "python3",
			},
		},
		"nbformat":       4,
		"nbformat_minor": 4,
	}
}

// Utility function to extract outputs from a notebook
func extractNotebookOutputs(notebook map[string]interface{}) string {
	var result strings.Builder

	cells, ok := notebook["cells"].([]interface{})
	if !ok || len(cells) == 0 {
		return "No output"
	}

	for _, cellInterface := range cells {
		cell, ok := cellInterface.(map[string]interface{})
		if !ok {
			continue
		}

		outputs, ok := cell["outputs"].([]interface{})
		if !ok {
			continue
		}

		for _, outputInterface := range outputs {
			output, ok := outputInterface.(map[string]interface{})
			if !ok {
				continue
			}

			// Text output
			if text, ok := output["text"].([]interface{}); ok {
				for _, t := range text {
					if str, ok := t.(string); ok {
						result.WriteString(str)
					}
				}
			}

			// Data output (like images, HTML, etc.)
			if data, ok := output["data"].(map[string]interface{}); ok {
				// Text/plain output
				if textPlain, ok := data["text/plain"].([]interface{}); ok {
					for _, t := range textPlain {
						if str, ok := t.(string); ok {
							result.WriteString(str)
							result.WriteString("\n")
						}
					}
				}

				// HTML output is handled specially - just note it was produced
				if _, ok := data["text/html"]; ok {
					result.WriteString("[HTML output was produced]\n")
				}

				// Image output is handled specially - just note it was produced
				if _, ok := data["image/png"]; ok {
					result.WriteString("[Image output was produced]\n")
				}
			}
		}
	}

	return result.String()
}
