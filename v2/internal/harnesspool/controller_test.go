package harnesspool

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestControllerClaimsOneQueuedRunWithCurrentVersion(t *testing.T) {
	core := &recordingControllerCore{dispatches: []RunDispatch{testControllerDispatch("queued")}, claimResult: testControllerClaim()}
	identities := &fixedClaimAllocator{identity: testControllerIdentity()}
	controller := newTestController(t, core, identities)
	scheduled, err := controller.ClaimNextRunAttempt(t.Context())
	if err != nil || scheduled == nil {
		t.Fatalf("ClaimNextRunAttempt() = %+v, %v", scheduled, err)
	}
	if identities.calls != 1 || len(core.claimRequests) != 1 || core.claimRequests[0].ExpectedRunVersion != 2 ||
		core.claimRequests[0].RunAttemptID != identities.identity.RunAttemptID || scheduled.Dispatch.RunDispatchID != testRunDispatchID {
		t.Fatalf("allocator/core/scheduled = %+v / %+v / %+v", identities, core.claimRequests, scheduled)
	}
	if len(core.completeRequests) != 0 || len(core.releaseRequests) != 0 {
		t.Fatalf("successful claim completed/released dispatch: complete=%+v release=%+v", core.completeRequests, core.releaseRequests)
	}
}

func TestControllerRetriesAmbiguousClaimWithExactIdentity(t *testing.T) {
	core := &recordingControllerCore{
		dispatches: testDispatchSlice("starting"), claimResult: testControllerClaim(),
		claimErrors: []error{errors.New("response lost")},
	}
	identities := &fixedClaimAllocator{identity: testControllerIdentity()}
	controller := newTestController(t, core, identities)
	scheduled, err := controller.ClaimNextRunAttempt(t.Context())
	if err != nil || scheduled == nil || len(core.claimRequests) != 2 {
		t.Fatalf("ClaimNextRunAttempt() = %+v, %v; requests = %+v", scheduled, err, core.claimRequests)
	}
	if !reflect.DeepEqual(core.claimRequests[0], core.claimRequests[1]) || identities.calls != 1 {
		t.Fatalf("ambiguous retry changed identity: requests=%+v allocator calls=%d", core.claimRequests, identities.calls)
	}
}

func TestControllerReleasesExpectedClaimContention(t *testing.T) {
	for _, code := range []string{"lease_held", "version_conflict"} {
		t.Run(code, func(t *testing.T) {
			core := &recordingControllerCore{
				dispatches:  testDispatchSlice("starting"),
				claimErrors: []error{&CoreCommandError{HTTPStatus: 409, Code: code, Message: "contended"}},
			}
			controller := newTestController(t, core, &fixedClaimAllocator{identity: testControllerIdentity()})
			scheduled, err := controller.ClaimNextRunAttempt(t.Context())
			if err != nil || scheduled != nil || len(core.releaseRequests) != 1 {
				t.Fatalf("ClaimNextRunAttempt() = %+v, %v; releases = %+v", scheduled, err, core.releaseRequests)
			}
			if core.releaseRequests[0].RetryAfter != 3*time.Second || core.releaseRequests[0].ClaimGeneration != 4 {
				t.Fatalf("release request = %+v", core.releaseRequests[0])
			}
		})
	}
}

func TestControllerCompletesOnlyNonDispatchableProjection(t *testing.T) {
	for _, status := range []string{"running", "finalizing", "completed", "failed", "interrupted", "cancelling", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			core := &recordingControllerCore{dispatches: testDispatchSlice(status), completeResult: true}
			identities := &fixedClaimAllocator{identity: testControllerIdentity()}
			controller := newTestController(t, core, identities)
			scheduled, err := controller.ClaimNextRunAttempt(t.Context())
			if err != nil || scheduled != nil || identities.calls != 0 || len(core.completeRequests) != 1 || len(core.claimRequests) != 0 {
				t.Fatalf("ClaimNextRunAttempt() = %+v, %v; allocator/core = %+v/%+v", scheduled, err, identities, core)
			}
		})
	}
}

func TestControllerLetsCoreGuardManualCompletionAndRelease(t *testing.T) {
	core := &recordingControllerCore{completeResult: true, releaseResult: true}
	controller := newTestController(t, core, &fixedClaimAllocator{identity: testControllerIdentity()})
	dispatch := testControllerDispatch("running")
	if err := controller.CompleteAcceptedDispatch(t.Context(), dispatch); err != nil {
		t.Fatal(err)
	}
	if err := controller.ReleaseUnstartedDispatch(t.Context(), dispatch); err != nil {
		t.Fatal(err)
	}
	if len(core.completeRequests) != 1 || len(core.releaseRequests) != 1 || core.completeRequests[0].OwnerID != "pool-instance" || core.releaseRequests[0].RetryAfter != 3*time.Second {
		t.Fatalf("complete/release requests = %+v / %+v", core.completeRequests, core.releaseRequests)
	}
}

func newTestController(t *testing.T, core ControllerCore, identities RunAttemptClaimIdentityAllocator) *Controller {
	t.Helper()
	controller, err := NewController(core, identities, ControllerConfig{
		HolderID: "pool-instance", DispatchLockTTL: 30 * time.Second, AttemptLeaseTTL: 20 * time.Second,
		LongPollTimeout: time.Second, ContentionBackoff: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return controller
}

func testControllerDispatch(status string) RunDispatch {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	return RunDispatch{
		RunDispatchID: testRunDispatchID, WorkspaceID: "40000000-0000-4000-8000-000000000004",
		SessionID: testSessionID, RunID: testRunID, EnqueuedRunVersion: 1, CurrentRunVersion: 2,
		CurrentRunStatus: status, ClaimOwnerID: "pool-instance", ClaimGeneration: 4,
		AvailableAt: now, LockExpiresAt: now.Add(time.Minute), CreatedAt: now,
	}
}

func testDispatchSlice(status string) []RunDispatch {
	return []RunDispatch{testControllerDispatch(status)}
}

func testControllerIdentity() RunAttemptClaimIdentity {
	return RunAttemptClaimIdentity{
		RunAttemptID: testRunAttemptID,
		Record: TransitionRecord{
			EventID: "71000000-0000-4000-8000-000000000001", ProducerInstanceID: "72000000-0000-4000-8000-000000000001",
			ProducerSeq: 1, OutboxID: "73000000-0000-4000-8000-000000000001",
		},
	}
}

func testControllerClaim() ClaimRunAttemptResult {
	return ClaimRunAttemptResult{
		Run:        Run{RunID: testRunID, SessionID: testSessionID, Status: "starting", CurrentAttemptGeneration: 1, Version: 3},
		RunAttempt: RunAttempt{RunAttemptID: testRunAttemptID, RunID: testRunID, Generation: 1, HolderID: "pool-instance", Version: 1},
		Created:    true,
	}
}

type fixedClaimAllocator struct {
	identity RunAttemptClaimIdentity
	err      error
	calls    int
}

func (allocator *fixedClaimAllocator) AllocateRunAttemptClaim() (RunAttemptClaimIdentity, error) {
	allocator.calls++
	return allocator.identity, allocator.err
}

type recordingControllerCore struct {
	dispatches       []RunDispatch
	dispatchError    error
	claimResult      ClaimRunAttemptResult
	claimErrors      []error
	completeResult   bool
	completeError    error
	releaseResult    bool
	releaseError     error
	claimRequests    []ClaimRunAttemptRequest
	completeRequests []CompleteRunDispatchRequest
	releaseRequests  []ReleaseRunDispatchRequest
}

func (core *recordingControllerCore) ClaimRunDispatches(_ context.Context, _ ClaimRunDispatchesRequest) ([]RunDispatch, error) {
	return append([]RunDispatch(nil), core.dispatches...), core.dispatchError
}

func (core *recordingControllerCore) ClaimRunAttempt(_ context.Context, request ClaimRunAttemptRequest) (ClaimRunAttemptResult, error) {
	core.claimRequests = append(core.claimRequests, request)
	if len(core.claimErrors) > 0 {
		err := core.claimErrors[0]
		core.claimErrors = core.claimErrors[1:]
		return ClaimRunAttemptResult{}, err
	}
	return core.claimResult, nil
}

func (core *recordingControllerCore) CompleteRunDispatch(_ context.Context, request CompleteRunDispatchRequest) (bool, error) {
	core.completeRequests = append(core.completeRequests, request)
	return core.completeResult, core.completeError
}

func (core *recordingControllerCore) ReleaseRunDispatch(_ context.Context, request ReleaseRunDispatchRequest) (bool, error) {
	core.releaseRequests = append(core.releaseRequests, request)
	return core.releaseResult, core.releaseError
}
