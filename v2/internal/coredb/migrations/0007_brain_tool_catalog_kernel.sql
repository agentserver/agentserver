CREATE TABLE brain_tool_catalogs (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id),
    session_id uuid NOT NULL,
    created_run_id uuid NOT NULL,
    created_run_attempt_id uuid NOT NULL,
    created_attempt_generation bigint NOT NULL,
    created_holder_id text NOT NULL,
    created_run_version bigint NOT NULL,
    created_attempt_version bigint NOT NULL,
    thread_id text,
    contract_version text NOT NULL,
    canonicalizer_version text NOT NULL,
    canonical_catalog bytea NOT NULL,
    catalog_digest bytea NOT NULL,
    policy_version text NOT NULL,
    policy_context_digest bytea NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    CONSTRAINT brain_tool_catalogs_run_scope_fk
        FOREIGN KEY (created_run_id, workspace_id, session_id)
        REFERENCES runs(id, workspace_id, session_id),
    CONSTRAINT brain_tool_catalogs_attempt_scope_fk
        FOREIGN KEY (
            created_run_attempt_id,
            created_run_id,
            created_attempt_generation
        )
        REFERENCES run_attempts(id, run_id, generation),
    CONSTRAINT brain_tool_catalogs_attempt_unique
        UNIQUE (created_run_attempt_id),
    CONSTRAINT brain_tool_catalogs_thread_unique
        UNIQUE (thread_id),
    CONSTRAINT brain_tool_catalogs_generation_positive CHECK (
        created_attempt_generation > 0
    ),
    CONSTRAINT brain_tool_catalogs_created_versions_positive CHECK (
        created_run_version > 0 AND created_attempt_version > 0
    ),
    CONSTRAINT brain_tool_catalogs_holder_bounded CHECK (
        pg_catalog.octet_length(created_holder_id) BETWEEN 1 AND 256
    ),
    CONSTRAINT brain_tool_catalogs_thread_bounded CHECK (
        thread_id IS NULL
        OR pg_catalog.octet_length(thread_id) BETWEEN 1 AND 256
    ),
    CONSTRAINT brain_tool_catalogs_contract_version_bounded CHECK (
        pg_catalog.octet_length(contract_version) BETWEEN 1 AND 128
    ),
    CONSTRAINT brain_tool_catalogs_canonicalizer_version_valid CHECK (
        canonicalizer_version = 'rfc8785-v1'
    ),
    CONSTRAINT brain_tool_catalogs_catalog_bounded CHECK (
        pg_catalog.octet_length(canonical_catalog) BETWEEN 1 AND 1048576
    ),
    CONSTRAINT brain_tool_catalogs_digests_sha256 CHECK (
        pg_catalog.octet_length(catalog_digest) = 32
        AND pg_catalog.octet_length(policy_context_digest) = 32
    ),
    CONSTRAINT brain_tool_catalogs_policy_version_bounded CHECK (
        pg_catalog.octet_length(policy_version) BETWEEN 1 AND 128
    ),
    CONSTRAINT brain_tool_catalogs_version_positive CHECK (version > 0)
);

CREATE INDEX brain_tool_catalogs_session_created_idx
    ON brain_tool_catalogs (session_id, created_at DESC);
