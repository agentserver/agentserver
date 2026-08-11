package coredb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecredentials"
	"github.com/jackc/pgx/v5"
)

type ClaimWorkspaceCredentialRefreshCommand struct {
	WorkspaceID    string
	Kind           string
	BindingID      string
	Before         time.Time
	LeaseToken     string
	LeaseExpiresAt time.Time
}

type CompleteWorkspaceCredentialRefreshCommand struct {
	WorkspaceID               string
	Kind                      string
	BindingID                 string
	ExpectedAuthorityVersion  int64
	ExpectedCredentialVersion int64
	LeaseToken                string
	AuthType                  string
	PublicMetadata            json.RawMessage
	SealedSecret              []byte
	SealingKeyID              string
	AccessExpiresAt           *time.Time
	RefreshExpiresAt          *time.Time
}

type FailWorkspaceCredentialRefreshCommand struct {
	WorkspaceID               string
	Kind                      string
	BindingID                 string
	ExpectedAuthorityVersion  int64
	ExpectedCredentialVersion int64
	LeaseToken                string
	ErrorCode                 string
	Terminal                  bool
}

func (s *StateStore) ClaimWorkspaceCredentialRefresh(ctx context.Context, command ClaimWorkspaceCredentialRefreshCommand) (corecredentials.Binding, bool, error) {
	const operation = "ClaimWorkspaceCredentialRefresh"
	if err := validateCredentialIdentity(command.WorkspaceID, command.Kind, command.BindingID); err != nil ||
		validateUUID("refresh_lease_token", command.LeaseToken) != nil || command.Before.IsZero() || command.LeaseExpiresAt.IsZero() {
		return corecredentials.Binding{}, false, commandError(ErrorInvalidArgument, operation, "credential", command.BindingID, "credential refresh claim is invalid")
	}
	type result struct {
		binding corecredentials.Binding
		claimed bool
	}
	value, err := withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (result, error) {
		query := fmt.Sprintf(`
UPDATE %s
SET refresh_lease_token = $5, refresh_lease_expires_at = $6,
    updated_at = pg_catalog.clock_timestamp()
WHERE workspace_id = $1 AND kind = $2 AND id = $3
  AND status = 'active' AND auth_type = 'device_oauth'
  AND access_expires_at IS NOT NULL AND access_expires_at <= $4
  AND refresh_expires_at IS NOT NULL AND refresh_expires_at > pg_catalog.clock_timestamp()
  AND (refresh_lease_token IS NULL OR refresh_lease_expires_at <= pg_catalog.clock_timestamp())
RETURNING id::text, workspace_id::text, kind, display_name, owner_scope,
 owner_user_id::text, public_metadata, auth_type, sealed_secret,
 authority_version, credential_version, status, is_default,
 access_expires_at, refresh_expires_at`, s.table("workspace_credential_bindings"))
		binding, err := scanWorkspaceCredentialBinding(transaction.QueryRow(ctx, query,
			command.WorkspaceID, command.Kind, command.BindingID, command.Before,
			command.LeaseToken, command.LeaseExpiresAt), operation)
		if errors.Is(err, pgx.ErrNoRows) {
			current, readErr := readWorkspaceCredentialBindingInTransaction(ctx, transaction, s, operation, command.WorkspaceID, command.Kind, command.BindingID)
			if readErr != nil {
				return result{}, readErr
			}
			return result{binding: current}, nil
		}
		if err != nil {
			return result{}, databaseError(operation+" claim", err)
		}
		return result{binding: binding, claimed: true}, nil
	})
	return value.binding, value.claimed, err
}

func (s *StateStore) CompleteWorkspaceCredentialRefresh(ctx context.Context, command CompleteWorkspaceCredentialRefreshCommand) (corecredentials.Binding, error) {
	const operation = "CompleteWorkspaceCredentialRefresh"
	if err := validateCredentialRefreshCompletion(command); err != nil {
		return corecredentials.Binding{}, commandError(ErrorInvalidArgument, operation, "credential", command.BindingID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (corecredentials.Binding, error) {
		query := fmt.Sprintf(`
UPDATE %s
SET public_metadata = $7, auth_type = $8, sealed_secret = $9, sealing_key_id = $10,
    access_expires_at = $11, refresh_expires_at = $12,
    credential_version = credential_version + 1,
    last_error_code = NULL, refresh_lease_token = NULL, refresh_lease_expires_at = NULL,
    updated_at = pg_catalog.clock_timestamp()
WHERE workspace_id = $1 AND kind = $2 AND id = $3
  AND authority_version = $4 AND credential_version = $5
  AND refresh_lease_token = $6 AND status = 'active'
RETURNING id::text, workspace_id::text, kind, display_name, owner_scope,
 owner_user_id::text, public_metadata, auth_type, sealed_secret,
 authority_version, credential_version, status, is_default,
 access_expires_at, refresh_expires_at`, s.table("workspace_credential_bindings"))
		binding, err := scanWorkspaceCredentialBinding(transaction.QueryRow(ctx, query,
			command.WorkspaceID, command.Kind, command.BindingID,
			command.ExpectedAuthorityVersion, command.ExpectedCredentialVersion, command.LeaseToken,
			normalizedJSON(command.PublicMetadata), command.AuthType, command.SealedSecret, command.SealingKeyID,
			command.AccessExpiresAt, command.RefreshExpiresAt), operation)
		if errors.Is(err, pgx.ErrNoRows) {
			return corecredentials.Binding{}, versionConflict(operation, "credential", command.BindingID, command.ExpectedAuthorityVersion)
		}
		if err != nil {
			return corecredentials.Binding{}, databaseError(operation+" complete", err)
		}
		return binding, nil
	})
}

func (s *StateStore) FailWorkspaceCredentialRefresh(ctx context.Context, command FailWorkspaceCredentialRefreshCommand) (corecredentials.Binding, error) {
	const operation = "FailWorkspaceCredentialRefresh"
	if err := validateCredentialIdentity(command.WorkspaceID, command.Kind, command.BindingID); err != nil ||
		validateUUID("refresh_lease_token", command.LeaseToken) != nil || command.ExpectedAuthorityVersion < 1 ||
		command.ExpectedCredentialVersion < 1 || command.ErrorCode == "" || len(command.ErrorCode) > 128 {
		return corecredentials.Binding{}, commandError(ErrorInvalidArgument, operation, "credential", command.BindingID, "credential refresh failure is invalid")
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (corecredentials.Binding, error) {
		query := fmt.Sprintf(`
UPDATE %s
SET status = CASE WHEN $8 THEN 'reauth_required' ELSE status END,
    is_default = CASE WHEN $8 THEN false ELSE is_default END,
    authority_version = authority_version + CASE WHEN $8 THEN 1 ELSE 0 END,
    last_error_code = $7, refresh_lease_token = NULL, refresh_lease_expires_at = NULL,
    updated_at = pg_catalog.clock_timestamp()
WHERE workspace_id = $1 AND kind = $2 AND id = $3
  AND authority_version = $4 AND credential_version = $5
  AND refresh_lease_token = $6 AND status = 'active'
RETURNING id::text, workspace_id::text, kind, display_name, owner_scope,
 owner_user_id::text, public_metadata, auth_type, sealed_secret,
 authority_version, credential_version, status, is_default,
 access_expires_at, refresh_expires_at`, s.table("workspace_credential_bindings"))
		binding, err := scanWorkspaceCredentialBinding(transaction.QueryRow(ctx, query,
			command.WorkspaceID, command.Kind, command.BindingID,
			command.ExpectedAuthorityVersion, command.ExpectedCredentialVersion,
			command.LeaseToken, command.ErrorCode, command.Terminal), operation)
		if errors.Is(err, pgx.ErrNoRows) {
			return corecredentials.Binding{}, versionConflict(operation, "credential", command.BindingID, command.ExpectedAuthorityVersion)
		}
		if err != nil {
			return corecredentials.Binding{}, databaseError(operation+" fail", err)
		}
		return binding, nil
	})
}

func validateCredentialRefreshCompletion(command CompleteWorkspaceCredentialRefreshCommand) error {
	if err := validateCredentialIdentity(command.WorkspaceID, command.Kind, command.BindingID); err != nil {
		return err
	}
	if err := validateUUID("refresh_lease_token", command.LeaseToken); err != nil {
		return err
	}
	if command.ExpectedAuthorityVersion < 1 || command.ExpectedCredentialVersion < 1 || command.AuthType != "device_oauth" ||
		len(command.SealedSecret) == 0 || len(command.SealedSecret) > 512*1024 || command.SealingKeyID == "" ||
		command.AccessExpiresAt == nil || command.RefreshExpiresAt == nil || !command.RefreshExpiresAt.After(*command.AccessExpiresAt) {
		return errors.New("refreshed credential material is invalid")
	}
	return nil
}
