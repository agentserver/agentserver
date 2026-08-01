package harnesspool

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

func TestCoreClientIssuesProductionCapabilitiesWithNoStoreResponse(t *testing.T) {
	issuedAt := time.Date(2026, 8, 2, 10, 0, 0, 123_000_000, time.UTC)
	deadline := issuedAt.Add(30 * time.Minute)
	request := IssueRunCapabilitiesRequest{
		WorkspaceID:  "94000000-0000-4000-8000-000000000001",
		SessionID:    "94000000-0000-4000-8000-000000000002",
		RunID:        "94000000-0000-4000-8000-000000000003",
		RunAttemptID: "94000000-0000-4000-8000-000000000004", HolderID: "pool/holder",
		RunAttemptGeneration: 3, ExpectedRunVersion: 4, ExpectedRunAttemptVersion: 5,
		ExecutorID:         "94000000-0000-4000-8000-000000000005",
		BrainToolCatalogID: "94000000-0000-4000-8000-000000000006",
		ToolCatalogDigest:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Model:              "gpt-5.6-codex", Provider: "openai",
		MaxRunDuration: 30 * time.Minute, MaxApprovalTTL: 10 * time.Second,
	}
	received := make(chan corecontract.IssueRunCapabilitiesRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, httpRequest *http.Request) {
		if httpRequest.Method != http.MethodPost || httpRequest.URL.Path != corecontract.IssueRunCapabilitiesPath ||
			httpRequest.URL.RawQuery != "" || httpRequest.Header.Get("Authorization") != "" {
			http.Error(response, "unexpected capability request", http.StatusNotFound)
			return
		}
		var command corecontract.IssueRunCapabilitiesRequest
		decoder := json.NewDecoder(httpRequest.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&command); err != nil {
			http.Error(response, "invalid command", http.StatusBadRequest)
			return
		}
		received <- command
		response.Header().Set("Content-Type", "application/json")
		response.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(response).Encode(corecontract.IssueRunCapabilitiesResponse{
			ExecutorMCP: corecontract.IssuedRunCapability{
				CapabilityID: "95000000-0000-4000-8000-000000000001", Audience: "executor-mcp",
				Token: "asv2cap1.executor.claims.signature", IssuedAt: issuedAt,
				RunDeadline: deadline, ExpiresAt: deadline.Add(45 * time.Second),
			},
			LLMProxy: corecontract.IssuedRunCapability{
				CapabilityID: "95000000-0000-4000-8000-000000000002", Audience: "llmproxy",
				Token: "asv2cap1.model.claims.signature", IssuedAt: issuedAt,
				RunDeadline: deadline, ExpiresAt: deadline.Add(45 * time.Second),
			},
		})
	}))
	defer server.Close()
	client, err := NewCoreClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.IssueRunCapabilities(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExecutorMCP.Token != "asv2cap1.executor.claims.signature" ||
		result.LLMProxy.Token != "asv2cap1.model.claims.signature" {
		t.Fatalf("Core issuance result = %+v", result)
	}
	command := <-received
	if command.WorkspaceID != request.WorkspaceID || command.SessionID != request.SessionID ||
		command.RunID != request.RunID || command.RunAttemptID != request.RunAttemptID ||
		command.HolderID != request.HolderID || command.RunAttemptGeneration != request.RunAttemptGeneration ||
		command.ExpectedRunVersion != request.ExpectedRunVersion ||
		command.ExpectedRunAttemptVersion != request.ExpectedRunAttemptVersion ||
		command.ExecutorID != request.ExecutorID || command.BrainToolCatalogID != request.BrainToolCatalogID ||
		command.ToolCatalogDigest != request.ToolCatalogDigest || command.Model != request.Model ||
		command.Provider != request.Provider || command.MaxRunDurationMillis != request.MaxRunDuration.Milliseconds() ||
		command.MaxApprovalTTLMillis != request.MaxApprovalTTL.Milliseconds() {
		t.Fatalf("Core capability command = %+v", command)
	}
}

func TestCoreClientRejectsCacheableCapabilityResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{}`))
	}))
	defer server.Close()
	client, err := NewCoreClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.IssueRunCapabilities(t.Context(), IssueRunCapabilitiesRequest{})
	if err == nil || err.Error() != "core capability response is missing Cache-Control no-store" {
		t.Fatalf("cacheable capability response error = %v", err)
	}
}
