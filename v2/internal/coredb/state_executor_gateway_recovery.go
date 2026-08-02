package coredb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type gatewayRecoveryFence struct {
	generation int64
	changed    bool
}

type gatewayRecoveryCandidate struct {
	executionID string
	runID       string
	attemptID   string
}

type gatewayRecoveryOperationChange struct {
	OperationID          string `json:"operationId"`
	Ordinal              int    `json:"ordinal"`
	FromStatus           string `json:"fromStatus"`
	ToStatus             string `json:"toStatus"`
	ConnectionGeneration int64  `json:"connectionGeneration,omitempty"`
}

type gatewayRecoveryOperationState struct {
	OperationID          string `json:"operationId"`
	Ordinal              int    `json:"ordinal"`
	Status               string `json:"status"`
	ConnectionGeneration int64  `json:"connectionGeneration,omitempty"`
}

// RecoverExecutorGateway is the startup boundary for the Phase 1
// single-replica executor-gateway. Fencing commits before execution recovery:
// once the first transaction returns, an old process can no longer pass the
// current-connection check in Begin/Acknowledge/CompleteOperation. Recovery
// may then fail or be retried without reopening the old send boundary.
func (s *StateStore) RecoverExecutorGateway(ctx context.Context, command RecoverExecutorGatewayCommand) (RecoverExecutorGatewayResult, error) {
	const operation = "RecoverExecutorGateway"
	if err := validateRecoverExecutorGateway(command); err != nil {
		return RecoverExecutorGatewayResult{}, commandError(ErrorInvalidArgument, operation, "executor", command.ExecutorID, err.Error())
	}
	fence, err := s.fenceExecutorConnectionForGatewayRecovery(ctx, command)
	if err != nil {
		return RecoverExecutorGatewayResult{}, err
	}
	recovered, remaining, err := s.recoverExecutorGatewayExecutions(ctx, command, fence.generation)
	if err != nil {
		return RecoverExecutorGatewayResult{}, err
	}
	return RecoverExecutorGatewayResult{
		FencedConnectionGeneration: fence.generation,
		ConnectionFenced:           fence.changed,
		RecoveredExecutions:        recovered,
		Remaining:                  remaining,
	}, nil
}

func (s *StateStore) fenceExecutorConnectionForGatewayRecovery(ctx context.Context, command RecoverExecutorGatewayCommand) (gatewayRecoveryFence, error) {
	const operation = "RecoverExecutorGateway"
	return withStateTransaction(ctx, s, operation+"Fence", func(transaction pgx.Tx) (gatewayRecoveryFence, error) {
		if _, err := s.lockEnrolledExecutor(ctx, transaction, operation, command.ExecutorID); err != nil {
			return gatewayRecoveryFence{}, err
		}
		connection, found, err := s.lockExecutorConnection(ctx, transaction, operation, command.ExecutorID)
		if err != nil {
			return gatewayRecoveryFence{}, err
		}
		if !found {
			generation, err := s.latestExecutorConnectionGeneration(ctx, transaction, operation, command.ExecutorID)
			if err != nil {
				return gatewayRecoveryFence{}, err
			}
			if err := s.markExecutorOffline(ctx, transaction, operation, command.ExecutorID); err != nil {
				return gatewayRecoveryFence{}, err
			}
			return gatewayRecoveryFence{generation: generation}, nil
		}
		if connection.GatewayInstanceID == command.GatewayInstanceID {
			return gatewayRecoveryFence{}, commandError(
				ErrorInvalidState, operation, "executor_connection", command.ExecutorID,
				"startup recovery cannot fence a connection already owned by the recovering gateway instance",
			)
		}
		if connection.Status == ExecutorConnectionStatusFenced {
			if err := s.markExecutorOffline(ctx, transaction, operation, command.ExecutorID); err != nil {
				return gatewayRecoveryFence{}, err
			}
			return gatewayRecoveryFence{generation: connection.Generation}, nil
		}

		query := fmt.Sprintf(`
UPDATE %s
SET expires_at = LEAST(expires_at, pg_catalog.clock_timestamp()),
    renewed_at = pg_catalog.clock_timestamp(),
    status = 'fenced',
    version = version + 1
WHERE executor_id = $1
  AND generation = $2
  AND status IN ('connecting', 'online')`, s.table("executor_connections"))
		tag, err := transaction.Exec(ctx, query, command.ExecutorID, connection.Generation)
		if err != nil {
			return gatewayRecoveryFence{}, databaseError(operation+" fence previous connection", err)
		}
		if tag.RowsAffected() != 1 {
			return gatewayRecoveryFence{}, commandError(ErrorConflict, operation, "executor_connection", command.ExecutorID, "connection changed during startup recovery")
		}
		query = fmt.Sprintf(`
UPDATE %s
SET ended_at = COALESCE(ended_at, pg_catalog.clock_timestamp()),
    end_reason = COALESCE(end_reason, 'fenced')
WHERE connection_id = $1`, s.table("executor_connection_attempts"))
		if _, err := transaction.Exec(ctx, query, connection.ConnectionID); err != nil {
			return gatewayRecoveryFence{}, databaseError(operation+" close previous connection attempt", err)
		}
		if err := s.markExecutorOffline(ctx, transaction, operation, command.ExecutorID); err != nil {
			return gatewayRecoveryFence{}, err
		}
		return gatewayRecoveryFence{generation: connection.Generation, changed: true}, nil
	})
}

func (s *StateStore) recoverExecutorGatewayExecutions(
	ctx context.Context,
	command RecoverExecutorGatewayCommand,
	throughGeneration int64,
) (int, bool, error) {
	const operation = "RecoverExecutorGateway"
	type recoveryResult struct {
		recovered int
		remaining bool
	}
	result, err := withStateTransaction(ctx, s, operation+"Executions", func(transaction pgx.Tx) (recoveryResult, error) {
		// Every connection mutation first locks the executor row. Holding it for
		// this transaction prevents a fresh generation from appearing while we
		// deliberately avoid the connection->run lock order.
		if _, err := s.lockEnrolledExecutor(ctx, transaction, operation, command.ExecutorID); err != nil {
			return recoveryResult{}, err
		}
		if err := s.verifyGatewayRecoveryFence(ctx, transaction, operation, command.ExecutorID, throughGeneration); err != nil {
			return recoveryResult{}, err
		}

		candidates, err := s.listGatewayRecoveryCandidates(ctx, transaction, operation, command.ExecutorID, len(command.Records))
		if err != nil {
			return recoveryResult{}, err
		}
		recovered := 0
		for _, candidate := range candidates {
			run, attempt, execution, err := s.lockExecutionContext(
				ctx, transaction, operation, candidate.runID, candidate.attemptID, candidate.executionID,
			)
			if err != nil {
				return recoveryResult{}, err
			}
			if isTerminalExecutionStatus(execution.Status) {
				continue
			}
			if execution.ExecutorID != command.ExecutorID || !gatewayRecoverableExecutionStatus(execution.Status) {
				return recoveryResult{}, commandError(ErrorInvalidState, operation, "execution", execution.ID, "execution changed outside the gateway recovery boundary")
			}
			if recovered >= len(command.Records) {
				return recoveryResult{}, commandError(ErrorInvalidState, operation, "executor", command.ExecutorID, "gateway recovery record batch was exhausted")
			}
			if err := s.recoverGatewayExecution(
				ctx, transaction, operation, run, attempt, execution,
				command.GatewayInstanceID, throughGeneration, command.Records[recovered],
			); err != nil {
				return recoveryResult{}, err
			}
			recovered++
		}
		remaining, err := s.gatewayRecoveryRemaining(ctx, transaction, operation, command.ExecutorID)
		if err != nil {
			return recoveryResult{}, err
		}
		return recoveryResult{recovered: recovered, remaining: remaining}, nil
	})
	if err != nil {
		return 0, false, err
	}
	return result.recovered, result.remaining, nil
}

func (s *StateStore) verifyGatewayRecoveryFence(
	ctx context.Context,
	transaction pgx.Tx,
	operation, executorID string,
	throughGeneration int64,
) error {
	query := fmt.Sprintf(`
SELECT generation, status
FROM %s
WHERE executor_id = $1`, s.table("executor_connections"))
	var generation int64
	var status string
	err := transaction.QueryRow(ctx, query, executorID).Scan(&generation, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		latest, latestErr := s.latestExecutorConnectionGeneration(ctx, transaction, operation, executorID)
		if latestErr != nil {
			return latestErr
		}
		if latest != throughGeneration {
			return commandError(ErrorConnectionFenced, operation, "executor_connection", executorID, "connection generation changed during startup recovery")
		}
		return nil
	}
	if err != nil {
		return databaseError(operation+" verify fenced connection", err)
	}
	if generation != throughGeneration || status != ExecutorConnectionStatusFenced {
		return &StateError{
			Code:              ErrorConnectionFenced,
			Operation:         operation,
			Resource:          "executor_connection",
			ResourceID:        executorID,
			CurrentGeneration: generation,
			Message:           "executor connection is not fenced at the recovery generation",
		}
	}
	return nil
}

func (s *StateStore) listGatewayRecoveryCandidates(
	ctx context.Context,
	transaction pgx.Tx,
	operation, executorID string,
	limit int,
) ([]gatewayRecoveryCandidate, error) {
	query := fmt.Sprintf(`
SELECT id::text, run_id::text, run_attempt_id::text
FROM %s
WHERE executor_id = $1
  AND status IN ('dispatching', 'running', 'cancelling')
ORDER BY run_id, created_at, id
LIMIT $2`, s.table("executions"))
	rows, err := transaction.Query(ctx, query, executorID, limit)
	if err != nil {
		return nil, databaseError(operation+" list recovery executions", err)
	}
	defer rows.Close()
	candidates := make([]gatewayRecoveryCandidate, 0, limit)
	for rows.Next() {
		var candidate gatewayRecoveryCandidate
		if err := rows.Scan(&candidate.executionID, &candidate.runID, &candidate.attemptID); err != nil {
			return nil, databaseError(operation+" scan recovery execution", err)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, databaseError(operation+" read recovery executions", err)
	}
	return candidates, nil
}

func (s *StateStore) gatewayRecoveryRemaining(ctx context.Context, transaction pgx.Tx, operation, executorID string) (bool, error) {
	query := fmt.Sprintf(`
SELECT EXISTS (
    SELECT 1
    FROM %s
    WHERE executor_id = $1
      AND status IN ('dispatching', 'running', 'cancelling')
)`, s.table("executions"))
	var remaining bool
	if err := transaction.QueryRow(ctx, query, executorID).Scan(&remaining); err != nil {
		return false, databaseError(operation+" count remaining recovery executions", err)
	}
	return remaining, nil
}

func (s *StateStore) recoverGatewayExecution(
	ctx context.Context,
	transaction pgx.Tx,
	operation string,
	run Run,
	attempt RunAttempt,
	execution Execution,
	gatewayInstanceID string,
	throughGeneration int64,
	record TransitionRecord,
) error {
	operations, err := s.lockExecutionOperations(ctx, transaction, operation, execution.ID)
	if err != nil {
		return err
	}
	finalStatuses, changes, err := planGatewayExecutionRecovery(execution, operations, throughGeneration)
	if err != nil {
		return commandError(ErrorInvalidState, operation, "execution", execution.ID, err.Error())
	}
	for _, change := range changes {
		var current ExecutionOperation
		for _, candidate := range operations {
			if candidate.ID == change.OperationID {
				current = candidate
				break
			}
		}
		_, hash, err := gatewayOperationRecoveryEvidence(execution, current, change.ToStatus, gatewayInstanceID, throughGeneration)
		if err != nil {
			return commandError(ErrorInvalidState, operation, "operation", current.ID, err.Error())
		}
		digest := hash.SHA256()
		query := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    terminal_result_hash = $2,
    terminal_at = pg_catalog.clock_timestamp(),
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $3 AND version = $4 AND status = $5`, s.table("execution_operations"))
		tag, err := transaction.Exec(ctx, query, change.ToStatus, digest[:], current.ID, current.Version, change.FromStatus)
		if err != nil {
			return databaseError(operation+" recover operation", err)
		}
		if tag.RowsAffected() != 1 {
			return versionConflict(operation, "operation", current.ID, current.Version)
		}
	}

	aggregateStatus, err := aggregateExecutionStatus(execution.Status, finalStatuses)
	if err != nil {
		return commandError(ErrorInvalidState, operation, "execution", execution.ID, "cannot aggregate recovered operation outcomes: "+err.Error())
	}
	states := make([]gatewayRecoveryOperationState, len(operations))
	for index, recoveredOperation := range operations {
		states[index] = gatewayRecoveryOperationState{
			OperationID: recoveredOperation.ID, Ordinal: recoveredOperation.Ordinal,
			Status: finalStatuses[index], ConnectionGeneration: recoveredOperation.ConnectionGeneration,
		}
	}
	_, executionHash, err := gatewayExecutionRecoveryEvidence(
		execution, aggregateStatus, states, gatewayInstanceID, throughGeneration,
	)
	if err != nil {
		return commandError(ErrorInvalidState, operation, "execution", execution.ID, err.Error())
	}
	digest := executionHash.SHA256()
	query := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    terminal_result_hash = $2,
    terminal_at = pg_catalog.clock_timestamp(),
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $3 AND version = $4
  AND status IN ('dispatching', 'running', 'cancelling')`, s.table("executions"))
	tag, err := transaction.Exec(ctx, query, aggregateStatus, digest[:], execution.ID, execution.Version)
	if err != nil {
		return databaseError(operation+" recover execution", err)
	}
	if tag.RowsAffected() != 1 {
		return versionConflict(operation, "execution", execution.ID, execution.Version)
	}

	payload, err := gatewayExecutionRecoveryEventPayload(
		run, attempt, execution, aggregateStatus, changes, gatewayInstanceID, throughGeneration,
	)
	if err != nil {
		return commandError(ErrorInvalidState, operation, "execution", execution.ID, err.Error())
	}
	return s.recordExecutionTransition(ctx, transaction, run, attempt, record, "execution."+aggregateStatus, payload)
}

func gatewayExecutionRecoveryEventPayload(
	run Run,
	attempt RunAttempt,
	execution Execution,
	status string,
	changes []gatewayRecoveryOperationChange,
	gatewayInstanceID string,
	throughGeneration int64,
) (json.RawMessage, error) {
	payload, err := marshalTransitionPayload(struct {
		Version                     string                           `json:"version"`
		Reason                      string                           `json:"reason"`
		RunID                       string                           `json:"runId"`
		RunAttemptID                string                           `json:"runAttemptId"`
		RunAttemptGeneration        int64                            `json:"runAttemptGeneration"`
		ExecutionID                 string                           `json:"executionId"`
		ExecutorID                  string                           `json:"executorId"`
		RecoveringGatewayInstanceID string                           `json:"recoveringGatewayInstanceId"`
		FencedConnectionGeneration  int64                            `json:"fencedConnectionGeneration"`
		Status                      string                           `json:"status"`
		OperationChanges            []gatewayRecoveryOperationChange `json:"operationChanges"`
	}{
		Version: "executor-gateway-recovery-v1", Reason: GatewayRecoveryReasonProcessRestart,
		RunID: run.ID, RunAttemptID: attempt.ID, RunAttemptGeneration: attempt.Generation,
		ExecutionID: execution.ID, ExecutorID: execution.ExecutorID,
		RecoveringGatewayInstanceID: gatewayInstanceID,
		FencedConnectionGeneration:  throughGeneration,
		Status:                      status, OperationChanges: changes,
	})
	if err != nil {
		return nil, err
	}
	if err := validateInlinePayload(payload); err != nil {
		return nil, errors.New("recovery event exceeds the canonical inline boundary: " + err.Error())
	}
	return payload, nil
}

func planGatewayExecutionRecovery(execution Execution, operations []ExecutionOperation, throughGeneration int64) ([]string, []gatewayRecoveryOperationChange, error) {
	if len(operations) != execution.OperationCount {
		return nil, nil, errors.New("persisted operation count does not match the frozen execution plan")
	}
	if throughGeneration < 1 {
		return nil, nil, errors.New("a dispatched execution has no fenced connection generation")
	}
	statuses := make([]string, len(operations))
	changes := make([]gatewayRecoveryOperationChange, 0, len(operations))
	for index, operation := range operations {
		if operation.Ordinal != index+1 {
			return nil, nil, errors.New("operation ordinals are not contiguous")
		}
		if operation.Status != OperationStatusPrepared && operation.Status != OperationStatusSkipped {
			if operation.ConnectionGeneration < 1 || operation.ConnectionGeneration > throughGeneration {
				return nil, nil, fmt.Errorf("operation %s belongs to an unfenced connection generation", operation.ID)
			}
		}
		switch operation.Status {
		case OperationStatusDispatching, OperationStatusAcknowledged:
			statuses[index] = OperationStatusUnknown
			changes = append(changes, gatewayRecoveryOperationChange{
				OperationID: operation.ID, Ordinal: operation.Ordinal,
				FromStatus: operation.Status, ToStatus: OperationStatusUnknown,
				ConnectionGeneration: operation.ConnectionGeneration,
			})
		case OperationStatusPrepared:
			statuses[index] = OperationStatusPrepared
		case OperationStatusSucceeded, OperationStatusFailed, OperationStatusCancelled, OperationStatusUnknown, OperationStatusSkipped:
			statuses[index] = operation.Status
		default:
			return nil, nil, fmt.Errorf("operation %s has unsupported status %q", operation.ID, operation.Status)
		}
	}
	for index, operation := range operations {
		if statuses[index] != OperationStatusPrepared {
			continue
		}
		if operation.Kind != OperationKindTimeoutTerminate || operation.Ordinal != execution.OperationCount || operation.Ordinal == 1 {
			return nil, nil, fmt.Errorf("required prepared operation %s cannot be fabricated as terminal during recovery", operation.ID)
		}
		for previous := 0; previous < index; previous++ {
			if !isTerminalOperationStatus(statuses[previous]) {
				return nil, nil, fmt.Errorf("optional timeout operation %s has a non-terminal predecessor", operation.ID)
			}
		}
		statuses[index] = OperationStatusSkipped
		changes = append(changes, gatewayRecoveryOperationChange{
			OperationID: operation.ID, Ordinal: operation.Ordinal,
			FromStatus: OperationStatusPrepared, ToStatus: OperationStatusSkipped,
		})
	}
	return statuses, changes, nil
}

func gatewayOperationRecoveryEvidence(
	execution Execution,
	operation ExecutionOperation,
	status, gatewayInstanceID string,
	throughGeneration int64,
) (json.RawMessage, CanonicalJSONHash, error) {
	raw, err := json.Marshal(struct {
		Version                     string `json:"version"`
		Reason                      string `json:"reason"`
		ExecutionID                 string `json:"executionId"`
		OperationID                 string `json:"operationId"`
		Status                      string `json:"status"`
		RecoveringGatewayInstanceID string `json:"recoveringGatewayInstanceId"`
		FencedConnectionGeneration  int64  `json:"fencedConnectionGeneration"`
	}{
		"executor-gateway-operation-recovery-v1", GatewayRecoveryReasonProcessRestart,
		execution.ID, operation.ID, status, gatewayInstanceID, throughGeneration,
	})
	if err != nil {
		return nil, CanonicalJSONHash{}, err
	}
	return ValidateAndHashCanonicalJSON(HashDomainOperationResult, raw, gatewayRecoveryObjectValidator(7))
}

func gatewayExecutionRecoveryEvidence(
	execution Execution,
	status string,
	operations []gatewayRecoveryOperationState,
	gatewayInstanceID string,
	throughGeneration int64,
) (json.RawMessage, CanonicalJSONHash, error) {
	raw, err := json.Marshal(struct {
		Version                     string                          `json:"version"`
		Reason                      string                          `json:"reason"`
		ExecutionID                 string                          `json:"executionId"`
		ExecutorID                  string                          `json:"executorId"`
		Status                      string                          `json:"status"`
		RecoveringGatewayInstanceID string                          `json:"recoveringGatewayInstanceId"`
		FencedConnectionGeneration  int64                           `json:"fencedConnectionGeneration"`
		Operations                  []gatewayRecoveryOperationState `json:"operations"`
	}{
		"executor-gateway-execution-recovery-v1", GatewayRecoveryReasonProcessRestart,
		execution.ID, execution.ExecutorID, status, gatewayInstanceID, throughGeneration, operations,
	})
	if err != nil {
		return nil, CanonicalJSONHash{}, err
	}
	return ValidateAndHashCanonicalJSON(HashDomainExecutionResult, raw, gatewayRecoveryObjectValidator(8))
}

func gatewayRecoveryObjectValidator(fields int) JSONValueValidator {
	return func(value any) error {
		object, ok := value.(map[string]any)
		if !ok || len(object) != fields {
			return errors.New("gateway recovery evidence must be the exact versioned object")
		}
		return nil
	}
}

func validateRecoverExecutorGateway(command RecoverExecutorGatewayCommand) error {
	if err := validateUUID("executor_id", command.ExecutorID); err != nil {
		return err
	}
	if err := validateUUID("gateway_instance_id", command.GatewayInstanceID); err != nil {
		return err
	}
	if command.ExecutorID == command.GatewayInstanceID {
		return errors.New("executor_id and gateway_instance_id must be distinct")
	}
	if len(command.Records) == 0 || len(command.Records) > MaxGatewayRecoveryBatch {
		return fmt.Errorf("records must contain between 1 and %d entries", MaxGatewayRecoveryBatch)
	}
	identities := map[string]struct{}{command.GatewayInstanceID: {}}
	var previousSequence int64
	for index, record := range command.Records {
		if err := validateTransitionRecord(record); err != nil {
			return fmt.Errorf("records[%d]: %w", index, err)
		}
		if record.ProducerInstanceID != command.GatewayInstanceID {
			return fmt.Errorf("records[%d].producer_instance_id does not match gateway_instance_id", index)
		}
		if record.ProducerSeq <= previousSequence {
			return errors.New("record producer sequences must be strictly increasing")
		}
		previousSequence = record.ProducerSeq
		for _, identity := range []string{record.EventID, record.OutboxID} {
			if _, duplicate := identities[identity]; duplicate {
				return errors.New("gateway recovery event, outbox, and producer identities must be distinct")
			}
			identities[identity] = struct{}{}
		}
	}
	return nil
}

func gatewayRecoverableExecutionStatus(status string) bool {
	return status == ExecutionStatusDispatching || status == ExecutionStatusRunning || status == ExecutionStatusCancelling
}

func (s *StateStore) requireLiveExecutorConnection(
	ctx context.Context,
	transaction pgx.Tx,
	operation string,
	execution Execution,
	connectionGeneration int64,
) error {
	connection, found, err := s.lockExecutorConnection(ctx, transaction, operation, execution.ExecutorID)
	if err != nil {
		return err
	}
	if !found {
		return &StateError{
			Code: ErrorConnectionFenced, Operation: operation, Resource: "operation", ResourceID: execution.ID,
			Message: "executor connection does not exist at the dispatch generation",
		}
	}
	if connection.Generation != connectionGeneration || connection.Status != ExecutorConnectionStatusOnline {
		return &StateError{
			Code: ErrorConnectionFenced, Operation: operation, Resource: "operation", ResourceID: execution.ID,
			CurrentGeneration: connection.Generation,
			Message:           "executor connection is not the current online dispatch generation",
		}
	}
	var live bool
	if err := transaction.QueryRow(ctx, "SELECT $1 > pg_catalog.clock_timestamp()", connection.ExpiresAt).Scan(&live); err != nil {
		return databaseError(operation+" evaluate executor connection lease", err)
	}
	if !live {
		return &StateError{
			Code: ErrorConnectionFenced, Operation: operation, Resource: "operation", ResourceID: execution.ID,
			CurrentGeneration: connection.Generation,
			Message:           "executor connection lease expired before the operation transition",
		}
	}
	return nil
}
