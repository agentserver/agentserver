package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/internal/server"
)

func TestCcClient_RunTurn_HappyPath(t *testing.T) {
	var gotBody string
	var gotPath string
	var gotSecret string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotSecret = r.Header.Get("X-Internal-Secret")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"sessionId":     "abc-123",
			"assistantText": "pong",
			"isError":       false,
			"durationMs":    int64(42),
			"totalCostUsd":  0.0001,
		})
	}))
	defer ts.Close()

	c := server.NewCcClient(ts.URL, "secret123")
	resp, err := c.RunTurn(context.Background(), server.CcTurnRequest{
		WorkspaceID: "ws_test",
		SessionID:   "00000000-0000-4000-8000-000000000001",
		UserMessage: "hi",
	})
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if gotPath != "/api/turns" {
		t.Errorf("path: %q", gotPath)
	}
	if gotSecret != "secret123" {
		t.Errorf("secret: %q", gotSecret)
	}
	if !strings.Contains(gotBody, `"workspaceId":"ws_test"`) {
		t.Errorf("body missing workspaceId: %q", gotBody)
	}
	if resp.AssistantText != "pong" {
		t.Errorf("AssistantText: %q", resp.AssistantText)
	}
}

func TestCcClient_RunTurn_HTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"runner_failed","code":"runner_failed"}`))
	}))
	defer ts.Close()

	c := server.NewCcClient(ts.URL, "secret123")
	_, err := c.RunTurn(context.Background(), server.CcTurnRequest{
		WorkspaceID: "ws", SessionID: "00000000-0000-4000-8000-000000000001", UserMessage: "hi",
	})
	if err == nil {
		t.Fatal("expected error on 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should mention status: %v", err)
	}
}

func TestCcClient_RunTurn_DecodesErrorMessage(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"sessionId":     "abc",
			"assistantText": "",
			"isError":       true,
			"errorMessage":  "context window exceeded",
			"durationMs":    int64(1000),
		})
	}))
	defer ts.Close()

	c := server.NewCcClient(ts.URL, "")
	resp, err := c.RunTurn(context.Background(), server.CcTurnRequest{
		WorkspaceID: "ws", SessionID: "00000000-0000-4000-8000-000000000001", UserMessage: "hi",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.IsError {
		t.Error("IsError should be true")
	}
	if resp.ErrorMessage != "context window exceeded" {
		t.Errorf("ErrorMessage: got %q", resp.ErrorMessage)
	}
}

func TestResolveCCAppGatewayRESTURL_Trim(t *testing.T) {
	t.Setenv("CC_APP_GATEWAY_REST_URL", "http://cc-app-gateway.svc:8087/")
	got := server.ResolveCCAppGatewayRESTURL()
	if got != "http://cc-app-gateway.svc:8087" {
		t.Errorf("trailing slash not trimmed: %q", got)
	}
}

func TestResolveCCAppGatewayRESTURL_Empty(t *testing.T) {
	t.Setenv("CC_APP_GATEWAY_REST_URL", "")
	if server.ResolveCCAppGatewayRESTURL() != "" {
		t.Errorf("empty env should return empty string")
	}
}
