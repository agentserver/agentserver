package harnessworker

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/harnesscontrol"
	"nhooyr.io/websocket"
)

func TestWorkerControlClientKeepsAckOrderingBarrierThroughEventWrite(t *testing.T) {
	limits := orderingControlLimits()
	workerSession := newOrderingControlSession(t, harnesscontrol.RoleWorker)
	poolSession := newOrderingControlSession(t, harnesscontrol.RolePool)
	socket := newOrderingWorkerControlSocket()
	connection := &workerControlConnection{socket: socket}
	client := &WorkerControlClient{
		config: WorkerControlClientConfig{WireLimits: limits, WriteTimeout: time.Second},
		ctx:    context.Background(), session: workerSession, connection: connection,
		stateChanged: make(chan struct{}), done: make(chan struct{}),
		commands: make(chan harnesscontrol.InterruptCommand, 1),
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	eventDone := make(chan error, 1)
	go func() {
		eventDone <- client.sendControlEvent(ctx, harnesscontrol.ThreadReadyEvent{
			Kind: harnesscontrol.EventKindThreadReady, ThreadID: "thread-ordering", Resumed: false,
		}, false, false)
	}()
	select {
	case <-socket.firstWriteStarted:
	case <-ctx.Done():
		t.Fatal("worker event did not reach the blocked socket write")
	}

	payload, err := json.Marshal(harnesscontrol.InterruptCommand{
		Kind: harnesscontrol.CommandKindInterrupt, Reason: "cancelled",
		GraceMillis: 1000, Message: "cancel ordering test",
	})
	if err != nil {
		t.Fatal(err)
	}
	command, err := poolSession.Send(harnesscontrol.Payload{
		Type: harnesscontrol.MessageTypeCommand, Payload: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := harnesscontrol.Encode(command, limits)
	if err != nil {
		t.Fatal(err)
	}
	socket.reads <- raw
	readDone := make(chan error, 1)
	go func() { readDone <- client.readOne(ctx, workerSession, connection, true) }()

	// The command reader has the frame bytes, but it must not advance to ACK 1
	// while the older worker event carrying piggyback ACK 0 is still blocked.
	time.Sleep(20 * time.Millisecond)
	advancedBeforeWrite := workerSession.Snapshot().ReceivedThrough
	close(socket.releaseFirstWrite)
	if err := <-eventDone; err != nil {
		t.Fatalf("send worker event: %v", err)
	}
	if err := <-readDone; err != nil {
		t.Fatalf("receive pool command: %v", err)
	}
	if advancedBeforeWrite != 0 {
		t.Fatalf("command receive cursor advanced to %d before the older event write completed", advancedBeforeWrite)
	}
	if snapshot := workerSession.Snapshot(); snapshot.ReceivedThrough != 1 {
		t.Fatalf("worker receive cursor after ordered writes = %d, want 1", snapshot.ReceivedThrough)
	}
}

func TestWorkerControlClientReplaysOldFramesBeforeNewResumeAck(t *testing.T) {
	limits := orderingControlLimits()
	workerSession := newOrderingControlSession(t, harnesscontrol.RoleWorker)
	poolSession := newOrderingControlSession(t, harnesscontrol.RolePool)
	commandPayload, err := json.Marshal(harnesscontrol.InterruptCommand{
		Kind: harnesscontrol.CommandKindInterrupt, Reason: "cancelled",
		GraceMillis: 1000, Message: "cancel resume ordering test",
	})
	if err != nil {
		t.Fatal(err)
	}
	firstCommand, err := poolSession.Send(harnesscontrol.Payload{
		Type: harnesscontrol.MessageTypeCommand, Payload: commandPayload,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workerSession.Receive(firstCommand); err != nil {
		t.Fatal(err)
	}
	eventPayload, err := json.Marshal(harnesscontrol.ThreadReadyEvent{
		Kind: harnesscontrol.EventKindThreadReady, ThreadID: "thread-resume-ordering", Resumed: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	oldReplay, err := workerSession.Send(harnesscontrol.Payload{
		Type: harnesscontrol.MessageTypeEvent, Payload: eventPayload,
	})
	if err != nil {
		t.Fatal(err)
	}
	secondCommand, err := poolSession.Send(harnesscontrol.Payload{
		Type: harnesscontrol.MessageTypeCommand, Payload: commandPayload,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workerSession.Receive(secondCommand); err != nil {
		t.Fatal(err)
	}
	if oldReplay.Ack != 1 || workerSession.Snapshot().ReceivedThrough != 2 {
		t.Fatalf("test cursors = replay ACK %d received %d, want 1 then 2", oldReplay.Ack, workerSession.Snapshot().ReceivedThrough)
	}

	socket := &recordingWorkerControlSocket{}
	client := &WorkerControlClient{config: WorkerControlClientConfig{
		WireLimits: limits, WriteTimeout: time.Second,
	}}
	if err := client.writeResumeTail(
		context.Background(), workerSession, &workerControlConnection{socket: socket},
		[]harnesscontrol.Frame{oldReplay}, true,
	); err != nil {
		t.Fatal(err)
	}
	writes := socket.snapshot()
	if len(writes) != 2 {
		t.Fatalf("resume writes = %d, want replay then ACK", len(writes))
	}
	first, err := harnesscontrol.Decode(writes[0], limits)
	if err != nil {
		t.Fatal(err)
	}
	second, err := harnesscontrol.Decode(writes[1], limits)
	if err != nil {
		t.Fatal(err)
	}
	if first.Frame == nil || first.Frame.Ack != 1 || second.Ack == nil || second.Ack.Ack != 2 {
		t.Fatalf("resume wire order = first %+v second %+v, want replay ACK 1 then standalone ACK 2", first, second)
	}
}

func orderingControlLimits() harnesscontrol.Limits {
	return harnesscontrol.Limits{MaxFrameBytes: 64 * 1024, MaxJSONValues: 1024, MaxJSONDepth: 32}
}

func newOrderingControlSession(t *testing.T, role harnesscontrol.Role) *harnesscontrol.Session {
	t.Helper()
	limits := orderingControlLimits()
	session, err := harnesscontrol.NewSession(harnesscontrol.SessionConfig{
		Role: role, PoolInstanceID: "60000000-0000-4000-8000-000000000006",
		ControlSessionID: "70000000-0000-4000-8000-000000000007",
		Attempt: harnesscontrol.AttemptBinding{
			WorkerInstanceID:     "10000000-0000-4000-8000-000000000001",
			WorkspaceID:          "20000000-0000-4000-8000-000000000002",
			SessionID:            "30000000-0000-4000-8000-000000000003",
			RunID:                "40000000-0000-4000-8000-000000000004",
			RunAttemptID:         "50000000-0000-4000-8000-000000000005",
			RunAttemptGeneration: 1,
			HolderID:             "holder-ordering-test",
			ManifestDigest:       strings.Repeat("a", 64),
		},
		WireLimits: limits, MaxUnackedFrames: 16, MaxJournalBytes: limits.MaxFrameBytes,
		MaxReceiveHistoryFrames: 16,
		ResumeWindow:            time.Duration(harnesscontrol.ResumeWindowMillis) * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return session
}

type orderingWorkerControlSocket struct {
	reads             chan []byte
	firstWriteStarted chan struct{}
	releaseFirstWrite chan struct{}

	mu         sync.Mutex
	writeCount int
	startOnce  sync.Once
}

func newOrderingWorkerControlSocket() *orderingWorkerControlSocket {
	return &orderingWorkerControlSocket{
		reads: make(chan []byte, 1), firstWriteStarted: make(chan struct{}),
		releaseFirstWrite: make(chan struct{}),
	}
}

func (socket *orderingWorkerControlSocket) Read(ctx context.Context) (websocket.MessageType, []byte, error) {
	select {
	case raw := <-socket.reads:
		return websocket.MessageText, raw, nil
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	}
}

func (socket *orderingWorkerControlSocket) Write(ctx context.Context, _ websocket.MessageType, _ []byte) error {
	socket.mu.Lock()
	socket.writeCount++
	writeNumber := socket.writeCount
	socket.mu.Unlock()
	if writeNumber != 1 {
		return nil
	}
	socket.startOnce.Do(func() { close(socket.firstWriteStarted) })
	select {
	case <-socket.releaseFirstWrite:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (*orderingWorkerControlSocket) CloseNow() error { return nil }

type recordingWorkerControlSocket struct {
	mu     sync.Mutex
	writes [][]byte
}

func (*recordingWorkerControlSocket) Read(ctx context.Context) (websocket.MessageType, []byte, error) {
	<-ctx.Done()
	return 0, nil, ctx.Err()
}

func (socket *recordingWorkerControlSocket) Write(_ context.Context, _ websocket.MessageType, raw []byte) error {
	socket.mu.Lock()
	socket.writes = append(socket.writes, append([]byte(nil), raw...))
	socket.mu.Unlock()
	return nil
}

func (*recordingWorkerControlSocket) CloseNow() error { return nil }

func (socket *recordingWorkerControlSocket) snapshot() [][]byte {
	socket.mu.Lock()
	defer socket.mu.Unlock()
	result := make([][]byte, len(socket.writes))
	for index := range socket.writes {
		result[index] = append([]byte(nil), socket.writes[index]...)
	}
	return result
}
