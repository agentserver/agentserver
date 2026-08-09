package corecontract

import "time"

// ResolveEgressCredentialPath is an internal, workload-authenticated Core
// endpoint.  It is intentionally part of the v2 egress contract rather than
// a second credential-proxy HTTP service: egress-authorizer asks Core for a
// one-hop header mutation after it has checked the TAE request and the
// operation-bound placeholder.
const (
	ResolveEgressCredentialAuthorityPath = "/internal/v2/egress/credentials:resolve-authority"
	ResolveEgressCredentialPath          = "/internal/v2/egress/credentials:resolve"
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
	ProviderKind      string    `json:"providerKind"`
	BindingID         string    `json:"bindingId"`
	AuthorityVersion  int64     `json:"authorityVersion"`
	CredentialVersion int64     `json:"credentialVersion"`
	PolicySHA256      string    `json:"policySha256"`
	AuthorizedAt      time.Time `json:"authorizedAt"`
}

// ResolveEgressCredentialResponse is the only credential-bearing response in
// v2. Headers are a closed-world mutation consumed immediately by
// egress-authorizer; Core never returns a token field, a refresh token, or a
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
