package harnessworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const testMCPBearer = "worker-only-executor-bearer"

type testMCPServer struct {
	server       *httptest.Server
	requestCount atomic.Int64
	badAuthCount atomic.Int64
}

func startTestMCPServer(t *testing.T, server *mcp.Server) *testMCPServer {
	return startTestMCPServerWithOptions(t, server, nil)
}

func startTestMCPServerWithOptions(t *testing.T, server *mcp.Server, options *mcp.StreamableHTTPOptions) *testMCPServer {
	t.Helper()
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, options)
	result := &testMCPServer{}
	result.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		result.requestCount.Add(1)
		if request.URL.Path != "/mcp" || request.URL.RawQuery != "" {
			http.NotFound(writer, request)
			return
		}
		if request.Header.Get("Authorization") != "Bearer "+testMCPBearer {
			result.badAuthCount.Add(1)
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		mcpHandler.ServeHTTP(writer, request)
	}))
	t.Cleanup(result.server.Close)
	return result
}

func (s *testMCPServer) endpoint() string { return s.server.URL + "/mcp" }

func TestMCPClientVerifiesPagedCatalogAndCallsTool(t *testing.T) {
	limits := DefaultLimits()
	descriptors := []ToolDescriptor{
		{
			Name:        "approved_echo",
			Description: "Echo one approved message.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"}},"required":["message"],"additionalProperties":false}`),
		},
		{
			Name:        "read_file",
			Description: "Read one bounded file.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
		},
	}
	expected := mustCatalog(t, descriptors, limits)
	server := mcp.NewServer(
		&mcp.Implementation{Name: "fake-executor-gateway", Version: "v2-test"},
		&mcp.ServerOptions{PageSize: 1, Capabilities: &mcp.ServerCapabilities{}},
	)
	var calls atomic.Int64
	server.AddTool(&mcp.Tool{
		Name:        descriptors[0].Name,
		Description: descriptors[0].Description,
		InputSchema: descriptors[0].InputSchema,
	}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		if request.Params.Meta[MCPMetaRunID] != "run-1" ||
			request.Params.Meta[MCPMetaThreadID] != "thread-1" ||
			request.Params.Meta[MCPMetaTurnID] != "turn-1" ||
			request.Params.Meta[MCPMetaCallID] != "call-1" ||
			request.Params.Meta[MCPMetaToolCatalogDigest] != expected.Digest() {
			return nil, fmt.Errorf("unexpected worker metadata: %+v", request.Params.Meta)
		}
		var arguments map[string]any
		if err := json.Unmarshal(request.Params.Arguments, &arguments); err != nil || arguments["message"] != "hello" {
			return nil, fmt.Errorf("unexpected tool arguments: %s (%v)", request.Params.Arguments, err)
		}
		return &mcp.CallToolResult{
			Content:           []mcp.Content{&mcp.TextContent{Text: "echo: hello"}},
			StructuredContent: map[string]any{"echoed": "hello", "ok": true},
		}, nil
	})
	server.AddTool(&mcp.Tool{
		Name:        descriptors[1].Name,
		Description: descriptors[1].Description,
		InputSchema: descriptors[1].InputSchema,
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "unused"}}}, nil
	})
	httpServer := startTestMCPServer(t, server)

	client, err := ConnectMCP(t.Context(), testMCPClientConfig(httpServer.endpoint(), expected, limits, func(context.Context, ElicitationRequest) (ElicitationDecision, error) {
		return ElicitationDecision{Action: ApprovalCancel}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if client.Catalog().Digest() != expected.Digest() || len(client.Catalog().Tools()) != 2 {
		t.Fatalf("verified catalog = %s/%d, want %s/2", client.Catalog().Digest(), len(client.Catalog().Tools()), expected.Digest())
	}
	result, err := client.CallDynamicTool(t.Context(), DynamicCall{
		RunID:                "run-1",
		ThreadID:             "thread-1",
		TurnID:               "turn-1",
		CallID:               "call-1",
		RunAttemptGeneration: 7,
		Namespace:            "executor",
		Tool:                 "approved_echo",
		Arguments:            json.RawMessage(`{"message":"hello"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := DynamicToolResult{
		ContentItems: []InputTextContent{
			{Type: "inputText", Text: "echo: hello"},
			{Type: "inputText", Text: `{"echoed":"hello","ok":true}`},
		},
		Success: true,
	}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("dynamic result = %+v, want %+v", result, want)
	}
	if calls.Load() != 1 {
		t.Fatalf("tools/call count = %d, want 1", calls.Load())
	}
	if httpServer.requestCount.Load() < 3 || httpServer.badAuthCount.Load() != 0 {
		t.Fatalf("MCP HTTP requests/bad auth = %d/%d", httpServer.requestCount.Load(), httpServer.badAuthCount.Load())
	}
}

func TestMCPClientA06RoutesCanonicalElicitationOutcomes(t *testing.T) {
	for _, action := range []ApprovalAction{ApprovalAccept, ApprovalDecline, ApprovalCancel} {
		t.Run(string(action), func(t *testing.T) {
			limits := DefaultLimits()
			descriptor := ToolDescriptor{
				Name:        "approved_echo",
				Description: "Echo only after approval.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"}},"required":["message"],"additionalProperties":false}`),
			}
			expected := mustCatalog(t, []ToolDescriptor{descriptor}, limits)
			server := mcp.NewServer(
				&mcp.Implementation{Name: "fake-executor-gateway", Version: "v2-test"},
				&mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{}},
			)
			var dispatches atomic.Int64
			decisionSeen := make(chan string, 1)
			server.AddTool(&mcp.Tool{
				Name: descriptor.Name, Description: descriptor.Description, InputSchema: descriptor.InputSchema,
			}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				decision, err := request.Session.Elicit(ctx, &mcp.ElicitParams{
					Meta: mcp.Meta{
						MCPMetaRunID:                "run-a06",
						MCPMetaCallID:               "call-a06-" + string(action),
						MCPMetaRunAttemptGeneration: int64(9),
						MCPMetaToolCatalogDigest:    expected.Digest(),
						MCPMetaExecutionID:          "execution-a06-" + string(action),
						MCPMetaApprovalID:           "approval-a06-" + string(action),
						MCPMetaApprovalNonce:        "nonce-a06-" + string(action),
						MCPMetaContextHash:          strings.Repeat("a", 64),
						MCPMetaExpiresAt:            time.Now().Add(10 * time.Second).UTC().Format(time.RFC3339Nano),
					},
					Mode:    "form",
					Message: "Allow the deterministic echo?",
					RequestedSchema: json.RawMessage(`{
  "type":"object",
  "properties":{"confirmed":{"type":"boolean"}},
  "required":["confirmed"],
  "additionalProperties":false
}`),
				})
				if err != nil {
					return nil, err
				}
				decisionSeen <- decision.Action
				if decision.Action != string(ApprovalAccept) {
					return &mcp.CallToolResult{
						Content: []mcp.Content{&mcp.TextContent{Text: "not dispatched: " + decision.Action}},
						IsError: true,
					}, nil
				}
				dispatches.Add(1)
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "dispatched"}}}, nil
			})
			httpServer := startTestMCPServer(t, server)
			var routed ElicitationRequest
			client, err := ConnectMCP(t.Context(), testMCPClientConfig(httpServer.endpoint(), expected, limits, func(_ context.Context, request ElicitationRequest) (ElicitationDecision, error) {
				routed = request
				decision := ElicitationDecision{Action: action}
				if action == ApprovalAccept {
					decision.Content = map[string]any{"confirmed": true}
				}
				return decision, nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = client.Close() })
			result, err := client.CallDynamicTool(t.Context(), DynamicCall{
				RunID:                "run-a06",
				ThreadID:             "thread-a06",
				TurnID:               "turn-a06",
				CallID:               "call-a06-" + string(action),
				RunAttemptGeneration: 9,
				Namespace:            "executor",
				Tool:                 descriptor.Name,
				Arguments:            json.RawMessage(`{"message":"hello"}`),
			})
			if err != nil {
				t.Fatal(err)
			}
			if got := <-decisionSeen; got != string(action) {
				t.Fatalf("gateway saw action %q, want %q", got, action)
			}
			if routed.RunID != "run-a06" || routed.CallID != "call-a06-"+string(action) ||
				routed.RunAttemptGeneration != 9 || routed.ToolCatalogDigest != expected.Digest() ||
				routed.ExecutionID != "execution-a06-"+string(action) || routed.ApprovalID != "approval-a06-"+string(action) ||
				routed.Nonce != "nonce-a06-"+string(action) || routed.ContextHash != strings.Repeat("a", 64) ||
				!json.Valid(routed.RequestedSchema) {
				t.Fatalf("routed elicitation = %+v", routed)
			}
			wantDispatches := int64(0)
			wantSuccess := false
			if action == ApprovalAccept {
				wantDispatches = 1
				wantSuccess = true
			}
			if dispatches.Load() != wantDispatches || result.Success != wantSuccess {
				t.Fatalf("dispatches/success = %d/%v, want %d/%v", dispatches.Load(), result.Success, wantDispatches, wantSuccess)
			}
		})
	}
}

func TestMCPClientA07CancellationReachesServerBeforeDispatch(t *testing.T) {
	limits := DefaultLimits()
	descriptor := ToolDescriptor{
		Name:        "approved_echo",
		Description: "Wait for approval.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"}},"required":["message"],"additionalProperties":false}`),
	}
	expected := mustCatalog(t, []ToolDescriptor{descriptor}, limits)
	server := mcp.NewServer(
		&mcp.Implementation{Name: "fake-executor-gateway", Version: "v2-test"},
		&mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{}},
	)
	serverCallStarted := make(chan struct{})
	gatewayCallCancelled := make(chan time.Time, 1)
	approvalEntered := make(chan struct{})
	approvalExited := make(chan struct{})
	approvalExpiry := make(chan time.Time, 1)
	var dispatches atomic.Int64
	server.AddTool(&mcp.Tool{
		Name: descriptor.Name, Description: descriptor.Description, InputSchema: descriptor.InputSchema,
	}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		close(serverCallStarted)
		go func() {
			<-ctx.Done()
			gatewayCallCancelled <- time.Now()
		}()
		expiresAt := time.Now().Add(2 * time.Second)
		approvalExpiry <- expiresAt
		decision, err := request.Session.Elicit(ctx, &mcp.ElicitParams{
			Meta: mcp.Meta{
				MCPMetaRunID:                "run-a07",
				MCPMetaCallID:               "call-a07",
				MCPMetaRunAttemptGeneration: int64(11),
				MCPMetaToolCatalogDigest:    expected.Digest(),
				MCPMetaExecutionID:          "execution-a07",
				MCPMetaApprovalID:           "approval-a07",
				MCPMetaApprovalNonce:        "nonce-a07",
				MCPMetaContextHash:          strings.Repeat("b", 64),
				MCPMetaExpiresAt:            expiresAt.UTC().Format(time.RFC3339Nano),
			},
			Mode:            "form",
			Message:         "Wait for an explicit decision.",
			RequestedSchema: json.RawMessage(`{"type":"object","properties":{"confirmed":{"type":"boolean"}},"required":["confirmed"]}`),
		})
		if err != nil {
			return nil, err
		}
		if decision.Action != string(ApprovalAccept) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "not dispatched: " + decision.Action}},
				IsError: true,
			}, nil
		}
		dispatches.Add(1)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "unexpected dispatch"}}}, nil
	})
	httpServer := startTestMCPServer(t, server)
	client, err := ConnectMCP(t.Context(), testMCPClientConfig(httpServer.endpoint(), expected, limits, func(ctx context.Context, _ ElicitationRequest) (ElicitationDecision, error) {
		close(approvalEntered)
		defer close(approvalExited)
		<-ctx.Done()
		return ElicitationDecision{}, ctx.Err()
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	callContext, cancel := context.WithCancel(t.Context())
	callDone := make(chan error, 1)
	go func() {
		_, err := client.CallDynamicTool(callContext, DynamicCall{
			RunID:                "run-a07",
			ThreadID:             "thread-a07",
			TurnID:               "turn-a07",
			CallID:               "call-a07",
			RunAttemptGeneration: 11,
			Namespace:            "executor",
			Tool:                 descriptor.Name,
			Arguments:            json.RawMessage(`{"message":"hello"}`),
		})
		callDone <- err
	}()
	waitSignal(t, serverCallStarted, "server tools/call")
	waitSignal(t, approvalEntered, "worker elicitation handler")
	expiresAt := <-approvalExpiry
	cancelStarted := time.Now()
	cancel()
	select {
	case err := <-callDone:
		if err == nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled CallDynamicTool() error = %v, want context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled CallDynamicTool did not return")
	}
	select {
	case cancelledAt := <-gatewayCallCancelled:
		if !cancelledAt.Before(expiresAt) {
			t.Fatalf("gateway observed cancellation at %s, not before approval expiry %s", cancelledAt, expiresAt)
		}
		if elapsed := cancelledAt.Sub(cancelStarted); elapsed >= time.Second {
			t.Fatalf("gateway observed cancellation after %s, want under 1s", elapsed)
		}
	case <-time.After(time.Second):
		t.Fatal("gateway tools/call context did not observe cancellation before approval expiry")
	}
	select {
	case <-approvalExited:
		if !time.Now().Before(expiresAt) {
			t.Fatalf("worker elicitation handler exited at or after approval expiry %s", expiresAt)
		}
	case <-time.After(time.Second):
		t.Fatal("worker elicitation handler did not exit promptly after outer call cancellation")
	}
	if dispatches.Load() != 0 {
		t.Fatalf("dispatch count after cancellation = %d, want zero", dispatches.Load())
	}
	closeStarted := time.Now()
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(closeStarted); elapsed >= time.Second {
		t.Fatalf("MCP client close took %s after cancellation, want under 1s", elapsed)
	}
}

func TestMCPClientRejectsProtocolWithoutStatefulElicitation(t *testing.T) {
	limits := DefaultLimits()
	expected := mustCatalog(t, nil, limits)
	server := mcp.NewServer(
		&mcp.Implementation{Name: "stateless-fake-executor-gateway", Version: "v2-test"},
		&mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{}},
	)
	httpServer := startTestMCPServerWithOptions(t, server, &mcp.StreamableHTTPOptions{Stateless: true})
	_, err := ConnectMCP(t.Context(), testMCPClientConfig(httpServer.endpoint(), expected, limits, func(context.Context, ElicitationRequest) (ElicitationDecision, error) {
		return ElicitationDecision{Action: ApprovalCancel}, nil
	}))
	if err == nil || !strings.Contains(err.Error(), "require \""+SupportedMCPProtocolVersion+"\" for stateful elicitation") {
		t.Fatalf("unsupported MCP protocol error = %v", err)
	}
}

func TestMCPClientFailsClosedOnCatalogMismatchAndForbiddenResult(t *testing.T) {
	limits := DefaultLimits()
	descriptor := ToolDescriptor{Name: "read_file", Description: "Read.", InputSchema: json.RawMessage(`{"type":"object"}`)}
	expected := mustCatalog(t, []ToolDescriptor{descriptor}, limits)
	server := mcp.NewServer(&mcp.Implementation{Name: "fake-executor-gateway", Version: "v2-test"}, &mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{}})
	var calls atomic.Int64
	server.AddTool(&mcp.Tool{Name: descriptor.Name, Description: "Changed description.", InputSchema: descriptor.InputSchema}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{MIMEType: "image/png", Data: []byte("not-an-image")}}}, nil
	})
	httpServer := startTestMCPServer(t, server)
	_, err := ConnectMCP(t.Context(), testMCPClientConfig(httpServer.endpoint(), expected, limits, func(context.Context, ElicitationRequest) (ElicitationDecision, error) {
		return ElicitationDecision{Action: ApprovalCancel}, nil
	}))
	if err == nil || !strings.Contains(err.Error(), "catalog digest mismatch") {
		t.Fatalf("catalog mismatch error = %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("catalog mismatch dispatched %d calls", calls.Load())
	}

	matching := mustCatalog(t, []ToolDescriptor{{Name: descriptor.Name, Description: "Changed description.", InputSchema: descriptor.InputSchema}}, limits)
	client, err := ConnectMCP(t.Context(), testMCPClientConfig(httpServer.endpoint(), matching, limits, func(context.Context, ElicitationRequest) (ElicitationDecision, error) {
		return ElicitationDecision{Action: ApprovalCancel}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	_, err = client.CallDynamicTool(t.Context(), DynamicCall{
		RunID: "run-result", ThreadID: "thread-result", TurnID: "turn-result", CallID: "call-result",
		RunAttemptGeneration: 1, Namespace: "executor", Tool: descriptor.Name, Arguments: json.RawMessage(`{}`),
	})
	if err == nil || !strings.Contains(err.Error(), "forbidden type") {
		t.Fatalf("forbidden MCP result error = %v", err)
	}
}

func TestMCPClientRejectsRedirectBeforeBearerCanReachAnotherOrigin(t *testing.T) {
	var targetHits atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetHits.Add(1) }))
	t.Cleanup(target.Close)
	source := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+testMCPBearer {
			t.Fatalf("source authorization = %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Location", target.URL+"/stolen")
		writer.WriteHeader(http.StatusTemporaryRedirect)
	}))
	t.Cleanup(source.Close)
	limits := DefaultLimits()
	expected := mustCatalog(t, nil, limits)
	_, err := ConnectMCP(t.Context(), testMCPClientConfig(source.URL+"/mcp", expected, limits, func(context.Context, ElicitationRequest) (ElicitationDecision, error) {
		return ElicitationDecision{Action: ApprovalCancel}, nil
	}))
	if err == nil || !strings.Contains(err.Error(), "forbidden redirect") {
		t.Fatalf("redirect error = %v", err)
	}
	if targetHits.Load() != 0 {
		t.Fatalf("redirect target received %d requests", targetHits.Load())
	}
}

func mustCatalog(t *testing.T, descriptors []ToolDescriptor, limits Limits) *Catalog {
	t.Helper()
	catalog, err := BuildCatalog("executor", "Deterministic executor tools.", descriptors, limits)
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func testMCPClientConfig(endpoint string, expected *Catalog, limits Limits, handler ElicitationHandler) MCPClientConfig {
	return MCPClientConfig{
		Endpoint:              endpoint,
		BearerToken:           testMCPBearer,
		AllowInsecureLoopback: true,
		Namespace:             expected.Namespace(),
		NamespaceDescription:  expected.NamespaceDescription(),
		ExpectedCatalogDigest: expected.Digest(),
		ExpectedCatalog:       expected.CanonicalBytes(),
		Limits:                limits,
		ElicitationHandler:    handler,
	}
}

func waitSignal(t *testing.T, signal <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
}

func TestValidateMCPEndpointAndBearer(t *testing.T) {
	for _, endpoint := range []string{
		"http://example.com/mcp",
		"https://Example.com/mcp",
		"https://example.com/",
		"https://example.com/a/../mcp",
		"https://user@example.com/mcp",
		"https://example.com/mcp?q=1",
		"https://[::1]/mcp",
	} {
		if _, err := validateMCPEndpoint(endpoint, true); err == nil {
			t.Errorf("validateMCPEndpoint(%q) succeeded", endpoint)
		}
	}
	if _, err := validateMCPEndpoint("http://127.0.0.1:1234/mcp", false); err == nil {
		t.Fatal("plain loopback endpoint succeeded without explicit test opt-in")
	}
	if _, err := validateMCPEndpoint("http://127.0.0.1:1234/mcp", true); err != nil {
		t.Fatal(err)
	}
	for _, bearer := range []string{"", "has space", "has\nnewline"} {
		if err := validateBearer(bearer); err == nil {
			t.Errorf("validateBearer(%q) succeeded", bearer)
		}
	}
}

func TestMCPClientRejectsInvalidApprovalMetadata(t *testing.T) {
	valid := mcp.Meta{
		MCPMetaRunID:                "run",
		MCPMetaCallID:               "call",
		MCPMetaRunAttemptGeneration: float64(1),
		MCPMetaToolCatalogDigest:    strings.Repeat("a", 64),
		MCPMetaExecutionID:          "execution",
		MCPMetaApprovalID:           "approval",
		MCPMetaApprovalNonce:        "nonce",
		MCPMetaContextHash:          strings.Repeat("b", 64),
		MCPMetaExpiresAt:            time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano),
		"progressToken":             "call",
	}
	if _, err := parseApprovalMetadata(valid); err != nil {
		t.Fatal(err)
	}
	mutations := []func(mcp.Meta){
		func(meta mcp.Meta) { meta["unknown"] = true },
		func(meta mcp.Meta) { meta[mcp.MetaKeyProtocolVersion] = "future" },
		func(meta mcp.Meta) { meta["progressToken"] = "other-call" },
		func(meta mcp.Meta) { meta[MCPMetaRunAttemptGeneration] = 1.5 },
		func(meta mcp.Meta) { meta[MCPMetaContextHash] = "bad" },
		func(meta mcp.Meta) { delete(meta, MCPMetaApprovalID) },
	}
	for index, mutate := range mutations {
		copyMeta := make(mcp.Meta, len(valid))
		for key, value := range valid {
			copyMeta[key] = value
		}
		mutate(copyMeta)
		if _, err := parseApprovalMetadata(copyMeta); err == nil {
			t.Errorf("invalid metadata mutation %d succeeded: %+v", index, copyMeta)
		}
	}
}

func TestBoundedReadCloserExactBoundaryAndFirstExcessByte(t *testing.T) {
	for _, oneByteAtATime := range []bool{false, true} {
		name := "bulk"
		if oneByteAtATime {
			name = "one-byte"
		}
		t.Run(name, func(t *testing.T) {
			for _, test := range []struct {
				name    string
				input   string
				want    string
				tooLong bool
			}{
				{name: "exact", input: "abc", want: "abc"},
				{name: "first-excess-byte", input: "abcd", want: "abc", tooLong: true},
			} {
				t.Run(test.name, func(t *testing.T) {
					var reader io.Reader = strings.NewReader(test.input)
					if oneByteAtATime {
						reader = iotest.OneByteReader(reader)
					}
					bounded := newBoundedReadCloser(io.NopCloser(reader), 3)
					got, err := io.ReadAll(bounded)
					if string(got) != test.want {
						t.Fatalf("bounded body = %q, want %q", got, test.want)
					}
					var maxBytesError *http.MaxBytesError
					if test.tooLong != errors.As(err, &maxBytesError) {
						t.Fatalf("bounded body error = %v, want MaxBytesError=%v", err, test.tooLong)
					}
					if maxBytesError != nil && maxBytesError.Limit != 3 {
						t.Fatalf("MaxBytesError limit = %d, want 3", maxBytesError.Limit)
					}
				})
			}
		})
	}
}

func TestTransportMessageLimitAddsOverheadWithoutIntOverflow(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxCatalogBytes = maxConfiguredPayloadBytes
	if err := limits.validate(); err != nil {
		t.Fatal(err)
	}
	want := int64(maxConfiguredPayloadBytes) + int64(transportOverhead)
	if got := transportMessageLimit(limits); got != want {
		t.Fatalf("transport message limit = %d, want %d", got, want)
	}
}

func TestValidateElicitationDecision(t *testing.T) {
	valid := []ElicitationDecision{
		{Action: ApprovalAccept, Content: map[string]any{}},
		{Action: ApprovalDecline},
		{Action: ApprovalCancel},
	}
	for _, decision := range valid {
		if err := validateElicitationDecision(decision); err != nil {
			t.Errorf("valid decision %+v: %v", decision, err)
		}
	}
	invalid := []ElicitationDecision{
		{Action: ApprovalAccept},
		{Action: ApprovalDecline, Content: map[string]any{}},
		{Action: "allow"},
	}
	for _, decision := range invalid {
		if err := validateElicitationDecision(decision); err == nil {
			t.Errorf("invalid decision %+v succeeded", decision)
		}
	}
}
