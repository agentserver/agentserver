package harnesspool

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

type ClaimRunDispatchesRequest struct {
	OwnerID     string
	Limit       int
	LockTTL     time.Duration
	WaitTimeout time.Duration
}

type RunDispatch struct {
	RunDispatchID      string
	WorkspaceID        string
	SessionID          string
	RunID              string
	EnqueuedRunVersion int64
	CurrentRunVersion  int64
	CurrentRunStatus   string
	ClaimOwnerID       string
	ClaimGeneration    int
	AvailableAt        time.Time
	LockExpiresAt      time.Time
	CreatedAt          time.Time
}

type CompleteRunDispatchRequest struct {
	RunDispatchID   string
	RunID           string
	OwnerID         string
	ClaimGeneration int
}

type ReleaseRunDispatchRequest struct {
	RunDispatchID   string
	RunID           string
	OwnerID         string
	ClaimGeneration int
	RetryAfter      time.Duration
}

func (client *CoreClient) ClaimRunDispatches(ctx context.Context, request ClaimRunDispatchesRequest) ([]RunDispatch, error) {
	contractRequest := corecontract.ClaimRunDispatchesRequest{
		OwnerID: request.OwnerID, Limit: request.Limit, LockTTLMillis: request.LockTTL.Milliseconds(),
		WaitTimeoutMillis: request.WaitTimeout.Milliseconds(),
	}
	var response corecontract.ClaimRunDispatchesResponse
	if err := client.post(ctx, corecontract.ClaimRunDispatchesPath, contractRequest, &response); err != nil {
		return nil, err
	}
	if len(response.RunDispatches) > request.Limit {
		return nil, errors.New("core run-dispatch response exceeds the requested limit")
	}
	dispatches := make([]RunDispatch, len(response.RunDispatches))
	seenDispatches := make(map[string]struct{}, len(response.RunDispatches))
	seenRuns := make(map[string]struct{}, len(response.RunDispatches))
	for index, source := range response.RunDispatches {
		if source.RunDispatchID == "" || source.WorkspaceID == "" || source.SessionID == "" || source.RunID == "" ||
			source.ClaimOwnerID != request.OwnerID || source.ClaimGeneration < 1 || source.EnqueuedRunVersion < 1 ||
			source.CurrentRunVersion < source.EnqueuedRunVersion || !validDispatchRunStatus(source.CurrentRunStatus) ||
			source.AvailableAt.IsZero() || source.LockExpiresAt.IsZero() || source.CreatedAt.IsZero() {
			return nil, fmt.Errorf("core run-dispatch response item %d has an invalid authority tuple", index)
		}
		if _, exists := seenDispatches[source.RunDispatchID]; exists {
			return nil, errors.New("core run-dispatch response repeats a dispatch identity")
		}
		if _, exists := seenRuns[source.RunID]; exists {
			return nil, errors.New("core run-dispatch response repeats a run identity")
		}
		seenDispatches[source.RunDispatchID] = struct{}{}
		seenRuns[source.RunID] = struct{}{}
		dispatches[index] = RunDispatch{
			RunDispatchID: source.RunDispatchID, WorkspaceID: source.WorkspaceID, SessionID: source.SessionID, RunID: source.RunID,
			EnqueuedRunVersion: source.EnqueuedRunVersion, CurrentRunVersion: source.CurrentRunVersion, CurrentRunStatus: source.CurrentRunStatus,
			ClaimOwnerID: source.ClaimOwnerID, ClaimGeneration: source.ClaimGeneration, AvailableAt: source.AvailableAt,
			LockExpiresAt: source.LockExpiresAt, CreatedAt: source.CreatedAt,
		}
	}
	return dispatches, nil
}

func (client *CoreClient) CompleteRunDispatch(ctx context.Context, request CompleteRunDispatchRequest) (bool, error) {
	contractRequest := corecontract.CompleteRunDispatchRequest{
		RunID: request.RunID, OwnerID: request.OwnerID, ClaimGeneration: request.ClaimGeneration,
	}
	var response corecontract.CompleteRunDispatchResponse
	if err := client.post(ctx, corecontract.CompleteRunDispatchPath(request.RunDispatchID), contractRequest, &response); err != nil {
		return false, err
	}
	return response.Completed, nil
}

func (client *CoreClient) ReleaseRunDispatch(ctx context.Context, request ReleaseRunDispatchRequest) (bool, error) {
	contractRequest := corecontract.ReleaseRunDispatchRequest{
		RunID: request.RunID, OwnerID: request.OwnerID, ClaimGeneration: request.ClaimGeneration,
		RetryAfterMillis: request.RetryAfter.Milliseconds(),
	}
	var response corecontract.ReleaseRunDispatchResponse
	if err := client.post(ctx, corecontract.ReleaseRunDispatchPath(request.RunDispatchID), contractRequest, &response); err != nil {
		return false, err
	}
	return response.Released, nil
}

func validDispatchRunStatus(status string) bool {
	switch status {
	case "queued", "starting", "running", "finalizing", "completed", "failed", "interrupted", "cancelling", "cancelled":
		return true
	default:
		return false
	}
}
