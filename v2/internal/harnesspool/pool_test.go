package harnesspool

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAttemptLifecycleBindsCatalogThenCommitsTurnExactly(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	scheduler := &poolTestScheduler{
		leaseTTL: time.Second,
		completeErrors: []error{
			errors.New("complete response lost"), nil,
		},
	}
	core := newPoolTestCore(prepared)
	core.bindErrors = []error{errors.New("bind response lost"), nil}
	core.markErrors = []error{errors.New("mark response lost"), nil}
	identities := &poolTestTransitionAllocator{record: poolTestTransitionRecord()}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	authority := &attemptLifecycleAuthority{
		ctx: ctx, scheduler: scheduler, core: core, identities: identities, prepared: prepared,
	}

	if err := authority.ThreadStarted("thread-pool-1"); err != nil {
		t.Fatal(err)
	}
	if err := authority.TurnAccepted("thread-pool-1", "turn-pool-1"); err != nil {
		t.Fatal(err)
	}
	if err := authority.ThreadStarted("thread-pool-1"); err != nil {
		t.Fatal(err)
	}
	if err := authority.TurnAccepted("thread-pool-1", "turn-pool-1"); err != nil {
		t.Fatal(err)
	}

	core.mu.Lock()
	bindRequests := append([]BindBrainThreadCatalogRequest(nil), core.bindRequests...)
	markRequests := append([]MarkTurnAcceptedRequest(nil), core.markRequests...)
	core.mu.Unlock()
	scheduler.mu.Lock()
	completeDispatches := append([]RunDispatch(nil), scheduler.completed...)
	scheduler.mu.Unlock()
	if len(bindRequests) != 2 || !reflect.DeepEqual(bindRequests[0], bindRequests[1]) {
		t.Fatalf("ambiguous bind changed authority: %+v", bindRequests)
	}
	if len(markRequests) != 2 || !reflect.DeepEqual(markRequests[0], markRequests[1]) {
		t.Fatalf("ambiguous mark changed authority: %+v", markRequests)
	}
	if identities.calls != 1 || markRequests[0].Record != identities.record {
		t.Fatalf("turn transition allocation/request = %d / %+v", identities.calls, markRequests[0])
	}
	if len(completeDispatches) != 2 || completeDispatches[0] != prepared.Scheduled.Dispatch ||
		completeDispatches[1] != prepared.Scheduled.Dispatch || authority.dispatchCleanupError() != nil {
		t.Fatalf("dispatch completion = %+v, cleanup error = %v", completeDispatches, authority.dispatchCleanupError())
	}
	if err := authority.ThreadStarted("thread-pool-changed"); err == nil {
		t.Fatal("attempt lifecycle accepted a changed thread identity")
	}
	if err := authority.TurnAccepted("thread-pool-1", "turn-pool-changed"); err == nil {
		t.Fatal("attempt lifecycle accepted a changed turn identity")
	}
}

func TestAttemptLifecycleRequiresThreadAndReusesResumeBinding(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	scheduler := &poolTestScheduler{leaseTTL: time.Second}
	core := newPoolTestCore(prepared)
	authority := &attemptLifecycleAuthority{
		ctx: t.Context(), scheduler: scheduler, core: core,
		identities: &poolTestTransitionAllocator{record: poolTestTransitionRecord()}, prepared: prepared,
	}
	if err := authority.TurnAccepted("thread-pool-1", "turn-pool-1"); err == nil || !strings.Contains(err.Error(), "before thread") {
		t.Fatalf("turn-before-thread error = %v", err)
	}

	prepared.FrozenCatalog.ThreadID = "thread-resumed"
	authority = &attemptLifecycleAuthority{
		ctx: t.Context(), scheduler: scheduler, core: core,
		identities: &poolTestTransitionAllocator{record: poolTestTransitionRecord()}, prepared: prepared,
	}
	if err := authority.ThreadStarted("thread-resumed"); err != nil {
		t.Fatal(err)
	}
	core.mu.Lock()
	bindCalls := len(core.bindRequests)
	core.mu.Unlock()
	if bindCalls != 0 {
		t.Fatalf("resume path rebound catalog %d time(s)", bindCalls)
	}
	if err := authority.ThreadStarted("thread-other"); err == nil {
		t.Fatal("resume path accepted a different thread")
	}
}

func TestAttemptLifecycleRejectsCoreAuthorityDrift(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	scheduler := &poolTestScheduler{leaseTTL: time.Second}
	core := newPoolTestCore(prepared)
	core.bindMutate = func(catalog *BrainToolCatalog) { catalog.SessionID = "43000000-0000-4000-8000-000000000099" }
	authority := &attemptLifecycleAuthority{
		ctx: t.Context(), scheduler: scheduler, core: core,
		identities: &poolTestTransitionAllocator{record: poolTestTransitionRecord()}, prepared: prepared,
	}
	if err := authority.ThreadStarted("thread-drift"); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("bound-catalog drift error = %v", err)
	}

	core = newPoolTestCore(prepared)
	core.markMutate = func(result *MarkTurnAcceptedResult) { result.Run.Status = "starting" }
	authority = &attemptLifecycleAuthority{
		ctx: t.Context(), scheduler: scheduler, core: core,
		identities: &poolTestTransitionAllocator{record: poolTestTransitionRecord()}, prepared: prepared,
	}
	if err := authority.ThreadStarted("thread-drift"); err != nil {
		t.Fatal(err)
	}
	if err := authority.TurnAccepted("thread-drift", "turn-drift"); err == nil || !strings.Contains(err.Error(), "scheduled attempt") {
		t.Fatalf("turn transition drift error = %v", err)
	}
	scheduler.mu.Lock()
	completeCount := len(scheduler.completed)
	scheduler.mu.Unlock()
	if completeCount != 0 {
		t.Fatalf("drifted turn response completed dispatch %d time(s)", completeCount)
	}
}

func TestPoolProcessKeepsLeasesAndReleasesUnacceptedStartup(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	scheduler := &poolTestScheduler{leaseTTL: 20 * time.Millisecond}
	core := newPoolTestCore(prepared)
	core.renewed = make(chan struct{}, 1)
	supervisor := attemptSupervisorFunc(func(ctx context.Context, _ PreparedRunLaunch, _ AttemptLifecycle) error {
		select {
		case <-core.renewed:
			return errors.New("worker launch failed")
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	})
	pool := newPoolForTest(t, scheduler, fixedPoolPreparer{prepared: prepared}, core, supervisor, PoolConfig{
		MaxConcurrentAttempts: 1, LeaseRenewInterval: 5 * time.Millisecond,
		IdleBackoff: time.Millisecond, FailureBackoff: time.Millisecond, CleanupTimeout: time.Second,
	})
	stage, err := pool.processAttempt(t.Context(), prepared.Scheduled)
	if stage != PoolFailureSupervise || err == nil || !strings.Contains(err.Error(), "worker launch failed") {
		t.Fatalf("process attempt stage/error = %s / %v", stage, err)
	}
	core.mu.Lock()
	renewRequests := append([]RenewRunAttemptRequest(nil), core.renewRequests...)
	core.mu.Unlock()
	scheduler.mu.Lock()
	released := append([]RunDispatch(nil), scheduler.released...)
	scheduler.mu.Unlock()
	if len(renewRequests) == 0 || renewRequests[0].RunAttemptID != testRunAttemptID ||
		renewRequests[0].LeaseTTL != scheduler.leaseTTL || len(released) != 1 || released[0] != prepared.Scheduled.Dispatch {
		t.Fatalf("renew/release requests = %+v / %+v", renewRequests, released)
	}
}

func TestPoolLeaseLossCancelsRuntimeAndFailsClosed(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	scheduler := &poolTestScheduler{leaseTTL: 20 * time.Millisecond}
	core := newPoolTestCore(prepared)
	core.renewErrors = []error{&CoreCommandError{HTTPStatus: 409, Code: "lease_lost", Message: "fenced"}}
	runtimeCancelled := make(chan error, 1)
	supervisor := attemptSupervisorFunc(func(ctx context.Context, _ PreparedRunLaunch, _ AttemptLifecycle) error {
		<-ctx.Done()
		err := context.Cause(ctx)
		runtimeCancelled <- err
		return err
	})
	pool := newPoolForTest(t, scheduler, fixedPoolPreparer{prepared: prepared}, core, supervisor, PoolConfig{
		MaxConcurrentAttempts: 1, LeaseRenewInterval: 5 * time.Millisecond,
		IdleBackoff: time.Millisecond, FailureBackoff: time.Millisecond, CleanupTimeout: time.Second,
	})
	stage, err := pool.processAttempt(t.Context(), prepared.Scheduled)
	if stage != PoolFailureSupervise || err == nil || !strings.Contains(err.Error(), "lease_lost") {
		t.Fatalf("lease-loss stage/error = %s / %v", stage, err)
	}
	select {
	case cancellation := <-runtimeCancelled:
		if cancellation == nil || !strings.Contains(cancellation.Error(), "renew run-attempt leases") {
			t.Fatalf("runtime cancellation = %v", cancellation)
		}
	default:
		t.Fatal("lease loss did not cancel the attempt runtime")
	}
}

func TestPoolTreatsDispatchCleanupAsNonAuthoritativeAfterAcceptance(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	scheduler := &poolTestScheduler{
		leaseTTL: time.Second,
		completeErrors: []error{&CoreCommandError{
			HTTPStatus: 409, Code: "outbox_claim_lost", Message: "another consumer owns cleanup",
		}},
	}
	core := newPoolTestCore(prepared)
	supervisor := attemptSupervisorFunc(func(_ context.Context, _ PreparedRunLaunch, lifecycle AttemptLifecycle) error {
		if err := lifecycle.ThreadStarted("thread-pool-accepted"); err != nil {
			return err
		}
		return lifecycle.TurnAccepted("thread-pool-accepted", "turn-pool-accepted")
	})
	pool := newPoolForTest(t, scheduler, fixedPoolPreparer{prepared: prepared}, core, supervisor, PoolConfig{
		MaxConcurrentAttempts: 1, LeaseRenewInterval: 100 * time.Millisecond,
		IdleBackoff: time.Millisecond, FailureBackoff: time.Millisecond, CleanupTimeout: time.Second,
	})
	stage, err := pool.processAttempt(t.Context(), prepared.Scheduled)
	if stage != PoolFailureCleanup || err == nil || !strings.Contains(err.Error(), "outbox_claim_lost") {
		t.Fatalf("accepted cleanup stage/error = %s / %v", stage, err)
	}
	scheduler.mu.Lock()
	releaseCount := len(scheduler.released)
	scheduler.mu.Unlock()
	if releaseCount != 0 {
		t.Fatalf("accepted attempt released its recovery dispatch %d time(s)", releaseCount)
	}
	core.mu.Lock()
	markCount := len(core.markRequests)
	core.mu.Unlock()
	if markCount != 1 {
		t.Fatalf("turn acceptance call count = %d", markCount)
	}
}

func TestPoolRejectsPreparedAuthorityDriftBeforeSupervisor(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	prepared.SignedManifest.Manifest = append([]byte(nil), prepared.SignedManifest.Manifest...)
	prepared.SignedManifest.Manifest[0] = '['
	scheduler := &poolTestScheduler{leaseTTL: time.Second}
	core := newPoolTestCore(prepared)
	supervisorCalls := 0
	supervisor := attemptSupervisorFunc(func(context.Context, PreparedRunLaunch, AttemptLifecycle) error {
		supervisorCalls++
		return nil
	})
	pool := newPoolForTest(t, scheduler, fixedPoolPreparer{prepared: prepared}, core, supervisor, PoolConfig{
		MaxConcurrentAttempts: 1, LeaseRenewInterval: 100 * time.Millisecond,
		IdleBackoff: time.Millisecond, FailureBackoff: time.Millisecond, CleanupTimeout: time.Second,
	})
	stage, err := pool.processAttempt(t.Context(), prepared.Scheduled)
	if stage != PoolFailurePrepare || err == nil || !strings.Contains(err.Error(), "signed run manifest") || supervisorCalls != 0 {
		t.Fatalf("drift stage/error/supervisor calls = %s / %v / %d", stage, err, supervisorCalls)
	}
	scheduler.mu.Lock()
	releaseCount := len(scheduler.released)
	scheduler.mu.Unlock()
	if releaseCount != 1 {
		t.Fatalf("prepared drift release count = %d", releaseCount)
	}
}

func TestPoolConfigurationBoundsLeaseHeartbeat(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	scheduler := &poolTestScheduler{leaseTTL: 20 * time.Millisecond}
	core := newPoolTestCore(prepared)
	base := PoolConfig{
		MaxConcurrentAttempts: 1, LeaseRenewInterval: 5 * time.Millisecond,
		IdleBackoff: time.Millisecond, FailureBackoff: time.Millisecond, CleanupTimeout: time.Second,
	}
	if _, err := NewPool(scheduler, fixedPoolPreparer{prepared: prepared}, core,
		&poolTestTransitionAllocator{record: poolTestTransitionRecord()},
		attemptSupervisorFunc(func(context.Context, PreparedRunLaunch, AttemptLifecycle) error { return nil }),
		&poolTestReporter{}, base,
	); err != nil {
		t.Fatal(err)
	}
	base.LeaseRenewInterval = 11 * time.Millisecond
	if _, err := NewPool(scheduler, fixedPoolPreparer{prepared: prepared}, core,
		&poolTestTransitionAllocator{record: poolTestTransitionRecord()},
		attemptSupervisorFunc(func(context.Context, PreparedRunLaunch, AttemptLifecycle) error { return nil }),
		&poolTestReporter{}, base,
	); err == nil || !strings.Contains(err.Error(), "half") {
		t.Fatalf("unsafe heartbeat error = %v", err)
	}
}

func TestPoolRunHonorsAttemptConcurrencyAndStopsWithContext(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	claims := make(chan *ScheduledRunAttempt, 3)
	for range 3 {
		scheduled := prepared.Scheduled
		claims <- &scheduled
	}
	scheduler := &poolTestScheduler{leaseTTL: time.Second, claims: claims}
	core := newPoolTestCore(prepared)
	started := make(chan struct{}, 3)
	release := make(chan struct{}, 1)
	supervisor := attemptSupervisorFunc(func(ctx context.Context, _ PreparedRunLaunch, _ AttemptLifecycle) error {
		started <- struct{}{}
		select {
		case <-release:
			return errors.New("test runtime stopped")
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	})
	reporter := &poolTestReporter{}
	pool, err := NewPool(
		scheduler, fixedPoolPreparer{prepared: prepared}, core,
		&poolTestTransitionAllocator{record: poolTestTransitionRecord()}, supervisor, reporter,
		PoolConfig{
			MaxConcurrentAttempts: 2, LeaseRenewInterval: 100 * time.Millisecond,
			IdleBackoff: time.Millisecond, FailureBackoff: time.Millisecond, CleanupTimeout: time.Second,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- pool.Run(ctx) }()
	waitPoolTestSignal(t, started, "first attempt")
	waitPoolTestSignal(t, started, "second attempt")
	select {
	case <-started:
		t.Fatal("pool started a third attempt before a concurrency slot was released")
	case <-time.After(20 * time.Millisecond):
	}
	release <- struct{}{}
	waitPoolTestSignal(t, started, "third attempt")
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Pool.Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Pool.Run() did not stop after context cancellation")
	}
	scheduler.mu.Lock()
	claimCalls := scheduler.claimCalls
	scheduler.mu.Unlock()
	if claimCalls != 3 {
		t.Fatalf("bounded pool claim calls = %d, want 3", claimCalls)
	}
}

func TestPoolRunReportsClaimFailureAndRetriesUntilShutdown(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	scheduler := &poolTestScheduler{
		leaseTTL: time.Second, claims: make(chan *ScheduledRunAttempt),
		claimErrors: []error{errors.New("core unavailable")},
	}
	core := newPoolTestCore(prepared)
	reporter := &poolTestReporter{reported: make(chan struct{}, 1)}
	pool, err := NewPool(
		scheduler, fixedPoolPreparer{prepared: prepared}, core,
		&poolTestTransitionAllocator{record: poolTestTransitionRecord()},
		attemptSupervisorFunc(func(context.Context, PreparedRunLaunch, AttemptLifecycle) error { return nil }),
		reporter,
		PoolConfig{
			MaxConcurrentAttempts: 1, LeaseRenewInterval: 100 * time.Millisecond,
			IdleBackoff: time.Millisecond, FailureBackoff: time.Millisecond, CleanupTimeout: time.Second,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- pool.Run(ctx) }()
	waitPoolTestSignal(t, reporter.reported, "claim failure report")
	reporter.mu.Lock()
	failures := append([]PoolFailure(nil), reporter.failures...)
	reporter.mu.Unlock()
	if len(failures) != 1 || failures[0].Stage != PoolFailureClaim ||
		failures[0].RunID != "" || !strings.Contains(failures[0].Err.Error(), "core unavailable") {
		t.Fatalf("claim failure report = %+v", failures)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("pool did not stop after claim retry cancellation")
	}
}

func poolTestPreparedLaunch(t *testing.T) PreparedRunLaunch {
	t.Helper()
	preparer := newTestLaunchPreparer(
		t,
		&recordingLaunchCore{},
		&fixedCatalogAllocator{id: "45000000-0000-4000-8000-000000000004"},
		&fixedLaunchResolver{inputs: testRunLaunchInputs()},
	)
	prepared, err := preparer.Prepare(t.Context(), ScheduledRunAttempt{
		Dispatch: testControllerDispatch("starting"), Claim: testControllerClaim(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

func newPoolForTest(
	t *testing.T,
	scheduler RunAttemptScheduler,
	preparer RunAttemptPreparer,
	core AttemptSupervisionCore,
	supervisor AttemptSupervisor,
	config PoolConfig,
) *Pool {
	t.Helper()
	pool, err := NewPool(
		scheduler, preparer, core,
		&poolTestTransitionAllocator{record: poolTestTransitionRecord()},
		supervisor, &poolTestReporter{}, config,
	)
	if err != nil {
		t.Fatal(err)
	}
	return pool
}

func poolTestTransitionRecord() TransitionRecord {
	return TransitionRecord{
		EventID:            "71000000-0000-4000-8000-000000000050",
		ProducerInstanceID: "72000000-0000-4000-8000-000000000050",
		ProducerSeq:        50,
		OutboxID:           "73000000-0000-4000-8000-000000000050",
	}
}

type fixedPoolPreparer struct {
	prepared PreparedRunLaunch
	err      error
}

func (preparer fixedPoolPreparer) Prepare(context.Context, ScheduledRunAttempt) (PreparedRunLaunch, error) {
	return preparer.prepared, preparer.err
}

type attemptSupervisorFunc func(context.Context, PreparedRunLaunch, AttemptLifecycle) error

func (supervisor attemptSupervisorFunc) Supervise(ctx context.Context, prepared PreparedRunLaunch, lifecycle AttemptLifecycle) error {
	return supervisor(ctx, prepared, lifecycle)
}

type poolTestTransitionAllocator struct {
	mu     sync.Mutex
	record TransitionRecord
	err    error
	calls  int
}

func (allocator *poolTestTransitionAllocator) AllocateTransitionRecord() (TransitionRecord, error) {
	allocator.mu.Lock()
	defer allocator.mu.Unlock()
	allocator.calls++
	return allocator.record, allocator.err
}

type poolTestReporter struct {
	mu       sync.Mutex
	failures []PoolFailure
	reported chan struct{}
}

func (reporter *poolTestReporter) ReportPoolFailure(failure PoolFailure) {
	reporter.mu.Lock()
	defer reporter.mu.Unlock()
	reporter.failures = append(reporter.failures, failure)
	if reporter.reported != nil {
		select {
		case reporter.reported <- struct{}{}:
		default:
		}
	}
}

type poolTestScheduler struct {
	mu             sync.Mutex
	leaseTTL       time.Duration
	claims         chan *ScheduledRunAttempt
	claimCalls     int
	claimErrors    []error
	completeErrors []error
	releaseErrors  []error
	completed      []RunDispatch
	released       []RunDispatch
}

func (scheduler *poolTestScheduler) ClaimNextRunAttempt(ctx context.Context) (*ScheduledRunAttempt, error) {
	scheduler.mu.Lock()
	scheduler.claimCalls++
	claims := scheduler.claims
	err := popPoolTestError(&scheduler.claimErrors)
	scheduler.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if claims == nil {
		return nil, nil
	}
	select {
	case scheduled := <-claims:
		return scheduled, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (scheduler *poolTestScheduler) CompleteAcceptedDispatch(_ context.Context, dispatch RunDispatch) error {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	scheduler.completed = append(scheduler.completed, dispatch)
	return popPoolTestError(&scheduler.completeErrors)
}

func (scheduler *poolTestScheduler) ReleaseUnstartedDispatch(_ context.Context, dispatch RunDispatch) error {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	scheduler.released = append(scheduler.released, dispatch)
	return popPoolTestError(&scheduler.releaseErrors)
}

func (scheduler *poolTestScheduler) AttemptLeaseTTL() time.Duration { return scheduler.leaseTTL }

type poolTestCore struct {
	mu sync.Mutex

	prepared      PreparedRunLaunch
	bindErrors    []error
	markErrors    []error
	renewErrors   []error
	bindRequests  []BindBrainThreadCatalogRequest
	markRequests  []MarkTurnAcceptedRequest
	renewRequests []RenewRunAttemptRequest
	renewed       chan struct{}
	bindMutate    func(*BrainToolCatalog)
	markMutate    func(*MarkTurnAcceptedResult)
}

func newPoolTestCore(prepared PreparedRunLaunch) *poolTestCore {
	return &poolTestCore{prepared: prepared}
}

func (core *poolTestCore) RenewRunAttempt(_ context.Context, request RenewRunAttemptRequest) (RenewRunAttemptResult, error) {
	core.mu.Lock()
	defer core.mu.Unlock()
	core.renewRequests = append(core.renewRequests, request)
	if err := popPoolTestError(&core.renewErrors); err != nil {
		return RenewRunAttemptResult{}, err
	}
	if core.renewed != nil {
		select {
		case core.renewed <- struct{}{}:
		default:
		}
	}
	now := time.Now()
	lease := Lease{
		HolderID: request.HolderID, Generation: request.RunAttemptGeneration,
		ExpiresAt: now.Add(request.LeaseTTL), AcquiredAt: now, RenewedAt: now,
	}
	return RenewRunAttemptResult{SessionLease: lease, AttemptLease: lease}, nil
}

func (core *poolTestCore) BindBrainThreadCatalog(_ context.Context, request BindBrainThreadCatalogRequest) (BindBrainThreadCatalogResult, error) {
	core.mu.Lock()
	defer core.mu.Unlock()
	core.bindRequests = append(core.bindRequests, request)
	if err := popPoolTestError(&core.bindErrors); err != nil {
		return BindBrainThreadCatalogResult{}, err
	}
	catalog := cloneBrainToolCatalog(core.prepared.FrozenCatalog)
	catalog.ThreadID = request.ThreadID
	catalog.Version = request.ExpectedCatalogVersion + 1
	catalog.UpdatedAt = catalog.UpdatedAt.Add(time.Millisecond)
	if core.bindMutate != nil {
		core.bindMutate(&catalog)
	}
	return BindBrainThreadCatalogResult{Catalog: catalog, Changed: true}, nil
}

func (core *poolTestCore) MarkTurnAccepted(_ context.Context, request MarkTurnAcceptedRequest) (MarkTurnAcceptedResult, error) {
	core.mu.Lock()
	defer core.mu.Unlock()
	core.markRequests = append(core.markRequests, request)
	if err := popPoolTestError(&core.markErrors); err != nil {
		return MarkTurnAcceptedResult{}, err
	}
	run := core.prepared.Scheduled.Claim.Run
	run.Status = "running"
	run.Version++
	attempt := core.prepared.Scheduled.Claim.RunAttempt
	attempt.Status = "running"
	attempt.Version++
	now := time.Now()
	attempt.TurnStartedAt = &now
	result := MarkTurnAcceptedResult{Run: run, RunAttempt: attempt, Changed: true}
	if core.markMutate != nil {
		core.markMutate(&result)
	}
	return result, nil
}

func popPoolTestError(errorsList *[]error) error {
	if len(*errorsList) == 0 {
		return nil
	}
	err := (*errorsList)[0]
	*errorsList = (*errorsList)[1:]
	return err
}

func waitPoolTestSignal(t *testing.T, signal <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
}
