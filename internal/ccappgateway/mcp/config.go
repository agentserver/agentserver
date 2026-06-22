package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ConfigInput carries everything WriteConfig needs to render the per-turn
// mcp.json file that claude reads via --mcp-config.
type ConfigInput struct {
	// EnvMcpBinary is the absolute path to the codex-app-gateway binary.
	EnvMcpBinary string

	// WorkspaceID identifies the workspace for env-mcp.
	WorkspaceID string

	// ExecGatewayBridgeURL is the WebSocket URL with "/bridge" already appended.
	// Caller is responsible for appending /bridge to the base WS URL before passing in.
	ExecGatewayBridgeURL string

	// ExecGatewayInternalURL is the http:// base URL for the exec-gateway internal API.
	ExecGatewayInternalURL string

	// ExecGatewayInternalSecret is an optional shared key for HTTP relay authentication.
	// When non-empty, it is passed via the CXG_EXEC_GATEWAY_INTERNAL_SECRET env var.
	ExecGatewayInternalSecret string

	// AgentserverInternalURL is the http:// base URL for agentserver's internal API.
	AgentserverInternalURL string

	// WorkspaceCapToken is the per-turn HMAC cap-token (NOT the proxy token).
	// It is placed in the env block as CXG_WORKSPACE_TOKEN.
	WorkspaceCapToken string

	// LogFile is the optional path passed to env-mcp's --log-file.
	// Use "/dev/stderr" to make env-mcp output visible in kubectl logs.
	LogFile string
}

// WriteConfig renders mcp.json into dir and returns the absolute path.
//
// The file is written with mode 0600 because the env block carries a
// cap-token (WorkspaceCapToken), which is sensitive.
//
// The JSON server key is "agentserver". This MUST match env-mcp's hardcoded
// MCPServer self-name (see internal/codexappgateway/envmcp/envmcp.go around
// NewMCPServer("agentserver", ...)). Claude's --tools allowlist uses the prefix
// mcp__agentserver__<tool>; a mismatched JSON key causes every tool call to fail.
func WriteConfig(dir string, in ConfigInput) (string, error) {
	if in.EnvMcpBinary == "" {
		return "", fmt.Errorf("mcp.WriteConfig: EnvMcpBinary required")
	}
	if in.WorkspaceID == "" {
		return "", fmt.Errorf("mcp.WriteConfig: WorkspaceID required")
	}
	if in.WorkspaceCapToken == "" {
		return "", fmt.Errorf("mcp.WriteConfig: WorkspaceCapToken required")
	}

	args := []string{
		"env-mcp",
		"--workspace-id", in.WorkspaceID,
		"--exec-gateway-url", in.ExecGatewayBridgeURL, // /bridge suffix is caller's responsibility
		"--exec-gateway-internal-url", in.ExecGatewayInternalURL,
		"--agentserver-internal-url", in.AgentserverInternalURL,
		"--workspace-token-env", "CXG_WORKSPACE_TOKEN",
	}

	env := map[string]string{
		"CXG_WORKSPACE_TOKEN": in.WorkspaceCapToken,
	}

	if in.ExecGatewayInternalSecret != "" {
		args = append(args, "--exec-gateway-internal-secret-env", "CXG_EXEC_GATEWAY_INTERNAL_SECRET")
		env["CXG_EXEC_GATEWAY_INTERNAL_SECRET"] = in.ExecGatewayInternalSecret
	}

	if in.LogFile != "" {
		args = append(args, "--log-file", in.LogFile)
	}

	payload := map[string]any{
		"mcpServers": map[string]any{
			"agentserver": map[string]any{
				"command": in.EnvMcpBinary,
				"args":    args,
				"env":     env,
			},
		},
	}

	b, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}

	path := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}

	return path, nil
}
