package runner

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func baseRunInput() RunInput {
	return RunInput{
		ClaudeBin:   "/usr/local/bin/claude",
		ClaudeDir:   "/tmp/test-claude-dir",
		ProjectDir:  "/tmp/test-project",
		SessionID:   "550e8400-e29b-41d4-a716-446655440000",
		Model:       "claude-haiku-4-5",
		UserMessage: "hello",
		WSToken:     "ws-token-abc123",
		LLMProxyURL: "http://llmproxy:8081",
		Timeout:     30 * time.Second,
	}
}

// argPairs converts a []string args slice into a map of flag → value.
// Flags without a value (bare flags like --print) map to "".
func argPairs(args []string) map[string]string {
	m := make(map[string]string)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") {
			continue
		}
		// Check if the next element is a value (not a flag).
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
			m[arg] = args[i+1]
			i++ // consume value
		} else {
			m[arg] = ""
		}
	}
	return m
}

func TestBuildArgs_RequiredFlags(t *testing.T) {
	in := baseRunInput()
	args := BuildArgs(in)
	pairs := argPairs(args)

	// --print (bare flag, no value)
	if _, ok := pairs["--print"]; !ok {
		t.Error("BuildArgs: missing --print")
	}

	// --input-format stream-json
	if v, ok := pairs["--input-format"]; !ok {
		t.Error("BuildArgs: missing --input-format")
	} else if v != "stream-json" {
		t.Errorf("BuildArgs: --input-format = %q, want stream-json", v)
	}

	// --output-format stream-json
	if v, ok := pairs["--output-format"]; !ok {
		t.Error("BuildArgs: missing --output-format")
	} else if v != "stream-json" {
		t.Errorf("BuildArgs: --output-format = %q, want stream-json", v)
	}

	// --verbose (bare flag)
	if _, ok := pairs["--verbose"]; !ok {
		t.Error("BuildArgs: missing --verbose")
	}

	// --permission-mode bypassPermissions
	if v, ok := pairs["--permission-mode"]; !ok {
		t.Error("BuildArgs: missing --permission-mode")
	} else if v != "bypassPermissions" {
		t.Errorf("BuildArgs: --permission-mode = %q, want bypassPermissions", v)
	}

	// --dangerously-skip-permissions (bare flag)
	if _, ok := pairs["--dangerously-skip-permissions"]; !ok {
		t.Error("BuildArgs: missing --dangerously-skip-permissions")
	}

	// --model <input model>
	if v, ok := pairs["--model"]; !ok {
		t.Error("BuildArgs: missing --model")
	} else if v != in.Model {
		t.Errorf("BuildArgs: --model = %q, want %q", v, in.Model)
	}

	// --session-id <input UUID>
	if v, ok := pairs["--session-id"]; !ok {
		t.Error("BuildArgs: missing --session-id")
	} else if v != in.SessionID {
		t.Errorf("BuildArgs: --session-id = %q, want %q", v, in.SessionID)
	}
}

func TestBuildArgs_ForbiddenFlags(t *testing.T) {
	in := baseRunInput()
	args := BuildArgs(in)
	joined := strings.Join(args, " ")

	forbidden := []string{"--mcp-config", "--strict-mcp-config", "--tools", "--resume", "--cwd"}
	for _, f := range forbidden {
		if strings.Contains(joined, f) {
			t.Errorf("BuildArgs: must not contain %q (Phase 1 restriction)", f)
		}
	}
}

func TestBuildEnv_StripsParentAnthropicAuthToken(t *testing.T) {
	in := baseRunInput()
	in.WSToken = "real-ws-token"

	parentEnv := []string{
		"ANTHROPIC_AUTH_TOKEN=parent-leaked",
		"PATH=/usr/bin",
	}

	result := BuildEnv(in, parentEnv)

	// Must contain our real token.
	if !slices.Contains(result, "ANTHROPIC_AUTH_TOKEN=real-ws-token") {
		t.Error("BuildEnv: ANTHROPIC_AUTH_TOKEN=real-ws-token not found in result")
	}

	// Must NOT contain the parent's leaked value.
	for _, kv := range result {
		if strings.Contains(kv, "parent-leaked") {
			t.Errorf("BuildEnv: leaked parent ANTHROPIC_AUTH_TOKEN value in %q", kv)
		}
	}
}

func TestBuildEnv_StripsParentAnthropicBaseURL(t *testing.T) {
	in := baseRunInput()
	in.LLMProxyURL = "http://real-proxy:8081"

	parentEnv := []string{
		"ANTHROPIC_BASE_URL=https://parent-leaked.example.com",
		"PATH=/usr/bin",
	}

	result := BuildEnv(in, parentEnv)

	if !slices.Contains(result, "ANTHROPIC_BASE_URL=http://real-proxy:8081") {
		t.Error("BuildEnv: ANTHROPIC_BASE_URL=http://real-proxy:8081 not found in result")
	}

	for _, kv := range result {
		if strings.Contains(kv, "parent-leaked.example.com") {
			t.Errorf("BuildEnv: leaked parent ANTHROPIC_BASE_URL value in %q", kv)
		}
	}
}

func TestBuildEnv_StripsClaudeCodeRemote(t *testing.T) {
	in := baseRunInput()

	parentEnv := []string{
		"CLAUDE_CODE_REMOTE_SESSION_ID=leaked-session",
		"PATH=/usr/bin",
	}

	result := BuildEnv(in, parentEnv)

	for _, kv := range result {
		if strings.HasPrefix(kv, "CLAUDE_CODE_REMOTE") {
			t.Errorf("BuildEnv: leaked CLAUDE_CODE_REMOTE* key in result: %q", kv)
		}
	}
}

func TestBuildEnv_PreservesInfraVars(t *testing.T) {
	in := baseRunInput()

	parentEnv := []string{
		"PATH=/usr/local/bin:/usr/bin",
		"HOME=/root",
		"USER=testuser",
		"ANTHROPIC_AUTH_TOKEN=should-be-stripped",
	}

	result := BuildEnv(in, parentEnv)

	preserve := []string{
		"PATH=/usr/local/bin:/usr/bin",
		"HOME=/root",
		"USER=testuser",
	}
	for _, want := range preserve {
		if !slices.Contains(result, want) {
			t.Errorf("BuildEnv: expected %q to be preserved, not found in result", want)
		}
	}
}

func TestBuildEnv_RequiredVarsPresent(t *testing.T) {
	in := baseRunInput()
	in.ClaudeDir = "/custom/claude/dir"
	in.WSToken = "my-ws-token"
	in.LLMProxyURL = "http://llmproxy:9999"

	result := BuildEnv(in, nil)

	required := map[string]string{
		"CLAUDE_CONFIG_DIR":                  "/custom/claude/dir",
		"IS_SANDBOX":                         "1",
		"CLAUDE_CODE_AUTO_COMPACT_WINDOW":    "165000",
		"CLAUDE_CODE_DISABLE_FILE_CHECKPOINTING": "1",
		"ANTHROPIC_AUTH_TOKEN":               "my-ws-token",
		"ANTHROPIC_BASE_URL":                 "http://llmproxy:9999",
	}

	for key, wantVal := range required {
		want := key + "=" + wantVal
		if !slices.Contains(result, want) {
			t.Errorf("BuildEnv: expected %q in result, not found", want)
		}
	}
}

func TestBuildEnv_StripsBroadClaudeCode(t *testing.T) {
	in := baseRunInput()

	parentEnv := []string{
		"CLAUDE_CODE_SESSION_ID=parent-leaked",
		"CLAUDE_CODE_ENTRYPOINT=parent-cli",
		"CLAUDE_CODE_SSE_PORT=99999",
		"PATH=/usr/bin",
	}

	result := BuildEnv(in, parentEnv)

	// Assert result does NOT contain any of the leaked parent values.
	for _, kv := range result {
		if strings.Contains(kv, "parent-leaked") {
			t.Errorf("BuildEnv: leaked parent CLAUDE_CODE_SESSION_ID value in %q", kv)
		}
		if strings.Contains(kv, "parent-cli") {
			t.Errorf("BuildEnv: leaked parent CLAUDE_CODE_ENTRYPOINT value in %q", kv)
		}
		if strings.Contains(kv, "99999") {
			t.Errorf("BuildEnv: leaked parent CLAUDE_CODE_SSE_PORT value in %q", kv)
		}
	}

	// Assert result DOES contain the PATH.
	if !slices.Contains(result, "PATH=/usr/bin") {
		t.Error("BuildEnv: PATH=/usr/bin not found in result")
	}
}

func TestBuildEnv_PreservesClaudeConfigDir(t *testing.T) {
	in := baseRunInput()
	in.ClaudeDir = "/turn-specific"

	parentEnv := []string{
		"CLAUDE_CONFIG_DIR=parent-leaked",
		"PATH=/usr/bin",
	}

	result := BuildEnv(in, parentEnv)

	// Assert result contains our CLAUDE_CONFIG_DIR value.
	if !slices.Contains(result, "CLAUDE_CONFIG_DIR=/turn-specific") {
		t.Error("BuildEnv: CLAUDE_CONFIG_DIR=/turn-specific not found in result")
	}

	// Assert result does NOT contain the parent's leaked value.
	for _, kv := range result {
		if strings.Contains(kv, "CLAUDE_CONFIG_DIR=parent-leaked") {
			t.Errorf("BuildEnv: leaked parent CLAUDE_CONFIG_DIR value in %q", kv)
		}
	}
}
