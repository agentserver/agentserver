package executorgateway

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/runmanifest"
)

func testAllowPolicyResolver(t *testing.T) ExecutionPolicyResolver {
	t.Helper()
	resolver, err := NewStaticExecutionPolicyResolver("test-execution-policy-v1", map[string]string{
		"shell": PolicyDecisionAllow, "read_file": PolicyDecisionAllow,
	})
	if err != nil {
		t.Fatal(err)
	}
	return resolver
}

type unexpectedTestApprovalGate struct{}

func (unexpectedTestApprovalGate) AuthorizeExecution(context.Context, ApprovalGateRequest) (ExecutionState, error) {
	return ExecutionState{}, errors.New("test approval gate was called for policy=allow")
}

type recordingTestApprovalGate struct {
	calls int
	err   error
}

func (gate *recordingTestApprovalGate) AuthorizeExecution(_ context.Context, request ApprovalGateRequest) (ExecutionState, error) {
	gate.calls++
	return request.Execution, gate.err
}

func configureTestShellPolicy(t *testing.T, config *ShellExecutorConfig) {
	t.Helper()
	config.PolicyResolver = testAllowPolicyResolver(t)
	config.ApprovalGate = unexpectedTestApprovalGate{}
}

func configureTestReadFilePolicy(t *testing.T, config *ReadFileExecutorConfig) {
	t.Helper()
	config.PolicyResolver = testAllowPolicyResolver(t)
	config.ApprovalGate = unexpectedTestApprovalGate{}
}

func TestStaticExecutionPolicyResolverIsExplicitAndFailClosed(t *testing.T) {
	resolver, err := NewStaticExecutionPolicyResolver("policy-v1", map[string]string{
		"shell": PolicyDecisionAsk,
	})
	if err != nil {
		t.Fatal(err)
	}
	resolution, err := resolver.ResolveExecutionPolicy(t.Context(), ExecutionPolicyInput{ToolName: "shell"})
	if err != nil || resolution.Version != "policy-v1" || resolution.Decision != PolicyDecisionAsk {
		t.Fatalf("ResolveExecutionPolicy(shell) = %+v, %v", resolution, err)
	}
	if _, err := resolver.ResolveExecutionPolicy(t.Context(), ExecutionPolicyInput{ToolName: "read_file"}); err == nil || !strings.Contains(err.Error(), "no explicit execution policy") {
		t.Fatalf("missing tool policy error = %v", err)
	}
	for _, decisions := range []map[string]string{
		nil,
		{"shell": "sometimes"},
		{"": PolicyDecisionAllow},
	} {
		if _, err := NewStaticExecutionPolicyResolver("policy-v1", decisions); err == nil {
			t.Fatalf("invalid static policy was accepted: %#v", decisions)
		}
	}
}

func TestPermissionModeExecutionPolicyResolverCouplesCodexAndExecutorPolicy(t *testing.T) {
	resolver, err := NewPermissionModeExecutionPolicyResolver("execution-policy-v2", map[string]string{
		"shell": PolicyDecisionAsk, "read_file": PolicyDecisionAllow,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		tool     string
		mode     runmanifest.CodexPermissionMode
		version  int64
		decision string
	}{
		{name: "legacy shell", tool: "shell", decision: PolicyDecisionAsk},
		{name: "read-only shell", tool: "shell", mode: runmanifest.CodexPermissionModeReadOnly, version: 1, decision: PolicyDecisionAsk},
		{name: "auto shell", tool: "shell", mode: runmanifest.CodexPermissionModeAuto, version: 2, decision: PolicyDecisionAllow},
		{name: "full-access shell", tool: "shell", mode: runmanifest.CodexPermissionModeFullAccess, version: 3, decision: PolicyDecisionAllow},
		{name: "read-only file", tool: "read_file", mode: runmanifest.CodexPermissionModeReadOnly, version: 1, decision: PolicyDecisionAllow},
		{name: "auto file", tool: "read_file", mode: runmanifest.CodexPermissionModeAuto, version: 2, decision: PolicyDecisionAllow},
		{name: "full-access file", tool: "read_file", mode: runmanifest.CodexPermissionModeFullAccess, version: 3, decision: PolicyDecisionAllow},
	} {
		t.Run(test.name, func(t *testing.T) {
			resolution, err := resolver.ResolveExecutionPolicy(t.Context(), ExecutionPolicyInput{
				ToolName: test.tool,
				Principal: ExecutorMCPPrincipal{
					PermissionMode: string(test.mode), PermissionModeVersion: test.version,
				},
			})
			if err != nil || resolution.Version != "execution-policy-v2" || resolution.Decision != test.decision {
				t.Fatalf("ResolveExecutionPolicy(%s/%s) = %+v, %v", test.tool, test.mode, resolution, err)
			}
		})
	}
}

func TestPermissionModeExecutionPolicyResolverPreservesDeploymentDenyAndRejectsInvalidAuthority(t *testing.T) {
	resolver, err := NewPermissionModeExecutionPolicyResolver("execution-policy-v2", map[string]string{
		"shell": PolicyDecisionDeny,
	})
	if err != nil {
		t.Fatal(err)
	}
	resolution, err := resolver.ResolveExecutionPolicy(t.Context(), ExecutionPolicyInput{
		ToolName: "shell",
		Principal: ExecutorMCPPrincipal{
			PermissionMode: string(runmanifest.CodexPermissionModeFullAccess), PermissionModeVersion: 1,
		},
	})
	if err != nil || resolution.Decision != PolicyDecisionDeny {
		t.Fatalf("full-access deployment deny = %+v, %v", resolution, err)
	}
	for _, principal := range []ExecutorMCPPrincipal{
		{PermissionMode: "future-mode", PermissionModeVersion: 1},
		{PermissionMode: string(runmanifest.CodexPermissionModeFullAccess)},
		{PermissionMode: string(runmanifest.CodexPermissionModeFullAccess), PermissionModeVersion: 1 << 53},
	} {
		if _, err := resolver.ResolveExecutionPolicy(t.Context(), ExecutionPolicyInput{ToolName: "shell", Principal: principal}); err == nil {
			t.Fatalf("invalid permission authority was accepted: %+v", principal)
		}
	}
}
