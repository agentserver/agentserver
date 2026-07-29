# Codex conformance lab

This directory establishes executable facts about the stock Codex binary used
by agentserver v2. It is intentionally independent of the v1 runtime.

Ordinary `go test` runs the dialect, framing, subprocess, and fixture tests. Live
tests are opt-in and never discover Codex from `PATH`:

```sh
make -C v2 conformance-live AGENTSERVER_CODEX_BIN=/absolute/path/to/codex
```

A full live run fails closed when the binary release has no explicit A03
characterization; version-specific negative probes skip only when another
known characterized release is under test.

The current live probes cover A01 through a bounded loopback scripted Responses
server (`initialize → initialized → thread/start → turn/start → item/completed →
turn/completed`), A02 experimental gating for `environments: []` in both
directions, A03 tool-surface and dispatch characterization through a bounded
sessionless Streamable HTTP MCP server, the A04 managed-requirements injection
boundary, A05 no-double-approval behavior and its prompt-mode positive control,
A06 client-controlled MCP form elicitation, its paused-tool-timeout behavior,
and its never-policy negative control, A07 pending-elicitation interrupt cleanup,
A08 graceful shutdown and stable state snapshots, A09 rollout-only completed-turn
checkpoint recovery, A10 mid-turn hard-crash rollback to the last sealed
checkpoint, E01 stdio/EOF, exec-server environment metadata, and these slices of
the executor matrix:

- E02: deterministic argv/arg0, canonical cwd, exact non-inherited child
  environment, non-TTY and PTY streams, output/exit/close sequencing, and
  retained `process/read` replay;
- E03: piped stdin, idempotent `writeId`, `unknownProcess`, `stdinClosed`,
  interrupt delivery, and terminate behavior. A negative probe proves that
  `process/signal` returns the same empty success object for missing, delivered,
  and already-exited targets, so E03 is not accepted;
- E04: `fs/readFile`, `fs/open`, `fs/readBlock`, `fs/close`, file-URI rejection,
  and `fs/canonicalize`;
- E06: stdio EOF shuts down exec-server and kills its managed child;
- E07: a negative root-crash probe proves that a descendant can survive after
  `process/exited`, while `process/terminate` returns `running: false`; only
  whole-connection shutdown then reaps the process group, so E07 is not accepted;
- E08: a tainted server environment and poison user-home config are excluded by
  the isolated `CODEX_HOME`, and the child still receives exactly the requested
  environment;
- E10: a bounded slice proves that replay retains only the final approximately
  1 MiB suffix of a larger streamed output. Frame, argv/env, write-id, and exited
  process retention bounds remain open.

The process probe deliberately accepts a `process/start` response arriving
after early output notifications and uses the one-based event sequence as the
ordering authority. It also fixes a subtle cursor distinction in the observed
stock protocol: a `process/read` with `maxBytes` advances `nextSeq` only beyond
its last returned output chunk, while a terminal read without `maxBytes` can
advance it beyond `exited` and `closed`.

On the observed 0.145.0 candidate, assistant content is authoritative on
`item/completed`; the terminal `turn/completed` has `itemsView: notLoaded` and
an empty `items` array. Consumers must reduce the item stream instead of
treating the terminal notification as a content snapshot. A01 records the
actual model request tool surface but intentionally does not declare A03
complete. A pinned local model catalog plus explicit feature, orchestrator,
skills, Web, user-input, goals, and multi-agent disables reduce the observed
0.145.0 surface to exactly `update_plan`. The A03 negative probe then scripts an
`update_plan` call and observes both a successful `Plan updated` result and
`turn/plan/updated`, proving the remaining tool is executable. Stock 0.145.0 is
therefore an A03 rejection, not a runtime pin.

Official tag `rust-v0.146.0-alpha.14` (commit
`9d84cad281364eb7f6be75e23067b0adc5e26106`) adds a real
`[tools.update_plan] enabled = false` switch. The no-MCP probe confirms that the
switch reduces the captured model tool surface to empty. Its A01 terminal
projection also changed to `itemsView: summary` with the completed agent item
present; this is locked separately from the 0.145.0 `notLoaded` shape and does
not change the requirement to reduce the streamed item events. The switch fixes
the 0.145.0 A03 blocker, but not A03 as a whole. With one MCP server whose `tools/list`
advertises both `approved_echo` and `blocked_echo`, and Codex configured with
`enabled_tools = ["approved_echo"]`, the captured surface is exactly:

- `mcp__executor.approved_echo`;
- `list_mcp_resources`;
- `list_mcp_resource_templates`;
- `read_mcp_resource`.

The approved tool reaches `tools/call`; the blocked MCP tool and an unregistered
`exec_command` call both return `unsupported call` before reaching MCP. However,
a scripted `list_mcp_resources` call is executable and sends `resources/list`
to the MCP server despite the per-server `enabled_tools` allowlist. The alpha is
therefore also an A03 rejection and, independently, is not a stable production
release. Prompt instructions, event filtering, or making the executor return an
error for `resources/list` do not satisfy the agreed exact model-tool-surface
invariant.

Official stable tag `rust-v0.146.0` (annotated tag object
`be449751a978f02e5bbba886999662956c7f38f5`, peeled commit
`e363b08c9175ac1cbe5893615dd2cb9ddf95043b`) was then published as npm `latest`.
Release-bound live probes show the same A01 `summary` projection and the same
A03 surface and dispatch behavior as alpha.14: the three generic MCP resource
handlers remain visible, and `list_mcp_resources` still reaches
`resources/list` outside `enabled_tools`. The tested macOS arm64 binary SHA-256
is `ae1d3ffe6d48aec6a4dc3f50e7eb8e0d11962485a6a9406c5a7012139383da02`;
the official platform package SHA-256 is
`279ec3460c5b8068daab2a4f5bcf057483303b3595f4a24ade6ceb4d02674935`.
Stable 0.146.0 is therefore also a characterized rejection, not a production
runtime pin.

A04 is still open as an image-level gate. The upstream managed allowlist
disables a configured MCP server unless both its name and transport identity
match, but official release binaries read the Unix system layer only from
`/etc/codex/requirements.toml`. The upstream
`CODEX_APP_SERVER_MANAGED_CONFIG_PATH` redirect is compiled only with Rust
`debug_assertions`; a release-bound negative probe confirms that the official
0.146.0 artifact ignores it. Consequently, a trustworthy positive A04 probe
must run the stock artifact in a disposable image or mount namespace with the
exact-string HTTPS requirement installed before process start. It must observe
that only the approved endpoint bootstraps and that wrong-name, wrong-URL,
user, and project additions stay disabled. Pointing the release binary at a
temporary file through that debug environment variable is not a valid shortcut.

A05 passes for the characterized 0.146.0 releases. The live probe advertises an
explicitly destructive, open-world executor tool, starts a thread with granular
approval that allows only MCP elicitations, and verifies that
`default_tools_approval_mode = "approve"` reaches `tools/call` without an
app-server reverse request. Its positive control changes only the server default
to `prompt`, captures `mcpServer/elicitation/request` with
`codex_approval_kind = "mcp_tool_call"`, cancels it, and proves that
`tools/call` is not sent. This establishes the Codex side of the single-approval
design. The separate A06 probe below establishes the reverse direction rather
than treating A05 as evidence for both flows.

A06 passes for the characterized 0.146.0 releases. During a real model-driven
`tools/call`, the fake MCP server holds the Streamable HTTP response open,
emits a standard `elicitation/create` request over SSE, receives Codex's
separate JSON-RPC response POST, and only then completes the original tool
call. Under the granular policy, app-server forwards the typed form schema,
execution metadata, thread, turn, and server identity to its client. Separate
live cases return `accept`, `decline`, and `cancel`; each action reaches MCP,
`serverRequest/resolved` precedes turn completion, and the resulting tool
output reaches the next model request. With the same non-empty form under
`approval_policy = "never"`, Codex emits no reverse request and returns
`decline` directly to MCP. A separate timed case configures
`tool_timeout_sec = 0.5`, leaves the form unanswered for 1.5 seconds, and sees
no resolution, terminal turn, or second model request until the client sends
`cancel`. The MCP timeout is therefore paused during elicitation and cannot be
used as the product approval TTL or its cleanup mechanism.

A07 also passes for both characterized 0.146.0 releases, with one important
ordering fact. When the client leaves the A06 form request unanswered and sends
`turn/interrupt`, app-server returns success, terminates the turn as
`interrupted`, resolves the outstanding reverse request, and sends `cancel` to
MCP. It performs no second model request or MCP tool call. However, the observed
wire order is `turn/completed` first and `serverRequest/resolved` second. A
harness must therefore keep draining stdio and track outstanding reverse
request IDs during finalization; a terminal turn notification alone is not a
cleanup barrier.

A08 passes for both characterized 0.146.0 releases. After a completed,
non-ephemeral turn with no outstanding reverse request, the probe closes stdin
immediately and waits for a bounded clean exit without sleeping. Two bounded
post-exit snapshots agree on every relative path, mode, size, and SHA-256; the
reported rollout is complete JSONL containing the thread and turn content, and
`state_5.sqlite` has a SQLite header. Clean exit still leaves stable
`.sqlite-wal` and `.sqlite-shm` files for state, goals, logs, and memories.
Therefore process exit is a byte-stability barrier, not evidence that WAL data
was merged into the main databases. A08 does not decide which stable files form
a checkpoint.

A09 passes for both `0.146.0-alpha.14` and stable `0.146.0`. For these builds,
the pinned checkpoint allowlist is exactly one app-server-reported rollout JSONL
per brain thread. The probe copies only that file, under its manifest-relative
path, into a fresh `CODEX_HOME`, verifies the staging tree contains no other
file, and then renames the source home so its old absolute paths cannot be used.
It regenerates config rather than restoring it, starts the same stock build, and
cold-resumes with the relocated rollout path and `excludeTurns: true`. A cold
`thread/resume` emits no `thread/started` notification; its successful RPC
response is the resume barrier, after which the next `turn/start` supplies
`environments: []`.

The restored model request contains both user turns, the original MCP call ID,
and its exact tool result, while the MCP side effect is not executed again.
`state_5.sqlite`, every SQLite WAL/SHM sidecar, and the goals/logs/memories
databases are therefore runtime-derived state, not checkpoint payload. Config,
requirements, credentials, logs, caches, and transport state are likewise
excluded and must be recreated for each attempt. As a negative control, a
missing rollout makes `thread/resume` fail with `-32600` before any model request
or MCP initialization. Every future Codex build must repeat this native
round-trip before receiving the same allowlist.

A10 passes for both characterized 0.146.0 releases. The probe first seals a
completed-turn rollout in a separate rollout-only checkpoint, then restores a
second app-server process from it. A hold-open scripted response provides a
deterministic crash barrier: the second `turn/start` has been accepted and its
model request contains both the sealed history and the new user input, but the
model has sent no response. The probe hard-kills app-server at that point. The
process exits unsuccessfully, does not retry the model call, and cannot mutate
the separately sealed checkpoint.

The crashed runtime is discarded rather than offered to native resume. A third
fresh `CODEX_HOME` is rebuilt from the sealed rollout and receives an explicit
new `turn/start`. Its turn ID differs from the abandoned turn, and the captured
model context contains the completed history plus the new continuation input but
not the abandoned input. This proves the safe recovery path and the need for an
externally committed checkpoint pointer. It does not claim stock Codex will
reject an uncommitted crash-runtime rollout if a caller incorrectly supplies
one; harness/core fencing must make that file ineligible in the first place.

The probes also report candidate binary and canonical app-server schema
fingerprints. Stock 0.145.0 was observed to randomize object-key order in one
generated schema, so the probe requires two consecutive generations to match
under the versioned canonical JSON tree algorithm; it never promotes a raw-tree
hash. These are bootstrap facts, not a claim that the Phase 0 A01-A12/E01-E10
gate is complete. A production runtime manifest and release-bound golden
fixtures will be added only after a stock release passes the full matrix.

The checked-in `fixtures/appserver`, `fixtures/dialect`, and
`fixtures/execserver` messages are
synthetic codec/shape fixtures. They lock the Codex JSON-RPC envelope (including
omission of `jsonrpc`) and the process/filesystem field names without pretending
to pin a Codex release. Release-bound golden traces remain a Phase 0 exit item.
