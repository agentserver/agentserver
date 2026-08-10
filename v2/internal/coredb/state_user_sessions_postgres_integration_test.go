package coredb

import (
	"fmt"
	"testing"
)

func TestPostgreSQLUserSessionLifecycleIsOwnerPrivateAndVersioned(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	workspaceID := stateTestUUID(80_000)
	actorID := stateTestUUID(80_001)
	otherActorID := stateTestUUID(80_002)
	sessionID := stateTestUUID(80_003)
	quotedSchema := quoteIdentifier(schema)

	if _, err := pool.Exec(
		t.Context(),
		fmt.Sprintf("INSERT INTO %s.workspaces (id, status, managed_lark_credential_mode) VALUES ($1, 'active', 'webhook_swap')", quotedSchema),
		workspaceID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(
		t.Context(),
		fmt.Sprintf("INSERT INTO %s.users (id, status) VALUES ($1, 'active'), ($2, 'active')", quotedSchema),
		actorID, otherActorID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(
		t.Context(),
		fmt.Sprintf("INSERT INTO %s.workspace_members (workspace_id, user_id, role) VALUES ($1, $2, 'developer'), ($1, $3, 'developer')", quotedSchema),
		workspaceID, actorID, otherActorID,
	); err != nil {
		t.Fatal(err)
	}

	create := CreateUserSessionCommand{
		WorkspaceID: workspaceID,
		SessionID:   sessionID,
		ActorID:     actorID,
		Title:       "Inspect SG deployment",
	}
	created, err := store.CreateUserSession(t.Context(), create)
	if err != nil || !created.Created || created.Session.Version != 1 ||
		created.Session.CreatorID != actorID || created.Session.Status != UserSessionStatusActive {
		t.Fatalf("CreateUserSession() = %+v, %v", created, err)
	}
	retry, err := store.CreateUserSession(t.Context(), create)
	if err != nil || retry.Created || retry.Session.ID != sessionID || retry.Session.Version != 1 {
		t.Fatalf("idempotent CreateUserSession() = %+v, %v", retry, err)
	}

	conflictingOwner := create
	conflictingOwner.ActorID = otherActorID
	if _, err := store.CreateUserSession(t.Context(), conflictingOwner); !HasStateErrorCode(err, ErrorConflict) {
		t.Fatalf("cross-owner CreateUserSession() error = %v, want conflict", err)
	}
	otherSessions, err := store.ListUserSessions(t.Context(), workspaceID, otherActorID)
	if err != nil || len(otherSessions) != 0 {
		t.Fatalf("other actor ListUserSessions() = %+v, %v", otherSessions, err)
	}
	if _, err := store.GetUserSession(t.Context(), workspaceID, sessionID, otherActorID); !HasStateErrorCode(err, ErrorNotFound) {
		t.Fatalf("other actor GetUserSession() error = %v, want not_found", err)
	}

	updated, err := store.UpdateUserSession(t.Context(), UpdateUserSessionCommand{
		WorkspaceID: workspaceID, SessionID: sessionID, ActorID: actorID,
		Title: "Inspect session creation", ExpectedVersion: 1,
	})
	if err != nil || !updated.Changed || updated.Session.Version != 2 || updated.Session.Title != "Inspect session creation" {
		t.Fatalf("UpdateUserSession() = %+v, %v", updated, err)
	}
	if _, err := store.UpdateUserSession(t.Context(), UpdateUserSessionCommand{
		WorkspaceID: workspaceID, SessionID: sessionID, ActorID: actorID,
		Title: "Stale rename", ExpectedVersion: 1,
	}); !HasStateErrorCode(err, ErrorVersionConflict) {
		t.Fatalf("stale UpdateUserSession() error = %v, want version_conflict", err)
	}
	authorized, err := store.AuthorizeRunSession(t.Context(), workspaceID, sessionID, actorID)
	if err != nil || authorized.SessionVersion != 2 || authorized.ActorID != actorID {
		t.Fatalf("AuthorizeRunSession() = %+v, %v", authorized, err)
	}

	archived, err := store.ArchiveUserSession(t.Context(), ArchiveUserSessionCommand{
		WorkspaceID: workspaceID, SessionID: sessionID, ActorID: actorID, ExpectedVersion: 2,
	})
	if err != nil || !archived.Changed || archived.Session.Status != UserSessionStatusArchived || archived.Session.Version != 3 {
		t.Fatalf("ArchiveUserSession() = %+v, %v", archived, err)
	}
	active, err := store.ListUserSessions(t.Context(), workspaceID, actorID)
	if err != nil || len(active) != 0 {
		t.Fatalf("archived ListUserSessions() = %+v, %v", active, err)
	}
	retained, err := store.GetUserSession(t.Context(), workspaceID, sessionID, actorID)
	if err != nil || retained.Status != UserSessionStatusArchived || retained.Version != 3 {
		t.Fatalf("archived GetUserSession() = %+v, %v", retained, err)
	}
	if _, err := store.AuthorizeRunSession(t.Context(), workspaceID, sessionID, actorID); !HasStateErrorCode(err, ErrorNotFound) {
		t.Fatalf("archived AuthorizeRunSession() error = %v, want not_found", err)
	}
}
