CREATE TABLE executors (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id),
    status text NOT NULL,
    machine_key_sha256 bytea,
    agentx_version text,
    runtime_manifest_sha256 bytea,
    exec_protocol_source_sha256 bytea,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT executors_identity_workspace_unique UNIQUE (id, workspace_id),
    CONSTRAINT executors_status_valid CHECK (
        status IN ('enrolling', 'offline', 'online', 'revoked')
    ),
    CONSTRAINT executors_enrollment_metadata_complete CHECK (
        (
            machine_key_sha256 IS NULL
            AND agentx_version IS NULL
            AND runtime_manifest_sha256 IS NULL
            AND exec_protocol_source_sha256 IS NULL
        )
        OR
        (
            machine_key_sha256 IS NOT NULL
            AND agentx_version IS NOT NULL
            AND runtime_manifest_sha256 IS NOT NULL
            AND exec_protocol_source_sha256 IS NOT NULL
            AND pg_catalog.octet_length(machine_key_sha256) = 32
            AND pg_catalog.octet_length(agentx_version) BETWEEN 1 AND 256
            AND pg_catalog.octet_length(runtime_manifest_sha256) = 32
            AND pg_catalog.octet_length(exec_protocol_source_sha256) = 32
        )
    ),
    CONSTRAINT executors_connected_metadata_present CHECK (
        status NOT IN ('offline', 'online')
        OR machine_key_sha256 IS NOT NULL
    ),
    CONSTRAINT executors_version_positive CHECK (version > 0)
);

CREATE TABLE executor_environments (
    id uuid PRIMARY KEY,
    executor_id uuid NOT NULL REFERENCES executors(id) ON DELETE CASCADE,
    root_descriptor jsonb NOT NULL,
    owner_policy_sha256 bytea NOT NULL,
    platform text NOT NULL,
    codex_release text NOT NULL,
    codex_commit text NOT NULL,
    codex_sha256 bytea NOT NULL,
    outer_profile_version text NOT NULL,
    process_methods text[] NOT NULL,
    insecure_dev boolean NOT NULL,
    status text NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT executor_environments_identity_executor_unique
        UNIQUE (id, executor_id),
    CONSTRAINT executor_environments_status_valid CHECK (
        status IN ('offline', 'online', 'disabled')
    ),
    CONSTRAINT executor_environments_root_descriptor_object CHECK (
        pg_catalog.jsonb_typeof(root_descriptor) = 'object'
        AND pg_catalog.octet_length(root_descriptor::text) BETWEEN 2 AND 65536
    ),
    CONSTRAINT executor_environments_owner_policy_sha256_exact CHECK (
        pg_catalog.octet_length(owner_policy_sha256) = 32
    ),
    CONSTRAINT executor_environments_platform_valid CHECK (
        platform IN (
            'linux-amd64', 'linux-arm64',
            'darwin-amd64', 'darwin-arm64',
            'windows-amd64', 'windows-arm64'
        )
    ),
    CONSTRAINT executor_environments_codex_release_bounded CHECK (
        pg_catalog.octet_length(codex_release) BETWEEN 1 AND 128
    ),
    CONSTRAINT executor_environments_codex_commit_valid CHECK (
        codex_commit ~ '^[0-9a-f]{40}$'
    ),
    CONSTRAINT executor_environments_codex_sha256_exact CHECK (
        pg_catalog.octet_length(codex_sha256) = 32
    ),
    CONSTRAINT executor_environments_process_profile_valid CHECK (
        outer_profile_version = 'process-v1'
        AND process_methods = ARRAY[
            'process/start', 'process/read', 'process/write', 'process/terminate'
        ]::text[]
    ),
    CONSTRAINT executor_environments_version_positive CHECK (version > 0)
);

CREATE TABLE executor_connection_attempts (
    connection_id uuid PRIMARY KEY,
    executor_id uuid NOT NULL REFERENCES executors(id) ON DELETE CASCADE,
    generation bigint NOT NULL,
    session_id uuid NOT NULL,
    gateway_instance_id uuid NOT NULL,
    agentx_version text NOT NULL,
    runtime_manifest_sha256 bytea NOT NULL,
    exec_protocol_source_sha256 bytea NOT NULL,
    environment_set_sha256 bytea NOT NULL,
    ended_at timestamptz,
    end_reason text,
    acquired_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT executor_connection_attempts_executor_generation_unique
        UNIQUE (executor_id, generation),
    CONSTRAINT executor_connection_attempts_session_id_unique UNIQUE (session_id),
    CONSTRAINT executor_connection_attempts_identity_generation_unique
        UNIQUE (connection_id, executor_id, generation),
    CONSTRAINT executor_connection_attempts_generation_positive CHECK (
        generation > 0
    ),
    CONSTRAINT executor_connection_attempts_agentx_version_bounded CHECK (
        pg_catalog.octet_length(agentx_version) BETWEEN 1 AND 256
    ),
    CONSTRAINT executor_connection_attempts_hashes_sha256 CHECK (
        pg_catalog.octet_length(runtime_manifest_sha256) = 32
        AND pg_catalog.octet_length(exec_protocol_source_sha256) = 32
        AND pg_catalog.octet_length(environment_set_sha256) = 32
    ),
    CONSTRAINT executor_connection_attempts_end_pair CHECK (
        (ended_at IS NULL AND end_reason IS NULL)
        OR
        (
            ended_at IS NOT NULL
            AND end_reason IN ('replaced', 'fenced', 'expired', 'revoked')
        )
    )
);

CREATE TABLE executor_connections (
    executor_id uuid PRIMARY KEY REFERENCES executors(id) ON DELETE CASCADE,
    generation bigint NOT NULL,
    connection_id uuid NOT NULL,
    session_id uuid NOT NULL,
    gateway_instance_id uuid NOT NULL,
    agentx_version text NOT NULL,
    runtime_manifest_sha256 bytea NOT NULL,
    exec_protocol_source_sha256 bytea NOT NULL,
    environment_set_sha256 bytea NOT NULL,
    status text NOT NULL,
    expires_at timestamptz NOT NULL,
    acquired_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    renewed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    version bigint NOT NULL DEFAULT 1,
    CONSTRAINT executor_connections_connection_id_unique UNIQUE (connection_id),
    CONSTRAINT executor_connections_session_id_unique UNIQUE (session_id),
    CONSTRAINT executor_connections_attempt_fk
        FOREIGN KEY (connection_id, executor_id, generation)
        REFERENCES executor_connection_attempts(
            connection_id, executor_id, generation
        ),
    CONSTRAINT executor_connections_generation_positive CHECK (generation > 0),
    CONSTRAINT executor_connections_status_valid CHECK (
        status IN ('connecting', 'online', 'fenced')
    ),
    CONSTRAINT executor_connections_agentx_version_bounded CHECK (
        pg_catalog.octet_length(agentx_version) BETWEEN 1 AND 256
    ),
    CONSTRAINT executor_connections_build_hashes_sha256 CHECK (
        pg_catalog.octet_length(runtime_manifest_sha256) = 32
        AND pg_catalog.octet_length(exec_protocol_source_sha256) = 32
        AND pg_catalog.octet_length(environment_set_sha256) = 32
    ),
    CONSTRAINT executor_connections_version_positive CHECK (version > 0)
);

CREATE INDEX executors_workspace_status_created_idx
    ON executors (workspace_id, status, created_at);

CREATE INDEX executor_environments_executor_status_created_idx
    ON executor_environments (executor_id, status, created_at);

CREATE INDEX executor_connections_expiry_idx
    ON executor_connections (expires_at, executor_id);

CREATE INDEX executor_connection_attempts_executor_acquired_idx
    ON executor_connection_attempts (executor_id, acquired_at DESC);
