package harnesspool

import (
	"context"
	"encoding/hex"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/harnesscontrol"
)

func TestAttemptApprovalAuthorityLongPollsExactCoreScope(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	request := harnesscontrol.ApprovalRequestEvent{
		Kind:  harnesscontrol.EventKindApprovalRequest,
		RunID: prepared.Manifest.RunID, CallID: "call-authority-approval",
		RunAttemptGeneration: prepared.Manifest.RunAttemptGeneration,
		ToolCatalogDigest:    prepared.Manifest.ExecutorMCP.CatalogDigest,
		ExecutionID:          "61000000-0000-4000-8000-000000000061",
		ApprovalID:           "62000000-0000-4000-8000-000000000062",
		Nonce:                "63000000-0000-4000-8000-000000000063", ApprovalVersion: 1,
		ContextHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ExpiresAt:   time.Now().Add(time.Minute).UTC(),
	}
	contextHashBytes, err := hex.DecodeString(request.ContextHash)
	if err != nil {
		t.Fatal(err)
	}
	var contextHash [32]byte
	copy(contextHash[:], contextHashBytes)
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	decidedAt := now.Add(time.Second)
	pending := ObserveApprovalResult{
		ExecutionID: request.ExecutionID, ExecutionStatus: "pending_approval", ExecutionVersion: 1,
		Approval: ObservedApproval{
			ApprovalID: request.ApprovalID, ExecutionID: request.ExecutionID,
			RunID: request.RunID, RunAttemptID: prepared.Manifest.RunAttemptID,
			RunAttemptGeneration: request.RunAttemptGeneration, Nonce: request.Nonce,
			ContextHash: contextHash, Status: "pending", Version: 1, ExpiresAt: request.ExpiresAt,
		},
	}
	approved := pending
	approved.ExecutionVersion = 2
	approved.OutcomeAvailable = true
	approved.Approval.Status = "approved"
	approved.Approval.Version = 2
	approved.Approval.ApproverID = "64000000-0000-4000-8000-000000000064"
	approved.Approval.Decision = "approve"
	approved.Approval.DecidedAt = &decidedAt

	core := &approvalObservationCore{
		poolTestCore: newPoolTestCore(prepared), responses: []ObserveApprovalResult{pending, approved},
	}
	identities := &poolTestTransitionAllocator{record: poolTestTransitionRecord()}
	authority := &attemptLifecycleAuthority{
		ctx: t.Context(), scheduler: &poolTestScheduler{}, core: core,
		identities: identities, prepared: prepared, turnWasAccepted: true,
	}
	outcome, err := authority.AwaitApproval(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	want := harnesscontrol.ApprovalOutcomeCommand{
		Kind:  harnesscontrol.CommandKindApprovalOutcome,
		RunID: request.RunID, CallID: request.CallID,
		RunAttemptGeneration: request.RunAttemptGeneration,
		ToolCatalogDigest:    request.ToolCatalogDigest,
		ExecutionID:          request.ExecutionID, ApprovalID: request.ApprovalID,
		Nonce: request.Nonce, ContextHash: request.ContextHash,
		Status: "approved", ApprovalVersion: 2,
	}
	if outcome != want {
		t.Fatalf("AwaitApproval() = %+v, want %+v", outcome, want)
	}

	requests := core.snapshot()
	if len(requests) != 2 || !reflect.DeepEqual(requests[0], requests[1]) {
		t.Fatalf("approval observation requests = %+v", requests)
	}
	claim := prepared.Scheduled.Claim
	observed := requests[0]
	if observed.RunAttemptID != prepared.Manifest.RunAttemptID ||
		observed.HolderID != claim.RunAttempt.HolderID ||
		observed.ContextHash != contextHash || observed.Wait != approvalCoreLongPoll ||
		observed.Record != identities.record || identities.calls != 1 {
		t.Fatalf("approval observation scope/identity = %+v / allocator %+v", observed, identities)
	}
}

type approvalObservationCore struct {
	*poolTestCore

	mu        sync.Mutex
	requests  []ObserveApprovalRequest
	responses []ObserveApprovalResult
}

func (core *approvalObservationCore) ObserveApproval(
	ctx context.Context,
	request ObserveApprovalRequest,
) (ObserveApprovalResult, error) {
	core.mu.Lock()
	defer core.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ObserveApprovalResult{}, context.Cause(ctx)
	}
	core.requests = append(core.requests, request)
	if len(core.responses) == 0 {
		return ObserveApprovalResult{}, context.DeadlineExceeded
	}
	response := core.responses[0]
	core.responses = core.responses[1:]
	return response, nil
}

func (core *approvalObservationCore) snapshot() []ObserveApprovalRequest {
	core.mu.Lock()
	defer core.mu.Unlock()
	return append([]ObserveApprovalRequest(nil), core.requests...)
}

var _ AttemptApprovalCore = (*approvalObservationCore)(nil)
