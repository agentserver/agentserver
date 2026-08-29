package sandboxgateway

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/agentserver/agentserver/v2/internal/sandboxcapability"
)

type CapabilityAuthorizer struct {
	verifier *sandboxcapability.Verifier
	now      func() time.Time
}

func NewCapabilityAuthorizer(verifier *sandboxcapability.Verifier, now func() time.Time) (*CapabilityAuthorizer, error) {
	if verifier == nil || now == nil {
		return nil, errors.New("sandbox capability verifier and clock are required")
	}
	return &CapabilityAuthorizer{verifier: verifier, now: now}, nil
}

func (authorizer *CapabilityAuthorizer) Authorize(request *http.Request, action string) (Principal, error) {
	if authorizer == nil || authorizer.verifier == nil || authorizer.now == nil || request == nil || request.URL == nil {
		return Principal{}, errors.New("sandbox capability authorizer and request are required")
	}
	if request.URL.RawQuery != "" || request.URL.ForceQuery || request.URL.Fragment != "" || request.URL.RawPath != "" {
		return Principal{}, errors.New("sandbox capability request URL is not canonical")
	}
	if request.Method != actionMethod(action) {
		return Principal{}, errors.New("sandbox capability action does not match the HTTP method")
	}
	values := request.Header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") || strings.Count(values[0], " ") != 1 {
		return Principal{}, errors.New("sandbox capability bearer authorization is missing or ambiguous")
	}
	audience := sandboxcapability.AudienceBackend
	if lifecycleAction(action) {
		audience = sandboxcapability.AudienceLifecycle
	}
	claims, err := authorizer.verifier.Verify(strings.TrimPrefix(values[0], "Bearer "), audience, action, authorizer.now().UTC())
	if err != nil {
		return Principal{}, err
	}
	return Principal{
		Audience:    CapabilityAudience(claims.Audience),
		WorkspaceID: claims.WorkspaceID, SessionID: claims.SessionID, EnvironmentID: claims.EnvironmentID,
		RunID: claims.RunID, RunAttemptID: claims.RunAttemptID, RunAttemptGeneration: claims.RunAttemptGeneration,
		HolderID: claims.HolderID, ExecutionID: claims.ExecutionID, OperationID: claims.OperationID,
		MutationKey: claims.MutationKey, SandboxID: claims.SandboxID, TargetGeneration: claims.TargetGeneration,
		WorkspaceAccess: claims.WorkspaceAccess,
	}, nil
}

func lifecycleAction(action string) bool {
	switch action {
	case ActionEnsure, ActionGet, ActionRenewActivity, ActionReleaseActivity, ActionSetTimeout, ActionDelete:
		return true
	default:
		return false
	}
}

func actionMethod(action string) string {
	switch action {
	case ActionGet:
		return http.MethodGet
	case ActionDelete:
		return http.MethodDelete
	case ActionEnsure, ActionRenewActivity, ActionReleaseActivity, ActionSetTimeout,
		ActionRunCommand, ActionSignalCommand, ActionReadFile:
		return http.MethodPost
	default:
		return ""
	}
}

var _ Authorizer = (*CapabilityAuthorizer)(nil)
