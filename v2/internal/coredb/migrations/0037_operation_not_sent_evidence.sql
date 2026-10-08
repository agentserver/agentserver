-- A credential injection failure happens after Core grants a one-shot dispatch
-- but before the executor contacts the runtime. Keep that evidence distinct
-- from an acknowledged process failure and an ambiguous/lost dispatch.
ALTER TABLE execution_operations
    ADD COLUMN dispatch_not_sent boolean NOT NULL DEFAULT false,
    DROP CONSTRAINT execution_operations_ack_matches_status;

ALTER TABLE execution_operations
    ADD CONSTRAINT execution_operations_not_sent_valid CHECK (
        NOT dispatch_not_sent OR (
            status = 'failed' AND kind = 'process_start'
            AND target_kind IN ('tae', 'k8s')
            AND acknowledgement_hash IS NULL AND acknowledged_at IS NULL
        )
    ),
    ADD CONSTRAINT execution_operations_ack_matches_status CHECK (
        (status IN ('prepared', 'dispatching', 'skipped') AND acknowledgement_hash IS NULL)
        OR (status IN ('acknowledged', 'succeeded', 'failed', 'cancelled') AND acknowledgement_hash IS NOT NULL)
        OR status = 'unknown'
        OR (status = 'failed' AND dispatch_not_sent)
    );
