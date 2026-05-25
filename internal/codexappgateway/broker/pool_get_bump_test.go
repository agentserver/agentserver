package broker

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// TestPoolGetReuseRefreshesActivity guards the narrow race that would
// otherwise re-introduce the wsDisconnect bug for callers who Get an
// almost-stale conn just before a reaper tick. After Get returns a reused
// conn, lastActiveAt must reflect "now" — otherwise the reaper running
// microseconds later could close the conn before the caller's first frame.
func TestPoolGetReuseRefreshesActivity(t *testing.T) {
	urlFn, _, stop := countingCodexServer(t)
	defer stop()

	resolver := func(_ context.Context, _ string) (string, *atomic.Int64, error) { return urlFn(""), nil, nil }
	// Big idleTTL so the background reaper can't interfere with the test.
	p := NewPool(resolver, time.Hour)
	defer p.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := p.Get(ctx, "ws-A")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// Simulate a conn that has been idle for an hour: directly backdate its
	// activity timestamp. The next Get must refresh it.
	conn.lastActiveAt.Store(time.Now().Add(-time.Hour).UnixNano())
	threshold := time.Now().UnixNano()

	got, err := p.Get(ctx, "ws-A")
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if got != conn {
		t.Fatalf("Get returned different conn — pool didn't reuse")
	}
	if v := conn.lastActiveAt.Load(); v < threshold {
		t.Errorf("Get reuse left stale lastActiveAt=%d (want >= %d)", v, threshold)
	}
}
