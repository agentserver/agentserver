package coreserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
)

func TestRunCapabilityHandlerKeepsWorkloadRoutesAndAudiencesSeparate(t *testing.T) {
	pool := &identityCapabilityAuthorizer{identity: "pool"}
	executor := &identityCapabilityAuthorizer{identity: "executor-gateway"}
	llmproxy := &identityCapabilityAuthorizer{identity: "llmproxy"}
	authority := &recordingRunCapabilityAuthority{
		issueResponse: corecontract.IssueRunCapabilitiesResponse{
			ExecutorMCP: corecontract.IssuedRunCapability{CapabilityID: testCapabilityAttempt, Audience: "executor-mcp", Token: "executor-token"},
			LLMProxy:    corecontract.IssuedRunCapability{CapabilityID: testCapabilityCatalog, Audience: "llmproxy", Token: "model-token"},
		},
		authorizeResponse: corecontract.AuthorizeRunCapabilityResponse{
			CapabilityID: testCapabilityAttempt, Audience: "executor-mcp", RunID: testCapabilityRun,
			RunAttemptID: testCapabilityAttempt, RunAttemptGeneration: 3,
			RunVersion: 5, RunAttemptVersion: 6, AuthorizedAt: time.Date(2026, 8, 2, 7, 0, 0, 0, time.UTC),
		},
		llmproxyResponse: corecontract.AuthorizeLLMProxyRunCapabilityResponse{
			CapabilityID: testCapabilityCatalog, Audience: "llmproxy", RunID: testCapabilityRun,
			RunAttemptID: testCapabilityAttempt, RunAttemptGeneration: 3,
			RunVersion: 5, RunAttemptVersion: 6, AuthorizedAt: time.Date(2026, 8, 2, 7, 0, 0, 0, time.UTC),
		},
	}
	handler, err := NewRunCapabilityHandler(pool, executor, llmproxy, authority)
	if err != nil {
		t.Fatal(err)
	}
	issueRequest, _ := productionCapabilityIssuanceFixture(time.Now().UTC())

	issueBody, _ := json.Marshal(issueRequest)
	response := serveRunCapabilityRequest(t, handler, corecontract.IssueRunCapabilitiesPath, "pool", "", issueBody)
	if response.Code != http.StatusOK || len(authority.issueCalls) != 1 ||
		len(pool.actions) != 1 || pool.actions[0] != "run-capabilities.issue" {
		t.Fatalf("issue response/calls = %d / %+v / %+v", response.Code, authority.issueCalls, pool.actions)
	}

	executorBody, _ := json.Marshal(corecontract.AuthorizeExecutorRunCapabilityRequest{
		ExecutorID: issueRequest.ExecutorID, ToolCatalogDigest: issueRequest.ToolCatalogDigest,
	})
	response = serveRunCapabilityRequest(
		t, handler, corecontract.AuthorizeExecutorRunCapabilityPath,
		"executor-gateway", "Bearer executor-token", executorBody,
	)
	if response.Code != http.StatusOK || len(authority.executorCalls) != 1 || authority.executorTokens[0] != "executor-token" ||
		len(executor.actions) != 1 || executor.actions[0] != "run-capabilities.authorize-executor-mcp" {
		t.Fatalf("executor response/calls = %d / %+v / %q / %+v", response.Code, authority.executorCalls, authority.executorTokens, executor.actions)
	}

	modelBody, _ := json.Marshal(corecontract.AuthorizeLLMProxyRunCapabilityRequest{
		Model: issueRequest.Model, Provider: issueRequest.Provider,
		LLMGatewayID: issueRequest.LLMGatewayID, LLMGatewayVersion: issueRequest.LLMGatewayVersion,
		LLMGatewayGrantUserID: issueRequest.LLMGatewayGrantUserID,
	})
	response = serveRunCapabilityRequest(
		t, handler, corecontract.AuthorizeLLMProxyRunCapabilityPath,
		"llmproxy", "Bearer model-token", modelBody,
	)
	if response.Code != http.StatusOK || len(authority.llmproxyCalls) != 1 || authority.llmproxyTokens[0] != "model-token" ||
		len(llmproxy.actions) != 1 || llmproxy.actions[0] != "run-capabilities.authorize-llmproxy" {
		t.Fatalf("llmproxy response/calls = %d / %+v / %q / %+v", response.Code, authority.llmproxyCalls, authority.llmproxyTokens, llmproxy.actions)
	}

	for _, test := range []struct {
		path, identity, bearer string
		body                   []byte
	}{
		{path: corecontract.IssueRunCapabilitiesPath, identity: "executor-gateway", body: issueBody},
		{path: corecontract.AuthorizeExecutorRunCapabilityPath, identity: "pool", bearer: "Bearer executor-token", body: executorBody},
		{path: corecontract.AuthorizeLLMProxyRunCapabilityPath, identity: "executor-gateway", bearer: "Bearer model-token", body: modelBody},
	} {
		response = serveRunCapabilityRequest(t, handler, test.path, test.identity, test.bearer, test.body)
		if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "workload is not authorized") {
			t.Fatalf("cross-workload %s response = %d %s", test.path, response.Code, response.Body.String())
		}
	}
	if len(authority.issueCalls) != 1 || len(authority.executorCalls) != 1 || len(authority.llmproxyCalls) != 1 {
		t.Fatalf("cross-workload requests reached authority: %d/%d/%d", len(authority.issueCalls), len(authority.executorCalls), len(authority.llmproxyCalls))
	}
}

func TestRunCapabilityHandlerRejectsMalformedBearerJSONAndOversizeBodies(t *testing.T) {
	authority := &recordingRunCapabilityAuthority{}
	handler, err := NewRunCapabilityHandler(
		&identityCapabilityAuthorizer{identity: "pool"},
		&identityCapabilityAuthorizer{identity: "executor-gateway"},
		&identityCapabilityAuthorizer{identity: "llmproxy"},
		authority,
	)
	if err != nil {
		t.Fatal(err)
	}
	validBody := []byte(`{"executorId":"61000000-0000-4000-8000-000000000001","toolCatalogDigest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)
	tests := []struct {
		name   string
		mutate func(*http.Request)
		body   []byte
		status int
	}{
		{name: "missing bearer", status: http.StatusForbidden},
		{name: "wrong scheme", mutate: func(request *http.Request) { request.Header.Set("Authorization", "Basic abc") }, status: http.StatusForbidden},
		{name: "empty bearer", mutate: func(request *http.Request) { request.Header.Set("Authorization", "Bearer ") }, status: http.StatusForbidden},
		{name: "padded bearer", mutate: func(request *http.Request) { request.Header.Set("Authorization", "Bearer token ") }, status: http.StatusForbidden},
		{name: "combined bearer", mutate: func(request *http.Request) { request.Header.Set("Authorization", "Bearer one,Bearer two") }, status: http.StatusForbidden},
		{name: "duplicate bearer", mutate: func(request *http.Request) {
			request.Header.Add("Authorization", "Bearer one")
			request.Header.Add("Authorization", "Bearer two")
		}, status: http.StatusForbidden},
		{name: "oversize bearer", mutate: func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 32*1024+1))
		}, status: http.StatusForbidden},
		{name: "unknown JSON", mutate: func(request *http.Request) { request.Header.Set("Authorization", "Bearer token") }, body: []byte(`{"future":true}`), status: http.StatusBadRequest},
		{name: "trailing JSON", mutate: func(request *http.Request) { request.Header.Set("Authorization", "Bearer token") }, body: append(validBody, []byte(` {}`)...), status: http.StatusBadRequest},
		{name: "oversize JSON", mutate: func(request *http.Request) { request.Header.Set("Authorization", "Bearer token") }, body: bytes.Repeat([]byte(" "), int(maximumRunCapabilityCommandBytes)+1), status: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := test.body
			if body == nil {
				body = validBody
			}
			request := httptest.NewRequest(http.MethodPost, corecontract.AuthorizeExecutorRunCapabilityPath, bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Test-Identity", "executor-gateway")
			if test.mutate != nil {
				test.mutate(request)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("response = %d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
			}
			if test.status == http.StatusForbidden && response.Header().Get("WWW-Authenticate") == "" {
				t.Fatal("bearer denial omitted WWW-Authenticate")
			}
		})
	}
	if len(authority.executorCalls) != 0 {
		t.Fatalf("malformed requests reached authority: %+v", authority.executorCalls)
	}
}

func TestRunCapabilityHandlerMakesLiveDenialsIndistinguishableAndDatabaseFailuresInternal(t *testing.T) {
	authority := &recordingRunCapabilityAuthority{}
	handler, err := NewRunCapabilityHandler(
		&identityCapabilityAuthorizer{identity: "pool"},
		&identityCapabilityAuthorizer{identity: "executor-gateway"},
		&identityCapabilityAuthorizer{identity: "llmproxy"},
		authority,
	)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"executorId":"61000000-0000-4000-8000-000000000001","toolCatalogDigest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)
	for _, code := range []coredb.StateErrorCode{coredb.ErrorForbidden, coredb.ErrorConflict, coredb.ErrorLeaseLost, coredb.ErrorInvalidState} {
		authority.executorErr = &coredb.StateError{Code: code, Message: "sensitive live-state reason"}
		response := serveRunCapabilityRequest(
			t, handler, corecontract.AuthorizeExecutorRunCapabilityPath,
			"executor-gateway", "Bearer token", body,
		)
		if response.Code != http.StatusForbidden || strings.Contains(response.Body.String(), "sensitive") ||
			response.Header().Get("WWW-Authenticate") == "" {
			t.Fatalf("%s denial response = %d %s", code, response.Code, response.Body.String())
		}
	}
	authority.executorErr = &coredb.StateError{Code: coredb.ErrorInvalidArgument, Message: "sensitive invalid detail"}
	response := serveRunCapabilityRequest(
		t, handler, corecontract.AuthorizeExecutorRunCapabilityPath,
		"executor-gateway", "Bearer token", body,
	)
	if response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), "sensitive") {
		t.Fatalf("invalid argument response = %d %s", response.Code, response.Body.String())
	}
	for _, failure := range []error{
		&coredb.StateError{Code: coredb.ErrorDatabase, Message: "database address"},
		errors.New("implementation detail"),
	} {
		authority.executorErr = failure
		response = serveRunCapabilityRequest(
			t, handler, corecontract.AuthorizeExecutorRunCapabilityPath,
			"executor-gateway", "Bearer token", body,
		)
		if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), failure.Error()) {
			t.Fatalf("internal failure response = %d %s", response.Code, response.Body.String())
		}
	}
}

func TestNewRunCapabilityHandlerRequiresAllBoundaries(t *testing.T) {
	authorizer := &identityCapabilityAuthorizer{identity: "workload"}
	authority := &recordingRunCapabilityAuthority{}
	if _, err := NewRunCapabilityHandler(authorizer, authorizer, authorizer, authority); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRunCapabilityHandler(nil, authorizer, authorizer, authority); err == nil {
		t.Fatal("nil pool authorizer was accepted")
	}
	if _, err := NewRunCapabilityHandler(authorizer, authorizer, authorizer, nil); err == nil {
		t.Fatal("nil capability authority was accepted")
	}
}

func serveRunCapabilityRequest(
	t *testing.T,
	handler http.Handler,
	path, identity, authorization string,
	body []byte,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Test-Identity", identity)
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%s response omitted Cache-Control: no-store", path)
	}
	return response
}

type identityCapabilityAuthorizer struct {
	identity string
	actions  []string
}

func (authorizer *identityCapabilityAuthorizer) AuthorizeWorkload(request *http.Request, action string) error {
	authorizer.actions = append(authorizer.actions, action)
	if request.Header.Get("X-Test-Identity") != authorizer.identity {
		return errors.New("workload identity does not match")
	}
	return nil
}

type recordingRunCapabilityAuthority struct {
	issueResponse     corecontract.IssueRunCapabilitiesResponse
	issueErr          error
	authorizeResponse corecontract.AuthorizeRunCapabilityResponse
	llmproxyResponse  corecontract.AuthorizeLLMProxyRunCapabilityResponse
	executorErr       error
	llmproxyErr       error
	issueCalls        []corecontract.IssueRunCapabilitiesRequest
	executorCalls     []corecontract.AuthorizeExecutorRunCapabilityRequest
	executorTokens    []string
	llmproxyCalls     []corecontract.AuthorizeLLMProxyRunCapabilityRequest
	llmproxyTokens    []string
}

func (authority *recordingRunCapabilityAuthority) IssueRunCapabilities(
	_ context.Context,
	request corecontract.IssueRunCapabilitiesRequest,
) (corecontract.IssueRunCapabilitiesResponse, error) {
	authority.issueCalls = append(authority.issueCalls, request)
	return authority.issueResponse, authority.issueErr
}

func (authority *recordingRunCapabilityAuthority) AuthorizeExecutorRunCapability(
	_ context.Context,
	token string,
	request corecontract.AuthorizeExecutorRunCapabilityRequest,
) (corecontract.AuthorizeRunCapabilityResponse, error) {
	authority.executorTokens = append(authority.executorTokens, token)
	authority.executorCalls = append(authority.executorCalls, request)
	return authority.authorizeResponse, authority.executorErr
}

func (authority *recordingRunCapabilityAuthority) AuthorizeLLMProxyRunCapability(
	_ context.Context,
	token string,
	request corecontract.AuthorizeLLMProxyRunCapabilityRequest,
) (corecontract.AuthorizeLLMProxyRunCapabilityResponse, error) {
	authority.llmproxyTokens = append(authority.llmproxyTokens, token)
	authority.llmproxyCalls = append(authority.llmproxyCalls, request)
	return authority.llmproxyResponse, authority.llmproxyErr
}
