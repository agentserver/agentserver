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
checkpoint, A11 runtime-secret exclusion and capability rotation, A12 host
launch and stock-network negative characterization, E01 stdio/EOF, exec-server
environment metadata, and these slices of the executor matrix:

- E02: deterministic argv/arg0, canonical cwd, exact non-inherited child
  environment, non-TTY and PTY streams, output/exit/close sequencing, and
  retained `process/read` replay;
- E03: piped stdin, idempotent `writeId`, `unknownProcess`, `stdinClosed`,
  interrupt delivery, and terminate behavior. A negative probe proves that
  `process/signal` returns the same empty success object for missing, delivered,
  and already-exited targets, so E03 is not accepted;
- E04: `fs/readFile`, `fs/open`, `fs/readBlock`, `fs/close`, file-URI rejection,
  and `fs/canonicalize`;
- E05: a deterministic child connects to the executor-local HTTP proxy injected
  by `process/start.networkProxy` and makes a real request to a bounded loopback
  origin. The emitted reverse request has the observed
  `{processId, request: {protocol, host, port}}` shape. `allow` reaches the
  origin exactly once; `deny`, `ask`, an RPC error, an unknown decision variant,
  callback timeout, and stdio connection EOF all reach it zero times. The
  reference client rejects unknown reverse methods with `-32601` and turns
  malformed known params into `deny(not_allowed)`;
- E06: stdio EOF shuts down exec-server and kills its managed child;
- E07: a negative root-crash probe proves that a descendant can survive after
  `process/exited`, while `process/terminate` returns `running: false`; only
  whole-connection shutdown then reaps the process group, so E07 is not accepted;
- E08: a tainted server environment and poison user-home config are excluded by
  the isolated `CODEX_HOME`, and the child still receives exactly the requested
  environment;
- E09: the runtime-lock launch boundary verifies the full platform executable
  set before invoking a starter, launches only absolute `bin/codex`, replaces
  ambient PATH, and never invokes the starter on a Codex/bwrap digest or bundle
  layout failure. Both characterized Darwin releases start a real copied stock
  exec-server from the minimal verified layout while poison `codex`, `bwrap`,
  and `rg` sentinels on the supplied PATH remain untouched. The disposable
  native Linux gate additionally runs real read-only and workspace-write
  requests through the manifest-pinned bundled bwrap;
- E10: both characterized releases accept a stdio JSON-RPC payload of exactly
  64 MiB and disconnect on the first byte above it; they accept exactly 262,144
  JSON values and reject the next value as one malformed message without
  disconnecting. Stock has no dedicated argv/env count or byte guard and a
  negative control proves that it accepts a request above the smaller agentx
  limits. Replay retains the final approximately 1 MiB suffix and exactly
  50,000 one-byte chunks in the handshake probe, stdin dedupe retains exactly
  4,096 write IDs FIFO, and a closed process remains readable for approximately
  30 seconds before its ID becomes reusable. These stock facts and the smaller
  agentx product limits are required runtime-manifest fields.

The process probe deliberately accepts a `process/start` response arriving
after early output notifications and uses the one-based event sequence as the
ordering authority. It also fixes a subtle cursor distinction in the observed
stock protocol: a `process/read` with `maxBytes` advances `nextSeq` only beyond
its last returned output chunk, while a terminal read without `maxBytes` can
advance it beyond `exited` and `closed`.

The E05 callback timeout is not an approval TTL. The observed builds add a
five-second transport margin to `policyDecisionTimeoutMs`; agentx must enforce
its own controller/approval deadline and answer deny at that deadline instead
of waiting for the transport fallback. A returned `ask` decision also does not
pause the proxied request: stock Codex immediately blocks it with HTTP 403.
Agentx must hold the reverse RPC while an authorized approval is pending and
eventually answer `allow` or `deny`; returning `ask` is only a terminal blocked
result.

E09 is platform-scoped. Upstream source inspection establishes that the fs
helper and arg0 exec helper re-enter the absolute current Codex, while the
runtime-created Linux sandbox alias points to the same bytes; they do not have
independent helper artifacts to hash. Linux `bwrap` does. Stock first probes a
system bwrap from PATH and only then tries `codex-resources/bwrap`. The Phase 1
reference profile therefore removes `codex-package.json`, supplies a
nonexistent bundle-local PATH, and pins the bundled resource.

The disposable image gate passed natively for official stable 0.146.0
`linux-arm64`: Codex SHA-256
`cb5e8cb8a333a408ce6adbe0d4fad1845c69772c2216af7c1f88c98a11460dc6`
(size `269098800`) and bwrap SHA-256
`c547cbdc762a70ed216789ffaa4c6c0e7d2beabe32245a498f8e365a9fc8dab4`
(size `529168`). The gate rechecks bwrap argv0 behavior, verifies the generated
Linux sandbox alias, leaves a working ambient poison bwrap untouched, and
proves read-only plus workspace-write enforcement. It refuses Apple
Silicon-to-amd64 emulation because that path rewrites argv0 and rejects the
seccomp filter; `linux-amd64` remains open until the same target runs on a
native amd64 worker. Neither platform result closes the eventual agentx
safe-open/exec TOCTOU requirement.

E10 deliberately separates upstream facts from product admission. The stock
`ExecParams` deserializer and launch path clone `argv` and `env` without a
dedicated size/count check; only frame/JSON parsing and the eventual host spawn
limit apply. A Darwin or Linux `E2BIG` result therefore cannot become a portable
wire contract. The initial agentx envelope rejects inner frames above 8 MiB,
messages above 65,536 JSON values, argv plus optional arg0 above 256 elements or
16 KiB of UTF-8 content, env above 256 variables or 16 KiB of UTF-8
`name=value` content, and
write IDs above 128 bytes. The reference validator proves every exact boundary
and first rejection. Stock itself accepts the deliberately oversized argv/env
negative control, proving that forwarding without agentx validation is unsafe.

The manifest also sets agentx's per-process raw-output delivery/resume buffer to
8 MiB. That is distinct from stock's approximately 1 MiB `process/read` replay
and is not an unlimited log. Agentx must keep draining child stdout; overflow
produces an explicit `output_gap/buffer_overflow` with the lost sequence range.
Methods whose maximum response cannot fit the smaller agentx frame/complexity
envelope must not be negotiated until they have a request-specific cap or a
paginated contract.

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

A04 passes for the official stable 0.146.0 Linux amd64 artifact. The upstream managed allowlist
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

The positive job is implemented in `conformance/image/a04`. Its scratch
image has no external network, uses a read-only root and an empty hardened
`tmpfs` at `/etc/codex`, and refuses to start without independently supplied
release/SHA/size pins. The bounded fixture also has an HTTPS mode with an
ephemeral CA; its stock 0.146.0 host sensitivity control reaches real MCP
bootstrap. The image test checks a projected managed sentinel, the exact allowed
bootstrap, the captured model tool surface, and an enabled project layer while
requiring no MCP request for wrong-name, wrong-URL, user, or project additions.
It deliberately does not treat `mcpServerStatus/list` names as an allowlist
oracle: stock 0.146.0 lists configured-but-disabled entries and opens a new
connection to enabled entries while collecting status.

The passing image run used the official
`codex-x86_64-unknown-linux-musl.tar.gz` archive (SHA-256
`5ba3b9405543953081f661d0854d266f76e2abbe51d41349355a36de7673776a`)
and verified the unpacked binary as SHA-256
`2e863156ed35ecc5253b1e2f907a9143077b9f7cb51942070c61996471ff6e04`,
size `311001136`, release `0.146.0`. Both the direct image invocation and the
reproducible Make target passed under Apple `container` 1.1.0. This closes A04
only for that artifact; stable 0.146.0 remains rejected as a production runtime
because A03 independently fails.

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

A11 passes for both characterized 0.146.0 releases. The source attempt puts
distinct sentinels in config, auth, token, requirements-decoy, log, environment
dump, and transport-buffer files under `CODEX_HOME`. It also supplies an MCP
capability through `bearer_token_env_var`; every observed MCP bootstrap and tool
request carries that bearer, so the credential is exercised rather than inert.
After clean exit the probe confirms every sentinel file still contains its
marker, while model request bodies, stderr, and the app-server-reported rollout
contain none of the nine runtime-secret values. The rollout still contains the
user turns, assistant content, original MCP call ID, and safe tool result.

Checkpoint staging is again exactly the one rollout JSONL. The source home is
retired, a fresh attempt regenerates config, and MCP starts with a different
rotated bearer. Native resume succeeds, does not replay the MCP side effect, and
the next model request retains the model-visible history without either the old
or new runtime credential. The requirements file in this probe is an adversarial
`CODEX_HOME` decoy; A04 remains responsible for the real system requirements
mount. A11 tests accidental runtime-secret ingress. It does not authorize
byte-redacting content the user, model, or MCP deliberately put into model-visible
history: an unexpected runtime secret must reject/quarantine the checkpoint,
while model-visible content requires prevention, encryption, retention, and
deletion policy rather than a lossy “native resume” rewrite.

A12 is not yet a pass. Its host-level positive probe configures the model
provider to exfiltrate a worker-mTLS sentinel as an HTTP header if visible. A
sensitivity control explicitly injects the variable into one child and observes
the exact header. With the same value only in the parent, the isolated child
receives an explicit environment, the turn succeeds without the header or
sentinel in the request/stderr, and app-server reports an empty temporary cwd
outside the source tree. Those are launch-contract facts, not a filesystem
namespace: the eventual image must additionally prove that no workspace or
service-account volume is mounted anywhere the child can read.

Two negative controls keep the remaining boundary honest. On the characterized
Darwin host, clearing `CLOEXEC` on an otherwise unlisted parent pipe leaves that
descriptor open in the stock child; omitting `ExtraFiles` alone is not a
close-all policy, and the production Linux image must repeat the trap.
The production runner therefore needs both worker-owned `CLOEXEC` descriptors
and a final exec trampoline that closes/marks every descriptor above stderr from
an explicit allowlist. Separately, a configured scripted llmproxy returns a
cross-origin `307`; both characterized releases follow it, resend the model
request to the other origin, and complete the turn from that sink. A model/MCP
URL is routing configuration, not egress enforcement.

A trustworthy A12 positive gate must run the production Linux image with
different worker/child UIDs, unreadable worker credential/control state, the
real close-all trampoline, an empty non-workspace mount set, and per-process
network enforcement. It must allow llmproxy and the approved MCP egress path
while direct and redirected forbidden sinks receive zero requests. Kubernetes
`NetworkPolicy` is only Pod-scoped, so it cannot by itself distinguish the
worker control stream from child egress.

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
