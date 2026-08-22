package harnessworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/codexwire"
)

const (
	runnerTestThreadID  = "thread-runner-1"
	runnerTestSessionID = "session-runner-1"
	runnerTestTurnID    = "turn-runner-1"
	runnerTestCWD       = "/empty-workspace"
	runnerTestRollout   = "/codex-home/sessions/runner-rollout.jsonl"
)

func TestAppServerRunnerDrivesDynamicToolLifecycleAndWriteBarrier(t *testing.T) {
	catalog := runnerTestCatalog(t)
	calls := make(chan DynamicCall, 1)
	caller := &fakeDynamicCaller{call: func(_ context.Context, call DynamicCall) (DynamicToolResult, error) {
		calls <- call
		return DynamicToolResult{
			ContentItems: []InputTextContent{{Type: "inputText", Text: "approved echo: hello"}},
			Success:      true,
		}, nil
	}}
	lifecycle := &recordingAppServerLifecycleSink{}
	options := DefaultAppServerRunnerOptions()
	options.LifecycleSink = lifecycle
	runner, bridge, server := newRunnerFixture(t, caller, catalog, options)

	serverDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := serveRunnerStartLifecycle(ctx, server, catalog); err != nil {
			serverDone <- err
			return
		}
		if err := server.Send(map[string]any{
			"id":     "callback-runner-1",
			"method": "item/tool/call",
			"params": map[string]any{
				"threadId":  runnerTestThreadID,
				"turnId":    runnerTestTurnID,
				"callId":    "call-runner-1",
				"namespace": "executor",
				"tool":      "approved_echo",
				"arguments": map[string]any{"message": "hello"},
			},
		}); err != nil {
			serverDone <- fmt.Errorf("send dynamic callback: %w", err)
			return
		}
		response, err := receiveRunnerMessage(ctx, server, codexwire.KindResponse, "", `"callback-runner-1"`)
		if err != nil {
			serverDone <- err
			return
		}
		var dynamicResult DynamicToolResult
		if err := response.DecodeResult(&dynamicResult); err != nil {
			serverDone <- err
			return
		}
		if !dynamicResult.Success || len(dynamicResult.ContentItems) != 1 || dynamicResult.ContentItems[0].Text != "approved echo: hello" {
			serverDone <- fmt.Errorf("dynamic response = %+v", dynamicResult)
			return
		}
		if err := server.Send(map[string]any{
			"method": "item/completed",
			"params": map[string]any{"threadId": runnerTestThreadID, "turnId": runnerTestTurnID},
		}); err != nil {
			serverDone <- err
			return
		}
		serverDone <- sendRunnerTerminal(server, "completed")
	}()

	result, err := runner.Run(t.Context(), runnerStartRequest(catalog))
	if err != nil {
		t.Fatal(err)
	}
	if serverErr := <-serverDone; serverErr != nil {
		t.Fatal(serverErr)
	}
	if result.Thread.Thread.ID != runnerTestThreadID || result.Turn.ID != runnerTestTurnID ||
		result.Terminal.ThreadID != runnerTestThreadID || result.Terminal.Turn.Status != "completed" {
		t.Fatalf("runner result = %+v", result)
	}
	call := <-calls
	if call.RunID != "run-runner-1" || call.RunAttemptGeneration != 7 ||
		call.ThreadID != runnerTestThreadID || call.TurnID != runnerTestTurnID || call.CallID != "call-runner-1" {
		t.Fatalf("dynamic call scope = %+v", call)
	}
	if bridge.Outstanding() != 0 {
		t.Fatalf("bridge retained %d callbacks after response write", bridge.Outstanding())
	}
	if got, want := lifecycle.snapshot(), []string{
		"thread:" + runnerTestThreadID + ":false",
		"turn:" + runnerTestThreadID + ":" + runnerTestTurnID,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("lifecycle calls = %v, want %v", got, want)
	}
	if got, want := collectRunnerEventMethods(runner), []string{
		"thread/started", "turn/started", "item/completed", "turn/completed",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("event methods = %v, want %v", got, want)
	}
}

func TestAppServerRunnerInterruptsWhenTurnAcceptanceAuthorityFails(t *testing.T) {
	catalog := runnerTestCatalog(t)
	var calls atomic.Int64
	caller := &fakeDynamicCaller{call: func(context.Context, DynamicCall) (DynamicToolResult, error) {
		calls.Add(1)
		return DynamicToolResult{}, nil
	}}
	lifecycleErr := errors.New("synthetic core turn acceptance failure")
	lifecycle := &recordingAppServerLifecycleSink{turnErr: lifecycleErr}
	options := DefaultAppServerRunnerOptions()
	options.InterruptGrace = time.Second
	options.LifecycleSink = lifecycle
	runner, bridge, server := newRunnerFixture(t, caller, catalog, options)

	serverDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := serveRunnerStartLifecycle(ctx, server, catalog); err != nil {
			serverDone <- err
			return
		}
		interrupt, err := receiveRunnerMessage(ctx, server, codexwire.KindRequest, "turn/interrupt", "4")
		if err != nil {
			serverDone <- err
			return
		}
		var params appServerTurnInterruptParams
		if err := interrupt.DecodeParams(&params); err != nil {
			serverDone <- err
			return
		}
		if params.ThreadID != runnerTestThreadID || params.TurnID != runnerTestTurnID {
			serverDone <- fmt.Errorf("interrupt params = %+v", params)
			return
		}
		if err := server.Send(map[string]any{"id": 4, "result": map[string]any{}}); err != nil {
			serverDone <- err
			return
		}
		serverDone <- sendRunnerTerminal(server, "interrupted")
	}()

	result, err := runner.Run(t.Context(), runnerStartRequest(catalog))
	if !errors.Is(err, lifecycleErr) {
		t.Fatalf("runner error = %v, want lifecycle failure", err)
	}
	if result.Terminal.Turn.Status != "interrupted" {
		t.Fatalf("runner terminal = %+v", result.Terminal)
	}
	if serverErr := <-serverDone; serverErr != nil {
		t.Fatal(serverErr)
	}
	if calls.Load() != 0 || bridge.Outstanding() != 0 {
		t.Fatalf("failed turn acceptance dispatched %d calls and retained %d", calls.Load(), bridge.Outstanding())
	}
}

func TestAppServerRunnerCancelInterruptsTurnAndPendingMCP(t *testing.T) {
	catalog := runnerTestCatalog(t)
	callStarted := make(chan struct{})
	callCancelled := make(chan error, 1)
	caller := &fakeDynamicCaller{call: func(ctx context.Context, _ DynamicCall) (DynamicToolResult, error) {
		close(callStarted)
		<-ctx.Done()
		callCancelled <- context.Cause(ctx)
		return DynamicToolResult{}, ctx.Err()
	}}
	options := DefaultAppServerRunnerOptions()
	options.InterruptGrace = time.Second
	runner, bridge, server := newRunnerFixture(t, caller, catalog, options)

	serverDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := serveRunnerStartLifecycle(ctx, server, catalog); err != nil {
			serverDone <- err
			return
		}
		if err := server.Send(map[string]any{
			"id":     81,
			"method": "item/tool/call",
			"params": map[string]any{
				"threadId":  runnerTestThreadID,
				"turnId":    runnerTestTurnID,
				"callId":    "call-pending-cancel",
				"namespace": "executor",
				"tool":      "approved_echo",
				"arguments": map[string]any{"message": "wait"},
			},
		}); err != nil {
			serverDone <- err
			return
		}
		interrupt, err := receiveRunnerMessage(ctx, server, codexwire.KindRequest, "turn/interrupt", "4")
		if err != nil {
			serverDone <- err
			return
		}
		var params appServerTurnInterruptParams
		if err := interrupt.DecodeParams(&params); err != nil {
			serverDone <- err
			return
		}
		if params.ThreadID != runnerTestThreadID || params.TurnID != runnerTestTurnID {
			serverDone <- fmt.Errorf("interrupt params = %+v", params)
			return
		}
		if err := server.Send(map[string]any{"id": 4, "result": map[string]any{}}); err != nil {
			serverDone <- err
			return
		}
		serverDone <- sendRunnerTerminal(server, "interrupted")
	}()

	runCtx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan struct {
		result AppServerRunResult
		err    error
	}, 1)
	go func() {
		result, err := runner.Run(runCtx, runnerStartRequest(catalog))
		runDone <- struct {
			result AppServerRunResult
			err    error
		}{result: result, err: err}
	}()
	waitSignal(t, callStarted, "runner dynamic call")
	cancelRun()
	outcome := <-runDone
	if !errors.Is(outcome.err, context.Canceled) {
		t.Fatalf("cancelled runner error = %v", outcome.err)
	}
	if outcome.result.Terminal.Turn.Status != "interrupted" {
		t.Fatalf("cancelled runner terminal = %+v", outcome.result.Terminal)
	}
	if serverErr := <-serverDone; serverErr != nil {
		t.Fatal(serverErr)
	}
	select {
	case cause := <-callCancelled:
		if !errors.Is(cause, context.Canceled) {
			t.Fatalf("MCP cancellation cause = %v", cause)
		}
	case <-time.After(time.Second):
		t.Fatal("runner cancel did not cancel pending MCP call")
	}
	if bridge.Outstanding() != 0 {
		t.Fatalf("cancelled runner retained %d callbacks", bridge.Outstanding())
	}
}

func TestAppServerRunnerRejectsUnknownReverseRequestAndInterrupts(t *testing.T) {
	catalog := runnerTestCatalog(t)
	var calls atomic.Int64
	caller := &fakeDynamicCaller{call: func(context.Context, DynamicCall) (DynamicToolResult, error) {
		calls.Add(1)
		return DynamicToolResult{}, nil
	}}
	options := DefaultAppServerRunnerOptions()
	options.InterruptGrace = time.Second
	runner, bridge, server := newRunnerFixture(t, caller, catalog, options)

	serverDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := serveRunnerStartLifecycle(ctx, server, catalog); err != nil {
			serverDone <- err
			return
		}
		if err := server.Send(map[string]any{
			"id":     "host-request",
			"method": "item/commandExecution/requestApproval",
			"params": map[string]any{},
		}); err != nil {
			serverDone <- err
			return
		}
		if _, err := receiveRunnerMessage(ctx, server, codexwire.KindRequest, "turn/interrupt", "4"); err != nil {
			serverDone <- err
			return
		}
		if err := server.Send(map[string]any{"id": 4, "result": map[string]any{}}); err != nil {
			serverDone <- err
			return
		}
		serverDone <- sendRunnerTerminal(server, "interrupted")
	}()

	result, err := runner.Run(t.Context(), runnerStartRequest(catalog))
	if err == nil || !strings.Contains(err.Error(), "not allowlisted") {
		t.Fatalf("unknown reverse request error = %v", err)
	}
	if result.Terminal.Turn.Status != "interrupted" {
		t.Fatalf("unknown reverse request terminal = %+v", result.Terminal)
	}
	if serverErr := <-serverDone; serverErr != nil {
		t.Fatal(serverErr)
	}
	if calls.Load() != 0 {
		t.Fatalf("unknown reverse request reached MCP %d times", calls.Load())
	}
	if bridge.Outstanding() != 0 {
		t.Fatalf("unknown reverse request retained %d callbacks", bridge.Outstanding())
	}
}

func TestAppServerRunnerUsesNativeResumeWithoutDynamicToolOverride(t *testing.T) {
	catalog := runnerTestCatalog(t)
	caller := &fakeDynamicCaller{call: func(context.Context, DynamicCall) (DynamicToolResult, error) {
		return DynamicToolResult{}, errors.New("resume fixture must not call MCP")
	}}
	runner, _, server := newRunnerFixture(t, caller, catalog, DefaultAppServerRunnerOptions())

	serverDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := serveRunnerInitialize(ctx, server); err != nil {
			serverDone <- err
			return
		}
		resume, err := receiveRunnerMessage(ctx, server, codexwire.KindRequest, "thread/resume", "2")
		if err != nil {
			serverDone <- err
			return
		}
		var params map[string]json.RawMessage
		if err := resume.DecodeParams(&params); err != nil {
			serverDone <- err
			return
		}
		if _, leaked := params["dynamicTools"]; leaked {
			serverDone <- errors.New("thread/resume included a dynamicTools override")
			return
		}
		if string(params["threadId"]) != `"`+runnerTestThreadID+`"` ||
			string(params["path"]) != `"`+runnerTestRollout+`"` || string(params["excludeTurns"]) != "true" {
			serverDone <- fmt.Errorf("thread/resume params = %s", resume.Params)
			return
		}
		if err := sendRunnerThreadResponse(server, 2); err != nil {
			serverDone <- err
			return
		}
		turn, err := receiveRunnerMessage(ctx, server, codexwire.KindRequest, "turn/start", "3")
		if err != nil {
			serverDone <- err
			return
		}
		var turnParams appServerTurnStartParams
		if err := turn.DecodeParams(&turnParams); err != nil {
			serverDone <- err
			return
		}
		if turnParams.CWD != runnerTestCWD || turnParams.ApprovalPolicy != "never" || len(turnParams.Environments) != 0 {
			serverDone <- fmt.Errorf("resumed turn params = %+v", turnParams)
			return
		}
		if err := sendRunnerTurnStarted(server); err != nil {
			serverDone <- err
			return
		}
		serverDone <- sendRunnerTerminal(server, "completed")
	}()

	request := runnerStartRequest(catalog)
	request.Start = nil
	request.Resume = &AppServerThreadResume{
		ThreadID:                runnerTestThreadID,
		RolloutPath:             runnerTestRollout,
		CWD:                     runnerTestCWD,
		CheckpointCatalogDigest: catalog.Digest(),
	}
	result, err := runner.Run(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if serverErr := <-serverDone; serverErr != nil {
		t.Fatal(serverErr)
	}
	if !result.Resumed || result.Thread.Thread.ID != runnerTestThreadID || result.Terminal.Turn.Status != "completed" {
		t.Fatalf("resume result = %+v", result)
	}
}

func TestAppServerRunnerResponseWriteFailureIsTerminalAndDoesNotRetry(t *testing.T) {
	catalog := runnerTestCatalog(t)
	var calls atomic.Int64
	caller := &fakeDynamicCaller{call: func(context.Context, DynamicCall) (DynamicToolResult, error) {
		calls.Add(1)
		return DynamicToolResult{ContentItems: []InputTextContent{{Type: "inputText", Text: "result"}}, Success: true}, nil
	}}
	worker, server, closePair := newRunnerPeerPair(t)
	t.Cleanup(closePair)
	failing := &failResponseTransport{AppServerTransport: worker, requestID: `"callback-write-fail"`}
	bridge, err := NewDynamicBridge(caller, 4, DefaultLimits().MaxArgumentBytes)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewAppServerRunner(failing, bridge, DefaultAppServerRunnerOptions())
	if err != nil {
		t.Fatal(err)
	}

	serverDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := serveRunnerStartLifecycle(ctx, server, catalog); err != nil {
			serverDone <- err
			return
		}
		serverDone <- server.Send(map[string]any{
			"id":     "callback-write-fail",
			"method": "item/tool/call",
			"params": map[string]any{
				"threadId":  runnerTestThreadID,
				"turnId":    runnerTestTurnID,
				"callId":    "call-write-fail",
				"namespace": "executor",
				"tool":      "approved_echo",
				"arguments": map[string]any{"message": "hello"},
			},
		})
	}()

	_, err = runner.Run(t.Context(), runnerStartRequest(catalog))
	if err == nil || !strings.Contains(err.Error(), "synthetic response write failure") {
		t.Fatalf("response write error = %v", err)
	}
	if serverErr := <-serverDone; serverErr != nil {
		t.Fatal(serverErr)
	}
	if calls.Load() != 1 {
		t.Fatalf("response write failure called MCP %d times, want one", calls.Load())
	}
	if bridge.Outstanding() != 0 {
		t.Fatalf("response write failure retained %d callbacks", bridge.Outstanding())
	}
}

func TestAppServerRunnerEventOverflowInterruptsWithoutDispatch(t *testing.T) {
	catalog := runnerTestCatalog(t)
	var calls atomic.Int64
	caller := &fakeDynamicCaller{call: func(context.Context, DynamicCall) (DynamicToolResult, error) {
		calls.Add(1)
		return DynamicToolResult{}, nil
	}}
	options := DefaultAppServerRunnerOptions()
	options.EventBuffer = 2
	options.InterruptGrace = time.Second
	runner, bridge, server := newRunnerFixture(t, caller, catalog, options)

	serverDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := serveRunnerStartLifecycle(ctx, server, catalog); err != nil {
			serverDone <- err
			return
		}
		// thread/started and turn/started fill the two-slot sink. This third
		// notification must fail closed instead of blocking the stdio reader.
		if err := server.Send(map[string]any{
			"method": "item/started",
			"params": map[string]any{"threadId": runnerTestThreadID, "turnId": runnerTestTurnID},
		}); err != nil {
			serverDone <- err
			return
		}
		if _, err := receiveRunnerMessage(ctx, server, codexwire.KindRequest, "turn/interrupt", "4"); err != nil {
			serverDone <- err
			return
		}
		if err := server.Send(map[string]any{"id": 4, "result": map[string]any{}}); err != nil {
			serverDone <- err
			return
		}
		serverDone <- sendRunnerTerminal(server, "interrupted")
	}()

	result, err := runner.Run(t.Context(), runnerStartRequest(catalog))
	if !errors.Is(err, ErrAppServerEventBufferFull) {
		t.Fatalf("event overflow error = %v", err)
	}
	if result.Terminal.Turn.Status != "interrupted" {
		t.Fatalf("event overflow terminal = %+v", result.Terminal)
	}
	if serverErr := <-serverDone; serverErr != nil {
		t.Fatal(serverErr)
	}
	if calls.Load() != 0 || bridge.Outstanding() != 0 {
		t.Fatalf("event overflow calls/outstanding = %d/%d", calls.Load(), bridge.Outstanding())
	}
}

func TestAppServerRunnerRetainsSmallBurstByActualBytes(t *testing.T) {
	caller := &fakeDynamicCaller{call: func(context.Context, DynamicCall) (DynamicToolResult, error) {
		return DynamicToolResult{}, nil
	}}
	bridge, err := NewDynamicBridge(caller, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	options := DefaultAppServerRunnerOptions()
	options.EventBuffer = maxConfiguredAppServerEvents
	options.MaxEventBytes = workerMaxEventBytes
	options.MaxEventBufferBytes = 8 * 1024 * 1024
	runner, err := NewAppServerRunner(&countingAppServerTransport{}, bridge, options)
	if err != nil {
		t.Fatal(err)
	}
	state := &appServerProtocolState{runner: runner}
	const eventCount = 128
	for index := 0; index < eventCount; index++ {
		message := codexwire.Message{
			Kind: codexwire.KindNotification, Method: "item/agentMessage/delta",
			Params: json.RawMessage(`{"delta":"hello"}`),
		}
		if err := state.emit(message); err != nil {
			t.Fatalf("emit small event %d: %v", index, err)
		}
	}
	close(runner.events)
	consumed := 0
	if err := runner.ConsumeEvents(func(codexwire.Message) { consumed++ }); err != nil {
		t.Fatal(err)
	}
	if consumed != eventCount {
		t.Fatalf("consumed events = %d, want %d", consumed, eventCount)
	}
	runner.eventMu.Lock()
	defer runner.eventMu.Unlock()
	if runner.retainedEventCount != 0 || runner.retainedEventBytes != 0 {
		t.Fatalf("retained events/bytes after drain = %d/%d", runner.retainedEventCount, runner.retainedEventBytes)
	}
}

func TestAppServerRunnerEventAggregateBytesRemainBounded(t *testing.T) {
	caller := &fakeDynamicCaller{call: func(context.Context, DynamicCall) (DynamicToolResult, error) {
		return DynamicToolResult{}, nil
	}}
	bridge, err := NewDynamicBridge(caller, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	message := codexwire.Message{
		Kind: codexwire.KindNotification, Method: "item/agentMessage/delta",
		Params: json.RawMessage(`{"delta":"bounded"}`),
	}
	retained := appServerMessageRetainedBytes(message)
	options := DefaultAppServerRunnerOptions()
	options.EventBuffer = maxConfiguredAppServerEvents
	options.MaxEventBytes = retained
	options.MaxEventBufferBytes = retained * 2
	runner, err := NewAppServerRunner(&countingAppServerTransport{}, bridge, options)
	if err != nil {
		t.Fatal(err)
	}
	state := &appServerProtocolState{runner: runner}
	if err := state.emit(message); err != nil {
		t.Fatal(err)
	}
	if err := state.emit(message); err != nil {
		t.Fatal(err)
	}
	if err := state.emit(message); !errors.Is(err, ErrAppServerEventBufferFull) {
		t.Fatalf("aggregate byte overflow error = %v", err)
	}
	close(runner.events)
	if err := runner.ConsumeEvents(func(codexwire.Message) {}); err != nil {
		t.Fatal(err)
	}
}

func TestAppServerRunnerOversizedEventInterruptsWithoutRetention(t *testing.T) {
	catalog := runnerTestCatalog(t)
	var calls atomic.Int64
	caller := &fakeDynamicCaller{call: func(context.Context, DynamicCall) (DynamicToolResult, error) {
		calls.Add(1)
		return DynamicToolResult{}, nil
	}}
	options := DefaultAppServerRunnerOptions()
	options.MaxEventBytes = 1024
	options.InterruptGrace = time.Second
	runner, bridge, server := newRunnerFixture(t, caller, catalog, options)

	serverDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := serveRunnerStartLifecycle(ctx, server, catalog); err != nil {
			serverDone <- err
			return
		}
		if err := server.Send(map[string]any{
			"method": "item/started",
			"params": map[string]any{
				"threadId": runnerTestThreadID,
				"turnId":   runnerTestTurnID,
				"payload":  strings.Repeat("x", 2048),
			},
		}); err != nil {
			serverDone <- err
			return
		}
		if _, err := receiveRunnerMessage(ctx, server, codexwire.KindRequest, "turn/interrupt", "4"); err != nil {
			serverDone <- err
			return
		}
		if err := server.Send(map[string]any{"id": 4, "result": map[string]any{}}); err != nil {
			serverDone <- err
			return
		}
		serverDone <- sendRunnerTerminal(server, "interrupted")
	}()

	result, err := runner.Run(t.Context(), runnerStartRequest(catalog))
	if !errors.Is(err, ErrAppServerEventTooLarge) {
		t.Fatalf("oversized event error = %v", err)
	}
	if result.Terminal.Turn.Status != "interrupted" {
		t.Fatalf("oversized event terminal = %+v", result.Terminal)
	}
	if serverErr := <-serverDone; serverErr != nil {
		t.Fatal(serverErr)
	}
	if calls.Load() != 0 || bridge.Outstanding() != 0 {
		t.Fatalf("oversized event calls/outstanding = %d/%d", calls.Load(), bridge.Outstanding())
	}
}

func TestAppServerRunnerRejectsResumeCatalogMismatchBeforeStdio(t *testing.T) {
	catalog := runnerTestCatalog(t)
	transport := &countingAppServerTransport{}
	caller := &fakeDynamicCaller{call: func(context.Context, DynamicCall) (DynamicToolResult, error) {
		return DynamicToolResult{}, errors.New("catalog mismatch must not call MCP")
	}}
	bridge, err := NewDynamicBridge(caller, 1, catalog.Limits().MaxArgumentBytes)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewAppServerRunner(transport, bridge, DefaultAppServerRunnerOptions())
	if err != nil {
		t.Fatal(err)
	}
	request := runnerStartRequest(catalog)
	request.Start = nil
	request.Resume = &AppServerThreadResume{
		ThreadID:                runnerTestThreadID,
		RolloutPath:             runnerTestRollout,
		CWD:                     runnerTestCWD,
		CheckpointCatalogDigest: strings.Repeat("0", 64),
	}
	if _, err := runner.Run(t.Context(), request); err == nil || !strings.Contains(err.Error(), "does not match verified catalog") {
		t.Fatalf("resume catalog mismatch error = %v", err)
	}
	if transport.sends.Load() != 0 {
		t.Fatalf("resume catalog mismatch wrote stdio %d times", transport.sends.Load())
	}
}

func TestAppServerRunnerRejectsUnboundedOptions(t *testing.T) {
	caller := &fakeDynamicCaller{call: func(context.Context, DynamicCall) (DynamicToolResult, error) {
		return DynamicToolResult{}, nil
	}}
	bridge, err := NewDynamicBridge(caller, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	transport := &countingAppServerTransport{}
	for _, test := range []struct {
		name   string
		mutate func(*AppServerRunnerOptions)
		want   string
	}{
		{"event count", func(options *AppServerRunnerOptions) { options.EventBuffer = maxConfiguredAppServerEvents + 1 }, "event buffer exceeds"},
		{"event bytes", func(options *AppServerRunnerOptions) { options.MaxEventBytes = maxConfiguredAppServerBufferedBytes + 1 }, "max event bytes exceeds"},
		{"event buffer bytes", func(options *AppServerRunnerOptions) {
			options.MaxEventBufferBytes = maxConfiguredAppServerBufferedBytes + 1
		}, "max event buffer bytes exceeds"},
		{"event aggregate", func(options *AppServerRunnerOptions) {
			options.MaxEventBytes = 1024
			options.MaxEventBufferBytes = 512
		}, "must not exceed max event buffer bytes"},
		{"interrupt grace", func(options *AppServerRunnerOptions) { options.InterruptGrace = maxInterruptGrace + time.Second }, "interrupt grace exceeds"},
		{"prompt bytes", func(options *AppServerRunnerOptions) { options.MaxPromptTextBytes = maxConfiguredPayloadBytes + 1 }, "prompt text bytes exceeds"},
	} {
		t.Run(test.name, func(t *testing.T) {
			options := DefaultAppServerRunnerOptions()
			test.mutate(&options)
			if _, err := NewAppServerRunner(transport, bridge, options); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("NewAppServerRunner() error = %v, want %q", err, test.want)
			}
		})
	}
}

type countingAppServerTransport struct {
	sends atomic.Int64
}

type recordingAppServerLifecycleSink struct {
	mu        sync.Mutex
	calls     []string
	threadErr error
	turnErr   error
}

func (sink *recordingAppServerLifecycleSink) SendThreadReady(_ context.Context, threadID string, resumed bool) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.calls = append(sink.calls, fmt.Sprintf("thread:%s:%t", threadID, resumed))
	return sink.threadErr
}

func (sink *recordingAppServerLifecycleSink) SendTurnAccepted(_ context.Context, threadID, turnID string) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.calls = append(sink.calls, "turn:"+threadID+":"+turnID)
	return sink.turnErr
}

func (sink *recordingAppServerLifecycleSink) snapshot() []string {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return append([]string(nil), sink.calls...)
}

func (t *countingAppServerTransport) Send(any) error {
	t.sends.Add(1)
	return nil
}

func (*countingAppServerTransport) Receive(context.Context) (codexwire.Message, error) {
	return codexwire.Message{}, errors.New("unexpected receive")
}

type failResponseTransport struct {
	AppServerTransport
	requestID string
}

func (t *failResponseTransport) Send(value any) error {
	frame, err := json.Marshal(value)
	if err != nil {
		return err
	}
	message, err := codexwire.Parse(frame)
	if err != nil {
		return err
	}
	if message.Kind == codexwire.KindResponse && string(message.ID) == t.requestID {
		return errors.New("synthetic response write failure")
	}
	return t.AppServerTransport.Send(value)
}

func newRunnerFixture(
	t *testing.T,
	caller DynamicToolCaller,
	catalog *Catalog,
	options AppServerRunnerOptions,
) (*AppServerRunner, *DynamicBridge, *codexwire.Peer) {
	t.Helper()
	worker, server, closePair := newRunnerPeerPair(t)
	t.Cleanup(closePair)
	bridge, err := NewDynamicBridge(caller, 4, catalog.Limits().MaxArgumentBytes)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewAppServerRunner(worker, bridge, options)
	if err != nil {
		t.Fatal(err)
	}
	return runner, bridge, server
}

func newRunnerPeerPair(t *testing.T) (*codexwire.Peer, *codexwire.Peer, func()) {
	t.Helper()
	workerConn, serverConn := net.Pipe()
	worker, err := codexwire.NewPeer(workerConn, workerConn, 1024*1024, 32)
	if err != nil {
		t.Fatal(err)
	}
	server, err := codexwire.NewPeer(serverConn, serverConn, 1024*1024, 32)
	if err != nil {
		workerConn.Close()
		serverConn.Close()
		t.Fatal(err)
	}
	return worker, server, func() {
		_ = workerConn.Close()
		_ = serverConn.Close()
	}
}

func runnerTestCatalog(t *testing.T) *Catalog {
	t.Helper()
	catalog, err := BuildCatalog("executor", "deterministic executor", []ToolDescriptor{{
		Name:        "approved_echo",
		Description: "return one approved message",
		InputSchema: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{"message": map[string]any{"type": "string"}},
			"required":             []any{"message"},
			"additionalProperties": false,
		},
	}}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func runnerStartRequest(catalog *Catalog) AppServerRunRequest {
	return AppServerRunRequest{
		RunID:                "run-runner-1",
		RunAttemptGeneration: 7,
		ClientInfo: AppServerClientInfo{
			Name:    "agentserver_harness_worker",
			Title:   "agentserver harness worker",
			Version: "v2-reference",
		},
		Catalog: catalog,
		Start: &AppServerThreadStart{
			Model:                 "scripted-model",
			CWD:                   runnerTestCWD,
			BaseInstructions:      "Return only the scripted result.",
			DeveloperInstructions: "Use only the frozen dynamic tools.",
		},
		UserText: "run the deterministic tool",
	}
}

func serveRunnerStartLifecycle(ctx context.Context, server *codexwire.Peer, catalog *Catalog) error {
	if err := serveRunnerInitialize(ctx, server); err != nil {
		return err
	}
	start, err := receiveRunnerMessage(ctx, server, codexwire.KindRequest, "thread/start", "2")
	if err != nil {
		return err
	}
	var params appServerThreadStartParams
	if err := start.DecodeParams(&params); err != nil {
		return err
	}
	if params.ApprovalPolicy != "never" || params.ApprovalsReviewer != "" || params.Sandbox != "read-only" || params.Ephemeral ||
		len(params.Environments) != 0 || len(params.SelectedCapabilityRoots) != 0 {
		return fmt.Errorf("thread/start profile = %+v", params)
	}
	gotTools, err := json.Marshal(params.DynamicTools)
	if err != nil {
		return err
	}
	wantTools, err := json.Marshal(catalog.DynamicTools())
	if err != nil {
		return err
	}
	if string(gotTools) != string(wantTools) {
		return fmt.Errorf("thread/start dynamic tools = %s, want %s", gotTools, wantTools)
	}
	if err := sendRunnerThreadResponse(server, 2); err != nil {
		return err
	}
	if err := server.Send(map[string]any{
		"method": "thread/started",
		"params": map[string]any{"thread": runnerThreadPayload()},
	}); err != nil {
		return err
	}
	turn, err := receiveRunnerMessage(ctx, server, codexwire.KindRequest, "turn/start", "3")
	if err != nil {
		return err
	}
	var turnParams appServerTurnStartParams
	if err := turn.DecodeParams(&turnParams); err != nil {
		return err
	}
	if turnParams.ThreadID != runnerTestThreadID || turnParams.CWD != "" || turnParams.ApprovalPolicy != "" ||
		len(turnParams.Environments) != 0 || len(turnParams.Input) != 1 || turnParams.Input[0].Type != "text" {
		return fmt.Errorf("turn/start params = %+v", turnParams)
	}
	return sendRunnerTurnStarted(server)
}

func serveRunnerInitialize(ctx context.Context, server *codexwire.Peer) error {
	initialize, err := receiveRunnerMessage(ctx, server, codexwire.KindRequest, "initialize", "1")
	if err != nil {
		return err
	}
	var params appServerInitializeParams
	if err := initialize.DecodeParams(&params); err != nil {
		return err
	}
	if !params.Capabilities.ExperimentalAPI || params.ClientInfo.Name != "agentserver_harness_worker" {
		return fmt.Errorf("initialize params = %+v", params)
	}
	if err := server.Send(map[string]any{
		"id": 1,
		"result": map[string]any{
			"userAgent":      "codex-test",
			"codexHome":      "/codex-home",
			"platformFamily": "unix",
			"platformOs":     "test",
		},
	}); err != nil {
		return err
	}
	_, err = receiveRunnerMessage(ctx, server, codexwire.KindNotification, "initialized", "")
	return err
}

func sendRunnerThreadResponse(server *codexwire.Peer, id int64) error {
	return server.Send(map[string]any{
		"id": id,
		"result": map[string]any{
			"thread":        runnerThreadPayload(),
			"model":         "scripted-model",
			"modelProvider": "scripted-provider",
			"cwd":           runnerTestCWD,
		},
	})
}

func sendRunnerTurnStarted(server *codexwire.Peer) error {
	if err := server.Send(map[string]any{
		"id": 3,
		"result": map[string]any{
			"turn": map[string]any{"id": runnerTestTurnID, "status": "inProgress", "error": nil},
		},
	}); err != nil {
		return err
	}
	return server.Send(map[string]any{
		"method": "turn/started",
		"params": map[string]any{
			"threadId": runnerTestThreadID,
			"turn":     map[string]any{"id": runnerTestTurnID, "status": "inProgress", "error": nil},
		},
	})
}

func sendRunnerTerminal(server *codexwire.Peer, status string) error {
	return server.Send(map[string]any{
		"method": "turn/completed",
		"params": map[string]any{
			"threadId": runnerTestThreadID,
			"turn":     map[string]any{"id": runnerTestTurnID, "status": status, "error": nil},
		},
	})
}

func runnerThreadPayload() map[string]any {
	return map[string]any{
		"id":        runnerTestThreadID,
		"sessionId": runnerTestSessionID,
		"ephemeral": false,
		"path":      runnerTestRollout,
		"cwd":       runnerTestCWD,
	}
}

func receiveRunnerMessage(
	ctx context.Context,
	peer *codexwire.Peer,
	kind codexwire.Kind,
	method string,
	id string,
) (codexwire.Message, error) {
	message, err := peer.Receive(ctx)
	if err != nil {
		return codexwire.Message{}, err
	}
	if message.Kind != kind {
		return codexwire.Message{}, fmt.Errorf("message kind = %s, want %s: %s", message.Kind, kind, message.Raw)
	}
	if method != "" && message.Method != method {
		return codexwire.Message{}, fmt.Errorf("message method = %q, want %q", message.Method, method)
	}
	if id != "" && string(message.ID) != id {
		return codexwire.Message{}, fmt.Errorf("message id = %s, want %s", message.ID, id)
	}
	return message, nil
}

func collectRunnerEventMethods(runner *AppServerRunner) []string {
	var methods []string
	if err := runner.ConsumeEvents(func(event codexwire.Message) {
		methods = append(methods, event.Method)
	}); err != nil {
		panic(err)
	}
	return methods
}
