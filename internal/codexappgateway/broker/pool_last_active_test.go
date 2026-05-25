package broker

import (
	"context"
	"testing"
	"time"
)

// TestPoolLastActiveAt covers the accessor used by the supervisor's
// IdleReaper to fold broker frame activity into its reap decision.
func TestPoolLastActiveAt(t *testing.T) {
	urlFn, _, stop := countingCodexServer(t)
	defer stop()

	resolver := func(_ context.Context, _ string) (string, error) { return urlFn(""), nil }
	p := NewPool(resolver, time.Hour)
	defer p.Close()

	// Zero before any Get.
	if got := p.LastActiveAt("ws-A"); !got.IsZero() {
		t.Fatalf("expected zero LastActiveAt for unknown workspace, got %v", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	before := time.Now()
	if _, err := p.Get(ctx, "ws-A"); err != nil {
		t.Fatalf("Get: %v", err)
	}

	got := p.LastActiveAt("ws-A")
	if got.IsZero() {
		t.Fatal("expected non-zero LastActiveAt after Get")
	}
	if got.Before(before) {
		t.Errorf("LastActiveAt %v is older than the Get call started at %v", got, before)
	}

	// A different workspace remains zero.
	if got := p.LastActiveAt("ws-other"); !got.IsZero() {
		t.Errorf("expected zero LastActiveAt for unrelated workspace, got %v", got)
	}
}
