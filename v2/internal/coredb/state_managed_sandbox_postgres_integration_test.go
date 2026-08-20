package coredb

import (
	"crypto/sha256"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgreSQLManagedSandboxReservationPersistsTypedTTLs(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	running := startExecutionTestRun(t, store, pool, schema, 800_000)
	reserve := managedSandboxTestReserve(801_000, running)
	// The gateway does not know the provider session reference until after
	// provider create. Production reservations therefore carry the Go zero
	// value, which must be persisted as SQL NULL to satisfy the bounded-ref
	// constraint and scan back as an empty string.
	reserve.ProviderSessionRef = ""

	beforeReserve := time.Now().UTC()
	result, err := store.ReserveManagedSandbox(t.Context(), reserve)
	afterReserve := time.Now().UTC()
	if err != nil {
		t.Fatalf("ReserveManagedSandbox() error = %v", err)
	}
	if !result.Created {
		t.Fatal("ReserveManagedSandbox() did not create the initial reservation")
	}
	if result.Sandbox.ProviderSessionRef != "" {
		t.Fatalf("reserved provider session ref = %q, want empty", result.Sandbox.ProviderSessionRef)
	}
	if result.Sandbox.RequestedTTL != reserve.RequestedTTL || result.Sandbox.IdleTTL != reserve.RequestedIdleTTL {
		t.Fatalf(
			"reserved TTLs = (%s, %s), want (%s, %s)",
			result.Sandbox.RequestedTTL, result.Sandbox.IdleTTL,
			reserve.RequestedTTL, reserve.RequestedIdleTTL,
		)
	}
	if result.Sandbox.IdleExpiresAt == nil {
		t.Fatal("reserved sandbox has no idle expiry")
	}
	wantEarliestExpiry := beforeReserve.Add(reserve.RequestedIdleTTL)
	wantLatestExpiry := afterReserve.Add(reserve.RequestedIdleTTL)
	if result.Sandbox.IdleExpiresAt.Before(wantEarliestExpiry) || result.Sandbox.IdleExpiresAt.After(wantLatestExpiry) {
		t.Fatalf(
			"reserved idle expiry = %s, want between %s and %s",
			result.Sandbox.IdleExpiresAt, wantEarliestExpiry, wantLatestExpiry,
		)
	}
}

func TestPostgreSQLManagedSandboxLifecycleActivityAndTAEDispatch(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	running := startExecutionTestRun(t, store, pool, schema, 810_000)
	reserve := managedSandboxTestReserve(811_000, running)

	const contenders = 8
	results := make(chan ReserveManagedSandboxResult, contenders)
	errorsChannel := make(chan error, contenders)
	start := make(chan struct{})
	var waitGroup sync.WaitGroup
	for range contenders {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			result, err := store.ReserveManagedSandbox(t.Context(), reserve)
			results <- result
			errorsChannel <- err
		}()
	}
	close(start)
	waitGroup.Wait()
	close(results)
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatalf("concurrent ReserveManagedSandbox() error = %v", err)
		}
	}
	createdCount := 0
	var sandbox ManagedSandbox
	for result := range results {
		if result.Created {
			createdCount++
		}
		if result.Sandbox.ID != reserve.SandboxID || result.Sandbox.Generation != 1 {
			t.Fatalf("reserved sandbox = %+v, want ID %s generation 1", result.Sandbox, reserve.SandboxID)
		}
		sandbox = result.Sandbox
	}
	if createdCount != 1 {
		t.Fatalf("concurrent reservations created %d rows, want exactly one", createdCount)
	}

	creating, changed, err := store.BeginManagedSandboxCreate(t.Context(), BeginManagedSandboxCreateCommand{
		SandboxID: sandbox.ID, Generation: sandbox.Generation, ExpectedVersion: sandbox.Version,
	})
	if err != nil || !changed || creating.ObservedState != ManagedSandboxCreating {
		t.Fatalf("BeginManagedSandboxCreate() = %+v, changed = %v, error = %v", creating, changed, err)
	}
	pastExpiry := time.Now().UTC().Add(-time.Minute)
	if _, _, err := store.ObserveManagedSandbox(t.Context(), ObserveManagedSandboxCommand{
		SandboxID: creating.ID, Generation: creating.Generation, ExpectedVersion: creating.Version,
		ObservedState: ManagedSandboxReady, ProviderSessionRef: reserve.ProviderSessionRef, ExpiresAt: &pastExpiry,
	}); !HasStateErrorCode(err, ErrorInvalidArgument) {
		t.Fatalf("past-expiry ObserveManagedSandbox() error = %v, want invalid_argument", err)
	}

	readyExpiry := time.Now().UTC().Add(30 * time.Minute).Truncate(time.Microsecond)
	ready, changed, err := store.ObserveManagedSandbox(t.Context(), ObserveManagedSandboxCommand{
		SandboxID: creating.ID, Generation: creating.Generation, ExpectedVersion: creating.Version,
		ObservedState: ManagedSandboxReady, ProviderSessionRef: reserve.ProviderSessionRef, ExpiresAt: &readyExpiry,
	})
	if err != nil || !changed || ready.ObservedState != ManagedSandboxReady {
		t.Fatalf("ready ObserveManagedSandbox() = %+v, changed = %v, error = %v", ready, changed, err)
	}
	errorDigest := sha256.Sum256([]byte("provider observation timeout"))
	unknown, changed, err := store.ObserveManagedSandbox(t.Context(), ObserveManagedSandboxCommand{
		SandboxID: ready.ID, Generation: ready.Generation, ExpectedVersion: ready.Version,
		ObservedState: ManagedSandboxUnknown, ErrorCode: "provider_observation_timeout", ErrorDigest: &errorDigest,
	})
	if err != nil || !changed || unknown.ProviderSessionRef != ready.ProviderSessionRef ||
		!equalOptionalTime(unknown.ExpiresAt, ready.ExpiresAt) {
		t.Fatalf("unknown ObserveManagedSandbox() = %+v, changed = %v, error = %v", unknown, changed, err)
	}
	retryUnknown, changed, err := store.ObserveManagedSandbox(t.Context(), ObserveManagedSandboxCommand{
		SandboxID: ready.ID, Generation: ready.Generation, ExpectedVersion: ready.Version,
		ObservedState: ManagedSandboxUnknown, ErrorCode: "provider_observation_timeout", ErrorDigest: &errorDigest,
	})
	if err != nil || changed || retryUnknown.Version != unknown.Version {
		t.Fatalf("retry unknown observation = %+v, changed = %v, error = %v", retryUnknown, changed, err)
	}
	readyExpiry = time.Now().UTC().Add(30 * time.Minute).Truncate(time.Microsecond)
	ready, changed, err = store.ObserveManagedSandbox(t.Context(), ObserveManagedSandboxCommand{
		SandboxID: unknown.ID, Generation: unknown.Generation, ExpectedVersion: unknown.Version,
		ObservedState: ManagedSandboxReady, ProviderSessionRef: reserve.ProviderSessionRef, ExpiresAt: &readyExpiry,
	})
	if err != nil || !changed {
		t.Fatalf("recovered ready observation = %+v, changed = %v, error = %v", ready, changed, err)
	}

	ready, err = store.RenewManagedSandboxActivity(t.Context(), RenewManagedSandboxActivityCommand{
		SandboxID: ready.ID, Generation: ready.Generation,
		RunID: running.Run.ID, AttemptID: running.Attempt.ID,
		AttemptGeneration: running.Attempt.Generation, HolderID: running.Attempt.HolderID,
		ActivityTTL: 5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("RenewManagedSandboxActivity() error = %v", err)
	}
	setManagedSandboxIdleExpiry(t, pool, schema, ready.ID, time.Now().UTC().Add(-time.Minute))
	reconcile, err := store.ListManagedSandboxesForReconcile(t.Context(), ListManagedSandboxesForReconcileQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if containsManagedSandbox(reconcile, ready.ID, ready.Generation) {
		t.Fatal("live activity did not protect an idle-expired sandbox from reconciliation")
	}

	prepareExecution := executionTestPrepareCommand(t, 812_000, running, "tae-managed-shell", 1)
	prepareExecution.ExecutorID = ""
	prepareExecution.EnvID = reserve.EnvironmentID
	prepareExecution.Target = ready.Target()
	preparedExecution, err := store.PrepareExecution(t.Context(), prepareExecution)
	if err != nil || !preparedExecution.Created || preparedExecution.Execution.ExecutorID != "" || preparedExecution.Execution.Target != ready.Target() {
		t.Fatalf("PrepareExecution(TAE) = %+v, error = %v", preparedExecution, err)
	}
	var executorIDIsNull bool
	if err := pool.QueryRow(t.Context(), fmt.Sprintf(`
SELECT executor_id IS NULL
FROM %s.executions
WHERE id = $1`, quoteIdentifier(schema)), preparedExecution.Execution.ID).Scan(&executorIDIsNull); err != nil {
		t.Fatalf("read managed execution executor_id: %v", err)
	}
	if !executorIDIsNull {
		t.Fatal("managed TAE execution persisted a legacy executor_id")
	}
	preparedOperation, err := store.PrepareOperation(t.Context(), executionTestPrepareOperationCommand(
		t, 813_000, running, preparedExecution.Execution, 1,
	))
	if err != nil {
		t.Fatal(err)
	}
	begin := executionTestBeginCommand(t, 814_000, running, preparedOperation, 0)
	begin.Target = ready.Target()
	fenced := begin
	fenced.Target.Generation++
	if _, err := store.BeginOperationDispatch(t.Context(), fenced); !HasStateErrorCode(err, ErrorInvalidArgument) {
		t.Fatalf("wrong-generation BeginOperationDispatch(TAE) error = %v, want invalid_argument", err)
	}
	dispatching, err := store.BeginOperationDispatch(t.Context(), begin)
	if err != nil || !dispatching.Began || dispatching.Operation.Target != ready.Target() || dispatching.Operation.ConnectionGeneration != 0 {
		t.Fatalf("BeginOperationDispatch(TAE) = %+v, error = %v", dispatching, err)
	}
	authorized, err := store.AuthorizeManagedSandboxOperation(t.Context(), AuthorizeManagedSandboxOperationQuery{
		WorkspaceID: running.Run.WorkspaceID, SessionID: running.Run.SessionID,
		RunID: running.Run.ID, AttemptID: running.Attempt.ID,
		AttemptGeneration: running.Attempt.Generation,
		ExecutionID:       dispatching.Execution.ID, OperationID: dispatching.Operation.ID,
		MutationKey: dispatching.Operation.MutationKey, SandboxID: ready.ID,
		TargetGeneration: ready.Generation, EnvironmentID: ready.EnvironmentID,
		Action: ManagedSandboxActionRunCommand,
	})
	if err != nil || authorized.OperationID != dispatching.Operation.ID || authorized.OperationKind != "process_start" {
		t.Fatalf("AuthorizeManagedSandboxOperation() = %+v, error = %v", authorized, err)
	}
	wrongAction := AuthorizeManagedSandboxOperationQuery{
		WorkspaceID: running.Run.WorkspaceID, SessionID: running.Run.SessionID,
		RunID: running.Run.ID, AttemptID: running.Attempt.ID,
		AttemptGeneration: running.Attempt.Generation,
		ExecutionID:       dispatching.Execution.ID, OperationID: dispatching.Operation.ID,
		MutationKey: dispatching.Operation.MutationKey, SandboxID: ready.ID,
		TargetGeneration: ready.Generation, EnvironmentID: ready.EnvironmentID,
		Action: ManagedSandboxActionReadFile,
	}
	if _, err := store.AuthorizeManagedSandboxOperation(t.Context(), wrongAction); !HasStateErrorCode(err, ErrorLeaseLost) {
		t.Fatalf("wrong-action AuthorizeManagedSandboxOperation() error = %v, want lease_lost", err)
	}
	acknowledged, err := store.AcknowledgeOperation(t.Context(), AcknowledgeOperationCommand{
		OperationID: dispatching.Operation.ID, ExecutionID: dispatching.Execution.ID,
		RunID: running.Run.ID, AttemptID: running.Attempt.ID, Generation: running.Attempt.Generation,
		Target: ready.Target(), ExpectedExecutionVersion: dispatching.Execution.Version,
		ExpectedOperationVersion: dispatching.Operation.Version,
		AcknowledgementHash:      executionTestHash(t, HashDomainOperationAck, 815_000),
		Record:                   stateTransitionRecord(815_100),
	})
	if err != nil || !acknowledged.Changed {
		t.Fatalf("AcknowledgeOperation(TAE) = %+v, error = %v", acknowledged, err)
	}
	completed, err := store.CompleteOperation(t.Context(), CompleteOperationCommand{
		OperationID: acknowledged.Operation.ID, ExecutionID: acknowledged.Execution.ID,
		RunID: running.Run.ID, AttemptID: running.Attempt.ID, Generation: running.Attempt.Generation,
		Target: ready.Target(), ExpectedExecutionVersion: acknowledged.Execution.Version,
		ExpectedOperationVersion: acknowledged.Operation.Version, TerminalStatus: OperationStatusSucceeded,
		ResultHash: executionTestHash(t, HashDomainOperationResult, 816_000),
		Record:     stateTransitionRecord(816_100),
	})
	if err != nil || !completed.Changed || completed.Operation.Status != OperationStatusSucceeded {
		t.Fatalf("CompleteOperation(TAE) = %+v, error = %v", completed, err)
	}

	released, changed, err := store.ReleaseManagedSandboxActivity(t.Context(), ReleaseManagedSandboxActivityCommand{
		SandboxID: ready.ID, Generation: ready.Generation,
		RunID: running.Run.ID, AttemptID: running.Attempt.ID,
		AttemptGeneration: running.Attempt.Generation, HolderID: running.Attempt.HolderID,
		IdleTTL: 2 * time.Minute,
	})
	if err != nil || !changed {
		t.Fatalf("ReleaseManagedSandboxActivity() = %+v, changed = %v, error = %v", released, changed, err)
	}
	retryRelease, changed, err := store.ReleaseManagedSandboxActivity(t.Context(), ReleaseManagedSandboxActivityCommand{
		SandboxID: ready.ID, Generation: ready.Generation,
		RunID: running.Run.ID, AttemptID: running.Attempt.ID,
		AttemptGeneration: running.Attempt.Generation, HolderID: running.Attempt.HolderID,
		IdleTTL: 2 * time.Minute,
	})
	if err != nil || changed || retryRelease.Version != released.Version ||
		!equalOptionalTime(retryRelease.IdleExpiresAt, released.IdleExpiresAt) {
		t.Fatalf("retry release = %+v, changed = %v, error = %v", retryRelease, changed, err)
	}
	setManagedSandboxIdleExpiry(t, pool, schema, ready.ID, time.Now().UTC().Add(-time.Minute))
	reconcile, err = store.ListManagedSandboxesForReconcile(t.Context(), ListManagedSandboxesForReconcileQuery{Limit: 100})
	if err != nil || !containsManagedSandbox(reconcile, ready.ID, ready.Generation) {
		t.Fatalf("idle sandbox reconcile result = %+v, error = %v", reconcile, err)
	}

	current, err := store.GetManagedSandbox(t.Context(), released.ID, released.Generation)
	if err != nil {
		t.Fatal(err)
	}
	deleting, changed, err := store.BeginManagedSandboxDelete(t.Context(), BeginManagedSandboxDeleteCommand{
		SandboxID: current.ID, Generation: current.Generation, ExpectedVersion: current.Version, Reason: "session reset",
	})
	if err != nil || !changed || deleting.ObservedState != ManagedSandboxDeleting {
		t.Fatalf("BeginManagedSandboxDelete() = %+v, changed = %v, error = %v", deleting, changed, err)
	}
	deleted, changed, err := store.ObserveManagedSandbox(t.Context(), ObserveManagedSandboxCommand{
		SandboxID: deleting.ID, Generation: deleting.Generation,
		ExpectedVersion: deleting.Version, ObservedState: ManagedSandboxDeleted,
	})
	if err != nil || !changed || deleted.ProviderSessionRef != ready.ProviderSessionRef {
		t.Fatalf("deleted observation = %+v, changed = %v, error = %v", deleted, changed, err)
	}
	nextReserve := reserve
	nextReserve.SandboxID = stateTestUUID(817_000)
	nextReserve.CreateIdempotencyKey = stateTestUUID(817_001)
	nextReserve.ProviderSessionRef = "tae-session-next"
	next, err := store.ReserveManagedSandbox(t.Context(), nextReserve)
	if err != nil || !next.Created || next.Sandbox.Generation != 2 {
		t.Fatalf("next generation ReserveManagedSandbox() = %+v, error = %v", next, err)
	}
}

func managedSandboxTestReserve(seed int, running executionTestRunningRun) ReserveManagedSandboxCommand {
	return ReserveManagedSandboxCommand{
		SandboxID: stateTestUUID(seed), WorkspaceID: running.Run.WorkspaceID,
		SessionID: running.Run.SessionID, EnvironmentID: stateTestUUID(seed + 1),
		ProviderRegion: "sg", ProviderPSM: "toutiao.tae.sandbox",
		ProviderSessionRef: "tae-session-managed", CreateIdempotencyKey: stateTestUUID(seed + 2),
		RequestedTTL: 30 * time.Minute, RequestedIdleTTL: 2 * time.Minute,
	}
}

func setManagedSandboxIdleExpiry(t *testing.T, pool *pgxpool.Pool, schema, sandboxID string, expiry time.Time) {
	t.Helper()
	query := fmt.Sprintf("UPDATE %s.managed_sandboxes SET idle_expires_at = $1 WHERE id = $2", quoteIdentifier(schema))
	if _, err := pool.Exec(t.Context(), query, expiry, sandboxID); err != nil {
		t.Fatal(err)
	}
}

func containsManagedSandbox(sandboxes []ManagedSandbox, sandboxID string, generation int64) bool {
	for _, sandbox := range sandboxes {
		if sandbox.ID == sandboxID && sandbox.Generation == generation {
			return true
		}
	}
	return false
}
