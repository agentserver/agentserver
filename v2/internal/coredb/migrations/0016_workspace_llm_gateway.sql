CREATE TABLE workspace_llm_gateways (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name text NOT NULL,
    responses_url text NOT NULL,
    oidc_issuer text NOT NULL,
    oidc_client_id text NOT NULL,
    oidc_scopes text NOT NULL,
    bearer_token_type text NOT NULL,
    default_model text NOT NULL,
    status text NOT NULL DEFAULT 'active',
    is_default boolean NOT NULL DEFAULT FALSE,
    version bigint NOT NULL DEFAULT 1,
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    CONSTRAINT workspace_llm_gateways_identity_scope_unique
        UNIQUE (id, workspace_id),
    CONSTRAINT workspace_llm_gateways_name_unique
        UNIQUE (workspace_id, name),
    CONSTRAINT workspace_llm_gateways_name_bounded CHECK (
        pg_catalog.octet_length(name) BETWEEN 1 AND 128
        AND name = pg_catalog.btrim(name)
        AND pg_catalog.strpos(name, pg_catalog.chr(10)) = 0
        AND pg_catalog.strpos(name, pg_catalog.chr(13)) = 0
    ),
    CONSTRAINT workspace_llm_gateways_responses_url_bounded CHECK (
        pg_catalog.octet_length(responses_url) BETWEEN 20 AND 4096
        AND responses_url = pg_catalog.btrim(responses_url)
        AND pg_catalog.strpos(responses_url, pg_catalog.chr(10)) = 0
        AND pg_catalog.strpos(responses_url, pg_catalog.chr(13)) = 0
    ),
    CONSTRAINT workspace_llm_gateways_oidc_issuer_bounded CHECK (
        pg_catalog.octet_length(oidc_issuer) BETWEEN 8 AND 2048
        AND oidc_issuer = pg_catalog.btrim(oidc_issuer)
        AND pg_catalog.strpos(oidc_issuer, pg_catalog.chr(10)) = 0
        AND pg_catalog.strpos(oidc_issuer, pg_catalog.chr(13)) = 0
    ),
    CONSTRAINT workspace_llm_gateways_oidc_client_bounded CHECK (
        pg_catalog.octet_length(oidc_client_id) BETWEEN 1 AND 512
        AND oidc_client_id = pg_catalog.btrim(oidc_client_id)
        AND pg_catalog.strpos(oidc_client_id, pg_catalog.chr(10)) = 0
        AND pg_catalog.strpos(oidc_client_id, pg_catalog.chr(13)) = 0
    ),
    CONSTRAINT workspace_llm_gateways_oidc_scopes_bounded CHECK (
        pg_catalog.octet_length(oidc_scopes) BETWEEN 6 AND 2048
        AND oidc_scopes = pg_catalog.btrim(oidc_scopes)
        AND pg_catalog.strpos(oidc_scopes, pg_catalog.chr(9)) = 0
        AND pg_catalog.strpos(oidc_scopes, pg_catalog.chr(10)) = 0
        AND pg_catalog.strpos(oidc_scopes, pg_catalog.chr(13)) = 0
    ),
    CONSTRAINT workspace_llm_gateways_bearer_type_valid CHECK (
        bearer_token_type IN ('id_token', 'access_token')
    ),
    CONSTRAINT workspace_llm_gateways_model_bounded CHECK (
        pg_catalog.octet_length(default_model) BETWEEN 1 AND 256
        AND default_model = pg_catalog.btrim(default_model)
        AND pg_catalog.strpos(default_model, pg_catalog.chr(10)) = 0
        AND pg_catalog.strpos(default_model, pg_catalog.chr(13)) = 0
    ),
    CONSTRAINT workspace_llm_gateways_status_valid CHECK (
        status IN ('active', 'disabled')
    ),
    CONSTRAINT workspace_llm_gateways_version_json_safe CHECK (
        version BETWEEN 1 AND 9007199254740991
    )
);

CREATE UNIQUE INDEX workspace_llm_gateways_one_active_default_idx
    ON workspace_llm_gateways (workspace_id)
    WHERE status = 'active' AND is_default = TRUE;

CREATE INDEX workspace_llm_gateways_workspace_status_idx
    ON workspace_llm_gateways (workspace_id, status, name, id);

CREATE TABLE workspace_llm_gateway_grants (
    id uuid PRIMARY KEY,
    gateway_id uuid NOT NULL,
    workspace_id uuid NOT NULL,
    user_id uuid NOT NULL REFERENCES users(id),
    oidc_issuer text NOT NULL,
    oidc_subject text NOT NULL,
    status text NOT NULL,
    sealed_token_set bytea NOT NULL,
    bearer_expires_at timestamptz NOT NULL,
    last_refreshed_at timestamptz,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    CONSTRAINT workspace_llm_gateway_grants_gateway_scope_fk
        FOREIGN KEY (gateway_id, workspace_id)
        REFERENCES workspace_llm_gateways(id, workspace_id),
    CONSTRAINT workspace_llm_gateway_grants_gateway_user_unique
        UNIQUE (gateway_id, user_id),
    CONSTRAINT workspace_llm_gateway_grants_gateway_scope_user_unique
        UNIQUE (gateway_id, workspace_id, user_id),
    CONSTRAINT workspace_llm_gateway_grants_issuer_bounded CHECK (
        pg_catalog.octet_length(oidc_issuer) BETWEEN 8 AND 2048
    ),
    CONSTRAINT workspace_llm_gateway_grants_subject_bounded CHECK (
        pg_catalog.octet_length(oidc_subject) BETWEEN 1 AND 2048
    ),
    CONSTRAINT workspace_llm_gateway_grants_status_valid CHECK (
        status IN ('active', 'reauth_required', 'revoked')
    ),
    CONSTRAINT workspace_llm_gateway_grants_token_set_bounded CHECK (
        pg_catalog.octet_length(sealed_token_set) BETWEEN 29 AND 262144
    ),
    CONSTRAINT workspace_llm_gateway_grants_version_json_safe CHECK (
        version BETWEEN 1 AND 9007199254740991
    )
);

CREATE INDEX workspace_llm_gateway_grants_user_status_idx
    ON workspace_llm_gateway_grants (workspace_id, user_id, status, gateway_id);

CREATE TABLE workspace_llm_gateway_auth_transactions (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL,
    gateway_id uuid NOT NULL,
    gateway_version bigint NOT NULL,
    user_id uuid NOT NULL REFERENCES users(id),
    oidc_state_sha256 bytea NOT NULL UNIQUE,
    browser_binding_sha256 bytea NOT NULL,
    sealed_secrets bytea NOT NULL,
    status text NOT NULL,
    failure_code text,
    expires_at timestamptz NOT NULL,
    callback_claimed_at timestamptz,
    completed_at timestamptz,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    CONSTRAINT workspace_llm_gateway_auth_gateway_scope_fk
        FOREIGN KEY (gateway_id, workspace_id)
        REFERENCES workspace_llm_gateways(id, workspace_id),
    CONSTRAINT workspace_llm_gateway_auth_gateway_version_json_safe CHECK (
        gateway_version BETWEEN 1 AND 9007199254740991
    ),
    CONSTRAINT workspace_llm_gateway_auth_state_hash_sha256 CHECK (
        pg_catalog.octet_length(oidc_state_sha256) = 32
    ),
    CONSTRAINT workspace_llm_gateway_auth_browser_hash_sha256 CHECK (
        pg_catalog.octet_length(browser_binding_sha256) = 32
    ),
    CONSTRAINT workspace_llm_gateway_auth_secrets_bounded CHECK (
        pg_catalog.octet_length(sealed_secrets) BETWEEN 29 AND 32768
    ),
    CONSTRAINT workspace_llm_gateway_auth_status_valid CHECK (
        status IN ('pending', 'callback_claimed', 'completed', 'failed', 'expired')
    ),
    CONSTRAINT workspace_llm_gateway_auth_failure_bounded CHECK (
        failure_code IS NULL OR pg_catalog.octet_length(failure_code) BETWEEN 1 AND 128
    ),
    CONSTRAINT workspace_llm_gateway_auth_expiry_order CHECK (
        expires_at > created_at
    ),
    CONSTRAINT workspace_llm_gateway_auth_version_json_safe CHECK (
        version BETWEEN 1 AND 9007199254740991
    ),
    CONSTRAINT workspace_llm_gateway_auth_state_evidence CHECK (
        (status = 'pending'
            AND callback_claimed_at IS NULL AND completed_at IS NULL
            AND failure_code IS NULL)
        OR
        (status = 'callback_claimed'
            AND callback_claimed_at IS NOT NULL AND completed_at IS NULL
            AND failure_code IS NULL)
        OR
        (status = 'completed'
            AND callback_claimed_at IS NOT NULL AND completed_at IS NOT NULL
            AND failure_code IS NULL)
        OR
        (status IN ('failed', 'expired')
            AND completed_at IS NOT NULL AND failure_code IS NOT NULL)
    )
);

CREATE INDEX workspace_llm_gateway_auth_expiry_idx
    ON workspace_llm_gateway_auth_transactions (expires_at, id)
    WHERE status IN ('pending', 'callback_claimed');

-- Existing terminal rows are retained during an upgrade. New code refuses to
-- launch a legacy row without a complete Gateway binding; operators must drain
-- queued/running runs before rolling from the static-upstream profile.
ALTER TABLE run_launch_states
    ADD COLUMN llm_gateway_id uuid,
    ADD COLUMN llm_gateway_version bigint,
    ADD COLUMN llm_gateway_grant_user_id uuid,
    ADD COLUMN model text,
    ADD CONSTRAINT run_launch_states_llm_gateway_scope_fk
        FOREIGN KEY (llm_gateway_id, workspace_id)
        REFERENCES workspace_llm_gateways(id, workspace_id),
    ADD CONSTRAINT run_launch_states_llm_gateway_grant_fk
        FOREIGN KEY (llm_gateway_id, workspace_id, llm_gateway_grant_user_id)
        REFERENCES workspace_llm_gateway_grants(gateway_id, workspace_id, user_id),
    ADD CONSTRAINT run_launch_states_llm_gateway_complete CHECK (
        (llm_gateway_id IS NULL
            AND llm_gateway_version IS NULL
            AND llm_gateway_grant_user_id IS NULL
            AND model IS NULL)
        OR
        (llm_gateway_id IS NOT NULL
            AND llm_gateway_version BETWEEN 1 AND 9007199254740991
            AND llm_gateway_grant_user_id IS NOT NULL
            AND pg_catalog.octet_length(model) BETWEEN 1 AND 256
            AND model = pg_catalog.btrim(model)
            AND pg_catalog.strpos(model, pg_catalog.chr(10)) = 0
            AND pg_catalog.strpos(model, pg_catalog.chr(13)) = 0)
    );

CREATE INDEX run_launch_states_llm_gateway_idx
    ON run_launch_states (llm_gateway_id, llm_gateway_version, llm_gateway_grant_user_id)
    WHERE llm_gateway_id IS NOT NULL;
