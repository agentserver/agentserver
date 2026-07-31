package coredb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5"
)

const runQueuedOutboxKind = "run.queued"

// RunDispatch is the scheduler-only projection of one claimed run.queued
// outbox row. Other outbox kinds are deliberately unreachable through this
// API so harness-pool cannot consume event-relay work.
type RunDispatch struct {
	ID                 string
	WorkspaceID        string
	SessionID          string
	RunID              string
	EnqueuedRunVersion int64
	CurrentRunVersion  int64
	CurrentRunStatus   string
	ClaimOwner         string
	ClaimGeneration    int
	AvailableAt        time.Time
	LockUntil          time.Time
	CreatedAt          time.Time
}

type ClaimRunDispatchesCommand struct {
	Owner   string
	Limit   int
	LockTTL time.Duration
}

type CompleteRunDispatchCommand struct {
	ID              string
	RunID           string
	Owner           string
	ClaimGeneration int
}

type ReleaseRunDispatchCommand struct {
	ID              string
	RunID           string
	Owner           string
	ClaimGeneration int
	RetryAfter      time.Duration
}

func (s *StateStore) ClaimRunDispatches(ctx context.Context, command ClaimRunDispatchesCommand) ([]RunDispatch, error) {
	const operation = "ClaimRunDispatches"
	lockMilliseconds, err := validateClaimRunDispatches(command)
	if err != nil {
		return nil, commandError(ErrorInvalidArgument, operation, "run_dispatch", "", err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) ([]RunDispatch, error) {
		query := fmt.Sprintf(`
WITH candidates AS (
    SELECT o.id
    FROM %s AS o
    WHERE o.kind = '%s'
      AND o.completed_at IS NULL
      AND o.available_at <= pg_catalog.clock_timestamp()
      AND (o.lock_until IS NULL OR o.lock_until <= pg_catalog.clock_timestamp())
    ORDER BY o.available_at, o.id
    FOR UPDATE OF o SKIP LOCKED
    LIMIT $1
), claimed AS (
    UPDATE %s AS o
    SET lock_owner = $2,
        lock_until = pg_catalog.clock_timestamp() + ($3::bigint * interval '1 millisecond'),
        attempts = attempts + 1
    FROM candidates AS c
    WHERE o.id = c.id
    RETURNING o.id::text, o.aggregate_id::text, o.payload,
              o.available_at, o.lock_owner, o.lock_until, o.attempts, o.created_at
)
SELECT c.id, c.aggregate_id, c.payload, c.available_at, c.lock_owner,
       c.lock_until, c.attempts, c.created_at,
       r.workspace_id::text, r.session_id::text, r.status, r.version
FROM claimed AS c
LEFT JOIN %s AS r ON r.id = c.aggregate_id
ORDER BY c.available_at, c.id`, s.table("outbox"), runQueuedOutboxKind, s.table("outbox"), s.table("runs"))
		rows, err := transaction.Query(ctx, query, command.Limit, command.Owner, lockMilliseconds)
		if err != nil {
			return nil, databaseError(operation, err)
		}
		defer rows.Close()

		dispatches := make([]RunDispatch, 0, command.Limit)
		for rows.Next() {
			var dispatch RunDispatch
			var aggregateID string
			var payload []byte
			var currentWorkspaceID *string
			var currentSessionID *string
			var currentStatus *string
			var currentVersion *int64
			if err := rows.Scan(
				&dispatch.ID,
				&aggregateID,
				&payload,
				&dispatch.AvailableAt,
				&dispatch.ClaimOwner,
				&dispatch.LockUntil,
				&dispatch.ClaimGeneration,
				&dispatch.CreatedAt,
				&currentWorkspaceID,
				&currentSessionID,
				&currentStatus,
				&currentVersion,
			); err != nil {
				return nil, databaseError(operation+" scan", err)
			}
			if currentWorkspaceID == nil || currentSessionID == nil || currentStatus == nil || currentVersion == nil {
				return nil, databaseError(operation+" validate", errors.New("run.queued outbox references a missing run"))
			}
			queued, err := decodeQueuedRunDispatchPayload(payload)
			if err != nil {
				return nil, databaseError(operation+" decode run.queued payload", err)
			}
			if queued.RunID != aggregateID || queued.WorkspaceID != *currentWorkspaceID || queued.SessionID != *currentSessionID || queued.RunVersion > *currentVersion {
				return nil, databaseError(operation+" validate", errors.New("run.queued outbox identity does not match the current run"))
			}
			dispatch.WorkspaceID = queued.WorkspaceID
			dispatch.SessionID = queued.SessionID
			dispatch.RunID = queued.RunID
			dispatch.EnqueuedRunVersion = queued.RunVersion
			dispatch.CurrentRunVersion = *currentVersion
			dispatch.CurrentRunStatus = *currentStatus
			dispatches = append(dispatches, dispatch)
		}
		if err := rows.Err(); err != nil {
			return nil, databaseError(operation+" rows", err)
		}
		return dispatches, nil
	})
}

func validateClaimRunDispatches(command ClaimRunDispatchesCommand) (int64, error) {
	if err := validateBoundedText("owner", command.Owner, 256); err != nil {
		return 0, err
	}
	if command.Limit < 1 || command.Limit > MaxOutboxClaimBatch {
		return 0, fmt.Errorf("limit must be between 1 and %d", MaxOutboxClaimBatch)
	}
	return durationMilliseconds("lock_ttl", command.LockTTL, MaxLeaseTTL)
}

func (s *StateStore) CompleteRunDispatch(ctx context.Context, command CompleteRunDispatchCommand) (bool, error) {
	const operation = "CompleteRunDispatch"
	if err := validateRunDispatchClaimIdentity(command.ID, command.RunID, command.Owner, command.ClaimGeneration); err != nil {
		return false, commandError(ErrorInvalidArgument, operation, "run_dispatch", command.ID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (bool, error) {
		claim, err := s.lockRunDispatchClaim(ctx, transaction, command.ID, command.RunID)
		if err != nil {
			return false, err
		}
		if claim.completed {
			return false, nil
		}
		if !claim.live || claim.owner != command.Owner || claim.generation != command.ClaimGeneration {
			return false, commandError(ErrorOutboxClaimLost, operation, "run_dispatch", command.ID, "claim expired, changed owner, or changed generation")
		}
		if !runDispatchCanComplete(claim.runStatus) {
			return false, &StateError{
				Code:           ErrorInvalidState,
				Operation:      operation,
				Resource:       "run",
				ResourceID:     command.RunID,
				CurrentVersion: claim.runVersion,
				Message:        "run dispatch remains recoverable until turn acceptance or terminal state",
			}
		}
		query := fmt.Sprintf(`
UPDATE %s
SET completed_at = pg_catalog.clock_timestamp(),
    lock_owner = NULL,
    lock_until = NULL
WHERE id = $1 AND completed_at IS NULL`, s.table("outbox"))
		commandTag, err := transaction.Exec(ctx, query, command.ID)
		if err != nil {
			return false, databaseError(operation, err)
		}
		return commandTag.RowsAffected() == 1, nil
	})
}

func runDispatchCanComplete(status string) bool {
	switch status {
	case RunStatusRunning, RunStatusFinalizing, RunStatusCompleted, RunStatusFailed,
		RunStatusInterrupted, RunStatusCancelling, RunStatusCancelled:
		return true
	default:
		return false
	}
}

func (s *StateStore) ReleaseRunDispatch(ctx context.Context, command ReleaseRunDispatchCommand) (bool, error) {
	const operation = "ReleaseRunDispatch"
	if err := validateRunDispatchClaimIdentity(command.ID, command.RunID, command.Owner, command.ClaimGeneration); err != nil {
		return false, commandError(ErrorInvalidArgument, operation, "run_dispatch", command.ID, err.Error())
	}
	if command.RetryAfter < 0 || command.RetryAfter > MaxOutboxRetryDelay {
		return false, commandError(ErrorInvalidArgument, operation, "run_dispatch", command.ID, "retry_after must be between zero and 24h")
	}
	retryMilliseconds := command.RetryAfter.Milliseconds()
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (bool, error) {
		claim, err := s.lockRunDispatchClaim(ctx, transaction, command.ID, command.RunID)
		if err != nil {
			return false, err
		}
		if claim.completed {
			return false, nil
		}
		if !claim.live || claim.owner != command.Owner || claim.generation != command.ClaimGeneration {
			return false, commandError(ErrorOutboxClaimLost, operation, "run_dispatch", command.ID, "claim expired, changed owner, or changed generation")
		}
		query := fmt.Sprintf(`
UPDATE %s
SET available_at = pg_catalog.clock_timestamp() + ($1::bigint * interval '1 millisecond'),
    lock_owner = NULL,
    lock_until = NULL
WHERE id = $2 AND completed_at IS NULL`, s.table("outbox"))
		commandTag, err := transaction.Exec(ctx, query, retryMilliseconds, command.ID)
		if err != nil {
			return false, databaseError(operation, err)
		}
		return commandTag.RowsAffected() == 1, nil
	})
}

type lockedRunDispatchClaim struct {
	owner      string
	generation int
	live       bool
	completed  bool
	runStatus  string
	runVersion int64
}

func (s *StateStore) lockRunDispatchClaim(ctx context.Context, transaction pgx.Tx, dispatchID, runID string) (lockedRunDispatchClaim, error) {
	query := fmt.Sprintf(`
SELECT COALESCE(o.lock_owner, ''::text), o.attempts,
       o.lock_until IS NOT NULL AND o.lock_until > pg_catalog.clock_timestamp(),
       o.completed_at IS NOT NULL,
       r.status, r.version
FROM %s AS o
JOIN %s AS r ON r.id = o.aggregate_id
WHERE o.id = $1
  AND o.kind = '%s'
  AND o.aggregate_id = $2
FOR UPDATE OF o, r`, s.table("outbox"), s.table("runs"), runQueuedOutboxKind)
	var claim lockedRunDispatchClaim
	if err := transaction.QueryRow(ctx, query, dispatchID, runID).Scan(
		&claim.owner,
		&claim.generation,
		&claim.live,
		&claim.completed,
		&claim.runStatus,
		&claim.runVersion,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return lockedRunDispatchClaim{}, commandError(ErrorNotFound, "lock run dispatch claim", "run_dispatch", dispatchID, "run dispatch does not exist")
		}
		return lockedRunDispatchClaim{}, databaseError("lock run dispatch claim", err)
	}
	return claim, nil
}

func validateRunDispatchClaimIdentity(id, runID, owner string, generation int) error {
	if err := validateUUID("id", id); err != nil {
		return err
	}
	if err := validateUUID("run_id", runID); err != nil {
		return err
	}
	if err := validateBoundedText("owner", owner, 256); err != nil {
		return err
	}
	if generation < 1 {
		return errors.New("claim_generation must be positive")
	}
	return nil
}

type queuedRunDispatchPayload struct {
	WorkspaceID string `json:"workspaceId"`
	SessionID   string `json:"sessionId"`
	RunID       string `json:"runId"`
	RunVersion  int64  `json:"runVersion"`
}

func decodeQueuedRunDispatchPayload(raw []byte) (queuedRunDispatchPayload, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var payload queuedRunDispatchPayload
	if err := decoder.Decode(&payload); err != nil {
		return queuedRunDispatchPayload{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return queuedRunDispatchPayload{}, errors.New("additional JSON value")
		}
		return queuedRunDispatchPayload{}, err
	}
	if err := validateUUID("workspace_id", payload.WorkspaceID); err != nil {
		return queuedRunDispatchPayload{}, err
	}
	if err := validateUUID("session_id", payload.SessionID); err != nil {
		return queuedRunDispatchPayload{}, err
	}
	if err := validateUUID("run_id", payload.RunID); err != nil {
		return queuedRunDispatchPayload{}, err
	}
	if payload.RunVersion < 1 {
		return queuedRunDispatchPayload{}, errors.New("run_version must be positive")
	}
	return payload, nil
}
