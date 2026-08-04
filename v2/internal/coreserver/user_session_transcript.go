package coreserver

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
	"github.com/agentserver/agentserver/v2/internal/runevent"
)

const (
	maximumUserSessionTranscriptMessages     = 512
	maximumUserSessionTranscriptContentBytes = 256 * 1024
)

func (commands StateStoreUserSessionCommands) GetTranscript(
	ctx context.Context,
	workspaceID, sessionID, actorID string,
) (corecontract.GetUserSessionTranscriptResponse, error) {
	if commands.Store == nil || commands.Prompts == nil {
		return corecontract.GetUserSessionTranscriptResponse{}, errors.New("user transcript state and prompt readers are required")
	}
	source, err := commands.Store.ReadUserSessionTranscript(ctx, workspaceID, sessionID, actorID)
	if err != nil {
		return corecontract.GetUserSessionTranscriptResponse{}, err
	}
	result := corecontract.GetUserSessionTranscriptResponse{
		WorkspaceID: source.Session.WorkspaceID,
		SessionID:   source.Session.ID,
		Messages:    []corecontract.UserSessionTranscriptMessage{},
		Truncated:   source.Truncated,
	}
	eventsByRun := make(map[string][]coredb.UserSessionTranscriptEvent, len(source.Runs))
	knownRuns := make(map[string]struct{}, len(source.Runs))
	for _, run := range source.Runs {
		knownRuns[run.ID] = struct{}{}
	}
	for _, event := range source.Events {
		if _, ok := knownRuns[event.RunID]; !ok {
			return corecontract.GetUserSessionTranscriptResponse{}, errors.New("transcript event escaped the selected run set")
		}
		eventsByRun[event.RunID] = append(eventsByRun[event.RunID], event)
	}

	remainingBytes := maximumUserSessionTranscriptContentBytes
	remainingMessages := maximumUserSessionTranscriptMessages
	newestFirst := make([][]corecontract.UserSessionTranscriptMessage, 0, len(source.Runs))
	for index := len(source.Runs) - 1; index >= 0; index-- {
		if remainingBytes == 0 || remainingMessages == 0 {
			result.Truncated = true
			break
		}
		run := source.Runs[index]
		prompt, err := commands.Prompts.ReadUserPrompt(ctx, UserPromptReadRequest{
			WorkspaceID: workspaceID,
			Pointer:     run.Prompt,
		})
		if err != nil {
			return corecontract.GetUserSessionTranscriptResponse{}, fmt.Errorf("read transcript prompt for run %s: %w", run.ID, err)
		}
		group, usedBytes, usedMessages, truncated, err := projectUserSessionTranscriptRun(
			workspaceID, sessionID, run, prompt, eventsByRun[run.ID], remainingBytes, remainingMessages,
		)
		if err != nil {
			return corecontract.GetUserSessionTranscriptResponse{}, err
		}
		remainingBytes -= usedBytes
		remainingMessages -= usedMessages
		result.Truncated = result.Truncated || truncated
		if len(group) != 0 {
			newestFirst = append(newestFirst, group)
		}
		if truncated {
			break
		}
	}
	for index := len(newestFirst) - 1; index >= 0; index-- {
		result.Messages = append(result.Messages, newestFirst[index]...)
	}
	return result, nil
}

type transcriptAssistantMessage struct {
	message   corecontract.UserSessionTranscriptMessage
	started   bool
	completed bool
	truncated bool
}

func projectUserSessionTranscriptRun(
	workspaceID, sessionID string,
	run coredb.UserSessionTranscriptRun,
	prompt string,
	events []coredb.UserSessionTranscriptEvent,
	maximumBytes, maximumMessages int,
) ([]corecontract.UserSessionTranscriptMessage, int, int, bool, error) {
	if maximumBytes < 1 || maximumMessages < 1 {
		return nil, 0, 0, true, nil
	}
	userContent, userComplete := transcriptPrefix(prompt, maximumBytes)
	messages := []corecontract.UserSessionTranscriptMessage{{
		MessageID: "user-" + run.ID,
		RunID:     run.ID,
		Role:      "user",
		Content:   userContent,
		Complete:  userComplete,
		CreatedAt: run.CreatedAt,
	}}
	usedBytes := len(userContent)
	truncated := !userComplete
	if truncated || maximumMessages == 1 {
		return messages, usedBytes, 1, truncated || len(events) != 0, nil
	}

	remainingBytes := maximumBytes - usedBytes
	order := make([]string, 0)
	builders := make(map[string]*transcriptAssistantMessage)
	for _, source := range events {
		event, err := contractUserSessionTranscriptEvent(workspaceID, sessionID, source)
		if err != nil {
			return nil, 0, 0, false, err
		}
		payload, err := runevent.DecodeSemanticPayload(event)
		if err != nil {
			return nil, 0, 0, false, fmt.Errorf("decode transcript event %s: %w", event.EventID, err)
		}
		switch event.Kind {
		case runevent.KindAssistantMessageStarted:
			value := payload.(runevent.MessageStartedPayload)
			builder := builders[value.MessageID]
			if builder != nil && builder.started {
				return nil, 0, 0, false, fmt.Errorf("assistant transcript message %q started more than once", value.MessageID)
			}
			if builder == nil {
				builder = newTranscriptAssistantMessage(run.ID, value.MessageID, event)
				builders[value.MessageID] = builder
				order = append(order, value.MessageID)
			}
			builder.started = true
		case runevent.KindAssistantMessageDelta:
			value := payload.(runevent.MessageDeltaPayload)
			builder := builders[value.MessageID]
			if builder == nil {
				builder = newTranscriptAssistantMessage(run.ID, value.MessageID, event)
				builders[value.MessageID] = builder
				order = append(order, value.MessageID)
			}
			prefix, complete := transcriptPrefix(value.Delta, remainingBytes)
			builder.message.Content += prefix
			usedBytes += len(prefix)
			remainingBytes -= len(prefix)
			if !complete {
				builder.truncated = true
				truncated = true
			}
		case runevent.KindAssistantMessageCompleted:
			value := payload.(runevent.MessageCompletedPayload)
			builder := builders[value.MessageID]
			if builder == nil {
				builder = newTranscriptAssistantMessage(run.ID, value.MessageID, event)
				builders[value.MessageID] = builder
				order = append(order, value.MessageID)
			}
			builder.completed = true
		default:
			return nil, 0, 0, false, fmt.Errorf("unsupported transcript event kind %q", event.Kind)
		}
	}

	for _, messageID := range order {
		builder := builders[messageID]
		if len(messages) == maximumMessages {
			truncated = true
			break
		}
		builder.message.Complete = builder.completed && !builder.truncated
		messages = append(messages, builder.message)
	}
	return messages, usedBytes, len(messages), truncated, nil
}

func newTranscriptAssistantMessage(runID, messageID string, event runevent.Event) *transcriptAssistantMessage {
	return &transcriptAssistantMessage{message: corecontract.UserSessionTranscriptMessage{
		MessageID: messageID,
		RunID:     runID,
		Role:      "assistant",
		Content:   "",
		Complete:  false,
		CreatedAt: event.CreatedAt,
	}}
}

func contractUserSessionTranscriptEvent(
	workspaceID, sessionID string,
	source coredb.UserSessionTranscriptEvent,
) (runevent.Event, error) {
	event := runevent.Event{
		EventID: source.Event.EventID, SchemaVersion: source.Event.SchemaVersion, Seq: source.Event.Seq,
		WorkspaceID: workspaceID, SessionID: sessionID, RunID: source.RunID,
		RunAttemptID: source.Event.RunAttemptID, RunAttemptGeneration: source.Event.RunAttemptGeneration,
		ProducerInstanceID: source.Event.ProducerInstanceID, ProducerSeq: source.Event.ProducerSeq,
		Source: source.Event.Source, Kind: source.Event.Kind, CreatedAt: source.Event.CreatedAt,
		Payload: append([]byte(nil), source.Event.Payload...),
	}
	if source.Event.Object != nil {
		event.Payload = nil
		event.Object = &runevent.ObjectPointer{
			ObjectID: source.Event.Object.ObjectID,
			SHA256:   hex.EncodeToString(source.Event.Object.SHA256[:]),
			Size:     source.Event.Object.Size, MediaType: source.Event.Object.MediaType,
		}
	}
	if err := event.Validate(); err != nil {
		return runevent.Event{}, fmt.Errorf("validate transcript event %s: %w", event.EventID, err)
	}
	return event, nil
}

func transcriptPrefix(value string, maximumBytes int) (string, bool) {
	if len(value) <= maximumBytes {
		return value, true
	}
	if maximumBytes <= 0 {
		return "", false
	}
	end := maximumBytes
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end], false
}
