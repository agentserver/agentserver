package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

func TestWriteConfig_HappyPath(t *testing.T) {
	tmp := t.TempDir()
	path, err := WriteConfig(tmp, ConfigInput{
		EnvMcpBinary:           "/usr/local/bin/codex-app-gateway",
		WorkspaceID:            "wsp_abc",
		ExecGatewayBridgeURL:   "ws://codex-exec-gateway:6060/bridge",
		ExecGatewayInternalURL: "http://codex-exec-gateway:6060",
		AgentserverInternalURL: "http://agentserver:8080",
		WorkspaceCapToken:      "cap-token-abc.def.ghi",
		LogFile:                "/dev/stderr",
	})
	if err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	if path != filepath.Join(tmp, "mcp.json") {
		t.Errorf("path = %q, want %q", path, filepath.Join(tmp, "mcp.json"))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600 (secret in env block)", info.Mode().Perm())
	}
	raw, _ := os.ReadFile(path)
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	servers := got["mcpServers"].(map[string]any)
	srv := servers["agentserver"].(map[string]any) // key MUST be "agentserver" — env-mcp's self-name
	if srv["command"] != "/usr/local/bin/codex-app-gateway" {
		t.Errorf("command = %v", srv["command"])
	}
	args := srv["args"].([]any)
	wantSubstrs := []string{"env-mcp", "--workspace-id", "wsp_abc",
		"--exec-gateway-url", "ws://codex-exec-gateway:6060/bridge",
		"--exec-gateway-internal-url", "http://codex-exec-gateway:6060",
		"--agentserver-internal-url", "http://agentserver:8080",
		"--workspace-token-env", "CXG_WORKSPACE_TOKEN",
		"--log-file", "/dev/stderr"}
	var got_args []string
	for _, a := range args {
		got_args = append(got_args, a.(string))
	}
	for _, want := range wantSubstrs {
		if !contains(got_args, want) {
			t.Errorf("args missing %q; got %v", want, got_args)
		}
	}
	env := srv["env"].(map[string]any)
	if env["CXG_WORKSPACE_TOKEN"] != "cap-token-abc.def.ghi" {
		t.Errorf("env CXG_WORKSPACE_TOKEN = %v", env["CXG_WORKSPACE_TOKEN"])
	}
}

func TestWriteConfig_OptionalExecGatewayInternalSecret(t *testing.T) {
	tmp := t.TempDir()
	_, err := WriteConfig(tmp, ConfigInput{
		EnvMcpBinary: "/x", WorkspaceID: "w", WorkspaceCapToken: "t",
		ExecGatewayInternalSecret: "secret-xxx",
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(tmp, "mcp.json"))
	if !bytes.Contains(raw, []byte("CXG_EXEC_GATEWAY_INTERNAL_SECRET")) {
		t.Errorf("expected --exec-gateway-internal-secret-env flag + env var")
	}
	if !bytes.Contains(raw, []byte("secret-xxx")) {
		t.Errorf("expected secret value in env block")
	}
}

func TestWriteConfig_ValidatesRequired(t *testing.T) {
	cases := []struct {
		name string
		in   ConfigInput
	}{
		{"empty binary", ConfigInput{WorkspaceID: "w", WorkspaceCapToken: "t"}},
		{"empty workspace", ConfigInput{EnvMcpBinary: "/x", WorkspaceCapToken: "t"}},
		{"empty captoken", ConfigInput{EnvMcpBinary: "/x", WorkspaceID: "w"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := WriteConfig(t.TempDir(), tc.in)
			if err == nil {
				t.Errorf("want error, got nil")
			}
		})
	}
}
