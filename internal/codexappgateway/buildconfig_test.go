package codexappgateway

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/codexexecgateway"
)

// stubTokenFetcher returns a fake workspace token; good enough for
// tests that don't care about the env value, only about the
// config.toml content.
type stubTokenFetcher struct{}

func (stubTokenFetcher) GetOrCreate(_ context.Context, _ string) (string, error) {
	return "stub-ws-token", nil
}

func newTestCfg() ServeConfig {
	return ServeConfig{
		ExecGatewayWSURL:       "ws://exec-gw:6060",
		ExecGatewayInternalURL: "http://exec-gw:8087",
		AgentserverInternalURL: "http://agentserver:8080",
		CapTokenHMACSecret:     []byte("cap-secret"),
		CapTokenTTL:            time.Minute,
		ModelProvider:          "modelserver",
		Model:                  "gpt-5.5",
		ModelProviderBaseURL:   "http://llmproxy:8085/v1",
		ModelProviderEnvKey:    "CODEX_API_KEY",
		ModelProviderWireAPI:   "responses",
		ListenAddr:             ":8086",
	}
}

func newDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
}

func TestBuildConfig_EmitsAgentserverMCPAndMintsWorkspaceToken(t *testing.T) {
	cfg := newTestCfg()
	build := makeBuildConfig(cfg, stubTokenFetcher{}, "/usr/local/bin/codex-app-gateway", newDiscardLogger())

	got, err := build(context.Background(), "ws_a", "u_test")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	m := got.Config.AgentServer
	if m.CodexBin != "/usr/local/bin/codex-app-gateway" {
		t.Errorf("CodexBin = %q", m.CodexBin)
	}
	if m.WorkspaceID != "ws_a" {
		t.Errorf("WorkspaceID = %q", m.WorkspaceID)
	}
	if m.ExecGatewayURL != "ws://exec-gw:6060/bridge" {
		t.Errorf("ExecGatewayURL = %q", m.ExecGatewayURL)
	}
	if m.ExecGatewayInternalURL != "http://exec-gw:8087" {
		t.Errorf("ExecGatewayInternalURL = %q", m.ExecGatewayInternalURL)
	}
	if m.AgentserverInternalURL != "http://agentserver:8080" {
		t.Errorf("AgentserverInternalURL = %q", m.AgentserverInternalURL)
	}
	p, err := codexexecgateway.VerifyCapabilityToken(m.WorkspaceToken, cfg.CapTokenHMACSecret)
	if err != nil {
		t.Fatalf("workspace token verify: %v", err)
	}
	if p.WorkspaceID != "ws_a" {
		t.Errorf("workspace token .workspace_id = %q", p.WorkspaceID)
	}
	if p.TurnID == "" {
		t.Error("workspace token .turn_id empty")
	}
}

func TestBuildConfig_RespectsConfiguredTrustedPaths(t *testing.T) {
	cfg := newTestCfg()
	cfg.ProjectTrustedPaths = []string{"/workspace", "/data"}
	build := makeBuildConfig(cfg, stubTokenFetcher{}, "/x", newDiscardLogger())
	got, err := build(context.Background(), "ws_a", "")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if strings.Join(got.Config.ProjectTrustedPaths, ",") != "/workspace,/data" {
		t.Errorf("trusted paths = %v", got.Config.ProjectTrustedPaths)
	}
}

// Note on deleted tests:
//   - TestLoopbackInternalURL — loopbackInternalURL helper is gone
//     with the loopback removal; no callers left.
//   - TestBuildConfig_NoExecGatewayFetchHappens — predated #240; the
//     ExecGatewayClient was deleted there.
