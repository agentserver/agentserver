package runevent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
)

func TestCanonicalRunEventJSONSchemaAcceptsGoContractAndRejectsUnsafeKnownPayloads(t *testing.T) {
	rawSchema := readRunEventContract(t, "schema", "canonical-run-event.schema.json")
	var schema jsonschema.Schema
	if err := json.Unmarshal(rawSchema, &schema); err != nil {
		t.Fatalf("canonical run event schema is invalid JSON: %v", err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("resolve canonical run event schema: %v", err)
	}

	valid := []Event{
		contractEvent(KindAssistantMessageStarted, json.RawMessage(`{"messageId":"message-1","role":"assistant"}`)),
		contractEvent(KindToolCallProgress, json.RawMessage(`{"toolCallId":"call-1","progress":1,"total":2,"message":"running"}`)),
		contractEvent(KindToolCallResult, json.RawMessage(`{
            "messageId":"tool-message-1",
            "toolCallId":"call-1",
            "content":"ok",
            "presentation":{"kind":"command","command":{"command":"pwd","output":"/workspace","status":"succeeded"}}
        }`)),
		contractEvent(KindRunCompleted, json.RawMessage(`{"result":{"answer":42}}`)),
	}
	unknown := contractEvent("artifact.available", nil)
	unknown.SchemaVersion = 42
	unknown.Object = &ObjectPointer{
		ObjectID:  "70000000-0000-4000-8000-000000000007",
		SHA256:    strings.Repeat("a", 64),
		Size:      10,
		MediaType: "application/octet-stream",
	}
	valid = append(valid, unknown)
	for _, event := range valid {
		raw, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		if err := resolved.Validate(value); err != nil {
			t.Fatalf("schema rejected %s: %v\n%s", event.Kind, err, raw)
		}
	}

	base, err := json.Marshal(valid[2])
	if err != nil {
		t.Fatal(err)
	}
	invalid := []string{
		strings.Replace(string(base), `"schemaVersion":1`, `"schemaVersion":2`, 1),
		strings.Replace(string(base), `"content":"ok"`, `"content":"ok","future":true`, 1),
		strings.Replace(string(base), `"runAttemptGeneration":1`, `"runAttemptGeneration":null`, 1),
		strings.Replace(string(base), `"kind":"command"`, `"kind":"command","fileChange":{"files":[]}`, 1),
	}
	for _, raw := range invalid {
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			t.Fatal(err)
		}
		if err := resolved.Validate(value); err == nil {
			t.Fatalf("schema accepted unsafe known event: %s", raw)
		}
	}
}

func TestCanonicalEventsAsyncAPIDocumentsProjectionBoundaries(t *testing.T) {
	raw := readRunEventContract(t, "asyncapi", "canonical-events.yaml")
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
		Projection struct {
			CanonicalAuthority             string `json:"canonicalAuthority"`
			GatewayOwnsRunState            bool   `json:"gatewayOwnsRunState"`
			GatewayReadsPostgreSQL         bool   `json:"gatewayReadsPostgreSQL"`
			BrowserDisconnectCancelsRun    bool   `json:"browserDisconnectCancelsRun"`
			OnlyRunCompletedMapsToFinished bool   `json:"onlyRunCompletedMapsToRunFinished"`
			CursorIsAuthorization          bool   `json:"cursorIsAuthorization"`
			MembershipRecheckedPerPoll     bool   `json:"membershipRecheckedPerPoll"`
			PerEventCursors                bool   `json:"perEventCursors"`
			CursorResolution               string `json:"cursorResolution"`
			CursorPublication              string `json:"cursorPublication"`
			CursorCarrier                  string `json:"cursorCarrier"`
			ReconnectInput                 string `json:"reconnectInput"`
			ToolProgressCarrier            string `json:"toolProgressCarrier"`
			A2UIVersion                    string `json:"a2uiVersion"`
			A2UICarrier                    string `json:"a2uiCarrier"`
		} `json:"x-agentserver-projection"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("canonical-events.yaml must remain valid JSON (and therefore YAML): %v", err)
	}
	if document.AsyncAPI != "3.0.0" || document.Info.Version != "1.1.0" {
		t.Fatalf("AsyncAPI identity = %q/%q", document.AsyncAPI, document.Info.Version)
	}
	if got := document.Components.Messages["CanonicalRunEvent"].Payload.Reference; got != "../schema/canonical-run-event.schema.json" {
		t.Fatalf("canonical event schema reference = %q", got)
	}
	projection := document.Projection
	if projection.CanonicalAuthority != "agentserver-core" || projection.GatewayOwnsRunState || projection.GatewayReadsPostgreSQL ||
		projection.BrowserDisconnectCancelsRun || !projection.OnlyRunCompletedMapsToFinished ||
		projection.CursorIsAuthorization || !projection.MembershipRecheckedPerPoll || !projection.PerEventCursors ||
		projection.CursorResolution != "GET event cursor with limit=0 and waitMs=0" ||
		projection.CursorPublication != "initial run.queued, authorized snapshot rebase, and committed lifecycle-safe boundaries only" ||
		projection.CursorCarrier != "CUSTOM{name:agentserver.event_cursor,value:{version,runId,cursor,lastEventSequence}}" ||
		projection.ReconnectInput != "forwardedProps.agentserver.eventCursor" ||
		projection.ToolProgressCarrier != "CUSTOM{name:agentserver.tool_progress,value:{toolCallId,progress,total,message}}" ||
		projection.A2UIVersion != "v0.9" || projection.A2UICarrier != "CUSTOM{name:a2ui.operations,value:[operations]}" {
		t.Fatalf("AsyncAPI projection boundaries = %+v", projection)
	}
}

func contractEvent(kind string, payload json.RawMessage) Event {
	attemptID := "50000000-0000-4000-8000-000000000005"
	generation := int64(1)
	return Event{
		EventID:              "10000000-0000-4000-8000-000000000001",
		SchemaVersion:        CurrentSchemaVersion,
		Seq:                  1,
		WorkspaceID:          "20000000-0000-4000-8000-000000000002",
		SessionID:            "30000000-0000-4000-8000-000000000003",
		RunID:                "40000000-0000-4000-8000-000000000004",
		RunAttemptID:         &attemptID,
		RunAttemptGeneration: &generation,
		ProducerInstanceID:   "60000000-0000-4000-8000-000000000006",
		ProducerSeq:          1,
		Source:               "brain",
		Kind:                 kind,
		CreatedAt:            time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC),
		Payload:              payload,
	}
}

func readRunEventContract(t *testing.T, directory, name string) []byte {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate runevent package")
	}
	path := filepath.Join(filepath.Dir(currentFile), "..", "..", "api", directory, name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
