package codex_test

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/conformance/codex/internal/codexprocess"
	"github.com/agentserver/agentserver/v2/conformance/codex/internal/scriptedmcp"
	"github.com/agentserver/agentserver/v2/conformance/codex/internal/scriptedmodel"
	"github.com/agentserver/agentserver/v2/internal/codexwire"
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
)

type stateFileSnapshot struct {
	Mode   os.FileMode
	Size   int64
	SHA256 string
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
