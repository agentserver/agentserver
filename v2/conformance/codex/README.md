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

The current live probes cover A01 lifecycle, A02 experimental gating, the
revised A03 exact `dynamicTools` surface/callback path plus the rejected direct
Codex-MCP path, and the revised A04 managed MCP deny-all image gate. Existing
A05–A12 probes retain valuable stock/runtime facts. A07 now also has a
worker-runner composition gate and exact stable artifact pass, but it still
needs the execution/control cases described below. A09's revised worker-runner
gate now passes the same exact stable artifact. A11's revised
worker-owned credential host gate now passes the exact stable 0.146.0 macOS
artifact, while its remaining OS boundary evidence belongs to revised A12;
A06/A12 still need the remaining product/image compositions. No
direct-Codex-MCP result is silently carried into the
production profile. The lab also covers
E01 stdio/EOF, exec-server environment metadata, and these executor slices:

- E02: deterministic argv/arg0, canonical cwd, exact non-inherited child
  environment, non-TTY and PTY streams, output/exit/close sequencing, and
  retained `process/read` replay;
- E03: piped stdin, idempotent `writeId`, `unknownProcess`, `stdinClosed`, and
  terminate behavior. A negative probe proves that `process/signal` returns the
  same empty success object for missing, delivered, and already-exited targets;
  the revised Phase 1 outer profile therefore does not negotiate or expose
  `process/signal`. An outer-contract gate is still required;
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
  whole-connection shutdown reaps the process group. The revised Phase 1
  adapter gives every managed process a dedicated stdio exec-server instance;
  its no-collateral cleanup gate is still required;
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
the 0.145.0 builtin blocker, but the old direct-Codex-MCP design still fails.
With one MCP server whose `tools/list`
advertises both `approved_echo` and `blocked_echo`, and Codex configured with
`enabled_tools = ["approved_echo"]`, the captured surface is exactly:

- `mcp__executor.approved_echo`;
- `list_mcp_resources`;
- `list_mcp_resource_templates`;
- `read_mcp_resource`.

The approved tool reaches `tools/call`; the blocked MCP tool and an unregistered
`exec_command` call both return `unsupported call` before reaching MCP. However,
a scripted `list_mcp_resources` call is executable and sends `resources/list`
to the MCP server despite the per-server `enabled_tools` allowlist. The alpha
therefore rejects the direct-MCP architecture and, independently, is not a
stable production release. Prompt instructions, event filtering, or making the executor return an
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
Stable 0.146.0 therefore also rejects direct Codex MCP. The dynamic bridge
described below changes the architecture instead of weakening this invariant;
stable 0.146.0 is now a candidate for the revised matrix, not yet a production
runtime pin.

Official alpha tag `rust-v0.147.0-alpha.2` (annotated tag object
`cff73291c5dd427cb305c8791c89ece30a11c61e`, peeled commit
`1d12a16dd9bcbd37bda22a71a1ae8ac2a49f0aba`) was tested as a targeted
candidate, not as an extension of the full release matrix. Its direct-MCP probes
observe the same empty no-MCP builtin surface, the same filtered approved tool,
the same three extra generic resource handlers, and the same executable
`resources/list` bypass. The stock signal and descendant-cleanup negative facts
also remain unchanged; the revised outer/dedicated-instance adapters are not
part of this alpha probe. The tested macOS arm64 binary SHA-256 is
`8e9f6e95320ea2360a07e7716cccea1292d67a2ba47d93bc81d601814abe7135`
(size `276983200`), the official platform archive SHA-256 is
`dfe3db5f1f32b19cf1b2875fe9347f970e3750b764a0dba483ee3b43375da4f7`,
and the canonical app-server schema tree SHA-256 is
`4393de9e38501330e39c433b43af7a58d3e0008e159f464845472ab66a6e7561`.
These facts reject its direct-MCP path without claiming A01-A12/E01-E10
conformance; as an alpha it is not a production pin.

A separate production-shape candidate probe now exercises the stock
`thread/start.dynamicTools` client bridge without configuring any Codex MCP
server. On stable 0.146.0 and 0.147.0-alpha.2, the captured model surface is
exactly `executor.approved_echo`; a real namespaced function call becomes an
`item/tool/call` reverse request with the original thread, turn, call id, tool,
and structured arguments, and the client result returns to the next model
request. Calls to an omitted executor tool and `exec_command` both fail as
`unsupported call` without producing a reverse request. A pending dynamic call
can be interrupted, but unlike approval and MCP-elicitation requests it never
emits `serverRequest/resolved`: normal client response and interrupted terminal
are the two release-bound cleanup signals the harness must track. This evidence
closes the revised stock side of A03/A05 and supports moving executor MCP client
ownership into the stateless worker. The old direct-MCP tests remain negative
regression guards; worker MCP transport, elicitation, resume, secret, and image
boundaries still require their revised gates.

A04 deny-all passes for the official stable 0.146.0 Linux amd64 artifact.
Official release binaries read the Unix system layer only from
`/etc/codex/requirements.toml`; the debug-only path redirect is ignored. The
disposable gate therefore installs `mcp_servers = {}` at the real system path
before process start. A production-shaped direct executor config, a user MCP,
and an enabled trusted-project MCP must all receive zero requests, while the
client-supplied dynamic executor tool remains visible.

The job is implemented in `conformance/image/a04`. Its scratch image has no
external network, uses a read-only root and an empty hardened `tmpfs` at
`/etc/codex`, and refuses to start without independently supplied
release/SHA/size pins. HTTPS loopback fixtures use ephemeral CAs. The test
checks a projected managed sentinel, all three endpoint request counters, the
enabled project layer, and an exact model surface of
`executor.approved_echo`. It does not use `mcpServerStatus/list` as an oracle.

The passing image run used the official
`codex-x86_64-unknown-linux-musl.tar.gz` archive (SHA-256
`5ba3b9405543953081f661d0854d266f76e2abbe51d41349355a36de7673776a`)
and verified the unpacked binary as SHA-256
`2e863156ed35ecc5253b1e2f907a9143077b9f7cb51942070c61996471ff6e04`,
size `311001136`, release `0.146.0`. The reproducible deny-all Make target
passed under Apple `container` 1.2.0. This closes revised A04 only for that
artifact; the remaining revised gates still prevent a production pin.

A05's revised stock side passes on stable 0.146.0 and 0.147.0-alpha.2: a thread
using `approvalPolicy=never` still emits the approved dynamic callback and no
Codex generic approval request. Product approval is outside app-server, on the
worker-owned MCP connection. The older direct-MCP `approve` versus `prompt`
probe remains a useful characterization but is no longer production config.

A06 is reopened for the revised architecture. The existing Codex-side probe
only proves elicitation when Codex is the MCP client, which production does not
use. A reference bridge-side sub-gate now uses the official Go MCP SDK to page
and verify the frozen catalog, issue `tools/call`, validate trusted
execution/run/call/generation metadata, and return each of
accept/decline/cancel to a fake gateway with zero dispatch on non-accept paths.
It pins the stateful MCP `2025-11-25` profile and rejects the newer stateless
profile before `tools/list`, because that profile cannot carry the nested
server-originated `elicitation/create` used by this design. Full A06 remains
open until the same path uses real pool/core approval CAS and covers active
expiry, nonce consumption, stale generation, and control/MCP disconnects.

A07's stock dynamic probe fixes a different rule: pending `item/tool/call`
interruption yields `turn/completed(interrupted)` and never emits
`serverRequest/resolved`; normal dynamic responses emit no resolved event
either. The reference `DynamicBridge` now proves that normal callbacks remain
owned until their JSON-RPC response write succeeds, unanswered callbacks are
cancelled by their owning terminal, and result/terminal races have one winner.
Its real-SDK cancellation sub-gate also proves cancellation reaches the fake
gateway and exits the worker's nested elicitation handler before approval
expiry, with zero dispatch. This avoids waiting for an SDK nested cancellation
notification that is carried on an already-abandoned SSE response. The
reference `AppServerRunner` now wires this bridge into a one-reader, one-writer
Codex wire loop with typed initialize/thread/turn/interrupt lifecycle, a bounded
notification sink, strict matching terminal cleanup, and pre-I/O resume catalog
checking. Its net.Pipe fixture and race suite cover normal response writes,
cancel, terminal races, unknown reverse requests, write failure, event overflow,
and native resume without a `dynamicTools` override. A non-live composition
gate now drives the complete runner → bridge → official-SDK MCP client →
authenticated HTTP gateway path while rejecting the worker bearer on every
app-server wire frame. Its real-disconnect case closes an in-flight MCP HTTP
connection: the runner emits one interrupt, receives the matching terminal,
and releases its callback. `MCPClient.Close` tracks all HTTP requests, first
allows a configurable bounded graceful close, then aborts the private
transport and returns a forced-close error instead of hanging in the SDK's
session DELETE path. A broken transport cannot deliver cancellation to an
already-dispatched remote handler, so gateway connection grace and execution
deadlines remain mandatory. The live test using the same runner and a stock
app-server passes on the documented stable `0.146.0` macOS arm64 binary
(SHA-256
`ae1d3ffe6d48aec6a4dc3f50e7eb8e0d11962485a6a9406c5a7012139383da02`,
size `271056976`). Full A07 remains open for already-dispatched execution
closure, real control disconnects, and the gateway-side disconnect
deadline/unknown transition.

A08's process-exit and byte-stability sub-gate passes for both characterized
0.146.0 releases. After a completed non-ephemeral turn with no typed outstanding request, the probe closes stdin
immediately and waits for a bounded clean exit without sleeping. Two bounded
post-exit snapshots agree on every relative path, mode, size, and SHA-256; the
reported rollout is complete JSONL containing the thread and turn content, and
`state_5.sqlite` has a SQLite header. Clean exit still leaves stable
`.sqlite-wal` and `.sqlite-shm` files for state, goals, logs, and memories.
Therefore process exit is a byte-stability barrier, not evidence that WAL data
was merged into the main databases. A08 does not decide which stable files form
a checkpoint; it still needs composition with revised dynamic/MCP cleanup.

A09's rollout-only/direct-MCP sub-gate passes for both `0.146.0-alpha.14` and stable `0.146.0`. For these builds,
the pinned checkpoint allowlist is exactly one app-server-reported rollout JSONL
per brain thread. The probe copies only that file, under its manifest-relative
path, into a fresh `CODEX_HOME`, verifies the staging tree contains no other
file, and then renames the source home so its old absolute paths cannot be used.
It regenerates config rather than restoring it, starts the same stock build, and
cold-resumes with the relocated rollout path and `excludeTurns: true`. A cold
`thread/resume` emits no `thread/started` notification; its successful RPC
response is the resume barrier, after which the next `turn/start` supplies
`environments: []`.

The restored model request contains both user turns, the original direct-MCP call ID,
and its exact tool result, while the MCP side effect is not executed again.
`state_5.sqlite`, every SQLite WAL/SHM sidecar, and the goals/logs/memories
databases are therefore runtime-derived state, not checkpoint payload. Config,
requirements, credentials, logs, caches, and transport state are likewise
excluded and must be recreated for each attempt. As a negative control, a
missing rollout makes `thread/resume` fail with `-32600` before any model request
or MCP initialization. Every future Codex build must repeat this native
round-trip before receiving the same allowlist.

The revised A09 worker-runner gate is now implemented and pinned in source to
stable `0.146.0`, the intersection of the existing dynamic-bridge and
rollout-only evidence. Its first attempt executes one worker-owned dynamic
callback and checkpoints only the reported rollout. It retires the source
`CODEX_HOME`, cold-resumes the rollout in a fresh home without a `dynamicTools`
override, and checks that the next real model request retains both user turns,
the original call ID/result, and the complete original model tool schema. A
shared counter requires exactly one total executor-side effect across both
attempts. The runner unit suite separately proves that a checkpoint catalog
digest mismatch fails before the first stdio byte; a changed catalog must
therefore create a new thread rather than alter the resumed one. This live gate
passes on the documented stable `0.146.0` macOS arm64 binary (SHA-256
`ae1d3ffe6d48aec6a4dc3f50e7eb8e0d11962485a6a9406c5a7012139383da02`,
size `271056976`). Revised A09 is therefore closed for this exact artifact;
every future Codex build must repeat the gate before receiving the same
checkpoint allowlist.

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

A11's old direct-MCP secret sub-gate passes for both characterized 0.146.0 releases. The source attempt puts
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

The revised A11 source gate now composes the stock app-server runner with the
worker-owned official-SDK MCP client. Codex receives no MCP endpoint,
`mcp_servers` entry, bearer environment variable, or bearer value: it receives
only the frozen dynamic catalog. The source worker authenticates every MCP
bootstrap/list/call request with one capability, executes exactly one dynamic
side effect, and checkpoints only the app-server-reported rollout. After the
source home is retired, a fresh worker authenticates with a rotated capability,
verifies the same catalog, and resumes without a `dynamicTools` override. The
gate scans the explicit child environment, config, every bounded `CODEX_HOME`
file, stderr, model request headers/bodies, rollout, and one-file checkpoint for
both executor bearer values. The separate model/llmproxy auth sentinel is
allowed only as the exact `Authorization` value on the scripted model transport
and is still forbidden from every model body, rollout, and checkpoint. The gate
requires the original call/result and complete tool schema in the restored
model request while the total side-effect count remains one.

The custom stateful gateway fixture and worker MCP composition run in ordinary
CI. The stock round-trip also passes on the documented stable `0.146.0` macOS
arm64 binary (SHA-256
`ae1d3ffe6d48aec6a4dc3f50e7eb8e0d11962485a6a9406c5a7012139383da02`,
size `271056976`). This host pass does not supersede close-all/UID/mount
isolation: inherited-FD exclusion and the worker/app network split remain
revised A12 image evidence. A11 therefore remains open only on that revised
image boundary; a host artifact pass is not an image pass.

A12 host characterization remains the reason the image boundary is mandatory.
Its exfiltration sensitivity control proves that an inherited worker variable
would become a model header, while an explicit child environment excludes it;
the reported cwd is an empty directory outside the source tree. On Darwin, a
parent pipe with `CLOEXEC` deliberately cleared survives a normal Go launch,
and both characterized releases follow a cross-origin model `307` to an
unconfigured sink. An explicit environment, omitted `ExtraFiles`, base URL,
managed requirements, and bearer audience are therefore not filesystem, FD, or
network isolation mechanisms.

The positive job is implemented in `conformance/image/a12` and passed natively
for official stable 0.146.0 `linux-arm64`. Its scratch image starts a bounded
root init fixture, then starts each scenario as a real worker UID process with
only `CAP_SETUID` and `CAP_SETGID`; that worker is the direct parent and stdio
supervisor of a fixed app UID final-exec child and waits for it. The child
environment excludes the worker secret, and immediately after launch the
one-child worker seals all of its own capabilities before supervising. Before
any isolation probe, final-exec
verifies real/effective/saved IDs, clears all
ambient/inheritable/permitted/effective capabilities, and sets `no_new_privs`
across every Go runtime OS thread. It disables dumpability, verifies the sealed
identity, then proves the worker credential/staging/control paths and worker
`/proc` state are
inaccessible, the worker cannot be signalled, workspace/service-account paths
are absent, and an intentionally inherited descriptor is closed by
`close_range(3, UINT_MAX, 0)` before the absolute stock Codex exec.

The old raw-netfilter profile used `meta skuid`: worker IPv4 egress was limited
to the worker-only harness endpoint, while app IPv4 could reach llmproxy and an
approved MCP. A real allowed model turn plus app-owned MCP call succeeded.
Direct and cross-origin-redirect forbidden sinks, a
DNS-shaped UDP sink, the worker-only endpoint, and an IPv6 sensitivity sink all
remain at zero app requests; root controls prove the UDP and IPv6 sinks are
live. The verified Codex artifact is SHA-256
`cb5e8cb8a333a408ce6adbe0d4fad1845c69772c2216af7c1f88c98a11460dc6`,
size `269098800`.

Those OS UID/capability/mount/FD facts remain valid, but the app-owned MCP
network profile conflicts with the revised architecture and no longer closes
A12. The new target must allow worker → harness/executor MCP and app → llmproxy
only, perform the real MCP call in the worker, and prove the app cannot reach
MCP. `linux-amd64` also remains open. The disposable in-image netfilter proof is not evidence that a real
Kubernetes NetworkPolicy, service routing, or egress proxy deployment is
correct; those remain deployment gates. Kubernetes `NetworkPolicy` is
Pod-scoped and cannot replace the per-UID boundary.

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
