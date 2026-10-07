package coredb

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/runmanifest"
	"github.com/jackc/pgx/v5/pgxpool"
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
	empty, err := store.ReadUserSessionJournal(t.Context(), workspaceID, sessionID, actorID, 0)
	if err != nil || len(empty.Entries) != 2 || empty.Entries[0].PermissionMode != "read-only" || empty.Entries[1].TitleSource != "placeholder" || empty.Cursor != 2 {
		t.Fatalf("initial permission: %+v %v", empty, err)
	}
	for version, mode := range []runmanifest.CodexPermissionMode{runmanifest.CodexPermissionModeFullAccess, runmanifest.CodexPermissionModeReadOnly} {
		_, err := store.UpdateUserSessionPermissionMode(t.Context(), UpdateUserSessionPermissionModeCommand{WorkspaceID: workspaceID, SessionID: sessionID, ActorID: actorID, PermissionMode: mode, ExpectedPermissionModeVersion: int64(version + 1)})
		if err != nil {
			t.Fatal(err)
		}
	}
	create := stateCreateRunCommand(870010, workspaceID, sessionID, "journal-first")
	create.ActorID = actorID
	create.Prompt.MediaType = userPromptTranscriptMediaType
	first, err := store.CreateRun(t.Context(), create)
	if err != nil {
		t.Fatal(err)
	}
	for seq := 2; seq <= 140; seq++ {
		_, err := pool.Exec(t.Context(), fmt.Sprintf(`INSERT INTO %s.run_events (run_id,seq,event_id,producer_instance_id,producer_seq,source,kind,schema_version,payload) VALUES ($1,$2,$3,$4,$2,'system','test.journal',1,'{}')`, q), first.Run.ID, seq, stateTestUUID(880000+seq), stateTestUUID(890000))
		if err != nil {
			t.Fatal(err)
		}
	}
	page, err := store.ReadUserSessionJournal(t.Context(), workspaceID, sessionID, actorID, 0)
	if err != nil || !page.HasMore || len(page.Entries) != 128 || page.Cursor != 128 {
		t.Fatalf("page1: %+v %v", page, err)
	}
	if page.Entries[4].RequestID != "journal-first" || page.Entries[2].PermissionMode != "full-access" || page.Entries[3].PermissionVersion != 3 {
		t.Fatal("lost permission changes or prompt receipt")
	}
	page, err = store.ReadUserSessionJournal(t.Context(), workspaceID, sessionID, actorID, 128)
	if err != nil || page.HasMore || len(page.Entries) != 17 || page.Cursor != 145 {
		t.Fatalf("page2: %+v %v", page, err)
	}
	if _, err := store.ReadUserSessionJournal(t.Context(), workspaceID, sessionID, actorID, 146); !HasStateErrorCode(err, ErrorInvalidArgument) {
		t.Fatalf("future cursor: %v", err)
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
	page, err = store.ReadUserSessionJournal(t.Context(), workspaceID, sessionID, actorID, 145)
	if err != nil || len(page.Entries) != 2 || page.Entries[0].Run.ID != second.Run.ID || page.Cursor != 147 {
		t.Fatalf("second run: %+v %v", page, err)
	}
	replayed, err := store.ReadUserSessionJournal(t.Context(), workspaceID, sessionID, actorID, 145)
	if err != nil || !reflect.DeepEqual(page, replayed) {
		t.Fatal("replay changed committed entries", err)
	}
	page, err = store.ReadUserSessionJournal(t.Context(), workspaceID, sessionID, actorID, 147)
	if err != nil || len(page.Entries) != 0 || page.Cursor != 147 || page.HasMore {
		t.Fatalf("idle: %+v %v", page, err)
	}
	if _, err := pool.Exec(t.Context(), fmt.Sprintf(`DELETE FROM %s.workspace_members WHERE workspace_id=$1 AND user_id=$2`, q), workspaceID, actorID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadUserSessionJournal(t.Context(), workspaceID, sessionID, actorID, 147); err == nil {
		t.Fatal("membership removal did not revoke journal access")
	}
}

func TestPostgreSQLSessionJournalMigrationPreservesExistingRunPrefix(t *testing.T) {
	config := postgresIntegrationConfig(t)
	schema := newPostgresTestSchema(t, config)
	catalog, err := EmbeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	runner := runnerConfig{schema: schema, lockKey: migrationAdvisoryLockKey, catalog: catalog[:33]}
	if _, err := migrateConfig(t.Context(), config, runner); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(t.Context(), os.Getenv("AGENTSERVER_V2_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal("open local test pool failed")
	}
	defer pool.Close()
	store := newStateStore(pool, schema)
	workspaceID, sessionID := stateTestUUID(870100), stateTestUUID(870101)
	const actorID = "99000000-0000-4000-8000-000000000001"
	insertStateTestSession(t, pool, schema, workspaceID, sessionID)
	q := quoteIdentifier(schema)
	if _, err := pool.Exec(t.Context(), fmt.Sprintf(`INSERT INTO %s.workspace_members (workspace_id,user_id,role) VALUES ($1,$2,'developer')`, q), workspaceID, actorID); err != nil {
		t.Fatal(err)
	}
	create := stateCreateRunCommand(870110, workspaceID, sessionID, "pre-migration-prompt")
	create.ActorID = actorID
	create.Prompt.MediaType = userPromptTranscriptMediaType
	if _, err := store.CreateRun(t.Context(), create); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), fmt.Sprintf(`UPDATE %s.sessions SET permission_mode='full-access',permission_mode_version=2 WHERE id=$1`, q), sessionID); err != nil {
		t.Fatal(err)
	}
	runner.catalog = catalog
	if _, err := migrateConfig(t.Context(), config, runner); err != nil {
		t.Fatal(err)
	}
	page, err := store.ReadUserSessionJournal(t.Context(), workspaceID, sessionID, actorID, 0)
	if err != nil || len(page.Entries) != 4 {
		t.Fatalf("migrated journal: %+v %v", page, err)
	}
	if page.Entries[0].Kind != "prompt" || page.Entries[1].Kind != "run_event" || page.Entries[2].PermissionMode != "full-access" || page.Entries[2].PermissionVersion != 2 {
		t.Fatalf("migration inserted metadata into old run prefix: %+v", page)
	}
}

func TestPostgreSQLSessionJournalCommitOrderingAndRollback(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(fmt.Sprintf("rollback=%t", rollback), func(t *testing.T) {
			store, pool, schema := newPostgresStateStore(t)
			workspaceID, sessionID := stateTestUUID(870200), stateTestUUID(870201)
			const actorID = "99000000-0000-4000-8000-000000000001"
			insertStateTestSession(t, pool, schema, workspaceID, sessionID)
			q := quoteIdentifier(schema)
			if _, err := pool.Exec(t.Context(), fmt.Sprintf(`INSERT INTO %s.workspace_members (workspace_id,user_id,role) VALUES ($1,$2,'developer')`, q), workspaceID, actorID); err != nil {
				t.Fatal(err)
			}
			create := stateCreateRunCommand(870210, workspaceID, sessionID, "concurrent-journal")
			create.ActorID = actorID
			create.Prompt.MediaType = userPromptTranscriptMediaType
			if _, err := store.CreateRun(t.Context(), create); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			first, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer first.Rollback(context.Background())
			if _, err := first.Exec(ctx, fmt.Sprintf(`UPDATE %s.sessions SET permission_mode='full-access',permission_mode_version=2 WHERE id=$1`, q), sessionID); err != nil {
				t.Fatal(err)
			}
			second, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer second.Rollback(context.Background())
			var pid int
			if err := second.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				_, err := second.Exec(ctx, fmt.Sprintf(`INSERT INTO %s.run_events (run_id,seq,event_id,producer_instance_id,producer_seq,source,kind,schema_version,payload) VALUES ($1,2,$2,$3,2,'system','test.journal',1,'{}')`, q), create.RunID, stateTestUUID(870220), stateTestUUID(870221))
				if err == nil {
					err = second.Commit(ctx)
				}
				done <- err
			}()
			// Prove the later source mutation is actually waiting for the journal
			// head, rather than relying on scheduling or a fixed sleep.
			for {
				var waiting bool
				if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_locks WHERE pid=$1 AND NOT granted)`, pid).Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				select {
				case err := <-done:
					t.Fatalf("later source committed ahead of permission: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(5 * time.Millisecond):
				}
			}
			page, err := store.ReadUserSessionJournal(ctx, workspaceID, sessionID, actorID, 4)
			if err != nil || page.Cursor != 4 || len(page.Entries) != 0 || page.Session.PermissionMode != "read-only" {
				t.Fatalf("uncommitted change leaked: %+v %v", page, err)
			}
			if rollback {
				err = first.Rollback(ctx)
			} else {
				err = first.Commit(ctx)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			page, err = store.ReadUserSessionJournal(ctx, workspaceID, sessionID, actorID, 4)
			if err != nil {
				t.Fatal(err)
			}
			if rollback {
				if len(page.Entries) != 1 || page.Cursor != 5 || page.Entries[0].Kind != "run_event" || page.Session.PermissionMode != "read-only" {
					t.Fatalf("rollback left gap or permission: %+v", page)
				}
			} else if len(page.Entries) != 2 || page.Entries[0].Kind != "permission" || page.Entries[1].Kind != "run_event" || page.Cursor != 6 {
				t.Fatalf("committed prefix reordered: %+v", page)
			}
		})
	}
}
