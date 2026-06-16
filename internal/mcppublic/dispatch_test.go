package mcppublic

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/agentserver/agentserver/internal/envtools/tools"
)

// fakeExecutorsSource is a deterministic ExecutorsSource stub for the
// dispatcher tests. Returns rowsByWS[wid] for a given workspace.
type fakeExecutorsSource struct {
	rowsByWS map[string][]ExecutorEntry
	err      error
}

func (f *fakeExecutorsSource) ListWorkspaceExecutors(_ context.Context, wid string) ([]ExecutorEntry, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.rowsByWS[wid], nil
}

// stubBackend records each Call invocation and returns canned results.
// The dispatcher's job is to authorize + mint cap-token + delegate;
// the backend's observable behaviour from a unit test's POV is "was
// I invoked with the right principal + cap-token?".
type stubBackend struct {
	mu     sync.Mutex
	calls  []ToolBackendCall
	result tools.MCPCallToolResult
	err    error
}

func (s *stubBackend) Call(_ context.Context, in ToolBackendCall) (tools.MCPCallToolResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, in)
	if s.err != nil {
		return tools.MCPCallToolResult{}, s.err
	}
	return s.result, nil
}

func (s *stubBackend) lastCall(t *testing.T) ToolBackendCall {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.calls) == 0 {
		t.Fatal("backend was not invoked")
	}
	return s.calls[len(s.calls)-1]
}

func newTestDispatcher(t *testing.T, src *fakeExecutorsSource, backend ToolBackend) *Dispatcher {
	t.Helper()
	if src == nil {
		src = &fakeExecutorsSource{rowsByWS: map[string][]ExecutorEntry{}}
	}
	minter, err := NewCapMinter([]byte("test-hmac-secret"))
	if err != nil {
		t.Fatalf("NewCapMinter: %v", err)
	}
	meta := []PublicToolMeta{
		{Name: "list_environments", Description: "List", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "read_file", Description: "Read", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "shell", Description: "Run shell", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}
	return NewDispatcher(src, minter, backend, meta, nil)
}

func principalWith(workspaceID string) *Principal {
	return &Principal{
		UserID:      "user_42",
		WorkspaceID: workspaceID,
		Tools:       map[string]struct{}{},
	}
}

func principalReadOnly(workspaceID string) *Principal {
	p := principalWith(workspaceID)
	for k := range ToolsRead {
		p.Tools[k] = struct{}{}
	}
	return p
}

func principalReadExec(workspaceID string) *Principal {
	p := principalReadOnly(workspaceID)
	for k := range ToolsExec {
		p.Tools[k] = struct{}{}
	}
	return p
}

func TestDispatcher_ToolsList_FiltersByPrincipalScope(t *testing.T) {
	d := newTestDispatcher(t, nil, &stubBackend{})

	got := d.ToolsList(principalReadOnly("ws_1"))
	names := toolNames(got.Tools)
	if !contains(names, "list_environments") || !contains(names, "read_file") {
		t.Errorf("read-only principal: missing read tools, got %v", names)
	}
	if contains(names, "shell") {
		t.Errorf("read-only principal saw shell tool: %v", names)
	}

	got = d.ToolsList(principalReadExec("ws_1"))
	names = toolNames(got.Tools)
	if !contains(names, "shell") {
		t.Errorf("read+exec principal missing shell, got %v", names)
	}
}

func TestDispatcher_ToolsList_NilPrincipalIsEmpty(t *testing.T) {
	d := newTestDispatcher(t, nil, &stubBackend{})
	got := d.ToolsList(nil)
	if len(got.Tools) != 0 {
		t.Errorf("nil principal saw tools: %v", toolNames(got.Tools))
	}
}

func TestDispatcher_ToolsCall_ForbiddenToolWithoutScope(t *testing.T) {
	backend := &stubBackend{}
	d := newTestDispatcher(t, nil, backend)
	p := principalReadOnly("ws_1") // no mcp:exec

	_, errObj := d.ToolsCall(context.Background(), p, tools.MCPCallToolParams{
		Name:      "shell",
		Arguments: json.RawMessage(`{"environment_id":"laptop"}`),
	})
	if errObj == nil || errObj.Code != codeForbiddenTool {
		t.Fatalf("want codeForbiddenTool, got %+v", errObj)
	}
	if len(backend.calls) != 0 {
		t.Errorf("backend was invoked despite scope check failing")
	}
}

func TestDispatcher_ToolsCall_UnknownToolName(t *testing.T) {
	d := newTestDispatcher(t, nil, &stubBackend{})
	p := principalReadExec("ws_1")

	_, errObj := d.ToolsCall(context.Background(), p, tools.MCPCallToolParams{
		Name:      "nonexistent",
		Arguments: json.RawMessage(`{}`),
	})
	if errObj == nil || errObj.Code != codeMethodNotFound {
		t.Fatalf("want codeMethodNotFound, got %+v", errObj)
	}
}

func TestDispatcher_ToolsCall_NilPrincipalIsAuthMissing(t *testing.T) {
	d := newTestDispatcher(t, nil, &stubBackend{})
	_, errObj := d.ToolsCall(context.Background(), nil, tools.MCPCallToolParams{
		Name:      "shell",
		Arguments: json.RawMessage(`{}`),
	})
	if errObj == nil || errObj.Code != codeAuthMissing {
		t.Fatalf("want codeAuthMissing, got %+v", errObj)
	}
}

func TestDispatcher_ToolsCall_HappyPathInvokesBackendWithMintedToken(t *testing.T) {
	backend := &stubBackend{result: tools.MCPCallToolResult{
		Content: []tools.MCPToolContent{{Type: "text", Text: "ok"}},
	}}
	d := newTestDispatcher(t, nil, backend)
	p := principalReadExec("ws_1")

	res, errObj := d.ToolsCall(context.Background(), p, tools.MCPCallToolParams{
		Name:      "shell",
		Arguments: json.RawMessage(`{"environment_id":"laptop","command":["echo","hi"]}`),
	})
	if errObj != nil {
		t.Fatalf("unexpected errObj: %+v", errObj)
	}
	if res == nil || res.Content[0].Text != "ok" {
		t.Fatalf("unexpected result: %+v", res)
	}
	call := backend.lastCall(t)
	if call.Tool != "shell" {
		t.Errorf("backend Tool: got %q, want shell", call.Tool)
	}
	if call.CapToken == "" {
		t.Errorf("cap-token empty in backend call")
	}
	if call.Principal != p {
		t.Errorf("backend principal != original principal")
	}
	// Args are passed through verbatim — backend does its own
	// name → exe_id resolution.
	if string(call.RawArgs) != `{"environment_id":"laptop","command":["echo","hi"]}` {
		t.Errorf("RawArgs mutated: %s", call.RawArgs)
	}
}

func TestDispatcher_ToolsCall_BackendFailureSurfacesAsExecError(t *testing.T) {
	backend := &stubBackend{err: errors.New("bridge dial timed out")}
	d := newTestDispatcher(t, nil, backend)
	p := principalReadExec("ws_1")

	_, errObj := d.ToolsCall(context.Background(), p, tools.MCPCallToolParams{
		Name:      "shell",
		Arguments: json.RawMessage(`{"environment_id":"laptop"}`),
	})
	if errObj == nil || errObj.Code != codeToolExecutionFail {
		t.Fatalf("want codeToolExecutionFail, got %+v", errObj)
	}
	if !strings.Contains(errObj.Message, "bridge dial timed out") {
		t.Errorf("error message should include backend cause: %s", errObj.Message)
	}
}

func TestDispatcher_ToolsCall_ListEnvironmentsHandledInProcess(t *testing.T) {
	backend := &stubBackend{}
	src := &fakeExecutorsSource{rowsByWS: map[string][]ExecutorEntry{
		"ws_1": {
			{ExeID: "exe_a", Name: "laptop", Description: "macbook", IsDefault: true, LastSeenISO: "2026-06-09T07:00:00Z"},
			{ExeID: "exe_b", Name: "server"},
		},
	}}
	d := newTestDispatcher(t, src, backend)
	p := principalReadOnly("ws_1")

	res, errObj := d.ToolsCall(context.Background(), p, tools.MCPCallToolParams{
		Name:      "list_environments",
		Arguments: json.RawMessage(`{}`),
	})
	if errObj != nil {
		t.Fatalf("unexpected errObj: %+v", errObj)
	}
	if len(backend.calls) != 0 {
		t.Errorf("backend was invoked for list_environments (should be in-process)")
	}
	if res == nil || len(res.Content) != 1 {
		t.Fatalf("expected one text content, got %+v", res)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &rows); err != nil {
		t.Fatalf("body not JSON: %v (text=%q)", err, res.Content[0].Text)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d (rows=%v)", len(rows), rows)
	}
	names := []string{rows[0]["name"].(string), rows[1]["name"].(string)}
	if !contains(names, "laptop") || !contains(names, "server") {
		t.Errorf("unexpected names: %v", names)
	}
	for _, r := range rows {
		if _, hasExe := r["exe_id"]; hasExe {
			t.Errorf("exe_id leaked to MCP client: %v", r)
		}
		if _, hasWS := r["workspace_id"]; hasWS {
			t.Errorf("workspace_id leaked to MCP client: %v", r)
		}
	}
}

func TestDispatcher_ToolsCall_ListEnvironmentsScopedToPrincipalsWorkspace(t *testing.T) {
	// Different workspaces have different executors; the dispatcher
	// passes the principal's WorkspaceID to the source.
	src := &fakeExecutorsSource{rowsByWS: map[string][]ExecutorEntry{
		"ws_alpha": {{ExeID: "exe_a", Name: "alpha-only"}},
		"ws_beta":  {{ExeID: "exe_b", Name: "beta-only"}},
	}}
	d := newTestDispatcher(t, src, &stubBackend{})

	for _, ws := range []string{"ws_alpha", "ws_beta"} {
		t.Run(ws, func(t *testing.T) {
			res, errObj := d.ToolsCall(context.Background(), principalReadOnly(ws),
				tools.MCPCallToolParams{Name: "list_environments", Arguments: json.RawMessage(`{}`)})
			if errObj != nil {
				t.Fatalf("unexpected errObj: %+v", errObj)
			}
			var rows []map[string]any
			_ = json.Unmarshal([]byte(res.Content[0].Text), &rows)
			if len(rows) != 1 {
				t.Fatalf("workspace %s: want 1 row, got %d", ws, len(rows))
			}
			gotName := rows[0]["name"].(string)
			if !strings.HasPrefix(gotName, strings.TrimPrefix(ws, "ws_")) {
				t.Errorf("workspace %s: leaked cross-workspace row: %s", ws, gotName)
			}
		})
	}
}

func TestDispatcher_ToolsCall_ListEnvironmentsUpstreamErrorIsServiceUnavailable(t *testing.T) {
	src := &fakeExecutorsSource{err: errors.New("upstream down")}
	d := newTestDispatcher(t, src, &stubBackend{})

	_, errObj := d.ToolsCall(context.Background(), principalReadOnly("ws_1"),
		tools.MCPCallToolParams{Name: "list_environments", Arguments: json.RawMessage(`{}`)})
	if errObj == nil || errObj.Code != codeUpstreamUnavail {
		t.Fatalf("want codeUpstreamUnavail, got %+v", errObj)
	}
}

func TestDispatcher_Initialize_ReturnsServerInfo(t *testing.T) {
	d := newTestDispatcher(t, nil, &stubBackend{})
	res := d.Initialize()
	if res.ProtocolVersion != "2025-06-18" {
		t.Errorf("ProtocolVersion: got %q, want 2025-06-18 (codex CLI's expectation)", res.ProtocolVersion)
	}
	if res.ServerInfo.Name == "" {
		t.Errorf("ServerInfo.Name empty: %+v", res)
	}
}

// helpers --------------------------------------------------------------

func toolNames(ts []tools.MCPTool) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Name
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
