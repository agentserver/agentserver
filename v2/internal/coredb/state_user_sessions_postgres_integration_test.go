package coredb

import (
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/runmanifest"
	"github.com/agentserver/agentserver/v2/internal/workspaceauthority"
	"github.com/jackc/pgx/v5/pgxpool"
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
		created.Session.PermissionMode != runmanifest.CodexPermissionModeReadOnly || created.Session.PermissionModeVersion != 1 ||
		created.Session.WorkingEnvironmentID != "" || created.Session.WorkingDirectory != "." || created.Session.WorkingDirectoryVersion != 1 ||
		created.Session.CreatorID != actorID || created.Session.Status != UserSessionStatusActive {
		t.Fatalf("CreateUserSession() = %+v, %v", created, err)
	}
	retry, err := store.CreateUserSession(t.Context(), create)
	if err != nil || retry.Created || retry.Session.ID != sessionID || retry.Session.Version != 1 ||
		retry.Session.PermissionMode != runmanifest.CodexPermissionModeReadOnly || retry.Session.PermissionModeVersion != 1 ||
		retry.Session.WorkingDirectory != "." || retry.Session.WorkingDirectoryVersion != 1 {
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
	modeUpdated, err := store.UpdateUserSessionPermissionMode(t.Context(), UpdateUserSessionPermissionModeCommand{
		WorkspaceID: workspaceID, SessionID: sessionID, ActorID: actorID,
		PermissionMode: runmanifest.CodexPermissionModeAuto, ExpectedPermissionModeVersion: 1,
	})
	if err != nil || !modeUpdated.Changed || modeUpdated.Session.Version != 2 ||
		modeUpdated.Session.PermissionMode != runmanifest.CodexPermissionModeAuto || modeUpdated.Session.PermissionModeVersion != 2 {
		t.Fatalf("UpdateUserSessionPermissionMode() = %+v, %v", modeUpdated, err)
	}
	modeRetry, err := store.UpdateUserSessionPermissionMode(t.Context(), UpdateUserSessionPermissionModeCommand{
		WorkspaceID: workspaceID, SessionID: sessionID, ActorID: actorID,
		PermissionMode: runmanifest.CodexPermissionModeAuto, ExpectedPermissionModeVersion: 2,
	})
	if err != nil || modeRetry.Changed || modeRetry.Session.Version != 2 || modeRetry.Session.PermissionModeVersion != 2 {
		t.Fatalf("idempotent UpdateUserSessionPermissionMode() = %+v, %v", modeRetry, err)
	}
	if _, err := store.UpdateUserSessionPermissionMode(t.Context(), UpdateUserSessionPermissionModeCommand{
		WorkspaceID: workspaceID, SessionID: sessionID, ActorID: actorID,
		PermissionMode: runmanifest.CodexPermissionModeFullAccess, ExpectedPermissionModeVersion: 1,
	}); !HasStateErrorCode(err, ErrorVersionConflict) {
		t.Fatalf("stale UpdateUserSessionPermissionMode() error = %v, want version_conflict", err)
	}
	if _, err := pool.Exec(t.Context(), fmt.Sprintf("UPDATE %s.workspace_members SET role = 'viewer' WHERE workspace_id = $1 AND user_id = $2", quotedSchema), workspaceID, actorID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateUserSessionPermissionMode(t.Context(), UpdateUserSessionPermissionModeCommand{
		WorkspaceID: workspaceID, SessionID: sessionID, ActorID: actorID,
		PermissionMode: runmanifest.CodexPermissionModeFullAccess, ExpectedPermissionModeVersion: 2,
	}); !HasStateErrorCode(err, ErrorForbidden) {
		t.Fatalf("viewer UpdateUserSessionPermissionMode() error = %v, want forbidden", err)
	}
	if _, err := pool.Exec(t.Context(), fmt.Sprintf("UPDATE %s.workspace_members SET role = 'developer' WHERE workspace_id = $1 AND user_id = $2", quotedSchema), workspaceID, actorID); err != nil {
		t.Fatal(err)
	}
	authorized, err := store.AuthorizeRunSession(t.Context(), workspaceID, sessionID, actorID)
	if err != nil || authorized.SessionVersion != 2 || authorized.ActorID != actorID ||
		authorized.PermissionMode != runmanifest.CodexPermissionModeAuto || authorized.PermissionModeVersion != 2 {
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

func TestPostgreSQLWorkingDirectoryCASAndRunLaunchFreeze(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	workspaceID := stateTestUUID(81_000)
	actorID := stateTestUUID(81_001)
	sessionID := stateTestUUID(81_002)
	executorID := stateTestUUID(81_003)
	environmentID := stateTestUUID(81_004)
	otherWorkspaceID := stateTestUUID(81_005)
	otherExecutorID := stateTestUUID(81_006)
	otherEnvironmentID := stateTestUUID(81_007)
	quotedSchema := quoteIdentifier(schema)
	insertStateTestSession(t, pool, schema, workspaceID, sessionID)
	if _, err := pool.Exec(t.Context(), fmt.Sprintf("INSERT INTO %s.users (id, status) VALUES ($1, 'active')", quotedSchema), actorID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), fmt.Sprintf("UPDATE %s.sessions SET creator_id = $1 WHERE id = $2", quotedSchema), actorID, sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), fmt.Sprintf("INSERT INTO %s.workspace_members (workspace_id, user_id, role) VALUES ($1, $2, 'developer')", quotedSchema), workspaceID, actorID); err != nil {
		t.Fatal(err)
	}
	installSessionWorkspaceEnvironment(t, pool, schema, workspaceID, executorID, environmentID, `{"kind":"local","root":"/workspace/projects"}`)
	installSessionWorkspaceEnvironment(t, pool, schema, otherWorkspaceID, otherExecutorID, otherEnvironmentID, `{"kind":"local","root":"/workspace/other"}`)

	updated, err := store.UpdateUserSessionWorkingDirectory(t.Context(), UpdateUserSessionWorkingDirectoryCommand{
		WorkspaceID: workspaceID, SessionID: sessionID, ActorID: actorID,
		EnvironmentID: environmentID, WorkingDirectory: "rtm-aihub", ExpectedWorkingDirectoryVersion: 1,
	})
	if err != nil || !updated.Changed || updated.Session.Version != 1 || updated.Session.WorkingDirectoryVersion != 2 ||
		updated.Session.WorkingEnvironmentID != environmentID || updated.Session.WorkingDirectory != "rtm-aihub" {
		t.Fatalf("working-directory update = %+v, %v", updated, err)
	}
	authorized, err := store.AuthorizeRunSession(t.Context(), workspaceID, sessionID, actorID)
	if err != nil || authorized.WorkingEnvironmentID != environmentID || authorized.WorkingDirectory != "rtm-aihub" || authorized.WorkingDirectoryVersion != 2 {
		t.Fatalf("AuthorizeRunSession workspace projection = %+v, %v", authorized, err)
	}
	if _, err := store.UpdateUserSessionWorkingDirectory(t.Context(), UpdateUserSessionWorkingDirectoryCommand{
		WorkspaceID: workspaceID, SessionID: sessionID, ActorID: actorID,
		EnvironmentID: environmentID, WorkingDirectory: "stale", ExpectedWorkingDirectoryVersion: 1,
	}); !HasStateErrorCode(err, ErrorVersionConflict) {
		t.Fatalf("stale working-directory update = %v, want version_conflict", err)
	}
	if _, err := store.UpdateUserSessionWorkingDirectory(t.Context(), UpdateUserSessionWorkingDirectoryCommand{
		WorkspaceID: workspaceID, SessionID: sessionID, ActorID: actorID,
		EnvironmentID: otherEnvironmentID, WorkingDirectory: "rtm-aihub", ExpectedWorkingDirectoryVersion: 2,
	}); !HasStateErrorCode(err, ErrorNotFound) {
		t.Fatalf("cross-workspace environment update = %v, want not_found", err)
	}
	if _, err := pool.Exec(t.Context(), fmt.Sprintf("UPDATE %s.executor_environments SET status = 'disabled' WHERE id = $1", quotedSchema), environmentID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateUserSessionWorkingDirectory(t.Context(), UpdateUserSessionWorkingDirectoryCommand{
		WorkspaceID: workspaceID, SessionID: sessionID, ActorID: actorID,
		EnvironmentID: environmentID, WorkingDirectory: "disabled", ExpectedWorkingDirectoryVersion: 2,
	}); !HasStateErrorCode(err, ErrorInvalidState) {
		t.Fatalf("disabled environment update = %v, want invalid_state", err)
	}
	if _, err := pool.Exec(t.Context(), fmt.Sprintf("UPDATE %s.executor_environments SET status = 'online' WHERE id = $1", quotedSchema), environmentID); err != nil {
		t.Fatal(err)
	}

	create := stateCreateRunCommand(81_010, workspaceID, sessionID, "working-directory-freeze")
	create.ActorID = actorID
	create.ExpectedSessionVersion = 1
	staleCreate := create
	staleCreate.ExpectedWorkingDirectoryVersion = 1
	if _, err := store.CreateRun(t.Context(), staleCreate); !HasStateErrorCode(err, ErrorVersionConflict) {
		t.Fatalf("CreateRun with stale working-directory CAS = %v, want version_conflict", err)
	}
	create.ExpectedWorkingDirectoryVersion = 2
	created, err := store.CreateRun(t.Context(), create)
	if err != nil || !created.Created || created.SessionVersion != 2 {
		t.Fatalf("CreateRun with workspace binding = %+v, %v", created, err)
	}
	var frozenEnvironmentID *string
	var frozenEnvironmentVersion, frozenWorkingDirectoryVersion int64
	var frozenRoot []byte
	var frozenDirectory string
	if err := pool.QueryRow(t.Context(), fmt.Sprintf(`
SELECT workspace_environment_id::text, workspace_environment_version, workspace_root_sha256,
       workspace_working_directory, workspace_working_directory_version
FROM %s.run_launch_states WHERE run_id = $1`, quotedSchema), created.Run.ID).Scan(
		&frozenEnvironmentID, &frozenEnvironmentVersion, &frozenRoot, &frozenDirectory, &frozenWorkingDirectoryVersion,
	); err != nil {
		t.Fatal(err)
	}
	if frozenEnvironmentID == nil || *frozenEnvironmentID != environmentID || frozenEnvironmentVersion != 1 ||
		frozenDirectory != "rtm-aihub" || frozenWorkingDirectoryVersion != 2 || len(frozenRoot) != sha256.Size {
		t.Fatalf("frozen workspace launch authority = %v/%d/%x/%q/%d", frozenEnvironmentID, frozenEnvironmentVersion, frozenRoot, frozenDirectory, frozenWorkingDirectoryVersion)
	}
	var rootDescriptor []byte
	if err := pool.QueryRow(t.Context(), fmt.Sprintf("SELECT root_descriptor::text FROM %s.executor_environments WHERE id = $1", quotedSchema), environmentID).Scan(&rootDescriptor); err != nil {
		t.Fatal(err)
	}
	digest, err := workspaceauthority.RootDescriptorSHA256(rootDescriptor)
	if err != nil || string(frozenRoot) != string(digest[:]) {
		t.Fatalf("frozen root digest = %x, current descriptor digest = %x, error = %v", frozenRoot, digest, err)
	}

	changed, err := store.UpdateUserSessionWorkingDirectory(t.Context(), UpdateUserSessionWorkingDirectoryCommand{
		WorkspaceID: workspaceID, SessionID: sessionID, ActorID: actorID,
		EnvironmentID: environmentID, WorkingDirectory: "other-project", ExpectedWorkingDirectoryVersion: 2,
	})
	if err != nil || !changed.Changed || changed.Session.WorkingDirectoryVersion != 3 {
		t.Fatalf("post-run working-directory update = %+v, %v", changed, err)
	}
	var stillFrozenDirectory string
	if err := pool.QueryRow(t.Context(), fmt.Sprintf("SELECT workspace_working_directory FROM %s.run_launch_states WHERE run_id = $1", quotedSchema), created.Run.ID).Scan(&stillFrozenDirectory); err != nil {
		t.Fatal(err)
	}
	if stillFrozenDirectory != "rtm-aihub" {
		t.Fatalf("active run workspace directory changed from frozen value: %q", stillFrozenDirectory)
	}
	unbound, err := store.UpdateUserSessionWorkingDirectory(t.Context(), UpdateUserSessionWorkingDirectoryCommand{
		WorkspaceID: workspaceID, SessionID: sessionID, ActorID: actorID,
		WorkingDirectory: ".", ExpectedWorkingDirectoryVersion: 3,
	})
	if err != nil || !unbound.Changed || unbound.Session.WorkingEnvironmentID != "" || unbound.Session.WorkingDirectory != "." || unbound.Session.WorkingDirectoryVersion != 4 {
		t.Fatalf("unbound working-directory update = %+v, %v", unbound, err)
	}

	// A user-facing idempotency retry recovers the committed run even after the
	// mutable session binding has moved (and even if its old environment is no
	// longer available). The retry never rewrites the frozen launch row.
	if _, err := pool.Exec(t.Context(), fmt.Sprintf("UPDATE %s.executor_environments SET status = 'disabled' WHERE id = $1", quotedSchema), environmentID); err != nil {
		t.Fatal(err)
	}
	retry, err := store.CreateAuthorizedRun(t.Context(), create)
	if err != nil || retry.Created || retry.Run.ID != created.Run.ID {
		t.Fatalf("idempotent authorized retry after environment disable = %+v, %v", retry, err)
	}
}

func TestPostgreSQLWorkingDirectoryUpdateAndAuthorizedCreateDoNotMixAuthority(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	workspaceID := stateTestUUID(82_000)
	actorID := stateTestUUID(82_001)
	sessionID := stateTestUUID(82_002)
	executorID := stateTestUUID(82_003)
	environmentID := stateTestUUID(82_004)
	quotedSchema := quoteIdentifier(schema)
	insertStateTestSession(t, pool, schema, workspaceID, sessionID)
	if _, err := pool.Exec(t.Context(), fmt.Sprintf("INSERT INTO %s.users (id, status) VALUES ($1, 'active')", quotedSchema), actorID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), fmt.Sprintf("UPDATE %s.sessions SET creator_id = $1 WHERE id = $2", quotedSchema), actorID, sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), fmt.Sprintf("INSERT INTO %s.workspace_members (workspace_id, user_id, role) VALUES ($1, $2, 'developer')", quotedSchema), workspaceID, actorID); err != nil {
		t.Fatal(err)
	}
	installSessionWorkspaceEnvironment(t, pool, schema, workspaceID, executorID, environmentID, `{"kind":"local","root":"/workspace/projects"}`)
	initial, err := store.UpdateUserSessionWorkingDirectory(t.Context(), UpdateUserSessionWorkingDirectoryCommand{
		WorkspaceID: workspaceID, SessionID: sessionID, ActorID: actorID,
		EnvironmentID: environmentID, WorkingDirectory: "initial", ExpectedWorkingDirectoryVersion: 1,
	})
	if err != nil || !initial.Changed || initial.Session.WorkingDirectoryVersion != 2 {
		t.Fatalf("initial workspace binding = %+v, %v", initial, err)
	}

	create := stateCreateRunCommand(82_010, workspaceID, sessionID, "working-directory-race")
	create.ActorID = actorID
	create.ExpectedSessionVersion = 0
	create.ExpectedWorkingDirectoryVersion = 2
	update := UpdateUserSessionWorkingDirectoryCommand{
		WorkspaceID: workspaceID, SessionID: sessionID, ActorID: actorID,
		EnvironmentID: environmentID, WorkingDirectory: "updated", ExpectedWorkingDirectoryVersion: 2,
	}
	type createOutcome struct {
		result CreateRunResult
		err    error
	}
	type updateOutcome struct {
		result UpdateUserSessionWorkingDirectoryResult
		err    error
	}
	createCh := make(chan createOutcome, 1)
	updateCh := make(chan updateOutcome, 1)
	start := make(chan struct{})
	go func() {
		<-start
		result, err := store.CreateAuthorizedRun(t.Context(), create)
		createCh <- createOutcome{result: result, err: err}
	}()
	go func() {
		<-start
		result, err := store.UpdateUserSessionWorkingDirectory(t.Context(), update)
		updateCh <- updateOutcome{result: result, err: err}
	}()
	close(start)
	var created createOutcome
	var moved updateOutcome
	select {
	case created = <-createCh:
	case <-time.After(15 * time.Second):
		t.Fatal("CreateAuthorizedRun did not finish during working-directory race")
	}
	select {
	case moved = <-updateCh:
	case <-time.After(15 * time.Second):
		t.Fatal("UpdateUserSessionWorkingDirectory did not finish during run race")
	}
	if moved.err != nil || !moved.result.Changed || moved.result.Session.WorkingDirectory != "updated" || moved.result.Session.WorkingDirectoryVersion != 3 {
		t.Fatalf("concurrent working-directory update = %+v, %v", moved.result, moved.err)
	}
	if created.err == nil {
		if !created.result.Created {
			t.Fatalf("concurrent authorized create unexpectedly replayed: %+v", created.result)
		}
		var frozen string
		if err := pool.QueryRow(t.Context(), fmt.Sprintf("SELECT workspace_working_directory FROM %s.run_launch_states WHERE run_id = $1", quotedSchema), created.result.Run.ID).Scan(&frozen); err != nil {
			t.Fatal(err)
		}
		if frozen != "initial" {
			t.Fatalf("run froze mixed workspace directory %q, want initial", frozen)
		}
		return
	}
	if !HasStateErrorCode(created.err, ErrorVersionConflict) {
		t.Fatalf("concurrent authorized create error = %v, want working-directory version conflict", created.err)
	}
	retry := create
	retry.RunID = stateTestUUID(82_020)
	retry.ExpectedWorkingDirectoryVersion = 3
	retried, err := store.CreateAuthorizedRun(t.Context(), retry)
	if err != nil || !retried.Created {
		t.Fatalf("refreshed authorized create = %+v, %v", retried, err)
	}
	var frozen string
	if err := pool.QueryRow(t.Context(), fmt.Sprintf("SELECT workspace_working_directory FROM %s.run_launch_states WHERE run_id = $1", quotedSchema), retried.Run.ID).Scan(&frozen); err != nil {
		t.Fatal(err)
	}
	if frozen != "updated" {
		t.Fatalf("refreshed run froze workspace directory %q, want updated", frozen)
	}
}

func installSessionWorkspaceEnvironment(t *testing.T, pool *pgxpool.Pool, schema, workspaceID, executorID, environmentID, descriptor string) {
	t.Helper()
	quotedSchema := quoteIdentifier(schema)
	machineHash := sha256.Sum256([]byte(executorID + "-machine"))
	runtimeHash := sha256.Sum256([]byte(executorID + "-runtime"))
	protocolHash := sha256.Sum256([]byte(executorID + "-protocol"))
	if _, err := pool.Exec(t.Context(), fmt.Sprintf("INSERT INTO %s.workspaces (id, status, managed_lark_credential_mode) VALUES ($1, 'active', 'webhook_swap') ON CONFLICT (id) DO NOTHING", quotedSchema), workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), fmt.Sprintf(`
INSERT INTO %s.executors
    (id, workspace_id, status, machine_key_sha256, agentx_version, runtime_manifest_sha256, exec_protocol_source_sha256)
VALUES ($1, $2, 'offline', $3, 'test-agentx', $4, $5)`, quotedSchema), executorID, workspaceID, machineHash[:], runtimeHash[:], protocolHash[:]); err != nil {
		t.Fatal(err)
	}
	ownerPolicy := sha256.Sum256([]byte(executorID + "-policy"))
	codex := sha256.Sum256([]byte(executorID + "-codex"))
	if _, err := pool.Exec(t.Context(), fmt.Sprintf(`
INSERT INTO %s.executor_environments
    (id, executor_id, root_descriptor, owner_policy_sha256, platform, codex_release, codex_commit,
     codex_sha256, outer_profile_version, process_methods, insecure_dev, status)
VALUES ($1, $2, $3::jsonb, $4, 'linux-arm64', '0.146.0', $5, $6, 'process-v1',
        ARRAY['process/start','process/read','process/write','process/terminate']::text[], true, 'online')`, quotedSchema),
		environmentID, executorID, descriptor, ownerPolicy[:], fmt.Sprintf("%040x", 81000), codex[:]); err != nil {
		t.Fatal(err)
	}
}
