// Package mapper translates codex v2 server notification frames into AG-UI
// events. It is pure (no I/O) so it is trivially unit-testable with recorded
// frames. Unknown frames are logged and skipped (prefer over-logging to
// silently dropping new data).
package mapper

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/agentserver/agentserver/internal/browsergateway/a2ui"
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
	Type             string            `json:"type"`
	ID               string            `json:"id"`
	Text             string            `json:"text"`
	Summary          []string          `json:"summary"`
	Content          []string          `json:"content"`
	Command          string            `json:"command"`
	AggregatedOutput string            `json:"aggregatedOutput"`
	ExitCode         *int              `json:"exitCode"`
	Status           string            `json:"status"`
	Changes          []codexFileChange `json:"changes"`
}

type codexFileChange struct {
	Path string `json:"path"`
	Kind struct {
		Type string `json:"type"`
	} `json:"kind"`
	Diff string `json:"diff"`
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
	case "item/started":
		var p itemParams
		if err := json.Unmarshal(f.Params, &p); err != nil {
			slog.Warn("browser-gateway/mapper: bad item/started params", "err", err)
			return Result{}
		}
		return mapItemStarted(p.Item)
	case "item/agentMessage/delta":
		var d struct {
			ItemID string `json:"itemId"`
			Delta  string `json:"delta"`
		}
		if err := json.Unmarshal(f.Params, &d); err != nil || d.Delta == "" {
			return Result{}
		}
		return Result{Events: []events.Event{events.NewTextMessageContentEvent(d.ItemID, d.Delta)}}
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
		// turn/started, thread/*, ...
		// Not surfaced in P1.
		return Result{}
	}
}

func mapItemStarted(it codexItem) Result {
	switch it.Type {
	case "agentMessage":
		return Result{Events: []events.Event{events.NewTextMessageStartEvent(it.ID, events.WithRole("assistant"))}}
	default:
		return Result{}
	}
}

func mapItem(it codexItem) Result {
	switch it.Type {
	case "agentMessage":
		return Result{Events: []events.Event{events.NewTextMessageEndEvent(it.ID)}}
	case "reasoning":
		text := strings.TrimSpace(strings.Join(append(append([]string{}, it.Summary...), it.Content...), "\n"))
		if text == "" {
			return Result{}
		}
		return Result{Events: []events.Event{
			events.NewReasoningMessageStartEvent(it.ID, "assistant"),
			events.NewReasoningMessageContentEvent(it.ID, text),
			events.NewReasoningMessageEndEvent(it.ID),
		}}
	case "userMessage":
		return Result{} // client already has the user's own message
	case "commandExecution":
		statusLine := it.Status
		if it.ExitCode != nil {
			statusLine = fmt.Sprintf("%s (exit %d)", it.Status, *it.ExitCode)
		}
		card := a2ui.CommandCard(it.ID, it.Command, it.AggregatedOutput, statusLine)
		result := it.AggregatedOutput
		if result == "" {
			result = statusLine
		}
		return Result{Events: []events.Event{
			events.NewToolCallStartEvent(it.ID, "shell"),
			events.NewToolCallArgsEvent(it.ID, it.Command),
			events.NewToolCallEndEvent(it.ID),
			events.NewToolCallResultEvent(it.ID, it.ID, result),
			events.NewCustomEvent("a2ui.operations", events.WithValue(card)),
		}}
	case "fileChange":
		files := make([]a2ui.FileChange, 0, len(it.Changes))
		var argsB strings.Builder
		for _, c := range it.Changes {
			files = append(files, a2ui.FileChange{Path: c.Path, Kind: c.Kind.Type, Diff: c.Diff})
			fmt.Fprintf(&argsB, "%s %s\n", c.Kind.Type, c.Path)
		}
		card := a2ui.FileDiffCard(it.ID, files)
		args := strings.TrimSpace(argsB.String())
		if args == "" {
			args = "no changes"
		}
		return Result{Events: []events.Event{
			events.NewToolCallStartEvent(it.ID, "apply_patch"),
			events.NewToolCallArgsEvent(it.ID, args),
			events.NewToolCallEndEvent(it.ID),
			events.NewToolCallResultEvent(it.ID, it.ID, fmt.Sprintf("%d file(s) changed", len(it.Changes))),
			events.NewCustomEvent("a2ui.operations", events.WithValue(card)),
		}}
	default:
		slog.Warn("browser-gateway/mapper: unmapped item type", "type", it.Type)
		return Result{}
	}
}
