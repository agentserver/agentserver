package executorgateway

import (
	"context"
	"errors"
	"strings"
	"testing"
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
