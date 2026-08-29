package executorgateway

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/executorgateway/agentxconn"
	"github.com/agentserver/agentserver/v2/internal/runmanifest"
	"github.com/agentserver/agentserver/v2/internal/workspaceauthority"
)

func TestMapShellV1BuildsDeterministicRestrictedProcessPlan(t *testing.T) {
	environment, err := resolveRegisteredEnvironment(testRegisteredEnvironment(
		testEnvironmentID,
		`{"kind":"local","root":"/workspace/team one","displayName":"primary","defaultCwd":"src"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{ "environment_id":"60000000-0000-4000-8000-000000000006", "argv":["/usr/bin/printf","ok"], "env":{"LANG":"C"}, "timeout_ms":17000, "tty":true }`)
	plan, err := MapShellV1(raw, testExecutorMCPPrincipal("capability-shell"), "call-shell-1", environment, testShellPolicy(), testShellV1Identities())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plan.Arguments, raw) {
		t.Fatalf("raw arguments changed: %q", plan.Arguments)
	}
	if plan.ProcessID != testShellV1Identities().ProcessID || plan.TimeoutMillis != 17_000 ||
		plan.RootURI != "file:///workspace/team%20one" || plan.CWDURI != "file:///workspace/team%20one/src" {
		t.Fatalf("mapped shell plan = %+v", plan)
	}
	if plan.Directives.ProcessTimeout == nil || plan.Directives.ProcessTimeout.AfterMillis != 17_000 ||
		plan.Directives.ProcessTimeout.OperationID != plan.Timeout.OperationID ||
		plan.Directives.ProcessTimeout.MutationKey != plan.Timeout.MutationKey {
		t.Fatalf("timeout directives = %+v", plan.Directives)
	}
	if plan.Start.Routing.OperationID != plan.Start.OperationID || plan.Timeout.Routing.OperationID != plan.Timeout.OperationID ||
		plan.Start.Routing.MutationKey == plan.Timeout.Routing.MutationKey {
		t.Fatalf("operation routing = start %+v timeout %+v", plan.Start.Routing, plan.Timeout.Routing)
	}

	var start map[string]any
	if err := json.Unmarshal(plan.Start.Params, &start); err != nil {
		t.Fatal(err)
	}
	if _, leaked := start["timeout_ms"]; leaked {
		t.Fatal("timeout leaked into stock process/start params")
	}
	if _, leaked := start["directives"]; leaked {
		t.Fatal("outer directives leaked into stock process/start params")
	}
	if start["cwd"] != plan.CWDURI || start["enforceManagedNetwork"] != true || start["tty"] != true || start["pipeStdin"] != false {
		t.Fatalf("stock process/start fields = %+v", start)
	}
	envPolicy := start["envPolicy"].(map[string]any)
	if envPolicy["inherit"] != "none" || envPolicy["ignoreDefaultExcludes"] != false ||
		len(envPolicy["exclude"].([]any)) != 0 || len(envPolicy["includeOnly"].([]any)) != 0 || len(envPolicy["set"].(map[string]any)) != 0 {
		t.Fatalf("clean env policy = %+v", envPolicy)
	}
	sandbox := start["sandbox"].(map[string]any)
	if roots := sandbox["workspaceRoots"].([]any); len(roots) != 1 || roots[0] != plan.RootURI {
		t.Fatalf("workspaceRoots = %+v", roots)
	}
	entries := sandbox["permissions"].(map[string]any)["file_system"].(map[string]any)["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("sandbox entries = %+v", entries)
	}
	minimal := entries[0].(map[string]any)
	minimalPath := minimal["path"].(map[string]any)
	minimalValue := minimalPath["value"].(map[string]any)
	if minimal["access"] != "read" || minimalPath["type"] != "special" || minimalValue["kind"] != "minimal" {
		t.Fatalf("sandbox minimal runtime entry = %+v", minimal)
	}
	workspace := entries[1].(map[string]any)
	workspacePath := workspace["path"].(map[string]any)
	if workspace["access"] != "write" || workspacePath["type"] != "path" || workspacePath["path"] != plan.RootURI {
		t.Fatalf("sandbox workspace entry escaped registered root: %+v", workspace)
	}
	if _, special := workspacePath["value"]; special {
		t.Fatalf("special root leaked into shell-v1 workspace entry: %+v", workspacePath)
	}

	limits := agentxconn.Limits{MaxFrameBytes: 8 * 1024 * 1024, MaxJSONValues: 65_536, MaxJSONDepth: 256}
	startRouting := plan.Start.Routing
	if _, err := agentxconn.Encode(agentxconn.Frame{
		Type: agentxconn.MessageTypeRPC, SessionID: "73000000-0000-4000-8000-000000000001",
		SessionSeq: 1, Generation: environment.ConnectionGeneration, Context: &startRouting,
		Directives: &plan.Directives, RPC: plan.Start.RPC,
	}, limits); err != nil {
		t.Fatalf("mapped process/start violates agentx profile: %v", err)
	}
	timeoutRouting := plan.Timeout.Routing
	if _, err := agentxconn.Encode(agentxconn.Frame{
		Type: agentxconn.MessageTypeRPC, SessionID: "73000000-0000-4000-8000-000000000001",
		SessionSeq: 2, Generation: environment.ConnectionGeneration, Context: &timeoutRouting, RPC: plan.Timeout.RPC,
	}, limits); err != nil {
		t.Fatalf("mapped process/terminate violates agentx profile: %v", err)
	}
}

func TestMapShellV1DefaultsAndWindowsFileURIs(t *testing.T) {
	registered := testRegisteredEnvironment(testEnvironmentID, `{"kind":"local","root":"C:\\Workspace","defaultCwd":"src/pkg"}`)
	registered.Platform = "windows-amd64"
	environment, err := resolveRegisteredEnvironment(registered)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := MapShellV1(
		json.RawMessage(`{"environment_id":"60000000-0000-4000-8000-000000000006","argv":["tool.exe"]}`),
		testExecutorMCPPrincipal("capability-shell"), "call-shell-windows", environment, testShellPolicy(), testShellV1Identities(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if plan.RootURI != "file:///C:/Workspace" || plan.CWDURI != "file:///C:/Workspace/src/pkg" || plan.TimeoutMillis != ShellV1DefaultTimeoutMillis {
		t.Fatalf("Windows/default mapping = root %q cwd %q timeout %d", plan.RootURI, plan.CWDURI, plan.TimeoutMillis)
	}
	var params struct {
		Env     map[string]string `json:"env"`
		TTY     bool              `json:"tty"`
		Sandbox struct {
			WindowsSandboxLevel string `json:"windowsSandboxLevel"`
		} `json:"sandbox"`
	}
	if err := json.Unmarshal(plan.Start.Params, &params); err != nil {
		t.Fatal(err)
	}
	if params.Env == nil || len(params.Env) != 0 || params.TTY || params.Sandbox.WindowsSandboxLevel != "restricted-token" {
		t.Fatalf("Windows/default process params = %+v", params)
	}
}

func TestMapShellV1RejectsUnmappedOrEscapingInputs(t *testing.T) {
	environment, err := resolveRegisteredEnvironment(testRegisteredEnvironment(testEnvironmentID, `{"kind":"local","root":"/workspace","defaultCwd":"."}`))
	if err != nil {
		t.Fatal(err)
	}
	valid := `{"environment_id":"60000000-0000-4000-8000-000000000006","argv":["/bin/true"]%s}`
	tests := []string{
		fmt.Sprintf(valid, `,"future":true`),
		fmt.Sprintf(valid, `,"cwd":"../escape"`),
		fmt.Sprintf(valid, `,"env":{"BAD-NAME":"x"}`),
		fmt.Sprintf(valid, `,"timeout_ms":3600001`),
	}
	for _, raw := range tests {
		if _, err := MapShellV1(json.RawMessage(raw), testExecutorMCPPrincipal("capability-shell"), "call-shell-invalid", environment, testShellPolicy(), testShellV1Identities()); err == nil {
			t.Errorf("invalid shell input was accepted: %s", raw)
		}
	}
	wrongEnvironment := testShellV1Identities()
	if _, err := MapShellV1(
		json.RawMessage(`{"environment_id":"30000000-0000-4000-8000-000000000099","argv":["/bin/true"]}`),
		testExecutorMCPPrincipal("capability-shell"), "call-shell-invalid", environment, testShellPolicy(), wrongEnvironment,
	); err == nil {
		t.Fatal("mismatched environment identity was accepted")
	}
}

func TestMapShellV1ProjectsFrozenWorkspaceAndCodexPermissionModes(t *testing.T) {
	descriptor := json.RawMessage(`{"kind":"local","root":"/workspace/projects"}`)
	digest, err := workspaceauthority.RootDescriptorSHA256(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := resolveRegisteredEnvironment(testRegisteredEnvironment(testEnvironmentID, string(descriptor)))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		mode    runmanifest.CodexPermissionMode
		access  string
		version int64
	}{
		{mode: runmanifest.CodexPermissionModeReadOnly, access: "read", version: 1},
		{mode: runmanifest.CodexPermissionModeAuto, access: "write", version: 2},
		{mode: runmanifest.CodexPermissionModeFullAccess, access: "write", version: 3},
	} {
		t.Run(string(test.mode), func(t *testing.T) {
			principal := testExecutorMCPPrincipal("workspace-shell-" + string(test.mode))
			principal.PermissionMode = string(test.mode)
			principal.PermissionModeVersion = test.version
			principal.Workspace = &workspaceauthority.Binding{
				EnvironmentID: testEnvironmentID, EnvironmentVersion: environment.EnvironmentVersion,
				RootSHA256: digest, WorkingDirectory: "rtm-aihub", WorkingDirectoryVersion: 4,
			}
			plan, err := MapShellV1(
				json.RawMessage(`{"environment_id":"60000000-0000-4000-8000-000000000006","argv":["/bin/pwd"]}`),
				principal, "call-workspace-shell-"+string(test.mode), environment, testShellPolicy(), testShellV1Identities(),
			)
			if err != nil {
				t.Fatal(err)
			}
			wantPlanAccess := "write"
			if test.mode == runmanifest.CodexPermissionModeReadOnly {
				wantPlanAccess = "read"
			}
			if plan.WorkspaceRoot != "/workspace/projects" || plan.WorkingDirectory != "/workspace/projects/rtm-aihub" ||
				plan.CWDURI != "file:///workspace/projects/rtm-aihub" || plan.WorkspaceAccess != wantPlanAccess {
				t.Fatalf("workspace projection = root %q cwd %q uri %q access %q, want access %q", plan.WorkspaceRoot, plan.WorkingDirectory, plan.CWDURI, plan.WorkspaceAccess, wantPlanAccess)
			}
			var params struct {
				Sandbox struct {
					Permissions struct {
						FileSystem struct {
							Entries []struct {
								Access string `json:"access"`
							} `json:"entries"`
						} `json:"file_system"`
					} `json:"permissions"`
				} `json:"sandbox"`
			}
			if err := json.Unmarshal(plan.Start.Params, &params); err != nil {
				t.Fatal(err)
			}
			entries := params.Sandbox.Permissions.FileSystem.Entries
			if len(entries) != 2 || entries[1].Access != test.access {
				t.Fatalf("permission mode %q filesystem entries = %+v, want workspace access %q", test.mode, entries, test.access)
			}
		})
	}
	if _, err := MapShellV1(
		json.RawMessage(`{"environment_id":"60000000-0000-4000-8000-000000000006","argv":["/bin/pwd"],"cwd":"../escape"}`),
		func() ExecutorMCPPrincipal {
			principal := testExecutorMCPPrincipal("workspace-shell-escape")
			principal.PermissionMode = "read-only"
			principal.PermissionModeVersion = 1
			principal.Workspace = &workspaceauthority.Binding{EnvironmentID: testEnvironmentID, EnvironmentVersion: 1, RootSHA256: digest, WorkingDirectory: "rtm-aihub", WorkingDirectoryVersion: 4}
			return principal
		}(), "call-workspace-shell-escape", environment, testShellPolicy(), testShellV1Identities(),
	); err == nil {
		t.Fatal("shell cwd escaped frozen working directory")
	}
}

func TestMapShellV1RejectsWorkspaceRootDigestOrGenerationDrift(t *testing.T) {
	descriptor := json.RawMessage(`{"kind":"local","root":"/workspace/projects"}`)
	digest := sha256.Sum256(descriptor)
	principal := testExecutorMCPPrincipal("workspace-shell-drift")
	principal.PermissionMode = "read-only"
	principal.PermissionModeVersion = 1
	principal.Workspace = &workspaceauthority.Binding{EnvironmentID: testEnvironmentID, EnvironmentVersion: 1, RootSHA256: digest, WorkingDirectory: "rtm-aihub", WorkingDirectoryVersion: 1}
	for name, registered := range map[string]RegisteredEnvironment{
		"generation": func() RegisteredEnvironment {
			value := testRegisteredEnvironment(testEnvironmentID, string(descriptor))
			value.EnvironmentVersion = 2
			return value
		}(),
		"root": testRegisteredEnvironment(testEnvironmentID, `{"kind":"local","root":"/workspace/other"}`),
	} {
		t.Run(name, func(t *testing.T) {
			environment, err := resolveRegisteredEnvironment(registered)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := MapShellV1(json.RawMessage(`{"environment_id":"60000000-0000-4000-8000-000000000006","argv":["/bin/true"]}`), principal, "call-workspace-shell-drift-"+name, environment, testShellPolicy(), testShellV1Identities()); err == nil {
				t.Fatal("workspace drift was accepted")
			}
		})
	}
	mutatedProjection := environmentForWorkspaceTest(t, descriptor)
	mutatedProjection.TargetID = "different-target"
	if _, err := MapShellV1(json.RawMessage(`{"environment_id":"60000000-0000-4000-8000-000000000006","argv":["/bin/true"]}`), principal, "call-workspace-shell-target-drift", mutatedProjection, testShellPolicy(), testShellV1Identities()); err == nil {
		t.Fatal("mutated backend target projection was accepted")
	}
}

func environmentForWorkspaceTest(t *testing.T, descriptor json.RawMessage) ResolvedEnvironment {
	t.Helper()
	resolved, err := resolveRegisteredEnvironment(testRegisteredEnvironment(testEnvironmentID, string(descriptor)))
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func testShellPolicy() ExecutionPolicyResolution {
	return ExecutionPolicyResolution{Version: ShellV1PolicyVersion, Decision: PolicyDecisionAllow}
}

func TestShellV1IdentityAllocatorRejectsDuplicateGeneratorValues(t *testing.T) {
	allocator, err := NewShellV1IdentityAllocator(func() (string, error) {
		return "74000000-0000-4000-8000-000000000001", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := allocator.Allocate(); err == nil {
		t.Fatal("duplicate shell identities were accepted")
	}
}

func testShellV1Identities() ShellV1Identities {
	return ShellV1Identities{
		ExecutionID:         "50000000-0000-4000-8000-000000000005",
		ProcessID:           "80000000-0000-4000-8000-000000000008",
		StartOperationID:    "51000000-0000-4000-8000-000000000005",
		StartMutationKey:    "61000000-0000-4000-8000-000000000006",
		TimeoutOperationID:  "52000000-0000-4000-8000-000000000005",
		TimeoutMutationKey:  "62000000-0000-4000-8000-000000000006",
		StartRPCRequestID:   "75000000-0000-4000-8000-000000000001",
		TimeoutRPCRequestID: "76000000-0000-4000-8000-000000000001",
	}
}
