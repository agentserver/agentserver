package audit_test

import (
	"context"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/codexexecgateway/audit"
)

func TestNoopRecorder_AllMethodsAreSafe(t *testing.T) {
	r := audit.NewNoopRecorder()
	sid, err := r.SessionOpen(audit.SessionMeta{
		WorkspaceID: "ws", ExeID: "exe", StreamID: "s1",
		OpenedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("noop SessionOpen unexpectedly returned err: %v", err)
	}
	if sid == "" {
		t.Fatal("expected non-empty session id even from noop")
	}
	r.OnFrameToBackend(sid, nil, nil)
	r.OnFrameToClient(sid, nil, nil)
	cid, err := r.CallStart(audit.CallStartMeta{
		Source:      "rest",
		WorkspaceID: "ws",
		ExeID:       "exe",
		StartedAt:   time.Now(),
	})
	if err != nil {
		t.Fatalf("noop CallStart unexpectedly returned err: %v", err)
	}
	if cid == "" {
		t.Fatal("expected non-empty call id even from noop")
	}
	r.CallEnd(cid, audit.CallEndMeta{CompletedAt: time.Now()})
	r.SessionClose(sid, "ok", audit.Counters{})
	if err := r.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestNewConfigFromEnv_Defaults(t *testing.T) {
	// All CXG_AUDIT_* vars unset → defaults.
	cfg := audit.NewConfigFromEnv()
	if cfg.Enabled {
		t.Errorf("Enabled should default to false; got true")
	}
	if cfg.WALDir != "/var/cxg-audit" {
		t.Errorf("WALDir default: got %q", cfg.WALDir)
	}
	if cfg.PayloadMaxBytes != 4*1024*1024 {
		t.Errorf("PayloadMaxBytes default: got %d", cfg.PayloadMaxBytes)
	}
	if cfg.RPCPairTimeout != 30*time.Second {
		t.Errorf("RPCPairTimeout default: got %v", cfg.RPCPairTimeout)
	}
}

func TestNewConfigFromEnv_OverridesViaEnv(t *testing.T) {
	t.Setenv("CXG_AUDIT_ENABLED", "true")
	t.Setenv("CXG_AUDIT_WAL_DIR", "/custom")
	t.Setenv("CXG_AUDIT_PAYLOAD_MAX_BYTES", "1024")
	t.Setenv("CXG_AUDIT_RPC_PAIR_TIMEOUT", "5s")
	cfg := audit.NewConfigFromEnv()
	if !cfg.Enabled {
		t.Error("Enabled: want true")
	}
	if cfg.WALDir != "/custom" {
		t.Errorf("WALDir: got %q", cfg.WALDir)
	}
	if cfg.PayloadMaxBytes != 1024 {
		t.Errorf("PayloadMaxBytes: got %d", cfg.PayloadMaxBytes)
	}
	if cfg.RPCPairTimeout != 5*time.Second {
		t.Errorf("RPCPairTimeout: got %v", cfg.RPCPairTimeout)
	}
}
