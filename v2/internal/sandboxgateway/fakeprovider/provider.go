// Package fakeprovider supplies a deterministic, in-memory sandbox provider
// for contract, fault-injection, and golden-path tests. It does not execute
// host commands.
package fakeprovider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/agentserver/agentserver/v2/internal/executionbackend"
	"github.com/agentserver/agentserver/v2/internal/sandboxgateway"
)

type CommandResult struct {
	Stdout         []byte
	Stderr         []byte
	ExitCode       int32
	Status         executionbackend.TerminalStatus
	ReasonCode     string
	OutputComplete bool
}

type CommandFunc func(sandboxgateway.StartProcessProviderRequest) (CommandResult, error)

type Provider struct {
	mu       sync.Mutex
	now      func() time.Time
	sessions map[string]*session
	command  CommandFunc

	CreateError error
	GetError    error
	DeleteError error
}

type session struct {
	provider sandboxgateway.ProviderSandbox
	create   sandboxgateway.CreateSandboxRequest
	files    map[string][]byte
	process  map[string]string
}

func New(now func() time.Time, command CommandFunc) *Provider {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if command == nil {
		command = defaultCommand
	}
	return &Provider{now: now, command: command, sessions: make(map[string]*session)}
}

func (provider *Provider) CreateSandbox(_ context.Context, request sandboxgateway.CreateSandboxRequest) (sandboxgateway.ProviderSandbox, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.CreateError != nil {
		return sandboxgateway.ProviderSandbox{}, provider.CreateError
	}
	if request.SessionRef != "" {
		if existing := provider.sessions[request.SessionRef]; existing != nil {
			if !sameCreate(existing.create, request) {
				return sandboxgateway.ProviderSandbox{}, &sandboxgateway.ProviderError{Code: "idempotency_conflict", Cause: errors.New("fake provider session reference was reused with a different profile")}
			}
			return existing.provider, nil
		}
	}
	for _, existing := range provider.sessions {
		if existing.create.IdempotencyKey == request.IdempotencyKey {
			if !sameCreateIdentity(existing.create, request) {
				return sandboxgateway.ProviderSandbox{}, &sandboxgateway.ProviderError{Code: "idempotency_conflict", Cause: errors.New("fake provider idempotency key was reused with a different profile")}
			}
			return existing.provider, nil
		}
	}
	sessionRef := request.SessionRef
	if sessionRef == "" {
		sessionRef = "fake-" + request.IdempotencyKey
	}
	created := sandboxgateway.ProviderSandbox{
		SessionRef: sessionRef, State: sandboxgateway.ProviderSandboxReady,
		Root: "/workspace", ExpiresAt: provider.now().Add(request.TTL),
		RequestID: "fake-create-" + request.IdempotencyKey,
	}
	request.SessionRef = sessionRef
	provider.sessions[sessionRef] = &session{
		provider: created, create: request,
		files: map[string][]byte{
			"/workspace/README.md": []byte("managed sandbox\n"),
		},
		process: make(map[string]string),
	}
	return created, nil
}

func (provider *Provider) FindSandbox(_ context.Context, request sandboxgateway.FindSandboxRequest) (sandboxgateway.ProviderSandbox, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.GetError != nil {
		return sandboxgateway.ProviderSandbox{}, provider.GetError
	}
	var matched *session
	for _, candidate := range provider.sessions {
		if !matchesFind(candidate.create, request) {
			continue
		}
		if matched != nil {
			return sandboxgateway.ProviderSandbox{}, &sandboxgateway.ProviderError{
				Code: "idempotency_ambiguous", Ambiguous: true,
				Cause: errors.New("fake provider lookup returned more than one session"),
			}
		}
		matched = candidate
	}
	if matched == nil {
		return sandboxgateway.ProviderSandbox{}, sandboxgateway.ErrProviderSandboxNotFound
	}
	return matched.provider, nil
}

func (provider *Provider) GetSandbox(_ context.Context, sessionRef string) (sandboxgateway.ProviderSandbox, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.GetError != nil {
		return sandboxgateway.ProviderSandbox{}, provider.GetError
	}
	current := provider.sessions[sessionRef]
	if current == nil {
		return sandboxgateway.ProviderSandbox{}, sandboxgateway.ErrProviderSandboxNotFound
	}
	return current.provider, nil
}

func (provider *Provider) SetSandboxTimeout(_ context.Context, request sandboxgateway.SetSandboxTimeoutProviderRequest) (sandboxgateway.ProviderSandbox, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	current := provider.sessions[request.SessionRef]
	if current == nil {
		return sandboxgateway.ProviderSandbox{}, sandboxgateway.ErrProviderSandboxNotFound
	}
	current.provider.ExpiresAt = provider.now().Add(request.TTL)
	return current.provider, nil
}

func (provider *Provider) DeleteSandbox(_ context.Context, request sandboxgateway.DeleteSandboxProviderRequest) error {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.DeleteError != nil {
		return provider.DeleteError
	}
	if request.SessionRef != "" {
		current := provider.sessions[request.SessionRef]
		if current == nil {
			return sandboxgateway.ErrProviderSandboxNotFound
		}
		if !matchesFind(current.create, request.Identity) {
			return &sandboxgateway.ProviderError{
				Code:  "provider_identity_mismatch",
				Cause: errors.New("fake provider delete identity did not match the referenced session"),
			}
		}
		delete(provider.sessions, request.SessionRef)
		return nil
	}
	deleted := 0
	for sessionRef, current := range provider.sessions {
		if !matchesFind(current.create, request.Identity) {
			continue
		}
		delete(provider.sessions, sessionRef)
		deleted++
	}
	if deleted == 0 {
		return sandboxgateway.ErrProviderSandboxNotFound
	}
	return nil
}

func (provider *Provider) StartProcess(_ context.Context, request sandboxgateway.StartProcessProviderRequest) (executionbackend.Exchange, error) {
	if err := request.Request.Validate(); err != nil {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "invalid_request", err)
	}
	provider.mu.Lock()
	current := provider.sessions[request.SessionRef]
	command := provider.command
	if current == nil {
		provider.mu.Unlock()
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeRejected, "sandbox_not_found", sandboxgateway.ErrProviderSandboxNotFound)
	}
	current.process[request.Request.ProcessID] = "fake-process-" + request.Request.ProcessID
	provider.mu.Unlock()
	result, err := command(request)
	if err != nil {
		return nil, err
	}
	if result.Status == "" {
		if result.ExitCode == 0 {
			result.Status = executionbackend.TerminalSucceeded
		} else {
			result.Status = executionbackend.TerminalFailed
		}
	}
	if !result.OutputComplete && result.ReasonCode == "" {
		result.OutputComplete = true
	}
	events, terminal := boundedCommandResult(result, request.Request.OutputLimitBytes, provider.now())
	return newExchange(request.Request.Target, request.Request.Operation,
		executionbackend.Acknowledgement{
			ProviderOperationID: "fake-process-" + request.Request.ProcessID,
			ProviderRequestID:   request.Request.RequestID, AcceptedAt: provider.now(),
		}, events, terminal), nil
}

func (provider *Provider) SignalProcess(_ context.Context, request sandboxgateway.SignalProcessProviderRequest) (executionbackend.Exchange, error) {
	if err := request.Request.Validate(); err != nil {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "invalid_request", err)
	}
	provider.mu.Lock()
	current := provider.sessions[request.SessionRef]
	if current == nil {
		provider.mu.Unlock()
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeRejected, "sandbox_not_found", sandboxgateway.ErrProviderSandboxNotFound)
	}
	handle := current.process[request.Request.ProcessID]
	provider.mu.Unlock()
	if handle == "" {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeRejected, "process_not_found", errors.New("fake provider process does not exist"))
	}
	return newExchange(request.Request.Target, request.Request.Operation,
		executionbackend.Acknowledgement{ProviderOperationID: handle, ProviderRequestID: request.Request.RequestID, AcceptedAt: provider.now()},
		nil,
		executionbackend.TerminalResult{Status: executionbackend.TerminalSucceeded, ReasonCode: "signal_delivered", OutputComplete: true, CompletedAt: provider.now()},
	), nil
}

func (provider *Provider) ReadFile(_ context.Context, request sandboxgateway.ReadFileProviderRequest) (executionbackend.Exchange, error) {
	if err := request.Request.Validate(); err != nil {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeNotSent, "invalid_request", err)
	}
	provider.mu.Lock()
	current := provider.sessions[request.SessionRef]
	if current == nil {
		provider.mu.Unlock()
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeRejected, "sandbox_not_found", sandboxgateway.ErrProviderSandboxNotFound)
	}
	content, exists := current.files[request.Request.Path]
	content = append([]byte(nil), content...)
	provider.mu.Unlock()
	if !exists {
		return nil, executionbackend.NewDispatchError(executionbackend.OutcomeRejected, "file_not_found", errors.New("fake provider file does not exist"))
	}
	start := request.Request.Offset
	if start > uint64(len(content)) {
		start = uint64(len(content))
	}
	end := start + request.Request.Limit
	if end > uint64(len(content)) {
		end = uint64(len(content))
	}
	var events []executionbackend.Event
	if start < end {
		events = []executionbackend.Event{{Sequence: 1, Kind: executionbackend.EventFileBytes, Data: content[start:end]}}
	}
	return newExchange(request.Request.Target, request.Request.Operation,
		executionbackend.Acknowledgement{ProviderOperationID: "fake-read-" + request.Request.Operation.OperationID, ProviderRequestID: request.Request.RequestID, AcceptedAt: provider.now()},
		events,
		executionbackend.TerminalResult{Status: executionbackend.TerminalSucceeded, OutputComplete: true, CompletedAt: provider.now()},
	), nil
}

func (provider *Provider) PutFile(sessionRef, path string, content []byte) error {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	current := provider.sessions[sessionRef]
	if current == nil {
		return sandboxgateway.ErrProviderSandboxNotFound
	}
	current.files[path] = append([]byte(nil), content...)
	return nil
}

func (provider *Provider) SessionCount() int {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return len(provider.sessions)
}

func defaultCommand(request sandboxgateway.StartProcessProviderRequest) (CommandResult, error) {
	switch request.Request.Executable {
	case "lark-cli":
		return CommandResult{
			Stdout:   []byte("{\"source\":\"fake-lark\",\"title\":\"Managed executor golden path\"}\n"),
			ExitCode: 0, Status: executionbackend.TerminalSucceeded, OutputComplete: true,
		}, nil
	case "true":
		return CommandResult{ExitCode: 0, Status: executionbackend.TerminalSucceeded, OutputComplete: true}, nil
	default:
		return CommandResult{
			Stderr:   []byte(fmt.Sprintf("%s: command not found\n", request.Request.Executable)),
			ExitCode: 127, Status: executionbackend.TerminalFailed,
			ReasonCode: "command_not_found", OutputComplete: true,
		}, nil
	}
}

func boundedCommandResult(result CommandResult, limit int64, now time.Time) ([]executionbackend.Event, executionbackend.TerminalResult) {
	remaining := limit
	var events []executionbackend.Event
	appendEvent := func(kind executionbackend.EventKind, data []byte) {
		if len(data) == 0 || remaining == 0 {
			return
		}
		if int64(len(data)) > remaining {
			data = data[:remaining]
		}
		accepted := len(data)
		for len(data) > 0 {
			length := len(data)
			if length > executionbackend.MaxEventBytes {
				length = executionbackend.MaxEventBytes
			}
			events = append(events, executionbackend.Event{Sequence: uint64(len(events) + 1), Kind: kind, Data: append([]byte(nil), data[:length]...)})
			data = data[length:]
		}
		remaining -= int64(accepted)
	}
	// Track the untruncated total separately because appendEvent consumes its
	// local slice while chunking.
	total := int64(len(result.Stdout) + len(result.Stderr))
	appendEvent(executionbackend.EventStdout, result.Stdout)
	appendEvent(executionbackend.EventStderr, result.Stderr)
	outputComplete := result.OutputComplete
	status, reason := result.Status, result.ReasonCode
	if total > limit {
		outputComplete = false
		status = executionbackend.TerminalFailed
		reason = "output_limit_exceeded"
	}
	exitCode := result.ExitCode
	return events, executionbackend.TerminalResult{
		Status: status, ExitCode: &exitCode, ReasonCode: reason,
		OutputComplete: outputComplete, CompletedAt: now,
	}
}

func sameCreate(left, right sandboxgateway.CreateSandboxRequest) bool {
	return left.SessionRef == right.SessionRef && left.SandboxID == right.SandboxID && left.IdempotencyKey == right.IdempotencyKey &&
		left.WorkspaceID == right.WorkspaceID && left.SessionID == right.SessionID &&
		left.EnvironmentID == right.EnvironmentID && left.Region == right.Region && left.PSM == right.PSM &&
		left.TTL == right.TTL
}

func sameCreateIdentity(left, right sandboxgateway.CreateSandboxRequest) bool {
	return left.SandboxID == right.SandboxID && left.IdempotencyKey == right.IdempotencyKey &&
		left.WorkspaceID == right.WorkspaceID && left.SessionID == right.SessionID &&
		left.EnvironmentID == right.EnvironmentID && left.Region == right.Region && left.PSM == right.PSM &&
		left.TTL == right.TTL
}

func matchesFind(create sandboxgateway.CreateSandboxRequest, request sandboxgateway.FindSandboxRequest) bool {
	return create.SandboxID == request.SandboxID && create.IdempotencyKey == request.IdempotencyKey && create.WorkspaceID == request.WorkspaceID &&
		create.SessionID == request.SessionID && create.EnvironmentID == request.EnvironmentID &&
		create.Region == request.Region && create.PSM == request.PSM
}

type exchange struct {
	target    executionbackend.Target
	operation executionbackend.OperationContext
	ack       executionbackend.Acknowledgement
	events    []executionbackend.Event
	terminal  executionbackend.TerminalResult
	next      int
	mu        sync.Mutex
	done      chan struct{}
	doneOnce  sync.Once
}

func newExchange(target executionbackend.Target, operation executionbackend.OperationContext, ack executionbackend.Acknowledgement, events []executionbackend.Event, terminal executionbackend.TerminalResult) *exchange {
	return &exchange{target: target, operation: operation, ack: ack, events: events, terminal: terminal, done: make(chan struct{})}
}

func (current *exchange) Target() executionbackend.Target              { return current.target }
func (current *exchange) Operation() executionbackend.OperationContext { return current.operation }

func (current *exchange) AwaitAcknowledgement(ctx context.Context) (executionbackend.Acknowledgement, error) {
	if err := contextError(ctx); err != nil {
		return executionbackend.Acknowledgement{}, err
	}
	return current.ack, nil
}

func (current *exchange) NextEvent(ctx context.Context) (executionbackend.Event, error) {
	if err := contextError(ctx); err != nil {
		return executionbackend.Event{}, err
	}
	current.mu.Lock()
	defer current.mu.Unlock()
	if current.next >= len(current.events) {
		return executionbackend.Event{}, io.EOF
	}
	event := current.events[current.next]
	event.Data = append([]byte(nil), event.Data...)
	current.next++
	return event, nil
}

func (current *exchange) AwaitTerminal(ctx context.Context) (executionbackend.TerminalResult, error) {
	if err := contextError(ctx); err != nil {
		return executionbackend.TerminalResult{}, err
	}
	current.doneOnce.Do(func() { close(current.done) })
	return current.terminal, nil
}

func (current *exchange) Done() <-chan struct{} { return current.done }

func contextError(ctx context.Context) error {
	if ctx == nil {
		return errors.New("fake provider context is required")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

var _ sandboxgateway.Provider = (*Provider)(nil)
var _ executionbackend.Exchange = (*exchange)(nil)
