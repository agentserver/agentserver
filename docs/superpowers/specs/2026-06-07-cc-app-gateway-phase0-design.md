# cc-app-gateway Phase 0 (PoC-2) — Subscribe-Half Protocol + Backend Harness Selection

**Date:** 2026-06-07
**Status:** Draft — design awaiting user sign-off
**Author:** mryao + Claude (Opus 4.8)
**Predecessor:** `docs/superpowers/specs/2026-06-06-cc-cloud-thin-client-poc-design.md` (PoC-1, PASSed 2026-06-07 with 2 patches)
**Successor:** Phase 1 v0 cc-app-gateway design spec — to be authored after Phase 0 lands; filename/date assigned at that time.
**Related memory:** `agent_integration_strategy.md`, `cc_binary_internals.md`
**Reference source (use sparingly — early version):** `/root/cc/source/src/bridge/` (v2.1.88)

## 1. Problem Statement

The goal is a **cc-app-gateway** in the shape of the existing
`internal/codexappgateway/`: a per-request fork harness where the
user's local CLI is a thin terminal and the agent loop lives in a pod.
PoC-1 proved the *submit half* of this is feasible: `claude --cloud`
can be redirected to a non-Anthropic gateway with 2 same-length binary
patches, an OAuth env stub, and ~9 mock HTTP routes. The launcher
uploads the prompt as an embedded event in `POST /v1/sessions`, prints
a teleport URL, and exits 0 — no live bidirectional channel involved.

Two open questions block a proper cc-app-gateway design:

1. **Subscribe-half wire format.** A real cc-app-gateway needs the
   *bidirectional* channel — the one the resume client
   (`claude --teleport <sid>` and claude.ai/code web) uses to receive
   the cloud session's live frames. Static binary analysis points to a
   v2 CCR transport: `SSETransport` (reads) + a `POST /worker/*`
   write path, gated by `tengu_bridge_repl_v2` and bootstrapped via
   `POST /v1/code/sessions/{id}/bridge` → `worker_jwt` (per
   `/root/cc/source/src/bridge/remoteBridgeCore.ts` v2.1.88). This has
   never been exercised end-to-end against our mock; the wire shape we
   inferred could be wrong in subtle ways that only surface when the
   binary actually subscribes.

2. **Backend harness choice.** cc-app-gateway has to fork *some*
   claude subprocess per turn. Two candidates exist and each has open
   risks:
   - **A:** the same patched `claude --cloud` PoC-1 used. Same patches,
     same binary, no second strain to maintain. But `--cloud` is
     fire-and-forget; we'd be using it for a use it wasn't designed
     for, harvesting frames from stdout/telemetry rather than the
     "natural" bridge:repl channel.
   - **B:** plain `claude -p '<prompt>' --output-format stream-json`.
     No patches needed for the backend (still need patches on the
     thin-client *frontend* binary). But we'd be translating
     stream-json frames into v2 transport frames in the gateway —
     extra glue, possible format-fidelity loss for `tool_use` /
     `thinking` / `permission_request`.

Both questions must be answered before a cc-app-gateway v0 spec is
worth writing. Phase 0 (this spec) answers them.

## 2. Goal

Within ~2-3 days, deliver:

1. **A.1 — Subscribe-half verified.** Extend the PoC-1 mock gateway
   with the v2 transport endpoints (`/bridge`, SSE events stream,
   `POST /worker/*`). Run `claude --teleport <sid>` (using PoC-1's
   patched binary) against the mock and confirm the TUI renders a
   synthetic `assistant_message` we push through the SSE channel.
2. **A.2 — Backend A trialled.** Drive PoC-1's patched
   `claude --cloud 'hello'` from a small adapter; capture its
   per-turn output; feed those frames into the A.1 mock's SSE stream
   so the teleport TUI sees the actual model output (text + thinking
   + tool_use).
3. **A.3 — Backend B trialled.** Same end-state via
   `claude -p '<prompt>' --output-format stream-json`, translating
   each stream-json line into a thin-client v2 frame.
4. **A.4 — Decision matrix.** A 5-dimension comparison of A vs B
   covering token usage per turn, wall-clock latency, frame fidelity
   (tool_use / thinking / text / permission_request), controllability
   (cancel / interrupt / permission prompts), and maintenance
   (per-release patch count, upgrade fragility).

No production code. No in-tree changes beyond this spec, its outcome
section, and follow-on memory updates.

## 3. Scope

In-scope:

- Mock-gateway extensions for the v2 CCR transport endpoints, only as
  deep as the binary requires to render an assistant frame.
- Two thin Python drivers (`driver_a.py`, `driver_b.py`) that bridge
  a subprocess's output into the mock's SSE queue.
- One round-trip per backend that proves a complete model turn
  (containing at minimum `text`, ideally also `thinking` and one
  `tool_use`) reaches the teleport TUI.
- A `decision-matrix.md` capturing the A/B comparison.
- Stuck-point reporting per spec §6 limits.

Out-of-scope:

- Any cc-app-gateway production code (`internal/ccappgateway/`,
  `cmd/cc-app-gateway/`, Dockerfile, Helm). That is Phase 1.
- Workspace-token / billing / multi-tenant — Phase 1 concerns.
- Per-thread codexhome equivalent (tarballs, MCP, scheduler) — Phase 1.
- A real Anthropic-account token. We continue with the fake-OAuth env
  + `.claude-custom-oauth.json` pre-seed established in PoC-1.
- WebSocket support. `/root/cc/source/src/bridge/replBridgeTransport.ts`
  v2.1.88 deprecates the WS v1 path in favor of v2 SSE+POST. v2.1.167
  binary string analysis matches; we mock v2 only.

## 4. Approach

Sequential sub-phases, gated.

- **A.1 is the hard gate.** If A.1 cannot pass, A.2 and A.3 cannot be
  attempted (they both inject into the SSE channel A.1 builds and
  validates). A.1 failure ends Phase 0 with a "subscribe-half
  infeasible" report.
- **A.2 and A.3 are independent of each other.** If one fails the
  other is still attempted; the decision matrix in A.4 then describes
  one-sided results honestly.

```
A.1 mock v2 endpoints  →  A.2 backend A driver  ─┐
        │                                        ├→  A.4 decision matrix
        └──→  A.3 backend B driver  ─────────────┘
```

We run sub-phases in the order A.1 → A.2 → A.3 → A.4 to share the
mock infrastructure A.1 builds. If A.1 reveals the wire format is
fundamentally different from what static analysis suggested, we
update this spec's §6 and re-plan rather than soldier on.

## 5. Architecture

Builds on PoC-1's workspace under `/tmp/cc-cloud-poc/`. No new
top-level layout — same `mock_gateway.py`, `patch_cli.py`,
`run_poc.sh`, plus three new files.

```
                  PoC-1 (done, PASSed)               PoC-2 (this spec)
                  ────────────────────               ─────────────────
[patched cc 167] ─submit half─> [mock_gw v2] <─SSE/POST─ [claude --teleport sid]   (A.1)
                                      ▲                          │
                                      │                          └─ proves: subscribe
                                      │                             channel bidirectional,
                                      │                             TUI renders inbound frames
                                      │
   [patched cc 167 --cloud] ──stdout/telemetry──> [driver_a.py] ──> mock SSE queue (A.2)

   [plain cc 167 -p stream-json] ──────stream-json──> [driver_b.py] ──> mock SSE queue (A.3)

   /tmp/cc-cloud-poc/decision-matrix.md                                              (A.4)
```

### 5.1 `mock_gateway.py` — new endpoints

Add to the existing aiohttp server (PoC-1 iteration 9 left it at 9
routes; we add roughly these — names finalized iteratively as the
binary probes):

- `POST /v1/code/sessions/{sid}/bridge` — returns
  `{worker_jwt, worker_epoch, api_base_url, expires_in}`. JWT is any
  unverified opaque string; the binary doesn't cryptographically
  verify it locally (it's bearer-passed to subsequent `/worker/*`
  calls). `worker_epoch` is a monotonic integer per `/bridge` call.
- `GET /v1/code/sessions/{sid}/events/stream` — SSE long-running
  response. Each pushed frame is one `data: <json>\n\n`. The mock
  keeps an in-memory queue per `sid`; a writer (driver_a / driver_b /
  manual `curl`) appends frames, the SSE handler drains them and
  marks them processed.
- `POST /worker/events/{event_id}/delivery` — accepts
  `{status: "processing"|"processed"}`, just logs.
- `PUT /worker/state` and `PUT /worker/external_metadata` — accept
  any body, log it. Required only if the binary refuses to subscribe
  without them.

Plus a tiny **frame-injection HTTP API** on the same port — sibling
to the mock-anthropic surface but used by our drivers:

- `POST /_poc/sessions/{sid}/inject` — body is any JSON frame; mock
  enqueues it onto sid's SSE queue verbatim.

This `_poc/*` namespace is the driver→mock interface; it never appears
in the Anthropic surface and so can't collide with anything the binary
hits.

### 5.2 `driver_a.py` — backend A adapter

Forks `patched-claude --cloud '<prompt>'` against a localhost mock
configured per PoC-1. Captures:

- stdout (Ink alt-screen text — limited use; mostly the teleport URL),
- the telemetry NDJSON under `$CLAUDE_CONFIG_DIR/telemetry/`
  (where the launcher writes every event it would have uploaded),
- the bodies of `POST /v1/sessions` and `POST /api/event_logging/v2/batch`
  the mock receives (which carry the prompt and event frames).

Translates those into v2 transport frame shape and `POST /_poc/.../inject`
into the mock. Frame shape derived from `/root/cc/source/src/bridge/bridgeMessaging.ts` v2.1.88; expected to roughly look like:

```json
{
  "type": "event",
  "data": {
    "uuid": "<uuid>",
    "session_id": "<sid>",
    "type": "assistant",
    "message": {
      "role": "assistant",
      "content": [{"type": "text", "text": "..."}]
    }
  }
}
```

### 5.3 `driver_b.py` — backend B adapter

Forks `plain-claude -p '<prompt>' --output-format stream-json`. Reads
each stream-json line from stdout (well-documented format) and
translates to the same v2 frame shape, then injects. No patches
needed for backend B's subprocess (still need the
thin-client-frontend patches for `--teleport` to talk to the mock).

### 5.4 `decision-matrix.md`

5-dimension scorecard (×3 backends including a "real Anthropic"
baseline if achievable, but the comparison is A vs B):

| Dimension | A (patched --cloud) | B (claude -p stream-json) | Notes |
|---|---|---|---|
| Token usage per turn | tokens in/out from telemetry | tokens in/out from stream-json | n=3 runs same prompt |
| Wall-clock latency | first-frame & last-frame ms | first-frame & last-frame ms | n=3 runs |
| Frame fidelity: text | yes / partial / no | yes / partial / no | "partial" = frame reaches TUI but with a sub-field lost (e.g. role missing, text truncated). |
| Frame fidelity: thinking | yes / partial / no | yes / partial / no | same definition. Skip with N/A if the model doesn't emit thinking for the prompt used. |
| Frame fidelity: tool_use | yes / partial / no | yes / partial / no | one trivial bash tool turn (`echo hi`). |
| Frame fidelity: permission_request | yes / partial / no | yes / partial / no | one dangerous-op turn (writing outside cwd). |
| Controllability: cancel | how it lands a cancel | how it lands a cancel | |
| Maintenance: patch count | total patches needed | total patches needed | |
| Maintenance: upgrade risk | qualitative | qualitative | |

Each row pinned to evidence (log excerpt, mock-gateway log line, or
quoted reasoning).

## 6. Stuck-Point Policy

The PoC aborts the active sub-phase and writes a stuck-point report
into this spec's §11 if any of:

- One sub-phase burns > 2 hours wall-clock without progress.
- Cumulative new mock routes for a single sub-phase reaches 5
  (PoC-1's full budget) without that sub-phase passing.
- The binary's subscribe handshake includes a server-issued signature
  the mock can't forge (e.g. an asymmetric-signed JWT verified
  client-side at construction time).
- Total Phase-0 wall-clock exceeds 3 days.

A.2 and A.3 failures do NOT abort each other; A.1 failure DOES end
Phase 0 (see §4 — A.1 is the gate). The Phase-0 outcome is "what we
learned," not "all sub-phases passed."

## 7. Data Flow — A.1 Round-Trip (the gate that protects A.2/A.3)

1. `mock_gateway.py` (extended) runs on 127.0.0.1:8181.
2. Operator (us) seeds a session id via `curl -X POST
   http://127.0.0.1:8181/cccloud11ab/v1/sessions -d '{}'` — mock
   returns `{id: "fake-sid-1", websocket_url: ..., status: "active"}`.
3. Run `patched-claude --teleport fake-sid-1` under the same PoC-1
   env (CLAUDE_CONFIG_DIR, ANTHROPIC_BASE_URL, etc.). Binary does
   `POST /v1/code/sessions/fake-sid-1/bridge` → gets fake worker JWT,
   opens `GET /v1/code/sessions/fake-sid-1/events/stream` (SSE).
4. Operator pushes one frame: `curl -X POST
   http://127.0.0.1:8181/_poc/sessions/fake-sid-1/inject -d
   '{"type":"event","data":{"type":"assistant","message":{...,"text":"echo: hello"}}}'`.
5. Mock places the frame on the SSE queue; the SSE handler emits it
   as `data: <json>\n\n`.
6. Binary's `SSETransport` reads it; `handleInboundMessage` dispatches
   it; the TUI's Ink renderer shows `echo: hello`. **A.1 passes.**

## 8. Data Flow — A.2 & A.3 Round-Trip

Same as A.1 steps 1-3, then instead of manual injection:

- (A.2) `driver_a.py fake-sid-1 'hello'` — runs
  patched `--cloud 'hello'` in the background, reads its output
  channels, translates each frame into the SSE format and injects.
- (A.3) `driver_b.py fake-sid-1 'hello'` — same with `claude -p
  stream-json`.

**Pass:** teleport TUI shows the model's actual response (text +
thinking if the model emits it).

## 9. Testing / Verification

No automated tests — this remains a PoC.

- **Pass per sub-phase:** human eye sees the expected text rendered
  in the teleport TUI; mock log records the expected request
  sequence; for A.2/A.3, the driver log records frame translations.
- **Fail:** stuck-point report (§11) describes which sub-phase
  blocked and where.

## 10. Deliverables and Repo Footprint

In-repo:

- This spec.
- §11 outcome after PoC-2 concludes (PASS or partial).
- Memory updates: extend `cc_binary_internals.md` with v2 transport
  endpoint shapes and backend selection conclusion;
  `agent_integration_strategy.md` only if Phase 1 decision changes.
- If Phase 0 succeeds: a follow-on Phase 1 cc-app-gateway design spec
  is authored (separate file, separate brainstorming round, not part
  of this Phase 0 plan).

Out-of-repo (live in `/tmp/cc-cloud-poc/`):

- Extended `mock_gateway.py` with v2 transport endpoints.
- `driver_a.py`, `driver_b.py`, `decision-matrix.md`.
- New iteration journals `iteration-1{0..N}.md`.
- Run logs.

## 11. Outcome — STUCK at A.1 (gate failed structurally, A.2/A.3 not attempted)

PoC-2 aborted on 2026-06-08 after 6 A.1 iterations
(`iteration-11.md` … `iteration-16.md` under `/tmp/cc-cloud-poc/`).
Per spec §4 A.1 is a hard gate; per spec §6 the binding stuck-point
is "binary needs server-signed material we can't forge" generalised
to "the data path A.1's PASS criterion requires is not opened by the
binary at all in this mode."

### Sub-phase status

| Sub-phase | Status | Pass criterion |
|---|---|---|
| A.1 subscribe-half | **FAIL** | `claude --teleport <sid>` never opens any live transport against the mock; injected SSE frame has no consumer. |
| A.2 backend A | **NOT ATTEMPTED** | Gated by A.1 per spec §4. |
| A.3 backend B | **NOT ATTEMPTED** | Gated by A.1 per spec §4. |
| A.4 decision matrix | **NOT ATTEMPTED** | No A.2/A.3 data to compare. |

### What did work

`--teleport` got farther than expected. With 4 new mock routes added
(within §6's 5-route budget), the binary completed every documented
teleport phase against the mock:

- `GET /v1/code/sessions/{sid}` — session lookup (iter-11)
- `GET /v1/code/sessions/{sid}/teleport-events` — bulk history (iter-13)
- `GET /v1/session_ingress/session/{sid}` — log-line fallback (iter-13)
- `GET /mcp-registry/v0/servers` — registry stub (iter-13)

Binary printed "Session resumed" and rendered the chat TUI. PoC-1's
2-patch redirect held across every probe. The `response_shape` and
`loglines` payload schemas were reverse-engineered from binary
v2.1.167 functions `rjH`, `ErK`, `Qn6`.

### Why A.1 failed structurally

After "Session resumed," the gateway request count stayed at 32 for
the entire 30-second post-resume observation window (`iteration-16.md`):

```
T+3s : 29 requests
T+6s : 29
T+9s : 29
T+12s: 32   (closing teleport telemetry — happens once)
T+15s..T+30s: 32 (no further requests)
```

`--teleport` mode hydrates history then hands off to the **local**
chat REPL. There is no live remote-subscription channel that an
injected SSE frame could land on. Decompilation of the teleport
React component `_89` confirms: fetch history via `Qn6` →
`applyMessageOp({type:"replace-all"})` → hand off to local REPL.
The bridge:repl gates (`Skipping: bridge not enabled`,
`Skipping: allow_remote_sessions policy not allowed`) keep the live
channel closed in this mode.

Telemetry NDJSON corroborates: `tengu_teleport_resume_session` event
present; **zero** `tengu_bridge_*` / `tengu_ccr_*` events. The
bridge:repl code path is never entered during `--teleport`.

What it would take to open a live channel from the public binary:

1. A binary patch (or three) to enter the bridge:repl path that is
   currently gated by org-policy flags and feature flags. The full
   thin-client mode (`claude assistant`) that uses these flags is
   KAIROS-stripped.
2. A binary patch to rewrite the hardcoded
   `wss://bridge.claudeusercontent.com` literal — separate host, not
   redirected by `ANTHROPIC_BASE_URL`. Same-length substitution
   should work; not attempted in this PoC.
3. Mocking the `[bridge:repl]` envelope protocol
   (`control_request`/`control_response`, ingress message types, JWT
   semantics on the worker channel).

Each of those individually fits the spec §6 "server-signed material
we can't forge" criterion or the §3 in-scope boundary ("only patches
required to redirect URL allowlist and bypass OAuth gate"). Together
they would be a Phase 0.5 or PoC-3 of similar magnitude to all of
PoC-1.

### Wider observation — this is what memory already said

`/root/.claude/projects/-root-agentserver/memory/cc_binary_internals.md`
records, from PoC G (May 2026, v2.1.128):

> Even with all 5 patches, the binary's `--teleport` mode does NOT
> become a thin client. User input goes to local REPL → direct
> `/v1/messages` → api.anthropic.com (NOT our gateway).
> `--teleport` is **local-resume** … NOT a thin-client subscriber.
> There is no "thin-client TUI subscriber" mode in the public binary.

This Phase 0 spec was authored on the (incorrect) premise that
v2.1.167's `--teleport` exercises the same bridge:repl channel that
claude.ai/code web exercises. It does not. The bridge:repl symbols
are present in the binary; the renderer code that consumes them
exists; what is *missing* in any user-reachable CLI mode is the call
site that opens the channel. `--cloud` is fire-and-forget (PoC-1's
finding); `--teleport` is local-resume (this finding). The renderer
is only entered from `claude assistant`, which remains stripped.

### Implications for Phase 1 cc-app-gateway design

Substantial. A codex-app-gateway-shape cc-app-gateway requires a
public CLI mode that subscribes to a live channel — and the public
binary has no such mode. Three forward options exist, none cheap:

1. **Three-patch unlock (PoC-3 scope):** patch the entry condition to
   the bridge:repl path so a standard CLI mode (or a new one we
   define via patch) opens the channel; patch the
   `bridge.claudeusercontent.com` literal to the mock; mock the
   `[bridge:repl]` envelope. Same-length patching is the right tool;
   cost is finding 3 anchors, not bytes per se. Maintenance risk per
   release is meaningfully higher than PoC-1's 2 patches.
2. **Reverse direction — agentserver-side wrapping:** treat the
   existing `Dockerfile.claudecode` + ttyd + per-user-sandbox
   topology as the long-form answer. cc-app-gateway becomes a thin
   shim that routes per-workspace LLM credentials into the sandbox
   (extending `internal/llmproxy/anthropic.go`), and the user keeps
   running their own `claude` against api.anthropic.com (proxied).
   Loses the "remote harness" property — agent loop runs in the
   sandbox container, not in the gateway. But works today with zero
   patches.
3. **Defer indefinitely:** keep claude on the local-loop ttyd path,
   focus cc-broker-replacement energy on the agent-loop side via the
   already-headless `claude -p` path (which Backend B in this spec
   was going to trial). No "thin client TUI" pretense.

The codex-primary stance in
`agent_integration_strategy.md` is **reinforced** by this finding —
the patch-maintenance gap to a real Claude thin-client remains
wide. No update to that file is warranted; the 2026-06-07 entry
already records the right framing.

### Artifacts archived

PoC-2 files remain under `/tmp/cc-cloud-poc/`:
- `mock_gateway.py` extended with v2 CCR endpoints + 4 A.1 teleport routes
- `run_teleport.sh` runner
- `iteration-10.md` … `iteration-16.md` journals
- `teleport-tui.log`, `gateway.log` last-run captures

Not committed; the value is the conclusion (this section) and the
updated memory.
