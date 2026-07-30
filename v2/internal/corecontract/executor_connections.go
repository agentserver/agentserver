// Package corecontract contains transport-only internal API types. It has no
// database dependency so non-core components cannot reach coredb through it.
package corecontract

import (
	"encoding/json"
	"time"
)

const (
	AcquireExecutorConnectionPath = "/internal/v2/executor-connections:acquire"
	ExecutorConnectionPathPrefix  = "/internal/v2/executor-connections/"
	ListExecutorEnvironmentsPath  = "/internal/v2/executor-environments:list"
)

type EnvironmentDeclaration struct {
	ID                  string   `json:"envId"`
	Platform            string   `json:"platform"`
	CodexRelease        string   `json:"codexRelease"`
	CodexCommit         string   `json:"codexCommit"`
	CodexSHA256         string   `json:"codexSha256"`
	OuterProfileVersion string   `json:"outerProfileVersion"`
	ProcessMethods      []string `json:"processMethods"`
	InsecureDev         bool     `json:"insecureDev"`
}

type ConnectionHolder struct {
	ExecutorID        string    `json:"executorId"`
	ConnectionID      string    `json:"connectionId"`
	SessionID         string    `json:"sessionId"`
	GatewayInstanceID string    `json:"gatewayInstanceId"`
	Generation        int64     `json:"generation"`
	Status            string    `json:"status"`
	ExpiresAt         time.Time `json:"expiresAt"`
}

type AcquireExecutorConnectionRequest struct {
	ExecutorID               string                   `json:"executorId"`
	ConnectionID             string                   `json:"connectionId"`
	SessionID                string                   `json:"sessionId"`
	GatewayInstanceID        string                   `json:"gatewayInstanceId"`
	AgentxVersion            string                   `json:"agentxVersion"`
	RuntimeManifestSHA256    string                   `json:"runtimeManifestSha256"`
	ExecProtocolSourceSHA256 string                   `json:"execProtocolSourceSha256"`
	Environments             []EnvironmentDeclaration `json:"environments"`
	ConnectionLeaseTTLMillis int64                    `json:"connectionLeaseTtlMs"`
}

type RenewExecutorConnectionRequest struct {
	Holder                   ConnectionHolder `json:"holder"`
	ConnectionLeaseTTLMillis int64            `json:"connectionLeaseTtlMs"`
}

type ActivateExecutorConnectionRequest struct {
	Holder       ConnectionHolder         `json:"holder"`
	Environments []EnvironmentDeclaration `json:"environments"`
}

type FenceExecutorConnectionRequest struct {
	Holder ConnectionHolder `json:"holder"`
}

type ExecutorConnectionResponse struct {
	Holder ConnectionHolder `json:"holder"`
}

type ListExecutorEnvironmentsRequest struct {
	WorkspaceID string `json:"workspaceId"`
	ExecutorID  string `json:"executorId,omitempty"`
}

type ExecutorEnvironment struct {
	EnvironmentID        string          `json:"environmentId"`
	ExecutorID           string          `json:"executorId"`
	RootDescriptor       json.RawMessage `json:"rootDescriptor"`
	Platform             string          `json:"platform"`
	InsecureDev          bool            `json:"insecureDev"`
	EnvironmentVersion   int64           `json:"environmentVersion"`
	ConnectionGeneration int64           `json:"connectionGeneration"`
}

type ListExecutorEnvironmentsResponse struct {
	Environments []ExecutorEnvironment `json:"environments"`
}

type ErrorResponse struct {
	Code              string `json:"code"`
	Message           string `json:"message"`
	CurrentVersion    int64  `json:"currentVersion,omitempty"`
	CurrentGeneration int64  `json:"currentGeneration,omitempty"`
}

func RenewExecutorConnectionPath(executorID string) string {
	return ExecutorConnectionPathPrefix + executorID + ":renew"
}

func ActivateExecutorConnectionPath(executorID string) string {
	return ExecutorConnectionPathPrefix + executorID + ":activate"
}

func FenceExecutorConnectionPath(executorID string) string {
	return ExecutorConnectionPathPrefix + executorID + ":fence"
}
