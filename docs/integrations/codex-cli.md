# Codex CLI integration

Run your agentserver workspaces' tools (`shell`, `read_file`, `apply_patch`, …) from inside Codex CLI, against any executor you've registered.

## Prerequisites

- A workspace + at least one registered executor in agentserver (Settings → Executors)
- Codex CLI ≥ 0.137 (any version with `bearer_token_env_var` support for `[mcp_servers]`)

## Step 1 — Mint a Personal Access Token

PATs are **workspace-scoped** — one PAT covers exactly one workspace. If you want Codex to reach several workspaces, mint one PAT per workspace (see "Multi-workspace setup" below).

Today the only PAT management UI is the REST API. Mint via your session cookie or a long-lived workspace API key:

```bash
# Via session cookie (paste from browser DevTools → agentserver origin)
WID=ws_yourworkspaceid
curl -X POST "https://app.agent.cs.ac.cn/api/workspaces/${WID}/mcp/pats" \
  -H "Cookie: agentserver_session=..." \
  -H "Content-Type: application/json" \
  -d '{"name":"my-laptop-codex","scopes":["mcp:read","mcp:exec"],"expires_at":"2026-09-09T00:00:00Z"}'
```

Response (the `secret` is shown **once** — store it now):

```json
{
  "id": "agpat_a1b2c3d4e5f6g7h8",
  "name": "my-laptop-codex",
  "prefix": "agpat_a1b2c3d4e5f6g7h8",
  "workspace_id": "ws_yourworkspaceid",
  "secret": "agpat_a1b2c3d4e5f6g7h8_X9y8Z7w6V5u4T3s2R1q0P9o8N7m6L5k4J3i2H1g0F9e8...",
  "scopes": ["mcp:read","mcp:exec"],
  "created_at": "2026-06-15T08:30:00Z",
  "expires_at": "2026-09-09T00:00:00Z"
}
```

### Scopes

| Scope | Tools granted | Notes |
|---|---|---|
| `mcp:read` | `list_environments`, `read_file` | Side-effect-free |
| `mcp:exec` | `shell`, `exec_command`, `write_stdin`, `read_output`, `terminate`, `apply_patch`, `copy_path` | **Full shell access on registered executors** — opt in explicitly |

Default to `mcp:read` and only add `mcp:exec` when the agent actually needs to mutate state.

### Workspace-level role required to mint

You need `owner` or `maintainer` on the workspace. `developer` / `viewer` get a 403 — minting a PAT with `mcp:exec` is equivalent to handing out shell, so it follows the same admin gate as workspace API keys.

## Step 2 — Add to `~/.codex/config.toml`

```toml
[mcp_servers.agentserver]
url = "https://mcp.agent.cs.ac.cn/v1/mcp"
bearer_token_env_var = "AGENTSERVER_PAT"
```

```bash
export AGENTSERVER_PAT='agpat_a1b2c3d4e5f6g7h8_X9y8Z7w6V5u4T3s2R1q0P9o8N7m6L5k4J3i2H1g0F9e8...'
```

That's it. Next time you start Codex CLI, you'll see `agentserver_shell`, `agentserver_read_file`, `agentserver_list_environments`, etc. in the tool palette.

## Step 3 — Verify

In a Codex session:

```
> please list the environments I have access to
```

The LLM should call `agentserver_list_environments` and print your workspace's executors.

Direct curl smoke test (skips the LLM):

```bash
curl -s -X POST https://mcp.agent.cs.ac.cn/v1/mcp \
  -H "Authorization: Bearer $AGENTSERVER_PAT" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | jq
```

You should see your 9 tool definitions in the response.

## Multi-workspace setup

For each workspace, mint a PAT (Step 1) and add a separate `[mcp_servers.X]` entry where `X` is a short name you choose:

```toml
[mcp_servers.work]
url = "https://mcp.agent.cs.ac.cn/v1/mcp"
bearer_token_env_var = "AGENTSERVER_PAT_WORK"

[mcp_servers.personal]
url = "https://mcp.agent.cs.ac.cn/v1/mcp"
bearer_token_env_var = "AGENTSERVER_PAT_PERSONAL"
```

```bash
export AGENTSERVER_PAT_WORK=agpat_...
export AGENTSERVER_PAT_PERSONAL=agpat_...
```

Codex auto-prefixes tool names by server: the LLM sees `work_shell`, `personal_shell`, etc. — visually distinct, no ambiguity even if both workspaces happen to have an executor named `laptop`.

## Revoke

Same workspace, same admin role:

```bash
curl -X DELETE "https://app.agent.cs.ac.cn/api/workspaces/${WID}/mcp/pats/agpat_a1b2c3d4e5f6g7h8" \
  -H "Cookie: agentserver_session=..."
```

A revoked PAT stops working within ~one cap-token TTL window (≤10 min) on the gateway side.

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `401 unauthorized` on every request | Wrong PAT, expired, revoked, or you were removed from the workspace | Re-mint; verify with the curl smoke test above |
| `tools/call ... not granted to this principal` | PAT lacks the `mcp:exec` scope | Re-mint with `["mcp:read","mcp:exec"]` |
| `no environment named X` | The executor name isn't in `list_environments` output | Run `list_environments` first; copy a `name` value verbatim into your prompt |
| `bridge dial timed out` | Executor offline or unreachable from the gateway | Check `Settings → Executors` — `last_seen` should be recent |

## Related

- [Claude Desktop (3P / Developer Mode)](./claude-desktop-3p.md) — same gateway, different client
- Spec: `docs/superpowers/specs/2026-06-09-envmcp-public-gateway-design.md` (with 2026-06-15 amendment)
