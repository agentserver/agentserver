package coredb

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

const (
	cancelReasonUser     = "cancelled"
	cancelCodeUser       = "user_cancelled"
	cancelMessage        = "the run was cancelled by a workspace member"
	abandonReasonStartup = "startup_failed"
	abandonCodeStartup   = "attempt_startup_failed"
	abandonMessage       = "the attempt stopped before accepting a turn and will be retried"

	AbandonDispositionRequeued  = "requeued"
	AbandonDispositionCancelled = "cancelled"
)

// CancelRun is the user-authorized run cancellation boundary. A run without a
// holder can become terminal in this transaction. Once an attempt exists, the
// command only records cancelling; the exact holder must subsequently prove
// bounded workload cleanup through InterruptAttempt or the atomic pre-turn
// AbandonAttempt handoff.
func (s *StateStore) CancelRun(ctx context.Context, command CancelRunCommand) (CancelRunResult, error) {
	const operation = "CancelRun"
	if err := validateCancelRun(command); err != nil {
		return CancelRunResult{}, commandError(ErrorInvalidArgument, operation, "run", command.RunID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (CancelRunResult, error) {
		run, err := s.lockRun(ctx, transaction, operation, command.RunID)
		if err != nil {
			return CancelRunResult{}, err
		}
		if run.WorkspaceID != command.WorkspaceID {
			return CancelRunResult{}, commandError(ErrorNotFound, operation, "run", run.ID, "run does not exist in the requested workspace")
		}
		role, err := s.readCancellationMemberRole(ctx, transaction, command.WorkspaceID, command.ActorID)
		if err != nil {
			return CancelRunResult{}, err
		}
		if role == "viewer" {
			return CancelRunResult{}, commandError(ErrorForbidden, operation, "workspace", command.WorkspaceID, "workspace role cannot cancel runs")
		}

		activeRunID, sessionVersion, err := s.lockCancellationSession(ctx, transaction, operation, run.SessionID)
		if err != nil {
			return CancelRunResult{}, err
		}
		if terminalCancellationRunStatus(run.Status) {
			if activeRunID != nil && *activeRunID == run.ID {
				return CancelRunResult{}, databaseError(operation+" validate terminal session", errors.New("terminal run remains active in its session"))
			}
			return CancelRunResult{Run: run, SessionVersion: sessionVersion, Changed: false}, nil
		}
		if activeRunID == nil || *activeRunID != run.ID {
			return CancelRunResult{}, commandError(ErrorInvalidState, operation, "run", run.ID, "run is not the session active run")
		}
		if run.Status == RunStatusCancelling {
			return CancelRunResult{Run: run, SessionVersion: sessionVersion, Changed: false}, nil
		}

		status := RunStatusCancelling
		terminal := false
		if run.Status == RunStatusQueued {
			status = RunStatusCancelled
			terminal = true
		} else if run.Status != RunStatusStarting && run.Status != RunStatusRunning && run.Status != RunStatusFinalizing {
			return CancelRunResult{}, commandError(ErrorInvalidState, operation, "run", run.ID, "run cannot enter cancellation from its current state")
		}

		updateRun := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    next_event_seq = next_event_seq + 1,
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $2 AND version = $3
RETURNING %s`, s.table("runs"), runColumns(""))
		updated, err := scanRun(transaction.QueryRow(ctx, updateRun, status, run.ID, run.Version))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return CancelRunResult{}, versionConflict(operation, "run", run.ID, run.Version)
			}
			return CancelRunResult{}, databaseError(operation+" update run", err)
		}

		kind := "run.cancelling"
		payload, err := marshalTransitionPayload(struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}{cancelCodeUser, cancelMessage})
		if err != nil {
			return CancelRunResult{}, commandError(ErrorInvalidArgument, operation, "run", run.ID, err.Error())
		}
		if terminal {
			kind = "run.cancelled"
		}
		if err := s.insertTransitionEvent(ctx, transaction, run.ID, run.NextEventSeq, nil, nil, command.Record, EventSourceSystem, kind, payload); err != nil {
			return CancelRunResult{}, err
		}
		if err := s.insertOutbox(ctx, transaction, command.Record.OutboxID, kind, run.ID, payload); err != nil {
			return CancelRunResult{}, err
		}

		if terminal {
			updateSession := fmt.Sprintf(`
UPDATE %s
SET active_run_id = NULL,
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $1 AND active_run_id = $2 AND version = $3
RETURNING version`, s.table("sessions"))
			if err := transaction.QueryRow(ctx, updateSession, run.SessionID, run.ID, sessionVersion).Scan(&sessionVersion); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return CancelRunResult{}, versionConflict(operation, "session", run.SessionID, sessionVersion)
				}
				return CancelRunResult{}, databaseError(operation+" release session", err)
			}
		}
		return CancelRunResult{Run: updated, SessionVersion: sessionVersion, Changed: true}, nil
	})
}

// InterruptAttempt closes a previously requested user cancellation only after
// the exact live holder has stopped its workload at a canonical-safe boundary.
func (s *StateStore) InterruptAttempt(ctx context.Context, command InterruptAttemptCommand) (InterruptAttemptResult, error) {
	const operation = "InterruptAttempt"
	if err := validateInterruptAttempt(command); err != nil {
		return InterruptAttemptResult{}, commandError(ErrorInvalidArgument, operation, "attempt", command.AttemptID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (InterruptAttemptResult, error) {
		run, attempt, err := s.lockRunAttempt(ctx, transaction, operation, command.RunID, command.AttemptID)
		if err != nil {
			return InterruptAttemptResult{}, err
		}
		if run.CurrentAttemptGeneration != command.Generation || attempt.Generation != command.Generation || attempt.HolderID != command.HolderID {
			return InterruptAttemptResult{}, fencedAttemptError(operation, attempt.ID, run.CurrentAttemptGeneration, "attempt holder or generation was fenced")
		}
		activeRunID, sessionVersion, err := s.lockCancellationSession(ctx, transaction, operation, run.SessionID)
		if err != nil {
			return InterruptAttemptResult{}, err
		}
		if run.Status == RunStatusCancelled && attempt.Status == AttemptStatusInterrupted {
			if activeRunID != nil && *activeRunID == run.ID {
				return InterruptAttemptResult{}, databaseError(operation+" validate terminal session", errors.New("cancelled run remains active in its session"))
			}
			return InterruptAttemptResult{Run: run, Attempt: attempt, SessionVersion: sessionVersion, Changed: false}, nil
		}
		if run.Version != command.ExpectedRunVersion {
			return InterruptAttemptResult{}, versionConflict(operation, "run", run.ID, run.Version)
		}
		if attempt.Version != command.ExpectedAttemptVersion {
			return InterruptAttemptResult{}, versionConflict(operation, "attempt", attempt.ID, attempt.Version)
		}
		if run.Status != RunStatusCancelling || activeRunID == nil || *activeRunID != run.ID {
			return InterruptAttemptResult{}, commandError(ErrorInvalidState, operation, "run", run.ID, "run is not an active cancellation request")
		}
		switch attempt.Status {
		case AttemptStatusLeased, AttemptStatusStarting, AttemptStatusRunning, AttemptStatusFinalizing:
		default:
			return InterruptAttemptResult{}, commandError(ErrorInvalidState, operation, "attempt", attempt.ID, "attempt cannot be interrupted from its current state")
		}
		if err := s.requireLiveLeases(ctx, transaction, run, attempt, command.HolderID, command.Generation); err != nil {
			return InterruptAttemptResult{}, err
		}
		if err := s.requireTerminalExecutions(ctx, transaction, operation, attempt.ID); err != nil {
			return InterruptAttemptResult{}, err
		}

		updateAttempt := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $2 AND version = $3
RETURNING %s`, s.table("run_attempts"), attemptColumns(""))
		updatedAttempt, err := scanAttempt(transaction.QueryRow(ctx, updateAttempt, AttemptStatusInterrupted, attempt.ID, attempt.Version))
		if err != nil {
			return InterruptAttemptResult{}, databaseError(operation+" update attempt", err)
		}

		updateRun := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    next_event_seq = next_event_seq + 1,
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $2 AND version = $3
RETURNING %s`, s.table("runs"), runColumns(""))
		updatedRun, err := scanRun(transaction.QueryRow(ctx, updateRun, RunStatusCancelled, run.ID, run.Version))
		if err != nil {
			return InterruptAttemptResult{}, databaseError(operation+" update run", err)
		}

		payload, err := marshalTransitionPayload(struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}{cancelCodeUser, cancelMessage})
		if err != nil {
			return InterruptAttemptResult{}, commandError(ErrorInvalidArgument, operation, "run", run.ID, err.Error())
		}
		if err := s.insertTransitionEvent(ctx, transaction, run.ID, run.NextEventSeq, &attempt.ID, &attempt.Generation, command.Record, EventSourceSystem, "run.cancelled", payload); err != nil {
			return InterruptAttemptResult{}, err
		}
		if err := s.insertOutbox(ctx, transaction, command.Record.OutboxID, "run.cancelled", run.ID, payload); err != nil {
			return InterruptAttemptResult{}, err
		}

		updateSession := fmt.Sprintf(`
UPDATE %s
SET active_run_id = NULL,
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $1 AND active_run_id = $2 AND version = $3
RETURNING version`, s.table("sessions"))
		if err := transaction.QueryRow(ctx, updateSession, run.SessionID, run.ID, sessionVersion).Scan(&sessionVersion); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return InterruptAttemptResult{}, versionConflict(operation, "session", run.SessionID, sessionVersion)
			}
			return InterruptAttemptResult{}, databaseError(operation+" release session", err)
		}
		if err := s.deleteAttemptLeases(ctx, transaction, operation, run, attempt, command.HolderID, command.Generation); err != nil {
			return InterruptAttemptResult{}, err
		}
		return InterruptAttemptResult{
			Run: updatedRun, Attempt: updatedAttempt, SessionVersion: sessionVersion, Changed: true,
		}, nil
	})
}

// AbandonAttempt atomically hands a stopped pre-turn attempt back to core. It
// deliberately chooses the disposition while holding the run lock: a normal
// startup failure becomes queued with a failed historical attempt, while a
// cancellation that committed before this handoff becomes terminal. This
// removes the otherwise unavoidable observe-then-release race between the
// harness holder and CancelRun.
func (s *StateStore) AbandonAttempt(ctx context.Context, command AbandonAttemptCommand) (AbandonAttemptResult, error) {
	const operation = "AbandonAttempt"
	if err := validateAbandonAttempt(command); err != nil {
		return AbandonAttemptResult{}, commandError(ErrorInvalidArgument, operation, "attempt", command.AttemptID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (AbandonAttemptResult, error) {
		run, attempt, err := s.lockRunAttempt(ctx, transaction, operation, command.RunID, command.AttemptID)
		if err != nil {
			return AbandonAttemptResult{}, err
		}
		if run.CurrentAttemptGeneration != command.Generation || attempt.Generation != command.Generation || attempt.HolderID != command.HolderID {
			return AbandonAttemptResult{}, fencedAttemptError(operation, attempt.ID, run.CurrentAttemptGeneration, "attempt holder or generation was fenced")
		}
		activeRunID, sessionVersion, err := s.lockCancellationSession(ctx, transaction, operation, run.SessionID)
		if err != nil {
			return AbandonAttemptResult{}, err
		}

		// Exact retry after either possible disposition. CancelRun can legally
		// win after a successful requeue, leaving a failed historical attempt
		// under a now-cancelled run; that is also a fully reconciled result.
		if run.Status == RunStatusQueued && attempt.Status == AttemptStatusFailed {
			if activeRunID == nil || *activeRunID != run.ID {
				return AbandonAttemptResult{}, databaseError(operation+" validate requeued session", errors.New("requeued run is not active in its session"))
			}
			return AbandonAttemptResult{
				Run: run, Attempt: attempt, SessionVersion: sessionVersion,
				Disposition: AbandonDispositionRequeued, Changed: false,
			}, nil
		}
		if run.Status == RunStatusCancelled && (attempt.Status == AttemptStatusInterrupted || attempt.Status == AttemptStatusFailed) {
			if activeRunID != nil && *activeRunID == run.ID {
				return AbandonAttemptResult{}, databaseError(operation+" validate cancelled session", errors.New("cancelled run remains active in its session"))
			}
			return AbandonAttemptResult{
				Run: run, Attempt: attempt, SessionVersion: sessionVersion,
				Disposition: AbandonDispositionCancelled, Changed: false,
			}, nil
		}

		if activeRunID == nil || *activeRunID != run.ID {
			return AbandonAttemptResult{}, commandError(ErrorInvalidState, operation, "run", run.ID, "run is not the session active run")
		}
		if attempt.TurnStartedAt != nil || (attempt.Status != AttemptStatusLeased && attempt.Status != AttemptStatusStarting) {
			return AbandonAttemptResult{}, commandError(ErrorInvalidState, operation, "attempt", attempt.ID, "only a stopped pre-turn attempt can be abandoned")
		}
		if run.Status != RunStatusStarting && run.Status != RunStatusCancelling {
			return AbandonAttemptResult{}, commandError(ErrorInvalidState, operation, "run", run.ID, "run cannot accept a pre-turn abandonment from its current state")
		}
		if err := s.requireLiveLeases(ctx, transaction, run, attempt, command.HolderID, command.Generation); err != nil {
			return AbandonAttemptResult{}, err
		}
		if err := s.requireTerminalExecutions(ctx, transaction, operation, attempt.ID); err != nil {
			return AbandonAttemptResult{}, err
		}

		attemptStatus := AttemptStatusFailed
		runStatus := RunStatusQueued
		kind := "attempt.abandoned"
		code := abandonCodeStartup
		message := abandonMessage
		disposition := AbandonDispositionRequeued
		if run.Status == RunStatusCancelling {
			attemptStatus = AttemptStatusInterrupted
			runStatus = RunStatusCancelled
			kind = "run.cancelled"
			code = cancelCodeUser
			message = cancelMessage
			disposition = AbandonDispositionCancelled
		}

		updateAttempt := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $2 AND version = $3
RETURNING %s`, s.table("run_attempts"), attemptColumns(""))
		updatedAttempt, err := scanAttempt(transaction.QueryRow(ctx, updateAttempt, attemptStatus, attempt.ID, attempt.Version))
		if err != nil {
			return AbandonAttemptResult{}, databaseError(operation+" update attempt", err)
		}

		updateRun := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    next_event_seq = next_event_seq + 1,
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $2 AND version = $3
RETURNING %s`, s.table("runs"), runColumns(""))
		updatedRun, err := scanRun(transaction.QueryRow(ctx, updateRun, runStatus, run.ID, run.Version))
		if err != nil {
			return AbandonAttemptResult{}, databaseError(operation+" update run", err)
		}

		payload, err := marshalTransitionPayload(struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}{code, message})
		if err != nil {
			return AbandonAttemptResult{}, commandError(ErrorInvalidArgument, operation, "run", run.ID, err.Error())
		}
		if err := s.insertTransitionEvent(ctx, transaction, run.ID, run.NextEventSeq, &attempt.ID, &attempt.Generation, command.Record, EventSourceSystem, kind, payload); err != nil {
			return AbandonAttemptResult{}, err
		}
		if err := s.insertOutbox(ctx, transaction, command.Record.OutboxID, kind, run.ID, payload); err != nil {
			return AbandonAttemptResult{}, err
		}

		if disposition == AbandonDispositionCancelled {
			updateSession := fmt.Sprintf(`
UPDATE %s
SET active_run_id = NULL,
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $1 AND active_run_id = $2 AND version = $3
RETURNING version`, s.table("sessions"))
			if err := transaction.QueryRow(ctx, updateSession, run.SessionID, run.ID, sessionVersion).Scan(&sessionVersion); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return AbandonAttemptResult{}, versionConflict(operation, "session", run.SessionID, sessionVersion)
				}
				return AbandonAttemptResult{}, databaseError(operation+" release session", err)
			}
		}
		if err := s.deleteAttemptLeases(ctx, transaction, operation, run, attempt, command.HolderID, command.Generation); err != nil {
			return AbandonAttemptResult{}, err
		}
		return AbandonAttemptResult{
			Run: updatedRun, Attempt: updatedAttempt, SessionVersion: sessionVersion,
			Disposition: disposition, Changed: true,
		}, nil
	})
}

func validateCancelRun(command CancelRunCommand) error {
	for field, value := range map[string]string{
		"workspace_id": command.WorkspaceID, "run_id": command.RunID, "actor_id": command.ActorID,
	} {
		if err := validateUUID(field, value); err != nil {
			return err
		}
	}
	return validateTransitionRecord(command.Record)
}

func validateInterruptAttempt(command InterruptAttemptCommand) error {
	if err := validateUUID("run_id", command.RunID); err != nil {
		return err
	}
	if err := validateUUID("attempt_id", command.AttemptID); err != nil {
		return err
	}
	if err := validateBoundedText("holder_id", command.HolderID, 256); err != nil {
		return err
	}
	if command.Generation < 1 || command.Generation >= maxSafeJSONInteger ||
		command.ExpectedRunVersion < 1 || command.ExpectedRunVersion >= maxSafeJSONInteger ||
		command.ExpectedAttemptVersion < 1 || command.ExpectedAttemptVersion >= maxSafeJSONInteger {
		return errors.New("generation and expected versions must be positive JSON-safe integers")
	}
	if command.Reason != cancelReasonUser {
		return errors.New("interrupt reason must be cancelled")
	}
	return validateTransitionRecord(command.Record)
}

func validateAbandonAttempt(command AbandonAttemptCommand) error {
	if err := validateUUID("run_id", command.RunID); err != nil {
		return err
	}
	if err := validateUUID("attempt_id", command.AttemptID); err != nil {
		return err
	}
	if err := validateBoundedText("holder_id", command.HolderID, 256); err != nil {
		return err
	}
	if command.Generation < 1 || command.Generation >= maxSafeJSONInteger {
		return errors.New("generation must be a positive JSON-safe integer")
	}
	if command.Reason != abandonReasonStartup {
		return errors.New("abandon reason must be startup_failed")
	}
	return validateTransitionRecord(command.Record)
}

func (s *StateStore) readCancellationMemberRole(ctx context.Context, transaction pgx.Tx, workspaceID, actorID string) (string, error) {
	query := fmt.Sprintf(`SELECT role FROM %s WHERE workspace_id = $1 AND user_id = $2 FOR SHARE`, s.table("workspace_members"))
	var role string
	if err := transaction.QueryRow(ctx, query, workspaceID, actorID).Scan(&role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", commandError(ErrorForbidden, "CancelRun", "workspace", workspaceID, "actor is not a current workspace member")
		}
		return "", databaseError("CancelRun read workspace membership", err)
	}
	return role, nil
}

func (s *StateStore) lockCancellationSession(ctx context.Context, transaction pgx.Tx, operation, sessionID string) (*string, int64, error) {
	query := fmt.Sprintf(`SELECT active_run_id::text, version FROM %s WHERE id = $1 FOR UPDATE`, s.table("sessions"))
	var activeRunID *string
	var version int64
	if err := transaction.QueryRow(ctx, query, sessionID).Scan(&activeRunID, &version); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, 0, commandError(ErrorNotFound, operation, "session", sessionID, "session does not exist")
		}
		return nil, 0, databaseError(operation+" lock session", err)
	}
	return activeRunID, version, nil
}

func (s *StateStore) deleteAttemptLeases(ctx context.Context, transaction pgx.Tx, operation string, run Run, attempt RunAttempt, holderID string, generation int64) error {
	attemptQuery := fmt.Sprintf(`DELETE FROM %s WHERE run_attempt_id = $1 AND holder_id = $2 AND generation = $3`, s.table("attempt_leases"))
	result, err := transaction.Exec(ctx, attemptQuery, attempt.ID, holderID, generation)
	if err != nil {
		return databaseError(operation+" delete attempt lease", err)
	}
	if result.RowsAffected() != 1 {
		return commandError(ErrorLeaseLost, operation, "attempt", attempt.ID, "attempt lease disappeared while committing interruption")
	}
	sessionQuery := fmt.Sprintf(`DELETE FROM %s WHERE session_id = $1 AND run_id = $2 AND holder_id = $3 AND generation = $4`, s.table("session_leases"))
	result, err = transaction.Exec(ctx, sessionQuery, run.SessionID, run.ID, holderID, generation)
	if err != nil {
		return databaseError(operation+" delete session lease", err)
	}
	if result.RowsAffected() != 1 {
		return commandError(ErrorLeaseLost, operation, "session", run.SessionID, "session lease disappeared while committing interruption")
	}
	return nil
}

func terminalCancellationRunStatus(status string) bool {
	switch status {
	case RunStatusCompleted, RunStatusFailed, RunStatusInterrupted, RunStatusCancelled:
		return true
	default:
		return false
	}
}
