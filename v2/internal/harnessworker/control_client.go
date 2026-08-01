package harnessworker

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/agentserver/agentserver/v2/internal/codexwire"
	"github.com/agentserver/agentserver/v2/internal/harnessbootstrap"
	"github.com/agentserver/agentserver/v2/internal/harnesscontrol"
	"github.com/agentserver/agentserver/v2/internal/runmanifest"
	"nhooyr.io/websocket"
)

const (
	workerControlCommandBuffer        = 8
	maximumWorkerPendingApprovals     = 256
	defaultWorkerPendingApprovalLimit = 64
)

type WorkerControlClientConfig struct {
	Manifest                runmanifest.Manifest
	SignedManifest          runmanifest.SignedManifest
	ControlCapability       string
	WorkerInstanceID        string
	HTTPClient              *http.Client
	WireLimits              harnesscontrol.Limits
	MaxUnackedFrames        int
	MaxReceiveHistoryFrames int
	MaxPendingApprovals     int
	HandshakeTimeout        time.Duration
	WriteTimeout            time.Duration
	ReconnectBackoff        time.Duration
	Now                     func() time.Time
}

func DefaultWorkerControlClientConfig(
	manifest runmanifest.Manifest,
	signed runmanifest.SignedManifest,
	controlCapability string,
	workerInstanceID string,
	httpClient *http.Client,
) WorkerControlClientConfig {
	return WorkerControlClientConfig{
		Manifest: manifest, SignedManifest: signed, ControlCapability: controlCapability,
		WorkerInstanceID: workerInstanceID, HTTPClient: httpClient,
		WireLimits: harnesscontrol.Limits{
			MaxFrameBytes: 1024 * 1024, MaxJSONValues: 65_536, MaxJSONDepth: 128,
		},
		MaxUnackedFrames: 1024, MaxReceiveHistoryFrames: 4096,
		MaxPendingApprovals: defaultWorkerPendingApprovalLimit,
		HandshakeTimeout:    10 * time.Second, WriteTimeout: 10 * time.Second,
		ReconnectBackoff: 100 * time.Millisecond, Now: time.Now,
	}
}

// WorkerControlClient is one worker process's bounded control endpoint.
// Lifecycle methods wait for the holder's cumulative ACK. Runtime methods
// pipeline frames through the bounded in-memory journal, and the terminal
// lifecycle method waits for a cumulative ACK that covers every earlier
// runtime fact. Transport ACKs do not themselves create core authority.
type WorkerControlClient struct {
	config     WorkerControlClientConfig
	httpClient *http.Client
	helloBase  harnesscontrol.Hello

	startMu sync.Mutex
	started bool
	ctx     context.Context
	cancel  context.CancelCauseFunc

	eventMu      sync.Mutex
	threadID     string
	turnID       string
	terminalSent bool
	terminalSeq  uint64

	sendMu           sync.Mutex
	mu               sync.Mutex
	poolInstanceID   string
	controlSessionID string
	session          *harnesscontrol.Session
	connection       *workerControlConnection
	stateEpoch       uint64
	stateChanged     chan struct{}
	terminalErr      error
	done             chan struct{}
	doneOnce         sync.Once
	commands         chan harnesscontrol.InterruptCommand

	approvalMu sync.Mutex
	approvals  map[string]*workerPendingApproval
}

type workerPendingApproval struct {
	request ElicitationRequest
	result  chan harnesscontrol.ApprovalOutcomeCommand
}

type workerControlConnection struct {
	writeMu sync.Mutex
	socket  *websocket.Conn
}

func NewWorkerControlClient(config WorkerControlClientConfig) (*WorkerControlClient, error) {
	httpClient, hello, err := validateWorkerControlClientConfig(config)
	if err != nil {
		return nil, err
	}
	config.SignedManifest.Manifest = append(json.RawMessage(nil), config.SignedManifest.Manifest...)
	return &WorkerControlClient{
		config: config, httpClient: httpClient, helloBase: hello,
		stateChanged: make(chan struct{}), done: make(chan struct{}),
		commands:  make(chan harnesscontrol.InterruptCommand, workerControlCommandBuffer),
		approvals: make(map[string]*workerPendingApproval),
	}, nil
}

// Start establishes the fresh authenticated WebSocket before returning and
// then owns exact, same-process resume in the background.
func (client *WorkerControlClient) Start(ctx context.Context) error {
	if client == nil {
		return errors.New("worker control client is required")
	}
	if ctx == nil {
		return errors.New("worker control start context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	client.startMu.Lock()
	defer client.startMu.Unlock()
	if client.started {
		return errors.New("worker control client is one-shot")
	}
	client.started = true
	client.ctx, client.cancel = context.WithCancelCause(ctx)
	initial := make(chan error, 1)
	go client.run(initial)
	select {
	case err := <-initial:
		return err
	case <-ctx.Done():
		client.cancel(context.Cause(ctx))
		return ctx.Err()
	}
}

func (client *WorkerControlClient) Interrupts() <-chan harnesscontrol.InterruptCommand {
	if client == nil {
		return nil
	}
	return client.commands
}

func (client *WorkerControlClient) SendThreadReady(ctx context.Context, threadID string, resumed bool) error {
	if client == nil {
		return errors.New("worker control client is required")
	}
	if ctx == nil {
		return errors.New("worker control event context is required")
	}
	client.eventMu.Lock()
	defer client.eventMu.Unlock()
	if client.threadID != "" || client.turnID != "" || client.terminalSent {
		return errors.New("thread_ready is out of order or repeated")
	}
	if resumed != (client.config.Manifest.PreviousCheckpoint != nil) {
		return errors.New("thread_ready resume mode does not match the signed manifest")
	}
	event := harnesscontrol.ThreadReadyEvent{
		Kind: harnesscontrol.EventKindThreadReady, ThreadID: threadID, Resumed: resumed,
	}
	if err := client.sendControlEvent(ctx, event, false, true); err != nil {
		return err
	}
	client.threadID = threadID
	return nil
}

func (client *WorkerControlClient) SendTurnAccepted(ctx context.Context, threadID, turnID string) error {
	if client == nil {
		return errors.New("worker control client is required")
	}
	if ctx == nil {
		return errors.New("worker control event context is required")
	}
	client.eventMu.Lock()
	defer client.eventMu.Unlock()
	if client.threadID == "" || threadID != client.threadID || client.turnID != "" || client.terminalSent {
		return errors.New("turn_accepted is out of order or changes the thread")
	}
	event := harnesscontrol.TurnAcceptedEvent{
		Kind: harnesscontrol.EventKindTurnAccepted, ThreadID: threadID, TurnID: turnID,
	}
	if err := client.sendControlEvent(ctx, event, false, true); err != nil {
		return err
	}
	client.turnID = turnID
	return nil
}

func (client *WorkerControlClient) SendTurnTerminal(ctx context.Context, event harnesscontrol.TurnTerminalEvent) error {
	if client == nil {
		return errors.New("worker control client is required")
	}
	if ctx == nil {
		return errors.New("worker control event context is required")
	}
	client.eventMu.Lock()
	defer client.eventMu.Unlock()
	if client.threadID == "" || client.turnID == "" || client.terminalSent ||
		event.ThreadID != client.threadID || event.TurnID != client.turnID {
		return errors.New("turn_terminal is out of order or changes the accepted turn")
	}
	if event.Kind == "" {
		event.Kind = harnesscontrol.EventKindTurnTerminal
	}
	if err := client.sendControlEvent(ctx, event, true, true); err != nil {
		return err
	}
	client.terminalSent = true
	return nil
}

// SendAppServerNotification journals one bounded stock app-server
// notification after turn acceptance. Runtime facts are pipelined: this
// method returns after the frame crosses the bounded control write boundary;
// the later terminal frame waits for a cumulative ACK covering every prior
// runtime frame.
func (client *WorkerControlClient) SendAppServerNotification(ctx context.Context, message codexwire.Message) error {
	if client == nil {
		return errors.New("worker control client is required")
	}
	if ctx == nil {
		return errors.New("worker control event context is required")
	}
	if message.Kind != codexwire.KindNotification || message.Method == "" || len(message.Params) == 0 {
		return errors.New("app-server runtime event must be a notification with params")
	}
	client.eventMu.Lock()
	defer client.eventMu.Unlock()
	if client.threadID == "" || client.turnID == "" || client.terminalSent {
		return errors.New("app-server notification is outside an accepted turn")
	}
	event := harnesscontrol.AppServerNotificationEvent{
		Kind: harnesscontrol.EventKindAppServerNotification, Method: message.Method,
		Params: append(json.RawMessage(nil), message.Params...),
	}
	return client.sendControlEvent(ctx, event, false, false)
}

// SendExecutorMCPProgress journals progress already correlated by MCPClient
// with the current run, generation, and dynamic call.
func (client *WorkerControlClient) SendExecutorMCPProgress(ctx context.Context, progress ProgressEvent) error {
	if client == nil {
		return errors.New("worker control client is required")
	}
	if ctx == nil {
		return errors.New("worker control event context is required")
	}
	if progress.RunID != client.config.Manifest.RunID ||
		progress.RunAttemptGeneration != client.config.Manifest.RunAttemptGeneration {
		return errors.New("executor MCP progress escaped the signed run attempt")
	}
	client.eventMu.Lock()
	defer client.eventMu.Unlock()
	if client.threadID == "" || client.turnID == "" || client.terminalSent {
		return errors.New("executor MCP progress is outside an accepted turn")
	}
	event := harnesscontrol.ExecutorMCPProgressEvent{
		Kind: harnesscontrol.EventKindExecutorMCPProgress, CallID: progress.CallID,
		Progress: progress.Progress, Total: progress.Total, Message: progress.Message,
	}
	return client.sendControlEvent(ctx, event, false, false)
}

// AwaitApproval projects gateway-owned elicitation metadata onto the control
// stream and waits only for the holder's canonical Core observation. The
// worker never decides an approval and never forwards model-authored schema or
// message content to the approval authority.
func (client *WorkerControlClient) AwaitApproval(
	ctx context.Context,
	request ElicitationRequest,
) (ElicitationDecision, error) {
	if client == nil {
		return ElicitationDecision{Action: ApprovalCancel}, nil
	}
	if ctx == nil {
		return ElicitationDecision{}, errors.New("worker approval context is required")
	}
	event := harnesscontrol.ApprovalRequestEvent{
		Kind:  harnesscontrol.EventKindApprovalRequest,
		RunID: request.RunID, CallID: request.CallID,
		RunAttemptGeneration: request.RunAttemptGeneration,
		ToolCatalogDigest:    request.ToolCatalogDigest,
		ExecutionID:          request.ExecutionID, ApprovalID: request.ApprovalID,
		Nonce: request.Nonce, ApprovalVersion: request.ApprovalVersion,
		ContextHash: request.ContextHash, ExpiresAt: request.ExpiresAt,
	}
	if err := event.Validate(); err != nil {
		return ElicitationDecision{}, err
	}
	if request.RunID != client.config.Manifest.RunID ||
		request.RunAttemptGeneration != client.config.Manifest.RunAttemptGeneration ||
		request.ToolCatalogDigest != client.config.Manifest.ExecutorMCP.CatalogDigest {
		return ElicitationDecision{}, errors.New("worker approval request escaped the signed run attempt")
	}
	now := client.config.Now()
	if !now.Before(request.ExpiresAt) {
		return ElicitationDecision{Action: ApprovalDecline}, nil
	}
	if request.ExpiresAt.After(now.Add(time.Duration(client.config.Manifest.Limits.MaxApprovalTTLMS) * time.Millisecond)) {
		return ElicitationDecision{}, errors.New("worker approval expiry exceeds the signed attempt limit")
	}

	waiter := &workerPendingApproval{
		request: request,
		result:  make(chan harnesscontrol.ApprovalOutcomeCommand, 1),
	}
	client.approvalMu.Lock()
	if len(client.approvals) >= client.config.MaxPendingApprovals {
		client.approvalMu.Unlock()
		return ElicitationDecision{}, errors.New("worker pending approval limit reached")
	}
	for _, pending := range client.approvals {
		if pending.request.ApprovalID == request.ApprovalID || pending.request.CallID == request.CallID {
			client.approvalMu.Unlock()
			return ElicitationDecision{}, errors.New("worker approval or call ID is already pending")
		}
	}
	client.approvals[request.ApprovalID] = waiter
	client.approvalMu.Unlock()

	client.eventMu.Lock()
	if client.threadID == "" || client.turnID == "" || client.terminalSent {
		client.eventMu.Unlock()
		client.removePendingApproval(waiter)
		return ElicitationDecision{}, errors.New("worker approval request is outside an accepted turn")
	}
	err := client.sendControlEvent(ctx, event, false, true)
	client.eventMu.Unlock()
	if err != nil {
		// Keep a possibly-journaled request correlated until either its outcome
		// arrives or the one-shot control session ends. This prevents a late,
		// canonical outcome from becoming an unknown command after caller expiry.
		return client.approvalCancellationDecision(ctx, request), nil
	}

	select {
	case outcome := <-waiter.result:
		return client.decisionFromApprovalOutcome(request, outcome)
	case <-client.done:
		return ElicitationDecision{Action: ApprovalCancel}, nil
	case <-ctx.Done():
		return client.approvalCancellationDecision(ctx, request), nil
	}
}

func (client *WorkerControlClient) deliverApprovalOutcome(outcome harnesscontrol.ApprovalOutcomeCommand) error {
	client.approvalMu.Lock()
	waiter := client.approvals[outcome.ApprovalID]
	if waiter == nil {
		client.approvalMu.Unlock()
		return &harnesscontrol.ProtocolError{
			Code: harnesscontrol.ErrorAttemptMismatch, Message: "approval outcome has no pending worker request", Terminal: true,
		}
	}
	if err := validateWorkerApprovalOutcome(waiter.request, client.config.Manifest, outcome); err != nil {
		client.approvalMu.Unlock()
		return err
	}
	delete(client.approvals, outcome.ApprovalID)
	client.approvalMu.Unlock()
	waiter.result <- outcome
	return nil
}

func validateWorkerApprovalOutcome(
	request ElicitationRequest,
	manifest runmanifest.Manifest,
	outcome harnesscontrol.ApprovalOutcomeCommand,
) error {
	if err := outcome.Validate(); err != nil {
		return err
	}
	if outcome.RunID != request.RunID || outcome.CallID != request.CallID ||
		outcome.RunAttemptGeneration != request.RunAttemptGeneration ||
		outcome.ToolCatalogDigest != request.ToolCatalogDigest ||
		outcome.ExecutionID != request.ExecutionID || outcome.ApprovalID != request.ApprovalID ||
		outcome.Nonce != request.Nonce || outcome.ContextHash != request.ContextHash ||
		outcome.ApprovalVersion <= request.ApprovalVersion ||
		outcome.RunID != manifest.RunID || outcome.RunAttemptGeneration != manifest.RunAttemptGeneration {
		return &harnesscontrol.ProtocolError{
			Code: harnesscontrol.ErrorAttemptMismatch, Message: "approval outcome does not match the pending worker request", Terminal: true,
		}
	}
	return nil
}

func (client *WorkerControlClient) decisionFromApprovalOutcome(
	request ElicitationRequest,
	outcome harnesscontrol.ApprovalOutcomeCommand,
) (ElicitationDecision, error) {
	switch outcome.Status {
	case "approved":
		return ElicitationDecision{
			Action: ApprovalAccept,
			Content: map[string]any{
				"approvalId": request.ApprovalID, "executionId": request.ExecutionID,
				"runId": request.RunID, "runAttemptId": client.config.Manifest.RunAttemptID,
				"runAttemptGeneration": request.RunAttemptGeneration,
				"nonce":                request.Nonce, "contextHash": request.ContextHash,
				"status": "approved", "approvalVersion": outcome.ApprovalVersion,
			},
		}, nil
	case "denied", "expired":
		return ElicitationDecision{Action: ApprovalDecline}, nil
	case "cancelled", "consumed":
		// consumed cannot authorize this still-pending elicitation; another
		// consumer already crossed Core's one-shot consume boundary.
		return ElicitationDecision{Action: ApprovalCancel}, nil
	default:
		return ElicitationDecision{}, fmt.Errorf("unsupported canonical approval outcome %q", outcome.Status)
	}
}

func (client *WorkerControlClient) approvalCancellationDecision(
	ctx context.Context,
	request ElicitationRequest,
) ElicitationDecision {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || !client.config.Now().Before(request.ExpiresAt) {
		return ElicitationDecision{Action: ApprovalDecline}
	}
	return ElicitationDecision{Action: ApprovalCancel}
}

func (client *WorkerControlClient) removePendingApproval(waiter *workerPendingApproval) {
	client.approvalMu.Lock()
	defer client.approvalMu.Unlock()
	if client.approvals[waiter.request.ApprovalID] == waiter {
		delete(client.approvals, waiter.request.ApprovalID)
	}
}

func (client *WorkerControlClient) Wait(ctx context.Context) error {
	if client == nil {
		return errors.New("worker control client is required")
	}
	if ctx == nil {
		return errors.New("worker control wait context is required")
	}
	select {
	case <-client.done:
		client.mu.Lock()
		defer client.mu.Unlock()
		return client.terminalErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (client *WorkerControlClient) Close(cause error) {
	if client == nil {
		return
	}
	client.startMu.Lock()
	cancel := client.cancel
	client.startMu.Unlock()
	if cancel != nil {
		if cause == nil {
			cause = context.Canceled
		}
		cancel(cause)
	}
	client.mu.Lock()
	connection := client.connection
	client.mu.Unlock()
	if connection != nil {
		connection.closeNow()
	}
}

func (client *WorkerControlClient) run(initial chan<- error) {
	connection, session, err := client.connectFresh(client.ctx)
	if err != nil {
		initial <- err
		client.finish(err)
		return
	}
	client.activate(session, connection)
	initial <- nil

	for {
		readErr := client.readConnection(client.ctx, connection)
		connection.closeNow()
		if client.terminalWasAcknowledged() {
			client.finish(nil)
			return
		}
		if err := client.ctx.Err(); err != nil {
			client.finish(context.Cause(client.ctx))
			return
		}
		if isTerminalWorkerControlError(readErr) {
			client.finish(readErr)
			return
		}
		disconnectedAt := client.config.Now()
		if err := client.detach(session, connection, disconnectedAt); err != nil {
			client.finish(errors.Join(readErr, err))
			return
		}
		connection, err = client.resumeUntil(session, disconnectedAt)
		if err != nil {
			client.finish(errors.Join(readErr, err))
			return
		}
		client.activate(session, connection)
		// A resume welcome can cumulatively ACK the terminal frame even when
		// the holder's original ACK bytes were lost. No further server message
		// is required in that case, so close the transport and finish locally.
		if client.terminalWasAcknowledged() {
			connection.closeNow()
			client.finish(nil)
			return
		}
	}
}

func (client *WorkerControlClient) connectFresh(ctx context.Context) (*workerControlConnection, *harnesscontrol.Session, error) {
	connection, err := client.dial(ctx)
	if err != nil {
		return nil, nil, err
	}
	fail := true
	defer func() {
		if fail {
			connection.closeNow()
		}
	}()
	hello := client.helloBase
	if err := connection.write(ctx, client.config.WriteTimeout, client.config.WireLimits, hello); err != nil {
		return nil, nil, fmt.Errorf("write fresh worker control hello: %w", err)
	}
	welcome, err := client.readWelcome(ctx, connection)
	if err != nil {
		return nil, nil, err
	}
	if welcome.ResumeStatus != "fresh" || welcome.PoolSentThrough != 0 || welcome.PoolReceivedThrough != 0 {
		return nil, nil, errors.New("fresh worker control handshake returned resume state")
	}
	binding, err := harnesscontrol.BindingFromHello(hello)
	if err != nil {
		return nil, nil, err
	}
	session, err := harnesscontrol.NewSession(harnesscontrol.SessionConfig{
		Role: harnesscontrol.RoleWorker, PoolInstanceID: welcome.PoolInstanceID,
		ControlSessionID: welcome.ControlSessionID, Attempt: binding,
		WireLimits: client.config.WireLimits, MaxUnackedFrames: client.config.MaxUnackedFrames,
		MaxJournalBytes:         int(client.config.Manifest.Limits.MaxControlBufferBytes),
		MaxReceiveHistoryFrames: client.config.MaxReceiveHistoryFrames,
		ResumeWindow:            time.Duration(harnesscontrol.ResumeWindowMillis) * time.Millisecond,
	})
	if err != nil {
		return nil, nil, err
	}
	client.mu.Lock()
	client.poolInstanceID = welcome.PoolInstanceID
	client.controlSessionID = welcome.ControlSessionID
	client.mu.Unlock()
	fail = false
	return connection, session, nil
}

func (client *WorkerControlClient) resumeUntil(session *harnesscontrol.Session, disconnectedAt time.Time) (*workerControlConnection, error) {
	deadline := disconnectedAt.Add(time.Duration(harnesscontrol.ResumeWindowMillis) * time.Millisecond)
	var lastErr error
	for {
		if err := client.ctx.Err(); err != nil {
			return nil, context.Cause(client.ctx)
		}
		now := client.config.Now()
		if !now.Before(deadline) {
			return nil, errors.Join(
				lastErr,
				&harnesscontrol.ProtocolError{
					Code:    harnesscontrol.ErrorResumeExpired,
					Message: "worker could not resume on the original holder within 30 seconds", Terminal: true,
				},
			)
		}
		connection, err := client.connectResume(client.ctx, session)
		if err == nil {
			return connection, nil
		}
		if isTerminalWorkerControlError(err) {
			return nil, err
		}
		lastErr = err
		remaining := deadline.Sub(client.config.Now())
		pause := min(client.config.ReconnectBackoff, remaining)
		if pause <= 0 {
			continue
		}
		timer := time.NewTimer(pause)
		select {
		case <-client.ctx.Done():
			timer.Stop()
			return nil, context.Cause(client.ctx)
		case <-timer.C:
		}
	}
}

func (client *WorkerControlClient) connectResume(ctx context.Context, session *harnesscontrol.Session) (*workerControlConnection, error) {
	snapshot := session.Snapshot()
	client.mu.Lock()
	poolInstanceID := client.poolInstanceID
	controlSessionID := client.controlSessionID
	client.mu.Unlock()
	hello := client.helloBase
	hello.Resume = &harnesscontrol.ResumeCursor{
		PoolInstanceID: poolInstanceID, ControlSessionID: controlSessionID,
		RunAttemptGeneration: client.config.Manifest.RunAttemptGeneration,
		WorkerSentThrough:    snapshot.SentThrough, WorkerReceivedThrough: snapshot.ReceivedThrough,
	}
	connection, err := client.dial(ctx)
	if err != nil {
		return nil, err
	}
	fail := true
	defer func() {
		if fail {
			connection.closeNow()
		}
	}()
	resumedSession := false
	defer func() {
		if fail && resumedSession && session.Snapshot().State == harnesscontrol.SessionActive {
			_ = session.Disconnect(client.config.Now())
		}
	}()
	if err := connection.write(ctx, client.config.WriteTimeout, client.config.WireLimits, hello); err != nil {
		return nil, fmt.Errorf("write resume worker control hello: %w", err)
	}
	welcome, err := client.readWelcome(ctx, connection)
	if err != nil {
		return nil, err
	}
	if welcome.ResumeStatus != "resumed" || welcome.PoolInstanceID != hello.Resume.PoolInstanceID ||
		welcome.ControlSessionID != hello.Resume.ControlSessionID {
		return nil, &harnesscontrol.ProtocolError{
			Code: harnesscontrol.ErrorResumeRejected, Message: "holder did not resume the requested control session", Terminal: true,
		}
	}
	resumed, err := session.Resume(harnesscontrol.ResumeRequest{
		PoolInstanceID: welcome.PoolInstanceID, ControlSessionID: welcome.ControlSessionID,
		RunAttemptGeneration: welcome.RunAttemptGeneration,
		PeerSentThrough:      welcome.PoolSentThrough, PeerReceivedThrough: welcome.PoolReceivedThrough,
	}, client.config.Now())
	if err != nil {
		return nil, err
	}
	resumedSession = true
	// Drain the holder's replay first. If both journals are large, making both
	// peers write all replays before either reads can deadlock on socket buffers.
	for session.Snapshot().ReceivedThrough < welcome.PoolSentThrough {
		if err := client.readOne(ctx, session, connection, false); err != nil {
			return nil, err
		}
	}
	if session.Snapshot().ReceivedThrough > snapshot.ReceivedThrough {
		ack, err := session.AckFrame()
		if err != nil {
			return nil, err
		}
		if err := connection.write(ctx, client.config.WriteTimeout, client.config.WireLimits, ack); err != nil {
			return nil, err
		}
	}
	for _, frame := range resumed.Replay {
		if err := connection.write(ctx, client.config.WriteTimeout, client.config.WireLimits, frame); err != nil {
			return nil, err
		}
	}
	fail = false
	return connection, nil
}

func (client *WorkerControlClient) dial(ctx context.Context) (*workerControlConnection, error) {
	endpoint, err := url.Parse(client.config.Manifest.ControllerCallback.Endpoint)
	if err != nil {
		return nil, err
	}
	endpoint.Scheme = "wss"
	header := make(http.Header)
	header.Set("Authorization", "Bearer "+client.config.ControlCapability)
	handshakeContext, cancel := context.WithTimeout(ctx, client.config.HandshakeTimeout)
	defer cancel()
	socket, response, err := websocket.Dial(handshakeContext, endpoint.String(), &websocket.DialOptions{
		HTTPClient: client.httpClient, HTTPHeader: header, CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		if response != nil {
			return nil, fmt.Errorf("dial worker control WebSocket: HTTP %d: %w", response.StatusCode, err)
		}
		return nil, fmt.Errorf("dial worker control WebSocket: %w", err)
	}
	socket.SetReadLimit(int64(client.config.WireLimits.MaxFrameBytes))
	return &workerControlConnection{socket: socket}, nil
}

func (client *WorkerControlClient) readWelcome(ctx context.Context, connection *workerControlConnection) (harnesscontrol.Welcome, error) {
	handshakeContext, cancel := context.WithTimeout(ctx, client.config.HandshakeTimeout)
	defer cancel()
	messageType, raw, err := connection.socket.Read(handshakeContext)
	if err != nil {
		return harnesscontrol.Welcome{}, fmt.Errorf("read worker control welcome: %w", err)
	}
	if messageType != websocket.MessageText {
		return harnesscontrol.Welcome{}, errors.New("worker control welcome must be text")
	}
	message, err := harnesscontrol.Decode(raw, client.config.WireLimits)
	if err != nil {
		return harnesscontrol.Welcome{}, err
	}
	if message.SessionError != nil {
		return harnesscontrol.Welcome{}, protocolErrorFromSessionError(*message.SessionError)
	}
	if message.Welcome == nil {
		return harnesscontrol.Welcome{}, errors.New("holder did not return worker control welcome")
	}
	welcome := *message.Welcome
	if welcome.ProtocolVersion != harnesscontrol.CurrentProtocolVersion ||
		welcome.RunAttemptGeneration != client.config.Manifest.RunAttemptGeneration ||
		welcome.ResumeWindowMillis != harnesscontrol.ResumeWindowMillis {
		return harnesscontrol.Welcome{}, errors.New("worker control welcome does not match the attempt protocol")
	}
	return welcome, nil
}

func (client *WorkerControlClient) readConnection(ctx context.Context, connection *workerControlConnection) error {
	client.mu.Lock()
	session := client.session
	client.mu.Unlock()
	for {
		if err := client.readOne(ctx, session, connection, true); err != nil {
			return err
		}
	}
}

func (client *WorkerControlClient) readOne(
	ctx context.Context,
	session *harnesscontrol.Session,
	connection *workerControlConnection,
	writeAck bool,
) error {
	messageType, raw, err := connection.socket.Read(ctx)
	if err != nil {
		return err
	}
	if messageType != websocket.MessageText {
		return &harnesscontrol.ProtocolError{
			Code: harnesscontrol.ErrorMalformedFrame, Message: "worker control message must be text", Terminal: true,
		}
	}
	message, err := harnesscontrol.Decode(raw, client.config.WireLimits)
	if err != nil {
		return err
	}
	switch {
	case message.Ack != nil:
		if err := session.ReceiveAck(*message.Ack); err != nil {
			return err
		}
		client.notifyStateChange()
		return nil
	case message.Frame != nil:
		received, err := session.Receive(*message.Frame)
		if err != nil {
			return err
		}
		// A sequenced command can cumulatively ACK worker lifecycle frames, so
		// wake event waiters even when the holder sent no standalone ACK.
		client.notifyStateChange()
		if received.Deliver {
			command, err := harnesscontrol.DecodeCommandPayload(message.Frame.Payload, client.config.WireLimits)
			if err != nil {
				return err
			}
			switch {
			case command.Interrupt != nil:
				select {
				case client.commands <- *command.Interrupt:
				default:
					return &harnesscontrol.ProtocolError{
						Code:    harnesscontrol.ErrorBufferOverflow,
						Message: "worker interrupt command buffer is full", Terminal: true,
					}
				}
			case command.ApprovalOutcome != nil:
				if err := client.deliverApprovalOutcome(*command.ApprovalOutcome); err != nil {
					return err
				}
			default:
				return errors.New("worker control command omitted its payload")
			}
		}
		if writeAck {
			ack, err := session.AckFrame()
			if err != nil {
				return err
			}
			if err := connection.write(ctx, client.config.WriteTimeout, client.config.WireLimits, ack); err != nil {
				return err
			}
		}
		return nil
	case message.SessionError != nil:
		return protocolErrorFromSessionError(*message.SessionError)
	default:
		return &harnesscontrol.ProtocolError{
			Code:    harnesscontrol.ErrorMalformedFrame,
			Message: "welcome or hello is invalid after worker control handshake", Terminal: true,
		}
	}
}

func (client *WorkerControlClient) sendControlEvent(ctx context.Context, event any, terminal, waitForAuthority bool) error {
	if client == nil {
		return errors.New("worker control client is required")
	}
	if ctx == nil {
		return errors.New("worker control event context is required")
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err := harnesscontrol.DecodeEventPayload(payload, client.config.WireLimits); err != nil {
		return err
	}
	client.sendMu.Lock()
	defer client.sendMu.Unlock()
	var (
		frame      harnesscontrol.Frame
		connection *workerControlConnection
	)
	for {
		session, candidate, err := client.waitConnected(ctx)
		if err != nil {
			return err
		}

		// Bind sequence allocation to the currently attached connection. If
		// detach wins first, retry after resume without allocating a frame. If
		// allocation wins first, detach retains this exact frame for replay.
		client.mu.Lock()
		if client.session != session || client.connection != candidate {
			client.mu.Unlock()
			continue
		}
		frame, err = session.Send(harnesscontrol.Payload{
			Type: harnesscontrol.MessageTypeEvent, Payload: payload,
		})
		if err == nil && terminal {
			// Publish the terminal sequence in the same critical section as
			// allocation. The reader may detach and complete a cursor-based
			// resume as soon as this lock is released.
			client.terminalSeq = frame.SessionSeq
		}
		client.mu.Unlock()
		if err == nil {
			connection = candidate
			break
		}
		if session.Snapshot().State == harnesscontrol.SessionDisconnected {
			continue
		}
		return err
	}
	if err := connection.write(ctx, client.config.WriteTimeout, client.config.WireLimits, frame); err != nil {
		connection.closeNow()
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	if !waitForAuthority {
		return nil
	}
	return client.waitForAck(ctx, frame.SessionSeq)
}

func (client *WorkerControlClient) waitConnected(ctx context.Context) (*harnesscontrol.Session, *workerControlConnection, error) {
	for {
		client.mu.Lock()
		if client.terminalErr != nil {
			err := client.terminalErr
			client.mu.Unlock()
			return nil, nil, err
		}
		if client.connection != nil && client.session != nil {
			session, connection := client.session, client.connection
			client.mu.Unlock()
			return session, connection, nil
		}
		changed := client.stateChanged
		client.mu.Unlock()
		select {
		case <-changed:
		case <-client.done:
			return nil, nil, client.finishedError()
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}
}

func (client *WorkerControlClient) waitForAck(ctx context.Context, sequence uint64) error {
	for {
		client.mu.Lock()
		session := client.session
		if session != nil && session.Snapshot().PeerAck >= sequence {
			client.mu.Unlock()
			return nil
		}
		if client.terminalErr != nil {
			err := client.terminalErr
			client.mu.Unlock()
			return err
		}
		changed := client.stateChanged
		client.mu.Unlock()
		select {
		case <-changed:
		case <-client.done:
			if client.terminalWasAcknowledged() && client.terminalSequence() == sequence {
				return nil
			}
			return client.finishedError()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (client *WorkerControlClient) activate(session *harnesscontrol.Session, connection *workerControlConnection) {
	client.mu.Lock()
	if client.session == nil {
		client.session = session
	}
	client.connection = connection
	client.signalStateChangedLocked()
	client.mu.Unlock()
}

func (client *WorkerControlClient) detach(
	session *harnesscontrol.Session,
	connection *workerControlConnection,
	now time.Time,
) error {
	client.mu.Lock()
	if client.connection == connection {
		client.connection = nil
		client.signalStateChangedLocked()
	}
	client.mu.Unlock()
	return session.Disconnect(now)
}

func (client *WorkerControlClient) notifyStateChange() {
	client.mu.Lock()
	client.signalStateChangedLocked()
	client.mu.Unlock()
}

func (client *WorkerControlClient) signalStateChangedLocked() {
	close(client.stateChanged)
	client.stateChanged = make(chan struct{})
	client.stateEpoch++
}

func (client *WorkerControlClient) terminalWasAcknowledged() bool {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.terminalSeq > 0 && client.session != nil &&
		client.session.Snapshot().PeerAck >= client.terminalSeq
}

func (client *WorkerControlClient) terminalSequence() uint64 {
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.terminalSeq
}

func (client *WorkerControlClient) finish(err error) {
	client.doneOnce.Do(func() {
		client.mu.Lock()
		client.terminalErr = err
		connection := client.connection
		client.connection = nil
		client.signalStateChangedLocked()
		client.mu.Unlock()
		if connection != nil {
			connection.closeNow()
		}
		close(client.commands)
		close(client.done)
	})
}

func (client *WorkerControlClient) finishedError() error {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.terminalErr == nil {
		return errors.New("worker control session ended")
	}
	return client.terminalErr
}

func (connection *workerControlConnection) write(
	ctx context.Context,
	timeout time.Duration,
	limits harnesscontrol.Limits,
	value any,
) error {
	raw, err := harnesscontrol.Encode(value, limits)
	if err != nil {
		return err
	}
	writeContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	connection.writeMu.Lock()
	defer connection.writeMu.Unlock()
	return connection.socket.Write(writeContext, websocket.MessageText, raw)
}

func (connection *workerControlConnection) closeNow() {
	connection.writeMu.Lock()
	defer connection.writeMu.Unlock()
	_ = connection.socket.CloseNow()
}

func validateWorkerControlClientConfig(
	config WorkerControlClientConfig,
) (*http.Client, harnesscontrol.Hello, error) {
	if err := config.Manifest.Validate(); err != nil {
		return nil, harnesscontrol.Hello{}, err
	}
	canonical, err := runmanifest.CanonicalBytes(config.Manifest)
	if err != nil {
		return nil, harnesscontrol.Hello{}, err
	}
	if !bytes.Equal(canonical, config.SignedManifest.Manifest) || config.SignedManifest.Algorithm != runmanifest.SignatureAlgorithm {
		return nil, harnesscontrol.Hello{}, errors.New("worker control signed manifest does not match the verified manifest")
	}
	manifestDigest, err := runmanifest.Digest(canonical)
	if err != nil {
		return nil, harnesscontrol.Hello{}, err
	}
	if err := harnessbootstrap.ValidateControlCapability(config.ControlCapability); err != nil {
		return nil, harnesscontrol.Hello{}, err
	}
	if config.HTTPClient == nil {
		return nil, harnesscontrol.Hello{}, errors.New("worker control HTTP client is required")
	}
	httpClient, err := secureWorkerControlHTTPClient(config.HTTPClient, config.Manifest.ControllerCallback.TLSIdentity)
	if err != nil {
		return nil, harnesscontrol.Hello{}, err
	}
	if config.WireLimits.MaxFrameBytes < 1 || config.WireLimits.MaxJSONValues < 1 || config.WireLimits.MaxJSONDepth < 1 {
		return nil, harnesscontrol.Hello{}, errors.New("worker control wire limits must be positive")
	}
	if config.MaxUnackedFrames < 1 || config.MaxReceiveHistoryFrames < 1 {
		return nil, harnesscontrol.Hello{}, errors.New("worker control journal limits must be positive")
	}
	if config.MaxPendingApprovals < 1 || config.MaxPendingApprovals > maximumWorkerPendingApprovals {
		return nil, harnesscontrol.Hello{}, fmt.Errorf(
			"worker pending approval limit must be between 1 and %d", maximumWorkerPendingApprovals,
		)
	}
	for field, duration := range map[string]time.Duration{
		"handshake timeout": config.HandshakeTimeout, "write timeout": config.WriteTimeout,
		"reconnect backoff": config.ReconnectBackoff,
	} {
		if duration < time.Millisecond || duration > time.Minute {
			return nil, harnesscontrol.Hello{}, fmt.Errorf("worker control %s must be between 1ms and 1m", field)
		}
	}
	if config.Now == nil {
		return nil, harnesscontrol.Hello{}, errors.New("worker control clock is required")
	}
	hello := harnesscontrol.Hello{
		Type: harnesscontrol.MessageTypeHello, ProtocolVersions: []string{harnesscontrol.CurrentProtocolVersion},
		WorkerInstanceID: config.WorkerInstanceID,
		WorkspaceID:      config.Manifest.WorkspaceID, SessionID: config.Manifest.SessionID,
		RunID: config.Manifest.RunID, RunAttemptID: config.Manifest.RunAttemptID,
		RunAttemptGeneration: config.Manifest.RunAttemptGeneration,
		HolderID:             config.Manifest.HolderID, ManifestDigest: manifestDigest,
	}
	if err := hello.Validate(); err != nil {
		return nil, harnesscontrol.Hello{}, err
	}
	return httpClient, hello, nil
}

func secureWorkerControlHTTPClient(source *http.Client, expectedIdentity string) (*http.Client, error) {
	parsedIdentity, err := url.Parse(expectedIdentity)
	if err != nil || parsedIdentity.Scheme != "spiffe" || parsedIdentity.Host == "" || parsedIdentity.Path == "" {
		return nil, errors.New("worker control server TLS identity must be a SPIFFE URI")
	}
	transport, ok := source.Transport.(*http.Transport)
	if !ok || transport == nil {
		return nil, errors.New("worker control HTTP client must use an explicit *http.Transport")
	}
	transport = transport.Clone()
	tlsConfig := transport.TLSClientConfig
	if tlsConfig == nil {
		tlsConfig = &tls.Config{}
	} else {
		tlsConfig = tlsConfig.Clone()
	}
	if tlsConfig.InsecureSkipVerify {
		return nil, errors.New("worker control TLS verification cannot be disabled")
	}
	if tlsConfig.MinVersion < tls.VersionTLS13 {
		tlsConfig.MinVersion = tls.VersionTLS13
	}
	priorVerify := tlsConfig.VerifyConnection
	tlsConfig.VerifyConnection = func(state tls.ConnectionState) error {
		if priorVerify != nil {
			if err := priorVerify(state); err != nil {
				return err
			}
		}
		if len(state.PeerCertificates) == 0 || len(state.PeerCertificates[0].URIs) != 1 ||
			state.PeerCertificates[0].URIs[0].String() != expectedIdentity {
			return errors.New("worker control server certificate has the wrong SPIFFE URI SAN")
		}
		return nil
	}
	transport.TLSClientConfig = tlsConfig
	client := *source
	client.Transport = transport
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("worker control redirects are forbidden")
	}
	return &client, nil
}

func protocolErrorFromSessionError(value harnesscontrol.SessionError) error {
	return &harnesscontrol.ProtocolError{
		Code: value.Code, Message: "holder terminated the worker control session",
		Terminal: value.Terminal, LostFrom: value.LostFrom, LostTo: value.LostTo,
	}
}

func isTerminalWorkerControlError(err error) bool {
	var protocol *harnesscontrol.ProtocolError
	return errors.As(err, &protocol) && protocol.Terminal
}
