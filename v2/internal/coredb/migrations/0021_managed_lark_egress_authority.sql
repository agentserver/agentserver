CREATE TABLE workspace_lark_grants (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL,
    user_id uuid NOT NULL REFERENCES users(id),
    pack_id text NOT NULL DEFAULT 'lark-readonly@v1',
    policy_sha256 bytea NOT NULL,
    status text NOT NULL DEFAULT 'active',
    sealed_token_set bytea NOT NULL,
    access_expires_at timestamptz NOT NULL,
    refresh_expires_at timestamptz,
    authority_version bigint NOT NULL DEFAULT 1,
    credential_version bigint NOT NULL DEFAULT 1,
    last_refreshed_at timestamptz,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    CONSTRAINT workspace_lark_grants_membership_fk
        FOREIGN KEY (workspace_id, user_id)
        REFERENCES workspace_members(workspace_id, user_id) ON DELETE CASCADE,
    CONSTRAINT workspace_lark_grants_identity_scope_user_unique
        UNIQUE (id, workspace_id, user_id),
    CONSTRAINT workspace_lark_grants_workspace_pack_user_unique
        UNIQUE (workspace_id, pack_id, user_id),
    CONSTRAINT workspace_lark_grants_pack_valid CHECK (
        pack_id = 'lark-readonly@v1'
    ),
    CONSTRAINT workspace_lark_grants_policy_sha256_exact CHECK (
        pg_catalog.octet_length(policy_sha256) = 32
    ),
    CONSTRAINT workspace_lark_grants_status_valid CHECK (
        status IN ('active', 'reauth_required', 'revoked', 'expired')
    ),
    CONSTRAINT workspace_lark_grants_token_set_bounded CHECK (
        pg_catalog.octet_length(sealed_token_set) BETWEEN 29 AND 262144
    ),
    CONSTRAINT workspace_lark_grants_versions_json_safe CHECK (
        authority_version BETWEEN 1 AND 9007199254740991
        AND credential_version BETWEEN 1 AND 9007199254740991
    ),
    CONSTRAINT workspace_lark_grants_expiry_order CHECK (
        refresh_expires_at IS NULL OR refresh_expires_at > access_expires_at
    ),
    CONSTRAINT workspace_lark_grants_revocation_state CHECK (
        (status = 'revoked' AND revoked_at IS NOT NULL)
        OR (status <> 'revoked' AND revoked_at IS NULL)
    )
);

CREATE INDEX workspace_lark_grants_live_idx
    ON workspace_lark_grants (
        workspace_id, user_id, status, access_expires_at, id
    );

ALTER TABLE run_launch_states
    ADD COLUMN lark_grant_id uuid,
    ADD COLUMN lark_grant_version bigint,
    ADD COLUMN lark_grant_user_id uuid,
    ADD COLUMN lark_policy_sha256 bytea,
    ADD CONSTRAINT run_launch_states_lark_grant_fk
        FOREIGN KEY (lark_grant_id, workspace_id, lark_grant_user_id)
        REFERENCES workspace_lark_grants(id, workspace_id, user_id),
    ADD CONSTRAINT run_launch_states_lark_grant_complete CHECK (
        (
            lark_grant_id IS NULL
            AND lark_grant_version IS NULL
            AND lark_grant_user_id IS NULL
            AND lark_policy_sha256 IS NULL
        )
        OR
        (
            lark_grant_id IS NOT NULL
            AND lark_grant_version BETWEEN 1 AND 9007199254740991
            AND lark_grant_user_id IS NOT NULL
            AND pg_catalog.octet_length(lark_policy_sha256) = 32
        )
    );

CREATE INDEX run_launch_states_lark_grant_idx
    ON run_launch_states (
        lark_grant_id, lark_grant_version, lark_grant_user_id
    )
    WHERE lark_grant_id IS NOT NULL;

CREATE TABLE managed_egress_audit_events (
    id uuid PRIMARY KEY,
    decided_at timestamptz NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    capability_id text,
    workspace_id text,
    session_id text,
    run_id text,
    run_attempt_id text,
    run_attempt_generation bigint,
    execution_id text,
    operation_id text,
    sandbox_id text,
    target_generation bigint,
    grant_id text,
    grant_version bigint,
    tae_psm text,
    request_host text NOT NULL,
    request_path text NOT NULL,
    request_method text NOT NULL,
    decision text NOT NULL,
    reason_code text NOT NULL,
    CONSTRAINT managed_egress_audit_capability_bounded CHECK (
        capability_id IS NULL
        OR pg_catalog.octet_length(capability_id) BETWEEN 1 AND 2048
    ),
    CONSTRAINT managed_egress_audit_scope_bounded CHECK (
        (workspace_id IS NULL OR pg_catalog.octet_length(workspace_id) BETWEEN 1 AND 2048)
        AND (session_id IS NULL OR pg_catalog.octet_length(session_id) BETWEEN 1 AND 2048)
        AND (run_id IS NULL OR pg_catalog.octet_length(run_id) BETWEEN 1 AND 2048)
        AND (run_attempt_id IS NULL OR pg_catalog.octet_length(run_attempt_id) BETWEEN 1 AND 2048)
        AND (execution_id IS NULL OR pg_catalog.octet_length(execution_id) BETWEEN 1 AND 2048)
        AND (operation_id IS NULL OR pg_catalog.octet_length(operation_id) BETWEEN 1 AND 2048)
        AND (sandbox_id IS NULL OR pg_catalog.octet_length(sandbox_id) BETWEEN 1 AND 2048)
        AND (grant_id IS NULL OR pg_catalog.octet_length(grant_id) BETWEEN 1 AND 2048)
    ),
    CONSTRAINT managed_egress_audit_generations_positive CHECK (
        (run_attempt_generation IS NULL OR run_attempt_generation > 0)
        AND (target_generation IS NULL OR target_generation > 0)
        AND (grant_version IS NULL OR grant_version > 0)
    ),
    CONSTRAINT managed_egress_audit_psm_bounded CHECK (
        tae_psm IS NULL
        OR pg_catalog.octet_length(tae_psm) BETWEEN 1 AND 256
    ),
    CONSTRAINT managed_egress_audit_request_bounded CHECK (
        pg_catalog.octet_length(request_host) BETWEEN 0 AND 65536
        AND pg_catalog.octet_length(request_path) BETWEEN 0 AND 65536
        AND pg_catalog.octet_length(request_method) BETWEEN 0 AND 256
    ),
    CONSTRAINT managed_egress_audit_decision_valid CHECK (
        decision IN ('allow', 'deny')
    ),
    CONSTRAINT managed_egress_audit_reason_bounded CHECK (
        pg_catalog.octet_length(reason_code) BETWEEN 1 AND 128
    )
);

CREATE INDEX managed_egress_audit_run_time_idx
    ON managed_egress_audit_events (run_id, decided_at DESC, id)
    WHERE run_id IS NOT NULL;

CREATE INDEX managed_egress_audit_capability_idx
    ON managed_egress_audit_events (capability_id, decided_at DESC, id)
    WHERE capability_id IS NOT NULL;

CREATE INDEX managed_egress_audit_time_idx
    ON managed_egress_audit_events (decided_at DESC, id);

CREATE TABLE managed_egress_audit_outbox (
    audit_event_id uuid PRIMARY KEY
        REFERENCES managed_egress_audit_events(id) ON DELETE CASCADE,
    available_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    lock_owner text,
    lock_until timestamptz,
    attempts integer NOT NULL DEFAULT 0,
    completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    CONSTRAINT managed_egress_audit_outbox_lock_owner_bounded CHECK (
        lock_owner IS NULL OR pg_catalog.octet_length(lock_owner) BETWEEN 1 AND 256
    ),
    CONSTRAINT managed_egress_audit_outbox_lock_pair CHECK (
        (lock_owner IS NULL AND lock_until IS NULL)
        OR (lock_owner IS NOT NULL AND lock_until IS NOT NULL)
    ),
    CONSTRAINT managed_egress_audit_outbox_attempts_nonnegative CHECK (
        attempts >= 0
    ),
    CONSTRAINT managed_egress_audit_outbox_completed_unlocked CHECK (
        completed_at IS NULL OR (lock_owner IS NULL AND lock_until IS NULL)
    )
);

CREATE INDEX managed_egress_audit_outbox_claim_idx
    ON managed_egress_audit_outbox (available_at, audit_event_id)
    WHERE completed_at IS NULL;
