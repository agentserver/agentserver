package executorgateway

import (
	"context"
	"errors"
	"time"

	"github.com/agentserver/agentserver/v2/internal/sandboxcapability"
)

// SignedSandboxGatewayTokenSource issues one short-lived capability bound to
// the exact Core-frozen backend operation and target generation.
type SignedSandboxGatewayTokenSource struct {
	signer *sandboxcapability.Signer
	now    func() time.Time
	ttl    time.Duration
}

func NewSignedSandboxGatewayTokenSource(
	signer *sandboxcapability.Signer,
	now func() time.Time,
	ttl time.Duration,
) (*SignedSandboxGatewayTokenSource, error) {
	if signer == nil || signer.Audience() != sandboxcapability.AudienceBackend || now == nil {
		return nil, errors.New("sandbox backend capability signer and clock are required")
	}
	if ttl == 0 {
		ttl = 30 * time.Second
	}
	if ttl < time.Second || ttl > time.Minute || ttl%time.Millisecond != 0 {
		return nil, errors.New("sandbox backend capability TTL must be whole milliseconds between one second and one minute")
	}
	return &SignedSandboxGatewayTokenSource{signer: signer, now: now, ttl: ttl}, nil
}

func (source *SignedSandboxGatewayTokenSource) Token(ctx context.Context, request SandboxGatewayTokenRequest) (string, error) {
	if source == nil || source.signer == nil || source.now == nil || ctx == nil {
		return "", errors.New("sandbox backend capability token source is required")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := request.Target.Validate(); err != nil {
		return "", err
	}
	if err := request.Operation.Validate(); err != nil {
		return "", err
	}
	now := source.now().UTC().Truncate(time.Millisecond)
	claims := sandboxcapability.Claims{
		Version: sandboxcapability.Version, Issuer: source.signer.Issuer(), Audience: source.signer.Audience(),
		Action: request.Action, CapabilityID: request.Operation.OperationID + ":" + request.Action,
		WorkspaceID: request.Operation.WorkspaceID, SessionID: request.Operation.SessionID,
		EnvironmentID: request.Target.EnvironmentID, RunID: request.Operation.RunID,
		RunAttemptID: request.Operation.RunAttemptID, RunAttemptGeneration: request.Operation.RunAttemptGeneration,
		ExecutionID: request.Operation.ExecutionID, OperationID: request.Operation.OperationID,
		MutationKey: request.Operation.MutationKey, SandboxID: request.Target.ID,
		TargetGeneration: request.Target.Generation,
		IssuedAtUnixMS:   now.Add(-5 * time.Second).UnixMilli(), ExpiresAtUnixMS: now.Add(source.ttl).UnixMilli(),
	}
	return source.signer.Sign(claims)
}

var _ SandboxGatewayTokenSource = (*SignedSandboxGatewayTokenSource)(nil)
