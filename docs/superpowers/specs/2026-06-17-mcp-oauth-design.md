
---

## Amendment 2026-06-17 (afternoon) — DCR off, static client_id in

The DCR-based design above shipped as #249/#251 and **worked**, but
investigation of the Codex CLI source (rmcp-client/perform_oauth_login.rs)
showed Codex calls `start_authorization()`, not the SDK's
`start_authorization_with_metadata_url()` — i.e. **Codex CLI does
not implement CIMD**, only DCR or pre-registered `client_id`. Claude
Code supports DCR + CIMD + pre-registered.

Combined with RFC 7591 §5's explicit warning that open registration
endpoints invite table-bloat attacks, we flipped strategy:

- **Disable DCR** in Hydra (`OIDC_DYNAMIC_CLIENT_REGISTRATION_ENABLED=false`)
- **Stop advertising** `registration_endpoint` in AS metadata
- **Stop reverse-proxying** `/oauth2/register` through agentserver
- **Add per-user static OAuth client management** at
  `POST /api/me/oauth-clients` (and GET, DELETE). User-owned table
  `mcp_oauth_clients` mapping our opaque id → Hydra's client_id; the
  HTTP handler authenticates the user, calls Hydra admin to mint a
  public client (`token_endpoint_auth_method=none`, PKCE-protected),
  records the ownership row, and returns the `client_id` for the user
  to paste into Codex / Claude Code config.

Cost: ~400 LOC backend (migration + db layer + 3 HTTP handlers + auth
admin wrapper + tests). PAT path unchanged.

UX trade: one extra `curl POST` per developer per machine vs. open
DCR. Acceptable because:
- It's a one-time setup, cached in CLI config thereafter
- Maps cleanly to enterprise OAuth norms (every developer creates
  their own GitHub Personal OAuth App once)
- Cuts off the table-bloat vector with no rate-limiting infrastructure

Frontend UI for /api/me/oauth-clients management lives in a follow-up
PR; for v1 docs show the `curl` flow directly.

---

## Amendment 2026-06-17 (evening) — single shared client

#256 (per-user clients via `/api/me/oauth-clients`) shipped and worked,
but on reflection the per-user model was overengineered:

- The `client_id` of a public OAuth client is not a secret (PKCE
  carries that load). Same shape gh CLI / GitHub Desktop / VS Code
  share globally.
- Per-user clients added a curl step to every install. Friction with
  no security benefit.
- Audit / revocation can still happen at the user (token.sub) +
  workspace-membership level — client-id-per-machine granularity
  isn't actually useful for our deployment shape.

So this amendment **deletes the per-user table + REST surface from
#256** and replaces it with a single shared client provisioned by the
existing `hydra-client-setup` Helm job:

- `client_id = "agentserver-mcp"` (split into `-cli` + `-desktop` in 2026-06-18 amendment below)
- `token_endpoint_auth_method = none` (public client, PKCE-protected)
- `redirect_uris = [http://localhost/callback, http://127.0.0.1/callback]`
  — host-only per RFC 8252 §7.3 so Hydra accepts any port the CLI
  binds
- `scope = "openid mcp:read mcp:exec"`
- `audience = ["https://mcp.<domain>/mcp"]` so issued tokens
  carry the RFC 8707 `aud` claim our resolver enforces

Docs (codex-cli.md, claude-code-cli.md) updated to point to the
fixed client_id directly — no curl step. Users just paste the
client_id into their CLI config.

If a future need for per-user clients re-emerges (e.g. multi-org
deployments where one user's client should be revocable without
affecting others), revive the #256 table — the Hydra admin wrapper
(internal/auth/hydra.go CreateOAuth2Client/DeleteOAuth2Client) and
REST surface design can be cherry-picked back.

---

## Amendment 2026-06-18 — split mcp client into cli + desktop

End-to-end testing surfaced two issues that pushed us to split the
single `agentserver-mcp` client into two:

1. **Different callback paths per surface.** Claude Code / Codex use
   `http://localhost:PORT/callback`; mcp-remote (Claude Desktop's
   stdio bridge) uses `http://localhost:PORT/oauth/callback`. Hydra
   string-matches the full URL including path, so we'd previously
   stuffed all four URIs (2 hosts × 2 paths) onto one client. Means
   a single client's compromise leaks across surfaces.

2. **Independent audit / revocation.** With a single client_id, ops
   can't tell from the Hydra side whether a token came from CLI vs
   desktop, can't revoke desktop sessions without also nuking CLI.
   Spliting gives per-surface granularity in `hydra delete
   oauth2-client` and in introspect `client_id` field.

Two clients now:

| client_id | redirect_uris | surface |
|---|---|---|
| `agentserver-mcp-cli` | `http://{localhost,127.0.0.1}:20202/callback` | Claude Code CLI, Codex CLI |
| `agentserver-mcp-desktop` | `http://{localhost,127.0.0.1}:20202/oauth/callback` | Claude Desktop via `mcp-remote` |

Same port (20202), same audience, same scopes — only the redirect
path differs. The hydra-client-setup helm job creates both and
deletes the old combined `agentserver-mcp` row (idempotent on
subsequent upgrades).

Docs (codex-cli.md, claude-code-cli.md, claude-desktop.md — the
last one replacing the older "claude-desktop-3p.md") updated. The
"3P / Developer Mode" qualifier dropped because Custom Connectors
are now available across all Claude Desktop plans (Free included),
so the doc covers both the native Custom Connector path (Path A,
not enabled yet) and the mcp-remote bridge path (Path B, default).

OAuthResolver in mcppublic accepts both client_ids transparently —
it never inspects `client_id`, only the introspected `sub` /
`workspace_id` / `aud` / `scope` claims.

---

## Amendment 2026-06-18 (refined) — three-way split: claude-code, codex, claude-desktop

The 2026-06-18 amendment above split `agentserver-mcp` into two
clients (`-cli` + `-desktop`). After confirming Codex Desktop's MCP
OAuth code path is identical to Codex CLI's (both use the same
`~/.codex/config.toml` `mcp_oauth_callback_port` / `_url`; see
`codex-rs/app-server/src/request_processors/mcp_processor.rs:172`),
refined to a three-way split that respects the actual
config-store boundaries each vendor draws:

| client_id | callback path | surface |
|---|---|---|
| `agentserver-mcp-claude-code` | `/callback` | Claude Code CLI |
| `agentserver-mcp-codex` | `/callback` | Codex CLI **and** Codex Desktop (shared config) |
| `agentserver-mcp-claude-desktop` | `/oauth/callback` | Claude Desktop via `mcp-remote` |

Why not split Codex into CLI + Desktop:
- They share `~/.codex/config.toml` and the same OAuth callback code
  (Desktop's `mcpServer/oauth/login` JSON-RPC delegates to the same
  CLI path). Splitting would force users to maintain two configs
  for no audit benefit (a single user's Codex install IS a single
  user/machine).

Why split Claude Code vs Claude Desktop:
- Different config stores (`~/.claude/` vs OS-app-data dir).
- Claude Desktop's only working OAuth path today is mcp-remote
  bridge, which uses `/oauth/callback` path — naturally distinct.
- Independent revocation (`hydra delete oauth2-client
  agentserver-mcp-claude-desktop`) without affecting CLI.

The hydra-client-setup helm job creates all three, deletes
obsolete `agentserver-mcp`, `agentserver-mcp-cli`,
`agentserver-mcp-desktop` from the 2026-06-17 and 2026-06-18 first
amendments (idempotent best-effort).
