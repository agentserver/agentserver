package coredb

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (s *StateStore) AppendAttemptEvents(ctx context.Context, command AppendAttemptEventsCommand) (AppendAttemptEventsResult, error) {
	const operation = "AppendAttemptEvents"
	if err := validateAppendAttemptEvents(command); err != nil {
		return AppendAttemptEventsResult{}, commandError(ErrorInvalidArgument, operation, "run", command.RunID, err.Error())
	}

	return withStateTransaction(ctx, s, operation, func(transaction pgx.Tx) (AppendAttemptEventsResult, error) {
		runQuery := fmt.Sprintf("SELECT %s FROM %s AS r WHERE r.id = $1 FOR UPDATE", runColumns("r"), s.table("runs"))
		run, err := scanRun(transaction.QueryRow(ctx, runQuery, command.RunID))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return AppendAttemptEventsResult{}, commandError(ErrorNotFound, operation, "run", command.RunID, "run does not exist")
			}
			return AppendAttemptEventsResult{}, databaseError(operation+" lock run", err)
		}

		result := AppendAttemptEventsResult{Events: make([]AppendedEvent, len(command.Events))}
		newEventIndexes := make([]int, 0, len(command.Events))
		for index, event := range command.Events {
			sequence, exists, matches, err := s.findAttemptEvent(ctx, transaction, command, event)
			if err != nil {
				return AppendAttemptEventsResult{}, err
			}
			result.Events[index] = AppendedEvent{
				EventID:            event.EventID,
				ProducerInstanceID: event.ProducerInstanceID,
				ProducerSeq:        event.ProducerSeq,
				RunSeq:             sequence,
				Duplicate:          exists,
			}
			if exists {
				if !matches {
					return AppendAttemptEventsResult{}, commandError(ErrorEventConflict, operation, "event", event.EventID, "producer key was already used for different event content")
				}
				continue
			}
			newEventIndexes = append(newEventIndexes, index)
		}
		if len(newEventIndexes) == 0 {
			return result, nil
		}

		if run.CurrentAttemptGeneration != command.Generation {
			return AppendAttemptEventsResult{}, &StateError{
				Code:              ErrorLeaseLost,
				Operation:         operation,
				Resource:          "attempt",
				ResourceID:        command.AttemptID,
				CurrentGeneration: run.CurrentAttemptGeneration,
				Message:           "attempt generation was fenced",
			}
		}
		attemptQuery := fmt.Sprintf("SELECT %s FROM %s AS a WHERE a.id = $1 AND a.run_id = $2 FOR UPDATE", attemptColumns("a"), s.table("run_attempts"))
		attempt, err := scanAttempt(transaction.QueryRow(ctx, attemptQuery, command.AttemptID, command.RunID))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return AppendAttemptEventsResult{}, commandError(ErrorNotFound, operation, "attempt", command.AttemptID, "attempt does not exist for run")
			}
			return AppendAttemptEventsResult{}, databaseError(operation+" lock attempt", err)
		}
		if attempt.Generation != command.Generation || !validAttemptEventStatus(attempt.Status) || !validRunLeaseStatus(run.Status) {
			return AppendAttemptEventsResult{}, commandError(ErrorInvalidState, operation, "attempt", command.AttemptID, "run or attempt is not accepting canonical events")
		}
		if err := s.requireLiveLeases(ctx, transaction, run, attempt, command.HolderID, command.Generation); err != nil {
			return AppendAttemptEventsResult{}, err
		}

		var maximumProducerSeq int64
		maxQuery := fmt.Sprintf(`
SELECT COALESCE(pg_catalog.max(producer_seq), 0::bigint)
FROM %s
WHERE run_id = $1 AND producer_instance_id = $2`, s.table("run_events"))
		if err := transaction.QueryRow(ctx, maxQuery, command.RunID, command.Events[0].ProducerInstanceID).Scan(&maximumProducerSeq); err != nil {
			return AppendAttemptEventsResult{}, databaseError(operation+" read producer cursor", err)
		}
		for _, index := range newEventIndexes {
			if command.Events[index].ProducerSeq <= maximumProducerSeq {
				return AppendAttemptEventsResult{}, commandError(ErrorEventConflict, operation, "event", command.Events[index].EventID, "new producer sequence is not greater than the committed producer cursor")
			}
		}

		updateRunQuery := fmt.Sprintf(`
UPDATE %s
SET next_event_seq = next_event_seq + $1,
    updated_at = pg_catalog.clock_timestamp()
WHERE id = $2
RETURNING next_event_seq - $1`, s.table("runs"))
		var firstSequence int64
		if err := transaction.QueryRow(ctx, updateRunQuery, len(newEventIndexes), command.RunID).Scan(&firstSequence); err != nil {
			return AppendAttemptEventsResult{}, databaseError(operation+" allocate run sequence", err)
		}

		nextSequence := firstSequence
		for _, index := range newEventIndexes {
			event := command.Events[index]
			if err := s.insertAttemptEvent(ctx, transaction, command, event, nextSequence); err != nil {
				return AppendAttemptEventsResult{}, err
			}
			result.Events[index].RunSeq = nextSequence
			result.Events[index].Duplicate = false
			nextSequence++
		}
		result.NewCount = len(newEventIndexes)

		outboxPayload, err := marshalTransitionPayload(struct {
			RunID             string `json:"runId"`
			RunAttemptID      string `json:"runAttemptId"`
			AttemptGeneration int64  `json:"runAttemptGeneration"`
			FromSeq           int64  `json:"fromSeq"`
			ToSeq             int64  `json:"toSeq"`
		}{command.RunID, command.AttemptID, command.Generation, firstSequence, nextSequence - 1})
		if err != nil {
			return AppendAttemptEventsResult{}, commandError(ErrorInvalidArgument, operation, "run", command.RunID, err.Error())
		}
		if err := s.insertOutbox(ctx, transaction, command.OutboxID, "run.events.appended", command.RunID, outboxPayload); err != nil {
			return AppendAttemptEventsResult{}, err
		}
		return result, nil
	})
}

func validateAppendAttemptEvents(command AppendAttemptEventsCommand) error {
	if err := validateUUID("run_id", command.RunID); err != nil {
		return err
	}
	if err := validateUUID("attempt_id", command.AttemptID); err != nil {
		return err
	}
	if err := validateBoundedText("holder_id", command.HolderID, 256); err != nil {
		return err
	}
	if command.Generation < 1 {
		return errors.New("generation must be positive")
	}
	if err := validateUUID("outbox_id", command.OutboxID); err != nil {
		return err
	}
	if len(command.Events) == 0 || len(command.Events) > MaxAttemptEventsPerAppend {
		return fmt.Errorf("events must contain between 1 and %d entries", MaxAttemptEventsPerAppend)
	}
	producerID := command.Events[0].ProducerInstanceID
	var previousProducerSeq int64
	eventIDs := make(map[string]struct{}, len(command.Events))
	for index, event := range command.Events {
		if err := validateUUID(fmt.Sprintf("events[%d].event_id", index), event.EventID); err != nil {
			return err
		}
		if _, duplicate := eventIDs[event.EventID]; duplicate {
			return fmt.Errorf("events[%d].event_id is duplicated in the batch", index)
		}
		eventIDs[event.EventID] = struct{}{}
		if err := validateUUID(fmt.Sprintf("events[%d].producer_instance_id", index), event.ProducerInstanceID); err != nil {
			return err
		}
		if event.ProducerInstanceID != producerID {
			return errors.New("all events in one append must use the same producer_instance_id")
		}
		if event.ProducerSeq <= previousProducerSeq {
			return errors.New("producer_seq values must be positive and strictly increasing within the batch")
		}
		previousProducerSeq = event.ProducerSeq
		if !validEventSource(event.Source) {
			return fmt.Errorf("events[%d].source is not supported", index)
		}
		if err := validateBoundedText(fmt.Sprintf("events[%d].kind", index), event.Kind, 128); err != nil {
			return err
		}
		if event.SchemaVersion < 1 || int64(event.SchemaVersion) > math.MaxInt32 {
			return fmt.Errorf("events[%d].schema_version must fit a positive PostgreSQL integer", index)
		}
		if (len(event.Payload) == 0) == (event.Object == nil) {
			return fmt.Errorf("events[%d] must contain exactly one of payload or object", index)
		}
		if event.Object == nil {
			if err := validateInlinePayload(event.Payload); err != nil {
				return fmt.Errorf("events[%d]: %w", index, err)
			}
			continue
		}
		if err := validateUUID(fmt.Sprintf("events[%d].object.object_id", index), event.Object.ObjectID); err != nil {
			return err
		}
		if event.Object.Size < 1 {
			return fmt.Errorf("events[%d].object.size must be positive", index)
		}
		if err := validateBoundedText(fmt.Sprintf("events[%d].object.media_type", index), event.Object.MediaType, 255); err != nil {
			return err
		}
		if strings.ContainsAny(event.Object.MediaType, "\r\n") {
			return fmt.Errorf("events[%d].object.media_type must not contain a line break", index)
		}
	}
	return nil
}

func (s *StateStore) findAttemptEvent(ctx context.Context, transaction pgx.Tx, command AppendAttemptEventsCommand, event AttemptEvent) (sequence int64, exists bool, matches bool, err error) {
	var inlinePayload any
	var objectID any
	var objectHash any
	var objectSize any
	var objectMediaType any
	if event.Object == nil {
		inlinePayload = string(event.Payload)
	} else {
		objectID = event.Object.ObjectID
		objectHash = event.Object.SHA256[:]
		objectSize = event.Object.Size
		objectMediaType = event.Object.MediaType
	}
	query := fmt.Sprintf(`
SELECT seq,
       event_id = $4::uuid
       AND run_attempt_id = $5::uuid
       AND run_attempt_generation = $6
       AND source = $7
       AND kind = $8
       AND schema_version = $9
       AND payload IS NOT DISTINCT FROM $10::jsonb
       AND object_id IS NOT DISTINCT FROM $11::uuid
       AND object_sha256 IS NOT DISTINCT FROM $12::bytea
       AND object_size IS NOT DISTINCT FROM $13::bigint
       AND object_media_type IS NOT DISTINCT FROM $14::text
FROM %s
WHERE run_id = $1 AND producer_instance_id = $2 AND producer_seq = $3`, s.table("run_events"))
	err = transaction.QueryRow(ctx, query,
		command.RunID,
		event.ProducerInstanceID,
		event.ProducerSeq,
		event.EventID,
		command.AttemptID,
		command.Generation,
		event.Source,
		event.Kind,
		event.SchemaVersion,
		inlinePayload,
		objectID,
		objectHash,
		objectSize,
		objectMediaType,
	).Scan(&sequence, &matches)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, false, nil
	}
	if err != nil {
		return 0, false, false, databaseError("AppendAttemptEvents read producer key", err)
	}
	return sequence, true, matches, nil
}

func (s *StateStore) insertAttemptEvent(ctx context.Context, transaction pgx.Tx, command AppendAttemptEventsCommand, event AttemptEvent, sequence int64) error {
	var inlinePayload any
	var objectID any
	var objectHash any
	var objectSize any
	var objectMediaType any
	if event.Object == nil {
		inlinePayload = string(event.Payload)
	} else {
		objectID = event.Object.ObjectID
		objectHash = event.Object.SHA256[:]
		objectSize = event.Object.Size
		objectMediaType = event.Object.MediaType
	}
	query := fmt.Sprintf(`
INSERT INTO %s
    (run_id, seq, event_id, run_attempt_id, run_attempt_generation,
     producer_instance_id, producer_seq, source, kind, schema_version,
     payload, object_id, object_sha256, object_size, object_media_type)
VALUES
    ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
     $11::jsonb, $12, $13, $14, $15)`, s.table("run_events"))
	if _, err := transaction.Exec(ctx, query,
		command.RunID,
		sequence,
		event.EventID,
		command.AttemptID,
		command.Generation,
		event.ProducerInstanceID,
		event.ProducerSeq,
		event.Source,
		event.Kind,
		event.SchemaVersion,
		inlinePayload,
		objectID,
		objectHash,
		objectSize,
		objectMediaType,
	); err != nil {
		var postgresError *pgconn.PgError
		if pgxErrorAs(err, &postgresError) && postgresError.Code == "23505" {
			return commandError(ErrorEventConflict, "AppendAttemptEvents", "event", event.EventID, "event identity or producer key is already in use")
		}
		return databaseError("AppendAttemptEvents insert event", err)
	}
	return nil
}
