package llmproxy

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

func TestCoreClientAuthorizesLLMProxyCapabilityOverExactHTTPContract(t *testing.T) {
	now := time.Date(2026, 8, 2, 16, 0, 0, 123_000_000, time.UTC)
	request := testCoreAuthorizationRequest()
	captured := make(chan capturedCoreRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, incoming *http.Request) {
		body, _ := io.ReadAll(incoming.Body)
		captured <- capturedCoreRequest{
			method: incoming.Method, path: incoming.URL.Path, rawQuery: incoming.URL.RawQuery,
			authorization: append([]string(nil), incoming.Header.Values("Authorization")...), body: body,
		}
		response.Header().Set("Content-Type", "application/json")
		response.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(response).Encode(testCoreAuthorizationResponse(request, now))
	}))
	defer server.Close()
	client, err := NewCoreClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.AuthorizeLLMProxyRunCapability(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.CapabilityID != request.CapabilityID || result.RunVersion != 5 || result.RunAttemptVersion != 6 ||
		!result.AuthorizedAt.Equal(now) {
		t.Fatalf("Core llmproxy authorization = %+v", result)
	}
	wire := <-captured
	if wire.method != http.MethodPost || wire.path != corecontract.AuthorizeLLMProxyRunCapabilityPath || wire.rawQuery != "" ||
		len(wire.authorization) != 1 || wire.authorization[0] != "Bearer "+request.Token {
		t.Fatalf("Core llmproxy HTTP framing = %+v", wire)
	}
	if bytes.Contains(wire.body, []byte(request.Token)) {
		t.Fatal("llmproxy capability bearer leaked into the Core JSON body")
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(wire.body, &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 5 || string(body["model"]) != `"`+testModel+`"` || string(body["provider"]) != `"`+testProvider+`"` ||
		string(body["llmGatewayId"]) != `"`+request.LLMGatewayID+`"` {
		t.Fatalf("Core llmproxy JSON body = %s", wire.body)
	}
}

func TestCoreClientRejectsInconsistentLLMProxyAuthorization(t *testing.T) {
	request := testCoreAuthorizationRequest()
	valid := testCoreAuthorizationResponse(request, time.Date(2026, 8, 2, 16, 30, 0, 0, time.UTC))
	tests := []struct {
		name   string
		mutate func(*corecontract.AuthorizeLLMProxyRunCapabilityResponse)
	}{
		{name: "capability", mutate: func(value *corecontract.AuthorizeLLMProxyRunCapabilityResponse) {
			value.CapabilityID = "97000000-0000-4000-8000-000000000099"
		}},
		{name: "audience", mutate: func(value *corecontract.AuthorizeLLMProxyRunCapabilityResponse) { value.Audience = "executor-mcp" }},
		{name: "run", mutate: func(value *corecontract.AuthorizeLLMProxyRunCapabilityResponse) {
			value.RunID = "97000000-0000-4000-8000-000000000099"
		}},
		{name: "attempt", mutate: func(value *corecontract.AuthorizeLLMProxyRunCapabilityResponse) {
			value.RunAttemptID = "97000000-0000-4000-8000-000000000099"
		}},
		{name: "generation", mutate: func(value *corecontract.AuthorizeLLMProxyRunCapabilityResponse) { value.RunAttemptGeneration++ }},
		{name: "run version", mutate: func(value *corecontract.AuthorizeLLMProxyRunCapabilityResponse) { value.RunVersion = 0 }},
		{name: "attempt version", mutate: func(value *corecontract.AuthorizeLLMProxyRunCapabilityResponse) { value.RunAttemptVersion = 1 << 53 }},
		{name: "authorization time", mutate: func(value *corecontract.AuthorizeLLMProxyRunCapabilityResponse) { value.AuthorizedAt = time.Time{} }},
		{name: "gateway", mutate: func(value *corecontract.AuthorizeLLMProxyRunCapabilityResponse) { value.LLMGatewayVersion++ }},
		{name: "upstream bearer", mutate: func(value *corecontract.AuthorizeLLMProxyRunCapabilityResponse) { value.UpstreamAuthorization = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			responseBody := valid
			test.mutate(&responseBody)
			server := testAuthorizationServer(http.StatusOK, "no-store", responseBody)
			defer server.Close()
			client, err := NewCoreClient(server.URL, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.AuthorizeLLMProxyRunCapability(t.Context(), request); err == nil {
				t.Fatal("inconsistent Core llmproxy authorization was accepted")
			}
		})
	}
}

func TestCoreClientLLMProxyAuthorizationFailsClosedWithoutLeaks(t *testing.T) {
	request := testCoreAuthorizationRequest()
	valid := testCoreAuthorizationResponse(request, time.Date(2026, 8, 2, 17, 0, 0, 0, time.UTC))
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
				response.Header().Set("Content-Type", "application/json")
				if test.cache != "" {
					response.Header().Set("Cache-Control", test.cache)
				}
				if test.redirectTo != "" {
					response.Header().Set("Location", test.redirectTo)
				}
				response.WriteHeader(test.status)
				if test.body != nil {
					_ = json.NewEncoder(response).Encode(test.body)
				}
			}))
			defer server.Close()
			client, err := NewCoreClient(server.URL, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.AuthorizeLLMProxyRunCapability(t.Context(), request)
			if err == nil || strings.Contains(err.Error(), request.Token) {
				t.Fatalf("fail-closed Core authorization error = %v", err)
			}
		})
	}

	server := testAuthorizationServer(http.StatusOK, "no-store", valid)
	client, err := NewCoreClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	server.Close()
	_, err = client.AuthorizeLLMProxyRunCapability(t.Context(), request)
	if err == nil || strings.Contains(err.Error(), request.Token) {
		t.Fatalf("unavailable Core authorization error = %v", err)
	}
}

func TestCoreClientRejectsCleartextNonLoopback(t *testing.T) {
	if _, err := NewCoreClient("http://core.internal:8080", http.DefaultClient); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("cleartext Core URL error = %v", err)
	}
}

func testAuthorizationServer(status int, cacheControl string, body corecontract.AuthorizeLLMProxyRunCapabilityResponse) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if cacheControl != "" {
			response.Header().Set("Cache-Control", cacheControl)
		}
		response.WriteHeader(status)
		_ = json.NewEncoder(response).Encode(body)
	}))
}

func testCoreAuthorizationRequest() RunCapabilityAuthorizationRequest {
	return RunCapabilityAuthorizationRequest{
		Token: "asv2cap1.key.claims.signature", CapabilityID: "97000000-0000-4000-8000-000000000001",
		Model: testModel, Provider: testProvider,
		LLMGatewayID: "97000000-0000-4000-8000-000000000007", LLMGatewayVersion: 2,
		LLMGatewayGrantUserID: "97000000-0000-4000-8000-000000000006",
		RunID:                 "97000000-0000-4000-8000-000000000004", RunAttemptID: "97000000-0000-4000-8000-000000000005",
		RunAttemptGeneration: 3,
	}
}

func testCoreAuthorizationResponse(request RunCapabilityAuthorizationRequest, now time.Time) corecontract.AuthorizeLLMProxyRunCapabilityResponse {
	return corecontract.AuthorizeLLMProxyRunCapabilityResponse{
		CapabilityID: request.CapabilityID, Audience: "llmproxy",
		RunID: request.RunID, RunAttemptID: request.RunAttemptID,
		RunAttemptGeneration: request.RunAttemptGeneration,
		RunVersion:           5, RunAttemptVersion: 6, AuthorizedAt: now,
		Model: request.Model, Provider: request.Provider,
		LLMGatewayID: request.LLMGatewayID, LLMGatewayVersion: request.LLMGatewayVersion,
		LLMGatewayGrantUserID: request.LLMGatewayGrantUserID,
		ResponsesURL:          "https://gateway.example.com/v1/responses",
		UpstreamAuthorization: "Bearer upstream-secret", BearerExpiresAt: now.Add(10 * time.Minute),
	}
}

type capturedCoreRequest struct {
	method        string
	path          string
	rawQuery      string
	authorization []string
	body          []byte
}
