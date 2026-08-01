package executorgateway

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/execprofile"
)

func TestEnvironmentResolverProductionListOmitsInsecureDevelopmentEnvironments(t *testing.T) {
	insecure := testRegisteredEnvironment(testEnvironmentID, `{"kind":"local","root":"/insecure"}`)
	production := testRegisteredEnvironment(
		"60000000-0000-4000-8000-000000000007",
		`{"kind":"local","root":"/production"}`,
	)
	production.InsecureDev = false
	resolver, err := NewEnvironmentResolver(&fakeEnvironmentRegistry{environments: []RegisteredEnvironment{insecure, production}})
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := "40000000-0000-4000-8000-000000000004"
	listed, err := resolver.ListProduction(t.Context(), workspaceID, testExecutorID)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Environments) != 1 || listed.Environments[0].EnvironmentID != production.EnvironmentID {
		t.Fatalf("production environment projection = %+v", listed.Environments)
	}
	development, err := resolver.List(t.Context(), workspaceID, testExecutorID)
	if err != nil {
		t.Fatal(err)
	}
	if len(development.Environments) != 2 {
		t.Fatalf("development environment projection = %+v", development.Environments)
	}
}

func TestProductionExecutorsRejectInsecureDevelopmentEnvironmentBeforeCoreOrDispatch(t *testing.T) {
	t.Run("shell", func(t *testing.T) {
		authority := newFakeShellAuthority()
		dispatcher := &fakeShellDispatcher{start: func(ProcessDispatchRequest) (*ProcessExchange, error) {
			t.Fatal("production shell reached agentx dispatch")
			return nil, nil
		}}
		executor := newTestShellExecutor(t, authority, dispatcher)
		request := testShellExecuteRequest(10_000)
		request.Principal.Production = true
		_, err := executor.Execute(t.Context(), request)
		if err == nil || !strings.Contains(err.Error(), "insecure-development") {
			t.Fatalf("production shell rejection = %v", err)
		}
		authority.mu.Lock()
		records := len(authority.records)
		executionID := authority.execution.ExecutionID
		authority.mu.Unlock()
		if records != 0 || executionID != "" || dispatcher.count() != 0 {
			t.Fatalf("production shell crossed authority boundary: records=%d execution=%q dispatches=%d", records, executionID, dispatcher.count())
		}
	})

	t.Run("read_file", func(t *testing.T) {
		dispatcher := &fakeFilesystemDispatcher{}
		executor, authority := newReadFileExecutorFixture(t, dispatcher)
		principal := testExecutorMCPPrincipal("capability-production-read-file")
		principal.Production = true
		_, err := executor.Execute(t.Context(), ReadFileExecuteRequest{
			Principal: principal, ToolCallID: "call-production-read-file",
			Arguments: json.RawMessage(`{"environment_id":"60000000-0000-4000-8000-000000000006","path":"data.txt","limit":1}`),
		})
		if err == nil || !strings.Contains(err.Error(), "insecure-development") {
			t.Fatalf("production read_file rejection = %v", err)
		}
		authority.mu.Lock()
		records := len(authority.records)
		executionID := authority.execution.ExecutionID
		authority.mu.Unlock()
		dispatcher.mu.Lock()
		dispatches := len(dispatcher.requests)
		dispatcher.mu.Unlock()
		if records != 0 || executionID != "" || dispatches != 0 {
			t.Fatalf("production read_file crossed authority boundary: records=%d execution=%q dispatches=%d", records, executionID, dispatches)
		}
	})
}

func TestProductionMappersRejectInsecureDevelopmentEnvironment(t *testing.T) {
	principal := testExecutorMCPPrincipal("capability-production-mapper")
	principal.Production = true

	shellEnvironment, err := resolveRegisteredEnvironment(testRegisteredEnvironment(
		testEnvironmentID, `{"kind":"local","root":"/workspace"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := MapShellV1(
		json.RawMessage(`{"environment_id":"60000000-0000-4000-8000-000000000006","argv":["/bin/true"]}`),
		principal, "call-production-shell-mapper", shellEnvironment, testShellPolicy(), testShellV1Identities(),
	); err == nil || !strings.Contains(err.Error(), "insecure-development") {
		t.Fatalf("production shell mapper rejection = %v", err)
	}

	registered := testRegisteredEnvironment(testEnvironmentID, `{"kind":"local","root":"/workspace"}`)
	registered.OuterProfileVersion = execprofile.FilesystemReadVersion
	readEnvironment, err := resolveRegisteredEnvironment(registered)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := MapReadFileV1(
		json.RawMessage(`{"environment_id":"60000000-0000-4000-8000-000000000006","path":"data.txt","limit":1}`),
		principal, "call-production-read-file-mapper", readEnvironment, testReadFilePolicy(), testReadFileV1Identities(),
	); err == nil || !strings.Contains(err.Error(), "insecure-development") {
		t.Fatalf("production read_file mapper rejection = %v", err)
	}
}
