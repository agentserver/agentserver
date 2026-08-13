package executorgateway

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	ErrExecutionPolicyDenied = errors.New("execution denied by policy")
	ErrApprovalNotGranted    = errors.New("execution approval was not granted")
)

const (
	defaultApprovalSettlementGrace = 3 * time.Second
	maximumApprovalSettlementGrace = 30 * time.Second
	// The gateway and one-shot worker run on different nodes. Leave bounded
	// headroom below the signed MaxApprovalTTL so a gateway clock that is a few
	// seconds ahead cannot make an otherwise valid approval look overlong to
	// the worker before the request is even journaled.
	maximumApprovalExpirySkewAllowance = 5 * time.Second
)

type ApprovalElicitor interface {
	Elicit(context.Context, *mcp.ElicitParams) (*mcp.ElicitResult, error)
}

type ApprovalGateRequest struct {
	Principal  ExecutorMCPPrincipal
	Execution  ExecutionState
	ToolName   string
	ToolCallID string
	Elicitor   ApprovalElicitor
}

type ExecutionApprovalGate interface {
	AuthorizeExecution(context.Context, ApprovalGateRequest) (ExecutionState, error)
}

type CoreApprovalGateConfig struct {
	Authority   ApprovalAuthority
	Transitions *ExecutionTransitionAllocator
	IDGenerator IDGenerator
	Now         func() time.Time
	// SettlementGrace bounds best-effort expire/cancel after the MCP request
	// has already ended. Zero selects the bounded default.
	SettlementGrace time.Duration
}

// CoreApprovalGate owns policy=ask from durable approval creation through the
// one-shot consume boundary. MCP accept is only correlation evidence: Core
// independently requires an approved canonical decision, live holder and
// generation, unexpired nonce/digest, and current approver RBAC before it
// changes the execution to approved.
type CoreApprovalGate struct {
	authority       ApprovalAuthority
	transitions     *ExecutionTransitionAllocator
	idGenerator     IDGenerator
	now             func() time.Time
	settlementGrace time.Duration

	idMu sync.Mutex
}

func NewCoreApprovalGate(config CoreApprovalGateConfig) (*CoreApprovalGate, error) {
	if config.Authority == nil || config.Transitions == nil || config.IDGenerator == nil || config.Now == nil {
		return nil, errors.New("approval authority, transition allocator, identity generator, and clock are required")
	}
	if config.SettlementGrace == 0 {
		config.SettlementGrace = defaultApprovalSettlementGrace
	}
	if config.SettlementGrace < 0 || config.SettlementGrace > maximumApprovalSettlementGrace {
		return nil, fmt.Errorf("approval settlement grace must be positive and at most %s", maximumApprovalSettlementGrace)
	}
	return &CoreApprovalGate{
		authority: config.Authority, transitions: config.Transitions,
		idGenerator: config.IDGenerator, now: config.Now, settlementGrace: config.SettlementGrace,
	}, nil
}

func NewDefaultCoreApprovalGate(authority ApprovalAuthority, transitions *ExecutionTransitionAllocator) (*CoreApprovalGate, error) {
	return NewCoreApprovalGate(CoreApprovalGateConfig{
		Authority: authority, Transitions: transitions, IDGenerator: newRandomUUID, Now: time.Now,
	})
}

func (gate *CoreApprovalGate) AuthorizeExecution(ctx context.Context, request ApprovalGateRequest) (ExecutionState, error) {
	if gate == nil {
		return ExecutionState{}, errors.New("execution approval gate is required")
	}
	if ctx == nil || request.Elicitor == nil {
		return ExecutionState{}, errors.New("approval context and MCP elicitor are required")
	}
	if err := validateExecutorMCPPrincipal(request.Principal); err != nil {
		return ExecutionState{}, fmt.Errorf("approval principal: %w", err)
	}
	if request.Execution.Status != "pending_approval" || request.Execution.PolicyDecision != PolicyDecisionAsk {
		return ExecutionState{}, fmt.Errorf("approval gate requires a pending policy=ask execution, got %q/%q", request.Execution.PolicyDecision, request.Execution.Status)
	}
	if request.Execution.RunID != request.Principal.Run.RunID ||
		request.Execution.RunAttemptID != request.Principal.Run.RunAttemptID ||
		request.Execution.RunAttemptGeneration != request.Principal.Run.RunAttemptGeneration ||
		request.Execution.AppServerToolCallID != request.ToolCallID || request.Execution.ToolName != request.ToolName {
		return ExecutionState{}, errors.New("approval execution differs from the authenticated MCP call")
	}

	approvalID, nonce, err := gate.allocateApprovalIdentity()
	if err != nil {
		return ExecutionState{}, err
	}
	now := gate.now().UTC().Truncate(time.Microsecond)
	expiresAt := minApprovalExpiry(
		now.Add(approvalTTLWithClockSkewHeadroom(request.Principal.MaxApprovalTTL)),
		request.Principal.RunDeadline,
		request.Principal.CapabilityExpiresAt,
	).UTC().Truncate(time.Microsecond)
	if !expiresAt.After(now) {
		return ExecutionState{}, errors.New("execution approval authority has already expired")
	}
	record, err := gate.allocateRecord("create approval")
	if err != nil {
		return ExecutionState{}, err
	}
	created, err := gate.authority.CreateApproval(ctx, CreateApprovalRequest{
		ApprovalID: approvalID, ExecutionID: request.Execution.ExecutionID,
		RunID: request.Execution.RunID, RunAttemptID: request.Execution.RunAttemptID,
		HolderID: request.Principal.Run.HolderID, RunAttemptGeneration: request.Execution.RunAttemptGeneration,
		ExpectedExecutionVersion: request.Execution.Version, Nonce: nonce,
		RequesterID: request.Principal.ActorID, ExpiresAt: expiresAt, Record: record,
	})
	if err != nil {
		return ExecutionState{}, fmt.Errorf("create execution approval: %w", err)
	}
	if created.Execution != request.Execution || created.Approval.Status != "pending" || created.Approval.Version < 1 {
		return ExecutionState{}, errors.New("Core created an approval with an unexpected execution or status")
	}

	params, err := approvalElicitParams(request, created.Approval)
	if err != nil {
		settleErr := gate.cancelApproval(ctx, created.Approval)
		return ExecutionState{}, errors.Join(err, settleErr)
	}
	// The approval authority still expires at ExpiresAt. The extra bounded
	// transport window only lets the worker receive Core's database-time
	// terminal outcome; ConsumeApproval independently rejects an approval once
	// that authority boundary has passed.
	elicitationCtx, cancel := context.WithDeadline(ctx, created.Approval.ExpiresAt.Add(gate.settlementGrace))
	result, elicitErr := request.Elicitor.Elicit(elicitationCtx, params)
	timedOut := gate.approvalWaitReachedExpiry(ctx, elicitationCtx, created.Approval.ExpiresAt)
	cancel()
	if elicitErr != nil {
		settleErr := gate.settleUnconsumed(ctx, created.Approval, timedOut)
		return ExecutionState{}, errors.Join(fmt.Errorf("await canonical approval outcome: %w", elicitErr), settleErr)
	}
	if result == nil {
		settleErr := gate.settleUnconsumed(ctx, created.Approval, timedOut)
		return ExecutionState{}, errors.Join(errors.New("MCP elicitation returned no result"), settleErr)
	}
	if result.Action != "accept" {
		settleErr := gate.settleUnconsumed(ctx, created.Approval, timedOut || result.Action == "decline" && !gate.now().Before(created.Approval.ExpiresAt))
		return ExecutionState{}, errors.Join(fmt.Errorf("%w: elicitation action %q", ErrApprovalNotGranted, result.Action), settleErr)
	}
	evidence, err := parseApprovalEvidence(result.Content, request, created.Approval)
	if err != nil {
		settleErr := gate.cancelApproval(ctx, created.Approval)
		return ExecutionState{}, errors.Join(fmt.Errorf("validate canonical approval evidence: %w", err), settleErr)
	}
	record, err = gate.allocateRecord("consume approval")
	if err != nil {
		return ExecutionState{}, errors.Join(err, gate.cancelApproval(ctx, created.Approval))
	}
	consumed, err := gate.authority.ConsumeApproval(ctx, ConsumeApprovalRequest{
		ApprovalID: created.Approval.ApprovalID, ExecutionID: created.Execution.ExecutionID,
		RunID: created.Execution.RunID, RunAttemptID: created.Execution.RunAttemptID,
		HolderID: request.Principal.Run.HolderID, RunAttemptGeneration: created.Execution.RunAttemptGeneration,
		Nonce: created.Approval.Nonce, ContextDigest: created.Approval.ContextDigest,
		ExpectedApprovalVersion: evidence.ApprovalVersion, ExpectedExecutionVersion: created.Execution.Version,
		Record: record,
	})
	if err != nil {
		return ExecutionState{}, fmt.Errorf("consume canonical execution approval: %w", err)
	}
	if consumed.Approval.Status != "consumed" || consumed.Execution.Status != "approved" {
		return ExecutionState{}, fmt.Errorf("%w: Core outcome is approval=%q execution=%q", ErrApprovalNotGranted, consumed.Approval.Status, consumed.Execution.Status)
	}
	return consumed.Execution, nil
}

func (gate *CoreApprovalGate) approvalWaitReachedExpiry(parent, wait context.Context, expiresAt time.Time) bool {
	if !gate.now().Before(expiresAt) {
		return true
	}
	if !errors.Is(wait.Err(), context.DeadlineExceeded) {
		return false
	}
	// A parent deadline before the approval boundary is request cancellation,
	// not evidence of approval expiry. Otherwise the gate-owned transport
	// deadline can only fire after ExpiresAt plus the bounded grace.
	parentDeadline, ok := parent.Deadline()
	return !ok || !parentDeadline.Before(expiresAt)
}

type approvalDecisionEvidence struct {
	ApprovalVersion int64
}

func approvalElicitParams(request ApprovalGateRequest, approval ApprovalState) (*mcp.ElicitParams, error) {
	schema := json.RawMessage(`{"type":"object","properties":{"approvalId":{"type":"string"},"executionId":{"type":"string"},"runId":{"type":"string"},"runAttemptId":{"type":"string"},"runAttemptGeneration":{"type":"integer"},"nonce":{"type":"string"},"contextHash":{"type":"string"},"status":{"type":"string","enum":["approved"]},"approvalVersion":{"type":"integer","minimum":1}},"required":["approvalId","executionId","runId","runAttemptId","runAttemptGeneration","nonce","contextHash","status","approvalVersion"],"additionalProperties":false}`)
	if !json.Valid(schema) {
		return nil, errors.New("internal approval elicitation schema is invalid")
	}
	return &mcp.ElicitParams{
		Meta: mcp.Meta{
			executorMCPMetaRunID:                request.Principal.Run.RunID,
			executorMCPMetaCallID:               request.ToolCallID,
			executorMCPMetaRunAttemptGeneration: request.Principal.Run.RunAttemptGeneration,
			executorMCPMetaToolCatalogDigest:    request.Principal.ToolCatalogDigest,
			executorMCPMetaExecutionID:          approval.ExecutionID,
			executorMCPMetaApprovalID:           approval.ApprovalID,
			executorMCPMetaApprovalNonce:        approval.Nonce,
			executorMCPMetaApprovalVersion:      approval.Version,
			executorMCPMetaContextHash:          hex.EncodeToString(approval.ContextDigest.SHA256[:]),
			executorMCPMetaExpiresAt:            approval.ExpiresAt.UTC().Format(time.RFC3339Nano),
			executorMCPMetaProgressToken:        request.ToolCallID,
		},
		Mode: "form", Message: fmt.Sprintf("Approve executor tool %q for this run?", request.ToolName),
		RequestedSchema: schema,
	}, nil
}

func parseApprovalEvidence(content map[string]any, request ApprovalGateRequest, approval ApprovalState) (approvalDecisionEvidence, error) {
	if len(content) != 9 {
		return approvalDecisionEvidence{}, errors.New("accepted approval evidence must contain exactly nine fields")
	}
	wantStrings := map[string]string{
		"approvalId": approval.ApprovalID, "executionId": approval.ExecutionID,
		"runId": approval.RunID, "runAttemptId": approval.RunAttemptID,
		"nonce": approval.Nonce, "contextHash": hex.EncodeToString(approval.ContextDigest.SHA256[:]),
		"status": "approved",
	}
	for key, want := range wantStrings {
		value, ok := content[key].(string)
		if !ok || value != want {
			return approvalDecisionEvidence{}, fmt.Errorf("approval evidence %s does not match", key)
		}
	}
	generation, err := approvalEvidenceInt64(content["runAttemptGeneration"])
	if err != nil || generation != request.Principal.Run.RunAttemptGeneration {
		return approvalDecisionEvidence{}, errors.New("approval evidence runAttemptGeneration does not match")
	}
	version, err := approvalEvidenceInt64(content["approvalVersion"])
	if err != nil || version <= approval.Version {
		return approvalDecisionEvidence{}, errors.New("approval evidence version is not a later canonical decision version")
	}
	return approvalDecisionEvidence{ApprovalVersion: version}, nil
}

func approvalEvidenceInt64(value any) (int64, error) {
	switch value := value.(type) {
	case int:
		return int64(value), nil
	case int64:
		return value, nil
	case json.Number:
		return value.Int64()
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) || value != math.Trunc(value) || value < 1 || value > float64(1<<53-1) {
			return 0, errors.New("not a positive safe integer")
		}
		return int64(value), nil
	default:
		return 0, fmt.Errorf("unexpected integer type %T", value)
	}
}

func (gate *CoreApprovalGate) settleUnconsumed(ctx context.Context, approval ApprovalState, expired bool) error {
	settlementCtx, cancel := gate.settlementContext(ctx)
	defer cancel()
	if expired {
		result, err := gate.expireApproval(settlementCtx, approval)
		if err == nil || result.Approval.Status == "expired" {
			return err
		}
		// Database time is authoritative. If the local deadline ran slightly
		// ahead, cancellation lets Core choose cancelled versus expired.
	}
	_, err := gate.terminalApproval(settlementCtx, approval, false)
	return err
}

func (gate *CoreApprovalGate) expireApproval(ctx context.Context, approval ApprovalState) (ApprovalTerminalResult, error) {
	return gate.terminalApproval(ctx, approval, true)
}

func (gate *CoreApprovalGate) cancelApproval(ctx context.Context, approval ApprovalState) error {
	settlementCtx, cancel := gate.settlementContext(ctx)
	defer cancel()
	_, err := gate.terminalApproval(settlementCtx, approval, false)
	return err
}

func (gate *CoreApprovalGate) settlementContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(ctx), gate.settlementGrace)
}

func (gate *CoreApprovalGate) terminalApproval(ctx context.Context, approval ApprovalState, expire bool) (ApprovalTerminalResult, error) {
	version := approval.Version
	for attempt := 0; attempt < 2; attempt++ {
		record, err := gate.allocateRecord("settle approval")
		if err != nil {
			return ApprovalTerminalResult{}, err
		}
		request := ApprovalTerminalRequest{
			ApprovalID: approval.ApprovalID, Nonce: approval.Nonce, ContextDigest: approval.ContextDigest,
			ExpectedApprovalVersion: version, Record: record,
		}
		var result ApprovalTerminalResult
		if expire {
			result, err = gate.authority.ExpireApproval(ctx, request)
		} else {
			result, err = gate.authority.CancelApproval(ctx, request)
		}
		if err == nil {
			return result, nil
		}
		var commandError *CoreCommandError
		if !errors.As(err, &commandError) || commandError.Code != "version_conflict" || commandError.CurrentVersion <= version {
			return ApprovalTerminalResult{}, err
		}
		version = commandError.CurrentVersion
	}
	return ApprovalTerminalResult{}, errors.New("approval settlement version changed repeatedly")
}

func (gate *CoreApprovalGate) allocateApprovalIdentity() (string, string, error) {
	gate.idMu.Lock()
	defer gate.idMu.Unlock()
	approvalID, err := gate.idGenerator()
	if err != nil {
		return "", "", fmt.Errorf("allocate approval ID: %w", err)
	}
	nonce, err := gate.idGenerator()
	if err != nil {
		return "", "", fmt.Errorf("allocate approval nonce: %w", err)
	}
	if err := validateRegistryIdentity("approval ID", approvalID); err != nil {
		return "", "", err
	}
	if err := validateRegistryIdentity("approval nonce", nonce); err != nil {
		return "", "", err
	}
	if approvalID == nonce {
		return "", "", errors.New("approval ID and nonce must be distinct")
	}
	return approvalID, nonce, nil
}

func (gate *CoreApprovalGate) allocateRecord(action string) (ExecutionTransitionRecord, error) {
	record, err := gate.transitions.Allocate()
	if err != nil {
		return ExecutionTransitionRecord{}, fmt.Errorf("allocate transition to %s: %w", action, err)
	}
	return record, nil
}

var _ ExecutionApprovalGate = (*CoreApprovalGate)(nil)

func minApprovalExpiry(values ...time.Time) time.Time {
	result := values[0]
	for _, value := range values[1:] {
		if value.Before(result) {
			result = value
		}
	}
	return result
}

func approvalTTLWithClockSkewHeadroom(ttl time.Duration) time.Duration {
	allowance := maximumApprovalExpirySkewAllowance
	// Preserve a useful approval window for deliberately short development and
	// test policies while still avoiding the exact signed-limit boundary.
	if half := ttl / 2; allowance > half {
		allowance = half
	}
	return ttl - allowance
}
