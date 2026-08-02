package coredb

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func (s *StateStore) BeginOperationDispatch(ctx context.Context, command BeginOperationDispatchCommand) (BeginOperationDispatchResult, error) {
	const operation = "BeginOperationDispatch"
	if err := validateBeginOperationDispatch(command); err != nil {
		return BeginOperationDispatchResult{}, commandError(ErrorInvalidArgument, operation, "operation", command.OperationID, err.Error())
	}

	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (BeginOperationDispatchResult, error) {
		run, attempt, execution, err := s.lockExecutionContext(
			ctx, transaction, operation, command.RunID, command.AttemptID, command.ExecutionID,
		)
		if err != nil {
			return BeginOperationDispatchResult{}, err
		}
		executionOperation, err := s.lockExecutionOperation(ctx, transaction, operation, execution.ID, command.OperationID)
		if err != nil {
			return BeginOperationDispatchResult{}, err
		}
		if err := verifyPersistedAttemptGeneration(operation, execution, attempt, command.Generation); err != nil {
			return BeginOperationDispatchResult{}, err
		}
		if !execution.PolicyContextHash.equal(command.PolicyContextHash) ||
			!execution.OperationPlanHash.equal(command.OperationPlanHash) ||
			!executionOperation.ParamsHash.equal(command.ParamsHash) {
			return BeginOperationDispatchResult{}, commandError(ErrorIdempotencyConflict, operation, "operation", executionOperation.ID, "dispatch context does not match the frozen execution and operation hashes")
		}

		if executionOperation.Status == OperationStatusSkipped {
			return BeginOperationDispatchResult{Execution: execution, Operation: executionOperation, Began: false}, nil
		}
		if executionOperation.Status != OperationStatusPrepared {
			if executionOperation.ConnectionGeneration != command.ConnectionGeneration {
				return BeginOperationDispatchResult{}, connectionFencedError(operation, executionOperation.ID, executionOperation.ConnectionGeneration)
			}
			return BeginOperationDispatchResult{Execution: execution, Operation: executionOperation, Began: false}, nil
		}

		if execution.Version != command.ExpectedExecutionVersion {
			return BeginOperationDispatchResult{}, versionConflict(operation, "execution", execution.ID, execution.Version)
		}
		if executionOperation.Version != command.ExpectedOperationVersion {
			return BeginOperationDispatchResult{}, versionConflict(operation, "operation", executionOperation.ID, executionOperation.Version)
		}
		if err := s.requireLiveExecutionContext(ctx, transaction, operation, run, attempt, command.HolderID, command.Generation); err != nil {
			return BeginOperationDispatchResult{}, err
		}
		if execution.Status != ExecutionStatusApproved && execution.Status != ExecutionStatusDispatching && execution.Status != ExecutionStatusRunning {
			return BeginOperationDispatchResult{}, commandError(ErrorInvalidState, operation, "execution", execution.ID, "execution is not authorized for dispatch")
		}
		var operationCount int
		countQuery := fmt.Sprintf("SELECT pg_catalog.count(*) FROM %s WHERE execution_id = $1", s.table("execution_operations"))
		if err := transaction.QueryRow(ctx, countQuery, execution.ID).Scan(&operationCount); err != nil {
			return BeginOperationDispatchResult{}, databaseError(operation+" count frozen operations", err)
		}
		if operationCount != execution.OperationCount {
			return BeginOperationDispatchResult{}, commandError(ErrorInvalidState, operation, "execution", execution.ID, "all frozen operations must be prepared before the first dispatch")
		}
		if err := s.requireLiveExecutorConnection(ctx, transaction, operation, execution, command.ConnectionGeneration); err != nil {
			return BeginOperationDispatchResult{}, err
		}

		updateOperation := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    connection_generation = $2,
    dispatched_at = pg_catalog.clock_timestamp(),
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $3 AND version = $4 AND status = $5
RETURNING %s`, s.table("execution_operations"), executionOperationColumns(""))
		dispatchingOperation, err := scanExecutionOperation(transaction.QueryRow(ctx, updateOperation,
			OperationStatusDispatching,
			command.ConnectionGeneration,
			executionOperation.ID,
			executionOperation.Version,
			OperationStatusPrepared,
		))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return BeginOperationDispatchResult{}, versionConflict(operation, "operation", executionOperation.ID, executionOperation.Version)
			}
			return BeginOperationDispatchResult{}, databaseError(operation+" update operation", err)
		}

		nextExecutionStatus := execution.Status
		if nextExecutionStatus == ExecutionStatusApproved {
			nextExecutionStatus = ExecutionStatusDispatching
		}
		updateExecution := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    dispatched_at = COALESCE(dispatched_at, pg_catalog.clock_timestamp()),
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $2 AND version = $3
RETURNING %s`, s.table("executions"), executionColumns(""))
		updatedExecution, err := scanExecution(transaction.QueryRow(ctx, updateExecution,
			nextExecutionStatus,
			execution.ID,
			execution.Version,
		))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return BeginOperationDispatchResult{}, versionConflict(operation, "execution", execution.ID, execution.Version)
			}
			return BeginOperationDispatchResult{}, databaseError(operation+" update execution", err)
		}

		payload, err := marshalOperationTransitionPayload(run, attempt, execution, dispatchingOperation)
		if err != nil {
			return BeginOperationDispatchResult{}, commandError(ErrorInvalidArgument, operation, "operation", executionOperation.ID, err.Error())
		}
		if err := s.recordExecutionTransition(ctx, transaction, run, attempt, command.Record, "operation.dispatching", payload); err != nil {
			return BeginOperationDispatchResult{}, err
		}
		return BeginOperationDispatchResult{Execution: updatedExecution, Operation: dispatchingOperation, Began: true}, nil
	})
}

func (s *StateStore) AcknowledgeOperation(ctx context.Context, command AcknowledgeOperationCommand) (AcknowledgeOperationResult, error) {
	const operation = "AcknowledgeOperation"
	if err := validateAcknowledgeOperation(command); err != nil {
		return AcknowledgeOperationResult{}, commandError(ErrorInvalidArgument, operation, "operation", command.OperationID, err.Error())
	}

	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (AcknowledgeOperationResult, error) {
		run, attempt, execution, err := s.lockExecutionContext(
			ctx, transaction, operation, command.RunID, command.AttemptID, command.ExecutionID,
		)
		if err != nil {
			return AcknowledgeOperationResult{}, err
		}
		executionOperation, err := s.lockExecutionOperation(ctx, transaction, operation, execution.ID, command.OperationID)
		if err != nil {
			return AcknowledgeOperationResult{}, err
		}
		if err := verifyPersistedAttemptGeneration(operation, execution, attempt, command.Generation); err != nil {
			return AcknowledgeOperationResult{}, err
		}
		if executionOperation.ConnectionGeneration != command.ConnectionGeneration {
			return AcknowledgeOperationResult{}, connectionFencedError(operation, executionOperation.ID, executionOperation.ConnectionGeneration)
		}

		if executionOperation.Status == OperationStatusAcknowledged || isTerminalOperationStatus(executionOperation.Status) {
			if executionOperation.AcknowledgementHash == nil || !executionOperation.AcknowledgementHash.equal(command.AcknowledgementHash) {
				return AcknowledgeOperationResult{}, commandError(ErrorIdempotencyConflict, operation, "operation", executionOperation.ID, "acknowledgement evidence differs from the committed evidence")
			}
			return AcknowledgeOperationResult{Execution: execution, Operation: executionOperation, Changed: false}, nil
		}
		if execution.Version != command.ExpectedExecutionVersion {
			return AcknowledgeOperationResult{}, versionConflict(operation, "execution", execution.ID, execution.Version)
		}
		if executionOperation.Version != command.ExpectedOperationVersion {
			return AcknowledgeOperationResult{}, versionConflict(operation, "operation", executionOperation.ID, executionOperation.Version)
		}
		if executionOperation.Status != OperationStatusDispatching {
			return AcknowledgeOperationResult{}, commandError(ErrorInvalidState, operation, "operation", executionOperation.ID, "only a dispatching operation can be acknowledged")
		}
		if execution.Status != ExecutionStatusDispatching && execution.Status != ExecutionStatusRunning && execution.Status != ExecutionStatusCancelling {
			return AcknowledgeOperationResult{}, commandError(ErrorInvalidState, operation, "execution", execution.ID, "execution is not awaiting operation acknowledgement")
		}
		if err := s.requireLiveExecutorConnection(ctx, transaction, operation, execution, command.ConnectionGeneration); err != nil {
			return AcknowledgeOperationResult{}, err
		}

		acknowledgementHash := command.AcknowledgementHash.SHA256()
		updateOperation := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    acknowledgement_hash = $2,
    acknowledged_at = pg_catalog.clock_timestamp(),
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $3 AND version = $4 AND status = $5
RETURNING %s`, s.table("execution_operations"), executionOperationColumns(""))
		acknowledgedOperation, err := scanExecutionOperation(transaction.QueryRow(ctx, updateOperation,
			OperationStatusAcknowledged,
			acknowledgementHash[:],
			executionOperation.ID,
			executionOperation.Version,
			OperationStatusDispatching,
		))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return AcknowledgeOperationResult{}, versionConflict(operation, "operation", executionOperation.ID, executionOperation.Version)
			}
			return AcknowledgeOperationResult{}, databaseError(operation+" update operation", err)
		}

		nextExecutionStatus := execution.Status
		if nextExecutionStatus == ExecutionStatusDispatching {
			nextExecutionStatus = ExecutionStatusRunning
		}
		updateExecution := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $2 AND version = $3
RETURNING %s`, s.table("executions"), executionColumns(""))
		updatedExecution, err := scanExecution(transaction.QueryRow(ctx, updateExecution,
			nextExecutionStatus,
			execution.ID,
			execution.Version,
		))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return AcknowledgeOperationResult{}, versionConflict(operation, "execution", execution.ID, execution.Version)
			}
			return AcknowledgeOperationResult{}, databaseError(operation+" update execution", err)
		}

		payload, err := marshalOperationTransitionPayload(run, attempt, execution, acknowledgedOperation)
		if err != nil {
			return AcknowledgeOperationResult{}, commandError(ErrorInvalidArgument, operation, "operation", executionOperation.ID, err.Error())
		}
		if err := s.recordExecutionTransition(ctx, transaction, run, attempt, command.Record, "operation.acknowledged", payload); err != nil {
			return AcknowledgeOperationResult{}, err
		}
		return AcknowledgeOperationResult{Execution: updatedExecution, Operation: acknowledgedOperation, Changed: true}, nil
	})
}

func (s *StateStore) CompleteOperation(ctx context.Context, command CompleteOperationCommand) (CompleteOperationResult, error) {
	const operation = "CompleteOperation"
	if err := validateCompleteOperation(command); err != nil {
		return CompleteOperationResult{}, commandError(ErrorInvalidArgument, operation, "operation", command.OperationID, err.Error())
	}

	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (CompleteOperationResult, error) {
		run, attempt, execution, err := s.lockExecutionContext(
			ctx, transaction, operation, command.RunID, command.AttemptID, command.ExecutionID,
		)
		if err != nil {
			return CompleteOperationResult{}, err
		}
		executionOperation, err := s.lockExecutionOperation(ctx, transaction, operation, execution.ID, command.OperationID)
		if err != nil {
			return CompleteOperationResult{}, err
		}
		if err := verifyPersistedAttemptGeneration(operation, execution, attempt, command.Generation); err != nil {
			return CompleteOperationResult{}, err
		}
		if executionOperation.ConnectionGeneration != command.ConnectionGeneration {
			return CompleteOperationResult{}, connectionFencedError(operation, executionOperation.ID, executionOperation.ConnectionGeneration)
		}

		if isTerminalOperationStatus(executionOperation.Status) {
			if executionOperation.Status != command.TerminalStatus || executionOperation.TerminalResultHash == nil || !executionOperation.TerminalResultHash.equal(command.ResultHash) {
				return CompleteOperationResult{}, commandError(ErrorIdempotencyConflict, operation, "operation", executionOperation.ID, "terminal outcome differs from the committed outcome")
			}
			return CompleteOperationResult{Execution: execution, Operation: executionOperation, Changed: false}, nil
		}
		if execution.Version != command.ExpectedExecutionVersion {
			return CompleteOperationResult{}, versionConflict(operation, "execution", execution.ID, execution.Version)
		}
		if executionOperation.Version != command.ExpectedOperationVersion {
			return CompleteOperationResult{}, versionConflict(operation, "operation", executionOperation.ID, executionOperation.Version)
		}
		if executionOperation.Status == OperationStatusDispatching && command.TerminalStatus != OperationStatusUnknown {
			return CompleteOperationResult{}, commandError(ErrorInvalidState, operation, "operation", executionOperation.ID, "dispatching without acknowledgement can only close as unknown")
		}
		if executionOperation.Status != OperationStatusDispatching && executionOperation.Status != OperationStatusAcknowledged {
			return CompleteOperationResult{}, commandError(ErrorInvalidState, operation, "operation", executionOperation.ID, "operation has not crossed the dispatch boundary")
		}
		if execution.Status != ExecutionStatusDispatching && execution.Status != ExecutionStatusRunning && execution.Status != ExecutionStatusCancelling {
			return CompleteOperationResult{}, commandError(ErrorInvalidState, operation, "execution", execution.ID, "execution is not accepting operation terminal evidence")
		}
		if err := s.requireLiveExecutorConnection(ctx, transaction, operation, execution, command.ConnectionGeneration); err != nil {
			return CompleteOperationResult{}, err
		}

		resultHash := command.ResultHash.SHA256()
		updateOperation := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    terminal_result_hash = $2,
    terminal_at = pg_catalog.clock_timestamp(),
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $3 AND version = $4
RETURNING %s`, s.table("execution_operations"), executionOperationColumns(""))
		completedOperation, err := scanExecutionOperation(transaction.QueryRow(ctx, updateOperation,
			command.TerminalStatus,
			resultHash[:],
			executionOperation.ID,
			executionOperation.Version,
		))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return CompleteOperationResult{}, versionConflict(operation, "operation", executionOperation.ID, executionOperation.Version)
			}
			return CompleteOperationResult{}, databaseError(operation+" update operation", err)
		}

		updateExecution := fmt.Sprintf(`
UPDATE %s
SET version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $1 AND version = $2
RETURNING %s`, s.table("executions"), executionColumns(""))
		updatedExecution, err := scanExecution(transaction.QueryRow(ctx, updateExecution, execution.ID, execution.Version))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return CompleteOperationResult{}, versionConflict(operation, "execution", execution.ID, execution.Version)
			}
			return CompleteOperationResult{}, databaseError(operation+" update execution", err)
		}

		payload, err := marshalOperationTransitionPayload(run, attempt, execution, completedOperation)
		if err != nil {
			return CompleteOperationResult{}, commandError(ErrorInvalidArgument, operation, "operation", executionOperation.ID, err.Error())
		}
		if err := s.recordExecutionTransition(ctx, transaction, run, attempt, command.Record, "operation."+command.TerminalStatus, payload); err != nil {
			return CompleteOperationResult{}, err
		}
		return CompleteOperationResult{Execution: updatedExecution, Operation: completedOperation, Changed: true}, nil
	})
}

func (s *StateStore) SkipOperation(ctx context.Context, command SkipOperationCommand) (SkipOperationResult, error) {
	const operation = "SkipOperation"
	if err := validateSkipOperation(command); err != nil {
		return SkipOperationResult{}, commandError(ErrorInvalidArgument, operation, "operation", command.OperationID, err.Error())
	}

	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (SkipOperationResult, error) {
		run, attempt, execution, err := s.lockExecutionContext(
			ctx, transaction, operation, command.RunID, command.AttemptID, command.ExecutionID,
		)
		if err != nil {
			return SkipOperationResult{}, err
		}
		operations, err := s.lockExecutionOperations(ctx, transaction, operation, execution.ID)
		if err != nil {
			return SkipOperationResult{}, err
		}
		if len(operations) != execution.OperationCount {
			return SkipOperationResult{}, commandError(ErrorInvalidState, operation, "execution", execution.ID, "persisted operation count does not match the frozen execution plan")
		}
		var executionOperation *ExecutionOperation
		for index := range operations {
			if operations[index].ID == command.OperationID {
				executionOperation = &operations[index]
				break
			}
		}
		if executionOperation == nil {
			return SkipOperationResult{}, commandError(ErrorNotFound, operation, "operation", command.OperationID, "operation does not exist for execution")
		}
		if err := verifyPersistedAttemptGeneration(operation, execution, attempt, command.Generation); err != nil {
			return SkipOperationResult{}, err
		}

		if executionOperation.Status == OperationStatusSkipped {
			if executionOperation.TerminalResultHash == nil || !executionOperation.TerminalResultHash.equal(command.ResultHash) {
				return SkipOperationResult{}, commandError(ErrorIdempotencyConflict, operation, "operation", executionOperation.ID, "skip result differs from the committed result")
			}
			return SkipOperationResult{Execution: execution, Operation: *executionOperation, Changed: false}, nil
		}
		if isTerminalOperationStatus(executionOperation.Status) {
			return SkipOperationResult{}, commandError(ErrorIdempotencyConflict, operation, "operation", executionOperation.ID, "operation already has a dispatched terminal outcome")
		}
		if execution.Version != command.ExpectedExecutionVersion {
			return SkipOperationResult{}, versionConflict(operation, "execution", execution.ID, execution.Version)
		}
		if executionOperation.Version != command.ExpectedOperationVersion {
			return SkipOperationResult{}, versionConflict(operation, "operation", executionOperation.ID, executionOperation.Version)
		}
		if err := s.requireLiveExecutionContext(ctx, transaction, operation, run, attempt, command.HolderID, command.Generation); err != nil {
			return SkipOperationResult{}, err
		}
		if err := validatePreparedOperationSkip(execution, *executionOperation, operations); err != nil {
			return SkipOperationResult{}, commandError(ErrorInvalidState, operation, "operation", executionOperation.ID, err.Error())
		}

		resultHash := command.ResultHash.SHA256()
		updateOperation := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    terminal_result_hash = $2,
    terminal_at = pg_catalog.clock_timestamp(),
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $3 AND version = $4 AND status = $5
RETURNING %s`, s.table("execution_operations"), executionOperationColumns(""))
		skippedOperation, err := scanExecutionOperation(transaction.QueryRow(ctx, updateOperation,
			OperationStatusSkipped,
			resultHash[:],
			executionOperation.ID,
			executionOperation.Version,
			OperationStatusPrepared,
		))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return SkipOperationResult{}, versionConflict(operation, "operation", executionOperation.ID, executionOperation.Version)
			}
			return SkipOperationResult{}, databaseError(operation+" update operation", err)
		}

		updateExecution := fmt.Sprintf(`
UPDATE %s
SET version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $1 AND version = $2
RETURNING %s`, s.table("executions"), executionColumns(""))
		updatedExecution, err := scanExecution(transaction.QueryRow(ctx, updateExecution, execution.ID, execution.Version))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return SkipOperationResult{}, versionConflict(operation, "execution", execution.ID, execution.Version)
			}
			return SkipOperationResult{}, databaseError(operation+" update execution", err)
		}

		payload, err := marshalOperationTransitionPayload(run, attempt, execution, skippedOperation)
		if err != nil {
			return SkipOperationResult{}, commandError(ErrorInvalidArgument, operation, "operation", executionOperation.ID, err.Error())
		}
		if err := s.recordExecutionTransition(ctx, transaction, run, attempt, command.Record, "operation.skipped", payload); err != nil {
			return SkipOperationResult{}, err
		}
		return SkipOperationResult{Execution: updatedExecution, Operation: skippedOperation, Changed: true}, nil
	})
}

func (s *StateStore) CompleteExecution(ctx context.Context, command CompleteExecutionCommand) (CompleteExecutionResult, error) {
	const operation = "CompleteExecution"
	if err := validateCompleteExecution(command); err != nil {
		return CompleteExecutionResult{}, commandError(ErrorInvalidArgument, operation, "execution", command.ExecutionID, err.Error())
	}

	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (CompleteExecutionResult, error) {
		run, attempt, execution, err := s.lockExecutionContext(
			ctx, transaction, operation, command.RunID, command.AttemptID, command.ExecutionID,
		)
		if err != nil {
			return CompleteExecutionResult{}, err
		}
		if err := verifyPersistedAttemptGeneration(operation, execution, attempt, command.Generation); err != nil {
			return CompleteExecutionResult{}, err
		}

		if isTerminalExecutionStatus(execution.Status) {
			if execution.Status != command.TerminalStatus || execution.TerminalResultHash == nil || !execution.TerminalResultHash.equal(command.ResultHash) {
				return CompleteExecutionResult{}, commandError(ErrorIdempotencyConflict, operation, "execution", execution.ID, "terminal outcome differs from the committed outcome")
			}
			return CompleteExecutionResult{Execution: execution, Changed: false}, nil
		}
		if execution.Version != command.ExpectedExecutionVersion {
			return CompleteExecutionResult{}, versionConflict(operation, "execution", execution.ID, execution.Version)
		}
		if execution.Status != ExecutionStatusDispatching && execution.Status != ExecutionStatusRunning && execution.Status != ExecutionStatusCancelling {
			return CompleteExecutionResult{}, commandError(ErrorInvalidState, operation, "execution", execution.ID, "execution has not crossed the dispatch boundary")
		}

		operationStatuses, err := s.lockExecutionOperationStatuses(ctx, transaction, execution.ID)
		if err != nil {
			return CompleteExecutionResult{}, err
		}
		if len(operationStatuses) != execution.OperationCount {
			return CompleteExecutionResult{}, commandError(ErrorInvalidState, operation, "execution", execution.ID, "persisted operation count does not match the frozen execution plan")
		}
		aggregateStatus, err := aggregateExecutionStatus(execution.Status, operationStatuses)
		if err != nil {
			return CompleteExecutionResult{}, commandError(ErrorInvalidState, operation, "execution", execution.ID, err.Error())
		}
		if aggregateStatus != command.TerminalStatus {
			return CompleteExecutionResult{}, commandError(ErrorInvalidState, operation, "execution", execution.ID, "requested terminal status does not match operation outcomes; expected "+aggregateStatus)
		}

		resultHash := command.ResultHash.SHA256()
		updateExecution := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    terminal_result_hash = $2,
    terminal_at = pg_catalog.clock_timestamp(),
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $3 AND version = $4
RETURNING %s`, s.table("executions"), executionColumns(""))
		completedExecution, err := scanExecution(transaction.QueryRow(ctx, updateExecution,
			command.TerminalStatus,
			resultHash[:],
			execution.ID,
			execution.Version,
		))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return CompleteExecutionResult{}, versionConflict(operation, "execution", execution.ID, execution.Version)
			}
			return CompleteExecutionResult{}, databaseError(operation+" update execution", err)
		}

		payload, err := marshalTransitionPayload(struct {
			RunID             string `json:"runId"`
			RunAttemptID      string `json:"runAttemptId"`
			AttemptGeneration int64  `json:"runAttemptGeneration"`
			ExecutionID       string `json:"executionId"`
			Status            string `json:"status"`
		}{run.ID, attempt.ID, attempt.Generation, execution.ID, completedExecution.Status})
		if err != nil {
			return CompleteExecutionResult{}, commandError(ErrorInvalidArgument, operation, "execution", execution.ID, err.Error())
		}
		if err := s.recordExecutionTransition(ctx, transaction, run, attempt, command.Record, "execution."+command.TerminalStatus, payload); err != nil {
			return CompleteExecutionResult{}, err
		}
		return CompleteExecutionResult{Execution: completedExecution, Changed: true}, nil
	})
}

func validateBeginOperationDispatch(command BeginOperationDispatchCommand) error {
	if err := validateOperationTransitionIdentity(command.OperationID, command.ExecutionID, command.RunID, command.AttemptID, command.Generation, command.ConnectionGeneration, command.ExpectedExecutionVersion, command.ExpectedOperationVersion); err != nil {
		return err
	}
	if err := validateBoundedText("holder_id", command.HolderID, 256); err != nil {
		return err
	}
	hashes := []struct {
		field  string
		hash   CanonicalJSONHash
		domain CanonicalHashDomain
	}{
		{"policy_context_hash", command.PolicyContextHash, HashDomainPolicyContext},
		{"operation_plan_hash", command.OperationPlanHash, HashDomainOperationPlan},
		{"params_hash", command.ParamsHash, HashDomainOperationParams},
	}
	for _, hash := range hashes {
		if err := validateCanonicalHash(hash.field, hash.hash, hash.domain); err != nil {
			return err
		}
	}
	return validateTransitionRecord(command.Record)
}

func validateAcknowledgeOperation(command AcknowledgeOperationCommand) error {
	if err := validateOperationTransitionIdentity(command.OperationID, command.ExecutionID, command.RunID, command.AttemptID, command.Generation, command.ConnectionGeneration, command.ExpectedExecutionVersion, command.ExpectedOperationVersion); err != nil {
		return err
	}
	if err := validateCanonicalHash("acknowledgement_hash", command.AcknowledgementHash, HashDomainOperationAck); err != nil {
		return err
	}
	return validateTransitionRecord(command.Record)
}

func validateCompleteOperation(command CompleteOperationCommand) error {
	if err := validateOperationTransitionIdentity(command.OperationID, command.ExecutionID, command.RunID, command.AttemptID, command.Generation, command.ConnectionGeneration, command.ExpectedExecutionVersion, command.ExpectedOperationVersion); err != nil {
		return err
	}
	if !isDispatchedTerminalOperationStatus(command.TerminalStatus) {
		return errors.New("terminal_status must be succeeded, failed, cancelled, or unknown")
	}
	if err := validateCanonicalHash("result_hash", command.ResultHash, HashDomainOperationResult); err != nil {
		return err
	}
	return validateTransitionRecord(command.Record)
}

func validateSkipOperation(command SkipOperationCommand) error {
	identifiers := []struct {
		field string
		value string
	}{
		{"operation_id", command.OperationID},
		{"execution_id", command.ExecutionID},
		{"run_id", command.RunID},
		{"attempt_id", command.AttemptID},
	}
	for _, identifier := range identifiers {
		if err := validateUUID(identifier.field, identifier.value); err != nil {
			return err
		}
	}
	if err := validateBoundedText("holder_id", command.HolderID, 256); err != nil {
		return err
	}
	if command.Generation < 1 || command.ExpectedExecutionVersion < 1 || command.ExpectedOperationVersion < 1 {
		return errors.New("generation and expected versions must be positive")
	}
	if err := validateCanonicalHash("result_hash", command.ResultHash, HashDomainOperationResult); err != nil {
		return err
	}
	return validateTransitionRecord(command.Record)
}

func validateCompleteExecution(command CompleteExecutionCommand) error {
	identifiers := []struct {
		field string
		value string
	}{
		{"execution_id", command.ExecutionID},
		{"run_id", command.RunID},
		{"attempt_id", command.AttemptID},
	}
	for _, identifier := range identifiers {
		if err := validateUUID(identifier.field, identifier.value); err != nil {
			return err
		}
	}
	if command.Generation < 1 || command.ExpectedExecutionVersion < 1 {
		return errors.New("generation and expected_execution_version must be positive")
	}
	if !isTerminalExecutionStatus(command.TerminalStatus) {
		return errors.New("terminal_status must be succeeded, failed, cancelled, or unknown")
	}
	if err := validateCanonicalHash("result_hash", command.ResultHash, HashDomainExecutionResult); err != nil {
		return err
	}
	return validateTransitionRecord(command.Record)
}

func validateOperationTransitionIdentity(operationID, executionID, runID, attemptID string, generation, connectionGeneration, expectedExecutionVersion, expectedOperationVersion int64) error {
	identifiers := []struct {
		field string
		value string
	}{
		{"operation_id", operationID},
		{"execution_id", executionID},
		{"run_id", runID},
		{"attempt_id", attemptID},
	}
	for _, identifier := range identifiers {
		if err := validateUUID(identifier.field, identifier.value); err != nil {
			return err
		}
	}
	if generation < 1 || connectionGeneration < 1 || expectedExecutionVersion < 1 || expectedOperationVersion < 1 {
		return errors.New("generation, connection_generation, and expected versions must be positive")
	}
	return nil
}

func (s *StateStore) lockExecutionOperation(ctx context.Context, transaction pgx.Tx, operation, executionID, operationID string) (ExecutionOperation, error) {
	query := fmt.Sprintf(`
SELECT %s
FROM %s AS o
WHERE o.id = $1 AND o.execution_id = $2
FOR UPDATE`, executionOperationColumns("o"), s.table("execution_operations"))
	executionOperation, err := scanExecutionOperation(transaction.QueryRow(ctx, query, operationID, executionID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ExecutionOperation{}, commandError(ErrorNotFound, operation, "operation", operationID, "operation does not exist for execution")
		}
		return ExecutionOperation{}, databaseError(operation+" lock operation", err)
	}
	return executionOperation, nil
}

func (s *StateStore) lockExecutionOperationStatuses(ctx context.Context, transaction pgx.Tx, executionID string) ([]string, error) {
	query := fmt.Sprintf(`
SELECT status
FROM %s
WHERE execution_id = $1
ORDER BY ordinal
FOR UPDATE`, s.table("execution_operations"))
	rows, err := transaction.Query(ctx, query, executionID)
	if err != nil {
		return nil, databaseError("CompleteExecution lock operations", err)
	}
	defer rows.Close()
	var statuses []string
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			return nil, databaseError("CompleteExecution scan operation status", err)
		}
		statuses = append(statuses, status)
	}
	if err := rows.Err(); err != nil {
		return nil, databaseError("CompleteExecution read operation statuses", err)
	}
	return statuses, nil
}

func (s *StateStore) lockExecutionOperations(ctx context.Context, transaction pgx.Tx, operation, executionID string) ([]ExecutionOperation, error) {
	query := fmt.Sprintf(`
SELECT %s
FROM %s AS o
WHERE o.execution_id = $1
ORDER BY o.ordinal
FOR UPDATE`, executionOperationColumns("o"), s.table("execution_operations"))
	rows, err := transaction.Query(ctx, query, executionID)
	if err != nil {
		return nil, databaseError(operation+" lock operations", err)
	}
	defer rows.Close()
	var operations []ExecutionOperation
	for rows.Next() {
		executionOperation, scanErr := scanExecutionOperation(rows)
		if scanErr != nil {
			return nil, databaseError(operation+" scan operation", scanErr)
		}
		operations = append(operations, executionOperation)
	}
	if err := rows.Err(); err != nil {
		return nil, databaseError(operation+" read operations", err)
	}
	return operations, nil
}

func verifyPersistedAttemptGeneration(operation string, execution Execution, attempt RunAttempt, generation int64) error {
	if execution.RunAttemptGeneration != generation || attempt.Generation != generation || execution.RunAttemptID != attempt.ID {
		return fencedAttemptError(operation, attempt.ID, execution.RunAttemptGeneration, "command does not match the persisted execution attempt generation")
	}
	return nil
}

func connectionFencedError(operation, operationID string, currentGeneration int64) error {
	return &StateError{
		Code:              ErrorConnectionFenced,
		Operation:         operation,
		Resource:          "operation",
		ResourceID:        operationID,
		CurrentGeneration: currentGeneration,
		Message:           "executor connection generation does not match the dispatch boundary",
	}
}

func isTerminalOperationStatus(status string) bool {
	switch status {
	case OperationStatusSucceeded, OperationStatusFailed, OperationStatusCancelled, OperationStatusUnknown, OperationStatusSkipped:
		return true
	default:
		return false
	}
}

func isDispatchedTerminalOperationStatus(status string) bool {
	switch status {
	case OperationStatusSucceeded, OperationStatusFailed, OperationStatusCancelled, OperationStatusUnknown:
		return true
	default:
		return false
	}
}

func validatePreparedOperationSkip(execution Execution, target ExecutionOperation, operations []ExecutionOperation) error {
	if execution.Status != ExecutionStatusDispatching && execution.Status != ExecutionStatusRunning && execution.Status != ExecutionStatusCancelling {
		return errors.New("execution has not crossed the dispatch boundary")
	}
	if target.Status != OperationStatusPrepared {
		return errors.New("only a prepared operation can be skipped")
	}
	if target.Kind != OperationKindTimeoutTerminate || target.Ordinal != execution.OperationCount {
		return errors.New("only the trailing optional timeout_terminate operation can be skipped")
	}
	if target.Ordinal == 1 {
		return errors.New("timeout_terminate requires a preceding dispatched operation")
	}
	for _, preceding := range operations {
		if preceding.Ordinal >= target.Ordinal {
			break
		}
		if !isTerminalOperationStatus(preceding.Status) {
			return errors.New("all preceding operations must be terminal before timeout_terminate can be skipped")
		}
	}
	return nil
}

func isTerminalExecutionStatus(status string) bool {
	switch status {
	case ExecutionStatusSucceeded, ExecutionStatusFailed, ExecutionStatusCancelled, ExecutionStatusUnknown:
		return true
	default:
		return false
	}
}

func aggregateExecutionStatus(executionStatus string, statuses []string) (string, error) {
	if len(statuses) == 0 {
		return "", errors.New("execution has no operations")
	}
	counts := make(map[string]int)
	for _, status := range statuses {
		switch status {
		case OperationStatusPrepared:
			return "", errors.New("an operation is still prepared")
		case OperationStatusDispatching, OperationStatusAcknowledged:
			return "", errors.New("an operation is still dispatching or acknowledged")
		case OperationStatusSucceeded, OperationStatusFailed, OperationStatusCancelled, OperationStatusUnknown, OperationStatusSkipped:
			counts[status]++
		default:
			return "", fmt.Errorf("operation has unsupported status %q", status)
		}
	}
	if counts[OperationStatusUnknown] > 0 {
		return ExecutionStatusUnknown, nil
	}
	if counts[OperationStatusFailed] > 0 {
		return ExecutionStatusFailed, nil
	}
	if counts[OperationStatusCancelled] > 0 {
		return ExecutionStatusCancelled, nil
	}
	if counts[OperationStatusSucceeded] > 0 && counts[OperationStatusSucceeded]+counts[OperationStatusSkipped] == len(statuses) {
		return ExecutionStatusSucceeded, nil
	}
	return "", errors.New("execution has no dispatched terminal outcome")
}

func marshalOperationTransitionPayload(run Run, attempt RunAttempt, execution Execution, operation ExecutionOperation) ([]byte, error) {
	return marshalTransitionPayload(struct {
		RunID             string `json:"runId"`
		RunAttemptID      string `json:"runAttemptId"`
		AttemptGeneration int64  `json:"runAttemptGeneration"`
		ExecutionID       string `json:"executionId"`
		OperationID       string `json:"operationId"`
		Ordinal           int    `json:"ordinal"`
		Status            string `json:"status"`
	}{run.ID, attempt.ID, attempt.Generation, execution.ID, operation.ID, operation.Ordinal, operation.Status})
}
