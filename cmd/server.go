package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"strings"

	"github.com/denysvitali/openhands-runtime-go/pkg/config"
	"github.com/denysvitali/openhands-runtime-go/pkg/server"
	"github.com/denysvitali/openhands-runtime-go/pkg/telemetry"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// serverCmd represents the server command
var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Start the OpenHands runtime server",
	Long: `Start the OpenHands runtime server that listens for action execution requests
and provides observations back to the OpenHands backend.`,
	RunE: runServer,
}

func init() {
	viper.AutomaticEnv()
	// Replace . with _ in env var names (e.g., server.port becomes SERVER_PORT)
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	// Use this viper instance for all subsequent viper calls in this package
	// by replacing the global viper instance.
	// viper.SetViper(vip) // This was causing issues
	rootCmd.AddCommand(serverCmd)

	// Server-specific flags
	serverCmd.Flags().IntP("port", "p", 8000, "Port to listen on")
	serverCmd.Flags().String("working-dir", "", "Working directory for action execution")
	serverCmd.Flags().StringSlice("plugins", []string{}, "Plugins to initialize")
	serverCmd.Flags().String("username", "openhands", "User to run as")
	serverCmd.Flags().Int("user-id", 1000, "User ID to run as")
	serverCmd.Flags().String("browsergym-eval-env", "", "BrowserGym environment for browser evaluation")
	serverCmd.Flags().String("session-api-key", "", "API key for session authentication")
	serverCmd.Flags().Bool("enable-telemetry", true, "Enable OpenTelemetry tracing")
	serverCmd.Flags().String("otel-endpoint", "", "OpenTelemetry endpoint (if empty, uses auto-export)")

	// Bind flags to viper
	_ = viper.BindPFlag("server.port", serverCmd.Flags().Lookup("port"))
	_ = viper.BindPFlag("server.working_dir", serverCmd.Flags().Lookup("working-dir"))
	_ = viper.BindPFlag("server.plugins", serverCmd.Flags().Lookup("plugins"))
	_ = viper.BindPFlag("server.username", serverCmd.Flags().Lookup("username"))
	_ = viper.BindPFlag("server.user_id", serverCmd.Flags().Lookup("user-id"))
	_ = viper.BindPFlag("server.browsergym_eval_env", serverCmd.Flags().Lookup("browsergym-eval-env"))
	_ = viper.BindPFlag("server.session_api_key", serverCmd.Flags().Lookup("session-api-key"))
	_ = viper.BindPFlag("telemetry.enabled", serverCmd.Flags().Lookup("enable-telemetry"))
	_ = viper.BindPFlag("telemetry.endpoint", serverCmd.Flags().Lookup("otel-endpoint"))
}

func runServer(cmd *cobra.Command, args []string) error {
	logger := GetLogger()
	logger.Info("Starting OpenHands Runtime Server")

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	// Initialize telemetry if enabled
	var cleanupTelemetry func()
	if cfg.Telemetry.Enabled {
		logger.Info("Initializing OpenTelemetry with autoexport")
		cleanupTelemetry, err = telemetry.Initialize(cfg.Telemetry, logger)
		if err != nil {
			logger.Warnf("Failed to initialize telemetry: %v", err)
		} else {
			// Defer cleanup of telemetry resources
			defer func() {
				logger.Info("Cleaning up telemetry...")
				cleanupTelemetry()
			}()
		}
	}

	// Create and start server
	srv, err := server.New(cfg, logger)
	if err != nil {
		return fmt.Errorf("failed to create server: %w", err)
	}

	// Start server in a goroutine
	serverErrors := make(chan error, 1)
	go func() {
		logger.Infof("Server starting on port %d", cfg.Server.Port)
		serverErrors <- srv.Start()
	}()

	// Wait for interrupt signal
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		// If server.Start() returns an error (e.g., port already in use), log and exit.
		// The executor might not have been fully initialized or might not need explicit closing here,
		// as srv.Shutdown() (which calls executor.Close()) won't be called.
		// However, if New() succeeded, the executor was created.
		// It's safer to attempt a close if the server instance is valid.
		if srv != nil && srv.Executor() != nil {
			logger.Info("Server failed to start, attempting to clean up executor...")
			if closeErr := srv.Executor().Close(); closeErr != nil {
				logger.Errorf("Error closing executor after server start failure: %v", closeErr)
			}
		}
		return fmt.Errorf("server error: %w", err)
	case sig := <-interrupt:
		logger.Infof("Received signal %v, shutting down...", sig)

		// Graceful shutdown with timeout
		// The server.Shutdown() method now also handles executor.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := srv.Shutdown(ctx); err != nil {
			logger.Errorf("Server shutdown error: %v", err)
			// Even if shutdown has an error, we return it, and deferred telemetry cleanup will run.
			return err
		}

		logger.Info("Server stopped gracefully")
		return nil
	}
}
