package sandboxclient

import (
	"context"
	"errors"
	"time"

	"github.com/agentserver/agentserver/v2/internal/sandboxcapability"
)

const defaultCapabilityTTL = 30 * time.Second

// SignedTokenSource issues a fresh, exact lifecycle capability for each
// sandbox-gateway request. It never returns a reusable broad bearer token.
type SignedTokenSource struct {
	signer *sandboxcapability.Signer
	now    func() time.Time
	ttl    time.Duration
}

func NewSignedTokenSource(signer *sandboxcapability.Signer, now func() time.Time, ttl time.Duration) (*SignedTokenSource, error) {
	if signer == nil || signer.Audience() != sandboxcapability.AudienceLifecycle || now == nil {
		return nil, errors.New("sandbox lifecycle capability signer and clock are required")
	}
	if ttl == 0 {
		ttl = defaultCapabilityTTL
	}
	if ttl < time.Second || ttl > time.Minute || ttl%time.Millisecond != 0 {
		return nil, errors.New("sandbox lifecycle capability TTL must be whole milliseconds between one second and one minute")
	}
	return &SignedTokenSource{signer: signer, now: now, ttl: ttl}, nil
}

func (source *SignedTokenSource) Token(ctx context.Context, request TokenRequest) (string, error) {
	if source == nil || source.signer == nil || source.now == nil || ctx == nil {
		return "", errors.New("sandbox lifecycle capability token source is required")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	now := source.now().UTC().Truncate(time.Millisecond)
	claims := sandboxcapability.Claims{
		Version: sandboxcapability.Version, Issuer: source.signer.Issuer(), Audience: source.signer.Audience(),
		Action: request.Action, CapabilityID: lifecycleCapabilityID(request),
		WorkspaceID: request.Session.WorkspaceID, SessionID: request.Session.SessionID,
		EnvironmentID: request.Session.EnvironmentID, RunID: request.RunID,
		RunAttemptID: request.RunAttemptID, RunAttemptGeneration: request.RunAttemptGeneration,
		HolderID: request.HolderID, SandboxID: request.Ref.SandboxID,
		TargetGeneration: request.Ref.TargetGeneration,
		IssuedAtUnixMS:   now.Add(-5 * time.Second).UnixMilli(), ExpiresAtUnixMS: now.Add(source.ttl).UnixMilli(),
	}
	return source.signer.Sign(claims)
}

func lifecycleCapabilityID(request TokenRequest) string {
	identity := request.RunAttemptID + ":" + request.Action
	if request.Ref.SandboxID != "" {
		identity += ":" + request.Ref.SandboxID
	}
	return identity
}

var _ TokenSource = (*SignedTokenSource)(nil)
