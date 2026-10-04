package browsergateway

import (
	"context"
	"encoding/json"
	"time"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/runevent"
)

func (state *dshSessionState) lastSeq() int64 {
	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.events) == 0 {
		return -1
	}
	return state.events[len(state.events)-1].Seq
}

func (state *dshSessionState) permissionMode() string {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.session.PermissionMode
}

func (state *dshSessionState) projection() (string, string, int64) {
	state.mu.Lock()
	defer state.mu.Unlock()
	seq := state.nextSeq - 1
	if len(state.events) == 0 {
		seq = -1
	}
	return state.session.SessionID, state.session.PermissionMode, seq
}

func (state *dshSessionState) terminal() bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.session.ActiveRunID == ""
}

func (state *dshSessionState) loadTranscript(transcript corecontract.GetUserSessionTranscriptResponse) {
	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.events) != 0 {
		return
	}
	turn := 0
	for _, message := range transcript.Messages {
		if message.Role == "user" {
			turn++
			state.appendLocked("turn/start", message.CreatedAt.UnixMilli(), map[string]any{"turn": turn})
			state.appendLocked("step/start", message.CreatedAt.UnixMilli(), map[string]any{"turn": turn, "step": 1})
			state.appendLocked("user/message", message.CreatedAt.UnixMilli(), map[string]any{
				"role": "user", "content": []any{map[string]any{"type": "text", "text": message.Content}},
				"source": map[string]any{"kind": "user"}, "id": message.MessageID,
			})
			continue
		}
		if message.Role == "assistant" {
			state.appendLocked("assistant/message", message.CreatedAt.UnixMilli(), map[string]any{
				"turn": turn, "step": 1, "message": map[string]any{
					"role": "assistant", "content": []any{map[string]any{"type": "text", "text": message.Content}},
					"source": map[string]any{"kind": "model", "provider": "agentserver", "model": "codex"}, "id": message.MessageID,
				}, "stream": []any{},
			})
			state.appendLocked("step/end", message.CreatedAt.UnixMilli(), map[string]any{"turn": turn, "step": 1})
			state.appendLocked("turn/end", message.CreatedAt.UnixMilli(), map[string]any{"turn": turn, "reason": map[string]any{"kind": "completed"}})
		}
	}
}

func (state *dshSessionState) appendSyntheticUser(text, requestID string) {
	state.mu.Lock()
	defer state.mu.Unlock()
	turn := 1
	for _, event := range state.events {
		if event.Type == "turn/start" {
			if value, ok := event.Data.(map[string]any); ok {
				if number, ok := value["turn"].(int); ok && number >= turn {
					turn = number + 1
				}
			}
		}
	}
	now := time.Now().UnixMilli()
	state.appendLocked("turn/start", now, map[string]any{"turn": turn})
	state.appendLocked("step/start", now, map[string]any{"turn": turn, "step": 1})
	state.appendLocked("user/message", now, map[string]any{
		"role": "user", "content": []any{map[string]any{"type": "text", "text": text}},
		"source": map[string]any{"kind": "user", "rpcId": requestID}, "id": requestID,
	})
}

func (state *dshSessionState) mapCanonical(event runevent.Event) {
	payload, err := runevent.DecodeSemanticPayload(event)
	if err != nil {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	timeMS := event.CreatedAt.UnixMilli()
	turn, step := 1, 1
	if len(state.events) > 0 {
		for index := len(state.events) - 1; index >= 0; index-- {
			if state.events[index].Type == "turn/start" {
				if values, ok := state.events[index].Data.(map[string]any); ok {
					if value, ok := values["turn"].(int); ok {
						turn = value
					}
				}
				break
			}
		}
	}
	switch event.Kind {
	case runevent.KindAssistantMessageStarted, runevent.KindAssistantReasoningStarted:
		value := payload.(runevent.MessageStartedPayload)
		state.builders[value.MessageID] = &dshAssistantBuilder{id: value.MessageID, reasoning: event.Kind == runevent.KindAssistantReasoningStarted}
	case runevent.KindAssistantMessageDelta, runevent.KindAssistantReasoningDelta:
		value := payload.(runevent.MessageDeltaPayload)
		builder := state.builders[value.MessageID]
		if builder == nil {
			builder = &dshAssistantBuilder{id: value.MessageID, reasoning: event.Kind == runevent.KindAssistantReasoningDelta}
			state.builders[value.MessageID] = builder
		}
		builder.text.WriteString(value.Delta)
	case runevent.KindAssistantMessageCompleted, runevent.KindAssistantReasoningDone:
		value := payload.(runevent.MessageCompletedPayload)
		builder := state.builders[value.MessageID]
		if builder == nil {
			return
		}
		contentType := "text"
		if builder.reasoning {
			contentType = "reasoning"
		}
		state.appendLocked("assistant/message", timeMS, map[string]any{"turn": turn, "step": step, "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": contentType, "text": builder.text.String()}}, "source": map[string]any{"kind": "model", "provider": "agentserver", "model": "codex"}, "id": builder.id}, "stream": []any{}})
		delete(state.builders, value.MessageID)
	case runevent.KindToolCallStarted:
		value := payload.(runevent.ToolCallStartedPayload)
		state.tools[value.ToolCallID] = &dshToolBuilder{id: value.ToolCallID, name: value.ToolCallName}
	case runevent.KindToolCallArguments:
		value := payload.(runevent.ToolCallArgumentsPayload)
		tool := state.tools[value.ToolCallID]
		if tool == nil {
			tool = &dshToolBuilder{id: value.ToolCallID}
			state.tools[value.ToolCallID] = tool
		}
		tool.arguments.WriteString(value.Delta)
	case runevent.KindToolCallCompleted:
		value := payload.(runevent.ToolCallCompletedPayload)
		tool := state.tools[value.ToolCallID]
		if tool == nil {
			return
		}
		if !tool.callEmitted {
			state.appendLocked("tool/call", timeMS, map[string]any{"turn": turn, "step": step, "callId": tool.id, "name": tool.name, "arguments": validToolArguments(tool.arguments.String())})
			tool.callEmitted = true
		}
	case runevent.KindToolCallResult:
		value := payload.(runevent.ToolCallResultPayload)
		tool := state.tools[value.ToolCallID]
		if tool != nil && !tool.callEmitted {
			state.appendLocked("tool/call", timeMS, map[string]any{"turn": turn, "step": step, "callId": tool.id, "name": tool.name, "arguments": validToolArguments(tool.arguments.String())})
			tool.callEmitted = true
		}
		state.appendLocked("tool/result", timeMS, map[string]any{"turn": turn, "step": step, "message": map[string]any{"id": value.MessageID, "role": "tool", "content": []any{map[string]any{"type": "text", "text": value.Content}}, "source": map[string]any{"kind": "tool", "callId": value.ToolCallID}, "toolCallId": value.ToolCallID}})
	case runevent.KindRunCompleted, runevent.KindRunFailed, runevent.KindRunInterrupted, runevent.KindRunCancelled:
		state.appendLocked("step/end", timeMS, map[string]any{"turn": turn, "step": step})
		reason := map[string]any{"kind": "completed"}
		if event.Kind != runevent.KindRunCompleted {
			reason = map[string]any{"kind": "interrupted"}
		}
		state.appendLocked("turn/end", timeMS, map[string]any{"turn": turn, "reason": reason})
		state.session.ActiveRunID = ""
	case runevent.KindRunCancelling:
		// Keep the turn open until Core commits its terminal cancelled or
		// interrupted event; DSH's journal must not close the same turn twice.
	}
}

func validToolArguments(raw string) string {
	if raw == "" {
		return "{}"
	}
	var value any
	if json.Unmarshal([]byte(raw), &value) != nil {
		return "{}"
	}
	return raw
}

func (state *dshSessionState) appendLocked(kind string, timeMS int64, data any) {
	event := dshEvent{Type: kind, Seq: state.nextSeq, Time: timeMS, Data: data}
	if kind == "user/message" || kind == "assistant/message" || kind == "tool/result" {
		event.SurfaceOp = "append"
	}
	state.nextSeq++
	state.events = append(state.events, event)
	for _, subscriber := range state.subs {
		select {
		case subscriber <- event:
		default:
		}
	}
}

func (state *dshSessionState) page(before, through *int64, max int) ([]map[string]any, bool) {
	state.mu.Lock()
	defer state.mu.Unlock()
	limit := max
	if limit <= 0 {
		limit = 500
	}
	result := make([]map[string]any, 0, limit+1)
	hasMore := false
	for index := len(state.events) - 1; index >= 0; index-- {
		event := state.events[index]
		if through != nil && event.Seq > *through {
			continue
		}
		if before != nil && event.Seq >= *before {
			continue
		}
		if len(result) >= limit {
			hasMore = true
			break
		}
		result = append(result, map[string]any{"type": "event", "event": event})
	}
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result, hasMore
}

func (state *dshSessionState) snapshot() ([]dshEvent, dshSessionHeader, int64) {
	state.mu.Lock()
	defer state.mu.Unlock()
	events := append([]dshEvent(nil), state.events...)
	header := dshSessionHeader{Version: 4, ID: state.session.SessionID, CreatedAt: state.session.CreatedAt.UnixMilli(), Cwd: state.session.WorkingDirectory, IsSeeded: false}
	cursor := state.nextSeq - 1
	if len(events) == 0 {
		cursor = -1
	}
	return events, header, cursor
}

func (state *dshSessionState) subscribe(ctx context.Context) (<-chan dshEvent, func()) {
	state.mu.Lock()
	id := state.nextSub
	state.nextSub++
	channel := make(chan dshEvent, 128)
	state.subs[id] = channel
	state.mu.Unlock()
	stop := func() {
		state.mu.Lock()
		if current, ok := state.subs[id]; ok {
			delete(state.subs, id)
			close(current)
		}
		state.mu.Unlock()
	}
	go func() { <-ctx.Done(); stop() }()
	return channel, stop
}

func eventValue(event dshEvent) map[string]any {
	return map[string]any{"type": "event", "event": event}
}
