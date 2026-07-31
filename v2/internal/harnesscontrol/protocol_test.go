package harnesscontrol

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const (
	testPoolInstanceID   = "10000000-0000-4000-8000-000000000001"
	testControlSessionID = "20000000-0000-4000-8000-000000000002"
	testWorkerInstanceID = "30000000-0000-4000-8000-000000000003"
	testWorkspaceID      = "40000000-0000-4000-8000-000000000004"
	testSessionID        = "41000000-0000-4000-8000-000000000004"
	testRunID            = "42000000-0000-4000-8000-000000000004"
	testRunAttemptID     = "43000000-0000-4000-8000-000000000004"
)

func TestHarnessControlCodecAcceptsExactDirectionBoundMessages(t *testing.T) {
	limits := testLimits()
	eventPayload := mustPayload(t, ThreadReadyEvent{Kind: EventKindThreadReady, ThreadID: "thread-1", Resumed: false})
	commandPayload := mustPayload(t, InterruptCommand{
		Kind: CommandKindInterrupt, Reason: "lease_lost", GraceMillis: 10_000, Message: "attempt lease was fenced",
	})
	values := []any{
		validHello(),
		Welcome{
			Type: MessageTypeWelcome, ProtocolVersion: CurrentProtocolVersion,
			PoolInstanceID: testPoolInstanceID, ControlSessionID: testControlSessionID,
			RunAttemptGeneration: 3, ResumeStatus: "fresh", ResumeWindowMillis: ResumeWindowMillis,
		},
		Frame{
			Type: MessageTypeEvent, ControlSessionID: testControlSessionID,
			SessionSeq: 1, Ack: 0, RunAttemptGeneration: 3, Payload: eventPayload,
		},
		Frame{
			Type: MessageTypeCommand, ControlSessionID: testControlSessionID,
			SessionSeq: 1, Ack: 1, RunAttemptGeneration: 3, Payload: commandPayload,
		},
		Ack{Type: MessageTypeAck, ControlSessionID: testControlSessionID, RunAttemptGeneration: 3, Ack: 1},
		SessionError{Type: MessageTypeSessionError, Code: ErrorResumeRejected, Message: "pool process changed", Terminal: true},
	}
	for _, value := range values {
		raw, err := Encode(value, limits)
		if err != nil {
			t.Fatalf("Encode(%T) error = %v", value, err)
		}
		decoded, err := Decode(raw, limits)
		if err != nil || decoded.Type == "" {
			t.Fatalf("Decode(%T) = %+v, %v", value, decoded, err)
		}
	}

	event := values[2].(Frame)
	if err := event.ValidateForReceiver(RolePool, limits); err != nil {
		t.Fatal(err)
	}
	if err := event.ValidateForReceiver(RoleWorker, limits); err == nil {
		t.Fatal("worker accepted a worker-originated event")
	}
	command := values[3].(Frame)
	if err := command.ValidateForReceiver(RoleWorker, limits); err != nil {
		t.Fatal(err)
	}
	if err := command.ValidateForReceiver(RolePool, limits); err == nil {
		t.Fatal("pool accepted a pool-originated command")
	}
}

func TestHarnessControlHelloResumeAndSafeCursors(t *testing.T) {
	hello := validHello()
	hello.Resume = &ResumeCursor{
		PoolInstanceID: testPoolInstanceID, ControlSessionID: testControlSessionID,
		RunAttemptGeneration: 3, WorkerSentThrough: 10, WorkerReceivedThrough: 4,
	}
	if err := hello.Validate(); err != nil {
		t.Fatal(err)
	}
	hello.Resume.RunAttemptGeneration = 2
	if err := hello.Validate(); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("resume generation mismatch error = %v", err)
	}
	hello = validHello()
	hello.Resume = &ResumeCursor{
		PoolInstanceID: testPoolInstanceID, ControlSessionID: testControlSessionID,
		RunAttemptGeneration: 3, WorkerSentThrough: maxSafeJSONInteger + 1,
	}
	if err := hello.Validate(); err == nil || !strings.Contains(err.Error(), "safe integer") {
		t.Fatalf("unsafe cursor error = %v", err)
	}
}

func TestHarnessControlRejectsUnknownMissingNullDuplicateAndOversized(t *testing.T) {
	limits := testLimits()
	validRaw, err := Encode(validHello(), limits)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "unknown", raw: strings.TrimSuffix(string(validRaw), "}") + `,"future":true}`, want: "unknown field"},
		{name: "missing", raw: `{"type":"ack","controlSessionId":"20000000-0000-4000-8000-000000000002","runAttemptGeneration":3}`, want: "required field"},
		{name: "null", raw: `{"type":"hello","protocolVersions":["1.1"],"workerInstanceId":"30000000-0000-4000-8000-000000000003","workspaceId":"40000000-0000-4000-8000-000000000004","sessionId":"41000000-0000-4000-8000-000000000004","runId":"42000000-0000-4000-8000-000000000004","runAttemptId":"43000000-0000-4000-8000-000000000004","runAttemptGeneration":3,"holderId":"pool-holder","manifestDigest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","resume":null}`, want: "cannot be null"},
		{name: "nested unknown", raw: `{"type":"hello","protocolVersions":["1.1"],"workerInstanceId":"30000000-0000-4000-8000-000000000003","workspaceId":"40000000-0000-4000-8000-000000000004","sessionId":"41000000-0000-4000-8000-000000000004","runId":"42000000-0000-4000-8000-000000000004","runAttemptId":"43000000-0000-4000-8000-000000000004","runAttemptGeneration":3,"holderId":"pool-holder","manifestDigest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","resume":{"poolInstanceId":"10000000-0000-4000-8000-000000000001","controlSessionId":"20000000-0000-4000-8000-000000000002","runAttemptGeneration":3,"workerSentThrough":1,"workerReceivedThrough":0,"future":true}}`, want: "unknown field"},
		{name: "nested missing", raw: `{"type":"hello","protocolVersions":["1.1"],"workerInstanceId":"30000000-0000-4000-8000-000000000003","workspaceId":"40000000-0000-4000-8000-000000000004","sessionId":"41000000-0000-4000-8000-000000000004","runId":"42000000-0000-4000-8000-000000000004","runAttemptId":"43000000-0000-4000-8000-000000000004","runAttemptGeneration":3,"holderId":"pool-holder","manifestDigest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","resume":{"poolInstanceId":"10000000-0000-4000-8000-000000000001","controlSessionId":"20000000-0000-4000-8000-000000000002","runAttemptGeneration":3,"workerSentThrough":1}}`, want: "required field"},
		{name: "duplicate", raw: `{"type":"ack","type":"ack","controlSessionId":"20000000-0000-4000-8000-000000000002","runAttemptGeneration":3,"ack":0}`, want: "duplicate"},
		{name: "payload array", raw: `{"type":"event","controlSessionId":"20000000-0000-4000-8000-000000000002","sessionSeq":1,"ack":0,"runAttemptGeneration":3,"payload":[]}`, want: "must be an object"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Decode([]byte(test.raw), limits)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Decode() error = %v, want %q", err, test.want)
			}
		})
	}
	tooSmall := limits
	tooSmall.MaxFrameBytes = len(validRaw) - 1
	if _, err := Decode(validRaw, tooSmall); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("oversized frame error = %v", err)
	}
}

func TestHarnessControlEventAndInterruptSemanticsFailClosed(t *testing.T) {
	limits := testLimits()
	validEvents := []any{
		ThreadReadyEvent{Kind: EventKindThreadReady, ThreadID: "thread-1", Resumed: true},
		TurnAcceptedEvent{Kind: EventKindTurnAccepted, ThreadID: "thread-1", TurnID: "turn-1"},
		AppServerNotificationEvent{
			Kind: EventKindAppServerNotification, Method: "item/agentMessage/delta",
			Params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","itemId":"message-1","delta":"hello"}`),
		},
		ExecutorMCPProgressEvent{
			Kind: EventKindExecutorMCPProgress, CallID: "call-1", Progress: 3, Total: 10, Message: "running",
		},
		TurnTerminalEvent{Kind: EventKindTurnTerminal, ThreadID: "thread-1", TurnID: "turn-1", Status: "completed"},
		TurnTerminalEvent{
			Kind: EventKindTurnTerminal, ThreadID: "thread-1", TurnID: "turn-1", Status: "failed",
			ErrorCode: "model_error", ErrorMessage: "model request failed",
		},
	}
	for _, value := range validEvents {
		if _, err := DecodeEventPayload(mustPayload(t, value), limits); err != nil {
			t.Fatalf("DecodeEventPayload(%T) error = %v", value, err)
		}
	}
	invalidEvents := []any{
		TurnTerminalEvent{Kind: EventKindTurnTerminal, ThreadID: "thread-1", TurnID: "turn-1", Status: "completed", ErrorCode: "unexpected", ErrorMessage: "bad"},
		TurnTerminalEvent{Kind: EventKindTurnTerminal, ThreadID: "thread-1", TurnID: "turn-1", Status: "failed"},
	}
	for _, value := range invalidEvents {
		if _, err := DecodeEventPayload(mustPayload(t, value), limits); err == nil {
			t.Fatalf("invalid event %T was accepted: %+v", value, value)
		}
	}
	invalidRuntimeEvents := []string{
		`{"kind":"app_server_notification","method":"item/started now","params":{}}`,
		`{"kind":"app_server_notification","method":"item/started","params":[]}`,
		`{"kind":"executor_mcp_progress","callId":"bad call","progress":1,"total":2}`,
		`{"kind":"executor_mcp_progress","callId":"call-1","progress":3,"total":2}`,
		`{"kind":"executor_mcp_progress","callId":"call-1","progress":-1,"total":2}`,
		`{"kind":"executor_mcp_progress","callId":"call-1","progress":1,"total":2,"message":null}`,
	}
	for _, raw := range invalidRuntimeEvents {
		if _, err := DecodeEventPayload([]byte(raw), limits); err == nil {
			t.Fatalf("invalid runtime event was accepted: %s", raw)
		}
	}
	if _, err := DecodeCommandPayload(mustPayload(t, InterruptCommand{
		Kind: CommandKindInterrupt, Reason: "retry", GraceMillis: 1000, Message: "try again",
	}), limits); err == nil || !strings.Contains(err.Error(), "not negotiated") {
		t.Fatalf("unsafe interrupt reason error = %v", err)
	}
}

func TestHarnessControlSessionErrorHidesInternalFailure(t *testing.T) {
	internal := SessionErrorFrom(errors.New("database password leaked"))
	if internal.Code != ErrorSessionClosed || strings.Contains(internal.Message, "password") || !internal.Terminal {
		t.Fatalf("internal session error projection = %+v", internal)
	}
	from, to := uint64(2), uint64(4)
	protocol := SessionErrorFrom(&ProtocolError{
		Code: ErrorSequenceGap, Message: "missing worker events", Terminal: true,
		LostFrom: &from, LostTo: &to,
	})
	if protocol.Code != ErrorSequenceGap || protocol.LostFrom == nil || *protocol.LostFrom != 2 {
		t.Fatalf("protocol session error projection = %+v", protocol)
	}
	from = 99
	if *protocol.LostFrom != 2 {
		t.Fatal("session error projection aliases protocol error lost range")
	}
}

func validHello() Hello {
	return Hello{
		Type: MessageTypeHello, ProtocolVersions: []string{CurrentProtocolVersion},
		WorkerInstanceID: testWorkerInstanceID, WorkspaceID: testWorkspaceID, SessionID: testSessionID,
		RunID: testRunID, RunAttemptID: testRunAttemptID, RunAttemptGeneration: 3,
		HolderID: "pool-holder", ManifestDigest: strings.Repeat("a", 64),
	}
}

func testLimits() Limits {
	return Limits{MaxFrameBytes: 2 * 1024 * 1024, MaxJSONValues: 65_536, MaxJSONDepth: 128}
}

func mustPayload(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
