package executorgateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/execprofile"
	"github.com/agentserver/agentserver/v2/internal/executorgateway/mcpcontract"
	"github.com/agentserver/agentserver/v2/internal/harnessworker"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	testMCPWorkspaceID = "40000000-0000-4000-8000-000000000004"
	testMCPSessionID   = "40500000-0000-4000-8000-000000000004"
	testMCPRunID       = "41000000-0000-4000-8000-000000000004"
	testMCPAttemptID   = "42000000-0000-4000-8000-000000000004"
	testMCPBearerA     = "executor-worker-test-bearer-a"
	testMCPBearerB     = "executor-worker-test-bearer-b"
)

func TestExecutorMCPListEnvironmentsUsesFrozenContractAndAuthenticatedScope(t *testing.T) {
	registry := &recordingMCPEnvironmentRegistry{environments: []RegisteredEnvironment{
		testRegisteredEnvironment(testEnvironmentID, `{"kind":"local","root":"/workspace","displayName":"primary","defaultCwd":"src"}`),
	}}
	handler := newTestExecutorMCPHandler(t, registry, map[string]ExecutorMCPPrincipal{
		testMCPBearerA: testExecutorMCPPrincipal("capability-a"),
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	contractTool, found := mcpcontract.Lookup(mcpcontract.ToolListEnvironments)
	if !found {
		t.Fatal("list_environments contract is missing")
	}
	catalog, err := harnessworker.BuildCatalog(
		mcpcontract.Namespace,
		mcpcontract.NamespaceDescription,
		[]harnessworker.ToolDescriptor{{
			Name:        contractTool.Name,
			Description: contractTool.Description,
			InputSchema: contractTool.InputSchema,
		}},
		harnessworker.DefaultLimits(),
	)
	if err != nil {
		t.Fatal(err)
	}
	client, err := harnessworker.ConnectMCP(t.Context(), harnessworker.MCPClientConfig{
		Endpoint:              server.URL + ExecutorMCPPath,
		BearerToken:           testMCPBearerA,
		HTTPClient:            server.Client(),
		AllowInsecureLoopback: true,
		Namespace:             catalog.Namespace(),
		NamespaceDescription:  catalog.NamespaceDescription(),
		ExpectedCatalogDigest: catalog.Digest(),
		ExpectedCatalog:       catalog.CanonicalBytes(),
		Limits:                harnessworker.DefaultLimits(),
		CloseGrace:            time.Second,
		ElicitationHandler: func(context.Context, harnessworker.ElicitationRequest) (harnessworker.ElicitationDecision, error) {
			return harnessworker.ElicitationDecision{Action: harnessworker.ApprovalCancel}, nil
		},
	})
	if err != nil {
		t.Fatalf("connect production-shape harness MCP client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	result, err := client.CallDynamicTool(t.Context(), harnessworker.DynamicCall{
		RunID:                "run-list-environments",
		ThreadID:             "thread-list-environments",
		TurnID:               "turn-list-environments",
		CallID:               "call-list-environments",
		RunAttemptGeneration: 1,
		Namespace:            mcpcontract.Namespace,
		Tool:                 mcpcontract.ToolListEnvironments,
		Arguments:            json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success || len(result.ContentItems) != 1 {
		t.Fatalf("list_environments result = %+v", result)
	}
	var projection ListEnvironmentsResult
	if err := json.Unmarshal([]byte(result.ContentItems[0].Text), &projection); err != nil {
		t.Fatalf("decode list_environments result %q: %v", result.ContentItems[0].Text, err)
	}
	if len(projection.Environments) != 1 || projection.Environments[0].EnvironmentID != testEnvironmentID || projection.Environments[0].DefaultCWD != "src" {
		t.Fatalf("list_environments projection = %+v", projection)
	}
	workspaceID, executorID, calls := registry.snapshot()
	if calls != 1 || workspaceID != testMCPWorkspaceID || executorID != testExecutorID {
		t.Fatalf("registry call = workspace %q executor %q calls %d", workspaceID, executorID, calls)
	}
}

func TestExecutorMCPShellUsesAuthenticatedCallContextAndTerminalOrchestrator(t *testing.T) {
	registered := testRegisteredEnvironment(testEnvironmentID, `{"kind":"local","root":"/workspace","displayName":"primary","defaultCwd":"."}`)
	registry := &recordingMCPEnvironmentRegistry{environments: []RegisteredEnvironment{registered}}
	resolver, err := NewEnvironmentResolver(registry)
	if err != nil {
		t.Fatal(err)
	}
	authority := newFakeShellAuthority()
	dispatcher := &fakeShellDispatcher{start: func(request ProcessDispatchRequest) (*ProcessExchange, error) {
		exchange := testShellStartExchange(request, 4)
		exchange.response <- shellStartResponse(request.RPC, testProcessID)
		exchange.events <- json.RawMessage(`{"method":"process/exited","params":{"processId":"80000000-0000-4000-8000-000000000008","seq":1,"exitCode":0,"sandboxDenied":false}}`)
		exchange.events <- json.RawMessage(`{"method":"process/closed","params":{"processId":"80000000-0000-4000-8000-000000000008","seq":2}}`)
		closeShellStartExchange(exchange)
		return exchange, nil
	}}
	shell := newTestShellExecutor(t, authority, dispatcher)

	contractTools := make([]mcpcontract.Tool, 0, 2)
	for _, name := range []string{mcpcontract.ToolListEnvironments, mcpcontract.ToolShell} {
		tool, found := mcpcontract.Lookup(name)
		if !found {
			t.Fatalf("contract tool %q is missing", name)
		}
		contractTools = append(contractTools, tool)
	}
	descriptors := make([]harnessworker.ToolDescriptor, len(contractTools))
	for index, tool := range contractTools {
		descriptors[index] = harnessworker.ToolDescriptor{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema}
	}
	catalog, err := harnessworker.BuildCatalog(
		mcpcontract.Namespace, mcpcontract.NamespaceDescription, descriptors, harnessworker.DefaultLimits(),
	)
	if err != nil {
		t.Fatal(err)
	}
	principal := testExecutorMCPPrincipal("capability-shell-mcp")
	principal.ToolCatalogDigest = catalog.Digest()
	config := DefaultExecutorMCPConfig()
	config.ShellExecutor = shell
	sequence := 0
	config.IDGenerator = func() (string, error) {
		sequence++
		return fmtMCPTestSessionID(sequence), nil
	}
	handler, err := NewExecutorMCPHandler(testExecutorMCPAuthenticator{principals: map[string]ExecutorMCPPrincipal{
		testMCPBearerA: principal,
	}}, resolver, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := harnessworker.ConnectMCP(t.Context(), harnessworker.MCPClientConfig{
		Endpoint: server.URL + ExecutorMCPPath, BearerToken: testMCPBearerA, HTTPClient: server.Client(), AllowInsecureLoopback: true,
		Namespace: catalog.Namespace(), NamespaceDescription: catalog.NamespaceDescription(),
		ExpectedCatalogDigest: catalog.Digest(), ExpectedCatalog: catalog.CanonicalBytes(), Limits: harnessworker.DefaultLimits(),
		CloseGrace: time.Second,
		ElicitationHandler: func(context.Context, harnessworker.ElicitationRequest) (harnessworker.ElicitationDecision, error) {
			return harnessworker.ElicitationDecision{Action: harnessworker.ApprovalCancel}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	result, err := client.CallDynamicTool(t.Context(), harnessworker.DynamicCall{
		RunID: testMCPRunID, ThreadID: "thread-shell", TurnID: "turn-shell", CallID: "call-shell-mcp",
		RunAttemptGeneration: 3, Namespace: mcpcontract.Namespace, Tool: mcpcontract.ToolShell,
		Arguments: json.RawMessage(fmt.Sprintf(`{"environment_id":"%s","argv":["/bin/true"],"timeout_ms":10000}`, testEnvironmentID)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success || len(result.ContentItems) != 1 {
		t.Fatalf("MCP shell result = %+v", result)
	}
	var shellResult ShellV1Result
	if err := json.Unmarshal([]byte(result.ContentItems[0].Text), &shellResult); err != nil {
		t.Fatal(err)
	}
	if shellResult.Status != "succeeded" || !shellResult.OutputComplete || dispatcher.count() != 1 || authority.executionStatus() != "succeeded" {
		t.Fatalf("terminal MCP shell result=%+v dispatches=%d core=%q", shellResult, dispatcher.count(), authority.executionStatus())
	}
}

func TestExecutorMCPReadFileUsesFullCatalogAndBoundedExecutor(t *testing.T) {
	registered := testRegisteredEnvironment(testEnvironmentID, `{"kind":"local","root":"/workspace","displayName":"primary","defaultCwd":"."}`)
	registered.OuterProfileVersion = execprofile.FilesystemReadVersion
	registry := &recordingMCPEnvironmentRegistry{environments: []RegisteredEnvironment{registered}}
	resolver, err := NewEnvironmentResolver(registry)
	if err != nil {
		t.Fatal(err)
	}
	authority := newFakeReadFileAuthority()
	maximumBlock := bytes.Repeat([]byte{0}, int(execprofile.MaxFilesystemReadLength))
	maximumChunk := base64.StdEncoding.EncodeToString(maximumBlock)
	dispatcher := &fakeFilesystemDispatcher{respond: responseForFilesystemRequest(func(id json.RawMessage) string {
		return fmt.Sprintf(`{"id":%s,"result":{"chunk":%q,"eof":true}}`, id, maximumChunk)
	})}
	identities, err := NewReadFileV1IdentityAllocator(deterministicIDGenerator())
	if err != nil {
		t.Fatal(err)
	}
	transitions, err := NewExecutionTransitionAllocator("91000000-0000-4000-8000-000000000009", deterministicIDGenerator())
	if err != nil {
		t.Fatal(err)
	}
	readFileConfig := DefaultReadFileExecutorConfig(t.Context())
	configureTestReadFilePolicy(t, &readFileConfig)
	readFile, err := NewReadFileExecutor(
		resolver, authority, dispatcher, identities, transitions,
		readFileConfig,
	)
	if err != nil {
		t.Fatal(err)
	}

	contractTools := mcpcontract.Tools()
	descriptors := make([]harnessworker.ToolDescriptor, len(contractTools))
	for index, tool := range contractTools {
		descriptors[index] = harnessworker.ToolDescriptor{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema}
	}
	catalog, err := harnessworker.BuildCatalog(
		mcpcontract.Namespace, mcpcontract.NamespaceDescription, descriptors, harnessworker.DefaultLimits(),
	)
	if err != nil {
		t.Fatal(err)
	}
	principal := testExecutorMCPPrincipal("capability-read-file-mcp")
	principal.ToolCatalogDigest = catalog.Digest()
	config := DefaultExecutorMCPConfig()
	config.ShellExecutor = &ShellExecutor{}
	config.ReadFileExecutor = readFile
	sequence := 0
	config.IDGenerator = func() (string, error) {
		sequence++
		return fmtMCPTestSessionID(sequence), nil
	}
	handler, err := NewExecutorMCPHandler(testExecutorMCPAuthenticator{principals: map[string]ExecutorMCPPrincipal{
		testMCPBearerA: principal,
	}}, resolver, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := harnessworker.ConnectMCP(t.Context(), harnessworker.MCPClientConfig{
		Endpoint: server.URL + ExecutorMCPPath, BearerToken: testMCPBearerA, HTTPClient: server.Client(), AllowInsecureLoopback: true,
		Namespace: catalog.Namespace(), NamespaceDescription: catalog.NamespaceDescription(),
		ExpectedCatalogDigest: catalog.Digest(), ExpectedCatalog: catalog.CanonicalBytes(), Limits: harnessworker.DefaultLimits(),
		CloseGrace: time.Second,
		ElicitationHandler: func(context.Context, harnessworker.ElicitationRequest) (harnessworker.ElicitationDecision, error) {
			return harnessworker.ElicitationDecision{Action: harnessworker.ApprovalCancel}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	result, err := client.CallDynamicTool(t.Context(), harnessworker.DynamicCall{
		RunID: testMCPRunID, ThreadID: "thread-read-file", TurnID: "turn-read-file", CallID: "call-read-file-mcp",
		RunAttemptGeneration: 3, Namespace: mcpcontract.Namespace, Tool: mcpcontract.ToolReadFile,
		Arguments: json.RawMessage(fmt.Sprintf(`{"environment_id":"%s","path":"data.bin","limit":1048576}`, testEnvironmentID)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success || len(result.ContentItems) != 1 {
		t.Fatalf("MCP read_file result = %+v", result)
	}
	var readResult ReadFileV1Result
	if err := json.Unmarshal([]byte(result.ContentItems[0].Text), &readResult); err != nil {
		t.Fatal(err)
	}
	if readResult.Status != "succeeded" || readResult.Encoding != "base64" || len(readResult.Content) != 1_398_104 ||
		readResult.BytesRead != execprofile.MaxFilesystemReadLength || authority.execution.Status != "succeeded" {
		t.Fatalf("terminal MCP read_file result=%+v core=%q", readResult, authority.execution.Status)
	}
}

func TestParseExecutorMCPCallContextRejectsCapabilityMismatch(t *testing.T) {
	principal := testExecutorMCPPrincipal("capability-meta")
	valid := mcp.Meta{
		executorMCPMetaRunID: principal.Run.RunID, executorMCPMetaThreadID: "thread", executorMCPMetaTurnID: "turn",
		executorMCPMetaCallID: "call", executorMCPMetaRunAttemptGeneration: float64(principal.Run.RunAttemptGeneration),
		executorMCPMetaToolCatalogDigest: principal.ToolCatalogDigest, executorMCPMetaProgressToken: "call",
	}
	if _, err := parseExecutorMCPCallContext(valid, principal); err != nil {
		t.Fatal(err)
	}
	mutations := []func(mcp.Meta){
		func(meta mcp.Meta) { meta[executorMCPMetaRunID] = "41000000-0000-4000-8000-000000000099" },
		func(meta mcp.Meta) { meta[executorMCPMetaRunAttemptGeneration] = float64(4) },
		func(meta mcp.Meta) { meta[executorMCPMetaToolCatalogDigest] = strings.Repeat("b", 64) },
		func(meta mcp.Meta) { meta[executorMCPMetaProgressToken] = "other" },
		func(meta mcp.Meta) { meta["future"] = true },
	}
	for index, mutate := range mutations {
		copyMeta := make(mcp.Meta, len(valid))
		for key, value := range valid {
			copyMeta[key] = value
		}
		mutate(copyMeta)
		if _, err := parseExecutorMCPCallContext(copyMeta, principal); err == nil {
			t.Errorf("invalid metadata mutation %d was accepted", index)
		}
	}
}

func TestExecutorMCPRejectsInvalidArgumentsBeforeRegistry(t *testing.T) {
	registry := &recordingMCPEnvironmentRegistry{}
	handler := newTestExecutorMCPHandler(t, registry, map[string]ExecutorMCPPrincipal{
		testMCPBearerA: testExecutorMCPPrincipal("capability-a"),
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	session := connectRawMCPClient(t, server, testMCPBearerA)
	t.Cleanup(func() { _ = session.Close() })

	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      mcpcontract.ToolListEnvironments,
		Arguments: json.RawMessage(`{"future":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || !result.IsError || len(result.Content) != 1 {
		t.Fatalf("invalid argument result = %+v", result)
	}
	if _, _, calls := registry.snapshot(); calls != 0 {
		t.Fatalf("invalid arguments reached registry %d time(s)", calls)
	}

	result, err = session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      mcpcontract.ToolListEnvironments,
		Arguments: json.RawMessage(`{"executor_id":"20000000-0000-4000-8000-000000000099"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || !result.IsError || !strings.Contains(result.Content[0].(*mcp.TextContent).Text, "outside the authenticated run capability") {
		t.Fatalf("out-of-scope executor result = %+v", result)
	}
	if _, _, calls := registry.snapshot(); calls != 0 {
		t.Fatalf("out-of-scope executor reached registry %d time(s)", calls)
	}

	registry.setError(errors.New("dial https://core.internal:8443: secret diagnostic"))
	result, err = session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      mcpcontract.ToolListEnvironments,
		Arguments: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].(*mcp.TextContent).Text
	if !result.IsError || !strings.Contains(text, "temporarily unavailable") || strings.Contains(text, "core.internal") || strings.Contains(text, "secret diagnostic") {
		t.Fatalf("registry failure projection = %+v", result)
	}
}

func TestExecutorMCPSessionCannotBeReusedByAnotherCapability(t *testing.T) {
	registry := &recordingMCPEnvironmentRegistry{}
	handler := newTestExecutorMCPHandler(t, registry, map[string]ExecutorMCPPrincipal{
		testMCPBearerA: testExecutorMCPPrincipal("capability-a"),
		testMCPBearerB: testExecutorMCPPrincipal("capability-b"),
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	session := connectRawMCPClient(t, server, testMCPBearerA)
	t.Cleanup(func() { _ = session.Close() })
	if session.ID() == "" {
		t.Fatal("stateful MCP session has no ID")
	}

	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+ExecutorMCPPath, strings.NewReader(`{"jsonrpc":"2.0","id":7,"method":"tools/list","params":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+testMCPBearerB)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set(mcpSessionIDHeader, session.ID())
	request.Header.Set("MCP-Protocol-Version", ExecutorMCPProtocolVersion)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("cross-capability session status = %d, body %q", response.StatusCode, body)
	}

	listed, err := session.ListTools(t.Context(), nil)
	if err != nil || len(listed.Tools) != 1 || listed.Tools[0].Name != mcpcontract.ToolListEnvironments || listed.CacheScope != "private" {
		t.Fatalf("original session tools/list = %+v, %v", listed, err)
	}
}

func TestExecutorMCPSessionAcceptsReconstructedManagedSandboxPrincipal(t *testing.T) {
	principal := testExecutorMCPPrincipal("capability-managed-sandbox")
	principal.ManagedSandbox = &ExecutorManagedSandboxAuthority{
		SettingVersion: 2,
		Region:         "boe",
		EnvironmentID:  "43000000-0000-4000-8000-000000000004",
	}
	registry := &recordingMCPEnvironmentRegistry{}
	resolver, err := NewEnvironmentResolver(registry)
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultExecutorMCPConfig()
	sequence := 0
	config.IDGenerator = func() (string, error) {
		sequence++
		return fmtMCPTestSessionID(sequence), nil
	}
	handler, err := NewExecutorMCPHandler(reconstructingTestExecutorMCPAuthenticator{
		bearer:    testMCPBearerA,
		principal: principal,
	}, resolver, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	session := connectRawMCPClient(t, server, testMCPBearerA)
	t.Cleanup(func() { _ = session.Close() })
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil || len(listed.Tools) != 1 || listed.Tools[0].Name != mcpcontract.ToolListEnvironments {
		t.Fatalf("reconstructed managed principal tools/list = %+v, %v", listed, err)
	}
}

func TestEqualExecutorMCPPrincipalsRejectsManagedSandboxAuthorityDrift(t *testing.T) {
	left := testExecutorMCPPrincipal("capability-managed-sandbox-equality")
	left.ManagedSandbox = &ExecutorManagedSandboxAuthority{
		SettingVersion: 2,
		Region:         "boe",
		EnvironmentID:  "43000000-0000-4000-8000-000000000004",
	}
	right := left
	equivalent := *left.ManagedSandbox
	right.ManagedSandbox = &equivalent
	if !equalExecutorMCPPrincipals(left, right) {
		t.Fatal("equal managed sandbox authority with a distinct allocation was rejected")
	}
	drifted := equivalent
	drifted.Region = "cn"
	right.ManagedSandbox = &drifted
	if equalExecutorMCPPrincipals(left, right) {
		t.Fatal("managed sandbox authority drift was accepted")
	}
	right.ManagedSandbox = nil
	if equalExecutorMCPPrincipals(left, right) {
		t.Fatal("missing managed sandbox authority was accepted")
	}
}

func TestExecutorMCPRejectsOtherProtocolProfilesAndBrowserOrigins(t *testing.T) {
	handler := newTestExecutorMCPHandler(t, &recordingMCPEnvironmentRegistry{}, map[string]ExecutorMCPPrincipal{
		testMCPBearerA: testExecutorMCPPrincipal("capability-a"),
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	initialize := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"old-client","version":"test"}}}`)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+ExecutorMCPPath, bytes.NewReader(initialize))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+testMCPBearerA)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte("requires protocol "+ExecutorMCPProtocolVersion)) {
		t.Fatalf("old protocol response status/body = %d/%q", response.StatusCode, body)
	}
	if !strings.Contains(response.Header.Get("Cache-Control"), "no-store") || !strings.Contains(response.Header.Get("Cache-Control"), "no-transform") {
		t.Fatalf("MCP cache control = %q", response.Header.Get("Cache-Control"))
	}

	request, err = http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+ExecutorMCPPath, bytes.NewReader(initialize))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+testMCPBearerA)
	request.Header.Set("Origin", "https://attacker.example")
	response, err = server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("browser-origin request status = %d", response.StatusCode)
	}
}

func TestExecutorMCPShutdownRejectsNewSessions(t *testing.T) {
	handler := newTestExecutorMCPHandler(t, &recordingMCPEnvironmentRegistry{}, map[string]ExecutorMCPPrincipal{
		testMCPBearerA: testExecutorMCPPrincipal("capability-a"),
	})
	if err := handler.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "https://gateway.test"+ExecutorMCPPath, strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer "+testMCPBearerA)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("request after shutdown status = %d", response.Code)
	}
}

func TestExecutorMCPRejectsNonCanonicalRequestTargetsBeforeAuthentication(t *testing.T) {
	handler := newTestExecutorMCPHandler(t, &recordingMCPEnvironmentRegistry{}, map[string]ExecutorMCPPrincipal{
		testMCPBearerA: testExecutorMCPPrincipal("capability-a"),
	})
	requests := map[string]*http.Request{
		"query":       httptest.NewRequest(http.MethodPost, "https://gateway.test"+ExecutorMCPPath+"?other=1", strings.NewReader(`{}`)),
		"force query": httptest.NewRequest(http.MethodPost, "https://gateway.test"+ExecutorMCPPath+"?", strings.NewReader(`{}`)),
		"raw path": func() *http.Request {
			request := httptest.NewRequest(http.MethodPost, "https://gateway.test"+ExecutorMCPPath, strings.NewReader(`{}`))
			request.URL.RawPath = "/%6dcp"
			return request
		}(),
	}
	for name, request := range requests {
		t.Run(name, func(t *testing.T) {
			request.Header.Set("Authorization", "Bearer "+testMCPBearerA)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusNotFound || response.Header().Get("WWW-Authenticate") != "" {
				t.Fatalf("non-canonical MCP target = %d headers %v", response.Code, response.Header())
			}
		})
	}
}

func newTestExecutorMCPHandler(t *testing.T, registry EnvironmentRegistry, principals map[string]ExecutorMCPPrincipal) *ExecutorMCPHandler {
	t.Helper()
	resolver, err := NewEnvironmentResolver(registry)
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultExecutorMCPConfig()
	sequence := 0
	config.IDGenerator = func() (string, error) {
		sequence++
		return fmtMCPTestSessionID(sequence), nil
	}
	handler, err := NewExecutorMCPHandler(testExecutorMCPAuthenticator{principals: principals}, resolver, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = handler.Shutdown(ctx)
	})
	return handler
}

func testExecutorMCPPrincipal(capabilityID string) ExecutorMCPPrincipal {
	return ExecutorMCPPrincipal{
		CapabilityID:        capabilityID,
		WorkspaceID:         testMCPWorkspaceID,
		SessionID:           testMCPSessionID,
		ActorID:             "44000000-0000-4000-8000-000000000004",
		ExecutorID:          testExecutorID,
		ToolCatalogDigest:   strings.Repeat("a", 64),
		MaxApprovalTTL:      time.Minute,
		RunDeadline:         time.Now().Add(time.Hour),
		CapabilityExpiresAt: time.Now().Add(2 * time.Hour),
		Run: ExecutorMCPRunContext{
			RunID:                     testMCPRunID,
			RunAttemptID:              testMCPAttemptID,
			RunAttemptGeneration:      3,
			HolderID:                  "test-mcp-holder",
			ExpectedRunVersion:        4,
			ExpectedRunAttemptVersion: 5,
		},
	}
}

func fmtMCPTestSessionID(sequence int) string {
	return fmt.Sprintf("mcp-test-session-%04d", sequence)
}

type testExecutorMCPAuthenticator struct {
	principals map[string]ExecutorMCPPrincipal
}

func (authenticator testExecutorMCPAuthenticator) AuthenticateExecutorMCP(request *http.Request) (ExecutorMCPPrincipal, error) {
	const prefix = "Bearer "
	values := request.Header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], prefix) {
		return ExecutorMCPPrincipal{}, errors.New("missing bearer")
	}
	principal, found := authenticator.principals[strings.TrimPrefix(values[0], prefix)]
	if !found {
		return ExecutorMCPPrincipal{}, errors.New("unknown bearer")
	}
	return principal, nil
}

type reconstructingTestExecutorMCPAuthenticator struct {
	bearer    string
	principal ExecutorMCPPrincipal
}

func (authenticator reconstructingTestExecutorMCPAuthenticator) AuthenticateExecutorMCP(request *http.Request) (ExecutorMCPPrincipal, error) {
	if request.Header.Get("Authorization") != "Bearer "+authenticator.bearer {
		return ExecutorMCPPrincipal{}, errors.New("unknown bearer")
	}
	principal := authenticator.principal
	if principal.ManagedSandbox != nil {
		managedSandbox := *principal.ManagedSandbox
		principal.ManagedSandbox = &managedSandbox
	}
	return principal, nil
}

type recordingMCPEnvironmentRegistry struct {
	mu           sync.Mutex
	environments []RegisteredEnvironment
	workspaceID  string
	executorID   string
	calls        int
	err          error
}

func (registry *recordingMCPEnvironmentRegistry) ListEnvironments(_ context.Context, workspaceID, executorID string) ([]RegisteredEnvironment, error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.workspaceID = workspaceID
	registry.executorID = executorID
	registry.calls++
	if registry.err != nil {
		return nil, registry.err
	}
	result := make([]RegisteredEnvironment, len(registry.environments))
	copy(result, registry.environments)
	for index := range result {
		result[index].RootDescriptor = append(json.RawMessage(nil), result[index].RootDescriptor...)
	}
	return result, nil
}

func (registry *recordingMCPEnvironmentRegistry) setError(err error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.err = err
}

func (registry *recordingMCPEnvironmentRegistry) snapshot() (string, string, int) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return registry.workspaceID, registry.executorID, registry.calls
}

func connectRawMCPClient(t *testing.T, server *httptest.Server, bearer string) *mcp.ClientSession {
	t.Helper()
	httpClient := *server.Client()
	httpClient.Transport = bearerRoundTripper{base: httpClient.Transport, bearer: bearer}
	client := mcp.NewClient(
		&mcp.Implementation{Name: "executor-gateway-test-client", Version: "test"},
		&mcp.ClientOptions{},
	)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:             server.URL + ExecutorMCPPath,
		HTTPClient:           &httpClient,
		MaxRetries:           -1,
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect raw MCP client: %v", err)
	}
	if result := session.InitializeResult(); result == nil || result.ProtocolVersion != ExecutorMCPProtocolVersion {
		t.Fatalf("negotiated MCP protocol = %+v", result)
	}
	return session
}

type bearerRoundTripper struct {
	base   http.RoundTripper
	bearer string
}

func (transport bearerRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.Header = request.Header.Clone()
	request.Header.Set("Authorization", "Bearer "+transport.bearer)
	return transport.base.RoundTrip(request)
}
