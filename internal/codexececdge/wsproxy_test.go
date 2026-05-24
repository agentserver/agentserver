package codexececdge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/codexexecgateway/wsticket"
	"nhooyr.io/websocket"
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

// fakeUpstream serves /codex-exec/{exe_id} as an echo server.  Returns the
// test server.
func fakeUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/codex-exec/") {
			http.NotFound(w, r)
			return
		}
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer ws.Close(websocket.StatusNormalClosure, "")
		ws.SetReadLimit(-1)
		ctx := r.Context()
		for {
			mt, data, err := ws.Read(ctx)
			if err != nil {
				return
			}
			if err := ws.Write(ctx, mt, data); err != nil {
				return
			}
		}
	}))
}

func TestWSProxy_UpstreamUnreachableReturns502(t *testing.T) {
	srv := newTestServer(t, Config{
		UpstreamBaseURL:           "http://127.0.0.1:1", // closed port
		AgentserverInternalSecret: "secret",
		UpstreamDialTimeout:       200 * time.Millisecond,
	})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	ticket, _ := wsticket.Mint("exe_1", "secret")
	resp, err := http.Get(ts.URL + "/codex-exec/exe_1?token=" + ticket)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status: got %d want 502", resp.StatusCode)
	}
}

func TestWSProxy_EchoThroughEdge(t *testing.T) {
	up := fakeUpstream(t)
	defer up.Close()

	srv := newTestServer(t, Config{
		UpstreamBaseURL:           up.URL,
		AgentserverInternalSecret: "secret",
		UpstreamDialTimeout:       2 * time.Second,
	})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	ticket, _ := wsticket.Mint("exe_1", "secret")
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/codex-exec/exe_1?token=" + url.QueryEscape(ticket)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	if err := c.Write(ctx, websocket.MessageBinary, []byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	mt, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if mt != websocket.MessageBinary || string(data) != "hello" {
		t.Errorf("echo: mt=%v data=%q", mt, data)
	}
}

func TestWSProxy_PropagatesXForwardedFor(t *testing.T) {
	gotHeaders := make(chan http.Header, 1)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders <- r.Header.Clone()
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		ws.Close(websocket.StatusNormalClosure, "")
	}))
	defer up.Close()

	srv := newTestServer(t, Config{
		UpstreamBaseURL:           up.URL,
		AgentserverInternalSecret: "secret",
		UpstreamDialTimeout:       2 * time.Second,
	})
	ts := httptest.NewServer(srv.Routes())
	defer ts.Close()

	ticket, _ := wsticket.Mint("exe_1", "secret")
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/codex-exec/exe_1?token=" + url.QueryEscape(ticket)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"User-Agent": []string{"codex_cli_rs/0.130.0 (Linux x; x86_64)"}},
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	c.Close(websocket.StatusNormalClosure, "")

	select {
	case h := <-gotHeaders:
		if h.Get("X-Forwarded-For") == "" {
			t.Error("X-Forwarded-For not set")
		}
		if h.Get("X-Real-IP") == "" {
			t.Error("X-Real-IP not set")
		}
		if !strings.Contains(h.Get("User-Agent"), "codex_cli_rs") {
			t.Errorf("User-Agent not forwarded: %q", h.Get("User-Agent"))
		}
	case <-ctx.Done():
		t.Fatal("upstream never observed headers")
	}
}
