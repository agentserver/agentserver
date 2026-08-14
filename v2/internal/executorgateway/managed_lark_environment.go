package executorgateway

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/agentserver/agentserver/v2/internal/bkectlpolicy"
	"github.com/agentserver/agentserver/v2/internal/egresscapability"
	"github.com/agentserver/agentserver/v2/internal/executionbackend"
	"github.com/agentserver/agentserver/v2/internal/larkegresspolicy"
	"github.com/agentserver/agentserver/v2/internal/managedcredential"
)

var (
	managedLarkPolicyDigestPattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	managedLarkApplicationIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$`)
)

type ManagedCredentialAuthority struct {
	CredentialMode    string
	ProviderKind      string
	ApplicationID     string
	BindingID         string
	AuthorityVersion  int64
	CredentialVersion int64
	PolicySHA256      string
}

func (authority ManagedCredentialAuthority) Validate() error {
	if !managedcredential.ValidMode(authority.CredentialMode) || !managedLarkPolicyDigestPattern.MatchString(authority.PolicySHA256) {
		return errors.New("managed credential delivery mode or policy is invalid")
	}
	if authority.ProviderKind != "lark" && authority.ProviderKind != bkectlpolicy.CredentialKind {
		return errors.New("managed credential provider is invalid")
	}
	if authority.BindingID == "" {
		if authority.ApplicationID != "" || authority.AuthorityVersion != 0 || authority.CredentialVersion != 0 {
			return errors.New("managed empty credential authority is partial")
		}
		return nil
	}
	if authority.AuthorityVersion < 1 || authority.CredentialVersion < 1 {
		return errors.New("managed credential binding identity or authority version is invalid")
	}
	if authority.ProviderKind == "lark" && !managedLarkApplicationIDPattern.MatchString(authority.ApplicationID) {
		return errors.New("managed Lark credential application identity is invalid")
	}
	if authority.ProviderKind != "lark" && authority.ApplicationID != "" {
		return errors.New("managed non-Lark credential contains an application identity")
	}
	if err := validateRegistryIdentity("managed credential binding ID", authority.BindingID); err != nil {
		return err
	}
	return nil
}

type ManagedCredentialAuthoritySource interface {
	ResolveManagedCredentialAuthority(context.Context, ManagedProcessEnvironmentRequest) (ManagedCredentialAuthority, error)
}

// FrozenManagedCredentialAuthoritySource is a deterministic source for tests
// and already-resolved operation snapshots. Production process starts use the
// Core-backed source so the workspace mode is never frozen in deployment
// configuration. Revocation and live operation checks are repeated by Core
// before any real credential is used.
type FrozenManagedCredentialAuthoritySource struct {
	authority ManagedCredentialAuthority
}

func NewFrozenManagedCredentialAuthoritySource(authority ManagedCredentialAuthority) (*FrozenManagedCredentialAuthoritySource, error) {
	if err := authority.Validate(); err != nil {
		return nil, err
	}
	return &FrozenManagedCredentialAuthoritySource{authority: authority}, nil
}

func (source *FrozenManagedCredentialAuthoritySource) ResolveManagedCredentialAuthority(ctx context.Context, _ ManagedProcessEnvironmentRequest) (ManagedCredentialAuthority, error) {
	if source == nil || ctx == nil {
		return ManagedCredentialAuthority{}, errors.New("managed credential authority source is required")
	}
	if err := ctx.Err(); err != nil {
		return ManagedCredentialAuthority{}, err
	}
	return source.authority, nil
}

type SignedManagedLarkEnvironmentIssuer struct {
	signer      *egresscapability.Signer
	authorities ManagedCredentialAuthoritySource
	idGenerator IDGenerator
	now         func() time.Time
	ttl         time.Duration
}

func NewSignedManagedLarkEnvironmentIssuer(
	signer *egresscapability.Signer,
	authorities ManagedCredentialAuthoritySource,
	idGenerator IDGenerator,
	now func() time.Time,
	ttl time.Duration,
) (*SignedManagedLarkEnvironmentIssuer, error) {
	if signer == nil || authorities == nil || idGenerator == nil || now == nil {
		return nil, errors.New("managed Lark environment signer, authority source, identity generator, and clock are required")
	}
	if ttl < time.Second || ttl > 115*time.Second || ttl%time.Millisecond != 0 {
		return nil, errors.New("managed Lark placeholder TTL must be whole milliseconds between one and 115 seconds")
	}
	return &SignedManagedLarkEnvironmentIssuer{
		signer: signer, authorities: authorities,
		idGenerator: idGenerator, now: now, ttl: ttl,
	}, nil
}

func NewDefaultSignedManagedLarkEnvironmentIssuer(
	signer *egresscapability.Signer,
	authorities ManagedCredentialAuthoritySource,
) (*SignedManagedLarkEnvironmentIssuer, error) {
	return NewSignedManagedLarkEnvironmentIssuer(signer, authorities, newRandomUUID, time.Now, 60*time.Second)
}

func (issuer *SignedManagedLarkEnvironmentIssuer) IssueManagedProcessEnvironment(
	ctx context.Context,
	request ManagedProcessEnvironmentRequest,
) (map[string]string, error) {
	if issuer == nil || issuer.signer == nil || issuer.authorities == nil || issuer.idGenerator == nil || issuer.now == nil || ctx == nil {
		return nil, errors.New("managed Lark environment issuer and context are required")
	}
	tool, credentialRequired, applies, err := validateManagedProcessRequest(request)
	if err != nil {
		return nil, err
	}
	if !applies {
		return map[string]string{}, nil
	}
	if !credentialRequired {
		return managedDiscoveryEnvironment(), nil
	}
	if tool.ProviderKind != "lark" {
		return nil, errors.New("managed bkectl does not support webhook credential delivery")
	}
	authority, err := issuer.authorities.ResolveManagedCredentialAuthority(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("resolve managed Lark egress authority: %w", err)
	}
	return issueManagedLarkPlaceholder(issuer, request, authority)
}

func (issuer *SignedManagedLarkEnvironmentIssuer) issueWithAuthority(
	request ManagedProcessEnvironmentRequest,
	authority ManagedCredentialAuthority,
) (map[string]string, error) {
	tool, credentialRequired, applies, err := validateManagedProcessRequest(request)
	if err != nil || !applies || !credentialRequired || tool.ProviderKind != "lark" ||
		authority.ProviderKind != tool.ProviderKind || authority.PolicySHA256 != tool.PolicySHA256 {
		return nil, errors.New("managed Lark placeholder request or authority is invalid")
	}
	principal := request.Principal
	// No workspace binding is a valid state. Keep the non-sensitive runtime
	// projection (PATH and app hint) so lark-cli can report a normal
	// credential-not-configured error, but never mint a placeholder that could
	// be confused with a credential authority.
	if authority.BindingID == "" {
		return managedToolBaseEnvironment(tool, ""), nil
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
		AuthorityVersion: authority.AuthorityVersion, PolicySHA256: authority.PolicySHA256, Executable: tool.Executable,
		IssuedAtUnixMS: now.Add(-5 * time.Second).UnixMilli(), ExpiresAtUnixMS: expiresAt.UnixMilli(),
	}
	placeholder, err := issuer.signer.Sign(claims)
	if err != nil {
		return nil, err
	}
	environment := managedToolBaseEnvironment(tool, authority.ApplicationID)
	environment[ManagedLarkUserAccessTokenEnvironment] = placeholder
	return environment, nil
}

func issueManagedLarkPlaceholder(
	issuer *SignedManagedLarkEnvironmentIssuer,
	request ManagedProcessEnvironmentRequest,
	authority ManagedCredentialAuthority,
) (map[string]string, error) {
	if authority.CredentialMode != managedcredential.ModeWebhookSwap {
		return nil, errors.New("managed Lark placeholder requires webhook_swap workspace mode")
	}
	return issuer.issueWithAuthority(request, authority)
}

type managedProcessTool struct {
	Executable            string
	ProviderKind          string
	PolicySHA256          string
	CredentialEnvironment string
	WebhookSupported      bool
}

func validateManagedProcessRequest(request ManagedProcessEnvironmentRequest) (managedProcessTool, bool, bool, error) {
	if request.Target.Kind != executionbackend.KindTAE {
		// The issuer is installed on the unified execution gateway, so BYO
		// AgentX operations also pass this hook. They must remain byte-for-byte
		// unchanged and never receive managed credential material.
		return managedProcessTool{}, false, false, nil
	}
	if request.ToolName != "shell" {
		return managedProcessTool{}, false, false, nil
	}
	var (
		tool               managedProcessTool
		credentialRequired bool
	)
	switch request.Executable {
	case "lark-cli":
		tool = managedProcessTool{
			Executable: "lark-cli", ProviderKind: "lark", PolicySHA256: larkegresspolicy.SHA256Hex(),
			CredentialEnvironment: ManagedLarkUserAccessTokenEnvironment, WebhookSupported: true,
		}
		credentialRequired = true
	case bkectlpolicy.Executable:
		tool = managedProcessTool{
			Executable: bkectlpolicy.Executable, ProviderKind: bkectlpolicy.CredentialKind,
			PolicySHA256: bkectlpolicy.SHA256Hex(), CredentialEnvironment: ManagedBkectlJWTEnvironment,
		}
		var err error
		credentialRequired, err = bkectlpolicy.CredentialRequired(request.Arguments)
		if err != nil {
			return managedProcessTool{}, false, true, err
		}
	default:
		return managedProcessTool{}, false, false, nil
	}
	if err := validateExecutorMCPPrincipal(request.Principal); err != nil {
		return managedProcessTool{}, false, true, err
	}
	if err := request.Target.Validate(); err != nil {
		return managedProcessTool{}, false, true, err
	}
	if err := request.Operation.Validate(); err != nil {
		return managedProcessTool{}, false, true, err
	}
	principal := request.Principal
	if request.Operation.WorkspaceID != principal.WorkspaceID || request.Operation.SessionID != principal.SessionID ||
		request.Operation.RunID != principal.Run.RunID || request.Operation.RunAttemptID != principal.Run.RunAttemptID ||
		request.Operation.RunAttemptGeneration != principal.Run.RunAttemptGeneration || request.Target.EnvironmentID == "" {
		return managedProcessTool{}, false, true, errors.New("managed credential operation differs from the MCP principal")
	}
	return tool, credentialRequired, true, nil
}

func managedToolBaseEnvironment(tool managedProcessTool, applicationID string) map[string]string {
	environment := map[string]string{
		ManagedToolPathEnvironment: ManagedToolPathValue,
	}
	switch tool.ProviderKind {
	case "lark":
		environment[ManagedLarkNoUpdateNotifierEnvironment] = "1"
		environment[ManagedLarkNoSkillsNotifierEnvironment] = "1"
		if applicationID != "" {
			environment[ManagedLarkApplicationIDEnvironment] = applicationID
		}
	case bkectlpolicy.CredentialKind:
		environment[ManagedBkectlAuthModeEnvironment] = ManagedBkectlAuthModeValue
		environment[ManagedBkectlRegionEnvironment] = ManagedBkectlRegionValue
	}
	return environment
}

func managedDiscoveryEnvironment() map[string]string {
	return map[string]string{ManagedToolPathEnvironment: ManagedToolPathValue}
}

var _ ManagedProcessEnvironmentIssuer = (*SignedManagedLarkEnvironmentIssuer)(nil)
var _ ManagedCredentialAuthoritySource = (*FrozenManagedCredentialAuthoritySource)(nil)
