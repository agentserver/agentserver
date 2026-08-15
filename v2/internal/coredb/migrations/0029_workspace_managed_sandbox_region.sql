CREATE TABLE workspace_managed_sandbox_settings (
    workspace_id uuid PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
    region text NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    updated_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    CONSTRAINT workspace_managed_sandbox_settings_region_valid CHECK (
        region IN ('cn', 'boe', 'i18n-bd', 'i18n-tt')
    ),
    CONSTRAINT workspace_managed_sandbox_settings_version_positive CHECK (version > 0)
);

-- Preserve the previous single-region behavior for every existing workspace.
-- The earliest owner is the deterministic audit actor for this migration-only
-- backfill; all later mutations carry the authenticated caller explicitly.
INSERT INTO workspace_managed_sandbox_settings (workspace_id, region, updated_by)
SELECT workspace.id, 'i18n-tt', owner.user_id
FROM workspaces AS workspace
JOIN LATERAL (
    SELECT member.user_id
    FROM workspace_members AS member
    WHERE member.workspace_id = workspace.id AND member.role = 'owner'
    ORDER BY member.created_at, member.user_id
    LIMIT 1
) AS owner ON true;

CREATE TABLE workspace_managed_sandbox_setting_events (
    event_id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    actor_id uuid NOT NULL REFERENCES users(id),
    previous_region text NOT NULL,
    current_region text NOT NULL,
    setting_version bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    CONSTRAINT workspace_managed_sandbox_setting_events_previous_valid CHECK (
        previous_region IN ('cn', 'boe', 'i18n-bd', 'i18n-tt')
    ),
    CONSTRAINT workspace_managed_sandbox_setting_events_current_valid CHECK (
        current_region IN ('cn', 'boe', 'i18n-bd', 'i18n-tt')
    ),
    CONSTRAINT workspace_managed_sandbox_setting_events_changed CHECK (
        previous_region <> current_region
    ),
    CONSTRAINT workspace_managed_sandbox_setting_events_version_positive CHECK (
        setting_version > 0
    )
);

CREATE INDEX workspace_managed_sandbox_setting_events_workspace_created_idx
    ON workspace_managed_sandbox_setting_events(workspace_id, created_at DESC, event_id);

ALTER TABLE run_launch_states
    ADD COLUMN managed_sandbox_setting_version bigint,
    ADD COLUMN managed_sandbox_region text,
    ADD COLUMN managed_sandbox_profile_id text,
    ADD COLUMN managed_sandbox_binding_sha256 bytea,
    ADD COLUMN managed_sandbox_environment_id uuid,
    ADD CONSTRAINT run_launch_states_managed_sandbox_complete CHECK (
        (
            managed_sandbox_setting_version IS NULL
            AND managed_sandbox_region IS NULL
            AND managed_sandbox_profile_id IS NULL
            AND managed_sandbox_binding_sha256 IS NULL
            AND managed_sandbox_environment_id IS NULL
        )
        OR
        (
            managed_sandbox_setting_version > 0
            AND managed_sandbox_region IN ('cn', 'boe', 'i18n-bd', 'i18n-tt')
            AND pg_catalog.octet_length(managed_sandbox_profile_id) BETWEEN 1 AND 128
            AND managed_sandbox_profile_id ~ '(^[a-z0-9]$)|(^[a-z0-9][a-z0-9._-]{0,126}[a-z0-9]$)'
            AND pg_catalog.octet_length(managed_sandbox_binding_sha256) = 32
            AND managed_sandbox_environment_id IS NOT NULL
        )
    );

CREATE INDEX run_launch_states_managed_sandbox_profile_idx
    ON run_launch_states(managed_sandbox_region, managed_sandbox_profile_id, run_id)
    WHERE managed_sandbox_profile_id IS NOT NULL;
