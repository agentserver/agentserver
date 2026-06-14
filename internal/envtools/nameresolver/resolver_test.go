package nameresolver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestResolver_SendsBearerNotLoopbackHeader pins the 2026-06-14 auth
// header switch: the HTTP path used to send `X-Loopback-Token` to the
// app-gateway loopback; it now sends `Authorization: Bearer <cap-token>`
// directly to codex-exec-gateway. A regression here would silently
// route auth back to the dead loopback handler — server-side that's a
// 404 and env-mcp's list_environments breaks.
func TestResolver_SendsBearerNotLoopbackHeader(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("X-Loopback-Token") != "" {
			t.Errorf("resolver still sending X-Loopback-Token; should be Authorization: Bearer")
		}
		if got, want := r.Header.Get("Authorization"), "Bearer my-cap-token"; got != want {
			t.Errorf("Authorization = %q, want %q", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]ConnectedEntry{
			{ExeID: "exe_a", Name: "laptop"},
		})
	}))
	t.Cleanup(srv.Close)

	r := NewResolver(srv.URL+"/api/exec-gateway/connected", "my-cap-token", nil)
	exeID, err := r.Resolve(context.Background(), "laptop")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if exeID != "exe_a" {
		t.Errorf("exeID = %q, want exe_a", exeID)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("calls = %d, want 1", got)
	}
}

// TestResolver_CachesAcrossResolves verifies the 10s TTL — a second
// Resolve hits the cache, not the upstream.
func TestResolver_CachesAcrossResolves(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode([]ConnectedEntry{{ExeID: "exe_a", Name: "laptop"}})
	}))
	t.Cleanup(srv.Close)

	r := NewResolver(srv.URL, "tok", nil)
	for i := 0; i < 3; i++ {
		if _, err := r.Resolve(context.Background(), "laptop"); err != nil {
			t.Fatalf("Resolve #%d: %v", i, err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("calls = %d, want 1 (cache should absorb #2 and #3)", got)
	}
}

// TestResolver_MissTriggersRefresh exercises the miss → refresh →
// re-lookup path.
func TestResolver_MissTriggersRefresh(t *testing.T) {
	entries := []ConnectedEntry{}
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(entries)
	}))
	t.Cleanup(srv.Close)

	r := NewResolver(srv.URL, "tok", nil)
	if _, err := r.Resolve(context.Background(), "ghost"); err == nil {
		t.Fatal("want not-found error for ghost")
	}
	// Now upstream has it.
	entries = []ConnectedEntry{{ExeID: "exe_b", Name: "real"}}
	// Force cache staleness so refresh runs.
	r.cacheTTL = 0
	exeID, err := r.Resolve(context.Background(), "real")
	if err != nil {
		t.Fatalf("Resolve after refresh: %v", err)
	}
	if exeID != "exe_b" {
		t.Errorf("exeID = %q, want exe_b", exeID)
	}
}

// TestResolver_FetcherModeBypassesHTTP verifies the in-process
// Fetcher path (used by codex-exec-gateway's SDK) doesn't try to dial
// the URL field.
func TestResolver_FetcherModeBypassesHTTP(t *testing.T) {
	r := NewResolverWithFetcher(func(_ context.Context) ([]ConnectedEntry, error) {
		return []ConnectedEntry{{ExeID: "exe_x", Name: "in-process"}}, nil
	}, nil)
	exeID, err := r.Resolve(context.Background(), "in-process")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if exeID != "exe_x" {
		t.Errorf("exeID = %q, want exe_x", exeID)
	}
}

// TestResolver_SurfacesUpstreamErrorOnColdCache verifies that an
// upstream 5xx during a first-Resolve returns an error (no stale
// cache to fall back on).
func TestResolver_SurfacesUpstreamErrorOnColdCache(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	r := NewResolver(srv.URL, "tok", nil)
	if _, err := r.Resolve(context.Background(), "anything"); err == nil {
		t.Fatal("want error on upstream 500 with cold cache")
	}
}

// TestResolver_UsesContextTimeout makes sure the resolver respects ctx
// cancellation rather than blocking on a slow upstream.
func TestResolver_UsesContextTimeout(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(block) })

	r := NewResolver(srv.URL, "tok", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := r.Resolve(ctx, "anything"); err == nil {
		t.Fatal("want context-deadline error")
	}
}
