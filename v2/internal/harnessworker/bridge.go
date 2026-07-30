package harnessworker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"sync"

	"github.com/agentserver/agentserver/v2/internal/codexwire"
)

const maxConfiguredPendingCalls = 4 * 1024

// RunScope is the trusted worker state against which an item/tool/call
// callback is checked. None of these values are accepted from tool arguments.
type RunScope struct {
	RunID                string
	ThreadID             string
	TurnID               string
	RunAttemptGeneration int64
}

type DynamicToolCaller interface {
	CallDynamicTool(context.Context, DynamicCall) (DynamicToolResult, error)
}

type BridgeEventKind uint8

const (
	BridgeResultReady BridgeEventKind = iota + 1
	BridgeCallFailed
)

// BridgeEvent is consumed by the worker's single app-server event loop. A
// ready result must still be claimed before writing, so a turn terminal that
// arrived first can win the race and suppress the response.
type BridgeEvent struct {
	Kind   BridgeEventKind
	CallID string
	Err    error
}

type ResponseLease struct {
	RequestID json.RawMessage
	CallID    string
	Result    DynamicToolResult
}

type callbackState uint8

const (
	callbackPending callbackState = iota + 1
	callbackReady
	callbackWriting
	callbackFailed
)

type trackedCallback struct {
	requestID    json.RawMessage
	requestKey   string
	call         DynamicCall
	state        callbackState
	result       DynamicToolResult
	cancel       context.CancelCauseFunc
	terminalSeen bool
}

// DynamicBridge owns only bounded, attempt-local callback correlation. It is
// disposable and never becomes session or execution authority.
type DynamicBridge struct {
	caller           DynamicToolCaller
	maxPending       int
	maxArgumentBytes int
	events           chan BridgeEvent

	mu         sync.Mutex
	closed     bool
	callbacks  map[string]*trackedCallback
	requestIDs map[string]string
	wg         sync.WaitGroup
	closeOnce  sync.Once
}

func NewDynamicBridge(caller DynamicToolCaller, maxPending, maxArgumentBytes int) (*DynamicBridge, error) {
	if caller == nil {
		return nil, errors.New("dynamic tool caller is required")
	}
	if maxPending < 1 {
		return nil, errors.New("dynamic bridge max pending calls must be positive")
	}
	if maxPending > maxConfiguredPendingCalls {
		return nil, fmt.Errorf("dynamic bridge max pending calls exceeds hard maximum %d", maxConfiguredPendingCalls)
	}
	if maxArgumentBytes < 1 {
		return nil, errors.New("dynamic bridge max argument bytes must be positive")
	}
	if maxArgumentBytes > maxConfiguredPayloadBytes {
		return nil, fmt.Errorf("dynamic bridge max argument bytes exceeds hard maximum %d", maxConfiguredPayloadBytes)
	}
	return &DynamicBridge{
		caller:           caller,
		maxPending:       maxPending,
		maxArgumentBytes: maxArgumentBytes,
		events:           make(chan BridgeEvent, maxPending),
		callbacks:        make(map[string]*trackedCallback),
		requestIDs:       make(map[string]string),
	}, nil
}

func (b *DynamicBridge) Events() <-chan BridgeEvent { return b.events }

// HandleToolCall validates one stock app-server reverse request, registers its
// typed cleanup state, and starts exactly one MCP call. It never writes stdio
// itself; the worker event loop owns response ordering.
func (b *DynamicBridge) HandleToolCall(parent context.Context, message codexwire.Message, scope RunScope) error {
	if parent == nil {
		return errors.New("dynamic tool call context is required")
	}
	requestID, call, err := parseDynamicToolCall(message, scope)
	if err != nil {
		return err
	}
	if len(call.Arguments) > b.maxArgumentBytes {
		return fmt.Errorf("dynamic tool arguments are %d bytes, limit is %d", len(call.Arguments), b.maxArgumentBytes)
	}
	requestKey, err := canonicalRequestIDKey(requestID)
	if err != nil {
		return err
	}
	callContext, cancel := context.WithCancelCause(parent)
	tracked := &trackedCallback{
		requestID:  requestID,
		requestKey: requestKey,
		call:       call,
		state:      callbackPending,
		cancel:     cancel,
	}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		cancel(errors.New("dynamic bridge is closed"))
		return errors.New("dynamic bridge is closed")
	}
	if len(b.callbacks) >= b.maxPending {
		b.mu.Unlock()
		cancel(errors.New("dynamic callback bound exceeded"))
		return fmt.Errorf("dynamic bridge has %d pending calls, limit is %d", len(b.callbacks), b.maxPending)
	}
	if _, duplicate := b.callbacks[call.CallID]; duplicate {
		b.mu.Unlock()
		cancel(errors.New("duplicate dynamic call id"))
		return fmt.Errorf("dynamic call id %q is already outstanding", call.CallID)
	}
	if existing, duplicate := b.requestIDs[requestKey]; duplicate {
		b.mu.Unlock()
		cancel(errors.New("duplicate app-server request id"))
		return fmt.Errorf("app-server request id is already outstanding for call %q", existing)
	}
	b.callbacks[call.CallID] = tracked
	b.requestIDs[requestKey] = call.CallID
	b.wg.Add(1)
	b.mu.Unlock()

	go b.dispatch(callContext, tracked)
	return nil
}

func (b *DynamicBridge) dispatch(ctx context.Context, tracked *trackedCallback) {
	defer b.wg.Done()
	result, err := b.caller.CallDynamicTool(ctx, tracked.call)
	b.mu.Lock()
	current := b.callbacks[tracked.call.CallID]
	if current != tracked || b.closed {
		b.mu.Unlock()
		return
	}
	tracked.call.Arguments = nil
	var event BridgeEvent
	if err != nil {
		tracked.state = callbackFailed
		tracked.cancel(err)
		event = BridgeEvent{Kind: BridgeCallFailed, CallID: tracked.call.CallID, Err: err}
	} else {
		tracked.state = callbackReady
		tracked.result = result
		event = BridgeEvent{Kind: BridgeResultReady, CallID: tracked.call.CallID}
	}
	b.mu.Unlock()
	b.events <- event
}

// ClaimResponse atomically chooses the result side of the result/turn-terminal
// race. Once claimed, a later terminal records terminalSeen but cannot cause a
// second outcome or remove the entry before ResponseWritten/ResponseWriteFailed.
func (b *DynamicBridge) ClaimResponse(callID string) (ResponseLease, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	tracked := b.callbacks[callID]
	if tracked == nil || tracked.state != callbackReady {
		return ResponseLease{}, false
	}
	tracked.state = callbackWriting
	return ResponseLease{
		RequestID: append(json.RawMessage(nil), tracked.requestID...),
		CallID:    tracked.call.CallID,
		Result:    cloneDynamicToolResult(tracked.result),
	}, true
}

// ResponseWritten is the normal dynamic callback cleanup barrier. Stock does
// not emit serverRequest/resolved for item/tool/call.
func (b *DynamicBridge) ResponseWritten(callID string) error {
	b.mu.Lock()
	tracked := b.callbacks[callID]
	if tracked == nil || tracked.state != callbackWriting {
		b.mu.Unlock()
		return fmt.Errorf("dynamic call %q has no claimed response", callID)
	}
	b.removeLocked(tracked)
	b.mu.Unlock()
	tracked.cancel(nil)
	return nil
}

// ResponseWriteFailed removes a response whose bytes could not be confirmed.
// The caller must interrupt the run; it must not issue another MCP call.
func (b *DynamicBridge) ResponseWriteFailed(callID string, cause error) error {
	if cause == nil {
		cause = errors.New("app-server response write failed")
	}
	b.mu.Lock()
	tracked := b.callbacks[callID]
	if tracked == nil || tracked.state != callbackWriting {
		b.mu.Unlock()
		return fmt.Errorf("dynamic call %q has no claimed response", callID)
	}
	b.removeLocked(tracked)
	b.mu.Unlock()
	tracked.cancel(cause)
	return nil
}

// TurnTerminal applies typed cleanup to callbacks owned by one turn. Pending,
// failed, and unclaimed-ready callbacks are cancelled and removed. A response
// already claimed by the serialized writer wins the race and remains until its
// write outcome is recorded.
func (b *DynamicBridge) TurnTerminal(turnID string) []string {
	b.mu.Lock()
	var cancelled []*trackedCallback
	for _, tracked := range b.callbacks {
		if tracked.call.TurnID != turnID {
			continue
		}
		tracked.terminalSeen = true
		if tracked.state == callbackWriting {
			continue
		}
		b.removeLocked(tracked)
		cancelled = append(cancelled, tracked)
	}
	b.mu.Unlock()
	callIDs := make([]string, len(cancelled))
	for index, tracked := range cancelled {
		callIDs[index] = tracked.call.CallID
		tracked.cancel(errors.New("owning turn reached terminal state"))
	}
	sort.Strings(callIDs)
	return callIDs
}

func (b *DynamicBridge) Outstanding() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.callbacks)
}

// Close cancels every remaining MCP call and waits for the bounded dispatcher
// set before closing Events. It is idempotent.
func (b *DynamicBridge) Close(cause error) {
	b.closeOnce.Do(func() {
		if cause == nil {
			cause = errors.New("dynamic bridge closed")
		}
		b.mu.Lock()
		b.closed = true
		remaining := make([]*trackedCallback, 0, len(b.callbacks))
		for _, tracked := range b.callbacks {
			remaining = append(remaining, tracked)
		}
		b.callbacks = make(map[string]*trackedCallback)
		b.requestIDs = make(map[string]string)
		b.mu.Unlock()
		for _, tracked := range remaining {
			tracked.cancel(cause)
		}
		b.wg.Wait()
		close(b.events)
	})
}

func (b *DynamicBridge) removeLocked(tracked *trackedCallback) {
	delete(b.callbacks, tracked.call.CallID)
	delete(b.requestIDs, tracked.requestKey)
}

// canonicalRequestIDKey correlates JSON-RPC IDs by value, not by their raw
// JSON spelling. In particular, "callback" and "\u0063allback" are the same
// string ID and cannot own two outstanding reverse requests.
func canonicalRequestIDKey(raw json.RawMessage) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", fmt.Errorf("decode app-server request id: %w", err)
	}
	switch value := value.(type) {
	case string:
		if err := validateNameText("app-server request id", value, maxIdentityBytes); err != nil {
			return "", err
		}
		return "s:" + value, nil
	case json.Number:
		integer, err := strconv.ParseInt(value.String(), 10, 64)
		if err != nil {
			return "", errors.New("app-server request id number must be a signed 64-bit integer")
		}
		return "n:" + strconv.FormatInt(integer, 10), nil
	default:
		return "", errors.New("app-server request id must be a string or signed 64-bit integer")
	}
}

func parseDynamicToolCall(message codexwire.Message, scope RunScope) (json.RawMessage, DynamicCall, error) {
	if message.Kind != codexwire.KindRequest || message.Method != "item/tool/call" || len(message.ID) == 0 {
		return nil, DynamicCall{}, errors.New("message is not an item/tool/call request")
	}
	if err := validateDynamicCallScope(scope); err != nil {
		return nil, DynamicCall{}, err
	}
	var params struct {
		ThreadID  string          `json:"threadId"`
		TurnID    string          `json:"turnId"`
		CallID    string          `json:"callId"`
		Namespace string          `json:"namespace"`
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
	}
	decoder := json.NewDecoder(bytes.NewReader(message.Params))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&params); err != nil {
		return nil, DynamicCall{}, fmt.Errorf("decode item/tool/call params: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, DynamicCall{}, errors.New("item/tool/call params contain multiple JSON values")
		}
		return nil, DynamicCall{}, fmt.Errorf("decode trailing item/tool/call params: %w", err)
	}
	if params.ThreadID != scope.ThreadID || params.TurnID != scope.TurnID {
		return nil, DynamicCall{}, errors.New("item/tool/call thread or turn does not match active scope")
	}
	call := DynamicCall{
		RunID:                scope.RunID,
		ThreadID:             params.ThreadID,
		TurnID:               params.TurnID,
		CallID:               params.CallID,
		RunAttemptGeneration: scope.RunAttemptGeneration,
		Namespace:            params.Namespace,
		Tool:                 params.Tool,
		Arguments:            append(json.RawMessage(nil), params.Arguments...),
	}
	if err := validateDynamicCallEnvelope(call); err != nil {
		return nil, DynamicCall{}, err
	}
	if len(call.Arguments) == 0 {
		return nil, DynamicCall{}, errors.New("item/tool/call arguments are required")
	}
	return append(json.RawMessage(nil), message.ID...), call, nil
}

func validateDynamicCallScope(scope RunScope) error {
	for label, value := range map[string]string{"run id": scope.RunID, "thread id": scope.ThreadID, "turn id": scope.TurnID} {
		if err := validateNameText(label, value, maxIdentityBytes); err != nil {
			return err
		}
	}
	if scope.RunAttemptGeneration < 1 {
		return errors.New("run attempt generation must be positive")
	}
	return nil
}

func cloneDynamicToolResult(result DynamicToolResult) DynamicToolResult {
	clone := result
	clone.ContentItems = append([]InputTextContent(nil), result.ContentItems...)
	return clone
}
