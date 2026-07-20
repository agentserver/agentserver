package browsergateway

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/internal/browsergateway/codexclient"
)

func newTestServer(t *testing.T, fc *fakeConn) *Server {
	t.Helper()
	s := NewServer(ServeConfig{CodexAppGatewayWSURL: "ws://unused", AllowedOrigins: []string{"*"}}, slog.Default())
	s.dial = func(context.Context, string) (codexConn, error) { return fc, nil }
	return s
}

func TestServer_HealthZ(t *testing.T) {
	s := newTestServer(t, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestServer_AGUI_RequiresBearer(t *testing.T) {
	s := newTestServer(t, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/agui", strings.NewReader(`{"messages":[]}`))
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestServer_AGUI_StreamsRun(t *testing.T) {
	fc := &fakeConn{frames: make(chan codexclient.Frame, 4)}
	fc.frames <- codexclient.Frame{Method: "item/completed", Params: []byte(`{"item":{"type":"agentMessage","id":"m1","text":"hi"}}`)}
	fc.frames <- codexclient.Frame{Method: "turn/completed", Params: []byte(`{"turn":{"id":"t1"}}`)}
	s := newTestServer(t, fc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/agui", strings.NewReader(`{"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer tok-1")
	s.Handler().ServeHTTP(rec, req)

	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if !strings.Contains(rec.Body.String(), "RUN_FINISHED") {
		t.Errorf("body missing RUN_FINISHED:\n%s", rec.Body.String())
	}
}
