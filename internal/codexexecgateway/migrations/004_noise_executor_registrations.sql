-- Per spec docs/superpowers/specs/2026-06-18-codex-exec-gateway-noise-relay-design.md
-- §3.1 + Phase 2.1 + §6 D-3 (single-replica, in-memory routing).
--
-- Rows are durable so a gateway pod restart can re-issue the same
-- registration_id when an executor re-registers with the same static
-- public key (idempotent re-registration on (env_id, pubkey_fingerprint)).
-- The active WS connection itself lives only in memory per-pod.
--
-- env_id is the workspace-scoped environment id chosen by the
-- registering executor. It maps 1:1 to the legacy executors.exe_id
-- when this executor is also a legacy-bridge user (transition period
-- per §6 D-1) but the noise-registration row stays separate to avoid
-- polluting the legacy schema with optional-everywhere noise columns.

CREATE TABLE IF NOT EXISTS noise_executor_registrations (
    registration_id        TEXT        PRIMARY KEY,
    env_id                 TEXT        NOT NULL,

    -- Both halves of the suite live here as base64 strings: the wire
    -- form matches codex's NoiseChannelPublicKey JSON so re-emission
    -- needs no decode/encode round trip.
    suite                  TEXT        NOT NULL,
    x25519_public_key      TEXT        NOT NULL,
    mlkem768_public_key    TEXT        NOT NULL,

    -- Stable hash over (suite, x25519, mlkem768) for idempotent upsert
    -- on (env_id, executor_pubkey_fingerprint).
    pubkey_fingerprint     BYTEA       NOT NULL,

    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (env_id, pubkey_fingerprint)
);

CREATE INDEX IF NOT EXISTS idx_noise_executor_registrations_env_id
    ON noise_executor_registrations (env_id);

CREATE INDEX IF NOT EXISTS idx_noise_executor_registrations_last_seen
    ON noise_executor_registrations (last_seen_at);
