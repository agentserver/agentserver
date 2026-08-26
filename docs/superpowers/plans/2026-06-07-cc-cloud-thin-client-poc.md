# Claude Code v2.1.167 `--cloud` Thin-Client Redirect PoC — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prove or disprove that v2.1.167 can be redirected to a local mock gateway in `--cloud` thin-client mode and complete a round-trip ("user types hello → TUI renders echo: hello").

**Architecture:** Three single-file PoC artifacts in `/tmp/cc-cloud-poc/` (mock gateway, patch script, run script). Same-length binary patches on a copy of `/root/.local/share/claude/versions/2.1.167`. Iterate patch → run → observe until round-trip works or a stuck point is hit. Only the spec and a final outcome/memory update land in the repo.

**Tech Stack:** Python 3.13+ (`aiohttp`, `argparse`, stdlib `socket`/`re`/`pathlib`); bash + `script(1)` for PTY; `strings`/`grep -aEo` for binary auditing; existing `/root/.local/share/claude/versions/2.1.167` ELF.

**Spec:** `docs/superpowers/specs/2026-06-06-cc-cloud-thin-client-poc-design.md`

**Branch:** `spec/cc-cloud-thin-client-poc` (already has the spec committed).

**Repo footprint (final):** updates to this plan's outcome section, the spec's section 11, and two memory files. No source code lands in the repo. PoC artifacts stay in `/tmp/cc-cloud-poc/`.

---

## File Structure

| Path | Role | Repo? |
|---|---|---|
| `/tmp/cc-cloud-poc/patch_cli.py` | Same-length byte-substitution patcher; canonical record of the patch set | No |
| `/tmp/cc-cloud-poc/mock_gateway.py` | aiohttp server: `POST /v1/code/sessions`, `WS /v1/code/sessions/{id}/stream`, etc. | No |
| `/tmp/cc-cloud-poc/run_poc.sh` | Boots patched binary under PTY with fake-identity env | No |
| `/tmp/cc-cloud-poc/claude-167-patched` | Output of `patch_cli.py` | No |
| `/tmp/cc-cloud-poc/tui.log` | `script(1)` capture of TUI output | No |
| `/tmp/cc-cloud-poc/gateway.log` | Mock gateway log stream | No |
| `/tmp/cc-cloud-poc/iteration-NN.md` | One short journal entry per iteration loop | No |
| `docs/superpowers/specs/2026-06-06-cc-cloud-thin-client-poc-design.md` | Spec — append section 11 outcome | Yes (modify) |
| `docs/superpowers/plans/2026-06-07-cc-cloud-thin-client-poc.md` | This plan — task checkboxes get ticked off as we go | Yes (modify) |
| `/root/.claude/projects/-root-agentserver/memory/agent_integration_strategy.md` | Memory — update with PoC outcome | Yes (modify) |
| `/root/.claude/projects/-root-agentserver/memory/cc_binary_internals.md` | Memory — update v2.1.167 patch points | Yes (modify) |
| `/root/.claude/projects/-root-agentserver/memory/MEMORY.md` | Index — update memory pointer descriptions | Yes (modify) |

---

## Task 1: Workspace setup

**Files:**
- Create: `/tmp/cc-cloud-poc/`
- Create (placeholder): `/tmp/cc-cloud-poc/iteration-00.md`

- [ ] **Step 1: Confirm binary exists, locate strings**

Run:
```bash
ls -la /root/.local/share/claude/versions/2.1.167
file /root/.local/share/claude/versions/2.1.167
```

Expected: file exists, ~245 MB, `ELF 64-bit LSB executable ... not stripped`.

If the binary is missing or stripped, STOP and report — patch tooling depends on `strings` finding readable JS bundle text.

- [ ] **Step 2: Create workspace directory and copy the binary**

Run:
```bash
mkdir -p /tmp/cc-cloud-poc
cp /root/.local/share/claude/versions/2.1.167 /tmp/cc-cloud-poc/claude-167-original
chmod +x /tmp/cc-cloud-poc/claude-167-original
ls -la /tmp/cc-cloud-poc/
```

Expected: `claude-167-original` present, same size as source.

- [ ] **Step 3: Verify `aiohttp` is importable**

Run:
```bash
python3 -c "import aiohttp; print(aiohttp.__version__)"
```

Expected: prints a version number (e.g. `3.x.x`). If `ModuleNotFoundError`, install:
```bash
python3 -m pip install --user aiohttp
```
…and re-run.

- [ ] **Step 4: Verify `script(1)` exists (provides PTY for `--cloud`)**

Run:
```bash
which script && script --version | head -1
```

Expected: path present, version line printed. If missing, `apt-get install bsdmainutils util-linux` (or distro equivalent) before continuing.

- [ ] **Step 5: Seed iteration log**

Write `/tmp/cc-cloud-poc/iteration-00.md`:
```markdown
# Iteration 00 — workspace ready

- Binary: /tmp/cc-cloud-poc/claude-167-original (245 MB, ELF, not stripped)
- aiohttp: <version>
- script(1): <version>
- Next: build mock_gateway skeleton and patch_cli skeleton
```

Fill in the placeholders with the actual versions captured above.

- [ ] **Step 6: No commit** — `/tmp/cc-cloud-poc/` is not tracked. Move to Task 2.

---

## Task 2: `mock_gateway.py` skeleton

**Files:**
- Create: `/tmp/cc-cloud-poc/mock_gateway.py`

The skeleton implements the endpoints the spec lists (§5.2). We start with the minimum body and grow it during iteration only if the TUI demands additional fields.

- [ ] **Step 1: Write the file**

Create `/tmp/cc-cloud-poc/mock_gateway.py`:

```python
#!/usr/bin/env python3
"""Mock Anthropic /v1/code/sessions gateway for cc-cloud-poc.

Listens on 127.0.0.1:8181. Logs every request and WS frame with
millisecond timestamps to stdout (which run_poc.sh tees into
/tmp/cc-cloud-poc/gateway.log).
"""
import argparse
import asyncio
import json
import time
import uuid
from collections import defaultdict
from typing import Any

from aiohttp import WSMsgType, web

HOST = "127.0.0.1"
PORT = 8181

# Must match the URL prefix written into the patched binary (Patch 1).
# Keep updated as the patcher pins the exact prefix.
BASE_URL = f"http://{HOST}:{PORT}/cccloud11ab"

sessions: dict[str, dict[str, Any]] = {}
ws_clients: dict[str, list[web.WebSocketResponse]] = defaultdict(list)


def ts() -> str:
    t = time.time()
    return time.strftime("%H:%M:%S", time.localtime(t)) + f".{int((t % 1) * 1000):03d}"


def log(msg: str) -> None:
    print(f"[{ts()}] {msg}", flush=True)


async def create_session(request: web.Request) -> web.Response:
    body = await request.text()
    log(f"POST /v1/code/sessions body={body[:500]}")
    sid = str(uuid.uuid4())
    sessions[sid] = {"id": sid, "created_at": time.time()}
    envelope = {
        "id": sid,
        "websocket_url": f"ws://{HOST}:{PORT}/v1/code/sessions/{sid}/stream",
        "status": "active",
    }
    log(f"-> 200 {envelope}")
    return web.json_response(envelope)


async def post_event(request: web.Request) -> web.Response:
    sid = request.match_info["sid"]
    body = await request.text()
    log(f"POST /v1/code/sessions/{sid}/events body={body[:1000]}")
    return web.json_response({"ok": True})


async def get_events(request: web.Request) -> web.Response:
    sid = request.match_info["sid"]
    log(f"GET /v1/code/sessions/{sid}/events query={dict(request.query)}")
    return web.json_response({"events": [], "cursor": None})


async def stream_ws(request: web.Request) -> web.WebSocketResponse:
    sid = request.match_info["sid"]
    ws = web.WebSocketResponse(heartbeat=20.0)
    await ws.prepare(request)
    ws_clients[sid].append(ws)
    log(f"WS open sid={sid}")
    try:
        async for msg in ws:
            if msg.type == WSMsgType.TEXT:
                log(f"WS<-{sid} text={msg.data[:1000]}")
                await maybe_echo(sid, msg.data)
            elif msg.type == WSMsgType.BINARY:
                log(f"WS<-{sid} binary len={len(msg.data)}")
            elif msg.type == WSMsgType.ERROR:
                log(f"WS<-{sid} ERROR {ws.exception()}")
    finally:
        ws_clients[sid].remove(ws)
        log(f"WS close sid={sid}")
    return ws


async def maybe_echo(sid: str, raw: str) -> None:
    """If raw frame contains a user prompt, push back a synthetic assistant message."""
    try:
        data = json.loads(raw)
    except Exception:
        return
    # The exact wire shape is determined during iteration; this is a best-guess
    # that covers the most likely envelopes seen in [bridge:repl] log strings.
    candidates = []
    if isinstance(data, dict):
        candidates.append(data)
        for key in ("message", "request", "payload"):
            v = data.get(key)
            if isinstance(v, dict):
                candidates.append(v)
    user_text = None
    for c in candidates:
        if c.get("type") in ("user", "user_message", "user_input"):
            content = c.get("content") or c.get("text") or c.get("input")
            if isinstance(content, str):
                user_text = content
            elif isinstance(content, list):
                for item in content:
                    if isinstance(item, dict) and item.get("type") == "text":
                        user_text = item.get("text")
                        break
    if user_text is None:
        return
    reply = {
        "type": "assistant_message",
        "session_id": sid,
        "content": [{"type": "text", "text": f"echo: {user_text}"}],
    }
    out = json.dumps(reply)
    for client in list(ws_clients[sid]):
        if not client.closed:
            await client.send_str(out)
            log(f"WS->{sid} text={out}")


def make_app() -> web.Application:
    app = web.Application()
    app.router.add_post("/v1/code/sessions", create_session)
    app.router.add_post("/v1/code/sessions/{sid}/events", post_event)
    app.router.add_get("/v1/code/sessions/{sid}/events", get_events)
    app.router.add_get("/v1/code/sessions/{sid}/stream", stream_ws)
    return app


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--host", default=HOST)
    parser.add_argument("--port", type=int, default=PORT)
    args = parser.parse_args()
    log(f"mock_gateway listening on http://{args.host}:{args.port}")
    web.run_app(make_app(), host=args.host, port=args.port, print=lambda *_: None)


if __name__ == "__main__":
    main()
```

- [ ] **Step 2: Smoke-test the gateway**

In one shell:
```bash
python3 /tmp/cc-cloud-poc/mock_gateway.py
```

In another shell:
```bash
curl -sS -X POST http://127.0.0.1:8181/v1/code/sessions -d '{}' -H 'content-type: application/json'
```

Expected: gateway prints `POST /v1/code/sessions body={}` and `-> 200 {...}`. Curl returns JSON containing `id` and `websocket_url`.

Stop the gateway (Ctrl-C) after confirming.

- [ ] **Step 3: No commit** — out-of-tree.

---

## Task 3: `patch_cli.py` skeleton + binary audit for Patch 1

**Files:**
- Create: `/tmp/cc-cloud-poc/patch_cli.py`

The script holds a global `PATCHES` list. Each entry is `(name, find_bytes, replace_bytes)`. Adding a patch = appending an entry. Same-length is asserted; not-found-exactly-once is asserted.

- [ ] **Step 1: Write the skeleton**

Create `/tmp/cc-cloud-poc/patch_cli.py`:

```python
#!/usr/bin/env python3
"""Same-length byte-substitution patcher for Claude Code v2.1.167.

Reads SRC, applies each patch in PATCHES, writes DST. Each patch must
- have len(find) == len(replace), and
- find the anchor exactly once in the binary.
Any violation aborts (exit 1).
"""
import argparse
import os
import sys
from pathlib import Path

SRC = Path("/tmp/cc-cloud-poc/claude-167-original")
DST = Path("/tmp/cc-cloud-poc/claude-167-patched")

# Each entry pinned by Task 3+. Add iteratively. NEVER reorder; offsets in
# logs / iteration journals refer to the indices here.
PATCHES: list[tuple[str, bytes, bytes]] = [
    # ("P1_url_allowlist", b"<find>", b"<replace>"),
    # ("P2_rz6", b"function rz6(){return!1}", b"function rz6(){return!0}"),
    # ("P3_api_key_rejection", b"<find>", b"<replace>"),
]


def audit_only(data: bytes) -> int:
    rc = 0
    for name, find, _ in PATCHES:
        n = data.count(find)
        print(f"  [{name}] len={len(find)} occurrences={n}")
        print(f"    find: {find!r}")
        if n != 1:
            rc = 1
    return rc


def apply_patches(data: bytearray) -> None:
    for name, find, replace in PATCHES:
        if len(find) != len(replace):
            raise SystemExit(f"[{name}] length mismatch find={len(find)} replace={len(replace)}")
        n = data.count(find)
        if n != 1:
            raise SystemExit(f"[{name}] anchor occurrences={n} (need exactly 1)")
        idx = data.find(find)
        data[idx : idx + len(find)] = replace
        print(f"[{name}] patched at offset 0x{idx:x}")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--dry-run", action="store_true", help="audit anchors only, do not write DST")
    parser.add_argument("--src", default=str(SRC))
    parser.add_argument("--dst", default=str(DST))
    args = parser.parse_args()

    src = Path(args.src)
    if not src.exists():
        raise SystemExit(f"src missing: {src}")
    raw = src.read_bytes()

    if args.dry_run:
        print(f"Audit of {src} ({len(raw)} bytes), {len(PATCHES)} patches:")
        rc = audit_only(raw)
        sys.exit(rc)

    data = bytearray(raw)
    apply_patches(data)
    dst = Path(args.dst)
    dst.write_bytes(bytes(data))
    os.chmod(dst, 0o755)
    print(f"wrote {dst} ({len(data)} bytes)")


if __name__ == "__main__":
    main()
```

- [ ] **Step 2: Audit current `Fk$` URL allowlist for Patch 1 anchor**

Run:
```bash
strings -n 6 /tmp/cc-cloud-poc/claude-167-original | \
  grep -aEo 'https://[a-zA-Z0-9.\-]*anthropic[a-zA-Z0-9.\-/]*' | sort -u
```

Expected: a small list of Anthropic URLs (the spec recorded ~20 hits including `https://api.anthropic.com`, `https://api-staging.anthropic.com`, `https://mcp-proxy.anthropic.com`, etc.).

Pick an entry that:
1. Is plausibly an OAuth-base-URL allowlist member (`api.anthropic.com`, `api-staging.anthropic.com`, or similar).
2. Has a length we can match with a `http://127.0.0.1:8181/...` URL (≥ 23 bytes; pad the path with hex chars to reach the exact length).

Record the chosen entry in `/tmp/cc-cloud-poc/iteration-01.md`:
```markdown
# Iteration 01 — Patch 1 anchor chosen

- find: <exact-anthropic-url>
- find length: <N> bytes
- replace: http://127.0.0.1:8181/<pad-to-N-bytes>
- replace length: <N> bytes (asserted equal)
```

- [ ] **Step 3: Verify the anchor is unique in the binary**

Run:
```bash
python3 -c "
import sys
data = open('/tmp/cc-cloud-poc/claude-167-original','rb').read()
target = b'<paste exact find bytes here>'
print('len=', len(target), 'count=', data.count(target))
"
```

Expected: `count= 1`. If not, pick a different allowlist entry (Step 2) and retry.

- [ ] **Step 4: Add Patch 1 to `PATCHES` in `patch_cli.py`**

Edit `/tmp/cc-cloud-poc/patch_cli.py`, uncomment and fill in the `P1_url_allowlist` line with the exact bytes from Step 2.

- [ ] **Step 5: Dry-run the patcher**

Run:
```bash
python3 /tmp/cc-cloud-poc/patch_cli.py --dry-run
```

Expected: `[P1_url_allowlist] len=<N> occurrences=1`. Exit code 0.

- [ ] **Step 6: Apply patches**

Run:
```bash
python3 /tmp/cc-cloud-poc/patch_cli.py
ls -la /tmp/cc-cloud-poc/claude-167-patched
```

Expected: `wrote /tmp/cc-cloud-poc/claude-167-patched (<bytes>)`. File exists, executable, same size as the source.

- [ ] **Step 7: No commit** — out-of-tree.

---

## Task 4: `run_poc.sh` script

**Files:**
- Create: `/tmp/cc-cloud-poc/run_poc.sh`

- [ ] **Step 1: Write the script**

Create `/tmp/cc-cloud-poc/run_poc.sh`:

```bash
#!/usr/bin/env bash
# Launch the patched binary under a PTY and capture output to tui.log.
#
# The mock_gateway MUST already be running (in another shell or
# backgrounded) before this script is invoked.
set -u
cd /tmp/cc-cloud-poc

# The exact BASE URL that Patch 1 wrote into the binary. Keep in sync
# with patch_cli.py's P1_url_allowlist replacement and mock_gateway.BASE_URL.
: "${POC_BASE_URL:=http://127.0.0.1:8181/cccloud11ab}"

CLAUDE_CODE_OAUTH_TOKEN=fake-poc-token \
CLAUDE_CODE_ORGANIZATION_UUID=00000000-0000-0000-0000-000000000001 \
CLAUDE_CODE_ACCOUNT_UUID=00000000-0000-0000-0000-000000000002 \
CLAUDE_CODE_USER_EMAIL=poc@example.invalid \
CLAUDE_CODE_CUSTOM_OAUTH_URL="${POC_BASE_URL}" \
CLAUDE_CODE_REMOTE=1 \
DISABLE_INSTALLATION_CHECKS=1 \
CLAUDE_CODE_SANDBOXED=1 \
  script -qc "/tmp/cc-cloud-poc/claude-167-patched --cloud 'hello'" \
  /tmp/cc-cloud-poc/tui.log
```

Make it executable:
```bash
chmod +x /tmp/cc-cloud-poc/run_poc.sh
```

- [ ] **Step 2: No commit** — out-of-tree.

---

## Task 5: Iteration loop (the heart of the PoC)

This task is **repeated** for each patch step. Maximum 5 iterations
total (per spec §8). Each iteration produces a journal entry
`/tmp/cc-cloud-poc/iteration-NN.md`.

After completing one iteration, decide:
- Round-trip working? → go to Task 6.
- Stuck per spec §8? → go to Task 7.
- Otherwise → start the next iteration (still Task 5).

- [ ] **Step 1: Start the mock gateway in background**

```bash
pkill -f mock_gateway.py 2>/dev/null
( python3 /tmp/cc-cloud-poc/mock_gateway.py \
    > /tmp/cc-cloud-poc/gateway.log 2>&1 & )
sleep 1
tail -1 /tmp/cc-cloud-poc/gateway.log
```

Expected: `mock_gateway listening on http://127.0.0.1:8181`.

- [ ] **Step 2: Run the PoC**

```bash
rm -f /tmp/cc-cloud-poc/tui.log
timeout 30 /tmp/cc-cloud-poc/run_poc.sh; echo "exit=$?"
```

Expected: TUI starts inside `script(1)` PTY, runs for up to 30s. Exit code: 124 = timeout (expected if round-trip is working and TUI stays open), other codes = early exit (read the error).

- [ ] **Step 3: Inspect both logs**

```bash
echo "===== TUI log (last 80 lines, ANSI stripped) ====="
sed -E 's/\x1B\[[0-9;]*[A-Za-z]//g; s/\r//g' /tmp/cc-cloud-poc/tui.log | tail -80
echo
echo "===== Gateway log ====="
cat /tmp/cc-cloud-poc/gateway.log
```

Identify ONE of:
- **(a) Round-trip success:** TUI rendered `echo: hello` AND gateway log shows both an inbound `hello` frame and the outbound `assistant_message` frame. → mark this task done, go to Task 6.
- **(b) Error pointing at the next patch:** e.g. "API key authentication is not sufficient", `tengu_ccr_bridge`, `needsLogin`, an HTTP 401, or a URL that's still pointing at anthropic.com. → keep going.
- **(c) Stuck point per spec §8:** integrity-check failure, > 1h on this iteration, ≥ 5 iterations in `PATCHES`, or unsigned-frame rejection. → go to Task 7.

- [ ] **Step 4: Pin the next patch anchor**

For case (b) above: locate the failing predicate or sentinel string in the binary. Tools:

```bash
# locate the sentinel string the TUI printed
strings -n 8 /tmp/cc-cloud-poc/claude-167-original \
  | grep -aEo '.{60}API key authentication is not sufficient.{200}' | sort -u | head -3
```

…and similar for whatever the actual error was. Find the same-length predicate (typically `if(<expr>)` → `if(false)` flips a few common shapes:
- `if(A&&B)` → `if(0&&B)` (same length)
- `if(!_)` → `if(!1)` (same length)
- `if(X)return!1` → `if(0)return!1` (same length)
- `function rz6(){return!1}` → `function rz6(){return!0}` (single-byte literal flip)

Verify uniqueness:
```bash
python3 -c "
data = open('/tmp/cc-cloud-poc/claude-167-original','rb').read()
find = b'<paste>'
print('len=', len(find), 'count=', data.count(find))
"
```

If unique and same-length replacement exists, write iteration journal:

`/tmp/cc-cloud-poc/iteration-NN.md`:
```markdown
# Iteration NN — Patch <name>

- Triggering error in tui.log: "<excerpt>"
- find: <bytes literal>
- replace: <bytes literal>
- find length == replace length: yes
- count in binary: 1
```

- [ ] **Step 5: Add the patch to `patch_cli.py` and re-apply**

Edit `/tmp/cc-cloud-poc/patch_cli.py`, append the new entry to `PATCHES`.

```bash
python3 /tmp/cc-cloud-poc/patch_cli.py --dry-run   # all patches occurrences=1
python3 /tmp/cc-cloud-poc/patch_cli.py             # re-write claude-167-patched
```

Expected: every patch reports `occurrences=1`; rewrite succeeds. If any patch now reports `occurrences=0` or `>1` due to a prior patch having shifted/destroyed it, reorder `PATCHES` (later patches must not touch earlier-patched bytes) or pick a new anchor.

- [ ] **Step 6: Loop back to Step 1 of this task**

Each loop is a new iteration NN. Strict limits:
- Wall clock for ONE iteration: 1 hour. Past that, treat as stuck (Task 7).
- Cumulative patch count: 5 (initial patch set + 2 more = 3 by Task 5's first run; absolute cap 5 patches in `PATCHES`). Past that, stuck.

- [ ] **Step 7: No commit** — all artifacts out-of-tree.

---

## Task 6: Round-trip success — record outcome

Reached only if Task 5 produced a passing run.

- [ ] **Step 1: Capture the proof**

Save the two log excerpts that prove the round-trip:

```bash
mkdir -p /tmp/cc-cloud-poc/proof
sed -E 's/\x1B\[[0-9;]*[A-Za-z]//g; s/\r//g' /tmp/cc-cloud-poc/tui.log \
    | grep -nE 'echo: hello|Session \(remote\)' > /tmp/cc-cloud-poc/proof/tui-grep.txt
grep -nE 'POST /v1/code/sessions|WS<-|WS->' /tmp/cc-cloud-poc/gateway.log \
    > /tmp/cc-cloud-poc/proof/gateway-grep.txt
echo "===== TUI proof ====="; cat /tmp/cc-cloud-poc/proof/tui-grep.txt
echo "===== Gateway proof ====="; cat /tmp/cc-cloud-poc/proof/gateway-grep.txt
```

Both files should be non-empty. The TUI grep should contain `echo: hello`; the gateway grep should contain a `POST /v1/code/sessions`, at least one inbound `WS<-` frame carrying `hello`, and the corresponding outbound `WS->` `assistant_message`.

- [ ] **Step 2: Append outcome to the spec**

Edit `docs/superpowers/specs/2026-06-06-cc-cloud-thin-client-poc-design.md`,
replace section 11 with:

```markdown
## 11. Outcome — PASS

Round-trip achieved on <YYYY-MM-DD> with N same-length patches.

### Final patch set

| # | Name | find length | offset | one-line description |
|---|------|-------------|--------|----------------------|
| 1 | P1_url_allowlist | <N> | 0x<hex> | Redirect Fk$ entry to mock gateway |
| 2 | P2_rz6 | 24 | 0x<hex> | Force bridge entitlement true |
| 3 | <…>   | …   | …       | … |

### Proof

TUI log grep:
\`\`\`
<paste contents of /tmp/cc-cloud-poc/proof/tui-grep.txt>
\`\`\`

Gateway log grep:
\`\`\`
<paste contents of /tmp/cc-cloud-poc/proof/gateway-grep.txt>
\`\`\`

### Implications

The Claude Code v2.1.167 public binary CAN be redirected to a non-Anthropic
gateway in `--cloud` thin-client mode. The 2026-05-05 "no thin-client TUI
in the public binary" finding is superseded. See updated memory:
`agent_integration_strategy.md`, `cc_binary_internals.md`.

Decision to build cc-app v2 is deferred to a separate spec.
```

Fill in all `<…>` placeholders from the actual run.

- [ ] **Step 3: Update memory files (see Task 8) then commit (see Task 9)**

---

## Task 7: Stuck-point report (only if Task 5 hit the §8 limits)

Reached only if iteration aborted per spec §8.

- [ ] **Step 1: Record the stuck point**

Edit `docs/superpowers/specs/2026-06-06-cc-cloud-thin-client-poc-design.md`,
replace section 11 with:

```markdown
## 11. Outcome — STUCK

Aborted on <YYYY-MM-DD> after N iterations because <which §8 condition>.

### Last successful step

<plain English: e.g. "Patches 1 and 2 applied successfully; TUI got past URL
allowlist and bridge entitlement and POSTed POST /v1/code/sessions">

### Stopping error

TUI log excerpt:
\`\`\`
<paste 5–20 lines of tui.log around the failure>
\`\`\`

Gateway log excerpt:
\`\`\`
<paste 5–20 lines of gateway.log around the failure>
\`\`\`

### Offending binary substring

- string: \`<bytes>\`
- offset: 0x<hex>
- occurrences in binary: <N>
- why same-length patching can't proceed: <one paragraph>

### Honest next-step assessment

<one paragraph: what it would take to break through (e.g. "Bun bundle
length-change rewrite", "supply a real claude.ai OAuth token", "give up — Anthropic
added <X>"), and a rough cost estimate.>
```

Fill in all `<…>` from the actual iteration logs.

- [ ] **Step 2: Update memory files (Task 8) then commit (Task 9)**

---

## Task 8: Update memory files

Whether outcome is PASS or STUCK, the previous "no thin-client TUI" finding in `agent_integration_strategy.md` is invalidated and binary internals memory needs the v2.1.167 picture.

- [ ] **Step 1: Update `agent_integration_strategy.md`**

Read the current file:
```bash
cat /root/.claude/projects/-root-agentserver/memory/agent_integration_strategy.md
```

Edit it to add (at the top of the body, after the existing decision block):

```markdown
**Update 2026-06-07 (v2.1.167 PoC):** The 2026-05-05 "no thin-client TUI
in public binary" finding is invalidated. v2.1.167 ships a real
`--cloud` thin-client mode (`isThinClient`, `bridge:repl` carrying user
input and assistant frames, `teleportToRemote` / `pollRemoteSessionEvents`
submit-and-subscribe against /v1/code/sessions). PoC outcome: **<PASS|STUCK>**
— see `docs/superpowers/specs/2026-06-06-cc-cloud-thin-client-poc-design.md`
section 11.

Strategic implication: <one-paragraph translation of the PoC outcome
into "does this change the codex-primary stance?". Default if PASS:
"Path is now viable in principle but still requires patch maintenance
per release; codex-primary stance unchanged absent a strong product
reason to add a Claude TUI option." Default if STUCK: "Path remains
closed; codex-primary stance unchanged.">
```

- [ ] **Step 2: Update `cc_binary_internals.md`**

Read current state, then append a new section under "v2.1.145 re-verification":

```markdown
**v2.1.167 re-verification (2026-06-07):** Binary form changed from
Node `cli.js` to a single-file Bun-compiled ELF (~245 MB,
`not stripped`). New top-level user-facing thin-client entrypoint:
`--cloud` flag (requires TTY; rejects pure API key absent
Claude.ai-account context). New symbols: `isThinClient` (6 hits, drives
TUI "Session (remote)" label), `routeThinClientCommand`,
`isThinClientSafe`, `isBridgeDispatchable`, `teleportToRemote`,
`pollRemoteSessionEvents`, `awaitRemoteSessionResult`,
`archiveRemoteSession`. `bridge:repl` log family confirms bi-directional
forwarding ("Injecting inbound user message",
"Ignoring non-user inbound message", "writeSdkMessages",
"sendControlRequest", "writeBatch"). `claude assistant` subcommand
STILL stripped (zero `.command("assistant"` hits) — KAIROS entrypoint
remains dead; the `--cloud` flag is the new path.

CLAUDE_CODE_REMOTE_* env vars expanded to 9 entries (added
`_ENVIRONMENT_TYPE`, `_HERMETIC_MODE`, `_RAW_EVENTS_FILE`,
`_SEND_KEEPALIVES`, `_SETTINGS_PATH`, `_SETTINGS_POLL_MS`).

PoC patch set (2026-06-07): <PASS — N patches: list each name +
purpose + offset | STUCK at patch K — see spec section 11>.
```

- [ ] **Step 3: Update `MEMORY.md` index**

Read the current index:
```bash
cat /root/.claude/projects/-root-agentserver/memory/MEMORY.md
```

If the entries for `agent_integration_strategy.md` and
`cc_binary_internals.md` already exist (they should — they're in the
session memory), edit their one-line hooks to reflect the 2026-06-07
update. Keep the format `- [Title](file.md) — hook` exactly.

Example replacement hooks:
- `agent integration strategy` — `…codex primary, Claude Code thin-client v2.1.167 PoC <PASS|STUCK> 2026-06-07`
- `Claude Code v2.1.128 binary internals` — `…retitled "v2.1.128/167 binary internals"; v2.1.167 adds --cloud / isThinClient / bridge:repl`

- [ ] **Step 4: No commit yet** — Task 9 batches all in-tree changes.

---

## Task 9: Commit + push + open PR

The branch already has the spec commit. This task adds the outcome,
plan checkboxes, and memory updates as a second commit, then pushes.

- [ ] **Step 1: Confirm branch**

```bash
git rev-parse --abbrev-ref HEAD
```

Expected: `spec/cc-cloud-thin-client-poc`. If not, `git checkout spec/cc-cloud-thin-client-poc`.

- [ ] **Step 2: Diff sanity check**

```bash
git status -sb
git diff --stat
```

Expected modifications:
- `docs/superpowers/specs/2026-06-06-cc-cloud-thin-client-poc-design.md` (section 11 filled)
- `docs/superpowers/plans/2026-06-07-cc-cloud-thin-client-poc.md` (checkboxes ticked)
- `/root/.claude/projects/-root-agentserver/memory/agent_integration_strategy.md`
- `/root/.claude/projects/-root-agentserver/memory/cc_binary_internals.md`
- `/root/.claude/projects/-root-agentserver/memory/MEMORY.md`

Confirm NOTHING else is staged (no `/tmp/cc-cloud-poc/` artifact, no
unrelated file).

- [ ] **Step 3: Stage in-repo files**

```bash
git add docs/superpowers/specs/2026-06-06-cc-cloud-thin-client-poc-design.md \
        docs/superpowers/plans/2026-06-07-cc-cloud-thin-client-poc.md
```

(Memory files live under `/root/.claude/…` outside the repo — they are
already updated on disk and persist across sessions; they are NOT
staged into the repo. No `git add` for them.)

- [ ] **Step 4: Commit**

For PASS outcome:
```bash
git commit -m "docs(superpowers): cc-cloud thin-client PoC — PASS

v2.1.167 --cloud thin-client mode redirected to local mock gateway
with N same-length patches; round-trip (user input → bridge:repl WS →
mock-pushed assistant frame → TUI render) verified. See spec section 11
for patch set and proof excerpts.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

For STUCK outcome:
```bash
git commit -m "docs(superpowers): cc-cloud thin-client PoC — STUCK

Iterated N patches against v2.1.167 --cloud; aborted per spec section 8
because <reason>. See spec section 11 for last successful step, stopping
error, and next-step assessment.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

- [ ] **Step 5: Push branch**

```bash
git push -u github spec/cc-cloud-thin-client-poc
```

Expected: branch created on remote.

- [ ] **Step 6: Open PR**

```bash
gh pr create --base main --head spec/cc-cloud-thin-client-poc \
  --title "docs(superpowers): cc-cloud thin-client PoC (v2.1.167)" \
  --body "$(cat <<'EOF'
Re-verifies the 2026-05-05 finding that Claude Code's public binary
has no thin-client TUI mode. v2.1.167 ships a real `--cloud` thin
client; this PR carries:

- Design spec for a 1-2 day PoC verifying whether the new thin-client
  mode can be redirected to a non-Anthropic gateway via same-length
  binary patches.
- Implementation plan (this PR also includes the executed checkboxes).
- PoC outcome (section 11 of the spec): **<PASS|STUCK>**.

PoC artifacts (`patch_cli.py`, `mock_gateway.py`, `run_poc.sh`,
`claude-167-patched`, logs) live under `/tmp/cc-cloud-poc/` and are
deliberately NOT committed — the PoC's value is the conclusion, not
re-runnable code. If the PoC PASSed and we later decide to build
cc-app v2, those artifacts will be re-authored as a proper in-tree
component under a new spec.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

Expected: PR URL printed.

- [ ] **Step 7: Done.**

---

## Self-Review

Walked through this plan against the spec:

**Spec coverage check:**
- §1 Problem statement → context only, no task needed.
- §2 Goal (round-trip) → Task 5 + Task 6 step 1.
- §3 Scope → Tasks 1-7 stay inside it; Task 9 step 2 enforces "no other staged files".
- §4 Approach B (iterate patch/run/observe) → Task 5 is exactly this loop.
- §5.1 patch_cli.py → Task 3.
- §5.2 mock_gateway.py → Task 2.
- §5.3 run_poc.sh → Task 4.
- §6 Patch set → seeded as commented-out entries in Task 3; iteratively added in Task 5.
- §7 Data flow → Task 5 steps 1-3 exercise it; Task 6 step 1 captures proof.
- §8 Stuck-point policy → Task 5 step 6 enforces wall-clock + count limits; Task 7 records.
- §9 Pass/fail criteria → Task 6 (pass), Task 7 (stuck).
- §10 Repo footprint → Task 9 step 2 sanity check + Task 8 separation of in-repo vs out-of-repo.
- §11 Outcome → Task 6 step 2 / Task 7 step 1.

**Placeholder scan:** all `<…>` markers are explicitly labeled as "fill in from the actual run" — these are runtime values, not plan-time placeholders. No "TBD" / "implement later" / "add appropriate handling" remain.

**Type / symbol consistency:** `PATCHES` list (Task 3 step 1) referenced consistently in Task 5 step 5. `BASE_URL` in `mock_gateway.py` (Task 2 step 1) matches `POC_BASE_URL` default in `run_poc.sh` (Task 4 step 1) — both `http://127.0.0.1:8181/cccloud11ab`. Spec section 6's "URL prefix is exactly the patch's replace string" constraint is honored in Task 3 step 2 (pick the replace string, then mirror it into both files).
