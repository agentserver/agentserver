package coredb

import (
	"encoding/json"
	"time"
)

const MaxExecutorEnrollmentTTL = 15 * time.Minute

type ExecutorResource struct {
	ID          string
	WorkspaceID string
	Status      string
	Version     int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type CreateExecutorResourceCommand struct {
	ExecutorID  string
	WorkspaceID string
	ActorID     string
}

type CreateExecutorResourceResult struct {
	Executor ExecutorResource
	Created  bool
}

type ExecutorEnrollmentToken struct {
	ID               string
	WorkspaceID      string
	ExecutorID       string
	IssuedBy         string
	IdempotencyKey   string
	IssuedAt         time.Time
	ExpiresAt        time.Time
	ClaimedAt        *time.Time
	ConsumedAt       *time.Time
	RevokedAt        *time.Time
	EnrollmentSHA256 []byte
	Version          int64
}

type IssueExecutorEnrollmentTokenCommand struct {
	TokenID        string
	WorkspaceID    string
	ExecutorID     string
	ActorID        string
	IdempotencyKey string
	TTL            time.Duration
}

type IssueExecutorEnrollmentTokenResult struct {
	Token   ExecutorEnrollmentToken
	Created bool
}

type ExecutorEnrollmentEnvironment struct {
	ExecutorEnvironmentDeclaration
	RootDescriptor    json.RawMessage
	OwnerPolicySHA256 [32]byte
}

type ClaimExecutorEnrollmentCommand struct {
	TokenID                  string
	WorkspaceID              string
	ExecutorID               string
	IssuedByActorID          string
	IssuedAt                 time.Time
	ExpiresAt                time.Time
	MachinePublicKeyEd25519  [32]byte
	MachineKeySHA256         [32]byte
	OAuthPublicKeyP256X      [32]byte
	OAuthPublicKeyP256Y      [32]byte
	OAuthKeySHA256           [32]byte
	OAuthClientID            string
	AgentxVersion            string
	RuntimeManifestSHA256    [32]byte
	ExecProtocolSourceSHA256 [32]byte
	EnrollmentRequestSHA256  [32]byte
	Environments             []ExecutorEnrollmentEnvironment
}

type ExecutorEnrollmentReservation struct {
	Executor      ExecutorResource
	OAuthClientID string
	Created       bool
}

type CompleteExecutorEnrollmentCommand struct {
	TokenID                 string
	WorkspaceID             string
	ExecutorID              string
	EnrollmentRequestSHA256 [32]byte
}

type ExecutorMachineAuthority struct {
	ExecutorID              string
	WorkspaceID             string
	OAuthClientID           string
	MachinePublicKeyEd25519 [32]byte
	MachineKeySHA256        [32]byte
	OAuthPublicKeyP256X     [32]byte
	OAuthPublicKeyP256Y     [32]byte
	OAuthKeySHA256          [32]byte
	ExecutorVersion         int64
	AuthorizedAt            time.Time
}
