CREATE TABLE workspaces (
    id uuid PRIMARY KEY,
    status text NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT workspaces_status_valid CHECK (status IN ('active', 'suspended', 'deleted')),
    CONSTRAINT workspaces_version_positive CHECK (version > 0)
);

CREATE TABLE sessions (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id),
    active_run_id uuid,
    latest_checkpoint_id uuid,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT sessions_identity_workspace_unique UNIQUE (id, workspace_id),
    CONSTRAINT sessions_version_positive CHECK (version > 0)
);

CREATE TABLE runs (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL,
    session_id uuid NOT NULL,
    actor_id uuid NOT NULL,
    status text NOT NULL,
    request_hash bytea NOT NULL,
    idempotency_key text NOT NULL,
    current_attempt_generation bigint NOT NULL DEFAULT 0,
    next_event_seq bigint NOT NULL DEFAULT 1,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT runs_session_workspace_fk
        FOREIGN KEY (session_id, workspace_id)
        REFERENCES sessions(id, workspace_id),
    CONSTRAINT runs_identity_session_unique UNIQUE (id, session_id),
    CONSTRAINT runs_identity_workspace_session_unique UNIQUE (id, workspace_id, session_id),
    CONSTRAINT runs_idempotency_unique
        UNIQUE (workspace_id, actor_id, session_id, idempotency_key),
    CONSTRAINT runs_status_valid CHECK (
        status IN (
            'queued', 'claimed', 'running', 'finalizing',
            'completed', 'failed', 'interrupted', 'cancelling', 'cancelled'
        )
    ),
    CONSTRAINT runs_request_hash_sha256 CHECK (octet_length(request_hash) = 32),
    CONSTRAINT runs_idempotency_key_bounded CHECK (
        octet_length(idempotency_key) BETWEEN 1 AND 256
    ),
    CONSTRAINT runs_attempt_generation_nonnegative CHECK (current_attempt_generation >= 0),
    CONSTRAINT runs_next_event_seq_positive CHECK (next_event_seq > 0),
    CONSTRAINT runs_version_positive CHECK (version > 0)
);

ALTER TABLE sessions
    ADD CONSTRAINT sessions_active_run_same_session_fk
    FOREIGN KEY (active_run_id, id)
    REFERENCES runs(id, session_id)
    DEFERRABLE INITIALLY IMMEDIATE;

CREATE TABLE run_attempts (
    id uuid PRIMARY KEY,
    run_id uuid NOT NULL REFERENCES runs(id),
    generation bigint NOT NULL,
    status text NOT NULL,
    turn_started_at timestamptz,
    holder_id text,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT run_attempts_run_generation_unique UNIQUE (run_id, generation),
    CONSTRAINT run_attempts_identity_generation_unique UNIQUE (id, generation),
    CONSTRAINT run_attempts_identity_run_generation_unique UNIQUE (id, run_id, generation),
    CONSTRAINT run_attempts_generation_positive CHECK (generation > 0),
    CONSTRAINT run_attempts_status_valid CHECK (
        status IN (
            'created', 'leased', 'starting', 'running', 'finalizing',
            'succeeded', 'failed', 'interrupted', 'fenced'
        )
    ),
    CONSTRAINT run_attempts_holder_bounded CHECK (
        holder_id IS NULL OR octet_length(holder_id) BETWEEN 1 AND 256
    ),
    CONSTRAINT run_attempts_version_positive CHECK (version > 0)
);

CREATE TABLE session_leases (
    session_id uuid PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    run_id uuid NOT NULL,
    holder_id text NOT NULL,
    generation bigint NOT NULL,
    expires_at timestamptz NOT NULL,
    acquired_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    renewed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT session_leases_run_session_fk
        FOREIGN KEY (run_id, session_id)
        REFERENCES runs(id, session_id),
    CONSTRAINT session_leases_holder_bounded CHECK (
        octet_length(holder_id) BETWEEN 1 AND 256
    ),
    CONSTRAINT session_leases_generation_positive CHECK (generation > 0)
);

CREATE TABLE attempt_leases (
    run_attempt_id uuid PRIMARY KEY REFERENCES run_attempts(id) ON DELETE CASCADE,
    holder_id text NOT NULL,
    generation bigint NOT NULL,
    expires_at timestamptz NOT NULL,
    acquired_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    renewed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT attempt_leases_attempt_generation_fk
        FOREIGN KEY (run_attempt_id, generation)
        REFERENCES run_attempts(id, generation),
    CONSTRAINT attempt_leases_holder_bounded CHECK (
        octet_length(holder_id) BETWEEN 1 AND 256
    ),
    CONSTRAINT attempt_leases_generation_positive CHECK (generation > 0)
);

CREATE TABLE run_events (
    run_id uuid NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    seq bigint NOT NULL,
    event_id uuid NOT NULL,
    run_attempt_id uuid,
    run_attempt_generation bigint,
    producer_instance_id uuid NOT NULL,
    producer_seq bigint NOT NULL,
    kind text NOT NULL,
    schema_version integer NOT NULL,
    payload jsonb,
    object_id uuid,
    object_sha256 bytea,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (run_id, seq),
    CONSTRAINT run_events_event_id_unique UNIQUE (event_id),
    CONSTRAINT run_events_producer_key_unique
        UNIQUE (run_id, producer_instance_id, producer_seq),
    CONSTRAINT run_events_attempt_fk
        FOREIGN KEY (run_attempt_id, run_id, run_attempt_generation)
        REFERENCES run_attempts(id, run_id, generation),
    CONSTRAINT run_events_seq_positive CHECK (seq > 0),
    CONSTRAINT run_events_producer_seq_positive CHECK (producer_seq > 0),
    CONSTRAINT run_events_kind_bounded CHECK (octet_length(kind) BETWEEN 1 AND 128),
    CONSTRAINT run_events_schema_version_positive CHECK (schema_version > 0),
    CONSTRAINT run_events_attempt_scope_complete CHECK (
        (run_attempt_id IS NULL AND run_attempt_generation IS NULL)
        OR
        (run_attempt_id IS NOT NULL AND run_attempt_generation IS NOT NULL)
    ),
    CONSTRAINT run_events_payload_or_object CHECK (
        (payload IS NOT NULL AND object_id IS NULL AND object_sha256 IS NULL)
        OR
        (payload IS NULL AND object_id IS NOT NULL AND object_sha256 IS NOT NULL)
    ),
    CONSTRAINT run_events_object_hash_sha256 CHECK (
        object_sha256 IS NULL OR octet_length(object_sha256) = 32
    )
);

CREATE TABLE outbox (
    id uuid PRIMARY KEY,
    kind text NOT NULL,
    aggregate_id uuid NOT NULL,
    payload jsonb NOT NULL,
    available_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    lock_owner text,
    lock_until timestamptz,
    attempts integer NOT NULL DEFAULT 0,
    completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT outbox_kind_bounded CHECK (octet_length(kind) BETWEEN 1 AND 128),
    CONSTRAINT outbox_lock_owner_bounded CHECK (
        lock_owner IS NULL OR octet_length(lock_owner) BETWEEN 1 AND 256
    ),
    CONSTRAINT outbox_lock_pair CHECK (
        (lock_owner IS NULL AND lock_until IS NULL)
        OR
        (lock_owner IS NOT NULL AND lock_until IS NOT NULL)
    ),
    CONSTRAINT outbox_attempts_nonnegative CHECK (attempts >= 0),
    CONSTRAINT outbox_completed_unlocked CHECK (
        completed_at IS NULL OR (lock_owner IS NULL AND lock_until IS NULL)
    )
);

CREATE INDEX runs_session_status_created_idx
    ON runs (session_id, status, created_at);

CREATE INDEX run_attempts_run_created_idx
    ON run_attempts (run_id, generation DESC);

CREATE INDEX run_events_run_created_idx
    ON run_events (run_id, created_at);

CREATE INDEX outbox_claim_idx
    ON outbox (available_at, id)
    WHERE completed_at IS NULL;
