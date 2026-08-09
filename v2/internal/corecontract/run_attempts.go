package corecontract

import (
	"encoding/json"
	"time"
)

const (
	ClaimRunAttemptPath  = "/internal/v2/run-attempts:claim"
	RunAttemptPathPrefix = "/internal/v2/run-attempts/"
)

// RunState is the bounded scheduler projection returned to harness-pool. It
// deliberately omits request hashes and idempotency keys, which are not part
// of the worker launch authority.
type RunState struct {
	RunID                    string    `json:"runId"`
	WorkspaceID              string    `json:"workspaceId"`
	SessionID                string    `json:"sessionId"`
	ActorID                  string    `json:"actorId"`
	Status                   string    `json:"status"`
	CurrentAttemptGeneration int64     `json:"currentAttemptGeneration"`
	NextEventSeq             int64     `json:"nextEventSeq"`
	Version                  int64     `json:"version"`
	CreatedAt                time.Time `json:"createdAt"`
	UpdatedAt                time.Time `json:"updatedAt"`
}

type RunAttemptState struct {
	RunAttemptID     string     `json:"runAttemptId"`
	RunID            string     `json:"runId"`
	Generation       int64      `json:"generation"`
	Status           string     `json:"status"`
	TurnStartedAt    *time.Time `json:"turnStartedAt,omitempty"`
	TerminalThreadID string     `json:"terminalThreadId,omitempty"`
	TerminalTurnID   string     `json:"terminalTurnId,omitempty"`
	HolderID         string     `json:"holderId"`
	Version          int64      `json:"version"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
}

type LeaseState struct {
	HolderID   string    `json:"holderId"`
	Generation int64     `json:"generation"`
	ExpiresAt  time.Time `json:"expiresAt"`
	AcquiredAt time.Time `json:"acquiredAt"`
	RenewedAt  time.Time `json:"renewedAt"`
}

type ClaimRunAttemptRequest struct {
	RunID              string           `json:"runId"`
	RunAttemptID       string           `json:"runAttemptId"`
	HolderID           string           `json:"holderId"`
	ExpectedRunVersion int64            `json:"expectedRunVersion"`
	LeaseTTLMillis     int64            `json:"leaseTtlMs"`
	Record             TransitionRecord `json:"record"`
}

type ClaimRunAttemptResponse struct {
	Run          RunState        `json:"run"`
	RunAttempt   RunAttemptState `json:"runAttempt"`
	SessionLease LeaseState      `json:"sessionLease"`
	AttemptLease LeaseState      `json:"attemptLease"`
	Created      bool            `json:"created"`
	Reclaimed    bool            `json:"reclaimed"`
}

type RenewRunAttemptRequest struct {
	SessionID            string `json:"sessionId"`
	RunID                string `json:"runId"`
	RunAttemptID         string `json:"runAttemptId"`
	HolderID             string `json:"holderId"`
	RunAttemptGeneration int64  `json:"runAttemptGeneration"`
	LeaseTTLMillis       int64  `json:"leaseTtlMs"`
}

type RenewRunAttemptResponse struct {
	Run          RunState        `json:"run"`
	RunAttempt   RunAttemptState `json:"runAttempt"`
	SessionLease LeaseState      `json:"sessionLease"`
	AttemptLease LeaseState      `json:"attemptLease"`
}

type InterruptRunAttemptRequest struct {
	RunID                     string           `json:"runId"`
	RunAttemptID              string           `json:"runAttemptId"`
	HolderID                  string           `json:"holderId"`
	RunAttemptGeneration      int64            `json:"runAttemptGeneration"`
	ExpectedRunVersion        int64            `json:"expectedRunVersion"`
	ExpectedRunAttemptVersion int64            `json:"expectedRunAttemptVersion"`
	Reason                    string           `json:"reason"`
	Record                    TransitionRecord `json:"record"`
}

type InterruptRunAttemptResponse struct {
	Run            RunState        `json:"run"`
	RunAttempt     RunAttemptState `json:"runAttempt"`
	SessionVersion int64           `json:"sessionVersion"`
	Changed        bool            `json:"changed"`
}

type CommitAttemptTerminalRequest struct {
	RunID                string           `json:"runId"`
	RunAttemptID         string           `json:"runAttemptId"`
	HolderID             string           `json:"holderId"`
	RunAttemptGeneration int64            `json:"runAttemptGeneration"`
	TerminalStatus       string           `json:"terminalStatus"`
	ThreadID             string           `json:"threadId"`
	TurnID               string           `json:"turnId"`
	Code                 string           `json:"code"`
	Message              string           `json:"message"`
	Record               TransitionRecord `json:"record"`
}

type CommitAttemptTerminalResponse struct {
	Run            RunState        `json:"run"`
	RunAttempt     RunAttemptState `json:"runAttempt"`
	SessionVersion int64           `json:"sessionVersion"`
	Disposition    string          `json:"disposition"`
	Changed        bool            `json:"changed"`
}

type AbandonRunAttemptRequest struct {
	RunID                string           `json:"runId"`
	RunAttemptID         string           `json:"runAttemptId"`
	HolderID             string           `json:"holderId"`
	RunAttemptGeneration int64            `json:"runAttemptGeneration"`
	Reason               string           `json:"reason"`
	Terminal             bool             `json:"terminal,omitempty"`
	Record               TransitionRecord `json:"record"`
}

type AbandonRunAttemptResponse struct {
	Run            RunState        `json:"run"`
	RunAttempt     RunAttemptState `json:"runAttempt"`
	SessionVersion int64           `json:"sessionVersion"`
	Disposition    string          `json:"disposition"`
	Changed        bool            `json:"changed"`
}

type MarkTurnAcceptedRequest struct {
	RunID                     string           `json:"runId"`
	RunAttemptID              string           `json:"runAttemptId"`
	HolderID                  string           `json:"holderId"`
	RunAttemptGeneration      int64            `json:"runAttemptGeneration"`
	ExpectedRunVersion        int64            `json:"expectedRunVersion"`
	ExpectedRunAttemptVersion int64            `json:"expectedRunAttemptVersion"`
	Record                    TransitionRecord `json:"record"`
}

type MarkTurnAcceptedResponse struct {
	Run        RunState        `json:"run"`
	RunAttempt RunAttemptState `json:"runAttempt"`
	Changed    bool            `json:"changed"`
}

type BeginRunFinalizationRequest struct {
	RunID                     string           `json:"runId"`
	RunAttemptID              string           `json:"runAttemptId"`
	HolderID                  string           `json:"holderId"`
	RunAttemptGeneration      int64            `json:"runAttemptGeneration"`
	ExpectedRunVersion        int64            `json:"expectedRunVersion"`
	ExpectedRunAttemptVersion int64            `json:"expectedRunAttemptVersion"`
	ThreadID                  string           `json:"threadId"`
	TurnID                    string           `json:"turnId"`
	Record                    TransitionRecord `json:"record"`
}

type BeginRunFinalizationResponse struct {
	Run        RunState        `json:"run"`
	RunAttempt RunAttemptState `json:"runAttempt"`
	Changed    bool            `json:"changed"`
}

type CheckpointCommit struct {
	CheckpointID               string             `json:"checkpointId"`
	BrainToolCatalogID         string             `json:"brainToolCatalogId"`
	ThreadID                   string             `json:"threadId"`
	TurnID                     string             `json:"turnId"`
	ManifestDigest             string             `json:"manifestDigest"`
	CatalogDigest              string             `json:"catalogDigest"`
	Object                     EventObjectPointer `json:"object"`
	CodexRuntimeManifestDigest string             `json:"codexRuntimeManifestDigest"`
	CheckpointAllowlistVersion int64              `json:"checkpointAllowlistVersion"`
	PackSetDigest              string             `json:"packSetDigest,omitempty"`
}

type CommitCheckpointRequest struct {
	RunID                     string           `json:"runId"`
	RunAttemptID              string           `json:"runAttemptId"`
	HolderID                  string           `json:"holderId"`
	RunAttemptGeneration      int64            `json:"runAttemptGeneration"`
	ExpectedRunVersion        int64            `json:"expectedRunVersion"`
	ExpectedRunAttemptVersion int64            `json:"expectedRunAttemptVersion"`
	Checkpoint                CheckpointCommit `json:"checkpoint"`
	Record                    TransitionRecord `json:"record"`
}

type CheckpointState struct {
	CheckpointID               string             `json:"checkpointId"`
	WorkspaceID                string             `json:"workspaceId"`
	SessionID                  string             `json:"sessionId"`
	RunID                      string             `json:"runId"`
	RunAttemptID               string             `json:"runAttemptId"`
	RunAttemptGeneration       int64              `json:"runAttemptGeneration"`
	BrainToolCatalogID         string             `json:"brainToolCatalogId"`
	ThreadID                   string             `json:"threadId"`
	TurnID                     string             `json:"turnId"`
	ManifestDigest             string             `json:"manifestDigest"`
	CatalogDigest              string             `json:"catalogDigest"`
	Object                     EventObjectPointer `json:"object"`
	CodexRuntimeManifestDigest string             `json:"codexRuntimeManifestDigest"`
	CheckpointAllowlistVersion int64              `json:"checkpointAllowlistVersion"`
	PackSetDigest              string             `json:"packSetDigest,omitempty"`
	CreatedAt                  time.Time          `json:"createdAt"`
}

type CommitCheckpointResponse struct {
	Run            RunState        `json:"run"`
	RunAttempt     RunAttemptState `json:"runAttempt"`
	Checkpoint     CheckpointState `json:"checkpoint"`
	SessionVersion int64           `json:"sessionVersion"`
	Created        bool            `json:"created"`
}

type EventObjectPointer struct {
	ObjectID  string `json:"objectId"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	MediaType string `json:"mediaType"`
}

type AttemptEvent struct {
	EventID            string              `json:"eventId"`
	ProducerInstanceID string              `json:"producerInstanceId"`
	ProducerSeq        int64               `json:"producerSeq"`
	Source             string              `json:"source"`
	Kind               string              `json:"kind"`
	SchemaVersion      int                 `json:"schemaVersion"`
	Payload            json.RawMessage     `json:"payload,omitempty"`
	Object             *EventObjectPointer `json:"object,omitempty"`
}

type AppendAttemptEventsRequest struct {
	RunID                string         `json:"runId"`
	RunAttemptID         string         `json:"runAttemptId"`
	HolderID             string         `json:"holderId"`
	RunAttemptGeneration int64          `json:"runAttemptGeneration"`
	OutboxID             string         `json:"outboxId"`
	Events               []AttemptEvent `json:"events"`
}

type AppendedAttemptEvent struct {
	EventID            string `json:"eventId"`
	ProducerInstanceID string `json:"producerInstanceId"`
	ProducerSeq        int64  `json:"producerSeq"`
	RunSeq             int64  `json:"runSeq"`
	Duplicate          bool   `json:"duplicate"`
}

type AppendAttemptEventsResponse struct {
	Events   []AppendedAttemptEvent `json:"events"`
	NewCount int                    `json:"newCount"`
}

func RenewRunAttemptPath(runAttemptID string) string {
	return RunAttemptPathPrefix + runAttemptID + ":renew"
}

func InterruptRunAttemptPath(runAttemptID string) string {
	return RunAttemptPathPrefix + runAttemptID + ":interrupt"
}

func CommitAttemptTerminalPath(runAttemptID string) string {
	return RunAttemptPathPrefix + runAttemptID + ":commitTerminal"
}

func AbandonRunAttemptPath(runAttemptID string) string {
	return RunAttemptPathPrefix + runAttemptID + ":abandon"
}

func MarkTurnAcceptedPath(runAttemptID string) string {
	return RunAttemptPathPrefix + runAttemptID + ":turnAccepted"
}

func BeginRunFinalizationPath(runAttemptID string) string {
	return RunAttemptPathPrefix + runAttemptID + ":beginFinalization"
}

func CommitCheckpointPath(runAttemptID string) string {
	return RunAttemptPathPrefix + runAttemptID + ":commitCheckpoint"
}

func AppendAttemptEventsPath(runAttemptID string) string {
	return RunAttemptPathPrefix + runAttemptID + "/events:append"
}
