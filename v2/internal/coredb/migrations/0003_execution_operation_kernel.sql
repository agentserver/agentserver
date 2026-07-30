CREATE TABLE executions (
    id uuid PRIMARY KEY,
    run_id uuid NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    run_attempt_id uuid NOT NULL,
    run_attempt_generation bigint NOT NULL,
    app_server_tool_call_id text NOT NULL,
    executor_id uuid NOT NULL,
    env_id uuid NOT NULL,
    tool_name text NOT NULL,
    tool_version text NOT NULL,
    mapper_version text NOT NULL,
    policy_version text NOT NULL,
    policy_decision text NOT NULL,
    operation_count integer NOT NULL,
    canonicalizer_version text NOT NULL,
    arguments_hash bytea NOT NULL,
    tool_schema_hash bytea NOT NULL,
    operation_plan_hash bytea NOT NULL,
    policy_context_hash bytea NOT NULL,
    status text NOT NULL,
    dispatched_at timestamptz,
    terminal_result_hash bytea,
    terminal_at timestamptz,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT executions_run_tool_call_unique
        UNIQUE (run_id, app_server_tool_call_id),
    CONSTRAINT executions_attempt_fk
        FOREIGN KEY (run_attempt_id, run_id, run_attempt_generation)
        REFERENCES run_attempts(id, run_id, generation),
    CONSTRAINT executions_attempt_generation_positive CHECK (
        run_attempt_generation > 0
    ),
    CONSTRAINT executions_tool_call_id_bounded CHECK (
        pg_catalog.octet_length(app_server_tool_call_id) BETWEEN 1 AND 256
    ),
    CONSTRAINT executions_tool_name_bounded CHECK (
        pg_catalog.octet_length(tool_name) BETWEEN 1 AND 128
    ),
    CONSTRAINT executions_tool_version_bounded CHECK (
        pg_catalog.octet_length(tool_version) BETWEEN 1 AND 128
    ),
    CONSTRAINT executions_mapper_version_bounded CHECK (
        pg_catalog.octet_length(mapper_version) BETWEEN 1 AND 128
    ),
    CONSTRAINT executions_policy_version_bounded CHECK (
        pg_catalog.octet_length(policy_version) BETWEEN 1 AND 128
    ),
    CONSTRAINT executions_policy_decision_valid CHECK (
        policy_decision IN ('allow', 'ask', 'deny')
    ),
    CONSTRAINT executions_operation_count_bounded CHECK (
        operation_count BETWEEN 1 AND 256
    ),
    CONSTRAINT executions_canonicalizer_version_valid CHECK (
        canonicalizer_version = 'rfc8785-v1'
    ),
    CONSTRAINT executions_hashes_sha256 CHECK (
        pg_catalog.octet_length(arguments_hash) = 32
        AND pg_catalog.octet_length(tool_schema_hash) = 32
        AND pg_catalog.octet_length(operation_plan_hash) = 32
        AND pg_catalog.octet_length(policy_context_hash) = 32
        AND (
            terminal_result_hash IS NULL
            OR pg_catalog.octet_length(terminal_result_hash) = 32
        )
    ),
    CONSTRAINT executions_status_valid CHECK (
        status IN (
            'created', 'pending_approval', 'approved', 'denied', 'expired',
            'dispatching', 'running', 'cancelling',
            'succeeded', 'failed', 'cancelled', 'unknown'
        )
    ),
    CONSTRAINT executions_dispatch_time_matches_status CHECK (
        (
            status IN ('created', 'pending_approval', 'approved', 'denied', 'expired')
            AND dispatched_at IS NULL
        )
        OR
        (
            status IN ('dispatching', 'running', 'cancelling', 'succeeded', 'failed', 'unknown')
            AND dispatched_at IS NOT NULL
        )
        OR status = 'cancelled'
    ),
    CONSTRAINT executions_terminal_matches_status CHECK (
        (
            status IN ('denied', 'expired')
            AND terminal_result_hash IS NULL
            AND terminal_at IS NOT NULL
        )
        OR
        (
            status IN ('succeeded', 'failed', 'cancelled', 'unknown')
            AND terminal_result_hash IS NOT NULL
            AND terminal_at IS NOT NULL
        )
        OR
        (
            status IN (
                'created', 'pending_approval', 'approved',
                'dispatching', 'running', 'cancelling'
            )
            AND terminal_result_hash IS NULL
            AND terminal_at IS NULL
        )
    ),
    CONSTRAINT executions_version_positive CHECK (version > 0)
);

CREATE TABLE execution_operations (
    id uuid PRIMARY KEY,
    execution_id uuid NOT NULL REFERENCES executions(id) ON DELETE CASCADE,
    ordinal integer NOT NULL,
    kind text NOT NULL,
    effect_class text NOT NULL,
    mutation_key uuid NOT NULL,
    canonicalizer_version text NOT NULL,
    params_hash bytea NOT NULL,
    status text NOT NULL,
    connection_generation bigint,
    acknowledgement_hash bytea,
    terminal_result_hash bytea,
    dispatched_at timestamptz,
    acknowledged_at timestamptz,
    terminal_at timestamptz,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT execution_operations_execution_ordinal_unique
        UNIQUE (execution_id, ordinal),
    CONSTRAINT execution_operations_mutation_key_unique UNIQUE (mutation_key),
    CONSTRAINT execution_operations_ordinal_positive CHECK (ordinal > 0),
    CONSTRAINT execution_operations_kind_bounded CHECK (
        pg_catalog.octet_length(kind) BETWEEN 1 AND 128
    ),
    CONSTRAINT execution_operations_effect_class_valid CHECK (
        effect_class IN ('read', 'mutation')
    ),
    CONSTRAINT execution_operations_canonicalizer_version_valid CHECK (
        canonicalizer_version = 'rfc8785-v1'
    ),
    CONSTRAINT execution_operations_hashes_sha256 CHECK (
        pg_catalog.octet_length(params_hash) = 32
        AND (
            acknowledgement_hash IS NULL
            OR pg_catalog.octet_length(acknowledgement_hash) = 32
        )
        AND (
            terminal_result_hash IS NULL
            OR pg_catalog.octet_length(terminal_result_hash) = 32
        )
    ),
    CONSTRAINT execution_operations_status_valid CHECK (
        status IN (
            'prepared', 'dispatching', 'acknowledged',
            'succeeded', 'failed', 'cancelled', 'unknown'
        )
    ),
    CONSTRAINT execution_operations_dispatch_matches_status CHECK (
        (
            status = 'prepared'
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
    CONSTRAINT execution_operations_ack_pair CHECK (
        (acknowledgement_hash IS NULL AND acknowledged_at IS NULL)
        OR
        (acknowledgement_hash IS NOT NULL AND acknowledged_at IS NOT NULL)
    ),
    CONSTRAINT execution_operations_ack_matches_status CHECK (
        (
            status IN ('prepared', 'dispatching')
            AND acknowledgement_hash IS NULL
        )
        OR
        (
            status IN ('acknowledged', 'succeeded', 'failed', 'cancelled')
            AND acknowledgement_hash IS NOT NULL
        )
        OR status = 'unknown'
    ),
    CONSTRAINT execution_operations_terminal_matches_status CHECK (
        (
            status IN ('succeeded', 'failed', 'cancelled', 'unknown')
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
    CONSTRAINT execution_operations_version_positive CHECK (version > 0)
);

CREATE INDEX executions_run_status_created_idx
    ON executions (run_id, status, created_at);

CREATE INDEX execution_operations_execution_status_ordinal_idx
    ON execution_operations (execution_id, status, ordinal);
