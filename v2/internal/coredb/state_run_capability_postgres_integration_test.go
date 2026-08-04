package coredb

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/braincatalog"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgreSQLRunCapabilityFreshCatalogIsLiveRevocable(t *testing.T) {
	fixture := newPostgresRunCapabilityFixture(t, 170_000)
	command := fixture.issuanceCommand()
	authority, err := fixture.store.ResolveRunCapabilityIssuance(t.Context(), command)
	if err != nil {
		t.Fatal(err)
	}
	if authority.WorkspaceID != fixture.workspaceID || authority.SessionID != fixture.sessionID ||
		authority.RunID != fixture.claim.Run.ID || authority.AttemptID != fixture.claim.Attempt.ID ||
		authority.ActorID != fixture.createCommand.ActorID || authority.HolderID != fixture.claim.Attempt.HolderID ||
		authority.Generation != fixture.claim.Attempt.Generation || authority.RunVersion != fixture.claim.Run.Version ||
		authority.AttemptVersion != fixture.claim.Attempt.Version || authority.ExecutorID != fixture.executor.ExecutorID ||
		authority.BrainToolCatalogID != fixture.freeze.CatalogID || authority.ToolCatalogDigest != fixture.freeze.CatalogDigest ||
		authority.LLMGateway != fixture.llmGateway || authority.AttemptCreatedAt.IsZero() || authority.DatabaseTime.IsZero() {
		t.Fatalf("fresh issuance authority = %+v", authority)
	}

	executorAuthorization := fixture.executorAuthorizationCommand()
	assertPostgresRunCapabilityAuthorized(t, fixture.store, executorAuthorization, RunStatusStarting, AttemptStatusLeased)
	modelAuthorization := fixture.modelAuthorizationCommand()
	assertPostgresRunCapabilityAuthorized(t, fixture.store, modelAuthorization, RunStatusStarting, AttemptStatusLeased)

	wrongCatalog := executorAuthorization
	wrongCatalog.ToolCatalogDigest = sha256.Sum256([]byte("wrong catalog"))
	assertPostgresRunCapabilityForbidden(t, fixture.store, wrongCatalog)
	wrongIssuance := command
	wrongIssuance.ToolCatalogDigest = wrongCatalog.ToolCatalogDigest
	if _, err := fixture.store.ResolveRunCapabilityIssuance(t.Context(), wrongIssuance); !HasStateErrorCode(err, ErrorForbidden) {
		t.Fatalf("catalog-drift issuance error = %v, want forbidden", err)
	}

	bound, err := fixture.store.BindBrainThreadCatalog(t.Context(), BindBrainThreadCatalogCommand{
		CatalogID: fixture.freeze.CatalogID, RunID: fixture.freeze.RunID, AttemptID: fixture.freeze.AttemptID,
		HolderID: fixture.freeze.HolderID, Generation: fixture.freeze.Generation,
		ExpectedRunVersion: fixture.freeze.ExpectedRunVersion, ExpectedAttemptVersion: fixture.freeze.ExpectedAttemptVersion,
		ExpectedCatalogVersion: fixture.frozen.Catalog.Version, ThreadID: "thread-capability-fresh",
	})
	if err != nil {
		t.Fatal(err)
	}
	if rebound, err := fixture.store.ResolveRunCapabilityIssuance(t.Context(), command); err != nil || rebound.BrainToolCatalogID != bound.Catalog.ID {
		t.Fatalf("issuance after thread bind = %+v, %v", rebound, err)
	}

	quotedSchema := quoteIdentifier(fixture.schema)
	updateRole := fmt.Sprintf("UPDATE %s.workspace_members SET role = $3 WHERE workspace_id = $1 AND user_id = $2", quotedSchema)
	if _, err := fixture.pool.Exec(t.Context(), updateRole, fixture.workspaceID, fixture.createCommand.ActorID, "viewer"); err != nil {
		t.Fatal(err)
	}
	assertPostgresRunCapabilityForbidden(t, fixture.store, executorAuthorization)
	if _, err := fixture.pool.Exec(t.Context(), updateRole, fixture.workspaceID, fixture.createCommand.ActorID, "developer"); err != nil {
		t.Fatal(err)
	}
	deleteMember := fmt.Sprintf("DELETE FROM %s.workspace_members WHERE workspace_id = $1 AND user_id = $2", quotedSchema)
	if _, err := fixture.pool.Exec(t.Context(), deleteMember, fixture.workspaceID, fixture.createCommand.ActorID); err != nil {
		t.Fatal(err)
	}
	assertPostgresRunCapabilityForbidden(t, fixture.store, modelAuthorization)
	insertMember := fmt.Sprintf("INSERT INTO %s.workspace_members (workspace_id, user_id, role) VALUES ($1, $2, 'developer')", quotedSchema)
	if _, err := fixture.pool.Exec(t.Context(), insertMember, fixture.workspaceID, fixture.createCommand.ActorID); err != nil {
		t.Fatal(err)
	}

	setGatewayVersion := fmt.Sprintf("UPDATE %s.workspace_llm_gateways SET version = $2 WHERE id = $1", quotedSchema)
	if _, err := fixture.pool.Exec(t.Context(), setGatewayVersion, fixture.llmGateway.GatewayID, fixture.llmGateway.ConfigVersion+1); err != nil {
		t.Fatal(err)
	}
	assertPostgresRunCapabilityForbidden(t, fixture.store, modelAuthorization)
	if _, err := fixture.pool.Exec(t.Context(), setGatewayVersion, fixture.llmGateway.GatewayID, fixture.llmGateway.ConfigVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(t.Context(), updateRole, fixture.workspaceID, fixture.createCommand.ActorID, "owner"); err != nil {
		t.Fatal(err)
	}
	disabled, err := fixture.store.DisableWorkspaceLLMGateway(t.Context(), DisableWorkspaceLLMGatewayCommand{
		WorkspaceID: fixture.workspaceID, GatewayID: fixture.llmGateway.GatewayID, ActorID: fixture.createCommand.ActorID,
	})
	if err != nil || !disabled.Changed || disabled.Gateway.Status != LLMGatewayStatusDisabled || disabled.Gateway.Default ||
		disabled.Gateway.Version != fixture.llmGateway.ConfigVersion+1 {
		t.Fatalf("disabled workspace LLM Gateway = %+v, %v", disabled, err)
	}
	repeatedDisable, err := fixture.store.DisableWorkspaceLLMGateway(t.Context(), DisableWorkspaceLLMGatewayCommand{
		WorkspaceID: fixture.workspaceID, GatewayID: fixture.llmGateway.GatewayID, ActorID: fixture.createCommand.ActorID,
	})
	if err != nil || repeatedDisable.Changed || repeatedDisable.Gateway.Version != disabled.Gateway.Version {
		t.Fatalf("repeated workspace LLM Gateway disable = %+v, %v", repeatedDisable, err)
	}
	assertPostgresRunCapabilityForbidden(t, fixture.store, modelAuthorization)
	restoreGateway := fmt.Sprintf("UPDATE %s.workspace_llm_gateways SET status = $2, is_default = TRUE, version = $3 WHERE id = $1", quotedSchema)
	if _, err := fixture.pool.Exec(t.Context(), restoreGateway, fixture.llmGateway.GatewayID, LLMGatewayStatusActive, fixture.llmGateway.ConfigVersion); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(t.Context(), updateRole, fixture.workspaceID, fixture.createCommand.ActorID, "developer"); err != nil {
		t.Fatal(err)
	}
	setGrantStatus := fmt.Sprintf("UPDATE %s.workspace_llm_gateway_grants SET status = $2 WHERE gateway_id = $1 AND user_id = $3", quotedSchema)
	if _, err := fixture.pool.Exec(t.Context(), setGrantStatus, fixture.llmGateway.GatewayID, LLMGatewayGrantStatusRevoked, fixture.llmGateway.GrantUserID); err != nil {
		t.Fatal(err)
	}
	assertPostgresRunCapabilityForbidden(t, fixture.store, modelAuthorization)
	if _, err := fixture.pool.Exec(t.Context(), setGrantStatus, fixture.llmGateway.GatewayID, LLMGatewayGrantStatusActive, fixture.llmGateway.GrantUserID); err != nil {
		t.Fatal(err)
	}

	setExecutorStatus := fmt.Sprintf("UPDATE %s.executors SET status = $2 WHERE id = $1", quotedSchema)
	if _, err := fixture.pool.Exec(t.Context(), setExecutorStatus, fixture.executor.ExecutorID, ExecutorStatusOffline); err != nil {
		t.Fatal(err)
	}
	assertPostgresRunCapabilityAuthorized(t, fixture.store, executorAuthorization, RunStatusStarting, AttemptStatusLeased)
	if _, err := fixture.pool.Exec(t.Context(), setExecutorStatus, fixture.executor.ExecutorID, ExecutorStatusOnline); err != nil {
		t.Fatal(err)
	}

	setConnectionExpiry := fmt.Sprintf("UPDATE %s.executor_connections SET expires_at = $2 WHERE executor_id = $1", quotedSchema)
	if _, err := fixture.pool.Exec(t.Context(), setConnectionExpiry, fixture.executor.ExecutorID, time.Now().UTC().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	assertPostgresRunCapabilityAuthorized(t, fixture.store, executorAuthorization, RunStatusStarting, AttemptStatusLeased)
	if _, err := fixture.pool.Exec(t.Context(), setConnectionExpiry, fixture.executor.ExecutorID, time.Now().UTC().Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}

	setInsecure := fmt.Sprintf("UPDATE %s.executor_environments SET insecure_dev = $2 WHERE executor_id = $1", quotedSchema)
	if _, err := fixture.pool.Exec(t.Context(), setInsecure, fixture.executor.ExecutorID, true); err != nil {
		t.Fatal(err)
	}
	assertPostgresRunCapabilityAuthorized(t, fixture.store, executorAuthorization, RunStatusStarting, AttemptStatusLeased)
	if _, err := fixture.pool.Exec(t.Context(), setInsecure, fixture.executor.ExecutorID, false); err != nil {
		t.Fatal(err)
	}

	// Executor availability is optional run authority. Capability issuance and
	// MCP session authorization must remain live while the workspace has no
	// connected executor or production environment; the environment lookup and
	// operation-dispatch transaction enforce those requirements only when an
	// executor tool is actually called.
	if _, err := fixture.pool.Exec(t.Context(), fmt.Sprintf("DELETE FROM %s.executor_connections WHERE executor_id = $1", quotedSchema), fixture.executor.ExecutorID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(t.Context(), fmt.Sprintf("DELETE FROM %s.executor_environments WHERE executor_id = $1", quotedSchema), fixture.executor.ExecutorID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.pool.Exec(t.Context(), setExecutorStatus, fixture.executor.ExecutorID, ExecutorStatusEnrolling); err != nil {
		t.Fatal(err)
	}
	environments, err := fixture.store.ListOnlineExecutorEnvironments(t.Context(), ListOnlineExecutorEnvironmentsQuery{
		WorkspaceID: fixture.workspaceID, ExecutorID: fixture.executor.ExecutorID,
	})
	if err != nil || len(environments) != 0 {
		t.Fatalf("offline executor environment projection = %+v, %v; want empty", environments, err)
	}
	if _, err := fixture.store.ResolveRunCapabilityIssuance(t.Context(), command); err != nil {
		t.Fatalf("capability issuance without an executor = %v", err)
	}
	assertPostgresRunCapabilityAuthorized(t, fixture.store, executorAuthorization, RunStatusStarting, AttemptStatusLeased)
	assertPostgresRunCapabilityAuthorized(t, fixture.store, modelAuthorization, RunStatusStarting, AttemptStatusLeased)

	setPolicyVersion := fmt.Sprintf("UPDATE %s.run_launch_states SET executor_policy_version = $2 WHERE run_id = $1", quotedSchema)
	if _, err := fixture.pool.Exec(t.Context(), setPolicyVersion, fixture.claim.Run.ID, "executor-policy/drift"); err != nil {
		t.Fatal(err)
	}
	assertPostgresRunCapabilityForbidden(t, fixture.store, executorAuthorization)
	if _, err := fixture.pool.Exec(t.Context(), setPolicyVersion, fixture.claim.Run.ID, fixture.createCommand.ExecutorPolicy.Version); err != nil {
		t.Fatal(err)
	}

	expireStateLeases(t, fixture.pool, fixture.schema, fixture.sessionID, fixture.claim.Attempt.ID)
	assertPostgresRunCapabilityForbidden(t, fixture.store, modelAuthorization)
	restoreStateLeases(t, fixture)

	accepted, err := fixture.store.MarkTurnAccepted(t.Context(), MarkTurnAcceptedCommand{
		RunID: fixture.claim.Run.ID, AttemptID: fixture.claim.Attempt.ID,
		HolderID: fixture.claim.Attempt.HolderID, Generation: fixture.claim.Attempt.Generation,
		ExpectedRunVersion: fixture.claim.Run.Version, ExpectedAttemptVersion: fixture.claim.Attempt.Version,
		Record: stateTransitionRecord(170_900),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertPostgresRunCapabilityAuthorized(t, fixture.store, executorAuthorization, RunStatusRunning, AttemptStatusRunning)
	assertPostgresRunCapabilityAuthorized(t, fixture.store, modelAuthorization, RunStatusRunning, AttemptStatusRunning)
	if accepted.Run.Version != executorAuthorization.ExpectedRunVersion ||
		accepted.Attempt.Version != executorAuthorization.ExpectedAttemptVersion {
		t.Fatalf("accepted versions = %d/%d, token expected %d/%d", accepted.Run.Version, accepted.Attempt.Version, executorAuthorization.ExpectedRunVersion, executorAuthorization.ExpectedAttemptVersion)
	}

	if _, err := fixture.store.CancelRun(t.Context(), CancelRunCommand{
		WorkspaceID: fixture.workspaceID, RunID: fixture.claim.Run.ID,
		ActorID: fixture.createCommand.ActorID, Record: stateTransitionRecord(170_910),
	}); err != nil {
		t.Fatal(err)
	}
	assertPostgresRunCapabilityForbidden(t, fixture.store, executorAuthorization)
	assertPostgresRunCapabilityForbidden(t, fixture.store, modelAuthorization)
}

func TestPostgreSQLRunCapabilityResumeUsesCommittedCheckpointAndFencesFinalizing(t *testing.T) {
	fixture := newPostgresRunCapabilityFixture(t, 180_000)
	const threadID = "thread-capability-resume"
	bound, err := fixture.store.BindBrainThreadCatalog(t.Context(), BindBrainThreadCatalogCommand{
		CatalogID: fixture.freeze.CatalogID, RunID: fixture.freeze.RunID, AttemptID: fixture.freeze.AttemptID,
		HolderID: fixture.freeze.HolderID, Generation: fixture.freeze.Generation,
		ExpectedRunVersion: fixture.freeze.ExpectedRunVersion, ExpectedAttemptVersion: fixture.freeze.ExpectedAttemptVersion,
		ExpectedCatalogVersion: fixture.frozen.Catalog.Version, ThreadID: threadID,
	})
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := fixture.store.MarkTurnAccepted(t.Context(), MarkTurnAcceptedCommand{
		RunID: fixture.claim.Run.ID, AttemptID: fixture.claim.Attempt.ID,
		HolderID: fixture.claim.Attempt.HolderID, Generation: fixture.claim.Attempt.Generation,
		ExpectedRunVersion: fixture.claim.Run.Version, ExpectedAttemptVersion: fixture.claim.Attempt.Version,
		Record: stateTransitionRecord(180_900),
	})
	if err != nil {
		t.Fatal(err)
	}
	finalizing, err := fixture.store.BeginRunFinalization(t.Context(), BeginRunFinalizationCommand{
		RunID: accepted.Run.ID, AttemptID: accepted.Attempt.ID, HolderID: accepted.Attempt.HolderID,
		Generation: accepted.Attempt.Generation, ExpectedRunVersion: accepted.Run.Version,
		ExpectedAttemptVersion: accepted.Attempt.Version, ThreadID: threadID, TurnID: "turn-capability-source",
		Record: stateTransitionRecord(180_910),
	})
	if err != nil {
		t.Fatal(err)
	}
	commit := CommitCheckpointAndTerminalRunCommand{
		RunID: finalizing.Run.ID, AttemptID: finalizing.Attempt.ID, HolderID: finalizing.Attempt.HolderID,
		Generation: finalizing.Attempt.Generation, ExpectedRunVersion: finalizing.Run.Version,
		ExpectedAttemptVersion: finalizing.Attempt.Version,
		CheckpointID:           stateTestUUID(180_920), BrainToolCatalogID: bound.Catalog.ID,
		ThreadID: threadID, TurnID: "turn-capability-source",
		ManifestDigest: sha256.Sum256([]byte("capability source manifest")), CatalogDigest: bound.Catalog.CatalogDigest,
		Object: ObjectPointer{
			ObjectID: stateTestUUID(180_921), SHA256: sha256.Sum256([]byte("capability source object")),
			Size: 4096, MediaType: "application/vnd.agentserver.codex-checkpoint.v1",
		},
		CodexRuntimeManifestDigest: sha256.Sum256([]byte("capability runtime")),
		CheckpointAllowlistVersion: 1, Record: stateTransitionRecord(180_930),
	}
	committed, err := fixture.store.CommitCheckpointAndTerminalRun(t.Context(), commit)
	if err != nil {
		t.Fatal(err)
	}

	resumeCommand := stateCreateRunCommand(181_000, fixture.workspaceID, fixture.sessionID, "capability-resume")
	resumeCommand.ActorID = fixture.createCommand.ActorID
	resumeCommand.ExpectedSessionVersion = committed.SessionVersion
	resumeCommand.ExecutorPolicy = fixture.createCommand.ExecutorPolicy
	resumeCommand.LLMGateway = fixture.llmGateway
	resume := mustCreateStateRun(t, fixture.store, resumeCommand)
	resumeClaim := mustClaimStateRun(t, fixture.store, stateClaimRunCommand(181_010, resume.Run.ID, resume.Run.Version, "capability-resume-holder"))
	resumeIssuance := ResolveRunCapabilityIssuanceCommand{
		WorkspaceID: fixture.workspaceID, SessionID: fixture.sessionID, RunID: resume.Run.ID,
		AttemptID: resumeClaim.Attempt.ID, HolderID: resumeClaim.Attempt.HolderID,
		Generation: resumeClaim.Attempt.Generation, ExpectedRunVersion: resumeClaim.Run.Version,
		ExpectedAttemptVersion: resumeClaim.Attempt.Version, ExecutorID: fixture.executor.ExecutorID,
		BrainToolCatalogID: bound.Catalog.ID, ToolCatalogDigest: bound.Catalog.CatalogDigest,
		LLMGateway: fixture.llmGateway,
	}
	resumeAuthority, err := fixture.store.ResolveRunCapabilityIssuance(t.Context(), resumeIssuance)
	if err != nil {
		t.Fatal(err)
	}
	if resumeAuthority.BrainToolCatalogID != bound.Catalog.ID || resumeAuthority.ToolCatalogDigest != bound.Catalog.CatalogDigest {
		t.Fatalf("resume issuance authority = %+v", resumeAuthority)
	}
	assertStateTableCount(t, fixture.pool, fixture.schema, "brain_tool_catalogs", 1)

	resumeExecutorAuthorization := AuthorizeRunCapabilityCommand{
		Audience: RunCapabilityAudienceExecutorMCP, CapabilityID: stateTestUUID(181_020),
		WorkspaceID: fixture.workspaceID, SessionID: fixture.sessionID, RunID: resume.Run.ID,
		AttemptID: resumeClaim.Attempt.ID, ActorID: resumeCommand.ActorID,
		HolderID: resumeClaim.Attempt.HolderID, Generation: resumeClaim.Attempt.Generation,
		ExecutorID: fixture.executor.ExecutorID, ToolCatalogDigest: bound.Catalog.CatalogDigest,
		ExpectedRunVersion: resumeClaim.Run.Version + 1, ExpectedAttemptVersion: resumeClaim.Attempt.Version + 1,
	}
	resumeModelAuthorization := resumeExecutorAuthorization
	resumeModelAuthorization.Audience = RunCapabilityAudienceLLMProxy
	resumeModelAuthorization.CapabilityID = stateTestUUID(181_021)
	resumeModelAuthorization.ExecutorID = ""
	resumeModelAuthorization.ToolCatalogDigest = [sha256.Size]byte{}
	resumeModelAuthorization.ExpectedRunVersion = 0
	resumeModelAuthorization.ExpectedAttemptVersion = 0
	resumeModelAuthorization.LLMGateway = fixture.llmGateway
	assertPostgresRunCapabilityAuthorized(t, fixture.store, resumeExecutorAuthorization, RunStatusStarting, AttemptStatusLeased)
	assertPostgresRunCapabilityAuthorized(t, fixture.store, resumeModelAuthorization, RunStatusStarting, AttemptStatusLeased)

	resumeAccepted, err := fixture.store.MarkTurnAccepted(t.Context(), MarkTurnAcceptedCommand{
		RunID: resume.Run.ID, AttemptID: resumeClaim.Attempt.ID, HolderID: resumeClaim.Attempt.HolderID,
		Generation: resumeClaim.Attempt.Generation, ExpectedRunVersion: resumeClaim.Run.Version,
		ExpectedAttemptVersion: resumeClaim.Attempt.Version, Record: stateTransitionRecord(181_030),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertPostgresRunCapabilityAuthorized(t, fixture.store, resumeExecutorAuthorization, RunStatusRunning, AttemptStatusRunning)
	if _, err := fixture.store.BeginRunFinalization(t.Context(), BeginRunFinalizationCommand{
		RunID: resumeAccepted.Run.ID, AttemptID: resumeAccepted.Attempt.ID, HolderID: resumeAccepted.Attempt.HolderID,
		Generation: resumeAccepted.Attempt.Generation, ExpectedRunVersion: resumeAccepted.Run.Version,
		ExpectedAttemptVersion: resumeAccepted.Attempt.Version, ThreadID: threadID, TurnID: "turn-capability-resume",
		Record: stateTransitionRecord(181_040),
	}); err != nil {
		t.Fatal(err)
	}
	assertPostgresRunCapabilityForbidden(t, fixture.store, resumeExecutorAuthorization)
	assertPostgresRunCapabilityForbidden(t, fixture.store, resumeModelAuthorization)
}

type postgresRunCapabilityFixture struct {
	store         *StateStore
	pool          *pgxpool.Pool
	schema        string
	workspaceID   string
	sessionID     string
	createCommand CreateRunCommand
	claim         ClaimQueuedRunResult
	freeze        FreezeBrainToolCatalogCommand
	frozen        FreezeBrainToolCatalogResult
	executor      AcquireExecutorConnectionCommand
	llmGateway    RunLLMGatewayBinding
}

func newPostgresRunCapabilityFixture(t *testing.T, seed int) postgresRunCapabilityFixture {
	t.Helper()
	store, pool, schema := newPostgresStateStore(t)
	workspaceID := stateTestUUID(seed)
	sessionID := stateTestUUID(seed + 1)
	insertStateTestSession(t, pool, schema, workspaceID, sessionID)
	createCommand := stateCreateRunCommand(seed+10, workspaceID, sessionID, fmt.Sprintf("capability-%d", seed))
	userQuery := fmt.Sprintf("INSERT INTO %s.users (id, status) VALUES ($1, 'active')", quoteIdentifier(schema))
	if _, err := pool.Exec(t.Context(), userQuery, createCommand.ActorID); err != nil {
		t.Fatal(err)
	}
	memberQuery := fmt.Sprintf("INSERT INTO %s.workspace_members (workspace_id, user_id, role) VALUES ($1, $2, 'developer')", quoteIdentifier(schema))
	if _, err := pool.Exec(t.Context(), memberQuery, workspaceID, createCommand.ActorID); err != nil {
		t.Fatal(err)
	}
	llmGateway := RunLLMGatewayBinding{
		GatewayID: stateTestUUID(seed + 500), ConfigVersion: 1,
		GrantUserID: createCommand.ActorID, Model: "capability-test-model",
	}
	quotedSchema := quoteIdentifier(schema)
	insertGateway := fmt.Sprintf(`INSERT INTO %s.workspace_llm_gateways
    (id, workspace_id, name, responses_url, oidc_issuer, oidc_client_id, oidc_scopes,
     bearer_token_type, default_model, status, is_default, version, created_by)
VALUES ($1, $2, $3, 'https://llm.example.com/v1/responses', 'https://id.example.com',
        'capability-test-client', 'offline_access openid', 'id_token', $4, 'active', TRUE, $5, $6)`, quotedSchema)
	if _, err := pool.Exec(t.Context(), insertGateway, llmGateway.GatewayID, workspaceID,
		fmt.Sprintf("capability-gateway-%d", seed), llmGateway.Model, llmGateway.ConfigVersion, createCommand.ActorID); err != nil {
		t.Fatal(err)
	}
	insertGrant := fmt.Sprintf(`INSERT INTO %s.workspace_llm_gateway_grants
    (id, gateway_id, workspace_id, user_id, oidc_issuer, oidc_subject, status,
     sealed_token_set, bearer_expires_at)
VALUES ($1, $2, $3, $4, 'https://id.example.com', $5, 'active', $6,
        pg_catalog.clock_timestamp() + interval '30 minutes')`, quotedSchema)
	if _, err := pool.Exec(t.Context(), insertGrant, stateTestUUID(seed+501), llmGateway.GatewayID,
		workspaceID, createCommand.ActorID, fmt.Sprintf("capability-subject-%d", seed), make([]byte, 64)); err != nil {
		t.Fatal(err)
	}
	createCommand.LLMGateway = llmGateway
	created := mustCreateStateRun(t, store, createCommand)
	claim := mustClaimStateRun(t, store, stateClaimRunCommand(seed+20, created.Run.ID, created.Run.Version, fmt.Sprintf("capability-holder-%d", seed)))
	catalog, err := braincatalog.BuildCatalog("executor", "Production executor tools.", []braincatalog.ToolDescriptor{{
		Name: "read_file", Description: "Read one file.", InputSchema: json.RawMessage(`{"type":"object"}`),
	}}, braincatalog.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	freeze := FreezeBrainToolCatalogCommand{
		CatalogID: stateTestUUID(seed + 30), WorkspaceID: workspaceID, SessionID: sessionID,
		RunID: created.Run.ID, AttemptID: claim.Attempt.ID, HolderID: claim.Attempt.HolderID,
		Generation: claim.Attempt.Generation, ExpectedRunVersion: claim.Run.Version,
		ExpectedAttemptVersion: claim.Attempt.Version, ContractVersion: "executor-mcp/1.1",
		CanonicalizerVersion: braincatalog.CatalogCanonicalizer,
		CanonicalCatalog:     catalog.CanonicalBytes(), CatalogDigest: catalog.DigestSHA256(),
		PolicyVersion: createCommand.ExecutorPolicy.Version, PolicyContextDigest: createCommand.ExecutorPolicy.ContextDigest,
	}
	frozen, err := store.FreezeBrainToolCatalog(t.Context(), freeze)
	if err != nil {
		t.Fatal(err)
	}
	executor := insertExecutorConnectionFixture(t, pool, schema, seed+100)
	updateExecutorWorkspace := fmt.Sprintf("UPDATE %s.executors SET workspace_id = $2 WHERE id = $1", quoteIdentifier(schema))
	if _, err := pool.Exec(t.Context(), updateExecutorWorkspace, executor.ExecutorID, workspaceID); err != nil {
		t.Fatal(err)
	}
	acquired, err := store.AcquireExecutorConnection(t.Context(), executor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ActivateExecutorConnection(t.Context(), ActivateExecutorConnectionCommand{
		ExecutorID: executor.ExecutorID, SessionID: executor.SessionID,
		GatewayInstanceID: executor.GatewayInstanceID, Generation: acquired.Connection.Generation,
		Environments: executor.Environments,
	}); err != nil {
		t.Fatal(err)
	}
	return postgresRunCapabilityFixture{
		store: store, pool: pool, schema: schema, workspaceID: workspaceID, sessionID: sessionID,
		createCommand: createCommand, claim: claim, freeze: freeze, frozen: frozen, executor: executor,
		llmGateway: llmGateway,
	}
}

func (fixture postgresRunCapabilityFixture) issuanceCommand() ResolveRunCapabilityIssuanceCommand {
	return ResolveRunCapabilityIssuanceCommand{
		WorkspaceID: fixture.workspaceID, SessionID: fixture.sessionID, RunID: fixture.claim.Run.ID,
		AttemptID: fixture.claim.Attempt.ID, HolderID: fixture.claim.Attempt.HolderID,
		Generation: fixture.claim.Attempt.Generation, ExpectedRunVersion: fixture.claim.Run.Version,
		ExpectedAttemptVersion: fixture.claim.Attempt.Version, ExecutorID: fixture.executor.ExecutorID,
		BrainToolCatalogID: fixture.freeze.CatalogID, ToolCatalogDigest: fixture.freeze.CatalogDigest,
		LLMGateway: fixture.llmGateway,
	}
}

func (fixture postgresRunCapabilityFixture) executorAuthorizationCommand() AuthorizeRunCapabilityCommand {
	return AuthorizeRunCapabilityCommand{
		Audience: RunCapabilityAudienceExecutorMCP, CapabilityID: stateTestUUID(179_990),
		WorkspaceID: fixture.workspaceID, SessionID: fixture.sessionID, RunID: fixture.claim.Run.ID,
		AttemptID: fixture.claim.Attempt.ID, ActorID: fixture.createCommand.ActorID,
		HolderID: fixture.claim.Attempt.HolderID, Generation: fixture.claim.Attempt.Generation,
		ExecutorID: fixture.executor.ExecutorID, ToolCatalogDigest: fixture.freeze.CatalogDigest,
		ExpectedRunVersion: fixture.claim.Run.Version + 1, ExpectedAttemptVersion: fixture.claim.Attempt.Version + 1,
	}
}

func (fixture postgresRunCapabilityFixture) modelAuthorizationCommand() AuthorizeRunCapabilityCommand {
	command := fixture.executorAuthorizationCommand()
	command.Audience = RunCapabilityAudienceLLMProxy
	command.CapabilityID = stateTestUUID(179_991)
	command.ExecutorID = ""
	command.ToolCatalogDigest = [sha256.Size]byte{}
	command.ExpectedRunVersion = 0
	command.ExpectedAttemptVersion = 0
	command.LLMGateway = fixture.llmGateway
	return command
}

func assertPostgresRunCapabilityAuthorized(
	t *testing.T,
	store *StateStore,
	command AuthorizeRunCapabilityCommand,
	wantRunStatus, wantAttemptStatus string,
) {
	t.Helper()
	result, err := store.AuthorizeRunCapability(t.Context(), command)
	if err != nil {
		t.Fatal(err)
	}
	if result.RunStatus != wantRunStatus || result.AttemptStatus != wantAttemptStatus || result.DatabaseTime.IsZero() {
		t.Fatalf("authorized run capability = %+v", result)
	}
}

func assertPostgresRunCapabilityForbidden(t *testing.T, store *StateStore, command AuthorizeRunCapabilityCommand) {
	t.Helper()
	if _, err := store.AuthorizeRunCapability(t.Context(), command); !HasStateErrorCode(err, ErrorForbidden) {
		t.Fatalf("AuthorizeRunCapability() error = %v, want forbidden", err)
	}
}

func restoreStateLeases(t *testing.T, fixture postgresRunCapabilityFixture) {
	t.Helper()
	quotedSchema := quoteIdentifier(fixture.schema)
	for table, key := range map[string]string{
		"session_leases": fixture.sessionID,
		"attempt_leases": fixture.claim.Attempt.ID,
	} {
		query := fmt.Sprintf("UPDATE %s.%s SET expires_at = pg_catalog.clock_timestamp() + interval '5 minutes' WHERE %s = $1", quotedSchema, quoteIdentifier(table), map[string]string{
			"session_leases": "session_id", "attempt_leases": "run_attempt_id",
		}[table])
		if _, err := fixture.pool.Exec(t.Context(), query, key); err != nil {
			t.Fatal(err)
		}
	}
}
