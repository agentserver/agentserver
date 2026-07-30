package agentxconn

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/execprofile"
)

func TestDecodeHelloFreezesOuterProfileAndResumeIdentity(t *testing.T) {
	hello := validHello()
	raw, err := Encode(hello, testWireLimits())
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(raw, testWireLimits())
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Hello == nil || decoded.Hello.Environments[0].ProcessMethods[0] != "process/start" {
		t.Fatalf("decoded hello = %+v", decoded.Hello)
	}
	hello.Environments[0].OuterProfileVersion = execprofile.FilesystemReadVersion
	if _, err := Encode(hello, testWireLimits()); err != nil {
		t.Fatalf("filesystem-read environment profile rejected: %v", err)
	}
	hello.Environments[0].OuterProfileVersion = "filesystem-read-v1"
	if _, err := Encode(hello, testWireLimits()); codeOf(err) != ErrorMalformedFrame {
		t.Fatalf("partial filesystem environment profile error = %v", err)
	}

	hello = validHello()
	hello.Environments[0].ProcessMethods = append(hello.Environments[0].ProcessMethods, "process/signal")
	if _, err := Encode(hello, testWireLimits()); codeOf(err) != ErrorMalformedFrame {
		t.Fatalf("hello with process/signal error = %v", err)
	}

	hello = validHello()
	hello.Environments[0].ActiveProcesses = []ActiveProcess{{
		ProcessID:           "70000000-0000-0000-0000-000000000007",
		LocalExecInstanceID: "80000000-0000-0000-0000-000000000008",
	}}
	if _, err := Encode(hello, testWireLimits()); codeOf(err) != ErrorMalformedFrame {
		t.Fatalf("fresh hello claiming a process error = %v", err)
	}
	hello.Resume = &ResumeCursor{
		GatewayInstanceID:     testGatewayID,
		SessionID:             testSessionID,
		Generation:            7,
		AgentxSentThrough:     10,
		AgentxReceivedThrough: 11,
	}
	if _, err := Encode(hello, testWireLimits()); err != nil {
		t.Fatalf("resuming hello with active process rejected: %v", err)
	}
}

func TestDecodeRejectsDuplicateUnknownAndOversizedOuterFields(t *testing.T) {
	duplicate := []byte(`{"type":"ack","type":"ack","sessionId":"30000000-0000-0000-0000-000000000003","generation":1,"ack":0}`)
	if _, err := Decode(duplicate, testWireLimits()); codeOf(err) != ErrorMalformedFrame || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate key error = %v", err)
	}
	unknown := []byte(`{"type":"ack","sessionId":"30000000-0000-0000-0000-000000000003","generation":1,"ack":0,"future":true}`)
	if _, err := Decode(unknown, testWireLimits()); codeOf(err) != ErrorMalformedFrame || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown field error = %v", err)
	}
	missingAck := []byte(`{"type":"ack","sessionId":"30000000-0000-0000-0000-000000000003","generation":1}`)
	if _, err := Decode(missingAck, testWireLimits()); codeOf(err) != ErrorMalformedFrame || !strings.Contains(err.Error(), "ack") {
		t.Fatalf("missing required ack error = %v", err)
	}
	nullAck := []byte(`{"type":"ack","sessionId":"30000000-0000-0000-0000-000000000003","generation":1,"ack":null}`)
	if _, err := Decode(nullAck, testWireLimits()); codeOf(err) != ErrorMalformedFrame || !strings.Contains(err.Error(), "null") {
		t.Fatalf("null required ack error = %v", err)
	}
	helloRaw, err := json.Marshal(validHello())
	if err != nil {
		t.Fatal(err)
	}
	var helloObject map[string]any
	if err := json.Unmarshal(helloRaw, &helloObject); err != nil {
		t.Fatal(err)
	}
	environments := helloObject["environments"].([]any)
	delete(environments[0].(map[string]any), "insecureDev")
	helloRaw, err = json.Marshal(helloObject)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(helloRaw, testWireLimits()); codeOf(err) != ErrorMalformedFrame || !strings.Contains(err.Error(), "insecureDev") {
		t.Fatalf("missing nested required field error = %v", err)
	}
	environments[0].(map[string]any)["insecureDev"] = false
	helloObject["resume"] = nil
	helloRaw, err = json.Marshal(helloObject)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(helloRaw, testWireLimits()); codeOf(err) != ErrorMalformedFrame || !strings.Contains(err.Error(), "resume") {
		t.Fatalf("null resume error = %v", err)
	}
	limits := testWireLimits()
	limits.MaxFrameBytes = 32
	if _, err := Decode([]byte(strings.Repeat("x", 33)), limits); codeOf(err) != ErrorMalformedFrame {
		t.Fatalf("oversized frame error = %v", err)
	}
}

func TestBusinessRPCDirectionAndStockDialectAreFailClosed(t *testing.T) {
	context := testRoutingContext()
	valid := Frame{
		Type:       MessageTypeRPC,
		SessionID:  testSessionID,
		SessionSeq: 1,
		Ack:        0,
		Generation: 7,
		Context:    &context,
		RPC:        json.RawMessage(`{"id":"request-1","method":"process/read","params":{"processId":"70000000-0000-0000-0000-000000000007","afterSeq":0,"maxBytes":1024,"waitMs":0}}`),
	}
	if err := valid.ValidateForReceiver(RoleAgentx); err != nil {
		t.Fatalf("valid gateway process request rejected: %v", err)
	}
	filesystemRead := cloneFrame(valid)
	filesystemRead.RPC = json.RawMessage(`{"id":"fs-read-1","method":"agentx/fs/readFileBlock","params":{"path":"file:///workspace/data.bin","offset":0,"len":1048576}}`)
	if err := filesystemRead.ValidateForReceiver(RoleAgentx); err != nil {
		t.Fatalf("valid gateway filesystem read rejected: %v", err)
	}

	withJSONRPC := cloneFrame(valid)
	withJSONRPC.RPC = json.RawMessage(`{"jsonrpc":"2.0","id":"request-1","method":"process/read","params":{}}`)
	if err := withJSONRPC.ValidateForReceiver(RoleAgentx); codeOf(err) != ErrorMalformedFrame {
		t.Fatalf("stock dialect with jsonrpc error = %v", err)
	}

	signal := cloneFrame(valid)
	signal.RPC = json.RawMessage(`{"id":"request-2","method":"process/signal","params":{"processId":"70000000-0000-0000-0000-000000000007","signal":"interrupt"}}`)
	if err := signal.ValidateForReceiver(RoleAgentx); codeOf(err) != ErrorMethodNotNegotiated {
		t.Fatalf("process/signal direction error = %v", err)
	}

	wrongDirection := cloneFrame(valid)
	wrongDirection.RPC = json.RawMessage(`{"method":"process/output","params":{"processId":"70000000-0000-0000-0000-000000000007","seq":1,"stream":"stdout","chunk":"YQ=="}}`)
	if err := wrongDirection.ValidateForReceiver(RoleAgentx); codeOf(err) != ErrorMethodNotNegotiated {
		t.Fatalf("gateway notification direction error = %v", err)
	}
	if err := wrongDirection.ValidateForReceiver(RoleGateway); err != nil {
		t.Fatalf("agentx process notification rejected: %v", err)
	}

	reverse := cloneFrame(valid)
	reverse.RPC = json.RawMessage(`{"id":"policy-1","method":"network/policyRequest","params":{"processId":"70000000-0000-0000-0000-000000000007","request":{"protocol":"http","host":"example.com","port":80}}}`)
	if err := reverse.ValidateForReceiver(RoleGateway); err != nil {
		t.Fatalf("agentx reverse request rejected: %v", err)
	}
	if err := reverse.ValidateForReceiver(RoleAgentx); codeOf(err) != ErrorMethodNotNegotiated {
		t.Fatalf("gateway reverse request direction error = %v", err)
	}
}

func TestBusinessRPCMethodParamsAreFailClosed(t *testing.T) {
	context := testRoutingContext()
	frame := Frame{
		Type:       MessageTypeRPC,
		SessionID:  testSessionID,
		SessionSeq: 1,
		Generation: 7,
		Context:    &context,
	}

	valid := []struct {
		name     string
		receiver Role
		rpc      string
	}{
		{
			name:     "managed process start",
			receiver: RoleAgentx,
			rpc:      `{"id":"start-1","method":"process/start","params":{"processId":"70000000-0000-0000-0000-000000000007","argv":["/bin/echo","ok"],"cwd":"file:///workspace","env":{},"envPolicy":{"inherit":"none","ignoreDefaultExcludes":false,"exclude":[],"set":{},"includeOnly":[]},"tty":false,"pipeStdin":false,"arg0":null,"sandbox":{"permissions":{"type":"managed","file_system":{"type":"restricted","entries":[{"path":{"type":"special","value":{"kind":"minimal"}},"access":"read"},{"path":{"type":"path","path":"file:///workspace"},"access":"write"}]},"network":"restricted"},"cwd":"file:///workspace","workspaceRoots":["file:///workspace"],"windowsSandboxLevel":"restricted-token","windowsSandboxPrivateDesktop":false,"useLegacyLandlock":false},"enforceManagedNetwork":true}}`,
		},
		{
			name:     "process write",
			receiver: RoleAgentx,
			rpc:      `{"id":"write-1","method":"process/write","params":{"processId":"70000000-0000-0000-0000-000000000007","chunk":"YQ==","writeId":"write-1"}}`,
		},
		{
			name:     "bounded filesystem read",
			receiver: RoleAgentx,
			rpc:      `{"id":"fs-read-1","method":"agentx/fs/readFileBlock","params":{"path":"file:///workspace/data.bin","offset":9007199254740991,"len":1048576}}`,
		},
		{
			name:     "process output",
			receiver: RoleGateway,
			rpc:      `{"method":"process/output","params":{"processId":"70000000-0000-0000-0000-000000000007","seq":1,"stream":"stdout","chunk":"YQ=="}}`,
		},
		{
			name:     "process exited",
			receiver: RoleGateway,
			rpc:      `{"method":"process/exited","params":{"processId":"70000000-0000-0000-0000-000000000007","seq":2,"exitCode":0,"sandboxDenied":false}}`,
		},
	}
	for _, test := range valid {
		t.Run("valid "+test.name, func(t *testing.T) {
			candidate := cloneFrame(frame)
			candidate.RPC = json.RawMessage(test.rpc)
			if err := candidate.ValidateForReceiver(test.receiver); err != nil {
				t.Fatalf("valid RPC rejected: %v", err)
			}
		})
	}

	invalid := []struct {
		name     string
		receiver Role
		rpc      string
		wantCode ErrorCode
	}{
		{
			name:     "start inherits ambient env",
			receiver: RoleAgentx,
			rpc:      `{"id":"start-1","method":"process/start","params":{"processId":"70000000-0000-0000-0000-000000000007","argv":["/bin/echo"],"cwd":"file:///workspace","env":{},"envPolicy":{"inherit":"all","ignoreDefaultExcludes":false,"exclude":[],"set":{},"includeOnly":[]},"tty":false,"pipeStdin":false,"arg0":null,"sandbox":{"permissions":{"type":"managed","file_system":{"type":"restricted","entries":[{"path":{"type":"special","value":{"kind":"minimal"}},"access":"read"}]},"network":"restricted"},"cwd":"file:///workspace","workspaceRoots":["file:///workspace"],"windowsSandboxLevel":"disabled","windowsSandboxPrivateDesktop":false,"useLegacyLandlock":false},"enforceManagedNetwork":true}}`,
		},
		{
			name:     "old windows enum",
			receiver: RoleAgentx,
			rpc:      `{"id":"start-1","method":"process/start","params":{"processId":"70000000-0000-0000-0000-000000000007","argv":["/bin/echo"],"cwd":"file:///workspace","env":{},"envPolicy":{"inherit":"none","ignoreDefaultExcludes":false,"exclude":[],"set":{},"includeOnly":[]},"tty":false,"pipeStdin":false,"arg0":null,"sandbox":{"permissions":{"type":"managed","file_system":{"type":"restricted","entries":[{"path":{"type":"special","value":{"kind":"minimal"}},"access":"read"}]},"network":"restricted"},"cwd":"file:///workspace","workspaceRoots":["file:///workspace"],"windowsSandboxLevel":"standard","windowsSandboxPrivateDesktop":false,"useLegacyLandlock":false},"enforceManagedNetwork":true}}`,
		},
		{
			name:     "tagged sandbox union carries forbidden field",
			receiver: RoleAgentx,
			rpc:      `{"id":"start-1","method":"process/start","params":{"processId":"70000000-0000-0000-0000-000000000007","argv":["/bin/echo"],"cwd":"file:///workspace","env":{},"envPolicy":{"inherit":"none","ignoreDefaultExcludes":false,"exclude":[],"set":{},"includeOnly":[]},"tty":false,"pipeStdin":false,"arg0":null,"sandbox":{"permissions":{"type":"managed","file_system":{"type":"restricted","entries":[{"path":{"type":"special","path":"","value":{"kind":"minimal"}},"access":"read"}]},"network":"restricted"},"cwd":"file:///workspace","workspaceRoots":["file:///workspace"],"windowsSandboxLevel":"disabled","windowsSandboxPrivateDesktop":false,"useLegacyLandlock":false},"enforceManagedNetwork":true}}`,
		},
		{
			name:     "special root read",
			receiver: RoleAgentx,
			rpc:      `{"id":"start-1","method":"process/start","params":{"processId":"70000000-0000-0000-0000-000000000007","argv":["/bin/echo"],"cwd":"file:///workspace","env":{},"envPolicy":{"inherit":"none","ignoreDefaultExcludes":false,"exclude":[],"set":{},"includeOnly":[]},"tty":false,"pipeStdin":false,"arg0":null,"sandbox":{"permissions":{"type":"managed","file_system":{"type":"restricted","entries":[{"path":{"type":"special","value":{"kind":"root"}},"access":"read"}]},"network":"restricted"},"cwd":"file:///workspace","workspaceRoots":["file:///workspace"],"windowsSandboxLevel":"disabled","windowsSandboxPrivateDesktop":false,"useLegacyLandlock":false},"enforceManagedNetwork":true}}`,
		},
		{
			name:     "special minimal write",
			receiver: RoleAgentx,
			rpc:      `{"id":"start-1","method":"process/start","params":{"processId":"70000000-0000-0000-0000-000000000007","argv":["/bin/echo"],"cwd":"file:///workspace","env":{},"envPolicy":{"inherit":"none","ignoreDefaultExcludes":false,"exclude":[],"set":{},"includeOnly":[]},"tty":false,"pipeStdin":false,"arg0":null,"sandbox":{"permissions":{"type":"managed","file_system":{"type":"restricted","entries":[{"path":{"type":"special","value":{"kind":"minimal"}},"access":"write"}]},"network":"restricted"},"cwd":"file:///workspace","workspaceRoots":["file:///workspace"],"windowsSandboxLevel":"disabled","windowsSandboxPrivateDesktop":false,"useLegacyLandlock":false},"enforceManagedNetwork":true}}`,
		},
		{
			name:     "read missing field",
			receiver: RoleAgentx,
			rpc:      `{"id":"read-1","method":"process/read","params":{"processId":"70000000-0000-0000-0000-000000000007","afterSeq":0,"waitMs":0}}`,
		},
		{
			name:     "write invalid base64",
			receiver: RoleAgentx,
			rpc:      `{"id":"write-1","method":"process/write","params":{"processId":"70000000-0000-0000-0000-000000000007","chunk":"not-base64","writeId":"write-1"}}`,
		},
		{
			name:     "unbounded stock filesystem read",
			receiver: RoleAgentx,
			rpc:      `{"id":"fs-read-1","method":"fs/readFile","params":{"path":"file:///workspace/data.bin","sandbox":null}}`,
			wantCode: ErrorMethodNotNegotiated,
		},
		{
			name:     "filesystem read zero length",
			receiver: RoleAgentx,
			rpc:      `{"id":"fs-read-1","method":"agentx/fs/readFileBlock","params":{"path":"file:///workspace/data.bin","offset":0,"len":0}}`,
		},
		{
			name:     "filesystem read unsafe offset",
			receiver: RoleAgentx,
			rpc:      `{"id":"fs-read-1","method":"agentx/fs/readFileBlock","params":{"path":"file:///workspace/data.bin","offset":9007199254740992,"len":1}}`,
		},
		{
			name:     "notification zero sequence",
			receiver: RoleGateway,
			rpc:      `{"method":"process/closed","params":{"processId":"70000000-0000-0000-0000-000000000007","seq":0}}`,
		},
		{
			name:     "network unicode whitespace",
			receiver: RoleGateway,
			rpc:      `{"id":"policy-1","method":"network/policyRequest","params":{"processId":"70000000-0000-0000-0000-000000000007","request":{"protocol":"http","host":"example.com\u00a0evil","port":80}}}`,
		},
		{
			name:     "unknown stock envelope field",
			receiver: RoleAgentx,
			rpc:      `{"id":"read-1","method":"process/read","params":{"processId":"70000000-0000-0000-0000-000000000007","afterSeq":0,"maxBytes":1024,"waitMs":0},"future":true}`,
		},
	}
	for _, test := range invalid {
		t.Run("invalid "+test.name, func(t *testing.T) {
			candidate := cloneFrame(frame)
			candidate.RPC = json.RawMessage(test.rpc)
			wantCode := test.wantCode
			if wantCode == "" {
				wantCode = ErrorMalformedFrame
			}
			if err := candidate.ValidateForReceiver(test.receiver); codeOf(err) != wantCode {
				t.Fatalf("invalid RPC error = %v", err)
			}
		})
	}
}

func TestProcessTimeoutDirectiveAndDueNotificationAreOuterOnly(t *testing.T) {
	startContext := testRoutingContext()
	timeoutOperationID := "52000000-0000-0000-0000-000000000005"
	timeoutMutationKey := "62000000-0000-0000-0000-000000000006"
	start := Frame{
		Type:       MessageTypeRPC,
		SessionID:  testSessionID,
		SessionSeq: 1,
		Generation: 7,
		Context:    &startContext,
		Directives: &DispatchDirectives{ProcessTimeout: &ProcessTimeoutDirective{
			AfterMillis: 60_000,
			OperationID: timeoutOperationID,
			MutationKey: timeoutMutationKey,
		}},
		RPC: testProcessStartRPC(),
	}
	if err := start.ValidateForReceiver(RoleAgentx); err != nil {
		t.Fatalf("process/start timeout directive rejected: %v", err)
	}

	timeoutContext := startContext
	timeoutContext.OperationID = timeoutOperationID
	timeoutContext.MutationKey = timeoutMutationKey
	due := Frame{
		Type:       MessageTypeRPC,
		SessionID:  testSessionID,
		SessionSeq: 2,
		Generation: 7,
		Context:    &timeoutContext,
		RPC:        json.RawMessage(`{"method":"agentx/timeoutDue","params":{"processId":"70000000-0000-0000-0000-000000000007"}}`),
	}
	if err := due.ValidateForReceiver(RoleGateway); err != nil {
		t.Fatalf("agentx timeout-due notification rejected: %v", err)
	}
	if err := due.ValidateForReceiver(RoleAgentx); codeOf(err) != ErrorMethodNotNegotiated {
		t.Fatalf("gateway was allowed to send timeout-due notification: %v", err)
	}

	read := cloneFrame(start)
	read.RPC = json.RawMessage(`{"id":"read-1","method":"process/read","params":{"processId":"70000000-0000-0000-0000-000000000007","afterSeq":0,"maxBytes":1024,"waitMs":0}}`)
	if err := read.ValidateForReceiver(RoleAgentx); codeOf(err) != ErrorMalformedFrame {
		t.Fatalf("non-start directive error = %v", err)
	}
	sameOperation := cloneFrame(start)
	sameOperation.Directives.ProcessTimeout.OperationID = startContext.OperationID
	if err := sameOperation.ValidateForReceiver(RoleAgentx); codeOf(err) != ErrorMalformedFrame {
		t.Fatalf("reused timeout operation error = %v", err)
	}
	tooLong := cloneFrame(start)
	tooLong.Directives.ProcessTimeout.AfterMillis = maxProcessTimeoutMS + 1
	if err := tooLong.ValidateForReceiver(RoleAgentx); codeOf(err) != ErrorMalformedFrame {
		t.Fatalf("unbounded timeout directive error = %v", err)
	}
	missingTimeout := cloneFrame(start)
	missingTimeout.Directives.ProcessTimeout = nil
	if err := missingTimeout.ValidateForReceiver(RoleAgentx); codeOf(err) != ErrorMalformedFrame {
		t.Fatalf("empty timeout directives error = %v", err)
	}
}

func TestRemoteLifecycleIsSeparateFromStockChildDialect(t *testing.T) {
	initialize := Frame{
		Type:       MessageTypeLifecycle,
		SessionID:  testSessionID,
		SessionSeq: 1,
		Generation: 7,
		RPC: json.RawMessage(`{
  "jsonrpc":"2.0",
  "id":"init-1",
  "method":"initialize",
  "params":{
    "protocolVersion":"2.0",
    "clientName":"agentserver-executor-gateway",
    "outerProfileVersion":"process-v1",
    "processMethods":["process/start","process/read","process/write","process/terminate"]
  }
}`),
	}
	if err := initialize.ValidateForReceiver(RoleAgentx); err != nil {
		t.Fatalf("remote initialize rejected: %v", err)
	}
	asBusiness := cloneFrame(initialize)
	asBusiness.Type = MessageTypeRPC
	context := testRoutingContext()
	asBusiness.Context = &context
	if err := asBusiness.ValidateForReceiver(RoleAgentx); codeOf(err) != ErrorMalformedFrame {
		t.Fatalf("remote lifecycle accepted as stock RPC: %v", err)
	}

	result := Frame{
		Type:       MessageTypeLifecycle,
		SessionID:  testSessionID,
		SessionSeq: 1,
		Generation: 7,
		RPC: json.RawMessage(`{
  "jsonrpc":"2.0",
  "id":"init-1",
  "result":{
    "sessionId":"30000000-0000-0000-0000-000000000003",
    "protocolVersion":"2.0",
    "serverName":"agentx",
    "outerProfileVersion":"process-v1",
    "processMethods":["process/start","process/read","process/write","process/terminate"]
  }
}`),
	}
	if err := result.ValidateForReceiver(RoleGateway); err != nil {
		t.Fatalf("remote initialize result rejected: %v", err)
	}
	context = testRoutingContext()
	result.Context = &context
	if err := result.ValidateForReceiver(RoleGateway); codeOf(err) != ErrorMalformedFrame {
		t.Fatalf("lifecycle routing context error = %v", err)
	}

	initializedWithNullParams := Frame{
		Type:       MessageTypeLifecycle,
		SessionID:  testSessionID,
		SessionSeq: 2,
		Generation: 7,
		RPC:        json.RawMessage(`{"jsonrpc":"2.0","method":"initialized","params":null}`),
	}
	if err := initializedWithNullParams.ValidateForReceiver(RoleAgentx); codeOf(err) != ErrorMalformedFrame {
		t.Fatalf("initialized null params error = %v", err)
	}
}

func TestSessionErrorLostRangeIsOnlyForGapOrOverflow(t *testing.T) {
	from, to := uint64(4), uint64(9)
	value := SessionError{
		Type:     MessageTypeSessionError,
		Code:     ErrorResumeGap,
		Message:  "missing retained frames",
		Terminal: true,
		LostFrom: &from,
		LostTo:   &to,
	}
	if _, err := Encode(value, testWireLimits()); err != nil {
		t.Fatal(err)
	}
	value.Code = ErrorResumeRejected
	if _, err := Encode(value, testWireLimits()); codeOf(err) != ErrorMalformedFrame {
		t.Fatalf("non-gap lost range error = %v", err)
	}
}

func validHello() Hello {
	return Hello{
		Type:                     MessageTypeHello,
		ConnectionID:             "90000000-0000-0000-0000-000000000009",
		ProtocolVersions:         []string{CurrentProtocolVersion},
		AgentxVersion:            "2.0.0-test",
		RuntimeManifestSHA256:    strings.Repeat("a", 64),
		ExecProtocolSourceSHA256: strings.Repeat("b", 64),
		Environments: []HelloEnvironment{{
			EnvID:               "60000000-0000-0000-0000-000000000006",
			Platform:            "darwin-arm64",
			CodexRelease:        "0.146.0",
			CodexCommit:         strings.Repeat("c", 40),
			CodexSHA256:         strings.Repeat("d", 64),
			OuterProfileVersion: execprofile.Version,
			ProcessMethods:      execprofile.ProcessMethods(),
			ActiveProcesses:     []ActiveProcess{},
		}},
	}
}

func testWireLimits() Limits {
	return Limits{MaxFrameBytes: 64 * 1024, MaxJSONValues: 4096, MaxJSONDepth: 64}
}

func testProcessStartRPC() json.RawMessage {
	return json.RawMessage(`{"id":"start-1","method":"process/start","params":{"processId":"70000000-0000-0000-0000-000000000007","argv":["/bin/echo","ok"],"cwd":"file:///workspace","env":{},"envPolicy":{"inherit":"none","ignoreDefaultExcludes":false,"exclude":[],"set":{},"includeOnly":[]},"tty":false,"pipeStdin":false,"arg0":null,"sandbox":{"permissions":{"type":"managed","file_system":{"type":"restricted","entries":[{"path":{"type":"path","path":"file:///workspace"},"access":"write"}]},"network":"restricted"},"cwd":"file:///workspace","workspaceRoots":["file:///workspace"],"windowsSandboxLevel":"restricted-token","windowsSandboxPrivateDesktop":false,"useLegacyLandlock":false},"enforceManagedNetwork":true}}`)
}

func codeOf(err error) ErrorCode {
	var protocol *ProtocolError
	if errors.As(err, &protocol) {
		return protocol.Code
	}
	return ""
}
