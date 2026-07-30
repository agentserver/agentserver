ALTER TABLE execution_operations
    DROP CONSTRAINT execution_operations_status_valid,
    DROP CONSTRAINT execution_operations_dispatch_matches_status,
    DROP CONSTRAINT execution_operations_ack_matches_status,
    DROP CONSTRAINT execution_operations_terminal_matches_status;

ALTER TABLE execution_operations
    ADD CONSTRAINT execution_operations_status_valid CHECK (
        status IN (
            'prepared', 'dispatching', 'acknowledged',
            'succeeded', 'failed', 'cancelled', 'unknown', 'skipped'
        )
    ),
    ADD CONSTRAINT execution_operations_dispatch_matches_status CHECK (
        (
            status IN ('prepared', 'skipped')
            AND connection_generation IS NULL
            AND dispatched_at IS NULL
        )
        OR
        (
            status IN (
                'dispatching', 'acknowledged',
                'succeeded', 'failed', 'cancelled', 'unknown'
            )
            AND connection_generation > 0
            AND dispatched_at IS NOT NULL
        )
    ),
    ADD CONSTRAINT execution_operations_ack_matches_status CHECK (
        (
            status IN ('prepared', 'dispatching', 'skipped')
            AND acknowledgement_hash IS NULL
        )
        OR
        (
            status IN ('acknowledged', 'succeeded', 'failed', 'cancelled')
            AND acknowledgement_hash IS NOT NULL
        )
        OR status = 'unknown'
    ),
    ADD CONSTRAINT execution_operations_terminal_matches_status CHECK (
        (
            status IN ('succeeded', 'failed', 'cancelled', 'unknown', 'skipped')
            AND terminal_result_hash IS NOT NULL
            AND terminal_at IS NOT NULL
        )
        OR
        (
            status IN ('prepared', 'dispatching', 'acknowledged')
            AND terminal_result_hash IS NULL
            AND terminal_at IS NULL
        )
    ),
    ADD CONSTRAINT execution_operations_skipped_kind_valid CHECK (
        status <> 'skipped' OR kind = 'timeout_terminate'
    );
