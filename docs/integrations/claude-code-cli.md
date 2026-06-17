# Claude Code CLI integration

Run your agentserver workspaces' tools (`shell`, `read_file`, `apply_patch`, …) from inside Claude Code, against any executor you've registered.

## Prerequisites

- A workspace + at least one registered executor in agentserver
- Claude Code CLI ≥ 2.1.30 (`--client-id` flag support)
- An agentserver login

## Add the MCP server

```bash
claude mcp add --transport http agentserver \
  https://mcp.agent.cs.ac.cn/v1/mcp \
  --client-id agentserver-mcp-shared \
  --callback-port 3000
```

Notes:
- `client_id = "agentserver-mcp-shared"` is a fixed, public value shared by all users (same shape as gh CLI's hard-coded GitHub OAuth client). It has no auth power on its own — the OAuth flow's user login + workspace consent screen is what authorizes.
- `--callback-port` is required: Claude Code binds the OAuth callback on this exact port. Any free port works (we register loopback host-only with Hydra, so any port is accepted per RFC 8252).
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
      "url": "https://mcp.agent.cs.ac.cn/v1/mcp",
      "oauth": {
        "client_id": "agentserver-mcp-shared",
        "callback_port": 3000
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
      "url": "https://mcp.agent.cs.ac.cn/v1/mcp",
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
| `invalid_redirect_uri` | `--callback-port` didn't match what Claude Code actually bound; pick any free port and retry |
| Token works once then fails | Check gateway logs for `audience mismatch` — Claude Code currently doesn't pass `oauth_resource`. Tracked in follow-up. |

## Related

- [Codex CLI](./codex-cli.md) — same gateway, different client
- [Claude Desktop (3P)](./claude-desktop-3p.md)
