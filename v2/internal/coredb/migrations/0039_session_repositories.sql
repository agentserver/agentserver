-- Independent from workspace defaults and sandbox-claim TTLs. These rows are
-- source identity, not secrets; credential material remains in sealed storage.
CREATE TABLE session_repositories (
    session_id uuid PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    binding jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    CONSTRAINT session_repository_binding_object CHECK (
        pg_catalog.jsonb_typeof(binding) = 'object'
        AND pg_catalog.octet_length(binding::text) BETWEEN 2 AND 16384
    )
);
CREATE INDEX session_repositories_workspace_idx ON session_repositories (workspace_id, session_id);

-- A run snapshot remains readable even if the workspace default changes.
ALTER TABLE run_launch_states ADD COLUMN repository_binding jsonb,
    ADD CONSTRAINT run_repository_binding_object CHECK (
        repository_binding IS NULL OR (
            pg_catalog.jsonb_typeof(repository_binding) = 'object'
            AND pg_catalog.octet_length(repository_binding::text) BETWEEN 2 AND 16384
        )
    );
