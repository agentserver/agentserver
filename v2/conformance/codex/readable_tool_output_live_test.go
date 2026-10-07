package codex_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/conformance/codex/internal/scriptedmodel"
	"github.com/agentserver/agentserver/v2/internal/executorgateway/mcpcontract"
	"github.com/agentserver/agentserver/v2/internal/harnessworker"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Exercise both real protocol boundaries and inspect what stock Codex sends
// back to the model, not just a helper's return value. All endpoints are local
// mocks; no commands, external models or account credentials are used.
func TestAppServerModelReceivesDecodedExecutorOutput(t *testing.T) {
	binary, paths := prepareLiveCodex(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	tool, _ := mcpcontract.Lookup(mcpcontract.ToolShell)
	catalog, err := harnessworker.BuildCatalog(mcpcontract.Namespace, mcpcontract.NamespaceDescription, []harnessworker.ToolDescriptor{{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema}}, harnessworker.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "readable-output-test", Version: "1"}, nil)
	mcpServer.AddTool(&mcp.Tool{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		data := []byte("飞书文档：你好\n")
		doc := map[string]any{"process_id": "10000000-0000-4000-8000-000000000001", "status": "succeeded", "exit_code": 0, "sandbox_denied": false, "timed_out": false, "output_complete": true, "next_sequence": 3, "chunks": []any{
			map[string]any{"sequence": 1, "stream": "stdout", "chunk_base64": base64.StdEncoding.EncodeToString(data[:2])},
			map[string]any{"sequence": 2, "stream": "stdout", "chunk_base64": base64.StdEncoding.EncodeToString(data[2:])},
		}}
		return &mcp.CallToolResult{Content: []mcp.Content{}, StructuredContent: doc}, nil
	})
	transport := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, nil)
	mcpHTTP := httptest.NewServer(transport)
	defer mcpHTTP.Close()
	client, err := harnessworker.ConnectMCP(ctx, harnessworker.MCPClientConfig{Endpoint: mcpHTTP.URL + "/mcp", BearerToken: "local-test-only", AllowInsecureLoopback: true, Namespace: catalog.Namespace(), NamespaceDescription: catalog.NamespaceDescription(), ExpectedCatalogDigest: catalog.Digest(), ExpectedCatalog: catalog.CanonicalBytes(), Limits: harnessworker.DefaultLimits(), CloseGrace: time.Second, ElicitationHandler: func(context.Context, harnessworker.ElicitationRequest) (harnessworker.ElicitationDecision, error) {
		t.Error("unexpected approval request in text conversion test")
		return harnessworker.ElicitationDecision{Action: harnessworker.ApprovalCancel}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	call, err := scriptedmodel.NamespacedFunctionCall("response-output-1", "call-output-1", "executor", "shell", `{"environment_id":"20000000-0000-4000-8000-000000000002","argv":["example"]}`)
	if err != nil {
		t.Fatal(err)
	}
	answer, err := scriptedmodel.AssistantMessage("response-output-2", "answer-output-2", "done")
	if err != nil {
		t.Fatal(err)
	}
	model, err := scriptedmodel.Start(scriptedmodel.Config{Responses: []scriptedmodel.Response{call, answer}})
	if err != nil {
		t.Fatal(err)
	}
	defer model.Close()
	writeScriptedModelConfigWithOptions(t, paths.codexHome, model.URL(), scriptedModelConfigOptions{disableUpdatePlan: true})
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
	result, err := runner.Run(ctx, harnessworker.AppServerRunRequest{RunID: "run-readable-output", RunAttemptGeneration: 1, ClientInfo: harnessworker.AppServerClientInfo{Name: "agentserver_v2_conformance", Title: "readable output test", Version: "0.0.0"}, Catalog: catalog, Start: &harnessworker.AppServerThreadStart{Model: conformanceModelName, CWD: paths.cwd, BaseInstructions: "Use the test tool and report its result."}, UserText: "read tool output"})
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
	second := decodeCapturedModelRequest(t, requests[1])
	if !modelInputContainsFunctionOutput(second.Input, "call-output-1", "stdout:\n飞书文档：你好\n") {
		t.Fatalf("model did not receive decoded text: %s", encodeModelInput(t, second.Input))
	}
	raw, _ := json.Marshal(second.Input)
	if strings.Contains(string(raw), "chunk_base64") || strings.Contains(string(raw), base64.StdEncoding.EncodeToString([]byte("飞书文档：你好\n"))) {
		t.Fatal("transport base64 reached model")
	}
	closeAndWait(t, process)
}
