package runner

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// makeRunInput returns a RunInput pointing ClaudeBin at the fake claude shell
// wrapper so the subprocess re-execs TestHelperProcess.
func makeRunInput(t *testing.T, scenario string, timeout time.Duration) RunInput {
	t.Helper()
	bin := writeFakeClaudeScript(t)

	// These env vars must be in os.Environ() when BuildEnv is called so the
	// subprocess inherits them. t.Setenv patches the current process env.
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	t.Setenv("FAKECLAUDE_SCENARIO", scenario)

	return RunInput{
		ClaudeBin:   bin,
		ClaudeDir:   t.TempDir(),
		ProjectDir:  t.TempDir(),
		SessionID:   "test-session-id-1234",
		Model:       "claude-haiku-4-5",
		UserMessage: "hello",
		WSToken:     "fake-ws-token",
		LLMProxyURL: "http://fake-llmproxy:8081",
		Timeout:     timeout,
	}
}

// TestRun_HappyPath verifies the pong scenario returns the expected RunResult.
func TestRun_HappyPath(t *testing.T) {
	in := makeRunInput(t, "pong", 30*time.Second)
	ctx := context.Background()

	result, err := Run(ctx, in)
	if err != nil {
		t.Fatalf("Run returned unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("Run returned nil result")
	}

	if result.AssistantText != "pong" {
		t.Errorf("AssistantText = %q, want %q", result.AssistantText, "pong")
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
	if result.Meta == nil {
		t.Fatal("Meta is nil")
	}
	if result.Meta.Subtype != "success" {
		t.Errorf("Meta.Subtype = %q, want %q", result.Meta.Subtype, "success")
	}
	if result.Meta.IsError {
		t.Error("Meta.IsError = true, want false")
	}
	if result.Meta.DurationMs != 100 {
		t.Errorf("Meta.DurationMs = %d, want 100", result.Meta.DurationMs)
	}
	if result.DurationMs <= 0 {
		t.Errorf("DurationMs should be positive, got %d", result.DurationMs)
	}
}

// TestRun_NonZeroExit verifies that a subprocess crashing returns a non-nil error.
func TestRun_NonZeroExit(t *testing.T) {
	in := makeRunInput(t, "crash", 30*time.Second)
	ctx := context.Background()

	result, err := Run(ctx, in)
	// We expect either an error OR a result with ExitCode != 0.
	if err == nil && (result == nil || result.ExitCode == 0) {
		t.Error("Run: expected error or non-zero ExitCode for crash scenario")
	}
	// If there's an error, it should mention the exit code or stream failure.
	if err != nil {
		msg := strings.ToLower(err.Error())
		if !strings.Contains(msg, "exit") && !strings.Contains(msg, "code") && !strings.Contains(msg, "status") && !strings.Contains(msg, "stream") {
			t.Errorf("error message %q should mention exit code or stream failure", err.Error())
		}
	}
}

// TestRun_Timeout verifies that a slow subprocess gets killed and the error
// wraps context.DeadlineExceeded.
func TestRun_Timeout(t *testing.T) {
	in := makeRunInput(t, "slow", 1*time.Second) // 1 s timeout vs 5 s sleep in subprocess
	ctx := context.Background()

	start := time.Now()
	_, err := Run(ctx, in)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Run: expected timeout error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Run error %v should wrap context.DeadlineExceeded", err)
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "timeout") {
		t.Errorf("error message %q should mention 'timeout'", err.Error())
	}
	// The call should have taken <10 s (1 s timeout + 5 s grace + slack).
	if elapsed > 10*time.Second {
		t.Errorf("Run took %v; expected < 10s (SIGTERM/SIGKILL didn't work)", elapsed)
	}
}

// TestRun_MalformedStream verifies that malformed stream-json from the subprocess
// causes Run to return an error mentioning "parse", "json", "decode", or "stream".
func TestRun_MalformedStream(t *testing.T) {
	in := makeRunInput(t, "malformed", 30*time.Second)
	ctx := context.Background()

	_, err := Run(ctx, in)
	if err == nil {
		t.Fatal("Run: expected error for malformed stream-json, got nil")
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "parse") && !strings.Contains(msg, "json") && !strings.Contains(msg, "decode") && !strings.Contains(msg, "stream") {
		t.Errorf("error %q should mention parse/json/decode/stream failure", err.Error())
	}
}

// TestRun_ToolResult verifies that when the transcript has multiple assistant
// frames (with a user/tool_result frame in between), the LAST assistant text wins.
func TestRun_ToolResult(t *testing.T) {
	in := makeRunInput(t, "toolresult", 30*time.Second)
	ctx := context.Background()

	result, err := Run(ctx, in)
	if err != nil {
		t.Fatalf("Run returned unexpected error: %v", err)
	}
	if result.AssistantText != "done" {
		t.Errorf("AssistantText = %q, want %q (last assistant frame should win)", result.AssistantText, "done")
	}
}

// TestRun_StdinEarlyClose verifies that if the subprocess closes its stdin
// before we write to it, Run does not panic and returns gracefully.
func TestRun_StdinEarlyClose(t *testing.T) {
	in := makeRunInput(t, "stdin_early_close", 30*time.Second)
	t.Setenv("FAKECLAUDE_STDIN_CLOSE_EARLY", "1")
	ctx := context.Background()

	// We don't care whether err is nil or not — only that there is no panic.
	result, err := Run(ctx, in)
	if err == nil && result != nil {
		_ = result.AssistantText
	}
	// Test passes as long as we reach here without panicking.
}
