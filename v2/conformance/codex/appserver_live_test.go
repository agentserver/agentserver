package codex_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/conformance/codex/internal/scriptedmodel"
	"github.com/agentserver/agentserver/v2/internal/codexwire"
)

const (
	conformanceModelName = "agentserver-v2-scripted-model"
	conformanceUserText  = "complete the deterministic phase-zero lifecycle"
	conformanceFinalText = "scripted lifecycle complete"
)

func TestAppServerA01ScriptedModelLifecycle(t *testing.T) {
	binary, paths := prepareLiveCodex(t)
	response, err := scriptedmodel.AssistantMessage("response-a01", "message-a01", conformanceFinalText)
	if err != nil {
		t.Fatal(err)
	}
	modelServer, err := scriptedmodel.Start(scriptedmodel.Config{
		Responses: []scriptedmodel.Response{response},
	})
	if err != nil {
		t.Fatalf("start loopback scripted model: %v", err)
	}
	t.Cleanup(modelServer.Close)

	writeScriptedModelConfig(t, paths.codexHome, modelServer.URL())
	process := startPreparedLiveCodex(t, binary, paths, "app-server", "--listen", "stdio://", "--strict-config")
	initialize := initializeAppServer(t, process)
	assertSamePath(t, initialize.CodexHome, paths.codexHome)
	collector := newRPCCollector(process)

	sendRPC(t, process, map[string]any{
		"id":     2,
		"method": "thread/start",
		"params": map[string]any{
			"model":                   conformanceModelName,
			"cwd":                     paths.cwd,
			"approvalPolicy":          "never",
			"sandbox":                 "read-only",
			"baseInstructions":        "Return only the scripted model result.",
			"developerInstructions":   "This is a deterministic conformance turn.",
			"ephemeral":               false,
			"threadSource":            "user",
			"environments":            []any{},
			"dynamicTools":            []any{},
			"selectedCapabilityRoots": []any{},
		},
	})
	threadResponse := collector.response(t, "2")
	thread := decodeThreadStart(t, threadResponse)
	if thread.Model != conformanceModelName || thread.ModelProvider != "scripted_provider" {
		t.Fatalf("unexpected model selection: model=%q provider=%q", thread.Model, thread.ModelProvider)
	}
	if thread.Thread.ID == "" || thread.Thread.SessionID == "" {
		t.Fatalf("thread/start returned incomplete identity: %+v", thread.Thread)
	}
	if thread.Thread.Ephemeral || thread.Thread.Status.Type != "idle" || thread.Thread.ThreadSource != "user" {
		t.Fatalf("thread/start returned unexpected thread state: %+v", thread.Thread)
	}
	assertSamePath(t, thread.CWD, paths.cwd)
	assertSamePath(t, thread.Thread.CWD, paths.cwd)

	threadStartedMessage := collector.notification(t, "thread/started")
	var threadStarted struct {
		Thread appServerThread `json:"thread"`
	}
	if err := threadStartedMessage.DecodeParams(&threadStarted); err != nil {
		t.Fatal(err)
	}
	if threadStarted.Thread.ID != thread.Thread.ID || threadStarted.Thread.SessionID != thread.Thread.SessionID {
		t.Fatalf("thread/started identity differs from response: response=%+v notification=%+v", thread.Thread, threadStarted.Thread)
	}

	sendRPC(t, process, map[string]any{
		"id":     3,
		"method": "turn/start",
		"params": map[string]any{
			"threadId": thread.Thread.ID,
			"input": []any{
				map[string]any{
					"type":         "text",
					"text":         conformanceUserText,
					"textElements": []any{},
				},
			},
		},
	})
	turnResponse := collector.response(t, "3")
	turn := decodeTurnStart(t, turnResponse)
	if turn.ID == "" || turn.Status != "inProgress" {
		t.Fatalf("turn/start returned unexpected turn: %+v", turn)
	}

	turnStartedMessage := collector.notification(t, "turn/started")
	var turnStarted struct {
		ThreadID string        `json:"threadId"`
		Turn     appServerTurn `json:"turn"`
	}
	if err := turnStartedMessage.DecodeParams(&turnStarted); err != nil {
		t.Fatal(err)
	}
	if turnStarted.ThreadID != thread.Thread.ID || turnStarted.Turn.ID != turn.ID || turnStarted.Turn.Status != "inProgress" {
		t.Fatalf("unexpected turn/started: %+v", turnStarted)
	}
	assertAgentItemCompleted(t, collector, thread.Thread.ID, turn.ID, conformanceFinalText)

	turnCompletedMessage := collector.notification(t, "turn/completed")
	var turnCompleted struct {
		ThreadID string        `json:"threadId"`
		Turn     appServerTurn `json:"turn"`
	}
	if err := turnCompletedMessage.DecodeParams(&turnCompleted); err != nil {
		t.Fatal(err)
	}
	if turnCompleted.ThreadID != thread.Thread.ID || turnCompleted.Turn.ID != turn.ID {
		t.Fatalf("turn/completed identity mismatch: %+v", turnCompleted)
	}
	assertScriptedModelRequest(t, modelServer)
	if turnCompleted.Turn.Status != "completed" || turnCompleted.Turn.Error != nil || turnCompleted.Turn.ItemsView != "notLoaded" || len(turnCompleted.Turn.Items) != 0 {
		t.Fatalf("unexpected terminal turn: %+v", turnCompleted.Turn)
	}

	closeAndWait(t, process)
}

func TestAppServerA02EnvironmentsRequiresExperimentalAPI(t *testing.T) {
	binary, paths := prepareLiveCodex(t)
	// The request must be rejected during protocol gating, before any model
	// request. A closed loopback port makes accidental I/O fail closed.
	writeScriptedModelConfig(t, paths.codexHome, "http://127.0.0.1:1")
	process := startPreparedLiveCodex(t, binary, paths, "app-server", "--listen", "stdio://", "--strict-config")
	initializeAppServerWithExperimental(t, process, false)
	collector := newRPCCollector(process)

	sendRPC(t, process, map[string]any{
		"id":     2,
		"method": "thread/start",
		"params": map[string]any{
			"model":        conformanceModelName,
			"cwd":          paths.cwd,
			"environments": []any{},
		},
	})
	response := collector.response(t, "2")
	if response.Kind != codexwire.KindError || response.Error == nil {
		t.Fatalf("thread/start without experimentalApi returned %s, want RPC error", response.Kind)
	}
	if response.Error.Code != -32600 || response.Error.Message != "thread/start.environments requires experimentalApi capability" {
		t.Fatalf("unexpected experimental gating error: code=%d message=%q", response.Error.Code, response.Error.Message)
	}

	closeAndWait(t, process)
}

type appServerThread struct {
	ID            string `json:"id"`
	SessionID     string `json:"sessionId"`
	Preview       string `json:"preview"`
	Ephemeral     bool   `json:"ephemeral"`
	ModelProvider string `json:"modelProvider"`
	Status        struct {
		Type string `json:"type"`
	} `json:"status"`
	CWD          string `json:"cwd"`
	ThreadSource string `json:"threadSource"`
}

type appServerTurn struct {
	ID        string           `json:"id"`
	Items     []map[string]any `json:"items"`
	ItemsView string           `json:"itemsView"`
	Status    string           `json:"status"`
	Error     any              `json:"error"`
}

type threadStartResult struct {
	Thread        appServerThread `json:"thread"`
	Model         string          `json:"model"`
	ModelProvider string          `json:"modelProvider"`
	CWD           string          `json:"cwd"`
}

func decodeThreadStart(t *testing.T, message codexwire.Message) threadStartResult {
	t.Helper()
	var result threadStartResult
	mustDecodeResult(t, message, &result)
	return result
}

func decodeTurnStart(t *testing.T, message codexwire.Message) appServerTurn {
	t.Helper()
	var result struct {
		Turn appServerTurn `json:"turn"`
	}
	mustDecodeResult(t, message, &result)
	return result.Turn
}

func writeScriptedModelConfig(t *testing.T, codexHome, serverURL string) {
	t.Helper()
	config := fmt.Sprintf(`model = %q
approval_policy = "never"
sandbox_mode = "read-only"
model_provider = "scripted_provider"

[model_providers.scripted_provider]
name = "agentserver v2 scripted provider"
base_url = %q
wire_api = "responses"
request_max_retries = 0
stream_max_retries = 0
`, conformanceModelName, serverURL+"/v1")
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatalf("write scripted model config: %v", err)
	}
}

func assertScriptedModelRequest(t *testing.T, server *scriptedmodel.Server) {
	t.Helper()
	if failures := server.Failures(); len(failures) != 0 {
		t.Fatalf("scripted model server failures: %v", failures)
	}
	requests := server.Requests()
	if len(requests) != 1 {
		t.Fatalf("scripted model received %d requests, want exactly one", len(requests))
	}
	request := requests[0]
	if request.Method != "POST" || request.Path != "/v1/responses" {
		t.Fatalf("unexpected model request target: %s %s", request.Method, request.Path)
	}
	if request.Header.Get("Authorization") != "" {
		t.Fatal("scripted model request unexpectedly carried Authorization")
	}
	if !strings.HasPrefix(strings.ToLower(request.Header.Get("Content-Type")), "application/json") {
		t.Fatalf("model request Content-Type = %q", request.Header.Get("Content-Type"))
	}

	var body struct {
		Model  string            `json:"model"`
		Input  []json.RawMessage `json:"input"`
		Tools  []json.RawMessage `json:"tools"`
		Stream bool              `json:"stream"`
	}
	if err := json.Unmarshal(request.Body, &body); err != nil {
		t.Fatalf("decode captured model request: %v", err)
	}
	if body.Model != conformanceModelName || !body.Stream {
		t.Fatalf("unexpected model request envelope: model=%q stream=%t", body.Model, body.Stream)
	}
	if !modelInputContainsUserText(t, body.Input, conformanceUserText) {
		t.Fatalf("model input omitted user text %q", conformanceUserText)
	}
	toolNames := modelToolNames(t, body.Tools)
	t.Logf("candidate model tool surface (A03 not yet asserted): %v", toolNames)
}

func assertAgentItemCompleted(t *testing.T, collector *rpcCollector, threadID, turnID, wantText string) {
	t.Helper()
	for itemIndex := 0; itemIndex < 32; itemIndex++ {
		message := collector.notification(t, "item/completed")
		var completed struct {
			ThreadID string         `json:"threadId"`
			TurnID   string         `json:"turnId"`
			Item     map[string]any `json:"item"`
		}
		if err := message.DecodeParams(&completed); err != nil {
			t.Fatal(err)
		}
		if completed.ThreadID != threadID || completed.TurnID != turnID {
			t.Fatalf("item/completed identity mismatch: %+v", completed)
		}
		if completed.Item["type"] != "agentMessage" {
			continue
		}
		if completed.Item["text"] != wantText {
			t.Fatalf("agent item text = %q, want %q", completed.Item["text"], wantText)
		}
		if completed.Item["id"] == "" {
			t.Fatal("agent item has an empty id")
		}
		return
	}
	t.Fatalf("no agent item/completed found in the first 32 completed items")
}

func modelInputContainsUserText(t *testing.T, input []json.RawMessage, want string) bool {
	t.Helper()
	for _, raw := range input {
		var item struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			t.Fatalf("decode model input item: %v", err)
		}
		if item.Role != "user" {
			continue
		}
		for _, content := range item.Content {
			if content.Type == "input_text" && content.Text == want {
				return true
			}
		}
	}
	return false
}

func modelToolNames(t *testing.T, tools []json.RawMessage) []string {
	t.Helper()
	names := make([]string, 0, len(tools))
	for _, raw := range tools {
		var tool struct {
			Type string `json:"type"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &tool); err != nil {
			t.Fatalf("decode model tool: %v", err)
		}
		if tool.Name != "" {
			names = append(names, tool.Name)
		} else {
			names = append(names, "<"+tool.Type+">")
		}
	}
	sort.Strings(names)
	return names
}
