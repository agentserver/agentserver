//go:build integration
// +build integration

package ccappgateway_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/ccappgateway"
	"github.com/agentserver/agentserver/internal/ccappgateway/runner"
)

// TestIntegration_EnvMcp_ListEnvironments tests the full Phase 3 env-MCP path:
//
//	cc-app-gateway server
//	  → real claude binary (with MCP config pointing at codex-app-gateway env-mcp)
//	  → codex-app-gateway env-mcp child
//	  → mock codex-exec-gateway (serves /api/exec-gateway/connected)
//
// The test asks claude to call list_environments and asserts the response
// contains the mock env's name or id.
//
// Prerequisites (t.Skip if missing):
//   - claude binary in PATH
//   - ANTHROPIC_BASE_URL and ANTHROPIC_AUTH_TOKEN set
func TestIntegration_EnvMcp_ListEnvironments(t *testing.T) {
	anthropicBaseURL := os.Getenv("ANTHROPIC_BASE_URL")
	anthropicAuthToken := os.Getenv("ANTHROPIC_AUTH_TOKEN")
	if anthropicBaseURL == "" || anthropicAuthToken == "" {
		t.Skip("ANTHROPIC_BASE_URL + ANTHROPIC_AUTH_TOKEN required for Phase 3 integration test")
	}
	claudeBin, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude binary not in PATH")
	}

	// 1. Build codex-app-gateway binary into a temp dir (provides the env-mcp subcommand).
	binDir := t.TempDir()
	envMcpBin := filepath.Join(binDir, "codex-app-gateway")
	repoRoot := findRepoRootPhase3(t)
	buildCmd := exec.Command("go", "build", "-o", envMcpBin, "./cmd/codex-app-gateway")
	buildCmd.Dir = repoRoot
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build codex-app-gateway: %v\n%s", err, out)
	}

	// 2. Mock codex-exec-gateway: serves GET /api/exec-gateway/connected
	//    returning a fake environment list.
	//
	//    The nameresolver expects a bare JSON array of ConnectedEntry objects
	//    (not a wrapper object): []{"exe_id","name","description","is_default"}.
	mockExecGw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("mock-exec-gw: %s %s", r.Method, r.URL.Path)
		if r.URL.Path == "/api/exec-gateway/connected" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			// Bare array — matches nameresolver.ConnectedEntry JSON shape.
			json.NewEncoder(w).Encode([]map[string]any{ //nolint:errcheck
				{"exe_id": "env_test123", "name": "test-env", "description": "integration test env", "is_default": true},
			})
			return
		}
		t.Logf("mock-exec-gw: 404 for %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}))
	defer mockExecGw.Close()

	// 3. Mock agentserver-main: serves POST /internal/workspace-token.
	//    Returns the real ANTHROPIC_AUTH_TOKEN as the workspace token so that
	//    the claude subprocess gets a valid credential for the real API.
	mockAgsv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("mock-agsv: %s %s", r.Method, r.URL.Path)
		if r.URL.Path == "/internal/workspace-token" && r.Method == http.MethodPost {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"token": anthropicAuthToken}) //nolint:errcheck
			return
		}
		t.Logf("mock-agsv: 404 for %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}))
	defer mockAgsv.Close()

	// 4. Build ServeConfig directly (avoid S3 + LoadServeConfigFromEnv complexity).
	//    Phase 3 requires CapTokenTTL > TurnTimeout.
	turnTimeout := 90 * time.Second
	capTokenTTL := 2 * time.Hour
	cfg := ccappgateway.ServeConfig{
		ListenAddr:             "127.0.0.1:0",
		ClaudeBin:              claudeBin,
		InternalSecret:         "test-internal-secret",
		AgentserverInternalURL: mockAgsv.URL,
		LLMProxyURL:            anthropicBaseURL,
		DefaultModel:           "claude-haiku-4-5",
		TurnTimeout:            turnTimeout,
		TmpRoot:                t.TempDir(),
		// Phase 3 fields
		EnvMcpBinary:           envMcpBin,
		ExecGatewayWSURL:       strings.Replace(mockExecGw.URL, "http://", "ws://", 1),
		ExecGatewayInternalURL: mockExecGw.URL,
		CapTokenHMACSecret:     []byte("integration-test-hmac-secret-32b!"),
		CapTokenTTL:            capTokenTTL,
	}

	// 5. Wire up the server with a real runner.Run and no-op store (no S3 needed).
	srv, err := ccappgateway.NewServerWithRunnerAndStore(cfg, runner.Run, newFakeStore())
	if err != nil {
		t.Fatalf("NewServerWithRunnerAndStore: %v", err)
	}

	// 6. POST /api/turns: ask claude to call list_environments.
	reqBody := `{
		"workspaceId": "wsp_phase3test",
		"sessionId":   "00000000-0000-0000-0000-000000000003",
		"userMessage": "Please call the list_environments tool and tell me what environments exist. Include each environment name and id in your reply.",
		"model":       "claude-haiku-4-5"
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/turns", strings.NewReader(reqBody))
	req.Header.Set("X-Internal-Secret", "test-internal-secret")
	req.Header.Set("Content-Type", "application/json")

	// Apply a generous timeout so the test doesn't hang forever.
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	// 7. Assertions.
	if rec.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		AssistantText string `json:"assistantText"`
		IsError       bool   `json:"isError"`
		ErrorMessage  string `json:"errorMessage,omitempty"`
		SessionID     string `json:"sessionId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response body: %v\nraw: %s", err, rec.Body.String())
	}

	t.Logf("assistantText: %q", resp.AssistantText)
	t.Logf("isError: %v errorMessage: %q", resp.IsError, resp.ErrorMessage)

	if resp.IsError {
		t.Errorf("turn returned isError=true; errorMessage=%q; assistantText=%q",
			resp.ErrorMessage, resp.AssistantText)
	}

	// Check that the response mentions the mock env's name or id.
	lower := strings.ToLower(resp.AssistantText)
	if !strings.Contains(lower, "test-env") && !strings.Contains(resp.AssistantText, "env_test123") {
		t.Errorf("expected mock env name %q or id %q in response; got: %q",
			"test-env", "env_test123", resp.AssistantText)
	}
}

// findRepoRootPhase3 walks up from cwd until it finds a go.mod file.
func findRepoRootPhase3(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	for d := cwd; d != "/"; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
	}
	t.Fatal("could not find repo root (no go.mod found)")
	return ""
}
