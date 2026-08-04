package browsergateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

func TestWorkspaceLLMGatewayProxyForwardsOnlyUserAuthorityAndBoundedJSON(t *testing.T) {
	workspaceID := "71000000-0000-4000-8000-000000000011"
	gatewayID := "71000000-0000-4000-8000-000000000012"
	client := &http.Client{Transport: browserRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.String() != "https://core.internal"+corecontract.AuthorizeLLMGatewayPath(workspaceID, gatewayID) ||
			request.Header.Get("Authorization") != "Bearer user-token" || request.Header.Get("Cookie") != "" ||
			request.Header.Get("Origin") != "" || request.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("proxied request = %s %s headers=%v", request.Method, request.URL, request.Header)
		}
		raw, err := io.ReadAll(request.Body)
		if err != nil || string(raw) != `{"browserBinding":"binding"}` {
			t.Fatalf("proxied body = %q, %v", raw, err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "Set-Cookie": []string{"forbidden=1"}},
			Body:       io.NopCloser(strings.NewReader(`{"gatewayId":"` + gatewayID + `","authorizationUrl":"https://idp.example/auth","expiresAt":"2026-08-02T00:00:00Z"}`)),
		}, nil
	})}
	proxy, err := NewWorkspaceLLMGatewayProxy("https://core.internal", client)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "https://browser.example"+corecontract.AuthorizeLLMGatewayPath(workspaceID, gatewayID), strings.NewReader(`{"browserBinding":"binding"}`))
	request.Header.Set("Authorization", "Bearer user-token")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Cookie", "browser=secret")
	request.Header.Set("Origin", "https://agent.example")
	response := httptest.NewRecorder()
	proxy.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "application/json" ||
		response.Header().Get("Set-Cookie") != "" || response.Header().Get("Cache-Control") != "no-store" ||
		!strings.Contains(response.Body.String(), gatewayID) {
		t.Fatalf("proxy response = %d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
	}
}

func TestWorkspaceLLMGatewayProxyRejectsInvalidAuthorityMethodAndResponse(t *testing.T) {
	proxy, err := NewWorkspaceLLMGatewayProxy("https://core.internal", &http.Client{Transport: browserRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/plain"}}, Body: io.NopCloser(strings.NewReader("not json"))}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	path := "/v2/workspaces/71000000-0000-4000-8000-000000000011/llm-gateways"
	for _, test := range []struct {
		method string
		bearer string
		status int
	}{
		{method: http.MethodDelete, bearer: "Bearer user-token", status: http.StatusMethodNotAllowed},
		{method: http.MethodGet, status: http.StatusUnauthorized},
		{method: http.MethodGet, bearer: "Bearer user-token", status: http.StatusBadGateway},
	} {
		request := httptest.NewRequest(test.method, "https://browser.example"+path, nil)
		if test.bearer != "" {
			request.Header.Set("Authorization", test.bearer)
		}
		response := httptest.NewRecorder()
		proxy.Routes().ServeHTTP(response, request)
		if response.Code != test.status || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s proxy response = %d headers=%v body=%q", test.method, response.Code, response.Header(), response.Body.String())
		}
	}
}

func TestWorkspaceLLMGatewayProxyForwardsGatewayPatch(t *testing.T) {
	workspaceID := "71000000-0000-4000-8000-000000000011"
	gatewayID := "71000000-0000-4000-8000-000000000012"
	client := &http.Client{Transport: browserRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPatch || request.URL.String() != "https://core.internal"+corecontract.WorkspaceLLMGatewayPath(workspaceID, gatewayID) {
			t.Fatalf("proxied update = %s %s", request.Method, request.URL)
		}
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{"gateway":{"gatewayId":"` + gatewayID + `"},"changed":true}`)),
		}, nil
	})}
	proxy, err := NewWorkspaceLLMGatewayProxy("https://core.internal", client)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPatch, "https://agent.example"+corecontract.WorkspaceLLMGatewayPath(workspaceID, gatewayID), strings.NewReader(`{"expectedVersion":1}`))
	request.Header.Set("Authorization", "Bearer user-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	proxy.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"changed":true`) {
		t.Fatalf("patch proxy response = %d %s", response.Code, response.Body.String())
	}
}
