# Codex conformance lab

This directory establishes executable facts about the stock Codex binary used
by agentserver v2. It is intentionally independent of the v1 runtime.

Ordinary `go test` runs the dialect, framing, subprocess, and fixture tests. Live
tests are opt-in and never discover Codex from `PATH`:

```sh
make -C v2 conformance-live AGENTSERVER_CODEX_BIN=/absolute/path/to/codex
```

The current live probes cover the initialization and stdio/EOF portions of A01
and E01 plus exec-server environment metadata. They are bootstrap evidence, not
a claim that the Phase 0 A01-A12/E01-E10 gate is complete. A production runtime
manifest and release-bound golden fixtures will be added only after a stock
release passes the full matrix.

The checked-in `fixtures/dialect` messages are synthetic codec fixtures. They
lock the Codex JSON-RPC envelope (including omission of `jsonrpc`) without
pretending to pin a Codex release.
