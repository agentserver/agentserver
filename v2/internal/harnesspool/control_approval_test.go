package harnesspool

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/harnesscontrol"
	"nhooyr.io/websocket"
)

func TestControlServerAcknowledgesApprovalBeforeCanonicalDecision(t *testing.T) {
	lifecycle := newBlockingControlApprovalLifecycle()
	fixture := newControlServerFixture(t, lifecycle)
	connection, worker := connectAcceptedControlTurn(t, fixture)
	request := controlApprovalRequest(fixture, "91", "call-approval-1")

	frame, err := worker.Send(harnesscontrol.Payload{
		Type: harnesscontrol.MessageTypeEvent, Payload: mustControlPayload(t, request),
	})
	if err != nil {
		t.Fatal(err)
	}
	writeControlValue(t, connection, fixture.config.WireLimits, frame)
	select {
	case observed := <-lifecycle.requests:
		if observed != request {
			t.Fatalf("observed approval = %+v, want %+v", observed, request)
		}
	case <-time.After(time.Second):
		t.Fatal("approval observation did not start")
	}

	ack := readControlMessage(t, connection, fixture.config.WireLimits)
	if ack.Ack == nil || ack.Ack.Ack != frame.SessionSeq {
		t.Fatalf("approval ACK = %+v, want sequence %d", ack, frame.SessionSeq)
	}
	if err := worker.ReceiveAck(*ack.Ack); err != nil {
		t.Fatal(err)
	}
	if snapshot, found := fixture.control.Snapshot(); !found || snapshot.ReceivedThrough != frame.SessionSeq {
		t.Fatalf("approval receive cursor = found %v snapshot %+v", found, snapshot)
	}

	lifecycle.releaseAll()
	message := readControlMessage(t, connection, fixture.config.WireLimits)
	if message.Frame == nil {
		t.Fatalf("approval outcome = %+v", message)
	}
	command, err := harnesscontrol.DecodeCommandPayload(message.Frame.Payload, fixture.config.WireLimits)
	if err != nil {
		t.Fatal(err)
	}
	want := canonicalControlApprovalOutcome(request, "approved")
	if command.ApprovalOutcome == nil || *command.ApprovalOutcome != want {
		t.Fatalf("approval outcome = %+v, want %+v", command.ApprovalOutcome, want)
	}
}

func TestControlServerReplaysJournaledApprovalOutcomeAfterResume(t *testing.T) {
	lifecycle := newBlockingControlApprovalLifecycle()
	fixture := newControlServerFixture(t, lifecycle)
	firstConnection, worker := connectAcceptedControlTurn(t, fixture)
	request := controlApprovalRequest(fixture, "92", "call-approval-replay")
	sendWorkerEvent(t, firstConnection, worker, request)
	select {
	case <-lifecycle.requests:
	case <-time.After(time.Second):
		t.Fatal("approval observation did not start")
	}

	lifecycle.releaseAll()
	waitForControlJournalFrames(t, fixture.control, 1)
	firstConnection.CloseNow()
	waitForControlState(t, fixture.control, harnesscontrol.SessionDisconnected)

	snapshot := worker.Snapshot()
	resumeHello := fixture.hello(&harnesscontrol.ResumeCursor{
		PoolInstanceID: testControlPoolInstanceID, ControlSessionID: testHarnessControlID,
		RunAttemptGeneration: fixture.prepared.Manifest.RunAttemptGeneration,
		WorkerSentThrough:    snapshot.SentThrough, WorkerReceivedThrough: snapshot.ReceivedThrough,
	})
	secondConnection := fixture.dial(t, fixture.control.Capability())
	writeControlValue(t, secondConnection, fixture.config.WireLimits, resumeHello)
	welcome := readControlMessage(t, secondConnection, fixture.config.WireLimits)
	if welcome.Welcome == nil || welcome.Welcome.ResumeStatus != "resumed" || welcome.Welcome.PoolSentThrough != 1 {
		t.Fatalf("approval resume welcome = %+v", welcome)
	}
	replayed := readControlMessage(t, secondConnection, fixture.config.WireLimits)
	if replayed.Frame == nil || replayed.Frame.SessionSeq != 1 {
		t.Fatalf("replayed approval frame = %+v", replayed)
	}
	command, err := harnesscontrol.DecodeCommandPayload(replayed.Frame.Payload, fixture.config.WireLimits)
	if err != nil {
		t.Fatal(err)
	}
	if command.ApprovalOutcome == nil || command.ApprovalOutcome.ApprovalID != request.ApprovalID {
		t.Fatalf("replayed approval outcome = %+v", command)
	}
}

func TestControlServerBoundsAndUniquelyCorrelatesOutstandingApprovals(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*harnesscontrol.ApprovalRequestEvent)
		want   string
	}{
		{
			name: "duplicate approval ID",
			mutate: func(request *harnesscontrol.ApprovalRequestEvent) {
				request.CallID = "call-approval-distinct"
			},
			want: "approval ID",
		},
		{
			name: "duplicate call ID",
			mutate: func(request *harnesscontrol.ApprovalRequestEvent) {
				request.ApprovalID = "93000000-0000-4000-8000-000000000093"
				request.ExecutionID = "94000000-0000-4000-8000-000000000094"
				request.Nonce = "95000000-0000-4000-8000-000000000095"
			},
			want: "call ID",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lifecycle := newBlockingControlApprovalLifecycle()
			fixture := newControlServerFixture(t, lifecycle)
			fixture.server.config.MaxOutstandingApprovals = 2
			connection, worker := connectAcceptedControlTurn(t, fixture)
			first := controlApprovalRequest(fixture, "96", "call-approval-duplicate")
			sendWorkerEvent(t, connection, worker, first)
			second := first
			test.mutate(&second)
			frame, err := worker.Send(harnesscontrol.Payload{
				Type: harnesscontrol.MessageTypeEvent, Payload: mustControlPayload(t, second),
			})
			if err != nil {
				t.Fatal(err)
			}
			writeControlValue(t, connection, fixture.config.WireLimits, frame)
			failure := readControlMessage(t, connection, fixture.config.WireLimits)
			if failure.SessionError == nil || failure.SessionError.Code != harnesscontrol.ErrorAttemptMismatch {
				t.Fatalf("duplicate approval failure = %+v", failure)
			}
			if _, err := fixture.control.WaitTerminal(testContext(t)); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("duplicate approval terminal error = %v, want %q", err, test.want)
			}
		})
	}

	t.Run("outstanding limit", func(t *testing.T) {
		lifecycle := newBlockingControlApprovalLifecycle()
		fixture := newControlServerFixture(t, lifecycle)
		fixture.server.config.MaxOutstandingApprovals = 1
		connection, worker := connectAcceptedControlTurn(t, fixture)
		first := controlApprovalRequest(fixture, "97", "call-approval-limit-1")
		sendWorkerEvent(t, connection, worker, first)
		second := controlApprovalRequest(fixture, "98", "call-approval-limit-2")
		frame, err := worker.Send(harnesscontrol.Payload{
			Type: harnesscontrol.MessageTypeEvent, Payload: mustControlPayload(t, second),
		})
		if err != nil {
			t.Fatal(err)
		}
		writeControlValue(t, connection, fixture.config.WireLimits, frame)
		failure := readControlMessage(t, connection, fixture.config.WireLimits)
		if failure.SessionError == nil || failure.SessionError.Code != harnesscontrol.ErrorJournalFull {
			t.Fatalf("approval limit failure = %+v", failure)
		}
	})
}

func TestControlServerCancelsOutstandingApprovalAtTurnTerminal(t *testing.T) {
	lifecycle := newBlockingControlApprovalLifecycle()
	fixture := newControlServerFixture(t, lifecycle)
	connection, worker := connectAcceptedControlTurn(t, fixture)
	request := controlApprovalRequest(fixture, "99", "call-approval-terminal")
	sendWorkerEvent(t, connection, worker, request)
	select {
	case <-lifecycle.requests:
	case <-time.After(time.Second):
		t.Fatal("approval observation did not start")
	}
	sendWorkerEvent(t, connection, worker, harnesscontrol.TurnTerminalEvent{
		Kind: harnesscontrol.EventKindTurnTerminal, ThreadID: "thread-approval-control",
		TurnID: "turn-approval-control", Status: "completed", RolloutLocator: testCompletedRolloutLocator,
	})
	select {
	case <-lifecycle.cancelled:
	case <-time.After(time.Second):
		t.Fatal("turn terminal did not cancel outstanding approval observation")
	}
}

func TestControlApprovalOutcomeRequiresLaterCanonicalVersion(t *testing.T) {
	request := harnesscontrol.ApprovalRequestEvent{
		Kind:  harnesscontrol.EventKindApprovalRequest,
		RunID: "10000000-0000-4000-8000-000000000001", CallID: "call-version",
		RunAttemptGeneration: 3, ToolCatalogDigest: strings.Repeat("a", 64),
		ExecutionID: "20000000-0000-4000-8000-000000000002",
		ApprovalID:  "30000000-0000-4000-8000-000000000003",
		Nonce:       "40000000-0000-4000-8000-000000000004",
		ContextHash: strings.Repeat("b", 64), ApprovalVersion: 2,
		ExpiresAt: time.Now().Add(time.Minute).UTC(),
	}
	outcome := canonicalControlApprovalOutcome(request, "approved")
	outcome.ApprovalVersion = request.ApprovalVersion
	if err := validateApprovalOutcomeCorrelation(request, outcome); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("non-incrementing approval outcome error = %v", err)
	}
}

func connectAcceptedControlTurn(
	t *testing.T,
	fixture *controlServerFixture,
) (*websocket.Conn, *harnesscontrol.Session) {
	t.Helper()
	connection := fixture.dial(t, fixture.control.Capability())
	hello := fixture.hello(nil)
	writeControlValue(t, connection, fixture.config.WireLimits, hello)
	welcome := readControlMessage(t, connection, fixture.config.WireLimits)
	if welcome.Welcome == nil {
		t.Fatalf("control welcome = %+v", welcome)
	}
	worker := fixture.workerSession(t, hello, *welcome.Welcome)
	sendWorkerEvent(t, connection, worker, harnesscontrol.ThreadReadyEvent{
		Kind: harnesscontrol.EventKindThreadReady, ThreadID: "thread-approval-control", Resumed: false,
	})
	sendWorkerEvent(t, connection, worker, harnesscontrol.TurnAcceptedEvent{
		Kind: harnesscontrol.EventKindTurnAccepted, ThreadID: "thread-approval-control", TurnID: "turn-approval-control",
	})
	return connection, worker
}

func controlApprovalRequest(
	fixture *controlServerFixture,
	suffix string,
	callID string,
) harnesscontrol.ApprovalRequestEvent {
	return harnesscontrol.ApprovalRequestEvent{
		Kind:  harnesscontrol.EventKindApprovalRequest,
		RunID: fixture.prepared.Manifest.RunID, CallID: callID,
		RunAttemptGeneration: fixture.prepared.Manifest.RunAttemptGeneration,
		ToolCatalogDigest:    fixture.prepared.Manifest.ExecutorMCP.CatalogDigest,
		ExecutionID:          "a1000000-0000-4000-8000-0000000000" + suffix,
		ApprovalID:           "a2000000-0000-4000-8000-0000000000" + suffix,
		Nonce:                "a3000000-0000-4000-8000-0000000000" + suffix,
		ApprovalVersion:      1,
		ContextHash:          strings.Repeat(suffix[:1], 64),
		ExpiresAt:            time.Now().Add(10 * time.Second).UTC(),
	}
}

func canonicalControlApprovalOutcome(
	request harnesscontrol.ApprovalRequestEvent,
	status string,
) harnesscontrol.ApprovalOutcomeCommand {
	return harnesscontrol.ApprovalOutcomeCommand{
		Kind:  harnesscontrol.CommandKindApprovalOutcome,
		RunID: request.RunID, CallID: request.CallID,
		RunAttemptGeneration: request.RunAttemptGeneration,
		ToolCatalogDigest:    request.ToolCatalogDigest,
		ExecutionID:          request.ExecutionID, ApprovalID: request.ApprovalID,
		Nonce: request.Nonce, ContextHash: request.ContextHash,
		Status: status, ApprovalVersion: request.ApprovalVersion + 1,
	}
}

type blockingControlApprovalLifecycle struct {
	requests    chan harnesscontrol.ApprovalRequestEvent
	release     chan struct{}
	cancelled   chan struct{}
	releaseOnce sync.Once
	cancelOnce  sync.Once
}

func newBlockingControlApprovalLifecycle() *blockingControlApprovalLifecycle {
	return &blockingControlApprovalLifecycle{
		requests: make(chan harnesscontrol.ApprovalRequestEvent, 8),
		release:  make(chan struct{}), cancelled: make(chan struct{}),
	}
}

func (*blockingControlApprovalLifecycle) ThreadStarted(string) error        { return nil }
func (*blockingControlApprovalLifecycle) TurnAccepted(string, string) error { return nil }

func (lifecycle *blockingControlApprovalLifecycle) AwaitApproval(
	ctx context.Context,
	request harnesscontrol.ApprovalRequestEvent,
) (harnesscontrol.ApprovalOutcomeCommand, error) {
	lifecycle.requests <- request
	select {
	case <-lifecycle.release:
		return canonicalControlApprovalOutcome(request, "approved"), nil
	case <-ctx.Done():
		lifecycle.cancelOnce.Do(func() { close(lifecycle.cancelled) })
		return harnesscontrol.ApprovalOutcomeCommand{}, context.Cause(ctx)
	}
}

func (lifecycle *blockingControlApprovalLifecycle) releaseAll() {
	lifecycle.releaseOnce.Do(func() { close(lifecycle.release) })
}

var _ AttemptApprovalLifecycle = (*blockingControlApprovalLifecycle)(nil)
