package executorgateway

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/larkegresspolicy"
	"github.com/agentserver/agentserver/v2/internal/managedcredential"
)

// ManagedLarkProcessCredential is the version-fenced material returned by
// Core only after a workspace has explicitly selected process_env. It is
// consumed immediately while constructing one lark-cli process environment.
type ManagedLarkProcessCredential struct {
	Configured        bool
	CredentialMode    string
	AccessToken       string
	BindingID         string
	AuthorityVersion  int64
	CredentialVersion int64
	PolicySHA256      string
	TAEPSM            string
	ResolvedAt        time.Time
	AccessExpiresAt   *time.Time
}

type ManagedLarkProcessCredentialSource interface {
	ResolveManagedLarkProcessCredential(context.Context, ManagedProcessEnvironmentRequest, string, ManagedLarkEgressAuthority) (ManagedLarkProcessCredential, error)
}

func (client *CoreConnectionClient) ResolveManagedLarkProcessCredential(
	ctx context.Context,
	request ManagedProcessEnvironmentRequest,
	taePSM string,
	authority ManagedLarkEgressAuthority,
) (ManagedLarkProcessCredential, error) {
	if client == nil || client.authorizationNow == nil || ctx == nil {
		return ManagedLarkProcessCredential{}, errors.New("Core managed Lark process credential client and context are required")
	}
	if applies, err := validateManagedLarkProcessRequest(request); err != nil || !applies {
		if err != nil {
			return ManagedLarkProcessCredential{}, err
		}
		return ManagedLarkProcessCredential{}, errors.New("managed Lark process credential request is not a lark-cli launch")
	}
	if err := authority.Validate(); err != nil || authority.CredentialMode != managedcredential.ModeProcessEnv || authority.BindingID == "" {
		return ManagedLarkProcessCredential{}, errors.New("managed Lark process credential authority is invalid")
	}
	policySHA256 := larkegresspolicy.SHA256Hex()
	principal := request.Principal
	command := corecontract.ResolveExecutionLarkCredentialRequest{
		Operation: corecontract.EgressCredentialOperation{
			WorkspaceID: principal.WorkspaceID, SessionID: principal.SessionID, ActorID: principal.ActorID,
			EnvironmentID: request.Target.EnvironmentID, RunID: principal.Run.RunID,
			RunAttemptID: principal.Run.RunAttemptID, RunAttemptGeneration: principal.Run.RunAttemptGeneration,
			ExecutionID: request.Operation.ExecutionID, OperationID: request.Operation.OperationID,
			SandboxID: request.Target.ID, TargetGeneration: request.Target.Generation,
		},
		TAEPSM: taePSM, PolicySHA256: policySHA256,
		ToolName: request.ToolName, Executable: request.Executable,
		BindingID: authority.BindingID, AuthorityVersion: authority.AuthorityVersion,
		CredentialVersion: authority.CredentialVersion,
	}
	var response corecontract.ResolveExecutionLarkCredentialResponse
	if err := client.postWithPolicy(
		ctx, corecontract.ResolveExecutionLarkCredentialPath, command, &response,
		http.StatusOK, "", true, nil,
	); err != nil {
		return ManagedLarkProcessCredential{}, err
	}
	if response.ProviderKind != "lark" || response.PolicySHA256 != policySHA256 || response.TAEPSM != taePSM ||
		response.CredentialMode != authority.CredentialMode ||
		response.ResolvedAt.IsZero() || response.ResolvedAt.After(client.authorizationNow().UTC().Add(5*time.Second)) {
		return ManagedLarkProcessCredential{}, errors.New("Core returned an invalid managed Lark process credential scope")
	}
	if !response.Configured {
		if response.AccessToken != "" || response.BindingID != "" || response.AuthorityVersion != 0 || response.CredentialVersion != 0 || response.AccessExpiresAt != nil {
			return ManagedLarkProcessCredential{}, errors.New("Core returned a partial unconfigured managed Lark process credential")
		}
		return ManagedLarkProcessCredential{
			CredentialMode: response.CredentialMode,
			PolicySHA256:   policySHA256, TAEPSM: taePSM, ResolvedAt: response.ResolvedAt,
		}, nil
	}
	if response.BindingID == "" || response.AuthorityVersion < 1 || response.CredentialVersion < 1 ||
		response.BindingID != authority.BindingID || response.AuthorityVersion != authority.AuthorityVersion ||
		response.CredentialVersion != authority.CredentialVersion ||
		response.AccessToken == "" || len(response.AccessToken) > 32*1024 ||
		strings.TrimSpace(response.AccessToken) != response.AccessToken || strings.ContainsAny(response.AccessToken, "\x00\r\n") ||
		(response.AccessExpiresAt != nil && !response.AccessExpiresAt.After(client.authorizationNow().UTC().Add(time.Second))) {
		return ManagedLarkProcessCredential{}, errors.New("Core returned invalid managed Lark process credential material")
	}
	return ManagedLarkProcessCredential{
		Configured: true, CredentialMode: response.CredentialMode,
		AccessToken: response.AccessToken, BindingID: response.BindingID,
		AuthorityVersion: response.AuthorityVersion, CredentialVersion: response.CredentialVersion,
		PolicySHA256: response.PolicySHA256, TAEPSM: response.TAEPSM, ResolvedAt: response.ResolvedAt,
		AccessExpiresAt: response.AccessExpiresAt,
	}, nil
}

var _ ManagedLarkProcessCredentialSource = (*CoreConnectionClient)(nil)
