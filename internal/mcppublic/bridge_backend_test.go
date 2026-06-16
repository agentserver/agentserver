package mcppublic

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/envtools/tools"
)

func newTestBackend(t *testing.T) *BridgeBackend {
	t.Helper()
	src := &fakeExecutorsSource{rowsByWS: map[string][]ExecutorEntry{}}
	b, err := NewBridgeBackend("ws://test-exec-gateway/bridge", src, nil, nil)
	if err != nil {
		t.Fatalf("NewBridgeBackend: %v", err)
	}
	t.Cleanup(b.Close)
	return b
}

func TestBridgeBackend_RequiresBridgeBase(t *testing.T) {
	if _, err := NewBridgeBackend("", &fakeExecutorsSource{}, nil, nil); err == nil {
		t.Fatal("want error for empty bridge base")
	}
}

func TestBridgeBackend_RequiresExecutorsSource(t *testing.T) {
	if _, err := NewBridgeBackend("ws://x", nil, nil, nil); err == nil {
		t.Fatal("want error for nil ExecutorsSource")
	}
}

func TestBridgeBackend_ToolkitReusedForSamePrincipalAndCapToken(t *testing.T) {
	b := newTestBackend(t)
	tk1 := b.toolkitFor("user_a", "ws_1", "cap.abc.def")
	tk2 := b.toolkitFor("user_a", "ws_1", "cap.abc.def")
	if tk1 != tk2 {
		t.Errorf("toolkit not reused for identical (user, workspace, captoken)")
	}
}

func TestBridgeBackend_ToolkitDistinctPerCapToken(t *testing.T) {
	// Cap-token rotation lands a fresh toolkit (and a fresh pool
	// underneath). The old toolkit lingers until reaped.
	b := newTestBackend(t)
	tk1 := b.toolkitFor("user_a", "ws_1", "cap.v1")
	tk2 := b.toolkitFor("user_a", "ws_1", "cap.v2")
	if tk1 == tk2 {
		t.Errorf("cap-token rotation should produce a distinct toolkit")
	}
}

func TestBridgeBackend_ToolkitDistinctPerWorkspace(t *testing.T) {
	b := newTestBackend(t)
	tk1 := b.toolkitFor("user_a", "ws_1", "cap")
	tk2 := b.toolkitFor("user_a", "ws_2", "cap")
	if tk1 == tk2 {
		t.Errorf("toolkit shared across workspaces of same user")
	}
}

func TestBridgeBackend_ToolkitDistinctPerUser(t *testing.T) {
	b := newTestBackend(t)
	tk1 := b.toolkitFor("user_a", "ws_1", "cap")
	tk2 := b.toolkitFor("user_b", "ws_1", "cap")
	if tk1 == tk2 {
		t.Errorf("toolkit shared across users on same workspace")
	}
}

func TestBridgeBackend_BuildToolkit_AllToolsRegistered(t *testing.T) {
	b := newTestBackend(t)
	tk := b.toolkitFor("user_a", "ws_1", "cap")
	want := []string{
		"list_environments", "shell", "exec_command",
		"write_stdin", "read_output", "terminate",
		"read_file", "apply_patch", "copy_path",
	}
	for _, name := range want {
		tool, ok := tk.tools[name]
		if !ok {
			t.Errorf("toolkit missing %q", name)
			continue
		}
		// Sanity: the registry key matches the tool's wire name.
		// Catches the kind of drift the previous revision had
		// (registry "unified_exec" vs tool's Name() "exec_command").
		if tool.Name() != name {
			t.Errorf("toolkit key %q has Tool.Name()=%q (wire-name drift)", name, tool.Name())
		}
	}
}

func TestBridgeBackend_Call_NilPrincipal(t *testing.T) {
	b := newTestBackend(t)
	_, err := b.Call(context.Background(), ToolBackendCall{
		Tool:    "shell",
		RawArgs: json.RawMessage(`{}`),
	})
	if err == nil {
		t.Fatal("want error for nil principal")
	}
}

func TestBridgeBackend_Call_UnknownTool(t *testing.T) {
	b := newTestBackend(t)
	_, err := b.Call(context.Background(), ToolBackendCall{
		Tool:      "no-such-tool",
		CapToken:  "cap",
		RawArgs:   json.RawMessage(`{}`),
		Principal: &Principal{UserID: "u", WorkspaceID: "ws"},
	})
	if err == nil {
		t.Fatal("want error for unknown tool")
	}
}

func TestBridgeBackend_ReaperClosesIdleToolkit(t *testing.T) {
	b := newTestBackend(t)
	b.IdleTimeout = 50 * time.Millisecond

	b.toolkitFor("user_a", "ws_1", "cap.short-lived")
	// Backdate lastUsed past the cutoff.
	b.mu.Lock()
	for _, tk := range b.toolkits {
		tk.lastUsed = time.Now().Add(-time.Hour)
	}
	b.mu.Unlock()

	b.reapOnce()

	b.mu.Lock()
	n := len(b.toolkits)
	b.mu.Unlock()
	if n != 0 {
		t.Errorf("idle toolkit was not reaped; %d remain", n)
	}
}

func TestBridgeBackend_ReaperLeavesActiveToolkit(t *testing.T) {
	b := newTestBackend(t)
	b.IdleTimeout = time.Hour

	b.toolkitFor("user_a", "ws_1", "cap.active")
	b.reapOnce()

	b.mu.Lock()
	n := len(b.toolkits)
	b.mu.Unlock()
	if n != 1 {
		t.Errorf("active toolkit was reaped; %d remain", n)
	}
}

func TestBridgeBackend_Close_ClearsToolkits(t *testing.T) {
	b, err := NewBridgeBackend("ws://x", &fakeExecutorsSource{rowsByWS: map[string][]ExecutorEntry{}}, nil, nil)
	if err != nil {
		t.Fatalf("NewBridgeBackend: %v", err)
	}
	b.toolkitFor("u", "w", "cap")
	b.Close()
	if len(b.toolkits) != 0 {
		t.Errorf("toolkits not cleared on Close: %d remaining", len(b.toolkits))
	}
}

func TestDefaultPublicToolMeta_ContainsAllExpectedTools(t *testing.T) {
	meta := DefaultPublicToolMeta()
	// Every wire name advertised in tools/list. exec_command is the
	// wire name for the long-form session tool (its Go type is
	// UnifiedExecTool but Name() returns exec_command — see
	// principal.go ToolsExec comment for the rationale).
	want := map[string]bool{
		"list_environments": true,
		"shell":             true,
		"exec_command":      true,
		"write_stdin":       true,
		"read_output":       true,
		"terminate":         true,
		"read_file":         true,
		"apply_patch":       true,
		"copy_path":         true,
	}
	got := map[string]bool{}
	for _, m := range meta {
		got[m.Name] = true
	}
	for name := range want {
		if !got[name] {
			t.Errorf("DefaultPublicToolMeta missing %q (got %v)", name, got)
		}
	}
}

// Compile-time interface assertions so a future API drift fails the
// build, not a runtime call.
var _ ToolBackend = (*BridgeBackend)(nil)
var _ ExecutorsSource = (*HTTPExecutorsSource)(nil)
var _ tools.Tool = (*tools.ListEnvironmentsTool)(nil)
