-- A Policy Webhook denial can happen before a signed placeholder has been
-- parsed. Keep those fail-closed decisions auditable without inventing a
-- workspace or operation identity, while requiring a complete scope for every
-- allow decision. This table never stores request headers or credential bytes.
ALTER TABLE workspace_credential_use_events
    ALTER COLUMN workspace_id DROP NOT NULL,
    ALTER COLUMN provider_kind DROP NOT NULL,
    ALTER COLUMN request_host DROP NOT NULL,
    ALTER COLUMN request_path DROP NOT NULL,
    ALTER COLUMN request_method DROP NOT NULL,
    ADD COLUMN stage text NOT NULL DEFAULT 'materialize',
    ADD COLUMN capability_id text,
    ADD COLUMN actor_id uuid,
    ADD COLUMN environment_id uuid,
    ADD COLUMN tae_psm text,
    ADD CONSTRAINT workspace_credential_use_events_stage_valid
        CHECK (stage IN ('materialize', 'egress')),
    ADD CONSTRAINT workspace_credential_use_events_capability_bounded
        CHECK (capability_id IS NULL OR octet_length(capability_id) BETWEEN 1 AND 256),
    ADD CONSTRAINT workspace_credential_use_events_tae_psm_bounded
        CHECK (tae_psm IS NULL OR octet_length(tae_psm) BETWEEN 1 AND 256),
    ADD CONSTRAINT workspace_credential_use_events_attempt_scope_complete
        CHECK ((run_attempt_id IS NULL) = (run_attempt_generation IS NULL)),
    ADD CONSTRAINT workspace_credential_use_events_sandbox_scope_complete
        CHECK ((sandbox_id IS NULL) = (target_generation IS NULL)),
    ADD CONSTRAINT workspace_credential_use_events_binding_scope_complete
        CHECK ((binding_id IS NULL) = (authority_version IS NULL)),
    ADD CONSTRAINT workspace_credential_use_events_allow_scope_complete
        CHECK (
            decision <> 'allow'
            OR (
                capability_id IS NOT NULL
                AND workspace_id IS NOT NULL
                AND session_id IS NOT NULL
                AND actor_id IS NOT NULL
                AND environment_id IS NOT NULL
                AND run_id IS NOT NULL
                AND run_attempt_id IS NOT NULL
                AND run_attempt_generation IS NOT NULL
                AND execution_id IS NOT NULL
                AND operation_id IS NOT NULL
                AND sandbox_id IS NOT NULL
                AND target_generation IS NOT NULL
                AND provider_kind IS NOT NULL
                AND binding_id IS NOT NULL
                AND authority_version IS NOT NULL
                AND credential_version IS NOT NULL
                AND tae_psm IS NOT NULL
                AND request_host IS NOT NULL
                AND request_path IS NOT NULL
                AND request_method IS NOT NULL
            )
        );

CREATE INDEX workspace_credential_use_events_capability_created_idx
    ON workspace_credential_use_events(capability_id, created_at DESC)
    WHERE capability_id IS NOT NULL;
