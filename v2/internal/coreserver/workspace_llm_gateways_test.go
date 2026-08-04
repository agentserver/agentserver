package coreserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

func TestWorkspaceLLMGatewayHandlerDisablesThroughOwnerScopedAction(t *testing.T) {
	workload := &recordingRunAttemptAuthorizer{}
	users := &recordingUserAuthorizer{actorID: testLLMGatewayUserID}
	commands := &recordingWorkspaceLLMGatewayCommands{disableResult: corecontract.DisableWorkspaceLLMGatewayResponse{
		GatewayID: testLLMGatewayID, Status: "disabled", Version: 4, Changed: true,
	}}
	handler, err := NewWorkspaceLLMGatewayHandler(workload, users, commands)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, corecontract.DisableLLMGatewayPath(testLLMGatewayWorkspaceID, testLLMGatewayID), nil)
	request.Header.Set("Authorization", "Bearer user-token")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || workload.action != "llm-gateways.disable" || users.action != "llm-gateways.disable" ||
		commands.workspaceID != testLLMGatewayWorkspaceID || commands.gatewayID != testLLMGatewayID || commands.actorID != testLLMGatewayUserID {
		t.Fatalf("disable action = %d workload=%q user=%q commands=%+v body=%q", response.Code, workload.action, users.action, commands, response.Body.String())
	}
}

func TestWorkspaceLLMGatewayHandlerUpdatesThroughOwnerScopedResource(t *testing.T) {
	workload := &recordingRunAttemptAuthorizer{}
	users := &recordingUserAuthorizer{actorID: testLLMGatewayUserID}
	commands := &recordingWorkspaceLLMGatewayCommands{updateResult: corecontract.UpdateWorkspaceLLMGatewayResponse{
		Gateway: corecontract.WorkspaceLLMGatewayState{GatewayID: testLLMGatewayID, WorkspaceID: testLLMGatewayWorkspaceID, Version: 4}, Changed: true,
	}}
	handler, err := NewWorkspaceLLMGatewayHandler(workload, users, commands)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"name":"updated","responsesUrl":"https://llm.example/v1/responses","oidcIssuer":"https://id.example","oidcClientId":"client","oidcScopes":["openid","offline_access"],"bearerTokenType":"access_token","defaultModel":"model-2","makeDefault":true,"expectedVersion":3}`
	request := httptest.NewRequest(http.MethodPatch, corecontract.WorkspaceLLMGatewayPath(testLLMGatewayWorkspaceID, testLLMGatewayID), strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer user-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || workload.action != "llm-gateways.update" || users.action != "llm-gateways.update" ||
		commands.workspaceID != testLLMGatewayWorkspaceID || commands.gatewayID != testLLMGatewayID || commands.actorID != testLLMGatewayUserID ||
		commands.updateInput.ExpectedVersion != 3 || commands.updateInput.DefaultModel != "model-2" {
		t.Fatalf("update action = %d workload=%q user=%q commands=%+v body=%q", response.Code, workload.action, users.action, commands, response.Body.String())
	}
}

type recordingWorkspaceLLMGatewayCommands struct {
	disableResult corecontract.DisableWorkspaceLLMGatewayResponse
	updateResult  corecontract.UpdateWorkspaceLLMGatewayResponse
	updateInput   corecontract.UpdateWorkspaceLLMGatewayRequest
	workspaceID   string
	gatewayID     string
	actorID       string
}

func (*recordingWorkspaceLLMGatewayCommands) CreateGateway(context.Context, string, string, corecontract.CreateWorkspaceLLMGatewayRequest) (corecontract.CreateWorkspaceLLMGatewayResponse, error) {
	panic("unexpected CreateGateway")
}

func (commands *recordingWorkspaceLLMGatewayCommands) UpdateGateway(_ context.Context, workspaceID, gatewayID, actorID string, input corecontract.UpdateWorkspaceLLMGatewayRequest) (corecontract.UpdateWorkspaceLLMGatewayResponse, error) {
	commands.workspaceID, commands.gatewayID, commands.actorID, commands.updateInput = workspaceID, gatewayID, actorID, input
	return commands.updateResult, nil
}

func (*recordingWorkspaceLLMGatewayCommands) ListGateways(context.Context, string, string) (corecontract.ListWorkspaceLLMGatewaysResponse, error) {
	panic("unexpected ListGateways")
}

func (*recordingWorkspaceLLMGatewayCommands) BeginAuthorization(context.Context, string, string, string, corecontract.BeginWorkspaceLLMGatewayAuthorizationRequest) (corecontract.BeginWorkspaceLLMGatewayAuthorizationResponse, error) {
	panic("unexpected BeginAuthorization")
}

func (*recordingWorkspaceLLMGatewayCommands) CompleteAuthorization(context.Context, string, string, string, corecontract.CompleteWorkspaceLLMGatewayAuthorizationRequest) (corecontract.CompleteWorkspaceLLMGatewayAuthorizationResponse, error) {
	panic("unexpected CompleteAuthorization")
}

func (*recordingWorkspaceLLMGatewayCommands) RevokeGrant(context.Context, string, string, string) (corecontract.RevokeWorkspaceLLMGatewayGrantResponse, error) {
	panic("unexpected RevokeGrant")
}

func (commands *recordingWorkspaceLLMGatewayCommands) DisableGateway(_ context.Context, workspaceID, gatewayID, actorID string) (corecontract.DisableWorkspaceLLMGatewayResponse, error) {
	commands.workspaceID, commands.gatewayID, commands.actorID = workspaceID, gatewayID, actorID
	return commands.disableResult, nil
}
