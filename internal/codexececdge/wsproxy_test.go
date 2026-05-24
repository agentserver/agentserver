package codexececdge

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentserver/agentserver/internal/codexexecgateway/wsticket"
)

func TestWSProxy_RejectsMissingToken(t *testing.T) {
	srv := newTestServer(t, Config{})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/codex-exec/exe_1") // no ?token=
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status: got %d want 401", resp.StatusCode)
	}
}

func TestWSProxy_RejectsBadToken(t *testing.T) {
	srv := newTestServer(t, Config{})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/codex-exec/exe_1?token=garbage")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status: got %d want 401", resp.StatusCode)
	}
}

func TestWSProxy_RejectsTokenForOtherExe(t *testing.T) {
	srv := newTestServer(t, Config{AgentserverInternalSecret: "secret"})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	ticket, _ := wsticket.Mint("exe_other", "secret")
	resp, err := http.Get(ts.URL + "/codex-exec/exe_1?token=" + ticket)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status: got %d want 401", resp.StatusCode)
	}
}
