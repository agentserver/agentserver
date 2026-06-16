# Claude Desktop (3P / Developer Mode) integration

If you run Claude Desktop in "Cowork on 3P" / Developer Mode, the Custom Connectors UI is **disabled** (that's a 1P-only feature, requires OAuth which the gateway will support in Phase 2). The official workaround is to bridge a remote MCP endpoint into a local stdio MCP server via `mcp-remote`.

## Prerequisites

- Claude Desktop in Developer / 3P mode
- Node.js installed (for `npx`)
- An agentserver PAT — see [the codex-cli guide](./codex-cli.md#step-1--mint-a-personal-access-token) for how to mint one

## Configure

Edit `claude_desktop_config.json` (on macOS: `~/Library/Application Support/Claude/claude_desktop_config.json`; on Windows: `%APPDATA%\Claude\claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "agentserver": {
      "command": "npx",
      "args": [
        "mcp-remote",
        "https://mcp.agent.cs.ac.cn/v1/mcp",
        "--header",
        "Authorization:${AGENTSERVER_PAT}"
      ],
      "env": {
        "AGENTSERVER_PAT": "Bearer agpat_a1b2c3d4e5f6g7h8_X9y8Z7w6V5u4T3s2R1q0P9o8N7m6L5k4J3i2H1g0F9e8..."
      }
    }
  }
}
```

> **Note** the `Authorization:${AGENTSERVER_PAT}` (no space). Some `mcp-remote` versions on Windows / Cursor mangle the space in `Authorization: Bearer …` when expanding the env var — keeping the `Bearer ` prefix inside the env var value and joining with `:` (no space) sidesteps that.

Restart Claude Desktop. The next time you start a conversation, the tools `agentserver_shell`, `agentserver_read_file`, etc. should be available.

## Verify

Same direct-curl smoke test as the Codex guide:

```bash
curl -s -X POST https://mcp.agent.cs.ac.cn/v1/mcp \
  -H "Authorization: Bearer $(echo "$AGENTSERVER_PAT" | sed 's/^Bearer //')" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | jq
```

If that returns your 9 tool definitions but Claude Desktop says "tools not available", check Claude Desktop's logs (`~/Library/Logs/Claude/mcp.log` on macOS) for the `mcp-remote` subprocess output.

## Multi-workspace setup

One PAT per workspace, one entry per workspace:

```json
{
  "mcpServers": {
    "work": {
      "command": "npx",
      "args": ["mcp-remote", "https://mcp.agent.cs.ac.cn/v1/mcp", "--header", "Authorization:${AGENTSERVER_PAT_WORK}"],
      "env": { "AGENTSERVER_PAT_WORK": "Bearer agpat_work_…" }
    },
    "personal": {
      "command": "npx",
      "args": ["mcp-remote", "https://mcp.agent.cs.ac.cn/v1/mcp", "--header", "Authorization:${AGENTSERVER_PAT_PERSONAL}"],
      "env": { "AGENTSERVER_PAT_PERSONAL": "Bearer agpat_personal_…" }
    }
  }
}
```

Claude Desktop prefixes tool names by server entry, same as Codex (`work_shell`, `personal_shell`, …) — no name collisions even if both workspaces share an executor name.

## Why this isn't via Custom Connectors

Custom Connectors is 1P-only and expects an OAuth 2.1 + DCR flow. The gateway will support it in Phase 2 (Hydra-backed `/oauth/authorize` + `/oauth/token` + `.well-known/oauth-authorization-server`). Until then, 3P + `mcp-remote` is the supported path.

## Related

- [Codex CLI](./codex-cli.md) — same gateway from a different client
- Spec: `docs/superpowers/specs/2026-06-09-envmcp-public-gateway-design.md`
