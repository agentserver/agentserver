package executorgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/executorgateway/mcpcontract"
	"github.com/agentserver/agentserver/v2/internal/runmanifest"
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

// PermissionModeExecutionPolicyResolver couples the executor product policy
// to the same per-run Codex permission authority carried by the signed run
// capability.  The deployment decision remains the fail-closed baseline:
// explicit deny always wins, read-only never inherits an allow for the
// mutation-capable shell tool, and the auto/full-access presets may remove the
// redundant approval for bounded executor tools.  The filesystem, network,
// capability, and Core live-authority checks still run for every call; this
// resolver only decides whether the separate human approval round-trip is
// needed.
type PermissionModeExecutionPolicyResolver struct {
	version   string
	decisions map[string]string
}

// NewPermissionModeExecutionPolicyResolver builds the production resolver
// from an explicit deployment baseline.  Keeping the baseline in the locked
// production document preserves an operator kill switch (deny) while making
// session permission-mode changes effective in both Codex and AgentServer.
func NewPermissionModeExecutionPolicyResolver(version string, decisions map[string]string) (*PermissionModeExecutionPolicyResolver, error) {
	static, err := NewStaticExecutionPolicyResolver(version, decisions)
	if err != nil {
		return nil, err
	}
	return &PermissionModeExecutionPolicyResolver{
		version:   static.version,
		decisions: cloneExecutionPolicyDecisions(static.decisions),
	}, nil
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

func cloneExecutionPolicyDecisions(source map[string]string) map[string]string {
	cloned := make(map[string]string, len(source))
	for tool, decision := range source {
		cloned[tool] = decision
	}
	return cloned
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

func (resolver *PermissionModeExecutionPolicyResolver) ResolveExecutionPolicy(ctx context.Context, input ExecutionPolicyInput) (ExecutionPolicyResolution, error) {
	if ctx == nil {
		return ExecutionPolicyResolution{}, errors.New("execution policy context is required")
	}
	if err := ctx.Err(); err != nil {
		return ExecutionPolicyResolution{}, err
	}
	if resolver == nil {
		return ExecutionPolicyResolution{}, errors.New("permission-mode execution policy resolver is required")
	}
	decision, found := resolver.decisions[input.ToolName]
	if !found {
		return ExecutionPolicyResolution{}, fmt.Errorf("tool %q has no explicit execution policy", input.ToolName)
	}
	if input.Principal.PermissionMode != "" {
		mode, err := runmanifest.CodexPermissionMode(input.Principal.PermissionMode).Effective()
		if err != nil {
			return ExecutionPolicyResolution{}, fmt.Errorf("permission mode: %w", err)
		}
		if input.Principal.PermissionModeVersion < 1 || input.Principal.PermissionModeVersion > 1<<53-1 {
			return ExecutionPolicyResolution{}, errors.New("explicit permission mode requires a positive JSON-safe version")
		}
		decision = permissionModeExecutionDecision(input.ToolName, mode, decision)
	}
	result := ExecutionPolicyResolution{Version: resolver.version, Decision: decision}
	if err := validateExecutionPolicyResolution(result); err != nil {
		return ExecutionPolicyResolution{}, err
	}
	return result, nil
}

func permissionModeExecutionDecision(tool string, mode runmanifest.CodexPermissionMode, baseline string) string {
	if baseline == PolicyDecisionDeny {
		return PolicyDecisionDeny
	}
	switch tool {
	case mcpcontract.ToolShell:
		// Shell is the mutation-capable executor tool.  A read-only session
		// must never inherit an operator-level allow, while auto/full-access
		// can remove the redundant product approval when the deployment did
		// not explicitly deny the tool.  The backend still enforces the
		// corresponding Codex/AgentX filesystem and network profile.
		if mode == runmanifest.CodexPermissionModeReadOnly {
			return PolicyDecisionAsk
		}
		if mode == runmanifest.CodexPermissionModeAuto || mode == runmanifest.CodexPermissionModeFullAccess {
			return PolicyDecisionAllow
		}
	case mcpcontract.ToolReadFile:
		// Reading a bounded file does not need a stronger sandbox profile.  A
		// deployment-level ask is still honored for read-only/legacy calls,
		// while auto/full-access removes that redundant prompt just like the
		// shell path.
		if baseline == PolicyDecisionAsk && (mode == runmanifest.CodexPermissionModeAuto || mode == runmanifest.CodexPermissionModeFullAccess) {
			return PolicyDecisionAllow
		}
		return baseline
	}
	return baseline
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
