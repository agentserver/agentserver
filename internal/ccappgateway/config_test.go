package ccappgateway

import (
	"os"
	"strings"
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
				os.Setenv("CCAPPGW_S3_ENDPOINT", "http://s3:9000")
				os.Setenv("CCAPPGW_S3_REGION", "us-east-1")
				os.Setenv("CCAPPGW_S3_BUCKET", "my-bucket")
				os.Setenv("CCAPPGW_S3_PATH_STYLE", "true")
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
				os.Unsetenv("CCAPPGW_S3_ENDPOINT")
				os.Unsetenv("CCAPPGW_S3_REGION")
				os.Unsetenv("CCAPPGW_S3_BUCKET")
				os.Unsetenv("CCAPPGW_S3_PATH_STYLE")
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
				if cfg.S3Endpoint != "http://s3:9000" {
					t.Errorf("S3Endpoint = %s, want http://s3:9000", cfg.S3Endpoint)
				}
				if cfg.S3Region != "us-east-1" {
					t.Errorf("S3Region = %s, want us-east-1", cfg.S3Region)
				}
				if cfg.S3Bucket != "my-bucket" {
					t.Errorf("S3Bucket = %s, want my-bucket", cfg.S3Bucket)
				}
				if !cfg.S3PathStyle {
					t.Errorf("S3PathStyle = %v, want true", cfg.S3PathStyle)
				}
			},
		},
		{
			name: "missing CCAPPGW_LLMPROXY_URL",
			setup: func() {
				os.Setenv("INTERNAL_API_SECRET", "secret123")
				os.Setenv("AGENTSERVER_INTERNAL_URL", "http://agentserver:8080")
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
			check: func(t *testing.T, cfg ServeConfig) {
				// Not called on error case, but check function signature for consistency
			},
		},
		{
			name: "missing INTERNAL_API_SECRET",
			setup: func() {
				os.Unsetenv("INTERNAL_API_SECRET")
				os.Setenv("AGENTSERVER_INTERNAL_URL", "http://agentserver:8080")
				os.Setenv("CCAPPGW_LLMPROXY_URL", "http://llmproxy:8081")
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
			check: func(t *testing.T, cfg ServeConfig) {
				// Not called on error case, but check function signature for consistency
			},
		},
		{
			name: "missing AGENTSERVER_INTERNAL_URL",
			setup: func() {
				os.Setenv("INTERNAL_API_SECRET", "secret123")
				os.Unsetenv("AGENTSERVER_INTERNAL_URL")
				os.Setenv("CCAPPGW_LLMPROXY_URL", "http://llmproxy:8081")
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
			check: func(t *testing.T, cfg ServeConfig) {
				// Not called on error case, but check function signature for consistency
			},
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
					return
				}
				// Verify that error message names the missing variable
				errMsg := err.Error()
				switch tt.name {
				case "missing CCAPPGW_LLMPROXY_URL":
					if !strings.Contains(errMsg, "CCAPPGW_LLMPROXY_URL") {
						t.Errorf("LoadServeConfigFromEnv() error message does not contain 'CCAPPGW_LLMPROXY_URL': %s", errMsg)
					}
				case "missing INTERNAL_API_SECRET":
					if !strings.Contains(errMsg, "INTERNAL_API_SECRET") {
						t.Errorf("LoadServeConfigFromEnv() error message does not contain 'INTERNAL_API_SECRET': %s", errMsg)
					}
				case "missing AGENTSERVER_INTERNAL_URL":
					if !strings.Contains(errMsg, "AGENTSERVER_INTERNAL_URL") {
						t.Errorf("LoadServeConfigFromEnv() error message does not contain 'AGENTSERVER_INTERNAL_URL': %s", errMsg)
					}
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

func TestLoadServeConfigFromEnv_S3Vars(t *testing.T) {
	// Set all required Phase 1 vars + S3 vars.
	setBaseEnv := func(t *testing.T) {
		t.Setenv("INTERNAL_API_SECRET", "secret")
		t.Setenv("AGENTSERVER_INTERNAL_URL", "http://a:8080")
		t.Setenv("CCAPPGW_LLMPROXY_URL", "http://l:8081")
	}

	t.Run("happy path with all S3 vars", func(t *testing.T) {
		setBaseEnv(t)
		t.Setenv("CCAPPGW_S3_ENDPOINT", "http://minio:9000")
		t.Setenv("CCAPPGW_S3_REGION", "us-east-1")
		t.Setenv("CCAPPGW_S3_BUCKET", "test-bucket")
		t.Setenv("CCAPPGW_S3_PATH_STYLE", "true")

		cfg, err := LoadServeConfigFromEnv(ServeFlags{})
		if err != nil {
			t.Fatalf("LoadServeConfigFromEnv: %v", err)
		}
		if cfg.S3Endpoint != "http://minio:9000" {
			t.Errorf("S3Endpoint: %q", cfg.S3Endpoint)
		}
		if cfg.S3Region != "us-east-1" {
			t.Errorf("S3Region: %q", cfg.S3Region)
		}
		if cfg.S3Bucket != "test-bucket" {
			t.Errorf("S3Bucket: %q", cfg.S3Bucket)
		}
		if !cfg.S3PathStyle {
			t.Errorf("S3PathStyle should be true")
		}
	})

	t.Run("S3_REGION required", func(t *testing.T) {
		setBaseEnv(t)
		t.Setenv("CCAPPGW_S3_BUCKET", "b")
		t.Setenv("CCAPPGW_S3_REGION", "")
		_, err := LoadServeConfigFromEnv(ServeFlags{})
		if err == nil || !strings.Contains(err.Error(), "CCAPPGW_S3_REGION") {
			t.Errorf("missing S3_REGION should error mentioning the var; got: %v", err)
		}
	})

	t.Run("S3_BUCKET required", func(t *testing.T) {
		setBaseEnv(t)
		t.Setenv("CCAPPGW_S3_REGION", "us-east-1")
		t.Setenv("CCAPPGW_S3_BUCKET", "")
		_, err := LoadServeConfigFromEnv(ServeFlags{})
		if err == nil || !strings.Contains(err.Error(), "CCAPPGW_S3_BUCKET") {
			t.Errorf("missing S3_BUCKET should error mentioning the var; got: %v", err)
		}
	})

	t.Run("PATH_STYLE defaults false", func(t *testing.T) {
		setBaseEnv(t)
		t.Setenv("CCAPPGW_S3_REGION", "us-east-1")
		t.Setenv("CCAPPGW_S3_BUCKET", "b")
		t.Setenv("CCAPPGW_S3_PATH_STYLE", "")
		cfg, err := LoadServeConfigFromEnv(ServeFlags{})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.S3PathStyle {
			t.Errorf("S3PathStyle should default false")
		}
	})
}

// Phase 3 tests for env-MCP config fields
func TestLoad_Phase3FieldsEmpty_BackwardCompat(t *testing.T) {
	// Set only required Phase 1/2 vars, leave Phase 3 vars empty
	t.Setenv("INTERNAL_API_SECRET", "secret")
	t.Setenv("AGENTSERVER_INTERNAL_URL", "http://a:8080")
	t.Setenv("CCAPPGW_LLMPROXY_URL", "http://l:8081")
	t.Setenv("CCAPPGW_S3_REGION", "us-east-1")
	t.Setenv("CCAPPGW_S3_BUCKET", "bucket")

	// Explicitly clear Phase 3 env vars
	t.Setenv("CCAPPGW_ENV_MCP_BINARY", "")
	t.Setenv("CCAPPGW_EXEC_GATEWAY_WS_URL", "")
	t.Setenv("CCAPPGW_EXEC_GATEWAY_INTERNAL_URL", "")
	t.Setenv("CCAPPGW_EXEC_GATEWAY_INTERNAL_SECRET", "")
	t.Setenv("CCAPPGW_CAPTOKEN_HMAC_SECRET", "")
	t.Setenv("CCAPPGW_CAPTOKEN_TTL", "")

	cfg, err := LoadServeConfigFromEnv(ServeFlags{})
	if err != nil {
		t.Fatalf("LoadServeConfigFromEnv: %v", err)
	}

	// All Phase 3 fields should be zero-valued
	if cfg.EnvMcpBinary != "" {
		t.Errorf("EnvMcpBinary should be empty, got %q", cfg.EnvMcpBinary)
	}
	if cfg.ExecGatewayWSURL != "" {
		t.Errorf("ExecGatewayWSURL should be empty, got %q", cfg.ExecGatewayWSURL)
	}
	if cfg.ExecGatewayInternalURL != "" {
		t.Errorf("ExecGatewayInternalURL should be empty, got %q", cfg.ExecGatewayInternalURL)
	}
	if cfg.ExecGatewayInternalSecret != "" {
		t.Errorf("ExecGatewayInternalSecret should be empty, got %q", cfg.ExecGatewayInternalSecret)
	}
	if len(cfg.CapTokenHMACSecret) != 0 {
		t.Errorf("CapTokenHMACSecret should be empty, got %d bytes", len(cfg.CapTokenHMACSecret))
	}
	if cfg.CapTokenTTL != time.Hour {
		t.Errorf("CapTokenTTL should be time.Hour, got %v", cfg.CapTokenTTL)
	}
}

func TestLoad_Phase3FieldsSet(t *testing.T) {
	// Set all required Phase 1/2 vars
	t.Setenv("INTERNAL_API_SECRET", "secret")
	t.Setenv("AGENTSERVER_INTERNAL_URL", "http://a:8080")
	t.Setenv("CCAPPGW_LLMPROXY_URL", "http://l:8081")
	t.Setenv("CCAPPGW_S3_REGION", "us-east-1")
	t.Setenv("CCAPPGW_S3_BUCKET", "bucket")

	// Set all Phase 3 vars
	t.Setenv("CCAPPGW_ENV_MCP_BINARY", "/usr/local/bin/codex-app-gateway")
	t.Setenv("CCAPPGW_EXEC_GATEWAY_WS_URL", "ws://codex-exec-gateway:6060")
	t.Setenv("CCAPPGW_EXEC_GATEWAY_INTERNAL_URL", "http://codex-exec-gateway:6060")
	t.Setenv("CCAPPGW_EXEC_GATEWAY_INTERNAL_SECRET", "secret-internal")
	t.Setenv("CCAPPGW_CAPTOKEN_HMAC_SECRET", "fake-secret-123")
	t.Setenv("CCAPPGW_CAPTOKEN_TTL", "2h")

	cfg, err := LoadServeConfigFromEnv(ServeFlags{})
	if err != nil {
		t.Fatalf("LoadServeConfigFromEnv: %v", err)
	}

	// Verify all Phase 3 fields are populated correctly
	if cfg.EnvMcpBinary != "/usr/local/bin/codex-app-gateway" {
		t.Errorf("EnvMcpBinary = %q, want /usr/local/bin/codex-app-gateway", cfg.EnvMcpBinary)
	}
	if cfg.ExecGatewayWSURL != "ws://codex-exec-gateway:6060" {
		t.Errorf("ExecGatewayWSURL = %q, want ws://codex-exec-gateway:6060", cfg.ExecGatewayWSURL)
	}
	if cfg.ExecGatewayInternalURL != "http://codex-exec-gateway:6060" {
		t.Errorf("ExecGatewayInternalURL = %q, want http://codex-exec-gateway:6060", cfg.ExecGatewayInternalURL)
	}
	if cfg.ExecGatewayInternalSecret != "secret-internal" {
		t.Errorf("ExecGatewayInternalSecret = %q, want secret-internal", cfg.ExecGatewayInternalSecret)
	}
	if string(cfg.CapTokenHMACSecret) != "fake-secret-123" {
		t.Errorf("CapTokenHMACSecret = %q, want fake-secret-123", string(cfg.CapTokenHMACSecret))
	}
	if cfg.CapTokenTTL != 2*time.Hour {
		t.Errorf("CapTokenTTL = %v, want 2h", cfg.CapTokenTTL)
	}
}

func TestLoad_CapTokenTTL_Invalid(t *testing.T) {
	// Set all required Phase 1/2 vars
	t.Setenv("INTERNAL_API_SECRET", "secret")
	t.Setenv("AGENTSERVER_INTERNAL_URL", "http://a:8080")
	t.Setenv("CCAPPGW_LLMPROXY_URL", "http://l:8081")
	t.Setenv("CCAPPGW_S3_REGION", "us-east-1")
	t.Setenv("CCAPPGW_S3_BUCKET", "bucket")

	// Set invalid TTL
	t.Setenv("CCAPPGW_CAPTOKEN_TTL", "not-a-duration")

	_, err := LoadServeConfigFromEnv(ServeFlags{})
	if err == nil {
		t.Errorf("LoadServeConfigFromEnv should return error for invalid CCAPPGW_CAPTOKEN_TTL, got nil")
	}
	if !strings.Contains(err.Error(), "CCAPPGW_CAPTOKEN_TTL") {
		t.Errorf("error should mention CCAPPGW_CAPTOKEN_TTL, got: %v", err)
	}
}
