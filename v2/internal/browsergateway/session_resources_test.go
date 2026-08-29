package browsergateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

func TestSessionResourceProxyForwardsOnlyScopedSessionLifecycle(t *testing.T) {
	now := time.Date(2026, 8, 4, 5, 0, 0, 0, time.UTC)
	client := &http.Client{Transport: browserRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.Path != corecontract.UserSessionsPath(projectorWorkspaceID) ||
			request.Header.Get("Authorization") != "Bearer user-token" || request.Header.Get("Accept") != "application/json" {
			t.Fatalf("Core session request = %s %s headers=%v", request.Method, request.URL, request.Header)
		}
		var input corecontract.CreateUserSessionRequest
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil || input.SessionID != projectorSessionID || input.Title != "Inspect SG" {
			t.Fatalf("Core session input = %+v, %v", input, err)
		}
		return browserJSONResponse(request, http.StatusCreated, corecontract.CreateUserSessionResponse{
			Session: corecontract.UserSessionState{
				SessionID: projectorSessionID, WorkspaceID: projectorWorkspaceID,
				Title: "Inspect SG", Status: "active", Version: 1, CreatedAt: now, UpdatedAt: now,
			},
			Created: true,
		}), nil
	})}
	backend, err := NewCoreRunBackend("https://core.agentserver.local", client)
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := NewSessionResourceProxy(backend)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, corecontract.UserSessionsPath(projectorWorkspaceID), strings.NewReader(
		`{"sessionId":"`+projectorSessionID+`","title":"Inspect SG"}`,
	))
	request.Header.Set("Authorization", "Bearer user-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	proxy.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusCreated || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), projectorSessionID) {
		t.Fatalf("session proxy response = %d %s headers=%v", response.Code, response.Body.String(), response.Header())
	}
}

func TestSessionResourceProxyRejectsUnreviewedMethodBeforeCore(t *testing.T) {
	called := false
	client := &http.Client{Transport: browserRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		called = true
		return browserJSONResponse(request, http.StatusOK, map[string]any{}), nil
	})}
	backend, _ := NewCoreRunBackend("https://core.agentserver.local", client)
	proxy, _ := NewSessionResourceProxy(backend)
	request := httptest.NewRequest(http.MethodDelete, corecontract.UserSessionPath(projectorWorkspaceID, projectorSessionID), nil)
	request.Header.Set("Authorization", "Bearer user-token")
	response := httptest.NewRecorder()
	proxy.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != "GET, PATCH" || called {
		t.Fatalf("unreviewed method response = %d headers=%v coreCalled=%v", response.Code, response.Header(), called)
	}
}

func TestSessionResourceProxyForwardsTranscriptAsReadOnly(t *testing.T) {
	called := 0
	client := &http.Client{Transport: browserRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		called++
		if request.Method != http.MethodGet || request.URL.Path != corecontract.UserSessionTranscriptPath(projectorWorkspaceID, projectorSessionID) ||
			request.Header.Get("Authorization") != "Bearer user-token" {
			t.Fatalf("Core transcript request = %s %s headers=%v", request.Method, request.URL, request.Header)
		}
		return browserJSONResponse(request, http.StatusOK, corecontract.GetUserSessionTranscriptResponse{
			WorkspaceID: projectorWorkspaceID, SessionID: projectorSessionID,
			Messages: []corecontract.UserSessionTranscriptMessage{}, Truncated: false,
		}), nil
	})}
	backend, _ := NewCoreRunBackend("https://core.agentserver.local", client)
	proxy, _ := NewSessionResourceProxy(backend)
	path := corecontract.UserSessionTranscriptPath(projectorWorkspaceID, projectorSessionID)
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer user-token")
	response := httptest.NewRecorder()
	proxy.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || called != 1 || !strings.Contains(response.Body.String(), `"messages":[]`) {
		t.Fatalf("transcript proxy response = %d %s calls=%d", response.Code, response.Body.String(), called)
	}

	patch := httptest.NewRequest(http.MethodPatch, path, strings.NewReader(`{}`))
	patch.Header.Set("Authorization", "Bearer user-token")
	patch.Header.Set("Content-Type", "application/json")
	patchResponse := httptest.NewRecorder()
	proxy.Routes().ServeHTTP(patchResponse, patch)
	if patchResponse.Code != http.StatusMethodNotAllowed || patchResponse.Header().Get("Allow") != http.MethodGet || called != 1 {
		t.Fatalf("transcript mutation response = %d headers=%v calls=%d", patchResponse.Code, patchResponse.Header(), called)
	}
}

func TestSessionResourceProxyForwardsPermissionModePatch(t *testing.T) {
	client := &http.Client{Transport: browserRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPatch || request.URL.Path != corecontract.UserSessionPermissionModePath(projectorWorkspaceID, projectorSessionID) ||
			request.Header.Get("Authorization") != "Bearer user-token" || request.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("Core permission mode request = %s %s headers=%v", request.Method, request.URL, request.Header)
		}
		var input corecontract.UpdateUserSessionPermissionModeRequest
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil || input.PermissionMode != "auto" || input.ExpectedPermissionModeVersion != 1 {
			t.Fatalf("Core permission mode input = %+v, %v", input, err)
		}
		return browserJSONResponse(request, http.StatusOK, corecontract.UpdateUserSessionPermissionModeResponse{
			Session: corecontract.UserSessionState{SessionID: projectorSessionID, WorkspaceID: projectorWorkspaceID, Title: "Mode", Status: "active", Version: 2, PermissionMode: "auto", PermissionModeVersion: 2},
			Changed: true,
		}), nil
	})}
	backend, _ := NewCoreRunBackend("https://core.agentserver.local", client)
	proxy, _ := NewSessionResourceProxy(backend)
	request := httptest.NewRequest(http.MethodPatch, corecontract.UserSessionPermissionModePath(projectorWorkspaceID, projectorSessionID), strings.NewReader(`{"permissionMode":"auto","expectedPermissionModeVersion":1}`))
	request.Header.Set("Authorization", "Bearer user-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	proxy.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"permissionMode":"auto"`) {
		t.Fatalf("permission mode proxy response = %d %s", response.Code, response.Body.String())
	}
}

func TestSessionResourceProxyForwardsWorkingDirectoryPatch(t *testing.T) {
	client := &http.Client{Transport: browserRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPatch || request.URL.Path != corecontract.UserSessionWorkingDirectoryPath(projectorWorkspaceID, projectorSessionID) ||
			request.Header.Get("Authorization") != "Bearer user-token" || request.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("Core working-directory request = %s %s headers=%v", request.Method, request.URL, request.Header)
		}
		var input corecontract.UpdateUserSessionWorkingDirectoryRequest
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil || input.EnvironmentID != "94000000-0000-4000-8000-000000000004" ||
			input.WorkingDirectory != "rtm-aihub" || input.ExpectedWorkingDirectoryVersion != 1 {
			t.Fatalf("Core working-directory input = %+v, %v", input, err)
		}
		return browserJSONResponse(request, http.StatusOK, corecontract.UpdateUserSessionWorkingDirectoryResponse{
			Session: corecontract.UserSessionState{SessionID: projectorSessionID, WorkspaceID: projectorWorkspaceID, Title: "Workspace", Status: "active", Version: 2, WorkingDirectory: "rtm-aihub", WorkingDirectoryVersion: 2},
			Changed: true,
		}), nil
	})}
	backend, _ := NewCoreRunBackend("https://core.agentserver.local", client)
	proxy, _ := NewSessionResourceProxy(backend)
	request := httptest.NewRequest(http.MethodPatch, corecontract.UserSessionWorkingDirectoryPath(projectorWorkspaceID, projectorSessionID), strings.NewReader(
		`{"environmentId":"94000000-0000-4000-8000-000000000004","workingDirectory":"rtm-aihub","expectedWorkingDirectoryVersion":1}`,
	))
	request.Header.Set("Authorization", "Bearer user-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	proxy.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"workingDirectory":"rtm-aihub"`) {
		t.Fatalf("working-directory proxy response = %d %s", response.Code, response.Body.String())
	}
}

func TestSessionResourceProxyForwardsOnlyReviewedTrajectoryQuery(t *testing.T) {
	called := 0
	client := &http.Client{Transport: browserRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		called++
		if request.Method != http.MethodGet || request.URL.Path != corecontract.UserSessionTrajectoryPath(projectorWorkspaceID, projectorSessionID) ||
			request.URL.RawQuery != "before=v1.cursor&limit=40" || request.Header.Get("Authorization") != "Bearer user-token" {
			t.Fatalf("Core trajectory request = %s %s headers=%v", request.Method, request.URL, request.Header)
		}
		return browserJSONResponse(request, http.StatusOK, corecontract.GetUserSessionTrajectoryResponse{
			SchemaVersion: 1, WorkspaceID: projectorWorkspaceID, SessionID: projectorSessionID,
			Records: []corecontract.UserSessionTrajectoryRecord{}, ReadAt: time.Now().UTC(),
		}), nil
	})}
	backend, _ := NewCoreRunBackend("https://core.agentserver.local", client)
	proxy, _ := NewSessionResourceProxy(backend)
	path := corecontract.UserSessionTrajectoryPath(projectorWorkspaceID, projectorSessionID)
	request := httptest.NewRequest(http.MethodGet, path+"?limit=40&before=v1.cursor", nil)
	request.Header.Set("Authorization", "Bearer user-token")
	response := httptest.NewRecorder()
	proxy.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || called != 1 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("trajectory response = %d %s calls=%d", response.Code, response.Body.String(), called)
	}

	for _, query := range []string{"?future=true", "?before=", "?before=%0A", "?before=v1.a&before=v1.b", "?limit=1&limit=2", "?limit=0", "?limit="} {
		rejected := httptest.NewRequest(http.MethodGet, path+query, nil)
		rejected.Header.Set("Authorization", "Bearer user-token")
		rejectedResponse := httptest.NewRecorder()
		proxy.Routes().ServeHTTP(rejectedResponse, rejected)
		if rejectedResponse.Code != http.StatusBadRequest || called != 1 {
			t.Fatalf("query %q response = %d %s calls=%d", query, rejectedResponse.Code, rejectedResponse.Body.String(), called)
		}
	}
}
