package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/agentserver/agentserver/internal/envtools/processes"
)

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
