package browsergateway

import (
	"context"
	"encoding/json"
	"sort"

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

func (state *dshSessionState) appendJournalPrompt(message corecontract.UserSessionTranscriptMessage, requestID string) {
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
	now := message.CreatedAt.UnixMilli()
	state.appendLocked("turn/start", now, map[string]any{"turn": turn})
	state.appendLocked("step/start", now, map[string]any{"turn": turn, "step": 1})
	state.appendLocked("user/message", now, map[string]any{
		"role": "user", "content": []any{map[string]any{"type": "text", "text": message.Content}},
		"source": map[string]any{"kind": "user", "rpcId": requestID}, "id": message.MessageID,
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
		builder := &dshAssistantBuilder{
			id: value.MessageID, attemptID: dshAssistantAttemptID(event, value.MessageID),
			reasoning: event.Kind == runevent.KindAssistantReasoningStarted, turn: turn, step: step,
			startedAfterSeq: state.nextSeq - 1,
		}
		state.builders[value.MessageID] = builder
		state.emitAssistantStartLocked(builder)
		state.emitAssistantChunkLocked(builder, event.CreatedAt.UnixMilli(), map[string]any{
			"type": "block-start", "index": 0, "blockType": assistantBlockType(builder),
		})
	case runevent.KindAssistantMessageDelta, runevent.KindAssistantReasoningDelta:
		value := payload.(runevent.MessageDeltaPayload)
		builder := state.builders[value.MessageID]
		if builder == nil {
			builder = &dshAssistantBuilder{
				id: value.MessageID, attemptID: dshAssistantAttemptID(event, value.MessageID),
				reasoning: event.Kind == runevent.KindAssistantReasoningDelta, turn: turn, step: step,
				startedAfterSeq: state.nextSeq - 1,
			}
			state.builders[value.MessageID] = builder
			state.emitAssistantStartLocked(builder)
			state.emitAssistantChunkLocked(builder, event.CreatedAt.UnixMilli(), map[string]any{
				"type": "block-start", "index": 0, "blockType": assistantBlockType(builder),
			})
		}
		builder.text.WriteString(value.Delta)
		chunkType := "text-delta"
		if builder.reasoning {
			chunkType = "reasoning-delta"
		}
		state.emitAssistantChunkLocked(builder, event.CreatedAt.UnixMilli(), map[string]any{
			"type": chunkType, "index": 0, "text": value.Delta,
		})
	case runevent.KindAssistantMessageCompleted, runevent.KindAssistantReasoningDone:
		value := payload.(runevent.MessageCompletedPayload)
		builder := state.builders[value.MessageID]
		if builder == nil {
			builder = &dshAssistantBuilder{
				id: value.MessageID, attemptID: dshAssistantAttemptID(event, value.MessageID),
				reasoning: event.Kind == runevent.KindAssistantReasoningDone, turn: turn, step: step,
				startedAfterSeq: state.nextSeq - 1,
			}
			state.builders[value.MessageID] = builder
			state.emitAssistantStartLocked(builder)
			state.emitAssistantChunkLocked(builder, event.CreatedAt.UnixMilli(), map[string]any{
				"type": "block-start", "index": 0, "blockType": assistantBlockType(builder),
			})
		}
		contentType := "text"
		if builder.reasoning {
			contentType = "reasoning"
		}
		state.emitAssistantChunkLocked(builder, timeMS, map[string]any{
			"type": "block-end", "index": 0,
			"block": map[string]any{"type": contentType, "text": builder.text.String()},
		})
		state.emitAssistantChunkLocked(builder, timeMS, map[string]any{
			"type": "finish", "reason": map[string]any{"kind": "stop"},
		})
		stream := make([]any, 0, len(builder.stream))
		for _, record := range builder.stream {
			stream = append(stream, record)
		}
		settlement := state.appendLocked("assistant/message", timeMS, map[string]any{"turn": turn, "step": step, "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": contentType, "text": builder.text.String()}}, "source": map[string]any{"kind": "model", "provider": "agentserver", "model": "codex"}, "id": builder.id}, "stream": stream})
		state.emitAssistantEndLocked(builder, "committed", settlement.Seq, "assistant/message")
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
		state.abandonAssistantStreamsLocked()
		state.appendLocked("step/end", timeMS, map[string]any{"turn": turn, "step": step})
		reason := map[string]any{"kind": "completed"}
		if event.Kind != runevent.KindRunCompleted {
			reason = map[string]any{"kind": "interrupted"}
		}
		if event.Kind == runevent.KindRunFailed {
			failure := payload.(runevent.RunTerminalPayload)
			reason = map[string]any{"kind": "error", "error": map[string]any{"code": "UNKNOWN", "message": failure.Message}}
		}
		state.appendLocked("turn/end", timeMS, map[string]any{"turn": turn, "reason": reason})
		state.session.ActiveRunID = ""
	case runevent.KindRunCancelling:
		// Keep the turn open until Core commits its terminal cancelled or
		// interrupted event; DSH's journal must not close the same turn twice.
	}
}

func dshAssistantAttemptID(event runevent.Event, messageID string) string {
	// DSH only requires a stable opaque attempt identity within one stream. A
	// message-scoped identity keeps reasoning and answer settlements independent
	// even when Core reports several Assistant items under one run attempt.
	if messageID != "" {
		return "agentserver-" + messageID
	}
	if event.RunAttemptID != nil && *event.RunAttemptID != "" {
		return "agentserver-" + *event.RunAttemptID
	}
	return "agentserver-" + event.EventID
}

func assistantBlockType(builder *dshAssistantBuilder) string {
	if builder.reasoning {
		return "reasoning"
	}
	return "text"
}

func (state *dshSessionState) emitAssistantStartLocked(builder *dshAssistantBuilder) {
	state.emitAssistantFrameLocked(map[string]any{
		"type": "start", "attemptId": builder.attemptID,
		"startedAfterSeq": builder.startedAfterSeq, "turn": builder.turn, "step": builder.step,
	})
}

func (state *dshSessionState) emitAssistantChunkLocked(builder *dshAssistantBuilder, timeMS int64, chunk map[string]any) {
	index := builder.nextIndex
	builder.nextIndex++
	record := map[string]any{"type": "chunk", "time": timeMS, "chunk": chunk}
	builder.stream = append(builder.stream, record)
	state.emitAssistantFrameLocked(map[string]any{
		"type": "chunk", "attemptId": builder.attemptID, "index": index, "time": timeMS, "chunk": chunk,
	})
}

func (state *dshSessionState) emitAssistantEndLocked(builder *dshAssistantBuilder, outcomeKind string, seq int64, eventType string) {
	outcome := map[string]any{"kind": outcomeKind}
	if outcomeKind == "committed" {
		outcome["eventType"] = eventType
		outcome["seq"] = seq
	}
	state.emitAssistantFrameLocked(map[string]any{
		"type": "end", "attemptId": builder.attemptID, "index": builder.nextIndex, "outcome": outcome,
	})
}

func (state *dshSessionState) abandonAssistantStreamsLocked() {
	ids := make([]string, 0, len(state.builders))
	for id := range state.builders {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		builder := state.builders[id]
		state.emitAssistantEndLocked(builder, "abandoned", 0, "")
		delete(state.builders, id)
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

func (state *dshSessionState) appendLocked(kind string, timeMS int64, data any) dshEvent {
	event := dshEvent{Type: kind, Seq: state.nextSeq, Time: timeMS, Data: data}
	if kind == "user/message" || kind == "assistant/message" || kind == "tool/result" {
		event.SurfaceOp = "append"
	}
	state.nextSeq++
	state.events = append(state.events, event)
	for id, subscriber := range state.subs {
		select {
		case subscriber <- dshSessionUpdate{event: &event}:
		default:
			delete(state.subs, id)
			close(subscriber)
		}
	}
	return event
}

func (state *dshSessionState) emitAssistantFrameLocked(frame map[string]any) {
	// Revisions are process-local and monotonically increasing.  This is the
	// continuity token used by DSH followers to detect a dropped frame and
	// request a fresh baseline.
	state.assistantRevision++
	frame["revision"] = state.assistantRevision
	update := dshSessionUpdate{assistantFrame: frame}
	for id, subscriber := range state.subs {
		select {
		case subscriber <- update:
		default:
			delete(state.subs, id)
			close(subscriber)
		}
	}
}

func (state *dshSessionState) assistantBaseline() map[string]any {
	state.mu.Lock()
	defer state.mu.Unlock()
	baseline := map[string]any{"revision": state.assistantRevision}
	if len(state.builders) == 0 {
		return baseline
	}
	ids := make([]string, 0, len(state.builders))
	for id := range state.builders {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	builder := state.builders[ids[len(ids)-1]]
	stream := make([]any, 0, len(builder.stream))
	for _, record := range builder.stream {
		stream = append(stream, record)
	}
	baseline["activeAttempt"] = map[string]any{
		"attemptId": builder.attemptID, "startedAfterSeq": builder.startedAfterSeq,
		"turn": builder.turn, "step": builder.step, "nextIndex": builder.nextIndex,
		"stream": stream,
	}
	return baseline
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

func (state *dshSessionState) subscribe(ctx context.Context) (<-chan dshSessionUpdate, func()) {
	state.mu.Lock()
	id := state.nextSub
	state.nextSub++
	channel := make(chan dshSessionUpdate, 256)
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
