package coreserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
)

type recordingApprovalCommands struct {
	createRequest  corecontract.CreateApprovalRequest
	expireRequest  corecontract.ApprovalTerminalRequest
	cancelRequest  corecontract.ApprovalTerminalRequest
	consumeRequest corecontract.ConsumeApprovalRequest
}

func (commands *recordingApprovalCommands) CreateApproval(_ context.Context, request corecontract.CreateApprovalRequest) (corecontract.CreateApprovalResponse, error) {
	commands.createRequest = request
	return corecontract.CreateApprovalResponse{}, nil
}

func (commands *recordingApprovalCommands) ExpireApproval(_ context.Context, request corecontract.ApprovalTerminalRequest) (corecontract.ApprovalTerminalResponse, error) {
	commands.expireRequest = request
	return corecontract.ApprovalTerminalResponse{}, nil
}

func (commands *recordingApprovalCommands) CancelApproval(_ context.Context, request corecontract.ApprovalTerminalRequest) (corecontract.ApprovalTerminalResponse, error) {
	commands.cancelRequest = request
	return corecontract.ApprovalTerminalResponse{}, nil
}

func (commands *recordingApprovalCommands) ConsumeApproval(_ context.Context, request corecontract.ConsumeApprovalRequest) (corecontract.ConsumeApprovalResponse, error) {
	commands.consumeRequest = request
	return corecontract.ConsumeApprovalResponse{}, nil
}

func TestApprovalHandlerRoutesBoundedCommandsWithPathIdentity(t *testing.T) {
	commands := &recordingApprovalCommands{}
	authorizer := &recordingRunAttemptAuthorizer{}
	handler, err := NewApprovalHandler(authorizer, commands)
	if err != nil {
		t.Fatal(err)
	}
	approvalID := "40000000-0000-4000-8000-000000000071"
	tests := []struct {
		path   string
		action string
		body   string
		seen   func() string
	}{
		{corecontract.CreateApprovalPath, "approvals.create", `{"approvalId":"` + approvalID + `"}`, func() string { return commands.createRequest.ApprovalID }},
		{corecontract.ExpireApprovalPath(approvalID), "approvals.expire", `{"approvalId":"` + approvalID + `"}`, func() string { return commands.expireRequest.ApprovalID }},
		{corecontract.CancelApprovalPath(approvalID), "approvals.cancel", `{"approvalId":"` + approvalID + `"}`, func() string { return commands.cancelRequest.ApprovalID }},
		{corecontract.ConsumeApprovalPath(approvalID), "approvals.consume", `{"approvalId":"` + approvalID + `"}`, func() string { return commands.consumeRequest.ApprovalID }},
	}
	for _, test := range tests {
		request := httptest.NewRequest(http.MethodPost, test.path, bytes.NewBufferString(test.body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || authorizer.action != test.action || test.seen() != approvalID {
			t.Fatalf("POST %s = %d action=%q id=%q body=%s", test.path, response.Code, authorizer.action, test.seen(), response.Body.String())
		}
	}

	request := httptest.NewRequest(http.MethodPost, corecontract.ExpireApprovalPath(approvalID), bytes.NewBufferString(`{"approvalId":"40000000-0000-4000-8000-000000000072"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("path mismatch = %d %s", response.Code, response.Body.String())
	}
}

func TestApprovalContextDigestIsClosedAndCanonical(t *testing.T) {
	valid := corecontract.CanonicalJSONDigest{
		Domain: string(coredb.HashDomainApprovalContext), CanonicalizerVersion: coredb.CanonicalizerRFC8785V1,
		SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	parsed, err := approvalContextDigest(valid)
	if err != nil || parsed[0] != 0x01 || parsed[31] != 0xef {
		t.Fatalf("approvalContextDigest(valid) = %x, %v", parsed, err)
	}
	for _, mutate := range []func(*corecontract.CanonicalJSONDigest){
		func(value *corecontract.CanonicalJSONDigest) { value.Domain = "policy-context" },
		func(value *corecontract.CanonicalJSONDigest) { value.CanonicalizerVersion = "other" },
		func(value *corecontract.CanonicalJSONDigest) { value.SHA256 = "ABCDEF" + value.SHA256[6:] },
		func(value *corecontract.CanonicalJSONDigest) { value.SHA256 = "00" },
	} {
		candidate := valid
		mutate(&candidate)
		if _, err := approvalContextDigest(candidate); err == nil {
			t.Fatalf("approvalContextDigest(%+v) succeeded", candidate)
		}
	}
}

type recordingUserApprovalCommands struct {
	command DecideUserApprovalCommand
	result  corecontract.DecideUserApprovalResponse
	err     error
}

func (commands *recordingUserApprovalCommands) DecideUserApproval(_ context.Context, command DecideUserApprovalCommand) (corecontract.DecideUserApprovalResponse, error) {
	commands.command = command
	return commands.result, commands.err
}

func TestUserApprovalHandlerRequiresBothAuthoritiesAndStrictBody(t *testing.T) {
	commands := &recordingUserApprovalCommands{result: corecontract.DecideUserApprovalResponse{
		WorkspaceID: userRunWorkspaceID, ExecutionID: userRunID, ExecutionStatus: "pending_approval", ExecutionVersion: 2,
		Approval: corecontract.ApprovalState{ApprovalID: "40000000-0000-4000-8000-000000000071"}, Changed: true,
	}}
	workload := &recordingRunAttemptAuthorizer{}
	users := &recordingUserAuthorizer{actorID: userRunActorID}
	handler, err := NewUserApprovalHandler(workload, users, commands)
	if err != nil {
		t.Fatal(err)
	}
	approvalID := "40000000-0000-4000-8000-000000000071"
	body := `{"decision":"approve","nonce":"40000000-0000-4000-8000-000000000072","contextDigest":{"domain":"approval-context","canonicalizerVersion":"rfc8785-v1","sha256":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},"expectedApprovalVersion":1}`
	request := httptest.NewRequest(http.MethodPost, corecontract.DecideUserApprovalPath(userRunWorkspaceID, approvalID), bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer user-token")
	response := httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || workload.action != "approvals.decide" || users.action != "approvals.decide" ||
		commands.command.ActorID != userRunActorID || commands.command.WorkspaceID != userRunWorkspaceID || commands.command.ApprovalID != approvalID {
		t.Fatalf("decision response=%d %s authority=%q/%q command=%+v", response.Code, response.Body.String(), workload.action, users.action, commands.command)
	}
	var result corecontract.DecideUserApprovalResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || !result.Changed {
		t.Fatalf("decision response body = %+v, %v", result, err)
	}

	commands.command = DecideUserApprovalCommand{}
	request = httptest.NewRequest(http.MethodPost, corecontract.DecideUserApprovalPath(userRunWorkspaceID, approvalID)+"?force=true", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || commands.command.ActorID != "" {
		t.Fatalf("query decision = %d %s command=%+v", response.Code, response.Body.String(), commands.command)
	}
}

type rejectingUserApprovalStore struct {
	command coredb.DecideApprovalCommand
}

func (store *rejectingUserApprovalStore) DecideApproval(_ context.Context, command coredb.DecideApprovalCommand) (coredb.DecideApprovalResult, error) {
	store.command = command
	return coredb.DecideApprovalResult{}, errors.New("stop after conversion")
}

func TestUserApprovalServiceAllocatesServerTransitionAndConvertsDigest(t *testing.T) {
	store := &rejectingUserApprovalStore{}
	identities := []string{
		"40000000-0000-4000-8000-000000000081",
		"40000000-0000-4000-8000-000000000082",
		"40000000-0000-4000-8000-000000000083",
	}
	index := 0
	service, err := NewUserApprovalService(UserApprovalServiceConfig{Store: store, NewID: func() (string, error) {
		value := identities[index]
		index++
		return value, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.DecideUserApproval(t.Context(), DecideUserApprovalCommand{
		ActorID: userRunActorID, WorkspaceID: userRunWorkspaceID,
		ApprovalID: "40000000-0000-4000-8000-000000000071", Decision: coredb.ApprovalDecisionApprove,
		Nonce: "40000000-0000-4000-8000-000000000072", ExpectedApprovalVersion: 4,
		ContextDigest: corecontract.CanonicalJSONDigest{
			Domain: string(coredb.HashDomainApprovalContext), CanonicalizerVersion: coredb.CanonicalizerRFC8785V1,
			SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		},
	})
	if err == nil || store.command.Record.EventID != identities[0] || store.command.Record.ProducerInstanceID != identities[1] ||
		store.command.Record.OutboxID != identities[2] || store.command.ExpectedContextHash[0] != 0x01 {
		t.Fatalf("DecideUserApproval() error=%v command=%+v", err, store.command)
	}
}
