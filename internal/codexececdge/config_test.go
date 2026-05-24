package codexececdge

import (
	"testing"
	"time"
)

func TestLoadConfigFromEnv_Defaults(t *testing.T) {
	t.Setenv("CXE_UPSTREAM_BASE_URL", "http://upstream:6060")
	t.Setenv("CXE_AGENTSERVER_INTERNAL_SECRET", "s")
	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("LoadConfigFromEnv: %v", err)
	}
	if cfg.Port != "6061" {
		t.Errorf("Port default: got %q", cfg.Port)
	}
	if cfg.RegisterRetryTotalTimeout != 30*time.Second {
		t.Errorf("RegisterRetryTotalTimeout default: got %v", cfg.RegisterRetryTotalTimeout)
	}
	if cfg.RegisterRetryInitialBackoff != 500*time.Millisecond {
		t.Errorf("RegisterRetryInitialBackoff default: got %v", cfg.RegisterRetryInitialBackoff)
	}
	if cfg.UpstreamDialTimeout != 5*time.Second {
		t.Errorf("UpstreamDialTimeout default: got %v", cfg.UpstreamDialTimeout)
	}
}

func TestLoadConfigFromEnv_RequiresUpstream(t *testing.T) {
	t.Setenv("CXE_UPSTREAM_BASE_URL", "")
	t.Setenv("CXE_AGENTSERVER_INTERNAL_SECRET", "s")
	if _, err := LoadConfigFromEnv(); err == nil {
		t.Fatal("expected error for missing CXE_UPSTREAM_BASE_URL")
	}
}

func TestLoadConfigFromEnv_RequiresSecret(t *testing.T) {
	t.Setenv("CXE_UPSTREAM_BASE_URL", "http://upstream:6060")
	t.Setenv("CXE_AGENTSERVER_INTERNAL_SECRET", "")
	if _, err := LoadConfigFromEnv(); err == nil {
		t.Fatal("expected error for missing CXE_AGENTSERVER_INTERNAL_SECRET")
	}
}
