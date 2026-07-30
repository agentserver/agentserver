ALTER TABLE runs
    DROP CONSTRAINT runs_status_valid;

UPDATE runs
SET status = 'starting'
WHERE status = 'claimed';

ALTER TABLE runs
    ADD CONSTRAINT runs_status_valid CHECK (
        status IN (
            'queued', 'starting', 'running', 'finalizing',
            'completed', 'failed', 'interrupted', 'cancelling', 'cancelled'
        )
    );

DO $migration$
BEGIN
    IF EXISTS (SELECT 1 FROM run_events) THEN
        RAISE EXCEPTION
            'migration 0002 requires an empty pre-runtime run_events table; manually inserted PR 8 development rows must be removed explicitly';
    END IF;
END
$migration$;

ALTER TABLE run_events
    ADD COLUMN source text NOT NULL,
    ADD COLUMN object_size bigint,
    ADD COLUMN object_media_type text;

ALTER TABLE run_events
    DROP CONSTRAINT run_events_payload_or_object;

ALTER TABLE run_events
    ADD CONSTRAINT run_events_source_valid CHECK (
        source IN ('brain', 'executor', 'system', 'approval')
    ),
    ADD CONSTRAINT run_events_payload_or_object CHECK (
        (
            payload IS NOT NULL
            AND object_id IS NULL
            AND object_sha256 IS NULL
            AND object_size IS NULL
            AND object_media_type IS NULL
        )
        OR
        (
            payload IS NULL
            AND object_id IS NOT NULL
            AND object_sha256 IS NOT NULL
            AND object_size IS NOT NULL
            AND object_media_type IS NOT NULL
        )
    ),
    ADD CONSTRAINT run_events_object_size_positive CHECK (
        object_size IS NULL OR object_size > 0
    ),
    ADD CONSTRAINT run_events_object_media_type_bounded CHECK (
        object_media_type IS NULL
        OR pg_catalog.octet_length(object_media_type) BETWEEN 1 AND 255
    );

DROP INDEX outbox_claim_idx;

CREATE INDEX outbox_claim_idx
    ON outbox (available_at, lock_until, id)
    WHERE completed_at IS NULL;
