package executorgateway

import (
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/codexwire"
	"github.com/agentserver/agentserver/v2/internal/executorgateway/agentxconn"
	"nhooyr.io/websocket"
)

const testProcessID = "80000000-0000-4000-8000-000000000008"

func TestServerDispatchProcessCorrelatesResponseAndTerminalEvents(t *testing.T) {
	fixture := newServerFixture(t, testGatewayInstanceID)
	connection, agentSession, welcome := fixture.connectAndInitialize(t, testConnectionID(80))
	routing := testProcessRoutingContext()
	exchange, err := fixture.server.DispatchProcess(testDeadline(t), ProcessDispatchRequest{
		ExecutorID:                   testExecutorID,
		ExpectedConnectionGeneration: welcome.Generation,
		Context:                      routing,
		RPC:                          testProcessStartRPC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := readAgentxMessage(t, connection, fixture.config.WireLimits)
	if request.Frame == nil || request.Frame.Context == nil || *request.Frame.Context != routing {
		t.Fatalf("dispatched process frame = %+v", request)
	}
	if received, err := agentSession.Receive(*request.Frame); err != nil || !received.Deliver {
		t.Fatalf("agent receive process/start = %+v, %v", received, err)
	}
	requestMessage, err := codexwire.Parse(request.Frame.RPC)
	if err != nil || requestMessage.Method != "process/start" {
		t.Fatalf("process/start RPC = %s, %v", request.Frame.RPC, err)
	}

	responseRPC := json.RawMessage(`{"id":"start-1","result":{"processId":"80000000-0000-4000-8000-000000000008"}}`)
	sendAgentBusinessRPC(t, connection, fixture.config.WireLimits, agentSession, routing, responseRPC)
	response, err := exchange.AwaitResponse(testDeadline(t))
	if err != nil || string(response) != string(responseRPC) {
		t.Fatalf("process response = %s, %v", response, err)
	}
	readAgentAck(t, connection, fixture.config.WireLimits, agentSession)

	events := []json.RawMessage{
		json.RawMessage(`{"method":"process/output","params":{"processId":"80000000-0000-4000-8000-000000000008","seq":1,"stream":"stdout","chunk":"b2s="}}`),
		json.RawMessage(`{"method":"process/exited","params":{"processId":"80000000-0000-4000-8000-000000000008","seq":2,"exitCode":0,"sandboxDenied":false}}`),
		json.RawMessage(`{"method":"process/closed","params":{"processId":"80000000-0000-4000-8000-000000000008","seq":3}}`),
	}
	for _, event := range events {
		sendAgentBusinessRPC(t, connection, fixture.config.WireLimits, agentSession, routing, event)
		retained, err := exchange.NextEvent(testDeadline(t))
		if err != nil || string(retained) != string(event) {
			t.Fatalf("process event = %s, %v; want %s", retained, err, event)
		}
		readAgentAck(t, connection, fixture.config.WireLimits, agentSession)
	}
	if _, err := exchange.NextEvent(testDeadline(t)); !errors.Is(err, io.EOF) {
		t.Fatalf("process event after closed error = %v, want EOF", err)
	}
	select {
	case <-exchange.Done():
	default:
		t.Fatal("process exchange did not close after process/closed")
	}
	_ = connection.CloseNow()
}

func TestServerDispatchProcessSurvivesSameProcessTransportResume(t *testing.T) {
	fixture := newServerFixture(t, testGatewayInstanceID)
	connection, agentSession, welcome := fixture.connectAndInitialize(t, testConnectionID(81))
	routing := testProcessRoutingContext()
	exchange, err := fixture.server.DispatchProcess(testDeadline(t), ProcessDispatchRequest{
		ExecutorID:                   testExecutorID,
		ExpectedConnectionGeneration: welcome.Generation,
		Context:                      routing,
		RPC:                          testProcessStartRPC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	request := readAgentxMessage(t, connection, fixture.config.WireLimits)
	if request.Frame == nil {
		t.Fatalf("process request = %+v", request)
	}
	if received, err := agentSession.Receive(*request.Frame); err != nil || !received.Deliver {
		t.Fatalf("agent receive process/start = %+v, %v", received, err)
	}
	if err := connection.CloseNow(); err != nil {
		t.Fatal(err)
	}
	waitForSessionState(t, fixture.server, testExecutorID, agentxconn.SessionDisconnected)

	snapshot := agentSession.Snapshot()
	resumedConnection := fixture.dial(t)
	writeAgentxValue(t, resumedConnection, fixture.config.WireLimits, validServerHello(testConnectionID(82), &agentxconn.ResumeCursor{
		GatewayInstanceID:     welcome.GatewayInstanceID,
		SessionID:             welcome.SessionID,
		Generation:            welcome.Generation,
		AgentxSentThrough:     snapshot.SentThrough,
		AgentxReceivedThrough: snapshot.ReceivedThrough,
	}))
	resumed := readAgentxMessage(t, resumedConnection, fixture.config.WireLimits)
	if resumed.Welcome == nil || resumed.Welcome.ResumeStatus != "resumed" {
		t.Fatalf("process resume welcome = %+v", resumed)
	}
	responseRPC := json.RawMessage(`{"id":"start-1","result":{"processId":"80000000-0000-4000-8000-000000000008"}}`)
	sendAgentBusinessRPC(t, resumedConnection, fixture.config.WireLimits, agentSession, routing, responseRPC)
	response, err := exchange.AwaitResponse(testDeadline(t))
	if err != nil || string(response) != string(responseRPC) {
		t.Fatalf("resumed process response = %s, %v", response, err)
	}
	readAgentAck(t, resumedConnection, fixture.config.WireLimits, agentSession)
	_ = resumedConnection.CloseNow()
}

func TestServerDispatchProcessFencesUnexpectedConnectionGeneration(t *testing.T) {
	fixture := newServerFixture(t, testGatewayInstanceID)
	connection, _, welcome := fixture.connectAndInitialize(t, testConnectionID(83))
	_, err := fixture.server.DispatchProcess(testDeadline(t), ProcessDispatchRequest{
		ExecutorID:                   testExecutorID,
		ExpectedConnectionGeneration: welcome.Generation + 1,
		Context:                      testProcessRoutingContext(),
		RPC:                          testProcessStartRPC(),
	})
	if !errors.Is(err, ErrConnectionFenced) {
		t.Fatalf("stale process dispatch error = %v", err)
	}
	fixture.server.processCalls.mu.Lock()
	pending := len(fixture.server.processCalls.byProcess)
	fixture.server.processCalls.mu.Unlock()
	if pending != 0 {
		t.Fatal("stale generation registered a process call")
	}
	_ = connection.CloseNow()
}

func TestServerCorrelatesTimeoutDueAndDispatchesTerminate(t *testing.T) {
	fixture := newServerFixture(t, testGatewayInstanceID)
	connection, agentSession, welcome := fixture.connectAndInitialize(t, testConnectionID(84))
	startRouting := testProcessRoutingContext()
	directives := testProcessTimeoutDirectives()
	timeoutRouting := testProcessTimeoutRoutingContext()
	startExchange, err := fixture.server.DispatchProcess(testDeadline(t), ProcessDispatchRequest{
		ExecutorID:                   testExecutorID,
		ExpectedConnectionGeneration: welcome.Generation,
		Context:                      startRouting,
		Directives:                   directives,
		RPC:                          testProcessStartRPC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	startFrame := readAgentxMessage(t, connection, fixture.config.WireLimits)
	if startFrame.Frame == nil || startFrame.Frame.Directives == nil ||
		startFrame.Frame.Directives.ProcessTimeout == nil || startFrame.Frame.Directives.ProcessTimeout.AfterMillis != 60_000 {
		t.Fatalf("process/start timeout directives = %+v", startFrame.Frame)
	}
	if received, err := agentSession.Receive(*startFrame.Frame); err != nil || !received.Deliver {
		t.Fatalf("agent receive timeout process/start = %+v, %v", received, err)
	}
	startResponse := json.RawMessage(`{"id":"start-1","result":{"processId":"80000000-0000-4000-8000-000000000008"}}`)
	sendAgentBusinessRPC(t, connection, fixture.config.WireLimits, agentSession, startRouting, startResponse)
	if _, err := startExchange.AwaitResponse(testDeadline(t)); err != nil {
		t.Fatal(err)
	}
	readAgentAck(t, connection, fixture.config.WireLimits, agentSession)

	timeoutNotification := json.RawMessage(`{"method":"agentx/timeoutDue","params":{"processId":"80000000-0000-4000-8000-000000000008"}}`)
	sendAgentBusinessRPC(t, connection, fixture.config.WireLimits, agentSession, timeoutRouting, timeoutNotification)
	due, err := startExchange.AwaitTimeoutDue(testDeadline(t))
	if err != nil || due.ProcessID != testProcessID || due.Context != timeoutRouting {
		t.Fatalf("timeout due = %+v, %v", due, err)
	}
	readAgentAck(t, connection, fixture.config.WireLimits, agentSession)

	terminateExchange, err := fixture.server.DispatchProcess(testDeadline(t), ProcessDispatchRequest{
		ExecutorID:                   testExecutorID,
		ExpectedConnectionGeneration: welcome.Generation,
		Context:                      timeoutRouting,
		RPC:                          testProcessTerminateRPC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	terminateFrame := readAgentxMessage(t, connection, fixture.config.WireLimits)
	if terminateFrame.Frame == nil || terminateFrame.Frame.Context == nil || *terminateFrame.Frame.Context != timeoutRouting || terminateFrame.Frame.Directives != nil {
		t.Fatalf("process/terminate frame = %+v", terminateFrame.Frame)
	}
	if received, err := agentSession.Receive(*terminateFrame.Frame); err != nil || !received.Deliver {
		t.Fatalf("agent receive process/terminate = %+v, %v", received, err)
	}
	terminateResponse := json.RawMessage(`{"id":"terminate-1","result":{}}`)
	sendAgentBusinessRPC(t, connection, fixture.config.WireLimits, agentSession, timeoutRouting, terminateResponse)
	response, err := terminateExchange.AwaitResponse(testDeadline(t))
	if err != nil || string(response) != string(terminateResponse) {
		t.Fatalf("process/terminate response = %s, %v", response, err)
	}
	readAgentAck(t, connection, fixture.config.WireLimits, agentSession)
	select {
	case <-terminateExchange.Done():
	default:
		t.Fatal("terminate exchange remained open after matching response")
	}

	for _, event := range []json.RawMessage{
		json.RawMessage(`{"method":"process/exited","params":{"processId":"80000000-0000-4000-8000-000000000008","seq":1,"exitCode":143,"sandboxDenied":false}}`),
		json.RawMessage(`{"method":"process/closed","params":{"processId":"80000000-0000-4000-8000-000000000008","seq":2}}`),
	} {
		sendAgentBusinessRPC(t, connection, fixture.config.WireLimits, agentSession, startRouting, event)
		if retained, err := startExchange.NextEvent(testDeadline(t)); err != nil || string(retained) != string(event) {
			t.Fatalf("terminal event = %s, %v", retained, err)
		}
		readAgentAck(t, connection, fixture.config.WireLimits, agentSession)
	}
	_ = connection.CloseNow()
}

func TestProcessCallTableRejectsRoutingMismatchAndOutputGap(t *testing.T) {
	table, err := newProcessCallTable(2, 4, 4096)
	if err != nil {
		t.Fatal(err)
	}
	holder := testProcessHolder()
	routing := testProcessRoutingContext()
	exchange, err := table.register(holder, ProcessDispatchRequest{
		ExecutorID: holder.ExecutorID, ExpectedConnectionGeneration: holder.Generation, Context: routing, RPC: testProcessStartRPC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	mismatched := routing
	mismatched.OperationID = "51000000-0000-4000-8000-000000000099"
	handled, err := table.handle(holder, agentxconn.Frame{Context: &mismatched, RPC: json.RawMessage(`{"id":"start-1","result":{"processId":"80000000-0000-4000-8000-000000000008"}}`)})
	if !handled || protocolErrorCode(err) != agentxconn.ErrorMutationConflict {
		t.Fatalf("routing mismatch = handled %t error %v", handled, err)
	}

	handled, err = table.handle(holder, agentxconn.Frame{Context: &routing, RPC: json.RawMessage(`{"method":"process/output","params":{"processId":"80000000-0000-4000-8000-000000000008","seq":2,"stream":"stdout","chunk":"YQ=="}}`)})
	if !handled || protocolErrorCode(err) != agentxconn.ErrorOutputGap {
		t.Fatalf("output gap = handled %t error %v", handled, err)
	}
	table.failHolder(holder, ErrConnectionFenced)
	if _, err := exchange.AwaitResponse(testDeadline(t)); !errors.Is(err, ErrConnectionFenced) {
		t.Fatalf("fenced process response error = %v", err)
	}
}

func TestProcessCallTableRequiresPreallocatedTimeoutRouting(t *testing.T) {
	table, err := newProcessCallTable(2, 4, 4096)
	if err != nil {
		t.Fatal(err)
	}
	holder := testProcessHolder()
	startRouting := testProcessRoutingContext()
	exchange, err := table.register(holder, ProcessDispatchRequest{
		ExecutorID: holder.ExecutorID, ExpectedConnectionGeneration: holder.Generation,
		Context: startRouting, Directives: testProcessTimeoutDirectives(), RPC: testProcessStartRPC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	dueRPC := json.RawMessage(`{"method":"agentx/timeoutDue","params":{"processId":"80000000-0000-4000-8000-000000000008"}}`)
	badRouting := testProcessTimeoutRoutingContext()
	badRouting.MutationKey = "62000000-0000-4000-8000-000000000099"
	if handled, err := table.handle(holder, agentxconn.Frame{Context: &badRouting, RPC: dueRPC}); !handled || protocolErrorCode(err) != agentxconn.ErrorMutationConflict {
		t.Fatalf("bad timeout routing = handled %t error %v", handled, err)
	}
	timeoutRouting := testProcessTimeoutRoutingContext()
	if handled, err := table.handle(holder, agentxconn.Frame{Context: &timeoutRouting, RPC: dueRPC}); !handled || err != nil {
		t.Fatalf("valid timeout routing = handled %t error %v", handled, err)
	}
	due, err := exchange.AwaitTimeoutDue(testDeadline(t))
	if err != nil || due.Context != timeoutRouting {
		t.Fatalf("retained timeout due = %+v, %v", due, err)
	}
	if handled, err := table.handle(holder, agentxconn.Frame{Context: &timeoutRouting, RPC: dueRPC}); !handled || protocolErrorCode(err) != agentxconn.ErrorSequenceConflict {
		t.Fatalf("duplicate timeout due = handled %t error %v", handled, err)
	}
	table.failHolder(holder, ErrConnectionFenced)
}

func sendAgentBusinessRPC(t *testing.T, connection *websocket.Conn, limits agentxconn.Limits, session *agentxconn.Session, routing agentxconn.RoutingContext, rpc json.RawMessage) {
	t.Helper()
	frame, err := session.Send(agentxconn.Payload{Type: agentxconn.MessageTypeRPC, Context: &routing, RPC: rpc})
	if err != nil {
		t.Fatal(err)
	}
	writeAgentxValue(t, connection, limits, frame)
}

func readAgentAck(t *testing.T, connection *websocket.Conn, limits agentxconn.Limits, session *agentxconn.Session) {
	t.Helper()
	message := readAgentxMessage(t, connection, limits)
	if message.Ack == nil {
		t.Fatalf("gateway business ACK = %+v", message)
	}
	if err := session.ReceiveAck(*message.Ack); err != nil {
		t.Fatal(err)
	}
}

func testProcessStartRPC() json.RawMessage {
	return json.RawMessage(`{"id":"start-1","method":"process/start","params":{"processId":"80000000-0000-4000-8000-000000000008","argv":["/usr/bin/printf","ok"],"cwd":"file:///workspace","env":{},"envPolicy":{"inherit":"none","ignoreDefaultExcludes":false,"exclude":[],"set":{},"includeOnly":[]},"tty":false,"pipeStdin":false,"arg0":null,"sandbox":{"permissions":{"type":"managed","file_system":{"type":"restricted","entries":[{"path":{"type":"special","value":{"kind":"root"}},"access":"read"},{"path":{"type":"path","path":"file:///workspace"},"access":"write"}]},"network":"restricted"},"cwd":"file:///workspace","workspaceRoots":["file:///workspace"],"windowsSandboxLevel":"restricted-token","windowsSandboxPrivateDesktop":false,"useLegacyLandlock":false},"enforceManagedNetwork":true}}`)
}

func testProcessTerminateRPC() json.RawMessage {
	return json.RawMessage(`{"id":"terminate-1","method":"process/terminate","params":{"processId":"80000000-0000-4000-8000-000000000008"}}`)
}

func testProcessTimeoutDirectives() *agentxconn.DispatchDirectives {
	return &agentxconn.DispatchDirectives{ProcessTimeout: &agentxconn.ProcessTimeoutDirective{
		AfterMillis: 60_000,
		OperationID: "52000000-0000-4000-8000-000000000005",
		MutationKey: "62000000-0000-4000-8000-000000000006",
	}}
}

func testProcessTimeoutRoutingContext() agentxconn.RoutingContext {
	routing := testProcessRoutingContext()
	routing.OperationID = testProcessTimeoutDirectives().ProcessTimeout.OperationID
	routing.MutationKey = testProcessTimeoutDirectives().ProcessTimeout.MutationKey
	return routing
}

func testProcessRoutingContext() agentxconn.RoutingContext {
	return agentxconn.RoutingContext{
		WorkspaceID:          "40000000-0000-4000-8000-000000000004",
		RunID:                "41000000-0000-4000-8000-000000000004",
		RunAttemptID:         "42000000-0000-4000-8000-000000000004",
		RunAttemptGeneration: 3,
		ExecutionID:          "50000000-0000-4000-8000-000000000005",
		OperationID:          "51000000-0000-4000-8000-000000000005",
		EnvID:                testEnvironmentID,
		MutationKey:          "61000000-0000-4000-8000-000000000006",
	}
}

func testProcessHolder() ConnectionHolder {
	return ConnectionHolder{
		ExecutorID:        testExecutorID,
		ConnectionID:      testConnectionID(90),
		SessionID:         "70000000-0000-4000-8000-000000000090",
		GatewayInstanceID: testGatewayInstanceID,
		Generation:        4,
		Status:            "online",
	}
}

func protocolErrorCode(err error) agentxconn.ErrorCode {
	var protocol *agentxconn.ProtocolError
	if errors.As(err, &protocol) {
		return protocol.Code
	}
	return ""
}
