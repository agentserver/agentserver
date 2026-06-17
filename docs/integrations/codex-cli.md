# Codex CLI integration

Run your agentserver workspaces' tools (`shell`, `read_file`, `apply_patch`, …) from inside Codex CLI, against any executor you've registered.

## Prerequisites

- A workspace + at least one registered executor in agentserver (Settings → Executors)
- Codex CLI ≥ 0.140 (any version with `codex mcp login` support)

## Recommended: OAuth (zero-config)

In `~/.codex/config.toml`:

```toml
[mcp_servers.agentserver]
url = "https://mcp.agent.cs.ac.cn/v1/mcp"
```

Then once per machine:

```
codex mcp login agentserver
```

A browser opens, you log into agentserver, pick the workspace you want this codex install to act on, click "Allow read + exec". The token gets stored in `~/.codex` and silently refreshes on every use. No environment variables, no curl, no manual token management.

The browser shows a consent screen that names the two scopes — `mcp:read` (list executors, read files) and `mcp:exec` (shell access). `mcp:exec` is rendered as a warning row; only grant it to clients you trust.

To re-authorize against a different workspace, run `codex mcp logout agentserver` then `codex mcp login agentserver` again — you'll get the workspace picker on the next consent screen.

## Verify

In a Codex session:

```
> please list the environments I have access to
```

The LLM should call `agentserver_list_environments` and print your workspace's executors.

Direct curl smoke test (skips the LLM):

```bash
# Token is stored at ~/.codex/mcp_oauth/<server>.json
TOKEN=$(jq -r .access_token ~/.codex/mcp_oauth/agentserver.json)
curl -s -X POST https://mcp.agent.cs.ac.cn/v1/mcp \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | jq
```

You should see your 9 tool definitions in the response.

## Multi-workspace setup

For each workspace you want to reach, add a separate `[mcp_servers.X]` entry with a unique server name:

```toml
[mcp_servers.work]
url = "https://mcp.agent.cs.ac.cn/v1/mcp"

[mcp_servers.personal]
url = "https://mcp.agent.cs.ac.cn/v1/mcp"
```

```bash
codex mcp login work        # consent screen → pick "Work" workspace
codex mcp login personal    # consent screen → pick "Personal" workspace
```

Codex auto-prefixes tool names by server: the LLM sees `work_shell`, `personal_shell`, etc. — visually distinct, no ambiguity even if both workspaces happen to have an executor named `laptop`.

## Revoke

From any browser logged into agentserver:

```
Settings → Authorized Apps → revoke the entry whose client_id matches your codex install
```

The gateway stops accepting the token within ≤10s (Hydra revocation + envmcp-public-gateway introspects every request, no token cache).

On the codex side: `codex mcp logout agentserver` clears the local copy.

## Service-account / CI alternative: Personal Access Tokens

For service accounts or CI environments where browser OAuth doesn't fit, mint a long-lived PAT via the REST API (workspace `owner` or `maintainer` role required):

```bash
WID=ws_yourworkspaceid
curl -X POST "https://agent.cs.ac.cn/api/workspaces/${WID}/mcp/pats" \
  -H "Cookie: agentserver_session=..." \
  -H "Content-Type: application/json" \
  -d '{"name":"ci-pipeline","scopes":["mcp:read","mcp:exec"],"expires_at":"2026-09-09T00:00:00Z"}'
```

The response includes a `secret` (shown once) — wire it into your CI as an env var and configure Codex with:

```toml
[mcp_servers.agentserver]
url = "https://mcp.agent.cs.ac.cn/v1/mcp"
bearer_token_env_var = "AGENTSERVER_PAT"
```

```bash
export AGENTSERVER_PAT='agpat_...'
```

PATs are workspace-scoped (one PAT = one workspace) and revoked the same way as OAuth tokens.

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `codex mcp login` opens browser, browser hangs | Network blocked between codex callback port and the browser | Configure `mcp_oauth_callback_port` to a port your browser can reach |
| `Dynamic client registration not supported` | Hit a different MCP server, not ours — agentserver supports DCR | Confirm the URL is `mcp.agent.cs.ac.cn` |
| `401 unauthorized` on every request | Token revoked, expired, or workspace membership lost | Re-run `codex mcp login agentserver` |
| `tools/call ... not granted to this principal` | Token lacks the `mcp:exec` scope | Re-login and grant both scopes on the consent screen |
| `no environment named X` | Executor name not in `list_environments` output | Run `list_environments` first; copy a `name` value verbatim |
| `bridge dial timed out` | Executor offline or unreachable from the gateway | Check `Settings → Executors` — `last_seen` should be recent |

## Related

- [Claude Code CLI](./claude-code-cli.md) — same gateway, different client
- [Claude Desktop (3P / Developer Mode)](./claude-desktop-3p.md) — Claude Desktop via mcp-remote
- Spec: `docs/superpowers/specs/2026-06-09-envmcp-public-gateway-design.md` + `2026-06-17-mcp-oauth-design.md`
