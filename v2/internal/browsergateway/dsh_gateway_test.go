package browsergateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/runevent"
)

type dshFakeBackend struct {
	sessions []corecontract.UserSessionState
	created  corecontract.CreateUserSessionResponse
	mode     corecontract.UpdateUserSessionPermissionModeResponse
	journal  []corecontract.UserSessionJournalEntry
}

func (fake *dshFakeBackend) ListSessions(context.Context, string, string) (corecontract.ListUserSessionsResponse, error) {
	return corecontract.ListUserSessionsResponse{Sessions: append([]corecontract.UserSessionState(nil), fake.sessions...)}, nil
}
func (fake *dshFakeBackend) GetSession(context.Context, string, string, string) (corecontract.UserSessionState, error) {
	if len(fake.sessions) == 0 {
		return corecontract.UserSessionState{}, nil
	}
	return fake.sessions[0], nil
}
func (fake *dshFakeBackend) CreateSession(_ context.Context, _, _ string, input corecontract.CreateUserSessionRequest) (corecontract.CreateUserSessionResponse, error) {
	now := time.Now().UTC()
	state := corecontract.UserSessionState{SessionID: input.SessionID, Title: input.Title, Status: "active", Version: 1, PermissionMode: "read-only", PermissionModeVersion: 1, WorkingDirectory: ".", WorkingDirectoryVersion: 1, CreatedAt: now, UpdatedAt: now}
	fake.sessions = append(fake.sessions, state)
	fake.created = corecontract.CreateUserSessionResponse{Session: state, Created: true}
	return fake.created, nil
}
func (fake *dshFakeBackend) UpdateSession(_ context.Context, _ string, _ string, _ string, request corecontract.UpdateUserSessionRequest) (corecontract.UpdateUserSessionResponse, error) {
	state := fake.sessions[0]
	state.Title = request.Title
	state.TitleSource = "manual"
	state.TitleVersion++
	state.Version++
	fake.sessions[0] = state
	fake.journal = append(fake.journal, corecontract.UserSessionJournalEntry{Seq: int64(len(fake.journal) + 1), Kind: "title", CreatedAt: time.Now(), Title: state.Title, TitleSource: state.TitleSource, TitleVersion: state.TitleVersion})
	return corecontract.UpdateUserSessionResponse{Session: state, Changed: true}, nil
}
func (fake *dshFakeBackend) ArchiveSession(context.Context, string, string, string, corecontract.ArchiveUserSessionRequest) (corecontract.ArchiveUserSessionResponse, error) {
	return corecontract.ArchiveUserSessionResponse{}, nil
}
func (fake *dshFakeBackend) UpdatePermissionMode(_ context.Context, _, _, _ string, input corecontract.UpdateUserSessionPermissionModeRequest) (corecontract.UpdateUserSessionPermissionModeResponse, error) {
	state := fake.sessions[0]
	state.PermissionMode = input.PermissionMode
	state.PermissionModeVersion++
	fake.sessions[0] = state
	fake.journal = append(fake.journal, corecontract.UserSessionJournalEntry{Seq: int64(len(fake.journal) + 1), Kind: "permission", CreatedAt: time.Now(), PermissionMode: state.PermissionMode, PermissionVersion: state.PermissionModeVersion})
	fake.mode = corecontract.UpdateUserSessionPermissionModeResponse{Session: state, Changed: true}
	return fake.mode, nil
}
func (fake *dshFakeBackend) UpdateWorkingDirectory(context.Context, string, string, string, corecontract.UpdateUserSessionWorkingDirectoryRequest) (corecontract.UpdateUserSessionWorkingDirectoryResponse, error) {
	return corecontract.UpdateUserSessionWorkingDirectoryResponse{}, nil
}
func (fake *dshFakeBackend) GetTranscript(context.Context, string, string, string) (corecontract.GetUserSessionTranscriptResponse, error) {
	return corecontract.GetUserSessionTranscriptResponse{}, nil
}

func (fake *dshFakeBackend) GetJournal(_ context.Context, _ string, workspaceID, sessionID string, cursor int64) (corecontract.UserSessionJournalPage, error) {
	state := corecontract.UserSessionState{}
	if len(fake.sessions) > 0 {
		state = fake.sessions[0]
	}
	state.WorkspaceID, state.SessionID = workspaceID, sessionID
	if len(fake.journal) == 0 {
		mode := state.PermissionMode
		if mode == "" {
			mode = "read-only"
		}
		version := state.PermissionModeVersion
		if version < 1 {
			version = 1
		}
		fake.journal = []corecontract.UserSessionJournalEntry{{Seq: 1, Kind: "permission", CreatedAt: state.CreatedAt, PermissionMode: mode, PermissionVersion: version}}
	}
	return corecontract.UserSessionJournalPage{Session: state, Entries: fake.journal[cursor:], Cursor: int64(len(fake.journal))}, nil
}
func (fake *dshFakeBackend) StartRun(context.Context, StartRunRequest) (StartRunResult, error) {
	return StartRunResult{}, nil
}
func (fake *dshFakeBackend) ReadRunEvents(context.Context, ReadRunEventsRequest) (ReadRunEventsResult, error) {
	return ReadRunEventsResult{}, nil
}
func (fake *dshFakeBackend) CancelRun(context.Context, CancelRunRequest) (CancelRunResult, error) {
	return CancelRunResult{}, nil
}
func (fake *dshFakeBackend) DecideApproval(context.Context, DecideApprovalRequest) (DecideApprovalResult, error) {
	return DecideApprovalResult{}, nil
}

func newDSHTestGateway(t *testing.T, fake *dshFakeBackend) *DSHGateway {
	t.Helper()
	gateway, err := NewDSHGateway(fake, DSHGatewayConfig{WorkspaceID: "40000000-0000-4000-8000-000000000004"})
	if err != nil {
		t.Fatal(err)
	}
	return gateway
}

func dshRequest(t *testing.T, gateway *DSHGateway, method string, args string) map[string]any {
	t.Helper()
	body := `{"type":"client-request","rpcId":"test-rpc","method":"` + method + `","payload":{"args":` + args + `}}`
	request := httptest.NewRequest(http.MethodPost, DSHAPIPath+method, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer user-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, request)
	var envelope map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("response = %s: %v", response.Body, err)
	}
	return envelope
}

func TestDSHGatewayServesConnectionEnvelopeAndSessionList(t *testing.T) {
	fake := &dshFakeBackend{sessions: []corecontract.UserSessionState{{SessionID: "41000000-0000-4000-8000-000000000004", Version: 1, PermissionMode: "read-only", UpdatedAt: time.Now().UTC()}}}
	gateway := newDSHTestGateway(t, fake)
	envelope := dshRequest(t, gateway, "session/list", `{}`)
	result := envelope["result"].(map[string]any)
	if result["ok"] != true {
		t.Fatalf("session/list result = %#v", result)
	}
	value := result["value"].(map[string]any)
	if len(value["items"].([]any)) != 1 {
		t.Fatalf("session/list value = %#v", value)
	}
}

func TestDSHGatewayModelCatalogProjectsActiveWorkspaceGateway(t *testing.T) {
	fake := &dshFakeBackend{}
	gateway := newDSHTestGateway(t, fake)
	envelope := dshRequest(t, gateway, "session/modelCatalog", `{}`)
	result := envelope["result"].(map[string]any)
	if result["ok"] != true {
		t.Fatalf("model catalog result = %#v", result)
	}
	catalog := result["value"].(map[string]any)
	if catalog["default"].(map[string]any)["provider"] != corecontract.WorkspaceLLMGatewayProvider ||
		catalog["default"].(map[string]any)["model"] != "gpt-5.6-sol" {
		t.Fatalf("model catalog default = %#v", catalog["default"])
	}
	groups := catalog["groups"].([]any)
	if len(groups) != 1 || groups[0].(map[string]any)["id"] != corecontract.WorkspaceLLMGatewayProvider {
		t.Fatalf("model catalog groups = %#v", groups)
	}
	models := groups[0].(map[string]any)["models"].([]any)
	if len(models) != 1 || models[0].(map[string]any)["id"] != "gpt-5.6-sol" {
		t.Fatalf("model catalog models = %#v", models)
	}
	providers := dshRequest(t, gateway, "llm/listProviders", `{}`)["result"].(map[string]any)["value"].([]any)
	if len(providers) != 1 || providers[0].(map[string]any)["id"] != corecontract.WorkspaceLLMGatewayProvider {
		t.Fatalf("llm providers = %#v", providers)
	}
}

func TestDSHGatewaySessionProjectionsSeedModelSelection(t *testing.T) {
	fake := &dshFakeBackend{sessions: []corecontract.UserSessionState{{
		SessionID: "41000000-0000-4000-8000-000000000004", Version: 1,
		PermissionMode: "read-only", UpdatedAt: time.Now().UTC(),
	}}}
	gateway := newDSHTestGateway(t, fake)
	envelope := dshRequest(t, gateway, "session/projections", `{"request":{"sessionId":"41000000-0000-4000-8000-000000000004"}}`)
	result := envelope["result"].(map[string]any)
	if result["ok"] != true {
		t.Fatalf("session projections failed: %#v", result)
	}
	values := result["value"].(map[string]any)["values"].(map[string]any)
	selection := values["modelSelection"].(map[string]any)["next"].(map[string]any)
	if selection["provider"] != corecontract.WorkspaceLLMGatewayProvider || selection["model"] != "gpt-5.6-sol" {
		t.Fatalf("model selection projection = %#v", selection)
	}
}

func TestDSHGatewayPermissionCommandUsesCoreCAS(t *testing.T) {
	fake := &dshFakeBackend{sessions: []corecontract.UserSessionState{{SessionID: "41000000-0000-4000-8000-000000000004", Version: 1, PermissionMode: "read-only", PermissionModeVersion: 1, UpdatedAt: time.Now().UTC()}}}
	gateway := newDSHTestGateway(t, fake)
	envelope := dshRequest(t, gateway, "commands/execute", `{"agentId":"41000000-0000-4000-8000-000000000004","line":"/permission auto","submittedAttachments":[]}`)
	result := envelope["result"].(map[string]any)
	if result["ok"] != true || fake.mode.Session.PermissionMode != "auto" {
		t.Fatalf("permission result = %#v, mode = %#v", result, fake.mode)
	}
}

func TestDSHGatewayRejectsMissingBearer(t *testing.T) {
	gateway := newDSHTestGateway(t, &dshFakeBackend{})
	request := httptest.NewRequest(http.MethodPost, DSHAPIPath+"session/list", strings.NewReader(`{"type":"client-request"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestDSHGatewaySetsBearerCookieForBrowserWebSocketBootstrap(t *testing.T) {
	gateway := newDSHTestGateway(t, &dshFakeBackend{})
	request := httptest.NewRequest(http.MethodPost, DSHAPIPath+"session/list", strings.NewReader(`{"type":"client-request","rpcId":"r","method":"session/list","payload":{"args":{}}}`))
	request.Header.Set("Authorization", "Bearer browser-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	gateway.ServeHTTP(response, request)
	if !strings.Contains(response.Header().Get("Set-Cookie"), "agentserver-bearer=browser-token") {
		t.Fatalf("set-cookie = %q", response.Header().Get("Set-Cookie"))
	}
}

func TestDSHGatewayProvidesNoopInspectionCatalogAndPickerCapabilityError(t *testing.T) {
	gateway := newDSHTestGateway(t, &dshFakeBackend{})
	for _, test := range []struct {
		method string
		want   any
	}{
		{method: "dynamicCordisRunner/syncInspectManifest", want: nil},
		{method: "dynamicCordisRunner/inventory", want: []any{}},
	} {
		envelope := dshRequest(t, gateway, test.method, `{}`)
		result := envelope["result"].(map[string]any)
		if result["ok"] != true {
			t.Fatalf("%s result = %#v", test.method, result)
		}
	}
	envelope := dshRequest(t, gateway, "directoryPicker/list", `{"path":"/workspace"}`)
	result := envelope["result"].(map[string]any)
	if result["ok"] != false || result["error"].(map[string]any)["code"] != "directory-picker/unavailable" {
		t.Fatalf("directory picker result = %#v", result)
	}
}

func TestDSHGatewayRejectsMalformedAllowedOrigin(t *testing.T) {
	if _, err := NewDSHGateway(&dshFakeBackend{}, DSHGatewayConfig{WorkspaceID: "40000000-0000-4000-8000-000000000004", AllowedOrigins: []string{"https://example.test/path"}}); err == nil {
		t.Fatal("malformed DSH origin was accepted")
	}
}

func TestDSHGatewayFollowIncludesAssistantBaselineWhenRequested(t *testing.T) {
	fake := &dshFakeBackend{sessions: []corecontract.UserSessionState{{SessionID: "41000000-0000-4000-8000-000000000004", Version: 1, PermissionMode: "read-only", UpdatedAt: time.Now().UTC()}}}
	gateway := newDSHTestGateway(t, fake)
	args := map[string]json.RawMessage{
		"request": json.RawMessage(`{"address":{"kind":"session","sessionId":"41000000-0000-4000-8000-000000000004"},"assistantStream":true}`),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := gateway.openStream(ctx, "user-token", args, "session/follow")
	if err != nil {
		t.Fatal(err)
	}
	value := <-stream
	snapshot, ok := value.(map[string]any)
	if !ok || snapshot["assistantStream"] == nil {
		t.Fatalf("follow snapshot = %#v", value)
	}
}

func TestDSHGatewayAssistantStreamPublishesDeltasBeforeSettlement(t *testing.T) {
	fake := &dshFakeBackend{sessions: []corecontract.UserSessionState{{
		SessionID: projectorSessionID, Version: 1, PermissionMode: "read-only", UpdatedAt: time.Now().UTC(),
	}}}
	gateway := newDSHTestGateway(t, fake)
	state := gateway.installSession(fake.sessions[0])
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates, stop := state.subscribe(ctx)
	defer stop()

	state.mapCanonical(projectorEvent(t, 1, runevent.KindAssistantMessageStarted, runevent.MessageStartedPayload{MessageID: "message-stream", Role: "assistant"}))
	state.mapCanonical(projectorEvent(t, 2, runevent.KindAssistantMessageDelta, runevent.MessageDeltaPayload{MessageID: "message-stream", Delta: "hello"}))
	stepStart := <-updates
	if stepStart.event == nil || stepStart.event.Type != "step/start" {
		t.Fatalf("missing owning step: %+v", stepStart)
	}
	start := <-updates
	if start.assistantFrame == nil || start.assistantFrame["type"] != "start" {
		t.Fatalf("assistant start update = %#v", start)
	}
	blockStart := <-updates
	if blockStart.assistantFrame == nil || blockStart.assistantFrame["type"] != "chunk" {
		t.Fatalf("assistant block-start update = %#v", blockStart)
	}
	delta := <-updates
	if delta.assistantFrame == nil || delta.assistantFrame["type"] != "chunk" {
		t.Fatalf("assistant delta update = %#v", delta)
	}
	deltaChunk := delta.assistantFrame["chunk"].(map[string]any)
	if deltaChunk["type"] != "text-delta" || deltaChunk["text"] != "hello" {
		t.Fatalf("assistant delta = %#v", deltaChunk)
	}
	baseline := state.assistantBaseline()
	active, ok := baseline["activeAttempt"].(map[string]any)
	if !ok || active["nextIndex"] != 2 || len(active["stream"].([]any)) != 2 {
		t.Fatalf("assistant baseline = %#v", baseline)
	}

	state.mapCanonical(projectorEvent(t, 3, runevent.KindAssistantMessageCompleted, runevent.MessageCompletedPayload{MessageID: "message-stream"}))
	blockEnd := <-updates
	finish := <-updates
	settlement := <-updates
	end := <-updates
	if blockEnd.assistantFrame == nil || finish.assistantFrame == nil || settlement.event == nil || end.assistantFrame == nil {
		t.Fatalf("completion update order = %#v %#v %#v %#v", blockEnd, finish, settlement, end)
	}
	if settlement.event.Type != "assistant/message" || end.assistantFrame["type"] != "end" {
		t.Fatalf("completion updates = %#v %#v", settlement, end)
	}
	endOutcome := end.assistantFrame["outcome"].(map[string]any)
	if endOutcome["kind"] != "committed" || endOutcome["eventType"] != "assistant/message" || endOutcome["seq"] != int64(1) {
		t.Fatalf("assistant end outcome = %#v", endOutcome)
	}
}
