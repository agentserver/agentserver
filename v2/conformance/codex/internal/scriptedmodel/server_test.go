package scriptedmodel

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestServerCapturesBoundedResponsesRequest(t *testing.T) {
	response, err := AssistantMessage("response-1", "message-1", "complete")
	if err != nil {
		t.Fatal(err)
	}
	server, err := newServer(Config{Responses: []Response{response}})
	if err != nil {
		t.Fatal(err)
	}

	request := newJSONRequest(`{"model":"mock"}`)
	recorder := httptest.NewRecorder()
	server.serveHTTP(recorder, request)
	httpResponse := recorder.Result()
	defer httpResponse.Body.Close()
	body, err := io.ReadAll(httpResponse.Body)
	if err != nil {
		t.Fatal(err)
	}
	if httpResponse.StatusCode != http.StatusOK || !bytes.Contains(body, []byte("response.completed")) {
		t.Fatalf("unexpected scripted response: status=%d body=%q", httpResponse.StatusCode, body)
	}
	requests := server.Requests()
	if len(requests) != 1 || string(requests[0].Body) != `{"model":"mock"}` {
		t.Fatalf("captured requests = %+v", requests)
	}
	if failures := server.Failures(); len(failures) != 0 {
		t.Fatalf("unexpected server failures: %v", failures)
	}
}

func TestServerHoldsResponseUntilClientCancellation(t *testing.T) {
	server, err := Start(Config{Responses: []Response{{HoldOpen: true}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)

	requestContext, cancelRequest := context.WithCancel(context.Background())
	defer cancelRequest()
	request, err := http.NewRequestWithContext(
		requestContext,
		http.MethodPost,
		server.URL()+"/v1/responses",
		bytes.NewBufferString(`{"model":"mock"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	result := make(chan error, 1)
	go func() {
		response, requestErr := http.DefaultClient.Do(request)
		if response != nil {
			_ = response.Body.Close()
		}
		result <- requestErr
	}()

	waitContext, cancelWait := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelWait()
	if err := server.WaitForRequests(waitContext, 1); err != nil {
		t.Fatalf("wait for held request: %v", err)
	}
	select {
	case err := <-result:
		t.Fatalf("held response completed before cancellation: %v", err)
	default:
	}
	cancelRequest()
	cancelContext, cancelCancellationWait := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelCancellationWait()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("held request cancellation error = %v, want context canceled", err)
		}
	case <-cancelContext.Done():
		t.Fatalf("held request did not stop after cancellation: %v", cancelContext.Err())
	}
	if requests := server.Requests(); len(requests) != 1 {
		t.Fatalf("held request count = %d, want one", len(requests))
	}
	if failures := server.Failures(); len(failures) != 0 {
		t.Fatalf("held response failures: %v", failures)
	}
}

func TestServerReturnsScriptedRedirect(t *testing.T) {
	const target = "http://127.0.0.1:43210/v1/responses"
	server, err := newServer(Config{Responses: []Response{{
		StatusCode:  http.StatusTemporaryRedirect,
		RedirectURL: target,
	}}})
	if err != nil {
		t.Fatal(err)
	}

	request := newJSONRequest(`{"model":"mock"}`)
	recorder := httptest.NewRecorder()
	server.serveHTTP(recorder, request)
	response := recorder.Result()
	defer response.Body.Close()
	if response.StatusCode != http.StatusTemporaryRedirect || response.Header.Get("Location") != target {
		t.Fatalf("redirect response: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	if body, err := io.ReadAll(response.Body); err != nil || len(body) != 0 {
		t.Fatalf("redirect body = %q, err=%v; want empty", body, err)
	}
	if requests := server.Requests(); len(requests) != 1 {
		t.Fatalf("redirect request count = %d, want one", len(requests))
	}
}

func TestServerRejectsInvalidScriptedRedirect(t *testing.T) {
	_, err := newServer(Config{Responses: []Response{{
		StatusCode:  http.StatusOK,
		RedirectURL: "http://127.0.0.1:43210/v1/responses",
	}}})
	if err == nil || !strings.Contains(err.Error(), "unsupported status") {
		t.Fatalf("newServer() error = %v, want redirect status validation", err)
	}
}

func TestServerRejectsOversizedRequest(t *testing.T) {
	response, err := AssistantMessage("response-1", "message-1", "complete")
	if err != nil {
		t.Fatal(err)
	}
	server, err := newServer(Config{MaxRequestBytes: 4, Responses: []Response{response}})
	if err != nil {
		t.Fatal(err)
	}

	request := newJSONRequest(`{"too":"large"}`)
	recorder := httptest.NewRecorder()
	server.serveHTTP(recorder, request)
	httpResponse := recorder.Result()
	defer httpResponse.Body.Close()
	if httpResponse.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", httpResponse.StatusCode, http.StatusRequestEntityTooLarge)
	}
	if len(server.Requests()) != 0 || len(server.Failures()) != 1 {
		t.Fatalf("requests=%d failures=%v", len(server.Requests()), server.Failures())
	}
}

func TestServerFailsClosedWhenScriptIsExhausted(t *testing.T) {
	response, err := AssistantMessage("response-1", "message-1", "complete")
	if err != nil {
		t.Fatal(err)
	}
	server, err := newServer(Config{Responses: []Response{response}})
	if err != nil {
		t.Fatal(err)
	}

	for index, wantStatus := range []int{http.StatusOK, http.StatusInternalServerError} {
		request := newJSONRequest(`{"model":"mock"}`)
		recorder := httptest.NewRecorder()
		server.serveHTTP(recorder, request)
		httpResponse := recorder.Result()
		_ = httpResponse.Body.Close()
		if httpResponse.StatusCode != wantStatus {
			t.Fatalf("request %d status = %d, want %d", index, httpResponse.StatusCode, wantStatus)
		}
	}
	if len(server.Requests()) != 2 || len(server.Failures()) != 1 {
		t.Fatalf("requests=%d failures=%v", len(server.Requests()), server.Failures())
	}
}

func TestFunctionCallRejectsMalformedArguments(t *testing.T) {
	if _, err := FunctionCall("response-1", "call-1", "update_plan", `{`); err == nil {
		t.Fatal("FunctionCall accepted malformed JSON arguments")
	}
	response, err := FunctionCall("response-1", "call-1", "update_plan", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(response.Body, []byte(`"type":"function_call"`)) ||
		!bytes.Contains(response.Body, []byte(`"name":"update_plan"`)) {
		t.Fatalf("unexpected function call response: %q", response.Body)
	}
}

func TestNamespacedFunctionCallIncludesNamespace(t *testing.T) {
	response, err := NamespacedFunctionCall(
		"response-1",
		"call-1",
		"mcp__executor",
		"approved_echo",
		`{"message":"hello"}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(response.Body, []byte(`"namespace":"mcp__executor"`)) ||
		!bytes.Contains(response.Body, []byte(`"name":"approved_echo"`)) {
		t.Fatalf("unexpected namespaced function call response: %q", response.Body)
	}
	if _, err := NamespacedFunctionCall("response-1", "call-1", "", "approved_echo", `{}`); err == nil {
		t.Fatal("NamespacedFunctionCall accepted an empty namespace")
	}
}

func newJSONRequest(body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "http://scripted.invalid/v1/responses", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	return request
}
