package harnesspool

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"time"

	"github.com/agentserver/agentserver/v2/internal/harnesscontrol"
	"github.com/agentserver/agentserver/v2/internal/runmanifest"
	"nhooyr.io/websocket"
)

const HarnessControlPath = "/internal/v2/harness/control"

const maximumControlOutstandingApprovals = 256

var (
	errControlServerShuttingDown = errors.New("harness control server is shutting down")
	ErrControlWriteAmbiguous     = errors.New("control command was journaled but its transport write is ambiguous")
)

type ControlServerConfig struct {
	PoolInstanceID          string
	HolderID                string
	CallbackEndpoint        string
	CallbackTLSIdentity     string
	CallbackAudience        string
	WorkerServiceAccount    string
	WorkerTLSIdentity       string
	WireLimits              harnesscontrol.Limits
	MaxUnackedFrames        int
	MaxReceiveHistoryFrames int
	MaxOutstandingApprovals int
	HandshakeTimeout        time.Duration
	WriteTimeout            time.Duration
	IDGenerator             IDGenerator
	CapabilityGenerator     ControlCapabilityGenerator
	Now                     func() time.Time
}

func DefaultControlServerConfig(
	poolInstanceID, holderID, callbackEndpoint, callbackTLSIdentity, callbackAudience,
	workerServiceAccount, workerTLSIdentity string,
) ControlServerConfig {
	return ControlServerConfig{
		PoolInstanceID: poolInstanceID, HolderID: holderID,
		CallbackEndpoint: callbackEndpoint, CallbackTLSIdentity: callbackTLSIdentity,
		CallbackAudience: callbackAudience, WorkerServiceAccount: workerServiceAccount,
		WorkerTLSIdentity: workerTLSIdentity,
		WireLimits: harnesscontrol.Limits{
			MaxFrameBytes: 1024 * 1024, MaxJSONValues: 65_536, MaxJSONDepth: 128,
		},
		MaxUnackedFrames: 1024, MaxReceiveHistoryFrames: 4096, MaxOutstandingApprovals: 64,
		HandshakeTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second,
		IDGenerator: newRandomUUID, CapabilityGenerator: newControlCapability, Now: time.Now,
	}
}

type controlExpectedAttempt struct {
	WorkspaceID          string
	SessionID            string
	RunID                string
	RunAttemptID         string
	RunAttemptGeneration int64
	HolderID             string
	ManifestDigest       string
	ToolCatalogDigest    string
	Resumed              bool
	MaxJournalBytes      int
	MaxApprovalTTL       time.Duration
}

type controlConnection struct {
	writeMu sync.Mutex
	socket  *websocket.Conn
}

func (connection *controlConnection) write(ctx context.Context, timeout time.Duration, limits harnesscontrol.Limits, value any) error {
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

func (connection *controlConnection) close(status websocket.StatusCode, reason string) {
	connection.writeMu.Lock()
	defer connection.writeMu.Unlock()
	_ = connection.socket.Close(status, reason)
}

func (connection *controlConnection) closeNow() {
	connection.writeMu.Lock()
	defer connection.writeMu.Unlock()
	_ = connection.socket.CloseNow()
}

type controlOutcome struct {
	terminal *harnesscontrol.TurnTerminalEvent
	err      error
}

type outstandingControlApproval struct {
	request harnesscontrol.ApprovalRequestEvent
	cancel  context.CancelFunc
}

type attemptControlRuntime struct {
	server       *ControlServer
	expected     controlExpectedAttempt
	lifecycle    AttemptLifecycle
	capabilityID [sha256.Size]byte
	controlID    string

	mu                sync.Mutex
	eventMu           sync.Mutex
	commandMu         sync.Mutex
	ackBarrier        sync.Mutex
	session           *harnesscontrol.Session
	workerID          string
	connection        *controlConnection
	connectionReady   bool
	epoch             uint64
	resumeTimer       *time.Timer
	closed            bool
	ready             chan struct{}
	readyOnce         sync.Once
	readyErr          error
	done              chan struct{}
	doneOnce          sync.Once
	outcome           controlOutcome
	connectionChanged chan struct{}

	approvalCtx    context.Context
	approvalCancel context.CancelCauseFunc
	approvalMu     sync.Mutex
	approvals      map[string]*outstandingControlApproval
	approvalIDs    map[string]struct{}
	approvalCalls  map[string]struct{}

	threadID     string
	turnID       string
	turnAccepted bool
	terminalSeen bool
}

// AttemptControl is the registration handed to a workload launcher. The
// plaintext capability exists only in this handle; the server registry keeps
// its SHA-256 digest.
type AttemptControl struct {
	runtime    *attemptControlRuntime
	capability string
}

func (control *AttemptControl) Capability() string {
	if control == nil {
		return ""
	}
	return control.capability
}

func (control *AttemptControl) ControlSessionID() string {
	if control == nil || control.runtime == nil {
		return ""
	}
	return control.runtime.controlID
}

func (control *AttemptControl) WaitConnected(ctx context.Context) error {
	if control == nil || control.runtime == nil {
		return errors.New("attempt control registration is required")
	}
	if ctx == nil {
		return errors.New("wait context is required")
	}
	select {
	case <-control.runtime.ready:
		control.runtime.mu.Lock()
		err := control.runtime.readyErr
		control.runtime.mu.Unlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (control *AttemptControl) WaitTerminal(ctx context.Context) (harnesscontrol.TurnTerminalEvent, error) {
	if control == nil || control.runtime == nil {
		return harnesscontrol.TurnTerminalEvent{}, errors.New("attempt control registration is required")
	}
	if ctx == nil {
		return harnesscontrol.TurnTerminalEvent{}, errors.New("wait context is required")
	}
	select {
	case <-control.runtime.done:
		control.runtime.mu.Lock()
		outcome := control.runtime.outcome
		control.runtime.mu.Unlock()
		if outcome.terminal == nil {
			return harnesscontrol.TurnTerminalEvent{}, outcome.err
		}
		return *outcome.terminal, outcome.err
	case <-ctx.Done():
		return harnesscontrol.TurnTerminalEvent{}, ctx.Err()
	}
}

func (control *AttemptControl) SendInterrupt(ctx context.Context, command harnesscontrol.InterruptCommand) error {
	if control == nil || control.runtime == nil {
		return errors.New("attempt control registration is required")
	}
	if ctx == nil {
		return errors.New("interrupt context is required")
	}
	if err := command.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(command)
	if err != nil {
		return err
	}
	runtime := control.runtime
	runtime.ackBarrier.Lock()
	defer runtime.ackBarrier.Unlock()
	runtime.commandMu.Lock()
	defer runtime.commandMu.Unlock()
	runtime.mu.Lock()
	if runtime.closed || runtime.session == nil || runtime.connection == nil || !runtime.connectionReady {
		runtime.mu.Unlock()
		return errors.New("attempt worker control connection is not active")
	}
	session := runtime.session
	connection := runtime.connection
	runtime.mu.Unlock()
	frame, err := session.Send(harnesscontrol.Payload{Type: harnesscontrol.MessageTypeCommand, Payload: payload})
	if err != nil {
		return err
	}
	if err := connection.write(ctx, runtime.server.config.WriteTimeout, runtime.server.config.WireLimits, frame); err != nil {
		return errors.Join(ErrControlWriteAmbiguous, err)
	}
	return nil
}

func (control *AttemptControl) Snapshot() (harnesscontrol.SessionSnapshot, bool) {
	if control == nil || control.runtime == nil {
		return harnesscontrol.SessionSnapshot{}, false
	}
	control.runtime.mu.Lock()
	session := control.runtime.session
	control.runtime.mu.Unlock()
	if session == nil {
		return harnesscontrol.SessionSnapshot{}, false
	}
	return session.Snapshot(), true
}

func (control *AttemptControl) Close(cause error) {
	if control == nil || control.runtime == nil {
		return
	}
	control.runtime.server.unregister(control.runtime, cause)
}

type ControlServer struct {
	config ControlServerConfig
	path   string

	mu           sync.Mutex
	shuttingDown bool
	byCapability map[[sha256.Size]byte]*attemptControlRuntime
	byAttempt    map[string]*attemptControlRuntime
}

func NewControlServer(config ControlServerConfig) (*ControlServer, error) {
	path, err := validateControlServerConfig(config)
	if err != nil {
		return nil, err
	}
	return &ControlServer{
		config: config, path: path,
		byCapability: make(map[[sha256.Size]byte]*attemptControlRuntime),
		byAttempt:    make(map[string]*attemptControlRuntime),
	}, nil
}

func (server *ControlServer) RegisterAttempt(prepared PreparedRunLaunch, lifecycle AttemptLifecycle) (*AttemptControl, error) {
	if server == nil {
		return nil, errors.New("harness control server is required")
	}
	if lifecycle == nil {
		return nil, errors.New("attempt lifecycle authority is required")
	}
	if err := validatePreparedSupervisionInput(prepared.Scheduled, prepared); err != nil {
		return nil, fmt.Errorf("validate control attempt: %w", err)
	}
	manifest := prepared.Manifest
	if manifest.HolderID != server.config.HolderID ||
		manifest.ControllerCallback.Endpoint != server.config.CallbackEndpoint ||
		manifest.ControllerCallback.TLSIdentity != server.config.CallbackTLSIdentity ||
		manifest.ControllerCallback.Audience != server.config.CallbackAudience ||
		manifest.ExpectedServiceAccount != server.config.WorkerServiceAccount {
		return nil, errors.New("prepared launch does not match this control holder deployment profile")
	}
	if manifest.Limits.MaxControlBufferBytes < int64(server.config.WireLimits.MaxFrameBytes) {
		return nil, errors.New("run maxControlBufferBytes cannot hold one maximum control frame")
	}
	manifestDigest, err := runmanifest.Digest(prepared.SignedManifest.Manifest)
	if err != nil {
		return nil, fmt.Errorf("digest prepared run manifest: %w", err)
	}
	controlID, err := server.config.IDGenerator()
	if err != nil {
		return nil, fmt.Errorf("allocate control session ID: %w", err)
	}
	if err := validateUUIDIdentity("control session ID", controlID); err != nil {
		return nil, err
	}

	server.mu.Lock()
	defer server.mu.Unlock()
	if server.shuttingDown {
		return nil, errControlServerShuttingDown
	}
	if _, duplicate := server.byAttempt[manifest.RunAttemptID]; duplicate {
		return nil, errors.New("run attempt already has a control registration in this pool process")
	}
	var capability string
	var capabilityID [sha256.Size]byte
	for tries := 0; tries < 4; tries++ {
		capability, err = server.config.CapabilityGenerator()
		if err != nil {
			return nil, fmt.Errorf("allocate attempt control capability: %w", err)
		}
		if err := validateControlCapability(capability); err != nil {
			return nil, err
		}
		capabilityID = controlCapabilityDigest(capability)
		if _, collision := server.byCapability[capabilityID]; !collision {
			break
		}
		capability = ""
	}
	if capability == "" {
		return nil, errors.New("could not allocate a distinct attempt control capability")
	}
	approvalCtx, cancelApprovals := context.WithCancelCause(context.Background())
	runtime := &attemptControlRuntime{
		server: server,
		expected: controlExpectedAttempt{
			WorkspaceID: manifest.WorkspaceID, SessionID: manifest.SessionID, RunID: manifest.RunID,
			RunAttemptID: manifest.RunAttemptID, RunAttemptGeneration: manifest.RunAttemptGeneration,
			HolderID: manifest.HolderID, ManifestDigest: manifestDigest,
			ToolCatalogDigest: manifest.ExecutorMCP.CatalogDigest,
			Resumed:           manifest.PreviousCheckpoint != nil, MaxJournalBytes: int(manifest.Limits.MaxControlBufferBytes),
			MaxApprovalTTL: time.Duration(manifest.Limits.MaxApprovalTTLMS) * time.Millisecond,
		},
		lifecycle: lifecycle, capabilityID: capabilityID, controlID: controlID,
		ready: make(chan struct{}), done: make(chan struct{}), connectionChanged: make(chan struct{}),
		approvalCtx: approvalCtx, approvalCancel: cancelApprovals,
		approvals:   make(map[string]*outstandingControlApproval),
		approvalIDs: make(map[string]struct{}), approvalCalls: make(map[string]struct{}),
	}
	server.byCapability[capabilityID] = runtime
	server.byAttempt[manifest.RunAttemptID] = runtime
	return &AttemptControl{runtime: runtime, capability: capability}, nil
}

func (server *ControlServer) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet || request.URL.Path != server.path {
		http.NotFound(response, request)
		return
	}
	if err := authenticateHarnessWorker(request, server.config.WorkerTLSIdentity); err != nil {
		http.Error(response, "harness worker authentication failed", http.StatusUnauthorized)
		return
	}
	capability, err := bearerCapability(request)
	if err != nil {
		http.Error(response, "harness worker authentication failed", http.StatusUnauthorized)
		return
	}
	runtime := server.runtimeForCapability(controlCapabilityDigest(capability))
	if runtime == nil {
		http.Error(response, "harness worker authentication failed", http.StatusUnauthorized)
		return
	}

	socket, err := websocket.Accept(response, request, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	socket.SetReadLimit(int64(server.config.WireLimits.MaxFrameBytes))
	connection := &controlConnection{socket: socket}

	fresh, replay, epoch, err := runtime.handshake(request.Context(), connection)
	if err != nil {
		server.writeSessionFailure(request.Context(), connection, err)
		connection.close(websocket.StatusPolicyViolation, "harness control handshake rejected")
		return
	}
	detached := false
	defer func() {
		if !detached {
			runtime.detach(epoch, server.config.Now())
		}
	}()
	welcome := harnesscontrol.Welcome{
		Type: harnesscontrol.MessageTypeWelcome, ProtocolVersion: harnesscontrol.CurrentProtocolVersion,
		PoolInstanceID: server.config.PoolInstanceID, ControlSessionID: runtime.controlID,
		RunAttemptGeneration: runtime.expected.RunAttemptGeneration,
		ResumeStatus:         "resumed", ResumeWindowMillis: harnesscontrol.ResumeWindowMillis,
		PoolSentThrough: replay.SentThrough, PoolReceivedThrough: replay.ReceivedThrough,
	}
	if fresh {
		welcome.ResumeStatus = "fresh"
	}
	if err := connection.write(request.Context(), server.config.WriteTimeout, server.config.WireLimits, welcome); err != nil {
		runtime.detach(epoch, server.config.Now())
		detached = true
		return
	}
	for _, frame := range replay.Replay {
		if err := connection.write(request.Context(), server.config.WriteTimeout, server.config.WireLimits, frame); err != nil {
			runtime.detach(epoch, server.config.Now())
			detached = true
			return
		}
	}
	runtime.activateConnection(epoch)

	terminal, err := server.runConnection(request.Context(), runtime, connection)
	if terminal {
		if err != nil {
			server.writeSessionFailure(request.Context(), connection, err)
			runtime.fail(err)
			connection.close(websocket.StatusPolicyViolation, "harness control session terminated")
		} else {
			connection.close(websocket.StatusNormalClosure, "turn terminal received")
		}
		runtime.closeSession(err)
		runtime.detach(epoch, server.config.Now())
		detached = true
		return
	}
}

func (server *ControlServer) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return errors.New("control server shutdown context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	server.mu.Lock()
	server.shuttingDown = true
	runtimes := make([]*attemptControlRuntime, 0, len(server.byAttempt))
	for _, runtime := range server.byAttempt {
		runtimes = append(runtimes, runtime)
	}
	server.mu.Unlock()
	for _, runtime := range runtimes {
		server.unregister(runtime, errControlServerShuttingDown)
	}
	return nil
}

type controlResumeReplay struct {
	Replay          []harnesscontrol.Frame
	SentThrough     uint64
	ReceivedThrough uint64
}

func (runtime *attemptControlRuntime) handshake(ctx context.Context, connection *controlConnection) (bool, controlResumeReplay, uint64, error) {
	handshakeContext, cancel := context.WithTimeout(ctx, runtime.server.config.HandshakeTimeout)
	defer cancel()
	messageType, raw, err := connection.socket.Read(handshakeContext)
	if err != nil {
		return false, controlResumeReplay{}, 0, fmt.Errorf("read harness worker hello: %w", err)
	}
	if messageType != websocket.MessageText {
		return false, controlResumeReplay{}, 0, controlProtocolError(harnesscontrol.ErrorMalformedFrame, "hello must be a text message")
	}
	message, err := harnesscontrol.Decode(raw, runtime.server.config.WireLimits)
	if err != nil {
		return false, controlResumeReplay{}, 0, err
	}
	if message.Hello == nil {
		return false, controlResumeReplay{}, 0, controlProtocolError(harnesscontrol.ErrorMalformedFrame, "first message must be hello")
	}
	hello := *message.Hello
	if !slices.Contains(hello.ProtocolVersions, harnesscontrol.CurrentProtocolVersion) {
		return false, controlResumeReplay{}, 0, controlProtocolError(
			harnesscontrol.ErrorProtocolVersionUnsupported, "worker does not offer harness control protocol "+harnesscontrol.CurrentProtocolVersion,
		)
	}

	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.closed {
		return false, controlResumeReplay{}, 0, controlProtocolError(harnesscontrol.ErrorSessionClosed, "attempt control registration is closed")
	}
	if runtime.connection != nil {
		// The peer can observe socket failure just before this handler's old
		// ServeHTTP goroutine runs its detach. Reject that overlap as retryable
		// within the original resume deadline; it is not an authority failure.
		return false, controlResumeReplay{}, 0, &harnesscontrol.ProtocolError{
			Code:     harnesscontrol.ErrorResumeRejected,
			Message:  "attempt still has the previous control connection attached",
			Terminal: false,
		}
	}
	if err := runtime.matchExpectedHello(hello); err != nil {
		return false, controlResumeReplay{}, 0, err
	}
	fresh := hello.Resume == nil
	result := controlResumeReplay{}
	if fresh {
		if runtime.session != nil || runtime.workerID != "" {
			return false, controlResumeReplay{}, 0, controlProtocolError(
				harnesscontrol.ErrorResumeRejected, "existing control session must be resumed without replacing its journal",
			)
		}
		binding, err := harnesscontrol.BindingFromHello(hello)
		if err != nil {
			return false, controlResumeReplay{}, 0, err
		}
		session, err := harnesscontrol.NewSession(harnesscontrol.SessionConfig{
			Role: harnesscontrol.RolePool, PoolInstanceID: runtime.server.config.PoolInstanceID,
			ControlSessionID: runtime.controlID, Attempt: binding,
			WireLimits:              runtime.server.config.WireLimits,
			MaxUnackedFrames:        runtime.server.config.MaxUnackedFrames,
			MaxJournalBytes:         runtime.expected.MaxJournalBytes,
			MaxReceiveHistoryFrames: runtime.server.config.MaxReceiveHistoryFrames,
			ResumeWindow:            time.Duration(harnesscontrol.ResumeWindowMillis) * time.Millisecond,
		})
		if err != nil {
			return false, controlResumeReplay{}, 0, err
		}
		runtime.workerID = hello.WorkerInstanceID
		runtime.session = session
	} else {
		if runtime.session == nil || runtime.workerID == "" || hello.WorkerInstanceID != runtime.workerID {
			return false, controlResumeReplay{}, 0, controlProtocolError(
				harnesscontrol.ErrorResumeRejected, "resume is not owned by this worker and pool process",
			)
		}
		resume := hello.Resume
		resumed, err := runtime.session.Resume(harnesscontrol.ResumeRequest{
			PoolInstanceID: resume.PoolInstanceID, ControlSessionID: resume.ControlSessionID,
			RunAttemptGeneration: resume.RunAttemptGeneration,
			PeerSentThrough:      resume.WorkerSentThrough, PeerReceivedThrough: resume.WorkerReceivedThrough,
		}, runtime.server.config.Now())
		if err != nil {
			return false, controlResumeReplay{}, 0, err
		}
		result = controlResumeReplay{
			Replay: resumed.Replay, SentThrough: resumed.SentThrough, ReceivedThrough: resumed.ReceivedThrough,
		}
	}
	if runtime.resumeTimer != nil {
		runtime.resumeTimer.Stop()
		runtime.resumeTimer = nil
	}
	runtime.epoch++
	runtime.connection = connection
	runtime.connectionReady = false
	return fresh, result, runtime.epoch, nil
}

func (runtime *attemptControlRuntime) matchExpectedHello(hello harnesscontrol.Hello) error {
	expected := runtime.expected
	if hello.WorkspaceID != expected.WorkspaceID || hello.SessionID != expected.SessionID ||
		hello.RunID != expected.RunID || hello.RunAttemptID != expected.RunAttemptID ||
		hello.HolderID != expected.HolderID || hello.ManifestDigest != expected.ManifestDigest {
		return controlProtocolError(harnesscontrol.ErrorAttemptMismatch, "worker hello does not match the registered attempt authority")
	}
	if hello.RunAttemptGeneration != expected.RunAttemptGeneration {
		return controlProtocolError(harnesscontrol.ErrorStaleGeneration, "worker hello has a stale run-attempt generation")
	}
	return nil
}

func (server *ControlServer) runConnection(ctx context.Context, runtime *attemptControlRuntime, connection *controlConnection) (bool, error) {
	for {
		messageType, raw, err := connection.socket.Read(ctx)
		if err != nil {
			return false, err
		}
		if messageType != websocket.MessageText {
			return true, controlProtocolError(harnesscontrol.ErrorMalformedFrame, "harness control messages must be text")
		}
		terminal, err := server.handleMessage(ctx, runtime, connection, raw)
		if err != nil || terminal {
			return terminal, err
		}
	}
}

func (server *ControlServer) handleMessage(ctx context.Context, runtime *attemptControlRuntime, connection *controlConnection, raw []byte) (bool, error) {
	message, err := harnesscontrol.Decode(raw, server.config.WireLimits)
	if err != nil {
		return true, err
	}
	switch {
	case message.Ack != nil:
		if err := runtime.session.ReceiveAck(*message.Ack); err != nil {
			return true, err
		}
		return false, nil
	case message.Frame != nil:
		runtime.ackBarrier.Lock()
		defer runtime.ackBarrier.Unlock()
		prepared, received, err := runtime.session.PrepareReceive(*message.Frame)
		if err != nil {
			return true, err
		}
		if received.Deliver {
			event, err := harnesscontrol.DecodeEventPayload(message.Frame.Payload, server.config.WireLimits)
			if err != nil {
				return true, err
			}
			if err := runtime.processSequencedEvent(ctx, message.Frame.SessionSeq, event); err != nil {
				return !isRetryableRuntimeEventError(err), err
			}
		}
		if _, err := runtime.session.CommitReceive(prepared); err != nil {
			return true, err
		}
		ack, err := runtime.session.AckFrame()
		if err != nil {
			return true, err
		}
		if err := connection.write(ctx, server.config.WriteTimeout, server.config.WireLimits, ack); err != nil {
			// The event and its synchronous authority boundary may already be
			// committed. Treat only the ACK write as ambiguous transport loss;
			// the peer will reconcile the cumulative cursor on resume.
			return false, err
		}
		return runtime.terminalReceived(), nil
	case message.SessionError != nil:
		return true, &harnesscontrol.ProtocolError{
			Code: message.SessionError.Code, Message: "harness worker terminated the control session",
			Terminal: true, LostFrom: message.SessionError.LostFrom, LostTo: message.SessionError.LostTo,
		}
	default:
		return true, controlProtocolError(harnesscontrol.ErrorMalformedFrame, "hello or welcome is invalid after handshake")
	}
}

func (runtime *attemptControlRuntime) processEvent(event harnesscontrol.Event) error {
	return runtime.processSequencedEvent(context.Background(), 0, event)
}

func (runtime *attemptControlRuntime) processSequencedEvent(ctx context.Context, controlSequence uint64, event harnesscontrol.Event) error {
	runtime.eventMu.Lock()
	defer runtime.eventMu.Unlock()
	switch event.Kind {
	case harnesscontrol.EventKindThreadReady:
		value := event.ThreadReady
		if value == nil || runtime.threadID != "" || runtime.turnAccepted || runtime.terminalSeen {
			return controlProtocolError(harnesscontrol.ErrorAttemptMismatch, "thread_ready is out of order or repeated")
		}
		if value.Resumed != runtime.expected.Resumed {
			return controlProtocolError(harnesscontrol.ErrorAttemptMismatch, "thread_ready resume mode does not match the signed manifest")
		}
		if err := runtime.lifecycle.ThreadStarted(value.ThreadID); err != nil {
			return fmt.Errorf("apply worker thread_ready lifecycle: %w", err)
		}
		runtime.threadID = value.ThreadID
		return nil
	case harnesscontrol.EventKindTurnAccepted:
		value := event.TurnAccepted
		if value == nil || runtime.threadID == "" || runtime.turnAccepted || value.ThreadID != runtime.threadID || runtime.terminalSeen {
			return controlProtocolError(harnesscontrol.ErrorAttemptMismatch, "turn_accepted is out of order or changes the thread")
		}
		if err := runtime.lifecycle.TurnAccepted(value.ThreadID, value.TurnID); err != nil {
			return fmt.Errorf("apply worker turn_accepted lifecycle: %w", err)
		}
		runtime.turnID = value.TurnID
		runtime.turnAccepted = true
		return nil
	case harnesscontrol.EventKindTurnTerminal:
		value := event.TurnTerminal
		if value == nil || !runtime.turnAccepted || value.ThreadID != runtime.threadID || value.TurnID != runtime.turnID || runtime.terminalSeen {
			return controlProtocolError(harnesscontrol.ErrorAttemptMismatch, "turn_terminal is out of order or changes the accepted turn")
		}
		copy := *value
		runtime.terminalSeen = true
		runtime.approvalCancel(errors.New("turn reached terminal state"))
		runtime.mu.Lock()
		runtime.finishLocked(controlOutcome{terminal: &copy})
		runtime.mu.Unlock()
		return nil
	case harnesscontrol.EventKindAppServerNotification, harnesscontrol.EventKindExecutorMCPProgress:
		if !runtime.turnAccepted || runtime.terminalSeen {
			return controlProtocolError(harnesscontrol.ErrorAttemptMismatch, "runtime event is outside the accepted turn")
		}
		runtimeLifecycle, ok := runtime.lifecycle.(AttemptRuntimeLifecycle)
		if !ok {
			return errors.New("attempt lifecycle does not implement runtime event authority")
		}
		if err := runtimeLifecycle.RuntimeEvent(ctx, AttemptRuntimeEvent{
			ControlSequence: controlSequence, Event: event,
		}); err != nil {
			return fmt.Errorf("apply worker runtime event: %w", err)
		}
		return nil
	case harnesscontrol.EventKindApprovalRequest:
		if !runtime.turnAccepted || runtime.terminalSeen || event.ApprovalRequest == nil {
			return controlProtocolError(harnesscontrol.ErrorAttemptMismatch, "approval request is outside the accepted turn")
		}
		return runtime.registerApproval(*event.ApprovalRequest)
	default:
		return controlProtocolError(harnesscontrol.ErrorMalformedFrame, "unknown worker lifecycle event")
	}
}

func (runtime *attemptControlRuntime) registerApproval(request harnesscontrol.ApprovalRequestEvent) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if request.RunID != runtime.expected.RunID ||
		request.RunAttemptGeneration != runtime.expected.RunAttemptGeneration ||
		request.ToolCatalogDigest != runtime.expected.ToolCatalogDigest {
		return controlProtocolError(
			harnesscontrol.ErrorAttemptMismatch,
			"approval request does not match the signed run attempt and executor catalog",
		)
	}
	now := runtime.server.config.Now()
	if !now.Before(request.ExpiresAt) || request.ExpiresAt.After(now.Add(runtime.expected.MaxApprovalTTL)) {
		return controlProtocolError(
			harnesscontrol.ErrorAttemptMismatch,
			"approval request expiry is outside the signed attempt limit",
		)
	}
	lifecycle, ok := runtime.lifecycle.(AttemptApprovalLifecycle)
	if !ok {
		return errors.New("attempt lifecycle does not implement approval observation authority")
	}

	runtime.approvalMu.Lock()
	if _, duplicate := runtime.approvalIDs[request.ApprovalID]; duplicate {
		runtime.approvalMu.Unlock()
		return controlProtocolError(harnesscontrol.ErrorAttemptMismatch, "approval ID was already observed in this attempt")
	}
	if _, duplicate := runtime.approvalCalls[request.CallID]; duplicate {
		runtime.approvalMu.Unlock()
		return controlProtocolError(harnesscontrol.ErrorAttemptMismatch, "approval call ID was already observed in this attempt")
	}
	if len(runtime.approvals) >= runtime.server.config.MaxOutstandingApprovals {
		runtime.approvalMu.Unlock()
		return controlProtocolError(harnesscontrol.ErrorJournalFull, "too many outstanding approval observations")
	}
	// Retaining seen IDs prevents a later sequence from reusing authority
	// identifiers after its outcome was delivered. Bound the history by the
	// already-negotiated per-session receive-history limit.
	if len(runtime.approvalIDs) >= runtime.server.config.MaxReceiveHistoryFrames {
		runtime.approvalMu.Unlock()
		return controlProtocolError(harnesscontrol.ErrorJournalFull, "approval identity history is full")
	}
	approvalCtx, cancel := context.WithCancel(runtime.approvalCtx)
	entry := &outstandingControlApproval{request: request, cancel: cancel}
	runtime.approvals[request.ApprovalID] = entry
	runtime.approvalIDs[request.ApprovalID] = struct{}{}
	runtime.approvalCalls[request.CallID] = struct{}{}
	runtime.approvalMu.Unlock()

	go runtime.awaitApproval(approvalCtx, lifecycle, entry)
	return nil
}

func (runtime *attemptControlRuntime) awaitApproval(
	ctx context.Context,
	lifecycle AttemptApprovalLifecycle,
	entry *outstandingControlApproval,
) {
	defer entry.cancel()
	defer runtime.removeOutstandingApproval(entry)

	outcome, err := lifecycle.AwaitApproval(ctx, entry.request)
	if err != nil {
		if ctx.Err() == nil {
			runtime.server.unregister(runtime, fmt.Errorf("observe canonical approval outcome: %w", err))
		}
		return
	}
	if err := validateApprovalOutcomeCorrelation(entry.request, outcome); err != nil {
		runtime.server.unregister(runtime, err)
		return
	}
	if err := runtime.sendApprovalOutcome(ctx, outcome); err != nil && ctx.Err() == nil {
		runtime.server.unregister(runtime, fmt.Errorf("send canonical approval outcome: %w", err))
	}
}

func validateApprovalOutcomeCorrelation(
	request harnesscontrol.ApprovalRequestEvent,
	outcome harnesscontrol.ApprovalOutcomeCommand,
) error {
	if err := outcome.Validate(); err != nil {
		return fmt.Errorf("validate canonical approval outcome: %w", err)
	}
	if outcome.RunID != request.RunID || outcome.CallID != request.CallID ||
		outcome.RunAttemptGeneration != request.RunAttemptGeneration ||
		outcome.ToolCatalogDigest != request.ToolCatalogDigest ||
		outcome.ExecutionID != request.ExecutionID || outcome.ApprovalID != request.ApprovalID ||
		outcome.Nonce != request.Nonce || outcome.ContextHash != request.ContextHash ||
		outcome.ApprovalVersion <= request.ApprovalVersion {
		return errors.New("canonical approval outcome does not match the registered request")
	}
	return nil
}

// sendApprovalOutcome makes the exact command authoritative in the bounded
// session journal. If the transport is detached, the frame is allocated for
// the next same-holder resume. If a transport write fails after allocation,
// normal resume replays those same bytes and must never allocate a replacement
// sequence.
func (runtime *attemptControlRuntime) sendApprovalOutcome(
	ctx context.Context,
	outcome harnesscontrol.ApprovalOutcomeCommand,
) error {
	payload, err := json.Marshal(outcome)
	if err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return context.Cause(ctx)
		}

		runtime.ackBarrier.Lock()
		runtime.commandMu.Lock()
		runtime.mu.Lock()
		if runtime.closed {
			runtime.mu.Unlock()
			runtime.commandMu.Unlock()
			runtime.ackBarrier.Unlock()
			return errors.New("attempt control registration is closed")
		}
		if runtime.session == nil {
			changed := runtime.connectionChanged
			runtime.mu.Unlock()
			runtime.commandMu.Unlock()
			runtime.ackBarrier.Unlock()
			select {
			case <-changed:
				continue
			case <-ctx.Done():
				return context.Cause(ctx)
			}
		}
		session := runtime.session
		if runtime.connection == nil && session.Snapshot().State == harnesscontrol.SessionDisconnected {
			_, sendErr := session.QueueForResume(harnesscontrol.Payload{
				Type: harnesscontrol.MessageTypeCommand, Payload: payload,
			})
			runtime.mu.Unlock()
			runtime.commandMu.Unlock()
			runtime.ackBarrier.Unlock()
			return sendErr
		}
		if runtime.connection == nil || !runtime.connectionReady {
			// A resume handshake has already fixed the welcome cursors while its
			// connection is not ready. Wait until replay completes before
			// allocating a later live frame.
			changed := runtime.connectionChanged
			runtime.mu.Unlock()
			runtime.commandMu.Unlock()
			runtime.ackBarrier.Unlock()
			select {
			case <-changed:
				continue
			case <-ctx.Done():
				return context.Cause(ctx)
			}
		}
		connection := runtime.connection
		frame, sendErr := session.Send(harnesscontrol.Payload{
			Type: harnesscontrol.MessageTypeCommand, Payload: payload,
		})
		runtime.mu.Unlock()
		runtime.commandMu.Unlock()
		runtime.ackBarrier.Unlock()
		if sendErr != nil {
			return sendErr
		}
		if err := connection.write(ctx, runtime.server.config.WriteTimeout, runtime.server.config.WireLimits, frame); err != nil {
			// The command is already journaled. Force the reader to detach so the
			// same bytes are replayed; treating this as a new send would duplicate
			// the approval outcome under another sequence.
			connection.closeNow()
		}
		return nil
	}
}

func (runtime *attemptControlRuntime) removeOutstandingApproval(entry *outstandingControlApproval) {
	runtime.approvalMu.Lock()
	defer runtime.approvalMu.Unlock()
	if runtime.approvals[entry.request.ApprovalID] == entry {
		delete(runtime.approvals, entry.request.ApprovalID)
	}
}

func (runtime *attemptControlRuntime) signalConnectionChangedLocked() {
	close(runtime.connectionChanged)
	runtime.connectionChanged = make(chan struct{})
}

func (runtime *attemptControlRuntime) terminalReceived() bool {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.outcome.terminal != nil
}

func (runtime *attemptControlRuntime) markReady() {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.readyOnce.Do(func() { close(runtime.ready) })
}

func (runtime *attemptControlRuntime) activateConnection(epoch uint64) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.closed || runtime.epoch != epoch || runtime.connection == nil {
		return
	}
	runtime.connectionReady = true
	runtime.signalConnectionChangedLocked()
	runtime.readyOnce.Do(func() { close(runtime.ready) })
}

func (runtime *attemptControlRuntime) fail(err error) {
	if err == nil {
		err = errors.New("harness control session failed")
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.approvalCancel(err)
	runtime.finishLocked(controlOutcome{err: err})
}

func (runtime *attemptControlRuntime) finishLocked(outcome controlOutcome) {
	runtime.doneOnce.Do(func() {
		runtime.outcome = outcome
		if runtime.readyErr == nil && outcome.err != nil {
			runtime.readyErr = outcome.err
		}
		runtime.readyOnce.Do(func() { close(runtime.ready) })
		close(runtime.done)
	})
}

func (runtime *attemptControlRuntime) closeSession(cause error) {
	runtime.mu.Lock()
	session := runtime.session
	runtime.mu.Unlock()
	if session != nil {
		session.Close(cause)
	}
}

func (runtime *attemptControlRuntime) detach(epoch uint64, now time.Time) {
	runtime.mu.Lock()
	if runtime.epoch != epoch || runtime.connection == nil {
		runtime.mu.Unlock()
		return
	}
	runtime.connection = nil
	runtime.connectionReady = false
	runtime.signalConnectionChangedLocked()
	if runtime.closed || runtime.session == nil {
		runtime.mu.Unlock()
		return
	}
	snapshot := runtime.session.Snapshot()
	if snapshot.State != harnesscontrol.SessionActive {
		runtime.mu.Unlock()
		return
	}
	if err := runtime.session.Disconnect(now); err != nil {
		runtime.finishLocked(controlOutcome{err: err})
		runtime.mu.Unlock()
		return
	}
	timer := time.AfterFunc(time.Duration(harnesscontrol.ResumeWindowMillis)*time.Millisecond, func() {
		runtime.expireResume(epoch)
	})
	runtime.resumeTimer = timer
	runtime.mu.Unlock()
}

func (runtime *attemptControlRuntime) expireResume(epoch uint64) {
	err := &harnesscontrol.ProtocolError{
		Code: harnesscontrol.ErrorResumeExpired, Message: "worker did not resume on the original holder process within the negotiated window", Terminal: true,
	}
	runtime.mu.Lock()
	if runtime.closed || runtime.epoch != epoch || runtime.connection != nil || runtime.session == nil ||
		runtime.session.Snapshot().State != harnesscontrol.SessionDisconnected {
		runtime.mu.Unlock()
		return
	}
	runtime.resumeTimer = nil
	runtime.approvalCancel(err)
	runtime.session.Close(err)
	runtime.finishLocked(controlOutcome{err: err})
	runtime.mu.Unlock()
	runtime.server.remove(runtime)
}

func (server *ControlServer) runtimeForCapability(capabilityID [sha256.Size]byte) *attemptControlRuntime {
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.shuttingDown {
		return nil
	}
	return server.byCapability[capabilityID]
}

func (server *ControlServer) unregister(runtime *attemptControlRuntime, cause error) {
	server.remove(runtime)
	if cause == nil {
		cause = errors.New("attempt control registration closed")
	}
	runtime.mu.Lock()
	if runtime.closed {
		runtime.mu.Unlock()
		return
	}
	runtime.closed = true
	runtime.approvalCancel(cause)
	if runtime.resumeTimer != nil {
		runtime.resumeTimer.Stop()
		runtime.resumeTimer = nil
	}
	connection := runtime.connection
	runtime.connection = nil
	runtime.connectionReady = false
	runtime.signalConnectionChangedLocked()
	session := runtime.session
	runtime.finishLocked(controlOutcome{err: cause})
	runtime.mu.Unlock()
	if session != nil {
		session.Close(cause)
	}
	if connection != nil {
		connection.closeNow()
	}
}

func (server *ControlServer) remove(runtime *attemptControlRuntime) {
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.byCapability[runtime.capabilityID] == runtime {
		delete(server.byCapability, runtime.capabilityID)
	}
	if server.byAttempt[runtime.expected.RunAttemptID] == runtime {
		delete(server.byAttempt, runtime.expected.RunAttemptID)
	}
}

func (server *ControlServer) writeSessionFailure(ctx context.Context, connection *controlConnection, err error) {
	value := harnesscontrol.SessionErrorFrom(err)
	_ = connection.write(ctx, server.config.WriteTimeout, server.config.WireLimits, value)
}

func controlProtocolError(code harnesscontrol.ErrorCode, message string) error {
	return &harnesscontrol.ProtocolError{Code: code, Message: message, Terminal: true}
}

func validateControlServerConfig(config ControlServerConfig) (string, error) {
	if err := validateUUIDIdentity("pool instance ID", config.PoolInstanceID); err != nil {
		return "", err
	}
	if !validClientProtocolText(config.HolderID, 256) {
		return "", errors.New("control holder ID must contain between 1 and 256 valid UTF-8 bytes without NUL")
	}
	parsed, err := url.Parse(config.CallbackEndpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path == "" {
		return "", errors.New("control callback endpoint must be a credential-free HTTPS URL with an explicit path")
	}
	if parsed.Path != HarnessControlPath {
		return "", fmt.Errorf("control callback endpoint path must be %q", HarnessControlPath)
	}
	if err := validateSPIFFEIdentity("control callback TLS identity", config.CallbackTLSIdentity); err != nil {
		return "", err
	}
	if !validClientProtocolText(config.CallbackAudience, 256) {
		return "", errors.New("control callback audience must contain between 1 and 256 valid UTF-8 bytes without NUL")
	}
	if config.WorkerServiceAccount == "" {
		return "", errors.New("worker service account is required")
	}
	if err := validateSPIFFEIdentity("worker TLS identity", config.WorkerTLSIdentity); err != nil {
		return "", err
	}
	if config.WireLimits.MaxFrameBytes < 1 || config.WireLimits.MaxJSONValues < 1 || config.WireLimits.MaxJSONDepth < 1 {
		return "", errors.New("control wire limits must be positive")
	}
	if config.MaxUnackedFrames < 1 || config.MaxReceiveHistoryFrames < 1 {
		return "", errors.New("control journal frame limits must be positive")
	}
	if config.MaxOutstandingApprovals < 1 || config.MaxOutstandingApprovals > maximumControlOutstandingApprovals {
		return "", fmt.Errorf("control outstanding approvals must be between 1 and %d", maximumControlOutstandingApprovals)
	}
	if config.HandshakeTimeout <= 0 || config.WriteTimeout <= 0 {
		return "", errors.New("control handshake and write timeouts must be positive")
	}
	if config.IDGenerator == nil || config.CapabilityGenerator == nil || config.Now == nil {
		return "", errors.New("control identity, capability, and clock functions are required")
	}
	return parsed.Path, nil
}
