package mcppublic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/server"
)

// fakeExecGateway is the bare minimum of codex-exec-gateway needed to
// answer GET /api/codex-exec/workspaces/{wid}/executors. Tests can
// configure per-workspace rows + simulate a failing or slow workspace.
type fakeExecGateway struct {
	mu        sync.Mutex
	rowsByWid map[string][]server.ListedExecutor
	failFor   map[string]bool
	delay     time.Duration
	calls     atomic.Int32
}

func (f *fakeExecGateway) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/codex-exec/workspaces/", func(w http.ResponseWriter, r *http.Request) {
		// Path: /api/codex-exec/workspaces/{wid}/executors
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/codex-exec/workspaces/"), "/")
		if len(parts) < 2 || parts[1] != "executors" {
			http.NotFound(w, r)
			return
		}
		wid := parts[0]
		f.calls.Add(1)
		if f.delay > 0 {
			time.Sleep(f.delay)
		}
		if f.failFor[wid] {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		f.mu.Lock()
		rows := f.rowsByWid[wid]
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rows)
	})
	return mux
}

func TestHTTPExecutorsSource_RequiresClient(t *testing.T) {
	if _, err := NewHTTPExecutorsSource(nil); err == nil {
		t.Fatal("want error for nil client")
	}
}

func TestHTTPExecutorsSource_ListSingleWorkspace(t *testing.T) {
	now := time.Date(2026, 6, 9, 7, 0, 0, 0, time.UTC)
	g := &fakeExecGateway{rowsByWid: map[string][]server.ListedExecutor{
		"ws_1": {
			{ExeID: "exe_a", Name: "laptop", Description: "macbook", IsDefault: true, LastSeenAt: &now},
			{ExeID: "exe_b", Name: "server"},
		},
	}}
	ts := httptest.NewServer(g.handler())
	defer ts.Close()

	client := server.NewExecutorsClient(ts.URL, "test-secret")
	src, err := NewHTTPExecutorsSource(client)
	if err != nil {
		t.Fatalf("NewHTTPExecutorsSource: %v", err)
	}

	got, err := src.ListWorkspaceExecutors(context.Background(), "ws_1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d: %+v", len(got), got)
	}
	// Sorted by name: laptop, server.
	if got[0].Name != "laptop" || got[0].ExeID != "exe_a" {
		t.Errorf("row 0 = %+v, want laptop/exe_a", got[0])
	}
	if got[1].Name != "server" || got[1].ExeID != "exe_b" {
		t.Errorf("row 1 = %+v, want server/exe_b", got[1])
	}
	if got[0].LastSeenISO == "" || got[0].LastSeenISO != now.Format(time.RFC3339) {
		t.Errorf("last_seen ISO mismatch: %q", got[0].LastSeenISO)
	}
}

func TestHTTPExecutorsSource_EmptyWorkspaceID(t *testing.T) {
	src, _ := NewHTTPExecutorsSource(server.NewExecutorsClient("http://unused", ""))
	_, err := src.ListWorkspaceExecutors(context.Background(), "")
	if err == nil {
		t.Fatal("want error for empty workspaceID")
	}
}

func TestHTTPExecutorsSource_PropagatesUpstreamError(t *testing.T) {
	g := &fakeExecGateway{
		rowsByWid: map[string][]server.ListedExecutor{},
		failFor:   map[string]bool{"ws_dead": true},
	}
	ts := httptest.NewServer(g.handler())
	defer ts.Close()

	client := server.NewExecutorsClient(ts.URL, "test-secret")
	src, _ := NewHTTPExecutorsSource(client)
	_, err := src.ListWorkspaceExecutors(context.Background(), "ws_dead")
	if err == nil {
		t.Fatal("want error when upstream 500s")
	}
	if !strings.Contains(err.Error(), "workspace ws_dead") {
		t.Errorf("error should name the failing workspace: %v", err)
	}
}

func TestHTTPExecutorsSource_SingleCallPerListRequest(t *testing.T) {
	// Post the 2026-06-15 amendment, no fanout. Each call to
	// ListWorkspaceExecutors makes exactly one HTTP request.
	g := &fakeExecGateway{rowsByWid: map[string][]server.ListedExecutor{
		"ws_1": {},
	}}
	ts := httptest.NewServer(g.handler())
	defer ts.Close()

	client := server.NewExecutorsClient(ts.URL, "")
	src, _ := NewHTTPExecutorsSource(client)

	for i := 0; i < 3; i++ {
		if _, err := src.ListWorkspaceExecutors(context.Background(), "ws_1"); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if got := g.calls.Load(); got != 3 {
		t.Errorf("want 3 upstream calls (one per List), got %d", got)
	}
}
