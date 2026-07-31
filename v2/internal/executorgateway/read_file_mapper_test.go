package executorgateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/execprofile"
	"github.com/agentserver/agentserver/v2/internal/executorgateway/agentxconn"
)

func TestMapReadFileV1BuildsOneBoundedReadOperation(t *testing.T) {
	registered := testRegisteredEnvironment(
		testEnvironmentID,
		`{"kind":"local","root":"/workspace/team one","displayName":"primary","defaultCwd":"src"}`,
	)
	registered.OuterProfileVersion = execprofile.FilesystemReadVersion
	environment, err := resolveRegisteredEnvironment(registered)
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{ "environment_id":"60000000-0000-4000-8000-000000000006", "path":"src/data #1.bin", "offset":17, "limit":4096 }`)
	identities := testReadFileV1Identities()
	plan, err := MapReadFileV1(raw, testExecutorMCPPrincipal("capability-read-file"), "call-read-file-1", environment, identities)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plan.Arguments, raw) {
		t.Fatalf("raw arguments changed: %q", plan.Arguments)
	}
	if plan.Read.OperationID != identities.OperationID || plan.Read.Ordinal != 1 ||
		plan.Read.Kind != ReadFileV1OperationRead || plan.Read.EffectClass != ReadFileV1EffectRead ||
		plan.Read.MutationKey != identities.MutationKey {
		t.Fatalf("mapped read operation = %+v", plan.Read)
	}
	if plan.RelativePath != "src/data #1.bin" || plan.Offset != 17 || plan.Limit != 4096 ||
		plan.RootURI != "file:///workspace/team%20one" ||
		plan.PathURI != "file:///workspace/team%20one/src/data%20%231.bin" {
		t.Fatalf("mapped read-file plan = %+v", plan)
	}
	if plan.Read.Routing.ExecutionID != identities.ExecutionID ||
		plan.Read.Routing.OperationID != identities.OperationID ||
		plan.Read.Routing.MutationKey != identities.MutationKey ||
		plan.Read.Routing.EnvID != environment.EnvironmentID {
		t.Fatalf("read-file routing = %+v", plan.Read.Routing)
	}

	var params readFileBlockParams
	if err := json.Unmarshal(plan.Read.Params, &params); err != nil {
		t.Fatal(err)
	}
	if params.Path != plan.PathURI || params.Offset != 17 || params.Length != 4096 {
		t.Fatalf("outer read params = %+v", params)
	}
	var operationPlan readFileOperationPlan
	if err := json.Unmarshal(plan.OperationPlan, &operationPlan); err != nil {
		t.Fatal(err)
	}
	if operationPlan.Version != "read-file-v1" || operationPlan.Lifecycle != "request" || len(operationPlan.Operations) != 1 ||
		operationPlan.Operations[0].Retry != "none-phase1" || operationPlan.Operations[0].RPCRequestID != identities.RPCRequestID {
		t.Fatalf("frozen operation plan = %+v", operationPlan)
	}
	var policy readFilePolicyContext
	if err := json.Unmarshal(plan.PolicyContext, &policy); err != nil {
		t.Fatal(err)
	}
	if policy.OuterProfileVersion != execprofile.FilesystemReadVersion || policy.RelativePath != plan.RelativePath ||
		policy.PathURI != plan.PathURI || policy.ConnectionGeneration != environment.ConnectionGeneration ||
		policy.FilesystemProfile != "bounded-registered-root-read-v1" {
		t.Fatalf("frozen policy context = %+v", policy)
	}

	limits := agentxconn.Limits{MaxFrameBytes: 8 * 1024 * 1024, MaxJSONValues: 65_536, MaxJSONDepth: 256}
	routing := plan.Read.Routing
	if _, err := agentxconn.Encode(agentxconn.Frame{
		Type: agentxconn.MessageTypeRPC, SessionID: "73000000-0000-4000-8000-000000000001",
		SessionSeq: 1, Generation: environment.ConnectionGeneration, Context: &routing, RPC: plan.Read.RPC,
	}, limits); err != nil {
		t.Fatalf("mapped filesystem request violates agentx profile: %v", err)
	}
}

func TestMapReadFileV1DefaultsAndWindowsFileURI(t *testing.T) {
	registered := testRegisteredEnvironment(testEnvironmentID, `{"kind":"local","root":"C:\\Workspace"}`)
	registered.Platform = "windows-amd64"
	registered.OuterProfileVersion = execprofile.FilesystemReadVersion
	environment, err := resolveRegisteredEnvironment(registered)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := MapReadFileV1(
		json.RawMessage(`{"environment_id":"60000000-0000-4000-8000-000000000006","path":"src/data file.txt"}`),
		testExecutorMCPPrincipal("capability-read-file"), "call-read-file-windows", environment, testReadFileV1Identities(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Offset != 0 || plan.Limit != execprofile.MaxFilesystemReadLength ||
		plan.RootURI != "file:///C:/Workspace" || plan.PathURI != "file:///C:/Workspace/src/data%20file.txt" {
		t.Fatalf("Windows/default read mapping = %+v", plan)
	}
}

func TestMapReadFileV1RejectsUnsupportedProfileAndUnsafeInputs(t *testing.T) {
	registered := testRegisteredEnvironment(testEnvironmentID, `{"kind":"local","root":"/workspace"}`)
	processOnly, err := resolveRegisteredEnvironment(registered)
	if err != nil {
		t.Fatal(err)
	}
	validRaw := json.RawMessage(`{"environment_id":"60000000-0000-4000-8000-000000000006","path":"data.bin"}`)
	if _, err := MapReadFileV1(validRaw, testExecutorMCPPrincipal("capability-read-file"), "call-read-file-invalid", processOnly, testReadFileV1Identities()); err == nil {
		t.Fatal("process-only environment admitted a filesystem read")
	}

	registered.OuterProfileVersion = execprofile.FilesystemReadVersion
	environment, err := resolveRegisteredEnvironment(registered)
	if err != nil {
		t.Fatal(err)
	}
	tests := []string{
		`{"environment_id":"60000000-0000-4000-8000-000000000006","path":".","limit":1}`,
		`{"environment_id":"60000000-0000-4000-8000-000000000006","path":"../secret","limit":1}`,
		`{"environment_id":"60000000-0000-4000-8000-000000000006","path":"src/./data","limit":1}`,
		`{"environment_id":"60000000-0000-4000-8000-000000000006","path":"/etc/passwd","limit":1}`,
		`{"environment_id":"60000000-0000-4000-8000-000000000006","path":"src\\data","limit":1}`,
		`{"environment_id":"60000000-0000-4000-8000-000000000006","path":"src//data","limit":1}`,
		`{"environment_id":"60000000-0000-4000-8000-000000000006","path":"data.bin","limit":0}`,
		`{"environment_id":"60000000-0000-4000-8000-000000000006","path":"data.bin","limit":1048577}`,
		`{"environment_id":"60000000-0000-4000-8000-000000000006","path":"data.bin","offset":9007199254740992}`,
		`{"environment_id":"60000000-0000-4000-8000-000000000006","path":"data.bin","future":true}`,
	}
	for _, raw := range tests {
		if _, err := MapReadFileV1(json.RawMessage(raw), testExecutorMCPPrincipal("capability-read-file"), "call-read-file-invalid", environment, testReadFileV1Identities()); err == nil {
			t.Errorf("invalid read_file input was accepted: %s", raw)
		}
	}
	wrongEnvironment := fmt.Sprintf(`{"environment_id":"%s","path":"data.bin"}`, "30000000-0000-4000-8000-000000000099")
	if _, err := MapReadFileV1(json.RawMessage(wrongEnvironment), testExecutorMCPPrincipal("capability-read-file"), "call-read-file-invalid", environment, testReadFileV1Identities()); err == nil {
		t.Fatal("mismatched read_file environment identity was accepted")
	}
}

func TestReadFileV1IdentityAllocatorRejectsDuplicateGeneratorValues(t *testing.T) {
	allocator, err := NewReadFileV1IdentityAllocator(func() (string, error) {
		return "77000000-0000-4000-8000-000000000001", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := allocator.Allocate(); err == nil {
		t.Fatal("duplicate read-file identities were accepted")
	}
}

func testReadFileV1Identities() ReadFileV1Identities {
	return ReadFileV1Identities{
		ExecutionID:  "53000000-0000-4000-8000-000000000005",
		OperationID:  "54000000-0000-4000-8000-000000000005",
		MutationKey:  "63000000-0000-4000-8000-000000000006",
		RPCRequestID: "77000000-0000-4000-8000-000000000001",
	}
}
