package executorgateway

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/egresscapability"
	"github.com/agentserver/agentserver/v2/internal/managedcredential"
)

// WorkspaceManagedLarkEnvironmentIssuer resolves the delivery mode from Core
// for every exact TAE lark-cli process_start. There is deliberately no
// deployment default and no cross-mode fallback: the workspace row is the
// sole mode authority at the operation boundary.
type WorkspaceManagedLarkEnvironmentIssuer struct {
	placeholder *SignedManagedLarkEnvironmentIssuer
	credentials ManagedLarkProcessCredentialSource
	taePSM      string
}

func NewWorkspaceManagedLarkEnvironmentIssuer(
	signer *egresscapability.Signer,
	authorities ManagedLarkEgressAuthoritySource,
	credentials ManagedLarkProcessCredentialSource,
	applicationID, taePSM string,
	idGenerator IDGenerator,
	now func() time.Time,
	ttl time.Duration,
) (*WorkspaceManagedLarkEnvironmentIssuer, error) {
	if credentials == nil || !managedLarkApplicationIDPattern.MatchString(taePSM) {
		return nil, errors.New("workspace managed Lark credential source or TAE PSM is invalid")
	}
	placeholder, err := NewSignedManagedLarkEnvironmentIssuer(signer, authorities, applicationID, idGenerator, now, ttl)
	if err != nil {
		return nil, err
	}
	return &WorkspaceManagedLarkEnvironmentIssuer{placeholder: placeholder, credentials: credentials, taePSM: taePSM}, nil
}

func NewDefaultWorkspaceManagedLarkEnvironmentIssuer(
	signer *egresscapability.Signer,
	authorities ManagedLarkEgressAuthoritySource,
	credentials ManagedLarkProcessCredentialSource,
	applicationID, taePSM string,
) (*WorkspaceManagedLarkEnvironmentIssuer, error) {
	return NewWorkspaceManagedLarkEnvironmentIssuer(
		signer, authorities, credentials, applicationID, taePSM,
		newRandomUUID, time.Now, 60*time.Second,
	)
}

func (issuer *WorkspaceManagedLarkEnvironmentIssuer) IssueManagedProcessEnvironment(
	ctx context.Context,
	request ManagedProcessEnvironmentRequest,
) (map[string]string, error) {
	if issuer == nil || issuer.placeholder == nil || issuer.credentials == nil || ctx == nil {
		return nil, errors.New("workspace managed Lark environment issuer and context are required")
	}
	applies, err := validateManagedLarkProcessRequest(request)
	if err != nil {
		return nil, err
	}
	if !applies {
		return map[string]string{}, nil
	}
	authority, err := issuer.placeholder.authorities.ResolveManagedLarkEgressAuthority(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace managed Lark mode: %w", err)
	}
	if err := authority.Validate(); err != nil {
		return nil, err
	}
	switch authority.CredentialMode {
	case managedcredential.ModeWebhookSwap:
		return issuer.placeholder.issueWithAuthority(request, authority)
	case managedcredential.ModeProcessEnv:
		return issuer.issueProcessEnvironment(ctx, request, authority)
	default:
		return nil, errors.New("Core returned an unknown workspace managed Lark mode")
	}
}

func (issuer *WorkspaceManagedLarkEnvironmentIssuer) issueProcessEnvironment(
	ctx context.Context,
	request ManagedProcessEnvironmentRequest,
	authority ManagedLarkEgressAuthority,
) (map[string]string, error) {
	environment := managedLarkBaseEnvironment(issuer.placeholder.applicationID)
	if authority.BindingID == "" {
		return environment, nil
	}
	credential, err := issuer.credentials.ResolveManagedLarkProcessCredential(ctx, request, issuer.taePSM, authority)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace managed Lark process credential: %w", err)
	}
	if !credential.Configured || credential.CredentialMode != managedcredential.ModeProcessEnv ||
		credential.BindingID != authority.BindingID || credential.AuthorityVersion != authority.AuthorityVersion ||
		credential.CredentialVersion != authority.CredentialVersion || credential.PolicySHA256 != authority.PolicySHA256 ||
		credential.TAEPSM != issuer.taePSM || credential.AccessToken == "" || len(credential.AccessToken) > 32*1024 ||
		strings.TrimSpace(credential.AccessToken) != credential.AccessToken || strings.ContainsAny(credential.AccessToken, "\x00\r\n") {
		return nil, errors.New("Core returned an inconsistent workspace managed Lark process credential")
	}
	capabilityID, err := issuer.placeholder.idGenerator()
	if err != nil {
		return nil, fmt.Errorf("allocate managed Lark process proof identity: %w", err)
	}
	if err := validateRegistryIdentity("managed Lark process proof ID", capabilityID); err != nil {
		return nil, err
	}
	principal := request.Principal
	now := issuer.placeholder.now().UTC().Truncate(time.Millisecond)
	expiresAt := now.Add(issuer.placeholder.ttl)
	if principal.RunDeadline.Before(expiresAt) {
		expiresAt = principal.RunDeadline.UTC().Truncate(time.Millisecond)
	}
	if principal.CapabilityExpiresAt.Before(expiresAt) {
		expiresAt = principal.CapabilityExpiresAt.UTC().Truncate(time.Millisecond)
	}
	if credential.AccessExpiresAt != nil && credential.AccessExpiresAt.Before(expiresAt) {
		expiresAt = credential.AccessExpiresAt.UTC().Truncate(time.Millisecond)
	}
	if !expiresAt.After(now.Add(time.Second)) {
		return nil, errors.New("managed Lark process proof has no safe remaining authority window")
	}
	proof, err := issuer.placeholder.signer.SignProcessEnvironment(egresscapability.ProcessEnvironmentClaims{
		Version: egresscapability.ProcessEnvironmentVersion, Issuer: issuer.placeholder.signer.Issuer(),
		CapabilityID: capabilityID, WorkspaceID: principal.WorkspaceID, SessionID: principal.SessionID,
		ActorID: principal.ActorID, EnvironmentID: request.Target.EnvironmentID, RunID: principal.Run.RunID,
		RunAttemptID: principal.Run.RunAttemptID, RunAttemptGeneration: principal.Run.RunAttemptGeneration,
		ExecutionID: request.Operation.ExecutionID, OperationID: request.Operation.OperationID,
		SandboxID: request.Target.ID, TargetGeneration: request.Target.Generation,
		ProviderKind: "lark", BindingID: authority.BindingID, AuthorityVersion: authority.AuthorityVersion,
		CredentialVersion: authority.CredentialVersion, PolicySHA256: authority.PolicySHA256,
		IssuedAtUnixMS: now.Add(-5 * time.Second).UnixMilli(), ExpiresAtUnixMS: expiresAt.UnixMilli(),
	})
	if err != nil {
		return nil, err
	}
	environment[ManagedLarkUserAccessTokenEnvironment] = credential.AccessToken
	environment[ManagedLarkAgentTraceEnvironment] = proof
	return environment, nil
}

var _ ManagedProcessEnvironmentIssuer = (*WorkspaceManagedLarkEnvironmentIssuer)(nil)
