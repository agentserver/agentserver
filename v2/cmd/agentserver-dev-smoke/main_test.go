package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/devfixtures"
)

func TestExecuteSmokeRequiresTLS13AndObservesTerminal(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet && request.URL.Path == "/" {
			if request.Header.Get("Authorization") != "" {
				t.Errorf("reference web request leaked Authorization: %v", request.Header)
			}
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			response.Header().Set("Cache-Control", "no-store")
			response.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; connect-src 'self'")
			_, _ = io.WriteString(response, `<main data-agentserver-reference-web="v2"></main>`)
			return
		}
		if request.Method != http.MethodPost || request.URL.Path != "/v2/workspaces/"+smokeWorkspaceID+"/sessions/"+smokeSessionID+"/agui" ||
			request.Header.Get("Authorization") != "Bearer test-browser-bearer" || request.Header.Get("Idempotency-Key") == "" {
			t.Errorf("smoke request = %s %s headers=%v", request.Method, request.URL.Path, request.Header)
			http.Error(response, "bad request", http.StatusBadRequest)
			return
		}
		response.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(response, "data: {\"type\":\"TEXT_MESSAGE_CONTENT\",\"delta\":\""+finalMessage+"\"}\n\n")
		_, _ = io.WriteString(response, "data: {\"type\":\"CUSTOM\",\"name\":\"a2ui.operations\",\"value\":[{\"version\":\"v0.9\",\"createSurface\":{\"surfaceId\":\"command-event-1\"}},{\"version\":\"v0.9\",\"updateDataModel\":{\"surfaceId\":\"command-event-1\",\"value\":{\"command\":\"[\\\"/bin/pwd\\\"]\",\"output\":\"/workspace\\n\",\"status\":\"succeeded (exit 0)\"}}}]}\n\n")
		_, _ = io.WriteString(response, "data: {\"type\":\"RUN_FINISHED\"}\n\n")
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
	server.StartTLS()
	defer server.Close()

	root := t.TempDir()
	caFile := filepath.Join(root, "ca.pem")
	certificate := server.Certificate()
	if certificate == nil {
		t.Fatal("test server has no certificate")
	}
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	bearerFile := filepath.Join(root, "bearer.token")
	if err := os.WriteFile(bearerFile, []byte("test-browser-bearer\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	requestID, stream, err := executeSmoke(t.Context(), smokeOptions{
		origin: server.URL, caFile: caFile, bearerFile: bearerFile,
	})
	if err != nil || !strings.HasPrefix(requestID, "smoke-") || len(stream) == 0 {
		t.Fatalf("executeSmoke() = id %q stream %q error %v", requestID, stream, err)
	}
	if server.TLS.MinVersion != tls.VersionTLS13 {
		t.Fatalf("test server minimum TLS = %x", server.TLS.MinVersion)
	}
	if _, err := x509.ParseCertificate(certificate.Raw); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteSmokeApprovesOnlyCanonicalApprovalCommandEvent(t *testing.T) {
	const (
		approvalID = "70000000-0000-4000-8000-000000000070"
		nonce      = "71000000-0000-4000-8000-000000000071"
	)
	digest := strings.Repeat("a", 64)
	approved := make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			response.Header().Set("Cache-Control", "no-store")
			response.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; connect-src 'self'")
			_, _ = io.WriteString(response, `<main data-agentserver-reference-web="v2"></main>`)
		case request.Method == http.MethodPost && request.URL.Path == "/v2/workspaces/"+smokeWorkspaceID+"/sessions/"+smokeSessionID+"/agui":
			flusher, ok := response.(http.Flusher)
			if !ok {
				t.Error("test response does not support streaming")
				return
			}
			response.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(response, `data: {"type":"CUSTOM","name":"agentserver.approval","value":{"approvalId":"`+approvalID+`","nonce":"`+nonce+`","status":"pending","version":1,"contextDigest":{"domain":"approval-context","canonicalizerVersion":"rfc8785-v1","sha256":"`+digest+`"}}}`+"\n\n")
			flusher.Flush()
			select {
			case <-approved:
			case <-request.Context().Done():
				return
			}
			_, _ = io.WriteString(response, "data: {\"type\":\"TEXT_MESSAGE_CONTENT\",\"delta\":\""+finalMessage+"\"}\n\n")
			_, _ = io.WriteString(response, "data: {\"type\":\"CUSTOM\",\"name\":\"a2ui.operations\",\"value\":[{\"version\":\"v0.9\",\"createSurface\":{\"surfaceId\":\"command-event-1\"}},{\"version\":\"v0.9\",\"updateDataModel\":{\"surfaceId\":\"command-event-1\",\"value\":{\"command\":\"[\\\"/bin/pwd\\\"]\",\"output\":\"/workspace\\n\",\"status\":\"succeeded (exit 0)\"}}}]}\n\n")
			_, _ = io.WriteString(response, "data: {\"type\":\"RUN_FINISHED\"}\n\n")
			flusher.Flush()
		case request.Method == http.MethodPost && request.URL.Path == "/v2/workspaces/"+smokeWorkspaceID+"/approvals/"+approvalID+":decide":
			if request.Header.Get("Authorization") != "Bearer test-browser-bearer" || request.Header.Get("Content-Type") != "application/json" {
				t.Errorf("approval request headers = %v", request.Header)
				http.Error(response, "bad request", http.StatusBadRequest)
				return
			}
			var body struct {
				Decision                string `json:"decision"`
				Nonce                   string `json:"nonce"`
				ExpectedApprovalVersion int64  `json:"expectedApprovalVersion"`
				ContextDigest           struct {
					Domain               string `json:"domain"`
					CanonicalizerVersion string `json:"canonicalizerVersion"`
					SHA256               string `json:"sha256"`
				} `json:"contextDigest"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body.Decision != "approve" ||
				body.Nonce != nonce || body.ExpectedApprovalVersion != 1 ||
				body.ContextDigest.Domain != "approval-context" ||
				body.ContextDigest.CanonicalizerVersion != "rfc8785-v1" || body.ContextDigest.SHA256 != digest {
				t.Errorf("approval decision body = %+v, error %v", body, err)
				http.Error(response, "bad request", http.StatusBadRequest)
				return
			}
			close(approved)
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"executionStatus":"pending_approval","approval":{"approvalId":"`+approvalID+`","nonce":"`+nonce+`","status":"approved","decision":"approve","version":2}}`)
		default:
			http.NotFound(response, request)
		}
	}))
	server.EnableHTTP2 = true
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
	server.StartTLS()
	defer server.Close()
	caFile, bearerFile := writeSmokeTestAuthority(t, server)

	requestID, stream, err := executeSmoke(t.Context(), smokeOptions{
		origin: server.URL, caFile: caFile, bearerFile: bearerFile,
	})
	if err != nil || !strings.HasPrefix(requestID, "smoke-") || !strings.Contains(string(stream), approvalID) {
		t.Fatalf("executeSmoke() = id %q stream %q error %v", requestID, stream, err)
	}
}

func TestExecuteCancellationSmokeWaitsForRunningHoldAndExplicitTerminal(t *testing.T) {
	const serverRunID = "30000000-0000-4000-8000-000000000003"
	cancelled := make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/v2/workspaces/"+smokeWorkspaceID+"/sessions/"+smokeSessionID+"/agui":
			body, err := io.ReadAll(request.Body)
			if err != nil || !strings.Contains(string(body), devfixtures.CancellationHoldMarker) || request.Header.Get("Authorization") != "Bearer test-browser-bearer" {
				t.Errorf("cancellation smoke request = body %q error %v headers=%v", body, err, request.Header)
				http.Error(response, "bad request", http.StatusBadRequest)
				return
			}
			flusher, ok := response.(http.Flusher)
			if !ok {
				t.Error("test response does not support streaming")
				return
			}
			response.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(response, "data: {\"type\":\"RUN_STARTED\",\"runId\":\""+serverRunID+"\"}\n\n")
			_, _ = io.WriteString(response, "data: {\"type\":\"CUSTOM\",\"name\":\"a2ui.operations\",\"value\":[{\"version\":\"v0.9\",\"createSurface\":{\"surfaceId\":\"command-event-1\"}},{\"version\":\"v0.9\",\"updateDataModel\":{\"surfaceId\":\"command-event-1\",\"value\":{\"command\":\"[\\\"/bin/pwd\\\"]\",\"output\":\"/workspace\\n\",\"status\":\"succeeded (exit 0)\"}}}]}\n\n")
			flusher.Flush()
			select {
			case <-cancelled:
			case <-request.Context().Done():
				return
			}
			_, _ = io.WriteString(response, "data: {\"type\":\"RUN_ERROR\",\"runId\":\""+serverRunID+"\",\"code\":\"user_cancelled\",\"message\":\"cancelled\"}\n\n")
			flusher.Flush()
		case request.Method == http.MethodPost && request.URL.Path == "/v2/workspaces/"+smokeWorkspaceID+"/runs/"+serverRunID+":cancel":
			if request.ContentLength != 0 || request.URL.RawQuery != "" || request.Header.Get("Authorization") != "Bearer test-browser-bearer" {
				t.Errorf("explicit cancel request = length %d query %q headers=%v", request.ContentLength, request.URL.RawQuery, request.Header)
				http.Error(response, "bad request", http.StatusBadRequest)
				return
			}
			close(cancelled)
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"workspaceId":"`+smokeWorkspaceID+`","sessionId":"`+smokeSessionID+`","runId":"`+serverRunID+`","status":"cancelling","runVersion":4,"terminal":false,"changed":true}`)
		default:
			http.NotFound(response, request)
		}
	}))
	server.EnableHTTP2 = true
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
	server.StartTLS()
	defer server.Close()
	caFile, bearerFile := writeSmokeTestAuthority(t, server)

	requestID, stream, err := executeCancellationSmoke(t.Context(), smokeOptions{
		origin: server.URL, caFile: caFile, bearerFile: bearerFile,
	})
	if err != nil || !strings.HasPrefix(requestID, "smoke-") ||
		!strings.Contains(string(stream), `"code":"user_cancelled"`) {
		t.Fatalf("executeCancellationSmoke() = id %q stream %q error %v", requestID, stream, err)
	}
}

func TestInspectSSERejectsMalformedDataAndReportsMissingPieces(t *testing.T) {
	if _, _, _, err := inspectSSE([]byte("data: not-json\n")); err == nil {
		t.Fatal("malformed SSE data accepted")
	}
	terminal, scripted, commandSurface, err := inspectSSE([]byte("data: {\"type\":\"RUN_FINISHED\"}\n"))
	if err != nil || !terminal || scripted || commandSurface {
		t.Fatalf("inspectSSE terminal = %v scripted = %v commandSurface = %v error = %v", terminal, scripted, commandSurface, err)
	}
}

func TestRunRejectsIncompleteArguments(t *testing.T) {
	var stderr strings.Builder
	if exitCode := run(context.Background(), []string{"--origin=https://127.0.0.1:17444"}, io.Discard, &stderr); exitCode != 1 ||
		!strings.Contains(stderr.String(), "CA file is required") {
		t.Fatalf("run() = %d stderr=%q", exitCode, stderr.String())
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if exitCode := run(cancelled, nil, io.Discard, io.Discard); exitCode != 1 || time.Since(start) > time.Second {
		t.Fatalf("cancelled run() = %d after %s", exitCode, time.Since(start))
	}
}

func writeSmokeTestAuthority(t *testing.T, server *httptest.Server) (string, string) {
	t.Helper()
	root := t.TempDir()
	caFile := filepath.Join(root, "ca.pem")
	certificate := server.Certificate()
	if certificate == nil {
		t.Fatal("test server has no certificate")
	}
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	bearerFile := filepath.Join(root, "bearer.token")
	if err := os.WriteFile(bearerFile, []byte("test-browser-bearer\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return caFile, bearerFile
}
