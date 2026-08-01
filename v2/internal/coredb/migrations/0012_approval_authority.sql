ALTER TABLE executions
    ADD CONSTRAINT executions_identity_attempt_scope_unique
    UNIQUE (id, run_id, run_attempt_id, run_attempt_generation);

CREATE TABLE approvals (
    id uuid PRIMARY KEY,
    execution_id uuid NOT NULL,
    run_id uuid NOT NULL,
    run_attempt_id uuid NOT NULL,
    run_attempt_generation bigint NOT NULL,
    nonce uuid NOT NULL,
    requester_id text NOT NULL,
    approver_id uuid,
    decision text,
    canonicalizer_version text NOT NULL,
    context_hash bytea NOT NULL,
    status text NOT NULL,
    expires_at timestamptz NOT NULL,
    decided_at timestamptz,
    consumed_at timestamptz,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    CONSTRAINT approvals_execution_unique UNIQUE (execution_id),
    CONSTRAINT approvals_nonce_unique UNIQUE (nonce),
    CONSTRAINT approvals_execution_scope_fk
        FOREIGN KEY (
            execution_id, run_id, run_attempt_id, run_attempt_generation
        )
        REFERENCES executions (
            id, run_id, run_attempt_id, run_attempt_generation
        )
        ON DELETE CASCADE,
    CONSTRAINT approvals_attempt_generation_positive CHECK (
        run_attempt_generation > 0
    ),
    CONSTRAINT approvals_requester_bounded CHECK (
        pg_catalog.octet_length(requester_id) BETWEEN 1 AND 256
    ),
    CONSTRAINT approvals_canonicalizer_version_valid CHECK (
        canonicalizer_version = 'rfc8785-v1'
    ),
    CONSTRAINT approvals_context_hash_sha256 CHECK (
        pg_catalog.octet_length(context_hash) = 32
    ),
    CONSTRAINT approvals_status_valid CHECK (
        status IN (
            'pending', 'approved', 'denied', 'expired', 'cancelled',
            'consumed'
        )
    ),
    CONSTRAINT approvals_decision_valid CHECK (
        decision IS NULL OR decision IN ('approve', 'deny')
    ),
    CONSTRAINT approvals_expiry_after_creation CHECK (
        expires_at > created_at
    ),
    CONSTRAINT approvals_decision_evidence_matches_status CHECK (
        (
            status = 'pending'
            AND approver_id IS NULL
            AND decision IS NULL
            AND decided_at IS NULL
            AND consumed_at IS NULL
        )
        OR
        (
            status = 'approved'
            AND approver_id IS NOT NULL
            AND decision = 'approve'
            AND decided_at IS NOT NULL
            AND consumed_at IS NULL
        )
        OR
        (
            status = 'denied'
            AND approver_id IS NOT NULL
            AND decision = 'deny'
            AND decided_at IS NOT NULL
            AND consumed_at IS NULL
        )
        OR
        (
            status IN ('expired', 'cancelled')
            AND (
                (approver_id IS NULL AND decision IS NULL)
                OR
                (approver_id IS NOT NULL AND decision = 'approve')
            )
            AND decided_at IS NOT NULL
            AND consumed_at IS NULL
        )
        OR
        (
            status = 'consumed'
            AND approver_id IS NOT NULL
            AND decision = 'approve'
            AND decided_at IS NOT NULL
            AND consumed_at IS NOT NULL
            AND consumed_at >= decided_at
        )
    ),
    CONSTRAINT approvals_version_positive CHECK (version > 0)
);

CREATE INDEX approvals_run_status_expiry_idx
    ON approvals (run_id, status, expires_at);
