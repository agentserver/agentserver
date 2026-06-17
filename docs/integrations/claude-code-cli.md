# Claude Code CLI integration

Run your agentserver workspaces' tools (`shell`, `read_file`, `apply_patch`, …) from inside Claude Code, against any executor you've registered.

## Prerequisites

- A workspace + at least one registered executor in agentserver
- Claude Code CLI ≥ 2.1.30 (`--client-id` flag support)
- An agentserver login

## Step 1 — Create a static OAuth client

Claude Code's MCP OAuth requires a pre-registered `client_id`. Mint one via the agentserver REST API:

```bash
curl -s -X POST "https://agent.cs.ac.cn/api/me/oauth-clients" \
  -H "Cookie: agentserver_session=..." \
  -H "Content-Type: application/json" \
  -d '{"name":"my-laptop-claude"}'
```

Returns `{"client_id": "df66ecfa-25ad-404c-b364-1d94ca7f986c", ...}`. Copy the `client_id`.

## Step 2 — Add the MCP server

```bash
claude mcp add --transport http agentserver \
  https://mcp.agent.cs.ac.cn/v1/mcp \
  --client-id df66ecfa-25ad-404c-b364-1d94ca7f986c \
  --callback-port 3000
```

Notes:
- `--callback-port` is required — Claude Code binds the OAuth callback at that exact port. Any free port works (we register loopback host-only with Hydra, so any port is accepted per RFC 8252).
- No `--client-secret`: every client minted from step 1 is a **public** OAuth client (PKCE-protected).

## Step 3 — First connect triggers OAuth

Start a `claude` session:

```
claude
```

The first time it tries to talk to `agentserver`, a browser opens to agentserver. Log in, pick a workspace, click **Allow**. Token is cached in Claude Code's config + silently refreshes.

`/mcp` inside the session should show `agentserver: connected`. Tools appear as `mcp__agentserver__shell`, `mcp__agentserver__read_file`, etc.

## Manual config (alternative to `claude mcp add`)

`~/.claude/settings.json`:

```json
{
  "mcpServers": {
    "agentserver": {
      "type": "http",
      "url": "https://mcp.agent.cs.ac.cn/v1/mcp",
      "oauth": {
        "client_id": "df66ecfa-25ad-404c-b364-1d94ca7f986c",
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

Same DELETE endpoint as Codex CLI doc. Revocation takes effect within ≤10s.

## Troubleshooting

| Symptom | Fix |
|---|---|
| `Incompatible auth server: does not support dynamic client registration` | You forgot `--client-id`; re-add with the flag |
| `invalid_redirect_uri` | `--callback-port` mismatched what Claude Code actually bound; pick any free port and retry |
| Token works once then fails | Check gateway logs for `audience mismatch` — Claude Code currently doesn't pass `oauth_resource`. If hit, this is a known issue; tracked in our follow-up |

## Related

- [Codex CLI](./codex-cli.md) — same gateway, different client
- [Claude Desktop (3P)](./claude-desktop-3p.md)
