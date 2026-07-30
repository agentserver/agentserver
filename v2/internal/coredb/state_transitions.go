package coredb

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (s *StateStore) insertTransitionEvent(
	ctx context.Context,
	transaction pgx.Tx,
	runID string,
	seq int64,
	attemptID *string,
	generation *int64,
	record TransitionRecord,
	source string,
	kind string,
	payload []byte,
) error {
	query := fmt.Sprintf(`
INSERT INTO %s
    (run_id, seq, event_id, run_attempt_id, run_attempt_generation,
     producer_instance_id, producer_seq, source, kind, schema_version, payload)
VALUES
    ($1, $2, $3, $4, $5, $6, $7, $8, $9, 1, $10::jsonb)`, s.table("run_events"))
	if _, err := transaction.Exec(ctx, query,
		runID,
		seq,
		record.EventID,
		attemptID,
		generation,
		record.ProducerInstanceID,
		record.ProducerSeq,
		source,
		kind,
		string(payload),
	); err != nil {
		return classifyTransitionInsertError("insert "+kind+" event", runID, err)
	}
	return nil
}

func (s *StateStore) insertOutbox(
	ctx context.Context,
	transaction pgx.Tx,
	id string,
	kind string,
	aggregateID string,
	payload []byte,
) error {
	query := fmt.Sprintf(`
INSERT INTO %s (id, kind, aggregate_id, payload)
VALUES ($1, $2, $3, $4::jsonb)`, s.table("outbox"))
	if _, err := transaction.Exec(ctx, query, id, kind, aggregateID, string(payload)); err != nil {
		return classifyTransitionInsertError("insert "+kind+" outbox", aggregateID, err)
	}
	return nil
}

func marshalTransitionPayload(value any) ([]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal transition payload: %w", err)
	}
	return payload, nil
}

func classifyTransitionInsertError(operation, resourceID string, err error) error {
	var postgresError *pgconn.PgError
	if pgxErrorAs(err, &postgresError) && postgresError.Code == "23505" {
		return commandError(ErrorConflict, operation, "run", resourceID, "event or outbox identity is already in use")
	}
	return databaseError(operation, err)
}

func pgxErrorAs(err error, target **pgconn.PgError) bool {
	for err != nil {
		if postgresError, ok := err.(*pgconn.PgError); ok {
			*target = postgresError
			return true
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapper.Unwrap()
	}
	return false
}
