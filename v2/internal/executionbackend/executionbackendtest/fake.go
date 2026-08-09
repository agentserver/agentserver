// Package executionbackendtest provides deterministic fakes for backend
// adapter and execution orchestration tests.
package executionbackendtest

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/agentserver/agentserver/v2/internal/executionbackend"
)

type ExchangeScript struct {
	Target               executionbackend.Target
	Operation            executionbackend.OperationContext
	Acknowledgement      executionbackend.Acknowledgement
	AcknowledgementError error
	Events               []executionbackend.Event
	EventError           error
	Terminal             executionbackend.TerminalResult
	TerminalError        error
}

type ScriptedExchange struct {
	target    executionbackend.Target
	operation executionbackend.OperationContext

	acknowledgement executionbackend.Acknowledgement
	ackError        error
	events          []executionbackend.Event
	eventError      error
	terminal        executionbackend.TerminalResult
	terminalError   error

	mu        sync.Mutex
	nextEvent int
	done      chan struct{}
	doneOnce  sync.Once
}

func NewScriptedExchange(script ExchangeScript) (*ScriptedExchange, error) {
	if err := script.Target.Validate(); err != nil {
		return nil, err
	}
	if err := script.Operation.Validate(); err != nil {
		return nil, err
	}
	if script.AcknowledgementError == nil {
		if err := script.Acknowledgement.Validate(); err != nil {
			return nil, err
		}
	}
	var previous uint64
	events := make([]executionbackend.Event, len(script.Events))
	for index, event := range script.Events {
		if err := event.Validate(); err != nil {
			return nil, err
		}
		if index > 0 && event.Sequence <= previous {
			return nil, errors.New("scripted exchange event sequences must be strictly increasing")
		}
		previous = event.Sequence
		events[index] = cloneEvent(event)
	}
	if script.TerminalError == nil {
		if err := script.Terminal.Validate(); err != nil {
			return nil, err
		}
	}
	return &ScriptedExchange{
		target: script.Target, operation: script.Operation,
		acknowledgement: script.Acknowledgement, ackError: script.AcknowledgementError,
		events: events, eventError: script.EventError,
		terminal: cloneTerminal(script.Terminal), terminalError: script.TerminalError,
		done: make(chan struct{}),
	}, nil
}

func (exchange *ScriptedExchange) Target() executionbackend.Target { return exchange.target }

func (exchange *ScriptedExchange) Operation() executionbackend.OperationContext {
	return exchange.operation
}

func (exchange *ScriptedExchange) AwaitAcknowledgement(ctx context.Context) (executionbackend.Acknowledgement, error) {
	if err := contextError(ctx); err != nil {
		return executionbackend.Acknowledgement{}, err
	}
	if exchange.ackError != nil {
		return executionbackend.Acknowledgement{}, exchange.ackError
	}
	return exchange.acknowledgement, nil
}

func (exchange *ScriptedExchange) NextEvent(ctx context.Context) (executionbackend.Event, error) {
	if err := contextError(ctx); err != nil {
		return executionbackend.Event{}, err
	}
	exchange.mu.Lock()
	defer exchange.mu.Unlock()
	if exchange.nextEvent < len(exchange.events) {
		event := cloneEvent(exchange.events[exchange.nextEvent])
		exchange.nextEvent++
		return event, nil
	}
	if exchange.eventError != nil {
		return executionbackend.Event{}, exchange.eventError
	}
	return executionbackend.Event{}, io.EOF
}

func (exchange *ScriptedExchange) AwaitTerminal(ctx context.Context) (executionbackend.TerminalResult, error) {
	if err := contextError(ctx); err != nil {
		return executionbackend.TerminalResult{}, err
	}
	exchange.doneOnce.Do(func() { close(exchange.done) })
	if exchange.terminalError != nil {
		return executionbackend.TerminalResult{}, exchange.terminalError
	}
	return cloneTerminal(exchange.terminal), nil
}

func (exchange *ScriptedExchange) Done() <-chan struct{} { return exchange.done }

type StartProcessFunc func(context.Context, executionbackend.StartProcessRequest) (executionbackend.Exchange, error)
type SignalProcessFunc func(context.Context, executionbackend.SignalProcessRequest) (executionbackend.Exchange, error)
type ReadFileFunc func(context.Context, executionbackend.ReadFileRequest) (executionbackend.Exchange, error)

type FakeBackend struct {
	kind executionbackend.Kind

	Start  StartProcessFunc
	Signal SignalProcessFunc
	Read   ReadFileFunc

	mu          sync.Mutex
	startCalls  []executionbackend.StartProcessRequest
	signalCalls []executionbackend.SignalProcessRequest
	readCalls   []executionbackend.ReadFileRequest
}

func NewFakeBackend(kind executionbackend.Kind) (*FakeBackend, error) {
	if err := kind.Validate(); err != nil {
		return nil, err
	}
	return &FakeBackend{kind: kind}, nil
}

func (backend *FakeBackend) Kind() executionbackend.Kind { return backend.kind }

func (backend *FakeBackend) StartProcess(ctx context.Context, request executionbackend.StartProcessRequest) (executionbackend.Exchange, error) {
	backend.mu.Lock()
	backend.startCalls = append(backend.startCalls, cloneStartRequest(request))
	handler := backend.Start
	backend.mu.Unlock()
	if handler == nil {
		return nil, unconfiguredError("start_process")
	}
	return handler(ctx, request)
}

func (backend *FakeBackend) SignalProcess(ctx context.Context, request executionbackend.SignalProcessRequest) (executionbackend.Exchange, error) {
	backend.mu.Lock()
	backend.signalCalls = append(backend.signalCalls, request)
	handler := backend.Signal
	backend.mu.Unlock()
	if handler == nil {
		return nil, unconfiguredError("signal_process")
	}
	return handler(ctx, request)
}

func (backend *FakeBackend) ReadFile(ctx context.Context, request executionbackend.ReadFileRequest) (executionbackend.Exchange, error) {
	backend.mu.Lock()
	backend.readCalls = append(backend.readCalls, request)
	handler := backend.Read
	backend.mu.Unlock()
	if handler == nil {
		return nil, unconfiguredError("read_file")
	}
	return handler(ctx, request)
}

func (backend *FakeBackend) StartCalls() []executionbackend.StartProcessRequest {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	result := make([]executionbackend.StartProcessRequest, len(backend.startCalls))
	for index, request := range backend.startCalls {
		result[index] = cloneStartRequest(request)
	}
	return result
}

func (backend *FakeBackend) SignalCalls() []executionbackend.SignalProcessRequest {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return append([]executionbackend.SignalProcessRequest(nil), backend.signalCalls...)
}

func (backend *FakeBackend) ReadCalls() []executionbackend.ReadFileRequest {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return append([]executionbackend.ReadFileRequest(nil), backend.readCalls...)
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return errors.New("scripted exchange context is required")
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func cloneEvent(event executionbackend.Event) executionbackend.Event {
	event.Data = append([]byte(nil), event.Data...)
	return event
}

func cloneTerminal(terminal executionbackend.TerminalResult) executionbackend.TerminalResult {
	if terminal.ExitCode != nil {
		exitCode := *terminal.ExitCode
		terminal.ExitCode = &exitCode
	}
	return terminal
}

func cloneStartRequest(request executionbackend.StartProcessRequest) executionbackend.StartProcessRequest {
	request.Arguments = append([]string(nil), request.Arguments...)
	if request.Environment != nil {
		environment := make(map[string]string, len(request.Environment))
		for name, value := range request.Environment {
			environment[name] = value
		}
		request.Environment = environment
	}
	return request
}

func unconfiguredError(operation string) error {
	return executionbackend.NewDispatchError(
		executionbackend.OutcomeNotSent,
		"fake_unconfigured",
		errors.New("fake backend has no "+operation+" handler"),
	)
}

var _ executionbackend.Backend = (*FakeBackend)(nil)
var _ executionbackend.Exchange = (*ScriptedExchange)(nil)
