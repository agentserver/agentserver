package harnesspool

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/agentserver/agentserver/v2/internal/runevent"
)

type pendingRuntimeAppend struct {
	controlSequence uint64
	eventDigest     [sha256.Size]byte
	request         AppendAttemptEventsRequest
}

type retryableRuntimeEventError struct{ cause error }

func (err *retryableRuntimeEventError) Error() string {
	return "append canonical runtime event can be retried on the same holder: " + err.cause.Error()
}

func (err *retryableRuntimeEventError) Unwrap() error { return err.cause }

func isRetryableRuntimeEventError(err error) bool {
	var retryable *retryableRuntimeEventError
	return errors.As(err, &retryable)
}

// RuntimeEvent maps one exact worker control fact and synchronously crosses
// core's AppendAttemptEvents boundary. The pending request, including producer
// identities, remains in memory until core confirms it; a same-holder control
// resume therefore retries the exact command instead of allocating new keys.
func (authority *attemptLifecycleAuthority) RuntimeEvent(ctx context.Context, input AttemptRuntimeEvent) error {
	if ctx == nil {
		return errors.New("runtime event context is required")
	}
	if input.ControlSequence == 0 {
		return errors.New("runtime event control sequence must be positive")
	}
	raw, err := json.Marshal(input.Event)
	if err != nil {
		return fmt.Errorf("encode pending runtime event identity: %w", err)
	}
	digest := sha256.Sum256(raw)

	authority.runtimeMu.Lock()
	defer authority.runtimeMu.Unlock()
	if err := context.Cause(authority.ctx); err != nil {
		return err
	}

	authority.mu.Lock()
	turnAccepted := authority.turnWasAccepted
	threadID, turnID := authority.threadID, authority.turnID
	frozen := authority.prepared.FrozenCatalog
	claim := authority.prepared.Scheduled.Claim
	authority.mu.Unlock()
	if !turnAccepted {
		return errors.New("runtime event arrived before authoritative turn acceptance")
	}

	pending := authority.pendingRuntime
	if pending != nil {
		if pending.controlSequence != input.ControlSequence || pending.eventDigest != digest {
			return errors.New("a different runtime event arrived while a core append is pending")
		}
		if err := authority.appendPendingRuntime(ctx, pending.request); err != nil {
			return err
		}
		authority.runtimeCursor = input.ControlSequence
		authority.pendingRuntime = nil
		return nil
	}
	if input.ControlSequence <= authority.runtimeCursor {
		return fmt.Errorf("runtime event control sequence %d did not advance after %d", input.ControlSequence, authority.runtimeCursor)
	}
	if authority.runtimeMapper == nil {
		mapper, err := newRuntimeEventMapper(threadID, turnID, frozen)
		if err != nil {
			return err
		}
		authority.runtimeMapper = mapper
	}
	mapped, err := authority.runtimeMapper.Map(input.Event)
	if err != nil {
		return fmt.Errorf("map worker runtime event: %w", err)
	}
	if len(mapped) == 0 {
		authority.runtimeCursor = input.ControlSequence
		return nil
	}

	events := make([]AttemptEvent, len(mapped))
	var outboxID string
	for index, candidate := range mapped {
		record, err := authority.identities.AllocateTransitionRecord()
		if err != nil {
			return fmt.Errorf("allocate runtime event identity %d: %w", index, err)
		}
		if index == 0 {
			outboxID = record.OutboxID
		}
		events[index] = AttemptEvent{
			EventID: record.EventID, ProducerInstanceID: record.ProducerInstanceID,
			ProducerSeq: record.ProducerSeq, Source: candidate.Source, Kind: candidate.Kind,
			SchemaVersion: runevent.CurrentSchemaVersion,
			Payload:       append(json.RawMessage(nil), candidate.Payload...),
		}
	}
	request := AppendAttemptEventsRequest{
		RunID: claim.Run.RunID, RunAttemptID: claim.RunAttempt.RunAttemptID,
		HolderID: claim.RunAttempt.HolderID, RunAttemptGeneration: claim.RunAttempt.Generation,
		OutboxID: outboxID, Events: events,
	}
	authority.pendingRuntime = &pendingRuntimeAppend{
		controlSequence: input.ControlSequence, eventDigest: digest, request: cloneAppendAttemptEventsRequest(request),
	}
	if err := authority.appendPendingRuntime(ctx, request); err != nil {
		return err
	}
	authority.runtimeCursor = input.ControlSequence
	authority.pendingRuntime = nil
	return nil
}

func (authority *attemptLifecycleAuthority) appendPendingRuntime(ctx context.Context, request AppendAttemptEventsRequest) error {
	eventCore, ok := authority.core.(AttemptEventCore)
	if !ok {
		return errors.New("attempt supervision core does not implement canonical event append")
	}
	callCtx, cancel := context.WithCancelCause(authority.ctx)
	stop := context.AfterFunc(ctx, func() { cancel(context.Cause(ctx)) })
	defer func() {
		stop()
		cancel(nil)
	}()
	_, err := eventCore.AppendAttemptEvents(callCtx, request)
	if err != nil && ambiguousPoolCommand(err, callCtx) {
		_, err = eventCore.AppendAttemptEvents(callCtx, request)
	}
	if err == nil {
		return nil
	}
	if callCtx.Err() != nil {
		return &retryableRuntimeEventError{cause: errors.Join(err, context.Cause(callCtx))}
	}
	var command *CoreCommandError
	if !errors.As(err, &command) || command.HTTPStatus >= http.StatusInternalServerError {
		return &retryableRuntimeEventError{cause: err}
	}
	return fmt.Errorf("append canonical runtime event: %w", err)
}

func cloneAppendAttemptEventsRequest(request AppendAttemptEventsRequest) AppendAttemptEventsRequest {
	clone := request
	clone.Events = make([]AttemptEvent, len(request.Events))
	for index, event := range request.Events {
		clone.Events[index] = event
		clone.Events[index].Payload = append(json.RawMessage(nil), event.Payload...)
		if event.Object != nil {
			object := *event.Object
			clone.Events[index].Object = &object
		}
	}
	return clone
}
