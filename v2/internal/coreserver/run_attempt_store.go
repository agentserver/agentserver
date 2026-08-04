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

type RunAttemptStateStore interface {
	ClaimQueuedRun(context.Context, coredb.ClaimQueuedRunCommand) (coredb.ClaimQueuedRunResult, error)
	RenewRunAttemptLeases(context.Context, coredb.RenewRunAttemptLeasesCommand) (coredb.RenewRunAttemptLeasesResult, error)
	InterruptAttempt(context.Context, coredb.InterruptAttemptCommand) (coredb.InterruptAttemptResult, error)
	CommitAttemptTerminal(context.Context, coredb.CommitAttemptTerminalCommand) (coredb.CommitAttemptTerminalResult, error)
	AbandonAttempt(context.Context, coredb.AbandonAttemptCommand) (coredb.AbandonAttemptResult, error)
	MarkTurnAccepted(context.Context, coredb.MarkTurnAcceptedCommand) (coredb.MarkTurnAcceptedResult, error)
	BeginRunFinalization(context.Context, coredb.BeginRunFinalizationCommand) (coredb.BeginRunFinalizationResult, error)
	CommitCheckpointAndTerminalRun(context.Context, coredb.CommitCheckpointAndTerminalRunCommand) (coredb.CommitCheckpointAndTerminalRunResult, error)
	AppendAttemptEvents(context.Context, coredb.AppendAttemptEventsCommand) (coredb.AppendAttemptEventsResult, error)
}

type StateStoreRunAttemptCommands struct {
	Store RunAttemptStateStore
}

var _ RunAttemptCommands = StateStoreRunAttemptCommands{}

func (commands StateStoreRunAttemptCommands) ClaimRunAttempt(ctx context.Context, request corecontract.ClaimRunAttemptRequest) (corecontract.ClaimRunAttemptResponse, error) {
	if commands.Store == nil {
		return corecontract.ClaimRunAttemptResponse{}, errors.New("nil core state store")
	}
	leaseTTL, err := runAttemptLeaseTTL(request.LeaseTTLMillis)
	if err != nil {
		return corecontract.ClaimRunAttemptResponse{}, runAttemptConversionError("ClaimQueuedRun", "run", request.RunID, err)
	}
	result, err := commands.Store.ClaimQueuedRun(ctx, coredb.ClaimQueuedRunCommand{
		RunID:              request.RunID,
		AttemptID:          request.RunAttemptID,
		HolderID:           request.HolderID,
		ExpectedRunVersion: request.ExpectedRunVersion,
		LeaseTTL:           leaseTTL,
		Record:             databaseTransitionRecord(request.Record),
	})
	if err != nil {
		return corecontract.ClaimRunAttemptResponse{}, err
	}
	return corecontract.ClaimRunAttemptResponse{
		Run:          contractRun(result.Run),
		RunAttempt:   contractRunAttempt(result.Attempt),
		SessionLease: contractLease(result.SessionLease),
		AttemptLease: contractLease(result.AttemptLease),
		Created:      result.Created,
		Reclaimed:    result.Reclaimed,
	}, nil
}

func (commands StateStoreRunAttemptCommands) RenewRunAttempt(ctx context.Context, request corecontract.RenewRunAttemptRequest) (corecontract.RenewRunAttemptResponse, error) {
	if commands.Store == nil {
		return corecontract.RenewRunAttemptResponse{}, errors.New("nil core state store")
	}
	leaseTTL, err := runAttemptLeaseTTL(request.LeaseTTLMillis)
	if err != nil {
		return corecontract.RenewRunAttemptResponse{}, runAttemptConversionError("RenewRunAttemptLeases", "attempt", request.RunAttemptID, err)
	}
	result, err := commands.Store.RenewRunAttemptLeases(ctx, coredb.RenewRunAttemptLeasesCommand{
		SessionID:  request.SessionID,
		RunID:      request.RunID,
		AttemptID:  request.RunAttemptID,
		HolderID:   request.HolderID,
		Generation: request.RunAttemptGeneration,
		LeaseTTL:   leaseTTL,
	})
	if err != nil {
		return corecontract.RenewRunAttemptResponse{}, err
	}
	return corecontract.RenewRunAttemptResponse{
		Run:          contractRun(result.Run),
		RunAttempt:   contractRunAttempt(result.Attempt),
		SessionLease: contractLease(result.SessionLease),
		AttemptLease: contractLease(result.AttemptLease),
	}, nil
}

func (commands StateStoreRunAttemptCommands) InterruptRunAttempt(ctx context.Context, request corecontract.InterruptRunAttemptRequest) (corecontract.InterruptRunAttemptResponse, error) {
	if commands.Store == nil {
		return corecontract.InterruptRunAttemptResponse{}, errors.New("nil core state store")
	}
	result, err := commands.Store.InterruptAttempt(ctx, coredb.InterruptAttemptCommand{
		RunID: request.RunID, AttemptID: request.RunAttemptID, HolderID: request.HolderID,
		Generation: request.RunAttemptGeneration, ExpectedRunVersion: request.ExpectedRunVersion,
		ExpectedAttemptVersion: request.ExpectedRunAttemptVersion, Reason: request.Reason,
		Record: databaseTransitionRecord(request.Record),
	})
	if err != nil {
		return corecontract.InterruptRunAttemptResponse{}, err
	}
	return corecontract.InterruptRunAttemptResponse{
		Run: contractRun(result.Run), RunAttempt: contractRunAttempt(result.Attempt),
		SessionVersion: result.SessionVersion, Changed: result.Changed,
	}, nil
}

func (commands StateStoreRunAttemptCommands) CommitAttemptTerminal(ctx context.Context, request corecontract.CommitAttemptTerminalRequest) (corecontract.CommitAttemptTerminalResponse, error) {
	if commands.Store == nil {
		return corecontract.CommitAttemptTerminalResponse{}, errors.New("nil core state store")
	}
	result, err := commands.Store.CommitAttemptTerminal(ctx, coredb.CommitAttemptTerminalCommand{
		RunID: request.RunID, AttemptID: request.RunAttemptID, HolderID: request.HolderID,
		Generation: request.RunAttemptGeneration, TerminalStatus: request.TerminalStatus,
		ThreadID: request.ThreadID, TurnID: request.TurnID, Code: request.Code, Message: request.Message,
		Record: databaseTransitionRecord(request.Record),
	})
	if err != nil {
		return corecontract.CommitAttemptTerminalResponse{}, err
	}
	return corecontract.CommitAttemptTerminalResponse{
		Run: contractRun(result.Run), RunAttempt: contractRunAttempt(result.Attempt),
		SessionVersion: result.SessionVersion, Disposition: result.Disposition, Changed: result.Changed,
	}, nil
}

func (commands StateStoreRunAttemptCommands) AbandonRunAttempt(ctx context.Context, request corecontract.AbandonRunAttemptRequest) (corecontract.AbandonRunAttemptResponse, error) {
	if commands.Store == nil {
		return corecontract.AbandonRunAttemptResponse{}, errors.New("nil core state store")
	}
	result, err := commands.Store.AbandonAttempt(ctx, coredb.AbandonAttemptCommand{
		RunID: request.RunID, AttemptID: request.RunAttemptID, HolderID: request.HolderID,
		Generation: request.RunAttemptGeneration, Reason: request.Reason, Terminal: request.Terminal,
		Record: databaseTransitionRecord(request.Record),
	})
	if err != nil {
		return corecontract.AbandonRunAttemptResponse{}, err
	}
	return corecontract.AbandonRunAttemptResponse{
		Run: contractRun(result.Run), RunAttempt: contractRunAttempt(result.Attempt),
		SessionVersion: result.SessionVersion, Disposition: result.Disposition, Changed: result.Changed,
	}, nil
}

func (commands StateStoreRunAttemptCommands) MarkTurnAccepted(ctx context.Context, request corecontract.MarkTurnAcceptedRequest) (corecontract.MarkTurnAcceptedResponse, error) {
	if commands.Store == nil {
		return corecontract.MarkTurnAcceptedResponse{}, errors.New("nil core state store")
	}
	result, err := commands.Store.MarkTurnAccepted(ctx, coredb.MarkTurnAcceptedCommand{
		RunID:                  request.RunID,
		AttemptID:              request.RunAttemptID,
		HolderID:               request.HolderID,
		Generation:             request.RunAttemptGeneration,
		ExpectedRunVersion:     request.ExpectedRunVersion,
		ExpectedAttemptVersion: request.ExpectedRunAttemptVersion,
		Record:                 databaseTransitionRecord(request.Record),
	})
	if err != nil {
		return corecontract.MarkTurnAcceptedResponse{}, err
	}
	return corecontract.MarkTurnAcceptedResponse{
		Run:        contractRun(result.Run),
		RunAttempt: contractRunAttempt(result.Attempt),
		Changed:    result.Changed,
	}, nil
}

func (commands StateStoreRunAttemptCommands) BeginRunFinalization(ctx context.Context, request corecontract.BeginRunFinalizationRequest) (corecontract.BeginRunFinalizationResponse, error) {
	if commands.Store == nil {
		return corecontract.BeginRunFinalizationResponse{}, errors.New("nil core state store")
	}
	result, err := commands.Store.BeginRunFinalization(ctx, coredb.BeginRunFinalizationCommand{
		RunID: request.RunID, AttemptID: request.RunAttemptID, HolderID: request.HolderID,
		Generation: request.RunAttemptGeneration, ExpectedRunVersion: request.ExpectedRunVersion,
		ExpectedAttemptVersion: request.ExpectedRunAttemptVersion, ThreadID: request.ThreadID,
		TurnID: request.TurnID, Record: databaseTransitionRecord(request.Record),
	})
	if err != nil {
		return corecontract.BeginRunFinalizationResponse{}, err
	}
	return corecontract.BeginRunFinalizationResponse{
		Run: contractRun(result.Run), RunAttempt: contractRunAttempt(result.Attempt), Changed: result.Changed,
	}, nil
}

func (commands StateStoreRunAttemptCommands) CommitCheckpoint(ctx context.Context, request corecontract.CommitCheckpointRequest) (corecontract.CommitCheckpointResponse, error) {
	if commands.Store == nil {
		return corecontract.CommitCheckpointResponse{}, errors.New("nil core state store")
	}
	manifestDigest, err := decodeCanonicalSHA256(request.Checkpoint.ManifestDigest)
	if err != nil {
		return corecontract.CommitCheckpointResponse{}, runAttemptConversionError("CommitCheckpointAndTerminalRun", "checkpoint", request.Checkpoint.CheckpointID, fmt.Errorf("manifest digest: %w", err))
	}
	catalogDigest, err := decodeCanonicalSHA256(request.Checkpoint.CatalogDigest)
	if err != nil {
		return corecontract.CommitCheckpointResponse{}, runAttemptConversionError("CommitCheckpointAndTerminalRun", "checkpoint", request.Checkpoint.CheckpointID, fmt.Errorf("catalog digest: %w", err))
	}
	objectDigest, err := decodeCanonicalSHA256(request.Checkpoint.Object.SHA256)
	if err != nil {
		return corecontract.CommitCheckpointResponse{}, runAttemptConversionError("CommitCheckpointAndTerminalRun", "checkpoint", request.Checkpoint.CheckpointID, fmt.Errorf("object digest: %w", err))
	}
	runtimeDigest, err := decodeCanonicalSHA256(request.Checkpoint.CodexRuntimeManifestDigest)
	if err != nil {
		return corecontract.CommitCheckpointResponse{}, runAttemptConversionError("CommitCheckpointAndTerminalRun", "checkpoint", request.Checkpoint.CheckpointID, fmt.Errorf("runtime manifest digest: %w", err))
	}
	result, err := commands.Store.CommitCheckpointAndTerminalRun(ctx, coredb.CommitCheckpointAndTerminalRunCommand{
		RunID: request.RunID, AttemptID: request.RunAttemptID, HolderID: request.HolderID,
		Generation: request.RunAttemptGeneration, ExpectedRunVersion: request.ExpectedRunVersion,
		ExpectedAttemptVersion: request.ExpectedRunAttemptVersion,
		CheckpointID:           request.Checkpoint.CheckpointID, BrainToolCatalogID: request.Checkpoint.BrainToolCatalogID,
		ThreadID: request.Checkpoint.ThreadID, TurnID: request.Checkpoint.TurnID,
		ManifestDigest: manifestDigest, CatalogDigest: catalogDigest,
		Object: coredb.ObjectPointer{
			ObjectID: request.Checkpoint.Object.ObjectID, SHA256: objectDigest,
			Size: request.Checkpoint.Object.Size, MediaType: request.Checkpoint.Object.MediaType,
		},
		CodexRuntimeManifestDigest: runtimeDigest,
		CheckpointAllowlistVersion: request.Checkpoint.CheckpointAllowlistVersion,
		Record:                     databaseTransitionRecord(request.Record),
	})
	if err != nil {
		return corecontract.CommitCheckpointResponse{}, err
	}
	return corecontract.CommitCheckpointResponse{
		Run: contractRun(result.Run), RunAttempt: contractRunAttempt(result.Attempt),
		Checkpoint: contractCheckpoint(result.Checkpoint), SessionVersion: result.SessionVersion, Created: result.Created,
	}, nil
}

func (commands StateStoreRunAttemptCommands) AppendAttemptEvents(ctx context.Context, request corecontract.AppendAttemptEventsRequest) (corecontract.AppendAttemptEventsResponse, error) {
	if commands.Store == nil {
		return corecontract.AppendAttemptEventsResponse{}, errors.New("nil core state store")
	}
	events := make([]coredb.AttemptEvent, len(request.Events))
	for index, event := range request.Events {
		converted, err := databaseAttemptEvent(event)
		if err != nil {
			return corecontract.AppendAttemptEventsResponse{}, runAttemptConversionError("AppendAttemptEvents", "event", event.EventID, fmt.Errorf("events[%d]: %w", index, err))
		}
		events[index] = converted
	}
	result, err := commands.Store.AppendAttemptEvents(ctx, coredb.AppendAttemptEventsCommand{
		RunID:      request.RunID,
		AttemptID:  request.RunAttemptID,
		HolderID:   request.HolderID,
		Generation: request.RunAttemptGeneration,
		OutboxID:   request.OutboxID,
		Events:     events,
	})
	if err != nil {
		return corecontract.AppendAttemptEventsResponse{}, err
	}
	appended := make([]corecontract.AppendedAttemptEvent, len(result.Events))
	for index, event := range result.Events {
		appended[index] = corecontract.AppendedAttemptEvent{
			EventID:            event.EventID,
			ProducerInstanceID: event.ProducerInstanceID,
			ProducerSeq:        event.ProducerSeq,
			RunSeq:             event.RunSeq,
			Duplicate:          event.Duplicate,
		}
	}
	return corecontract.AppendAttemptEventsResponse{Events: appended, NewCount: result.NewCount}, nil
}

func databaseAttemptEvent(event corecontract.AttemptEvent) (coredb.AttemptEvent, error) {
	converted := coredb.AttemptEvent{
		EventID:            event.EventID,
		ProducerInstanceID: event.ProducerInstanceID,
		ProducerSeq:        event.ProducerSeq,
		Source:             event.Source,
		Kind:               event.Kind,
		SchemaVersion:      event.SchemaVersion,
		Payload:            append([]byte(nil), event.Payload...),
	}
	if event.Object == nil {
		return converted, nil
	}
	digest, err := decodeCanonicalSHA256(event.Object.SHA256)
	if err != nil {
		return coredb.AttemptEvent{}, fmt.Errorf("object sha256: %w", err)
	}
	converted.Object = &coredb.ObjectPointer{
		ObjectID:  event.Object.ObjectID,
		SHA256:    digest,
		Size:      event.Object.Size,
		MediaType: event.Object.MediaType,
	}
	return converted, nil
}

func decodeCanonicalSHA256(value string) ([32]byte, error) {
	var digest [32]byte
	if len(value) != hex.EncodedLen(len(digest)) {
		return digest, errors.New("must contain 64 lowercase hexadecimal characters")
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || hex.EncodeToString(decoded) != value {
		return digest, errors.New("must contain 64 lowercase hexadecimal characters")
	}
	copy(digest[:], decoded)
	return digest, nil
}

func runAttemptLeaseTTL(milliseconds int64) (time.Duration, error) {
	if milliseconds < 1 || milliseconds > coredb.MaxLeaseTTL.Milliseconds() {
		return 0, fmt.Errorf("leaseTtlMs must be between 1 and %d", coredb.MaxLeaseTTL.Milliseconds())
	}
	return time.Duration(milliseconds) * time.Millisecond, nil
}

func contractRun(run coredb.Run) corecontract.RunState {
	return corecontract.RunState{
		RunID:                    run.ID,
		WorkspaceID:              run.WorkspaceID,
		SessionID:                run.SessionID,
		ActorID:                  run.ActorID,
		Status:                   run.Status,
		CurrentAttemptGeneration: run.CurrentAttemptGeneration,
		NextEventSeq:             run.NextEventSeq,
		Version:                  run.Version,
		CreatedAt:                run.CreatedAt,
		UpdatedAt:                run.UpdatedAt,
	}
}

func contractRunAttempt(attempt coredb.RunAttempt) corecontract.RunAttemptState {
	return corecontract.RunAttemptState{
		RunAttemptID: attempt.ID, RunID: attempt.RunID, Generation: attempt.Generation,
		Status: attempt.Status, TurnStartedAt: attempt.TurnStartedAt,
		TerminalThreadID: attempt.TerminalThreadID, TerminalTurnID: attempt.TerminalTurnID,
		HolderID: attempt.HolderID, Version: attempt.Version,
		CreatedAt: attempt.CreatedAt, UpdatedAt: attempt.UpdatedAt,
	}
}

func contractCheckpoint(checkpoint coredb.Checkpoint) corecontract.CheckpointState {
	return corecontract.CheckpointState{
		CheckpointID: checkpoint.ID, WorkspaceID: checkpoint.WorkspaceID, SessionID: checkpoint.SessionID,
		RunID: checkpoint.RunID, RunAttemptID: checkpoint.AttemptID,
		RunAttemptGeneration: checkpoint.AttemptGeneration, BrainToolCatalogID: checkpoint.BrainToolCatalogID,
		ThreadID: checkpoint.ThreadID, TurnID: checkpoint.TurnID,
		ManifestDigest: hex.EncodeToString(checkpoint.ManifestDigest[:]),
		CatalogDigest:  hex.EncodeToString(checkpoint.CatalogDigest[:]),
		Object: corecontract.EventObjectPointer{
			ObjectID: checkpoint.Object.ObjectID, SHA256: hex.EncodeToString(checkpoint.Object.SHA256[:]),
			Size: checkpoint.Object.Size, MediaType: checkpoint.Object.MediaType,
		},
		CodexRuntimeManifestDigest: hex.EncodeToString(checkpoint.CodexRuntimeManifestDigest[:]),
		CheckpointAllowlistVersion: checkpoint.CheckpointAllowlistVersion, CreatedAt: checkpoint.CreatedAt,
	}
}

func contractLease(lease coredb.Lease) corecontract.LeaseState {
	return corecontract.LeaseState{
		HolderID:   lease.HolderID,
		Generation: lease.Generation,
		ExpiresAt:  lease.ExpiresAt,
		AcquiredAt: lease.AcquiredAt,
		RenewedAt:  lease.RenewedAt,
	}
}

func runAttemptConversionError(operation, resource, resourceID string, err error) error {
	return &coredb.StateError{
		Code:       coredb.ErrorInvalidArgument,
		Operation:  operation,
		Resource:   resource,
		ResourceID: resourceID,
		Message:    fmt.Sprintf("invalid internal command: %v", err),
	}
}
