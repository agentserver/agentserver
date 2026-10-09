package coredb

import (
	"fmt"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/managedsandboxprofile"
	"github.com/agentserver/agentserver/v2/internal/workspacerepository"
)

func TestPostgreSQLSessionRepositoryInheritanceIsImmutable(t *testing.T) {
	f := newWorkspaceCredentialAuthorizationPostgresFixture(t, 982_000)
	profile := validManagedEnvironmentProfile()
	profile.WorkspaceID = f.workspaceID
	profile.ExecutorID = stateTestUUID(982_010)
	profile.EnvironmentID = stateTestUUID(982_011)
	profile.BackendKind = DispatchTargetKubernetes
	if _, err := f.pool.Exec(t.Context(), fmt.Sprintf("INSERT INTO %s.executors (id,workspace_id,status) VALUES ($1,$2,'enrolling')", quoteIdentifier(f.schema)), profile.ExecutorID, profile.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := insertManagedEnvironmentProfile(t.Context(), tx, quoteIdentifier(f.schema), profile); err != nil {
		tx.Rollback(t.Context())
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	catalog, err := managedsandboxprofile.NewCatalog("sg", []managedsandboxprofile.Binding{{Region: "sg", EnvironmentID: profile.EnvironmentID}})
	if err != nil {
		t.Fatal(err)
	}
	f.store = f.store.WithManagedSandboxCatalog(catalog)
	if _, err := f.pool.Exec(t.Context(), fmt.Sprintf("INSERT INTO %s.workspace_managed_sandbox_settings (workspace_id,region,version,updated_by) VALUES ($1,'sg',1,$2)", quoteIdentifier(f.schema)), f.workspaceID, f.ownerID); err != nil {
		t.Fatal(err)
	}
	source := &workspacerepository.Source{URL: "https://code.byted.org/tce/rtm-aihub", Ref: "main", WorkingDirectory: "wiki"}
	if _, err := f.store.UpdateWorkspaceRepository(t.Context(), UpdateWorkspaceRepositoryCommand{WorkspaceID: f.workspaceID, ActorID: f.ownerID, Source: source, AuditEventID: stateTestUUID(982_020)}); err != nil {
		t.Fatal(err)
	}
	create := CreateUserSessionCommand{WorkspaceID: f.workspaceID, SessionID: stateTestUUID(982_030), ActorID: f.ownerID, Title: "New session"}
	first, err := f.store.CreateUserSession(t.Context(), create)
	if err != nil || !first.Created || first.Session.WorkingEnvironmentID != profile.EnvironmentID || first.Session.WorkingDirectory != "wiki" {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	binding, err := f.store.GetUserSessionRepository(t.Context(), f.workspaceID, create.SessionID, f.ownerID)
	if err != nil || binding == nil || binding.CheckoutID != create.SessionID || binding.SourceVersion != 1 || binding.Source.URL != source.URL+".git" {
		t.Fatalf("binding=%+v err=%v", binding, err)
	}
	if _, err := f.store.GetUserSessionRepository(t.Context(), f.workspaceID, create.SessionID, f.secondOwnerID); err == nil {
		t.Fatal("another session owner read repository")
	}
	next := *source
	next.WorkingDirectory = "src"
	next.Ref = "feature"
	if _, err := f.store.UpdateWorkspaceRepository(t.Context(), UpdateWorkspaceRepositoryCommand{WorkspaceID: f.workspaceID, ActorID: f.ownerID, Source: &next, ExpectedVersion: 1, AuditEventID: stateTestUUID(982_021)}); err != nil {
		t.Fatal(err)
	}
	retry, err := f.store.CreateUserSession(t.Context(), create)
	if err != nil || retry.Created || retry.Session.WorkingDirectory != "wiki" {
		t.Fatal("default change rewrote existing session", err)
	}
	unchanged, err := f.store.GetUserSessionRepository(t.Context(), f.workspaceID, create.SessionID, f.ownerID)
	if err != nil || *unchanged != *binding {
		t.Fatal("immutable source snapshot changed")
	}
	create.SessionID = stateTestUUID(982_031)
	second, err := f.store.CreateUserSession(t.Context(), create)
	if err != nil || second.Session.WorkingDirectory != "src" {
		t.Fatal("new session did not inherit new default", err)
	}
	updated, err := f.store.UpdateUserSessionWorkingDirectory(t.Context(), UpdateUserSessionWorkingDirectoryCommand{WorkspaceID: f.workspaceID, SessionID: create.SessionID, ActorID: f.ownerID, EnvironmentID: profile.EnvironmentID, WorkingDirectory: "src/tools", ExpectedWorkingDirectoryVersion: 1})
	if err != nil || !updated.Changed || updated.Session.WorkingDirectoryVersion != 2 {
		t.Fatalf("override=%+v err=%v", updated, err)
	}
	if _, err := f.store.UpdateUserSessionWorkingDirectory(t.Context(), UpdateUserSessionWorkingDirectoryCommand{WorkspaceID: f.workspaceID, SessionID: create.SessionID, ActorID: f.ownerID, WorkingDirectory: ".", ExpectedWorkingDirectoryVersion: 2}); err == nil {
		t.Fatal("detached persistent repository through generic directory route")
	}
	runCommand := stateCreateRunCommand(983_000, f.workspaceID, create.SessionID, "repository-run")
	runCommand.ActorID = f.ownerID
	run, err := f.store.CreateAuthorizedRun(t.Context(), runCommand)
	if err != nil {
		t.Fatal("create repository run", err)
	}
	if _, err := f.store.UpdateUserSessionWorkingDirectory(t.Context(), UpdateUserSessionWorkingDirectoryCommand{WorkspaceID: f.workspaceID, SessionID: create.SessionID, ActorID: f.ownerID, EnvironmentID: profile.EnvironmentID, WorkingDirectory: "src/next", ExpectedWorkingDirectoryVersion: 2}); err != nil {
		t.Fatal(err)
	}
	read, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer read.Rollback(t.Context())
	frozen, err := f.store.readRunWorkspaceBinding(t.Context(), read, "test", run.Run.ID)
	if err != nil || frozen == nil || frozen.RepositoryID != create.SessionID || frozen.WorkingDirectory != "src/tools" || frozen.WorkingDirectoryVersion != 2 {
		t.Fatalf("frozen=%+v err=%v", frozen, err)
	}
	var raw []byte
	if err := read.QueryRow(t.Context(), fmt.Sprintf("SELECT repository_binding FROM %s.run_launch_states WHERE run_id=$1", quoteIdentifier(f.schema)), run.Run.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	frozenSource, err := decodeRepositoryBinding(raw)
	if err != nil || frozenSource == nil || frozenSource.SourceVersion != 2 || frozenSource.Source.Ref != "feature" {
		t.Fatalf("frozen source=%+v err=%v", frozenSource, err)
	}
	repeated, err := f.store.CreateAuthorizedRun(t.Context(), runCommand)
	if err != nil || repeated.Created || repeated.Run.ID != run.Run.ID {
		t.Fatal("directory change broke run idempotency", err)
	}
}
