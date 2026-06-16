package mcppublic

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/agentserver/agentserver/internal/envtools/tools"
)

// Streamable HTTP transport for the public MCP gateway, targeting
// the MCP protocol version `2025-06-18` (Codex CLI's default; see
// codex-rs/codex-mcp/src/rmcp_client.rs:489-493).
//
// Endpoints on the gateway hostname (production: mcp.agent.cs.ac.cn):
//
//   POST /v1/mcp        — JSON-RPC request → JSON-RPC response.
//                         Content-Type: application/json.
//   GET  /v1/mcp        — 405. Stateless server, no SSE re-attach;
//                         the spec marks GET as MAY for stateless.
//   DELETE /v1/mcp      — 405. No Mcp-Session-Id support in this
//                         transport; nothing to delete.
//   GET  /healthz       — 200, plain text "ok". For K8s liveness +
//                         istio readiness probes.
//   GET  /v1/.well-known/oauth-protected-resource
//                       — small JSON stub pointing at agentserver's
//                         /api/workspaces/{wid}/mcp/pats minting UI.
//                         Codex CLI ignores this and short-circuits
//                         to bearer_token_env_var; Claude Desktop 1P
//                         (Phase 2) will use it for OAuth discovery.
//
// Wire shape per JSON-RPC 2.0:
//   {"jsonrpc":"2.0","id":<n>,"method":"<m>","params":<…>}
//   {"jsonrpc":"2.0","id":<n>,"result":<…>}
//   {"jsonrpc":"2.0","id":<n>,"error":{"code":<n>,"message":"<m>"}}
//
// Auth: the auth.Middleware (PR D) wraps this handler; by the time
// ServeHTTP runs, the Principal is in r.Context() via PrincipalFromContext.

// jsonrpcReq is the inbound wire envelope. We only need a handful of
// fields; everything else is ignored.
type jsonrpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"` // RawMessage so a string id round-trips
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// jsonrpcResp is the outbound envelope. Either Result or Error is
// populated, never both. ID is echoed verbatim from the request
// (null for notifications, which we don't actually reply to).
type jsonrpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcErr     `json:"error,omitempty"`
}

// Server is the http.Handler that exposes Dispatcher over Streamable
// HTTP MCP. Wrap it with auth.Middleware before mounting.
type Server struct {
	Dispatcher *Dispatcher
	Logger     *slog.Logger

	// IssuerURL, when non-empty, is the agentserver web base URL
	// used to build the `resource_metadata` link advertised in 401
	// WWW-Authenticate headers + the oauth-protected-resource doc.
	// Typical value: "https://app.agent.cs.ac.cn".
	IssuerURL string
}

// NewServer wires a transport in front of a dispatcher.
func NewServer(d *Dispatcher, issuerURL string, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{Dispatcher: d, IssuerURL: issuerURL, Logger: logger}
}

// Mount returns an http.Handler with the gateway's routes. Mount
// auth middleware around `/v1/mcp` separately so the well-known
// metadata endpoint and /healthz stay public.
func (s *Server) Mount(authMW func(http.Handler) http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	})
	mux.HandleFunc("/v1/.well-known/oauth-protected-resource", s.handleOAuthProtectedResource)

	mcp := http.HandlerFunc(s.handleMCP)
	mux.Handle("/v1/mcp", authMW(mcp))
	return mux
}

// handleMCP routes a single Streamable HTTP MCP request. POST is the
// only verb that carries a JSON-RPC request; GET/DELETE return 405
// since this transport is stateless (no session id, nothing to
// stream server-initiated, nothing to delete).
func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.handleJSONRPC(w, r)
	case http.MethodGet, http.MethodDelete:
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed; this gateway is stateless (POST only)", http.StatusMethodNotAllowed)
	default:
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleJSONRPC reads one JSON-RPC request, dispatches by method,
// writes one JSON-RPC response. JSON-RPC 2.0 batch (an array of
// requests) is NOT supported — the MCP profile codex/mcp-remote
// emit is always a single request per HTTP POST, and supporting
// batch adds wire complexity for zero clients we care about.
func (s *Server) handleJSONRPC(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MiB cap
	if err != nil {
		s.writeErr(w, nil, codeInternal, "read body: "+err.Error())
		return
	}
	// Be tolerant of a stray BOM / whitespace — saves debugging a
	// `null` request when a curl pipe adds CR.
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		s.writeErr(w, nil, codeInvalidRequest, "empty request body")
		return
	}
	// Reject obvious JSON-RPC batch arrays early with a clear
	// message — better than letting json.Unmarshal fail with a less
	// actionable error.
	if raw[0] == '[' {
		s.writeErr(w, nil, codeInvalidRequest, "JSON-RPC batch is not supported on this gateway")
		return
	}

	var req jsonrpcReq
	if err := json.Unmarshal(raw, &req); err != nil {
		s.writeErr(w, nil, codeParseError, "parse error: "+err.Error())
		return
	}
	if req.JSONRPC != "" && req.JSONRPC != "2.0" {
		// Codex sends "2.0"; tolerate empty (some clients omit it)
		// but reject anything else.
		s.writeErr(w, req.ID, codeInvalidRequest, `jsonrpc must be "2.0"`)
		return
	}

	p := PrincipalFromContext(r.Context())

	switch req.Method {
	case "initialize":
		res := s.Dispatcher.Initialize()
		s.writeOK(w, req.ID, res)

	case "notifications/initialized", "notifications/cancelled":
		// JSON-RPC notification — id is absent. The MCP spec also
		// permits no response body for notifications, but Codex
		// happily accepts an empty 200 either way.
		w.WriteHeader(http.StatusOK)

	case "tools/list":
		res := s.Dispatcher.ToolsList(p)
		s.writeOK(w, req.ID, res)

	case "tools/call":
		var params tools.MCPCallToolParams
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &params); err != nil {
				s.writeErr(w, req.ID, codeInvalidParams, "invalid tools/call params: "+err.Error())
				return
			}
		}
		res, jerr := s.Dispatcher.ToolsCall(r.Context(), p, params)
		if jerr != nil {
			s.writeErr(w, req.ID, jerr.Code, jerr.Message)
			return
		}
		s.writeOK(w, req.ID, res)

	case "prompts/list":
		// MCP requires the server to answer the prompts/list probe
		// even if it carries nothing. Empty list keeps clients happy.
		s.writeOK(w, req.ID, map[string]any{"prompts": []any{}})

	case "resources/list", "resources/templates/list":
		s.writeOK(w, req.ID, map[string]any{"resources": []any{}})

	case "ping":
		// MCP's keepalive — Codex doesn't currently send it, but the
		// spec mandates a server-side response.
		s.writeOK(w, req.ID, map[string]any{})

	default:
		if len(req.ID) == 0 || string(req.ID) == "null" {
			// Notification of an unknown method — drop silently.
			w.WriteHeader(http.StatusOK)
			return
		}
		s.writeErr(w, req.ID, codeMethodNotFound, "method not found: "+req.Method)
	}
}

// JSON-RPC 2.0 standard error codes for transport-level problems
// (these are distinct from dispatch.go's authorization codes).
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeInvalidParams  = -32602
)

func (s *Server) writeOK(w http.ResponseWriter, id json.RawMessage, result any) {
	body, err := json.Marshal(result)
	if err != nil {
		s.writeErr(w, id, codeInternal, "marshal result: "+err.Error())
		return
	}
	resp := jsonrpcResp{
		JSONRPC: "2.0",
		ID:      idOrNull(id),
		Result:  body,
	}
	envelope, err := json.Marshal(resp)
	if err != nil {
		// Fallback — shouldn't happen, but don't leave the wire empty.
		s.Logger.Error("mcppublic: marshal envelope failed", "err", err)
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(envelope)
}

func (s *Server) writeErr(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	resp := jsonrpcResp{
		JSONRPC: "2.0",
		ID:      idOrNull(id),
		Error:   &jsonrpcErr{Code: code, Message: msg},
	}
	envelope, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	// JSON-RPC always uses 200 for "the protocol succeeded but the
	// call errored"; per MCP spec, transport-level problems (parse
	// error / no body) are likewise carried in-band rather than
	// surfaced as HTTP 4xx. The only exceptions are the auth
	// middleware's 401s and the 405s on GET/DELETE — those happen
	// before we get here.
	_, _ = w.Write(envelope)
}

// idOrNull echoes the request id verbatim, falling back to JSON null
// when the request had no id (notification or malformed parse).
func idOrNull(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage("null")
	}
	return id
}

// handleOAuthProtectedResource serves the small JSON document the MCP
// spec's authorization profile (2025-06-18) points clients at via
// WWW-Authenticate. Codex CLI ignores this (it short-circuits to
// bearer_token_env_var); Claude Desktop 1P (Phase 2) will use it.
//
// The minimal shape required by RFC 9728 / MCP's profile:
//
//	{
//	  "resource": "<this-gateway-url>",
//	  "authorization_servers": ["<issuer>"]
//	}
//
// We don't run an OAuth server yet (Phase 2), so authorization_servers
// points back at the agentserver web UI — clients that bother to
// follow the link land on the workspace settings page where they
// can mint a PAT manually.
func (s *Server) handleOAuthProtectedResource(w http.ResponseWriter, r *http.Request) {
	gatewayURL := "https://" + r.Host + "/v1/mcp"
	if !requestIsHTTPS(r) {
		// Dev / port-forward case — keep things working over plain http.
		gatewayURL = "http://" + r.Host + "/v1/mcp"
	}
	doc := map[string]any{
		"resource": gatewayURL,
		"authorization_servers": []string{
			func() string {
				if s.IssuerURL != "" {
					return s.IssuerURL
				}
				return gatewayURL
			}(),
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(doc)
}

// requestIsHTTPS reports whether the original client request was over
// TLS. The pod terminates TLS at the ingress layer (istio-ingress +
// cert-manager) and arrives on the cleartext Service port, so
// `r.TLS` is always nil in production — we have to trust the
// `X-Forwarded-Proto` header the ingress sets. Trusting it is fine
// here because the Service is only reachable through the ingress
// (NetworkPolicy + HTTPRoute pin the path; no direct pod traffic
// from outside the cluster).
//
// Order:
//  1. X-Forwarded-Proto if set (production istio-ingress path)
//  2. r.TLS != nil (httptest.NewTLSServer / standalone TLS)
//  3. otherwise plain http (dev / port-forward / curl-against-Service)
func requestIsHTTPS(r *http.Request) bool {
	if xfp := r.Header.Get("X-Forwarded-Proto"); xfp != "" {
		// Header may carry a comma-separated chain (RFC 7239-style);
		// take the first entry which is the client-facing scheme.
		first := xfp
		if i := strings.IndexByte(xfp, ','); i >= 0 {
			first = xfp[:i]
		}
		return strings.EqualFold(strings.TrimSpace(first), "https")
	}
	return r.TLS != nil
}

// AuthMiddleware constructs the auth.Middleware-equivalent http.Handler
// wrapper. Exposed as a convenience so the cmd binary doesn't have to
// know about the Middleware struct's fields directly — pass an empty
// `resolvers` here and the gateway 401s every request, which is the
// correct fail-closed posture if the cmd forgot to wire a PATResolver.
func AuthMiddleware(resolvers []PrincipalResolver, resourceMetadataURL string, logger *slog.Logger) func(http.Handler) http.Handler {
	mw := &Middleware{
		Resolvers:           resolvers,
		ResourceMetadataURL: resourceMetadataURL,
		Logger:              logger,
	}
	return mw.Wrap
}

