package corecontract

import (
	"encoding/json"
	"time"
)

const (
	CompleteExecutorEnrollmentPath      = "/internal/v2/executor-enrollments:complete"
	AuthorizeExecutorConnectionPath     = "/internal/v2/executor-connections:authorize"
	ExecutorManagementRoutePattern      = "/v2/workspaces/{workspaceId}/executors"
	ExecutorEnrollmentTokenRoutePattern = "/v2/workspaces/{workspaceId}/executors/{executorAction}"
)

type CreateExecutorResourceRequest struct {
	ExecutorID string `json:"executorId"`
}

type ExecutorResourceState struct {
	ExecutorID  string    `json:"executorId"`
	WorkspaceID string    `json:"workspaceId"`
	Status      string    `json:"status"`
	Version     int64     `json:"version"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type CreateExecutorResourceResponse struct {
	Executor ExecutorResourceState `json:"executor"`
	Created  bool                  `json:"created"`
}

type IssueExecutorEnrollmentTokenResponse struct {
	ExecutorID string    `json:"executorId"`
	Token      string    `json:"token"`
	ExpiresAt  time.Time `json:"expiresAt"`
	Created    bool      `json:"created"`
}

type ExecutorEnrollmentEnvironment struct {
	EnvironmentDeclaration
	RootDescriptor    json.RawMessage `json:"rootDescriptor"`
	OwnerPolicySHA256 string          `json:"ownerPolicySha256"`
}

type CompleteExecutorEnrollmentRequest struct {
	MachinePublicKeyEd25519  string                          `json:"machinePublicKeyEd25519"`
	MachineProofEd25519      string                          `json:"machineProofEd25519"`
	OAuthPublicKeyP256X      string                          `json:"oauthPublicKeyP256X"`
	OAuthPublicKeyP256Y      string                          `json:"oauthPublicKeyP256Y"`
	OAuthProofES256          string                          `json:"oauthProofES256"`
	AgentxVersion            string                          `json:"agentxVersion"`
	RuntimeManifestSHA256    string                          `json:"runtimeManifestSha256"`
	ExecProtocolSourceSHA256 string                          `json:"execProtocolSourceSha256"`
	Environments             []ExecutorEnrollmentEnvironment `json:"environments"`
}

type CompleteExecutorEnrollmentResponse struct {
	Executor      ExecutorResourceState `json:"executor"`
	OAuthClientID string                `json:"oauthClientId"`
	Audience      string                `json:"audience"`
	Scope         string                `json:"scope"`
}

type AuthorizeExecutorConnectionResponse struct {
	ExecutorID              string    `json:"executorId"`
	WorkspaceID             string    `json:"workspaceId"`
	OAuthClientID           string    `json:"oauthClientId"`
	MachinePublicKeyEd25519 string    `json:"machinePublicKeyEd25519"`
	MachineKeySHA256        string    `json:"machineKeySha256"`
	ExecutorVersion         int64     `json:"executorVersion"`
	TokenExpiresAt          time.Time `json:"tokenExpiresAt"`
	AuthorizedAt            time.Time `json:"authorizedAt"`
}

func CreateExecutorResourcePath(workspaceID string) string {
	return "/v2/workspaces/" + workspaceID + "/executors"
}

func IssueExecutorEnrollmentTokenPath(workspaceID, executorID string) string {
	return "/v2/workspaces/" + workspaceID + "/executors/" + executorID + ":enrollmentToken"
}
