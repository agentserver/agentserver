# envmcp Public Gateway Design

**Date:** 2026-06-09
**Status:** Draft (with 2026-06-15 amendment — see § "2026-06-15 amendment: PAT = 1 workspace")

## Goal

Expose the existing env-mcp tool surface to **external MCP clients** (Codex Desktop / CLI, Claude Desktop in both 1P and 3P modes, and any other MCP-spec client) over a public **Streamable HTTP** endpoint authenticated by **OAuth 2.1 + DCR** (primary) or **Personal Access Token** (fallback for CI/automation).

Today env-mcp is a stdio subprocess spawned by codex-app-gateway per turn; only codex (locally embedded in the gateway pod) can reach it. After this work, any standards-compliant MCP client outside the cluster can use the same 8 tools (`list_environments`, `shell`, `unified_exec`, `apply_patch`, `read_file`, `write_stdin`, `read_output`, `terminate`) against the user's own registered executors.

Out of scope: replacing the in-pod env-mcp spawn path (it stays; it's the right design for codex-app-gateway's internal use).

## Why now

External-MCP demand has three concrete drivers:

1. **Codex Desktop / CLI** (2026-02 mac, 2026-03 win) supports remote Streamable HTTP MCP with bearer auth natively (`[mcp_servers.X] url=..., bearer_token_env_var=...`). Users want one line in `~/.codex/config.toml` instead of installing a local proxy binary.
2. **Claude Desktop 1P** (default mode) supports remote MCP via Custom Connectors UI, which speaks OAuth 2.1 + DCR. Users want UI-driven setup, not hand-edited JSON.
3. **Claude Desktop 3P** (Developer Mode, "Cowork on 3P") **disables Connectors entirely** — official guidance is "use an MCP server instead." 3P users have no choice but local stdio MCP, which today means hand-writing `claude_desktop_config.json` and pointing at a local proxy. `mcp-remote` (`npx`-installable third-party tool) bridges remote HTTP MCP → local stdio + handles OAuth/bearer transparently, so a public HTTPS MCP endpoint serves 3P users perfectly via one config snippet.

env-mcp's tool implementations (`internal/envtools/tools/*`) and bridge dialer (`internal/envtools/bridge/*`) are already transport-agnostic. Only `internal/codexappgateway/envmcp/mcp_server.go` is tied to stdio. We extract the transport-agnostic core and put a Streamable HTTP head on it.

## Non-goals

- **Not removing in-pod env-mcp.** codex-app-gateway keeps spawning env-mcp as a stdio child for its own codex subprocess — it's the right pattern there (token in env var, lifetime = turn).
- **Not adding new tools.** The 8 tools from the 2026-05-16 redesign are the public surface, full stop. New tools land in env-mcp first, gateway inherits.
- **Not building an OAuth Authorization Server from scratch.** We reuse the in-cluster Hydra deployment (`agentserver-hydra`, v26.2.0, already live).
- **Not shipping a custom local stdio binary.** Third-party `mcp-remote` covers every client we care about; writing our own would be a maintenance liability with zero UX gain.

## Architecture

```
─────────────────────── public internet ───────────────────────
                              │
                              │  HTTPS (Streamable HTTP, MCP 2025-11-25)
                              │  Authorization: Bearer <oauth_access_token | agpat_xxx>
                              ▼
istio-ingress: mcp.agent.cs.ac.cn ──► envmcp-public-gateway (new pod)
                                          │
   ┌──────────────────────────────────────┤
   │                                      │
   │ tools/list, tools/call ──────────┐   │ /oauth/authorize, /oauth/token,
   │                                  │   │ /oauth/register (DCR), /.well-known/
   │                                  ▼   ▼
   │                          ┌─────────────────┐
   │                          │ auth middleware │
   │                          │  ├─ PAT path:   verify hash → user_id
   │                          │  └─ OAuth path: introspect token via Hydra
   │                          └────────┬────────┘
   │                                   │
   │  user_id + workspace_ids + tool_allowlist
   │                                   │
   │                          ┌────────▼────────┐
   │                          │ captoken issuer │  (factored out of
   │                          │  10min TTL       │   codexappgateway/captoken.go)
   │                          └────────┬────────┘
   │                                   │
   │                          ┌────────▼────────────────┐
   │                          │ envtools/tools/*        │  (reused, unchanged)
   │                          │   shell, unified_exec,  │
   │                          │   apply_patch, ...      │
   │                          └────────┬────────────────┘
   │                                   │  ws + Bearer cap-token
   │                                   ▼
   │                          codex-exec-gateway /bridge/{exe_id}
   │                                   │  (UNCHANGED)
   │                                   ▼
   │                          executor (user's machine)
   │
   │ list_environments ──► postgres directly (workspace_executors table)
   ▼
```

Two consumers, **same backend**:

- **In-pod env-mcp** (current): stdio → tools → cap-token → exec-gateway. Untouched.
- **envmcp-public-gateway** (new): Streamable HTTP → auth → tools → cap-token → exec-gateway. Same tools, same cap-token, same exec-gateway.

## Wire protocol

**MCP spec version: 2025-11-25** (matches Claude Desktop's current implementation; Codex doesn't pin a version and works against this).

Transport: **Streamable HTTP** only (single endpoint, POST for requests + optional GET for SSE stream of server-initiated messages). No HTTP+SSE (deprecated), no WebSocket (no client supports it).

Endpoints on `https://mcp.agent.cs.ac.cn`:

| Path | Method | Purpose |
|---|---|---|
| `/mcp` | POST | MCP JSON-RPC request → JSON-RPC response (single-shot) |
| `/mcp` | GET | SSE stream for server-initiated messages (mostly idle; reserved for future server→client notifications) |
| `/mcp` | DELETE | Session termination (per spec) |
| `/.well-known/oauth-authorization-server` | GET | OAuth 2.1 metadata (RFC 8414) |
| `/.well-known/oauth-protected-resource` | GET | Resource metadata (RFC 9728) |
| `/oauth/authorize` | GET | OAuth authorization code endpoint (delegates to Hydra) |
| `/oauth/token` | POST | OAuth token endpoint (delegates to Hydra) |
| `/oauth/register` | POST | DCR (RFC 7591) |

## Authentication

**Two paths accepted on `/mcp`. Both produce the same internal principal (`user_id + workspace_ids + tool_allowlist`); rest of the gateway doesn't care which path you took.**

### Path A — OAuth 2.1 + DCR (primary, recommended for interactive clients)

For Claude Desktop UI Connectors and any client that wants zero-touch auth.

Flow (standard MCP 2025-11-25 authorization):

1. Client (Claude Desktop / mcp-remote / etc.) POSTs `/mcp` with no auth → gateway returns `401 WWW-Authenticate: Bearer resource_metadata="https://mcp.agent.cs.ac.cn/.well-known/oauth-protected-resource"`
2. Client fetches `/.well-known/oauth-protected-resource` → discovers our authorization server URL
3. Client fetches `/.well-known/oauth-authorization-server` → discovers `/oauth/register`, `/oauth/authorize`, `/oauth/token`
4. Client POSTs `/oauth/register` (DCR) → gateway forwards to Hydra → returns `client_id` (no client_secret; public client w/ PKCE)
5. Client opens browser to `/oauth/authorize?...&resource=https://mcp.agent.cs.ac.cn/mcp` (RFC 8707) → user logs in to agentserver (existing session reused if any) → consent screen → redirect to client callback with code
6. Client exchanges code at `/oauth/token` → gets access_token + refresh_token
7. Subsequent `/mcp` calls send `Authorization: Bearer <access_token>`; gateway introspects via Hydra → resolves to user_id
8. On 401 (expired), client uses refresh_token to get a new access_token automatically

Scopes:
- `mcp:read` — read_file, list_environments (default if no scope requested)
- `mcp:exec` — shell, unified_exec, apply_patch, write_stdin, read_output, terminate (explicit consent required)
- `workspace:<workspace_id>` — pin token to a specific workspace (omit for "all user's workspaces")

DCR registration request includes `scope` field; consent screen shows which scopes the client requested and lets user narrow.

### Path B — Personal Access Token (fallback for CI / automation)

For Codex CLI in scripts, CI runners, anyone who can't do interactive OAuth.

**2026-06-15 amendment: 1 PAT = 1 workspace.** PAT creation is workspace-scoped (the URL `POST /api/workspaces/{wid}/mcp/pats` carries the wid; the body has no workspace field). A user with multiple workspaces mints one PAT per workspace and adds one `[mcp_servers.X]` entry per workspace in their client config. See the dedicated amendment section at the end of this doc for the rationale; this section reflects the post-amendment shape.

User generates a PAT in the agentserver Web UI ("Workspace → Settings → MCP Access"):

```
Workspace:   ws_alpha                    # from URL — not a UI field
Name:        my-laptop-codex
Tools:       [mcp:read, mcp:exec]        # multi-select scope list; default = ["mcp:read"]
Expires:     90 days (default) / 30 / 7 / never
```

Format: `agpat_<id>_<48-char-secret><6-char-CRC>` (see `internal/secrets.MCPPATSpec`). Stored hashed (HMAC-SHA256 over server pepper) in the `mcp_pats` table.

Header: `Authorization: Bearer agpat_xxx`. Gateway recognizes the prefix and skips OAuth introspection.

PAT-derived principal: `{user_id, workspace_id, tool_allowlist, expires_at}`. The OAuth-derived principal (Phase 2) carries the same shape — `workspace_id` may be derived from the OAuth `workspace:<id>` scope; mixed PATs and OAuth principals look identical to the rest of the gateway.

### Audit & rate limit (both paths)

Every `tools/call` writes to `mcp_audit_log`:
```
{ts, principal_kind: oauth|pat, principal_id, tool, env_id, workspace_id, arg_hash, status, latency_ms}
```

Rate limit: token bucket per principal, default 60 calls/min, burst 20. Per-workspace cap on concurrent `shell`/`unified_exec` sessions: 5.

## Single-workspace routing (post 2026-06-15 amendment)

**Same as current envmcp: `environment_id` is a tool-call argument (a name from `list_environments` output), not a URL path component.**

Because each PAT is scoped to exactly one workspace, the gateway's job per request is:

1. Auth middleware → `Principal{user_id, workspace_id, tools, ...}`
2. `list_environments` returns `workspace_executors` rows for the principal's single workspace — same shape as in-pod env-mcp's output (just `[{name, description, is_default, last_seen}]`, no qualifier, no duplicates since names are unique per workspace).
3. Every other tool call carries `{environment_id: "<name>", ...}`; the gateway:
   - Uses the principal's workspace_id (no lookup needed — it's intrinsic to the PAT)
   - Mints a per-request cap-token `{turn_id: <synthetic>, workspace_id, iat, exp: now+10min}`
   - Dispatches to a per-Principal toolkit (`bridge.Pool` + `nameresolver.Resolver` + `tools.SessionStore` + tool instances, all workspace-scoped — built once per Principal, reused). The toolkit's resolver hits `codex-exec-gateway /api/codex-exec/workspaces/{wid}/executors` via the internal HTTP API to map name → exe_id, then dials `ws://codex-exec-gateway:6060/bridge/{exe_id}` with the cap-token.

Rationale:
- Architecture is **symmetric** with in-pod env-mcp: per-process workspace + per-process toolkit. The only real differences are (a) the auth front-door (PAT bearer vs codex spawn) and (b) the executor-list fetcher (HTTP to exec-gateway via shared secret vs HTTP to exec-gateway via cap-token — same endpoint different auth).
- A multi-workspace user adds N `[mcp_servers.X]` entries (one per workspace) in their client config; codex/Claude prefix tool names by server name (`work_shell`, `personal_shell`, ...) so the LLM sees them as visually distinct without any cross-workspace plumbing in the gateway.
- Tool implementations don't change.

## Cap token changes

The cap-token's existing shape `{turn_id, workspace_id, iat, exp}` already fits — `turn_id` is opaque to exec-gateway; we just synthesize `pub_<random>` for public-gateway-minted tokens.

What changes in `internal/codexappgateway/captoken.go`:
- Move to `internal/captoken/` (new package) so envmcp-public-gateway can import it without depending on codex-app-gateway internals
- Add `IssueForPrincipal(workspace_id) (token, error)` that doesn't require a real turn_id
- Default TTL stays 1h for in-pod env-mcp; **new TTL = 10min for public gateway** (public exposure → shorter blast radius)

`/bridge/{exe_id}` handler in codex-exec-gateway: **no change**. It already validates `(token.workspace_id, exe_id) ∈ workspace_executors`. Synthetic turn_id passes through.

## What's added

| Component | Purpose | Approx LOC |
|---|---|---|
| `cmd/envmcp-public-gateway/main.go` | Binary entry point | ~150 |
| `internal/mcppublic/server.go` | Streamable HTTP MCP server (POST/GET/DELETE handlers, session mgmt) | ~600 |
| `internal/mcppublic/auth.go` | OAuth introspection + PAT verification middleware | ~400 |
| `internal/mcppublic/oauth.go` | Hydra delegation, DCR proxy, metadata endpoints | ~500 |
| `internal/mcppublic/audit.go` | Audit log writer + rate limiter | ~200 |
| `internal/captoken/` | Factored out of `internal/codexappgateway/captoken.go` | (moved, no new) |
| `internal/db/mcp_pats.go` + migration `035_mcp_pats.sql` | PAT storage | ~150 + SQL |
| `internal/db/mcp_audit_log.go` + migration `036_mcp_audit_log.sql` | Audit log | ~80 + SQL |
| `internal/server/mcp_pats_api.go` | Web UI CRUD for PATs (POST/GET/DELETE under `/api/mcp/pats`) | ~250 |
| `web/src/pages/settings/MCPAccess.tsx` | UI for PAT management | ~400 |
| `deploy/helm/agentserver/templates/envmcp-public-gateway/` | Deployment, Service, HTTPRoute | ~150 yaml |
| `docs/integrations/{codex,claude-1p,claude-3p}.md` | End-user setup docs | ~150 markdown |

Reused unchanged:
- `internal/envtools/tools/*` — every tool implementation
- `internal/envtools/bridge/*` — connection pool + dialer
- `internal/codexexecgateway/*` — bridge handler, auth, frame relay

## What's removed

Nothing. All current paths continue to work.

## Multi-tenancy & security

| Concern | Mitigation |
|---|---|
| PAT theft | Argon2id-hashed at rest; prefix-indexed lookup; auto-expire; user UI shows last-used IP + ts |
| OAuth token theft | 1h access_token TTL, refresh_token rotation, DPoP not in v1 (revisit if needed) |
| Workspace boundary bypass | Cap-token signature + `workspace_executors` row check at exec-gateway (unchanged from current) |
| Cross-user workspace access | PAT/OAuth principal's `workspace_ids` ∩ requested env's workspace → reject if empty |
| Tool-level escalation | `tool_allowlist` checked at gateway before tool dispatch; PAT default omits `shell`/`unified_exec` |
| Rate abuse | Token bucket per principal + per-workspace session cap |
| Replay across regions | cap-token TTL 10min; not federated across clusters |
| Public exposure of `/internal/connected` | Public gateway does not call it. `list_environments` reads `workspace_executors` from postgres directly. The loopback `/internal/connected` endpoint stays loopback-only (unchanged) |
| TLS | istio-ingress terminates TLS with cert-manager + Let's Encrypt (existing pattern) |
| Audit gaps | Every `tools/call` logged; `tools/list` and auth-failure events also logged |

The biggest residual risk is `shell` and `unified_exec`: they're full arbitrary command execution on the user's registered executors. Mitigations layered:
1. Default PAT scope is `mcp:read` only — user must explicitly opt into `mcp:exec`
2. UI warns prominently when enabling `mcp:exec`: "this grants any holder of this token full shell access to your executors"
3. Audit log is queryable by user in Web UI
4. Executor-side codex sandbox (existing) still applies — gateway exposure doesn't bypass it

## Phases

**Phase 1 — PAT only (2 weeks):**
- Factor `internal/captoken/`
- Build `envmcp-public-gateway` binary + Streamable HTTP server + PAT auth
- PAT CRUD API + Web UI
- Helm chart + ingress
- Docs for Codex Desktop / CLI (native bearer) and Claude Desktop 1P+3P (via `npx mcp-remote --header "Authorization: Bearer ..."`)

Unblocks Codex Desktop / CLI users immediately (native config, zero proxy). Unblocks Claude Desktop users via mcp-remote (one extra line in their JSON config).

**Phase 2 — OAuth + DCR (2-3 weeks, follows Phase 1):**
- Wire Hydra as authorization backend (Hydra already deployed)
- Implement `/oauth/register` (DCR proxy), `/oauth/authorize`/`/oauth/token` (delegate to Hydra), `.well-known/*` metadata
- Consent screen UI (workspace + scope selection)
- Token introspection in auth middleware

Unblocks Claude Desktop 1P "Add Custom Connector" UI path (zero JSON, browser-based login). Also fixes Codex's open OAuth issue (#19154 "DCR not supported by enterprise IdPs") by making agentserver itself a DCR-compliant authorization server.

**Phase 3 — nice-to-have (deferred, separate spec if pursued):**
- Static-compiled `agentserver-mcp-local` Go binary for users who don't want Node/`npx` on their machine
- DPoP support if token-theft scenarios prove real
- Per-tool sub-permissions (e.g., `shell` restricted to specific commands)

## Client config examples (Phase 1)

**Codex Desktop / CLI** (`~/.codex/config.toml`):
```toml
[mcp_servers.agentserver]
url = "https://mcp.agent.cs.ac.cn/mcp"
bearer_token_env_var = "AGENTSERVER_PAT"
```
Set `AGENTSERVER_PAT=agpat_xxx` in shell env or use `codex mcp login agentserver` once Phase 2 lands.

**Claude Desktop 1P or 3P** (`claude_desktop_config.json`):
```json
{
  "mcpServers": {
    "agentserver": {
      "command": "npx",
      "args": [
        "mcp-remote",
        "https://mcp.agent.cs.ac.cn/mcp",
        "--header", "Authorization: Bearer ${AGENTSERVER_PAT}"
      ],
      "env": { "AGENTSERVER_PAT": "agpat_xxx" }
    }
  }
}
```

**Claude Desktop 1P** (Phase 2, UI):
1. Settings → Customize → Connectors → "+" → "Add custom connector"
2. URL: `https://mcp.agent.cs.ac.cn/mcp`
3. Browser opens to agentserver login → consent → done

## Open questions

1. **PAT prefix.** `agpat_` chosen for grep-ability in logs and avoidance of github-style `ghp_` confusion. Alternatives: `as_pat_`, `mcp_`. Pick one in implementation.
2. **`workspace_executors` direct query for `list_environments`.** Current in-pod env-mcp goes through loopback `/internal/connected`. Public gateway hitting DB directly is faster but duplicates logic. Acceptable since the query is trivial (single `SELECT WHERE workspace_id IN (...)`).
3. **Hydra schema mapping.** Hydra's `subject` field needs to map to agentserver `user_id`. Existing agentserver login flow may need a small bridge to mint Hydra sessions on agentserver login. Verify whether `agentserver-hydra` is already wired to agentserver's user table or just exists as a placeholder.
4. **Rate-limit storage.** In-memory token bucket per pod = different limits if multiple gateway pods. Acceptable for v1 (single replica); revisit when scaling out (Redis bucket).
5. **Hostname.** `mcp.agent.cs.ac.cn` is the natural pick; confirm with DNS owner and istio-ingress operator.

## File map

| Layer | File | Status |
|---|---|---|
| Tool implementations | `internal/envtools/tools/*` | Reused, no change |
| Bridge dialer | `internal/envtools/bridge/*` | Reused, no change |
| Cap-token issuer | `internal/codexappgateway/captoken.go` → `internal/captoken/` | Moved |
| In-pod env-mcp stdio server | `internal/codexappgateway/envmcp/mcp_server.go` | Unchanged |
| Public gateway entry | `cmd/envmcp-public-gateway/main.go` | New |
| Streamable HTTP MCP server | `internal/mcppublic/server.go` | New |
| Auth middleware | `internal/mcppublic/auth.go` | New |
| OAuth/Hydra wiring | `internal/mcppublic/oauth.go` | New |
| Audit + rate limit | `internal/mcppublic/audit.go` | New |
| PAT storage | `internal/db/mcp_pats.go` + migration 035 | New |
| Audit storage | `internal/db/mcp_audit_log.go` + migration 036 | New |
| PAT CRUD API | `internal/server/mcp_pats_api.go` | New |
| PAT UI | `web/src/pages/settings/MCPAccess.tsx` | New |
| Helm | `deploy/helm/agentserver/templates/envmcp-public-gateway/*.yaml` | New |
| Docs | `docs/integrations/{codex,claude-1p,claude-3p}.md` | New |

---

## 2026-06-15 amendment: PAT = 1 workspace

The original draft of § 4.3 ("Path B — PAT") let a single PAT span multiple workspaces of the same user (default = all memberships, optional `workspace:<id>` scopes to pin the set). While implementing this we surfaced enough drawbacks that we hard-switched the design to **exactly one workspace per PAT**, enforced at the table level (`mcp_pats.workspace_id TEXT NOT NULL REFERENCES workspaces(id)`).

### What forced the change

1. **Name collisions become a forever-tax on every tool call.** A user with `ws_alpha/macbook` and `ws_beta/macbook` (both legitimate — names are unique only per workspace, see `uniq_workspace_executors_name`) sees ambiguity at every shell/read_file/etc. call. The first draft solved this with a `@workspace_id` suffix in `list_environments` output, but that required new parsing in the dispatcher, a new code path in the resolver, and was the actual root cause of a class-of-bug in the implementation (see B1 below).
2. **Blast radius on PAT leak is the user's whole tenant.** Default-grants-all means an inadvertently committed PAT exposes every workspace the user belongs to. Hard-binding to one workspace gives the user a knob to express "this token is for client X, that token is for personal" — and a stolen client-X token can never touch personal.
3. **Audit / ops granularity is too coarse.** `last_used_at` on a PAT is meaningful only if it's bound to one purpose; a multi-workspace PAT pin-pricks across the audit log in a way that's hard to attribute.
4. **The disambiguation hack caused a real bug.** The initial `BridgeBackend.singletonResolver` stripped `@workspace_id` before caching, but the in-pod tools still received the verbatim qualified arg and tried to resolve it — guaranteed miss. The fix is "don't strip", but a cleaner fix is "the qualifier shouldn't exist in the first place", which is what 1-PAT-1-workspace gives us.
5. **Symmetry with in-pod env-mcp.** In-pod is per-process, single-workspace. Making the public path also per-Principal, single-workspace lets us reuse the in-pod `nameresolver.Resolver` + `bridge.Pool` + `tools.SessionStore` machinery directly — no `WorkspaceCache`, no `parseQualifiedName`, no per-call resolver wrapping. The two paths become near-mirror images, just authenticating differently at the front door.

### Cost: user UX for multi-workspace users

A user with N workspaces who wants Codex CLI access to all of them now needs N PATs and N `[mcp_servers.X]` entries in `~/.codex/config.toml`:

```toml
[mcp_servers.work]
url = "https://mcp.agent.cs.ac.cn/mcp"
bearer_token_env_var = "AGENTSERVER_PAT_WORK"

[mcp_servers.personal]
url = "https://mcp.agent.cs.ac.cn/mcp"
bearer_token_env_var = "AGENTSERVER_PAT_PERSONAL"
```

Codex (and other MCP clients) namespace tool calls by server name: the LLM sees `work_shell`, `work_read_file`, `personal_shell`, `personal_read_file`, … The two surfaces are visually distinct to the LLM, which actually *helps* it pick the right one without us having to teach it any cross-workspace semantics. We judge this UX cost minor for the segment of users that actually have multiple workspaces (developers and ops, not casual users).

### Schema delta

| Table | Field | Before | After |
|---|---|---|---|
| `mcp_pats` | `workspace_id` | (absent — encoded as `workspace:<id>` scope) | `TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE` |
| `mcp_pats.scopes` | catalog | `mcp:read`, `mcp:exec`, `workspace:<id>` | `mcp:read`, `mcp:exec` (the `workspace:<id>` scope is gone) |
| CRUD endpoint | path | `/api/mcp/pats` | `/api/workspaces/{wid}/mcp/pats` |
| `Principal` (Go) | workspace field | `WorkspaceIDs map[string]struct{}` | `WorkspaceID string` |

### Code delta

| What | Before | After |
|---|---|---|
| `internal/mcppublic/workspace_cache.go` | TTL'd snapshot per (principal's workspace set) + name → (ws,exe) + qualifier disambig | **deleted** — single workspace, no collisions, in-pod `nameresolver.Resolver` is sufficient |
| `internal/mcppublic/dispatch.go::handleListEnvironments` | union across workspaces, count dups, append `@workspace_id` | flat list-one-workspace, no dup handling |
| `internal/mcppublic/dispatch.go::ToolsCall` | extract env_id, look up workspace, check `Principal.HasWorkspace`, mint cap-token | extract is not needed; principal.WorkspaceID is intrinsic, mint and dispatch |
| `internal/mcppublic/bridge_backend.go::singletonResolver` | per-call resolver pre-populated with one (name → exe_id) entry | **deleted** — backend builds a per-Principal toolkit with the real `nameresolver.Resolver` driven by an HTTPExecutorsSource-backed Fetcher |
| `parseQualifiedName` (and the `@workspace_id` convention) | parser + tests | **deleted** |

Net effect: roughly **-250 LOC**, simpler control flow, intentional symmetry with the in-pod path.

### Migration concern (none)

The `mcp_pats` table doesn't yet exist on `main` (the bottom-of-stack PR adding it has not merged), so we can land the amended migration without a follow-up `ALTER TABLE` — the `workspace_id NOT NULL` column simply ships in migration 035 from day one.
