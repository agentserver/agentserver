package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/agentserver/agentserver/internal/codexexecgateway/audit"
	"github.com/agentserver/agentserver/internal/envtools/processes"
)

// capRec captures CallStart/CallEnd for assertion in recordCall tests.
type capRec struct {
	mu          sync.Mutex
	startErr    error
	starts      []audit.CallStartMeta
	ends        map[string]audit.CallEndMeta
	endOrder    []string
	startCount  int
}

func (r *capRec) SessionOpen(audit.SessionMeta) (string, error)    { return "", nil }
func (r *capRec) SessionClose(string, string, audit.Counters)      {}
func (r *capRec) OnFrameToBackend(string, any, []byte)             {}
func (r *capRec) OnFrameToClient(string, any, []byte)              {}
func (r *capRec) CallStart(m audit.CallStartMeta) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.startErr != nil {
		return "", r.startErr
	}
	r.startCount++
	id := fmt.Sprintf("call-%d", r.startCount)
	r.starts = append(r.starts, m)
	return id, nil
}
func (r *capRec) CallEnd(id string, m audit.CallEndMeta) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ends == nil {
		r.ends = map[string]audit.CallEndMeta{}
	}
	r.ends[id] = m
	r.endOrder = append(r.endOrder, id)
}
func (r *capRec) Close(context.Context) error { return nil }

// TestEnvsList_RecorderObservesCallPair: wiring test for test-analyzer
// #2. Pins that handleEnvsList actually invokes the Recorder so a
// future refactor that drops s.Recorder won't silently disable audit
// for the entire SDK surface — at least one handler now fails fast.
func TestEnvsList_RecorderObservesCallPair(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"workspace_id": "ws-1", "user_id": "u-1"})
	}))
	defer upstream.Close()
	rec := &capRec{}
	s := &Server{
		Auth:     NewProxyTokenAuth(upstream.URL, "x", time.Minute, time.Second),
		Registry: connectedListerStub{},
		Recorder: rec,
	}
	r := chi.NewRouter()
	s.Mount(r)
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/envs/list",
		bytes.NewReader([]byte("{}")))
	req.Header.Set("Authorization", "Bearer tok-1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if len(rec.starts) != 1 {
		t.Fatalf("want 1 CallStart, got %d", len(rec.starts))
	}
	if got := rec.starts[0].Source; got != "rest" {
		t.Errorf("Source=%q want %q", got, "rest")
	}
	if got := rec.starts[0].RPCMethod; got != "envs.list" {
		t.Errorf("RPCMethod=%q want envs.list", got)
	}
	if len(rec.ends) != 1 {
		t.Fatalf("want 1 CallEnd, got %d", len(rec.ends))
	}
}

// TestRecordCall_PanicProducesCallEnd: a panic inside fn() must still
// emit a paired CallEnd with IsError=true + ErrorSummary so the audit
// trail can't be left with an orphaned CallStart.
func TestRecordCall_PanicProducesCallEnd(t *testing.T) {
	rec := &capRec{}
	w := httptest.NewRecorder()
	defer func() {
		// The panic must be re-raised. Recover here so the test passes.
		if r := recover(); r == nil {
			t.Fatal("expected recordCall to re-raise the panic from fn")
		}
		// Verify CallEnd was emitted before the re-raise.
		if len(rec.ends) != 1 {
			t.Fatalf("want 1 CallEnd, got %d", len(rec.ends))
		}
		var end audit.CallEndMeta
		for _, v := range rec.ends {
			end = v
		}
		if !end.IsError {
			t.Errorf("CallEnd.IsError should be true on panic")
		}
		if !strings.HasPrefix(end.ErrorSummary, "panic:") {
			t.Errorf("CallEnd.ErrorSummary should start with 'panic:', got %q", end.ErrorSummary)
		}
	}()
	recordCall(rec, w, "ws", "u", "exe", "tool.call:shell", nil, func() ([]byte, bool, string) {
		panic("boom")
	})
}

// TestRecordCall_StartErrorWrites503: when CallStart returns an error,
// recordCall must short-circuit, write 503, and never invoke fn.
func TestRecordCall_StartErrorWrites503(t *testing.T) {
	rec := &capRec{startErr: errors.New("audit disk full")}
	w := httptest.NewRecorder()
	invoked := false
	ok := recordCall(rec, w, "ws", "u", "exe", "tool.call:shell", nil, func() ([]byte, bool, string) {
		invoked = true
		return nil, false, ""
	})
	if ok {
		t.Fatal("recordCall should return ok=false when CallStart fails")
	}
	if invoked {
		t.Fatal("fn must NOT be invoked when CallStart fails")
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

// connectedListerStub returns hard-coded envs for one workspace.
type connectedListerStub struct{}

func (connectedListerStub) Connected(ctx context.Context, wsID string) ([]ConnectedExecutor, error) {
	if wsID == "ws-1" {
		return []ConnectedExecutor{
			{Name: "my-mac", IsDefault: true, LastSeenAt: "2026-05-19T08:00:00Z"},
		}, nil
	}
	return nil, nil
}

func TestEnvsList_HappyPath(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"workspace_id": "ws-1", "user_id": "u-1"})
	}))
	defer upstream.Close()
	s := &Server{
		Auth:     NewProxyTokenAuth(upstream.URL, "x", time.Minute, time.Second),
		Registry: connectedListerStub{},
	}
	r := chi.NewRouter()
	s.Mount(r)
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/envs/list", bytes.NewReader([]byte("{}")))
	req.Header.Set("Authorization", "Bearer tok-1")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Envs []map[string]any `json:"envs"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.Envs) != 1 || got.Envs[0]["name"] != "my-mac" {
		t.Fatalf("envs=%+v", got.Envs)
	}
}

func TestEnvsList_MissingBearer_401(t *testing.T) {
	s := &Server{Registry: connectedListerStub{}}
	r := chi.NewRouter()
	s.Mount(r)
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/envs/list", bytes.NewReader([]byte("{}")))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestToolCall_UnknownTool_400(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"workspace_id": "ws-1", "user_id": "u-1"})
	}))
	defer upstream.Close()
	// wsCtxFor builds the per-workspace tool registry from the fixed
	// list inside wsCtxFor; requesting an unknown tool by name should
	// 400 regardless of which workspace the request lands on.
	s := &Server{
		Auth:             NewProxyTokenAuth(upstream.URL, "x", time.Minute, time.Second),
		Registry:         connectedListerStub{},
		ExecGatewayWSURL: "ws://test/bridge",
		CapTokenSecret:   []byte("test-secret"),
	}
	r := chi.NewRouter()
	s.Mount(r)
	body := bytes.NewReader([]byte(`{"tool":"unknown","arguments":{}}`))
	req := httptest.NewRequest(http.MethodPost, "/api/connectors/envs/my-mac/tool/call", body)
	req.Header.Set("Authorization", "Bearer tok-1")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// TestCoreTools_IncludesWriteFile confirms the SDK's envs/list response
// advertises write_file alongside read_file. Originally missing in B6.
func TestCoreTools_IncludesWriteFile(t *testing.T) {
	found := false
	for _, td := range coreTools() {
		if td.Name == "write_file" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("write_file missing from coreTools()")
	}
}

// TestWsCtxFor_HasWriteFile confirms the per-workspace tool registry
// actually wires up write_file (gap 1 + gap 2 together).
func TestWsCtxFor_HasWriteFile(t *testing.T) {
	s := &Server{
		Registry:         connectedListerStub{},
		ExecGatewayWSURL: "ws://test/bridge",
		CapTokenSecret:   []byte("test-secret"),
	}
	wc, err := s.wsCtxFor("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := wc.tools["write_file"]; !ok {
		t.Fatal("write_file not in per-workspace tool registry")
	}
}

func TestProcessOutput_ForbiddenOtherWorkspace(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"workspace_id": "ws-2", "user_id": "u-1"})
	}))
	defer upstream.Close()
	s := &Server{
		Auth:     NewProxyTokenAuth(upstream.URL, "x", time.Minute, time.Second),
		Sessions: processes.NewManager(30 * time.Minute),
	}
	s.Sessions.Register(&processes.Session{ID: "sid-1", WorkspaceID: "ws-1"})
	r := chi.NewRouter()
	s.Mount(r)
	req := httptest.NewRequest(http.MethodGet, "/api/connectors/processes/sid-1/output", nil)
	req.Header.Set("Authorization", "Bearer tok-1")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestProcessOutput_HappyPath(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"workspace_id": "ws-1", "user_id": "u-1"})
	}))
	defer upstream.Close()
	s := &Server{
		Auth:     NewProxyTokenAuth(upstream.URL, "x", time.Minute, time.Second),
		Sessions: processes.NewManager(30 * time.Minute),
	}
	sess := &processes.Session{ID: "sid-1", WorkspaceID: "ws-1"}
	sess.Append("stdout", []byte("hello"))
	s.Sessions.Register(sess)
	r := chi.NewRouter()
	s.Mount(r)
	req := httptest.NewRequest(http.MethodGet, "/api/connectors/processes/sid-1/output?since=0", nil)
	req.Header.Set("Authorization", "Bearer tok-1")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		Chunks []map[string]any `json:"chunks"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.Chunks) != 1 {
		t.Fatalf("chunks=%+v", got.Chunks)
	}
}
