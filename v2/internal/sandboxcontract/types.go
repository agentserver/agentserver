package sandboxcontract

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/executionbackend"
)

const ProfileV1 = "e2b-semantic-subset/v1"

var (
	contractIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$`)
	executablePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,255}$`)
)

type SessionIdentity struct {
	WorkspaceID   string `json:"workspaceId"`
	SessionID     string `json:"sessionId"`
	EnvironmentID string `json:"environmentId"`
}

func (identity SessionIdentity) Validate() error {
	for _, value := range []struct {
		name string
		text string
	}{
		{"workspace ID", identity.WorkspaceID},
		{"session ID", identity.SessionID},
		{"environment ID", identity.EnvironmentID},
	} {
		if err := validateID(value.name, value.text); err != nil {
			return err
		}
	}
	return nil
}

type OperationIdentity struct {
	Session              SessionIdentity `json:"session"`
	RunID                string          `json:"runId"`
	RunAttemptID         string          `json:"runAttemptId"`
	RunAttemptGeneration int64           `json:"runAttemptGeneration"`
	ExecutionID          string          `json:"executionId"`
	OperationID          string          `json:"operationId"`
	MutationKey          string          `json:"mutationKey"`
}

func (identity OperationIdentity) Validate() error {
	if err := identity.Session.Validate(); err != nil {
		return err
	}
	for _, value := range []struct {
		name string
		text string
	}{
		{"run ID", identity.RunID},
		{"run attempt ID", identity.RunAttemptID},
		{"execution ID", identity.ExecutionID},
		{"operation ID", identity.OperationID},
		{"mutation key", identity.MutationKey},
	} {
		if err := validateID(value.name, value.text); err != nil {
			return err
		}
	}
	if identity.RunAttemptGeneration < 1 {
		return errors.New("run attempt generation must be positive")
	}
	return nil
}

func (identity OperationIdentity) BackendContext() executionbackend.OperationContext {
	return executionbackend.OperationContext{
		WorkspaceID: identity.Session.WorkspaceID, SessionID: identity.Session.SessionID,
		RunID: identity.RunID, RunAttemptID: identity.RunAttemptID,
		RunAttemptGeneration: identity.RunAttemptGeneration,
		ExecutionID:          identity.ExecutionID, OperationID: identity.OperationID,
		MutationKey: identity.MutationKey,
	}
}

type SandboxRef struct {
	SandboxID        string `json:"sandboxId"`
	TargetGeneration int64  `json:"targetGeneration"`
}

func (ref SandboxRef) Validate() error {
	if err := validateID("sandbox ID", ref.SandboxID); err != nil {
		return err
	}
	if ref.TargetGeneration < 1 {
		return errors.New("sandbox target generation must be positive")
	}
	return nil
}

func (ref SandboxRef) Target(environmentID string) executionbackend.Target {
	return executionbackend.Target{
		Kind: executionbackend.KindTAE, ID: ref.SandboxID,
		Generation: ref.TargetGeneration, EnvironmentID: environmentID,
	}
}

type SandboxState string

const (
	SandboxReserved SandboxState = "reserved"
	SandboxCreating SandboxState = "creating"
	SandboxReady    SandboxState = "ready"
	SandboxDeleting SandboxState = "deleting"
	SandboxDeleted  SandboxState = "deleted"
	SandboxFailed   SandboxState = "failed"
	SandboxUnknown  SandboxState = "unknown"
)

func (state SandboxState) Validate() error {
	switch state {
	case SandboxReserved, SandboxCreating, SandboxReady, SandboxDeleting, SandboxDeleted, SandboxFailed, SandboxUnknown:
		return nil
	default:
		return fmt.Errorf("unsupported sandbox state %q", state)
	}
}

type Sandbox struct {
	Profile   string       `json:"profile"`
	Ref       SandboxRef   `json:"ref"`
	State     SandboxState `json:"state"`
	Root      string       `json:"root,omitempty"`
	ExpiresAt time.Time    `json:"expiresAt,omitempty"`
}

func (sandbox Sandbox) Validate() error {
	if err := validateProfile(sandbox.Profile); err != nil {
		return err
	}
	if err := sandbox.Ref.Validate(); err != nil {
		return err
	}
	if err := sandbox.State.Validate(); err != nil {
		return err
	}
	if sandbox.State == SandboxReady {
		if err := validateAbsolutePath("sandbox root", sandbox.Root); err != nil {
			return err
		}
		if sandbox.ExpiresAt.IsZero() {
			return errors.New("ready sandbox expiry is required")
		}
	} else if sandbox.Root != "" {
		if err := validateAbsolutePath("sandbox root", sandbox.Root); err != nil {
			return err
		}
	}
	return nil
}

type EnsureSandboxRequest struct {
	Profile             string          `json:"profile"`
	RequestID           string          `json:"requestId"`
	Session             SessionIdentity `json:"session"`
	RequestedTTLSeconds int64           `json:"requestedTtlSeconds"`
}

func (request EnsureSandboxRequest) Validate(limits Limits) error {
	if err := validateLimitsAndRequest(limits, request.Profile, request.RequestID); err != nil {
		return err
	}
	if err := request.Session.Validate(); err != nil {
		return err
	}
	if request.RequestedTTLSeconds < limits.MinSandboxTTLSeconds || request.RequestedTTLSeconds > limits.MaxSandboxTTLSeconds {
		return fmt.Errorf("requested sandbox TTL must be between %d and %d seconds", limits.MinSandboxTTLSeconds, limits.MaxSandboxTTLSeconds)
	}
	return nil
}

type EnsureSandboxResponse struct {
	Sandbox Sandbox `json:"sandbox"`
}

func (response EnsureSandboxResponse) Validate() error { return response.Sandbox.Validate() }

type SandboxResponse struct {
	Sandbox Sandbox `json:"sandbox"`
	Changed bool    `json:"changed,omitempty"`
}

func (response SandboxResponse) Validate() error { return response.Sandbox.Validate() }

type GetSandboxRequest struct {
	Profile   string          `json:"profile"`
	RequestID string          `json:"requestId"`
	Session   SessionIdentity `json:"session"`
	Ref       SandboxRef      `json:"ref"`
}

func (request GetSandboxRequest) Validate(limits Limits) error {
	if err := validateLimitsAndRequest(limits, request.Profile, request.RequestID); err != nil {
		return err
	}
	if err := request.Session.Validate(); err != nil {
		return err
	}
	return request.Ref.Validate()
}

type SetSandboxTimeoutRequest struct {
	Profile    string          `json:"profile"`
	RequestID  string          `json:"requestId"`
	Session    SessionIdentity `json:"session"`
	Ref        SandboxRef      `json:"ref"`
	TTLSeconds int64           `json:"ttlSeconds"`
}

func (request SetSandboxTimeoutRequest) Validate(limits Limits) error {
	if err := validateLimitsAndRequest(limits, request.Profile, request.RequestID); err != nil {
		return err
	}
	if err := request.Session.Validate(); err != nil {
		return err
	}
	if err := request.Ref.Validate(); err != nil {
		return err
	}
	if request.TTLSeconds < limits.MinSandboxTTLSeconds || request.TTLSeconds > limits.MaxSandboxTTLSeconds {
		return fmt.Errorf("sandbox TTL must be between %d and %d seconds", limits.MinSandboxTTLSeconds, limits.MaxSandboxTTLSeconds)
	}
	return nil
}

type RenewSandboxActivityRequest struct {
	Profile              string          `json:"profile"`
	RequestID            string          `json:"requestId"`
	Session              SessionIdentity `json:"session"`
	Ref                  SandboxRef      `json:"ref"`
	RunAttemptID         string          `json:"runAttemptId"`
	RunAttemptGeneration int64           `json:"runAttemptGeneration"`
	ActivityTTLSeconds   int64           `json:"activityTtlSeconds"`
}

func (request RenewSandboxActivityRequest) Validate(limits Limits) error {
	if err := validateLimitsAndRequest(limits, request.Profile, request.RequestID); err != nil {
		return err
	}
	if err := request.Session.Validate(); err != nil {
		return err
	}
	if err := request.Ref.Validate(); err != nil {
		return err
	}
	if err := validateID("run attempt ID", request.RunAttemptID); err != nil {
		return err
	}
	if request.RunAttemptGeneration < 1 {
		return errors.New("run attempt generation must be positive")
	}
	if request.ActivityTTLSeconds < 1 || request.ActivityTTLSeconds > limits.MaxActivityTTLSeconds {
		return fmt.Errorf("activity TTL must be between 1 and %d seconds", limits.MaxActivityTTLSeconds)
	}
	return nil
}

type ReleaseSandboxActivityRequest struct {
	Profile              string          `json:"profile"`
	RequestID            string          `json:"requestId"`
	Session              SessionIdentity `json:"session"`
	Ref                  SandboxRef      `json:"ref"`
	RunAttemptID         string          `json:"runAttemptId"`
	RunAttemptGeneration int64           `json:"runAttemptGeneration"`
}

func (request ReleaseSandboxActivityRequest) Validate(limits Limits) error {
	if err := validateLimitsAndRequest(limits, request.Profile, request.RequestID); err != nil {
		return err
	}
	if err := request.Session.Validate(); err != nil {
		return err
	}
	if err := request.Ref.Validate(); err != nil {
		return err
	}
	if err := validateID("run attempt ID", request.RunAttemptID); err != nil {
		return err
	}
	if request.RunAttemptGeneration < 1 {
		return errors.New("run attempt generation must be positive")
	}
	return nil
}

type DeleteSandboxRequest struct {
	Profile   string          `json:"profile"`
	RequestID string          `json:"requestId"`
	Session   SessionIdentity `json:"session"`
	Ref       SandboxRef      `json:"ref"`
	Reason    string          `json:"reason"`
}

func (request DeleteSandboxRequest) Validate(limits Limits) error {
	if err := validateLimitsAndRequest(limits, request.Profile, request.RequestID); err != nil {
		return err
	}
	if err := request.Session.Validate(); err != nil {
		return err
	}
	if err := request.Ref.Validate(); err != nil {
		return err
	}
	return validateText("sandbox delete reason", request.Reason, 1, 1024)
}

type RunCommandRequest struct {
	Profile          string            `json:"profile"`
	RequestID        string            `json:"requestId"`
	Identity         OperationIdentity `json:"identity"`
	Ref              SandboxRef        `json:"ref"`
	ProcessID        string            `json:"processId"`
	Executable       string            `json:"executable"`
	Arguments        []string          `json:"arguments"`
	WorkingDirectory string            `json:"workingDirectory"`
	// Environment is the final clean environment assembled by the execution
	// gateway after policy validation. A webhook_swap workspace carries only a
	// short-lived placeholder; a process_env workspace carries the live
	// provider credential only for an exact policy-approved managed CLI process
	// start. Transports and logs must redact
	// every value and must never persist this map.
	Environment      map[string]string `json:"environment,omitempty"`
	TimeoutMillis    int64             `json:"timeoutMillis"`
	OutputLimitBytes int64             `json:"outputLimitBytes"`
}

func (request RunCommandRequest) Validate(limits Limits) error {
	if err := validateLimitsAndRequest(limits, request.Profile, request.RequestID); err != nil {
		return err
	}
	if err := request.Identity.Validate(); err != nil {
		return err
	}
	if err := request.Ref.Validate(); err != nil {
		return err
	}
	if err := validateExecutable(request.Executable); err != nil {
		return err
	}
	if err := validateAbsolutePath("command working directory", request.WorkingDirectory); err != nil {
		return err
	}
	if request.TimeoutMillis < 1 || request.TimeoutMillis > limits.MaxCommandTimeoutMillis {
		return fmt.Errorf("command timeout must be between 1 and %d milliseconds", limits.MaxCommandTimeoutMillis)
	}
	if request.OutputLimitBytes < 1 || request.OutputLimitBytes > limits.MaxOutputBytes {
		return fmt.Errorf("command output limit must be between 1 and %d bytes", limits.MaxOutputBytes)
	}
	backendRequest := executionbackend.StartProcessRequest{
		Target:    request.Ref.Target(request.Identity.Session.EnvironmentID),
		Operation: request.Identity.BackendContext(), ProcessID: request.ProcessID,
		RequestID:  request.RequestID,
		Executable: request.Executable, Arguments: request.Arguments,
		WorkingDirectory: request.WorkingDirectory, WorkspaceRoot: "/workspace", Platform: "linux-amd64",
		Environment:      request.Environment,
		Timeout:          time.Duration(request.TimeoutMillis) * time.Millisecond,
		OutputLimitBytes: request.OutputLimitBytes,
	}
	return backendRequest.Validate()
}

type SignalCommandRequest struct {
	Profile        string                  `json:"profile"`
	RequestID      string                  `json:"requestId"`
	Identity       OperationIdentity       `json:"identity"`
	Ref            SandboxRef              `json:"ref"`
	ProcessID      string                  `json:"processId"`
	ProviderHandle string                  `json:"providerHandle,omitempty"`
	Signal         executionbackend.Signal `json:"signal"`
	Reason         string                  `json:"reason"`
}

func (request SignalCommandRequest) Validate(limits Limits) error {
	if err := validateLimitsAndRequest(limits, request.Profile, request.RequestID); err != nil {
		return err
	}
	if err := request.Identity.Validate(); err != nil {
		return err
	}
	if err := request.Ref.Validate(); err != nil {
		return err
	}
	backendRequest := executionbackend.SignalProcessRequest{
		Target:    request.Ref.Target(request.Identity.Session.EnvironmentID),
		Operation: request.Identity.BackendContext(), ProcessID: request.ProcessID,
		RequestID:      request.RequestID,
		ProviderHandle: request.ProviderHandle, Signal: request.Signal, Reason: request.Reason,
	}
	return backendRequest.Validate()
}

type ReadFileRequest struct {
	Profile   string            `json:"profile"`
	RequestID string            `json:"requestId"`
	Identity  OperationIdentity `json:"identity"`
	Ref       SandboxRef        `json:"ref"`
	Path      string            `json:"path"`
	Offset    uint64            `json:"offset"`
	Limit     uint64            `json:"limit"`
}

func (request ReadFileRequest) Validate(limits Limits) error {
	if err := validateLimitsAndRequest(limits, request.Profile, request.RequestID); err != nil {
		return err
	}
	if err := request.Identity.Validate(); err != nil {
		return err
	}
	if err := request.Ref.Validate(); err != nil {
		return err
	}
	if err := validateAbsolutePath("read-file path", request.Path); err != nil {
		return err
	}
	if request.Limit < 1 || request.Limit > limits.MaxReadFileBytes {
		return fmt.Errorf("read-file limit must be between 1 and %d bytes", limits.MaxReadFileBytes)
	}
	backendRequest := executionbackend.ReadFileRequest{
		Target:    request.Ref.Target(request.Identity.Session.EnvironmentID),
		Operation: request.Identity.BackendContext(),
		RequestID: request.RequestID,
		Path:      request.Path, Offset: request.Offset, Limit: request.Limit,
	}
	return backendRequest.Validate()
}

type OperationAcknowledgement struct {
	Profile         string                           `json:"profile"`
	Identity        OperationIdentity                `json:"identity"`
	Ref             SandboxRef                       `json:"ref"`
	Acknowledgement executionbackend.Acknowledgement `json:"acknowledgement"`
}

func (acknowledgement OperationAcknowledgement) Validate() error {
	if err := validateProfile(acknowledgement.Profile); err != nil {
		return err
	}
	if err := acknowledgement.Identity.Validate(); err != nil {
		return err
	}
	if err := acknowledgement.Ref.Validate(); err != nil {
		return err
	}
	return acknowledgement.Acknowledgement.Validate()
}

type OperationEvent struct {
	Profile  string                 `json:"profile"`
	Identity OperationIdentity      `json:"identity"`
	Ref      SandboxRef             `json:"ref"`
	Event    executionbackend.Event `json:"event"`
}

func (event OperationEvent) Validate() error {
	if err := validateProfile(event.Profile); err != nil {
		return err
	}
	if err := event.Identity.Validate(); err != nil {
		return err
	}
	if err := event.Ref.Validate(); err != nil {
		return err
	}
	return event.Event.Validate()
}

type OperationTerminal struct {
	Profile  string                          `json:"profile"`
	Identity OperationIdentity               `json:"identity"`
	Ref      SandboxRef                      `json:"ref"`
	Terminal executionbackend.TerminalResult `json:"terminal"`
}

func (terminal OperationTerminal) Validate() error {
	if err := validateProfile(terminal.Profile); err != nil {
		return err
	}
	if err := terminal.Identity.Validate(); err != nil {
		return err
	}
	if err := terminal.Ref.Validate(); err != nil {
		return err
	}
	return terminal.Terminal.Validate()
}

type OperationFrameType string

const (
	OperationFrameAcknowledgement OperationFrameType = "acknowledgement"
	OperationFrameEvent           OperationFrameType = "event"
	OperationFrameTerminal        OperationFrameType = "terminal"
)

// OperationFrame is the strict NDJSON stream emitted by sandbox-gateway.
// Exactly one payload must be present, matching Type. Identity and Ref are
// repeated on every frame so reconnecting clients can fence a stale stream
// before consuming provider output.
type OperationFrame struct {
	Profile         string                            `json:"profile"`
	Type            OperationFrameType                `json:"type"`
	Identity        OperationIdentity                 `json:"identity"`
	Ref             SandboxRef                        `json:"ref"`
	Acknowledgement *executionbackend.Acknowledgement `json:"acknowledgement,omitempty"`
	Event           *executionbackend.Event           `json:"event,omitempty"`
	Terminal        *executionbackend.TerminalResult  `json:"terminal,omitempty"`
}

func (frame OperationFrame) Validate() error {
	if err := validateProfile(frame.Profile); err != nil {
		return err
	}
	if err := frame.Identity.Validate(); err != nil {
		return err
	}
	if err := frame.Ref.Validate(); err != nil {
		return err
	}
	switch frame.Type {
	case OperationFrameAcknowledgement:
		if frame.Acknowledgement == nil || frame.Event != nil || frame.Terminal != nil {
			return errors.New("acknowledgement frame must contain only acknowledgement")
		}
		return frame.Acknowledgement.Validate()
	case OperationFrameEvent:
		if frame.Acknowledgement != nil || frame.Event == nil || frame.Terminal != nil {
			return errors.New("event frame must contain only event")
		}
		return frame.Event.Validate()
	case OperationFrameTerminal:
		if frame.Acknowledgement != nil || frame.Event != nil || frame.Terminal == nil {
			return errors.New("terminal frame must contain only terminal")
		}
		return frame.Terminal.Validate()
	default:
		return fmt.Errorf("unsupported operation frame type %q", frame.Type)
	}
}

type ErrorResponse struct {
	Code               string `json:"code"`
	Message            string `json:"message"`
	Outcome            string `json:"outcome,omitempty"`
	ProviderRequestID  string `json:"providerRequestId,omitempty"`
	ProviderCode       string `json:"providerCode,omitempty"`
	ProviderHTTPStatus int    `json:"providerHttpStatus,omitempty"`
	RequestWritten     *bool  `json:"requestWritten,omitempty"`
}

func validateLimitsAndRequest(limits Limits, profile, requestID string) error {
	if err := limits.Validate(); err != nil {
		return err
	}
	if err := validateProfile(profile); err != nil {
		return err
	}
	return validateID("request ID", requestID)
}

func validateProfile(profile string) error {
	if profile != ProfileV1 {
		return fmt.Errorf("unsupported sandbox contract profile %q", profile)
	}
	return nil
}

func validateID(name, value string) error {
	if !contractIDPattern.MatchString(value) {
		return fmt.Errorf("%s must match %s", name, contractIDPattern)
	}
	return nil
}

func validateText(name, value string, minimumRunes, maximumRunes int) error {
	count := utf8.RuneCountInString(value)
	if !utf8.ValidString(value) || strings.ContainsRune(value, 0) || count < minimumRunes || count > maximumRunes {
		return fmt.Errorf("%s must be valid UTF-8 without NUL and contain between %d and %d characters", name, minimumRunes, maximumRunes)
	}
	return nil
}

func validateExecutable(executable string) error {
	if err := validateText("command executable", executable, 1, executionbackend.MaxPathRunes); err != nil {
		return err
	}
	if !strings.Contains(executable, "/") {
		if !executablePattern.MatchString(executable) {
			return errors.New("command executable basename is invalid")
		}
		return nil
	}
	return validateAbsolutePath("command executable", executable)
}

func validateAbsolutePath(name, value string) error {
	if err := validateText(name, value, 1, executionbackend.MaxPathRunes); err != nil {
		return err
	}
	if !strings.HasPrefix(value, "/") || path.Clean(value) != value {
		return fmt.Errorf("%s must be a clean absolute Unix path", name)
	}
	return nil
}
