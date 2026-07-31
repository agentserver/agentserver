package harnesscontrol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/braincatalog"
)

const (
	maxProtocolVersions  = 8
	maxProtocolTextBytes = 4096
	maxSafeJSONInteger   = uint64(1<<53 - 1)
	maxInterruptGraceMS  = int64(300_000)
)

var (
	uuidPattern    = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	digestPattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+$`)
)

func Decode(raw []byte, limits Limits) (Message, error) {
	if err := validateLimits(limits); err != nil {
		return Message{}, err
	}
	if len(raw) == 0 {
		return Message{}, protocolError(ErrorMalformedFrame, true, "message is empty")
	}
	if len(raw) > limits.MaxFrameBytes {
		return Message{}, protocolError(ErrorMalformedFrame, true, "message is %d bytes, limit is %d", len(raw), limits.MaxFrameBytes)
	}
	jsonLimits := braincatalog.DefaultLimits()
	jsonLimits.MaxJSONValues = limits.MaxJSONValues
	jsonLimits.MaxJSONDepth = limits.MaxJSONDepth
	if _, _, err := braincatalog.DecodeCanonicalJSON(raw, limits.MaxFrameBytes, jsonLimits); err != nil {
		return Message{}, protocolError(ErrorMalformedFrame, true, "%v", err)
	}
	var discriminator struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &discriminator); err != nil {
		return Message{}, protocolError(ErrorMalformedFrame, true, "decode message type: %v", err)
	}
	message := Message{Type: discriminator.Type, Raw: append(json.RawMessage(nil), raw...)}
	switch discriminator.Type {
	case MessageTypeHello:
		var value Hello
		if err := decodeRequiredObject(raw, &value,
			"type", "protocolVersions", "workerInstanceId", "workspaceId", "sessionId", "runId",
			"runAttemptId", "runAttemptGeneration", "holderId", "manifestDigest",
		); err != nil {
			return Message{}, malformed("decode hello: %v", err)
		}
		fields, _ := objectFields(raw)
		if resume, present := fields["resume"]; present {
			if value.Resume == nil || isJSONNull(resume) {
				return Message{}, malformed("decode hello: resume cannot be null")
			}
			if err := decodeRequiredObject(resume, value.Resume,
				"poolInstanceId", "controlSessionId", "runAttemptGeneration",
				"workerSentThrough", "workerReceivedThrough",
			); err != nil {
				return Message{}, malformed("decode hello resume: %v", err)
			}
		}
		if err := value.Validate(); err != nil {
			return Message{}, err
		}
		message.Hello = &value
	case MessageTypeWelcome:
		var value Welcome
		if err := decodeRequiredObject(raw, &value,
			"type", "protocolVersion", "poolInstanceId", "controlSessionId", "runAttemptGeneration",
			"resumeStatus", "resumeWindowMs", "poolSentThrough", "poolReceivedThrough",
		); err != nil {
			return Message{}, malformed("decode welcome: %v", err)
		}
		if err := value.Validate(); err != nil {
			return Message{}, err
		}
		message.Welcome = &value
	case MessageTypeEvent, MessageTypeCommand:
		var value Frame
		if err := decodeRequiredObject(raw, &value,
			"type", "controlSessionId", "sessionSeq", "ack", "runAttemptGeneration", "payload",
		); err != nil {
			return Message{}, malformed("decode sequenced frame: %v", err)
		}
		if err := value.validateStructure(limits); err != nil {
			return Message{}, err
		}
		message.Frame = &value
	case MessageTypeAck:
		var value Ack
		if err := decodeRequiredObject(raw, &value, "type", "controlSessionId", "runAttemptGeneration", "ack"); err != nil {
			return Message{}, malformed("decode ack: %v", err)
		}
		if err := value.Validate(); err != nil {
			return Message{}, err
		}
		message.Ack = &value
	case MessageTypeSessionError:
		var value SessionError
		if err := decodeRequiredObject(raw, &value, "type", "code", "message", "terminal"); err != nil {
			return Message{}, malformed("decode session error: %v", err)
		}
		fields, _ := objectFields(raw)
		for _, optional := range []string{"lostFrom", "lostTo"} {
			if field, present := fields[optional]; present && isJSONNull(field) {
				return Message{}, malformed("decode session error: %s cannot be null", optional)
			}
		}
		if err := value.Validate(); err != nil {
			return Message{}, err
		}
		message.SessionError = &value
	default:
		return Message{}, malformed("unknown message type %q", discriminator.Type)
	}
	return message, nil
}

func Encode(value any, limits Limits) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode harness control message: %w", err)
	}
	if _, err := Decode(raw, limits); err != nil {
		return nil, err
	}
	return raw, nil
}

func DecodeEventPayload(raw []byte, limits Limits) (Event, error) {
	if err := validateEmbeddedPayload(raw, limits); err != nil {
		return Event{}, err
	}
	var discriminator struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &discriminator); err != nil {
		return Event{}, malformed("decode event kind: %v", err)
	}
	event := Event{Kind: discriminator.Kind}
	switch discriminator.Kind {
	case EventKindThreadReady:
		var value ThreadReadyEvent
		if err := decodeRequiredObject(raw, &value, "kind", "threadId", "resumed"); err != nil {
			return Event{}, malformed("decode thread_ready: %v", err)
		}
		if err := value.Validate(); err != nil {
			return Event{}, err
		}
		event.ThreadReady = &value
	case EventKindTurnAccepted:
		var value TurnAcceptedEvent
		if err := decodeRequiredObject(raw, &value, "kind", "threadId", "turnId"); err != nil {
			return Event{}, malformed("decode turn_accepted: %v", err)
		}
		if err := value.Validate(); err != nil {
			return Event{}, err
		}
		event.TurnAccepted = &value
	case EventKindTurnTerminal:
		var value TurnTerminalEvent
		if err := decodeRequiredObject(raw, &value, "kind", "threadId", "turnId", "status"); err != nil {
			return Event{}, malformed("decode turn_terminal: %v", err)
		}
		fields, _ := objectFields(raw)
		for _, optional := range []string{"errorCode", "errorMessage"} {
			if field, present := fields[optional]; present && isJSONNull(field) {
				return Event{}, malformed("decode turn_terminal: %s cannot be null", optional)
			}
		}
		if err := value.Validate(); err != nil {
			return Event{}, err
		}
		event.TurnTerminal = &value
	case EventKindAppServerNotification:
		var value AppServerNotificationEvent
		if err := decodeRequiredObject(raw, &value, "kind", "method", "params"); err != nil {
			return Event{}, malformed("decode app_server_notification: %v", err)
		}
		if err := value.Validate(limits); err != nil {
			return Event{}, err
		}
		event.AppServerNotification = &value
	case EventKindExecutorMCPProgress:
		var value ExecutorMCPProgressEvent
		if err := decodeRequiredObject(raw, &value, "kind", "callId", "progress", "total"); err != nil {
			return Event{}, malformed("decode executor_mcp_progress: %v", err)
		}
		fields, _ := objectFields(raw)
		if field, present := fields["message"]; present && isJSONNull(field) {
			return Event{}, malformed("decode executor_mcp_progress: message cannot be null")
		}
		if err := value.Validate(); err != nil {
			return Event{}, err
		}
		event.ExecutorMCPProgress = &value
	default:
		return Event{}, protocolError(ErrorMalformedFrame, true, "unknown worker event kind %q", discriminator.Kind)
	}
	return event, nil
}

func DecodeCommandPayload(raw []byte, limits Limits) (Command, error) {
	if err := validateEmbeddedPayload(raw, limits); err != nil {
		return Command{}, err
	}
	var discriminator struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &discriminator); err != nil {
		return Command{}, malformed("decode command kind: %v", err)
	}
	command := Command{Kind: discriminator.Kind}
	switch discriminator.Kind {
	case CommandKindInterrupt:
		var value InterruptCommand
		if err := decodeRequiredObject(raw, &value, "kind", "reason", "graceMs", "message"); err != nil {
			return Command{}, malformed("decode interrupt: %v", err)
		}
		if err := value.Validate(); err != nil {
			return Command{}, err
		}
		command.Interrupt = &value
	default:
		return Command{}, protocolError(ErrorMalformedFrame, true, "unknown pool command kind %q", discriminator.Kind)
	}
	return command, nil
}

func (hello Hello) Validate() error {
	if hello.Type != MessageTypeHello {
		return malformed("hello type = %q", hello.Type)
	}
	if len(hello.ProtocolVersions) < 1 || len(hello.ProtocolVersions) > maxProtocolVersions {
		return malformed("protocolVersions must contain between 1 and %d entries", maxProtocolVersions)
	}
	seen := make(map[string]struct{}, len(hello.ProtocolVersions))
	for _, version := range hello.ProtocolVersions {
		if !versionPattern.MatchString(version) {
			return malformed("invalid protocol version %q", version)
		}
		if _, duplicate := seen[version]; duplicate {
			return malformed("duplicate protocol version %q", version)
		}
		seen[version] = struct{}{}
	}
	for field, value := range map[string]string{
		"workerInstanceId": hello.WorkerInstanceID, "workspaceId": hello.WorkspaceID,
		"sessionId": hello.SessionID, "runId": hello.RunID, "runAttemptId": hello.RunAttemptID,
	} {
		if err := validateUUID(field, value); err != nil {
			return err
		}
	}
	if err := validateGeneration("runAttemptGeneration", hello.RunAttemptGeneration); err != nil {
		return err
	}
	if err := validateText("holderId", hello.HolderID, 256); err != nil {
		return err
	}
	if err := validateDigest("manifestDigest", hello.ManifestDigest); err != nil {
		return err
	}
	if hello.Resume == nil {
		return nil
	}
	if err := hello.Resume.Validate(); err != nil {
		return err
	}
	if hello.Resume.RunAttemptGeneration != hello.RunAttemptGeneration {
		return protocolError(ErrorAttemptMismatch, true, "resume generation does not match hello generation")
	}
	return nil
}

func (cursor ResumeCursor) Validate() error {
	if err := validateUUID("poolInstanceId", cursor.PoolInstanceID); err != nil {
		return err
	}
	if err := validateUUID("controlSessionId", cursor.ControlSessionID); err != nil {
		return err
	}
	if err := validateGeneration("resume.runAttemptGeneration", cursor.RunAttemptGeneration); err != nil {
		return err
	}
	if err := validateCursor("workerSentThrough", cursor.WorkerSentThrough); err != nil {
		return err
	}
	return validateCursor("workerReceivedThrough", cursor.WorkerReceivedThrough)
}

func (welcome Welcome) Validate() error {
	if welcome.Type != MessageTypeWelcome {
		return malformed("welcome type = %q", welcome.Type)
	}
	if welcome.ProtocolVersion != CurrentProtocolVersion {
		return protocolError(ErrorProtocolVersionUnsupported, true, "welcome protocolVersion = %q", welcome.ProtocolVersion)
	}
	if err := validateUUID("poolInstanceId", welcome.PoolInstanceID); err != nil {
		return err
	}
	if err := validateUUID("controlSessionId", welcome.ControlSessionID); err != nil {
		return err
	}
	if err := validateGeneration("runAttemptGeneration", welcome.RunAttemptGeneration); err != nil {
		return err
	}
	if welcome.ResumeStatus != "fresh" && welcome.ResumeStatus != "resumed" {
		return malformed("resumeStatus must be fresh or resumed")
	}
	if welcome.ResumeWindowMillis != ResumeWindowMillis {
		return malformed("resumeWindowMs = %d, want %d", welcome.ResumeWindowMillis, ResumeWindowMillis)
	}
	if err := validateCursor("poolSentThrough", welcome.PoolSentThrough); err != nil {
		return err
	}
	if err := validateCursor("poolReceivedThrough", welcome.PoolReceivedThrough); err != nil {
		return err
	}
	if welcome.ResumeStatus == "fresh" && (welcome.PoolSentThrough != 0 || welcome.PoolReceivedThrough != 0) {
		return malformed("fresh welcome must have zero sequence cursors")
	}
	return nil
}

func (frame Frame) validateStructure(limits Limits) error {
	if frame.Type != MessageTypeEvent && frame.Type != MessageTypeCommand {
		return malformed("sequenced frame type = %q", frame.Type)
	}
	if err := validateUUID("controlSessionId", frame.ControlSessionID); err != nil {
		return err
	}
	if frame.SessionSeq < 1 || frame.SessionSeq > maxSafeJSONInteger {
		return malformed("sessionSeq must be a positive safe integer")
	}
	if err := validateCursor("ack", frame.Ack); err != nil {
		return err
	}
	if err := validateGeneration("runAttemptGeneration", frame.RunAttemptGeneration); err != nil {
		return err
	}
	if len(frame.Payload) == 0 || isJSONNull(frame.Payload) {
		return malformed("payload must be an object")
	}
	if frame.Type == MessageTypeEvent {
		_, err := DecodeEventPayload(frame.Payload, limits)
		return err
	}
	_, err := DecodeCommandPayload(frame.Payload, limits)
	return err
}

func (frame Frame) ValidateForReceiver(receiver Role, limits Limits) error {
	if receiver != RolePool && receiver != RoleWorker {
		return malformed("invalid control receiver role %q", receiver)
	}
	if err := frame.validateStructure(limits); err != nil {
		return err
	}
	if frame.Type == MessageTypeEvent && receiver != RolePool {
		return protocolError(ErrorAttemptMismatch, true, "worker event cannot be sent by pool")
	}
	if frame.Type == MessageTypeCommand && receiver != RoleWorker {
		return protocolError(ErrorAttemptMismatch, true, "pool command cannot be sent by worker")
	}
	return nil
}

func (ack Ack) Validate() error {
	if ack.Type != MessageTypeAck {
		return malformed("ack type = %q", ack.Type)
	}
	if err := validateUUID("controlSessionId", ack.ControlSessionID); err != nil {
		return err
	}
	if err := validateGeneration("runAttemptGeneration", ack.RunAttemptGeneration); err != nil {
		return err
	}
	return validateCursor("ack", ack.Ack)
}

func (sessionError SessionError) Validate() error {
	if sessionError.Type != MessageTypeSessionError {
		return malformed("session error type = %q", sessionError.Type)
	}
	if !knownErrorCode(sessionError.Code) {
		return malformed("unknown session error code %q", sessionError.Code)
	}
	if err := validateText("message", sessionError.Message, maxProtocolTextBytes); err != nil {
		return err
	}
	if (sessionError.LostFrom == nil) != (sessionError.LostTo == nil) {
		return malformed("lostFrom and lostTo must be present together")
	}
	requiresRange := sessionError.Code == ErrorSequenceGap || sessionError.Code == ErrorBufferOverflow
	if requiresRange != (sessionError.LostFrom != nil) {
		return malformed("sequence gap and buffer overflow errors require an exact lost range")
	}
	if sessionError.LostFrom != nil {
		if *sessionError.LostFrom < 1 || *sessionError.LostTo < *sessionError.LostFrom || *sessionError.LostTo > maxSafeJSONInteger {
			return malformed("lost range must be non-empty safe integers")
		}
	}
	return nil
}

func (event ThreadReadyEvent) Validate() error {
	if event.Kind != EventKindThreadReady {
		return malformed("thread_ready kind = %q", event.Kind)
	}
	return validateText("threadId", event.ThreadID, 256)
}

func (event TurnAcceptedEvent) Validate() error {
	if event.Kind != EventKindTurnAccepted {
		return malformed("turn_accepted kind = %q", event.Kind)
	}
	if err := validateText("threadId", event.ThreadID, 256); err != nil {
		return err
	}
	return validateText("turnId", event.TurnID, 256)
}

func (event TurnTerminalEvent) Validate() error {
	if event.Kind != EventKindTurnTerminal {
		return malformed("turn_terminal kind = %q", event.Kind)
	}
	if err := validateText("threadId", event.ThreadID, 256); err != nil {
		return err
	}
	if err := validateText("turnId", event.TurnID, 256); err != nil {
		return err
	}
	if event.Status != "completed" && event.Status != "interrupted" && event.Status != "failed" {
		return malformed("turn terminal status must be completed, interrupted, or failed")
	}
	if event.Status == "completed" {
		if event.ErrorCode != "" || event.ErrorMessage != "" {
			return malformed("completed turn terminal cannot contain an error")
		}
		return nil
	}
	if err := validateText("errorCode", event.ErrorCode, 128); err != nil {
		return err
	}
	return validateText("errorMessage", event.ErrorMessage, maxProtocolTextBytes)
}

func (event AppServerNotificationEvent) Validate(limits Limits) error {
	if event.Kind != EventKindAppServerNotification {
		return malformed("app_server_notification kind = %q", event.Kind)
	}
	if err := validateText("method", event.Method, 256); err != nil {
		return err
	}
	if strings.ContainsAny(event.Method, "\r\n\t ") {
		return malformed("app-server notification method must not contain whitespace")
	}
	if err := validateEmbeddedPayload(event.Params, limits); err != nil {
		return malformed("app-server notification params: %v", err)
	}
	return nil
}

func (event ExecutorMCPProgressEvent) Validate() error {
	if event.Kind != EventKindExecutorMCPProgress {
		return malformed("executor_mcp_progress kind = %q", event.Kind)
	}
	if err := validateText("callId", event.CallID, 256); err != nil {
		return err
	}
	if strings.ContainsAny(event.CallID, "\r\n\t ") {
		return malformed("callId must not contain whitespace")
	}
	if math.IsNaN(event.Progress) || math.IsInf(event.Progress, 0) || event.Progress < 0 ||
		math.IsNaN(event.Total) || math.IsInf(event.Total, 0) || event.Total < 0 ||
		event.Total > 0 && event.Progress > event.Total {
		return malformed("executor MCP progress and total must be finite non-negative values with progress <= total when total is positive")
	}
	if event.Message != "" {
		return validateText("message", event.Message, maxProtocolTextBytes)
	}
	return nil
}

func (command InterruptCommand) Validate() error {
	if command.Kind != CommandKindInterrupt {
		return malformed("interrupt kind = %q", command.Kind)
	}
	if !slices.Contains([]string{"lease_lost", "cancelled", "fenced", "shutdown", "protocol_error"}, command.Reason) {
		return malformed("interrupt reason %q is not negotiated", command.Reason)
	}
	if command.GraceMillis < 1 || command.GraceMillis > maxInterruptGraceMS {
		return malformed("interrupt graceMs must be between 1 and %d", maxInterruptGraceMS)
	}
	return validateText("message", command.Message, maxProtocolTextBytes)
}

func validateEmbeddedPayload(raw []byte, limits Limits) error {
	if len(raw) == 0 || len(raw) > limits.MaxFrameBytes {
		return malformed("embedded payload size is invalid")
	}
	jsonLimits := braincatalog.DefaultLimits()
	jsonLimits.MaxJSONValues = limits.MaxJSONValues
	jsonLimits.MaxJSONDepth = limits.MaxJSONDepth
	value, _, err := braincatalog.DecodeCanonicalJSON(raw, limits.MaxFrameBytes, jsonLimits)
	if err != nil {
		return malformed("validate embedded payload: %v", err)
	}
	if _, ok := value.(map[string]any); !ok {
		return malformed("embedded payload must be an object")
	}
	return nil
}

func decodeRequiredObject(raw []byte, destination any, required ...string) error {
	fields, err := objectFields(raw)
	if err != nil {
		return err
	}
	for _, field := range required {
		value, present := fields[field]
		if !present {
			return fmt.Errorf("required field %q is missing", field)
		}
		if isJSONNull(value) {
			return fmt.Errorf("required field %q cannot be null", field)
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON contains more than one value")
		}
		return fmt.Errorf("decode trailing JSON: %w", err)
	}
	return nil
}

func objectFields(raw []byte) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	if fields == nil {
		return nil, errors.New("value must be an object")
	}
	return fields, nil
}

func isJSONNull(raw []byte) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }

func validateLimits(limits Limits) error {
	if limits.MaxFrameBytes < 1 || limits.MaxJSONValues < 1 || limits.MaxJSONDepth < 1 {
		return errors.New("harness control wire limits must be positive")
	}
	return nil
}

func validateUUID(field, value string) error {
	if !uuidPattern.MatchString(value) || value == "00000000-0000-0000-0000-000000000000" {
		return malformed("%s must be a non-zero lowercase canonical UUID", field)
	}
	return nil
}

func validateDigest(field, value string) error {
	if !digestPattern.MatchString(value) {
		return malformed("%s must be lowercase SHA-256 hex", field)
	}
	return nil
}

func validateText(field, value string, maximum int) error {
	if value == "" || len(value) > maximum || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return malformed("%s must contain between 1 and %d valid UTF-8 bytes without NUL", field, maximum)
	}
	return nil
}

func validateGeneration(field string, value int64) error {
	if value < 1 || uint64(value) > maxSafeJSONInteger {
		return malformed("%s must be a positive safe integer", field)
	}
	return nil
}

func validateCursor(field string, value uint64) error {
	if value > maxSafeJSONInteger {
		return malformed("%s must be a non-negative safe integer", field)
	}
	return nil
}

func knownErrorCode(code ErrorCode) bool {
	return slices.Contains([]ErrorCode{
		ErrorMalformedFrame, ErrorProtocolVersionUnsupported, ErrorAttemptMismatch, ErrorStaleGeneration,
		ErrorSequenceConflict, ErrorSequenceGap, ErrorAckOutOfRange, ErrorAckRegression,
		ErrorResumeRejected, ErrorResumeExpired, ErrorJournalFull, ErrorBufferOverflow, ErrorSessionClosed,
	}, code)
}

func malformed(format string, arguments ...any) error {
	return protocolError(ErrorMalformedFrame, true, format, arguments...)
}
