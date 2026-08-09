package executorgateway

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/execprofile"
	"github.com/agentserver/agentserver/v2/internal/executionbackend"
	"github.com/agentserver/agentserver/v2/internal/executorgateway/agentxconn"
)

func TestAgentXBackendStartEncoderPreservesExistingShellWire(t *testing.T) {
	environment, err := resolveRegisteredEnvironment(testRegisteredEnvironment(
		testEnvironmentID,
		`{"kind":"local","root":"/workspace/team one","displayName":"primary","defaultCwd":"src"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	identities := testShellV1Identities()
	plan, err := MapShellV1(
		json.RawMessage(`{"environment_id":"60000000-0000-4000-8000-000000000006","argv":["/usr/bin/printf","ok"],"env":{"LANG":"C"},"timeout_ms":17000,"tty":true}`),
		testExecutorMCPPrincipal("agentx-backend-wire"), "call-agentx-wire", environment,
		testShellPolicy(), identities,
	)
	if err != nil {
		t.Fatal(err)
	}
	startOperation := backendOperationFromRouting(plan.Start.Routing)
	timeoutOperation := backendOperationFromRouting(plan.Timeout.Routing)
	request := executionbackend.StartProcessRequest{
		Target: executionbackend.Target{
			Kind: executionbackend.KindAgentX, ID: environment.ExecutorID,
			Generation: environment.ConnectionGeneration, EnvironmentID: environment.EnvironmentID,
		},
		Operation: startOperation, RequestID: identities.StartRPCRequestID,
		ProcessID: plan.ProcessID, Executable: "/usr/bin/printf", Arguments: []string{"ok"},
		WorkingDirectory: "/workspace/team one/src", WorkspaceRoot: environment.Root,
		Platform: environment.Platform, Environment: map[string]string{"LANG": "C"}, TTY: true,
		Timeout: 17 * time.Second, OutputLimitBytes: defaultShellMaxOutputBytes,
		DeadlineNotification: &executionbackend.DeadlineNotification{
			After: 17 * time.Second, Operation: timeoutOperation, RequestID: identities.TimeoutRPCRequestID,
		},
	}
	if err := validateAgentXStartRequest(request); err != nil {
		t.Fatalf("validateAgentXStartRequest() error = %v", err)
	}
	rpc, routing, directives, err := mapAgentXStartRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rpc, plan.Start.RPC) {
		t.Fatalf("agentx start RPC changed\n got: %s\nwant: %s", rpc, plan.Start.RPC)
	}
	if routing != plan.Start.Routing || !reflect.DeepEqual(directives, &plan.Directives) {
		t.Fatalf("agentx envelope changed: routing=%+v directives=%+v", routing, directives)
	}
}

func TestAgentXBackendReadEncoderPreservesExistingReadFileWire(t *testing.T) {
	registered := testRegisteredEnvironment(
		testEnvironmentID,
		`{"kind":"local","root":"/workspace/team one","displayName":"primary","defaultCwd":"src"}`,
	)
	registered.OuterProfileVersion = execprofile.FilesystemReadVersion
	environment, err := resolveRegisteredEnvironment(registered)
	if err != nil {
		t.Fatal(err)
	}
	identities := testReadFileV1Identities()
	plan, err := MapReadFileV1(
		json.RawMessage(`{"environment_id":"60000000-0000-4000-8000-000000000006","path":"src/data #1.bin","offset":17,"limit":4096}`),
		testExecutorMCPPrincipal("agentx-backend-read-wire"), "call-agentx-read-wire", environment,
		testReadFilePolicy(), identities,
	)
	if err != nil {
		t.Fatal(err)
	}
	request := executionbackend.ReadFileRequest{
		Target: executionbackend.Target{
			Kind: executionbackend.KindAgentX, ID: environment.ExecutorID,
			Generation: environment.ConnectionGeneration, EnvironmentID: environment.EnvironmentID,
		},
		Operation: backendOperationFromRouting(plan.Read.Routing), RequestID: identities.RPCRequestID,
		Path: "/workspace/team one/src/data #1.bin", Offset: 17, Limit: 4096,
	}
	if err := validateAgentXReadRequest(request); err != nil {
		t.Fatalf("validateAgentXReadRequest() error = %v", err)
	}
	rpc, routing, err := mapAgentXReadRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rpc, plan.Read.RPC) {
		t.Fatalf("agentx read RPC changed\n got: %s\nwant: %s", rpc, plan.Read.RPC)
	}
	if routing != plan.Read.Routing {
		t.Fatalf("agentx read routing changed: got %+v want %+v", routing, plan.Read.Routing)
	}
}

func backendOperationFromRouting(routing agentxconn.RoutingContext) executionbackend.OperationContext {
	return executionbackend.OperationContext{
		WorkspaceID: routing.WorkspaceID,
		SessionID:   "30000000-0000-4000-8000-000000000003",
		RunID:       routing.RunID, RunAttemptID: routing.RunAttemptID,
		RunAttemptGeneration: routing.RunAttemptGeneration,
		ExecutionID:          routing.ExecutionID, OperationID: routing.OperationID,
		MutationKey: routing.MutationKey,
	}
}
