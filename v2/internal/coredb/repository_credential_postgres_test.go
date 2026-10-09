package coredb

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/workspacerepository"
)

func TestPostgreSQLRepositoryCredentialRequiresFrozenLiveAttempt(t *testing.T) {
	f := newManagedCredentialPostgresFixture(t, 985000, DispatchTargetKubernetes)
	q := quoteIdentifier(f.schema)
	ctx := t.Context()
	key := stateTestUUID(985600)
	_, err := f.pool.Exec(ctx, fmt.Sprintf(`INSERT INTO %s.workspace_credential_bindings (id,workspace_id,kind,display_name,owner_scope,auth_type,sealed_secret,sealing_key_id) VALUES ($1,$2,'git','test Git','workspace','https-token',$3,'test-key')`, q), key, f.running.Run.WorkspaceID, bytes.Repeat([]byte{1}, 96))
	if err != nil {
		t.Fatal(err)
	}
	b := workspacerepository.Binding{CheckoutID: f.running.Run.SessionID, Source: workspacerepository.Source{URL: "https://code.byted.org/tce/rtm-aihub.git", WorkingDirectory: ".", CredentialBindingID: key}, SourceVersion: 1, Region: "sg", EnvironmentID: f.sandbox.EnvironmentID, ManagedSettingVersion: 1}
	raw, _ := json.Marshal(b)
	_, err = f.pool.Exec(ctx, fmt.Sprintf(`UPDATE %s.run_launch_states SET repository_binding=$2,workspace_environment_id=$3,workspace_environment_version=1,workspace_root_sha256=$4,workspace_working_directory='.',workspace_working_directory_version=1 WHERE run_id=$1`, q), f.running.Run.ID, raw, f.sandbox.EnvironmentID, bytes.Repeat([]byte{2}, 32))
	if err != nil {
		t.Fatal(err)
	}
	r := corecontract.ResolveRepositoryCredentialRequest{BindingID: key, EnvironmentID: b.EnvironmentID, RunID: f.running.Run.ID, RunAttemptID: f.running.Attempt.ID, HolderID: f.running.Attempt.HolderID, Operation: corecontract.EgressCredentialOperation{WorkspaceID: f.running.Run.WorkspaceID, SessionID: f.running.Run.SessionID, ActorID: f.running.Run.ActorID, EnvironmentID: b.EnvironmentID, RunID: f.running.Run.ID, RunAttemptID: f.running.Attempt.ID, RunAttemptGeneration: f.running.Attempt.Generation, SandboxID: f.sandbox.ID, TargetGeneration: f.sandbox.Generation}}
	if err := f.store.AuthorizeRepositoryCredential(ctx, r); err != nil {
		t.Fatal("valid frozen repository request rejected", err)
	}
	for _, field := range []string{"workspace", "actor", "attempt", "generation", "holder", "sandbox", "binding", "version", "duplicate-run", "tool-operation"} {
		bad := r
		switch field {
		case "workspace":
			bad.Operation.WorkspaceID = stateTestUUID(986000)
		case "actor":
			bad.Operation.ActorID = stateTestUUID(986000)
		case "attempt":
			bad.Operation.RunAttemptID = stateTestUUID(986000)
			bad.RunAttemptID = bad.Operation.RunAttemptID
		case "generation":
			bad.Operation.RunAttemptGeneration++
		case "holder":
			bad.HolderID = "other-holder"
		case "sandbox":
			bad.Operation.SandboxID = stateTestUUID(986000)
		case "binding":
			bad.BindingID = stateTestUUID(986000)
		case "version":
			bad.ExpectedCredentialVersion = 2
		case "duplicate-run":
			bad.RunID = stateTestUUID(986000)
		case "tool-operation":
			bad.Operation.OperationID = stateTestUUID(986000)
		}
		if err := f.store.AuthorizeRepositoryCredential(ctx, bad); err == nil {
			t.Fatalf("accepted invalid %s", field)
		}
	}
	// The same preflight works before turn/started: it relies on a live leased
	// attempt, not fabricated process_start rows.
	if _, err := f.pool.Exec(ctx, fmt.Sprintf("UPDATE %s.runs SET status='starting' WHERE id=$1", q), r.RunID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, fmt.Sprintf("UPDATE %s.run_attempts SET status='leased',turn_started_at=NULL WHERE id=$1", q), r.RunAttemptID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.AuthorizeRepositoryCredential(ctx, r); err != nil {
		t.Fatal("leased preflight rejected", err)
	}
	if _, err := f.pool.Exec(ctx, fmt.Sprintf("UPDATE %s.attempt_leases SET expires_at=pg_catalog.clock_timestamp()-interval '1 second' WHERE run_attempt_id=$1", q), r.RunAttemptID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.AuthorizeRepositoryCredential(ctx, r); err == nil {
		t.Fatal("expired attempt retained Git credential access")
	}
	if err := f.store.RecordRepositoryCredentialUse(ctx, r, stateTestUUID(985700), "deny"); err != nil {
		t.Fatal(err)
	}
}
