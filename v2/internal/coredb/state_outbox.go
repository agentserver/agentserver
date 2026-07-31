package coredb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
)

func (s *StateStore) ClaimOutbox(ctx context.Context, command ClaimOutboxCommand) ([]OutboxMessage, error) {
	const operation = "ClaimOutbox"
	lockMilliseconds, err := validateClaimOutbox(command)
	if err != nil {
		return nil, commandError(ErrorInvalidArgument, operation, "outbox", "", err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) ([]OutboxMessage, error) {
		query := fmt.Sprintf(`
WITH candidates AS (
    SELECT id
    FROM %s
    WHERE completed_at IS NULL
      AND kind <> '%s'
      AND available_at <= pg_catalog.clock_timestamp()
      AND (lock_until IS NULL OR lock_until <= pg_catalog.clock_timestamp())
    ORDER BY available_at, id
    FOR UPDATE SKIP LOCKED
    LIMIT $1
), claimed AS (
    UPDATE %s AS o
    SET lock_owner = $2,
        lock_until = pg_catalog.clock_timestamp() + ($3::bigint * interval '1 millisecond'),
        attempts = attempts + 1
    FROM candidates AS c
    WHERE o.id = c.id
    RETURNING o.id::text, o.kind, o.aggregate_id::text, o.payload,
              o.available_at, o.lock_owner, o.lock_until, o.attempts, o.created_at
)
SELECT id, kind, aggregate_id, payload, available_at, lock_owner,
       lock_until, attempts, created_at
FROM claimed
ORDER BY available_at, id`, s.table("outbox"), runQueuedOutboxKind, s.table("outbox"))
		rows, err := transaction.Query(ctx, query, command.Limit, command.Owner, lockMilliseconds)
		if err != nil {
			return nil, databaseError(operation, err)
		}
		defer rows.Close()
		messages := make([]OutboxMessage, 0, command.Limit)
		for rows.Next() {
			var message OutboxMessage
			var payload []byte
			if err := rows.Scan(
				&message.ID,
				&message.Kind,
				&message.AggregateID,
				&payload,
				&message.AvailableAt,
				&message.LockOwner,
				&message.LockUntil,
				&message.ClaimGeneration,
				&message.CreatedAt,
			); err != nil {
				return nil, databaseError(operation+" scan", err)
			}
			message.Payload = append(json.RawMessage(nil), payload...)
			messages = append(messages, message)
		}
		if err := rows.Err(); err != nil {
			return nil, databaseError(operation+" rows", err)
		}
		sort.Slice(messages, func(i, j int) bool {
			if messages[i].AvailableAt.Equal(messages[j].AvailableAt) {
				return messages[i].ID < messages[j].ID
			}
			return messages[i].AvailableAt.Before(messages[j].AvailableAt)
		})
		return messages, nil
	})
}

func validateClaimOutbox(command ClaimOutboxCommand) (int64, error) {
	if err := validateBoundedText("owner", command.Owner, 256); err != nil {
		return 0, err
	}
	if command.Limit < 1 || command.Limit > MaxOutboxClaimBatch {
		return 0, fmt.Errorf("limit must be between 1 and %d", MaxOutboxClaimBatch)
	}
	return durationMilliseconds("lock_ttl", command.LockTTL, MaxLeaseTTL)
}

func (s *StateStore) CompleteOutbox(ctx context.Context, command CompleteOutboxCommand) (bool, error) {
	const operation = "CompleteOutbox"
	if err := validateOutboxClaimIdentity(command.ID, command.Owner, command.ClaimGeneration); err != nil {
		return false, commandError(ErrorInvalidArgument, operation, "outbox", command.ID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (bool, error) {
		claim, err := s.lockOutboxClaim(ctx, transaction, command.ID)
		if err != nil {
			return false, err
		}
		if claim.completed {
			return false, nil
		}
		if !claim.live || claim.owner != command.Owner || claim.generation != command.ClaimGeneration {
			return false, commandError(ErrorOutboxClaimLost, operation, "outbox", command.ID, "claim expired, changed owner, or changed generation")
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

func (s *StateStore) ReleaseOutbox(ctx context.Context, command ReleaseOutboxCommand) (bool, error) {
	const operation = "ReleaseOutbox"
	if err := validateOutboxClaimIdentity(command.ID, command.Owner, command.ClaimGeneration); err != nil {
		return false, commandError(ErrorInvalidArgument, operation, "outbox", command.ID, err.Error())
	}
	if command.RetryAfter < 0 || command.RetryAfter > MaxOutboxRetryDelay {
		return false, commandError(ErrorInvalidArgument, operation, "outbox", command.ID, "retry_after must be between zero and 24h")
	}
	retryMilliseconds := command.RetryAfter.Milliseconds()
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (bool, error) {
		claim, err := s.lockOutboxClaim(ctx, transaction, command.ID)
		if err != nil {
			return false, err
		}
		if claim.completed {
			return false, nil
		}
		if !claim.live || claim.owner != command.Owner || claim.generation != command.ClaimGeneration {
			return false, commandError(ErrorOutboxClaimLost, operation, "outbox", command.ID, "claim expired, changed owner, or changed generation")
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

type lockedOutboxClaim struct {
	owner      string
	generation int
	live       bool
	completed  bool
}

func (s *StateStore) lockOutboxClaim(ctx context.Context, transaction pgx.Tx, id string) (lockedOutboxClaim, error) {
	query := fmt.Sprintf(`
SELECT COALESCE(lock_owner, ''::text), attempts,
       lock_until IS NOT NULL AND lock_until > pg_catalog.clock_timestamp(),
       completed_at IS NOT NULL
FROM %s
WHERE id = $1
  AND kind <> '%s'
FOR UPDATE`, s.table("outbox"), runQueuedOutboxKind)
	var claim lockedOutboxClaim
	if err := transaction.QueryRow(ctx, query, id).Scan(&claim.owner, &claim.generation, &claim.live, &claim.completed); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return lockedOutboxClaim{}, commandError(ErrorNotFound, "lock outbox claim", "outbox", id, "outbox row does not exist")
		}
		return lockedOutboxClaim{}, databaseError("lock outbox claim", err)
	}
	return claim, nil
}

func validateOutboxClaimIdentity(id, owner string, generation int) error {
	if err := validateUUID("id", id); err != nil {
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
