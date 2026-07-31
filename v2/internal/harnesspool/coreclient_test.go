package harnesspool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
