ALTER TABLE executors
    ADD COLUMN machine_public_key_ed25519 bytea,
    ADD COLUMN oauth_public_key_p256_x bytea,
    ADD COLUMN oauth_public_key_p256_y bytea,
    ADD COLUMN oauth_key_sha256 bytea,
    ADD COLUMN oauth_client_id text,
    ADD COLUMN enrollment_request_sha256 bytea,
    ADD CONSTRAINT executors_production_machine_identity_complete CHECK (
        (
            machine_public_key_ed25519 IS NULL
            AND oauth_public_key_p256_x IS NULL
            AND oauth_public_key_p256_y IS NULL
            AND oauth_key_sha256 IS NULL
            AND oauth_client_id IS NULL
            AND enrollment_request_sha256 IS NULL
        )
        OR
        (
            machine_public_key_ed25519 IS NOT NULL
            AND oauth_public_key_p256_x IS NOT NULL
            AND oauth_public_key_p256_y IS NOT NULL
            AND oauth_key_sha256 IS NOT NULL
            AND oauth_client_id IS NOT NULL
            AND enrollment_request_sha256 IS NOT NULL
            AND machine_key_sha256 IS NOT NULL
            AND pg_catalog.octet_length(machine_public_key_ed25519) = 32
            AND pg_catalog.octet_length(machine_key_sha256) = 32
            AND pg_catalog.octet_length(oauth_public_key_p256_x) = 32
            AND pg_catalog.octet_length(oauth_public_key_p256_y) = 32
            AND pg_catalog.octet_length(oauth_key_sha256) = 32
            AND pg_catalog.octet_length(enrollment_request_sha256) = 32
            AND pg_catalog.octet_length(oauth_client_id) BETWEEN 1 AND 128
            AND oauth_client_id = 'agentserver-executor-' || id::text
        )
    );

CREATE UNIQUE INDEX executors_oauth_client_id_unique
    ON executors (oauth_client_id)
    WHERE oauth_client_id IS NOT NULL;

CREATE TABLE executor_enrollment_tokens (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL,
    executor_id uuid NOT NULL,
    issued_by uuid NOT NULL REFERENCES users(id),
    idempotency_key text NOT NULL,
    issued_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    expires_at timestamptz NOT NULL,
    claimed_at timestamptz,
    consumed_at timestamptz,
    revoked_at timestamptz,
    enrollment_request_sha256 bytea,
    version bigint NOT NULL DEFAULT 1,
    CONSTRAINT executor_enrollment_tokens_executor_scope_fk
        FOREIGN KEY (executor_id, workspace_id)
        REFERENCES executors(id, workspace_id)
        ON DELETE CASCADE,
    CONSTRAINT executor_enrollment_tokens_request_unique
        UNIQUE (executor_id, issued_by, idempotency_key),
    CONSTRAINT executor_enrollment_tokens_idempotency_bounded CHECK (
        pg_catalog.octet_length(idempotency_key) BETWEEN 1 AND 256
    ),
    CONSTRAINT executor_enrollment_tokens_expiry_order CHECK (
        expires_at > issued_at
        AND expires_at <= issued_at + INTERVAL '15 minutes'
    ),
    CONSTRAINT executor_enrollment_tokens_request_hash_exact CHECK (
        enrollment_request_sha256 IS NULL
        OR pg_catalog.octet_length(enrollment_request_sha256) = 32
    ),
    CONSTRAINT executor_enrollment_tokens_lifecycle CHECK (
        (
            claimed_at IS NULL
            AND consumed_at IS NULL
            AND enrollment_request_sha256 IS NULL
        )
        OR
        (
            claimed_at IS NOT NULL
            AND enrollment_request_sha256 IS NOT NULL
            AND claimed_at >= issued_at
            AND claimed_at < expires_at
            AND (consumed_at IS NULL OR consumed_at >= claimed_at)
        )
    ),
    CONSTRAINT executor_enrollment_tokens_terminal_exclusive CHECK (
        consumed_at IS NULL OR revoked_at IS NULL
    ),
    CONSTRAINT executor_enrollment_tokens_revocation_order CHECK (
        revoked_at IS NULL
        OR (
            revoked_at >= issued_at
            AND (claimed_at IS NULL OR revoked_at >= claimed_at)
        )
    ),
    CONSTRAINT executor_enrollment_tokens_version_positive CHECK (version > 0)
);

CREATE UNIQUE INDEX executor_enrollment_tokens_one_live_per_executor
    ON executor_enrollment_tokens (executor_id)
    WHERE consumed_at IS NULL AND revoked_at IS NULL;

CREATE INDEX executor_enrollment_tokens_expiry_idx
    ON executor_enrollment_tokens (expires_at, executor_id)
    WHERE consumed_at IS NULL AND revoked_at IS NULL;
