package runner

import (
	"fmt"
	"strings"
	"time"
)

// RunInput carries all parameters needed to spawn a claude --print subprocess.
type RunInput struct {
	ClaudeBin   string        // path to the claude binary
	ClaudeDir   string        // workspace ClaudeDir → CLAUDE_CONFIG_DIR
	ProjectDir  string        // workspace ProjectDir → cmd.Dir
	SessionID   string        // UUID used as --session-id (Phase 1: always new)
	Model       string        // e.g. "haiku"
	UserMessage string        // text for the single user turn
	WSToken     string        // per-workspace auth token → ANTHROPIC_AUTH_TOKEN
	LLMProxyURL string        // e.g. "http://llmproxy:8081" → ANTHROPIC_BASE_URL
	Timeout     time.Duration // wall-clock cap; runner SIGTERMs on hit
}

// BuildArgs returns the exact CLI flag list for claude --print in Phase 1.
//
// Includes: --print, --input-format stream-json, --output-format stream-json,
// --verbose, --permission-mode bypassPermissions, --dangerously-skip-permissions,
// --model <Model>, --session-id <SessionID>.
//
// Phase 1 deliberately omits --mcp-config, --strict-mcp-config, --tools, --resume.
// There is no --cwd flag on claude; use cmd.Dir instead.
func BuildArgs(in RunInput) []string {
	return []string{
		"--print",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--permission-mode", "bypassPermissions",
		"--dangerously-skip-permissions",
		"--model", in.Model,
		"--session-id", in.SessionID,
	}
}

// BuildEnv returns the env var list for the claude subprocess.
//
// Starts from parentEnv, strips any inherited ANTHROPIC_* and CLAUDE_CODE_REMOTE*
// keys (to prevent leakage of the gateway's own credentials/mode), then sets:
//
//	CLAUDE_CONFIG_DIR=<ClaudeDir>
//	IS_SANDBOX=1
//	CLAUDE_CODE_AUTO_COMPACT_WINDOW=165000
//	CLAUDE_CODE_DISABLE_FILE_CHECKPOINTING=1
//	ANTHROPIC_AUTH_TOKEN=<WSToken>
//	ANTHROPIC_BASE_URL=<LLMProxyURL>
//
// PATH and other infra vars flow from parentEnv unchanged.
func BuildEnv(in RunInput, parentEnv []string) []string {
	// Filter out keys we override or that must not leak.
	filtered := make([]string, 0, len(parentEnv)+6)
	for _, kv := range parentEnv {
		key, _, _ := strings.Cut(kv, "=")
		if isStrippedKey(key) {
			continue
		}
		filtered = append(filtered, kv)
	}

	// Append our overrides.
	filtered = append(filtered,
		fmt.Sprintf("CLAUDE_CONFIG_DIR=%s", in.ClaudeDir),
		"IS_SANDBOX=1",
		"CLAUDE_CODE_AUTO_COMPACT_WINDOW=165000",
		"CLAUDE_CODE_DISABLE_FILE_CHECKPOINTING=1",
		fmt.Sprintf("ANTHROPIC_AUTH_TOKEN=%s", in.WSToken),
		fmt.Sprintf("ANTHROPIC_BASE_URL=%s", in.LLMProxyURL),
	)
	return filtered
}

// isStrippedKey returns true if the env var key should be removed from
// parentEnv before passing it to the claude subprocess.
func isStrippedKey(key string) bool {
	// Strip anything we're about to set ourselves, or that could leak
	// gateway credentials/mode into the subprocess.
	switch key {
	case "CLAUDE_CONFIG_DIR",
		"IS_SANDBOX",
		"CLAUDE_CODE_AUTO_COMPACT_WINDOW",
		"CLAUDE_CODE_DISABLE_FILE_CHECKPOINTING",
		"ANTHROPIC_AUTH_TOKEN",
		"ANTHROPIC_BASE_URL":
		return true
	}
	// Strip all remaining ANTHROPIC_* and CLAUDE_CODE_REMOTE* keys.
	if strings.HasPrefix(key, "ANTHROPIC_") {
		return true
	}
	if strings.HasPrefix(key, "CLAUDE_CODE_REMOTE") {
		return true
	}
	return false
}
