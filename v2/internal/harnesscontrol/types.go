// Package harnesscontrol defines the bounded, process-local-resumable control
// protocol between one harness-pool holder and one per-attempt worker.
package harnesscontrol

import (
	"encoding/json"
	"errors"
	"fmt"
)

const (
	CurrentProtocolVersion = "1.0"
	ResumeWindowMillis     = 30_000

	MessageTypeHello        = "hello"
	MessageTypeWelcome      = "welcome"
	MessageTypeEvent        = "event"
	MessageTypeCommand      = "command"
	MessageTypeAck          = "ack"
	MessageTypeSessionError = "session_error"

	EventKindThreadReady  = "thread_ready"
	EventKindTurnAccepted = "turn_accepted"
	EventKindTurnTerminal = "turn_terminal"

	CommandKindInterrupt = "interrupt"
)

type Role string

const (
	RolePool   Role = "pool"
	RoleWorker Role = "worker"
)

func (role Role) peer() Role {
	if role == RolePool {
		return RoleWorker
	}
	return RolePool
}

type ErrorCode string

const (
	ErrorMalformedFrame             ErrorCode = "malformed_frame"
	ErrorProtocolVersionUnsupported ErrorCode = "protocol_version_unsupported"
	ErrorAttemptMismatch            ErrorCode = "attempt_mismatch"
	ErrorStaleGeneration            ErrorCode = "stale_generation"
	ErrorSequenceConflict           ErrorCode = "sequence_conflict"
	ErrorSequenceGap                ErrorCode = "sequence_gap"
	ErrorAckOutOfRange              ErrorCode = "ack_out_of_range"
	ErrorAckRegression              ErrorCode = "ack_regression"
	ErrorResumeRejected             ErrorCode = "resume_rejected"
	ErrorResumeExpired              ErrorCode = "resume_expired"
	ErrorJournalFull                ErrorCode = "journal_full"
	ErrorBufferOverflow             ErrorCode = "buffer_overflow"
	ErrorSessionClosed              ErrorCode = "session_closed"
)

type ProtocolError struct {
	Code     ErrorCode
	Message  string
	Terminal bool
	LostFrom *uint64
	LostTo   *uint64
}

func (err *ProtocolError) Error() string {
	if err == nil {
		return "<nil>"
	}
	return fmt.Sprintf("harness control %s: %s", err.Code, err.Message)
}

func protocolError(code ErrorCode, terminal bool, format string, arguments ...any) error {
	return &ProtocolError{Code: code, Terminal: terminal, Message: fmt.Sprintf(format, arguments...)}
}

func gapProtocolError(code ErrorCode, terminal bool, from, to uint64, format string, arguments ...any) error {
	return &ProtocolError{
		Code: code, Terminal: terminal, Message: fmt.Sprintf(format, arguments...),
		LostFrom: cloneUint64(&from), LostTo: cloneUint64(&to),
	}
}

type Limits struct {
	MaxFrameBytes int
	MaxJSONValues int
	MaxJSONDepth  int
}

type Hello struct {
	Type                 string        `json:"type"`
	ProtocolVersions     []string      `json:"protocolVersions"`
	WorkerInstanceID     string        `json:"workerInstanceId"`
	WorkspaceID          string        `json:"workspaceId"`
	SessionID            string        `json:"sessionId"`
	RunID                string        `json:"runId"`
	RunAttemptID         string        `json:"runAttemptId"`
	RunAttemptGeneration int64         `json:"runAttemptGeneration"`
	HolderID             string        `json:"holderId"`
	ManifestDigest       string        `json:"manifestDigest"`
	Resume               *ResumeCursor `json:"resume,omitempty"`
}

// ResumeCursor is accepted only while PoolInstanceID still names the exact
// process holding the in-memory session journals.
type ResumeCursor struct {
	PoolInstanceID        string `json:"poolInstanceId"`
	ControlSessionID      string `json:"controlSessionId"`
	RunAttemptGeneration  int64  `json:"runAttemptGeneration"`
	WorkerSentThrough     uint64 `json:"workerSentThrough"`
	WorkerReceivedThrough uint64 `json:"workerReceivedThrough"`
}

type Welcome struct {
	Type                 string `json:"type"`
	ProtocolVersion      string `json:"protocolVersion"`
	PoolInstanceID       string `json:"poolInstanceId"`
	ControlSessionID     string `json:"controlSessionId"`
	RunAttemptGeneration int64  `json:"runAttemptGeneration"`
	ResumeStatus         string `json:"resumeStatus"`
	ResumeWindowMillis   int64  `json:"resumeWindowMs"`
	PoolSentThrough      uint64 `json:"poolSentThrough"`
	PoolReceivedThrough  uint64 `json:"poolReceivedThrough"`
}

// Frame is one sequenced event or command. Ack cumulatively acknowledges the
// peer's sequenced frames and never authorizes a core state transition.
type Frame struct {
	Type                 string          `json:"type"`
	ControlSessionID     string          `json:"controlSessionId"`
	SessionSeq           uint64          `json:"sessionSeq"`
	Ack                  uint64          `json:"ack"`
	RunAttemptGeneration int64           `json:"runAttemptGeneration"`
	Payload              json.RawMessage `json:"payload"`
}

type Ack struct {
	Type                 string `json:"type"`
	ControlSessionID     string `json:"controlSessionId"`
	RunAttemptGeneration int64  `json:"runAttemptGeneration"`
	Ack                  uint64 `json:"ack"`
}

type SessionError struct {
	Type     string    `json:"type"`
	Code     ErrorCode `json:"code"`
	Message  string    `json:"message"`
	Terminal bool      `json:"terminal"`
	LostFrom *uint64   `json:"lostFrom,omitempty"`
	LostTo   *uint64   `json:"lostTo,omitempty"`
}

type ThreadReadyEvent struct {
	Kind     string `json:"kind"`
	ThreadID string `json:"threadId"`
	Resumed  bool   `json:"resumed"`
}

type TurnAcceptedEvent struct {
	Kind     string `json:"kind"`
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
}

type TurnTerminalEvent struct {
	Kind         string `json:"kind"`
	ThreadID     string `json:"threadId"`
	TurnID       string `json:"turnId"`
	Status       string `json:"status"`
	ErrorCode    string `json:"errorCode,omitempty"`
	ErrorMessage string `json:"errorMessage,omitempty"`
}

type InterruptCommand struct {
	Kind        string `json:"kind"`
	Reason      string `json:"reason"`
	GraceMillis int64  `json:"graceMs"`
	Message     string `json:"message"`
}

type Event struct {
	Kind         string
	ThreadReady  *ThreadReadyEvent
	TurnAccepted *TurnAcceptedEvent
	TurnTerminal *TurnTerminalEvent
}

type Command struct {
	Kind      string
	Interrupt *InterruptCommand
}

type Message struct {
	Type         string
	Raw          json.RawMessage
	Hello        *Hello
	Welcome      *Welcome
	Frame        *Frame
	Ack          *Ack
	SessionError *SessionError
}

func SessionErrorFrom(err error) SessionError {
	var protocol *ProtocolError
	if errors.As(err, &protocol) {
		return SessionError{
			Type: MessageTypeSessionError, Code: protocol.Code, Message: protocol.Message,
			Terminal: protocol.Terminal, LostFrom: cloneUint64(protocol.LostFrom), LostTo: cloneUint64(protocol.LostTo),
		}
	}
	return SessionError{
		Type: MessageTypeSessionError, Code: ErrorSessionClosed,
		Message: "internal control session failure", Terminal: true,
	}
}

func cloneUint64(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
