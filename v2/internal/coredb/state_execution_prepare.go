package coredb

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (s *StateStore) PrepareExecution(ctx context.Context, command PrepareExecutionCommand) (PrepareExecutionResult, error) {
	const operation = "PrepareExecution"
	if err := validatePrepareExecution(command); err != nil {
		return PrepareExecutionResult{}, commandError(ErrorInvalidArgument, operation, "execution", command.ExecutionID, err.Error())
	}
	initialStatus := executionStatusForPolicyDecision(command.PolicyDecision)
	target, err := normalizedPrepareTarget(command)
	if err != nil {
		return PrepareExecutionResult{}, commandError(ErrorInvalidArgument, operation, "execution", command.ExecutionID, err.Error())
	}

	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (PrepareExecutionResult, error) {
		run, err := s.lockRun(ctx, transaction, operation, command.RunID)
		if err != nil {
			return PrepareExecutionResult{}, err
		}

		existingQuery := fmt.Sprintf(`
SELECT %s
FROM %s AS e
WHERE e.run_id = $1 AND e.app_server_tool_call_id = $2
FOR UPDATE`, executionColumns("e"), s.table("executions"))
		existing, err := scanExecution(transaction.QueryRow(ctx, existingQuery, command.RunID, command.AppServerToolCallID))
		if err == nil {
			if !executionMatchesPrepare(existing, command) {
				return PrepareExecutionResult{}, &StateError{
					Code:         ErrorIdempotencyConflict,
					Operation:    operation,
					Resource:     "execution",
					ResourceID:   existing.ID,
					CurrentRunID: existing.RunID,
					Message:      "tool call identity was already used with a different execution fingerprint",
				}
			}
			return PrepareExecutionResult{Execution: existing, Created: false}, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return PrepareExecutionResult{}, databaseError(operation+" read tool call identity", err)
		}
		attempt, err := s.lockAttempt(ctx, transaction, operation, command.RunID, command.AttemptID)
		if err != nil {
			return PrepareExecutionResult{}, err
		}

		if run.Version != command.ExpectedRunVersion {
			return PrepareExecutionResult{}, versionConflict(operation, "run", run.ID, run.Version)
		}
		if attempt.Version != command.ExpectedAttemptVersion {
			return PrepareExecutionResult{}, versionConflict(operation, "attempt", attempt.ID, attempt.Version)
		}
		if err := s.requireLiveExecutionContext(ctx, transaction, operation, run, attempt, command.HolderID, command.Generation); err != nil {
			return PrepareExecutionResult{}, err
		}

		argumentsHash := command.ArgumentsHash.SHA256()
		toolSchemaHash := command.ToolSchemaHash.SHA256()
		operationPlanHash := command.OperationPlanHash.SHA256()
		policyContextHash := command.PolicyContextHash.SHA256()
		insertQuery := fmt.Sprintf(`
INSERT INTO %s
    (id, run_id, run_attempt_id, run_attempt_generation,
     app_server_tool_call_id, executor_id, env_id,
     target_kind, target_id, target_generation,
     tool_name, tool_version, mapper_version, policy_version, policy_decision,
     operation_count,
     canonicalizer_version, arguments_hash, tool_schema_hash,
     operation_plan_hash, policy_context_hash, status, terminal_at)
VALUES
    ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
     $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22,
     CASE WHEN $22 = 'denied' THEN pg_catalog.clock_timestamp() ELSE NULL END)
RETURNING %s`, s.table("executions"), executionColumns(""))
		execution, err := scanExecution(transaction.QueryRow(ctx, insertQuery,
			command.ExecutionID,
			command.RunID,
			command.AttemptID,
			command.Generation,
			command.AppServerToolCallID,
			nullableUUID(command.ExecutorID),
			command.EnvID,
			target.Kind,
			target.ID,
			nullablePositiveInt64(target.Generation),
			command.ToolName,
			command.ToolVersion,
			command.MapperVersion,
			command.PolicyVersion,
			command.PolicyDecision,
			command.OperationCount,
			CanonicalizerRFC8785V1,
			argumentsHash[:],
			toolSchemaHash[:],
			operationPlanHash[:],
			policyContextHash[:],
			initialStatus,
		))
		if err != nil {
			var postgresError *pgconn.PgError
			if pgxErrorAs(err, &postgresError) && postgresError.Code == "23505" {
				return PrepareExecutionResult{}, commandError(ErrorConflict, operation, "execution", command.ExecutionID, "execution identity is already in use")
			}
			return PrepareExecutionResult{}, databaseError(operation+" insert execution", err)
		}

		kind := "execution." + initialStatus
		payload, err := marshalTransitionPayload(struct {
			RunID             string `json:"runId"`
			RunAttemptID      string `json:"runAttemptId"`
			AttemptGeneration int64  `json:"runAttemptGeneration"`
			ExecutionID       string `json:"executionId"`
			Status            string `json:"status"`
		}{run.ID, attempt.ID, attempt.Generation, execution.ID, execution.Status})
		if err != nil {
			return PrepareExecutionResult{}, commandError(ErrorInvalidArgument, operation, "execution", execution.ID, err.Error())
		}
		if err := s.recordExecutionTransition(ctx, transaction, run, attempt, command.Record, kind, payload); err != nil {
			return PrepareExecutionResult{}, err
		}
		return PrepareExecutionResult{Execution: execution, Created: true}, nil
	})
}

func (s *StateStore) PrepareOperation(ctx context.Context, command PrepareOperationCommand) (PrepareOperationResult, error) {
	const operation = "PrepareOperation"
	if err := validatePrepareOperation(command); err != nil {
		return PrepareOperationResult{}, commandError(ErrorInvalidArgument, operation, "operation", command.OperationID, err.Error())
	}

	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (PrepareOperationResult, error) {
		run, attempt, execution, err := s.lockExecutionContext(
			ctx, transaction, operation, command.RunID, command.AttemptID, command.ExecutionID,
		)
		if err != nil {
			return PrepareOperationResult{}, err
		}

		existingQuery := fmt.Sprintf(`
SELECT %s
FROM %s AS o
WHERE o.execution_id = $1 AND o.ordinal = $2
FOR UPDATE`, executionOperationColumns("o"), s.table("execution_operations"))
		existing, err := scanExecutionOperation(transaction.QueryRow(ctx, existingQuery, command.ExecutionID, command.Ordinal))
		if err == nil {
			if !operationMatchesPrepare(existing, command) {
				return PrepareOperationResult{}, &StateError{
					Code:       ErrorIdempotencyConflict,
					Operation:  operation,
					Resource:   "operation",
					ResourceID: existing.ID,
					Message:    "execution ordinal was already used with a different operation identity or fingerprint",
				}
			}
			return PrepareOperationResult{Execution: execution, Operation: existing, Created: false}, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return PrepareOperationResult{}, databaseError(operation+" read execution ordinal", err)
		}

		if execution.Version != command.ExpectedExecutionVersion {
			return PrepareOperationResult{}, versionConflict(operation, "execution", execution.ID, execution.Version)
		}
		if err := s.requireLiveExecutionContext(ctx, transaction, operation, run, attempt, command.HolderID, command.Generation); err != nil {
			return PrepareOperationResult{}, err
		}
		if execution.RunAttemptGeneration != command.Generation || execution.RunAttemptID != command.AttemptID {
			return PrepareOperationResult{}, fencedAttemptError(operation, attempt.ID, run.CurrentAttemptGeneration, "execution belongs to a different attempt generation")
		}
		if command.Ordinal > execution.OperationCount {
			return PrepareOperationResult{}, commandError(ErrorInvalidArgument, operation, "operation", command.OperationID, "ordinal exceeds the frozen execution operation count")
		}
		if execution.Status != ExecutionStatusApproved && execution.Status != ExecutionStatusPendingApproval && execution.Status != ExecutionStatusCreated {
			return PrepareOperationResult{}, commandError(ErrorInvalidState, operation, "execution", execution.ID, "operation plan is immutable after execution dispatch or terminal policy decision")
		}

		paramsHash := command.ParamsHash.SHA256()
		insertQuery := fmt.Sprintf(`
INSERT INTO %s
    (id, execution_id, ordinal, kind, effect_class, mutation_key,
     canonicalizer_version, params_hash, status,
     target_kind, target_id, target_generation)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING %s`, s.table("execution_operations"), executionOperationColumns(""))
		prepared, err := scanExecutionOperation(transaction.QueryRow(ctx, insertQuery,
			command.OperationID,
			command.ExecutionID,
			command.Ordinal,
			command.Kind,
			command.EffectClass,
			command.MutationKey,
			CanonicalizerRFC8785V1,
			paramsHash[:],
			OperationStatusPrepared,
			execution.Target.Kind,
			nullableUUID(execution.Target.ID),
			nullablePositiveInt64(execution.Target.Generation),
		))
		if err != nil {
			var postgresError *pgconn.PgError
			if pgxErrorAs(err, &postgresError) && postgresError.Code == "23505" {
				return PrepareOperationResult{}, commandError(ErrorConflict, operation, "operation", command.OperationID, "operation identity, ordinal, or mutation key is already in use")
			}
			return PrepareOperationResult{}, databaseError(operation+" insert operation", err)
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
				return PrepareOperationResult{}, versionConflict(operation, "execution", execution.ID, execution.Version)
			}
			return PrepareOperationResult{}, databaseError(operation+" update execution", err)
		}

		payload, err := marshalTransitionPayload(struct {
			RunID             string `json:"runId"`
			RunAttemptID      string `json:"runAttemptId"`
			AttemptGeneration int64  `json:"runAttemptGeneration"`
			ExecutionID       string `json:"executionId"`
			OperationID       string `json:"operationId"`
			Ordinal           int    `json:"ordinal"`
			Status            string `json:"status"`
		}{run.ID, attempt.ID, attempt.Generation, execution.ID, prepared.ID, prepared.Ordinal, prepared.Status})
		if err != nil {
			return PrepareOperationResult{}, commandError(ErrorInvalidArgument, operation, "operation", prepared.ID, err.Error())
		}
		if err := s.recordExecutionTransition(ctx, transaction, run, attempt, command.Record, "operation.prepared", payload); err != nil {
			return PrepareOperationResult{}, err
		}
		return PrepareOperationResult{Execution: updatedExecution, Operation: prepared, Created: true}, nil
	})
}

func validatePrepareExecution(command PrepareExecutionCommand) error {
	identifiers := []struct {
		field string
		value string
	}{
		{"execution_id", command.ExecutionID},
		{"run_id", command.RunID},
		{"attempt_id", command.AttemptID},
		{"env_id", command.EnvID},
	}
	for _, identifier := range identifiers {
		if err := validateUUID(identifier.field, identifier.value); err != nil {
			return err
		}
	}
	if _, err := normalizedPrepareTarget(command); err != nil {
		return err
	}
	if err := validateBoundedText("holder_id", command.HolderID, 256); err != nil {
		return err
	}
	texts := []struct {
		field   string
		value   string
		maximum int
	}{
		{"app_server_tool_call_id", command.AppServerToolCallID, 256},
		{"tool_name", command.ToolName, 128},
		{"tool_version", command.ToolVersion, 128},
		{"mapper_version", command.MapperVersion, 128},
		{"policy_version", command.PolicyVersion, 128},
	}
	for _, text := range texts {
		if err := validateBoundedText(text.field, text.value, text.maximum); err != nil {
			return err
		}
	}
	if command.Generation < 1 || command.ExpectedRunVersion < 1 || command.ExpectedAttemptVersion < 1 {
		return errors.New("generation and expected versions must be positive")
	}
	if command.OperationCount < 1 || command.OperationCount > MaxExecutionOperations {
		return fmt.Errorf("operation_count must be between 1 and %d", MaxExecutionOperations)
	}
	if executionStatusForPolicyDecision(command.PolicyDecision) == "" {
		return errors.New("policy_decision must be allow, ask, or deny")
	}
	hashes := []struct {
		field  string
		hash   CanonicalJSONHash
		domain CanonicalHashDomain
	}{
		{"arguments_hash", command.ArgumentsHash, HashDomainExecutionArguments},
		{"tool_schema_hash", command.ToolSchemaHash, HashDomainToolSchema},
		{"operation_plan_hash", command.OperationPlanHash, HashDomainOperationPlan},
		{"policy_context_hash", command.PolicyContextHash, HashDomainPolicyContext},
	}
	for _, hash := range hashes {
		if err := validateCanonicalHash(hash.field, hash.hash, hash.domain); err != nil {
			return err
		}
	}
	return validateTransitionRecord(command.Record)
}

func validatePrepareOperation(command PrepareOperationCommand) error {
	identifiers := []struct {
		field string
		value string
	}{
		{"operation_id", command.OperationID},
		{"execution_id", command.ExecutionID},
		{"run_id", command.RunID},
		{"attempt_id", command.AttemptID},
		{"mutation_key", command.MutationKey},
	}
	for _, identifier := range identifiers {
		if err := validateUUID(identifier.field, identifier.value); err != nil {
			return err
		}
	}
	if err := validateBoundedText("holder_id", command.HolderID, 256); err != nil {
		return err
	}
	if err := validateBoundedText("kind", command.Kind, 128); err != nil {
		return err
	}
	if command.Generation < 1 || command.ExpectedExecutionVersion < 1 || command.Ordinal < 1 {
		return errors.New("generation, expected_execution_version, and ordinal must be positive")
	}
	if command.EffectClass != OperationEffectRead && command.EffectClass != OperationEffectMutation {
		return errors.New("effect_class must be read or mutation")
	}
	if err := validateCanonicalHash("params_hash", command.ParamsHash, HashDomainOperationParams); err != nil {
		return err
	}
	return validateTransitionRecord(command.Record)
}

func executionStatusForPolicyDecision(decision string) string {
	switch decision {
	case PolicyDecisionAllow:
		return ExecutionStatusApproved
	case PolicyDecisionAsk:
		return ExecutionStatusPendingApproval
	case PolicyDecisionDeny:
		return ExecutionStatusDenied
	default:
		return ""
	}
}

func executionMatchesPrepare(execution Execution, command PrepareExecutionCommand) bool {
	target, err := normalizedPrepareTarget(command)
	if err != nil {
		return false
	}
	targetMatches := execution.Target.Kind == target.Kind && execution.Target.ID == target.ID &&
		(target.Generation == 0 || execution.Target.Generation == target.Generation)
	return execution.RunID == command.RunID &&
		execution.RunAttemptID == command.AttemptID &&
		execution.RunAttemptGeneration == command.Generation &&
		execution.AppServerToolCallID == command.AppServerToolCallID &&
		execution.ExecutorID == command.ExecutorID &&
		execution.EnvID == command.EnvID &&
		targetMatches &&
		execution.ToolName == command.ToolName &&
		execution.ToolVersion == command.ToolVersion &&
		execution.MapperVersion == command.MapperVersion &&
		execution.PolicyVersion == command.PolicyVersion &&
		execution.PolicyDecision == command.PolicyDecision &&
		execution.OperationCount == command.OperationCount &&
		execution.ArgumentsHash.equal(command.ArgumentsHash) &&
		execution.ToolSchemaHash.equal(command.ToolSchemaHash) &&
		execution.OperationPlanHash.equal(command.OperationPlanHash) &&
		execution.PolicyContextHash.equal(command.PolicyContextHash)
}

func normalizedPrepareTarget(command PrepareExecutionCommand) (DispatchTarget, error) {
	target := command.Target
	if target.Kind == "" {
		if err := validateUUID("executor_id", command.ExecutorID); err != nil {
			return DispatchTarget{}, err
		}
		return DispatchTarget{Kind: DispatchTargetAgentX, ID: command.ExecutorID}, nil
	}
	if err := validateDispatchTarget(target, true); err != nil {
		return DispatchTarget{}, err
	}
	switch target.Kind {
	case DispatchTargetAgentX:
		if command.ExecutorID != target.ID {
			return DispatchTarget{}, errors.New("agentx target_id must equal executor_id")
		}
	case DispatchTargetTAE:
		if command.ExecutorID != "" {
			return DispatchTarget{}, errors.New("managed execution must not project a TAE sandbox as executor_id")
		}
	}
	return target, nil
}

func validateDispatchTarget(target DispatchTarget, requireGeneration bool) error {
	if target.Kind != DispatchTargetAgentX && target.Kind != DispatchTargetTAE {
		return errors.New("target_kind must be agentx or tae")
	}
	if err := validateUUID("target_id", target.ID); err != nil {
		return err
	}
	if target.Generation < 0 || (requireGeneration && target.Generation < 1) {
		return errors.New("target_generation must be positive")
	}
	return nil
}

func nullablePositiveInt64(value int64) any {
	if value < 1 {
		return nil
	}
	return value
}

func nullableUUID(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func operationMatchesPrepare(operation ExecutionOperation, command PrepareOperationCommand) bool {
	return operation.ID == command.OperationID &&
		operation.ExecutionID == command.ExecutionID &&
		operation.Ordinal == command.Ordinal &&
		operation.Kind == command.Kind &&
		operation.EffectClass == command.EffectClass &&
		operation.MutationKey == command.MutationKey &&
		operation.ParamsHash.equal(command.ParamsHash)
}

func (s *StateStore) lockRunAttempt(
	ctx context.Context,
	transaction pgx.Tx,
	operation string,
	runID string,
	attemptID string,
) (Run, RunAttempt, error) {
	run, err := s.lockRun(ctx, transaction, operation, runID)
	if err != nil {
		return Run{}, RunAttempt{}, err
	}
	attempt, err := s.lockAttempt(ctx, transaction, operation, runID, attemptID)
	if err != nil {
		return Run{}, RunAttempt{}, err
	}
	return run, attempt, nil
}

func (s *StateStore) lockRun(ctx context.Context, transaction pgx.Tx, operation, runID string) (Run, error) {
	runQuery := fmt.Sprintf("SELECT %s FROM %s AS r WHERE r.id = $1 FOR UPDATE", runColumns("r"), s.table("runs"))
	run, err := scanRun(transaction.QueryRow(ctx, runQuery, runID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Run{}, commandError(ErrorNotFound, operation, "run", runID, "run does not exist")
		}
		return Run{}, databaseError(operation+" lock run", err)
	}
	return run, nil
}

func (s *StateStore) lockAttempt(ctx context.Context, transaction pgx.Tx, operation, runID, attemptID string) (RunAttempt, error) {
	attemptQuery := fmt.Sprintf(`
SELECT %s
FROM %s AS a
WHERE a.id = $1 AND a.run_id = $2
FOR UPDATE`, attemptColumns("a"), s.table("run_attempts"))
	attempt, err := scanAttempt(transaction.QueryRow(ctx, attemptQuery, attemptID, runID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return RunAttempt{}, commandError(ErrorNotFound, operation, "attempt", attemptID, "attempt does not exist for run")
		}
		return RunAttempt{}, databaseError(operation+" lock attempt", err)
	}
	return attempt, nil
}

func (s *StateStore) lockExecutionContext(
	ctx context.Context,
	transaction pgx.Tx,
	operation string,
	runID string,
	attemptID string,
	executionID string,
) (Run, RunAttempt, Execution, error) {
	run, attempt, err := s.lockRunAttempt(ctx, transaction, operation, runID, attemptID)
	if err != nil {
		return Run{}, RunAttempt{}, Execution{}, err
	}
	executionQuery := fmt.Sprintf(`
SELECT %s
FROM %s AS e
WHERE e.id = $1 AND e.run_id = $2 AND e.run_attempt_id = $3
FOR UPDATE`, executionColumns("e"), s.table("executions"))
	execution, err := scanExecution(transaction.QueryRow(ctx, executionQuery, executionID, runID, attemptID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Run{}, RunAttempt{}, Execution{}, commandError(ErrorNotFound, operation, "execution", executionID, "execution does not exist in the requested run attempt")
		}
		return Run{}, RunAttempt{}, Execution{}, databaseError(operation+" lock execution", err)
	}
	return run, attempt, execution, nil
}

func (s *StateStore) requireLiveExecutionContext(
	ctx context.Context,
	transaction pgx.Tx,
	operation string,
	run Run,
	attempt RunAttempt,
	holderID string,
	generation int64,
) error {
	if run.CurrentAttemptGeneration != generation || attempt.Generation != generation {
		return fencedAttemptError(operation, attempt.ID, run.CurrentAttemptGeneration, "attempt generation was fenced")
	}
	if attempt.HolderID != holderID {
		return fencedAttemptError(operation, attempt.ID, run.CurrentAttemptGeneration, "attempt holder was fenced")
	}
	if run.Status != RunStatusRunning || attempt.Status != AttemptStatusRunning || attempt.TurnStartedAt == nil {
		return commandError(ErrorInvalidState, operation, "attempt", attempt.ID, "execution commands require a running accepted turn")
	}
	activeQuery := fmt.Sprintf("SELECT active_run_id::text FROM %s WHERE id = $1", s.table("sessions"))
	var activeRunID *string
	if err := transaction.QueryRow(ctx, activeQuery, run.SessionID).Scan(&activeRunID); err != nil {
		return databaseError(operation+" read active run", err)
	}
	if activeRunID == nil || *activeRunID != run.ID {
		return commandError(ErrorInvalidState, operation, "run", run.ID, "run is not the session active run")
	}
	if err := s.requireLiveLeases(ctx, transaction, run, attempt, holderID, generation); err != nil {
		return err
	}
	return nil
}

func (s *StateStore) recordExecutionTransition(
	ctx context.Context,
	transaction pgx.Tx,
	run Run,
	attempt RunAttempt,
	record TransitionRecord,
	kind string,
	payload []byte,
) error {
	allocateQuery := fmt.Sprintf(`
UPDATE %s
SET next_event_seq = next_event_seq + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $1
RETURNING next_event_seq - 1`, s.table("runs"))
	var sequence int64
	if err := transaction.QueryRow(ctx, allocateQuery, run.ID).Scan(&sequence); err != nil {
		return databaseError("allocate "+kind+" event sequence", err)
	}
	if err := s.insertTransitionEvent(ctx, transaction, run.ID, sequence, &attempt.ID, &attempt.Generation, record, EventSourceExecutor, kind, payload); err != nil {
		return err
	}
	if err := s.insertOutbox(ctx, transaction, record.OutboxID, kind, run.ID, payload); err != nil {
		return err
	}
	return nil
}

func versionConflict(operation, resource, resourceID string, currentVersion int64) error {
	return &StateError{
		Code:           ErrorVersionConflict,
		Operation:      operation,
		Resource:       resource,
		ResourceID:     resourceID,
		CurrentVersion: currentVersion,
		Message:        resource + " version does not match",
	}
}

func fencedAttemptError(operation, attemptID string, currentGeneration int64, message string) error {
	return &StateError{
		Code:              ErrorLeaseLost,
		Operation:         operation,
		Resource:          "attempt",
		ResourceID:        attemptID,
		CurrentGeneration: currentGeneration,
		Message:           message,
	}
}
