package coreserver

import (
	"context"
	"net/http"
	"net/http/httptest"
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

type recordingWorkspaceLLMGatewayCommands struct {
	disableResult corecontract.DisableWorkspaceLLMGatewayResponse
	workspaceID   string
	gatewayID     string
	actorID       string
}

func (*recordingWorkspaceLLMGatewayCommands) CreateGateway(context.Context, string, string, corecontract.CreateWorkspaceLLMGatewayRequest) (corecontract.CreateWorkspaceLLMGatewayResponse, error) {
	panic("unexpected CreateGateway")
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
