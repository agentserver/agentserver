package harnesspool

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/agentserver/agentserver/v2/internal/harnesscontrol"
)

const approvalCoreLongPoll = 20 * time.Second

// AwaitApproval performs no approval decision itself. It repeatedly asks Core
// for the canonical state using the exact worker-projected capability and
// returns only a canonical status/version command for the worker to correlate.
func (authority *attemptLifecycleAuthority) AwaitApproval(ctx context.Context, request harnesscontrol.ApprovalRequestEvent) (harnesscontrol.ApprovalOutcomeCommand, error) {
	if ctx == nil {
		return harnesscontrol.ApprovalOutcomeCommand{}, errors.New("approval wait context is required")
	}
	if err := request.Validate(); err != nil {
		return harnesscontrol.ApprovalOutcomeCommand{}, err
	}
	if authority == nil || authority.ctx == nil || authority.identities == nil {
		return harnesscontrol.ApprovalOutcomeCommand{}, errors.New("attempt approval authority is unavailable")
	}
	core, ok := authority.core.(AttemptApprovalCore)
	if !ok {
		return harnesscontrol.ApprovalOutcomeCommand{}, errors.New("attempt core client does not implement approval observation")
	}
	authority.mu.Lock()
	claim := authority.prepared.Scheduled.Claim
	manifest := authority.prepared.Manifest
	accepted := authority.turnWasAccepted
	authority.mu.Unlock()
	if !accepted || request.RunID != manifest.RunID || request.RunAttemptGeneration != manifest.RunAttemptGeneration ||
		request.ToolCatalogDigest != manifest.ExecutorMCP.CatalogDigest {
		return harnesscontrol.ApprovalOutcomeCommand{}, errors.New("approval request escaped the accepted signed attempt")
	}
	record, err := authority.identities.AllocateTransitionRecord()
	if err != nil {
		return harnesscontrol.ApprovalOutcomeCommand{}, fmt.Errorf("allocate approval observation transition identity: %w", err)
	}
	waitCtx, cancel := context.WithCancelCause(ctx)
	stopAttempt := context.AfterFunc(authority.ctx, func() {
		cancel(context.Cause(authority.ctx))
	})
	defer stopAttempt()
	defer cancel(nil)

	contextHash, err := decodeClientSHA256(request.ContextHash)
	if err != nil {
		return harnesscontrol.ApprovalOutcomeCommand{}, err
	}
	observe := ObserveApprovalRequest{
		ApprovalID: request.ApprovalID, ExecutionID: request.ExecutionID,
		RunID: request.RunID, RunAttemptID: manifest.RunAttemptID,
		HolderID: claim.RunAttempt.HolderID, RunAttemptGeneration: request.RunAttemptGeneration,
		Nonce: request.Nonce, ContextHash: contextHash, AfterApprovalVersion: request.ApprovalVersion,
		Wait: approvalCoreLongPoll, Record: record,
	}
	for {
		observed, err := core.ObserveApproval(waitCtx, observe)
		if err != nil {
			return harnesscontrol.ApprovalOutcomeCommand{}, err
		}
		if !observed.OutcomeAvailable {
			continue
		}
		return harnesscontrol.ApprovalOutcomeCommand{
			Kind:  harnesscontrol.CommandKindApprovalOutcome,
			RunID: request.RunID, CallID: request.CallID,
			RunAttemptGeneration: request.RunAttemptGeneration, ToolCatalogDigest: request.ToolCatalogDigest,
			ExecutionID: request.ExecutionID, ApprovalID: request.ApprovalID,
			Nonce: request.Nonce, ContextHash: request.ContextHash,
			Status: observed.Approval.Status, ApprovalVersion: observed.Approval.Version,
		}, nil
	}
}

var _ AttemptApprovalLifecycle = (*attemptLifecycleAuthority)(nil)
