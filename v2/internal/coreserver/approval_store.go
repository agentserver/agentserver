package coreserver

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
)

type ApprovalStateStore interface {
	CreateApproval(context.Context, coredb.CreateApprovalCommand) (coredb.CreateApprovalResult, error)
	DecideApproval(context.Context, coredb.DecideApprovalCommand) (coredb.DecideApprovalResult, error)
	ExpireApproval(context.Context, coredb.ExpireApprovalCommand) (coredb.ExpireApprovalResult, error)
	CancelApproval(context.Context, coredb.CancelApprovalCommand) (coredb.CancelApprovalResult, error)
	ConsumeApproval(context.Context, coredb.ConsumeApprovalCommand) (coredb.ConsumeApprovalResult, error)
	ObserveApproval(context.Context, coredb.ObserveApprovalCommand) (coredb.ObserveApprovalResult, error)
}

type StateStoreApprovalCommands struct {
	Store ApprovalStateStore
}

var _ ApprovalCommands = StateStoreApprovalCommands{}

func (commands StateStoreApprovalCommands) CreateApproval(ctx context.Context, request corecontract.CreateApprovalRequest) (corecontract.CreateApprovalResponse, error) {
	if commands.Store == nil {
		return corecontract.CreateApprovalResponse{}, errors.New("nil core approval store")
	}
	result, err := commands.Store.CreateApproval(ctx, coredb.CreateApprovalCommand{
		ApprovalID: request.ApprovalID, ExecutionID: request.ExecutionID, RunID: request.RunID,
		AttemptID: request.RunAttemptID, HolderID: request.HolderID,
		Generation: request.RunAttemptGeneration, ExpectedExecutionVersion: request.ExpectedExecutionVersion,
		Nonce: request.Nonce, RequesterID: request.RequesterID, ExpiresAt: request.ExpiresAt,
		Record: databaseTransitionRecord(request.Record),
	})
	if err != nil {
		return corecontract.CreateApprovalResponse{}, err
	}
	return corecontract.CreateApprovalResponse{
		Execution: contractExecution(result.Execution), Approval: contractApproval(result.Approval), Created: result.Created,
	}, nil
}

func (commands StateStoreApprovalCommands) ExpireApproval(ctx context.Context, request corecontract.ApprovalTerminalRequest) (corecontract.ApprovalTerminalResponse, error) {
	if commands.Store == nil {
		return corecontract.ApprovalTerminalResponse{}, errors.New("nil core approval store")
	}
	contextHash, err := approvalContextDigest(request.ContextDigest)
	if err != nil {
		return corecontract.ApprovalTerminalResponse{}, approvalConversionError("ExpireApproval", request.ApprovalID, err)
	}
	result, err := commands.Store.ExpireApproval(ctx, coredb.ExpireApprovalCommand{
		ApprovalID: request.ApprovalID, Nonce: request.Nonce, ExpectedContextHash: contextHash,
		ExpectedApprovalVersion: request.ExpectedApprovalVersion, Record: databaseTransitionRecord(request.Record),
	})
	if err != nil {
		return corecontract.ApprovalTerminalResponse{}, err
	}
	return corecontract.ApprovalTerminalResponse{
		Execution: contractExecution(result.Execution), Approval: contractApproval(result.Approval), Changed: result.Changed,
	}, nil
}

func (commands StateStoreApprovalCommands) CancelApproval(ctx context.Context, request corecontract.ApprovalTerminalRequest) (corecontract.ApprovalTerminalResponse, error) {
	if commands.Store == nil {
		return corecontract.ApprovalTerminalResponse{}, errors.New("nil core approval store")
	}
	contextHash, err := approvalContextDigest(request.ContextDigest)
	if err != nil {
		return corecontract.ApprovalTerminalResponse{}, approvalConversionError("CancelApproval", request.ApprovalID, err)
	}
	result, err := commands.Store.CancelApproval(ctx, coredb.CancelApprovalCommand{
		ApprovalID: request.ApprovalID, Nonce: request.Nonce, ExpectedContextHash: contextHash,
		ExpectedApprovalVersion: request.ExpectedApprovalVersion, Record: databaseTransitionRecord(request.Record),
	})
	if err != nil {
		return corecontract.ApprovalTerminalResponse{}, err
	}
	return corecontract.ApprovalTerminalResponse{
		Execution: contractExecution(result.Execution), Approval: contractApproval(result.Approval), Changed: result.Changed,
	}, nil
}

func (commands StateStoreApprovalCommands) ConsumeApproval(ctx context.Context, request corecontract.ConsumeApprovalRequest) (corecontract.ConsumeApprovalResponse, error) {
	if commands.Store == nil {
		return corecontract.ConsumeApprovalResponse{}, errors.New("nil core approval store")
	}
	contextHash, err := approvalContextDigest(request.ContextDigest)
	if err != nil {
		return corecontract.ConsumeApprovalResponse{}, approvalConversionError("ConsumeApprovalAndAuthorizeExecution", request.ApprovalID, err)
	}
	result, err := commands.Store.ConsumeApproval(ctx, coredb.ConsumeApprovalCommand{
		ApprovalID: request.ApprovalID, ExecutionID: request.ExecutionID, RunID: request.RunID,
		AttemptID: request.RunAttemptID, HolderID: request.HolderID, Generation: request.RunAttemptGeneration,
		Nonce: request.Nonce, ExpectedContextHash: contextHash,
		ExpectedApprovalVersion: request.ExpectedApprovalVersion, ExpectedExecutionVersion: request.ExpectedExecutionVersion,
		Record: databaseTransitionRecord(request.Record),
	})
	if err != nil {
		return corecontract.ConsumeApprovalResponse{}, err
	}
	return corecontract.ConsumeApprovalResponse{
		Execution: contractExecution(result.Execution), Approval: contractApproval(result.Approval), Consumed: result.Consumed,
	}, nil
}

func (commands StateStoreApprovalCommands) ObserveApproval(ctx context.Context, request corecontract.ObserveApprovalRequest) (corecontract.ObserveApprovalResponse, error) {
	if commands.Store == nil {
		return corecontract.ObserveApprovalResponse{}, errors.New("nil core approval store")
	}
	contextHash, err := approvalContextDigest(request.ContextDigest)
	if err != nil {
		return corecontract.ObserveApprovalResponse{}, approvalConversionError("ObserveApproval", request.ApprovalID, err)
	}
	result, err := commands.Store.ObserveApproval(ctx, coredb.ObserveApprovalCommand{
		ApprovalID: request.ApprovalID, ExecutionID: request.ExecutionID,
		RunID: request.RunID, AttemptID: request.RunAttemptID, HolderID: request.HolderID,
		Generation: request.RunAttemptGeneration, Nonce: request.Nonce,
		ExpectedContextHash: contextHash, AfterApprovalVersion: request.AfterApprovalVersion,
		Record: databaseTransitionRecord(request.Record),
	})
	if err != nil {
		return corecontract.ObserveApprovalResponse{}, err
	}
	if result.Approval.ID != request.ApprovalID || result.Execution.ID != request.ExecutionID ||
		result.Approval.ExecutionID != request.ExecutionID || result.Execution.RunID != request.RunID ||
		result.Approval.RunID != request.RunID || result.Execution.RunAttemptID != request.RunAttemptID ||
		result.Approval.RunAttemptID != request.RunAttemptID ||
		result.Execution.RunAttemptGeneration != request.RunAttemptGeneration ||
		result.Approval.RunAttemptGeneration != request.RunAttemptGeneration ||
		result.Approval.Nonce != request.Nonce || result.Approval.ContextHash.SHA256() != contextHash ||
		result.Approval.Version < request.AfterApprovalVersion {
		return corecontract.ObserveApprovalResponse{}, errors.New("core state store returned an invalid approval observation scope")
	}
	return corecontract.ObserveApprovalResponse{
		ExecutionID: result.Execution.ID, ExecutionStatus: result.Execution.Status,
		ExecutionVersion: result.Execution.Version, Approval: contractApproval(result.Approval),
		OutcomeAvailable: result.Approval.Status != coredb.ApprovalStatusPending,
	}, nil
}

func contractApproval(approval coredb.Approval) corecontract.ApprovalState {
	return corecontract.ApprovalState{
		ApprovalID: approval.ID, ExecutionID: approval.ExecutionID, RunID: approval.RunID,
		RunAttemptID: approval.RunAttemptID, RunAttemptGeneration: approval.RunAttemptGeneration,
		Nonce: approval.Nonce, RequesterID: approval.RequesterID, ApproverID: approval.ApproverID,
		Decision: approval.Decision, ContextDigest: contractCanonicalJSONDigest(approval.ContextHash),
		Status: approval.Status, ExpiresAt: approval.ExpiresAt, DecidedAt: approval.DecidedAt,
		ConsumedAt: approval.ConsumedAt, Version: approval.Version,
		CreatedAt: approval.CreatedAt, UpdatedAt: approval.UpdatedAt,
	}
}

func approvalContextDigest(digest corecontract.CanonicalJSONDigest) ([32]byte, error) {
	if digest.Domain != string(coredb.HashDomainApprovalContext) {
		return [32]byte{}, fmt.Errorf("context digest domain must be %q", coredb.HashDomainApprovalContext)
	}
	if digest.CanonicalizerVersion != coredb.CanonicalizerRFC8785V1 {
		return [32]byte{}, fmt.Errorf("context digest canonicalizer must be %q", coredb.CanonicalizerRFC8785V1)
	}
	decoded, err := hex.DecodeString(digest.SHA256)
	if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != digest.SHA256 {
		return [32]byte{}, errors.New("context digest SHA-256 must be 64 canonical lowercase hexadecimal characters")
	}
	var result [32]byte
	copy(result[:], decoded)
	if result == [32]byte{} {
		return [32]byte{}, errors.New("context digest SHA-256 must not be all zero")
	}
	return result, nil
}

func approvalConversionError(operation, approvalID string, err error) error {
	return &coredb.StateError{
		Code: coredb.ErrorInvalidArgument, Operation: operation, Resource: "approval", ResourceID: approvalID,
		Message: fmt.Sprintf("invalid internal command: %v", err),
	}
}
