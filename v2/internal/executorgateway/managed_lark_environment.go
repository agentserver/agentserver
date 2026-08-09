package executorgateway

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/agentserver/agentserver/v2/internal/egresscapability"
	"github.com/agentserver/agentserver/v2/internal/executionbackend"
)

var (
	managedLarkPolicyDigestPattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	managedLarkApplicationIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$`)
)

type ManagedLarkEgressAuthority struct {
	BindingID         string
	AuthorityVersion  int64
	CredentialVersion int64
	PolicySHA256      string
}

func (authority ManagedLarkEgressAuthority) Validate() error {
	if authority.BindingID == "" || authority.AuthorityVersion < 1 || authority.CredentialVersion < 1 {
		return errors.New("managed Lark credential binding identity or authority version is invalid")
	}
	if err := validateRegistryIdentity("managed Lark credential binding ID", authority.BindingID); err != nil {
		return err
	}
	if !managedLarkPolicyDigestPattern.MatchString(authority.PolicySHA256) {
		return errors.New("managed Lark grant version or policy digest is invalid")
	}
	return nil
}

type ManagedLarkEgressAuthoritySource interface {
	ResolveManagedLarkEgressAuthority(context.Context, ManagedProcessEnvironmentRequest) (ManagedLarkEgressAuthority, error)
}

// FrozenManagedLarkEgressAuthoritySource projects deployment/run authority
// that was already frozen by Core. Revocation and live operation checks are
// repeated by the egress authorizer before any real credential is injected.
type FrozenManagedLarkEgressAuthoritySource struct {
	authority ManagedLarkEgressAuthority
}

func NewFrozenManagedLarkEgressAuthoritySource(authority ManagedLarkEgressAuthority) (*FrozenManagedLarkEgressAuthoritySource, error) {
	if err := authority.Validate(); err != nil {
		return nil, err
	}
	return &FrozenManagedLarkEgressAuthoritySource{authority: authority}, nil
}

func (source *FrozenManagedLarkEgressAuthoritySource) ResolveManagedLarkEgressAuthority(ctx context.Context, _ ManagedProcessEnvironmentRequest) (ManagedLarkEgressAuthority, error) {
	if source == nil || ctx == nil {
		return ManagedLarkEgressAuthority{}, errors.New("managed Lark egress authority source is required")
	}
	if err := ctx.Err(); err != nil {
		return ManagedLarkEgressAuthority{}, err
	}
	return source.authority, nil
}

type SignedManagedLarkEnvironmentIssuer struct {
	signer        *egresscapability.Signer
	authorities   ManagedLarkEgressAuthoritySource
	applicationID string
	idGenerator   IDGenerator
	now           func() time.Time
	ttl           time.Duration
}

func NewSignedManagedLarkEnvironmentIssuer(
	signer *egresscapability.Signer,
	authorities ManagedLarkEgressAuthoritySource,
	applicationID string,
	idGenerator IDGenerator,
	now func() time.Time,
	ttl time.Duration,
) (*SignedManagedLarkEnvironmentIssuer, error) {
	if signer == nil || authorities == nil || idGenerator == nil || now == nil {
		return nil, errors.New("managed Lark environment signer, authority source, identity generator, and clock are required")
	}
	if !managedLarkApplicationIDPattern.MatchString(applicationID) {
		return nil, errors.New("managed Lark application ID must be bounded canonical text")
	}
	if ttl < time.Second || ttl > 115*time.Second || ttl%time.Millisecond != 0 {
		return nil, errors.New("managed Lark placeholder TTL must be whole milliseconds between one and 115 seconds")
	}
	return &SignedManagedLarkEnvironmentIssuer{
		signer: signer, authorities: authorities, applicationID: applicationID,
		idGenerator: idGenerator, now: now, ttl: ttl,
	}, nil
}

func NewDefaultSignedManagedLarkEnvironmentIssuer(
	signer *egresscapability.Signer,
	authorities ManagedLarkEgressAuthoritySource,
	applicationID string,
) (*SignedManagedLarkEnvironmentIssuer, error) {
	return NewSignedManagedLarkEnvironmentIssuer(signer, authorities, applicationID, newRandomUUID, time.Now, 60*time.Second)
}

func (issuer *SignedManagedLarkEnvironmentIssuer) IssueManagedProcessEnvironment(
	ctx context.Context,
	request ManagedProcessEnvironmentRequest,
) (map[string]string, error) {
	if issuer == nil || issuer.signer == nil || issuer.authorities == nil || issuer.idGenerator == nil || issuer.now == nil || ctx == nil {
		return nil, errors.New("managed Lark environment issuer and context are required")
	}
	if request.Target.Kind != executionbackend.KindTAE {
		return nil, errors.New("managed Lark placeholder requires a TAE target")
	}
	// Non-Lark commands deliberately receive no placeholder. TAE's network
	// policy remains default-deny, so curl or another binary cannot acquire a
	// real credential merely by running in the same sandbox.
	if request.ToolName != "shell" || request.Executable != "lark-cli" {
		return map[string]string{}, nil
	}
	if err := validateExecutorMCPPrincipal(request.Principal); err != nil {
		return nil, err
	}
	if err := request.Target.Validate(); err != nil {
		return nil, err
	}
	if err := request.Operation.Validate(); err != nil {
		return nil, err
	}
	principal := request.Principal
	if request.Operation.WorkspaceID != principal.WorkspaceID || request.Operation.SessionID != principal.SessionID ||
		request.Operation.RunID != principal.Run.RunID || request.Operation.RunAttemptID != principal.Run.RunAttemptID ||
		request.Operation.RunAttemptGeneration != principal.Run.RunAttemptGeneration ||
		request.Target.EnvironmentID == "" {
		return nil, errors.New("managed Lark placeholder operation differs from the MCP principal")
	}
	authority, err := issuer.authorities.ResolveManagedLarkEgressAuthority(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("resolve managed Lark egress authority: %w", err)
	}
	// No workspace binding is a valid state. Keep the non-sensitive runtime
	// projection (PATH and app hint) so lark-cli can report a normal
	// credential-not-configured error, but never mint a placeholder that could
	// be confused with a credential authority.
	if authority.BindingID == "" {
		return map[string]string{
			ManagedLarkApplicationIDEnvironment:    issuer.applicationID,
			ManagedLarkNoUpdateNotifierEnvironment: "1",
			ManagedLarkNoSkillsNotifierEnvironment: "1",
			ManagedLarkPathEnvironment:             ManagedLarkPathValue,
		}, nil
	}
	if err := authority.Validate(); err != nil {
		return nil, err
	}
	capabilityID, err := issuer.idGenerator()
	if err != nil {
		return nil, fmt.Errorf("allocate managed Lark placeholder identity: %w", err)
	}
	if err := validateRegistryIdentity("managed Lark placeholder ID", capabilityID); err != nil {
		return nil, err
	}
	now := issuer.now().UTC().Truncate(time.Millisecond)
	expiresAt := now.Add(issuer.ttl)
	if principal.RunDeadline.Before(expiresAt) {
		expiresAt = principal.RunDeadline.UTC().Truncate(time.Millisecond)
	}
	if principal.CapabilityExpiresAt.Before(expiresAt) {
		expiresAt = principal.CapabilityExpiresAt.UTC().Truncate(time.Millisecond)
	}
	if !expiresAt.After(now.Add(time.Second)) {
		return nil, errors.New("managed Lark placeholder has no safe remaining authority window")
	}
	claims := egresscapability.Claims{
		Version: egresscapability.Version, Issuer: issuer.signer.Issuer(),
		Audience: egresscapability.AudienceForProvider("lark"), CapabilityID: capabilityID,
		WorkspaceID: principal.WorkspaceID, SessionID: principal.SessionID, ActorID: principal.ActorID,
		EnvironmentID: request.Target.EnvironmentID, RunID: principal.Run.RunID,
		RunAttemptID: principal.Run.RunAttemptID, RunAttemptGeneration: principal.Run.RunAttemptGeneration,
		ExecutionID: request.Operation.ExecutionID, OperationID: request.Operation.OperationID,
		SandboxID: request.Target.ID, TargetGeneration: request.Target.Generation,
		PackID: egresscapability.PackLarkReadOnly, ProviderKind: "lark", BindingID: authority.BindingID,
		AuthorityVersion: authority.AuthorityVersion, PolicySHA256: authority.PolicySHA256, Executable: "lark-cli",
		IssuedAtUnixMS: now.Add(-5 * time.Second).UnixMilli(), ExpiresAtUnixMS: expiresAt.UnixMilli(),
	}
	placeholder, err := issuer.signer.Sign(claims)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		ManagedLarkApplicationIDEnvironment:    issuer.applicationID,
		ManagedLarkUserAccessTokenEnvironment:  placeholder,
		ManagedLarkNoUpdateNotifierEnvironment: "1",
		ManagedLarkNoSkillsNotifierEnvironment: "1",
		ManagedLarkPathEnvironment:             ManagedLarkPathValue,
	}, nil
}

var _ ManagedProcessEnvironmentIssuer = (*SignedManagedLarkEnvironmentIssuer)(nil)
var _ ManagedLarkEgressAuthoritySource = (*FrozenManagedLarkEgressAuthoritySource)(nil)
