# agentx — codex exec-server extraction design

> Status: draft 2026-06-23
> Author: claude + mryao
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

Everything outside this table is **mechanical rename only** (sed `codex_*` →
`agentx_*`, `CODEX_*` → `AGENTX_*`, `~/.codex/` → `~/.agentx/`).

| # | File (post-fork path) | Change | Why |
|---|---|---|---|
| 1 | `crates/agentx-agent-identity/src/lib.rs:53-70` `ChatGptEnvironment::from_chatgpt_base_url` | Read `AGENTX_AGENT_IDENTITY_ALLOWED_BASE_URLS` env (comma-separated); match → ok; unset → preserve original whitelist | The direct trigger. Lets `https://codex-auth.agent.cs.ac.cn` pass. |
| 2 | `crates/agentx-cli/src/main.rs:1774` `validate_api_key_remote_host` | Add `AGENTX_API_KEY_ALLOWED_HOSTS` env override; unset → preserve openai.com/openai.org whitelist | Same shape, for API-key path. |
| 3 | `crates/agentx-cli/src/main.rs` overall | (a) delete all non-exec-server subcommands (login/cloud/mcp/tui/...); (b) promote `exec-server` flags to top-level; (c) delete `--listen` local-mode branch; (d) rename `CODEX_*` env reads → `AGENTX_*`; (e) accept new flag `--agent-identity-authapi-base-url` and matching env `AGENTX_AGENT_IDENTITY_AUTHAPI_BASE_URL` (wire it into `CodexAuth::from_agent_identity_jwt`'s `agent_identity_authapi_base_url_override` arg, currently hardcoded `None`) | Single-function binary; new auth-api URL plumbing |
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
| C1 | DELETE `internal/codexexecgateway/noise/`, `internal/relaypb/`, `internal/codexexecgateway/handlers/noise_handlers.go`, all `*_noise_*_test.go`, `noise/live_codex_test.go` |
| C2 | `server.go` HTTP paths: `/cloud/{executor,environment}/{id}/register` → `/agentx/environment/{env_id}/register`; ws `/cloud/relay/{rid}` → `/agentx/{exe_id}`. Old paths removed entirely (hard cut). |
| C3 | Rename `cloud_register.go` → `agentx_register.go`; rename `CloudRegister` → `AgentxRegister`; delete `security_profile` field from response struct |
| C4 | `internal/server/codex_executors.go:175` ConnectCommands template rewritten as in §6.5 |
| C5 | `internal/codexauth/integration_test.go` invokes `agentx` binary instead of `codex`; CI workflow downloads agentx tarball |
| C6 | `deploy/helm/agentserver/values.yaml`: delete `codexExecGateway.noiseRelayEnabled` and `codexGateway.noiseRelayHmacKey`; default `publicHost` for codexExecGateway → `x.<ingress.host>` |
| C7 | `deploy/helm/agentserver/templates/codex-gateway-secret.yaml`: delete `noise-relay-hmac-key` field |
| C8 | `deploy/helm/agentserver/templates/codex-exec-gateway.yaml`: delete `CXG_NOISE_RELAY_HMAC_KEY` env block |
| C9 | `deploy/helm/agentserver/Chart.yaml`: bump 0.69.5 → 0.70.0 |

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
Day 1   Phase B   agentserver single PR (C1-C9) merges; chart 0.70.0 builds
Day 2   Phase C   pulumi up; old hostname/paths gone, new hostname/paths live
Day ≥3  Phase D   DNS/cert cleanup
```

### 9.1 Phase A — agentx repo (no production impact)

1. Manually create `github.com/agentserver/agentx` (NOT GitHub's Fork button —
   we want no upstream remote).
2. Snapshot codex `rust-v0.142.0` source: `git clone github.com/openai/codex
   codex-snapshot && cd codex-snapshot && git checkout rust-v0.142.0`
3. **Single import commit**: `cp -r` only the crates listed in §4 into the new
   repo, then `git add . && git commit -m "Import codex rust-v0.142.0 subset"`.
   No upstream history preserved — keeps blame clean; we won't rebase anyway.
4. Subsequent commits, each independently reviewable, in this order:
   - chore: rename crates `codex-*` → `agentx-*`
   - chore: rename `CODEX_*` env vars → `AGENTX_*`
   - chore: rename `~/.codex` → `~/.agentx`
   - feat: `AGENTX_AGENT_IDENTITY_ALLOWED_BASE_URLS` env override
   - feat: `AGENTX_API_KEY_ALLOWED_HOSTS` env override
   - feat: `--agent-identity-authapi-base-url` flag + env wired into
     `from_agent_identity_jwt`
   - refactor(cli): drop all subcommands except exec-server; promote to top-level
   - refactor(cli): drop `--listen` local mode
   - refactor: remove noise relay + RelayMessageFrame + `security_profile`
   - refactor: remove `aws-auth` crate + Bedrock auth variant
   - refactor: remove `analytics` crate
   - chore: rewrite README, drop OpenAI branding
   - ci: add release.yml (4 targets) + ci.yml (fmt/clippy/test/verify-no-codex-refs)
5. Tag `v0.0.1`; release workflow produces tarballs.

This phase is independent of production — agentserver continues running as-is
through all of Phase A.

### 9.2 Phase B — agentserver single PR

Single PR with commits C1-C9 from §7. Merge to main → chart 0.70.0 builds →
image tag 0.70.0 in registry.

No coexistence layer: the PR removes noise code AND old `/cloud/*` paths in
the same change. main branch is briefly chart-version-ahead of what's deployed
in k8s — that's fine; Pulumi controls deploy.

### 9.3 Phase C — Pulumi hard-cut

Sequence:

1. **Pre-step (manual k8s)**: kubectl-apply a cert-manager `Certificate` for
   `x.agent.cs.ac.cn` separately, BEFORE editing Pulumi. Wait for cert to be
   issued (cert-manager logs). Skipping this leaves an HTTPS unavailable window
   between pulumi up and cert issuance.
2. Edit `/root/k8s/stacks/agentserver.ts`:
   - chart version 0.69.5 → 0.70.0
   - all `codexAppGateway.image.tag` / `codexExecGateway.image.tag` etc. → 0.70.0
   - `publicHost: "codex-exec.agent.cs.ac.cn"` → `"x.agent.cs.ac.cn"`
   - HTTPRoute hostname: replace (don't add) old → new
   - delete `noiseRelayEnabled: true`
   - delete `noiseRelayHmacKey` secret reference in `Pulumi.nj-prod.yaml`
3. `pulumi preview` → `pulumi up`.
4. The moment Pulumi applies:
   - Old hostname `codex-exec.agent.cs.ac.cn`: DNS still resolves, but Istio
     Gateway returns 404 (no route). Any in-flight `codex exec-server --remote
     codex-exec...` connection drops.
   - New hostname `x.agent.cs.ac.cn`: live and serving `/agentx/*`.
5. Issue UI banner / email to users: "switched; install agentx; new connect
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
