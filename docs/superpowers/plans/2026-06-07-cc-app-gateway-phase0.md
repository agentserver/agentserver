# cc-app-gateway Phase 0 (PoC-2) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Within 2-3 days, verify v2.1.167's subscribe-half (SSE reads + POST writes via the `/bridge` handshake) end-to-end against a local mock, then trial two backend-harness candidates (patched `--cloud` and plain `claude -p stream-json`) and write a 5-dimension decision matrix to inform a future cc-app-gateway design.

**Architecture:** Extend the existing `/tmp/cc-cloud-poc/` workspace from PoC-1 (PR #229) with v2 CCR transport endpoints in `mock_gateway.py`, then add two thin Python drivers (`driver_a.py`, `driver_b.py`) that translate each backend candidate's per-turn output into v2 SSE frames injected into the mock. Sub-phase A.1 (subscribe-half) is a hard gate; A.2/A.3 (the two backends) are independent of each other; A.4 writes the matrix. All work stays under `/tmp/cc-cloud-poc/`; only this plan, the spec's §11 outcome, and memory updates land in the repo.

**Tech Stack:** Python 3.13+ (`aiohttp` already pinned by PoC-1), bash + `script(1)` for PTY, existing PoC-1 patched binary at `/tmp/cc-cloud-poc/claude-167-patched`, original at `/tmp/cc-cloud-poc/claude-167-original`. Reference source (sparingly, v2.1.88-vintage): `/root/cc/source/src/bridge/`.

**Spec:** `docs/superpowers/specs/2026-06-07-cc-app-gateway-phase0-design.md`

**Branch:** `spec/cc-app-gateway-phase0` (branched off `github/main`, spec already committed at `22315c1`).

**Repo footprint (final):** updates to this plan's checkboxes, this spec's §11, and 1-2 memory files. PoC artifacts stay in `/tmp/cc-cloud-poc/`.

---

## File Structure

| Path | Role | In repo? |
|---|---|---|
| `/tmp/cc-cloud-poc/mock_gateway.py` | Existing aiohttp server; extended with v2 CCR endpoints + `/_poc/*` injection API | No |
| `/tmp/cc-cloud-poc/driver_a.py` | Backend A: fork patched `claude --cloud`, harvest its per-turn frames, inject into mock SSE | No |
| `/tmp/cc-cloud-poc/driver_b.py` | Backend B: fork `claude -p --output-format stream-json`, translate each line to v2 frame, inject | No |
| `/tmp/cc-cloud-poc/decision-matrix.md` | A.4 deliverable: A-vs-B 5-dimension comparison with evidence | No |
| `/tmp/cc-cloud-poc/iteration-1{0..N}.md` | One short journal per iteration loop (continuing PoC-1's `iteration-NN.md` numbering) | No |
| `docs/superpowers/specs/2026-06-07-cc-app-gateway-phase0-design.md` | Spec — append §11 Outcome | Yes (modify) |
| `docs/superpowers/plans/2026-06-07-cc-app-gateway-phase0.md` | This plan — checkboxes get ticked | Yes (modify) |
| `/root/.claude/projects/-root-agentserver/memory/cc_binary_internals.md` | Append v2 transport endpoint shapes + backend selection conclusion | Yes (modify) |
| `/root/.claude/projects/-root-agentserver/memory/MEMORY.md` | Refresh one-line hook for `cc_binary_internals.md` | Yes (modify) |

`agent_integration_strategy.md` is only updated if Phase 0 changes the strategic conclusion (it likely won't — Phase 0 only feeds Phase 1 design, doesn't itself pivot anything).

---

## Task 1: Workspace sanity check

**Files:**
- Read-only check of `/tmp/cc-cloud-poc/`

- [ ] **Step 1: Confirm PoC-1 artifacts are intact**

Run:
```bash
ls -la /tmp/cc-cloud-poc/claude-167-original \
       /tmp/cc-cloud-poc/claude-167-patched \
       /tmp/cc-cloud-poc/mock_gateway.py \
       /tmp/cc-cloud-poc/patch_cli.py \
       /tmp/cc-cloud-poc/run_poc.sh
```

Expected: all five files present and executable (the two binaries 245,434,064 B each; mock_gateway ~10 KB; patch_cli ~3.5 KB; run_poc ~3 KB).

If anything is missing, STOP and report — Phase 0 builds on PoC-1's workspace. Recreate via PoC-1's plan if needed.

- [ ] **Step 2: Confirm patched binary still passes PoC-1 round-trip**

Run a quick sanity pass to confirm PoC-1 still works (catches "I broke something between sessions"):
```bash
ss -tlnp 2>/dev/null | grep -q ':8181 ' && fuser -k 8181/tcp 2>/dev/null
sleep 1
rm -rf /tmp/cc-cloud-poc/claude-cfg
rm -f /tmp/cc-cloud-poc/tui.log /tmp/cc-cloud-poc/tui-stderr.log /tmp/cc-cloud-poc/gateway.log
nohup python3 /tmp/cc-cloud-poc/mock_gateway.py > /tmp/cc-cloud-poc/gateway.log 2>&1 &
sleep 1
timeout 30 /tmp/cc-cloud-poc/run_poc.sh; echo "exit=$?"
ss -tlnp 2>/dev/null | grep ':8181 ' | grep -oP 'pid=\d+' | head -1 | cut -d= -f2 | xargs -r kill 2>/dev/null
sleep 1
sed -E 's/\x1B\[[0-9;]*[A-Za-z]//g; s/\r//g' /tmp/cc-cloud-poc/tui.log | grep -E "Created remote session|Resume with:" || echo "PoC-1 SMOKE FAILED"
```

Expected: lines like `Created remote session: poc` and `Resume with: claude --teleport <sid>` printed; exit 0. If "PoC-1 SMOKE FAILED" appears, STOP and report — diagnose before adding the new endpoints.

- [ ] **Step 3: Seed iteration journal `iteration-10.md`**

Write `/tmp/cc-cloud-poc/iteration-10.md`:
```markdown
# Iteration 10 — PoC-2 kickoff (workspace sanity)

- PoC-1 smoke pass: yes
- Patched binary mtime: <copy from `ls -la`>
- Mock gateway routes confirmed (PoC-1 set): 9
- Next: Task 2 — add v2 CCR endpoints (Bridge JWT exchange + SSE events stream + inject API)
```

Fill in the placeholders with actual values. No commit (out of tree).

---

## Task 2: A.1 — add v2 CCR endpoints to mock_gateway.py

**Files:**
- Modify: `/tmp/cc-cloud-poc/mock_gateway.py`

This task adds four endpoints needed to satisfy v2.1.167's `bridge:repl` v2 transport handshake (per `/root/cc/source/src/bridge/remoteBridgeCore.ts`). The endpoints are dumb mocks — JWT is opaque (binary doesn't cryptographically verify it locally for `/worker/*` calls), epoch is a monotonic counter, SSE keeps a per-sid in-memory queue.

It also adds the `_poc` injection sibling endpoint so drivers (Tasks 5, 7) can push frames onto SSE queues over plain HTTP.

- [ ] **Step 1: Add per-sid SSE queue state at module level**

In `/tmp/cc-cloud-poc/mock_gateway.py`, near the existing `sessions: dict[...]` and `ws_clients: dict[...]` module-level dicts (around line 27), add:

```python
# Per-sid SSE event queue. Each entry is a JSON-serializable dict; the
# SSE handler drains them as `data: <json>\n\n`. Producers: /_poc/inject
# and the existing maybe_echo() WS path. Consumers: stream_events_v2_sse.
sse_queues: dict[str, asyncio.Queue] = defaultdict(asyncio.Queue)

# Per-sid monotonic epoch counter for /bridge calls (worker_epoch).
sse_epochs: dict[str, int] = defaultdict(int)
```

- [ ] **Step 2: Add the four new handler functions**

Place these handlers in `/tmp/cc-cloud-poc/mock_gateway.py` after the existing `environment_providers_create` handler (around line 120 — anywhere in the handler block is fine, but keep them grouped):

```python
async def bridge_exchange(request: web.Request) -> web.Response:
    """POST /v1/code/sessions/{sid}/bridge — exchange OAuth for worker_jwt.

    Per cc 2.1.88 source (remoteBridgeCore.ts §2): each call bumps epoch
    monotonically; binary trusts whatever we return as a bearer for
    subsequent /worker/* calls (no client-side signature verification).
    """
    sid = request.match_info["sid"]
    body = await request.text()
    log(f"POST {request.path} body={body[:300]}")
    sse_epochs[sid] += 1
    envelope = {
        "worker_jwt": f"fake-worker-jwt-{sid}-epoch-{sse_epochs[sid]}",
        "worker_epoch": sse_epochs[sid],
        "api_base_url": f"http://{HOST}:{PORT}/cccloud11ab",
        "expires_in": 3600,
    }
    log(f"-> 200 {envelope}")
    return web.json_response(envelope)


async def stream_events_v2_sse(request: web.Request) -> web.StreamResponse:
    """GET /v1/code/sessions/{sid}/events/stream — SSE long-poll.

    Drains sse_queues[sid] and emits each frame as `data: <json>\\n\\n`.
    The binary's SSETransport will read frames and dispatch via
    handleInboundMessage.
    """
    sid = request.match_info["sid"]
    log(f"GET {request.path} (SSE open) query={dict(request.query)}")
    resp = web.StreamResponse(
        status=200,
        headers={
            "Content-Type": "text/event-stream",
            "Cache-Control": "no-cache",
            "Connection": "keep-alive",
            "X-Accel-Buffering": "no",
        },
    )
    await resp.prepare(request)
    queue = sse_queues[sid]
    try:
        while True:
            try:
                frame = await asyncio.wait_for(queue.get(), timeout=20.0)
            except asyncio.TimeoutError:
                # Keep-alive comment line; SSE parsers ignore lines starting with ":"
                await resp.write(b": keep-alive\n\n")
                continue
            data = json.dumps(frame)
            log(f"SSE->{sid} {data[:300]}")
            await resp.write(f"data: {data}\n\n".encode("utf-8"))
    except (ConnectionResetError, asyncio.CancelledError) as e:
        log(f"SSE close sid={sid} reason={type(e).__name__}")
    return resp


async def worker_event_delivery(request: web.Request) -> web.Response:
    """POST /worker/events/{event_id}/delivery — ack from binary.

    Body shape: {status: "processing" | "processed"}. We only log it.
    """
    event_id = request.match_info["event_id"]
    body = await request.text()
    log(f"POST {request.path} event_id={event_id} body={body[:200]}")
    return web.json_response({"ok": True})


async def inject_frame(request: web.Request) -> web.Response:
    """POST /_poc/sessions/{sid}/inject — driver-facing API.

    Body: any JSON frame, enqueued verbatim onto sid's SSE queue. NOT part
    of the Anthropic API surface; the _poc/* namespace can't collide.
    """
    sid = request.match_info["sid"]
    try:
        frame = await request.json()
    except json.JSONDecodeError as e:
        log(f"POST {request.path} bad json: {e}")
        return web.json_response({"ok": False, "error": str(e)}, status=400)
    log(f"_poc inject sid={sid} frame={json.dumps(frame)[:300]}")
    await sse_queues[sid].put(frame)
    return web.json_response({"ok": True})
```

Make sure `asyncio` is imported at the top of the file (it already is per PoC-1 iteration 8's gateway), and that `defaultdict`, `web`, `json`, `log` are in scope (they are).

- [ ] **Step 3: Register the new routes**

In `make_app()`, find the existing iter-9 `/v1/sessions/*` registrations and add immediately after (prefixed and unprefixed forms, mirroring the iter-9 pattern):

```python
    # v2 CCR transport handshake + stream + delivery ack (PoC-2 / A.1)
    app.router.add_post("/cccloud11ab/v1/code/sessions/{sid}/bridge", bridge_exchange)
    app.router.add_post("/v1/code/sessions/{sid}/bridge", bridge_exchange)
    app.router.add_get("/cccloud11ab/v1/code/sessions/{sid}/events/stream", stream_events_v2_sse)
    app.router.add_get("/v1/code/sessions/{sid}/events/stream", stream_events_v2_sse)
    app.router.add_post("/cccloud11ab/worker/events/{event_id}/delivery", worker_event_delivery)
    app.router.add_post("/worker/events/{event_id}/delivery", worker_event_delivery)

    # Driver-facing injection sibling (NOT Anthropic surface)
    app.router.add_post("/_poc/sessions/{sid}/inject", inject_frame)
```

Also add the `/v1/sessions/{sid}/events/stream` aliases (the binary may probe both `/code/sessions/...` and `/sessions/...` based on which code path is taken):

```python
    app.router.add_get("/cccloud11ab/v1/sessions/{sid}/events/stream", stream_events_v2_sse)
    app.router.add_get("/v1/sessions/{sid}/events/stream", stream_events_v2_sse)
    app.router.add_post("/cccloud11ab/v1/sessions/{sid}/bridge", bridge_exchange)
    app.router.add_post("/v1/sessions/{sid}/bridge", bridge_exchange)
```

- [ ] **Step 4: Syntax check + smoke test**

```bash
python3 -c "import ast; ast.parse(open('/tmp/cc-cloud-poc/mock_gateway.py').read()); print('ok')"
```

Then start and probe:
```bash
ss -tlnp 2>/dev/null | grep -q ':8181 ' && fuser -k 8181/tcp 2>/dev/null
sleep 1
nohup python3 /tmp/cc-cloud-poc/mock_gateway.py > /tmp/cc-cloud-poc/gateway.log 2>&1 &
sleep 1

# Bridge handshake
curl -sS -X POST http://127.0.0.1:8181/cccloud11ab/v1/code/sessions/sid-1/bridge -d '{}' -H 'content-type: application/json'
echo

# Inject a frame
curl -sS -X POST http://127.0.0.1:8181/_poc/sessions/sid-1/inject \
  -d '{"type":"event","data":{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hello from inject"}]}}}' \
  -H 'content-type: application/json'
echo

# Open SSE briefly to confirm it streams the injected frame
timeout 3 curl -sN http://127.0.0.1:8181/cccloud11ab/v1/code/sessions/sid-1/events/stream || true
echo

ss -tlnp 2>/dev/null | grep ':8181 ' | grep -oP 'pid=\d+' | head -1 | cut -d= -f2 | xargs -r kill 2>/dev/null
```

Expected:
- bridge response includes `worker_jwt`, `worker_epoch=1`, `api_base_url`, `expires_in`.
- inject responds `{"ok": true}`.
- SSE shows `data: {"type":"event","data":{...,"text":"hello from inject"}}` then the connection ends on timeout.

If any of those fail, STOP and fix before Task 3 — Task 3 depends on these working.

- [ ] **Step 5: Append iteration-10.md**

Add to `/tmp/cc-cloud-poc/iteration-10.md`:
```markdown

## Endpoints added (smoke-tested)

- POST /v1/code/sessions/{sid}/bridge — returns fake JWT + epoch
- GET  /v1/code/sessions/{sid}/events/stream — SSE drain of sse_queues[sid]
- POST /worker/events/{event_id}/delivery — ack sink
- POST /_poc/sessions/{sid}/inject — driver-facing frame injection

All registered prefixed (`/cccloud11ab/...`) and unprefixed.
```

No commit (out of tree).

---

## Task 3: A.1 — drive `claude --teleport <sid>` against the extended mock

**Files:**
- Modify: `/tmp/cc-cloud-poc/run_poc.sh` (or create a sibling runner script — see Step 1)

A.1's pass criterion (spec §7): operator-injected `echo: hello` frame is rendered by `claude --teleport <sid>`'s TUI. This task does that round-trip and records the result.

- [ ] **Step 1: Create a teleport runner script**

`run_poc.sh` is dedicated to `--cloud` (single-prompt flow). Create a sibling for `--teleport` so we keep both working:

Write `/tmp/cc-cloud-poc/run_teleport.sh`:
```bash
#!/usr/bin/env bash
# Launch the patched binary in --teleport mode against the local mock.
# Mock gateway MUST already be running.
set -euo pipefail
cd /tmp/cc-cloud-poc

if [[ ! -x /tmp/cc-cloud-poc/claude-167-patched ]]; then
  echo "claude-167-patched missing or not executable. Run patch_cli.py first." >&2
  exit 2
fi

if [[ $# -ne 1 ]]; then
  echo "usage: $0 <sid>" >&2
  exit 2
fi
SID="$1"

: "${POC_BASE_URL:=http://127.0.0.1:8181/cccloud11ab}"

export CLAUDE_CONFIG_DIR=/tmp/cc-cloud-poc/claude-cfg
mkdir -p "$CLAUDE_CONFIG_DIR"
for f in .claude.json .claude-custom-oauth.json; do
  cat > "$CLAUDE_CONFIG_DIR/$f" <<'JSON'
{
  "numStartups": 5,
  "theme": "dark",
  "hasCompletedOnboarding": true,
  "hasCompletedProjectOnboarding": true,
  "hasAcknowledgedCostThreshold": true,
  "bypassPermissionsModeAccepted": true,
  "customApiKeyResponses": {"approved": [], "rejected": []},
  "firstStartTime": "2025-01-01T00:00:00.000Z",
  "userID": "00000000-0000-0000-0000-000000000003",
  "oauthAccount": {
    "accountUuid": "00000000-0000-0000-0000-000000000002",
    "emailAddress": "poc@example.invalid",
    "organizationUuid": "00000000-0000-0000-0000-000000000001"
  }
}
JSON
done

CLAUDE_CODE_OAUTH_TOKEN=fake-poc-token \
CLAUDE_CODE_ORGANIZATION_UUID=00000000-0000-0000-0000-000000000001 \
CLAUDE_CODE_ACCOUNT_UUID=00000000-0000-0000-0000-000000000002 \
CLAUDE_CODE_USER_EMAIL=poc@example.invalid \
CLAUDE_CODE_CUSTOM_OAUTH_URL="${POC_BASE_URL}" \
ANTHROPIC_BASE_URL="${POC_BASE_URL}" \
CLAUDE_CODE_REMOTE=1 \
CLAUDE_CODE_USE_CCR_V2=1 \
DISABLE_INSTALLATION_CHECKS=1 \
CLAUDE_CODE_SANDBOXED=1 \
  script -qc "/tmp/cc-cloud-poc/claude-167-patched --teleport '${SID}' 2>/tmp/cc-cloud-poc/teleport-stderr.log" \
  /tmp/cc-cloud-poc/teleport-tui.log
```

The `CLAUDE_CODE_USE_CCR_V2=1` env var forces SSE transport (per `cc_binary_internals.md` memory — added in v2.1.128, still valid in v2.1.167). Without it the binary might prefer v1 hybrid.

```bash
chmod +x /tmp/cc-cloud-poc/run_teleport.sh
bash -n /tmp/cc-cloud-poc/run_teleport.sh
```

- [ ] **Step 2: Pre-create a session id and prepare to inject**

The teleport flow needs a session id that the mock already knows about. Pre-create one via the existing iter-9 `/v1/sessions` POST and capture the id:

```bash
ss -tlnp 2>/dev/null | grep -q ':8181 ' && fuser -k 8181/tcp 2>/dev/null
sleep 1
rm -rf /tmp/cc-cloud-poc/claude-cfg
rm -f /tmp/cc-cloud-poc/teleport-tui.log /tmp/cc-cloud-poc/teleport-stderr.log /tmp/cc-cloud-poc/gateway.log
nohup python3 /tmp/cc-cloud-poc/mock_gateway.py > /tmp/cc-cloud-poc/gateway.log 2>&1 &
sleep 1

SID=$(curl -sS -X POST http://127.0.0.1:8181/cccloud11ab/v1/sessions \
  -d '{"title":"poc-A1","events":[]}' \
  -H 'content-type: application/json' | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
echo "SID=$SID"
```

Expected: `SID=<uuid>`. Note it down for the next steps.

- [ ] **Step 3: Start the teleport TUI in background and inject a frame**

This is the round-trip. Run teleport in a backgrounded shell, give it 3-4 seconds to subscribe to SSE, inject a frame, give it 2-3 seconds to render, then kill.

```bash
# Launch teleport in background (timeout caps wall-clock so we don't hang)
( timeout 20 /tmp/cc-cloud-poc/run_teleport.sh "$SID" > /tmp/cc-cloud-poc/teleport-run.log 2>&1 ) &
TELEPORT_PID=$!
sleep 4   # give SSE time to subscribe

# Inject the frame
curl -sS -X POST "http://127.0.0.1:8181/_poc/sessions/${SID}/inject" \
  -d '{"type":"event","data":{"uuid":"frame-1","session_id":"'"$SID"'","type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"echo: hello"}]}}}' \
  -H 'content-type: application/json'
echo

sleep 3   # give TUI time to render

# Stop teleport (timeout above will also catch us)
kill -INT $TELEPORT_PID 2>/dev/null
wait $TELEPORT_PID 2>/dev/null
ss -tlnp 2>/dev/null | grep ':8181 ' | grep -oP 'pid=\d+' | head -1 | cut -d= -f2 | xargs -r kill 2>/dev/null
sleep 1
```

- [ ] **Step 4: Inspect logs**

```bash
echo "===== Teleport TUI log (ANSI/CR stripped, last 100 lines) ====="
sed -E 's/\x1B\[[0-9;]*[A-Za-z]//g; s/\x1B\][^\x07]*\x07//g; s/\r//g' /tmp/cc-cloud-poc/teleport-tui.log | tail -100
echo
echo "===== Gateway log ====="
cat /tmp/cc-cloud-poc/gateway.log
echo
echo "===== Telemetry (failed events) ====="
find /tmp/cc-cloud-poc/claude-cfg/telemetry -type f 2>/dev/null | head
python3 -c "
import json, base64, glob
for f in glob.glob('/tmp/cc-cloud-poc/claude-cfg/telemetry/1p_failed_events*.json*'):
    print('==', f)
    for line in open(f):
        line = line.strip()
        if not line: continue
        try:
            ev = json.loads(line)
            md = ev.get('additional_metadata', '')
            if md:
                try: ev['_decoded'] = json.loads(base64.b64decode(md))
                except Exception: pass
            print(json.dumps(ev)[:500])
        except Exception:
            print('parse err:', line[:200])
" 2>/dev/null
```

- [ ] **Step 5: Classify outcome and either advance to Task 4 or iterate**

Classification:

**(A.1-PASS)** TUI shows `echo: hello` somewhere in the rendered output AND gateway log shows: `POST /v1/code/sessions/<sid>/bridge`, `GET /v1/code/sessions/<sid>/events/stream` opened (SSE), `_poc inject sid=<sid>` accepted, and `SSE-><sid>` line emitted. → advance to Task 4 (A.2).

**(A.1-NEED-MORE-MOCK)** Teleport binary made progress (hit `/bridge` and/or opened SSE) but failed on a new 404 or wire-shape mismatch. Per spec §6: at most 5 new mock routes per sub-phase. Repeat Tasks 2-3 in an `iteration-1{N+1}.md` cycle, adding routes one at a time until pass or budget exhausted.

**(A.1-FAIL — hard stop for Phase 0)** Per spec §6: subscribe handshake requires server-signed material the mock can't forge, OR sub-phase wall-clock > 2 hours without progress, OR 5 new mock routes added without pass. → write Task 9 STUCK report; Phase 0 ends here (A.2/A.3 cannot run without A.1).

Write `/tmp/cc-cloud-poc/iteration-11.md` (or higher) recording which classification fired and the evidence. Standard journal shape (see iter-09 in PoC-1 for the template).

---

## Task 4: A.2 — backend A driver (patched `--cloud`)

**Files:**
- Create: `/tmp/cc-cloud-poc/driver_a.py`

Backend A reuses PoC-1's patched `claude --cloud` binary. The `--cloud` launcher is fire-and-forget: it uploads the prompt as an embedded event in `POST /v1/sessions` and exits. To turn it into a backend harness, we extract the per-turn frames it produces (text response, thinking, tool_use) and feed them into A.1's SSE injection API so a separately-running teleport client sees them.

Frames are observable in three places (per PoC-1 iter-7 findings):
1. The `events` array in the body of `POST /v1/sessions` (the prompt itself, as a user-role event).
2. The 89 KB `last_batch.bin` body of `POST /api/event_logging/v2/batch` (telemetry batch — also contains rich event metadata).
3. The `1p_failed_events.*.json` NDJSON if the batch upload "failed" (we make ours succeed, so this won't be the primary path).

Driver A taps source (1) for the prompt, runs `--cloud`, then waits for source (2) and translates the relevant events into v2 SSE frames.

- [ ] **Step 1: Write driver_a.py**

Create `/tmp/cc-cloud-poc/driver_a.py`:

```python
#!/usr/bin/env python3
"""driver_a.py — Backend A adapter for cc-app-gateway PoC-2.

Forks `patched-claude --cloud '<prompt>'` against the local mock,
intercepts the POST /v1/sessions and POST /api/event_logging/v2/batch
request bodies (the binary's own writes; we read them from the mock's
disk side effects), and injects translated frames into the mock's
SSE queue for sid <sid> so a separately-running `claude --teleport
<sid>` sees the model's response.

Usage:
    driver_a.py <sid> <prompt>
"""
import argparse
import json
import os
import shutil
import subprocess
import sys
import time
from pathlib import Path

import urllib.request

POC = Path("/tmp/cc-cloud-poc")
MOCK_BASE = "http://127.0.0.1:8181"


def log(msg: str) -> None:
    print(f"[driver_a] {msg}", flush=True)


def inject(sid: str, frame: dict) -> None:
    body = json.dumps(frame).encode()
    req = urllib.request.Request(
        f"{MOCK_BASE}/_poc/sessions/{sid}/inject",
        data=body,
        headers={"content-type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=5) as resp:
        log(f"injected (status={resp.status}) {json.dumps(frame)[:120]}")


def reset_cfg() -> None:
    shutil.rmtree(POC / "claude-cfg", ignore_errors=True)


def run_cloud(prompt: str) -> int:
    """Run the patched `claude --cloud` against the mock; return its exit code."""
    log(f"forking patched --cloud {prompt!r}")
    # Reuse run_poc.sh's env setup via a wrapper — simpler than duplicating
    # the whole env block here. Override the inner command to pass our prompt.
    env = os.environ.copy()
    env["POC_PROMPT"] = prompt
    # We can't easily override run_poc.sh's hard-coded 'hello' prompt without
    # editing it; instead invoke the binary directly with the same env block.
    cfg_dir = POC / "claude-cfg"
    cfg_dir.mkdir(exist_ok=True)
    seed = {
        "numStartups": 5,
        "theme": "dark",
        "hasCompletedOnboarding": True,
        "hasCompletedProjectOnboarding": True,
        "hasAcknowledgedCostThreshold": True,
        "bypassPermissionsModeAccepted": True,
        "customApiKeyResponses": {"approved": [], "rejected": []},
        "firstStartTime": "2025-01-01T00:00:00.000Z",
        "userID": "00000000-0000-0000-0000-000000000003",
        "oauthAccount": {
            "accountUuid": "00000000-0000-0000-0000-000000000002",
            "emailAddress": "poc@example.invalid",
            "organizationUuid": "00000000-0000-0000-0000-000000000001",
        },
    }
    for name in (".claude.json", ".claude-custom-oauth.json"):
        (cfg_dir / name).write_text(json.dumps(seed))
    env.update({
        "CLAUDE_CODE_OAUTH_TOKEN": "fake-poc-token",
        "CLAUDE_CODE_ORGANIZATION_UUID": "00000000-0000-0000-0000-000000000001",
        "CLAUDE_CODE_ACCOUNT_UUID": "00000000-0000-0000-0000-000000000002",
        "CLAUDE_CODE_USER_EMAIL": "poc@example.invalid",
        "CLAUDE_CODE_CUSTOM_OAUTH_URL": f"{MOCK_BASE}/cccloud11ab",
        "ANTHROPIC_BASE_URL": f"{MOCK_BASE}/cccloud11ab",
        "CLAUDE_CODE_REMOTE": "1",
        "DISABLE_INSTALLATION_CHECKS": "1",
        "CLAUDE_CODE_SANDBOXED": "1",
        "CLAUDE_CONFIG_DIR": str(cfg_dir),
    })
    # script(1) provides the PTY --cloud requires.
    cmd = [
        "script", "-qc",
        f"/tmp/cc-cloud-poc/claude-167-patched --cloud {json.dumps(prompt)} "
        f"2>/tmp/cc-cloud-poc/driver-a-stderr.log",
        "/tmp/cc-cloud-poc/driver-a-tui.log",
    ]
    proc = subprocess.run(cmd, env=env, timeout=60)
    log(f"--cloud exited rc={proc.returncode}")
    return proc.returncode


def extract_frames_from_last_batch() -> list[dict]:
    """Parse /tmp/cc-cloud-poc/last_batch.bin (telemetry) for assistant frames.

    last_batch.bin is plain JSON (per PoC-1 iter-9). The schema is the
    Anthropic event_logging v2 batch shape; the assistant-turn events live
    under a known path. We extract them defensively and convert each into
    a v2 transport frame.
    """
    path = POC / "last_batch.bin"
    if not path.exists():
        log("last_batch.bin missing — telemetry was never POSTed")
        return []
    raw = path.read_bytes()
    try:
        data = json.loads(raw)
    except Exception as e:
        log(f"last_batch.bin not JSON: {e}")
        return []
    # event_logging batches are typically {"events":[...]}; each event has
    # event_name + properties. Filter for events that carry assistant text.
    out = []
    events = data.get("events", []) if isinstance(data, dict) else []
    for ev in events:
        if not isinstance(ev, dict):
            continue
        name = ev.get("event_name", "")
        props = ev.get("properties", {}) or {}
        # Heuristic: any event whose props contain a "message" with role=assistant
        msg = props.get("message")
        if isinstance(msg, dict) and msg.get("role") == "assistant":
            out.append({
                "type": "event",
                "data": {
                    "uuid": ev.get("uuid", f"a2-{len(out)}"),
                    "type": "assistant",
                    "message": msg,
                },
            })
    log(f"extracted {len(out)} assistant frame(s) from last_batch.bin")
    return out


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("sid", help="session id (must already exist in mock)")
    parser.add_argument("prompt", help="user prompt for --cloud")
    args = parser.parse_args()
    reset_cfg()
    (POC / "last_batch.bin").unlink(missing_ok=True)
    rc = run_cloud(args.prompt)
    # Inject the prompt back as a user event so the teleport TUI sees the
    # user side of the turn too.
    inject(args.sid, {
        "type": "event",
        "data": {
            "uuid": "a2-user-1",
            "type": "user",
            "message": {"role": "user", "content": args.prompt},
        },
    })
    frames = extract_frames_from_last_batch()
    for f in frames:
        inject(args.sid, f)
    if not frames:
        log("WARNING: no assistant frames extracted. last_batch.bin schema "
            "may differ from what we expect — re-inspect.")
    return 0 if rc == 0 else rc


if __name__ == "__main__":
    sys.exit(main())
```

Make executable:
```bash
chmod +x /tmp/cc-cloud-poc/driver_a.py
python3 -c "import ast; ast.parse(open('/tmp/cc-cloud-poc/driver_a.py').read()); print('ok')"
```

- [ ] **Step 2: Round-trip A.2 — driver_a + teleport TUI**

Two terminals (or one with backgrounding). Driver A runs the `--cloud` backend; teleport TUI subscribes to the same sid via SSE.

```bash
# Reset
ss -tlnp 2>/dev/null | grep -q ':8181 ' && fuser -k 8181/tcp 2>/dev/null
sleep 1
rm -rf /tmp/cc-cloud-poc/claude-cfg
rm -f /tmp/cc-cloud-poc/teleport-tui.log /tmp/cc-cloud-poc/gateway.log \
      /tmp/cc-cloud-poc/driver-a-tui.log /tmp/cc-cloud-poc/driver-a-stderr.log
nohup python3 /tmp/cc-cloud-poc/mock_gateway.py > /tmp/cc-cloud-poc/gateway.log 2>&1 &
sleep 1

# Pre-create session
SID=$(curl -sS -X POST http://127.0.0.1:8181/cccloud11ab/v1/sessions \
  -d '{"title":"poc-A2","events":[]}' \
  -H 'content-type: application/json' | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
echo "SID=$SID"

# Start teleport TUI in background, give it time to subscribe
( timeout 60 /tmp/cc-cloud-poc/run_teleport.sh "$SID" > /tmp/cc-cloud-poc/teleport-run.log 2>&1 ) &
TELEPORT_PID=$!
sleep 4

# Run driver_a (this forks --cloud, harvests frames, injects into sid)
python3 /tmp/cc-cloud-poc/driver_a.py "$SID" "Say the single word: hello"

sleep 4  # give teleport time to render injected frames
kill -INT $TELEPORT_PID 2>/dev/null
wait $TELEPORT_PID 2>/dev/null
ss -tlnp 2>/dev/null | grep ':8181 ' | grep -oP 'pid=\d+' | head -1 | cut -d= -f2 | xargs -r kill 2>/dev/null
sleep 1
```

- [ ] **Step 3: Inspect + classify**

```bash
echo "===== Teleport TUI log ====="
sed -E 's/\x1B\[[0-9;]*[A-Za-z]//g; s/\x1B\][^\x07]*\x07//g; s/\r//g' /tmp/cc-cloud-poc/teleport-tui.log | tail -80
echo
echo "===== Driver A output log ====="
sed -E 's/\x1B\[[0-9;]*[A-Za-z]//g; s/\r//g' /tmp/cc-cloud-poc/driver-a-tui.log | tail -40
echo
echo "===== Gateway log (driver_a + teleport hits) ====="
cat /tmp/cc-cloud-poc/gateway.log
```

Classification:

**(A.2-PASS)** Teleport TUI shows the assistant's actual word "hello" (or the model's response to the prompt). Driver log shows `extracted N>=1 assistant frame(s)` and `injected` lines. Gateway log shows the corresponding `SSE-><sid>` emissions.

**(A.2-SCHEMA-MISMATCH)** Driver ran but `extract_frames_from_last_batch` returned 0 frames. Inspect `/tmp/cc-cloud-poc/last_batch.bin` with `python3 -c "import json; d=json.load(open('/tmp/cc-cloud-poc/last_batch.bin')); print(list(d.keys())); print(json.dumps(d, indent=2)[:3000])"` and refine the extractor heuristic. One iteration loop allowed per spec §6.

**(A.2-FAIL)** Per spec §6: > 2h on this sub-phase, or repeated extractor refinements still produce nothing useful. Record the failure; A.2 ends with no usable backend-A path; continue to A.3 (independent per spec §4).

Write `/tmp/cc-cloud-poc/iteration-12.md` (or successor) recording the classification, the `last_batch.bin` schema observed, and the per-turn frame count.

---

## Task 5: A.3 — backend B driver (`claude -p stream-json`)

**Files:**
- Create: `/tmp/cc-cloud-poc/driver_b.py`

Backend B uses the unpatched binary (well, still patched — but `-p` doesn't go through the `--cloud` path so most of the patches don't matter). Output format `stream-json` emits one JSON-per-line covering text, thinking, tool_use, permission_request — a documented format we don't have to reverse-engineer.

- [ ] **Step 1: Write driver_b.py**

Create `/tmp/cc-cloud-poc/driver_b.py`:

```python
#!/usr/bin/env python3
"""driver_b.py — Backend B adapter for cc-app-gateway PoC-2.

Forks `claude -p '<prompt>' --output-format stream-json --verbose`, reads
each line, translates each stream-json event into a v2 transport frame,
and injects into the local mock's SSE queue for sid <sid>.

Usage:
    driver_b.py <sid> <prompt>

stream-json line shape (per `claude -p --output-format stream-json` docs):
  Each line is a JSON object with type ∈ {system, user, assistant, result, ...}
  assistant lines carry the message: {message:{role, content:[{type:text|tool_use|thinking, ...}]}}
"""
import argparse
import json
import os
import subprocess
import sys
import urllib.request
from pathlib import Path

POC = Path("/tmp/cc-cloud-poc")
MOCK_BASE = "http://127.0.0.1:8181"
PATCHED_BINARY = POC / "claude-167-patched"  # also fine for -p; patches don't affect this path


def log(msg: str) -> None:
    print(f"[driver_b] {msg}", flush=True)


def inject(sid: str, frame: dict) -> None:
    body = json.dumps(frame).encode()
    req = urllib.request.Request(
        f"{MOCK_BASE}/_poc/sessions/{sid}/inject",
        data=body,
        headers={"content-type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=5) as resp:
        log(f"injected (status={resp.status}) {json.dumps(frame)[:120]}")


def stream_json_to_frame(line_obj: dict) -> dict | None:
    """Translate one stream-json line into a v2 transport frame.

    Returns None for lines we don't surface (system, result, etc.).
    """
    t = line_obj.get("type")
    if t in ("assistant", "user"):
        msg = line_obj.get("message") or {}
        # stream-json's assistant message already has role+content[] in the
        # exact shape replBridgeTransport expects. Wrap it in the v2 envelope.
        return {
            "type": "event",
            "data": {
                "uuid": line_obj.get("session_id", "")
                + "-" + str(line_obj.get("turn_index", 0)),
                "type": t,
                "message": msg,
            },
        }
    return None


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("sid", help="session id (must already exist in mock)")
    parser.add_argument("prompt", help="user prompt for `claude -p`")
    args = parser.parse_args()

    env = os.environ.copy()
    # -p ignores OAuth; an API key works. Set a no-op so the binary doesn't
    # complain. Real key not needed when the binary is configured for our mock?
    # Actually -p WILL try to hit api.anthropic.com / v1/messages by default.
    # For PoC-2 we POINT IT AT THE SAME MOCK so the cost of the backend's
    # /v1/messages call is paid against our titlegen stub — meaning A.3 is
    # measuring the IPC overhead, not real model responses. This is
    # acceptable for the matrix (A and B both use the mock for token cost;
    # the comparison is about frame fidelity, latency, and patch maintenance).
    env.update({
        "ANTHROPIC_BASE_URL": f"{MOCK_BASE}/cccloud11ab",
        "ANTHROPIC_API_KEY": "fake-poc-key",
        "DISABLE_INSTALLATION_CHECKS": "1",
        "CLAUDE_CODE_SANDBOXED": "1",
    })

    cmd = [
        str(PATCHED_BINARY), "-p", args.prompt,
        "--output-format", "stream-json",
        "--verbose",  # required with stream-json for full event detail
        "--include-partial-messages",  # so we see text deltas too
    ]
    log(f"forking: {' '.join(cmd)}")

    proc = subprocess.Popen(
        cmd, env=env,
        stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        text=True, bufsize=1,
    )
    n_lines = 0
    n_injected = 0
    try:
        for line in proc.stdout:
            line = line.strip()
            if not line:
                continue
            n_lines += 1
            try:
                obj = json.loads(line)
            except Exception:
                log(f"non-JSON line: {line[:100]}")
                continue
            frame = stream_json_to_frame(obj)
            if frame is not None:
                inject(args.sid, frame)
                n_injected += 1
    finally:
        rc = proc.wait(timeout=60)
        stderr = proc.stderr.read()
        log(f"-p exited rc={rc}, lines={n_lines}, injected={n_injected}")
        if stderr.strip():
            log(f"stderr: {stderr.strip()[:500]}")
    return 0 if rc == 0 else rc


if __name__ == "__main__":
    sys.exit(main())
```

```bash
chmod +x /tmp/cc-cloud-poc/driver_b.py
python3 -c "import ast; ast.parse(open('/tmp/cc-cloud-poc/driver_b.py').read()); print('ok')"
```

- [ ] **Step 2: Round-trip A.3**

```bash
# Reset
ss -tlnp 2>/dev/null | grep -q ':8181 ' && fuser -k 8181/tcp 2>/dev/null
sleep 1
rm -rf /tmp/cc-cloud-poc/claude-cfg
rm -f /tmp/cc-cloud-poc/teleport-tui.log /tmp/cc-cloud-poc/gateway.log
nohup python3 /tmp/cc-cloud-poc/mock_gateway.py > /tmp/cc-cloud-poc/gateway.log 2>&1 &
sleep 1

SID=$(curl -sS -X POST http://127.0.0.1:8181/cccloud11ab/v1/sessions \
  -d '{"title":"poc-A3","events":[]}' \
  -H 'content-type: application/json' | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
echo "SID=$SID"

( timeout 60 /tmp/cc-cloud-poc/run_teleport.sh "$SID" > /tmp/cc-cloud-poc/teleport-run.log 2>&1 ) &
TELEPORT_PID=$!
sleep 4

python3 /tmp/cc-cloud-poc/driver_b.py "$SID" "Say the single word: hello"

sleep 4
kill -INT $TELEPORT_PID 2>/dev/null
wait $TELEPORT_PID 2>/dev/null
ss -tlnp 2>/dev/null | grep ':8181 ' | grep -oP 'pid=\d+' | head -1 | cut -d= -f2 | xargs -r kill 2>/dev/null
sleep 1

echo "===== Teleport TUI ====="
sed -E 's/\x1B\[[0-9;]*[A-Za-z]//g; s/\x1B\][^\x07]*\x07//g; s/\r//g' /tmp/cc-cloud-poc/teleport-tui.log | tail -80
echo
echo "===== Gateway log ====="
cat /tmp/cc-cloud-poc/gateway.log
```

- [ ] **Step 3: Classify A.3**

**(A.3-PASS)** Teleport TUI shows the assistant's response. Driver log shows `injected=N>=1` lines.

**(A.3-FRAME-SHAPE-MISMATCH)** Driver ran but teleport TUI didn't render the frames — wire shape from stream-json doesn't match what `handleInboundMessage` expects. Inspect mock log for `SSE-><sid>` content; refine `stream_json_to_frame()`; one iteration loop allowed per spec §6.

**(A.3-FAIL)** Per spec §6 limits. Record and continue to A.4.

Write `/tmp/cc-cloud-poc/iteration-13.md` (or successor) — classification + per-turn frame count + observed stream-json line distribution.

---

## Task 6: A.4 — decision matrix

**Files:**
- Create: `/tmp/cc-cloud-poc/decision-matrix.md`

Synthesize the data collected during A.2 and A.3 into a 5-dimension comparison. The matrix is the primary deliverable for Phase 0 — its quality determines whether Phase 1 spec authoring can proceed.

- [ ] **Step 1: Collect raw measurements (n=3 per cell where applicable)**

For each backend (A, B), run the round-trip 3× with the same prompt and capture:

- Wall-clock from process spawn to first injected frame (use `time` and grep the driver log timestamps).
- Wall-clock from process spawn to last injected frame.
- Driver log `injected=N` count.
- Per-turn input/output token count if observable from the binary's own telemetry (PoC-1 telemetry NDJSON `tengu_api_success` events carry `input_tokens` and `output_tokens`; check `claude-cfg/telemetry/`).

Example collection harness (run 3× per backend, paste results into matrix):

```bash
for i in 1 2 3; do
  rm -rf /tmp/cc-cloud-poc/claude-cfg
  rm -f /tmp/cc-cloud-poc/gateway.log /tmp/cc-cloud-poc/driver-a-tui.log
  ss -tlnp 2>/dev/null | grep -q ':8181 ' && fuser -k 8181/tcp 2>/dev/null
  sleep 1
  nohup python3 /tmp/cc-cloud-poc/mock_gateway.py > /tmp/cc-cloud-poc/gateway.log 2>&1 &
  sleep 1
  SID=$(curl -sS -X POST http://127.0.0.1:8181/cccloud11ab/v1/sessions -d '{"title":"bench","events":[]}' -H 'content-type: application/json' | python3 -c 'import sys,json; print(json.load(sys.stdin)["id"])')
  echo "=== run $i, backend A, sid=$SID ==="
  /usr/bin/time -f 'real=%es' python3 /tmp/cc-cloud-poc/driver_a.py "$SID" "Say hello" 2>&1 | tail
  ss -tlnp 2>/dev/null | grep ':8181 ' | grep -oP 'pid=\d+' | head -1 | cut -d= -f2 | xargs -r kill 2>/dev/null
  sleep 1
done
```

Repeat with `driver_b.py`.

- [ ] **Step 2: Run the tool_use and permission_request probe turns**

Frame fidelity dimensions need at least one turn that exercises the relevant frame type:

- `tool_use`: prompt `"Use the Bash tool to run: echo hi. Just run it; don't explain."` — both drivers should produce at least one tool_use frame.
- `permission_request`: prompt `"Use the Bash tool to run: rm -rf /tmp/some-bogus-path"` — should trigger a permission prompt frame in `-p` mode (may or may not surface in `--cloud` mode).

For each, inspect the injected frames (grep gateway log for `SSE->`) and judge fidelity yes/partial/no per the spec's §5.4 definition.

- [ ] **Step 3: Write decision-matrix.md**

Create `/tmp/cc-cloud-poc/decision-matrix.md`:

```markdown
# cc-app-gateway Backend Decision Matrix (Phase 0 / PoC-2)

Date: 2026-06-08
Spec: docs/superpowers/specs/2026-06-07-cc-app-gateway-phase0-design.md §5.4
Method: 3 runs per cell for token/latency; single trial for fidelity.

| Dimension | A (patched --cloud) | B (claude -p stream-json) | Notes |
|---|---|---|---|
| Token usage per turn (in/out) | <mean ± stdev> | <mean ± stdev> | from tengu_api_success telemetry |
| First-frame latency | <mean s> | <mean s> | spawn → first inject |
| Last-frame latency  | <mean s> | <mean s> | spawn → last inject |
| Frame fidelity: text | <yes/partial/no> | <yes/partial/no> | "Say hello" prompt |
| Frame fidelity: thinking | <yes/partial/no/NA> | <yes/partial/no/NA> | model-dependent |
| Frame fidelity: tool_use | <yes/partial/no> | <yes/partial/no> | bash `echo hi` |
| Frame fidelity: permission_request | <yes/partial/no> | <yes/partial/no> | rm in /tmp |
| Controllability: cancel | <description> | <description> | SIGINT mid-turn |
| Maintenance: patch count | 2 (P1+P2 from PoC-1) | 2 (same; only thin-client side) | |
| Maintenance: upgrade risk | <qualitative> | <qualitative> | Bun bundle drift / stream-json schema stability |

## Recommendation

<one paragraph: A vs B for Phase 1 cc-app-gateway, with the dominant factor named.
Acceptable to say "either viable" if the matrix is genuinely a tie. Acceptable
to say "neither viable" if both fail enough fidelity dimensions.>

## Evidence pointers

- Backend A run logs: `/tmp/cc-cloud-poc/driver-a-tui.log`, gateway log excerpts in `iteration-12.md`
- Backend B run logs: `/tmp/cc-cloud-poc/driver-b-tui.log`, gateway log excerpts in `iteration-13.md`
- Telemetry batches: `/tmp/cc-cloud-poc/last_batch.bin`, `claude-cfg/telemetry/*.json`
```

Fill in every `<…>` from the actual measurements. Do NOT hand-wave any cell — if you didn't measure it because a sub-phase failed, write "N/A — A.<n> stuck-pointed" with a pointer to the iteration journal.

- [ ] **Step 4: Append the iteration journal**

Write `/tmp/cc-cloud-poc/iteration-14.md` (or wherever the numbering lands):
```markdown
# Iteration N — A.4 decision matrix

- Matrix: /tmp/cc-cloud-poc/decision-matrix.md
- Recommendation: <A | B | either | neither>
- Dominant factor: <one sentence>

## Conditions under which the recommendation would flip

<short bullet list — e.g. "if Anthropic deprecates stream-json output format,
B becomes unviable">
```

---

## Task 7: Write spec §11 outcome

**Files:**
- Modify: `docs/superpowers/specs/2026-06-07-cc-app-gateway-phase0-design.md`

- [ ] **Step 1: Replace §11 with the actual outcome**

The current §11 is `_Pending execution._`. Replace with the structured outcome below; choose the matching template based on what actually happened.

**Template for PASS (all three sub-phases ran):**

```markdown
## 11. Outcome — PASS (A.1, A.2, A.3 all ran; matrix written)

PoC-2 completed on <date> with N iteration journals
(`iteration-10.md` … `iteration-1N.md`) under `/tmp/cc-cloud-poc/`.

### Sub-phase status

| Sub-phase | Status | Pass criterion |
|---|---|---|
| A.1 subscribe-half | PASS | Teleport TUI rendered `echo: hello` from manually injected frame; bridge handshake + SSE subscribe + injection chain verified. |
| A.2 backend A (patched --cloud) | <PASS / SCHEMA-MISMATCH / FAIL> | <one-line evidence> |
| A.3 backend B (claude -p stream-json) | <PASS / SHAPE-MISMATCH / FAIL> | <one-line evidence> |
| A.4 decision matrix | DONE | `/tmp/cc-cloud-poc/decision-matrix.md` |

### Mock surface added in PoC-2

- `POST /v1/code/sessions/{sid}/bridge` — worker_jwt + epoch exchange
- `GET /v1/code/sessions/{sid}/events/stream` — SSE (with `/v1/sessions/…/events/stream` alias)
- `POST /worker/events/{event_id}/delivery` — ack sink
- `POST /_poc/sessions/{sid}/inject` — driver-facing (NOT Anthropic surface)
- Plus any additional routes added during iteration loops; full list in iteration journals.

### Decision matrix summary

| | A (patched --cloud) | B (claude -p stream-json) |
|---|---|---|
| Token usage / turn (in / out) | <…> | <…> |
| First-frame latency | <…> | <…> |
| Frame fidelity (text/think/tool/perm) | <…> | <…> |
| Maintenance burden | <…> | <…> |

Recommendation for Phase 1 cc-app-gateway: **<A | B | either | neither>**.
Dominant factor: <one sentence>.

### Implications for Phase 1 design

<2-3 paragraphs: what Phase 1 should bake in, what it should defer, what
unknowns the matrix exposed that Phase 1 needs to handle.>

### Artifacts archived

PoC-2 files (`mock_gateway.py` extensions, `driver_a.py`, `driver_b.py`,
`decision-matrix.md`, `iteration-1{0..N}.md`, run logs) remain under
`/tmp/cc-cloud-poc/`. Not committed; the conclusion + recipe is in this
spec section + updated `cc_binary_internals.md`.
```

**Template for STUCK at A.1 (Phase 0 ended on the gate):**

```markdown
## 11. Outcome — STUCK at A.1 (subscribe-half infeasible)

PoC-2 aborted on <date> after N iteration journals; per spec §6, A.1
is a hard gate and Phase 0 cannot proceed without it.

### Last successful step

<plain English; e.g. "Bridge handshake succeeded — binary received
worker_jwt and opened SSE — but inbound frames are rejected because
the binary expects an asymmetrically-signed event whose signature it
verifies client-side.">

### Stopping evidence

Teleport TUI log:
\`\`\`
<paste 5-20 lines>
\`\`\`

Gateway log:
\`\`\`
<paste 5-20 lines>
\`\`\`

Telemetry NDJSON:
\`\`\`
<paste decoded events>
\`\`\`

### Why same-length patching can't proceed

<one paragraph>

### Implications

A real cc-app-gateway in the codex-app-gateway shape is **infeasible
without breaking the signed-frame check**, which is a much larger
binary-patching effort than the 2 patches PoC-1 needed. The codex-primary
stance is **reinforced**, not changed. See updated
`agent_integration_strategy.md`.
```

**Template for PARTIAL (A.1 passed, one of A.2/A.3 failed):**

```markdown
## 11. Outcome — PARTIAL (A.1 PASS, A.<2|3> failed)

A.1 verified the subscribe-half. Backend <A|B> failed at <which step>;
backend <other> succeeded. Phase 1 design can proceed with one backend
characterised; the failed backend is recorded as "not viable per
PoC-2" rather than "untried."

<then same structure as PASS template, with the failed backend's row
marked clearly and the recommendation honestly favoring the
characterised one OR noting that Phase 1 needs to revisit the failed
backend with different tactics.>
```

Pick the template that matches; fill in every `<…>`. The literal `<…>` markers in the templates are explicitly runtime values (not plan placeholders).

---

## Task 8: Update memory

**Files:**
- Modify: `/root/.claude/projects/-root-agentserver/memory/cc_binary_internals.md`
- Modify: `/root/.claude/projects/-root-agentserver/memory/MEMORY.md`
- Modify (conditional): `/root/.claude/projects/-root-agentserver/memory/agent_integration_strategy.md`

- [ ] **Step 1: Append v2 transport endpoint shape to `cc_binary_internals.md`**

Read the current file (after PoC-1 it has a "v2.1.167 re-verification" section and a patch-set + mock-surface block). Append a new sub-section under "v2.1.167 mock surface" (or wherever the v2.1.167 block ends):

```markdown
### v2.1.167 subscribe-half (v2 CCR transport) — PoC-2 (2026-06-08)

Bootstrap (per `/root/cc/source/src/bridge/remoteBridgeCore.ts` v2.1.88, confirmed via PoC-2 against the live binary):

1. `POST /v1/code/sessions` → `{id, ...}`
2. `POST /v1/code/sessions/{id}/bridge` → `{worker_jwt, worker_epoch, api_base_url, expires_in}`. Each call bumps epoch monotonically. JWT is bearer for `/worker/*` only; binary does NOT cryptographically verify it locally before passing it back.
3. `GET /v1/code/sessions/{id}/events/stream` (Accept: text/event-stream) — SSE long-poll. Each frame: `data: <json>\n\n`. `:` lines are keep-alive comments.
4. `POST /worker/events/{event_id}/delivery` with `{status: "processing"|"processed"}` — binary's ack on processed inbound frames.
5. (Optional) `PUT /worker/state`, `PUT /worker/external_metadata` — only required if certain UI features touch them.

Triggered locally by `CLAUDE_CODE_USE_CCR_V2=1` env var; without it the binary may prefer the legacy v1 hybrid (WebSocket reads + POST writes) path.

Inbound v2 frame shape (`handleInboundMessage` dispatches these):
```json
{"type": "event", "data": {
   "uuid": "<uuid>", "session_id": "<sid>",
   "type": "assistant" | "user" | "system",
   "message": {"role": "assistant"|"user", "content": [{"type":"text"|"tool_use"|"thinking"|..., ...}]}
}}
```

### v2.1.167 backend choice for cc-app-gateway (PoC-2 conclusion)

**Recommendation: <A | B | either | neither>** — see `docs/superpowers/specs/2026-06-07-cc-app-gateway-phase0-design.md` §11 for the matrix and rationale.

Dominant trade-off: <one sentence>. Patch budget stays at 2 (P1, P2 from PoC-1) for the thin-client front-end regardless of backend choice; backend A reuses the same patched binary, backend B uses an unpatched binary in `-p` mode.
```

Fill in the `<…>` placeholders from the actual matrix.

- [ ] **Step 2: Update `MEMORY.md` hook**

Read `/root/.claude/projects/-root-agentserver/memory/MEMORY.md`. The hook for `cc_binary_internals.md` currently reads:

```
- [Claude Code v2.1.128/167 binary internals](cc_binary_internals.md) — patch points, env vars, dispatch protocol; v2.1.167 adds --cloud / isThinClient / bridge:repl + 2-patch redirect recipe
```

Replace with:

```
- [Claude Code v2.1.128/167 binary internals](cc_binary_internals.md) — patch points, env vars, dispatch protocol; v2.1.167 --cloud thin-client + v2 CCR transport endpoints + PoC-2 backend recipe
```

- [ ] **Step 3: Update `agent_integration_strategy.md` ONLY if Phase 0 changed the strategic stance**

Read the file. If the PoC-2 outcome changes the codex-primary conclusion (e.g. one backend turned out to be dramatically cheaper / lower-maintenance than expected, or A.1 STUCK reinforces "not feasible"), append a short update mirroring the existing 2026-06-07 update block. If the outcome is "Phase 1 can proceed with backend X but it doesn't displace codex," do NOT touch this file — the PoC-1 update already says "strategic stance unchanged."

- [ ] **Step 4: No commit yet — Task 9 batches all in-repo changes**

---

## Task 9: Commit + push + PR

The branch already has the spec commit (`22315c1`). This task adds the outcome, plan checkboxes, and memory updates as a second commit, then pushes and opens a PR against `main`.

- [ ] **Step 1: Confirm branch**

```bash
git rev-parse --abbrev-ref HEAD
```

Expected: `spec/cc-app-gateway-phase0`. If not, `git checkout spec/cc-app-gateway-phase0`.

- [ ] **Step 2: Diff sanity check**

```bash
git status -sb
git diff --stat
```

Expected modifications (all in repo):
- `docs/superpowers/specs/2026-06-07-cc-app-gateway-phase0-design.md` (§11 filled)
- `docs/superpowers/plans/2026-06-07-cc-app-gateway-phase0.md` (checkboxes ticked)

Memory files at `/root/.claude/...` are updated on disk but NOT staged (they're outside the repo).

Confirm NOTHING under `/tmp/cc-cloud-poc/` appears in `git status` (it never should — it's outside the repo — but a paranoid `find . -path ./.git -prune -o -name 'mock_gateway.py' -print` is cheap).

- [ ] **Step 3: Stage and commit**

```bash
git add docs/superpowers/specs/2026-06-07-cc-app-gateway-phase0-design.md \
        docs/superpowers/plans/2026-06-07-cc-app-gateway-phase0.md
```

For PASS outcome:
```bash
git commit -m "$(cat <<'EOF'
docs(superpowers): cc-app-gateway Phase 0 PoC — PASS

PoC-2 verified v2.1.167's subscribe-half end-to-end (A.1: teleport
TUI rendered a manually injected `echo: hello` SSE frame after
bridge JWT exchange), then trialled both backend candidates:

  - Backend A (patched --cloud + telemetry harvest): <PASS|FAIL>
  - Backend B (claude -p --output-format stream-json):   <PASS|FAIL>

Decision matrix at /tmp/cc-cloud-poc/decision-matrix.md compares the
two on token usage, first/last-frame latency, frame fidelity
(text/thinking/tool_use/permission_request), controllability, and
maintenance burden. Recommendation for the Phase 1 cc-app-gateway
spec: backend <A|B|either>.

Mock surface added in PoC-2 (kept under /tmp/cc-cloud-poc/, not
committed): POST /v1/code/sessions/{sid}/bridge (worker_jwt+epoch),
GET .../events/stream (SSE), POST /worker/events/{id}/delivery,
plus a /_poc/sessions/{sid}/inject sibling for driver→mock frame
injection.

See spec section 11 for matrix and implications; see updated
cc_binary_internals.md memory for the v2 transport recipe.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

For STUCK outcome (template):
```bash
git commit -m "$(cat <<'EOF'
docs(superpowers): cc-app-gateway Phase 0 PoC — STUCK at A.1

PoC-2's A.1 hard gate (subscribe-half) could not pass: <one-sentence
reason>. Per spec §6 / §4, A.2 and A.3 cannot run without A.1, so
Phase 0 ends here.

See spec section 11 for the stopping evidence and the implication
for Phase 1 cc-app-gateway design (likely "not feasible in this
shape"). codex-primary stance reinforced; see updated memory.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

For PARTIAL outcome — adapt PASS commit message body to honestly describe which sub-phase failed.

- [ ] **Step 4: Push branch**

```bash
git push -u github spec/cc-app-gateway-phase0
```

Expected: branch created on remote.

- [ ] **Step 5: Open PR**

```bash
gh pr create --base main --head spec/cc-app-gateway-phase0 \
  --title "docs(superpowers): cc-app-gateway Phase 0 PoC (v2.1.167 subscribe-half + backend matrix)" \
  --body "$(cat <<'EOF'
Builds on PR #229 (PoC-1: v2.1.167 --cloud redirect verified, 2 patches).
Phase 0 answers the two questions left open before a real
cc-app-gateway can be designed:

1. **Subscribe-half wire format** — extended the mock with v2 CCR
   transport endpoints (`/bridge` worker JWT exchange, SSE events
   stream, `/worker/*` ack sink) and verified the loop end-to-end
   against `claude --teleport <sid>` using PoC-1's patched binary.
2. **Backend harness selection** — trialled two candidates:
   - A: patched `--cloud` reused as a backend, frames harvested from
     telemetry batches.
   - B: plain `claude -p '<prompt>' --output-format stream-json`,
     translated line-by-line into v2 SSE frames.

Outcome: <PASS|PARTIAL|STUCK>; recommendation for the future Phase 1
spec recorded in spec section 11.

PoC-2 artifacts (driver_a.py, driver_b.py, decision-matrix.md, mock
gateway extensions, 5+ iteration journals) live under
`/tmp/cc-cloud-poc/` and are deliberately NOT committed — the value
is the conclusion and the recipe, captured in the spec and the
updated `cc_binary_internals.md` memory.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 6: Done.**

---

## Self-Review

Walked through this plan against the spec.

**Spec coverage:**
- §1 Problem → context; no task.
- §2 Goal → Tasks 2–6 cover A.1–A.4.
- §3 Scope (in-scope items) → Tasks 2 (mock extensions), 4-5 (drivers), 6 (matrix). Out-of-scope items are explicitly absent from any task.
- §4 Approach (sequential, A.1 hard-gate) → Task 3 Step 5 enforces the gate (`A.1-FAIL → write Task 9 STUCK report; Phase 0 ends here`).
- §5.1–5.4 (architecture components) → Task 2 (mock endpoints), Task 4 (driver_a), Task 5 (driver_b), Task 6 (matrix).
- §6 Stuck-point policy → invoked in Task 3 Step 5, Task 4 Step 3, Task 5 Step 3.
- §7 Data flow A.1 → Task 3 Steps 2-4.
- §8 Data flow A.2/A.3 → Task 4 Step 2 / Task 5 Step 2.
- §9 Pass/fail → embedded in classification steps.
- §10 Repo footprint → Task 9 Step 2 enforces "nothing from /tmp staged" + Tasks 7-8 separate in-repo vs out-of-repo updates.
- §11 Outcome → Task 7 (templates for PASS/STUCK/PARTIAL).

**Placeholder scan:** All `<…>` markers in this plan are inside the §11 templates (Task 7) or commit-message templates (Task 9) and are explicitly described as "fill in from the actual run." No "TBD" / "implement later" / "add appropriate handling" appears outside those runtime-value contexts.

**Type / symbol consistency:**
- `sse_queues`, `sse_epochs`, `inject_frame`, `bridge_exchange`, `stream_events_v2_sse`, `worker_event_delivery` all defined in Task 2 and referenced consistently in Tasks 3, 4, 5.
- `_poc/sessions/{sid}/inject` path used identically across Task 2 (registration), Task 3 Step 3 (manual curl), Task 4 Step 1 (driver_a's `inject()`), Task 5 Step 1 (driver_b's `inject()`).
- `MOCK_BASE = "http://127.0.0.1:8181"` constant in both drivers; mock listens on the same `127.0.0.1:8181` from PoC-1 (matches `BASE_URL` in `mock_gateway.py`).
- `run_teleport.sh` created in Task 3 used unchanged in Tasks 4 and 5.
