package harnesspool

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/executorgateway/mcpcontract"
	"github.com/agentserver/agentserver/v2/internal/harnesscontrol"
	"github.com/agentserver/agentserver/v2/internal/runevent"
)

const runtimeMapperEnvironmentID = "91000000-0000-4000-8000-000000000091"

func TestRuntimeEventMapperMapsMessageReasoningDynamicToolAndProgressInOrder(t *testing.T) {
	mapper := newTestRuntimeEventMapper(t)
	var kinds []string
	var mapped []mappedRuntimeEvent
	apply := func(event harnesscontrol.Event) {
		t.Helper()
		result, err := mapper.Map(event)
		if err != nil {
			t.Fatal(err)
		}
		mapped = append(mapped, result...)
		for _, item := range result {
			kinds = append(kinds, item.Kind)
		}
	}

	apply(appRuntimeEvent(t, "item/started", map[string]any{
		"threadId": "thread-runtime-1", "turnId": "turn-runtime-1", "startedAtMs": 1,
		"item": map[string]any{"type": "agentMessage", "id": "message-1", "text": ""},
	}))
	apply(appRuntimeEvent(t, "item/agentMessage/delta", map[string]any{
		"threadId": "thread-runtime-1", "turnId": "turn-runtime-1", "itemId": "message-1", "delta": "hello",
	}))
	apply(appRuntimeEvent(t, "item/completed", map[string]any{
		"threadId": "thread-runtime-1", "turnId": "turn-runtime-1", "completedAtMs": 2,
		"item": map[string]any{"type": "agentMessage", "id": "message-1", "text": "hello"},
	}))
	apply(appRuntimeEvent(t, "item/started", map[string]any{
		"threadId": "thread-runtime-1", "turnId": "turn-runtime-1", "startedAtMs": 3,
		"item": map[string]any{"type": "reasoning", "id": "reasoning-1", "summary": []string{}, "content": []string{}},
	}))
	apply(appRuntimeEvent(t, "item/reasoning/textDelta", map[string]any{
		"threadId": "thread-runtime-1", "turnId": "turn-runtime-1", "itemId": "reasoning-1", "contentIndex": 0, "delta": "private raw reasoning",
	}))
	apply(appRuntimeEvent(t, "item/reasoning/summaryTextDelta", map[string]any{
		"threadId": "thread-runtime-1", "turnId": "turn-runtime-1", "itemId": "reasoning-1", "summaryIndex": 0, "delta": "checking",
	}))
	apply(appRuntimeEvent(t, "item/completed", map[string]any{
		"threadId": "thread-runtime-1", "turnId": "turn-runtime-1", "completedAtMs": 4,
		"item": map[string]any{"type": "reasoning", "id": "reasoning-1", "summary": []string{"checking"}, "content": []string{"private raw reasoning"}},
	}))

	arguments := map[string]any{"path": "README.md", "environment_id": runtimeMapperEnvironmentID}
	apply(dynamicToolRuntimeEvent(t, "item/started", "call-1", mcpcontract.ToolReadFile, "inProgress", arguments, nil, nil))
	apply(harnesscontrol.Event{
		Kind: harnesscontrol.EventKindExecutorMCPProgress,
		ExecutorMCPProgress: &harnesscontrol.ExecutorMCPProgressEvent{
			Kind: harnesscontrol.EventKindExecutorMCPProgress, CallID: "call-1",
			Progress: 1, Total: 2, Message: "reading",
		},
	})
	apply(dynamicToolRuntimeEvent(t, "item/completed", "call-1", mcpcontract.ToolReadFile, "completed", arguments,
		[]map[string]any{{"type": "inputText", "text": "file contents"}}, boolPointer(true)))
	apply(appRuntimeEvent(t, "turn/completed", map[string]any{
		"threadId": "thread-runtime-1", "turn": map[string]any{"id": "turn-runtime-1", "status": "completed"},
	}))

	wantKinds := []string{
		runevent.KindAssistantMessageStarted,
		runevent.KindAssistantMessageDelta,
		runevent.KindAssistantMessageCompleted,
		runevent.KindAssistantReasoningStarted,
		runevent.KindAssistantReasoningDelta,
		runevent.KindAssistantReasoningDone,
		runevent.KindToolCallStarted,
		runevent.KindToolCallArguments,
		runevent.KindToolCallProgress,
		runevent.KindToolCallCompleted,
		runevent.KindToolCallResult,
	}
	if !reflect.DeepEqual(kinds, wantKinds) {
		t.Fatalf("mapped kinds = %v, want %v", kinds, wantKinds)
	}
	if mapped[0].Source != "brain" || mapped[8].Source != "executor" {
		t.Fatalf("mapped sources = first %q, progress %q", mapped[0].Source, mapped[8].Source)
	}
	argumentsPayload := decodeMappedPayload[runevent.ToolCallArgumentsPayload](t, mapped[7])
	if argumentsPayload.Delta != `{"environment_id":"91000000-0000-4000-8000-000000000091","path":"README.md"}` {
		t.Fatalf("canonical tool arguments = %q", argumentsPayload.Delta)
	}
	progressPayload := decodeMappedPayload[runevent.ToolCallProgressPayload](t, mapped[8])
	if progressPayload.ToolCallID != "call-1" || progressPayload.Progress != 1 || progressPayload.Total != 2 || progressPayload.Message != "reading" {
		t.Fatalf("mapped progress = %+v", progressPayload)
	}
	resultPayload := decodeMappedPayload[runevent.ToolCallResultPayload](t, mapped[10])
	if resultPayload.Content != "file contents" || resultPayload.Presentation != nil {
		t.Fatalf("mapped read result = %+v", resultPayload)
	}
	if _, err := mapper.Map(appRuntimeEvent(t, "item/started", map[string]any{
		"threadId": "thread-runtime-1", "turnId": "turn-runtime-1",
		"item": map[string]any{"type": "agentMessage", "id": "late", "text": ""},
	})); err == nil || !strings.Contains(err.Error(), "after stock turn terminal") {
		t.Fatalf("post-terminal mapper error = %v", err)
	}
}

func TestRuntimeEventMapperClosesUnfinishedProjectionLifecyclesAtAbnormalTurnTerminal(t *testing.T) {
	for _, status := range []string{"interrupted", "failed"} {
		t.Run(status, func(t *testing.T) {
			wantResultText := "stock turn was interrupted"
			if status == "failed" {
				wantResultText = "stock turn failed"
			}
			mapper := newTestRuntimeEventMapperWithTools(t, mcpcontract.ToolShell)
			for _, id := range []string{"message-z", "message-a"} {
				if _, err := mapper.Map(appRuntimeEvent(t, "item/started", map[string]any{
					"threadId": "thread-runtime-1", "turnId": "turn-runtime-1",
					"item": map[string]any{"type": "agentMessage", "id": id, "text": ""},
				})); err != nil {
					t.Fatal(err)
				}
			}
			for _, id := range []string{"reasoning-z", "reasoning-a"} {
				if _, err := mapper.Map(appRuntimeEvent(t, "item/started", map[string]any{
					"threadId": "thread-runtime-1", "turnId": "turn-runtime-1",
					"item": map[string]any{"type": "reasoning", "id": id, "summary": []string{}, "content": []string{}},
				})); err != nil {
					t.Fatal(err)
				}
			}
			for _, id := range []string{"call-z", "call-a"} {
				arguments := map[string]any{"environment_id": runtimeMapperEnvironmentID, "argv": []string{"sh", "-c", id}}
				if _, err := mapper.Map(dynamicToolRuntimeEvent(t, "item/started", id, mcpcontract.ToolShell, "inProgress", arguments, nil, nil)); err != nil {
					t.Fatal(err)
				}
			}

			mapped, err := mapper.Map(appRuntimeEvent(t, "turn/completed", map[string]any{
				"threadId": "thread-runtime-1", "turn": map[string]any{"id": "turn-runtime-1", "status": status},
			}))
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, event := range mapped {
				if event.Source != "brain" {
					t.Fatalf("terminal projection source = %q", event.Source)
				}
				switch event.Kind {
				case runevent.KindAssistantMessageCompleted, runevent.KindAssistantReasoningDone:
					payload := decodeMappedPayload[runevent.MessageCompletedPayload](t, event)
					got = append(got, event.Kind+":"+payload.MessageID)
				case runevent.KindToolCallCompleted:
					payload := decodeMappedPayload[runevent.ToolCallCompletedPayload](t, event)
					got = append(got, event.Kind+":"+payload.ToolCallID)
				case runevent.KindToolCallResult:
					payload := decodeMappedPayload[runevent.ToolCallResultPayload](t, event)
					if payload.Presentation != nil || !strings.Contains(payload.Content, wantResultText) {
						t.Fatalf("%s unfinished tool result = %+v", status, payload)
					}
					got = append(got, event.Kind+":"+payload.ToolCallID)
				default:
					t.Fatalf("unexpected terminal projection kind %q", event.Kind)
				}
			}
			want := []string{
				runevent.KindAssistantMessageCompleted + ":message-a",
				runevent.KindAssistantMessageCompleted + ":message-z",
				runevent.KindAssistantReasoningDone + ":reasoning-a",
				runevent.KindAssistantReasoningDone + ":reasoning-z",
				runevent.KindToolCallCompleted + ":call-a",
				runevent.KindToolCallResult + ":call-a",
				runevent.KindToolCallCompleted + ":call-z",
				runevent.KindToolCallResult + ":call-z",
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("terminal projection order = %v, want %v", got, want)
			}
			if !mapper.terminal || len(mapper.messages) != 0 || len(mapper.reasoning) != 0 || len(mapper.toolCalls) != 0 {
				t.Fatalf("terminal mapper retained unfinished state: %+v", mapper)
			}
		})
	}
}

func TestRuntimeEventMapperUsesCompletedItemFallbacksAndBoundsLargeContent(t *testing.T) {
	mapper := newTestRuntimeEventMapper(t)
	if _, err := mapper.Map(appRuntimeEvent(t, "item/started", map[string]any{
		"threadId": "thread-runtime-1", "turnId": "turn-runtime-1",
		"item": map[string]any{"type": "agentMessage", "id": "message-large", "text": ""},
	})); err != nil {
		t.Fatal(err)
	}
	large := strings.Repeat("x", maximumInlineProjectionText+1)
	mapped, err := mapper.Map(appRuntimeEvent(t, "item/completed", map[string]any{
		"threadId": "thread-runtime-1", "turnId": "turn-runtime-1",
		"item": map[string]any{"type": "agentMessage", "id": "message-large", "text": large},
	}))
	if err != nil || len(mapped) != 2 {
		t.Fatalf("large completed message = %+v, %v", mapped, err)
	}
	delta := decodeMappedPayload[runevent.MessageDeltaPayload](t, mapped[0])
	digest := sha256.Sum256([]byte(large))
	want := "[inline projection omitted: 32769 bytes, sha256=" + hex.EncodeToString(digest[:]) + "]"
	if delta.Delta != want || strings.Contains(delta.Delta, strings.Repeat("x", 64)) {
		t.Fatalf("large message projection = %q", delta.Delta)
	}

	arguments := map[string]any{"environment_id": runtimeMapperEnvironmentID, "path": "large.txt"}
	if _, err := mapper.Map(dynamicToolRuntimeEvent(t, "item/started", "call-large", mcpcontract.ToolReadFile, "inProgress", arguments, nil, nil)); err != nil {
		t.Fatal(err)
	}
	mapped, err = mapper.Map(dynamicToolRuntimeEvent(t, "item/completed", "call-large", mcpcontract.ToolReadFile, "completed", arguments,
		[]map[string]any{{"type": "inputText", "text": large}}, boolPointer(true)))
	if err != nil || len(mapped) != 2 {
		t.Fatalf("large tool result = %+v, %v", mapped, err)
	}
	result := decodeMappedPayload[runevent.ToolCallResultPayload](t, mapped[1])
	if result.Content != want {
		t.Fatalf("large tool result projection = %q", result.Content)
	}
}

func TestRuntimeEventMapperBuildsShellPresentationFromDeterministicResult(t *testing.T) {
	mapper := newTestRuntimeEventMapperWithTools(t, mcpcontract.ToolShell)
	arguments := map[string]any{
		"environment_id": runtimeMapperEnvironmentID,
		"argv":           []string{"sh", "-c", "pwd"},
	}
	if _, err := mapper.Map(dynamicToolRuntimeEvent(t, "item/started", "call-shell", mcpcontract.ToolShell, "inProgress", arguments, nil, nil)); err != nil {
		t.Fatal(err)
	}
	resultDocument := map[string]any{
		"status": "succeeded", "output_complete": true, "exit_code": 0,
		"chunks": []map[string]any{{
			"sequence": 1, "stream": "stdout",
			"chunk_base64": base64.StdEncoding.EncodeToString([]byte("/workspace\n")),
		}},
	}
	rawResult, err := json.Marshal(resultDocument)
	if err != nil {
		t.Fatal(err)
	}
	mapped, err := mapper.Map(dynamicToolRuntimeEvent(t, "item/completed", "call-shell", mcpcontract.ToolShell, "completed", arguments,
		[]map[string]any{{"type": "inputText", "text": string(rawResult)}}, boolPointer(true)))
	if err != nil {
		t.Fatal(err)
	}
	result := decodeMappedPayload[runevent.ToolCallResultPayload](t, mapped[1])
	if result.Presentation == nil || result.Presentation.Command == nil ||
		result.Presentation.Command.Command != `["sh","-c","pwd"]` ||
		result.Presentation.Command.Output != "/workspace\n" ||
		result.Presentation.Command.Status != "succeeded (exit 0)" {
		t.Fatalf("shell presentation = %+v", result.Presentation)
	}
}

func TestRuntimeEventMapperBoundsShellPresentationAtCommandCardLimit(t *testing.T) {
	mapper := newTestRuntimeEventMapperWithTools(t, mcpcontract.ToolShell)
	arguments := map[string]any{
		"environment_id": runtimeMapperEnvironmentID,
		"argv":           []string{"sh", "-c", "generate-output"},
	}
	if _, err := mapper.Map(dynamicToolRuntimeEvent(t, "item/started", "call-shell-large", mcpcontract.ToolShell, "inProgress", arguments, nil, nil)); err != nil {
		t.Fatal(err)
	}
	output := strings.Repeat("o", maximumCommandCardOutput+1)
	resultDocument := map[string]any{
		"status": "succeeded", "output_complete": true, "exit_code": 0,
		"chunks": []map[string]any{{
			"sequence": 1, "stream": "stdout",
			"chunk_base64": base64.StdEncoding.EncodeToString([]byte(output)),
		}},
	}
	rawResult, err := json.Marshal(resultDocument)
	if err != nil {
		t.Fatal(err)
	}
	mapped, err := mapper.Map(dynamicToolRuntimeEvent(t, "item/completed", "call-shell-large", mcpcontract.ToolShell, "completed", arguments,
		[]map[string]any{{"type": "inputText", "text": string(rawResult)}}, boolPointer(true)))
	if err != nil {
		t.Fatal(err)
	}
	result := decodeMappedPayload[runevent.ToolCallResultPayload](t, mapped[1])
	if result.Presentation == nil || result.Presentation.Command == nil {
		t.Fatalf("large shell presentation = %+v", result.Presentation)
	}
	digest := sha256.Sum256([]byte(output))
	want := "[inline projection omitted: 24577 bytes, sha256=" + hex.EncodeToString(digest[:]) + "]"
	if result.Presentation.Command.Output != want {
		t.Fatalf("large shell output projection = %q, want %q", result.Presentation.Command.Output, want)
	}
}

func TestRuntimeEventMapperRequiresPinnedCatalogBoundToThread(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	prepared.FrozenCatalog.ThreadID = "thread-runtime-1"

	wrongContract := prepared.FrozenCatalog
	wrongContract.ContractVersion = "executor-mcp/future"
	if _, err := newRuntimeEventMapper("thread-runtime-1", "turn-runtime-1", wrongContract); err == nil || !strings.Contains(err.Error(), "contract does not match") {
		t.Fatalf("wrong catalog contract error = %v", err)
	}

	wrongThread := prepared.FrozenCatalog
	wrongThread.ThreadID = "other-thread"
	if _, err := newRuntimeEventMapper("thread-runtime-1", "turn-runtime-1", wrongThread); err == nil || !strings.Contains(err.Error(), "bound executor catalog") {
		t.Fatalf("wrong catalog thread error = %v", err)
	}
}

func TestRuntimeEventMapperRejectsScopeCatalogLifecycleAndArgumentDrift(t *testing.T) {
	t.Run("scope", func(t *testing.T) {
		mapper := newTestRuntimeEventMapper(t)
		_, err := mapper.Map(appRuntimeEvent(t, "item/started", map[string]any{
			"threadId": "other-thread", "turnId": "turn-runtime-1",
			"item": map[string]any{"type": "agentMessage", "id": "message-1", "text": ""},
		}))
		assertRuntimeMapperError(t, err, "escaped")
	})

	t.Run("unknown notification", func(t *testing.T) {
		mapper := newTestRuntimeEventMapper(t)
		_, err := mapper.Map(appRuntimeEvent(t, "future/notification", map[string]any{}))
		assertRuntimeMapperError(t, err, "outside the pinned runtime event profile")
	})

	t.Run("built-in item", func(t *testing.T) {
		mapper := newTestRuntimeEventMapper(t)
		_, err := mapper.Map(appRuntimeEvent(t, "item/started", map[string]any{
			"threadId": "thread-runtime-1", "turnId": "turn-runtime-1",
			"item": map[string]any{"type": "commandExecution", "id": "command-1"},
		}))
		assertRuntimeMapperError(t, err, "dynamic-only runtime profile")
	})

	t.Run("unknown tool", func(t *testing.T) {
		mapper := newTestRuntimeEventMapper(t)
		_, err := mapper.Map(dynamicToolRuntimeEvent(t, "item/started", "call-1", mcpcontract.ToolShell, "inProgress",
			map[string]any{"environment_id": runtimeMapperEnvironmentID, "argv": []string{"pwd"}}, nil, nil))
		assertRuntimeMapperError(t, err, "not in frozen catalog")
	})

	t.Run("arguments violate schema", func(t *testing.T) {
		mapper := newTestRuntimeEventMapper(t)
		_, err := mapper.Map(dynamicToolRuntimeEvent(t, "item/started", "call-1", mcpcontract.ToolReadFile, "inProgress",
			map[string]any{"path": "README.md"}, nil, nil))
		assertRuntimeMapperError(t, err, "frozen schema")
	})

	t.Run("arguments drift on completion", func(t *testing.T) {
		mapper := newTestRuntimeEventMapper(t)
		start := map[string]any{"environment_id": runtimeMapperEnvironmentID, "path": "one.txt"}
		if _, err := mapper.Map(dynamicToolRuntimeEvent(t, "item/started", "call-1", mcpcontract.ToolReadFile, "inProgress", start, nil, nil)); err != nil {
			t.Fatal(err)
		}
		changed := map[string]any{"environment_id": runtimeMapperEnvironmentID, "path": "two.txt"}
		_, err := mapper.Map(dynamicToolRuntimeEvent(t, "item/completed", "call-1", mcpcontract.ToolReadFile, "completed", changed,
			[]map[string]any{{"type": "inputText", "text": "ok"}}, boolPointer(true)))
		assertRuntimeMapperError(t, err, "changed tool identity or arguments")
	})

	t.Run("progress before start", func(t *testing.T) {
		mapper := newTestRuntimeEventMapper(t)
		_, err := mapper.Map(harnesscontrol.Event{
			Kind: harnesscontrol.EventKindExecutorMCPProgress,
			ExecutorMCPProgress: &harnesscontrol.ExecutorMCPProgressEvent{
				Kind: harnesscontrol.EventKindExecutorMCPProgress, CallID: "call-1", Progress: 1, Total: 2,
			},
		})
		assertRuntimeMapperError(t, err, "outside its dynamic tool lifecycle")
	})

	t.Run("terminal with unfinished item", func(t *testing.T) {
		mapper := newTestRuntimeEventMapper(t)
		if _, err := mapper.Map(appRuntimeEvent(t, "item/started", map[string]any{
			"threadId": "thread-runtime-1", "turnId": "turn-runtime-1",
			"item": map[string]any{"type": "agentMessage", "id": "message-1", "text": ""},
		})); err != nil {
			t.Fatal(err)
		}
		_, err := mapper.Map(appRuntimeEvent(t, "turn/completed", map[string]any{
			"threadId": "thread-runtime-1", "turn": map[string]any{"id": "turn-runtime-1", "status": "completed"},
		}))
		assertRuntimeMapperError(t, err, "unfinished")
	})

	t.Run("terminal result inconsistent", func(t *testing.T) {
		mapper := newTestRuntimeEventMapper(t)
		arguments := map[string]any{"environment_id": runtimeMapperEnvironmentID, "path": "one.txt"}
		if _, err := mapper.Map(dynamicToolRuntimeEvent(t, "item/started", "call-1", mcpcontract.ToolReadFile, "inProgress", arguments, nil, nil)); err != nil {
			t.Fatal(err)
		}
		_, err := mapper.Map(dynamicToolRuntimeEvent(t, "item/completed", "call-1", mcpcontract.ToolReadFile, "completed", arguments,
			[]map[string]any{{"type": "inputText", "text": "ok"}}, boolPointer(false)))
		assertRuntimeMapperError(t, err, "inconsistent")
	})
}

func newTestRuntimeEventMapper(t *testing.T) *runtimeEventMapper {
	t.Helper()
	prepared := poolTestPreparedLaunch(t)
	prepared.FrozenCatalog.ThreadID = "thread-runtime-1"
	mapper, err := newRuntimeEventMapper("thread-runtime-1", "turn-runtime-1", prepared.FrozenCatalog)
	if err != nil {
		t.Fatal(err)
	}
	return mapper
}

func newTestRuntimeEventMapperWithTools(t *testing.T, tools ...string) *runtimeEventMapper {
	t.Helper()
	proposal, err := BuildExecutorCatalog(ExecutorCatalogPolicy{
		Version: "runtime-mapper-test/1", ContextDigest: sha256.Sum256([]byte("runtime mapper policy")), AllowedTools: tools,
	})
	if err != nil {
		t.Fatal(err)
	}
	frozen := poolTestPreparedLaunch(t).FrozenCatalog
	frozen.ThreadID = "thread-runtime-1"
	frozen.CanonicalCatalog = proposal.CanonicalCatalog
	frozen.CatalogDigest = proposal.CatalogDigest
	mapper, err := newRuntimeEventMapper("thread-runtime-1", "turn-runtime-1", frozen)
	if err != nil {
		t.Fatal(err)
	}
	return mapper
}

func appRuntimeEvent(t *testing.T, method string, params any) harnesscontrol.Event {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return harnesscontrol.Event{
		Kind: harnesscontrol.EventKindAppServerNotification,
		AppServerNotification: &harnesscontrol.AppServerNotificationEvent{
			Kind: harnesscontrol.EventKindAppServerNotification, Method: method, Params: raw,
		},
	}
}

func dynamicToolRuntimeEvent(
	t *testing.T,
	method, callID, tool, status string,
	arguments any,
	contentItems []map[string]any,
	success *bool,
) harnesscontrol.Event {
	t.Helper()
	item := map[string]any{
		"type": "dynamicToolCall", "id": callID, "namespace": mcpcontract.Namespace,
		"tool": tool, "arguments": arguments, "status": status,
		"contentItems": contentItems, "success": success,
	}
	return appRuntimeEvent(t, method, map[string]any{
		"threadId": "thread-runtime-1", "turnId": "turn-runtime-1", "item": item,
	})
}

func decodeMappedPayload[T any](t *testing.T, event mappedRuntimeEvent) T {
	t.Helper()
	var payload T
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func assertRuntimeMapperError(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("runtime mapper error = %v, want %q", err, want)
	}
}

func boolPointer(value bool) *bool { return &value }
