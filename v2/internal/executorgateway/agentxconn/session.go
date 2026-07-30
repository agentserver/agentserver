package agentxconn

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"
)

type SessionState string

const (
	SessionActive       SessionState = "active"
	SessionDisconnected SessionState = "disconnected"
	SessionFenced       SessionState = "fenced"
	SessionClosed       SessionState = "closed"
)

type SessionConfig struct {
	Role                    Role
	GatewayInstanceID       string
	ExecutorID              string
	SessionID               string
	Generation              int64
	WireLimits              Limits
	MaxUnackedFrames        int
	MaxJournalBytes         int
	MaxReceiveHistoryFrames int
	ResumeWindow            time.Duration
}

type Payload struct {
	Type    string
	Context *RoutingContext
	RPC     json.RawMessage
}

type ReceiveResult struct {
	Deliver         bool
	Duplicate       bool
	ReceivedThrough uint64
}

type ResumeRequest struct {
	GatewayInstanceID   string
	SessionID           string
	Generation          int64
	PeerSentThrough     uint64
	PeerReceivedThrough uint64
}

type ResumeResult struct {
	Replay          []Frame
	ExpectPeerFrom  uint64
	SentThrough     uint64
	ReceivedThrough uint64
}

type SessionSnapshot struct {
	State           SessionState
	SentThrough     uint64
	ReceivedThrough uint64
	PeerAck         uint64
	JournalFrames   int
	JournalBytes    int
	DisconnectedAt  time.Time
	TerminalError   error
}

// Session is one endpoint's in-memory view of an exec session. Its journals
// are process-local by construction; recreating Session after a process restart
// is not resume.
type Session struct {
	mu sync.Mutex

	config SessionConfig
	state  SessionState

	sentThrough     uint64
	receivedThrough uint64
	peerAck         uint64

	journal      map[uint64]Frame
	journalSizes map[uint64]int
	journalBytes int

	receiveDigests map[uint64][sha256.Size]byte
	receiveOrder   []uint64

	disconnectedAt time.Time
	terminalError  error
}

func NewSession(config SessionConfig) (*Session, error) {
	if config.Role != RoleGateway && config.Role != RoleAgentx {
		return nil, errors.New("session role must be gateway or agentx")
	}
	if err := validateUUID("gatewayInstanceId", config.GatewayInstanceID); err != nil {
		return nil, err
	}
	if err := validateUUID("executorId", config.ExecutorID); err != nil {
		return nil, err
	}
	if err := validateUUID("sessionId", config.SessionID); err != nil {
		return nil, err
	}
	if config.Generation < 1 {
		return nil, errors.New("session generation must be positive")
	}
	if err := validateLimits(config.WireLimits); err != nil {
		return nil, err
	}
	if config.MaxUnackedFrames < 1 {
		return nil, errors.New("MaxUnackedFrames must be positive")
	}
	if config.MaxJournalBytes < config.WireLimits.MaxFrameBytes {
		return nil, errors.New("MaxJournalBytes must hold at least one maximum-sized wire frame")
	}
	if config.MaxReceiveHistoryFrames < 1 {
		return nil, errors.New("MaxReceiveHistoryFrames must be positive")
	}
	if config.ResumeWindow != time.Duration(ResumeWindowMillis)*time.Millisecond {
		return nil, fmt.Errorf("ResumeWindow must be exactly %dms in Phase 1", ResumeWindowMillis)
	}
	return &Session{
		config:         config,
		state:          SessionActive,
		journal:        make(map[uint64]Frame),
		journalSizes:   make(map[uint64]int),
		receiveDigests: make(map[uint64][sha256.Size]byte),
	}, nil
}

// Send allocates the next local sequence and journals the complete immutable
// frame before returning it to the transport. A journal admission failure does
// not consume a sequence number.
func (s *Session) Send(payload Payload) (Frame, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireActiveLocked(); err != nil {
		return Frame{}, err
	}
	if s.sentThrough == math.MaxUint64 {
		return Frame{}, protocolError(ErrorSessionClosed, true, "session sequence exhausted")
	}
	frame := Frame{
		Type:       payload.Type,
		SessionID:  s.config.SessionID,
		SessionSeq: s.sentThrough + 1,
		Ack:        s.receivedThrough,
		Generation: s.config.Generation,
		Context:    cloneContext(payload.Context),
		RPC:        append(json.RawMessage(nil), payload.RPC...),
	}
	if err := frame.ValidateForReceiver(s.config.Role.peer()); err != nil {
		return Frame{}, err
	}
	raw, err := Encode(frame, s.config.WireLimits)
	if err != nil {
		return Frame{}, err
	}
	if len(s.journal) >= s.config.MaxUnackedFrames || len(raw) > s.config.MaxJournalBytes-s.journalBytes {
		return Frame{}, protocolError(ErrorJournalFull, false, "unacked frame journal is full")
	}
	s.sentThrough = frame.SessionSeq
	s.journal[frame.SessionSeq] = cloneFrame(frame)
	s.journalSizes[frame.SessionSeq] = len(raw)
	s.journalBytes += len(raw)
	return cloneFrame(frame), nil
}

// Receive accepts only the next sequence or an exact already-observed replay.
// WebSocket ordering means a future sequence is a permanent gap, not a reason
// to buffer and continue.
func (s *Session) Receive(frame Frame) (ReceiveResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireActiveLocked(); err != nil {
		return ReceiveResult{}, err
	}
	if err := s.validateIdentityLocked(frame.SessionID, frame.Generation); err != nil {
		s.closeLocked(err, SessionClosed)
		return ReceiveResult{}, err
	}
	if err := frame.ValidateForReceiver(s.config.Role); err != nil {
		s.closeLocked(err, SessionClosed)
		return ReceiveResult{}, err
	}
	raw, err := Encode(frame, s.config.WireLimits)
	if err != nil {
		s.closeLocked(err, SessionClosed)
		return ReceiveResult{}, err
	}
	digest := sha256.Sum256(raw)
	if frame.SessionSeq <= s.receivedThrough {
		prior, retained := s.receiveDigests[frame.SessionSeq]
		if !retained {
			err := protocolError(ErrorSequenceConflict, true, "sequence %d is older than retained receive history and cannot be verified", frame.SessionSeq)
			s.closeLocked(err, SessionClosed)
			return ReceiveResult{}, err
		}
		if prior != digest {
			err := protocolError(ErrorSequenceConflict, true, "sequence %d was replayed with different bytes", frame.SessionSeq)
			s.closeLocked(err, SessionClosed)
			return ReceiveResult{}, err
		}
		return ReceiveResult{Duplicate: true, ReceivedThrough: s.receivedThrough}, nil
	}
	if frame.SessionSeq != s.receivedThrough+1 {
		err := protocolError(ErrorResumeGap, true, "received sequence %d after %d", frame.SessionSeq, s.receivedThrough)
		s.closeLocked(err, SessionClosed)
		return ReceiveResult{}, err
	}
	if err := s.validateAckLocked(frame.Ack); err != nil {
		s.closeLocked(err, SessionClosed)
		return ReceiveResult{}, err
	}
	s.applyAckLocked(frame.Ack)
	s.receivedThrough = frame.SessionSeq
	s.recordReceiveDigestLocked(frame.SessionSeq, digest)
	return ReceiveResult{Deliver: true, ReceivedThrough: s.receivedThrough}, nil
}

func (s *Session) AckFrame() (Ack, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireActiveLocked(); err != nil {
		return Ack{}, err
	}
	return Ack{
		Type:       MessageTypeAck,
		SessionID:  s.config.SessionID,
		Generation: s.config.Generation,
		Ack:        s.receivedThrough,
	}, nil
}

func (s *Session) ReceiveAck(ack Ack) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.requireActiveLocked(); err != nil {
		return err
	}
	if err := ack.Validate(); err != nil {
		s.closeLocked(err, SessionClosed)
		return err
	}
	if err := s.validateIdentityLocked(ack.SessionID, ack.Generation); err != nil {
		s.closeLocked(err, SessionClosed)
		return err
	}
	if err := s.validateAckLocked(ack.Ack); err != nil {
		s.closeLocked(err, SessionClosed)
		return err
	}
	s.applyAckLocked(ack.Ack)
	return nil
}

func (s *Session) Disconnect(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now.IsZero() {
		return errors.New("disconnect time is required")
	}
	if s.state != SessionActive {
		return protocolError(ErrorSessionClosed, false, "cannot disconnect session in state %s", s.state)
	}
	s.state = SessionDisconnected
	s.disconnectedAt = now
	return nil
}

// Resume reconciles peer cursors and returns the exact retained frames the
// peer has not processed. It never fabricates a new sequence after a gap.
func (s *Session) Resume(request ResumeRequest, now time.Time) (ResumeResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now.IsZero() {
		return ResumeResult{}, errors.New("resume time is required")
	}
	if request.GatewayInstanceID != s.config.GatewayInstanceID {
		return ResumeResult{}, protocolError(ErrorResumeRejected, true, "gateway process does not own the requested session")
	}
	if err := s.validateIdentityLocked(request.SessionID, request.Generation); err != nil {
		return ResumeResult{}, protocolError(ErrorResumeRejected, true, "%v", err)
	}
	if s.state != SessionDisconnected {
		return ResumeResult{}, protocolError(ErrorResumeRejected, true, "session state is %s, not disconnected", s.state)
	}
	if now.After(s.disconnectedAt.Add(s.config.ResumeWindow)) {
		err := protocolError(ErrorResumeExpired, true, "resume window expired")
		s.closeLocked(err, SessionClosed)
		return ResumeResult{}, err
	}
	if request.PeerSentThrough < s.receivedThrough {
		err := protocolError(ErrorResumeGap, true, "peer retained sends through %d but local endpoint processed through %d", request.PeerSentThrough, s.receivedThrough)
		s.closeLocked(err, SessionClosed)
		return ResumeResult{}, err
	}
	if request.PeerReceivedThrough > s.sentThrough {
		err := protocolError(ErrorResumeGap, true, "peer claims receipt through %d but local endpoint sent through %d", request.PeerReceivedThrough, s.sentThrough)
		s.closeLocked(err, SessionClosed)
		return ResumeResult{}, err
	}
	replay := make([]Frame, 0, len(s.journal))
	if request.PeerReceivedThrough < s.sentThrough {
		for sequence := request.PeerReceivedThrough + 1; sequence <= s.sentThrough; sequence++ {
			frame, retained := s.journal[sequence]
			if !retained {
				err := protocolError(ErrorResumeGap, true, "frame %d required by peer is no longer retained", sequence)
				s.closeLocked(err, SessionClosed)
				return ResumeResult{}, err
			}
			replay = append(replay, cloneFrame(frame))
			if sequence == s.sentThrough {
				break
			}
		}
	}
	if request.PeerReceivedThrough < s.peerAck {
		err := protocolError(ErrorResumeGap, true, "peer receipt cursor %d regressed below committed ack %d", request.PeerReceivedThrough, s.peerAck)
		s.closeLocked(err, SessionClosed)
		return ResumeResult{}, err
	}
	s.applyAckLocked(request.PeerReceivedThrough)
	s.state = SessionActive
	s.disconnectedAt = time.Time{}
	return ResumeResult{
		Replay:          replay,
		ExpectPeerFrom:  s.receivedThrough + 1,
		SentThrough:     s.sentThrough,
		ReceivedThrough: s.receivedThrough,
	}, nil
}

func (s *Session) Fence(generation int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == SessionFenced || s.state == SessionClosed {
		return
	}
	err := protocolError(ErrorStaleGeneration, true, "session generation %d was fenced by generation %d", s.config.Generation, generation)
	s.closeLocked(err, SessionFenced)
}

func (s *Session) Close(cause error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cause == nil {
		cause = protocolError(ErrorSessionClosed, true, "session closed")
	}
	s.closeLocked(cause, SessionClosed)
}

func (s *Session) Snapshot() SessionSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return SessionSnapshot{
		State:           s.state,
		SentThrough:     s.sentThrough,
		ReceivedThrough: s.receivedThrough,
		PeerAck:         s.peerAck,
		JournalFrames:   len(s.journal),
		JournalBytes:    s.journalBytes,
		DisconnectedAt:  s.disconnectedAt,
		TerminalError:   s.terminalError,
	}
}

func (s *Session) requireActiveLocked() error {
	if s.state == SessionActive {
		return nil
	}
	if s.terminalError != nil {
		return s.terminalError
	}
	return protocolError(ErrorSessionClosed, false, "session state is %s", s.state)
}

func (s *Session) validateIdentityLocked(sessionID string, generation int64) error {
	if sessionID != s.config.SessionID {
		return protocolError(ErrorSessionMismatch, true, "sessionId %q does not match %q", sessionID, s.config.SessionID)
	}
	if generation != s.config.Generation {
		return protocolError(ErrorStaleGeneration, true, "generation %d does not match %d", generation, s.config.Generation)
	}
	return nil
}

func (s *Session) validateAckLocked(ack uint64) error {
	if ack < s.peerAck {
		return protocolError(ErrorAckRegression, true, "ack %d is below prior ack %d", ack, s.peerAck)
	}
	if ack > s.sentThrough {
		return protocolError(ErrorAckOutOfRange, true, "ack %d exceeds sent sequence %d", ack, s.sentThrough)
	}
	return nil
}

func (s *Session) applyAckLocked(ack uint64) {
	if ack <= s.peerAck {
		return
	}
	for sequence := s.peerAck + 1; sequence <= ack; sequence++ {
		if size, retained := s.journalSizes[sequence]; retained {
			s.journalBytes -= size
			delete(s.journalSizes, sequence)
			delete(s.journal, sequence)
		}
		if sequence == ack {
			break
		}
	}
	s.peerAck = ack
}

func (s *Session) recordReceiveDigestLocked(sequence uint64, digest [sha256.Size]byte) {
	s.receiveDigests[sequence] = digest
	s.receiveOrder = append(s.receiveOrder, sequence)
	if len(s.receiveOrder) <= s.config.MaxReceiveHistoryFrames {
		return
	}
	oldest := s.receiveOrder[0]
	s.receiveOrder = s.receiveOrder[1:]
	delete(s.receiveDigests, oldest)
}

func (s *Session) closeLocked(cause error, state SessionState) {
	if s.state == SessionClosed || s.state == SessionFenced {
		return
	}
	s.state = state
	s.terminalError = cause
}

func cloneFrame(frame Frame) Frame {
	frame.Context = cloneContext(frame.Context)
	frame.RPC = append(json.RawMessage(nil), frame.RPC...)
	return frame
}

func cloneContext(context *RoutingContext) *RoutingContext {
	if context == nil {
		return nil
	}
	copy := *context
	return &copy
}
