package coredb

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/agentserver/agentserver/v2/internal/managedsandboxprofile"
	"github.com/jackc/pgx/v5"
)

type WorkspaceManagedSandboxSetting struct {
	WorkspaceID string
	Region      string
	Version     int64
	UpdatedBy   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type UpdateWorkspaceManagedSandboxSettingCommand struct {
	WorkspaceID     string
	ActorID         string
	Region          string
	ExpectedVersion int64
	AuditEventID    string
}

type UpdateWorkspaceManagedSandboxSettingResult struct {
	Setting WorkspaceManagedSandboxSetting
	Changed bool
}

func (s *StateStore) GetWorkspaceManagedSandboxSetting(
	ctx context.Context,
	workspaceID, actorID string,
) (WorkspaceManagedSandboxSetting, error) {
	const operation = "GetWorkspaceManagedSandboxSetting"
	if err := validatePlatformWorkspaceScope(workspaceID, actorID); err != nil {
		return WorkspaceManagedSandboxSetting{}, commandError(ErrorInvalidArgument, operation, "workspace", workspaceID, err.Error())
	}
	return withStateReadTransaction(ctx, s, operation, func(transaction pgx.Tx) (WorkspaceManagedSandboxSetting, error) {
		if _, err := s.requireActiveWorkspaceMember(ctx, transaction, operation, workspaceID, actorID, false); err != nil {
			return WorkspaceManagedSandboxSetting{}, err
		}
		return s.readWorkspaceManagedSandboxSetting(ctx, transaction, operation, workspaceID, false)
	})
}

func (s *StateStore) UpdateWorkspaceManagedSandboxSetting(
	ctx context.Context,
	command UpdateWorkspaceManagedSandboxSettingCommand,
) (UpdateWorkspaceManagedSandboxSettingResult, error) {
	const operation = "UpdateWorkspaceManagedSandboxSetting"
	if err := validatePlatformWorkspaceScope(command.WorkspaceID, command.ActorID); err != nil {
		return UpdateWorkspaceManagedSandboxSettingResult{}, commandError(ErrorInvalidArgument, operation, "workspace", command.WorkspaceID, err.Error())
	}
	if !managedsandboxprofile.ValidRegion(command.Region) {
		return UpdateWorkspaceManagedSandboxSettingResult{}, commandError(ErrorInvalidArgument, operation, "workspace", command.WorkspaceID, "managed sandbox region must be cn, boe, i18n-bd, or i18n-tt")
	}
	if command.ExpectedVersion < 1 {
		return UpdateWorkspaceManagedSandboxSettingResult{}, commandError(ErrorInvalidArgument, operation, "workspace", command.WorkspaceID, "expected_version must be positive")
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (UpdateWorkspaceManagedSandboxSettingResult, error) {
		if _, err := s.lockActiveWorkspaceOwner(ctx, transaction, operation, command.WorkspaceID, command.ActorID); err != nil {
			return UpdateWorkspaceManagedSandboxSettingResult{}, err
		}
		setting, err := s.readWorkspaceManagedSandboxSetting(ctx, transaction, operation, command.WorkspaceID, true)
		if err != nil {
			return UpdateWorkspaceManagedSandboxSettingResult{}, err
		}
		if setting.Version != command.ExpectedVersion {
			return UpdateWorkspaceManagedSandboxSettingResult{}, versionConflict(operation, "workspace_managed_sandbox_setting", command.WorkspaceID, setting.Version)
		}
		if setting.Region == command.Region {
			return UpdateWorkspaceManagedSandboxSettingResult{Setting: setting, Changed: false}, nil
		}
		if err := validateUUID("audit_event_id", command.AuditEventID); err != nil {
			return UpdateWorkspaceManagedSandboxSettingResult{}, commandError(ErrorInvalidArgument, operation, "workspace", command.WorkspaceID, err.Error())
		}
		update := fmt.Sprintf(`
UPDATE %s
SET region = $2, version = version + 1, updated_by = $3,
    updated_at = pg_catalog.clock_timestamp()
WHERE workspace_id = $1
RETURNING workspace_id::text, region, version, updated_by::text, created_at, updated_at`, s.table("workspace_managed_sandbox_settings"))
		var updated WorkspaceManagedSandboxSetting
		if err := transaction.QueryRow(ctx, update, command.WorkspaceID, command.Region, command.ActorID).Scan(
			&updated.WorkspaceID, &updated.Region, &updated.Version, &updated.UpdatedBy,
			&updated.CreatedAt, &updated.UpdatedAt,
		); err != nil {
			return UpdateWorkspaceManagedSandboxSettingResult{}, databaseError(operation+" update setting", err)
		}
		event := fmt.Sprintf(`
INSERT INTO %s
    (event_id, workspace_id, actor_id, previous_region, current_region, setting_version)
VALUES ($1, $2, $3, $4, $5, $6)`, s.table("workspace_managed_sandbox_setting_events"))
		if _, err := transaction.Exec(
			ctx, event, command.AuditEventID, command.WorkspaceID, command.ActorID,
			setting.Region, updated.Region, updated.Version,
		); err != nil {
			return UpdateWorkspaceManagedSandboxSettingResult{}, databaseError(operation+" insert audit event", err)
		}
		return UpdateWorkspaceManagedSandboxSettingResult{Setting: updated, Changed: true}, nil
	})
}

func (s *StateStore) readWorkspaceManagedSandboxSetting(
	ctx context.Context,
	transaction pgx.Tx,
	operation, workspaceID string,
	lock bool,
) (WorkspaceManagedSandboxSetting, error) {
	query := fmt.Sprintf(`
SELECT workspace_id::text, region, version, updated_by::text, created_at, updated_at
FROM %s
WHERE workspace_id = $1`, s.table("workspace_managed_sandbox_settings"))
	if lock {
		query += " FOR UPDATE"
	}
	var setting WorkspaceManagedSandboxSetting
	if err := transaction.QueryRow(ctx, query, workspaceID).Scan(
		&setting.WorkspaceID, &setting.Region, &setting.Version, &setting.UpdatedBy,
		&setting.CreatedAt, &setting.UpdatedAt,
	); errors.Is(err, pgx.ErrNoRows) {
		return WorkspaceManagedSandboxSetting{}, commandError(ErrorInvalidState, operation, "workspace", workspaceID, "workspace has no managed sandbox setting")
	} else if err != nil {
		return WorkspaceManagedSandboxSetting{}, databaseError(operation+" read managed sandbox setting", err)
	}
	if !managedsandboxprofile.ValidRegion(setting.Region) || setting.Version < 1 {
		return WorkspaceManagedSandboxSetting{}, databaseError(operation+" validate managed sandbox setting", errors.New("stored managed sandbox setting is invalid"))
	}
	return setting, nil
}
