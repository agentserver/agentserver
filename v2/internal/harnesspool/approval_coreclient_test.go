package harnesspool

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

func TestCoreClientObserveApprovalRequiresCanonicalScopedEvidence(t *testing.T) {
	request := testObserveApprovalClientRequest()
	valid := testObserveApprovalContractResponse(request)

	t.Run("valid approved outcome", func(t *testing.T) {
		client, received := newObserveApprovalResponseClient(t, valid)
		result, err := client.ObserveApproval(t.Context(), request)
		if err != nil || !result.OutcomeAvailable || result.Approval.Status != "approved" ||
			result.Approval.Version != 2 {
			t.Fatalf("ObserveApproval() = %+v, %v", result, err)
		}
		wire := <-received
		if wire.ApprovalID != request.ApprovalID || wire.ExecutionID != request.ExecutionID ||
			wire.WaitMillis != request.Wait.Milliseconds() ||
			wire.ContextDigest.SHA256 != hex.EncodeToString(request.ContextHash[:]) {
			t.Fatalf("approval observation wire request = %+v", wire)
		}
	})

	tests := []struct {
		name   string
		mutate func(*corecontract.ObserveApprovalResponse)
		want   string
	}{
		{
			name: "execution scope drift",
			mutate: func(response *corecontract.ObserveApprovalResponse) {
				response.ExecutionID = "51000000-0000-4000-8000-000000000051"
			},
			want: "fingerprint",
		},
		{
			name: "attempt scope drift",
			mutate: func(response *corecontract.ObserveApprovalResponse) {
				response.Approval.RunAttemptID = "52000000-0000-4000-8000-000000000052"
			},
			want: "fingerprint",
		},
		{
			name: "context digest domain drift",
			mutate: func(response *corecontract.ObserveApprovalResponse) {
				response.Approval.ContextDigest.Domain = "different-domain"
			},
			want: "context digest",
		},
		{
			name: "approval version regression",
			mutate: func(response *corecontract.ObserveApprovalResponse) {
				response.Approval.Version = 0
			},
			want: "fingerprint",
		},
		{
			name: "approved without approver evidence",
			mutate: func(response *corecontract.ObserveApprovalResponse) {
				response.Approval.ApproverID = ""
			},
			want: "decision evidence",
		},
		{
			name: "outcome availability drift",
			mutate: func(response *corecontract.ObserveApprovalResponse) {
				response.OutcomeAvailable = false
			},
			want: "inconsistent",
		},
		{
			name: "execution status drift",
			mutate: func(response *corecontract.ObserveApprovalResponse) {
				response.ExecutionStatus = "approved"
			},
			want: "inconsistent",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := testObserveApprovalContractResponse(request)
			test.mutate(&response)
			client, _ := newObserveApprovalResponseClient(t, response)
			_, err := client.ObserveApproval(t.Context(), request)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ObserveApproval() error = %v, want %q", err, test.want)
			}
		})
	}
}

func testObserveApprovalClientRequest() ObserveApprovalRequest {
	return ObserveApprovalRequest{
		ApprovalID:  "47000000-0000-4000-8000-000000000047",
		ExecutionID: "48000000-0000-4000-8000-000000000048",
		RunID:       testRunID, RunAttemptID: testRunAttemptID, HolderID: "pool-holder",
		RunAttemptGeneration: 3,
		Nonce:                "49000000-0000-4000-8000-000000000049",
		ContextHash:          sha256.Sum256([]byte("approval context")),
		AfterApprovalVersion: 1, Wait: 20 * time.Second, Record: testTransitionRecord(9),
	}
}

func testObserveApprovalContractResponse(request ObserveApprovalRequest) corecontract.ObserveApprovalResponse {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	decidedAt := now.Add(time.Second)
	return corecontract.ObserveApprovalResponse{
		ExecutionID: request.ExecutionID, ExecutionStatus: "pending_approval", ExecutionVersion: 2,
		Approval: corecontract.ApprovalState{
			ApprovalID: request.ApprovalID, ExecutionID: request.ExecutionID,
			RunID: request.RunID, RunAttemptID: request.RunAttemptID,
			RunAttemptGeneration: request.RunAttemptGeneration, Nonce: request.Nonce,
			RequesterID: "executor-gateway", ApproverID: "50000000-0000-4000-8000-000000000050",
			Decision: "approve",
			ContextDigest: corecontract.CanonicalJSONDigest{
				Domain: "approval-context", CanonicalizerVersion: "rfc8785-v1",
				SHA256: hex.EncodeToString(request.ContextHash[:]),
			},
			Status: "approved", ExpiresAt: now.Add(time.Minute), DecidedAt: &decidedAt,
			Version: 2, CreatedAt: now, UpdatedAt: decidedAt,
		},
		OutcomeAvailable: true,
	}
}

func newObserveApprovalResponseClient(
	t *testing.T,
	response corecontract.ObserveApprovalResponse,
) (*CoreClient, <-chan corecontract.ObserveApprovalRequest) {
	t.Helper()
	received := make(chan corecontract.ObserveApprovalRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || !strings.HasSuffix(request.URL.Path, ":observe") {
			http.NotFound(writer, request)
			return
		}
		var command corecontract.ObserveApprovalRequest
		if err := json.NewDecoder(request.Body).Decode(&command); err != nil {
			t.Errorf("decode observation request: %v", err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		received <- command
		writer.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(writer).Encode(response); err != nil {
			t.Errorf("encode observation response: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	client, err := NewCoreClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	return client, received
}
