// internal/codexappgateway/scheduler/dispatcher_test.go
package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type fakeAgent struct {
	lastResult *ResultRequest
	channels   []ChannelRef
}

func (f *fakeAgent) LeaseDue(ctx context.Context, _ LeaseRequest) ([]Task, error) {
	return nil, nil
}
func (f *fakeAgent) PostResult(_ context.Context, r ResultRequest) error {
	f.lastResult = &r
	return nil
}
func (f *fakeAgent) ListChannels(_ context.Context, _ string) ([]ChannelRef, error) {
	return f.channels, nil
}

type fakeSpawner struct {
	res SpawnResult
	err error
}

func (f *fakeSpawner) Run(_ context.Context, _ SpawnInput) (SpawnResult, error) {
	return f.res, f.err
}

type fakeBroadcaster struct{ called int }

func (f *fakeBroadcaster) Send(_ context.Context, _, _ string, ch []ChannelRef) BroadcastReport {
	f.called++
	to := make([]string, len(ch))
	for i, c := range ch {
		to[i] = c.ID
	}
	return BroadcastReport{To: to, Errors: map[string]string{}}
}

func TestDispatcher_Fire_HappyPath_PostsResultAndBroadcasts(t *testing.T) {
	a := &fakeAgent{channels: []ChannelRef{{ID: "ch1", UserID: "u1"}}}
	sp := &fakeSpawner{res: SpawnResult{ExitCode: 0, Summary: "ok", Transcript: "ok"}}
	br := &fakeBroadcaster{}
	d := NewDispatcher(a, sp, br)
	err := d.Fire(context.Background(), Task{ID: "sch_a", RunID: "run_1", WorkspaceID: "ws", Prompt: "hi", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	if a.lastResult == nil || a.lastResult.Status != "succeeded" {
		t.Fatalf("result=%+v", a.lastResult)
	}
	if br.called != 1 {
		t.Fatalf("broadcast called %d times", br.called)
	}
}

func TestDispatcher_Fire_ScriptGated_Skips(t *testing.T) {
	a := &fakeAgent{channels: []ChannelRef{{ID: "ch1", UserID: "u1"}}}
	sp := &fakeSpawner{res: SpawnResult{ExitCode: 0, Summary: "should not appear"}}
	br := &fakeBroadcaster{}
	d := NewDispatcher(a, sp, br)
	skipScript := `echo '{"wakeAgent":false,"data":null}'`
	t1 := Task{ID: "sch_a", RunID: "r1", WorkspaceID: "ws", Prompt: "hi", Timezone: "UTC", Script: &skipScript}
	if err := d.Fire(context.Background(), t1); err != nil {
		t.Fatal(err)
	}
	if a.lastResult == nil || a.lastResult.Status != "skipped" {
		t.Fatalf("result=%+v", a.lastResult)
	}
	if br.called != 0 {
		t.Fatalf("must not broadcast on skip; called=%d", br.called)
	}
}

func TestDispatcher_Fire_SpawnError_ReportsFailed(t *testing.T) {
	a := &fakeAgent{}
	sp := &fakeSpawner{err: errors.New("boom")}
	d := NewDispatcher(a, sp, &fakeBroadcaster{})
	_ = d.Fire(context.Background(), Task{ID: "sch_x", RunID: "r", WorkspaceID: "w", Prompt: "p", Timezone: "UTC"})
	if a.lastResult == nil || a.lastResult.Status != "failed" {
		t.Fatalf("result=%+v", a.lastResult)
	}
}

// silence unused-import warnings; keeps the test file self-contained.
var _ = json.Marshal
