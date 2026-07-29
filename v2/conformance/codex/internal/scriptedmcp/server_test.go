package scriptedmcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestServerImplementsBoundedToolLifecycle(t *testing.T) {
	server, err := Start(Config{
		Tools: []Tool{
			{
				Name:        "approved_echo",
				Description: "Echo an approved message.",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"}},"required":["message"],"additionalProperties":false}`),
				Annotations: json.RawMessage(`{"readOnlyHint":false,"destructiveHint":true,"openWorldHint":true}`),
			},
		},
		ExpectedCalls: []ExpectedCall{
			{
				Name:      "approved_echo",
				Arguments: json.RawMessage(`{"message":"hello"}`),
				Result:    json.RawMessage(`{"content":[{"type":"text","text":"echo: hello"}],"structuredContent":{"echoed":"hello"},"isError":false}`),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)

	initialize := post(t, server.URL(), `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0"}}}`)
	assertResultID(t, initialize, "1")
	initialized := post(t, server.URL(), `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if initialized.StatusCode != http.StatusAccepted {
		t.Fatalf("initialized status = %d", initialized.StatusCode)
	}
	_ = initialized.Body.Close()

	listed := post(t, server.URL(), `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	listedBody := decodeBody(t, listed)
	if !bytes.Contains(listedBody, []byte(`"name":"approved_echo"`)) ||
		!bytes.Contains(listedBody, []byte(`"destructiveHint":true`)) {
		t.Fatalf("tools/list response omitted approved tool: %s", listedBody)
	}

	called := post(t, server.URL(), `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"approved_echo","arguments":{"message":"hello"},"_meta":{"threadId":"thread-1"}}}`)
	calledBody := decodeBody(t, called)
	if !bytes.Contains(calledBody, []byte(`"text":"echo: hello"`)) {
		t.Fatalf("tools/call response omitted result: %s", calledBody)
	}
	if failures := server.Failures(); len(failures) != 0 {
		t.Fatalf("server failures: %v", failures)
	}
	requests := server.Requests()
	if len(requests) != 4 {
		t.Fatalf("recorded requests = %d, want 4", len(requests))
	}
	wantMethods := []string{"initialize", "notifications/initialized", "tools/list", "tools/call"}
	for index, want := range wantMethods {
		if requests[index].RPCMethod != want {
			t.Fatalf("request %d method = %q, want %q", index, requests[index].RPCMethod, want)
		}
	}
	calls := server.Calls()
	if len(calls) != 1 || calls[0].Name != "approved_echo" || !bytes.Contains(calls[0].Meta, []byte("thread-1")) {
		t.Fatalf("unexpected calls: %+v", calls)
	}
}

func TestServerFailsClosedOnUnsupportedMethod(t *testing.T) {
	server, err := Start(Config{
		Tools: []Tool{{Name: "approved_echo", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)

	response := post(t, server.URL(), `{"jsonrpc":"2.0","id":1,"method":"resources/list","params":{}}`)
	body := decodeBody(t, response)
	if !bytes.Contains(body, []byte(`"code":-32601`)) {
		t.Fatalf("unsupported method response = %s", body)
	}
	if failures := server.Failures(); len(failures) != 1 {
		t.Fatalf("failures = %v, want one", failures)
	}
}

func TestServerRoundTripsElicitationWithinToolCall(t *testing.T) {
	server, err := Start(Config{
		Tools: []Tool{{
			Name:        "approved_echo",
			Description: "Echo after a client decision.",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}},
		ExpectedCalls: []ExpectedCall{{
			Name:      "approved_echo",
			Arguments: json.RawMessage(`{"message":"hello"}`),
			Result:    json.RawMessage(`{"content":[{"type":"text","text":"accepted"}],"isError":false}`),
			Elicitation: &ExpectedElicitation{
				ID:       json.RawMessage(`"elicitation-1"`),
				Params:   json.RawMessage(`{"message":"Allow?","requestedSchema":{"type":"object","properties":{"confirmed":{"type":"boolean"}},"required":["confirmed"]}}`),
				Response: json.RawMessage(`{"action":"accept","content":{"confirmed":true}}`),
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)

	initialize := post(t, server.URL(), `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	assertResultID(t, initialize, "1")
	initialized := post(t, server.URL(), `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	_ = initialized.Body.Close()

	callRequest, err := http.NewRequest(
		http.MethodPost,
		server.URL(),
		bytes.NewBufferString(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"approved_echo","arguments":{"message":"hello"}}}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	callRequest.Header.Set("Content-Type", "application/json")
	callRequest.Header.Set("Accept", "application/json, text/event-stream")
	callResponse, err := http.DefaultClient.Do(callRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer callResponse.Body.Close()
	if callResponse.StatusCode != http.StatusOK ||
		callResponse.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("eliciting tools/call response = %d %q", callResponse.StatusCode, callResponse.Header.Get("Content-Type"))
	}
	reader := bufio.NewReader(callResponse.Body)
	eventLine, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	dataLine, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	blankLine, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if eventLine != "event: message\n" || blankLine != "\n" ||
		!bytes.Contains([]byte(dataLine), []byte(`"method":"elicitation/create"`)) ||
		!bytes.Contains([]byte(dataLine), []byte(`"id":"elicitation-1"`)) {
		t.Fatalf("unexpected elicitation SSE event: %q%q%q", eventLine, dataLine, blankLine)
	}

	decision := post(t, server.URL(), `{"jsonrpc":"2.0","id":"elicitation-1","result":{"action":"accept","content":{"confirmed":true}}}`)
	if decision.StatusCode != http.StatusAccepted {
		t.Fatalf("elicitation response status = %d, want %d", decision.StatusCode, http.StatusAccepted)
	}
	_ = decision.Body.Close()
	remaining, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(remaining, []byte(`"id":2`)) ||
		!bytes.Contains(remaining, []byte(`"text":"accepted"`)) {
		t.Fatalf("tools/call SSE result = %s", remaining)
	}
	if failures := server.Failures(); len(failures) != 0 {
		t.Fatalf("server failures: %v", failures)
	}
	responses := server.ElicitationResponses()
	if len(responses) != 1 || string(responses[0].ID) != `"elicitation-1"` ||
		!bytes.Contains(responses[0].Result, []byte(`"action":"accept"`)) {
		t.Fatalf("unexpected elicitation responses: %+v", responses)
	}
}

func TestServerRejectsInvalidConfiguration(t *testing.T) {
	if _, err := newServer(Config{}); err == nil {
		t.Fatal("server accepted an empty tool set")
	}
	if _, err := newServer(Config{Tools: []Tool{
		{Name: "duplicate", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "duplicate", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}}); err == nil {
		t.Fatal("server accepted duplicate tool names")
	}
	if _, err := newServer(Config{Tools: []Tool{
		{Name: "bad", InputSchema: json.RawMessage(`[]`)},
	}}); err == nil {
		t.Fatal("server accepted a non-object input schema")
	}
	if _, err := newServer(Config{Tools: []Tool{
		{Name: "bad", InputSchema: json.RawMessage(`{"type":"object"}`), Annotations: json.RawMessage(`[]`)},
	}}); err == nil {
		t.Fatal("server accepted non-object tool annotations")
	}
	if _, err := newServer(Config{
		Tools: []Tool{{Name: "bad", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		ExpectedCalls: []ExpectedCall{{
			Name:      "bad",
			Arguments: json.RawMessage(`{}`),
			Result:    json.RawMessage(`{}`),
			Elicitation: &ExpectedElicitation{
				ID:       json.RawMessage(`null`),
				Params:   json.RawMessage(`{}`),
				Response: json.RawMessage(`{}`),
			},
		}},
	}); err == nil {
		t.Fatal("server accepted an invalid elicitation id")
	}
}

func post(t *testing.T, url, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, url, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func assertResultID(t *testing.T, response *http.Response, want string) {
	t.Helper()
	body := decodeBody(t, response)
	var envelope struct {
		ID json.RawMessage `json:"id"`
	}
	if json.Unmarshal(body, &envelope) != nil || string(envelope.ID) != want {
		t.Fatalf("response id = %s, want %s; body=%s", envelope.ID, want, body)
	}
}

func decodeBody(t *testing.T, response *http.Response) []byte {
	t.Helper()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("response status = %d; body=%s", response.StatusCode, body)
	}
	return body
}
