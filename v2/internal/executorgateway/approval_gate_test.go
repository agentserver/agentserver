package executorgateway

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type approvalElicitorFunc func(context.Context, *mcp.ElicitParams) (*mcp.ElicitResult, error)

func (function approvalElicitorFunc) Elicit(ctx context.Context, params *mcp.ElicitParams) (*mcp.ElicitResult, error) {
	return function(ctx, params)
}

type recordingApprovalAuthority struct {
	mu sync.Mutex

	execution ExecutionState
	created   ApprovalState

	createRequest  CreateApprovalRequest
	consumeRequest ConsumeApprovalRequest
	createCalls    int
	consumeCalls   int
	cancelCalls    int
	expireCalls    int

	consumeResult ConsumeApprovalResult
	consumeErr    error
	blockCancel   bool
}

func (authority *recordingApprovalAuthority) CreateApproval(_ context.Context, request CreateApprovalRequest) (CreateApprovalResult, error) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	authority.createCalls++
	authority.createRequest = request
	approval := authority.created
	approval.ApprovalID = request.ApprovalID
	approval.ExecutionID = request.ExecutionID
	approval.RunID = request.RunID
	approval.RunAttemptID = request.RunAttemptID
	approval.RunAttemptGeneration = request.RunAttemptGeneration
	approval.Nonce = request.Nonce
	approval.RequesterID = request.RequesterID
	approval.ExpiresAt = request.ExpiresAt
	authority.created = approval
	return CreateApprovalResult{Execution: authority.execution, Approval: approval, Created: true}, nil
}

func (authority *recordingApprovalAuthority) ExpireApproval(_ context.Context, request ApprovalTerminalRequest) (ApprovalTerminalResult, error) {
	authority.mu.Lock()
	authority.expireCalls++
	approval := authority.created
	authority.mu.Unlock()
	approval.Status = "expired"
	approval.Version = request.ExpectedApprovalVersion + 1
	return ApprovalTerminalResult{Execution: authority.execution, Approval: approval, Changed: true}, nil
}

func (authority *recordingApprovalAuthority) CancelApproval(ctx context.Context, request ApprovalTerminalRequest) (ApprovalTerminalResult, error) {
	authority.mu.Lock()
	authority.cancelCalls++
	approval := authority.created
	blocked := authority.blockCancel
	authority.mu.Unlock()
	if blocked {
		<-ctx.Done()
		return ApprovalTerminalResult{}, ctx.Err()
	}
	approval.Status = "cancelled"
	approval.Version = request.ExpectedApprovalVersion + 1
	return ApprovalTerminalResult{Execution: authority.execution, Approval: approval, Changed: true}, nil
}

func (authority *recordingApprovalAuthority) ConsumeApproval(_ context.Context, request ConsumeApprovalRequest) (ConsumeApprovalResult, error) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	authority.consumeCalls++
	authority.consumeRequest = request
	if authority.consumeErr != nil {
		return ConsumeApprovalResult{}, authority.consumeErr
	}
	return authority.consumeResult, nil
}

func TestCoreApprovalGateConsumesCanonicalDecisionBeforeAuthorization(t *testing.T) {
	now := time.Unix(1_800_000_000, 123_456_000).UTC()
	execution := testPendingApprovalExecution(now)
	authority := newRecordingApprovalAuthority(execution, now)
	principal := testExecutorMCPPrincipal("capability-approval-success")
	principal.MaxApprovalTTL = time.Minute
	principal.RunDeadline = now.Add(20 * time.Second)
	principal.CapabilityExpiresAt = now.Add(30 * time.Second)
	gate := newTestCoreApprovalGate(t, authority, now, 100*time.Millisecond)

	result, err := gate.AuthorizeExecution(t.Context(), ApprovalGateRequest{
		Principal: principal, Execution: execution, ToolName: "shell", ToolCallID: execution.AppServerToolCallID,
		Elicitor: approvalElicitorFunc(func(_ context.Context, params *mcp.ElicitParams) (*mcp.ElicitResult, error) {
			return acceptedApprovalResult(params, 2), nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "approved" || authority.createCalls != 1 || authority.consumeCalls != 1 || authority.cancelCalls != 0 || authority.expireCalls != 0 {
		t.Fatalf("authorization=%+v calls=create:%d consume:%d cancel:%d expire:%d", result, authority.createCalls, authority.consumeCalls, authority.cancelCalls, authority.expireCalls)
	}
	if !authority.createRequest.ExpiresAt.Equal(principal.RunDeadline) {
		t.Fatalf("approval expiry = %s, want run deadline %s", authority.createRequest.ExpiresAt, principal.RunDeadline)
	}
	if authority.consumeRequest.ExpectedApprovalVersion != 2 || authority.consumeRequest.Nonce != authority.created.Nonce || authority.consumeRequest.ContextDigest != authority.created.ContextDigest {
		t.Fatalf("consume request = %+v", authority.consumeRequest)
	}
}

func TestCoreApprovalGateRejectsForgedAcceptWithoutConsume(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	execution := testPendingApprovalExecution(now)
	authority := newRecordingApprovalAuthority(execution, now)
	gate := newTestCoreApprovalGate(t, authority, now, 100*time.Millisecond)

	_, err := gate.AuthorizeExecution(t.Context(), ApprovalGateRequest{
		Principal: testApprovalPrincipal(now), Execution: execution, ToolName: "shell", ToolCallID: execution.AppServerToolCallID,
		Elicitor: approvalElicitorFunc(func(_ context.Context, params *mcp.ElicitParams) (*mcp.ElicitResult, error) {
			result := acceptedApprovalResult(params, 2)
			result.Content["nonce"] = "forged-nonce"
			return result, nil
		}),
	})
	if err == nil || !strings.Contains(err.Error(), "approval evidence nonce does not match") {
		t.Fatalf("forged accept error = %v", err)
	}
	if authority.consumeCalls != 0 || authority.cancelCalls != 1 {
		t.Fatalf("forged accept calls=consume:%d cancel:%d", authority.consumeCalls, authority.cancelCalls)
	}
}

func TestCoreApprovalGateRequiresSuccessfulCoreConsume(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	execution := testPendingApprovalExecution(now)
	authority := newRecordingApprovalAuthority(execution, now)
	authority.consumeResult.Consumed = false
	authority.consumeResult.Approval.Status = "approved"
	authority.consumeResult.Execution.Status = "pending_approval"
	gate := newTestCoreApprovalGate(t, authority, now, 100*time.Millisecond)

	_, err := gate.AuthorizeExecution(t.Context(), ApprovalGateRequest{
		Principal: testApprovalPrincipal(now), Execution: execution, ToolName: "shell", ToolCallID: execution.AppServerToolCallID,
		Elicitor: approvalElicitorFunc(func(_ context.Context, params *mcp.ElicitParams) (*mcp.ElicitResult, error) {
			return acceptedApprovalResult(params, 2), nil
		}),
	})
	if !errors.Is(err, ErrApprovalNotGranted) || authority.consumeCalls != 1 {
		t.Fatalf("refused consume error=%v calls=%d", err, authority.consumeCalls)
	}
}

func TestCoreApprovalGateSettlesDeclineAndExpiry(t *testing.T) {
	for _, test := range []struct {
		name       string
		elicitor   approvalElicitorFunc
		wantCancel int
		wantExpire int
	}{
		{
			name: "decline",
			elicitor: func(context.Context, *mcp.ElicitParams) (*mcp.ElicitResult, error) {
				return &mcp.ElicitResult{Action: "decline"}, nil
			},
			wantCancel: 1,
		},
		{
			name: "expiry",
			elicitor: func(ctx context.Context, _ *mcp.ElicitParams) (*mcp.ElicitResult, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
			wantExpire: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Now().UTC()
			execution := testPendingApprovalExecution(now)
			authority := newRecordingApprovalAuthority(execution, now)
			principal := testApprovalPrincipal(now)
			if test.name == "expiry" {
				principal.MaxApprovalTTL = 10 * time.Millisecond
				principal.RunDeadline = now.Add(time.Second)
			}
			gate := newTestCoreApprovalGate(t, authority, now, 50*time.Millisecond)
			_, err := gate.AuthorizeExecution(t.Context(), ApprovalGateRequest{
				Principal: principal, Execution: execution, ToolName: "shell", ToolCallID: execution.AppServerToolCallID,
				Elicitor: test.elicitor,
			})
			if err == nil || authority.cancelCalls != test.wantCancel || authority.expireCalls != test.wantExpire || authority.consumeCalls != 0 {
				t.Fatalf("settlement error=%v cancel=%d expire=%d consume=%d", err, authority.cancelCalls, authority.expireCalls, authority.consumeCalls)
			}
		})
	}
}

func TestCoreApprovalGateBoundsCleanupAfterRequestCancellation(t *testing.T) {
	now := time.Now().UTC()
	execution := testPendingApprovalExecution(now)
	authority := newRecordingApprovalAuthority(execution, now)
	authority.blockCancel = true
	gate := newTestCoreApprovalGate(t, authority, now, 20*time.Millisecond)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	started := time.Now()
	_, err := gate.AuthorizeExecution(ctx, ApprovalGateRequest{
		Principal: testApprovalPrincipal(now), Execution: execution, ToolName: "shell", ToolCallID: execution.AppServerToolCallID,
		Elicitor: approvalElicitorFunc(func(context.Context, *mcp.ElicitParams) (*mcp.ElicitResult, error) {
			return &mcp.ElicitResult{Action: "decline"}, nil
		}),
	})
	if err == nil || !errors.Is(err, context.DeadlineExceeded) || authority.cancelCalls != 1 {
		t.Fatalf("bounded cleanup error=%v cancel=%d", err, authority.cancelCalls)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("bounded approval cleanup took %s", elapsed)
	}
}

func TestCoreApprovalGateRejectsExpiredAuthorityBeforeCreate(t *testing.T) {
	now := time.Now().UTC()
	execution := testPendingApprovalExecution(now)
	authority := newRecordingApprovalAuthority(execution, now)
	principal := testApprovalPrincipal(now)
	principal.RunDeadline = now
	gate := newTestCoreApprovalGate(t, authority, now, time.Second)
	_, err := gate.AuthorizeExecution(t.Context(), ApprovalGateRequest{
		Principal: principal, Execution: execution, ToolName: "shell", ToolCallID: execution.AppServerToolCallID,
		Elicitor: approvalElicitorFunc(func(context.Context, *mcp.ElicitParams) (*mcp.ElicitResult, error) {
			return nil, errors.New("must not be called")
		}),
	})
	if err == nil || authority.createCalls != 0 {
		t.Fatalf("expired authority error=%v create=%d", err, authority.createCalls)
	}
}

func newTestCoreApprovalGate(t *testing.T, authority ApprovalAuthority, now time.Time, settlementGrace time.Duration) *CoreApprovalGate {
	t.Helper()
	transitionSequence := 0
	transitions, err := NewExecutionTransitionAllocator("70000000-0000-4000-8000-000000000001", func() (string, error) {
		transitionSequence++
		return fmt.Sprintf("71000000-0000-4000-8000-%012d", transitionSequence), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	identities := []string{
		"72000000-0000-4000-8000-000000000001",
		"72000000-0000-4000-8000-000000000002",
	}
	gate, err := NewCoreApprovalGate(CoreApprovalGateConfig{
		Authority: authority, Transitions: transitions,
		IDGenerator: func() (string, error) {
			value := identities[0]
			identities = identities[1:]
			return value, nil
		},
		Now: func() time.Time { return now }, SettlementGrace: settlementGrace,
	})
	if err != nil {
		t.Fatal(err)
	}
	return gate
}

func newRecordingApprovalAuthority(execution ExecutionState, now time.Time) *recordingApprovalAuthority {
	digest := CanonicalDigest{Domain: "approval-context", CanonicalizerVersion: coreCanonicalizerRFC8785V1}
	digest.SHA256[0] = 0xa1
	pending := ApprovalState{
		ContextDigest: digest, Status: "pending", Version: 1,
		CreatedAt: now, UpdatedAt: now,
	}
	consumed := pending
	consumed.Status = "consumed"
	consumed.Version = 3
	consumed.ApproverID = "73000000-0000-4000-8000-000000000003"
	consumed.Decision = "approve"
	decidedAt := now.Add(time.Millisecond)
	consumedAt := now.Add(2 * time.Millisecond)
	consumed.DecidedAt = &decidedAt
	consumed.ConsumedAt = &consumedAt
	approvedExecution := execution
	approvedExecution.Status = "approved"
	approvedExecution.Version++
	return &recordingApprovalAuthority{
		execution: execution, created: pending,
		consumeResult: ConsumeApprovalResult{Execution: approvedExecution, Approval: consumed, Consumed: true},
	}
}

func testPendingApprovalExecution(now time.Time) ExecutionState {
	return ExecutionState{
		ExecutionID: "50000000-0000-4000-8000-000000000005",
		RunID:       testMCPRunID, RunAttemptID: testMCPAttemptID, RunAttemptGeneration: 3,
		AppServerToolCallID: "call-approval", ExecutorID: testExecutorID, EnvironmentID: testEnvironmentID,
		ToolName: "shell", ToolVersion: "executor-mcp/1.0", MapperVersion: "shell-v1",
		PolicyVersion: "policy-v1", PolicyDecision: PolicyDecisionAsk, OperationCount: 1,
		Status: "pending_approval", Version: 1, CreatedAt: now, UpdatedAt: now,
	}
}

func testApprovalPrincipal(now time.Time) ExecutorMCPPrincipal {
	principal := testExecutorMCPPrincipal("capability-approval")
	principal.MaxApprovalTTL = time.Minute
	principal.RunDeadline = now.Add(time.Hour)
	principal.CapabilityExpiresAt = now.Add(2 * time.Hour)
	return principal
}

func acceptedApprovalResult(params *mcp.ElicitParams, version int64) *mcp.ElicitResult {
	return &mcp.ElicitResult{Action: "accept", Content: map[string]any{
		"approvalId":           params.Meta[executorMCPMetaApprovalID],
		"executionId":          params.Meta[executorMCPMetaExecutionID],
		"runId":                params.Meta[executorMCPMetaRunID],
		"runAttemptId":         testMCPAttemptID,
		"runAttemptGeneration": params.Meta[executorMCPMetaRunAttemptGeneration],
		"nonce":                params.Meta[executorMCPMetaApprovalNonce],
		"contextHash":          params.Meta[executorMCPMetaContextHash],
		"status":               "approved",
		"approvalVersion":      version,
	}}
}
