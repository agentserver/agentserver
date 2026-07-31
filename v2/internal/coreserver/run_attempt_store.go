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
	MarkTurnAccepted(context.Context, coredb.MarkTurnAcceptedCommand) (coredb.MarkTurnAcceptedResult, error)
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
		SessionLease: contractLease(result.SessionLease),
		AttemptLease: contractLease(result.AttemptLease),
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
		RunAttemptID:  attempt.ID,
		RunID:         attempt.RunID,
		Generation:    attempt.Generation,
		Status:        attempt.Status,
		TurnStartedAt: attempt.TurnStartedAt,
		HolderID:      attempt.HolderID,
		Version:       attempt.Version,
		CreatedAt:     attempt.CreatedAt,
		UpdatedAt:     attempt.UpdatedAt,
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
