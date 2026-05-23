-- 032_exec_audit.sql
-- Schema for the codex-exec-gateway audit subsystem. Each ws bridge session
-- between env-mcp (or the SDK REST bridge.Pool) and codex-exec produces one
-- exec_audit_sessions row; every logical call (JSON-RPC request/response
-- pair, or SDK tool invocation, or relay PUT/GET) produces one
-- exec_audit_calls row; payload bytes >4 KiB live in exec_audit_payloads
-- (zstd-compressed, sha256-deduped) and are referenced by id.
--
-- See docs/superpowers/specs/2026-05-23-codex-exec-gateway-audit-design.md.

CREATE TABLE exec_audit_payloads (
  id              UUID PRIMARY KEY,
  sha256          TEXT NOT NULL UNIQUE,
  compressed      BYTEA NOT NULL,
  original_size   INT NOT NULL,
  compressed_size INT NOT NULL,
  ref_count       INT NOT NULL DEFAULT 0,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX exec_audit_payloads_created ON exec_audit_payloads(created_at);

CREATE TABLE exec_audit_sessions (
  id                UUID PRIMARY KEY,
  workspace_id      TEXT NOT NULL,
  user_id           TEXT,
  exe_id            TEXT NOT NULL,
  turn_id           TEXT,
  stream_id         TEXT NOT NULL,
  client_ip         INET,
  cap_iat           TIMESTAMPTZ,
  cap_exp           TIMESTAMPTZ,
  opened_at         TIMESTAMPTZ NOT NULL,
  closed_at         TIMESTAMPTZ,
  close_reason      TEXT,
  frames_to_backend INT NOT NULL DEFAULT 0,
  frames_to_client  INT NOT NULL DEFAULT 0,
  bytes_to_backend  BIGINT NOT NULL DEFAULT 0,
  bytes_to_client   BIGINT NOT NULL DEFAULT 0
);

CREATE INDEX exec_audit_sessions_ws_time   ON exec_audit_sessions(workspace_id, opened_at DESC);
CREATE INDEX exec_audit_sessions_exe_time  ON exec_audit_sessions(exe_id, opened_at DESC);
CREATE INDEX exec_audit_sessions_user_time ON exec_audit_sessions(user_id, opened_at DESC) WHERE user_id IS NOT NULL;
CREATE INDEX exec_audit_sessions_turn      ON exec_audit_sessions(turn_id) WHERE turn_id IS NOT NULL;

CREATE TABLE exec_audit_calls (
  id                  UUID PRIMARY KEY,
  session_id          UUID REFERENCES exec_audit_sessions(id) ON DELETE CASCADE,
  workspace_id        TEXT NOT NULL,
  user_id             TEXT,
  exe_id              TEXT NOT NULL,
  source              TEXT NOT NULL CHECK (source IN ('envmcp','rest','relay')),
  rpc_id              TEXT,
  rpc_method          TEXT,
  rpc_kind            TEXT,
  request_payload_id  UUID REFERENCES exec_audit_payloads(id),
  request_size        INT NOT NULL DEFAULT 0,
  request_sha256      TEXT,
  response_payload_id UUID REFERENCES exec_audit_payloads(id),
  response_size       INT NOT NULL DEFAULT 0,
  response_sha256     TEXT,
  is_error            BOOLEAN NOT NULL DEFAULT FALSE,
  error_summary       TEXT,
  started_at          TIMESTAMPTZ NOT NULL,
  completed_at        TIMESTAMPTZ,
  duration_ms         INTEGER
);

CREATE INDEX exec_audit_calls_ws_time   ON exec_audit_calls(workspace_id, started_at DESC);
CREATE INDEX exec_audit_calls_exe_time  ON exec_audit_calls(exe_id, started_at DESC);
CREATE INDEX exec_audit_calls_user_time ON exec_audit_calls(user_id, started_at DESC) WHERE user_id IS NOT NULL;
CREATE INDEX exec_audit_calls_method    ON exec_audit_calls(rpc_method) WHERE rpc_method IS NOT NULL;
CREATE INDEX exec_audit_calls_source    ON exec_audit_calls(source, started_at DESC);
CREATE INDEX exec_audit_calls_session   ON exec_audit_calls(session_id) WHERE session_id IS NOT NULL;
CREATE INDEX exec_audit_calls_errors    ON exec_audit_calls(workspace_id, started_at DESC) WHERE is_error;
