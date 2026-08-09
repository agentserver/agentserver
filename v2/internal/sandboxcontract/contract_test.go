package sandboxcontract

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/executionbackend"
)

func validSessionIdentity() SessionIdentity {
	return SessionIdentity{WorkspaceID: "workspace-123", SessionID: "session-123", EnvironmentID: "env-123"}
}

func validOperationIdentity() OperationIdentity {
	return OperationIdentity{
		Session: validSessionIdentity(), RunID: "run-123", RunAttemptID: "attempt-123",
		RunAttemptGeneration: 2, ExecutionID: "execution-123", OperationID: "operation-123",
		MutationKey: "mutation-123",
	}
}

func validSandboxRef() SandboxRef {
	return SandboxRef{SandboxID: "sandbox-123", TargetGeneration: 4}
}

func TestDefaultLimitsValidate(t *testing.T) {
	if err := DefaultLimits().Validate(); err != nil {
		t.Fatalf("DefaultLimits().Validate() error = %v", err)
	}
	limits := DefaultLimits()
	limits.MaxSandboxTTLSeconds = limits.MinSandboxTTLSeconds - 1
	if err := limits.Validate(); err == nil {
		t.Fatal("Limits.Validate() accepted inverted TTL range")
	}
}

func TestEnsureSandboxRequestAndReadyResponseValidate(t *testing.T) {
	request := EnsureSandboxRequest{
		Profile: ProfileV1, RequestID: "request-123", Session: validSessionIdentity(),
		RequestedTTLSeconds:  600,
		RuntimeProfileDigest: strings.Repeat("a", 64), PackSetDigest: strings.Repeat("b", 64),
	}
	if err := request.Validate(DefaultLimits()); err != nil {
		t.Fatalf("EnsureSandboxRequest.Validate() error = %v", err)
	}

	response := EnsureSandboxResponse{Sandbox: Sandbox{
		Profile: ProfileV1, Ref: validSandboxRef(), State: SandboxReady,
		Root: "/workspace", ExpiresAt: time.Now().UTC().Add(time.Hour),
	}}
	if err := response.Validate(); err != nil {
		t.Fatalf("EnsureSandboxResponse.Validate() error = %v", err)
	}

	request.Profile = "e2b/v1"
	if err := request.Validate(DefaultLimits()); err == nil {
		t.Fatal("EnsureSandboxRequest.Validate() accepted unsupported profile")
	}
	request.Profile = ProfileV1
	request.RequestedTTLSeconds = DefaultLimits().MaxSandboxTTLSeconds + 1
	if err := request.Validate(DefaultLimits()); err == nil {
		t.Fatal("EnsureSandboxRequest.Validate() accepted excessive TTL")
	}
	request.RequestedTTLSeconds = 600
	request.PackSetDigest = "not-a-digest"
	if err := request.Validate(DefaultLimits()); err == nil {
		t.Fatal("EnsureSandboxRequest.Validate() accepted invalid digest")
	}
}

func TestRunCommandRequestPreservesArgvAndValidatesManagedPaths(t *testing.T) {
	request := RunCommandRequest{
		Profile: ProfileV1, RequestID: "request-123", Identity: validOperationIdentity(), Ref: validSandboxRef(),
		ProcessID: "process-123", Executable: "lark-cli",
		Arguments: []string{"doc", "get", "token with spaces"}, WorkingDirectory: "/workspace",
		Environment: map[string]string{"LANG": "C.UTF-8"}, TimeoutMillis: 30_000, OutputLimitBytes: 512 * 1024,
	}
	if err := request.Validate(DefaultLimits()); err != nil {
		t.Fatalf("RunCommandRequest.Validate() error = %v", err)
	}
	if request.Arguments[2] != "token with spaces" {
		t.Fatal("RunCommandRequest validation rewrote argv")
	}

	tests := map[string]func(*RunCommandRequest){
		"shell expression executable": func(value *RunCommandRequest) { value.Executable = "sh -c" },
		"relative cwd":                func(value *RunCommandRequest) { value.WorkingDirectory = "workspace" },
		"parent cwd":                  func(value *RunCommandRequest) { value.WorkingDirectory = "/workspace/../secret" },
		"zero timeout":                func(value *RunCommandRequest) { value.TimeoutMillis = 0 },
		"excess output":               func(value *RunCommandRequest) { value.OutputLimitBytes = DefaultLimits().MaxOutputBytes + 1 },
		"invalid env":                 func(value *RunCommandRequest) { value.Environment = map[string]string{"BAD-NAME": "value"} },
		"stale generation":            func(value *RunCommandRequest) { value.Ref.TargetGeneration = 0 },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := request
			candidate.Arguments = append([]string(nil), request.Arguments...)
			candidate.Environment = map[string]string{"LANG": "C.UTF-8"}
			mutate(&candidate)
			if err := candidate.Validate(DefaultLimits()); err == nil {
				t.Fatal("RunCommandRequest.Validate() succeeded, want error")
			}
		})
	}
}

func TestSignalAndReadFileRequestsValidate(t *testing.T) {
	signal := SignalCommandRequest{
		Profile: ProfileV1, RequestID: "request-signal", Identity: validOperationIdentity(), Ref: validSandboxRef(),
		ProcessID: "process-123", ProviderHandle: "provider-process", Signal: executionbackend.SignalTerminate,
		Reason: "deadline reached",
	}
	if err := signal.Validate(DefaultLimits()); err != nil {
		t.Fatalf("SignalCommandRequest.Validate() error = %v", err)
	}

	read := ReadFileRequest{
		Profile: ProfileV1, RequestID: "request-read", Identity: validOperationIdentity(), Ref: validSandboxRef(),
		Path: "/workspace/result.md", Offset: 0, Limit: 4096,
	}
	if err := read.Validate(DefaultLimits()); err != nil {
		t.Fatalf("ReadFileRequest.Validate() error = %v", err)
	}
	read.Path = "/workspace/../credential"
	if err := read.Validate(DefaultLimits()); err == nil {
		t.Fatal("ReadFileRequest.Validate() accepted parent path")
	}
}

func TestSetSandboxTimeoutRequestValidate(t *testing.T) {
	request := SetSandboxTimeoutRequest{
		Profile: ProfileV1, RequestID: "request-timeout", Session: validSessionIdentity(),
		Ref: validSandboxRef(), TTLSeconds: 600,
	}
	if err := request.Validate(DefaultLimits()); err != nil {
		t.Fatalf("SetSandboxTimeoutRequest.Validate() error = %v", err)
	}
	request.TTLSeconds = DefaultLimits().MaxSandboxTTLSeconds + 1
	if err := request.Validate(DefaultLimits()); err == nil {
		t.Fatal("SetSandboxTimeoutRequest.Validate() accepted excessive TTL")
	}
}

func TestOperationEnvelopesValidateIdentityAndGeneration(t *testing.T) {
	now := time.Now().UTC()
	acknowledgement := OperationAcknowledgement{
		Profile: ProfileV1, Identity: validOperationIdentity(), Ref: validSandboxRef(),
		Acknowledgement: executionbackend.Acknowledgement{ProviderOperationID: "provider-op", AcceptedAt: now},
	}
	if err := acknowledgement.Validate(); err != nil {
		t.Fatalf("OperationAcknowledgement.Validate() error = %v", err)
	}
	event := OperationEvent{
		Profile: ProfileV1, Identity: validOperationIdentity(), Ref: validSandboxRef(),
		Event: executionbackend.Event{Sequence: 1, Kind: executionbackend.EventStdout, Data: []byte("ok")},
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("OperationEvent.Validate() error = %v", err)
	}
	terminal := OperationTerminal{
		Profile: ProfileV1, Identity: validOperationIdentity(), Ref: validSandboxRef(),
		Terminal: executionbackend.TerminalResult{Status: executionbackend.TerminalSucceeded, OutputComplete: true, CompletedAt: now},
	}
	if err := terminal.Validate(); err != nil {
		t.Fatalf("OperationTerminal.Validate() error = %v", err)
	}
}

func TestContractPathsRejectPathInjection(t *testing.T) {
	path, err := RunCommandPath("sandbox-123")
	if err != nil || path != "/internal/v2/sandboxes/sandbox-123/commands:run" {
		t.Fatalf("RunCommandPath() = %q, %v", path, err)
	}
	path, err = SignalProcessPath("sandbox-123", "process-123")
	if err != nil || path != "/internal/v2/sandboxes/sandbox-123/processes/process-123:signal" {
		t.Fatalf("SignalProcessPath() = %q, %v", path, err)
	}
	path, err = SetSandboxTimeoutPath("sandbox-123")
	if err != nil || path != "/internal/v2/sandboxes/sandbox-123:set-timeout" {
		t.Fatalf("SetSandboxTimeoutPath() = %q, %v", path, err)
	}
	if _, err := RunCommandPath("../escape"); err == nil {
		t.Fatal("RunCommandPath() accepted path injection")
	}
}

func TestRunCommandJSONContractIsVersionedAndNested(t *testing.T) {
	request := RunCommandRequest{
		Profile: ProfileV1, RequestID: "request-123", Identity: validOperationIdentity(), Ref: validSandboxRef(),
		ProcessID: "process-123", Executable: "lark-cli", Arguments: []string{"doc", "get"},
		WorkingDirectory: "/workspace", TimeoutMillis: 1000, OutputLimitBytes: 1024,
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		"profile", "requestId", "identity", "ref", "processId", "executable", "arguments",
		"workingDirectory", "timeoutMillis", "outputLimitBytes",
	} {
		if _, exists := document[field]; !exists {
			t.Fatalf("RunCommandRequest JSON is missing %q: %s", field, encoded)
		}
	}
	if string(document["profile"]) != `"e2b-semantic-subset/v1"` {
		t.Fatalf("RunCommandRequest profile = %s", document["profile"])
	}
}
