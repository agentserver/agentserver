package coredb

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/sessiontitle"
)

func TestPostgreSQLLegacyRenameDuringRolloutIsManualAndJournaled(t *testing.T) {
	_, pool, schema := newPostgresStateStore(t)
	workspace, session := stateTestUUID(921000), stateTestUUID(921001)
	insertStateTestSession(t, pool, schema, workspace, session)
	q := quoteIdentifier(schema)
	if _, err := pool.Exec(t.Context(), fmt.Sprintf(`UPDATE %s.sessions SET title='Legacy manual title',version=version+1 WHERE id=$1`, q), session); err != nil {
		t.Fatal(err)
	}
	var source, title string
	var version int64
	if err := pool.QueryRow(t.Context(), fmt.Sprintf(`SELECT title,title_source,title_version FROM %s.sessions WHERE id=$1`, q), session).Scan(&title, &source, &version); err != nil {
		t.Fatal(err)
	}
	if title != "Legacy manual title" || source != "manual" || version != 2 {
		t.Fatalf("legacy rename lost: %s %s %d", title, source, version)
	}
	var count int
	if err := pool.QueryRow(t.Context(), fmt.Sprintf(`SELECT count(*) FROM %s.session_journal WHERE session_id=$1 AND kind='title' AND title_version=2 AND title_source='manual'`, q), session).Scan(&count); err != nil || count != 1 {
		t.Fatalf("legacy rename journal=%d err=%v", count, err)
	}
}

func TestPostgreSQLAutomaticTitleUsesFencedRunAndPreservesManualRename(t *testing.T) {
	for _, manual := range []bool{false, true} {
		t.Run(fmt.Sprintf("manual=%t", manual), func(t *testing.T) {
			store, pool, schema := newPostgresStateStore(t)
			workspaceID, sessionID := stateTestUUID(920000), stateTestUUID(920001)
			const actor = "99000000-0000-4000-8000-000000000001"
			insertStateTestSession(t, pool, schema, workspaceID, sessionID)
			if _, err := pool.Exec(t.Context(), fmt.Sprintf(`INSERT INTO %s.workspace_members (workspace_id,user_id,role) VALUES ($1,$2,'developer')`, quoteIdentifier(schema)), workspaceID, actor); err != nil {
				t.Fatal(err)
			}
			create := stateCreateRunCommand(920010, workspaceID, sessionID, "title-first")
			create.ActorID = actor
			created := mustCreateStateRun(t, store, create)
			claim := mustClaimStateRun(t, store, stateClaimRunCommand(920020, created.Run.ID, created.Run.Version, "title-holder"))
			if _, err := store.MarkTurnAccepted(t.Context(), MarkTurnAcceptedCommand{RunID: created.Run.ID, AttemptID: claim.Attempt.ID, HolderID: "title-holder", Generation: 1, ExpectedRunVersion: claim.Run.Version, ExpectedAttemptVersion: claim.Attempt.Version, Record: stateTransitionRecord(920030)}); err != nil {
				t.Fatal(err)
			}
			propose := func(seed int, p sessiontitle.Proposal) AppendAttemptEventsCommand {
				cmd := stateAppendEventsCommand(seed, created.Run.ID, claim.Attempt.ID, "title-holder", 1)
				cmd.Events[0].Kind = sessiontitle.EventKind
				cmd.Events[0].Source = EventSourceBrain
				cmd.Events[0].Payload, _ = json.Marshal(p)
				return cmd
			}
			fallback := propose(920040, sessiontitle.Fallback("检查一下权限为什么没变化"))
			if _, err := store.AppendAttemptEvents(t.Context(), fallback); err != nil {
				t.Fatal(err)
			}
			read := func() UserSession {
				s, err := store.GetUserSession(t.Context(), workspaceID, sessionID, actor)
				if err != nil {
					t.Fatal(err)
				}
				return s
			}
			before := read()
			if before.TitleSource != "fallback" || before.TitleVersion != 2 || before.Version != created.SessionVersion {
				t.Fatalf("fallback did not preserve run CAS: %+v", before)
			}
			if manual {
				// Saving exactly the fallback string still pins the title.
				if _, err := store.UpdateUserSession(t.Context(), UpdateUserSessionCommand{WorkspaceID: workspaceID, SessionID: sessionID, ActorID: actor, Title: before.Title, ExpectedVersion: before.Version}); err != nil {
					t.Fatal(err)
				}
			}
			generated := propose(920050, sessiontitle.Proposal{Title: "修复权限同步", Source: "generated"})
			stale := generated
			stale.Generation = 2
			if _, err := store.AppendAttemptEvents(t.Context(), stale); err == nil {
				t.Fatal("stale generation accepted")
			}
			if _, err := store.AppendAttemptEvents(t.Context(), generated); err != nil {
				t.Fatal(err)
			}
			after := read()
			if manual {
				if after.Title != before.Title || after.TitleSource != "manual" {
					t.Fatalf("overwrote manual title: %+v", after)
				}
			} else if after.Title != "修复权限同步" || after.TitleSource != "generated" || after.TitleVersion != 3 {
				t.Fatalf("generated title missing: %+v", after)
			}
			if _, err := store.AppendAttemptEvents(t.Context(), generated); err != nil {
				t.Fatal(err)
			}
			if read().TitleVersion != after.TitleVersion {
				t.Fatal("retry changed title version")
			}
			lateFallback := propose(920060, sessiontitle.Proposal{Title: "late fallback", Source: "fallback"})
			if _, err := store.AppendAttemptEvents(t.Context(), lateFallback); err != nil {
				t.Fatal(err)
			}
			if read().TitleVersion != after.TitleVersion {
				t.Fatal("fallback replaced accepted title")
			}
			journal, err := store.ReadUserSessionJournal(t.Context(), workspaceID, sessionID, actor, 0)
			if err != nil {
				t.Fatal(err)
			}
			var titles []UserSessionJournalEntry
			for _, entry := range journal.Entries {
				if entry.Kind == "title" {
					titles = append(titles, entry)
				}
			}
			if len(titles) != 3 || titles[2].Title != after.Title || titles[2].TitleVersion != after.TitleVersion {
				t.Fatal("title journal lost accepted revisions")
			}
		})
	}
}
