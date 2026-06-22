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
	SessionID   string        // UUID used as --session-id or --resume (Phase 2+)
	Model       string        // e.g. "haiku"
	UserMessage string        // text for the single user turn
	WSToken     string        // per-workspace auth token → ANTHROPIC_AUTH_TOKEN
	LLMProxyURL string        // e.g. "http://llmproxy:8081" → ANTHROPIC_BASE_URL
	Timeout     time.Duration // wall-clock cap; runner SIGTERMs on hit

	// SessionMode controls which flag carries SessionID:
	//   "fresh"  → --session-id <UUID> (first turn for this session)
	//   "resume" → --resume     <UUID> (subsequent turn — S3 had a prior tarball)
	// Default "" behaves as "fresh" (backward compat for Phase 1 callers).
	SessionMode string

	// ExtraAllowedEnv is an optional set of env-var keys that should pass
	// through to the subprocess in addition to envAllowlist. Production
	// callers leave this empty; tests use it to inject helper vars (e.g.
	// GO_WANT_HELPER_PROCESS, FAKECLAUDE_*) needed by fake-claude binaries.
	// Each entry must be an exact key name (no globs).
	ExtraAllowedEnv []string

	// ParentEnv is the parent environment to filter against envAllowlist.
	// Production callers leave this nil → Run falls back to os.Environ().
	// Tests set it explicitly to compose a minimal env without depending on
	// the process's actual environment.
	ParentEnv []string
}

// BuildArgs returns the exact CLI flag list for claude --print in Phase 2+.
//
// Includes: --print, --input-format stream-json, --output-format stream-json,
// --verbose, --permission-mode bypassPermissions, --dangerously-skip-permissions,
// --model <Model>, followed by either --session-id <SessionID> (fresh) or
// --resume <SessionID> (resume), depending on RunInput.SessionMode.
//
// Phase 1 deliberately omits --mcp-config, --strict-mcp-config, --tools.
// There is no --cwd flag on claude; use cmd.Dir instead.
func BuildArgs(in RunInput) []string {
	args := []string{
		"--print",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--permission-mode", "bypassPermissions",
		"--dangerously-skip-permissions",
		"--model", in.Model,
	}
	switch in.SessionMode {
	case "resume":
		args = append(args, "--resume", in.SessionID)
	default: // "fresh" or empty
		args = append(args, "--session-id", in.SessionID)
	}
	return args
}

// envAllowlist is the set of parent env var keys that pass through to the
// claude subprocess unchanged. Allowlist (vs denylist) means every NEW env
// var the deployment adds is secure-by-default — secrets like
// INTERNAL_API_SECRET, AGENTSERVER_INTERNAL_URL, CCAPPGW_LLMPROXY_URL,
// CCAPPGW_*, or anything else the gateway happens to read at startup
// cannot leak into the subprocess without being explicitly named here.
//
// Members are the minimum surface a typical Linux process needs to function:
// PATH for binary lookups, HOME/USER for user-local config (claude reads
// $HOME), LANG/LC_*/TZ for locale, SSL_CERT_DIR/SSL_CERT_FILE for HTTPS,
// TMPDIR for scratch (claude may use it). Notably ABSENT: anything the
// gateway might use (INTERNAL_API_SECRET, AGENTSERVER_*, CCAPPGW_*, CC_*),
// anything matching ANTHROPIC_*/CLAUDE_* (those we set explicitly below),
// and anything cluster-injected (KUBERNETES_*, POD_*).
var envAllowlist = map[string]struct{}{
	"PATH":          {},
	"HOME":          {},
	"USER":          {},
	"LOGNAME":       {},
	"LANG":          {},
	"LC_ALL":        {},
	"LC_CTYPE":      {},
	"TZ":            {},
	"SSL_CERT_DIR":  {},
	"SSL_CERT_FILE": {},
	"TMPDIR":        {},
}

// BuildEnv returns the env var list for the claude subprocess.
//
// Starts from an empty list, copies ONLY the keys in envAllowlist from
// parentEnv (so deployment-added vars are secure-by-default — never leak
// to subprocess unless explicitly allowed), then appends:
//
//	CLAUDE_CONFIG_DIR=<ClaudeDir>
//	IS_SANDBOX=1
//	CLAUDE_CODE_AUTO_COMPACT_WINDOW=165000
//	CLAUDE_CODE_DISABLE_FILE_CHECKPOINTING=1
//	ANTHROPIC_AUTH_TOKEN=<WSToken>
//	ANTHROPIC_BASE_URL=<LLMProxyURL>
//
// To add a new pass-through var, append it to envAllowlist above with a
// one-line justification.
func BuildEnv(in RunInput, parentEnv []string) []string {
	// Build the effective allowlist: static envAllowlist + per-call ExtraAllowedEnv.
	allow := make(map[string]struct{}, len(envAllowlist)+len(in.ExtraAllowedEnv))
	for k := range envAllowlist {
		allow[k] = struct{}{}
	}
	for _, k := range in.ExtraAllowedEnv {
		allow[k] = struct{}{}
	}

	filtered := make([]string, 0, len(allow)+6)
	for _, kv := range parentEnv {
		key, _, _ := strings.Cut(kv, "=")
		if _, ok := allow[key]; !ok {
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
