package coreserver

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
)

const defaultRunDispatchPollInterval = 250 * time.Millisecond

type RunDispatchStateStore interface {
	ClaimRunDispatches(context.Context, coredb.ClaimRunDispatchesCommand) ([]coredb.RunDispatch, error)
	CompleteRunDispatch(context.Context, coredb.CompleteRunDispatchCommand) (bool, error)
	ReleaseRunDispatch(context.Context, coredb.ReleaseRunDispatchCommand) (bool, error)
}

type StateStoreRunDispatchCommands struct {
	Store        RunDispatchStateStore
	PollInterval time.Duration
}

var _ RunDispatchCommands = StateStoreRunDispatchCommands{}

func (commands StateStoreRunDispatchCommands) ClaimRunDispatches(ctx context.Context, request corecontract.ClaimRunDispatchesRequest) (corecontract.ClaimRunDispatchesResponse, error) {
	if commands.Store == nil {
		return corecontract.ClaimRunDispatchesResponse{}, errors.New("nil core state store")
	}
	lockTTL, err := runAttemptLeaseTTL(request.LockTTLMillis)
	if err != nil {
		return corecontract.ClaimRunDispatchesResponse{}, runDispatchConversionError("ClaimRunDispatches", "", err)
	}
	waitTimeout, err := runDispatchWaitTimeout(request.WaitTimeoutMillis)
	if err != nil {
		return corecontract.ClaimRunDispatchesResponse{}, runDispatchConversionError("ClaimRunDispatches", "", err)
	}
	command := coredb.ClaimRunDispatchesCommand{Owner: request.OwnerID, Limit: request.Limit, LockTTL: lockTTL}
	pollInterval := commands.PollInterval
	if pollInterval <= 0 {
		pollInterval = defaultRunDispatchPollInterval
	}
	deadline := time.Now().Add(waitTimeout)
	for {
		dispatches, err := commands.Store.ClaimRunDispatches(ctx, command)
		if err != nil {
			return corecontract.ClaimRunDispatchesResponse{}, err
		}
		if len(dispatches) > 0 || waitTimeout == 0 {
			return contractRunDispatches(dispatches), nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return corecontract.ClaimRunDispatchesResponse{RunDispatches: []corecontract.RunDispatch{}}, nil
		}
		if remaining < pollInterval {
			pollInterval = remaining
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return corecontract.ClaimRunDispatchesResponse{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func (commands StateStoreRunDispatchCommands) CompleteRunDispatch(ctx context.Context, dispatchID string, request corecontract.CompleteRunDispatchRequest) (corecontract.CompleteRunDispatchResponse, error) {
	if commands.Store == nil {
		return corecontract.CompleteRunDispatchResponse{}, errors.New("nil core state store")
	}
	completed, err := commands.Store.CompleteRunDispatch(ctx, coredb.CompleteRunDispatchCommand{
		ID: dispatchID, RunID: request.RunID, Owner: request.OwnerID, ClaimGeneration: request.ClaimGeneration,
	})
	if err != nil {
		return corecontract.CompleteRunDispatchResponse{}, err
	}
	return corecontract.CompleteRunDispatchResponse{Completed: completed}, nil
}

func (commands StateStoreRunDispatchCommands) ReleaseRunDispatch(ctx context.Context, dispatchID string, request corecontract.ReleaseRunDispatchRequest) (corecontract.ReleaseRunDispatchResponse, error) {
	if commands.Store == nil {
		return corecontract.ReleaseRunDispatchResponse{}, errors.New("nil core state store")
	}
	retryAfter, err := runDispatchRetryAfter(request.RetryAfterMillis)
	if err != nil {
		return corecontract.ReleaseRunDispatchResponse{}, runDispatchConversionError("ReleaseRunDispatch", dispatchID, err)
	}
	released, err := commands.Store.ReleaseRunDispatch(ctx, coredb.ReleaseRunDispatchCommand{
		ID: dispatchID, RunID: request.RunID, Owner: request.OwnerID, ClaimGeneration: request.ClaimGeneration, RetryAfter: retryAfter,
	})
	if err != nil {
		return corecontract.ReleaseRunDispatchResponse{}, err
	}
	return corecontract.ReleaseRunDispatchResponse{Released: released}, nil
}

func contractRunDispatches(source []coredb.RunDispatch) corecontract.ClaimRunDispatchesResponse {
	dispatches := make([]corecontract.RunDispatch, len(source))
	for index, dispatch := range source {
		dispatches[index] = corecontract.RunDispatch{
			RunDispatchID: dispatch.ID, WorkspaceID: dispatch.WorkspaceID, SessionID: dispatch.SessionID, RunID: dispatch.RunID,
			EnqueuedRunVersion: dispatch.EnqueuedRunVersion, CurrentRunVersion: dispatch.CurrentRunVersion, CurrentRunStatus: dispatch.CurrentRunStatus,
			ClaimOwnerID: dispatch.ClaimOwner, ClaimGeneration: dispatch.ClaimGeneration, AvailableAt: dispatch.AvailableAt,
			LockExpiresAt: dispatch.LockUntil, CreatedAt: dispatch.CreatedAt,
		}
	}
	return corecontract.ClaimRunDispatchesResponse{RunDispatches: dispatches}
}

func runDispatchWaitTimeout(milliseconds int64) (time.Duration, error) {
	if milliseconds < 0 || milliseconds > corecontract.MaxRunDispatchClaimWait.Milliseconds() {
		return 0, fmt.Errorf("waitTimeoutMs must be between 0 and %d", corecontract.MaxRunDispatchClaimWait.Milliseconds())
	}
	return time.Duration(milliseconds) * time.Millisecond, nil
}

func runDispatchRetryAfter(milliseconds int64) (time.Duration, error) {
	if milliseconds < 0 || milliseconds > coredb.MaxOutboxRetryDelay.Milliseconds() {
		return 0, fmt.Errorf("retryAfterMs must be between 0 and %d", coredb.MaxOutboxRetryDelay.Milliseconds())
	}
	return time.Duration(milliseconds) * time.Millisecond, nil
}

func runDispatchConversionError(operation, resourceID string, err error) error {
	return &coredb.StateError{
		Code:       coredb.ErrorInvalidArgument,
		Operation:  operation,
		Resource:   "run_dispatch",
		ResourceID: resourceID,
		Message:    fmt.Sprintf("invalid internal command: %v", err),
	}
}
