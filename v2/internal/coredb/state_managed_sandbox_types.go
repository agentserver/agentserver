package coredb

import "time"

const (
	ManagedSandboxDesiredReady   = "ready"
	ManagedSandboxDesiredDeleted = "deleted"

	ManagedSandboxReserved = "reserved"
	ManagedSandboxCreating = "creating"
	ManagedSandboxReady    = "ready"
	ManagedSandboxDeleting = "deleting"
	ManagedSandboxDeleted  = "deleted"
	ManagedSandboxFailed   = "failed"
	ManagedSandboxUnknown  = "unknown"

	ManagedSandboxActionRunCommand    = "run_command"
	ManagedSandboxActionSignalCommand = "signal_command"
	ManagedSandboxActionReadFile      = "read_file"

	MaxManagedSandboxTTL      = 24 * time.Hour
	MaxManagedActivityTTL     = time.Hour
	MinManagedSandboxTTL      = 30 * time.Second
	ManagedSandboxIdleDefault = 10 * time.Minute
)

type ManagedSandbox struct {
	ID                   string
	WorkspaceID          string
	SessionID            string
	EnvironmentID        string
	ProviderKind         string
	Generation           int64
	DesiredState         string
	ObservedState        string
	ProviderRegion       string
	ProviderPSM          string
	ProviderSessionRef   string
	CreateIdempotencyKey string
	RequestedTTL         time.Duration
	IdleTTL              time.Duration
	ExpiresAt            *time.Time
	IdleExpiresAt        *time.Time
	LastObservedAt       *time.Time
	Version              int64
	CreatedAt            time.Time
	UpdatedAt            time.Time
	DeletedAt            *time.Time
	LastErrorCode        string
	LastErrorDigest      *[32]byte
}

func (sandbox ManagedSandbox) Target() DispatchTarget {
	return DispatchTarget{Kind: DispatchTargetTAE, ID: sandbox.ID, Generation: sandbox.Generation}
}

type ReserveManagedSandboxCommand struct {
	SandboxID            string
	WorkspaceID          string
	SessionID            string
	EnvironmentID        string
	ProviderRegion       string
	ProviderPSM          string
	ProviderSessionRef   string
	CreateIdempotencyKey string
	RequestedTTL         time.Duration
	RequestedIdleTTL     time.Duration
}

type ReserveManagedSandboxResult struct {
	Sandbox ManagedSandbox
	Created bool
}

type BeginManagedSandboxCreateCommand struct {
	SandboxID       string
	Generation      int64
	ExpectedVersion int64
}

type ObserveManagedSandboxCommand struct {
	SandboxID          string
	Generation         int64
	ExpectedVersion    int64
	ObservedState      string
	ProviderSessionRef string
	ExpiresAt          *time.Time
	ErrorCode          string
	ErrorDigest        *[32]byte
}

type RenewManagedSandboxActivityCommand struct {
	SandboxID         string
	Generation        int64
	RunID             string
	AttemptID         string
	AttemptGeneration int64
	HolderID          string
	ActivityTTL       time.Duration
}

type ReleaseManagedSandboxActivityCommand struct {
	SandboxID         string
	Generation        int64
	RunID             string
	AttemptID         string
	AttemptGeneration int64
	HolderID          string
	IdleTTL           time.Duration
}

type BeginManagedSandboxDeleteCommand struct {
	SandboxID       string
	Generation      int64
	ExpectedVersion int64
	Reason          string
}

type ListManagedSandboxesForReconcileQuery struct {
	Limit int
}

type AuthorizeManagedSandboxOperationQuery struct {
	WorkspaceID       string
	SessionID         string
	RunID             string
	AttemptID         string
	AttemptGeneration int64
	ExecutionID       string
	OperationID       string
	MutationKey       string
	SandboxID         string
	TargetGeneration  int64
	EnvironmentID     string
	Action            string
}

type AuthorizedManagedSandboxOperation struct {
	SandboxID        string
	TargetGeneration int64
	OperationID      string
	OperationKind    string
	AuthorizedAt     time.Time
}
