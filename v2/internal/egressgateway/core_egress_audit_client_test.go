package egressgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

type egressAuditRoundTripFunc func(*http.Request) (*http.Response, error)

func (function egressAuditRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestCoreEgressAuditClientSerializesCredentialVersion(t *testing.T) {
	now := time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)
	transport := egressAuditRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.Path != corecontract.RecordEgressCredentialAuditPath {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		var command corecontract.RecordEgressCredentialAuditRequest
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&command); err != nil {
			t.Fatal(err)
		}
		if command.CredentialVersion != 7 || command.AuthorityVersion != 3 || command.BindingID != "binding-1" {
			t.Fatalf("audit command = %+v", command)
		}
		body := []byte(`{"recorded":true}`)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "Cache-Control": []string{"no-store"}},
			Body:       io.NopCloser(bytes.NewReader(body)), Request: request,
		}, nil
	})
	client, err := newCoreEgressAuditClient("https://core.internal", &http.Client{Transport: transport}, func() (string, error) {
		return "11111111-1111-4111-8111-111111111111", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = client.RecordEgressDecision(context.Background(), AuditRecord{
		At: now, CapabilityID: "capability-1", WorkspaceID: "workspace-1", SessionID: "session-1",
		ActorID: "actor-1", EnvironmentID: "environment-1", RunID: "run-1", RunAttemptID: "attempt-1",
		RunAttemptGeneration: 2, ExecutionID: "execution-1", OperationID: "operation-1",
		SandboxID: "sandbox-1", TargetGeneration: 3, ProviderKind: "lark", BindingID: "binding-1",
		AuthorityVersion: 3, CredentialVersion: 7, PSM: "bytedance.sandbox.agentserver",
		Host: "open.feishu.cn", Path: "/open-apis/docx/v1/documents/document-1/raw_content",
		Method: "GET", Decision: "allow", ReasonCode: "allowed",
	})
	if err != nil {
		t.Fatal(err)
	}
}
