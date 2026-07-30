package codex_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/conformance/codex/internal/codexprocess"
	"github.com/agentserver/agentserver/v2/conformance/codex/internal/scriptedmcp"
	"github.com/agentserver/agentserver/v2/conformance/codex/internal/scriptedmodel"
	"github.com/agentserver/agentserver/v2/internal/codexwire"
	"github.com/agentserver/agentserver/v2/internal/harnessworker"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	maxA08StateFiles     = 512
	maxA08StateFileBytes = 16 * 1024 * 1024
	maxA08StateTreeBytes = 64 * 1024 * 1024
	maxA08RolloutLine    = 4 * 1024 * 1024

	a09FirstUserText       = "persist a deterministic executor result for the next run"
	a09SecondUserText      = "continue from the restored checkpoint"
	a09FirstAssistantText  = "first checkpoint turn complete"
	a09SecondAssistantText = "restored checkpoint turn complete"
	a09ToolCallID          = "call-a09-checkpoint"
	a09ToolMarker          = "a09-executor-result-preserved"

	a10BaseUserText          = "complete the durable turn before the crash probe"
	a10CrashUserText         = "this accepted turn must be abandoned after the hard crash"
	a10RecoveryUserText      = "start a new turn from the last completed checkpoint"
	a10BaseAssistantText     = "durable pre-crash checkpoint complete"
	a10RecoveryAssistantText = "new post-crash turn complete"

	a11CapabilityEnvName     = "AGENTSERVER_A11_MCP_CAPABILITY"
	a11SourceCapability      = "a11-source-capability-secret-7d30e7"
	a11RestoredCapability    = "a11-restored-capability-secret-4b29c1"
	a11ConfigSecret          = "a11-config-secret-c8e17f"
	a11RequirementsSecret    = "a11-requirements-secret-e51729"
	a11ModelAuthSecret       = "a11-auth-secret-3ad66c"
	a11TokenFileSecret       = "a11-token-file-secret-825fcb"
	a11LogSecret             = "a11-log-secret-9c211a"
	a11EnvironmentDumpSecret = "a11-env-dump-secret-28b7a4"
	a11TransportSecret       = "a11-transport-secret-61f439"
	a11FirstUserText         = "complete a turn while runtime-only secrets stay out of history"
	a11SecondUserText        = "continue after restoring without the prior runtime secrets"
	a11FirstAssistantText    = "secret-exclusion checkpoint complete"
	a11SecondAssistantText   = "secret-free restored turn complete"
	a11ToolCallID            = "call-a11-secret-exclusion"
	a11ToolMarker            = "a11-model-visible-tool-result"
)

type stateFileSnapshot struct {
	Mode   os.FileMode
	Size   int64
	SHA256 string
}

type secretSentinel struct {
	Label string
	Value string
}

// TestAppServerA08GracefulShutdownStabilizesState establishes stdin EOF plus
// clean process exit as the persistence barrier. The probe never snapshots a
// live CODEX_HOME and uses no fixed sleep: after Wait reports a zero exit, two
// bounded tree reads must agree byte-for-byte, the reported rollout must be a
// complete JSONL file, and the state database must have a SQLite header.
func TestAppServerA08GracefulShutdownStabilizesState(t *testing.T) {
	binary, paths := prepareLiveCodex(t)
	requireCandidateReleaseOneOf(t, binary, paths, "0.146.0-alpha.14", "0.146.0")
	response, err := scriptedmodel.AssistantMessage(
		"response-a08-graceful-shutdown",
		"message-a08-graceful-shutdown",
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
	thread, turn := startMinimalAppServerTurn(
		t,
		collector,
		paths.cwd,
		"complete a persisted turn before graceful shutdown",
	)
	assertAgentItemCompleted(t, collector, thread.Thread.ID, turn.ID, conformanceFinalText)
	collector.notification(t, "turn/completed")

	// This turn issues no server requests, so the worker's outstanding set is
	// empty at terminal. EOF and Wait are the only persistence barrier.
	closeAndWait(t, process)

	first := snapshotStateTree(t, paths.codexHome)
	second := snapshotStateTree(t, paths.codexHome)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("CODEX_HOME changed after clean process exit:\nfirst=%v\nsecond=%v", first, second)
	}
	if len(first) == 0 {
		t.Fatal("clean app-server exit left an empty CODEX_HOME")
	}

	rolloutRelative := stateRelativePath(t, paths.codexHome, thread.Thread.Path)
	rollout, exists := first[rolloutRelative]
	if !exists {
		t.Fatalf("thread rollout %q is absent from the stable state tree", rolloutRelative)
	}
	if !strings.HasSuffix(rolloutRelative, ".jsonl") || rollout.Size == 0 {
		t.Fatalf("thread rollout snapshot = %+v at %q, want non-empty JSONL", rollout, rolloutRelative)
	}
	assertCompleteRolloutJSONL(
		t,
		thread.Thread.Path,
		thread.Thread.ID,
		"complete a persisted turn before graceful shutdown",
		conformanceFinalText,
	)

	stateDatabase := filepath.Join(paths.codexHome, "state_5.sqlite")
	stateRelative := stateRelativePath(t, paths.codexHome, stateDatabase)
	if _, exists := first[stateRelative]; !exists {
		t.Fatalf("stable state tree omitted %q", stateRelative)
	}
	assertSQLiteHeader(t, stateDatabase)

	if failures := modelServer.Failures(); len(failures) != 0 {
		t.Fatalf("scripted model server failures: %v", failures)
	}
	if requests := modelServer.Requests(); len(requests) != 1 {
		t.Fatalf("scripted model received %d requests, want one", len(requests))
	}
	t.Logf("A08 stable post-exit state: %s", formatStateTree(first))
}

// TestAppServerA09CompletedTurnResumeControl isolates the stock cold-resume
// path from MCP history and checkpoint relocation. A failure here means A09
// cannot attribute the problem to the persisted executor result or file set.
func TestAppServerA09CompletedTurnResumeControl(t *testing.T) {
	binary, paths := prepareLiveCodex(t)
	requireCandidateReleaseOneOf(t, binary, paths, "0.146.0-alpha.14", "0.146.0")
	firstFinal, err := scriptedmodel.AssistantMessage(
		"response-a09-control-first",
		"message-a09-control-first",
		a09FirstAssistantText,
	)
	if err != nil {
		t.Fatal(err)
	}
	firstModelServer, err := scriptedmodel.Start(scriptedmodel.Config{
		Responses: []scriptedmodel.Response{firstFinal},
	})
	if err != nil {
		t.Fatalf("start first control model: %v", err)
	}
	t.Cleanup(firstModelServer.Close)
	writeScriptedModelConfigWithOptions(t, paths.codexHome, firstModelServer.URL(), scriptedModelConfigOptions{
		disableUpdatePlan: true,
	})
	firstProcess := startPreparedLiveCodex(t, binary, paths, "app-server", "--listen", "stdio://", "--strict-config")
	initializeAppServer(t, firstProcess)
	firstCollector := newRPCCollector(firstProcess)
	thread, firstTurn := startMinimalAppServerTurn(t, firstCollector, paths.cwd, a09FirstUserText)
	assertAgentItemCompleted(t, firstCollector, thread.Thread.ID, firstTurn.ID, a09FirstAssistantText)
	firstCollector.notification(t, "turn/completed")
	closeAndWait(t, firstProcess)

	secondFinal, err := scriptedmodel.AssistantMessage(
		"response-a09-control-second",
		"message-a09-control-second",
		a09SecondAssistantText,
	)
	if err != nil {
		t.Fatal(err)
	}
	secondModelServer, err := scriptedmodel.Start(scriptedmodel.Config{
		Responses: []scriptedmodel.Response{secondFinal},
	})
	if err != nil {
		t.Fatalf("start second control model: %v", err)
	}
	t.Cleanup(secondModelServer.Close)
	writeScriptedModelConfigWithOptions(t, paths.codexHome, secondModelServer.URL(), scriptedModelConfigOptions{
		disableUpdatePlan: true,
	})
	secondProcess := startPreparedLiveCodex(t, binary, paths, "app-server", "--listen", "stdio://", "--strict-config")
	initializeAppServer(t, secondProcess)
	secondCollector := newRPCCollector(secondProcess)
	resumed, secondTurn := resumeAppServerThreadAndStartTurn(
		t,
		secondCollector,
		thread.Thread.ID,
		"",
		paths.cwd,
		a09SecondUserText,
	)
	if len(resumed.Thread.Turns) != 0 {
		t.Fatalf("metadata-only control resume returned turns: %+v", resumed.Thread.Turns)
	}
	assertAgentItemCompleted(t, secondCollector, resumed.Thread.ID, secondTurn.ID, a09SecondAssistantText)
	secondCollector.notification(t, "turn/completed")
	closeAndWait(t, secondProcess)

	if failures := firstModelServer.Failures(); len(failures) != 0 {
		t.Fatalf("first control model failures: %v", failures)
	}
	if failures := secondModelServer.Failures(); len(failures) != 0 {
		t.Fatalf("second control model failures: %v", failures)
	}
	requests := secondModelServer.Requests()
	if len(requests) != 1 {
		t.Fatalf("second control model received %d requests, want one", len(requests))
	}
	restoredRequest := decodeCapturedModelRequest(t, requests[0])
	if !modelInputContainsUserText(t, restoredRequest.Input, a09FirstUserText) ||
		!modelInputContainsUserText(t, restoredRequest.Input, a09SecondUserText) {
		t.Fatalf("control resume omitted first or second user input: input=%s", encodeModelInput(t, restoredRequest.Input))
	}
}

// TestAppServerA09RolloutOnlyCheckpointRoundTrip proves that a completed
// thread can be restored into a fresh CODEX_HOME from its rollout alone.
// The source directory is renamed out of the original absolute path before
// resume, the manifest-relative rollout path is rebuilt under the new root,
// configuration is regenerated, and the next model request must still contain
// the first user turn and the exact MCP function result.
func TestAppServerA09RolloutOnlyCheckpointRoundTrip(t *testing.T) {
	binary, sourcePaths := prepareLiveCodex(t)
	requireCandidateReleaseOneOf(t, binary, sourcePaths, "0.146.0-alpha.14", "0.146.0")
	toolCall, err := scriptedmodel.NamespacedFunctionCall(
		"response-a09-tool-call",
		a09ToolCallID,
		executorMCPNamespace,
		approvedMCPToolName,
		`{"message":"persist this executor result"}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	firstFinal, err := scriptedmodel.AssistantMessage(
		"response-a09-first-final",
		"message-a09-first-final",
		a09FirstAssistantText,
	)
	if err != nil {
		t.Fatal(err)
	}
	firstModelServer, err := scriptedmodel.Start(scriptedmodel.Config{
		Responses: []scriptedmodel.Response{toolCall, firstFinal},
	})
	if err != nil {
		t.Fatalf("start first loopback scripted model: %v", err)
	}
	t.Cleanup(firstModelServer.Close)
	mcpServer := startDestructiveExecutorMCPServer(t, []scriptedmcp.ExpectedCall{{
		Name:      approvedMCPToolName,
		Arguments: json.RawMessage(`{"message":"persist this executor result"}`),
		Result: json.RawMessage(
			`{"content":[{"type":"text","text":"checkpoint result recorded"}],"structuredContent":{"checkpointMarker":"a09-executor-result-preserved"},"isError":false}`,
		),
	}})

	writeScriptedModelConfigWithOptions(t, sourcePaths.codexHome, firstModelServer.URL(), scriptedModelConfigOptions{
		disableUpdatePlan: true,
		mcpServerURL:      mcpServer.URL(),
		mcpEnabledTools:   []string{approvedMCPToolName},
	})
	firstProcess := startPreparedLiveCodex(t, binary, sourcePaths, "app-server", "--listen", "stdio://", "--strict-config")
	initializeAppServer(t, firstProcess)
	firstCollector := newRPCCollector(firstProcess)
	thread, firstTurn := startMinimalAppServerTurn(
		t,
		firstCollector,
		sourcePaths.cwd,
		a09FirstUserText,
	)
	assertAgentItemCompleted(t, firstCollector, thread.Thread.ID, firstTurn.ID, a09FirstAssistantText)
	firstCollector.notification(t, "turn/completed")
	closeAndWait(t, firstProcess)

	if failures := firstModelServer.Failures(); len(failures) != 0 {
		t.Fatalf("first scripted model server failures: %v", failures)
	}
	firstRequests := firstModelServer.Requests()
	if len(firstRequests) != 2 {
		t.Fatalf("first scripted model received %d requests, want two", len(firstRequests))
	}
	firstFollowup := decodeCapturedModelRequest(t, firstRequests[1])
	if !modelInputContainsFunctionOutput(firstFollowup.Input, a09ToolCallID, a09ToolMarker) {
		t.Fatalf("first turn omitted MCP result before checkpoint: input=%s", encodeModelInput(t, firstFollowup.Input))
	}
	if failures := mcpServer.Failures(); len(failures) != 0 {
		t.Fatalf("scripted MCP server failures before restore: %v", failures)
	}
	if calls := mcpServer.Calls(); len(calls) != 1 || calls[0].Name != approvedMCPToolName {
		t.Fatalf("scripted MCP calls before restore = %+v, want one", calls)
	}

	sourceSnapshot := snapshotStateTree(t, sourcePaths.codexHome)
	rolloutRelative := stateRelativePath(t, sourcePaths.codexHome, thread.Thread.Path)
	rolloutSnapshot, exists := sourceSnapshot[rolloutRelative]
	if !exists {
		t.Fatalf("source checkpoint omitted rollout %q", rolloutRelative)
	}
	if !strings.HasSuffix(rolloutRelative, ".jsonl") || rolloutSnapshot.Size == 0 {
		t.Fatalf("checkpoint rollout snapshot = %+v at %q, want non-empty JSONL", rolloutSnapshot, rolloutRelative)
	}
	assertCompleteRolloutJSONL(
		t,
		thread.Thread.Path,
		thread.Thread.ID,
		a09FirstUserText,
		a09FirstAssistantText,
		a09ToolMarker,
	)

	restoredPaths := createRestoredLivePaths(t, t.TempDir(), "runtime")
	copyCheckpointFile(
		t,
		sourcePaths.codexHome,
		restoredPaths.codexHome,
		rolloutRelative,
		rolloutSnapshot,
	)
	checkpointSnapshot := snapshotStateTree(t, restoredPaths.codexHome)
	wantCheckpointSnapshot := map[string]stateFileSnapshot{rolloutRelative: rolloutSnapshot}
	if !reflect.DeepEqual(checkpointSnapshot, wantCheckpointSnapshot) {
		t.Fatalf("rollout-only checkpoint contains unexpected files: got=%v want=%v", checkpointSnapshot, wantCheckpointSnapshot)
	}
	restoredRolloutPath := filepath.Join(restoredPaths.codexHome, filepath.FromSlash(rolloutRelative))
	retiredSourceHome := filepath.Join(sourcePaths.root, "retired-source-codex-home")
	if err := os.Rename(sourcePaths.codexHome, retiredSourceHome); err != nil {
		t.Fatalf("retire source CODEX_HOME before restore: %v", err)
	}
	if _, err := os.Stat(thread.Thread.Path); !os.IsNotExist(err) {
		t.Fatalf("source rollout path remained available after retirement: %v", err)
	}

	secondFinal, err := scriptedmodel.AssistantMessage(
		"response-a09-second-final",
		"message-a09-second-final",
		a09SecondAssistantText,
	)
	if err != nil {
		t.Fatal(err)
	}
	secondModelServer, err := scriptedmodel.Start(scriptedmodel.Config{
		Responses: []scriptedmodel.Response{secondFinal},
	})
	if err != nil {
		t.Fatalf("start restored loopback scripted model: %v", err)
	}
	t.Cleanup(secondModelServer.Close)
	restoredMCPServer := startDestructiveExecutorMCPServer(t, nil)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("A09 restored MCP requests: %v", mcpRequestMethods(restoredMCPServer.Requests()))
			t.Logf("A09 restored MCP failures: %v", restoredMCPServer.Failures())
			t.Logf("A09 restored model requests: %d failures: %v", len(secondModelServer.Requests()), secondModelServer.Failures())
		}
	})
	missingPaths := createRestoredLivePaths(t, t.TempDir(), "missing-runtime")
	writeScriptedModelConfigWithOptions(t, missingPaths.codexHome, secondModelServer.URL(), scriptedModelConfigOptions{
		disableUpdatePlan: true,
		mcpServerURL:      restoredMCPServer.URL(),
		mcpEnabledTools:   []string{approvedMCPToolName},
	})
	missingProcess := startPreparedLiveCodex(t, binary, missingPaths, "app-server", "--listen", "stdio://", "--strict-config")
	initializeAppServer(t, missingProcess)
	missingCollector := newRPCCollector(missingProcess)
	missingRolloutPath := filepath.Join(missingPaths.codexHome, filepath.FromSlash(rolloutRelative))
	sendRPC(t, missingProcess, map[string]any{
		"id":     2,
		"method": "thread/resume",
		"params": map[string]any{
			"threadId":     thread.Thread.ID,
			"path":         missingRolloutPath,
			"excludeTurns": true,
		},
	})
	missingResponse := missingCollector.response(t, "2")
	if missingResponse.Kind != codexwire.KindError || missingResponse.Error == nil ||
		missingResponse.Error.Code != -32600 || !strings.Contains(missingResponse.Error.Message, "rollout") {
		t.Fatalf("missing rollout resume did not fail closed: %+v", missingResponse)
	}
	closeAndWait(t, missingProcess)
	if requests := secondModelServer.Requests(); len(requests) != 0 {
		t.Fatalf("missing rollout reached the model %d times", len(requests))
	}
	if requests := restoredMCPServer.Requests(); len(requests) != 0 {
		t.Fatalf("missing rollout initialized MCP: %v", mcpRequestMethods(requests))
	}
	writeScriptedModelConfigWithOptions(t, restoredPaths.codexHome, secondModelServer.URL(), scriptedModelConfigOptions{
		disableUpdatePlan: true,
		mcpServerURL:      restoredMCPServer.URL(),
		mcpEnabledTools:   []string{approvedMCPToolName},
	})
	secondProcess := startPreparedLiveCodex(t, binary, restoredPaths, "app-server", "--listen", "stdio://", "--strict-config")
	initializeAppServer(t, secondProcess)
	secondCollector := newRPCCollector(secondProcess)
	resumed, secondTurn := resumeAppServerThreadAndStartTurn(
		t,
		secondCollector,
		thread.Thread.ID,
		restoredRolloutPath,
		restoredPaths.cwd,
		a09SecondUserText,
	)
	if resumed.Thread.ID != thread.Thread.ID || resumed.Thread.SessionID != thread.Thread.SessionID {
		t.Fatalf("resumed thread identity changed: source=%+v restored=%+v", thread.Thread, resumed.Thread)
	}
	if len(resumed.Thread.Turns) != 0 {
		t.Fatalf("metadata-only thread/resume returned turns: %+v", resumed.Thread.Turns)
	}
	restoredRolloutRelative := stateRelativePath(t, restoredPaths.codexHome, resumed.Thread.Path)
	if restoredRolloutRelative != rolloutRelative {
		t.Fatalf("resumed rollout path = %q, want restored relative path %q", restoredRolloutRelative, rolloutRelative)
	}
	assertAgentItemCompleted(t, secondCollector, resumed.Thread.ID, secondTurn.ID, a09SecondAssistantText)
	secondCollector.notification(t, "turn/completed")
	closeAndWait(t, secondProcess)

	if failures := secondModelServer.Failures(); len(failures) != 0 {
		t.Fatalf("restored scripted model server failures: %v", failures)
	}
	secondRequests := secondModelServer.Requests()
	if len(secondRequests) != 1 {
		t.Fatalf("restored scripted model received %d requests, want one", len(secondRequests))
	}
	restoredRequest := decodeCapturedModelRequest(t, secondRequests[0])
	if !modelInputContainsUserText(t, restoredRequest.Input, a09FirstUserText) ||
		!modelInputContainsUserText(t, restoredRequest.Input, a09SecondUserText) ||
		!modelInputContainsFunctionOutput(restoredRequest.Input, a09ToolCallID, a09ToolMarker) {
		t.Fatalf("restored model context omitted first turn or MCP result: input=%s", encodeModelInput(t, restoredRequest.Input))
	}
	if failures := restoredMCPServer.Failures(); len(failures) != 0 {
		t.Fatalf("restored scripted MCP server failures: %v", failures)
	}
	if calls := restoredMCPServer.Calls(); len(calls) != 0 {
		t.Fatalf("restored turn unexpectedly repeated MCP side effect: %+v", calls)
	}
	assertMCPBootstrap(t, restoredMCPServer)
	t.Logf("A09 restored %s from rollout-only checkpoint %q", thread.Thread.ID, rolloutRelative)
}

// TestAppServerA09DynamicRunnerCheckpointRoundTrip recomposes A09 around the
// production worker-owned bridge. The first app-server sees only a frozen
// dynamic catalog and persists the client callback result. A fresh app-server
// then resumes from the rollout alone without a dynamicTools override, exposes
// the exact same model tool schema, retains the call/result in model context,
// and never invokes the executor side effect again.
func TestAppServerA09DynamicRunnerCheckpointRoundTrip(t *testing.T) {
	binary, sourcePaths := prepareLiveCodex(t)
	// Stable 0.146.0 is currently the intersection of the release-bound
	// dynamic bridge and rollout-only checkpoint evidence.
	requireCandidateRelease(t, binary, sourcePaths, "0.146.0")
	catalog := approvedDynamicExecutorCatalog(t)

	toolCall, err := scriptedmodel.NamespacedFunctionCall(
		"response-a09-dynamic-runner-call",
		a09ToolCallID,
		executorDynamicNamespace,
		approvedMCPToolName,
		`{"message":"persist this dynamic executor result"}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	firstFinal, err := scriptedmodel.AssistantMessage(
		"response-a09-dynamic-runner-final",
		"message-a09-dynamic-runner-final",
		a09FirstAssistantText,
	)
	if err != nil {
		t.Fatal(err)
	}
	firstModelServer, err := scriptedmodel.Start(scriptedmodel.Config{
		Responses: []scriptedmodel.Response{toolCall, firstFinal},
	})
	if err != nil {
		t.Fatalf("start first dynamic checkpoint model: %v", err)
	}
	t.Cleanup(firstModelServer.Close)
	writeScriptedModelConfigWithOptions(t, sourcePaths.codexHome, firstModelServer.URL(), scriptedModelConfigOptions{
		disableUpdatePlan: true,
	})

	var sideEffects atomic.Int64
	firstCaller := &a09RunnerDynamicCaller{
		catalog:         catalog,
		sideEffects:     &sideEffects,
		allow:           true,
		expectedMessage: "persist this dynamic executor result",
	}
	firstBridge, err := harnessworker.NewDynamicBridge(firstCaller, 8, harnessworker.DefaultLimits().MaxArgumentBytes)
	if err != nil {
		t.Fatal(err)
	}
	firstProcess := startPreparedLiveCodex(t, binary, sourcePaths, "app-server", "--listen", "stdio://", "--strict-config")
	firstRunner, err := harnessworker.NewAppServerRunner(
		firstProcess.Peer,
		firstBridge,
		harnessworker.DefaultAppServerRunnerOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	firstEventsDrained := drainAppServerRunnerEvents(firstRunner)
	firstResult, err := firstRunner.Run(t.Context(), harnessworker.AppServerRunRequest{
		RunID:                "run-a09-dynamic-source",
		RunAttemptGeneration: 1,
		ClientInfo: harnessworker.AppServerClientInfo{
			Name:    "agentserver_v2_conformance",
			Title:   "agentserver v2 conformance",
			Version: "0.0.0",
		},
		Catalog: catalog,
		Start: &harnessworker.AppServerThreadStart{
			Model:                 conformanceModelName,
			CWD:                   sourcePaths.cwd,
			BaseInstructions:      "Return only the scripted model result.",
			DeveloperInstructions: "Persist the frozen dynamic callback without local tools.",
		},
		UserText: a09FirstUserText,
	})
	if err != nil {
		t.Fatal(err)
	}
	<-firstEventsDrained
	if firstResult.Terminal.Turn.Status != "completed" || firstBridge.Outstanding() != 0 {
		t.Fatalf("first dynamic checkpoint lifecycle/outstanding = %+v/%d", firstResult.Terminal, firstBridge.Outstanding())
	}
	closeAndWait(t, firstProcess)
	if got := sideEffects.Load(); got != 1 {
		t.Fatalf("source dynamic executor side effects = %d, want one", got)
	}
	if failures := firstModelServer.Failures(); len(failures) != 0 {
		t.Fatalf("source dynamic checkpoint model failures: %v", failures)
	}
	firstRequests := firstModelServer.Requests()
	if len(firstRequests) != 2 {
		t.Fatalf("source dynamic checkpoint model requests = %d, want two", len(firstRequests))
	}
	firstInitial := decodeCapturedModelRequest(t, firstRequests[0])
	firstFollowup := decodeCapturedModelRequest(t, firstRequests[1])
	wantSurface := []string{executorDynamicNamespace + "." + approvedMCPToolName}
	if got := modelToolNames(t, firstInitial.Tools); !reflect.DeepEqual(got, wantSurface) {
		t.Fatalf("source dynamic checkpoint tool surface = %v, want %v", got, wantSurface)
	}
	if !modelInputContainsFunctionOutput(firstFollowup.Input, a09ToolCallID, a09ToolMarker) {
		t.Fatalf("source dynamic checkpoint omitted callback result: input=%s", encodeModelInput(t, firstFollowup.Input))
	}

	sourceSnapshot := snapshotStateTree(t, sourcePaths.codexHome)
	rolloutRelative := stateRelativePath(t, sourcePaths.codexHome, firstResult.Thread.Thread.Path)
	rolloutSnapshot, exists := sourceSnapshot[rolloutRelative]
	if !exists || !strings.HasSuffix(rolloutRelative, ".jsonl") || rolloutSnapshot.Size == 0 {
		t.Fatalf("dynamic checkpoint rollout = %+v at %q", rolloutSnapshot, rolloutRelative)
	}
	assertCompleteRolloutJSONL(
		t,
		firstResult.Thread.Thread.Path,
		firstResult.Thread.Thread.ID,
		a09FirstUserText,
		a09FirstAssistantText,
		a09ToolMarker,
	)

	restoredPaths := createRestoredLivePaths(t, t.TempDir(), "dynamic-runner-runtime")
	copyCheckpointFile(
		t,
		sourcePaths.codexHome,
		restoredPaths.codexHome,
		rolloutRelative,
		rolloutSnapshot,
	)
	wantCheckpointSnapshot := map[string]stateFileSnapshot{rolloutRelative: rolloutSnapshot}
	if got := snapshotStateTree(t, restoredPaths.codexHome); !reflect.DeepEqual(got, wantCheckpointSnapshot) {
		t.Fatalf("dynamic rollout-only checkpoint contains unexpected files: got=%v want=%v", got, wantCheckpointSnapshot)
	}
	restoredRolloutPath := filepath.Join(restoredPaths.codexHome, filepath.FromSlash(rolloutRelative))
	retiredSourceHome := filepath.Join(sourcePaths.root, "retired-dynamic-source-codex-home")
	if err := os.Rename(sourcePaths.codexHome, retiredSourceHome); err != nil {
		t.Fatalf("retire dynamic source CODEX_HOME: %v", err)
	}
	if _, err := os.Stat(firstResult.Thread.Thread.Path); !os.IsNotExist(err) {
		t.Fatalf("dynamic source rollout remained available after retirement: %v", err)
	}

	secondFinal, err := scriptedmodel.AssistantMessage(
		"response-a09-dynamic-runner-restored",
		"message-a09-dynamic-runner-restored",
		a09SecondAssistantText,
	)
	if err != nil {
		t.Fatal(err)
	}
	secondModelServer, err := scriptedmodel.Start(scriptedmodel.Config{
		Responses: []scriptedmodel.Response{secondFinal},
	})
	if err != nil {
		t.Fatalf("start restored dynamic checkpoint model: %v", err)
	}
	t.Cleanup(secondModelServer.Close)
	writeScriptedModelConfigWithOptions(t, restoredPaths.codexHome, secondModelServer.URL(), scriptedModelConfigOptions{
		disableUpdatePlan: true,
	})
	secondCaller := &a09RunnerDynamicCaller{
		catalog:     catalog,
		sideEffects: &sideEffects,
		allow:       false,
	}
	secondBridge, err := harnessworker.NewDynamicBridge(secondCaller, 8, harnessworker.DefaultLimits().MaxArgumentBytes)
	if err != nil {
		t.Fatal(err)
	}
	secondProcess := startPreparedLiveCodex(t, binary, restoredPaths, "app-server", "--listen", "stdio://", "--strict-config")
	secondRunner, err := harnessworker.NewAppServerRunner(
		secondProcess.Peer,
		secondBridge,
		harnessworker.DefaultAppServerRunnerOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	secondEventsDrained := drainAppServerRunnerEvents(secondRunner)
	secondResult, err := secondRunner.Run(t.Context(), harnessworker.AppServerRunRequest{
		RunID:                "run-a09-dynamic-restored",
		RunAttemptGeneration: 2,
		ClientInfo: harnessworker.AppServerClientInfo{
			Name:    "agentserver_v2_conformance",
			Title:   "agentserver v2 conformance",
			Version: "0.0.0",
		},
		Catalog: catalog,
		Resume: &harnessworker.AppServerThreadResume{
			ThreadID:                firstResult.Thread.Thread.ID,
			RolloutPath:             restoredRolloutPath,
			CWD:                     restoredPaths.cwd,
			CheckpointCatalogDigest: catalog.Digest(),
		},
		UserText: a09SecondUserText,
	})
	if err != nil {
		t.Fatal(err)
	}
	<-secondEventsDrained
	if !secondResult.Resumed || secondResult.Terminal.Turn.Status != "completed" || secondBridge.Outstanding() != 0 {
		t.Fatalf("restored dynamic checkpoint lifecycle/outstanding = %+v/%d", secondResult, secondBridge.Outstanding())
	}
	if secondResult.Thread.Thread.ID != firstResult.Thread.Thread.ID ||
		secondResult.Thread.Thread.SessionID != firstResult.Thread.Thread.SessionID {
		t.Fatalf("restored dynamic thread identity changed: source=%+v restored=%+v", firstResult.Thread.Thread, secondResult.Thread.Thread)
	}
	if got := stateRelativePath(t, restoredPaths.codexHome, secondResult.Thread.Thread.Path); got != rolloutRelative {
		t.Fatalf("restored dynamic rollout path = %q, want %q", got, rolloutRelative)
	}
	closeAndWait(t, secondProcess)
	if got := sideEffects.Load(); got != 1 {
		t.Fatalf("restored dynamic executor side effects = %d, want no replay after source call", got)
	}
	if failures := secondModelServer.Failures(); len(failures) != 0 {
		t.Fatalf("restored dynamic checkpoint model failures: %v", failures)
	}
	secondRequests := secondModelServer.Requests()
	if len(secondRequests) != 1 {
		t.Fatalf("restored dynamic checkpoint model requests = %d, want one", len(secondRequests))
	}
	restoredRequest := decodeCapturedModelRequest(t, secondRequests[0])
	if !modelInputContainsUserText(t, restoredRequest.Input, a09FirstUserText) ||
		!modelInputContainsUserText(t, restoredRequest.Input, a09SecondUserText) ||
		!modelInputContainsFunctionOutput(restoredRequest.Input, a09ToolCallID, a09ToolMarker) {
		t.Fatalf("restored dynamic context omitted call history: input=%s", encodeModelInput(t, restoredRequest.Input))
	}
	if got := modelToolNames(t, restoredRequest.Tools); !reflect.DeepEqual(got, wantSurface) {
		t.Fatalf("restored dynamic checkpoint tool surface = %v, want %v", got, wantSurface)
	}
	if !reflect.DeepEqual(modelToolValues(t, restoredRequest.Tools), modelToolValues(t, firstInitial.Tools)) {
		t.Fatal("restored dynamic checkpoint changed the frozen model tool schema")
	}
	t.Logf(
		"A09 dynamic runner restored %s from rollout-only checkpoint %q with catalog %s and one total side effect",
		firstResult.Thread.Thread.ID,
		rolloutRelative,
		catalog.Digest(),
	)
}

type a09RunnerDynamicCaller struct {
	catalog         *harnessworker.Catalog
	sideEffects     *atomic.Int64
	allow           bool
	expectedMessage string
}

func (c *a09RunnerDynamicCaller) CallDynamicTool(
	_ context.Context,
	call harnessworker.DynamicCall,
) (harnessworker.DynamicToolResult, error) {
	arguments, err := c.catalog.ValidateCall(call.Namespace, call.Tool, call.Arguments)
	if err != nil {
		return harnessworker.DynamicToolResult{}, err
	}
	c.sideEffects.Add(1)
	if !c.allow {
		return harnessworker.DynamicToolResult{}, errors.New("restored checkpoint replayed a dynamic executor side effect")
	}
	var payload struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(arguments, &payload); err != nil {
		return harnessworker.DynamicToolResult{}, err
	}
	if payload.Message != c.expectedMessage {
		return harnessworker.DynamicToolResult{}, fmt.Errorf("dynamic checkpoint message = %q, want %q", payload.Message, c.expectedMessage)
	}
	return harnessworker.DynamicToolResult{
		ContentItems: []harnessworker.InputTextContent{{Type: "inputText", Text: a09ToolMarker}},
		Success:      true,
	}, nil
}

func modelToolValues(t *testing.T, tools []json.RawMessage) []any {
	t.Helper()
	values := make([]any, len(tools))
	for index, tool := range tools {
		if err := json.Unmarshal(tool, &values[index]); err != nil {
			t.Fatalf("decode model tool %d: %v", index, err)
		}
	}
	return values
}

// TestAppServerA10MidTurnCrashRestoresLastCompletedCheckpoint hard-kills a
// second app-server process after its turn and model request are both in flight.
// The crashed runtime is never promoted to a checkpoint. A third process must
// restore the separately sealed completed-turn rollout, create a different turn,
// and omit the abandoned turn's input from the model-visible history.
func TestAppServerA10MidTurnCrashRestoresLastCompletedCheckpoint(t *testing.T) {
	binary, sourcePaths := prepareLiveCodex(t)
	requireCandidateReleaseOneOf(t, binary, sourcePaths, "0.146.0-alpha.14", "0.146.0")
	baseFinal, err := scriptedmodel.AssistantMessage(
		"response-a10-base-final",
		"message-a10-base-final",
		a10BaseAssistantText,
	)
	if err != nil {
		t.Fatal(err)
	}
	baseModelServer, err := scriptedmodel.Start(scriptedmodel.Config{
		Responses: []scriptedmodel.Response{baseFinal},
	})
	if err != nil {
		t.Fatalf("start A10 base model: %v", err)
	}
	t.Cleanup(baseModelServer.Close)
	writeScriptedModelConfigWithOptions(t, sourcePaths.codexHome, baseModelServer.URL(), scriptedModelConfigOptions{
		disableUpdatePlan: true,
	})
	baseProcess := startPreparedLiveCodex(t, binary, sourcePaths, "app-server", "--listen", "stdio://", "--strict-config")
	initializeAppServer(t, baseProcess)
	baseCollector := newRPCCollector(baseProcess)
	thread, baseTurn := startMinimalAppServerTurn(t, baseCollector, sourcePaths.cwd, a10BaseUserText)
	assertAgentItemCompleted(t, baseCollector, thread.Thread.ID, baseTurn.ID, a10BaseAssistantText)
	baseCollector.notification(t, "turn/completed")
	closeAndWait(t, baseProcess)
	if failures := baseModelServer.Failures(); len(failures) != 0 {
		t.Fatalf("A10 base model failures: %v", failures)
	}
	if requests := baseModelServer.Requests(); len(requests) != 1 {
		t.Fatalf("A10 base model received %d requests, want one", len(requests))
	}

	sourceSnapshot := snapshotStateTree(t, sourcePaths.codexHome)
	rolloutRelative := stateRelativePath(t, sourcePaths.codexHome, thread.Thread.Path)
	rolloutSnapshot, exists := sourceSnapshot[rolloutRelative]
	if !exists || !strings.HasSuffix(rolloutRelative, ".jsonl") || rolloutSnapshot.Size == 0 {
		t.Fatalf("A10 base rollout snapshot = %+v at %q, want one non-empty JSONL", rolloutSnapshot, rolloutRelative)
	}
	assertCompleteRolloutJSONL(
		t,
		thread.Thread.Path,
		thread.Thread.ID,
		a10BaseUserText,
		a10BaseAssistantText,
	)
	checkpointPaths := createRestoredLivePaths(t, t.TempDir(), "sealed-checkpoint")
	copyCheckpointFile(t, sourcePaths.codexHome, checkpointPaths.codexHome, rolloutRelative, rolloutSnapshot)
	wantCheckpointSnapshot := map[string]stateFileSnapshot{rolloutRelative: rolloutSnapshot}
	if checkpointSnapshot := snapshotStateTree(t, checkpointPaths.codexHome); !reflect.DeepEqual(checkpointSnapshot, wantCheckpointSnapshot) {
		t.Fatalf("A10 sealed checkpoint contains unexpected files: got=%v want=%v", checkpointSnapshot, wantCheckpointSnapshot)
	}
	retiredSourceHome := filepath.Join(sourcePaths.root, "retired-a10-source-codex-home")
	if err := os.Rename(sourcePaths.codexHome, retiredSourceHome); err != nil {
		t.Fatalf("retire A10 source CODEX_HOME: %v", err)
	}
	if _, err := os.Stat(thread.Thread.Path); !os.IsNotExist(err) {
		t.Fatalf("A10 source rollout path remained available after retirement: %v", err)
	}

	crashPaths := createRestoredLivePaths(t, t.TempDir(), "crash-runtime")
	copyCheckpointFile(t, checkpointPaths.codexHome, crashPaths.codexHome, rolloutRelative, rolloutSnapshot)
	heldModelServer, err := scriptedmodel.Start(scriptedmodel.Config{
		Responses: []scriptedmodel.Response{{HoldOpen: true}},
	})
	if err != nil {
		t.Fatalf("start A10 held model: %v", err)
	}
	t.Cleanup(heldModelServer.Close)
	writeScriptedModelConfigWithOptions(t, crashPaths.codexHome, heldModelServer.URL(), scriptedModelConfigOptions{
		disableUpdatePlan: true,
	})
	crashProcess := startPreparedLiveCodex(t, binary, crashPaths, "app-server", "--listen", "stdio://", "--strict-config")
	initializeAppServer(t, crashProcess)
	crashCollector := newRPCCollector(crashProcess)
	crashRolloutPath := filepath.Join(crashPaths.codexHome, filepath.FromSlash(rolloutRelative))
	crashResume, crashedTurn := resumeAppServerThreadAndStartTurn(
		t,
		crashCollector,
		thread.Thread.ID,
		crashRolloutPath,
		crashPaths.cwd,
		a10CrashUserText,
	)
	if crashResume.Thread.ID != thread.Thread.ID || crashedTurn.ID == "" || crashedTurn.ID == baseTurn.ID {
		t.Fatalf("A10 in-flight turn identity is invalid: base=%+v resumed=%+v crashed=%+v", baseTurn, crashResume.Thread, crashedTurn)
	}
	if resumedRelative := stateRelativePath(t, crashPaths.codexHome, crashResume.Thread.Path); resumedRelative != rolloutRelative {
		t.Fatalf("A10 crash runtime resumed rollout %q, want %q", resumedRelative, rolloutRelative)
	}
	waitForModelContext, cancelWaitForModel := context.WithTimeout(context.Background(), liveProbeTimeout)
	defer cancelWaitForModel()
	if err := heldModelServer.WaitForRequests(waitForModelContext, 1); err != nil {
		t.Fatalf("wait for A10 in-flight model request: %v", err)
	}
	heldRequests := heldModelServer.Requests()
	if len(heldRequests) != 1 {
		t.Fatalf("A10 held model received %d requests, want one", len(heldRequests))
	}
	heldRequest := decodeCapturedModelRequest(t, heldRequests[0])
	if !modelInputContainsUserText(t, heldRequest.Input, a10BaseUserText) ||
		!modelInputContainsUserText(t, heldRequest.Input, a10CrashUserText) {
		t.Fatalf("A10 in-flight model request omitted durable or crashing turn: input=%s", encodeModelInput(t, heldRequest.Input))
	}
	for _, notification := range crashCollector.notifications {
		if notification.Method == "turn/completed" {
			t.Fatal("A10 crash turn reached terminal before the hard kill")
		}
	}
	if err := crashProcess.Kill(); err != nil {
		t.Fatalf("hard-kill A10 app-server: %v", err)
	}
	waitForCrash, cancelWaitForCrash := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelWaitForCrash()
	crashErr := crashProcess.Wait(waitForCrash)
	if waitForCrash.Err() != nil {
		t.Fatalf("A10 app-server did not exit after hard kill: %v", waitForCrash.Err())
	}
	if crashErr == nil {
		t.Fatal("A10 hard-killed app-server exited successfully")
	}
	if failures := heldModelServer.Failures(); len(failures) != 0 {
		t.Fatalf("A10 held model failures: %v", failures)
	}
	if requests := heldModelServer.Requests(); len(requests) != 1 {
		t.Fatalf("A10 hard crash retried the model request: got %d requests, want one", len(requests))
	}
	discardedCrashHome := filepath.Join(crashPaths.root, "discarded-crash-codex-home")
	if err := os.Rename(crashPaths.codexHome, discardedCrashHome); err != nil {
		t.Fatalf("discard A10 crash CODEX_HOME: %v", err)
	}
	if _, err := os.Stat(crashRolloutPath); !os.IsNotExist(err) {
		t.Fatalf("A10 crash rollout path remained available after discard: %v", err)
	}
	if checkpointSnapshot := snapshotStateTree(t, checkpointPaths.codexHome); !reflect.DeepEqual(checkpointSnapshot, wantCheckpointSnapshot) {
		t.Fatalf("A10 hard crash mutated the sealed checkpoint: got=%v want=%v", checkpointSnapshot, wantCheckpointSnapshot)
	}

	recoveryPaths := createRestoredLivePaths(t, t.TempDir(), "recovery-runtime")
	copyCheckpointFile(t, checkpointPaths.codexHome, recoveryPaths.codexHome, rolloutRelative, rolloutSnapshot)
	recoveryFinal, err := scriptedmodel.AssistantMessage(
		"response-a10-recovery-final",
		"message-a10-recovery-final",
		a10RecoveryAssistantText,
	)
	if err != nil {
		t.Fatal(err)
	}
	recoveryModelServer, err := scriptedmodel.Start(scriptedmodel.Config{
		Responses: []scriptedmodel.Response{recoveryFinal},
	})
	if err != nil {
		t.Fatalf("start A10 recovery model: %v", err)
	}
	t.Cleanup(recoveryModelServer.Close)
	writeScriptedModelConfigWithOptions(t, recoveryPaths.codexHome, recoveryModelServer.URL(), scriptedModelConfigOptions{
		disableUpdatePlan: true,
	})
	recoveryProcess := startPreparedLiveCodex(t, binary, recoveryPaths, "app-server", "--listen", "stdio://", "--strict-config")
	initializeAppServer(t, recoveryProcess)
	recoveryCollector := newRPCCollector(recoveryProcess)
	recoveryRolloutPath := filepath.Join(recoveryPaths.codexHome, filepath.FromSlash(rolloutRelative))
	recovered, recoveryTurn := resumeAppServerThreadAndStartTurn(
		t,
		recoveryCollector,
		thread.Thread.ID,
		recoveryRolloutPath,
		recoveryPaths.cwd,
		a10RecoveryUserText,
	)
	if recovered.Thread.ID != thread.Thread.ID || recovered.Thread.SessionID != thread.Thread.SessionID {
		t.Fatalf("A10 recovered thread identity changed: source=%+v recovered=%+v", thread.Thread, recovered.Thread)
	}
	if recoveryTurn.ID == "" || recoveryTurn.ID == crashedTurn.ID || recoveryTurn.ID == baseTurn.ID {
		t.Fatalf("A10 recovery did not create a new turn: base=%q crashed=%q recovery=%q", baseTurn.ID, crashedTurn.ID, recoveryTurn.ID)
	}
	assertAgentItemCompleted(t, recoveryCollector, recovered.Thread.ID, recoveryTurn.ID, a10RecoveryAssistantText)
	recoveryCollector.notification(t, "turn/completed")
	closeAndWait(t, recoveryProcess)
	if failures := recoveryModelServer.Failures(); len(failures) != 0 {
		t.Fatalf("A10 recovery model failures: %v", failures)
	}
	recoveryRequests := recoveryModelServer.Requests()
	if len(recoveryRequests) != 1 {
		t.Fatalf("A10 recovery model received %d requests, want one", len(recoveryRequests))
	}
	recoveryRequest := decodeCapturedModelRequest(t, recoveryRequests[0])
	if !modelInputContainsUserText(t, recoveryRequest.Input, a10BaseUserText) ||
		!modelInputContainsUserText(t, recoveryRequest.Input, a10RecoveryUserText) ||
		modelInputContainsUserText(t, recoveryRequest.Input, a10CrashUserText) {
		t.Fatalf("A10 recovery context did not stop at the completed checkpoint: input=%s", encodeModelInput(t, recoveryRequest.Input))
	}
	t.Logf("A10 abandoned turn %s and created turn %s from sealed checkpoint %q", crashedTurn.ID, recoveryTurn.ID, rolloutRelative)
}

// TestAppServerA11CheckpointExcludesRuntimeSecrets proves that credentials and
// runtime artifacts are neither required by native resume nor copied into the
// rollout-only checkpoint. The MCP capability is actually used as an HTTP
// bearer, then rotated for restore; the other sentinels challenge the exact
// manifest allowlist from config/auth/requirements/log/diagnostic/transport
// files. Model-visible user and MCP-result markers remain intact.
func TestAppServerA11CheckpointExcludesRuntimeSecrets(t *testing.T) {
	binary, sourcePaths := prepareLiveCodex(t)
	requireCandidateReleaseOneOf(t, binary, sourcePaths, "0.146.0-alpha.14", "0.146.0")
	secrets := a11SecretSentinels()
	toolCall, err := scriptedmodel.NamespacedFunctionCall(
		"response-a11-tool-call",
		a11ToolCallID,
		executorMCPNamespace,
		approvedMCPToolName,
		`{"message":"return a model-visible non-secret marker"}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	firstFinal, err := scriptedmodel.AssistantMessage(
		"response-a11-first-final",
		"message-a11-first-final",
		a11FirstAssistantText,
	)
	if err != nil {
		t.Fatal(err)
	}
	firstModelServer, err := scriptedmodel.Start(scriptedmodel.Config{
		Responses: []scriptedmodel.Response{toolCall, firstFinal},
	})
	if err != nil {
		t.Fatalf("start A11 source model: %v", err)
	}
	t.Cleanup(firstModelServer.Close)
	firstMCPServer := startDestructiveExecutorMCPServer(t, []scriptedmcp.ExpectedCall{{
		Name:      approvedMCPToolName,
		Arguments: json.RawMessage(`{"message":"return a model-visible non-secret marker"}`),
		Result: json.RawMessage(
			`{"content":[{"type":"text","text":"safe checkpoint result"}],"structuredContent":{"checkpointMarker":"a11-model-visible-tool-result"},"isError":false}`,
		),
	}})
	sourcePaths.environment, err = codexprocess.Environment(
		sourcePaths.home,
		sourcePaths.codexHome,
		sourcePaths.temporary,
		map[string]string{a11CapabilityEnvName: a11SourceCapability},
	)
	if err != nil {
		t.Fatalf("build A11 source environment: %v", err)
	}
	writeScriptedModelConfigWithOptions(t, sourcePaths.codexHome, firstModelServer.URL(), scriptedModelConfigOptions{
		disableUpdatePlan:    true,
		mcpServerURL:         firstMCPServer.URL(),
		mcpEnabledTools:      []string{approvedMCPToolName},
		mcpBearerTokenEnvVar: a11CapabilityEnvName,
	})
	writeA11RuntimeSecretFiles(t, sourcePaths.codexHome)
	firstProcess := startPreparedLiveCodex(t, binary, sourcePaths, "app-server", "--listen", "stdio://", "--strict-config")
	initializeAppServer(t, firstProcess)
	firstCollector := newRPCCollector(firstProcess)
	thread, firstTurn := startMinimalAppServerTurn(t, firstCollector, sourcePaths.cwd, a11FirstUserText)
	assertAgentItemCompleted(t, firstCollector, thread.Thread.ID, firstTurn.ID, a11FirstAssistantText)
	firstCollector.notification(t, "turn/completed")
	closeAndWait(t, firstProcess)

	if failures := firstModelServer.Failures(); len(failures) != 0 {
		t.Fatalf("A11 source model failures: %v", failures)
	}
	firstModelRequests := firstModelServer.Requests()
	if len(firstModelRequests) != 2 {
		t.Fatalf("A11 source model received %d requests, want two", len(firstModelRequests))
	}
	for index, request := range firstModelRequests {
		assertBytesExcludeSecrets(t, fmt.Sprintf("A11 source model request %d", index), request.Body, secrets)
	}
	if failures := firstMCPServer.Failures(); len(failures) != 0 {
		t.Fatalf("A11 source MCP failures: %v", failures)
	}
	if calls := firstMCPServer.Calls(); len(calls) != 1 || calls[0].Name != approvedMCPToolName {
		t.Fatalf("A11 source MCP calls = %+v, want one", calls)
	}
	assertMCPBearerToken(t, firstMCPServer, a11SourceCapability)
	assertMCPBootstrap(t, firstMCPServer)
	stderr, stderrTruncated := firstProcess.Stderr()
	if stderrTruncated {
		t.Fatal("A11 source app-server stderr exceeded the probe bound")
	}
	assertBytesExcludeSecrets(t, "A11 source app-server stderr", stderr, secrets)

	sourceSnapshot := snapshotStateTree(t, sourcePaths.codexHome)
	for relative, secret := range a11RuntimeSecretFileSentinels() {
		if _, exists := sourceSnapshot[relative]; !exists {
			t.Fatalf("A11 source runtime omitted sentinel file %q", relative)
		}
		contents, err := os.ReadFile(filepath.Join(sourcePaths.codexHome, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatalf("read A11 source sentinel file %q: %v", relative, err)
		}
		if !bytes.Contains(contents, []byte(secret.Value)) {
			t.Fatalf("A11 source sentinel file %q no longer contains its %s sentinel", relative, secret.Label)
		}
	}
	rolloutRelative := stateRelativePath(t, sourcePaths.codexHome, thread.Thread.Path)
	rolloutSnapshot, exists := sourceSnapshot[rolloutRelative]
	if !exists || !strings.HasSuffix(rolloutRelative, ".jsonl") || rolloutSnapshot.Size == 0 {
		t.Fatalf("A11 rollout snapshot = %+v at %q, want one non-empty JSONL", rolloutSnapshot, rolloutRelative)
	}
	rolloutContents, err := os.ReadFile(thread.Thread.Path)
	if err != nil {
		t.Fatalf("read A11 source rollout: %v", err)
	}
	assertBytesExcludeSecrets(t, "A11 source rollout", rolloutContents, secrets)
	assertCompleteRolloutJSONL(
		t,
		thread.Thread.Path,
		thread.Thread.ID,
		a11FirstUserText,
		a11FirstAssistantText,
		a11ToolMarker,
	)

	restoredPaths := createRestoredLivePaths(t, t.TempDir(), "a11-restored-runtime")
	copyCheckpointFile(t, sourcePaths.codexHome, restoredPaths.codexHome, rolloutRelative, rolloutSnapshot)
	checkpointSnapshot := snapshotStateTree(t, restoredPaths.codexHome)
	wantCheckpointSnapshot := map[string]stateFileSnapshot{rolloutRelative: rolloutSnapshot}
	if !reflect.DeepEqual(checkpointSnapshot, wantCheckpointSnapshot) {
		t.Fatalf("A11 checkpoint contains runtime-only files: got=%v want=%v", checkpointSnapshot, wantCheckpointSnapshot)
	}
	restoredRolloutPath := filepath.Join(restoredPaths.codexHome, filepath.FromSlash(rolloutRelative))
	checkpointContents, err := os.ReadFile(restoredRolloutPath)
	if err != nil {
		t.Fatalf("read A11 checkpoint rollout: %v", err)
	}
	assertBytesExcludeSecrets(t, "A11 checkpoint", checkpointContents, secrets)
	retiredSourceHome := filepath.Join(sourcePaths.root, "retired-a11-source-codex-home")
	if err := os.Rename(sourcePaths.codexHome, retiredSourceHome); err != nil {
		t.Fatalf("retire A11 source CODEX_HOME: %v", err)
	}
	if _, err := os.Stat(thread.Thread.Path); !os.IsNotExist(err) {
		t.Fatalf("A11 source rollout path remained available after retirement: %v", err)
	}

	secondFinal, err := scriptedmodel.AssistantMessage(
		"response-a11-second-final",
		"message-a11-second-final",
		a11SecondAssistantText,
	)
	if err != nil {
		t.Fatal(err)
	}
	secondModelServer, err := scriptedmodel.Start(scriptedmodel.Config{
		Responses: []scriptedmodel.Response{secondFinal},
	})
	if err != nil {
		t.Fatalf("start A11 restored model: %v", err)
	}
	t.Cleanup(secondModelServer.Close)
	restoredMCPServer := startDestructiveExecutorMCPServer(t, nil)
	restoredPaths.environment, err = codexprocess.Environment(
		restoredPaths.home,
		restoredPaths.codexHome,
		restoredPaths.temporary,
		map[string]string{a11CapabilityEnvName: a11RestoredCapability},
	)
	if err != nil {
		t.Fatalf("build A11 restored environment: %v", err)
	}
	writeScriptedModelConfigWithOptions(t, restoredPaths.codexHome, secondModelServer.URL(), scriptedModelConfigOptions{
		disableUpdatePlan:    true,
		mcpServerURL:         restoredMCPServer.URL(),
		mcpEnabledTools:      []string{approvedMCPToolName},
		mcpBearerTokenEnvVar: a11CapabilityEnvName,
	})
	secondProcess := startPreparedLiveCodex(t, binary, restoredPaths, "app-server", "--listen", "stdio://", "--strict-config")
	initializeAppServer(t, secondProcess)
	secondCollector := newRPCCollector(secondProcess)
	resumed, secondTurn := resumeAppServerThreadAndStartTurn(
		t,
		secondCollector,
		thread.Thread.ID,
		restoredRolloutPath,
		restoredPaths.cwd,
		a11SecondUserText,
	)
	assertAgentItemCompleted(t, secondCollector, resumed.Thread.ID, secondTurn.ID, a11SecondAssistantText)
	secondCollector.notification(t, "turn/completed")
	closeAndWait(t, secondProcess)
	restoredStderr, restoredStderrTruncated := secondProcess.Stderr()
	if restoredStderrTruncated {
		t.Fatal("A11 restored app-server stderr exceeded the probe bound")
	}
	assertBytesExcludeSecrets(t, "A11 restored app-server stderr", restoredStderr, secrets)
	if failures := secondModelServer.Failures(); len(failures) != 0 {
		t.Fatalf("A11 restored model failures: %v", failures)
	}
	secondModelRequests := secondModelServer.Requests()
	if len(secondModelRequests) != 1 {
		t.Fatalf("A11 restored model received %d requests, want one", len(secondModelRequests))
	}
	restoredModelRequest := decodeCapturedModelRequest(t, secondModelRequests[0])
	if !modelInputContainsUserText(t, restoredModelRequest.Input, a11FirstUserText) ||
		!modelInputContainsUserText(t, restoredModelRequest.Input, a11SecondUserText) ||
		!modelInputContainsFunctionOutput(restoredModelRequest.Input, a11ToolCallID, a11ToolMarker) {
		t.Fatalf("A11 restored context omitted model-visible history: input=%s", encodeModelInput(t, restoredModelRequest.Input))
	}
	assertBytesExcludeSecrets(t, "A11 restored model request", secondModelRequests[0].Body, secrets)
	if failures := restoredMCPServer.Failures(); len(failures) != 0 {
		t.Fatalf("A11 restored MCP failures: %v", failures)
	}
	if calls := restoredMCPServer.Calls(); len(calls) != 0 {
		t.Fatalf("A11 restore unexpectedly repeated the MCP side effect: %+v", calls)
	}
	assertMCPBearerToken(t, restoredMCPServer, a11RestoredCapability)
	assertMCPBootstrap(t, restoredMCPServer)
	restoredRolloutContents, err := os.ReadFile(resumed.Thread.Path)
	if err != nil {
		t.Fatalf("read A11 restored rollout: %v", err)
	}
	assertBytesExcludeSecrets(t, "A11 restored rollout", restoredRolloutContents, secrets)
	t.Logf("A11 restored %s after excluding %d runtime secret values from checkpoint %q", thread.Thread.ID, len(secrets), rolloutRelative)
}

// TestAppServerA11WorkerOwnedCredentialCheckpointRoundTrip moves the executor
// capability out of stock app-server entirely. Each attempt establishes its
// own authenticated worker MCP session, while app-server receives only the
// frozen dynamic catalog. The rotated restore session may verify the catalog
// but must not replay the completed tool side effect.
func TestAppServerA11WorkerOwnedCredentialCheckpointRoundTrip(t *testing.T) {
	binary, sourcePaths := prepareLiveCodex(t)
	requireCandidateRelease(t, binary, sourcePaths, "0.146.0")
	catalog := approvedDynamicExecutorCatalog(t)
	secrets := a11SecretSentinels()
	credentialSecrets := []secretSentinel{
		{Label: "source MCP capability", Value: a11SourceCapability},
		{Label: "restored MCP capability", Value: a11RestoredCapability},
	}

	toolCall, err := scriptedmodel.NamespacedFunctionCall(
		"response-a11-worker-tool-call",
		a11ToolCallID,
		executorDynamicNamespace,
		approvedMCPToolName,
		`{"message":"return a model-visible non-secret marker"}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	firstFinal, err := scriptedmodel.AssistantMessage(
		"response-a11-worker-first-final",
		"message-a11-worker-first-final",
		a11FirstAssistantText,
	)
	if err != nil {
		t.Fatal(err)
	}
	firstModelServer, err := scriptedmodel.Start(scriptedmodel.Config{
		Responses: []scriptedmodel.Response{toolCall, firstFinal},
	})
	if err != nil {
		t.Fatalf("start A11 worker source model: %v", err)
	}
	t.Cleanup(firstModelServer.Close)

	var sideEffects atomic.Int64
	firstGateway := startWorkerMCPGateway(t, workerMCPGatewayConfig{
		BearerToken: a11SourceCapability,
		Catalog:     catalog,
		CallTool: func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if err := validateA11WorkerMCPCall(request, catalog, "run-a11-worker-source", 31); err != nil {
				return nil, err
			}
			if got := sideEffects.Add(1); got != 1 {
				return nil, fmt.Errorf("A11 source executor side effects = %d, want one", got)
			}
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "safe checkpoint result"}},
				StructuredContent: map[string]any{
					"checkpointMarker": a11ToolMarker,
				},
			}, nil
		},
	})
	firstMCPClient := connectWorkerMCPClient(t, firstGateway, a11SourceCapability, catalog)
	firstBridge, err := harnessworker.NewDynamicBridge(
		firstMCPClient,
		8,
		harnessworker.DefaultLimits().MaxArgumentBytes,
	)
	if err != nil {
		t.Fatal(err)
	}

	writeScriptedModelConfigWithOptions(t, sourcePaths.codexHome, firstModelServer.URL(), scriptedModelConfigOptions{
		disableUpdatePlan: true,
	})
	writeA11RuntimeSecretFiles(t, sourcePaths.codexHome)
	assertA11WorkerCredentialBoundary(t, "source", sourcePaths, firstGateway.Endpoint(), credentialSecrets)
	firstProcess := startPreparedLiveCodex(t, binary, sourcePaths, "app-server", "--listen", "stdio://", "--strict-config")
	firstRunner, err := harnessworker.NewAppServerRunner(
		firstProcess.Peer,
		firstBridge,
		harnessworker.DefaultAppServerRunnerOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	firstEventsDrained := drainAppServerRunnerEvents(firstRunner)
	firstResult, err := firstRunner.Run(t.Context(), harnessworker.AppServerRunRequest{
		RunID:                "run-a11-worker-source",
		RunAttemptGeneration: 31,
		ClientInfo: harnessworker.AppServerClientInfo{
			Name:    "agentserver_v2_conformance",
			Title:   "agentserver v2 conformance",
			Version: "0.0.0",
		},
		Catalog: catalog,
		Start: &harnessworker.AppServerThreadStart{
			Model:                 conformanceModelName,
			CWD:                   sourcePaths.cwd,
			BaseInstructions:      "Return only the scripted model result.",
			DeveloperInstructions: "Use only the frozen worker-owned executor callback.",
		},
		UserText: a11FirstUserText,
	})
	if err != nil {
		t.Fatal(err)
	}
	<-firstEventsDrained
	if firstResult.Terminal.Turn.Status != "completed" || firstBridge.Outstanding() != 0 {
		t.Fatalf("A11 worker source lifecycle/outstanding = %+v/%d", firstResult.Terminal, firstBridge.Outstanding())
	}
	closeAndWait(t, firstProcess)
	if err := firstMCPClient.Close(); err != nil {
		t.Fatalf("close A11 source worker MCP: %v", err)
	}
	firstGateway.AssertAuthenticated(t)
	if got := firstGateway.ToolCalls(); got != 1 {
		t.Fatalf("A11 source worker MCP calls = %d, want one", got)
	}
	if got := sideEffects.Load(); got != 1 {
		t.Fatalf("A11 source executor side effects = %d, want one", got)
	}

	if failures := firstModelServer.Failures(); len(failures) != 0 {
		t.Fatalf("A11 worker source model failures: %v", failures)
	}
	firstModelRequests := firstModelServer.Requests()
	if len(firstModelRequests) != 2 {
		t.Fatalf("A11 worker source model received %d requests, want two", len(firstModelRequests))
	}
	for index, request := range firstModelRequests {
		assertA11ModelRequestSecretBoundary(t, fmt.Sprintf("source model request %d", index), request, secrets)
	}
	firstInitial := decodeCapturedModelRequest(t, firstModelRequests[0])
	firstFollowup := decodeCapturedModelRequest(t, firstModelRequests[1])
	wantSurface := []string{executorDynamicNamespace + "." + approvedMCPToolName}
	if got := modelToolNames(t, firstInitial.Tools); !reflect.DeepEqual(got, wantSurface) {
		t.Fatalf("A11 worker source tool surface = %v, want %v", got, wantSurface)
	}
	if !modelInputContainsFunctionOutput(firstFollowup.Input, a11ToolCallID, a11ToolMarker) {
		t.Fatalf("A11 worker source omitted dynamic result: input=%s", encodeModelInput(t, firstFollowup.Input))
	}
	stderr, stderrTruncated := firstProcess.Stderr()
	if stderrTruncated {
		t.Fatal("A11 worker source app-server stderr exceeded the probe bound")
	}
	assertBytesExcludeSecrets(t, "A11 worker source app-server stderr", stderr, secrets)

	sourceSnapshot := snapshotStateTree(t, sourcePaths.codexHome)
	assertA11StateTreeExcludesCredentials(t, "source CODEX_HOME", sourcePaths.codexHome, sourceSnapshot, credentialSecrets)
	for relative, secret := range a11RuntimeSecretFileSentinels() {
		if _, exists := sourceSnapshot[relative]; !exists {
			t.Fatalf("A11 worker source runtime omitted sentinel file %q", relative)
		}
		contents, err := os.ReadFile(filepath.Join(sourcePaths.codexHome, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatalf("read A11 worker source sentinel file %q: %v", relative, err)
		}
		if !bytes.Contains(contents, []byte(secret.Value)) {
			t.Fatalf("A11 worker source sentinel file %q omitted its %s sentinel", relative, secret.Label)
		}
	}
	rolloutRelative := stateRelativePath(t, sourcePaths.codexHome, firstResult.Thread.Thread.Path)
	rolloutSnapshot, exists := sourceSnapshot[rolloutRelative]
	if !exists || !strings.HasSuffix(rolloutRelative, ".jsonl") || rolloutSnapshot.Size == 0 {
		t.Fatalf("A11 worker rollout snapshot = %+v at %q", rolloutSnapshot, rolloutRelative)
	}
	rolloutContents, err := os.ReadFile(firstResult.Thread.Thread.Path)
	if err != nil {
		t.Fatalf("read A11 worker source rollout: %v", err)
	}
	assertBytesExcludeSecrets(t, "A11 worker source rollout", rolloutContents, secrets)
	assertCompleteRolloutJSONL(
		t,
		firstResult.Thread.Thread.Path,
		firstResult.Thread.Thread.ID,
		a11FirstUserText,
		a11FirstAssistantText,
		a11ToolCallID,
		a11ToolMarker,
	)

	restoredPaths := createRestoredLivePaths(t, t.TempDir(), "a11-worker-restored-runtime")
	copyCheckpointFile(t, sourcePaths.codexHome, restoredPaths.codexHome, rolloutRelative, rolloutSnapshot)
	wantCheckpointSnapshot := map[string]stateFileSnapshot{rolloutRelative: rolloutSnapshot}
	if got := snapshotStateTree(t, restoredPaths.codexHome); !reflect.DeepEqual(got, wantCheckpointSnapshot) {
		t.Fatalf("A11 worker checkpoint contains runtime-only files: got=%v want=%v", got, wantCheckpointSnapshot)
	}
	restoredRolloutPath := filepath.Join(restoredPaths.codexHome, filepath.FromSlash(rolloutRelative))
	checkpointContents, err := os.ReadFile(restoredRolloutPath)
	if err != nil {
		t.Fatalf("read A11 worker checkpoint rollout: %v", err)
	}
	assertBytesExcludeSecrets(t, "A11 worker checkpoint", checkpointContents, secrets)
	retiredSourceHome := filepath.Join(sourcePaths.root, "retired-a11-worker-source-codex-home")
	if err := os.Rename(sourcePaths.codexHome, retiredSourceHome); err != nil {
		t.Fatalf("retire A11 worker source CODEX_HOME: %v", err)
	}
	if _, err := os.Stat(firstResult.Thread.Thread.Path); !os.IsNotExist(err) {
		t.Fatalf("A11 worker source rollout remained available after retirement: %v", err)
	}

	secondFinal, err := scriptedmodel.AssistantMessage(
		"response-a11-worker-restored-final",
		"message-a11-worker-restored-final",
		a11SecondAssistantText,
	)
	if err != nil {
		t.Fatal(err)
	}
	secondModelServer, err := scriptedmodel.Start(scriptedmodel.Config{
		Responses: []scriptedmodel.Response{secondFinal},
	})
	if err != nil {
		t.Fatalf("start A11 worker restored model: %v", err)
	}
	t.Cleanup(secondModelServer.Close)
	secondGateway := startWorkerMCPGateway(t, workerMCPGatewayConfig{
		BearerToken: a11RestoredCapability,
		Catalog:     catalog,
		CallTool: func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			sideEffects.Add(1)
			return nil, errors.New("A11 restored checkpoint replayed an executor side effect")
		},
	})
	secondMCPClient := connectWorkerMCPClient(t, secondGateway, a11RestoredCapability, catalog)
	secondBridge, err := harnessworker.NewDynamicBridge(
		secondMCPClient,
		8,
		harnessworker.DefaultLimits().MaxArgumentBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	writeScriptedModelConfigWithOptions(t, restoredPaths.codexHome, secondModelServer.URL(), scriptedModelConfigOptions{
		disableUpdatePlan: true,
	})
	writeA11RuntimeSecretFiles(t, restoredPaths.codexHome)
	assertA11WorkerCredentialBoundary(t, "restored", restoredPaths, secondGateway.Endpoint(), credentialSecrets)
	secondProcess := startPreparedLiveCodex(t, binary, restoredPaths, "app-server", "--listen", "stdio://", "--strict-config")
	secondRunner, err := harnessworker.NewAppServerRunner(
		secondProcess.Peer,
		secondBridge,
		harnessworker.DefaultAppServerRunnerOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	secondEventsDrained := drainAppServerRunnerEvents(secondRunner)
	secondResult, err := secondRunner.Run(t.Context(), harnessworker.AppServerRunRequest{
		RunID:                "run-a11-worker-restored",
		RunAttemptGeneration: 32,
		ClientInfo: harnessworker.AppServerClientInfo{
			Name:    "agentserver_v2_conformance",
			Title:   "agentserver v2 conformance",
			Version: "0.0.0",
		},
		Catalog: catalog,
		Resume: &harnessworker.AppServerThreadResume{
			ThreadID:                firstResult.Thread.Thread.ID,
			RolloutPath:             restoredRolloutPath,
			CWD:                     restoredPaths.cwd,
			CheckpointCatalogDigest: catalog.Digest(),
		},
		UserText: a11SecondUserText,
	})
	if err != nil {
		t.Fatal(err)
	}
	<-secondEventsDrained
	if !secondResult.Resumed || secondResult.Terminal.Turn.Status != "completed" || secondBridge.Outstanding() != 0 {
		t.Fatalf("A11 worker restored lifecycle/outstanding = %+v/%d", secondResult, secondBridge.Outstanding())
	}
	if secondResult.Thread.Thread.ID != firstResult.Thread.Thread.ID ||
		secondResult.Thread.Thread.SessionID != firstResult.Thread.Thread.SessionID {
		t.Fatalf("A11 worker restored thread identity changed: source=%+v restored=%+v", firstResult.Thread.Thread, secondResult.Thread.Thread)
	}
	closeAndWait(t, secondProcess)
	if err := secondMCPClient.Close(); err != nil {
		t.Fatalf("close A11 restored worker MCP: %v", err)
	}
	secondGateway.AssertAuthenticated(t)
	if calls := secondGateway.ToolCalls(); calls != 0 {
		t.Fatalf("A11 worker restore replayed %d MCP calls", calls)
	}
	if got := sideEffects.Load(); got != 1 {
		t.Fatalf("A11 worker executor side effects across restore = %d, want one", got)
	}

	restoredStderr, restoredStderrTruncated := secondProcess.Stderr()
	if restoredStderrTruncated {
		t.Fatal("A11 worker restored app-server stderr exceeded the probe bound")
	}
	assertBytesExcludeSecrets(t, "A11 worker restored app-server stderr", restoredStderr, secrets)
	if failures := secondModelServer.Failures(); len(failures) != 0 {
		t.Fatalf("A11 worker restored model failures: %v", failures)
	}
	secondModelRequests := secondModelServer.Requests()
	if len(secondModelRequests) != 1 {
		t.Fatalf("A11 worker restored model received %d requests, want one", len(secondModelRequests))
	}
	restoredModelRequest := decodeCapturedModelRequest(t, secondModelRequests[0])
	if !modelInputContainsUserText(t, restoredModelRequest.Input, a11FirstUserText) ||
		!modelInputContainsUserText(t, restoredModelRequest.Input, a11SecondUserText) ||
		!modelInputContainsFunctionOutput(restoredModelRequest.Input, a11ToolCallID, a11ToolMarker) {
		t.Fatalf("A11 worker restored context omitted model-visible history: input=%s", encodeModelInput(t, restoredModelRequest.Input))
	}
	if got := modelToolNames(t, restoredModelRequest.Tools); !reflect.DeepEqual(got, wantSurface) {
		t.Fatalf("A11 worker restored tool surface = %v, want %v", got, wantSurface)
	}
	if !reflect.DeepEqual(modelToolValues(t, restoredModelRequest.Tools), modelToolValues(t, firstInitial.Tools)) {
		t.Fatal("A11 worker restore changed the frozen model tool schema")
	}
	assertA11ModelRequestSecretBoundary(t, "restored model request", secondModelRequests[0], secrets)
	restoredRuntimeSnapshot := snapshotStateTree(t, restoredPaths.codexHome)
	assertA11StateTreeExcludesCredentials(t, "restored CODEX_HOME", restoredPaths.codexHome, restoredRuntimeSnapshot, credentialSecrets)
	restoredRolloutContents, err := os.ReadFile(secondResult.Thread.Thread.Path)
	if err != nil {
		t.Fatalf("read A11 worker restored rollout: %v", err)
	}
	assertBytesExcludeSecrets(t, "A11 worker restored rollout", restoredRolloutContents, secrets)
	t.Logf(
		"A11 worker restored %s with catalog %s, rotated bearer, and one total executor side effect",
		firstResult.Thread.Thread.ID,
		catalog.Digest(),
	)
}

func validateA11WorkerMCPCall(
	request *mcp.CallToolRequest,
	catalog *harnessworker.Catalog,
	wantRunID string,
	wantGeneration int64,
) error {
	if request == nil || request.Params == nil {
		return errors.New("A11 worker MCP call has no params")
	}
	metadataBytes, err := json.Marshal(request.Params.Meta)
	if err != nil {
		return fmt.Errorf("encode A11 worker MCP metadata: %w", err)
	}
	var metadata struct {
		RunID                string `json:"io.agentserver/runId"`
		ThreadID             string `json:"io.agentserver/threadId"`
		TurnID               string `json:"io.agentserver/turnId"`
		CallID               string `json:"io.agentserver/callId"`
		RunAttemptGeneration int64  `json:"io.agentserver/runAttemptGeneration"`
		ToolCatalogDigest    string `json:"io.agentserver/toolCatalogDigest"`
	}
	if err := json.Unmarshal(metadataBytes, &metadata); err != nil {
		return fmt.Errorf("decode A11 worker MCP metadata: %w", err)
	}
	if metadata.RunID != wantRunID || metadata.ThreadID == "" || metadata.TurnID == "" ||
		metadata.CallID != a11ToolCallID || metadata.RunAttemptGeneration != wantGeneration ||
		metadata.ToolCatalogDigest != catalog.Digest() {
		return fmt.Errorf("A11 worker MCP metadata = %+v", metadata)
	}
	var arguments struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(request.Params.Arguments, &arguments); err != nil {
		return fmt.Errorf("decode A11 worker MCP arguments: %w", err)
	}
	if arguments.Message != "return a model-visible non-secret marker" {
		return fmt.Errorf("A11 worker MCP message = %q", arguments.Message)
	}
	return nil
}

func assertA11WorkerCredentialBoundary(
	t *testing.T,
	attempt string,
	paths livePaths,
	gatewayEndpoint string,
	credentialSecrets []secretSentinel,
) {
	t.Helper()
	environment := []byte(strings.Join(paths.environment, "\x00"))
	assertBytesExcludeSecrets(t, "A11 worker "+attempt+" child environment", environment, credentialSecrets)
	if bytes.Contains(environment, []byte(a11CapabilityEnvName+"=")) {
		t.Fatalf("A11 worker %s child environment contains the legacy capability variable", attempt)
	}
	config, err := os.ReadFile(filepath.Join(paths.codexHome, "config.toml"))
	if err != nil {
		t.Fatalf("read A11 worker %s app-server config: %v", attempt, err)
	}
	assertBytesExcludeSecrets(t, "A11 worker "+attempt+" app-server config", config, credentialSecrets)
	if bytes.Contains(config, []byte(gatewayEndpoint)) || bytes.Contains(config, []byte("[mcp_servers.")) ||
		bytes.Contains(config, []byte(a11CapabilityEnvName)) {
		t.Fatalf("A11 worker %s app-server config contains a worker MCP endpoint or credential reference", attempt)
	}
}

func assertA11StateTreeExcludesCredentials(
	t *testing.T,
	artifact string,
	root string,
	snapshot map[string]stateFileSnapshot,
	credentialSecrets []secretSentinel,
) {
	t.Helper()
	paths := make([]string, 0, len(snapshot))
	for relative := range snapshot {
		paths = append(paths, relative)
	}
	sort.Strings(paths)
	for _, relative := range paths {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatalf("read A11 worker %s file %q: %v", artifact, relative, err)
		}
		assertBytesExcludeSecrets(t, "A11 worker "+artifact+" file "+relative, contents, credentialSecrets)
	}
}

func assertA11ModelRequestSecretBoundary(
	t *testing.T,
	artifact string,
	request scriptedmodel.Request,
	secrets []secretSentinel,
) {
	t.Helper()
	headers, err := json.Marshal(request.Header)
	if err != nil {
		t.Fatalf("encode A11 worker %s headers: %v", artifact, err)
	}
	for _, secret := range secrets {
		if secret.Value == a11ModelAuthSecret {
			continue
		}
		assertBytesExcludeSecrets(t, "A11 worker "+artifact+" headers", headers, []secretSentinel{secret})
	}
	authorization := request.Header.Values("Authorization")
	wantAuthorization := "Bearer " + a11ModelAuthSecret
	if len(authorization) != 1 || authorization[0] != wantAuthorization {
		firstLength := 0
		if len(authorization) != 0 {
			firstLength = len(authorization[0])
		}
		t.Fatalf(
			"A11 worker %s model Authorization boundary mismatch: values=%d first_present=%t first_length=%d",
			artifact,
			len(authorization),
			firstLength != 0,
			firstLength,
		)
	}
	for name, values := range request.Header {
		if strings.EqualFold(name, "Authorization") {
			continue
		}
		for _, value := range values {
			if strings.Contains(value, a11ModelAuthSecret) {
				t.Fatalf("A11 worker %s model auth sentinel entered non-Authorization header %q", artifact, name)
			}
		}
	}
	assertBytesExcludeSecrets(t, "A11 worker "+artifact+" body", request.Body, secrets)
}

func a11SecretSentinels() []secretSentinel {
	return []secretSentinel{
		{Label: "source MCP capability", Value: a11SourceCapability},
		{Label: "restored MCP capability", Value: a11RestoredCapability},
		{Label: "config", Value: a11ConfigSecret},
		{Label: "requirements", Value: a11RequirementsSecret},
		{Label: "model transport auth", Value: a11ModelAuthSecret},
		{Label: "token file", Value: a11TokenFileSecret},
		{Label: "log", Value: a11LogSecret},
		{Label: "environment dump", Value: a11EnvironmentDumpSecret},
		{Label: "transport buffer", Value: a11TransportSecret},
	}
}

func a11RuntimeSecretFileSentinels() map[string]secretSentinel {
	return map[string]secretSentinel{
		"auth.json":                  {Label: "model transport auth", Value: a11ModelAuthSecret},
		"config.toml":                {Label: "config", Value: a11ConfigSecret},
		"diagnostics/a11.log":        {Label: "log", Value: a11LogSecret},
		"diagnostics/env.dump":       {Label: "environment dump", Value: a11EnvironmentDumpSecret},
		"requirements.toml":          {Label: "requirements", Value: a11RequirementsSecret},
		"tokens/a11.token":           {Label: "token file", Value: a11TokenFileSecret},
		"transport/a11-buffer.jsonl": {Label: "transport buffer", Value: a11TransportSecret},
	}
}

func writeA11RuntimeSecretFiles(t *testing.T, codexHome string) {
	t.Helper()
	configPath := filepath.Join(codexHome, "config.toml")
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read A11 config for sentinel injection: %v", err)
	}
	config = append(config, []byte("\n# runtime-only sentinel: "+a11ConfigSecret+"\n")...)
	if err := os.WriteFile(configPath, config, 0o600); err != nil {
		t.Fatalf("write A11 config sentinel: %v", err)
	}
	files := map[string]string{
		"auth.json":                  `{"OPENAI_API_KEY":"` + a11ModelAuthSecret + `"}`,
		"diagnostics/a11.log":        "runtime log " + a11LogSecret + "\n",
		"diagnostics/env.dump":       a11CapabilityEnvName + "=" + a11EnvironmentDumpSecret + "\n",
		"requirements.toml":          "# runtime requirements " + a11RequirementsSecret + "\n",
		"tokens/a11.token":           a11TokenFileSecret + "\n",
		"transport/a11-buffer.jsonl": `{"pending":"` + a11TransportSecret + `"}` + "\n",
	}
	for relative, contents := range files {
		path := filepath.Join(codexHome, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("create A11 runtime secret directory for %q: %v", relative, err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatalf("write A11 runtime secret file %q: %v", relative, err)
		}
	}
}

func assertBytesExcludeSecrets(t *testing.T, artifact string, contents []byte, secrets []secretSentinel) {
	t.Helper()
	for _, secret := range secrets {
		if secret.Value == "" {
			t.Fatalf("A11 %s sentinel is empty", secret.Label)
		}
		if bytes.Contains(contents, []byte(secret.Value)) {
			t.Fatalf("%s contains the A11 %s sentinel", artifact, secret.Label)
		}
	}
}

func assertMCPBearerToken(t *testing.T, server *scriptedmcp.Server, token string) {
	t.Helper()
	requests := server.Requests()
	if len(requests) == 0 {
		t.Fatal("A11 MCP server received no authenticated requests")
	}
	want := "Bearer " + token
	for index, request := range requests {
		got := request.Header.Get("Authorization")
		if got != want {
			t.Fatalf("A11 MCP request %d bearer mismatch: present=%t length=%d", index, got != "", len(got))
		}
	}
}

func createRestoredLivePaths(t *testing.T, parentRoot, name string) livePaths {
	t.Helper()
	root := filepath.Join(parentRoot, name)
	paths := livePaths{
		root:      root,
		home:      filepath.Join(root, "home"),
		codexHome: filepath.Join(root, "codex-home"),
		temporary: filepath.Join(root, "tmp"),
		cwd:       filepath.Join(root, "cwd"),
	}
	for _, directory := range []string{paths.home, paths.codexHome, paths.temporary, paths.cwd} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatalf("create restored runtime directory: %v", err)
		}
	}
	environment, err := codexprocess.Environment(paths.home, paths.codexHome, paths.temporary, nil)
	if err != nil {
		t.Fatalf("build restored runtime environment: %v", err)
	}
	paths.environment = environment
	return paths
}

func copyCheckpointFile(
	t *testing.T,
	sourceRoot string,
	destinationRoot string,
	relative string,
	want stateFileSnapshot,
) {
	t.Helper()
	relative = filepath.FromSlash(relative)
	if filepath.IsAbs(relative) || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		t.Fatalf("checkpoint path is unsafe: %q", relative)
	}
	source := filepath.Join(sourceRoot, relative)
	destination := filepath.Join(destinationRoot, relative)
	contents, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read checkpoint file %q: %v", relative, err)
	}
	digest := sha256.Sum256(contents)
	if int64(len(contents)) != want.Size || hex.EncodeToString(digest[:]) != want.SHA256 {
		t.Fatalf("checkpoint source %q changed after manifest snapshot", relative)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		t.Fatalf("create checkpoint destination for %q: %v", relative, err)
	}
	if err := os.WriteFile(destination, contents, want.Mode.Perm()); err != nil {
		t.Fatalf("write checkpoint file %q: %v", relative, err)
	}
}

func resumeAppServerThreadAndStartTurn(
	t *testing.T,
	collector *rpcCollector,
	threadID string,
	rolloutPath string,
	cwd string,
	userText string,
) (threadStartResult, appServerTurn) {
	t.Helper()
	resumeParams := map[string]any{
		"threadId":     threadID,
		"excludeTurns": true,
	}
	if rolloutPath != "" {
		resumeParams["path"] = rolloutPath
	}
	sendRPC(t, collector.process, map[string]any{
		"id":     2,
		"method": "thread/resume",
		"params": resumeParams,
	})
	thread := decodeThreadStart(t, collector.response(t, "2"))
	// Unlike thread/start and thread/fork, a cold thread/resume has no
	// thread/started notification. Its RPC response is the lifecycle barrier.
	sendRPC(t, collector.process, map[string]any{
		"id":     3,
		"method": "turn/start",
		"params": map[string]any{
			"threadId":       thread.Thread.ID,
			"cwd":            cwd,
			"approvalPolicy": "never",
			"environments":   []any{},
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
	// Reading the later turn/started drains every causally earlier stdout frame.
	// A resume-specific thread/started would now be buffered if Codex emitted it.
	for _, notification := range collector.notifications {
		if notification.Method == "thread/started" {
			t.Fatal("cold thread/resume unexpectedly emitted thread/started")
		}
	}
	return thread, turn
}

func snapshotStateTree(t *testing.T, root string) map[string]stateFileSnapshot {
	t.Helper()
	root = filepath.Clean(root)
	result := make(map[string]stateFileSnapshot)
	var totalBytes int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("relativize state path %q: %w", path, err)
		}
		if relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("state path escaped root: %q", relative)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("state tree contains symlink %q", relative)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat state file %q: %w", relative, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("state tree contains non-regular file %q with mode %s", relative, info.Mode())
		}
		if info.Size() < 0 || info.Size() > maxA08StateFileBytes {
			return fmt.Errorf("state file %q has size %d outside [0, %d]", relative, info.Size(), maxA08StateFileBytes)
		}
		totalBytes += info.Size()
		if totalBytes > maxA08StateTreeBytes {
			return fmt.Errorf("state tree exceeds %d bytes", maxA08StateTreeBytes)
		}
		if len(result) >= maxA08StateFiles {
			return fmt.Errorf("state tree exceeds %d files", maxA08StateFiles)
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read state file %q: %w", relative, err)
		}
		if int64(len(contents)) != info.Size() {
			return fmt.Errorf("state file %q changed size while snapshotting: stat=%d read=%d", relative, info.Size(), len(contents))
		}
		digest := sha256.Sum256(contents)
		result[filepath.ToSlash(relative)] = stateFileSnapshot{
			Mode:   info.Mode().Perm(),
			Size:   info.Size(),
			SHA256: hex.EncodeToString(digest[:]),
		}
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot stable CODEX_HOME: %v", err)
	}
	return result
}

func stateRelativePath(t *testing.T, root, path string) string {
	t.Helper()
	if path == "" || !filepath.IsAbs(path) {
		t.Fatalf("state path must be absolute and non-empty: %q", path)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("resolve CODEX_HOME %q: %v", root, err)
	}
	canonicalPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve state path %q: %v", path, err)
	}
	relative, err := filepath.Rel(filepath.Clean(canonicalRoot), filepath.Clean(canonicalPath))
	if err != nil {
		t.Fatalf("relativize state path %q: %v", path, err)
	}
	if relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		t.Fatalf("state path %q escaped CODEX_HOME %q", path, root)
	}
	return filepath.ToSlash(relative)
}

func assertCompleteRolloutJSONL(t *testing.T, path string, required ...string) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open rollout: %v", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), maxA08RolloutLine)
	lineCount := 0
	var contents bytes.Buffer
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		if len(bytes.TrimSpace(line)) == 0 || !json.Valid(line) {
			t.Fatalf("rollout line %d is not one non-empty JSON value", lineCount+1)
		}
		lineCount++
		contents.Write(line)
		contents.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan rollout: %v", err)
	}
	if lineCount == 0 {
		t.Fatal("rollout contains no JSONL records")
	}
	for _, value := range required {
		if value == "" || !bytes.Contains(contents.Bytes(), []byte(value)) {
			t.Fatalf("rollout omitted required completed-turn value %q", value)
		}
	}
}

func assertSQLiteHeader(t *testing.T, path string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read SQLite state database: %v", err)
	}
	if len(contents) < 100 || !bytes.Equal(contents[:16], []byte("SQLite format 3\x00")) {
		t.Fatalf("state database %q lacks a valid SQLite format header", path)
	}
}

func formatStateTree(snapshot map[string]stateFileSnapshot) string {
	paths := make([]string, 0, len(snapshot))
	for path := range snapshot {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	entries := make([]string, 0, len(paths))
	for _, path := range paths {
		file := snapshot[path]
		entries = append(entries, fmt.Sprintf("%s(%d,%s)", path, file.Size, file.SHA256[:12]))
	}
	return strings.Join(entries, ", ")
}
