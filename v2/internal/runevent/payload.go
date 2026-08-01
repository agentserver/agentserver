package runevent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	KindAssistantMessageStarted   = "assistant.message.started"
	KindAssistantMessageDelta     = "assistant.message.delta"
	KindAssistantMessageCompleted = "assistant.message.completed"
	KindAssistantReasoningStarted = "assistant.reasoning.started"
	KindAssistantReasoningDelta   = "assistant.reasoning.delta"
	KindAssistantReasoningDone    = "assistant.reasoning.completed"
	KindToolCallStarted           = "tool.call.started"
	KindToolCallArguments         = "tool.call.arguments"
	KindToolCallProgress          = "tool.call.progress"
	KindToolCallCompleted         = "tool.call.completed"
	KindToolCallResult            = "tool.call.result"
	KindRunCompleted              = "run.completed"
	KindRunFailed                 = "run.failed"
	KindRunInterrupted            = "run.interrupted"
	KindRunCancelling             = "run.cancelling"
	KindRunCancelled              = "run.cancelled"
	KindApprovalRequested         = "approval.requested"
	KindApprovalApproved          = "approval.approved"
	KindApprovalDenied            = "approval.denied"
	KindApprovalExpired           = "approval.expired"
	KindApprovalCancelled         = "approval.cancelled"
	KindApprovalConsumed          = "approval.consumed"
)

var knownKinds = map[string]struct{}{
	KindAssistantMessageStarted:   {},
	KindAssistantMessageDelta:     {},
	KindAssistantMessageCompleted: {},
	KindAssistantReasoningStarted: {},
	KindAssistantReasoningDelta:   {},
	KindAssistantReasoningDone:    {},
	KindToolCallStarted:           {},
	KindToolCallArguments:         {},
	KindToolCallProgress:          {},
	KindToolCallCompleted:         {},
	KindToolCallResult:            {},
	KindRunCompleted:              {},
	KindRunFailed:                 {},
	KindRunInterrupted:            {},
	KindRunCancelling:             {},
	KindRunCancelled:              {},
	KindApprovalRequested:         {},
	KindApprovalApproved:          {},
	KindApprovalDenied:            {},
	KindApprovalExpired:           {},
	KindApprovalCancelled:         {},
	KindApprovalConsumed:          {},
}

// IsKnownKind reports whether browser-gateway has a closed-world semantic
// decoder for kind. Unknown future kinds remain valid canonical ledger facts
// and can be skipped by older projectors.
func IsKnownKind(kind string) bool {
	_, ok := knownKinds[kind]
	return ok
}

type MessageStartedPayload struct {
	MessageID string `json:"messageId"`
	Role      string `json:"role"`
}

type MessageDeltaPayload struct {
	MessageID string `json:"messageId"`
	Delta     string `json:"delta"`
}

type MessageCompletedPayload struct {
	MessageID string `json:"messageId"`
}

type ToolCallStartedPayload struct {
	ToolCallID      string `json:"toolCallId"`
	ToolCallName    string `json:"toolCallName"`
	ParentMessageID string `json:"parentMessageId,omitempty"`
}

type ToolCallArgumentsPayload struct {
	ToolCallID string `json:"toolCallId"`
	Delta      string `json:"delta"`
}

type ToolCallProgressPayload struct {
	ToolCallID string  `json:"toolCallId"`
	Progress   float64 `json:"progress"`
	Total      float64 `json:"total"`
	Message    string  `json:"message,omitempty"`
}

type ToolCallCompletedPayload struct {
	ToolCallID string `json:"toolCallId"`
}

type ToolCallResultPayload struct {
	MessageID    string            `json:"messageId"`
	ToolCallID   string            `json:"toolCallId"`
	Content      string            `json:"content"`
	Presentation *ToolPresentation `json:"presentation,omitempty"`
}

type ToolPresentation struct {
	Kind       string                  `json:"kind"`
	Command    *CommandPresentation    `json:"command,omitempty"`
	FileChange *FileChangePresentation `json:"fileChange,omitempty"`
}

type CommandPresentation struct {
	Command string `json:"command"`
	Output  string `json:"output"`
	Status  string `json:"status"`
}

type FileChangePresentation struct {
	Files []PresentedFileChange `json:"files"`
}

type PresentedFileChange struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
	Diff string `json:"diff"`
}

type RunTerminalPayload struct {
	Code    string          `json:"code,omitempty"`
	Message string          `json:"message,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
}

type ApprovalPayload struct {
	RunID                string    `json:"runId"`
	RunAttemptID         string    `json:"runAttemptId"`
	RunAttemptGeneration int64     `json:"runAttemptGeneration"`
	ExecutionID          string    `json:"executionId"`
	ApprovalID           string    `json:"approvalId"`
	ToolName             string    `json:"toolName"`
	Status               string    `json:"status"`
	Decision             string    `json:"decision,omitempty"`
	Nonce                string    `json:"nonce"`
	ContextSHA256        string    `json:"contextSha256"`
	ExpiresAt            time.Time `json:"expiresAt"`
	ApproverID           string    `json:"approverId,omitempty"`
	Version              int64     `json:"version"`
}

// DecodeSemanticPayload decodes the payload for a known schema-v1 event. It
// rejects object-backed known events until an authorized object materializer
// is added to the browser-gateway backend, and fails closed on a schema version
// the projector does not understand.
func DecodeSemanticPayload(event Event) (any, error) {
	if !IsKnownKind(event.Kind) {
		return nil, fmt.Errorf("canonical run event kind %q is not known", event.Kind)
	}
	if event.SchemaVersion != CurrentSchemaVersion {
		return nil, fmt.Errorf("canonical run event kind %q schema version %d is unsupported", event.Kind, event.SchemaVersion)
	}
	if event.Object != nil {
		return nil, fmt.Errorf("canonical run event kind %q requires an inline schema-v1 payload", event.Kind)
	}

	switch event.Kind {
	case KindAssistantMessageStarted, KindAssistantReasoningStarted:
		payload, err := decodePayload[MessageStartedPayload](event.Payload)
		if err == nil {
			err = payload.validate()
		}
		return payload, wrapPayloadError(event.Kind, err)
	case KindAssistantMessageDelta, KindAssistantReasoningDelta:
		payload, err := decodePayload[MessageDeltaPayload](event.Payload)
		if err == nil {
			err = payload.validate()
		}
		return payload, wrapPayloadError(event.Kind, err)
	case KindAssistantMessageCompleted, KindAssistantReasoningDone:
		payload, err := decodePayload[MessageCompletedPayload](event.Payload)
		if err == nil {
			err = validateIdentifier("messageId", payload.MessageID)
		}
		return payload, wrapPayloadError(event.Kind, err)
	case KindToolCallStarted:
		payload, err := decodePayload[ToolCallStartedPayload](event.Payload)
		if err == nil {
			err = payload.validate()
		}
		return payload, wrapPayloadError(event.Kind, err)
	case KindToolCallArguments:
		payload, err := decodePayload[ToolCallArgumentsPayload](event.Payload)
		if err == nil {
			err = payload.validate()
		}
		return payload, wrapPayloadError(event.Kind, err)
	case KindToolCallProgress:
		payload, err := decodePayload[ToolCallProgressPayload](event.Payload)
		if err == nil {
			err = payload.validate()
		}
		return payload, wrapPayloadError(event.Kind, err)
	case KindToolCallCompleted:
		payload, err := decodePayload[ToolCallCompletedPayload](event.Payload)
		if err == nil {
			err = validateIdentifier("toolCallId", payload.ToolCallID)
		}
		return payload, wrapPayloadError(event.Kind, err)
	case KindToolCallResult:
		payload, err := decodePayload[ToolCallResultPayload](event.Payload)
		if err == nil {
			err = payload.validate()
		}
		return payload, wrapPayloadError(event.Kind, err)
	case KindRunCompleted, KindRunFailed, KindRunInterrupted, KindRunCancelling, KindRunCancelled:
		payload, err := decodePayload[RunTerminalPayload](event.Payload)
		if err == nil {
			err = payload.validate(event.Kind)
		}
		return payload, wrapPayloadError(event.Kind, err)
	case KindApprovalRequested, KindApprovalApproved, KindApprovalDenied, KindApprovalExpired, KindApprovalCancelled, KindApprovalConsumed:
		payload, err := decodePayload[ApprovalPayload](event.Payload)
		if err == nil {
			err = payload.validate(event)
		}
		return payload, wrapPayloadError(event.Kind, err)
	default:
		return nil, fmt.Errorf("canonical run event kind %q has no semantic decoder", event.Kind)
	}
}

func decodePayload[T any](raw json.RawMessage) (T, error) {
	var payload T
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return payload, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return payload, errors.New("unexpected trailing JSON value")
		}
		return payload, err
	}
	return payload, nil
}

func wrapPayloadError(kind string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("decode %s schema-v1 payload: %w", kind, err)
}

func (payload MessageStartedPayload) validate() error {
	if err := validateIdentifier("messageId", payload.MessageID); err != nil {
		return err
	}
	if payload.Role != "assistant" {
		return errors.New("role must be assistant")
	}
	return nil
}

func (payload MessageDeltaPayload) validate() error {
	if err := validateIdentifier("messageId", payload.MessageID); err != nil {
		return err
	}
	return validateText("delta", payload.Delta, 1, MaxInlinePayloadBytes)
}

func (payload ToolCallStartedPayload) validate() error {
	if err := validateIdentifier("toolCallId", payload.ToolCallID); err != nil {
		return err
	}
	if err := validateText("toolCallName", payload.ToolCallName, 1, 256); err != nil {
		return err
	}
	if strings.ContainsAny(payload.ToolCallName, "\x00\r\n") {
		return errors.New("toolCallName must not contain NUL or line breaks")
	}
	if payload.ParentMessageID != "" {
		return validateIdentifier("parentMessageId", payload.ParentMessageID)
	}
	return nil
}

func (payload ToolCallArgumentsPayload) validate() error {
	if err := validateIdentifier("toolCallId", payload.ToolCallID); err != nil {
		return err
	}
	return validateText("delta", payload.Delta, 1, MaxInlinePayloadBytes)
}

func (payload ToolCallProgressPayload) validate() error {
	if err := validateIdentifier("toolCallId", payload.ToolCallID); err != nil {
		return err
	}
	if math.IsNaN(payload.Progress) || math.IsInf(payload.Progress, 0) || payload.Progress < 0 ||
		math.IsNaN(payload.Total) || math.IsInf(payload.Total, 0) || payload.Total < 0 ||
		payload.Total > 0 && payload.Progress > payload.Total {
		return errors.New("progress and total must be finite non-negative values with progress <= total when total is positive")
	}
	return validateText("message", payload.Message, 0, 4096)
}

func (payload ToolCallResultPayload) validate() error {
	if err := validateIdentifier("messageId", payload.MessageID); err != nil {
		return err
	}
	if err := validateIdentifier("toolCallId", payload.ToolCallID); err != nil {
		return err
	}
	if err := validateText("content", payload.Content, 1, MaxInlinePayloadBytes); err != nil {
		return err
	}
	if payload.Presentation == nil {
		return nil
	}
	return payload.Presentation.validate()
}

func (presentation ToolPresentation) validate() error {
	switch presentation.Kind {
	case "command":
		if presentation.Command == nil || presentation.FileChange != nil {
			return errors.New("command presentation must contain only command")
		}
		if err := validateText("presentation.command.command", presentation.Command.Command, 1, 16*1024); err != nil {
			return err
		}
		if err := validateText("presentation.command.output", presentation.Command.Output, 0, MaxInlinePayloadBytes); err != nil {
			return err
		}
		return validateText("presentation.command.status", presentation.Command.Status, 1, 256)
	case "file_change":
		if presentation.FileChange == nil || presentation.Command != nil {
			return errors.New("file_change presentation must contain only fileChange")
		}
		if len(presentation.FileChange.Files) == 0 || len(presentation.FileChange.Files) > 128 {
			return errors.New("fileChange.files must contain between 1 and 128 entries")
		}
		for index, file := range presentation.FileChange.Files {
			if err := file.validate(); err != nil {
				return fmt.Errorf("fileChange.files[%d]: %w", index, err)
			}
		}
		return nil
	default:
		return errors.New("presentation.kind must be command or file_change")
	}
}

func (file PresentedFileChange) validate() error {
	if err := validateText("path", file.Path, 1, 4096); err != nil {
		return err
	}
	if strings.ContainsAny(file.Path, "\x00\r\n") {
		return errors.New("path must not contain NUL or line breaks")
	}
	switch file.Kind {
	case "add", "delete", "update":
	default:
		return errors.New("kind must be add, delete, or update")
	}
	return validateText("diff", file.Diff, 0, MaxInlinePayloadBytes)
}

func (payload RunTerminalPayload) validate(kind string) error {
	if err := validateText("code", payload.Code, 0, 128); err != nil {
		return err
	}
	if strings.ContainsAny(payload.Code, "\x00\r\n") {
		return errors.New("code must not contain NUL or line breaks")
	}
	if err := validateText("message", payload.Message, 0, 4096); err != nil {
		return err
	}
	if kind != KindRunCompleted && payload.Message == "" {
		return errors.New("message is required for non-completed terminal events")
	}
	return nil
}

func (payload ApprovalPayload) validate(event Event) error {
	for field, value := range map[string]string{
		"runId": payload.RunID, "runAttemptId": payload.RunAttemptID,
		"executionId": payload.ExecutionID, "approvalId": payload.ApprovalID, "nonce": payload.Nonce,
	} {
		if !uuidPattern.MatchString(value) || value == "00000000-0000-0000-0000-000000000000" {
			return fmt.Errorf("%s must be a non-zero canonical lowercase UUID", field)
		}
	}
	if payload.RunID != event.RunID || event.RunAttemptID == nil || event.RunAttemptGeneration == nil ||
		payload.RunAttemptID != *event.RunAttemptID || payload.RunAttemptGeneration != *event.RunAttemptGeneration {
		return errors.New("approval payload scope must match the attempt-scoped event envelope")
	}
	if payload.RunAttemptGeneration < 1 || payload.RunAttemptGeneration > maxSafeJSONInteger {
		return fmt.Errorf("runAttemptGeneration must be between 1 and %d", maxSafeJSONInteger)
	}
	if payload.Version < 1 || payload.Version > maxSafeJSONInteger {
		return fmt.Errorf("version must be between 1 and %d", maxSafeJSONInteger)
	}
	if err := validateText("toolName", payload.ToolName, 1, 128); err != nil {
		return err
	}
	if strings.ContainsAny(payload.ToolName, "\x00\r\n") {
		return errors.New("toolName must not contain NUL or line breaks")
	}
	if !digestPattern.MatchString(payload.ContextSHA256) {
		return errors.New("contextSha256 must be lowercase 64-character SHA-256 hex")
	}
	if payload.ExpiresAt.IsZero() {
		return errors.New("expiresAt is required")
	}
	wantStatus := strings.TrimPrefix(event.Kind, "approval.")
	if event.Kind == KindApprovalRequested {
		wantStatus = "pending"
	}
	if payload.Status != wantStatus {
		return fmt.Errorf("approval status %q does not match event kind %q", payload.Status, event.Kind)
	}
	switch payload.Status {
	case "pending":
		if payload.Decision != "" || payload.ApproverID != "" {
			return errors.New("pending approval must not contain a decision or approver")
		}
	case "approved", "consumed":
		if payload.Decision != "approve" || !validApprovalActor(payload.ApproverID) {
			return errors.New("approved or consumed approval must contain approve and a canonical approverId")
		}
	case "denied":
		if payload.Decision != "deny" || !validApprovalActor(payload.ApproverID) {
			return errors.New("denied approval must contain deny and a canonical approverId")
		}
	case "expired", "cancelled":
		if (payload.Decision == "") != (payload.ApproverID == "") {
			return errors.New("terminal approval decision and approverId must be present together")
		}
		if payload.Decision != "" && (payload.Decision != "approve" || !validApprovalActor(payload.ApproverID)) {
			return errors.New("expired or cancelled approval can preserve only a prior approve decision")
		}
	default:
		return errors.New("approval status is not supported")
	}
	return nil
}

func validApprovalActor(value string) bool {
	return uuidPattern.MatchString(value) && value != "00000000-0000-0000-0000-000000000000"
}

func validateIdentifier(field, value string) error {
	if err := validateText(field, value, 1, 256); err != nil {
		return err
	}
	if strings.ContainsAny(value, "\x00\r\n\t ") {
		return fmt.Errorf("%s must not contain whitespace or control separators", field)
	}
	return nil
}

func validateText(field, value string, minimum, maximum int) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s must be valid UTF-8", field)
	}
	if len(value) < minimum || len(value) > maximum {
		return fmt.Errorf("%s must contain between %d and %d bytes", field, minimum, maximum)
	}
	return nil
}
