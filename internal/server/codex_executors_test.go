package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/internal/auth"
	"github.com/go-chi/chi/v5"
)

func TestConnectCommands_EmitsAgentxCommand(t *testing.T) {
	s := &Server{
		CodexExecGatewayPublicHost: "x.agent.cs.ac.cn",
		CodexAuthIssuerURL:         "https://codex-auth.agent.cs.ac.cn",
	}
	cmd := buildConnectCommand(s, "fake-jwt", "exe_xxx", "my-laptop")

	if !strings.HasPrefix(cmd, "export AGENTX_ACCESS_TOKEN='fake-jwt'") {
		t.Errorf("missing AGENTX_ACCESS_TOKEN export; got: %s", cmd)
	}
	if !strings.Contains(cmd, "agentx --remote 'https://x.agent.cs.ac.cn'") {
		t.Errorf("missing agentx --remote invocation; got: %s", cmd)
	}
	if !strings.Contains(cmd, "--agent-identity-authapi-base-url 'https://codex-auth.agent.cs.ac.cn'") {
		t.Errorf("missing --agent-identity-authapi-base-url; got: %s", cmd)
	}
	if strings.Contains(cmd, "codex -c chatgpt_base_url") {
		t.Errorf("still emitting legacy codex command; got: %s", cmd)
	}
	if strings.Contains(cmd, "CODEX_ACCESS_TOKEN") {
		t.Errorf("still emitting legacy CODEX_ACCESS_TOKEN env; got: %s", cmd)
	}
}

// withChiURLParam plumbs a chi URL parameter into the request context so
// handlers can retrieve it via chi.URLParam without needing a full router.
func withChiURLParam(r *http.Request, key, val string) *http.Request {
	rctx, ok := r.Context().Value(chi.RouteCtxKey).(*chi.Context)
	if !ok || rctx == nil {
		rctx = chi.NewRouteContext()
	}
	rctx.URLParams.Add(key, val)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// newExecutorsServer wires Server with a stubbed codex-exec-gateway.
func newExecutorsServer(t *testing.T, stub http.Handler) (*Server, func()) {
	t.Helper()
	gw := httptest.NewServer(stub)
	d := newCodexTestDBForServer(t)
	srv := &Server{
		DB:                         d,
		ExecutorsClient:            NewExecutorsClient(gw.URL, "test-secret"),
		CodexExecGatewayPublicHost: "codex-exec.example.com",
	}
	return srv, func() { gw.Close() }
}

func TestHandleRegisterExecutor_HappyPath(t *testing.T) {
	stub := http.NewServeMux()
	registerCalls := 0
	bindCalls := 0
	stub.HandleFunc("/api/codex-exec/register", func(w http.ResponseWriter, r *http.Request) {
		registerCalls++
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"exe_id": "exe_test"})
	})
	stub.HandleFunc("/api/codex-exec/workspaces/ws_a/executors", func(w http.ResponseWriter, r *http.Request) {
		bindCalls++
		w.WriteHeader(http.StatusCreated)
	})
	srv, cleanup := newExecutorsServer(t, stub)
	defer cleanup()
	seedWorkspaceMember(t, srv.DB, "ws_a", "u1", "owner")

	body := bytes.NewReader([]byte(`{"name":"laptop"}`))
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws_a/executors", body).
		WithContext(auth.ContextWithUserID(context.Background(), "u1"))
	req.Header.Set("Content-Type", "application/json")
	req = withChiURLParam(req, "wid", "ws_a")
	rr := httptest.NewRecorder()
	srv.handleRegisterExecutor(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var resp registerExecutorResp
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.ExeID != "exe_test" {
		t.Errorf("resp = %+v", resp)
	}
	if registerCalls != 1 || bindCalls != 1 {
		t.Errorf("register=%d bind=%d", registerCalls, bindCalls)
	}
}

func TestHandleRegisterExecutor_RequiresAdmin(t *testing.T) {
	srv, cleanup := newExecutorsServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer cleanup()
	seedWorkspaceMember(t, srv.DB, "ws_a", "u_dev", "developer")
	body := bytes.NewReader([]byte(`{}`))
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/ws_a/executors", body).
		WithContext(auth.ContextWithUserID(context.Background(), "u_dev"))
	req = withChiURLParam(req, "wid", "ws_a")
	rr := httptest.NewRecorder()
	srv.handleRegisterExecutor(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestHandleListExecutors_AnyMember(t *testing.T) {
	stub := http.NewServeMux()
	stub.HandleFunc("/api/codex-exec/workspaces/ws_a/executors", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"exe_id":"exe_x","name":"alpha","description":"d","is_default":true}]`))
	})
	srv, cleanup := newExecutorsServer(t, stub)
	defer cleanup()
	seedWorkspaceMember(t, srv.DB, "ws_a", "u_dev", "developer")
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/ws_a/executors", nil).
		WithContext(auth.ContextWithUserID(context.Background(), "u_dev"))
	req = withChiURLParam(req, "wid", "ws_a")
	rr := httptest.NewRecorder()
	srv.handleListExecutors(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var rows []ListedExecutor
	json.Unmarshal(rr.Body.Bytes(), &rows)
	if len(rows) != 1 || rows[0].ExeID != "exe_x" {
		t.Errorf("rows = %+v", rows)
	}
}

func TestHandleUnbindExecutor_HappyPath(t *testing.T) {
	stub := http.NewServeMux()
	stub.HandleFunc("/api/codex-exec/workspaces/ws_a/executors/exe_x", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	srv, cleanup := newExecutorsServer(t, stub)
	defer cleanup()
	seedWorkspaceMember(t, srv.DB, "ws_a", "u1", "maintainer")
	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/ws_a/executors/exe_x", nil).
		WithContext(auth.ContextWithUserID(context.Background(), "u1"))
	req = withChiURLParam(req, "wid", "ws_a")
	req = withChiURLParam(req, "exe_id", "exe_x")
	rr := httptest.NewRecorder()
	srv.handleUnbindExecutor(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
}
