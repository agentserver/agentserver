package coredb

import (
	"fmt"
	"testing"
)

func TestPostgreSQLKubernetesCutoverAuditsRegionsOnce(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	owner := startExecutionTestRun(t, store, pool, schema, 930_000)
	other := startExecutionTestRun(t, store, pool, schema, 940_000)
	q := quoteIdentifier(schema)
	for _, id := range []string{owner.Run.WorkspaceID, other.Run.WorkspaceID} {
		_, err := pool.Exec(t.Context(), fmt.Sprintf(`INSERT INTO %s.workspace_managed_sandbox_settings (workspace_id,region,version,updated_by) VALUES ($1,'i18n-tt',1,'99000000-0000-4000-8000-000000000001') ON CONFLICT (workspace_id) DO UPDATE SET region='i18n-tt',version=1`, q), id)
		if err != nil {
			t.Fatal(err)
		}
	}
	profile := validManagedEnvironmentProfile()
	profile.WorkspaceID = owner.Run.WorkspaceID
	profile.BackendKind = DispatchTargetKubernetes
	profile.MigrateAllWorkspaceRegions = true
	if _, err := pool.Exec(t.Context(), fmt.Sprintf(`INSERT INTO %s.executors (id,workspace_id,status) VALUES ($1,$2,'enrolling')`, q), profile.ExecutorID, profile.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := bootstrapManagedEnvironmentProfileConfig(t.Context(), pool.Config().ConnConfig, schema, profile); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{owner.Run.WorkspaceID, other.Run.WorkspaceID} {
		var region string
		var version, events int
		if err := pool.QueryRow(t.Context(), fmt.Sprintf(`SELECT region,version FROM %s.workspace_managed_sandbox_settings WHERE workspace_id=$1`, q), id).Scan(&region, &version); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(t.Context(), fmt.Sprintf(`SELECT count(*) FROM %s.workspace_managed_sandbox_setting_events WHERE workspace_id=$1 AND previous_region='i18n-tt' AND current_region='sg'`, q), id).Scan(&events); err != nil {
			t.Fatal(err)
		}
		if region != "sg" || version != 2 || events != 1 {
			t.Fatalf("non-idempotent region cutover: %s %d %d", region, version, events)
		}
	}
	// Once CN is installed, a subsequent deployment must preserve the owner's
	// regional choice rather than silently moving it back to SG.
	if _, err := pool.Exec(t.Context(), fmt.Sprintf(`UPDATE %s.workspace_managed_sandbox_settings SET region='cn' WHERE workspace_id=$1`, q), other.Run.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	profile.RetainedWorkspaceRegions = []string{"cn", "sg"}
	if _, err := bootstrapManagedEnvironmentProfileConfig(t.Context(), pool.Config().ConnConfig, schema, profile); err != nil {
		t.Fatal(err)
	}
	var region string
	var version int
	if err := pool.QueryRow(t.Context(), fmt.Sprintf(`SELECT region,version FROM %s.workspace_managed_sandbox_settings WHERE workspace_id=$1`, q), other.Run.WorkspaceID).Scan(&region, &version); err != nil {
		t.Fatal(err)
	}
	if region != "cn" || version != 2 {
		t.Fatalf("upgrade erased regional choice: %s v%d", region, version)
	}
}

func TestKubernetesDispatchTargetPreservesProvider(t *testing.T) {
	id := "f58448a6-9ca8-4d2a-b5f3-86a07f7b7524"
	target := (ManagedSandbox{ID: id, ProviderKind: DispatchTargetKubernetes, Generation: 3}).Target()
	if target.Kind != DispatchTargetKubernetes {
		t.Fatal("Kubernetes projected as TAE")
	}
	if err := validateDispatchTarget(target, true); err != nil {
		t.Fatal(err)
	}
	if _, err := normalizedPrepareTarget(PrepareExecutionCommand{Target: target}); err != nil {
		t.Fatal(err)
	}
	if _, err := normalizedPrepareTarget(PrepareExecutionCommand{Target: target, ExecutorID: id}); err == nil {
		t.Fatal("managed sandbox accepted legacy executor ID")
	}
	if _, err := resolveDispatchTarget(Execution{Target: target}, target, 1); err == nil {
		t.Fatal("Kubernetes accepted AgentX connection generation")
	}
	other := target
	other.Kind = DispatchTargetTAE
	if _, err := resolveDispatchTarget(Execution{Target: target}, other, 0); err == nil {
		t.Fatal("frozen target crossed provider")
	}
	if connectionGenerationForTarget(target) != nil {
		t.Fatal("managed target acquired AgentX connection")
	}
}

func TestManagedReservationDoesNotCrossProviders(t *testing.T) {
	s := ManagedSandbox{WorkspaceID: "w", SessionID: "s", EnvironmentID: "e", ProviderKind: DispatchTargetKubernetes, ProviderRegion: "sg", ProviderPSM: "sg-managed"}
	c := ReserveManagedSandboxCommand{WorkspaceID: "w", SessionID: "s", EnvironmentID: "e", ProviderKind: DispatchTargetKubernetes, ProviderRegion: "sg", ProviderPSM: "sg-managed"}
	if !managedSandboxReservationMatches(s, c) {
		t.Fatal("matching Kubernetes reservation rejected")
	}
	c.ProviderKind = DispatchTargetTAE
	if managedSandboxReservationMatches(s, c) {
		t.Fatal("Kubernetes reservation adopted by TAE")
	}
	c.ProviderKind = ""
	if managedSandboxReservationMatches(s, c) {
		t.Fatal("Kubernetes reservation adopted by legacy caller")
	}
}
