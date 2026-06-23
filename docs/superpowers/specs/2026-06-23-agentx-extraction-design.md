# agentx — codex exec-server extraction design

> Status: draft 2026-06-23 (rev 2 — post-review fixes)
> Author: claude + mryao
>
> Rev 2 changes (2026-06-23): addressed 8 review findings — second allow-list
> call site in login crate (A1), `from_agent_identity_jwt` signature
> reality-check (A2), missed noise wiring in codex-exec-gateway server.go
> and config.go (A3), codex-exec-edge `/cloud/*` proxy (A4), missed noise
> bits in helm templates (A5), DNS A-record creation step (A6),
> wildcard-cert obviates pre-apply step (A7), §5 "mechanical rename only"
> overstatement corrected (A8). Added migration risks B1–B4. README
> rewrite folded into the same PR (new commit C11).
> Related:
>   - `2026-06-08-cxg-codex-0.137-upgrade-design.md`
>   - `2026-06-18-codex-exec-gateway-noise-relay-design.md` (this design DELETES the noise path it added)
>   - memory `[[codex_no_fork]]` (this design supersedes it for the exec-server subset)

## 1. Problem & goal

codex `rust-v0.142.0` introduced a hardcoded allow-list on `--use-agent-identity-auth`:
`ChatGptEnvironment::from_chatgpt_base_url` (in `codex-rs/agent-identity/src/lib.rs:53-70`)
only accepts `https://chatgpt.com[/...]` and `https://chatgpt-staging.com[/...]`. Any
other `chatgpt_base_url` — including our self-hosted issuer `https://codex-auth.agent.cs.ac.cn`
— hard-fails with:

```
Error: Agent Identity only supports production and staging ChatGPT environments
```

The CLI gives no env-var or config override (`agent_identity_authapi_base_url_override`
is passed as `None` from `cli/src/main.rs:1737`). This is the third upstream
breakage in two months that has landed on us by surprise (cf. noise relay
adoption in 0.141, env-id rename in 0.133). The cost/benefit of tracking
upstream codex for the `exec-server --remote` use case has flipped — we now do
more work absorbing upstream churn than we get back in features.

**Goal.** Extract the `codex exec-server --remote` subset into a new project
`agentx`, owned by us, hard-forked from codex `rust-v0.142.0`, with no upstream
git remote. Drop dependencies we don't use (noise relay, Bedrock, analytics).
Adopt new wire paths and hostnames. Single binary, single function. Distributed
via GitHub Releases.

**Non-goal.** This design does NOT touch `codex app-server` (the LLM dialogue
loop spawned by codex-app-gateway). That keeps following upstream codex — the
upstream churn rate there is acceptable because we treat it as a black-box
subprocess. The two concerns are independent.

## 2. Scope

### In

- New Rust project at `github.com/agentserver/agentx`, hard-forked from
  codex `rust-v0.142.0` with no upstream remote (no `git remote add upstream`,
  no rebase strategy).
- One binary `agentx`, equivalent to codex `exec-server --remote`. Single
  function — no subcommands.
- Three auth modes kept: Agent Identity JWT (default), ChatGPT login, API key.
  (Bedrock SigV4 dropped — see §2-out.)
- Plaintext JSON-RPC over WebSocket to codex-exec-gateway. No Noise, no
  RelayMessageFrame multiplexing.
- Multi-platform release: macOS (arm64 + x64), Linux musl (arm64 + x64).
  4-target GitHub Actions matrix on GitHub-hosted runners.
- New gateway hostname `x.agent.cs.ac.cn` replacing `codex-exec.agent.cs.ac.cn`.
- New gateway paths `/agentx/environment/{env_id}/register` (HTTP) and
  `/agentx/{exe_id}` (WS) replacing `/cloud/{executor,environment}/{id}/register`
  and `/cloud/relay/{rid}`.
- All env vars renamed `CODEX_*` → `AGENTX_*`; default config dir `~/.codex/`
  → `~/.agentx/`.

### Out

- `codex app-server` (LLM dialogue loop). codex-app-gateway continues to spawn
  upstream codex binary, pinned at the current 0.137 (or wherever it is when
  this lands). Out of scope here.
- TUI, Cloud Tasks, Code Mode, Collaboration Mode, MCP host, all other codex
  subcommands.
- Noise hybrid IK encryption (deleted; both endpoints are ours, E2E encryption
  is overhead with no threat-model benefit).
- Bedrock auth (`aws-auth` crate). agentserver does not route Bedrock traffic.
- ChatGPT/OpenAI brand assets in source code and user-facing strings.
- Backwards-compatible coexistence with old paths/hostnames. We hard-cut at
  rollout.
- Windows builds. codex itself does not ship Windows.

## 3. Architecture

```
                              user's macbook / any linux box
                              ┌──────────────────────────────────┐
                              │  agentx --remote <url>           │   ← single-purpose binary
                              │   --environment-id <exe_id>      │     (replaces codex exec-server)
                              │   --name laptop-1                │
                              │   --use-agent-identity-auth      │
                              │                                  │
                              │   AGENTX_ACCESS_TOKEN=<jwt>      │
                              │   AGENTX_AGENT_IDENTITY_         │
                              │     ALLOWED_BASE_URLS=https://…  │
                              └────────────────┬─────────────────┘
                                               │
                                               │ ① POST https://x.agent.cs.ac.cn/agentx/environment/{id}/register
                                               │    Authorization: AgentAssertion <jwt>
                                               │    Body: {"id":"...","public_key":"..."}      (plaintext, no security_profile)
                                               │
                                               │ ② WS upgrade wss://x.agent.cs.ac.cn/agentx/{exe_id}?token=<HMAC>
                                               │    Frames: 1 JSON-RPC msg per text frame (no protobuf, no segmentation)
                                               ▼
        ┌───────────────────────────────────────────────────────────────────────┐
        │                       agentserver in k8s                              │
        │                                                                       │
        │  ┌──────────────────────┐    ┌──────────────────────────────────┐    │
        │  │ codex-exec-gateway   │    │  agentserver core (Go)           │    │
        │  │ (Go)                 │◄──►│   internal/codexauth/            │    │
        │  │                      │    │     - JWKS endpoint              │    │
        │  │  ❌ noise/  (deleted)│    │     - task register endpoint     │    │
        │  │  ❌ relaypb/(deleted)│    │     - mint Agent Identity JWT    │    │
        │  │  ✅ plaintext bridge │    │     - OAuth proxy → Hydra        │    │
        │  └──────────┬───────────┘    └──────────────────────────────────┘    │
        │             │ bridge ws /bridge/{exe_id}                              │
        │             ▼                                                         │
        │  ┌──────────────────────┐    ┌──────────────────────────────────┐    │
        │  │ codex-app-gateway    │───►│  spawn upstream codex            │    │
        │  │ (Go)                 │    │   codex app-server               │    │
        │  │  env-mcp subprocess  │    │   (UNCHANGED — different concern)│    │
        │  └──────────────────────┘    └──────────────────────────────────┘    │
        └───────────────────────────────────────────────────────────────────────┘
```

Three unchanged systems:

1. codex-app-gateway continues spawning upstream `codex app-server`.
   Pinning, image build, the whole flow stays as-is.
2. `internal/codexauth/` (the JWKS issuer + task-register endpoint that
   gives clients their Agent Identity JWTs) is unchanged. agentx is wire-
   compatible with this.
3. envmcp-public-gateway is unchanged.

## 4. agentx repo layout

```
agentserver/agentx/
├── Cargo.toml                                    # workspace root
├── Cargo.lock                                    # inherited from codex v0.142
├── README.md                                     # rewritten, no OpenAI brand
├── LICENSE                                       # Apache-2.0 (matches codex)
├── NOTICE                                        # declares derivation from codex v0.142
├── .github/workflows/
│   ├── ci.yml                                    # cargo test + clippy + verify-no-codex-refs
│   └── release.yml                               # 4-target tarball → GH Releases
├── crates/
│   ├── agentx-cli/                               # was codex-rs/cli/  (main.rs heavily slimmed)
│   ├── agentx-exec-server/                       # was codex-rs/exec-server/  (noise/proto removed)
│   ├── agentx-app-server-protocol/               # was codex-rs/app-server-protocol/
│   ├── agentx-app-server-transport/              # was codex-rs/app-server-transport/
│   ├── agentx-api/                               # was codex-rs/codex-api/
│   ├── agentx-client/                            # was codex-rs/codex-client/
│   ├── agentx-protocol/                          # was codex-rs/protocol/
│   ├── agentx-file-system/                       # was codex-rs/file-system/
│   ├── agentx-sandboxing/                        # was codex-rs/sandboxing/
│   ├── agentx-shell-command/                     # was codex-rs/shell-command/
│   ├── agentx-agent-identity/                    # was codex-rs/agent-identity/  (allow-list → env)
│   ├── agentx-login/                             # was codex-rs/login/           (allow-list → env)
│   ├── agentx-arg0/                              # was codex-rs/arg0/
│   ├── agentx-async-utils/                       # was codex-rs/async-utils/
│   ├── agentx-bwrap/                             # was codex-rs/bwrap/  (Linux sandbox helper)
│   └── agentx-utils-*/                           # was codex-rs/utils/*/  (pty, path-uri, ...)
└── scripts/
    ├── install.sh                                # curl|sh installer
    └── verify-no-codex-refs.sh                   # CI gate: no stray "codex" symbols
```

Crates dropped vs codex-rs: `tui`, `cloud-tasks*`, `code-mode*`, `collaboration-mode-templates`,
`aws-auth`, `analytics`, `chatgpt`, `core`, `app-server`, `app-server-daemon`,
`app-server-client`, `app-server-test-client`, `connectors`, `plugins`, ~70
others not reachable from exec-server.

## 5. Semantic change points

The table below lists the **intentional behavioural changes** vs. codex
v0.142. Beyond these, agentx code is a renamed subset of codex — but the
rename itself is **not pure sed**. Three categories of work attend it:

- **True sed**: crate names `codex-*` → `agentx-*`, env vars `CODEX_*` →
  `AGENTX_*`, `~/.codex/` → `~/.agentx/` in string literals. Safe to script;
  may need a manual review pass over comments to keep prose coherent.
- **Mass deletion in shared files**: workspace `Cargo.toml` (113 of the 124
  members removed; `[workspace.dependencies]` table shrinks ~100 lines), CLI
  `Cargo.toml` (5 unused dep lines removed), CLI `Subcommand` enum (8 of 10
  variants removed, ~200 lines of dispatch code rewritten to flatten
  exec-server flags to top-level). Error-prone; CI catches with
  `cargo check`.
- **Coupled deletions across kept crates**: dropping `CodexAuth::BedrockApiKey`
  variant (~20 lines of `match` arms across `login/src/auth/manager.rs` and
  `cli/src/main.rs` `AuthMode` dispatch) and ~400 lines of noise/relay tests
  in `exec-server/{src,tests}/`. These are not sed; they require reading the
  match exhaustiveness errors `cargo build` emits and surgically removing the
  arms.

Plan the import commit accordingly (see §9.1 for the commit breakdown).

| # | File (post-fork path) | Change | Why |
|---|---|---|---|
| 1 | **Two call sites**: `crates/agentx-agent-identity/src/lib.rs:53-70` `ChatGptEnvironment::from_chatgpt_base_url` AND `crates/agentx-login/src/auth/agent_identity.rs:28-37` `agent_identity_authapi_base_url` (the resolver added in 0.142) | Both consult the env `AGENTX_AGENT_IDENTITY_ALLOWED_BASE_URLS` (comma-separated). Semantics: env set → URL must be in the list to pass; env unset → fall back to the original hardcoded whitelist (zero-config behaviour preserved). Apply in both places — either by factoring a shared helper or by patching both call sites identically. Verify by `grep -rn from_chatgpt_base_url` in the post-fork tree returning only test calls + these two production calls. | The direct trigger. Lets `https://codex-auth.agent.cs.ac.cn` pass. Patching only one site leaves the auth flow still bailing in `login`. |
| 2 | `crates/agentx-cli/src/main.rs:1774` `validate_api_key_remote_host` | Add `AGENTX_API_KEY_ALLOWED_HOSTS` env override; unset → preserve openai.com/openai.org whitelist | Same shape, for API-key path. |
| 3 | `crates/agentx-cli/src/main.rs` overall | (a) delete all non-exec-server subcommands (login/cloud/mcp/tui/...); (b) promote `exec-server` flags to top-level; (c) delete `--listen` local-mode branch; (d) rename `CODEX_*` env reads → `AGENTX_*`; (e) accept new flag `--agent-identity-authapi-base-url` and matching env `AGENTX_AGENT_IDENTITY_AUTHAPI_BASE_URL`. **NOTE the 0.142 signature**: `CodexAuth::from_agent_identity_jwt(jwt, chatgpt_base_url, auth_route_config)` — there is no public `agent_identity_authapi_base_url_override` arg. The override is currently consumed only by the private `from_agent_identity_jwt_with_authapi_base_url` helper (`login/src/auth/manager.rs:368`). Wire the env in by either: (i) adding a fourth public param to `from_agent_identity_jwt` and threading through, or (ii) making `from_agent_identity_jwt_with_authapi_base_url` pub(crate) and calling it from the CLI. Either is ~10 lines; pick (i) for clarity. | Single-function binary; new auth-api URL plumbing |
| 4 | `crates/agentx-exec-server/src/proto/`, `noise.rs`, all callers | DELETE `codex.exec_server.relay.v1.proto`, all RelayMessageFrame plumbing, all Noise handshake code. exec-server speaks plaintext JSON-RPC on the WS. Delete `security_profile` field from the register response struct | Noise is overhead when we own both ends |
| 5 | `crates/agentx-analytics/` (entire crate) | DELETE; replace `analytics::*` callers with no-ops or remove the call sites | No telemetry to OpenAI |
| 6 | `crates/agentx-aws-auth/` (entire crate) + `CodexAuth::BedrockApiKey` variant + CLI dispatch | DELETE | Bedrock unused |
| 7 | `~/.codex/` default dir → `~/.agentx/`; `CODEX_HOME` env → `AGENTX_HOME` | Find/replace + tests | Clean break |

**Explicitly NOT changed** (preserves rebase friendliness even though we don't
plan to rebase):

- Crate internal module trees, struct fields, function signatures.
- JSON-RPC method names (`initialize`, `process/start`, `fs/readFile`, …) —
  wire protocol; agentserver Go side consumes these verbatim.
- Agent Identity JWT claims, signing alg, JWKS shape — wire compat with
  `internal/codexauth/`.
- Process/PTY supervision, sandbox policy semantics, helper-process IPC.
- `Cargo.lock` exact deps (only `cargo update` later).

## 6. Wire protocol — agentx ↔ codex-exec-gateway

### 6.1 Registration (HTTP)

```
POST https://x.agent.cs.ac.cn/agentx/environment/{env_id}/register
Authorization: AgentAssertion <jwt>           # or Bearer <chatgpt-token|api-key>
Content-Type: application/json

{ "id": "exe_…", "public_key": "<base64-ed25519-ssh>" }

→ 200 OK
{ "executor_id": "exe_…",
  "url": "wss://x.agent.cs.ac.cn/agentx/{exe_id}?token=<HMAC>" }
```

- **No** `security_profile` field in request body (agentx doesn't send) or
  response body (gateway doesn't return). This is the field whose absence
  caused upstream codex 0.142 to fail-fast.
- **No** `executor_registration_id` field. We don't need rendezvous via a
  separate id — `exe_id` is the WS path key.

### 6.2 Bridge (WebSocket)

```
agentx ─── ws upgrade ───► wss://x.agent.cs.ac.cn/agentx/{exe_id}?token=<HMAC>
        ◄──── 101 Switching Protocols ────

Each WS text frame = exactly one JSON-RPC 2.0 message.

NO RelayMessageFrame protobuf wrapping
NO stream_id multiplexing (one WS = one session)
NO Noise handshake / NoiseWrapper encryption
NO segment_index / segment_count / ack_bits
```

### 6.3 JSON-RPC methods (agentx implements; gateway consumes)

Carried verbatim from current codex exec-server:

| Method | Direction | Purpose |
|---|---|---|
| `initialize` | gateway → agentx | handshake |
| `initialized` (notification) | gateway → agentx | handshake ack |
| `process/start` | gateway → agentx | start subprocess (PTY or pipe) |
| `process/read` | gateway → agentx | pull buffered output |
| `process/write` | gateway → agentx | stdin write |
| `process/terminate` | gateway → agentx | kill |
| `process/output` (notification) | agentx → gateway | streaming output |
| `process/exited` (notification) | agentx → gateway | exit code |
| `process/closed` (notification) | agentx → gateway | handle released |
| `fs/{readFile,writeFile,open,readBlock,close,createDirectory,getMetadata,canonicalize,readDirectory,remove,copy}` | gateway → agentx | filesystem ops with sandbox policy |

agentserver Go side `internal/codexexecgateway/bridge*.go` and
`internal/codexappgateway/envmcp/` already consume these on the plaintext
path — wire is zero-change for them, only path/hostname.

### 6.4 Auth (3 modes, no allow-list)

| Mode | What agentx sends | Gateway impl |
|---|---|---|
| Agent Identity JWT (default for our usage) | `Authorization: AgentAssertion <jwt>`, JWT from `AGENTX_ACCESS_TOKEN` env | `cloud_register.go:165` already handles `AgentAssertion ` prefix (will be renamed `agentx_register.go`) |
| ChatGPT login | `Authorization: Bearer <token>` from `~/.agentx/auth.json` (written by `codex login` equivalent — but agentx has no login subcommand; user provides the file out-of-band) | gateway uses same Bearer path |
| API key | `Authorization: Bearer <key>` from `AGENTX_API_KEY` env | same |

agentx's only behavioral difference from codex 0.142: both whitelists
(`AGENTX_AGENT_IDENTITY_ALLOWED_BASE_URLS`, `AGENTX_API_KEY_ALLOWED_HOSTS`)
read env and skip the openai.com check when set.

### 6.5 User-facing connect command (shown in agentserver UI)

```bash
# Install once
curl -fsSL https://github.com/agentserver/agentx/releases/latest/download/install.sh | sh

# Connect
export AGENTX_ACCESS_TOKEN='<jwt>'
export AGENTX_AGENT_IDENTITY_ALLOWED_BASE_URLS='https://codex-auth.agent.cs.ac.cn'
agentx --remote 'https://x.agent.cs.ac.cn' \
  --environment-id 'exe_xxx' --name 'my-laptop' --use-agent-identity-auth \
  --agent-identity-authapi-base-url 'https://codex-auth.agent.cs.ac.cn'
```

`internal/server/codex_executors.go:175` ConnectCommands template emits this.

## 7. agentserver Go-side changes (single PR)

All changes in one PR — `feat(agentx): drop noise+cloud, adopt /agentx/* paths,
new gateway host`. Chart bumps 0.69.5 → **0.70.0** in the same PR.

| Commit | Change |
|---|---|
| C1 | **Delete noise code (Go)**: `internal/codexexecgateway/noise/`, `internal/relaypb/`, `internal/codexexecgateway/handlers/noise_handlers.go`, all `*_noise_*_test.go`, `noise/live_codex_test.go`. **Also unwire the references**: `internal/codexexecgateway/server.go:47-50` (delete `if len(cfg.NoiseRelayHMACKey) > 0` block that constructs `NoiseHandlers` + `NoiseRouter`); `internal/codexexecgateway/config.go:29-31, 44` (delete `NoiseRelayHMACKey []byte` field and `os.Getenv("CXG_NOISE_RELAY_HMAC_KEY")` read). Without these the package won't build after the deletes above. |
| C2 | **Path swap (gateway)**: `internal/codexexecgateway/server.go` HTTP paths `/cloud/{executor,environment}/{id}/register` → `/agentx/environment/{env_id}/register`; ws `/cloud/relay/{rid}` → `/agentx/{exe_id}`. Old paths removed entirely (hard cut). |
| C2b | **Path swap (edge)**: `internal/codexececdge/server.go:58-59` **delete** both `/cloud/executor/{exe_id}/register` and `/cloud/environment/{env_id}/register` proxy routes. agentx has its own register-retry loop (1→30s exponential backoff in `exec-server/src/remote.rs:427`); the edge retry layer is redundant for agentx and adding `/agentx/*` proxy here would just duplicate it. Also delete the corresponding wsproxy route if it carries `/codex-exec/` only (verify; bridge stays direct to gateway). |
| C3 | Rename `cloud_register.go` → `agentx_register.go`; rename `CloudRegister` → `AgentxRegister`; delete `security_profile` field from response struct |
| C4 | `internal/server/codex_executors.go:175` ConnectCommands template rewritten as in §6.5 |
| C5 | `internal/codexauth/integration_test.go` invokes `agentx` binary instead of `codex` (test currently `exec.LookPath("codex")` at line 30 — switch to `agentx`); CI workflow downloads agentx tarball before running this test |
| C6 | `deploy/helm/agentserver/values.yaml`: delete `codexExecGateway.noiseRelayEnabled` and `codexGateway.noiseRelayHmacKey` (and their describing comment blocks); default `publicHost` for codexExecGateway → `x.<ingress.host>`. Also delete `/root/k8s/stacks/agentserver.ts:326` `noiseRelayEnabled: true` and `/root/k8s/Pulumi.nj-prod.yaml`'s `noiseRelayHmacKey` secret reference. Update `publicHost: "codex-exec.agent.cs.ac.cn"` (line 316) → `"x.agent.cs.ac.cn"` and the matching HTTPRoute hostnames (line 578 and ~643 if applicable). |
| C7 | `deploy/helm/agentserver/templates/codex-gateway-secret.yaml`: delete `noise-relay-hmac-key` field from `stringData`, AND delete the corresponding lookup-preservation logic at lines 24, 28, 32 (the `$noise = (index $existing.data "noise-relay-hmac-key" \| b64dec)` block and the `if not $noise ... randAlphaNum 48` block). Update the header comment listing keys. |
| C8 | `deploy/helm/agentserver/templates/codex-exec-gateway.yaml`: delete the **entire** `{{- if .Values.codexExecGateway.noiseRelayEnabled }}` conditional block (lines 102-114), not just the `CXG_NOISE_RELAY_HMAC_KEY` env entry. Also update the comment at lines 124-125 / 128 that references `/cloud/executor/{id}/register`. |
| C9 | `deploy/helm/agentserver/Chart.yaml`: bump 0.69.5 → 0.70.0 |
| C10 | **DNS** (manual prerequisite, not in PR): create A record `x.agent.cs.ac.cn → <istio-gateway-ip>` via DNSPod (account credentials live in `Pulumi.nj-prod.yaml:38-40`). DNS is not Pulumi-managed in this repo. Confirm with `dig +short x.agent.cs.ac.cn`. |
| C11 | Rewrite `README.md` + `README.zh.md` sections that mention `codex exec-server --remote` / `codex-exec-gateway` connect command. New command per §6.5. Same PR — keep day-1 user experience consistent. |

## 8. Build, release, distribution

### 8.1 GitHub Actions release matrix

Tagged on `v*` push. 4 targets on GitHub-hosted runners (no self-hosted):

| Target | Runner | Artifact |
|---|---|---|
| `aarch64-apple-darwin` | `macos-15` | `agentx-aarch64-apple-darwin.tar.gz` + `.dmg` (unsigned) |
| `x86_64-apple-darwin` | `macos-15` | `agentx-x86_64-apple-darwin.tar.gz` + `.dmg` (unsigned) |
| `x86_64-unknown-linux-musl` | `ubuntu-24.04` | `agentx-x86_64-unknown-linux-musl.tar.gz` (+ bwrap) |
| `aarch64-unknown-linux-musl` | `ubuntu-24.04-arm` | `agentx-aarch64-unknown-linux-musl.tar.gz` (+ bwrap) |

Linux musl builds reuse codex's hermetic pattern: `mlugg/setup-zig@v2.2.1` +
`zig 0.14.0`, `install-musl-build-tools.sh`, disable aws-lc jitter entropy.
Cargo home pinned hermetic per build.

macOS binaries/DMGs unsigned in v0.0.1 — user must `xattr -d com.apple.quarantine`
on first run. README documents this. Signing deferred (no Apple Developer cert
yet).

Each release publishes:
- 4 tarballs (with embedded `bwrap` on Linux)
- 2 DMGs (macOS)
- per-artifact `.sha256` checksum
- `install.sh` shell installer (detects platform, downloads, verifies, installs
  to `/usr/local/bin/`)

### 8.2 No OCI image

agentx is a user-side binary. agentserver in-cluster components do not spawn
agentx (codex-app-gateway spawns `codex app-server`, NOT `agentx`). Only
non-user consumer is the agentserver CI integration test, which downloads
the tarball directly. No OCI image needed; defer indefinitely (YAGNI).

### 8.3 Versioning

- First release: `v0.0.1`. pre-1.0 semver — wire-protocol breaks allowed in
  minor bumps until 1.0.
- Not coupled to agentserver chart version.

### 8.4 CI gates

`agentx repo .github/workflows/ci.yml`:
- `cargo fmt --check`
- `cargo clippy -- -D warnings`
- `cargo test --workspace`
- `scripts/verify-no-codex-refs.sh` — grep source tree for stray `codex`,
  `Codex`, `CODEX_`, `OpenAI`, `ChatGPT`; whitelist README/NOTICE/CHANGELOG
  where derivation is intentionally declared.

`agentserver repo CI`:
- new job that downloads agentx latest release tarball, runs
  `internal/codexauth/integration_test.go` against it. Replaces the current
  job that exercises `codex` binary.

## 9. Migration (hard-cut, no deprecation window)

```
Day 0   Phase A   agentx v0.0.1 release lands on GitHub
Day 1   Phase B   agentserver single PR (C1–C11; see §7) merges; chart 0.70.0 builds
Day 2   Phase C   pulumi up; old hostname/paths gone, new hostname/paths live
Day ≥3  Phase D   DNS/cert cleanup
```

### 9.1 Phase A — agentx repo (no production impact)

1. Manually create `github.com/agentserver/agentx` (NOT GitHub's Fork button —
   we want no upstream remote).
2. Snapshot codex `rust-v0.142.0` source: `git clone github.com/openai/codex
   codex-snapshot && cd codex-snapshot && git checkout rust-v0.142.0`
3. **Import commit**: `cp -r` only the crates listed in §4 into the new
   repo, then `git add . && git commit -m "Import codex rust-v0.142.0 subset"`.
   This commit alone **does not build** — it carries forward many references
   to crates we dropped. That's expected; the cleanup commits below restore
   buildability.
4. Subsequent commits, each independently reviewable, in this order:
   - **chore: cargo workspace cleanup** — edit `Cargo.toml` workspace
     `members` list to the 9 kept crates only; shrink
     `[workspace.dependencies]` accordingly. Edit `crates/agentx-cli/Cargo.toml`
     to delete unused crate deps (`codex-cloud-tasks`, `codex-tui`,
     `codex-mcp`, `codex-mcp-server`, `codex-app-server`). Run
     `cargo check` — many compile errors are expected here; this is the
     "set the stage" commit.
   - **refactor(cli): drop all non-exec-server subcommands** — rewrite the
     `Subcommand` enum in `cli/src/main.rs` (remove 8 of 10 variants),
     flatten exec-server flags to top-level, delete `--listen` local mode
     branch. After this commit, `cargo check` should pass except for items
     covered by subsequent commits.
   - **refactor: drop Bedrock auth path** — delete `CodexAuth::BedrockApiKey`
     variant; rewrite the 6+ `match` arms across `login/src/auth/manager.rs`
     and CLI dispatch that exhaustively-match on it. Delete
     `login/src/auth/bedrock_api_key.rs`. (Workspace `aws-auth` already
     removed by the workspace-cleanup commit.)
   - **refactor: drop analytics** — delete `analytics::*` call sites in kept
     crates or replace with no-op stubs. (Crate already dropped by workspace
     cleanup.)
   - **refactor: drop noise relay + RelayMessageFrame + security_profile** —
     delete `exec-server/src/noise*.rs`, `exec-server/src/relay*.rs`,
     `exec-server/src/proto/codex.exec_server.relay.v1.proto`, all
     `exec-server/{src,tests}/*noise*` test files (~400 lines), and the
     `security_profile` field from the register response struct. Update any
     callers in app-server-protocol / api / client (verify none beyond
     tests).
   - **chore: rename crates `codex-*` → `agentx-*`** — sed across
     `Cargo.toml` files, all `use codex_*` paths, all `extern crate`
     references. Should be mostly mechanical once the above commits have
     pruned the surface area.
   - **chore: rename `CODEX_*` env vars → `AGENTX_*`** — sed pass over
     string literals; manual review of comments.
   - **chore: rename `~/.codex` → `~/.agentx`** — same.
   - **chore: workspace.package metadata** — rewrite `[workspace.package]`
     `homepage`, `repository`, `authors`; remove OpenAI brand strings from
     user-facing `--help` text and error messages.
   - **feat: `AGENTX_AGENT_IDENTITY_ALLOWED_BASE_URLS` env override** —
     both call sites per §5 #1.
   - **feat: `AGENTX_API_KEY_ALLOWED_HOSTS` env override** — per §5 #2.
   - **feat: `--agent-identity-authapi-base-url` flag + env** — wire into
     `from_agent_identity_jwt` per §5 #3(e).
   - **chore: rewrite README, NOTICE, drop remaining brand strings**.
   - **ci: add release.yml (4 targets) + ci.yml (fmt/clippy/test/verify-no-codex-refs)**.
5. Tag `v0.0.1`; release workflow produces tarballs.

This phase is independent of production — agentserver continues running as-is
through all of Phase A.

### 9.2 Phase B — agentserver single PR

Single PR with commits C1–C11 from §7. Merge to main → chart 0.70.0 builds →
image tag 0.70.0 in registry.

No coexistence layer: the PR removes noise code AND old `/cloud/*` paths in
the same change. main branch is briefly chart-version-ahead of what's deployed
in k8s — that's fine; Pulumi controls deploy.

### 9.3 Phase C — Pulumi hard-cut

Sequence:

1. **Pre-step 1 — DNS** (covered by commit C10 step):
   - Create A record `x.agent.cs.ac.cn → <istio-ingress-gateway-IP>` via
     DNSPod. Verify with `dig +short x.agent.cs.ac.cn`.
   - DNS is not Pulumi-managed in `/root/k8s/`; this is operator action.

2. **Pre-step 2 — TLS cert** (no action needed):
   - `/root/k8s/stacks/cert-manager.ts` already provisions a wildcard
     `*.agent.cs.ac.cn` certificate. `x.agent.cs.ac.cn` is automatically
     covered; no per-host Certificate resource needed.
   - Verify with `kubectl get certificate -n istio-ingress` showing the
     wildcard cert in `Ready=True` state.

3. **Edit `/root/k8s/stacks/agentserver.ts`** (single commit):
   - chart version 0.69.5 → 0.70.0
   - all image tags 0.69.5 → 0.70.0 (`codexAppGateway.image.tag`,
     `codexExecGateway.image.tag`, etc.)
   - `publicHost: "codex-exec.agent.cs.ac.cn"` → `"x.agent.cs.ac.cn"`
   - HTTPRoute hostname (line 578 and any others matching old hostname):
     **replace** (don't add) old → new
   - delete `noiseRelayEnabled: true` (line 326)
   - delete `noiseRelayHmacKey` secret reference in `Pulumi.nj-prod.yaml`

4. `pulumi preview`. **Verify the diff**:
   - Image / chart / value changes show as `~` (update).
   - HTTPRoute hostname change: must show as `~` on `spec.hostnames` only,
     NOT as a `-` then `+` (delete-then-create). If preview shows recreate,
     stop and migrate via two-step: (a) extend `hostnames: [old, new]`,
     `pulumi up`, then (b) shrink to `[new]`. Recreating mid-flight could
     cause a longer Istio routing gap than the deploy itself.

5. `pulumi up` (scope: **nj-prod stack only** — other stacks
   (bj-prod / ictbj-prod / ucas-prod) do NOT deploy agentserver).
   The moment Pulumi applies:
   - Old hostname `codex-exec.agent.cs.ac.cn`: DNS still resolves, but Istio
     Gateway returns 404 (no route). Any in-flight `codex exec-server --remote
     codex-exec...` connection drops.
   - New hostname `x.agent.cs.ac.cn`: live and serving `/agentx/*`.
   - codex-exec-gateway uses `strategy: Recreate` (RWO audit PVC); expect
     40–70 s of 503 on `/agentx/*` during the pod-replace cycle. agentx
     clients retry register with their own 1→30 s exponential backoff and
     reconnect once the new pod is ready.

6. Issue UI banner / email to users: "switched; install agentx; new connect
   command is …". Administrative announcement, not part of this spec.

### 9.4 Phase D — cleanup (any time after C settles)

- Delete `codex-exec.agent.cs.ac.cn` DNS A record.
- Delete its cert-manager Certificate.
- Update FAQ / docs.

### 9.5 Rollback

True rollback window: **~1 hour after pulumi up**.

- If agentx users haven't actually started registering yet → `git revert
  stacks/agentserver.ts` + `pulumi up` brings back chart 0.69.5 + old hostname +
  old `/cloud/*` paths. ~5 min.
- Once agentx users have started registering: rollback to 0.69.5 breaks them
  because 0.69.5 expects `security_profile` on register. After this point,
  only fix-forward — apply patches on top of 0.70.0, do not revert.

Accepted as part of the design.

## 10. Risk register

| # | Risk | Likelihood | Mitigation |
|---|---|---|---|
| 1 | Upstream codex security fix needs cherry-picking | Medium (over 12 months) | Manual port: read upstream diff, recreate against renamed crates; small enough surface area to remain feasible. CI verify-no-codex-refs catches accidental brand reintroduction. |
| 2 | macOS unsigned binary blocked by Gatekeeper | High (first-run UX) | README explicit `xattr -d com.apple.quarantine` step; eventually add codesigning when budget allows |
| 3 | Pulumi up vs cert-manager race → HTTPS down window | Low (mitigated by pre-step in 9.3) | Pre-apply Certificate, verify issuance, THEN pulumi up |
| 4 | Rollback impossible after agentx users register | Accepted | Fix-forward policy. Single-PR design means we can hotfix on 0.70.x without untangling. |
| 5 | bwrap (Linux sandbox helper) build flakiness across distros | Low | Statically link via musl; codex already validated this path. CI runs against ubuntu-24.04 (matches release runner). |
| 6 | Future upstream codex feature we want (e.g. better PTY semantics) | Low | Reimplement in agentx if needed. Past two months show upstream changes mostly hurt us, not help. |
| 7 | `verify-no-codex-refs.sh` false positives blocking development | Low | Maintained whitelist file at `scripts/.codex-refs-allowed`; PR-time grep diff |
| 8 | **codex-exec-gateway Recreate downtime kills active bridge WS sessions** | High (1 occurrence at cutover) | RWO audit PVC forces `strategy: Recreate`; pulumi up creates a 40–70 s 503 window on `/agentx/*` during pod replace. Active `agentx --remote` sessions drop mid-RPC; agentx auto-retries register (1→30 s exponential backoff in `exec-server/src/remote.rs:427`) and reconnects. Bridge sessions in the middle of a `process/start` see EOF and must be restarted by the user. Mitigation: schedule pulumi up during low-traffic window; pre-announce 1 h prior. |
| 9 | **github.com Releases CDN intermittently unreachable from mainland China** | Medium (ongoing UX) | `install.sh` uses `curl -fsSL https://github.com/agentserver/agentx/releases/...`. When the GitHub CDN is throttled, users can't install. Mitigation: document workaround in README (use proxy, or `wget` from a mirror). Mirroring releases to in-region storage (S3 / OSS) is a follow-up if pain is sustained, not a v0.0.1 requirement. |
| 10 | **Pulumi HTTPRoute hostname swap mode** | Low (gated by §9.3 step 4 preview check) | If Pulumi decides to delete-then-recreate the HTTPRoute on hostname change, there is a routing gap on top of the Recreate window. Mitigation: §9.3 step 4 requires the operator to confirm `pulumi preview` shows `~` on `spec.hostnames` only, and to fall back to two-step `[old, new] → [new]` migration if recreate is shown. |
| 11 | **README / docs lag in non-spec stacks** | Low | Same-PR `C11` covers `README.md` + `README.zh.md`. Other doc surfaces (frontend strings, OpenAPI examples) are not covered by this spec — accepted as follow-up docs PR. Frontend `RemoteExecutorsPanel.tsx` consumes the server response template (updated by C4), so the live UI shows the new command on day 1 automatically. |

## 11. Open questions (none blocking)

None — all decisions converged through brainstorming. Future revisions of
this doc may extend it (e.g. when we want codesigning, or when wire protocol
breaks for 1.0).

## 12. Glossary

- **exec-server**: codex subcommand that owns a remote machine (or local
  loopback) for spawning processes and reading files. Communicates with a
  central gateway via WS.
- **agent identity JWT**: a JWT issued by `internal/codexauth/` that lets a
  remote exec-server prove it represents a particular workspace user without
  carrying a long-lived OAuth refresh token.
- **codex-exec-gateway**: agentserver-side Go service that accepts exec-server
  registrations and bridges harness ↔ executor WS traffic.
- **codex-app-gateway**: separate Go service that spawns `codex app-server`
  subprocesses for the LLM dialogue loop. Out of scope for this design.
- **bwrap**: bubblewrap (Linux unprivileged sandbox helper) used by sandboxing
  filesystem ops.
- **hard fork**: in this document, means "no `git remote add upstream`, no
  rebase strategy; manual port of upstream commits when needed". Distinct
  from a GitHub "Fork" button which preserves upstream linkage.
