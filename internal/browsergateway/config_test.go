package browsergateway

import (
	"log/slog"
	"testing"
)

func TestLoadServeConfigFromEnv_Defaults(t *testing.T) {
	t.Setenv("BRG_CODEX_APP_GATEWAY_WS_URL", "ws://cxg:8086")
	cfg, err := LoadServeConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if cfg.ListenAddr != ":8088" {
		t.Errorf("ListenAddr = %q, want :8088", cfg.ListenAddr)
	}
	if cfg.CodexAppGatewayWSURL != "ws://cxg:8086" {
		t.Errorf("CodexAppGatewayWSURL = %q", cfg.CodexAppGatewayWSURL)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want Info", cfg.LogLevel)
	}
}

func TestLoadServeConfigFromEnv_RequiresWSURL(t *testing.T) {
	t.Setenv("BRG_CODEX_APP_GATEWAY_WS_URL", "")
	if _, err := LoadServeConfigFromEnv(); err == nil {
		t.Fatal("expected error when BRG_CODEX_APP_GATEWAY_WS_URL is unset")
	}
}

func TestLoadServeConfigFromEnv_OriginsAndLevel(t *testing.T) {
	t.Setenv("BRG_CODEX_APP_GATEWAY_WS_URL", "ws://cxg:8086")
	t.Setenv("BRG_ALLOWED_ORIGINS", "https://a.example, https://b.example")
	t.Setenv("BRG_LOG_LEVEL", "debug")
	cfg, err := LoadServeConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(cfg.AllowedOrigins) != 2 || cfg.AllowedOrigins[0] != "https://a.example" || cfg.AllowedOrigins[1] != "https://b.example" {
		t.Errorf("AllowedOrigins = %#v", cfg.AllowedOrigins)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v, want Debug", cfg.LogLevel)
	}
}
