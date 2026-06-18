# Claude Desktop integration

Claude Desktop has two paths for connecting to remote MCP servers like ours. **Pick one.**

| Path | OAuth callback target | Token stored | Notes |
|---|---|---|---|
| **A. Native Custom Connector** | `https://claude.ai/api/mcp/auth_callback` (Anthropic cloud) | Anthropic cloud | Simplest. Settings → Connectors UI. All MCP traffic + tool calls go through Anthropic's servers. |
| **B. `mcp-remote` stdio bridge** | `http://localhost:20202/oauth/callback` (your machine) | `~/.mcp-auth/` (your machine) | Token never leaves your laptop. Needs Node.js + `npx`. |

Path A is officially supported by Anthropic since 2026; Path B is the community workaround.

---

## Path A — Native Custom Connector

**Status: not enabled on agentserver yet.** Path A requires us to register `https://claude.ai/api/mcp/auth_callback` (and `https://claude.com/api/mcp/auth_callback`) as redirect URIs on the `mcp-claude-desktop` Hydra client. That's a one-line helm change we haven't done because the security implications (Anthropic cloud holds your tokens + initiates all MCP traffic from cloud IPs) need an explicit go-ahead. If you want Path A enabled, ping the agentserver maintainer.

After enablement, the flow is:
- Settings → Connectors → "Add custom connector"
- URL: `https://mcp.agent.cs.ac.cn/mcp`
- Advanced settings → OAuth Client ID: `mcp-claude-desktop`
- OAuth Client Secret: **leave empty** (public client, PKCE)
- Click "Connect" — browser does OAuth, picks workspace, grants scopes.

---

## Path B — mcp-remote (OAuth, recommended for local-token storage)

### Prerequisites

- Node.js 18+ installed (for `npx`)
- A workspace on agentserver

### Config

Edit `claude_desktop_config.json` (on macOS: `~/Library/Application Support/Claude/claude_desktop_config.json`; on Windows: `%APPDATA%\Claude\claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "agentserver": {
      "command": "npx",
      "args": [
        "-y",
        "mcp-remote",
        "https://mcp.agent.cs.ac.cn/mcp",
        "20202",
        "--static-oauth-client-info",
        "{\"client_id\":\"mcp-claude-desktop\"}",
        "--transport",
        "http-only"
      ]
    }
  }
}
```

What each arg does:

| Arg | Purpose |
|---|---|
| `-y` | npx auto-accept install prompt |
| `mcp-remote` | npm package name |
| `https://mcp.agent.cs.ac.cn/mcp` | remote MCP server URL |
| `20202` | **positional**: local OAuth callback port (mcp-remote default is 3334; we override because Hydra registers exactly 20202) |
| `--static-oauth-client-info '{"client_id":"mcp-claude-desktop"}'` | use the pre-registered Hydra client; skip DCR |
| `--transport http-only` | server speaks Streamable HTTP, not SSE |

**Completely quit Claude Desktop** (Cmd-Q on macOS, system-tray Quit on Windows — closing the window isn't enough) and reopen. On the next chat, mcp-remote starts up, gets a 401 from `mcp.agent.cs.ac.cn/mcp`, and opens a browser to `https://agent.cs.ac.cn/oauth2/auth?...`. Log in if needed, pick the workspace, click **Allow**. Browser jumps to `localhost:20202/oauth/callback`, mcp-remote stores the token under `~/.mcp-auth/<server-hash>/`, and Claude Desktop's tool palette gets `agentserver_shell`, `agentserver_read_file`, etc. populated.

### Verify

Look for the hammer icon in the Claude Desktop chat UI — it should list 9 `agentserver_*` tools. Or:

```
请列出我能访问的 environments
```

Or curl smoke test (skips Claude):

```bash
# mcp-remote stores token in ~/.mcp-auth/<server-hash>/tokens.json
TOKEN=$(jq -r .access_token ~/.mcp-auth/*/tokens.json | head -1)
curl -s -X POST https://mcp.agent.cs.ac.cn/mcp \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | jq '.result.tools | length'
```

Expected: `9`.

### Multi-workspace

Each `mcpServers` entry can target a different workspace by going through OAuth separately. Each gets its own `~/.mcp-auth/<server-hash>/` directory keyed off the URL + a unique session label:

```json
{
  "mcpServers": {
    "agentserver-work": {
      "command": "npx",
      "args": ["-y", "mcp-remote", "https://mcp.agent.cs.ac.cn/mcp", "20202",
               "--static-oauth-client-info", "{\"client_id\":\"mcp-claude-desktop\"}",
               "--transport", "http-only",
               "--resource", "work"]
    },
    "agentserver-personal": {
      "command": "npx",
      "args": ["-y", "mcp-remote", "https://mcp.agent.cs.ac.cn/mcp", "20202",
               "--static-oauth-client-info", "{\"client_id\":\"mcp-claude-desktop\"}",
               "--transport", "http-only",
               "--resource", "personal"]
    }
  }
}
```

`--resource <label>` isolates the OAuth session so each entry triggers its own consent screen → its own workspace pick.

### Re-OAuth (changed workspaces, expired tokens)

```bash
rm -rf ~/.mcp-auth/
```

Then completely quit + reopen Claude Desktop. Each `mcpServers` entry will browser-flow again.

### Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| No tools in hammer icon | mcp-remote isn't starting | `tail -f ~/Library/Logs/Claude/mcp*.log` (macOS) for the actual error |
| `Dynamic client registration not supported` | Forgot `--static-oauth-client-info` | check the args array |
| `invalid_redirect_uri` | the positional port arg isn't `20202`, or you omitted it | The second arg after the URL must be `"20202"` (as a string in JSON) |
| `EADDRINUSE: 20202` | Another process owns port 20202 (e.g., Codex CLI running `codex mcp login` at the same time) | Kill the offender (`lsof -ti:20202 | xargs kill`) or close that other client |
| Browser doesn't open | Sandboxed Claude Desktop can't spawn browser | Look in mcp log for the OAuth URL — copy-paste it manually into a browser |
| `npx: command not found` | No Node.js | Install Node 18+ from nodejs.org |

---

## PAT fallback (no browser, CI, automation)

If browser OAuth doesn't fit your scenario, mint a long-lived PAT and stuff it in a header. See [Codex CLI doc § Service-account](./codex-cli.md#service-account--ci-alternative-personal-access-tokens) for how to mint a PAT, then:

```json
{
  "mcpServers": {
    "agentserver": {
      "command": "npx",
      "args": [
        "-y", "mcp-remote",
        "https://mcp.agent.cs.ac.cn/mcp",
        "--transport", "http-only",
        "--header", "Authorization:${AGENTSERVER_PAT}"
      ],
      "env": {
        "AGENTSERVER_PAT": "Bearer agpat_..."
      }
    }
  }
}
```

The colon-no-space `Authorization:${AGENTSERVER_PAT}` syntax dodges a Windows env-var expansion bug in mcp-remote (the space in `Bearer xxx` gets swallowed). Putting `Bearer ` inside the env var value works around it.

---

## Related

- [Codex CLI](./codex-cli.md) — `mcp-codex` client, callback at `/callback`
- [Claude Code CLI](./claude-code-cli.md) — `mcp-claude-code` client, also `/callback` path
- Spec: `docs/superpowers/specs/2026-06-17-mcp-oauth-design.md`
