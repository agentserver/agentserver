-- Helm executes production bootstrap after every install and upgrade. Product
-- rows alone cannot identify the immutable seed once a workspace legitimately
-- contains additional sessions or identities, so retain one explicit seed
-- authority and reject a different bootstrap document before inserting rows.
ALTER TABLE user_identities
    ADD CONSTRAINT user_identities_identity_user_unique
    UNIQUE (issuer, subject, user_id);

CREATE TABLE production_bootstrap_seeds (
    singleton boolean PRIMARY KEY DEFAULT TRUE,
    workspace_id uuid NOT NULL REFERENCES workspaces(id),
    session_id uuid NOT NULL,
    user_id uuid NOT NULL REFERENCES users(id),
    external_oidc_issuer text NOT NULL,
    external_oidc_subject text NOT NULL,
    executor_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp(),
    CONSTRAINT production_bootstrap_seeds_singleton CHECK (singleton),
    CONSTRAINT production_bootstrap_seeds_session_scope_fk
        FOREIGN KEY (session_id, workspace_id)
        REFERENCES sessions(id, workspace_id),
    CONSTRAINT production_bootstrap_seeds_identity_user_fk
        FOREIGN KEY (external_oidc_issuer, external_oidc_subject, user_id)
        REFERENCES user_identities(issuer, subject, user_id),
    CONSTRAINT production_bootstrap_seeds_membership_fk
        FOREIGN KEY (workspace_id, user_id)
        REFERENCES workspace_members(workspace_id, user_id),
    CONSTRAINT production_bootstrap_seeds_executor_scope_fk
        FOREIGN KEY (executor_id, workspace_id)
        REFERENCES executors(id, workspace_id),
    CONSTRAINT production_bootstrap_seeds_issuer_bounded CHECK (
        pg_catalog.octet_length(external_oidc_issuer) BETWEEN 8 AND 2048
        AND external_oidc_issuer = pg_catalog.btrim(external_oidc_issuer)
        AND pg_catalog.strpos(external_oidc_issuer, pg_catalog.chr(10)) = 0
        AND pg_catalog.strpos(external_oidc_issuer, pg_catalog.chr(13)) = 0
    ),
    CONSTRAINT production_bootstrap_seeds_subject_bounded CHECK (
        pg_catalog.octet_length(external_oidc_subject) BETWEEN 1 AND 2048
    )
);
