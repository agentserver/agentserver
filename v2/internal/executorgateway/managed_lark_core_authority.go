package executorgateway

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/managedcredential"
)

func (client *CoreConnectionClient) ResolveManagedCredentialAuthority(
	ctx context.Context,
	request ManagedProcessEnvironmentRequest,
) (ManagedCredentialAuthority, error) {
	if client == nil || client.authorizationNow == nil || ctx == nil {
		return ManagedCredentialAuthority{}, errors.New("Core managed credential authority client and context are required")
	}
	tool, credentialRequired, applies, err := validateManagedProcessRequest(request)
	if err != nil {
		return ManagedCredentialAuthority{}, err
	}
	if !applies || !credentialRequired {
		return ManagedCredentialAuthority{}, errors.New("Core managed credential authority request does not require a credential")
	}
	if err := validateExecutorMCPPrincipal(request.Principal); err != nil {
		return ManagedCredentialAuthority{}, err
	}
	if err := request.Target.Validate(); err != nil {
		return ManagedCredentialAuthority{}, err
	}
	if err := request.Operation.Validate(); err != nil {
		return ManagedCredentialAuthority{}, err
	}
	principal := request.Principal
	command := corecontract.ResolveEgressCredentialAuthorityRequest{
		Operation: corecontract.EgressCredentialOperation{
			WorkspaceID: principal.WorkspaceID, SessionID: principal.SessionID,
			ActorID: principal.ActorID, EnvironmentID: request.Target.EnvironmentID,
			RunID: principal.Run.RunID, RunAttemptID: principal.Run.RunAttemptID,
			RunAttemptGeneration: principal.Run.RunAttemptGeneration,
			ExecutionID:          request.Operation.ExecutionID, OperationID: request.Operation.OperationID,
			SandboxID: request.Target.ID, TargetGeneration: request.Target.Generation,
		},
		ProviderKind: tool.ProviderKind, PolicySHA256: tool.PolicySHA256,
	}
	var response corecontract.ResolveEgressCredentialAuthorityResponse
	if err := client.postWithPolicy(
		ctx, corecontract.ResolveExecutionCredentialAuthorityPath, command, &response,
		http.StatusOK, "", true, nil,
	); err != nil {
		return ManagedCredentialAuthority{}, err
	}
	if response.ProviderKind != tool.ProviderKind || response.PolicySHA256 != tool.PolicySHA256 || response.AuthorizedAt.IsZero() ||
		response.AuthorizedAt.After(client.authorizationNow().UTC().Add(5*time.Second)) ||
		!managedcredential.ValidMode(response.CredentialMode) {
		return ManagedCredentialAuthority{}, errors.New("Core returned invalid managed credential authority")
	}
	// No active workspace binding is a supported state. Return the policy
	// reference without a binding so the caller can project only non-secret
	// process settings and let the CLI report credential_not_configured.
	if response.BindingID == "" {
		if response.ApplicationID != "" || response.AuthorityVersion != 0 || response.CredentialVersion != 0 {
			return ManagedCredentialAuthority{}, errors.New("Core returned a partial empty credential authority")
		}
		return ManagedCredentialAuthority{
			CredentialMode: response.CredentialMode, ProviderKind: tool.ProviderKind, PolicySHA256: tool.PolicySHA256,
		}, nil
	}
	authority := ManagedCredentialAuthority{
		CredentialMode: response.CredentialMode,
		ProviderKind:   response.ProviderKind,
		ApplicationID:  response.ApplicationID,
		BindingID:      response.BindingID, AuthorityVersion: response.AuthorityVersion,
		CredentialVersion: response.CredentialVersion, PolicySHA256: response.PolicySHA256,
	}
	if err := authority.Validate(); err != nil {
		return ManagedCredentialAuthority{}, errors.New("Core returned invalid managed credential authority")
	}
	return authority, nil
}

var _ ManagedCredentialAuthoritySource = (*CoreConnectionClient)(nil)
