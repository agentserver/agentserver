package coredb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/managedcredential"
	"github.com/jackc/pgx/v5"
)

const (
	WorkspaceStatusActive    = "active"
	WorkspaceStatusSuspended = "suspended"
	WorkspaceStatusArchived  = "archived"

	WorkspaceRoleOwner     = "owner"
	WorkspaceRoleDeveloper = "developer"
	WorkspaceRoleViewer    = "viewer"
)

type PlatformWorkspace struct {
	ID                        string
	Name                      string
	Status                    string
	Role                      string
	ManagedLarkCredentialMode string
	Version                   int64
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
}

type CreatePlatformWorkspaceCommand struct {
	WorkspaceID               string
	ActorID                   string
	Name                      string
	ManagedLarkCredentialMode string
}

type CreatePlatformWorkspaceResult struct {
	Workspace PlatformWorkspace
	Created   bool
}

type UpdatePlatformWorkspaceCommand struct {
	WorkspaceID               string
	ActorID                   string
	Name                      string
	ManagedLarkCredentialMode string
	ExpectedVersion           int64
	AuditEventID              string
}

type UpdatePlatformWorkspaceResult struct {
	Workspace PlatformWorkspace
	Changed   bool
}

type ArchivePlatformWorkspaceCommand struct {
	WorkspaceID     string
	ActorID         string
	ExpectedVersion int64
}

type ArchivePlatformWorkspaceResult struct {
	Workspace PlatformWorkspace
	Changed   bool
}

type WorkspaceMember struct {
	UserID    string
	Role      string
	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

type AddWorkspaceMemberCommand struct {
	WorkspaceID string
	ActorID     string
	UserID      string
	Role        string
}

type AddWorkspaceMemberResult struct {
	Member  WorkspaceMember
	Created bool
}

type UpdateWorkspaceMemberCommand struct {
	WorkspaceID     string
	ActorID         string
	UserID          string
	Role            string
	ExpectedVersion int64
}

type UpdateWorkspaceMemberResult struct {
	Member  WorkspaceMember
	Changed bool
}

type RemoveWorkspaceMemberCommand struct {
	WorkspaceID string
	ActorID     string
	UserID      string
}

type RemoveWorkspaceMemberResult struct {
	UserID  string
	Removed bool
}

type ArchiveExecutorResourceCommand struct {
	WorkspaceID string
	ActorID     string
	ExecutorID  string
}

type ArchiveExecutorResourceResult struct {
	Executor ExecutorResource
	Changed  bool
}

func (s *StateStore) ListPlatformWorkspaces(ctx context.Context, actorID string) ([]PlatformWorkspace, error) {
	const operation = "ListPlatformWorkspaces"
	if err := validateUUID("actor_id", actorID); err != nil {
		return nil, commandError(ErrorInvalidArgument, operation, "user", actorID, err.Error())
	}
	return withStateReadTransaction(ctx, s, operation, func(transaction pgx.Tx) ([]PlatformWorkspace, error) {
		query := fmt.Sprintf(`
SELECT workspace.id::text, workspace.name, workspace.status, member.role,
       workspace.managed_lark_credential_mode,
       workspace.version, workspace.created_at, workspace.updated_at
FROM %s AS workspace
JOIN %s AS member
  ON member.workspace_id = workspace.id AND member.user_id = $1
JOIN %s AS local_user
  ON local_user.id = member.user_id AND local_user.status = 'active'
ORDER BY (workspace.status = 'active') DESC,
         pg_catalog.lower(workspace.name), workspace.id
LIMIT 256`, s.table("workspaces"), s.table("workspace_members"), s.table("users"))
		rows, err := transaction.Query(ctx, query, actorID)
		if err != nil {
			return nil, databaseError(operation+" query workspaces", err)
		}
		defer rows.Close()
		result := make([]PlatformWorkspace, 0)
		for rows.Next() {
			workspace, err := scanPlatformWorkspace(rows)
			if err != nil {
				return nil, databaseError(operation+" scan workspace", err)
			}
			result = append(result, workspace)
		}
		if err := rows.Err(); err != nil {
			return nil, databaseError(operation+" iterate workspaces", err)
		}
		return result, nil
	})
}

func (s *StateStore) GetPlatformWorkspace(ctx context.Context, workspaceID, actorID string) (PlatformWorkspace, error) {
	const operation = "GetPlatformWorkspace"
	if err := validatePlatformWorkspaceScope(workspaceID, actorID); err != nil {
		return PlatformWorkspace{}, commandError(ErrorInvalidArgument, operation, "workspace", workspaceID, err.Error())
	}
	return withStateReadTransaction(ctx, s, operation, func(transaction pgx.Tx) (PlatformWorkspace, error) {
		return s.readPlatformWorkspace(ctx, transaction, operation, workspaceID, actorID, false)
	})
}

func (s *StateStore) CreatePlatformWorkspace(ctx context.Context, command CreatePlatformWorkspaceCommand) (CreatePlatformWorkspaceResult, error) {
	const operation = "CreatePlatformWorkspace"
	if err := validatePlatformWorkspaceScope(command.WorkspaceID, command.ActorID); err != nil {
		return CreatePlatformWorkspaceResult{}, commandError(ErrorInvalidArgument, operation, "workspace", command.WorkspaceID, err.Error())
	}
	if err := validateWorkspaceName(command.Name); err != nil {
		return CreatePlatformWorkspaceResult{}, commandError(ErrorInvalidArgument, operation, "workspace", command.WorkspaceID, err.Error())
	}
	if !managedcredential.ValidMode(command.ManagedLarkCredentialMode) {
		return CreatePlatformWorkspaceResult{}, commandError(ErrorInvalidArgument, operation, "workspace", command.WorkspaceID, "managed_lark_credential_mode must be webhook_swap or process_env")
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (CreatePlatformWorkspaceResult, error) {
		userQuery := fmt.Sprintf("SELECT 1 FROM %s WHERE id = $1 AND status = 'active' FOR SHARE", s.table("users"))
		var present int
		if err := transaction.QueryRow(ctx, userQuery, command.ActorID).Scan(&present); errors.Is(err, pgx.ErrNoRows) {
			return CreatePlatformWorkspaceResult{}, commandError(ErrorForbidden, operation, "user", command.ActorID, "actor is not an active user")
		} else if err != nil {
			return CreatePlatformWorkspaceResult{}, databaseError(operation+" read actor", err)
		}
		insert := fmt.Sprintf(`
INSERT INTO %s (id, name, status, managed_lark_credential_mode)
VALUES ($1, $2, 'active', $3)
ON CONFLICT (id) DO NOTHING`, s.table("workspaces"))
		tag, err := transaction.Exec(ctx, insert, command.WorkspaceID, command.Name, command.ManagedLarkCredentialMode)
		if err != nil {
			return CreatePlatformWorkspaceResult{}, databaseError(operation+" insert workspace", err)
		}
		created := tag.RowsAffected() == 1
		if created {
			memberInsert := fmt.Sprintf(`
INSERT INTO %s (workspace_id, user_id, role)
VALUES ($1, $2, 'owner')`, s.table("workspace_members"))
			if _, err := transaction.Exec(ctx, memberInsert, command.WorkspaceID, command.ActorID); err != nil {
				return CreatePlatformWorkspaceResult{}, databaseError(operation+" insert owner membership", err)
			}
		}
		workspace, err := s.readPlatformWorkspace(ctx, transaction, operation, command.WorkspaceID, command.ActorID, true)
		if err != nil {
			return CreatePlatformWorkspaceResult{}, err
		}
		if workspace.Name != command.Name || workspace.Status != WorkspaceStatusActive || workspace.Role != WorkspaceRoleOwner ||
			workspace.ManagedLarkCredentialMode != command.ManagedLarkCredentialMode {
			return CreatePlatformWorkspaceResult{}, commandError(ErrorConflict, operation, "workspace", command.WorkspaceID, "workspace identity is already in use by different authority")
		}
		return CreatePlatformWorkspaceResult{Workspace: workspace, Created: created}, nil
	})
}

func (s *StateStore) UpdatePlatformWorkspace(ctx context.Context, command UpdatePlatformWorkspaceCommand) (UpdatePlatformWorkspaceResult, error) {
	const operation = "UpdatePlatformWorkspace"
	if err := validatePlatformWorkspaceScope(command.WorkspaceID, command.ActorID); err != nil {
		return UpdatePlatformWorkspaceResult{}, commandError(ErrorInvalidArgument, operation, "workspace", command.WorkspaceID, err.Error())
	}
	if err := validateWorkspaceName(command.Name); err != nil {
		return UpdatePlatformWorkspaceResult{}, commandError(ErrorInvalidArgument, operation, "workspace", command.WorkspaceID, err.Error())
	}
	if !managedcredential.ValidMode(command.ManagedLarkCredentialMode) {
		return UpdatePlatformWorkspaceResult{}, commandError(ErrorInvalidArgument, operation, "workspace", command.WorkspaceID, "managed_lark_credential_mode must be webhook_swap or process_env")
	}
	if command.ExpectedVersion < 1 {
		return UpdatePlatformWorkspaceResult{}, commandError(ErrorInvalidArgument, operation, "workspace", command.WorkspaceID, "expected_version must be positive")
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (UpdatePlatformWorkspaceResult, error) {
		workspace, err := s.lockActiveWorkspaceOwner(ctx, transaction, operation, command.WorkspaceID, command.ActorID)
		if err != nil {
			return UpdatePlatformWorkspaceResult{}, err
		}
		if workspace.Version != command.ExpectedVersion {
			return UpdatePlatformWorkspaceResult{}, versionConflict(operation, "workspace", command.WorkspaceID, workspace.Version)
		}
		if workspace.Name == command.Name && workspace.ManagedLarkCredentialMode == command.ManagedLarkCredentialMode {
			return UpdatePlatformWorkspaceResult{Workspace: workspace, Changed: false}, nil
		}
		modeChanged := workspace.ManagedLarkCredentialMode != command.ManagedLarkCredentialMode
		if modeChanged {
			if err := validateUUID("audit_event_id", command.AuditEventID); err != nil {
				return UpdatePlatformWorkspaceResult{}, commandError(ErrorInvalidArgument, operation, "workspace", command.WorkspaceID, err.Error())
			}
		}
		update := fmt.Sprintf(`
UPDATE %s
SET name = $2, managed_lark_credential_mode = $3,
    version = version + 1, updated_at = pg_catalog.clock_timestamp()
WHERE id = $1`, s.table("workspaces"))
		if _, err := transaction.Exec(ctx, update, command.WorkspaceID, command.Name, command.ManagedLarkCredentialMode); err != nil {
			return UpdatePlatformWorkspaceResult{}, databaseError(operation+" update workspace", err)
		}
		if modeChanged {
			audit := fmt.Sprintf(`
INSERT INTO %s (
    event_id, workspace_id, actor_id, previous_mode, current_mode, workspace_version
)
VALUES ($1, $2, $3, $4, $5, $6)`, s.table("workspace_managed_credential_mode_events"))
			if _, err := transaction.Exec(ctx, audit, command.AuditEventID, command.WorkspaceID, command.ActorID,
				workspace.ManagedLarkCredentialMode, command.ManagedLarkCredentialMode, workspace.Version+1); err != nil {
				return UpdatePlatformWorkspaceResult{}, databaseError(operation+" audit managed credential mode", err)
			}
		}
		workspace, err = s.readPlatformWorkspace(ctx, transaction, operation, command.WorkspaceID, command.ActorID, false)
		if err != nil {
			return UpdatePlatformWorkspaceResult{}, err
		}
		return UpdatePlatformWorkspaceResult{Workspace: workspace, Changed: true}, nil
	})
}

func (s *StateStore) ArchivePlatformWorkspace(ctx context.Context, command ArchivePlatformWorkspaceCommand) (ArchivePlatformWorkspaceResult, error) {
	const operation = "ArchivePlatformWorkspace"
	if err := validatePlatformWorkspaceScope(command.WorkspaceID, command.ActorID); err != nil {
		return ArchivePlatformWorkspaceResult{}, commandError(ErrorInvalidArgument, operation, "workspace", command.WorkspaceID, err.Error())
	}
	if command.ExpectedVersion < 1 {
		return ArchivePlatformWorkspaceResult{}, commandError(ErrorInvalidArgument, operation, "workspace", command.WorkspaceID, "expected_version must be positive")
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (ArchivePlatformWorkspaceResult, error) {
		workspace, err := s.readPlatformWorkspace(ctx, transaction, operation, command.WorkspaceID, command.ActorID, true)
		if err != nil {
			return ArchivePlatformWorkspaceResult{}, err
		}
		if workspace.Role != WorkspaceRoleOwner {
			return ArchivePlatformWorkspaceResult{}, commandError(ErrorForbidden, operation, "workspace", command.WorkspaceID, "only an owner may archive a workspace")
		}
		if workspace.Status == WorkspaceStatusArchived {
			return ArchivePlatformWorkspaceResult{Workspace: workspace, Changed: false}, nil
		}
		if workspace.Status != WorkspaceStatusActive {
			return ArchivePlatformWorkspaceResult{}, commandError(ErrorInvalidState, operation, "workspace", command.WorkspaceID, "only an active workspace can be archived")
		}
		if workspace.Version != command.ExpectedVersion {
			return ArchivePlatformWorkspaceResult{}, versionConflict(operation, "workspace", command.WorkspaceID, workspace.Version)
		}
		update := fmt.Sprintf(`
UPDATE %s
SET status = 'archived', version = version + 1, updated_at = pg_catalog.clock_timestamp()
WHERE id = $1`, s.table("workspaces"))
		if _, err := transaction.Exec(ctx, update, command.WorkspaceID); err != nil {
			return ArchivePlatformWorkspaceResult{}, databaseError(operation+" archive workspace", err)
		}
		workspace, err = s.readPlatformWorkspace(ctx, transaction, operation, command.WorkspaceID, command.ActorID, false)
		if err != nil {
			return ArchivePlatformWorkspaceResult{}, err
		}
		return ArchivePlatformWorkspaceResult{Workspace: workspace, Changed: true}, nil
	})
}

func (s *StateStore) ListWorkspaceMembers(ctx context.Context, workspaceID, actorID string) ([]WorkspaceMember, error) {
	const operation = "ListWorkspaceMembers"
	if err := validatePlatformWorkspaceScope(workspaceID, actorID); err != nil {
		return nil, commandError(ErrorInvalidArgument, operation, "workspace", workspaceID, err.Error())
	}
	return withStateReadTransaction(ctx, s, operation, func(transaction pgx.Tx) ([]WorkspaceMember, error) {
		if _, err := s.requireActiveWorkspaceMember(ctx, transaction, operation, workspaceID, actorID, false); err != nil {
			return nil, err
		}
		query := fmt.Sprintf(`
SELECT member.user_id::text, member.role, member.version,
       member.created_at, member.updated_at
FROM %s AS member
JOIN %s AS local_user
  ON local_user.id = member.user_id AND local_user.status = 'active'
WHERE member.workspace_id = $1
ORDER BY CASE member.role WHEN 'owner' THEN 0 WHEN 'developer' THEN 1 ELSE 2 END,
         member.user_id
LIMIT 256`, s.table("workspace_members"), s.table("users"))
		rows, err := transaction.Query(ctx, query, workspaceID)
		if err != nil {
			return nil, databaseError(operation+" query members", err)
		}
		defer rows.Close()
		members := make([]WorkspaceMember, 0)
		for rows.Next() {
			member, err := scanWorkspaceMember(rows)
			if err != nil {
				return nil, databaseError(operation+" scan member", err)
			}
			members = append(members, member)
		}
		if err := rows.Err(); err != nil {
			return nil, databaseError(operation+" iterate members", err)
		}
		return members, nil
	})
}

func (s *StateStore) AddWorkspaceMember(ctx context.Context, command AddWorkspaceMemberCommand) (AddWorkspaceMemberResult, error) {
	const operation = "AddWorkspaceMember"
	if err := validateMemberMutation(command.WorkspaceID, command.ActorID, command.UserID, command.Role); err != nil {
		return AddWorkspaceMemberResult{}, commandError(ErrorInvalidArgument, operation, "workspace_member", command.UserID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (AddWorkspaceMemberResult, error) {
		if _, err := s.lockActiveWorkspaceOwner(ctx, transaction, operation, command.WorkspaceID, command.ActorID); err != nil {
			return AddWorkspaceMemberResult{}, err
		}
		userQuery := fmt.Sprintf("SELECT 1 FROM %s WHERE id = $1 AND status = 'active' FOR SHARE", s.table("users"))
		var present int
		if err := transaction.QueryRow(ctx, userQuery, command.UserID).Scan(&present); errors.Is(err, pgx.ErrNoRows) {
			return AddWorkspaceMemberResult{}, commandError(ErrorNotFound, operation, "user", command.UserID, "target user is not active")
		} else if err != nil {
			return AddWorkspaceMemberResult{}, databaseError(operation+" read target user", err)
		}
		insert := fmt.Sprintf(`
INSERT INTO %s (workspace_id, user_id, role)
VALUES ($1, $2, $3)
ON CONFLICT (workspace_id, user_id) DO NOTHING`, s.table("workspace_members"))
		tag, err := transaction.Exec(ctx, insert, command.WorkspaceID, command.UserID, command.Role)
		if err != nil {
			return AddWorkspaceMemberResult{}, databaseError(operation+" insert member", err)
		}
		member, err := s.readWorkspaceMember(ctx, transaction, operation, command.WorkspaceID, command.UserID, true)
		if err != nil {
			return AddWorkspaceMemberResult{}, err
		}
		if member.Role != command.Role {
			return AddWorkspaceMemberResult{}, commandError(ErrorConflict, operation, "workspace_member", command.UserID, "member already exists with a different role")
		}
		return AddWorkspaceMemberResult{Member: member, Created: tag.RowsAffected() == 1}, nil
	})
}

func (s *StateStore) UpdateWorkspaceMember(ctx context.Context, command UpdateWorkspaceMemberCommand) (UpdateWorkspaceMemberResult, error) {
	const operation = "UpdateWorkspaceMember"
	if err := validateMemberMutation(command.WorkspaceID, command.ActorID, command.UserID, command.Role); err != nil {
		return UpdateWorkspaceMemberResult{}, commandError(ErrorInvalidArgument, operation, "workspace_member", command.UserID, err.Error())
	}
	if command.ExpectedVersion < 1 {
		return UpdateWorkspaceMemberResult{}, commandError(ErrorInvalidArgument, operation, "workspace_member", command.UserID, "expected_version must be positive")
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (UpdateWorkspaceMemberResult, error) {
		if _, err := s.lockActiveWorkspaceOwner(ctx, transaction, operation, command.WorkspaceID, command.ActorID); err != nil {
			return UpdateWorkspaceMemberResult{}, err
		}
		member, err := s.readWorkspaceMember(ctx, transaction, operation, command.WorkspaceID, command.UserID, true)
		if err != nil {
			return UpdateWorkspaceMemberResult{}, err
		}
		if member.Version != command.ExpectedVersion {
			return UpdateWorkspaceMemberResult{}, versionConflict(operation, "workspace_member", command.UserID, member.Version)
		}
		if member.Role == command.Role {
			return UpdateWorkspaceMemberResult{Member: member, Changed: false}, nil
		}
		if member.Role == WorkspaceRoleOwner && command.Role != WorkspaceRoleOwner {
			if err := s.requireAnotherWorkspaceOwner(ctx, transaction, operation, command.WorkspaceID, command.UserID); err != nil {
				return UpdateWorkspaceMemberResult{}, err
			}
		}
		update := fmt.Sprintf(`
UPDATE %s
SET role = $3, version = version + 1, updated_at = pg_catalog.clock_timestamp()
WHERE workspace_id = $1 AND user_id = $2`, s.table("workspace_members"))
		if _, err := transaction.Exec(ctx, update, command.WorkspaceID, command.UserID, command.Role); err != nil {
			return UpdateWorkspaceMemberResult{}, databaseError(operation+" update member", err)
		}
		member, err = s.readWorkspaceMember(ctx, transaction, operation, command.WorkspaceID, command.UserID, false)
		if err != nil {
			return UpdateWorkspaceMemberResult{}, err
		}
		return UpdateWorkspaceMemberResult{Member: member, Changed: true}, nil
	})
}

func (s *StateStore) RemoveWorkspaceMember(ctx context.Context, command RemoveWorkspaceMemberCommand) (RemoveWorkspaceMemberResult, error) {
	const operation = "RemoveWorkspaceMember"
	if err := validatePlatformWorkspaceScope(command.WorkspaceID, command.ActorID); err != nil {
		return RemoveWorkspaceMemberResult{}, commandError(ErrorInvalidArgument, operation, "workspace_member", command.UserID, err.Error())
	}
	if err := validateUUID("user_id", command.UserID); err != nil {
		return RemoveWorkspaceMemberResult{}, commandError(ErrorInvalidArgument, operation, "workspace_member", command.UserID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (RemoveWorkspaceMemberResult, error) {
		if _, err := s.lockActiveWorkspaceOwner(ctx, transaction, operation, command.WorkspaceID, command.ActorID); err != nil {
			return RemoveWorkspaceMemberResult{}, err
		}
		member, err := s.readWorkspaceMember(ctx, transaction, operation, command.WorkspaceID, command.UserID, true)
		if HasStateErrorCode(err, ErrorNotFound) {
			return RemoveWorkspaceMemberResult{UserID: command.UserID, Removed: false}, nil
		}
		if err != nil {
			return RemoveWorkspaceMemberResult{}, err
		}
		if member.Role == WorkspaceRoleOwner {
			if err := s.requireAnotherWorkspaceOwner(ctx, transaction, operation, command.WorkspaceID, command.UserID); err != nil {
				return RemoveWorkspaceMemberResult{}, err
			}
		}
		deleteMember := fmt.Sprintf("DELETE FROM %s WHERE workspace_id = $1 AND user_id = $2", s.table("workspace_members"))
		if _, err := transaction.Exec(ctx, deleteMember, command.WorkspaceID, command.UserID); err != nil {
			return RemoveWorkspaceMemberResult{}, databaseError(operation+" delete member", err)
		}
		revokeGrants := fmt.Sprintf(`
UPDATE %s
SET status = 'revoked', version = version + 1, updated_at = pg_catalog.clock_timestamp()
WHERE workspace_id = $1 AND user_id = $2 AND status <> 'revoked'`, s.table("workspace_llm_gateway_grants"))
		if _, err := transaction.Exec(ctx, revokeGrants, command.WorkspaceID, command.UserID); err != nil {
			return RemoveWorkspaceMemberResult{}, databaseError(operation+" revoke member gateway grants", err)
		}
		return RemoveWorkspaceMemberResult{UserID: command.UserID, Removed: true}, nil
	})
}

func (s *StateStore) ListExecutorResources(ctx context.Context, workspaceID, actorID string) ([]ExecutorResource, error) {
	const operation = "ListExecutorResources"
	if err := validatePlatformWorkspaceScope(workspaceID, actorID); err != nil {
		return nil, commandError(ErrorInvalidArgument, operation, "workspace", workspaceID, err.Error())
	}
	return withStateReadTransaction(ctx, s, operation, func(transaction pgx.Tx) ([]ExecutorResource, error) {
		if _, err := s.requireActiveWorkspaceMember(ctx, transaction, operation, workspaceID, actorID, false); err != nil {
			return nil, err
		}
		query := fmt.Sprintf(`
SELECT id::text, workspace_id::text, status, version, created_at, updated_at
FROM %s
WHERE workspace_id = $1
ORDER BY (status <> 'revoked') DESC, created_at, id
LIMIT 256`, s.table("executors"))
		rows, err := transaction.Query(ctx, query, workspaceID)
		if err != nil {
			return nil, databaseError(operation+" query executors", err)
		}
		defer rows.Close()
		result := make([]ExecutorResource, 0)
		for rows.Next() {
			var executor ExecutorResource
			if err := rows.Scan(&executor.ID, &executor.WorkspaceID, &executor.Status, &executor.Version, &executor.CreatedAt, &executor.UpdatedAt); err != nil {
				return nil, databaseError(operation+" scan executor", err)
			}
			result = append(result, executor)
		}
		if err := rows.Err(); err != nil {
			return nil, databaseError(operation+" iterate executors", err)
		}
		return result, nil
	})
}

func (s *StateStore) ArchiveExecutorResource(ctx context.Context, command ArchiveExecutorResourceCommand) (ArchiveExecutorResourceResult, error) {
	const operation = "ArchiveExecutorResource"
	if err := validatePlatformWorkspaceScope(command.WorkspaceID, command.ActorID); err != nil {
		return ArchiveExecutorResourceResult{}, commandError(ErrorInvalidArgument, operation, "executor", command.ExecutorID, err.Error())
	}
	if err := validateUUID("executor_id", command.ExecutorID); err != nil {
		return ArchiveExecutorResourceResult{}, commandError(ErrorInvalidArgument, operation, "executor", command.ExecutorID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (ArchiveExecutorResourceResult, error) {
		if _, err := s.lockActiveWorkspaceOwner(ctx, transaction, operation, command.WorkspaceID, command.ActorID); err != nil {
			return ArchiveExecutorResourceResult{}, err
		}
		executor, err := s.readExecutorResource(ctx, transaction, operation, command.ExecutorID, true)
		if err != nil {
			return ArchiveExecutorResourceResult{}, err
		}
		if executor.WorkspaceID != command.WorkspaceID {
			return ArchiveExecutorResourceResult{}, commandError(ErrorNotFound, operation, "executor", command.ExecutorID, "executor does not belong to the workspace")
		}
		if executor.Status == ExecutorStatusRevoked {
			return ArchiveExecutorResourceResult{Executor: executor, Changed: false}, nil
		}
		endAttempts := fmt.Sprintf(`
UPDATE %s AS attempt
SET ended_at = COALESCE(attempt.ended_at, pg_catalog.clock_timestamp()),
    end_reason = COALESCE(attempt.end_reason, 'revoked')
FROM %s AS connection
WHERE connection.executor_id = $1
  AND attempt.connection_id = connection.connection_id
  AND attempt.ended_at IS NULL`, s.table("executor_connection_attempts"), s.table("executor_connections"))
		if _, err := transaction.Exec(ctx, endAttempts, command.ExecutorID); err != nil {
			return ArchiveExecutorResourceResult{}, databaseError(operation+" end connection attempt", err)
		}
		fenceConnection := fmt.Sprintf(`
UPDATE %s
SET status = 'fenced', expires_at = pg_catalog.clock_timestamp(), version = version + 1
WHERE executor_id = $1 AND status <> 'fenced'`, s.table("executor_connections"))
		if _, err := transaction.Exec(ctx, fenceConnection, command.ExecutorID); err != nil {
			return ArchiveExecutorResourceResult{}, databaseError(operation+" fence connection", err)
		}
		disableEnvironments := fmt.Sprintf(`
UPDATE %s
SET status = 'disabled', version = version + 1, updated_at = pg_catalog.clock_timestamp()
WHERE executor_id = $1 AND status <> 'disabled'`, s.table("executor_environments"))
		if _, err := transaction.Exec(ctx, disableEnvironments, command.ExecutorID); err != nil {
			return ArchiveExecutorResourceResult{}, databaseError(operation+" disable environments", err)
		}
		revokeTokens := fmt.Sprintf(`
UPDATE %s
SET revoked_at = pg_catalog.clock_timestamp(), version = version + 1
WHERE executor_id = $1 AND consumed_at IS NULL AND revoked_at IS NULL`, s.table("executor_enrollment_tokens"))
		if _, err := transaction.Exec(ctx, revokeTokens, command.ExecutorID); err != nil {
			return ArchiveExecutorResourceResult{}, databaseError(operation+" revoke enrollment tokens", err)
		}
		archive := fmt.Sprintf(`
UPDATE %s
SET status = 'revoked', version = version + 1, updated_at = pg_catalog.clock_timestamp()
WHERE id = $1`, s.table("executors"))
		if _, err := transaction.Exec(ctx, archive, command.ExecutorID); err != nil {
			return ArchiveExecutorResourceResult{}, databaseError(operation+" revoke executor", err)
		}
		executor, err = s.readExecutorResource(ctx, transaction, operation, command.ExecutorID, false)
		if err != nil {
			return ArchiveExecutorResourceResult{}, err
		}
		return ArchiveExecutorResourceResult{Executor: executor, Changed: true}, nil
	})
}

func (s *StateStore) readPlatformWorkspace(ctx context.Context, transaction pgx.Tx, operation, workspaceID, actorID string, lock bool) (PlatformWorkspace, error) {
	query := fmt.Sprintf(`
SELECT workspace.id::text, workspace.name, workspace.status, member.role,
       workspace.managed_lark_credential_mode,
       workspace.version, workspace.created_at, workspace.updated_at
FROM %s AS workspace
JOIN %s AS member
  ON member.workspace_id = workspace.id AND member.user_id = $2
JOIN %s AS local_user
  ON local_user.id = member.user_id AND local_user.status = 'active'
WHERE workspace.id = $1`, s.table("workspaces"), s.table("workspace_members"), s.table("users"))
	if lock {
		query += " FOR UPDATE OF workspace, member"
	}
	workspace, err := scanPlatformWorkspace(transaction.QueryRow(ctx, query, workspaceID, actorID))
	if errors.Is(err, pgx.ErrNoRows) {
		return PlatformWorkspace{}, commandError(ErrorNotFound, operation, "workspace", workspaceID, "workspace is unavailable to the current user")
	}
	if err != nil {
		return PlatformWorkspace{}, databaseError(operation+" read workspace", err)
	}
	return workspace, nil
}

func (s *StateStore) lockActiveWorkspaceOwner(ctx context.Context, transaction pgx.Tx, operation, workspaceID, actorID string) (PlatformWorkspace, error) {
	workspace, err := s.readPlatformWorkspace(ctx, transaction, operation, workspaceID, actorID, true)
	if err != nil {
		return PlatformWorkspace{}, err
	}
	if workspace.Status != WorkspaceStatusActive {
		return PlatformWorkspace{}, commandError(ErrorInvalidState, operation, "workspace", workspaceID, "workspace is not active")
	}
	if workspace.Role != WorkspaceRoleOwner {
		return PlatformWorkspace{}, commandError(ErrorForbidden, operation, "workspace", workspaceID, "only a workspace owner may perform this action")
	}
	return workspace, nil
}

func (s *StateStore) requireActiveWorkspaceMember(ctx context.Context, transaction pgx.Tx, operation, workspaceID, actorID string, lock bool) (string, error) {
	workspace, err := s.readPlatformWorkspace(ctx, transaction, operation, workspaceID, actorID, lock)
	if err != nil {
		return "", err
	}
	if workspace.Status != WorkspaceStatusActive {
		return "", commandError(ErrorInvalidState, operation, "workspace", workspaceID, "workspace is not active")
	}
	return workspace.Role, nil
}

func (s *StateStore) readWorkspaceMember(ctx context.Context, transaction pgx.Tx, operation, workspaceID, userID string, lock bool) (WorkspaceMember, error) {
	query := fmt.Sprintf(`
SELECT user_id::text, role, version, created_at, updated_at
FROM %s
WHERE workspace_id = $1 AND user_id = $2`, s.table("workspace_members"))
	if lock {
		query += " FOR UPDATE"
	}
	member, err := scanWorkspaceMember(transaction.QueryRow(ctx, query, workspaceID, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return WorkspaceMember{}, commandError(ErrorNotFound, operation, "workspace_member", userID, "workspace member does not exist")
	}
	if err != nil {
		return WorkspaceMember{}, databaseError(operation+" read member", err)
	}
	return member, nil
}

func (s *StateStore) requireAnotherWorkspaceOwner(ctx context.Context, transaction pgx.Tx, operation, workspaceID, excludedUserID string) error {
	query := fmt.Sprintf(`
SELECT 1
FROM %s
WHERE workspace_id = $1 AND role = 'owner' AND user_id <> $2
LIMIT 1`, s.table("workspace_members"))
	var present int
	if err := transaction.QueryRow(ctx, query, workspaceID, excludedUserID).Scan(&present); errors.Is(err, pgx.ErrNoRows) {
		return commandError(ErrorConflict, operation, "workspace", workspaceID, "workspace must retain at least one owner")
	} else if err != nil {
		return databaseError(operation+" verify remaining owner", err)
	}
	return nil
}

type platformRowScanner interface {
	Scan(...any) error
}

func scanPlatformWorkspace(row platformRowScanner) (PlatformWorkspace, error) {
	var workspace PlatformWorkspace
	err := row.Scan(&workspace.ID, &workspace.Name, &workspace.Status, &workspace.Role, &workspace.ManagedLarkCredentialMode,
		&workspace.Version, &workspace.CreatedAt, &workspace.UpdatedAt)
	return workspace, err
}

func scanWorkspaceMember(row platformRowScanner) (WorkspaceMember, error) {
	var member WorkspaceMember
	err := row.Scan(&member.UserID, &member.Role, &member.Version, &member.CreatedAt, &member.UpdatedAt)
	return member, err
}

func validatePlatformWorkspaceScope(workspaceID, actorID string) error {
	if err := validateUUID("workspace_id", workspaceID); err != nil {
		return err
	}
	return validateUUID("actor_id", actorID)
}

func validateWorkspaceName(name string) error {
	if name == "" || len(name) > 256 || !utf8.ValidString(name) || strings.TrimSpace(name) != name {
		return errors.New("name must contain between 1 and 256 canonical UTF-8 bytes")
	}
	for _, value := range name {
		if unicode.IsControl(value) {
			return errors.New("name must not contain control characters")
		}
	}
	return nil
}

func validateMemberMutation(workspaceID, actorID, userID, role string) error {
	if err := validatePlatformWorkspaceScope(workspaceID, actorID); err != nil {
		return err
	}
	if err := validateUUID("user_id", userID); err != nil {
		return err
	}
	if !validWorkspaceRole(role) {
		return errors.New("role must be owner, developer, or viewer")
	}
	return nil
}

func validWorkspaceRole(role string) bool {
	return role == WorkspaceRoleOwner || role == WorkspaceRoleDeveloper || role == WorkspaceRoleViewer
}
