package harnesscontrol

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestControlSessionSequenceAckAndDirection(t *testing.T) {
	pool := newTestControlSession(t, RolePool, 8)
	worker := newTestControlSession(t, RoleWorker, 8)
	event, err := worker.Send(Payload{
		Type: MessageTypeEvent,
		Payload: mustPayload(t, ThreadReadyEvent{
			Kind: EventKindThreadReady, ThreadID: "thread-1", Resumed: false,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if event.SessionSeq != 1 || event.Ack != 0 {
		t.Fatalf("first worker frame = seq %d ack %d", event.SessionSeq, event.Ack)
	}
	received, err := pool.Receive(event)
	if err != nil || !received.Deliver || received.Duplicate || received.ReceivedThrough != 1 {
		t.Fatalf("pool Receive() = %+v, %v", received, err)
	}
	ack, err := pool.AckFrame()
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.ReceiveAck(ack); err != nil {
		t.Fatal(err)
	}
	if snapshot := worker.Snapshot(); snapshot.PeerAck != 1 || snapshot.JournalFrames != 0 || snapshot.SentThrough != 1 {
		t.Fatalf("worker after ack = %+v", snapshot)
	}
	duplicate, err := pool.Receive(event)
	if err != nil || duplicate.Deliver || !duplicate.Duplicate || duplicate.ReceivedThrough != 1 {
		t.Fatalf("duplicate Receive() = %+v, %v", duplicate, err)
	}

	command, err := pool.Send(Payload{
		Type: MessageTypeCommand,
		Payload: mustPayload(t, InterruptCommand{
			Kind: CommandKindInterrupt, Reason: "cancelled", GraceMillis: 1_000, Message: "run cancelled",
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if command.SessionSeq != 1 || command.Ack != 1 {
		t.Fatalf("first pool command = seq %d ack %d", command.SessionSeq, command.Ack)
	}
	if result, err := worker.Receive(command); err != nil || !result.Deliver {
		t.Fatalf("worker Receive(command) = %+v, %v", result, err)
	}
	if _, err := worker.Send(Payload{Type: MessageTypeCommand, Payload: command.Payload}); errorCode(err) != ErrorAttemptMismatch {
		t.Fatalf("worker-originated command error = %v", err)
	}
}

func TestControlSessionJournalsImmutableFrameBeforeTransport(t *testing.T) {
	pool := newTestControlSession(t, RolePool, 8)
	payload := mustPayload(t, InterruptCommand{
		Kind: CommandKindInterrupt, Reason: "fenced", GraceMillis: 1_000, Message: "attempt fenced",
	})
	frame, err := pool.Send(Payload{Type: MessageTypeCommand, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	payload[0] = '['
	frame.Payload[0] = '['
	if err := pool.Disconnect(time.Unix(1_000, 0)); err != nil {
		t.Fatal(err)
	}
	resumed, err := pool.Resume(ResumeRequest{
		PoolInstanceID: testPoolInstanceID, ControlSessionID: testControlSessionID,
		RunAttemptGeneration: 3, PeerReceivedThrough: 0, PeerSentThrough: 0,
	}, time.Unix(1_001, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(resumed.Replay) != 1 || len(resumed.Replay[0].Payload) == 0 || resumed.Replay[0].Payload[0] != '{' {
		t.Fatalf("journal aliases caller bytes: %+v", resumed.Replay)
	}
}

func TestControlSessionRejectsGapConflictAndInvalidACK(t *testing.T) {
	t.Run("sequence gap", func(t *testing.T) {
		pool := newTestControlSession(t, RolePool, 8)
		worker := newTestControlSession(t, RoleWorker, 8)
		if _, err := worker.Send(workerThreadReadyPayload(t, "thread-1")); err != nil {
			t.Fatal(err)
		}
		second, err := worker.Send(workerTurnAcceptedPayload(t, "thread-1", "turn-1"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Receive(second); errorCode(err) != ErrorSequenceGap {
			t.Fatalf("sequence gap error = %v", err)
		}
		if pool.Snapshot().State != SessionClosed {
			t.Fatalf("gap left session in %s", pool.Snapshot().State)
		}
	})

	t.Run("conflicting replay", func(t *testing.T) {
		pool := newTestControlSession(t, RolePool, 8)
		worker := newTestControlSession(t, RoleWorker, 8)
		first, err := worker.Send(workerThreadReadyPayload(t, "thread-1"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Receive(first); err != nil {
			t.Fatal(err)
		}
		conflict := cloneFrame(first)
		conflict.Payload = mustPayload(t, ThreadReadyEvent{Kind: EventKindThreadReady, ThreadID: "changed", Resumed: false})
		if _, err := pool.Receive(conflict); errorCode(err) != ErrorSequenceConflict {
			t.Fatalf("conflicting replay error = %v", err)
		}
	})

	t.Run("ack regression", func(t *testing.T) {
		pool := newTestControlSession(t, RolePool, 8)
		for index := 0; index < 2; index++ {
			if _, err := pool.Send(poolInterruptPayload(t, fmt.Sprintf("interrupt %d", index))); err != nil {
				t.Fatal(err)
			}
		}
		if err := pool.ReceiveAck(testControlAck(2)); err != nil {
			t.Fatal(err)
		}
		if err := pool.ReceiveAck(testControlAck(1)); errorCode(err) != ErrorAckRegression {
			t.Fatalf("ack regression error = %v", err)
		}
	})

	t.Run("ack out of range", func(t *testing.T) {
		pool := newTestControlSession(t, RolePool, 8)
		if err := pool.ReceiveAck(testControlAck(1)); errorCode(err) != ErrorAckOutOfRange {
			t.Fatalf("ack out-of-range error = %v", err)
		}
	})
}

func TestControlSessionJournalBoundDoesNotConsumeSequence(t *testing.T) {
	pool := newTestControlSession(t, RolePool, 1)
	first, err := pool.Send(poolInterruptPayload(t, "first interrupt"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Send(poolInterruptPayload(t, "second interrupt")); errorCode(err) != ErrorJournalFull {
		t.Fatalf("journal full error = %v", err)
	}
	if snapshot := pool.Snapshot(); snapshot.SentThrough != 1 || snapshot.JournalFrames != 1 {
		t.Fatalf("failed send mutated session = %+v", snapshot)
	}
	if err := pool.ReceiveAck(testControlAck(first.SessionSeq)); err != nil {
		t.Fatal(err)
	}
	second, err := pool.Send(poolInterruptPayload(t, "second interrupt"))
	if err != nil {
		t.Fatal(err)
	}
	if second.SessionSeq != 2 {
		t.Fatalf("second admitted sequence = %d, want 2", second.SessionSeq)
	}
}

func TestControlSessionSequenceExhaustionClosesSession(t *testing.T) {
	pool := newTestControlSession(t, RolePool, 1)
	pool.mu.Lock()
	pool.sentThrough = maxSafeJSONInteger
	pool.mu.Unlock()
	if _, err := pool.Send(poolInterruptPayload(t, "cannot allocate")); errorCode(err) != ErrorSessionClosed {
		t.Fatalf("sequence exhaustion error = %v", err)
	}
	if snapshot := pool.Snapshot(); snapshot.State != SessionClosed {
		t.Fatalf("sequence exhaustion left state %s", snapshot.State)
	}
}

func TestControlSessionResumeReplaysExactFrames(t *testing.T) {
	now := time.Unix(1_000, 0)
	pool := newTestControlSession(t, RolePool, 8)
	frames := make([]Frame, 3)
	for index := range frames {
		frame, err := pool.Send(poolInterruptPayload(t, fmt.Sprintf("interrupt %d", index+1)))
		if err != nil {
			t.Fatal(err)
		}
		frames[index] = frame
	}
	if err := pool.Disconnect(now); err != nil {
		t.Fatal(err)
	}
	result, err := pool.Resume(ResumeRequest{
		PoolInstanceID: testPoolInstanceID, ControlSessionID: testControlSessionID,
		RunAttemptGeneration: 3, PeerSentThrough: 0, PeerReceivedThrough: 1,
	}, now.Add(29*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Replay) != 2 || !reflect.DeepEqual(result.Replay[0], frames[1]) || !reflect.DeepEqual(result.Replay[1], frames[2]) {
		t.Fatalf("resume replay = %+v, want frames 2 and 3", result.Replay)
	}
	if result.ExpectPeerFrom != 1 || result.SentThrough != 3 || result.ReceivedThrough != 0 {
		t.Fatalf("resume result = %+v", result)
	}
	if snapshot := pool.Snapshot(); snapshot.State != SessionActive || snapshot.PeerAck != 1 || snapshot.JournalFrames != 2 {
		t.Fatalf("resumed snapshot = %+v", snapshot)
	}
}

func TestControlSessionResumeFailsClosedOnExpiredOrImpossibleCursor(t *testing.T) {
	now := time.Unix(2_000, 0)
	tests := []struct {
		name   string
		setup  func(*testing.T, *Session)
		cursor ResumeRequest
		at     time.Time
		code   ErrorCode
	}{
		{
			name: "expired", setup: func(t *testing.T, pool *Session) {
				if err := pool.Disconnect(now); err != nil {
					t.Fatal(err)
				}
			},
			cursor: testResumeRequest(), at: now.Add(31 * time.Second), code: ErrorResumeExpired,
		},
		{
			name: "ack out of range", setup: func(t *testing.T, pool *Session) {
				if err := pool.Disconnect(now); err != nil {
					t.Fatal(err)
				}
			},
			cursor: withResumeReceived(testResumeRequest(), 1), at: now.Add(time.Second), code: ErrorAckOutOfRange,
		},
		{
			name: "ack regression", setup: func(t *testing.T, pool *Session) {
				if _, err := pool.Send(poolInterruptPayload(t, "interrupt")); err != nil {
					t.Fatal(err)
				}
				if err := pool.ReceiveAck(testControlAck(1)); err != nil {
					t.Fatal(err)
				}
				if err := pool.Disconnect(now); err != nil {
					t.Fatal(err)
				}
			},
			cursor: testResumeRequest(), at: now.Add(time.Second), code: ErrorAckRegression,
		},
		{
			name: "worker send cursor regression", setup: func(t *testing.T, pool *Session) {
				worker := newTestControlSession(t, RoleWorker, 8)
				frame, err := worker.Send(workerThreadReadyPayload(t, "thread-1"))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Receive(frame); err != nil {
					t.Fatal(err)
				}
				if err := pool.Disconnect(now); err != nil {
					t.Fatal(err)
				}
			},
			cursor: testResumeRequest(), at: now.Add(time.Second), code: ErrorSequenceGap,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool := newTestControlSession(t, RolePool, 8)
			test.setup(t, pool)
			_, err := pool.Resume(test.cursor, test.at)
			if errorCode(err) != test.code {
				t.Fatalf("Resume() error = %v, want %s", err, test.code)
			}
			if pool.Snapshot().State != SessionClosed {
				t.Fatalf("failed resume left state %s", pool.Snapshot().State)
			}
		})
	}
}

func TestAttemptBindingRejectsRestartedWorkerAndGenerationDrift(t *testing.T) {
	binding := testAttemptBinding()
	hello := validHello()
	if err := binding.MatchHello(hello); err != nil {
		t.Fatal(err)
	}
	hello.WorkerInstanceID = "30000000-0000-4000-8000-000000000099"
	if err := binding.MatchHello(hello); errorCode(err) != ErrorAttemptMismatch {
		t.Fatalf("restarted worker error = %v", err)
	}
	hello = validHello()
	hello.RunAttemptGeneration++
	if err := binding.MatchHello(hello); errorCode(err) != ErrorStaleGeneration {
		t.Fatalf("generation drift error = %v", err)
	}
}

func newTestControlSession(t *testing.T, role Role, maxFrames int) *Session {
	t.Helper()
	session, err := NewSession(SessionConfig{
		Role: role, PoolInstanceID: testPoolInstanceID, ControlSessionID: testControlSessionID,
		Attempt: testAttemptBinding(), WireLimits: testLimits(), MaxUnackedFrames: maxFrames,
		MaxJournalBytes: 4 * 1024 * 1024, MaxReceiveHistoryFrames: 8,
		ResumeWindow: time.Duration(ResumeWindowMillis) * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func testAttemptBinding() AttemptBinding {
	return AttemptBinding{
		WorkerInstanceID: testWorkerInstanceID, WorkspaceID: testWorkspaceID, SessionID: testSessionID,
		RunID: testRunID, RunAttemptID: testRunAttemptID, RunAttemptGeneration: 3,
		HolderID: "pool-holder", ManifestDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
}

func workerThreadReadyPayload(t *testing.T, threadID string) Payload {
	t.Helper()
	return Payload{
		Type: MessageTypeEvent,
		Payload: mustPayload(t, ThreadReadyEvent{
			Kind: EventKindThreadReady, ThreadID: threadID, Resumed: false,
		}),
	}
}

func workerTurnAcceptedPayload(t *testing.T, threadID, turnID string) Payload {
	t.Helper()
	return Payload{
		Type: MessageTypeEvent,
		Payload: mustPayload(t, TurnAcceptedEvent{
			Kind: EventKindTurnAccepted, ThreadID: threadID, TurnID: turnID,
		}),
	}
}

func poolInterruptPayload(t *testing.T, message string) Payload {
	t.Helper()
	return Payload{
		Type: MessageTypeCommand,
		Payload: mustPayload(t, InterruptCommand{
			Kind: CommandKindInterrupt, Reason: "cancelled", GraceMillis: 1_000, Message: message,
		}),
	}
}

func testControlAck(value uint64) Ack {
	return Ack{
		Type: MessageTypeAck, ControlSessionID: testControlSessionID,
		RunAttemptGeneration: 3, Ack: value,
	}
}

func testResumeRequest() ResumeRequest {
	return ResumeRequest{
		PoolInstanceID: testPoolInstanceID, ControlSessionID: testControlSessionID,
		RunAttemptGeneration: 3,
	}
}

func withResumeReceived(request ResumeRequest, received uint64) ResumeRequest {
	request.PeerReceivedThrough = received
	return request
}

func errorCode(err error) ErrorCode {
	var protocol *ProtocolError
	if errors.As(err, &protocol) {
		return protocol.Code
	}
	return ""
}
