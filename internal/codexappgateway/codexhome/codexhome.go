// Package codexhome owns per-thread CODEX_HOME tmpdirs: creation,
// destruction, and the rendering of the config.toml fragment we plant
// inside each one before spawning `codex app-server`.
package codexhome

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ModelProvider struct {
	Name    string
	BaseURL string
	EnvKey  string
	WireAPI string
}

// AgentServerMCP carries everything codexhome needs to emit one
// fixed `[mcp_servers.agentserver]` block per the 2026-05-16 redesign.
// One env-mcp child per codex app-server handles every executor in
// the workspace via env_id routing — no per-executor sections.
//
// Post the 2026-06-14 full loopback removal, env-mcp talks directly
// to codex-exec-gateway (ExecGatewayInternalURL, for list_environments
// + copy_path relay) and to agentserver-main (AgentserverInternalURL,
// for scheduling). No more `AppGatewayInternalURL` / `LoopbackToken`
// fields — env-mcp doesn't need to reach back into its parent gateway.
type AgentServerMCP struct {
	CodexBin       string // absolute path to codex-app-gateway binary
	WorkspaceID    string
	ExecGatewayURL string // ws base URL; env-mcp appends /<exe_id>
	WorkspaceToken string // workspace-scoped cap token (env-injected)
	// ExecGatewayInternalURL is the http base for codex-exec-gateway's
	// internal API (NOT the ws bridge URL). Required: env-mcp's
	// list_environments calls <base>/api/exec-gateway/connected with
	// the workspace cap-token. When ExecGatewayInternalSecret is also
	// set, env-mcp's copy_path tool can mint relay tickets and use
	// the HTTPS relay path; otherwise copy_path falls back to the ws
	// cat-pump.
	ExecGatewayInternalURL    string
	ExecGatewayInternalSecret string // written verbatim into env-mcp's env block
	// AgentserverInternalURL is the http base for agentserver-main's
	// internal API. Required: env-mcp's scheduling tools POST to
	// <base>/api/internal/workspaces/<wid>/scheduled-tasks/... with
	// the workspace cap-token.
	AgentserverInternalURL string
	// LogFile, when non-empty, adds `--log-file <path>` to the env-mcp
	// args. WriteConfig auto-injects this with `<codexHome>/env-mcp.log`
	// when the caller leaves it empty, so callers normally don't set it.
	LogFile string
}

type ConfigInput struct {
	ModelProvider       string
	Model               string
	ReasoningEffort     string // optional; emits `model_reasoning_effort = "..."` when non-empty
	ModelProviders      map[string]ModelProvider
	AgentServer         AgentServerMCP
	ProjectTrustedPaths []string
}

// Manager creates per-thread CODEX_HOME tmpdirs under root.
type Manager struct{ root string }

func NewManager(root string) *Manager { return &Manager{root: root} }

// NewTmpDir creates `<root>/<workspaceID>/` with mode 0700. Idempotent.
func (m *Manager) NewTmpDir(workspaceID string) (string, error) {
	if workspaceID == "" {
		return "", fmt.Errorf("codexhome: empty workspace id")
	}
	d := filepath.Join(m.root, workspaceID)
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", d, err)
	}
	if err := os.Chmod(d, 0o700); err != nil {
		return "", fmt.Errorf("chmod %s: %w", d, err)
	}
	return d, nil
}

// RemoveTmpDir removes a previously-created tmpdir tree, but refuses
// to remove anything outside the manager's root.
func (m *Manager) RemoveTmpDir(path string) error {
	rel, err := filepath.Rel(m.root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("codexhome: refusing to remove %s outside root %s", path, m.root)
	}
	return os.RemoveAll(path)
}

// WriteConfig renders `config.toml` into the given CODEX_HOME dir.
// Side effect: if the caller didn't supply AgentServer.LogFile, it is
// filled in with `<codexHome>/env-mcp.log` so env-mcp's logger fans
// out to a file the operator can `kubectl exec ... cat`. Callers that
// want a different path (or no file at all — set to "/dev/null") can
// pre-populate the field. Mutation is on the local copy (ConfigInput
// is passed by value), so the caller's struct is unaffected.
func (m *Manager) WriteConfig(codexHome string, cfg ConfigInput) error {
	if cfg.AgentServer.CodexBin != "" && cfg.AgentServer.LogFile == "" {
		cfg.AgentServer.LogFile = filepath.Join(codexHome, "env-mcp.log")
	}
	out, err := RenderConfigTOML(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte(out), 0o600)
}

// RenderConfigTOML produces the TOML body. Pure function so tests can
// assert exact substrings without filesystem.
func RenderConfigTOML(cfg ConfigInput) (string, error) {
	if cfg.ModelProvider == "" {
		return "", fmt.Errorf("codexhome: ModelProvider required")
	}
	if cfg.Model == "" {
		return "", fmt.Errorf("codexhome: Model required")
	}
	if len(cfg.ModelProviders) > 0 {
		if _, ok := cfg.ModelProviders[cfg.ModelProvider]; !ok {
			return "", fmt.Errorf("codexhome: ModelProvider %q not in ModelProviders", cfg.ModelProvider)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "model_provider = %q\n", cfg.ModelProvider)
	fmt.Fprintf(&b, "model = %q\n", cfg.Model)
	if cfg.ReasoningEffort != "" {
		fmt.Fprintf(&b, "model_reasoning_effort = %q\n", cfg.ReasoningEffort)
	}
	b.WriteString("\n")

	for name, p := range cfg.ModelProviders {
		fmt.Fprintf(&b, "[model_providers.%s]\n", tomlKey(name))
		fmt.Fprintf(&b, "name = %q\n", p.Name)
		fmt.Fprintf(&b, "base_url = %q\n", p.BaseURL)
		fmt.Fprintf(&b, "env_key = %q\n", p.EnvKey)
		fmt.Fprintf(&b, "wire_api = %q\n\n", p.WireAPI)
	}

	for _, p := range cfg.ProjectTrustedPaths {
		fmt.Fprintf(&b, "[projects.%q]\n", p)
		b.WriteString("trust_level = \"trusted\"\n\n")
	}

	// Disable codex's builtin local-execution paths so the only way the
	// LLM can reach a shell is through the agentserver MCP server below.
	b.WriteString("[features]\n")
	b.WriteString("shell_tool = false\n")
	b.WriteString("unified_exec = false\n")
	b.WriteString("apply_patch_freeform = false\n\n")

	m := cfg.AgentServer
	if m.CodexBin != "" {
		b.WriteString("[mcp_servers.agentserver]\n")
		fmt.Fprintf(&b, "command = %q\n", m.CodexBin)
		args := []string{
			"env-mcp",
			"--workspace-id", m.WorkspaceID,
			"--exec-gateway-url", m.ExecGatewayURL,
			"--exec-gateway-internal-url", m.ExecGatewayInternalURL,
			"--agentserver-internal-url", m.AgentserverInternalURL,
			"--workspace-token-env", "CXG_WORKSPACE_TOKEN",
		}
		httpRelayEnabled := m.ExecGatewayInternalSecret != ""
		if httpRelayEnabled {
			args = append(args,
				"--exec-gateway-internal-secret-env", "CXG_EXEC_GATEWAY_INTERNAL_SECRET",
			)
		}
		if m.LogFile != "" {
			args = append(args, "--log-file", m.LogFile)
		}
		b.WriteString("args = [")
		for i, a := range args {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%q", a)
		}
		b.WriteString("]\n")
		fmt.Fprintf(&b, "env = { CXG_WORKSPACE_TOKEN = %q", m.WorkspaceToken)
		if httpRelayEnabled {
			fmt.Fprintf(&b, ", CXG_EXEC_GATEWAY_INTERNAL_SECRET = %q",
				m.ExecGatewayInternalSecret)
		}
		b.WriteString(" }\n")
		// Auto-approve all envmcp tool calls. Codex defaults to "auto" with
		// approval_required=true for tools lacking readOnlyHint annotations,
		// which would surface every read_file/exec_command as a client
		// approval prompt. We route over WeChat / REST where interactive
		// approval is impossible; the broker also tolerantly approves any
		// approval frame that slips through (defense in depth).
		b.WriteString("default_tools_approval_mode = \"approve\"\n\n")
	}
	return b.String(), nil
}

// tomlKey leaves bare keys for safe identifiers, otherwise quotes.
func tomlKey(s string) string {
	for _, r := range s {
		if !(r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return fmt.Sprintf("%q", s)
		}
	}
	return s
}
