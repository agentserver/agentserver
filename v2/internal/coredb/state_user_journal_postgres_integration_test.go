package coredb

import (
	"fmt"
	"testing"
)

func TestPostgreSQLSessionJournalPagesRunsAndEnforcesCursorOwnership(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	workspaceID, sessionID := stateTestUUID(870000), stateTestUUID(870001)
	const actorID = "99000000-0000-4000-8000-000000000001"
	insertStateTestSession(t, pool, schema, workspaceID, sessionID)
	q := quoteIdentifier(schema)
	if _, err := pool.Exec(t.Context(), fmt.Sprintf(`INSERT INTO %s.workspace_members (workspace_id,user_id,role) VALUES ($1,$2,'developer')`, q), workspaceID, actorID); err != nil {
		t.Fatal(err)
	}
	empty, err := store.ReadUserSessionJournal(t.Context(), workspaceID, sessionID, actorID, "", 0)
	if err != nil || empty.Run.ID != "" || empty.HasMore {
		t.Fatalf("empty: %+v %v", empty, err)
	}
	create := stateCreateRunCommand(870010, workspaceID, sessionID, "journal-first")
	create.ActorID = actorID
	create.Prompt.MediaType = userPromptTranscriptMediaType
	first, err := store.CreateRun(t.Context(), create)
	if err != nil {
		t.Fatal(err)
	}
	// Fixture includes more than one page, including kinds the DSH mapper
	// does not display. Such events still advance the canonical source cursor.
	for seq := 2; seq <= 140; seq++ {
		_, err := pool.Exec(t.Context(), fmt.Sprintf(`INSERT INTO %s.run_events (run_id,seq,event_id,producer_instance_id,producer_seq,source,kind,schema_version,payload) VALUES ($1,$2,$3,$4,$2,'system','test.journal',1,'{}')`, q), first.Run.ID, seq, stateTestUUID(880000+seq), stateTestUUID(890000))
		if err != nil {
			t.Fatal(err)
		}
	}
	page, err := store.ReadUserSessionJournal(t.Context(), workspaceID, sessionID, actorID, "", 0)
	if err != nil || !page.IncludePrompt || !page.HasMore || len(page.Events) != 128 || page.AfterSeq != 128 {
		t.Fatalf("page1: count=%d seq=%d more=%t prompt=%t err=%v", len(page.Events), page.AfterSeq, page.HasMore, page.IncludePrompt, err)
	}
	if page.RequestID != "journal-first" {
		t.Fatal("journal lost durable request identity")
	}
	page, err = store.ReadUserSessionJournal(t.Context(), workspaceID, sessionID, actorID, first.Run.ID, 128)
	if err != nil || page.IncludePrompt || page.HasMore || len(page.Events) != 12 || page.AfterSeq != 140 {
		t.Fatalf("page2: %+v %v", page, err)
	}
	if _, err := store.ReadUserSessionJournal(t.Context(), workspaceID, sessionID, actorID, first.Run.ID, 141); !HasStateErrorCode(err, ErrorInvalidArgument) {
		t.Fatalf("future cursor: %v", err)
	}
	if _, err := store.ReadUserSessionJournal(t.Context(), workspaceID, sessionID, actorID, stateTestUUID(900000), 0); !HasStateErrorCode(err, ErrorNotFound) {
		t.Fatalf("foreign cursor: %v", err)
	}
	if _, err := pool.Exec(t.Context(), fmt.Sprintf(`UPDATE %s.runs SET status='completed' WHERE id=$1`, q), first.Run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), fmt.Sprintf(`UPDATE %s.sessions SET active_run_id=NULL WHERE id=$1`, q), sessionID); err != nil {
		t.Fatal(err)
	}
	create = stateCreateRunCommand(870020, workspaceID, sessionID, "journal-second")
	create.ActorID = actorID
	create.Prompt.MediaType = userPromptTranscriptMediaType
	create.ExpectedSessionVersion = first.SessionVersion
	second, err := store.CreateRun(t.Context(), create)
	if err != nil {
		t.Fatal(err)
	}
	page, err = store.ReadUserSessionJournal(t.Context(), workspaceID, sessionID, actorID, first.Run.ID, 140)
	if err != nil || page.Run.ID != second.Run.ID || !page.IncludePrompt || page.AfterSeq != 1 {
		t.Fatalf("next run: %+v %v", page, err)
	}
	page, err = store.ReadUserSessionJournal(t.Context(), workspaceID, sessionID, actorID, second.Run.ID, 1)
	if err != nil || page.IncludePrompt || len(page.Events) != 0 || page.AfterSeq != 1 {
		t.Fatalf("idle: %+v %v", page, err)
	}
	if _, err := pool.Exec(t.Context(), fmt.Sprintf(`DELETE FROM %s.workspace_members WHERE workspace_id=$1 AND user_id=$2`, q), workspaceID, actorID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadUserSessionJournal(t.Context(), workspaceID, sessionID, actorID, second.Run.ID, 1); err == nil {
		t.Fatal("membership removal did not revoke journal access")
	}
}
