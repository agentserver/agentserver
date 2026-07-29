# E09 disposable Linux image gate

This runner closes the Linux positive half of E09 for one exact native
platform artifact set. It builds a scratch image whose runtime bundle contains
only:

```text
<root>/
├── bin/codex
└── codex-resources/bwrap
```

There is no `codex-package.json`. The test constructs the launch through
`runtimelock.PrepareExecServerLaunch`, starts the absolute verified Codex path,
and inherits its nonexistent bundle-local PATH into the Linux sandbox helper.
The image contains no compatibility wrapper or shim.
The image also contains a working poison `bwrap` outside the runtime bundle;
it is supplied as the ambient PATH before launch, and selecting or probing it
leaves a marker and fails the gate.

The image runs as uid/gid 65532 with no capabilities, no network, a read-only
root, and a writable `/tmp`. Before starting exec-server it directly checks
that the pinned bwrap preserves `--argv0`, which the stock inner sandbox
re-entry requires. It then sends two real stock `process/start` requests:

- a managed read-only profile that reads a fixture and exits cleanly;
- a managed workspace-write profile that writes inside its declared workspace
  while a sibling path on the same writable `/tmp` remains denied.

Because the controlled PATH does not exist, the runtime has no package
metadata, and the only independently executable sandbox resource is the
manifest-verified `codex-resources/bwrap`, successful sandbox enforcement proves
that stock selected the bundled resource rather than a host executable.

Provide unpacked official Linux musl executables plus independently trusted
release pins. `AGENTSERVER_E09_GOARCH` must be `amd64` or `arm64`:

```sh
AGENTSERVER_CONTAINER_RUNTIME=container \
AGENTSERVER_E09_GOARCH=arm64 \
AGENTSERVER_CODEX_LINUX_BIN=/absolute/path/to/codex-aarch64-unknown-linux-musl \
AGENTSERVER_BWRAP_LINUX_BIN=/absolute/path/to/bwrap-aarch64-unknown-linux-musl \
AGENTSERVER_EXPECTED_CODEX_RELEASE=0.146.0 \
AGENTSERVER_EXPECTED_CODEX_SHA256=cb5e8cb8a333a408ce6adbe0d4fad1845c69772c2216af7c1f88c98a11460dc6 \
AGENTSERVER_EXPECTED_CODEX_SIZE_BYTES=269098800 \
AGENTSERVER_EXPECTED_BWRAP_SHA256=c547cbdc762a70ed216789ffaa4c6c0e7d2beabe32245a498f8e365a9fc8dab4 \
AGENTSERVER_EXPECTED_BWRAP_SIZE_BYTES=529168 \
make -C v2 conformance-image-e09
```

`AGENTSERVER_CONTAINER_RUNTIME` defaults to a Docker-compatible `docker` CLI.
Such a runtime must permit unprivileged nested user namespaces. Inability to
run bubblewrap under the stated production restrictions is a gate failure, not
a reason to add ambient capabilities.

Apple `container` keeps its build context under the workspace and additionally
requires `AGENTSERVER_E09_GOARCH` to match the host. Cross-architecture
emulation is not accepted for this gate: on Apple Silicon, an attempted
`linux/amd64` run rewrote bwrap's inner argv0 and later rejected Codex's seccomp
filter, even though the same exact request passed natively on `linux/arm64`.
The direct argv0 preflight also catches an emulating Docker-compatible runtime.

The recorded native Apple `container` 1.1.0 run used these official stable
`0.146.0` arm64 release artifacts:

- `codex-aarch64-unknown-linux-musl.tar.gz`: archive SHA-256
  `975bac91562abeedeb8f79636d51a86649b31f34a9de6a3bcb059565b6cf1f87`,
  unpacked SHA-256
  `cb5e8cb8a333a408ce6adbe0d4fad1845c69772c2216af7c1f88c98a11460dc6`,
  size `269098800`;
- `bwrap-aarch64-unknown-linux-musl.tar.gz`: archive SHA-256
  `878377db09e307f884c0c1b3ece9923cd54412c67dd43118fe0a98494b4a6a48`,
  unpacked SHA-256
  `c547cbdc762a70ed216789ffaa4c6c0e7d2beabe32245a498f8e365a9fc8dab4`,
  size `529168`.

The already-intaken amd64 artifacts remain valid candidate inputs: Codex
archive/binary SHA-256
`5ba3b9405543953081f661d0854d266f76e2abbe51d41349355a36de7673776a` /
`2e863156ed35ecc5253b1e2f907a9143077b9f7cb51942070c61996471ff6e04`
(size `311001136`), and bwrap archive/binary SHA-256
`debe6f23b085976553083e2a3090156f6e38bdc0f44245f4ee904fb515b8b4fe` /
`77360cb751ccedc5971391444ac86a8a33c15b04d6b4a6fe45f5d25496e62c4c`
(size `529776`). They still require this same target on a native Linux amd64
runner before amd64 can be declared closed.

A pass applies only to the logged platform/release/SHA/size. It does not close
the separate immutable safe-open/exec TOCTOU requirement for the eventual
agentx installation path.
