package harnesscontrol

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	maximumSessionJournalFrames = 65_536
	maximumSessionJournalBytes  = 64 * 1024 * 1024
	maximumReceiveHistoryFrames = 65_536
)

type SessionState string

const (
	SessionActive       SessionState = "active"
	SessionDisconnected SessionState = "disconnected"
	SessionFenced       SessionState = "fenced"
	SessionClosed       SessionState = "closed"
)

// AttemptBinding is the complete immutable authority tuple carried by hello.
// It deliberately includes WorkerInstanceID: a restarted worker process does
// not inherit the first process's in-memory resume journal.
type AttemptBinding struct {
	WorkerInstanceID     string
	WorkspaceID          string
	SessionID            string
	RunID                string
	RunAttemptID         string
	RunAttemptGeneration int64
	HolderID             string
	ManifestDigest       string
}

func BindingFromHello(hello Hello) (AttemptBinding, error) {
	if err := hello.Validate(); err != nil {
		return AttemptBinding{}, err
	}
	return AttemptBinding{
		WorkerInstanceID: hello.WorkerInstanceID, WorkspaceID: hello.WorkspaceID,
		SessionID: hello.SessionID, RunID: hello.RunID, RunAttemptID: hello.RunAttemptID,
		RunAttemptGeneration: hello.RunAttemptGeneration, HolderID: hello.HolderID,
		ManifestDigest: hello.ManifestDigest,
	}, nil
}

func (binding AttemptBinding) Validate() error {
	for field, value := range map[string]string{
		"workerInstanceId": binding.WorkerInstanceID, "workspaceId": binding.WorkspaceID,
		"sessionId": binding.SessionID, "runId": binding.RunID, "runAttemptId": binding.RunAttemptID,
	} {
		if err := validateUUID(field, value); err != nil {
			return err
		}
	}
	if err := validateGeneration("runAttemptGeneration", binding.RunAttemptGeneration); err != nil {
		return err
	}
	if err := validateText("holderId", binding.HolderID, 256); err != nil {
		return err
	}
	return validateDigest("manifestDigest", binding.ManifestDigest)
}

func (binding AttemptBinding) MatchHello(hello Hello) error {
	candidate, err := BindingFromHello(hello)
	if err != nil {
		return err
	}
	if candidate.RunAttemptGeneration != binding.RunAttemptGeneration {
		return protocolError(
			ErrorStaleGeneration, true, "hello run-attempt generation %d does not match %d",
			candidate.RunAttemptGeneration, binding.RunAttemptGeneration,
		)
	}
	if candidate != binding {
		return protocolError(ErrorAttemptMismatch, true, "hello does not match the registered attempt binding")
	}
	return nil
}

type SessionConfig struct {
	Role                    Role
	PoolInstanceID          string
	ControlSessionID        string
	Attempt                 AttemptBinding
	WireLimits              Limits
	MaxUnackedFrames        int
	MaxJournalBytes         int
	MaxReceiveHistoryFrames int
	ResumeWindow            time.Duration
}

type Payload struct {
	Type    string
	Payload json.RawMessage
}

type ReceiveResult struct {
	Deliver         bool
	Duplicate       bool
	ReceivedThrough uint64
}

// PreparedReceive is an opaque validation result that lets an authority
// consumer delay advancing the cumulative receive cursor until its
// synchronous side effect has committed. It is bound to one Session and one
// immutable frame digest and cannot be committed to another session.
type PreparedReceive struct {
	session   *Session
	frame     Frame
	digest    [sha256.Size]byte
	duplicate bool
}

type ResumeRequest struct {
	PoolInstanceID       string
	ControlSessionID     string
	RunAttemptGeneration int64
	PeerSentThrough      uint64
	PeerReceivedThrough  uint64
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

// Session is one endpoint's bounded in-memory view of a control stream. The
// journal is intentionally not durable: constructing a new Session after a
// process restart can never satisfy resume.
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
	if config.Role != RolePool && config.Role != RoleWorker {
		return nil, errors.New("control session role must be pool or worker")
	}
	if err := validateUUID("poolInstanceId", config.PoolInstanceID); err != nil {
		return nil, err
	}
	if err := validateUUID("controlSessionId", config.ControlSessionID); err != nil {
		return nil, err
	}
	if err := config.Attempt.Validate(); err != nil {
		return nil, err
	}
	if err := validateLimits(config.WireLimits); err != nil {
		return nil, err
	}
	if config.MaxUnackedFrames < 1 || config.MaxUnackedFrames > maximumSessionJournalFrames {
		return nil, fmt.Errorf("MaxUnackedFrames must be between 1 and %d", maximumSessionJournalFrames)
	}
	if config.MaxJournalBytes < config.WireLimits.MaxFrameBytes || config.MaxJournalBytes > maximumSessionJournalBytes {
		return nil, fmt.Errorf(
			"MaxJournalBytes must be between MaxFrameBytes and %d", maximumSessionJournalBytes,
		)
	}
	if config.MaxReceiveHistoryFrames < 1 || config.MaxReceiveHistoryFrames > maximumReceiveHistoryFrames {
		return nil, fmt.Errorf("MaxReceiveHistoryFrames must be between 1 and %d", maximumReceiveHistoryFrames)
	}
	if config.ResumeWindow != time.Duration(ResumeWindowMillis)*time.Millisecond {
		return nil, fmt.Errorf("ResumeWindow must be exactly %dms in Phase 1", ResumeWindowMillis)
	}
	return &Session{
		config: config, state: SessionActive,
		journal: make(map[uint64]Frame), journalSizes: make(map[uint64]int),
		receiveDigests: make(map[uint64][sha256.Size]byte),
	}, nil
}

// Send allocates a sequence only after the complete immutable frame fits in
// the unacknowledged journal. Returning a frame therefore means a transport
// write may be retried only by replaying this exact frame.
func (session *Session) Send(payload Payload) (Frame, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if err := session.requireActiveLocked(); err != nil {
		return Frame{}, err
	}
	return session.sendLocked(payload)
}

// QueueForResume allocates one immutable outbound frame while the transport
// is detached. The frame cannot cross a socket until the same holder resumes
// this session, where it is included in the ordinary exact replay range.
func (session *Session) QueueForResume(payload Payload) (Frame, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.state != SessionDisconnected {
		return Frame{}, protocolError(
			ErrorSessionClosed, false,
			"cannot queue control frame for resume in session state %s", session.state,
		)
	}
	return session.sendLocked(payload)
}

func (session *Session) sendLocked(payload Payload) (Frame, error) {
	if session.sentThrough >= maxSafeJSONInteger {
		err := protocolError(ErrorSessionClosed, true, "control session sequence exhausted")
		session.closeLocked(err, SessionClosed)
		return Frame{}, err
	}
	frame := Frame{
		Type: payload.Type, ControlSessionID: session.config.ControlSessionID,
		SessionSeq: session.sentThrough + 1, Ack: session.receivedThrough,
		RunAttemptGeneration: session.config.Attempt.RunAttemptGeneration,
		Payload:              append(json.RawMessage(nil), payload.Payload...),
	}
	if err := frame.ValidateForReceiver(session.config.Role.peer(), session.config.WireLimits); err != nil {
		return Frame{}, err
	}
	raw, err := Encode(frame, session.config.WireLimits)
	if err != nil {
		return Frame{}, err
	}
	if len(session.journal) >= session.config.MaxUnackedFrames || len(raw) > session.config.MaxJournalBytes-session.journalBytes {
		return Frame{}, protocolError(ErrorJournalFull, false, "unacknowledged control frame journal is full")
	}
	session.sentThrough = frame.SessionSeq
	session.journal[frame.SessionSeq] = cloneFrame(frame)
	session.journalSizes[frame.SessionSeq] = len(raw)
	session.journalBytes += len(raw)
	return cloneFrame(frame), nil
}

// PrepareReceive validates only the next peer sequence or an exact retained
// replay without advancing ReceivedThrough. Pool-side users call this before
// crossing a durable core boundary and CommitReceive only after that boundary
// succeeds. Ordered WebSocket delivery turns a future sequence into a
// terminal gap.
func (session *Session) PrepareReceive(frame Frame) (PreparedReceive, ReceiveResult, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if err := session.requireActiveLocked(); err != nil {
		return PreparedReceive{}, ReceiveResult{}, err
	}
	if err := session.validateIdentityLocked(frame.ControlSessionID, frame.RunAttemptGeneration); err != nil {
		session.closeLocked(err, SessionClosed)
		return PreparedReceive{}, ReceiveResult{}, err
	}
	if err := frame.ValidateForReceiver(session.config.Role, session.config.WireLimits); err != nil {
		session.closeLocked(err, SessionClosed)
		return PreparedReceive{}, ReceiveResult{}, err
	}
	raw, err := Encode(frame, session.config.WireLimits)
	if err != nil {
		session.closeLocked(err, SessionClosed)
		return PreparedReceive{}, ReceiveResult{}, err
	}
	digest := sha256.Sum256(raw)
	if frame.SessionSeq <= session.receivedThrough {
		prior, retained := session.receiveDigests[frame.SessionSeq]
		if !retained {
			err := protocolError(
				ErrorSequenceConflict, true,
				"sequence %d is older than retained receive history and cannot be verified", frame.SessionSeq,
			)
			session.closeLocked(err, SessionClosed)
			return PreparedReceive{}, ReceiveResult{}, err
		}
		if prior != digest {
			err := protocolError(ErrorSequenceConflict, true, "sequence %d was replayed with different bytes", frame.SessionSeq)
			session.closeLocked(err, SessionClosed)
			return PreparedReceive{}, ReceiveResult{}, err
		}
		prepared := PreparedReceive{session: session, frame: cloneFrame(frame), digest: digest, duplicate: true}
		return prepared, ReceiveResult{Duplicate: true, ReceivedThrough: session.receivedThrough}, nil
	}
	if frame.SessionSeq != session.receivedThrough+1 {
		err := gapProtocolError(
			ErrorSequenceGap, true, session.receivedThrough+1, frame.SessionSeq-1,
			"received sequence %d after %d", frame.SessionSeq, session.receivedThrough,
		)
		session.closeLocked(err, SessionClosed)
		return PreparedReceive{}, ReceiveResult{}, err
	}
	if err := session.validateAckLocked(frame.Ack); err != nil {
		session.closeLocked(err, SessionClosed)
		return PreparedReceive{}, ReceiveResult{}, err
	}
	prepared := PreparedReceive{session: session, frame: cloneFrame(frame), digest: digest}
	return prepared, ReceiveResult{Deliver: true, ReceivedThrough: session.receivedThrough}, nil
}

// CommitReceive advances the cumulative receive and piggyback-ACK cursors for
// a previously prepared frame. A duplicate commit is a verified no-op.
func (session *Session) CommitReceive(prepared PreparedReceive) (ReceiveResult, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if prepared.session != session {
		return ReceiveResult{}, errors.New("prepared control receive belongs to another session")
	}
	if err := session.requireActiveLocked(); err != nil {
		return ReceiveResult{}, err
	}
	frame := prepared.frame
	if prepared.duplicate {
		prior, retained := session.receiveDigests[frame.SessionSeq]
		if frame.SessionSeq > session.receivedThrough || !retained || prior != prepared.digest {
			err := protocolError(ErrorSequenceConflict, true, "prepared duplicate sequence %d no longer matches receive history", frame.SessionSeq)
			session.closeLocked(err, SessionClosed)
			return ReceiveResult{}, err
		}
		return ReceiveResult{Duplicate: true, ReceivedThrough: session.receivedThrough}, nil
	}
	if err := session.validateIdentityLocked(frame.ControlSessionID, frame.RunAttemptGeneration); err != nil {
		session.closeLocked(err, SessionClosed)
		return ReceiveResult{}, err
	}
	if frame.SessionSeq != session.receivedThrough+1 {
		err := protocolError(ErrorSequenceConflict, true, "prepared sequence %d cannot commit after receive cursor %d", frame.SessionSeq, session.receivedThrough)
		session.closeLocked(err, SessionClosed)
		return ReceiveResult{}, err
	}
	if err := session.validateAckLocked(frame.Ack); err != nil {
		session.closeLocked(err, SessionClosed)
		return ReceiveResult{}, err
	}
	session.applyAckLocked(frame.Ack)
	session.receivedThrough = frame.SessionSeq
	session.recordReceiveDigestLocked(frame.SessionSeq, prepared.digest)
	return ReceiveResult{Deliver: true, ReceivedThrough: session.receivedThrough}, nil
}

// Receive preserves the original immediate-commit behavior for endpoints
// whose frame handler has no external authority side effect.
func (session *Session) Receive(frame Frame) (ReceiveResult, error) {
	prepared, _, err := session.PrepareReceive(frame)
	if err != nil {
		return ReceiveResult{}, err
	}
	return session.CommitReceive(prepared)
}

func (session *Session) AckFrame() (Ack, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if err := session.requireActiveLocked(); err != nil {
		return Ack{}, err
	}
	return Ack{
		Type: MessageTypeAck, ControlSessionID: session.config.ControlSessionID,
		RunAttemptGeneration: session.config.Attempt.RunAttemptGeneration, Ack: session.receivedThrough,
	}, nil
}

func (session *Session) ReceiveAck(ack Ack) error {
	session.mu.Lock()
	defer session.mu.Unlock()
	if err := session.requireActiveLocked(); err != nil {
		return err
	}
	if err := ack.Validate(); err != nil {
		session.closeLocked(err, SessionClosed)
		return err
	}
	if err := session.validateIdentityLocked(ack.ControlSessionID, ack.RunAttemptGeneration); err != nil {
		session.closeLocked(err, SessionClosed)
		return err
	}
	if err := session.validateAckLocked(ack.Ack); err != nil {
		session.closeLocked(err, SessionClosed)
		return err
	}
	session.applyAckLocked(ack.Ack)
	return nil
}

func (session *Session) Disconnect(now time.Time) error {
	session.mu.Lock()
	defer session.mu.Unlock()
	if now.IsZero() {
		return errors.New("disconnect time is required")
	}
	if session.state != SessionActive {
		return protocolError(ErrorSessionClosed, false, "cannot disconnect control session in state %s", session.state)
	}
	session.state = SessionDisconnected
	session.disconnectedAt = now
	return nil
}

// Resume reconciles the peer's two independent cursors and returns only exact
// retained frames. It never fills a missing range with newly allocated frames.
func (session *Session) Resume(request ResumeRequest, now time.Time) (ResumeResult, error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if now.IsZero() {
		return ResumeResult{}, errors.New("resume time is required")
	}
	if request.PoolInstanceID != session.config.PoolInstanceID {
		return ResumeResult{}, protocolError(ErrorResumeRejected, true, "pool process does not own the requested control session")
	}
	if request.ControlSessionID != session.config.ControlSessionID {
		return ResumeResult{}, protocolError(ErrorResumeRejected, true, "control session is not owned by this pool process")
	}
	if request.RunAttemptGeneration != session.config.Attempt.RunAttemptGeneration {
		return ResumeResult{}, protocolError(ErrorResumeRejected, true, "run-attempt generation does not match the control session")
	}
	if err := validateCursor("peerSentThrough", request.PeerSentThrough); err != nil {
		return ResumeResult{}, err
	}
	if err := validateCursor("peerReceivedThrough", request.PeerReceivedThrough); err != nil {
		return ResumeResult{}, err
	}
	if session.state != SessionDisconnected {
		return ResumeResult{}, protocolError(ErrorResumeRejected, true, "control session state is %s, not disconnected", session.state)
	}
	if now.After(session.disconnectedAt.Add(session.config.ResumeWindow)) {
		err := protocolError(ErrorResumeExpired, true, "control session resume window expired")
		session.closeLocked(err, SessionClosed)
		return ResumeResult{}, err
	}
	if request.PeerSentThrough < session.receivedThrough {
		err := gapProtocolError(
			ErrorSequenceGap, true, request.PeerSentThrough+1, session.receivedThrough,
			"peer retained sends through %d but local endpoint processed through %d",
			request.PeerSentThrough, session.receivedThrough,
		)
		session.closeLocked(err, SessionClosed)
		return ResumeResult{}, err
	}
	if request.PeerReceivedThrough > session.sentThrough {
		err := protocolError(
			ErrorAckOutOfRange, true, "peer claims receipt through %d but local endpoint sent through %d",
			request.PeerReceivedThrough, session.sentThrough,
		)
		session.closeLocked(err, SessionClosed)
		return ResumeResult{}, err
	}
	if request.PeerReceivedThrough < session.peerAck {
		err := protocolError(
			ErrorAckRegression, true, "peer receipt cursor %d is below prior ack %d",
			request.PeerReceivedThrough, session.peerAck,
		)
		session.closeLocked(err, SessionClosed)
		return ResumeResult{}, err
	}
	replay := make([]Frame, 0, len(session.journal))
	for sequence := request.PeerReceivedThrough + 1; sequence <= session.sentThrough; sequence++ {
		frame, retained := session.journal[sequence]
		if !retained {
			err := gapProtocolError(
				ErrorSequenceGap, true, sequence, session.sentThrough,
				"control frame %d required by peer is no longer retained", sequence,
			)
			session.closeLocked(err, SessionClosed)
			return ResumeResult{}, err
		}
		replay = append(replay, cloneFrame(frame))
		if sequence == session.sentThrough {
			break
		}
	}
	session.applyAckLocked(request.PeerReceivedThrough)
	session.state = SessionActive
	session.disconnectedAt = time.Time{}
	return ResumeResult{
		Replay: replay, ExpectPeerFrom: session.receivedThrough + 1,
		SentThrough: session.sentThrough, ReceivedThrough: session.receivedThrough,
	}, nil
}

func (session *Session) Fence(generation int64) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.state == SessionFenced || session.state == SessionClosed {
		return
	}
	err := protocolError(
		ErrorStaleGeneration, true, "control session generation %d was fenced by generation %d",
		session.config.Attempt.RunAttemptGeneration, generation,
	)
	session.closeLocked(err, SessionFenced)
}

func (session *Session) Close(cause error) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if cause == nil {
		cause = protocolError(ErrorSessionClosed, true, "control session closed")
	}
	session.closeLocked(cause, SessionClosed)
}

func (session *Session) Snapshot() SessionSnapshot {
	session.mu.Lock()
	defer session.mu.Unlock()
	return SessionSnapshot{
		State: session.state, SentThrough: session.sentThrough, ReceivedThrough: session.receivedThrough,
		PeerAck: session.peerAck, JournalFrames: len(session.journal), JournalBytes: session.journalBytes,
		DisconnectedAt: session.disconnectedAt, TerminalError: session.terminalError,
	}
}

func (session *Session) AttemptBinding() AttemptBinding {
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.config.Attempt
}

func (session *Session) requireActiveLocked() error {
	if session.state == SessionActive {
		return nil
	}
	if session.terminalError != nil {
		return session.terminalError
	}
	return protocolError(ErrorSessionClosed, false, "control session state is %s", session.state)
}

func (session *Session) validateIdentityLocked(controlSessionID string, generation int64) error {
	if controlSessionID != session.config.ControlSessionID {
		return protocolError(ErrorAttemptMismatch, true, "controlSessionId does not match the active control session")
	}
	if generation != session.config.Attempt.RunAttemptGeneration {
		return protocolError(
			ErrorStaleGeneration, true, "run-attempt generation %d does not match %d",
			generation, session.config.Attempt.RunAttemptGeneration,
		)
	}
	return nil
}

func (session *Session) validateAckLocked(ack uint64) error {
	if ack < session.peerAck {
		return protocolError(ErrorAckRegression, true, "ack %d is below prior ack %d", ack, session.peerAck)
	}
	if ack > session.sentThrough {
		return protocolError(ErrorAckOutOfRange, true, "ack %d exceeds sent sequence %d", ack, session.sentThrough)
	}
	return nil
}

func (session *Session) applyAckLocked(ack uint64) {
	if ack <= session.peerAck {
		return
	}
	for sequence := session.peerAck + 1; sequence <= ack; sequence++ {
		if size, retained := session.journalSizes[sequence]; retained {
			session.journalBytes -= size
			delete(session.journalSizes, sequence)
			delete(session.journal, sequence)
		}
		if sequence == ack {
			break
		}
	}
	session.peerAck = ack
}

func (session *Session) recordReceiveDigestLocked(sequence uint64, digest [sha256.Size]byte) {
	session.receiveDigests[sequence] = digest
	session.receiveOrder = append(session.receiveOrder, sequence)
	if len(session.receiveOrder) <= session.config.MaxReceiveHistoryFrames {
		return
	}
	oldest := session.receiveOrder[0]
	session.receiveOrder = session.receiveOrder[1:]
	delete(session.receiveDigests, oldest)
}

func (session *Session) closeLocked(cause error, state SessionState) {
	if session.state == SessionClosed || session.state == SessionFenced {
		return
	}
	session.state = state
	session.terminalError = cause
}

func cloneFrame(frame Frame) Frame {
	frame.Payload = append(json.RawMessage(nil), frame.Payload...)
	return frame
}
