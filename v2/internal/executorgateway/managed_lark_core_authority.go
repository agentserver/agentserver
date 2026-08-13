package executorgateway

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/larkegresspolicy"
	"github.com/agentserver/agentserver/v2/internal/managedcredential"
)

func (client *CoreConnectionClient) ResolveManagedLarkEgressAuthority(
	ctx context.Context,
	request ManagedProcessEnvironmentRequest,
) (ManagedLarkEgressAuthority, error) {
	if client == nil || client.authorizationNow == nil || ctx == nil {
		return ManagedLarkEgressAuthority{}, errors.New("Core managed Lark authority client and context are required")
	}
	if err := validateExecutorMCPPrincipal(request.Principal); err != nil {
		return ManagedLarkEgressAuthority{}, err
	}
	if err := request.Target.Validate(); err != nil {
		return ManagedLarkEgressAuthority{}, err
	}
	if err := request.Operation.Validate(); err != nil {
		return ManagedLarkEgressAuthority{}, err
	}
	principal := request.Principal
	policySHA256 := larkegresspolicy.SHA256Hex()
	command := corecontract.ResolveEgressCredentialAuthorityRequest{
		Operation: corecontract.EgressCredentialOperation{
			WorkspaceID: principal.WorkspaceID, SessionID: principal.SessionID,
			ActorID: principal.ActorID, EnvironmentID: request.Target.EnvironmentID,
			RunID: principal.Run.RunID, RunAttemptID: principal.Run.RunAttemptID,
			RunAttemptGeneration: principal.Run.RunAttemptGeneration,
			ExecutionID:          request.Operation.ExecutionID, OperationID: request.Operation.OperationID,
			SandboxID: request.Target.ID, TargetGeneration: request.Target.Generation,
		},
		ProviderKind: "lark", PolicySHA256: policySHA256,
	}
	var response corecontract.ResolveEgressCredentialAuthorityResponse
	if err := client.postWithPolicy(
		ctx, corecontract.ResolveExecutionLarkCredentialAuthorityPath, command, &response,
		http.StatusOK, "", true, nil,
	); err != nil {
		return ManagedLarkEgressAuthority{}, err
	}
	if response.ProviderKind != "lark" || response.PolicySHA256 != policySHA256 || response.AuthorizedAt.IsZero() ||
		response.AuthorizedAt.After(client.authorizationNow().UTC().Add(5*time.Second)) ||
		!managedcredential.ValidMode(response.CredentialMode) {
		return ManagedLarkEgressAuthority{}, errors.New("Core returned invalid managed credential authority")
	}
	// No active workspace binding is a supported state. Return the policy
	// reference without a binding so the caller can project only non-secret
	// process settings and let the CLI report credential_not_configured.
	if response.BindingID == "" {
		if response.ApplicationID != "" || response.AuthorityVersion != 0 || response.CredentialVersion != 0 {
			return ManagedLarkEgressAuthority{}, errors.New("Core returned a partial empty credential authority")
		}
		return ManagedLarkEgressAuthority{CredentialMode: response.CredentialMode, PolicySHA256: policySHA256}, nil
	}
	authority := ManagedLarkEgressAuthority{
		CredentialMode: response.CredentialMode,
		ApplicationID:  response.ApplicationID,
		BindingID:      response.BindingID, AuthorityVersion: response.AuthorityVersion,
		CredentialVersion: response.CredentialVersion, PolicySHA256: response.PolicySHA256,
	}
	if err := authority.Validate(); err != nil {
		return ManagedLarkEgressAuthority{}, errors.New("Core returned invalid managed credential authority")
	}
	return authority, nil
}

var _ ManagedLarkEgressAuthoritySource = (*CoreConnectionClient)(nil)
