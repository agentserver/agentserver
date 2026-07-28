# agentx runtime lock

`runtime-manifest.json` is deliberately absent while Phase 0 is incomplete.
The production filename is reserved for a stock Codex release that has passed
the full A01-A12 and E01-E10 matrix; a developer-machine binary or source
checkout must never be promoted by filling in plausible values.

The source contract is
[`../../api/schema/runtime-manifest.schema.json`](../../api/schema/runtime-manifest.schema.json).
The Go semantic validator lives in `internal/runtimelock`.

Digest rules:

- SHA-256 values are lowercase, unprefixed, 64-character hex strings.
- A file digest covers the exact release-bundle bytes at the manifest-relative
  path. Runtime verification rejects symlinks and path escape.
- A tree digest is SHA-256 over UTF-8 records sorted by relative slash path:
  `<file-sha256><two spaces><relative-path><LF>`.
- `appServerSchemaSha256` covers every JSON file emitted by `codex app-server
  generate-json-schema --experimental`, not one selected file. Stock generation
  can randomize JSON object key order, so `canonical-json-tree-v1` decodes each
  file with numbers preserved and re-encodes compact JSON with lexicographically
  sorted object keys while retaining array order. Its Go `encoding/json`
  escaping and number behavior is pinned by golden tests. The algorithm name is
  part of the manifest and two consecutive generations must produce the same
  digest.
- Original generated files are retained as conformance evidence even though
  their raw tree digest is not a reproducible protocol identity.
- `execProtocolSourceSha256` covers the explicitly pinned upstream protocol
  source allowlist. That allowlist will be committed with the production lock.

`sourceUrl` must be a stable HTTPS release URL without credentials, query, or
fragment. Release signing and SBOM verification belong to the outer agentx
bundle; this inner manifest pins every executable byte agentx may launch.

Hash verification alone does not make launch atomic on a hostile mutable
filesystem. The agentx supervisor must additionally use platform-specific safe
open/execute and immutable-install controls described in the architecture.
