# Codex CLI integration

Run your agentserver workspaces' tools (`shell`, `read_file`, `apply_patch`, …) from inside Codex CLI, against any executor you've registered.

## Prerequisites

- A workspace + at least one registered executor in agentserver (Settings → Executors)
- Codex CLI ≥ 0.140 (`codex mcp login` support)
- An agentserver login

## Step 1 — Add to `~/.codex/config.toml`

```toml
[mcp_servers.agentserver]
url = "https://mcp.agent.cs.ac.cn/v1/mcp"
oauth_resource = "https://mcp.agent.cs.ac.cn/v1/mcp"

[mcp_servers.agentserver.oauth]
client_id = "agentserver-mcp"
```

The `client_id` is a fixed, public value shared by all users (same shape as `gh` CLI's hard-coded GitHub OAuth client). It carries no auth power on its own — the OAuth flow's user login + workspace consent screen is what actually authorizes the issued token.

The `oauth_resource` (RFC 8707) binds the token to this gateway's URL — without it, the gateway's resolver rejects every request because the token's `aud` claim is empty.

## Step 2 — Login

```
codex mcp login agentserver
```

A browser opens to agentserver. Log in if needed, pick the workspace you want this Codex install to act on, click **Allow** (you'll see two scope rows: `mcp:read` "Read files and list environments", and ⚠️ `mcp:exec` "Run shell commands"). Browser jumps back to localhost; token is stored under `~/.codex/mcp_oauth/agentserver.json` and silently refreshes from there.

## Step 3 — Verify

In a Codex session:

```
> please list the environments I have access to
```

Codex should call `agentserver_list_environments` and print your workspace's executors.

Direct curl smoke test (skips the LLM):

```bash
TOKEN=$(jq -r .access_token ~/.codex/mcp_oauth/agentserver.json)
curl -s -X POST https://mcp.agent.cs.ac.cn/v1/mcp \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | jq
```

You should see your 9 tool definitions.

## Multi-workspace setup

Each `[mcp_servers.X]` entry corresponds to one workspace (workspace is selected at consent time, then baked into the token). Reuse the same `client_id` across entries; the workspace selection during login is what distinguishes them.

```toml
[mcp_servers.work]
url = "https://mcp.agent.cs.ac.cn/v1/mcp"
oauth_resource = "https://mcp.agent.cs.ac.cn/v1/mcp"
[mcp_servers.work.oauth]
client_id = "agentserver-mcp"

[mcp_servers.personal]
url = "https://mcp.agent.cs.ac.cn/v1/mcp"
oauth_resource = "https://mcp.agent.cs.ac.cn/v1/mcp"
[mcp_servers.personal.oauth]
client_id = "agentserver-mcp"
```

```bash
codex mcp login work       # consent → pick "Work" workspace
codex mcp login personal   # consent → pick "Personal" workspace
```

Codex auto-prefixes tool names by server (`work_shell`, `personal_shell`).

## Revoke

`codex mcp logout agentserver` clears the local token. The token continues to be valid against the gateway until it expires naturally (1h access token, 30d refresh), unless an admin revokes it via `hydra revoke access-token` from inside the cluster.

For workspace-membership-based revocation (most common case — "this user shouldn't access this workspace anymore"), just remove the user from the workspace; the gateway re-checks membership on every request and rejects within ≤10s.

## Service-account / CI alternative: Personal Access Tokens

For environments without a browser (CI, scripts), mint a long-lived workspace-scoped PAT (workspace `owner` or `maintainer` role required):

```bash
WID=ws_yourworkspaceid
curl -s -X POST "https://agent.cs.ac.cn/api/workspaces/${WID}/mcp/pats" \
  -H "Cookie: agentserver_session=..." \
  -H "Content-Type: application/json" \
  -d '{"name":"ci-pipeline","scopes":["mcp:read","mcp:exec"],"expires_at":"2026-09-09T00:00:00Z"}'
```

Then:

```toml
[mcp_servers.agentserver]
url = "https://mcp.agent.cs.ac.cn/v1/mcp"
bearer_token_env_var = "AGENTSERVER_PAT"
```

```bash
export AGENTSERVER_PAT='agpat_...'
```

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `Incompatible auth server: does not support dynamic client registration` | Forgot `oauth.client_id` in config | Add `[mcp_servers.agentserver.oauth] client_id = "agentserver-mcp"` |
| `audience mismatch` in gateway logs | Forgot `oauth_resource` in config | Add `oauth_resource = "https://mcp.agent.cs.ac.cn/v1/mcp"` |
| Browser opens but never returns | Network blocks codex's callback port | Set `mcp_oauth_callback_port = 8765` (or any reachable port) in config |
| `401 unauthorized` after successful login | Token expired or workspace membership lost | Re-run `codex mcp login agentserver` |
| `tools/call ... not granted to this principal` | Granted only `mcp:read` on consent | Re-login, grant both scopes |
| `no environment named X` | Executor name not in `list_environments` | Run `list_environments` first; copy a `name` verbatim |

## Related

- [Claude Code CLI](./claude-code-cli.md) — same gateway, different client
- [Claude Desktop (3P / Developer Mode)](./claude-desktop-3p.md)
- Spec: `docs/superpowers/specs/2026-06-09-envmcp-public-gateway-design.md` + `2026-06-17-mcp-oauth-design.md`
