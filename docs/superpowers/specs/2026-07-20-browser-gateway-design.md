# browser-gateway design — codex harness as a standard AG-UI agent endpoint

- **Date:** 2026-07-20
- **Status:** Approved (design); ready for implementation plan
- **Author:** design brainstormed with the user
- **Related:** `2026-06-21-cc-app-gateway-design.md` (sibling stream translator),
  `2026-05-10-codex-app-gateway-subprocess.md` (codex app-server subprocess model),
  `2026-05-05-codex-app-gateway-and-exec-gateway-design.md` (codex ServerNotification surface)

## 1. Summary

Add a new standalone component, **browser-gateway**, that exposes the existing
codex harness as a **standard [AG-UI](https://github.com/ag-ui-protocol/ag-ui)
agent endpoint** over HTTP + Server-Sent Events (SSE). A browser (or any
AG-UI-compatible frontend) POSTs a `RunAgentInput`; browser-gateway drives one
codex *turn* and streams standard AG-UI events back as they arrive. For known
interaction points (`commandExecution`, `fileChange`) the gateway
**synthesizes [A2UI](https://github.com/a2ui-project/a2ui) generative-UI
payloads** server-side and carries them inside AG-UI `CUSTOM` events. A
CopilotKit-based reference frontend ships alongside to validate the endpoint.

The primary deliverable is the **standard endpoint**; the frontend is a
reference/demo. HITL/approvals are out of scope for v1 (codex's existing
auto-accept behavior is preserved), but the wire shape is reserved.

## 2. Goals / non-goals

### Goals (v1)
- A new standalone binary `cmd/browser-gateway` + `internal/browsergateway` +
  `Dockerfile.browser-gateway`, following the established gateway pattern.
- `POST /agui` — accept `RunAgentInput`, stream AG-UI events over SSE.
- Translate the codex v2 app-server streaming frames into AG-UI events
  (lifecycle, text message, reasoning, tool call).
- Synthesize A2UI cards for `commandExecution` and `fileChange` items,
  delivered as AG-UI `CUSTOM` events (framework-neutral carrier).
- A CopilotKit reference SPA (`browserweb/`) embedded and served by the gateway.
- **Zero changes to codex-app-gateway.**

### Non-goals (v1) — deferred, interfaces reserved
- **HITL / approvals.** codex is configured `default_tools_approval_mode =
  "approve"` and `/codex-app/ws` already auto-accepts + drops the 5 approval
  request frames. v1 keeps that. The AG-UI interrupt/resume types
  (`RunFinishedOutcome{type:"interrupt"}` + `Resume []ResumeEntry`) and the
  A2UI approval-card synthesis are reserved for a later phase.
- **A2UI interactive callbacks** (buttons that call back into the agent). v1
  A2UI cards are display-only. Routing `client_to_server` actions into codex is
  deferred.
- **Frontend-declared tools** (`RunAgentInput.tools`) — ignored in v1.
- **Full-history replay** — codex owns thread history server-side; we send only
  the latest user message per turn.

## 3. Background (grounding facts from reconnaissance)

### 3.1 The codex streaming surface (agentserver repo)

- `POST /api/turns` (`internal/codexappgateway/turn_api.go:57`) is **blocking**:
  `broker.Conn.Turn` (`internal/codexappgateway/broker/conn.go:299-450`) sends
  `turn/start`, waits on a single channel for `turn/completed`, and returns only
  the final Turn JSON. Intermediate frames are accumulated into a map
  (`conn.go:169-183`) or dropped (`conn.go:205`). **Not usable for streaming.**
- `GET /codex-app/ws` (and its `/` alias) — `handleCodexAppWS`
  (`internal/codexappgateway/server.go:374-511`) is a **raw bidirectional ws
  proxy** built on `wsbridge.RunProxyWithInterceptor`
  (`internal/wsbridge/wsbridge.go:104-157`). It forwards **every** codex v2
  JSON-RPC notification frame to the client verbatim. Its interceptor
  (`server.go:457-506`) only: (a) blocks `command/exec*` / `fs/*` client RPCs
  (`intercept_local_io.go`), and (b) **auto-accepts and drops** the 5 approval
  request frames (`approvalfilter`, `server.go:493-504`). **This is the
  streaming source browser-gateway builds on.**
- Codex subprocess lifecycle, S3 `CODEX_HOME` persistence, and the
  env-mcp→exec-gateway boundary all live in codex-app-gateway's
  `supervisor`/`broker`. browser-gateway reuses them by being a client of
  `/codex-app/ws` — it does **not** spawn codex.
- Auth on `/codex-app/ws`: Bearer token, verified in production by
  `RemoteVerifier` (`internal/codexappgateway/auth/remote_verifier.go:30-84`),
  which POSTs to agentserver `POST /api/internal/codex/tokens/verify` and
  returns `{user_id, workspace_id}`. The token *is* the workspace binding.

### 3.2 codex v2 event/item inventory (to translate)

Verified wire methods (client→server): `initialize`, `initialized`,
`thread/start`, `thread/resume`, `turn/start`, `turn/interrupt`.

Server→client notifications (no id) — pass through the proxy untouched:
- `turn/started`, `turn/completed`, `item/started`, `item/completed`
- `item/agentMessage/delta` — incremental text deltas *(name per design docs;
  **must be confirmed against pinned codex 0.137.0** — see Phase 0)*
- `thread/started`, `thread/status/changed`, top-level `error`

Item types carried in `item.type` (camelCase in v2): `agentMessage`,
`reasoning`, `commandExecution`, `fileChange`, `userMessage`. A Turn object
carries `status`, `itemsView`, `items`, `error`, `startedAt`, `completedAt`,
`durationMs`, and usage.

There are **no vendored codex Go protocol types** in the repo — browser-gateway
defines its own minimal structs against the pinned codex tag.

Sibling pattern to mirror: `internal/ccappgateway/runner/events.go` —
`KeepFrame` (`events.go:25-40`) + typed structs + "unknown frame → keep and log
a warning" philosophy, with `events_test.go` + `testdata/`.

### 3.3 AG-UI (protocol + Go SDK)

- Module: `github.com/ag-ui-protocol/ag-ui/sdks/community/go`, Go 1.24 (this
  repo is Go 1.26 — compatible).
- **Use from the SDK:**
  - `pkg/core/events` — all 30 event structs, `EventType` constants, `New*`
    constructors, `Validate()`, ID generators (`GenerateThreadID/RunID`).
  - `pkg/core/types` — `RunAgentInput` (`types.go:342-362`: `threadId`, `runId`,
    `parentRunId`, `state`, `messages`, `tools`, `context`, `forwardedProps`,
    `resume`) with camelCase/snake_case tolerant `UnmarshalJSON`; plus
    `Message`, `Tool`, `Context`, `Interrupt`, `ResumeEntry`.
  - `pkg/encoding/sse` — **`SSEWriter.WriteEvent(ctx, io.Writer, Event)`**
    (`writer.go:40`): correct `data:`+`\n\n` framing, newline escaping,
    auto-flush via `http.Flusher`. (The piece most likely to be hand-rolled
    wrong — use it.)
- **Hand-write (no SDK support):** the HTTP endpoint (route, `RunAgentInput`
  unmarshal, the 3 SSE headers), the run loop / emitter (`RUN_STARTED` … terminal
  `RUN_FINISHED`/`RUN_ERROR`, panic→`RUN_ERROR`, per-write error handling), CORS,
  auth, keep-alive, and the codex→AG-UI mapping. There is **no** `Agent`/
  `Server`/`Handler`/`HTTPAgent` type in the Go SDK. Reference pattern:
  `example/server/internal/agent/emitter.go` (~200 LOC, example-only).
- Server contract: `POST` to an agent-defined path; body = `RunAgentInput` JSON;
  response `200 text/event-stream`, one AG-UI event JSON per `data:` frame; echo
  the client's `threadId`/`runId` (generate if empty). `threadId` = conversation,
  `runId` = one turn.
- HITL (reserved): terminal-interrupt model — the run ends with `RUN_FINISHED`
  carrying `outcome:{type:"interrupt", interrupts:[...]}`; the client resumes
  with a new run on the same `threadId` and a `Resume []ResumeEntry`. Already
  typed in the Go SDK.

### 3.4 A2UI (protocol; synthesized server-side)

- A2UI is a **stream of message objects**, each `{version, <oneKey>:{...}}`.
  **Target v0.9 wire** (`"version":"v0.9"`) — matches today's renderers and
  CopilotKit. Four message types: `createSurface`, `updateComponents`,
  `updateDataModel`, `deleteSurface`. (v1.0 is a release *candidate*; its extra
  `callFunction`/`actionResponse` and inline-components-in-`createSurface` are
  not needed.)
- **Component model:** flat adjacency list; exactly one `id:"root"`; the client
  reconstructs the tree from id refs. Single-child containers (`Card`, `Button`,
  `Modal`) use **`child`** (a component-id string); multi-child containers
  (`Column`, `Row`, `List`, `Tabs`) use **`children`** (array of ids, or a
  `{componentId, path}` list template).
- **Basic catalog** component `type` strings: `Text, Image, Icon, Video,
  AudioPlayer, Row, Column, List, Card, Tabs, Modal, Divider, Button, TextField,
  CheckBox, ChoicePicker, Slider, DateTimeInput`. `catalogId` =
  `https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json`.
- **Data binding:** any bindable prop is `literal | {path:"/ptr"} | {call,args}`.
  Content is supplied/patched via `updateDataModel` (JSON Pointer).
- **No Go producer SDK** (only Python). We **hand-roll the JSON in Go**; the
  source of truth for struct shapes and runtime validation is the JSON Schema at
  `/root/a2ui/specification/v0_9/json/*.json` +
  `catalogs/basic/catalog.json` (draft-2020-12).
- **Carrier over AG-UI:** the exact A2UI↔AG-UI mapping lives in CopilotKit's
  runtime (external to both repos). For our server-synthesized flow the
  framework-neutral choice is to place the A2UI message array inside an AG-UI
  **`CUSTOM`** event `{name:"a2ui.operations", value:[...messages...]}`. The
  reference frontend renders those via `@a2ui/react`
  (`MessageProcessor` + `<A2uiSurface>`). (Alternative, deferred: emit the array
  as the arguments of a `generate_a2ui` tool call so CopilotKit's native
  `A2UIMiddleware` renders it — requires CopilotKit's Node runtime.)

## 4. Architecture

Topology (chosen: standalone component + ws client; codex-app-gateway unchanged):

```
Browser SPA  (CopilotKit + @ag-ui/client + @a2ui/react)
   │  POST /agui  {RunAgentInput}  →  text/event-stream  (AG-UI events)
   │  Authorization: Bearer <workspace codex token>
   ▼
browser-gateway
   • serves reference SPA (embed) + AG-UI SSE endpoint
   • translates: RunAgentInput → codex turn/start;
                 codex frames → AG-UI events;
                 command/file items → A2UI cards (CUSTOM events)
   │  codex v2 JSON-RPC over ws   (forwards the same Bearer)
   ▼
codex-app-gateway   /codex-app/ws   (ZERO changes)
   • supervisor: codex app-server per workspace + S3 persistence
   • exec/fs boundary + approval auto-accept
   ▼
codex app-server subprocess
```

### 4.1 Authentication — Bearer pass-through
The SPA holds a **workspace-scoped codex token** — the same token codex
`--remote` / the "codex browsers" surface uses. **This already exists:** the
console mints these via `POST /api/codex/tokens`
(`internal/server/codex_tokens.go:15`), and they are exactly what
`RemoteVerifier` validates via `POST /api/internal/codex/tokens/verify`. So
browser-gateway needs **no new auth**. The SPA sends the token as
`Authorization: Bearer <token>` on the `POST /agui` request. browser-gateway
forwards the identical Bearer when dialing `/codex-app/ws`; codex-app-gateway's
`RemoteVerifier` validates it and resolves `{user_id, workspace_id}`.
**browser-gateway is near-stateless** — it does not maintain its own session
store. (Optional early-reject: browser-gateway may verify the token itself via
the same internal API before opening the ws; not required for v1.)

### 4.2 Session / thread model
- `AG-UI threadId == codex threadId`. The Bearer token binds the workspace, and
  a codex thread lives inside that workspace's subprocess, so no server-side
  mapping table is needed.
- Empty `threadId` → codex `thread/start` → the returned codex thread id is
  echoed as `threadId` in `RUN_STARTED`.
- Non-empty `threadId` → codex `thread/resume(threadId)` then `turn/start`.
- `runId` maps to a single codex turn (generated per run if the client omits it).
- The turn input is the **latest user message** from `RunAgentInput.messages`;
  codex maintains the rest of the thread history server-side.

## 5. Component breakdown (`internal/browsergateway/`)

Each unit has one purpose, a narrow interface, and is testable in isolation.

- **`config.go`** — `ServeConfig` + `LoadServeConfigFromEnv()` (mirrors
  `codexappgateway/config.go`). Env prefix `BRG_`:
  - `BRG_LISTEN_ADDR` (default `:8088`; browser-gateway is its own pod / network
    namespace, so the port number is free — it cannot collide with other
    gateways. Only host-published ports in `docker-compose` need to differ.)
  - `BRG_CODEX_APP_GATEWAY_WS_URL` (e.g. `ws://codex-app-gateway:8086`) — base to
    dial `/codex-app/ws`
  - `BRG_ALLOWED_ORIGINS` (CORS allowlist; `*` for dev)
  - `BRG_LOG_LEVEL`
  - *(optional)* `BRG_AGENTSERVER_INTERNAL_URL` + `BRG_AGENTSERVER_INTERNAL_SECRET`
    if early token verification is enabled.
  Simpler than `CXG_*` — no S3, no exec-gateway, no codex binary config.

- **`server.go`** — `net/http` (or chi, matching repo idiom) server:
  - `POST /agui` → the run handler (§6).
  - `GET /healthz` → open.
  - `GET /*` → embedded SPA from `browserweb`.
  - CORS middleware (`Origin`, `Content-Type`, `Accept`, `Cache-Control`,
    `Authorization`; methods `GET, POST, OPTIONS`).
  - Bearer extraction.

- **`run.go`** (emitter + loop) — parse `RunAgentInput`; set SSE headers; emit
  `RUN_STARTED`; drive `codexclient`; pump mapper output through
  `sse.SSEWriter.WriteEvent`; terminal `RUN_FINISHED`/`RUN_ERROR`; recover panics
  into `RUN_ERROR`. Uses the AG-UI Go SDK for events + SSE framing.

- **`codexclient/`** — a **streaming** codex v2 ws client: dial
  `/codex-app/ws` (forward Bearer), `initialize`/`initialized` handshake,
  `thread/start` or `thread/resume`, `turn/start`, then a read loop that surfaces
  every notification frame on a channel until `turn/completed`. Exposes a
  `turn/interrupt` cancel path. May share handshake/framing helpers with
  `broker` but is deliberately **not** `broker.Turn` (which is blocking and drops
  intermediate frames). Enforces the same client-side discipline as the proxy: it
  never emits `command/exec*` / `fs/*` RPCs.

- **`mapper/`** — pure translation `codex frame → []agui.Event`. Typed structs
  for the minimal codex subset; `KeepFrame`-style filtering; unknown frames
  logged + skipped (optionally surfaced as AG-UI `RAW`). No I/O — trivially
  unit-testable with recorded frames.

- **`a2ui/`** — hand-rolled Go structs mirroring the A2UI v0.9 JSON Schema, plus
  builders: `CommandCard(item) []Message` and `FileDiffCard(item) []Message`.
  Output validated against the JSON Schema in tests.

- **`browserweb/`** (top-level, sibling to `web/` and `opencodeweb/`) — CopilotKit
  + Vite + React 19 + Tailwind reference SPA; `embed.go` (`go:embed dist`);
  consumes `POST /agui` via `@ag-ui/client` `HttpAgent`; renders `a2ui.operations`
  CUSTOM events through `@a2ui/react`.

- **`cmd/browser-gateway/main.go`** — flag parsing with a `serve` subcommand,
  `LoadServeConfigFromEnv`, signal handling (mirrors
  `cmd/codex-app-gateway/main.go`).

## 6. Data flow (one run, end to end)

1. SPA `POST /agui` with `Authorization: Bearer <token>` + `RunAgentInput`.
2. Handler validates the Bearer (401 on failure) and `RunAgentInput` (400 on
   malformed body); dials `/codex-app/ws` forwarding the Bearer; sets
   `Content-Type: text/event-stream`, `Cache-Control: no-cache`,
   `Connection: keep-alive`; emits `RUN_STARTED{threadId, runId}`.
3. `codexclient`: `initialize` → (`thread/start` if new, else `thread/resume`) →
   `turn/start` with the latest user message.
4. For each codex notification frame: `mapper` → 0..N AG-UI events →
   `SSEWriter.WriteEvent`. (Mapping table in §7.)
5. `turn/completed` → `RUN_FINISHED{result: usage}`. Any transport/RPC error →
   `RUN_ERROR{message, code}`.
6. If the client disconnects (write error / context cancel) → send
   `turn/interrupt` to codex and close the ws cleanly.

## 7. Protocol translation

### 7.1 codex → AG-UI mapping

| codex frame | AG-UI event(s) |
|---|---|
| `turn/started` | (none — `RUN_STARTED` already emitted at run open) |
| `item/started` type `agentMessage` | `TEXT_MESSAGE_START{messageId=itemId, role:"assistant"}` |
| `item/agentMessage/delta` ※ | `TEXT_MESSAGE_CONTENT{messageId, delta}` |
| `item/completed` type `agentMessage` | `TEXT_MESSAGE_END{messageId}` |
| `item/*` type `reasoning` | `REASONING_MESSAGE_START` / `_CONTENT` / `_END` |
| `item/started` type `commandExecution` | `TOOL_CALL_START{toolCallId=itemId, toolCallName:"shell"}` + `TOOL_CALL_ARGS{delta: command}` |
| `item/completed` type `commandExecution` | `TOOL_CALL_END` + `TOOL_CALL_RESULT{content: output}` + **`CUSTOM{name:"a2ui.operations", value:[command card]}`** |
| `item/completed` type `fileChange` | `TOOL_CALL_START/END` + `TOOL_CALL_RESULT` + **`CUSTOM{name:"a2ui.operations", value:[diff card]}`** |
| `item/completed` type `userMessage` | (skip — client already has it) |
| `turn/completed` | `RUN_FINISHED{threadId, runId, result: usage}` |
| top-level `error` / transport failure | `RUN_ERROR{message, code}` |
| unknown frame | log warning; skip (optionally forward as `RAW`) |

※ The exact delta method name (`item/agentMessage/delta`) and item schemas are
**pinned in Phase 0** against codex 0.137.0 (§10). Treat this table as the
design intent; the recorded-frame fixtures are the source of truth for the code.

### 7.2 A2UI synthesis (v0.9)

For a `commandExecution` item the gateway emits an ordered message array, e.g.:

```json
[
  { "version": "v0.9", "createSurface": {
      "surfaceId": "cmd-<itemId>",
      "catalogId": "https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json",
      "sendDataModel": true } },
  { "version": "v0.9", "updateComponents": { "surfaceId": "cmd-<itemId>", "components": [
      { "id": "root", "component": "Card", "child": "col" },
      { "id": "col", "component": "Column", "children": ["cmd", "out", "exit"] },
      { "id": "cmd",  "component": "Text", "text": { "path": "/command" } },
      { "id": "out",  "component": "Text", "text": { "path": "/output" } },
      { "id": "exit", "component": "Text", "text": { "path": "/exitLine" } } ] } },
  { "version": "v0.9", "updateDataModel": { "surfaceId": "cmd-<itemId>",
      "value": { "command": "...", "output": "...", "exitLine": "exit 0" } } }
]
```

`fileChange` is analogous (a diff card). `surfaceId` is globally unique per item.
The array is placed in the AG-UI `CUSTOM` event value. v1 cards are
**display-only** (no `action`/callbacks).

## 8. Error handling

- codex transport errors reuse codex-app-gateway's taxonomy
  (`brokerTimeout` / `codexRPCError` / `subprocessCrash` / `wsDisconnect`,
  `turn_api.go:121-145`) → mapped into `RUN_ERROR.code`.
- SSE write failure / client disconnect → cancel context, send `turn/interrupt`
  to codex, close ws.
- Panic in the run loop → recover → single `RUN_ERROR` frame (emitter pattern).
- Unknown codex frame → log warning and skip (prefer over-logging to silent
  drops); optionally forward as AG-UI `RAW` for observability.
- Bearer missing/invalid → `401` **before** opening the SSE stream.
- Malformed `RunAgentInput` → `400`.

## 9. Testing strategy

- **`mapper` (unit, table-driven):** recorded codex frames (`testdata/*.jsonl`)
  → assert the AG-UI event sequence. Mirrors `ccappgateway/runner/events_test.go`
  + `testdata/`.
- **`a2ui` (golden):** synthesize command/diff cards → compare to golden JSON
  **and** validate against the A2UI v0.9 JSON Schema (draft-2020-12) at test time.
- **`codexclient` (unit):** a fake codex ws server emitting canned frames →
  assert handshake + streaming behavior + `turn/interrupt` on cancel.
- **Integration:** browser-gateway wired to a fake `/codex-app/ws` (emitting
  canned codex frames) → `POST /agui` → decode the SSE body with the SDK's
  `events.EventDecoder` and assert a valid AG-UI stream (`RUN_STARTED` …
  `RUN_FINISHED`). Mirrors `codexappgateway/integration_test.go`.
- **Frontend:** minimal smoke (SPA renders a streamed message); optional in v1.

## 10. Phase 0 spike (do first)

cc-app-gateway started with a "Phase 0 probe"; browser-gateway does the same.
Against **pinned codex 0.137.0**, run one real turn through `/codex-app/ws` and
record the actual notification frames. Deliverable: a fixture set + a confirmed
mapping table (esp. the `item/agentMessage/delta` method name and the
`commandExecution` / `fileChange` item JSON schemas). Everything in §7 is
provisional until this lands; the fixtures become the `mapper` testdata.

## 11. Deployment & wiring

Follows the established gateway pattern (each gateway = `Dockerfile.<name>` + a
CI `build-<name>` job + a chart `templates/<name>.yaml` + a `values.yaml` block +
httproute/ingress entry):

- **`Dockerfile.browser-gateway`** — two-stage Go build (`golang:1.26-trixie` →
  `debian:trixie-slim`), copying the binary **and** the embedded `browserweb`
  dist. No codex binary, no bubblewrap, no S3 — much simpler than
  `Dockerfile.codex-app-gateway`. `EXPOSE 8088`; `ENTRYPOINT
  ["/usr/local/bin/browser-gateway"]`; `CMD ["serve"]`.
- **CI** (`.gitlab-ci.yml`) — a `build-browser-gateway` job + a
  `BROWSER_GATEWAY_IMAGE` variable + path rules (`Dockerfile.browser-gateway`,
  `cmd/browser-gateway/**/*`, `internal/browsergateway/**/*`, `browserweb/**/*`).
- **Makefile** — a build target; the `frontend` target (or a new one) runs
  `cd browserweb && pnpm install && pnpm build` before the Go build so the embed
  dist exists.
- **Helm** — `deploy/helm/agentserver/templates/browser-gateway.yaml`
  (Deployment + Service), a `browserGateway:` block in `values.yaml`, a public
  host in `httproute.yaml`/`ingress.yaml`, and a `--set
  browserGateway.image.repository=...` in the deploy job.
- **Chart version bump + `v<version>` git tag** on release, per the repo release
  flow (otherwise images only get `:latest`/`:sha` and pulumi ImagePullBackOffs).

## 12. Dependencies

- **Go (new):** `github.com/ag-ui-protocol/ag-ui/sdks/community/go` — used only
  for `pkg/core/events`, `pkg/core/types`, `pkg/encoding/sse`. Community-tier;
  Go 1.24 (builds under this repo's Go 1.26). *Alternative considered:* vendor /
  hand-copy just those types to avoid the dependency. **Decision: take the
  dependency** — the SSE framing + 30 event structs are high-value and
  error-prone to reproduce.
- **Frontend (new, in `browserweb/`):** `@copilotkit/react-core`,
  `@copilotkit/react-ui`, `@ag-ui/client`, `@a2ui/react` (+ `@a2ui/web_core`),
  on the existing Vite + React 19 + Tailwind 4 toolchain.

## 13. Risks

- **CopilotKit's native A2UI rendering leans on its Node runtime.** v1 avoids
  that by using the standard endpoint + `CUSTOM`-event carrier +
  `@a2ui/react` client-side rendering, keeping a single Go binary deploy. Full
  CopilotKit-runtime A2UI middleware integration is deferred (§2 non-goals).
- **codex frame drift.** The delta method name and item schemas are pinned in
  Phase 0 against a specific codex tag; a codex upgrade may shift them. The
  "unknown frame → log + skip" rule bounds the blast radius.
- **Long silent turns.** codex can be silent for minutes between tool calls; the
  SSE connection needs keep-alive. `/codex-app/ws` already pings the child ws;
  browser-gateway must also keep the browser SSE alive (periodic comment frame or
  a benign event) to defeat middlebox idle timeouts.

## 14. Phasing

- **P0** — codex frame probe + confirmed mapping table + fixtures (§10).
- **P1** — Go endpoint minimal loop: plain-text chat streaming (`RUN_STARTED` /
  `TEXT_MESSAGE_*` / `RUN_FINISHED`) + fake-codex integration test.
- **P2** — tool events + A2UI card synthesis (command / file).
- **P3** — CopilotKit reference SPA + embed + deployment wiring.
- **P4 (reserved)** — HITL, A2UI interactive callbacks, CopilotKit-runtime native
  A2UI.

## 15. Open questions / future work

- **Token handoff UX (small, not a subsystem).** The workspace codex token the
  SPA carries already exists (`POST /api/codex/tokens`, §4.1). Open only: does
  the user mint/paste it in the console (as codex CLI users do today), or does
  the console add an "open chat" link that forwards the token to the SPA?
  Decide during P3 (reference frontend).
- **CopilotKit-native A2UI carrier (deferred, P4).** Optionally also emit the
  A2UI array as the arguments of a `generate_a2ui` tool call
  (`TOOL_CALL_START{name:"generate_a2ui"}` / `TOOL_CALL_ARGS{delta}` /
  `TOOL_CALL_END`) so CopilotKit's `A2UIMiddleware` renders it out of the box.
  Trade-off: binds the wire to a CopilotKit convention and its cleanest path
  wants CopilotKit's Node runtime. v1 stays framework-neutral (`CUSTOM` event).
