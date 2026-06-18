# Claude Code CLI integration

Run your agentserver workspaces' tools (`shell`, `read_file`, `apply_patch`, …) from inside Claude Code, against any executor you've registered.

## Prerequisites

- A workspace + at least one registered executor in agentserver
- Claude Code CLI ≥ 2.1.30 (`--client-id` flag support)
- An agentserver login

## Add the MCP server

```bash
claude mcp add --transport http agentserver \
  https://mcp.agent.cs.ac.cn/mcp \
  --client-id mcp-claude-code \
  --callback-port 20202
```

Notes:
- `client_id = "mcp-claude-code"` is a fixed, public value shared by all users (same shape as gh CLI's hard-coded GitHub OAuth client). It has no auth power on its own — the OAuth flow's user login + workspace consent screen is what authorizes.
- `--callback-port 20202` is required and the port number is **not arbitrary** — Hydra v2 doesn't implement RFC 8252 §7.3 (loopback-any-port), so the server only accepts callbacks on the exact port registered with the OAuth client. We registered port 20202 (rare enough to dodge dev-server collisions on 3000/8080/5173/etc.). If 20202 is taken on your machine, file an issue.
- No `--client-secret`: the shared client is a **public** OAuth client (PKCE-protected).

## First connect triggers OAuth

```
claude
```

The first time it talks to `agentserver`, a browser opens. Log in to agentserver, pick a workspace, click **Allow**. Token cached + silently refreshed.

`/mcp` inside the session should show `agentserver: connected`. Tools appear as `mcp__agentserver__shell`, `mcp__agentserver__read_file`, etc.

## Manual config

`~/.claude/settings.json`:

```json
{
  "mcpServers": {
    "agentserver": {
      "type": "http",
      "url": "https://mcp.agent.cs.ac.cn/mcp",
      "oauth": {
        "client_id": "mcp-claude-code",
        "callback_port": 20202
      }
    }
  }
}
```

## Service-account / static token alternative

For CI / headless environments, use a PAT instead of OAuth (see [Codex CLI doc § Service-account](./codex-cli.md#service-account--ci-alternative-personal-access-tokens) for how to mint one). Then configure:

```json
{
  "mcpServers": {
    "agentserver": {
      "type": "http",
      "url": "https://mcp.agent.cs.ac.cn/mcp",
      "headers": { "Authorization": "Bearer agpat_..." }
    }
  }
}
```

## Revoke

Local clear: `claude mcp remove agentserver`. For workspace-membership-based revocation (most common), removing the user from the workspace causes the gateway to reject within ≤10s on every subsequent request.

## Troubleshooting

| Symptom | Fix |
|---|---|
| `Incompatible auth server: does not support dynamic client registration` | Forgot `--client-id`; re-add with the flag |
| `invalid_redirect_uri` | `--callback-port` must be exactly 20202 (Hydra registers that one port, not any-loopback). |
| Token works once then fails | Check gateway logs for `audience mismatch` — Claude Code currently doesn't pass `oauth_resource`. Tracked in follow-up. |

## Related

- [Codex CLI](./codex-cli.md) — same gateway, different client
- [Claude Desktop (3P)](./claude-desktop-3p.md)
