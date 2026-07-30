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
- `execServerBounds` records release behavior established by E10. For the two
  characterized 0.146 builds this is a 64 MiB stdio payload, 262,144 JSON
  values, no dedicated argv/env limit beyond transport and the host process
  API, 1 MiB/50,000-chunk retained output, 4,096 retained stdin write IDs, and
  30,000 ms exited-process retention. A host `E2BIG` result is not a manifest
  limit.
- `agentxLimits` records the smaller product envelope that must be enforced
  before writing to stock stdin: initially 8 MiB/65,536 JSON values, 256
  argv-plus-arg0 elements and 16 KiB of their UTF-8 bytes, 256 final
  non-inherited env variables and 16 KiB `name=value` bytes, 128-byte write IDs,
  and an 8 MiB per-process raw-output buffer. Exact input byte accounting is
  defined by the Go reference validator. A separately released agentx must run
  the synced fixture against its own validator.

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

Phase 1 launches this bundle as one stdio exec-server instance per managed
process; an instance accepts at most one `process/start`. Environments that
pass the additional filesystem gate advertise
`process-v1+filesystem-read-v1`; process-only environments remain valid as
`process-v1`. The filesystem profile exposes only the bounded outer
`agentx/fs/readFileBlock` method. Each call starts a disposable fs-only instance
that cannot start processes, performs exactly
`fs/open(sandbox=null) -> fs/readBlock -> fs/close`, and then shuts the instance
down and verifies cleanup. Stock 0.146.0 rejects platform-sandboxed streaming
reads, so authorization comes from agentx's before/after registered-root checks
and runner OS containment, not from the inner `sandbox=null`. Remote callers
never receive a stock handle and never invoke unbounded `fs/readFile`.

The outer agentx capability set exposes `process/write` and
`process/terminate`, but deliberately omits stock `process/signal`, whose `{}`
response cannot distinguish missing, delivered, and already-exited targets. If
a root process exits without `process/closed`, agentx closes only that process's
dedicated stdio instance and verifies containment cleanup, so other managed
processes are unaffected.

This PATH rule covers runtime discovery, not workload commands. Product tools
must send deterministic argv and an explicit child environment; any workload
PATH is constrained separately by the env/owner policy.

Hash verification alone does not make launch atomic on a hostile mutable
filesystem. The agentx supervisor must additionally use platform-specific safe
open/execute and immutable-install controls described in the architecture. The
same restriction applies to filesystem reads: same-UID insecure development
mode cannot close a symlink swap between canonical-path validation and stock
open, and therefore is not a production filesystem containment claim. The
Linux production profile must also prove with real sandbox requests and a
poisoned host PATH that the bundled bwrap is selected. The checked-in disposable
image gate has done so natively for the exact official stable 0.146.0
`linux-arm64` Codex/bwrap artifact set; it does not generalize to another
release, digest, or architecture. In particular, `linux-amd64` remains open
until the same target passes on a native amd64 worker. Cross-architecture
emulation and a Darwin-only host probe cannot close that image-level gate.
