package coredb

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgreSQLApprovalDecisionRequiresSingleConsumptionBeforeDispatch(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	running := startExecutionTestRun(t, store, pool, schema, 160_000)
	insertCancellationMember(t, pool, schema, running.Run.WorkspaceID, running.Run.ActorID, "developer")

	prepare := executionTestPrepareCommand(t, 160_100, running, "approval-happy", 1)
	prepare.PolicyDecision = PolicyDecisionAsk
	prepared, err := store.PrepareExecution(t.Context(), prepare)
	if err != nil {
		t.Fatal(err)
	}
	preparedOperation, err := store.PrepareOperation(t.Context(), executionTestPrepareOperationCommand(t, 160_200, running, prepared.Execution, 1))
	if err != nil {
		t.Fatal(err)
	}
	create := approvalTestCreateCommand(160_300, running, preparedOperation.Execution, time.Now().Add(10*time.Minute))
	created, err := store.CreateApproval(t.Context(), create)
	if err != nil || !created.Created || created.Approval.Status != ApprovalStatusPending || created.Execution.Status != ExecutionStatusPendingApproval {
		t.Fatalf("CreateApproval() = %+v, %v", created, err)
	}
	retry, err := store.CreateApproval(t.Context(), create)
	if err != nil || retry.Created || retry.Approval.ID != created.Approval.ID {
		t.Fatalf("retry CreateApproval() = %+v, %v", retry, err)
	}

	preConsumptionBegin := executionTestBeginCommand(t, 160_400, running, preparedOperation, 31)
	preConsumptionBegin.ExpectedExecutionVersion = created.Execution.Version
	if _, err := store.BeginOperationDispatch(t.Context(), preConsumptionBegin); !HasStateErrorCode(err, ErrorInvalidState) {
		t.Fatalf("BeginOperationDispatch() before consume = %v, want invalid_state", err)
	}

	contextHash := created.Approval.ContextHash.SHA256()
	decision := DecideApprovalCommand{
		ApprovalID: created.Approval.ID, WorkspaceID: running.Run.WorkspaceID, ActorID: running.Run.ActorID,
		Nonce: created.Approval.Nonce, ExpectedContextHash: contextHash,
		ExpectedApprovalVersion: created.Approval.Version, Decision: ApprovalDecisionApprove,
		Record: stateTransitionRecord(160_500),
	}
	decided, err := store.DecideApproval(t.Context(), decision)
	if err != nil || !decided.Changed || decided.Approval.Status != ApprovalStatusApproved ||
		decided.Approval.Decision != ApprovalDecisionApprove || decided.Execution.Status != ExecutionStatusPendingApproval {
		t.Fatalf("DecideApproval(approve) = %+v, %v", decided, err)
	}
	decisionRetry, err := store.DecideApproval(t.Context(), decision)
	if err != nil || decisionRetry.Changed || decisionRetry.Approval.Status != ApprovalStatusApproved {
		t.Fatalf("retry DecideApproval(approve) = %+v, %v", decisionRetry, err)
	}

	consume := ConsumeApprovalCommand{
		ApprovalID: decided.Approval.ID, ExecutionID: decided.Execution.ID, RunID: running.Run.ID,
		AttemptID: running.Attempt.ID, HolderID: running.Attempt.HolderID, Generation: running.Attempt.Generation,
		Nonce: decided.Approval.Nonce, ExpectedContextHash: contextHash,
		ExpectedApprovalVersion: decided.Approval.Version, ExpectedExecutionVersion: decided.Execution.Version,
		Record: stateTransitionRecord(160_600),
	}
	consumed, err := store.ConsumeApproval(t.Context(), consume)
	if err != nil || !consumed.Consumed || consumed.Approval.Status != ApprovalStatusConsumed ||
		consumed.Execution.Status != ExecutionStatusApproved || consumed.Approval.ConsumedAt == nil {
		t.Fatalf("ConsumeApproval() = %+v, %v", consumed, err)
	}
	consumeRetry, err := store.ConsumeApproval(t.Context(), consume)
	if err != nil || consumeRetry.Consumed || consumeRetry.Approval.Status != ApprovalStatusConsumed {
		t.Fatalf("retry ConsumeApproval() = %+v, %v", consumeRetry, err)
	}

	begin := executionTestBeginCommand(t, 160_700, running, preparedOperation, 31)
	begin.ExpectedExecutionVersion = consumed.Execution.Version
	dispatching, err := store.BeginOperationDispatch(t.Context(), begin)
	if err != nil || !dispatching.Began || dispatching.Execution.Status != ExecutionStatusDispatching {
		t.Fatalf("BeginOperationDispatch() after consume = %+v, %v", dispatching, err)
	}
	assertStateTableCount(t, pool, schema, "approvals", 1)
	assertApprovalEventLedger(t, pool, schema, running.Run.ID, 3)
}

func TestPostgreSQLApprovalConflictingDecisionsCommitExactlyOneOutcome(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	running := startExecutionTestRun(t, store, pool, schema, 161_000)
	insertCancellationMember(t, pool, schema, running.Run.WorkspaceID, running.Run.ActorID, "developer")
	created := approvalTestPrepareAndCreate(t, store, running, 161_100, "approval-race", time.Now().Add(10*time.Minute))
	contextHash := created.Approval.ContextHash.SHA256()

	approve := DecideApprovalCommand{
		ApprovalID: created.Approval.ID, WorkspaceID: running.Run.WorkspaceID, ActorID: running.Run.ActorID,
		Nonce: created.Approval.Nonce, ExpectedContextHash: contextHash,
		ExpectedApprovalVersion: created.Approval.Version, Decision: ApprovalDecisionApprove,
		Record: stateTransitionRecord(161_300),
	}
	deny := approve
	deny.Decision = ApprovalDecisionDeny
	deny.Record = stateTransitionRecord(161_400)

	start := make(chan struct{})
	results := make(chan DecideApprovalResult, 2)
	errorsChannel := make(chan error, 2)
	var wait sync.WaitGroup
	for _, command := range []DecideApprovalCommand{approve, deny} {
		command := command
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			result, err := store.DecideApproval(t.Context(), command)
			results <- result
			errorsChannel <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	close(errorsChannel)

	successes := 0
	conflicts := 0
	for err := range errorsChannel {
		if err == nil {
			successes++
		} else if HasStateErrorCode(err, ErrorIdempotencyConflict) {
			conflicts++
		} else {
			t.Fatalf("concurrent DecideApproval() error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("decision outcomes = %d success/%d conflict, want one each", successes, conflicts)
	}
	changed := 0
	for result := range results {
		if result.Changed {
			changed++
		}
	}
	if changed != 1 {
		t.Fatalf("changed decisions = %d, want 1", changed)
	}
	assertApprovalEventLedger(t, pool, schema, running.Run.ID, 2)
}

func TestPostgreSQLApprovalExpiryAndCapabilityMismatchFailClosed(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	running := startExecutionTestRun(t, store, pool, schema, 162_000)
	insertCancellationMember(t, pool, schema, running.Run.WorkspaceID, running.Run.ActorID, "developer")
	created := approvalTestPrepareAndCreate(t, store, running, 162_100, "approval-expiry", time.Now().Add(100*time.Millisecond))
	contextHash := created.Approval.ContextHash.SHA256()

	wrongNonce := DecideApprovalCommand{
		ApprovalID: created.Approval.ID, WorkspaceID: running.Run.WorkspaceID, ActorID: running.Run.ActorID,
		Nonce: stateTestUUID(162_900), ExpectedContextHash: contextHash,
		ExpectedApprovalVersion: created.Approval.Version, Decision: ApprovalDecisionApprove,
		Record: stateTransitionRecord(162_910),
	}
	if _, err := store.DecideApproval(t.Context(), wrongNonce); !HasStateErrorCode(err, ErrorIdempotencyConflict) {
		t.Fatalf("DecideApproval(wrong nonce) = %v, want idempotency_conflict", err)
	}
	wrongContext := wrongNonce
	wrongContext.Nonce = created.Approval.Nonce
	wrongContext.ExpectedContextHash[0] ^= 0xff
	if _, err := store.DecideApproval(t.Context(), wrongContext); !HasStateErrorCode(err, ErrorIdempotencyConflict) {
		t.Fatalf("DecideApproval(wrong context) = %v, want idempotency_conflict", err)
	}

	time.Sleep(125 * time.Millisecond)
	expired, err := store.ExpireApproval(t.Context(), ExpireApprovalCommand{
		ApprovalID: created.Approval.ID, Nonce: created.Approval.Nonce, ExpectedContextHash: contextHash,
		ExpectedApprovalVersion: created.Approval.Version, Record: stateTransitionRecord(162_500),
	})
	if err != nil || !expired.Changed || expired.Approval.Status != ApprovalStatusExpired ||
		expired.Execution.Status != ExecutionStatusExpired || expired.Execution.TerminalAt == nil {
		t.Fatalf("ExpireApproval() = %+v, %v", expired, err)
	}
	expiryRetry, err := store.ExpireApproval(t.Context(), ExpireApprovalCommand{
		ApprovalID: created.Approval.ID, Nonce: created.Approval.Nonce, ExpectedContextHash: contextHash,
		ExpectedApprovalVersion: created.Approval.Version, Record: stateTransitionRecord(162_600),
	})
	if err != nil || expiryRetry.Changed {
		t.Fatalf("retry ExpireApproval() = %+v, %v", expiryRetry, err)
	}
	assertApprovalEventLedger(t, pool, schema, running.Run.ID, 2)
}

func TestPostgreSQLApprovalConsumptionRechecksCurrentRBAC(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	running := startExecutionTestRun(t, store, pool, schema, 163_000)
	insertCancellationMember(t, pool, schema, running.Run.WorkspaceID, running.Run.ActorID, "developer")
	created := approvalTestPrepareAndCreate(t, store, running, 163_100, "approval-rbac", time.Now().Add(10*time.Minute))
	contextHash := created.Approval.ContextHash.SHA256()
	decided, err := store.DecideApproval(t.Context(), DecideApprovalCommand{
		ApprovalID: created.Approval.ID, WorkspaceID: running.Run.WorkspaceID, ActorID: running.Run.ActorID,
		Nonce: created.Approval.Nonce, ExpectedContextHash: contextHash,
		ExpectedApprovalVersion: created.Approval.Version, Decision: ApprovalDecisionApprove,
		Record: stateTransitionRecord(163_300),
	})
	if err != nil {
		t.Fatal(err)
	}
	updateRole := fmt.Sprintf("UPDATE %s.workspace_members SET role = 'viewer' WHERE workspace_id = $1 AND user_id = $2", quoteIdentifier(schema))
	if result, err := pool.Exec(t.Context(), updateRole, running.Run.WorkspaceID, running.Run.ActorID); err != nil || result.RowsAffected() != 1 {
		t.Fatalf("revoke approval role = %v, %v", result, err)
	}

	consumed, err := store.ConsumeApproval(t.Context(), ConsumeApprovalCommand{
		ApprovalID: decided.Approval.ID, ExecutionID: decided.Execution.ID, RunID: running.Run.ID,
		AttemptID: running.Attempt.ID, HolderID: running.Attempt.HolderID, Generation: running.Attempt.Generation,
		Nonce: decided.Approval.Nonce, ExpectedContextHash: contextHash,
		ExpectedApprovalVersion: decided.Approval.Version, ExpectedExecutionVersion: decided.Execution.Version,
		Record: stateTransitionRecord(163_400),
	})
	if err != nil || consumed.Consumed || consumed.Approval.Status != ApprovalStatusCancelled ||
		consumed.Execution.Status != ExecutionStatusCancelled || consumed.Execution.TerminalResultHash == nil {
		t.Fatalf("ConsumeApproval(revoked) = %+v, %v", consumed, err)
	}
	assertApprovalEventLedger(t, pool, schema, running.Run.ID, 3)
}

func TestPostgreSQLApprovalObservationBindsScopeAndExpiresByDatabaseTime(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	running := startExecutionTestRun(t, store, pool, schema, 165_000)
	created := approvalTestPrepareAndCreate(t, store, running, 165_100, "approval-observe", time.Now().Add(100*time.Millisecond))
	contextHash := created.Approval.ContextHash.SHA256()
	observe := ObserveApprovalCommand{
		ApprovalID: created.Approval.ID, ExecutionID: created.Execution.ID, RunID: running.Run.ID,
		AttemptID: running.Attempt.ID, HolderID: running.Attempt.HolderID, Generation: running.Attempt.Generation,
		Nonce: created.Approval.Nonce, ExpectedContextHash: contextHash,
		AfterApprovalVersion: created.Approval.Version, Record: stateTransitionRecord(165_300),
	}
	pending, err := store.ObserveApproval(t.Context(), observe)
	if err != nil || pending.Changed || pending.Approval.Status != ApprovalStatusPending || pending.Execution.Status != ExecutionStatusPendingApproval {
		t.Fatalf("ObserveApproval(pending) = %+v, %v", pending, err)
	}

	wrongScope := observe
	wrongScope.ExecutionID = stateTestUUID(165_900)
	if _, err := store.ObserveApproval(t.Context(), wrongScope); !HasStateErrorCode(err, ErrorNotFound) {
		t.Fatalf("ObserveApproval(wrong scope) = %v, want not_found", err)
	}
	wrongNonce := observe
	wrongNonce.Nonce = stateTestUUID(165_901)
	if _, err := store.ObserveApproval(t.Context(), wrongNonce); !HasStateErrorCode(err, ErrorIdempotencyConflict) {
		t.Fatalf("ObserveApproval(wrong nonce) = %v, want idempotency_conflict", err)
	}

	time.Sleep(125 * time.Millisecond)
	expired, err := store.ObserveApproval(t.Context(), observe)
	if err != nil || !expired.Changed || expired.Approval.Status != ApprovalStatusExpired ||
		expired.Execution.Status != ExecutionStatusExpired || expired.Approval.Version != created.Approval.Version+1 {
		t.Fatalf("ObserveApproval(expired) = %+v, %v", expired, err)
	}
	retry, err := store.ObserveApproval(t.Context(), observe)
	if err != nil || retry.Changed || retry.Approval.Status != ApprovalStatusExpired {
		t.Fatalf("ObserveApproval(expiry retry) = %+v, %v", retry, err)
	}
	assertApprovalEventLedger(t, pool, schema, running.Run.ID, 2)
}

func TestPostgreSQLApprovalOutboxConflictRollsBackAuthority(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	running := startExecutionTestRun(t, store, pool, schema, 164_000)
	prepared := executionTestPrepareCommand(t, 164_100, running, "approval-rollback", 1)
	prepared.PolicyDecision = PolicyDecisionAsk
	execution, err := store.PrepareExecution(t.Context(), prepared)
	if err != nil {
		t.Fatal(err)
	}
	failed := approvalTestCreateCommand(164_200, running, execution.Execution, time.Now().Add(10*time.Minute))
	preinsertOutbox(t, pool, schema, failed.Record.OutboxID, running.Run.ID)
	if _, err := store.CreateApproval(t.Context(), failed); !HasStateErrorCode(err, ErrorConflict) {
		t.Fatalf("CreateApproval(outbox conflict) = %v, want conflict", err)
	}
	assertStateTableCount(t, pool, schema, "approvals", 0)

	succeeded := failed
	succeeded.Record = stateTransitionRecord(164_300)
	created, err := store.CreateApproval(t.Context(), succeeded)
	if err != nil || !created.Created {
		t.Fatalf("CreateApproval() after rollback = %+v, %v", created, err)
	}
	assertStateTableCount(t, pool, schema, "approvals", 1)
}

func approvalTestPrepareAndCreate(t *testing.T, store *StateStore, running executionTestRunningRun, seed int, toolCallID string, expiresAt time.Time) CreateApprovalResult {
	t.Helper()
	prepare := executionTestPrepareCommand(t, seed, running, toolCallID, 1)
	prepare.PolicyDecision = PolicyDecisionAsk
	prepared, err := store.PrepareExecution(t.Context(), prepare)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateApproval(t.Context(), approvalTestCreateCommand(seed+100, running, prepared.Execution, expiresAt))
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func approvalTestCreateCommand(seed int, running executionTestRunningRun, execution Execution, expiresAt time.Time) CreateApprovalCommand {
	return CreateApprovalCommand{
		ApprovalID: stateTestUUID(seed), ExecutionID: execution.ID, RunID: running.Run.ID,
		AttemptID: running.Attempt.ID, HolderID: running.Attempt.HolderID, Generation: running.Attempt.Generation,
		ExpectedExecutionVersion: execution.Version, Nonce: stateTestUUID(seed + 1),
		RequesterID: fmt.Sprintf("gateway-%d", seed), ExpiresAt: expiresAt.UTC(), Record: stateTransitionRecord(seed + 10),
	}
}

func assertApprovalEventLedger(t *testing.T, pool *pgxpool.Pool, schema, runID string, want int) {
	t.Helper()
	query := fmt.Sprintf("SELECT pg_catalog.count(*) FROM %s.run_events WHERE run_id = $1 AND source = 'approval'", quoteIdentifier(schema))
	var count int
	if err := pool.QueryRow(t.Context(), query, runID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("approval event count = %d, want %d", count, want)
	}
}
