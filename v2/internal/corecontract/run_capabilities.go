package corecontract

import "time"

const (
	IssueRunCapabilitiesPath           = "/internal/v2/run-capabilities:issue"
	AuthorizeExecutorRunCapabilityPath = "/internal/v2/run-capabilities:authorize-executor-mcp"
	AuthorizeLLMProxyRunCapabilityPath = "/internal/v2/run-capabilities:authorize-llmproxy"
)

// IssueRunCapabilitiesRequest is the complete manifest-derived projection a
// harness-pool asks Core to authorize. Core re-derives live run, lease,
// membership, catalog and model-route facts and requires the route and limits
// to match its production policy; no field in this request is authority by
// itself. Executor availability is checked only when a tool is actually
// dispatched, not while these audience-separated tokens are issued.
type IssueRunCapabilitiesRequest struct {
	WorkspaceID               string                        `json:"workspaceId"`
	SessionID                 string                        `json:"sessionId"`
	RunID                     string                        `json:"runId"`
	RunAttemptID              string                        `json:"runAttemptId"`
	HolderID                  string                        `json:"holderId"`
	RunAttemptGeneration      int64                         `json:"runAttemptGeneration"`
	ExpectedRunVersion        int64                         `json:"expectedRunVersion"`
	ExpectedRunAttemptVersion int64                         `json:"expectedRunAttemptVersion"`
	ExecutorID                string                        `json:"executorId"`
	BrainToolCatalogID        string                        `json:"brainToolCatalogId"`
	ToolCatalogDigest         string                        `json:"toolCatalogDigest"`
	Model                     string                        `json:"model"`
	Provider                  string                        `json:"provider"`
	LLMGatewayID              string                        `json:"llmGatewayId"`
	LLMGatewayVersion         int64                         `json:"llmGatewayVersion"`
	LLMGatewayGrantUserID     string                        `json:"llmGatewayGrantUserId"`
	MaxRunDurationMillis      int64                         `json:"maxRunDurationMs"`
	MaxApprovalTTLMillis      int64                         `json:"maxApprovalTtlMs"`
	ManagedSandbox            *RunLaunchManagedSandboxState `json:"managedSandbox,omitempty"`
	Workspace                 *RunLaunchWorkspaceState      `json:"workspace,omitempty"`
	PermissionMode            string                        `json:"permissionMode,omitempty"`
	PermissionModeVersion     int64                         `json:"permissionModeVersion,omitempty"`
}

type IssuedRunCapability struct {
	CapabilityID string    `json:"capabilityId"`
	Audience     string    `json:"audience"`
	Token        string    `json:"token"`
	IssuedAt     time.Time `json:"issuedAt"`
	RunDeadline  time.Time `json:"runDeadline"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

type IssueRunCapabilitiesResponse struct {
	ExecutorMCP IssuedRunCapability `json:"executorMcp"`
	LLMProxy    IssuedRunCapability `json:"llmproxy"`
}

// AuthorizeExecutorRunCapabilityRequest binds the bearer token to the exact
// executor gateway and frozen catalog serving this HTTP request. The token is
// carried in Authorization rather than duplicated in JSON.
type AuthorizeExecutorRunCapabilityRequest struct {
	ExecutorID        string `json:"executorId"`
	ToolCatalogDigest string `json:"toolCatalogDigest"`
}

// AuthorizeLLMProxyRunCapabilityRequest binds the bearer token to the exact
// upstream route requested through llmproxy.
type AuthorizeLLMProxyRunCapabilityRequest struct {
	Model                 string `json:"model"`
	Provider              string `json:"provider"`
	LLMGatewayID          string `json:"llmGatewayId"`
	LLMGatewayVersion     int64  `json:"llmGatewayVersion"`
	LLMGatewayGrantUserID string `json:"llmGatewayGrantUserId"`
}

type AuthorizeRunCapabilityResponse struct {
	CapabilityID         string    `json:"capabilityId"`
	Audience             string    `json:"audience"`
	RunID                string    `json:"runId"`
	RunAttemptID         string    `json:"runAttemptId"`
	RunAttemptGeneration int64     `json:"runAttemptGeneration"`
	RunVersion           int64     `json:"runVersion"`
	RunAttemptVersion    int64     `json:"runAttemptVersion"`
	AuthorizedAt         time.Time `json:"authorizedAt"`
}

// AuthorizeLLMProxyRunCapabilityResponse is intentionally separate from the
// executor response. The route and bearer are a fresh Core decision for this
// one proxy request; llmproxy must neither source nor cache either value.
type AuthorizeLLMProxyRunCapabilityResponse struct {
	CapabilityID          string    `json:"capabilityId"`
	Audience              string    `json:"audience"`
	RunID                 string    `json:"runId"`
	RunAttemptID          string    `json:"runAttemptId"`
	RunAttemptGeneration  int64     `json:"runAttemptGeneration"`
	RunVersion            int64     `json:"runVersion"`
	RunAttemptVersion     int64     `json:"runAttemptVersion"`
	AuthorizedAt          time.Time `json:"authorizedAt"`
	Model                 string    `json:"model"`
	Provider              string    `json:"provider"`
	LLMGatewayID          string    `json:"llmGatewayId"`
	LLMGatewayVersion     int64     `json:"llmGatewayVersion"`
	LLMGatewayGrantUserID string    `json:"llmGatewayGrantUserId"`
	ResponsesURL          string    `json:"responsesUrl"`
	UpstreamAuthorization string    `json:"upstreamAuthorization"`
	BearerExpiresAt       time.Time `json:"bearerExpiresAt"`
}
