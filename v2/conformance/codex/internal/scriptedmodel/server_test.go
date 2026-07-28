package scriptedmodel

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
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

func newJSONRequest(body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "http://scripted.invalid/v1/responses", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	return request
}
