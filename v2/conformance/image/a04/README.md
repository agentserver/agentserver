# A04 disposable image gate

This runner is the positive half of A04. It executes a Linux amd64 stock Codex
candidate in a networkless scratch image, with a read-only root filesystem and
an empty, separately mounted `tmpfs` at `/etc/codex`. The test installs the real
system `requirements.toml` before starting Codex and refuses to run if those
mount invariants are absent. It never edits the host's `/etc`.

The caller must provide an unpacked official Linux amd64 executable and its
independently trusted release metadata:

```sh
AGENTSERVER_CODEX_LINUX_AMD64_BIN=/absolute/path/to/codex \
AGENTSERVER_EXPECTED_CODEX_RELEASE=0.146.0 \
AGENTSERVER_EXPECTED_CODEX_SHA256=<64-lowercase-hex-from-artifact-intake> \
AGENTSERVER_EXPECTED_CODEX_SIZE_BYTES=<exact-unpacked-size> \
make -C v2 conformance-image-a04
```

`AGENTSERVER_CONTAINER_RUNTIME` defaults to `docker` and may name a
Docker-compatible runtime. Setting it to Apple `container` selects the native
compatibility path: the build context stays under the workspace, and the test
uses its sole added `CAP_SYS_ADMIN` capability to harden the isolated
`/etc/codex` tmpfs before re-checking mountinfo. The expected digest and size
must come from release intake or the future signed runtime manifest; deriving
them from the file inside this runner would only prove self-consistency.

The image has no non-loopback network interface. The test creates bounded HTTPS
model/MCP fixtures on loopback, trusts only their ephemeral CAs, and proves:

- `configRequirements/read` observes a harmless managed sentinel, proving that
  the real system requirements file was loaded;
- the exact `executor` name plus exact HTTPS URL bootstraps and appears in the
  model's namespaced tool surface;
- the same URL under another name is not initialized;
- another URL under the allowed name is not initialized;
- an extra user MCP and an extra MCP from a demonstrably enabled trusted project
  layer receive zero MCP requests and remain absent from model tool exposure.

The gate does not infer enablement from `mcpServerStatus/list` names. Stock
0.146.0 reports configured-but-disabled names there and creates another
connection to enabled servers while taking the snapshot, so that RPC is a
status view rather than an allowlist oracle.

The recorded stable result used release `0.146.0`, unpacked binary SHA-256
`2e863156ed35ecc5253b1e2f907a9143077b9f7cb51942070c61996471ff6e04`,
and size `311001136`; the official archive SHA-256 was
`5ba3b9405543953081f661d0854d266f76e2abbe51d41349355a36de7673776a`.
The Make target passed under Apple `container` 1.1.0.

A passing run closes A04 only for the exact release/SHA/size in its log. It does
not close A03, A12, or the Linux sandbox selection portion of E09.
