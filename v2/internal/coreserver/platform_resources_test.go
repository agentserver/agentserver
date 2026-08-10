package coreserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

const (
	platformResourceTestWorkspace = "81000000-0000-4000-8000-000000000011"
	platformResourceTestActor     = "82000000-0000-4000-8000-000000000012"
	platformResourceTestMember    = "83000000-0000-4000-8000-000000000013"
)

func TestPlatformResourceHandlerDispatchesGlobalAndWorkspaceAuthority(t *testing.T) {
	now := time.Date(2026, 8, 4, 1, 0, 0, 0, time.UTC)
	commands := &recordingPlatformResourceCommands{list: corecontract.ListWorkspacesResponse{Workspaces: []corecontract.WorkspaceState{{
		WorkspaceID: platformResourceTestWorkspace, Name: "SG", Status: "active", CurrentUserRole: "owner",
		ManagedLarkCredentialMode: "webhook_swap", Version: 1, CreatedAt: now, UpdatedAt: now,
	}}}}
	workload := &identityCapabilityAuthorizer{identity: "platform-gateway"}
	users := &recordingUserAuthorizer{actorID: platformResourceTestActor}
	handler, err := NewPlatformResourceHandler(workload, users, commands)
	if err != nil {
		t.Fatal(err)
	}

	list := httptest.NewRequest(http.MethodGet, corecontract.WorkspacesPath(), nil)
	list.Header.Set("X-Test-Identity", "platform-gateway")
	listResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(listResponse, list)
	if listResponse.Code != http.StatusOK || commands.listActor != platformResourceTestActor || users.action != "workspaces.list" || workload.actions[0] != "workspaces.list" {
		t.Fatalf("list response/authority = %d %s / %+v / %q", listResponse.Code, listResponse.Body.String(), workload.actions, users.action)
	}

	add := httptest.NewRequest(http.MethodPost, corecontract.WorkspaceMembersPath(platformResourceTestWorkspace), strings.NewReader(`{"userId":"`+platformResourceTestMember+`","role":"developer"}`))
	add.Header.Set("X-Test-Identity", "platform-gateway")
	add.Header.Set("Content-Type", "application/json")
	addResponse := httptest.NewRecorder()
	handler.Routes().ServeHTTP(addResponse, add)
	if addResponse.Code != http.StatusCreated || commands.addWorkspace != platformResourceTestWorkspace || commands.addActor != platformResourceTestActor ||
		commands.add.UserID != platformResourceTestMember || commands.add.Role != "developer" || users.action != "members.add" || workload.actions[1] != "members.add" {
		t.Fatalf("add response/authority = %d %s / %+v / %+v", addResponse.Code, addResponse.Body.String(), workload.actions, commands.add)
	}
}

func TestPlatformResourceHandlerRejectsUnknownJSONBeforeCommand(t *testing.T) {
	commands := &recordingPlatformResourceCommands{}
	handler, err := NewPlatformResourceHandler(&identityCapabilityAuthorizer{identity: "platform-gateway"}, &recordingUserAuthorizer{actorID: platformResourceTestActor}, commands)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, corecontract.WorkspacesPath(), strings.NewReader(`{"workspaceId":"`+platformResourceTestWorkspace+`","name":"SG","extra":true}`))
	request.Header.Set("X-Test-Identity", "platform-gateway")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || commands.createCalls != 0 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unknown JSON response = %d %s calls=%d", response.Code, response.Body.String(), commands.createCalls)
	}
}

func TestPlatformResourceHandlerRequiresExplicitWorkspaceCredentialMode(t *testing.T) {
	for name, body := range map[string]string{
		"missing": `{"workspaceId":"` + platformResourceTestWorkspace + `","name":"SG"}`,
		"unknown": `{"workspaceId":"` + platformResourceTestWorkspace + `","name":"SG","managedLarkCredentialMode":"global"}`,
	} {
		t.Run(name, func(t *testing.T) {
			commands := &recordingPlatformResourceCommands{}
			handler, err := NewPlatformResourceHandler(
				&identityCapabilityAuthorizer{identity: "platform-gateway"},
				&recordingUserAuthorizer{actorID: platformResourceTestActor}, commands,
			)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, corecontract.WorkspacesPath(), strings.NewReader(body))
			request.Header.Set("X-Test-Identity", "platform-gateway")
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.Routes().ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest || commands.createCalls != 0 ||
				!strings.Contains(response.Body.String(), "managedLarkCredentialMode") {
				t.Fatalf("mode response = %d %s calls=%d", response.Code, response.Body.String(), commands.createCalls)
			}
		})
	}
}

type recordingPlatformResourceCommands struct {
	list         corecontract.ListWorkspacesResponse
	listActor    string
	createCalls  int
	addWorkspace string
	addActor     string
	add          corecontract.AddWorkspaceMemberRequest
}

func (commands *recordingPlatformResourceCommands) ListWorkspaces(_ context.Context, actorID string) (corecontract.ListWorkspacesResponse, error) {
	commands.listActor = actorID
	return commands.list, nil
}
func (*recordingPlatformResourceCommands) GetWorkspace(context.Context, string, string) (corecontract.WorkspaceState, error) {
	return corecontract.WorkspaceState{}, nil
}
func (commands *recordingPlatformResourceCommands) CreateWorkspace(context.Context, string, corecontract.CreateWorkspaceRequest) (corecontract.CreateWorkspaceResponse, error) {
	commands.createCalls++
	return corecontract.CreateWorkspaceResponse{}, nil
}
func (*recordingPlatformResourceCommands) UpdateWorkspace(context.Context, string, string, corecontract.UpdateWorkspaceRequest) (corecontract.UpdateWorkspaceResponse, error) {
	return corecontract.UpdateWorkspaceResponse{}, nil
}
func (*recordingPlatformResourceCommands) ArchiveWorkspace(context.Context, string, string, corecontract.ArchiveWorkspaceRequest) (corecontract.ArchiveWorkspaceResponse, error) {
	return corecontract.ArchiveWorkspaceResponse{}, nil
}
func (*recordingPlatformResourceCommands) ListMembers(context.Context, string, string) (corecontract.ListWorkspaceMembersResponse, error) {
	return corecontract.ListWorkspaceMembersResponse{}, nil
}
func (commands *recordingPlatformResourceCommands) AddMember(_ context.Context, workspaceID, actorID string, input corecontract.AddWorkspaceMemberRequest) (corecontract.AddWorkspaceMemberResponse, error) {
	commands.addWorkspace, commands.addActor, commands.add = workspaceID, actorID, input
	return corecontract.AddWorkspaceMemberResponse{Member: corecontract.WorkspaceMemberState{UserID: input.UserID, Role: input.Role, Version: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}, Created: true}, nil
}
func (*recordingPlatformResourceCommands) UpdateMember(context.Context, string, string, string, corecontract.UpdateWorkspaceMemberRequest) (corecontract.UpdateWorkspaceMemberResponse, error) {
	return corecontract.UpdateWorkspaceMemberResponse{}, nil
}
func (*recordingPlatformResourceCommands) RemoveMember(context.Context, string, string, string) (corecontract.RemoveWorkspaceMemberResponse, error) {
	return corecontract.RemoveWorkspaceMemberResponse{}, nil
}
