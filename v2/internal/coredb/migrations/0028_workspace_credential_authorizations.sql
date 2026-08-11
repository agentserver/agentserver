ALTER TABLE workspace_credential_bindings
    ADD COLUMN refresh_lease_token uuid,
    ADD COLUMN refresh_lease_expires_at timestamptz,
    ADD CONSTRAINT workspace_credential_bindings_refresh_lease_complete CHECK (
        (refresh_lease_token IS NULL) = (refresh_lease_expires_at IS NULL)
    );

CREATE INDEX workspace_credential_bindings_refresh_due_idx
    ON workspace_credential_bindings(access_expires_at, workspace_id, kind)
    WHERE status = 'active' AND refresh_expires_at IS NOT NULL;

CREATE TABLE workspace_credential_authorizations (
    id                              uuid PRIMARY KEY,
    workspace_id                    uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    kind                            text NOT NULL,
    actor_id                        uuid NOT NULL REFERENCES users(id),
    target_binding_id               uuid NOT NULL,
    target_exists                   boolean NOT NULL,
    expected_authority_version      bigint,
    expected_credential_version     bigint,
    display_name                    text NOT NULL,
    owner_scope                     text NOT NULL,
    owner_user_id                   uuid,
    make_default                    boolean NOT NULL DEFAULT false,
    provider_public                 jsonb NOT NULL DEFAULT '{}'::jsonb,
    user_code                       text NOT NULL DEFAULT '',
    verification_uri                text NOT NULL,
    verification_uri_complete       text NOT NULL,
    sealed_provider_state           bytea,
    sealing_key_id                  text NOT NULL,
    provider_state_version          bigint NOT NULL DEFAULT 1,
    status                          text NOT NULL DEFAULT 'pending',
    poll_interval_seconds           integer NOT NULL,
    next_poll_at                    timestamptz NOT NULL,
    expires_at                      timestamptz NOT NULL,
    poll_lease_token                uuid,
    poll_lease_expires_at           timestamptz,
    binding_id                      uuid,
    last_error_code                 text,
    version                         bigint NOT NULL DEFAULT 1,
    created_at                      timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    updated_at                      timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    completed_at                    timestamptz,
    CONSTRAINT workspace_credential_authorizations_kind_bounded CHECK (
        octet_length(kind) BETWEEN 1 AND 128
    ),
    CONSTRAINT workspace_credential_authorizations_display_name_valid CHECK (
        octet_length(display_name) BETWEEN 1 AND 256
        AND display_name = pg_catalog.btrim(display_name)
        AND display_name !~ '[[:cntrl:]]'
    ),
    CONSTRAINT workspace_credential_authorizations_owner_scope_valid CHECK (
        owner_scope IN ('workspace', 'user')
    ),
    CONSTRAINT workspace_credential_authorizations_owner_identity_valid CHECK (
        (owner_scope = 'workspace' AND owner_user_id IS NULL)
        OR (owner_scope = 'user' AND owner_user_id IS NOT NULL)
    ),
    CONSTRAINT workspace_credential_authorizations_owner_membership_fk
        FOREIGN KEY (workspace_id, owner_user_id)
        REFERENCES workspace_members(workspace_id, user_id)
        ON DELETE CASCADE,
    CONSTRAINT workspace_credential_authorizations_target_version_valid CHECK (
        (
            target_exists
            AND expected_authority_version > 0
            AND expected_credential_version > 0
        ) OR (
            NOT target_exists
            AND expected_authority_version IS NULL
            AND expected_credential_version IS NULL
        )
    ),
    CONSTRAINT workspace_credential_authorizations_provider_public_object CHECK (
        pg_catalog.jsonb_typeof(provider_public) = 'object'
        AND pg_catalog.octet_length(provider_public::text) <= 65536
    ),
    CONSTRAINT workspace_credential_authorizations_public_values_bounded CHECK (
        octet_length(user_code) <= 1024
        AND octet_length(verification_uri) BETWEEN 8 AND 8192
        AND octet_length(verification_uri_complete) BETWEEN 8 AND 8192
        AND octet_length(sealing_key_id) BETWEEN 1 AND 128
    ),
    CONSTRAINT workspace_credential_authorizations_state_bounded CHECK (
        sealed_provider_state IS NULL
        OR octet_length(sealed_provider_state) BETWEEN 1 AND 524288
    ),
    CONSTRAINT workspace_credential_authorizations_state_version_positive CHECK (
        provider_state_version > 0
    ),
    CONSTRAINT workspace_credential_authorizations_status_valid CHECK (
        status IN ('pending', 'succeeded', 'denied', 'expired', 'cancelled', 'failed')
    ),
    CONSTRAINT workspace_credential_authorizations_interval_valid CHECK (
        poll_interval_seconds BETWEEN 1 AND 60
    ),
    CONSTRAINT workspace_credential_authorizations_expiry_valid CHECK (
        expires_at > created_at AND next_poll_at <= expires_at
    ),
    CONSTRAINT workspace_credential_authorizations_poll_lease_complete CHECK (
        (poll_lease_token IS NULL) = (poll_lease_expires_at IS NULL)
    ),
    CONSTRAINT workspace_credential_authorizations_pending_authority CHECK (
        (
            status = 'pending'
            AND sealed_provider_state IS NOT NULL
            AND completed_at IS NULL
            AND binding_id IS NULL
        ) OR (
            status <> 'pending'
            AND sealed_provider_state IS NULL
            AND poll_lease_token IS NULL
            AND poll_lease_expires_at IS NULL
            AND completed_at IS NOT NULL
        )
    ),
    CONSTRAINT workspace_credential_authorizations_success_binding CHECK (
        (status = 'succeeded' AND binding_id = target_binding_id)
        OR (status <> 'succeeded' AND binding_id IS NULL)
    ),
    CONSTRAINT workspace_credential_authorizations_error_bounded CHECK (
        last_error_code IS NULL OR octet_length(last_error_code) BETWEEN 1 AND 128
    ),
    CONSTRAINT workspace_credential_authorizations_version_positive CHECK (version > 0)
);

CREATE UNIQUE INDEX workspace_credential_authorizations_one_pending_target_idx
    ON workspace_credential_authorizations(workspace_id, kind, target_binding_id)
    WHERE status = 'pending';

CREATE INDEX workspace_credential_authorizations_workspace_created_idx
    ON workspace_credential_authorizations(workspace_id, kind, created_at DESC, id);

CREATE INDEX workspace_credential_authorizations_pending_expiry_idx
    ON workspace_credential_authorizations(expires_at, next_poll_at)
    WHERE status = 'pending';
