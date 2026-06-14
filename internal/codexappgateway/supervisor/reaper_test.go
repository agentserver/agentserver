package supervisor

import (
	"context"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/codexappgateway/codexhome"
)

func TestReaper_RetiresIdleSubprocess(t *testing.T) {
	bin := buildFakeCodex(t)
	root := t.TempDir()
	store := newFakeStore()
	mgr := codexhome.NewManager(root)
	sup := NewSupervisor(SupervisorConfig{CodexBin: bin, HomeMgr: mgr, Store: store})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	build := func() (SpawnConfig, error) { return SpawnConfig{Config: defaultConfigInput()}, nil }
	if _, err := sup.EnsureSubprocess(ctx, Key{WorkspaceID: "ws_a"}, build); err != nil {
		t.Fatal(err)
	}

	r := NewIdleReaper(sup, 50*time.Millisecond, 100*time.Millisecond, nil)
	rctx, rcancel := context.WithCancel(context.Background())
	defer rcancel()
	go r.Run(rctx)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(sup.snapshot()) == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := sup.snapshot(); len(got) != 0 {
		t.Fatalf("expected empty after reap, got %v", got)
	}
}

func TestReaper_HandleClockWriteKeepsSubprocessAlive(t *testing.T) {
	// Regression: long broker.Turn would freeze supervisor.lastActive at
	// the first-dial timestamp because broker.Pool short-circuits Get
	// without re-entering EnsureSubprocess, and broker.Turn never calls
	// supervisor.Touch. ChildHandle.LastActiveAt() now exposes the
	// supervisor entry's atomic so broker.Conn can bump the same clock
	// the reaper reads — one clock, two writers. This test simulates a
	// broker.Conn streaming frames WITHOUT calling sup.Touch.
	bin := buildFakeCodex(t)
	root := t.TempDir()
	store := newFakeStore()
	mgr := codexhome.NewManager(root)
	sup := NewSupervisor(SupervisorConfig{CodexBin: bin, HomeMgr: mgr, Store: store})
	defer sup.ShutdownAll(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	build := func() (SpawnConfig, error) { return SpawnConfig{Config: defaultConfigInput()}, nil }
	key := Key{WorkspaceID: "ws_a"}
	handle, err := sup.EnsureSubprocess(ctx, key, build)
	if err != nil {
		t.Fatal(err)
	}
	clock := handle.LastActiveAt()
	if clock == nil {
		t.Fatal("ChildHandle.LastActiveAt is nil — supervisor did not wire the atomic")
	}

	r := NewIdleReaper(sup, 30*time.Millisecond, 200*time.Millisecond, nil)
	rctx, rcancel := context.WithCancel(context.Background())
	defer rcancel()
	go r.Run(rctx)

	// Frame-pump simulation: every 50ms bump the handle's clock directly
	// (NEVER calling sup.Touch). Mirrors broker.Conn.readLoop / writeJSON.
	stop := time.After(600 * time.Millisecond)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
loop:
	for {
		select {
		case <-stop:
			break loop
		case <-tick.C:
			clock.Store(time.Now().UnixNano())
		}
	}
	if got := sup.snapshot(); len(got) != 1 {
		t.Fatalf("expected subprocess to survive (handle clock kept fresh), got %v", got)
	}
}

func TestReaper_KeepsActiveSubprocess(t *testing.T) {
	bin := buildFakeCodex(t)
	root := t.TempDir()
	store := newFakeStore()
	mgr := codexhome.NewManager(root)
	sup := NewSupervisor(SupervisorConfig{CodexBin: bin, HomeMgr: mgr, Store: store})
	defer sup.ShutdownAll(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	build := func() (SpawnConfig, error) { return SpawnConfig{Config: defaultConfigInput()}, nil }
	key := Key{WorkspaceID: "ws_a"}
	if _, err := sup.EnsureSubprocess(ctx, key, build); err != nil {
		t.Fatal(err)
	}

	r := NewIdleReaper(sup, 30*time.Millisecond, 200*time.Millisecond, nil)
	rctx, rcancel := context.WithCancel(context.Background())
	defer rcancel()
	go r.Run(rctx)

	end := time.Now().Add(600 * time.Millisecond)
	for time.Now().Before(end) {
		sup.Touch(key)
		time.Sleep(50 * time.Millisecond)
	}
	if got := sup.snapshot(); len(got) != 1 {
		t.Fatalf("expected entry to survive, got %v", got)
	}
}
