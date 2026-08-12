package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"sync"
	"time"

	"github.com/agentserver/agentserver/v2/internal/executionbackend"
	"github.com/agentserver/agentserver/v2/internal/sandboxgateway"
)

type exchange struct {
	target    executionbackend.Target
	operation executionbackend.OperationContext

	ackReady chan struct{}
	ackOnce  sync.Once
	ack      executionbackend.Acknowledgement
	ackErr   error

	events chan executionbackend.Event

	done         chan struct{}
	terminalOnce sync.Once
	terminal     executionbackend.TerminalResult
	terminalErr  error

	mu sync.Mutex
}

func newExchange(target executionbackend.Target, operation executionbackend.OperationContext, eventCapacity int) *exchange {
	if eventCapacity < 1 {
		eventCapacity = 1
	}
	return &exchange{
		target: target, operation: operation, ackReady: make(chan struct{}),
		events: make(chan executionbackend.Event, eventCapacity), done: make(chan struct{}),
	}
}

func newCompletedExchange(target executionbackend.Target, operation executionbackend.OperationContext,
	ack executionbackend.Acknowledgement, events []executionbackend.Event, terminal executionbackend.TerminalResult,
) *exchange {
	current := newExchange(target, operation, len(events)+1)
	current.setAcknowledgement(ack, nil)
	for _, event := range events {
		current.events <- cloneEvent(event)
	}
	current.finish(terminal, nil)
	return current
}

func (current *exchange) Target() executionbackend.Target { return current.target }

func (current *exchange) Operation() executionbackend.OperationContext { return current.operation }

func (current *exchange) AwaitAcknowledgement(ctx context.Context) (executionbackend.Acknowledgement, error) {
	if ctx == nil {
		return executionbackend.Acknowledgement{}, errors.New("TAE exchange context is required")
	}
	select {
	case <-ctx.Done():
		return executionbackend.Acknowledgement{}, ctx.Err()
	case <-current.ackReady:
		current.mu.Lock()
		defer current.mu.Unlock()
		return current.ack, current.ackErr
	}
}

func (current *exchange) NextEvent(ctx context.Context) (executionbackend.Event, error) {
	if ctx == nil {
		return executionbackend.Event{}, errors.New("TAE exchange context is required")
	}
	for {
		select {
		case event, ok := <-current.events:
			if !ok {
				return executionbackend.Event{}, io.EOF
			}
			return cloneEvent(event), nil
		default:
		}
		select {
		case <-ctx.Done():
			return executionbackend.Event{}, ctx.Err()
		case event, ok := <-current.events:
			if !ok {
				return executionbackend.Event{}, io.EOF
			}
			return cloneEvent(event), nil
		case <-current.done:
			select {
			case event, ok := <-current.events:
				if ok {
					return cloneEvent(event), nil
				}
			default:
			}
			return executionbackend.Event{}, io.EOF
		}
	}
}

func (current *exchange) AwaitTerminal(ctx context.Context) (executionbackend.TerminalResult, error) {
	if ctx == nil {
		return executionbackend.TerminalResult{}, errors.New("TAE exchange context is required")
	}
	select {
	case <-ctx.Done():
		return executionbackend.TerminalResult{}, ctx.Err()
	case <-current.done:
		current.mu.Lock()
		defer current.mu.Unlock()
		return cloneTerminal(current.terminal), current.terminalErr
	}
}

func (current *exchange) Done() <-chan struct{} { return current.done }

func (current *exchange) setAcknowledgement(ack executionbackend.Acknowledgement, err error) {
	current.ackOnce.Do(func() {
		current.mu.Lock()
		current.ack, current.ackErr = ack, err
		current.mu.Unlock()
		close(current.ackReady)
	})
}

func (current *exchange) finish(terminal executionbackend.TerminalResult, err error) {
	current.terminalOnce.Do(func() {
		current.mu.Lock()
		current.terminal, current.terminalErr = cloneTerminal(terminal), err
		current.mu.Unlock()
		close(current.events)
		close(current.done)
	})
}

type processExchange struct {
	*exchange
	provider  *Provider
	ctx       context.Context
	cancel    context.CancelFunc
	request   sandboxgateway.StartProcessProviderRequest
	sequence  uint64
	remaining int64
	pid       int
	acked     bool
	reconnect int
	complete  bool
}

func newProcessExchange(provider *Provider, ctx context.Context, cancel context.CancelFunc,
	request sandboxgateway.StartProcessProviderRequest, _ EventStream,
) *processExchange {
	capacity := int((request.Request.OutputLimitBytes+executionbackend.MaxEventBytes-1)/executionbackend.MaxEventBytes) + 2
	return &processExchange{
		exchange: newExchange(request.Request.Target, request.Request.Operation, capacity),
		provider: provider, ctx: ctx, cancel: cancel, request: request,
		remaining: request.Request.OutputLimitBytes,
	}
}

func (current *processExchange) consume(stream EventStream) {
	defer current.cancel()
	defer func() { _ = stream.Close() }()
	for {
		event, err := stream.Next(current.ctx)
		if err != nil {
			if current.tryReconnect(&stream) {
				continue
			}
			current.finishStreamLoss(stream, err)
			return
		}
		switch event.Name {
		case "process.start":
			pid, ok := positiveInteger(event.Data, "pid")
			if !ok || (current.pid != 0 && current.pid != pid) {
				current.finishProtocolError(stream, "provider_start_invalid")
				return
			}
			current.pid = pid
			if !current.acked {
				current.acked = true
				current.setAcknowledgement(executionbackend.Acknowledgement{
					ProviderOperationID: providerOperationPrefix + strconv.Itoa(pid),
					ProviderRequestID:   stream.RequestID(), AcceptedAt: current.provider.now(),
				}, nil)
			}
		case "process.data":
			if !current.acked {
				current.finishProtocolError(stream, "provider_data_before_start")
				return
			}
			stdout, stdoutPresent, stdoutValid := optionalString(event.Data, "stdout")
			stderr, stderrPresent, stderrValid := optionalString(event.Data, "stderr")
			if (!stdoutPresent && !stderrPresent) || !stdoutValid || !stderrValid {
				current.finishProtocolError(stream, "provider_data_invalid")
				return
			}
			if current.appendOutput(executionbackend.EventStdout, stdout) ||
				current.appendOutput(executionbackend.EventStderr, stderr) {
				current.killForOutputLimit()
				return
			}
		case "process.exit":
			if !current.acked {
				current.finishProtocolError(stream, "provider_exit_before_start")
				return
			}
			exitCode, ok := integer(event.Data, "exit_code")
			if !ok {
				exitCode, ok = integer(event.Data, "exitCode")
			}
			if !ok || exitCode < -2147483648 || exitCode > 2147483647 {
				current.finishProtocolError(stream, "provider_exit_invalid")
				return
			}
			converted := int32(exitCode)
			status := executionbackend.TerminalSucceeded
			reason := ""
			if converted != 0 {
				status = executionbackend.TerminalFailed
				reason = "process_exit_nonzero"
			}
			if current.reconnect > 0 {
				current.complete = false
				if reason == "" {
					reason = "stream_reconnected"
				}
			} else {
				current.complete = true
			}
			current.finish(executionbackend.TerminalResult{
				Status: status, ExitCode: &converted, ReasonCode: reason,
				OutputComplete: current.complete, CompletedAt: current.provider.now(),
			}, nil)
			return
		default:
			// Heartbeats and future additive event types are ignored. The
			// process.start/data/exit ordering remains strict.
		}
	}
}

func (current *processExchange) tryReconnect(stream *EventStream) bool {
	if !current.acked || current.pid < 1 || current.reconnect >= current.provider.reconnectAttempts || current.ctx.Err() != nil {
		return false
	}
	if !wait(current.ctx, current.provider.reconnectDelay) {
		return false
	}
	reconnected, err := current.provider.data.ConnectProcess(current.ctx, current.request.SessionRef, current.pid)
	current.reconnect++
	if err != nil {
		return current.tryReconnect(stream)
	}
	_ = (*stream).Close()
	*stream = reconnected
	return true
}

func (current *processExchange) appendOutput(kind executionbackend.EventKind, value string) bool {
	data := []byte(value)
	if len(data) == 0 {
		return false
	}
	exceeded := int64(len(data)) > current.remaining
	if exceeded {
		data = data[:current.remaining]
	}
	for len(data) > 0 {
		length := len(data)
		if length > executionbackend.MaxEventBytes {
			length = executionbackend.MaxEventBytes
		}
		current.sequence++
		current.events <- executionbackend.Event{Sequence: current.sequence, Kind: kind, Data: append([]byte(nil), data[:length]...)}
		current.remaining -= int64(length)
		data = data[length:]
	}
	return exceeded
}

func (current *processExchange) killForOutputLimit() {
	if current.pid > 0 {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(current.ctx), current.provider.signalTimeout)
		_, _ = current.provider.data.SignalProcess(ctx, current.request.SessionRef, current.pid, 9)
		cancel()
	}
	current.finish(executionbackend.TerminalResult{
		Status: executionbackend.TerminalFailed, ReasonCode: "output_limit_exceeded",
		OutputComplete: false, CompletedAt: current.provider.now(),
	}, nil)
}

func (current *processExchange) finishProtocolError(stream EventStream, code string) {
	if !current.acked {
		current.setAcknowledgement(executionbackend.Acknowledgement{}, current.streamDispatchError(
			code, stream, nil, errors.New("TAE process stream violated the documented event protocol")))
	}
	current.finish(executionbackend.TerminalResult{
		Status: executionbackend.TerminalUnknown, ReasonCode: code,
		OutputComplete: false, CompletedAt: current.provider.now(),
	}, nil)
}

func (current *processExchange) finishStreamLoss(stream EventStream, streamErr error) {
	code := "provider_stream_lost"
	if !current.acked {
		current.setAcknowledgement(executionbackend.Acknowledgement{}, current.streamDispatchError(
			code, stream, streamErr, errors.New("TAE process stream ended before provider acknowledgement")))
	}
	_ = streamErr
	current.finish(executionbackend.TerminalResult{
		Status: executionbackend.TerminalUnknown, ReasonCode: code,
		OutputComplete: false, CompletedAt: current.provider.now(),
	}, nil)
}

func (current *processExchange) streamDispatchError(code string, stream EventStream, sourceErr, cause error) error {
	dispatchError := executionbackend.NewDispatchError(executionbackend.OutcomeUnknown, code, cause)
	requestWritten := true
	dispatchError.RequestWritten = &requestWritten
	var requestError *RequestError
	if errors.As(sourceErr, &requestError) && requestError != nil {
		dispatchError.ProviderRequestID = requestError.RequestID
		dispatchError.ProviderCode = requestError.ProviderCode
		dispatchError.HTTPStatus = requestError.StatusCode
	}
	if dispatchError.ProviderRequestID == "" && stream != nil {
		dispatchError.ProviderRequestID = stream.RequestID()
	}
	return dispatchError
}

func positiveInteger(data map[string]any, key string) (int, bool) {
	value, ok := integer(data, key)
	return int(value), ok && value > 0 && int64(int(value)) == value
}

func integer(data map[string]any, key string) (int64, bool) {
	value, ok := data[key]
	if !ok {
		return 0, false
	}
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int32:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		converted := int64(typed)
		return converted, float64(converted) == typed
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseInt(typed, 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func optionalString(data map[string]any, key string) (value string, present, valid bool) {
	raw, present := data[key]
	if !present {
		return "", false, true
	}
	value, valid = raw.(string)
	return value, true, valid
}

func wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
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

var _ executionbackend.Exchange = (*exchange)(nil)
