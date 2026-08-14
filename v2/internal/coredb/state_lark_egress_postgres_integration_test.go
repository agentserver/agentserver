package coredb

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecredentials"
	"github.com/agentserver/agentserver/v2/internal/managedcredential"
	"github.com/jackc/pgx/v5/pgxpool"
)

type managedLarkPostgresFixture struct {
	store     *StateStore
	pool      *pgxpool.Pool
	schema    string
	running   executionTestRunningRun
	grant     WorkspaceLarkGrant
	binding   RunLarkEgressBinding
	sandbox   ManagedSandbox
	dispatch  BeginOperationDispatchResult
	authority ManagedLarkAuthorityQuery
}

func TestPostgreSQLByteCloudCredentialUsesProcessEnvironmentIndependentOfLarkMode(t *testing.T) {
	fixture := newManagedLarkPostgresFixture(t, 849_000)
	bindingID := stateTestUUID(849_600)
	quotedSchema := quoteIdentifier(fixture.schema)
	insertBinding := fmt.Sprintf(`INSERT INTO %s.workspace_credential_bindings (
 id, workspace_id, kind, display_name, owner_scope, public_metadata, auth_type,
 sealed_secret, sealing_key_id, status, is_default, access_expires_at, refresh_expires_at)
VALUES ($1, $2, 'bytecloud', 'Workspace ByteCloud', 'workspace',
        '{"site":"i18n-tt"}'::jsonb, 'device_oauth', $3, 'credential-key-1',
        'active', true, pg_catalog.clock_timestamp() + interval '1 hour',
        pg_catalog.clock_timestamp() + interval '7 days')`, quotedSchema)
	if _, err := fixture.pool.Exec(t.Context(), insertBinding, bindingID, fixture.running.Run.WorkspaceID, bytes.Repeat([]byte{0x61}, 96)); err != nil {
		t.Fatal(err)
	}
	policySHA256 := fmt.Sprintf("%x", sha256.Sum256([]byte("managed-bytecloud-policy")))
	authorityRequest := corecredentials.AuthorityRequest{
		WorkspaceID: fixture.running.Run.WorkspaceID, SessionID: fixture.running.Run.SessionID,
		ActorID: fixture.running.Run.ActorID, EnvironmentID: fixture.sandbox.EnvironmentID,
		RunID: fixture.running.Run.ID, RunAttemptID: fixture.running.Attempt.ID,
		RunAttemptGeneration: fixture.running.Attempt.Generation,
		ExecutionID:          fixture.dispatch.Execution.ID, OperationID: fixture.dispatch.Operation.ID,
		SandboxID: fixture.sandbox.ID, TargetGeneration: fixture.sandbox.Generation,
		ProviderKind: "bytecloud", PolicySHA256: policySHA256,
	}
	reference, err := fixture.store.ResolveCredentialAuthority(t.Context(), authorityRequest)
	if err != nil || reference.Kind != "bytecloud" || reference.BindingID != bindingID ||
		reference.AuthorityVersion != 1 || reference.CredentialVersion != 1 ||
		reference.CredentialMode != managedcredential.ModeProcessEnv {
		t.Fatalf("ByteCloud authority in Lark webhook workspace = %+v, %v", reference, err)
	}
	use := corecredentials.UseRequest{
		WorkspaceID: authorityRequest.WorkspaceID, SessionID: authorityRequest.SessionID,
		ActorID: authorityRequest.ActorID, EnvironmentID: authorityRequest.EnvironmentID,
		RunID: authorityRequest.RunID, RunAttemptID: authorityRequest.RunAttemptID,
		RunAttemptGeneration: authorityRequest.RunAttemptGeneration,
		ExecutionID:          authorityRequest.ExecutionID, OperationID: authorityRequest.OperationID,
		SandboxID: authorityRequest.SandboxID, TargetGeneration: authorityRequest.TargetGeneration,
		ProviderKind: "bytecloud", BindingID: bindingID, AuthorityVersion: reference.AuthorityVersion,
		ExpectedCredentialVersion: reference.CredentialVersion, CredentialMode: managedcredential.ModeProcessEnv,
		PolicySHA256: policySHA256, TAEPSM: fixture.authority.TAEPSM,
		Host: "cloud-i18n-sg.bytedance.net", Path: "/", Method: "PROCESS_ENV",
	}
	live, err := fixture.store.AuthorizeCredentialUse(t.Context(), use)
	if err != nil || live != reference {
		t.Fatalf("live ByteCloud process_env authority = %+v, %v; want %+v", live, err, reference)
	}
	use.CredentialMode = managedcredential.ModeWebhookSwap
	if _, err := fixture.store.AuthorizeCredentialUse(t.Context(), use); !HasStateErrorCode(err, ErrorForbidden) {
		t.Fatalf("ByteCloud webhook authority error = %v, want forbidden", err)
	}
}

func TestPostgreSQLManagedLarkAuthorityFencesCredentialAndOperationState(t *testing.T) {
	fixture := newManagedLarkPostgresFixture(t, 850_000)

	resolved, err := fixture.store.ResolveManagedLarkEgressAuthority(t.Context(), fixture.authorityForResolve())
	if err != nil || !larkBindingsEqual(resolved.Binding, fixture.binding) ||
		resolved.CredentialVersion != 1 || resolved.WorkspaceID != fixture.running.Run.WorkspaceID {
		t.Fatalf("ResolveManagedLarkEgressAuthority() = %+v, %v", resolved, err)
	}
	live, err := fixture.store.AuthorizeManagedLarkEgress(t.Context(), fixture.authority)
	if err != nil || !larkBindingsEqual(live.Binding, fixture.binding) || live.CredentialVersion != 1 {
		t.Fatalf("AuthorizeManagedLarkEgress(dispatching) = %+v, %v", live, err)
	}

	rotatedExpiry := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Microsecond)
	rotated, err := fixture.store.UpdateWorkspaceLarkGrantCredential(t.Context(), UpdateWorkspaceLarkGrantCredentialCommand{
		GrantID: fixture.grant.ID, ExpectedAuthorityVersion: fixture.grant.AuthorityVersion,
		ExpectedCredentialVersion: fixture.grant.CredentialVersion,
		SealedTokenSet:            bytes.Repeat([]byte{0x72}, 96), AccessExpiresAt: rotatedExpiry,
		RefreshExpiresAt: fixture.grant.RefreshExpiresAt, NextRefreshAt: rotatedExpiry.Add(-10 * time.Minute),
	})
	if err != nil || rotated.AuthorityVersion != fixture.grant.AuthorityVersion || rotated.CredentialVersion != 2 {
		t.Fatalf("UpdateWorkspaceLarkGrantCredential() = %+v, %v", rotated, err)
	}
	live, err = fixture.store.AuthorizeManagedLarkEgress(t.Context(), fixture.authority)
	if err != nil || live.CredentialVersion != 2 || !live.AccessExpiresAt.Equal(rotatedExpiry) {
		t.Fatalf("live authority after credential rotation = %+v, %v", live, err)
	}

	quotedSchema := quoteIdentifier(fixture.schema)
	revoke := fmt.Sprintf(`UPDATE %s.workspace_lark_grants
SET status = 'revoked', revoked_at = pg_catalog.clock_timestamp()
WHERE id = $1`, quotedSchema)
	if _, err := fixture.pool.Exec(t.Context(), revoke, fixture.grant.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.AuthorizeManagedLarkEgress(t.Context(), fixture.authority); !HasStateErrorCode(err, ErrorLeaseLost) {
		t.Fatalf("revoked grant live authority error = %v, want lease_lost", err)
	}
	frozen, err := fixture.store.ResolveUserRunLarkEgressBinding(t.Context(), ResolveUserRunLarkEgressBindingCommand{
		WorkspaceID: fixture.running.Run.WorkspaceID, SessionID: fixture.running.Run.SessionID,
		ActorID: fixture.running.Run.ActorID, IdempotencyKey: fmt.Sprintf("managed-lark-run-%d", 850_000),
	})
	if err != nil || !larkBindingsEqual(frozen, fixture.binding) {
		t.Fatalf("frozen run binding after revocation = %+v, %v", frozen, err)
	}
	restore := fmt.Sprintf(`UPDATE %s.workspace_lark_grants
SET status = 'active', revoked_at = NULL
WHERE id = $1`, quotedSchema)
	if _, err := fixture.pool.Exec(t.Context(), restore, fixture.grant.ID); err != nil {
		t.Fatal(err)
	}

	bumpAuthority := fmt.Sprintf(`UPDATE %s.workspace_lark_grants
SET authority_version = authority_version + 1
WHERE id = $1`, quotedSchema)
	if _, err := fixture.pool.Exec(t.Context(), bumpAuthority, fixture.grant.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.AuthorizeManagedLarkEgress(t.Context(), fixture.authority); !HasStateErrorCode(err, ErrorLeaseLost) {
		t.Fatalf("advanced authority version error = %v, want lease_lost", err)
	}
	restoreAuthority := fmt.Sprintf(`UPDATE %s.workspace_lark_grants
SET authority_version = authority_version - 1
WHERE id = $1`, quotedSchema)
	if _, err := fixture.pool.Exec(t.Context(), restoreAuthority, fixture.grant.ID); err != nil {
		t.Fatal(err)
	}

	wrongPSM := fixture.authority
	wrongPSM.TAEPSM = "prod.tae.other"
	if _, err := fixture.store.AuthorizeManagedLarkEgress(t.Context(), wrongPSM); !HasStateErrorCode(err, ErrorLeaseLost) {
		t.Fatalf("wrong TAE PSM error = %v, want lease_lost", err)
	}
	wrongVersion := fixture.authority
	wrongVersion.GrantVersion++
	if _, err := fixture.store.AuthorizeManagedLarkEgress(t.Context(), wrongVersion); !HasStateErrorCode(err, ErrorLeaseLost) {
		t.Fatalf("wrong frozen grant version error = %v, want lease_lost", err)
	}

	acknowledged, err := fixture.store.AcknowledgeOperation(t.Context(), AcknowledgeOperationCommand{
		OperationID: fixture.dispatch.Operation.ID, ExecutionID: fixture.dispatch.Execution.ID,
		RunID: fixture.running.Run.ID, AttemptID: fixture.running.Attempt.ID,
		Generation: fixture.running.Attempt.Generation, Target: fixture.sandbox.Target(),
		ExpectedExecutionVersion: fixture.dispatch.Execution.Version,
		ExpectedOperationVersion: fixture.dispatch.Operation.Version,
		AcknowledgementHash:      executionTestHash(t, HashDomainOperationAck, 850_500),
		Record:                   stateTransitionRecord(850_510),
	})
	if err != nil || !acknowledged.Changed {
		t.Fatalf("AcknowledgeOperation() = %+v, %v", acknowledged, err)
	}
	live, err = fixture.store.AuthorizeManagedLarkEgress(t.Context(), fixture.authority)
	if err != nil || live.CredentialVersion != 2 {
		t.Fatalf("AuthorizeManagedLarkEgress(acknowledged) = %+v, %v", live, err)
	}
	completed, err := fixture.store.CompleteOperation(t.Context(), CompleteOperationCommand{
		OperationID: acknowledged.Operation.ID, ExecutionID: acknowledged.Execution.ID,
		RunID: fixture.running.Run.ID, AttemptID: fixture.running.Attempt.ID,
		Generation: fixture.running.Attempt.Generation, Target: fixture.sandbox.Target(),
		ExpectedExecutionVersion: acknowledged.Execution.Version,
		ExpectedOperationVersion: acknowledged.Operation.Version,
		TerminalStatus:           OperationStatusSucceeded,
		ResultHash:               executionTestHash(t, HashDomainOperationResult, 850_520),
		Record:                   stateTransitionRecord(850_530),
	})
	if err != nil || !completed.Changed {
		t.Fatalf("CompleteOperation() = %+v, %v", completed, err)
	}
	if _, err := fixture.store.AuthorizeManagedLarkEgress(t.Context(), fixture.authority); !HasStateErrorCode(err, ErrorLeaseLost) {
		t.Fatalf("terminal operation authority error = %v, want lease_lost", err)
	}
}

func TestPostgreSQLManagedLarkGrantRefreshLifecycle(t *testing.T) {
	fixture := newManagedLarkPostgresFixture(t, 860_000)
	quotedSchema := quoteIdentifier(fixture.schema)
	forceDue := fmt.Sprintf(`UPDATE %s.workspace_lark_grants
SET next_refresh_at = pg_catalog.clock_timestamp() - interval '1 second'
WHERE id = $1`, quotedSchema)
	if _, err := fixture.pool.Exec(t.Context(), forceDue, fixture.grant.ID); err != nil {
		t.Fatal(err)
	}

	claimed, err := fixture.store.ClaimWorkspaceLarkGrantRefreshes(t.Context(), ClaimWorkspaceLarkGrantRefreshesCommand{
		Owner: "core-refresh-a", Limit: 10, LockTTL: time.Minute,
	})
	if err != nil || len(claimed) != 1 || claimed[0].RefreshLockOwner == nil || *claimed[0].RefreshLockOwner != "core-refresh-a" || claimed[0].RefreshAttempts != 1 {
		t.Fatalf("ClaimWorkspaceLarkGrantRefreshes() = %+v, %v", claimed, err)
	}
	competing, err := fixture.store.ClaimWorkspaceLarkGrantRefreshes(t.Context(), ClaimWorkspaceLarkGrantRefreshesCommand{
		Owner: "core-refresh-b", Limit: 10, LockTTL: time.Minute,
	})
	if err != nil || len(competing) != 0 {
		t.Fatalf("competing refresh claim = %+v, %v", competing, err)
	}
	dispatched, err := fixture.store.MarkWorkspaceLarkGrantRefreshDispatched(t.Context(), MarkWorkspaceLarkGrantRefreshDispatchedCommand{
		GrantID: fixture.grant.ID, Owner: "core-refresh-a",
		ExpectedAuthorityVersion:  fixture.grant.AuthorityVersion,
		ExpectedCredentialVersion: fixture.grant.CredentialVersion,
		DispatchTTL:               time.Minute,
	})
	if err != nil || dispatched.RefreshDispatchedAt == nil {
		t.Fatalf("MarkWorkspaceLarkGrantRefreshDispatched() = %+v, %v", dispatched, err)
	}
	newAccessExpiry := time.Now().UTC().Add(3 * time.Hour).Truncate(time.Microsecond)
	newRefreshExpiry := newAccessExpiry.Add(30 * 24 * time.Hour)
	completed, err := fixture.store.CompleteWorkspaceLarkGrantRefresh(t.Context(), CompleteWorkspaceLarkGrantRefreshCommand{
		GrantID: fixture.grant.ID, Owner: "core-refresh-a",
		ExpectedAuthorityVersion:  fixture.grant.AuthorityVersion,
		ExpectedCredentialVersion: fixture.grant.CredentialVersion,
		SealedTokenSet:            bytes.Repeat([]byte{0x81}, 96), AccessExpiresAt: newAccessExpiry,
		RefreshExpiresAt: &newRefreshExpiry, NextRefreshAt: newAccessExpiry.Add(-10 * time.Minute),
	})
	if err != nil || completed.AuthorityVersion != fixture.grant.AuthorityVersion || completed.CredentialVersion != fixture.grant.CredentialVersion+1 ||
		completed.RefreshLockOwner != nil || completed.RefreshDispatchedAt != nil || completed.RefreshAttempts != 0 {
		t.Fatalf("CompleteWorkspaceLarkGrantRefresh() = %+v, %v", completed, err)
	}

	if _, err := fixture.pool.Exec(t.Context(), forceDue, fixture.grant.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err = fixture.store.ClaimWorkspaceLarkGrantRefreshes(t.Context(), ClaimWorkspaceLarkGrantRefreshesCommand{
		Owner: "core-refresh-c", Limit: 1, LockTTL: time.Minute,
	})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("second refresh claim = %+v, %v", claimed, err)
	}
	dispatched, err = fixture.store.MarkWorkspaceLarkGrantRefreshDispatched(t.Context(), MarkWorkspaceLarkGrantRefreshDispatchedCommand{
		GrantID: fixture.grant.ID, Owner: "core-refresh-c",
		ExpectedAuthorityVersion:  completed.AuthorityVersion,
		ExpectedCredentialVersion: completed.CredentialVersion, DispatchTTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	retryAt := time.Now().UTC().Add(5 * time.Minute).Truncate(time.Microsecond)
	deferred, err := fixture.store.DeferWorkspaceLarkGrantRefresh(t.Context(), DeferWorkspaceLarkGrantRefreshCommand{
		GrantID: fixture.grant.ID, Owner: "core-refresh-c",
		ExpectedAuthorityVersion:  dispatched.AuthorityVersion,
		ExpectedCredentialVersion: dispatched.CredentialVersion,
		NextRefreshAt:             retryAt, ErrorCode: "feishu_20072",
	})
	if err != nil || deferred.RefreshLockOwner != nil || deferred.RefreshDispatchedAt != nil ||
		deferred.LastRefreshErrorCode == nil || *deferred.LastRefreshErrorCode != "feishu_20072" {
		t.Fatalf("DeferWorkspaceLarkGrantRefresh() = %+v, %v", deferred, err)
	}

	if _, err := fixture.pool.Exec(t.Context(), forceDue, fixture.grant.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err = fixture.store.ClaimWorkspaceLarkGrantRefreshes(t.Context(), ClaimWorkspaceLarkGrantRefreshesCommand{
		Owner: "core-refresh-d", Limit: 1, LockTTL: time.Minute,
	})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("permanent-failure claim = %+v, %v", claimed, err)
	}
	failed, err := fixture.store.FailWorkspaceLarkGrantRefresh(t.Context(), FailWorkspaceLarkGrantRefreshCommand{
		GrantID: fixture.grant.ID, Owner: "core-refresh-d",
		ExpectedAuthorityVersion:  claimed[0].AuthorityVersion,
		ExpectedCredentialVersion: claimed[0].CredentialVersion,
		ErrorCode:                 "feishu_20073",
	})
	if err != nil || failed.Status != LarkGrantStatusReauthRequired || failed.AuthorityVersion != completed.AuthorityVersion+1 ||
		len(failed.SealedTokenSet) != larkGrantCredentialTombstoneBytes || !bytes.Equal(failed.SealedTokenSet, make([]byte, larkGrantCredentialTombstoneBytes)) {
		t.Fatalf("FailWorkspaceLarkGrantRefresh() = %+v, %v", failed, err)
	}
	if _, err := fixture.store.AuthorizeManagedLarkEgress(t.Context(), fixture.authority); !HasStateErrorCode(err, ErrorLeaseLost) {
		t.Fatalf("failed refresh retained live authority: %v", err)
	}

	reauthorizedAccess := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Microsecond)
	reauthorizedRefresh := reauthorizedAccess.Add(30 * 24 * time.Hour)
	reauthorized, err := fixture.store.UpsertWorkspaceLarkGrant(t.Context(), UpsertWorkspaceLarkGrantCommand{
		ID: fixture.grant.ID, WorkspaceID: fixture.grant.WorkspaceID, UserID: fixture.grant.UserID,
		PolicySHA256: fixture.grant.PolicySHA256, SealedTokenSet: bytes.Repeat([]byte{0x82}, 96),
		AccessExpiresAt: reauthorizedAccess, RefreshExpiresAt: &reauthorizedRefresh,
		NextRefreshAt: reauthorizedAccess.Add(-10 * time.Minute),
	})
	if err != nil || reauthorized.Status != LarkGrantStatusActive || reauthorized.AuthorityVersion != failed.AuthorityVersion+1 ||
		reauthorized.CredentialVersion != failed.CredentialVersion+1 || reauthorized.LastRefreshErrorCode != nil {
		t.Fatalf("UpsertWorkspaceLarkGrant(reauthorize) = %+v, %v", reauthorized, err)
	}
	revoked, err := fixture.store.RevokeWorkspaceLarkGrant(t.Context(), RevokeWorkspaceLarkGrantCommand{
		GrantID: fixture.grant.ID, WorkspaceID: fixture.grant.WorkspaceID,
		UserID: fixture.grant.UserID, ReasonCode: "operator_revoked",
	})
	if err != nil || revoked.Status != LarkGrantStatusRevoked || revoked.RevokedAt == nil ||
		revoked.AuthorityVersion != reauthorized.AuthorityVersion+1 || len(revoked.SealedTokenSet) != larkGrantCredentialTombstoneBytes {
		t.Fatalf("RevokeWorkspaceLarkGrant() = %+v, %v", revoked, err)
	}
	idempotent, err := fixture.store.RevokeWorkspaceLarkGrant(t.Context(), RevokeWorkspaceLarkGrantCommand{
		GrantID: fixture.grant.ID, WorkspaceID: fixture.grant.WorkspaceID,
		UserID: fixture.grant.UserID, ReasonCode: "operator_revoked",
	})
	if err != nil || idempotent.AuthorityVersion != revoked.AuthorityVersion {
		t.Fatalf("idempotent RevokeWorkspaceLarkGrant() = %+v, %v", idempotent, err)
	}
}

func TestPostgreSQLManagedLarkAbandonedDispatchedRefreshRequiresReauthorization(t *testing.T) {
	fixture := newManagedLarkPostgresFixture(t, 870_000)
	quotedSchema := quoteIdentifier(fixture.schema)
	forceDue := fmt.Sprintf(`UPDATE %s.workspace_lark_grants
SET next_refresh_at = pg_catalog.clock_timestamp() - interval '1 second'
WHERE id = $1`, quotedSchema)
	if _, err := fixture.pool.Exec(t.Context(), forceDue, fixture.grant.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err := fixture.store.ClaimWorkspaceLarkGrantRefreshes(t.Context(), ClaimWorkspaceLarkGrantRefreshesCommand{
		Owner: "crashed-core", Limit: 1, LockTTL: time.Minute,
	})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	if _, err := fixture.store.MarkWorkspaceLarkGrantRefreshDispatched(t.Context(), MarkWorkspaceLarkGrantRefreshDispatchedCommand{
		GrantID: fixture.grant.ID, Owner: "crashed-core",
		ExpectedAuthorityVersion:  claimed[0].AuthorityVersion,
		ExpectedCredentialVersion: claimed[0].CredentialVersion, DispatchTTL: time.Minute,
	}); err != nil {
		t.Fatal(err)
	}
	expireLease := fmt.Sprintf(`UPDATE %s.workspace_lark_grants
SET refresh_lock_until = pg_catalog.clock_timestamp() - interval '1 second'
WHERE id = $1`, quotedSchema)
	if _, err := fixture.pool.Exec(t.Context(), expireLease, fixture.grant.ID); err != nil {
		t.Fatal(err)
	}
	fenced, err := fixture.store.FenceAbandonedWorkspaceLarkGrantRefreshes(t.Context())
	if err != nil || fenced != 1 {
		t.Fatalf("FenceAbandonedWorkspaceLarkGrantRefreshes() = %d, %v", fenced, err)
	}
	reclaimed, err := fixture.store.ClaimWorkspaceLarkGrantRefreshes(t.Context(), ClaimWorkspaceLarkGrantRefreshesCommand{
		Owner: "other-core", Limit: 1, LockTTL: time.Minute,
	})
	if err != nil || len(reclaimed) != 0 {
		t.Fatalf("ambiguous dispatched refresh was reclaimed = %+v, %v", reclaimed, err)
	}
}

func TestPostgreSQLManagedEgressAuditIsIdempotentAndAtomic(t *testing.T) {
	store, pool, schema := newPostgresStateStore(t)
	decidedAt := time.Now().UTC().Truncate(time.Microsecond)
	event := ManagedEgressAuditEvent{
		ID: stateTestUUID(860_000), DecidedAt: decidedAt,
		CapabilityID: "capability-860000", WorkspaceID: stateTestUUID(860_001),
		SessionID: stateTestUUID(860_002), RunID: stateTestUUID(860_003),
		RunAttemptID: stateTestUUID(860_004), RunAttemptGeneration: 2,
		ExecutionID: stateTestUUID(860_005), OperationID: stateTestUUID(860_006),
		SandboxID: stateTestUUID(860_007), TargetGeneration: 3,
		GrantID: stateTestUUID(860_008), GrantVersion: 4, TAEPSM: "prod.tae.agent-gateway",
		RequestHost: "open.feishu.cn", RequestPath: "/open-apis/docx/v1/documents/x",
		RequestMethod: "GET", Decision: "allow", ReasonCode: "allowed",
	}
	if err := store.RecordManagedEgressAuditEvent(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordManagedEgressAuditEvent(t.Context(), event); err != nil {
		t.Fatalf("exact audit retry error = %v", err)
	}
	assertStateTableCount(t, pool, schema, "managed_egress_audit_events", 1)
	assertStateTableCount(t, pool, schema, "managed_egress_audit_outbox", 1)
	conflict := event
	conflict.ReasonCode = "different-decision"
	if err := store.RecordManagedEgressAuditEvent(t.Context(), conflict); !HasStateErrorCode(err, ErrorIdempotencyConflict) {
		t.Fatalf("conflicting audit retry error = %v, want idempotency_conflict", err)
	}
	assertStateTableCount(t, pool, schema, "managed_egress_audit_events", 1)
	assertStateTableCount(t, pool, schema, "managed_egress_audit_outbox", 1)

	quotedSchema := quoteIdentifier(schema)
	functionName := quotedSchema + ".reject_managed_egress_audit_outbox"
	if _, err := pool.Exec(t.Context(), fmt.Sprintf(`
CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $body$
BEGIN
    RAISE EXCEPTION 'forced managed egress audit outbox failure';
END
$body$`, functionName)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), fmt.Sprintf(`
CREATE TRIGGER reject_managed_egress_audit_outbox_insert
BEFORE INSERT ON %s.managed_egress_audit_outbox
FOR EACH ROW EXECUTE FUNCTION %s()`, quotedSchema, functionName)); err != nil {
		t.Fatal(err)
	}
	atomicFailure := event
	atomicFailure.ID = stateTestUUID(860_010)
	if err := store.RecordManagedEgressAuditEvent(t.Context(), atomicFailure); err == nil {
		t.Fatal("forced audit outbox failure unexpectedly committed")
	}
	var eventCount, outboxCount int
	if err := pool.QueryRow(t.Context(), fmt.Sprintf(
		"SELECT pg_catalog.count(*) FROM %s.managed_egress_audit_events WHERE id = $1", quotedSchema,
	), atomicFailure.ID).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), fmt.Sprintf(
		"SELECT pg_catalog.count(*) FROM %s.managed_egress_audit_outbox WHERE audit_event_id = $1", quotedSchema,
	), atomicFailure.ID).Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 0 || outboxCount != 0 {
		t.Fatalf("audit/outbox rows after atomic failure = %d/%d, want 0/0", eventCount, outboxCount)
	}
}

func newManagedLarkPostgresFixture(t *testing.T, seed int) managedLarkPostgresFixture {
	t.Helper()
	store, pool, schema := newPostgresStateStore(t)
	workspaceID := stateTestUUID(seed)
	sessionID := stateTestUUID(seed + 1)
	actorID := stateTestUUID(seed + 11)
	insertStateTestSession(t, pool, schema, workspaceID, sessionID)
	quotedSchema := quoteIdentifier(schema)
	if _, err := pool.Exec(t.Context(), fmt.Sprintf(
		"INSERT INTO %s.users (id, status) VALUES ($1, 'active')", quotedSchema,
	), actorID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), fmt.Sprintf(
		"UPDATE %s.sessions SET creator_id = $1 WHERE id = $2", quotedSchema,
	), actorID, sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), fmt.Sprintf(
		"INSERT INTO %s.workspace_members (workspace_id, user_id, role) VALUES ($1, $2, 'developer')", quotedSchema,
	), workspaceID, actorID); err != nil {
		t.Fatal(err)
	}
	policy := sha256.Sum256([]byte(fmt.Sprintf("managed-lark-policy-%d", seed)))
	accessExpiry := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	refreshExpiry := accessExpiry.Add(30 * 24 * time.Hour)
	grant, err := store.CreateWorkspaceLarkGrant(t.Context(), CreateWorkspaceLarkGrantCommand{
		ID: stateTestUUID(seed + 2), WorkspaceID: workspaceID, UserID: actorID,
		PolicySHA256: policy, SealedTokenSet: bytes.Repeat([]byte{0x71}, 96),
		AccessExpiresAt: accessExpiry, RefreshExpiresAt: &refreshExpiry,
		NextRefreshAt: accessExpiry.Add(-10 * time.Minute),
	})
	if err != nil {
		t.Fatalf("CreateWorkspaceLarkGrant() error = %v", err)
	}
	idempotencyKey := fmt.Sprintf("managed-lark-run-%d", seed)
	binding, err := store.ResolveUserRunLarkEgressBinding(t.Context(), ResolveUserRunLarkEgressBindingCommand{
		WorkspaceID: workspaceID, SessionID: sessionID, ActorID: actorID, IdempotencyKey: idempotencyKey,
	})
	if err != nil || binding.GrantID != grant.ID || binding.GrantVersion != grant.AuthorityVersion ||
		binding.GrantUserID != actorID || binding.PolicySHA256 != policy {
		t.Fatalf("ResolveUserRunLarkEgressBinding() = %+v, %v", binding, err)
	}
	create := stateCreateRunCommand(seed+10, workspaceID, sessionID, idempotencyKey)
	create.ActorID = actorID
	create.LarkEgress = binding
	created := mustCreateStateRun(t, store, create)
	claimed := mustClaimStateRun(t, store, stateClaimRunCommand(seed+20, created.Run.ID, created.Run.Version, fmt.Sprintf("managed-lark-holder-%d", seed)))
	accepted, err := store.MarkTurnAccepted(t.Context(), MarkTurnAcceptedCommand{
		RunID: created.Run.ID, AttemptID: claimed.Attempt.ID,
		HolderID: claimed.Attempt.HolderID, Generation: claimed.Attempt.Generation,
		ExpectedRunVersion: claimed.Run.Version, ExpectedAttemptVersion: claimed.Attempt.Version,
		Record: stateTransitionRecord(seed + 30),
	})
	if err != nil {
		t.Fatalf("MarkTurnAccepted() error = %v", err)
	}
	running := executionTestRunningRun{Run: accepted.Run, Attempt: accepted.Attempt}

	reserve := managedSandboxTestReserve(seed+100, running)
	reserve.ProviderPSM = "prod.tae.agent-gateway"
	reserved, err := store.ReserveManagedSandbox(t.Context(), reserve)
	if err != nil {
		t.Fatal(err)
	}
	creating, _, err := store.BeginManagedSandboxCreate(t.Context(), BeginManagedSandboxCreateCommand{
		SandboxID: reserved.Sandbox.ID, Generation: reserved.Sandbox.Generation,
		ExpectedVersion: reserved.Sandbox.Version,
	})
	if err != nil {
		t.Fatal(err)
	}
	sandboxExpiry := time.Now().UTC().Add(30 * time.Minute).Truncate(time.Microsecond)
	ready, _, err := store.ObserveManagedSandbox(t.Context(), ObserveManagedSandboxCommand{
		SandboxID: creating.ID, Generation: creating.Generation, ExpectedVersion: creating.Version,
		ObservedState: ManagedSandboxReady, ProviderSessionRef: reserve.ProviderSessionRef,
		ExpiresAt: &sandboxExpiry,
	})
	if err != nil {
		t.Fatal(err)
	}
	ready, err = store.RenewManagedSandboxActivity(t.Context(), RenewManagedSandboxActivityCommand{
		SandboxID: ready.ID, Generation: ready.Generation, RunID: running.Run.ID,
		AttemptID: running.Attempt.ID, AttemptGeneration: running.Attempt.Generation,
		HolderID: running.Attempt.HolderID, ActivityTTL: 5 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	prepare := executionTestPrepareCommand(t, seed+200, running, "managed-lark-shell", 1)
	prepare.ExecutorID = ""
	prepare.EnvID = ready.EnvironmentID
	prepare.Target = ready.Target()
	preparedExecution, err := store.PrepareExecution(t.Context(), prepare)
	if err != nil {
		t.Fatal(err)
	}
	preparedOperation, err := store.PrepareOperation(t.Context(), executionTestPrepareOperationCommand(
		t, seed+210, running, preparedExecution.Execution, 1,
	))
	if err != nil {
		t.Fatal(err)
	}
	begin := executionTestBeginCommand(t, seed+220, running, preparedOperation, 0)
	begin.Target = ready.Target()
	dispatch, err := store.BeginOperationDispatch(t.Context(), begin)
	if err != nil || !dispatch.Began {
		t.Fatalf("BeginOperationDispatch() = %+v, %v", dispatch, err)
	}
	authority := ManagedLarkAuthorityQuery{
		WorkspaceID: workspaceID, SessionID: sessionID, ActorID: actorID,
		EnvironmentID: ready.EnvironmentID, RunID: running.Run.ID,
		AttemptID: running.Attempt.ID, AttemptGeneration: running.Attempt.Generation,
		ExecutionID: dispatch.Execution.ID, OperationID: dispatch.Operation.ID,
		SandboxID: ready.ID, TargetGeneration: ready.Generation,
		GrantID: binding.GrantID, GrantVersion: binding.GrantVersion,
		PolicySHA256: binding.PolicySHA256, TAEPSM: reserve.ProviderPSM,
	}
	return managedLarkPostgresFixture{
		store: store, pool: pool, schema: schema, running: running,
		grant: grant, binding: binding, sandbox: ready, dispatch: dispatch, authority: authority,
	}
}

func (fixture managedLarkPostgresFixture) authorityForResolve() ManagedLarkAuthorityQuery {
	query := fixture.authority
	query.GrantID = ""
	query.GrantVersion = 0
	query.PolicySHA256 = [32]byte{}
	query.TAEPSM = ""
	return query
}
