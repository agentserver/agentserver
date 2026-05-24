package sdk

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/agentserver/agentserver/internal/codexexecgateway/audit"
	"github.com/agentserver/agentserver/internal/envtools/processes"
	"github.com/agentserver/agentserver/internal/envtools/tools"
)

// recordCall wraps a handler body with a CallStart / CallEnd pair so
// the SDK call is captured at handler-level granularity. The matching
// per-WS-frame audit hooks are suppressed for sdk-pool-managed bridges
// in codexexecgateway.handleBridge (see captoken.go's SkipAudit field)
// to avoid double-recording the same call.
//
// method is the RPC-equivalent name ("tool.call:shell",
// "envs.list", "processes.stdin", etc.).
// exeID is best-effort: empty when the handler cannot resolve it
// pre-call (e.g. tool.Call's internal name → exe_id resolution).
// requestJSON is captured as the CallStart Request bytes; pass nil
// when there is nothing meaningful (e.g. an empty GET).
// fn is invoked under the wrapper; it returns the response bytes for
// CallEnd's Response field plus the error metadata.
//
// Returns ok=true when fn was invoked, ok=false when CallStart failed
// (handler should short-circuit; recordCall has already written a 503).
// On panic inside fn, recordCall emits a CallEnd with IsError=true +
// ErrorSummary="panic: ..." before re-raising — so a panic doesn't
// leave a CallStart orphaned with no matching End.
func recordCall(
	rec audit.Recorder,
	w http.ResponseWriter,
	workspaceID, userID, exeID, method string,
	requestJSON []byte,
	fn func() (responseJSON []byte, isError bool, errorSummary string),
) (ok bool) {
	callID, err := rec.CallStart(audit.CallStartMeta{
		WorkspaceID: workspaceID,
		UserID:      userID,
		ExeID:       exeID,
		Source:      "rest",
		RPCMethod:   method,
		RPCKind:     "request",
		Request:     requestJSON,
		StartedAt:   time.Now().UTC(),
	})
	if err != nil {
		log.Printf("exec-audit: CallStart failed for %s: %v", method, err)
		writeErr(w, http.StatusServiceUnavailable, "audit_unavailable", err.Error())
		return false
	}
	completed := false
	var endMeta audit.CallEndMeta
	defer func() {
		if !completed {
			r := recover()
			endMeta = audit.CallEndMeta{
				CompletedAt: time.Now().UTC(),
				IsError:     true,
			}
			if r != nil {
				endMeta.ErrorSummary = fmt.Sprintf("panic: %v", r)
			} else {
				endMeta.ErrorSummary = "incomplete (early return without panic)"
			}
			rec.CallEnd(callID, endMeta)
			if r != nil {
				panic(r) // re-raise after audit
			}
			return
		}
		rec.CallEnd(callID, endMeta)
	}()
	resp, isErr, errSum := fn()
	endMeta = audit.CallEndMeta{
		CompletedAt:  time.Now().UTC(),
		IsError:      isErr,
		ErrorSummary: errSum,
		Response:     resp,
	}
	completed = true
	return true
}

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
	userID := userIDFromCtx(r.Context())
	wsCtx, err := s.wsCtxFor(wsID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "workspace_init", err.Error())
		return
	}
	_ = chi.URLParam(r, "name") // env name; tool args carry environment_id, resolver maps to exe
	// Read the full body up front so the audit Request can capture the
	// raw bytes (NOT the re-marshaled form, which drops the original
	// field ordering and any unknown keys).
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	var req ConnectorToolCallRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
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
	// exeID is not derivable pre-call here: the env-name → exe_id
	// resolution lives inside tool.Call (via nameresolver.Resolver) and
	// isn't exposed. Leave "" — the WAL still carries workspace_id +
	// user_id + the tool method.
	recordCall(s.Recorder, w, wsID, userID, "", "tool.call:"+req.Tool, bodyBytes, func() ([]byte, bool, string) {
		result, callErr := tool.Call(r.Context(), argsJSON)
		if callErr != nil {
			writeErr(w, http.StatusInternalServerError, "tool_error", callErr.Error())
			return nil, true, callErr.Error()
		}
		// exec_command encodes session_id as JSON text in Content[0].Text.
		// Register a Session row so subsequent /processes/{sid}/* calls find it.
		if sid := extractSessionID(result); sid != "" && s.Sessions != nil {
			s.Sessions.Register(&processes.Session{
				ID:          sid,
				WorkspaceID: wsID,
			})
		}
		respBytes, mErr := json.Marshal(result)
		if mErr != nil {
			log.Printf("exec-audit: marshal tool result for %s: %v", req.Tool, mErr)
			respBytes = []byte(fmt.Sprintf(`{"audit_marshal_failed":%q}`, mErr.Error()))
		}
		writeJSON(w, result)
		return respBytes, false, ""
	})
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
	userID := userIDFromCtx(r.Context())
	recordCall(s.Recorder, w, wsID, userID, "", "envs.list", nil, func() ([]byte, bool, string) {
		connected, err := s.Registry.Connected(r.Context(), wsID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "registry_error", err.Error())
			return nil, true, err.Error()
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
		resp := ConnectorEnvsListResponse{Envs: envs}
		respBytes, mErr := json.Marshal(resp)
		if mErr != nil {
			log.Printf("exec-audit: marshal envs/list: %v", mErr)
			respBytes = []byte(fmt.Sprintf(`{"audit_marshal_failed":%q}`, mErr.Error()))
		}
		writeJSON(w, resp)
		return respBytes, false, ""
	})
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
	sess, ok := s.sessionFromReq(w, r)
	if !ok {
		return
	}
	wsID := workspaceFromCtx(r.Context())
	userID := userIDFromCtx(r.Context())
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	_ = sess // processes.Session has no ExeID yet (TODO above); pass "" for exe_id
	recordCall(s.Recorder, w, wsID, userID, "", "processes.stdin", bodyBytes, func() ([]byte, bool, string) {
		var req ConnectorStdinRequest
		if err := json.Unmarshal(bodyBytes, &req); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return nil, true, err.Error()
		}
		if _, err := base64.StdEncoding.DecodeString(req.DataB64); err != nil {
			writeErr(w, http.StatusBadRequest, "bad_base64", err.Error())
			return nil, true, err.Error()
		}
		// TODO: wire bridge.WriteStdin(session.ExeID, session.ExeSessionID, data).
		// For v0.61.0 the endpoint contract is testable; full bridge integration
		// lands in a follow-up once Session has the exe-side fields wired.
		resp := ConnectorOKResponse{OK: true}
		respBytes, mErr := json.Marshal(resp)
		if mErr != nil {
			log.Printf("exec-audit: marshal stdin OK: %v", mErr)
			respBytes = []byte(fmt.Sprintf(`{"audit_marshal_failed":%q}`, mErr.Error()))
		}
		writeJSON(w, resp)
		return respBytes, false, ""
	})
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
	wsID := workspaceFromCtx(r.Context())
	userID := userIDFromCtx(r.Context())
	recordCall(s.Recorder, w, wsID, userID, "", "processes.output", nil, func() ([]byte, bool, string) {
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
		resp := ConnectorOutputResponse{
			Chunks:       out,
			ExitCode:     exit,
			SessionAlive: alive,
			Truncated:    sess.LostBytes() > 0,
			LostBytes:    sess.LostBytes(),
		}
		// Output chunks can be large; record size+hash via the Recorder
		// (it truncates above PayloadMaxBytes). Pass marshaled bytes.
		respBytes, mErr := json.Marshal(resp)
		if mErr != nil {
			log.Printf("exec-audit: marshal processes/output: %v", mErr)
			respBytes = []byte(fmt.Sprintf(`{"audit_marshal_failed":%q}`, mErr.Error()))
		}
		writeJSON(w, resp)
		return respBytes, false, ""
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
	wsID := workspaceFromCtx(r.Context())
	userID := userIDFromCtx(r.Context())
	recordCall(s.Recorder, w, wsID, userID, "", "processes.terminate", nil, func() ([]byte, bool, string) {
		// For v0.61.0 mark exit -1; bridge.Terminate(...) wiring lands in
		// a follow-up. The endpoint contract works for the SDK's polling
		// pattern (next GET output sees session_alive=false + exit_code=-1).
		sess.SetExit(-1)
		s.Sessions.Forget(sess.ID)
		resp := ConnectorOKResponse{OK: true}
		respBytes, mErr := json.Marshal(resp)
		if mErr != nil {
			log.Printf("exec-audit: marshal terminate OK: %v", mErr)
			respBytes = []byte(fmt.Sprintf(`{"audit_marshal_failed":%q}`, mErr.Error()))
		}
		writeJSON(w, resp)
		return respBytes, false, ""
	})
}
