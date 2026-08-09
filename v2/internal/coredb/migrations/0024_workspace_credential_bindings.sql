CREATE TABLE workspace_credential_bindings (
    id                  uuid PRIMARY KEY,
    workspace_id        uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    kind                text NOT NULL,
    display_name        text NOT NULL,
    owner_scope         text NOT NULL,
    owner_user_id       uuid,
    public_metadata     jsonb NOT NULL DEFAULT '{}'::jsonb,
    auth_type           text NOT NULL,
    sealed_secret       bytea NOT NULL,
    sealing_key_id      text NOT NULL,
    authority_version   bigint NOT NULL DEFAULT 1,
    credential_version  bigint NOT NULL DEFAULT 1,
    status              text NOT NULL DEFAULT 'active',
    is_default          boolean NOT NULL DEFAULT false,
    access_expires_at   timestamptz,
    refresh_expires_at  timestamptz,
    last_error_code     text,
    created_at          timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    updated_at          timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    CONSTRAINT workspace_credential_bindings_identity_unique
        UNIQUE (workspace_id, kind, display_name),
    CONSTRAINT workspace_credential_bindings_owner_scope_valid
        CHECK (owner_scope IN ('workspace', 'user')),
    CONSTRAINT workspace_credential_bindings_owner_identity_valid
        CHECK (
            (owner_scope = 'workspace' AND owner_user_id IS NULL)
            OR
            (owner_scope = 'user' AND owner_user_id IS NOT NULL)
        ),
    CONSTRAINT workspace_credential_bindings_owner_membership_fk
        FOREIGN KEY (workspace_id, owner_user_id)
        REFERENCES workspace_members(workspace_id, user_id)
        ON DELETE CASCADE,
    CONSTRAINT workspace_credential_bindings_kind_bounded
        CHECK (octet_length(kind) BETWEEN 1 AND 128),
    CONSTRAINT workspace_credential_bindings_display_name_bounded
        CHECK (octet_length(display_name) BETWEEN 1 AND 256),
    CONSTRAINT workspace_credential_bindings_display_name_canonical
        CHECK (display_name = pg_catalog.btrim(display_name) AND display_name !~ '[[:cntrl:]]'),
    CONSTRAINT workspace_credential_bindings_auth_type_bounded
        CHECK (octet_length(auth_type) BETWEEN 1 AND 128),
    CONSTRAINT workspace_credential_bindings_sealing_key_bounded
        CHECK (octet_length(sealing_key_id) BETWEEN 1 AND 128),
    CONSTRAINT workspace_credential_bindings_public_metadata_object
        CHECK (pg_catalog.jsonb_typeof(public_metadata) = 'object'
            AND pg_catalog.octet_length(public_metadata::text) <= 65536),
    CONSTRAINT workspace_credential_bindings_sealed_secret_bounded
        CHECK (octet_length(sealed_secret) BETWEEN 1 AND 524288),
    CONSTRAINT workspace_credential_bindings_authority_version_positive
        CHECK (authority_version > 0),
    CONSTRAINT workspace_credential_bindings_credential_version_positive
        CHECK (credential_version > 0),
    CONSTRAINT workspace_credential_bindings_status_valid
        CHECK (status IN ('active', 'reauth_required', 'revoked', 'disabled')),
    CONSTRAINT workspace_credential_bindings_expiry_order
        CHECK (refresh_expires_at IS NULL OR access_expires_at IS NULL OR access_expires_at <= refresh_expires_at),
    CONSTRAINT workspace_credential_bindings_last_error_bounded
        CHECK (last_error_code IS NULL OR octet_length(last_error_code) BETWEEN 1 AND 128)
);

CREATE INDEX workspace_credential_bindings_workspace_kind_idx
    ON workspace_credential_bindings(workspace_id, kind, status, updated_at DESC);

CREATE UNIQUE INDEX workspace_credential_bindings_one_default_idx
    ON workspace_credential_bindings(workspace_id, kind)
    WHERE is_default AND status = 'active';

CREATE TABLE workspace_credential_use_events (
    event_id              uuid PRIMARY KEY,
    workspace_id          uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    session_id            uuid,
    run_id                uuid,
    run_attempt_id        uuid,
    run_attempt_generation bigint,
    execution_id          uuid,
    operation_id          uuid,
    sandbox_id            uuid,
    target_generation     bigint,
    provider_kind         text NOT NULL,
    binding_id            uuid,
    authority_version     bigint,
    credential_version    bigint,
    request_host          text NOT NULL,
    request_path          text NOT NULL,
    request_method        text NOT NULL,
    decision              text NOT NULL,
    reason_code           text NOT NULL,
    created_at            timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    CONSTRAINT workspace_credential_use_events_provider_bounded
        CHECK (octet_length(provider_kind) BETWEEN 1 AND 128),
    CONSTRAINT workspace_credential_use_events_target_bounded
        CHECK (octet_length(request_host) BETWEEN 1 AND 512
            AND octet_length(request_path) BETWEEN 1 AND 4096
            AND octet_length(request_method) BETWEEN 1 AND 16),
    CONSTRAINT workspace_credential_use_events_decision_valid
        CHECK (decision IN ('allow', 'deny')),
    CONSTRAINT workspace_credential_use_events_reason_bounded
        CHECK (octet_length(reason_code) BETWEEN 1 AND 128),
    CONSTRAINT workspace_credential_use_events_versions_valid
        CHECK (
            (authority_version IS NULL OR authority_version > 0)
            AND (credential_version IS NULL OR credential_version > 0)
            AND (target_generation IS NULL OR target_generation > 0)
            AND (run_attempt_generation IS NULL OR run_attempt_generation > 0)
        )
);

CREATE INDEX workspace_credential_use_events_workspace_created_idx
    ON workspace_credential_use_events(workspace_id, created_at DESC);
