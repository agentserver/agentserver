package coredb

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/agentserver/agentserver/v2/internal/runmanifest"
	"github.com/jackc/pgx/v5"
)

const maxAuthorizedRunEventPage = 1024

// AuthorizeRunSession performs an early membership check before a caller
// writes a prompt object. CreateAuthorizedRun repeats the security decision in
// its transaction, so a membership revocation between these calls fails
// closed and leaves at most an unreferenced object for the retention cleaner.
func (s *StateStore) AuthorizeRunSession(ctx context.Context, workspaceID, sessionID, actorID string) (AuthorizedSession, error) {
	const operation = "AuthorizeRunSession"
	for field, value := range map[string]string{
		"workspace_id": workspaceID,
		"session_id":   sessionID,
		"actor_id":     actorID,
	} {
		if err := validateUUID(field, value); err != nil {
			return AuthorizedSession{}, commandError(ErrorInvalidArgument, operation, "session", sessionID, err.Error())
		}
	}
	query := fmt.Sprintf(`
	SELECT wm.role, s.version, s.permission_mode, s.permission_mode_version
FROM %s AS s
JOIN %s AS w ON w.id = s.workspace_id
JOIN %s AS wm ON wm.workspace_id = s.workspace_id AND wm.user_id = $3
WHERE s.id = $1 AND s.workspace_id = $2 AND s.creator_id = $3
  AND s.status = 'active' AND w.status = 'active'`,
		s.table("sessions"), s.table("workspaces"), s.table("workspace_members"))
	var role string
	var version int64
	var permissionMode string
	var permissionModeVersion int64
	if err := s.queryRow(ctx, query, sessionID, workspaceID, actorID).Scan(&role, &version, &permissionMode, &permissionModeVersion); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AuthorizedSession{}, commandError(ErrorNotFound, operation, "session", sessionID, "active authorized session does not exist")
		}
		return AuthorizedSession{}, databaseError(operation+" read session membership", err)
	}
	if role == "viewer" {
		return AuthorizedSession{}, commandError(ErrorForbidden, operation, "workspace", workspaceID, "workspace role cannot create runs")
	}
	mode, err := runmanifest.CodexPermissionMode(permissionMode).Effective()
	if err != nil || permissionModeVersion < 1 || permissionModeVersion > maxSafeJSONInteger {
		if err == nil {
			err = errors.New("stored session permission mode version is invalid")
		}
		return AuthorizedSession{}, databaseError(operation+" validate session permission mode", err)
	}
	return AuthorizedSession{WorkspaceID: workspaceID, SessionID: sessionID, ActorID: actorID, Role: role, SessionVersion: version,
		PermissionMode: mode, PermissionModeVersion: permissionModeVersion}, nil
}

// ReadAuthorizedRunEvents returns one transactionally consistent committed
// page. Authorization is joined into the run lookup, so removing a workspace
// member immediately fences subsequent long-poll iterations.
func (s *StateStore) ReadAuthorizedRunEvents(ctx context.Context, command ReadAuthorizedRunEventsCommand) (ReadAuthorizedRunEventsResult, error) {
	const operation = "ReadAuthorizedRunEvents"
	if err := validateReadAuthorizedRunEvents(command); err != nil {
		return ReadAuthorizedRunEventsResult{}, commandError(ErrorInvalidArgument, operation, "run", command.RunID, err.Error())
	}
	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (ReadAuthorizedRunEventsResult, error) {
		runQuery := fmt.Sprintf(`
SELECT %s
FROM %s AS r
JOIN %s AS w ON w.id = r.workspace_id AND w.status = 'active'
JOIN %s AS wm ON wm.workspace_id = r.workspace_id AND wm.user_id = $3
WHERE r.id = $1 AND r.workspace_id = $2 AND r.actor_id = $3
FOR SHARE OF r, w, wm`,
			runColumns("r"), s.table("runs"), s.table("workspaces"), s.table("workspace_members"))
		run, err := scanRun(transaction.QueryRow(ctx, runQuery, command.RunID, command.WorkspaceID, command.ActorID))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ReadAuthorizedRunEventsResult{}, commandError(ErrorNotFound, operation, "run", command.RunID, "active authorized run does not exist")
			}
			return ReadAuthorizedRunEventsResult{}, databaseError(operation+" read authorized run", err)
		}
		lastSequence := run.NextEventSeq - 1
		if command.AfterSeq > lastSequence {
			return ReadAuthorizedRunEventsResult{}, commandError(ErrorInvalidArgument, operation, "run", run.ID, "event cursor is ahead of the committed run ledger")
		}

		minimumQuery := fmt.Sprintf(`SELECT COALESCE(pg_catalog.min(seq), $2) FROM %s WHERE run_id = $1`, s.table("run_events"))
		var earliestSequence int64
		if err := transaction.QueryRow(ctx, minimumQuery, run.ID, run.NextEventSeq).Scan(&earliestSequence); err != nil {
			return ReadAuthorizedRunEventsResult{}, databaseError(operation+" read event retention boundary", err)
		}
		result := ReadAuthorizedRunEventsResult{
			Run: run, EarliestSequence: earliestSequence, LastSequence: lastSequence,
			Events: make([]RunEvent, 0, command.Limit),
		}
		if command.AfterSeq < earliestSequence-1 {
			rebaseQuery := fmt.Sprintf(`
SELECT after_seq, run_status, run_version, run_updated_at, snapshot, created_at
FROM %s
WHERE run_id = $1 AND after_seq >= $2`, s.table("run_event_rebases"))
			var rebase RunEventRebase
			var snapshot []byte
			if err := transaction.QueryRow(ctx, rebaseQuery, run.ID, command.AfterSeq).Scan(
				&rebase.AfterSequence,
				&rebase.RunStatus,
				&rebase.RunVersion,
				&rebase.RunUpdatedAt,
				&snapshot,
				&rebase.CreatedAt,
			); err != nil {
				if !errors.Is(err, pgx.ErrNoRows) {
					return ReadAuthorizedRunEventsResult{}, databaseError(operation+" read event rebase", err)
				}
			} else {
				if rebase.AfterSequence != earliestSequence-1 || len(snapshot) == 0 {
					return ReadAuthorizedRunEventsResult{}, databaseError(operation+" validate event rebase", errors.New("stored rebase does not match the retention boundary"))
				}
				rebase.Snapshot = append(rebase.Snapshot, snapshot...)
				result.Rebase = &rebase
			}
			return result, nil
		}

		eventQuery := fmt.Sprintf(`
SELECT event_id::text, seq, run_attempt_id::text, run_attempt_generation,
       producer_instance_id::text, producer_seq, source, kind, schema_version,
       payload, object_id::text, object_sha256, object_size,
       object_media_type, created_at
FROM %s
WHERE run_id = $1 AND seq > $2
ORDER BY seq
LIMIT $3`, s.table("run_events"))
		rows, err := transaction.Query(ctx, eventQuery, run.ID, command.AfterSeq, command.Limit)
		if err != nil {
			return ReadAuthorizedRunEventsResult{}, databaseError(operation+" read event page", err)
		}
		defer rows.Close()
		for rows.Next() {
			event, err := scanAuthorizedRunEvent(rows)
			if err != nil {
				return ReadAuthorizedRunEventsResult{}, databaseError(operation+" scan event", err)
			}
			result.Events = append(result.Events, event)
		}
		if err := rows.Err(); err != nil {
			return ReadAuthorizedRunEventsResult{}, databaseError(operation+" finish event page", err)
		}
		return result, nil
	})
}

func (s *StateStore) readWorkspaceMemberRole(ctx context.Context, transaction pgx.Tx, workspaceID, actorID string) (string, error) {
	query := fmt.Sprintf(`SELECT role FROM %s WHERE workspace_id = $1 AND user_id = $2 FOR SHARE`, s.table("workspace_members"))
	var role string
	if err := transaction.QueryRow(ctx, query, workspaceID, actorID).Scan(&role); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", commandError(ErrorForbidden, "CreateAuthorizedRun", "workspace", workspaceID, "actor is not a current workspace member")
		}
		return "", databaseError("CreateAuthorizedRun read workspace membership", err)
	}
	return role, nil
}

type queryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (s *StateStore) queryRow(ctx context.Context, query string, arguments ...any) pgx.Row {
	if database, ok := s.database.(queryRower); ok {
		return database.QueryRow(ctx, query, arguments...)
	}
	return errorRow{err: errors.New("state database does not support read queries")}
}

type errorRow struct{ err error }

func (row errorRow) Scan(...any) error { return row.err }

func validateReadAuthorizedRunEvents(command ReadAuthorizedRunEventsCommand) error {
	for field, value := range map[string]string{
		"workspace_id": command.WorkspaceID,
		"actor_id":     command.ActorID,
		"run_id":       command.RunID,
	} {
		if err := validateUUID(field, value); err != nil {
			return err
		}
	}
	if command.AfterSeq < 0 || command.AfterSeq >= maxSafeJSONInteger {
		return errors.New("after_seq must be a nonnegative JSON-safe integer")
	}
	if command.Limit < 1 || command.Limit > maxAuthorizedRunEventPage {
		return fmt.Errorf("limit must be between 1 and %d", maxAuthorizedRunEventPage)
	}
	return nil
}

func scanAuthorizedRunEvent(scanner rowScanner) (RunEvent, error) {
	var event RunEvent
	var payload []byte
	var objectID *string
	var objectDigest []byte
	var objectSize *int64
	var objectMediaType *string
	if err := scanner.Scan(
		&event.EventID, &event.Seq, &event.RunAttemptID, &event.RunAttemptGeneration,
		&event.ProducerInstanceID, &event.ProducerSeq, &event.Source, &event.Kind,
		&event.SchemaVersion, &payload, &objectID, &objectDigest, &objectSize,
		&objectMediaType, &event.CreatedAt,
	); err != nil {
		return RunEvent{}, err
	}
	if event.Seq < 1 || event.Seq >= maxSafeJSONInteger || event.ProducerSeq < 1 || event.ProducerSeq >= maxSafeJSONInteger || event.SchemaVersion < 1 {
		return RunEvent{}, errors.New("stored run event contains an invalid sequence or schema version")
	}
	if (event.RunAttemptID == nil) != (event.RunAttemptGeneration == nil) {
		return RunEvent{}, errors.New("stored run event has an incomplete attempt scope")
	}
	if objectID == nil {
		if len(payload) == 0 {
			return RunEvent{}, errors.New("stored run event has neither payload nor object")
		}
		event.Payload = append(event.Payload, payload...)
		return event, nil
	}
	if len(payload) != 0 || objectSize == nil || objectMediaType == nil || *objectSize < 1 || *objectSize > math.MaxInt64 {
		return RunEvent{}, errors.New("stored run event object metadata is incomplete")
	}
	pointer := ObjectPointer{ObjectID: *objectID, Size: *objectSize, MediaType: *objectMediaType}
	if err := copyStoredSHA256(&pointer.SHA256, objectDigest); err != nil {
		return RunEvent{}, err
	}
	event.Object = &pointer
	return event, nil
}
