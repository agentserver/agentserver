package browsergateway

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// ServeConfig is browser-gateway's runtime configuration, sourced from BRG_* env.
type ServeConfig struct {
	ListenAddr           string
	CodexAppGatewayWSURL string // base URL of codex-app-gateway, e.g. ws://codex-app-gateway:8086
	AllowedOrigins       []string
	LogLevel             slog.Level
}

// LoadServeConfigFromEnv reads BRG_* env vars. BRG_CODEX_APP_GATEWAY_WS_URL is required.
func LoadServeConfigFromEnv() (ServeConfig, error) {
	cfg := ServeConfig{
		ListenAddr:           envOr("BRG_LISTEN_ADDR", ":8088"),
		CodexAppGatewayWSURL: os.Getenv("BRG_CODEX_APP_GATEWAY_WS_URL"),
		LogLevel:             slog.LevelInfo,
	}
	if v := os.Getenv("BRG_ALLOWED_ORIGINS"); v != "" {
		for _, o := range strings.Split(v, ",") {
			if o = strings.TrimSpace(o); o != "" {
				cfg.AllowedOrigins = append(cfg.AllowedOrigins, o)
			}
		}
	}
	switch strings.ToLower(os.Getenv("BRG_LOG_LEVEL")) {
	case "debug":
		cfg.LogLevel = slog.LevelDebug
	case "warn":
		cfg.LogLevel = slog.LevelWarn
	case "error":
		cfg.LogLevel = slog.LevelError
	}
	if cfg.CodexAppGatewayWSURL == "" {
		return cfg, fmt.Errorf("BRG_CODEX_APP_GATEWAY_WS_URL is required")
	}
	return cfg, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
