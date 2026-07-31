package corecontract

import "time"

const (
	ClaimRunDispatchesPath  = "/internal/v2/run-dispatches:claim"
	RunDispatchPathPrefix   = "/internal/v2/run-dispatches/"
	MaxRunDispatchClaimWait = 30 * time.Second
)

// ClaimRunDispatchesRequest long-polls the run.queued delivery lane. The
// owner identifies one harness-pool process, not a reusable deployment name.
type ClaimRunDispatchesRequest struct {
	OwnerID           string `json:"ownerId"`
	Limit             int    `json:"limit"`
	LockTTLMillis     int64  `json:"lockTtlMs"`
	WaitTimeoutMillis int64  `json:"waitTimeoutMs"`
}

// RunDispatch is a generation-fenced claim on one run.queued outbox fact.
// EnqueuedRunVersion is immutable evidence from that fact; CurrentRunVersion
// and CurrentRunStatus are a projection read in the same claim transaction so
// a redelivery can safely resume pre-turn scheduling.
type RunDispatch struct {
	RunDispatchID      string    `json:"runDispatchId"`
	WorkspaceID        string    `json:"workspaceId"`
	SessionID          string    `json:"sessionId"`
	RunID              string    `json:"runId"`
	EnqueuedRunVersion int64     `json:"enqueuedRunVersion"`
	CurrentRunVersion  int64     `json:"currentRunVersion"`
	CurrentRunStatus   string    `json:"currentRunStatus"`
	ClaimOwnerID       string    `json:"claimOwnerId"`
	ClaimGeneration    int       `json:"claimGeneration"`
	AvailableAt        time.Time `json:"availableAt"`
	LockExpiresAt      time.Time `json:"lockExpiresAt"`
	CreatedAt          time.Time `json:"createdAt"`
}

type ClaimRunDispatchesResponse struct {
	RunDispatches []RunDispatch `json:"runDispatches"`
}

type CompleteRunDispatchRequest struct {
	RunID           string `json:"runId"`
	OwnerID         string `json:"ownerId"`
	ClaimGeneration int    `json:"claimGeneration"`
}

type CompleteRunDispatchResponse struct {
	Completed bool `json:"completed"`
}

type ReleaseRunDispatchRequest struct {
	RunID            string `json:"runId"`
	OwnerID          string `json:"ownerId"`
	ClaimGeneration  int    `json:"claimGeneration"`
	RetryAfterMillis int64  `json:"retryAfterMs"`
}

type ReleaseRunDispatchResponse struct {
	Released bool `json:"released"`
}

func CompleteRunDispatchPath(runDispatchID string) string {
	return RunDispatchPathPrefix + runDispatchID + ":complete"
}

func ReleaseRunDispatchPath(runDispatchID string) string {
	return RunDispatchPathPrefix + runDispatchID + ":release"
}
