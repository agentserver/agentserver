package corecontract

import "time"

const (
	ReserveManagedSandboxPath            = "/internal/v2/managed-sandboxes:reserve"
	ListManagedSandboxesForReconcilePath = "/internal/v2/managed-sandboxes:reconcile"
	AuthorizeManagedSandboxOperationPath = "/internal/v2/managed-sandbox-operations:authorize"
	ManagedSandboxPathPrefix             = "/internal/v2/managed-sandboxes/"
	ManagedSandboxRoutePattern           = "/internal/v2/managed-sandboxes/{sandboxId}"
	ManagedSandboxActionRoutePattern     = "/internal/v2/managed-sandboxes/{sandboxId}:{action}"
)

const (
	ManagedSandboxActionRunCommand    = "run_command"
	ManagedSandboxActionSignalCommand = "signal_command"
	ManagedSandboxActionReadFile      = "read_file"
)

type ManagedSandboxState struct {
	SandboxID            string     `json:"sandboxId"`
	WorkspaceID          string     `json:"workspaceId"`
	SessionID            string     `json:"sessionId"`
	EnvironmentID        string     `json:"environmentId"`
	ProviderKind         string     `json:"providerKind"`
	Generation           int64      `json:"generation"`
	DesiredState         string     `json:"desiredState"`
	ObservedState        string     `json:"observedState"`
	ProviderRegion       string     `json:"providerRegion"`
	ProviderPSM          string     `json:"providerPsm"`
	ProviderSessionRef   string     `json:"providerSessionRef,omitempty"`
	CreateIdempotencyKey string     `json:"createIdempotencyKey"`
	RequestedTTLSeconds  int64      `json:"requestedTtlSeconds"`
	IdleTTLSeconds       int64      `json:"idleTtlSeconds"`
	ExpiresAt            *time.Time `json:"expiresAt,omitempty"`
	IdleExpiresAt        *time.Time `json:"idleExpiresAt,omitempty"`
	LastObservedAt       *time.Time `json:"lastObservedAt,omitempty"`
	Version              int64      `json:"version"`
	CreatedAt            time.Time  `json:"createdAt"`
	UpdatedAt            time.Time  `json:"updatedAt"`
	DeletedAt            *time.Time `json:"deletedAt,omitempty"`
	LastErrorCode        string     `json:"lastErrorCode,omitempty"`
	LastErrorSHA256      string     `json:"lastErrorSha256,omitempty"`
}

type ReserveManagedSandboxRequest struct {
	SandboxID            string `json:"sandboxId"`
	WorkspaceID          string `json:"workspaceId"`
	SessionID            string `json:"sessionId"`
	EnvironmentID        string `json:"environmentId"`
	ProviderRegion       string `json:"providerRegion"`
	ProviderPSM          string `json:"providerPsm"`
	ProviderSessionRef   string `json:"providerSessionRef"`
	CreateIdempotencyKey string `json:"createIdempotencyKey"`
	RequestedTTLSeconds  int64  `json:"requestedTtlSeconds"`
	IdleTTLSeconds       int64  `json:"idleTtlSeconds"`
}

type ReserveManagedSandboxResponse struct {
	Sandbox ManagedSandboxState `json:"sandbox"`
	Created bool                `json:"created"`
}

type GetManagedSandboxResponse struct {
	Sandbox ManagedSandboxState `json:"sandbox"`
}

type BeginManagedSandboxCreateRequest struct {
	SandboxID       string `json:"sandboxId"`
	Generation      int64  `json:"generation"`
	ExpectedVersion int64  `json:"expectedVersion"`
}

type ObserveManagedSandboxRequest struct {
	SandboxID          string     `json:"sandboxId"`
	Generation         int64      `json:"generation"`
	ExpectedVersion    int64      `json:"expectedVersion"`
	ObservedState      string     `json:"observedState"`
	ProviderSessionRef string     `json:"providerSessionRef,omitempty"`
	ExpiresAt          *time.Time `json:"expiresAt,omitempty"`
	ErrorCode          string     `json:"errorCode,omitempty"`
	ErrorSHA256        string     `json:"errorSha256,omitempty"`
}

type RenewManagedSandboxActivityRequest struct {
	SandboxID            string `json:"sandboxId"`
	Generation           int64  `json:"generation"`
	RunID                string `json:"runId"`
	RunAttemptID         string `json:"runAttemptId"`
	RunAttemptGeneration int64  `json:"runAttemptGeneration"`
	HolderID             string `json:"holderId"`
	ActivityTTLMillis    int64  `json:"activityTtlMillis"`
}

type ReleaseManagedSandboxActivityRequest struct {
	SandboxID            string `json:"sandboxId"`
	Generation           int64  `json:"generation"`
	RunID                string `json:"runId"`
	RunAttemptID         string `json:"runAttemptId"`
	RunAttemptGeneration int64  `json:"runAttemptGeneration"`
	HolderID             string `json:"holderId"`
	IdleTTLMillis        int64  `json:"idleTtlMillis"`
}

type BeginManagedSandboxDeleteRequest struct {
	SandboxID       string `json:"sandboxId"`
	Generation      int64  `json:"generation"`
	ExpectedVersion int64  `json:"expectedVersion"`
	Reason          string `json:"reason"`
}

type ManagedSandboxMutationResponse struct {
	Sandbox ManagedSandboxState `json:"sandbox"`
	Changed bool                `json:"changed"`
}

type ListManagedSandboxesForReconcileRequest struct {
	Limit int `json:"limit"`
}

type ListManagedSandboxesForReconcileResponse struct {
	Sandboxes []ManagedSandboxState `json:"sandboxes"`
}

type AuthorizeManagedSandboxOperationRequest struct {
	WorkspaceID          string `json:"workspaceId"`
	SessionID            string `json:"sessionId"`
	RunID                string `json:"runId"`
	RunAttemptID         string `json:"runAttemptId"`
	RunAttemptGeneration int64  `json:"runAttemptGeneration"`
	ExecutionID          string `json:"executionId"`
	OperationID          string `json:"operationId"`
	MutationKey          string `json:"mutationKey"`
	SandboxID            string `json:"sandboxId"`
	TargetGeneration     int64  `json:"targetGeneration"`
	EnvironmentID        string `json:"environmentId"`
	Action               string `json:"action"`
}

type AuthorizeManagedSandboxOperationResponse struct {
	SandboxID        string    `json:"sandboxId"`
	TargetGeneration int64     `json:"targetGeneration"`
	OperationID      string    `json:"operationId"`
	OperationKind    string    `json:"operationKind"`
	AuthorizedAt     time.Time `json:"authorizedAt"`
}

func ManagedSandboxPath(sandboxID string) string {
	return ManagedSandboxPathPrefix + sandboxID
}

func BeginManagedSandboxCreatePath(sandboxID string) string {
	return ManagedSandboxPath(sandboxID) + ":begin-create"
}

func ObserveManagedSandboxPath(sandboxID string) string {
	return ManagedSandboxPath(sandboxID) + ":observe"
}

func RenewManagedSandboxActivityPath(sandboxID string) string {
	return ManagedSandboxPath(sandboxID) + ":renew-activity"
}

func ReleaseManagedSandboxActivityPath(sandboxID string) string {
	return ManagedSandboxPath(sandboxID) + ":release-activity"
}

func BeginManagedSandboxDeletePath(sandboxID string) string {
	return ManagedSandboxPath(sandboxID) + ":begin-delete"
}
