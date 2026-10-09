-- Git credentials stay in workspace_credential_bindings. This table contains
-- only the repository identity and a credential binding reference, never tokens.
CREATE TABLE workspace_repository_settings (
    workspace_id uuid PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
    source jsonb,
    version bigint NOT NULL CHECK (version BETWEEN 1 AND 9007199254740991),
    updated_by uuid NOT NULL REFERENCES users(id),
    updated_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    CONSTRAINT workspace_repository_source_bounded CHECK (
        source IS NULL OR (pg_catalog.jsonb_typeof(source) = 'object'
            AND pg_catalog.octet_length(source::text) BETWEEN 2 AND 16384)
    )
);

CREATE TABLE workspace_repository_setting_events (
    event_id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    actor_id uuid NOT NULL REFERENCES users(id),
    previous_source jsonb,
    current_source jsonb,
    setting_version bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp()
);
CREATE INDEX workspace_repository_setting_events_workspace_idx
    ON workspace_repository_setting_events (workspace_id, created_at, event_id);
