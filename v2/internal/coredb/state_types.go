package coredb

import (
	"encoding/json"
	"time"
)

const (
	RunStatusQueued      = "queued"
	RunStatusStarting    = "starting"
	RunStatusRunning     = "running"
	RunStatusFinalizing  = "finalizing"
	RunStatusCompleted   = "completed"
	RunStatusFailed      = "failed"
	RunStatusInterrupted = "interrupted"
	RunStatusCancelling  = "cancelling"
	RunStatusCancelled   = "cancelled"

	AttemptStatusCreated     = "created"
	AttemptStatusLeased      = "leased"
	AttemptStatusStarting    = "starting"
	AttemptStatusRunning     = "running"
	AttemptStatusFinalizing  = "finalizing"
	AttemptStatusSucceeded   = "succeeded"
	AttemptStatusFailed      = "failed"
	AttemptStatusInterrupted = "interrupted"
	AttemptStatusFenced      = "fenced"

	EventSourceBrain    = "brain"
	EventSourceExecutor = "executor"
	EventSourceSystem   = "system"
	EventSourceApproval = "approval"
)

// TransitionRecord supplies immutable identities for the canonical event and
// outbox row written atomically with a state transition.
type TransitionRecord struct {
	EventID            string
	ProducerInstanceID string
	ProducerSeq        int64
	OutboxID           string
}

type Run struct {
	ID                       string
	WorkspaceID              string
	SessionID                string
	ActorID                  string
	Status                   string
	RequestHash              [32]byte
	IdempotencyKey           string
	CurrentAttemptGeneration int64
	NextEventSeq             int64
	Version                  int64
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

type RunAttempt struct {
	ID            string
	RunID         string
	Generation    int64
	Status        string
	TurnStartedAt *time.Time
	HolderID      string
	Version       int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type Lease struct {
	HolderID   string
	Generation int64
	ExpiresAt  time.Time
	AcquiredAt time.Time
	RenewedAt  time.Time
}

type CreateRunCommand struct {
	RunID                  string
	WorkspaceID            string
	SessionID              string
	ActorID                string
	RequestHash            [32]byte
	IdempotencyKey         string
	Prompt                 ObjectPointer
	ExecutorPolicy         RunExecutorPolicy
	ExpectedSessionVersion int64
	Record                 TransitionRecord
}

type CreateRunResult struct {
	Run            Run
	SessionVersion int64
	Created        bool
}

type ClaimQueuedRunCommand struct {
	RunID              string
	AttemptID          string
	HolderID           string
	ExpectedRunVersion int64
	LeaseTTL           time.Duration
	Record             TransitionRecord
}

type ClaimQueuedRunResult struct {
	Run          Run
	Attempt      RunAttempt
	SessionLease Lease
	AttemptLease Lease
	Created      bool
	Reclaimed    bool
}

type MarkTurnAcceptedCommand struct {
	RunID                  string
	AttemptID              string
	HolderID               string
	Generation             int64
	ExpectedRunVersion     int64
	ExpectedAttemptVersion int64
	Record                 TransitionRecord
}

type MarkTurnAcceptedResult struct {
	Run     Run
	Attempt RunAttempt
	Changed bool
}

type RenewSessionLeaseCommand struct {
	SessionID  string
	RunID      string
	HolderID   string
	Generation int64
	LeaseTTL   time.Duration
}

type RenewAttemptLeaseCommand struct {
	RunID      string
	AttemptID  string
	HolderID   string
	Generation int64
	LeaseTTL   time.Duration
}

// RenewRunAttemptLeasesCommand renews the session and attempt lease as one
// transaction. A harness holder must never observe only one half as renewed.
type RenewRunAttemptLeasesCommand struct {
	SessionID  string
	RunID      string
	AttemptID  string
	HolderID   string
	Generation int64
	LeaseTTL   time.Duration
}

type RenewRunAttemptLeasesResult struct {
	SessionLease Lease
	AttemptLease Lease
}

type ObjectPointer struct {
	ObjectID  string
	SHA256    [32]byte
	Size      int64
	MediaType string
}

// RunExecutorPolicy is the trusted, immutable model-visible tool projection
// captured atomically with CreateRun. Runtime authorization still rechecks
// live RBAC for every tool call.
type RunExecutorPolicy struct {
	Version       string
	ContextDigest [32]byte
	AllowedTools  []string
}

type ResolveRunLaunchStateCommand struct {
	WorkspaceID            string
	SessionID              string
	RunID                  string
	AttemptID              string
	HolderID               string
	Generation             int64
	ExpectedRunVersion     int64
	ExpectedAttemptVersion int64
}

// Checkpoint is an immutable committed native-resume pointer. Object content
// and the checkpoint manifest remain outside PostgreSQL; core stores all
// hashes and compatibility metadata needed to authorize their use.
type Checkpoint struct {
	ID                         string
	WorkspaceID                string
	SessionID                  string
	RunID                      string
	AttemptID                  string
	AttemptGeneration          int64
	BrainToolCatalogID         string
	ThreadID                   string
	TurnID                     string
	ManifestDigest             [32]byte
	CatalogDigest              [32]byte
	Object                     ObjectPointer
	CodexRuntimeManifestDigest [32]byte
	CheckpointAllowlistVersion int64
	Catalog                    BrainToolCatalog
	CreatedAt                  time.Time
}

type ResolvedRunLaunchState struct {
	WorkspaceID        string
	SessionID          string
	RunID              string
	AttemptID          string
	HolderID           string
	Generation         int64
	RunVersion         int64
	AttemptVersion     int64
	Prompt             ObjectPointer
	PreviousCheckpoint *Checkpoint
	ExecutorPolicy     RunExecutorPolicy
}

type AttemptEvent struct {
	EventID            string
	ProducerInstanceID string
	ProducerSeq        int64
	Source             string
	Kind               string
	SchemaVersion      int
	Payload            json.RawMessage
	Object             *ObjectPointer
}

type AppendAttemptEventsCommand struct {
	RunID      string
	AttemptID  string
	HolderID   string
	Generation int64
	OutboxID   string
	Events     []AttemptEvent
}

type AppendedEvent struct {
	EventID            string
	ProducerInstanceID string
	ProducerSeq        int64
	RunSeq             int64
	Duplicate          bool
}

type AppendAttemptEventsResult struct {
	Events   []AppendedEvent
	NewCount int
}

type OutboxMessage struct {
	ID              string
	Kind            string
	AggregateID     string
	Payload         json.RawMessage
	AvailableAt     time.Time
	LockOwner       string
	LockUntil       time.Time
	ClaimGeneration int
	CreatedAt       time.Time
}

type ClaimOutboxCommand struct {
	Owner   string
	Limit   int
	LockTTL time.Duration
}

type CompleteOutboxCommand struct {
	ID              string
	Owner           string
	ClaimGeneration int
}

type ReleaseOutboxCommand struct {
	ID              string
	Owner           string
	ClaimGeneration int
	RetryAfter      time.Duration
}
