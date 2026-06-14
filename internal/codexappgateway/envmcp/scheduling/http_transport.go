package scheduling

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// HTTPTransport implements ScheduleTransport by calling agentserver-main
// directly at /api/internal/workspaces/{wid}/scheduled-tasks/*, presenting
// the workspace cap-token as Authorization: Bearer.
//
// Replaces the pre-2026-06-14 LoopbackTransport which POSTed all six MCP
// actions to codex-app-gateway's loopback /internal/scheduled-tasks/<action>
// handler — that handler reverse-looked-up workspace_id from a per-spawn
// loopback token, translated MCP actions into real REST verbs/paths, and
// forwarded with X-Internal-Secret. Now that agentserver-main verifies
// cap-tokens directly, we cut out the loopback hop entirely: env-mcp does
// the action→verb/path translation in-process, ships the cap-token bearer,
// and the server validates both the token signature and that the URL
// {wid} equals the token's workspace_id.
type HTTPTransport struct {
	// AgentserverBaseURL is the http(s) base for agentserver-main's
	// internal API, e.g. "http://release-agentserver:8080". The
	// /api/internal/workspaces/{wid}/scheduled-tasks/... suffix is
	// appended per action.
	AgentserverBaseURL string

	// WorkspaceID is the workspace this env-mcp instance is bound to
	// (set at spawn time). Used to build URL paths and as the source
	// of truth the server cross-checks against the cap-token payload.
	WorkspaceID string

	// CapToken is the workspace cap-token shipped as Authorization:
	// Bearer. Same token env-mcp uses for /bridge and for the
	// connected-list lookup against codex-exec-gateway — one token
	// authorises every workspace-scoped operation the LLM can take.
	CapToken string

	HTTP *http.Client
}

// NewHTTPTransport wraps an http.Client with the per-spawn config.
// http may be nil; the default sets a 10s timeout (matches the
// prior LoopbackTransport so existing reasoning about tail latency
// carries over).
func NewHTTPTransport(agentserverBaseURL, workspaceID, capToken string, httpClient *http.Client) *HTTPTransport {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &HTTPTransport{
		AgentserverBaseURL: strings.TrimRight(agentserverBaseURL, "/"),
		WorkspaceID:        workspaceID,
		CapToken:           capToken,
		HTTP:               httpClient,
	}
}

// Call dispatches one MCP scheduling action against the agentserver-main
// REST surface. The mapping below mirrors what
// codex-app-gateway/internal_api.go::handleInternalScheduledTask used to
// do — the in-process translation is the only thing that moved across
// the wire boundary; the actual REST endpoints on agentserver-main are
// unchanged.
func (t *HTTPTransport) Call(ctx context.Context, action string, body any) (json.RawMessage, error) {
	raw, _ := json.Marshal(body)
	raw = injectTimezone(raw)

	method, path, bodyToSend, err := t.routeAction(action, raw)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, method, t.AgentserverBaseURL+path, bodyToSend)
	if err != nil {
		return nil, err
	}
	if bodyToSend != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+t.CapToken)
	req.Header.Set("Accept", "application/json")

	resp, err := t.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("scheduling %s: %w", action, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("scheduling %s: status %d: %s", action, resp.StatusCode, string(out))
	}
	return out, nil
}

// routeAction maps an MCP action name to the corresponding REST
// method/path on agentserver-main. For actions that target a single
// task (cancel/pause/resume/update) the taskId is extracted from the
// JSON body — same convention LoopbackTransport relied on. list takes
// an optional status filter from the body and translates POST→GET
// with a query string (the MCP wire is POST-only).
//
// Returns (method, path, body, err). When body is nil the request is
// sent without a Content-Type header.
func (t *HTTPTransport) routeAction(action string, body json.RawMessage) (string, string, io.Reader, error) {
	base := "/api/internal/workspaces/" + t.WorkspaceID + "/scheduled-tasks"
	switch action {
	case "schedule":
		return http.MethodPost, base, bytes.NewReader(body), nil
	case "list":
		var v struct {
			Status string `json:"status"`
		}
		_ = json.Unmarshal(body, &v)
		p := base
		if v.Status != "" {
			p += "?status=" + url.QueryEscape(v.Status)
		}
		return http.MethodGet, p, nil, nil
	case "cancel", "pause", "resume":
		var v struct {
			TaskID string `json:"taskId"`
		}
		_ = json.Unmarshal(body, &v)
		if v.TaskID == "" {
			return "", "", nil, fmt.Errorf("taskId required for %s", action)
		}
		return http.MethodPost, fmt.Sprintf("%s/%s/%s", base, v.TaskID, action), nil, nil
	case "update":
		var v struct {
			TaskID string `json:"taskId"`
		}
		_ = json.Unmarshal(body, &v)
		if v.TaskID == "" {
			return "", "", nil, fmt.Errorf("taskId required for update")
		}
		return http.MethodPatch, fmt.Sprintf("%s/%s", base, v.TaskID), bytes.NewReader(body), nil
	default:
		return "", "", nil, fmt.Errorf("unknown action %q", action)
	}
}

// injectTimezone adds {"timezone": os.Getenv("TZ")} into a JSON object body
// when the body is an object, "timezone" is absent, and TZ is non-empty.
// Non-object bodies (or already-set timezone) pass through unchanged.
//
// Carried over verbatim from the deleted LoopbackTransport — codex
// historically relied on this for naive-timestamp interpretation in
// the user's local zone.
func injectTimezone(body json.RawMessage) json.RawMessage {
	tz := os.Getenv("TZ")
	if tz == "" {
		return body
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return body
	}
	if _, present := m["timezone"]; present {
		return body
	}
	tzBytes, _ := json.Marshal(tz)
	m["timezone"] = tzBytes
	out, err := json.Marshal(m)
	if err != nil {
		return body
	}
	return out
}
