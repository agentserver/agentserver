package harnessworker

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentserver/agentserver/v2/internal/codexwire"
)

func TestWorkerRuntimeEventForwarderOrdersProgressAfterDynamicToolStartJournal(t *testing.T) {
	sink := &recordingWorkerRuntimeEventSink{}
	forwarder := newWorkerRuntimeEventForwarder(sink, nil, nil)
	progress := ProgressEvent{
		RunID: "run-1", CallID: "call-1", RunAttemptGeneration: 1,
		Progress: 1, Total: 2, Message: "running",
	}

	progressResult := make(chan error, 1)
	go func() { progressResult <- forwarder.HandleProgress(t.Context(), progress) }()
	started := forwarder.dynamicToolStarted(progress.CallID)
	select {
	case <-started:
		t.Fatal("progress observed a dynamic tool start before its notification was journaled")
	default:
	}
	if got := sink.snapshot(); len(got) != 0 {
		t.Fatalf("progress reached control before item/started: %v", got)
	}

	start := codexwire.Message{
		Kind: codexwire.KindNotification, Method: "item/started",
		Params: json.RawMessage(`{
			"threadId":"thread-1","turnId":"turn-1","startedAtMs":1,
			"item":{"type":"dynamicToolCall","id":"call-1","namespace":"executor","tool":"read_file","arguments":{},"status":"inProgress","contentItems":null,"success":null}
		}`),
	}
	if err := forwarder.HandleNotification(t.Context(), start); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-progressResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("progress did not resume after item/started crossed control")
	}
	if got := sink.snapshot(); !reflect.DeepEqual(got, []string{"notification:item/started", "progress:call-1"}) {
		t.Fatalf("runtime forwarding order = %v", got)
	}
}

func TestWorkerRuntimeEventForwarderFailsClosedWithoutJournaledStart(t *testing.T) {
	sink := &recordingWorkerRuntimeEventSink{notificationErr: errors.New("control journal full")}
	forwarder := newWorkerRuntimeEventForwarder(sink, nil, nil)
	start := codexwire.Message{
		Kind: codexwire.KindNotification, Method: "item/started",
		Params: json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","item":{"type":"dynamicToolCall","id":"call-1"}}`),
	}
	if err := forwarder.HandleNotification(t.Context(), start); err == nil || !strings.Contains(err.Error(), "journal full") {
		t.Fatalf("notification journal error = %v", err)
	}

	ctx, cancel := context.WithCancelCause(t.Context())
	result := make(chan error, 1)
	go func() {
		result <- forwarder.HandleProgress(ctx, ProgressEvent{CallID: "call-1"})
	}()
	cancel(errors.New("turn stopped"))
	select {
	case err := <-result:
		if err == nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("progress without start error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("progress waiter did not stop with its turn")
	}
	if got := sink.snapshot(); !reflect.DeepEqual(got, []string{"notification:item/started"}) {
		t.Fatalf("failed start unexpectedly released progress: %v", got)
	}
}

func TestWorkerRuntimeEventAllowlistExcludesNonProjectionNotifications(t *testing.T) {
	for _, method := range []string{
		"item/started", "item/completed", "item/agentMessage/delta",
		"item/reasoning/summaryTextDelta", "item/reasoning/summaryPartAdded",
		"item/reasoning/textDelta", "turn/completed",
	} {
		if !shouldForwardAppServerNotification(method) {
			t.Fatalf("projection notification %q is not forwarded", method)
		}
	}
	for _, method := range []string{"thread/started", "turn/started", "error", "future/notification"} {
		if shouldForwardAppServerNotification(method) {
			t.Fatalf("non-projection notification %q crossed control", method)
		}
	}
}

type recordingWorkerRuntimeEventSink struct {
	mu              sync.Mutex
	events          []string
	notificationErr error
	progressErr     error
}

func (sink *recordingWorkerRuntimeEventSink) SendAppServerNotification(_ context.Context, message codexwire.Message) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.events = append(sink.events, "notification:"+message.Method)
	return sink.notificationErr
}

func (sink *recordingWorkerRuntimeEventSink) SendExecutorMCPProgress(_ context.Context, event ProgressEvent) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.events = append(sink.events, "progress:"+event.CallID)
	return sink.progressErr
}

func (sink *recordingWorkerRuntimeEventSink) snapshot() []string {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return append([]string(nil), sink.events...)
}
