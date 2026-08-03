package coredb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	OIDCLoginStatusPending         = "pending"
	OIDCLoginStatusCallbackClaimed = "callback_claimed"
	OIDCLoginStatusAuthenticated   = "authenticated"
	OIDCLoginStatusAccepting       = "accepting"
	OIDCLoginStatusAccepted        = "accepted"
	OIDCLoginStatusRejected        = "rejected"
	OIDCLoginStatusFailed          = "failed"
	OIDCLoginStatusExpired         = "expired"

	HydraConsentStatusAccepting = "accepting"
	HydraConsentStatusAccepted  = "accepted"
	HydraConsentStatusRejected  = "rejected"
	HydraConsentStatusFailed    = "failed"
	HydraConsentStatusExpired   = "expired"

	MaxOIDCLoginTransactionTTL = 10 * time.Minute
)

type OIDCLoginTransaction struct {
	ID                   string
	LoginChallengeSHA256 [32]byte
	OIDCStateSHA256      [32]byte
	BrowserBindingSHA256 [32]byte
	SealedSecrets        []byte
	OIDCIssuer           string
	HydraClientID        string
	Status               string
	UserID               string
	SealedRedirect       []byte
	FailureCode          string
	ExpiresAt            time.Time
	CallbackClaimedAt    *time.Time
	AuthenticatedAt      *time.Time
	CompletedAt          *time.Time
	Version              int64
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type CreateOIDCLoginTransactionCommand struct {
	ID                   string
	LoginChallengeSHA256 [32]byte
	OIDCStateSHA256      [32]byte
	BrowserBindingSHA256 [32]byte
	SealedSecrets        []byte
	OIDCIssuer           string
	HydraClientID        string
	TTL                  time.Duration
}

type ClaimOIDCLoginCallbackCommand struct {
	OIDCStateSHA256      [32]byte
	BrowserBindingSHA256 [32]byte
}

type AuthenticateOIDCLoginCommand struct {
	TransactionID string
	OIDCIssuer    string
	Subject       string
}

type CompleteOIDCLoginCommand struct {
	TransactionID  string
	SealedRedirect []byte
}

type FailOIDCLoginCommand struct {
	TransactionID string
	Status        string
	FailureCode   string
}

type HydraConsentTransaction struct {
	ConsentChallengeSHA256 [32]byte
	RequestSHA256          [32]byte
	HydraClientID          string
	UserID                 string
	Status                 string
	SealedRedirect         []byte
	FailureCode            string
	ExpiresAt              time.Time
	CompletedAt            *time.Time
	Version                int64
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

type CreateHydraConsentTransactionCommand struct {
	ConsentChallengeSHA256 [32]byte
	RequestSHA256          [32]byte
	HydraClientID          string
	UserID                 string
	TTL                    time.Duration
}

type CompleteHydraConsentCommand struct {
	ConsentChallengeSHA256 [32]byte
	SealedRedirect         []byte
}

type FailHydraConsentCommand struct {
	ConsentChallengeSHA256 [32]byte
	Status                 string
	FailureCode            string
}

// UserOAuthMembership is the minimum active workspace authority needed by the
// Hydra consent compiler. Generation is the persisted membership version, not
// a role encoded into the eventual access token.
type UserOAuthMembership struct {
	WorkspaceID string
	Role        string
	Generation  int64
}

type ResolveUserOAuthMembershipsCommand struct {
	UserID      string
	WorkspaceID string
	Limit       int
}

func (s *StateStore) CreateOIDCLoginTransaction(ctx context.Context, command CreateOIDCLoginTransactionCommand) (OIDCLoginTransaction, error) {
	const operation = "CreateOIDCLoginTransaction"
	if err := validateCreateOIDCLoginTransaction(command); err != nil {
		return OIDCLoginTransaction{}, commandError(ErrorInvalidArgument, operation, "oidc_login_transaction", command.ID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (OIDCLoginTransaction, error) {
		query := fmt.Sprintf(`
INSERT INTO %s
    (id, login_challenge_sha256, oidc_state_sha256,
     browser_binding_sha256, sealed_secrets, oidc_issuer,
     hydra_client_id, status, expires_at)
VALUES
    ($1, $2, $3, $4, $5, $6, $7, $8,
     pg_catalog.clock_timestamp() + ($9::bigint * interval '1 millisecond'))
RETURNING %s`, s.table("oidc_login_transactions"), oidcLoginTransactionColumns(""))
		result, err := scanOIDCLoginTransaction(transaction.QueryRow(
			ctx, query, command.ID, command.LoginChallengeSHA256[:], command.OIDCStateSHA256[:],
			command.BrowserBindingSHA256[:], command.SealedSecrets, command.OIDCIssuer,
			command.HydraClientID, OIDCLoginStatusPending, command.TTL.Milliseconds(),
		))
		if err != nil {
			var postgresError *pgconn.PgError
			if pgxErrorAs(err, &postgresError) && postgresError.Code == "23505" {
				return OIDCLoginTransaction{}, commandError(ErrorIdempotencyConflict, operation, "oidc_login_transaction", command.ID, "login challenge, state, or transaction identity is already in use")
			}
			return OIDCLoginTransaction{}, databaseError(operation+" insert", err)
		}
		return result, nil
	})
}

func (s *StateStore) ResumeOIDCLoginTransaction(
	ctx context.Context,
	challengeHash, browserBindingHash [32]byte,
) (OIDCLoginTransaction, error) {
	const operation = "ResumeOIDCLoginTransaction"
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (OIDCLoginTransaction, error) {
		query := fmt.Sprintf(`
SELECT %s
FROM %s
WHERE login_challenge_sha256 = $1
  AND browser_binding_sha256 = $2
  AND status = $3
  AND expires_at > pg_catalog.clock_timestamp()
FOR UPDATE`, oidcLoginTransactionColumns(""), s.table("oidc_login_transactions"))
		result, err := scanOIDCLoginTransaction(transaction.QueryRow(
			ctx, query, challengeHash[:], browserBindingHash[:], OIDCLoginStatusPending,
		))
		if errors.Is(err, pgx.ErrNoRows) {
			return OIDCLoginTransaction{}, commandError(ErrorNotFound, operation, "oidc_login_transaction", "", "no resumable login transaction matches the browser binding")
		}
		if err != nil {
			return OIDCLoginTransaction{}, databaseError(operation+" read", err)
		}
		return result, nil
	})
}

func (s *StateStore) ClaimOIDCLoginCallback(ctx context.Context, command ClaimOIDCLoginCallbackCommand) (OIDCLoginTransaction, error) {
	const operation = "ClaimOIDCLoginCallback"
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (OIDCLoginTransaction, error) {
		current, err := s.lockOIDCLoginByState(ctx, transaction, operation, command.OIDCStateSHA256)
		if err != nil {
			return OIDCLoginTransaction{}, err
		}
		if !bytes.Equal(current.BrowserBindingSHA256[:], command.BrowserBindingSHA256[:]) {
			return OIDCLoginTransaction{}, commandError(ErrorForbidden, operation, "oidc_login_transaction", current.ID, "browser binding does not match")
		}
		if current.Status != OIDCLoginStatusPending {
			return OIDCLoginTransaction{}, commandError(ErrorIdempotencyConflict, operation, "oidc_login_transaction", current.ID, "OIDC callback state was already consumed")
		}
		var live bool
		if err := transaction.QueryRow(ctx, "SELECT $1 > pg_catalog.clock_timestamp()", current.ExpiresAt).Scan(&live); err != nil {
			return OIDCLoginTransaction{}, databaseError(operation+" compare expiry", err)
		}
		if !live {
			return s.finishOIDCLogin(ctx, transaction, operation, current, OIDCLoginStatusExpired, "login_transaction_expired")
		}
		query := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    callback_claimed_at = pg_catalog.clock_timestamp(),
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $2 AND version = $3 AND status = $4
RETURNING %s`, s.table("oidc_login_transactions"), oidcLoginTransactionColumns(""))
		updated, err := scanOIDCLoginTransaction(transaction.QueryRow(
			ctx, query, OIDCLoginStatusCallbackClaimed, current.ID, current.Version, OIDCLoginStatusPending,
		))
		if errors.Is(err, pgx.ErrNoRows) {
			return OIDCLoginTransaction{}, versionConflict(operation, "oidc_login_transaction", current.ID, current.Version)
		}
		if err != nil {
			return OIDCLoginTransaction{}, databaseError(operation+" claim", err)
		}
		return updated, nil
	})
}

func (s *StateStore) AuthenticateOIDCLogin(ctx context.Context, command AuthenticateOIDCLoginCommand) (OIDCLoginTransaction, error) {
	const operation = "AuthenticateOIDCLogin"
	if command.OIDCIssuer == "" || command.Subject == "" || len(command.Subject) > 2048 {
		return OIDCLoginTransaction{}, commandError(ErrorInvalidArgument, operation, "oidc_login_transaction", command.TransactionID, "issuer and bounded subject are required")
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (OIDCLoginTransaction, error) {
		current, err := s.lockOIDCLoginByID(ctx, transaction, operation, command.TransactionID)
		if err != nil {
			return OIDCLoginTransaction{}, err
		}
		if current.Status != OIDCLoginStatusCallbackClaimed || current.OIDCIssuer != command.OIDCIssuer {
			return OIDCLoginTransaction{}, commandError(ErrorInvalidState, operation, "oidc_login_transaction", current.ID, "login callback is not awaiting identity mapping for this issuer")
		}
		identityQuery := fmt.Sprintf(`
SELECT ui.user_id::text
FROM %s AS ui
JOIN %s AS u ON u.id = ui.user_id
WHERE ui.issuer = $1 AND ui.subject = $2
  AND ui.status = 'active' AND u.status = 'active'
FOR SHARE OF ui, u`, s.table("user_identities"), s.table("users"))
		var userID string
		if err := transaction.QueryRow(ctx, identityQuery, command.OIDCIssuer, command.Subject).Scan(&userID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return s.finishOIDCLogin(ctx, transaction, operation, current, OIDCLoginStatusFailed, "identity_not_mapped")
			}
			return OIDCLoginTransaction{}, databaseError(operation+" map identity", err)
		}
		query := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    user_id = $2,
    authenticated_at = pg_catalog.clock_timestamp(),
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $3 AND version = $4 AND status = $5
RETURNING %s`, s.table("oidc_login_transactions"), oidcLoginTransactionColumns(""))
		updated, err := scanOIDCLoginTransaction(transaction.QueryRow(
			ctx, query, OIDCLoginStatusAuthenticated, userID, current.ID, current.Version, OIDCLoginStatusCallbackClaimed,
		))
		if errors.Is(err, pgx.ErrNoRows) {
			return OIDCLoginTransaction{}, versionConflict(operation, "oidc_login_transaction", current.ID, current.Version)
		}
		if err != nil {
			return OIDCLoginTransaction{}, databaseError(operation+" record identity", err)
		}
		return updated, nil
	})
}

func (s *StateStore) BeginOIDCLoginAcceptance(ctx context.Context, transactionID string) (OIDCLoginTransaction, error) {
	const operation = "BeginOIDCLoginAcceptance"
	return s.transitionOIDCLoginStatus(ctx, operation, transactionID, OIDCLoginStatusAuthenticated, OIDCLoginStatusAccepting)
}

func (s *StateStore) CompleteOIDCLogin(ctx context.Context, command CompleteOIDCLoginCommand) (OIDCLoginTransaction, error) {
	const operation = "CompleteOIDCLogin"
	if len(command.SealedRedirect) < 29 || len(command.SealedRedirect) > 16384 {
		return OIDCLoginTransaction{}, commandError(ErrorInvalidArgument, operation, "oidc_login_transaction", command.TransactionID, "sealed redirect is outside the stored bounds")
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (OIDCLoginTransaction, error) {
		current, err := s.lockOIDCLoginByID(ctx, transaction, operation, command.TransactionID)
		if err != nil {
			return OIDCLoginTransaction{}, err
		}
		if current.Status != OIDCLoginStatusAccepting {
			return OIDCLoginTransaction{}, commandError(ErrorInvalidState, operation, "oidc_login_transaction", current.ID, "login transaction is not accepting its Hydra challenge")
		}
		query := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    sealed_redirect = $2,
    completed_at = pg_catalog.clock_timestamp(),
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $3 AND version = $4 AND status = $5
RETURNING %s`, s.table("oidc_login_transactions"), oidcLoginTransactionColumns(""))
		updated, err := scanOIDCLoginTransaction(transaction.QueryRow(
			ctx, query, OIDCLoginStatusAccepted, command.SealedRedirect, current.ID, current.Version, OIDCLoginStatusAccepting,
		))
		if errors.Is(err, pgx.ErrNoRows) {
			return OIDCLoginTransaction{}, versionConflict(operation, "oidc_login_transaction", current.ID, current.Version)
		}
		if err != nil {
			return OIDCLoginTransaction{}, databaseError(operation+" complete", err)
		}
		return updated, nil
	})
}

func (s *StateStore) FailOIDCLogin(ctx context.Context, command FailOIDCLoginCommand) (OIDCLoginTransaction, error) {
	const operation = "FailOIDCLogin"
	if command.Status != OIDCLoginStatusRejected && command.Status != OIDCLoginStatusFailed && command.Status != OIDCLoginStatusExpired {
		return OIDCLoginTransaction{}, commandError(ErrorInvalidArgument, operation, "oidc_login_transaction", command.TransactionID, "terminal failure status is invalid")
	}
	if command.FailureCode == "" || len(command.FailureCode) > 128 {
		return OIDCLoginTransaction{}, commandError(ErrorInvalidArgument, operation, "oidc_login_transaction", command.TransactionID, "bounded failure code is required")
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (OIDCLoginTransaction, error) {
		current, err := s.lockOIDCLoginByID(ctx, transaction, operation, command.TransactionID)
		if err != nil {
			return OIDCLoginTransaction{}, err
		}
		if current.Status == OIDCLoginStatusAccepted || oidcLoginTerminal(current.Status) {
			return OIDCLoginTransaction{}, commandError(ErrorInvalidState, operation, "oidc_login_transaction", current.ID, "login transaction is already terminal")
		}
		return s.finishOIDCLogin(ctx, transaction, operation, current, command.Status, command.FailureCode)
	})
}

func (s *StateStore) RequireActiveUser(ctx context.Context, userID string) error {
	const operation = "RequireActiveUser"
	if err := validateUUID("user_id", userID); err != nil {
		return commandError(ErrorInvalidArgument, operation, "user", userID, err.Error())
	}
	_, err := withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (struct{}, error) {
		query := fmt.Sprintf("SELECT 1 FROM %s WHERE id = $1 AND status = 'active' FOR SHARE", s.table("users"))
		var one int
		if err := transaction.QueryRow(ctx, query, userID).Scan(&one); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return struct{}{}, commandError(ErrorForbidden, operation, "user", userID, "user is not active")
			}
			return struct{}{}, databaseError(operation+" read", err)
		}
		return struct{}{}, nil
	})
	return err
}

func (s *StateStore) ResolveUserOAuthMemberships(
	ctx context.Context,
	command ResolveUserOAuthMembershipsCommand,
) ([]UserOAuthMembership, error) {
	const operation = "ResolveUserOAuthMemberships"
	if err := validateUUID("user_id", command.UserID); err != nil {
		return nil, commandError(ErrorInvalidArgument, operation, "user", command.UserID, err.Error())
	}
	if command.WorkspaceID != "" {
		if err := validateUUID("workspace_id", command.WorkspaceID); err != nil {
			return nil, commandError(ErrorInvalidArgument, operation, "workspace", command.WorkspaceID, err.Error())
		}
	}
	if command.Limit < 1 || command.Limit > 256 {
		return nil, commandError(ErrorInvalidArgument, operation, "user", command.UserID, "membership projection limit must be between one and 256")
	}
	return withStateReadTransaction(ctx, s, operation, func(transaction pgx.Tx) ([]UserOAuthMembership, error) {
		userQuery := fmt.Sprintf("SELECT 1 FROM %s WHERE id = $1 AND status = 'active'", s.table("users"))
		var one int
		if err := transaction.QueryRow(ctx, userQuery, command.UserID).Scan(&one); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, commandError(ErrorForbidden, operation, "user", command.UserID, "consent subject is not active")
			}
			return nil, databaseError(operation+" read user", err)
		}

		query := fmt.Sprintf(`
SELECT member.workspace_id::text, member.role, member.version
FROM %s AS member
JOIN %s AS workspace ON workspace.id = member.workspace_id
WHERE member.user_id = $1
  AND workspace.status = 'active'
  AND ($2::uuid IS NULL OR member.workspace_id = $2::uuid)
ORDER BY member.workspace_id
LIMIT $3`, s.table("workspace_members"), s.table("workspaces"))
		var workspaceID any
		if command.WorkspaceID != "" {
			workspaceID = command.WorkspaceID
		}
		rows, err := transaction.Query(ctx, query, command.UserID, workspaceID, command.Limit+1)
		if err != nil {
			return nil, databaseError(operation+" read memberships", err)
		}
		defer rows.Close()
		memberships := make([]UserOAuthMembership, 0, command.Limit)
		for rows.Next() {
			var membership UserOAuthMembership
			if err := rows.Scan(&membership.WorkspaceID, &membership.Role, &membership.Generation); err != nil {
				return nil, databaseError(operation+" scan membership", err)
			}
			memberships = append(memberships, membership)
		}
		if err := rows.Err(); err != nil {
			return nil, databaseError(operation+" finish memberships", err)
		}
		if len(memberships) > command.Limit {
			return nil, commandError(ErrorConflict, operation, "user", command.UserID, "active workspace authority exceeds the bounded token projection")
		}
		return memberships, nil
	})
}

func (s *StateStore) CreateHydraConsentTransaction(ctx context.Context, command CreateHydraConsentTransactionCommand) (HydraConsentTransaction, error) {
	const operation = "CreateHydraConsentTransaction"
	if err := validateCreateHydraConsentTransaction(command); err != nil {
		return HydraConsentTransaction{}, commandError(ErrorInvalidArgument, operation, "hydra_consent_transaction", "", err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (HydraConsentTransaction, error) {
		userQuery := fmt.Sprintf("SELECT 1 FROM %s WHERE id = $1 AND status = 'active' FOR SHARE", s.table("users"))
		var one int
		if err := transaction.QueryRow(ctx, userQuery, command.UserID).Scan(&one); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return HydraConsentTransaction{}, commandError(ErrorForbidden, operation, "user", command.UserID, "consent subject is not active")
			}
			return HydraConsentTransaction{}, databaseError(operation+" read user", err)
		}
		query := fmt.Sprintf(`
INSERT INTO %s
    (consent_challenge_sha256, request_sha256, hydra_client_id,
     user_id, status, expires_at)
VALUES
    ($1, $2, $3, $4, $5,
     pg_catalog.clock_timestamp() + ($6::bigint * interval '1 millisecond'))
RETURNING %s`, s.table("hydra_consent_transactions"), hydraConsentTransactionColumns(""))
		result, err := scanHydraConsentTransaction(transaction.QueryRow(
			ctx, query, command.ConsentChallengeSHA256[:], command.RequestSHA256[:], command.HydraClientID,
			command.UserID, HydraConsentStatusAccepting, command.TTL.Milliseconds(),
		))
		if err != nil {
			var postgresError *pgconn.PgError
			if pgxErrorAs(err, &postgresError) && postgresError.Code == "23505" {
				return HydraConsentTransaction{}, commandError(ErrorIdempotencyConflict, operation, "hydra_consent_transaction", "", "consent challenge was already consumed")
			}
			return HydraConsentTransaction{}, databaseError(operation+" insert", err)
		}
		return result, nil
	})
}

func (s *StateStore) CompleteHydraConsent(ctx context.Context, command CompleteHydraConsentCommand) (HydraConsentTransaction, error) {
	const operation = "CompleteHydraConsent"
	if len(command.SealedRedirect) < 29 || len(command.SealedRedirect) > 16384 {
		return HydraConsentTransaction{}, commandError(ErrorInvalidArgument, operation, "hydra_consent_transaction", "", "sealed redirect is outside the stored bounds")
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (HydraConsentTransaction, error) {
		query := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    sealed_redirect = $2,
    completed_at = pg_catalog.clock_timestamp(),
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE consent_challenge_sha256 = $3 AND status = $4
RETURNING %s`, s.table("hydra_consent_transactions"), hydraConsentTransactionColumns(""))
		updated, err := scanHydraConsentTransaction(transaction.QueryRow(
			ctx, query, HydraConsentStatusAccepted, command.SealedRedirect,
			command.ConsentChallengeSHA256[:], HydraConsentStatusAccepting,
		))
		if errors.Is(err, pgx.ErrNoRows) {
			return HydraConsentTransaction{}, commandError(ErrorInvalidState, operation, "hydra_consent_transaction", "", "consent challenge is not awaiting acceptance")
		}
		if err != nil {
			return HydraConsentTransaction{}, databaseError(operation+" complete", err)
		}
		return updated, nil
	})
}

func (s *StateStore) FailHydraConsent(ctx context.Context, command FailHydraConsentCommand) (HydraConsentTransaction, error) {
	const operation = "FailHydraConsent"
	if command.Status != HydraConsentStatusRejected && command.Status != HydraConsentStatusFailed && command.Status != HydraConsentStatusExpired {
		return HydraConsentTransaction{}, commandError(ErrorInvalidArgument, operation, "hydra_consent_transaction", "", "terminal consent status is invalid")
	}
	if command.FailureCode == "" || len(command.FailureCode) > 128 {
		return HydraConsentTransaction{}, commandError(ErrorInvalidArgument, operation, "hydra_consent_transaction", "", "bounded failure code is required")
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (HydraConsentTransaction, error) {
		query := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    failure_code = $2,
    completed_at = pg_catalog.clock_timestamp(),
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE consent_challenge_sha256 = $3 AND status = $4
RETURNING %s`, s.table("hydra_consent_transactions"), hydraConsentTransactionColumns(""))
		updated, err := scanHydraConsentTransaction(transaction.QueryRow(
			ctx, query, command.Status, command.FailureCode,
			command.ConsentChallengeSHA256[:], HydraConsentStatusAccepting,
		))
		if errors.Is(err, pgx.ErrNoRows) {
			return HydraConsentTransaction{}, commandError(ErrorInvalidState, operation, "hydra_consent_transaction", "", "consent challenge is not awaiting a terminal outcome")
		}
		if err != nil {
			return HydraConsentTransaction{}, databaseError(operation+" fail", err)
		}
		return updated, nil
	})
}

func (s *StateStore) transitionOIDCLoginStatus(
	ctx context.Context,
	operation, transactionID, from, to string,
) (OIDCLoginTransaction, error) {
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (OIDCLoginTransaction, error) {
		current, err := s.lockOIDCLoginByID(ctx, transaction, operation, transactionID)
		if err != nil {
			return OIDCLoginTransaction{}, err
		}
		if current.Status != from {
			return OIDCLoginTransaction{}, commandError(ErrorInvalidState, operation, "oidc_login_transaction", current.ID, "login transaction is not in the required state")
		}
		query := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $2 AND version = $3 AND status = $4
RETURNING %s`, s.table("oidc_login_transactions"), oidcLoginTransactionColumns(""))
		updated, err := scanOIDCLoginTransaction(transaction.QueryRow(ctx, query, to, current.ID, current.Version, from))
		if errors.Is(err, pgx.ErrNoRows) {
			return OIDCLoginTransaction{}, versionConflict(operation, "oidc_login_transaction", current.ID, current.Version)
		}
		if err != nil {
			return OIDCLoginTransaction{}, databaseError(operation+" transition", err)
		}
		return updated, nil
	})
}

func (s *StateStore) finishOIDCLogin(
	ctx context.Context,
	transaction pgx.Tx,
	operation string,
	current OIDCLoginTransaction,
	status, failureCode string,
) (OIDCLoginTransaction, error) {
	query := fmt.Sprintf(`
UPDATE %s
SET status = $1,
    failure_code = $2,
    completed_at = pg_catalog.clock_timestamp(),
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $3 AND version = $4
RETURNING %s`, s.table("oidc_login_transactions"), oidcLoginTransactionColumns(""))
	updated, err := scanOIDCLoginTransaction(transaction.QueryRow(ctx, query, status, failureCode, current.ID, current.Version))
	if errors.Is(err, pgx.ErrNoRows) {
		return OIDCLoginTransaction{}, versionConflict(operation, "oidc_login_transaction", current.ID, current.Version)
	}
	if err != nil {
		return OIDCLoginTransaction{}, databaseError(operation+" finish", err)
	}
	return updated, nil
}

func (s *StateStore) lockOIDCLoginByID(
	ctx context.Context,
	transaction pgx.Tx,
	operation, transactionID string,
) (OIDCLoginTransaction, error) {
	query := fmt.Sprintf("SELECT %s FROM %s WHERE id = $1 FOR UPDATE", oidcLoginTransactionColumns(""), s.table("oidc_login_transactions"))
	result, err := scanOIDCLoginTransaction(transaction.QueryRow(ctx, query, transactionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return OIDCLoginTransaction{}, commandError(ErrorNotFound, operation, "oidc_login_transaction", transactionID, "login transaction does not exist")
	}
	if err != nil {
		return OIDCLoginTransaction{}, databaseError(operation+" lock", err)
	}
	return result, nil
}

func (s *StateStore) lockOIDCLoginByState(
	ctx context.Context,
	transaction pgx.Tx,
	operation string,
	stateHash [32]byte,
) (OIDCLoginTransaction, error) {
	query := fmt.Sprintf("SELECT %s FROM %s WHERE oidc_state_sha256 = $1 FOR UPDATE", oidcLoginTransactionColumns(""), s.table("oidc_login_transactions"))
	result, err := scanOIDCLoginTransaction(transaction.QueryRow(ctx, query, stateHash[:]))
	if errors.Is(err, pgx.ErrNoRows) {
		return OIDCLoginTransaction{}, commandError(ErrorNotFound, operation, "oidc_login_transaction", "", "OIDC callback state does not exist")
	}
	if err != nil {
		return OIDCLoginTransaction{}, databaseError(operation+" lock", err)
	}
	return result, nil
}

func oidcLoginTransactionColumns(alias string) string {
	if alias != "" {
		alias += "."
	}
	return alias + "id::text, " +
		alias + "login_challenge_sha256, " +
		alias + "oidc_state_sha256, " +
		alias + "browser_binding_sha256, " +
		alias + "sealed_secrets, " +
		alias + "oidc_issuer, " +
		alias + "hydra_client_id, " +
		alias + "status, " +
		"COALESCE(" + alias + "user_id::text, ''), " +
		"COALESCE(" + alias + "sealed_redirect, ''::bytea), " +
		"COALESCE(" + alias + "failure_code, ''), " +
		alias + "expires_at, " +
		alias + "callback_claimed_at, " +
		alias + "authenticated_at, " +
		alias + "completed_at, " +
		alias + "version, " +
		alias + "created_at, " +
		alias + "updated_at"
}

func scanOIDCLoginTransaction(row pgx.Row) (OIDCLoginTransaction, error) {
	var result OIDCLoginTransaction
	var challengeHash, stateHash, browserHash []byte
	err := row.Scan(
		&result.ID, &challengeHash, &stateHash, &browserHash, &result.SealedSecrets,
		&result.OIDCIssuer, &result.HydraClientID, &result.Status, &result.UserID,
		&result.SealedRedirect, &result.FailureCode, &result.ExpiresAt,
		&result.CallbackClaimedAt, &result.AuthenticatedAt, &result.CompletedAt,
		&result.Version, &result.CreatedAt, &result.UpdatedAt,
	)
	if err != nil {
		return OIDCLoginTransaction{}, err
	}
	if len(challengeHash) != 32 || len(stateHash) != 32 || len(browserHash) != 32 {
		return OIDCLoginTransaction{}, errors.New("stored OIDC login transaction contains an invalid digest")
	}
	copy(result.LoginChallengeSHA256[:], challengeHash)
	copy(result.OIDCStateSHA256[:], stateHash)
	copy(result.BrowserBindingSHA256[:], browserHash)
	result.SealedSecrets = append([]byte(nil), result.SealedSecrets...)
	result.SealedRedirect = append([]byte(nil), result.SealedRedirect...)
	return result, nil
}

func hydraConsentTransactionColumns(alias string) string {
	if alias != "" {
		alias += "."
	}
	return alias + "consent_challenge_sha256, " +
		alias + "request_sha256, " +
		alias + "hydra_client_id, " +
		alias + "user_id::text, " +
		alias + "status, " +
		"COALESCE(" + alias + "sealed_redirect, ''::bytea), " +
		"COALESCE(" + alias + "failure_code, ''), " +
		alias + "expires_at, " +
		alias + "completed_at, " +
		alias + "version, " +
		alias + "created_at, " +
		alias + "updated_at"
}

func scanHydraConsentTransaction(row pgx.Row) (HydraConsentTransaction, error) {
	var result HydraConsentTransaction
	var challengeHash, requestHash []byte
	err := row.Scan(
		&challengeHash, &requestHash, &result.HydraClientID, &result.UserID,
		&result.Status, &result.SealedRedirect, &result.FailureCode, &result.ExpiresAt,
		&result.CompletedAt, &result.Version, &result.CreatedAt, &result.UpdatedAt,
	)
	if err != nil {
		return HydraConsentTransaction{}, err
	}
	if len(challengeHash) != 32 || len(requestHash) != 32 {
		return HydraConsentTransaction{}, errors.New("stored Hydra consent transaction contains an invalid digest")
	}
	copy(result.ConsentChallengeSHA256[:], challengeHash)
	copy(result.RequestSHA256[:], requestHash)
	result.SealedRedirect = append([]byte(nil), result.SealedRedirect...)
	return result, nil
}

func validateCreateOIDCLoginTransaction(command CreateOIDCLoginTransactionCommand) error {
	if err := validateUUID("transaction_id", command.ID); err != nil {
		return err
	}
	if len(command.SealedSecrets) < 29 || len(command.SealedSecrets) > 16384 {
		return errors.New("sealed login secrets are outside the stored bounds")
	}
	if len(command.OIDCIssuer) < 8 || len(command.OIDCIssuer) > 2048 {
		return errors.New("OIDC issuer is outside the stored bounds")
	}
	if command.HydraClientID == "" || len(command.HydraClientID) > 512 {
		return errors.New("Hydra client ID is outside the stored bounds")
	}
	if command.TTL < time.Minute || command.TTL > MaxOIDCLoginTransactionTTL || command.TTL%time.Millisecond != 0 {
		return fmt.Errorf("login transaction TTL must be a whole number of milliseconds between one minute and %s", MaxOIDCLoginTransactionTTL)
	}
	return nil
}

func validateCreateHydraConsentTransaction(command CreateHydraConsentTransactionCommand) error {
	if err := validateUUID("user_id", command.UserID); err != nil {
		return err
	}
	if command.HydraClientID == "" || len(command.HydraClientID) > 512 {
		return errors.New("Hydra client ID is outside the stored bounds")
	}
	if command.TTL < time.Minute || command.TTL > MaxOIDCLoginTransactionTTL || command.TTL%time.Millisecond != 0 {
		return fmt.Errorf("consent transaction TTL must be a whole number of milliseconds between one minute and %s", MaxOIDCLoginTransactionTTL)
	}
	return nil
}

func oidcLoginTerminal(status string) bool {
	return status == OIDCLoginStatusRejected || status == OIDCLoginStatusFailed || status == OIDCLoginStatusExpired
}
