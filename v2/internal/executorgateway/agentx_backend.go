package executorgateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/agentserver/agentserver/v2/internal/codexwire"
	"github.com/agentserver/agentserver/v2/internal/execprofile"
	"github.com/agentserver/agentserver/v2/internal/executionbackend"
	"github.com/agentserver/agentserver/v2/internal/executorgateway/agentxconn"
)

// AgentXBackend is the compatibility adapter around the existing BYO
// dispatchers. It deliberately reuses the existing RPC structs and encoders;
// the adapter adds no fields to the agentx frame and does not change session,
// journal, or connection-generation behavior.
type AgentXBackend struct {
	processes ProcessDispatcher
	files     FilesystemDispatcher
	now       func() time.Time
}

func NewAgentXBackend(processes ProcessDispatcher, files FilesystemDispatcher, now func() time.Time) (*AgentXBackend, error) {
	if processes == nil || files == nil {
		return nil, errors.New("agentx process and filesystem dispatchers are required")
	}
	if now == nil {
		return nil, errors.New("agentx backend clock is required")
	}
	return &AgentXBackend{processes: processes, files: files, now: now}, nil
}

func (backend *AgentXBackend) Kind() executionbackend.Kind { return executionbackend.KindAgentX }

func (backend *AgentXBackend) StartProcess(ctx context.Context, request executionbackend.StartProcessRequest) (executionbackend.Exchange, error) {
	if err := validateAgentXStartRequest(request); err != nil {
		return nil, agentXNotSent("invalid_start_request", request.RequestID, err)
	}
	rpc, routing, directives, err := mapAgentXStartRequest(request)
	if err != nil {
		return nil, agentXNotSent("encode_start_request", request.RequestID, err)
	}
	raw, dispatchErr := backend.processes.DispatchProcess(ctx, ProcessDispatchRequest{
		ExecutorID: request.Target.ID, ExpectedConnectionGeneration: request.Target.Generation,
		Context: routing, Directives: directives, RPC: rpc,
	})
	if raw == nil {
		return nil, agentXNotSent("start_not_sent", request.RequestID, dispatchErr)
	}
	exchange := &agentXProcessExchange{request: request, raw: raw, now: backend.now}
	if dispatchErr != nil {
		return exchange, agentXUnknown("start_ambiguous", request.RequestID, dispatchErr)
	}
	return exchange, nil
}

func (backend *AgentXBackend) SignalProcess(ctx context.Context, request executionbackend.SignalProcessRequest) (executionbackend.Exchange, error) {
	if err := validateAgentXSignalRequest(request); err != nil {
		return nil, agentXNotSent("invalid_signal_request", request.RequestID, err)
	}
	rpc, routing, err := mapAgentXSignalRequest(request)
	if err != nil {
		return nil, agentXNotSent("encode_signal_request", request.RequestID, err)
	}
	raw, dispatchErr := backend.processes.DispatchProcess(ctx, ProcessDispatchRequest{
		ExecutorID: request.Target.ID, ExpectedConnectionGeneration: request.Target.Generation,
		Context: routing, RPC: rpc,
	})
	if raw == nil {
		return nil, agentXNotSent("signal_not_sent", request.RequestID, dispatchErr)
	}
	exchange := &agentXUnaryProcessExchange{
		target: request.Target, operation: request.Operation, requestID: request.RequestID,
		processID: request.ProcessID, method: execprofile.MethodProcessTerminate,
		raw: raw, now: backend.now,
	}
	if dispatchErr != nil {
		return exchange, agentXUnknown("signal_ambiguous", request.RequestID, dispatchErr)
	}
	return exchange, nil
}

func (backend *AgentXBackend) ReadFile(ctx context.Context, request executionbackend.ReadFileRequest) (executionbackend.Exchange, error) {
	if err := validateAgentXReadRequest(request); err != nil {
		return nil, agentXNotSent("invalid_read_request", request.RequestID, err)
	}
	rpc, routing, err := mapAgentXReadRequest(request)
	if err != nil {
		return nil, agentXNotSent("encode_read_request", request.RequestID, err)
	}
	raw, dispatchErr := backend.files.DispatchFilesystem(ctx, FilesystemDispatchRequest{
		ExecutorID: request.Target.ID, ExpectedConnectionGeneration: request.Target.Generation,
		Context: routing, RPC: rpc,
	})
	if raw == nil {
		return nil, agentXNotSent("read_not_sent", request.RequestID, dispatchErr)
	}
	exchange := &agentXFileExchange{request: request, raw: raw, now: backend.now}
	if dispatchErr != nil {
		return exchange, agentXUnknown("read_ambiguous", request.RequestID, dispatchErr)
	}
	return exchange, nil
}

func validateAgentXStartRequest(request executionbackend.StartProcessRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if request.Target.Kind != executionbackend.KindAgentX {
		return errors.New("agentx backend received a non-agentx target")
	}
	if request.OutputLimitBytes > maximumShellMaxOutputBytes {
		return fmt.Errorf("agentx shell output limit exceeds %d", maximumShellMaxOutputBytes)
	}
	return nil
}

func validateAgentXSignalRequest(request executionbackend.SignalProcessRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if request.Target.Kind != executionbackend.KindAgentX {
		return errors.New("agentx backend received a non-agentx target")
	}
	if request.Signal != executionbackend.SignalTerminate {
		return errors.New("agentx v2 supports only the terminate signal")
	}
	return nil
}

func validateAgentXReadRequest(request executionbackend.ReadFileRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if request.Target.Kind != executionbackend.KindAgentX {
		return errors.New("agentx backend received a non-agentx target")
	}
	if request.Offset > execprofile.MaxFilesystemReadOffset || request.Limit > execprofile.MaxFilesystemReadLength {
		return errors.New("agentx bounded filesystem read limit is exceeded")
	}
	return nil
}

func mapAgentXStartRequest(request executionbackend.StartProcessRequest) (json.RawMessage, agentxconn.RoutingContext, *agentxconn.DispatchDirectives, error) {
	rootURI, cwdURI, err := agentXPathURIs(request.Platform, request.WorkspaceRoot, request.WorkingDirectory)
	if err != nil {
		return nil, agentxconn.RoutingContext{}, nil, err
	}
	argv := make([]string, 0, len(request.Arguments)+1)
	argv = append(argv, request.Executable)
	argv = append(argv, request.Arguments...)
	params, err := json.Marshal(shellProcessStartParams{
		ProcessID: request.ProcessID, Argv: argv, CWD: cwdURI,
		Env: cloneStringMap(request.Environment),
		EnvPolicy: shellCleanEnvironmentPolicy{
			Inherit: "none", IgnoreDefaultExcludes: false,
			Exclude: []string{}, Set: map[string]string{}, IncludeOnly: []string{},
		},
		TTY: request.TTY, PipeStdin: false, Arg0: nil,
		Sandbox: shellSandboxContext{
			Permissions: shellSandboxPermissions{
				Type: "managed",
				FileSystem: shellSandboxFileSystem{Type: "restricted", Entries: []shellSandboxEntry{
					{Path: shellSandboxPath{Type: "special", Value: &shellSandboxSpecialPath{Kind: "minimal"}}, Access: "read"},
					{Path: shellSandboxPath{Type: "path", Path: rootURI}, Access: "write"},
				}},
				Network: "restricted",
			},
			CWD: cwdURI, WorkspaceRoots: []string{rootURI},
			WindowsSandboxLevel:          shellWindowsSandboxLevel(request.Platform),
			WindowsSandboxPrivateDesktop: false, UseLegacyLandlock: false,
		},
		EnforceManagedNetwork: true,
	})
	if err != nil {
		return nil, agentxconn.RoutingContext{}, nil, err
	}
	rpc, err := marshalShellRPC(request.RequestID, execprofile.MethodProcessStart, params)
	if err != nil {
		return nil, agentxconn.RoutingContext{}, nil, err
	}
	routing := agentXRouting(request.Target, request.Operation)
	var directives *agentxconn.DispatchDirectives
	if notification := request.DeadlineNotification; notification != nil {
		directives = &agentxconn.DispatchDirectives{ProcessTimeout: &agentxconn.ProcessTimeoutDirective{
			AfterMillis: notification.After.Milliseconds(),
			OperationID: notification.Operation.OperationID,
			MutationKey: notification.Operation.MutationKey,
		}}
	}
	return rpc, routing, directives, nil
}

func mapAgentXSignalRequest(request executionbackend.SignalProcessRequest) (json.RawMessage, agentxconn.RoutingContext, error) {
	params, err := json.Marshal(struct {
		ProcessID string `json:"processId"`
	}{ProcessID: request.ProcessID})
	if err != nil {
		return nil, agentxconn.RoutingContext{}, err
	}
	rpc, err := marshalShellRPC(request.RequestID, execprofile.MethodProcessTerminate, params)
	return rpc, agentXRouting(request.Target, request.Operation), err
}

func mapAgentXReadRequest(request executionbackend.ReadFileRequest) (json.RawMessage, agentxconn.RoutingContext, error) {
	pathURI, err := agentXAbsolutePathURI(request.Path)
	if err != nil {
		return nil, agentxconn.RoutingContext{}, err
	}
	params, err := json.Marshal(readFileBlockParams{Path: pathURI, Offset: request.Offset, Length: request.Limit})
	if err != nil {
		return nil, agentxconn.RoutingContext{}, err
	}
	rpc, err := marshalReadFileRPC(request.RequestID, params)
	return rpc, agentXRouting(request.Target, request.Operation), err
}

func agentXRouting(target executionbackend.Target, operation executionbackend.OperationContext) agentxconn.RoutingContext {
	return agentxconn.RoutingContext{
		WorkspaceID: operation.WorkspaceID, RunID: operation.RunID,
		RunAttemptID: operation.RunAttemptID, RunAttemptGeneration: operation.RunAttemptGeneration,
		ExecutionID: operation.ExecutionID, OperationID: operation.OperationID,
		EnvID: target.EnvironmentID, MutationKey: operation.MutationKey,
	}
}

func agentXPathURIs(platform, workspaceRoot, workingDirectory string) (string, string, error) {
	if err := validateRegisteredRoot(platform, workspaceRoot); err != nil {
		return "", "", err
	}
	if strings.HasPrefix(platform, "windows-") {
		normalizedRoot := strings.ReplaceAll(workspaceRoot, `\`, "/")
		normalizedCWD := strings.ReplaceAll(workingDirectory, `\`, "/")
		prefix := strings.TrimSuffix(strings.ToLower(normalizedRoot), "/") + "/"
		if strings.ToLower(normalizedCWD) != strings.ToLower(normalizedRoot) && !strings.HasPrefix(strings.ToLower(normalizedCWD), prefix) {
			return "", "", errors.New("agentx working directory is outside the workspace root")
		}
		return (&url.URL{Scheme: "file", Path: "/" + normalizedRoot}).String(), (&url.URL{Scheme: "file", Path: "/" + normalizedCWD}).String(), nil
	}
	if !strings.HasPrefix(workingDirectory, "/") || path.Clean(workingDirectory) != workingDirectory {
		return "", "", errors.New("agentx working directory must be a clean absolute path")
	}
	prefix := strings.TrimSuffix(workspaceRoot, "/") + "/"
	if workingDirectory != workspaceRoot && !strings.HasPrefix(workingDirectory, prefix) {
		return "", "", errors.New("agentx working directory is outside the workspace root")
	}
	return (&url.URL{Scheme: "file", Path: workspaceRoot}).String(), (&url.URL{Scheme: "file", Path: workingDirectory}).String(), nil
}

func agentXAbsolutePathURI(value string) (string, error) {
	if strings.HasPrefix(value, "/") && path.Clean(value) == value {
		return (&url.URL{Scheme: "file", Path: value}).String(), nil
	}
	if len(value) >= 3 && value[1] == ':' {
		normalized := strings.ReplaceAll(value, `\`, "/")
		return (&url.URL{Scheme: "file", Path: "/" + normalized}).String(), nil
	}
	return "", errors.New("agentx file path must be an absolute clean path")
}

func agentXNotSent(code, requestID string, cause error) error {
	if cause == nil {
		cause = errors.New("agentx dispatcher did not create an exchange")
	}
	dispatchErr := executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, code, cause)
	dispatchErr.ProviderRequestID = requestID
	return dispatchErr
}

func agentXUnknown(code, requestID string, cause error) error {
	if cause == nil {
		cause = errors.New("agentx dispatch result is unknown")
	}
	dispatchErr := executionbackend.NewDispatchError(executionbackend.OutcomeUnknown, code, cause)
	dispatchErr.ProviderRequestID = requestID
	return dispatchErr
}

func agentXRejected(code, requestID string, cause error) error {
	dispatchErr := executionbackend.NewDispatchError(executionbackend.OutcomeRejected, code, cause)
	dispatchErr.ProviderRequestID = requestID
	return dispatchErr
}

type agentXProcessExchange struct {
	request executionbackend.StartProcessRequest
	raw     *ProcessExchange
	now     func() time.Time

	ackOnce sync.Once
	ack     executionbackend.Acknowledgement
	ackErr  error

	consumeMu sync.Mutex
	exitCode  *int32
	denied    bool
	closed    bool
	terminal  executionbackend.TerminalResult
	termErr   error
}

func (exchange *agentXProcessExchange) Target() executionbackend.Target {
	return exchange.request.Target
}
func (exchange *agentXProcessExchange) Operation() executionbackend.OperationContext {
	return exchange.request.Operation
}
func (exchange *agentXProcessExchange) Done() <-chan struct{} { return exchange.raw.Done() }

func (exchange *agentXProcessExchange) AwaitAcknowledgement(ctx context.Context) (executionbackend.Acknowledgement, error) {
	exchange.ackOnce.Do(func() {
		raw, err := exchange.raw.AwaitResponse(ctx)
		if err != nil {
			exchange.ackErr = agentXUnknown("start_response_unknown", exchange.request.RequestID, err)
			return
		}
		succeeded, rpcError, err := classifyShellProcessResponse(raw, execprofile.MethodProcessStart, exchange.request.ProcessID)
		if err != nil {
			exchange.ackErr = agentXUnknown("invalid_start_response", exchange.request.RequestID, err)
			return
		}
		if rpcError || !succeeded {
			exchange.ackErr = agentXRejected("start_rejected", exchange.request.RequestID, errors.New("agentx rejected process/start"))
			return
		}
		exchange.ack = executionbackend.Acknowledgement{
			ProviderOperationID: exchange.request.ProcessID,
			ProviderRequestID:   exchange.request.RequestID, AcceptedAt: exchange.now().UTC(),
		}
	})
	return exchange.ack, exchange.ackErr
}

func (exchange *agentXProcessExchange) NextEvent(ctx context.Context) (executionbackend.Event, error) {
	exchange.consumeMu.Lock()
	defer exchange.consumeMu.Unlock()
	for {
		raw, err := exchange.raw.NextEvent(ctx)
		if err != nil {
			return executionbackend.Event{}, err
		}
		message, err := codexwire.Parse(raw)
		if err != nil || message.Kind != codexwire.KindNotification {
			return executionbackend.Event{}, agentXUnknown("invalid_process_event", exchange.request.RequestID, errors.New("agentx process event is not a notification"))
		}
		switch message.Method {
		case execprofile.NotificationProcessOutput:
			var output struct {
				ProcessID string `json:"processId"`
				Sequence  uint64 `json:"seq"`
				Stream    string `json:"stream"`
				Chunk     string `json:"chunk"`
			}
			if err := decodeExactJSON(message.Params, &output); err != nil || output.ProcessID != exchange.request.ProcessID {
				return executionbackend.Event{}, agentXUnknown("invalid_process_output", exchange.request.RequestID, errors.New("agentx process output identity differs"))
			}
			data, err := base64.StdEncoding.DecodeString(output.Chunk)
			if err != nil || base64.StdEncoding.EncodeToString(data) != output.Chunk || len(data) == 0 {
				return executionbackend.Event{}, agentXUnknown("invalid_process_output", exchange.request.RequestID, errors.New("agentx process output is not canonical base64"))
			}
			kind := executionbackend.EventStdout
			if output.Stream == "stderr" {
				kind = executionbackend.EventStderr
			} else if output.Stream != "stdout" {
				return executionbackend.Event{}, agentXUnknown("invalid_process_output", exchange.request.RequestID, errors.New("agentx process output stream is invalid"))
			}
			return executionbackend.Event{Sequence: output.Sequence, Kind: kind, Data: data}, nil
		case execprofile.NotificationProcessExited:
			var exited struct {
				ProcessID     string `json:"processId"`
				Sequence      uint64 `json:"seq"`
				ExitCode      int32  `json:"exitCode"`
				SandboxDenied *bool  `json:"sandboxDenied"`
			}
			if err := decodeExactJSON(message.Params, &exited); err != nil || exited.ProcessID != exchange.request.ProcessID || exited.SandboxDenied == nil || exchange.exitCode != nil {
				return executionbackend.Event{}, agentXUnknown("invalid_process_exit", exchange.request.RequestID, errors.New("agentx process exit differs"))
			}
			exitCode := exited.ExitCode
			exchange.exitCode = &exitCode
			exchange.denied = *exited.SandboxDenied
		case execprofile.NotificationProcessClosed:
			var closed struct {
				ProcessID string `json:"processId"`
				Sequence  uint64 `json:"seq"`
			}
			if err := decodeExactJSON(message.Params, &closed); err != nil || closed.ProcessID != exchange.request.ProcessID || exchange.exitCode == nil || exchange.closed {
				return executionbackend.Event{}, agentXUnknown("invalid_process_close", exchange.request.RequestID, errors.New("agentx process close differs"))
			}
			exchange.closed = true
			status := executionbackend.TerminalFailed
			if *exchange.exitCode == 0 && !exchange.denied {
				status = executionbackend.TerminalSucceeded
			}
			exchange.terminal = executionbackend.TerminalResult{
				Status: status, ExitCode: cloneInt32(exchange.exitCode),
				OutputComplete: true, CompletedAt: exchange.now().UTC(),
			}
		default:
			return executionbackend.Event{}, agentXUnknown("unsupported_process_event", exchange.request.RequestID, fmt.Errorf("unsupported agentx process event %q", message.Method))
		}
	}
}

func (exchange *agentXProcessExchange) AwaitTerminal(ctx context.Context) (executionbackend.TerminalResult, error) {
	if _, err := exchange.AwaitAcknowledgement(ctx); err != nil {
		return executionbackend.TerminalResult{}, err
	}
	for {
		exchange.consumeMu.Lock()
		terminal := cloneBackendTerminal(exchange.terminal)
		termErr := exchange.termErr
		closed := exchange.closed
		exchange.consumeMu.Unlock()
		if closed || termErr != nil {
			return terminal, termErr
		}
		if _, err := exchange.NextEvent(ctx); err != nil {
			if errors.Is(err, io.EOF) {
				exchange.consumeMu.Lock()
				defer exchange.consumeMu.Unlock()
				if exchange.closed {
					return cloneBackendTerminal(exchange.terminal), exchange.termErr
				}
				return executionbackend.TerminalResult{}, agentXUnknown("process_terminal_unknown", exchange.request.RequestID, errors.New("agentx process stream ended without process/closed"))
			}
			return executionbackend.TerminalResult{}, err
		}
	}
}

type agentXUnaryProcessExchange struct {
	target    executionbackend.Target
	operation executionbackend.OperationContext
	requestID string
	processID string
	method    string
	raw       *ProcessExchange
	now       func() time.Time
	once      sync.Once
	ack       executionbackend.Acknowledgement
	terminal  executionbackend.TerminalResult
	err       error
}

func (exchange *agentXUnaryProcessExchange) Target() executionbackend.Target { return exchange.target }
func (exchange *agentXUnaryProcessExchange) Operation() executionbackend.OperationContext {
	return exchange.operation
}
func (exchange *agentXUnaryProcessExchange) Done() <-chan struct{} { return exchange.raw.Done() }
func (exchange *agentXUnaryProcessExchange) resolve(ctx context.Context) {
	exchange.once.Do(func() {
		raw, err := exchange.raw.AwaitResponse(ctx)
		if err != nil {
			exchange.err = agentXUnknown("signal_response_unknown", exchange.requestID, err)
			return
		}
		succeeded, rpcError, err := classifyShellProcessResponse(raw, exchange.method, exchange.processID)
		if err != nil {
			exchange.err = agentXUnknown("invalid_signal_response", exchange.requestID, err)
			return
		}
		if rpcError || !succeeded {
			exchange.err = agentXRejected("signal_rejected", exchange.requestID, errors.New("agentx rejected process/terminate"))
			return
		}
		now := exchange.now().UTC()
		exchange.ack = executionbackend.Acknowledgement{ProviderOperationID: exchange.processID, ProviderRequestID: exchange.requestID, AcceptedAt: now}
		exchange.terminal = executionbackend.TerminalResult{Status: executionbackend.TerminalSucceeded, OutputComplete: true, CompletedAt: now}
	})
}
func (exchange *agentXUnaryProcessExchange) AwaitAcknowledgement(ctx context.Context) (executionbackend.Acknowledgement, error) {
	exchange.resolve(ctx)
	return exchange.ack, exchange.err
}
func (exchange *agentXUnaryProcessExchange) NextEvent(context.Context) (executionbackend.Event, error) {
	return executionbackend.Event{}, io.EOF
}
func (exchange *agentXUnaryProcessExchange) AwaitTerminal(ctx context.Context) (executionbackend.TerminalResult, error) {
	exchange.resolve(ctx)
	return cloneBackendTerminal(exchange.terminal), exchange.err
}

type agentXFileExchange struct {
	request executionbackend.ReadFileRequest
	raw     *FilesystemExchange
	now     func() time.Time
	once    sync.Once
	ack     executionbackend.Acknowledgement
	event   executionbackend.Event
	term    executionbackend.TerminalResult
	err     error
	emitted bool
	mu      sync.Mutex
}

func (exchange *agentXFileExchange) Target() executionbackend.Target { return exchange.request.Target }
func (exchange *agentXFileExchange) Operation() executionbackend.OperationContext {
	return exchange.request.Operation
}
func (exchange *agentXFileExchange) Done() <-chan struct{} { return exchange.raw.Done() }
func (exchange *agentXFileExchange) resolve(ctx context.Context) {
	exchange.once.Do(func() {
		raw, err := exchange.raw.AwaitResponse(ctx)
		if err != nil {
			exchange.err = agentXUnknown("read_response_unknown", exchange.request.RequestID, err)
			return
		}
		outcome, err := classifyReadFileResponse(raw, exchange.request.RequestID, exchange.request.Limit)
		if err != nil {
			exchange.err = agentXUnknown("invalid_read_response", exchange.request.RequestID, err)
			return
		}
		if outcome.responseKind != "result" {
			exchange.err = agentXRejected("read_rejected", exchange.request.RequestID, errors.New("agentx rejected filesystem read"))
			return
		}
		now := exchange.now().UTC()
		exchange.ack = executionbackend.Acknowledgement{ProviderRequestID: exchange.request.RequestID, AcceptedAt: now}
		if len(outcome.chunk) > 0 {
			exchange.event = executionbackend.Event{Sequence: 1, Kind: executionbackend.EventFileBytes, Data: append([]byte(nil), outcome.chunk...)}
		}
		exchange.term = executionbackend.TerminalResult{Status: executionbackend.TerminalSucceeded, OutputComplete: true, CompletedAt: now}
	})
}
func (exchange *agentXFileExchange) AwaitAcknowledgement(ctx context.Context) (executionbackend.Acknowledgement, error) {
	exchange.resolve(ctx)
	return exchange.ack, exchange.err
}
func (exchange *agentXFileExchange) NextEvent(ctx context.Context) (executionbackend.Event, error) {
	exchange.resolve(ctx)
	if exchange.err != nil {
		return executionbackend.Event{}, exchange.err
	}
	exchange.mu.Lock()
	defer exchange.mu.Unlock()
	if exchange.emitted || len(exchange.event.Data) == 0 {
		return executionbackend.Event{}, io.EOF
	}
	exchange.emitted = true
	event := exchange.event
	event.Data = append([]byte(nil), event.Data...)
	return event, nil
}
func (exchange *agentXFileExchange) AwaitTerminal(ctx context.Context) (executionbackend.TerminalResult, error) {
	exchange.resolve(ctx)
	return cloneBackendTerminal(exchange.term), exchange.err
}

func cloneInt32(value *int32) *int32 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneBackendTerminal(value executionbackend.TerminalResult) executionbackend.TerminalResult {
	value.ExitCode = cloneInt32(value.ExitCode)
	return value
}

var _ executionbackend.Backend = (*AgentXBackend)(nil)
var _ executionbackend.Exchange = (*agentXProcessExchange)(nil)
var _ executionbackend.Exchange = (*agentXUnaryProcessExchange)(nil)
var _ executionbackend.Exchange = (*agentXFileExchange)(nil)
