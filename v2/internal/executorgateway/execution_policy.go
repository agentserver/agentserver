package executorgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	PolicyDecisionAllow = "allow"
	PolicyDecisionAsk   = "ask"
	PolicyDecisionDeny  = "deny"
)

// ExecutionPolicyInput is the immutable gateway-owned projection supplied to
// the policy resolver. The model controls Arguments only; run and environment
// authority come from the authenticated MCP session and Core registry.
type ExecutionPolicyInput struct {
	Principal   ExecutorMCPPrincipal
	ToolName    string
	Arguments   json.RawMessage
	Environment ResolvedEnvironment
}

type ExecutionPolicyResolution struct {
	Version  string
	Decision string
}

type ExecutionPolicyResolver interface {
	ResolveExecutionPolicy(context.Context, ExecutionPolicyInput) (ExecutionPolicyResolution, error)
}

// StaticExecutionPolicyResolver is the explicit Phase 4 deployment policy.
// Missing tools fail closed; an empty map never means allow-all. A production
// policy adapter can replace it without changing mapper or execution state.
type StaticExecutionPolicyResolver struct {
	version   string
	decisions map[string]string
}

func NewStaticExecutionPolicyResolver(version string, decisions map[string]string) (*StaticExecutionPolicyResolver, error) {
	if err := validateExecutionPolicyResolution(ExecutionPolicyResolution{Version: version, Decision: PolicyDecisionDeny}); err != nil {
		return nil, fmt.Errorf("static execution policy version: %w", err)
	}
	if len(decisions) == 0 {
		return nil, errors.New("static execution policy requires at least one explicit tool decision")
	}
	cloned := make(map[string]string, len(decisions))
	for tool, decision := range decisions {
		if tool == "" || len(tool) > 128 || !utf8.ValidString(tool) || strings.ContainsRune(tool, 0) {
			return nil, fmt.Errorf("static execution policy contains invalid tool name %q", tool)
		}
		resolution := ExecutionPolicyResolution{Version: version, Decision: decision}
		if err := validateExecutionPolicyResolution(resolution); err != nil {
			return nil, fmt.Errorf("static execution policy tool %q: %w", tool, err)
		}
		cloned[tool] = decision
	}
	return &StaticExecutionPolicyResolver{version: version, decisions: cloned}, nil
}

func (resolver *StaticExecutionPolicyResolver) ResolveExecutionPolicy(ctx context.Context, input ExecutionPolicyInput) (ExecutionPolicyResolution, error) {
	if ctx == nil {
		return ExecutionPolicyResolution{}, errors.New("execution policy context is required")
	}
	if err := ctx.Err(); err != nil {
		return ExecutionPolicyResolution{}, err
	}
	if resolver == nil {
		return ExecutionPolicyResolution{}, errors.New("execution policy resolver is required")
	}
	decision, found := resolver.decisions[input.ToolName]
	if !found {
		return ExecutionPolicyResolution{}, fmt.Errorf("tool %q has no explicit execution policy", input.ToolName)
	}
	result := ExecutionPolicyResolution{Version: resolver.version, Decision: decision}
	if err := validateExecutionPolicyResolution(result); err != nil {
		return ExecutionPolicyResolution{}, err
	}
	return result, nil
}

func validateExecutionPolicyResolution(resolution ExecutionPolicyResolution) error {
	if len(resolution.Version) < 1 || len(resolution.Version) > 128 || !utf8.ValidString(resolution.Version) || strings.ContainsRune(resolution.Version, 0) {
		return errors.New("execution policy version must contain between 1 and 128 valid UTF-8 bytes without NUL")
	}
	switch resolution.Decision {
	case PolicyDecisionAllow, PolicyDecisionAsk, PolicyDecisionDeny:
		return nil
	default:
		return errors.New("execution policy decision must be allow, ask, or deny")
	}
}
