package executorgateway

import (
	"context"
	"errors"
	"net/http"
	"time"
)

var ErrConnectionFenced = errors.New("executor connection fenced")

type ExecutorIdentity struct {
	ExecutorID string
}

// ExecutorAuthenticator must verify aud=executor-gateway, executor:connect,
// enrollment status, and machine-key possession. The WSS handler never reads
// executor identity from a hello field.
type ExecutorAuthenticator interface {
	AuthenticateExecutor(*http.Request) (ExecutorIdentity, error)
}

type EnvironmentDeclaration struct {
	ID                  string
	Platform            string
	CodexRelease        string
	CodexCommit         string
	CodexSHA256         [32]byte
	OuterProfileVersion string
	ProcessMethods      []string
	InsecureDev         bool
}

type ConnectionHolder struct {
	ExecutorID        string
	ConnectionID      string
	SessionID         string
	GatewayInstanceID string
	Generation        int64
	Status            string
	ExpiresAt         time.Time
}

type AcquireConnectionRequest struct {
	ExecutorID               string
	ConnectionID             string
	SessionID                string
	GatewayInstanceID        string
	AgentxVersion            string
	RuntimeManifestSHA256    [32]byte
	ExecProtocolSourceSHA256 [32]byte
	Environments             []EnvironmentDeclaration
	LeaseTTL                 time.Duration
}

type ActivateConnectionRequest struct {
	Holder       ConnectionHolder
	Environments []EnvironmentDeclaration
}

// ConnectionAuthority is implemented by the authenticated core command
// client. executor-gateway must not link to coredb or write PostgreSQL.
type ConnectionAuthority interface {
	AcquireConnection(context.Context, AcquireConnectionRequest) (ConnectionHolder, error)
	RenewConnection(context.Context, ConnectionHolder, time.Duration) (ConnectionHolder, error)
	ActivateConnection(context.Context, ActivateConnectionRequest) (ConnectionHolder, error)
	FenceConnection(context.Context, ConnectionHolder) error
}
