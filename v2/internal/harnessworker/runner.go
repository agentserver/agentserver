package harnessworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"time"

	"github.com/agentserver/agentserver/v2/internal/codexwire"
)

const (
	maxConfiguredAppServerEvents        = 4 * 1024
	maxConfiguredAppServerBufferedBytes = 64 * 1024 * 1024
	maxInterruptGrace                   = 5 * time.Minute
)

var ErrAppServerEventBufferFull = errors.New("app-server event buffer is full")
var ErrAppServerEventTooLarge = errors.New("app-server event exceeds configured byte limit")

// AppServerTransport is the bounded, newline-delimited stdio peer owned by the
// worker. AppServerRunner is its only receiver and its only logical writer.
type AppServerTransport interface {
	Send(any) error
	Receive(context.Context) (codexwire.Message, error)
}

type AppServerRunnerOptions struct {
	EventBuffer        int
	MaxEventBytes      int
	InterruptGrace     time.Duration
	MaxPromptTextBytes int
}

func DefaultAppServerRunnerOptions() AppServerRunnerOptions {
	return AppServerRunnerOptions{
		EventBuffer:        64,
		MaxEventBytes:      1024 * 1024,
		InterruptGrace:     10 * time.Second,
		MaxPromptTextBytes: 1024 * 1024,
	}
}

type AppServerClientInfo struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Version string `json:"version"`
}

// AppServerThreadStart contains only the production dynamic-tool-only profile.
// Approval policy, sandbox, environments, capability roots, and dynamic tools
// are fixed by the runner and cannot be supplied as arbitrary wire payloads.
type AppServerThreadStart struct {
	Model                 string
	CWD                   string
	BaseInstructions      string
	DeveloperInstructions string
}

// AppServerThreadResume binds a native rollout resume to the already-frozen
// catalog. Dynamic tools cannot be overridden by thread/resume.
type AppServerThreadResume struct {
	ThreadID                string
	RolloutPath             string
	CWD                     string
	CheckpointCatalogDigest string
}

type AppServerRunRequest struct {
	RunID                string
	RunAttemptGeneration int64
	ClientInfo           AppServerClientInfo
	Catalog              *Catalog
	Start                *AppServerThreadStart
	Resume               *AppServerThreadResume
	UserText             string
}

type AppServerInitializeResult struct {
	UserAgent      string `json:"userAgent"`
	CodexHome      string `json:"codexHome"`
	PlatformFamily string `json:"platformFamily"`
	PlatformOS     string `json:"platformOs"`
}

type AppServerThread struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Ephemeral bool   `json:"ephemeral"`
	Path      string `json:"path"`
	CWD       string `json:"cwd"`
}

type AppServerThreadResult struct {
	Thread        AppServerThread `json:"thread"`
	Model         string          `json:"model"`
	ModelProvider string          `json:"modelProvider"`
	CWD           string          `json:"cwd"`
}

type AppServerTurn struct {
	ID     string          `json:"id"`
	Status string          `json:"status"`
	Error  json.RawMessage `json:"error"`
}

type AppServerTerminal struct {
	ThreadID string        `json:"threadId"`
	Turn     AppServerTurn `json:"turn"`
}

type AppServerRunResult struct {
	Initialize AppServerInitializeResult
	Thread     AppServerThreadResult
	Turn       AppServerTurn
	Terminal   AppServerTerminal
	Resumed    bool
}

type AppServerRPCError struct {
	Method  string
	Code    int64
	Message string
	Data    json.RawMessage
}

func (e *AppServerRPCError) Error() string {
	return fmt.Sprintf("app-server %s failed with RPC error %d: %s", e.Method, e.Code, e.Message)
}

// AppServerRunner drives one app-server child for one run attempt. It is
// intentionally one-shot: a failed or terminal attempt gets a fresh child and
// a fresh runner rather than reusing uncertain stdio state.
type AppServerRunner struct {
	peer    AppServerTransport
	bridge  *DynamicBridge
	options AppServerRunnerOptions
	events  chan codexwire.Message

	mu      sync.Mutex
	started bool
}

func NewAppServerRunner(
	peer AppServerTransport,
	bridge *DynamicBridge,
	options AppServerRunnerOptions,
) (*AppServerRunner, error) {
	if peer == nil {
		return nil, errors.New("app-server transport is required")
	}
	if bridge == nil {
		return nil, errors.New("dynamic bridge is required")
	}
	if options.EventBuffer < 1 {
		return nil, errors.New("app-server event buffer must be positive")
	}
	if options.EventBuffer > maxConfiguredAppServerEvents {
		return nil, fmt.Errorf("app-server event buffer exceeds hard maximum %d", maxConfiguredAppServerEvents)
	}
	if options.MaxEventBytes < 1 {
		return nil, errors.New("app-server max event bytes must be positive")
	}
	if options.MaxEventBytes > maxConfiguredAppServerBufferedBytes {
		return nil, fmt.Errorf("app-server max event bytes exceeds hard maximum %d", maxConfiguredAppServerBufferedBytes)
	}
	if options.EventBuffer > maxConfiguredAppServerBufferedBytes/options.MaxEventBytes {
		return nil, fmt.Errorf(
			"app-server event buffer could retain more than hard maximum %d bytes",
			maxConfiguredAppServerBufferedBytes,
		)
	}
	if options.InterruptGrace <= 0 {
		return nil, errors.New("app-server interrupt grace must be positive")
	}
	if options.InterruptGrace > maxInterruptGrace {
		return nil, fmt.Errorf("app-server interrupt grace exceeds hard maximum %s", maxInterruptGrace)
	}
	if options.MaxPromptTextBytes < 1 {
		return nil, errors.New("app-server max prompt text bytes must be positive")
	}
	if options.MaxPromptTextBytes > maxConfiguredPayloadBytes {
		return nil, fmt.Errorf("app-server max prompt text bytes exceeds hard maximum %d", maxConfiguredPayloadBytes)
	}
	return &AppServerRunner{
		peer:    peer,
		bridge:  bridge,
		options: options,
		events:  make(chan codexwire.Message, options.EventBuffer),
	}, nil
}

// Events contains validated app-server notifications without rewriting their
// method or params. The channel is bounded and closes when Run returns.
func (r *AppServerRunner) Events() <-chan codexwire.Message { return r.events }

type appServerReadResult struct {
	message codexwire.Message
	err     error
}

type appServerProtocolState struct {
	runner *AppServerRunner

	incoming <-chan appServerReadResult
	toolCtx  context.Context
	request  AppServerRunRequest

	threadID      string
	threadSession string
	turnID        string
	resumed       bool
	threadStarted bool
	turnStarted   bool
	dropEvents    bool
	interruptID   int64
}

type appServerInitializeParams struct {
	ClientInfo   AppServerClientInfo         `json:"clientInfo"`
	Capabilities appServerClientCapabilities `json:"capabilities"`
}

type appServerClientCapabilities struct {
	ExperimentalAPI bool `json:"experimentalApi"`
}

type appServerThreadStartParams struct {
	Model                   string             `json:"model"`
	CWD                     string             `json:"cwd"`
	ApprovalPolicy          string             `json:"approvalPolicy"`
	Sandbox                 string             `json:"sandbox"`
	BaseInstructions        string             `json:"baseInstructions,omitempty"`
	DeveloperInstructions   string             `json:"developerInstructions,omitempty"`
	Ephemeral               bool               `json:"ephemeral"`
	ThreadSource            string             `json:"threadSource"`
	Environments            []any              `json:"environments"`
	DynamicTools            []DynamicNamespace `json:"dynamicTools"`
	SelectedCapabilityRoots []any              `json:"selectedCapabilityRoots"`
}

type appServerThreadResumeParams struct {
	ThreadID     string `json:"threadId"`
	Path         string `json:"path"`
	ExcludeTurns bool   `json:"excludeTurns"`
}

type appServerTurnStartParams struct {
	ThreadID       string               `json:"threadId"`
	CWD            string               `json:"cwd,omitempty"`
	ApprovalPolicy string               `json:"approvalPolicy,omitempty"`
	Environments   []any                `json:"environments"`
	Input          []appServerTextInput `json:"input"`
}

type appServerTextInput struct {
	Type         string `json:"type"`
	Text         string `json:"text"`
	TextElements []any  `json:"textElements"`
}

type appServerTurnInterruptParams struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
}

type appServerTurnStartResult struct {
	Turn AppServerTurn `json:"turn"`
}

type appServerThreadStarted struct {
	Thread AppServerThread `json:"thread"`
}

type appServerTurnStarted struct {
	ThreadID string        `json:"threadId"`
	Turn     AppServerTurn `json:"turn"`
}

// Run performs initialize -> initialized -> thread/start|resume -> turn/start,
// then owns all bidirectional traffic until the matching turn terminal. A
// caller cancellation after turn/start is sent is converted to turn/interrupt
// and a bounded wait for the terminal, rather than abandoning the stdio pipe.
func (r *AppServerRunner) Run(ctx context.Context, request AppServerRunRequest) (result AppServerRunResult, err error) {
	if ctx == nil {
		return AppServerRunResult{}, errors.New("app-server run context is required")
	}
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return AppServerRunResult{}, errors.New("app-server runner is one-shot")
	}
	r.started = true
	r.mu.Unlock()

	defer close(r.events)
	defer func() { r.bridge.Close(err) }()
	if err := r.validateRequest(request); err != nil {
		return AppServerRunResult{}, err
	}

	readCtx, cancelRead := context.WithCancel(context.Background())
	defer cancelRead()
	incoming := make(chan appServerReadResult, 1)
	go pumpAppServer(readCtx, r.peer, incoming)
	toolCtx, cancelTools := context.WithCancelCause(context.Background())
	defer cancelTools(errors.New("app-server run ended"))
	state := &appServerProtocolState{
		runner:      r,
		incoming:    incoming,
		toolCtx:     toolCtx,
		request:     request,
		resumed:     request.Resume != nil,
		interruptID: 4,
	}

	if err := state.sendRequest(1, "initialize", appServerInitializeParams{
		ClientInfo:   request.ClientInfo,
		Capabilities: appServerClientCapabilities{ExperimentalAPI: true},
	}); err != nil {
		return AppServerRunResult{}, err
	}
	initializeMessage, err := state.waitResponse(ctx, 1, "initialize")
	if err != nil {
		return AppServerRunResult{}, err
	}
	if err := decodeAppServerResult(initializeMessage, &result.Initialize); err != nil {
		return AppServerRunResult{}, fmt.Errorf("decode initialize response: %w", err)
	}
	if err := validateInitializeResult(result.Initialize); err != nil {
		return AppServerRunResult{}, err
	}
	if err := state.sendNotification("initialized", nil); err != nil {
		return AppServerRunResult{}, err
	}

	if request.Start != nil {
		start := request.Start
		if err := state.sendRequest(2, "thread/start", appServerThreadStartParams{
			Model:                   start.Model,
			CWD:                     start.CWD,
			ApprovalPolicy:          "never",
			Sandbox:                 "read-only",
			BaseInstructions:        start.BaseInstructions,
			DeveloperInstructions:   start.DeveloperInstructions,
			Ephemeral:               false,
			ThreadSource:            "user",
			Environments:            []any{},
			DynamicTools:            request.Catalog.DynamicTools(),
			SelectedCapabilityRoots: []any{},
		}); err != nil {
			return AppServerRunResult{}, err
		}
	} else {
		resume := request.Resume
		if err := state.sendRequest(2, "thread/resume", appServerThreadResumeParams{
			ThreadID:     resume.ThreadID,
			Path:         resume.RolloutPath,
			ExcludeTurns: true,
		}); err != nil {
			return AppServerRunResult{}, err
		}
	}
	threadMethod := "thread/start"
	if state.resumed {
		threadMethod = "thread/resume"
	}
	threadMessage, err := state.waitResponse(ctx, 2, threadMethod)
	if err != nil {
		return AppServerRunResult{}, err
	}
	if err := decodeAppServerResult(threadMessage, &result.Thread); err != nil {
		return AppServerRunResult{}, fmt.Errorf("decode %s response: %w", threadMethod, err)
	}
	if err := validateThreadResult(result.Thread); err != nil {
		return AppServerRunResult{}, err
	}
	state.threadID = result.Thread.Thread.ID
	state.threadSession = result.Thread.Thread.SessionID
	if request.Resume != nil && state.threadID != request.Resume.ThreadID {
		return AppServerRunResult{}, fmt.Errorf("resumed thread id %q does not match checkpoint thread %q", state.threadID, request.Resume.ThreadID)
	}
	result.Resumed = state.resumed
	if !state.resumed {
		if err := state.waitThreadStarted(ctx); err != nil {
			return AppServerRunResult{}, err
		}
	}

	turnParams := appServerTurnStartParams{
		ThreadID:     state.threadID,
		Environments: []any{},
		Input: []appServerTextInput{{
			Type:         "text",
			Text:         request.UserText,
			TextElements: []any{},
		}},
	}
	if request.Resume != nil {
		turnParams.CWD = request.Resume.CWD
		turnParams.ApprovalPolicy = "never"
	}
	if err := state.sendRequest(3, "turn/start", turnParams); err != nil {
		return AppServerRunResult{}, err
	}
	turnMessage, cancellation, err := state.waitTurnStartResponse(ctx, 3)
	if err != nil {
		return AppServerRunResult{}, err
	}
	var turnResult appServerTurnStartResult
	if err := decodeAppServerResult(turnMessage, &turnResult); err != nil {
		return AppServerRunResult{}, fmt.Errorf("decode turn/start response: %w", err)
	}
	if err := validateNameText("turn/start turn id", turnResult.Turn.ID, maxIdentityBytes); err != nil {
		return AppServerRunResult{}, err
	}
	if turnResult.Turn.Status != "inProgress" {
		return AppServerRunResult{}, fmt.Errorf("turn/start returned invalid turn id or status: id=%q status=%q", turnResult.Turn.ID, turnResult.Turn.Status)
	}
	result.Turn = turnResult.Turn
	state.turnID = turnResult.Turn.ID

	terminal, runErr := state.runTurn(ctx, cancellation)
	result.Terminal = terminal
	if runErr != nil {
		return result, runErr
	}
	return result, nil
}

func (r *AppServerRunner) validateRequest(request AppServerRunRequest) error {
	if err := validateNameText("run id", request.RunID, maxIdentityBytes); err != nil {
		return err
	}
	if request.RunAttemptGeneration < 1 {
		return errors.New("run attempt generation must be positive")
	}
	if err := validateNameText("app-server client name", request.ClientInfo.Name, maxIdentityBytes); err != nil {
		return err
	}
	if err := validateNameText("app-server client title", request.ClientInfo.Title, maxIdentityBytes); err != nil {
		return err
	}
	if err := validateNameText("app-server client version", request.ClientInfo.Version, maxIdentityBytes); err != nil {
		return err
	}
	if request.Catalog == nil {
		return errors.New("frozen dynamic tool catalog is required")
	}
	if (request.Start == nil) == (request.Resume == nil) {
		return errors.New("exactly one of thread start or resume is required")
	}
	if err := validateText("user input", request.UserText, r.options.MaxPromptTextBytes); err != nil {
		return err
	}
	if request.Start != nil {
		if err := validateNameText("model", request.Start.Model, maxIdentityBytes); err != nil {
			return err
		}
		if err := validateAbsolutePath("thread cwd", request.Start.CWD); err != nil {
			return err
		}
		if err := validateText("base instructions", request.Start.BaseInstructions, r.options.MaxPromptTextBytes); err != nil {
			return err
		}
		if err := validateText("developer instructions", request.Start.DeveloperInstructions, r.options.MaxPromptTextBytes); err != nil {
			return err
		}
		return nil
	}
	resume := request.Resume
	if err := validateNameText("checkpoint thread id", resume.ThreadID, maxIdentityBytes); err != nil {
		return err
	}
	if err := validateAbsolutePath("checkpoint rollout path", resume.RolloutPath); err != nil {
		return err
	}
	if err := validateAbsolutePath("resumed turn cwd", resume.CWD); err != nil {
		return err
	}
	if !equalDigest(request.Catalog.Digest(), resume.CheckpointCatalogDigest) {
		return fmt.Errorf("checkpoint tool catalog digest %q does not match verified catalog %q", resume.CheckpointCatalogDigest, request.Catalog.Digest())
	}
	return nil
}

func validateAbsolutePath(label, value string) error {
	if err := validateNameText(label, value, maxConfiguredPayloadBytes); err != nil {
		return err
	}
	if !filepath.IsAbs(value) {
		return fmt.Errorf("%s must be absolute", label)
	}
	return nil
}

func validateInitializeResult(result AppServerInitializeResult) error {
	if err := validateText("app-server user agent", result.UserAgent, maxConfiguredDescriptionBytes); err != nil {
		return err
	}
	if result.UserAgent == "" {
		return errors.New("app-server user agent must not be empty")
	}
	if err := validateAbsolutePath("app-server Codex home", result.CodexHome); err != nil {
		return err
	}
	if err := validateNameText("app-server platform family", result.PlatformFamily, maxIdentityBytes); err != nil {
		return err
	}
	return validateNameText("app-server platform OS", result.PlatformOS, maxIdentityBytes)
}

func validateThreadResult(result AppServerThreadResult) error {
	for _, field := range []struct {
		label string
		value string
	}{
		{"thread id", result.Thread.ID},
		{"session id", result.Thread.SessionID},
		{"thread model", result.Model},
		{"model provider", result.ModelProvider},
	} {
		if err := validateNameText(field.label, field.value, maxIdentityBytes); err != nil {
			return err
		}
	}
	if err := validateAbsolutePath("thread rollout path", result.Thread.Path); err != nil {
		return err
	}
	if err := validateAbsolutePath("thread response cwd", result.Thread.CWD); err != nil {
		return err
	}
	if err := validateAbsolutePath("effective thread cwd", result.CWD); err != nil {
		return err
	}
	if result.Thread.Ephemeral {
		return errors.New("app-server unexpectedly created an ephemeral thread")
	}
	return nil
}

func pumpAppServer(ctx context.Context, peer AppServerTransport, output chan<- appServerReadResult) {
	defer close(output)
	for {
		message, err := peer.Receive(ctx)
		select {
		case output <- appServerReadResult{message: message, err: err}:
		case <-ctx.Done():
			return
		}
		if err != nil {
			return
		}
	}
}

func (s *appServerProtocolState) sendRequest(id int64, method string, params any) error {
	if err := s.runner.peer.Send(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return fmt.Errorf("write app-server %s request: %w", method, err)
	}
	return nil
}

func (s *appServerProtocolState) sendNotification(method string, params any) error {
	message := map[string]any{"method": method}
	if params != nil {
		message["params"] = params
	}
	if err := s.runner.peer.Send(message); err != nil {
		return fmt.Errorf("write app-server %s notification: %w", method, err)
	}
	return nil
}

func (s *appServerProtocolState) waitResponse(ctx context.Context, id int64, method string) (codexwire.Message, error) {
	for {
		select {
		case <-ctx.Done():
			return codexwire.Message{}, contextFailure(ctx)
		case event, ok := <-s.runner.bridge.Events():
			if !ok {
				return codexwire.Message{}, errors.New("dynamic bridge closed during app-server lifecycle")
			}
			return codexwire.Message{}, fmt.Errorf("unexpected dynamic bridge event before turn start: call=%q kind=%d", event.CallID, event.Kind)
		case read, ok := <-s.incoming:
			if !ok {
				return codexwire.Message{}, io.EOF
			}
			if read.err != nil {
				return codexwire.Message{}, fmt.Errorf("read app-server while waiting for %s: %w", method, read.err)
			}
			message := read.message
			switch message.Kind {
			case codexwire.KindResponse, codexwire.KindError:
				if !appServerResponseIDMatches(message.ID, id) {
					return codexwire.Message{}, fmt.Errorf("unexpected app-server response id %s while waiting for %s id %d", message.ID, method, id)
				}
				if message.Kind == codexwire.KindError {
					return codexwire.Message{}, rpcResponseError(method, message)
				}
				return message, nil
			case codexwire.KindNotification:
				terminal, err := s.processNotification(message)
				if err != nil {
					return codexwire.Message{}, err
				}
				if terminal != nil {
					return codexwire.Message{}, fmt.Errorf("turn terminal arrived while waiting for %s", method)
				}
			case codexwire.KindRequest:
				return codexwire.Message{}, fmt.Errorf("unexpected app-server reverse request %q while waiting for %s", message.Method, method)
			default:
				return codexwire.Message{}, fmt.Errorf("unexpected app-server message kind %s", message.Kind)
			}
		}
	}
}

func (s *appServerProtocolState) waitTurnStartResponse(
	ctx context.Context,
	id int64,
) (codexwire.Message, error, error) {
	ctxDone := ctx.Done()
	var cancellation error
	var timer *time.Timer
	var timeout <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		select {
		case <-ctxDone:
			cancellation = contextFailure(ctx)
			ctxDone = nil
			timer = time.NewTimer(s.runner.options.InterruptGrace)
			timeout = timer.C
		case <-timeout:
			return codexwire.Message{}, cancellation, errors.Join(
				cancellation,
				errors.New("turn/start response remained ambiguous through interrupt grace"),
			)
		case event, ok := <-s.runner.bridge.Events():
			if !ok {
				return codexwire.Message{}, cancellation, errors.New("dynamic bridge closed during turn/start")
			}
			return codexwire.Message{}, cancellation, fmt.Errorf("unexpected dynamic bridge event before turn/start response: call=%q kind=%d", event.CallID, event.Kind)
		case read, ok := <-s.incoming:
			if !ok {
				return codexwire.Message{}, cancellation, io.EOF
			}
			if read.err != nil {
				return codexwire.Message{}, cancellation, fmt.Errorf("read app-server while waiting for turn/start: %w", read.err)
			}
			message := read.message
			switch message.Kind {
			case codexwire.KindResponse, codexwire.KindError:
				if !appServerResponseIDMatches(message.ID, id) {
					return codexwire.Message{}, cancellation, fmt.Errorf("unexpected app-server response id %s while waiting for turn/start id %d", message.ID, id)
				}
				if message.Kind == codexwire.KindError {
					return codexwire.Message{}, cancellation, rpcResponseError("turn/start", message)
				}
				return message, cancellation, nil
			case codexwire.KindNotification:
				terminal, err := s.processNotification(message)
				if err != nil {
					return codexwire.Message{}, cancellation, err
				}
				if terminal != nil {
					return codexwire.Message{}, cancellation, errors.New("turn terminal arrived before turn/start response")
				}
			case codexwire.KindRequest:
				return codexwire.Message{}, cancellation, fmt.Errorf("app-server reverse request %q arrived before turn/start response", message.Method)
			default:
				return codexwire.Message{}, cancellation, fmt.Errorf("unexpected app-server message kind %s", message.Kind)
			}
		}
	}
}

func (s *appServerProtocolState) waitThreadStarted(ctx context.Context) error {
	for !s.threadStarted {
		select {
		case <-ctx.Done():
			return contextFailure(ctx)
		case event, ok := <-s.runner.bridge.Events():
			if !ok {
				return errors.New("dynamic bridge closed before thread/started")
			}
			return fmt.Errorf("unexpected dynamic bridge event before thread/started: call=%q kind=%d", event.CallID, event.Kind)
		case read, ok := <-s.incoming:
			if !ok {
				return io.EOF
			}
			if read.err != nil {
				return fmt.Errorf("read app-server while waiting for thread/started: %w", read.err)
			}
			if read.message.Kind != codexwire.KindNotification {
				return fmt.Errorf("unexpected app-server %s while waiting for thread/started", read.message.Kind)
			}
			terminal, err := s.processNotification(read.message)
			if err != nil {
				return err
			}
			if terminal != nil {
				return errors.New("turn terminal arrived before thread/started")
			}
		}
	}
	return nil
}

type appServerAbort struct {
	cause      error
	timer      *time.Timer
	timeout    <-chan time.Time
	ackPending bool
}

func (s *appServerProtocolState) beginAbort(cause error) (*appServerAbort, error) {
	if cause == nil {
		cause = errors.New("app-server run aborted")
	}
	s.runner.bridge.CancelTurn(s.turnID, cause)
	if err := s.sendRequest(s.interruptID, "turn/interrupt", appServerTurnInterruptParams{
		ThreadID: s.threadID,
		TurnID:   s.turnID,
	}); err != nil {
		return nil, errors.Join(cause, err)
	}
	timer := time.NewTimer(s.runner.options.InterruptGrace)
	return &appServerAbort{cause: cause, timer: timer, timeout: timer.C, ackPending: true}, nil
}

func (s *appServerProtocolState) runTurn(ctx context.Context, initialAbort error) (AppServerTerminal, error) {
	ctxDone := ctx.Done()
	bridgeEvents := s.runner.bridge.Events()
	var abort *appServerAbort
	if initialAbort != nil {
		var err error
		abort, err = s.beginAbort(initialAbort)
		if err != nil {
			return AppServerTerminal{}, err
		}
		ctxDone = nil
	}
	defer func() {
		if abort != nil && abort.timer != nil {
			abort.timer.Stop()
		}
	}()

	startAbort := func(cause error) error {
		if errors.Is(cause, ErrAppServerEventBufferFull) || errors.Is(cause, ErrAppServerEventTooLarge) {
			s.dropEvents = true
		}
		if abort != nil {
			return nil
		}
		var err error
		abort, err = s.beginAbort(cause)
		if err != nil {
			return err
		}
		ctxDone = nil
		return nil
	}

	for {
		var timeout <-chan time.Time
		if abort != nil {
			timeout = abort.timeout
		}
		select {
		case <-ctxDone:
			if err := startAbort(contextFailure(ctx)); err != nil {
				return AppServerTerminal{}, err
			}
		case <-timeout:
			return AppServerTerminal{}, errors.Join(abort.cause, errors.New("turn/interrupt did not reach a terminal before cleanup grace expired"))
		case event, ok := <-bridgeEvents:
			if !ok {
				bridgeEvents = nil
				if err := startAbort(errors.New("dynamic bridge closed before turn terminal")); err != nil {
					return AppServerTerminal{}, err
				}
				continue
			}
			if abort != nil {
				continue
			}
			switch event.Kind {
			case BridgeResultReady:
				lease, claimed := s.runner.bridge.ClaimResponse(event.CallID)
				if !claimed {
					continue
				}
				response := struct {
					ID     json.RawMessage   `json:"id"`
					Result DynamicToolResult `json:"result"`
				}{ID: lease.RequestID, Result: lease.Result}
				if err := s.runner.peer.Send(response); err != nil {
					writeErr := fmt.Errorf("write item/tool/call response for %q: %w", lease.CallID, err)
					cleanupErr := s.runner.bridge.ResponseWriteFailed(lease.CallID, writeErr)
					return AppServerTerminal{}, errors.Join(writeErr, cleanupErr)
				}
				if err := s.runner.bridge.ResponseWritten(lease.CallID); err != nil {
					return AppServerTerminal{}, fmt.Errorf("commit item/tool/call response write: %w", err)
				}
			case BridgeCallFailed:
				if err := startAbort(fmt.Errorf("dynamic tool call %q failed: %w", event.CallID, event.Err)); err != nil {
					return AppServerTerminal{}, err
				}
			default:
				if err := startAbort(fmt.Errorf("unknown dynamic bridge event kind %d", event.Kind)); err != nil {
					return AppServerTerminal{}, err
				}
			}
		case read, ok := <-s.incoming:
			if !ok {
				cause := io.EOF
				if abort != nil {
					cause = errors.Join(abort.cause, cause)
				}
				return AppServerTerminal{}, cause
			}
			if read.err != nil {
				cause := fmt.Errorf("read app-server event loop: %w", read.err)
				if abort != nil {
					cause = errors.Join(abort.cause, cause)
				}
				return AppServerTerminal{}, cause
			}
			message := read.message
			switch message.Kind {
			case codexwire.KindNotification:
				terminal, notificationErr := s.processNotification(message)
				if terminal != nil {
					s.runner.bridge.TurnTerminal(s.turnID)
					if outstanding := s.runner.bridge.Outstanding(); outstanding != 0 {
						notificationErr = errors.Join(notificationErr, fmt.Errorf("turn terminal left %d dynamic callbacks outstanding", outstanding))
					}
					if abort != nil {
						return *terminal, errors.Join(abort.cause, notificationErr)
					}
					return *terminal, notificationErr
				}
				if notificationErr != nil {
					if err := startAbort(notificationErr); err != nil {
						return AppServerTerminal{}, err
					}
				}
			case codexwire.KindRequest:
				if abort != nil {
					continue
				}
				if !s.turnStarted {
					if err := startAbort(fmt.Errorf("reverse request %q arrived before turn/started", message.Method)); err != nil {
						return AppServerTerminal{}, err
					}
					continue
				}
				if message.Method != "item/tool/call" {
					if err := startAbort(fmt.Errorf("app-server reverse request %q is not allowlisted", message.Method)); err != nil {
						return AppServerTerminal{}, err
					}
					continue
				}
				scope := RunScope{
					RunID:                s.request.RunID,
					ThreadID:             s.threadID,
					TurnID:               s.turnID,
					RunAttemptGeneration: s.request.RunAttemptGeneration,
				}
				if err := s.runner.bridge.HandleToolCall(s.toolCtx, message, scope); err != nil {
					if abortErr := startAbort(fmt.Errorf("reject item/tool/call: %w", err)); abortErr != nil {
						return AppServerTerminal{}, abortErr
					}
				}
			case codexwire.KindResponse, codexwire.KindError:
				if abort == nil || !appServerResponseIDMatches(message.ID, s.interruptID) {
					if err := startAbort(fmt.Errorf("unexpected app-server response id %s in turn event loop", message.ID)); err != nil {
						return AppServerTerminal{}, err
					}
					continue
				}
				if !abort.ackPending {
					continue
				}
				abort.ackPending = false
				if message.Kind == codexwire.KindError {
					abort.cause = errors.Join(abort.cause, rpcResponseError("turn/interrupt", message))
					continue
				}
				var empty map[string]json.RawMessage
				if err := decodeAppServerResult(message, &empty); err != nil || empty == nil || len(empty) != 0 {
					abort.cause = errors.Join(abort.cause, errors.New("turn/interrupt returned a non-empty result"))
					continue
				}
			default:
				if err := startAbort(fmt.Errorf("unexpected app-server message kind %s", message.Kind)); err != nil {
					return AppServerTerminal{}, err
				}
			}
		}
	}
}

func (s *appServerProtocolState) processNotification(message codexwire.Message) (*AppServerTerminal, error) {
	if message.Kind != codexwire.KindNotification {
		return nil, fmt.Errorf("message kind is %s, not notification", message.Kind)
	}
	var terminal *AppServerTerminal
	switch message.Method {
	case "thread/started":
		var notification appServerThreadStarted
		if err := decodeAppServerParams(message, &notification); err != nil {
			return nil, fmt.Errorf("decode thread/started: %w", err)
		}
		if s.resumed {
			return nil, errors.New("thread/resume unexpectedly emitted thread/started")
		}
		if s.threadID == "" || notification.Thread.ID != s.threadID || notification.Thread.SessionID != s.threadSession {
			return nil, errors.New("thread/started identity does not match thread/start response")
		}
		if s.threadStarted {
			return nil, errors.New("duplicate thread/started notification")
		}
		s.threadStarted = true
	case "turn/started":
		var notification appServerTurnStarted
		if err := decodeAppServerParams(message, &notification); err != nil {
			return nil, fmt.Errorf("decode turn/started: %w", err)
		}
		if s.turnID == "" || notification.ThreadID != s.threadID || notification.Turn.ID != s.turnID || notification.Turn.Status != "inProgress" {
			return nil, errors.New("turn/started identity or status does not match turn/start response")
		}
		if s.turnStarted {
			return nil, errors.New("duplicate turn/started notification")
		}
		s.turnStarted = true
	case "turn/completed":
		var notification AppServerTerminal
		if err := decodeAppServerParams(message, &notification); err != nil {
			return nil, fmt.Errorf("decode turn/completed: %w", err)
		}
		if s.turnID == "" || notification.ThreadID != s.threadID || notification.Turn.ID != s.turnID {
			return nil, errors.New("turn/completed identity does not match active turn")
		}
		switch notification.Turn.Status {
		case "completed", "interrupted", "failed":
		default:
			return nil, fmt.Errorf("turn/completed has non-terminal status %q", notification.Turn.Status)
		}
		terminal = &notification
		if !s.turnStarted {
			return terminal, errors.New("turn/completed arrived before turn/started")
		}
	}
	if err := s.emit(message); err != nil {
		return terminal, err
	}
	return terminal, nil
}

func (s *appServerProtocolState) emit(message codexwire.Message) error {
	if s.dropEvents {
		return nil
	}
	if retained := appServerMessageRetainedBytes(message); retained > s.runner.options.MaxEventBytes {
		return fmt.Errorf("%w: retained bytes %d, limit %d", ErrAppServerEventTooLarge, retained, s.runner.options.MaxEventBytes)
	}
	select {
	case s.runner.events <- message:
		return nil
	default:
		return ErrAppServerEventBufferFull
	}
}

func appServerMessageRetainedBytes(message codexwire.Message) int {
	retained := len(message.Raw) + len(message.ID) + len(message.Method) + len(message.Params) + len(message.Result)
	if message.Error != nil {
		retained += len(message.Error.Message) + len(message.Error.Data)
	}
	return retained
}

func decodeAppServerParams(message codexwire.Message, destination any) error {
	if len(message.Params) == 0 {
		return errors.New("notification params are required")
	}
	return json.Unmarshal(message.Params, destination)
}

func decodeAppServerResult(message codexwire.Message, destination any) error {
	if message.Kind != codexwire.KindResponse {
		return fmt.Errorf("message kind is %s, not response", message.Kind)
	}
	return json.Unmarshal(message.Result, destination)
}

func rpcResponseError(method string, message codexwire.Message) error {
	if message.Kind != codexwire.KindError || message.Error == nil {
		return fmt.Errorf("app-server %s returned a malformed error response", method)
	}
	return &AppServerRPCError{
		Method:  method,
		Code:    message.Error.Code,
		Message: message.Error.Message,
		Data:    append(json.RawMessage(nil), message.Error.Data...),
	}
}

func appServerResponseIDMatches(raw json.RawMessage, want int64) bool {
	key, err := canonicalRequestIDKey(raw)
	return err == nil && key == fmt.Sprintf("n:%d", want)
}

func contextFailure(ctx context.Context) error {
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	return ctx.Err()
}
