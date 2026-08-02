package coredb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	LLMGatewayStatusActive   = "active"
	LLMGatewayStatusDisabled = "disabled"

	LLMGatewayGrantStatusActive         = "active"
	LLMGatewayGrantStatusReauthRequired = "reauth_required"
	LLMGatewayGrantStatusRevoked        = "revoked"

	LLMGatewayAuthStatusPending         = "pending"
	LLMGatewayAuthStatusCallbackClaimed = "callback_claimed"
	LLMGatewayAuthStatusCompleted       = "completed"
	LLMGatewayAuthStatusFailed          = "failed"
	LLMGatewayAuthStatusExpired         = "expired"

	LLMGatewayBearerIDToken     = "id_token"
	LLMGatewayBearerAccessToken = "access_token"

	MaxLLMGatewayAuthTransactionTTL = 10 * time.Minute
)

type WorkspaceLLMGateway struct {
	ID              string
	WorkspaceID     string
	Name            string
	ResponsesURL    string
	OIDCIssuer      string
	OIDCClientID    string
	OIDCScopes      string
	BearerTokenType string
	DefaultModel    string
	Status          string
	Default         bool
	Version         int64
	CreatedBy       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	GrantStatus     string
	GrantExpiresAt  *time.Time
}

type CreateWorkspaceLLMGatewayCommand struct {
	ID              string
	WorkspaceID     string
	ActorID         string
	Name            string
	ResponsesURL    string
	OIDCIssuer      string
	OIDCClientID    string
	OIDCScopes      string
	BearerTokenType string
	DefaultModel    string
	MakeDefault     bool
}

type CreateWorkspaceLLMGatewayResult struct {
	Gateway WorkspaceLLMGateway
	Created bool
}

type WorkspaceLLMGatewayGrant struct {
	ID              string
	GatewayID       string
	WorkspaceID     string
	UserID          string
	OIDCIssuer      string
	OIDCSubject     string
	Status          string
	SealedTokenSet  []byte
	BearerExpiresAt time.Time
	LastRefreshedAt *time.Time
	Version         int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type WorkspaceLLMGatewayAuthTransaction struct {
	ID                   string
	WorkspaceID          string
	GatewayID            string
	GatewayVersion       int64
	UserID               string
	OIDCStateSHA256      [sha256.Size]byte
	BrowserBindingSHA256 [sha256.Size]byte
	SealedSecrets        []byte
	Status               string
	FailureCode          string
	ExpiresAt            time.Time
	CallbackClaimedAt    *time.Time
	CompletedAt          *time.Time
	Version              int64
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type CreateWorkspaceLLMGatewayAuthTransactionCommand struct {
	ID                   string
	WorkspaceID          string
	GatewayID            string
	GatewayVersion       int64
	UserID               string
	OIDCStateSHA256      [sha256.Size]byte
	BrowserBindingSHA256 [sha256.Size]byte
	SealedSecrets        []byte
	TTL                  time.Duration
}

type ClaimWorkspaceLLMGatewayAuthTransactionCommand struct {
	WorkspaceID          string
	GatewayID            string
	UserID               string
	OIDCStateSHA256      [sha256.Size]byte
	BrowserBindingSHA256 [sha256.Size]byte
}

type CompleteWorkspaceLLMGatewayAuthTransactionCommand struct {
	TransactionID   string
	ExpectedVersion int64
	GrantID         string
	OIDCIssuer      string
	OIDCSubject     string
	SealedTokenSet  []byte
	BearerExpiresAt time.Time
}

type FailWorkspaceLLMGatewayAuthTransactionCommand struct {
	TransactionID   string
	ExpectedVersion int64
	Status          string
	FailureCode     string
}

type RevokeWorkspaceLLMGatewayGrantCommand struct {
	WorkspaceID string
	GatewayID   string
	UserID      string
}

type RevokeWorkspaceLLMGatewayGrantResult struct {
	Grant   WorkspaceLLMGatewayGrant
	Changed bool
}

type DisableWorkspaceLLMGatewayCommand struct {
	WorkspaceID string
	GatewayID   string
	ActorID     string
}

type DisableWorkspaceLLMGatewayResult struct {
	Gateway WorkspaceLLMGateway
	Changed bool
}

type RunLLMGatewayBinding struct {
	GatewayID     string
	ConfigVersion int64
	GrantUserID   string
	Model         string
}

type ResolveUserRunLLMGatewayBindingCommand struct {
	WorkspaceID    string
	SessionID      string
	ActorID        string
	IdempotencyKey string
}

type LLMGatewayLiveAuthority struct {
	Gateway WorkspaceLLMGateway
	Grant   WorkspaceLLMGatewayGrant
	Model   string
}

// ReadWorkspaceLLMGatewayLiveAuthority is the retry boundary used after an
// optimistic grant refresh races another Core replica. It rechecks the active
// workspace, current owner/developer membership, exact frozen gateway version,
// and active per-user grant; it never returns a grant merely by its row ID.
func (s *StateStore) ReadWorkspaceLLMGatewayLiveAuthority(
	ctx context.Context,
	workspaceID string,
	binding RunLLMGatewayBinding,
) (LLMGatewayLiveAuthority, error) {
	const operation = "ReadWorkspaceLLMGatewayLiveAuthority"
	if err := validateUUID("workspace_id", workspaceID); err != nil {
		return LLMGatewayLiveAuthority{}, commandError(ErrorInvalidArgument, operation, "workspace", workspaceID, err.Error())
	}
	if err := validateRunLLMGatewayBinding(binding); err != nil {
		return LLMGatewayLiveAuthority{}, commandError(ErrorInvalidArgument, operation, "llm_gateway", binding.GatewayID, err.Error())
	}
	return withStateReadTransaction(ctx, s, operation, func(transaction pgx.Tx) (LLMGatewayLiveAuthority, error) {
		return s.readWorkspaceLLMGatewayLiveAuthority(ctx, transaction, operation, workspaceID, binding)
	})
}

func (s *StateStore) CreateWorkspaceLLMGateway(
	ctx context.Context,
	command CreateWorkspaceLLMGatewayCommand,
) (CreateWorkspaceLLMGatewayResult, error) {
	const operation = "CreateWorkspaceLLMGateway"
	if err := validateCreateWorkspaceLLMGateway(command); err != nil {
		return CreateWorkspaceLLMGatewayResult{}, commandError(ErrorInvalidArgument, operation, "llm_gateway", command.ID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (CreateWorkspaceLLMGatewayResult, error) {
		if err := s.requireWorkspaceLLMGatewayRole(ctx, transaction, operation, command.WorkspaceID, command.ActorID, true, true); err != nil {
			return CreateWorkspaceLLMGatewayResult{}, err
		}
		existing, found, err := s.readWorkspaceLLMGatewayByID(ctx, transaction, command.ID, command.WorkspaceID, command.ActorID, true)
		if err != nil {
			return CreateWorkspaceLLMGatewayResult{}, err
		}
		if found {
			if !workspaceLLMGatewayMatchesCreate(existing, command) {
				return CreateWorkspaceLLMGatewayResult{}, commandError(ErrorIdempotencyConflict, operation, "llm_gateway", command.ID, "gateway identity is already bound to different configuration")
			}
			return CreateWorkspaceLLMGatewayResult{Gateway: existing, Created: false}, nil
		}
		if command.MakeDefault {
			query := fmt.Sprintf(`
UPDATE %s
SET is_default = FALSE,
    updated_at = pg_catalog.clock_timestamp()
WHERE workspace_id = $1 AND status = $2 AND is_default = TRUE`, s.table("workspace_llm_gateways"))
			if _, err := transaction.Exec(ctx, query, command.WorkspaceID, LLMGatewayStatusActive); err != nil {
				return CreateWorkspaceLLMGatewayResult{}, databaseError(operation+" clear prior default", err)
			}
		}
		query := fmt.Sprintf(`
INSERT INTO %s
    (id, workspace_id, name, responses_url, oidc_issuer, oidc_client_id,
     oidc_scopes, bearer_token_type, default_model, status, is_default, created_by)
VALUES
    ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING %s`, s.table("workspace_llm_gateways"), workspaceLLMGatewayColumns(""))
		gateway, err := scanWorkspaceLLMGateway(transaction.QueryRow(
			ctx, query, command.ID, command.WorkspaceID, command.Name,
			command.ResponsesURL, command.OIDCIssuer, command.OIDCClientID,
			command.OIDCScopes, command.BearerTokenType, command.DefaultModel,
			LLMGatewayStatusActive, command.MakeDefault, command.ActorID,
		))
		if err != nil {
			var postgresError *pgconn.PgError
			if pgxErrorAs(err, &postgresError) && postgresError.Code == "23505" {
				return CreateWorkspaceLLMGatewayResult{}, commandError(ErrorConflict, operation, "llm_gateway", command.ID, "gateway name, identity, or default is already in use")
			}
			return CreateWorkspaceLLMGatewayResult{}, databaseError(operation+" insert", err)
		}
		return CreateWorkspaceLLMGatewayResult{Gateway: gateway, Created: true}, nil
	})
}

func (s *StateStore) ListWorkspaceLLMGateways(
	ctx context.Context,
	workspaceID, userID string,
) ([]WorkspaceLLMGateway, error) {
	const operation = "ListWorkspaceLLMGateways"
	if err := validateUUID("workspace_id", workspaceID); err != nil {
		return nil, commandError(ErrorInvalidArgument, operation, "workspace", workspaceID, err.Error())
	}
	if err := validateUUID("user_id", userID); err != nil {
		return nil, commandError(ErrorInvalidArgument, operation, "user", userID, err.Error())
	}
	return withStateReadTransaction(ctx, s, operation, func(transaction pgx.Tx) ([]WorkspaceLLMGateway, error) {
		if err := s.requireWorkspaceLLMGatewayRole(ctx, transaction, operation, workspaceID, userID, false, false); err != nil {
			return nil, err
		}
		query := fmt.Sprintf(`
SELECT %s, COALESCE(grant_state.status, ''), grant_state.bearer_expires_at
FROM %s AS gateway
LEFT JOIN %s AS grant_state
  ON grant_state.gateway_id = gateway.id
 AND grant_state.workspace_id = gateway.workspace_id
 AND grant_state.user_id = $2
WHERE gateway.workspace_id = $1
ORDER BY gateway.is_default DESC, gateway.name, gateway.id`,
			workspaceLLMGatewayColumns("gateway"), s.table("workspace_llm_gateways"), s.table("workspace_llm_gateway_grants"))
		rows, err := transaction.Query(ctx, query, workspaceID, userID)
		if err != nil {
			return nil, databaseError(operation+" query", err)
		}
		defer rows.Close()
		gateways := make([]WorkspaceLLMGateway, 0)
		for rows.Next() {
			gateway, err := scanWorkspaceLLMGatewayWithGrant(rows)
			if err != nil {
				return nil, databaseError(operation+" scan", err)
			}
			gateways = append(gateways, gateway)
		}
		if err := rows.Err(); err != nil {
			return nil, databaseError(operation+" finish", err)
		}
		return gateways, nil
	})
}

// RequireWorkspaceLLMGatewayOwner is the preflight authorization boundary
// used before owner-requested configuration performs OIDC discovery. The
// eventual write transaction must still recheck the role to close membership
// changes between this read and the external request.
func (s *StateStore) RequireWorkspaceLLMGatewayOwner(
	ctx context.Context,
	workspaceID, userID string,
) error {
	const operation = "RequireWorkspaceLLMGatewayOwner"
	if err := validateUUID("workspace_id", workspaceID); err != nil {
		return commandError(ErrorInvalidArgument, operation, "workspace", workspaceID, err.Error())
	}
	if err := validateUUID("user_id", userID); err != nil {
		return commandError(ErrorInvalidArgument, operation, "user", userID, err.Error())
	}
	_, err := withStateReadTransaction(ctx, s, operation, func(transaction pgx.Tx) (bool, error) {
		if err := s.requireWorkspaceLLMGatewayRole(ctx, transaction, operation, workspaceID, userID, true, false); err != nil {
			return false, err
		}
		return true, nil
	})
	return err
}

func (s *StateStore) ReadWorkspaceLLMGatewayForAuthorization(
	ctx context.Context,
	workspaceID, gatewayID, userID string,
) (WorkspaceLLMGateway, error) {
	const operation = "ReadWorkspaceLLMGatewayForAuthorization"
	for field, value := range map[string]string{"workspace_id": workspaceID, "gateway_id": gatewayID, "user_id": userID} {
		if err := validateUUID(field, value); err != nil {
			return WorkspaceLLMGateway{}, commandError(ErrorInvalidArgument, operation, "llm_gateway", gatewayID, err.Error())
		}
	}
	return withStateReadTransaction(ctx, s, operation, func(transaction pgx.Tx) (WorkspaceLLMGateway, error) {
		if err := s.requireWorkspaceLLMGatewayRole(ctx, transaction, operation, workspaceID, userID, false, false); err != nil {
			return WorkspaceLLMGateway{}, err
		}
		gateway, found, err := s.readWorkspaceLLMGatewayByID(ctx, transaction, gatewayID, workspaceID, userID, false)
		if err != nil {
			return WorkspaceLLMGateway{}, err
		}
		if !found || gateway.Status != LLMGatewayStatusActive {
			return WorkspaceLLMGateway{}, commandError(ErrorNotFound, operation, "llm_gateway", gatewayID, "active workspace LLM gateway does not exist")
		}
		return gateway, nil
	})
}

func (s *StateStore) CreateWorkspaceLLMGatewayAuthTransaction(
	ctx context.Context,
	command CreateWorkspaceLLMGatewayAuthTransactionCommand,
) (WorkspaceLLMGatewayAuthTransaction, error) {
	const operation = "CreateWorkspaceLLMGatewayAuthTransaction"
	if err := validateCreateWorkspaceLLMGatewayAuthTransaction(command); err != nil {
		return WorkspaceLLMGatewayAuthTransaction{}, commandError(ErrorInvalidArgument, operation, "llm_gateway_auth", command.ID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (WorkspaceLLMGatewayAuthTransaction, error) {
		if err := s.requireWorkspaceLLMGatewayRole(ctx, transaction, operation, command.WorkspaceID, command.UserID, false, true); err != nil {
			return WorkspaceLLMGatewayAuthTransaction{}, err
		}
		var status string
		var version int64
		gatewayQuery := fmt.Sprintf("SELECT status, version FROM %s WHERE id = $1 AND workspace_id = $2 FOR SHARE", s.table("workspace_llm_gateways"))
		if err := transaction.QueryRow(ctx, gatewayQuery, command.GatewayID, command.WorkspaceID).Scan(&status, &version); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return WorkspaceLLMGatewayAuthTransaction{}, commandError(ErrorNotFound, operation, "llm_gateway", command.GatewayID, "workspace LLM gateway does not exist")
			}
			return WorkspaceLLMGatewayAuthTransaction{}, databaseError(operation+" read gateway", err)
		}
		if status != LLMGatewayStatusActive || version != command.GatewayVersion {
			return WorkspaceLLMGatewayAuthTransaction{}, commandError(ErrorConflict, operation, "llm_gateway", command.GatewayID, "gateway changed before authorization transaction creation")
		}
		query := fmt.Sprintf(`
INSERT INTO %s
    (id, workspace_id, gateway_id, gateway_version, user_id,
     oidc_state_sha256, browser_binding_sha256, sealed_secrets, status, expires_at)
VALUES
    ($1, $2, $3, $4, $5, $6, $7, $8, $9,
     pg_catalog.clock_timestamp() + ($10::bigint * interval '1 millisecond'))
RETURNING %s`, s.table("workspace_llm_gateway_auth_transactions"), workspaceLLMGatewayAuthColumns(""))
		result, err := scanWorkspaceLLMGatewayAuthTransaction(transaction.QueryRow(
			ctx, query, command.ID, command.WorkspaceID, command.GatewayID, command.GatewayVersion,
			command.UserID, command.OIDCStateSHA256[:], command.BrowserBindingSHA256[:],
			command.SealedSecrets, LLMGatewayAuthStatusPending, command.TTL.Milliseconds(),
		))
		if err != nil {
			var postgresError *pgconn.PgError
			if pgxErrorAs(err, &postgresError) && postgresError.Code == "23505" {
				return WorkspaceLLMGatewayAuthTransaction{}, commandError(ErrorIdempotencyConflict, operation, "llm_gateway_auth", command.ID, "authorization transaction identity or state is already in use")
			}
			return WorkspaceLLMGatewayAuthTransaction{}, databaseError(operation+" insert", err)
		}
		return result, nil
	})
}

func (s *StateStore) ClaimWorkspaceLLMGatewayAuthTransaction(
	ctx context.Context,
	command ClaimWorkspaceLLMGatewayAuthTransactionCommand,
) (WorkspaceLLMGatewayAuthTransaction, error) {
	const operation = "ClaimWorkspaceLLMGatewayAuthTransaction"
	if err := validateClaimWorkspaceLLMGatewayAuthTransaction(command); err != nil {
		return WorkspaceLLMGatewayAuthTransaction{}, commandError(ErrorInvalidArgument, operation, "llm_gateway_auth", "", err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (WorkspaceLLMGatewayAuthTransaction, error) {
		query := fmt.Sprintf(`
SELECT %s
FROM %s
WHERE oidc_state_sha256 = $1
FOR UPDATE`, workspaceLLMGatewayAuthColumns(""), s.table("workspace_llm_gateway_auth_transactions"))
		current, err := scanWorkspaceLLMGatewayAuthTransaction(transaction.QueryRow(ctx, query, command.OIDCStateSHA256[:]))
		if errors.Is(err, pgx.ErrNoRows) {
			return WorkspaceLLMGatewayAuthTransaction{}, commandError(ErrorNotFound, operation, "llm_gateway_auth", "", "authorization transaction does not exist")
		}
		if err != nil {
			return WorkspaceLLMGatewayAuthTransaction{}, databaseError(operation+" read", err)
		}
		if current.WorkspaceID != command.WorkspaceID || current.GatewayID != command.GatewayID || current.UserID != command.UserID ||
			!bytes.Equal(current.BrowserBindingSHA256[:], command.BrowserBindingSHA256[:]) {
			return WorkspaceLLMGatewayAuthTransaction{}, commandError(ErrorForbidden, operation, "llm_gateway_auth", current.ID, "authorization transaction scope does not match")
		}
		if err := s.requireWorkspaceLLMGatewayRole(ctx, transaction, operation, command.WorkspaceID, command.UserID, false, true); err != nil {
			return WorkspaceLLMGatewayAuthTransaction{}, err
		}
		if current.Status != LLMGatewayAuthStatusPending {
			return WorkspaceLLMGatewayAuthTransaction{}, commandError(ErrorIdempotencyConflict, operation, "llm_gateway_auth", current.ID, "authorization callback state was already consumed")
		}
		var live bool
		if err := transaction.QueryRow(ctx, "SELECT $1 > pg_catalog.clock_timestamp()", current.ExpiresAt).Scan(&live); err != nil {
			return WorkspaceLLMGatewayAuthTransaction{}, databaseError(operation+" compare expiry", err)
		}
		if !live {
			return s.finishWorkspaceLLMGatewayAuth(ctx, transaction, operation, current, LLMGatewayAuthStatusExpired, "authorization_transaction_expired")
		}
		update := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    callback_claimed_at = pg_catalog.clock_timestamp(),
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $2 AND version = $3 AND status = $4
RETURNING %s`, s.table("workspace_llm_gateway_auth_transactions"), workspaceLLMGatewayAuthColumns(""))
		claimed, err := scanWorkspaceLLMGatewayAuthTransaction(transaction.QueryRow(
			ctx, update, LLMGatewayAuthStatusCallbackClaimed, current.ID, current.Version, LLMGatewayAuthStatusPending,
		))
		if errors.Is(err, pgx.ErrNoRows) {
			return WorkspaceLLMGatewayAuthTransaction{}, versionConflict(operation, "llm_gateway_auth", current.ID, current.Version)
		}
		if err != nil {
			return WorkspaceLLMGatewayAuthTransaction{}, databaseError(operation+" claim", err)
		}
		return claimed, nil
	})
}

func (s *StateStore) CompleteWorkspaceLLMGatewayAuthTransaction(
	ctx context.Context,
	command CompleteWorkspaceLLMGatewayAuthTransactionCommand,
) (WorkspaceLLMGatewayGrant, error) {
	const operation = "CompleteWorkspaceLLMGatewayAuthTransaction"
	if err := validateCompleteWorkspaceLLMGatewayAuthTransaction(command); err != nil {
		return WorkspaceLLMGatewayGrant{}, commandError(ErrorInvalidArgument, operation, "llm_gateway_auth", command.TransactionID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (WorkspaceLLMGatewayGrant, error) {
		query := fmt.Sprintf("SELECT %s FROM %s WHERE id = $1 FOR UPDATE", workspaceLLMGatewayAuthColumns(""), s.table("workspace_llm_gateway_auth_transactions"))
		current, err := scanWorkspaceLLMGatewayAuthTransaction(transaction.QueryRow(ctx, query, command.TransactionID))
		if errors.Is(err, pgx.ErrNoRows) {
			return WorkspaceLLMGatewayGrant{}, commandError(ErrorNotFound, operation, "llm_gateway_auth", command.TransactionID, "authorization transaction does not exist")
		}
		if err != nil {
			return WorkspaceLLMGatewayGrant{}, databaseError(operation+" read transaction", err)
		}
		if current.Status != LLMGatewayAuthStatusCallbackClaimed || current.Version != command.ExpectedVersion {
			return WorkspaceLLMGatewayGrant{}, commandError(ErrorConflict, operation, "llm_gateway_auth", current.ID, "authorization transaction is not claimable at the expected version")
		}
		if err := s.requireWorkspaceLLMGatewayRole(ctx, transaction, operation, current.WorkspaceID, current.UserID, false, true); err != nil {
			return WorkspaceLLMGatewayGrant{}, err
		}
		gatewayQuery := fmt.Sprintf("SELECT oidc_issuer, status, version FROM %s WHERE id = $1 AND workspace_id = $2 FOR SHARE", s.table("workspace_llm_gateways"))
		var issuer, status string
		var version int64
		if err := transaction.QueryRow(ctx, gatewayQuery, current.GatewayID, current.WorkspaceID).Scan(&issuer, &status, &version); err != nil {
			return WorkspaceLLMGatewayGrant{}, databaseError(operation+" read gateway", err)
		}
		if status != LLMGatewayStatusActive || version != current.GatewayVersion || issuer != command.OIDCIssuer {
			return WorkspaceLLMGatewayGrant{}, commandError(ErrorConflict, operation, "llm_gateway", current.GatewayID, "gateway changed during authorization")
		}
		grantQuery := fmt.Sprintf(`
INSERT INTO %s AS current_grant
    (id, gateway_id, workspace_id, user_id, oidc_issuer, oidc_subject,
     status, sealed_token_set, bearer_expires_at, last_refreshed_at)
VALUES
    ($1, $2, $3, $4, $5, $6, $7, $8, $9, pg_catalog.clock_timestamp())
ON CONFLICT (gateway_id, user_id) DO UPDATE
SET oidc_issuer = EXCLUDED.oidc_issuer,
    oidc_subject = EXCLUDED.oidc_subject,
    status = EXCLUDED.status,
    sealed_token_set = EXCLUDED.sealed_token_set,
    bearer_expires_at = EXCLUDED.bearer_expires_at,
    last_refreshed_at = pg_catalog.clock_timestamp(),
    version = current_grant.version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE current_grant.workspace_id = EXCLUDED.workspace_id
RETURNING %s`, s.table("workspace_llm_gateway_grants"), workspaceLLMGatewayGrantColumns(""))
		grant, err := scanWorkspaceLLMGatewayGrant(transaction.QueryRow(
			ctx, grantQuery, command.GrantID, current.GatewayID, current.WorkspaceID, current.UserID,
			command.OIDCIssuer, command.OIDCSubject, LLMGatewayGrantStatusActive,
			command.SealedTokenSet, command.BearerExpiresAt,
		))
		if err != nil {
			return WorkspaceLLMGatewayGrant{}, databaseError(operation+" upsert grant", err)
		}
		complete := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    completed_at = pg_catalog.clock_timestamp(),
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $2 AND version = $3 AND status = $4`, s.table("workspace_llm_gateway_auth_transactions"))
		result, err := transaction.Exec(ctx, complete, LLMGatewayAuthStatusCompleted, current.ID, current.Version, LLMGatewayAuthStatusCallbackClaimed)
		if err != nil {
			return WorkspaceLLMGatewayGrant{}, databaseError(operation+" complete transaction", err)
		}
		if result.RowsAffected() != 1 {
			return WorkspaceLLMGatewayGrant{}, versionConflict(operation, "llm_gateway_auth", current.ID, current.Version)
		}
		return grant, nil
	})
}

func (s *StateStore) FailWorkspaceLLMGatewayAuthTransaction(
	ctx context.Context,
	command FailWorkspaceLLMGatewayAuthTransactionCommand,
) (WorkspaceLLMGatewayAuthTransaction, error) {
	const operation = "FailWorkspaceLLMGatewayAuthTransaction"
	if err := validateFailWorkspaceLLMGatewayAuthTransaction(command); err != nil {
		return WorkspaceLLMGatewayAuthTransaction{}, commandError(ErrorInvalidArgument, operation, "llm_gateway_auth", command.TransactionID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (WorkspaceLLMGatewayAuthTransaction, error) {
		query := fmt.Sprintf("SELECT %s FROM %s WHERE id = $1 FOR UPDATE", workspaceLLMGatewayAuthColumns(""), s.table("workspace_llm_gateway_auth_transactions"))
		current, err := scanWorkspaceLLMGatewayAuthTransaction(transaction.QueryRow(ctx, query, command.TransactionID))
		if err != nil {
			return WorkspaceLLMGatewayAuthTransaction{}, databaseError(operation+" read", err)
		}
		if current.Version != command.ExpectedVersion || current.Status != LLMGatewayAuthStatusCallbackClaimed {
			return WorkspaceLLMGatewayAuthTransaction{}, commandError(ErrorConflict, operation, "llm_gateway_auth", current.ID, "authorization transaction cannot be failed at the expected version")
		}
		return s.finishWorkspaceLLMGatewayAuth(ctx, transaction, operation, current, command.Status, command.FailureCode)
	})
}

func (s *StateStore) RevokeWorkspaceLLMGatewayGrant(
	ctx context.Context,
	command RevokeWorkspaceLLMGatewayGrantCommand,
) (RevokeWorkspaceLLMGatewayGrantResult, error) {
	const operation = "RevokeWorkspaceLLMGatewayGrant"
	for field, value := range map[string]string{"workspace_id": command.WorkspaceID, "gateway_id": command.GatewayID, "user_id": command.UserID} {
		if err := validateUUID(field, value); err != nil {
			return RevokeWorkspaceLLMGatewayGrantResult{}, commandError(ErrorInvalidArgument, operation, "llm_gateway", command.GatewayID, err.Error())
		}
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (RevokeWorkspaceLLMGatewayGrantResult, error) {
		if err := s.requireWorkspaceLLMGatewayRole(ctx, transaction, operation, command.WorkspaceID, command.UserID, false, true); err != nil {
			return RevokeWorkspaceLLMGatewayGrantResult{}, err
		}
		query := fmt.Sprintf("SELECT %s FROM %s WHERE gateway_id = $1 AND workspace_id = $2 AND user_id = $3 FOR UPDATE", workspaceLLMGatewayGrantColumns(""), s.table("workspace_llm_gateway_grants"))
		grant, err := scanWorkspaceLLMGatewayGrant(transaction.QueryRow(ctx, query, command.GatewayID, command.WorkspaceID, command.UserID))
		if errors.Is(err, pgx.ErrNoRows) {
			return RevokeWorkspaceLLMGatewayGrantResult{}, commandError(ErrorNotFound, operation, "llm_gateway_grant", command.GatewayID, "gateway grant does not exist")
		}
		if err != nil {
			return RevokeWorkspaceLLMGatewayGrantResult{}, databaseError(operation+" read", err)
		}
		if grant.Status == LLMGatewayGrantStatusRevoked {
			return RevokeWorkspaceLLMGatewayGrantResult{Grant: grant, Changed: false}, nil
		}
		update := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $2 AND version = $3
RETURNING %s`, s.table("workspace_llm_gateway_grants"), workspaceLLMGatewayGrantColumns(""))
		updated, err := scanWorkspaceLLMGatewayGrant(transaction.QueryRow(ctx, update, LLMGatewayGrantStatusRevoked, grant.ID, grant.Version))
		if err != nil {
			return RevokeWorkspaceLLMGatewayGrantResult{}, databaseError(operation+" update", err)
		}
		return RevokeWorkspaceLLMGatewayGrantResult{Grant: updated, Changed: true}, nil
	})
}

// DisableWorkspaceLLMGateway is an owner-only, idempotent fence. The version
// increment invalidates every run frozen against the former active
// configuration; grants remain sealed audit records but cannot be resolved
// while the Gateway is disabled.
func (s *StateStore) DisableWorkspaceLLMGateway(
	ctx context.Context,
	command DisableWorkspaceLLMGatewayCommand,
) (DisableWorkspaceLLMGatewayResult, error) {
	const operation = "DisableWorkspaceLLMGateway"
	for field, value := range map[string]string{
		"workspace_id": command.WorkspaceID,
		"gateway_id":   command.GatewayID,
		"actor_id":     command.ActorID,
	} {
		if err := validateUUID(field, value); err != nil {
			return DisableWorkspaceLLMGatewayResult{}, commandError(ErrorInvalidArgument, operation, "llm_gateway", command.GatewayID, err.Error())
		}
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (DisableWorkspaceLLMGatewayResult, error) {
		if err := s.requireWorkspaceLLMGatewayRole(ctx, transaction, operation, command.WorkspaceID, command.ActorID, true, true); err != nil {
			return DisableWorkspaceLLMGatewayResult{}, err
		}
		query := fmt.Sprintf(
			"SELECT %s FROM %s WHERE id = $1 AND workspace_id = $2 FOR UPDATE",
			workspaceLLMGatewayColumns(""), s.table("workspace_llm_gateways"),
		)
		gateway, err := scanWorkspaceLLMGateway(transaction.QueryRow(ctx, query, command.GatewayID, command.WorkspaceID))
		if errors.Is(err, pgx.ErrNoRows) {
			return DisableWorkspaceLLMGatewayResult{}, commandError(ErrorNotFound, operation, "llm_gateway", command.GatewayID, "workspace LLM gateway does not exist")
		}
		if err != nil {
			return DisableWorkspaceLLMGatewayResult{}, databaseError(operation+" read", err)
		}
		if gateway.Status == LLMGatewayStatusDisabled {
			return DisableWorkspaceLLMGatewayResult{Gateway: gateway, Changed: false}, nil
		}
		update := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    is_default = FALSE,
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $2 AND workspace_id = $3 AND version = $4 AND status = $5
RETURNING %s`, s.table("workspace_llm_gateways"), workspaceLLMGatewayColumns(""))
		updated, err := scanWorkspaceLLMGateway(transaction.QueryRow(
			ctx, update, LLMGatewayStatusDisabled, command.GatewayID,
			command.WorkspaceID, gateway.Version, LLMGatewayStatusActive,
		))
		if errors.Is(err, pgx.ErrNoRows) {
			return DisableWorkspaceLLMGatewayResult{}, versionConflict(operation, "llm_gateway", command.GatewayID, gateway.Version)
		}
		if err != nil {
			return DisableWorkspaceLLMGatewayResult{}, databaseError(operation+" update", err)
		}
		return DisableWorkspaceLLMGatewayResult{Gateway: updated, Changed: true}, nil
	})
}

func (s *StateStore) ResolveUserRunLLMGatewayBinding(
	ctx context.Context,
	command ResolveUserRunLLMGatewayBindingCommand,
) (RunLLMGatewayBinding, error) {
	const operation = "ResolveUserRunLLMGatewayBinding"
	for field, value := range map[string]string{"workspace_id": command.WorkspaceID, "session_id": command.SessionID, "actor_id": command.ActorID} {
		if err := validateUUID(field, value); err != nil {
			return RunLLMGatewayBinding{}, commandError(ErrorInvalidArgument, operation, "workspace", command.WorkspaceID, err.Error())
		}
	}
	if err := validateBoundedText("idempotency_key", command.IdempotencyKey, 256); err != nil {
		return RunLLMGatewayBinding{}, commandError(ErrorInvalidArgument, operation, "workspace", command.WorkspaceID, err.Error())
	}
	return withStateReadTransaction(ctx, s, operation, func(transaction pgx.Tx) (RunLLMGatewayBinding, error) {
		if err := s.requireWorkspaceLLMGatewayRole(ctx, transaction, operation, command.WorkspaceID, command.ActorID, false, false); err != nil {
			return RunLLMGatewayBinding{}, err
		}
		existingQuery := fmt.Sprintf(`
SELECT launch.llm_gateway_id::text, launch.llm_gateway_version,
       launch.llm_gateway_grant_user_id::text, launch.model
FROM %s AS run
JOIN %s AS launch ON launch.run_id = run.id
WHERE run.workspace_id = $1 AND run.session_id = $2
  AND run.actor_id = $3 AND run.idempotency_key = $4`, s.table("runs"), s.table("run_launch_states"))
		var binding RunLLMGatewayBinding
		if err := transaction.QueryRow(ctx, existingQuery, command.WorkspaceID, command.SessionID, command.ActorID, command.IdempotencyKey).Scan(
			&binding.GatewayID, &binding.ConfigVersion, &binding.GrantUserID, &binding.Model,
		); err == nil {
			if err := validateRunLLMGatewayBinding(binding); err != nil {
				return RunLLMGatewayBinding{}, commandError(ErrorInvalidState, operation, "workspace", command.WorkspaceID, "existing run has no valid workspace LLM gateway binding")
			}
			return binding, nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return RunLLMGatewayBinding{}, databaseError(operation+" read existing binding", err)
		}
		query := fmt.Sprintf(`
SELECT gateway.id::text, gateway.version, grant_state.user_id::text,
       gateway.default_model
FROM %s AS gateway
JOIN %s AS grant_state
  ON grant_state.gateway_id = gateway.id
 AND grant_state.workspace_id = gateway.workspace_id
 AND grant_state.user_id = $2
 AND grant_state.status = $3
WHERE gateway.workspace_id = $1
  AND gateway.status = $4
  AND gateway.is_default = TRUE`, s.table("workspace_llm_gateways"), s.table("workspace_llm_gateway_grants"))
		if err := transaction.QueryRow(
			ctx, query, command.WorkspaceID, command.ActorID,
			LLMGatewayGrantStatusActive, LLMGatewayStatusActive,
		).Scan(&binding.GatewayID, &binding.ConfigVersion, &binding.GrantUserID, &binding.Model); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return RunLLMGatewayBinding{}, commandError(ErrorForbidden, operation, "workspace", command.WorkspaceID, "workspace has no active default LLM gateway grant for this user")
			}
			return RunLLMGatewayBinding{}, databaseError(operation+" resolve default", err)
		}
		return binding, nil
	})
}

func (s *StateStore) requireCreateRunLLMGateway(
	ctx context.Context,
	transaction pgx.Tx,
	command CreateRunCommand,
) error {
	query := fmt.Sprintf(`
SELECT 1
FROM %s AS gateway
JOIN %s AS grant_state
  ON grant_state.gateway_id = gateway.id
 AND grant_state.workspace_id = gateway.workspace_id
 AND grant_state.user_id = $4
 AND grant_state.status = $7
WHERE gateway.id = $1
  AND gateway.workspace_id = $2
  AND gateway.version = $3
  AND gateway.status = $6
  AND gateway.default_model = $5
FOR SHARE OF gateway, grant_state`, s.table("workspace_llm_gateways"), s.table("workspace_llm_gateway_grants"))
	var marker int
	if err := transaction.QueryRow(
		ctx, query, command.LLMGateway.GatewayID, command.WorkspaceID,
		command.LLMGateway.ConfigVersion, command.ActorID, command.LLMGateway.Model,
		LLMGatewayStatusActive, LLMGatewayGrantStatusActive,
	).Scan(&marker); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return commandError(ErrorForbidden, "CreateRun", "llm_gateway", command.LLMGateway.GatewayID, "frozen workspace LLM gateway grant is no longer active")
		}
		return databaseError("CreateRun authorize LLM gateway", err)
	}
	return nil
}

func (s *StateStore) UpdateWorkspaceLLMGatewayGrantTokens(
	ctx context.Context,
	grantID string,
	expectedVersion int64,
	sealedTokenSet []byte,
	bearerExpiresAt time.Time,
) (WorkspaceLLMGatewayGrant, error) {
	const operation = "UpdateWorkspaceLLMGatewayGrantTokens"
	if err := validateUUID("grant_id", grantID); err != nil || expectedVersion < 1 || expectedVersion > maxSafeJSONInteger ||
		len(sealedTokenSet) < 29 || len(sealedTokenSet) > 262144 || bearerExpiresAt.IsZero() {
		return WorkspaceLLMGatewayGrant{}, commandError(ErrorInvalidArgument, operation, "llm_gateway_grant", grantID, "grant refresh input is invalid")
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (WorkspaceLLMGatewayGrant, error) {
		query := fmt.Sprintf(`
UPDATE %s
SET sealed_token_set = $1,
    bearer_expires_at = $2,
    last_refreshed_at = pg_catalog.clock_timestamp(),
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $3 AND version = $4 AND status = $5
RETURNING %s`, s.table("workspace_llm_gateway_grants"), workspaceLLMGatewayGrantColumns(""))
		grant, err := scanWorkspaceLLMGatewayGrant(transaction.QueryRow(
			ctx, query, sealedTokenSet, bearerExpiresAt, grantID, expectedVersion, LLMGatewayGrantStatusActive,
		))
		if errors.Is(err, pgx.ErrNoRows) {
			return WorkspaceLLMGatewayGrant{}, versionConflict(operation, "llm_gateway_grant", grantID, expectedVersion)
		}
		if err != nil {
			return WorkspaceLLMGatewayGrant{}, databaseError(operation+" update", err)
		}
		return grant, nil
	})
}

func (s *StateStore) MarkWorkspaceLLMGatewayGrantReauthRequired(
	ctx context.Context,
	grantID string,
	expectedVersion int64,
) error {
	const operation = "MarkWorkspaceLLMGatewayGrantReauthRequired"
	if err := validateUUID("grant_id", grantID); err != nil || expectedVersion < 1 || expectedVersion > maxSafeJSONInteger {
		return commandError(ErrorInvalidArgument, operation, "llm_gateway_grant", grantID, "grant version is invalid")
	}
	_, err := withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (bool, error) {
		query := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $2 AND version = $3 AND status = $4`, s.table("workspace_llm_gateway_grants"))
		result, err := transaction.Exec(ctx, query, LLMGatewayGrantStatusReauthRequired, grantID, expectedVersion, LLMGatewayGrantStatusActive)
		if err != nil {
			return false, databaseError(operation+" update", err)
		}
		if result.RowsAffected() != 1 {
			return false, versionConflict(operation, "llm_gateway_grant", grantID, expectedVersion)
		}
		return true, nil
	})
	return err
}

func (s *StateStore) finishWorkspaceLLMGatewayAuth(
	ctx context.Context,
	transaction pgx.Tx,
	operation string,
	current WorkspaceLLMGatewayAuthTransaction,
	status, failureCode string,
) (WorkspaceLLMGatewayAuthTransaction, error) {
	query := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    failure_code = $2,
    completed_at = pg_catalog.clock_timestamp(),
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $3 AND version = $4
RETURNING %s`, s.table("workspace_llm_gateway_auth_transactions"), workspaceLLMGatewayAuthColumns(""))
	result, err := scanWorkspaceLLMGatewayAuthTransaction(transaction.QueryRow(ctx, query, status, failureCode, current.ID, current.Version))
	if errors.Is(err, pgx.ErrNoRows) {
		return WorkspaceLLMGatewayAuthTransaction{}, versionConflict(operation, "llm_gateway_auth", current.ID, current.Version)
	}
	if err != nil {
		return WorkspaceLLMGatewayAuthTransaction{}, databaseError(operation+" finish", err)
	}
	return result, nil
}

func (s *StateStore) requireWorkspaceLLMGatewayRole(
	ctx context.Context,
	transaction pgx.Tx,
	operation, workspaceID, userID string,
	ownerOnly bool,
	lock bool,
) error {
	lockClause := ""
	if lock {
		lockClause = " FOR SHARE OF workspace, member, local_user"
	}
	query := fmt.Sprintf(`
SELECT member.role
FROM %s AS workspace
JOIN %s AS member
  ON member.workspace_id = workspace.id AND member.user_id = $2
JOIN %s AS local_user
  ON local_user.id = member.user_id AND local_user.status = 'active'
WHERE workspace.id = $1 AND workspace.status = 'active'
%s`, s.table("workspaces"), s.table("workspace_members"), s.table("users"), lockClause)
	var role string
	if err := transaction.QueryRow(ctx, query, workspaceID, userID).Scan(&role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return commandError(ErrorForbidden, operation, "workspace", workspaceID, "active workspace membership is required")
		}
		return databaseError(operation+" read membership", err)
	}
	if ownerOnly && role != "owner" {
		return commandError(ErrorForbidden, operation, "workspace", workspaceID, "workspace owner role is required")
	}
	if !ownerOnly && role != "owner" && role != "developer" {
		return commandError(ErrorForbidden, operation, "workspace", workspaceID, "workspace role cannot authorize an LLM gateway")
	}
	return nil
}

func (s *StateStore) readWorkspaceLLMGatewayByID(
	ctx context.Context,
	transaction pgx.Tx,
	gatewayID, workspaceID, userID string,
	lock bool,
) (WorkspaceLLMGateway, bool, error) {
	lockClause := ""
	if lock {
		lockClause = " FOR SHARE OF gateway"
	}
	query := fmt.Sprintf(`
SELECT %s, COALESCE(grant_state.status, ''), grant_state.bearer_expires_at
FROM %s AS gateway
LEFT JOIN %s AS grant_state
  ON grant_state.gateway_id = gateway.id
 AND grant_state.workspace_id = gateway.workspace_id
 AND grant_state.user_id = $3
WHERE gateway.id = $1 AND gateway.workspace_id = $2%s`,
		workspaceLLMGatewayColumns("gateway"), s.table("workspace_llm_gateways"), s.table("workspace_llm_gateway_grants"), lockClause)
	gateway, err := scanWorkspaceLLMGatewayWithGrant(transaction.QueryRow(ctx, query, gatewayID, workspaceID, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return WorkspaceLLMGateway{}, false, nil
	}
	if err != nil {
		return WorkspaceLLMGateway{}, false, databaseError("read workspace LLM gateway", err)
	}
	return gateway, true, nil
}

func (s *StateStore) readWorkspaceLLMGatewayLiveAuthority(
	ctx context.Context,
	transaction pgx.Tx,
	operation, workspaceID string,
	binding RunLLMGatewayBinding,
) (LLMGatewayLiveAuthority, error) {
	query := fmt.Sprintf(`
SELECT %s, %s
FROM %s AS gateway
JOIN %s AS grant_state
  ON grant_state.gateway_id = gateway.id
 AND grant_state.workspace_id = gateway.workspace_id
 AND grant_state.user_id = $4
 AND grant_state.status = $7
JOIN %s AS workspace
  ON workspace.id = gateway.workspace_id AND workspace.status = 'active'
JOIN %s AS member
  ON member.workspace_id = gateway.workspace_id
 AND member.user_id = grant_state.user_id
 AND member.role IN ('owner', 'developer')
JOIN %s AS local_user
  ON local_user.id = grant_state.user_id AND local_user.status = 'active'
WHERE gateway.id = $1
  AND gateway.workspace_id = $2
  AND gateway.version = $3
  AND gateway.default_model = $5
  AND gateway.status = $6`,
		workspaceLLMGatewayColumns("gateway"), workspaceLLMGatewayGrantColumns("grant_state"),
		s.table("workspace_llm_gateways"), s.table("workspace_llm_gateway_grants"),
		s.table("workspaces"), s.table("workspace_members"), s.table("users"),
	)
	authority, err := scanWorkspaceLLMGatewayLiveAuthority(transaction.QueryRow(
		ctx, query, binding.GatewayID, workspaceID, binding.ConfigVersion,
		binding.GrantUserID, binding.Model, LLMGatewayStatusActive, LLMGatewayGrantStatusActive,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return LLMGatewayLiveAuthority{}, commandError(ErrorForbidden, operation, "llm_gateway", binding.GatewayID, "workspace LLM gateway grant is no longer authorized")
	}
	if err != nil {
		return LLMGatewayLiveAuthority{}, databaseError(operation+" read live gateway grant", err)
	}
	authority.Model = binding.Model
	return authority, nil
}

type llmGatewayRow interface {
	Scan(...any) error
}

func workspaceLLMGatewayColumns(alias string) string {
	prefix := ""
	if alias != "" {
		prefix = alias + "."
	}
	return prefix + "id::text, " + prefix + "workspace_id::text, " + prefix + "name, " +
		prefix + "responses_url, " + prefix + "oidc_issuer, " + prefix + "oidc_client_id, " +
		prefix + "oidc_scopes, " + prefix + "bearer_token_type, " + prefix + "default_model, " +
		prefix + "status, " + prefix + "is_default, " + prefix + "version, " +
		prefix + "created_by::text, " + prefix + "created_at, " + prefix + "updated_at"
}

func scanWorkspaceLLMGateway(row llmGatewayRow) (WorkspaceLLMGateway, error) {
	var value WorkspaceLLMGateway
	err := row.Scan(
		&value.ID, &value.WorkspaceID, &value.Name, &value.ResponsesURL,
		&value.OIDCIssuer, &value.OIDCClientID, &value.OIDCScopes,
		&value.BearerTokenType, &value.DefaultModel, &value.Status, &value.Default,
		&value.Version, &value.CreatedBy, &value.CreatedAt, &value.UpdatedAt,
	)
	return value, err
}

func scanWorkspaceLLMGatewayWithGrant(row llmGatewayRow) (WorkspaceLLMGateway, error) {
	var value WorkspaceLLMGateway
	err := row.Scan(
		&value.ID, &value.WorkspaceID, &value.Name, &value.ResponsesURL,
		&value.OIDCIssuer, &value.OIDCClientID, &value.OIDCScopes,
		&value.BearerTokenType, &value.DefaultModel, &value.Status, &value.Default,
		&value.Version, &value.CreatedBy, &value.CreatedAt, &value.UpdatedAt,
		&value.GrantStatus, &value.GrantExpiresAt,
	)
	return value, err
}

func workspaceLLMGatewayGrantColumns(alias string) string {
	prefix := ""
	if alias != "" {
		prefix = alias + "."
	}
	return prefix + "id::text, " + prefix + "gateway_id::text, " + prefix + "workspace_id::text, " +
		prefix + "user_id::text, " + prefix + "oidc_issuer, " + prefix + "oidc_subject, " +
		prefix + "status, " + prefix + "sealed_token_set, " + prefix + "bearer_expires_at, " +
		prefix + "last_refreshed_at, " + prefix + "version, " + prefix + "created_at, " + prefix + "updated_at"
}

func scanWorkspaceLLMGatewayGrant(row llmGatewayRow) (WorkspaceLLMGatewayGrant, error) {
	var value WorkspaceLLMGatewayGrant
	err := row.Scan(
		&value.ID, &value.GatewayID, &value.WorkspaceID, &value.UserID,
		&value.OIDCIssuer, &value.OIDCSubject, &value.Status, &value.SealedTokenSet,
		&value.BearerExpiresAt, &value.LastRefreshedAt, &value.Version,
		&value.CreatedAt, &value.UpdatedAt,
	)
	value.SealedTokenSet = append([]byte(nil), value.SealedTokenSet...)
	return value, err
}

func scanWorkspaceLLMGatewayLiveAuthority(row llmGatewayRow) (LLMGatewayLiveAuthority, error) {
	var value LLMGatewayLiveAuthority
	err := row.Scan(
		&value.Gateway.ID, &value.Gateway.WorkspaceID, &value.Gateway.Name,
		&value.Gateway.ResponsesURL, &value.Gateway.OIDCIssuer, &value.Gateway.OIDCClientID,
		&value.Gateway.OIDCScopes, &value.Gateway.BearerTokenType, &value.Gateway.DefaultModel,
		&value.Gateway.Status, &value.Gateway.Default, &value.Gateway.Version,
		&value.Gateway.CreatedBy, &value.Gateway.CreatedAt, &value.Gateway.UpdatedAt,
		&value.Grant.ID, &value.Grant.GatewayID, &value.Grant.WorkspaceID,
		&value.Grant.UserID, &value.Grant.OIDCIssuer, &value.Grant.OIDCSubject,
		&value.Grant.Status, &value.Grant.SealedTokenSet, &value.Grant.BearerExpiresAt,
		&value.Grant.LastRefreshedAt, &value.Grant.Version,
		&value.Grant.CreatedAt, &value.Grant.UpdatedAt,
	)
	value.Grant.SealedTokenSet = append([]byte(nil), value.Grant.SealedTokenSet...)
	return value, err
}

func workspaceLLMGatewayAuthColumns(alias string) string {
	prefix := ""
	if alias != "" {
		prefix = alias + "."
	}
	return prefix + "id::text, " + prefix + "workspace_id::text, " + prefix + "gateway_id::text, " +
		prefix + "gateway_version, " + prefix + "user_id::text, " + prefix + "oidc_state_sha256, " +
		prefix + "browser_binding_sha256, " + prefix + "sealed_secrets, " + prefix + "status, " +
		prefix + "COALESCE(failure_code, ''), " + prefix + "expires_at, " + prefix + "callback_claimed_at, " +
		prefix + "completed_at, " + prefix + "version, " + prefix + "created_at, " + prefix + "updated_at"
}

func scanWorkspaceLLMGatewayAuthTransaction(row llmGatewayRow) (WorkspaceLLMGatewayAuthTransaction, error) {
	var value WorkspaceLLMGatewayAuthTransaction
	var stateHash, bindingHash []byte
	err := row.Scan(
		&value.ID, &value.WorkspaceID, &value.GatewayID, &value.GatewayVersion,
		&value.UserID, &stateHash, &bindingHash, &value.SealedSecrets,
		&value.Status, &value.FailureCode, &value.ExpiresAt, &value.CallbackClaimedAt,
		&value.CompletedAt, &value.Version, &value.CreatedAt, &value.UpdatedAt,
	)
	if err != nil {
		return WorkspaceLLMGatewayAuthTransaction{}, err
	}
	if len(stateHash) != sha256.Size || len(bindingHash) != sha256.Size {
		return WorkspaceLLMGatewayAuthTransaction{}, errors.New("stored workspace LLM gateway authorization hash has invalid length")
	}
	copy(value.OIDCStateSHA256[:], stateHash)
	copy(value.BrowserBindingSHA256[:], bindingHash)
	value.SealedSecrets = append([]byte(nil), value.SealedSecrets...)
	return value, nil
}

func validateCreateWorkspaceLLMGateway(command CreateWorkspaceLLMGatewayCommand) error {
	for field, value := range map[string]string{"id": command.ID, "workspace_id": command.WorkspaceID, "actor_id": command.ActorID} {
		if err := validateUUID(field, value); err != nil {
			return err
		}
	}
	for field, bounded := range map[string]struct {
		value   string
		maximum int
	}{
		"name": {command.Name, 128}, "responses_url": {command.ResponsesURL, 4096},
		"oidc_issuer": {command.OIDCIssuer, 2048}, "oidc_client_id": {command.OIDCClientID, 512},
		"oidc_scopes": {command.OIDCScopes, 2048}, "default_model": {command.DefaultModel, 256},
	} {
		if err := validateBoundedText(field, bounded.value, bounded.maximum); err != nil {
			return err
		}
	}
	if command.BearerTokenType != LLMGatewayBearerIDToken && command.BearerTokenType != LLMGatewayBearerAccessToken {
		return errors.New("bearer_token_type must be id_token or access_token")
	}
	return nil
}

func validateCreateWorkspaceLLMGatewayAuthTransaction(command CreateWorkspaceLLMGatewayAuthTransactionCommand) error {
	for field, value := range map[string]string{"id": command.ID, "workspace_id": command.WorkspaceID, "gateway_id": command.GatewayID, "user_id": command.UserID} {
		if err := validateUUID(field, value); err != nil {
			return err
		}
	}
	if command.GatewayVersion < 1 || command.GatewayVersion > maxSafeJSONInteger ||
		command.OIDCStateSHA256 == ([sha256.Size]byte{}) || command.BrowserBindingSHA256 == ([sha256.Size]byte{}) ||
		len(command.SealedSecrets) < 29 || len(command.SealedSecrets) > 32768 {
		return errors.New("gateway version, correlation hashes, and sealed secrets are required")
	}
	if command.TTL < time.Minute || command.TTL > MaxLLMGatewayAuthTransactionTTL || command.TTL%time.Millisecond != 0 {
		return errors.New("authorization transaction TTL must be a whole-millisecond duration between one and ten minutes")
	}
	return nil
}

func validateClaimWorkspaceLLMGatewayAuthTransaction(command ClaimWorkspaceLLMGatewayAuthTransactionCommand) error {
	for field, value := range map[string]string{"workspace_id": command.WorkspaceID, "gateway_id": command.GatewayID, "user_id": command.UserID} {
		if err := validateUUID(field, value); err != nil {
			return err
		}
	}
	if command.OIDCStateSHA256 == ([sha256.Size]byte{}) || command.BrowserBindingSHA256 == ([sha256.Size]byte{}) {
		return errors.New("authorization state and browser binding hashes are required")
	}
	return nil
}

func validateCompleteWorkspaceLLMGatewayAuthTransaction(command CompleteWorkspaceLLMGatewayAuthTransactionCommand) error {
	for field, value := range map[string]string{"transaction_id": command.TransactionID, "grant_id": command.GrantID} {
		if err := validateUUID(field, value); err != nil {
			return err
		}
	}
	if command.ExpectedVersion < 1 || command.ExpectedVersion > maxSafeJSONInteger {
		return errors.New("expected authorization transaction version is invalid")
	}
	if err := validateBoundedText("oidc_issuer", command.OIDCIssuer, 2048); err != nil {
		return err
	}
	if err := validateBoundedText("oidc_subject", command.OIDCSubject, 2048); err != nil {
		return err
	}
	if len(command.SealedTokenSet) < 29 || len(command.SealedTokenSet) > 262144 || command.BearerExpiresAt.IsZero() {
		return errors.New("sealed token set and bearer expiry are required")
	}
	return nil
}

func validateFailWorkspaceLLMGatewayAuthTransaction(command FailWorkspaceLLMGatewayAuthTransactionCommand) error {
	if err := validateUUID("transaction_id", command.TransactionID); err != nil {
		return err
	}
	if command.ExpectedVersion < 1 || command.ExpectedVersion > maxSafeJSONInteger || command.Status != LLMGatewayAuthStatusFailed {
		return errors.New("authorization failure version or status is invalid")
	}
	return validateBoundedText("failure_code", command.FailureCode, 128)
}

func validateRunLLMGatewayBinding(binding RunLLMGatewayBinding) error {
	if err := validateUUID("gateway_id", binding.GatewayID); err != nil {
		return err
	}
	if err := validateUUID("grant_user_id", binding.GrantUserID); err != nil {
		return err
	}
	if binding.ConfigVersion < 1 || binding.ConfigVersion > maxSafeJSONInteger {
		return errors.New("gateway config version must be a positive safe integer")
	}
	return validateBoundedText("model", binding.Model, 256)
}

func workspaceLLMGatewayMatchesCreate(gateway WorkspaceLLMGateway, command CreateWorkspaceLLMGatewayCommand) bool {
	return gateway.ID == command.ID && gateway.WorkspaceID == command.WorkspaceID &&
		gateway.Name == command.Name && gateway.ResponsesURL == command.ResponsesURL &&
		gateway.OIDCIssuer == command.OIDCIssuer && gateway.OIDCClientID == command.OIDCClientID &&
		gateway.OIDCScopes == command.OIDCScopes && gateway.BearerTokenType == command.BearerTokenType &&
		gateway.DefaultModel == command.DefaultModel && gateway.Status == LLMGatewayStatusActive &&
		gateway.CreatedBy == command.ActorID
}

func canonicalLLMGatewayScopes(scopes []string) (string, error) {
	if len(scopes) < 1 || len(scopes) > 16 {
		return "", errors.New("OIDC scopes must contain between one and sixteen values")
	}
	seen := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		if err := validateBoundedText("OIDC scope", scope, 128); err != nil || strings.ContainsAny(scope, " \t") {
			return "", errors.New("OIDC scope is not a bounded token")
		}
		if _, duplicate := seen[scope]; duplicate {
			return "", errors.New("OIDC scopes must be unique")
		}
		seen[scope] = struct{}{}
	}
	return strings.Join(scopes, " "), nil
}
