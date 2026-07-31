package coreserver

import (
	"context"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
)

const testRunDispatchID = "45000000-0000-4000-8000-000000000004"

func TestStateStoreRunDispatchCommandsLongPollAndMapClaims(t *testing.T) {
	now := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	store := &recordingRunDispatchStore{now: now, emptyClaims: 1}
	commands := StateStoreRunDispatchCommands{Store: store, PollInterval: time.Millisecond}
	response, err := commands.ClaimRunDispatches(t.Context(), corecontract.ClaimRunDispatchesRequest{
		OwnerID: "pool-holder", Limit: 1, LockTTLMillis: 30_000, WaitTimeoutMillis: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if store.claimCalls != 2 || store.claim.Owner != "pool-holder" || store.claim.LockTTL != 30*time.Second || len(response.RunDispatches) != 1 {
		t.Fatalf("claim calls/store/response = %d / %+v / %+v", store.claimCalls, store.claim, response)
	}
	dispatch := response.RunDispatches[0]
	if dispatch.RunDispatchID != testRunDispatchID || dispatch.RunID != testRunID || dispatch.EnqueuedRunVersion != 1 ||
		dispatch.CurrentRunVersion != 2 || dispatch.CurrentRunStatus != coredb.RunStatusStarting || dispatch.ClaimGeneration != 3 {
		t.Fatalf("mapped run dispatch = %+v", dispatch)
	}

	completed, err := commands.CompleteRunDispatch(t.Context(), testRunDispatchID, corecontract.CompleteRunDispatchRequest{
		RunID: testRunID, OwnerID: "pool-holder", ClaimGeneration: 3,
	})
	if err != nil || !completed.Completed || store.complete.ID != testRunDispatchID {
		t.Fatalf("CompleteRunDispatch() = %+v, %v; store = %+v", completed, err, store.complete)
	}
	released, err := commands.ReleaseRunDispatch(t.Context(), testRunDispatchID, corecontract.ReleaseRunDispatchRequest{
		RunID: testRunID, OwnerID: "pool-holder", ClaimGeneration: 3, RetryAfterMillis: 2_000,
	})
	if err != nil || !released.Released || store.release.RetryAfter != 2*time.Second {
		t.Fatalf("ReleaseRunDispatch() = %+v, %v; store = %+v", released, err, store.release)
	}
}

func TestStateStoreRunDispatchCommandsReturnBoundedEmptyAndRejectInvalidDuration(t *testing.T) {
	store := &recordingRunDispatchStore{alwaysEmpty: true}
	commands := StateStoreRunDispatchCommands{Store: store, PollInterval: time.Millisecond}
	response, err := commands.ClaimRunDispatches(t.Context(), corecontract.ClaimRunDispatchesRequest{
		OwnerID: "pool-holder", Limit: 1, LockTTLMillis: 1_000, WaitTimeoutMillis: 3,
	})
	if err != nil || response.RunDispatches == nil || len(response.RunDispatches) != 0 || store.claimCalls < 1 {
		t.Fatalf("empty long poll = %+v, %v; calls = %d", response, err, store.claimCalls)
	}
	before := store.claimCalls
	if _, err := commands.ClaimRunDispatches(t.Context(), corecontract.ClaimRunDispatchesRequest{
		OwnerID: "pool-holder", Limit: 1, LockTTLMillis: 1_000,
		WaitTimeoutMillis: corecontract.MaxRunDispatchClaimWait.Milliseconds() + 1,
	}); err == nil || store.claimCalls != before {
		t.Fatalf("oversized wait error/calls = %v/%d", err, store.claimCalls)
	}
	if _, err := commands.ReleaseRunDispatch(t.Context(), testRunDispatchID, corecontract.ReleaseRunDispatchRequest{
		RetryAfterMillis: coredb.MaxOutboxRetryDelay.Milliseconds() + 1,
	}); err == nil || store.releaseCalls != 0 {
		t.Fatalf("oversized retry error/calls = %v/%d", err, store.releaseCalls)
	}
}

type recordingRunDispatchStore struct {
	now          time.Time
	emptyClaims  int
	alwaysEmpty  bool
	claim        coredb.ClaimRunDispatchesCommand
	complete     coredb.CompleteRunDispatchCommand
	release      coredb.ReleaseRunDispatchCommand
	claimCalls   int
	releaseCalls int
}

func (store *recordingRunDispatchStore) ClaimRunDispatches(_ context.Context, command coredb.ClaimRunDispatchesCommand) ([]coredb.RunDispatch, error) {
	store.claim = command
	store.claimCalls++
	if store.alwaysEmpty || store.claimCalls <= store.emptyClaims {
		return []coredb.RunDispatch{}, nil
	}
	return []coredb.RunDispatch{{
		ID: testRunDispatchID, WorkspaceID: "40000000-0000-4000-8000-000000000004",
		SessionID: "40000000-0000-4000-8000-000000000005", RunID: testRunID,
		EnqueuedRunVersion: 1, CurrentRunVersion: 2, CurrentRunStatus: coredb.RunStatusStarting,
		ClaimOwner: command.Owner, ClaimGeneration: 3, AvailableAt: store.now,
		LockUntil: store.now.Add(command.LockTTL), CreatedAt: store.now,
	}}, nil
}

func (store *recordingRunDispatchStore) CompleteRunDispatch(_ context.Context, command coredb.CompleteRunDispatchCommand) (bool, error) {
	store.complete = command
	return true, nil
}

func (store *recordingRunDispatchStore) ReleaseRunDispatch(_ context.Context, command coredb.ReleaseRunDispatchCommand) (bool, error) {
	store.release = command
	store.releaseCalls++
	return true, nil
}
