ALTER TABLE workspaces
    ADD COLUMN managed_lark_credential_mode text NOT NULL DEFAULT 'webhook_swap',
    ADD CONSTRAINT workspaces_managed_lark_credential_mode_valid CHECK (
        managed_lark_credential_mode IN ('webhook_swap', 'process_env')
    );

-- The DEFAULT above exists only to backfill rows created by earlier
-- migrations. Every new workspace must choose its own mode explicitly.
ALTER TABLE workspaces
    ALTER COLUMN managed_lark_credential_mode DROP DEFAULT;

CREATE TABLE workspace_managed_credential_mode_events (
    event_id       uuid PRIMARY KEY,
    workspace_id   uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    actor_id       uuid NOT NULL REFERENCES users(id),
    previous_mode  text NOT NULL,
    current_mode   text NOT NULL,
    workspace_version bigint NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    CONSTRAINT workspace_managed_credential_mode_events_previous_valid CHECK (
        previous_mode IN ('webhook_swap', 'process_env')
    ),
    CONSTRAINT workspace_managed_credential_mode_events_current_valid CHECK (
        current_mode IN ('webhook_swap', 'process_env')
    ),
    CONSTRAINT workspace_managed_credential_mode_events_changed CHECK (
        previous_mode <> current_mode
    ),
    CONSTRAINT workspace_managed_credential_mode_events_version_positive CHECK (
        workspace_version > 0
    )
);

CREATE INDEX workspace_managed_credential_mode_events_workspace_created_idx
    ON workspace_managed_credential_mode_events(workspace_id, created_at DESC, event_id);
