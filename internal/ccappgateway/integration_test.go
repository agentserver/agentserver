//go:build integration

package ccappgateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	testdataDir = "testdata/integration"
	gatewayURL  = "http://localhost:8087"
)

func TestIntegration_HappyPath(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}

	// Resolve absolute path to testdata dir from the test's cwd.
	abs, err := filepath.Abs(testdataDir)
	if err != nil {
		t.Fatal(err)
	}

	// Build images first (idempotent; uses cached layers on repeat runs).
	runMake(t, abs, "build")

	// Bring up the stack.
	runMake(t, abs, "up")
	t.Cleanup(func() { runMakeBestEffort(t, abs, "down") })

	// Wait for cc-app-gateway readyz.
	waitForReadyz(t, gatewayURL+"/readyz", 60*time.Second)

	// Drive the gateway.
	body := []byte(`{
		"workspaceId": "ws_integration_test",
		"sessionId":   "00000000-0000-4000-8000-000000000001",
		"userMessage": "Say only the word: pong",
		"model":       "claude-haiku-4-5"
	}`)
	req, err := http.NewRequest("POST", gatewayURL+"/api/turns", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Secret", "secret123")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		runMakeBestEffort(t, abs, "logs")
		t.Fatalf("POST /api/turns: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		rawBody, _ := io.ReadAll(resp.Body)
		runMakeBestEffort(t, abs, "logs")
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(rawBody))
	}

	var got struct {
		SessionID     string `json:"sessionId"`
		AssistantText string `json:"assistantText"`
		IsError       bool   `json:"isError"`
		DurationMs    int64  `json:"durationMs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// The fake llmproxy returns "pong" as the assistant's text content.
	// claude --print extracts and reports that text. Assert exact match.
	if got.AssistantText != "pong" {
		runMakeBestEffort(t, abs, "logs")
		t.Errorf("assistantText: want %q, got %q", "pong", got.AssistantText)
	}
	if got.SessionID != "00000000-0000-4000-8000-000000000001" {
		t.Errorf("sessionId mismatch: %q", got.SessionID)
	}
	if got.IsError {
		t.Errorf("isError unexpectedly true")
	}
	if got.DurationMs <= 0 {
		t.Errorf("durationMs should be positive, got %d", got.DurationMs)
	}
	t.Logf("PASS: assistantText=%q sessionId=%s durationMs=%d", got.AssistantText, got.SessionID, got.DurationMs)
}

func TestIntegration_ResumeAcrossTurns(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}

	abs, err := filepath.Abs(testdataDir)
	if err != nil {
		t.Fatal(err)
	}
	runMake(t, abs, "up")
	t.Cleanup(func() { runMakeBestEffort(t, abs, "down") })
	waitForReadyz(t, gatewayURL+"/readyz", 90*time.Second)

	sessionID := "00000000-0000-4000-8000-00000000beef"

	// Turn 1: tell claude a fact.
	turn1Body := fmt.Sprintf(`{
		"workspaceId": "ws_resume_test",
		"sessionId":   %q,
		"userMessage": "Remember this code: ALPHA-7."
	}`, sessionID)
	resp1 := doTurnRequest(t, turn1Body)
	defer resp1.Body.Close()
	if resp1.StatusCode != 200 {
		rawBody, _ := io.ReadAll(resp1.Body)
		runMakeBestEffort(t, abs, "logs")
		t.Fatalf("turn 1 status: %d body: %s", resp1.StatusCode, rawBody)
	}
	// Drain body so the connection is reusable.
	io.Copy(io.Discard, resp1.Body) //nolint:errcheck

	// Turn 2: same sessionID — claude should send the conversation history including turn 1.
	turn2Body := fmt.Sprintf(`{
		"workspaceId": "ws_resume_test",
		"sessionId":   %q,
		"userMessage": "Recall the code."
	}`, sessionID)
	resp2 := doTurnRequest(t, turn2Body)
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		rawBody, _ := io.ReadAll(resp2.Body)
		runMakeBestEffort(t, abs, "logs")
		t.Fatalf("turn 2 status: %d body: %s", resp2.StatusCode, rawBody)
	}
	io.Copy(io.Discard, resp2.Body) //nolint:errcheck

	// Inspect fake-llmproxy's request log: turn 2's request body MUST include
	// turn 1's user message text "ALPHA-7", proving claude resumed and sent
	// the full conversation history (not that the LLM "remembers" — we use a
	// fake LLM).
	logContent := readFakeLLMProxyLog(t, abs)
	t.Logf("fake-llmproxy log content:\n%s", logContent)
	if !strings.Contains(logContent, "ALPHA-7") {
		runMakeBestEffort(t, abs, "logs")
		t.Errorf("fake-llmproxy log should contain 'ALPHA-7' from turn 1 history; log content:\n%s", logContent)
	}
}

func doTurnRequest(t *testing.T, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("POST", gatewayURL+"/api/turns", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Secret", "secret123")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("turn request: %v", err)
	}
	return resp
}

func readFakeLLMProxyLog(t *testing.T, testdataAbs string) string {
	t.Helper()
	// Use `docker compose exec` to read the log file from inside the container.
	cmd := exec.Command("docker", "compose", "-f", filepath.Join(testdataAbs, "docker-compose.yml"),
		"exec", "-T", "fake-llmproxy", "cat", "/tmp/llmproxy-requests.log")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("read log: %v (output: %s)", err, out)
		return ""
	}
	return string(out)
}

// runMake calls `make <target>` in dir; fails the test on non-zero exit.
// It streams stdout+stderr to the test log so failures are actionable.
func runMake(t *testing.T, dir, target string) {
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

// runMakeBestEffort runs make <target> but does NOT fail the test on error.
// Use for diagnostic calls (e.g., "logs") and cleanup callbacks where
// t.Fatal would panic or mask the original failure.
func runMakeBestEffort(t *testing.T, dir, target string) {
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

// waitForReadyz polls url every second until it returns 200 or the deadline passes.
// On timeout it prints the last response body and fails the test.
func waitForReadyz(t *testing.T, url string, deadline time.Duration) {
	t.Helper()
	start := time.Now()
	var lastBody string
	var lastStatus int
	client := &http.Client{Timeout: 2 * time.Second}

	for {
		elapsed := time.Since(start)
		if elapsed > deadline {
			t.Fatalf("readyz never became healthy after %v; last status=%d body=%s", deadline, lastStatus, lastBody)
		}

		resp, err := client.Get(url)
		if err == nil {
			raw, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastBody = string(raw)
			lastStatus = resp.StatusCode
			if resp.StatusCode == http.StatusOK {
				t.Logf("readyz OK after %v", elapsed.Round(time.Millisecond))
				return
			}
		} else {
			lastBody = fmt.Sprintf("error: %v", err)
		}

		t.Logf("readyz not ready yet (%v elapsed): %s", elapsed.Round(time.Millisecond), lastBody)
		time.Sleep(1 * time.Second)
	}
}
