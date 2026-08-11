package coredb

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (s *StateStore) ReserveManagedSandbox(ctx context.Context, command ReserveManagedSandboxCommand) (ReserveManagedSandboxResult, error) {
	const operation = "ReserveManagedSandbox"
	if err := validateReserveManagedSandbox(command); err != nil {
		return ReserveManagedSandboxResult{}, commandError(ErrorInvalidArgument, operation, "sandbox", command.SandboxID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (ReserveManagedSandboxResult, error) {
		lockKey := command.WorkspaceID + ":" + command.SessionID + ":" + command.EnvironmentID
		if _, err := transaction.Exec(ctx, "SELECT pg_catalog.pg_advisory_xact_lock(pg_catalog.hashtextextended($1, 0))", lockKey); err != nil {
			return ReserveManagedSandboxResult{}, databaseError(operation+" acquire identity lock", err)
		}
		existingQuery := fmt.Sprintf(`
SELECT %s
FROM %s AS sandbox
WHERE sandbox.workspace_id = $1
  AND sandbox.session_id = $2
  AND sandbox.environment_id = $3
  AND (sandbox.desired_state <> 'deleted' OR sandbox.observed_state <> 'deleted')
FOR UPDATE`, managedSandboxColumns("sandbox"), s.table("managed_sandboxes"))
		existing, err := scanManagedSandbox(transaction.QueryRow(ctx, existingQuery, command.WorkspaceID, command.SessionID, command.EnvironmentID))
		if err == nil {
			if !managedSandboxReservationMatches(existing, command) {
				return ReserveManagedSandboxResult{}, commandError(
					ErrorIdempotencyConflict, operation, "sandbox", existing.ID,
					"active managed sandbox was reserved with a different immutable profile",
				)
			}
			return ReserveManagedSandboxResult{Sandbox: existing, Created: false}, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return ReserveManagedSandboxResult{}, databaseError(operation+" read active sandbox", err)
		}

		var sessionStatus string
		sessionQuery := fmt.Sprintf(`
SELECT status
FROM %s
WHERE id = $1 AND workspace_id = $2
FOR SHARE`, s.table("sessions"))
		if err := transaction.QueryRow(ctx, sessionQuery, command.SessionID, command.WorkspaceID).Scan(&sessionStatus); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ReserveManagedSandboxResult{}, commandError(ErrorNotFound, operation, "session", command.SessionID, "session does not exist in workspace")
			}
			return ReserveManagedSandboxResult{}, databaseError(operation+" read session", err)
		}
		if sessionStatus != "active" {
			return ReserveManagedSandboxResult{}, commandError(ErrorInvalidState, operation, "session", command.SessionID, "session is not active")
		}
		generationQuery := fmt.Sprintf(`
SELECT COALESCE(pg_catalog.max(generation), 0) + 1
FROM %s
WHERE workspace_id = $1 AND session_id = $2 AND environment_id = $3`, s.table("managed_sandboxes"))
		var generation int64
		if err := transaction.QueryRow(ctx, generationQuery, command.WorkspaceID, command.SessionID, command.EnvironmentID).Scan(&generation); err != nil {
			return ReserveManagedSandboxResult{}, databaseError(operation+" allocate generation", err)
		}
		insertQuery := fmt.Sprintf(`
INSERT INTO %s
    (id, workspace_id, session_id, environment_id, provider_kind,
     generation, desired_state, observed_state,
     provider_region, provider_psm, provider_session_ref,
     create_idempotency_key, runtime_profile_digest, pack_set_digest,
     requested_ttl_seconds, idle_ttl_seconds, idle_expires_at)
VALUES
    ($1, $2, $3, $4, 'tae', $5, 'ready', 'reserved',
     $6, $7, $8, $9, $10, $11, $12::bigint, $13::bigint,
     pg_catalog.clock_timestamp() + ($13::bigint * interval '1 second'))
RETURNING %s`, s.table("managed_sandboxes"), managedSandboxColumns(""))
		sandbox, err := scanManagedSandbox(transaction.QueryRow(ctx, insertQuery,
			command.SandboxID, command.WorkspaceID, command.SessionID, command.EnvironmentID,
			generation, command.ProviderRegion, command.ProviderPSM, command.ProviderSessionRef,
			command.CreateIdempotencyKey, command.RuntimeProfileDigest[:], command.PackSetDigest[:],
			int64(command.RequestedTTL/time.Second), int64(command.RequestedIdleTTL/time.Second),
		))
		if err != nil {
			var postgresError *pgconn.PgError
			if pgxErrorAs(err, &postgresError) && postgresError.Code == "23505" {
				return ReserveManagedSandboxResult{}, commandError(ErrorConflict, operation, "sandbox", command.SandboxID, "managed sandbox identity is already in use")
			}
			return ReserveManagedSandboxResult{}, databaseError(operation+" insert sandbox", err)
		}
		return ReserveManagedSandboxResult{Sandbox: sandbox, Created: true}, nil
	})
}

func (s *StateStore) GetManagedSandbox(ctx context.Context, sandboxID string, generation int64) (ManagedSandbox, error) {
	const operation = "GetManagedSandbox"
	if err := validateManagedSandboxIdentity(sandboxID, generation); err != nil {
		return ManagedSandbox{}, commandError(ErrorInvalidArgument, operation, "sandbox", sandboxID, err.Error())
	}
	return withStateReadTransaction(ctx, s, operation, func(transaction pgx.Tx) (ManagedSandbox, error) {
		query := fmt.Sprintf(`SELECT %s FROM %s AS sandbox WHERE sandbox.id = $1 AND sandbox.generation = $2`, managedSandboxColumns("sandbox"), s.table("managed_sandboxes"))
		sandbox, err := scanManagedSandbox(transaction.QueryRow(ctx, query, sandboxID, generation))
		if errors.Is(err, pgx.ErrNoRows) {
			return ManagedSandbox{}, commandError(ErrorNotFound, operation, "sandbox", sandboxID, "managed sandbox generation does not exist")
		}
		if err != nil {
			return ManagedSandbox{}, databaseError(operation+" read sandbox", err)
		}
		return sandbox, nil
	})
}

func (s *StateStore) BeginManagedSandboxCreate(ctx context.Context, command BeginManagedSandboxCreateCommand) (ManagedSandbox, bool, error) {
	const operation = "BeginManagedSandboxCreate"
	if err := validateManagedSandboxVersionIdentity(command.SandboxID, command.Generation, command.ExpectedVersion); err != nil {
		return ManagedSandbox{}, false, commandError(ErrorInvalidArgument, operation, "sandbox", command.SandboxID, err.Error())
	}
	type result struct {
		Sandbox ManagedSandbox
		Changed bool
	}
	resolved, err := withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (result, error) {
		sandbox, err := s.lockManagedSandbox(ctx, transaction, operation, command.SandboxID, command.Generation)
		if err != nil {
			return result{}, err
		}
		if sandbox.ObservedState == ManagedSandboxCreating || sandbox.ObservedState == ManagedSandboxReady {
			return result{sandbox, false}, nil
		}
		if sandbox.Version != command.ExpectedVersion {
			return result{}, versionConflict(operation, "sandbox", sandbox.ID, sandbox.Version)
		}
		if sandbox.DesiredState != ManagedSandboxDesiredReady || sandbox.ObservedState != ManagedSandboxReserved {
			return result{}, commandError(ErrorInvalidState, operation, "sandbox", sandbox.ID, "only a reserved desired-ready sandbox can begin create")
		}
		updated, err := s.updateManagedSandboxState(ctx, transaction, sandbox, ManagedSandboxDesiredReady, ManagedSandboxCreating, sandbox.ProviderSessionRef, nil, "", nil)
		if err != nil {
			return result{}, err
		}
		return result{updated, true}, nil
	})
	if err != nil {
		return ManagedSandbox{}, false, err
	}
	return resolved.Sandbox, resolved.Changed, nil
}

// ObserveManagedSandbox submits one provider observation under generation and
// version fencing. It never creates provider work and therefore cannot serve
// as a second execution state machine.
func (s *StateStore) ObserveManagedSandbox(ctx context.Context, command ObserveManagedSandboxCommand) (ManagedSandbox, bool, error) {
	const operation = "ObserveManagedSandbox"
	if err := validateObserveManagedSandbox(command); err != nil {
		return ManagedSandbox{}, false, commandError(ErrorInvalidArgument, operation, "sandbox", command.SandboxID, err.Error())
	}
	type result struct {
		Sandbox ManagedSandbox
		Changed bool
	}
	resolved, err := withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (result, error) {
		sandbox, err := s.lockManagedSandbox(ctx, transaction, operation, command.SandboxID, command.Generation)
		if err != nil {
			return result{}, err
		}
		if managedSandboxObservationMatches(sandbox, command) {
			return result{sandbox, false}, nil
		}
		if sandbox.Version != command.ExpectedVersion {
			return result{}, versionConflict(operation, "sandbox", sandbox.ID, sandbox.Version)
		}
		if err := validateManagedSandboxObservationTransition(sandbox, command); err != nil {
			return result{}, commandError(ErrorInvalidState, operation, "sandbox", sandbox.ID, err.Error())
		}
		if command.ObservedState == ManagedSandboxReady {
			var expiryIsFuture bool
			if err := transaction.QueryRow(ctx, "SELECT $1::timestamptz > pg_catalog.clock_timestamp()", command.ExpiresAt).Scan(&expiryIsFuture); err != nil {
				return result{}, databaseError(operation+" validate provider expiry", err)
			}
			if !expiryIsFuture {
				return result{}, commandError(ErrorInvalidArgument, operation, "sandbox", sandbox.ID, "ready observation expiry must be in the future")
			}
		}
		desired := sandbox.DesiredState
		if command.ObservedState == ManagedSandboxDeleted {
			desired = ManagedSandboxDesiredDeleted
		}
		providerSessionRef, expiresAt := effectiveManagedSandboxObservation(sandbox, command)
		updated, err := s.updateManagedSandboxState(
			ctx, transaction, sandbox, desired, command.ObservedState,
			providerSessionRef, expiresAt, command.ErrorCode, command.ErrorDigest,
		)
		if err != nil {
			return result{}, err
		}
		return result{updated, true}, nil
	})
	if err != nil {
		return ManagedSandbox{}, false, err
	}
	return resolved.Sandbox, resolved.Changed, nil
}

func (s *StateStore) RenewManagedSandboxActivity(ctx context.Context, command RenewManagedSandboxActivityCommand) (ManagedSandbox, error) {
	const operation = "RenewManagedSandboxActivity"
	if err := validateRenewManagedSandboxActivity(command); err != nil {
		return ManagedSandbox{}, commandError(ErrorInvalidArgument, operation, "sandbox", command.SandboxID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (ManagedSandbox, error) {
		run, attempt, err := s.lockRunAttempt(ctx, transaction, operation, command.RunID, command.AttemptID)
		if err != nil {
			return ManagedSandbox{}, err
		}
		if err := requireManagedActivityAttempt(run, attempt, command.AttemptGeneration, command.HolderID); err != nil {
			return ManagedSandbox{}, commandError(ErrorLeaseLost, operation, "attempt", attempt.ID, err.Error())
		}
		if err := s.requireLiveLeases(ctx, transaction, run, attempt, command.HolderID, command.AttemptGeneration); err != nil {
			return ManagedSandbox{}, err
		}
		sandbox, err := s.lockManagedSandbox(ctx, transaction, operation, command.SandboxID, command.Generation)
		if err != nil {
			return ManagedSandbox{}, err
		}
		if run.WorkspaceID != sandbox.WorkspaceID || run.SessionID != sandbox.SessionID {
			return ManagedSandbox{}, commandError(ErrorInvalidArgument, operation, "sandbox", sandbox.ID, "attempt is outside the sandbox session")
		}
		if sandbox.DesiredState != ManagedSandboxDesiredReady || sandbox.ObservedState != ManagedSandboxReady {
			return ManagedSandbox{}, commandError(ErrorInvalidState, operation, "sandbox", sandbox.ID, "sandbox is not ready for activity")
		}
		upsert := fmt.Sprintf(`
INSERT INTO %s
    (sandbox_id, target_generation, run_attempt_id, run_attempt_generation, lease_expires_at)
VALUES ($1, $2, $3, $4, pg_catalog.clock_timestamp() + ($5 * interval '1 millisecond'))
ON CONFLICT (sandbox_id, run_attempt_id, run_attempt_generation) DO UPDATE
SET target_generation = EXCLUDED.target_generation,
    lease_expires_at = EXCLUDED.lease_expires_at,
    released_at = NULL,
    version = %s.version + 1,
    updated_at = pg_catalog.clock_timestamp()`, s.table("managed_sandbox_activities"), s.table("managed_sandbox_activities"))
		if _, err := transaction.Exec(ctx, upsert, sandbox.ID, sandbox.Generation, attempt.ID, attempt.Generation, command.ActivityTTL.Milliseconds()); err != nil {
			return ManagedSandbox{}, databaseError(operation+" upsert activity", err)
		}
		return sandbox, nil
	})
}

func (s *StateStore) ReleaseManagedSandboxActivity(ctx context.Context, command ReleaseManagedSandboxActivityCommand) (ManagedSandbox, bool, error) {
	const operation = "ReleaseManagedSandboxActivity"
	if err := validateReleaseManagedSandboxActivity(command); err != nil {
		return ManagedSandbox{}, false, commandError(ErrorInvalidArgument, operation, "sandbox", command.SandboxID, err.Error())
	}
	type result struct {
		Sandbox ManagedSandbox
		Changed bool
	}
	resolved, err := withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (result, error) {
		run, attempt, err := s.lockRunAttempt(ctx, transaction, operation, command.RunID, command.AttemptID)
		if err != nil {
			return result{}, err
		}
		if err := requireManagedActivityAttempt(run, attempt, command.AttemptGeneration, command.HolderID); err != nil {
			return result{}, commandError(ErrorLeaseLost, operation, "attempt", attempt.ID, err.Error())
		}
		sandbox, err := s.lockManagedSandbox(ctx, transaction, operation, command.SandboxID, command.Generation)
		if err != nil {
			return result{}, err
		}
		if run.WorkspaceID != sandbox.WorkspaceID || run.SessionID != sandbox.SessionID {
			return result{}, commandError(ErrorInvalidArgument, operation, "sandbox", sandbox.ID, "attempt is outside the sandbox session")
		}
		var releasedAt *time.Time
		activityQuery := fmt.Sprintf(`
SELECT released_at
FROM %s
WHERE sandbox_id = $1 AND target_generation = $2
  AND run_attempt_id = $3 AND run_attempt_generation = $4
FOR UPDATE`, s.table("managed_sandbox_activities"))
		if err := transaction.QueryRow(ctx, activityQuery, sandbox.ID, sandbox.Generation, attempt.ID, attempt.Generation).Scan(&releasedAt); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return result{}, commandError(ErrorInvalidState, operation, "sandbox", sandbox.ID, "managed sandbox activity was not acquired")
			}
			return result{}, databaseError(operation+" lock activity", err)
		}
		if releasedAt != nil {
			return result{Sandbox: sandbox, Changed: false}, nil
		}
		release := fmt.Sprintf(`
UPDATE %s
SET released_at = pg_catalog.clock_timestamp(),
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE sandbox_id = $1 AND target_generation = $2
  AND run_attempt_id = $3 AND run_attempt_generation = $4
  AND released_at IS NULL`, s.table("managed_sandbox_activities"))
		if _, err := transaction.Exec(ctx, release, sandbox.ID, sandbox.Generation, attempt.ID, attempt.Generation); err != nil {
			return result{}, databaseError(operation+" release activity", err)
		}
		update := fmt.Sprintf(`
UPDATE %s
SET idle_expires_at = pg_catalog.clock_timestamp() + ($1 * interval '1 millisecond'),
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $2 AND generation = $3
RETURNING %s`, s.table("managed_sandboxes"), managedSandboxColumns(""))
		updated, err := scanManagedSandbox(transaction.QueryRow(ctx, update, command.IdleTTL.Milliseconds(), sandbox.ID, sandbox.Generation))
		if err != nil {
			return result{}, databaseError(operation+" update idle expiry", err)
		}
		return result{Sandbox: updated, Changed: true}, nil
	})
	if err != nil {
		return ManagedSandbox{}, false, err
	}
	return resolved.Sandbox, resolved.Changed, nil
}

func (s *StateStore) BeginManagedSandboxDelete(ctx context.Context, command BeginManagedSandboxDeleteCommand) (ManagedSandbox, bool, error) {
	const operation = "BeginManagedSandboxDelete"
	if err := validateBeginManagedSandboxDelete(command); err != nil {
		return ManagedSandbox{}, false, commandError(ErrorInvalidArgument, operation, "sandbox", command.SandboxID, err.Error())
	}
	type result struct {
		Sandbox ManagedSandbox
		Changed bool
	}
	resolved, err := withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (result, error) {
		sandbox, err := s.lockManagedSandbox(ctx, transaction, operation, command.SandboxID, command.Generation)
		if err != nil {
			return result{}, err
		}
		if sandbox.DesiredState == ManagedSandboxDesiredDeleted && (sandbox.ObservedState == ManagedSandboxDeleting || sandbox.ObservedState == ManagedSandboxDeleted) {
			return result{sandbox, false}, nil
		}
		if sandbox.Version != command.ExpectedVersion {
			return result{}, versionConflict(operation, "sandbox", sandbox.ID, sandbox.Version)
		}
		nextObserved := ManagedSandboxDeleting
		if sandbox.ObservedState == ManagedSandboxReserved && sandbox.ProviderSessionRef == "" {
			nextObserved = ManagedSandboxDeleted
		}
		updated, err := s.updateManagedSandboxState(ctx, transaction, sandbox, ManagedSandboxDesiredDeleted, nextObserved, sandbox.ProviderSessionRef, sandbox.ExpiresAt, "", nil)
		if err != nil {
			return result{}, err
		}
		release := fmt.Sprintf(`
UPDATE %s
SET released_at = COALESCE(released_at, pg_catalog.clock_timestamp()),
    version = CASE WHEN released_at IS NULL THEN version + 1 ELSE version END,
    updated_at = pg_catalog.clock_timestamp()
WHERE sandbox_id = $1 AND target_generation = $2`, s.table("managed_sandbox_activities"))
		if _, err := transaction.Exec(ctx, release, sandbox.ID, sandbox.Generation); err != nil {
			return result{}, databaseError(operation+" release activities", err)
		}
		return result{updated, true}, nil
	})
	if err != nil {
		return ManagedSandbox{}, false, err
	}
	return resolved.Sandbox, resolved.Changed, nil
}

func (s *StateStore) ListManagedSandboxesForReconcile(ctx context.Context, query ListManagedSandboxesForReconcileQuery) ([]ManagedSandbox, error) {
	const operation = "ListManagedSandboxesForReconcile"
	if query.Limit < 1 || query.Limit > 1000 {
		return nil, commandError(ErrorInvalidArgument, operation, "sandbox", "", "limit must be between 1 and 1000")
	}
	return withStateReadTransaction(ctx, s, operation, func(transaction pgx.Tx) ([]ManagedSandbox, error) {
		statement := fmt.Sprintf(`
SELECT %s
FROM %s AS sandbox
WHERE sandbox.observed_state NOT IN ('ready', 'deleted')
   OR sandbox.desired_state <> sandbox.observed_state
   OR (sandbox.observed_state = 'ready' AND sandbox.expires_at <= pg_catalog.clock_timestamp())
   OR (
       sandbox.desired_state = 'ready'
       AND sandbox.idle_expires_at <= pg_catalog.clock_timestamp()
       AND NOT EXISTS (
           SELECT 1
           FROM %s AS activity
           WHERE activity.sandbox_id = sandbox.id
             AND activity.target_generation = sandbox.generation
             AND activity.released_at IS NULL
             AND activity.lease_expires_at > pg_catalog.clock_timestamp()
       )
   )
ORDER BY sandbox.updated_at, sandbox.id
LIMIT $1`, managedSandboxColumns("sandbox"), s.table("managed_sandboxes"), s.table("managed_sandbox_activities"))
		rows, err := transaction.Query(ctx, statement, query.Limit)
		if err != nil {
			return nil, databaseError(operation+" query", err)
		}
		defer rows.Close()
		result := make([]ManagedSandbox, 0)
		for rows.Next() {
			sandbox, err := scanManagedSandbox(rows)
			if err != nil {
				return nil, databaseError(operation+" scan", err)
			}
			result = append(result, sandbox)
		}
		if err := rows.Err(); err != nil {
			return nil, databaseError(operation+" rows", err)
		}
		return result, nil
	})
}

// AuthorizeManagedSandboxOperation is the fail-closed data-plane
// introspection used by sandbox-gateway immediately before a provider call.
// It deliberately returns no provider reference or credential-bearing data.
func (s *StateStore) AuthorizeManagedSandboxOperation(ctx context.Context, query AuthorizeManagedSandboxOperationQuery) (AuthorizedManagedSandboxOperation, error) {
	const operation = "AuthorizeManagedSandboxOperation"
	if err := validateAuthorizeManagedSandboxOperation(query); err != nil {
		return AuthorizedManagedSandboxOperation{}, commandError(ErrorInvalidArgument, operation, "operation", query.OperationID, err.Error())
	}
	return withStateReadTransaction(ctx, s, operation, func(transaction pgx.Tx) (AuthorizedManagedSandboxOperation, error) {
		expectedKind := managedSandboxOperationKind(query.Action)
		statement := fmt.Sprintf(`
WITH authority_time AS MATERIALIZED (
    SELECT pg_catalog.clock_timestamp() AS now
)
SELECT sandbox.id::text,
       sandbox.generation,
       operation.id::text,
       operation.kind,
       authority_time.now
FROM authority_time
JOIN %s AS run
  ON run.id = $3
 AND run.workspace_id = $1
 AND run.session_id = $2
 AND run.status = 'running'
 AND run.current_attempt_generation = $5
JOIN %s AS attempt
  ON attempt.id = $4
 AND attempt.run_id = run.id
 AND attempt.generation = $5
 AND attempt.status = 'running'
 AND attempt.turn_started_at IS NOT NULL
JOIN %s AS session
  ON session.id = run.session_id
 AND session.workspace_id = run.workspace_id
 AND session.active_run_id = run.id
JOIN %s AS session_lease
  ON session_lease.session_id = session.id
 AND session_lease.run_id = run.id
 AND session_lease.holder_id = attempt.holder_id
 AND session_lease.generation = attempt.generation
 AND session_lease.expires_at > authority_time.now
JOIN %s AS attempt_lease
  ON attempt_lease.run_attempt_id = attempt.id
 AND attempt_lease.holder_id = attempt.holder_id
 AND attempt_lease.generation = attempt.generation
 AND attempt_lease.expires_at > authority_time.now
JOIN %s AS execution
  ON execution.id = $6
 AND execution.run_id = run.id
 AND execution.run_attempt_id = attempt.id
 AND execution.run_attempt_generation = attempt.generation
 AND execution.env_id = $11
 AND execution.target_kind = 'tae'
 AND execution.target_id = $9
 AND execution.target_generation = $10
 AND execution.status IN ('dispatching', 'running', 'cancelling')
JOIN %s AS operation
  ON operation.id = $7
 AND operation.execution_id = execution.id
 AND operation.mutation_key = $8
 AND operation.kind = $12
 AND operation.status = 'dispatching'
 AND operation.target_kind = 'tae'
 AND operation.target_id = execution.target_id
 AND operation.target_generation = execution.target_generation
JOIN %s AS sandbox
  ON sandbox.id = execution.target_id
 AND sandbox.generation = execution.target_generation
 AND sandbox.workspace_id = run.workspace_id
 AND sandbox.session_id = run.session_id
 AND sandbox.environment_id = execution.env_id
 AND sandbox.desired_state = 'ready'
 AND sandbox.observed_state = 'ready'
 AND sandbox.expires_at > authority_time.now
JOIN %s AS activity
  ON activity.sandbox_id = sandbox.id
 AND activity.target_generation = sandbox.generation
 AND activity.run_attempt_id = attempt.id
 AND activity.run_attempt_generation = attempt.generation
 AND activity.released_at IS NULL
 AND activity.lease_expires_at > authority_time.now`,
			s.table("runs"), s.table("run_attempts"), s.table("sessions"),
			s.table("session_leases"), s.table("attempt_leases"),
			s.table("executions"), s.table("execution_operations"),
			s.table("managed_sandboxes"), s.table("managed_sandbox_activities"),
		)
		var authorized AuthorizedManagedSandboxOperation
		err := transaction.QueryRow(ctx, statement,
			query.WorkspaceID, query.SessionID, query.RunID, query.AttemptID,
			query.AttemptGeneration, query.ExecutionID, query.OperationID,
			query.MutationKey, query.SandboxID, query.TargetGeneration,
			query.EnvironmentID, expectedKind,
		).Scan(
			&authorized.SandboxID, &authorized.TargetGeneration,
			&authorized.OperationID, &authorized.OperationKind, &authorized.AuthorizedAt,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return AuthorizedManagedSandboxOperation{}, commandError(
				ErrorLeaseLost, operation, "operation", query.OperationID,
				"managed sandbox operation no longer has live dispatch authority",
			)
		}
		if err != nil {
			return AuthorizedManagedSandboxOperation{}, databaseError(operation+" read live operation authority", err)
		}
		return authorized, nil
	})
}

func (s *StateStore) lockManagedSandbox(ctx context.Context, transaction pgx.Tx, operation, sandboxID string, generation int64) (ManagedSandbox, error) {
	query := fmt.Sprintf(`SELECT %s FROM %s AS sandbox WHERE sandbox.id = $1 AND sandbox.generation = $2 FOR UPDATE`, managedSandboxColumns("sandbox"), s.table("managed_sandboxes"))
	sandbox, err := scanManagedSandbox(transaction.QueryRow(ctx, query, sandboxID, generation))
	if errors.Is(err, pgx.ErrNoRows) {
		return ManagedSandbox{}, commandError(ErrorNotFound, operation, "sandbox", sandboxID, "managed sandbox generation does not exist")
	}
	if err != nil {
		return ManagedSandbox{}, databaseError(operation+" lock sandbox", err)
	}
	return sandbox, nil
}

func (s *StateStore) updateManagedSandboxState(
	ctx context.Context,
	transaction pgx.Tx,
	current ManagedSandbox,
	desiredState string,
	observedState string,
	providerSessionRef string,
	expiresAt *time.Time,
	errorCode string,
	errorDigest *[32]byte,
) (ManagedSandbox, error) {
	var errorDigestBytes []byte
	if errorDigest != nil {
		errorDigestBytes = errorDigest[:]
	}
	query := fmt.Sprintf(`
UPDATE %s
SET desired_state = $1,
    observed_state = $2,
    provider_session_ref = NULLIF($3, ''),
    expires_at = $4,
			last_observed_at = CASE WHEN $2 IN ('creating', 'ready', 'failed', 'unknown', 'deleted') THEN pg_catalog.clock_timestamp() ELSE last_observed_at END,
    last_error_code = NULLIF($5, ''),
    last_error_digest = $6,
    deleted_at = CASE WHEN $2 = 'deleted' THEN COALESCE(deleted_at, pg_catalog.clock_timestamp()) ELSE deleted_at END,
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $7 AND generation = $8 AND version = $9
RETURNING %s`, s.table("managed_sandboxes"), managedSandboxColumns(""))
	updated, err := scanManagedSandbox(transaction.QueryRow(ctx, query,
		desiredState, observedState, providerSessionRef, expiresAt,
		errorCode, errorDigestBytes, current.ID, current.Generation, current.Version,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return ManagedSandbox{}, versionConflict("update managed sandbox state", "sandbox", current.ID, current.Version)
	}
	if err != nil {
		return ManagedSandbox{}, databaseError("update managed sandbox state", err)
	}
	return updated, nil
}

func validateReserveManagedSandbox(command ReserveManagedSandboxCommand) error {
	for _, identity := range []struct{ name, value string }{
		{"sandbox_id", command.SandboxID}, {"workspace_id", command.WorkspaceID},
		{"session_id", command.SessionID}, {"environment_id", command.EnvironmentID},
		{"create_idempotency_key", command.CreateIdempotencyKey},
	} {
		if err := validateUUID(identity.name, identity.value); err != nil {
			return err
		}
	}
	for _, value := range []struct {
		name, text string
		maximum    int
	}{
		{"provider_region", command.ProviderRegion, 128},
		{"provider_psm", command.ProviderPSM, 256},
	} {
		if err := validateBoundedText(value.name, value.text, value.maximum); err != nil {
			return err
		}
	}
	if command.ProviderSessionRef != "" {
		if err := validateBoundedText("provider_session_ref", command.ProviderSessionRef, 1024); err != nil {
			return err
		}
	}
	if isZeroDigest(command.RuntimeProfileDigest) || isZeroDigest(command.PackSetDigest) {
		return errors.New("runtime and pack-set digests must not be zero")
	}
	if command.RequestedTTL < MinManagedSandboxTTL || command.RequestedTTL > MaxManagedSandboxTTL || command.RequestedTTL%time.Second != 0 {
		return fmt.Errorf("requested_ttl must be whole seconds between %s and %s", MinManagedSandboxTTL, MaxManagedSandboxTTL)
	}
	if command.RequestedIdleTTL < time.Second || command.RequestedIdleTTL > MaxManagedSandboxTTL || command.RequestedIdleTTL%time.Second != 0 {
		return errors.New("requested_idle_ttl must be whole seconds between 1s and 24h")
	}
	return nil
}

func managedSandboxReservationMatches(sandbox ManagedSandbox, command ReserveManagedSandboxCommand) bool {
	return sandbox.WorkspaceID == command.WorkspaceID && sandbox.SessionID == command.SessionID &&
		sandbox.EnvironmentID == command.EnvironmentID && sandbox.ProviderKind == DispatchTargetTAE &&
		sandbox.ProviderRegion == command.ProviderRegion && sandbox.ProviderPSM == command.ProviderPSM &&
		sandbox.RuntimeProfileDigest == command.RuntimeProfileDigest && sandbox.PackSetDigest == command.PackSetDigest &&
		sandbox.RequestedTTL == command.RequestedTTL && sandbox.IdleTTL == command.RequestedIdleTTL
}

func validateManagedSandboxIdentity(sandboxID string, generation int64) error {
	if err := validateUUID("sandbox_id", sandboxID); err != nil {
		return err
	}
	if generation < 1 {
		return errors.New("generation must be positive")
	}
	return nil
}

func validateManagedSandboxVersionIdentity(sandboxID string, generation, expectedVersion int64) error {
	if err := validateManagedSandboxIdentity(sandboxID, generation); err != nil {
		return err
	}
	if expectedVersion < 1 {
		return errors.New("expected_version must be positive")
	}
	return nil
}

func validateObserveManagedSandbox(command ObserveManagedSandboxCommand) error {
	if err := validateManagedSandboxVersionIdentity(command.SandboxID, command.Generation, command.ExpectedVersion); err != nil {
		return err
	}
	switch command.ObservedState {
	case ManagedSandboxCreating:
		if command.ProviderSessionRef == "" {
			return errors.New("creating observation requires provider_session_ref")
		}
		if command.ExpiresAt != nil || command.ErrorCode != "" || command.ErrorDigest != nil {
			return errors.New("creating observation must not carry expiry or provider error")
		}
	case ManagedSandboxReady:
		if command.ProviderSessionRef == "" || command.ExpiresAt == nil {
			return errors.New("ready observation requires provider_session_ref and expires_at")
		}
		if command.ErrorCode != "" || command.ErrorDigest != nil {
			return errors.New("ready observation must not carry provider error")
		}
	case ManagedSandboxFailed, ManagedSandboxUnknown:
		if command.ErrorCode == "" || command.ErrorDigest == nil {
			return errors.New("failed or unknown observation requires safe error code and digest")
		}
	case ManagedSandboxDeleted:
		if command.ErrorCode != "" || command.ErrorDigest != nil {
			return errors.New("deleted observation must not carry provider error")
		}
	default:
		return errors.New("observed_state must be creating, ready, failed, unknown, or deleted")
	}
	if command.ProviderSessionRef != "" {
		if err := validateBoundedText("provider_session_ref", command.ProviderSessionRef, 1024); err != nil {
			return err
		}
	}
	if command.ErrorCode != "" {
		if err := validateBoundedText("error_code", command.ErrorCode, 128); err != nil {
			return err
		}
	}
	return nil
}

func validateManagedSandboxObservationTransition(sandbox ManagedSandbox, command ObserveManagedSandboxCommand) error {
	switch command.ObservedState {
	case ManagedSandboxCreating:
		if sandbox.DesiredState != ManagedSandboxDesiredReady ||
			(sandbox.ObservedState != ManagedSandboxCreating && sandbox.ObservedState != ManagedSandboxUnknown) {
			return errors.New("creating can only observe a desired-ready creating or unknown sandbox")
		}
		if sandbox.ProviderSessionRef != "" && sandbox.ProviderSessionRef != command.ProviderSessionRef {
			return errors.New("provider session reference changed")
		}
	case ManagedSandboxReady:
		if sandbox.DesiredState != ManagedSandboxDesiredReady || (sandbox.ObservedState != ManagedSandboxCreating && sandbox.ObservedState != ManagedSandboxReady && sandbox.ObservedState != ManagedSandboxUnknown) {
			return errors.New("ready can only observe a desired-ready creating, ready, or unknown sandbox")
		}
		if sandbox.ProviderSessionRef != "" && sandbox.ProviderSessionRef != command.ProviderSessionRef {
			return errors.New("provider session reference changed")
		}
	case ManagedSandboxFailed:
		if sandbox.DesiredState != ManagedSandboxDesiredReady || (sandbox.ObservedState != ManagedSandboxCreating && sandbox.ObservedState != ManagedSandboxReady && sandbox.ObservedState != ManagedSandboxUnknown) {
			return errors.New("failed can only observe a desired-ready creating, ready, or unknown sandbox")
		}
	case ManagedSandboxUnknown:
		if sandbox.ObservedState == ManagedSandboxDeleted {
			return errors.New("deleted sandbox cannot become unknown")
		}
	case ManagedSandboxDeleted:
		if sandbox.DesiredState != ManagedSandboxDesiredDeleted || (sandbox.ObservedState != ManagedSandboxDeleting && sandbox.ObservedState != ManagedSandboxUnknown) {
			return errors.New("deleted can only observe a desired-deleted deleting or unknown sandbox")
		}
	}
	return nil
}

func managedSandboxObservationMatches(sandbox ManagedSandbox, command ObserveManagedSandboxCommand) bool {
	providerSessionRef, expiresAt := effectiveManagedSandboxObservation(sandbox, command)
	if sandbox.ObservedState != command.ObservedState || sandbox.ProviderSessionRef != providerSessionRef || sandbox.LastErrorCode != command.ErrorCode {
		return false
	}
	if !equalOptionalTime(sandbox.ExpiresAt, expiresAt) {
		return false
	}
	if (sandbox.LastErrorDigest == nil) != (command.ErrorDigest == nil) {
		return false
	}
	return sandbox.LastErrorDigest == nil || *sandbox.LastErrorDigest == *command.ErrorDigest
}

func effectiveManagedSandboxObservation(sandbox ManagedSandbox, command ObserveManagedSandboxCommand) (string, *time.Time) {
	providerSessionRef := command.ProviderSessionRef
	expiresAt := command.ExpiresAt
	if command.ObservedState != ManagedSandboxReady {
		if providerSessionRef == "" {
			providerSessionRef = sandbox.ProviderSessionRef
		}
		if expiresAt == nil {
			expiresAt = sandbox.ExpiresAt
		}
	}
	return providerSessionRef, expiresAt
}

func equalOptionalTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func validateRenewManagedSandboxActivity(command RenewManagedSandboxActivityCommand) error {
	if err := validateManagedActivityIdentity(command.SandboxID, command.Generation, command.RunID, command.AttemptID, command.AttemptGeneration, command.HolderID); err != nil {
		return err
	}
	if command.ActivityTTL <= 0 || command.ActivityTTL > MaxManagedActivityTTL || command.ActivityTTL%time.Millisecond != 0 {
		return errors.New("activity_ttl must be positive, at most one hour, and whole milliseconds")
	}
	return nil
}

func validateReleaseManagedSandboxActivity(command ReleaseManagedSandboxActivityCommand) error {
	if err := validateManagedActivityIdentity(command.SandboxID, command.Generation, command.RunID, command.AttemptID, command.AttemptGeneration, command.HolderID); err != nil {
		return err
	}
	if command.IdleTTL <= 0 || command.IdleTTL > MaxManagedSandboxTTL || command.IdleTTL%time.Millisecond != 0 {
		return errors.New("idle_ttl must be positive, at most 24 hours, and whole milliseconds")
	}
	return nil
}

func validateManagedActivityIdentity(sandboxID string, generation int64, runID, attemptID string, attemptGeneration int64, holderID string) error {
	if err := validateManagedSandboxIdentity(sandboxID, generation); err != nil {
		return err
	}
	if err := validateUUID("run_id", runID); err != nil {
		return err
	}
	if err := validateUUID("attempt_id", attemptID); err != nil {
		return err
	}
	if attemptGeneration < 1 {
		return errors.New("attempt_generation must be positive")
	}
	return validateBoundedText("holder_id", holderID, 256)
}

func requireManagedActivityAttempt(run Run, attempt RunAttempt, generation int64, holderID string) error {
	if run.CurrentAttemptGeneration != generation || attempt.Generation != generation || attempt.HolderID != holderID {
		return errors.New("attempt generation or holder was fenced")
	}
	return nil
}

func validateBeginManagedSandboxDelete(command BeginManagedSandboxDeleteCommand) error {
	if err := validateManagedSandboxVersionIdentity(command.SandboxID, command.Generation, command.ExpectedVersion); err != nil {
		return err
	}
	return validateBoundedText("reason", command.Reason, 1024)
}

func validateAuthorizeManagedSandboxOperation(query AuthorizeManagedSandboxOperationQuery) error {
	for _, identity := range []struct{ name, value string }{
		{"workspace_id", query.WorkspaceID}, {"session_id", query.SessionID},
		{"run_id", query.RunID}, {"attempt_id", query.AttemptID},
		{"execution_id", query.ExecutionID}, {"operation_id", query.OperationID},
		{"mutation_key", query.MutationKey}, {"sandbox_id", query.SandboxID},
		{"environment_id", query.EnvironmentID},
	} {
		if err := validateUUID(identity.name, identity.value); err != nil {
			return err
		}
	}
	if query.AttemptGeneration < 1 || query.TargetGeneration < 1 {
		return errors.New("attempt_generation and target_generation must be positive")
	}
	if managedSandboxOperationKind(query.Action) == "" {
		return errors.New("action must be run_command, signal_command, or read_file")
	}
	return nil
}

func managedSandboxOperationKind(action string) string {
	switch action {
	case ManagedSandboxActionRunCommand:
		return "process_start"
	case ManagedSandboxActionSignalCommand:
		return OperationKindTimeoutTerminate
	case ManagedSandboxActionReadFile:
		return "fs_read"
	default:
		return ""
	}
}
