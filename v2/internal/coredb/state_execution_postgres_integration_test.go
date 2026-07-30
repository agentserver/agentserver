package coredb

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgreSQLExecutionCrashAfterDispatchCommitDoesNotAuthorizeReplay(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	running := startExecutionTestRun(t, store, pool, schema, 10_000)

	prepareExecution := executionTestPrepareCommand(t, 11_000, running, "tool-call-crash", 1)
	preparedExecution, err := store.PrepareExecution(t.Context(), prepareExecution)
	if err != nil || !preparedExecution.Created || preparedExecution.Execution.Status != ExecutionStatusApproved {
		t.Fatalf("PrepareExecution() = %+v, error = %v", preparedExecution, err)
	}
	prepareOperation := executionTestPrepareOperationCommand(t, 12_000, running, preparedExecution.Execution, 1)
	preparedOperation, err := store.PrepareOperation(t.Context(), prepareOperation)
	if err != nil || !preparedOperation.Created || preparedOperation.Operation.Status != OperationStatusPrepared {
		t.Fatalf("PrepareOperation() = %+v, error = %v", preparedOperation, err)
	}

	// Force failure at the final outbox insert. The operation transition, the
	// execution version, the run sequence, and event insert must all roll back.
	failedBegin := executionTestBeginCommand(t, 13_000, running, preparedOperation, 7)
	preinsertOutbox(t, pool, schema, failedBegin.Record.OutboxID, running.Run.ID)
	if _, err := store.BeginOperationDispatch(t.Context(), failedBegin); !HasStateErrorCode(err, ErrorConflict) {
		t.Fatalf("BeginOperationDispatch() with outbox conflict error = %v, want conflict", err)
	}

	begin := executionTestBeginCommand(t, 13_100, running, preparedOperation, 7)
	dispatching, err := store.BeginOperationDispatch(t.Context(), begin)
	if err != nil || !dispatching.Began || dispatching.Operation.Status != OperationStatusDispatching || dispatching.Execution.Status != ExecutionStatusDispatching {
		t.Fatalf("BeginOperationDispatch() = %+v, error = %v", dispatching, err)
	}

	// Simulate losing the response after commit. The same command returns the
	// committed boundary but never grants a second external send.
	retry, err := store.BeginOperationDispatch(t.Context(), begin)
	if err != nil || retry.Began || retry.Operation.Version != dispatching.Operation.Version {
		t.Fatalf("retry BeginOperationDispatch() = %+v, error = %v", retry, err)
	}
	preparedRetry, err := store.PrepareExecution(t.Context(), prepareExecution)
	if err != nil || preparedRetry.Created || preparedRetry.Execution.Status != ExecutionStatusDispatching {
		t.Fatalf("post-dispatch PrepareExecution() retry = %+v, error = %v", preparedRetry, err)
	}
	operationRetry, err := store.PrepareOperation(t.Context(), prepareOperation)
	if err != nil || operationRetry.Created || operationRetry.Operation.Status != OperationStatusDispatching {
		t.Fatalf("post-dispatch PrepareOperation() retry = %+v, error = %v", operationRetry, err)
	}

	unknownOperation := CompleteOperationCommand{
		OperationID:              dispatching.Operation.ID,
		ExecutionID:              dispatching.Execution.ID,
		RunID:                    running.Run.ID,
		AttemptID:                running.Attempt.ID,
		Generation:               running.Attempt.Generation,
		ConnectionGeneration:     7,
		ExpectedExecutionVersion: dispatching.Execution.Version,
		ExpectedOperationVersion: dispatching.Operation.Version,
		TerminalStatus:           OperationStatusUnknown,
		ResultHash:               executionTestHash(t, HashDomainOperationResult, 13_200),
		Record:                   stateTransitionRecord(13_210),
	}
	closedOperation, err := store.CompleteOperation(t.Context(), unknownOperation)
	if err != nil || !closedOperation.Changed || closedOperation.Operation.Status != OperationStatusUnknown || closedOperation.Operation.AcknowledgementHash != nil {
		t.Fatalf("CompleteOperation(unknown) = %+v, error = %v", closedOperation, err)
	}
	closedOperationRetry, err := store.CompleteOperation(t.Context(), unknownOperation)
	if err != nil || closedOperationRetry.Changed {
		t.Fatalf("retry CompleteOperation(unknown) = %+v, error = %v", closedOperationRetry, err)
	}

	unknownExecution := CompleteExecutionCommand{
		ExecutionID:              closedOperation.Execution.ID,
		RunID:                    running.Run.ID,
		AttemptID:                running.Attempt.ID,
		Generation:               running.Attempt.Generation,
		ExpectedExecutionVersion: closedOperation.Execution.Version,
		TerminalStatus:           ExecutionStatusUnknown,
		ResultHash:               executionTestHash(t, HashDomainExecutionResult, 13_300),
		Record:                   stateTransitionRecord(13_310),
	}
	closedExecution, err := store.CompleteExecution(t.Context(), unknownExecution)
	if err != nil || !closedExecution.Changed || closedExecution.Execution.Status != ExecutionStatusUnknown {
		t.Fatalf("CompleteExecution(unknown) = %+v, error = %v", closedExecution, err)
	}
	closedExecutionRetry, err := store.CompleteExecution(t.Context(), unknownExecution)
	if err != nil || closedExecutionRetry.Changed {
		t.Fatalf("retry CompleteExecution(unknown) = %+v, error = %v", closedExecutionRetry, err)
	}
	assertStateTableCount(t, pool, schema, "executions", 1)
	assertStateTableCount(t, pool, schema, "execution_operations", 1)
}

func TestPostgreSQLExecutionAcknowledgedTerminalStateMachine(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	running := startExecutionTestRun(t, store, pool, schema, 20_000)
	preparedExecution, err := store.PrepareExecution(t.Context(), executionTestPrepareCommand(t, 21_000, running, "tool-call-success", 1))
	if err != nil {
		t.Fatal(err)
	}
	preparedOperation, err := store.PrepareOperation(t.Context(), executionTestPrepareOperationCommand(t, 22_000, running, preparedExecution.Execution, 1))
	if err != nil {
		t.Fatal(err)
	}
	dispatching, err := store.BeginOperationDispatch(t.Context(), executionTestBeginCommand(t, 23_000, running, preparedOperation, 11))
	if err != nil {
		t.Fatal(err)
	}

	notAcknowledged := CompleteOperationCommand{
		OperationID:              dispatching.Operation.ID,
		ExecutionID:              dispatching.Execution.ID,
		RunID:                    running.Run.ID,
		AttemptID:                running.Attempt.ID,
		Generation:               running.Attempt.Generation,
		ConnectionGeneration:     11,
		ExpectedExecutionVersion: dispatching.Execution.Version,
		ExpectedOperationVersion: dispatching.Operation.Version,
		TerminalStatus:           OperationStatusSucceeded,
		ResultHash:               executionTestHash(t, HashDomainOperationResult, 23_100),
		Record:                   stateTransitionRecord(23_110),
	}
	if _, err := store.CompleteOperation(t.Context(), notAcknowledged); !HasStateErrorCode(err, ErrorInvalidState) {
		t.Fatalf("pre-ACK CompleteOperation() error = %v, want invalid_state", err)
	}

	acknowledgement := AcknowledgeOperationCommand{
		OperationID:              dispatching.Operation.ID,
		ExecutionID:              dispatching.Execution.ID,
		RunID:                    running.Run.ID,
		AttemptID:                running.Attempt.ID,
		Generation:               running.Attempt.Generation,
		ConnectionGeneration:     11,
		ExpectedExecutionVersion: dispatching.Execution.Version,
		ExpectedOperationVersion: dispatching.Operation.Version,
		AcknowledgementHash:      executionTestHash(t, HashDomainOperationAck, 23_200),
		Record:                   stateTransitionRecord(23_210),
	}
	fencedAck := acknowledgement
	fencedAck.ConnectionGeneration = 12
	if _, err := store.AcknowledgeOperation(t.Context(), fencedAck); !HasStateErrorCode(err, ErrorConnectionFenced) {
		t.Fatalf("wrong-generation AcknowledgeOperation() error = %v, want connection_fenced", err)
	}
	acknowledged, err := store.AcknowledgeOperation(t.Context(), acknowledgement)
	if err != nil || !acknowledged.Changed || acknowledged.Operation.Status != OperationStatusAcknowledged || acknowledged.Execution.Status != ExecutionStatusRunning {
		t.Fatalf("AcknowledgeOperation() = %+v, error = %v", acknowledged, err)
	}
	ackRetry, err := store.AcknowledgeOperation(t.Context(), acknowledgement)
	if err != nil || ackRetry.Changed {
		t.Fatalf("retry AcknowledgeOperation() = %+v, error = %v", ackRetry, err)
	}

	prematureExecution := CompleteExecutionCommand{
		ExecutionID:              acknowledged.Execution.ID,
		RunID:                    running.Run.ID,
		AttemptID:                running.Attempt.ID,
		Generation:               running.Attempt.Generation,
		ExpectedExecutionVersion: acknowledged.Execution.Version,
		TerminalStatus:           ExecutionStatusSucceeded,
		ResultHash:               executionTestHash(t, HashDomainExecutionResult, 23_300),
		Record:                   stateTransitionRecord(23_310),
	}
	if _, err := store.CompleteExecution(t.Context(), prematureExecution); !HasStateErrorCode(err, ErrorInvalidState) {
		t.Fatalf("premature CompleteExecution() error = %v, want invalid_state", err)
	}

	completeOperation := notAcknowledged
	completeOperation.ExpectedExecutionVersion = acknowledged.Execution.Version
	completeOperation.ExpectedOperationVersion = acknowledged.Operation.Version
	completeOperation.Record = stateTransitionRecord(23_400)
	preinsertOutbox(t, pool, schema, completeOperation.Record.OutboxID, running.Run.ID)
	if _, err := store.CompleteOperation(t.Context(), completeOperation); !HasStateErrorCode(err, ErrorConflict) {
		t.Fatalf("CompleteOperation() with outbox conflict error = %v, want conflict", err)
	}
	completeOperation.Record = stateTransitionRecord(23_450)
	completedOperation, err := store.CompleteOperation(t.Context(), completeOperation)
	if err != nil || !completedOperation.Changed || completedOperation.Operation.Status != OperationStatusSucceeded {
		t.Fatalf("CompleteOperation(succeeded) = %+v, error = %v", completedOperation, err)
	}
	completeExecution := prematureExecution
	completeExecution.ExpectedExecutionVersion = completedOperation.Execution.Version
	completeExecution.Record = stateTransitionRecord(23_500)
	completedExecution, err := store.CompleteExecution(t.Context(), completeExecution)
	if err != nil || !completedExecution.Changed || completedExecution.Execution.Status != ExecutionStatusSucceeded {
		t.Fatalf("CompleteExecution(succeeded) = %+v, error = %v", completedExecution, err)
	}

	conflictingRetry := completeExecution
	conflictingRetry.TerminalStatus = ExecutionStatusFailed
	conflictingRetry.ResultHash = executionTestHash(t, HashDomainExecutionResult, 23_600)
	if _, err := store.CompleteExecution(t.Context(), conflictingRetry); !HasStateErrorCode(err, ErrorIdempotencyConflict) {
		t.Fatalf("conflicting CompleteExecution() error = %v, want idempotency_conflict", err)
	}
}

func TestPostgreSQLExecutionSkipsTrailingTimeoutAfterProcessTerminal(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	running := startExecutionTestRun(t, store, pool, schema, 25_000)
	preparedExecution, err := store.PrepareExecution(t.Context(), executionTestPrepareCommand(t, 25_100, running, "tool-call-timeout-skip", 2))
	if err != nil {
		t.Fatal(err)
	}
	process, err := store.PrepareOperation(t.Context(), executionTestPrepareOperationCommand(t, 25_200, running, preparedExecution.Execution, 1))
	if err != nil {
		t.Fatal(err)
	}
	timeoutCommand := executionTestPrepareOperationCommand(t, 25_300, running, process.Execution, 2)
	timeoutCommand.Kind = OperationKindTimeoutTerminate
	timeout, err := store.PrepareOperation(t.Context(), timeoutCommand)
	if err != nil {
		t.Fatal(err)
	}

	beginCommand := executionTestBeginCommand(t, 25_400, running, process, 12)
	beginCommand.ExpectedExecutionVersion = timeout.Execution.Version
	dispatching, err := store.BeginOperationDispatch(t.Context(), beginCommand)
	if err != nil {
		t.Fatal(err)
	}
	prematureSkip := SkipOperationCommand{
		OperationID:              timeout.Operation.ID,
		ExecutionID:              timeout.Execution.ID,
		RunID:                    running.Run.ID,
		AttemptID:                running.Attempt.ID,
		HolderID:                 running.Attempt.HolderID,
		Generation:               running.Attempt.Generation,
		ExpectedExecutionVersion: dispatching.Execution.Version,
		ExpectedOperationVersion: timeout.Operation.Version,
		ResultHash:               executionTestHash(t, HashDomainOperationResult, 25_410),
		Record:                   stateTransitionRecord(25_420),
	}
	if _, err := store.SkipOperation(t.Context(), prematureSkip); !HasStateErrorCode(err, ErrorInvalidState) {
		t.Fatalf("pre-terminal SkipOperation() error = %v, want invalid_state", err)
	}

	acknowledged, err := store.AcknowledgeOperation(t.Context(), AcknowledgeOperationCommand{
		OperationID:              dispatching.Operation.ID,
		ExecutionID:              dispatching.Execution.ID,
		RunID:                    running.Run.ID,
		AttemptID:                running.Attempt.ID,
		Generation:               running.Attempt.Generation,
		ConnectionGeneration:     12,
		ExpectedExecutionVersion: dispatching.Execution.Version,
		ExpectedOperationVersion: dispatching.Operation.Version,
		AcknowledgementHash:      executionTestHash(t, HashDomainOperationAck, 25_500),
		Record:                   stateTransitionRecord(25_510),
	})
	if err != nil {
		t.Fatal(err)
	}
	completedProcess, err := store.CompleteOperation(t.Context(), CompleteOperationCommand{
		OperationID:              acknowledged.Operation.ID,
		ExecutionID:              acknowledged.Execution.ID,
		RunID:                    running.Run.ID,
		AttemptID:                running.Attempt.ID,
		Generation:               running.Attempt.Generation,
		ConnectionGeneration:     12,
		ExpectedExecutionVersion: acknowledged.Execution.Version,
		ExpectedOperationVersion: acknowledged.Operation.Version,
		TerminalStatus:           OperationStatusSucceeded,
		ResultHash:               executionTestHash(t, HashDomainOperationResult, 25_600),
		Record:                   stateTransitionRecord(25_610),
	})
	if err != nil {
		t.Fatal(err)
	}

	skip := prematureSkip
	skip.ExpectedExecutionVersion = completedProcess.Execution.Version
	skip.Record = stateTransitionRecord(25_700)
	preinsertOutbox(t, pool, schema, skip.Record.OutboxID, running.Run.ID)
	if _, err := store.SkipOperation(t.Context(), skip); !HasStateErrorCode(err, ErrorConflict) {
		t.Fatalf("SkipOperation() with outbox conflict error = %v, want conflict", err)
	}
	skip.Record = stateTransitionRecord(25_750)
	skipped, err := store.SkipOperation(t.Context(), skip)
	if err != nil || !skipped.Changed || skipped.Operation.Status != OperationStatusSkipped || skipped.Operation.ConnectionGeneration != 0 ||
		skipped.Operation.DispatchedAt != nil || skipped.Operation.AcknowledgementHash != nil || skipped.Operation.TerminalResultHash == nil || skipped.Operation.TerminalAt == nil {
		t.Fatalf("SkipOperation() = %+v, error = %v", skipped, err)
	}
	retry, err := store.SkipOperation(t.Context(), skip)
	if err != nil || retry.Changed {
		t.Fatalf("retry SkipOperation() = %+v, error = %v", retry, err)
	}
	lateTimeoutBegin := executionTestBeginCommand(t, 25_775, running, timeout, 12)
	lateTimeoutBegin.ExpectedExecutionVersion = skipped.Execution.Version
	lateTimeoutBegin.ExpectedOperationVersion = skipped.Operation.Version
	lateBeginResult, err := store.BeginOperationDispatch(t.Context(), lateTimeoutBegin)
	if err != nil || lateBeginResult.Began || lateBeginResult.Operation.Status != OperationStatusSkipped {
		t.Fatalf("BeginOperationDispatch() after skip = %+v, error = %v", lateBeginResult, err)
	}

	completedExecution, err := store.CompleteExecution(t.Context(), CompleteExecutionCommand{
		ExecutionID:              skipped.Execution.ID,
		RunID:                    running.Run.ID,
		AttemptID:                running.Attempt.ID,
		Generation:               running.Attempt.Generation,
		ExpectedExecutionVersion: skipped.Execution.Version,
		TerminalStatus:           ExecutionStatusSucceeded,
		ResultHash:               executionTestHash(t, HashDomainExecutionResult, 25_800),
		Record:                   stateTransitionRecord(25_810),
	})
	if err != nil || completedExecution.Execution.Status != ExecutionStatusSucceeded {
		t.Fatalf("CompleteExecution() = %+v, error = %v", completedExecution, err)
	}
}

func TestPostgreSQLExecutionFingerprintLeaseAndGenerationFencing(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	running := startExecutionTestRun(t, store, pool, schema, 30_000)
	prepare := executionTestPrepareCommand(t, 31_000, running, "tool-call-fence", 1)
	preparedExecution, err := store.PrepareExecution(t.Context(), prepare)
	if err != nil {
		t.Fatal(err)
	}

	conflicting := prepare
	conflicting.ArgumentsHash = executionTestHash(t, HashDomainExecutionArguments, 31_999)
	if _, err := store.PrepareExecution(t.Context(), conflicting); !HasStateErrorCode(err, ErrorIdempotencyConflict) {
		t.Fatalf("conflicting PrepareExecution() error = %v, want idempotency_conflict", err)
	}
	preparedOperation, err := store.PrepareOperation(t.Context(), executionTestPrepareOperationCommand(t, 32_000, running, preparedExecution.Execution, 1))
	if err != nil {
		t.Fatal(err)
	}

	expireStateLeases(t, pool, schema, running.Run.SessionID, running.Attempt.ID)
	exactRetry, err := store.PrepareExecution(t.Context(), prepare)
	if err != nil || exactRetry.Created {
		t.Fatalf("expired-lease exact PrepareExecution() retry = %+v, error = %v", exactRetry, err)
	}
	newCall := executionTestPrepareCommand(t, 33_000, running, "tool-call-after-expiry", 1)
	if _, err := store.PrepareExecution(t.Context(), newCall); !HasStateErrorCode(err, ErrorLeaseLost) {
		t.Fatalf("expired-lease PrepareExecution() error = %v, want lease_lost", err)
	}
	begin := executionTestBeginCommand(t, 34_000, running, preparedOperation, 13)
	if _, err := store.BeginOperationDispatch(t.Context(), begin); !HasStateErrorCode(err, ErrorLeaseLost) {
		t.Fatalf("expired-lease BeginOperationDispatch() error = %v, want lease_lost", err)
	}
	begin.Generation++
	if _, err := store.BeginOperationDispatch(t.Context(), begin); !HasStateErrorCode(err, ErrorLeaseLost) {
		t.Fatalf("stale-generation BeginOperationDispatch() error = %v, want lease_lost", err)
	}
}

func TestPostgreSQLExecutionPolicyDecisionFencesDispatch(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	running := startExecutionTestRun(t, store, pool, schema, 34_100)

	askCommand := executionTestPrepareCommand(t, 34_200, running, "tool-call-ask", 1)
	askCommand.PolicyDecision = PolicyDecisionAsk
	ask, err := store.PrepareExecution(t.Context(), askCommand)
	if err != nil || ask.Execution.Status != ExecutionStatusPendingApproval || ask.Execution.TerminalAt != nil {
		t.Fatalf("PrepareExecution(ask) = %+v, error = %v", ask, err)
	}
	askOperation, err := store.PrepareOperation(t.Context(), executionTestPrepareOperationCommand(t, 34_300, running, ask.Execution, 1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.BeginOperationDispatch(t.Context(), executionTestBeginCommand(t, 34_400, running, askOperation, 15)); !HasStateErrorCode(err, ErrorInvalidState) {
		t.Fatalf("pending-approval BeginOperationDispatch() error = %v, want invalid_state", err)
	}

	denyCommand := executionTestPrepareCommand(t, 34_500, running, "tool-call-deny", 1)
	denyCommand.PolicyDecision = PolicyDecisionDeny
	denied, err := store.PrepareExecution(t.Context(), denyCommand)
	if err != nil || denied.Execution.Status != ExecutionStatusDenied || denied.Execution.TerminalAt == nil || denied.Execution.TerminalResultHash != nil {
		t.Fatalf("PrepareExecution(deny) = %+v, error = %v", denied, err)
	}
	denyRetry, err := store.PrepareExecution(t.Context(), denyCommand)
	if err != nil || denyRetry.Created || denyRetry.Execution.ID != denied.Execution.ID {
		t.Fatalf("retry PrepareExecution(deny) = %+v, error = %v", denyRetry, err)
	}
}

func TestPostgreSQLConcurrentPrepareExecutionUsesRunToolCallIdentity(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	running := startExecutionTestRun(t, store, pool, schema, 35_000)
	first := executionTestPrepareCommand(t, 36_000, running, "tool-call-concurrent", 1)
	second := first
	second.ExecutionID = stateTestUUID(36_500)
	second.Record = stateTransitionRecord(36_510)

	start := make(chan struct{})
	results := make(chan PrepareExecutionResult, 2)
	errorsChannel := make(chan error, 2)
	var waitGroup sync.WaitGroup
	for _, command := range []PrepareExecutionCommand{first, second} {
		command := command
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			result, err := store.PrepareExecution(t.Context(), command)
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
			t.Fatalf("concurrent PrepareExecution() error = %v", err)
		}
	}
	created := 0
	returnedIDs := make(map[string]struct{})
	for result := range results {
		if result.Created {
			created++
		}
		returnedIDs[result.Execution.ID] = struct{}{}
	}
	if created != 1 || len(returnedIDs) != 1 {
		t.Fatalf("concurrent execution results: created = %d, IDs = %v; want one committed identity", created, returnedIDs)
	}
	assertStateTableCount(t, pool, schema, "executions", 1)
}

func TestPostgreSQLBeginDispatchRequiresCompleteFrozenOperationPlan(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	running := startExecutionTestRun(t, store, pool, schema, 37_000)
	preparedExecution, err := store.PrepareExecution(t.Context(), executionTestPrepareCommand(t, 38_000, running, "tool-call-two-operations", 2))
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.PrepareOperation(t.Context(), executionTestPrepareOperationCommand(t, 38_100, running, preparedExecution.Execution, 1))
	if err != nil {
		t.Fatal(err)
	}
	beginFirst := executionTestBeginCommand(t, 38_200, running, first, 17)
	if _, err := store.BeginOperationDispatch(t.Context(), beginFirst); !HasStateErrorCode(err, ErrorInvalidState) {
		t.Fatalf("incomplete-plan BeginOperationDispatch() error = %v, want invalid_state", err)
	}

	secondCommand := executionTestPrepareOperationCommand(t, 38_300, running, first.Execution, 2)
	second, err := store.PrepareOperation(t.Context(), secondCommand)
	if err != nil {
		t.Fatal(err)
	}
	beginFirst.ExpectedExecutionVersion = second.Execution.Version
	dispatching, err := store.BeginOperationDispatch(t.Context(), beginFirst)
	if err != nil || !dispatching.Began {
		t.Fatalf("complete-plan BeginOperationDispatch() = %+v, error = %v", dispatching, err)
	}
	assertStateTableCount(t, pool, schema, "execution_operations", 2)
}

func TestPostgreSQLConcurrentOperationMutationKeyIsGloballyUnique(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	running := startExecutionTestRun(t, store, pool, schema, 40_000)
	firstExecution, err := store.PrepareExecution(t.Context(), executionTestPrepareCommand(t, 41_000, running, "tool-call-mutation-a", 1))
	if err != nil {
		t.Fatal(err)
	}
	secondExecution, err := store.PrepareExecution(t.Context(), executionTestPrepareCommand(t, 42_000, running, "tool-call-mutation-b", 1))
	if err != nil {
		t.Fatal(err)
	}
	first := executionTestPrepareOperationCommand(t, 43_000, running, firstExecution.Execution, 1)
	second := executionTestPrepareOperationCommand(t, 44_000, running, secondExecution.Execution, 1)
	second.MutationKey = first.MutationKey

	start := make(chan struct{})
	results := make(chan PrepareOperationResult, 2)
	errorsChannel := make(chan error, 2)
	var waitGroup sync.WaitGroup
	for _, command := range []PrepareOperationCommand{first, second} {
		command := command
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			result, err := store.PrepareOperation(t.Context(), command)
			results <- result
			errorsChannel <- err
		}()
	}
	close(start)
	waitGroup.Wait()
	close(results)
	close(errorsChannel)

	created := 0
	conflicts := 0
	for result := range results {
		if result.Created {
			created++
		}
	}
	for err := range errorsChannel {
		switch {
		case err == nil:
		case HasStateErrorCode(err, ErrorConflict):
			conflicts++
		default:
			t.Fatalf("concurrent PrepareOperation() error = %v", err)
		}
	}
	if created != 1 || conflicts != 1 {
		t.Fatalf("concurrent mutation results: created = %d, conflicts = %d; want 1 and 1", created, conflicts)
	}
	assertStateTableCount(t, pool, schema, "execution_operations", 1)
}

type executionTestRunningRun struct {
	Run     Run
	Attempt RunAttempt
}

func startExecutionTestRun(t *testing.T, store *StateStore, pool *pgxpool.Pool, schema string, seed int) executionTestRunningRun {
	t.Helper()
	workspaceID := stateTestUUID(seed)
	sessionID := stateTestUUID(seed + 1)
	insertStateTestSession(t, pool, schema, workspaceID, sessionID)
	created := mustCreateStateRun(t, store, stateCreateRunCommand(seed+10, workspaceID, sessionID, fmt.Sprintf("execution-key-%d", seed)))
	claimed := mustClaimStateRun(t, store, stateClaimRunCommand(seed+20, created.Run.ID, created.Run.Version, fmt.Sprintf("execution-holder-%d", seed)))
	accepted, err := store.MarkTurnAccepted(t.Context(), MarkTurnAcceptedCommand{
		RunID:                  created.Run.ID,
		AttemptID:              claimed.Attempt.ID,
		HolderID:               claimed.Attempt.HolderID,
		Generation:             claimed.Attempt.Generation,
		ExpectedRunVersion:     claimed.Run.Version,
		ExpectedAttemptVersion: claimed.Attempt.Version,
		Record:                 stateTransitionRecord(seed + 30),
	})
	if err != nil {
		t.Fatalf("MarkTurnAccepted() error = %v", err)
	}
	return executionTestRunningRun{Run: accepted.Run, Attempt: accepted.Attempt}
}

func executionTestPrepareCommand(t *testing.T, seed int, running executionTestRunningRun, toolCallID string, operationCount int) PrepareExecutionCommand {
	t.Helper()
	return PrepareExecutionCommand{
		ExecutionID:            stateTestUUID(seed),
		RunID:                  running.Run.ID,
		AttemptID:              running.Attempt.ID,
		HolderID:               running.Attempt.HolderID,
		Generation:             running.Attempt.Generation,
		ExpectedRunVersion:     running.Run.Version,
		ExpectedAttemptVersion: running.Attempt.Version,
		AppServerToolCallID:    toolCallID,
		ExecutorID:             stateTestUUID(seed + 1),
		EnvID:                  stateTestUUID(seed + 2),
		ToolName:               "shell",
		ToolVersion:            "executor-contract-v1",
		MapperVersion:          "shell-mapper-v1",
		PolicyVersion:          "policy-v1",
		OperationCount:         operationCount,
		ArgumentsHash:          executionTestHash(t, HashDomainExecutionArguments, seed+100),
		ToolSchemaHash:         executionTestHash(t, HashDomainToolSchema, seed+101),
		OperationPlanHash:      executionTestHash(t, HashDomainOperationPlan, seed+102),
		PolicyContextHash:      executionTestHash(t, HashDomainPolicyContext, seed+103),
		PolicyDecision:         PolicyDecisionAllow,
		Record:                 stateTransitionRecord(seed + 10),
	}
}

func executionTestPrepareOperationCommand(t *testing.T, seed int, running executionTestRunningRun, execution Execution, ordinal int) PrepareOperationCommand {
	t.Helper()
	return PrepareOperationCommand{
		OperationID:              stateTestUUID(seed),
		ExecutionID:              execution.ID,
		RunID:                    running.Run.ID,
		AttemptID:                running.Attempt.ID,
		HolderID:                 running.Attempt.HolderID,
		Generation:               running.Attempt.Generation,
		ExpectedExecutionVersion: execution.Version,
		Ordinal:                  ordinal,
		Kind:                     "process_start",
		EffectClass:              OperationEffectMutation,
		MutationKey:              stateTestUUID(seed + 1),
		ParamsHash:               executionTestHash(t, HashDomainOperationParams, seed+100),
		Record:                   stateTransitionRecord(seed + 10),
	}
}

func executionTestBeginCommand(t *testing.T, seed int, running executionTestRunningRun, prepared PrepareOperationResult, connectionGeneration int64) BeginOperationDispatchCommand {
	t.Helper()
	return BeginOperationDispatchCommand{
		OperationID:              prepared.Operation.ID,
		ExecutionID:              prepared.Execution.ID,
		RunID:                    running.Run.ID,
		AttemptID:                running.Attempt.ID,
		HolderID:                 running.Attempt.HolderID,
		Generation:               running.Attempt.Generation,
		ConnectionGeneration:     connectionGeneration,
		ExpectedExecutionVersion: prepared.Execution.Version,
		ExpectedOperationVersion: prepared.Operation.Version,
		PolicyContextHash:        prepared.Execution.PolicyContextHash,
		OperationPlanHash:        prepared.Execution.OperationPlanHash,
		ParamsHash:               prepared.Operation.ParamsHash,
		Record:                   stateTransitionRecord(seed),
	}
}

func executionTestHash(t *testing.T, domain CanonicalHashDomain, seed int) CanonicalJSONHash {
	t.Helper()
	_, hash, err := ValidateAndHashCanonicalJSON(
		domain,
		[]byte(fmt.Sprintf(`{"seed":%d}`, seed)),
		func(value any) error {
			if _, ok := value.(map[string]any); !ok {
				return errors.New("object required")
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("create execution test hash: %v", err)
	}
	return hash
}
