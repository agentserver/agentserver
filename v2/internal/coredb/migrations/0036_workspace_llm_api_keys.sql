-- OAuth grants remain per-user. API keys belong to the workspace gateway and
-- are usable only after the same live owner/developer membership checks.
ALTER TABLE workspace_llm_gateways
    ADD COLUMN auth_type text NOT NULL DEFAULT 'oidc',
    ADD COLUMN sealed_api_key bytea,
    DROP CONSTRAINT workspace_llm_gateways_oidc_issuer_bounded,
    DROP CONSTRAINT workspace_llm_gateways_oidc_client_bounded,
    DROP CONSTRAINT workspace_llm_gateways_oidc_scopes_bounded,
    ADD CONSTRAINT workspace_llm_gateways_auth_type_valid CHECK (auth_type IN ('oidc', 'api_key')),
    ADD CONSTRAINT workspace_llm_gateways_auth_configuration CHECK (
        (auth_type = 'oidc' AND sealed_api_key IS NULL
         AND pg_catalog.octet_length(oidc_issuer) BETWEEN 8 AND 2048
         AND pg_catalog.octet_length(oidc_client_id) BETWEEN 1 AND 512
         AND pg_catalog.octet_length(oidc_scopes) BETWEEN 6 AND 2048)
        OR
        (auth_type = 'api_key' AND oidc_issuer = '' AND oidc_client_id = '' AND oidc_scopes = ''
         AND bearer_token_type = 'access_token'
         AND ((status = 'active' AND sealed_api_key IS NOT NULL
               AND pg_catalog.octet_length(sealed_api_key) BETWEEN 29 AND 16384)
              OR (status = 'disabled' AND sealed_api_key IS NULL)))
    ),
    ADD CONSTRAINT workspace_llm_gateways_oidc_text_clean CHECK (
        oidc_issuer = pg_catalog.btrim(oidc_issuer) AND oidc_client_id = pg_catalog.btrim(oidc_client_id)
        AND oidc_scopes = pg_catalog.btrim(oidc_scopes)
        AND oidc_issuer !~ E'[\\r\\n]' AND oidc_client_id !~ E'[\\r\\n]' AND oidc_scopes !~ E'[\\t\\r\\n]'
    );

-- This historical column continues to identify the authorized run actor.
-- A workspace API key has no fabricated OAuth grant. OAuth grant existence,
-- gateway version, workspace scope and live membership remain checked in the
-- same run-creation / per-request authorization transactions.
ALTER TABLE run_launch_states
    DROP CONSTRAINT run_launch_states_llm_gateway_grant_fk,
    ADD CONSTRAINT run_launch_states_llm_gateway_actor_fk
        FOREIGN KEY (llm_gateway_grant_user_id) REFERENCES users(id);
