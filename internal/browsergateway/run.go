package browsergateway

import (
	"context"
	"net/http"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/encoding/sse"

	"github.com/agentserver/agentserver/internal/browsergateway/codexclient"
	"github.com/agentserver/agentserver/internal/browsergateway/mapper"
)

// codexConn is the codex-side surface runAGUI needs; *codexclient.Client
// satisfies it. Injected so tests can supply a scripted connection.
type codexConn interface {
	StartThread(ctx context.Context) (string, error)
	ResumeThread(ctx context.Context, threadID string) error
	StartTurn(ctx context.Context, threadID, userText string) (string, error)
	Frames() <-chan codexclient.Frame
	Interrupt(ctx context.Context, threadID, turnID string)
	Close() error
}

type dialFunc func(ctx context.Context, bearer string) (codexConn, error)

// latestUserText returns the text of the last user message, or "" if none.
func latestUserText(in *types.RunAgentInput) string {
	for i := len(in.Messages) - 1; i >= 0; i-- {
		if in.Messages[i].Role == types.RoleUser {
			if t, ok := in.Messages[i].ContentString(); ok {
				return t
			}
		}
	}
	return ""
}

// runAGUI drives one AG-UI run to completion, writing SSE events to w. It never
// returns an error: all failures are surfaced as a RUN_ERROR event so the
// client's stream is always well-formed.
func runAGUI(ctx context.Context, w http.ResponseWriter, sw *sse.SSEWriter, in *types.RunAgentInput, bearer string, dial dialFunc) {
	threadID := in.ThreadID
	runID := in.RunID
	if runID == "" {
		runID = events.GenerateRunID()
	}

	emitError := func(msg string) {
		tid := threadID
		if tid == "" {
			tid = events.GenerateThreadID()
		}
		_ = sw.WriteEvent(ctx, w, events.NewRunStartedEvent(tid, runID))
		_ = sw.WriteEvent(ctx, w, events.NewRunErrorEvent(msg, events.WithRunID(runID)))
	}

	userText := latestUserText(in)
	if userText == "" {
		emitError("no user message in RunAgentInput")
		return
	}

	conn, err := dial(ctx, bearer)
	if err != nil {
		emitError("codex dial failed: " + err.Error())
		return
	}
	defer conn.Close()

	if threadID == "" {
		threadID, err = conn.StartThread(ctx)
	} else {
		err = conn.ResumeThread(ctx, threadID)
	}
	if err != nil {
		emitError("codex thread setup failed: " + err.Error())
		return
	}

	if err := sw.WriteEvent(ctx, w, events.NewRunStartedEvent(threadID, runID)); err != nil {
		return // client gone
	}

	turnID, err := conn.StartTurn(ctx, threadID, userText)
	if err != nil {
		_ = sw.WriteEvent(ctx, w, events.NewRunErrorEvent("codex turn/start failed: "+err.Error(), events.WithRunID(runID)))
		return
	}

	frames := conn.Frames()
	for {
		select {
		case <-ctx.Done():
			conn.Interrupt(context.Background(), threadID, turnID)
			return
		case f, ok := <-frames:
			if !ok {
				_ = sw.WriteEvent(ctx, w, events.NewRunErrorEvent("codex connection closed before turn/completed", events.WithRunID(runID)))
				return
			}
			res := mapper.Map(f)
			for _, ev := range res.Events {
				if err := sw.WriteEvent(ctx, w, ev); err != nil {
					conn.Interrupt(context.Background(), threadID, turnID)
					return
				}
			}
			if res.Err != "" {
				_ = sw.WriteEvent(ctx, w, events.NewRunErrorEvent("codex error: "+res.Err, events.WithRunID(runID)))
				return
			}
			if res.Done {
				_ = sw.WriteEvent(ctx, w, events.NewRunFinishedEventWithOptions(threadID, runID, events.WithSuccessOutcome()))
				return
			}
		}
	}
}
