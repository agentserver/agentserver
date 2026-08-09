package coreserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/agentserver/agentserver/v2/internal/corecontract"
	"github.com/agentserver/agentserver/v2/internal/coredb"
	"github.com/agentserver/agentserver/v2/internal/runcursor"
	"github.com/agentserver/agentserver/v2/internal/runevent"
)

const maxUserRunPromptBytes = 256 * 1024

type UserRunStateStore interface {
	AuthorizeRunSession(context.Context, string, string, string) (coredb.AuthorizedSession, error)
	CreateAuthorizedRun(context.Context, coredb.CreateRunCommand) (coredb.CreateRunResult, error)
	CancelRun(context.Context, coredb.CancelRunCommand) (coredb.CancelRunResult, error)
	ReadAuthorizedRunEvents(context.Context, coredb.ReadAuthorizedRunEventsCommand) (coredb.ReadAuthorizedRunEventsResult, error)
}

type UserPromptWriteRequest struct {
	WorkspaceID    string
	SessionID      string
	ActorID        string
	IdempotencyKey string
	RequestHash    [32]byte
	Prompt         string
}

type UserPromptReadRequest struct {
	WorkspaceID string
	Pointer     coredb.ObjectPointer
}

// UserPromptStore must return the same complete object pointer for exact
// retries of one workspace/actor/session/idempotency key. A different prompt
// under that key must fail rather than allocate a second pointer.
type UserPromptStore interface {
	PutUserPrompt(context.Context, UserPromptWriteRequest) (coredb.ObjectPointer, error)
}

// UserPromptReader materializes only a Core-authorized immutable prompt
// pointer. Implementations must verify its complete plaintext descriptor
// before returning any user content.
type UserPromptReader interface {
	ReadUserPrompt(context.Context, UserPromptReadRequest) (string, error)
}

type UserRunPolicyResolver interface {
	ResolveUserRunPolicy(context.Context, coredb.AuthorizedSession) (coredb.RunExecutorPolicy, error)
}

type UserRunLLMGatewayResolver interface {
	ResolveUserRunLLMGatewayBinding(context.Context, coredb.ResolveUserRunLLMGatewayBindingCommand) (coredb.RunLLMGatewayBinding, error)
}

type UserRunLarkEgressResolver interface {
	ResolveUserRunLarkEgressBinding(context.Context, coredb.ResolveUserRunLarkEgressBindingCommand) (coredb.RunLarkEgressBinding, error)
}

type UserRunIDGenerator func() (string, error)

type UserRunServiceConfig struct {
	Store        UserRunStateStore
	Prompts      UserPromptStore
	Policies     UserRunPolicyResolver
	LLMGateways  UserRunLLMGatewayResolver
	LarkEgress   UserRunLarkEgressResolver
	CursorCodec  *runcursor.Codec
	NewID        UserRunIDGenerator
	PollInterval time.Duration
}

type UserRunService struct {
	store        UserRunStateStore
	prompts      UserPromptStore
	policies     UserRunPolicyResolver
	llmGateways  UserRunLLMGatewayResolver
	larkEgress   UserRunLarkEgressResolver
	cursors      *runcursor.Codec
	newID        UserRunIDGenerator
	pollInterval time.Duration
}

type CreateUserRunCommand struct {
	ActorID        string
	WorkspaceID    string
	SessionID      string
	IdempotencyKey string
	ClientRunID    string
	Prompt         string
}

type ReadUserRunEventsQuery struct {
	ActorID     string
	WorkspaceID string
	RunID       string
	After       string
	Limit       int
	Wait        time.Duration
}

type CancelUserRunCommand struct {
	ActorID     string
	WorkspaceID string
	RunID       string
}

type UserRunCursorExpiredError struct {
	Response corecontract.UserRunCursorExpiredResponse
}

func (err *UserRunCursorExpiredError) Error() string { return "authorized run event cursor expired" }

func NewUserRunService(config UserRunServiceConfig) (*UserRunService, error) {
	if config.Store == nil || config.Prompts == nil || config.Policies == nil || config.CursorCodec == nil {
		return nil, errors.New("user run store, prompt store, policy resolver, and cursor codec are required")
	}
	if config.NewID == nil {
		config.NewID = newCoreUUID
	}
	if config.PollInterval == 0 {
		config.PollInterval = 100 * time.Millisecond
	}
	if config.PollInterval < 10*time.Millisecond || config.PollInterval > time.Second {
		return nil, errors.New("user run poll interval must be between 10ms and one second")
	}
	return &UserRunService{
		store: config.Store, prompts: config.Prompts, policies: config.Policies,
		llmGateways: config.LLMGateways, larkEgress: config.LarkEgress,
		cursors: config.CursorCodec, newID: config.NewID, pollInterval: config.PollInterval,
	}, nil
}

func (service *UserRunService) CreateUserRun(ctx context.Context, command CreateUserRunCommand) (corecontract.CreateUserRunResponse, error) {
	if err := validateCreateUserRunCommand(command); err != nil {
		return corecontract.CreateUserRunResponse{}, publicRunStateError(coredb.ErrorInvalidArgument, "CreateUserRun", "run", "", err.Error())
	}
	session, err := service.store.AuthorizeRunSession(ctx, command.WorkspaceID, command.SessionID, command.ActorID)
	if err != nil {
		return corecontract.CreateUserRunResponse{}, err
	}
	requestHash, err := createUserRunRequestHash(command.Prompt)
	if err != nil {
		return corecontract.CreateUserRunResponse{}, publicRunStateError(coredb.ErrorInvalidArgument, "CreateUserRun", "run", "", err.Error())
	}
	prompt, err := service.prompts.PutUserPrompt(ctx, UserPromptWriteRequest{
		WorkspaceID: command.WorkspaceID, SessionID: command.SessionID, ActorID: command.ActorID,
		IdempotencyKey: command.IdempotencyKey, RequestHash: requestHash, Prompt: command.Prompt,
	})
	if err != nil {
		return corecontract.CreateUserRunResponse{}, fmt.Errorf("persist immutable user prompt: %w", err)
	}
	policy, err := service.policies.ResolveUserRunPolicy(ctx, session)
	if err != nil {
		return corecontract.CreateUserRunResponse{}, fmt.Errorf("resolve user run executor policy: %w", err)
	}
	var llmGateway coredb.RunLLMGatewayBinding
	if service.llmGateways != nil {
		llmGateway, err = service.llmGateways.ResolveUserRunLLMGatewayBinding(ctx, coredb.ResolveUserRunLLMGatewayBindingCommand{
			WorkspaceID: command.WorkspaceID, SessionID: command.SessionID,
			ActorID: command.ActorID, IdempotencyKey: command.IdempotencyKey,
		})
		if err != nil {
			return corecontract.CreateUserRunResponse{}, err
		}
	}
	var larkEgress coredb.RunLarkEgressBinding
	if service.larkEgress != nil {
		larkEgress, err = service.larkEgress.ResolveUserRunLarkEgressBinding(ctx, coredb.ResolveUserRunLarkEgressBindingCommand{
			WorkspaceID: command.WorkspaceID, SessionID: command.SessionID,
			ActorID: command.ActorID, IdempotencyKey: command.IdempotencyKey,
		})
		if err != nil {
			return corecontract.CreateUserRunResponse{}, err
		}
	}
	identities := make([]string, 4)
	seen := make(map[string]struct{}, len(identities))
	for index := range identities {
		identity, err := service.newID()
		if err != nil {
			return corecontract.CreateUserRunResponse{}, fmt.Errorf("allocate run transition identity: %w", err)
		}
		if _, duplicate := seen[identity]; duplicate {
			return corecontract.CreateUserRunResponse{}, errors.New("run identity generator returned a duplicate identity")
		}
		seen[identity] = struct{}{}
		identities[index] = identity
	}
	created, err := service.store.CreateAuthorizedRun(ctx, coredb.CreateRunCommand{
		RunID: identities[0], WorkspaceID: command.WorkspaceID, SessionID: command.SessionID,
		ActorID: command.ActorID, RequestHash: requestHash, IdempotencyKey: command.IdempotencyKey,
		Prompt: prompt, ExecutorPolicy: policy, LLMGateway: llmGateway, LarkEgress: larkEgress,
		Record: coredb.TransitionRecord{
			EventID: identities[1], ProducerInstanceID: identities[2], ProducerSeq: 1, OutboxID: identities[3],
		},
	})
	if err != nil {
		return corecontract.CreateUserRunResponse{}, err
	}
	if created.Run.WorkspaceID != command.WorkspaceID || created.Run.SessionID != command.SessionID || created.Run.ActorID != command.ActorID || created.Run.NextEventSeq < 2 {
		return corecontract.CreateUserRunResponse{}, errors.New("core state store returned an invalid authorized run scope")
	}
	cursor, err := service.cursors.Encode(runcursor.Scope{
		WorkspaceID: created.Run.WorkspaceID, SessionID: created.Run.SessionID, RunID: created.Run.ID,
	}, 1)
	if err != nil {
		return corecontract.CreateUserRunResponse{}, fmt.Errorf("encode initial run cursor: %w", err)
	}
	return corecontract.CreateUserRunResponse{
		WorkspaceID: created.Run.WorkspaceID, SessionID: created.Run.SessionID, RunID: created.Run.ID,
		CreatedAt: created.Run.CreatedAt, Cursor: cursor, LastEventSequence: 1, Created: created.Created,
	}, nil
}

func (service *UserRunService) CancelUserRun(ctx context.Context, command CancelUserRunCommand) (corecontract.CancelUserRunResponse, error) {
	if err := validateCancelUserRunCommand(command); err != nil {
		return corecontract.CancelUserRunResponse{}, publicRunStateError(coredb.ErrorInvalidArgument, "CancelUserRun", "run", command.RunID, err.Error())
	}
	identities := make([]string, 3)
	seen := make(map[string]struct{}, len(identities))
	for index := range identities {
		identity, err := service.newID()
		if err != nil {
			return corecontract.CancelUserRunResponse{}, fmt.Errorf("allocate cancel transition identity: %w", err)
		}
		if _, duplicate := seen[identity]; duplicate {
			return corecontract.CancelUserRunResponse{}, errors.New("cancel identity generator returned a duplicate identity")
		}
		seen[identity] = struct{}{}
		identities[index] = identity
	}
	result, err := service.store.CancelRun(ctx, coredb.CancelRunCommand{
		WorkspaceID: command.WorkspaceID, RunID: command.RunID, ActorID: command.ActorID,
		Record: coredb.TransitionRecord{
			EventID: identities[0], ProducerInstanceID: identities[1], ProducerSeq: 1, OutboxID: identities[2],
		},
	})
	if err != nil {
		return corecontract.CancelUserRunResponse{}, err
	}
	if result.Run.WorkspaceID != command.WorkspaceID || result.Run.ID != command.RunID ||
		result.Run.SessionID == "" || !cancelRunResponseStatus(result.Run.Status) ||
		result.Run.Version < 1 || result.Run.Version >= 1<<53-1 ||
		result.SessionVersion < 1 || result.SessionVersion >= 1<<53-1 {
		return corecontract.CancelUserRunResponse{}, errors.New("core state store returned an invalid cancelled run scope")
	}
	return corecontract.CancelUserRunResponse{
		WorkspaceID: result.Run.WorkspaceID, SessionID: result.Run.SessionID, RunID: result.Run.ID,
		Status: result.Run.Status, RunVersion: result.Run.Version,
		Terminal: terminalRunStatus(result.Run.Status), Changed: result.Changed,
	}, nil
}

func (service *UserRunService) ReadUserRunEvents(ctx context.Context, query ReadUserRunEventsQuery) (corecontract.ReadUserRunEventsResponse, error) {
	if err := validateReadUserRunEventsQuery(query); err != nil {
		return corecontract.ReadUserRunEventsResponse{}, publicRunStateError(coredb.ErrorInvalidArgument, "ReadUserRunEvents", "run", query.RunID, err.Error())
	}
	position, err := service.cursors.DecodePosition(query.After)
	if err != nil || position.Scope.WorkspaceID != query.WorkspaceID || position.Scope.RunID != query.RunID {
		return corecontract.ReadUserRunEventsResponse{}, publicRunStateError(coredb.ErrorInvalidArgument, "ReadUserRunEvents", "run", query.RunID, "invalid run event cursor")
	}
	deadline := time.Now().Add(query.Wait)
	for {
		storeLimit := query.Limit
		if storeLimit == 0 {
			storeLimit = 1
		}
		page, err := service.store.ReadAuthorizedRunEvents(ctx, coredb.ReadAuthorizedRunEventsCommand{
			WorkspaceID: query.WorkspaceID, ActorID: query.ActorID, RunID: query.RunID,
			AfterSeq: position.AfterSequence, Limit: storeLimit,
		})
		if err != nil {
			return corecontract.ReadUserRunEventsResponse{}, err
		}
		if page.Run.WorkspaceID != position.Scope.WorkspaceID || page.Run.SessionID != position.Scope.SessionID || page.Run.ID != position.Scope.RunID {
			return corecontract.ReadUserRunEventsResponse{}, errors.New("core state store returned an event page outside the cursor scope")
		}
		if position.AfterSequence < page.EarliestSequence-1 {
			return corecontract.ReadUserRunEventsResponse{}, service.cursorExpired(page)
		}
		if query.Limit == 0 {
			next, err := service.cursors.Encode(position.Scope, position.AfterSequence)
			if err != nil {
				return corecontract.ReadUserRunEventsResponse{}, fmt.Errorf("encode resolved run cursor: %w", err)
			}
			return corecontract.ReadUserRunEventsResponse{
				Events: []runevent.Event{}, EventCursors: []string{}, NextCursor: next,
				LastEventSequence: position.AfterSequence,
			}, nil
		}
		events, nextSequence, err := contractRunEvents(page, position.AfterSequence)
		if err != nil {
			return corecontract.ReadUserRunEventsResponse{}, err
		}
		if len(events) != 0 || query.Wait == 0 || terminalRunStatus(page.Run.Status) || !time.Now().Before(deadline) {
			next, err := service.cursors.Encode(position.Scope, nextSequence)
			if err != nil {
				return corecontract.ReadUserRunEventsResponse{}, fmt.Errorf("encode next run cursor: %w", err)
			}
			eventCursors := make([]string, len(events))
			for index := range events {
				eventCursors[index], err = service.cursors.Encode(position.Scope, events[index].Seq)
				if err != nil {
					return corecontract.ReadUserRunEventsResponse{}, fmt.Errorf("encode run event cursor %d: %w", index, err)
				}
			}
			return corecontract.ReadUserRunEventsResponse{
				Events: events, EventCursors: eventCursors, NextCursor: next, LastEventSequence: nextSequence,
			}, nil
		}
		remaining := time.Until(deadline)
		pause := service.pollInterval
		if remaining < pause {
			pause = remaining
		}
		timer := time.NewTimer(pause)
		select {
		case <-ctx.Done():
			timer.Stop()
			return corecontract.ReadUserRunEventsResponse{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func (service *UserRunService) cursorExpired(page coredb.ReadAuthorizedRunEventsResult) error {
	if page.Rebase == nil {
		return errors.New("run event retention boundary has no committed lifecycle snapshot")
	}
	if page.Rebase.AfterSequence != page.EarliestSequence-1 || len(page.Rebase.Snapshot) == 0 ||
		!knownRunStatus(page.Rebase.RunStatus) || page.Rebase.RunVersion < 1 || page.Rebase.RunVersion >= 1<<53-1 ||
		page.Rebase.RunUpdatedAt.IsZero() {
		return errors.New("run event retention rebase is inconsistent")
	}
	rebaseCursor, err := service.cursors.Encode(runcursor.Scope{
		WorkspaceID: page.Run.WorkspaceID, SessionID: page.Run.SessionID, RunID: page.Run.ID,
	}, page.Rebase.AfterSequence)
	if err != nil {
		return fmt.Errorf("encode run event rebase cursor: %w", err)
	}
	snapshot := append(json.RawMessage(nil), page.Rebase.Snapshot...)
	return &UserRunCursorExpiredError{Response: corecontract.UserRunCursorExpiredResponse{
		Code: "cursor_expired", Message: "run event cursor is outside the retained history",
		Snapshot: corecontract.UserRunSnapshot{
			WorkspaceID: page.Run.WorkspaceID, SessionID: page.Run.SessionID, RunID: page.Run.ID,
			Status: page.Rebase.RunStatus, RunVersion: page.Rebase.RunVersion, LastEventSequence: page.Rebase.AfterSequence,
			State: snapshot, UpdatedAt: page.Rebase.RunUpdatedAt,
		},
		RebaseCursor: rebaseCursor, LastEventSequence: page.Rebase.AfterSequence,
	}}
}

func contractRunEvents(page coredb.ReadAuthorizedRunEventsResult, after int64) ([]runevent.Event, int64, error) {
	result := make([]runevent.Event, len(page.Events))
	next := after
	for index, source := range page.Events {
		if source.Seq != next+1 {
			return nil, 0, fmt.Errorf("committed run event sequence gap: got %d, want %d", source.Seq, next+1)
		}
		event := runevent.Event{
			EventID: source.EventID, SchemaVersion: source.SchemaVersion, Seq: source.Seq,
			WorkspaceID: page.Run.WorkspaceID, SessionID: page.Run.SessionID, RunID: page.Run.ID,
			RunAttemptID: source.RunAttemptID, RunAttemptGeneration: source.RunAttemptGeneration,
			ProducerInstanceID: source.ProducerInstanceID, ProducerSeq: source.ProducerSeq,
			Source: source.Source, Kind: source.Kind, CreatedAt: source.CreatedAt,
			Payload: append(json.RawMessage(nil), source.Payload...),
		}
		if source.Object != nil {
			event.Payload = nil
			event.Object = &runevent.ObjectPointer{
				ObjectID: source.Object.ObjectID, SHA256: hex.EncodeToString(source.Object.SHA256[:]),
				Size: source.Object.Size, MediaType: source.Object.MediaType,
			}
		}
		if err := event.Validate(); err != nil {
			return nil, 0, fmt.Errorf("validate committed run event %d: %w", source.Seq, err)
		}
		result[index] = event
		next = source.Seq
	}
	return result, next, nil
}

func validateCreateUserRunCommand(command CreateUserRunCommand) error {
	for field, value := range map[string]string{"actorId": command.ActorID, "workspaceId": command.WorkspaceID, "sessionId": command.SessionID} {
		if !canonicalPublicUUID(value) {
			return fmt.Errorf("%s must be a non-zero canonical lowercase UUID", field)
		}
	}
	if err := validatePublicIdempotencyKey(command.IdempotencyKey); err != nil {
		return err
	}
	if !utf8.ValidString(command.Prompt) || command.Prompt == "" || len(command.Prompt) > maxUserRunPromptBytes || strings.ContainsRune(command.Prompt, '\x00') {
		return fmt.Errorf("prompt must contain between 1 and %d bytes of UTF-8 text without NUL", maxUserRunPromptBytes)
	}
	if len(command.ClientRunID) > 256 || strings.ContainsAny(command.ClientRunID, "\x00\r\n") {
		return errors.New("clientRunId must be bounded text without NUL or line breaks")
	}
	return nil
}

func validateCancelUserRunCommand(command CancelUserRunCommand) error {
	for field, value := range map[string]string{
		"actorId": command.ActorID, "workspaceId": command.WorkspaceID, "runId": command.RunID,
	} {
		if !canonicalPublicUUID(value) {
			return fmt.Errorf("%s must be a non-zero canonical lowercase UUID", field)
		}
	}
	return nil
}

func validateReadUserRunEventsQuery(query ReadUserRunEventsQuery) error {
	for field, value := range map[string]string{"actorId": query.ActorID, "workspaceId": query.WorkspaceID, "runId": query.RunID} {
		if !canonicalPublicUUID(value) {
			return fmt.Errorf("%s must be a non-zero canonical lowercase UUID", field)
		}
	}
	if query.After == "" || len(query.After) > 4096 || strings.ContainsAny(query.After, "\x00\r\n") {
		return errors.New("after cursor must be bounded opaque text")
	}
	if query.Limit < 0 || query.Limit > 1024 {
		return errors.New("event page limit must be between 0 and 1024")
	}
	if query.Wait < 0 || query.Wait > 30*time.Second {
		return errors.New("event long-poll wait must be between zero and 30 seconds")
	}
	return nil
}

func createUserRunRequestHash(prompt string) ([32]byte, error) {
	raw, err := json.Marshal(struct {
		Prompt string `json:"prompt"`
	}{Prompt: prompt})
	if err != nil {
		return [32]byte{}, err
	}
	_, hash, err := coredb.ValidateAndHashCanonicalJSON(coredb.HashDomainCreateRunRequest, raw, func(value any) error {
		object, ok := value.(map[string]any)
		if !ok || len(object) != 1 {
			return errors.New("create run request must contain exactly prompt")
		}
		text, ok := object["prompt"].(string)
		if !ok || text != prompt {
			return errors.New("create run prompt is invalid")
		}
		return nil
	})
	if err != nil {
		return [32]byte{}, err
	}
	return hash.SHA256(), nil
}

func validatePublicIdempotencyKey(value string) error {
	if len(value) < 1 || len(value) > 256 {
		return errors.New("idempotency key must contain between 1 and 256 bytes")
	}
	for _, character := range []byte(value) {
		if character < 0x21 || character > 0x7e {
			return errors.New("idempotency key must contain visible ASCII without spaces")
		}
	}
	return nil
}

func canonicalPublicUUID(value string) bool {
	if len(value) != 36 || value == "00000000-0000-0000-0000-000000000000" || strings.ToLower(value) != value {
		return false
	}
	for index, character := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if character != '-' {
				return false
			}
			continue
		}
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}

func terminalRunStatus(status string) bool {
	switch status {
	case coredb.RunStatusCompleted, coredb.RunStatusFailed, coredb.RunStatusInterrupted, coredb.RunStatusCancelled:
		return true
	default:
		return false
	}
}

func knownRunStatus(status string) bool {
	switch status {
	case coredb.RunStatusQueued, coredb.RunStatusStarting, coredb.RunStatusRunning, coredb.RunStatusFinalizing,
		coredb.RunStatusCompleted, coredb.RunStatusFailed, coredb.RunStatusInterrupted, coredb.RunStatusCancelling,
		coredb.RunStatusCancelled:
		return true
	default:
		return false
	}
}

func cancelRunResponseStatus(status string) bool {
	return status == coredb.RunStatusCancelling || terminalRunStatus(status)
}

func publicRunStateError(code coredb.StateErrorCode, operation, resource, resourceID, message string) error {
	return &coredb.StateError{Code: code, Operation: operation, Resource: resource, ResourceID: resourceID, Message: message}
}

func newCoreUUID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	var encoded [36]byte
	hex.Encode(encoded[0:8], raw[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], raw[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], raw[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], raw[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], raw[10:16])
	return string(encoded[:]), nil
}
