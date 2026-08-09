package egressgateway

import (
	"errors"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecredentials"
	"github.com/agentserver/agentserver/v2/internal/egresscapability"
)

// CapabilityPlaceholderVerifier adapts the v2 signed operation capability to
// the provider materializer. Keeping this adapter at the egress boundary
// means Core and egress-authorizer share the exact same signed-placeholder
// format without exposing the capability package in the credential store.
type CapabilityPlaceholderVerifier struct {
	verifier *egresscapability.Verifier
}

func NewCapabilityPlaceholderVerifier(verifier *egresscapability.Verifier) (*CapabilityPlaceholderVerifier, error) {
	if verifier == nil {
		return nil, errors.New("egress placeholder verifier is required")
	}
	return &CapabilityPlaceholderVerifier{verifier: verifier}, nil
}

func (adapter *CapabilityPlaceholderVerifier) Verify(token string, now time.Time) (corecredentials.PlaceholderClaims, error) {
	if adapter == nil || adapter.verifier == nil {
		return corecredentials.PlaceholderClaims{}, errors.New("egress placeholder verifier is unavailable")
	}
	claims, err := adapter.verifier.Verify(token, now)
	if err != nil || !strings.HasPrefix(claims.Audience, egresscapability.AudienceCredentialPrefix) {
		return corecredentials.PlaceholderClaims{}, errors.New("placeholder is not a provider credential capability")
	}
	return corecredentials.PlaceholderClaims{
		CapabilityID: claims.CapabilityID, WorkspaceID: claims.WorkspaceID,
		SessionID: claims.SessionID, ActorID: claims.ActorID,
		EnvironmentID: claims.EnvironmentID, RunID: claims.RunID, RunAttemptID: claims.RunAttemptID,
		RunAttemptGeneration: claims.RunAttemptGeneration, ExecutionID: claims.ExecutionID,
		OperationID: claims.OperationID, SandboxID: claims.SandboxID, TargetGeneration: claims.TargetGeneration,
		ProviderKind: claims.ProviderKind, BindingID: claims.BindingID, AuthorityVersion: claims.AuthorityVersion,
		PolicySHA256: claims.PolicySHA256, ExpiresAt: time.UnixMilli(claims.ExpiresAtUnixMS).UTC(),
	}, nil
}

var _ corecredentials.PlaceholderVerifier = (*CapabilityPlaceholderVerifier)(nil)
