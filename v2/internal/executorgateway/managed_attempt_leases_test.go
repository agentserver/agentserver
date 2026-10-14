package executorgateway

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/agentserver/agentserver/v2/internal/executionbackend"
	"github.com/agentserver/agentserver/v2/internal/sandboxcontract"
)

type managedAcquireFunc func(context.Context, ExecutorMCPPrincipal) (ManagedSandboxSessionLease, error)

func (f managedAcquireFunc) Acquire(ctx context.Context, p ExecutorMCPPrincipal) (ManagedSandboxSessionLease, error) {
	return f(ctx, p)
}

type managedFenceFunc func(context.Context, ExecutorMCPPrincipal, executionbackend.Target, string) error

func (f managedFenceFunc) FenceManagedTarget(ctx context.Context, p ExecutorMCPPrincipal, target executionbackend.Target, reason string) error {
	return f(ctx, p, target, reason)
}

type targetTestLease struct {
	*recordingManagedSandboxSessionLease
	target     executionbackend.Target
	repository *sandboxcontract.RepositoryContext
}

func (l *targetTestLease) Target() executionbackend.Target                       { return l.target }
func (l *targetTestLease) RepositoryContext() *sandboxcontract.RepositoryContext { return l.repository }

func TestManagedAttemptLeasesShareAcrossTransports(t *testing.T) {
	base := &targetTestLease{recordingManagedSandboxSessionLease: &recordingManagedSandboxSessionLease{done: make(chan struct{})}, repository: &sandboxcontract.RepositoryContext{Version: 1}}
	acquirer := &recordingManagedSandboxSessionAcquirer{lease: base}
	pool := newManagedAttemptLeases(acquirer)
	p := testExecutorMCPPrincipal("same-attempt")
	const count = 16
	refs := make([]ManagedSandboxSessionLease, count)
	var wg sync.WaitGroup
	for i := range refs {
		wg.Go(func() {
			ref, err := pool.Acquire(t.Context(), p)
			if err != nil {
				t.Error(err)
				return
			}
			refs[i] = ref
		})
	}
	wg.Wait()
	if acquirer.callCount() != 1 {
		t.Fatalf("acquires = %d", acquirer.callCount())
	}
	for _, ref := range refs[:count-1] {
		if ref.(repositoryContextLease).RepositoryContext() != base.repository {
			t.Fatal("repository instructions lost")
		}
		if err := ref.Release(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := ref.Release(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if base.releaseCallCount() != 0 {
		t.Fatal("one MCP close released another MCP client's activity")
	}
	select {
	case <-refs[count-1].Done():
		t.Fatal("remaining transport lost its lease")
	default:
	}
	if err := refs[count-1].Release(t.Context()); err != nil {
		t.Fatal(err)
	}
	if base.releaseCallCount() != 1 {
		t.Fatalf("releases = %d", base.releaseCallCount())
	}
}

func TestManagedAttemptLeasesRejectAuthorityDrift(t *testing.T) {
	p := testExecutorMCPPrincipal("same-attempt")
	base := &recordingManagedSandboxSessionLease{done: make(chan struct{})}
	pool := newManagedAttemptLeases(&recordingManagedSandboxSessionAcquirer{lease: base})
	ref, err := pool.Acquire(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	defer ref.Release(t.Context())
	for _, change := range []func(*ExecutorMCPPrincipal){
		func(p *ExecutorMCPPrincipal) { p.CapabilityID = "another-capability" },
		func(p *ExecutorMCPPrincipal) { p.Run.HolderID = "another-holder" },
		func(p *ExecutorMCPPrincipal) { p.ExecutorID = testEnvironmentID },
		func(p *ExecutorMCPPrincipal) { p.PermissionMode = "full-access" },
	} {
		other := p
		change(&other)
		if _, err := pool.Acquire(t.Context(), other); err == nil {
			t.Fatal("authority drift shared existing activity")
		}
	}
}

func TestManagedAttemptLeasesWaitForReleaseBeforeReacquiring(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := testExecutorMCPPrincipal("release-barrier")
		started, unblock := make(chan struct{}, 1), make(chan struct{})
		first := newBlockingManagedSandboxSessionLease(started, unblock)
		second := &recordingManagedSandboxSessionLease{done: make(chan struct{})}
		calls := 0
		pool := newManagedAttemptLeases(managedAcquireFunc(func(context.Context, ExecutorMCPPrincipal) (ManagedSandboxSessionLease, error) {
			calls++
			if calls == 1 {
				return first, nil
			}
			return second, nil
		}))
		old, err := pool.Acquire(t.Context(), p)
		if err != nil {
			t.Fatal(err)
		}
		go old.Release(t.Context())
		<-started
		ctx, cancel := context.WithCancel(t.Context())
		cancelled := make(chan error, 1)
		go func() { _, err := pool.Acquire(ctx, p); cancelled <- err }()
		results := make(chan ManagedSandboxSessionLease, 1)
		go func() {
			ref, err := pool.Acquire(t.Context(), p)
			if err != nil {
				t.Error(err)
			}
			results <- ref
		}()
		synctest.Wait()
		if calls != 1 {
			t.Fatal("new acquire overtook in-flight release")
		}
		cancel()
		if err := <-cancelled; !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled wait = %v", err)
		}
		close(unblock)
		ref := <-results
		if calls != 2 {
			t.Fatalf("acquires = %d", calls)
		}
		if err := ref.Release(t.Context()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestManagedAttemptLeasesFencingInvalidatesImmediately(t *testing.T) {
	p := testExecutorMCPPrincipal("fence-invalidation")
	target := executionbackend.Target{Kind: executionbackend.KindKubernetes, ID: "65000000-0000-4000-8000-000000000006", Generation: 1, EnvironmentID: testEnvironmentID}
	first := &targetTestLease{recordingManagedSandboxSessionLease: &recordingManagedSandboxSessionLease{done: make(chan struct{})}, target: target}
	second := &targetTestLease{recordingManagedSandboxSessionLease: &recordingManagedSandboxSessionLease{done: make(chan struct{})}, target: target}
	second.target.Generation++
	calls := 0
	pool := newManagedAttemptLeases(managedAcquireFunc(func(context.Context, ExecutorMCPPrincipal) (ManagedSandboxSessionLease, error) {
		calls++
		if calls == 1 {
			return first, nil
		}
		return second, nil
	}))
	a, err := pool.Acquire(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	b, err := pool.Acquire(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	fencer := &attemptLeaseFencer{pool: pool, base: managedFenceFunc(func(context.Context, ExecutorMCPPrincipal, executionbackend.Target, string) error { return nil })}
	if err := fencer.FenceManagedTarget(t.Context(), p, target, "managed_shell_outcome_unknown"); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []ManagedSandboxSessionLease{a, b} {
		select {
		case <-ref.Done():
		default:
			t.Fatal("fenced target stays usable until the next 100s heartbeat")
		}
		if ref.Err() == nil {
			t.Fatal("fenced reference has no failure cause")
		}
	}
	replacement, err := pool.Acquire(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("next independent tool did not reacquire: %d", calls)
	}
	// Late closes and a duplicate fence for generation 1 cannot release gen 2.
	_ = a.Release(t.Context())
	_ = b.Release(t.Context())
	_ = fencer.FenceManagedTarget(t.Context(), p, target, "late duplicate fence")
	select {
	case <-replacement.Done():
		t.Fatal("stale cleanup invalidated replacement")
	default:
	}
	if second.releaseCallCount() != 0 {
		t.Fatal("stale reference released replacement")
	}
	_ = replacement.Release(t.Context())
	if first.releaseCallCount() != 1 || second.releaseCallCount() != 1 {
		t.Fatal("generation release counts differ")
	}
}

func TestManagedAttemptLeasesFailedAcquisitionCanRetry(t *testing.T) {
	calls := 0
	base := &recordingManagedSandboxSessionLease{done: make(chan struct{})}
	pool := newManagedAttemptLeases(managedAcquireFunc(func(context.Context, ExecutorMCPPrincipal) (ManagedSandboxSessionLease, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("temporary preparation failure")
		}
		return base, nil
	}))
	p := testExecutorMCPPrincipal("failed-acquire")
	if _, err := pool.Acquire(t.Context(), p); err == nil {
		t.Fatal("missing acquisition error")
	}
	ref, err := pool.Acquire(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	_ = ref.Release(t.Context())
}

func TestManagedAttemptLeasesUnconfirmedAcquireCleanupBlocksRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		pool := newManagedAttemptLeases(managedAcquireFunc(func(context.Context, ExecutorMCPPrincipal) (ManagedSandboxSessionLease, error) {
			calls++
			return nil, &managedAcquireCleanupError{cause: errors.New("ambiguous release after repository failure")}
		}))
		p := testExecutorMCPPrincipal("failed-acquire-cleanup")
		p.CapabilityExpiresAt = time.Now().Add(time.Minute)
		for range 2 {
			if _, err := pool.Acquire(t.Context(), p); err == nil {
				t.Fatal("unconfirmed cleanup ignored")
			}
		}
		if calls != 1 {
			t.Fatal("new acquisition raced failed setup cleanup")
		}
		time.Sleep(time.Minute)
		synctest.Wait()
	})
}

func TestManagedAttemptLeasesFailedFenceDoesNotReuseUncertainTarget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := testExecutorMCPPrincipal("failed-fence")
		p.CapabilityExpiresAt = time.Now().Add(time.Minute)
		target := executionbackend.Target{Kind: executionbackend.KindKubernetes, ID: testEnvironmentID, Generation: 1, EnvironmentID: testEnvironmentID}
		base := &targetTestLease{recordingManagedSandboxSessionLease: &recordingManagedSandboxSessionLease{done: make(chan struct{})}, target: target}
		acquirer := &recordingManagedSandboxSessionAcquirer{lease: base}
		pool := newManagedAttemptLeases(acquirer)
		ref, err := pool.Acquire(t.Context(), p)
		if err != nil {
			t.Fatal(err)
		}
		fencer := &attemptLeaseFencer{pool: pool, base: managedFenceFunc(func(context.Context, ExecutorMCPPrincipal, executionbackend.Target, string) error {
			return errors.New("delete could not be confirmed")
		})}
		if err := fencer.FenceManagedTarget(t.Context(), p, target, "managed_shell_outcome_unknown"); err == nil {
			t.Fatal("fence failure hidden")
		}
		if _, err := pool.Acquire(t.Context(), p); err == nil {
			t.Fatal("reused target after unconfirmed fencing")
		}
		if acquirer.callCount() != 1 || base.releaseCallCount() != 1 {
			t.Fatal("unconfirmed target was reacquired")
		}
		_ = ref.Release(t.Context())
		time.Sleep(time.Minute)
		synctest.Wait()
	})
}

func TestManagedAttemptLeasesCancelledCloseKeepsReleaseBarrier(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started, unblock := make(chan struct{}, 1), make(chan struct{})
		base := newBlockingManagedSandboxSessionLease(started, unblock)
		pool := newManagedAttemptLeases(&recordingManagedSandboxSessionAcquirer{lease: base})
		p := testExecutorMCPPrincipal("cancelled-close")
		ref, err := pool.Acquire(t.Context(), p)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := ref.Release(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("close = %v", err)
		}
		<-started
		waitCtx, stopWait := context.WithTimeout(t.Context(), time.Second)
		defer stopWait()
		if _, err := pool.Acquire(waitCtx, p); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("cancelled close lost its in-flight release barrier: %v", err)
		}
		close(unblock)
		if err := ref.Release(t.Context()); err != nil {
			t.Fatal(err)
		}
	})
}

type failedReleaseLease struct {
	*recordingManagedSandboxSessionLease
}

func (lease *failedReleaseLease) Release(ctx context.Context) error {
	_ = lease.recordingManagedSandboxSessionLease.Release(ctx)
	return errors.New("remote release outcome is ambiguous")
}

func TestManagedAttemptLeasesFailedReleaseCannotRaceNewAcquisition(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		base := &failedReleaseLease{&recordingManagedSandboxSessionLease{done: make(chan struct{})}}
		acquirer := &recordingManagedSandboxSessionAcquirer{lease: base}
		pool := newManagedAttemptLeases(acquirer)
		p := testExecutorMCPPrincipal("ambiguous-release")
		p.CapabilityExpiresAt = time.Now().Add(time.Minute)
		ref, err := pool.Acquire(t.Context(), p)
		if err != nil {
			t.Fatal(err)
		}
		if err := ref.Release(t.Context()); err == nil {
			t.Fatal("release failure was hidden")
		}
		if _, err := pool.Acquire(t.Context(), p); err == nil {
			t.Fatal("new lease raced ambiguous remote release")
		}
		if acquirer.callCount() != 1 {
			t.Fatal("reacquired after unconfirmed release")
		}
		time.Sleep(time.Minute)
		synctest.Wait()
		pool.mu.Lock()
		defer pool.mu.Unlock()
		if len(pool.entries) != 0 {
			t.Fatal("expired failure record leaked")
		}
	})
}

func TestManagedAttemptLeasesIsolateAttempts(t *testing.T) {
	pool := newManagedAttemptLeases(managedAcquireFunc(func(context.Context, ExecutorMCPPrincipal) (ManagedSandboxSessionLease, error) {
		return &recordingManagedSandboxSessionLease{done: make(chan struct{})}, nil
	}))
	p := testExecutorMCPPrincipal("isolated")
	a, err := pool.Acquire(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	p.Run.RunAttemptGeneration++
	b, err := pool.Acquire(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Release(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-b.Done():
		t.Fatal("old attempt release killed new attempt")
	default:
	}
	if err := b.Release(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestManagedAttemptLeasesRenewalFailureReleasesBeforeReplacement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := testExecutorMCPPrincipal("failed-heartbeat")
		first := &recordingManagedSandboxSessionLease{done: make(chan struct{})}
		second := &recordingManagedSandboxSessionLease{done: make(chan struct{})}
		calls := 0
		pool := newManagedAttemptLeases(managedAcquireFunc(func(context.Context, ExecutorMCPPrincipal) (ManagedSandboxSessionLease, error) {
			calls++
			if calls == 1 {
				return first, nil
			}
			if first.releaseCallCount() != 1 {
				t.Error("reacquired before old activity release completed")
			}
			return second, nil
		}))
		old, err := pool.Acquire(t.Context(), p)
		if err != nil {
			t.Fatal(err)
		}
		first.once.Do(func() { close(first.done) })
		synctest.Wait()
		select {
		case <-old.Done():
		default:
			t.Fatal("keepalive failure not propagated")
		}
		ref, err := pool.Acquire(t.Context(), p)
		if err != nil {
			t.Fatal(err)
		}
		_ = old.Release(t.Context())
		if second.releaseCallCount() != 0 {
			t.Fatal("old failed session released replacement")
		}
		_ = ref.Release(t.Context())
		time.Sleep(time.Millisecond)
	})
}
