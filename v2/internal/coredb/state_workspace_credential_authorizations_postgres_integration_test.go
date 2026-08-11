package coredb

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecredentials"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgreSQLWorkspaceCredentialAuthorizationLeaseCanMoveBetweenOwners(t *testing.T) {
	fixture := newWorkspaceCredentialAuthorizationPostgresFixture(t, 960_000)
	record := fixture.authorizationRecord(10)
	created, err := fixture.store.CreateWorkspaceCredentialAuthorization(t.Context(), CreateWorkspaceCredentialAuthorizationCommand{Record: record})
	if err != nil || created.Status != WorkspaceCredentialAuthorizationPending || created.Version != 1 {
		t.Fatalf("create authorization = %+v, %v", created, err)
	}
	leaseToken := stateTestUUID(960_020)
	claimed, err := fixture.store.ClaimWorkspaceCredentialAuthorizationPoll(t.Context(), ClaimWorkspaceCredentialAuthorizationPollCommand{
		WorkspaceID: fixture.workspaceID, Kind: record.Kind, AuthorizationID: record.ID,
		ActorID: fixture.secondOwnerID, LeaseToken: leaseToken, LeaseExpiresAt: time.Now().UTC().Add(time.Minute),
	})
	if err != nil || claimed.PollLeaseToken != leaseToken || claimed.Version != 2 {
		t.Fatalf("claim authorization = %+v, %v", claimed, err)
	}
	nextPollAt := time.Now().UTC().Add(20 * time.Second)
	finished, err := fixture.store.FinishWorkspaceCredentialAuthorizationPoll(t.Context(), FinishWorkspaceCredentialAuthorizationPollCommand{
		WorkspaceID: fixture.workspaceID, Kind: record.Kind, AuthorizationID: record.ID,
		ActorID: fixture.secondOwnerID, LeaseToken: leaseToken, Status: WorkspaceCredentialAuthorizationPending,
		PollInterval: 20, NextPollAt: nextPollAt, LastErrorCode: "authorization_pending",
	})
	if err != nil || finished.ActorID != fixture.ownerID || finished.PollLeaseToken != "" ||
		finished.Status != WorkspaceCredentialAuthorizationPending || finished.Version != 3 || finished.NextPollAt.Before(nextPollAt.Add(-time.Millisecond)) {
		t.Fatalf("finish authorization as second owner = %+v, %v", finished, err)
	}
	cancelled, changed, err := fixture.store.CancelWorkspaceCredentialAuthorization(t.Context(), CancelWorkspaceCredentialAuthorizationCommand{
		WorkspaceID: fixture.workspaceID, Kind: record.Kind, AuthorizationID: record.ID,
		ActorID: fixture.secondOwnerID, ExpectedVersion: finished.Version,
	})
	if err != nil || !changed || cancelled.Status != WorkspaceCredentialAuthorizationCancelled ||
		cancelled.CompletedAt == nil || len(cancelled.SealedProviderState) != 0 {
		t.Fatalf("cancel authorization = %+v changed=%v, %v", cancelled, changed, err)
	}
}

func TestPostgreSQLWorkspaceCredentialAuthorizationFinalizeAndRefreshCAS(t *testing.T) {
	fixture := newWorkspaceCredentialAuthorizationPostgresFixture(t, 961_000)
	record := fixture.authorizationRecord(10)
	if _, err := fixture.store.CreateWorkspaceCredentialAuthorization(t.Context(), CreateWorkspaceCredentialAuthorizationCommand{Record: record}); err != nil {
		t.Fatal(err)
	}
	leaseToken := stateTestUUID(961_020)
	if _, err := fixture.store.ClaimWorkspaceCredentialAuthorizationPoll(t.Context(), ClaimWorkspaceCredentialAuthorizationPollCommand{
		WorkspaceID: fixture.workspaceID, Kind: record.Kind, AuthorizationID: record.ID,
		ActorID: fixture.secondOwnerID, LeaseToken: leaseToken, LeaseExpiresAt: time.Now().UTC().Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	accessExpiry := time.Now().UTC().Add(2 * time.Minute).Truncate(time.Microsecond)
	refreshExpiry := accessExpiry.Add(24 * time.Hour)
	finalized, err := fixture.store.FinalizeWorkspaceCredentialAuthorization(t.Context(), FinalizeWorkspaceCredentialAuthorizationCommand{
		WorkspaceID: fixture.workspaceID, Kind: record.Kind, AuthorizationID: record.ID,
		ActorID: fixture.secondOwnerID, LeaseToken: leaseToken, AuthType: "device_oauth",
		PublicMetadata: json.RawMessage(`{"subject":"owner"}`), SealedSecret: bytes.Repeat([]byte{0x51}, 96),
		SealingKeyID: "credential-key-1", AccessExpiresAt: &accessExpiry, RefreshExpiresAt: &refreshExpiry,
	})
	if err != nil || finalized.Authorization.Status != WorkspaceCredentialAuthorizationSucceeded ||
		finalized.Authorization.BindingID != record.TargetBindingID || finalized.Authorization.CompletedAt == nil ||
		finalized.Binding.ID != record.TargetBindingID || !finalized.Binding.IsDefault || finalized.Binding.CredentialVersion != 1 {
		t.Fatalf("finalize authorization = %+v, %v", finalized, err)
	}
	if _, err := fixture.store.FinalizeWorkspaceCredentialAuthorization(t.Context(), FinalizeWorkspaceCredentialAuthorizationCommand{
		WorkspaceID: fixture.workspaceID, Kind: record.Kind, AuthorizationID: record.ID,
		ActorID: fixture.ownerID, LeaseToken: leaseToken, AuthType: "device_oauth",
		SealedSecret: bytes.Repeat([]byte{0x52}, 96), SealingKeyID: "credential-key-1",
		AccessExpiresAt: &accessExpiry, RefreshExpiresAt: &refreshExpiry,
	}); !HasStateErrorCode(err, ErrorConflict) {
		t.Fatalf("replayed finalize error = %v, want conflict", err)
	}

	refreshLease := stateTestUUID(961_030)
	claimed, ownsLease, err := fixture.store.ClaimWorkspaceCredentialRefresh(t.Context(), ClaimWorkspaceCredentialRefreshCommand{
		WorkspaceID: fixture.workspaceID, Kind: record.Kind, BindingID: record.TargetBindingID,
		Before: time.Now().UTC().Add(5 * time.Minute), LeaseToken: refreshLease, LeaseExpiresAt: time.Now().UTC().Add(time.Minute),
	})
	if err != nil || !ownsLease || claimed.CredentialVersion != 1 {
		t.Fatalf("claim refresh = %+v owns=%v, %v", claimed, ownsLease, err)
	}
	competing, competingOwns, err := fixture.store.ClaimWorkspaceCredentialRefresh(t.Context(), ClaimWorkspaceCredentialRefreshCommand{
		WorkspaceID: fixture.workspaceID, Kind: record.Kind, BindingID: record.TargetBindingID,
		Before: time.Now().UTC().Add(5 * time.Minute), LeaseToken: stateTestUUID(961_031), LeaseExpiresAt: time.Now().UTC().Add(time.Minute),
	})
	if err != nil || competingOwns || competing.ID != claimed.ID {
		t.Fatalf("competing refresh = %+v owns=%v, %v", competing, competingOwns, err)
	}
	newAccessExpiry := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	newRefreshExpiry := newAccessExpiry.Add(48 * time.Hour)
	completed, err := fixture.store.CompleteWorkspaceCredentialRefresh(t.Context(), CompleteWorkspaceCredentialRefreshCommand{
		WorkspaceID: fixture.workspaceID, Kind: record.Kind, BindingID: record.TargetBindingID,
		ExpectedAuthorityVersion: claimed.AuthorityVersion, ExpectedCredentialVersion: claimed.CredentialVersion,
		LeaseToken: refreshLease, AuthType: "device_oauth", PublicMetadata: json.RawMessage(`{"subject":"refreshed"}`),
		SealedSecret: bytes.Repeat([]byte{0x53}, 96), SealingKeyID: "credential-key-1",
		AccessExpiresAt: &newAccessExpiry, RefreshExpiresAt: &newRefreshExpiry,
	})
	if err != nil || completed.CredentialVersion != claimed.CredentialVersion+1 || completed.AuthorityVersion != claimed.AuthorityVersion ||
		!completed.AccessExpiresAt.Equal(newAccessExpiry) {
		t.Fatalf("complete refresh = %+v, %v", completed, err)
	}
	if _, err := fixture.store.CompleteWorkspaceCredentialRefresh(t.Context(), CompleteWorkspaceCredentialRefreshCommand{
		WorkspaceID: fixture.workspaceID, Kind: record.Kind, BindingID: record.TargetBindingID,
		ExpectedAuthorityVersion: claimed.AuthorityVersion, ExpectedCredentialVersion: claimed.CredentialVersion,
		LeaseToken: refreshLease, AuthType: "device_oauth", SealedSecret: bytes.Repeat([]byte{0x54}, 96),
		SealingKeyID: "credential-key-1", AccessExpiresAt: &newAccessExpiry, RefreshExpiresAt: &newRefreshExpiry,
	}); !HasStateErrorCode(err, ErrorVersionConflict) {
		t.Fatalf("stale refresh completion error = %v, want version_conflict", err)
	}

	quotedSchema := quoteIdentifier(fixture.schema)
	forceDue := fmt.Sprintf(`UPDATE %s.workspace_credential_bindings
SET access_expires_at = pg_catalog.clock_timestamp() + interval '1 minute'
WHERE id = $1`, quotedSchema)
	if _, err := fixture.pool.Exec(t.Context(), forceDue, record.TargetBindingID); err != nil {
		t.Fatal(err)
	}
	terminalLease := stateTestUUID(961_040)
	terminalClaim, ownsLease, err := fixture.store.ClaimWorkspaceCredentialRefresh(t.Context(), ClaimWorkspaceCredentialRefreshCommand{
		WorkspaceID: fixture.workspaceID, Kind: record.Kind, BindingID: record.TargetBindingID,
		Before: time.Now().UTC().Add(5 * time.Minute), LeaseToken: terminalLease, LeaseExpiresAt: time.Now().UTC().Add(time.Minute),
	})
	if err != nil || !ownsLease {
		t.Fatalf("terminal refresh claim = %+v owns=%v, %v", terminalClaim, ownsLease, err)
	}
	failed, err := fixture.store.FailWorkspaceCredentialRefresh(t.Context(), FailWorkspaceCredentialRefreshCommand{
		WorkspaceID: fixture.workspaceID, Kind: record.Kind, BindingID: record.TargetBindingID,
		ExpectedAuthorityVersion: terminalClaim.AuthorityVersion, ExpectedCredentialVersion: terminalClaim.CredentialVersion,
		LeaseToken: terminalLease, ErrorCode: "invalid_refresh_token", Terminal: true,
	})
	if err != nil || failed.Status != corecredentials.StatusReauthRequired || failed.IsDefault ||
		failed.AuthorityVersion != terminalClaim.AuthorityVersion+1 || failed.CredentialVersion != terminalClaim.CredentialVersion {
		t.Fatalf("terminal refresh failure = %+v, %v", failed, err)
	}
}

type workspaceCredentialAuthorizationPostgresFixture struct {
	store                  *StateStore
	pool                   *pgxpool.Pool
	schema, workspaceID    string
	ownerID, secondOwnerID string
}

func newWorkspaceCredentialAuthorizationPostgresFixture(t *testing.T, seed int) workspaceCredentialAuthorizationPostgresFixture {
	t.Helper()
	store, pool, schema := newPostgresStateStore(t)
	fixture := workspaceCredentialAuthorizationPostgresFixture{
		store: store, pool: pool, schema: schema,
		workspaceID: stateTestUUID(seed), ownerID: stateTestUUID(seed + 1), secondOwnerID: stateTestUUID(seed + 2),
	}
	quotedSchema := quoteIdentifier(schema)
	if _, err := pool.Exec(t.Context(), fmt.Sprintf("INSERT INTO %s.workspaces (id, status, managed_lark_credential_mode) VALUES ($1, 'active', 'process_env')", quotedSchema), fixture.workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), fmt.Sprintf("INSERT INTO %s.users (id, status) VALUES ($1, 'active'), ($2, 'active')", quotedSchema), fixture.ownerID, fixture.secondOwnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), fmt.Sprintf("INSERT INTO %s.workspace_members (workspace_id, user_id, role) VALUES ($1, $2, 'owner'), ($1, $3, 'owner')", quotedSchema), fixture.workspaceID, fixture.ownerID, fixture.secondOwnerID); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func (fixture workspaceCredentialAuthorizationPostgresFixture) authorizationRecord(offset int) WorkspaceCredentialAuthorizationRecord {
	now := time.Now().UTC()
	return WorkspaceCredentialAuthorizationRecord{
		ID: stateTestUUID(962_000 + offset), WorkspaceID: fixture.workspaceID, Kind: "lark", ActorID: fixture.ownerID,
		TargetBindingID: stateTestUUID(962_100 + offset), DisplayName: "Workspace Lark", OwnerScope: corecredentials.OwnerScopeWorkspace,
		ProviderPublic: json.RawMessage(`{"requestedScopes":["offline_access"]}`), UserCode: "ABCD-EFGH",
		VerificationURI: "https://accounts.feishu.cn/device", VerificationURIComplete: "https://accounts.feishu.cn/device?code=ABCD-EFGH",
		SealedProviderState: bytes.Repeat([]byte{0x31}, 96), SealingKeyID: "credential-key-1", ProviderStateVersion: 1,
		Status: WorkspaceCredentialAuthorizationPending, PollIntervalSeconds: 2,
		NextPollAt: now.Add(-time.Second), ExpiresAt: now.Add(10 * time.Minute),
	}
}
