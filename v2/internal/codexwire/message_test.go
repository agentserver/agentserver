package codexwire

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseMessageKinds(t *testing.T) {
	tests := []struct {
		name  string
		frame string
		kind  Kind
	}{
		{name: "integer request", frame: `{"id":1,"method":"initialize","params":{}}`, kind: KindRequest},
		{name: "string request", frame: `{"id":"approval-1","method":"approval/respond"}`, kind: KindRequest},
		{name: "notification", frame: `{"method":"initialized"}`, kind: KindNotification},
		{name: "response", frame: `{"id":1,"result":null}`, kind: KindResponse},
		{name: "error", frame: `{"id":1,"error":{"code":-32601,"message":"unknown method"}}`, kind: KindError},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			message, err := Parse([]byte(test.frame))
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if message.Kind != test.kind {
				t.Fatalf("kind = %s, want %s", message.Kind, test.kind)
			}
		})
	}
}

func TestParseRejectsInvalidDialectAndEnvelope(t *testing.T) {
	tests := []struct {
		name    string
		frame   string
		wantErr string
	}{
		{name: "standard jsonrpc", frame: `{"jsonrpc":"2.0","id":1,"result":{}}`, wantErr: "omit the jsonrpc"},
		{name: "duplicate nested key", frame: `{"id":1,"method":"x","params":{"path":"a","path":"b"}}`, wantErr: "duplicate JSON object key"},
		{name: "fractional id", frame: `{"id":1.5,"method":"x"}`, wantErr: "signed 64-bit integer"},
		{name: "null id", frame: `{"id":null,"method":"x"}`, wantErr: "string or signed"},
		{name: "method and result", frame: `{"id":1,"method":"x","result":{}}`, wantErr: "cannot contain result"},
		{name: "missing response body", frame: `{"id":1}`, wantErr: "exactly one"},
		{name: "both response bodies", frame: `{"id":1,"result":{},"error":{"code":1,"message":"bad"}}`, wantErr: "exactly one"},
		{name: "empty method", frame: `{"method":""}`, wantErr: "non-empty string"},
		{name: "array", frame: `[]`, wantErr: "JSON object"},
		{name: "multiple values", frame: `{"method":"one"} {"method":"two"}`, wantErr: "more than one"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse([]byte(test.frame))
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Parse() error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}

func TestParseEnforcesJSONNodeLimit(t *testing.T) {
	_, err := parseWithNodeLimit([]byte(`{"method":"x","params":[1,2,3]}`), 4)
	if err == nil || !strings.Contains(err.Error(), "limit of 4 values") {
		t.Fatalf("parseWithNodeLimit() error = %v, want node limit", err)
	}
}

func TestDecodeResult(t *testing.T) {
	message, err := Parse([]byte(`{"id":1,"result":{"sessionId":"session-1"}}`))
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		SessionID string `json:"sessionId"`
	}
	if err := message.DecodeResult(&result); err != nil {
		t.Fatal(err)
	}
	if result.SessionID != "session-1" {
		t.Fatalf("session id = %q", result.SessionID)
	}
	if !json.Valid(message.Raw) {
		t.Fatal("raw message is not valid JSON")
	}
}
