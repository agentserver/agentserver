package codexececdge

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

type Config struct {
	Port                        string
	UpstreamBaseURL             string
	AgentserverInternalSecret   string
	RegisterRetryTotalTimeout   time.Duration
	RegisterRetryInitialBackoff time.Duration
	UpstreamDialTimeout         time.Duration
	LogLevel                    slog.Level
}

func (c Config) Validate() error {
	if c.UpstreamBaseURL == "" {
		return fmt.Errorf("CXE_UPSTREAM_BASE_URL required")
	}
	if c.AgentserverInternalSecret == "" {
		return fmt.Errorf("CXE_AGENTSERVER_INTERNAL_SECRET required")
	}
	return nil
}

func LoadConfigFromEnv() (Config, error) {
	cfg := Config{
		Port:                        envOr("CXE_PORT", "6061"),
		UpstreamBaseURL:             os.Getenv("CXE_UPSTREAM_BASE_URL"),
		AgentserverInternalSecret:   os.Getenv("CXE_AGENTSERVER_INTERNAL_SECRET"),
		RegisterRetryTotalTimeout:   parseDurationOr("CXE_REGISTER_RETRY_TIMEOUT", 30*time.Second),
		RegisterRetryInitialBackoff: parseDurationOr("CXE_REGISTER_RETRY_BASE", 500*time.Millisecond),
		UpstreamDialTimeout:         parseDurationOr("CXE_UPSTREAM_DIAL_TIMEOUT", 5*time.Second),
		LogLevel:                    slog.LevelInfo,
	}
	if v := os.Getenv("CXE_LOG_LEVEL"); v != "" {
		switch strings.ToLower(v) {
		case "debug":
			cfg.LogLevel = slog.LevelDebug
		case "warn":
			cfg.LogLevel = slog.LevelWarn
		case "error":
			cfg.LogLevel = slog.LevelError
		}
	}
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseDurationOr(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}
