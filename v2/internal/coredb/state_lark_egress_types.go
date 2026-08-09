package coredb

import (
	"crypto/sha256"
	"time"
)

const (
	LarkReadOnlyPackID = "lark-readonly@v1"

	LarkGrantStatusActive         = "active"
	LarkGrantStatusReauthRequired = "reauth_required"
	LarkGrantStatusRevoked        = "revoked"
	LarkGrantStatusExpired        = "expired"
)

// RunLarkEgressBinding is immutable launch authority. Credential rotation is
// deliberately not represented here: authority version changes fence runs,
// while credential version can advance without exposing or rewriting a run.
type RunLarkEgressBinding struct {
	GrantID      string
	GrantVersion int64
	GrantUserID  string
	PolicySHA256 [sha256.Size]byte
}

type ResolveUserRunLarkEgressBindingCommand struct {
	WorkspaceID    string
	SessionID      string
	ActorID        string
	IdempotencyKey string
}

type WorkspaceLarkGrant struct {
	ID                   string
	WorkspaceID          string
	UserID               string
	PackID               string
	PolicySHA256         [sha256.Size]byte
	Status               string
	SealedTokenSet       []byte
	AccessExpiresAt      time.Time
	RefreshExpiresAt     *time.Time
	AuthorityVersion     int64
	CredentialVersion    int64
	LastRefreshedAt      *time.Time
	RevokedAt            *time.Time
	NextRefreshAt        time.Time
	RefreshLockOwner     *string
	RefreshLockUntil     *time.Time
	RefreshDispatchedAt  *time.Time
	RefreshAttempts      int
	LastRefreshErrorCode *string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type CreateWorkspaceLarkGrantCommand struct {
	ID               string
	WorkspaceID      string
	UserID           string
	PolicySHA256     [sha256.Size]byte
	SealedTokenSet   []byte
	AccessExpiresAt  time.Time
	RefreshExpiresAt *time.Time
	NextRefreshAt    time.Time
}

// UpsertWorkspaceLarkGrantCommand installs a newly authorized credential set.
// Updating an existing stable grant is a reauthorization event: it advances
// authority_version so already-frozen runs cannot inherit the new authority.
type UpsertWorkspaceLarkGrantCommand struct {
	ID               string
	WorkspaceID      string
	UserID           string
	PolicySHA256     [sha256.Size]byte
	SealedTokenSet   []byte
	AccessExpiresAt  time.Time
	RefreshExpiresAt *time.Time
	NextRefreshAt    time.Time
}

type UpdateWorkspaceLarkGrantCredentialCommand struct {
	GrantID                   string
	ExpectedAuthorityVersion  int64
	ExpectedCredentialVersion int64
	SealedTokenSet            []byte
	AccessExpiresAt           time.Time
	RefreshExpiresAt          *time.Time
	NextRefreshAt             time.Time
}

type ClaimWorkspaceLarkGrantRefreshesCommand struct {
	Owner   string
	Limit   int
	LockTTL time.Duration
}

type MarkWorkspaceLarkGrantRefreshDispatchedCommand struct {
	GrantID                   string
	Owner                     string
	ExpectedAuthorityVersion  int64
	ExpectedCredentialVersion int64
	DispatchTTL               time.Duration
}

type CompleteWorkspaceLarkGrantRefreshCommand struct {
	GrantID                   string
	Owner                     string
	ExpectedAuthorityVersion  int64
	ExpectedCredentialVersion int64
	SealedTokenSet            []byte
	AccessExpiresAt           time.Time
	RefreshExpiresAt          *time.Time
	NextRefreshAt             time.Time
}

type DeferWorkspaceLarkGrantRefreshCommand struct {
	GrantID                   string
	Owner                     string
	ExpectedAuthorityVersion  int64
	ExpectedCredentialVersion int64
	NextRefreshAt             time.Time
	ErrorCode                 string
}

type FailWorkspaceLarkGrantRefreshCommand struct {
	GrantID                   string
	Owner                     string
	ExpectedAuthorityVersion  int64
	ExpectedCredentialVersion int64
	ErrorCode                 string
}

type RevokeWorkspaceLarkGrantCommand struct {
	GrantID     string
	WorkspaceID string
	UserID      string
	ReasonCode  string
}

// ManagedLarkAuthorityQuery binds both placeholder issuance and live webhook
// authorization to one exact Core operation and one TAE sandbox generation.
type ManagedLarkAuthorityQuery struct {
	WorkspaceID       string
	SessionID         string
	ActorID           string
	EnvironmentID     string
	RunID             string
	AttemptID         string
	AttemptGeneration int64
	ExecutionID       string
	OperationID       string
	SandboxID         string
	TargetGeneration  int64
	GrantID           string
	GrantVersion      int64
	PolicySHA256      [sha256.Size]byte
	TAEPSM            string
}

type ManagedLarkAuthority struct {
	WorkspaceID       string
	Binding           RunLarkEgressBinding
	CredentialVersion int64
	SealedTokenSet    []byte
	AccessExpiresAt   time.Time
	AuthorizedAt      time.Time
}

type ManagedEgressAuditEvent struct {
	ID                   string
	DecidedAt            time.Time
	CapabilityID         string
	WorkspaceID          string
	SessionID            string
	RunID                string
	RunAttemptID         string
	RunAttemptGeneration int64
	ExecutionID          string
	OperationID          string
	SandboxID            string
	TargetGeneration     int64
	GrantID              string
	GrantVersion         int64
	TAEPSM               string
	RequestHost          string
	RequestPath          string
	RequestMethod        string
	Decision             string
	ReasonCode           string
}
