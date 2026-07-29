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
- `codex` is the stock entrypoint. `externalExecutables` contains only files
  with distinct executable bytes; it is not a list of logical helper modes.
  In the characterized stock implementation, the fs helper and arg0 exec
  helper re-enter the absolute Codex executable, while the Linux
  `codex-linux-sandbox` alias points to that same executable. Their bytes are
  already covered by the Codex digest. Linux `codex-resources/bwrap` is a real
  external executable and has its own entry.
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
bundle; this inner manifest pins every executable byte in the enabled runtime
profile.

The Phase 1 exec-server profile is a deliberately minimal bundle:

```text
<root>/
├── bin/codex
└── codex-resources/bwrap    # Linux only
```

It does not contain `codex-package.json`. Stock package-layout detection would
otherwise prepend `codex-path` to PATH before agentx can supervise requests.
The verified launch plan starts `<root>/bin/codex` by absolute path and replaces
ambient PATH with `<root>/.agentserver-no-path`, which verification requires not
to exist. Stock may then prepend its protected, per-process arg0 alias directory;
those aliases all target the already verified Codex. On Linux this prevents the
stock launcher's system-bwrap-first lookup from selecting a host `bwrap`, so it
falls through to the verified `codex-resources/bwrap` resource.

This PATH rule covers runtime discovery, not workload commands. Product tools
must send deterministic argv and an explicit child environment; any workload
PATH is constrained separately by the env/owner policy.

Hash verification alone does not make launch atomic on a hostile mutable
filesystem. The agentx supervisor must additionally use platform-specific safe
open/execute and immutable-install controls described in the architecture. The
Linux production image must also prove with a real sandbox request and poisoned
host PATH that the bundled bwrap is selected. A Darwin host probe cannot close
that image-level gate.
