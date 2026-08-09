ALTER TABLE workspace_lark_grants
    ADD COLUMN next_refresh_at timestamptz NOT NULL
        DEFAULT pg_catalog.clock_timestamp(),
    ADD COLUMN refresh_lock_owner text,
    ADD COLUMN refresh_lock_until timestamptz,
    ADD COLUMN refresh_dispatched_at timestamptz,
    ADD COLUMN refresh_attempts integer NOT NULL DEFAULT 0,
    ADD COLUMN last_refresh_error_code text,
    ADD CONSTRAINT workspace_lark_grants_refresh_lock_pair CHECK (
        (refresh_lock_owner IS NULL AND refresh_lock_until IS NULL)
        OR (refresh_lock_owner IS NOT NULL AND refresh_lock_until IS NOT NULL)
    ),
    ADD CONSTRAINT workspace_lark_grants_refresh_dispatch_locked CHECK (
        refresh_dispatched_at IS NULL OR refresh_lock_owner IS NOT NULL
    ),
    ADD CONSTRAINT workspace_lark_grants_refresh_active_lock CHECK (
        status = 'active'
        OR (
            refresh_lock_owner IS NULL
            AND refresh_lock_until IS NULL
            AND refresh_dispatched_at IS NULL
        )
    ),
    ADD CONSTRAINT workspace_lark_grants_refresh_attempts_nonnegative CHECK (
        refresh_attempts >= 0
    ),
    ADD CONSTRAINT workspace_lark_grants_refresh_error_bounded CHECK (
        last_refresh_error_code IS NULL
        OR pg_catalog.octet_length(last_refresh_error_code) BETWEEN 1 AND 128
    );

CREATE INDEX workspace_lark_grants_refresh_claim_idx
    ON workspace_lark_grants (next_refresh_at, id)
    WHERE status = 'active' AND refresh_dispatched_at IS NULL;

CREATE INDEX workspace_lark_grants_refresh_orphan_idx
    ON workspace_lark_grants (refresh_lock_until, id)
    WHERE status = 'active' AND refresh_dispatched_at IS NOT NULL;
