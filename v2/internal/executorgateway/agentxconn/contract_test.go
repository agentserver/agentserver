package agentxconn

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/execprofile"
	"github.com/google/jsonschema-go/jsonschema"
)

func TestAgentxEnvelopeSchemaValidatesGoWireExamples(t *testing.T) {
	rawSchema := readContractFile(t, "schema", "agentx-envelope.schema.json")
	var schema jsonschema.Schema
	if err := json.Unmarshal(rawSchema, &schema); err != nil {
		t.Fatalf("decode agentx envelope schema: %v", err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve agentx envelope schema: %v", err)
	}

	context := testRoutingContext()
	valid := []any{
		validHello(),
		Welcome{
			Type:                   MessageTypeWelcome,
			ProtocolVersion:        CurrentProtocolVersion,
			GatewayInstanceID:      testGatewayID,
			SessionID:              testSessionID,
			Generation:             7,
			ResumeStatus:           "fresh",
			ResumeWindowMillis:     ResumeWindowMillis,
			GatewaySentThrough:     0,
			GatewayReceivedThrough: 0,
		},
		Ack{Type: MessageTypeAck, SessionID: testSessionID, Generation: 7, Ack: 0},
		SessionError{Type: MessageTypeSessionError, Code: ErrorResumeRejected, Message: "gateway process restarted", Terminal: true},
		Frame{
			Type:       MessageTypeLifecycle,
			SessionID:  testSessionID,
			SessionSeq: 1,
			Ack:        0,
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
		},
		Frame{
			Type:       MessageTypeRPC,
			SessionID:  testSessionID,
			SessionSeq: 2,
			Ack:        1,
			Generation: 7,
			Context:    &context,
			RPC:        json.RawMessage(`{"id":"read-1","method":"process/read","params":{"processId":"70000000-0000-0000-0000-000000000007","afterSeq":0,"maxBytes":1024,"waitMs":0}}`),
		},
	}
	for _, example := range valid {
		raw, err := Encode(example, testWireLimits())
		if err != nil {
			t.Fatalf("Go example does not pass protocol validator: %v", err)
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
		`{"type":"ack","sessionId":"30000000-0000-0000-0000-000000000003","generation":7,"ack":0,"sessionSeq":1}`,
		`{"type":"rpc","sessionId":"30000000-0000-0000-0000-000000000003","sessionSeq":1,"ack":0,"generation":7,"context":{"workspaceId":"40000000-0000-0000-0000-000000000004","runId":"41000000-0000-0000-0000-000000000004","runAttemptId":"42000000-0000-0000-0000-000000000004","runAttemptGeneration":3,"executionId":"50000000-0000-0000-0000-000000000005","operationId":"51000000-0000-0000-0000-000000000005","envId":"60000000-0000-0000-0000-000000000006","mutationKey":"61000000-0000-0000-0000-000000000006"},"rpc":{"id":"signal-1","method":"process/signal","params":{"processId":"70000000-0000-0000-0000-000000000007","signal":"interrupt"}}}`,
		`{"type":"rpc","sessionId":"30000000-0000-0000-0000-000000000003","sessionSeq":1,"ack":0,"generation":7,"context":{"workspaceId":"40000000-0000-0000-0000-000000000004","runId":"41000000-0000-0000-0000-000000000004","runAttemptId":"42000000-0000-0000-0000-000000000004","runAttemptGeneration":3,"executionId":"50000000-0000-0000-0000-000000000005","operationId":"51000000-0000-0000-0000-000000000005","envId":"60000000-0000-0000-0000-000000000006","mutationKey":"61000000-0000-0000-0000-000000000006"},"rpc":{"jsonrpc":"2.0","id":"read-1","method":"process/read","params":{}}}`,
	}
	for _, raw := range invalid {
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			t.Fatal(err)
		}
		if err := resolved.Validate(value); err == nil {
			t.Fatalf("schema accepted invalid message: %s", raw)
		}
	}
}

func TestAgentxSchemaProfileAndErrorCodesMatchGo(t *testing.T) {
	raw := readContractFile(t, "schema", "agentx-envelope.schema.json")
	var document struct {
		Schema      string                     `json:"$schema"`
		ID          string                     `json:"$id"`
		Definitions map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if document.Schema != "https://json-schema.org/draft/2020-12/schema" || document.ID != "https://agentserver.dev/v2/schema/agentx-envelope.schema.json" {
		t.Fatalf("schema identity = %q / %q", document.Schema, document.ID)
	}
	var processMethods struct {
		PrefixItems []struct {
			Const string `json:"const"`
		} `json:"prefixItems"`
	}
	if err := json.Unmarshal(document.Definitions["processMethods"], &processMethods); err != nil {
		t.Fatal(err)
	}
	var methods []string
	for _, item := range processMethods.PrefixItems {
		methods = append(methods, item.Const)
	}
	if !slices.Equal(methods, execprofile.ProcessMethods()) {
		t.Fatalf("schema process methods = %q, Go profile = %q", methods, execprofile.ProcessMethods())
	}
	var cleanEnvPolicy struct {
		Properties map[string]struct {
			Const any `json:"const"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(document.Definitions["cleanEnvPolicy"], &cleanEnvPolicy); err != nil {
		t.Fatal(err)
	}
	if inherit := cleanEnvPolicy.Properties["inherit"].Const; inherit != "none" {
		t.Fatalf("schema clean env inheritance = %v, want none", inherit)
	}
	if ignore := cleanEnvPolicy.Properties["ignoreDefaultExcludes"].Const; ignore != false {
		t.Fatalf("schema ignoreDefaultExcludes = %v, want false", ignore)
	}
	var sandboxContext struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(document.Definitions["sandboxContext"], &sandboxContext); err != nil {
		t.Fatal(err)
	}
	windowsLevels := sandboxContext.Properties["windowsSandboxLevel"].Enum
	if !slices.Equal(windowsLevels, []string{"disabled", "restricted-token", "elevated"}) {
		t.Fatalf("schema Windows sandbox levels = %q", windowsLevels)
	}

	wantCodes := []ErrorCode{
		ErrorMalformedFrame,
		ErrorProtocolVersionUnsupported,
		ErrorMethodNotNegotiated,
		ErrorSessionMismatch,
		ErrorStaleGeneration,
		ErrorAckOutOfRange,
		ErrorAckRegression,
		ErrorSequenceConflict,
		ErrorResumeGap,
		ErrorOutputGap,
		ErrorBufferOverflow,
		ErrorResumeRejected,
		ErrorResumeExpired,
		ErrorJournalFull,
		ErrorMutationConflict,
		ErrorSessionClosed,
		ErrorAmbiguous,
	}
	var sessionError struct {
		Properties map[string]struct {
			Enum []ErrorCode `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(document.Definitions["sessionError"], &sessionError); err != nil {
		t.Fatal(err)
	}
	gotCodes := sessionError.Properties["code"].Enum
	sort.Slice(gotCodes, func(i, j int) bool { return gotCodes[i] < gotCodes[j] })
	sort.Slice(wantCodes, func(i, j int) bool { return wantCodes[i] < wantCodes[j] })
	if !slices.Equal(gotCodes, wantCodes) {
		t.Fatalf("schema error codes = %q, want %q", gotCodes, wantCodes)
	}
}

func TestAgentxAsyncAPIReferencesTheSingleEnvelopeContract(t *testing.T) {
	raw := readContractFile(t, "asyncapi", "agentx-wss.yaml")
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
			GatewayReplicas        int  `json:"gatewayReplicas"`
			ResumeWindowMillis     int  `json:"resumeWindowMs"`
			CrossProcessResume     bool `json:"crossProcessResume"`
			StandaloneAckSequenced bool `json:"standaloneAckSequenced"`
			TransportAckIsOpAck    bool `json:"transportAckIsOperationAck"`
		} `json:"x-agentserver-phase1"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("agentx-wss.yaml must remain valid JSON (and therefore YAML): %v", err)
	}
	if document.AsyncAPI != "3.0.0" || document.Info.Version != "2.0.0" {
		t.Fatalf("AsyncAPI identity = %q/%q", document.AsyncAPI, document.Info.Version)
	}
	wantReferences := map[string]string{
		"Hello":        "../schema/agentx-envelope.schema.json#/$defs/hello",
		"Welcome":      "../schema/agentx-envelope.schema.json#/$defs/welcome",
		"Lifecycle":    "../schema/agentx-envelope.schema.json#/$defs/lifecycleFrame",
		"RPC":          "../schema/agentx-envelope.schema.json#/$defs/businessFrame",
		"Ack":          "../schema/agentx-envelope.schema.json#/$defs/ack",
		"SessionError": "../schema/agentx-envelope.schema.json#/$defs/sessionError",
	}
	for name, reference := range wantReferences {
		if got := document.Components.Messages[name].Payload.Reference; got != reference {
			t.Fatalf("AsyncAPI %s payload = %q, want %q", name, got, reference)
		}
	}
	if document.Phase.GatewayReplicas != 1 || document.Phase.ResumeWindowMillis != ResumeWindowMillis || document.Phase.CrossProcessResume || document.Phase.StandaloneAckSequenced || document.Phase.TransportAckIsOpAck {
		t.Fatalf("AsyncAPI Phase 1 semantics = %+v", document.Phase)
	}
}

func readContractFile(t *testing.T, directory, name string) []byte {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate agentxconn package")
	}
	path := filepath.Join(filepath.Dir(currentFile), "..", "..", "..", "api", directory, name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
