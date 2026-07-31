CREATE TABLE workspace_members (
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    user_id uuid NOT NULL,
    role text NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    PRIMARY KEY (workspace_id, user_id),
    CONSTRAINT workspace_members_role_valid CHECK (
        role IN ('owner', 'developer', 'viewer')
    ),
    CONSTRAINT workspace_members_version_positive CHECK (version > 0)
);

CREATE INDEX workspace_members_user_workspace_idx
    ON workspace_members (user_id, workspace_id);

-- Retention may remove canonical events only after committing a complete UI
-- snapshot at a lifecycle boundary. This table is the authority that lets the
-- public cursor return cursor_expired + snapshot/rebaseCursor without
-- pretending that an arbitrary remaining event is safe to resume from.
CREATE TABLE run_event_rebases (
    run_id uuid PRIMARY KEY REFERENCES runs(id) ON DELETE CASCADE,
    after_seq bigint NOT NULL,
    run_status text NOT NULL,
    run_version bigint NOT NULL,
    run_updated_at timestamptz NOT NULL,
    snapshot jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    CONSTRAINT run_event_rebases_after_seq_json_safe CHECK (
        after_seq BETWEEN 1 AND 9007199254740990
    ),
    CONSTRAINT run_event_rebases_status_valid CHECK (
        run_status IN (
            'queued', 'starting', 'running', 'finalizing', 'completed',
            'failed', 'interrupted', 'cancelling', 'cancelled'
        )
    ),
    CONSTRAINT run_event_rebases_version_json_safe CHECK (
        run_version BETWEEN 1 AND 9007199254740990
    ),
    CONSTRAINT run_event_rebases_materialization_time_order CHECK (
        run_updated_at <= created_at
    ),
    CONSTRAINT run_event_rebases_snapshot_object_bounded CHECK (
        pg_catalog.jsonb_typeof(snapshot) = 'object'
        AND pg_catalog.octet_length(snapshot::text) BETWEEN 2 AND 1048576
    )
);
