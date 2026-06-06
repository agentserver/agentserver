# Claude Code v2.1.167 `--cloud` Thin-Client Redirect PoC

**Date:** 2026-06-06
**Status:** Draft — design awaiting user sign-off
**Author:** mryao + Claude (Opus 4.8)
**Related memory:** `agent_integration_strategy.md`, `cc_binary_internals.md`, `cc_broker_evolution_paths.md`

## 1. Problem Statement

On 2026-05-05 we concluded that the Claude Code public binary has no
thin-client TUI mode and abandoned the cc-app-gateway path
(see `agent_integration_strategy.md`). That conclusion held through
v2.1.145.

Re-verifying against v2.1.167 (2026-06-06) shows the conclusion is
**no longer accurate**:

- `--cloud` is a new top-level flag (requires TTY; rejects pure API key,
  demands a Claude.ai account).
- `isThinClient` symbol is present (6 hits); the TUI renders
  `"Session (remote)"` when it is set.
- `routeThinClientCommand`, `isThinClientSafe`, `isBridgeDispatchable`
  classifiers are wired in.
- `[bridge:repl]` log lines for `Injecting inbound user message`,
  `Ignoring non-user inbound message`, `writeSdkMessages`,
  `sendControlRequest`, `writeBatch` exist and are reached when
  thin-client mode is active.
- `teleportToRemote` / `pollRemoteSessionEvents` /
  `awaitRemoteSessionResult` / `archiveRemoteSession` form a real
  submit-and-subscribe protocol against `/v1/code/sessions` on
  `api.anthropic.com`.

The architecture now matches codex-app's ws-subscribe topology. **The
open question is whether a redirect to our own gateway is feasible**,
given (a) the binary is now Bun-compiled ELF (~245 MB single file)
rather than the old Node `cli.js`, and (b) the cloud entrypoint adds a
new "API key not sufficient — must be Claude.ai" gate on top of the old
`Fk$` URL allowlist and `rz6()` bridge entitlement.

## 2. Goal

Prove **or disprove**, within ~1–2 days, whether v2.1.167 can be
patched to talk to a local mock gateway in thin-client mode and complete
a round-trip:

> User types `hello` in the patched TUI → mock gateway receives it via
> the bridge:repl WS → mock gateway pushes back a synthetic assistant
> message → TUI renders the response.

No real LLM. No production wiring. PoC artifacts live in
`/tmp/cc-cloud-poc/` and are **not** committed; only this spec and
follow-up memory updates land in the repo.

## 3. Scope

In-scope:

- Reverse-engineering v2.1.167 to confirm the bridge:repl wire format
  enough to mock it.
- Same-length-string binary patches: URL allowlist
  redirect, `rz6()` bypass, the new API-key-rejection bypass, and any
  further gate that surfaces during PoC iteration.
- A single-file Python mock gateway (aiohttp) implementing only the
  endpoints the TUI actually hits before it considers the session live.
- A run-script that launches the patched binary under a TTY with a
  fake OAuth env and points it at the mock gateway.

Out-of-scope:

- Real Anthropic credential proxying.
- Forwarding turns to a real Claude model.
- Multi-session, persistence, reconnect, or production hardening.
- Any change to `cc-broker`, `wsbridge`, `codexappgateway`, or other
  in-tree code.
- Decisions about whether to actually build a cc-app v2 — that is a
  separate spec that depends on this PoC's outcome.

## 4. Approach (chosen: B — iterative patch / run / observe)

Three approaches were considered (full alternatives recorded in
brainstorming history):

- A. Static-reverse all gates first, then patch once.
- **B. Iterate patch → run → read the next error → patch again.**
- C. Parallel mock-gateway + static analysis.

Approach B is chosen because:

1. Patches 1 and 2 (URL, `rz6()`) are already known from prior PoCs;
   only the new API-key-rejection gate is unknown. Front-loading static
   analysis (A) buys little.
2. Every iteration produces a concrete log message that is itself the
   answer to "are we stuck yet?", matching the "stop on first
   immovable block" failure policy.
3. A "stuck point" report is a natural byproduct of B.

## 5. Architecture

```
[patched cli-2.1.167]  ──HTTP──>  [mock_gateway.py]
       │                                  │
       │  bridge:repl WS                  │
       └─────────────────────────────────>│
                 (bi-directional)
```

Three components, each a single file under `/tmp/cc-cloud-poc/`:

### 5.1 `patch_cli.py`

Python script that applies same-length byte substitutions to a copy of
`/root/.local/share/claude/versions/2.1.167`, writing
`/tmp/cc-cloud-poc/claude-167-patched`.

- Reads source ELF, finds each patch's anchor string (`bytes.find`),
  fails loudly if not found exactly once.
- Each patch is `(name, find_bytes, replace_bytes)` with
  `len(find) == len(replace)` asserted.
- `--dry-run` flag prints offsets and surrounding context for
  inspection; default applies all patches and writes the output.
- Patches are added one at a time; the script is the canonical record
  of the patch set as iteration progresses.

### 5.2 `mock_gateway.py`

Single-file aiohttp server on `127.0.0.1:8181`. Minimum surface to
satisfy the TUI's thin-client entrypoint:

- `POST /v1/code/sessions` — creates an in-memory session, returns
  `{id, websocket_url, ...}` with the WS URL pointing back at this
  server. Schema fields filled in iteratively as the TUI complains
  about missing keys.
- `POST /v1/code/sessions/{id}/events` — accepts control_request /
  bash_command bodies, logs them, returns a minimal success envelope.
- `GET /v1/code/sessions/{id}/events` (or whichever poll path the TUI
  uses) — returns the queued event list since cursor; long-poll where
  needed.
- `WS /v1/code/sessions/{id}/stream` — bi-directional bridge:repl
  channel. On any inbound user message, queues an outbound synthetic
  `assistant_message` with text `echo: <input>`.
- All requests and frames are logged with millisecond timestamps to
  stdout for post-hoc inspection.

Whether the TUI uses polling, WS, or both (`CLAUDE_CODE_USE_CCR_V2` /
`CLAUDE_CODE_POST_FOR_SESSION_INGRESS_V2` env vars exist in the binary)
will be determined during iteration; the mock can support both by
mirroring frame content into both channels.

### 5.3 `run_poc.sh`

Launches the patched binary under `script -qc` to provide a PTY, with
the fake-identity env:

```sh
CLAUDE_CODE_OAUTH_TOKEN=fake-poc-token \
CLAUDE_CODE_ORGANIZATION_UUID=00000000-0000-0000-0000-000000000001 \
CLAUDE_CODE_ACCOUNT_UUID=00000000-0000-0000-0000-000000000002 \
CLAUDE_CODE_USER_EMAIL=poc@example.invalid \
CLAUDE_CODE_CUSTOM_OAUTH_URL=http://127.0.0.1:8181 \
CLAUDE_CODE_REMOTE=1 \
script -qc "/tmp/cc-cloud-poc/claude-167-patched --cloud 'hello'" \
    /tmp/cc-cloud-poc/tui.log
```

## 6. Patch Set (initial)

All patches are same-length byte substitutions — replacement length
must equal find length exactly. URL replacements are constrained to be
chosen at a length that matches an existing allowlist entry; we set
`CLAUDE_CODE_CUSTOM_OAUTH_URL` to a value beginning with the same
prefix the patch wrote in, so the runtime `.startsWith()` allowlist
check passes.

| # | Anchor (find)                                              | Replacement                              | Effect                                              |
|---|-------------------------------------------------------------|-------------------------------------------|-----------------------------------------------------|
| 1 | one Fk$ allowlist entry whose length we can match, e.g. `https://api-staging.anthropic.com` (33 bytes) | a same-length URL prefix at our mock, e.g. `http://127.0.0.1:8181/cccloud11ab` (33 bytes); env `CLAUDE_CODE_CUSTOM_OAUTH_URL` is set to that exact string so the prefix check passes | Honor custom `BASE_API_URL` pointing at the mock |
| 2 | `function rz6(){return!1}`                                  | `function rz6(){return!0}`                | Force bridge entitlement true; skip claude.ai-subscription gate |
| 3 | the predicate or sentinel string for "API key authentication is not sufficient" — likely `if(<expr>)<throw>` or a flag-set near that template | flip the predicate so the rejection branch is unreachable, e.g. `if(0)` in place of `if(<expr>)` if lengths align, otherwise a same-length no-op rewrite of the flag-set | Allow `--cloud` to accept the fake OAuth-env identity |
| ? | discovered during iteration                                  | TBD                                       | TBD                                                 |

The exact byte-for-byte find/replace payloads for Patches 1–3 are
finalized at implementation time from a fresh strings + offset audit
against the actual installed 2.1.167 ELF: prior anchors were verified
against 2.1.128 and the Bun bundle layout in 2.1.167 may have shifted
the surrounding minified symbols. Each patch is rejected by the
patcher if its anchor does not appear exactly once.

## 7. Data Flow — Round-Trip Verification

1. `run_poc.sh` boots patched TUI inside a PTY with fake-identity env
   vars (above) and `CLAUDE_CODE_CUSTOM_OAUTH_URL` pointed at the mock.
2. TUI enters the `--cloud` path. Patches 1–3 let it past URL allowlist,
   bridge entitlement, and API-key-sufficiency gates.
3. TUI POSTs `/v1/code/sessions` → mock returns an envelope with a WS
   URL pointing back at itself.
4. TUI upgrades to WS, sends its initial `writeSdkMessages` system/init
   frame; mock logs it (no parsing required for PoC).
5. The command-line prompt `'hello'` (passed as the positional arg to
   `--cloud`) is delivered via bridge:repl as an inbound user message.
6. Mock gateway receives, replies with
   `{type: "assistant_message", content:[{type:"text", text:"echo: hello"}]}`.
7. TUI renders the text. **Round-trip achieved.**

## 8. Failure / Stuck-Point Policy

The PoC aborts and writes a stuck-point report (appended as section 11
of this document) if **any** of:

- A single patch step burns > 1 hour without locating a unique
  same-length anchor.
- Cumulative patch count reaches 5 without achieving round-trip.
- The binary is found to perform a runtime integrity check (self-hash,
  signature verification, etc.) that defeats same-length patching.
- The bridge:repl protocol carries a server-issued signature or token
  that the mock cannot synthesize.

The report records: the last successful step, the exact error /
log excerpt that stopped progress, the offending binary substring with
offset, and an honest assessment of "next step would be ___, cost ___".

## 9. Testing / Verification

No automated tests — this is a PoC.

- **Pass:** human eye sees `echo: hello` rendered in the TUI; mock
  gateway log records the inbound `hello` frame and the outbound
  assistant_message frame.
- **Fail:** stuck-point report (section 11) describes where the chain
  broke.

## 10. Deliverables and Repo Footprint

In-repo:

- This spec (`docs/superpowers/specs/2026-06-06-cc-cloud-thin-client-poc-design.md`).
- Memory updates after the PoC concludes: refresh
  `agent_integration_strategy.md` and `cc_binary_internals.md` to
  reflect 2.1.167 findings (whichever way the PoC lands).

Out-of-repo:

- `/tmp/cc-cloud-poc/patch_cli.py`
- `/tmp/cc-cloud-poc/mock_gateway.py`
- `/tmp/cc-cloud-poc/run_poc.sh`
- `/tmp/cc-cloud-poc/claude-167-patched`
- `/tmp/cc-cloud-poc/tui.log`, `/tmp/cc-cloud-poc/gateway.log`

If the PoC passes and we later decide to build cc-app v2, those
artifacts will be re-authored as a proper in-tree component under a
new spec.

## 11. Outcome (to be filled in after the PoC)

_Pending execution._
