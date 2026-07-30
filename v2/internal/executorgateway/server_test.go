package executorgateway

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/execprofile"
	"github.com/agentserver/agentserver/v2/internal/executorgateway/agentxconn"
	"nhooyr.io/websocket"
)

const (
	testGatewayInstanceID = "10000000-0000-4000-8000-000000000001"
	testSecondGatewayID   = "10000000-0000-4000-8000-000000000002"
	testExecutorID        = "20000000-0000-4000-8000-000000000002"
	testEnvironmentID     = "60000000-0000-4000-8000-000000000006"
)

func TestServerFreshLifecycleAndSameProcessResume(t *testing.T) {
	fixture := newServerFixture(t, testGatewayInstanceID)
	connection, agentSession, welcome := fixture.connectAndInitialize(t, testConnectionID(1))

	if status := fixture.authority.currentStatus(); status != "online" {
		t.Fatalf("core connection status = %q, want online after initialized", status)
	}
	if err := connection.Close(websocket.StatusNormalClosure, "test disconnect"); err != nil {
		t.Fatalf("close first transport: %v", err)
	}
	waitForSessionState(t, fixture.server, testExecutorID, agentxconn.SessionDisconnected)

	snapshot := agentSession.Snapshot()
	resumedConnection := fixture.dial(t)
	writeAgentxValue(t, resumedConnection, fixture.config.WireLimits, validServerHello(testConnectionID(2), &agentxconn.ResumeCursor{
		GatewayInstanceID:     welcome.GatewayInstanceID,
		SessionID:             welcome.SessionID,
		Generation:            welcome.Generation,
		AgentxSentThrough:     snapshot.SentThrough,
		AgentxReceivedThrough: snapshot.ReceivedThrough,
	}))
	resumed := readAgentxMessage(t, resumedConnection, fixture.config.WireLimits)
	if resumed.Welcome == nil || resumed.Welcome.ResumeStatus != "resumed" {
		t.Fatalf("resume welcome = %+v", resumed)
	}
	if resumed.Welcome.SessionID != welcome.SessionID || resumed.Welcome.Generation != welcome.Generation {
		t.Fatalf("resume changed session/generation: %+v -> %+v", welcome, resumed.Welcome)
	}
	if resumed.Welcome.GatewaySentThrough != snapshot.ReceivedThrough || resumed.Welcome.GatewayReceivedThrough != snapshot.SentThrough {
		t.Fatalf("resume cursors = sent %d received %d, agent snapshot = %+v", resumed.Welcome.GatewaySentThrough, resumed.Welcome.GatewayReceivedThrough, snapshot)
	}
	if fixture.authority.renewCount() == 0 {
		t.Fatal("resume did not validate and renew the exact core holder")
	}
	_ = resumedConnection.Close(websocket.StatusNormalClosure, "done")
}

func TestServerResumeReplaysUnacknowledgedInitialize(t *testing.T) {
	fixture := newServerFixture(t, testGatewayInstanceID)
	connection := fixture.dial(t)
	writeAgentxValue(t, connection, fixture.config.WireLimits, validServerHello(testConnectionID(10), nil))
	welcomeMessage := readAgentxMessage(t, connection, fixture.config.WireLimits)
	if welcomeMessage.Welcome == nil {
		t.Fatalf("fresh welcome = %+v", welcomeMessage)
	}
	welcome := *welcomeMessage.Welcome

	agentSession := newAgentSession(t, fixture.config, welcome)
	if err := connection.CloseNow(); err != nil {
		t.Fatalf("force close before reading initialize: %v", err)
	}
	waitForSessionState(t, fixture.server, testExecutorID, agentxconn.SessionDisconnected)

	resumedConnection := fixture.dial(t)
	writeAgentxValue(t, resumedConnection, fixture.config.WireLimits, validServerHello(testConnectionID(11), &agentxconn.ResumeCursor{
		GatewayInstanceID: welcome.GatewayInstanceID,
		SessionID:         welcome.SessionID,
		Generation:        welcome.Generation,
	}))
	resumed := readAgentxMessage(t, resumedConnection, fixture.config.WireLimits)
	if resumed.Welcome == nil || resumed.Welcome.ResumeStatus != "resumed" || resumed.Welcome.GatewaySentThrough != 1 {
		t.Fatalf("resumed welcome = %+v", resumed)
	}
	replayed := readAgentxMessage(t, resumedConnection, fixture.config.WireLimits)
	if replayed.Frame == nil || replayed.Frame.Type != agentxconn.MessageTypeLifecycle || replayed.Frame.SessionSeq != 1 {
		t.Fatalf("replayed initialize = %+v", replayed)
	}
	if result, err := agentSession.Receive(*replayed.Frame); err != nil || !result.Deliver {
		t.Fatalf("agent receive replayed initialize = %+v, %v", result, err)
	}
	respondToInitialize(t, resumedConnection, fixture.config.WireLimits, agentSession, *replayed.Frame)
	initialized := readAgentxMessage(t, resumedConnection, fixture.config.WireLimits)
	if initialized.Frame == nil {
		t.Fatalf("initialized message = %+v", initialized)
	}
	if result, err := agentSession.Receive(*initialized.Frame); err != nil || !result.Deliver {
		t.Fatalf("agent receive initialized = %+v, %v", result, err)
	}
	writeAgentxValue(t, resumedConnection, fixture.config.WireLimits, mustAgentAck(t, agentSession))
	waitForAuthorityStatus(t, fixture.authority, "online")
	_ = resumedConnection.Close(websocket.StatusNormalClosure, "done")
}

func TestServerFreshGenerationFencesOldSessionAndRejectsItsResume(t *testing.T) {
	fixture := newServerFixture(t, testGatewayInstanceID)
	oldConnection, oldAgentSession, oldWelcome := fixture.connectAndInitialize(t, testConnectionID(20))

	newConnection := fixture.dial(t)
	writeAgentxValue(t, newConnection, fixture.config.WireLimits, validServerHello(testConnectionID(21), nil))
	newWelcomeMessage := readAgentxMessage(t, newConnection, fixture.config.WireLimits)
	if newWelcomeMessage.Welcome == nil || newWelcomeMessage.Welcome.Generation != oldWelcome.Generation+1 {
		t.Fatalf("replacement welcome = %+v, old = %+v", newWelcomeMessage, oldWelcome)
	}
	if _, _, err := oldConnection.Read(testDeadline(t)); err == nil {
		t.Fatal("old WebSocket remained readable after a fresh generation replaced it")
	}

	oldSnapshot := oldAgentSession.Snapshot()
	resumeOld := fixture.dial(t)
	writeAgentxValue(t, resumeOld, fixture.config.WireLimits, validServerHello(testConnectionID(22), &agentxconn.ResumeCursor{
		GatewayInstanceID:     oldWelcome.GatewayInstanceID,
		SessionID:             oldWelcome.SessionID,
		Generation:            oldWelcome.Generation,
		AgentxSentThrough:     oldSnapshot.SentThrough,
		AgentxReceivedThrough: oldSnapshot.ReceivedThrough,
	}))
	rejected := readAgentxMessage(t, resumeOld, fixture.config.WireLimits)
	if rejected.SessionError == nil || rejected.SessionError.Code != agentxconn.ErrorResumeRejected {
		t.Fatalf("old generation resume rejection = %+v", rejected)
	}
	_ = resumeOld.CloseNow()
	_ = newConnection.CloseNow()
}

func TestServerRestartRejectsDatabaseOnlyResume(t *testing.T) {
	first := newServerFixture(t, testGatewayInstanceID)
	connection, agentSession, welcome := first.connectAndInitialize(t, testConnectionID(30))
	if err := connection.Close(websocket.StatusNormalClosure, "restart fixture"); err != nil {
		t.Fatal(err)
	}
	waitForSessionState(t, first.server, testExecutorID, agentxconn.SessionDisconnected)

	second := newServerFixtureWithAuthority(t, testSecondGatewayID, first.authority)
	resumedConnection := second.dial(t)
	snapshot := agentSession.Snapshot()
	writeAgentxValue(t, resumedConnection, second.config.WireLimits, validServerHello(testConnectionID(31), &agentxconn.ResumeCursor{
		GatewayInstanceID:     welcome.GatewayInstanceID,
		SessionID:             welcome.SessionID,
		Generation:            welcome.Generation,
		AgentxSentThrough:     snapshot.SentThrough,
		AgentxReceivedThrough: snapshot.ReceivedThrough,
	}))
	rejected := readAgentxMessage(t, resumedConnection, second.config.WireLimits)
	if rejected.SessionError == nil || rejected.SessionError.Code != agentxconn.ErrorResumeRejected {
		t.Fatalf("restart resume rejection = %+v", rejected)
	}
	_ = resumedConnection.CloseNow()
}

func TestServerAuthenticatesBeforeWebSocketUpgrade(t *testing.T) {
	fixture := newServerFixture(t, testGatewayInstanceID)
	ctx := testDeadline(t)
	_, response, err := websocket.Dial(ctx, fixture.http.URL+AgentxConnectPath, nil)
	if err == nil {
		t.Fatal("unauthenticated WebSocket upgrade succeeded")
	}
	if response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated response = %+v, error = %v", response, err)
	}
}

func TestServerShutdownClosesTransportFencesHolderAndRejectsNewConnections(t *testing.T) {
	fixture := newServerFixture(t, testGatewayInstanceID)
	connection, _, _ := fixture.connectAndInitialize(t, testConnectionID(40))

	if err := fixture.server.Shutdown(testDeadline(t)); err != nil {
		t.Fatalf("shutdown executor gateway: %v", err)
	}
	if status := fixture.authority.currentStatus(); status != "fenced" {
		t.Fatalf("core connection status after shutdown = %q, want fenced", status)
	}
	if _, _, err := connection.Read(testDeadline(t)); err == nil {
		t.Fatal("active WebSocket remained readable after gateway shutdown")
	}

	ctx := testDeadline(t)
	_, response, err := websocket.Dial(ctx, fixture.http.URL+AgentxConnectPath, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer test-executor-proof"}},
	})
	if err == nil {
		t.Fatal("WebSocket upgrade succeeded after gateway shutdown")
	}
	if response == nil || response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("post-shutdown response = %+v, error = %v", response, err)
	}
	if err := fixture.server.Shutdown(testDeadline(t)); err != nil {
		t.Fatalf("second shutdown executor gateway: %v", err)
	}
}

func TestServerAttachRuntimeNeverLetsOlderGenerationReplaceNewerOwner(t *testing.T) {
	fixture := newServerFixture(t, testGatewayInstanceID)
	environments, err := convertHelloEnvironments(validServerHello(testConnectionID(50), nil).Environments)
	if err != nil {
		t.Fatal(err)
	}
	newer := ConnectionHolder{
		ExecutorID:        testExecutorID,
		ConnectionID:      testConnectionID(51),
		SessionID:         "71000000-0000-4000-8000-000000000051",
		GatewayInstanceID: testGatewayInstanceID,
		Generation:        2,
		Status:            "connecting",
	}
	newerRuntime, prior, err := fixture.server.attachRuntime(newer, environments)
	if err != nil || prior != nil {
		t.Fatalf("attach newer runtime = runtime %p prior %p error %v", newerRuntime, prior, err)
	}
	older := newer
	older.ConnectionID = testConnectionID(52)
	older.SessionID = "71000000-0000-4000-8000-000000000052"
	older.Generation = 1
	if _, _, err := fixture.server.attachRuntime(older, environments); err == nil {
		t.Fatal("older generation replaced newer process-local owner")
	}
	current, found := fixture.server.registry.Current(testExecutorID)
	if !found || current != newerRuntime.session {
		t.Fatal("newer generation is no longer the registry owner")
	}
	fixture.server.mu.Lock()
	published := fixture.server.byExecutor[testExecutorID]
	fixture.server.mu.Unlock()
	if published != newerRuntime {
		t.Fatal("newer generation is no longer the published server runtime")
	}
	if err := fixture.server.Shutdown(testDeadline(t)); err != nil {
		t.Fatal(err)
	}
}

type serverFixture struct {
	server    *Server
	authority *fakeConnectionAuthority
	config    ServerConfig
	http      *httptest.Server
}

func newServerFixture(t *testing.T, gatewayID string) *serverFixture {
	t.Helper()
	return newServerFixtureWithAuthority(t, gatewayID, &fakeConnectionAuthority{})
}

func newServerFixtureWithAuthority(t *testing.T, gatewayID string, authority *fakeConnectionAuthority) *serverFixture {
	t.Helper()
	config := DefaultServerConfig(gatewayID)
	config.HandshakeTimeout = 2 * time.Second
	config.WriteTimeout = 2 * time.Second
	config.RenewInterval = 5 * time.Second
	config.ConnectionLeaseTTL = 40 * time.Second
	config.IDGenerator = deterministicIDGenerator()
	server, err := NewServer(testExecutorAuthenticator{}, authority, config)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	return &serverFixture{server: server, authority: authority, config: config, http: httpServer}
}

func (fixture *serverFixture) dial(t *testing.T) *websocket.Conn {
	t.Helper()
	ctx := testDeadline(t)
	connection, response, err := websocket.Dial(ctx, fixture.http.URL+AgentxConnectPath, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer test-executor-proof"}},
	})
	if err != nil {
		t.Fatalf("dial agentx WSS fixture: %v (response %+v)", err, response)
	}
	t.Cleanup(func() { _ = connection.CloseNow() })
	return connection
}

func (fixture *serverFixture) connectAndInitialize(t *testing.T, connectionID string) (*websocket.Conn, *agentxconn.Session, agentxconn.Welcome) {
	t.Helper()
	connection := fixture.dial(t)
	writeAgentxValue(t, connection, fixture.config.WireLimits, validServerHello(connectionID, nil))
	welcomeMessage := readAgentxMessage(t, connection, fixture.config.WireLimits)
	if welcomeMessage.Welcome == nil || welcomeMessage.Welcome.ResumeStatus != "fresh" {
		t.Fatalf("fresh welcome = %+v", welcomeMessage)
	}
	welcome := *welcomeMessage.Welcome
	agentSession := newAgentSession(t, fixture.config, welcome)
	initialize := readAgentxMessage(t, connection, fixture.config.WireLimits)
	if initialize.Frame == nil || initialize.Frame.Type != agentxconn.MessageTypeLifecycle {
		t.Fatalf("initialize message = %+v", initialize)
	}
	if result, err := agentSession.Receive(*initialize.Frame); err != nil || !result.Deliver {
		t.Fatalf("agent receive initialize = %+v, %v", result, err)
	}
	respondToInitialize(t, connection, fixture.config.WireLimits, agentSession, *initialize.Frame)
	initialized := readAgentxMessage(t, connection, fixture.config.WireLimits)
	if initialized.Frame == nil || initialized.Frame.Type != agentxconn.MessageTypeLifecycle {
		t.Fatalf("initialized message = %+v", initialized)
	}
	if result, err := agentSession.Receive(*initialized.Frame); err != nil || !result.Deliver {
		t.Fatalf("agent receive initialized = %+v, %v", result, err)
	}
	writeAgentxValue(t, connection, fixture.config.WireLimits, mustAgentAck(t, agentSession))
	waitForAuthorityStatus(t, fixture.authority, "online")
	return connection, agentSession, welcome
}

type testExecutorAuthenticator struct{}

func (testExecutorAuthenticator) AuthenticateExecutor(request *http.Request) (ExecutorIdentity, error) {
	if request.Header.Get("Authorization") != "Bearer test-executor-proof" {
		return ExecutorIdentity{}, errors.New("missing machine proof")
	}
	return ExecutorIdentity{ExecutorID: testExecutorID}, nil
}

type fakeConnectionAuthority struct {
	mu       sync.Mutex
	current  ConnectionHolder
	renewals int
}

func (authority *fakeConnectionAuthority) AcquireConnection(_ context.Context, request AcquireConnectionRequest) (ConnectionHolder, error) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	generation := authority.current.Generation + 1
	authority.current = ConnectionHolder{
		ExecutorID:        request.ExecutorID,
		ConnectionID:      request.ConnectionID,
		SessionID:         request.SessionID,
		GatewayInstanceID: request.GatewayInstanceID,
		Generation:        generation,
		Status:            "connecting",
		ExpiresAt:         time.Now().Add(request.LeaseTTL),
	}
	return authority.current, nil
}

func (authority *fakeConnectionAuthority) RenewConnection(_ context.Context, holder ConnectionHolder, leaseTTL time.Duration) (ConnectionHolder, error) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	if !sameHolder(holder, authority.current) || authority.current.Status == "fenced" {
		return ConnectionHolder{}, ErrConnectionFenced
	}
	authority.renewals++
	authority.current.ExpiresAt = time.Now().Add(leaseTTL)
	return authority.current, nil
}

func (authority *fakeConnectionAuthority) ActivateConnection(_ context.Context, request ActivateConnectionRequest) (ConnectionHolder, error) {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	if !sameHolder(request.Holder, authority.current) || authority.current.Status == "fenced" {
		return ConnectionHolder{}, ErrConnectionFenced
	}
	authority.current.Status = "online"
	return authority.current, nil
}

func (authority *fakeConnectionAuthority) FenceConnection(_ context.Context, holder ConnectionHolder) error {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	if !sameHolder(holder, authority.current) {
		return ErrConnectionFenced
	}
	authority.current.Status = "fenced"
	return nil
}

func (authority *fakeConnectionAuthority) currentStatus() string {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	return authority.current.Status
}

func (authority *fakeConnectionAuthority) renewCount() int {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	return authority.renewals
}

func validServerHello(connectionID string, resume *agentxconn.ResumeCursor) agentxconn.Hello {
	runtimeDigest := sha256.Sum256([]byte("runtime-manifest"))
	protocolDigest := sha256.Sum256([]byte("exec-protocol"))
	codexDigest := sha256.Sum256([]byte("codex"))
	return agentxconn.Hello{
		Type:                     agentxconn.MessageTypeHello,
		ConnectionID:             connectionID,
		ProtocolVersions:         []string{agentxconn.CurrentProtocolVersion},
		AgentxVersion:            "0.1.0",
		RuntimeManifestSHA256:    fmt.Sprintf("%x", runtimeDigest),
		ExecProtocolSourceSHA256: fmt.Sprintf("%x", protocolDigest),
		Environments: []agentxconn.HelloEnvironment{{
			EnvID:               testEnvironmentID,
			Platform:            "linux-arm64",
			CodexRelease:        "0.146.0",
			CodexCommit:         strings.Repeat("a", 40),
			CodexSHA256:         fmt.Sprintf("%x", codexDigest),
			OuterProfileVersion: execprofile.Version,
			ProcessMethods:      execprofile.ProcessMethods(),
			ActiveProcesses:     []agentxconn.ActiveProcess{},
		}},
		Resume: resume,
	}
}

func newAgentSession(t *testing.T, config ServerConfig, welcome agentxconn.Welcome) *agentxconn.Session {
	t.Helper()
	session, err := agentxconn.NewSession(agentxconn.SessionConfig{
		Role:                    agentxconn.RoleAgentx,
		GatewayInstanceID:       welcome.GatewayInstanceID,
		ExecutorID:              testExecutorID,
		SessionID:               welcome.SessionID,
		Generation:              welcome.Generation,
		WireLimits:              config.WireLimits,
		MaxUnackedFrames:        config.MaxUnackedFrames,
		MaxJournalBytes:         config.MaxJournalBytes,
		MaxReceiveHistoryFrames: config.MaxReceiveHistoryFrames,
		ResumeWindow:            30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func respondToInitialize(t *testing.T, connection *websocket.Conn, limits agentxconn.Limits, session *agentxconn.Session, initialize agentxconn.Frame) {
	t.Helper()
	var request struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(initialize.RPC, &request); err != nil {
		t.Fatal(err)
	}
	rpc := json.RawMessage(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{"sessionId":%q,"protocolVersion":"2.0","serverName":"agentx","outerProfileVersion":"process-v1","processMethods":["process/start","process/read","process/write","process/terminate"]}}`, request.ID, initialize.SessionID))
	frame, err := session.Send(agentxconn.Payload{Type: agentxconn.MessageTypeLifecycle, RPC: rpc})
	if err != nil {
		t.Fatalf("build initialize response: %v", err)
	}
	writeAgentxValue(t, connection, limits, frame)
}

func mustAgentAck(t *testing.T, session *agentxconn.Session) agentxconn.Ack {
	t.Helper()
	ack, err := session.AckFrame()
	if err != nil {
		t.Fatal(err)
	}
	return ack
}

func writeAgentxValue(t *testing.T, connection *websocket.Conn, limits agentxconn.Limits, value any) {
	t.Helper()
	raw, err := agentxconn.Encode(value, limits)
	if err != nil {
		t.Fatalf("encode %T: %v", value, err)
	}
	if err := connection.Write(testDeadline(t), websocket.MessageText, raw); err != nil {
		t.Fatalf("write %T: %v", value, err)
	}
}

func readAgentxMessage(t *testing.T, connection *websocket.Conn, limits agentxconn.Limits) agentxconn.Message {
	t.Helper()
	messageType, raw, err := connection.Read(testDeadline(t))
	if err != nil {
		t.Fatalf("read agentx message: %v", err)
	}
	if messageType != websocket.MessageText {
		t.Fatalf("agentx message type = %v, want text", messageType)
	}
	message, err := agentxconn.Decode(raw, limits)
	if err != nil {
		t.Fatalf("decode agentx message: %v\n%s", err, raw)
	}
	return message
}

func waitForSessionState(t *testing.T, server *Server, executorID string, want agentxconn.SessionState) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if session, found := server.registry.Current(executorID); found && session.Snapshot().State == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	if session, found := server.registry.Current(executorID); found {
		t.Fatalf("session state = %s, want %s", session.Snapshot().State, want)
	}
	t.Fatalf("executor session disappeared, want state %s", want)
}

func waitForAuthorityStatus(t *testing.T, authority *fakeConnectionAuthority, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if authority.currentStatus() == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("authority status = %q, want %q", authority.currentStatus(), want)
}

func deterministicIDGenerator() IDGenerator {
	var mu sync.Mutex
	var next int
	return func() (string, error) {
		mu.Lock()
		defer mu.Unlock()
		next++
		return fmt.Sprintf("70000000-0000-4000-8000-%012x", next), nil
	}
}

func testConnectionID(seed int) string {
	return fmt.Sprintf("30000000-0000-4000-8000-%012x", seed)
}

func testDeadline(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	t.Cleanup(cancel)
	return ctx
}
