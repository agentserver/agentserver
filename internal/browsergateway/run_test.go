package browsergateway

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/encoding/sse"

	"github.com/agentserver/agentserver/internal/browsergateway/codexclient"
)

// fakeConn implements codexConn with a scripted frame stream.
type fakeConn struct {
	frames      chan codexclient.Frame
	startedTurn string
}

func (f *fakeConn) StartThread(context.Context) (string, error) { return "thr-1", nil }
func (f *fakeConn) ResumeThread(context.Context, string) error  { return nil }
func (f *fakeConn) StartTurn(_ context.Context, _, text string) (string, error) {
	f.startedTurn = text
	return "trn-1", nil
}
func (f *fakeConn) Frames() <-chan codexclient.Frame          { return f.frames }
func (f *fakeConn) Interrupt(context.Context, string, string) {}
func (f *fakeConn) Close() error                              { return nil }

func TestRunAGUI_TextRun(t *testing.T) {
	fc := &fakeConn{frames: make(chan codexclient.Frame, 4)}
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
