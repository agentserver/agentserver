package codex_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/conformance/codex/internal/scriptedmcp"
	"github.com/agentserver/agentserver/v2/conformance/codex/internal/scriptedmodel"
	"github.com/agentserver/agentserver/v2/internal/codexwire"
)

const (
	conformanceModelName = "agentserver-v2-scripted-model"
	conformanceUserText  = "complete the deterministic phase-zero lifecycle"
	conformanceFinalText = "scripted lifecycle complete"
	executorMCPNamespace = "mcp__executor"
	approvedMCPToolName  = "approved_echo"
	blockedMCPToolName   = "blocked_echo"
)

func TestAppServerA01ScriptedModelLifecycle(t *testing.T) {
	binary, paths := prepareLiveCodex(t)
	release := candidateRelease(t, binary, paths)
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
			"threadId":     thread.Thread.ID,
			"environments": []any{},
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
	assertReleaseBoundTerminalTurn(t, release, turnCompleted.Turn)

	closeAndWait(t, process)
}

func assertReleaseBoundTerminalTurn(t *testing.T, release string, turn appServerTurn) {
	t.Helper()
	if turn.Status != "completed" || turn.Error != nil {
		t.Fatalf("unexpected terminal turn status for Codex %s: %+v", release, turn)
	}
	switch release {
	case "0.145.0":
		if turn.ItemsView != "notLoaded" || len(turn.Items) != 0 {
			t.Fatalf("Codex %s terminal projection changed: %+v", release, turn)
		}
	case "0.146.0-alpha.14", "0.146.0":
		if turn.ItemsView != "summary" || len(turn.Items) != 1 ||
			turn.Items[0]["type"] != "agentMessage" ||
			turn.Items[0]["text"] != conformanceFinalText ||
			turn.Items[0]["id"] == "" {
			t.Fatalf("Codex %s terminal projection changed: %+v", release, turn)
		}
	default:
		t.Fatalf("Codex %s has no release-bound A01 terminal projection", release)
	}
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

// TestAppServerA03Codex0145StillExecutesUpdatePlan is a negative
// characterization probe, not an A03 pass. It proves that after every known
// non-MCP switch is disabled, stock 0.145.0 still advertises and executes the
// unconditional update_plan handler.
func TestAppServerA03Codex0145StillExecutesUpdatePlan(t *testing.T) {
	binary, paths := prepareLiveCodex(t)
	requireCandidateRelease(t, binary, paths, "0.145.0")
	planCall, err := scriptedmodel.FunctionCall(
		"response-a03-plan",
		"call-a03-plan",
		"update_plan",
		`{"explanation":"forbidden A03 probe","plan":[{"step":"must not execute","status":"in_progress"}]}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	finalResponse, err := scriptedmodel.AssistantMessage(
		"response-a03-final",
		"message-a03-final",
		conformanceFinalText,
	)
	if err != nil {
		t.Fatal(err)
	}
	modelServer, err := scriptedmodel.Start(scriptedmodel.Config{
		Responses: []scriptedmodel.Response{planCall, finalResponse},
	})
	if err != nil {
		t.Fatalf("start loopback scripted model: %v", err)
	}
	t.Cleanup(modelServer.Close)

	writeScriptedModelConfig(t, paths.codexHome, modelServer.URL())
	process := startPreparedLiveCodex(t, binary, paths, "app-server", "--listen", "stdio://", "--strict-config")
	initializeAppServer(t, process)
	collector := newRPCCollector(process)

	sendRPC(t, process, map[string]any{
		"id":     2,
		"method": "thread/start",
		"params": map[string]any{
			"model":                   conformanceModelName,
			"cwd":                     paths.cwd,
			"approvalPolicy":          "never",
			"sandbox":                 "read-only",
			"ephemeral":               false,
			"threadSource":            "user",
			"environments":            []any{},
			"dynamicTools":            []any{},
			"selectedCapabilityRoots": []any{},
		},
	})
	thread := decodeThreadStart(t, collector.response(t, "2"))
	collector.notification(t, "thread/started")

	sendRPC(t, process, map[string]any{
		"id":     3,
		"method": "turn/start",
		"params": map[string]any{
			"threadId":     thread.Thread.ID,
			"environments": []any{},
			"input": []any{
				map[string]any{
					"type":         "text",
					"text":         "attempt the forbidden update_plan tool",
					"textElements": []any{},
				},
			},
		},
	})
	turn := decodeTurnStart(t, collector.response(t, "3"))
	collector.notification(t, "turn/started")

	planUpdatedMessage := collector.notification(t, "turn/plan/updated")
	var planUpdated struct {
		ThreadID    string `json:"threadId"`
		TurnID      string `json:"turnId"`
		Explanation string `json:"explanation"`
		Plan        []struct {
			Step   string `json:"step"`
			Status string `json:"status"`
		} `json:"plan"`
	}
	if err := planUpdatedMessage.DecodeParams(&planUpdated); err != nil {
		t.Fatal(err)
	}
	if planUpdated.ThreadID != thread.Thread.ID || planUpdated.TurnID != turn.ID ||
		planUpdated.Explanation != "forbidden A03 probe" || len(planUpdated.Plan) != 1 ||
		planUpdated.Plan[0].Step != "must not execute" || planUpdated.Plan[0].Status != "inProgress" {
		t.Fatalf("unexpected executed update_plan notification: %+v", planUpdated)
	}

	assertAgentItemCompleted(t, collector, thread.Thread.ID, turn.ID, conformanceFinalText)
	collector.notification(t, "turn/completed")
	if failures := modelServer.Failures(); len(failures) != 0 {
		t.Fatalf("scripted model server failures: %v", failures)
	}
	requests := modelServer.Requests()
	if len(requests) != 2 {
		t.Fatalf("scripted model received %d requests, want two", len(requests))
	}
	first := decodeCapturedModelRequest(t, requests[0])
	toolNames := modelToolNames(t, first.Tools)
	if len(toolNames) != 1 || toolNames[0] != "update_plan" {
		t.Fatalf("hardened candidate tool surface = %v, want only the known A03 blocker update_plan", toolNames)
	}
	second := decodeCapturedModelRequest(t, requests[1])
	if !modelInputContainsFunctionOutput(second.Input, "call-a03-plan", "Plan updated") {
		t.Fatal("second model request omitted the successful update_plan result")
	}
	t.Log("A03 remains blocked: hardened stock candidate advertised and executed update_plan")

	closeAndWait(t, process)
}

func TestAppServerA03Codex0146HasNoBuiltinTools(t *testing.T) {
	binary, paths := prepareLiveCodex(t)
	requireCandidateReleaseOneOf(t, binary, paths, "0.146.0-alpha.14", "0.146.0")
	response, err := scriptedmodel.AssistantMessage(
		"response-a03-empty",
		"message-a03-empty",
		conformanceFinalText,
	)
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

	writeScriptedModelConfigWithOptions(t, paths.codexHome, modelServer.URL(), scriptedModelConfigOptions{
		disableUpdatePlan: true,
	})
	process := startPreparedLiveCodex(t, binary, paths, "app-server", "--listen", "stdio://", "--strict-config")
	initializeAppServer(t, process)
	collector := newRPCCollector(process)

	sendRPC(t, process, map[string]any{
		"id":     2,
		"method": "thread/start",
		"params": map[string]any{
			"model":                   conformanceModelName,
			"cwd":                     paths.cwd,
			"approvalPolicy":          "never",
			"sandbox":                 "read-only",
			"ephemeral":               false,
			"threadSource":            "user",
			"environments":            []any{},
			"dynamicTools":            []any{},
			"selectedCapabilityRoots": []any{},
		},
	})
	thread := decodeThreadStart(t, collector.response(t, "2"))
	collector.notification(t, "thread/started")

	sendRPC(t, process, map[string]any{
		"id":     3,
		"method": "turn/start",
		"params": map[string]any{
			"threadId":     thread.Thread.ID,
			"environments": []any{},
			"input": []any{
				map[string]any{
					"type":         "text",
					"text":         "complete without tools",
					"textElements": []any{},
				},
			},
		},
	})
	turn := decodeTurnStart(t, collector.response(t, "3"))
	collector.notification(t, "turn/started")
	assertAgentItemCompleted(t, collector, thread.Thread.ID, turn.ID, conformanceFinalText)
	collector.notification(t, "turn/completed")

	if failures := modelServer.Failures(); len(failures) != 0 {
		t.Fatalf("scripted model server failures: %v", failures)
	}
	requests := modelServer.Requests()
	if len(requests) != 1 {
		t.Fatalf("scripted model received %d requests, want one", len(requests))
	}
	request := decodeCapturedModelRequest(t, requests[0])
	if toolNames := modelToolNames(t, request.Tools); len(toolNames) != 0 {
		t.Fatalf("hardened candidate retained builtin tools: %v", toolNames)
	}

	closeAndWait(t, process)
}

// TestAppServerA03Codex0146StillExposesMCPResourceTools is a negative
// characterization probe. These releases can remove all builtins when no MCP
// server is present, but adding one allowlisted MCP server also registers three
// stock MCP resource handlers outside that allowlist.
func TestAppServerA03Codex0146StillExposesMCPResourceTools(t *testing.T) {
	binary, paths := prepareLiveCodex(t)
	requireCandidateReleaseOneOf(t, binary, paths, "0.146.0-alpha.14", "0.146.0")
	modelResponse, err := scriptedmodel.AssistantMessage(
		"response-a03-mcp-surface",
		"message-a03-mcp-surface",
		conformanceFinalText,
	)
	if err != nil {
		t.Fatal(err)
	}
	modelServer, err := scriptedmodel.Start(scriptedmodel.Config{
		Responses: []scriptedmodel.Response{modelResponse},
	})
	if err != nil {
		t.Fatalf("start loopback scripted model: %v", err)
	}
	t.Cleanup(modelServer.Close)
	mcpServer := startExecutorMCPServer(t, nil)

	writeScriptedModelConfigWithOptions(t, paths.codexHome, modelServer.URL(), scriptedModelConfigOptions{
		disableUpdatePlan: true,
		mcpServerURL:      mcpServer.URL(),
		mcpEnabledTools:   []string{approvedMCPToolName},
	})
	process := startPreparedLiveCodex(t, binary, paths, "app-server", "--listen", "stdio://", "--strict-config")
	initializeAppServer(t, process)
	collector := newRPCCollector(process)
	thread, turn := startMinimalAppServerTurn(t, collector, paths.cwd, "capture the exact MCP tool surface")
	assertAgentItemCompleted(t, collector, thread.Thread.ID, turn.ID, conformanceFinalText)
	collector.notification(t, "turn/completed")
	closeAndWait(t, process)

	if failures := modelServer.Failures(); len(failures) != 0 {
		t.Fatalf("scripted model server failures: %v", failures)
	}
	requests := modelServer.Requests()
	if len(requests) != 1 {
		t.Fatalf("scripted model received %d requests, want one", len(requests))
	}
	surface := modelToolNames(t, decodeCapturedModelRequest(t, requests[0]).Tools)
	wantSurface := []string{
		"list_mcp_resource_templates",
		"list_mcp_resources",
		executorMCPNamespace + "." + approvedMCPToolName,
		"read_mcp_resource",
	}
	sort.Strings(wantSurface)
	if !reflect.DeepEqual(surface, wantSurface) {
		t.Fatalf("MCP candidate tool surface = %v, want characterized blocker surface %v", surface, wantSurface)
	}
	if failures := mcpServer.Failures(); len(failures) != 0 {
		t.Fatalf("scripted MCP server failures: %v", failures)
	}
	assertMCPBootstrap(t, mcpServer)
	t.Log("A03 remains blocked: configuring one MCP server also exposed three non-allowlisted stock resource tools")
}

func TestAppServerA03Codex0146RoutesApprovedMCPTool(t *testing.T) {
	binary, paths := prepareLiveCodex(t)
	requireCandidateReleaseOneOf(t, binary, paths, "0.146.0-alpha.14", "0.146.0")
	toolCall, err := scriptedmodel.NamespacedFunctionCall(
		"response-a03-mcp-call",
		"call-a03-mcp-call",
		executorMCPNamespace,
		approvedMCPToolName,
		`{"message":"hello executor"}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	finalResponse, err := scriptedmodel.AssistantMessage(
		"response-a03-mcp-final",
		"message-a03-mcp-final",
		conformanceFinalText,
	)
	if err != nil {
		t.Fatal(err)
	}
	modelServer, err := scriptedmodel.Start(scriptedmodel.Config{
		Responses: []scriptedmodel.Response{toolCall, finalResponse},
	})
	if err != nil {
		t.Fatalf("start loopback scripted model: %v", err)
	}
	t.Cleanup(modelServer.Close)
	mcpServer := startExecutorMCPServer(t, []scriptedmcp.ExpectedCall{
		{
			Name:      approvedMCPToolName,
			Arguments: json.RawMessage(`{"message":"hello executor"}`),
			Result: json.RawMessage(
				`{"content":[{"type":"text","text":"approved echo: hello executor"}],"structuredContent":{"echoed":"hello executor"},"isError":false}`,
			),
		},
	})

	writeScriptedModelConfigWithOptions(t, paths.codexHome, modelServer.URL(), scriptedModelConfigOptions{
		disableUpdatePlan: true,
		mcpServerURL:      mcpServer.URL(),
		mcpEnabledTools:   []string{approvedMCPToolName},
	})
	process := startPreparedLiveCodex(t, binary, paths, "app-server", "--listen", "stdio://", "--strict-config")
	initializeAppServer(t, process)
	collector := newRPCCollector(process)
	thread, turn := startMinimalAppServerTurn(t, collector, paths.cwd, "call the approved MCP tool")
	assertAgentItemCompleted(t, collector, thread.Thread.ID, turn.ID, conformanceFinalText)
	collector.notification(t, "turn/completed")
	closeAndWait(t, process)

	if failures := modelServer.Failures(); len(failures) != 0 {
		t.Fatalf("scripted model server failures: %v", failures)
	}
	modelRequests := modelServer.Requests()
	if len(modelRequests) != 2 {
		t.Fatalf("scripted model received %d requests, want two", len(modelRequests))
	}
	firstSurface := modelToolNames(t, decodeCapturedModelRequest(t, modelRequests[0]).Tools)
	if !containsString(firstSurface, executorMCPNamespace+"."+approvedMCPToolName) ||
		containsString(firstSurface, executorMCPNamespace+"."+blockedMCPToolName) {
		t.Fatalf("unexpected filtered MCP surface: %v", firstSurface)
	}
	second := decodeCapturedModelRequest(t, modelRequests[1])
	if !modelInputContainsFunctionOutput(second.Input, "call-a03-mcp-call", `"echoed":"hello executor"`) {
		encodedInput, err := json.Marshal(second.Input)
		if err != nil {
			t.Fatalf("encode second model input after missing MCP result: %v", err)
		}
		t.Fatalf("second model request omitted the approved MCP result: input=%s", encodedInput)
	}
	if failures := mcpServer.Failures(); len(failures) != 0 {
		t.Fatalf("scripted MCP server failures: %v", failures)
	}
	calls := mcpServer.Calls()
	if len(calls) != 1 || calls[0].Name != approvedMCPToolName {
		t.Fatalf("scripted MCP calls = %+v, want one approved call", calls)
	}
}

// TestAppServerA04ReleaseDebugRequirementsOverrideIsUnavailable records the
// stock release boundary for endpoint-allowlist testing. Upstream has an
// internal debug hook that redirects managed_config.toml and its sibling
// requirements.toml, but official release binaries compile that hook out. A
// real A04 probe must therefore run in a disposable image or mount namespace
// with the managed file installed at the platform system path; setting this
// environment variable is not an equivalent test or deployment mechanism.
func TestAppServerA04ReleaseDebugRequirementsOverrideIsUnavailable(t *testing.T) {
	binary, paths := prepareLiveCodex(t)
	requireCandidateReleaseOneOf(t, binary, paths, "0.146.0-alpha.14", "0.146.0")
	managedConfigPath := filepath.Join(paths.root, "managed_config.toml")
	if err := os.WriteFile(managedConfigPath, []byte("\n"), 0o600); err != nil {
		t.Fatalf("write managed config sentinel: %v", err)
	}
	sqliteHomeSentinel := filepath.Join(paths.root, "redirected-sqlite-home")
	if err := os.MkdirAll(sqliteHomeSentinel, 0o700); err != nil {
		t.Fatalf("create sqlite home sentinel: %v", err)
	}
	requirementsPath := filepath.Join(paths.root, "requirements.toml")
	requirements := fmt.Sprintf("sqlite_home = %q\n", sqliteHomeSentinel)
	if err := os.WriteFile(requirementsPath, []byte(requirements), 0o600); err != nil {
		t.Fatalf("write requirements sentinel: %v", err)
	}
	paths.environment = append(
		paths.environment,
		"CODEX_APP_SERVER_MANAGED_CONFIG_PATH="+managedConfigPath,
	)

	process := startPreparedLiveCodex(t, binary, paths, "app-server", "--listen", "stdio://", "--strict-config")
	initializeAppServer(t, process)
	collector := newRPCCollector(process)
	sendRPC(t, process, map[string]any{
		"id":     2,
		"method": "configRequirements/read",
		"params": map[string]any{},
	})
	var result struct {
		Requirements *struct {
			SQLiteHome *string `json:"sqliteHome"`
		} `json:"requirements"`
	}
	mustDecodeResult(t, collector.response(t, "2"), &result)
	if result.Requirements != nil && result.Requirements.SQLiteHome != nil &&
		*result.Requirements.SQLiteHome == sqliteHomeSentinel {
		t.Fatal("official release unexpectedly honored the debug-only managed requirements override")
	}
	closeAndWait(t, process)
	t.Log("A04 still requires an image-level probe with /etc/codex/requirements.toml mounted before process start")
}

// TestAppServerA05Codex0146ApproveModeDoesNotDoublePrompt verifies the
// app-server side of the product's single-approval design. The advertised tool
// is explicitly destructive and open-world, and the thread enables MCP
// elicitations under granular approval. If default_tools_approval_mode is not
// honored, Codex emits its own mcpServer/elicitation/request and rpcCollector
// fails the probe before tools/call can complete.
func TestAppServerA05Codex0146ApproveModeDoesNotDoublePrompt(t *testing.T) {
	binary, paths := prepareLiveCodex(t)
	requireCandidateReleaseOneOf(t, binary, paths, "0.146.0-alpha.14", "0.146.0")
	toolCall, err := scriptedmodel.NamespacedFunctionCall(
		"response-a05-mcp-call",
		"call-a05-mcp-call",
		executorMCPNamespace,
		approvedMCPToolName,
		`{"message":"execute after product policy"}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	finalResponse, err := scriptedmodel.AssistantMessage(
		"response-a05-final",
		"message-a05-final",
		conformanceFinalText,
	)
	if err != nil {
		t.Fatal(err)
	}
	modelServer, err := scriptedmodel.Start(scriptedmodel.Config{
		Responses: []scriptedmodel.Response{toolCall, finalResponse},
	})
	if err != nil {
		t.Fatalf("start loopback scripted model: %v", err)
	}
	t.Cleanup(modelServer.Close)
	mcpServer := startDestructiveExecutorMCPServer(t, []scriptedmcp.ExpectedCall{
		{
			Name:      approvedMCPToolName,
			Arguments: json.RawMessage(`{"message":"execute after product policy"}`),
			Result: json.RawMessage(
				`{"content":[{"type":"text","text":"product policy already approved"}],"structuredContent":{"approved":true},"isError":false}`,
			),
		},
	})

	writeScriptedModelConfigWithOptions(t, paths.codexHome, modelServer.URL(), scriptedModelConfigOptions{
		disableUpdatePlan: true,
		mcpServerURL:      mcpServer.URL(),
		mcpEnabledTools:   []string{approvedMCPToolName},
	})
	process := startPreparedLiveCodex(t, binary, paths, "app-server", "--listen", "stdio://", "--strict-config")
	initializeAppServer(t, process)
	collector := newRPCCollector(process)
	thread, turn := startMinimalAppServerTurnWithApprovalPolicy(
		t,
		collector,
		paths.cwd,
		"call the destructive executor tool without a second Codex approval",
		granularMCPApprovalPolicy(),
	)
	assertAgentItemCompleted(t, collector, thread.Thread.ID, turn.ID, conformanceFinalText)
	collector.notification(t, "turn/completed")
	closeAndWait(t, process)

	if failures := modelServer.Failures(); len(failures) != 0 {
		t.Fatalf("scripted model server failures: %v", failures)
	}
	if failures := mcpServer.Failures(); len(failures) != 0 {
		t.Fatalf("scripted MCP server failures: %v", failures)
	}
	calls := mcpServer.Calls()
	if len(calls) != 1 || calls[0].Name != approvedMCPToolName {
		t.Fatalf("scripted MCP calls = %+v, want one direct approved call", calls)
	}
}

// TestAppServerA05ProbeDetectsCodexGenericPrompt is the positive control for
// the no-double-prompt probe. With the same destructive tool and granular
// thread policy, changing only the server default to prompt must produce the
// Codex-owned reverse request and must not reach tools/call after cancellation.
func TestAppServerA05ProbeDetectsCodexGenericPrompt(t *testing.T) {
	binary, paths := prepareLiveCodex(t)
	requireCandidateReleaseOneOf(t, binary, paths, "0.146.0-alpha.14", "0.146.0")
	toolCall, err := scriptedmodel.NamespacedFunctionCall(
		"response-a05-control-call",
		"call-a05-control-call",
		executorMCPNamespace,
		approvedMCPToolName,
		`{"message":"must wait for Codex approval"}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	finalResponse, err := scriptedmodel.AssistantMessage(
		"response-a05-control-final",
		"message-a05-control-final",
		conformanceFinalText,
	)
	if err != nil {
		t.Fatal(err)
	}
	modelServer, err := scriptedmodel.Start(scriptedmodel.Config{
		Responses: []scriptedmodel.Response{toolCall, finalResponse},
	})
	if err != nil {
		t.Fatalf("start loopback scripted model: %v", err)
	}
	t.Cleanup(modelServer.Close)
	mcpServer := startDestructiveExecutorMCPServer(t, nil)

	writeScriptedModelConfigWithOptions(t, paths.codexHome, modelServer.URL(), scriptedModelConfigOptions{
		disableUpdatePlan: true,
		mcpServerURL:      mcpServer.URL(),
		mcpEnabledTools:   []string{approvedMCPToolName},
		mcpApprovalMode:   "prompt",
	})
	process := startPreparedLiveCodex(t, binary, paths, "app-server", "--listen", "stdio://", "--strict-config")
	initializeAppServer(t, process)
	collector := newRPCCollector(process)
	thread, turn := startMinimalAppServerTurnWithApprovalPolicy(
		t,
		collector,
		paths.cwd,
		"prove that the A05 probe detects a Codex-owned generic prompt",
		granularMCPApprovalPolicy(),
	)
	reverseRequest := collector.request(t, "mcpServer/elicitation/request")
	var requestParams struct {
		ThreadID   string `json:"threadId"`
		TurnID     string `json:"turnId"`
		ServerName string `json:"serverName"`
		Mode       string `json:"mode"`
		Meta       struct {
			ApprovalKind string `json:"codex_approval_kind"`
		} `json:"_meta"`
	}
	if err := reverseRequest.DecodeParams(&requestParams); err != nil {
		t.Fatal(err)
	}
	if requestParams.ThreadID != thread.Thread.ID || requestParams.TurnID != turn.ID ||
		requestParams.ServerName != "executor" || requestParams.Mode != "form" ||
		requestParams.Meta.ApprovalKind != "mcp_tool_call" {
		t.Fatalf("unexpected Codex generic MCP approval request: %+v", requestParams)
	}
	sendRPC(t, process, map[string]any{
		"id": reverseRequest.ID,
		"result": map[string]any{
			"action":  "cancel",
			"content": nil,
		},
	})
	assertAgentItemCompleted(t, collector, thread.Thread.ID, turn.ID, conformanceFinalText)
	collector.notification(t, "turn/completed")
	closeAndWait(t, process)

	if failures := modelServer.Failures(); len(failures) != 0 {
		t.Fatalf("scripted model server failures: %v", failures)
	}
	if failures := mcpServer.Failures(); len(failures) != 0 {
		t.Fatalf("scripted MCP server failures: %v", failures)
	}
	if calls := mcpServer.Calls(); len(calls) != 0 {
		t.Fatalf("cancelled Codex generic approval still reached tools/call: %+v", calls)
	}
}

// TestAppServerA06Codex0146ForwardsMCPFormElicitation proves that an
// executor-originated MCP elicitation remains under app-server client control.
// Each action is returned to the MCP server, resolves the app-server reverse
// request before turn completion, and becomes part of the tool result seen by
// the next model request.
func TestAppServerA06Codex0146ForwardsMCPFormElicitation(t *testing.T) {
	decisions := []struct {
		action  string
		content any
	}{
		{action: "accept", content: map[string]any{"confirmed": true}},
		{action: "decline"},
		{action: "cancel"},
	}
	for _, decision := range decisions {
		t.Run(decision.action, func(t *testing.T) {
			runA06MCPFormElicitation(
				t,
				decision.action,
				decision.action,
				decision.content,
				granularMCPApprovalPolicy(),
				true,
			)
		})
	}
}

// TestAppServerA06NeverPolicyAutoDeclinesMCPFormElicitation is the negative
// control for the granular-policy probe. A non-empty form schema cannot be
// auto-accepted under never: Codex must return decline to MCP without emitting
// an app-server reverse request.
func TestAppServerA06NeverPolicyAutoDeclinesMCPFormElicitation(t *testing.T) {
	runA06MCPFormElicitation(
		t,
		"never",
		"decline",
		nil,
		"never",
		false,
	)
}

// TestAppServerA06MCPFormElicitationPausesToolTimeout verifies that
// tool_timeout_sec measures active MCP time rather than wall-clock time while
// the app-server client owns a pending elicitation. The product approval TTL
// therefore cannot delegate expiry cleanup to Codex's MCP tool timeout.
func TestAppServerA06MCPFormElicitationPausesToolTimeout(t *testing.T) {
	binary, paths := prepareLiveCodex(t)
	requireCandidateReleaseOneOf(t, binary, paths, "0.146.0-alpha.14", "0.146.0")
	const (
		toolTimeout       = 500 * time.Millisecond
		observationWindow = 1500 * time.Millisecond
		callID            = "call-a06-paused-tool-timeout"
	)
	toolArguments := `{"message":"wait beyond the configured MCP tool timeout"}`
	toolCall, err := scriptedmodel.NamespacedFunctionCall(
		"response-a06-paused-timeout-call",
		callID,
		executorMCPNamespace,
		approvedMCPToolName,
		toolArguments,
	)
	if err != nil {
		t.Fatal(err)
	}
	finalResponse, err := scriptedmodel.AssistantMessage(
		"response-a06-paused-timeout-final",
		"message-a06-paused-timeout-final",
		conformanceFinalText,
	)
	if err != nil {
		t.Fatal(err)
	}
	modelServer, err := scriptedmodel.Start(scriptedmodel.Config{
		Responses: []scriptedmodel.Response{toolCall, finalResponse},
	})
	if err != nil {
		t.Fatalf("start loopback scripted model: %v", err)
	}
	t.Cleanup(modelServer.Close)
	mcpServer := startDestructiveExecutorMCPServer(t, []scriptedmcp.ExpectedCall{{
		Name:      approvedMCPToolName,
		Arguments: json.RawMessage(toolArguments),
		Result: json.RawMessage(
			`{"content":[{"type":"text","text":"client cancelled after timeout observation"}],"structuredContent":{"clientAction":"cancel"},"isError":false}`,
		),
		Elicitation: &scriptedmcp.ExpectedElicitation{
			ID: json.RawMessage(`"elicitation-a06-paused-tool-timeout"`),
			Params: json.RawMessage(
				`{"_meta":{"agentserver_execution_id":"execution-a06-paused-tool-timeout"},"message":"Wait for an explicit product decision","requestedSchema":{"type":"object","properties":{"confirmed":{"type":"boolean"}},"required":["confirmed"]}}`,
			),
			Response: json.RawMessage(`{"action":"cancel"}`),
		},
	}})

	writeScriptedModelConfigWithOptions(t, paths.codexHome, modelServer.URL(), scriptedModelConfigOptions{
		disableUpdatePlan: true,
		mcpServerURL:      mcpServer.URL(),
		mcpEnabledTools:   []string{approvedMCPToolName},
		mcpToolTimeoutSec: toolTimeout.Seconds(),
	})
	process := startPreparedLiveCodex(t, binary, paths, "app-server", "--listen", "stdio://", "--strict-config")
	initializeAppServer(t, process)
	collector := newRPCCollector(process)
	thread, turn := startMinimalAppServerTurnWithApprovalPolicy(
		t,
		collector,
		paths.cwd,
		"call the executor and wait for a product decision beyond its tool timeout",
		granularMCPApprovalPolicy(),
	)
	reverseRequest := collector.request(t, "mcpServer/elicitation/request")
	var requestParams struct {
		ThreadID   string `json:"threadId"`
		TurnID     string `json:"turnId"`
		ServerName string `json:"serverName"`
		Mode       string `json:"mode"`
	}
	if err := reverseRequest.DecodeParams(&requestParams); err != nil {
		t.Fatal(err)
	}
	if requestParams.ThreadID != thread.Thread.ID || requestParams.TurnID != turn.ID ||
		requestParams.ServerName != "executor" || requestParams.Mode != "form" {
		t.Fatalf("unexpected pending MCP elicitation: %+v", requestParams)
	}

	collector.assertNoNotificationMethodsFor(
		t,
		observationWindow,
		"serverRequest/resolved",
		"turn/completed",
	)
	if requests := modelServer.Requests(); len(requests) != 1 {
		t.Fatalf("pending elicitation sent %d model requests after %s with tool timeout %s, want one", len(requests), observationWindow, toolTimeout)
	}
	if responses := mcpServer.ElicitationResponses(); len(responses) != 0 {
		t.Fatalf("pending elicitation received a decision before client response: %+v", responses)
	}

	sendRPC(t, process, map[string]any{
		"id": reverseRequest.ID,
		"result": map[string]any{
			"action":  "cancel",
			"content": nil,
		},
	})
	assertServerRequestResolvedBeforeTurnCompleted(t, collector, thread.Thread.ID, reverseRequest.ID)
	assertAgentItemCompleted(t, collector, thread.Thread.ID, turn.ID, conformanceFinalText)
	collector.notification(t, "turn/completed")
	closeAndWait(t, process)

	if failures := modelServer.Failures(); len(failures) != 0 {
		t.Fatalf("scripted model server failures: %v", failures)
	}
	modelRequests := modelServer.Requests()
	if len(modelRequests) != 2 {
		t.Fatalf("scripted model received %d requests, want two", len(modelRequests))
	}
	second := decodeCapturedModelRequest(t, modelRequests[1])
	if !modelInputContainsFunctionOutput(second.Input, callID, `"clientAction":"cancel"`) {
		t.Fatalf("second model request omitted the post-wait tool result: input=%s", encodeModelInput(t, second.Input))
	}
	if failures := mcpServer.Failures(); len(failures) != 0 {
		t.Fatalf("scripted MCP server failures: %v", failures)
	}
	if calls := mcpServer.Calls(); len(calls) != 1 || calls[0].Name != approvedMCPToolName {
		t.Fatalf("scripted MCP calls = %+v, want one eliciting call", calls)
	}
	responses := mcpServer.ElicitationResponses()
	if len(responses) != 1 {
		t.Fatalf("scripted MCP elicitation responses = %+v, want one", responses)
	}
	var observed struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(responses[0].Result, &observed); err != nil {
		t.Fatal(err)
	}
	if observed.Action != "cancel" {
		t.Fatalf("MCP elicitation action = %q, want cancel", observed.Action)
	}
	assertMCPBootstrap(t, mcpServer)
}

// TestAppServerA07InterruptClearsPendingMCPFormElicitation verifies the
// cancellation boundary while the app-server client owns an unresolved MCP
// reverse request. Interrupting the turn must resolve that request, send
// cancel back to MCP, produce a terminal interrupted turn, and stop before a
// second model request or tool call.
func TestAppServerA07InterruptClearsPendingMCPFormElicitation(t *testing.T) {
	binary, paths := prepareLiveCodex(t)
	requireCandidateReleaseOneOf(t, binary, paths, "0.146.0-alpha.14", "0.146.0")
	const callID = "call-a07-pending-elicitation"
	toolCall, err := scriptedmodel.NamespacedFunctionCall(
		"response-a07-call",
		callID,
		executorMCPNamespace,
		approvedMCPToolName,
		`{"message":"wait for a client decision"}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	modelServer, err := scriptedmodel.Start(scriptedmodel.Config{
		Responses: []scriptedmodel.Response{toolCall},
	})
	if err != nil {
		t.Fatalf("start loopback scripted model: %v", err)
	}
	t.Cleanup(modelServer.Close)
	mcpServer := startDestructiveExecutorMCPServer(t, []scriptedmcp.ExpectedCall{{
		Name:      approvedMCPToolName,
		Arguments: json.RawMessage(`{"message":"wait for a client decision"}`),
		Result: json.RawMessage(
			`{"content":[{"type":"text","text":"cancelled by turn interrupt"}],"structuredContent":{"clientAction":"cancel"},"isError":false}`,
		),
		Elicitation: &scriptedmcp.ExpectedElicitation{
			ID: json.RawMessage(`"elicitation-a07-pending"`),
			Params: json.RawMessage(
				`{"_meta":{"agentserver_execution_id":"execution-a07-pending"},"message":"Approve before interrupt?","requestedSchema":{"type":"object","properties":{"confirmed":{"type":"boolean"}},"required":["confirmed"]}}`,
			),
			Response: json.RawMessage(`{"action":"cancel"}`),
		},
	}})

	writeScriptedModelConfigWithOptions(t, paths.codexHome, modelServer.URL(), scriptedModelConfigOptions{
		disableUpdatePlan: true,
		mcpServerURL:      mcpServer.URL(),
		mcpEnabledTools:   []string{approvedMCPToolName},
	})
	process := startPreparedLiveCodex(t, binary, paths, "app-server", "--listen", "stdio://", "--strict-config")
	initializeAppServer(t, process)
	collector := newRPCCollector(process)
	thread, turn := startMinimalAppServerTurnWithApprovalPolicy(
		t,
		collector,
		paths.cwd,
		"interrupt while the executor is waiting for product approval",
		granularMCPApprovalPolicy(),
	)
	reverseRequest := collector.request(t, "mcpServer/elicitation/request")
	var requestParams struct {
		ThreadID   string `json:"threadId"`
		TurnID     string `json:"turnId"`
		ServerName string `json:"serverName"`
		Mode       string `json:"mode"`
	}
	if err := reverseRequest.DecodeParams(&requestParams); err != nil {
		t.Fatal(err)
	}
	if requestParams.ThreadID != thread.Thread.ID || requestParams.TurnID != turn.ID ||
		requestParams.ServerName != "executor" || requestParams.Mode != "form" {
		t.Fatalf("unexpected pending MCP elicitation: %+v", requestParams)
	}

	sendRPC(t, process, map[string]any{
		"id":     4,
		"method": "turn/interrupt",
		"params": map[string]any{
			"threadId": thread.Thread.ID,
			"turnId":   turn.ID,
		},
	})
	var interruptResult struct{}
	mustDecodeResult(t, collector.response(t, "4"), &interruptResult)
	terminal, resolvedBeforeTerminal := collectInterruptedTurnAndResolvedRequest(
		t, collector, thread.Thread.ID, reverseRequest.ID,
	)
	if terminal.ThreadID != thread.Thread.ID || terminal.Turn.ID != turn.ID ||
		terminal.Turn.Status != "interrupted" {
		t.Fatalf("unexpected interrupted terminal turn: %+v", terminal)
	}
	t.Logf("pending elicitation resolved before interrupted terminal: %t", resolvedBeforeTerminal)
	closeAndWait(t, process)

	if failures := modelServer.Failures(); len(failures) != 0 {
		t.Fatalf("scripted model server failures: %v", failures)
	}
	if requests := modelServer.Requests(); len(requests) != 1 {
		t.Fatalf("interrupted turn sent %d model requests, want only the pre-interrupt call", len(requests))
	}
	if failures := mcpServer.Failures(); len(failures) != 0 {
		t.Fatalf("scripted MCP server failures: %v", failures)
	}
	if calls := mcpServer.Calls(); len(calls) != 1 || calls[0].Name != approvedMCPToolName {
		t.Fatalf("scripted MCP calls after interrupt = %+v, want only the pending call", calls)
	}
	responses := mcpServer.ElicitationResponses()
	if len(responses) != 1 {
		t.Fatalf("scripted MCP elicitation responses = %+v, want one interrupt cancellation", responses)
	}
	var response struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(responses[0].Result, &response); err != nil {
		t.Fatal(err)
	}
	if response.Action != "cancel" {
		t.Fatalf("interrupted MCP elicitation action = %q, want cancel", response.Action)
	}
}

type interruptedTurnResolution struct {
	ThreadID string        `json:"threadId"`
	Turn     appServerTurn `json:"turn"`
}

func collectInterruptedTurnAndResolvedRequest(
	t *testing.T,
	collector *rpcCollector,
	threadID string,
	requestID json.RawMessage,
) (interruptedTurnResolution, bool) {
	t.Helper()
	var terminal interruptedTurnResolution
	terminalSeen := false
	resolvedSeen := false
	resolvedBeforeTerminal := false
	for notificationIndex := 0; notificationIndex < 128; notificationIndex++ {
		notification := collector.nextNotification(t)
		switch notification.Method {
		case "turn/completed":
			if err := notification.DecodeParams(&terminal); err != nil {
				t.Fatal(err)
			}
			terminalSeen = true
		case "serverRequest/resolved":
			var resolved struct {
				ThreadID  string          `json:"threadId"`
				RequestID json.RawMessage `json:"requestId"`
			}
			if err := notification.DecodeParams(&resolved); err != nil {
				t.Fatal(err)
			}
			var gotID any
			var wantID any
			if json.Unmarshal(resolved.RequestID, &gotID) != nil ||
				json.Unmarshal(requestID, &wantID) != nil ||
				resolved.ThreadID != threadID || !reflect.DeepEqual(gotID, wantID) {
				t.Fatalf("unexpected serverRequest/resolved after interrupt: %+v, want thread=%q request=%s", resolved, threadID, requestID)
			}
			resolvedBeforeTerminal = !terminalSeen
			resolvedSeen = true
		}
		if terminalSeen && resolvedSeen {
			return terminal, resolvedBeforeTerminal
		}
	}
	t.Fatal("interrupted turn and serverRequest/resolved were not both found in 128 notifications")
	return interruptedTurnResolution{}, false
}

func runA06MCPFormElicitation(
	t *testing.T,
	caseID string,
	expectedAction string,
	clientContent any,
	approvalPolicy any,
	expectClientRequest bool,
) {
	t.Helper()
	binary, paths := prepareLiveCodex(t)
	requireCandidateReleaseOneOf(t, binary, paths, "0.146.0-alpha.14", "0.146.0")
	callID := "call-a06-" + caseID
	toolArguments := fmt.Sprintf(`{"message":"request product approval: %s"}`, caseID)
	toolCall, err := scriptedmodel.NamespacedFunctionCall(
		"response-a06-call-"+caseID,
		callID,
		executorMCPNamespace,
		approvedMCPToolName,
		toolArguments,
	)
	if err != nil {
		t.Fatal(err)
	}
	finalResponse, err := scriptedmodel.AssistantMessage(
		"response-a06-final-"+caseID,
		"message-a06-final-"+caseID,
		conformanceFinalText,
	)
	if err != nil {
		t.Fatal(err)
	}
	modelServer, err := scriptedmodel.Start(scriptedmodel.Config{
		Responses: []scriptedmodel.Response{toolCall, finalResponse},
	})
	if err != nil {
		t.Fatalf("start loopback scripted model: %v", err)
	}
	t.Cleanup(modelServer.Close)

	executionID := "execution-a06-" + caseID
	elicitationID := fmt.Sprintf(`"elicitation-a06-%s"`, caseID)
	elicitationParams := json.RawMessage(fmt.Sprintf(
		`{"_meta":{"agentserver_execution_id":%q},"message":"Approve this deterministic execution?","requestedSchema":{"type":"object","properties":{"confirmed":{"type":"boolean","title":"Confirm execution"}},"required":["confirmed"]}}`,
		executionID,
	))
	expectedResponse := json.RawMessage(fmt.Sprintf(`{"action":%q}`, expectedAction))
	if clientContent != nil {
		expectedResponse = json.RawMessage(fmt.Sprintf(`{"action":%q,"content":{"confirmed":true}}`, expectedAction))
	}
	toolResult := json.RawMessage(fmt.Sprintf(
		`{"content":[{"type":"text","text":"client action: %s"}],"structuredContent":{"clientAction":%q},"isError":false}`,
		expectedAction,
		expectedAction,
	))
	mcpServer := startDestructiveExecutorMCPServer(t, []scriptedmcp.ExpectedCall{{
		Name:      approvedMCPToolName,
		Arguments: json.RawMessage(toolArguments),
		Result:    toolResult,
		Elicitation: &scriptedmcp.ExpectedElicitation{
			ID:       json.RawMessage(elicitationID),
			Params:   elicitationParams,
			Response: expectedResponse,
		},
	}})

	writeScriptedModelConfigWithOptions(t, paths.codexHome, modelServer.URL(), scriptedModelConfigOptions{
		disableUpdatePlan: true,
		mcpServerURL:      mcpServer.URL(),
		mcpEnabledTools:   []string{approvedMCPToolName},
	})
	process := startPreparedLiveCodex(t, binary, paths, "app-server", "--listen", "stdio://", "--strict-config")
	initializeAppServer(t, process)
	collector := newRPCCollector(process)
	thread, turn := startMinimalAppServerTurnWithApprovalPolicy(
		t,
		collector,
		paths.cwd,
		"call the executor and preserve its product approval decision",
		approvalPolicy,
	)

	if expectClientRequest {
		reverseRequest := collector.request(t, "mcpServer/elicitation/request")
		var requestParams struct {
			ThreadID   string `json:"threadId"`
			TurnID     string `json:"turnId"`
			ServerName string `json:"serverName"`
			Mode       string `json:"mode"`
			Meta       struct {
				ExecutionID string `json:"agentserver_execution_id"`
			} `json:"_meta"`
			Message         string `json:"message"`
			RequestedSchema struct {
				Type       string `json:"type"`
				Properties map[string]struct {
					Type  string `json:"type"`
					Title string `json:"title"`
				} `json:"properties"`
				Required []string `json:"required"`
			} `json:"requestedSchema"`
		}
		if err := reverseRequest.DecodeParams(&requestParams); err != nil {
			t.Fatal(err)
		}
		confirmed := requestParams.RequestedSchema.Properties["confirmed"]
		if requestParams.ThreadID != thread.Thread.ID || requestParams.TurnID != turn.ID ||
			requestParams.ServerName != "executor" || requestParams.Mode != "form" ||
			requestParams.Meta.ExecutionID != executionID ||
			requestParams.Message != "Approve this deterministic execution?" ||
			requestParams.RequestedSchema.Type != "object" ||
			confirmed.Type != "boolean" || confirmed.Title != "Confirm execution" ||
			!reflect.DeepEqual(requestParams.RequestedSchema.Required, []string{"confirmed"}) {
			t.Fatalf("unexpected forwarded MCP elicitation: %+v", requestParams)
		}
		sendRPC(t, process, map[string]any{
			"id": reverseRequest.ID,
			"result": map[string]any{
				"action":  expectedAction,
				"content": clientContent,
			},
		})
		assertServerRequestResolvedBeforeTurnCompleted(
			t,
			collector,
			thread.Thread.ID,
			reverseRequest.ID,
		)
	}

	// The ordinary collector paths reject any reverse request. In the never
	// control this is also the assertion that Codex declined internally rather
	// than surfacing the elicitation to the app-server client.
	assertAgentItemCompleted(t, collector, thread.Thread.ID, turn.ID, conformanceFinalText)
	collector.notification(t, "turn/completed")
	closeAndWait(t, process)

	if failures := modelServer.Failures(); len(failures) != 0 {
		t.Fatalf("scripted model server failures: %v", failures)
	}
	modelRequests := modelServer.Requests()
	if len(modelRequests) != 2 {
		t.Fatalf("scripted model received %d requests, want two", len(modelRequests))
	}
	second := decodeCapturedModelRequest(t, modelRequests[1])
	if !modelInputContainsFunctionOutput(second.Input, callID, `"clientAction":"`+expectedAction+`"`) {
		encodedInput, encodeErr := json.Marshal(second.Input)
		if encodeErr != nil {
			t.Fatalf("encode second model input after missing elicitation result: %v", encodeErr)
		}
		t.Fatalf("second model request omitted elicitation-controlled tool result: input=%s", encodedInput)
	}
	if failures := mcpServer.Failures(); len(failures) != 0 {
		t.Fatalf("scripted MCP server failures: %v", failures)
	}
	if calls := mcpServer.Calls(); len(calls) != 1 || calls[0].Name != approvedMCPToolName {
		t.Fatalf("scripted MCP calls = %+v, want one eliciting call", calls)
	}
	responses := mcpServer.ElicitationResponses()
	if len(responses) != 1 {
		t.Fatalf("scripted MCP elicitation responses = %+v, want one", responses)
	}
	var observed struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(responses[0].Result, &observed); err != nil {
		t.Fatal(err)
	}
	if observed.Action != expectedAction {
		t.Fatalf("MCP elicitation action = %q, want %q", observed.Action, expectedAction)
	}
	assertMCPBootstrap(t, mcpServer)
}

func assertServerRequestResolvedBeforeTurnCompleted(
	t *testing.T,
	collector *rpcCollector,
	threadID string,
	requestID json.RawMessage,
) {
	t.Helper()
	for notificationIndex := 0; notificationIndex < 64; notificationIndex++ {
		notification := collector.nextNotification(t)
		switch notification.Method {
		case "turn/completed":
			t.Fatal("turn completed before serverRequest/resolved")
		case "serverRequest/resolved":
			var resolved struct {
				ThreadID  string          `json:"threadId"`
				RequestID json.RawMessage `json:"requestId"`
			}
			if err := notification.DecodeParams(&resolved); err != nil {
				t.Fatal(err)
			}
			var gotID any
			var wantID any
			if json.Unmarshal(resolved.RequestID, &gotID) != nil ||
				json.Unmarshal(requestID, &wantID) != nil ||
				resolved.ThreadID != threadID || !reflect.DeepEqual(gotID, wantID) {
				t.Fatalf("unexpected serverRequest/resolved: %+v, want thread=%q request=%s", resolved, threadID, requestID)
			}
			return
		}
	}
	t.Fatal("serverRequest/resolved not found in the first 64 notifications")
}

func startDestructiveExecutorMCPServer(
	t *testing.T,
	expectedCalls []scriptedmcp.ExpectedCall,
) *scriptedmcp.Server {
	t.Helper()
	inputSchema := json.RawMessage(
		`{"type":"object","properties":{"message":{"type":"string"}},"required":["message"],"additionalProperties":false}`,
	)
	server, err := scriptedmcp.Start(scriptedmcp.Config{
		Tools: []scriptedmcp.Tool{
			{
				Name:        approvedMCPToolName,
				Description: "Execute one policy-approved deterministic instruction.",
				InputSchema: inputSchema,
				Annotations: json.RawMessage(`{"readOnlyHint":false,"destructiveHint":true,"openWorldHint":true}`),
			},
		},
		ExpectedCalls: expectedCalls,
	})
	if err != nil {
		t.Fatalf("start destructive scripted MCP: %v", err)
	}
	t.Cleanup(server.Close)
	return server
}

func granularMCPApprovalPolicy() map[string]any {
	return map[string]any{
		"granular": map[string]any{
			"sandbox_approval":    false,
			"rules":               false,
			"skill_approval":      false,
			"request_permissions": false,
			"mcp_elicitations":    true,
		},
	}
}

// TestAppServerA03Codex0146ExecutesMCPResourceHandler proves the generic
// resource surface is executable, rather than harmless schema residue. The
// call reaches resources/list on the MCP server even though enabled_tools only
// contains approved_echo.
func TestAppServerA03Codex0146ExecutesMCPResourceHandler(t *testing.T) {
	binary, paths := prepareLiveCodex(t)
	requireCandidateReleaseOneOf(t, binary, paths, "0.146.0-alpha.14", "0.146.0")
	resourceCall, err := scriptedmodel.FunctionCall(
		"response-a03-resource-call",
		"call-a03-resource-call",
		"list_mcp_resources",
		`{"server":"executor"}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	finalResponse, err := scriptedmodel.AssistantMessage(
		"response-a03-resource-final",
		"message-a03-resource-final",
		conformanceFinalText,
	)
	if err != nil {
		t.Fatal(err)
	}
	modelServer, err := scriptedmodel.Start(scriptedmodel.Config{
		Responses: []scriptedmodel.Response{resourceCall, finalResponse},
	})
	if err != nil {
		t.Fatalf("start loopback scripted model: %v", err)
	}
	t.Cleanup(modelServer.Close)
	mcpServer := startExecutorMCPServer(t, nil)

	writeScriptedModelConfigWithOptions(t, paths.codexHome, modelServer.URL(), scriptedModelConfigOptions{
		disableUpdatePlan: true,
		mcpServerURL:      mcpServer.URL(),
		mcpEnabledTools:   []string{approvedMCPToolName},
	})
	process := startPreparedLiveCodex(t, binary, paths, "app-server", "--listen", "stdio://", "--strict-config")
	initializeAppServer(t, process)
	collector := newRPCCollector(process)
	thread, turn := startMinimalAppServerTurn(t, collector, paths.cwd, "attempt the non-allowlisted MCP resource handler")
	assertAgentItemCompleted(t, collector, thread.Thread.ID, turn.ID, conformanceFinalText)
	collector.notification(t, "turn/completed")
	closeAndWait(t, process)

	if failures := modelServer.Failures(); len(failures) != 0 {
		t.Fatalf("scripted model server failures: %v", failures)
	}
	modelRequests := modelServer.Requests()
	if len(modelRequests) != 2 {
		t.Fatalf("scripted model received %d requests, want two", len(modelRequests))
	}
	second := decodeCapturedModelRequest(t, modelRequests[1])
	if !modelInputContainsFunctionOutput(second.Input, "call-a03-resource-call", "resources/list failed") {
		encodedInput, encodeErr := json.Marshal(second.Input)
		if encodeErr != nil {
			t.Fatalf("encode second model input after missing resource failure: %v", encodeErr)
		}
		t.Fatalf("resource handler result was not returned to the model: input=%s", encodedInput)
	}
	requests := mcpServer.Requests()
	if len(requests) != 4 || requests[3].RPCMethod != "resources/list" {
		t.Fatalf("scripted MCP request methods = %v, want bootstrap followed by resources/list", mcpRequestMethods(requests))
	}
	if calls := mcpServer.Calls(); len(calls) != 0 {
		t.Fatalf("generic resource handler unexpectedly appeared as tools/call: %+v", calls)
	}
	failures := mcpServer.Failures()
	if len(failures) != 1 || !strings.Contains(failures[0], "unsupported MCP method resources/list") {
		t.Fatalf("scripted MCP failures = %v, want the fail-closed resources/list observation", failures)
	}
	t.Log("A03 blocker is executable: list_mcp_resources reached resources/list outside the MCP enabled_tools allowlist")
}

func TestAppServerA03Codex0146RejectsUnregisteredCallsBeforeMCP(t *testing.T) {
	binary, paths := prepareLiveCodex(t)
	requireCandidateReleaseOneOf(t, binary, paths, "0.146.0-alpha.14", "0.146.0")
	blockedMCPCall, err := scriptedmodel.NamespacedFunctionCall(
		"response-a03-blocked-mcp",
		"call-a03-blocked-mcp",
		executorMCPNamespace,
		blockedMCPToolName,
		`{"message":"must not execute"}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	blockedShellCall, err := scriptedmodel.FunctionCall(
		"response-a03-blocked-shell",
		"call-a03-blocked-shell",
		"exec_command",
		`{"cmd":"must-not-execute"}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	finalResponse, err := scriptedmodel.AssistantMessage(
		"response-a03-blocked-final",
		"message-a03-blocked-final",
		conformanceFinalText,
	)
	if err != nil {
		t.Fatal(err)
	}
	modelServer, err := scriptedmodel.Start(scriptedmodel.Config{
		Responses: []scriptedmodel.Response{blockedMCPCall, blockedShellCall, finalResponse},
	})
	if err != nil {
		t.Fatalf("start loopback scripted model: %v", err)
	}
	t.Cleanup(modelServer.Close)
	mcpServer := startExecutorMCPServer(t, nil)

	writeScriptedModelConfigWithOptions(t, paths.codexHome, modelServer.URL(), scriptedModelConfigOptions{
		disableUpdatePlan: true,
		mcpServerURL:      mcpServer.URL(),
		mcpEnabledTools:   []string{approvedMCPToolName},
	})
	process := startPreparedLiveCodex(t, binary, paths, "app-server", "--listen", "stdio://", "--strict-config")
	initializeAppServer(t, process)
	collector := newRPCCollector(process)
	thread, turn := startMinimalAppServerTurn(t, collector, paths.cwd, "attempt calls omitted from the captured tool surface")
	assertAgentItemCompleted(t, collector, thread.Thread.ID, turn.ID, conformanceFinalText)
	collector.notification(t, "turn/completed")
	closeAndWait(t, process)

	if failures := modelServer.Failures(); len(failures) != 0 {
		t.Fatalf("scripted model server failures: %v", failures)
	}
	modelRequests := modelServer.Requests()
	if len(modelRequests) != 3 {
		t.Fatalf("scripted model received %d requests, want three", len(modelRequests))
	}
	second := decodeCapturedModelRequest(t, modelRequests[1])
	if !modelInputContainsFunctionOutput(second.Input, "call-a03-blocked-mcp", "unsupported call") {
		t.Fatalf("blocked MCP call did not receive an unsupported-call result: input=%s", encodeModelInput(t, second.Input))
	}
	third := decodeCapturedModelRequest(t, modelRequests[2])
	if !modelInputContainsFunctionOutput(third.Input, "call-a03-blocked-shell", "unsupported call") {
		t.Fatalf("blocked shell call did not receive an unsupported-call result: input=%s", encodeModelInput(t, third.Input))
	}
	if failures := mcpServer.Failures(); len(failures) != 0 {
		t.Fatalf("scripted MCP server failures: %v", failures)
	}
	if calls := mcpServer.Calls(); len(calls) != 0 {
		t.Fatalf("unregistered calls reached tools/call: %+v", calls)
	}
	requests := mcpServer.Requests()
	if len(requests) != 3 {
		t.Fatalf("unregistered calls escaped to MCP: methods=%v", mcpRequestMethods(requests))
	}
	assertMCPBootstrap(t, mcpServer)
}

func startExecutorMCPServer(t *testing.T, expectedCalls []scriptedmcp.ExpectedCall) *scriptedmcp.Server {
	t.Helper()
	inputSchema := json.RawMessage(
		`{"type":"object","properties":{"message":{"type":"string"}},"required":["message"],"additionalProperties":false}`,
	)
	server, err := scriptedmcp.Start(scriptedmcp.Config{
		Tools: []scriptedmcp.Tool{
			{
				Name:        approvedMCPToolName,
				Description: "Echo one approved deterministic executor instruction.",
				InputSchema: inputSchema,
			},
			{
				Name:        blockedMCPToolName,
				Description: "A tool intentionally excluded by the Codex MCP allowlist.",
				InputSchema: inputSchema,
			},
		},
		ExpectedCalls: expectedCalls,
	})
	if err != nil {
		t.Fatalf("start loopback scripted MCP: %v", err)
	}
	t.Cleanup(server.Close)
	return server
}

func startMinimalAppServerTurn(
	t *testing.T,
	collector *rpcCollector,
	cwd string,
	userText string,
) (threadStartResult, appServerTurn) {
	t.Helper()
	return startMinimalAppServerTurnWithApprovalPolicy(t, collector, cwd, userText, "never")
}

func startMinimalAppServerTurnWithApprovalPolicy(
	t *testing.T,
	collector *rpcCollector,
	cwd string,
	userText string,
	approvalPolicy any,
) (threadStartResult, appServerTurn) {
	t.Helper()
	sendRPC(t, collector.process, map[string]any{
		"id":     2,
		"method": "thread/start",
		"params": map[string]any{
			"model":                   conformanceModelName,
			"cwd":                     cwd,
			"approvalPolicy":          approvalPolicy,
			"sandbox":                 "read-only",
			"ephemeral":               false,
			"threadSource":            "user",
			"environments":            []any{},
			"dynamicTools":            []any{},
			"selectedCapabilityRoots": []any{},
		},
	})
	thread := decodeThreadStart(t, collector.response(t, "2"))
	collector.notification(t, "thread/started")
	sendRPC(t, collector.process, map[string]any{
		"id":     3,
		"method": "turn/start",
		"params": map[string]any{
			"threadId":     thread.Thread.ID,
			"environments": []any{},
			"input": []any{
				map[string]any{
					"type":         "text",
					"text":         userText,
					"textElements": []any{},
				},
			},
		},
	})
	turn := decodeTurnStart(t, collector.response(t, "3"))
	collector.notification(t, "turn/started")
	return thread, turn
}

func assertMCPBootstrap(t *testing.T, server *scriptedmcp.Server) {
	t.Helper()
	requests := server.Requests()
	if len(requests) < 3 {
		t.Fatalf("scripted MCP received %d requests, want at least bootstrap sequence", len(requests))
	}
	want := []string{"initialize", "notifications/initialized", "tools/list"}
	for index, method := range want {
		if requests[index].RPCMethod != method {
			t.Fatalf("scripted MCP bootstrap request %d = %q, want %q", index, requests[index].RPCMethod, method)
		}
	}
}

func mcpRequestMethods(requests []scriptedmcp.Request) []string {
	methods := make([]string, len(requests))
	for index, request := range requests {
		methods[index] = request.RPCMethod
	}
	return methods
}

type appServerThread struct {
	ID            string `json:"id"`
	SessionID     string `json:"sessionId"`
	Preview       string `json:"preview"`
	Ephemeral     bool   `json:"ephemeral"`
	ModelProvider string `json:"modelProvider"`
	Path          string `json:"path"`
	Status        struct {
		Type string `json:"type"`
	} `json:"status"`
	CWD          string          `json:"cwd"`
	ThreadSource string          `json:"threadSource"`
	Turns        []appServerTurn `json:"turns"`
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

type scriptedModelConfigOptions struct {
	disableUpdatePlan bool
	mcpServerURL      string
	mcpEnabledTools   []string
	mcpApprovalMode   string
	mcpToolTimeoutSec float64
}

func writeScriptedModelConfig(t *testing.T, codexHome, serverURL string) {
	t.Helper()
	writeScriptedModelConfigWithOptions(t, codexHome, serverURL, scriptedModelConfigOptions{})
}

func writeScriptedModelConfigWithOptions(
	t *testing.T,
	codexHome string,
	serverURL string,
	options scriptedModelConfigOptions,
) {
	t.Helper()
	modelCatalogPath := writeConformanceModelCatalog(t, codexHome)
	config := fmt.Sprintf(`model = %q
approval_policy = "never"
approvals_reviewer = "user"
sandbox_mode = "read-only"
model_provider = "scripted_provider"
model_catalog_json = %q
web_search = "disabled"

[model_providers.scripted_provider]
name = "agentserver v2 scripted provider"
base_url = %q
wire_api = "responses"
request_max_retries = 0
stream_max_retries = 0

[tools.experimental_request_user_input]
enabled = false

[agents]
enabled = false

[orchestrator.skills]
enabled = false

[orchestrator.mcp]
enabled = false

[skills.bundled]
enabled = false

[features]
apps = false
browser_use = false
browser_use_external = false
browser_use_full_cdp_access = false
code_mode = false
code_mode_only = false
computer_use = false
default_mode_request_user_input = false
goals = false
hooks = false
image_generation = false
in_app_browser = false
multi_agent = false
multi_agent_v2 = false
plugins = false
request_permissions_tool = false
shell_tool = false
skill_mcp_dependency_install = false
skill_search = false
standalone_web_search = false
tool_suggest = false
unified_exec = false
workspace_dependencies = false
`, conformanceModelName, modelCatalogPath, serverURL+"/v1")
	if options.disableUpdatePlan {
		config += `
[tools.update_plan]
enabled = false
`
	}
	if options.mcpServerURL != "" {
		if len(options.mcpEnabledTools) == 0 {
			t.Fatal("scripted MCP config requires at least one enabled tool")
		}
		enabledTools, err := json.Marshal(options.mcpEnabledTools)
		if err != nil {
			t.Fatalf("encode scripted MCP enabled tools: %v", err)
		}
		approvalMode := options.mcpApprovalMode
		if approvalMode == "" {
			approvalMode = "approve"
		}
		switch approvalMode {
		case "auto", "prompt", "writes", "approve":
		default:
			t.Fatalf("invalid scripted MCP approval mode %q", approvalMode)
		}
		toolTimeoutSec := options.mcpToolTimeoutSec
		if toolTimeoutSec == 0 {
			toolTimeoutSec = 5
		}
		if toolTimeoutSec <= 0 || toolTimeoutSec > 30 {
			t.Fatalf("scripted MCP tool timeout %g is outside (0, 30] seconds", toolTimeoutSec)
		}
		config += fmt.Sprintf(`
[mcp_servers.executor]
url = %q
required = true
startup_timeout_sec = 5.0
tool_timeout_sec = %g
default_tools_approval_mode = %q
enabled_tools = %s
`, options.mcpServerURL, toolTimeoutSec, approvalMode, enabledTools)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatalf("write scripted model config: %v", err)
	}
}

func writeConformanceModelCatalog(t *testing.T, codexHome string) string {
	t.Helper()
	catalog := map[string]any{
		"models": []any{
			map[string]any{
				"slug":                              conformanceModelName,
				"display_name":                      "agentserver v2 scripted model",
				"description":                       nil,
				"default_reasoning_level":           "medium",
				"supported_reasoning_levels":        []any{},
				"shell_type":                        "disabled",
				"visibility":                        "none",
				"supported_in_api":                  true,
				"priority":                          0,
				"upgrade":                           nil,
				"base_instructions":                 "Return only the scripted model result.",
				"model_messages":                    nil,
				"include_skills_usage_instructions": false,
				"default_reasoning_summary":         "none",
				"support_verbosity":                 false,
				"default_verbosity":                 nil,
				"apply_patch_tool_type":             nil,
				"web_search_tool_type":              "text",
				"truncation_policy":                 map[string]any{"mode": "bytes", "limit": 10_000},
				"supports_parallel_tool_calls":      false,
				"supports_image_detail_original":    false,
				"context_window":                    272_000,
				"max_context_window":                272_000,
				"auto_compact_token_limit":          nil,
				"effective_context_window_percent":  95,
				"experimental_supported_tools":      []any{},
				"input_modalities":                  []string{"text"},
				"supports_search_tool":              false,
				"use_responses_lite":                false,
				"auto_review_model_override":        nil,
				"tool_mode":                         "direct",
				"multi_agent_version":               "disabled",
			},
		},
	}
	contents, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		t.Fatalf("encode conformance model catalog: %v", err)
	}
	path := filepath.Join(codexHome, "model-catalog.json")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("write conformance model catalog: %v", err)
	}
	return path
}

type capturedModelRequest struct {
	Model  string            `json:"model"`
	Input  []json.RawMessage `json:"input"`
	Tools  []json.RawMessage `json:"tools"`
	Stream bool              `json:"stream"`
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

	body := decodeCapturedModelRequest(t, request)
	if body.Model != conformanceModelName || !body.Stream {
		t.Fatalf("unexpected model request envelope: model=%q stream=%t", body.Model, body.Stream)
	}
	if !modelInputContainsUserText(t, body.Input, conformanceUserText) {
		t.Fatalf("model input omitted user text %q", conformanceUserText)
	}
	toolNames := modelToolNames(t, body.Tools)
	t.Logf("candidate model tool surface (A03 not yet asserted): %v", toolNames)
}

func decodeCapturedModelRequest(t *testing.T, request scriptedmodel.Request) capturedModelRequest {
	t.Helper()
	var body capturedModelRequest
	if err := json.Unmarshal(request.Body, &body); err != nil {
		t.Fatalf("decode captured model request: %v", err)
	}
	return body
}

func encodeModelInput(t *testing.T, input []json.RawMessage) []byte {
	t.Helper()
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("encode captured model input: %v", err)
	}
	return encoded
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

func modelInputContainsFunctionOutput(input []json.RawMessage, callID, wantText string) bool {
	for _, raw := range input {
		var item struct {
			Type   string          `json:"type"`
			CallID string          `json:"call_id"`
			Output json.RawMessage `json:"output"`
		}
		if json.Unmarshal(raw, &item) != nil || item.Type != "function_call_output" || item.CallID != callID {
			continue
		}
		var outputText string
		if json.Unmarshal(item.Output, &outputText) == nil && strings.Contains(outputText, wantText) {
			return true
		}
		if strings.Contains(string(item.Output), wantText) {
			return true
		}
	}
	return false
}

func modelToolNames(t *testing.T, tools []json.RawMessage) []string {
	t.Helper()
	names := make([]string, 0, len(tools))
	for _, raw := range tools {
		var tool struct {
			Type  string `json:"type"`
			Name  string `json:"name"`
			Tools []struct {
				Type string `json:"type"`
				Name string `json:"name"`
			} `json:"tools"`
		}
		if err := json.Unmarshal(raw, &tool); err != nil {
			t.Fatalf("decode model tool: %v", err)
		}
		if tool.Type == "namespace" {
			if tool.Name == "" || len(tool.Tools) == 0 {
				names = append(names, "<invalid-namespace>")
				continue
			}
			for _, child := range tool.Tools {
				if child.Name == "" {
					names = append(names, tool.Name+".<"+child.Type+">")
					continue
				}
				names = append(names, tool.Name+"."+child.Name)
			}
		} else if tool.Name != "" {
			names = append(names, tool.Name)
		} else {
			names = append(names, "<"+tool.Type+">")
		}
	}
	sort.Strings(names)
	return names
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
