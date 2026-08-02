package browsergateway

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

func TestCoreExecutorBackendForwardsOnlyExactUserAuthority(t *testing.T) {
	now := time.Date(2026, 8, 2, 16, 0, 0, 0, time.UTC)
	calls := 0
	client := &http.Client{Transport: browserRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Method != http.MethodPost || request.Header.Get("Authorization") != "Bearer browser-user-token" ||
			request.Header.Get("Accept") != "application/json" {
			t.Fatalf("core executor request = %s %s headers=%v", request.Method, request.URL, request.Header)
		}
		switch calls {
		case 1:
			if request.URL.Path != corecontract.CreateExecutorResourcePath(executorResourceWorkspace) ||
				request.Header.Get("Content-Type") != "application/json" || request.Header.Get("Idempotency-Key") != "" {
				t.Fatalf("create request = %s headers=%v", request.URL, request.Header)
			}
			var input corecontract.CreateExecutorResourceRequest
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil || input.ExecutorID != executorResourceExecutor {
				t.Fatalf("create input = %+v, %v", input, err)
			}
			return executorCoreJSONResponse(request, http.StatusCreated, corecontract.CreateExecutorResourceResponse{
				Executor: executorResourceState(now, "enrolling"), Created: true,
			}), nil
		case 2:
			if request.URL.Path != corecontract.IssueExecutorEnrollmentTokenPath(executorResourceWorkspace, executorResourceExecutor) ||
				request.Header.Get("Idempotency-Key") != "enroll-1" || request.Header.Get("Content-Type") != "" || request.Body != nil {
				t.Fatalf("issue request = %s headers=%v body=%v", request.URL, request.Header, request.Body)
			}
			return executorCoreJSONResponse(request, http.StatusCreated, corecontract.IssueExecutorEnrollmentTokenResponse{
				ExecutorID: executorResourceExecutor, Token: validExecutorEnrollmentBearer(),
				ExpiresAt: now.Add(10 * time.Minute), Created: true,
			}), nil
		default:
			t.Fatalf("unexpected Core request %d", calls)
			return nil, nil
		}
	})}
	backend, err := NewCoreRunBackend("https://core.agentserver.local", client)
	if err != nil {
		t.Fatal(err)
	}
	created, err := backend.CreateExecutorResource(t.Context(), "browser-user-token", executorResourceWorkspace, corecontract.CreateExecutorResourceRequest{ExecutorID: executorResourceExecutor})
	if err != nil || !created.Created || created.Executor.ExecutorID != executorResourceExecutor {
		t.Fatalf("CreateExecutorResource() = %+v, %v", created, err)
	}
	issued, err := backend.IssueExecutorEnrollmentToken(t.Context(), "browser-user-token", executorResourceWorkspace, executorResourceExecutor, "enroll-1")
	if err != nil || !issued.Created || issued.Token == "" || calls != 2 {
		t.Fatalf("IssueExecutorEnrollmentToken() = %+v, %v; calls=%d", issued, err, calls)
	}
}

func TestCoreExecutorBackendRequiresNoStoreAndConsistentCreatedStatus(t *testing.T) {
	for name, response := range map[string]*http.Response{
		"missing no-store": browserJSONResponse(nil, http.StatusCreated, corecontract.CreateExecutorResourceResponse{Created: true}),
		"status mismatch":  executorCoreJSONResponse(nil, http.StatusOK, corecontract.CreateExecutorResourceResponse{Created: true}),
	} {
		t.Run(name, func(t *testing.T) {
			client := &http.Client{Transport: browserRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				response.Request = request
				return response, nil
			})}
			backend, _ := NewCoreRunBackend("https://core.agentserver.local", client)
			_, err := backend.CreateExecutorResource(t.Context(), "browser-user-token", executorResourceWorkspace, corecontract.CreateExecutorResourceRequest{ExecutorID: executorResourceExecutor})
			if err == nil {
				t.Fatal("invalid Core executor response was accepted")
			}
		})
	}
}

func executorCoreJSONResponse(request *http.Request, status int, value any) *http.Response {
	response := browserJSONResponse(request, status, value)
	response.Header.Set("Cache-Control", "no-store")
	return response
}

func TestCoreExecutorBackendDoesNotReflectEnrollmentBearerInErrors(t *testing.T) {
	client := &http.Client{Transport: browserRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return executorCoreJSONResponse(request, http.StatusForbidden, corecontract.PublicErrorResponse{
			Code: "forbidden", Message: "executor enrollment denied",
		}), nil
	})}
	backend, _ := NewCoreRunBackend("https://core.agentserver.local", client)
	secret := "browser-user-token-secret"
	_, err := backend.IssueExecutorEnrollmentToken(t.Context(), secret, executorResourceWorkspace, executorResourceExecutor, "enroll-1")
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("backend error = %v", err)
	}
}
