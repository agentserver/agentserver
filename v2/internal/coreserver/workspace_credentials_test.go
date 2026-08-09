package coreserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

const (
	testCredentialWorkspaceID = "10000000-0000-4000-8000-000000000001"
	testCredentialBindingID   = "20000000-0000-4000-8000-000000000002"
	testCredentialActorID     = "30000000-0000-4000-8000-000000000003"
)

func TestWorkspaceCredentialHandlerRoutesActionSuffixOutsideBindingPathValue(t *testing.T) {
	for _, test := range []struct {
		name       string
		path       string
		body       string
		wantAction string
		wantCall   string
	}{
		{
			name: "rotate", path: corecontract.RotateWorkspaceCredentialPath(testCredentialWorkspaceID, "lark", testCredentialBindingID),
			body:       `{"expectedAuthorityVersion":1,"expectedCredentialVersion":1,"authType":"oauth","secret":{"accessToken":"placeholder"}}`,
			wantAction: "credentials.rotate", wantCall: "rotate",
		},
		{
			name: "revoke", path: corecontract.RevokeWorkspaceCredentialPath(testCredentialWorkspaceID, "lark", testCredentialBindingID),
			body: `{"expectedAuthorityVersion":1}`, wantAction: "credentials.revoke", wantCall: "revoke",
		},
		{
			name: "delete", path: corecontract.DeleteWorkspaceCredentialPath(testCredentialWorkspaceID, "lark", testCredentialBindingID),
			body: `{"expectedAuthorityVersion":1}`, wantAction: "credentials.delete", wantCall: "delete",
		},
		{
			name: "default", path: corecontract.DefaultWorkspaceCredentialPath(testCredentialWorkspaceID, "lark", testCredentialBindingID),
			body: `{"expectedAuthorityVersion":1}`, wantAction: "credentials.set-default", wantCall: "default",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			workload := &recordingRunAttemptAuthorizer{}
			users := &recordingUserAuthorizer{actorID: testCredentialActorID}
			commands := &recordingWorkspaceCredentialCommands{}
			handler, err := NewWorkspaceCredentialHandler(workload, users, commands)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer platform-token")
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.Routes().ServeHTTP(response, request)
			if response.Code != http.StatusOK || workload.action != test.wantAction || users.action != test.wantAction ||
				commands.call != test.wantCall || commands.workspaceID != testCredentialWorkspaceID ||
				commands.kind != "lark" || commands.bindingID != testCredentialBindingID || commands.actorID != testCredentialActorID {
				t.Fatalf("route = status %d workload=%q user=%q commands=%+v body=%q", response.Code, workload.action, users.action, commands, response.Body.String())
			}
		})
	}
}

type recordingWorkspaceCredentialCommands struct {
	call        string
	workspaceID string
	kind        string
	bindingID   string
	actorID     string
}

func (*recordingWorkspaceCredentialCommands) ListSchemas(context.Context) (corecontract.ListWorkspaceCredentialProviderSchemasResponse, error) {
	panic("unexpected ListSchemas")
}

func (*recordingWorkspaceCredentialCommands) ListBindings(context.Context, string, string, string) (corecontract.ListWorkspaceCredentialsResponse, error) {
	panic("unexpected ListBindings")
}

func (*recordingWorkspaceCredentialCommands) CreateBinding(context.Context, string, string, string, corecontract.CreateWorkspaceCredentialRequest) (corecontract.CreateWorkspaceCredentialResponse, error) {
	panic("unexpected CreateBinding")
}

func (commands *recordingWorkspaceCredentialCommands) RotateBinding(_ context.Context, workspaceID, kind, bindingID, actorID string, _ corecontract.RotateWorkspaceCredentialRequest) (corecontract.RotateWorkspaceCredentialResponse, error) {
	commands.record("rotate", workspaceID, kind, bindingID, actorID)
	return corecontract.RotateWorkspaceCredentialResponse{}, nil
}

func (*recordingWorkspaceCredentialCommands) RenameBinding(context.Context, string, string, string, string, corecontract.RenameWorkspaceCredentialRequest) (corecontract.RenameWorkspaceCredentialResponse, error) {
	panic("unexpected RenameBinding")
}

func (commands *recordingWorkspaceCredentialCommands) RevokeBinding(_ context.Context, workspaceID, kind, bindingID, actorID string, _ int64) (corecontract.RevokeWorkspaceCredentialResponse, error) {
	commands.record("revoke", workspaceID, kind, bindingID, actorID)
	return corecontract.RevokeWorkspaceCredentialResponse{}, nil
}

func (commands *recordingWorkspaceCredentialCommands) DeleteBinding(_ context.Context, workspaceID, kind, bindingID, actorID string, _ int64) (corecontract.DeleteWorkspaceCredentialResponse, error) {
	commands.record("delete", workspaceID, kind, bindingID, actorID)
	return corecontract.DeleteWorkspaceCredentialResponse{}, nil
}

func (commands *recordingWorkspaceCredentialCommands) SetDefaultBinding(_ context.Context, workspaceID, kind, bindingID, actorID string, _ int64) (corecontract.SetDefaultWorkspaceCredentialResponse, error) {
	commands.record("default", workspaceID, kind, bindingID, actorID)
	return corecontract.SetDefaultWorkspaceCredentialResponse{}, nil
}

func (commands *recordingWorkspaceCredentialCommands) record(call, workspaceID, kind, bindingID, actorID string) {
	commands.call, commands.workspaceID, commands.kind, commands.bindingID, commands.actorID = call, workspaceID, kind, bindingID, actorID
}
