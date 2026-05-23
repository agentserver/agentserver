package codexappgateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/agentserver/agentserver/internal/codexexecgateway/execmodel"
)

// handleInternalConnected serves GET /internal/connected, called by the
// env-mcp child process running inside the same pod over loopback. It
// returns the same payload as codex-exec-gateway's /api/exec-gateway/
// connected, scoped to the workspace identified by the supplied
// X-Loopback-Token (issued per codex app-server spawn).
//
// Auth has two independent gates:
//   - The HTTP listener is the pod-wide gateway. We additionally reject
//     any RemoteAddr that's not a loopback IP, so even if the gateway
//     accidentally exposes this route over the LB it can't be reached.
//   - The X-Loopback-Token resolves to a workspace_id via the supervisor's
//     per-spawn token map (constant-time compare).
//
// Returns:
//   - 200 + JSON array on success (may be empty)
//   - 401 if the token header is missing or doesn't match any live spawn
//   - 403 if RemoteAddr is not loopback
//   - 500 on internal/connected upstream failure
func (s *Server) handleInternalConnected(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRemote(r.RemoteAddr) {
		s.logger.Warn("internal/connected: rejecting non-loopback caller", "remote", r.RemoteAddr)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	tok := r.Header.Get("X-Loopback-Token")
	if tok == "" {
		http.Error(w, "missing X-Loopback-Token", http.StatusUnauthorized)
		return
	}
	wid, ok := s.sup.LookupWorkspaceForLoopbackToken(tok)
	if !ok {
		http.Error(w, "bad token", http.StatusUnauthorized)
		return
	}
	list, err := s.execClient.Connected(r.Context(), wid)
	if err != nil {
		s.logger.Warn("internal/connected: upstream fetch failed", "workspace_id", wid, "err", err)
		http.Error(w, "list", http.StatusInternalServerError)
		return
	}
	if list == nil {
		list = []execmodel.ConnectedExecutor{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}

// isLoopbackRemote reports whether addr's host portion is a loopback IP.
// addr is in the net.RemoteAddr format "ip:port" (or "[ipv6]:port").
func isLoopbackRemote(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// handleInternalScheduledTask returns an http.HandlerFunc that:
//  1. Guards on loopback IP (403 if not).
//  2. Resolves workspace from X-Loopback-Token (401 if bad).
//  3. Maps the MCP action to an agentserver-main internal endpoint.
//  4. Forwards the request, signing with X-Internal-Secret.
//  5. Copies the response body verbatim back to the caller.
//
// All 6 MCP actions arrive as POST (env-mcp always POSTs); the proxy
// converts "list" into a GET and "cancel/pause/resume/update" extract
// the taskId from the JSON body to build the URL path.
func (s *Server) handleInternalScheduledTask(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackRemote(r.RemoteAddr) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		tok := r.Header.Get("X-Loopback-Token")
		if tok == "" {
			http.Error(w, "missing X-Loopback-Token", http.StatusUnauthorized)
			return
		}
		wid, ok := s.sup.LookupWorkspaceForLoopbackToken(tok)
		if !ok {
			http.Error(w, "bad token", http.StatusUnauthorized)
			return
		}

		body, _ := io.ReadAll(r.Body)

		// Map action → HTTP method + upstream path.
		var method, upstreamPath string
		switch action {
		case "schedule":
			method = http.MethodPost
			upstreamPath = fmt.Sprintf("/api/internal/workspaces/%s/scheduled-tasks", wid)

		case "list":
			// Convert from POST (MCP body) to GET (REST). Extract optional
			// status filter from the JSON body.
			method = http.MethodGet
			var v struct {
				Status string `json:"status"`
			}
			_ = json.Unmarshal(body, &v)
			upstreamPath = fmt.Sprintf("/api/internal/workspaces/%s/scheduled-tasks", wid)
			if v.Status != "" {
				upstreamPath += "?status=" + url.QueryEscape(v.Status)
			}
			body = nil // GET has no body

		case "cancel", "pause", "resume":
			var v struct {
				TaskID string `json:"taskId"`
			}
			_ = json.Unmarshal(body, &v)
			if v.TaskID == "" {
				http.Error(w, "taskId required", http.StatusBadRequest)
				return
			}
			method = http.MethodPost
			upstreamPath = fmt.Sprintf("/api/internal/workspaces/%s/scheduled-tasks/%s/%s", wid, v.TaskID, action)
			body = nil // no body needed for these actions

		case "update":
			var v struct {
				TaskID string `json:"taskId"`
			}
			_ = json.Unmarshal(body, &v)
			if v.TaskID == "" {
				http.Error(w, "taskId required", http.StatusBadRequest)
				return
			}
			method = http.MethodPatch
			upstreamPath = fmt.Sprintf("/api/internal/workspaces/%s/scheduled-tasks/%s", wid, v.TaskID)
			// body is forwarded as-is (handler ignores unknown fields)

		default:
			http.Error(w, "unknown action: "+action, http.StatusBadRequest)
			return
		}

		upstream := strings.TrimRight(s.cfg.AgentserverInternalURL, "/") + upstreamPath
		req, err := http.NewRequestWithContext(r.Context(), method, upstream, bytes.NewReader(body))
		if err != nil {
			http.Error(w, "build request: "+err.Error(), http.StatusInternalServerError)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Internal-Secret", s.cfg.AgentserverInternalSecret)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			http.Error(w, "upstream: "+err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}
}
