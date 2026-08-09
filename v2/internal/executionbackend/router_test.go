package executionbackend_test

import (
	"context"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/executionbackend"
	"github.com/agentserver/agentserver/v2/internal/executionbackend/executionbackendtest"
)

func TestRouterUsesOnlyFrozenTargetKind(t *testing.T) {
	agentx, err := executionbackendtest.NewFakeBackend(executionbackend.KindAgentX)
	if err != nil {
		t.Fatal(err)
	}
	tae, err := executionbackendtest.NewFakeBackend(executionbackend.KindTAE)
	if err != nil {
		t.Fatal(err)
	}
	tae.Start = func(_ context.Context, request executionbackend.StartProcessRequest) (executionbackend.Exchange, error) {
		return executionbackendtest.NewScriptedExchange(executionbackendtest.ExchangeScript{
			Target: request.Target, Operation: request.Operation,
			Acknowledgement: executionbackend.Acknowledgement{AcceptedAt: time.Now().UTC()},
			Terminal:        executionbackend.TerminalResult{Status: executionbackend.TerminalSucceeded, CompletedAt: time.Now().UTC()},
		})
	}
	router, err := executionbackend.NewRouter(agentx, tae)
	if err != nil {
		t.Fatal(err)
	}
	request := validStartRequest(executionbackend.KindTAE)
	if _, err := router.StartProcess(t.Context(), request); err != nil {
		t.Fatalf("Router.StartProcess() error = %v", err)
	}
	if len(tae.StartCalls()) != 1 || len(agentx.StartCalls()) != 0 {
		t.Fatalf("TAE calls = %d, agentx calls = %d", len(tae.StartCalls()), len(agentx.StartCalls()))
	}
}

func TestRouterFailsNotSentWithoutCrossProviderFallback(t *testing.T) {
	agentx, err := executionbackendtest.NewFakeBackend(executionbackend.KindAgentX)
	if err != nil {
		t.Fatal(err)
	}
	router, err := executionbackend.NewRouter(agentx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := router.StartProcess(t.Context(), validStartRequest(executionbackend.KindTAE)); !executionbackend.ProvesNotSent(err) {
		t.Fatalf("Router.StartProcess() error = %v, want not_sent", err)
	}
	if len(agentx.StartCalls()) != 0 {
		t.Fatal("router fell back to agentx for a TAE target")
	}
}

func validStartRequest(kind executionbackend.Kind) executionbackend.StartProcessRequest {
	return executionbackend.StartProcessRequest{
		Target: executionbackend.Target{Kind: kind, ID: "target-123", Generation: 1, EnvironmentID: "env-123"},
		Operation: executionbackend.OperationContext{
			WorkspaceID: "workspace-123", SessionID: "session-123", RunID: "run-123",
			RunAttemptID: "attempt-123", RunAttemptGeneration: 1,
			ExecutionID: "execution-123", OperationID: "operation-123", MutationKey: "mutation-123",
		},
		RequestID: "request-123", ProcessID: "process-123", Executable: "true",
		WorkingDirectory: "/workspace", WorkspaceRoot: "/workspace", Platform: "linux-amd64",
		Timeout: time.Second, OutputLimitBytes: 1024,
	}
}
