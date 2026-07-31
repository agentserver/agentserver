package browsergateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/agentserver/agentserver/v2/internal/browsergateway/a2ui"
	"github.com/agentserver/agentserver/v2/internal/runevent"
)

type ProjectionScope struct {
	WorkspaceID string
	SessionID   string
	RunID       string
}

type Projection struct {
	Events   []events.Event
	Terminal bool
}

// Projector validates one ordered canonical run stream and projects it into
// AG-UI. It owns no durable state; its maps only protect one live SSE stream
// from emitting invalid message/tool lifecycles.
type Projector struct {
	scope             ProjectionScope
	lastSequence      int64
	terminal          bool
	activeMessages    map[string]struct{}
	activeReasoning   map[string]struct{}
	activeToolCalls   map[string]struct{}
	completedToolCall map[string]struct{}
}

// AtLifecycleBoundary reports whether replay can safely resume after the last
// projected canonical event without a state snapshot. browser-gateway only
// publishes durable client cursors at these points.
func (projector *Projector) AtLifecycleBoundary() bool {
	return projector != nil && !projector.terminal &&
		len(projector.activeMessages) == 0 && len(projector.activeReasoning) == 0 &&
		len(projector.activeToolCalls) == 0 && len(projector.completedToolCall) == 0
}

func NewProjector(scope ProjectionScope, afterSequence int64) (*Projector, error) {
	if scope.WorkspaceID == "" || scope.SessionID == "" || scope.RunID == "" {
		return nil, errors.New("projection workspace, session, and run scope are required")
	}
	if afterSequence < 0 || afterSequence >= 1<<53-1 {
		return nil, errors.New("projection sequence cursor is outside the JSON-safe range")
	}
	return &Projector{
		scope:             scope,
		lastSequence:      afterSequence,
		activeMessages:    make(map[string]struct{}),
		activeReasoning:   make(map[string]struct{}),
		activeToolCalls:   make(map[string]struct{}),
		completedToolCall: make(map[string]struct{}),
	}, nil
}

// Rebase resets process-local lifecycle tracking after an authorized snapshot.
// The backend must choose a rebase cursor at a canonical lifecycle boundary.
func (projector *Projector) Rebase(afterSequence int64) error {
	if afterSequence < projector.lastSequence || afterSequence >= 1<<53-1 {
		return errors.New("rebase sequence must advance within the JSON-safe range")
	}
	projector.lastSequence = afterSequence
	projector.activeMessages = make(map[string]struct{})
	projector.activeReasoning = make(map[string]struct{})
	projector.activeToolCalls = make(map[string]struct{})
	projector.completedToolCall = make(map[string]struct{})
	return nil
}

func (projector *Projector) Project(event runevent.Event) (Projection, error) {
	if projector.terminal {
		return Projection{}, errors.New("canonical event received after terminal run event")
	}
	if err := event.Validate(); err != nil {
		return Projection{}, fmt.Errorf("validate canonical event: %w", err)
	}
	if event.WorkspaceID != projector.scope.WorkspaceID || event.SessionID != projector.scope.SessionID || event.RunID != projector.scope.RunID {
		return Projection{}, errors.New("canonical event escaped the authorized projection scope")
	}
	expected := projector.lastSequence + 1
	if event.Seq != expected {
		return Projection{}, fmt.Errorf("canonical event sequence gap: got %d, want %d", event.Seq, expected)
	}
	if !runevent.IsKnownKind(event.Kind) {
		projector.lastSequence = event.Seq
		return Projection{}, nil
	}

	payload, err := runevent.DecodeSemanticPayload(event)
	if err != nil {
		return Projection{}, err
	}
	projection, err := projector.projectKnown(event, payload)
	if err != nil {
		return Projection{}, err
	}
	for index, projected := range projection.Events {
		projected.SetTimestamp(event.CreatedAt.UnixMilli())
		if err := projected.Validate(); err != nil {
			return Projection{}, fmt.Errorf("validate projected AG-UI event %d: %w", index, err)
		}
	}
	projector.lastSequence = event.Seq
	if projection.Terminal {
		projector.terminal = true
	}
	return projection, nil
}

func (projector *Projector) projectKnown(event runevent.Event, payload any) (Projection, error) {
	switch event.Kind {
	case runevent.KindAssistantMessageStarted:
		message := payload.(runevent.MessageStartedPayload)
		if _, exists := projector.activeMessages[message.MessageID]; exists {
			return Projection{}, fmt.Errorf("assistant message %q started more than once", message.MessageID)
		}
		projector.activeMessages[message.MessageID] = struct{}{}
		return Projection{Events: []events.Event{
			events.NewTextMessageStartEvent(message.MessageID, events.WithRole(message.Role)),
		}}, nil
	case runevent.KindAssistantMessageDelta:
		message := payload.(runevent.MessageDeltaPayload)
		if _, exists := projector.activeMessages[message.MessageID]; !exists {
			return Projection{}, fmt.Errorf("assistant message %q delta arrived before start", message.MessageID)
		}
		return Projection{Events: []events.Event{
			events.NewTextMessageContentEvent(message.MessageID, message.Delta),
		}}, nil
	case runevent.KindAssistantMessageCompleted:
		message := payload.(runevent.MessageCompletedPayload)
		if _, exists := projector.activeMessages[message.MessageID]; !exists {
			return Projection{}, fmt.Errorf("assistant message %q completed before start", message.MessageID)
		}
		delete(projector.activeMessages, message.MessageID)
		return Projection{Events: []events.Event{events.NewTextMessageEndEvent(message.MessageID)}}, nil
	case runevent.KindAssistantReasoningStarted:
		message := payload.(runevent.MessageStartedPayload)
		if _, exists := projector.activeReasoning[message.MessageID]; exists {
			return Projection{}, fmt.Errorf("reasoning message %q started more than once", message.MessageID)
		}
		projector.activeReasoning[message.MessageID] = struct{}{}
		return Projection{Events: []events.Event{
			events.NewReasoningMessageStartEvent(message.MessageID, message.Role),
		}}, nil
	case runevent.KindAssistantReasoningDelta:
		message := payload.(runevent.MessageDeltaPayload)
		if _, exists := projector.activeReasoning[message.MessageID]; !exists {
			return Projection{}, fmt.Errorf("reasoning message %q delta arrived before start", message.MessageID)
		}
		return Projection{Events: []events.Event{
			events.NewReasoningMessageContentEvent(message.MessageID, message.Delta),
		}}, nil
	case runevent.KindAssistantReasoningDone:
		message := payload.(runevent.MessageCompletedPayload)
		if _, exists := projector.activeReasoning[message.MessageID]; !exists {
			return Projection{}, fmt.Errorf("reasoning message %q completed before start", message.MessageID)
		}
		delete(projector.activeReasoning, message.MessageID)
		return Projection{Events: []events.Event{events.NewReasoningMessageEndEvent(message.MessageID)}}, nil
	case runevent.KindToolCallStarted:
		tool := payload.(runevent.ToolCallStartedPayload)
		if _, active := projector.activeToolCalls[tool.ToolCallID]; active {
			return Projection{}, fmt.Errorf("tool call %q started more than once", tool.ToolCallID)
		}
		if _, completed := projector.completedToolCall[tool.ToolCallID]; completed {
			return Projection{}, fmt.Errorf("completed tool call %q cannot restart", tool.ToolCallID)
		}
		projector.activeToolCalls[tool.ToolCallID] = struct{}{}
		options := make([]events.ToolCallStartOption, 0, 1)
		if tool.ParentMessageID != "" {
			options = append(options, events.WithParentMessageID(tool.ParentMessageID))
		}
		return Projection{Events: []events.Event{
			events.NewToolCallStartEvent(tool.ToolCallID, tool.ToolCallName, options...),
		}}, nil
	case runevent.KindToolCallArguments:
		tool := payload.(runevent.ToolCallArgumentsPayload)
		if _, exists := projector.activeToolCalls[tool.ToolCallID]; !exists {
			return Projection{}, fmt.Errorf("tool call %q arguments arrived before start", tool.ToolCallID)
		}
		return Projection{Events: []events.Event{
			events.NewToolCallArgsEvent(tool.ToolCallID, tool.Delta),
		}}, nil
	case runevent.KindToolCallCompleted:
		tool := payload.(runevent.ToolCallCompletedPayload)
		if _, exists := projector.activeToolCalls[tool.ToolCallID]; !exists {
			return Projection{}, fmt.Errorf("tool call %q completed before start", tool.ToolCallID)
		}
		delete(projector.activeToolCalls, tool.ToolCallID)
		projector.completedToolCall[tool.ToolCallID] = struct{}{}
		return Projection{Events: []events.Event{events.NewToolCallEndEvent(tool.ToolCallID)}}, nil
	case runevent.KindToolCallResult:
		tool := payload.(runevent.ToolCallResultPayload)
		if _, exists := projector.completedToolCall[tool.ToolCallID]; !exists {
			return Projection{}, fmt.Errorf("tool call %q result arrived before completion", tool.ToolCallID)
		}
		delete(projector.completedToolCall, tool.ToolCallID)
		projected := []events.Event{
			events.NewToolCallResultEvent(tool.MessageID, tool.ToolCallID, tool.Content),
		}
		if tool.Presentation != nil {
			operations, err := presentationOperations(event.EventID, *tool.Presentation)
			if err != nil {
				return Projection{}, err
			}
			projected = append(projected, events.NewCustomEvent("a2ui.operations", events.WithValue(operations)))
		}
		return Projection{Events: projected}, nil
	case runevent.KindRunCompleted:
		if err := projector.requireClosedLifecycles(); err != nil {
			return Projection{}, err
		}
		terminal := payload.(runevent.RunTerminalPayload)
		options := []events.RunFinishedOption{events.WithSuccessOutcome()}
		if len(terminal.Result) > 0 && string(terminal.Result) != "null" {
			var result any
			decoder := json.NewDecoder(bytes.NewReader(terminal.Result))
			decoder.UseNumber()
			if err := decoder.Decode(&result); err != nil {
				return Projection{}, fmt.Errorf("decode run result: %w", err)
			}
			options = append(options, events.WithResult(result))
		}
		return Projection{Events: []events.Event{
			events.NewRunFinishedEventWithOptions(event.SessionID, event.RunID, options...),
		}, Terminal: true}, nil
	case runevent.KindRunFailed, runevent.KindRunInterrupted, runevent.KindRunCancelled:
		if err := projector.requireClosedLifecycles(); err != nil {
			return Projection{}, err
		}
		terminal := payload.(runevent.RunTerminalPayload)
		code := terminal.Code
		if code == "" {
			code = event.Kind
		}
		return Projection{Events: []events.Event{
			events.NewRunErrorEvent(terminal.Message, events.WithErrorCode(code), events.WithRunID(event.RunID)),
		}, Terminal: true}, nil
	default:
		return Projection{}, fmt.Errorf("canonical event kind %q has no AG-UI projection", event.Kind)
	}
}

func presentationOperations(eventID string, presentation runevent.ToolPresentation) ([]a2ui.Message, error) {
	var operations []a2ui.Message
	switch presentation.Kind {
	case "command":
		command := presentation.Command
		operations = a2ui.CommandCard(eventID, a2ui.CommandView{
			Command: command.Command,
			Output:  command.Output,
			Status:  command.Status,
		})
	case "file_change":
		files := make([]a2ui.FileChange, 0, len(presentation.FileChange.Files))
		for _, file := range presentation.FileChange.Files {
			files = append(files, a2ui.FileChange{Path: file.Path, Kind: file.Kind, Diff: file.Diff})
		}
		operations = a2ui.FileChangeCard(eventID, files)
	default:
		return nil, fmt.Errorf("unsupported A2UI presentation kind %q", presentation.Kind)
	}
	if err := a2ui.ValidateOperations(operations); err != nil {
		return nil, fmt.Errorf("validate generated A2UI operations: %w", err)
	}
	return operations, nil
}

func (projector *Projector) requireClosedLifecycles() error {
	if len(projector.activeMessages) != 0 || len(projector.activeReasoning) != 0 || len(projector.activeToolCalls) != 0 || len(projector.completedToolCall) != 0 {
		return errors.New("run reached a terminal event with unfinished AG-UI lifecycles")
	}
	return nil
}
