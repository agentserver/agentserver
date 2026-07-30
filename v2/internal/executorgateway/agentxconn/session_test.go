package agentxconn

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
)

const (
	testGatewayID  = "10000000-0000-0000-0000-000000000001"
	testExecutorID = "20000000-0000-0000-0000-000000000002"
	testSessionID  = "30000000-0000-0000-0000-000000000003"
)

func TestSessionSequenceAckAndDuplicateDelivery(t *testing.T) {
	gateway := newTestSession(t, RoleGateway, 8)
	agentx := newTestSession(t, RoleAgentx, 8)
	request, err := gateway.Send(processReadPayload("request-1"))
	if err != nil {
		t.Fatal(err)
	}
	if request.SessionSeq != 1 || request.Ack != 0 {
		t.Fatalf("first gateway frame = seq %d ack %d", request.SessionSeq, request.Ack)
	}
	received, err := agentx.Receive(request)
	if err != nil || !received.Deliver || received.Duplicate || received.ReceivedThrough != 1 {
		t.Fatalf("agentx Receive() = %+v, %v", received, err)
	}

	ack, err := agentx.AckFrame()
	if err != nil {
		t.Fatal(err)
	}
	if err := gateway.ReceiveAck(ack); err != nil {
		t.Fatal(err)
	}
	if snapshot := gateway.Snapshot(); snapshot.PeerAck != 1 || snapshot.JournalFrames != 0 || snapshot.SentThrough != 1 {
		t.Fatalf("gateway after ack = %+v", snapshot)
	}

	duplicate, err := agentx.Receive(request)
	if err != nil || duplicate.Deliver || !duplicate.Duplicate || duplicate.ReceivedThrough != 1 {
		t.Fatalf("duplicate Receive() = %+v, %v", duplicate, err)
	}

	context := testRoutingContext()
	response, err := agentx.Send(Payload{
		Type:    MessageTypeRPC,
		Context: &context,
		RPC:     json.RawMessage(`{"id":"request-1","result":{"chunks":[],"nextSeq":1,"exited":true,"exitCode":0,"closed":true,"failure":null,"sandboxDenied":false}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.SessionSeq != 1 || response.Ack != 1 {
		t.Fatalf("agentx response = seq %d ack %d", response.SessionSeq, response.Ack)
	}
	if result, err := gateway.Receive(response); err != nil || !result.Deliver {
		t.Fatalf("gateway Receive(response) = %+v, %v", result, err)
	}
	before := agentx.Snapshot().SentThrough
	if _, err := agentx.AckFrame(); err != nil {
		t.Fatal(err)
	}
	if after := agentx.Snapshot().SentThrough; after != before {
		t.Fatalf("standalone ack consumed sequence: before=%d after=%d", before, after)
	}
}

func TestSessionRejectsGapConflictingReplayAndAckRegression(t *testing.T) {
	t.Run("gap", func(t *testing.T) {
		gateway := newTestSession(t, RoleGateway, 8)
		agentx := newTestSession(t, RoleAgentx, 8)
		_, err := gateway.Send(processReadPayload("request-1"))
		if err != nil {
			t.Fatal(err)
		}
		second, err := gateway.Send(processReadPayload("request-2"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := agentx.Receive(second); codeOf(err) != ErrorResumeGap {
			t.Fatalf("sequence gap error = %v", err)
		}
		if state := agentx.Snapshot().State; state != SessionClosed {
			t.Fatalf("gap session state = %s", state)
		}
	})

	t.Run("conflicting duplicate", func(t *testing.T) {
		gateway := newTestSession(t, RoleGateway, 8)
		agentx := newTestSession(t, RoleAgentx, 8)
		first, err := gateway.Send(processReadPayload("request-1"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := agentx.Receive(first); err != nil {
			t.Fatal(err)
		}
		conflict := cloneFrame(first)
		conflict.RPC = json.RawMessage(`{"id":"request-CHANGED","method":"process/read","params":{"processId":"70000000-0000-0000-0000-000000000007","afterSeq":0,"maxBytes":1024,"waitMs":0}}`)
		if _, err := agentx.Receive(conflict); codeOf(err) != ErrorSequenceConflict {
			t.Fatalf("conflicting replay error = %v", err)
		}
	})

	t.Run("duplicate older than bounded verification history", func(t *testing.T) {
		gateway := newTestSession(t, RoleGateway, 8)
		agentx := newTestSessionWithReceiveHistory(t, RoleAgentx, 8, 1)
		first, err := gateway.Send(processReadPayload("request-1"))
		if err != nil {
			t.Fatal(err)
		}
		second, err := gateway.Send(processReadPayload("request-2"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := agentx.Receive(first); err != nil {
			t.Fatal(err)
		}
		if _, err := agentx.Receive(second); err != nil {
			t.Fatal(err)
		}
		if _, err := agentx.Receive(first); codeOf(err) != ErrorSequenceConflict {
			t.Fatalf("unverifiable old duplicate error = %v", err)
		}
	})

	t.Run("ack regression", func(t *testing.T) {
		gateway := newTestSession(t, RoleGateway, 8)
		for index := 0; index < 2; index++ {
			if _, err := gateway.Send(processReadPayload(fmt.Sprintf("request-%d", index))); err != nil {
				t.Fatal(err)
			}
		}
		if err := gateway.ReceiveAck(Ack{Type: MessageTypeAck, SessionID: testSessionID, Generation: 7, Ack: 2}); err != nil {
			t.Fatal(err)
		}
		if err := gateway.ReceiveAck(Ack{Type: MessageTypeAck, SessionID: testSessionID, Generation: 7, Ack: 1}); codeOf(err) != ErrorAckRegression {
			t.Fatalf("ack regression error = %v", err)
		}
	})
}

func TestSessionJournalBoundDoesNotConsumeSendPermission(t *testing.T) {
	gateway := newTestSession(t, RoleGateway, 1)
	first, err := gateway.Send(processReadPayload("request-1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.Send(processReadPayload("request-2")); codeOf(err) != ErrorJournalFull {
		t.Fatalf("journal full error = %v", err)
	}
	if snapshot := gateway.Snapshot(); snapshot.SentThrough != 1 || snapshot.JournalFrames != 1 {
		t.Fatalf("failed send mutated session = %+v", snapshot)
	}
	if err := gateway.ReceiveAck(Ack{Type: MessageTypeAck, SessionID: testSessionID, Generation: 7, Ack: first.SessionSeq}); err != nil {
		t.Fatal(err)
	}
	second, err := gateway.Send(processReadPayload("request-2"))
	if err != nil {
		t.Fatal(err)
	}
	if second.SessionSeq != 2 {
		t.Fatalf("second admitted sequence = %d, want 2", second.SessionSeq)
	}
}

func TestSessionResumeReplaysExactFramesOrFailsGap(t *testing.T) {
	now := time.Unix(1_000, 0)
	t.Run("exact replay", func(t *testing.T) {
		gateway := newTestSession(t, RoleGateway, 8)
		frames := make([]Frame, 3)
		for index := range frames {
			frame, err := gateway.Send(processReadPayload(fmt.Sprintf("request-%d", index+1)))
			if err != nil {
				t.Fatal(err)
			}
			frames[index] = frame
		}
		if err := gateway.Disconnect(now); err != nil {
			t.Fatal(err)
		}
		result, err := gateway.Resume(ResumeRequest{
			GatewayInstanceID:   testGatewayID,
			SessionID:           testSessionID,
			Generation:          7,
			PeerSentThrough:     0,
			PeerReceivedThrough: 1,
		}, now.Add(29*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Replay) != 2 || !reflect.DeepEqual(result.Replay[0], frames[1]) || !reflect.DeepEqual(result.Replay[1], frames[2]) {
			t.Fatalf("resume replay = %+v, want frames 2 and 3", result.Replay)
		}
		if result.ExpectPeerFrom != 1 || gateway.Snapshot().PeerAck != 1 {
			t.Fatalf("resume result/snapshot = %+v / %+v", result, gateway.Snapshot())
		}
	})

	t.Run("missing retained range", func(t *testing.T) {
		gateway := newTestSession(t, RoleGateway, 8)
		for index := 0; index < 2; index++ {
			if _, err := gateway.Send(processReadPayload(fmt.Sprintf("request-%d", index))); err != nil {
				t.Fatal(err)
			}
		}
		if err := gateway.ReceiveAck(Ack{Type: MessageTypeAck, SessionID: testSessionID, Generation: 7, Ack: 2}); err != nil {
			t.Fatal(err)
		}
		if err := gateway.Disconnect(now); err != nil {
			t.Fatal(err)
		}
		_, err := gateway.Resume(ResumeRequest{
			GatewayInstanceID:   testGatewayID,
			SessionID:           testSessionID,
			Generation:          7,
			PeerSentThrough:     0,
			PeerReceivedThrough: 1,
		}, now.Add(time.Second))
		if codeOf(err) != ErrorResumeGap {
			t.Fatalf("missing range error = %v", err)
		}
	})

	t.Run("expired", func(t *testing.T) {
		gateway := newTestSession(t, RoleGateway, 8)
		if err := gateway.Disconnect(now); err != nil {
			t.Fatal(err)
		}
		_, err := gateway.Resume(ResumeRequest{
			GatewayInstanceID: testGatewayID,
			SessionID:         testSessionID,
			Generation:        7,
		}, now.Add(30*time.Second+time.Nanosecond))
		if codeOf(err) != ErrorResumeExpired {
			t.Fatalf("expired resume error = %v", err)
		}
	})
}

func TestRegistryFencesOldGenerationAndRejectsGatewayRestartResume(t *testing.T) {
	registry := newTestRegistry(t, testGatewayID)
	old, err := registry.Attach(testExecutorID, testSessionID, 1)
	if err != nil {
		t.Fatal(err)
	}

	const generations = 24
	var wait sync.WaitGroup
	for generation := int64(2); generation <= generations; generation++ {
		generation := generation
		wait.Add(1)
		go func() {
			defer wait.Done()
			sessionID := fmt.Sprintf("%08x-0000-0000-0000-%012x", generation, generation)
			_, _ = registry.Attach(testExecutorID, sessionID, generation)
		}()
	}
	wait.Wait()
	current, found := registry.Current(testExecutorID)
	if !found || current.config.Generation != generations {
		t.Fatalf("current generation = %v, found=%t", current, found)
	}
	if snapshot := old.Snapshot(); snapshot.State != SessionFenced || codeOf(snapshot.TerminalError) != ErrorStaleGeneration {
		t.Fatalf("old session after generation race = %+v", snapshot)
	}
	if _, err := old.Send(processReadPayload("late")); codeOf(err) != ErrorStaleGeneration {
		t.Fatalf("old generation send error = %v", err)
	}
	registry.mu.Lock()
	_, staleSessionRetained := registry.bySession[testSessionID]
	registeredSessionCount := len(registry.bySession)
	registry.mu.Unlock()
	if staleSessionRetained || registeredSessionCount != 1 {
		t.Fatalf("registry session index retained stale generation: stale=%t count=%d", staleSessionRetained, registeredSessionCount)
	}

	otherGateway := "40000000-0000-0000-0000-000000000004"
	restarted := newTestRegistry(t, otherGateway)
	_, _, err = restarted.Resume(testExecutorID, ResumeCursor{
		GatewayInstanceID: testGatewayID,
		SessionID:         current.config.SessionID,
		Generation:        current.config.Generation,
	}, time.Now())
	if codeOf(err) != ErrorResumeRejected {
		t.Fatalf("cross-process resume error = %v", err)
	}
}

func newTestSession(t *testing.T, role Role, maxFrames int) *Session {
	return newTestSessionWithReceiveHistory(t, role, maxFrames, 32)
}

func newTestSessionWithReceiveHistory(t *testing.T, role Role, maxFrames, receiveHistory int) *Session {
	t.Helper()
	session, err := NewSession(SessionConfig{
		Role:                    role,
		GatewayInstanceID:       testGatewayID,
		ExecutorID:              testExecutorID,
		SessionID:               testSessionID,
		Generation:              7,
		WireLimits:              testWireLimits(),
		MaxUnackedFrames:        maxFrames,
		MaxJournalBytes:         8 * testWireLimits().MaxFrameBytes,
		MaxReceiveHistoryFrames: receiveHistory,
		ResumeWindow:            30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func newTestRegistry(t *testing.T, gatewayID string) *Registry {
	t.Helper()
	registry, err := NewRegistry(gatewayID, RegistryConfig{
		WireLimits:              testWireLimits(),
		MaxUnackedFrames:        32,
		MaxJournalBytes:         32 * testWireLimits().MaxFrameBytes,
		MaxReceiveHistoryFrames: 32,
		ResumeWindow:            30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func processReadPayload(id string) Payload {
	context := testRoutingContext()
	return Payload{
		Type:    MessageTypeRPC,
		Context: &context,
		RPC: json.RawMessage(fmt.Sprintf(
			`{"id":%q,"method":"process/read","params":{"processId":"70000000-0000-0000-0000-000000000007","afterSeq":0,"maxBytes":1024,"waitMs":0}}`,
			id,
		)),
	}
}

func testRoutingContext() RoutingContext {
	return RoutingContext{
		WorkspaceID:          "40000000-0000-0000-0000-000000000004",
		RunID:                "41000000-0000-0000-0000-000000000004",
		RunAttemptID:         "42000000-0000-0000-0000-000000000004",
		RunAttemptGeneration: 3,
		ExecutionID:          "50000000-0000-0000-0000-000000000005",
		OperationID:          "51000000-0000-0000-0000-000000000005",
		EnvID:                "60000000-0000-0000-0000-000000000006",
		MutationKey:          "61000000-0000-0000-0000-000000000006",
	}
}
