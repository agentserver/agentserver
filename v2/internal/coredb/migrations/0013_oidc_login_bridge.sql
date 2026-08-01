CREATE TABLE users (
    id uuid PRIMARY KEY,
    status text NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    CONSTRAINT users_status_valid CHECK (
        status IN ('active', 'suspended', 'deleted')
    ),
    CONSTRAINT users_version_positive CHECK (version > 0)
);

-- Earlier migrations used the canonical Hydra subject directly as user_id.
-- Materialize those existing principals before adding the membership FK.
INSERT INTO users (id, status)
SELECT user_id, 'active'
FROM workspace_members
ON CONFLICT DO NOTHING;

INSERT INTO users (id, status)
SELECT actor_id, 'active'
FROM runs
ON CONFLICT DO NOTHING;

ALTER TABLE workspace_members
    ADD CONSTRAINT workspace_members_user_fk
    FOREIGN KEY (user_id) REFERENCES users(id);

CREATE TABLE user_identities (
    issuer text NOT NULL,
    subject text NOT NULL,
    user_id uuid NOT NULL REFERENCES users(id),
    status text NOT NULL DEFAULT 'active',
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    PRIMARY KEY (issuer, subject),
    CONSTRAINT user_identities_issuer_bounded CHECK (
        pg_catalog.octet_length(issuer) BETWEEN 8 AND 2048
    ),
    CONSTRAINT user_identities_subject_bounded CHECK (
        pg_catalog.octet_length(subject) BETWEEN 1 AND 2048
    ),
    CONSTRAINT user_identities_status_valid CHECK (
        status IN ('active', 'revoked')
    ),
    CONSTRAINT user_identities_version_positive CHECK (version > 0)
);

CREATE INDEX user_identities_user_status_idx
    ON user_identities (user_id, status);

-- Only hashes used for lookup/correlation are stored in cleartext. The Hydra
-- challenge, OIDC state/nonce, PKCE verifier, and browser binding are sealed
-- together by Core with transaction-scoped AAD before they cross this boundary.
CREATE TABLE oidc_login_transactions (
    id uuid PRIMARY KEY,
    login_challenge_sha256 bytea NOT NULL UNIQUE,
    oidc_state_sha256 bytea NOT NULL UNIQUE,
    browser_binding_sha256 bytea NOT NULL,
    sealed_secrets bytea NOT NULL,
    oidc_issuer text NOT NULL,
    hydra_client_id text NOT NULL,
    status text NOT NULL,
    user_id uuid REFERENCES users(id),
    sealed_redirect bytea,
    failure_code text,
    expires_at timestamptz NOT NULL,
    callback_claimed_at timestamptz,
    authenticated_at timestamptz,
    completed_at timestamptz,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    CONSTRAINT oidc_login_transactions_challenge_hash_sha256 CHECK (
        pg_catalog.octet_length(login_challenge_sha256) = 32
    ),
    CONSTRAINT oidc_login_transactions_state_hash_sha256 CHECK (
        pg_catalog.octet_length(oidc_state_sha256) = 32
    ),
    CONSTRAINT oidc_login_transactions_browser_hash_sha256 CHECK (
        pg_catalog.octet_length(browser_binding_sha256) = 32
    ),
    CONSTRAINT oidc_login_transactions_sealed_secrets_bounded CHECK (
        pg_catalog.octet_length(sealed_secrets) BETWEEN 29 AND 16384
    ),
    CONSTRAINT oidc_login_transactions_issuer_bounded CHECK (
        pg_catalog.octet_length(oidc_issuer) BETWEEN 8 AND 2048
    ),
    CONSTRAINT oidc_login_transactions_client_bounded CHECK (
        pg_catalog.octet_length(hydra_client_id) BETWEEN 1 AND 512
    ),
    CONSTRAINT oidc_login_transactions_status_valid CHECK (
        status IN (
            'pending', 'callback_claimed', 'authenticated', 'accepting',
            'accepted', 'rejected', 'failed', 'expired'
        )
    ),
    CONSTRAINT oidc_login_transactions_failure_bounded CHECK (
        failure_code IS NULL OR pg_catalog.octet_length(failure_code) BETWEEN 1 AND 128
    ),
    CONSTRAINT oidc_login_transactions_redirect_bounded CHECK (
        sealed_redirect IS NULL OR pg_catalog.octet_length(sealed_redirect) BETWEEN 29 AND 16384
    ),
    CONSTRAINT oidc_login_transactions_expiry_order CHECK (
        expires_at > created_at
    ),
    CONSTRAINT oidc_login_transactions_version_positive CHECK (version > 0),
    CONSTRAINT oidc_login_transactions_state_evidence CHECK (
        (status = 'pending'
            AND callback_claimed_at IS NULL AND authenticated_at IS NULL
            AND completed_at IS NULL AND user_id IS NULL
            AND sealed_redirect IS NULL AND failure_code IS NULL)
        OR
        (status = 'callback_claimed'
            AND callback_claimed_at IS NOT NULL AND authenticated_at IS NULL
            AND completed_at IS NULL AND user_id IS NULL
            AND sealed_redirect IS NULL AND failure_code IS NULL)
        OR
        (status IN ('authenticated', 'accepting')
            AND callback_claimed_at IS NOT NULL AND authenticated_at IS NOT NULL
            AND completed_at IS NULL AND user_id IS NOT NULL
            AND sealed_redirect IS NULL AND failure_code IS NULL)
        OR
        (status = 'accepted'
            AND callback_claimed_at IS NOT NULL AND authenticated_at IS NOT NULL
            AND completed_at IS NOT NULL AND user_id IS NOT NULL
            AND sealed_redirect IS NOT NULL AND failure_code IS NULL)
        OR
        (status IN ('rejected', 'failed', 'expired')
            AND completed_at IS NOT NULL AND sealed_redirect IS NULL
            AND failure_code IS NOT NULL)
    )
);

CREATE INDEX oidc_login_transactions_expiry_idx
    ON oidc_login_transactions (expires_at, id)
    WHERE status IN ('pending', 'callback_claimed', 'authenticated', 'accepting');

-- Consent is a separate Hydra challenge and therefore has its own one-shot
-- receipt. It never inherits authority merely because login succeeded.
CREATE TABLE hydra_consent_transactions (
    consent_challenge_sha256 bytea PRIMARY KEY,
    request_sha256 bytea NOT NULL,
    hydra_client_id text NOT NULL,
    user_id uuid NOT NULL REFERENCES users(id),
    status text NOT NULL,
    sealed_redirect bytea,
    failure_code text,
    expires_at timestamptz NOT NULL,
    completed_at timestamptz,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    CONSTRAINT hydra_consent_transactions_challenge_hash_sha256 CHECK (
        pg_catalog.octet_length(consent_challenge_sha256) = 32
    ),
    CONSTRAINT hydra_consent_transactions_request_hash_sha256 CHECK (
        pg_catalog.octet_length(request_sha256) = 32
    ),
    CONSTRAINT hydra_consent_transactions_client_bounded CHECK (
        pg_catalog.octet_length(hydra_client_id) BETWEEN 1 AND 512
    ),
    CONSTRAINT hydra_consent_transactions_status_valid CHECK (
        status IN ('accepting', 'accepted', 'rejected', 'failed', 'expired')
    ),
    CONSTRAINT hydra_consent_transactions_redirect_bounded CHECK (
        sealed_redirect IS NULL OR pg_catalog.octet_length(sealed_redirect) BETWEEN 29 AND 16384
    ),
    CONSTRAINT hydra_consent_transactions_failure_bounded CHECK (
        failure_code IS NULL OR pg_catalog.octet_length(failure_code) BETWEEN 1 AND 128
    ),
    CONSTRAINT hydra_consent_transactions_expiry_order CHECK (
        expires_at > created_at
    ),
    CONSTRAINT hydra_consent_transactions_version_positive CHECK (version > 0),
    CONSTRAINT hydra_consent_transactions_state_evidence CHECK (
        (status = 'accepting' AND completed_at IS NULL
            AND sealed_redirect IS NULL AND failure_code IS NULL)
        OR
        (status = 'accepted' AND completed_at IS NOT NULL
            AND sealed_redirect IS NOT NULL AND failure_code IS NULL)
        OR
        (status IN ('rejected', 'failed', 'expired') AND completed_at IS NOT NULL
            AND sealed_redirect IS NULL AND failure_code IS NOT NULL)
    )
);

CREATE INDEX hydra_consent_transactions_expiry_idx
    ON hydra_consent_transactions (expires_at)
    WHERE status = 'accepting';
