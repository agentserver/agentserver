package executionbackend

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxArguments          = 256
	MaxArgumentRunes      = 16 * 1024
	MaxEnvironmentEntries = 256
	MaxEnvironmentRunes   = 16 * 1024
	MaxPathRunes          = 4096
	MaxEventBytes         = 256 * 1024
	MaxOutputBytes        = 16 * 1024 * 1024
	MaxReadFileBytes      = 8 * 1024 * 1024
	MaxOperationTimeout   = 24 * time.Hour
	// WorkspaceAccessRead and WorkspaceAccessWrite are the only filesystem
	// authority projections a process backend may receive. An empty value is
	// the legacy wire representation of write access.
	WorkspaceAccessRead  = "read"
	WorkspaceAccessWrite = "write"
)

var (
	opaqueIDPattern        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$`)
	environmentNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	reasonCodePattern      = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

type Kind string

const (
	KindAgentX Kind = "agentx"
	KindTAE    Kind = "tae"
)

func (kind Kind) Validate() error {
	switch kind {
	case KindAgentX, KindTAE:
		return nil
	default:
		return fmt.Errorf("unsupported execution backend kind %q", kind)
	}
}

// Target is an agentserver identity. ID is never a provider session ID.
type Target struct {
	Kind          Kind   `json:"kind"`
	ID            string `json:"id"`
	Generation    int64  `json:"generation"`
	EnvironmentID string `json:"environmentId"`
}

func (target Target) Validate() error {
	if err := target.Kind.Validate(); err != nil {
		return err
	}
	if err := validateOpaqueID("dispatch target ID", target.ID); err != nil {
		return err
	}
	if target.Generation < 1 {
		return errors.New("dispatch target generation must be positive")
	}
	return validateOpaqueID("dispatch target environment ID", target.EnvironmentID)
}

// OperationContext is the provider-neutral identity frozen by Core before an
// external dispatch. Backends must forward this identity unchanged and must
// never derive, replace, or retry it with a new operation identity.
//
// SessionID is required even for agentx so the same contract can be used by a
// session-scoped managed sandbox. Agentx adapters do not put SessionID on the
// existing wire; it remains authority metadata at the execution boundary.
type OperationContext struct {
	WorkspaceID          string `json:"workspaceId"`
	SessionID            string `json:"sessionId"`
	RunID                string `json:"runId"`
	RunAttemptID         string `json:"runAttemptId"`
	RunAttemptGeneration int64  `json:"runAttemptGeneration"`
	ExecutionID          string `json:"executionId"`
	OperationID          string `json:"operationId"`
	MutationKey          string `json:"mutationKey"`
}

func (operation OperationContext) Validate() error {
	for _, identity := range []struct {
		name  string
		value string
	}{
		{"operation workspace ID", operation.WorkspaceID},
		{"operation session ID", operation.SessionID},
		{"operation run ID", operation.RunID},
		{"operation run attempt ID", operation.RunAttemptID},
		{"operation execution ID", operation.ExecutionID},
		{"operation ID", operation.OperationID},
		{"operation mutation key", operation.MutationKey},
	} {
		if err := validateOpaqueID(identity.name, identity.value); err != nil {
			return err
		}
	}
	if operation.RunAttemptGeneration < 1 {
		return errors.New("operation run attempt generation must be positive")
	}
	return nil
}

type StartProcessRequest struct {
	Target           Target
	Operation        OperationContext
	RequestID        string
	ProcessID        string
	Executable       string
	Arguments        []string
	WorkingDirectory string
	WorkspaceRoot    string
	// WorkspaceAccess is optional for backwards-compatible callers. Empty is
	// normalized to WorkspaceAccessWrite; read-only runs carry "read" and the
	// backend must enforce that mode at its sandbox boundary.
	WorkspaceAccess  string
	Platform         string
	Environment      map[string]string
	TTY              bool
	Timeout          time.Duration
	OutputLimitBytes int64
	// DeadlineNotification pre-allocates the identity used when a process
	// deadline becomes due. A backend may emit a provider-side due signal, but
	// it is never permission to send SignalProcess; Core Begin remains the only
	// dispatch authority for that second operation.
	DeadlineNotification *DeadlineNotification
}

func (request StartProcessRequest) EffectiveWorkspaceAccess() string {
	if request.WorkspaceAccess == WorkspaceAccessRead {
		return WorkspaceAccessRead
	}
	return WorkspaceAccessWrite
}

func (request StartProcessRequest) Validate() error {
	if err := validateOperationTarget(request.Target, request.Operation); err != nil {
		return err
	}
	if err := validateOpaqueID("process ID", request.ProcessID); err != nil {
		return err
	}
	if err := validateOpaqueID("process request ID", request.RequestID); err != nil {
		return err
	}
	if err := validateText("process executable", request.Executable, 1, MaxPathRunes); err != nil {
		return err
	}
	if len(request.Arguments) > MaxArguments {
		return fmt.Errorf("process arguments exceed %d entries", MaxArguments)
	}
	for index, argument := range request.Arguments {
		if err := validateText(fmt.Sprintf("process argument %d", index), argument, 0, MaxArgumentRunes); err != nil {
			return err
		}
	}
	if err := validateText("process working directory", request.WorkingDirectory, 1, MaxPathRunes); err != nil {
		return err
	}
	if err := validateText("process workspace root", request.WorkspaceRoot, 1, MaxPathRunes); err != nil {
		return err
	}
	if request.WorkspaceAccess != "" && request.WorkspaceAccess != WorkspaceAccessRead && request.WorkspaceAccess != WorkspaceAccessWrite {
		return errors.New("process workspace access must be read or write")
	}
	if err := validateOpaqueID("process platform", request.Platform); err != nil {
		return err
	}
	if len(request.Environment) > MaxEnvironmentEntries {
		return fmt.Errorf("process environment exceeds %d entries", MaxEnvironmentEntries)
	}
	for name, value := range request.Environment {
		if !environmentNamePattern.MatchString(name) {
			return fmt.Errorf("process environment contains invalid name %q", name)
		}
		if err := validateText("process environment value", value, 0, MaxEnvironmentRunes); err != nil {
			return err
		}
	}
	if request.Timeout <= 0 || request.Timeout > MaxOperationTimeout {
		return fmt.Errorf("process timeout must be positive and at most %s", MaxOperationTimeout)
	}
	if request.OutputLimitBytes < 1 || request.OutputLimitBytes > MaxOutputBytes {
		return fmt.Errorf("process output limit must be between 1 and %d bytes", MaxOutputBytes)
	}
	if request.DeadlineNotification != nil {
		if err := request.DeadlineNotification.Validate(request.Operation, request.Timeout); err != nil {
			return err
		}
	}
	return nil
}

type DeadlineNotification struct {
	After     time.Duration
	Operation OperationContext
	RequestID string
}

func (notification DeadlineNotification) Validate(start OperationContext, timeout time.Duration) error {
	if err := notification.Operation.Validate(); err != nil {
		return fmt.Errorf("deadline notification operation: %w", err)
	}
	if err := validateOpaqueID("deadline notification request ID", notification.RequestID); err != nil {
		return err
	}
	if notification.After <= 0 || notification.After != timeout {
		return errors.New("deadline notification delay must equal the positive process timeout")
	}
	if notification.Operation.WorkspaceID != start.WorkspaceID ||
		notification.Operation.SessionID != start.SessionID ||
		notification.Operation.RunID != start.RunID ||
		notification.Operation.RunAttemptID != start.RunAttemptID ||
		notification.Operation.RunAttemptGeneration != start.RunAttemptGeneration ||
		notification.Operation.ExecutionID != start.ExecutionID {
		return errors.New("deadline notification is outside the process execution identity")
	}
	if notification.Operation.OperationID == start.OperationID || notification.Operation.MutationKey == start.MutationKey {
		return errors.New("deadline notification operation and mutation identities must differ from process start")
	}
	return nil
}

type Signal string

const (
	SignalTerminate Signal = "terminate"
	SignalInterrupt Signal = "interrupt"
	SignalKill      Signal = "kill"
)

func (signal Signal) Validate() error {
	switch signal {
	case SignalTerminate, SignalInterrupt, SignalKill:
		return nil
	default:
		return fmt.Errorf("unsupported process signal %q", signal)
	}
}

type SignalProcessRequest struct {
	Target         Target
	Operation      OperationContext
	RequestID      string
	ProcessID      string
	ProviderHandle string
	Signal         Signal
	Reason         string
}

func (request SignalProcessRequest) Validate() error {
	if err := validateOperationTarget(request.Target, request.Operation); err != nil {
		return err
	}
	if err := validateOpaqueID("process ID", request.ProcessID); err != nil {
		return err
	}
	if err := validateOpaqueID("process signal request ID", request.RequestID); err != nil {
		return err
	}
	if request.ProviderHandle != "" {
		if err := validateText("provider process handle", request.ProviderHandle, 1, 1024); err != nil {
			return err
		}
	}
	if err := request.Signal.Validate(); err != nil {
		return err
	}
	return validateText("process signal reason", request.Reason, 1, 1024)
}

type ReadFileRequest struct {
	Target    Target
	Operation OperationContext
	RequestID string
	Path      string
	Offset    uint64
	Limit     uint64
}

func (request ReadFileRequest) Validate() error {
	if err := validateOperationTarget(request.Target, request.Operation); err != nil {
		return err
	}
	if err := validateText("read-file path", request.Path, 1, MaxPathRunes); err != nil {
		return err
	}
	if err := validateOpaqueID("read-file request ID", request.RequestID); err != nil {
		return err
	}
	if request.Limit < 1 || request.Limit > MaxReadFileBytes {
		return fmt.Errorf("read-file limit must be between 1 and %d bytes", MaxReadFileBytes)
	}
	if request.Offset > ^uint64(0)-request.Limit {
		return errors.New("read-file offset plus limit overflows uint64")
	}
	return nil
}

type Acknowledgement struct {
	ProviderOperationID string    `json:"providerOperationId,omitempty"`
	ProviderRequestID   string    `json:"providerRequestId,omitempty"`
	AcceptedAt          time.Time `json:"acceptedAt"`
}

func (acknowledgement Acknowledgement) Validate() error {
	if acknowledgement.AcceptedAt.IsZero() {
		return errors.New("backend acknowledgement accepted time is required")
	}
	if acknowledgement.ProviderOperationID != "" {
		if err := validateText("provider operation ID", acknowledgement.ProviderOperationID, 1, 1024); err != nil {
			return err
		}
	}
	if acknowledgement.ProviderRequestID != "" {
		if err := validateText("provider request ID", acknowledgement.ProviderRequestID, 1, 1024); err != nil {
			return err
		}
	}
	return nil
}

type EventKind string

const (
	EventStdout    EventKind = "stdout"
	EventStderr    EventKind = "stderr"
	EventFileBytes EventKind = "file_bytes"
)

func (kind EventKind) Validate() error {
	switch kind {
	case EventStdout, EventStderr, EventFileBytes:
		return nil
	default:
		return fmt.Errorf("unsupported backend event kind %q", kind)
	}
}

type Event struct {
	Sequence uint64    `json:"sequence"`
	Kind     EventKind `json:"kind"`
	Data     []byte    `json:"data"`
}

func (event Event) Validate() error {
	if event.Sequence < 1 {
		return errors.New("backend event sequence must be positive")
	}
	if err := event.Kind.Validate(); err != nil {
		return err
	}
	if len(event.Data) < 1 || len(event.Data) > MaxEventBytes {
		return fmt.Errorf("backend event data must be between 1 and %d bytes", MaxEventBytes)
	}
	return nil
}

type TerminalStatus string

const (
	TerminalSucceeded TerminalStatus = "succeeded"
	TerminalFailed    TerminalStatus = "failed"
	TerminalCancelled TerminalStatus = "cancelled"
	TerminalUnknown   TerminalStatus = "unknown"
)

func (status TerminalStatus) Validate() error {
	switch status {
	case TerminalSucceeded, TerminalFailed, TerminalCancelled, TerminalUnknown:
		return nil
	default:
		return fmt.Errorf("unsupported backend terminal status %q", status)
	}
}

type TerminalResult struct {
	Status         TerminalStatus `json:"status"`
	ExitCode       *int32         `json:"exitCode,omitempty"`
	ReasonCode     string         `json:"reasonCode,omitempty"`
	OutputComplete bool           `json:"outputComplete"`
	CompletedAt    time.Time      `json:"completedAt"`
}

func (result TerminalResult) Validate() error {
	if err := result.Status.Validate(); err != nil {
		return err
	}
	if result.CompletedAt.IsZero() {
		return errors.New("backend terminal completion time is required")
	}
	if result.ReasonCode != "" && !reasonCodePattern.MatchString(result.ReasonCode) {
		return fmt.Errorf("backend terminal reason code %q is invalid", result.ReasonCode)
	}
	return nil
}

func validateOperationTarget(target Target, operation OperationContext) error {
	if err := target.Validate(); err != nil {
		return err
	}
	return operation.Validate()
}

func validateOpaqueID(name, value string) error {
	if !opaqueIDPattern.MatchString(value) {
		return fmt.Errorf("%s must match %s", name, opaqueIDPattern)
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
