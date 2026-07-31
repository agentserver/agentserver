CREATE TABLE run_launch_states (
    run_id uuid PRIMARY KEY REFERENCES runs(id) ON DELETE CASCADE,
    workspace_id uuid NOT NULL,
    session_id uuid NOT NULL,
    prompt_object_id uuid NOT NULL,
    prompt_sha256 bytea NOT NULL,
    prompt_size bigint NOT NULL,
    prompt_media_type text NOT NULL,
    executor_policy_version text NOT NULL,
    executor_policy_context_digest bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    CONSTRAINT run_launch_states_run_scope_fk
        FOREIGN KEY (run_id, workspace_id, session_id)
        REFERENCES runs(id, workspace_id, session_id),
    CONSTRAINT run_launch_states_prompt_sha256_exact CHECK (
        pg_catalog.octet_length(prompt_sha256) = 32
    ),
    CONSTRAINT run_launch_states_prompt_size_bounded CHECK (
        prompt_size BETWEEN 1 AND 1099511627776
    ),
    CONSTRAINT run_launch_states_prompt_media_type_bounded CHECK (
        pg_catalog.octet_length(prompt_media_type) BETWEEN 1 AND 255
        AND pg_catalog.strpos(prompt_media_type, pg_catalog.chr(10)) = 0
        AND pg_catalog.strpos(prompt_media_type, pg_catalog.chr(13)) = 0
    ),
    CONSTRAINT run_launch_states_policy_version_bounded CHECK (
        pg_catalog.octet_length(executor_policy_version) BETWEEN 1 AND 128
    ),
    CONSTRAINT run_launch_states_policy_digest_exact CHECK (
        pg_catalog.octet_length(executor_policy_context_digest) = 32
    )
);

CREATE TABLE run_launch_allowed_tools (
    run_id uuid NOT NULL REFERENCES run_launch_states(run_id) ON DELETE CASCADE,
    ordinal smallint NOT NULL,
    tool_name text NOT NULL,
    PRIMARY KEY (run_id, ordinal),
    CONSTRAINT run_launch_allowed_tools_name_unique UNIQUE (run_id, tool_name),
    CONSTRAINT run_launch_allowed_tools_ordinal_bounded CHECK (
        ordinal BETWEEN 1 AND 64
    ),
    CONSTRAINT run_launch_allowed_tools_name_bounded CHECK (
        pg_catalog.octet_length(tool_name) BETWEEN 1 AND 128
    )
);

ALTER TABLE brain_tool_catalogs
    ADD CONSTRAINT brain_tool_catalogs_identity_scope_thread_unique
    UNIQUE (id, workspace_id, session_id, thread_id);

CREATE TABLE checkpoints (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id),
    session_id uuid NOT NULL,
    run_id uuid NOT NULL,
    run_attempt_id uuid NOT NULL,
    attempt_generation bigint NOT NULL,
    brain_tool_catalog_id uuid NOT NULL,
    thread_id text NOT NULL,
    turn_id text NOT NULL,
    manifest_digest bytea NOT NULL,
    object_id uuid NOT NULL,
    object_sha256 bytea NOT NULL,
    object_size bigint NOT NULL,
    object_media_type text NOT NULL,
    codex_runtime_manifest_digest bytea NOT NULL,
    checkpoint_allowlist_version bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    CONSTRAINT checkpoints_identity_session_unique UNIQUE (id, session_id),
    CONSTRAINT checkpoints_run_unique UNIQUE (run_id),
    CONSTRAINT checkpoints_run_scope_fk
        FOREIGN KEY (run_id, workspace_id, session_id)
        REFERENCES runs(id, workspace_id, session_id),
    CONSTRAINT checkpoints_attempt_scope_fk
        FOREIGN KEY (run_attempt_id, run_id, attempt_generation)
        REFERENCES run_attempts(id, run_id, generation),
    CONSTRAINT checkpoints_catalog_scope_thread_fk
        FOREIGN KEY (
            brain_tool_catalog_id,
            workspace_id,
            session_id,
            thread_id
        )
        REFERENCES brain_tool_catalogs(id, workspace_id, session_id, thread_id),
    CONSTRAINT checkpoints_attempt_generation_positive CHECK (
        attempt_generation > 0
    ),
    CONSTRAINT checkpoints_thread_bounded CHECK (
        pg_catalog.octet_length(thread_id) BETWEEN 1 AND 256
    ),
    CONSTRAINT checkpoints_turn_bounded CHECK (
        pg_catalog.octet_length(turn_id) BETWEEN 1 AND 256
    ),
    CONSTRAINT checkpoints_digests_sha256 CHECK (
        pg_catalog.octet_length(manifest_digest) = 32
        AND pg_catalog.octet_length(object_sha256) = 32
        AND pg_catalog.octet_length(codex_runtime_manifest_digest) = 32
    ),
    CONSTRAINT checkpoints_object_size_bounded CHECK (
        object_size BETWEEN 1 AND 1099511627776
    ),
    CONSTRAINT checkpoints_object_media_type_bounded CHECK (
        pg_catalog.octet_length(object_media_type) BETWEEN 1 AND 255
        AND pg_catalog.strpos(object_media_type, pg_catalog.chr(10)) = 0
        AND pg_catalog.strpos(object_media_type, pg_catalog.chr(13)) = 0
    ),
    CONSTRAINT checkpoints_allowlist_version_bounded CHECK (
        checkpoint_allowlist_version BETWEEN 1 AND 9007199254740991
    )
);

ALTER TABLE sessions
    ADD CONSTRAINT sessions_latest_checkpoint_same_session_fk
    FOREIGN KEY (latest_checkpoint_id, id)
    REFERENCES checkpoints(id, session_id)
    DEFERRABLE INITIALLY IMMEDIATE;

CREATE INDEX checkpoints_session_created_idx
    ON checkpoints (session_id, created_at DESC);
