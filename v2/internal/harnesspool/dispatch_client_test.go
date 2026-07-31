package harnesspool

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coreserver"
)

const testRunDispatchID = "45000000-0000-4000-8000-000000000004"

func TestCoreClientRunDispatchRoundTrip(t *testing.T) {
	commands := &recordingDispatchCommands{now: time.Date(2026, 7, 31, 11, 0, 0, 0, time.UTC)}
	handler, err := coreserver.NewRunDispatchHandler(allowWorkload{}, commands)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewCoreClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}

	dispatches, err := client.ClaimRunDispatches(t.Context(), ClaimRunDispatchesRequest{
		OwnerID: "pool-holder", Limit: 2, LockTTL: 45 * time.Second, WaitTimeout: 5 * time.Second,
	})
	if err != nil || len(dispatches) != 1 {
		t.Fatalf("ClaimRunDispatches() = %+v, %v", dispatches, err)
	}
	if commands.claim.OwnerID != "pool-holder" || commands.claim.LockTTLMillis != 45_000 || commands.claim.WaitTimeoutMillis != 5_000 ||
		dispatches[0].RunDispatchID != testRunDispatchID || dispatches[0].CurrentRunVersion != 2 || dispatches[0].ClaimGeneration != 3 {
		t.Fatalf("claim wire/result = %+v / %+v", commands.claim, dispatches[0])
	}

	completed, err := client.CompleteRunDispatch(t.Context(), CompleteRunDispatchRequest{
		RunDispatchID: testRunDispatchID, RunID: testRunID, OwnerID: "pool-holder", ClaimGeneration: 3,
	})
	if err != nil || !completed || commands.completeID != testRunDispatchID || commands.complete.RunID != testRunID {
		t.Fatalf("CompleteRunDispatch() = %v, %v; wire = %s / %+v", completed, err, commands.completeID, commands.complete)
	}
	released, err := client.ReleaseRunDispatch(t.Context(), ReleaseRunDispatchRequest{
		RunDispatchID: testRunDispatchID, RunID: testRunID, OwnerID: "pool-holder", ClaimGeneration: 3, RetryAfter: 2 * time.Second,
	})
	if err != nil || !released || commands.releaseID != testRunDispatchID || commands.release.RetryAfterMillis != 2_000 {
		t.Fatalf("ReleaseRunDispatch() = %v, %v; wire = %s / %+v", released, err, commands.releaseID, commands.release)
	}
}

func TestCoreClientRejectsRunDispatchAuthorityMismatch(t *testing.T) {
	commands := &recordingDispatchCommands{now: time.Now(), badOwner: true}
	handler, err := coreserver.NewRunDispatchHandler(allowWorkload{}, commands)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewCoreClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ClaimRunDispatches(t.Context(), ClaimRunDispatchesRequest{
		OwnerID: "pool-holder", Limit: 1, LockTTL: time.Second,
	}); err == nil {
		t.Fatal("mismatched run-dispatch owner was accepted")
	}
}

type recordingDispatchCommands struct {
	now        time.Time
	badOwner   bool
	claim      corecontract.ClaimRunDispatchesRequest
	completeID string
	complete   corecontract.CompleteRunDispatchRequest
	releaseID  string
	release    corecontract.ReleaseRunDispatchRequest
}

func (commands *recordingDispatchCommands) ClaimRunDispatches(_ context.Context, request corecontract.ClaimRunDispatchesRequest) (corecontract.ClaimRunDispatchesResponse, error) {
	commands.claim = request
	owner := request.OwnerID
	if commands.badOwner {
		owner += "-other"
	}
	return corecontract.ClaimRunDispatchesResponse{RunDispatches: []corecontract.RunDispatch{{
		RunDispatchID: testRunDispatchID, WorkspaceID: "40000000-0000-4000-8000-000000000004",
		SessionID: testSessionID, RunID: testRunID, EnqueuedRunVersion: 1, CurrentRunVersion: 2,
		CurrentRunStatus: "starting", ClaimOwnerID: owner, ClaimGeneration: 3,
		AvailableAt: commands.now, LockExpiresAt: commands.now.Add(time.Duration(request.LockTTLMillis) * time.Millisecond), CreatedAt: commands.now,
	}}}, nil
}

func (commands *recordingDispatchCommands) CompleteRunDispatch(_ context.Context, dispatchID string, request corecontract.CompleteRunDispatchRequest) (corecontract.CompleteRunDispatchResponse, error) {
	commands.completeID = dispatchID
	commands.complete = request
	return corecontract.CompleteRunDispatchResponse{Completed: true}, nil
}

func (commands *recordingDispatchCommands) ReleaseRunDispatch(_ context.Context, dispatchID string, request corecontract.ReleaseRunDispatchRequest) (corecontract.ReleaseRunDispatchResponse, error) {
	commands.releaseID = dispatchID
	commands.release = request
	return corecontract.ReleaseRunDispatchResponse{Released: true}, nil
}
