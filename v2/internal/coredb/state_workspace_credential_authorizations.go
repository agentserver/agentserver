package coredb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecredentials"
	"github.com/jackc/pgx/v5"
)

const (
	WorkspaceCredentialAuthorizationPending   = "pending"
	WorkspaceCredentialAuthorizationSucceeded = "succeeded"
	WorkspaceCredentialAuthorizationDenied    = "denied"
	WorkspaceCredentialAuthorizationExpired   = "expired"
	WorkspaceCredentialAuthorizationCancelled = "cancelled"
	WorkspaceCredentialAuthorizationFailed    = "failed"
)

type WorkspaceCredentialAuthorizationRecord struct {
	ID                        string
	WorkspaceID               string
	Kind                      string
	ActorID                   string
	TargetBindingID           string
	TargetExists              bool
	ExpectedAuthorityVersion  int64
	ExpectedCredentialVersion int64
	DisplayName               string
	OwnerScope                string
	OwnerUserID               string
	MakeDefault               bool
	ProviderPublic            json.RawMessage
	UserCode                  string
	VerificationURI           string
	VerificationURIComplete   string
	SealedProviderState       []byte
	SealingKeyID              string
	ProviderStateVersion      int64
	Status                    string
	PollIntervalSeconds       int
	NextPollAt                time.Time
	ExpiresAt                 time.Time
	PollLeaseToken            string
	PollLeaseExpiresAt        *time.Time
	BindingID                 string
	LastErrorCode             string
	Version                   int64
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
	CompletedAt               *time.Time
}

type CreateWorkspaceCredentialAuthorizationCommand struct {
	Record WorkspaceCredentialAuthorizationRecord
}

type ClaimWorkspaceCredentialAuthorizationPollCommand struct {
	WorkspaceID     string
	Kind            string
	AuthorizationID string
	ActorID         string
	LeaseToken      string
	LeaseExpiresAt  time.Time
}

type FinishWorkspaceCredentialAuthorizationPollCommand struct {
	WorkspaceID         string
	Kind                string
	AuthorizationID     string
	ActorID             string
	LeaseToken          string
	Status              string
	NextPollAt          time.Time
	PollInterval        int
	LastErrorCode       string
	SealedProviderState []byte
}

type FinalizeWorkspaceCredentialAuthorizationCommand struct {
	WorkspaceID      string
	Kind             string
	AuthorizationID  string
	ActorID          string
	LeaseToken       string
	AuthType         string
	PublicMetadata   json.RawMessage
	SealedSecret     []byte
	SealingKeyID     string
	AccessExpiresAt  *time.Time
	RefreshExpiresAt *time.Time
}

type CancelWorkspaceCredentialAuthorizationCommand struct {
	WorkspaceID     string
	Kind            string
	AuthorizationID string
	ActorID         string
	ExpectedVersion int64
}

type WorkspaceCredentialAuthorizationFinalizeResult struct {
	Authorization WorkspaceCredentialAuthorizationRecord
	Binding       corecredentials.Binding
}

func (s *StateStore) CreateWorkspaceCredentialAuthorization(ctx context.Context, command CreateWorkspaceCredentialAuthorizationCommand) (WorkspaceCredentialAuthorizationRecord, error) {
	const operation = "CreateWorkspaceCredentialAuthorization"
	record := command.Record
	if err := validateCredentialAuthorizationRecord(record); err != nil {
		return WorkspaceCredentialAuthorizationRecord{}, commandError(ErrorInvalidArgument, operation, "credential_authorization", record.ID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (WorkspaceCredentialAuthorizationRecord, error) {
		if err := requireCredentialManager(ctx, transaction, s, operation, record.WorkspaceID, record.ActorID); err != nil {
			return WorkspaceCredentialAuthorizationRecord{}, err
		}
		if record.TargetExists {
			binding, err := readWorkspaceCredentialBindingInTransaction(ctx, transaction, s, operation, record.WorkspaceID, record.Kind, record.TargetBindingID)
			if err != nil {
				return WorkspaceCredentialAuthorizationRecord{}, err
			}
			defer clearBytes(binding.SealedSecret)
			if binding.AuthorityVersion != record.ExpectedAuthorityVersion || binding.CredentialVersion != record.ExpectedCredentialVersion ||
				(binding.Status != corecredentials.StatusActive && binding.Status != corecredentials.StatusReauthRequired) {
				return WorkspaceCredentialAuthorizationRecord{}, versionConflict(operation, "credential", record.TargetBindingID, record.ExpectedAuthorityVersion)
			}
		} else {
			query := fmt.Sprintf(`SELECT 1 FROM %s WHERE workspace_id = $1 AND id = $2`, s.table("workspace_credential_bindings"))
			var present int
			if err := transaction.QueryRow(ctx, query, record.WorkspaceID, record.TargetBindingID).Scan(&present); err == nil {
				return WorkspaceCredentialAuthorizationRecord{}, commandError(ErrorConflict, operation, "credential", record.TargetBindingID, "credential binding identity is already in use")
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return WorkspaceCredentialAuthorizationRecord{}, databaseError(operation+" inspect target", err)
			}
		}
		query := fmt.Sprintf(`
INSERT INTO %s (
 id, workspace_id, kind, actor_id, target_binding_id, target_exists,
 expected_authority_version, expected_credential_version, display_name,
 owner_scope, owner_user_id, make_default, provider_public, user_code,
 verification_uri, verification_uri_complete, sealed_provider_state,
 sealing_key_id, provider_state_version, poll_interval_seconds,
 next_poll_at, expires_at)
VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,0),NULLIF($8,0),$9,$10,
 NULLIF($11,'')::uuid,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22)
RETURNING `+workspaceCredentialAuthorizationColumns, s.table("workspace_credential_authorizations"))
		created, err := scanWorkspaceCredentialAuthorization(transaction.QueryRow(ctx, query,
			record.ID, record.WorkspaceID, record.Kind, record.ActorID, record.TargetBindingID, record.TargetExists,
			record.ExpectedAuthorityVersion, record.ExpectedCredentialVersion, record.DisplayName,
			record.OwnerScope, record.OwnerUserID, record.MakeDefault, normalizedJSON(record.ProviderPublic), record.UserCode,
			record.VerificationURI, record.VerificationURIComplete, record.SealedProviderState,
			record.SealingKeyID, record.ProviderStateVersion, record.PollIntervalSeconds,
			record.NextPollAt, record.ExpiresAt), operation)
		if err != nil {
			if isUniqueViolation(err) {
				return WorkspaceCredentialAuthorizationRecord{}, commandError(ErrorConflict, operation, "credential_authorization", record.ID, "a pending authorization already exists for this credential")
			}
			return WorkspaceCredentialAuthorizationRecord{}, databaseError(operation+" insert", err)
		}
		return created, nil
	})
}

func (s *StateStore) GetWorkspaceCredentialAuthorization(ctx context.Context, workspaceID, kind, authorizationID, actorID string) (WorkspaceCredentialAuthorizationRecord, error) {
	const operation = "GetWorkspaceCredentialAuthorization"
	if err := validateCredentialAuthorizationIdentity(workspaceID, kind, authorizationID, actorID); err != nil {
		return WorkspaceCredentialAuthorizationRecord{}, commandError(ErrorInvalidArgument, operation, "credential_authorization", authorizationID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (WorkspaceCredentialAuthorizationRecord, error) {
		if err := requireCredentialManager(ctx, transaction, s, operation, workspaceID, actorID); err != nil {
			return WorkspaceCredentialAuthorizationRecord{}, err
		}
		if err := expireWorkspaceCredentialAuthorization(ctx, transaction, s, operation, workspaceID, kind, authorizationID); err != nil {
			return WorkspaceCredentialAuthorizationRecord{}, err
		}
		return readWorkspaceCredentialAuthorization(ctx, transaction, s, operation, workspaceID, kind, authorizationID, false)
	})
}

func (s *StateStore) ClaimWorkspaceCredentialAuthorizationPoll(ctx context.Context, command ClaimWorkspaceCredentialAuthorizationPollCommand) (WorkspaceCredentialAuthorizationRecord, error) {
	const operation = "ClaimWorkspaceCredentialAuthorizationPoll"
	if err := validateCredentialAuthorizationIdentity(command.WorkspaceID, command.Kind, command.AuthorizationID, command.ActorID); err != nil {
		return WorkspaceCredentialAuthorizationRecord{}, commandError(ErrorInvalidArgument, operation, "credential_authorization", command.AuthorizationID, err.Error())
	}
	if err := validateUUID("lease_token", command.LeaseToken); err != nil || command.LeaseExpiresAt.IsZero() {
		return WorkspaceCredentialAuthorizationRecord{}, commandError(ErrorInvalidArgument, operation, "credential_authorization", command.AuthorizationID, "poll lease is invalid")
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (WorkspaceCredentialAuthorizationRecord, error) {
		if err := requireCredentialManager(ctx, transaction, s, operation, command.WorkspaceID, command.ActorID); err != nil {
			return WorkspaceCredentialAuthorizationRecord{}, err
		}
		if err := expireWorkspaceCredentialAuthorization(ctx, transaction, s, operation, command.WorkspaceID, command.Kind, command.AuthorizationID); err != nil {
			return WorkspaceCredentialAuthorizationRecord{}, err
		}
		record, err := readWorkspaceCredentialAuthorization(ctx, transaction, s, operation, command.WorkspaceID, command.Kind, command.AuthorizationID, true)
		if err != nil {
			return WorkspaceCredentialAuthorizationRecord{}, err
		}
		if record.Status != WorkspaceCredentialAuthorizationPending {
			return WorkspaceCredentialAuthorizationRecord{}, commandError(ErrorConflict, operation, "credential_authorization", command.AuthorizationID, "credential authorization is terminal")
		}
		query := fmt.Sprintf(`
UPDATE %s
SET poll_lease_token = $4, poll_lease_expires_at = $5,
    version = version + 1, updated_at = pg_catalog.clock_timestamp()
WHERE workspace_id = $1 AND kind = $2 AND id = $3
  AND status = 'pending'
  AND next_poll_at <= pg_catalog.clock_timestamp()
  AND (poll_lease_token IS NULL OR poll_lease_expires_at <= pg_catalog.clock_timestamp())
RETURNING `+workspaceCredentialAuthorizationColumns, s.table("workspace_credential_authorizations"))
		claimed, err := scanWorkspaceCredentialAuthorization(transaction.QueryRow(ctx, query,
			command.WorkspaceID, command.Kind, command.AuthorizationID, command.LeaseToken, command.LeaseExpiresAt), operation)
		if errors.Is(err, pgx.ErrNoRows) {
			return WorkspaceCredentialAuthorizationRecord{}, commandError(ErrorConflict, operation, "credential_authorization", command.AuthorizationID, "poll interval has not elapsed or another poll is active")
		}
		if err != nil {
			return WorkspaceCredentialAuthorizationRecord{}, databaseError(operation+" claim", err)
		}
		return claimed, nil
	})
}

func (s *StateStore) FinishWorkspaceCredentialAuthorizationPoll(ctx context.Context, command FinishWorkspaceCredentialAuthorizationPollCommand) (WorkspaceCredentialAuthorizationRecord, error) {
	const operation = "FinishWorkspaceCredentialAuthorizationPoll"
	if err := validateCredentialAuthorizationIdentity(command.WorkspaceID, command.Kind, command.AuthorizationID, command.ActorID); err != nil {
		return WorkspaceCredentialAuthorizationRecord{}, commandError(ErrorInvalidArgument, operation, "credential_authorization", command.AuthorizationID, err.Error())
	}
	if err := validateUUID("lease_token", command.LeaseToken); err != nil || !validCredentialAuthorizationFinishStatus(command.Status) ||
		command.PollInterval < 1 || command.PollInterval > 60 || len(command.LastErrorCode) > 128 {
		return WorkspaceCredentialAuthorizationRecord{}, commandError(ErrorInvalidArgument, operation, "credential_authorization", command.AuthorizationID, "poll result is invalid")
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (WorkspaceCredentialAuthorizationRecord, error) {
		if err := requireCredentialManager(ctx, transaction, s, operation, command.WorkspaceID, command.ActorID); err != nil {
			return WorkspaceCredentialAuthorizationRecord{}, err
		}
		terminal := command.Status != WorkspaceCredentialAuthorizationPending
		query := fmt.Sprintf(`
UPDATE %s
SET status = $5,
    poll_interval_seconds = $6,
    next_poll_at = CASE WHEN $7 THEN next_poll_at ELSE $8 END,
    last_error_code = NULLIF($9, ''),
    sealed_provider_state = CASE WHEN $7 THEN NULL WHEN octet_length($10::bytea) > 0 THEN $10 ELSE sealed_provider_state END,
    poll_lease_token = NULL,
    poll_lease_expires_at = NULL,
    completed_at = CASE WHEN $7 THEN pg_catalog.clock_timestamp() ELSE NULL END,
    version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE workspace_id = $1 AND kind = $2 AND id = $3
  AND poll_lease_token = $4 AND status = 'pending'
RETURNING `+workspaceCredentialAuthorizationColumns, s.table("workspace_credential_authorizations"))
		result, err := scanWorkspaceCredentialAuthorization(transaction.QueryRow(ctx, query,
			command.WorkspaceID, command.Kind, command.AuthorizationID, command.LeaseToken,
			command.Status, command.PollInterval, terminal, command.NextPollAt, command.LastErrorCode,
			command.SealedProviderState), operation)
		if errors.Is(err, pgx.ErrNoRows) {
			return WorkspaceCredentialAuthorizationRecord{}, versionConflict(operation, "credential_authorization", command.AuthorizationID, 0)
		}
		if err != nil {
			return WorkspaceCredentialAuthorizationRecord{}, databaseError(operation+" update", err)
		}
		return result, nil
	})
}

func (s *StateStore) FinalizeWorkspaceCredentialAuthorization(ctx context.Context, command FinalizeWorkspaceCredentialAuthorizationCommand) (WorkspaceCredentialAuthorizationFinalizeResult, error) {
	const operation = "FinalizeWorkspaceCredentialAuthorization"
	if err := validateCredentialAuthorizationIdentity(command.WorkspaceID, command.Kind, command.AuthorizationID, command.ActorID); err != nil {
		return WorkspaceCredentialAuthorizationFinalizeResult{}, commandError(ErrorInvalidArgument, operation, "credential_authorization", command.AuthorizationID, err.Error())
	}
	if err := validateUUID("lease_token", command.LeaseToken); err != nil || command.AuthType == "" || len(command.AuthType) > 128 ||
		len(command.SealedSecret) == 0 || len(command.SealedSecret) > 512*1024 || command.SealingKeyID == "" {
		return WorkspaceCredentialAuthorizationFinalizeResult{}, commandError(ErrorInvalidArgument, operation, "credential_authorization", command.AuthorizationID, "credential result is invalid")
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (WorkspaceCredentialAuthorizationFinalizeResult, error) {
		if err := requireCredentialManager(ctx, transaction, s, operation, command.WorkspaceID, command.ActorID); err != nil {
			return WorkspaceCredentialAuthorizationFinalizeResult{}, err
		}
		record, err := readWorkspaceCredentialAuthorization(ctx, transaction, s, operation, command.WorkspaceID, command.Kind, command.AuthorizationID, true)
		if err != nil {
			return WorkspaceCredentialAuthorizationFinalizeResult{}, err
		}
		if record.Status != WorkspaceCredentialAuthorizationPending || record.PollLeaseToken != command.LeaseToken {
			return WorkspaceCredentialAuthorizationFinalizeResult{}, commandError(ErrorConflict, operation, "credential_authorization", command.AuthorizationID, "credential authorization is no longer active")
		}
		isDefault := record.MakeDefault || (!record.TargetExists && !hasCredentialKind(ctx, transaction, s, record.WorkspaceID, record.Kind))
		if isDefault {
			if err := clearCredentialDefault(ctx, transaction, s, operation, record.WorkspaceID, record.Kind); err != nil {
				return WorkspaceCredentialAuthorizationFinalizeResult{}, err
			}
		}
		var binding corecredentials.Binding
		if record.TargetExists {
			query := fmt.Sprintf(`
UPDATE %s
SET display_name = $4, owner_scope = $5, owner_user_id = NULLIF($6,'')::uuid,
    public_metadata = $7, auth_type = $8, sealed_secret = $9, sealing_key_id = $10,
    access_expires_at = $11, refresh_expires_at = $12,
    status = 'active', is_default = CASE WHEN $13 THEN true ELSE is_default END,
    authority_version = authority_version + 1,
    credential_version = credential_version + 1,
    last_error_code = NULL, refresh_lease_token = NULL, refresh_lease_expires_at = NULL,
    updated_at = pg_catalog.clock_timestamp()
WHERE workspace_id = $1 AND kind = $2 AND id = $3
  AND authority_version = $14 AND credential_version = $15
RETURNING id::text, workspace_id::text, kind, display_name, owner_scope,
 owner_user_id::text, public_metadata, auth_type, sealed_secret,
 authority_version, credential_version, status, is_default,
 access_expires_at, refresh_expires_at`, s.table("workspace_credential_bindings"))
			binding, err = scanWorkspaceCredentialBinding(transaction.QueryRow(ctx, query,
				record.WorkspaceID, record.Kind, record.TargetBindingID, record.DisplayName, record.OwnerScope,
				record.OwnerUserID, normalizedJSON(command.PublicMetadata), command.AuthType, command.SealedSecret,
				command.SealingKeyID, command.AccessExpiresAt, command.RefreshExpiresAt, isDefault,
				record.ExpectedAuthorityVersion, record.ExpectedCredentialVersion), operation)
			if errors.Is(err, pgx.ErrNoRows) {
				return WorkspaceCredentialAuthorizationFinalizeResult{}, versionConflict(operation, "credential", record.TargetBindingID, record.ExpectedAuthorityVersion)
			}
		} else {
			query := fmt.Sprintf(`
INSERT INTO %s (
 id, workspace_id, kind, display_name, owner_scope, owner_user_id,
 public_metadata, auth_type, sealed_secret, sealing_key_id,
 access_expires_at, refresh_expires_at, is_default)
VALUES ($1,$2,$3,$4,$5,NULLIF($6,'')::uuid,$7,$8,$9,$10,$11,$12,$13)
RETURNING id::text, workspace_id::text, kind, display_name, owner_scope,
 owner_user_id::text, public_metadata, auth_type, sealed_secret,
 authority_version, credential_version, status, is_default,
 access_expires_at, refresh_expires_at`, s.table("workspace_credential_bindings"))
			binding, err = scanWorkspaceCredentialBinding(transaction.QueryRow(ctx, query,
				record.TargetBindingID, record.WorkspaceID, record.Kind, record.DisplayName,
				record.OwnerScope, record.OwnerUserID, normalizedJSON(command.PublicMetadata), command.AuthType,
				command.SealedSecret, command.SealingKeyID, command.AccessExpiresAt, command.RefreshExpiresAt, isDefault), operation)
		}
		if err != nil {
			if isUniqueViolation(err) {
				return WorkspaceCredentialAuthorizationFinalizeResult{}, commandError(ErrorConflict, operation, "credential", record.TargetBindingID, "credential binding identity or display name is already in use")
			}
			return WorkspaceCredentialAuthorizationFinalizeResult{}, databaseError(operation+" write binding", err)
		}
		update := fmt.Sprintf(`
UPDATE %s
SET status = 'succeeded', binding_id = target_binding_id,
    sealed_provider_state = NULL, poll_lease_token = NULL, poll_lease_expires_at = NULL,
    last_error_code = NULL, completed_at = pg_catalog.clock_timestamp(),
    version = version + 1, updated_at = pg_catalog.clock_timestamp()
WHERE workspace_id = $1 AND kind = $2 AND id = $3
  AND poll_lease_token = $4 AND status = 'pending'
  AND expires_at > pg_catalog.clock_timestamp()
RETURNING `+workspaceCredentialAuthorizationColumns, s.table("workspace_credential_authorizations"))
		completed, err := scanWorkspaceCredentialAuthorization(transaction.QueryRow(ctx, update,
			record.WorkspaceID, record.Kind, record.ID, command.LeaseToken), operation)
		if errors.Is(err, pgx.ErrNoRows) {
			return WorkspaceCredentialAuthorizationFinalizeResult{}, commandError(ErrorConflict, operation, "credential_authorization", command.AuthorizationID, "credential authorization is no longer active")
		}
		if err != nil {
			return WorkspaceCredentialAuthorizationFinalizeResult{}, databaseError(operation+" complete", err)
		}
		return WorkspaceCredentialAuthorizationFinalizeResult{Authorization: completed, Binding: binding}, nil
	})
}

func (s *StateStore) CancelWorkspaceCredentialAuthorization(ctx context.Context, command CancelWorkspaceCredentialAuthorizationCommand) (WorkspaceCredentialAuthorizationRecord, bool, error) {
	const operation = "CancelWorkspaceCredentialAuthorization"
	if err := validateCredentialAuthorizationIdentity(command.WorkspaceID, command.Kind, command.AuthorizationID, command.ActorID); err != nil || command.ExpectedVersion < 1 {
		return WorkspaceCredentialAuthorizationRecord{}, false, commandError(ErrorInvalidArgument, operation, "credential_authorization", command.AuthorizationID, "credential authorization identity or version is invalid")
	}
	type result struct {
		record  WorkspaceCredentialAuthorizationRecord
		changed bool
	}
	value, err := withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (result, error) {
		if err := requireCredentialManager(ctx, transaction, s, operation, command.WorkspaceID, command.ActorID); err != nil {
			return result{}, err
		}
		record, err := readWorkspaceCredentialAuthorization(ctx, transaction, s, operation, command.WorkspaceID, command.Kind, command.AuthorizationID, true)
		if err != nil {
			return result{}, err
		}
		if record.Version != command.ExpectedVersion {
			return result{}, versionConflict(operation, "credential_authorization", command.AuthorizationID, command.ExpectedVersion)
		}
		if record.Status != WorkspaceCredentialAuthorizationPending {
			return result{record: record}, nil
		}
		query := fmt.Sprintf(`
UPDATE %s
SET status = 'cancelled', sealed_provider_state = NULL,
    poll_lease_token = NULL, poll_lease_expires_at = NULL,
    completed_at = pg_catalog.clock_timestamp(), version = version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE workspace_id = $1 AND kind = $2 AND id = $3 AND version = $4 AND status = 'pending'
RETURNING `+workspaceCredentialAuthorizationColumns, s.table("workspace_credential_authorizations"))
		cancelled, err := scanWorkspaceCredentialAuthorization(transaction.QueryRow(ctx, query,
			command.WorkspaceID, command.Kind, command.AuthorizationID, command.ExpectedVersion), operation)
		if err != nil {
			return result{}, databaseError(operation+" cancel", err)
		}
		return result{record: cancelled, changed: true}, nil
	})
	return value.record, value.changed, err
}

const workspaceCredentialAuthorizationColumns = `
id::text, workspace_id::text, kind, actor_id::text, target_binding_id::text,
target_exists, COALESCE(expected_authority_version, 0), COALESCE(expected_credential_version, 0),
display_name, owner_scope, COALESCE(owner_user_id::text, ''), make_default,
provider_public, user_code, verification_uri, verification_uri_complete,
COALESCE(sealed_provider_state, ''::bytea), sealing_key_id, provider_state_version,
status, poll_interval_seconds, next_poll_at, expires_at,
COALESCE(poll_lease_token::text, ''), poll_lease_expires_at,
COALESCE(binding_id::text, ''), COALESCE(last_error_code, ''), version,
created_at, updated_at, completed_at`

type credentialAuthorizationRow interface{ Scan(...any) error }

func scanWorkspaceCredentialAuthorization(row credentialAuthorizationRow, operation string) (WorkspaceCredentialAuthorizationRecord, error) {
	var record WorkspaceCredentialAuthorizationRecord
	err := row.Scan(
		&record.ID, &record.WorkspaceID, &record.Kind, &record.ActorID, &record.TargetBindingID,
		&record.TargetExists, &record.ExpectedAuthorityVersion, &record.ExpectedCredentialVersion,
		&record.DisplayName, &record.OwnerScope, &record.OwnerUserID, &record.MakeDefault,
		&record.ProviderPublic, &record.UserCode, &record.VerificationURI, &record.VerificationURIComplete,
		&record.SealedProviderState, &record.SealingKeyID, &record.ProviderStateVersion,
		&record.Status, &record.PollIntervalSeconds, &record.NextPollAt, &record.ExpiresAt,
		&record.PollLeaseToken, &record.PollLeaseExpiresAt, &record.BindingID, &record.LastErrorCode,
		&record.Version, &record.CreatedAt, &record.UpdatedAt, &record.CompletedAt,
	)
	if err != nil {
		return WorkspaceCredentialAuthorizationRecord{}, err
	}
	return record, nil
}

func readWorkspaceCredentialAuthorization(ctx context.Context, transaction pgx.Tx, s *StateStore, operation, workspaceID, kind, authorizationID string, lock bool) (WorkspaceCredentialAuthorizationRecord, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	query := fmt.Sprintf(`SELECT `+workspaceCredentialAuthorizationColumns+` FROM %s WHERE workspace_id = $1 AND kind = $2 AND id = $3`+suffix, s.table("workspace_credential_authorizations"))
	record, err := scanWorkspaceCredentialAuthorization(transaction.QueryRow(ctx, query, workspaceID, kind, authorizationID), operation)
	if errors.Is(err, pgx.ErrNoRows) {
		return WorkspaceCredentialAuthorizationRecord{}, commandError(ErrorNotFound, operation, "credential_authorization", authorizationID, "credential authorization was not found")
	}
	if err != nil {
		return WorkspaceCredentialAuthorizationRecord{}, databaseError(operation+" read", err)
	}
	return record, nil
}

func expireWorkspaceCredentialAuthorization(ctx context.Context, transaction pgx.Tx, s *StateStore, operation, workspaceID, kind, authorizationID string) error {
	query := fmt.Sprintf(`
UPDATE %s
SET status = 'expired', sealed_provider_state = NULL,
    poll_lease_token = NULL, poll_lease_expires_at = NULL,
    last_error_code = 'expired_token', completed_at = pg_catalog.clock_timestamp(),
    version = version + 1, updated_at = pg_catalog.clock_timestamp()
WHERE workspace_id = $1 AND kind = $2 AND id = $3
  AND status = 'pending' AND expires_at <= pg_catalog.clock_timestamp()`, s.table("workspace_credential_authorizations"))
	if _, err := transaction.Exec(ctx, query, workspaceID, kind, authorizationID); err != nil {
		return databaseError(operation+" expire", err)
	}
	return nil
}

func validateCredentialAuthorizationRecord(record WorkspaceCredentialAuthorizationRecord) error {
	if err := validateCredentialAuthorizationIdentity(record.WorkspaceID, record.Kind, record.ID, record.ActorID); err != nil {
		return err
	}
	if err := validateUUID("target_binding_id", record.TargetBindingID); err != nil {
		return err
	}
	if record.TargetExists != (record.ExpectedAuthorityVersion > 0 && record.ExpectedCredentialVersion > 0) {
		return errors.New("target credential versions are invalid")
	}
	if strings.TrimSpace(record.DisplayName) != record.DisplayName || record.DisplayName == "" || len(record.DisplayName) > 256 ||
		(record.OwnerScope != corecredentials.OwnerScopeWorkspace && record.OwnerScope != corecredentials.OwnerScopeUser) ||
		(record.OwnerScope == corecredentials.OwnerScopeWorkspace) != (record.OwnerUserID == "") {
		return errors.New("credential authorization binding metadata is invalid")
	}
	if record.OwnerUserID != "" {
		if err := validateUUID("owner_user_id", record.OwnerUserID); err != nil {
			return err
		}
	}
	if len(record.SealedProviderState) == 0 || len(record.SealedProviderState) > 512*1024 || record.SealingKeyID == "" ||
		record.ProviderStateVersion < 1 || record.Status != WorkspaceCredentialAuthorizationPending ||
		record.PollIntervalSeconds < 1 || record.PollIntervalSeconds > 60 || record.NextPollAt.IsZero() ||
		record.ExpiresAt.IsZero() || !record.ExpiresAt.After(record.NextPollAt) ||
		len(record.UserCode) > 1024 || len(record.VerificationURI) < 8 || len(record.VerificationURI) > 8192 ||
		len(record.VerificationURIComplete) < 8 || len(record.VerificationURIComplete) > 8192 {
		return errors.New("credential authorization challenge is invalid")
	}
	return nil
}

func validateCredentialAuthorizationIdentity(workspaceID, kind, authorizationID, actorID string) error {
	if err := validateUUID("workspace_id", workspaceID); err != nil {
		return err
	}
	if !corecredentialsKindPattern.MatchString(kind) {
		return errors.New("provider kind is invalid")
	}
	if err := validateUUID("authorization_id", authorizationID); err != nil {
		return err
	}
	return validateUUID("actor_id", actorID)
}

func validCredentialAuthorizationFinishStatus(status string) bool {
	switch status {
	case WorkspaceCredentialAuthorizationPending, WorkspaceCredentialAuthorizationDenied,
		WorkspaceCredentialAuthorizationExpired, WorkspaceCredentialAuthorizationFailed:
		return true
	default:
		return false
	}
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
