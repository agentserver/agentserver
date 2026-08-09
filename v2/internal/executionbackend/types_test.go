package executionbackend

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func testTarget(kind Kind) Target {
	return Target{Kind: kind, ID: "sandbox-123", Generation: 7, EnvironmentID: "env-123"}
}

func testOperationContext() OperationContext {
	return OperationContext{
		WorkspaceID: "workspace-123", SessionID: "session-123", RunID: "run-123",
		RunAttemptID: "attempt-123", RunAttemptGeneration: 2,
		ExecutionID: "execution-123", OperationID: "operation-123", MutationKey: "mutation-123",
	}
}

func TestTargetValidateSupportsOnlyKnownBackends(t *testing.T) {
	for _, kind := range []Kind{KindAgentX, KindTAE} {
		if err := testTarget(kind).Validate(); err != nil {
			t.Fatalf("Target.Validate(%q) error = %v", kind, err)
		}
	}
	for name, target := range map[string]Target{
		"unknown kind":      {Kind: "other", ID: "target", Generation: 1, EnvironmentID: "env"},
		"unsafe ID":         {Kind: KindTAE, ID: "target/escape", Generation: 1, EnvironmentID: "env"},
		"zero generation":   {Kind: KindTAE, ID: "target", Generation: 0, EnvironmentID: "env"},
		"empty environment": {Kind: KindTAE, ID: "target", Generation: 1},
	} {
		t.Run(name, func(t *testing.T) {
			if err := target.Validate(); err == nil {
				t.Fatal("Target.Validate() succeeded, want error")
			}
		})
	}
}

func TestStartProcessRequestValidateLarkCLI(t *testing.T) {
	request := StartProcessRequest{
		Target: testTarget(KindTAE), Operation: testOperationContext(), ProcessID: "process-123",
		RequestID:  "request-123",
		Executable: "lark-cli", Arguments: []string{"doc", "get", "doc-token"},
		WorkingDirectory: "/workspace", WorkspaceRoot: "/workspace", Platform: "linux-amd64",
		Environment: map[string]string{"LANG": "C.UTF-8"},
		Timeout:     30 * time.Second, OutputLimitBytes: 512 * 1024,
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("StartProcessRequest.Validate() error = %v", err)
	}

	tests := map[string]func(*StartProcessRequest){
		"empty executable":    func(value *StartProcessRequest) { value.Executable = "" },
		"unsafe operation ID": func(value *StartProcessRequest) { value.Operation.OperationID = "operation/123" },
		"missing session ID":  func(value *StartProcessRequest) { value.Operation.SessionID = "" },
		"NUL argument":        func(value *StartProcessRequest) { value.Arguments = []string{"bad\x00arg"} },
		"invalid env":         func(value *StartProcessRequest) { value.Environment = map[string]string{"BAD-NAME": "x"} },
		"zero timeout":        func(value *StartProcessRequest) { value.Timeout = 0 },
		"excess output":       func(value *StartProcessRequest) { value.OutputLimitBytes = MaxOutputBytes + 1 },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := request
			candidate.Arguments = append([]string(nil), request.Arguments...)
			candidate.Environment = map[string]string{"LANG": "C.UTF-8"}
			mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatal("StartProcessRequest.Validate() succeeded, want error")
			}
		})
	}

	tooMany := request
	tooMany.Arguments = make([]string, MaxArguments+1)
	if err := tooMany.Validate(); err == nil {
		t.Fatal("StartProcessRequest.Validate() accepted excessive argv")
	}
}

func TestSignalAndReadFileRequestsValidate(t *testing.T) {
	signal := SignalProcessRequest{
		Target: testTarget(KindTAE), Operation: testOperationContext(), ProcessID: "process-123",
		RequestID:      "request-signal",
		ProviderHandle: "provider-process-123", Signal: SignalTerminate, Reason: "hard deadline reached",
	}
	if err := signal.Validate(); err != nil {
		t.Fatalf("SignalProcessRequest.Validate() error = %v", err)
	}
	signal.Signal = "pause"
	if err := signal.Validate(); err == nil {
		t.Fatal("SignalProcessRequest.Validate() accepted unknown signal")
	}

	read := ReadFileRequest{
		Target: testTarget(KindTAE), Operation: testOperationContext(), Path: "/workspace/result.md",
		RequestID: "request-read",
		Offset:    10, Limit: 1024,
	}
	if err := read.Validate(); err != nil {
		t.Fatalf("ReadFileRequest.Validate() error = %v", err)
	}
	read.Offset = ^uint64(0) - 5
	read.Limit = 10
	if err := read.Validate(); err == nil {
		t.Fatal("ReadFileRequest.Validate() accepted offset overflow")
	}
}

func TestExchangeValueValidation(t *testing.T) {
	now := time.Now().UTC()
	acknowledgement := Acknowledgement{ProviderOperationID: "tae/session/op=123", ProviderRequestID: "request/1", AcceptedAt: now}
	if err := acknowledgement.Validate(); err != nil {
		t.Fatalf("Acknowledgement.Validate() error = %v", err)
	}
	acknowledgement.AcceptedAt = time.Time{}
	if err := acknowledgement.Validate(); err == nil {
		t.Fatal("Acknowledgement.Validate() accepted zero time")
	}

	event := Event{Sequence: 1, Kind: EventStdout, Data: []byte("hello")}
	if err := event.Validate(); err != nil {
		t.Fatalf("Event.Validate() error = %v", err)
	}
	event.Data = make([]byte, MaxEventBytes+1)
	if err := event.Validate(); err == nil {
		t.Fatal("Event.Validate() accepted oversized data")
	}

	exitCode := int32(0)
	terminal := TerminalResult{
		Status: TerminalSucceeded, ExitCode: &exitCode, OutputComplete: true, CompletedAt: now,
	}
	if err := terminal.Validate(); err != nil {
		t.Fatalf("TerminalResult.Validate() error = %v", err)
	}
	terminal.ReasonCode = "Provider Error"
	if err := terminal.Validate(); err == nil {
		t.Fatal("TerminalResult.Validate() accepted unsafe reason code")
	}
}

func TestDispatchErrorPreservesOutcomeAndCause(t *testing.T) {
	cause := errors.New("connection refused")
	dispatchError := NewDispatchError(OutcomeNotSent, "dial_failed", cause)
	dispatchError.ProviderRequestID = "request-1"
	if err := dispatchError.Validate(); err != nil {
		t.Fatalf("DispatchError.Validate() error = %v", err)
	}
	if !errors.Is(dispatchError, cause) {
		t.Fatal("DispatchError does not unwrap its cause")
	}
	if !ProvesNotSent(dispatchError) || OutcomeOf(dispatchError) != OutcomeNotSent {
		t.Fatalf("dispatch outcome = %q, want not_sent", OutcomeOf(dispatchError))
	}
	if OutcomeOf(errors.New("plain error")) != OutcomeUnknown {
		t.Fatal("plain error must not be treated as retry evidence")
	}

	acceptedError := NewDispatchError(OutcomeAccepted, "invalid", cause)
	if err := acceptedError.Validate(); err == nil || !strings.Contains(err.Error(), "accepted") {
		t.Fatalf("accepted DispatchError.Validate() error = %v", err)
	}
}
