package executorgateway

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

type ApprovalState struct {
	ApprovalID           string
	ExecutionID          string
	RunID                string
	RunAttemptID         string
	RunAttemptGeneration int64
	Nonce                string
	RequesterID          string
	ApproverID           string
	Decision             string
	ContextDigest        CanonicalDigest
	Status               string
	ExpiresAt            time.Time
	DecidedAt            *time.Time
	ConsumedAt           *time.Time
	Version              int64
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type CreateApprovalRequest struct {
	ApprovalID               string
	ExecutionID              string
	RunID                    string
	RunAttemptID             string
	HolderID                 string
	RunAttemptGeneration     int64
	ExpectedExecutionVersion int64
	Nonce                    string
	RequesterID              string
	ExpiresAt                time.Time
	Record                   ExecutionTransitionRecord
}

type CreateApprovalResult struct {
	Execution ExecutionState
	Approval  ApprovalState
	Created   bool
}

type ApprovalTerminalRequest struct {
	ApprovalID              string
	Nonce                   string
	ContextDigest           CanonicalDigest
	ExpectedApprovalVersion int64
	Record                  ExecutionTransitionRecord
}

type ApprovalTerminalResult struct {
	Execution ExecutionState
	Approval  ApprovalState
	Changed   bool
}

type ConsumeApprovalRequest struct {
	ApprovalID               string
	ExecutionID              string
	RunID                    string
	RunAttemptID             string
	HolderID                 string
	RunAttemptGeneration     int64
	Nonce                    string
	ContextDigest            CanonicalDigest
	ExpectedApprovalVersion  int64
	ExpectedExecutionVersion int64
	Record                   ExecutionTransitionRecord
}

type ConsumeApprovalResult struct {
	Execution ExecutionState
	Approval  ApprovalState
	Consumed  bool
}

type ApprovalAuthority interface {
	CreateApproval(context.Context, CreateApprovalRequest) (CreateApprovalResult, error)
	ExpireApproval(context.Context, ApprovalTerminalRequest) (ApprovalTerminalResult, error)
	CancelApproval(context.Context, ApprovalTerminalRequest) (ApprovalTerminalResult, error)
	ConsumeApproval(context.Context, ConsumeApprovalRequest) (ConsumeApprovalResult, error)
}

var _ ApprovalAuthority = (*CoreConnectionClient)(nil)

func (client *CoreConnectionClient) CreateApproval(ctx context.Context, request CreateApprovalRequest) (CreateApprovalResult, error) {
	contractRequest := corecontract.CreateApprovalRequest{
		ApprovalID: request.ApprovalID, ExecutionID: request.ExecutionID,
		RunID: request.RunID, RunAttemptID: request.RunAttemptID, HolderID: request.HolderID,
		RunAttemptGeneration: request.RunAttemptGeneration, ExpectedExecutionVersion: request.ExpectedExecutionVersion,
		Nonce: request.Nonce, RequesterID: request.RequesterID, ExpiresAt: request.ExpiresAt,
		Record: contractExecutionTransitionRecord(request.Record),
	}
	var response corecontract.CreateApprovalResponse
	if err := client.post(ctx, corecontract.CreateApprovalPath, contractRequest, &response, http.StatusOK); err != nil {
		return CreateApprovalResult{}, err
	}
	execution, approval, err := gatewayApprovalResponse(response.Execution, response.Approval)
	if err != nil {
		return CreateApprovalResult{}, fmt.Errorf("validate core CreateApproval response: %w", err)
	}
	if approval.ApprovalID != request.ApprovalID || approval.ExecutionID != request.ExecutionID ||
		approval.RunID != request.RunID || approval.RunAttemptID != request.RunAttemptID ||
		approval.RunAttemptGeneration != request.RunAttemptGeneration || approval.Nonce != request.Nonce ||
		approval.RequesterID != request.RequesterID || !approval.ExpiresAt.Equal(request.ExpiresAt) {
		return CreateApprovalResult{}, errors.New("core CreateApproval response does not match the requested approval fingerprint")
	}
	return CreateApprovalResult{Execution: execution, Approval: approval, Created: response.Created}, nil
}

func (client *CoreConnectionClient) ExpireApproval(ctx context.Context, request ApprovalTerminalRequest) (ApprovalTerminalResult, error) {
	return client.approvalTerminal(ctx, corecontract.ExpireApprovalPath(request.ApprovalID), request)
}

func (client *CoreConnectionClient) CancelApproval(ctx context.Context, request ApprovalTerminalRequest) (ApprovalTerminalResult, error) {
	return client.approvalTerminal(ctx, corecontract.CancelApprovalPath(request.ApprovalID), request)
}

func (client *CoreConnectionClient) approvalTerminal(ctx context.Context, path string, request ApprovalTerminalRequest) (ApprovalTerminalResult, error) {
	contractRequest := corecontract.ApprovalTerminalRequest{
		ApprovalID: request.ApprovalID, Nonce: request.Nonce,
		ContextDigest:           contractCanonicalDigest(request.ContextDigest),
		ExpectedApprovalVersion: request.ExpectedApprovalVersion,
		Record:                  contractExecutionTransitionRecord(request.Record),
	}
	var response corecontract.ApprovalTerminalResponse
	if err := client.post(ctx, path, contractRequest, &response, http.StatusOK); err != nil {
		return ApprovalTerminalResult{}, err
	}
	execution, approval, err := gatewayApprovalResponse(response.Execution, response.Approval)
	if err != nil {
		return ApprovalTerminalResult{}, fmt.Errorf("validate core approval terminal response: %w", err)
	}
	if approval.ApprovalID != request.ApprovalID || approval.Nonce != request.Nonce || approval.ContextDigest != request.ContextDigest {
		return ApprovalTerminalResult{}, errors.New("core approval terminal response does not match the requested capability")
	}
	return ApprovalTerminalResult{Execution: execution, Approval: approval, Changed: response.Changed}, nil
}

func (client *CoreConnectionClient) ConsumeApproval(ctx context.Context, request ConsumeApprovalRequest) (ConsumeApprovalResult, error) {
	contractRequest := corecontract.ConsumeApprovalRequest{
		ApprovalID: request.ApprovalID, ExecutionID: request.ExecutionID,
		RunID: request.RunID, RunAttemptID: request.RunAttemptID, HolderID: request.HolderID,
		RunAttemptGeneration: request.RunAttemptGeneration, Nonce: request.Nonce,
		ContextDigest:            requestContractDigest(request.ContextDigest),
		ExpectedApprovalVersion:  request.ExpectedApprovalVersion,
		ExpectedExecutionVersion: request.ExpectedExecutionVersion,
		Record:                   contractExecutionTransitionRecord(request.Record),
	}
	var response corecontract.ConsumeApprovalResponse
	if err := client.post(ctx, corecontract.ConsumeApprovalPath(request.ApprovalID), contractRequest, &response, http.StatusOK); err != nil {
		return ConsumeApprovalResult{}, err
	}
	execution, approval, err := gatewayApprovalResponse(response.Execution, response.Approval)
	if err != nil {
		return ConsumeApprovalResult{}, fmt.Errorf("validate core ConsumeApproval response: %w", err)
	}
	if approval.ApprovalID != request.ApprovalID || approval.ExecutionID != request.ExecutionID ||
		approval.RunID != request.RunID || approval.RunAttemptID != request.RunAttemptID ||
		approval.RunAttemptGeneration != request.RunAttemptGeneration || approval.Nonce != request.Nonce ||
		approval.ContextDigest != request.ContextDigest {
		return ConsumeApprovalResult{}, errors.New("core ConsumeApproval response does not match the requested approval capability")
	}
	return ConsumeApprovalResult{Execution: execution, Approval: approval, Consumed: response.Consumed}, nil
}

func gatewayApprovalResponse(executionSource corecontract.ExecutionState, approvalSource corecontract.ApprovalState) (ExecutionState, ApprovalState, error) {
	execution, err := gatewayExecutionState(executionSource)
	if err != nil {
		return ExecutionState{}, ApprovalState{}, fmt.Errorf("execution: %w", err)
	}
	approval, err := gatewayApprovalState(approvalSource)
	if err != nil {
		return ExecutionState{}, ApprovalState{}, fmt.Errorf("approval: %w", err)
	}
	if approval.ExecutionID != execution.ExecutionID || approval.RunID != execution.RunID ||
		approval.RunAttemptID != execution.RunAttemptID || approval.RunAttemptGeneration != execution.RunAttemptGeneration {
		return ExecutionState{}, ApprovalState{}, errors.New("approval scope differs from its execution")
	}
	return execution, approval, nil
}

func gatewayApprovalState(source corecontract.ApprovalState) (ApprovalState, error) {
	contextDigest, err := gatewayCanonicalDigest(source.ContextDigest, "approval-context")
	if err != nil {
		return ApprovalState{}, fmt.Errorf("context digest: %w", err)
	}
	for name, value := range map[string]string{
		"approval ID": source.ApprovalID, "execution ID": source.ExecutionID,
		"run ID": source.RunID, "run attempt ID": source.RunAttemptID, "nonce": source.Nonce,
	} {
		if err := validateRegistryIdentity(name, value); err != nil {
			return ApprovalState{}, err
		}
	}
	if source.RunAttemptGeneration < 1 || source.Version < 1 || source.ExpiresAt.IsZero() || source.CreatedAt.IsZero() || source.UpdatedAt.IsZero() {
		return ApprovalState{}, errors.New("approval generation, version, or timestamps are invalid")
	}
	if source.RequesterID == "" || len(source.RequesterID) > 256 || !utf8.ValidString(source.RequesterID) || strings.ContainsRune(source.RequesterID, 0) {
		return ApprovalState{}, errors.New("approval requester is invalid")
	}
	if !validApprovalStatus(source.Status) {
		return ApprovalState{}, fmt.Errorf("unsupported approval status %q", source.Status)
	}
	if source.ApproverID != "" {
		if err := validateRegistryIdentity("approval approver ID", source.ApproverID); err != nil {
			return ApprovalState{}, err
		}
	}
	if source.Decision != "" && source.Decision != "approve" && source.Decision != "deny" {
		return ApprovalState{}, fmt.Errorf("unsupported approval decision %q", source.Decision)
	}
	if err := validateApprovalDecisionEvidence(source); err != nil {
		return ApprovalState{}, err
	}
	return ApprovalState{
		ApprovalID: source.ApprovalID, ExecutionID: source.ExecutionID, RunID: source.RunID,
		RunAttemptID: source.RunAttemptID, RunAttemptGeneration: source.RunAttemptGeneration,
		Nonce: source.Nonce, RequesterID: source.RequesterID, ApproverID: source.ApproverID,
		Decision: source.Decision, ContextDigest: contextDigest, Status: source.Status,
		ExpiresAt: source.ExpiresAt, DecidedAt: source.DecidedAt, ConsumedAt: source.ConsumedAt,
		Version: source.Version, CreatedAt: source.CreatedAt, UpdatedAt: source.UpdatedAt,
	}, nil
}

func validateApprovalDecisionEvidence(source corecontract.ApprovalState) error {
	switch source.Status {
	case "pending":
		if source.ApproverID != "" || source.Decision != "" || source.DecidedAt != nil || source.ConsumedAt != nil {
			return errors.New("pending approval contains terminal decision evidence")
		}
	case "approved":
		if source.ApproverID == "" || source.Decision != "approve" || source.DecidedAt == nil || source.ConsumedAt != nil {
			return errors.New("approved approval lacks canonical decision evidence")
		}
	case "denied":
		if source.ApproverID == "" || source.Decision != "deny" || source.DecidedAt == nil || source.ConsumedAt != nil {
			return errors.New("denied approval lacks canonical decision evidence")
		}
	case "expired", "cancelled":
		if source.DecidedAt == nil || source.ConsumedAt != nil ||
			(source.ApproverID == "") != (source.Decision == "") ||
			(source.Decision != "" && source.Decision != "approve") {
			return errors.New("terminal approval contains invalid decision evidence")
		}
	case "consumed":
		if source.ApproverID == "" || source.Decision != "approve" || source.DecidedAt == nil || source.ConsumedAt == nil || source.ConsumedAt.Before(*source.DecidedAt) {
			return errors.New("consumed approval lacks canonical consumption evidence")
		}
	}
	return nil
}

func validApprovalStatus(status string) bool {
	switch status {
	case "pending", "approved", "denied", "expired", "cancelled", "consumed":
		return true
	default:
		return false
	}
}

func contractCanonicalDigest(digest CanonicalDigest) corecontract.CanonicalJSONDigest {
	return requestContractDigest(digest)
}

func requestContractDigest(digest CanonicalDigest) corecontract.CanonicalJSONDigest {
	return corecontract.CanonicalJSONDigest{
		Domain: digest.Domain, CanonicalizerVersion: digest.CanonicalizerVersion,
		SHA256: hex.EncodeToString(digest.SHA256[:]),
	}
}
