package executorgateway

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

func TestCoreConnectionClientAuthorizesExecutorCapabilityOverExactHTTPContract(t *testing.T) {
	now := time.Date(2026, 8, 2, 13, 0, 0, 123_000_000, time.UTC)
	request := productionExecutorCoreAuthorizationRequest()
	for _, test := range []struct {
		name           string
		runVersion     int64
		attemptVersion int64
	}{
		{name: "pre-turn", runVersion: request.ExpectedRunVersion - 1, attemptVersion: request.ExpectedRunAttemptVersion - 1},
		{name: "turn accepted", runVersion: request.ExpectedRunVersion, attemptVersion: request.ExpectedRunAttemptVersion},
	} {
		t.Run(test.name, func(t *testing.T) {
			captured := make(chan capturedExecutorCapabilityHTTPRequest, 1)
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, incoming *http.Request) {
				body, _ := io.ReadAll(incoming.Body)
				captured <- capturedExecutorCapabilityHTTPRequest{
					method: incoming.Method, path: incoming.URL.Path, rawQuery: incoming.URL.RawQuery,
					authorization: append([]string(nil), incoming.Header.Values("Authorization")...), body: body,
				}
				response.Header().Set("Content-Type", "application/json")
				response.Header().Set("Cache-Control", "no-store")
				_ = json.NewEncoder(response).Encode(corecontract.AuthorizeRunCapabilityResponse{
					CapabilityID: request.CapabilityID, Audience: "executor-mcp",
					RunID: request.RunID, RunAttemptID: request.RunAttemptID,
					RunAttemptGeneration: request.RunAttemptGeneration,
					RunVersion:           test.runVersion, RunAttemptVersion: test.attemptVersion,
					AuthorizedAt: now,
				})
			}))
			defer server.Close()
			client, err := NewCoreConnectionClient(server.URL, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.AuthorizeExecutorRunCapability(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			if result.RunVersion != test.runVersion || result.RunAttemptVersion != test.attemptVersion ||
				!result.AuthorizedAt.Equal(now) {
				t.Fatalf("authorization result = %+v", result)
			}
			wire := <-captured
			if wire.method != http.MethodPost || wire.path != corecontract.AuthorizeExecutorRunCapabilityPath || wire.rawQuery != "" ||
				len(wire.authorization) != 1 || wire.authorization[0] != "Bearer "+request.Token {
				t.Fatalf("authorization HTTP framing = %+v", wire)
			}
			if bytes.Contains(wire.body, []byte(request.Token)) {
				t.Fatal("executor capability bearer leaked into the Core JSON body")
			}
			var body map[string]json.RawMessage
			if err := json.Unmarshal(wire.body, &body); err != nil {
				t.Fatal(err)
			}
			if len(body) != 2 || string(body["executorId"]) != `"`+request.ExecutorID+`"` ||
				string(body["toolCatalogDigest"]) != `"`+request.ToolCatalogDigest+`"` {
				t.Fatalf("authorization JSON body = %s", wire.body)
			}
		})
	}
}

func TestCoreConnectionClientRejectsInconsistentExecutorCapabilityAuthorization(t *testing.T) {
	now := time.Date(2026, 8, 2, 13, 30, 0, 0, time.UTC)
	request := productionExecutorCoreAuthorizationRequest()
	valid := corecontract.AuthorizeRunCapabilityResponse{
		CapabilityID: request.CapabilityID, Audience: "executor-mcp",
		RunID: request.RunID, RunAttemptID: request.RunAttemptID,
		RunAttemptGeneration: request.RunAttemptGeneration,
		RunVersion:           request.ExpectedRunVersion, RunAttemptVersion: request.ExpectedRunAttemptVersion,
		AuthorizedAt: now,
	}
	tests := []struct {
		name   string
		mutate func(*corecontract.AuthorizeRunCapabilityResponse)
	}{
		{name: "capability", mutate: func(value *corecontract.AuthorizeRunCapabilityResponse) {
			value.CapabilityID = "97000000-0000-4000-8000-000000000099"
		}},
		{name: "audience", mutate: func(value *corecontract.AuthorizeRunCapabilityResponse) { value.Audience = "llmproxy" }},
		{name: "run", mutate: func(value *corecontract.AuthorizeRunCapabilityResponse) {
			value.RunID = "97000000-0000-4000-8000-000000000099"
		}},
		{name: "attempt", mutate: func(value *corecontract.AuthorizeRunCapabilityResponse) {
			value.RunAttemptID = "97000000-0000-4000-8000-000000000099"
		}},
		{name: "generation", mutate: func(value *corecontract.AuthorizeRunCapabilityResponse) { value.RunAttemptGeneration++ }},
		{name: "run version", mutate: func(value *corecontract.AuthorizeRunCapabilityResponse) { value.RunVersion -= 2 }},
		{name: "attempt version", mutate: func(value *corecontract.AuthorizeRunCapabilityResponse) { value.RunAttemptVersion -= 2 }},
		{name: "authorization time", mutate: func(value *corecontract.AuthorizeRunCapabilityResponse) { value.AuthorizedAt = time.Time{} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			responseBody := valid
			test.mutate(&responseBody)
			server := executorCapabilityAuthorizationServer(t, http.StatusOK, "no-store", responseBody)
			defer server.Close()
			client, err := NewCoreConnectionClient(server.URL, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.AuthorizeExecutorRunCapability(t.Context(), request); err == nil {
				t.Fatal("inconsistent Core authorization was accepted")
			}
		})
	}
}

func TestCoreConnectionClientExecutorCapabilityAuthorizationFailsClosedWithoutLeaks(t *testing.T) {
	request := productionExecutorCoreAuthorizationRequest()
	valid := corecontract.AuthorizeRunCapabilityResponse{
		CapabilityID: request.CapabilityID, Audience: "executor-mcp",
		RunID: request.RunID, RunAttemptID: request.RunAttemptID,
		RunAttemptGeneration: request.RunAttemptGeneration,
		RunVersion:           request.ExpectedRunVersion, RunAttemptVersion: request.ExpectedRunAttemptVersion,
		AuthorizedAt: time.Date(2026, 8, 2, 14, 0, 0, 0, time.UTC),
	}
	for _, test := range []struct {
		name       string
		status     int
		cache      string
		body       any
		redirectTo string
	}{
		{name: "cacheable success", status: http.StatusOK, body: valid},
		{name: "Core denial reflecting bearer", status: http.StatusForbidden, cache: "no-store", body: corecontract.ErrorResponse{Code: "forbidden", Message: request.Token}},
		{name: "redirect", status: http.StatusTemporaryRedirect, cache: "no-store", redirectTo: "https://other.example.test/steal"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				if test.cache != "" {
					response.Header().Set("Cache-Control", test.cache)
				}
				response.Header().Set("Content-Type", "application/json")
				if test.redirectTo != "" {
					response.Header().Set("Location", test.redirectTo)
				}
				response.WriteHeader(test.status)
				if test.body != nil {
					_ = json.NewEncoder(response).Encode(test.body)
				}
			}))
			defer server.Close()
			client, err := NewCoreConnectionClient(server.URL, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.AuthorizeExecutorRunCapability(t.Context(), request)
			if err == nil || strings.Contains(err.Error(), request.Token) {
				t.Fatalf("fail-closed authorization error = %v", err)
			}
		})
	}

	server := executorCapabilityAuthorizationServer(t, http.StatusOK, "no-store", valid)
	client, err := NewCoreConnectionClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	server.Close()
	_, err = client.AuthorizeExecutorRunCapability(t.Context(), request)
	if err == nil || strings.Contains(err.Error(), request.Token) {
		t.Fatalf("unavailable Core authorization error = %v", err)
	}
}

func executorCapabilityAuthorizationServer(
	t *testing.T,
	status int,
	cacheControl string,
	body corecontract.AuthorizeRunCapabilityResponse,
) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if cacheControl != "" {
			response.Header().Set("Cache-Control", cacheControl)
		}
		response.WriteHeader(status)
		_ = json.NewEncoder(response).Encode(body)
	}))
}

func productionExecutorCoreAuthorizationRequest() ExecutorRunCapabilityAuthorizationRequest {
	return ExecutorRunCapabilityAuthorizationRequest{
		Token: "asv2cap1.key.claims.signature", CapabilityID: "97000000-0000-4000-8000-000000000001",
		ExecutorID: testProductionCapabilityExecutor, ToolCatalogDigest: strings.Repeat("a", 64),
		RunID: "97000000-0000-4000-8000-000000000004", RunAttemptID: "97000000-0000-4000-8000-000000000005",
		RunAttemptGeneration: 3, ExpectedRunVersion: 5, ExpectedRunAttemptVersion: 6,
	}
}

type capturedExecutorCapabilityHTTPRequest struct {
	method        string
	path          string
	rawQuery      string
	authorization []string
	body          []byte
}
