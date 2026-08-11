package corecontract

import "time"

// ResolveEgressCredentialPath is an internal, workload-authenticated Core
// endpoint.  It is intentionally part of the v2 egress contract rather than
// a second credential-proxy HTTP service: egress-authorizer asks Core for a
// one-hop header mutation after it has checked the TAE request and the
// operation-bound placeholder.
const (
	ResolveEgressCredentialAuthorityPath  = "/internal/v2/egress/credentials:resolve-authority"
	ResolveEgressCredentialPath           = "/internal/v2/egress/credentials:resolve"
	AuthorizeProcessEnvironmentEgressPath = "/internal/v2/egress/credentials:authorize-process-env"
	ResolveExecutionLarkCredentialPath    = "/internal/v2/execution/credentials/lark:resolve"
)

// RecordEgressCredentialAuditPath is kept separate from the legacy
// managed-lark audit endpoint so provider-neutral credential use events do not
// get squeezed into a Lark-shaped schema.
const RecordEgressCredentialAuditPath = "/internal/v2/egress/credential-use-events"

type EgressCredentialOperation struct {
	WorkspaceID          string `json:"workspaceId"`
	SessionID            string `json:"sessionId"`
	ActorID              string `json:"actorId"`
	EnvironmentID        string `json:"environmentId"`
	RunID                string `json:"runId"`
	RunAttemptID         string `json:"runAttemptId"`
	RunAttemptGeneration int64  `json:"runAttemptGeneration"`
	ExecutionID          string `json:"executionId"`
	OperationID          string `json:"operationId"`
	SandboxID            string `json:"sandboxId"`
	TargetGeneration     int64  `json:"targetGeneration"`
}

// ResolveEgressCredentialRequest contains no provider secret. Placeholder is
// an operation-bound, short-lived capability and the request tuple is
// canonicalized/checked again by Core.
type ResolveEgressCredentialRequest struct {
	Placeholder      string                    `json:"placeholder"`
	Operation        EgressCredentialOperation `json:"operation"`
	ProviderKind     string                    `json:"providerKind"`
	BindingID        string                    `json:"bindingId"`
	AuthorityVersion int64                     `json:"authorityVersion"`
	PolicySHA256     string                    `json:"policySha256"`
	TAEPSM           string                    `json:"taePsm"`
	Host             string                    `json:"host"`
	Path             string                    `json:"path"`
	Method           string                    `json:"method"`
	Headers          map[string]string         `json:"headers,omitempty"`
}

type ResolveEgressCredentialAuthorityRequest struct {
	Operation    EgressCredentialOperation `json:"operation"`
	ProviderKind string                    `json:"providerKind"`
	PolicySHA256 string                    `json:"policySha256"`
}

type ResolveEgressCredentialAuthorityResponse struct {
	CredentialMode    string    `json:"credentialMode"`
	ProviderKind      string    `json:"providerKind"`
	ApplicationID     string    `json:"applicationId,omitempty"`
	BindingID         string    `json:"bindingId"`
	AuthorityVersion  int64     `json:"authorityVersion"`
	CredentialVersion int64     `json:"credentialVersion"`
	PolicySHA256      string    `json:"policySha256"`
	AuthorizedAt      time.Time `json:"authorizedAt"`
}

// ResolveExecutionLarkCredentialRequest is the narrow process-environment
// delivery contract used only by executor-gateway. Core rechecks the exact
// live process_start operation and TAE sandbox authority before returning a
// real Lark access token. ToolName and Executable close the endpoint to the
// one supported shell/lark-cli launch shape.
type ResolveExecutionLarkCredentialRequest struct {
	Operation         EgressCredentialOperation `json:"operation"`
	TAEPSM            string                    `json:"taePsm"`
	PolicySHA256      string                    `json:"policySha256"`
	ToolName          string                    `json:"toolName"`
	Executable        string                    `json:"executable"`
	BindingID         string                    `json:"bindingId"`
	AuthorityVersion  int64                     `json:"authorityVersion"`
	CredentialVersion int64                     `json:"credentialVersion"`
}

type ResolveExecutionLarkCredentialResponse struct {
	Configured        bool       `json:"configured"`
	CredentialMode    string     `json:"credentialMode"`
	AccessToken       string     `json:"accessToken,omitempty"`
	ApplicationID     string     `json:"applicationId,omitempty"`
	ProviderKind      string     `json:"providerKind"`
	BindingID         string     `json:"bindingId,omitempty"`
	AuthorityVersion  int64      `json:"authorityVersion,omitempty"`
	CredentialVersion int64      `json:"credentialVersion,omitempty"`
	PolicySHA256      string     `json:"policySha256"`
	TAEPSM            string     `json:"taePsm"`
	ResolvedAt        time.Time  `json:"resolvedAt"`
	AccessExpiresAt   *time.Time `json:"accessExpiresAt,omitempty"`
}

// AuthorizeProcessEnvironmentEgressRequest is sent only by the TAE Policy
// Webhook after it has verified the compact X-Agent-Trace companion proof.
// Core verifies the proof independently, rechecks the workspace's current
// process_env setting and live operation, then compares the presented bearer
// to the current sealed binding before returning a sanitized pass-through
// mutation.
type AuthorizeProcessEnvironmentEgressRequest struct {
	ProcessProof      string                    `json:"processProof"`
	Operation         EgressCredentialOperation `json:"operation"`
	ProviderKind      string                    `json:"providerKind"`
	BindingID         string                    `json:"bindingId"`
	AuthorityVersion  int64                     `json:"authorityVersion"`
	CredentialVersion int64                     `json:"credentialVersion"`
	PolicySHA256      string                    `json:"policySha256"`
	TAEPSM            string                    `json:"taePsm"`
	Host              string                    `json:"host"`
	Path              string                    `json:"path"`
	Method            string                    `json:"method"`
	Headers           map[string]string         `json:"headers"`
}

// ResolveEgressCredentialResponse is the credential-bearing response consumed
// immediately by egress-authorizer. webhook_swap receives a replacement
// Authorization; process_env receives a validated pass-through Authorization
// and sanitized trace. Neither path returns a token field, refresh token, or
// sealed envelope to a sandbox, harness, or Platform client.
type ResolveEgressCredentialResponse struct {
	Headers           map[string]string `json:"headers"`
	ProviderKind      string            `json:"providerKind"`
	BindingID         string            `json:"bindingId"`
	AuthorityVersion  int64             `json:"authorityVersion"`
	CredentialVersion int64             `json:"credentialVersion"`
	ResolvedAt        time.Time         `json:"resolvedAt"`
	AccessExpiresAt   *time.Time        `json:"accessExpiresAt,omitempty"`
}

type RecordEgressCredentialAuditRequest struct {
	EventID           string                    `json:"eventId"`
	At                time.Time                 `json:"at"`
	CapabilityID      string                    `json:"capabilityId,omitempty"`
	Operation         EgressCredentialOperation `json:"operation"`
	ProviderKind      string                    `json:"providerKind"`
	BindingID         string                    `json:"bindingId,omitempty"`
	AuthorityVersion  int64                     `json:"authorityVersion,omitempty"`
	CredentialVersion int64                     `json:"credentialVersion,omitempty"`
	TAEPSM            string                    `json:"taePsm,omitempty"`
	Host              string                    `json:"host"`
	Path              string                    `json:"path"`
	Method            string                    `json:"method"`
	Decision          string                    `json:"decision"`
	ReasonCode        string                    `json:"reasonCode"`
}

type RecordEgressCredentialAuditResponse struct {
	Recorded bool `json:"recorded"`
}
