package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExecuteSmokeRequiresTLS13AndObservesTerminal(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v2/workspaces/"+smokeWorkspaceID+"/sessions/"+smokeSessionID+"/agui" ||
			request.Header.Get("Authorization") != "Bearer test-browser-bearer" || request.Header.Get("Idempotency-Key") == "" {
			t.Errorf("smoke request = %s %s headers=%v", request.Method, request.URL.Path, request.Header)
			http.Error(response, "bad request", http.StatusBadRequest)
			return
		}
		response.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(response, "data: {\"type\":\"TEXT_MESSAGE_CONTENT\",\"delta\":\""+finalMessage+"\"}\n\n")
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

func TestInspectSSERejectsMalformedDataAndReportsMissingPieces(t *testing.T) {
	if _, _, err := inspectSSE([]byte("data: not-json\n")); err == nil {
		t.Fatal("malformed SSE data accepted")
	}
	terminal, scripted, err := inspectSSE([]byte("data: {\"type\":\"RUN_FINISHED\"}\n"))
	if err != nil || !terminal || scripted {
		t.Fatalf("inspectSSE terminal = %v scripted = %v error = %v", terminal, scripted, err)
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
