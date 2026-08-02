package coredb

import (
	"bytes"
	"context"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (s *StateStore) CreateExecutorResource(ctx context.Context, command CreateExecutorResourceCommand) (CreateExecutorResourceResult, error) {
	const operation = "CreateExecutorResource"
	if err := validateCreateExecutorResource(command); err != nil {
		return CreateExecutorResourceResult{}, commandError(ErrorInvalidArgument, operation, "executor", command.ExecutorID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (CreateExecutorResourceResult, error) {
		if err := s.requireWorkspaceOwner(ctx, transaction, operation, command.WorkspaceID, command.ActorID); err != nil {
			return CreateExecutorResourceResult{}, err
		}
		insert := fmt.Sprintf(`
INSERT INTO %s (id, workspace_id, status)
VALUES ($1, $2, 'enrolling')
ON CONFLICT (id) DO NOTHING`, s.table("executors"))
		tag, err := transaction.Exec(ctx, insert, command.ExecutorID, command.WorkspaceID)
		if err != nil {
			return CreateExecutorResourceResult{}, databaseError(operation+" insert executor", err)
		}
		executor, err := s.readExecutorResource(ctx, transaction, operation, command.ExecutorID, true)
		if err != nil {
			return CreateExecutorResourceResult{}, err
		}
		if executor.WorkspaceID != command.WorkspaceID {
			return CreateExecutorResourceResult{}, commandError(ErrorConflict, operation, "executor", command.ExecutorID, "executor identity belongs to another workspace")
		}
		return CreateExecutorResourceResult{Executor: executor, Created: tag.RowsAffected() == 1}, nil
	})
}

func (s *StateStore) IssueExecutorEnrollmentToken(ctx context.Context, command IssueExecutorEnrollmentTokenCommand) (IssueExecutorEnrollmentTokenResult, error) {
	const operation = "IssueExecutorEnrollmentToken"
	ttlMilliseconds, err := validateIssueExecutorEnrollmentToken(command)
	if err != nil {
		return IssueExecutorEnrollmentTokenResult{}, commandError(ErrorInvalidArgument, operation, "executor", command.ExecutorID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (IssueExecutorEnrollmentTokenResult, error) {
		if err := s.requireWorkspaceOwner(ctx, transaction, operation, command.WorkspaceID, command.ActorID); err != nil {
			return IssueExecutorEnrollmentTokenResult{}, err
		}
		executor, err := s.readExecutorResource(ctx, transaction, operation, command.ExecutorID, true)
		if err != nil {
			return IssueExecutorEnrollmentTokenResult{}, err
		}
		if executor.WorkspaceID != command.WorkspaceID {
			return IssueExecutorEnrollmentTokenResult{}, commandError(ErrorNotFound, operation, "executor", command.ExecutorID, "executor does not belong to the workspace")
		}
		if executor.Status != ExecutorStatusEnrolling {
			return IssueExecutorEnrollmentTokenResult{}, commandError(ErrorInvalidState, operation, "executor", command.ExecutorID, "only an enrolling executor can receive an enrollment token")
		}

		existing, found, err := s.readEnrollmentTokenByRequest(ctx, transaction, operation, command.ExecutorID, command.ActorID, command.IdempotencyKey)
		if err != nil {
			return IssueExecutorEnrollmentTokenResult{}, err
		}
		if found {
			if existing.RevokedAt != nil || existing.ConsumedAt != nil {
				return IssueExecutorEnrollmentTokenResult{}, commandError(ErrorIdempotencyConflict, operation, "executor_enrollment_token", existing.ID, "idempotent token request was already superseded or consumed")
			}
			return IssueExecutorEnrollmentTokenResult{Token: existing, Created: false}, nil
		}

		revoke := fmt.Sprintf(`
UPDATE %s
SET revoked_at = pg_catalog.clock_timestamp(), version = version + 1
WHERE executor_id = $1 AND consumed_at IS NULL AND revoked_at IS NULL`, s.table("executor_enrollment_tokens"))
		if _, err := transaction.Exec(ctx, revoke, command.ExecutorID); err != nil {
			return IssueExecutorEnrollmentTokenResult{}, databaseError(operation+" revoke prior token", err)
		}
		insert := fmt.Sprintf(`
WITH authority_time AS MATERIALIZED (
    SELECT pg_catalog.date_trunc('milliseconds', pg_catalog.clock_timestamp()) AS now
)
INSERT INTO %s
    (id, workspace_id, executor_id, issued_by, idempotency_key, issued_at, expires_at)
SELECT $1, $2, $3, $4, $5, authority_time.now,
       authority_time.now + $6 * INTERVAL '1 millisecond'
FROM authority_time
RETURNING id::text, workspace_id::text, executor_id::text, issued_by::text,
          idempotency_key, issued_at, expires_at, claimed_at, consumed_at,
          revoked_at, enrollment_request_sha256, version`, s.table("executor_enrollment_tokens"))
		token, err := scanExecutorEnrollmentToken(transaction.QueryRow(ctx, insert,
			command.TokenID, command.WorkspaceID, command.ExecutorID, command.ActorID,
			command.IdempotencyKey, ttlMilliseconds,
		))
		if err != nil {
			var postgresError *pgconn.PgError
			if pgxErrorAs(err, &postgresError) && postgresError.Code == "23505" {
				return IssueExecutorEnrollmentTokenResult{}, commandError(ErrorConflict, operation, "executor_enrollment_token", command.TokenID, "token identity or idempotency key is already in use")
			}
			return IssueExecutorEnrollmentTokenResult{}, databaseError(operation+" insert token", err)
		}
		return IssueExecutorEnrollmentTokenResult{Token: token, Created: true}, nil
	})
}

func (s *StateStore) ClaimExecutorEnrollment(ctx context.Context, command ClaimExecutorEnrollmentCommand) (ExecutorEnrollmentReservation, error) {
	const operation = "ClaimExecutorEnrollment"
	if err := validateClaimExecutorEnrollment(command); err != nil {
		return ExecutorEnrollmentReservation{}, commandError(ErrorInvalidArgument, operation, "executor", command.ExecutorID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (ExecutorEnrollmentReservation, error) {
		// Enrollment issuance, claim, and completion all lock the executor row
		// before any enrollment-token row. A claim used to take these locks in
		// the reverse order, which could deadlock with an owner issuing a
		// replacement token while the claim was in flight.
		row, err := s.lockExecutorEnrollment(ctx, transaction, operation, command.ExecutorID)
		if err != nil {
			return ExecutorEnrollmentReservation{}, err
		}
		token, err := s.lockEnrollmentToken(ctx, transaction, operation, command.TokenID)
		if err != nil {
			return ExecutorEnrollmentReservation{}, err
		}
		if err := enrollmentTokenMatchesClaims(token, command); err != nil {
			return ExecutorEnrollmentReservation{}, commandError(ErrorForbidden, operation, "executor_enrollment_token", command.TokenID, err.Error())
		}
		if token.RevokedAt != nil {
			return ExecutorEnrollmentReservation{}, commandError(ErrorForbidden, operation, "executor_enrollment_token", command.TokenID, "enrollment token is revoked")
		}
		var claimTime time.Time
		if err := transaction.QueryRow(ctx, "SELECT pg_catalog.date_trunc('milliseconds', pg_catalog.clock_timestamp())").Scan(&claimTime); err != nil {
			return ExecutorEnrollmentReservation{}, databaseError(operation+" evaluate token expiry", err)
		}
		if !token.ExpiresAt.After(claimTime) {
			return ExecutorEnrollmentReservation{}, commandError(ErrorForbidden, operation, "executor_enrollment_token", command.TokenID, "enrollment token is expired")
		}

		executor := row.ExecutorResource
		if executor.WorkspaceID != command.WorkspaceID {
			return ExecutorEnrollmentReservation{}, commandError(ErrorForbidden, operation, "executor", command.ExecutorID, "executor workspace does not match enrollment token")
		}
		if executor.Status == ExecutorStatusRevoked {
			return ExecutorEnrollmentReservation{}, commandError(ErrorForbidden, operation, "executor", command.ExecutorID, "executor is revoked")
		}
		if executor.Status != ExecutorStatusEnrolling && executor.Status != ExecutorStatusOffline {
			return ExecutorEnrollmentReservation{}, commandError(ErrorInvalidState, operation, "executor", command.ExecutorID, "executor cannot be enrolled in its current state")
		}
		created := row.enrollmentHash == nil
		if !created {
			if !bytes.Equal(row.enrollmentHash, command.EnrollmentRequestSHA256[:]) ||
				!bytes.Equal(row.machinePublicKey, command.MachinePublicKeyEd25519[:]) ||
				!bytes.Equal(row.machineKeyHash, command.MachineKeySHA256[:]) ||
				!bytes.Equal(row.oauthPublicKeyX, command.OAuthPublicKeyP256X[:]) ||
				!bytes.Equal(row.oauthPublicKeyY, command.OAuthPublicKeyP256Y[:]) ||
				!bytes.Equal(row.oauthKeyHash, command.OAuthKeySHA256[:]) ||
				row.oauthClientID == nil || *row.oauthClientID != command.OAuthClientID {
				return ExecutorEnrollmentReservation{}, commandError(ErrorIdempotencyConflict, operation, "executor", command.ExecutorID, "executor was already claimed by different enrollment authority")
			}
		} else {
			if executor.Status != ExecutorStatusEnrolling {
				return ExecutorEnrollmentReservation{}, commandError(ErrorInvalidState, operation, "executor", command.ExecutorID, "offline executor lacks a complete enrollment identity")
			}
			update := fmt.Sprintf(`
UPDATE %s
SET machine_public_key_ed25519 = $2,
    machine_key_sha256 = $3,
    oauth_public_key_p256_x = $4,
    oauth_public_key_p256_y = $5,
    oauth_key_sha256 = $6,
    oauth_client_id = $7,
    enrollment_request_sha256 = $8,
    agentx_version = $9,
    runtime_manifest_sha256 = $10,
    exec_protocol_source_sha256 = $11,
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $1`, s.table("executors"))
			if _, err := transaction.Exec(ctx, update,
				command.ExecutorID, command.MachinePublicKeyEd25519[:], command.MachineKeySHA256[:],
				command.OAuthPublicKeyP256X[:], command.OAuthPublicKeyP256Y[:], command.OAuthKeySHA256[:],
				command.OAuthClientID, command.EnrollmentRequestSHA256[:], command.AgentxVersion,
				command.RuntimeManifestSHA256[:], command.ExecProtocolSourceSHA256[:],
			); err != nil {
				return ExecutorEnrollmentReservation{}, databaseError(operation+" reserve executor identity", err)
			}
			if err := s.insertExecutorEnrollmentEnvironments(ctx, transaction, operation, command); err != nil {
				return ExecutorEnrollmentReservation{}, err
			}
		}

		if token.EnrollmentSHA256 != nil && !bytes.Equal(token.EnrollmentSHA256, command.EnrollmentRequestSHA256[:]) {
			return ExecutorEnrollmentReservation{}, commandError(ErrorIdempotencyConflict, operation, "executor_enrollment_token", command.TokenID, "token was claimed by a different enrollment request")
		}
		claim := fmt.Sprintf(`
UPDATE %s
SET claimed_at = COALESCE(claimed_at, $3),
    enrollment_request_sha256 = $2,
    version = CASE WHEN claimed_at IS NULL THEN version + 1 ELSE version END
WHERE id = $1`, s.table("executor_enrollment_tokens"))
		if _, err := transaction.Exec(ctx, claim, command.TokenID, command.EnrollmentRequestSHA256[:], claimTime); err != nil {
			return ExecutorEnrollmentReservation{}, databaseError(operation+" claim token", err)
		}
		executor, err = s.readExecutorResource(ctx, transaction, operation, command.ExecutorID, false)
		if err != nil {
			return ExecutorEnrollmentReservation{}, err
		}
		return ExecutorEnrollmentReservation{Executor: executor, OAuthClientID: command.OAuthClientID, Created: created}, nil
	})
}

func (s *StateStore) CompleteExecutorEnrollment(ctx context.Context, command CompleteExecutorEnrollmentCommand) (ExecutorResource, error) {
	const operation = "CompleteExecutorEnrollment"
	if err := validateCompleteExecutorEnrollment(command); err != nil {
		return ExecutorResource{}, commandError(ErrorInvalidArgument, operation, "executor", command.ExecutorID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (ExecutorResource, error) {
		// Preserve the executor -> enrollment-token lock order shared with
		// issuance and claim. See ClaimExecutorEnrollment for the concurrency
		// boundary this enforces.
		row, err := s.lockExecutorEnrollment(ctx, transaction, operation, command.ExecutorID)
		if err != nil {
			return ExecutorResource{}, err
		}
		token, err := s.lockEnrollmentToken(ctx, transaction, operation, command.TokenID)
		if err != nil {
			return ExecutorResource{}, err
		}
		if token.WorkspaceID != command.WorkspaceID || token.ExecutorID != command.ExecutorID ||
			token.EnrollmentSHA256 == nil || !bytes.Equal(token.EnrollmentSHA256, command.EnrollmentRequestSHA256[:]) || token.RevokedAt != nil {
			return ExecutorResource{}, commandError(ErrorForbidden, operation, "executor_enrollment_token", command.TokenID, "token does not authorize this enrollment completion")
		}
		executor := row.ExecutorResource
		if executor.WorkspaceID != command.WorkspaceID || row.oauthClientID == nil || row.enrollmentHash == nil || !bytes.Equal(row.enrollmentHash, command.EnrollmentRequestSHA256[:]) {
			return ExecutorResource{}, commandError(ErrorIdempotencyConflict, operation, "executor", command.ExecutorID, "reserved executor identity differs from enrollment completion")
		}
		if token.ConsumedAt == nil {
			if executor.Status != ExecutorStatusEnrolling {
				return ExecutorResource{}, commandError(ErrorInvalidState, operation, "executor", command.ExecutorID, "unconsumed enrollment token requires an enrolling executor")
			}
			updateExecutor := fmt.Sprintf(`
UPDATE %s
SET status = 'offline', version = version + 1, updated_at = pg_catalog.clock_timestamp()
WHERE id = $1`, s.table("executors"))
			if _, err := transaction.Exec(ctx, updateExecutor, command.ExecutorID); err != nil {
				return ExecutorResource{}, databaseError(operation+" finalize executor", err)
			}
			updateToken := fmt.Sprintf(`
UPDATE %s
SET consumed_at = pg_catalog.clock_timestamp(), version = version + 1
WHERE id = $1`, s.table("executor_enrollment_tokens"))
			if _, err := transaction.Exec(ctx, updateToken, command.TokenID); err != nil {
				return ExecutorResource{}, databaseError(operation+" consume token", err)
			}
			revoke := fmt.Sprintf(`
UPDATE %s
SET revoked_at = pg_catalog.clock_timestamp(), version = version + 1
WHERE executor_id = $1 AND id <> $2 AND consumed_at IS NULL AND revoked_at IS NULL`, s.table("executor_enrollment_tokens"))
			if _, err := transaction.Exec(ctx, revoke, command.ExecutorID, command.TokenID); err != nil {
				return ExecutorResource{}, databaseError(operation+" revoke sibling tokens", err)
			}
		} else if executor.Status != ExecutorStatusOffline && executor.Status != ExecutorStatusOnline {
			return ExecutorResource{}, commandError(ErrorInvalidState, operation, "executor", command.ExecutorID, "completed enrollment has an invalid executor state")
		}
		return s.readExecutorResource(ctx, transaction, operation, command.ExecutorID, false)
	})
}

func (s *StateStore) AuthorizeExecutorOAuthClient(ctx context.Context, oauthClientID string) (ExecutorMachineAuthority, error) {
	const operation = "AuthorizeExecutorOAuthClient"
	if err := validateBoundedText("oauth_client_id", oauthClientID, 128); err != nil {
		return ExecutorMachineAuthority{}, commandError(ErrorInvalidArgument, operation, "executor_oauth_client", oauthClientID, err.Error())
	}
	return withStateReadTransaction(ctx, s, operation, func(transaction pgx.Tx) (ExecutorMachineAuthority, error) {
		query := fmt.Sprintf(`
SELECT e.id::text, e.workspace_id::text, e.oauth_client_id,
       e.machine_public_key_ed25519, e.machine_key_sha256,
       e.oauth_public_key_p256_x, e.oauth_public_key_p256_y, e.oauth_key_sha256,
       e.version,
       pg_catalog.clock_timestamp()
FROM %s AS e
JOIN %s AS w ON w.id = e.workspace_id AND w.status = 'active'
WHERE e.oauth_client_id = $1
  AND e.status IN ('offline', 'online')
  AND e.machine_public_key_ed25519 IS NOT NULL
  AND e.machine_key_sha256 IS NOT NULL
  AND e.oauth_public_key_p256_x IS NOT NULL
  AND e.oauth_public_key_p256_y IS NOT NULL
  AND e.oauth_key_sha256 IS NOT NULL`, s.table("executors"), s.table("workspaces"))
		var authority ExecutorMachineAuthority
		var publicKey, keyHash, oauthX, oauthY, oauthHash []byte
		err := transaction.QueryRow(ctx, query, oauthClientID).Scan(
			&authority.ExecutorID, &authority.WorkspaceID, &authority.OAuthClientID,
			&publicKey, &keyHash, &oauthX, &oauthY, &oauthHash,
			&authority.ExecutorVersion, &authority.AuthorizedAt,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return ExecutorMachineAuthority{}, commandError(ErrorForbidden, operation, "executor_oauth_client", oauthClientID, "OAuth client is not an active enrolled executor")
		}
		if err != nil {
			return ExecutorMachineAuthority{}, databaseError(operation+" read machine authority", err)
		}
		if len(publicKey) != len(authority.MachinePublicKeyEd25519) || len(keyHash) != len(authority.MachineKeySHA256) ||
			len(oauthX) != len(authority.OAuthPublicKeyP256X) || len(oauthY) != len(authority.OAuthPublicKeyP256Y) ||
			len(oauthHash) != len(authority.OAuthKeySHA256) {
			return ExecutorMachineAuthority{}, databaseError(operation+" validate stored machine authority", errors.New("stored executor machine identity has invalid length"))
		}
		copy(authority.MachinePublicKeyEd25519[:], publicKey)
		copy(authority.MachineKeySHA256[:], keyHash)
		copy(authority.OAuthPublicKeyP256X[:], oauthX)
		copy(authority.OAuthPublicKeyP256Y[:], oauthY)
		copy(authority.OAuthKeySHA256[:], oauthHash)
		return authority, nil
	})
}

func (s *StateStore) requireWorkspaceOwner(ctx context.Context, transaction pgx.Tx, operation, workspaceID, actorID string) error {
	query := fmt.Sprintf(`
SELECT wm.role
FROM %s AS w
JOIN %s AS wm ON wm.workspace_id = w.id AND wm.user_id = $2
JOIN %s AS u ON u.id = wm.user_id AND u.status = 'active'
WHERE w.id = $1 AND w.status = 'active'
FOR SHARE OF w, wm, u`, s.table("workspaces"), s.table("workspace_members"), s.table("users"))
	var role string
	if err := transaction.QueryRow(ctx, query, workspaceID, actorID).Scan(&role); errors.Is(err, pgx.ErrNoRows) {
		return commandError(ErrorForbidden, operation, "workspace", workspaceID, "actor is not a current member of the active workspace")
	} else if err != nil {
		return databaseError(operation+" read workspace owner", err)
	}
	if role != "owner" {
		return commandError(ErrorForbidden, operation, "workspace", workspaceID, "only a workspace owner may manage executor enrollment")
	}
	return nil
}

func (s *StateStore) readExecutorResource(ctx context.Context, transaction pgx.Tx, operation, executorID string, lock bool) (ExecutorResource, error) {
	query := fmt.Sprintf(`
SELECT id::text, workspace_id::text, status, version, created_at, updated_at
FROM %s WHERE id = $1`, s.table("executors"))
	if lock {
		query += " FOR UPDATE"
	}
	var executor ExecutorResource
	err := transaction.QueryRow(ctx, query, executorID).Scan(
		&executor.ID, &executor.WorkspaceID, &executor.Status, &executor.Version,
		&executor.CreatedAt, &executor.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ExecutorResource{}, commandError(ErrorNotFound, operation, "executor", executorID, "executor does not exist")
	}
	if err != nil {
		return ExecutorResource{}, databaseError(operation+" read executor", err)
	}
	return executor, nil
}

func (s *StateStore) readEnrollmentTokenByRequest(ctx context.Context, transaction pgx.Tx, operation, executorID, actorID, idempotencyKey string) (ExecutorEnrollmentToken, bool, error) {
	query := fmt.Sprintf(`
SELECT id::text, workspace_id::text, executor_id::text, issued_by::text,
       idempotency_key, issued_at, expires_at, claimed_at, consumed_at,
       revoked_at, enrollment_request_sha256, version
FROM %s
WHERE executor_id = $1 AND issued_by = $2 AND idempotency_key = $3
FOR UPDATE`, s.table("executor_enrollment_tokens"))
	token, err := scanExecutorEnrollmentToken(transaction.QueryRow(ctx, query, executorID, actorID, idempotencyKey))
	if errors.Is(err, pgx.ErrNoRows) {
		return ExecutorEnrollmentToken{}, false, nil
	}
	if err != nil {
		return ExecutorEnrollmentToken{}, false, databaseError(operation+" read token request", err)
	}
	return token, true, nil
}

func (s *StateStore) lockEnrollmentToken(ctx context.Context, transaction pgx.Tx, operation, tokenID string) (ExecutorEnrollmentToken, error) {
	query := fmt.Sprintf(`
SELECT id::text, workspace_id::text, executor_id::text, issued_by::text,
       idempotency_key, issued_at, expires_at, claimed_at, consumed_at,
       revoked_at, enrollment_request_sha256, version
FROM %s WHERE id = $1 FOR UPDATE`, s.table("executor_enrollment_tokens"))
	token, err := scanExecutorEnrollmentToken(transaction.QueryRow(ctx, query, tokenID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ExecutorEnrollmentToken{}, commandError(ErrorForbidden, operation, "executor_enrollment_token", tokenID, "enrollment token does not exist")
	}
	if err != nil {
		return ExecutorEnrollmentToken{}, databaseError(operation+" lock token", err)
	}
	return token, nil
}

type enrollmentExecutorRow struct {
	ExecutorResource
	machinePublicKey []byte
	machineKeyHash   []byte
	oauthPublicKeyX  []byte
	oauthPublicKeyY  []byte
	oauthKeyHash     []byte
	oauthClientID    *string
	enrollmentHash   []byte
}

func (s *StateStore) lockExecutorEnrollment(ctx context.Context, transaction pgx.Tx, operation, executorID string) (enrollmentExecutorRow, error) {
	query := fmt.Sprintf(`
SELECT id::text, workspace_id::text, status, version, created_at, updated_at,
       machine_public_key_ed25519, machine_key_sha256,
       oauth_public_key_p256_x, oauth_public_key_p256_y, oauth_key_sha256,
       oauth_client_id,
       enrollment_request_sha256
FROM %s WHERE id = $1 FOR UPDATE`, s.table("executors"))
	var row enrollmentExecutorRow
	err := transaction.QueryRow(ctx, query, executorID).Scan(
		&row.ID, &row.WorkspaceID, &row.Status, &row.Version, &row.CreatedAt, &row.UpdatedAt,
		&row.machinePublicKey, &row.machineKeyHash,
		&row.oauthPublicKeyX, &row.oauthPublicKeyY, &row.oauthKeyHash,
		&row.oauthClientID, &row.enrollmentHash,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return enrollmentExecutorRow{}, commandError(ErrorNotFound, operation, "executor", executorID, "executor does not exist")
	}
	if err != nil {
		return enrollmentExecutorRow{}, databaseError(operation+" lock executor enrollment", err)
	}
	return row, nil
}

func (s *StateStore) insertExecutorEnrollmentEnvironments(ctx context.Context, transaction pgx.Tx, operation string, command ClaimExecutorEnrollmentCommand) error {
	var count int
	countQuery := fmt.Sprintf("SELECT pg_catalog.count(*) FROM %s WHERE executor_id = $1", s.table("executor_environments"))
	if err := transaction.QueryRow(ctx, countQuery, command.ExecutorID).Scan(&count); err != nil {
		return databaseError(operation+" count existing environments", err)
	}
	if count != 0 {
		return commandError(ErrorConflict, operation, "executor", command.ExecutorID, "new enrollment executor already has environments")
	}
	insert := fmt.Sprintf(`
INSERT INTO %s
    (id, executor_id, root_descriptor, owner_policy_sha256, platform,
     codex_release, codex_commit, codex_sha256, outer_profile_version,
     process_methods, insecure_dev, status)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, false, 'offline')`, s.table("executor_environments"))
	for _, environment := range command.Environments {
		if _, err := transaction.Exec(ctx, insert,
			environment.ID, command.ExecutorID, []byte(environment.RootDescriptor), environment.OwnerPolicySHA256[:],
			environment.Platform, environment.CodexRelease, environment.CodexCommit, environment.CodexSHA256[:],
			environment.OuterProfileVersion, environment.ProcessMethods,
		); err != nil {
			var postgresError *pgconn.PgError
			if pgxErrorAs(err, &postgresError) && postgresError.Code == "23505" {
				return commandError(ErrorConflict, operation, "executor_environment", environment.ID, "environment identity is already in use")
			}
			return databaseError(operation+" insert environment", err)
		}
	}
	return nil
}

func scanExecutorEnrollmentToken(row pgx.Row) (ExecutorEnrollmentToken, error) {
	var token ExecutorEnrollmentToken
	err := row.Scan(
		&token.ID, &token.WorkspaceID, &token.ExecutorID, &token.IssuedBy,
		&token.IdempotencyKey, &token.IssuedAt, &token.ExpiresAt, &token.ClaimedAt,
		&token.ConsumedAt, &token.RevokedAt, &token.EnrollmentSHA256, &token.Version,
	)
	return token, err
}

func enrollmentTokenMatchesClaims(token ExecutorEnrollmentToken, command ClaimExecutorEnrollmentCommand) error {
	if token.ID != command.TokenID || token.WorkspaceID != command.WorkspaceID || token.ExecutorID != command.ExecutorID || token.IssuedBy != command.IssuedByActorID {
		return errors.New("enrollment token scope does not match signed claims")
	}
	if token.IssuedAt.UnixMilli() != command.IssuedAt.UnixMilli() || token.ExpiresAt.UnixMilli() != command.ExpiresAt.UnixMilli() {
		return errors.New("enrollment token time authority does not match signed claims")
	}
	return nil
}

func validateCreateExecutorResource(command CreateExecutorResourceCommand) error {
	for field, value := range map[string]string{"executor_id": command.ExecutorID, "workspace_id": command.WorkspaceID, "actor_id": command.ActorID} {
		if err := validateUUID(field, value); err != nil {
			return err
		}
	}
	return nil
}

func validateIssueExecutorEnrollmentToken(command IssueExecutorEnrollmentTokenCommand) (int64, error) {
	for field, value := range map[string]string{
		"token_id": command.TokenID, "executor_id": command.ExecutorID,
		"workspace_id": command.WorkspaceID, "actor_id": command.ActorID,
	} {
		if err := validateUUID(field, value); err != nil {
			return 0, err
		}
	}
	if err := validateBoundedText("idempotency_key", command.IdempotencyKey, 256); err != nil {
		return 0, err
	}
	return durationMilliseconds("enrollment_ttl", command.TTL, MaxExecutorEnrollmentTTL)
}

func validateClaimExecutorEnrollment(command ClaimExecutorEnrollmentCommand) error {
	for field, value := range map[string]string{
		"token_id": command.TokenID, "executor_id": command.ExecutorID,
		"workspace_id": command.WorkspaceID, "issued_by_actor_id": command.IssuedByActorID,
	} {
		if err := validateUUID(field, value); err != nil {
			return err
		}
	}
	if command.IssuedAt.IsZero() || command.ExpiresAt.IsZero() || !command.ExpiresAt.After(command.IssuedAt) || command.ExpiresAt.Sub(command.IssuedAt) > MaxExecutorEnrollmentTTL {
		return errors.New("signed enrollment token time window is invalid")
	}
	computedKeyHash := sha256.Sum256(command.MachinePublicKeyEd25519[:])
	if command.MachinePublicKeyEd25519 == [32]byte{} || command.MachineKeySHA256 != computedKeyHash {
		return errors.New("machine public key and fingerprint do not match")
	}
	if command.OAuthPublicKeyP256X == [32]byte{} || command.OAuthPublicKeyP256Y == [32]byte{} ||
		!elliptic.P256().IsOnCurve(new(big.Int).SetBytes(command.OAuthPublicKeyP256X[:]), new(big.Int).SetBytes(command.OAuthPublicKeyP256Y[:])) {
		return errors.New("OAuth public key coordinates are not a valid P-256 point")
	}
	encodedX := base64.RawURLEncoding.EncodeToString(command.OAuthPublicKeyP256X[:])
	encodedY := base64.RawURLEncoding.EncodeToString(command.OAuthPublicKeyP256Y[:])
	expectedOAuthHash := sha256.Sum256([]byte(`{"crv":"P-256","kty":"EC","x":"` + encodedX + `","y":"` + encodedY + `"}`))
	if command.OAuthKeySHA256 != expectedOAuthHash {
		return errors.New("OAuth public key and RFC 7638 fingerprint do not match")
	}
	if command.OAuthClientID != "agentserver-executor-"+command.ExecutorID {
		return errors.New("OAuth client ID is not the deterministic executor client identity")
	}
	if err := validateBoundedText("agentx_version", command.AgentxVersion, 256); err != nil {
		return err
	}
	if isZeroDigest(command.RuntimeManifestSHA256) || isZeroDigest(command.ExecProtocolSourceSHA256) || isZeroDigest(command.EnrollmentRequestSHA256) {
		return errors.New("runtime, protocol, and enrollment request digests must not be all zeroes")
	}
	if len(command.Environments) < 1 || len(command.Environments) > 256 {
		return errors.New("enrollment environments must contain between 1 and 256 entries")
	}
	declarations := make([]ExecutorEnvironmentDeclaration, len(command.Environments))
	seen := make(map[string]struct{}, len(command.Environments))
	for index, environment := range command.Environments {
		declarations[index] = environment.ExecutorEnvironmentDeclaration
		if environment.InsecureDev {
			return fmt.Errorf("environment %d: production enrollment cannot declare insecure_dev", index)
		}
		if isZeroDigest(environment.OwnerPolicySHA256) {
			return fmt.Errorf("environment %d: owner policy digest must not be all zeroes", index)
		}
		if len(environment.RootDescriptor) < 2 || len(environment.RootDescriptor) > 64*1024 || !json.Valid(environment.RootDescriptor) {
			return fmt.Errorf("environment %d: root descriptor must be a bounded JSON value", index)
		}
		var object map[string]any
		if err := json.Unmarshal(environment.RootDescriptor, &object); err != nil || object == nil {
			return fmt.Errorf("environment %d: root descriptor must be a JSON object", index)
		}
		if _, duplicate := seen[environment.ID]; duplicate {
			return fmt.Errorf("environment %d: duplicate environment ID", index)
		}
		seen[environment.ID] = struct{}{}
	}
	return validateExecutorEnvironmentDeclarations(declarations)
}

func validateCompleteExecutorEnrollment(command CompleteExecutorEnrollmentCommand) error {
	for field, value := range map[string]string{"token_id": command.TokenID, "executor_id": command.ExecutorID, "workspace_id": command.WorkspaceID} {
		if err := validateUUID(field, value); err != nil {
			return err
		}
	}
	if isZeroDigest(command.EnrollmentRequestSHA256) {
		return errors.New("enrollment request digest must not be all zeroes")
	}
	return nil
}
