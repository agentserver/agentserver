package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestExecutorMCPHeaderBudgetAndCancellation(t *testing.T) {
	type scenario struct {
		name                                    string
		delay, legacyHeaderTimeout, callTimeout time.Duration
		wantError                               bool
	}
	cases := []scenario{
		{"slow tool", 150 * time.Millisecond, 0, 3 * time.Second, false},
		{"old header budget reproduces failure", 150 * time.Millisecond, 50 * time.Millisecond, 3 * time.Second, true},
		{"run deadline still cancels tool", 150 * time.Millisecond, 0, 50 * time.Millisecond, true},
	}
	if os.Getenv("AGENTSERVER_RUN_LONG_MCP_TESTS") == "1" {
		cases = append(cases, scenario{"tool beyond old 30s cutoff", 31 * time.Second, 0, 40 * time.Second, false})
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			caPath, certPath, keyPath := writeWorkerTestTLS(t, t.TempDir())
			client, err := newWorkerExecutorHTTPClient(workerTLSDocument{CAFile: caPath, CertificateFile: certPath, KeyFile: keyPath})
			if err != nil {
				t.Fatal(err)
			}
			defer client.CloseIdleConnections()
			transport := client.Transport.(*http.Transport)
			transport.ResponseHeaderTimeout = test.legacyHeaderTimeout
			certificate, err := tls.LoadX509KeyPair(certPath, keyPath)
			if err != nil {
				t.Fatal(err)
			}
			pem, err := os.ReadFile(caPath)
			if err != nil {
				t.Fatal(err)
			}
			roots := x509.NewCertPool()
			if !roots.AppendCertsFromPEM(pem) {
				t.Fatal("test CA missing")
			}
			var calls atomic.Int64
			server := mcp.NewServer(&mcp.Implementation{Name: "executor-fixture", Version: "1"}, nil)
			server.AddTool(&mcp.Tool{Name: "shell", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				calls.Add(1)
				timer := time.NewTimer(test.delay)
				defer timer.Stop()
				select {
				case <-timer.C:
					return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "finished"}}}, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			})
			handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: false})
			https := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.ProtoMajor != 2 || r.TLS == nil || len(r.TLS.VerifiedChains) == 0 {
					http.Error(w, "authenticated HTTP2 required", 403)
					return
				}
				handler.ServeHTTP(w, r)
			}))
			https.EnableHTTP2 = true
			https.TLS = &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots}
			https.StartTLS()
			defer https.Close()
			connectCtx, cancelConnect := context.WithTimeout(t.Context(), test.callTimeout+5*time.Second)
			defer cancelConnect()
			mcpClient := mcp.NewClient(&mcp.Implementation{Name: "worker-fixture", Version: "1"}, nil)
			session, err := mcpClient.Connect(connectCtx, &mcp.StreamableClientTransport{Endpoint: https.URL + "/mcp", HTTPClient: client, MaxRetries: -1, DisableStandaloneSSE: true}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			callCtx, cancelCall := context.WithTimeout(t.Context(), test.callTimeout)
			defer cancelCall()
			result, err := session.CallTool(callCtx, &mcp.CallToolParams{Name: "shell", Arguments: map[string]any{}})
			if test.wantError {
				if err == nil || !(strings.Contains(err.Error(), "timeout") || strings.Contains(err.Error(), "deadline")) {
					t.Fatalf("expected bounded timeout, got %v", err)
				}
			} else if err != nil || result == nil || result.IsError {
				t.Fatalf("long-running MCP tool was truncated: %+v %v", result, err)
			} else if len(result.Content) != 1 {
				t.Fatalf("tool result missing: %+v", result)
			} else if content, ok := result.Content[0].(*mcp.TextContent); !ok || content.Text != "finished" {
				t.Fatalf("tool completion was not delivered: %+v", result)
			}
			if calls.Load() != 1 {
				t.Fatalf("tool was retried: %d", calls.Load())
			}
		})
	}
}
