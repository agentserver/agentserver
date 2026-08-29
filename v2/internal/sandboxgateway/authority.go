package sandboxgateway

import (
	"errors"
	"net/http"

	"github.com/agentserver/agentserver/v2/internal/sandboxcontract"
)

type CapabilityAudience string

const (
	AudienceLifecycle CapabilityAudience = "sandbox-lifecycle"
	AudienceBackend   CapabilityAudience = "sandbox-backend"
)

const (
	ActionEnsure          = "ensure"
	ActionGet             = "get"
	ActionRenewActivity   = "renew_activity"
	ActionReleaseActivity = "release_activity"
	ActionSetTimeout      = "set_timeout"
	ActionDelete          = "delete"
	ActionRunCommand      = "run_command"
	ActionSignalCommand   = "signal_command"
	ActionReadFile        = "read_file"
)

// Principal is the already-verified capability binding. Lifecycle principals
// carry the run-attempt holder used for Core lease commands; backend
// principals are bound to exactly one operation and target generation.
type Principal struct {
	Audience             CapabilityAudience
	WorkspaceID          string
	SessionID            string
	EnvironmentID        string
	RunID                string
	RunAttemptID         string
	RunAttemptGeneration int64
	HolderID             string
	ExecutionID          string
	OperationID          string
	MutationKey          string
	SandboxID            string
	TargetGeneration     int64
	WorkspaceAccess      string
}

type Authorizer interface {
	Authorize(*http.Request, string) (Principal, error)
}

func bindSessionPrincipal(principal Principal, action string, session sandboxcontract.SessionIdentity) error {
	if principal.Audience != AudienceLifecycle {
		return errors.New("lifecycle action requires sandbox-lifecycle audience")
	}
	if principal.WorkspaceID != session.WorkspaceID || principal.SessionID != session.SessionID ||
		principal.EnvironmentID != session.EnvironmentID {
		return errors.New("lifecycle capability does not match the requested session")
	}
	if (action == ActionRenewActivity || action == ActionReleaseActivity) &&
		(principal.RunID == "" || principal.RunAttemptID == "" || principal.RunAttemptGeneration < 1 || principal.HolderID == "") {
		return errors.New("activity action requires a run-attempt holder binding")
	}
	return nil
}

func bindOperationPrincipal(principal Principal, action string, identity sandboxcontract.OperationIdentity, ref sandboxcontract.SandboxRef) error {
	if principal.Audience != AudienceBackend {
		return errors.New("backend action requires sandbox-backend audience")
	}
	if principal.WorkspaceID != identity.Session.WorkspaceID || principal.SessionID != identity.Session.SessionID ||
		principal.EnvironmentID != identity.Session.EnvironmentID || principal.RunID != identity.RunID ||
		principal.RunAttemptID != identity.RunAttemptID || principal.RunAttemptGeneration != identity.RunAttemptGeneration ||
		principal.ExecutionID != identity.ExecutionID || principal.OperationID != identity.OperationID ||
		principal.MutationKey != identity.MutationKey || principal.SandboxID != ref.SandboxID ||
		principal.TargetGeneration != ref.TargetGeneration {
		return errors.New("backend capability does not match the requested operation target")
	}
	if action == ActionRunCommand {
		if principal.WorkspaceAccess != "" && principal.WorkspaceAccess != "read" && principal.WorkspaceAccess != "write" {
			return errors.New("backend capability workspace access is invalid")
		}
	} else if principal.WorkspaceAccess != "" {
		return errors.New("non-command backend capability contains workspace access")
	}
	return nil
}
