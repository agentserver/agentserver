package executorgateway

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/agentserver/agentserver/v2/internal/executorgateway/agentxconn"
	"nhooyr.io/websocket"
)

const AgentxConnectPath = "/internal/v2/agentx/connect"

var errServerShuttingDown = errors.New("executor gateway is shutting down")

type InboundFrameHandler interface {
	HandleAgentxFrame(context.Context, ConnectionHolder, agentxconn.Frame) error
}

type ServerConfig struct {
	GatewayInstanceID       string
	WireLimits              agentxconn.Limits
	MaxUnackedFrames        int
	MaxJournalBytes         int
	MaxReceiveHistoryFrames int
	MaxPendingProcesses     int
	MaxProcessEvents        int
	MaxProcessEventBytes    int
	HandshakeTimeout        time.Duration
	WriteTimeout            time.Duration
	ConnectionLeaseTTL      time.Duration
	RenewInterval           time.Duration
	IDGenerator             IDGenerator
	InboundHandler          InboundFrameHandler
	Now                     func() time.Time
}

func DefaultServerConfig(gatewayInstanceID string) ServerConfig {
	return ServerConfig{
		GatewayInstanceID: gatewayInstanceID,
		WireLimits: agentxconn.Limits{
			MaxFrameBytes: 8 * 1024 * 1024,
			MaxJSONValues: 65_536,
			MaxJSONDepth:  256,
		},
		MaxUnackedFrames:        1024,
		MaxJournalBytes:         64 * 1024 * 1024,
		MaxReceiveHistoryFrames: 4096,
		MaxPendingProcesses:     256,
		MaxProcessEvents:        4096,
		MaxProcessEventBytes:    8 * 1024 * 1024,
		HandshakeTimeout:        10 * time.Second,
		WriteTimeout:            10 * time.Second,
		ConnectionLeaseTTL:      45 * time.Second,
		RenewInterval:           10 * time.Second,
		IDGenerator:             newRandomUUID,
		Now:                     time.Now,
	}
}

type connectionPhase uint8

const (
	connectionInitializing connectionPhase = iota + 1
	connectionActivating
	connectionReady
)

type sessionRuntime struct {
	mu      sync.Mutex
	writeMu sync.Mutex

	session       *agentxconn.Session
	holder        ConnectionHolder
	environments  []EnvironmentDeclaration
	phase         connectionPhase
	initRequestID string
	connection    *websocket.Conn
	epoch         uint64
	resumeTimer   *time.Timer
}

type Server struct {
	authenticator ExecutorAuthenticator
	authority     ConnectionAuthority
	config        ServerConfig
	registry      *agentxconn.Registry
	processCalls  *processCallTable

	mu           sync.Mutex
	shuttingDown bool
	byExecutor   map[string]*sessionRuntime
	bySession    map[string]*sessionRuntime
}

func NewServer(authenticator ExecutorAuthenticator, authority ConnectionAuthority, config ServerConfig) (*Server, error) {
	if authenticator == nil {
		return nil, errors.New("executor authenticator is required")
	}
	if authority == nil {
		return nil, errors.New("connection authority is required")
	}
	if config.IDGenerator == nil {
		return nil, errors.New("ID generator is required")
	}
	if config.Now == nil {
		return nil, errors.New("clock is required")
	}
	if config.HandshakeTimeout <= 0 || config.WriteTimeout <= 0 {
		return nil, errors.New("handshake and write timeouts must be positive")
	}
	resumeWindow := time.Duration(agentxconn.ResumeWindowMillis) * time.Millisecond
	if config.RenewInterval <= 0 || config.ConnectionLeaseTTL <= resumeWindow+config.RenewInterval {
		return nil, errors.New("connection lease TTL must exceed resume window plus renew interval")
	}
	registry, err := agentxconn.NewRegistry(config.GatewayInstanceID, agentxconn.RegistryConfig{
		WireLimits:              config.WireLimits,
		MaxUnackedFrames:        config.MaxUnackedFrames,
		MaxJournalBytes:         config.MaxJournalBytes,
		MaxReceiveHistoryFrames: config.MaxReceiveHistoryFrames,
		ResumeWindow:            resumeWindow,
	})
	if err != nil {
		return nil, err
	}
	processCalls, err := newProcessCallTable(config.MaxPendingProcesses, config.MaxProcessEvents, config.MaxProcessEventBytes)
	if err != nil {
		return nil, err
	}
	return &Server{
		authenticator: authenticator,
		authority:     authority,
		config:        config,
		registry:      registry,
		processCalls:  processCalls,
		byExecutor:    make(map[string]*sessionRuntime),
		bySession:     make(map[string]*sessionRuntime),
	}, nil
}

func (s *Server) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet || request.URL.Path != AgentxConnectPath {
		http.NotFound(response, request)
		return
	}
	if !s.acceptingConnections() {
		http.Error(response, "executor gateway is shutting down", http.StatusServiceUnavailable)
		return
	}
	identity, err := s.authenticator.AuthenticateExecutor(request)
	if err != nil || identity.ExecutorID == "" {
		http.Error(response, "executor authentication failed", http.StatusUnauthorized)
		return
	}

	connection, err := websocket.Accept(response, request, &websocket.AcceptOptions{
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	connection.SetReadLimit(int64(s.config.WireLimits.MaxFrameBytes))

	runtime, fresh, replay, err := s.handshake(request.Context(), connection, identity.ExecutorID)
	if err != nil {
		s.writeSessionFailure(request.Context(), connection, err)
		_ = connection.Close(websocket.StatusPolicyViolation, "agentx handshake rejected")
		if runtime != nil {
			s.terminateRuntime(runtime)
		}
		return
	}
	epoch := runtime.bind(connection)
	defer runtime.unbind(epoch)

	welcome := s.welcome(runtime, fresh, replay)
	if err := s.writeValue(request.Context(), runtime, connection, welcome); err != nil {
		s.disconnectRuntime(runtime)
		return
	}
	for _, frame := range replay.Replay {
		if err := s.writeValue(request.Context(), runtime, connection, frame); err != nil {
			s.disconnectRuntime(runtime)
			return
		}
	}

	if fresh {
		if err := s.startLifecycle(request.Context(), runtime, connection); err != nil {
			s.writeSessionFailure(request.Context(), connection, err)
			_ = connection.Close(websocket.StatusPolicyViolation, "agentx lifecycle failed")
			s.terminateRuntime(runtime)
			return
		}
	} else if runtime.currentPhase() == connectionActivating {
		if err := s.activateRuntime(request.Context(), runtime); err != nil {
			s.writeSessionFailure(request.Context(), connection, err)
			_ = connection.Close(websocket.StatusPolicyViolation, "agentx activation failed")
			s.terminateRuntime(runtime)
			return
		}
	}

	terminal, err := s.runConnection(request.Context(), runtime, connection)
	if terminal {
		if err != nil {
			s.writeSessionFailure(request.Context(), connection, err)
		}
		_ = connection.Close(websocket.StatusPolicyViolation, "agentx session terminated")
		s.terminateRuntime(runtime)
		return
	}
	s.disconnectRuntime(runtime)
}

// Shutdown stops new handshakes, closes every process-local transport and
// fences each current core holder. net/http does not own hijacked WebSocket
// connections, so callers must invoke this alongside http.Server.Shutdown.
func (s *Server) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return errors.New("shutdown context is required")
	}
	s.mu.Lock()
	s.shuttingDown = true
	runtimes := make([]*sessionRuntime, 0, len(s.byExecutor))
	for _, runtime := range s.byExecutor {
		runtimes = append(runtimes, runtime)
	}
	s.mu.Unlock()

	for _, runtime := range runtimes {
		runtime.cancelResumeExpiry()
		runtime.session.Close(errServerShuttingDown)
		runtime.closeTransport()
		s.processCalls.failHolder(runtime.currentHolder(), errServerShuttingDown)
	}

	var shutdownErrors []error
	for _, runtime := range runtimes {
		err := s.authority.FenceConnection(ctx, runtime.currentHolder())
		if err != nil && !errors.Is(err, ErrConnectionFenced) {
			shutdownErrors = append(shutdownErrors, err)
		}
		s.removeRuntime(runtime)
		s.registry.Forget(runtime.currentHolder().SessionID)
	}
	return errors.Join(shutdownErrors...)
}

type resumeReplay struct {
	Replay          []agentxconn.Frame
	SentThrough     uint64
	ReceivedThrough uint64
}

func (s *Server) handshake(ctx context.Context, connection *websocket.Conn, executorID string) (*sessionRuntime, bool, resumeReplay, error) {
	handshakeContext, cancel := context.WithTimeout(ctx, s.config.HandshakeTimeout)
	defer cancel()
	messageType, raw, err := connection.Read(handshakeContext)
	if err != nil {
		return nil, false, resumeReplay{}, fmt.Errorf("read agentx hello: %w", err)
	}
	if messageType != websocket.MessageText {
		return nil, false, resumeReplay{}, &agentxconn.ProtocolError{Code: agentxconn.ErrorMalformedFrame, Message: "hello must be a text message", Terminal: true}
	}
	message, err := agentxconn.Decode(raw, s.config.WireLimits)
	if err != nil {
		return nil, false, resumeReplay{}, err
	}
	if message.Hello == nil {
		return nil, false, resumeReplay{}, &agentxconn.ProtocolError{Code: agentxconn.ErrorMalformedFrame, Message: "first message must be hello", Terminal: true}
	}
	hello := *message.Hello
	if !slices.Contains(hello.ProtocolVersions, agentxconn.CurrentProtocolVersion) {
		return nil, false, resumeReplay{}, &agentxconn.ProtocolError{Code: agentxconn.ErrorProtocolVersionUnsupported, Message: "agentx does not offer protocol 2.0", Terminal: true}
	}
	environments, err := convertHelloEnvironments(hello.Environments)
	if err != nil {
		return nil, false, resumeReplay{}, err
	}

	if hello.Resume != nil {
		for _, environment := range hello.Environments {
			if len(environment.ActiveProcesses) != 0 {
				return nil, false, resumeReplay{}, &agentxconn.ProtocolError{Code: agentxconn.ErrorResumeRejected, Message: "active process ownership resume is not enabled in this gateway slice", Terminal: true}
			}
		}
		runtime := s.runtimeForResume(executorID, hello.Resume.SessionID)
		if runtime == nil {
			return nil, false, resumeReplay{}, &agentxconn.ProtocolError{Code: agentxconn.ErrorResumeRejected, Message: "session journal is not owned by this gateway process", Terminal: true}
		}
		session, result, err := s.registry.Resume(executorID, *hello.Resume, s.config.Now())
		if err != nil {
			return runtime, false, resumeReplay{}, err
		}
		if session != runtime.session {
			session.Close(errors.New("registry/runtime session identity mismatch"))
			return runtime, false, resumeReplay{}, errors.New("registry/runtime session identity mismatch")
		}
		holder := runtime.currentHolder()
		renewed, err := s.authority.RenewConnection(ctx, holder, s.config.ConnectionLeaseTTL)
		if err != nil {
			runtime.session.Close(err)
			return runtime, false, resumeReplay{}, authorityProtocolError(err, true)
		}
		if !sameHolder(holder, renewed) {
			runtime.session.Close(errors.New("core renewed a different connection holder"))
			return runtime, false, resumeReplay{}, errors.New("core renewed a different connection holder")
		}
		if renewed.Status != "connecting" && renewed.Status != "online" {
			runtime.session.Close(errors.New("core renewed a non-live connection status"))
			return runtime, false, resumeReplay{}, errors.New("core renewed a non-live connection status")
		}
		runtime.updateHolder(renewed)
		return runtime, false, resumeReplay{
			Replay:          result.Replay,
			SentThrough:     result.SentThrough,
			ReceivedThrough: result.ReceivedThrough,
		}, nil
	}

	sessionID, err := s.config.IDGenerator()
	if err != nil {
		return nil, true, resumeReplay{}, err
	}
	runtimeManifestSHA256, err := decodeDigest(hello.RuntimeManifestSHA256)
	if err != nil {
		return nil, true, resumeReplay{}, err
	}
	execProtocolSourceSHA256, err := decodeDigest(hello.ExecProtocolSourceSHA256)
	if err != nil {
		return nil, true, resumeReplay{}, err
	}
	holder, err := s.authority.AcquireConnection(ctx, AcquireConnectionRequest{
		ExecutorID:               executorID,
		ConnectionID:             hello.ConnectionID,
		SessionID:                sessionID,
		GatewayInstanceID:        s.config.GatewayInstanceID,
		AgentxVersion:            hello.AgentxVersion,
		RuntimeManifestSHA256:    runtimeManifestSHA256,
		ExecProtocolSourceSHA256: execProtocolSourceSHA256,
		Environments:             environments,
		LeaseTTL:                 s.config.ConnectionLeaseTTL,
	})
	if err != nil {
		return nil, true, resumeReplay{}, authorityProtocolError(err, false)
	}
	wantHolder := ConnectionHolder{
		ExecutorID:        executorID,
		ConnectionID:      hello.ConnectionID,
		SessionID:         sessionID,
		GatewayInstanceID: s.config.GatewayInstanceID,
		Generation:        holder.Generation,
	}
	if holder.Generation < 1 || holder.Status != "connecting" || !sameHolder(wantHolder, holder) {
		return nil, true, resumeReplay{}, errors.New("core acquired an unexpected connection holder")
	}
	runtime, prior, err := s.attachRuntime(holder, environments)
	if err != nil {
		_ = s.authority.FenceConnection(ctx, holder)
		return nil, true, resumeReplay{}, err
	}
	if prior != nil {
		s.processCalls.failHolder(prior.currentHolder(), ErrConnectionFenced)
		prior.closeTransport()
	}
	return runtime, true, resumeReplay{}, nil
}

func (s *Server) welcome(runtime *sessionRuntime, fresh bool, replay resumeReplay) agentxconn.Welcome {
	holder := runtime.currentHolder()
	status := "resumed"
	if fresh {
		status = "fresh"
	}
	return agentxconn.Welcome{
		Type:                   agentxconn.MessageTypeWelcome,
		ProtocolVersion:        agentxconn.CurrentProtocolVersion,
		GatewayInstanceID:      holder.GatewayInstanceID,
		SessionID:              holder.SessionID,
		Generation:             holder.Generation,
		ResumeStatus:           status,
		ResumeWindowMillis:     agentxconn.ResumeWindowMillis,
		GatewaySentThrough:     replay.SentThrough,
		GatewayReceivedThrough: replay.ReceivedThrough,
	}
}

func (s *Server) startLifecycle(ctx context.Context, runtime *sessionRuntime, connection *websocket.Conn) error {
	requestID, err := s.config.IDGenerator()
	if err != nil {
		return err
	}
	payload, err := agentxconn.InitializePayload(requestID)
	if err != nil {
		return err
	}
	frame, err := runtime.session.Send(payload)
	if err != nil {
		return err
	}
	runtime.setInitializing(requestID)
	return s.writeValue(ctx, runtime, connection, frame)
}

type socketRead struct {
	messageType websocket.MessageType
	raw         []byte
	err         error
}

func (s *Server) runConnection(ctx context.Context, runtime *sessionRuntime, connection *websocket.Conn) (bool, error) {
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	reads := make(chan socketRead, 1)
	go func() {
		for {
			messageType, raw, err := connection.Read(runContext)
			select {
			case reads <- socketRead{messageType: messageType, raw: raw, err: err}:
			case <-runContext.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()

	ticker := time.NewTicker(s.config.RenewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-ticker.C:
			pingContext, pingCancel := context.WithTimeout(ctx, s.config.WriteTimeout)
			runtime.writeMu.Lock()
			pingErr := connection.Ping(pingContext)
			runtime.writeMu.Unlock()
			pingCancel()
			if pingErr != nil {
				return false, pingErr
			}
			holder := runtime.currentHolder()
			renewed, err := s.authority.RenewConnection(ctx, holder, s.config.ConnectionLeaseTTL)
			if err != nil {
				return true, authorityProtocolError(err, true)
			}
			if !sameHolder(holder, renewed) {
				return true, errors.New("core renewed a different connection holder")
			}
			if renewed.Status != "connecting" && renewed.Status != "online" {
				return true, errors.New("core renewed a non-live connection status")
			}
			runtime.updateHolder(renewed)
		case read := <-reads:
			if read.err != nil {
				return false, read.err
			}
			if read.messageType != websocket.MessageText {
				return true, &agentxconn.ProtocolError{Code: agentxconn.ErrorMalformedFrame, Message: "agentx messages must be text", Terminal: true}
			}
			if err := s.handleMessage(ctx, runtime, connection, read.raw); err != nil {
				return true, err
			}
		}
	}
}

func (s *Server) handleMessage(ctx context.Context, runtime *sessionRuntime, connection *websocket.Conn, raw []byte) error {
	message, err := agentxconn.Decode(raw, s.config.WireLimits)
	if err != nil {
		return err
	}
	switch {
	case message.Ack != nil:
		return runtime.session.ReceiveAck(*message.Ack)
	case message.Frame != nil:
		received, err := runtime.session.Receive(*message.Frame)
		if err != nil {
			return err
		}
		if received.Duplicate {
			return s.writeAck(ctx, runtime, connection)
		}
		if !received.Deliver {
			return nil
		}
		phase := runtime.currentPhase()
		if phase == connectionInitializing {
			if err := agentxconn.ValidateInitializeResponse(*message.Frame, runtime.currentInitializeRequestID()); err != nil {
				return err
			}
			initialized, err := runtime.session.Send(agentxconn.InitializedPayload())
			if err != nil {
				return err
			}
			runtime.setPhase(connectionActivating)
			if err := s.writeValue(ctx, runtime, connection, initialized); err != nil {
				return err
			}
			return s.activateRuntime(ctx, runtime)
		}
		if phase != connectionReady {
			return &agentxconn.ProtocolError{Code: agentxconn.ErrorMethodNotNegotiated, Message: "business frame arrived before connection activation", Terminal: true}
		}
		if message.Frame.Type == agentxconn.MessageTypeLifecycle {
			return &agentxconn.ProtocolError{Code: agentxconn.ErrorMethodNotNegotiated, Message: "lifecycle is already complete", Terminal: true}
		}
		handled, err := s.processCalls.handle(runtime.currentHolder(), *message.Frame)
		if err != nil {
			return err
		}
		if !handled {
			if s.config.InboundHandler == nil {
				return &agentxconn.ProtocolError{Code: agentxconn.ErrorMethodNotNegotiated, Message: "business frame is not correlated to a pending gateway request", Terminal: true}
			}
			if err := s.config.InboundHandler.HandleAgentxFrame(ctx, runtime.currentHolder(), *message.Frame); err != nil {
				return err
			}
		}
		return s.writeAck(ctx, runtime, connection)
	case message.SessionError != nil:
		return &agentxconn.ProtocolError{
			Code:     message.SessionError.Code,
			Message:  "agentx terminated the session",
			Terminal: true,
			LostFrom: message.SessionError.LostFrom,
			LostTo:   message.SessionError.LostTo,
		}
	default:
		return &agentxconn.ProtocolError{Code: agentxconn.ErrorMalformedFrame, Message: "hello/welcome is invalid after handshake", Terminal: true}
	}
}

func (s *Server) activateRuntime(ctx context.Context, runtime *sessionRuntime) error {
	holder := runtime.currentHolder()
	activated, err := s.authority.ActivateConnection(ctx, ActivateConnectionRequest{
		Holder:       holder,
		Environments: runtime.clonedEnvironments(),
	})
	if err != nil {
		return authorityProtocolError(err, true)
	}
	if !sameHolder(holder, activated) {
		return errors.New("core activated a different connection holder")
	}
	if activated.Status != "online" {
		return errors.New("core activation did not publish online status")
	}
	runtime.updateHolder(activated)
	runtime.setPhase(connectionReady)
	return nil
}

func (s *Server) writeAck(ctx context.Context, runtime *sessionRuntime, connection *websocket.Conn) error {
	ack, err := runtime.session.AckFrame()
	if err != nil {
		return err
	}
	return s.writeValue(ctx, runtime, connection, ack)
}

func (s *Server) writeValue(ctx context.Context, runtime *sessionRuntime, connection *websocket.Conn, value any) error {
	raw, err := agentxconn.Encode(value, s.config.WireLimits)
	if err != nil {
		return err
	}
	writeContext, cancel := context.WithTimeout(ctx, s.config.WriteTimeout)
	defer cancel()
	runtime.writeMu.Lock()
	defer runtime.writeMu.Unlock()
	return connection.Write(writeContext, websocket.MessageText, raw)
}

func (s *Server) writeSessionFailure(ctx context.Context, connection *websocket.Conn, err error) {
	value := agentxconn.SessionErrorFrom(err)
	raw, encodeErr := agentxconn.Encode(value, s.config.WireLimits)
	if encodeErr != nil {
		return
	}
	writeContext, cancel := context.WithTimeout(ctx, s.config.WriteTimeout)
	defer cancel()
	_ = connection.Write(writeContext, websocket.MessageText, raw)
}

func (s *Server) acceptingConnections() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.shuttingDown
}

// attachRuntime makes the process-local generation fence and the runtime map
// publication one atomic server operation. Splitting Registry.Attach from map
// publication would let an older concurrent handshake overwrite a newer one.
func (s *Server) attachRuntime(holder ConnectionHolder, environments []EnvironmentDeclaration) (*sessionRuntime, *sessionRuntime, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shuttingDown {
		return nil, nil, errServerShuttingDown
	}
	session, err := s.registry.Attach(holder.ExecutorID, holder.SessionID, holder.Generation)
	if err != nil {
		return nil, nil, err
	}
	runtime := &sessionRuntime{
		session:      session,
		holder:       holder,
		environments: cloneEnvironments(environments),
		phase:        connectionInitializing,
	}
	prior := s.byExecutor[holder.ExecutorID]
	if prior != nil {
		delete(s.bySession, prior.currentHolder().SessionID)
	}
	s.byExecutor[holder.ExecutorID] = runtime
	s.bySession[holder.SessionID] = runtime
	return runtime, prior, nil
}

func (s *Server) runtimeForResume(executorID, sessionID string) *sessionRuntime {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shuttingDown {
		return nil
	}
	runtime := s.bySession[sessionID]
	if runtime == nil || runtime.currentHolder().ExecutorID != executorID {
		return nil
	}
	return runtime
}

func (s *Server) removeRuntime(runtime *sessionRuntime) {
	holder := runtime.currentHolder()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byExecutor[holder.ExecutorID] == runtime {
		delete(s.byExecutor, holder.ExecutorID)
	}
	if s.bySession[holder.SessionID] == runtime {
		delete(s.bySession, holder.SessionID)
	}
}

func (s *Server) disconnectRuntime(runtime *sessionRuntime) {
	snapshot := runtime.session.Snapshot()
	if snapshot.State != agentxconn.SessionActive {
		s.removeRuntime(runtime)
		s.registry.Forget(runtime.currentHolder().SessionID)
		return
	}
	if err := s.registry.Disconnect(runtime.currentHolder().SessionID, s.config.Now()); err != nil {
		runtime.session.Close(err)
		s.removeRuntime(runtime)
		s.registry.Forget(runtime.currentHolder().SessionID)
		return
	}
	runtime.scheduleResumeExpiry(time.Duration(agentxconn.ResumeWindowMillis)*time.Millisecond, func() {
		runtime.session.Close(&agentxconn.ProtocolError{Code: agentxconn.ErrorResumeExpired, Message: "resume window expired", Terminal: true})
		s.processCalls.failHolder(runtime.currentHolder(), ErrConnectionFenced)
		s.fenceRuntime(runtime)
		s.removeRuntime(runtime)
		s.registry.Forget(runtime.currentHolder().SessionID)
	})
}

func (s *Server) terminateRuntime(runtime *sessionRuntime) {
	runtime.session.Close(errors.New("terminal agentx connection failure"))
	s.processCalls.failHolder(runtime.currentHolder(), ErrConnectionFenced)
	runtime.closeTransport()
	s.fenceRuntime(runtime)
	s.removeRuntime(runtime)
	s.registry.Forget(runtime.currentHolder().SessionID)
}

func (s *Server) fenceRuntime(runtime *sessionRuntime) {
	ctx, cancel := context.WithTimeout(context.Background(), s.config.WriteTimeout)
	defer cancel()
	err := s.authority.FenceConnection(ctx, runtime.currentHolder())
	if err != nil && !errors.Is(err, ErrConnectionFenced) {
		return
	}
}

func authorityProtocolError(err error, resume bool) error {
	code := agentxconn.ErrorResumeRejected
	if errors.Is(err, ErrConnectionFenced) {
		code = agentxconn.ErrorStaleGeneration
	}
	message := "core rejected fresh executor connection"
	if resume {
		message = "core rejected executor connection resume or renewal"
	}
	return &agentxconn.ProtocolError{Code: code, Message: message, Terminal: true}
}

func convertHelloEnvironments(source []agentxconn.HelloEnvironment) ([]EnvironmentDeclaration, error) {
	converted := make([]EnvironmentDeclaration, len(source))
	for index, environment := range source {
		digest, err := decodeDigest(environment.CodexSHA256)
		if err != nil {
			return nil, err
		}
		converted[index] = EnvironmentDeclaration{
			ID:                  environment.EnvID,
			Platform:            environment.Platform,
			CodexRelease:        environment.CodexRelease,
			CodexCommit:         environment.CodexCommit,
			CodexSHA256:         digest,
			OuterProfileVersion: environment.OuterProfileVersion,
			ProcessMethods:      append([]string(nil), environment.ProcessMethods...),
			InsecureDev:         environment.InsecureDev,
		}
	}
	return converted, nil
}

func decodeDigest(value string) ([32]byte, error) {
	var digest [32]byte
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != len(digest) {
		return digest, &agentxconn.ProtocolError{Code: agentxconn.ErrorMalformedFrame, Message: "hello contains an invalid digest", Terminal: true}
	}
	copy(digest[:], decoded)
	return digest, nil
}

func cloneEnvironments(source []EnvironmentDeclaration) []EnvironmentDeclaration {
	cloned := append([]EnvironmentDeclaration(nil), source...)
	for index := range cloned {
		cloned[index].ProcessMethods = append([]string(nil), cloned[index].ProcessMethods...)
	}
	return cloned
}

func sameHolder(left, right ConnectionHolder) bool {
	return left.ExecutorID == right.ExecutorID &&
		left.ConnectionID == right.ConnectionID &&
		left.SessionID == right.SessionID &&
		left.GatewayInstanceID == right.GatewayInstanceID &&
		left.Generation == right.Generation
}

func (runtime *sessionRuntime) bind(connection *websocket.Conn) uint64 {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.resumeTimer != nil {
		runtime.resumeTimer.Stop()
		runtime.resumeTimer = nil
	}
	runtime.epoch++
	runtime.connection = connection
	return runtime.epoch
}

func (runtime *sessionRuntime) unbind(epoch uint64) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.epoch == epoch {
		runtime.connection = nil
	}
}

func (runtime *sessionRuntime) closeTransport() {
	runtime.mu.Lock()
	connection := runtime.connection
	runtime.mu.Unlock()
	if connection != nil {
		_ = connection.CloseNow()
	}
}

func (runtime *sessionRuntime) currentHolder() ConnectionHolder {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.holder
}

func (runtime *sessionRuntime) updateHolder(holder ConnectionHolder) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.holder = holder
}

func (runtime *sessionRuntime) clonedEnvironments() []EnvironmentDeclaration {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return cloneEnvironments(runtime.environments)
}

func (runtime *sessionRuntime) currentPhase() connectionPhase {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.phase
}

func (runtime *sessionRuntime) setPhase(phase connectionPhase) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.phase = phase
}

func (runtime *sessionRuntime) setInitializing(requestID string) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.phase = connectionInitializing
	runtime.initRequestID = requestID
}

func (runtime *sessionRuntime) currentInitializeRequestID() string {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.initRequestID
}

func (runtime *sessionRuntime) scheduleResumeExpiry(delay time.Duration, expire func()) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.resumeTimer != nil {
		runtime.resumeTimer.Stop()
	}
	epoch := runtime.epoch
	var timer *time.Timer
	timer = time.AfterFunc(delay, func() {
		runtime.mu.Lock()
		if runtime.resumeTimer != timer || runtime.connection != nil || runtime.epoch != epoch {
			runtime.mu.Unlock()
			return
		}
		runtime.resumeTimer = nil
		runtime.mu.Unlock()
		expire()
	})
	runtime.resumeTimer = timer
}

func (runtime *sessionRuntime) cancelResumeExpiry() {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.resumeTimer != nil {
		runtime.resumeTimer.Stop()
		runtime.resumeTimer = nil
	}
}
