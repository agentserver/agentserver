package coredb

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgreSQLExecutorGatewayRecoveryFencesAndReconcilesEveryCrashWindow(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	running := startExecutionTestRun(t, store, pool, schema, 800_000)

	ambiguousPrepare := executionTestPrepareCommand(t, 801_000, running, "gateway-recovery-ambiguous", 2)
	ambiguousExecution, err := store.PrepareExecution(t.Context(), ambiguousPrepare)
	if err != nil {
		t.Fatal(err)
	}
	process, err := store.PrepareOperation(t.Context(), executionTestPrepareOperationCommand(t, 801_100, running, ambiguousExecution.Execution, 1))
	if err != nil {
		t.Fatal(err)
	}
	timeoutCommand := executionTestPrepareOperationCommand(t, 801_200, running, process.Execution, 2)
	timeoutCommand.Kind = OperationKindTimeoutTerminate
	timeout, err := store.PrepareOperation(t.Context(), timeoutCommand)
	if err != nil {
		t.Fatal(err)
	}
	installExecutionTestConnection(t, pool, schema, running, ambiguousExecution.Execution, 41, 810_000)
	beginAmbiguous := executionTestBeginCommand(t, 801_300, running, process, 41)
	beginAmbiguous.ExpectedExecutionVersion = timeout.Execution.Version
	dispatching, err := store.BeginOperationDispatch(t.Context(), beginAmbiguous)
	if err != nil {
		t.Fatal(err)
	}
	acknowledged, err := store.AcknowledgeOperation(t.Context(), AcknowledgeOperationCommand{
		OperationID: dispatching.Operation.ID, ExecutionID: dispatching.Execution.ID,
		RunID: running.Run.ID, AttemptID: running.Attempt.ID, Generation: running.Attempt.Generation,
		ConnectionGeneration: 41, ExpectedExecutionVersion: dispatching.Execution.Version,
		ExpectedOperationVersion: dispatching.Operation.Version,
		AcknowledgementHash:      executionTestHash(t, HashDomainOperationAck, 801_400),
		Record:                   stateTransitionRecord(801_410),
	})
	if err != nil {
		t.Fatal(err)
	}

	terminalPrepare := executionTestPrepareCommand(t, 802_000, running, "gateway-recovery-terminal-evidence", 1)
	terminalPrepare.ExecutorID = ambiguousExecution.Execution.ExecutorID
	terminalPrepare.EnvID = ambiguousExecution.Execution.EnvID
	terminalExecution, err := store.PrepareExecution(t.Context(), terminalPrepare)
	if err != nil {
		t.Fatal(err)
	}
	terminalOperation, err := store.PrepareOperation(t.Context(), executionTestPrepareOperationCommand(t, 802_100, running, terminalExecution.Execution, 1))
	if err != nil {
		t.Fatal(err)
	}
	terminalDispatch, err := store.BeginOperationDispatch(t.Context(), executionTestBeginCommand(t, 802_200, running, terminalOperation, 41))
	if err != nil {
		t.Fatal(err)
	}
	terminalAck, err := store.AcknowledgeOperation(t.Context(), AcknowledgeOperationCommand{
		OperationID: terminalDispatch.Operation.ID, ExecutionID: terminalDispatch.Execution.ID,
		RunID: running.Run.ID, AttemptID: running.Attempt.ID, Generation: running.Attempt.Generation,
		ConnectionGeneration: 41, ExpectedExecutionVersion: terminalDispatch.Execution.Version,
		ExpectedOperationVersion: terminalDispatch.Operation.Version,
		AcknowledgementHash:      executionTestHash(t, HashDomainOperationAck, 802_300),
		Record:                   stateTransitionRecord(802_310),
	})
	if err != nil {
		t.Fatal(err)
	}
	terminalOperationResult, err := store.CompleteOperation(t.Context(), CompleteOperationCommand{
		OperationID: terminalAck.Operation.ID, ExecutionID: terminalAck.Execution.ID,
		RunID: running.Run.ID, AttemptID: running.Attempt.ID, Generation: running.Attempt.Generation,
		ConnectionGeneration: 41, ExpectedExecutionVersion: terminalAck.Execution.Version,
		ExpectedOperationVersion: terminalAck.Operation.Version,
		TerminalStatus:           OperationStatusSucceeded,
		ResultHash:               executionTestHash(t, HashDomainOperationResult, 802_400),
		Record:                   stateTransitionRecord(802_410),
	})
	if err != nil {
		t.Fatal(err)
	}

	preparedOnlyCommand := executionTestPrepareCommand(t, 803_000, running, "gateway-recovery-never-sent", 1)
	preparedOnlyCommand.ExecutorID = ambiguousExecution.Execution.ExecutorID
	preparedOnlyCommand.EnvID = ambiguousExecution.Execution.EnvID
	preparedOnlyExecution, err := store.PrepareExecution(t.Context(), preparedOnlyCommand)
	if err != nil {
		t.Fatal(err)
	}
	preparedOnlyOperation, err := store.PrepareOperation(t.Context(), executionTestPrepareOperationCommand(t, 803_100, running, preparedOnlyExecution.Execution, 1))
	if err != nil {
		t.Fatal(err)
	}

	recoveringGatewayID := stateTestUUID(820_000)
	recovery, err := store.RecoverExecutorGateway(t.Context(), RecoverExecutorGatewayCommand{
		ExecutorID: ambiguousExecution.Execution.ExecutorID, GatewayInstanceID: recoveringGatewayID,
		Records: gatewayRecoveryTestRecords(recoveringGatewayID, 821_000, 4, 1),
	})
	if err != nil {
		t.Fatal(err)
	}
	if recovery.FencedConnectionGeneration != 41 || !recovery.ConnectionFenced || recovery.RecoveredExecutions != 2 || recovery.Remaining {
		t.Fatalf("RecoverExecutorGateway() = %+v", recovery)
	}

	assertGatewayRecoveryExecutionStatus(t, pool, schema, acknowledged.Execution.ID, ExecutionStatusUnknown)
	assertGatewayRecoveryOperationStatus(t, pool, schema, acknowledged.Operation.ID, OperationStatusUnknown)
	assertGatewayRecoveryOperationStatus(t, pool, schema, timeout.Operation.ID, OperationStatusSkipped)
	assertGatewayRecoveryExecutionStatus(t, pool, schema, terminalOperationResult.Execution.ID, ExecutionStatusSucceeded)
	assertGatewayRecoveryOperationStatus(t, pool, schema, terminalOperationResult.Operation.ID, OperationStatusSucceeded)
	assertGatewayRecoveryExecutionStatus(t, pool, schema, preparedOnlyExecution.Execution.ID, ExecutionStatusApproved)
	assertGatewayRecoveryOperationStatus(t, pool, schema, preparedOnlyOperation.Operation.ID, OperationStatusPrepared)
	assertExecutorRuntimeStatus(
		t, pool, schema, ambiguousExecution.Execution.ExecutorID, ambiguousExecution.Execution.EnvID,
		ExecutorStatusOffline, ExecutorEnvironmentStatusOffline,
	)
	assertGatewayRecoveryConnectionFenced(t, pool, schema, ambiguousExecution.Execution.ExecutorID, 41)
	assertGatewayRecoveryEvents(t, pool, schema, running.Run.ID, recoveringGatewayID)

	latePreparedBegin := executionTestBeginCommand(t, 803_200, running, preparedOnlyOperation, 41)
	if _, err := store.BeginOperationDispatch(t.Context(), latePreparedBegin); !HasStateErrorCode(err, ErrorConnectionFenced) {
		t.Fatalf("old-generation prepared BeginOperationDispatch() error = %v, want connection_fenced", err)
	}
	lateTerminal := CompleteOperationCommand{
		OperationID: acknowledged.Operation.ID, ExecutionID: acknowledged.Execution.ID,
		RunID: running.Run.ID, AttemptID: running.Attempt.ID, Generation: running.Attempt.Generation,
		ConnectionGeneration: 41, ExpectedExecutionVersion: acknowledged.Execution.Version,
		ExpectedOperationVersion: acknowledged.Operation.Version,
		TerminalStatus:           OperationStatusSucceeded,
		ResultHash:               executionTestHash(t, HashDomainOperationResult, 803_300),
		Record:                   stateTransitionRecord(803_310),
	}
	if _, err := store.CompleteOperation(t.Context(), lateTerminal); !HasStateErrorCode(err, ErrorIdempotencyConflict) {
		t.Fatalf("late success evidence error = %v, want idempotency_conflict", err)
	}

	retry, err := store.RecoverExecutorGateway(t.Context(), RecoverExecutorGatewayCommand{
		ExecutorID: ambiguousExecution.Execution.ExecutorID, GatewayInstanceID: recoveringGatewayID,
		Records: gatewayRecoveryTestRecords(recoveringGatewayID, 822_000, 2, 5),
	})
	if err != nil || retry.ConnectionFenced || retry.RecoveredExecutions != 0 || retry.Remaining || retry.FencedConnectionGeneration != 41 {
		t.Fatalf("exact startup recovery retry = %+v, %v", retry, err)
	}
}

func TestPostgreSQLExecutorGatewayRecoveryCommitsFenceBeforeFailClosedPlanError(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	running := startExecutionTestRun(t, store, pool, schema, 830_000)
	prepared, err := store.PrepareExecution(t.Context(), executionTestPrepareCommand(t, 831_000, running, "gateway-recovery-required-followup", 2))
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.PrepareOperation(t.Context(), executionTestPrepareOperationCommand(t, 831_100, running, prepared.Execution, 1))
	if err != nil {
		t.Fatal(err)
	}
	requiredCommand := executionTestPrepareOperationCommand(t, 831_200, running, first.Execution, 2)
	requiredCommand.Kind = "required_followup"
	required, err := store.PrepareOperation(t.Context(), requiredCommand)
	if err != nil {
		t.Fatal(err)
	}
	installExecutionTestConnection(t, pool, schema, running, prepared.Execution, 51, 840_000)
	begin := executionTestBeginCommand(t, 831_300, running, first, 51)
	begin.ExpectedExecutionVersion = required.Execution.Version
	dispatching, err := store.BeginOperationDispatch(t.Context(), begin)
	if err != nil {
		t.Fatal(err)
	}

	recoveringGatewayID := stateTestUUID(841_000)
	_, err = store.RecoverExecutorGateway(t.Context(), RecoverExecutorGatewayCommand{
		ExecutorID: prepared.Execution.ExecutorID, GatewayInstanceID: recoveringGatewayID,
		Records: gatewayRecoveryTestRecords(recoveringGatewayID, 842_000, 2, 1),
	})
	if !HasStateErrorCode(err, ErrorInvalidState) {
		t.Fatalf("RecoverExecutorGateway(required prepared) error = %v, want invalid_state", err)
	}
	assertGatewayRecoveryConnectionFenced(t, pool, schema, prepared.Execution.ExecutorID, 51)
	assertExecutorRuntimeStatus(
		t, pool, schema, prepared.Execution.ExecutorID, prepared.Execution.EnvID,
		ExecutorStatusOffline, ExecutorEnvironmentStatusOffline,
	)
	assertGatewayRecoveryExecutionStatus(t, pool, schema, dispatching.Execution.ID, ExecutionStatusDispatching)
	assertGatewayRecoveryOperationStatus(t, pool, schema, dispatching.Operation.ID, OperationStatusDispatching)
	assertGatewayRecoveryOperationStatus(t, pool, schema, required.Operation.ID, OperationStatusPrepared)
	query := fmt.Sprintf("SELECT pg_catalog.count(*) FROM %s.run_events WHERE producer_instance_id = $1", quoteIdentifier(schema))
	var eventCount int
	if err := pool.QueryRow(t.Context(), query, recoveringGatewayID).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 0 {
		t.Fatalf("failed recovery committed %d canonical events, want 0", eventCount)
	}
}

func gatewayRecoveryTestRecords(gatewayID string, seed, count int, firstSequence int64) []TransitionRecord {
	records := make([]TransitionRecord, count)
	for index := range records {
		record := stateTransitionRecord(seed + index*10)
		record.ProducerInstanceID = gatewayID
		record.ProducerSeq = firstSequence + int64(index)
		records[index] = record
	}
	return records
}

func assertGatewayRecoveryExecutionStatus(t *testing.T, pool *pgxpool.Pool, schema, executionID, want string) {
	t.Helper()
	query := fmt.Sprintf("SELECT status FROM %s.executions WHERE id = $1", quoteIdentifier(schema))
	var status string
	if err := pool.QueryRow(t.Context(), query, executionID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != want {
		t.Fatalf("execution %s status = %q, want %q", executionID, status, want)
	}
}

func assertGatewayRecoveryOperationStatus(t *testing.T, pool *pgxpool.Pool, schema, operationID, want string) {
	t.Helper()
	query := fmt.Sprintf("SELECT status FROM %s.execution_operations WHERE id = $1", quoteIdentifier(schema))
	var status string
	if err := pool.QueryRow(t.Context(), query, operationID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != want {
		t.Fatalf("operation %s status = %q, want %q", operationID, status, want)
	}
}

func assertGatewayRecoveryConnectionFenced(t *testing.T, pool *pgxpool.Pool, schema, executorID string, generation int64) {
	t.Helper()
	query := fmt.Sprintf(`
SELECT status, generation, expires_at <= pg_catalog.clock_timestamp()
FROM %s.executor_connections
WHERE executor_id = $1`, quoteIdentifier(schema))
	var status string
	var gotGeneration int64
	var expired bool
	if err := pool.QueryRow(t.Context(), query, executorID).Scan(&status, &gotGeneration, &expired); err != nil {
		t.Fatal(err)
	}
	if status != ExecutorConnectionStatusFenced || gotGeneration != generation || !expired {
		t.Fatalf("executor connection = status %q generation %d expired %t", status, gotGeneration, expired)
	}
}

func assertGatewayRecoveryEvents(t *testing.T, pool *pgxpool.Pool, schema, runID, producerID string) {
	t.Helper()
	query := fmt.Sprintf(`
SELECT kind, payload::text
FROM %s.run_events
WHERE run_id = $1 AND producer_instance_id = $2
ORDER BY producer_seq`, quoteIdentifier(schema))
	rows, err := pool.Query(t.Context(), query, runID, producerID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var kinds []string
	for rows.Next() {
		var kind string
		var raw []byte
		if err := rows.Scan(&kind, &raw); err != nil {
			t.Fatal(err)
		}
		var payload struct {
			Version                    string                           `json:"version"`
			Reason                     string                           `json:"reason"`
			FencedConnectionGeneration int64                            `json:"fencedConnectionGeneration"`
			OperationChanges           []gatewayRecoveryOperationChange `json:"operationChanges"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Version != "executor-gateway-recovery-v1" || payload.Reason != GatewayRecoveryReasonProcessRestart || payload.FencedConnectionGeneration != 41 {
			t.Fatalf("gateway recovery event payload = %+v", payload)
		}
		kinds = append(kinds, kind)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(kinds) != 2 || kinds[0] != "execution.unknown" || kinds[1] != "execution.succeeded" {
		t.Fatalf("gateway recovery event kinds = %v", kinds)
	}
	query = fmt.Sprintf("SELECT pg_catalog.count(*) FROM %s.outbox WHERE kind IN ('execution.unknown', 'execution.succeeded') AND aggregate_id = $1", quoteIdentifier(schema))
	var outboxCount int
	if err := pool.QueryRow(t.Context(), query, runID).Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if outboxCount != 2 {
		t.Fatalf("gateway recovery outbox count = %d, want 2", outboxCount)
	}
}
