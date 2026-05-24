package codexececdge

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTestServer(t *testing.T, cfg Config) *Server {
	t.Helper()
	if cfg.UpstreamBaseURL == "" {
		cfg.UpstreamBaseURL = "http://127.0.0.1:1"
	}
	if cfg.AgentserverInternalSecret == "" {
		cfg.AgentserverInternalSecret = "test-secret"
	}
	if cfg.UpstreamDialTimeout == 0 {
		cfg.UpstreamDialTimeout = time.Second
	}
	if cfg.LogLevel == 0 {
		cfg.LogLevel = slog.LevelError // quiet in tests
	}
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return srv
}

func TestHealthz(t *testing.T) {
	srv := newTestServer(t, Config{})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status: %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Errorf("body: %q", body)
	}
}
