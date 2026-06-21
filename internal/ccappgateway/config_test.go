package ccappgateway

import (
	"os"
	"testing"
	"time"
)

func TestLoadServeConfigFromEnv(t *testing.T) {
	tests := []struct {
		name    string
		setup   func()
		cleanup func()
		flags   ServeFlags
		wantErr bool
		check   func(t *testing.T, cfg ServeConfig)
	}{
		{
			name: "all env vars set",
			setup: func() {
				os.Setenv("CCAPPGW_LISTEN_ADDR", ":8087")
				os.Setenv("CCAPPGW_CLAUDE_BIN", "/usr/local/bin/claude")
				os.Setenv("INTERNAL_API_SECRET", "secret123")
				os.Setenv("AGENTSERVER_INTERNAL_URL", "http://agentserver:8080")
				os.Setenv("CCAPPGW_LLMPROXY_URL", "http://llmproxy:8081")
				os.Setenv("CCAPPGW_DEFAULT_MODEL", "haiku")
				os.Setenv("CCAPPGW_TURN_TIMEOUT", "10m")
				os.Setenv("CCAPPGW_TMP_ROOT", "/tmp/cc-app-gateway")
				os.Setenv("CCAPPGW_LOG_LEVEL", "info")
			},
			cleanup: func() {
				os.Unsetenv("CCAPPGW_LISTEN_ADDR")
				os.Unsetenv("CCAPPGW_CLAUDE_BIN")
				os.Unsetenv("INTERNAL_API_SECRET")
				os.Unsetenv("AGENTSERVER_INTERNAL_URL")
				os.Unsetenv("CCAPPGW_LLMPROXY_URL")
				os.Unsetenv("CCAPPGW_DEFAULT_MODEL")
				os.Unsetenv("CCAPPGW_TURN_TIMEOUT")
				os.Unsetenv("CCAPPGW_TMP_ROOT")
				os.Unsetenv("CCAPPGW_LOG_LEVEL")
			},
			flags: ServeFlags{
				ListenAddr: ":8087",
				ClaudeBin:  "/usr/local/bin/claude",
			},
			wantErr: false,
			check: func(t *testing.T, cfg ServeConfig) {
				if cfg.ListenAddr != ":8087" {
					t.Errorf("ListenAddr = %s, want :8087", cfg.ListenAddr)
				}
				if cfg.ClaudeBin != "/usr/local/bin/claude" {
					t.Errorf("ClaudeBin = %s, want /usr/local/bin/claude", cfg.ClaudeBin)
				}
				if cfg.InternalSecret != "secret123" {
					t.Errorf("InternalSecret = %s, want secret123", cfg.InternalSecret)
				}
				if cfg.AgentserverInternalURL != "http://agentserver:8080" {
					t.Errorf("AgentserverInternalURL = %s, want http://agentserver:8080", cfg.AgentserverInternalURL)
				}
				if cfg.LLMProxyURL != "http://llmproxy:8081" {
					t.Errorf("LLMProxyURL = %s, want http://llmproxy:8081", cfg.LLMProxyURL)
				}
				if cfg.DefaultModel != "haiku" {
					t.Errorf("DefaultModel = %s, want haiku", cfg.DefaultModel)
				}
				if cfg.TurnTimeout != 10*time.Minute {
					t.Errorf("TurnTimeout = %v, want 10m", cfg.TurnTimeout)
				}
				if cfg.TmpRoot != "/tmp/cc-app-gateway" {
					t.Errorf("TmpRoot = %s, want /tmp/cc-app-gateway", cfg.TmpRoot)
				}
				if cfg.LogLevel != "info" {
					t.Errorf("LogLevel = %s, want info", cfg.LogLevel)
				}
			},
		},
		{
			name: "missing required var",
			setup: func() {
				os.Setenv("INTERNAL_API_SECRET", "secret123")
				os.Setenv("AGENTSERVER_INTERNAL_URL", "http://agentserver:8080")
				// CCAPPGW_LLMPROXY_URL is missing
				os.Unsetenv("CCAPPGW_LLMPROXY_URL")
			},
			cleanup: func() {
				os.Unsetenv("INTERNAL_API_SECRET")
				os.Unsetenv("AGENTSERVER_INTERNAL_URL")
				os.Unsetenv("CCAPPGW_LLMPROXY_URL")
				os.Unsetenv("CCAPPGW_DEFAULT_MODEL")
				os.Unsetenv("CCAPPGW_TURN_TIMEOUT")
				os.Unsetenv("CCAPPGW_TMP_ROOT")
				os.Unsetenv("CCAPPGW_LOG_LEVEL")
			},
			flags: ServeFlags{
				ListenAddr: ":8087",
				ClaudeBin:  "/usr/local/bin/claude",
			},
			wantErr: true,
		},
		{
			name: "invalid duration",
			setup: func() {
				os.Setenv("INTERNAL_API_SECRET", "secret123")
				os.Setenv("AGENTSERVER_INTERNAL_URL", "http://agentserver:8080")
				os.Setenv("CCAPPGW_LLMPROXY_URL", "http://llmproxy:8081")
				os.Setenv("CCAPPGW_TURN_TIMEOUT", "garbage")
			},
			cleanup: func() {
				os.Unsetenv("INTERNAL_API_SECRET")
				os.Unsetenv("AGENTSERVER_INTERNAL_URL")
				os.Unsetenv("CCAPPGW_LLMPROXY_URL")
				os.Unsetenv("CCAPPGW_TURN_TIMEOUT")
				os.Unsetenv("CCAPPGW_DEFAULT_MODEL")
				os.Unsetenv("CCAPPGW_TMP_ROOT")
				os.Unsetenv("CCAPPGW_LOG_LEVEL")
			},
			flags: ServeFlags{
				ListenAddr: ":8087",
				ClaudeBin:  "/usr/local/bin/claude",
			},
			wantErr: true,
		},
		{
			name: "invalid log level",
			setup: func() {
				os.Setenv("INTERNAL_API_SECRET", "secret123")
				os.Setenv("AGENTSERVER_INTERNAL_URL", "http://agentserver:8080")
				os.Setenv("CCAPPGW_LLMPROXY_URL", "http://llmproxy:8081")
				os.Setenv("CCAPPGW_LOG_LEVEL", "invalid")
			},
			cleanup: func() {
				os.Unsetenv("INTERNAL_API_SECRET")
				os.Unsetenv("AGENTSERVER_INTERNAL_URL")
				os.Unsetenv("CCAPPGW_LLMPROXY_URL")
				os.Unsetenv("CCAPPGW_LOG_LEVEL")
				os.Unsetenv("CCAPPGW_DEFAULT_MODEL")
				os.Unsetenv("CCAPPGW_TURN_TIMEOUT")
				os.Unsetenv("CCAPPGW_TMP_ROOT")
			},
			flags: ServeFlags{
				ListenAddr: ":8087",
				ClaudeBin:  "/usr/local/bin/claude",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup()
			defer tt.cleanup()

			cfg, err := LoadServeConfigFromEnv(tt.flags)
			if tt.wantErr {
				if err == nil {
					t.Errorf("LoadServeConfigFromEnv() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Errorf("LoadServeConfigFromEnv() unexpected error: %v", err)
				return
			}
			if tt.check != nil {
				tt.check(t, cfg)
			}
		})
	}
}
