package browsergateway

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/encoding/sse"

	"github.com/agentserver/agentserver/internal/browsergateway/codexclient"
)

// fakeConn implements codexConn with a scripted frame stream.
type fakeConn struct {
	frames      chan codexclient.Frame
	startedTurn string
	interrupted bool
	turnStarted chan struct{} // closed by StartTurn when non-nil (lets a test know the run loop is reached)
}

func (f *fakeConn) StartThread(context.Context) (string, error) { return "thr-1", nil }
func (f *fakeConn) ResumeThread(context.Context, string) error  { return nil }
func (f *fakeConn) StartTurn(_ context.Context, _, text string) (string, error) {
	f.startedTurn = text
	if f.turnStarted != nil {
		close(f.turnStarted)
	}
	return "trn-1", nil
}
func (f *fakeConn) Frames() <-chan codexclient.Frame          { return f.frames }
func (f *fakeConn) Interrupt(context.Context, string, string) { f.interrupted = true }
func (f *fakeConn) Close() error                              { return nil }

func TestRunAGUI_TextRun(t *testing.T) {
	fc := &fakeConn{frames: make(chan codexclient.Frame, 5)}
	fc.frames <- codexclient.Frame{Method: "item/started", Params: []byte(`{"item":{"type":"agentMessage","id":"msg-1","text":"","phase":null}}`)}
	fc.frames <- codexclient.Frame{Method: "item/agentMessage/delta", Params: []byte(`{"itemId":"msg-1","delta":"hi there"}`)}
	fc.frames <- codexclient.Frame{Method: "item/completed", Params: []byte(`{"item":{"type":"agentMessage","id":"msg-1","text":"hi there"}}`)}
	fc.frames <- codexclient.Frame{Method: "turn/completed", Params: []byte(`{"turn":{"id":"trn-1"}}`)}

	in := &types.RunAgentInput{
		RunID:    "run-1",
		Messages: []types.Message{{Role: types.RoleUser, Content: "say hi"}},
	}
	rec := httptest.NewRecorder()
	dial := func(context.Context, string) (codexConn, error) { return fc, nil }

	runAGUI(context.Background(), rec, sse.NewSSEWriter(), in, "tok", dial)

	body := rec.Body.String()
	for _, want := range []string{"RUN_STARTED", "TEXT_MESSAGE_START", "TEXT_MESSAGE_CONTENT", "TEXT_MESSAGE_END", "RUN_FINISHED"} {
		if !strings.Contains(body, want) {
			t.Errorf("SSE body missing %q\n---\n%s", want, body)
		}
	}
	if fc.startedTurn != "say hi" {
		t.Errorf("turn input = %q, want %q", fc.startedTurn, "say hi")
	}
}

func TestRunAGUI_ContextCancel(t *testing.T) {
	// frames never carries a terminal frame, so the only way runAGUI returns is
	// via the ctx.Done() branch of its select — which must call Interrupt.
	fc := &fakeConn{
		frames:      make(chan codexclient.Frame),
		turnStarted: make(chan struct{}),
	}
	in := &types.RunAgentInput{
		RunID:    "run-1",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hang"}},
	}
	rec := httptest.NewRecorder()
	dial := func(context.Context, string) (codexConn, error) { return fc, nil }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runAGUI(ctx, rec, sse.NewSSEWriter(), in, "tok", dial)
	}()

	<-fc.turnStarted // run loop is now blocked on the frames/ctx select
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runAGUI did not return after ctx cancel")
	}
	if !fc.interrupted {
		t.Error("Interrupt was not called on ctx cancel")
	}
}

func TestRunAGUI_NoUserMessage(t *testing.T) {
	in := &types.RunAgentInput{RunID: "run-1"}
	rec := httptest.NewRecorder()
	dial := func(context.Context, string) (codexConn, error) {
		t.Fatal("dial should not be called")
		return nil, nil
	}
	runAGUI(context.Background(), rec, sse.NewSSEWriter(), in, "tok", dial)
	if !strings.Contains(rec.Body.String(), "RUN_ERROR") {
		t.Errorf("expected RUN_ERROR, got:\n%s", rec.Body.String())
	}
}
