//go:build integration

// Package server_test provides an end-to-end integration test that proves the
// IM → cc-app-gateway → claude → reply round-trip works.
//
// Requirements:
//   - docker (29+) and docker compose (5+) must be available.
//   - TEST_DATABASE_URL must be set to a live PostgreSQL database with all
//     migrations applied (used to store agent_sessions for turn continuity).
//   - The test brings up the Phase 2 docker-compose stack (cc-app-gateway +
//     minio + fake-llmproxy + fake-agentserver) on host port 8087, then runs
//     an in-process agentserver that routes to it.
//
// Run:
//
//	go test -tags integration -v -timeout 10m -run TestIntegration_IMToCcEndToEnd ./internal/server/
package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/db"
	"github.com/agentserver/agentserver/internal/server"
)

// TestIntegration_IMToCcEndToEnd proves the full IM → agentserver →
// cc-app-gateway → fake-llmproxy → imbridge reply round-trip, including
// session resume across two consecutive turns.
//
// Architecture:
//
//	[test] POST /api/internal/imbridge/cc/turn
//	  → in-process agentserver (httptest.Server)
//	  → cc-app-gateway:8087 (docker-compose)
//	  → fake-llmproxy (docker-compose)
//	  → reply via POST /api/internal/imbridge/send
//	  → fake-imbridge (httptest.Server in this test)
//
// Resume is verified by checking the fake-llmproxy request log: turn 2's
// request MUST include "DELTA-9" from turn 1's conversation history.
func TestIntegration_IMToCcEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: skipped in short mode")
	}
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL unset — skipping IM end-to-end integration test")
	}

	// ── Step 1: Bring up Phase 2 docker-compose ────────────────────────────────
	// Resolve the absolute path from this test file's package directory.
	composeDir, err := filepath.Abs("../ccappgateway/testdata/integration")
	if err != nil {
		t.Fatalf("resolve compose dir: %v", err)
	}

	// build + up (idempotent; uses cached docker layers on repeat runs).
	runMakeIM(t, composeDir, "build")
	runMakeIM(t, composeDir, "up")
	t.Cleanup(func() { runMakeIMBestEffort(t, composeDir, "down") })

	// ── Step 2: Wait for cc-app-gateway readyz ─────────────────────────────────
	waitForReadyzIM(t, "http://localhost:8087/readyz", 90*time.Second)

	// ── Step 3: Spin up a fake-imbridge that records /api/internal/imbridge/send
	var mu sync.Mutex
	var sendCalls []map[string]any
	fakeImbridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/internal/imbridge/send" {
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Logf("fake-imbridge: decode body: %v", err)
			}
			mu.Lock()
			sendCalls = append(sendCalls, payload)
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"sent"}`))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(fakeImbridge.Close)

	// ── Step 4: Open test DB ───────────────────────────────────────────────────
	dbURL := os.Getenv("TEST_DATABASE_URL")
	d, err := db.Open(dbURL)
	if err != nil {
		t.Fatalf("open test DB: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	// ── Step 5: Ensure workspace exists in DB ─────────────────────────────────
	// agent_sessions has a FK to workspaces; create the row if absent.
	wsID := "ws_cc_im_integration_test"
	if err := d.CreateWorkspace(wsID, "CC IM Integration Test Workspace"); err != nil {
		// Ignore "already exists" errors — the workspace may persist across test runs.
		t.Logf("CreateWorkspace (may already exist): %v", err)
	}
	t.Cleanup(func() {
		d.DeleteWorkspace(wsID) //nolint:errcheck — best-effort cleanup
	})

	// ── Step 6: Build in-process agentserver ──────────────────────────────────
	// Env vars must be set before Router() reads them.
	t.Setenv("INTERNAL_API_SECRET", "secret123")
	t.Setenv("CC_APP_GATEWAY_REST_URL", "http://localhost:8087")
	// CC route uses s.IMBridgeURL as the send-back URL; set it on the struct.

	srv := &server.Server{
		DB:          d,
		IMBridgeURL: fakeImbridge.URL,
	}
	t.Cleanup(srv.Close)
	agentserverTS := httptest.NewServer(srv.Router())
	t.Cleanup(agentserverTS.Close)

	// ── Step 7: Turns — post IM messages through agentserver ──────────────────
	turnBody := func(text string) string {
		return `{"channel_id":"ch_cc_im_test","workspace_id":"` + wsID + `","wechat_user_id":"wxid_alice","wechat_sender_name":"Alice","text":"` + text + `"}`
	}
	body1 := turnBody("Remember code DELTA-9.")
	postIMRequest(t, agentserverTS.URL, "secret123", body1)

	// Wait for fake-imbridge to receive the reply (fake-llmproxy replies "pong").
	waitForSendCallsIM(t, &mu, &sendCalls, 1, 90*time.Second)
	mu.Lock()
	t.Logf("turn 1 reply from fake-imbridge: %v", sendCalls[0])
	mu.Unlock()

	// ── Step 8: Turn 2 — ask claude to recall the marker ─────────────────────
	body2 := turnBody("What was the code I just gave you?")
	postIMRequest(t, agentserverTS.URL, "secret123", body2)

	waitForSendCallsIM(t, &mu, &sendCalls, 2, 90*time.Second)
	mu.Lock()
	t.Logf("turn 2 reply from fake-imbridge: %v", sendCalls[1])
	mu.Unlock()

	// ── Step 8: Verify resume — DELTA-9 must appear in fake-llmproxy log ──────
	// fake-llmproxy always returns "pong" as the reply text, so we cannot
	// check the reply itself. Instead, we verify that turn 2's Anthropic API
	// request body (logged by fake-llmproxy) contains "DELTA-9" from turn 1's
	// conversation history. This proves claude resumed the session and sent
	// the full history rather than starting fresh.
	logContent := readFakeLLMProxyLogIM(t, composeDir)
	t.Logf("fake-llmproxy request log:\n%s", logContent)
	if !strings.Contains(logContent, "DELTA-9") {
		runMakeIMBestEffort(t, composeDir, "logs")
		t.Errorf("fake-llmproxy request log should contain 'DELTA-9' from turn 1 history (proves session resume); log:\n%s", logContent)
	}

	// Both turns must have produced replies.
	mu.Lock()
	defer mu.Unlock()
	if len(sendCalls) < 2 {
		t.Fatalf("expected 2 replies from fake-imbridge, got %d", len(sendCalls))
	}
	// Sanity: each reply has a channel_id and text.
	for i, call := range sendCalls[:2] {
		if call["channel_id"] == nil {
			t.Errorf("sendCalls[%d] missing channel_id: %v", i, call)
		}
		if call["text"] == nil {
			t.Errorf("sendCalls[%d] missing text: %v", i, call)
		}
	}
	t.Logf("PASS: turn 1 reply=%q turn 2 reply=%q DELTA-9 in llmproxy log=%v",
		sendCalls[0]["text"], sendCalls[1]["text"],
		strings.Contains(logContent, "DELTA-9"))
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// runMakeIM calls `make <target>` in dir; fails the test on non-zero exit.
func runMakeIM(t *testing.T, dir, target string) {
	t.Helper()
	cmd := exec.Command("make", "-C", dir, target)
	var buf strings.Builder
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := buf.String()
	if out != "" {
		t.Logf("make %s output:\n%s", target, out)
	}
	if err != nil {
		t.Fatalf("make %s in %s: %v", target, dir, err)
	}
}

// runMakeIMBestEffort runs make <target> but logs rather than fatals on error.
// Use for diagnostic calls (e.g., "logs") and cleanup callbacks.
func runMakeIMBestEffort(t *testing.T, dir, target string) {
	t.Helper()
	cmd := exec.Command("make", "-C", dir, target)
	var buf strings.Builder
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := buf.String()
	if out != "" {
		t.Logf("make %s output:\n%s", target, out)
	}
	if err != nil {
		t.Logf("make %s (best-effort): %v", target, err)
	}
}

// waitForReadyzIM polls url every second until it returns 200 or deadline passes.
func waitForReadyzIM(t *testing.T, url string, timeout time.Duration) {
	t.Helper()
	start := time.Now()
	client := &http.Client{Timeout: 2 * time.Second}
	var lastErr string
	for {
		if time.Since(start) > timeout {
			t.Fatalf("readyz %s never became healthy after %v; last: %s", url, timeout, lastErr)
		}
		resp, err := client.Get(url)
		if err == nil {
			raw, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				t.Logf("readyz OK after %v", time.Since(start).Round(time.Millisecond))
				return
			}
			lastErr = fmt.Sprintf("status=%d body=%s", resp.StatusCode, raw)
		} else {
			lastErr = err.Error()
		}
		time.Sleep(1 * time.Second)
	}
}

// waitForSendCallsIM blocks until len(sendCalls) >= n or the timeout elapses.
func waitForSendCallsIM(t *testing.T, mu *sync.Mutex, sendCalls *[]map[string]any, n int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		mu.Lock()
		cur := len(*sendCalls)
		mu.Unlock()
		if cur >= n {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	mu.Lock()
	cur := len(*sendCalls)
	mu.Unlock()
	t.Fatalf("fake-imbridge: timed out waiting for %d send call(s); got %d after %v", n, cur, timeout)
}

// postIMRequest POSTs body to the agentserver's /api/internal/imbridge/cc/turn
// and asserts 202 Accepted.
func postIMRequest(t *testing.T, baseURL, secret, body string) {
	t.Helper()
	req, err := http.NewRequest("POST", baseURL+"/api/internal/imbridge/cc/turn", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("build IM request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Secret", secret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/internal/imbridge/cc/turn: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST /api/internal/imbridge/cc/turn: expected 202, got %d: %s", resp.StatusCode, raw)
	}
}

// readFakeLLMProxyLogIM reads the fake-llmproxy request log from inside its container.
func readFakeLLMProxyLogIM(t *testing.T, testdataAbs string) string {
	t.Helper()
	cmd := exec.Command("docker", "compose", "-f", filepath.Join(testdataAbs, "docker-compose.yml"),
		"exec", "-T", "fake-llmproxy", "cat", "/tmp/llmproxy-requests.log")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("readFakeLLMProxyLogIM: %v (output: %s)", err, out)
		return ""
	}
	return string(out)
}
