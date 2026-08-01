package executorgateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/codexwire"
	"github.com/agentserver/agentserver/v2/internal/execprofile"
	"github.com/agentserver/agentserver/v2/internal/executorgateway/agentxconn"
	"github.com/agentserver/agentserver/v2/internal/executorgateway/mcpcontract"
	"github.com/agentserver/agentserver/v2/internal/harnessworker"
)

func TestExecutorMCPShellClosesExpiredAgentxResumeAsUnknownWithoutRedispatch(t *testing.T) {
	fixture := newServerFixture(t, testGatewayInstanceID)
	connection, agentSession, welcome := fixture.connectAndInitialize(t, testConnectionID(96))

	registered := testRegisteredEnvironment(
		testEnvironmentID,
		`{"kind":"local","root":"/workspace","displayName":"primary","defaultCwd":"."}`,
	)
	registered.ConnectionGeneration = welcome.Generation
	registry := &recordingMCPEnvironmentRegistry{environments: []RegisteredEnvironment{registered}}
	resolver, err := NewEnvironmentResolver(registry)
	if err != nil {
		t.Fatal(err)
	}
	authority := newFakeShellAuthority()
	shell := newTestShellExecutor(t, authority, fixture.server)
	catalog := gatewayDisconnectTestCatalog(t)
	principal := testExecutorMCPPrincipal("capability-agentx-disconnect")
	principal.ToolCatalogDigest = catalog.Digest()
	mcpConfig := DefaultExecutorMCPConfig()
	mcpConfig.ShellExecutor = shell
	mcpConfig.IDGenerator = deterministicIDGenerator()
	handler, err := NewExecutorMCPHandler(
		testExecutorMCPAuthenticator{principals: map[string]ExecutorMCPPrincipal{testMCPBearerA: principal}},
		resolver,
		mcpConfig,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = handler.Shutdown(ctx)
	})
	mcpServer := httptest.NewServer(handler)
	t.Cleanup(mcpServer.Close)
	client, err := harnessworker.ConnectMCP(t.Context(), harnessworker.MCPClientConfig{
		Endpoint: mcpServer.URL + ExecutorMCPPath, BearerToken: testMCPBearerA,
		HTTPClient: mcpServer.Client(), AllowInsecureLoopback: true,
		Namespace: catalog.Namespace(), NamespaceDescription: catalog.NamespaceDescription(),
		ExpectedCatalogDigest: catalog.Digest(), ExpectedCatalog: catalog.CanonicalBytes(),
		Limits: harnessworker.DefaultLimits(), CloseGrace: time.Second,
		ElicitationHandler: func(context.Context, harnessworker.ElicitationRequest) (harnessworker.ElicitationDecision, error) {
			return harnessworker.ElicitationDecision{Action: harnessworker.ApprovalCancel}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	type callResult struct {
		result harnessworker.DynamicToolResult
		err    error
	}
	completed := make(chan callResult, 1)
	go func() {
		result, err := client.CallDynamicTool(t.Context(), harnessworker.DynamicCall{
			RunID: testMCPRunID, ThreadID: "thread-agentx-disconnect", TurnID: "turn-agentx-disconnect",
			CallID: "call-agentx-disconnect", RunAttemptGeneration: principal.Run.RunAttemptGeneration,
			Namespace: mcpcontract.Namespace, Tool: mcpcontract.ToolShell,
			Arguments: json.RawMessage(fmt.Sprintf(
				`{"environment_id":%q,"argv":["/bin/true"],"timeout_ms":10000}`,
				testEnvironmentID,
			)),
		})
		completed <- callResult{result: result, err: err}
	}()

	dispatched := readAgentxMessage(t, connection, fixture.config.WireLimits)
	if dispatched.Frame == nil || dispatched.Frame.Context == nil || dispatched.Frame.Context.EnvID != testEnvironmentID {
		t.Fatalf("agentx process dispatch = %+v", dispatched)
	}
	message, err := codexwire.Parse(dispatched.Frame.RPC)
	if err != nil || message.Method != execprofile.MethodProcessStart {
		t.Fatalf("agentx process dispatch RPC = %s, %v", dispatched.Frame.RPC, err)
	}
	if received, err := agentSession.Receive(*dispatched.Frame); err != nil || !received.Deliver {
		t.Fatalf("agentx receive process/start = %+v, %v", received, err)
	}
	if err := connection.CloseNow(); err != nil {
		t.Fatal(err)
	}
	waitForSessionState(t, fixture.server, testExecutorID, agentxconn.SessionDisconnected)
	select {
	case early := <-completed:
		t.Fatalf("shell completed before the agentx resume deadline: %+v", early)
	case <-time.After(20 * time.Millisecond):
	}
	if authority.executionStatus() != "dispatching" || authority.ackCallsFor(ShellV1OperationProcessStart) != 0 {
		t.Fatalf("pre-expiry core state = %q, start ACKs %d", authority.executionStatus(), authority.ackCallsFor(ShellV1OperationProcessStart))
	}

	accelerateAgentxResumeExpiry(t, fixture.server, testExecutorID)
	select {
	case completedCall := <-completed:
		if completedCall.err != nil {
			t.Fatal(completedCall.err)
		}
		if !completedCall.result.Success || len(completedCall.result.ContentItems) != 1 {
			t.Fatalf("unknown MCP shell result = %+v", completedCall.result)
		}
		var shellResult ShellV1Result
		if err := json.Unmarshal([]byte(completedCall.result.ContentItems[0].Text), &shellResult); err != nil {
			t.Fatal(err)
		}
		if shellResult.Status != "unknown" || shellResult.OutputComplete || shellResult.ExitCode != nil {
			t.Fatalf("agentx disconnect shell result = %+v", shellResult)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("MCP shell did not close after the agentx resume deadline")
	}
	statuses := authority.operationStatuses()
	if statuses != [2]string{"unknown", "skipped"} || authority.executionStatus() != "unknown" ||
		authority.ackCallsFor(ShellV1OperationProcessStart) != 0 || authority.skipCalls() != 1 {
		t.Fatalf(
			"agentx disconnect core state = operations %v execution %q ACKs %d skips %d",
			statuses, authority.executionStatus(), authority.ackCallsFor(ShellV1OperationProcessStart), authority.skipCalls(),
		)
	}
	waitForAuthorityStatus(t, fixture.authority, "fenced")
}

func gatewayDisconnectTestCatalog(t *testing.T) *harnessworker.Catalog {
	t.Helper()
	descriptors := make([]harnessworker.ToolDescriptor, 0, 2)
	for _, name := range []string{mcpcontract.ToolListEnvironments, mcpcontract.ToolShell} {
		tool, found := mcpcontract.Lookup(name)
		if !found {
			t.Fatalf("executor MCP contract tool %q is missing", name)
		}
		descriptors = append(descriptors, harnessworker.ToolDescriptor{
			Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema,
		})
	}
	catalog, err := harnessworker.BuildCatalog(
		mcpcontract.Namespace,
		mcpcontract.NamespaceDescription,
		descriptors,
		harnessworker.DefaultLimits(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func accelerateAgentxResumeExpiry(t *testing.T, server *Server, executorID string) {
	t.Helper()
	// Keep the protocol's exact 30-second resume window unchanged. This only
	// fires the production timer callback early after the test has observed the
	// real disconnected state and the callback has been scheduled.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		server.mu.Lock()
		runtime := server.byExecutor[executorID]
		server.mu.Unlock()
		if runtime != nil {
			runtime.mu.Lock()
			timer := runtime.resumeTimer
			runtime.mu.Unlock()
			if timer != nil {
				timer.Reset(time.Millisecond)
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("agentx disconnected runtime did not schedule resume expiry")
}
