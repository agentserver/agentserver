package codexappgateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/agentserver/agentserver/internal/codexappgateway/broker"
	"github.com/agentserver/agentserver/internal/codexappgateway/supervisor"
)

// turnRunner abstracts the broker so the handler is unit-testable
// without spinning up real codex subprocesses.
type turnRunner interface {
	StartThread(ctx context.Context, workspaceID string) (string, error)
	Turn(ctx context.Context, workspaceID, threadID string, params json.RawMessage, timeout time.Duration) (json.RawMessage, error)
}

// turnAPIRequest mirrors the REST request defined in the design spec.
// Field names use camelCase to align 1:1 with codex v2 protocol.
type turnAPIRequest struct {
	WorkspaceID string          `json:"workspaceId" validate:"required" example:"ws-7e7a4f6c"`
	ThreadID    *string         `json:"threadId,omitempty" extensions:"x-nullable=true"`
	Params      json.RawMessage `json:"params" validate:"required" swaggertype:"object"`
	TimeoutMs   int             `json:"timeoutMs,omitempty" example:"300000"`
} // @name TurnAPIRequest

// turnAPIResponse: either Turn (codex Turn raw) OR Transport, never both.
// ThreadID is always populated (existing or newly-created).
type turnAPIResponse struct {
	ThreadID  string          `json:"threadId" validate:"required"`
	Turn      json.RawMessage `json:"turn,omitempty" swaggertype:"object"`
	Transport *transportError `json:"transport,omitempty" extensions:"x-nullable=true"`
} // @name TurnAPIResponse

type transportError struct {
	Code    string `json:"code" validate:"required" example:"brokerTimeout"`
	Message string `json:"message" validate:"required"`
} // @name TurnTransportError

type turnAPIHandler struct {
	runner turnRunner
}

const defaultTurnTimeout = 60 * time.Minute

// turnsSubmitScope is the API-key scope required to call POST /api/turns.
// Mirrored from agentserver's internal/server/api_key_scopes.go — kept as
// a local constant to avoid coupling codex-app-gateway to agentserver's
// package layout. Keep in sync if the scope string ever changes.
const turnsSubmitScope = "turns:submit"

// ServeHTTP handles POST /api/turns. Accepts either a workspace API key
// (Authorization: Bearer wak_<...>) or X-Internal-Secret. The bearer
// path requires the `turns:submit` scope on the presented key.
//
//	@Summary     Submit a codex turn
//	@Description On success, returns either {turn} (codex's raw Turn JSON) or {transport} (a structured transport error). threadId is always populated — when omitted on input, a new thread is created and its id returned. timeoutMs defaults to 300000 (5 minutes) when omitted. Bearer auth requires the `turns:submit` scope; X-Internal-Secret bypasses the scope check.
//	@Tags        Turns
//	@Accept      json
//	@Produce     json
//	@Param       body  body      TurnAPIRequest  true  "Turn submission"
//	@Success     200   {object}  TurnAPIResponse
//	@Failure     400   {string}  string  "invalid json / workspaceId required / params required"
//	@Failure     401   {string}  string  "unauthorized"
//	@Failure     403   {string}  string  "api key not authorized for workspace / missing scope: turns:submit"
//	@Security    WorkspaceAPIKey
//	@Security    InternalSecret
//	@Router      /api/turns [post]
func (h *turnAPIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req turnAPIRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.WorkspaceID == "" {
		http.Error(w, "workspaceId required", http.StatusBadRequest)
		return
	}
	if len(req.Params) == 0 {
		http.Error(w, "params required", http.StatusBadRequest)
		return
	}

	// Bearer-path: enforce that the API key's workspace matches the request body.
	// X-Internal-Secret callers don't have ctxKeyAuthorizedWorkspace set; the
	// check is a no-op for them (v is nil).
	if v := r.Context().Value(ctxKeyAuthorizedWorkspace); v != nil {
		authorizedWS, _ := v.(string)
		if authorizedWS != "" && authorizedWS != req.WorkspaceID {
			http.Error(w, "api key not authorized for workspace "+req.WorkspaceID, http.StatusForbidden)
			return
		}
	}
	// Bearer-path: enforce scope presence. Internal-secret callers bypass.
	if err := requireBearerScope(r, turnsSubmitScope); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	timeout := defaultTurnTimeout
	if req.TimeoutMs > 0 {
		timeout = time.Duration(req.TimeoutMs) * time.Millisecond
	}

	ctx := r.Context()
	resp := turnAPIResponse{}

	threadID := ""
	if req.ThreadID != nil {
		threadID = *req.ThreadID
	}
	if threadID == "" {
		newID, err := h.runner.StartThread(ctx, req.WorkspaceID)
		if err != nil {
			resp.Transport = classifyTransport(err)
			writeJSON(w, resp)
			return
		}
		threadID = newID
	}
	resp.ThreadID = threadID

	rawTurn, err := h.runner.Turn(ctx, req.WorkspaceID, threadID, req.Params, timeout)
	if err != nil {
		resp.Transport = classifyTransport(err)
		writeJSON(w, resp)
		return
	}
	resp.Turn = rawTurn
	writeJSON(w, resp)
}

func classifyTransport(err error) *transportError {
	var te *broker.TimeoutError
	if errors.As(err, &te) {
		return &transportError{Code: "brokerTimeout", Message: te.Error()}
	}
	// Codex returned a JSON-RPC error response (well-formed, not a
	// transport failure). Surface this as its own code so the broker
	// reconnect/restart heuristics in ops dashboards don't fire on
	// what is actually a codex-side application error (e.g. the
	// "thread closing; retry after the thread is closed" race that
	// thread/resume returns under teardown).
	var rpcErr *broker.TurnRPCError
	if errors.As(err, &rpcErr) {
		return &transportError{Code: "codexRPCError", Message: err.Error()}
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "dial"), strings.Contains(msg, "connection refused"), strings.Contains(msg, "subprocess"):
		return &transportError{Code: "subprocessCrash", Message: msg}
	case strings.Contains(msg, "connection closed"), strings.Contains(msg, "ws"):
		return &transportError{Code: "wsDisconnect", Message: msg}
	default:
		return &transportError{Code: "wsDisconnect", Message: msg}
	}
}

func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

// poolRunner adapts *broker.Pool to the turnRunner interface used by
// the handler. Production wiring uses this; tests use fakes.
type poolRunner struct {
	pool *broker.Pool
}

func newPoolRunner(p *broker.Pool) *poolRunner { return &poolRunner{pool: p} }

func (r *poolRunner) StartThread(ctx context.Context, workspaceID string) (string, error) {
	conn, err := r.pool.Get(ctx, workspaceID)
	if err != nil {
		return "", err
	}
	// Issue 3: bump lastUsedAt after the call so the reaper does not kill a
	// connection that was active throughout a long StartThread round-trip.
	defer r.pool.Touch(workspaceID)
	return conn.StartThread(ctx)
}

func (r *poolRunner) Turn(ctx context.Context, workspaceID, threadID string, params json.RawMessage, timeout time.Duration) (json.RawMessage, error) {
	conn, err := r.pool.Get(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	// Issue 3: bump lastUsedAt after Turn so a 4-minute Turn does not miss
	// the 5-minute reap deadline (lastUsedAt was only set on Get, not on
	// completion of the long-running operation).
	defer r.pool.Touch(workspaceID)
	return conn.Turn(ctx, threadID, params, timeout)
}

// makeSupervisorResolver returns a broker.WSURLResolver that uses the
// existing supervisor + buildConfig wiring. Returns the ws URL of the
// loopback codex subprocess for the workspace.
func makeSupervisorResolver(sup *supervisor.Supervisor, build func(context.Context, string, string) (supervisor.SpawnConfig, error)) broker.WSURLResolver {
	return func(ctx context.Context, workspaceID string) (string, error) {
		key := supervisor.Key{WorkspaceID: workspaceID}
		handle, err := sup.EnsureSubprocess(ctx, key, func(loopbackToken string) (supervisor.SpawnConfig, error) {
			return build(ctx, workspaceID, loopbackToken)
		})
		if err != nil {
			return "", err
		}
		return handle.WSURL, nil
	}
}
