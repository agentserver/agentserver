package coredb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	maxUserSessionTranscriptRuns   = 128
	maxUserSessionTranscriptEvents = 4096
	userPromptTranscriptMediaType  = "text/plain; charset=utf-8"
)

type UserSessionTranscriptRun struct {
	ID        string
	Status    string
	Prompt    ObjectPointer
	CreatedAt time.Time
}

type UserSessionTranscriptEvent struct {
	RunID string
	Event RunEvent
}

type ReadUserSessionTranscriptResult struct {
	Session   UserSession
	Runs      []UserSessionTranscriptRun
	Events    []UserSessionTranscriptEvent
	Truncated bool
}

// ReadUserSessionTranscript returns one bounded, repeatable-read projection
// source. Session ownership and current workspace membership are checked in
// the same read-only transaction as the immutable run inputs and committed
// assistant lifecycle events.
func (s *StateStore) ReadUserSessionTranscript(
	ctx context.Context,
	workspaceID, sessionID, actorID string,
) (ReadUserSessionTranscriptResult, error) {
	const operation = "ReadUserSessionTranscript"
	if err := validateUserSessionScope(workspaceID, sessionID, actorID); err != nil {
		return ReadUserSessionTranscriptResult{}, commandError(ErrorInvalidArgument, operation, "session", sessionID, err.Error())
	}
	return withStateReadTransaction(ctx, s, operation, func(transaction pgx.Tx) (ReadUserSessionTranscriptResult, error) {
		session, err := s.readUserSession(ctx, transaction, operation, workspaceID, sessionID, actorID, false)
		if err != nil {
			return ReadUserSessionTranscriptResult{}, err
		}
		result := ReadUserSessionTranscriptResult{Session: session}
		runQuery := fmt.Sprintf(`
SELECT run.id::text, run.status,
       launch.prompt_object_id::text, launch.prompt_sha256,
       launch.prompt_size, launch.prompt_media_type, run.created_at
FROM %s AS run
JOIN %s AS launch ON launch.run_id = run.id
WHERE run.workspace_id = $1
  AND run.session_id = $2
  AND run.actor_id = $3
ORDER BY run.created_at DESC, run.id DESC
LIMIT $4`, s.table("runs"), s.table("run_launch_states"))
		rows, err := transaction.Query(ctx, runQuery, workspaceID, sessionID, actorID, maxUserSessionTranscriptRuns+1)
		if err != nil {
			return ReadUserSessionTranscriptResult{}, databaseError(operation+" query runs", err)
		}
		for rows.Next() {
			var run UserSessionTranscriptRun
			var digest []byte
			if err := rows.Scan(
				&run.ID, &run.Status, &run.Prompt.ObjectID, &digest,
				&run.Prompt.Size, &run.Prompt.MediaType, &run.CreatedAt,
			); err != nil {
				rows.Close()
				return ReadUserSessionTranscriptResult{}, databaseError(operation+" scan run", err)
			}
			if err := copyStoredSHA256(&run.Prompt.SHA256, digest); err != nil {
				rows.Close()
				return ReadUserSessionTranscriptResult{}, databaseError(operation+" decode prompt digest", err)
			}
			if err := validateRunObjectPointer("prompt", run.Prompt); err != nil || run.Prompt.MediaType != userPromptTranscriptMediaType {
				rows.Close()
				if err == nil {
					err = errors.New("stored prompt media type is unsupported")
				}
				return ReadUserSessionTranscriptResult{}, databaseError(operation+" validate prompt authority", err)
			}
			result.Runs = append(result.Runs, run)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return ReadUserSessionTranscriptResult{}, databaseError(operation+" iterate runs", err)
		}
		rows.Close()
		if len(result.Runs) > maxUserSessionTranscriptRuns {
			result.Runs = result.Runs[:maxUserSessionTranscriptRuns]
			result.Truncated = true
		}
		if len(result.Runs) == 0 {
			return result, nil
		}

		eventQuery := fmt.Sprintf(`
WITH selected_runs AS (
    SELECT run.id, run.created_at
    FROM %s AS run
    WHERE run.workspace_id = $1
      AND run.session_id = $2
      AND run.actor_id = $3
    ORDER BY run.created_at DESC, run.id DESC
    LIMIT $4
)
SELECT event.run_id::text,
       event.event_id::text, event.seq,
       event.run_attempt_id::text, event.run_attempt_generation,
       event.producer_instance_id::text, event.producer_seq,
       event.source, event.kind, event.schema_version,
       event.payload, event.object_id::text, event.object_sha256,
       event.object_size, event.object_media_type, event.created_at
FROM selected_runs AS selected
JOIN %s AS event ON event.run_id = selected.id
WHERE event.kind IN (
    'assistant.message.started',
    'assistant.message.delta',
    'assistant.message.completed'
)
ORDER BY selected.created_at DESC, selected.id DESC, event.seq DESC
LIMIT $5`, s.table("runs"), s.table("run_events"))
		rows, err = transaction.Query(
			ctx, eventQuery, workspaceID, sessionID, actorID,
			maxUserSessionTranscriptRuns, maxUserSessionTranscriptEvents+1,
		)
		if err != nil {
			return ReadUserSessionTranscriptResult{}, databaseError(operation+" query events", err)
		}
		for rows.Next() {
			event, err := scanUserSessionTranscriptEvent(rows)
			if err != nil {
				rows.Close()
				return ReadUserSessionTranscriptResult{}, databaseError(operation+" scan event", err)
			}
			result.Events = append(result.Events, event)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return ReadUserSessionTranscriptResult{}, databaseError(operation+" iterate events", err)
		}
		rows.Close()
		if len(result.Events) > maxUserSessionTranscriptEvents {
			result.Events = result.Events[:maxUserSessionTranscriptEvents]
			result.Truncated = true
		}
		reverseTranscriptRuns(result.Runs)
		reverseTranscriptEvents(result.Events)
		return result, nil
	})
}

func scanUserSessionTranscriptEvent(scanner rowScanner) (UserSessionTranscriptEvent, error) {
	var result UserSessionTranscriptEvent
	var payload []byte
	var objectID *string
	var objectDigest []byte
	var objectSize *int64
	var objectMediaType *string
	event := &result.Event
	if err := scanner.Scan(
		&result.RunID,
		&event.EventID, &event.Seq, &event.RunAttemptID, &event.RunAttemptGeneration,
		&event.ProducerInstanceID, &event.ProducerSeq, &event.Source, &event.Kind,
		&event.SchemaVersion, &payload, &objectID, &objectDigest, &objectSize,
		&objectMediaType, &event.CreatedAt,
	); err != nil {
		return UserSessionTranscriptEvent{}, err
	}
	if event.Seq < 1 || event.Seq >= maxSafeJSONInteger || event.ProducerSeq < 1 ||
		event.ProducerSeq >= maxSafeJSONInteger || event.SchemaVersion < 1 {
		return UserSessionTranscriptEvent{}, errors.New("stored transcript event contains an invalid sequence or schema version")
	}
	if (event.RunAttemptID == nil) != (event.RunAttemptGeneration == nil) {
		return UserSessionTranscriptEvent{}, errors.New("stored transcript event has an incomplete attempt scope")
	}
	if objectID == nil {
		if len(payload) == 0 {
			return UserSessionTranscriptEvent{}, errors.New("stored transcript event has neither payload nor object")
		}
		event.Payload = append(json.RawMessage(nil), payload...)
		return result, nil
	}
	if len(payload) != 0 || objectSize == nil || objectMediaType == nil || *objectSize < 1 || *objectSize > math.MaxInt64 {
		return UserSessionTranscriptEvent{}, errors.New("stored transcript event object metadata is incomplete")
	}
	pointer := ObjectPointer{ObjectID: *objectID, Size: *objectSize, MediaType: *objectMediaType}
	if err := copyStoredSHA256(&pointer.SHA256, objectDigest); err != nil {
		return UserSessionTranscriptEvent{}, err
	}
	event.Object = &pointer
	return result, nil
}

func reverseTranscriptRuns(values []UserSessionTranscriptRun) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

func reverseTranscriptEvents(values []UserSessionTranscriptEvent) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}
