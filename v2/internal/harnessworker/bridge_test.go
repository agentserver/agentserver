package harnessworker

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/codexwire"
)

type fakeDynamicCaller struct {
	call func(context.Context, DynamicCall) (DynamicToolResult, error)
}

func (f *fakeDynamicCaller) CallDynamicTool(ctx context.Context, call DynamicCall) (DynamicToolResult, error) {
	return f.call(ctx, call)
}

func TestDynamicBridgeMapsCallbackAndUsesResponseWriteAsCleanup(t *testing.T) {
	calls := make(chan DynamicCall, 1)
	caller := &fakeDynamicCaller{call: func(_ context.Context, call DynamicCall) (DynamicToolResult, error) {
		calls <- call
		return DynamicToolResult{ContentItems: []InputTextContent{{Type: "inputText", Text: "done"}}, Success: true}, nil
	}}
	bridge, err := NewDynamicBridge(caller, 4, DefaultLimits().MaxArgumentBytes)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bridge.Close(nil) })
	scope := RunScope{RunID: "run-1", ThreadID: "thread-1", TurnID: "turn-1", RunAttemptGeneration: 3}
	message := mustCodexMessage(t, `{"id":41,"method":"item/tool/call","params":{"threadId":"thread-1","turnId":"turn-1","callId":"call-1","namespace":"executor","tool":"approved_echo","arguments":{"message":"hello"}}}`)
	if err := bridge.HandleToolCall(t.Context(), message, scope); err != nil {
		t.Fatal(err)
	}
	call := <-calls
	if call.RunID != scope.RunID || call.ThreadID != scope.ThreadID || call.TurnID != scope.TurnID ||
		call.RunAttemptGeneration != scope.RunAttemptGeneration || call.CallID != "call-1" ||
		call.Namespace != "executor" || call.Tool != "approved_echo" || string(call.Arguments) != `{"message":"hello"}` {
		t.Fatalf("mapped dynamic call = %+v", call)
	}
	event := waitBridgeEvent(t, bridge)
	if event.Kind != BridgeResultReady || event.CallID != "call-1" || event.Err != nil {
		t.Fatalf("bridge event = %+v", event)
	}
	lease, ok := bridge.ClaimResponse("call-1")
	if !ok || string(lease.RequestID) != "41" || lease.CallID != "call-1" || !lease.Result.Success || lease.Result.ContentItems[0].Text != "done" {
		t.Fatalf("response lease = %+v/%v", lease, ok)
	}
	lease.Result.ContentItems[0].Text = "mutated"
	if err := bridge.ResponseWritten("call-1"); err != nil {
		t.Fatal(err)
	}
	if bridge.Outstanding() != 0 {
		t.Fatalf("outstanding callbacks after response write = %d", bridge.Outstanding())
	}
	if _, ok := bridge.ClaimResponse("call-1"); ok {
		t.Fatal("response was claimable twice")
	}
}

func TestDynamicBridgeTurnTerminalCancelsPendingMCPWithoutResponse(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan error, 1)
	caller := &fakeDynamicCaller{call: func(ctx context.Context, _ DynamicCall) (DynamicToolResult, error) {
		close(started)
		<-ctx.Done()
		cancelled <- context.Cause(ctx)
		return DynamicToolResult{}, ctx.Err()
	}}
	bridge, err := NewDynamicBridge(caller, 2, DefaultLimits().MaxArgumentBytes)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bridge.Close(nil) })
	scope := RunScope{RunID: "run-2", ThreadID: "thread-2", TurnID: "turn-2", RunAttemptGeneration: 4}
	message := mustCodexMessage(t, `{"id":"callback-2","method":"item/tool/call","params":{"threadId":"thread-2","turnId":"turn-2","callId":"call-2","namespace":"executor","tool":"wait","arguments":{}}}`)
	if err := bridge.HandleToolCall(t.Context(), message, scope); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, started, "fake MCP call")
	if got := bridge.TurnTerminal("turn-2"); !reflect.DeepEqual(got, []string{"call-2"}) {
		t.Fatalf("terminal cancellations = %v", got)
	}
	select {
	case cause := <-cancelled:
		if cause == nil || !strings.Contains(cause.Error(), "terminal") {
			t.Fatalf("MCP cancellation cause = %v", cause)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("terminal did not cancel MCP call")
	}
	if bridge.Outstanding() != 0 {
		t.Fatalf("outstanding callbacks after terminal = %d", bridge.Outstanding())
	}
	select {
	case event := <-bridge.Events():
		t.Fatalf("terminal-cleaned callback emitted late event: %+v", event)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestDynamicBridgeResultTerminalRaceHasOneWinner(t *testing.T) {
	ready := make(chan struct{})
	release := make(chan struct{})
	caller := &fakeDynamicCaller{call: func(context.Context, DynamicCall) (DynamicToolResult, error) {
		close(ready)
		<-release
		return DynamicToolResult{ContentItems: []InputTextContent{{Type: "inputText", Text: "result"}}, Success: true}, nil
	}}
	bridge, err := NewDynamicBridge(caller, 2, DefaultLimits().MaxArgumentBytes)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bridge.Close(nil) })
	scope := RunScope{RunID: "run-3", ThreadID: "thread-3", TurnID: "turn-3", RunAttemptGeneration: 5}
	message := mustCodexMessage(t, `{"id":3,"method":"item/tool/call","params":{"threadId":"thread-3","turnId":"turn-3","callId":"call-3","namespace":"executor","tool":"race","arguments":{}}}`)
	if err := bridge.HandleToolCall(t.Context(), message, scope); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, ready, "racing MCP call")
	close(release)
	event := waitBridgeEvent(t, bridge)
	if event.Kind != BridgeResultReady {
		t.Fatalf("race event = %+v", event)
	}
	if got := bridge.TurnTerminal("turn-3"); !reflect.DeepEqual(got, []string{"call-3"}) {
		t.Fatalf("terminal did not win unclaimed result: %v", got)
	}
	if _, ok := bridge.ClaimResponse("call-3"); ok {
		t.Fatal("terminal-lost response remained claimable")
	}

	second := mustCodexMessage(t, `{"id":4,"method":"item/tool/call","params":{"threadId":"thread-3","turnId":"turn-3","callId":"call-4","namespace":"executor","tool":"race","arguments":{}}}`)
	caller.call = func(context.Context, DynamicCall) (DynamicToolResult, error) {
		return DynamicToolResult{ContentItems: []InputTextContent{{Type: "inputText", Text: "claimed"}}, Success: true}, nil
	}
	if err := bridge.HandleToolCall(t.Context(), second, scope); err != nil {
		t.Fatal(err)
	}
	if event := waitBridgeEvent(t, bridge); event.CallID != "call-4" {
		t.Fatalf("second race event = %+v", event)
	}
	if _, ok := bridge.ClaimResponse("call-4"); !ok {
		t.Fatal("ready response could not be claimed")
	}
	if got := bridge.TurnTerminal("turn-3"); len(got) != 0 {
		t.Fatalf("terminal cancelled already claimed response: %v", got)
	}
	if bridge.Outstanding() != 1 {
		t.Fatalf("claimed response outstanding = %d, want 1", bridge.Outstanding())
	}
	if err := bridge.ResponseWritten("call-4"); err != nil {
		t.Fatal(err)
	}
	if bridge.Outstanding() != 0 {
		t.Fatalf("claimed response did not clean up: %d", bridge.Outstanding())
	}
}

func TestDynamicBridgeFailureWaitsForTerminalAndNeverBecomesResponse(t *testing.T) {
	caller := &fakeDynamicCaller{call: func(context.Context, DynamicCall) (DynamicToolResult, error) {
		return DynamicToolResult{}, errors.New("MCP transport lost")
	}}
	bridge, err := NewDynamicBridge(caller, 1, DefaultLimits().MaxArgumentBytes)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bridge.Close(nil) })
	scope := RunScope{RunID: "run-4", ThreadID: "thread-4", TurnID: "turn-4", RunAttemptGeneration: 6}
	message := mustCodexMessage(t, `{"id":9,"method":"item/tool/call","params":{"threadId":"thread-4","turnId":"turn-4","callId":"call-failed","namespace":"executor","tool":"fail","arguments":{}}}`)
	if err := bridge.HandleToolCall(t.Context(), message, scope); err != nil {
		t.Fatal(err)
	}
	event := waitBridgeEvent(t, bridge)
	if event.Kind != BridgeCallFailed || event.Err == nil || !strings.Contains(event.Err.Error(), "transport lost") {
		t.Fatalf("failure event = %+v", event)
	}
	if _, ok := bridge.ClaimResponse("call-failed"); ok {
		t.Fatal("failed MCP call became an app-server response")
	}
	if bridge.Outstanding() != 1 {
		t.Fatalf("failed callback outstanding = %d, want terminal cleanup", bridge.Outstanding())
	}
	if got := bridge.TurnTerminal("turn-4"); !reflect.DeepEqual(got, []string{"call-failed"}) {
		t.Fatalf("failed callback terminal cleanup = %v", got)
	}
}

func TestDynamicBridgeRejectsMalformedScopeDuplicatesAndBounds(t *testing.T) {
	block := make(chan struct{})
	started := make(chan struct{}, 1)
	var calls atomic.Int64
	caller := &fakeDynamicCaller{call: func(ctx context.Context, _ DynamicCall) (DynamicToolResult, error) {
		calls.Add(1)
		started <- struct{}{}
		select {
		case <-block:
			return DynamicToolResult{ContentItems: []InputTextContent{{Type: "inputText", Text: "ok"}}, Success: true}, nil
		case <-ctx.Done():
			return DynamicToolResult{}, ctx.Err()
		}
	}}
	bridge, err := NewDynamicBridge(caller, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bridge.Close(nil) })
	scope := RunScope{RunID: "run-5", ThreadID: "thread-5", TurnID: "turn-5", RunAttemptGeneration: 7}
	valid := mustCodexMessage(t, `{"id":1,"method":"item/tool/call","params":{"threadId":"thread-5","turnId":"turn-5","callId":"call-5","namespace":"executor","tool":"wait","arguments":{}}}`)
	if err := bridge.HandleToolCall(t.Context(), valid, scope); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, started, "bounded dynamic call")
	for _, test := range []struct {
		name    string
		message codexwire.Message
		want    string
	}{
		{"bound", mustCodexMessage(t, `{"id":2,"method":"item/tool/call","params":{"threadId":"thread-5","turnId":"turn-5","callId":"call-6","namespace":"executor","tool":"wait","arguments":{}}}`), "limit is 1"},
		{"duplicate", valid, "limit is 1"},
		{"wrong turn", mustCodexMessage(t, `{"id":3,"method":"item/tool/call","params":{"threadId":"thread-5","turnId":"other","callId":"call-7","namespace":"executor","tool":"wait","arguments":{}}}`), "does not match"},
		{"unknown param", mustCodexMessage(t, `{"id":4,"method":"item/tool/call","params":{"threadId":"thread-5","turnId":"turn-5","callId":"call-8","namespace":"executor","tool":"wait","arguments":{},"endpoint":"evil"}}`), "unknown field"},
		{"argument bound", mustCodexMessage(t, `{"id":5,"method":"item/tool/call","params":{"threadId":"thread-5","turnId":"turn-5","callId":"call-9","namespace":"executor","tool":"wait","arguments":{"x":1}}}`), "arguments are 7 bytes, limit is 2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := bridge.HandleToolCall(t.Context(), test.message, scope); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("HandleToolCall() error = %v, want %q", err, test.want)
			}
		})
	}
	if calls.Load() != 1 {
		t.Fatalf("MCP calls after rejected callbacks = %d, want 1", calls.Load())
	}
	if got := bridge.TurnTerminal("turn-5"); !reflect.DeepEqual(got, []string{"call-5"}) {
		t.Fatalf("cleanup = %v", got)
	}
}

func TestDynamicBridgeCorrelatesRequestIDsByJSONValue(t *testing.T) {
	started := make(chan struct{}, 1)
	var calls atomic.Int64
	caller := &fakeDynamicCaller{call: func(ctx context.Context, _ DynamicCall) (DynamicToolResult, error) {
		calls.Add(1)
		started <- struct{}{}
		<-ctx.Done()
		return DynamicToolResult{}, ctx.Err()
	}}
	bridge, err := NewDynamicBridge(caller, 2, DefaultLimits().MaxArgumentBytes)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bridge.Close(nil) })
	scope := RunScope{RunID: "run-id", ThreadID: "thread-id", TurnID: "turn-id", RunAttemptGeneration: 1}
	first := mustCodexMessage(t, `{"id":"callback","method":"item/tool/call","params":{"threadId":"thread-id","turnId":"turn-id","callId":"call-id-1","namespace":"executor","tool":"wait","arguments":{}}}`)
	if err := bridge.HandleToolCall(t.Context(), first, scope); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, started, "first dynamic call")

	// The second ID has a different raw JSON spelling but decodes to the same
	// string value as the first ID.
	second := mustCodexMessage(t, `{"id":"\u0063allback","method":"item/tool/call","params":{"threadId":"thread-id","turnId":"turn-id","callId":"call-id-2","namespace":"executor","tool":"wait","arguments":{}}}`)
	if err := bridge.HandleToolCall(t.Context(), second, scope); err == nil || !strings.Contains(err.Error(), "already outstanding") {
		t.Fatalf("equivalent request ID error = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("MCP calls after equivalent request ID = %d, want 1", calls.Load())
	}
	if got := bridge.TurnTerminal("turn-id"); !reflect.DeepEqual(got, []string{"call-id-1"}) {
		t.Fatalf("terminal cleanup = %v", got)
	}
}

func mustCodexMessage(t *testing.T, frame string) codexwire.Message {
	t.Helper()
	message, err := codexwire.Parse([]byte(frame))
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func waitBridgeEvent(t *testing.T, bridge *DynamicBridge) BridgeEvent {
	t.Helper()
	select {
	case event := <-bridge.Events():
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for dynamic bridge event")
		return BridgeEvent{}
	}
}

func TestParseDynamicToolCallRequiresRequestMethod(t *testing.T) {
	notification := mustCodexMessage(t, `{"method":"item/tool/call","params":{}}`)
	_, _, err := parseDynamicToolCall(notification, RunScope{RunID: "run", ThreadID: "thread", TurnID: "turn", RunAttemptGeneration: 1})
	if err == nil || !strings.Contains(err.Error(), "not an item/tool/call request") {
		t.Fatalf("notification parse error = %v", err)
	}
	response := mustCodexMessage(t, `{"id":1,"result":{}}`)
	_, _, err = parseDynamicToolCall(response, RunScope{RunID: "run", ThreadID: "thread", TurnID: "turn", RunAttemptGeneration: 1})
	if err == nil || !strings.Contains(err.Error(), "not an item/tool/call request") {
		t.Fatalf("response parse error = %v", err)
	}
}

func TestDynamicBridgeRejectsUnboundedConfigAndRequestIDs(t *testing.T) {
	caller := &fakeDynamicCaller{call: func(context.Context, DynamicCall) (DynamicToolResult, error) {
		return DynamicToolResult{}, nil
	}}
	if _, err := NewDynamicBridge(caller, maxConfiguredPendingCalls+1, 1); err == nil || !strings.Contains(err.Error(), "hard maximum") {
		t.Fatalf("pending-call hard bound error = %v", err)
	}
	if _, err := NewDynamicBridge(caller, 1, maxConfiguredPayloadBytes+1); err == nil || !strings.Contains(err.Error(), "hard maximum") {
		t.Fatalf("argument hard bound error = %v", err)
	}
	if _, err := canonicalRequestIDKey(json.RawMessage(`""`)); err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("empty request ID error = %v", err)
	}
	longID, err := json.Marshal(strings.Repeat("x", maxIdentityBytes+1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalRequestIDKey(longID); err == nil || !strings.Contains(err.Error(), "limit is") {
		t.Fatalf("long request ID error = %v", err)
	}
}
