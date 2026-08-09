package harnesspool

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/executorgateway/mcpcontract"
	"github.com/agentserver/agentserver/v2/internal/runmanifest"
)

func TestConfiguredRunLaunchInputResolverCombinesAndCopiesAuthorityState(t *testing.T) {
	base := testRunLaunchInputs()
	proposal, err := BuildExecutorCatalog(base.ExecutorCatalogPolicy)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := &runmanifest.PreviousCheckpoint{
		CheckpointID: "47000000-0000-4000-8000-000000000004",
		RunID:        "4c000000-0000-4000-8000-000000000004", RunAttemptID: "4d000000-0000-4000-8000-000000000004",
		RunAttemptGeneration: 2, ThreadID: "thread-previous", TurnID: "turn-previous",
		ManifestDigest: strings.Repeat("d", 64), CatalogDigest: proposal.Catalog.Digest(),
		CodexRuntimeManifestDigest: base.CodexRuntimeManifestDigest,
		CheckpointAllowlistVersion: int64(base.CheckpointAllowlistVersion),
		Object: runmanifest.ObjectPointer{
			ObjectID: "48000000-0000-4000-8000-000000000004", SHA256: strings.Repeat("e", 64),
			SizeBytes: 1024, MediaType: "application/vnd.agentserver.codex-checkpoint.v1",
		},
	}
	source := &recordingRunLaunchStateSource{state: RunLaunchState{
		Prompt: base.Prompt,
		PreviousCheckpoint: &RunLaunchCheckpoint{
			Checkpoint: *checkpoint,
			Catalog:    resolverCheckpointCatalog(proposal, checkpoint.ThreadID),
		},
		ExecutorPolicy: base.ExecutorCatalogPolicy,
	}}
	resolver, err := NewConfiguredRunLaunchInputResolver(source, launchProfileFromInputs(base))
	if err != nil {
		t.Fatal(err)
	}
	scheduled := ScheduledRunAttempt{Dispatch: testControllerDispatch("starting"), Claim: testControllerClaim()}
	inputs, err := resolver.ResolveRunLaunch(t.Context(), scheduled)
	if err != nil {
		t.Fatal(err)
	}
	if source.calls != 1 || source.scheduled.Claim.RunAttempt.RunAttemptID != testRunAttemptID ||
		inputs.PreviousCheckpoint == checkpoint || inputs.PreviousCheckpoint.ThreadID != checkpoint.ThreadID ||
		inputs.PreviousBrainToolCatalog == nil ||
		inputs.ExecutorMCPEndpoint != base.ExecutorMCPEndpoint ||
		len(inputs.ExecutorCatalogPolicy.AllowedTools) != len(base.ExecutorCatalogPolicy.AllowedTools) {
		t.Fatalf("resolved inputs/source = %+v / %+v", inputs, source)
	}
	inputs.PreviousCheckpoint.ThreadID = "mutated"
	inputs.PreviousBrainToolCatalog.CanonicalCatalog[0] = '!'
	inputs.ExecutorCatalogPolicy.AllowedTools[0] = mcpcontract.ToolShell
	if checkpoint.ThreadID != "thread-previous" || source.state.PreviousCheckpoint.Checkpoint.ThreadID != "thread-previous" ||
		source.state.PreviousCheckpoint.Catalog.CanonicalCatalog[0] == '!' ||
		source.state.ExecutorPolicy.AllowedTools[0] != base.ExecutorCatalogPolicy.AllowedTools[0] {
		t.Fatal("resolved launch inputs alias authority state")
	}
}

func TestConfiguredRunLaunchInputResolverRejectsProfileAndDynamicDrift(t *testing.T) {
	base := testRunLaunchInputs()
	profile := launchProfileFromInputs(base)
	profile.ExecutorMCPEndpoint = "http://executor.invalid/mcp"
	if _, err := NewConfiguredRunLaunchInputResolver(&recordingRunLaunchStateSource{}, profile); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("invalid profile error = %v", err)
	}

	proposal, err := BuildExecutorCatalog(base.ExecutorCatalogPolicy)
	if err != nil {
		t.Fatal(err)
	}
	source := &recordingRunLaunchStateSource{state: RunLaunchState{
		Prompt: base.Prompt,
		PreviousCheckpoint: &RunLaunchCheckpoint{
			Checkpoint: runmanifest.PreviousCheckpoint{
				CheckpointID: "47000000-0000-4000-8000-000000000004",
				RunID:        "4c000000-0000-4000-8000-000000000004", RunAttemptID: "4d000000-0000-4000-8000-000000000004",
				RunAttemptGeneration: 2, ThreadID: "thread-previous", TurnID: "turn-previous",
				ManifestDigest: strings.Repeat("d", 64), CatalogDigest: proposal.Catalog.Digest(),
				CodexRuntimeManifestDigest: base.CodexRuntimeManifestDigest,
				CheckpointAllowlistVersion: int64(base.CheckpointAllowlistVersion),
				Object: runmanifest.ObjectPointer{
					ObjectID: "48000000-0000-4000-8000-000000000004", SHA256: strings.Repeat("e", 64),
					SizeBytes: 1024, MediaType: "application/vnd.agentserver.codex-checkpoint.v1",
				},
			},
			Catalog: resolverCheckpointCatalog(proposal, "thread-previous"),
		},
		ExecutorPolicy: ExecutorCatalogPolicy{
			Version: "changed-policy", ContextDigest: sha256.Sum256([]byte("changed")),
			AllowedTools: []string{mcpcontract.ToolListEnvironments},
		},
	}}
	resolver, err := NewConfiguredRunLaunchInputResolver(source, launchProfileFromInputs(base))
	if err != nil {
		t.Fatal(err)
	}
	_, err = resolver.ResolveRunLaunch(t.Context(), ScheduledRunAttempt{Dispatch: testControllerDispatch("starting"), Claim: testControllerClaim()})
	if err == nil || !strings.Contains(err.Error(), "catalog authority") {
		t.Fatalf("checkpoint/catalog drift error = %v", err)
	}

	source.state.ExecutorPolicy = base.ExecutorCatalogPolicy
	source.state.PreviousCheckpoint.Checkpoint.CodexRuntimeManifestDigest = strings.Repeat("f", 64)
	_, err = resolver.ResolveRunLaunch(t.Context(), ScheduledRunAttempt{Dispatch: testControllerDispatch("starting"), Claim: testControllerClaim()})
	if err == nil || !strings.Contains(err.Error(), "runtime manifest or allowlist") {
		t.Fatalf("checkpoint/runtime drift error = %v", err)
	}

	source.err = errors.New("core launch state unavailable")
	if _, err := resolver.ResolveRunLaunch(t.Context(), ScheduledRunAttempt{Dispatch: testControllerDispatch("starting"), Claim: testControllerClaim()}); err == nil || !strings.Contains(err.Error(), "core launch state unavailable") {
		t.Fatalf("source error = %v", err)
	}
	if _, err := resolver.ResolveRunLaunch(nil, ScheduledRunAttempt{}); err == nil || !strings.Contains(err.Error(), "context") {
		t.Fatalf("nil context error = %v", err)
	}
}

func TestConfiguredRunLaunchInputResolverBindsManagedPackAcrossResume(t *testing.T) {
	base := testRunLaunchInputs()
	managed := poolTestManagedSandboxSpec()
	base.ManagedSandbox = &managed
	profile := launchProfileFromInputs(base)
	profile.ManagedSandbox = &managed
	proposal, err := BuildExecutorCatalog(base.ExecutorCatalogPolicy)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := runmanifest.PreviousCheckpoint{
		CheckpointID: "47000000-0000-4000-8000-000000000004",
		RunID:        "4c000000-0000-4000-8000-000000000004", RunAttemptID: "4d000000-0000-4000-8000-000000000004",
		RunAttemptGeneration: 2, ThreadID: "thread-managed", TurnID: "turn-managed",
		ManifestDigest: strings.Repeat("d", 64), CatalogDigest: proposal.Catalog.Digest(),
		CodexRuntimeManifestDigest: base.CodexRuntimeManifestDigest,
		CheckpointAllowlistVersion: int64(base.CheckpointAllowlistVersion),
		PackSetDigest:              managed.PackSetDigest,
		Object: runmanifest.ObjectPointer{
			ObjectID: "48000000-0000-4000-8000-000000000004", SHA256: strings.Repeat("e", 64),
			SizeBytes: 1024, MediaType: "application/vnd.agentserver.codex-checkpoint.v1",
		},
	}
	source := &recordingRunLaunchStateSource{state: RunLaunchState{
		Prompt: base.Prompt,
		PreviousCheckpoint: &RunLaunchCheckpoint{
			Checkpoint: checkpoint,
			Catalog:    resolverCheckpointCatalog(proposal, checkpoint.ThreadID),
		},
		ExecutorPolicy: base.ExecutorCatalogPolicy,
	}}
	resolver, err := NewConfiguredRunLaunchInputResolver(source, profile)
	if err != nil {
		t.Fatal(err)
	}
	scheduled := ScheduledRunAttempt{Dispatch: testControllerDispatch("starting"), Claim: testControllerClaim()}
	resolved, err := resolver.ResolveRunLaunch(t.Context(), scheduled)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ManagedSandbox == nil || resolved.ManagedSandbox.PackSetDigest != managed.PackSetDigest {
		t.Fatalf("resolved managed sandbox = %+v", resolved.ManagedSandbox)
	}

	source.state.PreviousCheckpoint.Checkpoint.PackSetDigest = strings.Repeat("f", 64)
	if _, err := resolver.ResolveRunLaunch(t.Context(), scheduled); err == nil || !strings.Contains(err.Error(), "pack-set digest") {
		t.Fatalf("changed pack-set resume error = %v", err)
	}
}

func resolverCheckpointCatalog(proposal ExecutorCatalogProposal, threadID string) BrainToolCatalog {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	return BrainToolCatalog{
		CatalogID:   "49000000-0000-4000-8000-000000000004",
		WorkspaceID: "40000000-0000-4000-8000-000000000004", SessionID: testSessionID,
		CreatedRunID:             "4a000000-0000-4000-8000-000000000004",
		CreatedRunAttemptID:      "4b000000-0000-4000-8000-000000000004",
		CreatedAttemptGeneration: 1, CreatedHolderID: "previous-pool-holder",
		CreatedRunVersion: 3, CreatedAttemptVersion: 1, ThreadID: threadID,
		ContractVersion: proposal.ContractVersion, CanonicalizerVersion: proposal.CanonicalizerVersion,
		CanonicalCatalog: append([]byte(nil), proposal.CanonicalCatalog...), CatalogDigest: proposal.CatalogDigest,
		PolicyVersion: proposal.PolicyVersion, PolicyContextDigest: proposal.PolicyContextDigest,
		Version: 2, CreatedAt: now, UpdatedAt: now,
	}
}

func launchProfileFromInputs(inputs RunLaunchInputs) RunLaunchProfile {
	return RunLaunchProfile{
		CodexRuntimeManifestDigest: inputs.CodexRuntimeManifestDigest, Model: inputs.Model,
		ExecutorMCPEndpoint: inputs.ExecutorMCPEndpoint, ExecutorMCPTLSIdentity: inputs.ExecutorMCPTLSIdentity,
		ExecutorMCPAudience: inputs.ExecutorMCPAudience, Limits: inputs.Limits,
		CheckpointAllowlistVersion: inputs.CheckpointAllowlistVersion,
		WorkerImageDigest:          inputs.WorkerImageDigest, ExpectedServiceAccount: inputs.ExpectedServiceAccount,
		ControllerCallbackEndpoint: inputs.ControllerCallbackEndpoint,
		ControllerCallbackIdentity: inputs.ControllerCallbackIdentity,
		ControllerCallbackAudience: inputs.ControllerCallbackAudience,
	}
}

type recordingRunLaunchStateSource struct {
	state     RunLaunchState
	err       error
	calls     int
	scheduled ScheduledRunAttempt
}

func (source *recordingRunLaunchStateSource) ResolveRunLaunchState(_ context.Context, scheduled ScheduledRunAttempt) (RunLaunchState, error) {
	source.calls++
	source.scheduled = scheduled
	return source.state, source.err
}
