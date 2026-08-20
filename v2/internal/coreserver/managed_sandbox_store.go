package coreserver

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
)

type ManagedSandboxStateStore interface {
	ReserveManagedSandbox(context.Context, coredb.ReserveManagedSandboxCommand) (coredb.ReserveManagedSandboxResult, error)
	GetManagedSandbox(context.Context, string, int64) (coredb.ManagedSandbox, error)
	BeginManagedSandboxCreate(context.Context, coredb.BeginManagedSandboxCreateCommand) (coredb.ManagedSandbox, bool, error)
	ObserveManagedSandbox(context.Context, coredb.ObserveManagedSandboxCommand) (coredb.ManagedSandbox, bool, error)
	RenewManagedSandboxActivity(context.Context, coredb.RenewManagedSandboxActivityCommand) (coredb.ManagedSandbox, error)
	ReleaseManagedSandboxActivity(context.Context, coredb.ReleaseManagedSandboxActivityCommand) (coredb.ManagedSandbox, bool, error)
	BeginManagedSandboxDelete(context.Context, coredb.BeginManagedSandboxDeleteCommand) (coredb.ManagedSandbox, bool, error)
	ListManagedSandboxesForReconcile(context.Context, coredb.ListManagedSandboxesForReconcileQuery) ([]coredb.ManagedSandbox, error)
	AuthorizeManagedSandboxOperation(context.Context, coredb.AuthorizeManagedSandboxOperationQuery) (coredb.AuthorizedManagedSandboxOperation, error)
}

func (commands StateStoreManagedSandboxCommands) AuthorizeManagedSandboxOperation(ctx context.Context, request corecontract.AuthorizeManagedSandboxOperationRequest) (corecontract.AuthorizeManagedSandboxOperationResponse, error) {
	if commands.Store == nil {
		return corecontract.AuthorizeManagedSandboxOperationResponse{}, errors.New("nil core state store")
	}
	authorized, err := commands.Store.AuthorizeManagedSandboxOperation(ctx, coredb.AuthorizeManagedSandboxOperationQuery{
		WorkspaceID: request.WorkspaceID, SessionID: request.SessionID,
		RunID: request.RunID, AttemptID: request.RunAttemptID,
		AttemptGeneration: request.RunAttemptGeneration,
		ExecutionID:       request.ExecutionID, OperationID: request.OperationID,
		MutationKey: request.MutationKey, SandboxID: request.SandboxID,
		TargetGeneration: request.TargetGeneration, EnvironmentID: request.EnvironmentID,
		Action: request.Action,
	})
	if err != nil {
		return corecontract.AuthorizeManagedSandboxOperationResponse{}, err
	}
	return corecontract.AuthorizeManagedSandboxOperationResponse{
		SandboxID: authorized.SandboxID, TargetGeneration: authorized.TargetGeneration,
		OperationID: authorized.OperationID, OperationKind: authorized.OperationKind,
		AuthorizedAt: authorized.AuthorizedAt,
	}, nil
}

type StateStoreManagedSandboxCommands struct {
	Store ManagedSandboxStateStore
}

func (commands StateStoreManagedSandboxCommands) ReserveManagedSandbox(ctx context.Context, request corecontract.ReserveManagedSandboxRequest) (corecontract.ReserveManagedSandboxResponse, error) {
	if commands.Store == nil {
		return corecontract.ReserveManagedSandboxResponse{}, errors.New("nil core state store")
	}
	result, err := commands.Store.ReserveManagedSandbox(ctx, coredb.ReserveManagedSandboxCommand{
		SandboxID: request.SandboxID, WorkspaceID: request.WorkspaceID,
		SessionID: request.SessionID, EnvironmentID: request.EnvironmentID,
		ProviderRegion: request.ProviderRegion, ProviderPSM: request.ProviderPSM,
		ProviderSessionRef:   request.ProviderSessionRef,
		CreateIdempotencyKey: request.CreateIdempotencyKey,
		RequestedTTL:         time.Duration(request.RequestedTTLSeconds) * time.Second,
		RequestedIdleTTL:     time.Duration(request.IdleTTLSeconds) * time.Second,
	})
	if err != nil {
		return corecontract.ReserveManagedSandboxResponse{}, err
	}
	return corecontract.ReserveManagedSandboxResponse{Sandbox: contractManagedSandbox(result.Sandbox), Created: result.Created}, nil
}

func (commands StateStoreManagedSandboxCommands) GetManagedSandbox(ctx context.Context, sandboxID string, generation int64) (corecontract.GetManagedSandboxResponse, error) {
	if commands.Store == nil {
		return corecontract.GetManagedSandboxResponse{}, errors.New("nil core state store")
	}
	sandbox, err := commands.Store.GetManagedSandbox(ctx, sandboxID, generation)
	if err != nil {
		return corecontract.GetManagedSandboxResponse{}, err
	}
	return corecontract.GetManagedSandboxResponse{Sandbox: contractManagedSandbox(sandbox)}, nil
}

func (commands StateStoreManagedSandboxCommands) BeginManagedSandboxCreate(ctx context.Context, request corecontract.BeginManagedSandboxCreateRequest) (corecontract.ManagedSandboxMutationResponse, error) {
	if commands.Store == nil {
		return corecontract.ManagedSandboxMutationResponse{}, errors.New("nil core state store")
	}
	sandbox, changed, err := commands.Store.BeginManagedSandboxCreate(ctx, coredb.BeginManagedSandboxCreateCommand{
		SandboxID: request.SandboxID, Generation: request.Generation, ExpectedVersion: request.ExpectedVersion,
	})
	if err != nil {
		return corecontract.ManagedSandboxMutationResponse{}, err
	}
	return corecontract.ManagedSandboxMutationResponse{Sandbox: contractManagedSandbox(sandbox), Changed: changed}, nil
}

func (commands StateStoreManagedSandboxCommands) ObserveManagedSandbox(ctx context.Context, request corecontract.ObserveManagedSandboxRequest) (corecontract.ManagedSandboxMutationResponse, error) {
	if commands.Store == nil {
		return corecontract.ManagedSandboxMutationResponse{}, errors.New("nil core state store")
	}
	errorDigest, err := managedSandboxOptionalDigest("errorSha256", request.ErrorSHA256)
	if err != nil {
		return corecontract.ManagedSandboxMutationResponse{}, managedSandboxConversionError("ObserveManagedSandbox", request.SandboxID, err)
	}
	sandbox, changed, err := commands.Store.ObserveManagedSandbox(ctx, coredb.ObserveManagedSandboxCommand{
		SandboxID: request.SandboxID, Generation: request.Generation,
		ExpectedVersion: request.ExpectedVersion, ObservedState: request.ObservedState,
		ProviderSessionRef: request.ProviderSessionRef, ExpiresAt: request.ExpiresAt,
		ErrorCode: request.ErrorCode, ErrorDigest: errorDigest,
	})
	if err != nil {
		return corecontract.ManagedSandboxMutationResponse{}, err
	}
	return corecontract.ManagedSandboxMutationResponse{Sandbox: contractManagedSandbox(sandbox), Changed: changed}, nil
}

func (commands StateStoreManagedSandboxCommands) RenewManagedSandboxActivity(ctx context.Context, request corecontract.RenewManagedSandboxActivityRequest) (corecontract.ManagedSandboxMutationResponse, error) {
	if commands.Store == nil {
		return corecontract.ManagedSandboxMutationResponse{}, errors.New("nil core state store")
	}
	sandbox, err := commands.Store.RenewManagedSandboxActivity(ctx, coredb.RenewManagedSandboxActivityCommand{
		SandboxID: request.SandboxID, Generation: request.Generation, RunID: request.RunID,
		AttemptID: request.RunAttemptID, AttemptGeneration: request.RunAttemptGeneration,
		HolderID: request.HolderID, ActivityTTL: time.Duration(request.ActivityTTLMillis) * time.Millisecond,
	})
	if err != nil {
		return corecontract.ManagedSandboxMutationResponse{}, err
	}
	return corecontract.ManagedSandboxMutationResponse{Sandbox: contractManagedSandbox(sandbox), Changed: true}, nil
}

func (commands StateStoreManagedSandboxCommands) ReleaseManagedSandboxActivity(ctx context.Context, request corecontract.ReleaseManagedSandboxActivityRequest) (corecontract.ManagedSandboxMutationResponse, error) {
	if commands.Store == nil {
		return corecontract.ManagedSandboxMutationResponse{}, errors.New("nil core state store")
	}
	sandbox, changed, err := commands.Store.ReleaseManagedSandboxActivity(ctx, coredb.ReleaseManagedSandboxActivityCommand{
		SandboxID: request.SandboxID, Generation: request.Generation, RunID: request.RunID,
		AttemptID: request.RunAttemptID, AttemptGeneration: request.RunAttemptGeneration,
		HolderID: request.HolderID, IdleTTL: time.Duration(request.IdleTTLMillis) * time.Millisecond,
	})
	if err != nil {
		return corecontract.ManagedSandboxMutationResponse{}, err
	}
	return corecontract.ManagedSandboxMutationResponse{Sandbox: contractManagedSandbox(sandbox), Changed: changed}, nil
}

func (commands StateStoreManagedSandboxCommands) BeginManagedSandboxDelete(ctx context.Context, request corecontract.BeginManagedSandboxDeleteRequest) (corecontract.ManagedSandboxMutationResponse, error) {
	if commands.Store == nil {
		return corecontract.ManagedSandboxMutationResponse{}, errors.New("nil core state store")
	}
	sandbox, changed, err := commands.Store.BeginManagedSandboxDelete(ctx, coredb.BeginManagedSandboxDeleteCommand{
		SandboxID: request.SandboxID, Generation: request.Generation,
		ExpectedVersion: request.ExpectedVersion, Reason: request.Reason,
	})
	if err != nil {
		return corecontract.ManagedSandboxMutationResponse{}, err
	}
	return corecontract.ManagedSandboxMutationResponse{Sandbox: contractManagedSandbox(sandbox), Changed: changed}, nil
}

func (commands StateStoreManagedSandboxCommands) ListManagedSandboxesForReconcile(ctx context.Context, request corecontract.ListManagedSandboxesForReconcileRequest) (corecontract.ListManagedSandboxesForReconcileResponse, error) {
	if commands.Store == nil {
		return corecontract.ListManagedSandboxesForReconcileResponse{}, errors.New("nil core state store")
	}
	sandboxes, err := commands.Store.ListManagedSandboxesForReconcile(ctx, coredb.ListManagedSandboxesForReconcileQuery{Limit: request.Limit})
	if err != nil {
		return corecontract.ListManagedSandboxesForReconcileResponse{}, err
	}
	response := corecontract.ListManagedSandboxesForReconcileResponse{Sandboxes: make([]corecontract.ManagedSandboxState, len(sandboxes))}
	for index, sandbox := range sandboxes {
		response.Sandboxes[index] = contractManagedSandbox(sandbox)
	}
	return response, nil
}

func contractManagedSandbox(sandbox coredb.ManagedSandbox) corecontract.ManagedSandboxState {
	result := corecontract.ManagedSandboxState{
		SandboxID: sandbox.ID, WorkspaceID: sandbox.WorkspaceID,
		SessionID: sandbox.SessionID, EnvironmentID: sandbox.EnvironmentID,
		ProviderKind: sandbox.ProviderKind, Generation: sandbox.Generation,
		DesiredState: sandbox.DesiredState, ObservedState: sandbox.ObservedState,
		ProviderRegion: sandbox.ProviderRegion, ProviderPSM: sandbox.ProviderPSM,
		ProviderSessionRef:   sandbox.ProviderSessionRef,
		CreateIdempotencyKey: sandbox.CreateIdempotencyKey,
		RequestedTTLSeconds:  int64(sandbox.RequestedTTL / time.Second),
		IdleTTLSeconds:       int64(sandbox.IdleTTL / time.Second),
		ExpiresAt:            sandbox.ExpiresAt, IdleExpiresAt: sandbox.IdleExpiresAt,
		LastObservedAt: sandbox.LastObservedAt, Version: sandbox.Version,
		CreatedAt: sandbox.CreatedAt, UpdatedAt: sandbox.UpdatedAt,
		DeletedAt: sandbox.DeletedAt, LastErrorCode: sandbox.LastErrorCode,
	}
	if sandbox.LastErrorDigest != nil {
		result.LastErrorSHA256 = hex.EncodeToString(sandbox.LastErrorDigest[:])
	}
	return result
}

func managedSandboxDigest(name, value string, optional bool) ([32]byte, error) {
	if value == "" && optional {
		return [32]byte{}, nil
	}
	raw, err := hex.DecodeString(value)
	if err != nil || len(raw) != 32 || hex.EncodeToString(raw) != value {
		return [32]byte{}, fmt.Errorf("%s must be 64 lowercase hexadecimal characters", name)
	}
	var digest [32]byte
	copy(digest[:], raw)
	return digest, nil
}

func managedSandboxOptionalDigest(name, value string) (*[32]byte, error) {
	if value == "" {
		return nil, nil
	}
	digest, err := managedSandboxDigest(name, value, false)
	if err != nil {
		return nil, err
	}
	return &digest, nil
}

func managedSandboxConversionError(operation, sandboxID string, err error) error {
	return &coredb.StateError{
		Code: coredb.ErrorInvalidArgument, Operation: operation,
		Resource: "sandbox", ResourceID: sandboxID,
		Message: fmt.Sprintf("invalid internal command: %v", err),
	}
}

var _ ManagedSandboxCommands = StateStoreManagedSandboxCommands{}
