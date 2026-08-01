package coredb

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (s *StateStore) CreateApproval(ctx context.Context, command CreateApprovalCommand) (CreateApprovalResult, error) {
	const operation = "CreateApproval"
	if err := validateCreateApproval(command); err != nil {
		return CreateApprovalResult{}, commandError(ErrorInvalidArgument, operation, "approval", command.ApprovalID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (CreateApprovalResult, error) {
		run, attempt, execution, err := s.lockExecutionContext(ctx, transaction, operation, command.RunID, command.AttemptID, command.ExecutionID)
		if err != nil {
			return CreateApprovalResult{}, err
		}
		contextHash, err := deriveApprovalContextHash(execution)
		if err != nil {
			return CreateApprovalResult{}, databaseError(operation+" derive context", err)
		}

		existingQuery := fmt.Sprintf(`
SELECT %s
FROM %s AS p
WHERE p.execution_id = $1
FOR UPDATE`, approvalColumns("p"), s.table("approvals"))
		existing, err := scanApproval(transaction.QueryRow(ctx, existingQuery, execution.ID))
		if err == nil {
			if !approvalMatchesCreate(existing, command, contextHash) {
				return CreateApprovalResult{}, commandError(ErrorIdempotencyConflict, operation, "approval", existing.ID, "execution already has a different approval identity or request fingerprint")
			}
			return CreateApprovalResult{Execution: execution, Approval: existing, Created: false}, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return CreateApprovalResult{}, databaseError(operation+" read execution approval", err)
		}
		if execution.Version != command.ExpectedExecutionVersion {
			return CreateApprovalResult{}, versionConflict(operation, "execution", execution.ID, execution.Version)
		}
		if err := s.requireLiveExecutionContext(ctx, transaction, operation, run, attempt, command.HolderID, command.Generation); err != nil {
			return CreateApprovalResult{}, err
		}
		if err := verifyPersistedAttemptGeneration(operation, execution, attempt, command.Generation); err != nil {
			return CreateApprovalResult{}, err
		}
		if execution.PolicyDecision != PolicyDecisionAsk || execution.Status != ExecutionStatusPendingApproval {
			return CreateApprovalResult{}, commandError(ErrorInvalidState, operation, "execution", execution.ID, "only a pending policy=ask execution can create an approval")
		}
		var databaseNow time.Time
		if err := transaction.QueryRow(ctx, "SELECT pg_catalog.clock_timestamp()").Scan(&databaseNow); err != nil {
			return CreateApprovalResult{}, databaseError(operation+" read database time", err)
		}
		if !command.ExpiresAt.After(databaseNow) || command.ExpiresAt.After(databaseNow.Add(MaxApprovalTTL)) {
			return CreateApprovalResult{}, commandError(ErrorInvalidArgument, operation, "approval", command.ApprovalID, "expires_at must be after database time and no more than the maximum approval TTL")
		}

		digest := contextHash.SHA256()
		insertQuery := fmt.Sprintf(`
INSERT INTO %s
    (id, execution_id, run_id, run_attempt_id, run_attempt_generation,
     nonce, requester_id, canonicalizer_version, context_hash, status,
     expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING %s`, s.table("approvals"), approvalColumns(""))
		approval, err := scanApproval(transaction.QueryRow(ctx, insertQuery,
			command.ApprovalID,
			execution.ID,
			run.ID,
			attempt.ID,
			attempt.Generation,
			command.Nonce,
			command.RequesterID,
			CanonicalizerRFC8785V1,
			digest[:],
			ApprovalStatusPending,
			command.ExpiresAt,
		))
		if err != nil {
			var postgresError *pgconn.PgError
			if pgxErrorAs(err, &postgresError) && postgresError.Code == "23505" {
				return CreateApprovalResult{}, commandError(ErrorConflict, operation, "approval", command.ApprovalID, "approval identity, execution, or nonce is already in use")
			}
			return CreateApprovalResult{}, databaseError(operation+" insert approval", err)
		}
		if err := s.recordApprovalStateTransition(ctx, transaction, run, attempt, execution, approval, command.Record, "approval.requested"); err != nil {
			return CreateApprovalResult{}, err
		}
		return CreateApprovalResult{Execution: execution, Approval: approval, Created: true}, nil
	})
}

func (s *StateStore) DecideApproval(ctx context.Context, command DecideApprovalCommand) (DecideApprovalResult, error) {
	const operation = "DecideApproval"
	if err := validateDecideApproval(command); err != nil {
		return DecideApprovalResult{}, commandError(ErrorInvalidArgument, operation, "approval", command.ApprovalID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (DecideApprovalResult, error) {
		run, attempt, execution, approval, err := s.lockApprovalContext(ctx, transaction, operation, command.ApprovalID)
		if err != nil {
			return DecideApprovalResult{}, err
		}
		if err := verifyApprovalCapability(execution, approval, command.Nonce, command.ExpectedContextHash); err != nil {
			return DecideApprovalResult{}, commandError(ErrorIdempotencyConflict, operation, "approval", approval.ID, err.Error())
		}
		if run.WorkspaceID != command.WorkspaceID {
			return DecideApprovalResult{}, commandError(ErrorNotFound, operation, "approval", approval.ID, "approval does not exist in the requested workspace")
		}
		if err := s.requireApprovalActor(ctx, transaction, operation, command.WorkspaceID, command.ActorID); err != nil {
			return DecideApprovalResult{}, err
		}
		if approvalDecisionAlreadyCommitted(approval, command.Decision) {
			return DecideApprovalResult{Execution: execution, Approval: approval, Changed: false}, nil
		}
		if approval.Status != ApprovalStatusPending {
			return DecideApprovalResult{}, commandError(ErrorIdempotencyConflict, operation, "approval", approval.ID, "approval already has a different terminal outcome")
		}
		if approval.Version != command.ExpectedApprovalVersion {
			return DecideApprovalResult{}, versionConflict(operation, "approval", approval.ID, approval.Version)
		}
		if err := s.requireLiveExecutionContext(ctx, transaction, operation, run, attempt, attempt.HolderID, approval.RunAttemptGeneration); err != nil {
			return DecideApprovalResult{}, err
		}
		if execution.Status != ExecutionStatusPendingApproval {
			return DecideApprovalResult{}, commandError(ErrorInvalidState, operation, "execution", execution.ID, "execution is no longer awaiting approval")
		}
		databaseNow, err := approvalDatabaseNow(ctx, transaction, operation)
		if err != nil {
			return DecideApprovalResult{}, err
		}
		if !databaseNow.Before(approval.ExpiresAt) {
			updatedExecution, updatedApproval, err := s.transitionApprovalTerminal(ctx, transaction, run, attempt, execution, approval, ApprovalStatusExpired, command.Record)
			if err != nil {
				return DecideApprovalResult{}, err
			}
			return DecideApprovalResult{Execution: updatedExecution, Approval: updatedApproval, Changed: true}, nil
		}

		status := ApprovalStatusApproved
		executionStatus := execution.Status
		if command.Decision == ApprovalDecisionDeny {
			status = ApprovalStatusDenied
			executionStatus = ExecutionStatusDenied
		}
		updateApproval := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    approver_id = $2,
    decision = $3,
    decided_at = pg_catalog.clock_timestamp(),
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $4 AND version = $5 AND status = $6
RETURNING %s`, s.table("approvals"), approvalColumns(""))
		updatedApproval, err := scanApproval(transaction.QueryRow(ctx, updateApproval,
			status, command.ActorID, command.Decision, approval.ID, approval.Version, ApprovalStatusPending,
		))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return DecideApprovalResult{}, versionConflict(operation, "approval", approval.ID, approval.Version)
			}
			return DecideApprovalResult{}, databaseError(operation+" update approval", err)
		}
		updatedExecution := execution
		if executionStatus == ExecutionStatusDenied {
			updatedExecution, err = s.updateApprovalExecutionTerminal(ctx, transaction, operation, execution, ExecutionStatusDenied)
			if err != nil {
				return DecideApprovalResult{}, err
			}
		}
		if err := s.recordApprovalStateTransition(ctx, transaction, run, attempt, updatedExecution, updatedApproval, command.Record, "approval."+status); err != nil {
			return DecideApprovalResult{}, err
		}
		return DecideApprovalResult{Execution: updatedExecution, Approval: updatedApproval, Changed: true}, nil
	})
}

func (s *StateStore) ExpireApproval(ctx context.Context, command ExpireApprovalCommand) (ExpireApprovalResult, error) {
	const operation = "ExpireApproval"
	if err := validateExpireApproval(command); err != nil {
		return ExpireApprovalResult{}, commandError(ErrorInvalidArgument, operation, "approval", command.ApprovalID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (ExpireApprovalResult, error) {
		run, attempt, execution, approval, err := s.lockApprovalContext(ctx, transaction, operation, command.ApprovalID)
		if err != nil {
			return ExpireApprovalResult{}, err
		}
		if err := verifyApprovalCapability(execution, approval, command.Nonce, command.ExpectedContextHash); err != nil {
			return ExpireApprovalResult{}, commandError(ErrorIdempotencyConflict, operation, "approval", approval.ID, err.Error())
		}
		if approval.Status == ApprovalStatusExpired {
			return ExpireApprovalResult{Execution: execution, Approval: approval, Changed: false}, nil
		}
		if approval.Status != ApprovalStatusPending && approval.Status != ApprovalStatusApproved {
			return ExpireApprovalResult{}, commandError(ErrorInvalidState, operation, "approval", approval.ID, "approval cannot expire after its current outcome")
		}
		if approval.Version != command.ExpectedApprovalVersion {
			return ExpireApprovalResult{}, versionConflict(operation, "approval", approval.ID, approval.Version)
		}
		databaseNow, err := approvalDatabaseNow(ctx, transaction, operation)
		if err != nil {
			return ExpireApprovalResult{}, err
		}
		if databaseNow.Before(approval.ExpiresAt) {
			return ExpireApprovalResult{}, commandError(ErrorInvalidState, operation, "approval", approval.ID, "approval has not reached its database expiry")
		}
		updatedExecution, updatedApproval, err := s.transitionApprovalTerminal(ctx, transaction, run, attempt, execution, approval, ApprovalStatusExpired, command.Record)
		if err != nil {
			return ExpireApprovalResult{}, err
		}
		return ExpireApprovalResult{Execution: updatedExecution, Approval: updatedApproval, Changed: true}, nil
	})
}

func (s *StateStore) CancelApproval(ctx context.Context, command CancelApprovalCommand) (CancelApprovalResult, error) {
	const operation = "CancelApproval"
	if err := validateCancelApproval(command); err != nil {
		return CancelApprovalResult{}, commandError(ErrorInvalidArgument, operation, "approval", command.ApprovalID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (CancelApprovalResult, error) {
		run, attempt, execution, approval, err := s.lockApprovalContext(ctx, transaction, operation, command.ApprovalID)
		if err != nil {
			return CancelApprovalResult{}, err
		}
		if err := verifyApprovalCapability(execution, approval, command.Nonce, command.ExpectedContextHash); err != nil {
			return CancelApprovalResult{}, commandError(ErrorIdempotencyConflict, operation, "approval", approval.ID, err.Error())
		}
		if approval.Status == ApprovalStatusCancelled || approval.Status == ApprovalStatusExpired || approval.Status == ApprovalStatusDenied {
			return CancelApprovalResult{Execution: execution, Approval: approval, Changed: false}, nil
		}
		if approval.Status != ApprovalStatusPending && approval.Status != ApprovalStatusApproved {
			return CancelApprovalResult{}, commandError(ErrorInvalidState, operation, "approval", approval.ID, "approval cannot be cancelled after its current outcome")
		}
		if approval.Version != command.ExpectedApprovalVersion {
			return CancelApprovalResult{}, versionConflict(operation, "approval", approval.ID, approval.Version)
		}
		databaseNow, err := approvalDatabaseNow(ctx, transaction, operation)
		if err != nil {
			return CancelApprovalResult{}, err
		}
		status := ApprovalStatusCancelled
		if !databaseNow.Before(approval.ExpiresAt) {
			status = ApprovalStatusExpired
		}
		updatedExecution, updatedApproval, err := s.transitionApprovalTerminal(ctx, transaction, run, attempt, execution, approval, status, command.Record)
		if err != nil {
			return CancelApprovalResult{}, err
		}
		return CancelApprovalResult{Execution: updatedExecution, Approval: updatedApproval, Changed: true}, nil
	})
}

func (s *StateStore) ConsumeApproval(ctx context.Context, command ConsumeApprovalCommand) (ConsumeApprovalResult, error) {
	const operation = "ConsumeApprovalAndAuthorizeExecution"
	if err := validateConsumeApproval(command); err != nil {
		return ConsumeApprovalResult{}, commandError(ErrorInvalidArgument, operation, "approval", command.ApprovalID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (ConsumeApprovalResult, error) {
		run, attempt, execution, approval, err := s.lockApprovalContext(ctx, transaction, operation, command.ApprovalID)
		if err != nil {
			return ConsumeApprovalResult{}, err
		}
		if execution.ID != command.ExecutionID || run.ID != command.RunID || attempt.ID != command.AttemptID {
			return ConsumeApprovalResult{}, commandError(ErrorNotFound, operation, "approval", approval.ID, "approval does not match the requested execution scope")
		}
		if err := verifyApprovalCapability(execution, approval, command.Nonce, command.ExpectedContextHash); err != nil {
			return ConsumeApprovalResult{}, commandError(ErrorIdempotencyConflict, operation, "approval", approval.ID, err.Error())
		}
		if approval.Status == ApprovalStatusConsumed {
			return ConsumeApprovalResult{Execution: execution, Approval: approval, Consumed: false}, nil
		}
		if approval.Status != ApprovalStatusApproved {
			return ConsumeApprovalResult{Execution: execution, Approval: approval, Consumed: false}, nil
		}
		if approval.Version != command.ExpectedApprovalVersion {
			return ConsumeApprovalResult{}, versionConflict(operation, "approval", approval.ID, approval.Version)
		}
		if execution.Version != command.ExpectedExecutionVersion {
			return ConsumeApprovalResult{}, versionConflict(operation, "execution", execution.ID, execution.Version)
		}
		databaseNow, err := approvalDatabaseNow(ctx, transaction, operation)
		if err != nil {
			return ConsumeApprovalResult{}, err
		}
		if !databaseNow.Before(approval.ExpiresAt) {
			updatedExecution, updatedApproval, err := s.transitionApprovalTerminal(ctx, transaction, run, attempt, execution, approval, ApprovalStatusExpired, command.Record)
			if err != nil {
				return ConsumeApprovalResult{}, err
			}
			return ConsumeApprovalResult{Execution: updatedExecution, Approval: updatedApproval, Consumed: false}, nil
		}
		if err := s.requireLiveExecutionContext(ctx, transaction, operation, run, attempt, command.HolderID, command.Generation); err != nil {
			return ConsumeApprovalResult{}, err
		}
		if err := verifyPersistedAttemptGeneration(operation, execution, attempt, command.Generation); err != nil {
			return ConsumeApprovalResult{}, err
		}
		allowed, err := s.approvalActorStillAuthorized(ctx, transaction, run.WorkspaceID, approval.ApproverID)
		if err != nil {
			return ConsumeApprovalResult{}, err
		}
		if !allowed {
			updatedExecution, updatedApproval, err := s.transitionApprovalTerminal(ctx, transaction, run, attempt, execution, approval, ApprovalStatusCancelled, command.Record)
			if err != nil {
				return ConsumeApprovalResult{}, err
			}
			return ConsumeApprovalResult{Execution: updatedExecution, Approval: updatedApproval, Consumed: false}, nil
		}
		if execution.Status != ExecutionStatusPendingApproval {
			return ConsumeApprovalResult{}, commandError(ErrorInvalidState, operation, "execution", execution.ID, "execution is no longer awaiting approval consumption")
		}

		updateApproval := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    consumed_at = pg_catalog.clock_timestamp(),
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $2 AND version = $3 AND status = $4
RETURNING %s`, s.table("approvals"), approvalColumns(""))
		consumed, err := scanApproval(transaction.QueryRow(ctx, updateApproval,
			ApprovalStatusConsumed, approval.ID, approval.Version, ApprovalStatusApproved,
		))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ConsumeApprovalResult{}, versionConflict(operation, "approval", approval.ID, approval.Version)
			}
			return ConsumeApprovalResult{}, databaseError(operation+" consume approval", err)
		}
		updateExecution := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $2 AND version = $3 AND status = $4
RETURNING %s`, s.table("executions"), executionColumns(""))
		authorized, err := scanExecution(transaction.QueryRow(ctx, updateExecution,
			ExecutionStatusApproved, execution.ID, execution.Version, ExecutionStatusPendingApproval,
		))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ConsumeApprovalResult{}, versionConflict(operation, "execution", execution.ID, execution.Version)
			}
			return ConsumeApprovalResult{}, databaseError(operation+" authorize execution", err)
		}
		if err := s.recordApprovalStateTransition(ctx, transaction, run, attempt, authorized, consumed, command.Record, "approval.consumed"); err != nil {
			return ConsumeApprovalResult{}, err
		}
		return ConsumeApprovalResult{Execution: authorized, Approval: consumed, Consumed: true}, nil
	})
}

// ObserveApproval returns the canonical approval visible to the exact live
// attempt holder. If database time has reached expiry, the read is promoted
// to the same atomic approval/execution/event transition used by explicit
// expiry; caller wall clocks never decide the terminal status.
func (s *StateStore) ObserveApproval(ctx context.Context, command ObserveApprovalCommand) (ObserveApprovalResult, error) {
	const operation = "ObserveApproval"
	if err := validateObserveApproval(command); err != nil {
		return ObserveApprovalResult{}, commandError(ErrorInvalidArgument, operation, "approval", command.ApprovalID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (ObserveApprovalResult, error) {
		run, attempt, execution, approval, err := s.lockApprovalContext(ctx, transaction, operation, command.ApprovalID)
		if err != nil {
			return ObserveApprovalResult{}, err
		}
		if execution.ID != command.ExecutionID || run.ID != command.RunID || attempt.ID != command.AttemptID ||
			approval.ExecutionID != command.ExecutionID || approval.RunID != command.RunID || approval.RunAttemptID != command.AttemptID {
			return ObserveApprovalResult{}, commandError(ErrorNotFound, operation, "approval", approval.ID, "approval does not match the requested execution scope")
		}
		if err := verifyApprovalCapability(execution, approval, command.Nonce, command.ExpectedContextHash); err != nil {
			return ObserveApprovalResult{}, commandError(ErrorIdempotencyConflict, operation, "approval", approval.ID, err.Error())
		}
		if approval.RunAttemptGeneration != command.Generation {
			return ObserveApprovalResult{}, fencedAttemptError(operation, attempt.ID, run.CurrentAttemptGeneration, "approval generation was fenced")
		}
		if approval.Version < command.AfterApprovalVersion {
			return ObserveApprovalResult{}, versionConflict(operation, "approval", approval.ID, approval.Version)
		}
		if err := s.requireApprovalObservationContext(ctx, transaction, operation, run, attempt, command.HolderID, command.Generation); err != nil {
			return ObserveApprovalResult{}, err
		}
		if approval.Status != ApprovalStatusPending && approval.Status != ApprovalStatusApproved {
			return ObserveApprovalResult{Execution: execution, Approval: approval}, nil
		}
		databaseNow, err := approvalDatabaseNow(ctx, transaction, operation)
		if err != nil {
			return ObserveApprovalResult{}, err
		}
		if databaseNow.Before(approval.ExpiresAt) {
			return ObserveApprovalResult{Execution: execution, Approval: approval}, nil
		}
		updatedExecution, updatedApproval, err := s.transitionApprovalTerminal(
			ctx, transaction, run, attempt, execution, approval, ApprovalStatusExpired, command.Record,
		)
		if err != nil {
			return ObserveApprovalResult{}, err
		}
		return ObserveApprovalResult{Execution: updatedExecution, Approval: updatedApproval, Changed: true}, nil
	})
}

// requireApprovalObservationContext preserves the exact holder, generation,
// active-run, and live-lease checks used by execution commands while allowing
// an accepted turn to finish the approval handshake after user cancellation
// has moved the run to cancelling. The attempt remains running until the
// worker has received the canonical outcome, acknowledged turn/interrupt, and
// the holder commits InterruptAttempt.
func (s *StateStore) requireApprovalObservationContext(
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
	if (run.Status != RunStatusRunning && run.Status != RunStatusCancelling) ||
		attempt.Status != AttemptStatusRunning || attempt.TurnStartedAt == nil {
		return commandError(ErrorInvalidState, operation, "attempt", attempt.ID, "approval observation requires a running accepted turn")
	}
	activeQuery := fmt.Sprintf("SELECT active_run_id::text FROM %s WHERE id = $1", s.table("sessions"))
	var activeRunID *string
	if err := transaction.QueryRow(ctx, activeQuery, run.SessionID).Scan(&activeRunID); err != nil {
		return databaseError(operation+" read active run", err)
	}
	if activeRunID == nil || *activeRunID != run.ID {
		return commandError(ErrorInvalidState, operation, "run", run.ID, "run is not the session active run")
	}
	return s.requireLiveLeases(ctx, transaction, run, attempt, holderID, generation)
}

func deriveApprovalContextHash(execution Execution) (CanonicalJSONHash, error) {
	digest := func(hash CanonicalJSONHash) string {
		value := hash.SHA256()
		return hex.EncodeToString(value[:])
	}
	raw, err := json.Marshal(struct {
		ExecutionID          string `json:"executionId"`
		RunID                string `json:"runId"`
		RunAttemptID         string `json:"runAttemptId"`
		RunAttemptGeneration int64  `json:"runAttemptGeneration"`
		AppServerToolCallID  string `json:"appServerToolCallId"`
		ExecutorID           string `json:"executorId"`
		EnvironmentID        string `json:"environmentId"`
		ToolName             string `json:"toolName"`
		ToolVersion          string `json:"toolVersion"`
		MapperVersion        string `json:"mapperVersion"`
		PolicyVersion        string `json:"policyVersion"`
		OperationCount       int    `json:"operationCount"`
		ArgumentsDigest      string `json:"argumentsDigest"`
		ToolSchemaDigest     string `json:"toolSchemaDigest"`
		OperationPlanDigest  string `json:"operationPlanDigest"`
		PolicyContextDigest  string `json:"policyContextDigest"`
	}{
		execution.ID, execution.RunID, execution.RunAttemptID, execution.RunAttemptGeneration,
		execution.AppServerToolCallID, execution.ExecutorID, execution.EnvID, execution.ToolName,
		execution.ToolVersion, execution.MapperVersion, execution.PolicyVersion, execution.OperationCount,
		digest(execution.ArgumentsHash), digest(execution.ToolSchemaHash), digest(execution.OperationPlanHash),
		digest(execution.PolicyContextHash),
	})
	if err != nil {
		return CanonicalJSONHash{}, err
	}
	_, result, err := ValidateAndHashCanonicalJSON(HashDomainApprovalContext, raw, func(value any) error {
		object, ok := value.(map[string]any)
		if !ok || len(object) != 16 {
			return errors.New("approval context must contain the frozen execution fingerprint")
		}
		return nil
	})
	return result, err
}

func (s *StateStore) lockApprovalContext(ctx context.Context, transaction pgx.Tx, operation, approvalID string) (Run, RunAttempt, Execution, Approval, error) {
	lookup := fmt.Sprintf(`SELECT run_id::text, run_attempt_id::text, execution_id::text FROM %s WHERE id = $1`, s.table("approvals"))
	var runID, attemptID, executionID string
	if err := transaction.QueryRow(ctx, lookup, approvalID).Scan(&runID, &attemptID, &executionID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Run{}, RunAttempt{}, Execution{}, Approval{}, commandError(ErrorNotFound, operation, "approval", approvalID, "approval does not exist")
		}
		return Run{}, RunAttempt{}, Execution{}, Approval{}, databaseError(operation+" read approval scope", err)
	}
	run, attempt, execution, err := s.lockExecutionContext(ctx, transaction, operation, runID, attemptID, executionID)
	if err != nil {
		return Run{}, RunAttempt{}, Execution{}, Approval{}, err
	}
	query := fmt.Sprintf(`SELECT %s FROM %s AS p WHERE p.id = $1 AND p.execution_id = $2 FOR UPDATE`, approvalColumns("p"), s.table("approvals"))
	approval, err := scanApproval(transaction.QueryRow(ctx, query, approvalID, executionID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Run{}, RunAttempt{}, Execution{}, Approval{}, commandError(ErrorNotFound, operation, "approval", approvalID, "approval does not exist for execution")
		}
		return Run{}, RunAttempt{}, Execution{}, Approval{}, databaseError(operation+" lock approval", err)
	}
	return run, attempt, execution, approval, nil
}

func verifyApprovalCapability(execution Execution, approval Approval, nonce string, expected [32]byte) error {
	derived, err := deriveApprovalContextHash(execution)
	if err != nil {
		return fmt.Errorf("derive approval context: %w", err)
	}
	if !approval.ContextHash.equal(derived) {
		return errors.New("stored approval context does not match the frozen execution fingerprint")
	}
	if approval.Nonce != nonce {
		return errors.New("approval nonce does not match")
	}
	if approval.ContextHash.SHA256() != expected {
		return errors.New("approval context digest does not match")
	}
	return nil
}

func approvalMatchesCreate(approval Approval, command CreateApprovalCommand, contextHash CanonicalJSONHash) bool {
	return approval.ID == command.ApprovalID &&
		approval.ExecutionID == command.ExecutionID &&
		approval.RunID == command.RunID &&
		approval.RunAttemptID == command.AttemptID &&
		approval.RunAttemptGeneration == command.Generation &&
		approval.Nonce == command.Nonce &&
		approval.RequesterID == command.RequesterID &&
		approval.ExpiresAt.Equal(command.ExpiresAt) &&
		approval.ContextHash.equal(contextHash)
}

func approvalDecisionAlreadyCommitted(approval Approval, decision string) bool {
	if decision == ApprovalDecisionApprove {
		return approval.Decision == ApprovalDecisionApprove && (approval.Status == ApprovalStatusApproved || approval.Status == ApprovalStatusConsumed)
	}
	return approval.Decision == ApprovalDecisionDeny && approval.Status == ApprovalStatusDenied
}

func approvalDatabaseNow(ctx context.Context, transaction pgx.Tx, operation string) (time.Time, error) {
	var now time.Time
	if err := transaction.QueryRow(ctx, "SELECT pg_catalog.clock_timestamp()").Scan(&now); err != nil {
		return time.Time{}, databaseError(operation+" read database time", err)
	}
	return now, nil
}

func (s *StateStore) transitionApprovalTerminal(ctx context.Context, transaction pgx.Tx, run Run, attempt RunAttempt, execution Execution, approval Approval, status string, record TransitionRecord) (Execution, Approval, error) {
	operation := "transition approval " + status
	updateApproval := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    decided_at = COALESCE(decided_at, pg_catalog.clock_timestamp()),
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $2 AND version = $3 AND status IN ($4, $5)
RETURNING %s`, s.table("approvals"), approvalColumns(""))
	updatedApproval, err := scanApproval(transaction.QueryRow(ctx, updateApproval,
		status, approval.ID, approval.Version, ApprovalStatusPending, ApprovalStatusApproved,
	))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Execution{}, Approval{}, versionConflict(operation, "approval", approval.ID, approval.Version)
		}
		return Execution{}, Approval{}, databaseError(operation+" update approval", err)
	}
	executionStatus := ExecutionStatusExpired
	if status == ApprovalStatusCancelled {
		executionStatus = ExecutionStatusCancelled
	}
	updatedExecution, err := s.updateApprovalExecutionTerminal(ctx, transaction, operation, execution, executionStatus)
	if err != nil {
		return Execution{}, Approval{}, err
	}
	if err := s.recordApprovalStateTransition(ctx, transaction, run, attempt, updatedExecution, updatedApproval, record, "approval."+status); err != nil {
		return Execution{}, Approval{}, err
	}
	return updatedExecution, updatedApproval, nil
}

func (s *StateStore) updateApprovalExecutionTerminal(ctx context.Context, transaction pgx.Tx, operation string, execution Execution, status string) (Execution, error) {
	var resultHash any
	if status == ExecutionStatusCancelled {
		raw := json.RawMessage(`{"reason":"approval_cancelled"}`)
		_, hash, err := ValidateAndHashCanonicalJSON(HashDomainExecutionResult, raw, func(value any) error {
			if _, ok := value.(map[string]any); !ok {
				return errors.New("approval cancellation result must be an object")
			}
			return nil
		})
		if err != nil {
			return Execution{}, databaseError(operation+" hash cancellation result", err)
		}
		digest := hash.SHA256()
		resultHash = digest[:]
	}
	update := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    terminal_result_hash = $2,
    terminal_at = pg_catalog.clock_timestamp(),
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $3 AND version = $4 AND status = $5
RETURNING %s`, s.table("executions"), executionColumns(""))
	updated, err := scanExecution(transaction.QueryRow(ctx, update,
		status, resultHash, execution.ID, execution.Version, ExecutionStatusPendingApproval,
	))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Execution{}, versionConflict(operation, "execution", execution.ID, execution.Version)
		}
		return Execution{}, databaseError(operation+" update execution", err)
	}
	return updated, nil
}

func (s *StateStore) recordApprovalStateTransition(ctx context.Context, transaction pgx.Tx, run Run, attempt RunAttempt, execution Execution, approval Approval, record TransitionRecord, kind string) error {
	digest := approval.ContextHash.SHA256()
	payload, err := marshalTransitionPayload(struct {
		RunID                string    `json:"runId"`
		RunAttemptID         string    `json:"runAttemptId"`
		RunAttemptGeneration int64     `json:"runAttemptGeneration"`
		ExecutionID          string    `json:"executionId"`
		ApprovalID           string    `json:"approvalId"`
		ToolName             string    `json:"toolName"`
		Status               string    `json:"status"`
		Decision             string    `json:"decision,omitempty"`
		Nonce                string    `json:"nonce"`
		ContextSHA256        string    `json:"contextSha256"`
		ExpiresAt            time.Time `json:"expiresAt"`
		ApproverID           string    `json:"approverId,omitempty"`
		Version              int64     `json:"version"`
	}{
		run.ID, attempt.ID, attempt.Generation, execution.ID, approval.ID,
		execution.ToolName, approval.Status, approval.Decision, approval.Nonce,
		hex.EncodeToString(digest[:]), approval.ExpiresAt, approval.ApproverID, approval.Version,
	})
	if err != nil {
		return commandError(ErrorInvalidArgument, "record approval transition", "approval", approval.ID, err.Error())
	}
	allocate := fmt.Sprintf(`
UPDATE %s
SET next_event_seq = next_event_seq + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $1
RETURNING next_event_seq - 1`, s.table("runs"))
	var sequence int64
	if err := transaction.QueryRow(ctx, allocate, run.ID).Scan(&sequence); err != nil {
		return databaseError("allocate "+kind+" event sequence", err)
	}
	if err := s.insertTransitionEvent(ctx, transaction, run.ID, sequence, &attempt.ID, &attempt.Generation, record, EventSourceApproval, kind, payload); err != nil {
		return err
	}
	return s.insertOutbox(ctx, transaction, record.OutboxID, kind, run.ID, payload)
}

func (s *StateStore) requireApprovalActor(ctx context.Context, transaction pgx.Tx, operation, workspaceID, actorID string) error {
	query := fmt.Sprintf(`
SELECT wm.role
FROM %s AS w
JOIN %s AS wm ON wm.workspace_id = w.id AND wm.user_id = $2
WHERE w.id = $1 AND w.status = 'active'
FOR SHARE OF w, wm`, s.table("workspaces"), s.table("workspace_members"))
	var role string
	if err := transaction.QueryRow(ctx, query, workspaceID, actorID).Scan(&role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return commandError(ErrorForbidden, operation, "workspace", workspaceID, "actor is not a current member of the active workspace")
		}
		return databaseError(operation+" read workspace membership", err)
	}
	if role == "viewer" {
		return commandError(ErrorForbidden, operation, "workspace", workspaceID, "workspace role cannot decide approvals")
	}
	return nil
}

func (s *StateStore) approvalActorStillAuthorized(ctx context.Context, transaction pgx.Tx, workspaceID, actorID string) (bool, error) {
	query := fmt.Sprintf(`
SELECT wm.role
FROM %s AS w
JOIN %s AS wm ON wm.workspace_id = w.id AND wm.user_id = $2
WHERE w.id = $1 AND w.status = 'active'
FOR SHARE OF w, wm`, s.table("workspaces"), s.table("workspace_members"))
	var role string
	if err := transaction.QueryRow(ctx, query, workspaceID, actorID).Scan(&role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, databaseError("consume approval read current approver membership", err)
	}
	return role != "viewer", nil
}

func validateCreateApproval(command CreateApprovalCommand) error {
	for field, value := range map[string]string{
		"approval_id": command.ApprovalID, "execution_id": command.ExecutionID,
		"run_id": command.RunID, "attempt_id": command.AttemptID, "nonce": command.Nonce,
	} {
		if err := validateUUID(field, value); err != nil {
			return err
		}
	}
	if err := validateBoundedText("holder_id", command.HolderID, 256); err != nil {
		return err
	}
	if err := validateBoundedText("requester_id", command.RequesterID, 256); err != nil {
		return err
	}
	if command.Generation < 1 || command.ExpectedExecutionVersion < 1 {
		return errors.New("generation and expected_execution_version must be positive")
	}
	if command.ExpiresAt.IsZero() {
		return errors.New("expires_at is required")
	}
	return validateTransitionRecord(command.Record)
}

func validateDecideApproval(command DecideApprovalCommand) error {
	for field, value := range map[string]string{
		"approval_id": command.ApprovalID, "workspace_id": command.WorkspaceID,
		"actor_id": command.ActorID, "nonce": command.Nonce,
	} {
		if err := validateUUID(field, value); err != nil {
			return err
		}
	}
	if command.ExpectedApprovalVersion < 1 {
		return errors.New("expected_approval_version must be positive")
	}
	if command.Decision != ApprovalDecisionApprove && command.Decision != ApprovalDecisionDeny {
		return errors.New("decision must be approve or deny")
	}
	if isZeroApprovalContext(command.ExpectedContextHash) {
		return errors.New("expected_context_hash is required")
	}
	return validateTransitionRecord(command.Record)
}

func validateExpireApproval(command ExpireApprovalCommand) error {
	return validateApprovalTerminalCommand(command.ApprovalID, command.Nonce, command.ExpectedContextHash, command.ExpectedApprovalVersion, command.Record)
}

func validateCancelApproval(command CancelApprovalCommand) error {
	return validateApprovalTerminalCommand(command.ApprovalID, command.Nonce, command.ExpectedContextHash, command.ExpectedApprovalVersion, command.Record)
}

func validateApprovalTerminalCommand(approvalID, nonce string, contextHash [32]byte, expectedVersion int64, record TransitionRecord) error {
	if err := validateUUID("approval_id", approvalID); err != nil {
		return err
	}
	if err := validateUUID("nonce", nonce); err != nil {
		return err
	}
	if isZeroApprovalContext(contextHash) {
		return errors.New("expected_context_hash is required")
	}
	if expectedVersion < 1 {
		return errors.New("expected_approval_version must be positive")
	}
	return validateTransitionRecord(record)
}

func validateConsumeApproval(command ConsumeApprovalCommand) error {
	for field, value := range map[string]string{
		"approval_id": command.ApprovalID, "execution_id": command.ExecutionID,
		"run_id": command.RunID, "attempt_id": command.AttemptID, "nonce": command.Nonce,
	} {
		if err := validateUUID(field, value); err != nil {
			return err
		}
	}
	if err := validateBoundedText("holder_id", command.HolderID, 256); err != nil {
		return err
	}
	if command.Generation < 1 || command.ExpectedApprovalVersion < 1 || command.ExpectedExecutionVersion < 1 {
		return errors.New("generation and expected versions must be positive")
	}
	if isZeroApprovalContext(command.ExpectedContextHash) {
		return errors.New("expected_context_hash is required")
	}
	return validateTransitionRecord(command.Record)
}

func validateObserveApproval(command ObserveApprovalCommand) error {
	for field, value := range map[string]string{
		"approval_id": command.ApprovalID, "execution_id": command.ExecutionID,
		"run_id": command.RunID, "attempt_id": command.AttemptID, "nonce": command.Nonce,
	} {
		if err := validateUUID(field, value); err != nil {
			return err
		}
	}
	if err := validateBoundedText("holder_id", command.HolderID, 256); err != nil {
		return err
	}
	if command.Generation < 1 || command.AfterApprovalVersion < 1 {
		return errors.New("generation and after_approval_version must be positive")
	}
	if isZeroApprovalContext(command.ExpectedContextHash) {
		return errors.New("expected_context_hash is required")
	}
	return validateTransitionRecord(command.Record)
}

func isZeroApprovalContext(value [32]byte) bool {
	return value == [32]byte{}
}
