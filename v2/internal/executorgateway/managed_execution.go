package executorgateway

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/agentserver/agentserver/v2/internal/executionbackend"
	"github.com/agentserver/agentserver/v2/internal/executorgateway/agentxconn"
	"github.com/agentserver/agentserver/v2/internal/managedcredential"
)

var errManagedCredentialNotConfigured = errors.New("managed credential is not configured")

func managedExecutionErrorMetadata(err error) (reasonCode string, coreHTTPStatus int) {
	if err == nil {
		return "", 0
	}
	if errors.Is(err, errManagedCredentialNotConfigured) {
		return "credential_not_configured", 0
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "context_deadline_exceeded", 0
	}
	if errors.Is(err, context.Canceled) {
		return "context_canceled", 0
	}
	var coreError *CoreCommandError
	if errors.As(err, &coreError) && coreError != nil {
		if coreError.Code == "" {
			return "core_command_failed", coreError.HTTPStatus
		}
		return coreError.Code, coreError.HTTPStatus
	}
	var dispatchError *executionbackend.DispatchError
	if errors.As(err, &dispatchError) && dispatchError != nil {
		if dispatchError.Code == "" {
			return "backend_dispatch_failed", 0
		}
		return dispatchError.Code, 0
	}
	return "internal_error", 0
}

func managedContextMetadata(ctx context.Context) (state string, deadlineRemainingMillis int64) {
	if ctx == nil {
		return "missing", -1
	}
	switch ctx.Err() {
	case context.Canceled:
		state = "canceled"
	case context.DeadlineExceeded:
		state = "deadline_exceeded"
	default:
		state = "active"
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return state, -1
	}
	remaining := time.Until(deadline).Milliseconds()
	if remaining < 0 {
		remaining = 0
	}
	return state, remaining
}

const (
	ManagedLarkApplicationIDEnvironment    = "LARKSUITE_CLI_APP_ID"
	ManagedLarkUserAccessTokenEnvironment  = "LARKSUITE_CLI_USER_ACCESS_TOKEN"
	ManagedLarkNoUpdateNotifierEnvironment = "LARKSUITE_CLI_NO_UPDATE_NOTIFIER"
	ManagedLarkNoSkillsNotifierEnvironment = "LARKSUITE_CLI_NO_SKILLS_NOTIFIER"
	ManagedLarkAgentTraceEnvironment       = managedcredential.LarkAgentTraceEnvironment
	ManagedBkectlJWTEnvironment            = "BKECTL_JWT_TOKEN"
	ManagedBkectlAuthModeEnvironment       = "BKECTL_AUTH_MODE"
	ManagedBkectlAuthModeValue             = "user_only"
	ManagedBkectlRegionEnvironment         = "BKECTL_REGION"
	ManagedBkectlRegionValue               = "i18nbd"
	// The TAE process API accepts an executable name. PATH is therefore a
	// reserved, non-secret projection so the name resolves to the immutable
	// image artifact instead of a workspace-provided binary.
	ManagedToolPathEnvironment = "PATH"
	ManagedToolPathValue       = "/usr/local/bin:/usr/bin:/bin"
)

// ManagedProcessEnvironmentIssuer returns operation-scoped reserved process
// values. The target workspace's current credential mode determines whether the
// Lark token value is a short-lived egress placeholder or a real credential
// resolved immediately before process_start. The execution gateway injects
// values after the model-controlled environment has been validated and
// rejects collisions.
type ManagedProcessEnvironmentIssuer interface {
	IssueManagedProcessEnvironment(context.Context, ManagedProcessEnvironmentRequest) (map[string]string, error)
}

type ManagedProcessEnvironmentRequest struct {
	Principal  ExecutorMCPPrincipal
	Target     executionbackend.Target
	Operation  executionbackend.OperationContext
	ToolName   string
	Executable string
	Arguments  []string
}

// ManagedTargetFencer is the exceptional recovery boundary for an ambiguous
// managed dispatch or termination. Implementations delete/fence exactly the
// supplied sandbox generation; they must never select a replacement target.
type ManagedTargetFencer interface {
	FenceManagedTarget(context.Context, ExecutorMCPPrincipal, executionbackend.Target, string) error
}

func executionTarget(environment ResolvedEnvironment) (executionbackend.Target, error) {
	target := environment.Target
	if err := target.Validate(); err != nil {
		return executionbackend.Target{}, fmt.Errorf("resolved environment target: %w", err)
	}
	if target.EnvironmentID != environment.EnvironmentID {
		return executionbackend.Target{}, errors.New("resolved environment target has a different environment identity")
	}
	return target, nil
}

func coreExecutorID(environment ResolvedEnvironment) string {
	if environment.Target.Kind == executionbackend.KindAgentX {
		return environment.ExecutorID
	}
	return ""
}

func targetConnectionGeneration(target executionbackend.Target) int64 {
	if target.Kind == executionbackend.KindAgentX {
		return target.Generation
	}
	return 0
}

func backendOperationContext(principal ExecutorMCPPrincipal, routing agentxconn.RoutingContext) executionbackend.OperationContext {
	return executionbackend.OperationContext{
		WorkspaceID:          principal.WorkspaceID,
		SessionID:            principal.SessionID,
		RunID:                routing.RunID,
		RunAttemptID:         routing.RunAttemptID,
		RunAttemptGeneration: routing.RunAttemptGeneration,
		ExecutionID:          routing.ExecutionID,
		OperationID:          routing.OperationID,
		MutationKey:          routing.MutationKey,
	}
}

func injectManagedProcessEnvironment(
	ctx context.Context,
	issuer ManagedProcessEnvironmentIssuer,
	request ManagedProcessEnvironmentRequest,
	explicit map[string]string,
) (map[string]string, error) {
	result := cloneStringMap(explicit)
	if issuer == nil {
		return result, nil
	}
	reserved, err := issuer.IssueManagedProcessEnvironment(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("issue managed process environment: %w", err)
	}
	for name, value := range reserved {
		if _, collision := result[name]; collision {
			return nil, fmt.Errorf("managed reserved process environment %q collides with tool arguments", name)
		}
		if name == "" || value == "" {
			return nil, errors.New("managed reserved process environment contains an empty name or value")
		}
		result[name] = value
	}
	return result, nil
}
