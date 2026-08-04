package platformgateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

func TestResourceProxyForwardsOnlyBoundedPlatformRequest(t *testing.T) {
	workspaceID := "91000000-0000-4000-8000-000000000011"
	transport := resourceProxyRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPatch || request.URL.String() != "https://core.example"+corecontract.WorkspacePath(workspaceID) ||
			request.Header.Get("Authorization") != "Bearer opaque-user" || request.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("upstream request = %s %s headers=%v", request.Method, request.URL, request.Header)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "Cache-Control": []string{"no-store"}},
			Body:       io.NopCloser(strings.NewReader(`{"workspace":{"workspaceId":"` + workspaceID + `"},"changed":true}`)),
			Request:    request,
		}, nil
	})
	proxy, err := NewResourceProxy("https://core.example", &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPatch, corecontract.WorkspacePath(workspaceID), strings.NewReader(`{"name":"SG","expectedVersion":1}`))
	request.Header.Set("Authorization", "Bearer opaque-user")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	proxy.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), `"changed":true`) {
		t.Fatalf("proxy response = %d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
}

func TestResourceProxyRejectsWrongMethodAndMultipleBearers(t *testing.T) {
	proxy, err := NewResourceProxy("https://core.example", &http.Client{Transport: resourceProxyRoundTripper(func(*http.Request) (*http.Response, error) {
		t.Fatal("rejected request reached Core")
		return nil, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := "91000000-0000-4000-8000-000000000011"
	wrongMethod := httptest.NewRequest(http.MethodDelete, corecontract.WorkspacePath(workspaceID), nil)
	wrongMethod.Header.Set("Authorization", "Bearer user")
	wrongResponse := httptest.NewRecorder()
	proxy.Routes().ServeHTTP(wrongResponse, wrongMethod)
	if wrongResponse.Code != http.StatusMethodNotAllowed {
		t.Fatalf("wrong method = %d %s", wrongResponse.Code, wrongResponse.Body.String())
	}
	multiple := httptest.NewRequest(http.MethodGet, corecontract.WorkspacesPath(), nil)
	multiple.Header.Add("Authorization", "Bearer one")
	multiple.Header.Add("Authorization", "Bearer two")
	multipleResponse := httptest.NewRecorder()
	proxy.Routes().ServeHTTP(multipleResponse, multiple)
	if multipleResponse.Code != http.StatusUnauthorized {
		t.Fatalf("multiple bearer = %d %s", multipleResponse.Code, multipleResponse.Body.String())
	}
}

type resourceProxyRoundTripper func(*http.Request) (*http.Response, error)

func (roundTrip resourceProxyRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}
