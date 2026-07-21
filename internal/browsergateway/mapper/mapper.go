// Package mapper translates codex v2 server notification frames into AG-UI
// events. It is pure (no I/O) so it is trivially unit-testable with recorded
// frames. Unknown frames are logged and skipped (prefer over-logging to
// silently dropping new data).
package mapper

import (
	"encoding/json"
	"log/slog"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/agentserver/agentserver/internal/browsergateway/codexclient"
)

// Result is the outcome of mapping one codex notification frame.
type Result struct {
	Events []events.Event // AG-UI content events to write
	Done   bool           // turn/completed observed → loop emits RUN_FINISHED
	Err    string         // non-empty → codex reported an error → loop emits RUN_ERROR
}

type itemParams struct {
	Item codexItem `json:"item"`
}

type codexItem struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Text string `json:"text"`
}

type turnCompletedParams struct {
	Turn struct {
		Status string          `json:"status"`
		Error  json.RawMessage `json:"error"`
	} `json:"turn"`
}

// Map translates one codex server notification into AG-UI content events.
func Map(f codexclient.Frame) Result {
	switch f.Method {
	case "item/completed":
		var p itemParams
		if err := json.Unmarshal(f.Params, &p); err != nil {
			slog.Warn("browser-gateway/mapper: bad item/completed params", "err", err)
			return Result{}
		}
		return mapItem(p.Item)
	case "turn/completed":
		var p turnCompletedParams
		if err := json.Unmarshal(f.Params, &p); err != nil {
			// Can't tell success from failure — don't hang the run.
			slog.Warn("browser-gateway/mapper: bad turn/completed params", "err", err)
			return Result{Done: true}
		}
		hasErr := len(p.Turn.Error) > 0 && string(p.Turn.Error) != "null"
		if hasErr || (p.Turn.Status != "" && p.Turn.Status != "completed") {
			return Result{Err: "codex turn " + p.Turn.Status + ": " + string(p.Turn.Error)}
		}
		return Result{Done: true}
	case "error":
		return Result{Err: string(f.Params)}
	default:
		// turn/started, item/started, thread/*, item/agentMessage/delta, ...
		// Not surfaced in P1. Deltas become TEXT_MESSAGE_CONTENT once Phase 0
		// pins their frame shape (see mapper/testdata/PROBE.md).
		return Result{}
	}
}

func mapItem(it codexItem) Result {
	switch it.Type {
	case "agentMessage":
		if it.Text == "" {
			return Result{}
		}
		return Result{Events: []events.Event{
			events.NewTextMessageStartEvent(it.ID, events.WithRole("assistant")),
			events.NewTextMessageContentEvent(it.ID, it.Text),
			events.NewTextMessageEndEvent(it.ID),
		}}
	case "reasoning":
		if it.Text == "" {
			return Result{}
		}
		return Result{Events: []events.Event{
			events.NewReasoningMessageStartEvent(it.ID, "assistant"),
			events.NewReasoningMessageContentEvent(it.ID, it.Text),
			events.NewReasoningMessageEndEvent(it.ID),
		}}
	case "userMessage":
		return Result{} // client already has the user's own message
	default:
		slog.Warn("browser-gateway/mapper: unmapped item type", "type", it.Type)
		return Result{}
	}
}
