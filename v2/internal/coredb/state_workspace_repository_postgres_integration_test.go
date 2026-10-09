package coredb

import (
	"bytes"
	"fmt"
	"sync"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/corecredentials"
	"github.com/agentserver/agentserver/v2/internal/workspacerepository"
)

func TestPostgreSQLWorkspaceRepositoryDefaultsAndCAS(t *testing.T) {
	f := newWorkspaceCredentialAuthorizationPostgresFixture(t, 980_000)
	ctx := t.Context()
	initial, err := f.store.GetWorkspaceRepository(ctx, f.workspaceID, f.ownerID)
	if err != nil || initial.Version != 0 || initial.Source != nil || initial.UpdatedAt != nil {
		t.Fatalf("initial=%+v err=%v", initial, err)
	}
	if _, err := f.store.GetWorkspaceRepository(ctx, f.workspaceID, stateTestUUID(980_099)); err == nil {
		t.Fatal("nonmember read accepted")
	}
	q := fmt.Sprintf("UPDATE %s.workspace_members SET role='developer' WHERE workspace_id=$1 AND user_id=$2", quoteIdentifier(f.schema))
	if _, err := f.pool.Exec(ctx, q, f.workspaceID, f.secondOwnerID); err != nil {
		t.Fatal(err)
	}
	source := &workspacerepository.Source{URL: "https://code.byted.org/tce/rtm-aihub"}
	command := UpdateWorkspaceRepositoryCommand{WorkspaceID: f.workspaceID, ActorID: f.ownerID, Source: source, AuditEventID: stateTestUUID(980_010)}
	denied := command
	denied.ActorID = f.secondOwnerID
	if _, err := f.store.UpdateWorkspaceRepository(ctx, denied); err == nil {
		t.Fatal("developer update accepted")
	}
	result, err := f.store.UpdateWorkspaceRepository(ctx, command)
	if err != nil || !result.Changed || result.Setting.Version != 1 || result.Setting.Source.URL != source.URL+".git" || result.Setting.Source.WorkingDirectory != "." {
		t.Fatalf("update=%+v err=%v", result, err)
	}
	if source.WorkingDirectory != "" {
		t.Fatal("caller source was mutated")
	}
	command.ExpectedVersion = 1
	repeat, err := f.store.UpdateWorkspaceRepository(ctx, command)
	if err != nil || repeat.Changed || repeat.Setting.Version != 1 {
		t.Fatalf("repeat=%+v err=%v", repeat, err)
	}
	command.ExpectedVersion = 0
	if _, err := f.store.UpdateWorkspaceRepository(ctx, command); !HasStateErrorCode(err, ErrorVersionConflict) {
		t.Fatalf("stale CAS=%v", err)
	}
	// Concurrent saves of the same revision have exactly one winner.
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	for n := 0; n < 2; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			c := command
			c.ExpectedVersion = 1
			c.AuditEventID = stateTestUUID(980_020 + n)
			c.Source = &workspacerepository.Source{URL: source.URL, Ref: fmt.Sprintf("branch-%d", n), WorkingDirectory: "src"}
			_, err := f.store.UpdateWorkspaceRepository(ctx, c)
			errors <- err
		}(n)
	}
	wg.Wait()
	close(errors)
	wins, conflicts := 0, 0
	for err := range errors {
		if err == nil {
			wins++
		} else if HasStateErrorCode(err, ErrorVersionConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("wins=%d conflicts=%d", wins, conflicts)
	}
	command.ExpectedVersion = 2
	command.Source = nil
	command.AuditEventID = stateTestUUID(980_030)
	cleared, err := f.store.UpdateWorkspaceRepository(ctx, command)
	if err != nil || !cleared.Changed || cleared.Setting.Source != nil || cleared.Setting.Version != 3 {
		t.Fatalf("clear=%+v err=%v", cleared, err)
	}
	if _, err := f.store.GetWorkspaceRepository(ctx, f.workspaceID, f.secondOwnerID); err != nil {
		t.Fatal("developer cannot read", err)
	}
	var events int
	if err := f.pool.QueryRow(ctx, fmt.Sprintf("SELECT count(*) FROM %s.workspace_repository_setting_events WHERE workspace_id=$1", quoteIdentifier(f.schema)), f.workspaceID).Scan(&events); err != nil || events != 3 {
		t.Fatalf("events=%d err=%v", events, err)
	}
}

func TestPostgreSQLWorkspaceRepositoryCredentialScope(t *testing.T) {
	f := newWorkspaceCredentialAuthorizationPostgresFixture(t, 981_000)
	for n, tc := range []struct {
		kind, scope string
		want        bool
	}{
		{"git", corecredentials.OwnerScopeWorkspace, true},
		{"git", corecredentials.OwnerScopeUser, false},
		{"bytecloud", corecredentials.OwnerScopeWorkspace, false},
	} {
		id := stateTestUUID(981_010 + n)
		c := CreateWorkspaceCredentialBindingCommand{ID: id, WorkspaceID: f.workspaceID, ActorID: f.ownerID, Kind: tc.kind, DisplayName: fmt.Sprintf("test-%d", n), OwnerScope: tc.scope, AuthType: "https-token", PublicMetadata: []byte(`{}`), SealedSecret: bytes.Repeat([]byte{1}, 96), SealingKeyID: "test-key"}
		if tc.scope == corecredentials.OwnerScopeUser {
			c.OwnerUserID = f.ownerID
		}
		if _, err := f.store.CreateWorkspaceCredentialBinding(t.Context(), c); err != nil {
			t.Fatal(err)
		}
		version := int64(0)
		if n > 0 {
			version = 1
		}
		_, err := f.store.UpdateWorkspaceRepository(t.Context(), UpdateWorkspaceRepositoryCommand{WorkspaceID: f.workspaceID, ActorID: f.ownerID, ExpectedVersion: version, AuditEventID: stateTestUUID(981_020 + n), Source: &workspacerepository.Source{URL: "https://code.byted.org/tce/rtm-aihub", WorkingDirectory: ".", CredentialBindingID: id}})
		if (err == nil) != tc.want {
			t.Fatalf("kind=%s scope=%s err=%v", tc.kind, tc.scope, err)
		}
	}
	// A referenced credential must still be active even for an otherwise no-op
	// settings save. Never silently substitute the workspace's other default.
	id := stateTestUUID(981_010)
	if _, err := f.pool.Exec(t.Context(), fmt.Sprintf("UPDATE %s.workspace_credential_bindings SET status='revoked' WHERE id=$1", quoteIdentifier(f.schema)), id); err != nil {
		t.Fatal(err)
	}
	_, err := f.store.UpdateWorkspaceRepository(t.Context(), UpdateWorkspaceRepositoryCommand{WorkspaceID: f.workspaceID, ActorID: f.ownerID, ExpectedVersion: 1, AuditEventID: stateTestUUID(981_030), Source: &workspacerepository.Source{URL: "https://code.byted.org/tce/rtm-aihub", WorkingDirectory: ".", CredentialBindingID: id}})
	if !HasStateErrorCode(err, ErrorInvalidArgument) {
		t.Fatalf("revoked binding=%v", err)
	}
}
