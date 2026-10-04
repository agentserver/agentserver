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
)

type dshFakeBackend struct {
	sessions []corecontract.UserSessionState
	created  corecontract.CreateUserSessionResponse
	mode     corecontract.UpdateUserSessionPermissionModeResponse
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
func (fake *dshFakeBackend) UpdateSession(context.Context, string, string, string, corecontract.UpdateUserSessionRequest) (corecontract.UpdateUserSessionResponse, error) {
	return corecontract.UpdateUserSessionResponse{}, nil
}
func (fake *dshFakeBackend) ArchiveSession(context.Context, string, string, string, corecontract.ArchiveUserSessionRequest) (corecontract.ArchiveUserSessionResponse, error) {
	return corecontract.ArchiveUserSessionResponse{}, nil
}
func (fake *dshFakeBackend) UpdatePermissionMode(_ context.Context, _, _, _ string, input corecontract.UpdateUserSessionPermissionModeRequest) (corecontract.UpdateUserSessionPermissionModeResponse, error) {
	state := fake.sessions[0]
	state.PermissionMode = input.PermissionMode
	state.PermissionModeVersion++
	fake.mode = corecontract.UpdateUserSessionPermissionModeResponse{Session: state, Changed: true}
	return fake.mode, nil
}
func (fake *dshFakeBackend) UpdateWorkingDirectory(context.Context, string, string, string, corecontract.UpdateUserSessionWorkingDirectoryRequest) (corecontract.UpdateUserSessionWorkingDirectoryResponse, error) {
	return corecontract.UpdateUserSessionWorkingDirectoryResponse{}, nil
}
func (fake *dshFakeBackend) GetTranscript(context.Context, string, string, string) (corecontract.GetUserSessionTranscriptResponse, error) {
	return corecontract.GetUserSessionTranscriptResponse{}, nil
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
