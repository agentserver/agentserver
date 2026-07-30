// Package execadapter implements the Phase 1 reference boundary between
// agentx's outer process profile and one stock Codex exec-server stdio child.
//
// It is intentionally one-process-per-instance. A stock connection that has
// accepted process/start is never reused for another process, and a root exit
// without process/closed is recovered by shutting down only that process's
// dedicated connection. This package is conformance code, not the agentx
// connector or its platform containment implementation.
package execadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/agentserver/agentserver/v2/internal/codexwire"
	"github.com/agentserver/agentserver/v2/internal/execprofile"
	"github.com/agentserver/agentserver/v2/internal/runtimelock"
)

const (
	maxConfiguredEventBuffer = 4096
	maxConfiguredEventBytes  = 8 * 1024 * 1024
	maxConfiguredGrace       = 5 * time.Minute
)

var (
	ErrInstanceAlreadyUsed  = errors.New("exec-server instance already accepted process/start")
	ErrInstanceClosed       = errors.New("exec-server instance is closed")
	ErrMethodNotNegotiated  = errors.New("method is not negotiated by the agentx outer process profile")
	ErrProcessDidNotClose   = errors.New("root process exited without process/closed")
	ErrTerminalReplyMissing = errors.New("process closed before an outstanding exec-server reply arrived")
)

// OuterProcessMethods returns the exact Phase 1 process capability. In
// particular, process/signal is absent because stock success does not prove
// whether a signal was delivered.
func OuterProcessMethods() []string {
	return execprofile.ProcessMethods()
}

func AllowsOuterProcessMethod(method string) bool {
	return execprofile.AllowsProcessMethod(method)
}

// Transport is one freshly started, not-yet-initialized stock exec-server
// stdio connection and its process lifecycle. Closing stdin is connection
// shutdown; it is not detach and cannot be resumed by another Instance.
type Transport interface {
	Send(any) error
	Receive(context.Context) (codexwire.Message, error)
	CloseStdin() error
	Wait(context.Context) error
	Kill() error
}

type CleanupVerifier func(context.Context, string) error

type Options struct {
	ClientName      string
	CleanupGrace    time.Duration
	ShutdownGrace   time.Duration
	EventBuffer     int
	MaxEventBytes   int
	Limits          runtimelock.AgentxLimits
	VerifyTreeEmpty CleanupVerifier
}

type TerminalState string

const (
	// TerminalClosed is the only protocol-complete state. The process exit code
	// still determines the command result.
	TerminalClosed TerminalState = "closed"
	// TerminalCleanupForced confirms the dedicated instance and verified tree
	// were reclaimed, but is an execution failure rather than success.
	TerminalCleanupForced TerminalState = "cleanup_forced"
	// TerminalUnknown means cleanup or terminal evidence could not be proved.
	TerminalUnknown TerminalState = "unknown"
)

type Result struct {
	InstanceID     string
	ProcessID      string
	State          TerminalState
	ExitCode       *int
	SandboxDenied  *bool
	ProtocolClosed bool
	Cause          error
}

type responseResult struct {
	message codexwire.Message
	err     error
}

// Instance owns exactly one stock stdio child and at most one process/start.
// Its receive pump is the only reader; all request IDs are local to this
// instance and are never forwarded from an outer connection.
type Instance struct {
	transport Transport
	options   Options
	id        string

	ctx    context.Context
	cancel context.CancelFunc

	sendMu sync.Mutex
	nextID atomic.Int64

	pendingMu sync.Mutex
	pending   map[string]chan responseResult

	stateMu        sync.Mutex
	started        bool
	processID      string
	exited         bool
	exitCode       *int
	sandboxDenied  *bool
	protocolClosed bool
	cleanupTimer   *time.Timer
	terminalTimer  *time.Timer
	lastEventSeq   uint64

	events   chan codexwire.Message
	done     chan struct{}
	pumpDone chan struct{}

	finishing  atomic.Bool
	finishOnce sync.Once
	resultMu   sync.Mutex
	result     Result
}

func New(transport Transport, instanceID string, options Options) (*Instance, error) {
	if transport == nil {
		return nil, errors.New("exec-server transport is required")
	}
	if instanceID == "" {
		return nil, errors.New("local exec instance id is required")
	}
	if options.ClientName == "" {
		return nil, errors.New("exec-server client name is required")
	}
	if options.CleanupGrace <= 0 || options.CleanupGrace > maxConfiguredGrace {
		return nil, fmt.Errorf("cleanup grace must be between 1ns and %s", maxConfiguredGrace)
	}
	if options.ShutdownGrace <= 0 || options.ShutdownGrace > maxConfiguredGrace {
		return nil, fmt.Errorf("shutdown grace must be between 1ns and %s", maxConfiguredGrace)
	}
	if options.EventBuffer < 1 || options.EventBuffer > maxConfiguredEventBuffer {
		return nil, fmt.Errorf("event buffer must be between 1 and %d", maxConfiguredEventBuffer)
	}
	if options.MaxEventBytes < 1 || options.MaxEventBytes > maxConfiguredEventBytes {
		return nil, fmt.Errorf("max event bytes must be between 1 and %d", maxConfiguredEventBytes)
	}
	if options.VerifyTreeEmpty == nil {
		return nil, errors.New("process-tree cleanup verifier is required")
	}
	if options.Limits.MaxFrameBytes < 1 || options.Limits.MaxJSONValues < 1 || options.Limits.MaxOutputBufferBytesPerProcess < 1 {
		return nil, errors.New("agentx frame, JSON-value, and output-buffer limits must be positive")
	}
	if int64(options.EventBuffer) > options.Limits.MaxOutputBufferBytesPerProcess/int64(options.MaxEventBytes) {
		return nil, errors.New("event buffer could retain more than the agentx per-process output limit")
	}
	if err := options.Limits.ValidateProcessStart([]string{"x"}, nil, map[string]string{"X": ""}); err != nil {
		return nil, fmt.Errorf("invalid agentx process/start limits: %w", err)
	}
	if err := options.Limits.ValidateWriteID("x"); err != nil {
		return nil, fmt.Errorf("invalid agentx writeId limits: %w", err)
	}

	instanceContext, cancel := context.WithCancel(context.Background())
	instance := &Instance{
		transport: transport,
		options:   options,
		id:        instanceID,
		ctx:       instanceContext,
		cancel:    cancel,
		pending:   make(map[string]chan responseResult),
		events:    make(chan codexwire.Message, options.EventBuffer),
		done:      make(chan struct{}),
		pumpDone:  make(chan struct{}),
	}
	go instance.receivePump()
	return instance, nil
}

func (i *Instance) ID() string { return i.id }

func (i *Instance) Events() <-chan codexwire.Message { return i.events }

// Start initializes the fresh stock instance, verifies its ready state, and
// forwards exactly one process/start after applying the smaller agentx argv,
// environment, and frame bounds.
func (i *Instance) Start(ctx context.Context, params any) error {
	if ctx == nil {
		return errors.New("context is required")
	}
	i.stateMu.Lock()
	if i.started {
		i.stateMu.Unlock()
		return ErrInstanceAlreadyUsed
	}
	i.started = true
	i.stateMu.Unlock()

	raw, start, err := i.validateStartParams(params)
	if err != nil {
		i.beginFinalize(TerminalUnknown, err)
		return err
	}

	i.stateMu.Lock()
	i.processID = start.ProcessID
	i.stateMu.Unlock()

	var initialized struct {
		SessionID string `json:"sessionId"`
	}
	if err := i.call(ctx, "initialize", map[string]any{"clientName": i.options.ClientName}, &initialized); err != nil {
		i.beginFinalize(TerminalUnknown, fmt.Errorf("initialize exec-server: %w", err))
		return err
	}
	if initialized.SessionID == "" {
		err := errors.New("exec-server initialize returned an empty sessionId")
		i.beginFinalize(TerminalUnknown, err)
		return err
	}
	if err := i.send(map[string]any{"method": "initialized", "params": nil}); err != nil {
		i.beginFinalize(TerminalUnknown, fmt.Errorf("send initialized: %w", err))
		return err
	}
	var environmentInfo map[string]json.RawMessage
	if err := i.call(ctx, "environment/info", nil, &environmentInfo); err != nil {
		i.beginFinalize(TerminalUnknown, fmt.Errorf("read environment/info: %w", err))
		return err
	}
	if environmentInfo == nil {
		err := errors.New("environment/info returned null")
		i.beginFinalize(TerminalUnknown, err)
		return err
	}
	var status struct {
		Status string `json:"status"`
	}
	if err := i.call(ctx, "environment/status", nil, &status); err != nil {
		i.beginFinalize(TerminalUnknown, fmt.Errorf("read environment/status: %w", err))
		return err
	}
	if status.Status != "ready" {
		err := fmt.Errorf("environment/status = %q, want ready", status.Status)
		i.beginFinalize(TerminalUnknown, err)
		return err
	}
	var started struct {
		ProcessID string `json:"processId"`
	}
	if err := i.callRaw(ctx, "process/start", raw, &started); err != nil {
		i.beginFinalize(TerminalUnknown, fmt.Errorf("process/start: %w", err))
		return err
	}
	if started.ProcessID != start.ProcessID {
		err := fmt.Errorf("process/start returned processId %q, want %q", started.ProcessID, start.ProcessID)
		i.beginFinalize(TerminalUnknown, err)
		return err
	}
	return nil
}

// Forward sends one allowed post-start process method after ownership and
// product-bound validation. process/signal and a second process/start are
// rejected before any frame reaches stock Codex.
func (i *Instance) Forward(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	if ctx == nil {
		return nil, errors.New("context is required")
	}
	if method == "process/start" {
		return nil, ErrInstanceAlreadyUsed
	}
	if !execprofile.AllowsProcessMethod(method) {
		return nil, fmt.Errorf("%w: %s", ErrMethodNotNegotiated, method)
	}
	if err := i.validateOwnedParams(method, params); err != nil {
		return nil, err
	}
	var result json.RawMessage
	if err := i.callRaw(ctx, method, params, &result); err != nil {
		return nil, err
	}
	return append(json.RawMessage(nil), result...), nil
}

func (i *Instance) Wait(ctx context.Context) (Result, error) {
	if ctx == nil {
		return Result{}, errors.New("context is required")
	}
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	case <-i.done:
	}
	i.resultMu.Lock()
	result := i.result
	i.resultMu.Unlock()
	if result.State == TerminalUnknown {
		if result.Cause == nil {
			return result, errors.New("exec-server instance ended in unknown state")
		}
		return result, result.Cause
	}
	return result, nil
}

// Abort closes this instance without claiming a process terminal. It is the
// fail-closed path for owner loss, protocol errors, and test cleanup.
func (i *Instance) Abort(cause error) {
	if cause == nil {
		cause = errors.New("exec-server instance aborted")
	}
	i.beginFinalize(TerminalUnknown, cause)
}

type startParams struct {
	ProcessID string            `json:"processId"`
	Argv      []string          `json:"argv"`
	Arg0      *string           `json:"arg0"`
	Env       map[string]string `json:"env"`
}

func (i *Instance) validateStartParams(params any) (json.RawMessage, startParams, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, startParams{}, fmt.Errorf("encode process/start params: %w", err)
	}
	if int64(len(raw)) > i.options.Limits.MaxFrameBytes {
		return nil, startParams{}, fmt.Errorf("process/start params exceed agentx frame limit %d", i.options.Limits.MaxFrameBytes)
	}
	var decoded startParams
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, startParams{}, fmt.Errorf("decode process/start params: %w", err)
	}
	if decoded.ProcessID == "" {
		return nil, startParams{}, errors.New("process/start processId is required")
	}
	if len(decoded.ProcessID) > 256 || strings.ContainsRune(decoded.ProcessID, '\x00') {
		return nil, startParams{}, errors.New("process/start processId exceeds 256 bytes or contains NUL")
	}
	if decoded.Env == nil {
		return nil, startParams{}, errors.New("process/start env must be an explicit object")
	}
	if err := i.options.Limits.ValidateProcessStart(decoded.Argv, decoded.Arg0, decoded.Env); err != nil {
		return nil, startParams{}, fmt.Errorf("validate process/start product bounds: %w", err)
	}
	return append(json.RawMessage(nil), raw...), decoded, nil
}

func (i *Instance) validateOwnedParams(method string, raw json.RawMessage) error {
	if len(raw) == 0 || int64(len(raw)) > i.options.Limits.MaxFrameBytes {
		return errors.New("process params are empty or exceed the agentx frame limit")
	}
	var ownership struct {
		ProcessID string `json:"processId"`
		WriteID   string `json:"writeId"`
	}
	if err := json.Unmarshal(raw, &ownership); err != nil {
		return fmt.Errorf("decode %s params: %w", method, err)
	}
	i.stateMu.Lock()
	started := i.started
	processID := i.processID
	i.stateMu.Unlock()
	if !started {
		return errors.New("exec-server instance has not accepted process/start")
	}
	if ownership.ProcessID != processID {
		return fmt.Errorf("%s processId %q is not owned by instance %q", method, ownership.ProcessID, i.id)
	}
	if method == "process/write" {
		if err := i.options.Limits.ValidateWriteID(ownership.WriteID); err != nil {
			return fmt.Errorf("validate process/write product bounds: %w", err)
		}
	}
	return nil
}

func (i *Instance) call(ctx context.Context, method string, params any, destination any) error {
	var raw json.RawMessage
	if params != nil {
		encoded, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("encode %s params: %w", method, err)
		}
		raw = encoded
	}
	return i.callRaw(ctx, method, raw, destination)
}

func (i *Instance) callRaw(ctx context.Context, method string, params json.RawMessage, destination any) error {
	if i.finishing.Load() {
		return ErrInstanceClosed
	}
	i.stateMu.Lock()
	protocolClosed := i.protocolClosed
	i.stateMu.Unlock()
	if protocolClosed {
		return ErrInstanceClosed
	}
	id := i.nextID.Add(1)
	key := fmt.Sprintf("%d", id)
	request := map[string]any{"id": id, "method": method}
	if params != nil {
		request["params"] = params
	}
	encodedRequest, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode %s request: %w", method, err)
	}
	if int64(len(encodedRequest)) > i.options.Limits.MaxFrameBytes {
		return fmt.Errorf("%s request exceeds agentx frame limit %d", method, i.options.Limits.MaxFrameBytes)
	}
	if _, err := codexwire.Parse(encodedRequest); err != nil {
		return fmt.Errorf("validate %s request envelope: %w", method, err)
	}
	response := make(chan responseResult, 1)
	i.pendingMu.Lock()
	if i.finishing.Load() {
		i.pendingMu.Unlock()
		return ErrInstanceClosed
	}
	i.pending[key] = response
	i.pendingMu.Unlock()

	if err := i.send(request); err != nil {
		i.removePending(key)
		return err
	}
	select {
	case <-ctx.Done():
		i.removePending(key)
		return ctx.Err()
	case result := <-response:
		if result.err != nil {
			return result.err
		}
		if result.message.Kind == codexwire.KindError {
			return fmt.Errorf("%s RPC error %d: %s", method, result.message.Error.Code, result.message.Error.Message)
		}
		if result.message.Kind != codexwire.KindResponse {
			return fmt.Errorf("%s reply kind = %s", method, result.message.Kind)
		}
		if destination == nil {
			return nil
		}
		if rawDestination, ok := destination.(*json.RawMessage); ok {
			*rawDestination = append((*rawDestination)[:0], result.message.Result...)
			return nil
		}
		if err := result.message.DecodeResult(destination); err != nil {
			return fmt.Errorf("decode %s result: %w", method, err)
		}
		return nil
	}
}

func (i *Instance) send(value any) error {
	i.sendMu.Lock()
	defer i.sendMu.Unlock()
	if i.finishing.Load() {
		return ErrInstanceClosed
	}
	return i.transport.Send(value)
}

func (i *Instance) removePending(key string) {
	i.pendingMu.Lock()
	delete(i.pending, key)
	i.pendingMu.Unlock()
	i.maybeFinalizeClosed()
}

func (i *Instance) receivePump() {
	defer close(i.pumpDone)
	for {
		message, err := i.transport.Receive(i.ctx)
		if err != nil {
			if !i.finishing.Load() && !errors.Is(err, context.Canceled) {
				i.beginFinalize(TerminalUnknown, fmt.Errorf("receive exec-server frame: %w", err))
			}
			return
		}
		switch message.Kind {
		case codexwire.KindResponse, codexwire.KindError:
			if !i.deliverResponse(message) {
				i.beginFinalize(TerminalUnknown, fmt.Errorf("unexpected exec-server response id %s", message.ID))
				return
			}
		case codexwire.KindRequest:
			if err := i.rejectReverseRequest(message); err != nil {
				i.beginFinalize(TerminalUnknown, err)
				return
			}
		case codexwire.KindNotification:
			if err := i.handleNotification(message); err != nil {
				i.beginFinalize(TerminalUnknown, err)
				return
			}
		default:
			i.beginFinalize(TerminalUnknown, fmt.Errorf("unexpected exec-server message kind %s", message.Kind))
			return
		}
	}
}

func (i *Instance) deliverResponse(message codexwire.Message) bool {
	key := string(message.ID)
	i.pendingMu.Lock()
	response, found := i.pending[key]
	if found {
		delete(i.pending, key)
	}
	i.pendingMu.Unlock()
	if !found {
		return false
	}
	response <- responseResult{message: message}
	i.maybeFinalizeClosed()
	return true
}

func (i *Instance) rejectReverseRequest(message codexwire.Message) error {
	var reply any
	if message.Method == "network/policyRequest" {
		reply = map[string]any{
			"id": message.ID,
			"result": map[string]any{
				"decision": map[string]any{"type": "deny", "reason": "not_allowed"},
			},
		}
	} else {
		reply = map[string]any{
			"id": message.ID,
			"error": map[string]any{
				"code":    -32601,
				"message": "unsupported exec-server reverse request",
			},
		}
	}
	if err := i.send(reply); err != nil {
		return fmt.Errorf("reject reverse request %s: %w", message.Method, err)
	}
	return nil
}

func (i *Instance) handleNotification(message codexwire.Message) error {
	if len(message.Raw) > i.options.MaxEventBytes {
		return fmt.Errorf("exec-server event %s exceeds %d bytes", message.Method, i.options.MaxEventBytes)
	}
	var common struct {
		ProcessID string `json:"processId"`
		Seq       uint64 `json:"seq"`
	}
	if err := message.DecodeParams(&common); err != nil {
		return fmt.Errorf("decode %s notification: %w", message.Method, err)
	}
	i.stateMu.Lock()
	processID := i.processID
	started := i.started
	if !started || common.ProcessID != processID || common.Seq == 0 {
		i.stateMu.Unlock()
		return fmt.Errorf("%s notification ownership/sequence = process %q seq %d, want %q and positive seq", message.Method, common.ProcessID, common.Seq, processID)
	}
	if common.Seq <= i.lastEventSeq {
		last := i.lastEventSeq
		i.stateMu.Unlock()
		return fmt.Errorf("%s notification seq = %d, want greater than %d", message.Method, common.Seq, last)
	}
	i.lastEventSeq = common.Seq
	i.stateMu.Unlock()

	switch message.Method {
	case "process/output":
		return i.publishEvent(message)
	case "process/exited":
		var exited struct {
			ProcessID     string `json:"processId"`
			Seq           uint64 `json:"seq"`
			ExitCode      int    `json:"exitCode"`
			SandboxDenied *bool  `json:"sandboxDenied"`
		}
		if err := message.DecodeParams(&exited); err != nil {
			return fmt.Errorf("decode process/exited: %w", err)
		}
		if exited.SandboxDenied == nil {
			return errors.New("process/exited omitted sandboxDenied")
		}
		i.stateMu.Lock()
		if i.exited {
			i.stateMu.Unlock()
			return errors.New("duplicate process/exited notification")
		}
		i.exited = true
		exitCode := exited.ExitCode
		sandboxDenied := *exited.SandboxDenied
		i.exitCode = &exitCode
		i.sandboxDenied = &sandboxDenied
		i.cleanupTimer = time.AfterFunc(i.options.CleanupGrace, func() {
			i.beginFinalize(TerminalCleanupForced, ErrProcessDidNotClose)
		})
		i.stateMu.Unlock()
		return i.publishEvent(message)
	case "process/closed":
		i.stateMu.Lock()
		if !i.exited {
			i.stateMu.Unlock()
			return errors.New("process/closed arrived before process/exited")
		}
		if i.protocolClosed {
			i.stateMu.Unlock()
			return errors.New("duplicate process/closed notification")
		}
		i.protocolClosed = true
		if i.cleanupTimer != nil {
			i.cleanupTimer.Stop()
		}
		i.stateMu.Unlock()
		if err := i.publishEvent(message); err != nil {
			return err
		}
		if i.pendingCount() == 0 {
			i.beginFinalize(TerminalClosed, nil)
			return nil
		}
		i.stateMu.Lock()
		if !i.finishing.Load() {
			i.terminalTimer = time.AfterFunc(i.options.CleanupGrace, func() {
				i.beginFinalize(TerminalUnknown, ErrTerminalReplyMissing)
			})
		}
		i.stateMu.Unlock()
		i.maybeFinalizeClosed()
		return nil
	default:
		return fmt.Errorf("unsupported exec-server notification %q", message.Method)
	}
}

func (i *Instance) publishEvent(message codexwire.Message) error {
	select {
	case i.events <- message:
		return nil
	default:
		return errors.New("exec-server event buffer is full")
	}
}

func (i *Instance) pendingCount() int {
	i.pendingMu.Lock()
	defer i.pendingMu.Unlock()
	return len(i.pending)
}

func (i *Instance) maybeFinalizeClosed() {
	i.stateMu.Lock()
	protocolClosed := i.protocolClosed
	i.stateMu.Unlock()
	if protocolClosed && i.pendingCount() == 0 {
		i.beginFinalize(TerminalClosed, nil)
	}
}

func (i *Instance) beginFinalize(requested TerminalState, cause error) {
	i.finishOnce.Do(func() {
		i.finishing.Store(true)
		i.stateMu.Lock()
		if i.cleanupTimer != nil {
			i.cleanupTimer.Stop()
		}
		if i.terminalTimer != nil {
			i.terminalTimer.Stop()
		}
		i.stateMu.Unlock()
		i.failPending(ErrInstanceClosed)
		go i.finalize(requested, cause)
	})
}

func (i *Instance) failPending(err error) {
	i.pendingMu.Lock()
	pending := i.pending
	i.pending = make(map[string]chan responseResult)
	i.pendingMu.Unlock()
	for _, response := range pending {
		response <- responseResult{err: err}
	}
}

func (i *Instance) finalize(requested TerminalState, cause error) {
	closeErr := i.transport.CloseStdin()
	waitContext, cancelWait := context.WithTimeout(context.Background(), i.options.ShutdownGrace)
	waitErr := i.transport.Wait(waitContext)
	cancelWait()
	if errors.Is(waitErr, context.DeadlineExceeded) {
		killErr := i.transport.Kill()
		secondWaitContext, cancelSecondWait := context.WithTimeout(context.Background(), i.options.ShutdownGrace)
		secondWaitErr := i.transport.Wait(secondWaitContext)
		cancelSecondWait()
		waitErr = errors.Join(waitErr, killErr, secondWaitErr)
	}

	i.stateMu.Lock()
	processID := i.processID
	exitCode := cloneIntPointer(i.exitCode)
	sandboxDenied := cloneBoolPointer(i.sandboxDenied)
	protocolClosed := i.protocolClosed
	exited := i.exited
	i.stateMu.Unlock()

	var verifyErr error
	if processID != "" {
		verifyContext, cancelVerify := context.WithTimeout(context.Background(), i.options.ShutdownGrace)
		verifyErr = i.options.VerifyTreeEmpty(verifyContext, processID)
		cancelVerify()
	}

	state := requested
	if requested == TerminalClosed && (!exited || !protocolClosed) {
		state = TerminalUnknown
		cause = errors.Join(cause, errors.New("normal terminal omitted process/exited or process/closed"))
	}
	if requested == TerminalCleanupForced && !exited {
		state = TerminalUnknown
		cause = errors.Join(cause, errors.New("forced cleanup lacked process/exited evidence"))
	}
	if closeErr != nil || waitErr != nil || verifyErr != nil {
		state = TerminalUnknown
		cause = errors.Join(cause, closeErr, waitErr, verifyErr)
	}

	i.cancel()
	select {
	case <-i.pumpDone:
	case <-time.After(i.options.ShutdownGrace):
		state = TerminalUnknown
		cause = errors.Join(cause, errors.New("exec-server receive pump did not stop"))
	}

	i.resultMu.Lock()
	i.result = Result{
		InstanceID:     i.id,
		ProcessID:      processID,
		State:          state,
		ExitCode:       exitCode,
		SandboxDenied:  sandboxDenied,
		ProtocolClosed: protocolClosed,
		Cause:          cause,
	}
	i.resultMu.Unlock()
	close(i.events)
	close(i.done)
}

func cloneIntPointer(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneBoolPointer(value *bool) *bool {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
