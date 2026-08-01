package executorgateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

func TestCoreApprovalClientValidatesCompleteCreateFingerprint(t *testing.T) {
	request := testCreateApprovalClientRequest()
	valid := testCreateApprovalContractResponse(request)

	for _, test := range []struct {
		name   string
		mutate func(*corecontract.CreateApprovalResponse)
		want   string
	}{
		{name: "valid"},
		{
			name: "different-nonce",
			mutate: func(response *corecontract.CreateApprovalResponse) {
				response.Approval.Nonce = "40000000-0000-4000-8000-000000000099"
			},
			want: "fingerprint",
		},
		{
			name: "different-execution-scope",
			mutate: func(response *corecontract.CreateApprovalResponse) {
				response.Approval.RunAttemptID = "40000000-0000-4000-8000-000000000098"
			},
			want: "scope differs",
		},
		{
			name: "pending-with-decision-evidence",
			mutate: func(response *corecontract.CreateApprovalResponse) {
				decided := response.Approval.UpdatedAt
				response.Approval.ApproverID = "40000000-0000-4000-8000-000000000097"
				response.Approval.Decision = "approve"
				response.Approval.DecidedAt = &decided
			},
			want: "pending approval contains terminal decision evidence",
		},
		{
			name: "wrong-context-domain",
			mutate: func(response *corecontract.CreateApprovalResponse) {
				response.Approval.ContextDigest.Domain = "policy-context"
			},
			want: "approval-context",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := valid
			if test.mutate != nil {
				test.mutate(&response)
			}
			client := newApprovalResponseTestClient(t, response)
			result, err := client.CreateApproval(t.Context(), request)
			if test.want == "" {
				if err != nil || !result.Created || result.Approval.Status != "pending" {
					t.Fatalf("CreateApproval() = %+v, %v", result, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("CreateApproval() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestCoreApprovalClientRejectsInvalidConsumedEvidence(t *testing.T) {
	create := testCreateApprovalClientRequest()
	response := testCreateApprovalContractResponse(create)
	decidedAt := response.Approval.UpdatedAt.Add(time.Second)
	consumedAt := decidedAt.Add(-time.Millisecond)
	response.Execution.Status = "approved"
	response.Execution.Version++
	response.Approval.Status = "consumed"
	response.Approval.Version = 3
	response.Approval.ApproverID = "40000000-0000-4000-8000-000000000097"
	response.Approval.Decision = "approve"
	response.Approval.DecidedAt = &decidedAt
	response.Approval.ConsumedAt = &consumedAt

	client := newApprovalResponseTestClient(t, corecontract.ConsumeApprovalResponse{
		Execution: response.Execution, Approval: response.Approval, Consumed: true,
	})
	contextDigest, err := gatewayCanonicalDigest(response.Approval.ContextDigest, "approval-context")
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ConsumeApproval(t.Context(), ConsumeApprovalRequest{
		ApprovalID: create.ApprovalID, ExecutionID: create.ExecutionID,
		RunID: create.RunID, RunAttemptID: create.RunAttemptID, HolderID: create.HolderID,
		RunAttemptGeneration: create.RunAttemptGeneration, Nonce: create.Nonce,
		ContextDigest: contextDigest, ExpectedApprovalVersion: 2, ExpectedExecutionVersion: 1,
		Record: testGatewayTransitionRecord(20),
	})
	if err == nil || !strings.Contains(err.Error(), "canonical consumption evidence") {
		t.Fatalf("ConsumeApproval() error = %v", err)
	}
}

func newApprovalResponseTestClient(t *testing.T, responseBody any) *CoreConnectionClient {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(response).Encode(responseBody); err != nil {
			t.Errorf("encode approval response: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	client, err := NewCoreConnectionClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func testCreateApprovalClientRequest() CreateApprovalRequest {
	return CreateApprovalRequest{
		ApprovalID:  "40000000-0000-4000-8000-000000000071",
		ExecutionID: testCoreExecutionID, RunID: testCoreRunID, RunAttemptID: testCoreAttemptID,
		HolderID: "holder", RunAttemptGeneration: 3, ExpectedExecutionVersion: 1,
		Nonce:       "40000000-0000-4000-8000-000000000072",
		RequesterID: "40000000-0000-4000-8000-000000000073",
		ExpiresAt:   time.Date(2026, time.July, 30, 12, 5, 0, 0, time.UTC),
		Record:      testGatewayTransitionRecord(19),
	}
}

func testCreateApprovalContractResponse(request CreateApprovalRequest) corecontract.CreateApprovalResponse {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	execution := testContractExecutionState("pending_approval", 1, nil)
	execution.PolicyDecision = PolicyDecisionAsk
	return corecontract.CreateApprovalResponse{
		Execution: execution,
		Approval: corecontract.ApprovalState{
			ApprovalID: request.ApprovalID, ExecutionID: request.ExecutionID,
			RunID: request.RunID, RunAttemptID: request.RunAttemptID,
			RunAttemptGeneration: request.RunAttemptGeneration, Nonce: request.Nonce,
			RequesterID: request.RequesterID, ContextDigest: testContractDigest("approval-context", "approval-context"),
			Status: "pending", ExpiresAt: request.ExpiresAt, Version: 1, CreatedAt: now, UpdatedAt: now,
		},
		Created: true,
	}
}
