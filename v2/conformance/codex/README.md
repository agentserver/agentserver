# Codex conformance lab

This directory establishes executable facts about the stock Codex binary used
by agentserver v2. It is intentionally independent of the v1 runtime.

Ordinary `go test` runs the dialect, framing, subprocess, and fixture tests. Live
tests are opt-in and never discover Codex from `PATH`:

```sh
make -C v2 conformance-live AGENTSERVER_CODEX_BIN=/absolute/path/to/codex
```

The current live probes cover A01 through a bounded loopback scripted Responses
server (`initialize → initialized → thread/start → turn/start → item/completed →
turn/completed`), A02 experimental gating for `environments: []` in both
directions, E01 stdio/EOF, exec-server environment metadata, and these slices
of the executor matrix:

- E02: deterministic non-TTY `process/start`, explicit non-inherited child
  environment, output/exit/close sequencing, and retained `process/read` replay;
- E03: piped stdin, idempotent `writeId`, `unknownProcess`, `stdinClosed`, and
  terminate behavior (the signal/terminate race matrix remains open);
- E04: `fs/readFile`, `fs/open`, `fs/readBlock`, `fs/close`, file-URI rejection,
  and `fs/canonicalize`;
- E06: stdio EOF shuts down exec-server and kills its managed child.

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
complete: with empty environments/capability roots and fallback model metadata,
stock Codex still exposed web search, goal/plan, user-input, skills, and
multi-agent tools. Those must be removed before the MCP-only gate can pass.

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
