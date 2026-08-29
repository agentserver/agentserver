package coredb

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/runmanifest"
	"github.com/agentserver/agentserver/v2/internal/workspaceauthority"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	UserSessionStatusActive   = "active"
	UserSessionStatusArchived = "archived"
)

type UserSession struct {
	ID                      string
	WorkspaceID             string
	CreatorID               string
	Title                   string
	Status                  string
	ActiveRunID             string
	Version                 int64
	PermissionMode          runmanifest.CodexPermissionMode
	PermissionModeVersion   int64
	WorkingEnvironmentID    string
	WorkingDirectory        string
	WorkingDirectoryVersion int64
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

type CreateUserSessionCommand struct {
	WorkspaceID string
	SessionID   string
	ActorID     string
	Title       string
}

type CreateUserSessionResult struct {
	Session UserSession
	Created bool
}

type UpdateUserSessionCommand struct {
	WorkspaceID     string
	SessionID       string
	ActorID         string
	Title           string
	ExpectedVersion int64
}

type UpdateUserSessionResult struct {
	Session UserSession
	Changed bool
}

type UpdateUserSessionPermissionModeCommand struct {
	WorkspaceID                   string
	SessionID                     string
	ActorID                       string
	PermissionMode                runmanifest.CodexPermissionMode
	ExpectedPermissionModeVersion int64
}

type UpdateUserSessionPermissionModeResult struct {
	Session UserSession
	Changed bool
}

// UpdateUserSessionWorkingDirectoryCommand changes only the next-run
// executor workspace binding. Its independent CAS version lets a UI switch
// directories while an unrelated run/session version is changing.
type UpdateUserSessionWorkingDirectoryCommand struct {
	WorkspaceID                     string
	SessionID                       string
	ActorID                         string
	EnvironmentID                   string
	WorkingDirectory                string
	ExpectedWorkingDirectoryVersion int64
}

type UpdateUserSessionWorkingDirectoryResult struct {
	Session UserSession
	Changed bool
}

type ArchiveUserSessionCommand struct {
	WorkspaceID     string
	SessionID       string
	ActorID         string
	ExpectedVersion int64
}

type ArchiveUserSessionResult struct {
	Session UserSession
	Changed bool
}

func (s *StateStore) ListUserSessions(ctx context.Context, workspaceID, actorID string) ([]UserSession, error) {
	const operation = "ListUserSessions"
	if err := validatePlatformWorkspaceScope(workspaceID, actorID); err != nil {
		return nil, commandError(ErrorInvalidArgument, operation, "workspace", workspaceID, err.Error())
	}
	return withStateReadTransaction(ctx, s, operation, func(transaction pgx.Tx) ([]UserSession, error) {
		if _, err := s.requireActiveWorkspaceMember(ctx, transaction, operation, workspaceID, actorID, false); err != nil {
			return nil, err
		}
		query := fmt.Sprintf(`
SELECT session.id::text, session.workspace_id::text, session.creator_id::text,
       session.title, session.status, session.active_run_id::text,
       session.version, session.permission_mode, session.permission_mode_version,
       session.working_environment_id::text, session.working_directory,
       session.working_directory_version,
       session.created_at, session.updated_at
FROM %s AS session
WHERE session.workspace_id = $1
  AND session.creator_id = $2
  AND session.status = 'active'
ORDER BY session.updated_at DESC, session.id
LIMIT 256`, s.table("sessions"))
		rows, err := transaction.Query(ctx, query, workspaceID, actorID)
		if err != nil {
			return nil, databaseError(operation+" query sessions", err)
		}
		defer rows.Close()
		result := make([]UserSession, 0)
		for rows.Next() {
			session, err := scanUserSession(rows)
			if err != nil {
				return nil, databaseError(operation+" scan session", err)
			}
			result = append(result, session)
		}
		if err := rows.Err(); err != nil {
			return nil, databaseError(operation+" iterate sessions", err)
		}
		return result, nil
	})
}

func (s *StateStore) GetUserSession(ctx context.Context, workspaceID, sessionID, actorID string) (UserSession, error) {
	const operation = "GetUserSession"
	if err := validateUserSessionScope(workspaceID, sessionID, actorID); err != nil {
		return UserSession{}, commandError(ErrorInvalidArgument, operation, "session", sessionID, err.Error())
	}
	return withStateReadTransaction(ctx, s, operation, func(transaction pgx.Tx) (UserSession, error) {
		return s.readUserSession(ctx, transaction, operation, workspaceID, sessionID, actorID, false)
	})
}

func (s *StateStore) CreateUserSession(ctx context.Context, command CreateUserSessionCommand) (CreateUserSessionResult, error) {
	const operation = "CreateUserSession"
	if err := validateUserSessionScope(command.WorkspaceID, command.SessionID, command.ActorID); err != nil {
		return CreateUserSessionResult{}, commandError(ErrorInvalidArgument, operation, "session", command.SessionID, err.Error())
	}
	if err := validateUserSessionTitle(command.Title); err != nil {
		return CreateUserSessionResult{}, commandError(ErrorInvalidArgument, operation, "session", command.SessionID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (CreateUserSessionResult, error) {
		role, err := s.requireActiveWorkspaceMember(ctx, transaction, operation, command.WorkspaceID, command.ActorID, true)
		if err != nil {
			return CreateUserSessionResult{}, err
		}
		if role == WorkspaceRoleViewer {
			return CreateUserSessionResult{}, commandError(ErrorForbidden, operation, "workspace", command.WorkspaceID, "workspace role cannot create sessions")
		}
		insert := fmt.Sprintf(`
INSERT INTO %s (id, workspace_id, creator_id, title, status)
VALUES ($1, $2, $3, $4, 'active')
ON CONFLICT (id) DO NOTHING`, s.table("sessions"))
		tag, err := transaction.Exec(ctx, insert, command.SessionID, command.WorkspaceID, command.ActorID, command.Title)
		if err != nil {
			var postgresError *pgconn.PgError
			if pgxErrorAs(err, &postgresError) && postgresError.Code == "23505" {
				return CreateUserSessionResult{}, commandError(ErrorConflict, operation, "session", command.SessionID, "session identity is already in use")
			}
			return CreateUserSessionResult{}, databaseError(operation+" insert session", err)
		}
		created := tag.RowsAffected() == 1
		session, err := s.readUserSession(ctx, transaction, operation, command.WorkspaceID, command.SessionID, command.ActorID, true)
		if err != nil {
			if !created && HasStateErrorCode(err, ErrorNotFound) {
				return CreateUserSessionResult{}, commandError(ErrorConflict, operation, "session", command.SessionID, "session identity is already in use")
			}
			return CreateUserSessionResult{}, err
		}
		if session.Title != command.Title || session.Status != UserSessionStatusActive {
			return CreateUserSessionResult{}, commandError(ErrorConflict, operation, "session", command.SessionID, "session identity is already in use with different state")
		}
		return CreateUserSessionResult{Session: session, Created: created}, nil
	})
}

// UpdateUserSessionPermissionMode changes only the next-turn preference. It
// deliberately leaves sessions.version untouched so an unrelated title/run
// CAS token cannot make a mode switch fail (or vice versa).
func (s *StateStore) UpdateUserSessionPermissionMode(ctx context.Context, command UpdateUserSessionPermissionModeCommand) (UpdateUserSessionPermissionModeResult, error) {
	const operation = "UpdateUserSessionPermissionMode"
	if err := validateUserSessionScope(command.WorkspaceID, command.SessionID, command.ActorID); err != nil {
		return UpdateUserSessionPermissionModeResult{}, commandError(ErrorInvalidArgument, operation, "session", command.SessionID, err.Error())
	}
	mode, err := command.PermissionMode.Effective()
	if err != nil || command.PermissionMode == "" {
		if err == nil {
			err = errors.New("permission_mode must be explicit")
		}
		return UpdateUserSessionPermissionModeResult{}, commandError(ErrorInvalidArgument, operation, "session", command.SessionID, err.Error())
	}
	if command.ExpectedPermissionModeVersion < 1 || command.ExpectedPermissionModeVersion > maxSafeJSONInteger {
		return UpdateUserSessionPermissionModeResult{}, commandError(ErrorInvalidArgument, operation, "session", command.SessionID, "expected_permission_mode_version must be a positive JSON-safe integer")
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (UpdateUserSessionPermissionModeResult, error) {
		session, err := s.readUserSession(ctx, transaction, operation, command.WorkspaceID, command.SessionID, command.ActorID, true)
		if err != nil {
			return UpdateUserSessionPermissionModeResult{}, err
		}
		role, err := s.readWorkspaceMemberRole(ctx, transaction, command.WorkspaceID, command.ActorID)
		if err != nil {
			return UpdateUserSessionPermissionModeResult{}, err
		}
		if role == WorkspaceRoleViewer {
			return UpdateUserSessionPermissionModeResult{}, commandError(ErrorForbidden, operation, "workspace", command.WorkspaceID, "workspace role cannot change session permission mode")
		}
		if session.Status != UserSessionStatusActive {
			return UpdateUserSessionPermissionModeResult{}, commandError(ErrorInvalidState, operation, "session", command.SessionID, "only an active session can change permission mode")
		}
		if session.PermissionModeVersion != command.ExpectedPermissionModeVersion {
			return UpdateUserSessionPermissionModeResult{}, versionConflict(operation, "session_permission_mode", command.SessionID, session.PermissionModeVersion)
		}
		if session.PermissionModeVersion >= maxSafeJSONInteger {
			return UpdateUserSessionPermissionModeResult{}, commandError(ErrorInvalidState, operation, "session_permission_mode", command.SessionID, "permission mode version is exhausted")
		}
		if session.PermissionMode == mode {
			return UpdateUserSessionPermissionModeResult{Session: session, Changed: false}, nil
		}
		update := fmt.Sprintf(`
UPDATE %s
SET permission_mode = $2,
    permission_mode_version = permission_mode_version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $1`, s.table("sessions"))
		if _, err := transaction.Exec(ctx, update, command.SessionID, string(mode)); err != nil {
			return UpdateUserSessionPermissionModeResult{}, databaseError(operation+" update session permission mode", err)
		}
		session, err = s.readUserSession(ctx, transaction, operation, command.WorkspaceID, command.SessionID, command.ActorID, false)
		return UpdateUserSessionPermissionModeResult{Session: session, Changed: true}, err
	})
}

// UpdateUserSessionWorkingDirectory changes the logical path used by future
// runs. The path is always relative to an enrolled executor environment root;
// absolute host paths are intentionally impossible to persist.
func (s *StateStore) UpdateUserSessionWorkingDirectory(ctx context.Context, command UpdateUserSessionWorkingDirectoryCommand) (UpdateUserSessionWorkingDirectoryResult, error) {
	const operation = "UpdateUserSessionWorkingDirectory"
	if err := validateUserSessionScope(command.WorkspaceID, command.SessionID, command.ActorID); err != nil {
		return UpdateUserSessionWorkingDirectoryResult{}, commandError(ErrorInvalidArgument, operation, "session", command.SessionID, err.Error())
	}
	if command.EnvironmentID != "" {
		if err := validateUUID("environment_id", command.EnvironmentID); err != nil {
			return UpdateUserSessionWorkingDirectoryResult{}, commandError(ErrorInvalidArgument, operation, "environment", command.EnvironmentID, err.Error())
		}
	} else if command.WorkingDirectory != "." {
		return UpdateUserSessionWorkingDirectoryResult{}, commandError(ErrorInvalidArgument, operation, "working_directory", command.WorkingDirectory, "an unbound session can use only the root directory '.'")
	}
	if err := workspaceauthority.ValidateWorkingDirectory(command.WorkingDirectory); err != nil {
		return UpdateUserSessionWorkingDirectoryResult{}, commandError(ErrorInvalidArgument, operation, "working_directory", command.WorkingDirectory, err.Error())
	}
	if command.ExpectedWorkingDirectoryVersion < 1 || command.ExpectedWorkingDirectoryVersion > maxSafeJSONInteger {
		return UpdateUserSessionWorkingDirectoryResult{}, commandError(ErrorInvalidArgument, operation, "session_working_directory", command.SessionID, "expected_working_directory_version must be a positive JSON-safe integer")
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (UpdateUserSessionWorkingDirectoryResult, error) {
		session, err := s.readUserSession(ctx, transaction, operation, command.WorkspaceID, command.SessionID, command.ActorID, true)
		if err != nil {
			return UpdateUserSessionWorkingDirectoryResult{}, err
		}
		role, err := s.readWorkspaceMemberRole(ctx, transaction, command.WorkspaceID, command.ActorID)
		if err != nil {
			return UpdateUserSessionWorkingDirectoryResult{}, err
		}
		if role == WorkspaceRoleViewer {
			return UpdateUserSessionWorkingDirectoryResult{}, commandError(ErrorForbidden, operation, "workspace", command.WorkspaceID, "workspace role cannot change session working directory")
		}
		if session.Status != UserSessionStatusActive {
			return UpdateUserSessionWorkingDirectoryResult{}, commandError(ErrorInvalidState, operation, "session", command.SessionID, "only an active session can change working directory")
		}
		if session.WorkingDirectoryVersion != command.ExpectedWorkingDirectoryVersion {
			return UpdateUserSessionWorkingDirectoryResult{}, versionConflict(operation, "session_working_directory", command.SessionID, session.WorkingDirectoryVersion)
		}
		if session.WorkingDirectoryVersion >= maxSafeJSONInteger {
			return UpdateUserSessionWorkingDirectoryResult{}, commandError(ErrorInvalidState, operation, "session_working_directory", command.SessionID, "working-directory version is exhausted")
		}

		if command.EnvironmentID != "" {
			// The shared helper keeps the environment lookup in the same
			// transaction and follows the executor -> environment lock order used
			// by connection acquire/revoke paths.
			environment, err := s.readWorkspaceBindingEnvironment(ctx, transaction, operation, command.WorkspaceID, command.EnvironmentID)
			if err != nil {
				return UpdateUserSessionWorkingDirectoryResult{}, err
			}
			if environment.Status == ExecutorEnvironmentStatusDisabled || environment.ExecutorStatus == ExecutorStatusRevoked {
				return UpdateUserSessionWorkingDirectoryResult{}, commandError(ErrorInvalidState, operation, "environment", command.EnvironmentID, "environment is disabled or its executor is revoked")
			}
			if environment.Version < 1 || environment.Version > maxSafeJSONInteger {
				return UpdateUserSessionWorkingDirectoryResult{}, databaseError(operation+" validate environment", errors.New("registered environment version is invalid"))
			}
		}

		if session.WorkingEnvironmentID == command.EnvironmentID && session.WorkingDirectory == command.WorkingDirectory {
			return UpdateUserSessionWorkingDirectoryResult{Session: session, Changed: false}, nil
		}
		var environment any
		if command.EnvironmentID != "" {
			environment = command.EnvironmentID
		}
		update := fmt.Sprintf(`
UPDATE %s
SET working_environment_id = $2::uuid,
    working_directory = $3,
    working_directory_version = working_directory_version + 1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $1`, s.table("sessions"))
		if _, err := transaction.Exec(ctx, update, command.SessionID, environment, command.WorkingDirectory); err != nil {
			return UpdateUserSessionWorkingDirectoryResult{}, databaseError(operation+" update session working directory", err)
		}
		session, err = s.readUserSession(ctx, transaction, operation, command.WorkspaceID, command.SessionID, command.ActorID, false)
		return UpdateUserSessionWorkingDirectoryResult{Session: session, Changed: true}, err
	})
}

func (s *StateStore) UpdateUserSession(ctx context.Context, command UpdateUserSessionCommand) (UpdateUserSessionResult, error) {
	const operation = "UpdateUserSession"
	if err := validateUserSessionScope(command.WorkspaceID, command.SessionID, command.ActorID); err != nil {
		return UpdateUserSessionResult{}, commandError(ErrorInvalidArgument, operation, "session", command.SessionID, err.Error())
	}
	if err := validateUserSessionTitle(command.Title); err != nil {
		return UpdateUserSessionResult{}, commandError(ErrorInvalidArgument, operation, "session", command.SessionID, err.Error())
	}
	if command.ExpectedVersion < 1 {
		return UpdateUserSessionResult{}, commandError(ErrorInvalidArgument, operation, "session", command.SessionID, "expected_version must be positive")
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (UpdateUserSessionResult, error) {
		session, err := s.readUserSession(ctx, transaction, operation, command.WorkspaceID, command.SessionID, command.ActorID, true)
		if err != nil {
			return UpdateUserSessionResult{}, err
		}
		if session.Status != UserSessionStatusActive {
			return UpdateUserSessionResult{}, commandError(ErrorInvalidState, operation, "session", command.SessionID, "only an active session can be renamed")
		}
		if session.Version != command.ExpectedVersion {
			return UpdateUserSessionResult{}, versionConflict(operation, "session", command.SessionID, session.Version)
		}
		if session.Title == command.Title {
			return UpdateUserSessionResult{Session: session, Changed: false}, nil
		}
		update := fmt.Sprintf(`
UPDATE %s
SET title = $2, version = version + 1, updated_at = pg_catalog.clock_timestamp()
WHERE id = $1`, s.table("sessions"))
		if _, err := transaction.Exec(ctx, update, command.SessionID, command.Title); err != nil {
			return UpdateUserSessionResult{}, databaseError(operation+" update session", err)
		}
		session, err = s.readUserSession(ctx, transaction, operation, command.WorkspaceID, command.SessionID, command.ActorID, false)
		return UpdateUserSessionResult{Session: session, Changed: true}, err
	})
}

func (s *StateStore) ArchiveUserSession(ctx context.Context, command ArchiveUserSessionCommand) (ArchiveUserSessionResult, error) {
	const operation = "ArchiveUserSession"
	if err := validateUserSessionScope(command.WorkspaceID, command.SessionID, command.ActorID); err != nil {
		return ArchiveUserSessionResult{}, commandError(ErrorInvalidArgument, operation, "session", command.SessionID, err.Error())
	}
	if command.ExpectedVersion < 1 {
		return ArchiveUserSessionResult{}, commandError(ErrorInvalidArgument, operation, "session", command.SessionID, "expected_version must be positive")
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (ArchiveUserSessionResult, error) {
		session, err := s.readUserSession(ctx, transaction, operation, command.WorkspaceID, command.SessionID, command.ActorID, true)
		if err != nil {
			return ArchiveUserSessionResult{}, err
		}
		if session.Status == UserSessionStatusArchived {
			return ArchiveUserSessionResult{Session: session, Changed: false}, nil
		}
		if session.Version != command.ExpectedVersion {
			return ArchiveUserSessionResult{}, versionConflict(operation, "session", command.SessionID, session.Version)
		}
		if session.ActiveRunID != "" {
			return ArchiveUserSessionResult{}, commandError(ErrorInvalidState, operation, "session", command.SessionID, "a session with an active run cannot be archived")
		}
		update := fmt.Sprintf(`
UPDATE %s
SET status = 'archived', version = version + 1, updated_at = pg_catalog.clock_timestamp()
WHERE id = $1`, s.table("sessions"))
		if _, err := transaction.Exec(ctx, update, command.SessionID); err != nil {
			return ArchiveUserSessionResult{}, databaseError(operation+" archive session", err)
		}
		session, err = s.readUserSession(ctx, transaction, operation, command.WorkspaceID, command.SessionID, command.ActorID, false)
		return ArchiveUserSessionResult{Session: session, Changed: true}, err
	})
}

func (s *StateStore) readUserSession(
	ctx context.Context,
	transaction pgx.Tx,
	operation, workspaceID, sessionID, actorID string,
	lock bool,
) (UserSession, error) {
	query := fmt.Sprintf(`
SELECT session.id::text, session.workspace_id::text, session.creator_id::text,
       session.title, session.status, session.active_run_id::text,
       session.version, session.permission_mode, session.permission_mode_version,
       session.working_environment_id::text, session.working_directory,
       session.working_directory_version,
       session.created_at, session.updated_at
FROM %s AS session
JOIN %s AS workspace
  ON workspace.id = session.workspace_id AND workspace.status = 'active'
JOIN %s AS member
  ON member.workspace_id = session.workspace_id AND member.user_id = $3
JOIN %s AS local_user
  ON local_user.id = member.user_id AND local_user.status = 'active'
WHERE session.id = $1
  AND session.workspace_id = $2
  AND session.creator_id = $3`,
		s.table("sessions"), s.table("workspaces"), s.table("workspace_members"), s.table("users"))
	if lock {
		query += " FOR UPDATE OF session FOR SHARE OF workspace, member, local_user"
	}
	session, err := scanUserSession(transaction.QueryRow(ctx, query, sessionID, workspaceID, actorID))
	if errors.Is(err, pgx.ErrNoRows) {
		return UserSession{}, commandError(ErrorNotFound, operation, "session", sessionID, "active authorized session does not exist")
	}
	if err != nil {
		return UserSession{}, databaseError(operation+" read session", err)
	}
	return session, nil
}

type userSessionRowScanner interface {
	Scan(...any) error
}

func scanUserSession(row userSessionRowScanner) (UserSession, error) {
	var session UserSession
	var activeRunID *string
	var workingEnvironmentID *string
	err := row.Scan(
		&session.ID, &session.WorkspaceID, &session.CreatorID, &session.Title,
		&session.Status, &activeRunID, &session.Version, &session.PermissionMode,
		&session.PermissionModeVersion, &workingEnvironmentID, &session.WorkingDirectory,
		&session.WorkingDirectoryVersion, &session.CreatedAt, &session.UpdatedAt,
	)
	if activeRunID != nil {
		session.ActiveRunID = *activeRunID
	}
	if workingEnvironmentID != nil {
		session.WorkingEnvironmentID = *workingEnvironmentID
	}
	return session, err
}

func validateUserSessionScope(workspaceID, sessionID, actorID string) error {
	if err := validateUUID("workspace_id", workspaceID); err != nil {
		return err
	}
	if err := validateUUID("session_id", sessionID); err != nil {
		return err
	}
	return validateUUID("actor_id", actorID)
}

func validateUserSessionTitle(title string) error {
	if title == "" || len(title) > 256 || !utf8.ValidString(title) || strings.TrimSpace(title) != title {
		return errors.New("title must contain between 1 and 256 canonical UTF-8 bytes")
	}
	for _, value := range title {
		if unicode.IsControl(value) {
			return errors.New("title must not contain control characters")
		}
	}
	return nil
}
