package harnesspool

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/braincatalog"
	"github.com/agentserver/agentserver/v2/internal/executorgateway/mcpcontract"
	"github.com/agentserver/agentserver/v2/internal/harnesscontrol"
	"github.com/agentserver/agentserver/v2/internal/runevent"
)

const (
	maximumInlineProjectionText = 32 * 1024
	maximumCommandCardOutput    = 24 * 1024
)

type mappedRuntimeEvent struct {
	Source  string
	Kind    string
	Payload json.RawMessage
}

type runtimeMessageState struct {
	sawDelta bool
}

type runtimeReasoningState struct {
	sawDelta bool
}

type runtimeToolState struct {
	name      string
	tool      string
	arguments json.RawMessage
}

// runtimeEventMapper is an attempt-local, deterministic stock-Codex adapter.
// It owns only ephemeral lifecycle validation; mapped facts become durable
// only after attemptLifecycleAuthority appends them through core.
type runtimeEventMapper struct {
	threadID string
	turnID   string
	catalog  *braincatalog.Catalog

	messages  map[string]*runtimeMessageState
	reasoning map[string]*runtimeReasoningState
	toolCalls map[string]runtimeToolState
	terminal  bool
}

func newRuntimeEventMapper(threadID, turnID string, frozen BrainToolCatalog) (*runtimeEventMapper, error) {
	if err := validateRuntimeIdentifier("thread ID", threadID); err != nil {
		return nil, err
	}
	if err := validateRuntimeIdentifier("turn ID", turnID); err != nil {
		return nil, err
	}
	if frozen.ContractVersion != mcpcontract.Version || frozen.CanonicalizerVersion != braincatalog.CatalogCanonicalizer {
		return nil, errors.New("runtime event catalog contract does not match the pinned executor mapper")
	}
	if frozen.ThreadID != threadID {
		return nil, errors.New("runtime event thread does not match the bound executor catalog")
	}
	catalog, err := braincatalog.ParseCanonical(frozen.CanonicalCatalog, braincatalog.DefaultLimits())
	if err != nil {
		return nil, fmt.Errorf("parse frozen runtime event catalog: %w", err)
	}
	if catalog.Namespace() != mcpcontract.Namespace || catalog.DigestSHA256() != frozen.CatalogDigest {
		return nil, errors.New("runtime event catalog does not match the frozen executor catalog")
	}
	return &runtimeEventMapper{
		threadID: threadID, turnID: turnID, catalog: catalog,
		messages:  make(map[string]*runtimeMessageState),
		reasoning: make(map[string]*runtimeReasoningState),
		toolCalls: make(map[string]runtimeToolState),
	}, nil
}

func (mapper *runtimeEventMapper) Map(event harnesscontrol.Event) ([]mappedRuntimeEvent, error) {
	if mapper == nil {
		return nil, errors.New("runtime event mapper is required")
	}
	if mapper.terminal {
		return nil, errors.New("runtime event arrived after stock turn terminal")
	}
	switch event.Kind {
	case harnesscontrol.EventKindAppServerNotification:
		if event.AppServerNotification == nil {
			return nil, errors.New("app-server runtime event payload is missing")
		}
		return mapper.mapAppServer(*event.AppServerNotification)
	case harnesscontrol.EventKindExecutorMCPProgress:
		if event.ExecutorMCPProgress == nil {
			return nil, errors.New("executor MCP progress payload is missing")
		}
		return mapper.mapProgress(*event.ExecutorMCPProgress)
	default:
		return nil, fmt.Errorf("event kind %q is not a runtime event", event.Kind)
	}
}

func (mapper *runtimeEventMapper) mapAppServer(event harnesscontrol.AppServerNotificationEvent) ([]mappedRuntimeEvent, error) {
	switch event.Method {
	case "item/started":
		return mapper.mapItemStarted(event.Params)
	case "item/completed":
		return mapper.mapItemCompleted(event.Params)
	case "item/agentMessage/delta":
		return mapper.mapAgentDelta(event.Params)
	case "item/reasoning/summaryTextDelta":
		return mapper.mapReasoningDelta(event.Params)
	case "item/reasoning/summaryPartAdded", "item/reasoning/textDelta":
		if err := mapper.validateDeltaScope(event.Params); err != nil {
			return nil, err
		}
		// Raw reasoning text is deliberately not a browser projection. The
		// readable reasoning summary has its own notification stream.
		return nil, nil
	case "turn/completed":
		return mapper.mapTurnCompleted(event.Params)
	default:
		return nil, fmt.Errorf("app-server notification method %q is outside the pinned runtime event profile", event.Method)
	}
}

type appItemEnvelope struct {
	ThreadID string          `json:"threadId"`
	TurnID   string          `json:"turnId"`
	Item     json.RawMessage `json:"item"`
}

type appItemDiscriminator struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

func (mapper *runtimeEventMapper) mapItemStarted(raw json.RawMessage) ([]mappedRuntimeEvent, error) {
	envelope, itemType, err := mapper.decodeItemEnvelope(raw)
	if err != nil {
		return nil, err
	}
	switch itemType.Type {
	case "agentMessage":
		if _, exists := mapper.messages[itemType.ID]; exists {
			return nil, fmt.Errorf("agent message %q started more than once", itemType.ID)
		}
		mapper.messages[itemType.ID] = &runtimeMessageState{}
		return mappedPayload("brain", runevent.KindAssistantMessageStarted, runevent.MessageStartedPayload{
			MessageID: itemType.ID, Role: "assistant",
		})
	case "reasoning":
		if _, exists := mapper.reasoning[itemType.ID]; exists {
			return nil, fmt.Errorf("reasoning item %q started more than once", itemType.ID)
		}
		mapper.reasoning[itemType.ID] = &runtimeReasoningState{}
		return mappedPayload("brain", runevent.KindAssistantReasoningStarted, runevent.MessageStartedPayload{
			MessageID: itemType.ID, Role: "assistant",
		})
	case "dynamicToolCall":
		var item dynamicToolItem
		if err := json.Unmarshal(envelope.Item, &item); err != nil {
			return nil, fmt.Errorf("decode dynamic tool start: %w", err)
		}
		state, err := mapper.validateDynamicTool(item, "inProgress")
		if err != nil {
			return nil, err
		}
		if _, exists := mapper.toolCalls[item.ID]; exists {
			return nil, fmt.Errorf("dynamic tool call %q started more than once", item.ID)
		}
		mapper.toolCalls[item.ID] = state
		started, err := mappedPayload("brain", runevent.KindToolCallStarted, runevent.ToolCallStartedPayload{
			ToolCallID: item.ID, ToolCallName: state.name,
		})
		if err != nil {
			return nil, err
		}
		arguments, err := mappedPayload("brain", runevent.KindToolCallArguments, runevent.ToolCallArgumentsPayload{
			ToolCallID: item.ID, Delta: boundedProjectionJSON(state.arguments),
		})
		if err != nil {
			return nil, err
		}
		return append(started, arguments...), nil
	case "userMessage":
		return nil, nil
	default:
		return nil, fmt.Errorf("app-server item type %q is outside the dynamic-only runtime profile", itemType.Type)
	}
}

func (mapper *runtimeEventMapper) mapItemCompleted(raw json.RawMessage) ([]mappedRuntimeEvent, error) {
	envelope, itemType, err := mapper.decodeItemEnvelope(raw)
	if err != nil {
		return nil, err
	}
	switch itemType.Type {
	case "agentMessage":
		state := mapper.messages[itemType.ID]
		if state == nil {
			return nil, fmt.Errorf("agent message %q completed before start", itemType.ID)
		}
		var item struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(envelope.Item, &item); err != nil {
			return nil, fmt.Errorf("decode completed agent message: %w", err)
		}
		result := make([]mappedRuntimeEvent, 0, 2)
		if !state.sawDelta && item.Text != "" {
			delta, err := mappedPayload("brain", runevent.KindAssistantMessageDelta, runevent.MessageDeltaPayload{
				MessageID: itemType.ID, Delta: boundedProjectionText(item.Text),
			})
			if err != nil {
				return nil, err
			}
			result = append(result, delta...)
		}
		completed, err := mappedPayload("brain", runevent.KindAssistantMessageCompleted, runevent.MessageCompletedPayload{MessageID: itemType.ID})
		if err != nil {
			return nil, err
		}
		delete(mapper.messages, itemType.ID)
		return append(result, completed...), nil
	case "reasoning":
		state := mapper.reasoning[itemType.ID]
		if state == nil {
			return nil, fmt.Errorf("reasoning item %q completed before start", itemType.ID)
		}
		var item struct {
			Summary []string `json:"summary"`
		}
		if err := json.Unmarshal(envelope.Item, &item); err != nil {
			return nil, fmt.Errorf("decode completed reasoning item: %w", err)
		}
		result := make([]mappedRuntimeEvent, 0, 2)
		if !state.sawDelta {
			if summary := strings.TrimSpace(strings.Join(item.Summary, "\n")); summary != "" {
				delta, err := mappedPayload("brain", runevent.KindAssistantReasoningDelta, runevent.MessageDeltaPayload{
					MessageID: itemType.ID, Delta: boundedProjectionText(summary),
				})
				if err != nil {
					return nil, err
				}
				result = append(result, delta...)
			}
		}
		completed, err := mappedPayload("brain", runevent.KindAssistantReasoningDone, runevent.MessageCompletedPayload{MessageID: itemType.ID})
		if err != nil {
			return nil, err
		}
		delete(mapper.reasoning, itemType.ID)
		return append(result, completed...), nil
	case "dynamicToolCall":
		var item dynamicToolItem
		if err := json.Unmarshal(envelope.Item, &item); err != nil {
			return nil, fmt.Errorf("decode completed dynamic tool: %w", err)
		}
		state, exists := mapper.toolCalls[item.ID]
		if !exists {
			return nil, fmt.Errorf("dynamic tool call %q completed before start", item.ID)
		}
		completedState, err := mapper.validateDynamicTool(item, "")
		if err != nil {
			return nil, err
		}
		if completedState.name != state.name || !jsonBytesEqual(completedState.arguments, state.arguments) {
			return nil, fmt.Errorf("dynamic tool call %q changed tool identity or arguments", item.ID)
		}
		if item.Status != "completed" && item.Status != "failed" {
			return nil, fmt.Errorf("dynamic tool call %q completed with status %q", item.ID, item.Status)
		}
		content, err := dynamicToolDisplayContent(item)
		if err != nil {
			return nil, err
		}
		finished, err := mappedPayload("brain", runevent.KindToolCallCompleted, runevent.ToolCallCompletedPayload{ToolCallID: item.ID})
		if err != nil {
			return nil, err
		}
		payload := runevent.ToolCallResultPayload{
			MessageID: item.ID, ToolCallID: item.ID, Content: boundedProjectionText(content),
		}
		if state.tool == mcpcontract.ToolShell {
			payload.Presentation = shellPresentation(state.arguments, item.ContentItems)
		}
		result, err := mappedPayload("brain", runevent.KindToolCallResult, payload)
		if err != nil {
			return nil, err
		}
		delete(mapper.toolCalls, item.ID)
		return append(finished, result...), nil
	case "userMessage":
		return nil, nil
	default:
		return nil, fmt.Errorf("app-server item type %q is outside the dynamic-only runtime profile", itemType.Type)
	}
}

type dynamicToolContentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type dynamicToolItem struct {
	Type         string                   `json:"type"`
	ID           string                   `json:"id"`
	Namespace    *string                  `json:"namespace"`
	Tool         string                   `json:"tool"`
	Arguments    json.RawMessage          `json:"arguments"`
	Status       string                   `json:"status"`
	ContentItems []dynamicToolContentItem `json:"contentItems"`
	Success      *bool                    `json:"success"`
}

func (mapper *runtimeEventMapper) validateDynamicTool(item dynamicToolItem, wantStatus string) (runtimeToolState, error) {
	if item.Type != "dynamicToolCall" {
		return runtimeToolState{}, errors.New("item is not a dynamicToolCall")
	}
	if err := validateRuntimeIdentifier("dynamic tool call ID", item.ID); err != nil {
		return runtimeToolState{}, err
	}
	if item.Namespace == nil {
		return runtimeToolState{}, errors.New("dynamic tool call namespace is not the frozen executor namespace")
	}
	if wantStatus != "" && item.Status != wantStatus {
		return runtimeToolState{}, fmt.Errorf("dynamic tool call %q status = %q, want %q", item.ID, item.Status, wantStatus)
	}
	if wantStatus == "inProgress" {
		if item.Success != nil || item.ContentItems != nil {
			return runtimeToolState{}, fmt.Errorf("dynamic tool call %q start already contains a terminal result", item.ID)
		}
	} else {
		if item.Success == nil || item.ContentItems == nil || *item.Success != (item.Status == "completed") {
			return runtimeToolState{}, fmt.Errorf("dynamic tool call %q terminal status, success, and contentItems are inconsistent", item.ID)
		}
	}
	canonical, err := mapper.catalog.ValidateCall(*item.Namespace, item.Tool, item.Arguments)
	if err != nil {
		return runtimeToolState{}, err
	}
	return runtimeToolState{
		name: mcpcontract.Namespace + "." + item.Tool, tool: item.Tool,
		arguments: append(json.RawMessage(nil), canonical...),
	}, nil
}

func (mapper *runtimeEventMapper) mapAgentDelta(raw json.RawMessage) ([]mappedRuntimeEvent, error) {
	params, err := mapper.decodeDelta(raw)
	if err != nil {
		return nil, err
	}
	state := mapper.messages[params.ItemID]
	if state == nil {
		return nil, fmt.Errorf("agent message %q delta arrived before start", params.ItemID)
	}
	if params.Delta == "" {
		return nil, nil
	}
	state.sawDelta = true
	return mappedPayload("brain", runevent.KindAssistantMessageDelta, runevent.MessageDeltaPayload{
		MessageID: params.ItemID, Delta: boundedProjectionText(params.Delta),
	})
}

func (mapper *runtimeEventMapper) mapReasoningDelta(raw json.RawMessage) ([]mappedRuntimeEvent, error) {
	params, err := mapper.decodeDelta(raw)
	if err != nil {
		return nil, err
	}
	state := mapper.reasoning[params.ItemID]
	if state == nil {
		return nil, fmt.Errorf("reasoning item %q delta arrived before start", params.ItemID)
	}
	if params.Delta == "" {
		return nil, nil
	}
	state.sawDelta = true
	return mappedPayload("brain", runevent.KindAssistantReasoningDelta, runevent.MessageDeltaPayload{
		MessageID: params.ItemID, Delta: boundedProjectionText(params.Delta),
	})
}

type appDeltaParams struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	ItemID   string `json:"itemId"`
	Delta    string `json:"delta"`
}

func (mapper *runtimeEventMapper) decodeDelta(raw json.RawMessage) (appDeltaParams, error) {
	var params appDeltaParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return appDeltaParams{}, fmt.Errorf("decode app-server item delta: %w", err)
	}
	if err := mapper.validateScope(params.ThreadID, params.TurnID); err != nil {
		return appDeltaParams{}, err
	}
	if err := validateRuntimeIdentifier("delta item ID", params.ItemID); err != nil {
		return appDeltaParams{}, err
	}
	if !utf8.ValidString(params.Delta) {
		return appDeltaParams{}, errors.New("app-server delta is not valid UTF-8")
	}
	return params, nil
}

func (mapper *runtimeEventMapper) validateDeltaScope(raw json.RawMessage) error {
	var params struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		ItemID   string `json:"itemId"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return fmt.Errorf("decode app-server reasoning notification: %w", err)
	}
	if err := mapper.validateScope(params.ThreadID, params.TurnID); err != nil {
		return err
	}
	if err := validateRuntimeIdentifier("reasoning item ID", params.ItemID); err != nil {
		return err
	}
	if mapper.reasoning[params.ItemID] == nil {
		return fmt.Errorf("reasoning item %q delta arrived before start", params.ItemID)
	}
	return nil
}

func (mapper *runtimeEventMapper) mapProgress(event harnesscontrol.ExecutorMCPProgressEvent) ([]mappedRuntimeEvent, error) {
	if _, active := mapper.toolCalls[event.CallID]; !active {
		return nil, fmt.Errorf("executor MCP progress for call %q arrived outside its dynamic tool lifecycle", event.CallID)
	}
	return mappedPayload("executor", runevent.KindToolCallProgress, runevent.ToolCallProgressPayload{
		ToolCallID: event.CallID, Progress: event.Progress, Total: event.Total, Message: event.Message,
	})
}

func (mapper *runtimeEventMapper) mapTurnCompleted(raw json.RawMessage) ([]mappedRuntimeEvent, error) {
	var params struct {
		ThreadID string `json:"threadId"`
		Turn     struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, fmt.Errorf("decode app-server turn/completed: %w", err)
	}
	if err := mapper.validateScope(params.ThreadID, params.Turn.ID); err != nil {
		return nil, err
	}
	switch params.Turn.Status {
	case "completed", "interrupted", "failed":
	default:
		return nil, fmt.Errorf("app-server turn/completed status %q is not terminal", params.Turn.Status)
	}
	unfinished := len(mapper.messages) != 0 || len(mapper.reasoning) != 0 || len(mapper.toolCalls) != 0
	if params.Turn.Status == "completed" {
		if unfinished {
			return nil, errors.New("app-server turn completed with unfinished canonical item lifecycles")
		}
		mapper.terminal = true
		return nil, nil
	}

	// Stock app-server can terminate an interrupted or failed turn without
	// emitting item/completed for items that were active at the terminal
	// boundary. Close only their browser projection lifecycles. In particular,
	// an unfinished dynamic tool gets no executor result or command
	// presentation: the synthetic result records only why its projection ended.
	// Map iteration is sorted so one terminal notification always produces the
	// same canonical event sequence.
	mapped := make([]mappedRuntimeEvent, 0, len(mapper.messages)+len(mapper.reasoning)+2*len(mapper.toolCalls))
	messageIDs := make([]string, 0, len(mapper.messages))
	for id := range mapper.messages {
		messageIDs = append(messageIDs, id)
	}
	sort.Strings(messageIDs)
	for _, id := range messageIDs {
		completed, err := mappedPayload("brain", runevent.KindAssistantMessageCompleted, runevent.MessageCompletedPayload{MessageID: id})
		if err != nil {
			return nil, err
		}
		mapped = append(mapped, completed...)
	}

	reasoningIDs := make([]string, 0, len(mapper.reasoning))
	for id := range mapper.reasoning {
		reasoningIDs = append(reasoningIDs, id)
	}
	sort.Strings(reasoningIDs)
	for _, id := range reasoningIDs {
		done, err := mappedPayload("brain", runevent.KindAssistantReasoningDone, runevent.MessageCompletedPayload{MessageID: id})
		if err != nil {
			return nil, err
		}
		mapped = append(mapped, done...)
	}

	toolIDs := make([]string, 0, len(mapper.toolCalls))
	for id := range mapper.toolCalls {
		toolIDs = append(toolIDs, id)
	}
	sort.Strings(toolIDs)
	for _, id := range toolIDs {
		completed, err := mappedPayload("brain", runevent.KindToolCallCompleted, runevent.ToolCallCompletedPayload{ToolCallID: id})
		if err != nil {
			return nil, err
		}
		result, err := mappedPayload("brain", runevent.KindToolCallResult, runevent.ToolCallResultPayload{
			MessageID: id, ToolCallID: id, Content: unfinishedToolProjectionResult(params.Turn.Status),
		})
		if err != nil {
			return nil, err
		}
		mapped = append(mapped, completed...)
		mapped = append(mapped, result...)
	}

	clear(mapper.messages)
	clear(mapper.reasoning)
	clear(mapper.toolCalls)
	mapper.terminal = true
	return mapped, nil
}

func unfinishedToolProjectionResult(turnStatus string) string {
	if turnStatus == "interrupted" {
		return "dynamic tool projection closed because the stock turn was interrupted before emitting a terminal result"
	}
	return "dynamic tool projection closed because the stock turn failed before emitting a terminal result"
}

func (mapper *runtimeEventMapper) decodeItemEnvelope(raw json.RawMessage) (appItemEnvelope, appItemDiscriminator, error) {
	var envelope appItemEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return appItemEnvelope{}, appItemDiscriminator{}, fmt.Errorf("decode app-server item envelope: %w", err)
	}
	if err := mapper.validateScope(envelope.ThreadID, envelope.TurnID); err != nil {
		return appItemEnvelope{}, appItemDiscriminator{}, err
	}
	if len(envelope.Item) == 0 || string(envelope.Item) == "null" {
		return appItemEnvelope{}, appItemDiscriminator{}, errors.New("app-server item envelope has no item")
	}
	var itemType appItemDiscriminator
	if err := json.Unmarshal(envelope.Item, &itemType); err != nil {
		return appItemEnvelope{}, appItemDiscriminator{}, fmt.Errorf("decode app-server item discriminator: %w", err)
	}
	if itemType.Type == "" {
		return appItemEnvelope{}, appItemDiscriminator{}, errors.New("app-server item type is required")
	}
	if err := validateRuntimeIdentifier("app-server item ID", itemType.ID); err != nil {
		return appItemEnvelope{}, appItemDiscriminator{}, err
	}
	return envelope, itemType, nil
}

func (mapper *runtimeEventMapper) validateScope(threadID, turnID string) error {
	if threadID != mapper.threadID || turnID != mapper.turnID {
		return errors.New("app-server runtime event escaped the accepted thread or turn")
	}
	return nil
}

func mappedPayload(source, kind string, payload any) ([]mappedRuntimeEvent, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode canonical %s payload: %w", kind, err)
	}
	if len(raw) > runevent.MaxInlinePayloadBytes {
		return nil, fmt.Errorf("canonical %s payload exceeds the inline event limit", kind)
	}
	if _, err := runevent.DecodeSemanticPayload(runevent.Event{
		SchemaVersion: runevent.CurrentSchemaVersion, Kind: kind, Payload: raw,
	}); err != nil {
		return nil, err
	}
	return []mappedRuntimeEvent{{Source: source, Kind: kind, Payload: raw}}, nil
}

func validateRuntimeIdentifier(label, value string) error {
	if value == "" || len(value) > 256 || !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n\t ") {
		return fmt.Errorf("%s must contain between 1 and 256 UTF-8 bytes without whitespace or NUL", label)
	}
	return nil
}

func boundedProjectionText(value string) string {
	return boundedProjectionTextAt(value, maximumInlineProjectionText)
}

func boundedProjectionTextAt(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	digest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("[inline projection omitted: %d bytes, sha256=%s]", len(value), hex.EncodeToString(digest[:]))
}

func boundedProjectionJSON(value json.RawMessage) string {
	if len(value) <= maximumInlineProjectionText {
		return string(value)
	}
	digest := sha256.Sum256(value)
	raw, _ := json.Marshal(map[string]any{
		"omitted": true, "size": len(value), "sha256": hex.EncodeToString(digest[:]),
	})
	return string(raw)
}

func dynamicToolDisplayContent(item dynamicToolItem) (string, error) {
	parts := make([]string, 0, len(item.ContentItems))
	for index, content := range item.ContentItems {
		if content.Type != "inputText" || !utf8.ValidString(content.Text) {
			return "", fmt.Errorf("dynamic tool result content item %d is not bounded inputText", index)
		}
		parts = append(parts, content.Text)
	}
	if len(parts) != 0 {
		return strings.Join(parts, "\n"), nil
	}
	if item.Status == "failed" || item.Success != nil && !*item.Success {
		return "dynamic tool call failed without inline result content", nil
	}
	return "dynamic tool call completed without inline result content", nil
}

func jsonBytesEqual(left, right json.RawMessage) bool {
	return string(left) == string(right)
}

type shellResult struct {
	Status     string `json:"status"`
	ReasonCode string `json:"reason_code"`
	Chunks     []struct {
		Sequence    uint64 `json:"sequence"`
		Stream      string `json:"stream"`
		ChunkBase64 string `json:"chunk_base64"`
	} `json:"chunks"`
	ExitCode       *int32 `json:"exit_code"`
	OutputComplete bool   `json:"output_complete"`
}

func shellPresentation(arguments json.RawMessage, contents []dynamicToolContentItem) *runevent.ToolPresentation {
	var input struct {
		Argv []string `json:"argv"`
	}
	if json.Unmarshal(arguments, &input) != nil || len(input.Argv) == 0 || len(contents) == 0 {
		return nil
	}
	var result shellResult
	if json.Unmarshal([]byte(contents[len(contents)-1].Text), &result) != nil {
		return nil
	}
	sort.Slice(result.Chunks, func(i, j int) bool { return result.Chunks[i].Sequence < result.Chunks[j].Sequence })
	var output []byte
	var previous uint64
	for _, chunk := range result.Chunks {
		if chunk.Sequence == 0 || chunk.Sequence <= previous {
			return nil
		}
		decoded, err := base64.StdEncoding.Strict().DecodeString(chunk.ChunkBase64)
		if err != nil {
			return nil
		}
		output = append(output, decoded...)
		previous = chunk.Sequence
	}
	outputText := string(output)
	if !utf8.Valid(output) {
		outputText = "base64:" + base64.StdEncoding.EncodeToString(output)
	}
	if len(outputText) > maximumCommandCardOutput {
		outputText = boundedProjectionTextAt(outputText, maximumCommandCardOutput)
	}
	command, _ := json.Marshal(input.Argv)
	status := result.Status
	if result.ReasonCode != "" {
		status += " (" + result.ReasonCode + ")"
	}
	if result.ExitCode != nil {
		status = fmt.Sprintf("%s (exit %d)", status, *result.ExitCode)
	}
	if !result.OutputComplete {
		status += " (output incomplete)"
	}
	return &runevent.ToolPresentation{
		Kind: "command",
		Command: &runevent.CommandPresentation{
			Command: string(command), Output: outputText, Status: status,
		},
	}
}
