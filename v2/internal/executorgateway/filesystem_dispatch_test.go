package executorgateway

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/codexwire"
	"github.com/agentserver/agentserver/v2/internal/execprofile"
	"github.com/agentserver/agentserver/v2/internal/executorgateway/agentxconn"
	"nhooyr.io/websocket"
)

func TestServerDispatchFilesystemCorrelatesOneExactResponse(t *testing.T) {
	fixture := newServerFixture(t, testGatewayInstanceID)
	connection, agentSession, welcome := connectFilesystemEnvironment(t, fixture, testConnectionID(91))
	routing := testFilesystemRoutingContext()
	exchange, err := fixture.server.DispatchFilesystem(testDeadline(t), FilesystemDispatchRequest{
		ExecutorID: testExecutorID, ExpectedConnectionGeneration: welcome.Generation,
		Context: routing, RPC: testFilesystemReadRPC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := readAgentxMessage(t, connection, fixture.config.WireLimits)
	if request.Frame == nil || request.Frame.Context == nil || *request.Frame.Context != routing || request.Frame.Directives != nil {
		t.Fatalf("dispatched filesystem frame = %+v", request.Frame)
	}
	if received, err := agentSession.Receive(*request.Frame); err != nil || !received.Deliver {
		t.Fatalf("agent receive filesystem read = %+v, %v", received, err)
	}
	message, err := codexwire.Parse(request.Frame.RPC)
	if err != nil || message.Method != execprofile.MethodFilesystemReadFileBlock {
		t.Fatalf("filesystem RPC = %s, %v", request.Frame.RPC, err)
	}

	responseRPC := json.RawMessage(`{"id":"fs-read-1","result":{"chunk":"aGVsbG8=","eof":true}}`)
	sendAgentBusinessRPC(t, connection, fixture.config.WireLimits, agentSession, routing, responseRPC)
	response, err := exchange.AwaitResponse(testDeadline(t))
	if err != nil || string(response) != string(responseRPC) {
		t.Fatalf("filesystem response = %s, %v", response, err)
	}
	readAgentAck(t, connection, fixture.config.WireLimits, agentSession)
	select {
	case <-exchange.Done():
	default:
		t.Fatal("filesystem exchange remained open after its response")
	}
	fixture.server.filesystemCalls.mu.Lock()
	pending := len(fixture.server.filesystemCalls.byResponse)
	fixture.server.filesystemCalls.mu.Unlock()
	if pending != 0 {
		t.Fatalf("filesystem response left %d pending calls", pending)
	}
	_ = connection.CloseNow()
}

func TestServerDispatchFilesystemRequiresCurrentHelloProfile(t *testing.T) {
	fixture := newServerFixture(t, testGatewayInstanceID)
	connection, _, welcome := fixture.connectAndInitialize(t, testConnectionID(92))
	_, err := fixture.server.DispatchFilesystem(testDeadline(t), FilesystemDispatchRequest{
		ExecutorID: testExecutorID, ExpectedConnectionGeneration: welcome.Generation,
		Context: testFilesystemRoutingContext(), RPC: testFilesystemReadRPC(),
	})
	if !errors.Is(err, ErrFilesystemReadUnavailable) {
		t.Fatalf("process-only live profile error = %v", err)
	}
	fixture.server.filesystemCalls.mu.Lock()
	pending := len(fixture.server.filesystemCalls.byResponse)
	fixture.server.filesystemCalls.mu.Unlock()
	if pending != 0 {
		t.Fatal("unsupported live profile registered a filesystem call")
	}
	_ = connection.CloseNow()
}

func TestServerFilesystemExchangeFailsOnTransportDisconnect(t *testing.T) {
	fixture := newServerFixture(t, testGatewayInstanceID)
	connection, agentSession, welcome := connectFilesystemEnvironment(t, fixture, testConnectionID(93))
	exchange, err := fixture.server.DispatchFilesystem(testDeadline(t), FilesystemDispatchRequest{
		ExecutorID: testExecutorID, ExpectedConnectionGeneration: welcome.Generation,
		Context: testFilesystemRoutingContext(), RPC: testFilesystemReadRPC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := readAgentxMessage(t, connection, fixture.config.WireLimits)
	if request.Frame == nil {
		t.Fatalf("filesystem request = %+v", request)
	}
	if received, err := agentSession.Receive(*request.Frame); err != nil || !received.Deliver {
		t.Fatalf("agent receive filesystem read = %+v, %v", received, err)
	}
	if err := connection.CloseNow(); err != nil {
		t.Fatal(err)
	}
	waitForSessionState(t, fixture.server, testExecutorID, agentxconn.SessionDisconnected)
	if _, err := exchange.AwaitResponse(testDeadline(t)); !errors.Is(err, ErrConnectionFenced) {
		t.Fatalf("disconnected filesystem exchange error = %v", err)
	}
}

func TestServerFilesystemLateResponseAfterSameSessionResumeIsAcknowledgedAndIgnored(t *testing.T) {
	fixture := newServerFixture(t, testGatewayInstanceID)
	connection, agentSession, welcome := connectFilesystemEnvironment(t, fixture, testConnectionID(94))
	routing := testFilesystemRoutingContext()
	requestRPC := json.RawMessage(`{"id":"fs-read-late","method":"agentx/fs/readFileBlock","params":{"path":"file:///workspace/data.bin","offset":0,"len":1048576}}`)
	exchange, err := fixture.server.DispatchFilesystem(testDeadline(t), FilesystemDispatchRequest{
		ExecutorID: testExecutorID, ExpectedConnectionGeneration: welcome.Generation,
		Context: routing, RPC: requestRPC,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := readAgentxMessage(t, connection, fixture.config.WireLimits)
	if request.Frame == nil {
		t.Fatalf("filesystem request = %+v", request)
	}
	if received, err := agentSession.Receive(*request.Frame); err != nil || !received.Deliver {
		t.Fatalf("agent receive filesystem read = %+v, %v", received, err)
	}

	lateResponse, err := agentSession.Send(agentxconn.Payload{
		Type:    agentxconn.MessageTypeRPC,
		Context: &routing,
		RPC:     json.RawMessage(`{"id":"fs-read-late","result":{"chunk":"bGF0ZQ==","eof":true}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.CloseNow(); err != nil {
		t.Fatal(err)
	}
	waitForSessionState(t, fixture.server, testExecutorID, agentxconn.SessionDisconnected)
	if _, err := exchange.AwaitResponse(testDeadline(t)); !errors.Is(err, ErrConnectionFenced) {
		t.Fatalf("disconnected filesystem exchange error = %v", err)
	}

	snapshot := agentSession.Snapshot()
	resumedConnection := fixture.dial(t)
	hello := validServerHello(testConnectionID(95), &agentxconn.ResumeCursor{
		GatewayInstanceID:     welcome.GatewayInstanceID,
		SessionID:             welcome.SessionID,
		Generation:            welcome.Generation,
		AgentxSentThrough:     snapshot.SentThrough,
		AgentxReceivedThrough: snapshot.ReceivedThrough,
	})
	hello.Environments[0].OuterProfileVersion = execprofile.FilesystemReadVersion
	writeAgentxValue(t, resumedConnection, fixture.config.WireLimits, hello)
	resumed := readAgentxMessage(t, resumedConnection, fixture.config.WireLimits)
	if resumed.Welcome == nil || resumed.Welcome.ResumeStatus != "resumed" {
		t.Fatalf("filesystem resume welcome = %+v", resumed)
	}
	if resumed.Welcome.GatewayReceivedThrough+1 != lateResponse.SessionSeq {
		t.Fatalf("gateway expected late response after %d, response sequence = %d", resumed.Welcome.GatewayReceivedThrough, lateResponse.SessionSeq)
	}

	writeAgentxValue(t, resumedConnection, fixture.config.WireLimits, lateResponse)
	readAgentAck(t, resumedConnection, fixture.config.WireLimits, agentSession)
	fixture.server.filesystemCalls.mu.Lock()
	pending := len(fixture.server.filesystemCalls.byResponse)
	fixture.server.filesystemCalls.mu.Unlock()
	if pending != 0 {
		t.Fatalf("late filesystem response left %d tombstones", pending)
	}

	secondRouting := routing
	secondRouting.OperationID = "54000000-0000-4000-8000-000000000099"
	secondRouting.MutationKey = "63000000-0000-4000-8000-000000000099"
	secondRPC := json.RawMessage(`{"id":"fs-read-after-resume","method":"agentx/fs/readFileBlock","params":{"path":"file:///workspace/next.bin","offset":0,"len":16}}`)
	secondExchange, err := fixture.server.DispatchFilesystem(testDeadline(t), FilesystemDispatchRequest{
		ExecutorID: testExecutorID, ExpectedConnectionGeneration: welcome.Generation,
		Context: secondRouting, RPC: secondRPC,
	})
	if err != nil {
		t.Fatalf("dispatch after late response: %v", err)
	}
	secondRequest := readAgentxMessage(t, resumedConnection, fixture.config.WireLimits)
	if secondRequest.Frame == nil || secondRequest.Frame.Context == nil || *secondRequest.Frame.Context != secondRouting {
		t.Fatalf("filesystem request after resume = %+v", secondRequest.Frame)
	}
	if received, err := agentSession.Receive(*secondRequest.Frame); err != nil || !received.Deliver {
		t.Fatalf("agent receive filesystem read after resume = %+v, %v", received, err)
	}
	secondResponse := json.RawMessage(`{"id":"fs-read-after-resume","result":{"chunk":"bmV4dA==","eof":true}}`)
	sendAgentBusinessRPC(t, resumedConnection, fixture.config.WireLimits, agentSession, secondRouting, secondResponse)
	response, err := secondExchange.AwaitResponse(testDeadline(t))
	if err != nil || string(response) != string(secondResponse) {
		t.Fatalf("filesystem response after resume = %s, %v", response, err)
	}
	readAgentAck(t, resumedConnection, fixture.config.WireLimits, agentSession)
	_ = resumedConnection.CloseNow()
}

func TestFilesystemCallTableRejectsRoutingMismatchAndDuplicateRegistration(t *testing.T) {
	table, err := newFilesystemCallTable(2)
	if err != nil {
		t.Fatal(err)
	}
	holder := testProcessHolder()
	routing := testFilesystemRoutingContext()
	exchange, err := table.register(holder, FilesystemDispatchRequest{
		ExecutorID: holder.ExecutorID, ExpectedConnectionGeneration: holder.Generation,
		Context: routing, RPC: testFilesystemReadRPC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := table.register(holder, FilesystemDispatchRequest{
		ExecutorID: holder.ExecutorID, ExpectedConnectionGeneration: holder.Generation,
		Context: routing, RPC: testFilesystemReadRPC(),
	}); err == nil {
		t.Fatal("duplicate filesystem registration was accepted")
	}
	mismatched := routing
	mismatched.OperationID = "54000000-0000-4000-8000-000000000099"
	handled, err := table.handle(holder, agentxconn.Frame{
		Context: &mismatched,
		RPC:     json.RawMessage(`{"id":"fs-read-1","result":{"chunk":"YQ==","eof":true}}`),
	})
	if !handled || protocolErrorCode(err) != agentxconn.ErrorMutationConflict {
		t.Fatalf("filesystem routing mismatch = handled %t error %v", handled, err)
	}
	table.failHolder(holder, ErrConnectionFenced)
	if _, err := exchange.AwaitResponse(testDeadline(t)); !errors.Is(err, ErrConnectionFenced) {
		t.Fatalf("fenced filesystem response error = %v", err)
	}
}

func connectFilesystemEnvironment(t *testing.T, fixture *serverFixture, connectionID string) (*websocket.Conn, *agentxconn.Session, agentxconn.Welcome) {
	t.Helper()
	connection := fixture.dial(t)
	hello := validServerHello(connectionID, nil)
	hello.Environments[0].OuterProfileVersion = execprofile.FilesystemReadVersion
	writeAgentxValue(t, connection, fixture.config.WireLimits, hello)
	welcomeMessage := readAgentxMessage(t, connection, fixture.config.WireLimits)
	if welcomeMessage.Welcome == nil || welcomeMessage.Welcome.ResumeStatus != "fresh" {
		t.Fatalf("fresh filesystem welcome = %+v", welcomeMessage)
	}
	welcome := *welcomeMessage.Welcome
	agentSession := newAgentSession(t, fixture.config, welcome)
	initialize := readAgentxMessage(t, connection, fixture.config.WireLimits)
	if initialize.Frame == nil || initialize.Frame.Type != agentxconn.MessageTypeLifecycle {
		t.Fatalf("filesystem initialize message = %+v", initialize)
	}
	if result, err := agentSession.Receive(*initialize.Frame); err != nil || !result.Deliver {
		t.Fatalf("agent receive filesystem initialize = %+v, %v", result, err)
	}
	respondToInitialize(t, connection, fixture.config.WireLimits, agentSession, *initialize.Frame)
	initialized := readAgentxMessage(t, connection, fixture.config.WireLimits)
	if initialized.Frame == nil || initialized.Frame.Type != agentxconn.MessageTypeLifecycle {
		t.Fatalf("filesystem initialized message = %+v", initialized)
	}
	if result, err := agentSession.Receive(*initialized.Frame); err != nil || !result.Deliver {
		t.Fatalf("agent receive filesystem initialized = %+v, %v", result, err)
	}
	writeAgentxValue(t, connection, fixture.config.WireLimits, mustAgentAck(t, agentSession))
	waitForAuthorityStatus(t, fixture.authority, "online")
	return connection, agentSession, welcome
}

func testFilesystemReadRPC() json.RawMessage {
	return json.RawMessage(`{"id":"fs-read-1","method":"agentx/fs/readFileBlock","params":{"path":"file:///workspace/data.bin","offset":0,"len":1048576}}`)
}

func testFilesystemRoutingContext() agentxconn.RoutingContext {
	return agentxconn.RoutingContext{
		WorkspaceID:          "40000000-0000-4000-8000-000000000004",
		RunID:                "41000000-0000-4000-8000-000000000004",
		RunAttemptID:         "42000000-0000-4000-8000-000000000004",
		RunAttemptGeneration: 3,
		ExecutionID:          "53000000-0000-4000-8000-000000000005",
		OperationID:          "54000000-0000-4000-8000-000000000005",
		EnvID:                testEnvironmentID,
		MutationKey:          "63000000-0000-4000-8000-000000000006",
	}
}
