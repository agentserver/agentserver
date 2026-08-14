package executorgateway

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/managedcredential"
)

// ManagedProcessCredential is the version-fenced material returned by
// Core only after a workspace has explicitly selected process_env. It is
// consumed immediately while constructing one managed CLI process environment.
type ManagedProcessCredential struct {
	Configured        bool
	CredentialMode    string
	ProviderKind      string
	Credential        string
	ApplicationID     string
	BindingID         string
	AuthorityVersion  int64
	CredentialVersion int64
	PolicySHA256      string
	TAEPSM            string
	ResolvedAt        time.Time
	AccessExpiresAt   *time.Time
}

type ManagedProcessCredentialSource interface {
	ResolveManagedProcessCredential(context.Context, ManagedProcessEnvironmentRequest, string, ManagedCredentialAuthority) (ManagedProcessCredential, error)
}

func (client *CoreConnectionClient) ResolveManagedProcessCredential(
	ctx context.Context,
	request ManagedProcessEnvironmentRequest,
	taePSM string,
	authority ManagedCredentialAuthority,
) (ManagedProcessCredential, error) {
	if client == nil || client.authorizationNow == nil || ctx == nil {
		return ManagedProcessCredential{}, errors.New("Core managed process credential client and context are required")
	}
	tool, credentialRequired, applies, err := validateManagedProcessRequest(request)
	if err != nil || !applies || !credentialRequired {
		if err != nil {
			return ManagedProcessCredential{}, err
		}
		return ManagedProcessCredential{}, errors.New("managed process credential request does not require a credential")
	}
	if err := authority.Validate(); err != nil || authority.CredentialMode != managedcredential.ModeProcessEnv || authority.BindingID == "" {
		return ManagedProcessCredential{}, errors.New("managed process credential authority is invalid")
	}
	if authority.ProviderKind != tool.ProviderKind || authority.PolicySHA256 != tool.PolicySHA256 {
		return ManagedProcessCredential{}, errors.New("managed process credential authority differs from the executable policy")
	}
	principal := request.Principal
	command := corecontract.ResolveExecutionCredentialRequest{
		Operation: corecontract.EgressCredentialOperation{
			WorkspaceID: principal.WorkspaceID, SessionID: principal.SessionID, ActorID: principal.ActorID,
			EnvironmentID: request.Target.EnvironmentID, RunID: principal.Run.RunID,
			RunAttemptID: principal.Run.RunAttemptID, RunAttemptGeneration: principal.Run.RunAttemptGeneration,
			ExecutionID: request.Operation.ExecutionID, OperationID: request.Operation.OperationID,
			SandboxID: request.Target.ID, TargetGeneration: request.Target.Generation,
		},
		TAEPSM: taePSM, PolicySHA256: tool.PolicySHA256, ProviderKind: tool.ProviderKind,
		ToolName: request.ToolName, Executable: request.Executable, Arguments: append([]string(nil), request.Arguments...),
		BindingID: authority.BindingID, AuthorityVersion: authority.AuthorityVersion,
		CredentialVersion: authority.CredentialVersion,
	}
	var response corecontract.ResolveExecutionCredentialResponse
	if err := client.postWithPolicy(
		ctx, corecontract.ResolveExecutionCredentialPath, command, &response,
		http.StatusOK, "", true, nil,
	); err != nil {
		return ManagedProcessCredential{}, err
	}
	if response.ProviderKind != tool.ProviderKind || response.PolicySHA256 != tool.PolicySHA256 || response.TAEPSM != taePSM ||
		response.CredentialMode != authority.CredentialMode ||
		response.ResolvedAt.IsZero() || response.ResolvedAt.After(client.authorizationNow().UTC().Add(5*time.Second)) {
		return ManagedProcessCredential{}, errors.New("Core returned an invalid managed process credential scope")
	}
	if !response.Configured {
		if response.Credential != "" || response.ApplicationID != "" || response.BindingID != "" || response.AuthorityVersion != 0 || response.CredentialVersion != 0 || response.AccessExpiresAt != nil {
			return ManagedProcessCredential{}, errors.New("Core returned a partial unconfigured managed process credential")
		}
		return ManagedProcessCredential{
			ProviderKind:   response.ProviderKind,
			CredentialMode: response.CredentialMode,
			PolicySHA256:   tool.PolicySHA256, TAEPSM: taePSM, ResolvedAt: response.ResolvedAt,
		}, nil
	}
	applicationValid := response.ApplicationID == ""
	if tool.ProviderKind == "lark" {
		applicationValid = response.ApplicationID == authority.ApplicationID &&
			managedLarkApplicationIDPattern.MatchString(response.ApplicationID)
	}
	if response.BindingID == "" || response.AuthorityVersion < 1 || response.CredentialVersion < 1 ||
		!applicationValid ||
		response.BindingID != authority.BindingID || response.AuthorityVersion != authority.AuthorityVersion ||
		response.CredentialVersion != authority.CredentialVersion ||
		response.Credential == "" || len(response.Credential) > 32*1024 ||
		strings.TrimSpace(response.Credential) != response.Credential || strings.ContainsAny(response.Credential, " \t\x00\r\n") ||
		(response.AccessExpiresAt != nil && !response.AccessExpiresAt.After(client.authorizationNow().UTC().Add(time.Second))) {
		return ManagedProcessCredential{}, errors.New("Core returned invalid managed process credential material")
	}
	return ManagedProcessCredential{
		Configured: true, CredentialMode: response.CredentialMode,
		ProviderKind: response.ProviderKind, Credential: response.Credential,
		ApplicationID: response.ApplicationID, BindingID: response.BindingID,
		AuthorityVersion: response.AuthorityVersion, CredentialVersion: response.CredentialVersion,
		PolicySHA256: response.PolicySHA256, TAEPSM: response.TAEPSM, ResolvedAt: response.ResolvedAt,
		AccessExpiresAt: response.AccessExpiresAt,
	}, nil
}

var _ ManagedProcessCredentialSource = (*CoreConnectionClient)(nil)
