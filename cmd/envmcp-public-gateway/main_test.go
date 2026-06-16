package main

import (
	"strings"
	"testing"
)

// fullEnv is the canonical complete env-var set; every test starts
// from this and mutates one var to exercise the failure cases.
func fullEnv() map[string]string {
	return map[string]string{
		"DATABASE_URL":                     "postgres://x/y",
		"CXG_LISTEN_ADDR":                  ":8090",
		"CXG_CAPTOKEN_HMAC_SECRET":         "shhh",
		"CXG_EXEC_GATEWAY_INTERNAL_URL":    "http://exec-gw:6060",
		"CXG_EXEC_GATEWAY_INTERNAL_SECRET": "internal",
		"CXG_BRIDGE_BASE_URL":              "ws://exec-gw:6060/bridge",
		"MCP_PUBLIC_RESOURCE_METADATA_URL": "https://mcp.example.com/v1/.well-known/oauth-protected-resource",
		"MCP_PUBLIC_ISSUER_URL":            "https://app.example.com",
	}
}

func envFn(env map[string]string) func(string) string {
	return func(k string) string { return env[k] }
}

func TestLoadConfig_FullEnvSucceeds(t *testing.T) {
	cfg, err := loadConfigFromEnv(envFn(fullEnv()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DatabaseURL != "postgres://x/y" {
		t.Errorf("DatabaseURL: %q", cfg.DatabaseURL)
	}
	if cfg.ResourceMetadataURL == "" || cfg.IssuerURL == "" {
		t.Errorf("MCP_PUBLIC_* vars empty: %+v", cfg)
	}
}

// TestLoadConfig_AllRequiredVarsEnforced sweeps every required env
// var, deletes it, expects a fail-closed error naming the missing
// var. Catches the H2 regression where the doc said vars were
// required but loadConfig forgot to enforce them.
func TestLoadConfig_AllRequiredVarsEnforced(t *testing.T) {
	required := []string{
		"DATABASE_URL",
		"CXG_CAPTOKEN_HMAC_SECRET",
		"CXG_EXEC_GATEWAY_INTERNAL_URL",
		"CXG_EXEC_GATEWAY_INTERNAL_SECRET",
		"CXG_BRIDGE_BASE_URL",
		"MCP_PUBLIC_RESOURCE_METADATA_URL",
		"MCP_PUBLIC_ISSUER_URL",
	}
	for _, missing := range required {
		t.Run(missing, func(t *testing.T) {
			env := fullEnv()
			delete(env, missing)
			_, err := loadConfigFromEnv(envFn(env))
			if err == nil {
				t.Fatalf("missing %s: want error, got nil", missing)
			}
			if !strings.Contains(err.Error(), missing) {
				t.Errorf("error should name the missing var: %v", err)
			}
		})
	}
}

func TestLoadConfig_ListenAddrDefaultsTo8090(t *testing.T) {
	env := fullEnv()
	delete(env, "CXG_LISTEN_ADDR")
	cfg, err := loadConfigFromEnv(envFn(env))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ListenAddr != ":8090" {
		t.Errorf("ListenAddr default: got %q, want :8090", cfg.ListenAddr)
	}
}
