package executorgateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/agentserver/agentserver/v2/internal/executionbackend"
	"github.com/agentserver/agentserver/v2/internal/sandboxcontract"
)

const (
	maxSandboxGatewayErrorBytes  = 256 * 1024
	maxSandboxGatewayStreamBytes = 64 * 1024 * 1024
	operationStreamMediaType     = "application/x-ndjson"
	taeActionRunCommand          = "run_command"
	taeActionSignalCommand       = "signal_command"
	taeActionReadFile            = "read_file"
)

type SandboxGatewayTokenRequest struct {
	Action    string
	Target    executionbackend.Target
	Operation executionbackend.OperationContext
}

type SandboxGatewayTokenSource interface {
	Token(context.Context, SandboxGatewayTokenRequest) (string, error)
}

type TAEBackend struct {
	baseURL    *url.URL
	httpClient *http.Client
	tokens     SandboxGatewayTokenSource
}

func NewTAEBackend(baseURL string, httpClient *http.Client, tokens SandboxGatewayTokenSource) (*TAEBackend, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawPath != "" ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" || parsed.ForceQuery ||
		(parsed.Path != "" && parsed.Path != "/") || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("sandbox-gateway base URL must be an absolute canonical HTTP(S) origin")
	}
	if parsed.Scheme == "http" && !taeLoopbackHost(parsed.Hostname()) {
		return nil, errors.New("cleartext sandbox-gateway URL is allowed only on loopback")
	}
	if httpClient == nil || tokens == nil {
		return nil, errors.New("sandbox-gateway HTTP client and backend token source are required")
	}
	parsed.Path = ""
	clientCopy := *httpClient
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &TAEBackend{baseURL: parsed, httpClient: &clientCopy, tokens: tokens}, nil
}

func taeLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func (*TAEBackend) Kind() executionbackend.Kind { return executionbackend.KindTAE }

func (backend *TAEBackend) StartProcess(ctx context.Context, request executionbackend.StartProcessRequest) (executionbackend.Exchange, error) {
	if err := request.Validate(); err != nil {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "invalid_request", err)
	}
	if request.Target.Kind != executionbackend.KindTAE {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "wrong_backend_kind", errors.New("TAE backend requires a TAE target"))
	}
	if request.TTY {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "tty_unsupported", errors.New("TAE managed executor v1 does not support TTY"))
	}
	if request.Timeout%time.Millisecond != 0 {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "invalid_timeout", errors.New("TAE command timeout must be whole milliseconds"))
	}
	path, err := sandboxcontract.RunCommandPath(request.Target.ID)
	if err != nil {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "invalid_target", err)
	}
	contractRequest := sandboxcontract.RunCommandRequest{
		Profile: sandboxcontract.ProfileV1, RequestID: request.RequestID,
		Identity:  backendOperationIdentity(request.Operation, request.Target.EnvironmentID),
		Ref:       sandboxcontract.SandboxRef{SandboxID: request.Target.ID, TargetGeneration: request.Target.Generation},
		ProcessID: request.ProcessID, Executable: request.Executable,
		Arguments: append([]string(nil), request.Arguments...), WorkingDirectory: request.WorkingDirectory,
		Environment: cloneTAEEnvironment(request.Environment), TimeoutMillis: request.Timeout.Milliseconds(),
		OutputLimitBytes: request.OutputLimitBytes,
	}
	return backend.openExchange(ctx, taeActionRunCommand, path, request.Target, request.Operation, contractRequest)
}

func (backend *TAEBackend) SignalProcess(ctx context.Context, request executionbackend.SignalProcessRequest) (executionbackend.Exchange, error) {
	if err := request.Validate(); err != nil {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "invalid_request", err)
	}
	if request.Target.Kind != executionbackend.KindTAE {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "wrong_backend_kind", errors.New("TAE backend requires a TAE target"))
	}
	path, err := sandboxcontract.SignalProcessPath(request.Target.ID, request.ProcessID)
	if err != nil {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "invalid_target", err)
	}
	contractRequest := sandboxcontract.SignalCommandRequest{
		Profile: sandboxcontract.ProfileV1, RequestID: request.RequestID,
		Identity:  backendOperationIdentity(request.Operation, request.Target.EnvironmentID),
		Ref:       sandboxcontract.SandboxRef{SandboxID: request.Target.ID, TargetGeneration: request.Target.Generation},
		ProcessID: request.ProcessID, ProviderHandle: request.ProviderHandle,
		Signal: request.Signal, Reason: request.Reason,
	}
	return backend.openExchange(ctx, taeActionSignalCommand, path, request.Target, request.Operation, contractRequest)
}

func (backend *TAEBackend) ReadFile(ctx context.Context, request executionbackend.ReadFileRequest) (executionbackend.Exchange, error) {
	if err := request.Validate(); err != nil {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "invalid_request", err)
	}
	if request.Target.Kind != executionbackend.KindTAE {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "wrong_backend_kind", errors.New("TAE backend requires a TAE target"))
	}
	path, err := sandboxcontract.ReadFilePath(request.Target.ID)
	if err != nil {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "invalid_target", err)
	}
	contractRequest := sandboxcontract.ReadFileRequest{
		Profile: sandboxcontract.ProfileV1, RequestID: request.RequestID,
		Identity: backendOperationIdentity(request.Operation, request.Target.EnvironmentID),
		Ref:      sandboxcontract.SandboxRef{SandboxID: request.Target.ID, TargetGeneration: request.Target.Generation},
		Path:     request.Path, Offset: request.Offset, Limit: request.Limit,
	}
	return backend.openExchange(ctx, taeActionReadFile, path, request.Target, request.Operation, contractRequest)
}

func (backend *TAEBackend) openExchange(ctx context.Context, action, path string, target executionbackend.Target, operation executionbackend.OperationContext, command any) (executionbackend.Exchange, error) {
	if ctx == nil {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "invalid_context", errors.New("TAE backend context is required"))
	}
	token, err := backend.tokens.Token(ctx, SandboxGatewayTokenRequest{Action: action, Target: target, Operation: operation})
	if err != nil {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "capability_unavailable", err)
	}
	if !validBackendToken(token) {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "invalid_capability", errors.New("sandbox-gateway backend token is invalid"))
	}
	var raw bytes.Buffer
	encoder := json.NewEncoder(&raw)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(command); err != nil {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "encode_failed", err)
	}
	endpoint := *backend.baseURL
	endpoint.Path = path
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(raw.Bytes()))
	if err != nil {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "request_construction_failed", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/x-ndjson, application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+token)
	httpResponse, err := backend.httpClient.Do(httpRequest)
	if err != nil {
		// net/http cannot generally prove whether request bytes reached the
		// peer. Treat every Do error as ambiguous unless a lower transport with
		// stronger evidence is introduced explicitly.
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeUnknown, "sandbox_gateway_transport", err)
	}
	if httpResponse.StatusCode != http.StatusOK {
		defer httpResponse.Body.Close()
		return nil, decodeSandboxGatewayDispatchError(httpResponse)
	}
	mediaType, _, err := mime.ParseMediaType(httpResponse.Header.Get("Content-Type"))
	if err != nil || mediaType != operationStreamMediaType {
		httpResponse.Body.Close()
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeUnknown, "invalid_stream_content_type", errors.New("sandbox-gateway returned a non-NDJSON operation stream"))
	}
	return newTAEHTTPExchange(target, operation, backendOperationIdentity(operation, target.EnvironmentID),
		sandboxcontract.SandboxRef{SandboxID: target.ID, TargetGeneration: target.Generation}, httpResponse.Body), nil
}

func decodeSandboxGatewayDispatchError(response *http.Response) error {
	body, err := io.ReadAll(io.LimitReader(response.Body, maxSandboxGatewayErrorBytes+1))
	if err != nil || len(body) > maxSandboxGatewayErrorBytes {
		return executionbackend.NewDispatchError(executionbackend.OutcomeUnknown, "invalid_gateway_error", errors.New("sandbox-gateway error response could not be read safely"))
	}
	var contractError sandboxcontract.ErrorResponse
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&contractError); err != nil || contractError.Code == "" || contractError.Message == "" {
		return executionbackend.NewDispatchError(executionbackend.OutcomeUnknown, "invalid_gateway_error", errors.New("sandbox-gateway returned an invalid error document"))
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return executionbackend.NewDispatchError(executionbackend.OutcomeUnknown, "invalid_gateway_error", errors.New("sandbox-gateway error document contains trailing JSON"))
	}
	outcome := executionbackend.DispatchOutcome(contractError.Outcome)
	if outcome.Validate() != nil || outcome == executionbackend.OutcomeAccepted {
		outcome = executionbackend.OutcomeUnknown
	}
	code := contractError.Code
	if !validReasonCode(code) {
		code = "gateway_rejected"
	}
	return executionbackend.NewDispatchError(outcome, code, errors.New("sandbox-gateway rejected the operation"))
}

type taeHTTPExchange struct {
	target    executionbackend.Target
	operation executionbackend.OperationContext
	identity  sandboxcontract.OperationIdentity
	ref       sandboxcontract.SandboxRef
	body      io.ReadCloser
	decoder   *json.Decoder

	mu       sync.Mutex
	ack      *executionbackend.Acknowledgement
	terminal *executionbackend.TerminalResult
	pending  []executionbackend.Event
	lastSeq  uint64
	err      error
	done     chan struct{}
	doneOnce sync.Once
}

func newTAEHTTPExchange(target executionbackend.Target, operation executionbackend.OperationContext, identity sandboxcontract.OperationIdentity, ref sandboxcontract.SandboxRef, body io.ReadCloser) *taeHTTPExchange {
	limited := io.LimitReader(body, maxSandboxGatewayStreamBytes)
	decoder := json.NewDecoder(bufio.NewReader(limited))
	decoder.DisallowUnknownFields()
	return &taeHTTPExchange{
		target: target, operation: operation, identity: identity, ref: ref,
		body: body, decoder: decoder, done: make(chan struct{}),
	}
}

func (exchange *taeHTTPExchange) Target() executionbackend.Target { return exchange.target }
func (exchange *taeHTTPExchange) Operation() executionbackend.OperationContext {
	return exchange.operation
}

func (exchange *taeHTTPExchange) AwaitAcknowledgement(ctx context.Context) (executionbackend.Acknowledgement, error) {
	exchange.mu.Lock()
	defer exchange.mu.Unlock()
	if exchange.ack != nil {
		return *exchange.ack, nil
	}
	if exchange.err != nil {
		return executionbackend.Acknowledgement{}, exchange.err
	}
	if err := contextReady(ctx); err != nil {
		return executionbackend.Acknowledgement{}, err
	}
	frame, err := exchange.decodeFrame()
	if err != nil {
		return executionbackend.Acknowledgement{}, err
	}
	if frame.Type != sandboxcontract.OperationFrameAcknowledgement || frame.Acknowledgement == nil {
		return executionbackend.Acknowledgement{}, exchange.fail("missing_acknowledgement", errors.New("sandbox-gateway stream did not begin with acknowledgement"))
	}
	ack := *frame.Acknowledgement
	exchange.ack = &ack
	return ack, nil
}

func (exchange *taeHTTPExchange) NextEvent(ctx context.Context) (executionbackend.Event, error) {
	if _, err := exchange.AwaitAcknowledgement(ctx); err != nil {
		return executionbackend.Event{}, err
	}
	exchange.mu.Lock()
	defer exchange.mu.Unlock()
	if len(exchange.pending) > 0 {
		event := exchange.pending[0]
		exchange.pending = exchange.pending[1:]
		return event, nil
	}
	if exchange.terminal != nil {
		return executionbackend.Event{}, io.EOF
	}
	if exchange.err != nil {
		return executionbackend.Event{}, exchange.err
	}
	if err := contextReady(ctx); err != nil {
		return executionbackend.Event{}, err
	}
	frame, err := exchange.decodeFrame()
	if err != nil {
		return executionbackend.Event{}, err
	}
	switch frame.Type {
	case sandboxcontract.OperationFrameEvent:
		return *frame.Event, nil
	case sandboxcontract.OperationFrameTerminal:
		terminal := *frame.Terminal
		exchange.terminal = &terminal
		exchange.finish()
		return executionbackend.Event{}, io.EOF
	default:
		return executionbackend.Event{}, exchange.fail("unexpected_stream_frame", errors.New("sandbox-gateway stream repeated acknowledgement"))
	}
}

func (exchange *taeHTTPExchange) AwaitTerminal(ctx context.Context) (executionbackend.TerminalResult, error) {
	if _, err := exchange.AwaitAcknowledgement(ctx); err != nil {
		return executionbackend.TerminalResult{}, err
	}
	for {
		exchange.mu.Lock()
		if exchange.terminal != nil {
			terminal := *exchange.terminal
			exchange.mu.Unlock()
			return terminal, nil
		}
		if exchange.err != nil {
			err := exchange.err
			exchange.mu.Unlock()
			return executionbackend.TerminalResult{}, err
		}
		if err := contextReady(ctx); err != nil {
			exchange.mu.Unlock()
			return executionbackend.TerminalResult{}, err
		}
		frame, err := exchange.decodeFrame()
		if err != nil {
			exchange.mu.Unlock()
			return executionbackend.TerminalResult{}, err
		}
		switch frame.Type {
		case sandboxcontract.OperationFrameEvent:
			exchange.pending = append(exchange.pending, *frame.Event)
			exchange.mu.Unlock()
		case sandboxcontract.OperationFrameTerminal:
			terminal := *frame.Terminal
			exchange.terminal = &terminal
			exchange.finish()
			exchange.mu.Unlock()
			return terminal, nil
		default:
			err := exchange.fail("unexpected_stream_frame", errors.New("sandbox-gateway stream repeated acknowledgement"))
			exchange.mu.Unlock()
			return executionbackend.TerminalResult{}, err
		}
	}
}

func (exchange *taeHTTPExchange) Done() <-chan struct{} { return exchange.done }

func (exchange *taeHTTPExchange) decodeFrame() (sandboxcontract.OperationFrame, error) {
	var frame sandboxcontract.OperationFrame
	if err := exchange.decoder.Decode(&frame); err != nil {
		if errors.Is(err, io.EOF) {
			return sandboxcontract.OperationFrame{}, exchange.fail("stream_ended_early", errors.New("sandbox-gateway stream ended before terminal"))
		}
		return sandboxcontract.OperationFrame{}, exchange.fail("invalid_stream_json", err)
	}
	if err := frame.Validate(); err != nil {
		return sandboxcontract.OperationFrame{}, exchange.fail("invalid_stream_frame", err)
	}
	if frame.Identity != exchange.identity || frame.Ref != exchange.ref {
		return sandboxcontract.OperationFrame{}, exchange.fail("stream_identity_mismatch", errors.New("sandbox-gateway stream frame changed operation identity"))
	}
	if frame.Type == sandboxcontract.OperationFrameEvent {
		if frame.Event.Sequence != exchange.lastSeq+1 {
			return sandboxcontract.OperationFrame{}, exchange.fail("stream_sequence_gap", errors.New("sandbox-gateway event sequence is not contiguous"))
		}
		exchange.lastSeq = frame.Event.Sequence
	}
	return frame, nil
}

func (exchange *taeHTTPExchange) fail(code string, cause error) error {
	if exchange.err == nil {
		exchange.err = executionbackend.NewDispatchError(executionbackend.OutcomeUnknown, code, cause)
		exchange.finish()
	}
	return exchange.err
}

func (exchange *taeHTTPExchange) finish() {
	exchange.doneOnce.Do(func() {
		_ = exchange.body.Close()
		close(exchange.done)
	})
}

func backendOperationIdentity(operation executionbackend.OperationContext, environmentID string) sandboxcontract.OperationIdentity {
	return sandboxcontract.OperationIdentity{
		Session: sandboxcontract.SessionIdentity{
			WorkspaceID: operation.WorkspaceID, SessionID: operation.SessionID, EnvironmentID: environmentID,
		},
		RunID: operation.RunID, RunAttemptID: operation.RunAttemptID,
		RunAttemptGeneration: operation.RunAttemptGeneration,
		ExecutionID:          operation.ExecutionID, OperationID: operation.OperationID, MutationKey: operation.MutationKey,
	}
}

func cloneTAEEnvironment(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func validBackendToken(token string) bool {
	return len(token) >= 16 && len(token) <= 32768 && strings.TrimSpace(token) == token && !strings.ContainsAny(token, "\r\n\x00")
}

func validReasonCode(code string) bool {
	if code == "" || len(code) > 64 {
		return false
	}
	for index, character := range code {
		if index == 0 {
			if character < 'a' || character > 'z' {
				return false
			}
			continue
		}
		if !((character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '_') {
			return false
		}
	}
	return true
}

func contextReady(ctx context.Context) error {
	if ctx == nil {
		return errors.New("exchange context is required")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

var _ executionbackend.Backend = (*TAEBackend)(nil)
var _ executionbackend.Exchange = (*taeHTTPExchange)(nil)
