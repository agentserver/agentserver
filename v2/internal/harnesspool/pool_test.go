package harnesspool

import (
	"context"
	"errors"
	"fmt"
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

func TestPoolDeterministicCoreStartupRejectionFailsRunWithoutRequeue(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	scheduler := &poolTestScheduler{leaseTTL: time.Second}
	core := newPoolTestCore(prepared)
	supervisor := attemptSupervisorFunc(func(context.Context, PreparedRunLaunch, AttemptLifecycle) error {
		return fmt.Errorf("issue runtime capabilities: %w", &CoreCommandError{
			HTTPStatus: 403, Code: "forbidden", Message: "live capability authority was rejected",
		})
	})
	pool := newPoolForTest(t, scheduler, fixedPoolPreparer{prepared: prepared}, core, supervisor, PoolConfig{
		MaxConcurrentAttempts: 1, LeaseRenewInterval: 100 * time.Millisecond,
		IdleBackoff: time.Millisecond, FailureBackoff: time.Millisecond, CleanupTimeout: time.Second,
	})

	stage, err := pool.processAttempt(t.Context(), prepared.Scheduled)
	if stage != PoolFailureSupervise || err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("deterministic rejection stage/error = %s / %v", stage, err)
	}
	core.mu.Lock()
	abandons := append([]AbandonRunAttemptRequest(nil), core.abandonRequests...)
	core.mu.Unlock()
	if len(abandons) != 1 || !abandons[0].Terminal {
		t.Fatalf("deterministic rejection abandon requests = %+v", abandons)
	}
	scheduler.mu.Lock()
	completed := append([]RunDispatch(nil), scheduler.completed...)
	released := append([]RunDispatch(nil), scheduler.released...)
	scheduler.mu.Unlock()
	if len(completed) != 1 || completed[0] != prepared.Scheduled.Dispatch || len(released) != 0 {
		t.Fatalf("deterministic rejection complete/release = %+v / %+v", completed, released)
	}
}

func TestTerminalStartupFailureClassification(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		failed bool
	}{
		{name: "forbidden", err: &CoreCommandError{HTTPStatus: 403, Code: "forbidden"}, failed: true},
		{name: "invalid argument", err: &CoreCommandError{HTTPStatus: 400, Code: "invalid_argument"}, failed: true},
		{name: "conflict", err: &CoreCommandError{HTTPStatus: 409, Code: "version_conflict"}},
		{name: "throttled", err: &CoreCommandError{HTTPStatus: 429, Code: "throttled"}},
		{name: "server failure", err: &CoreCommandError{HTTPStatus: 503, Code: "internal_error"}},
		{name: "transport failure", err: errors.New("connection reset")},
		{name: "worker stopped before control", err: errors.Join(ErrAttemptStoppedBeforeTerminal, errors.New("exit status 1")), failed: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := terminalStartupFailure(fmt.Errorf("wrapped: %w", test.err)); got != test.failed {
				t.Fatalf("terminalStartupFailure() = %v, want %v", got, test.failed)
			}
		})
	}
}

func TestPoolCommitsAcceptedFailedTurnBeforeReportingFailure(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	scheduler := &poolTestScheduler{leaseTTL: time.Second}
	core := newPoolTestCore(prepared)
	supervisor := attemptSupervisorFunc(func(_ context.Context, _ PreparedRunLaunch, lifecycle AttemptLifecycle) error {
		if err := lifecycle.ThreadStarted("thread-failed"); err != nil {
			return err
		}
		if err := lifecycle.TurnAccepted("thread-failed", "turn-failed"); err != nil {
			return err
		}
		return &AttemptTerminalError{
			Status: "failed", Code: "turn_failed", Message: "bounded diagnostic",
			ThreadID: "thread-failed", TurnID: "turn-failed",
		}
	})
	pool := newPoolForTest(t, scheduler, fixedPoolPreparer{prepared: prepared}, core, supervisor, PoolConfig{
		MaxConcurrentAttempts: 1, LeaseRenewInterval: 100 * time.Millisecond,
		IdleBackoff: time.Millisecond, FailureBackoff: time.Millisecond, CleanupTimeout: time.Second,
	})

	stage, err := pool.processAttempt(t.Context(), prepared.Scheduled)
	if stage != PoolFailureSupervise || err == nil || !strings.Contains(err.Error(), "turn_failed") {
		t.Fatalf("failed process stage/error = %s / %v", stage, err)
	}
	core.mu.Lock()
	terminals := append([]CommitAttemptTerminalRequest(nil), core.terminalRequests...)
	core.mu.Unlock()
	if len(terminals) != 1 || terminals[0].TerminalStatus != "failed" || terminals[0].ThreadID != "thread-failed" ||
		terminals[0].TurnID != "turn-failed" || terminals[0].Code != "turn_failed" || terminals[0].Message != "bounded diagnostic" {
		t.Fatalf("terminal requests = %+v", terminals)
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

func TestPoolCancellationKeepsLeaseUntilAcceptedTurnInterruptionIsCommitted(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	scheduler := &poolTestScheduler{leaseTTL: 40 * time.Millisecond}
	core := newPoolTestCore(prepared)
	turnAccepted := make(chan struct{})
	core.renewMutate = func(result *RenewRunAttemptResult) {
		select {
		case <-turnAccepted:
			result.Run.Status = "cancelling"
			result.Run.Version = 4
			result.RunAttempt.Status = "running"
			result.RunAttempt.Version = 2
		default:
		}
	}
	cleanupStarted := make(chan struct{}, 1)
	cleanupRelease := make(chan struct{})
	supervisor := attemptSupervisorFunc(func(ctx context.Context, _ PreparedRunLaunch, lifecycle AttemptLifecycle) error {
		if err := lifecycle.ThreadStarted("thread-cancel-running"); err != nil {
			return err
		}
		if err := lifecycle.TurnAccepted("thread-cancel-running", "turn-cancel-running"); err != nil {
			return err
		}
		close(turnAccepted)
		<-ctx.Done()
		cleanupStarted <- struct{}{}
		<-cleanupRelease
		return errors.Join(context.Cause(ctx), &AttemptTerminalError{
			Status: "interrupted", Code: "turn_interrupted", Message: "cancelled by user",
			ThreadID: "thread-cancel-running", TurnID: "turn-cancel-running",
		})
	})
	pool := newPoolForTest(t, scheduler, fixedPoolPreparer{prepared: prepared}, core, supervisor, PoolConfig{
		MaxConcurrentAttempts: 1, LeaseRenewInterval: 5 * time.Millisecond,
		IdleBackoff: time.Millisecond, FailureBackoff: time.Millisecond, CleanupTimeout: time.Second,
	})

	type processResult struct {
		stage PoolFailureStage
		err   error
	}
	done := make(chan processResult, 1)
	go func() {
		stage, err := pool.processAttempt(t.Context(), prepared.Scheduled)
		done <- processResult{stage: stage, err: err}
	}()
	waitPoolTestSignal(t, cleanupStarted, "cancelled workload cleanup")
	core.mu.Lock()
	renewalsAtCleanup := len(core.renewRequests)
	interruptsBeforeCleanup := len(core.terminalRequests)
	core.mu.Unlock()
	if interruptsBeforeCleanup != 0 {
		t.Fatalf("cancellation committed before workload cleanup: %d interrupt(s)", interruptsBeforeCleanup)
	}
	waitPoolTestCondition(t, "lease renewal during workload cleanup", func() bool {
		core.mu.Lock()
		defer core.mu.Unlock()
		return len(core.renewRequests) > renewalsAtCleanup
	})
	close(cleanupRelease)
	select {
	case result := <-done:
		if result.stage != PoolFailureCleanup || result.err != nil {
			t.Fatalf("cancelled process stage/error = %s / %v", result.stage, result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled attempt did not finish")
	}
	core.mu.Lock()
	interrupts := append([]CommitAttemptTerminalRequest(nil), core.terminalRequests...)
	core.mu.Unlock()
	if len(interrupts) != 1 || interrupts[0].TerminalStatus != "interrupted" ||
		interrupts[0].ThreadID != "thread-cancel-running" || interrupts[0].TurnID != "turn-cancel-running" {
		t.Fatalf("interrupt requests = %+v", interrupts)
	}
}

func TestPoolCancellationKeepsLifecycleAuthorityAliveThroughTerminalCleanup(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	scheduler := &poolTestScheduler{leaseTTL: 40 * time.Millisecond}
	core := newRuntimeAppendCore(prepared)
	turnAccepted := make(chan struct{})
	core.renewMutate = func(result *RenewRunAttemptResult) {
		select {
		case <-turnAccepted:
			result.Run.Status = "cancelling"
			result.Run.Version = 4
			result.RunAttempt.Status = "running"
			result.RunAttempt.Version = 2
		default:
		}
	}
	supervisor := attemptSupervisorFunc(func(ctx context.Context, _ PreparedRunLaunch, lifecycle AttemptLifecycle) error {
		if err := lifecycle.ThreadStarted("thread-cancel-runtime"); err != nil {
			return err
		}
		if err := lifecycle.TurnAccepted("thread-cancel-runtime", "turn-cancel-runtime"); err != nil {
			return err
		}
		close(turnAccepted)
		<-ctx.Done()
		runtimeLifecycle, ok := lifecycle.(AttemptRuntimeLifecycle)
		if !ok {
			return errors.New("attempt lifecycle does not accept runtime events")
		}
		event := appRuntimeEvent(t, "item/started", map[string]any{
			"threadId": "thread-cancel-runtime", "turnId": "turn-cancel-runtime",
			"item": map[string]any{"type": "agentMessage", "id": "message-cancel-runtime", "text": ""},
		})
		if err := runtimeLifecycle.RuntimeEvent(t.Context(), AttemptRuntimeEvent{ControlSequence: 3, Event: event}); err != nil {
			return fmt.Errorf("append runtime event during cancellation cleanup: %w", err)
		}
		return errors.Join(context.Cause(ctx), &AttemptTerminalError{
			Status: "interrupted", Code: "turn_interrupted", Message: "cancelled by user",
			ThreadID: "thread-cancel-runtime", TurnID: "turn-cancel-runtime",
		})
	})
	pool := newPoolForTest(t, scheduler, fixedPoolPreparer{prepared: prepared}, core, supervisor, PoolConfig{
		MaxConcurrentAttempts: 1, LeaseRenewInterval: 5 * time.Millisecond,
		IdleBackoff: time.Millisecond, FailureBackoff: time.Millisecond, CleanupTimeout: time.Second,
	})

	stage, err := pool.processAttempt(t.Context(), prepared.Scheduled)
	if stage != PoolFailureCleanup || err != nil {
		t.Fatalf("cancelled process stage/error = %s / %v", stage, err)
	}
	appends := core.appendSnapshot()
	if len(appends) != 1 || len(appends[0].Events) != 1 {
		t.Fatalf("runtime events committed during cancellation cleanup = %+v", appends)
	}
	core.mu.Lock()
	interruptCount := len(core.terminalRequests)
	core.mu.Unlock()
	if interruptCount != 1 {
		t.Fatalf("cancelled attempt interruption count = %d, want 1", interruptCount)
	}
}

func TestPoolCancellationWithoutAcceptedTurnTerminalProofRemainsCancelling(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	scheduler := &poolTestScheduler{leaseTTL: 40 * time.Millisecond}
	core := newPoolTestCore(prepared)
	turnAccepted := make(chan struct{})
	core.renewMutate = func(result *RenewRunAttemptResult) {
		select {
		case <-turnAccepted:
			result.Run.Status = "cancelling"
			result.Run.Version = 4
			result.RunAttempt.Status = "running"
			result.RunAttempt.Version = 2
		default:
		}
	}
	supervisor := attemptSupervisorFunc(func(ctx context.Context, _ PreparedRunLaunch, lifecycle AttemptLifecycle) error {
		if err := lifecycle.ThreadStarted("thread-cancel-unconfirmed"); err != nil {
			return err
		}
		if err := lifecycle.TurnAccepted("thread-cancel-unconfirmed", "turn-cancel-unconfirmed"); err != nil {
			return err
		}
		close(turnAccepted)
		<-ctx.Done()
		return context.Cause(ctx)
	})
	pool := newPoolForTest(t, scheduler, fixedPoolPreparer{prepared: prepared}, core, supervisor, PoolConfig{
		MaxConcurrentAttempts: 1, LeaseRenewInterval: 5 * time.Millisecond,
		IdleBackoff: time.Millisecond, FailureBackoff: time.Millisecond, CleanupTimeout: time.Second,
	})
	stage, err := pool.processAttempt(t.Context(), prepared.Scheduled)
	if stage != PoolFailureCleanup || err == nil || !strings.Contains(err.Error(), "did not confirm an interrupted stock turn") {
		t.Fatalf("unconfirmed cancellation stage/error = %s / %v", stage, err)
	}
	core.mu.Lock()
	interruptCount := len(core.interruptRequests)
	core.mu.Unlock()
	if interruptCount != 0 {
		t.Fatalf("unconfirmed cancellation committed %d interruption(s)", interruptCount)
	}
}

func TestPoolCancellationBeforeTurnAcceptanceCommitsAfterWorkloadStops(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	scheduler := &poolTestScheduler{leaseTTL: 40 * time.Millisecond}
	core := newPoolTestCore(prepared)
	core.renewMutate = func(result *RenewRunAttemptResult) {
		result.Run.Status = "cancelling"
		result.Run.Version = 3
	}
	supervisor := attemptSupervisorFunc(func(ctx context.Context, _ PreparedRunLaunch, _ AttemptLifecycle) error {
		<-ctx.Done()
		return context.Cause(ctx)
	})
	pool := newPoolForTest(t, scheduler, fixedPoolPreparer{prepared: prepared}, core, supervisor, PoolConfig{
		MaxConcurrentAttempts: 1, LeaseRenewInterval: 5 * time.Millisecond,
		IdleBackoff: time.Millisecond, FailureBackoff: time.Millisecond, CleanupTimeout: time.Second,
	})
	stage, err := pool.processAttempt(t.Context(), prepared.Scheduled)
	if stage != PoolFailureCleanup || err != nil {
		t.Fatalf("pre-turn cancellation stage/error = %s / %v", stage, err)
	}
	core.mu.Lock()
	abandons := append([]AbandonRunAttemptRequest(nil), core.abandonRequests...)
	core.mu.Unlock()
	if len(abandons) != 1 || abandons[0].Reason != "startup_failed" ||
		abandons[0].RunAttemptGeneration != prepared.Scheduled.Claim.RunAttempt.Generation {
		t.Fatalf("pre-turn abandon requests = %+v", abandons)
	}
	scheduler.mu.Lock()
	completed := len(scheduler.completed)
	scheduler.mu.Unlock()
	if completed != 1 {
		t.Fatalf("pre-turn cancelled dispatch completion count = %d", completed)
	}
}

func TestPoolCancellationDuringPreparationCommitsWithoutLaunchingWorkload(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	scheduler := &poolTestScheduler{leaseTTL: 40 * time.Millisecond}
	core := newPoolTestCore(prepared)
	core.renewMutate = func(result *RenewRunAttemptResult) {
		result.Run.Status = "cancelling"
		result.Run.Version = 3
	}
	supervisorCalls := 0
	preparer := runAttemptPreparerFunc(func(ctx context.Context, _ ScheduledRunAttempt) (PreparedRunLaunch, error) {
		<-ctx.Done()
		return PreparedRunLaunch{}, context.Cause(ctx)
	})
	pool := newPoolForTest(t, scheduler, preparer, core, attemptSupervisorFunc(
		func(context.Context, PreparedRunLaunch, AttemptLifecycle) error {
			supervisorCalls++
			return nil
		},
	), PoolConfig{
		MaxConcurrentAttempts: 1, LeaseRenewInterval: 5 * time.Millisecond,
		IdleBackoff: time.Millisecond, FailureBackoff: time.Millisecond, CleanupTimeout: time.Second,
	})
	stage, err := pool.processAttempt(t.Context(), prepared.Scheduled)
	if stage != PoolFailureCleanup || err != nil || supervisorCalls != 0 {
		t.Fatalf("prepare cancellation stage/error/supervisor calls = %s / %v / %d", stage, err, supervisorCalls)
	}
	core.mu.Lock()
	abandonCount := len(core.abandonRequests)
	core.mu.Unlock()
	if abandonCount != 1 {
		t.Fatalf("prepare cancellation abandon count = %d", abandonCount)
	}
}

func TestPoolStoppedPreTurnHandoffClosesCancellationAfterLastLeaseObservation(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	scheduler := &poolTestScheduler{leaseTTL: time.Second}
	core := newPoolTestCore(prepared)
	// No renewal reports cancellation. The atomic abandon command sees that
	// CancelRun won after the holder's final observation and closes it.
	core.abandonMutate = func(result *AbandonRunAttemptResult) {
		result.Run.Status = "cancelled"
		result.RunAttempt.Status = "interrupted"
		result.Disposition = "cancelled"
	}
	supervisor := attemptSupervisorFunc(func(context.Context, PreparedRunLaunch, AttemptLifecycle) error {
		return errors.New("worker exited before accepting a turn")
	})
	pool := newPoolForTest(t, scheduler, fixedPoolPreparer{prepared: prepared}, core, supervisor, PoolConfig{
		MaxConcurrentAttempts: 1, LeaseRenewInterval: 100 * time.Millisecond,
		IdleBackoff: time.Millisecond, FailureBackoff: time.Millisecond, CleanupTimeout: time.Second,
	})
	stage, err := pool.processAttempt(t.Context(), prepared.Scheduled)
	if stage != PoolFailureCleanup || err != nil {
		t.Fatalf("racing cancellation stage/error = %s / %v", stage, err)
	}
	core.mu.Lock()
	abandonCount := len(core.abandonRequests)
	interruptCount := len(core.interruptRequests)
	core.mu.Unlock()
	if abandonCount != 1 || interruptCount != 0 {
		t.Fatalf("racing cancellation abandon/interrupt calls = %d/%d", abandonCount, interruptCount)
	}
	scheduler.mu.Lock()
	completeCount := len(scheduler.completed)
	releaseCount := len(scheduler.released)
	scheduler.mu.Unlock()
	if completeCount != 1 || releaseCount != 0 {
		t.Fatalf("racing cancellation complete/release calls = %d/%d", completeCount, releaseCount)
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
	core.mu.Lock()
	abandons := append([]AbandonRunAttemptRequest(nil), core.abandonRequests...)
	core.mu.Unlock()
	scheduler.mu.Lock()
	completeCount := len(scheduler.completed)
	releaseCount := len(scheduler.released)
	scheduler.mu.Unlock()
	if len(abandons) != 1 || !abandons[0].Terminal || completeCount != 1 || releaseCount != 0 {
		t.Fatalf("prepared drift abandon/complete/release = %+v / %d / %d", abandons, completeCount, releaseCount)
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

type runAttemptPreparerFunc func(context.Context, ScheduledRunAttempt) (PreparedRunLaunch, error)

func (preparer runAttemptPreparerFunc) Prepare(ctx context.Context, scheduled ScheduledRunAttempt) (PreparedRunLaunch, error) {
	return preparer(ctx, scheduled)
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

	prepared          PreparedRunLaunch
	bindErrors        []error
	markErrors        []error
	renewErrors       []error
	interruptErrors   []error
	terminalErrors    []error
	abandonErrors     []error
	bindRequests      []BindBrainThreadCatalogRequest
	markRequests      []MarkTurnAcceptedRequest
	renewRequests     []RenewRunAttemptRequest
	interruptRequests []InterruptRunAttemptRequest
	terminalRequests  []CommitAttemptTerminalRequest
	abandonRequests   []AbandonRunAttemptRequest
	renewed           chan struct{}
	bindMutate        func(*BrainToolCatalog)
	markMutate        func(*MarkTurnAcceptedResult)
	renewMutate       func(*RenewRunAttemptResult)
	abandonMutate     func(*AbandonRunAttemptResult)
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
	run := core.prepared.Scheduled.Claim.Run
	attempt := core.prepared.Scheduled.Claim.RunAttempt
	result := RenewRunAttemptResult{Run: run, RunAttempt: attempt, SessionLease: lease, AttemptLease: lease}
	if core.renewMutate != nil {
		core.renewMutate(&result)
	}
	return result, nil
}

func (core *poolTestCore) InterruptRunAttempt(_ context.Context, request InterruptRunAttemptRequest) (InterruptRunAttemptResult, error) {
	core.mu.Lock()
	defer core.mu.Unlock()
	core.interruptRequests = append(core.interruptRequests, request)
	if err := popPoolTestError(&core.interruptErrors); err != nil {
		return InterruptRunAttemptResult{}, err
	}
	run := core.prepared.Scheduled.Claim.Run
	run.Status = "cancelled"
	run.Version = request.ExpectedRunVersion + 1
	attempt := core.prepared.Scheduled.Claim.RunAttempt
	attempt.Status = "interrupted"
	attempt.Version = request.ExpectedRunAttemptVersion + 1
	return InterruptRunAttemptResult{Run: run, RunAttempt: attempt, SessionVersion: 2, Changed: true}, nil
}

func (core *poolTestCore) CommitAttemptTerminal(_ context.Context, request CommitAttemptTerminalRequest) (CommitAttemptTerminalResult, error) {
	core.mu.Lock()
	defer core.mu.Unlock()
	core.terminalRequests = append(core.terminalRequests, request)
	if err := popPoolTestError(&core.terminalErrors); err != nil {
		return CommitAttemptTerminalResult{}, err
	}
	run := core.prepared.Scheduled.Claim.Run
	run.Status = request.TerminalStatus
	run.Version++
	attempt := core.prepared.Scheduled.Claim.RunAttempt
	attempt.Status = request.TerminalStatus
	attempt.Version++
	attempt.TerminalThreadID = request.ThreadID
	attempt.TerminalTurnID = request.TurnID
	disposition := request.TerminalStatus
	if core.renewMutate != nil {
		renewed := RenewRunAttemptResult{Run: run, RunAttempt: attempt}
		core.renewMutate(&renewed)
		if renewed.Run.Status == "cancelling" {
			run = renewed.Run
			run.Status = "cancelled"
			run.Version++
			attempt = renewed.RunAttempt
			attempt.Status = request.TerminalStatus
			attempt.Version++
			attempt.TerminalThreadID = request.ThreadID
			attempt.TerminalTurnID = request.TurnID
			disposition = "cancelled"
		}
	}
	return CommitAttemptTerminalResult{
		Run: run, RunAttempt: attempt, SessionVersion: 2, Disposition: disposition, Changed: true,
	}, nil
}

func (core *poolTestCore) AbandonRunAttempt(_ context.Context, request AbandonRunAttemptRequest) (AbandonRunAttemptResult, error) {
	core.mu.Lock()
	defer core.mu.Unlock()
	core.abandonRequests = append(core.abandonRequests, request)
	if err := popPoolTestError(&core.abandonErrors); err != nil {
		return AbandonRunAttemptResult{}, err
	}
	run := core.prepared.Scheduled.Claim.Run
	run.Status = "queued"
	run.Version++
	attempt := core.prepared.Scheduled.Claim.RunAttempt
	attempt.Status = "failed"
	attempt.Version++
	result := AbandonRunAttemptResult{
		Run: run, RunAttempt: attempt, SessionVersion: 2, Disposition: "requeued", Changed: true,
	}
	if request.Terminal {
		result.Run.Status = "failed"
		result.Disposition = "failed"
	}
	if core.renewMutate != nil {
		renewed := RenewRunAttemptResult{Run: run, RunAttempt: attempt}
		core.renewMutate(&renewed)
		if renewed.Run.Status == "cancelling" {
			result.Run = renewed.Run
			result.Run.Status = "cancelled"
			result.Run.Version++
			result.RunAttempt = renewed.RunAttempt
			result.RunAttempt.Status = "interrupted"
			result.RunAttempt.Version++
			result.Disposition = "cancelled"
		}
	}
	if core.abandonMutate != nil {
		core.abandonMutate(&result)
	}
	return result, nil
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

func waitPoolTestCondition(t *testing.T, label string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", label)
		}
		time.Sleep(time.Millisecond)
	}
}
