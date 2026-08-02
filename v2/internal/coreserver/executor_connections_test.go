package coreserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
)

func TestExecutorConnectionHandlerRoutesGatewayRecoveryWithExactWorkloadAction(t *testing.T) {
	const executorID = "91000000-0000-4000-8000-000000000001"
	requestBody := corecontract.RecoverExecutorGatewayRequest{
		GatewayInstanceID: "91000000-0000-4000-8000-000000000002",
		Records: []corecontract.TransitionRecord{{
			EventID: "91000000-0000-4000-8000-000000000003", ProducerInstanceID: "91000000-0000-4000-8000-000000000002",
			ProducerSeq: 1, OutboxID: "91000000-0000-4000-8000-000000000004",
		}},
	}
	raw, err := json.Marshal(requestBody)
	if err != nil {
		t.Fatal(err)
	}
	authorizer := &recordingExecutorConnectionAuthorizer{}
	commands := &recordingExecutorConnectionCommands{recovery: corecontract.RecoverExecutorGatewayResponse{
		FencedConnectionGeneration: 9, ConnectionFenced: true, RecoveredExecutions: 1,
	}}
	handler, err := NewExecutorConnectionHandler(authorizer, commands)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, corecontract.RecoverExecutorGatewayPath(executorID), bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || authorizer.action != "executor-connections.recover-gateway" || commands.executorID != executorID {
		t.Fatalf("gateway recovery response/action/scope = %d / %q / %q", response.Code, authorizer.action, commands.executorID)
	}
	if commands.request.GatewayInstanceID != requestBody.GatewayInstanceID || len(commands.request.Records) != 1 {
		t.Fatalf("gateway recovery command = %+v", commands.request)
	}
	var result corecontract.RecoverExecutorGatewayResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result != commands.recovery {
		t.Fatalf("gateway recovery response = %+v, %v", result, err)
	}
}

type recordingExecutorConnectionAuthorizer struct {
	action string
}

func (authorizer *recordingExecutorConnectionAuthorizer) AuthorizeWorkload(_ *http.Request, action string) error {
	authorizer.action = action
	return nil
}

type recordingExecutorConnectionCommands struct {
	executorID string
	request    corecontract.RecoverExecutorGatewayRequest
	recovery   corecontract.RecoverExecutorGatewayResponse
}

func (*recordingExecutorConnectionCommands) AcquireExecutorConnection(context.Context, corecontract.AcquireExecutorConnectionRequest) (corecontract.ConnectionHolder, error) {
	return corecontract.ConnectionHolder{}, nil
}

func (*recordingExecutorConnectionCommands) RenewExecutorConnection(context.Context, corecontract.RenewExecutorConnectionRequest) (corecontract.ConnectionHolder, error) {
	return corecontract.ConnectionHolder{}, nil
}

func (*recordingExecutorConnectionCommands) ActivateExecutorConnection(context.Context, corecontract.ActivateExecutorConnectionRequest) (corecontract.ConnectionHolder, error) {
	return corecontract.ConnectionHolder{}, nil
}

func (*recordingExecutorConnectionCommands) FenceExecutorConnection(context.Context, corecontract.FenceExecutorConnectionRequest) error {
	return nil
}

func (commands *recordingExecutorConnectionCommands) RecoverExecutorGateway(
	_ context.Context,
	executorID string,
	request corecontract.RecoverExecutorGatewayRequest,
) (corecontract.RecoverExecutorGatewayResponse, error) {
	commands.executorID = executorID
	commands.request = request
	return commands.recovery, nil
}
