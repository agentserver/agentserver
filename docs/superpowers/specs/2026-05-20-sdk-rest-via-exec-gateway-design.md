# Agentserver SDK → REST via codex-exec-gateway — Design

**Date**: 2026-05-20
**Status**: approved
**Author**: mryao (with Claude)
**Replaces**: the jupyter sandbox SDK transport that tunnels every call through the codex app-server WS protocol + env-mcp subprocess.

## Goal

Make the agentserver Python SDK talk **HTTP REST directly to codex-exec-gateway**. Codex is no longer in the path for `envs/list`, `shell`, `read_file`, `apply_patch`, `exec_command`, etc. — these are agentserver's own capabilities and never needed an LLM-aware transport.

## Why

The current path is `Jupyter (SDK) → ws → codex-app-gateway → spawn codex subprocess → mcpServer/tool/call → env-mcp subprocess → exec-gateway /bridge → executor`. Codex's only role is to forward the JSON-RPC; env-mcp is a child it spawns purely to host the tool implementations. For LLM-free RPCs (the entire SDK surface), routing through codex's WS framework is gratuitous and brings real costs:

- Codex subprocess spawned per WS connection (memory, latency, blast radius).
- Two extra hops (codex → env-mcp child → exec-gateway) for every shell command.
- SDK protocol coupled to codex's RPC method names — already cost us debugging in v0.60.7 (HMAC auth bolted onto `/notebook/ws`) and v0.60.13 (SDK had to adapt to codex 0.130 `thread/start` response shape change).
- env-mcp's "list_envs" tool is reimplemented client-side by the SDK as `envs/list` — a method codex itself doesn't know, requiring a gateway interceptor that never got built.

The fix is to stop using codex as a transport for non-LLM operations. The SDK calls codex-exec-gateway (where the bridge to executors already lives) over plain HTTP, with sandbox proxyToken as Bearer auth.

## Non-goals

- Touching the codex `--remote` TUI path. codex-app-gateway and its `/codex-app/ws` endpoint, codex subprocess spawning, and env-mcp's role as the MCP server for that path all stay as-is.
- Renaming `codex-exec-gateway` to drop the `codex-` prefix even though it now serves general SDK traffic. Defer to a separate PR.
- Streaming process I/O (true WS-bidirectional stdin/stdout). Polling REST matches the SDK's existing semantics and is simpler.
- Backwards compatibility for the old SDK. Jupyter image bundles the SDK at build time; every release rebuilds the image. No external PyPI consumers.

## Architecture

```
Jupyter sandbox (agentserver-sdk)
        │  HTTPS Bearer=<proxyToken>
        ▼
codex-exec-gateway  ── new router group /api/sdk/* ──┐
        │                                             │
        │ ProxyTokenAuth.Verify(token):               │
        │   LRU cache (5m TTL, 30s neg cache)         │
        │   miss → POST agentserver                   │
        │   /internal/validate-proxy-token            │
        │   → {workspace_id, user_id}                 │
        │                                             │
        ├─ /api/sdk/envs/list                         │
        │   ↳ store.Connected(ctx, wsID)              │
        │     (reuses existing /api/exec-gateway/connected handler logic)
        │                                             │
        └─ /api/sdk/envs/{name}/tool/call             │
           ├─ nameresolver: name → exe_id             │
           ├─ envtools.Registry[tool].Call(args, br)  │
           └─ bridge.Pool.Get(exe_id)                 │
                  ↳ existing /bridge/{exe_id} WS ─→ executor
        │
        ├─ /api/sdk/processes/{sid}/stdin
        ├─ /api/sdk/processes/{sid}/output?since=N
        └─ /api/sdk/processes/{sid}/terminate
              ↳ SessionManager owns long-lived bridge handle + ring buffer
```

`codex-app-gateway` keeps its `/codex-app/ws` and codex subprocess machinery; only `handleNotebookWS` is deleted. env-mcp tool implementations are extracted to a shared `internal/envtools/` package so both codex-app-gateway (via the MCP server adapter) and codex-exec-gateway (via REST handlers) call the same Go code.

## Components

### New: `internal/envtools/` (extracted from `internal/codexappgateway/envmcp/`)

```
internal/envtools/
├── tools/
│   ├── tool.go              # interface Tool { Name(); Call(ctx, args, bridge) (Result, error) }
│   ├── shell.go
│   ├── read_file.go
│   ├── write_file.go
│   ├── apply_patch.go
│   ├── copy_path.go
│   ├── exec_command.go
│   └── registry.go          # var Registry = map[string]Tool{...}
├── bridge/
│   ├── bridge.go            # was envmcp/bridge.go
│   └── pool.go              # was envmcp/pool.go
├── nameresolver/
│   └── resolver.go          # was envmcp/name_resolver.go
├── processes/
│   ├── session.go           # ring buffer, seq counter, terminate
│   └── manager.go           # sid → *Session, idle GC (30min)
└── mcpadapter/
    └── server.go            # was envmcp/mcp_server.go — MCP server registration used by codex-app-gateway only
```

The bridge and tool packages are pure libraries. The mcpadapter is the only piece codex-specific to the MCP protocol shape.

### New: `internal/codexexecgateway/sdk/`

```
internal/codexexecgateway/sdk/
├── auth.go        # ProxyTokenAuth: HTTP client to agentserver, LRU cache
├── server.go      # Server{auth, pool, resolver, sessions, registry}; Mount(r chi.Router)
├── handlers.go    # 5 handler funcs
└── handlers_test.go
```

### REST endpoints

All under `/api/sdk/*`. Bearer is sandbox proxyToken; middleware injects `workspace_id` into request context.

```
POST /api/sdk/envs/list
  req:  {}
  resp: {"envs":[{"name":"my-mac","type":"executor","is_default":true,
                  "tools":[{"name":"shell","kind":"core","description":"…"}, ...],
                  "last_seen":"2026-05-19T08:00:00Z"}]}

POST /api/sdk/envs/{name}/tool/call
  req:  {"tool":"shell","arguments":{"command":"ls"}}
  resp: {"isError":false,
         "content":[{"type":"text","text":"file1\nfile2\n"}],
         "structuredContent":{...optional...}}
  (covers shell/read_file/write_file/apply_patch/copy_path/exec_command;
   exec_command's structuredContent carries session_id)

POST /api/sdk/processes/{sid}/stdin
  req:  {"data_b64":"…"}
  resp: {"ok":true}

GET  /api/sdk/processes/{sid}/output?since=N
  resp: {"chunks":[{"stream":"stdout"|"stderr","data_b64":"…","seq":N+1},…],
         "exit_code":null|<int>,
         "session_alive":true|false,
         "truncated":false,
         "lost_bytes":0}

POST /api/sdk/processes/{sid}/terminate
  req:  {}
  resp: {"ok":true}
```

**Error envelope** for transport failures (auth, env-not-found, bridge-down): `{"error":{"code":"…","message":"…"}}` + appropriate HTTP status (400/401/403/404/500/502). Tool-level failures (a `shell` exits non-zero, an `apply_patch` patch is malformed) come back as `200 + isError:true + content` — this matches MCP convention and lets the SDK raise `ToolError` cleanly without conflating with transport errors.

### Auth flow

1. SDK sends `Authorization: Bearer <sandbox proxyToken>` on every request.
2. `ProxyTokenAuth.Verify` checks LRU cache (`token → (workspace_id, expires_at, negative?)`). TTL 5 min for positive, 30 s for negative.
3. Cache miss → `POST {agentserver}/internal/validate-proxy-token` with `X-Internal-Secret: <INTERNAL_API_SECRET>` (same pattern llmproxy uses today).
   - 200 + `{workspace_id, user_id}` → cache, continue.
   - 401 → cache negative, return 401 to caller.
4. Middleware injects `workspace_id` into `request.Context` so handlers don't re-extract.
5. Every read/write enforces workspace isolation: `nameresolver.Resolve(wsID, name)` only returns exe_ids bound to that workspace; session lookups verify `session.WorkspaceID == wsID` or 403.

### Session manager (`internal/envtools/processes/`)

```go
type Session struct {
    ID           string
    WorkspaceID  string
    bridge       bridge.Bridge
    exeSession   string  // executor-side identifier
    mu           sync.Mutex
    chunks       []OutputChunk  // ring buffer, ≤ 1 MiB total
    chunkSeq     int
    exitCode     *int
    lastActivity time.Time
}

type SessionManager struct {
    mu         sync.RWMutex
    sessions   map[string]*Session
    idleCutoff time.Duration  // 30 min default
    // background goroutine sweeps idle sessions, calls Terminate, deletes
}
```

Output beyond 1 MiB ring evicts oldest chunks; response carries `truncated:true` and `lost_bytes` so SDK can surface "your tail dropped N bytes". Per workspace soft cap: 10 concurrent sessions (configurable). Beyond cap → 429 on `exec_command`.

### Python SDK rewrite

`sdk/python/src/agentserver_sdk/`:

- `client.py` — replace `WSClient` with `HTTPClient` (httpx.AsyncClient). No initialize/initialized/thread/start handshake. No reader task, no pending-futures dict.
- `ctx.py` — `Ctx.envs()` calls `POST /api/sdk/envs/list`.
- `env.py` — `Env.call(tool, args)` calls `POST /api/sdk/envs/{name}/tool/call`.
- `process.py` — `Process.__aenter__` calls `Env.call("exec_command", ...)` (which returns `session_id` in structuredContent), then `write_stdin`/`read_output`/`terminate` hit the `/api/sdk/processes/*` endpoints directly.
- `pyproject.toml` — swap `websockets` → `httpx`.

`Env`'s shape simplifies: no `thread_id`, no inputSchema (server validates, SDK trusts). `ToolMetadata` keeps `{name, description, kind}`.

### Jupyter sandbox env var changes

`internal/sandbox/manager.go` jupyter case:

```go
// Before:
//   AGENTSERVER_GATEWAY_URL = "ws://...codex-app-gateway.../notebook/ws"
//   AGENTSERVER_WORKSPACE_TOKEN = mintCodexHMACToken(...)
//
// After:
//   AGENTSERVER_GATEWAY_URL = "http://agentserver-codex-exec-gateway.agentserver.svc:6060"
//   AGENTSERVER_WORKSPACE_TOKEN = opts.ProxyToken
//   AGENTSERVER_WORKSPACE_ID = opts.WorkspaceID
```

`sandbox.Config`: drop `CodexAppGatewayURL`, drop `CodexInboundHMACSecret`, add `CodexExecGatewayURL`.
`cmd/serve.go`: drop `CODEX_APP_GATEWAY_URL` + `CODEX_INBOUND_HMAC_SECRET` env reads, add `CODEX_EXEC_GATEWAY_URL`.
`mintCodexHMACToken` helper in `manager.go` — delete (no callers after this).

### Removals from codex-app-gateway

- `handleNotebookWS` function — deleted.
- `r.Get("/notebook/ws", s.handleNotebookWS)` route — deleted.
- `Server.notebookAuth` field + init code — deleted.
- `cfg.InboundHMACSecret` still loaded (codex-app-gateway might use it elsewhere — verify with grep; if no callers remain, remove env var from chart too).

### Helm chart changes

- `deployment.yaml` agentserver container: drop `CODEX_APP_GATEWAY_URL` + `CODEX_INBOUND_HMAC_SECRET` env (both behind `if .Values.codexAppGateway.enabled`); add `CODEX_EXEC_GATEWAY_URL` (behind `if .Values.codexExecGateway.enabled`) pointing at the internal Service.
- Chart bump 0.60.13 → **0.61.0** (SDK protocol breaking — anyone with a sandbox running an old SDK gets connection errors on first call).

## Removed gateway-side complexity

| File | Lines today | After |
|---|---|---|
| `internal/codexappgateway/server.go` `handleNotebookWS` | ~80 | 0 |
| `internal/codexappgateway/server.go` notebookAuth init | ~5 | 0 |
| `internal/sandbox/manager.go` mintCodexHMACToken helper | ~15 | 0 |
| `internal/codexappgateway/envmcp/` (kept as `envtools/`, no net change) | ~1500 | ~1500 |

## Tests

**envtools** — existing tests in `internal/codexappgateway/envmcp/*_test.go` move alongside the code to `internal/envtools/*/`. Assertions unchanged. Imports updated.

**codexexecgateway/sdk**
- `auth_test.go`: hit/miss/negative/expiry; concurrent miss coalescing.
- `handlers_test.go`:
  - envsList happy path with mock registry.
  - toolCall unknown tool → 400.
  - toolCall env not found → 404.
  - toolCall tool returns isError:true → 200.
  - toolCall bridge unavailable → 502.
  - stdin/output/terminate with mismatched workspace → 403.
- `session_test.go`: register, get, forget, ring buffer truncation reports lost_bytes, idle GC fires.

**SDK Python** — `sdk/python/tests/`
- `stub_gateway.py` (WS stub) replaced by `stub_rest.py` (ASGI handler).
- `test_client.py`, `test_ctx.py`, `test_env.py`, `test_process.py` updated to assert REST request shapes.

## Deployment + migration

1. agentserver PR includes everything above + tests; `go build && go test && pnpm build && pytest sdk/python` all green; chart 0.61.0.
2. CI builds agentserver / codex-exec-gateway / codex-app-gateway images + agentserver-jupyter image (with new SDK baked in) + chart 0.61.0.
3. `/root/k8s` PR: chart 0.61.0; no other diffs needed.
4. `pulumi up -s nj-prod`.
5. `kubectl -n agentserver rollout restart deploy/agentserver deploy/agentserver-codex-exec-gateway deploy/agentserver-codex-app-gateway` (image content changed but `:main` tag unchanged → manual rollout).
6. **Delete every existing jupyter sandbox** in the frontend; users recreate. New sandboxes get `CODEX_EXEC_GATEWAY_URL` + new jupyter image with new SDK.

Existing jupyter sandboxes will start 401-ing on SDK calls after the gateway upgrade because their pod env still points at `codex-app-gateway`'s `/notebook/ws` (now deleted). Frontend should show stale-sandbox state; recreating is the only path.

## Risks + fallback

- **agentserver down → SDK auth chain breaks.** Mitigated by 5-min positive cache on the gateway. Sustained outage > 5 min stops fresh sandboxes but in-flight requests succeed.
- **Session memory growth.** Hard cap 1 MiB ring per session + 30-min idle GC + 10 concurrent sessions per workspace. Surfaces `truncated:true` to SDK rather than silently dropping.
- **codex --remote TUI broken by handleNotebookWS removal.** Pre-flight grep run during spec drafting: only callers of `/notebook/ws` are (1) the Python SDK we're rewriting, (2) `internal/server/codex_client.go:34` which strips the suffix from the agentserver pod's own `CODEX_APP_GATEWAY_URL` env to derive `/api/turns` REST base. That latter use is on the **agentserver** pod (different env var instance from the jupyter-sandbox env we're rewriting), so the strip still resolves correctly. TUI uses `/codex-app/ws`. Safe to delete the route + handler.

## Open items (decide during implementation; not blocking spec)

- Cache size for ProxyTokenAuth's LRU — start with 1024 entries.
- Whether `inputSchema` needs to round-trip in `envs/list` for future client-side validation; for v0.61 we drop it to keep the response small.
- Whether `mcpadapter` should re-export tool registry separately or codex-app-gateway constructs its own — favor sharing the same `tools.Registry` map.
