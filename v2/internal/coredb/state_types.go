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
	ID               string
	RunID            string
	Generation       int64
	Status           string
	TurnStartedAt    *time.Time
	TerminalThreadID string
	TerminalTurnID   string
	HolderID         string
	Version          int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
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
	LLMGateway             RunLLMGatewayBinding
	LarkEgress             RunLarkEgressBinding
	ManagedSandbox         RunManagedSandboxBinding
	ExpectedSessionVersion int64
	Record                 TransitionRecord
}

// RunManagedSandboxBinding is the regional managed execution target selected
// from the workspace setting for one run. An all-zero value means that the
// deployment does not expose managed execution (for example insecure dev).
type RunManagedSandboxBinding struct {
	SettingVersion int64
	Region         string
	EnvironmentID  string
}

type CreateRunResult struct {
	Run            Run
	SessionVersion int64
	Created        bool
}

type CancelRunCommand struct {
	WorkspaceID string
	RunID       string
	ActorID     string
	Record      TransitionRecord
}

type CancelRunResult struct {
	Run            Run
	SessionVersion int64
	Changed        bool
}

// AuthorizedSession is the minimum user-facing scope projection needed before
// preparing immutable run inputs. CreateAuthorizedRun rechecks this membership
// in the write transaction; callers must not treat this preliminary read as
// lasting authorization.
type AuthorizedSession struct {
	WorkspaceID    string
	SessionID      string
	ActorID        string
	Role           string
	SessionVersion int64
}

type RunEvent struct {
	EventID              string
	Seq                  int64
	RunAttemptID         *string
	RunAttemptGeneration *int64
	ProducerInstanceID   string
	ProducerSeq          int64
	Source               string
	Kind                 string
	SchemaVersion        int
	Payload              json.RawMessage
	Object               *ObjectPointer
	CreatedAt            time.Time
}

type ReadAuthorizedRunEventsCommand struct {
	WorkspaceID string
	ActorID     string
	RunID       string
	AfterSeq    int64
	Limit       int
}

type ReadAuthorizedRunEventsResult struct {
	Run              Run
	Events           []RunEvent
	EarliestSequence int64
	LastSequence     int64
	Rebase           *RunEventRebase
}

type RunEventRebase struct {
	AfterSequence int64
	RunStatus     string
	RunVersion    int64
	RunUpdatedAt  time.Time
	Snapshot      json.RawMessage
	CreatedAt     time.Time
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

type BeginRunFinalizationCommand struct {
	RunID                  string
	AttemptID              string
	HolderID               string
	Generation             int64
	ExpectedRunVersion     int64
	ExpectedAttemptVersion int64
	ThreadID               string
	TurnID                 string
	Record                 TransitionRecord
}

type BeginRunFinalizationResult struct {
	Run     Run
	Attempt RunAttempt
	Changed bool
}

type CommitCheckpointAndTerminalRunCommand struct {
	RunID                      string
	AttemptID                  string
	HolderID                   string
	Generation                 int64
	ExpectedRunVersion         int64
	ExpectedAttemptVersion     int64
	CheckpointID               string
	BrainToolCatalogID         string
	ThreadID                   string
	TurnID                     string
	ManifestDigest             [32]byte
	CatalogDigest              [32]byte
	Object                     ObjectPointer
	CodexRuntimeManifestDigest [32]byte
	CheckpointAllowlistVersion int64
	Record                     TransitionRecord
}

type CommitCheckpointAndTerminalRunResult struct {
	Run            Run
	Attempt        RunAttempt
	Checkpoint     Checkpoint
	SessionVersion int64
	Created        bool
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
	Run          Run
	Attempt      RunAttempt
	SessionLease Lease
	AttemptLease Lease
}

type InterruptAttemptCommand struct {
	RunID                  string
	AttemptID              string
	HolderID               string
	Generation             int64
	ExpectedRunVersion     int64
	ExpectedAttemptVersion int64
	Reason                 string
	Record                 TransitionRecord
}

type InterruptAttemptResult struct {
	Run            Run
	Attempt        RunAttempt
	SessionVersion int64
	Changed        bool
}

// CommitAttemptTerminalCommand is the exact holder's post-cleanup handoff for
// an accepted stock turn that ended failed or interrupted. Completed turns use
// the separate checkpoint finalization transaction.
type CommitAttemptTerminalCommand struct {
	RunID          string
	AttemptID      string
	HolderID       string
	Generation     int64
	TerminalStatus string
	ThreadID       string
	TurnID         string
	Code           string
	Message        string
	Record         TransitionRecord
}

type CommitAttemptTerminalResult struct {
	Run            Run
	Attempt        RunAttempt
	SessionVersion int64
	Disposition    string
	Changed        bool
}

// AbandonAttemptCommand is the trusted pre-turn workload-stopped handoff from
// the exact harness holder. Unlike a lease expiry, it proves that this holder
// has finished local cleanup, so core can either requeue the run or close a
// cancellation that raced with cleanup in the same transaction.
type AbandonAttemptCommand struct {
	RunID      string
	AttemptID  string
	HolderID   string
	Generation int64
	Reason     string
	Terminal   bool
	Record     TransitionRecord
}

type AbandonAttemptResult struct {
	Run            Run
	Attempt        RunAttempt
	SessionVersion int64
	Disposition    string
	Changed        bool
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
	LLMGateway         RunLLMGatewayBinding
	LarkEgress         RunLarkEgressBinding
	ManagedSandbox     RunManagedSandboxBinding
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
