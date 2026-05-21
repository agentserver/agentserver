package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"

	"github.com/agentserver/agentserver/internal/auth"
	"github.com/agentserver/agentserver/internal/codexauth"

	"github.com/go-chi/chi/v5"
)

// executorNameRe is the allowed name shape: alphanumeric + dot, dash,
// underscore, 1–64 chars. Names go directly into a shell single-quoted
// argument in the connect command we surface to users (see line ~95);
// restricting the charset blocks any escape attempt without needing
// shell-aware quoting.
var executorNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

type registerExecutorReq struct {
	// Name is workspace-unique, surfaced to the LLM (via env-mcp's
	// list_environments) and used as the env_id parameter on shell /
	// apply_patch / etc. Required since v0.54.0.
	Name string `json:"name"`
	// Description is an optional per-binding note shown alongside
	// Name in list_environments. Free-text, no uniqueness constraint.
	Description string `json:"description,omitempty"`
}

type registerExecutorResp struct {
	ExeID string `json:"exe_id"`
	// ConnectCommand is the one-liner the user pastes on the machine
	// they want to expose as an executor. Empty when the gateway public
	// host isn't configured — the UI falls back to a generic template.
	ConnectCommand string `json:"connect_command,omitempty"`
	// AgentIdentityJWT is the codex Agent Identity JWT minted for this
	// executor. Present only when codexAuth is enabled
	// (CODEX_AUTH_ISSUER_URL set).
	AgentIdentityJWT string `json:"agent_identity_jwt,omitempty"`
	// ConnectCommands is the single-variant bundle surfaced by the Add
	// Connector UI. Empty when codexAuth is disabled.
	ConnectCommands ConnectCommands `json:"connect_commands,omitempty"`
}

// ConnectCommands is the Agent-Identity-only connect-command bundle
// returned by the Add Connector API. The pre-0.132 bcrypt path and the
// ChatGPT device-auth variant are gone — auth at /cloud/.../register
// now goes through the Agent Identity JWT (validated by agentserver),
// and the inbound ws verifies a short-lived HMAC ticket minted at
// register time.
type ConnectCommands struct {
	AgentIdentity string `json:"agent_identity"`
}

// handleRegisterExecutor mints a new executor owned by the calling user
// and immediately binds it to the workspace. ACL: caller must be
// owner/maintainer of the workspace.
//
// Returns the raw registration_token ONCE — UI must show it immediately
// and let the user copy. agentserver does not store the raw token.
//
//	@Summary   Register a new codex remote executor for a workspace
//	@Tags      Misc
//	@Accept    json
//	@Produce   json
//	@Param     wid   path  string                   true  "Workspace ID"
//	@Param     body  body  ExecutorRegisterRequest  true  "Executor registration payload"
//	@Success   201  {object}  ExecutorRegisterResponse
//	@Failure   400  {string}  string  "bad request / invalid name"
//	@Failure   401  {string}  string  "unauthorized"
//	@Failure   403  {string}  string  "insufficient role (owner/maintainer required)"
//	@Failure   502  {string}  string  "upstream gateway error"
//	@Failure   503  {string}  string  "executors not configured"
//	@Security  CookieAuth
//	@Router    /api/workspaces/{wid}/executors [post]
func (s *Server) handleRegisterExecutor(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	wid := chi.URLParam(r, "wid")
	if wid == "" {
		http.Error(w, "workspace id required", http.StatusBadRequest)
		return
	}
	if !s.requireWorkspaceRole(w, r, wid, "owner", "maintainer") {
		return
	}

	var req registerExecutorReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	if !executorNameRe.MatchString(req.Name) {
		http.Error(w, "name must be 1-64 chars of [A-Za-z0-9._-]", http.StatusBadRequest)
		return
	}
	if s.ExecutorsClient == nil {
		http.Error(w, "executors integration not configured", http.StatusServiceUnavailable)
		return
	}

	reg, err := s.ExecutorsClient.Register(r.Context(), userID, RegisterExecutorRequest{
		DisplayName: req.Name, // reuse for the system-level display_name; binding name is the one the LLM sees
	})
	if err != nil {
		http.Error(w, "register: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Auto-bind to the workspace this request was issued under, with
	// the user-supplied name and description. On bind failure (e.g.
	// duplicate name), tear down the freshly-registered executor so we
	// don't leak an orphan row with a wasted exe_id + registration
	// token. The cleanup is best-effort — if it also fails we log
	// (via the bridge error message) but the partial state will at
	// worst occupy a UUID + a never-bound bcrypt hash.
	if err := s.ExecutorsClient.Bind(r.Context(), userID, wid, reg.ExeID, req.Name, req.Description, false); err != nil {
		if cleanupErr := s.ExecutorsClient.Unregister(r.Context(), userID, reg.ExeID); cleanupErr != nil {
			http.Error(w, fmt.Sprintf("bind failed (%v); cleanup of orphan executor also failed (%v)", err, cleanupErr), http.StatusBadGateway)
			return
		}
		http.Error(w, "bind: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Mint Agent Identity JWT alongside the legacy bearer registration.
	// The exe_id is the agent_runtime_id (1-to-1 mapping). Best-effort
	// email lookup — empty email is OK, JWT just won't carry it.
	var aiResult *codexauth.MintAgentIdentityResult
	if s.CodexAuth != nil {
		var email string
		if user, uerr := s.Auth.GetUserByID(userID); uerr == nil && user != nil {
			email = user.Email
		}
		var mintErr error
		aiResult, mintErr = s.CodexAuth.MintAgentIdentity(r.Context(),
			codexauth.MintAgentIdentityArgs{
				AgentRuntimeID: reg.ExeID,
				UserID:         userID,
				Email:          email,
			})
		if mintErr != nil {
			http.Error(w, "mint agent identity: "+mintErr.Error(), http.StatusInternalServerError)
			return
		}
	}

	resp := registerExecutorResp{ExeID: reg.ExeID}
	if aiResult != nil {
		resp.AgentIdentityJWT = aiResult.JWT
	}
	if s.CodexExecGatewayPublicHost != "" && s.CodexAuthIssuerURL != "" && aiResult != nil {
		// Upstream codex `exec-server --remote` contract:
		//   1. POST <base_url>/cloud/executor/{id}/register with the
		//      Agent Identity JWT (Authorization: AgentAssertion ...).
		//   2. Server validates the JWT, returns {executor_id, url}
		//      with a short-lived HMAC ticket in ?token=.
		//   3. codex ws-dials url; inbound verifies the HMAC ticket.
		// Note `-c chatgpt_base_url=` not `chatgpt.base_url=` —
		// the codex config field is the snake_case top-level key, the
		// dotted form silently no-ops.
		gatewayURL := "https://" + s.CodexExecGatewayPublicHost
		issuer := s.CodexAuthIssuerURL
		resp.ConnectCommands = ConnectCommands{
			AgentIdentity: fmt.Sprintf(
				"export CODEX_ACCESS_TOKEN='%s'\nexport CODEX_AGENT_IDENTITY_AUTHAPI_BASE_URL='%s'\ncodex -c chatgpt_base_url='%s' exec-server --remote '%s' --executor-id '%s' --name '%s' --use-agent-identity-auth",
				aiResult.JWT, issuer, issuer, gatewayURL, reg.ExeID, req.Name),
		}
		resp.ConnectCommand = resp.ConnectCommands.AgentIdentity
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

// handleListExecutors returns executors bound to the workspace.
// ACL: any workspace member.
//
//	@Summary   List codex remote executors for a workspace
//	@Tags      Misc
//	@Produce   json
//	@Param     wid  path  string  true  "Workspace ID"
//	@Success   200  {array}   ExecutorItem
//	@Failure   401  {string}  string  "unauthorized"
//	@Failure   403  {string}  string  "not a workspace member"
//	@Failure   502  {string}  string  "upstream gateway error"
//	@Security  CookieAuth
//	@Router    /api/workspaces/{wid}/executors [get]
func (s *Server) handleListExecutors(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	wid := chi.URLParam(r, "wid")
	if _, ok := s.requireWorkspaceMember(w, r, wid); !ok {
		return
	}
	if s.ExecutorsClient == nil {
		_ = json.NewEncoder(w).Encode([]ListedExecutor{})
		return
	}

	rows, err := s.ExecutorsClient.List(r.Context(), userID, wid)
	if err != nil {
		http.Error(w, "list: "+err.Error(), http.StatusBadGateway)
		return
	}
	if rows == nil {
		rows = []ListedExecutor{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rows)
}

// handleUnbindExecutor removes an executor from the workspace. ACL:
// owner/maintainer of the workspace.
//
//	@Summary   Remove a codex remote executor from a workspace
//	@Tags      Misc
//	@Param     wid     path  string  true  "Workspace ID"
//	@Param     exe_id  path  string  true  "Executor ID"
//	@Success   204  "removed"
//	@Failure   400  {string}  string  "bad request"
//	@Failure   401  {string}  string  "unauthorized"
//	@Failure   403  {string}  string  "insufficient role (owner/maintainer required)"
//	@Failure   502  {string}  string  "upstream gateway error"
//	@Failure   503  {string}  string  "executors not configured"
//	@Security  CookieAuth
//	@Router    /api/workspaces/{wid}/executors/{exe_id} [delete]
func (s *Server) handleUnbindExecutor(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	wid := chi.URLParam(r, "wid")
	exeID := chi.URLParam(r, "exe_id")
	if wid == "" || exeID == "" {
		http.Error(w, "wid and exe_id required", http.StatusBadRequest)
		return
	}
	if !s.requireWorkspaceRole(w, r, wid, "owner", "maintainer") {
		return
	}
	if s.ExecutorsClient == nil {
		http.Error(w, "executors integration not configured", http.StatusServiceUnavailable)
		return
	}
	if err := s.ExecutorsClient.Unbind(r.Context(), userID, wid, exeID); err != nil {
		http.Error(w, "unbind: "+err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

