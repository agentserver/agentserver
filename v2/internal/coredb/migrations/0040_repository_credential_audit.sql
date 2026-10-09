CREATE TABLE repository_credential_use_events (
    event_id uuid PRIMARY KEY,
    scope jsonb NOT NULL CHECK (pg_catalog.jsonb_typeof(scope)='object' AND pg_catalog.octet_length(scope::text)<=16384),
    authority_version bigint NOT NULL CHECK (authority_version>=0),
    credential_version bigint NOT NULL CHECK (credential_version>=0),
    decision text NOT NULL CHECK (decision IN ('allow','deny')),
    created_at timestamptz NOT NULL DEFAULT pg_catalog.clock_timestamp()
);
COMMENT ON TABLE repository_credential_use_events IS 'Git preparation authorization metadata only; no credentials, URL userinfo or process payloads';
