package harnesspool

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/braincatalog"
	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
	"github.com/agentserver/agentserver/v2/internal/coreserver"
)

const (
	testRunID        = "41000000-0000-4000-8000-000000000004"
	testRunAttemptID = "42000000-0000-4000-8000-000000000004"
	testSessionID    = "43000000-0000-4000-8000-000000000004"
)

func TestCoreClientRunAttemptRoundTrip(t *testing.T) {
	commands := &recordingContractCommands{now: time.Date(2026, 7, 31, 9, 0, 0, 0, time.UTC)}
	handler, err := coreserver.NewRunAttemptHandler(allowWorkload{}, commands)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewCoreClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}

	claim := ClaimRunAttemptRequest{
		RunID: testRunID, RunAttemptID: testRunAttemptID, HolderID: "pool-holder", ExpectedRunVersion: 1,
		LeaseTTL: 30 * time.Second, Record: testTransitionRecord(1),
	}
	claimed, err := client.ClaimRunAttempt(t.Context(), claim)
	if err != nil || !claimed.Created || claimed.RunAttempt.Generation != 3 || claimed.Run.WorkspaceID == "" {
		t.Fatalf("ClaimRunAttempt() = %+v, %v", claimed, err)
	}
	if commands.claim.LeaseTTLMillis != 30_000 || commands.claim.Record.EventID != claim.Record.EventID {
		t.Fatalf("claim wire request = %+v", commands.claim)
	}

	renewed, err := client.RenewRunAttempt(t.Context(), RenewRunAttemptRequest{
		SessionID: testSessionID, RunID: testRunID, RunAttemptID: testRunAttemptID,
		HolderID: "pool-holder", RunAttemptGeneration: 3, LeaseTTL: 45 * time.Second,
	})
	if err != nil || renewed.SessionLease.Generation != 3 || renewed.AttemptLease.HolderID != "pool-holder" {
		t.Fatalf("RenewRunAttempt() = %+v, %v", renewed, err)
	}
	if commands.renew.LeaseTTLMillis != 45_000 || commands.renew.SessionID != testSessionID {
		t.Fatalf("renew wire request = %+v", commands.renew)
	}

	accepted, err := client.MarkTurnAccepted(t.Context(), MarkTurnAcceptedRequest{
		RunID: testRunID, RunAttemptID: testRunAttemptID, HolderID: "pool-holder", RunAttemptGeneration: 3,
		ExpectedRunVersion: 2, ExpectedRunAttemptVersion: 1, Record: testTransitionRecord(2),
	})
	if err != nil || !accepted.Changed || accepted.RunAttempt.TurnStartedAt == nil {
		t.Fatalf("MarkTurnAccepted() = %+v, %v", accepted, err)
	}

	objectDigest := sha256.Sum256([]byte("object"))
	appendRequest := AppendAttemptEventsRequest{
		RunID: testRunID, RunAttemptID: testRunAttemptID, HolderID: "pool-holder", RunAttemptGeneration: 3,
		OutboxID: "73000000-0000-4000-8000-000000000003",
		Events: []AttemptEvent{
			{
				EventID: "71000000-0000-4000-8000-000000000003", ProducerInstanceID: "72000000-0000-4000-8000-000000000001",
				ProducerSeq: 3, Source: "brain", Kind: "model.delta", SchemaVersion: 1, Payload: json.RawMessage(`{"text":"ok"}`),
			},
			{
				EventID: "71000000-0000-4000-8000-000000000004", ProducerInstanceID: "72000000-0000-4000-8000-000000000001",
				ProducerSeq: 4, Source: "executor", Kind: "execution.output", SchemaVersion: 1,
				Object: &EventObjectPointer{ObjectID: "74000000-0000-4000-8000-000000000001", SHA256: objectDigest, Size: 64, MediaType: "application/octet-stream"},
			},
		},
	}
	appended, err := client.AppendAttemptEvents(t.Context(), appendRequest)
	if err != nil || appended.NewCount != 1 || len(appended.Events) != 2 || !appended.Events[1].Duplicate {
		t.Fatalf("AppendAttemptEvents() = %+v, %v", appended, err)
	}
	if commands.append.Events[1].Object == nil || commands.append.Events[1].Object.SHA256 != hex.EncodeToString(objectDigest[:]) {
		t.Fatalf("append wire object = %+v", commands.append.Events[1].Object)
	}
}

func TestCoreClientResolvesFencedRunLaunchState(t *testing.T) {
	base := testRunLaunchInputs()
	proposal, err := BuildExecutorCatalog(base.ExecutorCatalogPolicy)
	if err != nil {
		t.Fatal(err)
	}
	commands := &recordingRunLaunchStateContractQueries{response: corecontract.ResolveRunLaunchStateResponse{
		WorkspaceID: "40000000-0000-4000-8000-000000000004", SessionID: testSessionID,
		RunID: testRunID, RunAttemptID: testRunAttemptID, HolderID: "pool-instance",
		RunAttemptGeneration: 1, RunVersion: 3, RunAttemptVersion: 1,
		Prompt: corecontract.RunLaunchObjectPointer{
			ObjectID: base.Prompt.ObjectID, SHA256: base.Prompt.SHA256,
			SizeBytes: base.Prompt.SizeBytes, MediaType: base.Prompt.MediaType,
		},
		PreviousCheckpoint: &corecontract.RunLaunchCheckpointState{
			CheckpointID: "47000000-0000-4000-8000-000000000004",
			RunID:        "4c000000-0000-4000-8000-000000000004", RunAttemptID: "4d000000-0000-4000-8000-000000000004",
			RunAttemptGeneration: 2, ThreadID: "thread-previous", TurnID: "turn-previous",
			ManifestDigest: strings.Repeat("d", 64), CatalogDigest: proposal.Catalog.Digest(),
			Catalog: contractRunLaunchCheckpointCatalog(proposal),
			Object: corecontract.RunLaunchObjectPointer{
				ObjectID: "48000000-0000-4000-8000-000000000004", SHA256: strings.Repeat("e", 64),
				SizeBytes: 1024, MediaType: "application/vnd.agentserver.codex-checkpoint.v1",
			},
			CodexRuntimeManifestDigest: base.CodexRuntimeManifestDigest,
			CheckpointAllowlistVersion: int64(base.CheckpointAllowlistVersion),
		},
		ExecutorPolicy: corecontract.RunLaunchExecutorPolicyState{
			Version:       base.ExecutorCatalogPolicy.Version,
			ContextDigest: hex.EncodeToString(base.ExecutorCatalogPolicy.ContextDigest[:]),
			AllowedTools:  append([]string(nil), base.ExecutorCatalogPolicy.AllowedTools...),
		},
	}}
	handler, err := coreserver.NewRunLaunchStateHandler(allowWorkload{}, commands)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewCoreClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	scheduled := ScheduledRunAttempt{Dispatch: testControllerDispatch("starting"), Claim: testControllerClaim()}
	state, err := client.ResolveRunLaunchState(t.Context(), scheduled)
	if err != nil {
		t.Fatal(err)
	}
	if commands.request.RunID != scheduled.Claim.Run.RunID ||
		commands.request.ExpectedRunVersion != scheduled.Claim.Run.Version ||
		commands.request.ExpectedRunAttemptVersion != scheduled.Claim.RunAttempt.Version ||
		state.Prompt != base.Prompt || state.PreviousCheckpoint == nil ||
		state.PreviousCheckpoint.Checkpoint.CatalogDigest != proposal.Catalog.Digest() ||
		state.PreviousCheckpoint.Checkpoint.CodexRuntimeManifestDigest != base.CodexRuntimeManifestDigest ||
		len(state.ExecutorPolicy.AllowedTools) != len(base.ExecutorCatalogPolicy.AllowedTools) {
		t.Fatalf("wire request/state = %+v / %+v", commands.request, state)
	}
	state.ExecutorPolicy.AllowedTools[0] = "mutated"
	if commands.response.ExecutorPolicy.AllowedTools[0] != base.ExecutorCatalogPolicy.AllowedTools[0] {
		t.Fatal("core launch state aliases transport response")
	}
	commands.response.RunVersion++
	if _, err := client.ResolveRunLaunchState(t.Context(), scheduled); err == nil || !strings.Contains(err.Error(), "authority tuple") {
		t.Fatalf("mismatched launch authority response error = %v", err)
	}
	commands.response.RunVersion--
	commands.response.PreviousCheckpoint.RunID = ""
	if _, err := client.ResolveRunLaunchState(t.Context(), scheduled); err == nil || !strings.Contains(err.Error(), "checkpoint run ID") {
		t.Fatalf("missing checkpoint source authority error = %v", err)
	}
}

func contractRunLaunchCheckpointCatalog(proposal ExecutorCatalogProposal) corecontract.BrainToolCatalogState {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	return corecontract.BrainToolCatalogState{
		CatalogID:   "49000000-0000-4000-8000-000000000004",
		WorkspaceID: "40000000-0000-4000-8000-000000000004", SessionID: testSessionID,
		CreatedRunID: testRunID, CreatedRunAttemptID: testRunAttemptID,
		CreatedAttemptGeneration: 1, CreatedHolderID: "previous-pool-holder",
		CreatedRunVersion: 3, CreatedAttemptVersion: 1, ThreadID: "thread-previous",
		ContractVersion: proposal.ContractVersion, CanonicalizerVersion: proposal.CanonicalizerVersion,
		CanonicalCatalog: append(json.RawMessage(nil), proposal.CanonicalCatalog...),
		CatalogDigest:    hex.EncodeToString(proposal.CatalogDigest[:]), PolicyVersion: proposal.PolicyVersion,
		PolicyContextDigest: hex.EncodeToString(proposal.PolicyContextDigest[:]),
		Version:             2, CreatedAt: now, UpdatedAt: now,
	}
}

func TestCoreClientBrainToolCatalogRoundTrip(t *testing.T) {
	commands := &recordingBrainContractCommands{now: time.Date(2026, 7, 31, 15, 0, 0, 0, time.UTC)}
	handler, err := coreserver.NewBrainToolCatalogHandler(allowWorkload{}, commands)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewCoreClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	policyDigest := sha256.Sum256([]byte("policy"))
	proposal, err := BuildExecutorCatalog(ExecutorCatalogPolicy{
		Version: "executor-policy/1", ContextDigest: policyDigest, AllowedTools: []string{"read_file"},
	})
	if err != nil {
		t.Fatal(err)
	}
	transportCatalog, err := braincatalog.BuildCatalog(
		"executor",
		"Canonical <catalog> & \u2028 transport.",
		[]braincatalog.ToolDescriptor{{
			Name: "read_file", Description: "Read <one> & preserve bytes.",
			InputSchema: json.RawMessage(`{"type":"object","description":"<path> & value"}`),
		}},
		braincatalog.DefaultLimits(),
	)
	if err != nil {
		t.Fatal(err)
	}
	proposal.CanonicalCatalog = transportCatalog.CanonicalBytes()
	proposal.CatalogDigest = transportCatalog.DigestSHA256()
	if !bytes.Contains(proposal.CanonicalCatalog, []byte("<catalog>")) {
		t.Fatalf("test catalog did not exercise JSON HTML escaping: %s", proposal.CanonicalCatalog)
	}
	catalogDigest := proposal.CatalogDigest
	freeze := FreezeBrainToolCatalogRequest{
		CatalogID:   "45000000-0000-4000-8000-000000000004",
		WorkspaceID: "40000000-0000-4000-8000-000000000004", SessionID: testSessionID,
		RunID: testRunID, RunAttemptID: testRunAttemptID, HolderID: "pool-holder",
		RunAttemptGeneration: 3, ExpectedRunVersion: 2, ExpectedRunAttemptVersion: 1,
		ContractVersion: proposal.ContractVersion, CanonicalizerVersion: proposal.CanonicalizerVersion,
		CanonicalCatalog: proposal.CanonicalCatalog, CatalogDigest: catalogDigest,
		PolicyVersion: "executor-policy/1", PolicyContextDigest: policyDigest,
	}
	frozen, err := client.FreezeBrainToolCatalog(t.Context(), freeze)
	if err != nil || !frozen.Created || frozen.Catalog.CatalogDigest != catalogDigest {
		t.Fatalf("FreezeBrainToolCatalog() = %+v, %v", frozen, err)
	}
	if commands.freeze.CatalogDigest != hex.EncodeToString(catalogDigest[:]) || string(commands.freeze.CanonicalCatalog) != string(freeze.CanonicalCatalog) {
		t.Fatalf("freeze wire request = %+v", commands.freeze)
	}

	bind := BindBrainThreadCatalogRequest{
		CatalogID: freeze.CatalogID, RunID: freeze.RunID, RunAttemptID: freeze.RunAttemptID,
		HolderID: freeze.HolderID, RunAttemptGeneration: freeze.RunAttemptGeneration,
		ExpectedRunVersion: freeze.ExpectedRunVersion, ExpectedRunAttemptVersion: freeze.ExpectedRunAttemptVersion,
		ExpectedCatalogVersion: 1, ThreadID: "thread-1",
	}
	bound, err := client.BindBrainThreadCatalog(t.Context(), bind)
	if err != nil || !bound.Changed || bound.Catalog.ThreadID != bind.ThreadID || commands.bind.ThreadID != bind.ThreadID {
		t.Fatalf("BindBrainThreadCatalog() = %+v, %v; wire = %+v", bound, err, commands.bind)
	}
}

func TestCoreClientPreservesStableConflict(t *testing.T) {
	commands := &recordingContractCommands{commandError: &coredb.StateError{
		Code: coredb.ErrorLeaseLost, Message: "attempt lease expired", CurrentGeneration: 4,
	}}
	handler, err := coreserver.NewRunAttemptHandler(allowWorkload{}, commands)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewCoreClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.RenewRunAttempt(t.Context(), RenewRunAttemptRequest{RunAttemptID: testRunAttemptID})
	var commandError *CoreCommandError
	if !errors.As(err, &commandError) || commandError.Code != "lease_lost" || commandError.CurrentGeneration != 4 || commandError.HTTPStatus != http.StatusConflict {
		t.Fatalf("renew conflict = %#v", err)
	}
}

func TestCoreClientRejectsMismatchedAuthorityResponseAndRemoteCleartext(t *testing.T) {
	commands := &recordingContractCommands{badClaimGeneration: true, now: time.Now()}
	handler, err := coreserver.NewRunAttemptHandler(allowWorkload{}, commands)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewCoreClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ClaimRunAttempt(t.Context(), ClaimRunAttemptRequest{RunID: testRunID, RunAttemptID: testRunAttemptID, HolderID: "pool-holder"})
	if err == nil {
		t.Fatal("mismatched claim generation was accepted")
	}
	if _, err := NewCoreClient("http://core.internal", http.DefaultClient); err == nil {
		t.Fatal("remote cleartext core origin was accepted")
	}
}

type allowWorkload struct{}

func (allowWorkload) AuthorizeWorkload(*http.Request, string) error { return nil }

type recordingContractCommands struct {
	now                time.Time
	claim              corecontract.ClaimRunAttemptRequest
	renew              corecontract.RenewRunAttemptRequest
	append             corecontract.AppendAttemptEventsRequest
	commandError       error
	badClaimGeneration bool
}

type recordingBrainContractCommands struct {
	now    time.Time
	freeze corecontract.FreezeBrainToolCatalogRequest
	bind   corecontract.BindBrainThreadCatalogRequest
}

type recordingRunLaunchStateContractQueries struct {
	request  corecontract.ResolveRunLaunchStateRequest
	response corecontract.ResolveRunLaunchStateResponse
}

func (queries *recordingRunLaunchStateContractQueries) ResolveRunLaunchState(_ context.Context, request corecontract.ResolveRunLaunchStateRequest) (corecontract.ResolveRunLaunchStateResponse, error) {
	queries.request = request
	return queries.response, nil
}

func (commands *recordingBrainContractCommands) FreezeBrainToolCatalog(_ context.Context, request corecontract.FreezeBrainToolCatalogRequest) (corecontract.FreezeBrainToolCatalogResponse, error) {
	commands.freeze = request
	return corecontract.FreezeBrainToolCatalogResponse{
		Catalog: contractTestBrainCatalog(request, commands.now, "", 1), Created: true,
	}, nil
}

func (commands *recordingBrainContractCommands) BindBrainThreadCatalog(_ context.Context, request corecontract.BindBrainThreadCatalogRequest) (corecontract.BindBrainThreadCatalogResponse, error) {
	commands.bind = request
	return corecontract.BindBrainThreadCatalogResponse{
		Catalog: contractTestBrainCatalog(commands.freeze, commands.now, request.ThreadID, 2), Changed: true,
	}, nil
}

func contractTestBrainCatalog(request corecontract.FreezeBrainToolCatalogRequest, now time.Time, threadID string, version int64) corecontract.BrainToolCatalogState {
	return corecontract.BrainToolCatalogState{
		CatalogID: request.CatalogID, WorkspaceID: request.WorkspaceID, SessionID: request.SessionID,
		CreatedRunID: request.RunID, CreatedRunAttemptID: request.RunAttemptID,
		CreatedAttemptGeneration: request.RunAttemptGeneration, CreatedHolderID: request.HolderID,
		CreatedRunVersion: request.ExpectedRunVersion, CreatedAttemptVersion: request.ExpectedRunAttemptVersion,
		ThreadID: threadID, ContractVersion: request.ContractVersion, CanonicalizerVersion: request.CanonicalizerVersion,
		CanonicalCatalog: append(json.RawMessage(nil), request.CanonicalCatalog...), CatalogDigest: request.CatalogDigest,
		PolicyVersion: request.PolicyVersion, PolicyContextDigest: request.PolicyContextDigest,
		Version: version, CreatedAt: now, UpdatedAt: now,
	}
}

func (commands *recordingContractCommands) ClaimRunAttempt(_ context.Context, request corecontract.ClaimRunAttemptRequest) (corecontract.ClaimRunAttemptResponse, error) {
	if commands.commandError != nil {
		return corecontract.ClaimRunAttemptResponse{}, commands.commandError
	}
	commands.claim = request
	generation := int64(3)
	leaseGeneration := generation
	if commands.badClaimGeneration {
		leaseGeneration++
	}
	return corecontract.ClaimRunAttemptResponse{
		Run: corecontract.RunState{
			RunID: request.RunID, WorkspaceID: "40000000-0000-4000-8000-000000000004", SessionID: testSessionID,
			ActorID: "44000000-0000-4000-8000-000000000004", Status: "starting", CurrentAttemptGeneration: generation,
			NextEventSeq: 3, Version: 2, CreatedAt: commands.now, UpdatedAt: commands.now,
		},
		RunAttempt: corecontract.RunAttemptState{
			RunAttemptID: request.RunAttemptID, RunID: request.RunID, Generation: generation, Status: "leased",
			HolderID: request.HolderID, Version: 1, CreatedAt: commands.now, UpdatedAt: commands.now,
		},
		SessionLease: testContractLease(request.HolderID, leaseGeneration, commands.now, time.Duration(request.LeaseTTLMillis)*time.Millisecond),
		AttemptLease: testContractLease(request.HolderID, generation, commands.now, time.Duration(request.LeaseTTLMillis)*time.Millisecond),
		Created:      true,
	}, nil
}

func (commands *recordingContractCommands) RenewRunAttempt(_ context.Context, request corecontract.RenewRunAttemptRequest) (corecontract.RenewRunAttemptResponse, error) {
	if commands.commandError != nil {
		return corecontract.RenewRunAttemptResponse{}, commands.commandError
	}
	commands.renew = request
	lease := testContractLease(request.HolderID, request.RunAttemptGeneration, commands.now, time.Duration(request.LeaseTTLMillis)*time.Millisecond)
	return corecontract.RenewRunAttemptResponse{SessionLease: lease, AttemptLease: lease}, nil
}

func (commands *recordingContractCommands) MarkTurnAccepted(_ context.Context, request corecontract.MarkTurnAcceptedRequest) (corecontract.MarkTurnAcceptedResponse, error) {
	if commands.commandError != nil {
		return corecontract.MarkTurnAcceptedResponse{}, commands.commandError
	}
	turnStarted := commands.now
	return corecontract.MarkTurnAcceptedResponse{
		Run: corecontract.RunState{
			RunID: request.RunID, WorkspaceID: "40000000-0000-4000-8000-000000000004", SessionID: testSessionID,
			ActorID: "44000000-0000-4000-8000-000000000004", Status: "running", CurrentAttemptGeneration: request.RunAttemptGeneration,
			NextEventSeq: 4, Version: request.ExpectedRunVersion + 1, CreatedAt: commands.now, UpdatedAt: commands.now,
		},
		RunAttempt: corecontract.RunAttemptState{
			RunAttemptID: request.RunAttemptID, RunID: request.RunID, Generation: request.RunAttemptGeneration, Status: "running",
			TurnStartedAt: &turnStarted, HolderID: request.HolderID, Version: request.ExpectedRunAttemptVersion + 1,
			CreatedAt: commands.now, UpdatedAt: commands.now,
		},
		Changed: true,
	}, nil
}

func (commands *recordingContractCommands) AppendAttemptEvents(_ context.Context, request corecontract.AppendAttemptEventsRequest) (corecontract.AppendAttemptEventsResponse, error) {
	if commands.commandError != nil {
		return corecontract.AppendAttemptEventsResponse{}, commands.commandError
	}
	commands.append = request
	return corecontract.AppendAttemptEventsResponse{
		Events: []corecontract.AppendedAttemptEvent{
			{EventID: request.Events[0].EventID, ProducerInstanceID: request.Events[0].ProducerInstanceID, ProducerSeq: request.Events[0].ProducerSeq, RunSeq: 10},
			{EventID: request.Events[1].EventID, ProducerInstanceID: request.Events[1].ProducerInstanceID, ProducerSeq: request.Events[1].ProducerSeq, RunSeq: 11, Duplicate: true},
		},
		NewCount: 1,
	}, nil
}

func testContractLease(holder string, generation int64, now time.Time, ttl time.Duration) corecontract.LeaseState {
	return corecontract.LeaseState{HolderID: holder, Generation: generation, ExpiresAt: now.Add(ttl), AcquiredAt: now, RenewedAt: now}
}

func testTransitionRecord(seed int64) TransitionRecord {
	return TransitionRecord{
		EventID: "71000000-0000-4000-8000-000000000001", ProducerInstanceID: "72000000-0000-4000-8000-000000000001",
		ProducerSeq: seed, OutboxID: "73000000-0000-4000-8000-000000000001",
	}
}
