package browsergateway

// DSH compatibility facade.
//
// This adapter intentionally lives beside the v2 Browser Gateway instead of
// inside Core.  It translates the DSH Client Remote protocol to the reviewed
// Core user-session/run resources, preserving the existing bearer-token and
// permission-version checks.  It is enabled only when a deployment supplies a
// default workspace id.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/runevent"
	"github.com/google/uuid"
)

const (
	DSHOriginEnvironment         = "AGENTSERVER_V2_DSH_ORIGIN"
	DSHWorkspaceIDEnvironment    = "AGENTSERVER_V2_DSH_WORKSPACE_ID"
	DSHWorkspacePathEnvironment  = "AGENTSERVER_V2_DSH_WORKSPACE_PATH"
	DSHWorkspaceTitleEnvironment = "AGENTSERVER_V2_DSH_WORKSPACE_TITLE"
	DSHHomeEnvironment           = "AGENTSERVER_V2_DSH_HOME"
)

// DSHGatewayConfig controls the optional compatibility facade.
type DSHGatewayConfig struct {
	WorkspaceID    string
	WorkspacePath  string
	WorkspaceTitle string
	Home           string
	AllowedOrigins []string
}

// DSHSessionBackend is the small, reviewed Core surface consumed by the
// compatibility layer.  It deliberately excludes unrelated Core endpoints.
type DSHSessionBackend interface {
	ListSessions(context.Context, string, string) (corecontract.ListUserSessionsResponse, error)
	ListLLMGateways(context.Context, string, string) (corecontract.ListWorkspaceLLMGatewaysResponse, error)
	GetSession(context.Context, string, string, string) (corecontract.UserSessionState, error)
	CreateSession(context.Context, string, string, corecontract.CreateUserSessionRequest) (corecontract.CreateUserSessionResponse, error)
	UpdateSession(context.Context, string, string, string, corecontract.UpdateUserSessionRequest) (corecontract.UpdateUserSessionResponse, error)
	ArchiveSession(context.Context, string, string, string, corecontract.ArchiveUserSessionRequest) (corecontract.ArchiveUserSessionResponse, error)
	UpdatePermissionMode(context.Context, string, string, string, corecontract.UpdateUserSessionPermissionModeRequest) (corecontract.UpdateUserSessionPermissionModeResponse, error)
	UpdateWorkingDirectory(context.Context, string, string, string, corecontract.UpdateUserSessionWorkingDirectoryRequest) (corecontract.UpdateUserSessionWorkingDirectoryResponse, error)
	GetTranscript(context.Context, string, string, string) (corecontract.GetUserSessionTranscriptResponse, error)
	StartRun(context.Context, StartRunRequest) (StartRunResult, error)
	ReadRunEvents(context.Context, ReadRunEventsRequest) (ReadRunEventsResult, error)
	CancelRun(context.Context, CancelRunRequest) (CancelRunResult, error)
	DecideApproval(context.Context, DecideApprovalRequest) (DecideApprovalResult, error)
}

// DSHGateway implements both the HTTP unary Connection route and the
// multiplexed Remote stream WebSocket used by the DSH browser client.
type DSHGateway struct {
	backend DSHSessionBackend
	config  DSHGatewayConfig

	mu            sync.Mutex
	sessions      map[string]*dshSessionState
	eventMu       sync.Mutex
	events        map[int]chan dshRemoteEvent
	nextEvent     int
	approvalMu    sync.Mutex
	approvals     map[string]dshPendingApproval
	workspaceMu   sync.Mutex
	workspaceSubs map[int]chan map[string]any
	nextWorkspace int
}

type dshSessionState struct {
	mu                sync.Mutex
	session           corecontract.UserSessionState
	events            []dshEvent
	nextSeq           int64
	subs              map[int]chan dshSessionUpdate
	nextSub           int
	assistantRevision int64
	runCancel         context.CancelFunc
	builders          map[string]*dshAssistantBuilder
	tools             map[string]*dshToolBuilder
	loaded            bool
	pending           []dshPendingPrompt
}

type dshPendingPrompt struct {
	Bearer    string
	RequestID string
	Text      string
}

type dshAssistantBuilder struct {
	id              string
	attemptID       string
	reasoning       bool
	text            strings.Builder
	turn            int
	step            int
	startedAfterSeq int64
	nextIndex       int
	stream          []map[string]any
}

// dshSessionUpdate preserves the ordering between durable Session events and
// process-local Assistant stream frames.  In particular, a committed
// assistant/message must reach the client before the matching stream/end
// frame, otherwise the DSH client has to rebaseline the stream.
type dshSessionUpdate struct {
	event          *dshEvent
	assistantFrame map[string]any
}

type dshToolBuilder struct {
	id          string
	name        string
	arguments   strings.Builder
	callEmitted bool
}

type dshEvent struct {
	Type      string `json:"type"`
	Seq       int64  `json:"seq"`
	Time      int64  `json:"time"`
	Data      any    `json:"data"`
	SurfaceOp any    `json:"surfaceOp,omitempty"`
}

type dshRemoteEvent struct {
	Name  string
	Args  []any
	Frame map[string]any
}

type dshPendingApproval struct {
	Bearer        string
	WorkspaceID   string
	ApprovalID    string
	Nonce         string
	ContextDigest corecontract.CanonicalJSONDigest
	Version       int64
}

type dshSessionHeader struct {
	Version   int    `json:"version"`
	ID        string `json:"id"`
	CreatedAt int64  `json:"createdAt"`
	Cwd       string `json:"cwd,omitempty"`
	IsSeeded  bool   `json:"isSeeded"`
}

// NewDSHGateway constructs the optional facade.  A blank workspace id is a
// configuration error; callers should simply omit the facade in that case.
func NewDSHGateway(backend DSHSessionBackend, config DSHGatewayConfig) (*DSHGateway, error) {
	if backend == nil {
		return nil, errors.New("DSH backend is required")
	}
	if config.WorkspaceID == "" {
		return nil, errors.New("DSH workspace id is required")
	}
	if _, err := uuid.Parse(config.WorkspaceID); err != nil {
		return nil, errors.New("DSH workspace id must be a UUID")
	}
	for _, origin := range config.AllowedOrigins {
		parsed, err := url.Parse(origin)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, errors.New("DSH allowed origin must be an exact HTTP(S) origin")
		}
	}
	if config.WorkspaceTitle == "" {
		config.WorkspaceTitle = "AgentServer"
	}
	return &DSHGateway{backend: backend, config: config, sessions: make(map[string]*dshSessionState), events: make(map[int]chan dshRemoteEvent), approvals: make(map[string]dshPendingApproval), workspaceSubs: make(map[int]chan map[string]any)}, nil
}

// Routes returns the HTTP handler for DSH unary calls and the stream mux.
func (gateway *DSHGateway) Routes() http.Handler { return gateway }

// ServeHTTP handles /api/<namespace>/<method> and /api/remote.mux.
func (gateway *DSHGateway) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.URL.Path == DSHRemoteMuxPath {
		gateway.serveMux(response, request)
		return
	}
	if !strings.HasPrefix(request.URL.Path, DSHAPIPath) || request.Method != http.MethodPost {
		http.NotFound(response, request)
		return
	}
	if !gateway.originAllowed(request) {
		writeDSHJSON(response, http.StatusForbidden, dshFailure("invalid-request", "gateway/forbidden", "origin not allowed", map[string]any{}))
		return
	}
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]))
	if mediaType != "application/json" {
		writeDSHJSON(response, http.StatusUnsupportedMediaType, dshFailure("invalid-request", "gateway/bad-request", "content type must be application/json", map[string]any{}))
		return
	}
	bearer, err := extractDSHBearer(request.Header)
	if err != nil {
		response.Header().Set("WWW-Authenticate", `Bearer realm="agentserver-browser-api"`)
		writeDSHJSON(response, http.StatusUnauthorized, dshFailure("invalid-request", "gateway/unauthorized", "a single bearer token is required", map[string]any{}))
		return
	}
	secure := request.TLS != nil || strings.EqualFold(request.Header.Get("X-Forwarded-Proto"), "https")
	secureFlag := ""
	if secure {
		secureFlag = "; Secure"
	}
	response.Header().Add("Set-Cookie", "agentserver-bearer="+url.QueryEscape(bearer)+"; Path=/; HttpOnly; SameSite=Strict"+secureFlag)
	raw, err := readBoundedBody(request, 2*1024*1024)
	if err != nil {
		writeDSHJSON(response, http.StatusBadRequest, dshFailure("invalid-request", "gateway/bad-request", err.Error(), map[string]any{}))
		return
	}
	envelope, args, err := parseDSHClientRequest(raw)
	if err != nil {
		rpcID := "invalid-request"
		var partial struct {
			RPCID string `json:"rpcId"`
		}
		if json.Unmarshal(raw, &partial) == nil && partial.RPCID != "" {
			rpcID = partial.RPCID
		}
		writeDSHJSON(response, http.StatusOK, dshFailure(rpcID, "gateway/bad-request", err.Error(), map[string]any{}))
		return
	}
	pathEndpoint := strings.TrimPrefix(request.URL.Path, DSHAPIPath)
	if envelope.Method != pathEndpoint {
		writeDSHJSON(response, http.StatusOK, dshFailure(envelope.RPCID, "gateway/bad-request", "method does not match request path", map[string]any{}))
		return
	}
	value, present, code, message, details := gateway.dispatch(request.Context(), bearer, envelope.Method, args)
	if code != "" {
		writeDSHJSON(response, http.StatusOK, dshFailure(envelope.RPCID, code, message, details))
		return
	}
	writeDSHJSON(response, http.StatusOK, dshSuccess(envelope.RPCID, value, present))
}

func (gateway *DSHGateway) originAllowed(request *http.Request) bool {
	origin := request.Header.Get("Origin")
	if origin == "" {
		return true
	}
	for _, allowed := range gateway.config.AllowedOrigins {
		if allowed != "" && origin == allowed {
			return true
		}
	}
	if len(gateway.config.AllowedOrigins) != 0 {
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	return parsed.Host == request.Host
}

// extractDSHBearer accepts the normal v2 Authorization header and the narrow
// bearer-cookie forms used by browser WebSocket clients (which cannot set an
// Authorization header during the native WebSocket handshake).  It never
// accepts arbitrary query parameters as credentials.
func extractDSHBearer(header http.Header) (string, error) {
	if bearer, err := extractBearer(header); err == nil {
		return bearer, nil
	}
	for _, raw := range header.Values("Cookie") {
		for _, pair := range strings.Split(raw, ";") {
			parts := strings.SplitN(strings.TrimSpace(pair), "=", 2)
			if len(parts) != 2 {
				continue
			}
			name, value := parts[0], parts[1]
			for _, accepted := range []string{"agentserver-bearer", "agentserver_access_token", "access_token"} {
				if name != accepted {
					continue
				}
				decoded, decodeErr := url.QueryUnescape(value)
				if decodeErr == nil && len(decoded) <= 8192 && !strings.ContainsAny(decoded, " \t\r\n\x00") {
					return decoded, nil
				}
			}
		}
	}
	return "", errors.New("a single bearer token is required")
}

func (gateway *DSHGateway) dispatch(ctx context.Context, bearer, endpoint string, args map[string]json.RawMessage) (any, bool, string, string, map[string]any) {
	switch endpoint {
	case "$events/result":
		return gateway.answerRemoteEvent(ctx, args)
	case "session/list":
		result, err := gateway.backend.ListSessions(ctx, bearer, gateway.config.WorkspaceID)
		if err != nil {
			return gateway.dshError(err)
		}
		items := make([]map[string]any, 0, len(result.Sessions))
		for _, session := range result.Sessions {
			items = append(items, gateway.summary(session))
		}
		return map[string]any{"items": items}, true, "", "", nil
	case "session/create":
		var request struct {
			WorkspaceID string `json:"workspaceId"`
			Cwd         string `json:"cwd"`
			SessionID   string `json:"sessionId"`
			AgentPreset string `json:"agentPreset"`
		}
		if err := decodeDSHArg(args, "request", &request); err != nil {
			return nil, false, "gateway/bad-request", err.Error(), nil
		}
		if request.WorkspaceID != "" && request.WorkspaceID != gateway.config.WorkspaceID {
			return nil, false, "workspace/not-found", "the requested workspace is not served by this AgentServer", map[string]any{"workspaceId": request.WorkspaceID}
		}
		if request.Cwd != "" && gateway.config.WorkspacePath != "" && request.Cwd != gateway.config.WorkspacePath {
			return nil, false, "workspace/invalid-path", "the configured AgentServer workspace is immutable", map[string]any{"path": request.Cwd}
		}
		id := request.SessionID
		if id == "" {
			id = uuid.New().String()
		}
		if _, err := uuid.Parse(id); err != nil {
			return nil, false, "gateway/bad-request", "sessionId must be a UUID", nil
		}
		created, err := gateway.backend.CreateSession(ctx, bearer, gateway.config.WorkspaceID, corecontract.CreateUserSessionRequest{SessionID: id, Title: "New session"})
		if err != nil {
			return gateway.dshError(err)
		}
		gateway.installSession(created.Session)
		gateway.emitRemoteEvent("api-session/added", gateway.summary(created.Session))
		gateway.emitWorkspaceUpdate()
		value := map[string]any{"sessionId": created.Session.SessionID}
		if request.AgentPreset != "" {
			value["agentPreset"] = request.AgentPreset
		}
		return value, true, "", "", nil
	case "session/rename":
		var request struct {
			SessionID string `json:"sessionId"`
			Title     string `json:"title"`
		}
		if err := decodeDSHArg(args, "request", &request); err != nil {
			return nil, false, "gateway/bad-request", err.Error(), nil
		}
		state, err := gateway.state(ctx, bearer, request.SessionID)
		if err != nil {
			return gateway.dshError(err)
		}
		state.mu.Lock()
		expectedVersion := state.session.Version
		state.mu.Unlock()
		result, err := gateway.backend.UpdateSession(ctx, bearer, gateway.config.WorkspaceID, request.SessionID, corecontract.UpdateUserSessionRequest{Title: request.Title, ExpectedVersion: expectedVersion})
		if err != nil {
			return gateway.dshError(err)
		}
		gateway.installSession(result.Session)
		seq := state.lastSeq()
		if seq < 0 {
			seq = 0
		}
		return map[string]any{"title": result.Session.Title, "seq": seq}, true, "", "", nil
	case "session/prompt":
		return gateway.prompt(ctx, bearer, args)
	case "session/cancel":
		var request struct {
			SessionID string `json:"sessionId"`
		}
		if err := decodeDSHArg(args, "request", &request); err != nil {
			return nil, false, "gateway/bad-request", err.Error(), nil
		}
		state, err := gateway.state(ctx, bearer, request.SessionID)
		if err != nil {
			return gateway.dshError(err)
		}
		state.mu.Lock()
		activeRunID := state.session.ActiveRunID
		state.mu.Unlock()
		if activeRunID == "" {
			return map[string]any{"accepted": true}, true, "", "", nil
		}
		_, err = gateway.backend.CancelRun(ctx, CancelRunRequest{BearerToken: bearer, WorkspaceID: gateway.config.WorkspaceID, RunID: activeRunID})
		if err != nil {
			return gateway.dshError(err)
		}
		return map[string]any{"accepted": true}, true, "", "", nil
	case "session/selectModel":
		var request struct {
			Provider        string `json:"provider"`
			Model           string `json:"model"`
			ReasoningEffort string `json:"reasoningEffort"`
		}
		if err := decodeDSHArg(args, "request", &request); err != nil {
			return nil, false, "gateway/bad-request", err.Error(), nil
		}
		if request.Provider == "" {
			request.Provider = "codex"
		}
		if request.Model == "" {
			request.Model = "codex"
		}
		selected := map[string]any{"provider": request.Provider, "model": request.Model}
		if request.ReasoningEffort != "" {
			selected["reasoningEffort"] = request.ReasoningEffort
		}
		return map[string]any{"selected": selected}, true, "", "", nil
	case "session/modelCatalog":
		return gateway.modelCatalog(ctx, bearer)
	case "session/projections":
		var request struct {
			SessionID string `json:"sessionId"`
		}
		if err := decodeDSHArg(args, "request", &request); err != nil {
			return nil, false, "gateway/bad-request", err.Error(), nil
		}
		state, err := gateway.state(ctx, bearer, request.SessionID)
		if err != nil {
			return gateway.dshError(err)
		}
		state.mu.Lock()
		permissionMode := state.session.PermissionMode
		state.mu.Unlock()
		return map[string]any{"asOfSeq": state.lastSeq(), "values": map[string]any{"permissions": map[string]any{"currentValue": permissionMode}}}, true, "", "", nil
	case "session/page":
		var request struct {
			Address     map[string]any `json:"address"`
			ThroughSeq  int64          `json:"throughSeq"`
			BeforeSeq   *int64         `json:"beforeSeq"`
			MaxMessages int            `json:"maxMessages"`
		}
		if err := decodeDSHArg(args, "request", &request); err != nil {
			return nil, false, "gateway/bad-request", err.Error(), nil
		}
		id := stringField(request.Address, "sessionId")
		state, err := gateway.state(ctx, bearer, id)
		if err != nil {
			return gateway.dshError(err)
		}
		through := request.ThroughSeq
		records, hasMore := state.page(request.BeforeSeq, &through, request.MaxMessages)
		return map[string]any{"records": records, "hasMore": hasMore}, true, "", "", nil
	case "session/attachment":
		return nil, false, "session/attachment-invalid", "attachments are not exposed by the AgentServer DSH facade", nil
	case "session/updateQueue":
		return nil, false, "session/queue-item-not-found", "queue mutation is not supported by the AgentServer DSH facade", nil
	case "session/fork":
		return nil, false, "session/fork-unavailable", "session fork is not supported by the AgentServer DSH facade", nil
	case "session/search":
		return map[string]any{"items": []any{}, "hasMore": false}, true, "", "", nil
	case "session/initializeDefaultModel":
		return nil, false, "", "", nil
	case "workspace/create":
		var request struct {
			Path string `json:"path"`
		}
		if err := decodeDSHArg(args, "request", &request); err != nil {
			return nil, false, "gateway/bad-request", err.Error(), nil
		}
		if request.Path != "" && request.Path != gateway.config.WorkspacePath {
			return nil, false, "workspace/invalid-path", "the configured AgentServer workspace is immutable", map[string]any{"path": request.Path}
		}
		return map[string]any{"workspace": gateway.workspaceView(), "created": false}, true, "", "", nil
	case "workspace/initializeDefault":
		return map[string]any{"workspace": gateway.workspaceView()}, true, "", "", nil
	case "workspace/rename", "workspace/delete", "workspace/insertBefore", "workspace/insertSessionBefore", "workspace/archiveSession", "workspace/unarchiveSession", "workspace/pinSession", "workspace/unpinSession":
		return gateway.workspaceMutation(endpoint, args)
	case "permissionPresets/catalog":
		return map[string]any{"options": []any{map[string]any{"value": "read-only", "name": "read-only"}, map[string]any{"value": "auto", "name": "auto"}, map[string]any{"value": "full-access", "name": "full-access"}}, "defaultOptions": []any{map[string]any{"value": "read-only", "name": "read-only"}}, "defaultPreset": "read-only"}, true, "", "", nil
	case "agentPresets/list":
		return map[string]any{"presets": []any{}}, true, "", "", nil
	case "settings/describe":
		return map[string]any{"writable": false, "hasDocument": false, "namespaces": []any{}}, true, "", "", nil
	case "credentials/describe":
		return map[string]any{}, true, "", "", nil
	case "llm/listProviders":
		catalog, ok, code, message, details := gateway.modelCatalog(ctx, bearer)
		if !ok {
			return nil, ok, code, message, details
		}
		value, _ := catalog.(map[string]any)
		groups, _ := value["groups"].([]any)
		providers := make([]any, 0, len(groups))
		for _, raw := range groups {
			group, _ := raw.(map[string]any)
			providers = append(providers, map[string]any{"id": group["id"], "name": group["name"]})
		}
		return providers, true, "", "", nil
	case "llm/listConfigurableProviders", "llm/discoverModels":
		return []any{}, true, "", "", nil
	case "skills/list":
		return map[string]any{"skills": []any{}}, true, "", "", nil
	case "terminal/list", "job/list":
		return []any{}, true, "", "", nil
	case "terminal/environment":
		cwd := gateway.config.WorkspacePath
		if cwd == "" {
			cwd = "."
		}
		return map[string]any{"cwd": cwd, "maxInputBytes": 65536, "maxCols": 500, "maxRows": 200, "scrollback": 10000}, true, "", "", nil
	case "terminal/shells":
		return []any{}, true, "", "", nil
	case "schedule/catalog", "schedule/list":
		return []any{}, true, "", "", nil
	case "subagents/list":
		return map[string]any{"entries": []any{}, "parentAvailable": true}, true, "", "", nil
	case "commands/list":
		return []any{map[string]any{"name": "permission", "description": "Switch the permission mode", "input": map[string]any{"hint": "<read-only|auto|full-access>"}}}, true, "", "", nil
	case "commands/execute":
		return gateway.executeCommand(ctx, bearer, args)
	case "dynamicCordisRunner/syncInspectManifest":
		return nil, true, "", "", nil
	case "dynamicCordisRunner/inventory":
		return []any{}, true, "", "", nil
	case "directoryPicker/pick", "directoryPicker/list", "directoryPicker/createDirectory":
		return nil, false, "directory-picker/unavailable", "the AgentServer DSH facade does not expose a local filesystem picker", map[string]any{"capability": "none"}
	case "dynamicCordisRunner/resolveInspectQuery", "dynamicCordisRunner/invoke", "dynamicCordisRunner/reportRenderFailure",
		"dynamicCordisRunner/reportClientGuardFailure", "dynamicCordisRunner/runHostHalf", "dynamicCordisRunner/getClientCode",
		"dynamicCordisRunner/resolveRequestRun", "dynamicCordisRunner/settleUserRun":
		return nil, false, "gateway/service-unavailable", "dynamic Cordis plugins are not available in the AgentServer facade", nil
	default:
		return nil, false, "gateway/service-unavailable", "DSH endpoint is not implemented by this AgentServer facade", map[string]any{"endpoint": endpoint}
	}
}

func (gateway *DSHGateway) answerRemoteEvent(ctx context.Context, args map[string]json.RawMessage) (any, bool, string, string, map[string]any) {
	var result struct {
		EventID string `json:"eventId"`
		Outcome struct {
			Kind  string `json:"kind"`
			Value string `json:"value"`
		} `json:"outcome"`
	}
	if err := decodeDSHArgs(args, &result); err != nil {
		return nil, false, "gateway/bad-request", err.Error(), nil
	}
	gateway.approvalMu.Lock()
	pending, ok := gateway.approvals[result.EventID]
	if ok {
		delete(gateway.approvals, result.EventID)
	}
	gateway.approvalMu.Unlock()
	if !ok {
		return nil, false, "gateway/bad-request", "Remote event is no longer pending", nil
	}
	decision := "deny"
	if result.Outcome.Kind == "result" && result.Outcome.Value == "allowed-once" {
		decision = "approve"
	}
	_, err := gateway.backend.DecideApproval(ctx, DecideApprovalRequest{
		BearerToken: pending.Bearer, WorkspaceID: pending.WorkspaceID, ApprovalID: pending.ApprovalID,
		Decision: decision, Nonce: pending.Nonce, ContextDigest: pending.ContextDigest,
		ExpectedApprovalVersion: pending.Version,
	})
	if err != nil {
		return gateway.dshError(err)
	}
	return nil, false, "", "", nil
}

func (gateway *DSHGateway) prompt(ctx context.Context, bearer string, args map[string]json.RawMessage) (any, bool, string, string, map[string]any) {
	var request struct {
		RequestID string `json:"requestId"`
		SessionID string `json:"sessionId"`
		Mode      string `json:"mode"`
		Content   []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := decodeDSHArg(args, "request", &request); err != nil {
		return nil, false, "gateway/bad-request", err.Error(), nil
	}
	if request.SessionID == "" || len(request.Content) == 0 {
		return nil, false, "gateway/bad-request", "prompt requires sessionId and content", nil
	}
	if request.Mode != "queue" && request.Mode != "steer" {
		return nil, false, "gateway/bad-request", "prompt mode must be queue or steer", nil
	}
	if request.RequestID == "" {
		request.RequestID = uuid.New().String()
	}
	var prompt strings.Builder
	for _, part := range request.Content {
		if part.Type == "text" {
			prompt.WriteString(part.Text)
		}
	}
	if strings.TrimSpace(prompt.String()) == "" {
		return nil, false, "gateway/bad-request", "prompt text is empty", nil
	}
	state, err := gateway.state(ctx, bearer, request.SessionID)
	if err != nil {
		return gateway.dshError(err)
	}
	state.mu.Lock()
	activeRunID := state.session.ActiveRunID
	state.mu.Unlock()
	if activeRunID != "" {
		state.appendSyntheticUser(prompt.String(), request.RequestID)
		state.mu.Lock()
		state.pending = append(state.pending, dshPendingPrompt{Bearer: bearer, RequestID: request.RequestID, Text: prompt.String()})
		runID := state.session.ActiveRunID
		state.mu.Unlock()
		if request.Mode == "steer" {
			_, _ = gateway.backend.CancelRun(ctx, CancelRunRequest{BearerToken: bearer, WorkspaceID: gateway.config.WorkspaceID, RunID: runID})
		}
		gateway.emitRemoteEvent("api-session/activity", request.SessionID, time.Now().UnixMilli())
		return map[string]any{"accepted": true}, true, "", "", nil
	}
	state.mu.Lock()
	permissionVersion := state.session.PermissionModeVersion
	workingVersion := state.session.WorkingDirectoryVersion
	state.mu.Unlock()
	result, err := gateway.backend.StartRun(ctx, StartRunRequest{BearerToken: bearer, WorkspaceID: gateway.config.WorkspaceID, SessionID: request.SessionID, IdempotencyKey: boundedIdempotency(request.RequestID), ClientRunID: request.RequestID, Prompt: prompt.String(), ExpectedPermissionModeVersion: permissionVersion, ExpectedWorkingDirectoryVersion: workingVersion})
	if err != nil {
		return gateway.dshError(err)
	}
	state.appendSyntheticUser(prompt.String(), request.RequestID)
	state.mu.Lock()
	state.session.ActiveRunID = result.RunID
	state.mu.Unlock()
	gateway.emitRemoteEvent("api-session/status", request.SessionID, true)
	gateway.emitRemoteEvent("api-session/activity", request.SessionID, time.Now().UnixMilli())
	gateway.startRunPoller(state, bearer, result)
	return map[string]any{"accepted": true}, true, "", "", nil
}

func (gateway *DSHGateway) executeCommand(ctx context.Context, bearer string, args map[string]json.RawMessage) (any, bool, string, string, map[string]any) {
	var request struct {
		AgentID              string `json:"agentId"`
		Line                 string `json:"line"`
		SubmittedAttachments []any  `json:"submittedAttachments"`
	}
	if err := decodeDSHArgs(args, &request); err != nil {
		return nil, false, "gateway/bad-request", err.Error(), nil
	}
	if request.AgentID == "" || request.Line == "" {
		return nil, false, "gateway/bad-request", "agentId and line are required", nil
	}
	line := strings.TrimSpace(request.Line)
	if !strings.HasPrefix(line, "/permission") {
		return nil, false, "gateway/bad-request", "only /permission is supported", nil
	}
	mode := strings.TrimSpace(strings.TrimPrefix(line, "/permission"))
	if mode == "" {
		return nil, false, "gateway/bad-request", "permission mode is required", nil
	}
	if mode != "read-only" && mode != "auto" && mode != "full-access" {
		return nil, false, "gateway/bad-request", "unknown permission mode", nil
	}
	state, err := gateway.state(ctx, bearer, request.AgentID)
	if err != nil {
		return gateway.dshError(err)
	}
	state.mu.Lock()
	expectedPermissionVersion := state.session.PermissionModeVersion
	state.mu.Unlock()
	result, err := gateway.backend.UpdatePermissionMode(ctx, bearer, gateway.config.WorkspaceID, request.AgentID, corecontract.UpdateUserSessionPermissionModeRequest{PermissionMode: mode, ExpectedPermissionModeVersion: expectedPermissionVersion})
	if err != nil {
		return gateway.dshError(err)
	}
	gateway.installSession(result.Session)
	state.mu.Lock()
	state.appendLocked("permission/preset", time.Now().UnixMilli(), map[string]any{"preset": mode})
	state.mu.Unlock()
	return map[string]any{"commandId": uuid.New().String(), "result": map[string]any{"kind": "success", "text": "permission " + mode}}, true, "", "", nil
}

func (gateway *DSHGateway) emitRemoteEvent(name string, args ...any) {
	gateway.eventMu.Lock()
	defer gateway.eventMu.Unlock()
	for _, subscriber := range gateway.events {
		select {
		case subscriber <- dshRemoteEvent{Name: name, Args: append([]any(nil), args...)}:
		default:
		}
	}
}

func (gateway *DSHGateway) emitRemoteFrame(frame map[string]any) {
	gateway.eventMu.Lock()
	defer gateway.eventMu.Unlock()
	for _, subscriber := range gateway.events {
		select {
		case subscriber <- dshRemoteEvent{Frame: frame}:
		default:
		}
	}
}

func (gateway *DSHGateway) publishApproval(event runevent.Event, bearer string) {
	decoded, err := runevent.DecodeSemanticPayload(event)
	if err != nil {
		return
	}
	approval := decoded.(runevent.ApprovalPayload)
	eventID := uuid.New().String()
	gateway.approvalMu.Lock()
	gateway.approvals[eventID] = dshPendingApproval{
		Bearer: bearer, WorkspaceID: event.WorkspaceID, ApprovalID: approval.ApprovalID,
		Nonce: approval.Nonce, Version: approval.Version,
		ContextDigest: corecontract.CanonicalJSONDigest{Domain: "approval-context", CanonicalizerVersion: "rfc8785-v1", SHA256: approval.ContextSHA256},
	}
	gateway.approvalMu.Unlock()
	gateway.emitRemoteFrame(map[string]any{
		"type": "waterfall", "event": "approval/request", "eventId": eventID, "agentId": event.SessionID,
		"request": map[string]any{"toolName": approval.ToolName, "reason": "AgentServer executor requested approval"},
	})
}

func (gateway *DSHGateway) cancelApproval(event runevent.Event) {
	decoded, err := runevent.DecodeSemanticPayload(event)
	if err != nil {
		return
	}
	approvalID := decoded.(runevent.ApprovalPayload).ApprovalID
	gateway.approvalMu.Lock()
	pendingIDs := make([]string, 0)
	for eventID, pending := range gateway.approvals {
		if pending.ApprovalID == approvalID {
			delete(gateway.approvals, eventID)
			pendingIDs = append(pendingIDs, eventID)
		}
	}
	gateway.approvalMu.Unlock()
	for _, eventID := range pendingIDs {
		gateway.emitRemoteFrame(map[string]any{"type": "cancel", "eventId": eventID})
	}
}

func (gateway *DSHGateway) subscribeRemoteEvents(ctx context.Context) (<-chan dshRemoteEvent, func()) {
	gateway.eventMu.Lock()
	id := gateway.nextEvent
	gateway.nextEvent++
	channel := make(chan dshRemoteEvent, 128)
	gateway.events[id] = channel
	gateway.eventMu.Unlock()
	stop := func() {
		gateway.eventMu.Lock()
		if current, ok := gateway.events[id]; ok {
			delete(gateway.events, id)
			close(current)
		}
		gateway.eventMu.Unlock()
	}
	go func() { <-ctx.Done(); stop() }()
	return channel, stop
}

func (gateway *DSHGateway) emitWorkspaceUpdate() {
	frame := map[string]any{"type": "upsert", "workspace": gateway.workspaceView()}
	gateway.workspaceMu.Lock()
	defer gateway.workspaceMu.Unlock()
	for _, subscriber := range gateway.workspaceSubs {
		select {
		case subscriber <- frame:
		default:
		}
	}
}

func (gateway *DSHGateway) subscribeWorkspace(ctx context.Context) (<-chan map[string]any, func()) {
	gateway.workspaceMu.Lock()
	id := gateway.nextWorkspace
	gateway.nextWorkspace++
	channel := make(chan map[string]any, 64)
	gateway.workspaceSubs[id] = channel
	gateway.workspaceMu.Unlock()
	stop := func() {
		gateway.workspaceMu.Lock()
		if current, ok := gateway.workspaceSubs[id]; ok {
			delete(gateway.workspaceSubs, id)
			close(current)
		}
		gateway.workspaceMu.Unlock()
	}
	go func() { <-ctx.Done(); stop() }()
	return channel, stop
}

func decodeDSHArgs(args map[string]json.RawMessage, destination any) error {
	object := make(map[string]json.RawMessage, len(args))
	for key, raw := range args {
		object[key] = raw
	}
	raw, err := json.Marshal(object)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, destination)
}

func (gateway *DSHGateway) workspaceMutation(endpoint string, args map[string]json.RawMessage) (any, bool, string, string, map[string]any) {
	if endpoint == "workspace/rename" {
		gateway.emitWorkspaceUpdate()
	}
	switch endpoint {
	case "workspace/insertBefore":
		return map[string]any{"workspaceIds": []string{gateway.config.WorkspaceID}}, true, "", "", nil
	case "workspace/archiveSession":
		return map[string]any{"archivedSessionIds": []string{}}, true, "", "", nil
	case "workspace/pinSession", "workspace/unpinSession":
		return map[string]any{"pinnedSessionIds": []string{}}, true, "", "", nil
	case "workspace/delete":
		return nil, false, "workspace/immutable", "the AgentServer workspace cannot be deleted through DSH", nil
	default:
		return map[string]any{"workspace": gateway.workspaceView()}, true, "", "", nil
	}
}

func (gateway *DSHGateway) workspaceView() map[string]any {
	gateway.mu.Lock()
	sessionIDs := make([]string, 0, len(gateway.sessions))
	for id := range gateway.sessions {
		sessionIDs = append(sessionIDs, id)
	}
	gateway.mu.Unlock()
	sort.Strings(sessionIDs)
	return map[string]any{"workspaceId": gateway.config.WorkspaceID, "path": gateway.config.WorkspacePath, "title": gateway.config.WorkspaceTitle, "sessionIds": sessionIDs, "createdAt": time.Now().UTC().Format(time.RFC3339Nano), "updatedAt": time.Now().UTC().Format(time.RFC3339Nano)}
}

func (gateway *DSHGateway) summary(session corecontract.UserSessionState) map[string]any {
	row := map[string]any{"agentAvailable": true, "sessionId": session.SessionID, "updatedAt": session.UpdatedAt.UnixMilli(), "running": session.ActiveRunID != "", "blank": session.Version <= 1}
	if session.WorkingDirectory != "" && session.WorkingDirectory != "." {
		row["cwd"] = session.WorkingDirectory
	}
	return row
}

// modelCatalog projects the one Core workspace LLM route that DSH can use.
// Core freezes the gateway and model into each run, so advertising the live
// workspace default is both more useful and more honest than the old
// codex/codex placeholder. The DSH client still gets the standard provider
// group shape and can render its model selector without knowing Core's
// workspace-gateway implementation detail.
func (gateway *DSHGateway) modelCatalog(ctx context.Context, bearer string) (any, bool, string, string, map[string]any) {
	gateways, err := gateway.backend.ListLLMGateways(ctx, bearer, gateway.config.WorkspaceID)
	if err != nil {
		return gateway.dshError(err)
	}
	var selected *corecontract.WorkspaceLLMGatewayState
	for index := range gateways.Gateways {
		candidate := &gateways.Gateways[index]
		if candidate.Status == "active" && candidate.Default && candidate.GrantStatus == "active" && candidate.DefaultModel != "" {
			selected = candidate
			break
		}
	}
	base := map[string]any{
		"default":           map[string]any{"provider": corecontract.WorkspaceLLMGatewayProvider, "model": ""},
		"routableProviders": []string{},
		"groups":            []any{},
		"failures":          []any{},
	}
	if selected == nil {
		base["failures"] = []any{map[string]any{
			"id": "workspace-gateway", "name": "Workspace Gateway",
			"message": "workspace has no active default LLM gateway model",
		}}
		return base, true, "", "", nil
	}
	base["default"] = map[string]any{"provider": corecontract.WorkspaceLLMGatewayProvider, "model": selected.DefaultModel}
	base["routableProviders"] = []string{corecontract.WorkspaceLLMGatewayProvider}
	base["groups"] = []any{map[string]any{
		"id":     corecontract.WorkspaceLLMGatewayProvider,
		"name":   nonEmptyDSHText(selected.Name, "Workspace Gateway"),
		"models": []any{map[string]any{"id": selected.DefaultModel, "name": selected.DefaultModel}},
	}}
	return base, true, "", "", nil
}

func nonEmptyDSHText(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func (gateway *DSHGateway) installSession(session corecontract.UserSessionState) *dshSessionState {
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	state := gateway.sessions[session.SessionID]
	if state == nil {
		state = &dshSessionState{session: session, nextSeq: 0, subs: map[int]chan dshSessionUpdate{}, builders: map[string]*dshAssistantBuilder{}, tools: map[string]*dshToolBuilder{}}
		gateway.sessions[session.SessionID] = state
	} else {
		state.mu.Lock()
		state.session = session
		state.mu.Unlock()
	}
	return state
}

func (gateway *DSHGateway) state(ctx context.Context, bearer, id string) (*dshSessionState, error) {
	gateway.mu.Lock()
	if state := gateway.sessions[id]; state != nil {
		gateway.mu.Unlock()
		state.mu.Lock()
		loaded := state.loaded
		state.mu.Unlock()
		if !loaded {
			transcript, err := gateway.backend.GetTranscript(ctx, bearer, gateway.config.WorkspaceID, id)
			if err == nil {
				state.loadTranscript(transcript)
			}
			state.mu.Lock()
			state.loaded = true
			state.mu.Unlock()
		}
		return state, nil
	}
	gateway.mu.Unlock()
	session, err := gateway.backend.GetSession(ctx, bearer, gateway.config.WorkspaceID, id)
	if err != nil {
		return nil, err
	}
	state := gateway.installSession(session)
	transcript, err := gateway.backend.GetTranscript(ctx, bearer, gateway.config.WorkspaceID, id)
	if err == nil {
		state.loadTranscript(transcript)
	}
	state.mu.Lock()
	state.loaded = true
	state.mu.Unlock()
	return state, nil
}

func (gateway *DSHGateway) startRunPoller(state *dshSessionState, bearer string, started StartRunResult) {
	state.mu.Lock()
	if state.runCancel != nil {
		state.runCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	state.runCancel = cancel
	sessionID := state.session.SessionID
	state.mu.Unlock()
	go func() {
		cursor := started.Cursor
		errorsSeen := 0
		defer cancel()
		for {
			batch, err := gateway.backend.ReadRunEvents(ctx, ReadRunEventsRequest{BearerToken: bearer, WorkspaceID: gateway.config.WorkspaceID, SessionID: sessionID, RunID: started.RunID, After: cursor, Limit: 128, Wait: 10 * time.Second})
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				errorsSeen++
				if errorsSeen >= 3 {
					state.mu.Lock()
					state.session.ActiveRunID = ""
					state.mu.Unlock()
					gateway.emitRemoteEvent("api-session/error", sessionID, err.Error())
					gateway.emitRemoteEvent("api-session/status", sessionID, false)
					return
				}
				time.Sleep(250 * time.Millisecond)
				continue
			}
			errorsSeen = 0
			for i, event := range batch.Events {
				state.mapCanonical(event)
				if event.Kind == runevent.KindApprovalRequested {
					gateway.publishApproval(event, bearer)
				}
				if event.Kind == runevent.KindApprovalApproved || event.Kind == runevent.KindApprovalDenied || event.Kind == runevent.KindApprovalExpired || event.Kind == runevent.KindApprovalCancelled || event.Kind == runevent.KindApprovalConsumed {
					gateway.cancelApproval(event)
				}
				if i < len(batch.EventCursors) {
					cursor = batch.EventCursors[i]
				}
			}
			if len(batch.Events) == 0 {
				continue
			}
			if state.terminal() {
				gateway.emitRemoteEvent("api-session/status", sessionID, false)
				gateway.startNextPending(state)
				return
			}
		}
	}()
}

func (gateway *DSHGateway) startNextPending(state *dshSessionState) {
	state.mu.Lock()
	if state.session.ActiveRunID != "" || len(state.pending) == 0 {
		state.mu.Unlock()
		return
	}
	next := state.pending[0]
	state.pending = state.pending[1:]
	sessionID := state.session.SessionID
	permissionVersion := state.session.PermissionModeVersion
	workingVersion := state.session.WorkingDirectoryVersion
	state.mu.Unlock()
	result, err := gateway.backend.StartRun(context.Background(), StartRunRequest{
		BearerToken: next.Bearer, WorkspaceID: gateway.config.WorkspaceID, SessionID: sessionID,
		IdempotencyKey: boundedIdempotency(next.RequestID), ClientRunID: next.RequestID, Prompt: next.Text,
		ExpectedPermissionModeVersion: permissionVersion, ExpectedWorkingDirectoryVersion: workingVersion,
	})
	if err != nil {
		state.mu.Lock()
		state.session.ActiveRunID = ""
		state.mu.Unlock()
		return
	}
	state.mu.Lock()
	state.session.ActiveRunID = result.RunID
	state.mu.Unlock()
	gateway.emitRemoteEvent("api-session/status", sessionID, true)
	gateway.startRunPoller(state, next.Bearer, result)
}

func (gateway *DSHGateway) mapError(err error) (string, string, map[string]any) {
	var public *BackendHTTPError
	if errors.As(err, &public) {
		if public.Code != "" {
			code := public.Code
			switch public.Code {
			case "not_found":
				code = "session/not-found"
			case "conflict", "active_run":
				code = "session/agent-busy"
			case "invalid_argument":
				code = "gateway/bad-request"
			case "forbidden":
				code = "gateway/forbidden"
			case "unauthorized":
				code = "gateway/unauthorized"
			}
			return code, public.Message, map[string]any{}
		}
	}
	return "gateway/internal", err.Error(), map[string]any{}
}

func (gateway *DSHGateway) dshError(err error) (any, bool, string, string, map[string]any) {
	code, message, details := gateway.mapError(err)
	return nil, false, code, message, details
}

func decodeDSHArg(args map[string]json.RawMessage, name string, destination any) error {
	raw, ok := args[name]
	if !ok {
		return fmt.Errorf("missing argument %q", name)
	}
	if err := json.Unmarshal(raw, destination); err != nil {
		return fmt.Errorf("invalid argument %q: %w", name, err)
	}
	return nil
}
func stringField(object map[string]any, key string) string {
	if value, ok := object[key].(string); ok {
		return value
	}
	return ""
}
func numberField(object map[string]any, key string) int64 {
	switch value := object[key].(type) {
	case int:
		return int64(value)
	case int64:
		return value
	case float64:
		return int64(value)
	default:
		return 0
	}
}
func boundedIdempotency(value string) string {
	if value == "" {
		return uuid.New().String()
	}
	value = strings.Map(func(r rune) rune {
		if r < 0x21 || r > 0x7e {
			return -1
		}
		return r
	}, value)
	if len(value) > 256 {
		return value[:256]
	}
	return value
}

func writeDSHJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
