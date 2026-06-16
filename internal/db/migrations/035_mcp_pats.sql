-- mcp_pats: workspace-scoped Personal Access Tokens for the envmcp
-- public gateway. Spec'd in
-- docs/superpowers/specs/2026-06-09-envmcp-public-gateway-design.md § 4.3
-- as the fallback for CI/automation; OAuth-via-Hydra is the primary path
-- and lands in Phase 2.
--
-- Design choice (2026-06-15 amendment to the 2026-06-09 spec):
-- **1 PAT = 1 workspace**, period. Earlier drafts allowed a single PAT
-- to span multiple workspaces of the same user (default = all
-- memberships, optional workspace:<id> scopes to pin). That was a
-- footgun: name collisions across workspaces forced an ad-hoc
-- @workspace_id disambiguation in tool calls, leak blast radius was
-- the user's whole workspace set, and audit granularity was too
-- coarse. We replaced it with a hard "workspace_id is a first-class
-- column, FK to workspaces, NOT NULL" constraint — a multi-workspace
-- user mints one PAT per workspace and adds one [mcp_servers.X]
-- entry per workspace in their client config (codex's tool-name
-- prefix-per-server keeps them visually distinct to the LLM).
--
-- Format on the wire (see internal/secrets.MCPPATSpec):
--   agpat_<16-char base62 id>_<48-char base62 secret><6-char base62 CRC32>
-- DB stores:
--   - id    = "agpat_<id>" (also indexed for O(1) bearer lookup)
--   - secret_hash = hex(HMAC-SHA256(server_pepper, full_token))
--                   or hex(sha256(full_token)) when pepper unset (dev)
--
-- Scopes are strings drawn from a fixed catalog (enforced in
-- internal/server/mcp_pat_scopes.go). The workspace:<id> scope from
-- the earlier draft is gone — workspace is intrinsic to the PAT row.
--   - 'mcp:read'   — read_file, list_environments
--   - 'mcp:exec'   — shell, exec_command, apply_patch, write_stdin,
--                    read_output, terminate, copy_path
CREATE TABLE mcp_pats (
    id            TEXT        PRIMARY KEY,
    user_id       TEXT        NOT NULL REFERENCES users(id)      ON DELETE CASCADE,
    -- workspace_id ON DELETE CASCADE so deleting a workspace garbage-
    -- collects every PAT bound to it (the PATs can't authorise
    -- anything anyway once the workspace is gone).
    workspace_id  TEXT        NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name          TEXT        NOT NULL,
    prefix        TEXT        NOT NULL,
    secret_hash   TEXT        NOT NULL,
    scopes        TEXT[]      NOT NULL DEFAULT '{}',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at    TIMESTAMPTZ NOT NULL,
    last_used_at  TIMESTAMPTZ,
    revoked_at    TIMESTAMPTZ
);

-- Lookup index for bearer validation. Partial index keeps it small by
-- excluding revoked rows (the common path is "token still active"). The
-- expires_at check is in the WHERE of validation queries, not the index,
-- because index predicates with NOW() are not immutable.
CREATE INDEX idx_mcp_pats_prefix_active
    ON mcp_pats (prefix)
    WHERE revoked_at IS NULL;

-- Per-user list view (Settings → MCP Access UI in Phase 1).
CREATE INDEX idx_mcp_pats_user
    ON mcp_pats (user_id, created_at DESC);

-- Per-workspace list view: settings tab inside a workspace shows the
-- PATs bound to *this* workspace; revoke-all-on-workspace-delete is
-- already handled by FK CASCADE but ops queries (audit, search) want
-- the index too.
CREATE INDEX idx_mcp_pats_workspace
    ON mcp_pats (workspace_id, created_at DESC);
