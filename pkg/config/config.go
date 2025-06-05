package config

import (
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// Config represents the application configuration
type Config struct {
	Server    ServerConfig    `mapstructure:"server"`
	Telemetry TelemetryConfig `mapstructure:"telemetry"`
	Log       LogConfig       `mapstructure:"log"`
}

// ServerConfig contains server-specific configuration
type ServerConfig struct {
	Port               int      `mapstructure:"port"`
	WorkingDir         string   `mapstructure:"working_dir"`
	Plugins            []string `mapstructure:"plugins"`
	Username           string   `mapstructure:"username"`
	UserID             int      `mapstructure:"user_id"`
	BrowserGymEvalEnv  string   `mapstructure:"browsergym_eval_env"`
	SessionAPIKey      string   `mapstructure:"session_api_key"`
	FileViewerPort     int      `mapstructure:"file_viewer_port"`
	MaxMemoryGB        int      `mapstructure:"max_memory_gb"`
	NoChangeTimeoutSec int      `mapstructure:"no_change_timeout_seconds"`
}

// TelemetryConfig contains telemetry configuration
type TelemetryConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	Endpoint string `mapstructure:"endpoint"`
}

// LogConfig contains logging configuration
type LogConfig struct {
	Level string `mapstructure:"level"`
	JSON  bool   `mapstructure:"json"`
}

// Load loads the configuration from viper
func Load() (*Config, error) {
	cfg := &Config{}

	// Set defaults
	setDefaults()

	// Unmarshal configuration
	if err := viper.Unmarshal(cfg); err != nil {
		return nil, err
	}

	// Post-process configuration
	if err := postProcess(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

func setDefaults() {
	// Server defaults
	viper.SetDefault("server.port", 8000)
	viper.SetDefault("server.username", "openhands")
	viper.SetDefault("server.user_id", 1000)
	viper.SetDefault("server.file_viewer_port", 0) // Auto-assign
	viper.SetDefault("server.max_memory_gb", 0)    // No limit
	viper.SetDefault("server.no_change_timeout_seconds", 10)

	// Telemetry defaults
	viper.SetDefault("telemetry.enabled", true)

	// Log defaults
	viper.SetDefault("log.level", "info")
	viper.SetDefault("log.json", false)

	// Environment variable mappings
	viper.BindEnv("server.session_api_key", "SESSION_API_KEY")
	viper.BindEnv("server.max_memory_gb", "RUNTIME_MAX_MEMORY_GB")
	viper.BindEnv("server.no_change_timeout_seconds", "NO_CHANGE_TIMEOUT_SECONDS")
	viper.BindEnv("telemetry.endpoint", "OTEL_EXPORTER_OTLP_ENDPOINT")
}

func postProcess(cfg *Config) error {
	// Set working directory to current directory if not specified
	if cfg.Server.WorkingDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		cfg.Server.WorkingDir = wd
	}

	// Ensure working directory is absolute
	if !filepath.IsAbs(cfg.Server.WorkingDir) {
		abs, err := filepath.Abs(cfg.Server.WorkingDir)
		if err != nil {
			return err
		}
		cfg.Server.WorkingDir = abs
	}

	// Get session API key from environment if not set
	if cfg.Server.SessionAPIKey == "" {
		cfg.Server.SessionAPIKey = os.Getenv("SESSION_API_KEY")
	}

	return nil
}