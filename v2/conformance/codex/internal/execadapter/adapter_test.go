package execadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/codexwire"
	"github.com/agentserver/agentserver/v2/internal/runtimelock"
)

func TestOuterProcessProfileExcludesSignal(t *testing.T) {
	want := []string{"process/start", "process/read", "process/write", "process/terminate"}
	if got := OuterProcessMethods(); !reflect.DeepEqual(got, want) {
		t.Fatalf("OuterProcessMethods() = %v, want %v", got, want)
	}
	if AllowsOuterProcessMethod("process/signal") {
		t.Fatal("outer process profile unexpectedly allows process/signal")
	}
	methods := OuterProcessMethods()
	methods[0] = "process/signal"
	if got := OuterProcessMethods(); !reflect.DeepEqual(got, want) {
		t.Fatalf("caller mutated outer process profile: %v", got)
	}
}

func TestNewRequiresTreeVerifierAndBoundedEventRetention(t *testing.T) {
	base := Options{
		ClientName:      "agentserver-v2-execadapter-test",
		CleanupGrace:    time.Second,
		ShutdownGrace:   time.Second,
		EventBuffer:     64,
		MaxEventBytes:   128 * 1024,
		Limits:          testAgentxLimits(),
		VerifyTreeEmpty: func(context.Context, string) error { return nil },
	}
	withoutVerifier := base
	withoutVerifier.VerifyTreeEmpty = nil
	if _, err := New(newReadyFakeTransport(t), "missing-verifier", withoutVerifier); err == nil || !strings.Contains(err.Error(), "verifier") {
		t.Fatalf("New() missing verifier error = %v", err)
	}
	overRetainedLimit := base
	overRetainedLimit.EventBuffer++
	if _, err := New(newReadyFakeTransport(t), "unbounded-events", overRetainedLimit); err == nil || !strings.Contains(err.Error(), "output limit") {
		t.Fatalf("New() event retention error = %v", err)
	}
}

func TestInstanceForwardsOnlyOwnedBoundedMethods(t *testing.T) {
	transport := newReadyFakeTransport(t)
	instance := newTestInstance(t, transport, "instance-owned", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := instance.Start(ctx, testStartParams("owned-process")); err != nil {
		t.Fatal(err)
	}

	before := transport.methodCount("process/signal")
	_, err := instance.Forward(ctx, "process/signal", json.RawMessage(`{"processId":"owned-process","signal":"interrupt"}`))
	if !errors.Is(err, ErrMethodNotNegotiated) {
		t.Fatalf("process/signal error = %v", err)
	}
	if got := transport.methodCount("process/signal"); got != before {
		t.Fatalf("process/signal reached stock transport %d times", got-before)
	}

	before = transport.methodCount("process/write")
	_, err = instance.Forward(ctx, "process/write", json.RawMessage(`{"processId":"other-process","chunk":"YQ==","writeId":"write-1"}`))
	if err == nil || !strings.Contains(err.Error(), "not owned") {
		t.Fatalf("foreign process/write error = %v", err)
	}
	if got := transport.methodCount("process/write"); got != before {
		t.Fatal("foreign process/write reached stock transport")
	}
	readBefore := transport.methodCount("process/read")
	_, err = instance.Forward(ctx, "process/read", json.RawMessage(`{"processId":"owned-process","processId":"owned-process","afterSeq":0,"waitMs":0}`))
	if err == nil || !strings.Contains(err.Error(), "duplicate JSON object key") {
		t.Fatalf("duplicate-key process/read error = %v", err)
	}
	if got := transport.methodCount("process/read"); got != readBefore {
		t.Fatal("duplicate-key process/read reached stock transport")
	}

	tooLongWriteID := strings.Repeat("w", testAgentxLimits().MaxWriteIDBytes+1)
	params, err := json.Marshal(map[string]any{
		"processId": "owned-process",
		"chunk":     "YQ==",
		"writeId":   tooLongWriteID,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = instance.Forward(ctx, "process/write", params)
	if err == nil || !strings.Contains(err.Error(), "writeId exceeds") {
		t.Fatalf("oversized process/write error = %v", err)
	}
	if got := transport.methodCount("process/write"); got != before {
		t.Fatal("oversized process/write reached stock transport")
	}

	result, err := instance.Forward(ctx, "process/write", json.RawMessage(`{"processId":"owned-process","chunk":"YQ==","writeId":"write-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	var write struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(result, &write); err != nil || write.Status != "accepted" {
		t.Fatalf("process/write result = %s, error %v", result, err)
	}

	if err := instance.Start(ctx, testStartParams("second-process")); !errors.Is(err, ErrInstanceAlreadyUsed) {
		t.Fatalf("second process/start error = %v", err)
	}
	if got := transport.methodCount("process/start"); got != 1 {
		t.Fatalf("stock process/start count = %d, want one", got)
	}

	transport.emitNotification(t, "process/exited", map[string]any{
		"processId":     "owned-process",
		"seq":           1,
		"exitCode":      0,
		"sandboxDenied": false,
	})
	transport.emitNotification(t, "process/closed", map[string]any{
		"processId": "owned-process",
		"seq":       2,
	})
	terminal, err := instance.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if terminal.State != TerminalClosed || !terminal.ProtocolClosed || terminal.ExitCode == nil || *terminal.ExitCode != 0 {
		t.Fatalf("normal terminal = %+v", terminal)
	}
}

func TestInstanceAcceptsTerminalNotificationsBeforeStartResponse(t *testing.T) {
	transport := newReadyFakeTransport(t)
	defaultHandler := transport.handler
	transport.handler = func(message codexwire.Message) {
		if message.Method != "process/start" {
			defaultHandler(message)
			return
		}
		var params struct {
			ProcessID string `json:"processId"`
		}
		if err := message.DecodeParams(&params); err != nil {
			t.Errorf("decode process/start: %v", err)
			return
		}
		transport.emitNotification(t, "process/exited", map[string]any{
			"processId":     params.ProcessID,
			"seq":           1,
			"exitCode":      0,
			"sandboxDenied": false,
		})
		transport.emitNotification(t, "process/closed", map[string]any{
			"processId": params.ProcessID,
			"seq":       2,
		})
		transport.emitResponse(t, message, map[string]any{"processId": params.ProcessID})
	}
	instance := newTestInstance(t, transport, "instance-terminal-race", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := instance.Start(ctx, testStartParams("fast-process")); err != nil {
		t.Fatalf("process/start response after terminal notifications: %v", err)
	}
	result, err := instance.Wait(ctx)
	if err != nil || result.State != TerminalClosed || !result.ProtocolClosed {
		t.Fatalf("fast process result = %+v, error %v", result, err)
	}
}

func TestDedicatedInstancesForceOnlyTheUnclosedProcess(t *testing.T) {
	firstTransport := newReadyFakeTransport(t)
	secondTransport := newReadyFakeTransport(t)
	var firstVerified atomic.Int64
	first := newTestInstance(t, firstTransport, "instance-first", func(context.Context, string) error {
		firstVerified.Add(1)
		return nil
	})
	second := newTestInstance(t, secondTransport, "instance-second", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := first.Start(ctx, testStartParams("first-process")); err != nil {
		t.Fatal(err)
	}
	if err := second.Start(ctx, testStartParams("second-process")); err != nil {
		t.Fatal(err)
	}

	firstTransport.emitNotification(t, "process/exited", map[string]any{
		"processId":     "first-process",
		"seq":           1,
		"exitCode":      23,
		"sandboxDenied": false,
	})
	firstResult, err := first.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if firstResult.State != TerminalCleanupForced || firstResult.ProtocolClosed ||
		firstResult.ExitCode == nil || *firstResult.ExitCode != 23 ||
		!errors.Is(firstResult.Cause, ErrProcessDidNotClose) {
		t.Fatalf("forced terminal = %+v", firstResult)
	}
	if firstVerified.Load() != 1 || firstTransport.closeCount.Load() != 1 {
		t.Fatalf("first cleanup verifier/close counts = %d/%d", firstVerified.Load(), firstTransport.closeCount.Load())
	}
	if secondTransport.closeCount.Load() != 0 {
		t.Fatal("first forced cleanup closed the second dedicated instance")
	}

	readResult, err := second.Forward(ctx, "process/read", json.RawMessage(`{"processId":"second-process","afterSeq":0,"waitMs":0}`))
	if err != nil {
		t.Fatalf("second instance stopped after first cleanup: %v", err)
	}
	if string(readResult) != `{"closed":false,"exited":false,"chunks":[],"nextSeq":1}` {
		t.Fatalf("second process/read result = %s", readResult)
	}
	secondTransport.emitNotification(t, "process/exited", map[string]any{
		"processId":     "second-process",
		"seq":           1,
		"exitCode":      0,
		"sandboxDenied": false,
	})
	secondTransport.emitNotification(t, "process/closed", map[string]any{
		"processId": "second-process",
		"seq":       2,
	})
	secondResult, err := second.Wait(ctx)
	if err != nil || secondResult.State != TerminalClosed {
		t.Fatalf("second terminal = %+v, error %v", secondResult, err)
	}
}

func TestCleanupVerificationFailureIsUnknown(t *testing.T) {
	transport := newReadyFakeTransport(t)
	verifyErr := errors.New("process tree is still alive")
	instance := newTestInstance(t, transport, "instance-unknown", func(context.Context, string) error {
		return verifyErr
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := instance.Start(ctx, testStartParams("unknown-process")); err != nil {
		t.Fatal(err)
	}
	transport.emitNotification(t, "process/exited", map[string]any{
		"processId":     "unknown-process",
		"seq":           1,
		"exitCode":      7,
		"sandboxDenied": false,
	})
	result, err := instance.Wait(ctx)
	if result.State != TerminalUnknown || !errors.Is(err, verifyErr) {
		t.Fatalf("unknown cleanup result = %+v, error %v", result, err)
	}
}

func testAgentxLimits() runtimelock.AgentxLimits {
	return runtimelock.AgentxLimits{
		MaxFrameBytes:                  8 * 1024 * 1024,
		MaxJSONValues:                  64 * 1024,
		MaxArgvElements:                256,
		MaxArgvBytes:                   16 * 1024,
		MaxEnvVariables:                256,
		MaxEnvBytes:                    16 * 1024,
		MaxWriteIDBytes:                128,
		MaxOutputBufferBytesPerProcess: 8 * 1024 * 1024,
	}
}

func newTestInstance(t *testing.T, transport *fakeTransport, id string, verifier CleanupVerifier) *Instance {
	t.Helper()
	if verifier == nil {
		verifier = func(context.Context, string) error { return nil }
	}
	instance, err := New(transport, id, Options{
		ClientName:      "agentserver-v2-execadapter-test",
		CleanupGrace:    20 * time.Millisecond,
		ShutdownGrace:   time.Second,
		EventBuffer:     16,
		MaxEventBytes:   64 * 1024,
		Limits:          testAgentxLimits(),
		VerifyTreeEmpty: verifier,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { instance.Abort(errors.New("test cleanup")) })
	return instance
}

func testStartParams(processID string) map[string]any {
	return map[string]any{
		"processId":             processID,
		"argv":                  []string{"/bin/test-helper"},
		"cwd":                   "file:///tmp",
		"env":                   map[string]string{},
		"tty":                   false,
		"pipeStdin":             true,
		"arg0":                  nil,
		"sandbox":               nil,
		"enforceManagedNetwork": false,
	}
}

type fakeRead struct {
	message codexwire.Message
	err     error
}

type fakeTransport struct {
	t          *testing.T
	incoming   chan fakeRead
	handler    func(codexwire.Message)
	mu         sync.Mutex
	closed     bool
	methods    map[string]int
	waitDone   chan struct{}
	closeOnce  sync.Once
	closeCount atomic.Int64
	killCount  atomic.Int64
}

func newReadyFakeTransport(t *testing.T) *fakeTransport {
	t.Helper()
	transport := &fakeTransport{
		t:        t,
		incoming: make(chan fakeRead, 64),
		methods:  make(map[string]int),
		waitDone: make(chan struct{}),
	}
	transport.handler = func(message codexwire.Message) {
		switch message.Method {
		case "initialize":
			transport.emitResponse(t, message, map[string]any{"sessionId": "fake-session"})
		case "initialized":
		case "environment/info":
			transport.emitResponse(t, message, map[string]any{"cwd": "file:///tmp"})
		case "environment/status":
			transport.emitResponse(t, message, map[string]any{"status": "ready"})
		case "process/start":
			var params struct {
				ProcessID string `json:"processId"`
			}
			if err := message.DecodeParams(&params); err != nil {
				t.Errorf("decode fake process/start: %v", err)
				return
			}
			transport.emitResponse(t, message, map[string]any{"processId": params.ProcessID})
		case "process/write":
			transport.emitResponse(t, message, map[string]any{"status": "accepted"})
		case "process/read":
			transport.emitResponse(t, message, json.RawMessage(`{"closed":false,"exited":false,"chunks":[],"nextSeq":1}`))
		case "process/terminate":
			transport.emitResponse(t, message, map[string]any{"running": true})
		default:
			t.Errorf("unexpected fake outbound method %q", message.Method)
		}
	}
	return transport
}

func (f *fakeTransport) Send(value any) error {
	frame, err := json.Marshal(value)
	if err != nil {
		return err
	}
	message, err := codexwire.Parse(frame)
	if err != nil {
		return err
	}
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return io.ErrClosedPipe
	}
	f.methods[message.Method]++
	handler := f.handler
	f.mu.Unlock()
	if handler != nil {
		handler(message)
	}
	return nil
}

func (f *fakeTransport) Receive(ctx context.Context) (codexwire.Message, error) {
	select {
	case <-ctx.Done():
		return codexwire.Message{}, ctx.Err()
	case result := <-f.incoming:
		return result.message, result.err
	}
}

func (f *fakeTransport) CloseStdin() error {
	f.closeOnce.Do(func() {
		f.closeCount.Add(1)
		f.mu.Lock()
		f.closed = true
		f.mu.Unlock()
		close(f.waitDone)
		f.incoming <- fakeRead{err: io.EOF}
	})
	return nil
}

func (f *fakeTransport) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-f.waitDone:
		return nil
	}
}

func (f *fakeTransport) Kill() error {
	f.killCount.Add(1)
	return f.CloseStdin()
}

func (f *fakeTransport) methodCount(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.methods[method]
}

func (f *fakeTransport) emitResponse(t *testing.T, request codexwire.Message, result any) {
	t.Helper()
	f.emitValue(t, map[string]any{"id": request.ID, "result": result})
}

func (f *fakeTransport) emitNotification(t *testing.T, method string, params any) {
	t.Helper()
	f.emitValue(t, map[string]any{"method": method, "params": params})
}

func (f *fakeTransport) emitValue(t *testing.T, value any) {
	t.Helper()
	frame, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	message, err := codexwire.Parse(frame)
	if err != nil {
		t.Fatalf("parse fake inbound frame %s: %v", frame, err)
	}
	select {
	case f.incoming <- fakeRead{message: message}:
	case <-time.After(time.Second):
		t.Fatalf("fake inbound queue blocked: %s", frame)
	}
}

func (f *fakeTransport) String() string {
	return fmt.Sprintf("fakeTransport(closes=%d,kills=%d)", f.closeCount.Load(), f.killCount.Load())
}
