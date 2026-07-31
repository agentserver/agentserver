package harnesscontrol

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

func TestHarnessControlJSONSchemaAcceptsGoContractAndRejectsUnsafeShapes(t *testing.T) {
	rawSchema := readHarnessContractFile(t, "schema", "harness-control.schema.json")
	var schema jsonschema.Schema
	if err := json.Unmarshal(rawSchema, &schema); err != nil {
		t.Fatalf("harness control schema is invalid JSON: %v", err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve harness control schema: %v", err)
	}

	limits := testLimits()
	resumeHello := validHello()
	resumeHello.Resume = &ResumeCursor{
		PoolInstanceID: testPoolInstanceID, ControlSessionID: testControlSessionID,
		RunAttemptGeneration: 3, WorkerSentThrough: 3, WorkerReceivedThrough: 2,
	}
	valid := []any{
		validHello(),
		resumeHello,
		Welcome{
			Type: MessageTypeWelcome, ProtocolVersion: CurrentProtocolVersion,
			PoolInstanceID: testPoolInstanceID, ControlSessionID: testControlSessionID,
			RunAttemptGeneration: 3, ResumeStatus: "fresh", ResumeWindowMillis: ResumeWindowMillis,
		},
		Welcome{
			Type: MessageTypeWelcome, ProtocolVersion: CurrentProtocolVersion,
			PoolInstanceID: testPoolInstanceID, ControlSessionID: testControlSessionID,
			RunAttemptGeneration: 3, ResumeStatus: "resumed", ResumeWindowMillis: ResumeWindowMillis,
			PoolSentThrough: 2, PoolReceivedThrough: 3,
		},
		Frame{
			Type: MessageTypeEvent, ControlSessionID: testControlSessionID, SessionSeq: 1,
			RunAttemptGeneration: 3,
			Payload:              mustPayload(t, ThreadReadyEvent{Kind: EventKindThreadReady, ThreadID: "thread-1", Resumed: false}),
		},
		Frame{
			Type: MessageTypeEvent, ControlSessionID: testControlSessionID, SessionSeq: 2, Ack: 1,
			RunAttemptGeneration: 3,
			Payload:              mustPayload(t, TurnAcceptedEvent{Kind: EventKindTurnAccepted, ThreadID: "thread-1", TurnID: "turn-1"}),
		},
		Frame{
			Type: MessageTypeEvent, ControlSessionID: testControlSessionID, SessionSeq: 3, Ack: 1,
			RunAttemptGeneration: 3,
			Payload: mustPayload(t, AppServerNotificationEvent{
				Kind: EventKindAppServerNotification, Method: "item/agentMessage/delta",
				Params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","itemId":"message-1","delta":"hello"}`),
			}),
		},
		Frame{
			Type: MessageTypeEvent, ControlSessionID: testControlSessionID, SessionSeq: 4, Ack: 1,
			RunAttemptGeneration: 3,
			Payload: mustPayload(t, ExecutorMCPProgressEvent{
				Kind: EventKindExecutorMCPProgress, CallID: "call-1", Progress: 1, Total: 2,
			}),
		},
		Frame{
			Type: MessageTypeEvent, ControlSessionID: testControlSessionID, SessionSeq: 5, Ack: 1,
			RunAttemptGeneration: 3,
			Payload: mustPayload(t, TurnTerminalEvent{
				Kind: EventKindTurnTerminal, ThreadID: "thread-1", TurnID: "turn-1", Status: "completed",
				RolloutLocator: testRolloutLocator,
			}),
		},
		Frame{
			Type: MessageTypeEvent, ControlSessionID: testControlSessionID, SessionSeq: 6, Ack: 1,
			RunAttemptGeneration: 3,
			Payload: mustPayload(t, TurnTerminalEvent{
				Kind: EventKindTurnTerminal, ThreadID: "thread-1", TurnID: "turn-1", Status: "failed",
				ErrorCode: "model_error", ErrorMessage: "model request failed",
			}),
		},
		Frame{
			Type: MessageTypeCommand, ControlSessionID: testControlSessionID, SessionSeq: 1, Ack: 3,
			RunAttemptGeneration: 3,
			Payload: mustPayload(t, InterruptCommand{
				Kind: CommandKindInterrupt, Reason: "lease_lost", GraceMillis: 10_000,
				Message: "attempt lease was fenced",
			}),
		},
		Ack{Type: MessageTypeAck, ControlSessionID: testControlSessionID, RunAttemptGeneration: 3, Ack: 3},
		SessionError{
			Type: MessageTypeSessionError, Code: ErrorSequenceGap, Message: "worker event gap", Terminal: true,
			LostFrom: uint64Pointer(2), LostTo: uint64Pointer(4),
		},
	}
	for _, example := range valid {
		raw, err := Encode(example, limits)
		if err != nil {
			t.Fatalf("Go example %T does not pass protocol validator: %v", example, err)
		}
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		if err := resolved.Validate(value); err != nil {
			t.Fatalf("schema rejected %T: %v\n%s", example, err, raw)
		}
	}

	invalid := []string{
		`{"type":"welcome","protocolVersion":"1.1","poolInstanceId":"10000000-0000-4000-8000-000000000001","controlSessionId":"20000000-0000-4000-8000-000000000002","runAttemptGeneration":3,"resumeStatus":"fresh","resumeWindowMs":30000,"poolSentThrough":1,"poolReceivedThrough":0}`,
		`{"type":"event","controlSessionId":"20000000-0000-4000-8000-000000000002","sessionSeq":1,"ack":0,"runAttemptGeneration":3,"payload":{"kind":"interrupt","reason":"fenced","graceMs":1000,"message":"fenced"}}`,
		`{"type":"event","controlSessionId":"20000000-0000-4000-8000-000000000002","sessionSeq":1,"ack":0,"runAttemptGeneration":3,"payload":{"kind":"turn_terminal","threadId":"thread-1","turnId":"turn-1","status":"failed"}}`,
		`{"type":"event","controlSessionId":"20000000-0000-4000-8000-000000000002","sessionSeq":1,"ack":0,"runAttemptGeneration":3,"payload":{"kind":"turn_terminal","threadId":"thread-1","turnId":"turn-1","status":"completed"}}`,
		`{"type":"event","controlSessionId":"20000000-0000-4000-8000-000000000002","sessionSeq":1,"ack":0,"runAttemptGeneration":3,"payload":{"kind":"turn_terminal","threadId":"thread-1","turnId":"turn-1","status":"completed","errorCode":"bad","errorMessage":"bad"}}`,
		`{"type":"event","controlSessionId":"20000000-0000-4000-8000-000000000002","sessionSeq":1,"ack":0,"runAttemptGeneration":3,"payload":{"kind":"turn_terminal","threadId":"thread-1","turnId":"turn-1","status":"failed","rolloutLocator":"sessions/rollout.jsonl","errorCode":"bad","errorMessage":"bad"}}`,
		`{"type":"event","controlSessionId":"20000000-0000-4000-8000-000000000002","sessionSeq":1,"ack":0,"runAttemptGeneration":3,"payload":{"kind":"app_server_notification","method":"item/started now","params":{}}}`,
		`{"type":"event","controlSessionId":"20000000-0000-4000-8000-000000000002","sessionSeq":1,"ack":0,"runAttemptGeneration":3,"payload":{"kind":"executor_mcp_progress","callId":"bad call","progress":1,"total":2}}`,
		`{"type":"ack","controlSessionId":"20000000-0000-4000-8000-000000000002","runAttemptGeneration":3,"ack":0,"sessionSeq":1}`,
		`{"type":"session_error","code":"sequence_gap","message":"gap","terminal":true}`,
		`{"type":"session_error","code":"session_closed","message":"closed","terminal":true,"lostFrom":1,"lostTo":2}`,
		`{"type":"ack","controlSessionId":"20000000-0000-4000-8000-000000000002","runAttemptGeneration":9007199254740992,"ack":0}`,
	}
	for _, raw := range invalid {
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			t.Fatal(err)
		}
		if err := resolved.Validate(value); err == nil {
			t.Fatalf("schema accepted unsafe message: %s", raw)
		}
	}
}

func TestHarnessControlSchemaConstantsMatchGoProtocol(t *testing.T) {
	raw := readHarnessContractFile(t, "schema", "harness-control.schema.json")
	var document struct {
		Schema      string                     `json:"$schema"`
		ID          string                     `json:"$id"`
		Definitions map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if document.Schema != "https://json-schema.org/draft/2020-12/schema" || document.ID != "https://agentserver.dev/v2/schema/harness-control.schema.json" {
		t.Fatalf("schema identity = %q / %q", document.Schema, document.ID)
	}
	var welcomeDefinition struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(document.Definitions["welcome"], &welcomeDefinition); err != nil {
		t.Fatal(err)
	}
	var protocolVersion struct {
		Const string `json:"const"`
	}
	if err := json.Unmarshal(welcomeDefinition.Properties["protocolVersion"], &protocolVersion); err != nil {
		t.Fatal(err)
	}
	if version := protocolVersion.Const; version != CurrentProtocolVersion {
		t.Fatalf("schema protocol version = %q, Go = %q", version, CurrentProtocolVersion)
	}
	wantCodes := []ErrorCode{
		ErrorMalformedFrame, ErrorProtocolVersionUnsupported, ErrorAttemptMismatch, ErrorStaleGeneration,
		ErrorSequenceConflict, ErrorSequenceGap, ErrorAckOutOfRange, ErrorAckRegression,
		ErrorResumeRejected, ErrorResumeExpired, ErrorJournalFull, ErrorBufferOverflow, ErrorSessionClosed,
	}
	var sessionErrorDefinition struct {
		Properties map[string]struct {
			Enum []ErrorCode `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(document.Definitions["sessionError"], &sessionErrorDefinition); err != nil {
		t.Fatal(err)
	}
	gotCodes := sessionErrorDefinition.Properties["code"].Enum
	sort.Slice(gotCodes, func(i, j int) bool { return gotCodes[i] < gotCodes[j] })
	sort.Slice(wantCodes, func(i, j int) bool { return wantCodes[i] < wantCodes[j] })
	if !slices.Equal(gotCodes, wantCodes) {
		t.Fatalf("schema error codes = %q, Go = %q", gotCodes, wantCodes)
	}
}

func TestHarnessControlAsyncAPIReferencesSingleSchemaAndPhaseOneSemantics(t *testing.T) {
	raw := readHarnessContractFile(t, "asyncapi", "harness-control.yaml")
	var document struct {
		AsyncAPI string `json:"asyncapi"`
		Info     struct {
			Version string `json:"version"`
		} `json:"info"`
		Components struct {
			Messages map[string]struct {
				Payload struct {
					Reference string `json:"$ref"`
				} `json:"payload"`
			} `json:"messages"`
		} `json:"components"`
		Phase struct {
			ResumeWindowMillis               int    `json:"resumeWindowMs"`
			CrossProcessResume               bool   `json:"crossProcessResume"`
			HolderRoutedEndpoint             bool   `json:"holderRoutedEndpoint"`
			StandaloneAckSequenced           bool   `json:"standaloneAckSequenced"`
			TransportAckAuthorizesTransition bool   `json:"transportAckAuthorizesCoreTransition"`
			RuntimeEventAckAfterCoreCommit   bool   `json:"runtimeEventAckAfterCoreCommit"`
			TerminalAckCoversPriorRuntime    bool   `json:"terminalAckCoversPriorRuntime"`
			CompletedTerminalCarriesLocator  bool   `json:"completedTerminalCarriesRolloutLocator"`
			Authentication                   string `json:"authentication"`
			WorkerHasDurableState            bool   `json:"workerHasDurableState"`
		} `json:"x-agentserver-phase1"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("harness-control.yaml must remain valid JSON (and therefore YAML): %v", err)
	}
	if document.AsyncAPI != "3.0.0" || document.Info.Version != "1.2.0" {
		t.Fatalf("AsyncAPI identity = %q/%q", document.AsyncAPI, document.Info.Version)
	}
	wantReferences := map[string]string{
		"Hello":        "../schema/harness-control.schema.json#/$defs/hello",
		"Welcome":      "../schema/harness-control.schema.json#/$defs/welcome",
		"Event":        "../schema/harness-control.schema.json#/$defs/eventFrame",
		"Command":      "../schema/harness-control.schema.json#/$defs/commandFrame",
		"Ack":          "../schema/harness-control.schema.json#/$defs/ack",
		"SessionError": "../schema/harness-control.schema.json#/$defs/sessionError",
	}
	for name, reference := range wantReferences {
		if got := document.Components.Messages[name].Payload.Reference; got != reference {
			t.Fatalf("AsyncAPI %s payload = %q, want %q", name, got, reference)
		}
	}
	phase := document.Phase
	if phase.ResumeWindowMillis != ResumeWindowMillis || phase.CrossProcessResume || !phase.HolderRoutedEndpoint ||
		phase.StandaloneAckSequenced || phase.TransportAckAuthorizesTransition || !phase.RuntimeEventAckAfterCoreCommit ||
		!phase.TerminalAckCoversPriorRuntime || !phase.CompletedTerminalCarriesLocator ||
		phase.Authentication != "worker mTLS AND per-attempt bearer" || phase.WorkerHasDurableState {
		t.Fatalf("AsyncAPI Phase 1 semantics = %+v", phase)
	}
}

func readHarnessContractFile(t *testing.T, directory, name string) []byte {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate harnesscontrol package")
	}
	path := filepath.Join(filepath.Dir(currentFile), "..", "..", "api", directory, name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func uint64Pointer(value uint64) *uint64 { return &value }
