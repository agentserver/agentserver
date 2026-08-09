package harnesspool

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPoolManagedSandboxEnsuresActivityBeforeSupervisorAndReleasesOnce(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	spec := poolTestManagedSandboxSpec()
	prepared.ManagedSandbox = &spec
	lifecycle := newPoolTestManagedSandboxLifecycle()
	supervisor := attemptSupervisorFunc(func(context.Context, PreparedRunLaunch, AttemptLifecycle) error {
		lifecycle.record("supervise")
		return errors.New("test worker stopped before accepting a turn")
	})
	pool := newManagedSandboxPoolForTest(t, prepared, lifecycle, supervisor, 100*time.Millisecond)

	stage, err := pool.processAttempt(t.Context(), prepared.Scheduled)
	if stage != PoolFailureSupervise || err == nil {
		t.Fatalf("managed attempt stage/error = %s / %v", stage, err)
	}
	if got := lifecycle.eventSnapshot(); !equalStrings(got, []string{"ensure", "renew", "supervise", "release"}) {
		t.Fatalf("managed lifecycle events = %v", got)
	}
	if lifecycle.releaseCallCount() != 1 {
		t.Fatalf("managed lifecycle release calls = %d, want 1", lifecycle.releaseCallCount())
	}
}

func TestPoolManagedSandboxPeriodicallyRenewsWhileSupervisorRuns(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	spec := poolTestManagedSandboxSpec()
	prepared.ManagedSandbox = &spec
	lifecycle := newPoolTestManagedSandboxLifecycle()
	supervisor := attemptSupervisorFunc(func(ctx context.Context, _ PreparedRunLaunch, _ AttemptLifecycle) error {
		lifecycle.record("supervise")
		for {
			select {
			case call := <-lifecycle.renewed:
				if call >= 2 {
					return errors.New("periodic renewal observed")
				}
			case <-ctx.Done():
				return context.Cause(ctx)
			}
		}
	})
	pool := newManagedSandboxPoolForTest(t, prepared, lifecycle, supervisor, 5*time.Millisecond)

	stage, err := pool.processAttempt(t.Context(), prepared.Scheduled)
	if stage != PoolFailureSupervise || err == nil || !strings.Contains(err.Error(), "periodic renewal observed") {
		t.Fatalf("periodic managed attempt stage/error = %s / %v", stage, err)
	}
	if lifecycle.renewCallCount() < 2 {
		t.Fatalf("managed lifecycle renew calls = %d, want initial plus periodic", lifecycle.renewCallCount())
	}
	if lifecycle.releaseCallCount() != 1 {
		t.Fatalf("managed lifecycle release calls = %d, want 1", lifecycle.releaseCallCount())
	}
}

func TestPoolManagedSandboxRenewalFailureCancelsWorkerAndReleasesOnce(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	spec := poolTestManagedSandboxSpec()
	prepared.ManagedSandbox = &spec
	renewalFailure := errors.New("sandbox activity lease lost")
	lifecycle := newPoolTestManagedSandboxLifecycle()
	lifecycle.renewErrors = []error{nil, renewalFailure}
	var supervisorCause error
	supervisor := attemptSupervisorFunc(func(ctx context.Context, _ PreparedRunLaunch, _ AttemptLifecycle) error {
		lifecycle.record("supervise")
		<-ctx.Done()
		supervisorCause = context.Cause(ctx)
		return supervisorCause
	})
	pool := newManagedSandboxPoolForTest(t, prepared, lifecycle, supervisor, 5*time.Millisecond)

	stage, err := pool.processAttempt(t.Context(), prepared.Scheduled)
	if stage != PoolFailureSupervise || err == nil || !strings.Contains(err.Error(), renewalFailure.Error()) {
		t.Fatalf("renewal-failure stage/error = %s / %v", stage, err)
	}
	if supervisorCause == nil || !strings.Contains(supervisorCause.Error(), renewalFailure.Error()) {
		t.Fatalf("supervisor cancellation cause = %v", supervisorCause)
	}
	if lifecycle.renewCallCount() != 2 || lifecycle.releaseCallCount() != 1 {
		t.Fatalf("renew/release calls = %d/%d, want 2/1", lifecycle.renewCallCount(), lifecycle.releaseCallCount())
	}
}

func TestPoolManagedSandboxInitialRenewalFailureDoesNotStartWorkerAndReleasesOnce(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	spec := poolTestManagedSandboxSpec()
	prepared.ManagedSandbox = &spec
	lifecycle := newPoolTestManagedSandboxLifecycle()
	lifecycle.renewErrors = []error{errors.New("initial activity renewal rejected")}
	supervisorCalls := 0
	supervisor := attemptSupervisorFunc(func(context.Context, PreparedRunLaunch, AttemptLifecycle) error {
		supervisorCalls++
		return nil
	})
	pool := newManagedSandboxPoolForTest(t, prepared, lifecycle, supervisor, 100*time.Millisecond)

	stage, err := pool.processAttempt(t.Context(), prepared.Scheduled)
	if stage != PoolFailurePrepare || err == nil || !strings.Contains(err.Error(), "initial activity renewal rejected") {
		t.Fatalf("initial-renewal stage/error = %s / %v", stage, err)
	}
	if supervisorCalls != 0 {
		t.Fatalf("supervisor started %d time(s) after initial activity renewal failed", supervisorCalls)
	}
	if lifecycle.ensureCallCount() != 1 || lifecycle.renewCallCount() != 1 || lifecycle.releaseCallCount() != 1 {
		t.Fatalf("ensure/renew/release calls = %d/%d/%d, want 1/1/1",
			lifecycle.ensureCallCount(), lifecycle.renewCallCount(), lifecycle.releaseCallCount())
	}
}

func TestPoolManagedSandboxEnsureFailureDoesNotReleaseUnknownBinding(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	spec := poolTestManagedSandboxSpec()
	prepared.ManagedSandbox = &spec
	lifecycle := newPoolTestManagedSandboxLifecycle()
	lifecycle.ensureErr = errors.New("sandbox provider unavailable")
	supervisorCalls := 0
	supervisor := attemptSupervisorFunc(func(context.Context, PreparedRunLaunch, AttemptLifecycle) error {
		supervisorCalls++
		return nil
	})
	pool := newManagedSandboxPoolForTest(t, prepared, lifecycle, supervisor, 100*time.Millisecond)

	stage, err := pool.processAttempt(t.Context(), prepared.Scheduled)
	if stage != PoolFailurePrepare || err == nil || !strings.Contains(err.Error(), "sandbox provider unavailable") {
		t.Fatalf("ensure-failure stage/error = %s / %v", stage, err)
	}
	if supervisorCalls != 0 || lifecycle.renewCallCount() != 0 || lifecycle.releaseCallCount() != 0 {
		t.Fatalf("ensure failure supervisor/renew/release = %d/%d/%d, want 0/0/0",
			supervisorCalls, lifecycle.renewCallCount(), lifecycle.releaseCallCount())
	}
}

func TestManagedSandboxLaunchRejectsActivityBeyondSandboxLifetime(t *testing.T) {
	spec := poolTestManagedSandboxSpec()
	spec.SandboxTTL = 30 * time.Second
	spec.ActivityTTL = time.Minute
	scheduled := ScheduledRunAttempt{Dispatch: testControllerDispatch("starting"), Claim: testControllerClaim()}
	if err := validateManagedSandboxLaunch(scheduled, spec); err == nil || !strings.Contains(err.Error(), "must not exceed") {
		t.Fatalf("activity lifetime validation error = %v", err)
	}
}

func poolTestManagedSandboxSpec() ManagedSandboxLaunchSpec {
	return ManagedSandboxLaunchSpec{
		EnvironmentID:        "64000000-0000-4000-8000-000000000006",
		RuntimeProfileDigest: strings.Repeat("a", 64),
		PackID:               "lark-readonly@v1",
		PackSetDigest:        strings.Repeat("b", 64),
		SkillSHA256:          strings.Repeat("c", 64),
		SandboxTTL:           time.Hour,
		ActivityTTL:          time.Second,
	}
}

func newManagedSandboxPoolForTest(
	t *testing.T,
	prepared PreparedRunLaunch,
	lifecycle ManagedSandboxLifecycle,
	supervisor AttemptSupervisor,
	renewInterval time.Duration,
) *Pool {
	t.Helper()
	scheduler := &poolTestScheduler{leaseTTL: time.Second}
	core := newPoolTestCore(prepared)
	return newPoolForTest(t, scheduler, fixedPoolPreparer{prepared: prepared}, core, supervisor, PoolConfig{
		MaxConcurrentAttempts: 1, LeaseRenewInterval: renewInterval,
		IdleBackoff: time.Millisecond, FailureBackoff: time.Millisecond, CleanupTimeout: time.Second,
		ManagedSandboxLifecycle: lifecycle,
	})
}

type poolTestManagedSandboxLifecycle struct {
	mu sync.Mutex

	binding      ManagedSandboxBinding
	ensureErr    error
	renewErrors  []error
	releaseErr   error
	events       []string
	ensureCalls  int
	renewCalls   int
	releaseCalls int
	renewed      chan int
}

func newPoolTestManagedSandboxLifecycle() *poolTestManagedSandboxLifecycle {
	return &poolTestManagedSandboxLifecycle{
		binding: ManagedSandboxBinding{
			SandboxID: "65000000-0000-4000-8000-000000000006", TargetGeneration: 7,
			Root: "/workspace", ExpiresAt: time.Now().UTC().Add(time.Hour),
		},
		renewed: make(chan int, 16),
	}
}

func (lifecycle *poolTestManagedSandboxLifecycle) Ensure(context.Context, ScheduledRunAttempt, ManagedSandboxLaunchSpec) (ManagedSandboxBinding, error) {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	lifecycle.events = append(lifecycle.events, "ensure")
	lifecycle.ensureCalls++
	if lifecycle.ensureErr != nil {
		return ManagedSandboxBinding{}, lifecycle.ensureErr
	}
	return lifecycle.binding, nil
}

func (lifecycle *poolTestManagedSandboxLifecycle) Renew(context.Context, ScheduledRunAttempt, ManagedSandboxLaunchSpec, ManagedSandboxBinding) error {
	lifecycle.mu.Lock()
	lifecycle.events = append(lifecycle.events, "renew")
	lifecycle.renewCalls++
	call := lifecycle.renewCalls
	var err error
	if len(lifecycle.renewErrors) > 0 {
		err = lifecycle.renewErrors[0]
		lifecycle.renewErrors = lifecycle.renewErrors[1:]
	}
	lifecycle.mu.Unlock()
	select {
	case lifecycle.renewed <- call:
	default:
	}
	return err
}

func (lifecycle *poolTestManagedSandboxLifecycle) Release(context.Context, ScheduledRunAttempt, ManagedSandboxLaunchSpec, ManagedSandboxBinding) error {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	lifecycle.events = append(lifecycle.events, "release")
	lifecycle.releaseCalls++
	return lifecycle.releaseErr
}

func (lifecycle *poolTestManagedSandboxLifecycle) record(event string) {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	lifecycle.events = append(lifecycle.events, event)
}

func (lifecycle *poolTestManagedSandboxLifecycle) eventSnapshot() []string {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	return append([]string(nil), lifecycle.events...)
}

func (lifecycle *poolTestManagedSandboxLifecycle) ensureCallCount() int {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	return lifecycle.ensureCalls
}

func (lifecycle *poolTestManagedSandboxLifecycle) renewCallCount() int {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	return lifecycle.renewCalls
}

func (lifecycle *poolTestManagedSandboxLifecycle) releaseCallCount() int {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	return lifecycle.releaseCalls
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
