package executorgateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/execprofile"
	"github.com/agentserver/agentserver/v2/internal/executionbackend"
	"github.com/agentserver/agentserver/v2/internal/executionbackend/executionbackendtest"
)

const testManagedSandboxID = "65000000-0000-4000-8000-000000000006"

func TestManagedShellRoutesLarkCLIThroughFrozenTAETarget(t *testing.T) {
	environment := testManagedEnvironment(t)
	backend, err := executionbackendtest.NewFakeBackend(executionbackend.KindTAE)
	if err != nil {
		t.Fatal(err)
	}
	exitCode := int32(0)
	backend.Start = func(_ context.Context, request executionbackend.StartProcessRequest) (executionbackend.Exchange, error) {
		return executionbackendtest.NewScriptedExchange(executionbackendtest.ExchangeScript{
			Target: request.Target, Operation: request.Operation,
			Acknowledgement: executionbackend.Acknowledgement{
				ProviderOperationID: "tae-process-1", ProviderRequestID: request.RequestID,
				AcceptedAt: time.Now().UTC(),
			},
			Events: []executionbackend.Event{{
				Sequence: 1, Kind: executionbackend.EventStdout, Data: []byte("managed lark document\n"),
			}},
			Terminal: executionbackend.TerminalResult{
				Status: executionbackend.TerminalSucceeded, ExitCode: &exitCode,
				OutputComplete: true, CompletedAt: time.Now().UTC(),
			},
		})
	}
	router, err := executionbackend.NewRouter(backend)
	if err != nil {
		t.Fatal(err)
	}
	authority := newFakeShellAuthority()
	executor := newManagedShellExecutor(t, environment, authority, router, staticManagedEnvironmentIssuer{
		ManagedLarkUserAccessTokenEnvironment: "placeholder-only-for-egress",
	})
	principal := testExecutorMCPPrincipal("managed-shell-lark")
	result, err := executor.Execute(t.Context(), ShellExecuteRequest{
		Principal: principal, ToolCallID: "call-managed-lark",
		Arguments: json.RawMessage(fmt.Sprintf(
			`{"environment_id":%q,"argv":["lark-cli","docs","read","doc-token"],"timeout_ms":10000}`,
			environment.EnvironmentID,
		)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "succeeded" || result.TimedOut || !result.OutputComplete || result.ExitCode == nil || *result.ExitCode != 0 ||
		len(result.Chunks) != 1 || result.Chunks[0].ChunkBase64 != base64.StdEncoding.EncodeToString([]byte("managed lark document\n")) {
		t.Fatalf("managed shell result = %+v", result)
	}
	calls := backend.StartCalls()
	if len(calls) != 1 {
		t.Fatalf("managed start calls = %d, want 1", len(calls))
	}
	call := calls[0]
	if call.Target != environment.Target || call.Operation.SessionID != principal.SessionID ||
		call.Executable != "lark-cli" || len(call.Arguments) != 3 || call.Arguments[0] != "docs" ||
		call.WorkingDirectory != "/workspace" || call.WorkspaceRoot != "/workspace" ||
		call.Environment[ManagedLarkUserAccessTokenEnvironment] != "placeholder-only-for-egress" {
		t.Fatalf("managed start request = %+v", call)
	}
	if len(backend.SignalCalls()) != 0 {
		t.Fatalf("successful managed process sent %d terminate calls", len(backend.SignalCalls()))
	}
	authority.mu.Lock()
	defer authority.mu.Unlock()
	if authority.execution.ExecutorID != "" || authority.execution.Target != environment.Target ||
		authority.operations[1].Target != environment.Target || authority.operations[1].ConnectionGeneration != 0 ||
		authority.operations[1].Status != "succeeded" || authority.operations[2].Status != "skipped" ||
		authority.acks[ShellV1OperationProcessStart] != 1 {
		t.Fatalf("managed Core projection = execution %+v operations %+v", authority.execution, authority.operations)
	}
}

func TestManagedShellLogsSafePreAcknowledgementDispatchMetadata(t *testing.T) {
	const secretArgument = "secret-command-argument"
	environment := testManagedEnvironment(t)
	backend, err := executionbackendtest.NewFakeBackend(executionbackend.KindTAE)
	if err != nil {
		t.Fatal(err)
	}
	backend.Start = func(_ context.Context, request executionbackend.StartProcessRequest) (executionbackend.Exchange, error) {
		requestWritten := true
		dispatchError := executionbackend.NewDispatchError(
			executionbackend.OutcomeUnknown, "provider_stream_lost", errors.New("safe transport summary"),
		)
		dispatchError.ProviderRequestID = "provider-stream-log-1"
		dispatchError.ProviderCode = "StreamClosed"
		dispatchError.HTTPStatus = 502
		dispatchError.RequestWritten = &requestWritten
		return executionbackendtest.NewScriptedExchange(executionbackendtest.ExchangeScript{
			Target: request.Target, Operation: request.Operation,
			AcknowledgementError: dispatchError,
			Terminal: executionbackend.TerminalResult{
				Status: executionbackend.TerminalUnknown, ReasonCode: "provider_stream_lost",
				OutputComplete: false, CompletedAt: time.Now().UTC(),
			},
		})
	}
	router, err := executionbackend.NewRouter(backend)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	executor := newManagedShellExecutor(t, environment, newFakeShellAuthority(), router, nil)
	executor.config.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	result, err := executor.Execute(t.Context(), ShellExecuteRequest{
		Principal: testExecutorMCPPrincipal("managed-shell-dispatch-log"), ToolCallID: "call-managed-dispatch-log",
		Arguments: json.RawMessage(fmt.Sprintf(
			`{"environment_id":%q,"argv":["lark-cli",%q],"timeout_ms":10000}`,
			environment.EnvironmentID, secretArgument,
		)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "unknown" || result.OutputComplete {
		t.Fatalf("managed dispatch result = %+v", result)
	}
	logged := logs.String()
	for _, wanted := range []string{"managed shell dispatch failed", "start_acknowledgement", "provider-stream-log-1", "StreamClosed", `"provider_http_status":502`} {
		if !strings.Contains(logged, wanted) {
			t.Fatalf("managed dispatch log %q does not contain %q", logged, wanted)
		}
	}
	if strings.Contains(logged, secretArgument) {
		t.Fatalf("managed dispatch log leaked argv: %s", logged)
	}
}

func TestManagedShellLogsSafeProcessEnvironmentFailureBeforeBackendDispatch(t *testing.T) {
	const secret = "secret-token-that-must-not-be-logged"
	environment := testManagedEnvironment(t)
	backend, err := executionbackendtest.NewFakeBackend(executionbackend.KindTAE)
	if err != nil {
		t.Fatal(err)
	}
	router, err := executionbackend.NewRouter(backend)
	if err != nil {
		t.Fatal(err)
	}
	issuer := managedEnvironmentIssuerFunc(func(context.Context, ManagedProcessEnvironmentRequest) (map[string]string, error) {
		return nil, errors.New("credential audit rejected " + secret)
	})
	var logs bytes.Buffer
	executor := newManagedShellExecutor(t, environment, newFakeShellAuthority(), router, issuer)
	executor.config.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	result, err := executor.Execute(t.Context(), ShellExecuteRequest{
		Principal: testExecutorMCPPrincipal("managed-shell-environment-log"), ToolCallID: "call-managed-environment-log",
		Arguments: json.RawMessage(fmt.Sprintf(
			`{"environment_id":%q,"argv":["lark-cli","skills","read","lark-doc"],"timeout_ms":10000}`,
			environment.EnvironmentID,
		)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "unknown" || result.OutputComplete || len(backend.StartCalls()) != 0 {
		t.Fatalf("managed environment failure result/calls = %+v / %d", result, len(backend.StartCalls()))
	}
	logged := logs.String()
	for _, wanted := range []string{"managed shell stage", "environment_inject", "internal_error", "operation_id", "elapsed_ms"} {
		if !strings.Contains(logged, wanted) {
			t.Fatalf("managed environment failure log %q does not contain %q", logged, wanted)
		}
	}
	if strings.Contains(logged, secret) {
		t.Fatalf("managed environment failure log leaked credential error: %s", logged)
	}
}

func TestManagedShellTerminalObservedAtDeadlineDispatchesTimeout(t *testing.T) {
	environment := testManagedEnvironment(t)
	backend, err := executionbackendtest.NewFakeBackend(executionbackend.KindTAE)
	if err != nil {
		t.Fatal(err)
	}
	exitCode := int32(0)
	backend.Start = func(_ context.Context, request executionbackend.StartProcessRequest) (executionbackend.Exchange, error) {
		return executionbackendtest.NewScriptedExchange(executionbackendtest.ExchangeScript{
			Target: request.Target, Operation: request.Operation,
			Acknowledgement: executionbackend.Acknowledgement{
				ProviderOperationID: "tae-process-deadline", ProviderRequestID: request.RequestID,
				AcceptedAt: time.Now().UTC(),
			},
			Terminal: executionbackend.TerminalResult{
				Status: executionbackend.TerminalSucceeded, ExitCode: &exitCode,
				OutputComplete: true, CompletedAt: time.Now().UTC(),
			},
		})
	}
	backend.Signal = func(_ context.Context, request executionbackend.SignalProcessRequest) (executionbackend.Exchange, error) {
		return executionbackendtest.NewScriptedExchange(executionbackendtest.ExchangeScript{
			Target: request.Target, Operation: request.Operation,
			Acknowledgement: executionbackend.Acknowledgement{
				ProviderOperationID: "tae-terminate-deadline", ProviderRequestID: request.RequestID,
				AcceptedAt: time.Now().UTC(),
			},
			Terminal: executionbackend.TerminalResult{
				Status: executionbackend.TerminalSucceeded, OutputComplete: true, CompletedAt: time.Now().UTC(),
			},
		})
	}
	router, err := executionbackend.NewRouter(backend)
	if err != nil {
		t.Fatal(err)
	}
	authority := newFakeShellAuthority()
	executor := newManagedShellExecutor(t, environment, authority, router, nil)
	base := time.Now().UTC()
	var clockCalls atomic.Int32
	executor.config.Now = func() time.Time {
		if clockCalls.Add(1) == 1 {
			return base
		}
		// The process exchange is immediately terminal, but the observation
		// itself lands exactly on the frozen deadline. This exercises the race
		// where the evidence channel can win the select over the timer channel.
		return base.Add(10 * time.Second)
	}

	result, err := executor.Execute(t.Context(), ShellExecuteRequest{
		Principal: testExecutorMCPPrincipal("managed-shell-deadline"), ToolCallID: "call-managed-deadline",
		Arguments: json.RawMessage(fmt.Sprintf(
			`{"environment_id":%q,"argv":["true"],"timeout_ms":10000}`,
			environment.EnvironmentID,
		)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "failed" || !result.TimedOut {
		t.Fatalf("deadline result = %+v, want failed timed-out result", result)
	}
	if len(backend.SignalCalls()) != 1 {
		t.Fatalf("managed deadline signal calls = %d, want 1", len(backend.SignalCalls()))
	}
	if got := authority.operationStatuses(); got != [2]string{"succeeded", "failed"} {
		t.Fatalf("deadline operation statuses = %v, want [succeeded failed]", got)
	}
	if authority.skipCalls() != 0 || authority.ackCallsFor(ShellV1OperationTimeoutTerminate) != 1 {
		t.Fatalf("deadline timeout skips = %d, acknowledgements = %d", authority.skipCalls(), authority.ackCallsFor(ShellV1OperationTimeoutTerminate))
	}
}

func TestManagedReadFileUsesBackendAcknowledgementAndFileEvents(t *testing.T) {
	environment := testManagedEnvironment(t)
	backend, err := executionbackendtest.NewFakeBackend(executionbackend.KindTAE)
	if err != nil {
		t.Fatal(err)
	}
	backend.Read = func(_ context.Context, request executionbackend.ReadFileRequest) (executionbackend.Exchange, error) {
		return executionbackendtest.NewScriptedExchange(executionbackendtest.ExchangeScript{
			Target: request.Target, Operation: request.Operation,
			Acknowledgement: executionbackend.Acknowledgement{
				ProviderOperationID: "tae-file-1", ProviderRequestID: request.RequestID,
				AcceptedAt: time.Now().UTC(),
			},
			Events: []executionbackend.Event{{Sequence: 1, Kind: executionbackend.EventFileBytes, Data: []byte("hello")}},
			Terminal: executionbackend.TerminalResult{
				Status: executionbackend.TerminalSucceeded, OutputComplete: true, CompletedAt: time.Now().UTC(),
			},
		})
	}
	router, err := executionbackend.NewRouter(backend)
	if err != nil {
		t.Fatal(err)
	}
	authority := newFakeReadFileAuthority()
	executor := newManagedReadFileExecutor(t, environment, authority, router)
	principal := testExecutorMCPPrincipal("managed-read-file")
	result, err := executor.Execute(t.Context(), ReadFileExecuteRequest{
		Principal: principal, ToolCallID: "call-managed-read",
		Arguments: json.RawMessage(fmt.Sprintf(
			`{"environment_id":%q,"path":"docs/result.txt","limit":16}`,
			environment.EnvironmentID,
		)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "succeeded" || result.Content != "hello" || !result.EOF || result.BytesRead != 5 {
		t.Fatalf("managed read result = %+v", result)
	}
	calls := backend.ReadCalls()
	if len(calls) != 1 || calls[0].Target != environment.Target || calls[0].Operation.SessionID != principal.SessionID ||
		calls[0].Path != "/workspace/docs/result.txt" || calls[0].Limit != 16 {
		t.Fatalf("managed read calls = %+v", calls)
	}
	authority.mu.Lock()
	defer authority.mu.Unlock()
	if authority.execution.ExecutorID != "" || authority.execution.Target != environment.Target ||
		authority.operation.Target != environment.Target || authority.operation.ConnectionGeneration != 0 ||
		authority.ackCalls != 1 || authority.operation.Status != "succeeded" {
		t.Fatalf("managed read Core projection = execution %+v operation %+v", authority.execution, authority.operation)
	}
}

func testManagedEnvironment(t *testing.T) ResolvedEnvironment {
	t.Helper()
	registered := testRegisteredEnvironment(testEnvironmentID, `{"kind":"managed","root":"/workspace","displayName":"Managed SG","defaultCwd":"."}`)
	registered.ConnectionGeneration = 0
	registered.Platform = "linux-amd64"
	registered.BackendKind = executionbackend.KindTAE
	registered.TargetID = testManagedSandboxID
	registered.TargetGeneration = 7
	registered.OuterProfileVersion = execprofile.FilesystemReadVersion
	resolved, err := resolveRegisteredEnvironment(registered)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func newManagedShellExecutor(
	t *testing.T,
	environment ResolvedEnvironment,
	authority *fakeShellAuthority,
	router *executionbackend.Router,
	issuer ManagedProcessEnvironmentIssuer,
) *ShellExecutor {
	t.Helper()
	resolver, err := NewEnvironmentResolver(&fakeEnvironmentRegistry{environments: []RegisteredEnvironment{environment.RegisteredEnvironment}})
	if err != nil {
		t.Fatal(err)
	}
	identities, err := NewShellV1IdentityAllocator(deterministicIDGenerator())
	if err != nil {
		t.Fatal(err)
	}
	transitions, err := NewExecutionTransitionAllocator("66000000-0000-4000-8000-000000000006", deterministicIDGenerator())
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultShellExecutorConfig(t.Context())
	config.TerminalGrace = 50 * time.Millisecond
	config.BackendRouter = router
	config.ManagedEnvironmentIssuer = issuer
	configureTestShellPolicy(t, &config)
	executor, err := NewShellExecutor(resolver, authority, &fakeShellDispatcher{}, identities, transitions, config)
	if err != nil {
		t.Fatal(err)
	}
	return executor
}

func newManagedReadFileExecutor(
	t *testing.T,
	environment ResolvedEnvironment,
	authority *fakeReadFileAuthority,
	router *executionbackend.Router,
) *ReadFileExecutor {
	t.Helper()
	resolver, err := NewEnvironmentResolver(&fakeEnvironmentRegistry{environments: []RegisteredEnvironment{environment.RegisteredEnvironment}})
	if err != nil {
		t.Fatal(err)
	}
	identities, err := NewReadFileV1IdentityAllocator(deterministicIDGenerator())
	if err != nil {
		t.Fatal(err)
	}
	transitions, err := NewExecutionTransitionAllocator("67000000-0000-4000-8000-000000000006", deterministicIDGenerator())
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultReadFileExecutorConfig(t.Context())
	config.BackendRouter = router
	configureTestReadFilePolicy(t, &config)
	executor, err := NewReadFileExecutor(resolver, authority, &fakeFilesystemDispatcher{}, identities, transitions, config)
	if err != nil {
		t.Fatal(err)
	}
	return executor
}

type staticManagedEnvironmentIssuer map[string]string

func (issuer staticManagedEnvironmentIssuer) IssueManagedProcessEnvironment(
	context.Context,
	ManagedProcessEnvironmentRequest,
) (map[string]string, error) {
	result := make(map[string]string, len(issuer))
	for name, value := range issuer {
		result[name] = value
	}
	return result, nil
}

type managedEnvironmentIssuerFunc func(context.Context, ManagedProcessEnvironmentRequest) (map[string]string, error)

func (issuer managedEnvironmentIssuerFunc) IssueManagedProcessEnvironment(
	ctx context.Context,
	request ManagedProcessEnvironmentRequest,
) (map[string]string, error) {
	return issuer(ctx, request)
}
