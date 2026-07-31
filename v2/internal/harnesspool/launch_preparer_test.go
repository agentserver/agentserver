package harnesspool

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/executorgateway/mcpcontract"
	"github.com/agentserver/agentserver/v2/internal/runmanifest"
)

func TestLaunchPreparerSignsBeforeFreezingCatalog(t *testing.T) {
	core := &recordingLaunchCore{}
	allocator := &fixedCatalogAllocator{id: "45000000-0000-4000-8000-000000000004"}
	resolver := &fixedLaunchResolver{inputs: testRunLaunchInputs()}
	seed := sha256.Sum256([]byte("launch-preparer-key"))
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	signer, err := NewEd25519ManifestSigner("cluster-key-1", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	preparer, err := NewLaunchPreparer(core, allocator, resolver, signer)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := preparer.Prepare(t.Context(), ScheduledRunAttempt{Dispatch: testControllerDispatch("starting"), Claim: testControllerClaim()})
	if err != nil {
		t.Fatal(err)
	}
	if allocator.calls != 1 || resolver.calls != 1 || len(core.requests) != 1 ||
		prepared.FrozenCatalog.CatalogID != allocator.id || prepared.Manifest.ExecutorMCP.CatalogID != allocator.id {
		t.Fatalf("prepared/allocator/resolver/core = %+v / %+v / %+v / %+v", prepared, allocator, resolver, core)
	}
	request := core.requests[0]
	if request.ExpectedRunVersion != prepared.Scheduled.Claim.Run.Version ||
		request.ExpectedRunAttemptVersion != prepared.Scheduled.Claim.RunAttempt.Version ||
		request.CatalogDigest != prepared.FrozenCatalog.CatalogDigest || request.PolicyVersion != "executor-policy/1" {
		t.Fatalf("freeze request = %+v", request)
	}
	verified, err := prepared.SignedManifest.Verify("cluster-key-1", privateKey.Public().(ed25519.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	if verified.RunID != testRunID || verified.RunAttemptID != testRunAttemptID ||
		verified.ControllerCallback.HolderID != "pool-instance" || verified.ExecutorMCP.ProtocolProfile != runmanifest.MCPProtocolProfile {
		t.Fatalf("verified manifest = %+v", verified)
	}
}

func TestLaunchPreparerReusesCheckpointCatalogWithoutAllocatingOrFreezing(t *testing.T) {
	inputs := testRunLaunchInputs()
	proposal, err := BuildExecutorCatalog(inputs.ExecutorCatalogPolicy)
	if err != nil {
		t.Fatal(err)
	}
	catalog := resolverCheckpointCatalog(proposal, "thread-previous")
	inputs.PreviousCheckpoint = &runmanifest.PreviousCheckpoint{
		CheckpointID: "47000000-0000-4000-8000-000000000004", ThreadID: catalog.ThreadID,
		ManifestDigest: strings.Repeat("d", 64), CatalogDigest: proposal.Catalog.Digest(),
		Object: runmanifest.ObjectPointer{
			ObjectID: "48000000-0000-4000-8000-000000000004", SHA256: strings.Repeat("e", 64),
			SizeBytes: 1024, MediaType: "application/octet-stream",
		},
	}
	inputs.PreviousBrainToolCatalog = &catalog
	core := &recordingLaunchCore{}
	allocator := &fixedCatalogAllocator{id: "45000000-0000-4000-8000-000000000099"}
	preparer := newTestLaunchPreparer(t, core, allocator, &fixedLaunchResolver{inputs: inputs})
	prepared, err := preparer.Prepare(t.Context(), ScheduledRunAttempt{Dispatch: testControllerDispatch("starting"), Claim: testControllerClaim()})
	if err != nil {
		t.Fatal(err)
	}
	if allocator.calls != 0 || len(core.requests) != 0 || prepared.FrozenCatalog.CatalogID != catalog.CatalogID ||
		prepared.Manifest.ExecutorMCP.CatalogID != catalog.CatalogID || prepared.Manifest.PreviousCheckpoint == nil ||
		prepared.Manifest.PreviousCheckpoint.ThreadID != catalog.ThreadID {
		t.Fatalf("resume prepared/allocator/core = %+v / %+v / %+v", prepared, allocator, core)
	}
	inputs.PreviousBrainToolCatalog.CanonicalCatalog[0] = '!'
	if prepared.FrozenCatalog.CanonicalCatalog[0] == '!' {
		t.Fatal("prepared resume catalog aliases resolver input")
	}
}

func TestLaunchPreparerRetriesAmbiguousFreezeWithExactIdentity(t *testing.T) {
	core := &recordingLaunchCore{errors: []error{errors.New("response lost")}}
	allocator := &fixedCatalogAllocator{id: "45000000-0000-4000-8000-000000000004"}
	preparer := newTestLaunchPreparer(t, core, allocator, &fixedLaunchResolver{inputs: testRunLaunchInputs()})
	prepared, err := preparer.Prepare(t.Context(), ScheduledRunAttempt{Dispatch: testControllerDispatch("queued"), Claim: testControllerClaim()})
	if err != nil || prepared.FrozenCatalog.CatalogID == "" || len(core.requests) != 2 {
		t.Fatalf("Prepare() = %+v, %v; requests = %+v", prepared, err, core.requests)
	}
	if allocator.calls != 1 || !reflect.DeepEqual(core.requests[0], core.requests[1]) {
		t.Fatalf("ambiguous freeze changed identity: calls=%d requests=%+v", allocator.calls, core.requests)
	}
}

func TestLaunchPreparerFailsClosedBeforeAndAfterFreezeBoundary(t *testing.T) {
	scheduled := ScheduledRunAttempt{Dispatch: testControllerDispatch("starting"), Claim: testControllerClaim()}
	core := &recordingLaunchCore{}
	resolver := &fixedLaunchResolver{err: errors.New("prompt unavailable")}
	preparer := newTestLaunchPreparer(t, core, &fixedCatalogAllocator{id: "45000000-0000-4000-8000-000000000004"}, resolver)
	if _, err := preparer.Prepare(t.Context(), scheduled); err == nil || len(core.requests) != 0 {
		t.Fatalf("resolver failure error/requests = %v/%d", err, len(core.requests))
	}

	core = &recordingLaunchCore{errors: []error{&CoreCommandError{HTTPStatus: 409, Code: "version_conflict"}}}
	preparer = newTestLaunchPreparer(t, core, &fixedCatalogAllocator{id: "45000000-0000-4000-8000-000000000004"}, &fixedLaunchResolver{inputs: testRunLaunchInputs()})
	if _, err := preparer.Prepare(t.Context(), scheduled); err == nil || len(core.requests) != 1 {
		t.Fatalf("command failure error/requests = %v/%d", err, len(core.requests))
	}

	core = &recordingLaunchCore{boundThreadID: "thread-already-started"}
	preparer = newTestLaunchPreparer(t, core, &fixedCatalogAllocator{id: "45000000-0000-4000-8000-000000000004"}, &fixedLaunchResolver{inputs: testRunLaunchInputs()})
	if _, err := preparer.Prepare(t.Context(), scheduled); err == nil || !strings.Contains(err.Error(), "unbound catalog") {
		t.Fatalf("bound catalog response error = %v", err)
	}
}

func newTestLaunchPreparer(t *testing.T, core LaunchPreparationCore, allocator BrainToolCatalogIDAllocator, resolver RunLaunchInputResolver) *LaunchPreparer {
	t.Helper()
	seed := sha256.Sum256([]byte("launch-preparer-key"))
	signer, err := NewEd25519ManifestSigner("cluster-key-1", ed25519.NewKeyFromSeed(seed[:]))
	if err != nil {
		t.Fatal(err)
	}
	preparer, err := NewLaunchPreparer(core, allocator, resolver, signer)
	if err != nil {
		t.Fatal(err)
	}
	return preparer
}

func testRunLaunchInputs() RunLaunchInputs {
	return RunLaunchInputs{
		Prompt: runmanifest.ObjectPointer{
			ObjectID: "46000000-0000-4000-8000-000000000004", SHA256: strings.Repeat("a", 64),
			SizeBytes: 128, MediaType: "application/json",
		},
		CodexRuntimeManifestDigest: strings.Repeat("b", 64),
		Model: runmanifest.ModelRoute{
			Model: "gpt-5", Provider: "llmproxy", Endpoint: "https://llmproxy.agentserver.svc/v1",
			TLSIdentity: "spiffe://agentserver.local/ns/agentserver/sa/llmproxy", Audience: "llmproxy",
		},
		ExecutorCatalogPolicy: ExecutorCatalogPolicy{
			Version: "executor-policy/1", ContextDigest: sha256.Sum256([]byte("policy")),
			AllowedTools: []string{mcpcontract.ToolListEnvironments, mcpcontract.ToolReadFile},
		},
		ExecutorMCPEndpoint:    "https://executor-gateway.agentserver.svc/mcp",
		ExecutorMCPTLSIdentity: "spiffe://agentserver.local/ns/agentserver/sa/executor-gateway",
		ExecutorMCPAudience:    "executor-mcp",
		Limits: runmanifest.RunLimits{
			MaxRunDurationMS: 3_600_000, MaxApprovalTTLMS: 300_000,
			GatewayActiveExecutionTimeoutMS: 600_000, MCPTransportGraceMS: 5_000,
			WorkerCallbackGraceMS: 10_000, MaxEventBufferBytes: 8 * 1024 * 1024,
			MaxControlBufferBytes: 2 * 1024 * 1024,
		},
		CheckpointAllowlistVersion: 1, WorkerImageDigest: strings.Repeat("c", 64),
		ExpectedServiceAccount:     "harness-worker",
		ControllerCallbackEndpoint: "https://pool-instance.agentserver.svc/internal/v2/harness/control",
		ControllerCallbackIdentity: "spiffe://agentserver.local/ns/agentserver/sa/harness-pool",
		ControllerCallbackAudience: "harness-pool-control",
	}
}

type fixedCatalogAllocator struct {
	id    string
	err   error
	calls int
}

func (allocator *fixedCatalogAllocator) AllocateBrainToolCatalogID() (string, error) {
	allocator.calls++
	return allocator.id, allocator.err
}

type fixedLaunchResolver struct {
	inputs RunLaunchInputs
	err    error
	calls  int
}

func (resolver *fixedLaunchResolver) ResolveRunLaunch(context.Context, ScheduledRunAttempt) (RunLaunchInputs, error) {
	resolver.calls++
	return resolver.inputs, resolver.err
}

type recordingLaunchCore struct {
	requests      []FreezeBrainToolCatalogRequest
	errors        []error
	boundThreadID string
}

func (core *recordingLaunchCore) FreezeBrainToolCatalog(_ context.Context, request FreezeBrainToolCatalogRequest) (FreezeBrainToolCatalogResult, error) {
	core.requests = append(core.requests, request)
	if len(core.errors) > 0 {
		err := core.errors[0]
		core.errors = core.errors[1:]
		return FreezeBrainToolCatalogResult{}, err
	}
	now := time.Date(2026, 7, 31, 16, 0, 0, 0, time.UTC)
	return FreezeBrainToolCatalogResult{
		Catalog: BrainToolCatalog{
			CatalogID: request.CatalogID, WorkspaceID: request.WorkspaceID, SessionID: request.SessionID,
			CreatedRunID: request.RunID, CreatedRunAttemptID: request.RunAttemptID,
			CreatedAttemptGeneration: request.RunAttemptGeneration, CreatedHolderID: request.HolderID,
			CreatedRunVersion: request.ExpectedRunVersion, CreatedAttemptVersion: request.ExpectedRunAttemptVersion,
			ThreadID: core.boundThreadID, ContractVersion: request.ContractVersion,
			CanonicalizerVersion: request.CanonicalizerVersion,
			CanonicalCatalog:     append(json.RawMessage(nil), request.CanonicalCatalog...), CatalogDigest: request.CatalogDigest,
			PolicyVersion: request.PolicyVersion, PolicyContextDigest: request.PolicyContextDigest,
			Version: 1, CreatedAt: now, UpdatedAt: now,
		},
		Created: true,
	}, nil
}
