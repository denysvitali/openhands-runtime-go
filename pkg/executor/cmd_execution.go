package executor

import (
        "context"
        "fmt"
        "path/filepath"
        "time"

        "github.com/denysvitali/openhands-runtime-go/internal/models"
        "go.opentelemetry.io/otel/attribute"
)

// executeCmdRun executes a command in the bash shell
func (e *Executor) executeCmdRun(ctx context.Context, action models.CmdRunAction) (interface{}, error) {
        _, span := e.tracer.Start(ctx, "cmd_run")
        defer span.End()

        // Set span attributes for tracing
        span.SetAttributes(
                attribute.String("command", action.Command),
                attribute.Bool("is_static", action.IsStatic),
        )

        // Log the command execution
        e.logger.Infof("Executing command: %s", action.Command)

        // Security check for command injection
        if err := e.sanitizeCommand(action.Command); err != nil {
                e.logger.Warnf("Potentially dangerous command blocked: %s", action.Command)
                return models.NewCmdOutputObservation(
                        fmt.Sprintf("Command blocked for security reasons: %v", err),
                        1, // Exit code 1 for blocked command
                        "",
                        action.Command,
                ), nil
        }

        // Set working directory if specified
        cwd := e.workingDir
        if action.Cwd != "" {
                // Make sure the path is resolved if it's relative
                if !filepath.IsAbs(action.Cwd) {
                        cwd = filepath.Join(e.workingDir, action.Cwd)
                } else {
                        cwd = action.Cwd
                }
        }

        // Create a new context with timeout if hardTimeout is specified
        execCtx := ctx
        var cancel context.CancelFunc
        if action.HardTimeout > 0 {
                execCtx, cancel = context.WithTimeout(ctx, time.Duration(action.HardTimeout)*time.Second)
                defer cancel()
        }

        // Send command to tmux session
        err = e.tmuxSession.SendCommand(execCtx, action.Command)

        // Capture output from tmux session
        rawOutput, err := e.tmuxSession.CaptureOutput(execCtx)
        if err != nil {
                return models.NewErrorObservation(
                        fmt.Sprintf("Failed to capture tmux output: %v", err),
                        "TmuxCaptureError",
                ), nil
        }

        // Parse output to get actual output and exit code
        output, metadata, err := e.tmuxSession.ParseOutput(rawOutput, action.Command)
        if err != nil {
                return models.NewErrorObservation(
                        fmt.Sprintf("Failed to parse tmux output: %v", err),
                        "TmuxParseError",
                ), nil
        }

        e.logger.Debugf("Command executed with exit code: %d in directory: %s", metadata.ExitCode, cwd)

        return models.NewCmdOutputObservation(output, metadata.ExitCode, metadata.CommandID, action.Command), nil
}

// StreamCommandExecution executes a command and streams output in real-time
func (e *Executor) StreamCommandExecution(ctx context.Context, action models.CmdRunAction, outputChan chan<- string) error {
        _, span := e.tracer.Start(ctx, "stream_cmd_run")
        defer span.End()

        // Set span attributes for tracing
        span.SetAttributes(
                attribute.String("command", action.Command),
                attribute.Bool("is_static", action.IsStatic),
        )

        // Log the command execution
        e.logger.Infof("Streaming command execution: %s", action.Command)

        // Security check for command injection
        if err := e.sanitizeCommand(action.Command); err != nil {
                e.logger.Warnf("Potentially dangerous command blocked: %s", action.Command)
                outputChan <- fmt.Sprintf("Command blocked for security reasons: %v
", err)
                close(outputChan)
                return err
        }

        // Set working directory if specified
        cwd := e.workingDir
        if action.Cwd != "" {
                // Make sure the path is resolved if it's relative
                if !filepath.IsAbs(action.Cwd) {
                        cwd = filepath.Join(e.workingDir, action.Cwd)
                } else {
                        cwd = action.Cwd
                }
        }

        // Create a new context with timeout if hardTimeout is specified
        execCtx := ctx
        var cancel context.CancelFunc
        if action.HardTimeout > 0 {
                execCtx, cancel = context.WithTimeout(ctx, time.Duration(action.HardTimeout)*time.Second)
                defer cancel()
        }

        // Send command to tmux session
        err := e.tmuxSession.SendCommand(ctx, action.Command)
        if err != nil {
                close(outputChan)
                return fmt.Errorf("failed to send command to tmux: %w", err)
        }

        // Continuously capture output and stream it
        go func() {
                defer close(outputChan)
                lastOutput := ""
                for {
                        select {
                        case <-ctx.Done():
                                return // Context cancelled or timed out
                        default:
                                rawOutput, err := e.tmuxSession.CaptureOutput(ctx)
                                if err != nil {
                                        e.logger.Errorf("Failed to capture tmux output during streaming: %v", err)
                                        return
                                }

                                // Only send new output
                                newOutput := rawOutput[len(lastOutput):]
                                if newOutput != "" {
                                        outputChan <- newOutput
                                }
                                lastOutput = rawOutput

                                // Check if the command has finished
                                _, metadata, err := e.tmuxSession.ParseOutput(rawOutput, action.Command)
                                if err == nil && metadata.ExitCode != -1 { // -1 indicates command is still running
                                        return // Command finished
                                }

                                // Wait a bit before polling again
                                time.Sleep(100 * time.Millisecond)
                        }
                }
        }()

        return nil
}
