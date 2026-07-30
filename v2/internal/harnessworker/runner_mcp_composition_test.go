package harnessworker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/codexwire"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAppServerRunnerComposesWorkerOwnedMCPWithoutCredentialOnWire(t *testing.T) {
	catalog := runnerTestCatalog(t)
	descriptor := catalog.Tools()[0]
	gatewayServer := mcp.NewServer(
		&mcp.Implementation{Name: "fake-executor-gateway", Version: "v2-test"},
		&mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{}},
	)
	callSeen := make(chan DynamicCall, 1)
	gatewayServer.AddTool(&mcp.Tool{
		Name:        descriptor.Name,
		Description: descriptor.Description,
		InputSchema: descriptor.InputSchema,
	}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		call, err := dynamicCallFromMCPRequest(request)
		if err != nil {
			return nil, err
		}
		if digest, ok := request.Params.Meta[MCPMetaToolCatalogDigest].(string); !ok || digest != catalog.Digest() {
			return nil, fmt.Errorf("worker MCP catalog digest = %v", request.Params.Meta[MCPMetaToolCatalogDigest])
		}
		callSeen <- call
		return &mcp.CallToolResult{
			Content:           []mcp.Content{&mcp.TextContent{Text: "approved echo: hello"}},
			StructuredContent: map[string]any{"echoed": "hello"},
		}, nil
	})
	httpServer := startTestMCPServer(t, gatewayServer)
	client, err := ConnectMCP(t.Context(), testMCPClientConfig(
		httpServer.endpoint(),
		catalog,
		DefaultLimits(),
		func(context.Context, ElicitationRequest) (ElicitationDecision, error) {
			return ElicitationDecision{Action: ApprovalCancel}, nil
		},
	))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	workerPeer, appServerPeer, closePair := newRunnerPeerPair(t)
	t.Cleanup(closePair)
	credentialBoundary := &secretRejectingAppServerTransport{
		AppServerTransport: workerPeer,
		secret:             []byte(testMCPBearer),
	}
	bridge, err := NewDynamicBridge(client, 4, catalog.limits.MaxArgumentBytes)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewAppServerRunner(credentialBoundary, bridge, DefaultAppServerRunnerOptions())
	if err != nil {
		t.Fatal(err)
	}

	serverDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := serveRunnerStartLifecycle(ctx, appServerPeer, catalog); err != nil {
			serverDone <- err
			return
		}
		if err := appServerPeer.Send(map[string]any{
			"id":     "callback-worker-mcp",
			"method": "item/tool/call",
			"params": map[string]any{
				"threadId":  runnerTestThreadID,
				"turnId":    runnerTestTurnID,
				"callId":    "call-worker-mcp",
				"namespace": "executor",
				"tool":      "approved_echo",
				"arguments": map[string]any{"message": "hello"},
			},
		}); err != nil {
			serverDone <- err
			return
		}
		response, err := receiveRunnerMessage(ctx, appServerPeer, codexwire.KindResponse, "", `"callback-worker-mcp"`)
		if err != nil {
			serverDone <- err
			return
		}
		var result DynamicToolResult
		if err := response.DecodeResult(&result); err != nil {
			serverDone <- err
			return
		}
		want := DynamicToolResult{
			ContentItems: []InputTextContent{
				{Type: "inputText", Text: "approved echo: hello"},
				{Type: "inputText", Text: `{"echoed":"hello"}`},
			},
			Success: true,
		}
		if !reflect.DeepEqual(result, want) {
			serverDone <- fmt.Errorf("worker MCP dynamic response = %+v, want %+v", result, want)
			return
		}
		serverDone <- sendRunnerTerminal(appServerPeer, "completed")
	}()

	result, err := runner.Run(t.Context(), runnerStartRequest(catalog))
	if err != nil {
		t.Fatal(err)
	}
	if serverErr := <-serverDone; serverErr != nil {
		t.Fatal(serverErr)
	}
	if result.Terminal.Turn.Status != "completed" || bridge.Outstanding() != 0 {
		t.Fatalf("worker MCP runner lifecycle/outstanding = %+v/%d", result.Terminal, bridge.Outstanding())
	}
	select {
	case call := <-callSeen:
		if call.RunID != "run-runner-1" || call.ThreadID != runnerTestThreadID ||
			call.TurnID != runnerTestTurnID || call.CallID != "call-worker-mcp" ||
			call.RunAttemptGeneration != 7 || call.Namespace != "executor" ||
			call.Tool != "approved_echo" || string(call.Arguments) != `{"message":"hello"}` {
			t.Fatalf("worker MCP call projection = %+v", call)
		}
	case <-time.After(time.Second):
		t.Fatal("worker MCP gateway did not receive tools/call")
	}
	if httpServer.requestCount.Load() < 3 || httpServer.badAuthCount.Load() != 0 {
		t.Fatalf("worker MCP HTTP requests/bad auth = %d/%d", httpServer.requestCount.Load(), httpServer.badAuthCount.Load())
	}
}

func TestAppServerRunnerInterruptsAndCleansUpOnRealMCPDisconnect(t *testing.T) {
	catalog := runnerTestCatalog(t)
	descriptor := catalog.Tools()[0]
	gatewayServer := mcp.NewServer(
		&mcp.Implementation{Name: "disconnecting-executor-gateway", Version: "v2-test"},
		&mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{}},
	)
	callEntered := make(chan struct{})
	releaseCall := make(chan struct{})
	callExited := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(releaseCall)
		}
	}()
	var calls atomic.Int64
	gatewayServer.AddTool(&mcp.Tool{
		Name:        descriptor.Name,
		Description: descriptor.Description,
		InputSchema: descriptor.InputSchema,
	}, func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		close(callEntered)
		select {
		case <-ctx.Done():
			// A healthy transport can deliver cancellation. A broken transport
			// requires the gateway's own connection grace and execution deadline.
		case <-releaseCall:
		}
		close(callExited)
		return nil, errors.New("disconnect fixture released the gateway tools/call")
	})
	httpServer := startTestMCPServer(t, gatewayServer)
	client, err := ConnectMCP(t.Context(), testMCPClientConfig(
		httpServer.endpoint(),
		catalog,
		DefaultLimits(),
		func(context.Context, ElicitationRequest) (ElicitationDecision, error) {
			return ElicitationDecision{Action: ApprovalCancel}, nil
		},
	))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	workerPeer, appServerPeer, closePair := newRunnerPeerPair(t)
	t.Cleanup(closePair)
	bridge, err := NewDynamicBridge(client, 4, catalog.limits.MaxArgumentBytes)
	if err != nil {
		t.Fatal(err)
	}
	options := DefaultAppServerRunnerOptions()
	options.InterruptGrace = time.Second
	runner, err := NewAppServerRunner(workerPeer, bridge, options)
	if err != nil {
		t.Fatal(err)
	}

	serverDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := serveRunnerStartLifecycle(ctx, appServerPeer, catalog); err != nil {
			serverDone <- err
			return
		}
		if err := appServerPeer.Send(map[string]any{
			"id":     "callback-mcp-disconnect",
			"method": "item/tool/call",
			"params": map[string]any{
				"threadId":  runnerTestThreadID,
				"turnId":    runnerTestTurnID,
				"callId":    "call-mcp-disconnect",
				"namespace": "executor",
				"tool":      "approved_echo",
				"arguments": map[string]any{"message": "wait"},
			},
		}); err != nil {
			serverDone <- err
			return
		}
		if _, err := receiveRunnerMessage(ctx, appServerPeer, codexwire.KindRequest, "turn/interrupt", "4"); err != nil {
			serverDone <- err
			return
		}
		if err := appServerPeer.Send(map[string]any{"id": 4, "result": map[string]any{}}); err != nil {
			serverDone <- err
			return
		}
		serverDone <- sendRunnerTerminal(appServerPeer, "interrupted")
	}()

	disconnectDone := make(chan error, 1)
	go func() {
		select {
		case <-callEntered:
			httpServer.server.CloseClientConnections()
			disconnectDone <- nil
		case <-time.After(2 * time.Second):
			disconnectDone <- errors.New("gateway tools/call did not start before disconnect deadline")
		}
	}()

	result, err := runner.Run(t.Context(), runnerStartRequest(catalog))
	if err == nil || !stringsContainAll(err.Error(), "dynamic tool call", "executor MCP tools/call") {
		t.Fatalf("real MCP disconnect runner error = %v", err)
	}
	if result.Terminal.Turn.Status != "interrupted" {
		t.Fatalf("real MCP disconnect terminal = %+v", result.Terminal)
	}
	if serverErr := <-serverDone; serverErr != nil {
		t.Fatal(serverErr)
	}
	if disconnectErr := <-disconnectDone; disconnectErr != nil {
		t.Fatal(disconnectErr)
	}
	closeStarted := time.Now()
	closeErr := client.Close()
	if closeErr == nil || !stringsContainAll(closeErr.Error(), "graceful close exceeded") {
		t.Fatalf("real MCP disconnect close error = %v", closeErr)
	}
	if elapsed := time.Since(closeStarted); elapsed >= time.Second {
		t.Fatalf("real MCP disconnect close took %s, want under one second", elapsed)
	}
	close(releaseCall)
	released = true
	select {
	case <-callExited:
	case <-time.After(time.Second):
		t.Fatal("disconnect fixture gateway call did not exit after explicit release")
	}
	if calls.Load() != 1 || bridge.Outstanding() != 0 {
		t.Fatalf("real MCP disconnect calls/outstanding = %d/%d", calls.Load(), bridge.Outstanding())
	}
}

type secretRejectingAppServerTransport struct {
	AppServerTransport
	secret []byte
}

func (t *secretRejectingAppServerTransport) Send(value any) error {
	frame, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if bytes.Contains(frame, t.secret) {
		return errors.New("worker MCP credential entered app-server output wire")
	}
	return t.AppServerTransport.Send(value)
}

func (t *secretRejectingAppServerTransport) Receive(ctx context.Context) (codexwire.Message, error) {
	message, err := t.AppServerTransport.Receive(ctx)
	if err != nil {
		return message, err
	}
	if bytes.Contains(message.Raw, t.secret) {
		return codexwire.Message{}, errors.New("worker MCP credential entered app-server input wire")
	}
	return message, nil
}

func dynamicCallFromMCPRequest(request *mcp.CallToolRequest) (DynamicCall, error) {
	if request == nil || request.Params == nil {
		return DynamicCall{}, errors.New("worker MCP tools/call has no params")
	}
	metadata, err := json.Marshal(request.Params.Meta)
	if err != nil {
		return DynamicCall{}, err
	}
	var call DynamicCall
	var envelope struct {
		RunID                string `json:"io.agentserver/runId"`
		ThreadID             string `json:"io.agentserver/threadId"`
		TurnID               string `json:"io.agentserver/turnId"`
		CallID               string `json:"io.agentserver/callId"`
		RunAttemptGeneration int64  `json:"io.agentserver/runAttemptGeneration"`
	}
	if err := json.Unmarshal(metadata, &envelope); err != nil {
		return DynamicCall{}, err
	}
	call.RunID = envelope.RunID
	call.ThreadID = envelope.ThreadID
	call.TurnID = envelope.TurnID
	call.CallID = envelope.CallID
	call.RunAttemptGeneration = envelope.RunAttemptGeneration
	call.Namespace = "executor"
	call.Tool = request.Params.Name
	call.Arguments = append(json.RawMessage(nil), request.Params.Arguments...)
	return call, nil
}

func stringsContainAll(value string, required ...string) bool {
	for _, fragment := range required {
		if !bytes.Contains([]byte(value), []byte(fragment)) {
			return false
		}
	}
	return true
}
