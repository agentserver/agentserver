package runner

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"testing"
	"time"
)

// TestHelperProcess is the entrypoint for the fake-claude subprocess.
// When run normally as a test it returns immediately (no-op).
// When the test binary is re-exec'd with GO_WANT_HELPER_PROCESS=1 it
// mimics `claude --print --output-format stream-json` for the requested scenario.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	// Drain stdin (one SDKUserMessage line), ignoring content.
	if os.Getenv("FAKECLAUDE_STDIN_CLOSE_EARLY") == "1" {
		// Intentionally close stdin before reading to test write-to-closed-pipe.
		os.Stdin.Close()
	} else {
		io.ReadAll(os.Stdin) //nolint:errcheck
	}

	scenario := os.Getenv("FAKECLAUDE_SCENARIO")

	switch scenario {
	case "pong":
		emitPong()
	case "slow":
		time.Sleep(5 * time.Second)
		emitPong()
	case "crash":
		emitInit()
		// Exit non-zero without a result frame.
		exitCode := 1
		if s := os.Getenv("FAKECLAUDE_EXIT_CODE"); s != "" {
			if n, err := strconv.Atoi(s); err == nil {
				exitCode = n
			}
		}
		os.Exit(exitCode)
	case "malformed":
		emitInit()
		// Emit one valid line then a malformed JSON fragment (no closing brace).
		fmt.Print("{garbage\n")
		os.Exit(0)
	case "toolresult":
		emitToolResultScenario()
	case "stdin_early_close":
		// Close stdin before we read it, then emit pong normally.
		emitPong()
	default:
		emitPong()
	}

	exitCode := 0
	if s := os.Getenv("FAKECLAUDE_EXIT_CODE"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			exitCode = n
		}
	}
	os.Exit(exitCode)
}

// emitJSON marshals v and prints it followed by a newline.
func emitJSON(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(b))
}

func emitInit() {
	emitJSON(map[string]any{
		"type":       "system",
		"subtype":    "init",
		"session_id": "fake-session-id",
		"model":      "claude-haiku-4-5",
	})
}

func emitPong() {
	emitInit()

	// Assistant frame with text "pong".
	emitJSON(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"role": "assistant",
			"content": []map[string]any{
				{"type": "text", "text": "pong"},
			},
		},
		"session_id": "fake-session-id",
	})

	// Result frame — success.
	emitJSON(map[string]any{
		"type":          "result",
		"subtype":       "success",
		"is_error":      false,
		"duration_ms":   100,
		"total_cost_usd": 0.0001,
		"session_id":    "fake-session-id",
		"modelUsage": map[string]any{
			"claude-haiku-4-5": map[string]any{
				"inputTokens":  10,
				"outputTokens": 5,
			},
		},
	})
}

func emitToolResultScenario() {
	emitInit()

	// First assistant frame.
	emitJSON(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"role": "assistant",
			"content": []map[string]any{
				{"type": "text", "text": "calling tool"},
			},
		},
		"session_id": "fake-session-id",
	})

	// User frame with tool_result (simulated tool response).
	emitJSON(map[string]any{
		"type": "user",
		"message": map[string]any{
			"role": "user",
			"content": []map[string]any{
				{"type": "tool_result", "content": "result"},
			},
		},
		"session_id": "fake-session-id",
	})

	// Second assistant frame — "done".
	emitJSON(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"role": "assistant",
			"content": []map[string]any{
				{"type": "text", "text": "done"},
			},
		},
		"session_id": "fake-session-id",
	})

	// Result frame — success.
	emitJSON(map[string]any{
		"type":          "result",
		"subtype":       "success",
		"is_error":      false,
		"duration_ms":   200,
		"total_cost_usd": 0.0002,
		"session_id":    "fake-session-id",
		"modelUsage": map[string]any{
			"claude-haiku-4-5": map[string]any{
				"inputTokens":  20,
				"outputTokens": 10,
			},
		},
	})
}

// fakeClaudeEnv returns the set of env vars to inject into the current
// process so that when the test binary re-execs itself as the fake claude
// subprocess, it picks up the right scenario.
func fakeClaudeEnv(scenario string) []string {
	return []string{
		"GO_WANT_HELPER_PROCESS=1",
		"FAKECLAUDE_SCENARIO=" + scenario,
	}
}

// writeFakeClaudeScript writes a shell script that:
// 1. Ignores all arguments (the real claude flags passed by runner).
// 2. Re-execs the test binary with -test.run=TestHelperProcess so that
//    TestHelperProcess runs and emits the canned transcript.
//
// The script is written to a temp file and made executable.
func writeFakeClaudeScript(t *testing.T) string {
	t.Helper()
	testBin := os.Args[0]
	// Note: we do NOT forward $@ (the claude flags) to the test binary because
	// the test binary's flag parser would reject --print etc.
	// The scenario is communicated via env vars set by t.Setenv in the test.
	// Run the test binary as TestHelperProcess. Suppress test framework output
	// by NOT passing -test.v (the canned frames go to stdout; test output would
	// pollute the stream-json parser).
	// -test.paniconexit0=false allows os.Exit calls from TestHelperProcess
	// to propagate normally rather than panicking.
	script := fmt.Sprintf("#!/bin/sh\nexec %q -test.run='^TestHelperProcess$' -test.paniconexit0=false\n", testBin)
	path := t.TempDir() + "/fake-claude"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("writeFakeClaudeScript: %v", err)
	}
	return path
}
