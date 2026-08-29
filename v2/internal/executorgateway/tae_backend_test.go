package executorgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/executionbackend"
	"github.com/agentserver/agentserver/v2/internal/sandboxcontract"
)

func TestTAEBackendMapsArgvAndConsumesFencedStream(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	const requestWorkspaceAccess = "read"
	tokens := &recordingSandboxTokenSource{token: "test-backend-capability-token"}
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/internal/v2/sandboxes/tae-sandbox-1/commands:run" {
			t.Fatalf("sandbox-gateway path = %q", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer test-backend-capability-token" {
			t.Fatalf("Authorization = %q", request.Header.Get("Authorization"))
		}
		var command sandboxcontract.RunCommandRequest
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&command); err != nil {
			t.Fatal(err)
		}
		if command.Executable != "lark-cli" || len(command.Arguments) != 3 ||
			command.Arguments[0] != "doc" || command.Arguments[1] != "get" || command.Arguments[2] != "a value with spaces" {
			t.Fatalf("mapped command = %+v", command)
		}
		if command.WorkspaceAccess != requestWorkspaceAccess {
			t.Fatalf("mapped workspace access = %q, want %q", command.WorkspaceAccess, requestWorkspaceAccess)
		}
		if command.Environment["LARK_AUTHORIZATION"] != "placeholder-value" {
			t.Fatalf("mapped environment = %+v", command.Environment)
		}
		ack := executionbackend.Acknowledgement{ProviderOperationID: "tae-process-1", ProviderRequestID: command.RequestID, AcceptedAt: now}
		event := executionbackend.Event{Sequence: 1, Kind: executionbackend.EventStdout, Data: []byte("document title\n")}
		terminal := executionbackend.TerminalResult{Status: executionbackend.TerminalSucceeded, OutputComplete: true, CompletedAt: now}
		body := encodeOperationFrames(t,
			sandboxcontract.OperationFrame{Profile: sandboxcontract.ProfileV1, Type: sandboxcontract.OperationFrameAcknowledgement, Identity: command.Identity, Ref: command.Ref, Acknowledgement: &ack},
			sandboxcontract.OperationFrame{Profile: sandboxcontract.ProfileV1, Type: sandboxcontract.OperationFrameEvent, Identity: command.Identity, Ref: command.Ref, Event: &event},
			sandboxcontract.OperationFrame{Profile: sandboxcontract.ProfileV1, Type: sandboxcontract.OperationFrameTerminal, Identity: command.Identity, Ref: command.Ref, Terminal: &terminal},
		)
		return operationHTTPResponse(body), nil
	})
	backend, err := NewTAEBackend("https://sandbox-gateway.internal", &http.Client{Transport: transport}, tokens)
	if err != nil {
		t.Fatal(err)
	}
	request := validTAEStartRequest()
	request.WorkspaceAccess = requestWorkspaceAccess
	exchange, err := backend.StartProcess(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	ack, err := exchange.AwaitAcknowledgement(t.Context())
	if err != nil || ack.ProviderOperationID != "tae-process-1" {
		t.Fatalf("AwaitAcknowledgement() = %+v, %v", ack, err)
	}
	event, err := exchange.NextEvent(t.Context())
	if err != nil || string(event.Data) != "document title\n" || event.Sequence != 1 {
		t.Fatalf("NextEvent() = %+v, %v", event, err)
	}
	if _, err := exchange.NextEvent(t.Context()); !errors.Is(err, io.EOF) {
		t.Fatalf("terminal NextEvent() error = %v, want EOF", err)
	}
	terminal, err := exchange.AwaitTerminal(t.Context())
	if err != nil || terminal.Status != executionbackend.TerminalSucceeded || !terminal.OutputComplete {
		t.Fatalf("AwaitTerminal() = %+v, %v", terminal, err)
	}
	select {
	case <-exchange.Done():
	default:
		t.Fatal("TAE HTTP exchange did not close Done after terminal")
	}
	if len(tokens.requests) != 1 || tokens.requests[0].Action != "run_command" || tokens.requests[0].Target != request.Target {
		t.Fatalf("token requests = %+v", tokens.requests)
	}
}

func TestTAEBackendTreatsSequenceGapAsUnknown(t *testing.T) {
	now := time.Now().UTC()
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var command sandboxcontract.RunCommandRequest
		if err := json.NewDecoder(request.Body).Decode(&command); err != nil {
			t.Fatal(err)
		}
		ack := executionbackend.Acknowledgement{AcceptedAt: now}
		event := executionbackend.Event{Sequence: 2, Kind: executionbackend.EventStdout, Data: []byte("gap")}
		return operationHTTPResponse(encodeOperationFrames(t,
			sandboxcontract.OperationFrame{Profile: sandboxcontract.ProfileV1, Type: sandboxcontract.OperationFrameAcknowledgement, Identity: command.Identity, Ref: command.Ref, Acknowledgement: &ack},
			sandboxcontract.OperationFrame{Profile: sandboxcontract.ProfileV1, Type: sandboxcontract.OperationFrameEvent, Identity: command.Identity, Ref: command.Ref, Event: &event},
		)), nil
	})
	backend, err := NewTAEBackend("https://sandbox-gateway.internal", &http.Client{Transport: transport},
		&recordingSandboxTokenSource{token: "test-backend-capability-token"})
	if err != nil {
		t.Fatal(err)
	}
	exchange, err := backend.StartProcess(t.Context(), validTAEStartRequest())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exchange.AwaitAcknowledgement(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := exchange.NextEvent(t.Context()); executionbackend.OutcomeOf(err) != executionbackend.OutcomeUnknown || !strings.Contains(err.Error(), "stream_sequence_gap") {
		t.Fatalf("sequence-gap error = %v", err)
	}
}

func TestTAEBackendClassifiesPreSendAndAmbiguousFailures(t *testing.T) {
	request := validTAEStartRequest()
	backend, err := NewTAEBackend("https://sandbox-gateway.internal", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connection reset")
	})}, &recordingSandboxTokenSource{token: "test-backend-capability-token"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.StartProcess(t.Context(), request); executionbackend.OutcomeOf(err) != executionbackend.OutcomeUnknown {
		t.Fatalf("transport error = %v, want unknown", err)
	}

	tokenFailure, err := NewTAEBackend("https://sandbox-gateway.internal", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("transport called after token failure")
		return nil, nil
	})}, &recordingSandboxTokenSource{err: errors.New("issuer unavailable")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tokenFailure.StartProcess(t.Context(), request); !executionbackend.ProvesNotSent(err) {
		t.Fatalf("token failure = %v, want not_sent", err)
	}
}

func TestTAEBackendLogsOnlySafeDispatchMetadata(t *testing.T) {
	const (
		secretArgument = "secret-command-argument"
		secretToken    = "secret-lark-token"
		secretBody     = "secret-provider-response-body"
	)
	request := validTAEStartRequest()
	request.Arguments = []string{secretArgument}
	request.Environment = map[string]string{"LARK_ACCESS_TOKEN": secretToken}
	written := true
	var logs bytes.Buffer
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		body, err := json.Marshal(sandboxcontract.ErrorResponse{
			Code: "forbidden", Message: secretBody, Outcome: string(executionbackend.OutcomeRejected),
			ProviderRequestID: "provider-log-1", ProviderCode: "PermissionDenied",
			ProviderHTTPStatus: http.StatusForbidden, RequestWritten: &written,
		})
		if err != nil {
			t.Fatal(err)
		}
		return &http.Response{
			StatusCode: http.StatusConflict,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewReader(body)),
		}, nil
	})
	backend, err := NewTAEBackendWithLogger("https://sandbox-gateway.internal", &http.Client{Transport: transport},
		&recordingSandboxTokenSource{token: "test-backend-capability-token"}, slog.New(slog.NewJSONHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	_, err = backend.StartProcess(t.Context(), request)
	var dispatchError *executionbackend.DispatchError
	if !errors.As(err, &dispatchError) || dispatchError.Outcome != executionbackend.OutcomeRejected ||
		dispatchError.Code != "forbidden" || dispatchError.ProviderRequestID != "provider-log-1" ||
		dispatchError.ProviderCode != "PermissionDenied" || dispatchError.HTTPStatus != http.StatusForbidden ||
		dispatchError.RequestWritten == nil || !*dispatchError.RequestWritten {
		t.Fatalf("StartProcess() dispatch error = %#v", err)
	}
	logged := logs.String()
	for _, wanted := range []string{"managed backend dispatch failed", "provider-log-1", "PermissionDenied", `"provider_http_status":403`, `"request_written":true`} {
		if !strings.Contains(logged, wanted) {
			t.Fatalf("dispatch log %q does not contain %q", logged, wanted)
		}
	}
	for _, forbidden := range []string{secretArgument, secretToken, secretBody, "Authorization"} {
		if strings.Contains(logged, forbidden) {
			t.Fatalf("dispatch log leaked %q: %s", forbidden, logged)
		}
	}
}

func TestTAEBackendRejectsUnsafeDispatchMetadata(t *testing.T) {
	written := false
	body, err := json.Marshal(sandboxcontract.ErrorResponse{
		Code: "provider_unavailable", Message: "safe", Outcome: string(executionbackend.OutcomeUnknown),
		ProviderRequestID: "provider-log\nunsafe", RequestWritten: &written,
	})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := NewTAEBackend("https://sandbox-gateway.internal", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})}, &recordingSandboxTokenSource{token: "test-backend-capability-token"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = backend.StartProcess(t.Context(), validTAEStartRequest())
	var dispatchError *executionbackend.DispatchError
	if !errors.As(err, &dispatchError) || dispatchError.Code != "invalid_gateway_error" || dispatchError.Outcome != executionbackend.OutcomeUnknown {
		t.Fatalf("unsafe metadata error = %#v", err)
	}
}

func TestTAEBackendLogsStreamFailureWithoutPayload(t *testing.T) {
	const secretStreamPayload = "secret-stream-payload"
	var logs bytes.Buffer
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var command sandboxcontract.RunCommandRequest
		if err := json.NewDecoder(request.Body).Decode(&command); err != nil {
			t.Fatal(err)
		}
		body := encodeOperationFrames(t, sandboxcontract.OperationFrame{
			Profile: sandboxcontract.ProfileV1, Type: sandboxcontract.OperationFrameAcknowledgement,
			Identity: command.Identity, Ref: command.Ref,
			Acknowledgement: &executionbackend.Acknowledgement{AcceptedAt: time.Now().UTC()},
		})
		body = append(body, []byte(secretStreamPayload)...)
		return operationHTTPResponse(body), nil
	})
	backend, err := NewTAEBackendWithLogger("https://sandbox-gateway.internal", &http.Client{Transport: transport},
		&recordingSandboxTokenSource{token: "test-backend-capability-token"}, slog.New(slog.NewJSONHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	exchange, err := backend.StartProcess(t.Context(), validTAEStartRequest())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exchange.AwaitAcknowledgement(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := exchange.NextEvent(t.Context()); err == nil {
		t.Fatal("invalid stream unexpectedly produced an event")
	}
	logged := logs.String()
	for _, wanted := range []string{"managed backend exchange failed", "invalid_stream_json", `"sandbox_gateway_http_status":200`} {
		if !strings.Contains(logged, wanted) {
			t.Fatalf("exchange log %q does not contain %q", logged, wanted)
		}
	}
	if strings.Contains(logged, secretStreamPayload) {
		t.Fatalf("exchange log leaked provider stream payload: %s", logged)
	}
}

func TestTAEBackendRequiresCanonicalSecureOriginAndDisablesRedirects(t *testing.T) {
	tokens := &recordingSandboxTokenSource{token: "test-backend-capability-token"}
	for _, raw := range []string{
		"http://sandbox-gateway.internal",
		"https://user@sandbox-gateway.internal",
		"https://sandbox-gateway.internal/base",
		"https://sandbox-gateway.internal?query=1",
		"https://sandbox-gateway.internal#fragment",
	} {
		if _, err := NewTAEBackend(raw, http.DefaultClient, tokens); err == nil {
			t.Fatalf("NewTAEBackend(%q) accepted an unsafe origin", raw)
		}
	}
	original := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return nil }}
	backend, err := NewTAEBackend("https://sandbox-gateway.internal/", original, tokens)
	if err != nil {
		t.Fatal(err)
	}
	if backend.baseURL.Path != "" || backend.httpClient == original {
		t.Fatalf("backend did not normalize origin or copy HTTP client: %+v", backend.baseURL)
	}
	if err := backend.httpClient.CheckRedirect(&http.Request{}, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("redirect policy = %v, want ErrUseLastResponse", err)
	}
	if _, err := NewTAEBackend("http://127.0.0.1:8080", original, tokens); err != nil {
		t.Fatalf("loopback cleartext should remain available for insecure development: %v", err)
	}
}

func validTAEStartRequest() executionbackend.StartProcessRequest {
	return executionbackend.StartProcessRequest{
		Target: executionbackend.Target{Kind: executionbackend.KindTAE, ID: "tae-sandbox-1", Generation: 3, EnvironmentID: "managed-env-1"},
		Operation: executionbackend.OperationContext{
			WorkspaceID: "workspace-1", SessionID: "session-1", RunID: "run-1",
			RunAttemptID: "attempt-1", RunAttemptGeneration: 1,
			ExecutionID: "execution-1", OperationID: "operation-1", MutationKey: "mutation-1",
		},
		RequestID: "request-1", ProcessID: "process-1", Executable: "lark-cli",
		Arguments: []string{"doc", "get", "a value with spaces"}, WorkingDirectory: "/workspace",
		WorkspaceRoot: "/workspace", Platform: "linux-amd64",
		Environment: map[string]string{"LARK_AUTHORIZATION": "placeholder-value"},
		Timeout:     30 * time.Second, OutputLimitBytes: 64 * 1024,
	}
}

func encodeOperationFrames(t *testing.T, frames ...sandboxcontract.OperationFrame) []byte {
	t.Helper()
	var raw bytes.Buffer
	encoder := json.NewEncoder(&raw)
	encoder.SetEscapeHTML(false)
	for _, frame := range frames {
		if err := frame.Validate(); err != nil {
			t.Fatal(err)
		}
		if err := encoder.Encode(frame); err != nil {
			t.Fatal(err)
		}
	}
	return raw.Bytes()
}

func operationHTTPResponse(body []byte) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/x-ndjson"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
}

type recordingSandboxTokenSource struct {
	token    string
	err      error
	requests []SandboxGatewayTokenRequest
}

func (source *recordingSandboxTokenSource) Token(_ context.Context, request SandboxGatewayTokenRequest) (string, error) {
	source.requests = append(source.requests, request)
	return source.token, source.err
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

var _ SandboxGatewayTokenSource = (*recordingSandboxTokenSource)(nil)
var _ http.RoundTripper = roundTripFunc(nil)
