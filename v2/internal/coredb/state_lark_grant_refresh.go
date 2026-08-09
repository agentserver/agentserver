package coredb

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	maximumWorkspaceLarkRefreshBatch  = 100
	larkGrantCredentialTombstoneBytes = 29
)

// UpsertWorkspaceLarkGrant installs the result of an explicit user
// authorization. Reusing the stable grant ID is required. A replacement
// advances authority_version in addition to credential_version so runs frozen
// before the reauthorization cannot inherit the new user's authority.
func (s *StateStore) UpsertWorkspaceLarkGrant(
	ctx context.Context,
	command UpsertWorkspaceLarkGrantCommand,
) (WorkspaceLarkGrant, error) {
	const operation = "UpsertWorkspaceLarkGrant"
	if err := validateUpsertWorkspaceLarkGrant(command); err != nil {
		return WorkspaceLarkGrant{}, commandError(ErrorInvalidArgument, operation, "lark_grant", command.ID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (WorkspaceLarkGrant, error) {
		query := fmt.Sprintf(`
INSERT INTO %s AS grant_state
    (id, workspace_id, user_id, pack_id, policy_sha256, status,
     sealed_token_set, access_expires_at, refresh_expires_at, next_refresh_at)
SELECT $1, member.workspace_id, member.user_id, $4, $5, $6, $7, $8, $9, $10
FROM %s AS member
JOIN %s AS workspace ON workspace.id = member.workspace_id
WHERE member.workspace_id = $2
  AND member.user_id = $3
  AND member.role IN ('owner', 'developer')
  AND workspace.status = 'active'
ON CONFLICT (workspace_id, pack_id, user_id) DO UPDATE
SET policy_sha256 = EXCLUDED.policy_sha256,
    status = EXCLUDED.status,
    sealed_token_set = EXCLUDED.sealed_token_set,
    access_expires_at = EXCLUDED.access_expires_at,
    refresh_expires_at = EXCLUDED.refresh_expires_at,
    next_refresh_at = EXCLUDED.next_refresh_at,
    authority_version = grant_state.authority_version + 1,
    credential_version = grant_state.credential_version + 1,
    last_refreshed_at = pg_catalog.clock_timestamp(),
    revoked_at = NULL,
    refresh_lock_owner = NULL,
    refresh_lock_until = NULL,
    refresh_dispatched_at = NULL,
    refresh_attempts = 0,
    last_refresh_error_code = NULL,
    updated_at = pg_catalog.clock_timestamp()
WHERE grant_state.id = EXCLUDED.id
  AND grant_state.authority_version < %d
  AND grant_state.credential_version < %d
RETURNING %s`,
			s.table("workspace_lark_grants"), s.table("workspace_members"), s.table("workspaces"),
			maxSafeJSONInteger, maxSafeJSONInteger, workspaceLarkGrantColumns(""),
		)
		grant, err := scanWorkspaceLarkGrant(transaction.QueryRow(
			ctx, query, command.ID, command.WorkspaceID, command.UserID,
			LarkReadOnlyPackID, command.PolicySHA256[:], LarkGrantStatusActive,
			command.SealedTokenSet, command.AccessExpiresAt, command.RefreshExpiresAt, command.NextRefreshAt,
		))
		if err == nil {
			return grant, nil
		}
		var postgresError *pgconn.PgError
		if pgxErrorAs(err, &postgresError) && postgresError.Code == "23505" {
			return WorkspaceLarkGrant{}, commandError(ErrorConflict, operation, "lark_grant", command.ID, "grant ID is already bound outside the requested workspace user")
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return WorkspaceLarkGrant{}, databaseError(operation+" upsert", err)
		}

		membershipQuery := fmt.Sprintf(`
SELECT EXISTS (
    SELECT 1
    FROM %s AS member
    JOIN %s AS workspace ON workspace.id = member.workspace_id
    WHERE member.workspace_id = $1 AND member.user_id = $2
      AND member.role IN ('owner', 'developer')
      AND workspace.status = 'active'
)`, s.table("workspace_members"), s.table("workspaces"))
		var member bool
		if err := transaction.QueryRow(ctx, membershipQuery, command.WorkspaceID, command.UserID).Scan(&member); err != nil {
			return WorkspaceLarkGrant{}, databaseError(operation+" verify membership", err)
		}
		if !member {
			return WorkspaceLarkGrant{}, commandError(ErrorForbidden, operation, "workspace", command.WorkspaceID, "active owner or developer membership is required")
		}
		return WorkspaceLarkGrant{}, commandError(ErrorConflict, operation, "lark_grant", command.ID, "workspace user grant has a different stable ID or exhausted version")
	})
}

// ClaimWorkspaceLarkGrantRefreshes claims work but does not yet declare that a
// one-use refresh token was sent. An expired, undispatched claim is safe to
// reclaim. Once MarkWorkspaceLarkGrantRefreshDispatched succeeds, an expired
// claim is instead fenced by FenceAbandonedWorkspaceLarkGrantRefreshes.
func (s *StateStore) ClaimWorkspaceLarkGrantRefreshes(
	ctx context.Context,
	command ClaimWorkspaceLarkGrantRefreshesCommand,
) ([]WorkspaceLarkGrant, error) {
	const operation = "ClaimWorkspaceLarkGrantRefreshes"
	lockMilliseconds, err := validateClaimWorkspaceLarkGrantRefreshes(command)
	if err != nil {
		return nil, commandError(ErrorInvalidArgument, operation, "lark_grant", "", err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) ([]WorkspaceLarkGrant, error) {
		query := fmt.Sprintf(`
WITH candidates AS (
    SELECT id
    FROM %s
    WHERE status = $1
      AND next_refresh_at <= pg_catalog.clock_timestamp()
      AND refresh_expires_at IS NOT NULL
      AND refresh_expires_at > pg_catalog.clock_timestamp()
      AND refresh_dispatched_at IS NULL
      AND (refresh_lock_until IS NULL OR refresh_lock_until <= pg_catalog.clock_timestamp())
    ORDER BY next_refresh_at, id
    FOR UPDATE SKIP LOCKED
    LIMIT $2
)
UPDATE %s AS grant_state
SET refresh_lock_owner = $3,
    refresh_lock_until = pg_catalog.clock_timestamp() + ($4::bigint * interval '1 millisecond'),
    refresh_attempts = refresh_attempts + 1,
    updated_at = pg_catalog.clock_timestamp()
FROM candidates
WHERE grant_state.id = candidates.id
RETURNING %s`, s.table("workspace_lark_grants"), s.table("workspace_lark_grants"), workspaceLarkGrantColumns("grant_state"))
		rows, err := transaction.Query(ctx, query, LarkGrantStatusActive, command.Limit, command.Owner, lockMilliseconds)
		if err != nil {
			return nil, databaseError(operation+" claim", err)
		}
		defer rows.Close()
		grants := make([]WorkspaceLarkGrant, 0, command.Limit)
		for rows.Next() {
			grant, err := scanWorkspaceLarkGrant(rows)
			if err != nil {
				return nil, databaseError(operation+" scan", err)
			}
			grants = append(grants, grant)
		}
		if err := rows.Err(); err != nil {
			return nil, databaseError(operation+" rows", err)
		}
		sort.Slice(grants, func(left, right int) bool {
			if grants[left].NextRefreshAt.Equal(grants[right].NextRefreshAt) {
				return grants[left].ID < grants[right].ID
			}
			return grants[left].NextRefreshAt.Before(grants[right].NextRefreshAt)
		})
		return grants, nil
	})
}

func (s *StateStore) MarkWorkspaceLarkGrantRefreshDispatched(
	ctx context.Context,
	command MarkWorkspaceLarkGrantRefreshDispatchedCommand,
) (WorkspaceLarkGrant, error) {
	const operation = "MarkWorkspaceLarkGrantRefreshDispatched"
	dispatchMilliseconds, err := validateMarkWorkspaceLarkGrantRefreshDispatched(command)
	if err != nil {
		return WorkspaceLarkGrant{}, commandError(ErrorInvalidArgument, operation, "lark_grant", command.GrantID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (WorkspaceLarkGrant, error) {
		query := fmt.Sprintf(`
UPDATE %s
SET refresh_dispatched_at = pg_catalog.clock_timestamp(),
    refresh_lock_until = pg_catalog.clock_timestamp() + ($1::bigint * interval '1 millisecond'),
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $2
  AND authority_version = $3
  AND credential_version = $4
  AND status = $5
  AND refresh_lock_owner = $6
  AND refresh_lock_until > pg_catalog.clock_timestamp()
  AND refresh_dispatched_at IS NULL
RETURNING %s`, s.table("workspace_lark_grants"), workspaceLarkGrantColumns(""))
		grant, err := scanWorkspaceLarkGrant(transaction.QueryRow(
			ctx, query, dispatchMilliseconds, command.GrantID,
			command.ExpectedAuthorityVersion, command.ExpectedCredentialVersion,
			LarkGrantStatusActive, command.Owner,
		))
		if errors.Is(err, pgx.ErrNoRows) {
			return WorkspaceLarkGrant{}, commandError(ErrorLeaseLost, operation, "lark_grant", command.GrantID, "refresh claim expired, changed owner, or was fenced")
		}
		if err != nil {
			return WorkspaceLarkGrant{}, databaseError(operation+" update", err)
		}
		return grant, nil
	})
}

func (s *StateStore) CompleteWorkspaceLarkGrantRefresh(
	ctx context.Context,
	command CompleteWorkspaceLarkGrantRefreshCommand,
) (WorkspaceLarkGrant, error) {
	const operation = "CompleteWorkspaceLarkGrantRefresh"
	if err := validateCompleteWorkspaceLarkGrantRefresh(command); err != nil {
		return WorkspaceLarkGrant{}, commandError(ErrorInvalidArgument, operation, "lark_grant", command.GrantID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (WorkspaceLarkGrant, error) {
		query := fmt.Sprintf(`
UPDATE %s
SET sealed_token_set = $1,
    access_expires_at = $2,
    refresh_expires_at = $3,
    next_refresh_at = $4,
    credential_version = credential_version + 1,
    last_refreshed_at = pg_catalog.clock_timestamp(),
    refresh_lock_owner = NULL,
    refresh_lock_until = NULL,
    refresh_dispatched_at = NULL,
    refresh_attempts = 0,
    last_refresh_error_code = NULL,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $5
  AND authority_version = $6
  AND credential_version = $7
  AND credential_version < %d
  AND status = $8
  AND refresh_lock_owner = $9
  AND refresh_lock_until > pg_catalog.clock_timestamp()
  AND refresh_dispatched_at IS NOT NULL
RETURNING %s`, s.table("workspace_lark_grants"), maxSafeJSONInteger, workspaceLarkGrantColumns(""))
		grant, err := scanWorkspaceLarkGrant(transaction.QueryRow(
			ctx, query, command.SealedTokenSet, command.AccessExpiresAt,
			command.RefreshExpiresAt, command.NextRefreshAt, command.GrantID,
			command.ExpectedAuthorityVersion, command.ExpectedCredentialVersion,
			LarkGrantStatusActive, command.Owner,
		))
		if errors.Is(err, pgx.ErrNoRows) {
			return WorkspaceLarkGrant{}, commandError(ErrorLeaseLost, operation, "lark_grant", command.GrantID, "dispatched refresh claim expired, changed owner, or was fenced")
		}
		if err != nil {
			return WorkspaceLarkGrant{}, databaseError(operation+" update", err)
		}
		return grant, nil
	})
}

func (s *StateStore) DeferWorkspaceLarkGrantRefresh(
	ctx context.Context,
	command DeferWorkspaceLarkGrantRefreshCommand,
) (WorkspaceLarkGrant, error) {
	const operation = "DeferWorkspaceLarkGrantRefresh"
	if err := validateDeferWorkspaceLarkGrantRefresh(command); err != nil {
		return WorkspaceLarkGrant{}, commandError(ErrorInvalidArgument, operation, "lark_grant", command.GrantID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (WorkspaceLarkGrant, error) {
		query := fmt.Sprintf(`
UPDATE %s
SET next_refresh_at = $1,
    refresh_lock_owner = NULL,
    refresh_lock_until = NULL,
    refresh_dispatched_at = NULL,
    last_refresh_error_code = $2,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $3
  AND authority_version = $4
  AND credential_version = $5
  AND status = $6
  AND refresh_lock_owner = $7
  AND refresh_lock_until > pg_catalog.clock_timestamp()
  AND refresh_dispatched_at IS NOT NULL
RETURNING %s`, s.table("workspace_lark_grants"), workspaceLarkGrantColumns(""))
		grant, err := scanWorkspaceLarkGrant(transaction.QueryRow(
			ctx, query, command.NextRefreshAt, command.ErrorCode, command.GrantID,
			command.ExpectedAuthorityVersion, command.ExpectedCredentialVersion,
			LarkGrantStatusActive, command.Owner,
		))
		if errors.Is(err, pgx.ErrNoRows) {
			return WorkspaceLarkGrant{}, commandError(ErrorLeaseLost, operation, "lark_grant", command.GrantID, "dispatched refresh claim expired, changed owner, or was fenced")
		}
		if err != nil {
			return WorkspaceLarkGrant{}, databaseError(operation+" update", err)
		}
		return grant, nil
	})
}

// FailWorkspaceLarkGrantRefresh handles both local validation failures before
// dispatch and permanent or ambiguous provider outcomes after dispatch. It
// erases the sealed credentials and advances authority_version immediately.
func (s *StateStore) FailWorkspaceLarkGrantRefresh(
	ctx context.Context,
	command FailWorkspaceLarkGrantRefreshCommand,
) (WorkspaceLarkGrant, error) {
	const operation = "FailWorkspaceLarkGrantRefresh"
	if err := validateFailWorkspaceLarkGrantRefresh(command); err != nil {
		return WorkspaceLarkGrant{}, commandError(ErrorInvalidArgument, operation, "lark_grant", command.GrantID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (WorkspaceLarkGrant, error) {
		query := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    sealed_token_set = pg_catalog.decode(pg_catalog.repeat('00', %d), 'hex'),
    authority_version = authority_version + 1,
    refresh_lock_owner = NULL,
    refresh_lock_until = NULL,
    refresh_dispatched_at = NULL,
    last_refresh_error_code = $2,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $3
  AND authority_version = $4
  AND credential_version = $5
  AND authority_version < %d
  AND status = $6
  AND refresh_lock_owner = $7
  AND refresh_lock_until > pg_catalog.clock_timestamp()
RETURNING %s`,
			s.table("workspace_lark_grants"), larkGrantCredentialTombstoneBytes,
			maxSafeJSONInteger, workspaceLarkGrantColumns(""),
		)
		grant, err := scanWorkspaceLarkGrant(transaction.QueryRow(
			ctx, query, LarkGrantStatusReauthRequired, command.ErrorCode, command.GrantID,
			command.ExpectedAuthorityVersion, command.ExpectedCredentialVersion,
			LarkGrantStatusActive, command.Owner,
		))
		if errors.Is(err, pgx.ErrNoRows) {
			return WorkspaceLarkGrant{}, commandError(ErrorLeaseLost, operation, "lark_grant", command.GrantID, "refresh claim expired, changed owner, or was fenced")
		}
		if err != nil {
			return WorkspaceLarkGrant{}, databaseError(operation+" update", err)
		}
		return grant, nil
	})
}

func (s *StateStore) FenceAbandonedWorkspaceLarkGrantRefreshes(ctx context.Context) (int64, error) {
	const operation = "FenceAbandonedWorkspaceLarkGrantRefreshes"
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (int64, error) {
		query := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    sealed_token_set = pg_catalog.decode(pg_catalog.repeat('00', %d), 'hex'),
    authority_version = authority_version + 1,
    refresh_lock_owner = NULL,
    refresh_lock_until = NULL,
    refresh_dispatched_at = NULL,
    last_refresh_error_code = 'refresh_outcome_ambiguous',
    updated_at = pg_catalog.clock_timestamp()
WHERE status = $2
  AND authority_version < %d
  AND refresh_dispatched_at IS NOT NULL
  AND refresh_lock_until <= pg_catalog.clock_timestamp()`,
			s.table("workspace_lark_grants"), larkGrantCredentialTombstoneBytes, maxSafeJSONInteger,
		)
		result, err := transaction.Exec(ctx, query, LarkGrantStatusReauthRequired, LarkGrantStatusActive)
		if err != nil {
			return 0, databaseError(operation+" update", err)
		}
		return result.RowsAffected(), nil
	})
}

func (s *StateStore) ExpireWorkspaceLarkGrants(ctx context.Context) (int64, error) {
	const operation = "ExpireWorkspaceLarkGrants"
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (int64, error) {
		query := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    sealed_token_set = pg_catalog.decode(pg_catalog.repeat('00', %d), 'hex'),
    authority_version = authority_version + 1,
    refresh_lock_owner = NULL,
    refresh_lock_until = NULL,
    refresh_dispatched_at = NULL,
    last_refresh_error_code = 'refresh_grant_expired',
    updated_at = pg_catalog.clock_timestamp()
WHERE status = $2
  AND authority_version < %d
  AND access_expires_at <= pg_catalog.clock_timestamp()
  AND (refresh_expires_at IS NULL OR refresh_expires_at <= pg_catalog.clock_timestamp())`,
			s.table("workspace_lark_grants"), larkGrantCredentialTombstoneBytes, maxSafeJSONInteger,
		)
		result, err := transaction.Exec(ctx, query, LarkGrantStatusExpired, LarkGrantStatusActive)
		if err != nil {
			return 0, databaseError(operation+" update", err)
		}
		return result.RowsAffected(), nil
	})
}

func (s *StateStore) RevokeWorkspaceLarkGrant(
	ctx context.Context,
	command RevokeWorkspaceLarkGrantCommand,
) (WorkspaceLarkGrant, error) {
	const operation = "RevokeWorkspaceLarkGrant"
	if err := validateRevokeWorkspaceLarkGrant(command); err != nil {
		return WorkspaceLarkGrant{}, commandError(ErrorInvalidArgument, operation, "lark_grant", command.GrantID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (WorkspaceLarkGrant, error) {
		query := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    sealed_token_set = pg_catalog.decode(pg_catalog.repeat('00', %d), 'hex'),
    authority_version = authority_version + 1,
    revoked_at = pg_catalog.clock_timestamp(),
    refresh_lock_owner = NULL,
    refresh_lock_until = NULL,
    refresh_dispatched_at = NULL,
    last_refresh_error_code = $2,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $3 AND workspace_id = $4 AND user_id = $5
  AND status <> $1
  AND authority_version < %d
RETURNING %s`,
			s.table("workspace_lark_grants"), larkGrantCredentialTombstoneBytes,
			maxSafeJSONInteger, workspaceLarkGrantColumns(""),
		)
		grant, err := scanWorkspaceLarkGrant(transaction.QueryRow(
			ctx, query, LarkGrantStatusRevoked, command.ReasonCode,
			command.GrantID, command.WorkspaceID, command.UserID,
		))
		if err == nil {
			return grant, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return WorkspaceLarkGrant{}, databaseError(operation+" update", err)
		}
		read := fmt.Sprintf(`
SELECT %s
FROM %s AS grant_state
WHERE grant_state.id = $1 AND grant_state.workspace_id = $2 AND grant_state.user_id = $3`,
			workspaceLarkGrantColumns("grant_state"), s.table("workspace_lark_grants"),
		)
		grant, err = scanWorkspaceLarkGrant(transaction.QueryRow(ctx, read, command.GrantID, command.WorkspaceID, command.UserID))
		if errors.Is(err, pgx.ErrNoRows) {
			return WorkspaceLarkGrant{}, commandError(ErrorNotFound, operation, "lark_grant", command.GrantID, "Lark grant does not exist in the requested workspace user scope")
		}
		if err != nil {
			return WorkspaceLarkGrant{}, databaseError(operation+" read", err)
		}
		if grant.Status == LarkGrantStatusRevoked {
			return grant, nil
		}
		return WorkspaceLarkGrant{}, commandError(ErrorConflict, operation, "lark_grant", command.GrantID, "Lark grant authority version is exhausted")
	})
}

func validateUpsertWorkspaceLarkGrant(command UpsertWorkspaceLarkGrantCommand) error {
	if err := validateCreateWorkspaceLarkGrant(CreateWorkspaceLarkGrantCommand{
		ID: command.ID, WorkspaceID: command.WorkspaceID, UserID: command.UserID,
		PolicySHA256: command.PolicySHA256, SealedTokenSet: command.SealedTokenSet,
		AccessExpiresAt: command.AccessExpiresAt, RefreshExpiresAt: command.RefreshExpiresAt,
		NextRefreshAt: command.NextRefreshAt,
	}); err != nil {
		return err
	}
	if command.RefreshExpiresAt == nil {
		return errors.New("Lark reauthorization requires an offline refresh expiry")
	}
	return nil
}

func validateClaimWorkspaceLarkGrantRefreshes(command ClaimWorkspaceLarkGrantRefreshesCommand) (int64, error) {
	if err := validateBoundedText("owner", command.Owner, 256); err != nil {
		return 0, err
	}
	if command.Limit < 1 || command.Limit > maximumWorkspaceLarkRefreshBatch {
		return 0, fmt.Errorf("limit must be between 1 and %d", maximumWorkspaceLarkRefreshBatch)
	}
	return durationMilliseconds("lock_ttl", command.LockTTL, MaxLeaseTTL)
}

func validateMarkWorkspaceLarkGrantRefreshDispatched(command MarkWorkspaceLarkGrantRefreshDispatchedCommand) (int64, error) {
	if err := validateLarkRefreshClaimIdentity(
		command.GrantID, command.Owner, command.ExpectedAuthorityVersion, command.ExpectedCredentialVersion,
	); err != nil {
		return 0, err
	}
	return durationMilliseconds("dispatch_ttl", command.DispatchTTL, MaxLeaseTTL)
}

func validateCompleteWorkspaceLarkGrantRefresh(command CompleteWorkspaceLarkGrantRefreshCommand) error {
	if err := validateLarkRefreshClaimIdentity(
		command.GrantID, command.Owner, command.ExpectedAuthorityVersion, command.ExpectedCredentialVersion,
	); err != nil {
		return err
	}
	if command.RefreshExpiresAt == nil {
		return errors.New("refreshed Lark credential requires a refresh expiry")
	}
	return validateWorkspaceLarkCredential(
		[32]byte{1}, command.SealedTokenSet, command.AccessExpiresAt,
		command.RefreshExpiresAt, command.NextRefreshAt,
	)
}

func validateDeferWorkspaceLarkGrantRefresh(command DeferWorkspaceLarkGrantRefreshCommand) error {
	if err := validateLarkRefreshClaimIdentity(
		command.GrantID, command.Owner, command.ExpectedAuthorityVersion, command.ExpectedCredentialVersion,
	); err != nil {
		return err
	}
	if command.NextRefreshAt.IsZero() {
		return errors.New("next_refresh_at is required")
	}
	return validateLarkRefreshErrorCode(command.ErrorCode)
}

func validateFailWorkspaceLarkGrantRefresh(command FailWorkspaceLarkGrantRefreshCommand) error {
	if err := validateLarkRefreshClaimIdentity(
		command.GrantID, command.Owner, command.ExpectedAuthorityVersion, command.ExpectedCredentialVersion,
	); err != nil {
		return err
	}
	return validateLarkRefreshErrorCode(command.ErrorCode)
}

func validateRevokeWorkspaceLarkGrant(command RevokeWorkspaceLarkGrantCommand) error {
	for name, value := range map[string]string{
		"grant_id": command.GrantID, "workspace_id": command.WorkspaceID, "user_id": command.UserID,
	} {
		if err := validateUUID(name, value); err != nil {
			return err
		}
	}
	return validateLarkRefreshErrorCode(command.ReasonCode)
}

func validateLarkRefreshClaimIdentity(grantID, owner string, authorityVersion, credentialVersion int64) error {
	if err := validateUUID("grant_id", grantID); err != nil {
		return err
	}
	if err := validateBoundedText("owner", owner, 256); err != nil {
		return err
	}
	if authorityVersion < 1 || authorityVersion > maxSafeJSONInteger ||
		credentialVersion < 1 || credentialVersion > maxSafeJSONInteger {
		return errors.New("expected Lark grant versions must be positive safe integers")
	}
	return nil
}

func validateLarkRefreshErrorCode(value string) error {
	if err := validateBoundedText("error_code", value, 128); err != nil {
		return err
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '_' {
			continue
		}
		return errors.New("error_code must contain only lowercase ASCII letters, digits, and underscore")
	}
	return nil
}
