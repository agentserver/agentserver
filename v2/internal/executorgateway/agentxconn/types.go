// Package agentxconn implements the bounded, in-memory Phase 1 reference
// protocol between executor-gateway and agentx. It does not claim durable or
// cross-gateway-process resume.
package agentxconn

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/agentserver/agentserver/v2/internal/codexwire"
)

const (
	CurrentProtocolVersion = "2.0"
	ResumeWindowMillis     = 30_000

	MessageTypeHello        = "hello"
	MessageTypeWelcome      = "welcome"
	MessageTypeLifecycle    = "lifecycle"
	MessageTypeRPC          = "rpc"
	MessageTypeAck          = "ack"
	MessageTypeSessionError = "session_error"
)

type Role string

const (
	RoleGateway Role = "gateway"
	RoleAgentx  Role = "agentx"
)

func (r Role) peer() Role {
	if r == RoleGateway {
		return RoleAgentx
	}
	return RoleGateway
}

type ErrorCode string

const (
	ErrorMalformedFrame             ErrorCode = "malformed_frame"
	ErrorProtocolVersionUnsupported ErrorCode = "protocol_version_unsupported"
	ErrorMethodNotNegotiated        ErrorCode = "method_not_negotiated"
	ErrorSessionMismatch            ErrorCode = "session_mismatch"
	ErrorStaleGeneration            ErrorCode = "stale_generation"
	ErrorAckOutOfRange              ErrorCode = "ack_out_of_range"
	ErrorAckRegression              ErrorCode = "ack_regression"
	ErrorSequenceConflict           ErrorCode = "sequence_conflict"
	ErrorResumeGap                  ErrorCode = "resume_gap"
	ErrorOutputGap                  ErrorCode = "output_gap"
	ErrorBufferOverflow             ErrorCode = "buffer_overflow"
	ErrorResumeRejected             ErrorCode = "resume_rejected"
	ErrorResumeExpired              ErrorCode = "resume_expired"
	ErrorJournalFull                ErrorCode = "journal_full"
	ErrorMutationConflict           ErrorCode = "mutation_conflict"
	ErrorSessionClosed              ErrorCode = "session_closed"
	ErrorAmbiguous                  ErrorCode = "ambiguous"
)

// ProtocolError carries a stable wire error code. Terminal means the current
// exec session cannot continue; callers still decide how to translate that
// fact into operation unknown/terminal state through core commands.
type ProtocolError struct {
	Code     ErrorCode
	Message  string
	Terminal bool
	LostFrom *uint64
	LostTo   *uint64
}

func (e *ProtocolError) Error() string {
	return fmt.Sprintf("agentx protocol %s: %s", e.Code, e.Message)
}

func protocolError(code ErrorCode, terminal bool, format string, arguments ...any) error {
	return &ProtocolError{Code: code, Message: fmt.Sprintf(format, arguments...), Terminal: terminal}
}

func gapProtocolError(code ErrorCode, terminal bool, lostFrom, lostTo uint64, format string, arguments ...any) error {
	return &ProtocolError{
		Code:     code,
		Message:  fmt.Sprintf(format, arguments...),
		Terminal: terminal,
		LostFrom: &lostFrom,
		LostTo:   &lostTo,
	}
}

// SessionErrorFrom converts an implementation error into the bounded public
// control frame. Unknown internal errors are not reflected to the peer.
func SessionErrorFrom(err error) SessionError {
	var protocol *ProtocolError
	if errors.As(err, &protocol) {
		return SessionError{
			Type:     MessageTypeSessionError,
			Code:     protocol.Code,
			Message:  protocol.Message,
			Terminal: protocol.Terminal,
			LostFrom: cloneUint64(protocol.LostFrom),
			LostTo:   cloneUint64(protocol.LostTo),
		}
	}
	return SessionError{
		Type:     MessageTypeSessionError,
		Code:     ErrorSessionClosed,
		Message:  "internal session failure",
		Terminal: true,
	}
}

func cloneUint64(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

// Limits apply to the complete outer WSS message, including routing metadata
// and the embedded RPC value.
type Limits struct {
	MaxFrameBytes int
	MaxJSONValues int
	MaxJSONDepth  int
}

type Hello struct {
	Type                     string             `json:"type"`
	ConnectionID             string             `json:"connectionId"`
	ProtocolVersions         []string           `json:"protocolVersions"`
	AgentxVersion            string             `json:"agentxVersion"`
	RuntimeManifestSHA256    string             `json:"runtimeManifestSha256"`
	ExecProtocolSourceSHA256 string             `json:"execProtocolSourceSha256"`
	Environments             []HelloEnvironment `json:"environments"`
	Resume                   *ResumeCursor      `json:"resume,omitempty"`
}

type HelloEnvironment struct {
	EnvID               string          `json:"envId"`
	Platform            string          `json:"platform"`
	CodexRelease        string          `json:"codexRelease"`
	CodexCommit         string          `json:"codexCommit"`
	CodexSHA256         string          `json:"codexSha256"`
	OuterProfileVersion string          `json:"outerProfileVersion"`
	ProcessMethods      []string        `json:"processMethods"`
	ActiveProcesses     []ActiveProcess `json:"activeProcesses"`
	InsecureDev         bool            `json:"insecureDev"`
}

type ActiveProcess struct {
	ProcessID           string `json:"processId"`
	LocalExecInstanceID string `json:"localExecInstanceId"`
}

// ResumeCursor is agentx's view of a previously negotiated session. The
// gateway accepts it only when GatewayInstanceID still names this exact
// process and its in-memory journals cover the requested ranges.
type ResumeCursor struct {
	GatewayInstanceID     string `json:"gatewayInstanceId"`
	SessionID             string `json:"sessionId"`
	Generation            int64  `json:"generation"`
	AgentxSentThrough     uint64 `json:"agentxSentThrough"`
	AgentxReceivedThrough uint64 `json:"agentxReceivedThrough"`
}

type Welcome struct {
	Type                   string `json:"type"`
	ProtocolVersion        string `json:"protocolVersion"`
	GatewayInstanceID      string `json:"gatewayInstanceId"`
	SessionID              string `json:"sessionId"`
	Generation             int64  `json:"generation"`
	ResumeStatus           string `json:"resumeStatus"`
	ResumeWindowMillis     int64  `json:"resumeWindowMs"`
	GatewaySentThrough     uint64 `json:"gatewaySentThrough"`
	GatewayReceivedThrough uint64 `json:"gatewayReceivedThrough"`
}

// Frame is one sequenced lifecycle or deterministic inner-RPC value. Ack is a
// cumulative piggyback acknowledgement for the peer's sequenced frames. A
// replay must preserve the complete Frame, including its original Ack.
type Frame struct {
	Type       string          `json:"type"`
	SessionID  string          `json:"sessionId"`
	SessionSeq uint64          `json:"sessionSeq"`
	Ack        uint64          `json:"ack"`
	Generation int64           `json:"generation"`
	Context    *RoutingContext `json:"context,omitempty"`
	RPC        json.RawMessage `json:"rpc"`
}

type RoutingContext struct {
	WorkspaceID          string `json:"workspaceId"`
	RunID                string `json:"runId"`
	RunAttemptID         string `json:"runAttemptId"`
	RunAttemptGeneration int64  `json:"runAttemptGeneration"`
	ExecutionID          string `json:"executionId"`
	OperationID          string `json:"operationId"`
	EnvID                string `json:"envId"`
	MutationKey          string `json:"mutationKey"`
}

// Ack is an unsequenced control frame. It exists so an otherwise idle peer can
// release the sender's journal without creating an infinite ack-of-ack chain.
// It never carries an RPC or authorizes an operation transition.
type Ack struct {
	Type       string `json:"type"`
	SessionID  string `json:"sessionId"`
	Generation int64  `json:"generation"`
	Ack        uint64 `json:"ack"`
}

type SessionError struct {
	Type     string    `json:"type"`
	Code     ErrorCode `json:"code"`
	Message  string    `json:"message"`
	Terminal bool      `json:"terminal"`
	LostFrom *uint64   `json:"lostFrom,omitempty"`
	LostTo   *uint64   `json:"lostTo,omitempty"`
}

type Message struct {
	Type         string
	Hello        *Hello
	Welcome      *Welcome
	Frame        *Frame
	Ack          *Ack
	SessionError *SessionError
	Raw          json.RawMessage
}

type standardRPCKind uint8

const (
	standardRPCRequest standardRPCKind = iota + 1
	standardRPCNotification
	standardRPCResponse
	standardRPCError
)

type standardRPC struct {
	Kind   standardRPCKind
	ID     json.RawMessage
	Method string
	Params json.RawMessage
	Result json.RawMessage
	Error  *codexwire.RPCError
}
