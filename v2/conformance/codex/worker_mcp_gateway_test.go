package codex_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/harnessworker"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	workerMCPGatewayMaxRequestBytes = 2 * 1024 * 1024
	workerMCPGatewayMaxFailures     = 64
)

type workerMCPGatewayConfig struct {
	BearerToken string
	Catalog     *harnessworker.Catalog
	CallTool    func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error)
}

// workerMCPGateway is a stateful Streamable HTTP fixture for production-shape
// worker tests. Stock app-server never receives its endpoint or bearer: only
// harnessworker.MCPClient connects to it. The fixture records authentication
// and side-effect entry without retaining the credential value in failures.
type workerMCPGateway struct {
	server *httptest.Server

	requestCount atomic.Int64
	badAuthCount atomic.Int64
	toolCalls    atomic.Int64

	failuresMu sync.Mutex
	failures   []string
}

func TestWorkerMCPGatewayAuthenticatesReferenceClient(t *testing.T) {
	catalog := approvedDynamicExecutorCatalog(t)
	gateway := startWorkerMCPGateway(t, workerMCPGatewayConfig{
		BearerToken: a11SourceCapability,
		Catalog:     catalog,
		CallTool: func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if request.Params.Name != approvedMCPToolName {
				return nil, fmt.Errorf("fixture tool = %q", request.Params.Name)
			}
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "fixture result"}},
			}, nil
		},
	})
	client := connectWorkerMCPClient(t, gateway, a11SourceCapability, catalog)
	result, err := client.CallDynamicTool(t.Context(), harnessworker.DynamicCall{
		RunID:                "run-worker-gateway-fixture",
		ThreadID:             "thread-worker-gateway-fixture",
		TurnID:               "turn-worker-gateway-fixture",
		CallID:               "call-worker-gateway-fixture",
		RunAttemptGeneration: 1,
		Namespace:            executorDynamicNamespace,
		Tool:                 approvedMCPToolName,
		Arguments:            json.RawMessage(`{"message":"fixture"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success || len(result.ContentItems) != 1 || result.ContentItems[0].Text != "fixture result" {
		t.Fatalf("worker MCP fixture result = %+v", result)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	gateway.AssertAuthenticated(t)
	if calls := gateway.ToolCalls(); calls != 1 {
		t.Fatalf("worker MCP fixture calls = %d, want one", calls)
	}
}

func startWorkerMCPGateway(t *testing.T, config workerMCPGatewayConfig) *workerMCPGateway {
	t.Helper()
	if config.BearerToken == "" {
		t.Fatal("worker MCP gateway bearer is required")
	}
	if config.Catalog == nil || len(config.Catalog.Tools()) == 0 {
		t.Fatal("worker MCP gateway catalog must contain at least one tool")
	}

	result := &workerMCPGateway{}
	server := mcp.NewServer(
		&mcp.Implementation{Name: "agentserver-v2-worker-gateway-fixture", Version: "v2-test"},
		&mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{}},
	)
	for _, descriptor := range config.Catalog.Tools() {
		descriptor := descriptor
		server.AddTool(&mcp.Tool{
			Name:        descriptor.Name,
			Description: descriptor.Description,
			InputSchema: descriptor.InputSchema,
		}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			result.toolCalls.Add(1)
			if request == nil || request.Params == nil || request.Params.Name != descriptor.Name {
				return nil, fmt.Errorf("worker MCP gateway received an invalid %q tool request", descriptor.Name)
			}
			if config.CallTool == nil {
				return nil, fmt.Errorf("worker MCP gateway received unexpected tools/call %q", descriptor.Name)
			}
			return config.CallTool(ctx, request)
		})
	}

	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		result.requestCount.Add(1)
		if request.URL.Path != "/mcp" || request.URL.RawQuery != "" {
			result.recordFailure("worker MCP request used a non-canonical endpoint")
			http.NotFound(writer, request)
			return
		}
		if got := request.Header.Get("Authorization"); got != "Bearer "+config.BearerToken {
			result.badAuthCount.Add(1)
			result.recordFailure(fmt.Sprintf("worker MCP request bearer mismatch: present=%t length=%d", got != "", len(got)))
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		request.Body = http.MaxBytesReader(writer, request.Body, workerMCPGatewayMaxRequestBytes)
		mcpHandler.ServeHTTP(writer, request)
	})
	result.server = httptest.NewUnstartedServer(handler)
	result.server.Config.MaxHeaderBytes = 32 * 1024
	result.server.Start()
	t.Cleanup(result.server.Close)
	return result
}

func (g *workerMCPGateway) Endpoint() string { return g.server.URL + "/mcp" }

func (g *workerMCPGateway) ToolCalls() int64 { return g.toolCalls.Load() }

func (g *workerMCPGateway) Failures() []string {
	g.failuresMu.Lock()
	defer g.failuresMu.Unlock()
	return append([]string(nil), g.failures...)
}

func (g *workerMCPGateway) AssertAuthenticated(t *testing.T) {
	t.Helper()
	if failures := g.Failures(); len(failures) != 0 {
		t.Fatalf("worker MCP gateway failures: %v", failures)
	}
	if requests, badAuth := g.requestCount.Load(), g.badAuthCount.Load(); requests == 0 || badAuth != 0 {
		t.Fatalf("worker MCP gateway requests/bad auth = %d/%d", requests, badAuth)
	}
}

func (g *workerMCPGateway) recordFailure(message string) {
	g.failuresMu.Lock()
	defer g.failuresMu.Unlock()
	if len(g.failures) < workerMCPGatewayMaxFailures {
		g.failures = append(g.failures, message)
	}
}

func connectWorkerMCPClient(
	t *testing.T,
	gateway *workerMCPGateway,
	bearer string,
	catalog *harnessworker.Catalog,
) *harnessworker.MCPClient {
	t.Helper()
	client, err := harnessworker.ConnectMCP(t.Context(), harnessworker.MCPClientConfig{
		Endpoint:              gateway.Endpoint(),
		BearerToken:           bearer,
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
		t.Fatalf("connect worker-owned MCP client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}
