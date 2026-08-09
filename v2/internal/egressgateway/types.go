// Package egressgateway implements the TAE Policy Engine webhook used to
// authorize read-only Lark traffic and swap a short-lived placeholder for a
// real credential at the final outbound hop.
package egressgateway

import (
	"context"
	"errors"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecredentials"
	"github.com/agentserver/agentserver/v2/internal/egresscapability"
)

const (
	ProtocolVersion         = "v1"
	PolicyPath              = "/v1/policy"
	ZTIHeader               = "X-Zti-Token"
	AuthorizationHeader     = "Authorization"
	maximumWebhookBodyBytes = 64 * 1024
)

type OriginalRequest struct {
	Host    string            `json:"host"`
	Path    string            `json:"path"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers"`
}

type webhookRequestEnvelope struct {
	Request OriginalRequest `json:"request"`
}

type WebhookResponse struct {
	Code    int                 `json:"code"`
	Message string              `json:"message,omitempty"`
	Version string              `json:"version"`
	Data    WebhookResponseData `json:"data"`
}

type WebhookResponseData struct {
	Result          string            `json:"result"`
	ApplicationInfo string            `json:"application_info,omitempty"`
	Header          map[string]string `json:"header,omitempty"`
}

type ZTIPrincipal struct {
	PSM  string
	User string
}

type ZTIVerifier interface {
	VerifyZTI(context.Context, string) (ZTIPrincipal, error)
}

type Credential struct {
	AccessToken string
	ExpiresAt   time.Time
}

// LiveAuthority must re-check the exact run/attempt/operation/target and
// grant version represented by the placeholder before returning a prefetched
// credential. It must not perform an interactive flow or an unbounded refresh.
type LiveAuthority interface {
	AuthorizeLarkReadOnly(context.Context, egresscapability.Claims, ZTIPrincipal) (Credential, error)
}

type AuditRecord struct {
	At                   time.Time
	CapabilityID         string
	WorkspaceID          string
	SessionID            string
	ActorID              string
	EnvironmentID        string
	RunID                string
	RunAttemptID         string
	RunAttemptGeneration int64
	ExecutionID          string
	OperationID          string
	SandboxID            string
	TargetGeneration     int64
	GrantID              string
	GrantVersion         int64
	ProviderKind         string
	BindingID            string
	AuthorityVersion     int64
	CredentialVersion    int64
	PSM                  string
	Host                 string
	Path                 string
	Method               string
	Decision             string
	ReasonCode           string
}

type AuditSink interface {
	RecordEgressDecision(context.Context, AuditRecord) error
}

type Decision struct {
	Allow      bool
	ReasonCode string
	Headers    map[string]string
}

func (decision Decision) validate() error {
	if decision.Allow {
		if decision.ReasonCode != "allowed" || corecredentials.ValidateClosedHeaderMutation(corecredentials.HeaderMutation{Headers: decision.Headers}) != nil {
			return errors.New("allow decision is incomplete")
		}
		return nil
	}
	if decision.ReasonCode == "" || len(decision.Headers) != 0 {
		return errors.New("deny decision is incomplete")
	}
	return nil
}
