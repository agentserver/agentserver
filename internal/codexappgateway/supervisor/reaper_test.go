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

	build := func(_ string) (SpawnConfig, error) { return SpawnConfig{Config: defaultConfigInput()}, nil }
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

func TestReaper_ProbeKeepsSubprocessAliveDespiteStaleSupervisorClock(t *testing.T) {
	// Regression: long broker.Turn would freeze supervisor.lastActive at
	// the first-dial timestamp because broker.Pool short-circuits Get
	// without re-entering EnsureSubprocess. The reaper used to kill the
	// subprocess mid-turn at the IdleShutdown mark. The ActivityProbe
	// lets external clients (broker.Pool) feed their own ws-frame clock
	// into the reap decision via max(supervisor, probe).
	bin := buildFakeCodex(t)
	root := t.TempDir()
	store := newFakeStore()
	mgr := codexhome.NewManager(root)
	sup := NewSupervisor(SupervisorConfig{CodexBin: bin, HomeMgr: mgr, Store: store})
	defer sup.ShutdownAll(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	build := func(_ string) (SpawnConfig, error) { return SpawnConfig{Config: defaultConfigInput()}, nil }
	key := Key{WorkspaceID: "ws_a"}
	if _, err := sup.EnsureSubprocess(ctx, key, build); err != nil {
		t.Fatal(err)
	}

	// Probe always reports "right now" — simulates broker.Conn streaming
	// frames continuously. Critically, we do NOT call sup.Touch, mirroring
	// the broker.Turn path that doesn't reach the supervisor at all.
	r := NewIdleReaper(sup, 30*time.Millisecond, 200*time.Millisecond, nil)
	r.SetProbe(func(_ Key) time.Time { return time.Now() })
	rctx, rcancel := context.WithCancel(context.Background())
	defer rcancel()
	go r.Run(rctx)

	// Sleep well past idleAfter; if probe weren't consulted, supervisor's
	// stale lastActive would let the reaper kill the subprocess.
	time.Sleep(600 * time.Millisecond)
	if got := sup.snapshot(); len(got) != 1 {
		t.Fatalf("expected subprocess to survive thanks to probe, got %v", got)
	}
}

func TestReaper_ProbeStaleStillReaps(t *testing.T) {
	bin := buildFakeCodex(t)
	root := t.TempDir()
	store := newFakeStore()
	mgr := codexhome.NewManager(root)
	sup := NewSupervisor(SupervisorConfig{CodexBin: bin, HomeMgr: mgr, Store: store})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	build := func(_ string) (SpawnConfig, error) { return SpawnConfig{Config: defaultConfigInput()}, nil }
	if _, err := sup.EnsureSubprocess(ctx, Key{WorkspaceID: "ws_a"}, build); err != nil {
		t.Fatal(err)
	}

	// Probe returns zero (no signal) — reaper falls back to supervisor.
	r := NewIdleReaper(sup, 50*time.Millisecond, 100*time.Millisecond, nil)
	r.SetProbe(func(_ Key) time.Time { return time.Time{} })
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
		t.Fatalf("expected reap when both clocks stale, got %v", got)
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

	build := func(_ string) (SpawnConfig, error) { return SpawnConfig{Config: defaultConfigInput()}, nil }
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
