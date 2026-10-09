package coredb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/agentserver/agentserver/v2/internal/workspacerepository"
	"github.com/jackc/pgx/v5"
)

type WorkspaceRepositorySetting struct {
	WorkspaceID string
	Source      *workspacerepository.Source
	Version     int64
	UpdatedBy   string
	UpdatedAt   *time.Time
}
type UpdateWorkspaceRepositoryCommand struct {
	WorkspaceID, ActorID, AuditEventID string
	Source                             *workspacerepository.Source
	ExpectedVersion                    int64
}
type UpdateWorkspaceRepositoryResult struct {
	Setting WorkspaceRepositorySetting
	Changed bool
}

func (s *StateStore) GetWorkspaceRepository(ctx context.Context, workspaceID, actorID string) (WorkspaceRepositorySetting, error) {
	const op = "GetWorkspaceRepository"
	if err := validatePlatformWorkspaceScope(workspaceID, actorID); err != nil {
		return WorkspaceRepositorySetting{}, commandError(ErrorInvalidArgument, op, "workspace", workspaceID, err.Error())
	}
	return withStateReadTransaction(ctx, s, op, func(tx pgx.Tx) (WorkspaceRepositorySetting, error) {
		if _, err := s.requireActiveWorkspaceMember(ctx, tx, op, workspaceID, actorID, false); err != nil {
			return WorkspaceRepositorySetting{}, err
		}
		return s.readWorkspaceRepository(ctx, tx, op, workspaceID, false)
	})
}

func (s *StateStore) UpdateWorkspaceRepository(ctx context.Context, c UpdateWorkspaceRepositoryCommand) (UpdateWorkspaceRepositoryResult, error) {
	const op = "UpdateWorkspaceRepository"
	bad := func(message string) (UpdateWorkspaceRepositoryResult, error) {
		return UpdateWorkspaceRepositoryResult{}, commandError(ErrorInvalidArgument, op, "workspace", c.WorkspaceID, message)
	}
	if err := validatePlatformWorkspaceScope(c.WorkspaceID, c.ActorID); err != nil {
		return bad(err.Error())
	}
	if c.ExpectedVersion < 0 || c.ExpectedVersion >= maxSafeJSONInteger {
		return bad("expected repository version is invalid")
	}
	var source *workspacerepository.Source
	if c.Source != nil {
		copy := *c.Source
		canonical, err := workspacerepository.NormalizeRepositoryURL(copy.URL)
		if err != nil {
			return bad(err.Error())
		}
		copy.URL = canonical
		if copy.WorkingDirectory == "" {
			copy.WorkingDirectory = "."
		}
		if err := copy.Validate(); err != nil {
			return bad(err.Error())
		}
		source = &copy
	}
	return withStateTransaction(ctx, s, op, func(tx pgx.Tx) (UpdateWorkspaceRepositoryResult, error) {
		if _, err := s.lockActiveWorkspaceOwner(ctx, tx, op, c.WorkspaceID, c.ActorID); err != nil {
			return UpdateWorkspaceRepositoryResult{}, err
		}
		before, err := s.readWorkspaceRepository(ctx, tx, op, c.WorkspaceID, true)
		if err != nil {
			return UpdateWorkspaceRepositoryResult{}, err
		}
		if before.Version != c.ExpectedVersion {
			return UpdateWorkspaceRepositoryResult{}, versionConflict(op, "workspace_repository", c.WorkspaceID, before.Version)
		}
		if source != nil && source.CredentialBindingID != "" {
			q := fmt.Sprintf(`SELECT id::text FROM %s WHERE workspace_id=$1 AND id=$2 AND kind='git' AND owner_scope='workspace' AND status='active' FOR SHARE`, s.table("workspace_credential_bindings"))
			var id string
			if err := tx.QueryRow(ctx, q, c.WorkspaceID, source.CredentialBindingID).Scan(&id); errors.Is(err, pgx.ErrNoRows) {
				return UpdateWorkspaceRepositoryResult{}, commandError(ErrorInvalidArgument, op, "credential", source.CredentialBindingID, "Git credential must be an active workspace-scoped binding in this workspace")
			} else if err != nil {
				return UpdateWorkspaceRepositoryResult{}, databaseError(op+" validate Git binding", err)
			}
		}
		if sourcesEqual(before.Source, source) {
			return UpdateWorkspaceRepositoryResult{Setting: before}, nil
		}
		if err := validateUUID("audit_event_id", c.AuditEventID); err != nil {
			return bad(err.Error())
		}
		var oldJSON, newJSON []byte
		if before.Source != nil {
			oldJSON, _ = json.Marshal(before.Source)
		}
		if source != nil {
			newJSON, _ = json.Marshal(source)
		}
		q := fmt.Sprintf(`INSERT INTO %s (workspace_id,source,version,updated_by) VALUES ($1,$2,$3,$4)
ON CONFLICT (workspace_id) DO UPDATE SET source=EXCLUDED.source,version=EXCLUDED.version,updated_by=EXCLUDED.updated_by,updated_at=pg_catalog.clock_timestamp()`, s.table("workspace_repository_settings"))
		if _, err := tx.Exec(ctx, q, c.WorkspaceID, newJSON, before.Version+1, c.ActorID); err != nil {
			return UpdateWorkspaceRepositoryResult{}, databaseError(op+" update setting", err)
		}
		audit := fmt.Sprintf(`INSERT INTO %s(event_id,workspace_id,actor_id,previous_source,current_source,setting_version) VALUES($1,$2,$3,$4,$5,$6)`, s.table("workspace_repository_setting_events"))
		if _, err := tx.Exec(ctx, audit, c.AuditEventID, c.WorkspaceID, c.ActorID, oldJSON, newJSON, before.Version+1); err != nil {
			return UpdateWorkspaceRepositoryResult{}, databaseError(op+" audit setting", err)
		}
		after, err := s.readWorkspaceRepository(ctx, tx, op, c.WorkspaceID, false)
		return UpdateWorkspaceRepositoryResult{Setting: after, Changed: err == nil}, err
	})
}

func sourcesEqual(a, b *workspacerepository.Source) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func (s *StateStore) readWorkspaceRepository(ctx context.Context, tx pgx.Tx, op, workspaceID string, lock bool) (WorkspaceRepositorySetting, error) {
	q := fmt.Sprintf(`SELECT source,version,updated_by::text,updated_at FROM %s WHERE workspace_id=$1`, s.table("workspace_repository_settings"))
	if lock {
		q += " FOR UPDATE"
	}
	result := WorkspaceRepositorySetting{WorkspaceID: workspaceID}
	var raw []byte
	var updated time.Time
	err := tx.QueryRow(ctx, q, workspaceID).Scan(&raw, &result.Version, &result.UpdatedBy, &updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return result, databaseError(op+" read repository", err)
	}
	if len(raw) > 0 && string(raw) != "null" {
		var source workspacerepository.Source
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&source); err != nil {
			return result, databaseError(op+" decode repository", err)
		}
		if err := source.Validate(); err != nil {
			return result, databaseError(op+" validate repository", err)
		}
		result.Source = &source
	}
	result.UpdatedAt = &updated
	return result, nil
}
