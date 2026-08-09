package executionbackendtest

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/executionbackend"
)

func fakeTarget() executionbackend.Target {
	return executionbackend.Target{
		Kind: executionbackend.KindTAE, ID: "sandbox-123", Generation: 3, EnvironmentID: "env-123",
	}
}

func fakeOperation() executionbackend.OperationContext {
	return executionbackend.OperationContext{
		WorkspaceID: "workspace-123", SessionID: "session-123", RunID: "run-123",
		RunAttemptID: "attempt-123", RunAttemptGeneration: 2,
		ExecutionID: "execution-123", OperationID: "operation-123", MutationKey: "mutation-123",
	}
}

func TestScriptedExchangeRetainsAcknowledgementEventsAndTerminal(t *testing.T) {
	now := time.Now().UTC()
	exitCode := int32(0)
	script := ExchangeScript{
		Target: fakeTarget(), Operation: fakeOperation(),
		Acknowledgement: executionbackend.Acknowledgement{ProviderOperationID: "provider-op", AcceptedAt: now},
		Events: []executionbackend.Event{
			{Sequence: 1, Kind: executionbackend.EventStdout, Data: []byte("hello")},
			{Sequence: 2, Kind: executionbackend.EventStderr, Data: []byte("warning")},
		},
		Terminal: executionbackend.TerminalResult{
			Status: executionbackend.TerminalSucceeded, ExitCode: &exitCode, OutputComplete: true, CompletedAt: now,
		},
	}
	exchange, err := NewScriptedExchange(script)
	if err != nil {
		t.Fatalf("NewScriptedExchange() error = %v", err)
	}
	script.Events[0].Data[0] = 'X'

	acknowledgement, err := exchange.AwaitAcknowledgement(t.Context())
	if err != nil || acknowledgement.ProviderOperationID != "provider-op" {
		t.Fatalf("AwaitAcknowledgement() = %+v, %v", acknowledgement, err)
	}
	first, err := exchange.NextEvent(t.Context())
	if err != nil || string(first.Data) != "hello" {
		t.Fatalf("first NextEvent() = %+v, %v", first, err)
	}
	first.Data[0] = 'Y'
	second, err := exchange.NextEvent(t.Context())
	if err != nil || second.Sequence != 2 || string(second.Data) != "warning" {
		t.Fatalf("second NextEvent() = %+v, %v", second, err)
	}
	if _, err := exchange.NextEvent(t.Context()); !errors.Is(err, io.EOF) {
		t.Fatalf("exhausted NextEvent() error = %v, want EOF", err)
	}
	terminal, err := exchange.AwaitTerminal(t.Context())
	if err != nil || terminal.Status != executionbackend.TerminalSucceeded {
		t.Fatalf("AwaitTerminal() = %+v, %v", terminal, err)
	}
	*terminal.ExitCode = 42
	retained, err := exchange.AwaitTerminal(t.Context())
	if err != nil || retained.ExitCode == nil || *retained.ExitCode != 0 {
		t.Fatalf("retained AwaitTerminal() = %+v, %v", retained, err)
	}
	select {
	case <-exchange.Done():
	default:
		t.Fatal("Done() remained open after terminal result")
	}
}

func TestScriptedExchangeRejectsNonMonotonicEvents(t *testing.T) {
	now := time.Now().UTC()
	_, err := NewScriptedExchange(ExchangeScript{
		Target: fakeTarget(), Operation: fakeOperation(),
		Acknowledgement: executionbackend.Acknowledgement{AcceptedAt: now},
		Events: []executionbackend.Event{
			{Sequence: 2, Kind: executionbackend.EventStdout, Data: []byte("first")},
			{Sequence: 2, Kind: executionbackend.EventStdout, Data: []byte("duplicate")},
		},
		Terminal: executionbackend.TerminalResult{Status: executionbackend.TerminalSucceeded, CompletedAt: now},
	})
	if err == nil {
		t.Fatal("NewScriptedExchange() accepted duplicate event sequence")
	}
}

func TestFakeBackendRecordsClonedCallsAndFailsClosedWhenUnconfigured(t *testing.T) {
	backend, err := NewFakeBackend(executionbackend.KindTAE)
	if err != nil {
		t.Fatalf("NewFakeBackend() error = %v", err)
	}
	request := executionbackend.StartProcessRequest{
		Target: fakeTarget(), Operation: fakeOperation(), ProcessID: "process-123",
		RequestID:  "request-123",
		Executable: "lark-cli", Arguments: []string{"doc", "get"}, WorkingDirectory: "/workspace",
		WorkspaceRoot: "/workspace", Platform: "linux-amd64",
		Environment: map[string]string{"LANG": "C.UTF-8"}, Timeout: time.Second, OutputLimitBytes: 1024,
	}
	if _, err := backend.StartProcess(t.Context(), request); !executionbackend.ProvesNotSent(err) {
		t.Fatalf("unconfigured StartProcess() error = %v, want not_sent", err)
	}
	request.Arguments[0] = "mutated"
	request.Environment["LANG"] = "mutated"
	calls := backend.StartCalls()
	if len(calls) != 1 || calls[0].Arguments[0] != "doc" || calls[0].Environment["LANG"] != "C.UTF-8" {
		t.Fatalf("StartCalls() = %+v, want retained clone", calls)
	}
}

func TestScriptedExchangeHonorsCancelledContext(t *testing.T) {
	now := time.Now().UTC()
	exchange, err := NewScriptedExchange(ExchangeScript{
		Target: fakeTarget(), Operation: fakeOperation(),
		Acknowledgement: executionbackend.Acknowledgement{AcceptedAt: now},
		Terminal:        executionbackend.TerminalResult{Status: executionbackend.TerminalSucceeded, CompletedAt: now},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := exchange.AwaitAcknowledgement(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("AwaitAcknowledgement() error = %v, want canceled", err)
	}
}
