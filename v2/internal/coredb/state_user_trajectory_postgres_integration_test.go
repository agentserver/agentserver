package coredb

import (
	"fmt"
	"testing"
)

func TestPostgreSQLReadUserSessionTrajectoryIsCreatorScopedAndQueriesAllSources(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	workspaceID := stateTestUUID(210_000)
	sessionID := stateTestUUID(210_001)
	actorID := stateTestUUID(210_002)
	insertStateTestSession(t, pool, schema, workspaceID, sessionID)

	userQuery := fmt.Sprintf("INSERT INTO %s.users (id, status) VALUES ($1, 'active')", quoteIdentifier(schema))
	if _, err := pool.Exec(t.Context(), userQuery, actorID); err != nil {
		t.Fatal(err)
	}
	setCreator := fmt.Sprintf("UPDATE %s.sessions SET creator_id = $1 WHERE id = $2", quoteIdentifier(schema))
	if _, err := pool.Exec(t.Context(), setCreator, actorID, sessionID); err != nil {
		t.Fatal(err)
	}
	membership := fmt.Sprintf("INSERT INTO %s.workspace_members (workspace_id, user_id, role) VALUES ($1, $2, 'developer')", quoteIdentifier(schema))
	if _, err := pool.Exec(t.Context(), membership, workspaceID, actorID); err != nil {
		t.Fatal(err)
	}

	command := stateCreateRunCommand(210_010, workspaceID, sessionID, "trajectory")
	command.ActorID = actorID
	created, err := store.CreateAuthorizedRun(t.Context(), command)
	if err != nil || !created.Created {
		t.Fatalf("CreateAuthorizedRun() = %+v, %v", created, err)
	}

	result, err := store.ReadUserSessionTrajectory(t.Context(), ReadUserSessionTrajectoryQuery{
		WorkspaceID: workspaceID, SessionID: sessionID, ActorID: actorID,
	})
	if err != nil {
		t.Fatalf("ReadUserSessionTrajectory() error = %v", err)
	}
	if result.Session.ID != sessionID || len(result.Runs) != 1 || result.Runs[0].ID != created.Run.ID ||
		result.PromptPointers[created.Run.ID] != command.Prompt ||
		len(result.Events) != 1 || result.Events[0].RunID != created.Run.ID || result.HasOlderRuns || result.Truncated {
		t.Fatalf("ReadUserSessionTrajectory() = %+v", result)
	}

	result, err = store.ReadUserSessionTrajectory(t.Context(), ReadUserSessionTrajectoryQuery{
		WorkspaceID: workspaceID, SessionID: sessionID, ActorID: actorID,
		Before: &UserSessionTrajectoryRunPosition{RunID: created.Run.ID, RunCreatedAt: created.Run.CreatedAt},
	})
	if err != nil || len(result.Runs) != 1 {
		t.Fatalf("cursor-bound ReadUserSessionTrajectory() = %+v, %v", result, err)
	}

	otherActorID := stateTestUUID(210_003)
	if _, err := store.ReadUserSessionTrajectory(t.Context(), ReadUserSessionTrajectoryQuery{
		WorkspaceID: workspaceID, SessionID: sessionID, ActorID: otherActorID,
	}); !HasStateErrorCode(err, ErrorNotFound) {
		t.Fatalf("cross-creator ReadUserSessionTrajectory() error = %v, want not_found", err)
	}
}
