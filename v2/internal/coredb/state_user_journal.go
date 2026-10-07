package coredb

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const userJournalPageEvents = 128

type UserSessionJournalPage struct {
	Session       UserSession
	Run           UserSessionTranscriptRun
	IncludePrompt bool
	RequestID     string
	Events        []UserSessionTranscriptEvent
	AfterSeq      int64
	HasMore       bool
}

// ReadUserSessionJournal advances through one run at a time. It never skips an
// unfinished run or uses the bounded/truncated transcript as a journal source.
// Ownership and cursor scope are checked in the same repeatable-read transaction.
func (s *StateStore) ReadUserSessionJournal(ctx context.Context, workspaceID, sessionID, actorID, runID string, after int64) (UserSessionJournalPage, error) {
	const operation = "ReadUserSessionJournal"
	if err := validateUserSessionScope(workspaceID, sessionID, actorID); err != nil {
		return UserSessionJournalPage{}, commandError(ErrorInvalidArgument, operation, "session", sessionID, err.Error())
	}
	if after < 0 || after >= maxSafeJSONInteger || (runID == "" && after != 0) {
		return UserSessionJournalPage{}, commandError(ErrorInvalidArgument, operation, "cursor", runID, "invalid journal sequence")
	}
	if runID != "" {
		if parsed, err := uuid.Parse(runID); err != nil || parsed.String() != runID {
			return UserSessionJournalPage{}, commandError(ErrorInvalidArgument, operation, "cursor", runID, "invalid journal run")
		}
	}
	return withStateReadTransaction(ctx, s, operation, func(tx pgx.Tx) (UserSessionJournalPage, error) {
		session, err := s.readUserSession(ctx, tx, operation, workspaceID, sessionID, actorID, false)
		if err != nil {
			return UserSessionJournalPage{}, err
		}
		page := UserSessionJournalPage{Session: session, AfterSeq: after}
		var anchor time.Time
		if runID != "" {
			var head int64
			err = tx.QueryRow(ctx, fmt.Sprintf(`SELECT run.created_at,COALESCE((SELECT MAX(seq) FROM %s event WHERE event.run_id=run.id),0) FROM %s run WHERE run.id=$1 AND run.workspace_id=$2 AND run.session_id=$3 AND run.actor_id=$4`, s.table("run_events"), s.table("runs")), runID, workspaceID, sessionID, actorID).Scan(&anchor, &head)
			if errors.Is(err, pgx.ErrNoRows) {
				return page, commandError(ErrorNotFound, operation, "run", runID, "journal cursor not found")
			}
			if err != nil {
				return page, databaseError(operation, err)
			}
			if after > head {
				return page, commandError(ErrorInvalidArgument, operation, "cursor", runID, "journal cursor is ahead of committed events")
			}
		}
		includePrompt := runID == ""
		for {
			query := fmt.Sprintf(`SELECT run.id::text, run.status, launch.prompt_object_id::text, launch.prompt_sha256, launch.prompt_size, launch.prompt_media_type, run.created_at, run.idempotency_key
FROM %s run JOIN %s launch ON launch.run_id=run.id
WHERE run.workspace_id=$1 AND run.session_id=$2 AND run.actor_id=$3
AND ($4::text='' OR (run.created_at,run.id::text) >= ($5::timestamptz,$4::text))
ORDER BY run.created_at,run.id LIMIT 1`, s.table("runs"), s.table("run_launch_states"))
			var digest []byte
			err = tx.QueryRow(ctx, query, workspaceID, sessionID, actorID, runID, anchor).Scan(&page.Run.ID, &page.Run.Status, &page.Run.Prompt.ObjectID, &digest, &page.Run.Prompt.Size, &page.Run.Prompt.MediaType, &page.Run.CreatedAt, &page.RequestID)
			if errors.Is(err, pgx.ErrNoRows) {
				return page, nil
			}
			if err != nil {
				return page, databaseError(operation+" run", err)
			}
			if err = copyStoredSHA256(&page.Run.Prompt.SHA256, digest); err != nil {
				return page, err
			}
			page.IncludePrompt = includePrompt
			if page.IncludePrompt {
				page.AfterSeq = 0
			}
			rows, err := tx.Query(ctx, fmt.Sprintf(`SELECT event.run_id::text, event.event_id::text,event.seq,event.run_attempt_id::text,event.run_attempt_generation,event.producer_instance_id::text,event.producer_seq,event.source,event.kind,event.schema_version,event.payload,event.object_id::text,event.object_sha256,event.object_size,event.object_media_type,event.created_at
FROM %s event WHERE event.run_id=$1 AND event.seq>$2 ORDER BY event.seq LIMIT $3`, s.table("run_events")), page.Run.ID, page.AfterSeq, userJournalPageEvents+1)
			if err != nil {
				return page, databaseError(operation+" events", err)
			}
			for rows.Next() {
				event, scanErr := scanUserSessionTranscriptEvent(rows)
				if scanErr != nil {
					rows.Close()
					return page, scanErr
				}
				page.Events = append(page.Events, event)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return page, err
			}
			if len(page.Events) > userJournalPageEvents {
				page.Events = page.Events[:userJournalPageEvents]
				page.HasMore = true
			}
			for _, event := range page.Events {
				if event.Event.Seq != page.AfterSeq+1 {
					return page, errors.New("session journal event gap")
				}
				page.AfterSeq = event.Event.Seq
			}
			terminal := page.Run.Status == "completed" || page.Run.Status == "failed" || page.Run.Status == "cancelled" || page.Run.Status == "interrupted"
			if !terminal || page.HasMore {
				return page, nil
			}
			var nextID string
			var nextAt time.Time
			err = tx.QueryRow(ctx, fmt.Sprintf(`SELECT id::text,created_at FROM %s WHERE workspace_id=$1 AND session_id=$2 AND actor_id=$3 AND (created_at,id::text)>($4,$5) ORDER BY created_at,id LIMIT 1`, s.table("runs")), workspaceID, sessionID, actorID, page.Run.CreatedAt, page.Run.ID).Scan(&nextID, &nextAt)
			if errors.Is(err, pgx.ErrNoRows) {
				return page, nil
			}
			if err != nil {
				return page, err
			}
			if page.IncludePrompt || len(page.Events) > 0 {
				page.HasMore = true
				return page, nil
			}
			// The previous run was already fully consumed. Start the next one.
			runID, anchor = nextID, nextAt
			page.AfterSeq = 0
			includePrompt = true
		}
	})
}
