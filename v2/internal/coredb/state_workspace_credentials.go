package coredb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecredentials"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	WorkspaceCredentialStatusActive         = corecredentials.StatusActive
	WorkspaceCredentialStatusReauthRequired = corecredentials.StatusReauthRequired
	WorkspaceCredentialStatusRevoked        = corecredentials.StatusRevoked
	WorkspaceCredentialStatusDisabled       = corecredentials.StatusDisabled
)

var corecredentialsKindPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

type CreateWorkspaceCredentialBindingCommand struct {
	ID               string
	WorkspaceID      string
	ActorID          string
	Kind             string
	DisplayName      string
	OwnerScope       string
	OwnerUserID      string
	PublicMetadata   json.RawMessage
	AuthType         string
	SealedSecret     []byte
	SealingKeyID     string
	AccessExpiresAt  *time.Time
	RefreshExpiresAt *time.Time
	MakeDefault      bool
}

type RotateWorkspaceCredentialBindingCommand struct {
	WorkspaceID               string
	BindingID                 string
	ActorID                   string
	ExpectedAuthorityVersion  int64
	ExpectedCredentialVersion int64
	SealedSecret              []byte
	SealingKeyID              string
	AuthType                  string
	AccessExpiresAt           *time.Time
	RefreshExpiresAt          *time.Time
}

type UpdateWorkspaceCredentialBindingCommand struct {
	WorkspaceID     string
	BindingID       string
	ActorID         string
	ExpectedVersion int64
	DisplayName     string
	PublicMetadata  json.RawMessage
	MakeDefault     bool
}

type RevokeWorkspaceCredentialBindingCommand struct {
	WorkspaceID     string
	BindingID       string
	ActorID         string
	ExpectedVersion int64
}

type DeleteWorkspaceCredentialBindingCommand struct {
	WorkspaceID     string
	BindingID       string
	ActorID         string
	ExpectedVersion int64
}

type WorkspaceCredentialBindingResult struct {
	Binding corecredentials.Binding
	Changed bool
}

func (s *StateStore) Get(ctx context.Context, workspaceID, kind, bindingID string) (corecredentials.Binding, error) {
	const operation = "GetWorkspaceCredentialBinding"
	if err := validateCredentialIdentity(workspaceID, kind, bindingID); err != nil {
		return corecredentials.Binding{}, commandError(ErrorInvalidArgument, operation, "credential", bindingID, err.Error())
	}
	return withStateReadTransaction(ctx, s, operation, func(transaction pgx.Tx) (corecredentials.Binding, error) {
		query := fmt.Sprintf(`
SELECT id::text, workspace_id::text, kind, display_name, owner_scope,
       owner_user_id::text, public_metadata, auth_type, sealed_secret,
       authority_version, credential_version, status, is_default,
       access_expires_at, refresh_expires_at
FROM %s
WHERE workspace_id = $1 AND kind = $2 AND id = $3`, s.table("workspace_credential_bindings"))
		binding, err := scanWorkspaceCredentialBinding(transaction.QueryRow(ctx, query, workspaceID, kind, bindingID), operation)
		if errors.Is(err, pgx.ErrNoRows) {
			// A missing workspace binding is normal product state. BindingStore
			// represents it as an empty binding so corecredentials can return
			// credential_not_configured instead of proxy_unavailable.
			return corecredentials.Binding{}, nil
		}
		return binding, err
	})
}

func (s *StateStore) List(ctx context.Context, workspaceID, kind string) ([]corecredentials.BindingMetadata, error) {
	const operation = "ListWorkspaceCredentialBindings"
	if err := validateWorkspaceCredentialScope(workspaceID); err != nil {
		return nil, commandError(ErrorInvalidArgument, operation, "workspace", workspaceID, err.Error())
	}
	if kind != "" && !corecredentialsKindPattern.MatchString(kind) {
		return nil, commandError(ErrorInvalidArgument, operation, "credential", kind, "provider kind is invalid")
	}
	return withStateReadTransaction(ctx, s, operation, func(transaction pgx.Tx) ([]corecredentials.BindingMetadata, error) {
		query := fmt.Sprintf(`
SELECT id::text, workspace_id::text, kind, display_name, owner_scope,
       owner_user_id::text, public_metadata, auth_type,
       authority_version, credential_version, status, is_default,
       access_expires_at, refresh_expires_at
FROM %s
WHERE workspace_id = $1 AND ($2 = '' OR kind = $2)
ORDER BY kind, is_default DESC, pg_catalog.lower(display_name), id
LIMIT 256`, s.table("workspace_credential_bindings"))
		rows, err := transaction.Query(ctx, query, workspaceID, kind)
		if err != nil {
			return nil, databaseError(operation+" query", err)
		}
		defer rows.Close()
		result := make([]corecredentials.BindingMetadata, 0)
		for rows.Next() {
			binding, err := scanWorkspaceCredentialMetadata(rows, operation)
			if err != nil {
				return nil, err
			}
			result = append(result, binding)
		}
		if err := rows.Err(); err != nil {
			return nil, databaseError(operation+" iterate", err)
		}
		return result, nil
	})
}

// AuthorizeCredentialUse is the live, operation-bound check used by
// corecredentials. It deliberately returns only versioned binding identity.
func (s *StateStore) AuthorizeCredentialUse(ctx context.Context, request corecredentials.UseRequest) (corecredentials.BindingReference, error) {
	const operation = "AuthorizeCredentialUse"
	if err := request.ValidateLiveAuthorityScope(); err != nil {
		return corecredentials.BindingReference{}, commandError(ErrorInvalidArgument, operation, "credential_use", request.OperationID, err.Error())
	}
	if err := validateUUIDFieldsForCredentialUse(request); err != nil {
		return corecredentials.BindingReference{}, commandError(ErrorInvalidArgument, operation, "credential_use", request.OperationID, err.Error())
	}
	return withStateReadTransaction(ctx, s, operation, func(transaction pgx.Tx) (corecredentials.BindingReference, error) {
		query := fmt.Sprintf(`
WITH authority_time AS MATERIALIZED (
    SELECT pg_catalog.clock_timestamp() AS now
)
SELECT binding.authority_version, binding.credential_version
FROM authority_time
JOIN %s AS workspace
  ON workspace.id = $1
 AND workspace.status = 'active'
 AND (
      ($12 = 'lark' AND workspace.managed_lark_credential_mode = $16)
      OR ($12 = 'bytecloud' AND $16 = 'process_env')
 )
JOIN %s AS member
  ON member.workspace_id = workspace.id
 AND member.user_id = $3
 AND member.role IN ('owner', 'developer')
JOIN %s AS local_user
  ON local_user.id = member.user_id
 AND local_user.status = 'active'
JOIN %s AS session
  ON session.id = $2
 AND session.workspace_id = workspace.id
 AND session.creator_id = member.user_id
 AND session.status = 'active'
JOIN %s AS run
  ON run.id = $5
 AND run.workspace_id = workspace.id
 AND run.session_id = session.id
 AND run.actor_id = member.user_id
 AND run.status = 'running'
 AND run.current_attempt_generation = $7
 AND session.active_run_id = run.id
JOIN %s AS attempt
  ON attempt.id = $6
 AND attempt.run_id = run.id
 AND attempt.generation = $7
 AND attempt.status = 'running'
 AND attempt.turn_started_at IS NOT NULL
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
  ON execution.id = $8
 AND execution.run_id = run.id
 AND execution.run_attempt_id = attempt.id
 AND execution.run_attempt_generation = attempt.generation
 AND execution.env_id = $4
 AND execution.tool_name = 'shell'
 AND execution.status IN ('dispatching', 'running')
 AND execution.target_kind = 'tae'
 AND execution.target_id = $10
 AND execution.target_generation = $11
JOIN %s AS operation_row
  ON operation_row.id = $9
 AND operation_row.execution_id = execution.id
 AND operation_row.kind = 'process_start'
  AND operation_row.status IN ('dispatching', 'acknowledged')
  AND operation_row.target_kind = 'tae'
 AND operation_row.target_id = execution.target_id
 AND operation_row.target_generation = execution.target_generation
JOIN %s AS sandbox
  ON sandbox.id = execution.target_id
 AND sandbox.generation = execution.target_generation
 AND sandbox.workspace_id = run.workspace_id
 AND sandbox.session_id = run.session_id
 AND sandbox.environment_id = execution.env_id
 AND sandbox.provider_kind = 'tae'
 AND sandbox.provider_psm = $15
 AND sandbox.desired_state = 'ready'
 AND sandbox.observed_state = 'ready'
 AND sandbox.expires_at > authority_time.now
JOIN %s AS activity
  ON activity.sandbox_id = sandbox.id
 AND activity.target_generation = sandbox.generation
 AND activity.run_attempt_id = attempt.id
 AND activity.run_attempt_generation = attempt.generation
 AND activity.released_at IS NULL
 AND activity.lease_expires_at > authority_time.now
JOIN %s AS binding
  ON binding.workspace_id = workspace.id
 AND binding.kind = $12
 AND binding.id = $13
 AND binding.authority_version = $14
 AND ($17 = 0 OR binding.credential_version = $17)
 AND binding.status = 'active'
 AND (binding.owner_scope = 'workspace' OR binding.owner_user_id = member.user_id)
LIMIT 1`,
			s.table("workspaces"), s.table("workspace_members"), s.table("users"), s.table("sessions"),
			s.table("runs"), s.table("run_attempts"), s.table("session_leases"), s.table("attempt_leases"),
			s.table("executions"), s.table("execution_operations"), s.table("managed_sandboxes"),
			s.table("managed_sandbox_activities"), s.table("workspace_credential_bindings"))
		var authorityVersion, credentialVersion int64
		err := transaction.QueryRow(ctx, query,
			request.WorkspaceID, request.SessionID, request.ActorID, request.EnvironmentID,
			request.RunID, request.RunAttemptID, request.RunAttemptGeneration,
			request.ExecutionID, request.OperationID, request.SandboxID, request.TargetGeneration,
			request.ProviderKind, request.BindingID, request.AuthorityVersion, request.TAEPSM,
			request.CredentialMode, request.ExpectedCredentialVersion,
		).Scan(&authorityVersion, &credentialVersion)
		if errors.Is(err, pgx.ErrNoRows) {
			return corecredentials.BindingReference{}, commandError(ErrorForbidden, operation, "credential_use", request.OperationID, "live operation authority is not active")
		}
		if err != nil {
			return corecredentials.BindingReference{}, databaseError(operation+" query authority", err)
		}
		return corecredentials.BindingReference{
			WorkspaceID: request.WorkspaceID, Kind: request.ProviderKind, BindingID: request.BindingID,
			AuthorityVersion: authorityVersion, CredentialVersion: credentialVersion,
			CredentialMode: request.CredentialMode,
		}, nil
	})
}

// ResolveCredentialAuthority selects the active default binding for an
// operation before a process-bound placeholder is minted. It returns only a
// versioned reference; an absent binding is normal product state and is
// represented by a zero reference so sandbox ensure/local shell can continue.
func (s *StateStore) ResolveCredentialAuthority(ctx context.Context, request corecredentials.AuthorityRequest) (corecredentials.BindingReference, error) {
	const operation = "ResolveCredentialAuthority"
	if err := request.Validate(); err != nil {
		return corecredentials.BindingReference{}, commandError(ErrorInvalidArgument, operation, "credential_use", request.OperationID, err.Error())
	}
	if err := validateUUIDFieldsForCredentialAuthority(request); err != nil {
		return corecredentials.BindingReference{}, commandError(ErrorInvalidArgument, operation, "credential_use", request.OperationID, err.Error())
	}
	return withStateReadTransaction(ctx, s, operation, func(transaction pgx.Tx) (corecredentials.BindingReference, error) {
		query := fmt.Sprintf(`
WITH authority_time AS MATERIALIZED (
    SELECT pg_catalog.clock_timestamp() AS now
)
SELECT COALESCE(binding.id::text, ''),
       COALESCE(binding.authority_version, 0),
       COALESCE(binding.credential_version, 0),
       CASE
         WHEN $12 = 'lark' THEN workspace.managed_lark_credential_mode
         WHEN $12 = 'bytecloud' THEN 'process_env'
         ELSE ''
       END
FROM authority_time
JOIN %s AS workspace
  ON workspace.id = $1
 AND workspace.status = 'active'
 AND $12 IN ('lark', 'bytecloud')
JOIN %s AS member
  ON member.workspace_id = workspace.id
 AND member.user_id = $3
 AND member.role IN ('owner', 'developer')
JOIN %s AS local_user
  ON local_user.id = member.user_id
 AND local_user.status = 'active'
JOIN %s AS session
  ON session.id = $2
 AND session.workspace_id = workspace.id
 AND session.creator_id = member.user_id
 AND session.status = 'active'
JOIN %s AS run
  ON run.id = $5
 AND run.workspace_id = workspace.id
 AND run.session_id = session.id
 AND run.actor_id = member.user_id
 AND run.status = 'running'
 AND run.current_attempt_generation = $7
 AND session.active_run_id = run.id
JOIN %s AS attempt
  ON attempt.id = $6
 AND attempt.run_id = run.id
 AND attempt.generation = $7
 AND attempt.status = 'running'
 AND attempt.turn_started_at IS NOT NULL
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
  ON execution.id = $8
 AND execution.run_id = run.id
 AND execution.run_attempt_id = attempt.id
 AND execution.run_attempt_generation = attempt.generation
 AND execution.env_id = $4
 AND execution.tool_name = 'shell'
 AND execution.status IN ('dispatching', 'running')
 AND execution.target_kind = 'tae'
 AND execution.target_id = $10
 AND execution.target_generation = $11
JOIN %s AS operation_row
  ON operation_row.id = $9
 AND operation_row.execution_id = execution.id
 AND operation_row.kind = 'process_start'
 AND operation_row.status = 'dispatching'
 AND operation_row.target_kind = 'tae'
 AND operation_row.target_id = execution.target_id
 AND operation_row.target_generation = execution.target_generation
JOIN %s AS sandbox
  ON sandbox.id = execution.target_id
 AND sandbox.generation = execution.target_generation
 AND sandbox.workspace_id = run.workspace_id
 AND sandbox.session_id = run.session_id
 AND sandbox.environment_id = execution.env_id
 AND sandbox.provider_kind = 'tae'
 AND sandbox.desired_state = 'ready'
 AND sandbox.observed_state = 'ready'
 AND sandbox.expires_at > authority_time.now
JOIN %s AS activity
  ON activity.sandbox_id = sandbox.id
 AND activity.target_generation = sandbox.generation
 AND activity.run_attempt_id = attempt.id
 AND activity.run_attempt_generation = attempt.generation
 AND activity.released_at IS NULL
 AND activity.lease_expires_at > authority_time.now
LEFT JOIN %s AS binding
  ON binding.workspace_id = workspace.id
 AND binding.kind = $12
 AND binding.is_default
 AND binding.status = 'active'
 AND (binding.owner_scope = 'workspace' OR binding.owner_user_id = member.user_id)
ORDER BY binding.updated_at DESC, binding.id
LIMIT 1`, s.table("workspaces"), s.table("workspace_members"), s.table("users"), s.table("sessions"),
			s.table("runs"), s.table("run_attempts"), s.table("session_leases"), s.table("attempt_leases"),
			s.table("executions"), s.table("execution_operations"), s.table("managed_sandboxes"),
			s.table("managed_sandbox_activities"), s.table("workspace_credential_bindings"))
		var bindingID, credentialMode string
		var authorityVersion, credentialVersion int64
		err := transaction.QueryRow(ctx, query,
			request.WorkspaceID, request.SessionID, request.ActorID, request.EnvironmentID,
			request.RunID, request.RunAttemptID, request.RunAttemptGeneration,
			request.ExecutionID, request.OperationID, request.SandboxID, request.TargetGeneration,
			request.ProviderKind,
		).Scan(&bindingID, &authorityVersion, &credentialVersion, &credentialMode)
		if errors.Is(err, pgx.ErrNoRows) {
			return corecredentials.BindingReference{}, commandError(ErrorForbidden, operation, "credential_use", request.OperationID, "live operation authority is not active")
		}
		if err != nil {
			return corecredentials.BindingReference{}, databaseError(operation+" query", err)
		}
		return corecredentials.BindingReference{WorkspaceID: request.WorkspaceID, Kind: request.ProviderKind,
			BindingID: bindingID, AuthorityVersion: authorityVersion, CredentialVersion: credentialVersion,
			CredentialMode: credentialMode}, nil
	})
}

func (s *StateStore) CreateWorkspaceCredentialBinding(ctx context.Context, command CreateWorkspaceCredentialBindingCommand) (WorkspaceCredentialBindingResult, error) {
	const operation = "CreateWorkspaceCredentialBinding"
	if err := validateCreateWorkspaceCredentialBinding(command); err != nil {
		return WorkspaceCredentialBindingResult{}, commandError(ErrorInvalidArgument, operation, "credential", command.ID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (WorkspaceCredentialBindingResult, error) {
		if err := requireCredentialManager(ctx, transaction, s, operation, command.WorkspaceID, command.ActorID); err != nil {
			return WorkspaceCredentialBindingResult{}, err
		}
		isDefault := command.MakeDefault || !hasCredentialKind(ctx, transaction, s, command.WorkspaceID, command.Kind)
		if isDefault {
			if err := clearCredentialDefault(ctx, transaction, s, operation, command.WorkspaceID, command.Kind); err != nil {
				return WorkspaceCredentialBindingResult{}, err
			}
		}
		query := fmt.Sprintf(`
INSERT INTO %s (
 id, workspace_id, kind, display_name, owner_scope, owner_user_id,
 public_metadata, auth_type, sealed_secret, sealing_key_id,
 access_expires_at, refresh_expires_at, is_default)
VALUES ($1,$2,$3,$4,$5,NULLIF($6,'')::uuid,$7,$8,$9,$10,$11,$12,$13)
RETURNING id::text, workspace_id::text, kind, display_name, owner_scope,
 owner_user_id::text, public_metadata, auth_type, sealed_secret,
 authority_version, credential_version, status, is_default,
 access_expires_at, refresh_expires_at`,
			s.table("workspace_credential_bindings"))
		binding, err := scanWorkspaceCredentialBinding(transaction.QueryRow(ctx, query,
			command.ID, command.WorkspaceID, command.Kind, command.DisplayName, command.OwnerScope,
			command.OwnerUserID, normalizedJSON(command.PublicMetadata), command.AuthType,
			command.SealedSecret, command.SealingKeyID, command.AccessExpiresAt, command.RefreshExpiresAt,
			isDefault,
		), operation)
		if err != nil {
			if isUniqueViolation(err) {
				return WorkspaceCredentialBindingResult{}, commandError(ErrorConflict, operation, "credential", command.ID, "credential binding identity is already in use")
			}
			return WorkspaceCredentialBindingResult{}, databaseError(operation+" insert", err)
		}
		return WorkspaceCredentialBindingResult{Binding: binding, Changed: true}, nil
	})
}

func (s *StateStore) RotateWorkspaceCredentialBinding(ctx context.Context, command RotateWorkspaceCredentialBindingCommand) (WorkspaceCredentialBindingResult, error) {
	const operation = "RotateWorkspaceCredentialBinding"
	if err := validateRotateWorkspaceCredentialBinding(command); err != nil {
		return WorkspaceCredentialBindingResult{}, commandError(ErrorInvalidArgument, operation, "credential", command.BindingID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (WorkspaceCredentialBindingResult, error) {
		if err := requireCredentialManager(ctx, transaction, s, operation, command.WorkspaceID, command.ActorID); err != nil {
			return WorkspaceCredentialBindingResult{}, err
		}
		query := fmt.Sprintf(`
UPDATE %s
SET sealed_secret = $3, sealing_key_id = $4, auth_type = $5,
    access_expires_at = $6, refresh_expires_at = $7,
    credential_version = credential_version + 1,
    status = 'active', updated_at = pg_catalog.clock_timestamp()
WHERE workspace_id = $1 AND id = $2
  AND authority_version = $8 AND credential_version = $9
RETURNING id::text, workspace_id::text, kind, display_name, owner_scope,
 owner_user_id::text, public_metadata, auth_type, sealed_secret,
 authority_version, credential_version, status, is_default,
 access_expires_at, refresh_expires_at`,
			s.table("workspace_credential_bindings"))
		binding, err := scanWorkspaceCredentialBinding(transaction.QueryRow(ctx, query,
			command.WorkspaceID, command.BindingID, command.SealedSecret, command.SealingKeyID,
			command.AuthType, command.AccessExpiresAt, command.RefreshExpiresAt,
			command.ExpectedAuthorityVersion, command.ExpectedCredentialVersion), operation)
		if errors.Is(err, pgx.ErrNoRows) {
			return WorkspaceCredentialBindingResult{}, versionConflict(operation, "credential", command.BindingID, command.ExpectedAuthorityVersion)
		}
		if err != nil {
			return WorkspaceCredentialBindingResult{}, databaseError(operation+" rotate", err)
		}
		return WorkspaceCredentialBindingResult{Binding: binding, Changed: true}, nil
	})
}

func (s *StateStore) RevokeWorkspaceCredentialBinding(ctx context.Context, command RevokeWorkspaceCredentialBindingCommand) (WorkspaceCredentialBindingResult, error) {
	const operation = "RevokeWorkspaceCredentialBinding"
	if err := validateCredentialMutationScope(command.WorkspaceID, command.BindingID, command.ActorID, command.ExpectedVersion); err != nil {
		return WorkspaceCredentialBindingResult{}, commandError(ErrorInvalidArgument, operation, "credential", command.BindingID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (WorkspaceCredentialBindingResult, error) {
		if err := requireCredentialManager(ctx, transaction, s, operation, command.WorkspaceID, command.ActorID); err != nil {
			return WorkspaceCredentialBindingResult{}, err
		}
		query := fmt.Sprintf(`
UPDATE %s SET status = 'revoked', is_default = false,
 authority_version = authority_version + 1, updated_at = pg_catalog.clock_timestamp()
WHERE workspace_id = $1 AND id = $2 AND authority_version = $3
RETURNING id::text, workspace_id::text, kind, display_name, owner_scope,
 owner_user_id::text, public_metadata, auth_type, sealed_secret,
 authority_version, credential_version, status, is_default,
 access_expires_at, refresh_expires_at`,
			s.table("workspace_credential_bindings"))
		binding, err := scanWorkspaceCredentialBinding(transaction.QueryRow(ctx, query,
			command.WorkspaceID, command.BindingID, command.ExpectedVersion), operation)
		if errors.Is(err, pgx.ErrNoRows) {
			return WorkspaceCredentialBindingResult{}, versionConflict(operation, "credential", command.BindingID, command.ExpectedVersion)
		}
		if err != nil {
			return WorkspaceCredentialBindingResult{}, databaseError(operation+" revoke", err)
		}
		return WorkspaceCredentialBindingResult{Binding: binding, Changed: true}, nil
	})
}

func (s *StateStore) SetDefaultWorkspaceCredentialBinding(ctx context.Context, workspaceID, bindingID, actorID string, expectedVersion int64) (WorkspaceCredentialBindingResult, error) {
	const operation = "SetDefaultWorkspaceCredentialBinding"
	if err := validateCredentialMutationScope(workspaceID, bindingID, actorID, expectedVersion); err != nil {
		return WorkspaceCredentialBindingResult{}, commandError(ErrorInvalidArgument, operation, "credential", bindingID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (WorkspaceCredentialBindingResult, error) {
		if err := requireCredentialManager(ctx, transaction, s, operation, workspaceID, actorID); err != nil {
			return WorkspaceCredentialBindingResult{}, err
		}
		query := fmt.Sprintf(`SELECT kind FROM %s WHERE workspace_id = $1 AND id = $2 AND authority_version = $3 AND status = 'active' FOR UPDATE`, s.table("workspace_credential_bindings"))
		var kind string
		if err := transaction.QueryRow(ctx, query, workspaceID, bindingID, expectedVersion).Scan(&kind); errors.Is(err, pgx.ErrNoRows) {
			return WorkspaceCredentialBindingResult{}, commandError(ErrorNotFound, operation, "credential", bindingID, "active credential binding was not found")
		} else if err != nil {
			return WorkspaceCredentialBindingResult{}, databaseError(operation+" read binding", err)
		}
		if err := clearCredentialDefault(ctx, transaction, s, operation, workspaceID, kind); err != nil {
			return WorkspaceCredentialBindingResult{}, err
		}
		update := fmt.Sprintf(`UPDATE %s SET is_default = true, authority_version = authority_version + 1, updated_at = pg_catalog.clock_timestamp() WHERE workspace_id = $1 AND id = $2`, s.table("workspace_credential_bindings"))
		if _, err := transaction.Exec(ctx, update, workspaceID, bindingID); err != nil {
			return WorkspaceCredentialBindingResult{}, databaseError(operation+" set default", err)
		}
		binding, err := readWorkspaceCredentialBindingInTransaction(ctx, transaction, s, operation, workspaceID, kind, bindingID)
		if err != nil {
			return WorkspaceCredentialBindingResult{}, err
		}
		return WorkspaceCredentialBindingResult{Binding: binding, Changed: true}, nil
	})
}

func (s *StateStore) UpdateWorkspaceCredentialBinding(ctx context.Context, command UpdateWorkspaceCredentialBindingCommand) (WorkspaceCredentialBindingResult, error) {
	const operation = "UpdateWorkspaceCredentialBinding"
	if err := validateCredentialMutationScope(command.WorkspaceID, command.BindingID, command.ActorID, command.ExpectedVersion); err != nil {
		return WorkspaceCredentialBindingResult{}, commandError(ErrorInvalidArgument, operation, "credential", command.BindingID, err.Error())
	}
	if strings.TrimSpace(command.DisplayName) != command.DisplayName || command.DisplayName == "" || len(command.DisplayName) > 256 {
		return WorkspaceCredentialBindingResult{}, commandError(ErrorInvalidArgument, operation, "credential", command.BindingID, "display name is invalid")
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (WorkspaceCredentialBindingResult, error) {
		if err := requireCredentialManager(ctx, transaction, s, operation, command.WorkspaceID, command.ActorID); err != nil {
			return WorkspaceCredentialBindingResult{}, err
		}
		binding, err := readWorkspaceCredentialBindingInTransaction(ctx, transaction, s, operation, command.WorkspaceID, "", command.BindingID)
		if err != nil {
			return WorkspaceCredentialBindingResult{}, err
		}
		if binding.AuthorityVersion != command.ExpectedVersion {
			return WorkspaceCredentialBindingResult{}, versionConflict(operation, "credential", command.BindingID, command.ExpectedVersion)
		}
		if command.MakeDefault {
			if err := clearCredentialDefault(ctx, transaction, s, operation, command.WorkspaceID, binding.Kind); err != nil {
				return WorkspaceCredentialBindingResult{}, err
			}
		}
		query := fmt.Sprintf(`UPDATE %s SET display_name = $3, public_metadata = $4, is_default = CASE WHEN $5 THEN true ELSE is_default END, authority_version = authority_version + 1, updated_at = pg_catalog.clock_timestamp() WHERE workspace_id = $1 AND id = $2`, s.table("workspace_credential_bindings"))
		if _, err := transaction.Exec(ctx, query, command.WorkspaceID, command.BindingID, command.DisplayName, normalizedJSON(command.PublicMetadata), command.MakeDefault); err != nil {
			if isUniqueViolation(err) {
				return WorkspaceCredentialBindingResult{}, commandError(ErrorConflict, operation, "credential", command.BindingID, "credential display name is already in use")
			}
			return WorkspaceCredentialBindingResult{}, databaseError(operation+" update", err)
		}
		binding, err = readWorkspaceCredentialBindingInTransaction(ctx, transaction, s, operation, command.WorkspaceID, binding.Kind, command.BindingID)
		if err != nil {
			return WorkspaceCredentialBindingResult{}, err
		}
		return WorkspaceCredentialBindingResult{Binding: binding, Changed: true}, nil
	})
}

func (s *StateStore) DeleteWorkspaceCredentialBinding(ctx context.Context, command DeleteWorkspaceCredentialBindingCommand) (string, bool, error) {
	const operation = "DeleteWorkspaceCredentialBinding"
	if err := validateCredentialMutationScope(command.WorkspaceID, command.BindingID, command.ActorID, command.ExpectedVersion); err != nil {
		return "", false, commandError(ErrorInvalidArgument, operation, "credential", command.BindingID, err.Error())
	}
	type deleteResult struct {
		id      string
		deleted bool
	}
	result, err := withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (deleteResult, error) {
		if err := requireCredentialManager(ctx, transaction, s, operation, command.WorkspaceID, command.ActorID); err != nil {
			return deleteResult{}, err
		}
		query := fmt.Sprintf(`DELETE FROM %s WHERE workspace_id = $1 AND id = $2 AND authority_version = $3 RETURNING kind, is_default`, s.table("workspace_credential_bindings"))
		var kind string
		var isDefault bool
		if err := transaction.QueryRow(ctx, query, command.WorkspaceID, command.BindingID, command.ExpectedVersion).Scan(&kind, &isDefault); errors.Is(err, pgx.ErrNoRows) {
			return deleteResult{}, versionConflict(operation, "credential", command.BindingID, command.ExpectedVersion)
		} else if err != nil {
			return deleteResult{}, databaseError(operation+" delete", err)
		}
		if isDefault {
			promote := fmt.Sprintf(`UPDATE %s SET is_default = true, authority_version = authority_version + 1, updated_at = pg_catalog.clock_timestamp() WHERE id = (SELECT id FROM %s WHERE workspace_id = $1 AND kind = $2 AND status = 'active' ORDER BY created_at, id LIMIT 1)`, s.table("workspace_credential_bindings"), s.table("workspace_credential_bindings"))
			if _, err := transaction.Exec(ctx, promote, command.WorkspaceID, kind); err != nil {
				return deleteResult{}, databaseError(operation+" promote default", err)
			}
		}
		return deleteResult{id: command.BindingID, deleted: true}, nil
	})
	return result.id, result.deleted, err
}

func readWorkspaceCredentialBindingInTransaction(ctx context.Context, transaction pgx.Tx, s *StateStore, operation, workspaceID, kind, bindingID string) (corecredentials.Binding, error) {
	query := fmt.Sprintf(`
SELECT id::text, workspace_id::text, kind, display_name, owner_scope,
       owner_user_id::text, public_metadata, auth_type, sealed_secret,
       authority_version, credential_version, status, is_default,
       access_expires_at, refresh_expires_at
FROM %s
WHERE workspace_id = $1 AND id = $2 AND ($3 = '' OR kind = $3)
FOR UPDATE`, s.table("workspace_credential_bindings"))
	return scanWorkspaceCredentialBinding(transaction.QueryRow(ctx, query, workspaceID, bindingID, kind), operation)
}

func requireCredentialManager(ctx context.Context, transaction pgx.Tx, s *StateStore, operation, workspaceID, actorID string) error {
	if err := validateWorkspaceCredentialScope(workspaceID, actorID); err != nil {
		return commandError(ErrorInvalidArgument, operation, "workspace", workspaceID, err.Error())
	}
	query := fmt.Sprintf(`
SELECT 1
FROM %s AS workspace
JOIN %s AS member
  ON member.workspace_id = workspace.id
 AND member.user_id = $2
 AND member.role = 'owner'
JOIN %s AS local_user
  ON local_user.id = member.user_id
 AND local_user.status = 'active'
WHERE workspace.id = $1
  AND workspace.status = 'active'
FOR SHARE OF workspace, member, local_user`, s.table("workspaces"), s.table("workspace_members"), s.table("users"))
	var present int
	if err := transaction.QueryRow(ctx, query, workspaceID, actorID).Scan(&present); errors.Is(err, pgx.ErrNoRows) {
		return commandError(ErrorForbidden, operation, "workspace", workspaceID, "workspace credential administrator membership is required")
	} else if err != nil {
		return databaseError(operation+" authorize credential manager", err)
	}
	return nil
}

func clearCredentialDefault(ctx context.Context, transaction pgx.Tx, s *StateStore, operation, workspaceID, kind string) error {
	query := fmt.Sprintf(`UPDATE %s SET is_default = false, updated_at = pg_catalog.clock_timestamp() WHERE workspace_id = $1 AND kind = $2 AND is_default`, s.table("workspace_credential_bindings"))
	if _, err := transaction.Exec(ctx, query, workspaceID, kind); err != nil {
		return databaseError(operation+" clear default", err)
	}
	return nil
}

func hasCredentialKind(ctx context.Context, transaction pgx.Tx, s *StateStore, workspaceID, kind string) bool {
	query := fmt.Sprintf(`SELECT 1 FROM %s WHERE workspace_id = $1 AND kind = $2 LIMIT 1`, s.table("workspace_credential_bindings"))
	var present int
	return transaction.QueryRow(ctx, query, workspaceID, kind).Scan(&present) == nil
}

func validateCredentialIdentity(workspaceID, kind, bindingID string) error {
	if err := validateUUID("workspace_id", workspaceID); err != nil {
		return err
	}
	if !corecredentialsKindPattern.MatchString(kind) {
		return errors.New("provider kind is invalid")
	}
	if err := validateUUID("binding_id", bindingID); err != nil {
		return err
	}
	return nil
}

func validateWorkspaceCredentialScope(workspaceID string, actorID ...string) error {
	if err := validateUUID("workspace_id", workspaceID); err != nil {
		return err
	}
	if len(actorID) > 0 {
		if err := validateUUID("actor_id", actorID[0]); err != nil {
			return err
		}
	}
	return nil
}

func validateCreateWorkspaceCredentialBinding(command CreateWorkspaceCredentialBindingCommand) error {
	if err := validateCredentialIdentity(command.WorkspaceID, command.Kind, command.ID); err != nil {
		return err
	}
	if err := validateUUID("actor_id", command.ActorID); err != nil {
		return err
	}
	if strings.TrimSpace(command.DisplayName) != command.DisplayName || len(command.DisplayName) < 1 || len(command.DisplayName) > 256 {
		return errors.New("display name is invalid")
	}
	if command.OwnerScope != corecredentials.OwnerScopeWorkspace && command.OwnerScope != corecredentials.OwnerScopeUser {
		return errors.New("owner scope is invalid")
	}
	if command.OwnerScope == corecredentials.OwnerScopeUser {
		if err := validateUUID("owner_user_id", command.OwnerUserID); err != nil {
			return err
		}
	} else if command.OwnerUserID != "" {
		return errors.New("workspace-owned credential must not specify owner_user_id")
	}
	if len(command.SealedSecret) < 1 || len(command.SealedSecret) > 512*1024 || command.SealingKeyID == "" ||
		len(command.AuthType) < 1 || len(command.AuthType) > 128 {
		return errors.New("sealed credential, sealing key, and auth type are invalid")
	}
	if command.RefreshExpiresAt != nil && command.AccessExpiresAt != nil && command.AccessExpiresAt.After(*command.RefreshExpiresAt) {
		return errors.New("access expiry must not be after refresh expiry")
	}
	return nil
}

func validateRotateWorkspaceCredentialBinding(command RotateWorkspaceCredentialBindingCommand) error {
	if err := validateCredentialMutationScope(command.WorkspaceID, command.BindingID, command.ActorID, command.ExpectedAuthorityVersion); err != nil {
		return err
	}
	if command.ExpectedCredentialVersion < 1 {
		return errors.New("expected credential version must be positive")
	}
	if len(command.SealedSecret) < 1 || len(command.SealedSecret) > 512*1024 || command.SealingKeyID == "" ||
		len(command.AuthType) < 1 || len(command.AuthType) > 128 {
		return errors.New("rotated credential, sealing key, and auth type are invalid")
	}
	if command.RefreshExpiresAt != nil && command.AccessExpiresAt != nil && command.AccessExpiresAt.After(*command.RefreshExpiresAt) {
		return errors.New("access expiry must not be after refresh expiry")
	}
	return nil
}

func validateCredentialMutationScope(workspaceID, bindingID, actorID string, version int64) error {
	if err := validateUUID("workspace_id", workspaceID); err != nil {
		return err
	}
	if err := validateUUID("binding_id", bindingID); err != nil {
		return err
	}
	if err := validateUUID("actor_id", actorID); err != nil {
		return err
	}
	if version < 1 {
		return errors.New("expected authority version must be positive")
	}
	return nil
}

func validateUUIDFieldsForCredentialUse(request corecredentials.UseRequest) error {
	for name, value := range map[string]string{
		"workspace_id": request.WorkspaceID, "session_id": request.SessionID, "actor_id": request.ActorID,
		"environment_id": request.EnvironmentID, "run_id": request.RunID,
		"run_attempt_id": request.RunAttemptID, "execution_id": request.ExecutionID,
		"operation_id": request.OperationID, "sandbox_id": request.SandboxID, "binding_id": request.BindingID,
	} {
		if err := validateUUID(name, value); err != nil {
			return err
		}
	}
	return nil
}

func validateUUIDFieldsForCredentialAuthority(request corecredentials.AuthorityRequest) error {
	for name, value := range map[string]string{
		"workspace_id": request.WorkspaceID, "session_id": request.SessionID, "actor_id": request.ActorID,
		"environment_id": request.EnvironmentID, "run_id": request.RunID,
		"run_attempt_id": request.RunAttemptID, "execution_id": request.ExecutionID,
		"operation_id": request.OperationID, "sandbox_id": request.SandboxID,
	} {
		if err := validateUUID(name, value); err != nil {
			return err
		}
	}
	return nil
}

func normalizedJSON(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return []byte("{}")
	}
	return append([]byte(nil), raw...)
}

func isUniqueViolation(err error) bool {
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) && pgError.Code == "23505"
}
