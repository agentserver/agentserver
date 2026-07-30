package coredb

import (
	"encoding/json"
	"time"
)

const (
	ExecutorStatusEnrolling = "enrolling"
	ExecutorStatusOffline   = "offline"
	ExecutorStatusOnline    = "online"
	ExecutorStatusRevoked   = "revoked"

	ExecutorEnvironmentStatusOffline  = "offline"
	ExecutorEnvironmentStatusOnline   = "online"
	ExecutorEnvironmentStatusDisabled = "disabled"

	ExecutorConnectionStatusConnecting = "connecting"
	ExecutorConnectionStatusOnline     = "online"
	ExecutorConnectionStatusFenced     = "fenced"

	MaxExecutorConnectionTTL      = 5 * time.Minute
	MaxListedExecutorEnvironments = 256
)

type ListOnlineExecutorEnvironmentsQuery struct {
	WorkspaceID string
	ExecutorID  string
}

type OnlineExecutorEnvironment struct {
	EnvironmentID        string
	ExecutorID           string
	RootDescriptor       json.RawMessage
	Platform             string
	InsecureDev          bool
	EnvironmentVersion   int64
	ConnectionGeneration int64
}

// ExecutorEnvironmentDeclaration is the build and profile identity asserted
// by an authenticated agentx hello. AcquireExecutorConnection compares every
// field with core's enrolled environment record before changing generation.
type ExecutorEnvironmentDeclaration struct {
	ID                  string
	Platform            string
	CodexRelease        string
	CodexCommit         string
	CodexSHA256         [32]byte
	OuterProfileVersion string
	ProcessMethods      []string
	InsecureDev         bool
}

type ExecutorConnection struct {
	ExecutorID               string
	Generation               int64
	ConnectionID             string
	SessionID                string
	GatewayInstanceID        string
	AgentxVersion            string
	RuntimeManifestSHA256    [32]byte
	ExecProtocolSourceSHA256 [32]byte
	EnvironmentSetSHA256     [32]byte
	Status                   string
	ExpiresAt                time.Time
	AcquiredAt               time.Time
	RenewedAt                time.Time
	Version                  int64
}

type AcquireExecutorConnectionCommand struct {
	ExecutorID               string
	ConnectionID             string
	SessionID                string
	GatewayInstanceID        string
	AgentxVersion            string
	RuntimeManifestSHA256    [32]byte
	ExecProtocolSourceSHA256 [32]byte
	Environments             []ExecutorEnvironmentDeclaration
	LeaseTTL                 time.Duration
}

type AcquireExecutorConnectionResult struct {
	Connection ExecutorConnection
	Acquired   bool
}

type RenewExecutorConnectionCommand struct {
	ExecutorID        string
	SessionID         string
	GatewayInstanceID string
	Generation        int64
	LeaseTTL          time.Duration
}

type ActivateExecutorConnectionCommand struct {
	ExecutorID        string
	SessionID         string
	GatewayInstanceID string
	Generation        int64
	Environments      []ExecutorEnvironmentDeclaration
}

type ActivateExecutorConnectionResult struct {
	Connection ExecutorConnection
	Activated  bool
}

type FenceExecutorConnectionCommand struct {
	ExecutorID        string
	SessionID         string
	GatewayInstanceID string
	Generation        int64
}
