package coredb

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const userJournalPageEvents = 128

type UserSessionJournalEntry struct {
	Seq               int64
	Kind              string
	RunID             string
	RunSeq            int64
	PermissionMode    string
	PermissionVersion int64
	Title             string
	TitleSource       string
	TitleVersion      int64
	CreatedAt         time.Time
	Run               UserSessionTranscriptRun
	RequestID         string
	Event             UserSessionTranscriptEvent
}

type UserSessionJournalPage struct {
	Session UserSession
	Entries []UserSessionJournalEntry
	Cursor  int64
	HasMore bool
}

// ReadUserSessionJournal reads a committed prefix including both run entries
// and permission mutations. The head allocator is transactional, so a later
// commit can never appear behind a cursor a reader has already consumed.
func (s *StateStore) ReadUserSessionJournal(ctx context.Context, workspaceID, sessionID, actorID string, cursor int64) (UserSessionJournalPage, error) {
	const operation = "ReadUserSessionJournal"
	if err := validateUserSessionScope(workspaceID, sessionID, actorID); err != nil {
		return UserSessionJournalPage{}, commandError(ErrorInvalidArgument, operation, "session", sessionID, err.Error())
	}
	if cursor < 0 || cursor >= maxSafeJSONInteger {
		return UserSessionJournalPage{}, commandError(ErrorInvalidArgument, operation, "cursor", sessionID, "invalid journal sequence")
	}
	return withStateReadTransaction(ctx, s, operation, func(tx pgx.Tx) (UserSessionJournalPage, error) {
		session, err := s.readUserSession(ctx, tx, operation, workspaceID, sessionID, actorID, false)
		if err != nil {
			return UserSessionJournalPage{}, err
		}
		page := UserSessionJournalPage{Session: session, Cursor: cursor}
		var head int64
		if err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT COALESCE(MAX(seq),0) FROM %s WHERE session_id=$1`, s.table("session_journal")), sessionID).Scan(&head); err != nil {
			return page, err
		}
		if cursor > head {
			return page, commandError(ErrorInvalidArgument, operation, "cursor", sessionID, "journal cursor is ahead of committed entries")
		}
		rows, err := tx.Query(ctx, fmt.Sprintf(`SELECT seq,kind,COALESCE(run_id::text,''),COALESCE(run_seq,0),COALESCE(permission_mode,''),COALESCE(permission_version,0),created_at,COALESCE(title,''),COALESCE(title_source,''),COALESCE(title_version,0) FROM %s WHERE session_id=$1 AND seq>$2 ORDER BY seq LIMIT $3`, s.table("session_journal")), sessionID, cursor, userJournalPageEvents)
		if err != nil {
			return page, err
		}
		prompts := map[string]int{}
		events := map[string]int{}
		for rows.Next() {
			var entry UserSessionJournalEntry
			if err := rows.Scan(&entry.Seq, &entry.Kind, &entry.RunID, &entry.RunSeq, &entry.PermissionMode, &entry.PermissionVersion, &entry.CreatedAt, &entry.Title, &entry.TitleSource, &entry.TitleVersion); err != nil {
				rows.Close()
				return page, err
			}
			if entry.Seq != page.Cursor+1 {
				rows.Close()
				return page, errors.New("session journal entry gap")
			}
			if entry.Kind == "prompt" {
				prompts[entry.RunID] = len(page.Entries)
			}
			if entry.Kind == "run_event" {
				events[fmt.Sprintf("%s/%d", entry.RunID, entry.RunSeq)] = len(page.Entries)
			}
			page.Entries = append(page.Entries, entry)
			page.Cursor = entry.Seq
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return page, err
		}
		page.HasMore = page.Cursor < head
		// Batch source reads within the same repeatable-read snapshot; no N+1
		// remote/SQL reads per event, no bounded transcript reconstruction.
		if len(prompts) > 0 {
			rows, err = tx.Query(ctx, fmt.Sprintf(`SELECT r.id::text,r.status,l.prompt_object_id::text,l.prompt_sha256,l.prompt_size,l.prompt_media_type,r.created_at,r.idempotency_key
FROM %s j JOIN %s r ON r.id=j.run_id JOIN %s l ON l.run_id=r.id
WHERE j.session_id=$1 AND j.seq>$2 AND j.seq<=$3 AND j.kind='prompt' AND r.actor_id=$4`, s.table("session_journal"), s.table("runs"), s.table("run_launch_states")), sessionID, cursor, page.Cursor, actorID)
			if err != nil {
				return page, err
			}
			for rows.Next() {
				var run UserSessionTranscriptRun
				var digest []byte
				var requestID string
				if err := rows.Scan(&run.ID, &run.Status, &run.Prompt.ObjectID, &digest, &run.Prompt.Size, &run.Prompt.MediaType, &run.CreatedAt, &requestID); err != nil {
					rows.Close()
					return page, err
				}
				if err := copyStoredSHA256(&run.Prompt.SHA256, digest); err != nil {
					rows.Close()
					return page, err
				}
				i := prompts[run.ID]
				page.Entries[i].Run = run
				page.Entries[i].RequestID = requestID
				delete(prompts, run.ID)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return page, err
			}
			if len(prompts) != 0 {
				return page, errors.New("session journal prompt source missing or unauthorized")
			}
		}
		if len(events) > 0 {
			rows, err = tx.Query(ctx, fmt.Sprintf(`SELECT e.run_id::text,e.event_id::text,e.seq,e.run_attempt_id::text,e.run_attempt_generation,e.producer_instance_id::text,e.producer_seq,e.source,e.kind,e.schema_version,e.payload,e.object_id::text,e.object_sha256,e.object_size,e.object_media_type,e.created_at
FROM %s j JOIN %s r ON r.id=j.run_id JOIN %s e ON e.run_id=j.run_id AND e.seq=j.run_seq
WHERE j.session_id=$1 AND j.seq>$2 AND j.seq<=$3 AND j.kind='run_event' AND r.actor_id=$4`, s.table("session_journal"), s.table("runs"), s.table("run_events")), sessionID, cursor, page.Cursor, actorID)
			if err != nil {
				return page, err
			}
			for rows.Next() {
				event, err := scanUserSessionTranscriptEvent(rows)
				if err != nil {
					rows.Close()
					return page, err
				}
				key := fmt.Sprintf("%s/%d", event.RunID, event.Event.Seq)
				page.Entries[events[key]].Event = event
				delete(events, key)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return page, err
			}
			if len(events) != 0 {
				return page, errors.New("session journal event source missing or unauthorized")
			}
		}
		return page, nil
	})
}
