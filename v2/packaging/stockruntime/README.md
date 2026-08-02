# Stock Codex production runtime

`runtime-manifest.json` is the exact production runtime identity shared by the
harness image and the independently released `agentx` executor endpoint. Its
bytes are generated from `internal/stockruntime`; a contract test rejects any
drift between the Go profile and this reviewed artifact.

The enabled production platform is intentionally only `linux-arm64`. The
runtime bundle paired with this manifest contains exactly:

```text
bundle/
├── bin/codex
└── codex-resources/bwrap
```

Both executables are official stock Codex 0.146.0 release artifacts and are
verified by SHA-256 and size before packaging. The build never downloads them.

The manifest is not self-authenticating. The harness trusts the copy sealed in
an image selected by OCI digest. An `agentx` installation additionally requires
an operator-owned detached Ed25519 signature and overlap keyring; private
release keys are never stored in this repository or accepted by an image
build.
