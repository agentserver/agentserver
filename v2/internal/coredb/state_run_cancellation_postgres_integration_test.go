package coredb

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgreSQLQueuedRunCancellationIsAuthorizedIdempotentAndAtomic(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	workspaceID := stateTestUUID(150_000)
	sessionID := stateTestUUID(150_001)
	insertStateTestSession(t, pool, schema, workspaceID, sessionID)
	created := mustCreateStateRun(t, store, stateCreateRunCommand(150_010, workspaceID, sessionID, "cancel-queued"))
	insertCancellationMember(t, pool, schema, workspaceID, created.Run.ActorID, "viewer")

	command := CancelRunCommand{
		WorkspaceID: workspaceID, RunID: created.Run.ID, ActorID: created.Run.ActorID,
		Record: stateTransitionRecord(150_020),
	}
	if _, err := store.CancelRun(t.Context(), command); !HasStateErrorCode(err, ErrorForbidden) {
		t.Fatalf("viewer CancelRun() error = %v, want forbidden", err)
	}
	wrongWorkspace := command
	wrongWorkspace.WorkspaceID = stateTestUUID(150_002)
	if _, err := store.CancelRun(t.Context(), wrongWorkspace); !HasStateErrorCode(err, ErrorNotFound) {
		t.Fatalf("cross-workspace CancelRun() error = %v, want not_found", err)
	}
	updateRole := fmt.Sprintf("UPDATE %s.workspace_members SET role = 'developer' WHERE workspace_id = $1 AND user_id = $2", quoteIdentifier(schema))
	if _, err := pool.Exec(t.Context(), updateRole, workspaceID, created.Run.ActorID); err != nil {
		t.Fatal(err)
	}

	rollback := command
	rollback.Record = stateTransitionRecord(150_030)
	preinsertOutbox(t, pool, schema, rollback.Record.OutboxID, created.Run.ID)
	if _, err := store.CancelRun(t.Context(), rollback); !HasStateErrorCode(err, ErrorConflict) {
		t.Fatalf("CancelRun() outbox conflict error = %v, want conflict", err)
	}
	assertCancellationAggregate(t, pool, schema, created.Run.ID, sessionID, RunStatusQueued, created.Run.Version, created.Run.NextEventSeq, true, 0, 0)
	deleteConflict := fmt.Sprintf("DELETE FROM %s.outbox WHERE id = $1 AND kind = 'test.conflict'", quoteIdentifier(schema))
	if result, err := pool.Exec(t.Context(), deleteConflict, rollback.Record.OutboxID); err != nil || result.RowsAffected() != 1 {
		t.Fatalf("delete isolated conflict row = %v, %v", result, err)
	}

	command.Record = stateTransitionRecord(150_040)
	cancelled, err := store.CancelRun(t.Context(), command)
	if err != nil {
		t.Fatalf("CancelRun() error = %v", err)
	}
	if !cancelled.Changed || cancelled.Run.Status != RunStatusCancelled || cancelled.Run.Version != created.Run.Version+1 ||
		cancelled.Run.NextEventSeq != created.Run.NextEventSeq+1 || cancelled.SessionVersion != 3 {
		t.Fatalf("queued CancelRun() = %+v", cancelled)
	}
	retry, err := store.CancelRun(t.Context(), command)
	if err != nil || retry.Changed || retry.Run != cancelled.Run || retry.SessionVersion != cancelled.SessionVersion {
		t.Fatalf("queued CancelRun() retry = %+v, %v", retry, err)
	}
	assertCancellationAggregate(t, pool, schema, created.Run.ID, sessionID, RunStatusCancelled, cancelled.Run.Version, cancelled.Run.NextEventSeq, false, 0, 0)
	assertCancellationTransition(t, pool, schema, command.Record, created.Run.ID, nil, "run.cancelled", 2)
}

func TestPostgreSQLRunningRunCancellationRequiresExactLiveHolderAndCommitsAtomically(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	workspaceID := stateTestUUID(151_000)
	sessionID := stateTestUUID(151_001)
	insertStateTestSession(t, pool, schema, workspaceID, sessionID)
	created := mustCreateStateRun(t, store, stateCreateRunCommand(151_010, workspaceID, sessionID, "cancel-running"))
	insertCancellationMember(t, pool, schema, workspaceID, created.Run.ActorID, "developer")
	claim := mustClaimStateRun(t, store, stateClaimRunCommand(151_020, created.Run.ID, created.Run.Version, "cancel-holder"))
	accepted, err := store.MarkTurnAccepted(t.Context(), MarkTurnAcceptedCommand{
		RunID: created.Run.ID, AttemptID: claim.Attempt.ID, HolderID: claim.Attempt.HolderID,
		Generation: claim.Attempt.Generation, ExpectedRunVersion: claim.Run.Version,
		ExpectedAttemptVersion: claim.Attempt.Version, Record: stateTransitionRecord(151_030),
	})
	if err != nil {
		t.Fatal(err)
	}

	cancelCommand := CancelRunCommand{
		WorkspaceID: workspaceID, RunID: created.Run.ID, ActorID: created.Run.ActorID,
		Record: stateTransitionRecord(151_040),
	}
	cancelling, err := store.CancelRun(t.Context(), cancelCommand)
	if err != nil || !cancelling.Changed || cancelling.Run.Status != RunStatusCancelling ||
		cancelling.Run.Version != accepted.Run.Version+1 || cancelling.SessionVersion != 2 {
		t.Fatalf("running CancelRun() = %+v, %v", cancelling, err)
	}
	retry, err := store.CancelRun(t.Context(), cancelCommand)
	if err != nil || retry.Changed || retry.Run != cancelling.Run {
		t.Fatalf("running CancelRun() retry = %+v, %v", retry, err)
	}
	renewed, err := store.RenewRunAttemptLeases(t.Context(), RenewRunAttemptLeasesCommand{
		SessionID: sessionID, RunID: created.Run.ID, AttemptID: accepted.Attempt.ID,
		HolderID: accepted.Attempt.HolderID, Generation: accepted.Attempt.Generation, LeaseTTL: time.Minute,
	})
	if err != nil || renewed.Run.Status != RunStatusCancelling || renewed.Run.Version != cancelling.Run.Version ||
		renewed.Attempt.Status != AttemptStatusRunning || renewed.Attempt.Version != accepted.Attempt.Version {
		t.Fatalf("cancelling lease renewal = %+v, %v", renewed, err)
	}

	interrupt := InterruptAttemptCommand{
		RunID: created.Run.ID, AttemptID: accepted.Attempt.ID, HolderID: accepted.Attempt.HolderID,
		Generation: accepted.Attempt.Generation, ExpectedRunVersion: cancelling.Run.Version,
		ExpectedAttemptVersion: accepted.Attempt.Version, Reason: cancelReasonUser,
		Record: stateTransitionRecord(151_050),
	}
	stale := interrupt
	stale.ExpectedRunVersion--
	if _, err := store.InterruptAttempt(t.Context(), stale); !HasStateErrorCode(err, ErrorVersionConflict) {
		t.Fatalf("stale InterruptAttempt() error = %v, want version_conflict", err)
	}
	wrongHolder := interrupt
	wrongHolder.HolderID = "another-holder"
	if _, err := store.InterruptAttempt(t.Context(), wrongHolder); !HasStateErrorCode(err, ErrorLeaseLost) {
		t.Fatalf("wrong-holder InterruptAttempt() error = %v, want lease_lost", err)
	}

	rollback := interrupt
	rollback.Record = stateTransitionRecord(151_060)
	preinsertOutbox(t, pool, schema, rollback.Record.OutboxID, created.Run.ID)
	if _, err := store.InterruptAttempt(t.Context(), rollback); !HasStateErrorCode(err, ErrorConflict) {
		t.Fatalf("InterruptAttempt() outbox conflict error = %v, want conflict", err)
	}
	assertCancellationAggregate(t, pool, schema, created.Run.ID, sessionID, RunStatusCancelling, cancelling.Run.Version, cancelling.Run.NextEventSeq, true, 1, 1)
	var attemptStatus string
	var attemptVersion int64
	attemptQuery := fmt.Sprintf("SELECT status, version FROM %s.run_attempts WHERE id = $1", quoteIdentifier(schema))
	if err := pool.QueryRow(t.Context(), attemptQuery, accepted.Attempt.ID).Scan(&attemptStatus, &attemptVersion); err != nil {
		t.Fatal(err)
	}
	if attemptStatus != AttemptStatusRunning || attemptVersion != accepted.Attempt.Version {
		t.Fatalf("rolled-back attempt status/version = %s/%d", attemptStatus, attemptVersion)
	}
	deleteConflict := fmt.Sprintf("DELETE FROM %s.outbox WHERE id = $1 AND kind = 'test.conflict'", quoteIdentifier(schema))
	if result, err := pool.Exec(t.Context(), deleteConflict, rollback.Record.OutboxID); err != nil || result.RowsAffected() != 1 {
		t.Fatalf("delete isolated conflict row = %v, %v", result, err)
	}

	interrupt.Record = stateTransitionRecord(151_070)
	interrupted, err := store.InterruptAttempt(t.Context(), interrupt)
	if err != nil {
		t.Fatalf("InterruptAttempt() error = %v", err)
	}
	if !interrupted.Changed || interrupted.Run.Status != RunStatusCancelled ||
		interrupted.Attempt.Status != AttemptStatusInterrupted || interrupted.SessionVersion != 3 {
		t.Fatalf("InterruptAttempt() = %+v", interrupted)
	}
	exactRetry, err := store.InterruptAttempt(t.Context(), interrupt)
	if err != nil || exactRetry.Changed || exactRetry.Run.ID != interrupted.Run.ID ||
		exactRetry.Run.Status != interrupted.Run.Status || exactRetry.Run.Version != interrupted.Run.Version ||
		exactRetry.Attempt.ID != interrupted.Attempt.ID || exactRetry.Attempt.Status != interrupted.Attempt.Status ||
		exactRetry.Attempt.Version != interrupted.Attempt.Version || exactRetry.SessionVersion != interrupted.SessionVersion {
		t.Fatalf("InterruptAttempt() retry = %+v, %v", exactRetry, err)
	}
	assertCancellationAggregate(t, pool, schema, created.Run.ID, sessionID, RunStatusCancelled, interrupted.Run.Version, interrupted.Run.NextEventSeq, false, 0, 0)
	assertCancellationTransition(t, pool, schema, interrupt.Record, created.Run.ID, &accepted.Attempt, "run.cancelled", 5)
	if _, err := store.RenewRunAttemptLeases(t.Context(), RenewRunAttemptLeasesCommand{
		SessionID: sessionID, RunID: created.Run.ID, AttemptID: accepted.Attempt.ID,
		HolderID: accepted.Attempt.HolderID, Generation: accepted.Attempt.Generation, LeaseTTL: time.Minute,
	}); !HasStateErrorCode(err, ErrorLeaseLost) {
		t.Fatalf("post-cancel lease renewal error = %v, want lease_lost", err)
	}
}

func TestPostgreSQLRunCancellationCannotHideNonTerminalExecution(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	running := startExecutionTestRun(t, store, pool, schema, 152_000)
	insertCancellationMember(t, pool, schema, running.Run.WorkspaceID, running.Run.ActorID, "developer")
	prepared, err := store.PrepareExecution(t.Context(), executionTestPrepareCommand(t, 152_100, running, "cancel-live-execution", 1))
	if err != nil || prepared.Execution.TerminalAt != nil {
		t.Fatalf("PrepareExecution() = %+v, %v", prepared, err)
	}
	cancelling, err := store.CancelRun(t.Context(), CancelRunCommand{
		WorkspaceID: running.Run.WorkspaceID, RunID: running.Run.ID, ActorID: running.Run.ActorID,
		Record: stateTransitionRecord(152_200),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.InterruptAttempt(t.Context(), InterruptAttemptCommand{
		RunID: running.Run.ID, AttemptID: running.Attempt.ID, HolderID: running.Attempt.HolderID,
		Generation: running.Attempt.Generation, ExpectedRunVersion: cancelling.Run.Version,
		ExpectedAttemptVersion: running.Attempt.Version, Reason: cancelReasonUser,
		Record: stateTransitionRecord(152_300),
	}); !HasStateErrorCode(err, ErrorInvalidState) {
		t.Fatalf("InterruptAttempt() with live execution error = %v, want invalid_state", err)
	}
	assertCancellationAggregate(t, pool, schema, running.Run.ID, running.Run.SessionID, RunStatusCancelling, cancelling.Run.Version, cancelling.Run.NextEventSeq, true, 1, 1)
}

func TestPostgreSQLPreTurnAbandonmentAtomicallyArbitratesCancellation(t *testing.T) {
	t.Run("abandon then cancel", func(t *testing.T) {
		store, pool, schema := newPostgresStateStore(t)
		workspaceID := stateTestUUID(153_000)
		sessionID := stateTestUUID(153_001)
		insertStateTestSession(t, pool, schema, workspaceID, sessionID)
		created := mustCreateStateRun(t, store, stateCreateRunCommand(153_010, workspaceID, sessionID, "abandon-before-cancel"))
		insertCancellationMember(t, pool, schema, workspaceID, created.Run.ActorID, "developer")
		claim := mustClaimStateRun(t, store, stateClaimRunCommand(153_020, created.Run.ID, created.Run.Version, "abandon-holder"))
		command := AbandonAttemptCommand{
			RunID: created.Run.ID, AttemptID: claim.Attempt.ID, HolderID: claim.Attempt.HolderID,
			Generation: claim.Attempt.Generation, Reason: abandonReasonStartup, Record: stateTransitionRecord(153_030),
		}
		abandoned, err := store.AbandonAttempt(t.Context(), command)
		if err != nil || !abandoned.Changed || abandoned.Disposition != AbandonDispositionRequeued ||
			abandoned.Run.Status != RunStatusQueued || abandoned.Attempt.Status != AttemptStatusFailed ||
			abandoned.SessionVersion != 2 {
			t.Fatalf("AbandonAttempt() = %+v, %v", abandoned, err)
		}
		retry, err := store.AbandonAttempt(t.Context(), command)
		if err != nil || retry.Changed || retry.Disposition != AbandonDispositionRequeued ||
			retry.Run.ID != abandoned.Run.ID || retry.Run.Version != abandoned.Run.Version ||
			retry.Attempt.ID != abandoned.Attempt.ID || retry.Attempt.Version != abandoned.Attempt.Version {
			t.Fatalf("AbandonAttempt() retry = %+v, %v", retry, err)
		}
		assertCancellationAggregate(t, pool, schema, created.Run.ID, sessionID, RunStatusQueued, abandoned.Run.Version, abandoned.Run.NextEventSeq, true, 0, 0)
		assertAttemptAbandonmentTransition(
			t, pool, schema, command.Record, created.Run.ID, claim.Attempt,
			"attempt.abandoned", abandonCodeStartup, abandonMessage, 3,
		)

		cancelled, err := store.CancelRun(t.Context(), CancelRunCommand{
			WorkspaceID: workspaceID, RunID: created.Run.ID, ActorID: created.Run.ActorID,
			Record: stateTransitionRecord(153_040),
		})
		if err != nil || !cancelled.Changed || cancelled.Run.Status != RunStatusCancelled || cancelled.SessionVersion != 3 {
			t.Fatalf("CancelRun() after abandon = %+v, %v", cancelled, err)
		}
		reconciled, err := store.AbandonAttempt(t.Context(), command)
		if err != nil || reconciled.Changed || reconciled.Disposition != AbandonDispositionCancelled ||
			reconciled.Attempt.Status != AttemptStatusFailed {
			t.Fatalf("AbandonAttempt() after cancellation = %+v, %v", reconciled, err)
		}
		assertCancellationAggregate(t, pool, schema, created.Run.ID, sessionID, RunStatusCancelled, cancelled.Run.Version, cancelled.Run.NextEventSeq, false, 0, 0)
	})

	t.Run("terminal startup rejection", func(t *testing.T) {
		store, pool, schema := newPostgresStateStore(t)
		workspaceID := stateTestUUID(155_000)
		sessionID := stateTestUUID(155_001)
		insertStateTestSession(t, pool, schema, workspaceID, sessionID)
		created := mustCreateStateRun(t, store, stateCreateRunCommand(155_010, workspaceID, sessionID, "terminal-startup-rejection"))
		claim := mustClaimStateRun(t, store, stateClaimRunCommand(155_020, created.Run.ID, created.Run.Version, "terminal-abandon-holder"))
		command := AbandonAttemptCommand{
			RunID: created.Run.ID, AttemptID: claim.Attempt.ID, HolderID: claim.Attempt.HolderID,
			Generation: claim.Attempt.Generation, Reason: abandonReasonStartup, Terminal: true,
			Record: stateTransitionRecord(155_030),
		}

		failed, err := store.AbandonAttempt(t.Context(), command)
		if err != nil || !failed.Changed || failed.Disposition != AbandonDispositionFailed ||
			failed.Run.Status != RunStatusFailed || failed.Attempt.Status != AttemptStatusFailed ||
			failed.SessionVersion != 3 {
			t.Fatalf("terminal AbandonAttempt() = %+v, %v", failed, err)
		}
		retry, err := store.AbandonAttempt(t.Context(), command)
		if err != nil || retry.Changed || retry.Disposition != AbandonDispositionFailed ||
			retry.Run.ID != failed.Run.ID || retry.Run.Version != failed.Run.Version ||
			retry.Attempt.ID != failed.Attempt.ID || retry.Attempt.Version != failed.Attempt.Version ||
			retry.SessionVersion != failed.SessionVersion {
			t.Fatalf("terminal AbandonAttempt() retry = %+v, %v", retry, err)
		}
		assertCancellationAggregate(
			t, pool, schema, created.Run.ID, sessionID, RunStatusFailed,
			failed.Run.Version, failed.Run.NextEventSeq, false, 0, 0,
		)
		assertAttemptAbandonmentTransition(
			t, pool, schema, command.Record, created.Run.ID, claim.Attempt,
			"run.failed", abandonCodeStartup, abandonTerminalMessage, 3,
		)
	})

	t.Run("cancel then abandon", func(t *testing.T) {
		store, pool, schema := newPostgresStateStore(t)
		workspaceID := stateTestUUID(154_000)
		sessionID := stateTestUUID(154_001)
		insertStateTestSession(t, pool, schema, workspaceID, sessionID)
		created := mustCreateStateRun(t, store, stateCreateRunCommand(154_010, workspaceID, sessionID, "cancel-before-abandon"))
		insertCancellationMember(t, pool, schema, workspaceID, created.Run.ActorID, "developer")
		claim := mustClaimStateRun(t, store, stateClaimRunCommand(154_020, created.Run.ID, created.Run.Version, "abandon-holder"))
		cancelling, err := store.CancelRun(t.Context(), CancelRunCommand{
			WorkspaceID: workspaceID, RunID: created.Run.ID, ActorID: created.Run.ActorID,
			Record: stateTransitionRecord(154_030),
		})
		if err != nil || cancelling.Run.Status != RunStatusCancelling {
			t.Fatalf("CancelRun() before abandon = %+v, %v", cancelling, err)
		}
		command := AbandonAttemptCommand{
			RunID: created.Run.ID, AttemptID: claim.Attempt.ID, HolderID: claim.Attempt.HolderID,
			Generation: claim.Attempt.Generation, Reason: abandonReasonStartup, Terminal: true,
			Record: stateTransitionRecord(154_040),
		}
		wrongHolder := command
		wrongHolder.HolderID = "another-holder"
		if _, err := store.AbandonAttempt(t.Context(), wrongHolder); !HasStateErrorCode(err, ErrorLeaseLost) {
			t.Fatalf("wrong-holder AbandonAttempt() error = %v, want lease_lost", err)
		}
		rollback := command
		rollback.Record = stateTransitionRecord(154_050)
		preinsertOutbox(t, pool, schema, rollback.Record.OutboxID, created.Run.ID)
		if _, err := store.AbandonAttempt(t.Context(), rollback); !HasStateErrorCode(err, ErrorConflict) {
			t.Fatalf("AbandonAttempt() outbox conflict error = %v, want conflict", err)
		}
		assertCancellationAggregate(t, pool, schema, created.Run.ID, sessionID, RunStatusCancelling, cancelling.Run.Version, cancelling.Run.NextEventSeq, true, 1, 1)

		abandoned, err := store.AbandonAttempt(t.Context(), command)
		if err != nil || !abandoned.Changed || abandoned.Disposition != AbandonDispositionCancelled ||
			abandoned.Run.Status != RunStatusCancelled || abandoned.Attempt.Status != AttemptStatusInterrupted ||
			abandoned.SessionVersion != 3 {
			t.Fatalf("AbandonAttempt() after cancel = %+v, %v", abandoned, err)
		}
		assertCancellationAggregate(t, pool, schema, created.Run.ID, sessionID, RunStatusCancelled, abandoned.Run.Version, abandoned.Run.NextEventSeq, false, 0, 0)
		assertCancellationTransition(t, pool, schema, command.Record, created.Run.ID, &claim.Attempt, "run.cancelled", 4)
	})
}

func insertCancellationMember(t *testing.T, pool *pgxpool.Pool, schema, workspaceID, actorID, role string) {
	t.Helper()
	userQuery := fmt.Sprintf("INSERT INTO %s.users (id, status) VALUES ($1, 'active') ON CONFLICT (id) DO NOTHING", quoteIdentifier(schema))
	if _, err := pool.Exec(t.Context(), userQuery, actorID); err != nil {
		t.Fatal(err)
	}
	query := fmt.Sprintf("INSERT INTO %s.workspace_members (workspace_id, user_id, role) VALUES ($1, $2, $3)", quoteIdentifier(schema))
	if _, err := pool.Exec(t.Context(), query, workspaceID, actorID, role); err != nil {
		t.Fatal(err)
	}
}

func assertCancellationAggregate(
	t *testing.T,
	pool *pgxpool.Pool,
	schema, runID, sessionID, wantStatus string,
	wantVersion, wantNextSequence int64,
	wantActive bool,
	wantSessionLeases, wantAttemptLeases int,
) {
	t.Helper()
	query := fmt.Sprintf(`
SELECT r.status, r.version, r.next_event_seq, s.active_run_id::text,
       (SELECT pg_catalog.count(*) FROM %s.session_leases WHERE session_id = $2),
       (SELECT pg_catalog.count(*) FROM %s.attempt_leases AS al JOIN %s.run_attempts AS a ON a.id = al.run_attempt_id WHERE a.run_id = $1)
FROM %s.runs AS r
JOIN %s.sessions AS s ON s.id = r.session_id
WHERE r.id = $1 AND s.id = $2`,
		quoteIdentifier(schema), quoteIdentifier(schema), quoteIdentifier(schema),
		quoteIdentifier(schema), quoteIdentifier(schema),
	)
	var status string
	var version, nextSequence int64
	var activeRunID *string
	var sessionLeases, attemptLeases int
	if err := pool.QueryRow(t.Context(), query, runID, sessionID).Scan(
		&status, &version, &nextSequence, &activeRunID, &sessionLeases, &attemptLeases,
	); err != nil {
		t.Fatal(err)
	}
	active := activeRunID != nil && *activeRunID == runID
	if status != wantStatus || version != wantVersion || nextSequence != wantNextSequence || active != wantActive ||
		sessionLeases != wantSessionLeases || attemptLeases != wantAttemptLeases {
		t.Fatalf("cancellation aggregate = status=%s version=%d next=%d active=%v sessionLeases=%d attemptLeases=%d", status, version, nextSequence, active, sessionLeases, attemptLeases)
	}
}

func assertCancellationTransition(
	t *testing.T,
	pool *pgxpool.Pool,
	schema string,
	record TransitionRecord,
	runID string,
	attempt *RunAttempt,
	kind string,
	wantSequence int64,
) {
	t.Helper()
	query := fmt.Sprintf(`
SELECT e.seq, e.run_attempt_id::text, e.run_attempt_generation, e.kind, e.payload,
       o.kind, o.aggregate_id::text, o.payload
FROM %s.run_events AS e
JOIN %s.outbox AS o ON o.id = $2
WHERE e.event_id = $1 AND e.run_id = $3`, quoteIdentifier(schema), quoteIdentifier(schema))
	var sequence int64
	var attemptID *string
	var generation *int64
	var eventKind, outboxKind, aggregateID string
	var eventPayload, outboxPayload []byte
	if err := pool.QueryRow(t.Context(), query, record.EventID, record.OutboxID, runID).Scan(
		&sequence, &attemptID, &generation, &eventKind, &eventPayload,
		&outboxKind, &aggregateID, &outboxPayload,
	); err != nil {
		t.Fatal(err)
	}
	if sequence != wantSequence || eventKind != kind || outboxKind != kind || aggregateID != runID ||
		string(eventPayload) != string(outboxPayload) {
		t.Fatalf("cancellation transition = seq=%d eventKind=%s outboxKind=%s aggregate=%s event=%s outbox=%s", sequence, eventKind, outboxKind, aggregateID, eventPayload, outboxPayload)
	}
	if attempt == nil {
		if attemptID != nil || generation != nil {
			t.Fatalf("queued cancellation unexpectedly scoped to attempt %v/%v", attemptID, generation)
		}
	} else if attemptID == nil || *attemptID != attempt.ID || generation == nil || *generation != attempt.Generation {
		t.Fatalf("running cancellation attempt scope = %v/%v, want %s/%d", attemptID, generation, attempt.ID, attempt.Generation)
	}
	var payload struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(eventPayload, &payload); err != nil || payload.Code != cancelCodeUser || payload.Message != cancelMessage {
		t.Fatalf("cancellation payload = %+v, %v", payload, err)
	}
}

func assertAttemptAbandonmentTransition(
	t *testing.T,
	pool *pgxpool.Pool,
	schema string,
	record TransitionRecord,
	runID string,
	attempt RunAttempt,
	wantKind, wantCode, wantMessage string,
	wantSequence int64,
) {
	t.Helper()
	query := fmt.Sprintf(`
SELECT e.seq, e.run_attempt_id::text, e.run_attempt_generation, e.kind, e.payload,
       o.kind, o.aggregate_id::text, o.payload
FROM %s.run_events AS e
JOIN %s.outbox AS o ON o.id = $2
WHERE e.event_id = $1 AND e.run_id = $3`, quoteIdentifier(schema), quoteIdentifier(schema))
	var sequence int64
	var attemptID *string
	var generation *int64
	var eventKind, outboxKind, aggregateID string
	var eventPayload, outboxPayload []byte
	if err := pool.QueryRow(t.Context(), query, record.EventID, record.OutboxID, runID).Scan(
		&sequence, &attemptID, &generation, &eventKind, &eventPayload,
		&outboxKind, &aggregateID, &outboxPayload,
	); err != nil {
		t.Fatal(err)
	}
	if sequence != wantSequence || attemptID == nil || *attemptID != attempt.ID || generation == nil || *generation != attempt.Generation ||
		eventKind != wantKind || outboxKind != wantKind || aggregateID != runID || string(eventPayload) != string(outboxPayload) {
		t.Fatalf("attempt abandonment transition = seq=%d scope=%v/%v eventKind=%s outboxKind=%s aggregate=%s event=%s outbox=%s", sequence, attemptID, generation, eventKind, outboxKind, aggregateID, eventPayload, outboxPayload)
	}
	var payload struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(eventPayload, &payload); err != nil || payload.Code != wantCode || payload.Message != wantMessage {
		t.Fatalf("attempt abandonment payload = %+v, %v", payload, err)
	}
}
