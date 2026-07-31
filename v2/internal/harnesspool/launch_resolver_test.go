package harnesspool

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"

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
		CheckpointID: "47000000-0000-4000-8000-000000000004", ThreadID: "thread-previous",
		ManifestDigest: strings.Repeat("d", 64), CatalogDigest: proposal.Catalog.Digest(),
		Object: runmanifest.ObjectPointer{
			ObjectID: "48000000-0000-4000-8000-000000000004", SHA256: strings.Repeat("e", 64),
			SizeBytes: 1024, MediaType: "application/octet-stream",
		},
	}
	source := &recordingRunLaunchStateSource{state: RunLaunchState{
		Prompt: base.Prompt, PreviousCheckpoint: checkpoint, ExecutorPolicy: base.ExecutorCatalogPolicy,
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
		inputs.ExecutorMCPEndpoint != base.ExecutorMCPEndpoint ||
		len(inputs.ExecutorCatalogPolicy.AllowedTools) != len(base.ExecutorCatalogPolicy.AllowedTools) {
		t.Fatalf("resolved inputs/source = %+v / %+v", inputs, source)
	}
	inputs.PreviousCheckpoint.ThreadID = "mutated"
	inputs.ExecutorCatalogPolicy.AllowedTools[0] = mcpcontract.ToolShell
	if checkpoint.ThreadID != "thread-previous" || source.state.ExecutorPolicy.AllowedTools[0] != base.ExecutorCatalogPolicy.AllowedTools[0] {
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
		PreviousCheckpoint: &runmanifest.PreviousCheckpoint{
			CheckpointID: "47000000-0000-4000-8000-000000000004", ThreadID: "thread-previous",
			ManifestDigest: strings.Repeat("d", 64), CatalogDigest: proposal.Catalog.Digest(),
			Object: runmanifest.ObjectPointer{
				ObjectID: "48000000-0000-4000-8000-000000000004", SHA256: strings.Repeat("e", 64),
				SizeBytes: 1024, MediaType: "application/octet-stream",
			},
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
	if err == nil || !strings.Contains(err.Error(), "catalogDigest must match") {
		t.Fatalf("checkpoint/catalog drift error = %v", err)
	}

	source.err = errors.New("core launch state unavailable")
	if _, err := resolver.ResolveRunLaunch(t.Context(), ScheduledRunAttempt{Dispatch: testControllerDispatch("starting"), Claim: testControllerClaim()}); err == nil || !strings.Contains(err.Error(), "core launch state unavailable") {
		t.Fatalf("source error = %v", err)
	}
	if _, err := resolver.ResolveRunLaunch(nil, ScheduledRunAttempt{}); err == nil || !strings.Contains(err.Error(), "context") {
		t.Fatalf("nil context error = %v", err)
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
