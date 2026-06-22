package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// CcClient calls cc-app-gateway's POST /api/turns from agentserver.
// Mirrors CodexClient (codex_client.go) — synchronous HTTP with 61-minute
// timeout (well above cc-app-gateway's 10-minute runner cap).
type CcClient struct {
	baseURL string
	secret  string
	http    *http.Client
}

// CcTurnRequest is the JSON body POSTed to cc-app-gateway /api/turns.
type CcTurnRequest struct {
	WorkspaceID string `json:"workspaceId"`
	SessionID   string `json:"sessionId"`
	UserMessage string `json:"userMessage"`
	Model       string `json:"model,omitempty"`
	TimeoutMs   int    `json:"timeoutMs,omitempty"`
}

// CcTurnResponse is the JSON body returned by cc-app-gateway on 200.
type CcTurnResponse struct {
	SessionID     string         `json:"sessionId"`
	AssistantText string         `json:"assistantText"`
	IsError       bool           `json:"isError"`
	ErrorMessage  string         `json:"errorMessage,omitempty"`
	DurationMs    int64          `json:"durationMs"`
	TotalCostUSD  float64        `json:"totalCostUsd"`
	ModelUsage    map[string]any `json:"modelUsage,omitempty"`
}

// NewCcClient constructs a CcClient with the given baseURL and internal secret.
// It trims trailing slashes from baseURL and uses a 61-minute HTTP timeout.
func NewCcClient(baseURL, secret string) *CcClient {
	return &CcClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		secret:  secret,
		http:    &http.Client{Timeout: 61 * time.Minute},
	}
}

// RunTurn POSTs the request to cc-app-gateway's /api/turns and decodes
// the response. Non-2xx responses are returned as errors with the body
// preview attached.
func (c *CcClient) RunTurn(ctx context.Context, req CcTurnRequest) (*CcTurnResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("CcClient.RunTurn marshal: %w", err)
	}
	hreq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/turns", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("CcClient.RunTurn build request: %w", err)
	}
	hreq.Header.Set("Content-Type", "application/json")
	if c.secret != "" {
		hreq.Header.Set("X-Internal-Secret", c.secret)
	}
	hresp, err := c.http.Do(hreq)
	if err != nil {
		return nil, fmt.Errorf("CcClient.RunTurn do: %w", err)
	}
	defer hresp.Body.Close()
	if hresp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(hresp.Body, 1024))
		return nil, fmt.Errorf("CcClient.RunTurn: status=%d body=%q", hresp.StatusCode, b)
	}
	var out CcTurnResponse
	if err := json.NewDecoder(hresp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("CcClient.RunTurn decode: %w", err)
	}
	return &out, nil
}

// ResolveCCAppGatewayRESTURL reads CC_APP_GATEWAY_REST_URL from env,
// trims trailing slash. Returns "" if unset (caller treats as disabled).
func ResolveCCAppGatewayRESTURL() string {
	return strings.TrimRight(strings.TrimSpace(os.Getenv("CC_APP_GATEWAY_REST_URL")), "/")
}
