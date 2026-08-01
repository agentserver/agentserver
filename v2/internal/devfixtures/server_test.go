package devfixtures

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/runcapability"
)

var fixtureTestNow = time.Date(2026, time.July, 31, 12, 0, 0, 0, time.UTC)

func TestHydraFixtureReturnsOnlyExactDevelopmentBrowserAuthority(t *testing.T) {
	runtime, _ := newTestRuntime(t)
	valid := introspectionRequest(t, runtime, string(runtime.bundle.browserToken))
	response := httptest.NewRecorder()
	runtime.serveHydra(response, valid)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("valid introspection status/headers = %d %v", response.Code, response.Header())
	}
	var active struct {
		Active   bool     `json:"active"`
		Subject  string   `json:"sub"`
		Audience []string `json:"aud"`
		Scope    string   `json:"scope"`
		Expires  int64    `json:"exp"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &active); err != nil {
		t.Fatal(err)
	}
	if !active.Active || active.Subject != runtime.bundle.document.Authority.ActorID ||
		len(active.Audience) != 1 || active.Audience[0] != BrowserTokenAudience || active.Scope != BrowserTokenScope ||
		active.Expires != fixtureTestNow.Add(15*time.Minute).Unix() {
		t.Fatalf("active introspection = %+v", active)
	}

	inactiveResponse := httptest.NewRecorder()
	runtime.serveHydra(inactiveResponse, introspectionRequest(t, runtime, "wrong-opaque-token"))
	if inactiveResponse.Code != http.StatusOK || strings.TrimSpace(inactiveResponse.Body.String()) != `{"active":false}` {
		t.Fatalf("inactive introspection = %d %q", inactiveResponse.Code, inactiveResponse.Body.String())
	}
}

func TestHydraFixtureRejectsAmbiguousProtocolInputs(t *testing.T) {
	runtime, _ := newTestRuntime(t)
	tests := []struct {
		name   string
		mutate func(*http.Request)
	}{
		{"method", func(request *http.Request) { request.Method = http.MethodGet }},
		{"path", func(request *http.Request) { request.URL.Path = "/oauth2/other" }},
		{"query", func(request *http.Request) { request.URL.RawQuery = "redirect=1" }},
		{"host", func(request *http.Request) { request.Host = "127.0.0.1:9" }},
		{"content type", func(request *http.Request) { request.Header.Set("Content-Type", "text/plain") }},
		{"accept", func(request *http.Request) { request.Header.Set("Accept", "*/*") }},
		{"duplicate content type", func(request *http.Request) { request.Header.Add("Content-Type", "application/x-www-form-urlencoded") }},
		{"chunked", func(request *http.Request) {
			request.TransferEncoding = []string{"chunked"}
			request.ContentLength = -1
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := introspectionRequest(t, runtime, string(runtime.bundle.browserToken))
			test.mutate(request)
			response := httptest.NewRecorder()
			runtime.serveHydra(response, request)
			if response.Code == http.StatusOK {
				t.Fatalf("ambiguous introspection was accepted: %s", response.Body.String())
			}
		})
	}

	body := "token=" + url.QueryEscape(string(runtime.bundle.browserToken)) + "&token=duplicate"
	request := httptest.NewRequest(http.MethodPost, runtime.bundle.hydraEndpoint.String(), strings.NewReader(body))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response := httptest.NewRecorder()
	runtime.serveHydra(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("duplicate form token status = %d", response.Code)
	}
}

func TestLLMProxyFixtureRunsListShellThenFinalPerCapability(t *testing.T) {
	runtime, codec := newTestRuntime(t)
	token := signTestCapability(t, codec, "70000000-0000-4000-8000-000000000007", "80000000-0000-4000-8000-000000000008", runcapability.AudienceLLMProxy, fixtureTestNow.Add(time.Hour), "gpt-5", "llmproxy")

	first := callLLMProxy(t, runtime, token, modelBody(`[]`))
	if first.Code != http.StatusOK || first.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("first scripted response = %d %s", first.Code, first.Body.String())
	}
	listCallID := responseCallID(t, first.Body.Bytes())
	if !strings.Contains(first.Body.String(), `"namespace":"executor"`) ||
		!strings.Contains(first.Body.String(), `"name":"list_environments"`) || listCallID == "" {
		t.Fatalf("first scripted response omitted executor call: %s", first.Body.String())
	}

	missing := callLLMProxy(t, runtime, token, modelBody(`[]`))
	if missing.Code != http.StatusConflict {
		t.Fatalf("missing tool output status = %d", missing.Code)
	}
	invalidEnvironmentInput := `[{"type":"function_call_output","call_id":` + mustJSONString(t, listCallID) + `,"output":"{\"environments\":[]}"}]`
	if invalid := callLLMProxy(t, runtime, token, modelBody(invalidEnvironmentInput)); invalid.Code != http.StatusConflict {
		t.Fatalf("invalid environment result status = %d", invalid.Code)
	}
	secondInput := functionOutputInput(t, listCallID, developmentEnvironmentOutput())
	second := callLLMProxy(t, runtime, token, modelBody(secondInput))
	shellCallID := responseCallID(t, second.Body.Bytes())
	if second.Code != http.StatusOK || shellCallID == "" || !strings.Contains(second.Body.String(), `"name":"shell"`) ||
		!strings.Contains(second.Body.String(), `\"argv\":[\"/bin/pwd\"]`) ||
		!strings.Contains(second.Body.String(), `\"environment_id\":\"60000000-0000-4000-8000-000000000006\"`) {
		t.Fatalf("shell scripted response = %d %s", second.Code, second.Body.String())
	}
	failedShellInput := functionOutputInput(t, shellCallID, `{"status":"failed","exit_code":1,"sandbox_denied":false,"timed_out":false,"output_complete":true}`)
	if failed := callLLMProxy(t, runtime, token, modelBody(failedShellInput)); failed.Code != http.StatusConflict {
		t.Fatalf("failed shell result status = %d", failed.Code)
	}
	thirdInput := functionOutputInput(t, shellCallID, developmentShellOutput())
	third := callLLMProxy(t, runtime, token, modelBody(thirdInput))
	if third.Code != http.StatusOK || !strings.Contains(third.Body.String(), "Agentserver v2 scripted development turn completed.") {
		t.Fatalf("final scripted response = %d %s", third.Code, third.Body.String())
	}
	exhausted := callLLMProxy(t, runtime, token, modelBody(thirdInput))
	if exhausted.Code != http.StatusConflict {
		t.Fatalf("exhausted scripted response status = %d", exhausted.Code)
	}
}

func TestLLMProxyFixtureCancellationHoldEndsOnlyWithRequestContext(t *testing.T) {
	runtime, codec := newTestRuntime(t)
	runtime.holdEntered = make(chan struct{}, 1)
	token := signTestCapability(t, codec, "70000000-0000-4000-8000-000000000087", "80000000-0000-4000-8000-000000000088", runcapability.AudienceLLMProxy, fixtureTestNow.Add(time.Hour), "gpt-5", "llmproxy")
	firstInput := `[{"role":"user","content":"cancel fixture ` + CancellationHoldMarker + `"}]`
	first := callLLMProxy(t, runtime, token, modelBody(firstInput))
	listCallID := responseCallID(t, first.Body.Bytes())
	if first.Code != http.StatusOK || listCallID == "" {
		t.Fatalf("cancellation script first response = %d %s", first.Code, first.Body.String())
	}
	second := callLLMProxy(t, runtime, token, modelBody(functionOutputInput(t, listCallID, developmentEnvironmentOutput())))
	shellCallID := responseCallID(t, second.Body.Bytes())
	if second.Code != http.StatusOK || shellCallID == "" {
		t.Fatalf("cancellation script shell response = %d %s", second.Code, second.Body.String())
	}

	ctx, cancel := context.WithCancel(t.Context())
	request := httptest.NewRequestWithContext(
		ctx,
		http.MethodPost,
		runtime.bundle.llmEndpoint.String()+"/responses",
		strings.NewReader(modelBody(functionOutputInput(t, shellCallID, developmentShellOutput()))),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		runtime.serveLLMProxy(response, request)
		close(done)
	}()
	select {
	case <-runtime.holdEntered:
	case <-time.After(time.Second):
		t.Fatal("cancellation model response did not enter its deterministic hold")
	}
	select {
	case <-done:
		t.Fatal("cancellation model response returned before request cancellation")
	default:
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancellation model response did not stop with its request context")
	}
	if response.Body.Len() != 0 {
		t.Fatalf("held model response wrote bytes after cancellation: %q", response.Body.String())
	}
}

func TestLLMProxyFixtureSeparatesConcurrentRunScripts(t *testing.T) {
	runtime, codec := newTestRuntime(t)
	tokens := []string{
		signTestCapability(t, codec, "70000000-0000-4000-8000-000000000017", "80000000-0000-4000-8000-000000000018", runcapability.AudienceLLMProxy, fixtureTestNow.Add(time.Hour), "gpt-5", "llmproxy"),
		signTestCapability(t, codec, "70000000-0000-4000-8000-000000000027", "80000000-0000-4000-8000-000000000028", runcapability.AudienceLLMProxy, fixtureTestNow.Add(time.Hour), "gpt-5", "llmproxy"),
	}
	listCallIDs := make([]string, len(tokens))
	var wait sync.WaitGroup
	for index := range tokens {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			response := callLLMProxy(t, runtime, tokens[index], modelBody(`[]`))
			if response.Code != http.StatusOK {
				t.Errorf("run %d first response = %d %s", index, response.Code, response.Body.String())
				return
			}
			listCallIDs[index] = responseCallID(t, response.Body.Bytes())
		}(index)
	}
	wait.Wait()
	if listCallIDs[0] == "" || listCallIDs[1] == "" || listCallIDs[0] == listCallIDs[1] {
		t.Fatalf("isolated list call IDs = %v", listCallIDs)
	}
	for index, token := range tokens {
		listInput := functionOutputInput(t, listCallIDs[index], developmentEnvironmentOutput())
		shell := callLLMProxy(t, runtime, token, modelBody(listInput))
		if shell.Code != http.StatusOK {
			t.Fatalf("run %d shell response = %d %s", index, shell.Code, shell.Body.String())
		}
		shellCallID := responseCallID(t, shell.Body.Bytes())
		finalInput := functionOutputInput(t, shellCallID, developmentShellOutput())
		if response := callLLMProxy(t, runtime, token, modelBody(finalInput)); response.Code != http.StatusOK {
			t.Fatalf("run %d final response = %d %s", index, response.Code, response.Body.String())
		}
	}
}

func TestLLMProxyFixtureRejectsWrongAudienceExpiryTamperingAndRoute(t *testing.T) {
	runtime, codec := newTestRuntime(t)
	valid := signTestCapability(t, codec, "70000000-0000-4000-8000-000000000037", "80000000-0000-4000-8000-000000000038", runcapability.AudienceLLMProxy, fixtureTestNow.Add(time.Hour), "gpt-5", "llmproxy")
	executor := signTestCapability(t, codec, "70000000-0000-4000-8000-000000000047", "80000000-0000-4000-8000-000000000048", runcapability.AudienceExecutorMCP, fixtureTestNow.Add(time.Hour), "", "")
	expired := signTestCapability(t, codec, "70000000-0000-4000-8000-000000000057", "80000000-0000-4000-8000-000000000058", runcapability.AudienceLLMProxy, fixtureTestNow.Add(-time.Millisecond), "gpt-5", "llmproxy")
	wrongProvider := signTestCapability(t, codec, "70000000-0000-4000-8000-000000000067", "80000000-0000-4000-8000-000000000068", runcapability.AudienceLLMProxy, fixtureTestNow.Add(time.Hour), "gpt-5", "other-provider")
	tampered := valid[:len(valid)-1] + map[bool]string{true: "A", false: "B"}[valid[len(valid)-1] != 'A']

	for name, token := range map[string]string{
		"executor audience": executor, "expired": expired, "tampered": tampered,
	} {
		t.Run(name, func(t *testing.T) {
			response := callLLMProxy(t, runtime, token, modelBody(`[]`))
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
			}
		})
	}
	if response := callLLMProxy(t, runtime, wrongProvider, modelBody(`[]`)); response.Code != http.StatusForbidden {
		t.Fatalf("wrong provider status = %d", response.Code)
	}
	if response := callLLMProxy(t, runtime, valid, strings.Replace(modelBody(`[]`), `"model":"gpt-5"`, `"model":"other"`, 1)); response.Code != http.StatusForbidden {
		t.Fatalf("wrong request model status = %d", response.Code)
	}
	duplicateModel := strings.Replace(modelBody(`[]`), `"model":"gpt-5"`, `"model":"gpt-5","model":"gpt-5"`, 1)
	if response := callLLMProxy(t, runtime, valid, duplicateModel); response.Code != http.StatusBadRequest {
		t.Fatalf("duplicate model status = %d", response.Code)
	}
	missingTool := strings.Replace(modelBody(`[]`), `"list_environments"`, `"read_file"`, 1)
	if response := callLLMProxy(t, runtime, valid, missingTool); response.Code != http.StatusConflict {
		t.Fatalf("missing scripted tool status = %d", response.Code)
	}
	if response := callLLMProxy(t, runtime, valid, modelBody(`[]`)); response.Code != http.StatusOK {
		t.Fatalf("rejected inputs advanced script state: %d %s", response.Code, response.Body.String())
	}
}

func TestLLMProxyFixtureRejectsAmbiguousHTTPInputs(t *testing.T) {
	runtime, codec := newTestRuntime(t)
	token := signTestCapability(t, codec, "70000000-0000-4000-8000-000000000077", "80000000-0000-4000-8000-000000000078", runcapability.AudienceLLMProxy, fixtureTestNow.Add(time.Hour), "gpt-5", "llmproxy")
	for _, test := range []struct {
		name   string
		mutate func(*http.Request)
	}{
		{"cleartext", func(request *http.Request) { request.TLS = nil }},
		{"method", func(request *http.Request) { request.Method = http.MethodGet }},
		{"path", func(request *http.Request) { request.URL.Path = "/v1/other" }},
		{"query", func(request *http.Request) { request.URL.RawQuery = "redirect=1" }},
		{"host", func(request *http.Request) { request.Host = "127.0.0.1:9" }},
		{"content type", func(request *http.Request) { request.Header.Set("Content-Type", "text/plain") }},
		{"duplicate content type", func(request *http.Request) { request.Header.Add("Content-Type", "application/json") }},
		{"duplicate authorization", func(request *http.Request) { request.Header.Add("Authorization", "Bearer "+token) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, runtime.bundle.llmEndpoint.String()+"/responses", strings.NewReader(modelBody(`[]`)))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer "+token)
			test.mutate(request)
			response := httptest.NewRecorder()
			runtime.serveLLMProxy(response, request)
			if response.Code == http.StatusOK {
				t.Fatalf("ambiguous model request was accepted: %s", response.Body.String())
			}
		})
	}
	if response := callLLMProxy(t, runtime, token, modelBody(`[]`)); response.Code != http.StatusOK {
		t.Fatalf("rejected HTTP inputs advanced script state: %d %s", response.Code, response.Body.String())
	}
}

func newTestRuntime(t *testing.T) (*fixtureRuntime, *runcapability.DevelopmentCodec) {
	t.Helper()
	hydraEndpoint, _ := url.Parse("http://127.0.0.1:17447/oauth2/introspect")
	llmEndpoint, _ := url.Parse("https://127.0.0.1:17448/v1")
	key := bytes.Repeat([]byte{0x42}, 32)
	codec, err := runcapability.NewDevelopmentCodec(key)
	if err != nil {
		t.Fatal(err)
	}
	bundle := &Bundle{
		document: ConfigDocument{
			Version: CurrentConfigVersion,
			Authority: AuthorityDocument{
				WorkspaceID: "40000000-0000-4000-8000-000000000004",
				SessionID:   "50000000-0000-4000-8000-000000000005",
				ActorID:     "10000000-0000-4000-8000-000000000001",
			},
			Hydra: HydraDocument{Audience: BrowserTokenAudience, Scope: BrowserTokenScope},
			LLMProxy: LLMProxyDocument{
				Model: "gpt-5", Provider: "llmproxy", ToolNamespace: ToolNamespace,
				ScriptedTool: ScriptedToolName, FinalMessage: "Agentserver v2 scripted development turn completed.",
			},
		},
		hydraEndpoint: hydraEndpoint, llmEndpoint: llmEndpoint, responseTTL: 15 * time.Minute,
		browserToken: []byte("asv2dev-browser-0123456789012345678901234567890123456789012"), codec: codec,
	}
	return &fixtureRuntime{bundle: bundle, now: func() time.Time { return fixtureTestNow }, sessions: make(map[scriptKey]scriptSession)}, codec
}

func introspectionRequest(t *testing.T, runtime *fixtureRuntime, token string) *http.Request {
	t.Helper()
	body := (url.Values{"token": []string{token}}).Encode()
	request := httptest.NewRequest(http.MethodPost, runtime.bundle.hydraEndpoint.String(), strings.NewReader(body))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	return request
}

func signTestCapability(
	t *testing.T,
	codec *runcapability.DevelopmentCodec,
	capabilityID, runID, audience string,
	expires time.Time,
	model, provider string,
) string {
	t.Helper()
	claims := runcapability.Claims{
		Version: runcapability.DevelopmentVersion, CapabilityID: capabilityID, Audience: audience,
		WorkspaceID: "40000000-0000-4000-8000-000000000004",
		SessionID:   "50000000-0000-4000-8000-000000000005", RunID: runID,
		RunAttemptID: "90000000-0000-4000-8000-000000000009", RunAttemptGeneration: 1,
		ActorID: "10000000-0000-4000-8000-000000000001", HolderID: "holder-test",
		IssuedAtUnixMS: fixtureTestNow.Add(-time.Minute).UnixMilli(), ExpiresAtUnixMS: expires.UnixMilli(),
	}
	if audience == runcapability.AudienceLLMProxy {
		claims.Model, claims.Provider = model, provider
	} else {
		claims.ExecutorID = "20000000-0000-4000-8000-000000000002"
		claims.ToolCatalogDigest = strings.Repeat("a", 64)
		claims.ExpectedRunVersion, claims.ExpectedRunAttemptVersion = 1, 1
	}
	token, err := codec.Sign(claims)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func modelBody(input string) string {
	return `{"model":"gpt-5","stream":true,"input":` + input + `,"tools":[{"type":"namespace","name":"executor","tools":[{"type":"function","name":"list_environments"},{"type":"function","name":"shell"}]}]}`
}

func developmentEnvironmentOutput() string {
	return `{"environments":[{"environment_id":"60000000-0000-4000-8000-000000000006"}]}`
}

func developmentShellOutput() string {
	return `{"status":"succeeded","exit_code":0,"sandbox_denied":false,"timed_out":false,"output_complete":true}`
}

func functionOutputInput(t *testing.T, callID, output string) string {
	t.Helper()
	return `[{"type":"function_call_output","call_id":` + mustJSONString(t, callID) + `,"output":` + mustJSONString(t, output) + `}]`
}

func callLLMProxy(t *testing.T, runtime *fixtureRuntime, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, runtime.bundle.llmEndpoint.String()+"/responses", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	runtime.serveLLMProxy(response, request)
	return response
}

func responseCallID(t *testing.T, body []byte) string {
	t.Helper()
	for _, line := range bytes.Split(body, []byte{'\n'}) {
		data, ok := bytes.CutPrefix(line, []byte("data: "))
		if !ok {
			continue
		}
		var event struct {
			Item struct {
				Type   string `json:"type"`
				CallID string `json:"call_id"`
			} `json:"item"`
		}
		if err := json.Unmarshal(data, &event); err != nil {
			t.Fatal(err)
		}
		if event.Item.Type == "function_call" {
			return event.Item.CallID
		}
	}
	return ""
}

func mustJSONString(t *testing.T, value string) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
