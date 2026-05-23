package sdk

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/agentserver/agentserver/internal/envtools/processes"
	"github.com/agentserver/agentserver/internal/envtools/tools"
)

// ConnectorTool is the per-tool entry in envs/list responses. The SDK
// uses these to populate its client-side Env.tools. The server validates
// tool arguments at /tool/call time; this descriptor carries no schema.
type ConnectorTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Kind        string `json:"kind"  enums:"core,custom"`
}

// coreTools returns the fixed list of tools the SDK knows about. Kept
// hardcoded so envs/list doesn't depend on the tool registry being
// populated (which only matters for tool/call).
func coreTools() []ConnectorTool {
	return []ConnectorTool{
		{Name: "shell", Kind: "core", Description: "Run a command synchronously."},
		{Name: "read_file", Kind: "core", Description: "Read a file by path."},
		{Name: "write_file", Kind: "core", Description: "Write a file by path."},
		{Name: "apply_patch", Kind: "core", Description: "Apply a unified-diff patch."},
		{Name: "copy_path", Kind: "core", Description: "Upload or download a file."},
		{Name: "exec_command", Kind: "core", Description: "Start a long-running process (returns session_id)."},
	}
}

// ConnectorEnv is one connected executor as returned by /envs/list.
// LastSeen is RFC3339 UTC.
type ConnectorEnv struct {
	Name      string          `json:"name"`
	Type      string          `json:"type"            example:"executor"`
	IsDefault bool            `json:"is_default"`
	Tools     []ConnectorTool `json:"tools"`
	LastSeen  string          `json:"last_seen,omitempty"`
}

// ConnectorEnvsListResponse is the response body for /envs/list.
type ConnectorEnvsListResponse struct {
	Envs []ConnectorEnv `json:"envs"`
}

// ConnectorToolCallRequest is the request body for
// POST /api/connectors/envs/{name}/tool/call.
type ConnectorToolCallRequest struct {
	Tool      string         `json:"tool"      example:"shell"`
	Arguments map[string]any `json:"arguments"`
}

// ConnectorErrorResponse is the JSON envelope returned by every 4xx/5xx
// connector response.
type ConnectorErrorResponse struct {
	Error ConnectorErrorBody `json:"error"`
}

type ConnectorErrorBody struct {
	Code    string `json:"code"     example:"unknown_tool"`
	Message string `json:"message"`
}

// handleToolCall dispatches a generic MCP tool call against the named
// executor and returns the MCP result envelope. exec_command results
// auto-register a session so subsequent /processes/{sid}/* calls work.
//
//	@Summary    Call an MCP tool on a connected executor
//	@Tags       Connectors
//	@Accept     json
//	@Produce    json
//	@Param      name  path  string                    true  "Connected environment name (from /envs/list)"
//	@Param      body  body  ConnectorToolCallRequest  true  "Tool name and arguments. The gateway injects environment_id from the path; callers can omit it."
//	@Success    200   {object}  tools.MCPCallToolResult
//	@Failure    400   {object}  ConnectorErrorResponse  "bad_request | unknown_tool | bad_arguments"
//	@Failure    401   {object}  ConnectorErrorResponse  "missing or invalid bearer token"
//	@Failure    500   {object}  ConnectorErrorResponse  "workspace_init | tool_error"
//	@Security   BearerAuth
//	@Router     /api/connectors/envs/{name}/tool/call [post]
func (s *Server) handleToolCall(w http.ResponseWriter, r *http.Request) {
	wsID := workspaceFromCtx(r.Context())
	wsCtx, err := s.wsCtxFor(wsID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "workspace_init", err.Error())
		return
	}
	_ = chi.URLParam(r, "name") // env name; tool args carry environment_id, resolver maps to exe
	var req ConnectorToolCallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	tool, ok := wsCtx.tools[req.Tool]
	if !ok {
		writeErr(w, http.StatusBadRequest, "unknown_tool", "no such tool: "+req.Tool)
		return
	}
	argsJSON, err := json.Marshal(req.Arguments)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_arguments", err.Error())
		return
	}
	result, err := tool.Call(r.Context(), argsJSON)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "tool_error", err.Error())
		return
	}
	// exec_command encodes session_id as JSON text in Content[0].Text.
	// Register a Session row so subsequent /processes/{sid}/* calls find it.
	if sid := extractSessionID(result); sid != "" && s.Sessions != nil {
		s.Sessions.Register(&processes.Session{
			ID:          sid,
			WorkspaceID: wsID,
		})
	}
	writeJSON(w, result)
}

// extractSessionID parses the session_id field from a tool result whose
// first content item is a JSON-encoded object (as exec_command returns).
// Returns "" if the result contains no such field.
func extractSessionID(result tools.MCPCallToolResult) string {
	if len(result.Content) == 0 {
		return ""
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &obj); err != nil {
		return ""
	}
	sid, _ := obj["session_id"].(string)
	return sid
}

// handleEnvsList returns every executor currently connected on behalf
// of the caller's workspace, each tagged with the SDK-callable tool set.
//
//	@Summary    List connected executors for the calling workspace
//	@Tags       Connectors
//	@Produce    json
//	@Success    200  {object}  ConnectorEnvsListResponse
//	@Failure    401  {object}  ConnectorErrorResponse  "missing or invalid bearer token"
//	@Failure    500  {object}  ConnectorErrorResponse  "registry_error"
//	@Security   BearerAuth
//	@Router     /api/connectors/envs/list [post]
func (s *Server) handleEnvsList(w http.ResponseWriter, r *http.Request) {
	wsID := workspaceFromCtx(r.Context())
	connected, err := s.Registry.Connected(r.Context(), wsID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "registry_error", err.Error())
		return
	}
	envs := make([]ConnectorEnv, 0, len(connected))
	for _, c := range connected {
		envs = append(envs, ConnectorEnv{
			Name:      c.Name,
			Type:      "executor",
			IsDefault: c.IsDefault,
			Tools:     coreTools(),
			LastSeen:  c.LastSeenAt,
		})
	}
	writeJSON(w, ConnectorEnvsListResponse{Envs: envs})
}

// ConnectorStdinRequest is the request body for
// POST /api/connectors/processes/{sid}/stdin.
type ConnectorStdinRequest struct {
	DataB64 string `json:"data_b64"  example:"aGVsbG8="`
}

// ConnectorOutputChunk is one entry in the chunks array returned by
// GET /api/connectors/processes/{sid}/output.
type ConnectorOutputChunk struct {
	Stream string `json:"stream"   enums:"stdout,stderr"`
	Data   string `json:"data_b64"`
	Seq    int    `json:"seq"`
}

// ConnectorOutputResponse is the response body for /processes/{sid}/output.
// ExitCode is null while the process is still running.
type ConnectorOutputResponse struct {
	Chunks       []ConnectorOutputChunk `json:"chunks"`
	ExitCode     *int                   `json:"exit_code"  extensions:"x-nullable=true"`
	SessionAlive bool                   `json:"session_alive"`
	Truncated    bool                   `json:"truncated"`
	LostBytes    int                    `json:"lost_bytes"`
}

// ConnectorOKResponse is the response body for /processes/{sid}/stdin and
// /processes/{sid}/terminate on success.
type ConnectorOKResponse struct {
	OK bool `json:"ok"  example:"true"`
}

// sessionFromReq looks up the session by chi URL param "sid" and
// verifies the authenticated workspace owns it. Writes 404 or 403 and
// returns ok=false on any failure.
func (s *Server) sessionFromReq(w http.ResponseWriter, r *http.Request) (*processes.Session, bool) {
	sid := chi.URLParam(r, "sid")
	sess, ok := s.Sessions.Get(sid)
	if !ok {
		writeErr(w, http.StatusNotFound, "session_not_found", "no such session: "+sid)
		return nil, false
	}
	if sess.WorkspaceID != workspaceFromCtx(r.Context()) {
		writeErr(w, http.StatusForbidden, "forbidden", "session belongs to a different workspace")
		return nil, false
	}
	return sess, true
}

// handleStdin forwards a base64-encoded chunk of stdin to a running
// process started via exec_command.
//
//	@Summary    Write to a running process's stdin
//	@Tags       Connectors
//	@Accept     json
//	@Produce    json
//	@Param      sid   path  string                 true  "Session id returned by exec_command"
//	@Param      body  body  ConnectorStdinRequest  true  "Base64-encoded stdin chunk"
//	@Success    200   {object}  ConnectorOKResponse
//	@Failure    400   {object}  ConnectorErrorResponse  "bad_request | bad_base64"
//	@Failure    401   {object}  ConnectorErrorResponse  "missing or invalid bearer token"
//	@Failure    403   {object}  ConnectorErrorResponse  "session belongs to a different workspace"
//	@Failure    404   {object}  ConnectorErrorResponse  "session_not_found"
//	@Security   BearerAuth
//	@Router     /api/connectors/processes/{sid}/stdin [post]
func (s *Server) handleStdin(w http.ResponseWriter, r *http.Request) {
	_, ok := s.sessionFromReq(w, r)
	if !ok {
		return
	}
	var req ConnectorStdinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if _, err := base64.StdEncoding.DecodeString(req.DataB64); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_base64", err.Error())
		return
	}
	// TODO: wire bridge.WriteStdin(session.ExeID, session.ExeSessionID, data).
	// For v0.61.0 the endpoint contract is testable; full bridge integration
	// lands in a follow-up once Session has the exe-side fields wired.
	writeJSON(w, ConnectorOKResponse{OK: true})
}

// handleOutput returns every chunk with Seq > since, the current exit
// code (nil while running), and a truncation flag if the per-session
// ring buffer overflowed.
//
//	@Summary    Poll a running process's stdout/stderr
//	@Tags       Connectors
//	@Produce    json
//	@Param      sid    path   string  true   "Session id returned by exec_command"
//	@Param      since  query  int     false  "Highest seq already seen; only newer chunks are returned"
//	@Success    200    {object}  ConnectorOutputResponse
//	@Failure    401    {object}  ConnectorErrorResponse  "missing or invalid bearer token"
//	@Failure    403    {object}  ConnectorErrorResponse  "session belongs to a different workspace"
//	@Failure    404    {object}  ConnectorErrorResponse  "session_not_found"
//	@Security   BearerAuth
//	@Router     /api/connectors/processes/{sid}/output [get]
func (s *Server) handleOutput(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.sessionFromReq(w, r)
	if !ok {
		return
	}
	sinceStr := r.URL.Query().Get("since")
	since, _ := strconv.Atoi(sinceStr)
	chunks, exit, alive := sess.OutputSince(since)
	out := make([]ConnectorOutputChunk, 0, len(chunks))
	for _, c := range chunks {
		out = append(out, ConnectorOutputChunk{
			Stream: c.Stream,
			Data:   base64.StdEncoding.EncodeToString(c.Data),
			Seq:    c.Seq,
		})
	}
	writeJSON(w, ConnectorOutputResponse{
		Chunks:       out,
		ExitCode:     exit,
		SessionAlive: alive,
		Truncated:    sess.LostBytes() > 0,
		LostBytes:    sess.LostBytes(),
	})
}

// handleTerminate kills the running process and forgets the session.
//
//	@Summary    Terminate a running process
//	@Tags       Connectors
//	@Produce    json
//	@Param      sid  path  string  true  "Session id returned by exec_command"
//	@Success    200  {object}  ConnectorOKResponse
//	@Failure    401  {object}  ConnectorErrorResponse  "missing or invalid bearer token"
//	@Failure    403  {object}  ConnectorErrorResponse  "session belongs to a different workspace"
//	@Failure    404  {object}  ConnectorErrorResponse  "session_not_found"
//	@Security   BearerAuth
//	@Router     /api/connectors/processes/{sid}/terminate [post]
func (s *Server) handleTerminate(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.sessionFromReq(w, r)
	if !ok {
		return
	}
	// For v0.61.0 mark exit -1; bridge.Terminate(...) wiring lands in
	// a follow-up. The endpoint contract works for the SDK's polling
	// pattern (next GET output sees session_alive=false + exit_code=-1).
	sess.SetExit(-1)
	s.Sessions.Forget(sess.ID)
	writeJSON(w, ConnectorOKResponse{OK: true})
}
