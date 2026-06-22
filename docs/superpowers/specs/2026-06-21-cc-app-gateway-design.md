# cc-app-gateway — managed Claude Code harness for IM-driven turns

**Status:** draft v2 (Phase 0 PoC validated 2026-06-21; self-audit revisions 2026-06-21, see § Audit revisions)
**Date:** 2026-06-21
**Owner:** agentserver / cc integration
**Reverses (partially):** PR #135 "purge stateless-cc stack" (2026-05-20, commit
`9dbf949`) — that purge dropped `cmd/cc-broker` + `internal/ccbroker` on the
grounds that "the Anthropic Claude Code public-binary path was abandoned
2026-05-05; codex is now the sole agent stack". This spec reverses that for the
limited scope of an IM-fronted managed harness, on the basis that (a) the
public binary works as a managed brain via `claude --print` + stream-json
without any of the `--sdk-url` / bridge dead-ends that motivated the purge, and
(b) Phase 0 PoC (2026-06-21) re-validated the four critical assumptions of
spec `2026-05-02-ccbroker-sdk-worker-design.md` against `claude 2.1.185`.

**Inherits without modification:**

- The IM-bridge → agentserver → app-gateway protocol pattern from
  [`2026-05-05-codex-app-gateway-and-exec-gateway-design.md`](2026-05-05-codex-app-gateway-and-exec-gateway-design.md)
  and its concrete codex implementation in
  `internal/server/codex_im_inbound.go` + `internal/server/codex_client.go`.
- The S3 workspace-store pattern from
  [`2026-05-03-ccbroker-s3-workspace-store-design.md`](2026-05-03-ccbroker-s3-workspace-store-design.md)
  (claude-home tarball, diff-snapshot, atomic upload).
- The per-channel-per-user FIFO dispatcher concept used in codex's IM path
  (Phase 4 will copy ~100 LOC into `cc_im_inbound.go`'s own `ccDispatcher`,
  NOT extract a generic package; see § Audit revisions).
- The per-workspace credential model: cc-app-gateway fetches a workspace
  proxy token from agentserver `POST /internal/workspace-token` and injects
  it into the claude subprocess; the subprocess's LLM calls hit llmproxy
  which exchanges the workspace token for upstream Anthropic credentials.
  Exactly mirrors codex's `wstoken_client.go` (see § Auth model).

**Supersedes (only the parts noted):**

- `2026-05-02-ccbroker-sdk-worker-design.md` — adopts the spec's architecture
  (workspace.Setup → runner.Run → workspace.Teardown; in-process MCP tools;
  session jsonl as LLM-context source of truth) but drops the `agentsdk.Client`
  abstraction in favor of a hand-written stream-json client. Reasons in § Why
  no SDK.

## Why this spec, and why no SDK

[`2026-05-02-ccbroker-sdk-worker-design.md`](2026-05-02-ccbroker-sdk-worker-design.md)
specified `cc-broker` against `github.com/agentserver/claude-agent-sdk-go`
which existed as a separate repo at `/root/claude-agent-sdk-go`. cc-broker
itself was deleted in PR #135 because the surrounding `--sdk-url`/bridge
architecture (specs §§2,4 of `2026-04-15-stateless-cc-design.md`) was dead.

**The bridge being dead does not make the SDK approach wrong**, but it does
remove the only reason `cc-broker` existed. Restarting requires picking an
explicit transport for claude. Three options were considered:

1. `pkg/agentsdk` (already in repo) — surveyed 2026-06-21, NOT applicable.
   This package is an agentserver-tunnel client SDK (WebSocket+yamux to
   agentserver, OAuth device flow, task polling). It does not spawn or drive
   `claude --print` subprocesses. Using it would require building a separate
   subprocess driver inside it anyway.

2. Vendor `/root/claude-agent-sdk-go` — feasible, but adds a dependency that
   evolves on its own cadence and that we own. Phase 0 PoC proved that the
   raw `claude --print --output-format stream-json` interface is direct
   enough that the SDK's value-add is mostly framing.

3. **Hand-written stream-json client** — chosen. Mirrors the same architectural
   choice
   [`2026-05-10-codex-app-gateway-subprocess.md`](2026-05-10-codex-app-gateway-subprocess.md)
   made for codex: there is no codex-go SDK either; the gateway speaks the
   subprocess's wire protocol directly because the subprocess IS the protocol
   implementation. For cc the wire protocol is one-shot stdin/stdout JSON
   lines, ~500 LOC.

`pkg/agentsdk` continues to exist for its own purpose (custom agents reaching
into agentserver from outside). It is unrelated to this spec.

## Architecture

The full Phase-4 flow (Phase 1 only implements the steps marked **[P1]**;
Phase 2-4 add **[P2]**, **[P3]**, **[P4]** respectively).

```
WeChat / Telegram / etc
       │
       ▼
imbridge
       │  POST /api/internal/imbridge/cc/turn            [P4]
       │  X-Internal-Secret
       │  {channel_id, workspace_id, wechat_user_id, text, media_*}
       ▼
┌──────────────────────────────────────────────────────────────────────┐
│ agentserver                                                            │
│                                                                        │
│  internal/server/cc_im_inbound.go        (NEW [P4])                    │
│     │ 1. Verify X-Internal-Secret                                      │
│     │ 2. ccDispatcher.Enqueue(req)    (per channel_id+wechat_user_id;  │
│     │                                  copied from codexDispatcher,    │
│     │                                  NOT a shared generic package)   │
│     │ 3. Return 202 + {"queued":true}                                  │
│     ▼ (goroutine)                                                      │
│  processTurn(req):                                                     │
│     ├─ sessions.GetOrCreateSessionByExternalID()                       │
│     │     reuse agent_sessions table                                   │
│     │     NEW column: claude_session_id TEXT (parallel to codex_thread)│
│     ├─ build cc UserInput (text + image data URLs same as codex)       │
│     ├─ turnID := newUUID()                                             │
│     ├─ register(turnID → req)  // in-memory map for callback lookup    │
│     ├─ ccClient.SubmitTurn(ctx, CcTurnRequest{                         │
│     │     turnId, workspaceId, sessionId, userMessage,                 │
│     │     callbackUrl: "http://agentserver:8080/internal/cc-turn-cb",  │
│     │   })                                                             │
│     │     POST ${CC_APP_GATEWAY_REST_URL}/api/turns                    │
│     │     X-Internal-Secret                                            │
│     │  ← returns 202 + {turnId} immediately                            │
│     │                                                                  │
│     ▼ (later — callback)                                               │
│  cc_im_inbound.handleCallback():                                       │
│     ├─ Verify X-Internal-Secret                                        │
│     ├─ Lookup req by turnId                                            │
│     ├─ sessions.SetSessionClaudeSessionID(sid, claudeSessionID)        │
│     └─ POST ${IMBridgeURL}/api/internal/imbridge/send                  │
│           {channel_id, to_user_id, text}                               │
└────────────────────────────────────────┬─────────────────────────────┘
                                         │
                                         │ POST /api/turns                [P1: synchronous; P4: async+callback]
                                         ▼
┌──────────────────────────────────────────────────────────────────────┐
│ cc-app-gateway                          (NEW service)                  │
│                                                                        │
│  server.go: POST /api/turns                                            │
│     ├─ Verify Bearer (workspace token) OR X-Internal-Secret    [P1]    │
│     │                                                                  │
│     │── Phase 1: synchronous path ────────────────────────────────     │
│     ├─ wstokenClient.GetOrCreate(workspaceID)                  [P1]    │
│     │     POST ${AGENTSERVER_INTERNAL_URL}/internal/workspace-token    │
│     │     X-Internal-Secret                                            │
│     │  ← {token: "<64-char hex>"}                                      │
│     │                                                                  │
│     ├─ workspace.Setup(ctx, wid, sid)                                  │
│     │     [P1] mkdir /tmp/cc/<uuid>/{claude-home,project,memory}       │
│     │     [P2] S3 download claude-home.tar.zst → claude-home/          │
│     │     [P2] snapshot for later diff                                 │
│     │                                                                  │
│     ├─ tools.BuildMcpConfig(turnCtx)                           [P3]    │
│     │     write /tmp/cc/<uuid>/mcp.json pointing at                    │
│     │     `cc-app-gateway env-mcp` self-exec subcommand                │
│     │     (Phase 1 omits --mcp-config entirely → no tools)             │
│     │                                                                  │
│     ├─ runner.Run(ctx, ws, sid, userMsg) <-chan SDKMessage             │
│     │                                                                  │
│     │     exec.CommandContext("claude",                                │
│     │       "--print",                                                 │
│     │       "--input-format", "stream-json",                           │
│     │       "--output-format", "stream-json",                          │
│     │       "--verbose",                                               │
│     │       (P3+) "--mcp-config", mcpJSONPath,                         │
│     │       (P3+) "--strict-mcp-config",                               │
│     │       (P3+) "--tools", "mcp__cc-app-gateway__*",                 │
│     │       "--permission-mode", "bypassPermissions",                  │
│     │       "--dangerously-skip-permissions",                          │
│     │       "--model", session.Model,                                  │
│     │       sessionFlag, sessionID)                                    │
│     │     cmd.Dir = ws.ProjectDir                                      │
│     │     cmd.Env += CLAUDE_CONFIG_DIR, IS_SANDBOX=1,                  │
│     │                CLAUDE_CODE_AUTO_COMPACT_WINDOW,                  │
│     │                ANTHROPIC_AUTH_TOKEN=<wsToken>,                   │
│     │                ANTHROPIC_BASE_URL=http://llmproxy:8081           │
│     │                                                                  │
│     │     stdin:  one SDKUserMessage JSON line, then close             │
│     │     stdout: scan; route by type                                  │
│     │                                                                  │
│     ├─ consume SDKMessage channel:                                     │
│     │     keep: system/init, assistant, user(tool_result), result      │
│     │     drop: stream_event, system/status, thinking_tokens           │
│     │     extract result.result text → assistantText                   │
│     │                                                                  │
│     ├─ workspace.Teardown(ctx, ws) [background goroutine]      [P2]    │
│     │     diff snapshot → S3 upload changed files                      │
│     │     rm -rf temp dir                                              │
│     │                                                                  │
│     │── Phase 1: synchronous return ──────────────────────────────     │
│     └─ HTTP 200 CcTurnResponse{sessionID, assistantText, status}       │
│                                                                        │
│  [P4] If req.callbackUrl is set, switch to async mode:                 │
│     ├─ Return 202 + {turnId} immediately to caller                     │
│     ├─ Run the same Setup → runner → Teardown in goroutine             │
│     └─ POST callbackUrl with CcTurnResult after completion             │
│                                                                        │
│  env-mcp/                              (Phase 3+, not in Phase 1)      │
│     subcommand: cc-app-gateway env-mcp                                 │
│     stdio MCP server; tool handlers run in the child process           │
└──────────────────────────────────────────────────────────────────────┘
                                  │
                                  │ subprocess LLM calls
                                  ▼
                  llmproxy on :8081 (Bearer <wsToken>)
                  → validates wsToken against agentserver
                  → forwards with real Anthropic creds OR modelserver JWT
```

### API contract evolution by phase

Phase 1 (direct curl) and Phase 4 (IM-fronted) need DIFFERENT contracts
because Phase 4 has a callback URL that doesn't exist at Phase 1:

- **Phase 1**: synchronous. Caller `POST /api/turns`, blocks until claude
  exits, gets `CcTurnResponse{assistantText, ...}`. Simple to exercise
  from curl; acceptance test does exactly this.
- **Phase 4**: same endpoint detects `callbackUrl` in request body, switches
  to async — returns `202 + {turnId}` immediately, runs the turn in a
  goroutine, POSTs the result to `callbackUrl` when done. Behavioral switch
  is a single `if req.CallbackUrl != "" {...}` branch; both code paths share
  the same `runTurn(ctx, req)` core.

This avoids the "Phase 1 = sync, Phase 4 = rewrite everything async"
trap. The same `/api/turns` endpoint serves both, with `callbackUrl` as
the mode switch.

### Differences vs codex-app-gateway

| Concern | codex-app-gateway | cc-app-gateway |
|---|---|---|
| Subprocess lifecycle | per-(workspace, thread) **long-lived** `codex app-server`, idle-reaped at 30m | per-turn **ephemeral** `claude --print`; spawned + exits per `/api/turns` |
| State persistence | sqlite + sessions/ + skills/ inside CODEX_HOME, tarballed to S3 on idle reap | session `.jsonl` inside CLAUDE_CONFIG_DIR/projects/, tarballed to S3 on every turn completion |
| Pool / supervisor | yes (broker + supervisor packages, ~2000 LOC) | **no** (runner spawns claude, waits for exit, returns) |
| Process count at idle | N subprocesses per workspace × idle window | 0 |
| Wire protocol | v2 JSON-RPC over loopback ws (forwarded byte-for-byte from upstream TUI) | stream-json over stdin/stdout (one user JSON in, N SDKMessage JSON lines out) |
| In-process MCP | spawned per-turn as child of codex subprocess (`codex-app-gateway env-mcp`) | spawned per-turn as child of claude subprocess (`cc-app-gateway env-mcp`); same pattern, separate binary |
| Phase 1 LOC estimate | ~5000 (broker/pool/supervisor are the bulk) | ~700 (runner + server + workspace skeleton; ~2700 at full Phase 4) |

The "no pool" choice is the load-bearing one: it inherits cleanly from
spec 2026-05-02 §1.3 ("each turn still spawns and exits a fresh CLI process"),
and it's what makes the LOC budget so much smaller than codex's. cc-app-gateway
stays horizontally scalable trivially.

## Component layout

```
cmd/cc-app-gateway/
  main.go                       ~150 LOC — subcommands: serve, env-mcp
                                            env: CCAPPGW_LISTEN_ADDR,
                                                 CLAUDE_BIN, S3_*,
                                                 AGENTSERVER_INTERNAL_URL,
                                                 INTERNAL_SECRET

internal/ccappgateway/
  server.go                     ~250 LOC — chi router, auth middleware,
                                            /healthz /readyz /api/turns
  config.go                     ~150 LOC — CCAPPGW_* env parsing
  turn_api.go                   ~200 LOC — POST /api/turns orchestration
  s3_store.go                   ~100 LOC — claude-home tarball IO

  workspace/                                (per-turn ephemeral local view)
    workspace.go                ~120 LOC — Workspace struct
    setup.go                    ~180 LOC — mkdir + S3 download + snapshot
    teardown.go                 ~120 LOC — diff snapshot + S3 upload
    snapshot.go                 ~100 LOC — file-level snapshot/diff

  runner/                                   (claude subprocess driver)
    runner.go                   ~250 LOC — Run(ctx, ws, sid, userMsg)
    stream_json.go              ~200 LOC — JSON-line encoder/decoder
    options.go                  ~100 LOC — buildArgs / buildEnv
    events.go                   ~150 LOC — SDKMessage struct + filtering

  tools/                                    (Phase 3+; stub in Phase 1)
    context.go                  ~60 LOC  — TurnContext
    mcp_server.go               ~250 LOC — minimal in-process MCP
    workspace_tools.go          ~150 LOC — workspace_read/write/ls
    im_tools.go                 ~120 LOC — send_message/send_image/send_file
    askuser.go                  ~100 LOC — AskUserQuestion → agentserver

  auth/
    auth.go                     ~150 LOC — Bearer / X-Internal-Secret middleware

  wstoken_client.go             ~80  LOC — POST /internal/workspace-token client;
                                            mirrors internal/codexappgateway/
                                            wstoken_client.go (Phase 1)

agentserver side:
  internal/server/
    cc_im_inbound.go            ~400 LOC — NEW [P4]; ~100 LOC of those are a
                                            copy of codexDispatcher renamed to
                                            ccDispatcher (with ccInboundRequest
                                            instead of codexInboundRequest) — we
                                            do NOT extract a generic package,
                                            see § Audit revisions
    cc_client.go                ~100 LOC — NEW [P4]; mirrors codex_client.go;
                                            SubmitTurn returns 202+turnId
    cc_turn_callback.go         ~80  LOC — NEW [P4]; POST /internal/cc-turn-cb
                                            handler that completes the turn:
                                            extracts assistantText, posts to
                                            imbridge /send
    server.go                   edits     — wire /api/internal/imbridge/cc/turn
                                            + /internal/cc-turn-cb routes;
                                            Server.CcAppGatewayURL field;
                                            Server.cc *cc.Client
  internal/imbridgesvc/handlers.go
                                edit [P4] — update routing_mode validator at L977
                                            to also accept "managed_cc" (existing
                                            check is in business code, not DB)

  internal/db/migrations/
    NNN_agent_sessions_claude_session_id.sql ~10 LOC  [P2]

deploy/helm/agentserver/templates/
  cc-app-gateway.yaml           — mirror codex-app-gateway.yaml
  + values.yaml ccAppGateway block

Dockerfile.cc-app-gateway       — multi-stage Go build + claude binary install
                                  mirror Dockerfile.claudecode for install step

.github/workflows/
  + build-cc-app-gateway job    — mirror build-codex-app-gateway
```

## Subprocess driver (runner) — protocol details

Validated against `claude 2.1.185` in Phase 0 PoC. All flags/env are
mandatory unless noted.

```go
args := []string{
    "--print",
    "--input-format", "stream-json",
    "--output-format", "stream-json",
    "--verbose",                        // REQUIRED with stream-json IO; CLI errors otherwise
    "--mcp-config", mcpJSONPath,
    "--strict-mcp-config",              // ignore user-level MCP servers
    "--tools", "mcp__cc-app-gateway__*",
    "--permission-mode", "bypassPermissions",
    "--dangerously-skip-permissions",
    "--model", session.Model,
}
if isFirstTurn(session) {
    args = append(args, "--session-id", sessionID)
} else {
    args = append(args, "--resume", sessionID)
}

// wsToken is fetched per-turn (cheap idempotent lookup; agentserver caches it).
// See § Auth model for the wstoken pattern (copied from codex-app-gateway).
wsToken, err := wstokenClient.GetOrCreate(ctx, workspaceID)
if err != nil { /* fail the turn */ }

cmd := exec.CommandContext(ctx, claudeBin, args...)
cmd.Dir = ws.ProjectDir
cmd.Env = append(os.Environ(),
    "CLAUDE_CONFIG_DIR="+ws.ClaudeDir,
    "IS_SANDBOX=1",                                  // bypass root guard
    "CLAUDE_CODE_AUTO_COMPACT_WINDOW=165000",
    "CLAUDE_CODE_DISABLE_FILE_CHECKPOINTING=1",
    // Per-workspace token (NOT the gateway's own env).
    // claude accepts ANTHROPIC_AUTH_TOKEN as Bearer (verified Phase 0:
    // init frame shows apiKeySource="none" when using AUTH_TOKEN).
    "ANTHROPIC_AUTH_TOKEN="+wsToken,
    // Point claude at llmproxy (which validates wsToken and forwards
    // to upstream Anthropic with real credentials).
    "ANTHROPIC_BASE_URL="+cfg.LLMProxyURL,            // e.g. http://llmproxy:8081
)
```

**Why per-workspace token, not gateway env:** spec v1 wrote "ANTHROPIC_AUTH_TOKEN
inherited from gateway env" — wrong. Codex established the per-workspace token
pattern (see `internal/codexappgateway/wstoken_client.go` and § Auth model).
This buys per-workspace billing, rate-limiting, and audit; without it cc-app-gateway
would lose multi-tenant accountability. The cost is one extra HTTP roundtrip per
turn to `agentserver POST /internal/workspace-token` — agentserver-side
`GetOrCreateWorkspaceToken` is idempotent and DB-backed, sub-millisecond hot
path. Both codex and cc share the same agentserver endpoint and the same
`proxy_tokens` table (`token_type='workspace'`).

stdin: write one line and close:

```json
{"type":"user","message":{"role":"user","content":[{"type":"text","text":"<userMsg>"}]}}
```

stdout: line-delimited JSON. Frames observed in PoC (62 frames across 2 turns):

| keep? | type/subtype | use |
|---|---|---|
| ✅ | `system/init` | first frame; carries session_id (confirms), model, mcp_servers connectivity |
| ✅ | `assistant` (role=assistant) | accumulated assistant message per content_block_stop |
| ✅ | `user` (role=user) | wraps tool_result content from a tool_use; one per tool call |
| ✅ | `result/success`, `result/error` | final frame; `result.result` is the text we ship to IM; `total_cost_usd`, `modelUsage` |
| ❌ | `stream_event` (message_start/content_block_delta/...) | partial deltas; suppress by NOT passing `--include-partial-messages` |
| ❌ | `system/status` (`requesting`) | keepalive; noise |
| ❌ | `system/thinking_tokens` | progress counter; noise |

Runner returns `<-chan SDKMessage` of the kept frames; turn_api consumes
until the channel closes (subprocess exits) and extracts the `result` frame's
assistant text.

### Session ID lifecycle

- Phase 1 turn on a workspace+user without a prior agent_session row →
  caller mints a fresh UUID, runner uses `--session-id <UUID>`.
- Subsequent turns on the same agent_session → runner uses `--resume <UUID>`.
  (`--session-id <existing>` errors with "session already exists".)
- jsonl file path: `${CLAUDE_CONFIG_DIR}/projects/${sanitize(cwd)}/${UUID}.jsonl`
  where `sanitize` replaces `/` with `-` and strips the leading dash.
  → `/tmp/cc/<uuid>/project` becomes `-tmp-cc-<uuid>-project`. Stable per
  workspace as long as we keep ProjectDir deterministic.

### What runner does NOT do

- No `--max-turns` enforcement (CLI doesn't expose the flag). Wall-clock
  timeout enforced by gateway via `context.WithTimeout`; if it fires we
  SIGTERM the subprocess. Per-turn cap is `CCAPPGW_TURN_TIMEOUT` (default
  10m).
- No streaming response. Phase 1 returns the full assistant text in the
  HTTP response body. SSE-streaming is a Phase 5 candidate (matches the
  codex pattern of `GET /api/sessions/{sid}/events` which is also not
  shipped yet).

## Phase 1 contract (this spec's primary scope)

### Service

`cc-app-gateway serve --listen :8087` (port chosen to not collide with
codex-app-gateway's 8086).

### Endpoints

```
POST /api/turns
  Auth: Bearer <workspace_token> OR X-Internal-Secret
  Body: CcTurnRequest {
    workspaceId:  string (required)
    sessionId:    string (required; caller mints UUID for new sessions)
    userMessage:  string (required; for Phase 1, text only)
    model:        string (optional; defaults to "haiku" or
                          CCAPPGW_DEFAULT_MODEL env)
    timeoutMs:    int    (optional; capped at CCAPPGW_TURN_TIMEOUT)
    callbackUrl:  string (optional; if present switches to async mode — Phase 4)
  }

  IF callbackUrl is empty (Phase 1 — synchronous):
    Response 200: CcTurnResponse {
      sessionId:      string  (echoes request)
      assistantText:  string  (the final assistant message)
      isError:        bool
      durationMs:     int
      totalCostUsd:   float
      modelUsage:     map[string]usage
    }

  IF callbackUrl is set (Phase 4 — async):
    Response 202: {turnId: string}     // ack only; result POSTs to callbackUrl
    Background: when claude exits, POST callbackUrl with:
      CcTurnResult {
        turnId, sessionId, assistantText, isError, durationMs,
        totalCostUsd, modelUsage, errorMessage?
      }
      Headers: X-Internal-Secret (same secret as gateway received)

  Response 4xx/5xx: {error: string, code: string}

GET /healthz   → 200 "ok"
GET /readyz    → 200 if claude binary is reachable AND agentserver
                 /internal/workspace-token is reachable; 503 otherwise
```

The `callbackUrl` field is REQUIRED to be set by IM-driven Phase-4 callers
(because IM turns can take longer than imbridge's HTTP timeout and need to
fire-and-forget). Direct callers (curl, future TUI/web client) leave it empty
and block on the synchronous response. Both code paths share the same
`runTurn(ctx, req)` core; the only branch is the response shape and whether
the body is returned inline or POSTed to a callback.

### Scope: explicit IN

- Single `/api/turns` endpoint, **synchronous mode only** (no callbackUrl).
  Spawns claude, runs one turn, returns the assistant text inline.
- Auth: Bearer (workspace token) + X-Internal-Secret.
- claude is spawned per-call; no pool, no resume yet (Phase 2).
- **wstoken client** fully wired: each `/api/turns` call fetches
  `/internal/workspace-token` from agentserver and injects the result into
  the claude subprocess. (This is NOT optional even in Phase 1 — without
  it llmproxy rejects the request as `invalid api key`.)
- Empty MCP tool set: Phase 1 OMITS `--mcp-config` and `--strict-mcp-config`
  entirely. claude uses 0 tools. (Avoids needing the env-mcp subcommand
  until Phase 3.)
- No S3 (workspace is `/tmp/cc/<uuid>/project`, empty; discarded after turn).
- claude binary path configurable via `CLAUDE_BIN` env.
- Helm chart + Dockerfile + CI workflow job.

### Scope: explicit OUT (deferred to Phase 2+)

- Session resume (`--resume`). Phase 1 is single-turn only; each call
  spawns a fresh `--session-id`. (No DB schema change in Phase 1 either;
  `claude_session_id` column lands in Phase 2.)
- S3 workspace persistence. No `claude-home.tar.zst` round-trip.
- In-process MCP tools (env-mcp subcommand).
- IM intake on agentserver side. Phase 1 acceptance is a curl. IM wiring
  is Phase 4.
- `callbackUrl` async mode (Phase 4).
- Streaming response (SSE).

## Phase 0 PoC log (2026-06-21)

Validated the four assumptions of spec 2026-05-02 against `claude 2.1.185`.
Full transcript at `/tmp/cc-probe/` (probe.go + echo_mcp.py + FINDINGS.md).

### PoC #1 — `--session-id <UUID>` accepted; jsonl at predictable path

```bash
claude --print --output-format stream-json --input-format stream-json \
    --verbose --mcp-config /tmp/cc-probe/mcp.json --strict-mcp-config \
    --tools mcp__echo__echo --permission-mode bypassPermissions \
    --dangerously-skip-permissions --model haiku \
    --session-id 56e9a3d9-98b0-4362-800c-d54faa44909b
# env: CLAUDE_CONFIG_DIR=/tmp/cc-probe/claude-home IS_SANDBOX=1
# cwd: /tmp/cc-probe
```

session_id was honored; jsonl appeared at
`/tmp/cc-probe/claude-home/projects/-tmp-cc-probe/56e9a3d9-...jsonl`.
cwd-sanitize rule (replace `/` with `-`, strip leading dash) determines
the project subdirectory.

### PoC #2 — external stdio MCP works

`mcp.json` pointed at a `python3 echo_mcp.py` stdio MCP server. claude
opened a child process, exchanged `initialize` (advertising protocolVersion
2025-11-25; server replied 2025-06-18, accepted), `tools/list`,
`tools/call name=echo arguments={text:"phase0-works"}`. Tool result wrapped
in a `user` SDKMessage; model emitted an assistant message referencing the
returned text.

### PoC #3 — resume across spawns

```bash
claude --print [...same flags...] --resume 56e9a3d9-98b0-4362-800c-d54faa44909b
```

with prompt "What text did I ask you to echo earlier?"; model answered
`You asked me to echo "phase0-works".`. jsonl file appended (11 → 20
lines, same file). Confirms `--resume <id>` reloads context from the jsonl;
`--session-id <existing>` would error.

### PoC #4 — SDKMessage schema enumerated

90 frames across PoC #1+#2 captured to `transcript.jsonl`. Frame-type
inventory in § Subprocess driver above. Schema match expectation: every
frame carries `type`, `session_id`, `uuid`; `result/success` carries
`{result, total_cost_usd, duration_ms, modelUsage, num_turns}` —
sufficient for IM-reply use case.

### Phase 0 spec corrections to 2026-05-02

| Spec 2026-05-02 said | Phase 0 found | Resolution |
|---|---|---|
| `WithCwd(ws.ProjectDir)` (implied `--cwd`) | `--cwd` is NOT a flag on 2.1.185 | Use `cmd.Dir = ws.ProjectDir` instead |
| `WithMaxTurns(config.maxTurns)` | `--max-turns` is NOT a flag | Use `context.WithTimeout` + SIGTERM; alternative is control_request frame on stdin (deferred) |
| `WithAllowDangerouslySkipPermissions()` | Refuses under root unless `IS_SANDBOX=1` | Set `IS_SANDBOX=1` (or `CLAUDE_CODE_SANDBOXED=1`) in subprocess env |
| (implicit) `--print` with stream-json works without ceremony | Requires `--verbose` flag too; CLI errors otherwise | Always pass `--verbose` in runner |
| (implicit) `WithResume(sessionID)` maps to a single flag | First turn is `--session-id <id>`; subsequent is `--resume <id>` (different flag) | Runner branches on isFirstTurn |

### What PoC did NOT cover

- Sustained multi-turn over many days (jsonl unbounded growth in practice).
  Mitigation `CLAUDE_CODE_AUTO_COMPACT_WINDOW=165000` is set; effectiveness
  unverified.
- claude binary version drift (PoC is one version, 2.1.185). Pin claude
  in Dockerfile; CI re-runs probe on bump.
- in-process MCP via self-exec subcommand. PoC used a python script as
  the MCP server; the gateway will use itself as a subcommand. The MCP
  protocol part is validated; the self-exec pattern is a Phase 3 deliverable.
- S3 round-trip behavior (Phase 2).
- IM-to-gateway end-to-end (Phase 4).

## Auth model

| Hop | Credential | Validator | Lifetime |
|---|---|---|---|
| [P4] imbridge → agentserver `/api/internal/imbridge/cc/turn` | `X-Internal-Secret` (existing shared secret) | agentserver middleware | static; rotated by env var update |
| agentserver → cc-app-gateway `/api/turns` | `X-Internal-Secret` (same shared secret) | cc-app-gateway middleware | static |
| **cc-app-gateway → agentserver `/internal/workspace-token`** | `X-Internal-Secret` | agentserver middleware | static |
|  ← returns per-workspace token (64-char hex; idempotent on `proxy_tokens.token_type='workspace'`) | | | persistent until rotated |
| cc-app-gateway → claude subprocess | none (parent-child); env var passes `ANTHROPIC_AUTH_TOKEN=<wsToken>` | n/a | per-turn (token reused across turns) |
| claude subprocess → llmproxy `:8081/v1/messages` etc | `Bearer <wsToken>` (Anthropic protocol) | llmproxy `extractProxyToken` + `ValidateProxyToken` | per workspace's issued token |
| llmproxy → upstream | modelserver JWT OR llmproxy's global ANTHROPIC_API_KEY/AUTH_TOKEN | upstream | short-lived (modelserver) / static (global) |
| (Phase 3+) claude subprocess → env-mcp child | none (parent-child) | n/a | n/a |
| (Future) external `POST /api/turns` caller → cc-app-gateway | `Bearer <workspace_token>` (existing wstoken) | cc-app-gateway middleware | per-workspace, refreshable |

**Token flow (the critical insight that v1 of this spec got wrong):**

```
cc-app-gateway POST /internal/workspace-token (X-Internal-Secret)
   → agentserver returns workspace token "ws_xxxxx..."
cc-app-gateway spawn claude with ANTHROPIC_AUTH_TOKEN=ws_xxxxx, ANTHROPIC_BASE_URL=http://llmproxy:8081
claude POST http://llmproxy:8081/v1/messages with header "Authorization: Bearer ws_xxxxx"
llmproxy validates ws_xxxxx → workspace_id → swaps Bearer for real Anthropic creds → forwards upstream
```

cc-app-gateway never sees real Anthropic creds. They live in llmproxy (or in
modelserver). cc-app-gateway only handles the workspace-scoped opaque token,
which means:

- Per-workspace billing/rate-limiting falls out automatically (llmproxy already
  attributes by token).
- A leaked cc-app-gateway pod compromises workspace tokens (recoverable by
  rotation) but not Anthropic master credentials.
- Multi-workspace concurrency requires zero gateway-side fan-out — just call
  GetOrCreate per turn; agentserver's `proxy_tokens.token_type='workspace'`
  uniqueness constraint deduplicates.

Phase 1 only exercises the X-Internal-Secret path AND the wstoken fetch
(it's not optional — the spawned claude needs a real workspace token to
talk to llmproxy). External Bearer auth on `/api/turns` is wired but not
exercised until a future external caller exists. This matches codex-app-gateway's
auth layout (`internal/codexappgateway/auth/`).

## State management

| Store | Owner | Per-turn shape |
|---|---|---|
| session `.jsonl` inside CLAUDE_CONFIG_DIR/projects/`<sanitized-cwd>` | claude itself | Append-only log of every SDKMessage |
| Local tmpdir | gateway | `/tmp/cc-app-gateway/<turn_uuid>/{claude-home,project,memory}` |
| S3 tarball (Phase 2+) | gateway | `s3://cc-app-gateway/<workspace_id>/<session_id>.tar.zst` |
| In-memory | gateway | None (every turn is independent; no pool) |
| Postgres (agentserver side) | agentserver | `agent_sessions` row with new column `claude_session_id` |

The "no in-memory state" property is what lets cc-app-gateway scale
horizontally with zero coordination. Multiple replicas behind a Service
work without sticky sessions.

## Phase 1 vs deferred

**Phase 1 (this spec's primary scope):**

- `cc-app-gateway serve` HTTP endpoint with X-Internal-Secret auth.
- runner subpackage: spawn `claude --print`, stream-json IO, return final
  assistant text.
- **wstoken_client wired**: per-turn `GetOrCreate` lookup against
  agentserver, inject `ANTHROPIC_AUTH_TOKEN=<wsToken>` +
  `ANTHROPIC_BASE_URL=<llmproxy>` into claude subprocess.
- Stub workspace: `/tmp/cc/<uuid>/project` (empty, discarded after turn).
- No MCP tools (`--mcp-config` omitted entirely; 0 tools to claude).
- No session resume; every call is single-turn (`--session-id` only).
- Helm chart + Dockerfile + CI workflow.
- Bearer-token validation wired but unused.
- Synchronous `/api/turns` response shape only (no callbackUrl support).

**Phase 2 candidates (out of scope):**

- S3 claude-home tarball round-trip on every turn (workspace.Setup/Teardown).
- Session resume across turns (`--resume`).
- `agent_sessions.claude_session_id` plumbing on agentserver side.

**Phase 3 candidates:**

- `cc-app-gateway env-mcp` self-exec subcommand.
- In-process MCP tool registry: workspace_read/write/ls, send_message,
  AskUserQuestion.

**Phase 4 candidates:**

- IM intake: `cc_im_inbound.go` + `cc_client.go` + `cc_turn_callback.go` on
  agentserver side.
- `callbackUrl` async mode on cc-app-gateway `/api/turns`.
- `ccDispatcher` (per channel+user FIFO) — **copy** of codexDispatcher with
  ccInboundRequest type, NOT a generic extraction. Rationale: extraction
  requires changing codex's existing hot-path code, costing 1-2 days plus
  regression risk; the duplicate is ~100 LOC and decouples the two flows
  cleanly. See § Audit revisions.
- `"managed_cc"` routing mode added to `internal/imbridgesvc/handlers.go:977`
  validator (the check is in code, not a DB CHECK constraint).

**Phase 5+ candidates:**

- SSE streaming response (`GET /api/sessions/{sid}/events`).
- Bearer-token validation flow (external `POST /api/turns` callers).
- Remote tools (executor-registry analog); only if a use case appears.
  Note that executor-registry was deleted in PR #135 alongside cc-broker.

## Open risks

1. **PR #135 reversal.** This spec materially undoes a strategic decision
   from 2026-05-20. The justification rests on (a) Phase 0 proving the
   public-binary path works cleanly, and (b) scoping cc-app-gateway to
   IM-managed harnesses only (not a TUI thin-client path; that is
   separately blocked per [[cc_v2_1_185_gateway_feasibility]]). If the
   2026-05-05 decision was driven by org-strategic factors beyond the
   technical dead-end, those factors are NOT addressed here.

2. **claude CLI version drift.** stream-json frame shapes and CLI flags
   could change between 2.1.185 and a future release. Mitigation: pin
   the claude binary version in Dockerfile.cc-app-gateway; add an
   integration test that re-runs the Phase 0 probe against the pinned
   version on CI. Spec recommends bumping the pin manually with a probe
   re-run, not auto-tracking latest.

3. **Session jsonl grows unboundedly.** Without S3 persistence (Phase 1)
   each session is throwaway, so this only bites in Phase 2+ when we
   resume. Mitigation: `CLAUDE_CODE_AUTO_COMPACT_WINDOW=165000` is set
   in subprocess env; effectiveness must be measured in Phase 2 load
   tests.

4. **No backpressure / queue.** Phase 1 accepts any `/api/turns` at line
   rate. A burst of concurrent turns spawns N concurrent claude processes.
   On a small pod this OOMs. Mitigation: agentserver-side dispatcher (the
   per-(channel,user) FIFO inherited from codex) bounds concurrency from
   one direction; for direct callers a basic per-workspace semaphore in
   server.go is a Phase 2 follow-up. Phase 1 leaves it open with a
   documented warning.

5. **Hand-written stream-json client schema drift.** Same risk as #2 but
   on the consumer side. PoC captured the schema at 2.1.185; if Anthropic
   changes the SDKMessage shape, runner/events.go silently drops the new
   frames (or misclassifies them as drop-list). Mitigation: log "unknown
   frame type" warnings; alert on high counts.

6. **`pkg/agentsdk` namespace collision.** Newcomers reading the codebase
   may assume `pkg/agentsdk` is the cc driver (the name suggests it).
   Add a `doc.go` paragraph cross-referencing this spec; document that
   `pkg/agentsdk` is for outside-in custom agents, `cc-app-gateway/runner`
   is for inside-out claude spawning. Different directions, different code.

7. **Pod sizing under burst load.** Each spawned claude subprocess is
   ~300-400 MB RSS (223 MB ELF + 100-200 MB JS runtime). At 10 concurrent
   turns per pod that's 3-4 GB. Without an in-gateway semaphore (Phase 1
   has none), a workspace burst can OOM the pod. Mitigation order:
   (a) start with `resources.requests.memory=512Mi, limits.memory=2Gi` +
   single replica (caps ~5 concurrent turns); (b) add per-workspace
   semaphore in Phase 2; (c) scale replicas only after Phase 4 IM dispatcher
   provides natural per-(channel,user) FIFO ceiling.

## Audit revisions (2026-06-21, post-self-review)

After the first draft this spec was self-audited against the actual codebase.
Three real bugs were found and fixed. Recording the diff here so future
readers see the rationale, not just the result.

### Revision 1 — credential model was wrong

**v1 wrote:** "ANTHROPIC_AUTH_TOKEN inherited from gateway env"

**Reality:** codex-app-gateway uses per-workspace tokens via
`internal/codexappgateway/wstoken_client.go`. Each spawned subprocess gets
a workspace-scoped opaque token; the LLM call hits llmproxy which validates
the token and swaps it for real Anthropic credentials. v1's "inherit from
env" approach would have lost per-workspace billing/audit/rate-limit and
forced cc-app-gateway to handle real Anthropic credentials (an unnecessary
expansion of the trust boundary).

**Fix:** Added `wstoken_client.go` to component layout. Updated runner code
example to fetch wsToken per turn and inject as `ANTHROPIC_AUTH_TOKEN` +
point `ANTHROPIC_BASE_URL` at llmproxy. Updated auth-model table to show
the full token-exchange flow. Made wstoken fetch part of Phase 1 scope
(not deferred) — without it, claude's first LLM call gets rejected by
llmproxy and the whole turn fails.

### Revision 2 — dispatcher reuse was unrealistic

**v1 wrote:** "complete codex dispatcher reuse — just import"

**Reality:** `codexDispatcher` (lines 467-573 of `codex_im_inbound.go`) has
the callback signature `func(codexInboundRequest)` and an internal `key()`
function hardcoded to `ChannelID + WechatUserID`. It is not a generic
package and not designed to be one.

**Fix:** Phase 4 plan now says explicitly that `ccDispatcher` is a
~100-LOC COPY in `cc_im_inbound.go`, NOT a generic extraction. Generic
extraction would require modifying codex's hot path (regression risk
greater than benefit at current scale).

### Revision 3 — synchronous vs async API contract

**v1 implied:** `POST /api/turns` always returns `assistantText` in the
HTTP response body.

**Reality:** Phase 4 IM-driven turns may run for tens of seconds; the
imbridge → agentserver → cc-app-gateway HTTP chain cannot reliably hold
that connection (imbridge timeouts, HTTP load-balancer ceilings, retry
semantics). codex-app-gateway dodges this by returning 202 + `{queued:true}`
from `/api/internal/imbridge/codex/turn` and writing the reply back
through a separate `/api/internal/imbridge/send` POST.

**Fix:** `/api/turns` now takes an optional `callbackUrl` field. Empty →
synchronous response (Phase 1's curl path). Set → async 202+turnId + POST
to callback when done (Phase 4's IM path). Both paths share the same
runTurn(ctx, req) core. Phase 1 scope is sync mode only; the async
branch is a Phase 4 deliverable.

### Other audit findings (no changes needed)

- agent_sessions table exists with the expected columns; adding
  `claude_session_id TEXT` is correct (Phase 2).
- imbridge send URL `/api/internal/imbridge/send` is correct.
- `routing_mode` validator is in code (`internal/imbridgesvc/handlers.go:977`),
  not a DB constraint — Phase 4 fix is a one-line edit, not a migration.
- Port 8087 is genuinely free (confirmed against existing port allocation:
  8080/agentserver, 8081/llmproxy, 8082/sandboxProxy, 8083/imbridge,
  8086/codex-app-gateway, 8090/envmcpPublicGateway, 6060/codex-exec-gateway).
- Dockerfile.claudecode install pattern (`curl -fsSL https://claude.ai/install.sh | bash`)
  is reusable. Resource sizing added as Open Risk #7.
- Helm chart + CI workflow are templated copies of codex-app-gateway equivalents;
  actual effort 2-3 days, not 1 week.

## Acceptance (Phase 1)

A developer can run docker-compose with three services:

```yaml
# docker-compose.yml (acceptance harness)
services:
  fake-agentserver:    # provides /internal/workspace-token
    image: cc-app-gateway-test-tools:dev
    command: fake-agentserver --listen :8080 --workspace-token deadbeef
  fake-llmproxy:        # accepts Bearer deadbeef, returns canned Anthropic responses
    image: cc-app-gateway-test-tools:dev
    command: fake-llmproxy --listen :8081 --accept-token deadbeef --canned-reply "pong"
  cc-app-gateway:
    image: cc-app-gateway:dev
    command: serve --listen :8087
    environment:
      INTERNAL_API_SECRET: secret123
      AGENTSERVER_INTERNAL_URL: http://fake-agentserver:8080
      CCAPPGW_LLMPROXY_URL: http://fake-llmproxy:8081
      CLAUDE_BIN: /usr/local/bin/claude
    ports: ["8087:8087"]
```

Then from the host:

```bash
curl -sX POST http://localhost:8087/api/turns \
  -H "X-Internal-Secret: secret123" \
  -H "Content-Type: application/json" \
  -d '{
    "workspaceId": "ws_test",
    "sessionId":   "00000000-0000-4000-8000-000000000001",
    "userMessage": "Say only the word: pong",
    "model":       "haiku"
  }'
```

Expected within 10 seconds:

```json
{
  "sessionId": "00000000-0000-4000-8000-000000000001",
  "assistantText": "pong",
  "isError": false,
  "durationMs": 1543,
  "totalCostUsd": 0.0001,
  "modelUsage": {"claude-haiku-4-5-20251001": {"inputTokens": ..., "outputTokens": ...}}
}
```

Additional acceptance assertions:

1. **wstoken fetched per call.** fake-agentserver logs one
   `POST /internal/workspace-token` per `/api/turns` call. Token value
   `deadbeef` appears in fake-llmproxy's `Authorization: Bearer ...` header.

2. **Independent sessions don't cross-contaminate.** Re-run with a different
   sessionId → independent turn, jsonl files don't share state.

3. **Same sessionId reuse fails cleanly.** Re-running with the same sessionId
   returns HTTP 4xx with an error message; the gateway does NOT crash. (Resume
   is Phase 2; Phase 1 explicit failure mode is preferred over silent
   misbehavior.)

4. **Pod restart recovery.** `docker-compose restart cc-app-gateway` → next
   curl works immediately. No in-memory state to recover.

5. **Auth failure paths return clean errors.** Calling with wrong
   `X-Internal-Secret` → 401. Calling without it (when secret is set in env)
   → 401. wstoken endpoint down → 503 from `/readyz` and 502 from `/api/turns`.

Optional but recommended: also run against the REAL pinned claude binary
inside the gateway image (not a mock) — this is what catches CLI flag/schema
regressions on version bump.

**Phase 1 acceptance harness:** `internal/ccappgateway/testdata/integration/` carries a docker-compose harness (fake-agentserver + fake-llmproxy + cc-app-gateway:dev with real pinned claude 2.1.185) plus a build-tagged Go integration test (`go test -tags integration ./internal/ccappgateway/...`). The test brings the stack up, POSTs `/api/turns`, and asserts the canned `"pong"` is returned as `assistantText`. CI runs this as the `integration-cc-app-gateway` job on every commit that touches `internal/ccappgateway/`, `cmd/cc-app-gateway/`, `cmd/cc-app-gateway-test-tools/`, or `Dockerfile.cc-app-gateway`.

## Migration

There is no prior `cc-app-gateway` to migrate from. PR #135 left
agentserver with no Claude-Code-side managed-harness path; this spec
re-introduces one in a fresh package (`internal/ccappgateway`) so there
is no ambiguity between "old cc-broker code" and "new cc-app-gateway
code". The 4 cc-broker specs (2026-05-02 through 2026-05-04) remain in
the specs/ directory for historical context; the parts of them this
spec inherits are explicitly noted in this document's header.

When Phase 4 lands (IM intake), the imbridge routing_mode enum gains a
`managed_cc` variant. Existing `codex` and `stateless_cc` (the latter
being a dead alias post-PR#135) entries are untouched.

## Plan files

- `docs/superpowers/plans/2026-06-21-cc-app-gateway-phase1.md`
  (new plan — to be written; TDD task list for Phase 1).
- Phase 2-5 plans are deferred until Phase 1 ships.
