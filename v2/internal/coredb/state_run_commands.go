package coredb

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (s *StateStore) CreateRun(ctx context.Context, command CreateRunCommand) (CreateRunResult, error) {
	const operation = "CreateRun"
	normalizedPolicy, err := normalizeRunExecutorPolicy(command.ExecutorPolicy)
	if err != nil {
		return CreateRunResult{}, commandError(ErrorInvalidArgument, operation, "run", command.RunID, err.Error())
	}
	command.ExecutorPolicy = normalizedPolicy
	if err := validateCreateRun(command); err != nil {
		return CreateRunResult{}, commandError(ErrorInvalidArgument, operation, "run", command.RunID, err.Error())
	}
	return s.createRun(ctx, command, false)
}

// CreateAuthorizedRun is the user-facing CreateRun boundary. Unlike the
// component-level command it obtains the session version while holding the
// session lock and rechecks current workspace membership in the same
// transaction that writes the run, event, outbox, and launch authority.
func (s *StateStore) CreateAuthorizedRun(ctx context.Context, command CreateRunCommand) (CreateRunResult, error) {
	const operation = "CreateAuthorizedRun"
	normalizedPolicy, err := normalizeRunExecutorPolicy(command.ExecutorPolicy)
	if err != nil {
		return CreateRunResult{}, commandError(ErrorInvalidArgument, operation, "run", command.RunID, err.Error())
	}
	command.ExecutorPolicy = normalizedPolicy
	if err := validateCreateRunBase(command); err != nil {
		return CreateRunResult{}, commandError(ErrorInvalidArgument, operation, "run", command.RunID, err.Error())
	}
	return s.createRun(ctx, command, true)
}

func (s *StateStore) createRun(ctx context.Context, command CreateRunCommand, requireUserMembership bool) (CreateRunResult, error) {
	operation := "CreateRun"
	if requireUserMembership {
		operation = "CreateAuthorizedRun"
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (CreateRunResult, error) {
		sessionQuery := fmt.Sprintf(`
SELECT s.workspace_id::text, s.active_run_id::text, s.version, w.status
FROM %s AS s
JOIN %s AS w ON w.id = s.workspace_id
WHERE s.id = $1
FOR UPDATE OF s
FOR SHARE OF w`, s.table("sessions"), s.table("workspaces"))
		var sessionWorkspaceID string
		var activeRunID *string
		var sessionVersion int64
		var workspaceStatus string
		if err := transaction.QueryRow(ctx, sessionQuery, command.SessionID).Scan(
			&sessionWorkspaceID,
			&activeRunID,
			&sessionVersion,
			&workspaceStatus,
		); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return CreateRunResult{}, commandError(ErrorNotFound, operation, "session", command.SessionID, "session does not exist")
			}
			return CreateRunResult{}, databaseError(operation+" lock session", err)
		}
		if sessionWorkspaceID != command.WorkspaceID {
			return CreateRunResult{}, commandError(ErrorNotFound, operation, "session", command.SessionID, "session is not in the requested workspace")
		}
		if requireUserMembership {
			role, err := s.readWorkspaceMemberRole(ctx, transaction, command.WorkspaceID, command.ActorID)
			if err != nil {
				return CreateRunResult{}, err
			}
			if role == "viewer" {
				return CreateRunResult{}, commandError(ErrorForbidden, operation, "workspace", command.WorkspaceID, "workspace role cannot create runs")
			}
			command.ExpectedSessionVersion = sessionVersion
		}

		existingQuery := fmt.Sprintf(`
SELECT %s
FROM %s AS r
WHERE r.workspace_id = $1
  AND r.actor_id = $2
  AND r.session_id = $3
  AND r.idempotency_key = $4`, runColumns("r"), s.table("runs"))
		existing, err := scanRun(transaction.QueryRow(ctx, existingQuery,
			command.WorkspaceID,
			command.ActorID,
			command.SessionID,
			command.IdempotencyKey,
		))
		if err == nil {
			if !bytes.Equal(existing.RequestHash[:], command.RequestHash[:]) {
				return CreateRunResult{}, &StateError{
					Code:         ErrorIdempotencyConflict,
					Operation:    operation,
					Resource:     "run",
					ResourceID:   existing.ID,
					CurrentRunID: existing.ID,
					Message:      "idempotency key was already used with a different request hash",
				}
			}
			// Component callers retry an already frozen command and retain the
			// stronger launch-authority equality check. A user retry is defined
			// solely by its canonical request hash: current server policy may have
			// changed after the original run was committed, but that must not make
			// recovery of the original run conflict.
			if !requireUserMembership {
				prompt, policy, llmGateway, launchErr := s.readRunLaunchInput(ctx, transaction, operation, existing.ID)
				if launchErr != nil {
					return CreateRunResult{}, launchErr
				}
				if !runLaunchInputMatches(prompt, policy, llmGateway, command) {
					return CreateRunResult{}, &StateError{
						Code:         ErrorIdempotencyConflict,
						Operation:    operation,
						Resource:     "run",
						ResourceID:   existing.ID,
						CurrentRunID: existing.ID,
						Message:      "idempotency key was already used with different launch authority",
					}
				}
			}
			return CreateRunResult{Run: existing, SessionVersion: sessionVersion, Created: false}, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return CreateRunResult{}, databaseError(operation+" read idempotency key", err)
		}

		if activeRunID != nil {
			return CreateRunResult{}, &StateError{
				Code:           ErrorActiveRun,
				Operation:      operation,
				Resource:       "session",
				ResourceID:     command.SessionID,
				CurrentRunID:   *activeRunID,
				CurrentVersion: sessionVersion,
				Message:        "session already has an active run",
			}
		}
		if workspaceStatus != "active" {
			return CreateRunResult{}, commandError(ErrorInvalidState, operation, "workspace", command.WorkspaceID, "workspace is not active")
		}
		if sessionVersion != command.ExpectedSessionVersion {
			return CreateRunResult{}, &StateError{
				Code:           ErrorVersionConflict,
				Operation:      operation,
				Resource:       "session",
				ResourceID:     command.SessionID,
				CurrentVersion: sessionVersion,
				Message:        "session version does not match",
			}
		}

		insertRunQuery := fmt.Sprintf(`
INSERT INTO %s
    (id, workspace_id, session_id, actor_id, status, request_hash,
     idempotency_key, current_attempt_generation, next_event_seq, version)
VALUES
    ($1, $2, $3, $4, $5, $6, $7, 0, 2, 1)
RETURNING %s`, s.table("runs"), runColumns(""))
		run, err := scanRun(transaction.QueryRow(ctx, insertRunQuery,
			command.RunID,
			command.WorkspaceID,
			command.SessionID,
			command.ActorID,
			RunStatusQueued,
			command.RequestHash[:],
			command.IdempotencyKey,
		))
		if err != nil {
			var postgresError *pgconn.PgError
			if pgxErrorAs(err, &postgresError) && postgresError.Code == "23505" {
				return CreateRunResult{}, commandError(ErrorConflict, operation, "run", command.RunID, "run identity is already in use")
			}
			return CreateRunResult{}, databaseError(operation+" insert run", err)
		}
		if command.LLMGateway != (RunLLMGatewayBinding{}) {
			if err := s.requireCreateRunLLMGateway(ctx, transaction, command); err != nil {
				return CreateRunResult{}, err
			}
		}
		if err := s.insertRunLaunchInput(ctx, transaction, command); err != nil {
			return CreateRunResult{}, err
		}

		payload, err := marshalTransitionPayload(struct {
			WorkspaceID string `json:"workspaceId"`
			SessionID   string `json:"sessionId"`
			RunID       string `json:"runId"`
			RunVersion  int64  `json:"runVersion"`
		}{command.WorkspaceID, command.SessionID, command.RunID, run.Version})
		if err != nil {
			return CreateRunResult{}, commandError(ErrorInvalidArgument, operation, "run", command.RunID, err.Error())
		}
		if err := s.insertTransitionEvent(ctx, transaction, command.RunID, 1, nil, nil, command.Record, EventSourceSystem, "run.queued", payload); err != nil {
			return CreateRunResult{}, err
		}
		if err := s.insertOutbox(ctx, transaction, command.Record.OutboxID, "run.queued", command.RunID, payload); err != nil {
			return CreateRunResult{}, err
		}

		updateSessionQuery := fmt.Sprintf(`
UPDATE %s
SET active_run_id = $1,
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $2 AND version = $3 AND active_run_id IS NULL
RETURNING version`, s.table("sessions"))
		var updatedSessionVersion int64
		if err := transaction.QueryRow(ctx, updateSessionQuery,
			command.RunID,
			command.SessionID,
			command.ExpectedSessionVersion,
		).Scan(&updatedSessionVersion); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return CreateRunResult{}, commandError(ErrorVersionConflict, operation, "session", command.SessionID, "session changed while creating run")
			}
			return CreateRunResult{}, databaseError(operation+" update session", err)
		}

		return CreateRunResult{Run: run, SessionVersion: updatedSessionVersion, Created: true}, nil
	})
}

func validateCreateRun(command CreateRunCommand) error {
	if err := validateCreateRunBase(command); err != nil {
		return err
	}
	if command.ExpectedSessionVersion < 1 {
		return errors.New("expected_session_version must be positive")
	}
	return nil
}

func validateCreateRunBase(command CreateRunCommand) error {
	identifiers := []struct {
		field string
		value string
	}{
		{field: "run_id", value: command.RunID},
		{field: "workspace_id", value: command.WorkspaceID},
		{field: "session_id", value: command.SessionID},
		{field: "actor_id", value: command.ActorID},
	}
	for _, identifier := range identifiers {
		if err := validateUUID(identifier.field, identifier.value); err != nil {
			return err
		}
	}
	if err := validateBoundedText("idempotency_key", command.IdempotencyKey, 256); err != nil {
		return err
	}
	if err := validateRunObjectPointer("prompt", command.Prompt); err != nil {
		return err
	}
	if err := validateRunExecutorPolicy(command.ExecutorPolicy); err != nil {
		return err
	}
	if command.LLMGateway != (RunLLMGatewayBinding{}) {
		if err := validateRunLLMGatewayBinding(command.LLMGateway); err != nil {
			return err
		}
		if command.LLMGateway.GrantUserID != command.ActorID {
			return errors.New("LLM gateway grant user must be the run actor")
		}
	}
	return validateTransitionRecord(command.Record)
}

func (s *StateStore) ClaimQueuedRun(ctx context.Context, command ClaimQueuedRunCommand) (ClaimQueuedRunResult, error) {
	const operation = "ClaimQueuedRun"
	leaseMilliseconds, err := validateClaimQueuedRun(command)
	if err != nil {
		return ClaimQueuedRunResult{}, commandError(ErrorInvalidArgument, operation, "run", command.RunID, err.Error())
	}

	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (ClaimQueuedRunResult, error) {
		runQuery := fmt.Sprintf(`
SELECT %s, s.active_run_id::text
FROM %s AS r
JOIN %s AS s ON s.id = r.session_id
WHERE r.id = $1
FOR UPDATE OF r, s`, runColumns("r"), s.table("runs"), s.table("sessions"))
		row := transaction.QueryRow(ctx, runQuery, command.RunID)
		var run Run
		var requestHash []byte
		var activeRunID *string
		err := row.Scan(
			&run.ID, &run.WorkspaceID, &run.SessionID, &run.ActorID, &run.Status,
			&requestHash, &run.IdempotencyKey, &run.CurrentAttemptGeneration,
			&run.NextEventSeq, &run.Version, &run.CreatedAt, &run.UpdatedAt,
			&activeRunID,
		)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ClaimQueuedRunResult{}, commandError(ErrorNotFound, operation, "run", command.RunID, "run does not exist")
			}
			return ClaimQueuedRunResult{}, databaseError(operation+" lock run", err)
		}
		if len(requestHash) != len(run.RequestHash) {
			return ClaimQueuedRunResult{}, databaseError(operation+" scan run", fmt.Errorf("invalid request hash length %d", len(requestHash)))
		}
		copy(run.RequestHash[:], requestHash)
		if activeRunID == nil || *activeRunID != run.ID {
			return ClaimQueuedRunResult{}, commandError(ErrorInvalidState, operation, "run", run.ID, "run is not the session active run")
		}

		if existing, ok, err := s.existingClaim(ctx, transaction, run, command); err != nil {
			return ClaimQueuedRunResult{}, err
		} else if ok {
			return existing, nil
		}
		if run.Version != command.ExpectedRunVersion {
			return ClaimQueuedRunResult{}, &StateError{
				Code:           ErrorVersionConflict,
				Operation:      operation,
				Resource:       "run",
				ResourceID:     run.ID,
				CurrentVersion: run.Version,
				Message:        "run version does not match",
			}
		}

		reclaimed := false
		newGeneration := run.CurrentAttemptGeneration + 1
		if run.Status == RunStatusStarting {
			if err := s.fenceExpiredPreTurnAttempt(ctx, transaction, run); err != nil {
				return ClaimQueuedRunResult{}, err
			}
			reclaimed = true
		} else if run.Status != RunStatusQueued {
			return ClaimQueuedRunResult{}, &StateError{
				Code:              ErrorInvalidState,
				Operation:         operation,
				Resource:          "run",
				ResourceID:        run.ID,
				CurrentVersion:    run.Version,
				CurrentGeneration: run.CurrentAttemptGeneration,
				Message:           "only queued or expired pre-turn starting runs can be claimed",
			}
		}

		sessionLease, err := s.replaceExpiredSessionLease(ctx, transaction, run, command.HolderID, newGeneration, leaseMilliseconds)
		if err != nil {
			return ClaimQueuedRunResult{}, err
		}
		attempt, attemptLease, err := s.createAttemptAndLease(ctx, transaction, run.ID, command.AttemptID, command.HolderID, newGeneration, leaseMilliseconds)
		if err != nil {
			return ClaimQueuedRunResult{}, err
		}

		updateRunQuery := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    current_attempt_generation = $2,
    next_event_seq = next_event_seq + 1,
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $3 AND version = $4
RETURNING %s`, s.table("runs"), runColumns(""))
		updatedRun, err := scanRun(transaction.QueryRow(ctx, updateRunQuery,
			RunStatusStarting,
			newGeneration,
			run.ID,
			run.Version,
		))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ClaimQueuedRunResult{}, commandError(ErrorVersionConflict, operation, "run", run.ID, "run changed while claiming")
			}
			return ClaimQueuedRunResult{}, databaseError(operation+" update run", err)
		}

		kind := "attempt.leased"
		if reclaimed {
			kind = "attempt.reclaimed"
		}
		payload, err := marshalTransitionPayload(struct {
			RunID              string `json:"runId"`
			RunAttemptID       string `json:"runAttemptId"`
			AttemptGeneration  int64  `json:"runAttemptGeneration"`
			PreviousGeneration int64  `json:"previousGeneration,omitempty"`
		}{run.ID, attempt.ID, newGeneration, newGeneration - 1})
		if err != nil {
			return ClaimQueuedRunResult{}, commandError(ErrorInvalidArgument, operation, "run", run.ID, err.Error())
		}
		if err := s.insertTransitionEvent(ctx, transaction, run.ID, run.NextEventSeq, &attempt.ID, &newGeneration, command.Record, EventSourceSystem, kind, payload); err != nil {
			return ClaimQueuedRunResult{}, err
		}
		if err := s.insertOutbox(ctx, transaction, command.Record.OutboxID, kind, run.ID, payload); err != nil {
			return ClaimQueuedRunResult{}, err
		}

		return ClaimQueuedRunResult{
			Run:          updatedRun,
			Attempt:      attempt,
			SessionLease: sessionLease,
			AttemptLease: attemptLease,
			Created:      true,
			Reclaimed:    reclaimed,
		}, nil
	})
}

func validateClaimQueuedRun(command ClaimQueuedRunCommand) (int64, error) {
	if err := validateUUID("run_id", command.RunID); err != nil {
		return 0, err
	}
	if err := validateUUID("attempt_id", command.AttemptID); err != nil {
		return 0, err
	}
	if err := validateBoundedText("holder_id", command.HolderID, 256); err != nil {
		return 0, err
	}
	if command.ExpectedRunVersion < 1 {
		return 0, errors.New("expected_run_version must be positive")
	}
	if err := validateTransitionRecord(command.Record); err != nil {
		return 0, err
	}
	return durationMilliseconds("lease_ttl", command.LeaseTTL, MaxLeaseTTL)
}

func (s *StateStore) existingClaim(ctx context.Context, transaction pgx.Tx, run Run, command ClaimQueuedRunCommand) (ClaimQueuedRunResult, bool, error) {
	query := fmt.Sprintf(`
SELECT %s
FROM %s AS a
WHERE a.id = $1
FOR UPDATE`, attemptColumns("a"), s.table("run_attempts"))
	attempt, err := scanAttempt(transaction.QueryRow(ctx, query, command.AttemptID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ClaimQueuedRunResult{}, false, nil
	}
	if err != nil {
		return ClaimQueuedRunResult{}, false, databaseError("ClaimQueuedRun read existing attempt", err)
	}
	if attempt.RunID != run.ID || attempt.Generation != run.CurrentAttemptGeneration || attempt.HolderID != command.HolderID {
		return ClaimQueuedRunResult{}, false, commandError(ErrorConflict, "ClaimQueuedRun", "attempt", command.AttemptID, "attempt identity is already in use")
	}
	if !validAttemptEventStatus(attempt.Status) {
		return ClaimQueuedRunResult{}, false, commandError(ErrorLeaseLost, "ClaimQueuedRun", "attempt", command.AttemptID, "existing attempt is no longer live")
	}

	sessionLease, sessionLive, err := s.readSessionLease(ctx, transaction, run.SessionID)
	if err != nil {
		if HasStateErrorCode(err, ErrorNotFound) {
			return ClaimQueuedRunResult{}, false, commandError(ErrorLeaseLost, "ClaimQueuedRun", "attempt", command.AttemptID, "existing session lease is missing")
		}
		return ClaimQueuedRunResult{}, false, err
	}
	attemptLease, attemptLive, err := s.readAttemptLease(ctx, transaction, attempt.ID)
	if err != nil {
		if HasStateErrorCode(err, ErrorNotFound) {
			return ClaimQueuedRunResult{}, false, commandError(ErrorLeaseLost, "ClaimQueuedRun", "attempt", command.AttemptID, "existing attempt lease is missing")
		}
		return ClaimQueuedRunResult{}, false, err
	}
	if !sessionLive || !attemptLive || sessionLease.HolderID != command.HolderID || attemptLease.HolderID != command.HolderID || sessionLease.Generation != attempt.Generation || attemptLease.Generation != attempt.Generation {
		return ClaimQueuedRunResult{}, false, commandError(ErrorLeaseLost, "ClaimQueuedRun", "attempt", command.AttemptID, "existing claim lease expired or was fenced")
	}
	return ClaimQueuedRunResult{
		Run:          run,
		Attempt:      attempt,
		SessionLease: sessionLease,
		AttemptLease: attemptLease,
		Created:      false,
	}, true, nil
}

func (s *StateStore) fenceExpiredPreTurnAttempt(ctx context.Context, transaction pgx.Tx, run Run) error {
	query := fmt.Sprintf(`
SELECT %s
FROM %s AS a
WHERE a.run_id = $1 AND a.generation = $2
FOR UPDATE`, attemptColumns("a"), s.table("run_attempts"))
	attempt, err := scanAttempt(transaction.QueryRow(ctx, query, run.ID, run.CurrentAttemptGeneration))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return commandError(ErrorInvalidState, "ClaimQueuedRun", "run", run.ID, "current attempt is missing")
		}
		return databaseError("ClaimQueuedRun lock current attempt", err)
	}
	if attempt.TurnStartedAt != nil || (attempt.Status != AttemptStatusLeased && attempt.Status != AttemptStatusStarting) {
		return commandError(ErrorInvalidState, "ClaimQueuedRun", "attempt", attempt.ID, "attempt crossed the turn acceptance boundary")
	}
	sessionLease, sessionLive, err := s.readSessionLease(ctx, transaction, run.SessionID)
	if err != nil {
		return err
	}
	attemptLease, attemptLive, err := s.readAttemptLease(ctx, transaction, attempt.ID)
	if err != nil {
		return err
	}
	if sessionLive || attemptLive {
		return &StateError{
			Code:              ErrorLeaseHeld,
			Operation:         "ClaimQueuedRun",
			Resource:          "attempt",
			ResourceID:        attempt.ID,
			CurrentGeneration: attempt.Generation,
			Message:           "current pre-turn attempt still has a live lease",
		}
	}
	if sessionLease.Generation != attempt.Generation || attemptLease.Generation != attempt.Generation {
		return commandError(ErrorInvalidState, "ClaimQueuedRun", "attempt", attempt.ID, "lease generation does not match current attempt")
	}
	update := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $2 AND version = $3`, s.table("run_attempts"))
	if _, err := transaction.Exec(ctx, update, AttemptStatusFenced, attempt.ID, attempt.Version); err != nil {
		return databaseError("ClaimQueuedRun fence expired attempt", err)
	}
	return nil
}

func (s *StateStore) replaceExpiredSessionLease(ctx context.Context, transaction pgx.Tx, run Run, holderID string, generation, leaseMilliseconds int64) (Lease, error) {
	lease, live, err := s.readSessionLease(ctx, transaction, run.SessionID)
	if err != nil && !HasStateErrorCode(err, ErrorNotFound) {
		return Lease{}, err
	}
	if err == nil && live {
		return Lease{}, &StateError{
			Code:              ErrorLeaseHeld,
			Operation:         "ClaimQueuedRun",
			Resource:          "session",
			ResourceID:        run.SessionID,
			CurrentGeneration: lease.Generation,
			Message:           "session lease is still live",
		}
	}

	if HasStateErrorCode(err, ErrorNotFound) {
		query := fmt.Sprintf(`
INSERT INTO %s
    (session_id, run_id, holder_id, generation, expires_at)
VALUES
    ($1, $2, $3, $4, pg_catalog.clock_timestamp() + ($5::bigint * interval '1 millisecond'))
RETURNING holder_id, generation, expires_at, acquired_at, renewed_at`, s.table("session_leases"))
		inserted, err := scanLease(transaction.QueryRow(ctx, query, run.SessionID, run.ID, holderID, generation, leaseMilliseconds))
		if err != nil {
			return Lease{}, databaseError("ClaimQueuedRun insert session lease", err)
		}
		return inserted, nil
	}

	query := fmt.Sprintf(`
UPDATE %s
SET run_id = $1,
    holder_id = $2,
    generation = $3,
    expires_at = pg_catalog.clock_timestamp() + ($4::bigint * interval '1 millisecond'),
    acquired_at = pg_catalog.clock_timestamp(),
    renewed_at = pg_catalog.clock_timestamp()
WHERE session_id = $5 AND expires_at <= pg_catalog.clock_timestamp()
RETURNING holder_id, generation, expires_at, acquired_at, renewed_at`, s.table("session_leases"))
	updated, err := scanLease(transaction.QueryRow(ctx, query, run.ID, holderID, generation, leaseMilliseconds, run.SessionID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Lease{}, commandError(ErrorLeaseHeld, "ClaimQueuedRun", "session", run.SessionID, "session lease became live")
		}
		return Lease{}, databaseError("ClaimQueuedRun replace session lease", err)
	}
	return updated, nil
}

func (s *StateStore) createAttemptAndLease(ctx context.Context, transaction pgx.Tx, runID, attemptID, holderID string, generation, leaseMilliseconds int64) (RunAttempt, Lease, error) {
	insertAttempt := fmt.Sprintf(`
INSERT INTO %s (id, run_id, generation, status, holder_id)
VALUES ($1, $2, $3, $4, $5)
RETURNING %s`, s.table("run_attempts"), attemptColumns(""))
	attempt, err := scanAttempt(transaction.QueryRow(ctx, insertAttempt, attemptID, runID, generation, AttemptStatusLeased, holderID))
	if err != nil {
		var postgresError *pgconn.PgError
		if pgxErrorAs(err, &postgresError) && postgresError.Code == "23505" {
			return RunAttempt{}, Lease{}, commandError(ErrorConflict, "ClaimQueuedRun", "attempt", attemptID, "attempt identity or generation is already in use")
		}
		return RunAttempt{}, Lease{}, databaseError("ClaimQueuedRun insert attempt", err)
	}
	insertLease := fmt.Sprintf(`
INSERT INTO %s
    (run_attempt_id, holder_id, generation, expires_at)
VALUES
    ($1, $2, $3, pg_catalog.clock_timestamp() + ($4::bigint * interval '1 millisecond'))
RETURNING holder_id, generation, expires_at, acquired_at, renewed_at`, s.table("attempt_leases"))
	lease, err := scanLease(transaction.QueryRow(ctx, insertLease, attemptID, holderID, generation, leaseMilliseconds))
	if err != nil {
		return RunAttempt{}, Lease{}, databaseError("ClaimQueuedRun insert attempt lease", err)
	}
	return attempt, lease, nil
}

func (s *StateStore) readSessionLease(ctx context.Context, transaction pgx.Tx, sessionID string) (Lease, bool, error) {
	query := fmt.Sprintf(`
SELECT holder_id, generation, expires_at, acquired_at, renewed_at,
       expires_at > pg_catalog.clock_timestamp()
FROM %s
WHERE session_id = $1
FOR UPDATE`, s.table("session_leases"))
	var lease Lease
	var live bool
	err := transaction.QueryRow(ctx, query, sessionID).Scan(&lease.HolderID, &lease.Generation, &lease.ExpiresAt, &lease.AcquiredAt, &lease.RenewedAt, &live)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Lease{}, false, commandError(ErrorNotFound, "read session lease", "session", sessionID, "session lease does not exist")
		}
		return Lease{}, false, databaseError("read session lease", err)
	}
	return lease, live, nil
}

func (s *StateStore) readAttemptLease(ctx context.Context, transaction pgx.Tx, attemptID string) (Lease, bool, error) {
	query := fmt.Sprintf(`
SELECT holder_id, generation, expires_at, acquired_at, renewed_at,
       expires_at > pg_catalog.clock_timestamp()
FROM %s
WHERE run_attempt_id = $1
FOR UPDATE`, s.table("attempt_leases"))
	var lease Lease
	var live bool
	err := transaction.QueryRow(ctx, query, attemptID).Scan(&lease.HolderID, &lease.Generation, &lease.ExpiresAt, &lease.AcquiredAt, &lease.RenewedAt, &live)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Lease{}, false, commandError(ErrorNotFound, "read attempt lease", "attempt", attemptID, "attempt lease does not exist")
		}
		return Lease{}, false, databaseError("read attempt lease", err)
	}
	return lease, live, nil
}

func (s *StateStore) MarkTurnAccepted(ctx context.Context, command MarkTurnAcceptedCommand) (MarkTurnAcceptedResult, error) {
	const operation = "MarkTurnAccepted"
	if err := validateMarkTurnAccepted(command); err != nil {
		return MarkTurnAcceptedResult{}, commandError(ErrorInvalidArgument, operation, "attempt", command.AttemptID, err.Error())
	}

	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (MarkTurnAcceptedResult, error) {
		runQuery := fmt.Sprintf("SELECT %s FROM %s AS r WHERE r.id = $1 FOR UPDATE", runColumns("r"), s.table("runs"))
		run, err := scanRun(transaction.QueryRow(ctx, runQuery, command.RunID))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return MarkTurnAcceptedResult{}, commandError(ErrorNotFound, operation, "run", command.RunID, "run does not exist")
			}
			return MarkTurnAcceptedResult{}, databaseError(operation+" lock run", err)
		}
		attemptQuery := fmt.Sprintf("SELECT %s FROM %s AS a WHERE a.id = $1 AND a.run_id = $2 FOR UPDATE", attemptColumns("a"), s.table("run_attempts"))
		attempt, err := scanAttempt(transaction.QueryRow(ctx, attemptQuery, command.AttemptID, command.RunID))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return MarkTurnAcceptedResult{}, commandError(ErrorNotFound, operation, "attempt", command.AttemptID, "attempt does not exist for run")
			}
			return MarkTurnAcceptedResult{}, databaseError(operation+" lock attempt", err)
		}
		if run.CurrentAttemptGeneration != command.Generation || attempt.Generation != command.Generation {
			return MarkTurnAcceptedResult{}, &StateError{
				Code:              ErrorLeaseLost,
				Operation:         operation,
				Resource:          "attempt",
				ResourceID:        command.AttemptID,
				CurrentGeneration: run.CurrentAttemptGeneration,
				Message:           "attempt generation was fenced",
			}
		}
		if attempt.HolderID != command.HolderID {
			return MarkTurnAcceptedResult{}, commandError(ErrorLeaseLost, operation, "attempt", command.AttemptID, "attempt holder was fenced")
		}
		if run.Status == RunStatusRunning && attempt.Status == AttemptStatusRunning && attempt.TurnStartedAt != nil {
			return MarkTurnAcceptedResult{Run: run, Attempt: attempt, Changed: false}, nil
		}
		if run.Version != command.ExpectedRunVersion {
			return MarkTurnAcceptedResult{}, &StateError{Code: ErrorVersionConflict, Operation: operation, Resource: "run", ResourceID: run.ID, CurrentVersion: run.Version, Message: "run version does not match"}
		}
		if attempt.Version != command.ExpectedAttemptVersion {
			return MarkTurnAcceptedResult{}, &StateError{Code: ErrorVersionConflict, Operation: operation, Resource: "attempt", ResourceID: attempt.ID, CurrentVersion: attempt.Version, Message: "attempt version does not match"}
		}
		if run.Status != RunStatusStarting || (attempt.Status != AttemptStatusLeased && attempt.Status != AttemptStatusStarting) || attempt.TurnStartedAt != nil {
			return MarkTurnAcceptedResult{}, commandError(ErrorInvalidState, operation, "attempt", attempt.ID, "run and attempt are not awaiting turn acceptance")
		}
		if err := s.requireLiveLeases(ctx, transaction, run, attempt, command.HolderID, command.Generation); err != nil {
			return MarkTurnAcceptedResult{}, err
		}

		updateAttempt := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    turn_started_at = pg_catalog.clock_timestamp(),
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $2 AND version = $3
RETURNING %s`, s.table("run_attempts"), attemptColumns(""))
		updatedAttempt, err := scanAttempt(transaction.QueryRow(ctx, updateAttempt, AttemptStatusRunning, attempt.ID, attempt.Version))
		if err != nil {
			return MarkTurnAcceptedResult{}, databaseError(operation+" update attempt", err)
		}
		updateRun := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    next_event_seq = next_event_seq + 1,
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $2 AND version = $3
RETURNING %s`, s.table("runs"), runColumns(""))
		updatedRun, err := scanRun(transaction.QueryRow(ctx, updateRun, RunStatusRunning, run.ID, run.Version))
		if err != nil {
			return MarkTurnAcceptedResult{}, databaseError(operation+" update run", err)
		}
		payload, err := marshalTransitionPayload(struct {
			RunID             string `json:"runId"`
			RunAttemptID      string `json:"runAttemptId"`
			AttemptGeneration int64  `json:"runAttemptGeneration"`
		}{run.ID, attempt.ID, attempt.Generation})
		if err != nil {
			return MarkTurnAcceptedResult{}, commandError(ErrorInvalidArgument, operation, "attempt", attempt.ID, err.Error())
		}
		if err := s.insertTransitionEvent(ctx, transaction, run.ID, run.NextEventSeq, &attempt.ID, &attempt.Generation, command.Record, EventSourceSystem, "turn.accepted", payload); err != nil {
			return MarkTurnAcceptedResult{}, err
		}
		if err := s.insertOutbox(ctx, transaction, command.Record.OutboxID, "turn.accepted", run.ID, payload); err != nil {
			return MarkTurnAcceptedResult{}, err
		}
		return MarkTurnAcceptedResult{Run: updatedRun, Attempt: updatedAttempt, Changed: true}, nil
	})
}

func validateMarkTurnAccepted(command MarkTurnAcceptedCommand) error {
	if err := validateUUID("run_id", command.RunID); err != nil {
		return err
	}
	if err := validateUUID("attempt_id", command.AttemptID); err != nil {
		return err
	}
	if err := validateBoundedText("holder_id", command.HolderID, 256); err != nil {
		return err
	}
	if command.Generation < 1 || command.ExpectedRunVersion < 1 || command.ExpectedAttemptVersion < 1 {
		return errors.New("generation and expected versions must be positive")
	}
	return validateTransitionRecord(command.Record)
}

func (s *StateStore) requireLiveLeases(ctx context.Context, transaction pgx.Tx, run Run, attempt RunAttempt, holderID string, generation int64) error {
	sessionLease, sessionLive, err := s.readSessionLease(ctx, transaction, run.SessionID)
	if err != nil {
		if HasStateErrorCode(err, ErrorNotFound) {
			return commandError(ErrorLeaseLost, "validate live leases", "session", run.SessionID, "session lease is missing")
		}
		return err
	}
	attemptLease, attemptLive, err := s.readAttemptLease(ctx, transaction, attempt.ID)
	if err != nil {
		if HasStateErrorCode(err, ErrorNotFound) {
			return commandError(ErrorLeaseLost, "validate live leases", "attempt", attempt.ID, "attempt lease is missing")
		}
		return err
	}
	if !sessionLive || !attemptLive || sessionLease.HolderID != holderID || attemptLease.HolderID != holderID || sessionLease.Generation != generation || attemptLease.Generation != generation {
		return &StateError{Code: ErrorLeaseLost, Operation: "validate live leases", Resource: "attempt", ResourceID: attempt.ID, CurrentGeneration: generation, Message: "lease expired, changed holder, or changed generation"}
	}
	return nil
}

func (s *StateStore) RenewSessionLease(ctx context.Context, command RenewSessionLeaseCommand) (Lease, error) {
	const operation = "RenewSessionLease"
	leaseMilliseconds, err := validateRenewSessionLease(command)
	if err != nil {
		return Lease{}, commandError(ErrorInvalidArgument, operation, "session", command.SessionID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (Lease, error) {
		// All live-attempt commands lock run state before lease rows. Preserve
		// that global order so approval observation cannot deadlock renewal.
		if _, err := s.lockRun(ctx, transaction, operation, command.RunID); err != nil {
			if HasStateErrorCode(err, ErrorNotFound) {
				return Lease{}, commandError(ErrorLeaseLost, operation, "session", command.SessionID, "lease no longer owns a live run")
			}
			return Lease{}, err
		}
		return s.renewSessionLease(ctx, transaction, operation, command.SessionID, command.RunID, command.HolderID, command.Generation, leaseMilliseconds)
	})
}

func validateRenewSessionLease(command RenewSessionLeaseCommand) (int64, error) {
	if err := validateUUID("session_id", command.SessionID); err != nil {
		return 0, err
	}
	if err := validateUUID("run_id", command.RunID); err != nil {
		return 0, err
	}
	if err := validateBoundedText("holder_id", command.HolderID, 256); err != nil {
		return 0, err
	}
	if command.Generation < 1 {
		return 0, errors.New("generation must be positive")
	}
	return durationMilliseconds("lease_ttl", command.LeaseTTL, MaxLeaseTTL)
}

func (s *StateStore) RenewAttemptLease(ctx context.Context, command RenewAttemptLeaseCommand) (Lease, error) {
	const operation = "RenewAttemptLease"
	leaseMilliseconds, err := validateRenewAttemptLease(command)
	if err != nil {
		return Lease{}, commandError(ErrorInvalidArgument, operation, "attempt", command.AttemptID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (Lease, error) {
		if _, _, err := s.lockRunAttempt(ctx, transaction, operation, command.RunID, command.AttemptID); err != nil {
			if HasStateErrorCode(err, ErrorNotFound) {
				return Lease{}, commandError(ErrorLeaseLost, operation, "attempt", command.AttemptID, "lease no longer owns a live run attempt")
			}
			return Lease{}, err
		}
		return s.renewAttemptLease(ctx, transaction, operation, command.RunID, command.AttemptID, command.HolderID, command.Generation, leaseMilliseconds)
	})
}

func validateRenewAttemptLease(command RenewAttemptLeaseCommand) (int64, error) {
	if err := validateUUID("run_id", command.RunID); err != nil {
		return 0, err
	}
	if err := validateUUID("attempt_id", command.AttemptID); err != nil {
		return 0, err
	}
	if err := validateBoundedText("holder_id", command.HolderID, 256); err != nil {
		return 0, err
	}
	if command.Generation < 1 {
		return 0, errors.New("generation must be positive")
	}
	return durationMilliseconds("lease_ttl", command.LeaseTTL, MaxLeaseTTL)
}

// RenewRunAttemptLeases extends both leases in one transaction. It follows the
// global run -> attempt -> session lease -> attempt lease lock order used by
// execution and approval commands. Each UPDATE checks the other lease and the
// live run/attempt ownership tuple; if either check fails, the transaction
// rolls back the other renewal.
func (s *StateStore) RenewRunAttemptLeases(ctx context.Context, command RenewRunAttemptLeasesCommand) (RenewRunAttemptLeasesResult, error) {
	const operation = "RenewRunAttemptLeases"
	leaseMilliseconds, err := validateRenewRunAttemptLeases(command)
	if err != nil {
		return RenewRunAttemptLeasesResult{}, commandError(ErrorInvalidArgument, operation, "attempt", command.AttemptID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (RenewRunAttemptLeasesResult, error) {
		run, attempt, err := s.lockRunAttempt(ctx, transaction, operation, command.RunID, command.AttemptID)
		if err != nil {
			if HasStateErrorCode(err, ErrorNotFound) {
				return RenewRunAttemptLeasesResult{}, commandError(ErrorLeaseLost, operation, "attempt", command.AttemptID, "lease pair no longer owns a live run attempt")
			}
			return RenewRunAttemptLeasesResult{}, err
		}
		sessionLease, err := s.renewSessionLease(ctx, transaction, operation, command.SessionID, command.RunID, command.HolderID, command.Generation, leaseMilliseconds)
		if err != nil {
			return RenewRunAttemptLeasesResult{}, err
		}
		attemptLease, err := s.renewAttemptLease(ctx, transaction, operation, command.RunID, command.AttemptID, command.HolderID, command.Generation, leaseMilliseconds)
		if err != nil {
			return RenewRunAttemptLeasesResult{}, err
		}
		return RenewRunAttemptLeasesResult{
			Run: run, Attempt: attempt, SessionLease: sessionLease, AttemptLease: attemptLease,
		}, nil
	})
}

func validateRenewRunAttemptLeases(command RenewRunAttemptLeasesCommand) (int64, error) {
	if err := validateUUID("session_id", command.SessionID); err != nil {
		return 0, err
	}
	if err := validateUUID("run_id", command.RunID); err != nil {
		return 0, err
	}
	if err := validateUUID("attempt_id", command.AttemptID); err != nil {
		return 0, err
	}
	if err := validateBoundedText("holder_id", command.HolderID, 256); err != nil {
		return 0, err
	}
	if command.Generation < 1 {
		return 0, errors.New("generation must be positive")
	}
	return durationMilliseconds("lease_ttl", command.LeaseTTL, MaxLeaseTTL)
}

func (s *StateStore) renewSessionLease(ctx context.Context, transaction pgx.Tx, operation, sessionID, runID, holderID string, generation, leaseMilliseconds int64) (Lease, error) {
	query := fmt.Sprintf(`
UPDATE %s AS l
SET expires_at = pg_catalog.clock_timestamp() + ($1::bigint * interval '1 millisecond'),
    renewed_at = pg_catalog.clock_timestamp()
FROM %s AS r, %s AS s, %s AS a, %s AS al
WHERE l.session_id = $2
  AND l.run_id = $3
  AND l.holder_id = $4
  AND l.generation = $5
  AND l.expires_at > pg_catalog.clock_timestamp()
  AND r.id = l.run_id
  AND r.current_attempt_generation = l.generation
  AND r.status IN ('starting', 'running', 'finalizing', 'cancelling')
  AND s.id = l.session_id
  AND s.active_run_id = l.run_id
  AND a.run_id = r.id
  AND a.generation = l.generation
  AND a.status IN ('leased', 'starting', 'running', 'finalizing')
  AND al.run_attempt_id = a.id
  AND al.holder_id = l.holder_id
  AND al.generation = l.generation
  AND al.expires_at > pg_catalog.clock_timestamp()
RETURNING l.holder_id, l.generation, l.expires_at, l.acquired_at, l.renewed_at`, s.table("session_leases"), s.table("runs"), s.table("sessions"), s.table("run_attempts"), s.table("attempt_leases"))
	lease, err := scanLease(transaction.QueryRow(ctx, query, leaseMilliseconds, sessionID, runID, holderID, generation))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Lease{}, commandError(ErrorLeaseLost, operation, "session", sessionID, "lease expired, was fenced, or no longer owns the active run")
		}
		return Lease{}, databaseError(operation+" renew session lease", err)
	}
	return lease, nil
}

func (s *StateStore) renewAttemptLease(ctx context.Context, transaction pgx.Tx, operation, runID, attemptID, holderID string, generation, leaseMilliseconds int64) (Lease, error) {
	query := fmt.Sprintf(`
UPDATE %s AS l
SET expires_at = pg_catalog.clock_timestamp() + ($1::bigint * interval '1 millisecond'),
    renewed_at = pg_catalog.clock_timestamp()
FROM %s AS a, %s AS r, %s AS sl, %s AS s
WHERE l.run_attempt_id = $2
  AND l.holder_id = $3
  AND l.generation = $4
  AND l.expires_at > pg_catalog.clock_timestamp()
  AND a.id = l.run_attempt_id
  AND a.run_id = $5
  AND a.generation = l.generation
  AND a.status IN ('leased', 'starting', 'running', 'finalizing')
  AND r.id = a.run_id
  AND r.current_attempt_generation = a.generation
  AND r.status IN ('starting', 'running', 'finalizing', 'cancelling')
  AND sl.session_id = r.session_id
  AND sl.run_id = r.id
  AND sl.holder_id = l.holder_id
  AND sl.generation = l.generation
  AND sl.expires_at > pg_catalog.clock_timestamp()
  AND s.id = r.session_id
  AND s.active_run_id = r.id
RETURNING l.holder_id, l.generation, l.expires_at, l.acquired_at, l.renewed_at`, s.table("attempt_leases"), s.table("run_attempts"), s.table("runs"), s.table("session_leases"), s.table("sessions"))
	lease, err := scanLease(transaction.QueryRow(ctx, query, leaseMilliseconds, attemptID, holderID, generation, runID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Lease{}, commandError(ErrorLeaseLost, operation, "attempt", attemptID, "lease expired, was fenced, or attempt is no longer live")
		}
		return Lease{}, databaseError(operation+" renew attempt lease", err)
	}
	return lease, nil
}
