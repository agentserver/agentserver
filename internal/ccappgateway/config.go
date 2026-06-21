package ccappgateway

import (
	"fmt"
	"os"
	"time"
)

// ServeConfig contains all configuration for the cc-app-gateway serve command.
type ServeConfig struct {
	ListenAddr             string
	ClaudeBin              string
	InternalSecret         string
	AgentserverInternalURL string
	LLMProxyURL            string
	DefaultModel           string
	TurnTimeout            time.Duration
	TmpRoot                string
	LogLevel               string
}

// ServeFlags represents parsed command-line flags for the serve subcommand.
type ServeFlags struct {
	ListenAddr string
	ClaudeBin  string
}

// LoadServeConfigFromEnv builds a ServeConfig from environment variables and flags.
func LoadServeConfigFromEnv(flags ServeFlags) (ServeConfig, error) {
	cfg := ServeConfig{
		ListenAddr: flags.ListenAddr,
		ClaudeBin:  flags.ClaudeBin,
	}

	// Load required env vars
	cfg.InternalSecret = os.Getenv("INTERNAL_API_SECRET")
	if cfg.InternalSecret == "" {
		return ServeConfig{}, fmt.Errorf("required env var INTERNAL_API_SECRET is empty")
	}

	cfg.AgentserverInternalURL = os.Getenv("AGENTSERVER_INTERNAL_URL")
	if cfg.AgentserverInternalURL == "" {
		return ServeConfig{}, fmt.Errorf("required env var AGENTSERVER_INTERNAL_URL is empty")
	}

	cfg.LLMProxyURL = os.Getenv("CCAPPGW_LLMPROXY_URL")
	if cfg.LLMProxyURL == "" {
		return ServeConfig{}, fmt.Errorf("required env var CCAPPGW_LLMPROXY_URL is empty")
	}

	// Load optional env vars with defaults
	cfg.DefaultModel = os.Getenv("CCAPPGW_DEFAULT_MODEL")
	if cfg.DefaultModel == "" {
		cfg.DefaultModel = "haiku"
	}

	turnTimeoutStr := os.Getenv("CCAPPGW_TURN_TIMEOUT")
	if turnTimeoutStr == "" {
		turnTimeoutStr = "10m"
	}
	timeout, err := time.ParseDuration(turnTimeoutStr)
	if err != nil {
		return ServeConfig{}, fmt.Errorf("failed to parse CCAPPGW_TURN_TIMEOUT %q: %w", turnTimeoutStr, err)
	}
	cfg.TurnTimeout = timeout

	cfg.TmpRoot = os.Getenv("CCAPPGW_TMP_ROOT")
	if cfg.TmpRoot == "" {
		cfg.TmpRoot = "/tmp/cc-app-gateway"
	}

	cfg.LogLevel = os.Getenv("CCAPPGW_LOG_LEVEL")
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}

	// Validate log level
	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLevels[cfg.LogLevel] {
		return ServeConfig{}, fmt.Errorf("invalid CCAPPGW_LOG_LEVEL %q: must be one of debug, info, warn, error", cfg.LogLevel)
	}

	return cfg, nil
}
