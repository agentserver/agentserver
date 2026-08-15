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

// WorkspaceManagedEnvironmentIssuer resolves the delivery mode from Core for
// every exact managed CLI process_start. There is deliberately no
// deployment default and no cross-mode fallback: the workspace row is the
// sole mode authority at the operation boundary.
type WorkspaceManagedEnvironmentIssuer struct {
	placeholder *SignedManagedLarkEnvironmentIssuer
	authorities ManagedCredentialAuthoritySource
	credentials ManagedProcessCredentialSource
	taePSM      string
	logger      *slog.Logger
}

func NewWorkspaceManagedEnvironmentIssuer(
	signer *egresscapability.Signer,
	authorities ManagedCredentialAuthoritySource,
	credentials ManagedProcessCredentialSource,
	taePSM string,
	idGenerator IDGenerator,
	now func() time.Time,
	ttl time.Duration,
	logger *slog.Logger,
) (*WorkspaceManagedEnvironmentIssuer, error) {
	if credentials == nil || !managedLarkApplicationIDPattern.MatchString(taePSM) {
		return nil, errors.New("workspace managed credential source or TAE PSM is invalid")
	}
	placeholder, err := NewSignedManagedLarkEnvironmentIssuer(signer, authorities, idGenerator, now, ttl)
	if err != nil {
		return nil, err
	}
	return &WorkspaceManagedEnvironmentIssuer{
		placeholder: placeholder, authorities: authorities, credentials: credentials, taePSM: taePSM, logger: logger,
	}, nil
}

// NewDirectWorkspaceManagedEnvironmentIssuer configures the exact
// process_env delivery path without installing placeholder signing authority.
// A workspace that selects webhook_swap fails closed and must be routed to a
// separate webhook-enabled Sandbox profile.
func NewDirectWorkspaceManagedEnvironmentIssuer(
	authorities ManagedCredentialAuthoritySource,
	credentials ManagedProcessCredentialSource,
	taePSM string,
	logger *slog.Logger,
) (*WorkspaceManagedEnvironmentIssuer, error) {
	if authorities == nil || credentials == nil || !managedLarkApplicationIDPattern.MatchString(taePSM) {
		return nil, errors.New("direct workspace managed authority, credential source, or TAE PSM is invalid")
	}
	return &WorkspaceManagedEnvironmentIssuer{
		authorities: authorities, credentials: credentials, taePSM: taePSM, logger: logger,
	}, nil
}

func NewDefaultWorkspaceManagedEnvironmentIssuer(
	signer *egresscapability.Signer,
	authorities ManagedCredentialAuthoritySource,
	credentials ManagedProcessCredentialSource,
	taePSM string,
	logger *slog.Logger,
) (*WorkspaceManagedEnvironmentIssuer, error) {
	return NewWorkspaceManagedEnvironmentIssuer(
		signer, authorities, credentials, taePSM,
		newRandomUUID, time.Now, 60*time.Second, logger,
	)
}

func (issuer *WorkspaceManagedEnvironmentIssuer) IssueManagedProcessEnvironment(
	ctx context.Context,
	request ManagedProcessEnvironmentRequest,
) (map[string]string, error) {
	if issuer == nil || issuer.authorities == nil || issuer.credentials == nil || ctx == nil {
		return nil, errors.New("workspace managed environment issuer and context are required")
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
	authorityStartedAt := time.Now()
	authority, err := issuer.authorities.ResolveManagedCredentialAuthority(ctx, request)
	if err != nil {
		issuer.logStage(ctx, request, tool, "authority_resolve", "failed", authorityStartedAt, err, ManagedCredentialAuthority{})
		return nil, fmt.Errorf("resolve workspace managed credential mode: %w", err)
	}
	if err := authority.Validate(); err != nil || authority.ProviderKind != tool.ProviderKind || authority.PolicySHA256 != tool.PolicySHA256 {
		if err == nil {
			err = errors.New("Core returned credential authority for a different managed tool")
		}
		issuer.logStage(ctx, request, tool, "authority_resolve", "failed", authorityStartedAt, err, authority)
		return nil, err
	}
	issuer.logStage(ctx, request, tool, "authority_resolve", "succeeded", authorityStartedAt, nil, authority)
	switch authority.CredentialMode {
	case managedcredential.ModeWebhookSwap:
		if issuer.placeholder == nil || !tool.WebhookSupported {
			return nil, errors.New("webhook_swap workspace requires a webhook-enabled TAE Sandbox profile")
		}
		return issuer.placeholder.issueWithAuthority(request, authority)
	case managedcredential.ModeProcessEnv:
		return issuer.issueProcessEnvironment(ctx, request, tool, authority)
	default:
		return nil, errors.New("Core returned an unknown workspace managed credential mode")
	}
}

func (issuer *WorkspaceManagedEnvironmentIssuer) issueProcessEnvironment(
	ctx context.Context,
	request ManagedProcessEnvironmentRequest,
	tool managedProcessTool,
	authority ManagedCredentialAuthority,
) (map[string]string, error) {
	if authority.BindingID == "" {
		if tool.ProviderKind != "lark" {
			err := fmt.Errorf("%w: workspace has no active ByteCloud credential for managed bkectl", errManagedCredentialNotConfigured)
			issuer.logStage(ctx, request, tool, "credential_resolve", "failed", time.Now(), err, authority)
			return nil, err
		}
		issuer.logStage(ctx, request, tool, "credential_resolve", "skipped", time.Now(), nil, authority)
		return managedToolBaseEnvironment(tool, ""), nil
	}
	credentialStartedAt := time.Now()
	credential, err := issuer.credentials.ResolveManagedProcessCredential(ctx, request, issuer.taePSM, authority)
	if err != nil {
		issuer.logStage(ctx, request, tool, "credential_resolve", "failed", credentialStartedAt, err, authority)
		return nil, fmt.Errorf("resolve workspace managed process credential: %w", err)
	}
	if !credential.Configured || credential.CredentialMode != managedcredential.ModeProcessEnv ||
		credential.ProviderKind != tool.ProviderKind || credential.ApplicationID != authority.ApplicationID ||
		credential.BindingID != authority.BindingID || credential.AuthorityVersion != authority.AuthorityVersion ||
		credential.CredentialVersion != authority.CredentialVersion || credential.PolicySHA256 != authority.PolicySHA256 ||
		credential.TAEPSM != issuer.taePSM || credential.Credential == "" || len(credential.Credential) > 32*1024 ||
		strings.TrimSpace(credential.Credential) != credential.Credential || strings.ContainsAny(credential.Credential, " \t\x00\r\n") ||
		(tool.ProviderKind == "lark" && !managedLarkApplicationIDPattern.MatchString(credential.ApplicationID)) {
		err := errors.New("Core returned an inconsistent workspace managed process credential")
		issuer.logStage(ctx, request, tool, "credential_resolve", "failed", credentialStartedAt, err, authority)
		return nil, err
	}
	issuer.logStage(ctx, request, tool, "credential_resolve", "succeeded", credentialStartedAt, nil, authority)
	environment := managedToolBaseEnvironment(tool, credential.ApplicationID)
	environment[tool.CredentialEnvironment] = credential.Credential
	return environment, nil
}

func (issuer *WorkspaceManagedEnvironmentIssuer) logStage(
	ctx context.Context,
	request ManagedProcessEnvironmentRequest,
	tool managedProcessTool,
	stage, status string,
	startedAt time.Time,
	err error,
	authority ManagedCredentialAuthority,
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
	issuer.logger.Log(ctx, level, "managed process environment stage",
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
		"executable", tool.Executable,
		"provider_kind", tool.ProviderKind,
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

var _ ManagedProcessEnvironmentIssuer = (*WorkspaceManagedEnvironmentIssuer)(nil)
