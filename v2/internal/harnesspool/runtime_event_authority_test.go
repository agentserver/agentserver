package harnesspool

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/agentserver/agentserver/v2/internal/runevent"
)

func TestAttemptRuntimeAuthorityAppendsCanonicalEventsBeforeAdvancingCursor(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	prepared.FrozenCatalog.ThreadID = "thread-runtime-1"
	core := newRuntimeAppendCore(prepared)
	identities := &runtimeSequenceIdentityAllocator{}
	authority := &attemptLifecycleAuthority{
		ctx: t.Context(), scheduler: &poolTestScheduler{}, core: core, identities: identities, prepared: prepared,
		threadID: "thread-runtime-1", turnID: "turn-runtime-1", turnWasAccepted: true,
	}
	event := appRuntimeEvent(t, "item/started", map[string]any{
		"threadId": "thread-runtime-1", "turnId": "turn-runtime-1",
		"item": map[string]any{"type": "agentMessage", "id": "message-1", "text": ""},
	})
	if err := authority.RuntimeEvent(t.Context(), AttemptRuntimeEvent{ControlSequence: 3, Event: event}); err != nil {
		t.Fatal(err)
	}

	requests := core.appendSnapshot()
	if len(requests) != 1 || len(requests[0].Events) != 1 {
		t.Fatalf("runtime append requests = %+v", requests)
	}
	claim := prepared.Scheduled.Claim
	request := requests[0]
	if request.RunID != claim.Run.RunID || request.RunAttemptID != claim.RunAttempt.RunAttemptID ||
		request.HolderID != claim.RunAttempt.HolderID || request.RunAttemptGeneration != claim.RunAttempt.Generation ||
		request.OutboxID == "" {
		t.Fatalf("runtime append scope = %+v", request)
	}
	appended := request.Events[0]
	if appended.Kind != runevent.KindAssistantMessageStarted || appended.Source != "brain" ||
		appended.SchemaVersion != runevent.CurrentSchemaVersion || appended.EventID == "" ||
		appended.ProducerInstanceID == "" || appended.ProducerSeq != 1 {
		t.Fatalf("canonical runtime event = %+v", appended)
	}
	if authority.runtimeCursor != 3 || authority.pendingRuntime != nil || identities.callCount() != 1 {
		t.Fatalf("runtime authority cursor/pending/identities = %d / %+v / %d", authority.runtimeCursor, authority.pendingRuntime, identities.callCount())
	}
	if err := authority.RuntimeEvent(t.Context(), AttemptRuntimeEvent{ControlSequence: 3, Event: event}); err == nil || !strings.Contains(err.Error(), "did not advance") {
		t.Fatalf("reused committed control sequence error = %v", err)
	}
}

func TestAttemptRuntimeAuthorityRetriesExactPendingAppendAfterAmbiguousControlResume(t *testing.T) {
	prepared := poolTestPreparedLaunch(t)
	prepared.FrozenCatalog.ThreadID = "thread-runtime-1"
	core := newRuntimeAppendCore(prepared)
	core.appendErrors = []error{errors.New("append response lost"), errors.New("core temporarily unavailable")}
	identities := &runtimeSequenceIdentityAllocator{}
	authority := &attemptLifecycleAuthority{
		ctx: t.Context(), scheduler: &poolTestScheduler{}, core: core, identities: identities, prepared: prepared,
		threadID: "thread-runtime-1", turnID: "turn-runtime-1", turnWasAccepted: true,
	}
	event := appRuntimeEvent(t, "item/started", map[string]any{
		"threadId": "thread-runtime-1", "turnId": "turn-runtime-1",
		"item": map[string]any{"type": "agentMessage", "id": "message-1", "text": ""},
	})

	err := authority.RuntimeEvent(t.Context(), AttemptRuntimeEvent{ControlSequence: 7, Event: event})
	if err == nil || !isRetryableRuntimeEventError(err) {
		t.Fatalf("ambiguous append error = %v", err)
	}
	requests := core.appendSnapshot()
	if len(requests) != 2 || !reflect.DeepEqual(requests[0], requests[1]) {
		t.Fatalf("immediate ambiguous retry changed request: %+v", requests)
	}
	if authority.runtimeCursor != 0 || authority.pendingRuntime == nil || identities.callCount() != 1 {
		t.Fatalf("failed append advanced state = cursor %d pending %+v identities %d", authority.runtimeCursor, authority.pendingRuntime, identities.callCount())
	}

	changed := appRuntimeEvent(t, "item/started", map[string]any{
		"threadId": "thread-runtime-1", "turnId": "turn-runtime-1",
		"item": map[string]any{"type": "agentMessage", "id": "different-message", "text": ""},
	})
	if err := authority.RuntimeEvent(t.Context(), AttemptRuntimeEvent{ControlSequence: 7, Event: changed}); err == nil || !strings.Contains(err.Error(), "different runtime event") {
		t.Fatalf("pending event replacement error = %v", err)
	}
	if err := authority.RuntimeEvent(t.Context(), AttemptRuntimeEvent{ControlSequence: 7, Event: event}); err != nil {
		t.Fatalf("same-holder resume retry = %v", err)
	}
	requests = core.appendSnapshot()
	if len(requests) != 3 || !reflect.DeepEqual(requests[0], requests[2]) {
		t.Fatalf("control resume changed producer identities: %+v", requests)
	}
	if authority.runtimeCursor != 7 || authority.pendingRuntime != nil || identities.callCount() != 1 {
		t.Fatalf("successful resume state = cursor %d pending %+v identities %d", authority.runtimeCursor, authority.pendingRuntime, identities.callCount())
	}
}

type runtimeAppendCore struct {
	*poolTestCore

	appendMu       sync.Mutex
	appendRequests []AppendAttemptEventsRequest
	appendErrors   []error
	appendStarted  chan struct{}
	appendRelease  chan struct{}
	startedOnce    sync.Once
}

func newRuntimeAppendCore(prepared PreparedRunLaunch) *runtimeAppendCore {
	return &runtimeAppendCore{poolTestCore: newPoolTestCore(prepared)}
}

func (core *runtimeAppendCore) AppendAttemptEvents(ctx context.Context, request AppendAttemptEventsRequest) (AppendAttemptEventsResult, error) {
	core.appendMu.Lock()
	core.appendRequests = append(core.appendRequests, cloneAppendAttemptEventsRequest(request))
	started, release := core.appendStarted, core.appendRelease
	var err error
	if len(core.appendErrors) != 0 {
		err = core.appendErrors[0]
		core.appendErrors = core.appendErrors[1:]
	}
	core.appendMu.Unlock()
	if started != nil {
		core.startedOnce.Do(func() { close(started) })
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return AppendAttemptEventsResult{}, context.Cause(ctx)
		}
	}
	if err != nil {
		return AppendAttemptEventsResult{}, err
	}
	result := AppendAttemptEventsResult{Events: make([]AppendedAttemptEvent, len(request.Events)), NewCount: len(request.Events)}
	for index, event := range request.Events {
		result.Events[index] = AppendedAttemptEvent{
			EventID: event.EventID, ProducerInstanceID: event.ProducerInstanceID,
			ProducerSeq: event.ProducerSeq, RunSeq: int64(index + 1),
		}
	}
	return result, nil
}

func (core *runtimeAppendCore) appendSnapshot() []AppendAttemptEventsRequest {
	core.appendMu.Lock()
	defer core.appendMu.Unlock()
	result := make([]AppendAttemptEventsRequest, len(core.appendRequests))
	for index, request := range core.appendRequests {
		result[index] = cloneAppendAttemptEventsRequest(request)
	}
	return result
}

type runtimeSequenceIdentityAllocator struct {
	mu    sync.Mutex
	calls int
}

func (allocator *runtimeSequenceIdentityAllocator) AllocateTransitionRecord() (TransitionRecord, error) {
	allocator.mu.Lock()
	defer allocator.mu.Unlock()
	allocator.calls++
	sequence := allocator.calls
	return TransitionRecord{
		EventID:            fmt.Sprintf("a1000000-0000-4000-8000-%012d", sequence),
		ProducerInstanceID: "a2000000-0000-4000-8000-000000000001",
		ProducerSeq:        int64(sequence),
		OutboxID:           fmt.Sprintf("a3000000-0000-4000-8000-%012d", sequence),
	}, nil
}

func (allocator *runtimeSequenceIdentityAllocator) callCount() int {
	allocator.mu.Lock()
	defer allocator.mu.Unlock()
	return allocator.calls
}

var _ AttemptSupervisionCore = (*runtimeAppendCore)(nil)
var _ AttemptEventCore = (*runtimeAppendCore)(nil)
