ALTER TABLE executor_environments
    ADD COLUMN backend_kind text NOT NULL DEFAULT 'agentx',
    ADD CONSTRAINT executor_environments_backend_kind_valid CHECK (
        backend_kind IN ('agentx', 'tae')
    ),
    ADD CONSTRAINT executor_environments_backend_root_valid CHECK (
        (backend_kind = 'agentx' AND root_descriptor->>'kind' = 'local')
        OR
        (
            backend_kind = 'tae'
            AND root_descriptor->>'kind' = 'managed'
            AND platform = 'linux-amd64'
            AND insecure_dev = false
        )
    );

ALTER TABLE executions
    ALTER COLUMN executor_id DROP NOT NULL,
    ADD COLUMN target_kind text,
    ADD COLUMN target_id uuid,
    ADD COLUMN target_generation bigint;

UPDATE executions
SET target_kind = 'agentx',
    target_id = executor_id;

UPDATE executions AS execution
SET target_generation = source.generation
FROM (
    SELECT operation.execution_id, pg_catalog.max(operation.connection_generation) AS generation
    FROM execution_operations AS operation
    WHERE operation.connection_generation IS NOT NULL
    GROUP BY operation.execution_id
) AS source
WHERE execution.id = source.execution_id;

ALTER TABLE executions
    ADD CONSTRAINT executions_target_complete CHECK (
        (target_kind IS NULL AND target_id IS NULL AND target_generation IS NULL)
        OR
        (
            target_kind IN ('agentx', 'tae')
            AND target_id IS NOT NULL
            AND (target_generation IS NULL OR target_generation > 0)
        )
    ),
    ADD CONSTRAINT executions_agentx_legacy_projection CHECK (
        target_kind IS DISTINCT FROM 'agentx'
        OR executor_id = target_id
    );

ALTER TABLE execution_operations
    DROP CONSTRAINT execution_operations_dispatch_matches_status,
    ADD COLUMN target_kind text,
    ADD COLUMN target_id uuid,
    ADD COLUMN target_generation bigint;

UPDATE execution_operations AS operation
SET target_kind = execution.target_kind,
    target_id = execution.target_id,
    target_generation = COALESCE(operation.connection_generation, execution.target_generation)
FROM executions AS execution
WHERE execution.id = operation.execution_id;

ALTER TABLE execution_operations
    ADD CONSTRAINT execution_operations_target_complete CHECK (
        (target_kind IS NULL AND target_id IS NULL AND target_generation IS NULL)
        OR
        (
            target_kind IN ('agentx', 'tae')
            AND target_id IS NOT NULL
            AND (target_generation IS NULL OR target_generation > 0)
        )
    ),
    ADD CONSTRAINT execution_operations_agentx_generation_projection CHECK (
        target_kind IS DISTINCT FROM 'agentx'
        OR target_generation IS NULL
        OR connection_generation = target_generation
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
            AND target_kind IN ('agentx', 'tae')
            AND target_id IS NOT NULL
            AND target_generation > 0
            AND dispatched_at IS NOT NULL
            AND (
                (target_kind = 'agentx' AND connection_generation = target_generation)
                OR
                (target_kind = 'tae' AND connection_generation IS NULL)
            )
        )
    );

CREATE INDEX executions_target_status_created_idx
    ON executions (target_kind, target_id, target_generation, status, created_at, id)
    WHERE status IN ('dispatching', 'running', 'cancelling');

CREATE TABLE managed_sandboxes (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id),
    session_id uuid NOT NULL,
    environment_id uuid NOT NULL,
    provider_kind text NOT NULL DEFAULT 'tae',
    generation bigint NOT NULL,
    desired_state text NOT NULL,
    observed_state text NOT NULL,
    provider_region text NOT NULL,
    provider_psm text NOT NULL,
    provider_session_ref text,
    create_idempotency_key uuid NOT NULL,
    runtime_profile_digest bytea NOT NULL,
    pack_set_digest bytea NOT NULL,
    requested_ttl_seconds bigint NOT NULL,
    idle_ttl_seconds bigint NOT NULL,
    expires_at timestamptz,
    idle_expires_at timestamptz,
    last_observed_at timestamptz,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    deleted_at timestamptz,
    last_error_code text,
    last_error_digest bytea,
    CONSTRAINT managed_sandboxes_session_fk
        FOREIGN KEY (session_id, workspace_id) REFERENCES sessions(id, workspace_id),
    CONSTRAINT managed_sandboxes_identity_generation_unique UNIQUE (id, generation),
    CONSTRAINT managed_sandboxes_provider_valid CHECK (provider_kind = 'tae'),
    CONSTRAINT managed_sandboxes_generation_positive CHECK (generation > 0),
    CONSTRAINT managed_sandboxes_desired_state_valid CHECK (
        desired_state IN ('ready', 'deleted')
    ),
    CONSTRAINT managed_sandboxes_observed_state_valid CHECK (
        observed_state IN (
            'reserved', 'creating', 'ready', 'deleting',
            'deleted', 'failed', 'unknown'
        )
    ),
    CONSTRAINT managed_sandboxes_provider_region_bounded CHECK (
        pg_catalog.octet_length(provider_region) BETWEEN 1 AND 128
    ),
    CONSTRAINT managed_sandboxes_provider_psm_bounded CHECK (
        pg_catalog.octet_length(provider_psm) BETWEEN 1 AND 256
    ),
    CONSTRAINT managed_sandboxes_provider_ref_bounded CHECK (
        provider_session_ref IS NULL
        OR pg_catalog.octet_length(provider_session_ref) BETWEEN 1 AND 1024
    ),
    CONSTRAINT managed_sandboxes_digests_sha256 CHECK (
        pg_catalog.octet_length(runtime_profile_digest) = 32
        AND pg_catalog.octet_length(pack_set_digest) = 32
        AND (
            last_error_digest IS NULL
            OR pg_catalog.octet_length(last_error_digest) = 32
        )
    ),
    CONSTRAINT managed_sandboxes_ttls_valid CHECK (
        requested_ttl_seconds BETWEEN 30 AND 86400
        AND idle_ttl_seconds BETWEEN 1 AND 86400
    ),
    CONSTRAINT managed_sandboxes_ready_projection CHECK (
        observed_state <> 'ready'
        OR (
            provider_session_ref IS NOT NULL
            AND expires_at IS NOT NULL
            AND last_observed_at IS NOT NULL
        )
    ),
    CONSTRAINT managed_sandboxes_deleted_projection CHECK (
        observed_state <> 'deleted'
        OR (desired_state = 'deleted' AND deleted_at IS NOT NULL)
    ),
    CONSTRAINT managed_sandboxes_error_code_bounded CHECK (
        last_error_code IS NULL
        OR pg_catalog.octet_length(last_error_code) BETWEEN 1 AND 128
    ),
    CONSTRAINT managed_sandboxes_version_positive CHECK (version > 0)
);

CREATE UNIQUE INDEX managed_sandboxes_active_session_environment_unique
    ON managed_sandboxes (workspace_id, session_id, environment_id)
    WHERE desired_state <> 'deleted' OR observed_state <> 'deleted';

CREATE INDEX managed_sandboxes_reconcile_idx
    ON managed_sandboxes (desired_state, observed_state, updated_at, id)
    WHERE observed_state NOT IN ('deleted', 'ready')
       OR desired_state <> observed_state;

CREATE TABLE managed_sandbox_activities (
    sandbox_id uuid NOT NULL,
    target_generation bigint NOT NULL,
    run_attempt_id uuid NOT NULL,
    run_attempt_generation bigint NOT NULL,
    lease_expires_at timestamptz NOT NULL,
    released_at timestamptz,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    PRIMARY KEY (sandbox_id, run_attempt_id, run_attempt_generation),
    CONSTRAINT managed_sandbox_activities_sandbox_fk
        FOREIGN KEY (sandbox_id, target_generation)
        REFERENCES managed_sandboxes(id, generation) ON DELETE CASCADE,
    CONSTRAINT managed_sandbox_activities_attempt_fk
        FOREIGN KEY (run_attempt_id, run_attempt_generation)
        REFERENCES run_attempts(id, generation) ON DELETE CASCADE,
    CONSTRAINT managed_sandbox_activities_generations_positive CHECK (
        target_generation > 0 AND run_attempt_generation > 0
    ),
    CONSTRAINT managed_sandbox_activities_release_valid CHECK (
        released_at IS NULL OR released_at <= updated_at
    ),
    CONSTRAINT managed_sandbox_activities_version_positive CHECK (version > 0)
);

CREATE INDEX managed_sandbox_activities_live_idx
    ON managed_sandbox_activities (sandbox_id, target_generation, lease_expires_at)
    WHERE released_at IS NULL;
