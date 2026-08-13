package executorgateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	logger      *slog.Logger
}

func NewWorkspaceManagedLarkEnvironmentIssuer(
	signer *egresscapability.Signer,
	authorities ManagedLarkEgressAuthoritySource,
	credentials ManagedLarkProcessCredentialSource,
	taePSM string,
	idGenerator IDGenerator,
	now func() time.Time,
	ttl time.Duration,
	logger *slog.Logger,
) (*WorkspaceManagedLarkEnvironmentIssuer, error) {
	if credentials == nil || !managedLarkApplicationIDPattern.MatchString(taePSM) {
		return nil, errors.New("workspace managed Lark credential source or TAE PSM is invalid")
	}
	placeholder, err := NewSignedManagedLarkEnvironmentIssuer(signer, authorities, idGenerator, now, ttl)
	if err != nil {
		return nil, err
	}
	return &WorkspaceManagedLarkEnvironmentIssuer{
		placeholder: placeholder, credentials: credentials, taePSM: taePSM, logger: logger,
	}, nil
}

func NewDefaultWorkspaceManagedLarkEnvironmentIssuer(
	signer *egresscapability.Signer,
	authorities ManagedLarkEgressAuthoritySource,
	credentials ManagedLarkProcessCredentialSource,
	taePSM string,
	logger *slog.Logger,
) (*WorkspaceManagedLarkEnvironmentIssuer, error) {
	return NewWorkspaceManagedLarkEnvironmentIssuer(
		signer, authorities, credentials, taePSM,
		newRandomUUID, time.Now, 60*time.Second, logger,
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
	authorityStartedAt := time.Now()
	authority, err := issuer.placeholder.authorities.ResolveManagedLarkEgressAuthority(ctx, request)
	if err != nil {
		issuer.logStage(ctx, request, "authority_resolve", "failed", authorityStartedAt, err, ManagedLarkEgressAuthority{})
		return nil, fmt.Errorf("resolve workspace managed Lark mode: %w", err)
	}
	if err := authority.Validate(); err != nil {
		issuer.logStage(ctx, request, "authority_resolve", "failed", authorityStartedAt, err, authority)
		return nil, err
	}
	issuer.logStage(ctx, request, "authority_resolve", "succeeded", authorityStartedAt, nil, authority)
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
	if authority.BindingID == "" {
		issuer.logStage(ctx, request, "credential_resolve", "skipped", time.Now(), nil, authority)
		return managedLarkBaseEnvironment(""), nil
	}
	credentialStartedAt := time.Now()
	credential, err := issuer.credentials.ResolveManagedLarkProcessCredential(ctx, request, issuer.taePSM, authority)
	if err != nil {
		issuer.logStage(ctx, request, "credential_resolve", "failed", credentialStartedAt, err, authority)
		return nil, fmt.Errorf("resolve workspace managed Lark process credential: %w", err)
	}
	if !credential.Configured || credential.CredentialMode != managedcredential.ModeProcessEnv ||
		credential.ApplicationID != authority.ApplicationID || !managedLarkApplicationIDPattern.MatchString(credential.ApplicationID) ||
		credential.BindingID != authority.BindingID || credential.AuthorityVersion != authority.AuthorityVersion ||
		credential.CredentialVersion != authority.CredentialVersion || credential.PolicySHA256 != authority.PolicySHA256 ||
		credential.TAEPSM != issuer.taePSM || credential.AccessToken == "" || len(credential.AccessToken) > 32*1024 ||
		strings.TrimSpace(credential.AccessToken) != credential.AccessToken || strings.ContainsAny(credential.AccessToken, "\x00\r\n") {
		err := errors.New("Core returned an inconsistent workspace managed Lark process credential")
		issuer.logStage(ctx, request, "credential_resolve", "failed", credentialStartedAt, err, authority)
		return nil, err
	}
	issuer.logStage(ctx, request, "credential_resolve", "succeeded", credentialStartedAt, nil, authority)
	environment := managedLarkBaseEnvironment(credential.ApplicationID)
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

func (issuer *WorkspaceManagedLarkEnvironmentIssuer) logStage(
	ctx context.Context,
	request ManagedProcessEnvironmentRequest,
	stage, status string,
	startedAt time.Time,
	err error,
	authority ManagedLarkEgressAuthority,
) {
	if issuer == nil || issuer.logger == nil {
		return
	}
	reasonCode, coreHTTPStatus := managedExecutionErrorMetadata(err)
	contextState, deadlineRemainingMillis := managedContextMetadata(ctx)
	level := slog.LevelInfo
	if err != nil {
		level = slog.LevelError
	}
	issuer.logger.Log(ctx, level, "managed Lark process environment stage",
		"stage", stage,
		"status", status,
		"workspace_id", request.Principal.WorkspaceID,
		"session_id", request.Principal.SessionID,
		"run_id", request.Operation.RunID,
		"run_attempt_id", request.Operation.RunAttemptID,
		"execution_id", request.Operation.ExecutionID,
		"operation_id", request.Operation.OperationID,
		"target_id", request.Target.ID,
		"target_generation", request.Target.Generation,
		"credential_mode", authority.CredentialMode,
		"binding_configured", authority.BindingID != "",
		"authority_version", authority.AuthorityVersion,
		"credential_version", authority.CredentialVersion,
		"reason_code", reasonCode,
		"core_http_status", coreHTTPStatus,
		"context_state", contextState,
		"deadline_remaining_ms", deadlineRemainingMillis,
		"elapsed_ms", time.Since(startedAt).Milliseconds(),
	)
}

var _ ManagedProcessEnvironmentIssuer = (*WorkspaceManagedLarkEnvironmentIssuer)(nil)
