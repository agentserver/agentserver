package codex_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/conformance/codex/internal/scriptedmodel"
	"github.com/agentserver/agentserver/v2/internal/codexmodelcatalog"
	"github.com/agentserver/agentserver/v2/internal/executorgateway/mcpcontract"
	"github.com/agentserver/agentserver/v2/internal/harnessworker"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Unlike the synthetic-model probes, exercise the production model's stock
// metadata. Its tool_mode can override disabled feature flags in newer Codex.
func TestAppServerProductionModelListsEnvironments(t *testing.T) {
	binary, paths := prepareLiveCodex(t)
	requireCandidateRelease(t, binary, paths, "0.160.1")
	tool, _ := mcpcontract.Lookup(mcpcontract.ToolListEnvironments)
	catalog, err := harnessworker.BuildCatalog(mcpcontract.Namespace, mcpcontract.NamespaceDescription, []harnessworker.ToolDescriptor{{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema}}, harnessworker.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	toolCalls := 0
	server := mcp.NewServer(&mcp.Implementation{Name: "production-model-test", Version: "1"}, nil)
	server.AddTool(&mcp.Tool{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		toolCalls++
		return &mcp.CallToolResult{StructuredContent: map[string]any{"environments": []any{map[string]any{"id": "20000000-0000-4000-8000-000000000002", "name": "sg-test-environment"}}}}, nil
	})
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer httpServer.Close()
	client, err := harnessworker.ConnectMCP(t.Context(), harnessworker.MCPClientConfig{Endpoint: httpServer.URL + "/mcp", BearerToken: "local-test-only", AllowInsecureLoopback: true, Namespace: catalog.Namespace(), NamespaceDescription: catalog.NamespaceDescription(), ExpectedCatalogDigest: catalog.Digest(), ExpectedCatalog: catalog.CanonicalBytes(), Limits: harnessworker.DefaultLimits(), ElicitationHandler: func(context.Context, harnessworker.ElicitationRequest) (harnessworker.ElicitationDecision, error) {
		t.Error("unexpected approval")
		return harnessworker.ElicitationDecision{Action: harnessworker.ApprovalCancel}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	call, err := scriptedmodel.NamespacedFunctionCall("production-list", "production-list-call", "executor", "list_environments", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	final, err := scriptedmodel.AssistantMessage("production-final", "production-final-message", "Environment listed")
	if err != nil {
		t.Fatal(err)
	}
	model, err := scriptedmodel.Start(scriptedmodel.Config{Responses: []scriptedmodel.Response{call, final}})
	if err != nil {
		t.Fatal(err)
	}
	defer model.Close()
	writeProductionModelProbeConfig(t, paths, model.URL())
	process := startPreparedLiveCodex(t, binary, paths, "app-server", "--listen", "stdio://", "--strict-config")
	bridge, err := harnessworker.NewDynamicBridge(client, 8, harnessworker.DefaultLimits().MaxArgumentBytes)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := harnessworker.NewAppServerRunner(process.Peer, bridge, harnessworker.DefaultAppServerRunnerOptions())
	if err != nil {
		t.Fatal(err)
	}
	drained := drainAppServerRunnerEvents(runner)
	result, err := runner.Run(t.Context(), harnessworker.AppServerRunRequest{RunID: "run-production-model", RunAttemptGeneration: 1, ClientInfo: harnessworker.AppServerClientInfo{Name: "agentserver_v2_conformance", Title: "production model regression", Version: "0.0.0"}, Catalog: catalog, Start: &harnessworker.AppServerThreadStart{Model: "gpt-5.6-sol", CWD: paths.cwd, BaseInstructions: "List the execution environments using the executor tool."}, UserText: "列出执行环境"})
	<-drained
	if err != nil {
		t.Fatal(err)
	}
	if result.Terminal.Turn.Status != "completed" {
		t.Fatal(result.Terminal)
	}
	requests := model.Requests()
	if len(requests) != 2 {
		t.Fatalf("model requests=%d", len(requests))
	}
	first := decodeCapturedModelRequest(t, requests[0])
	if names := modelToolNames(t, first.Tools); !reflect.DeepEqual(names, []string{"executor.list_environments"}) {
		t.Fatalf("production tool surface=%v; want executor.list_environments", names)
	}
	second := decodeCapturedModelRequest(t, requests[1])
	input, _ := json.Marshal(second.Input)
	if toolCalls != 1 || !strings.Contains(string(input), "sg-test-environment") {
		t.Fatalf("list_environments calls=%d, expected tool result missing", toolCalls)
	}
	closeAndWait(t, process)

	// Restore only the rollout into a fresh home, regenerating the corrected
	// catalog independently of persisted session state.
	relative := stateRelativePath(t, paths.codexHome, result.Thread.Thread.Path)
	snapshot := snapshotStateTree(t, paths.codexHome)
	restored := createRestoredLivePaths(t, t.TempDir(), "production-model-resume")
	copyCheckpointFile(t, paths.codexHome, restored.codexHome, relative, snapshot[relative])
	if err := os.Rename(paths.codexHome, filepath.Join(paths.root, "retired-codex-home")); err != nil {
		t.Fatal(err)
	}
	resumedModel, err := scriptedmodel.Start(scriptedmodel.Config{Responses: []scriptedmodel.Response{final}})
	if err != nil {
		t.Fatal(err)
	}
	defer resumedModel.Close()
	writeProductionModelProbeConfig(t, restored, resumedModel.URL())
	resumedProcess := startPreparedLiveCodex(t, binary, restored, "app-server", "--listen", "stdio://", "--strict-config")
	resumedBridge, err := harnessworker.NewDynamicBridge(client, 8, harnessworker.DefaultLimits().MaxArgumentBytes)
	if err != nil {
		t.Fatal(err)
	}
	resumedRunner, err := harnessworker.NewAppServerRunner(resumedProcess.Peer, resumedBridge, harnessworker.DefaultAppServerRunnerOptions())
	if err != nil {
		t.Fatal(err)
	}
	resumedDrained := drainAppServerRunnerEvents(resumedRunner)
	resumedResult, err := resumedRunner.Run(t.Context(), harnessworker.AppServerRunRequest{RunID: "run-production-model-resumed", RunAttemptGeneration: 2, ClientInfo: harnessworker.AppServerClientInfo{Name: "agentserver_v2_conformance", Title: "production model resume", Version: "0.0.0"}, Catalog: catalog, Resume: &harnessworker.AppServerThreadResume{ThreadID: result.Thread.Thread.ID, RolloutPath: filepath.Join(restored.codexHome, filepath.FromSlash(relative)), CWD: restored.cwd, CheckpointCatalogDigest: catalog.Digest()}, UserText: "继续"})
	<-resumedDrained
	if err != nil {
		t.Fatal(err)
	}
	if !resumedResult.Resumed || resumedResult.Terminal.Turn.Status != "completed" {
		t.Fatal(resumedResult)
	}
	resumedRequests := resumedModel.Requests()
	if len(resumedRequests) != 1 || toolCalls != 1 {
		t.Fatal("cold resume replayed a tool call or did not reach the model")
	}
	resumedRequest := decodeCapturedModelRequest(t, resumedRequests[0])
	if names := modelToolNames(t, resumedRequest.Tools); !reflect.DeepEqual(names, []string{"executor.list_environments"}) {
		t.Fatalf("cold-resumed tool surface=%v", names)
	}
	resumedInput, _ := json.Marshal(resumedRequest.Input)
	if !strings.Contains(string(resumedInput), "sg-test-environment") {
		t.Fatal("cold resume lost previous environment result")
	}
	closeAndWait(t, resumedProcess)
}

func writeProductionModelProbeConfig(t *testing.T, paths livePaths, serverURL string) {
	t.Helper()
	writeScriptedModelConfigWithOptions(t, paths.codexHome, serverURL, scriptedModelConfigOptions{disableUpdatePlan: true})
	configPath := filepath.Join(paths.codexHome, "config.toml")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := []string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "model_catalog_json = ") {
			lines = append(lines, line)
		}
	}
	config := strings.ReplaceAll(strings.Join(lines, "\n"), conformanceModelName, "gpt-5.6-sol")
	modelCatalog, err := codexmodelcatalog.Direct()
	if err != nil {
		t.Fatal(err)
	}
	catalogPath := filepath.Join(paths.codexHome, codexmodelcatalog.FileName)
	if err := os.WriteFile(catalogPath, modelCatalog, 0400); err != nil {
		t.Fatal(err)
	}
	quotedPath, _ := json.Marshal(catalogPath)
	config = "model_catalog_json = " + string(quotedPath) + "\n" + config
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
}
