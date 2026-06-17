# Claude Code CLI integration

Run your agentserver workspaces' tools (`shell`, `read_file`, `apply_patch`, …) from inside Claude Code, against any executor you've registered.

## Prerequisites

- A workspace + at least one registered executor in agentserver
- Claude Code CLI (any recent version supports remote MCP via OAuth)

## Recommended: OAuth (zero-config)

```
claude mcp add --transport http agentserver https://mcp.agent.cs.ac.cn/v1/mcp
```

The first time you start a `claude` session, the CLI sees a 401 from the MCP endpoint, follows the OAuth discovery doc, and opens a browser. Log in, pick the workspace, click "Allow", and the token gets stored in your Claude Code config + silently refreshes.

Inside a session, run `/mcp` to confirm `agentserver` is connected. Tools appear as `mcp__agentserver__shell`, `mcp__agentserver__read_file`, etc.

To re-authorize against a different workspace, remove and re-add:

```
claude mcp remove agentserver
claude mcp add --transport http agentserver https://mcp.agent.cs.ac.cn/v1/mcp
```

Then start a fresh session — the consent screen pops up again.

## Manual config (if you prefer)

`~/.claude/settings.json`:

```json
{
  "mcpServers": {
    "agentserver": {
      "type": "http",
      "url": "https://mcp.agent.cs.ac.cn/v1/mcp"
    }
  }
}
```

OAuth flow triggers on first connect; no headers/tokens needed in the JSON.

## Service-account / static token alternative

If you can't do an interactive browser flow (CI, headless containers, dev images shared between people), see the [Codex CLI doc's PAT section](./codex-cli.md#service-account--ci-alternative-personal-access-tokens) for how to mint a long-lived PAT, then wire it as a header:

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

## Verify

```bash
TOKEN=$(jq -r .access_token ~/.claude/mcp/agentserver.json 2>/dev/null) \
  || TOKEN='agpat_...'
curl -s -X POST https://mcp.agent.cs.ac.cn/v1/mcp \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | jq
```

## Related

- [Codex CLI](./codex-cli.md) — same gateway, different client
- [Claude Desktop (3P / Developer Mode)](./claude-desktop-3p.md) — Claude Desktop via mcp-remote
- Spec: `docs/superpowers/specs/2026-06-09-envmcp-public-gateway-design.md` + `2026-06-17-mcp-oauth-design.md`
