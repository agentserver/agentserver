-- 036_mcp_oauth_clients.sql
--
-- User-owned static OAuth clients for the MCP gateway.
--
-- 2026-06-17 — Replaces the DCR flow with per-user pre-registered
-- public OAuth clients. Each user creates one or more "MCP OAuth
-- Apps" in agentserver, each mapped to a Hydra OAuth2 client (Hydra
-- holds the redirect_uris / scopes / token_endpoint_auth_method;
-- this table just records ownership so a user can list and delete
-- the clients THEY created without seeing other users' clients).
--
-- Why we have this table instead of using Hydra directly:
--   - Hydra's hydra_client table has no ownership column; without
--     this mapping a user could list/delete any client in the system
--     by guessing client_ids.
--   - The Hydra admin API requires admin creds; this table lets the
--     user-facing API enforce "you can only touch clients you own"
--     before forwarding to Hydra admin.
--
-- The hydra_client_id is the same opaque UUID Hydra returns from
-- POST /admin/clients — clients use it as the OAuth `client_id` in
-- /oauth2/auth and /oauth2/token requests.
--
-- No client_secret column — every MCP OAuth client here is a public
-- client (token_endpoint_auth_method = none), so there's no secret
-- to store. PKCE provides the missing client authentication; that's
-- both clients (Codex CLI, Claude Code) handle.

CREATE TABLE IF NOT EXISTS mcp_oauth_clients (
    id              TEXT PRIMARY KEY,                    -- our own opaque id (mcpoc_…)
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    hydra_client_id TEXT NOT NULL UNIQUE,                -- the Hydra-issued client_id
    name            TEXT NOT NULL,                       -- user-supplied label
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at    TIMESTAMPTZ                          -- updated lazily by the OAuthResolver
);

CREATE INDEX IF NOT EXISTS idx_mcp_oauth_clients_user
    ON mcp_oauth_clients (user_id);
