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

## 11. Outcome (to be filled in after PoC-2)

_Pending execution._
