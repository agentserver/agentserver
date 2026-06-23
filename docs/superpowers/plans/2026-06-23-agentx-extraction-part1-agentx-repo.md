# agentx Extraction — Part 1: agentx Rust Repo (Phase A)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Hard-fork codex `rust-v0.142.0` exec-server subset into a new
self-owned project `agentserver/agentx`. End state: GitHub repo with multi-commit
history, CI/release workflows, and `v0.0.1` release tarballs published.

**Architecture:** Snapshot v0.142.0 source (no upstream git remote), prune to
the dependency closure of `exec-server`, strip Noise / Bedrock / analytics /
all non-`exec-server` subcommands, rename `codex-*` → `agentx-*` and
`CODEX_*` → `AGENTX_*`, expose two env overrides that 0.142 hardcoded, ship
single binary via 4-target GitHub Actions matrix on GitHub-hosted runners.

**Tech Stack:** Rust 1.95 (codex toolchain pin), Cargo workspace, Tokio, Axum,
clatter (Noise — to be deleted), reqwest, clap. CI: GitHub Actions, mlugg/setup-zig
for musl. Release: GitHub Releases tarballs.

**Spec:** `docs/superpowers/specs/2026-06-23-agentx-extraction-design.md` rev 2,
commit `1d1b98a` on branch `spec/agentx-extraction`. Sections §4, §5, §8, §9.1.

**Parts:** This is **Part 1 of 3**.
- Part 1 (this file): Phase A — agentx repo, v0.0.1 release. **Production-safe** — nothing in cs.ac.cn changes.
- Part 2 (`...-part2-agentserver-pr.md`): Phase B — single agentserver PR with C1..C11.
- Part 3 (`...-part3-pulumi-cutover.md`): Phase C — DNS + pulumi hard-cut + Phase D cleanup.

Each part has its own approval gate at the end. Do NOT start the next part
until you've verified the current part lands and the user OKs proceeding.

## Global Constraints

These apply to every task in this Part. They come from the spec (rev 2) and
user-confirmed decisions during brainstorming. Re-derived here so the plan
is self-contained.

- **Base ref**: codex `rust-v0.142.0` only. Do not pull a different tag, do
  not pull `main`. Tag verified to exist (`tag rust-v0.142.0, Date Mon Jun 22
  23:36:01 2026 +0200, Tagger jif`).
- **No upstream git remote**. Repo must NOT have a `remote` named `upstream`
  or `codex` or `openai`. `git remote` should list only `origin`. This is
  hard fork.
- **Repo URL**: `github.com/agentserver/agentx` (must be **manually created**
  via the GitHub "New repository" button; do NOT use the "Fork" button which
  preserves upstream linkage).
- **No Windows builds**. Drop any windows-only crates and any `#[cfg(target_os = "windows")]`-gated CLI subcommands.
- **4 release targets**: `aarch64-apple-darwin`, `x86_64-apple-darwin`,
  `x86_64-unknown-linux-musl`, `aarch64-unknown-linux-musl`. macOS unsigned.
  Linux includes `bwrap` helper. GitHub-hosted runners only.
- **Single binary** `agentx`. NO subcommands — flatten `exec-server` flags to
  top-level (`agentx --remote ...` not `agentx exec-server --remote ...`).
  Drop `--listen` local-mode branch entirely.
- **Auth modes kept**: Agent Identity JWT (default), ChatGPT login, API key.
  Bedrock (`aws-auth` + `CodexAuth::BedrockApiKey`) dropped.
- **Telemetry dropped**: `analytics` crate removed, callers no-op'd.
- **Noise dropped**: `noise*.rs`, `relay*.rs`, `proto/codex.exec_server.relay.v1.proto`,
  ~400 lines of tests under `exec-server/`. `security_profile` field deleted from
  register response. Wire reverts to plaintext JSON-RPC over WS.
- **Naming rename**: crate names `codex-*` → `agentx-*`; env vars `CODEX_*` →
  `AGENTX_*`; default config dir `~/.codex/` → `~/.agentx/`; `CODEX_HOME` env
  → `AGENTX_HOME`.
- **Two new env overrides** added (this is the trigger for the whole project):
  - `AGENTX_AGENT_IDENTITY_ALLOWED_BASE_URLS` (comma-separated) → patched in
    **both** `agentx-agent-identity/src/lib.rs` AND
    `agentx-login/src/auth/agent_identity.rs` (the 0.142 resolver). Env set →
    URL must be in list; env unset → original hardcoded whitelist
    (chatgpt.com / chatgpt-staging.com) preserved.
  - `AGENTX_API_KEY_ALLOWED_HOSTS` → patched in CLI `validate_api_key_remote_host`.
    Same semantics (env set → list; unset → openai.com/openai.org whitelist).
- **One new CLI flag**: `--agent-identity-authapi-base-url` + matching env
  `AGENTX_AGENT_IDENTITY_AUTHAPI_BASE_URL`. Wired into `CodexAuth::from_agent_identity_jwt`
  by adding a fourth public parameter (preferred over exposing the private helper).
- **Versioning**: first release `v0.0.1`. pre-1.0 semver; wire-breaks allowed in minor bumps until 1.0.
- **License**: Apache-2.0 (inherited from codex). Add `NOTICE` declaring derivation from `codex rust-v0.142.0`.
- **CI gates**: `cargo fmt --check`, `cargo clippy -- -D warnings`,
  `cargo test --workspace`, `scripts/verify-no-codex-refs.sh` (with whitelist
  for README/NOTICE/CHANGELOG).
- **Brand strip**: remove OpenAI / ChatGPT mentions in user-facing help text
  and error messages. **Keep** technical URLs like
  `https://chatgpt.com/codex-backend/agent-identity` and
  `https://auth.openai.com/api/accounts` — these are real infrastructure
  endpoints baked into the default whitelist branch (these are NOT branding;
  they're the legacy upstream issuer/authapi that the unset-env fallback
  preserves for zero-config use).

## File Structure (post-Phase A)

```
agentserver/agentx/                         # NEW REPO
├── Cargo.toml                              # workspace root, members = kept-set only
├── Cargo.lock                              # inherited from codex v0.142.0
├── README.md                               # rewritten, no OpenAI brand
├── LICENSE                                 # Apache-2.0 (copied from codex)
├── NOTICE                                  # declares derivation from codex rust-v0.142.0
├── CHANGELOG.md                            # 0.0.1: initial release
├── rust-toolchain.toml                     # 1.95.0 (copied from codex)
├── clippy.toml                             # copied from codex
├── .gitignore                              # copied from codex
├── .github/
│   ├── workflows/
│   │   ├── ci.yml                          # fmt + clippy + test + verify-no-codex-refs
│   │   └── release.yml                     # 4 targets → tarballs → GH Releases
│   └── scripts/
│       └── install-musl-build-tools.sh     # copied from codex
├── scripts/
│   ├── install.sh                          # curl|sh installer for users
│   └── verify-no-codex-refs.sh             # CI gate
└── crates/                                 # one dir per kept crate (see Task 4)
    ├── agentx-cli/                         # was cli/   — slimmed to single binary
    ├── agentx-exec-server/                 # was exec-server/   — noise removed
    ├── agentx-agent-identity/              # was agent-identity/   — env override
    ├── agentx-login/                       # was login/   — env override + Bedrock dropped
    ├── agentx-app-server-protocol/         # was app-server-protocol/
    ├── agentx-app-server-transport/        # was app-server-transport/
    ├── agentx-api/                         # was codex-api/
    ├── agentx-client/                      # was codex-client/
    ├── agentx-protocol/                    # was protocol/
    ├── agentx-file-system/                 # was file-system/
    ├── agentx-sandboxing/                  # was sandboxing/
    ├── agentx-shell-command/               # was shell-command/
    ├── agentx-arg0/                        # was arg0/
    ├── agentx-async-utils/                 # was async-utils/
    ├── agentx-bwrap/                       # was bwrap/  (Linux sandbox helper)
    └── agentx-utils-*/                     # was utils/*/  (pty, path-uri, absolute-path, rustls-provider, …)
```

> **Note on "kept set"**: The list above is the **starting hypothesis** from
> spec §4. Reality is determined empirically — `cargo check` will reveal
> transitive dependencies that we missed (e.g. cli pulls
> `codex-config` / `codex-keyring-store` / `codex-otel` / etc., login pulls
> `codex-terminal-detection` / `codex-utils-template`, etc.). Tasks below
> tell you when to expand the kept-set rather than locking it down upfront.

## Working directory

All Part 1 work happens **outside** the agentserver repo, in a fresh scratch
directory. Suggested:

```bash
mkdir -p /root/agentx-work && cd /root/agentx-work
```

Do NOT do any of this inside `/root/agentserver` — it would dirty the worktree
mid-spec.

You also need a clean checkout of codex at the tag, used only as a source for
`cp -r`:

```bash
# Will be done in Task 1
git clone --depth 1 --branch rust-v0.142.0 git@github.com:openai/codex.git /root/agentx-work/codex-snapshot
```

---

### Task 1: Bootstrap workspace + codex snapshot

**Files:**
- Create: `/root/agentx-work/agentx/` (empty git repo)
- Create: `/root/agentx-work/codex-snapshot/` (cloned at tag, source for `cp -r`)
- Create: `/root/agentx-work/agentx/.git/config` will end up with only `origin`

**Interfaces:** none (bootstrap task)

- [ ] **Step 1: Create scratch workspace**

```bash
mkdir -p /root/agentx-work
cd /root/agentx-work
```

- [ ] **Step 2: Clone codex at the exact tag**

```bash
git clone --depth 1 --branch rust-v0.142.0 \
  git@github.com:openai/codex.git \
  /root/agentx-work/codex-snapshot
cd /root/agentx-work/codex-snapshot
git describe --tags
```

Expected: `rust-v0.142.0`.

- [ ] **Step 3: Create the agentx git repo locally (no remote yet)**

```bash
mkdir -p /root/agentx-work/agentx
cd /root/agentx-work/agentx
git init -b main
git config user.email "agentx-bot@agentserver"
git config user.name "agentx bootstrap"
```

- [ ] **Step 4: Verify no upstream-like remote was added by mistake**

```bash
git remote
```

Expected: **empty output**. (origin gets added in Task 16 after GitHub repo is created.)

- [ ] **Step 5: Commit nothing yet — just stash the env**

Print a one-line note for the next task. No git commit at this step.

```bash
echo "Bootstrap done. Snapshot at $(git -C /root/agentx-work/codex-snapshot rev-parse HEAD)"
```

---

### Task 2: Import commit — copy kept crates verbatim

**Files:**
- Create: `/root/agentx-work/agentx/crates/{cli,exec-server,agent-identity,login,app-server-protocol,app-server-transport,codex-api,codex-client,protocol,file-system,sandboxing,shell-command,arg0,async-utils,bwrap}` and `utils/{absolute-path,path-uri,pty,rustls-provider}`
- Create: `/root/agentx-work/agentx/{Cargo.toml,Cargo.lock,LICENSE,rust-toolchain.toml,clippy.toml,.gitignore}`
- Create: `/root/agentx-work/agentx/.github/scripts/install-musl-build-tools.sh` (copied; release.yml will reference)

**Interfaces:**
- Produces: a non-building tree (expected) — references many dropped crates. Subsequent tasks (3+) prune those refs.

> Crate dirs are copied with **original codex names** (e.g. `cli/`, not
> `agentx-cli/`). Rename happens in Task 10 after the tree builds. This keeps
> early commits diff-friendly against the upstream snapshot.

- [ ] **Step 1: Copy workspace meta files**

```bash
cd /root/agentx-work/agentx
SRC=/root/agentx-work/codex-snapshot/codex-rs

cp -p $SRC/Cargo.toml .
cp -p $SRC/Cargo.lock .
cp -p $SRC/rust-toolchain.toml . 2>/dev/null || true   # may not exist
cp -p $SRC/clippy.toml .
cp -p /root/agentx-work/codex-snapshot/LICENSE .
cp -p /root/agentx-work/codex-snapshot/.gitignore .
mkdir -p .github/scripts
cp -p /root/agentx-work/codex-snapshot/.github/scripts/install-musl-build-tools.sh .github/scripts/
```

- [ ] **Step 2: Copy the starting kept-set crate dirs**

```bash
mkdir -p crates utils
for c in cli exec-server agent-identity login \
         app-server-protocol app-server-transport \
         codex-api codex-client protocol \
         file-system sandboxing shell-command \
         arg0 async-utils bwrap; do
  cp -rp $SRC/$c crates/$c
done
for u in absolute-path path-uri pty rustls-provider; do
  cp -rp $SRC/utils/$u utils/$u
done
```

- [ ] **Step 3: Verify the codex-snapshot's own .git did NOT leak in**

```bash
find . -name .git -not -path './.git/*' -prune
```

Expected: no output (only the top-level `./.git` exists).

- [ ] **Step 4: Stage and commit the import**

```bash
git add -A
git commit -m "Import codex rust-v0.142.0 subset

Snapshot of openai/codex@rust-v0.142.0 (commit $(git -C /root/agentx-work/codex-snapshot rev-parse HEAD)).
Kept crates: cli, exec-server, agent-identity, login, app-server-protocol,
app-server-transport, codex-api, codex-client, protocol, file-system,
sandboxing, shell-command, arg0, async-utils, bwrap, utils/{absolute-path,
path-uri, pty, rustls-provider}.

This commit does NOT build yet — workspace Cargo.toml still references
dropped crates. Cleanup commits follow."
```

- [ ] **Step 5: Confirm — this is the only commit**

```bash
git log --oneline
```

Expected: 1 line, hash + import message.

---

### Task 3: Workspace Cargo.toml cleanup — shrink members + workspace deps

**Files:**
- Modify: `Cargo.toml` (workspace root)
- Modify: `crates/cli/Cargo.toml` (delete unused crate deps that will be dropped)

**Interfaces:**
- Produces: A workspace whose `members =` lists only kept crates + utils. `[workspace.dependencies]` table shrunk accordingly. `cargo check` still fails (other crates' source still refs dropped types) — that's Task 4–7.

- [ ] **Step 1: Edit `Cargo.toml` workspace members**

Replace the existing `members = [ ... ]` array (lines ~2–125) with the kept set:

```toml
members = [
    "crates/cli",
    "crates/exec-server",
    "crates/agent-identity",
    "crates/login",
    "crates/app-server-protocol",
    "crates/app-server-transport",
    "crates/codex-api",
    "crates/codex-client",
    "crates/protocol",
    "crates/file-system",
    "crates/sandboxing",
    "crates/shell-command",
    "crates/arg0",
    "crates/async-utils",
    "crates/bwrap",
    "utils/absolute-path",
    "utils/path-uri",
    "utils/pty",
    "utils/rustls-provider",
]
```

(All paths are now under `crates/` and `utils/` since we copied them there in
Task 2.)

- [ ] **Step 2: Shrink `[workspace.dependencies]`**

In the same `Cargo.toml`, locate the `[workspace.dependencies]` section and
delete every `codex-*` entry that does NOT correspond to a kept crate. Keep
ONLY these `codex-*` entries:

```toml
codex-agent-identity = { path = "crates/agent-identity" }
codex-api = { path = "crates/codex-api" }
codex-app-server-protocol = { path = "crates/app-server-protocol" }
codex-app-server-transport = { path = "crates/app-server-transport" }
codex-arg0 = { path = "crates/arg0" }
codex-async-utils = { path = "crates/async-utils" }
codex-bwrap = { path = "crates/bwrap" }
codex-client = { path = "crates/codex-client" }
codex-exec-server = { path = "crates/exec-server" }
codex-file-system = { path = "crates/file-system" }
codex-login = { path = "crates/login" }
codex-protocol = { path = "crates/protocol" }
codex-sandboxing = { path = "crates/sandboxing" }
codex-shell-command = { path = "crates/shell-command" }
codex-utils-absolute-path = { path = "utils/absolute-path" }
codex-utils-path-uri = { path = "utils/path-uri" }
codex-utils-pty = { path = "utils/pty" }
codex-utils-rustls-provider = { path = "utils/rustls-provider" }
```

Delete every other `codex-*` workspace.dependency line. Leave non-codex
entries (tokio, axum, serde, etc.) untouched.

- [ ] **Step 3: Shrink `crates/cli/Cargo.toml` deps**

Open `crates/cli/Cargo.toml` and delete every dep line whose crate name is
NOT in the kept set above. Specifically delete:

```toml
codex-app-server = { workspace = true }
codex-app-server-daemon = { workspace = true }
codex-app-server-test-client = { workspace = true }
codex-chatgpt = { workspace = true }
codex-cloud-tasks = { path = "../cloud-tasks" }
codex-config = { workspace = true }
codex-core = { workspace = true }
codex-core-plugins = { workspace = true }
codex-home = { workspace = true }
codex-exec = { workspace = true }
codex-execpolicy = { workspace = true }
codex-features = { workspace = true }
codex-git-utils = { workspace = true }
codex-install-context = { workspace = true }
codex-memories-write = { workspace = true }
codex-mcp = { workspace = true }
codex-mcp-server = { workspace = true }
codex-model-provider = { workspace = true }
codex-models-manager = { workspace = true }
codex-otel = { workspace = true }
codex-plugin = { workspace = true }
codex-skills = { workspace = true }
codex-rmcp-client = { workspace = true }
codex-rollout = { workspace = true }
codex-tui = { workspace = true }
codex-utils-cli = { workspace = true }
```

(Anything `codex-` that isn't in the kept set from Step 2.)

- [ ] **Step 4: `cargo check` — observe expected failures**

```bash
cd /root/agentx-work/agentx
cargo check 2>&1 | tail -40
```

Expected: dozens of errors, mostly from `crates/cli/src/main.rs` using types
from now-deleted deps (`codex_tui::`, `codex_cloud_tasks::`, etc.) AND from
other kept crates pulling things via `workspace.dependencies` that no longer
exist. **This is expected and resolved in Tasks 4–7.**

- [ ] **Step 5: Commit**

```bash
git add Cargo.toml crates/cli/Cargo.toml
git commit -m "chore: prune workspace and cli deps to kept-set

Members reduced from 124 → 19. crates/cli/Cargo.toml shrunk by ~25
codex-* entries. Subsequent commits clean up src/ to match."
```

---

### Task 4: Prune CLI Subcommand enum — drop everything except exec-server

**Files:**
- Modify: `crates/cli/src/main.rs` (Subcommand enum at lines 123–212 in v0.142.0; dispatch block; ExecServer flags)
- Modify: `crates/cli/src/main.rs` `--listen` branch (delete)
- Possibly delete: `crates/cli/src/*.rs` files that are pure subcommand impls for dropped subcommands (login.rs, cloud.rs, mcp.rs, app_cmd.rs, debug.rs, …)

**Interfaces:**
- Produces: a CLI binary whose `clap::Parser` accepts only the flags from `ExecServerCommand`, flattened to top-level. Local-mode `--listen` is gone. After this task `cargo check -p codex-cli` should succeed (modulo Bedrock + analytics + noise refs still pending).

- [ ] **Step 1: Skim current `Subcommand` enum to confirm baseline**

```bash
cd /root/agentx-work/agentx
sed -n '120,220p' crates/cli/src/main.rs
```

Expected: enum `Subcommand` with ~22 variants (Exec, Review, Login, Logout,
Mcp, Plugin, McpServer, AppServer, RemoteControl, App, Completion, Update,
Doctor, Sandbox, Debug, Execpolicy, Apply, Resume, Archive, Delete,
Unarchive, Fork, Cloud, ResponsesApiProxy, StdioToUds, ExecServer, Features).

- [ ] **Step 2: Replace `Subcommand` enum + top-level parser to flatten ExecServer flags**

The goal: invoking `agentx --remote URL --environment-id ID ...` works
directly, no subcommand. Edit `crates/cli/src/main.rs`:

1. Delete the entire `enum Subcommand { ... }` block (all variants).
2. Find the top-level `MultitoolCli` (or whatever the v0.142.0 parser struct
   is named — confirm with `grep -n '^struct.*Cli' crates/cli/src/main.rs`).
3. Flatten the `ExecServerCommand` fields directly into it via `#[clap(flatten)]`:

```rust
#[derive(Debug, Parser)]
#[clap(name = "agentx", about = "Remote process / fs executor (forked from codex exec-server)")]
struct AgentxCli {
    #[clap(flatten)]
    config: CommonConfigArgs,   // keep whatever shared config-overrides struct existed

    #[clap(flatten)]
    exec: ExecServerCommand,    // the existing struct from the ExecServer subcommand
}
```

   Adjust `CommonConfigArgs` import path; if there is none, drop the line.

4. In `fn main()`, remove the entire `match cli.subcommand { ... }` block.
   Replace with a direct call to the exec-server dispatch (the body of the
   `Some(Subcommand::ExecServer(cmd)) => run_exec_server(...)` arm). Trace
   the call into `run_exec_server` in v0.142.0 and inline its prerequisites
   (config loading, arg0 paths).

5. Delete `--listen` branch in the resulting dispatch:

   ```rust
   if let Some(base_url) = cmd.remote {
       // keep this branch
   } else {
       // DELETE this entire else-branch (local-mode --listen)
   }
   ```

   After: `--remote` becomes required. Update the clap attribute on the
   `remote` field to `required = true`.

- [ ] **Step 3: Delete now-unreachable subcommand impl files**

```bash
cd crates/cli/src
# These exist in v0.142.0; verify before rm:
ls login.rs cloud.rs mcp_cmd.rs plugin_cmd.rs app_cmd.rs debug_cmd.rs \
   execpolicy_cmd.rs apply_cmd.rs resume_cmd.rs review_cmd.rs \
   completion_cmd.rs update_cmd.rs doctor_cmd.rs sandbox_cmd.rs \
   features_cmd.rs responses_api_proxy.rs stdio_to_uds.rs 2>/dev/null
```

For each file confirmed present, `git rm` it AND delete any matching
`mod ...;` line from `crates/cli/src/lib.rs` (or `main.rs` if mods are
declared there).

```bash
# After confirming each exists, then for each:
git rm crates/cli/src/<file>.rs
sed -i '/^mod <stem>;/d' crates/cli/src/lib.rs   # or main.rs
```

- [ ] **Step 4: `cargo check -p codex-cli` — expect remaining noise/Bedrock/analytics errors only**

```bash
cargo check -p codex-cli 2>&1 | tail -30
```

Expected errors should mention things still living in `exec-server`, `login`,
or noise modules — NOT missing `codex_tui::*` or `codex_cloud_tasks::*`
anymore. If you still see those, you missed an import.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor(cli): drop all subcommands except exec-server; flatten to top-level

Subcommand enum (22 variants) removed. Top-level parser AgentxCli flattens
ExecServerCommand fields so 'agentx --remote URL ...' works without a
subcommand. --listen local-mode branch deleted; --remote is now required.
Subcommand impl files (login.rs, cloud.rs, mcp_cmd.rs, ...) removed."
```

---

### Task 5: Drop Bedrock auth path

**Files:**
- Modify: `crates/login/src/auth/manager.rs` — `CodexAuth::BedrockApiKey` variant + all match arms
- Delete: `crates/login/src/auth/bedrock_api_key.rs` (if it exists; verify with `ls`)
- Modify: `crates/cli/src/main.rs` — any remaining `AuthMode::BedrockApiKey` dispatch
- Modify: `crates/login/src/auth/mod.rs` or equivalent — `pub mod bedrock_api_key;` removal

**Interfaces:**
- Consumes: working tree from Task 4 (CLI flattened).
- Produces: A `CodexAuth` enum with 5 variants (down from 6); `cargo check`
  errors related to Bedrock are gone.

- [ ] **Step 1: Locate `CodexAuth` enum**

```bash
grep -rn "pub enum CodexAuth" crates/login/src
```

Confirms file & line. Expected: `crates/login/src/auth/manager.rs:~69-76`.

- [ ] **Step 2: Delete the `BedrockApiKey` variant**

Edit `crates/login/src/auth/manager.rs`:

```rust
pub enum CodexAuth {
    ApiKey(ApiKeyAuth),
    Chatgpt(ChatgptAuth),
    ChatgptAuthTokens(ChatgptAuthTokens),
    AgentIdentity(AgentIdentityAuth),
    PersonalAccessToken(PersonalAccessTokenAuth),
    // DELETED: BedrockApiKey(BedrockApiKeyAuth),
}
```

- [ ] **Step 3: Run `cargo check -p codex-login` and let the compiler list the broken match arms**

```bash
cargo check -p codex-login 2>&1 | grep -E "non-exhaustive|BedrockApiKey" | head -30
```

For each `match` expression the compiler complains about (expect ~6 sites
in `manager.rs`: pattern guard at ~line 91, `is_api_key_auth()` at ~143,
`auth_mode()`, `api_auth_mode()`, debug Display, etc.):

- Delete the `Self::BedrockApiKey(_)` arm.
- If the arm carried distinct semantics (e.g. lumped together with
  `Self::ApiKey(_)` via `|`), just remove the `| Self::BedrockApiKey(_)`
  alternation.

Iterate until `cargo check -p codex-login` is silent on Bedrock.

- [ ] **Step 4: Delete the Bedrock auth module file**

```bash
ls crates/login/src/auth/bedrock_api_key.rs && git rm crates/login/src/auth/bedrock_api_key.rs
```

Edit `crates/login/src/auth/mod.rs` (or wherever `pub mod bedrock_api_key;`
is declared):

```bash
grep -rn "bedrock_api_key" crates/login/src
# delete any `mod bedrock_api_key;` / `pub mod bedrock_api_key;` / `use ... bedrock_api_key::*`
```

- [ ] **Step 5: Search CLI for remaining Bedrock dispatch**

```bash
grep -rn "Bedrock\|bedrock" crates/cli/src
```

For each hit: if it's an `AuthMode::BedrockApiKey` arm or `--bedrock-*`
CLI flag, delete it. Bedrock is now entirely out.

- [ ] **Step 6: `cargo check --workspace`**

```bash
cargo check --workspace 2>&1 | tail -30
```

Bedrock-related errors should be gone. Other errors (noise, analytics, broken
tests) remain — handled by following tasks.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "refactor: drop Bedrock auth path

Delete CodexAuth::BedrockApiKey variant and all match arms across login
and cli. Delete login/src/auth/bedrock_api_key.rs and its module
declaration. agentserver does not route Bedrock traffic."
```

---

### Task 6: Drop analytics

**Files:**
- Modify: any kept crate that imports `codex_analytics::*` (audit via grep)
- The workspace member `analytics` was already removed in Task 3, but call
  sites in kept crates still reference it.

**Interfaces:**
- Produces: kept crates compile without any reference to `codex_analytics::`.

- [ ] **Step 1: Inventory analytics usage**

```bash
grep -rn "codex_analytics\|codex-analytics\|use analytics\|analytics::" \
  crates/ utils/ 2>/dev/null
```

For each result, classify:
- Import statements (`use codex_analytics::Foo;`) → delete.
- Function calls (`analytics::record_event(...)`) → delete the call site
  if it's pure side-effect telemetry (no return value used); replace with
  a no-op `()` if the call is in an expression position requiring a value.
- Type uses (`AnalyticsHandle` in struct fields) → delete the field; trace
  the field's reads/writes and delete those too.

- [ ] **Step 2: Apply deletions site by site, run `cargo check --workspace` after each crate**

After all sites cleaned, build should succeed for analytics-related parts.

```bash
cargo check --workspace 2>&1 | grep -iE "analytics" | head -10
```

Expected: empty.

- [ ] **Step 3: Commit**

```bash
git add -A
git commit -m "refactor: drop analytics

Remove all codex_analytics imports and call sites from kept crates.
The analytics workspace member was removed in the workspace cleanup
commit. No telemetry to OpenAI."
```

---

### Task 7: Drop Noise relay + RelayMessageFrame + security_profile

**Files:**
- Delete: `crates/exec-server/src/noise*.rs`, `crates/exec-server/src/relay*.rs`, `crates/exec-server/src/proto/codex.exec_server.relay.v1.proto`, all `crates/exec-server/{src,tests}/*noise*` test files, all `crates/exec-server/{src,tests}/*relay*` test files
- Modify: `crates/exec-server/src/lib.rs` — delete `mod noise*;`, `mod relay*;`, any `pub use noise::*;`
- Modify: `crates/exec-server/build.rs` — if it invokes `prost-build` for the relay proto, delete that block
- Modify: register response struct (find with grep below) — delete `security_profile` field

**Interfaces:**
- Produces: exec-server speaks plaintext JSON-RPC on `--remote` WS. No
  RelayMessageFrame wrapping. No Noise handshake.

- [ ] **Step 1: Inventory noise/relay files**

```bash
cd /root/agentx-work/agentx
find crates/exec-server -type f \( -name '*noise*' -o -name '*relay*' \)
```

Capture this list. Expected: handshake.rs / responder.rs / suite.rs /
symmetric.rs / transport.rs / identity.rs / prologue.rs /
jsonrpc_framing.rs (or whatever the noise dir contains in v0.142.0), plus
the proto file under src/proto/, plus tests under src/ and tests/.

- [ ] **Step 2: Delete the noise/relay files**

```bash
# Run from previous step's list. Then:
git rm -r crates/exec-server/src/noise crates/exec-server/src/relay* \
         crates/exec-server/src/proto/codex.exec_server.relay.v1.proto \
         crates/exec-server/tests/*noise* crates/exec-server/tests/*relay* \
         crates/exec-server/src/*noise*_tests.rs 2>/dev/null
# Adjust paths to match actual inventory from Step 1.
```

- [ ] **Step 3: Remove `mod` declarations in `crates/exec-server/src/lib.rs`**

```bash
grep -n "mod noise\|mod relay\|pub use noise\|pub use relay" crates/exec-server/src/lib.rs
```

Delete all matching lines.

- [ ] **Step 4: Edit `build.rs` if it generated the relay proto**

```bash
cat crates/exec-server/build.rs 2>/dev/null
```

If present and it invokes `prost_build` on the relay .proto, delete the
proto-build block; if `build.rs` becomes empty other than `fn main() {}`,
keep the stub or delete the file (and the `build = "build.rs"` line in
`Cargo.toml`).

- [ ] **Step 5: Delete `clatter` dependency from exec-server**

```bash
grep -n "clatter" crates/exec-server/Cargo.toml
```

Delete the `clatter = { workspace = true }` line. Also from workspace root's
`[workspace.dependencies]` if no other crate uses it.

- [ ] **Step 6: Delete `security_profile` field from register response**

```bash
grep -rn "security_profile\|securityProfile\|SecurityProfile" crates/exec-server/src
```

For each hit:
- In the response struct (`RegisterResponse` or similar), delete the field
  and its derive-serde rename if any.
- In code that *reads* `response.security_profile` (decoding upstream
  responses), delete the field from the deserialization struct too.
- In code that *populates* it on outgoing requests, delete that too.

Expected: when done, `grep -rn security_profile crates/` is empty.

- [ ] **Step 7: `cargo check --workspace`**

```bash
cargo check --workspace 2>&1 | tail -40
```

Expect possible errors from kept crates that still try to use
RelayMessageFrame types (e.g. `app-server-transport`, `codex-api`,
`codex-client`). Walk those refs and delete them too — verify the kept
crates don't *need* relay framing (they don't; that was a noise-only
concern). The wire is plaintext JSON-RPC after this.

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "refactor: drop noise relay + RelayMessageFrame + security_profile

Delete noise hybrid IK handshake (~400 LOC across exec-server/src/noise*
and exec-server/{src,tests}/*noise*_tests.rs), RelayMessageFrame protobuf
and its prost-build wiring, and security_profile field from register
response struct.

agentx wire is plaintext JSON-RPC over WebSocket. Both endpoints
(agentx client, codex-exec-gateway server) are operated by us; E2E noise
encryption is overhead with no threat-model benefit.

Removes clatter dep."
```

---

### Task 8: First green build — confirm workspace builds end-to-end

**Files:** none (verification task)

**Interfaces:**
- Consumes: state after Tasks 4–7.
- Produces: a workspace where `cargo build --workspace` succeeds. Tests may
  still fail (next tasks fix). This is the "buildable kept-set" milestone.

- [ ] **Step 1: Build the workspace**

```bash
cd /root/agentx-work/agentx
cargo build --workspace 2>&1 | tail -50
```

Expected: a green build (warnings OK; no errors).

- [ ] **Step 2: If errors remain**

Common residuals at this point:
- A kept crate that transitively depends on a dropped crate via
  `workspace.dependencies`. Solution: either re-add the dropped crate to
  the kept-set (expand `members` in `Cargo.toml` + copy the dir from
  `codex-snapshot`), or prune the dep from the kept crate.
- A `use` import pointing at a deleted module — delete it.

Iterate. Run `cargo build --workspace` after each fix. When green, move on.

- [ ] **Step 3: Commit any incremental fixes from Step 2**

```bash
git add -A
git status
git commit -m "chore: expand kept-set to include transitively-required crates

[List the crates added in this iteration, with one-line reason each.]"
```

If no fixes were needed in Step 2, skip this commit.

- [ ] **Step 4: Snapshot the working kept-set**

```bash
ls crates/ utils/
git log --oneline
```

Record the kept-set in a project memo so later tasks know what's in scope.

---

### Task 9: Run upstream tests; delete broken noise/Bedrock/analytics tests

**Files:**
- Delete: test files in kept crates that exercise deleted functionality
- Modify: test files in kept crates that ASSERT on Bedrock/noise variants

**Interfaces:**
- Produces: `cargo test --workspace` is green. (Or skipped with a doc comment if a test exercises behaviour we no longer support but should — flag those for human review.)

- [ ] **Step 1: First test run**

```bash
cargo test --workspace --no-fail-fast 2>&1 | tail -60
```

Capture the list of failing tests.

- [ ] **Step 2: Classify each failing test**

For each failure:
- **Tests against deleted functionality** (Bedrock, noise, dropped subcommand):
  delete the test file or test function.
- **Tests that match exhaustively on `CodexAuth` variants**: update the
  match to drop the deleted arm.
- **Tests that constructed a `BedrockApiKey` value**: delete or rewrite to
  use a different variant.
- **Tests that asserted `security_profile` was set/unset on register
  response**: delete; the field no longer exists.
- **Tests that needed `--listen` local mode**: delete; we removed that branch.

- [ ] **Step 3: Re-run tests until green**

```bash
cargo test --workspace --no-fail-fast 2>&1 | grep -E "^(test result|FAILED|---- )" | head -40
```

Iterate.

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "test: drop / update tests broken by Bedrock + noise + listen-mode removal

[Optional: list the most consequential test deletions and any rewrites
done to preserve coverage of kept behaviour.]"
```

---

### Task 10: Mechanical rename — crate names `codex-*` → `agentx-*`

**Files:**
- Modify: every `Cargo.toml` in `crates/` and `utils/` (`name = "codex-foo"` → `"agentx-foo"`)
- Modify: workspace root `Cargo.toml` (`[workspace.dependencies] codex-foo = { path = "..." }` → `agentx-foo`)
- Modify: every Rust file (`use codex_foo` → `use agentx_foo`, `extern crate codex_foo` → `agentx_foo`)
- Rename: directories `crates/codex-api/` → `crates/agentx-api/`, `crates/codex-client/` → `crates/agentx-client/` (the only two dirs whose name starts with `codex-`)

**Interfaces:**
- Produces: crate names all use `agentx-` prefix. `cargo build --workspace` and `cargo test --workspace` still green.

- [ ] **Step 1: Dump pre-rename crate inventory for diffing**

```bash
cd /root/agentx-work/agentx
find crates utils -name Cargo.toml -exec grep '^name = ' {} \; | sort > /tmp/agentx-pre-rename.txt
cat /tmp/agentx-pre-rename.txt
```

- [ ] **Step 2: sed-rename `name` field in all crate Cargo.toml files**

```bash
find crates utils -name Cargo.toml -exec sed -i 's/^name = "codex-/name = "agentx-/' {} +
find crates utils -name Cargo.toml -exec sed -i 's/^name = "codex_/name = "agentx_/' {} +
```

(Second line covers the rare crate with underscore-style name.)

- [ ] **Step 3: sed-rename workspace.dependencies + dep references**

```bash
# In root Cargo.toml and every crate Cargo.toml:
find . -name Cargo.toml -not -path './.git/*' -exec sed -i \
  -e 's/^codex-\([a-z0-9-]*\) = /agentx-\1 = /' \
  -e 's/path = "crates\/codex-/path = "crates\/agentx-/' \
  -e 's/^codex-\([a-z0-9-]*\) = { /agentx-\1 = { /' {} +
```

Also rename dep KEYS that are referenced from within `[dependencies]`
sections of individual crates. The above handles the common case
(`codex-foo = { workspace = true }`); if you find any oddly-quoted forms,
edit those manually.

- [ ] **Step 4: Rename the two crate directories whose name starts with `codex-`**

```bash
git mv crates/codex-api crates/agentx-api
git mv crates/codex-client crates/agentx-client
```

Then update path references for these two in `Cargo.toml`:

```bash
sed -i 's|path = "crates/codex-api"|path = "crates/agentx-api"|g' Cargo.toml
sed -i 's|path = "crates/codex-client"|path = "crates/agentx-client"|g' Cargo.toml
```

(Other crates' paths in `Cargo.toml` already point at non-`codex-` dirs
like `crates/exec-server/`; they don't need rename, only the two above.)

- [ ] **Step 5: sed-rename Rust import paths**

```bash
# Rust uses underscores in module paths. Replace 'use codex_foo' → 'use agentx_foo'
# and 'extern crate codex_foo' → 'extern crate agentx_foo':
find crates utils -name '*.rs' -exec sed -i \
  -e 's/use codex_/use agentx_/g' \
  -e 's/extern crate codex_/extern crate agentx_/g' \
  -e 's/codex_\([a-z0-9_]*\)::/agentx_\1::/g' {} +
```

The third pattern catches `codex_foo::bar` mid-expression too. Risk: it
also matches `codex_foo` substrings inside strings or comments — that's
OK for non-string content; for strings we'll do a manual pass in Task 11.

- [ ] **Step 6: Build + test**

```bash
cargo build --workspace 2>&1 | tail -20
cargo test --workspace --no-fail-fast 2>&1 | grep -E "^test result" | head
```

Expected: green on both. If errors mention missing `agentx_X` types,
locate the orphan `codex_X` reference (likely an oddly-formatted import the
sed missed) and patch manually.

- [ ] **Step 7: Final post-rename inventory diff**

```bash
find crates utils -name Cargo.toml -exec grep '^name = ' {} \; | sort > /tmp/agentx-post-rename.txt
diff /tmp/agentx-pre-rename.txt /tmp/agentx-post-rename.txt
```

Every line should show `codex-foo` → `agentx-foo`. No stragglers.

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "chore: rename crates codex-* → agentx-*

Mechanical sed pass over Cargo.toml name fields, workspace.dependencies,
dep references in each crate's Cargo.toml, and Rust source imports
(use codex_x::, extern crate codex_x). Two crate dirs renamed:
crates/codex-api → crates/agentx-api, crates/codex-client →
crates/agentx-client.

Build + test green."
```

---

### Task 11: Mechanical rename — env vars `CODEX_*` → `AGENTX_*`

**Files:**
- Modify: every `.rs` file in `crates/` and `utils/` that reads `std::env::var("CODEX_...")` or writes literal `"CODEX_FOO"` env names
- Modify: test fixtures that set `CODEX_*` env

**Interfaces:**
- Produces: agentx reads/writes `AGENTX_*` env vars exclusively. Build + test green.

- [ ] **Step 1: Inventory `CODEX_*` references**

```bash
cd /root/agentx-work/agentx
grep -rnE '"CODEX_[A-Z_]+"' crates/ utils/ | wc -l
grep -rnE 'env::var\("CODEX_' crates/ utils/ | head -20
```

- [ ] **Step 2: sed-rename literal env names**

```bash
find crates utils -name '*.rs' -exec sed -i 's/"CODEX_\([A-Z_]*\)"/"AGENTX_\1"/g' {} +
```

- [ ] **Step 3: Build + test**

```bash
cargo build --workspace 2>&1 | tail -10
cargo test --workspace --no-fail-fast 2>&1 | grep "^test result" | head
```

If tests fail because a test fixture sets `CODEX_HOME=/tmp/...` and the
production code now reads `AGENTX_HOME`, fix the fixture too.

- [ ] **Step 4: Manual review pass over comments**

```bash
grep -rn "CODEX_" crates/ utils/ | grep -vE 'codex_(api|client|home|protocol)' | head
```

Hits are likely in `//` comments or docstrings. Edit those manually to
keep prose coherent. Don't sed comments — risk of mangling URLs like
`https://chatgpt.com/codex-backend/...`.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "chore: rename CODEX_* env vars → AGENTX_*

Sed pass over Rust string literals; manual review of comment prose.
Test fixtures updated to set AGENTX_HOME etc. Build + test green."
```

---

### Task 12: Mechanical rename — `~/.codex/` → `~/.agentx/` + `CODEX_HOME` consistency

**Files:**
- Modify: every `.rs` file containing literal `.codex` path strings, dotfile names, or default home-dir computation
- Modify: `crates/agentx-utils-home-dir/` (if kept in dep closure; otherwise the home-dir helper inside `agentx-cli` or `agentx-login`)

**Interfaces:**
- Produces: default config / auth.json / sessions / etc. live under `~/.agentx/`.

- [ ] **Step 1: Inventory `.codex` path strings**

```bash
grep -rnE '"\.codex"|"\.codex/|/\.codex/|/\.codex"' crates/ utils/ | head -20
```

- [ ] **Step 2: sed-rename `.codex` → `.agentx`**

```bash
find crates utils -name '*.rs' -exec sed -i \
  -e 's|"\.codex"|".agentx"|g' \
  -e 's|"\.codex/|".agentx/|g' \
  -e 's|/\.codex/|/.agentx/|g' \
  -e 's|/\.codex"|/.agentx"|g' {} +
```

- [ ] **Step 3: Build + test**

```bash
cargo build --workspace 2>&1 | tail -10
cargo test --workspace --no-fail-fast 2>&1 | grep "^test result" | head
```

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "chore: rename ~/.codex → ~/.agentx default home dir

CODEX_HOME env was already renamed AGENTX_HOME in earlier commit. This
also renames the default fallback dir."
```

---

### Task 13: Feature — `AGENTX_AGENT_IDENTITY_ALLOWED_BASE_URLS` env override (TWO call sites)

**Files:**
- Modify: `crates/agentx-agent-identity/src/lib.rs` lines 53–70 (the function `ChatGptEnvironment::from_chatgpt_base_url`)
- Modify: `crates/agentx-login/src/auth/agent_identity.rs` lines 28–37 (the helper `agent_identity_authapi_base_url`, added in 0.142)

**Interfaces:**
- Consumes: A `chatgpt_base_url: &str` URL.
- Produces: Behaviour change — env `AGENTX_AGENT_IDENTITY_ALLOWED_BASE_URLS`
  (comma-separated) if set, the URL passes iff it's in the list. If env
  unset, fall back to original hardcoded whitelist.

- [ ] **Step 1: Write the failing tests (both crates)**

In `crates/agentx-agent-identity/src/lib.rs`, append to the existing tests
module:

```rust
#[test]
fn from_chatgpt_base_url_respects_env_allowlist() {
    // Save + restore env to avoid test interference.
    let prev = std::env::var("AGENTX_AGENT_IDENTITY_ALLOWED_BASE_URLS").ok();

    // 1) URL in the env list → Ok(Production by convention).
    unsafe { std::env::set_var(
        "AGENTX_AGENT_IDENTITY_ALLOWED_BASE_URLS",
        "https://codex-auth.agent.cs.ac.cn,https://other.example",
    ); }
    assert!(
        ChatGptEnvironment::from_chatgpt_base_url("https://codex-auth.agent.cs.ac.cn").is_ok(),
        "URL in env list should pass"
    );

    // 2) URL not in env list → Err (we don't punch through to hardcoded list when env is set).
    assert!(
        ChatGptEnvironment::from_chatgpt_base_url("https://chatgpt.com").is_err(),
        "URL not in env list should fail even if in hardcoded list"
    );

    // 3) Env unset → original hardcoded list still works.
    unsafe { std::env::remove_var("AGENTX_AGENT_IDENTITY_ALLOWED_BASE_URLS"); }
    assert!(
        ChatGptEnvironment::from_chatgpt_base_url("https://chatgpt.com").is_ok(),
        "fallback to hardcoded whitelist when env unset"
    );

    // Restore.
    if let Some(v) = prev {
        unsafe { std::env::set_var("AGENTX_AGENT_IDENTITY_ALLOWED_BASE_URLS", v); }
    }
}
```

In `crates/agentx-login/src/auth/agent_identity.rs`, add a matching test
for `agent_identity_authapi_base_url(Some(url))`.

- [ ] **Step 2: Run tests to verify they fail**

```bash
cargo test -p agentx-agent-identity from_chatgpt_base_url_respects_env_allowlist 2>&1 | tail -10
cargo test -p agentx-login agent_identity_authapi_base_url_respects_env 2>&1 | tail -10
```

Expected: both FAIL.

- [ ] **Step 3: Implement in agent-identity**

Edit `crates/agentx-agent-identity/src/lib.rs` `from_chatgpt_base_url`:

```rust
impl ChatGptEnvironment {
    pub fn from_chatgpt_base_url(chatgpt_base_url: &str) -> Result<Self> {
        let trimmed = chatgpt_base_url.trim_end_matches('/');

        // Env override (set per-instance via AGENTX_AGENT_IDENTITY_ALLOWED_BASE_URLS,
        // comma-separated). When set, the URL must appear in the list.
        // When unset, fall back to the original hardcoded whitelist below.
        if let Ok(list) = std::env::var("AGENTX_AGENT_IDENTITY_ALLOWED_BASE_URLS") {
            let allowed: Vec<&str> = list.split(',').map(str::trim).filter(|s| !s.is_empty()).collect();
            if allowed.iter().any(|a| a.trim_end_matches('/') == trimmed) {
                // Treat any env-allowed URL as Production-equivalent.
                return Ok(Self::Production);
            }
            anyhow::bail!(
                "Agent Identity URL {chatgpt_base_url:?} not in AGENTX_AGENT_IDENTITY_ALLOWED_BASE_URLS"
            );
        }

        match trimmed {
            "https://chatgpt.com"
            | "https://chatgpt.com/backend-api"
            | "https://chatgpt.com/codex"
            | "https://chatgpt.com/backend-api/codex"
            | "https://chat.openai.com"
            | "https://chat.openai.com/backend-api"
            | "https://chat.openai.com/codex"
            | "https://chat.openai.com/backend-api/codex" => Ok(Self::Production),
            "https://chatgpt-staging.com"
            | "https://chatgpt-staging.com/backend-api"
            | "https://chatgpt-staging.com/codex"
            | "https://chatgpt-staging.com/backend-api/codex" => Ok(Self::Staging),
            _ => anyhow::bail!(
                "Agent Identity only supports production and staging ChatGPT environments"
            ),
        }
    }
}
```

- [ ] **Step 4: Implement in login**

Edit `crates/agentx-login/src/auth/agent_identity.rs` `agent_identity_authapi_base_url`:

```rust
pub(super) fn agent_identity_authapi_base_url(
    chatgpt_base_url: Option<&str>,
) -> std::io::Result<String> {
    let environment = match chatgpt_base_url {
        Some(url) => {
            // Same env override + fallback as agentx_agent_identity. Re-implementing
            // here instead of factoring out to keep changes localized for rebase.
            ChatGptEnvironment::from_chatgpt_base_url(url).map_err(std::io::Error::other)?
        }
        None => ChatGptEnvironment::default(),
    };
    Ok(environment.agent_identity_authapi_base_url().to_string())
}
```

(The simplification: `from_chatgpt_base_url` already does the env check, so
`login` automatically inherits it. Confirm by reading the chain. If
`agent_identity_authapi_base_url` did NOT previously call
`from_chatgpt_base_url` and instead duplicated the logic, replace that
duplicate with the single call shown above.)

- [ ] **Step 5: Run tests to verify they pass**

```bash
cargo test -p agentx-agent-identity 2>&1 | grep "^test result"
cargo test -p agentx-login agent_identity 2>&1 | grep "^test result"
```

Expected: both PASS.

- [ ] **Step 6: Sanity-check `grep -rn from_chatgpt_base_url`**

```bash
grep -rn from_chatgpt_base_url crates/
```

Expected: only the production definition + login's call + tests. No other
call sites left un-patched.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "feat: AGENTX_AGENT_IDENTITY_ALLOWED_BASE_URLS env override

When set (comma-separated URL list), agent-identity auth flow accepts
those URLs as Production-equivalent. When unset, original chatgpt.com /
chatgpt-staging.com whitelist preserved (zero-config behaviour unchanged).

Applied at both call sites: agent-identity/src/lib.rs::from_chatgpt_base_url
and login/src/auth/agent_identity.rs::agent_identity_authapi_base_url.
Login crate inherits the override transitively via the agent-identity
helper rather than duplicating env-read logic.

This is the change that lets https://codex-auth.agent.cs.ac.cn pass the
0.142 hardcoded whitelist — the trigger for the entire agentx project."
```

---

### Task 14: Feature — `AGENTX_API_KEY_ALLOWED_HOSTS` env override

**Files:**
- Modify: `crates/agentx-cli/src/main.rs` function `validate_api_key_remote_host` (line ~1774 in v0.142.0 baseline; post-rename location may differ — locate with grep)

**Interfaces:**
- Consumes: A `base_url: &str` for the remote registration.
- Produces: When `AGENTX_API_KEY_ALLOWED_HOSTS` env set, the URL host must be
  in that list. When unset, original openai.com/openai.org/loopback whitelist.

- [ ] **Step 1: Write the failing test**

In `crates/agentx-cli/src/main.rs` (or wherever the unit-tests module for
that function lives), add:

```rust
#[test]
fn validate_api_key_remote_host_respects_env_allowlist() {
    let prev = std::env::var("AGENTX_API_KEY_ALLOWED_HOSTS").ok();

    unsafe { std::env::set_var(
        "AGENTX_API_KEY_ALLOWED_HOSTS",
        "x.agent.cs.ac.cn,gateway.example",
    ); }
    assert!(
        validate_api_key_remote_host("https://x.agent.cs.ac.cn").is_ok(),
        "host in env list should pass"
    );
    assert!(
        validate_api_key_remote_host("https://random.example").is_err(),
        "host not in env list should fail"
    );

    unsafe { std::env::remove_var("AGENTX_API_KEY_ALLOWED_HOSTS"); }
    assert!(
        validate_api_key_remote_host("https://api.openai.com").is_ok(),
        "fallback to openai.com whitelist when env unset"
    );

    if let Some(v) = prev {
        unsafe { std::env::set_var("AGENTX_API_KEY_ALLOWED_HOSTS", v); }
    }
}
```

- [ ] **Step 2: Run to verify failure**

```bash
cargo test -p agentx-cli validate_api_key_remote_host_respects_env_allowlist 2>&1 | tail -10
```

Expected: FAIL.

- [ ] **Step 3: Implement**

Patch `validate_api_key_remote_host` to consult the env first:

```rust
fn validate_api_key_remote_host(base_url: &str) -> anyhow::Result<()> {
    let url = url::Url::parse(base_url)
        .map_err(|err| anyhow::anyhow!("invalid remote exec-server registration URL: {err}"))?;
    let host = url.host().ok_or_else(|| {
        anyhow::anyhow!("remote exec-server registration URL must include a host")
    })?;

    // Env override (set per-instance via AGENTX_API_KEY_ALLOWED_HOSTS,
    // comma-separated). When set, host must appear in the list.
    if let Ok(list) = std::env::var("AGENTX_API_KEY_ALLOWED_HOSTS") {
        let allowed: Vec<&str> = list.split(',').map(str::trim).filter(|s| !s.is_empty()).collect();
        let host_str = match &host {
            url::Host::Domain(d) => d.to_ascii_lowercase(),
            url::Host::Ipv4(ip) => ip.to_string(),
            url::Host::Ipv6(ip) => ip.to_string(),
        };
        if allowed.iter().any(|a| a.eq_ignore_ascii_case(&host_str)) {
            return Ok(());
        }
        anyhow::bail!(
            "remote exec-server host {host_str:?} not in AGENTX_API_KEY_ALLOWED_HOSTS"
        );
    }

    // Original openai.com / openai.org / loopback whitelist (preserved verbatim from v0.142).
    let is_loopback = match &host {
        url::Host::Domain(host) => host.eq_ignore_ascii_case("localhost"),
        url::Host::Ipv4(ip) => ip.is_loopback(),
        url::Host::Ipv6(ip) => ip.is_loopback(),
    };
    let is_openai_host = match &host {
        url::Host::Domain(host) => ["openai.com", "openai.org"].into_iter().any(|domain| {
            host.eq_ignore_ascii_case(domain)
                || host.to_ascii_lowercase().ends_with(&format!(".{domain}"))
        }),
        _ => false,
    };
    let is_allowed = match url.scheme() {
        "https" => is_loopback || is_openai_host,
        "http" => is_loopback,
        _ => false,
    };

    if !is_allowed {
        anyhow::bail!(
            "remote exec-server API-key authentication is restricted to HTTPS openai.com and openai.org hosts and subdomains or loopback hosts"
        );
    }

    Ok(())
}
```

- [ ] **Step 4: Run to verify pass**

```bash
cargo test -p agentx-cli validate_api_key_remote_host 2>&1 | grep "^test result"
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "feat: AGENTX_API_KEY_ALLOWED_HOSTS env override

When set (comma-separated host list), --remote URL host must be in the
list to pass the API-key auth gate. When unset, original openai.com/
openai.org/loopback whitelist preserved.

Lets agentx use API-key auth against gateways like x.agent.cs.ac.cn
without modifying the binary."
```

---

### Task 15: Feature — `--agent-identity-authapi-base-url` flag + env

**Files:**
- Modify: `crates/agentx-cli/src/main.rs` — top-level parser; add new flag + env binding; thread through to `CodexAuth::from_agent_identity_jwt`
- Modify: `crates/agentx-login/src/auth/manager.rs` — change `from_agent_identity_jwt` signature to take a fourth `Option<&str>` override (preferred over exposing the private helper)

**Interfaces:**
- Consumes: User-provided URL to a self-hosted Agent Identity authapi.
- Produces: `agentx` passes the override into `from_agent_identity_jwt`,
  which threads it into `from_agent_identity_jwt_with_authapi_base_url`.
  When the override is `None`, behaviour is unchanged from 0.142.

- [ ] **Step 1: Locate `from_agent_identity_jwt` signature**

```bash
grep -n "pub async fn from_agent_identity_jwt\b" crates/agentx-login/src/auth/manager.rs
sed -n '353,367p' crates/agentx-login/src/auth/manager.rs
```

Confirm the v0.142 signature:

```rust
pub async fn from_agent_identity_jwt(
    jwt: &str,
    chatgpt_base_url: Option<&str>,
    auth_route_config: Option<&AuthRouteConfig>,
) -> std::io::Result<Self> { ... }
```

- [ ] **Step 2: Write the failing test (signature-shape, not behaviour)**

Add to `crates/agentx-login/src/auth/manager.rs` tests:

```rust
#[tokio::test]
async fn from_agent_identity_jwt_accepts_authapi_override() {
    // This test just confirms the signature compiles with a fourth arg.
    // Real behaviour is covered by from_jwt_registers_task elsewhere.
    let result = CodexAuth::from_agent_identity_jwt(
        "not-a-jwt",
        Some("https://chatgpt.com/backend-api"),
        None,
        Some("https://example.com/api/accounts"),   // <-- new override arg
    ).await;
    // We don't care if it succeeds (JWT is junk); we care that it compiles.
    let _ = result;
}
```

- [ ] **Step 3: Run to verify failure (compile error: too many args)**

```bash
cargo test -p agentx-login from_agent_identity_jwt_accepts_authapi_override 2>&1 | tail -10
```

Expected: FAIL with "this function takes 3 arguments but 4 arguments were supplied".

- [ ] **Step 4: Add the fourth parameter**

```rust
pub async fn from_agent_identity_jwt(
    jwt: &str,
    chatgpt_base_url: Option<&str>,
    auth_route_config: Option<&AuthRouteConfig>,
    agent_identity_authapi_base_url_override: Option<&str>,
) -> std::io::Result<Self> {
    let agent_identity_authapi_base_url = match agent_identity_authapi_base_url_override {
        Some(url) => url.to_string(),
        None => agent_identity_authapi_base_url(chatgpt_base_url)?,
    };
    Self::from_agent_identity_jwt_with_authapi_base_url(
        jwt,
        chatgpt_base_url,
        &agent_identity_authapi_base_url,
        auth_route_config,
    )
    .await
}
```

Update every existing caller of `from_agent_identity_jwt` to pass `None`
as the new arg. `cargo build` will tell you which call sites need updating.

- [ ] **Step 5: Add CLI flag + env wiring in `crates/agentx-cli/src/main.rs`**

In the top-level `AgentxCli` parser (or `ExecServerCommand` if you didn't
inline it all in Task 4), add:

```rust
/// Override the Agent Identity auth-api base URL. If unset, derived from
/// the chatgpt_base_url via the default whitelist.
#[arg(
    long = "agent-identity-authapi-base-url",
    env = "AGENTX_AGENT_IDENTITY_AUTHAPI_BASE_URL",
)]
agent_identity_authapi_base_url: Option<String>,
```

In `load_exec_server_remote_auth_provider` (or wherever
`from_agent_identity_jwt` is called from the CLI), thread it through:

```rust
let auth = CodexAuth::from_agent_identity_jwt(
    &agent_identity_jwt,
    Some(&config.chatgpt_base_url),
    /*auth_route_config*/ None,
    cmd.agent_identity_authapi_base_url.as_deref(),
).await?;
```

(Replace the hardcoded `None` that 0.142 has at this call site.)

- [ ] **Step 6: Build + test**

```bash
cargo test -p agentx-login from_agent_identity_jwt 2>&1 | grep "^test result"
cargo build --workspace 2>&1 | tail -10
```

Expected: PASS + green.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "feat: --agent-identity-authapi-base-url flag + env

CodexAuth::from_agent_identity_jwt gains a fourth Option<&str> param,
agent_identity_authapi_base_url_override; when Some, it's used directly;
when None, the existing resolver (chatgpt_base_url → environment →
authapi base) runs. All existing callers pass None.

CLI exposes the override via --agent-identity-authapi-base-url flag
(env AGENTX_AGENT_IDENTITY_AUTHAPI_BASE_URL). Lets agentx point Agent
Identity bootstrap at a self-hosted authapi (codex-auth.agent.cs.ac.cn)
without invasive patching of the resolver."
```

---

### Task 16: Workspace metadata, README, NOTICE, brand strip

**Files:**
- Modify: `Cargo.toml` workspace root — `[workspace.package]` homepage, repository, authors
- Create: `README.md`
- Create: `NOTICE`
- Create: `CHANGELOG.md`
- Modify: any `--help` text or user-facing error message that mentions "OpenAI", "ChatGPT", "Codex" as a brand (NOT URLs — those stay)

**Interfaces:**
- Produces: a repo whose top-level metadata reads as a self-owned project.

- [ ] **Step 1: Edit workspace.package**

In `Cargo.toml`:

```toml
[workspace.package]
version = "0.0.1"
edition = "2024"  # (or whatever the v0.142 value was; keep)
license = "Apache-2.0"
homepage = "https://github.com/agentserver/agentx"
repository = "https://github.com/agentserver/agentx"
authors = ["agentserver maintainers"]
```

- [ ] **Step 2: Write README.md**

```markdown
# agentx

Single-binary remote process / filesystem executor.

`agentx` is a hard fork of [codex](https://github.com/openai/codex) `exec-server`
at tag `rust-v0.142.0`, with everything outside the remote-exec-server use
case removed. See `NOTICE` for derivation details.

## Install

```bash
curl -fsSL https://github.com/agentserver/agentx/releases/latest/download/install.sh | sh
```

Or download a tarball from
[releases](https://github.com/agentserver/agentx/releases/latest) and put
`agentx` on your `PATH`.

### macOS

Binaries are unsigned in v0.x. After download:

```bash
xattr -d com.apple.quarantine /usr/local/bin/agentx
```

## Usage

```bash
agentx --remote https://your-gateway/  --environment-id exe_… --name my-laptop \
       --use-agent-identity-auth \
       --agent-identity-authapi-base-url https://your-auth-server/
```

Environment:
- `AGENTX_ACCESS_TOKEN` — Agent Identity JWT.
- `AGENTX_AGENT_IDENTITY_ALLOWED_BASE_URLS` — comma-separated allow-list for
  the chatgpt_base_url config.
- `AGENTX_API_KEY` — bearer for API-key auth mode.
- `AGENTX_API_KEY_ALLOWED_HOSTS` — comma-separated allow-list for the
  --remote URL host in API-key mode.
- `AGENTX_HOME` — config directory (default `~/.agentx`).

## License

Apache-2.0. See LICENSE and NOTICE.
```

- [ ] **Step 3: Write NOTICE**

```
agentx
Copyright 2026 agentserver maintainers

This product includes software derived from codex
(https://github.com/openai/codex), released under the Apache License 2.0
by OpenAI.

Specifically, agentx v0.0.1 is forked from codex rust-v0.142.0 (git tag
rust-v0.142.0). The fork keeps a subset of crates around the exec-server
binary; see crates/ for the list. Modifications include:

- Removal of the noise relay protocol (Noise hybrid IK over WebSocket
  rendezvous) and related protobuf, ~400 LOC of tests.
- Removal of Bedrock auth, AWS SigV4 helpers, and CodexAuth::BedrockApiKey
  variant.
- Removal of the analytics telemetry crate and all call sites.
- Removal of all non-exec-server CLI subcommands and the --listen local-mode
  branch.
- Rename of all crate names, env vars, and config directory paths from
  codex-* / CODEX_* / ~/.codex to agentx-* / AGENTX_* / ~/.agentx.
- Two new env-driven allow-list overrides
  (AGENTX_AGENT_IDENTITY_ALLOWED_BASE_URLS and AGENTX_API_KEY_ALLOWED_HOSTS)
  and one new CLI flag (--agent-identity-authapi-base-url).

No upstream git remote is tracked.
```

- [ ] **Step 4: Write CHANGELOG.md**

```markdown
# Changelog

## v0.0.1 — 2026-06-XX

Initial release. Hard-fork of codex rust-v0.142.0; see NOTICE for derivation.
```

- [ ] **Step 5: Brand strip in source**

```bash
grep -rnE '"OpenAI"|"ChatGPT"|"Codex"' crates/ utils/ | grep -vE 'chatgpt\.com|chatgpt_base_url|codex_protocol' | head -20
```

For each hit in a user-facing string (CLI help, error message), rewrite
to neutral language ("agentx", "the remote exec-server"). Do NOT touch
URLs, dependency-feature flags, or struct field names.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "chore: workspace metadata + README + NOTICE + brand strip

Sets version 0.0.1, ownership metadata. README documents install + usage.
NOTICE declares derivation from codex rust-v0.142.0 per Apache-2.0
requirements. Brand-named strings ('OpenAI', 'ChatGPT' as product names,
not URLs) replaced with neutral wording in user-facing CLI text."
```

---

### Task 17: CI workflow — ci.yml

**Files:**
- Create: `.github/workflows/ci.yml`
- Create: `scripts/verify-no-codex-refs.sh`
- Create: `scripts/.codex-refs-allowed` (whitelist)

**Interfaces:**
- Produces: CI runs on every push + PR; gates merge on fmt / clippy / test /
  verify-no-codex-refs. Whitelist file lists README/NOTICE/CHANGELOG where
  "codex" is intentionally mentioned.

- [ ] **Step 1: Write `.github/workflows/ci.yml`**

```yaml
name: ci
on:
  push:
    branches: [main]
  pull_request:
    branches: [main]
concurrency:
  group: ci-${{ github.ref }}
  cancel-in-progress: true
jobs:
  build:
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/checkout@v4
      - uses: dtolnay/rust-toolchain@stable
        with:
          components: rustfmt, clippy
      - uses: Swatinem/rust-cache@v2
      - run: cargo fmt --check
      - run: cargo clippy --workspace --all-targets -- -D warnings
      - run: cargo test --workspace --no-fail-fast
      - run: bash scripts/verify-no-codex-refs.sh
```

- [ ] **Step 2: Write `scripts/verify-no-codex-refs.sh`**

```bash
#!/usr/bin/env bash
set -euo pipefail

# Fail if any source file outside the allow-list mentions codex/Codex/CODEX_/OpenAI/ChatGPT
# as branding. URLs (chatgpt.com, openai.com) are intentionally retained.

allow_file=scripts/.codex-refs-allowed
pattern='\b(codex|Codex|CODEX_[A-Z_]+|OpenAI|ChatGPT)\b'

# Search code dirs only.
hits=$(grep -rnE "$pattern" crates/ utils/ 2>/dev/null | \
       grep -v -F -f "$allow_file" || true)

if [[ -n "$hits" ]]; then
  echo "verify-no-codex-refs.sh: found unallowed brand references:"
  echo "$hits"
  exit 1
fi

echo "verify-no-codex-refs.sh: OK"
```

```bash
chmod +x scripts/verify-no-codex-refs.sh
```

- [ ] **Step 3: Write `scripts/.codex-refs-allowed`**

This file is a list of grep-output substrings that are allowed (exact-line
match via `grep -F`). Seed it with the unavoidable URL refs and any prose
in non-code files we end up keeping:

```
chatgpt.com
openai.com
openai.org
codex_protocol
codex-protocol
codex-snapshot
```

(Tune after running the script locally. The script greps `crates/` and
`utils/` only, so README/NOTICE/CHANGELOG matches don't appear.)

- [ ] **Step 4: Run the script locally**

```bash
bash scripts/verify-no-codex-refs.sh
```

Expected: `verify-no-codex-refs.sh: OK`. If it fails, examine each hit:
- Legitimate (URL, protocol name) → add a substring to `.codex-refs-allowed`.
- Spurious leftover (comment "from codex") → edit the source to remove it.

Iterate.

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "ci: fmt + clippy + test + verify-no-codex-refs

CI runs on ubuntu-24.04. verify-no-codex-refs.sh greps source dirs for
brand references and fails if any appear outside the allow-list (URLs,
protocol crate name retained)."
```

---

### Task 18: Release workflow — release.yml (4 targets)

**Files:**
- Create: `.github/workflows/release.yml`
- Create: `scripts/install.sh` (user-facing curl|sh installer)

**Interfaces:**
- Produces: On `v*` tag push, GitHub Actions builds 4 target tarballs (macOS arm/x64, Linux musl arm/x64) and uploads to GitHub Releases. Linux tarballs bundle `bwrap` helper.

- [ ] **Step 1: Write `.github/workflows/release.yml`**

```yaml
name: release
on:
  push:
    tags:
      - "v*.*.*"

concurrency:
  group: release-${{ github.ref }}
  cancel-in-progress: false

jobs:
  build:
    name: build-${{ matrix.target }}
    runs-on: ${{ matrix.runner }}
    timeout-minutes: 90
    permissions:
      contents: write   # to upload artifacts to the release
    strategy:
      fail-fast: false
      matrix:
        include:
          - target: aarch64-apple-darwin
            runner: macos-15
            artifact: agentx-aarch64-apple-darwin.tar.gz
            build_dmg: true
          - target: x86_64-apple-darwin
            runner: macos-15
            artifact: agentx-x86_64-apple-darwin.tar.gz
            build_dmg: true
          - target: x86_64-unknown-linux-musl
            runner: ubuntu-24.04
            artifact: agentx-x86_64-unknown-linux-musl.tar.gz
          - target: aarch64-unknown-linux-musl
            runner: ubuntu-24.04-arm
            artifact: agentx-aarch64-unknown-linux-musl.tar.gz
    steps:
      - uses: actions/checkout@v4

      - uses: dtolnay/rust-toolchain@stable
        with:
          targets: ${{ matrix.target }}

      - name: Install Linux musl tools (zig)
        if: contains(matrix.target, 'linux-musl')
        uses: mlugg/setup-zig@v2.2.1
        with:
          version: 0.14.0

      - name: Install musl build deps
        if: contains(matrix.target, 'linux-musl')
        env:
          TARGET: ${{ matrix.target }}
        run: |
          sudo apt-get update -y
          sudo DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
            binutils pkg-config libcap-dev
          bash .github/scripts/install-musl-build-tools.sh

      - name: Build agentx
        run: cargo build --release --target ${{ matrix.target }} --bin agentx

      - name: Build bwrap (Linux only)
        if: contains(matrix.target, 'linux-musl')
        run: cargo build --release --target ${{ matrix.target }} --bin bwrap || \
             cargo build --release --target ${{ matrix.target }} -p agentx-bwrap

      - name: Stage tarball contents
        shell: bash
        run: |
          set -euo pipefail
          stage="agentx-${{ matrix.target }}"
          mkdir -p "$stage"
          cp "target/${{ matrix.target }}/release/agentx" "$stage/"
          if [[ "${{ matrix.target }}" == *linux-musl ]]; then
            cp "target/${{ matrix.target }}/release/bwrap" "$stage/bwrap" 2>/dev/null || \
              cp "target/${{ matrix.target }}/release/agentx-bwrap" "$stage/bwrap"
          fi
          cp README.md NOTICE LICENSE "$stage/"

      - name: Create tarball + sha256
        shell: bash
        run: |
          set -euo pipefail
          tar -czf "${{ matrix.artifact }}" "agentx-${{ matrix.target }}"
          sha256sum "${{ matrix.artifact }}" > "${{ matrix.artifact }}.sha256"

      - name: Build .dmg (macOS only, unsigned)
        if: matrix.build_dmg == true
        run: |
          stage="agentx-${{ matrix.target }}"
          hdiutil create -volname agentx -srcfolder "$stage" -ov -format UDZO "agentx-${{ matrix.target }}.dmg"

      - uses: softprops/action-gh-release@v2
        with:
          files: |
            ${{ matrix.artifact }}
            ${{ matrix.artifact }}.sha256
            ${{ matrix.target == 'aarch64-apple-darwin' && 'agentx-aarch64-apple-darwin.dmg' || '' }}
            ${{ matrix.target == 'x86_64-apple-darwin' && 'agentx-x86_64-apple-darwin.dmg' || '' }}

  publish-installer:
    needs: build
    runs-on: ubuntu-24.04
    permissions:
      contents: write
    steps:
      - uses: actions/checkout@v4
      - uses: softprops/action-gh-release@v2
        with:
          files: scripts/install.sh
```

- [ ] **Step 2: Write `scripts/install.sh`**

```bash
#!/usr/bin/env bash
set -euo pipefail

# Usage: curl -fsSL https://github.com/agentserver/agentx/releases/latest/download/install.sh | sh

VERSION="${AGENTX_VERSION:-latest}"
INSTALL_DIR="${AGENTX_INSTALL_DIR:-/usr/local/bin}"

uname_s=$(uname -s)
uname_m=$(uname -m)

case "$uname_s-$uname_m" in
  Darwin-arm64)        target=aarch64-apple-darwin ;;
  Darwin-x86_64)       target=x86_64-apple-darwin ;;
  Linux-x86_64)        target=x86_64-unknown-linux-musl ;;
  Linux-aarch64|Linux-arm64) target=aarch64-unknown-linux-musl ;;
  *) echo "agentx: unsupported platform $uname_s-$uname_m" >&2; exit 1 ;;
esac

base="https://github.com/agentserver/agentx/releases/${VERSION}/download"
if [[ "$VERSION" == "latest" ]]; then
  base="https://github.com/agentserver/agentx/releases/latest/download"
fi

tarball="agentx-$target.tar.gz"
url="$base/$tarball"
checksum_url="$url.sha256"

tmp=$(mktemp -d)
trap "rm -rf $tmp" EXIT

echo "agentx: downloading $url"
curl -fsSL "$url" -o "$tmp/$tarball"
curl -fsSL "$checksum_url" -o "$tmp/$tarball.sha256"

(cd "$tmp" && sha256sum -c "$tarball.sha256")

tar -xzf "$tmp/$tarball" -C "$tmp"

src="$tmp/agentx-$target/agentx"
if [[ ! -x "$src" ]]; then
  echo "agentx: extracted tarball missing executable at $src" >&2
  exit 1
fi

if [[ -w "$INSTALL_DIR" ]]; then
  install -m 0755 "$src" "$INSTALL_DIR/agentx"
else
  echo "agentx: installing to $INSTALL_DIR (needs sudo)"
  sudo install -m 0755 "$src" "$INSTALL_DIR/agentx"
fi

# Linux: install bwrap helper next to agentx if present.
if [[ -x "$tmp/agentx-$target/bwrap" ]]; then
  if [[ -w "$INSTALL_DIR" ]]; then
    install -m 0755 "$tmp/agentx-$target/bwrap" "$INSTALL_DIR/bwrap"
  else
    sudo install -m 0755 "$tmp/agentx-$target/bwrap" "$INSTALL_DIR/bwrap"
  fi
fi

echo "agentx: installed to $INSTALL_DIR/agentx"
$INSTALL_DIR/agentx --version 2>/dev/null || true
```

```bash
chmod +x scripts/install.sh
```

- [ ] **Step 3: Commit**

```bash
git add -A
git commit -m "ci: release.yml (4 targets) + install.sh

GitHub Actions release pipeline builds aarch64-apple-darwin,
x86_64-apple-darwin, x86_64-unknown-linux-musl, and
aarch64-unknown-linux-musl tarballs on tag v*.*.*. macOS dmgs are
unsigned; Linux tarballs bundle bwrap helper.

install.sh autodetects platform, downloads tarball + sha256, verifies,
installs to /usr/local/bin (or AGENTX_INSTALL_DIR). Documented in README."
```

---

### Task 19: Push to GitHub + tag v0.0.1

**Files:** none (publishing)

**Interfaces:**
- Produces: live GitHub repo with main branch + v0.0.1 tag + release tarballs.

- [ ] **Step 1: Create the empty GitHub repo**

In a browser (NOT `gh repo fork`): https://github.com/organizations/agentserver/repositories/new
- Owner: `agentserver`
- Name: `agentx`
- Visibility: public (or per org policy)
- **Do NOT initialize with README / LICENSE / .gitignore** — repo will be
  populated from our local commits.
- **Do NOT use the Fork button** anywhere.

Confirm at `https://github.com/agentserver/agentx`.

- [ ] **Step 2: Wire the remote and push main**

```bash
cd /root/agentx-work/agentx
git remote add origin git@github.com:agentserver/agentx.git
git remote -v
```

Expected: only `origin`. No upstream.

```bash
git push -u origin main
```

- [ ] **Step 3: Verify CI passes on main**

Open `https://github.com/agentserver/agentx/actions` and wait for the `ci`
workflow to go green on the push. If it fails, fix and push again. Do NOT
proceed to tag until CI is green.

- [ ] **Step 4: Tag and push v0.0.1**

```bash
git tag -a v0.0.1 -m "v0.0.1 — initial release

Hard-fork of codex rust-v0.142.0; see NOTICE."
git push origin v0.0.1
```

- [ ] **Step 5: Wait for release workflow + verify artifacts**

Open `https://github.com/agentserver/agentx/actions` and wait for
`release` to go green on the tag.

Then `https://github.com/agentserver/agentx/releases/tag/v0.0.1` should
show 4 tarballs + 4 .sha256 files + 2 .dmg files + `install.sh`.

- [ ] **Step 6: Smoke-test the installer**

On a Linux box (or in a Docker container) NOT this one:

```bash
curl -fsSL https://github.com/agentserver/agentx/releases/latest/download/install.sh | sh
agentx --help | head
```

Expected: install completes; `agentx --help` shows the flattened
exec-server flags (`--remote`, `--environment-id`, `--name`,
`--use-agent-identity-auth`, `--agent-identity-authapi-base-url`, …).

If you can't run from another box, do it inside a fresh ubuntu container:

```bash
docker run --rm -it ubuntu:24.04 bash -lc 'apt-get update && apt-get install -y curl && curl -fsSL https://github.com/agentserver/agentx/releases/latest/download/install.sh | sh && agentx --help | head'
```

---

### Task 20: Verification + Part 1 sign-off

**Files:** none (verification)

**Interfaces:**
- Confirms: all Part 1 success criteria met. No production system has been
  touched yet — agentserver continues to run as-is on chart 0.69.5.

- [ ] **Step 1: Repo health check**

```bash
cd /root/agentx-work/agentx
git remote -v       # only origin
git log --oneline   # ~18 commits (Tasks 2–18)
git tag             # v0.0.1
```

- [ ] **Step 2: CI status**

Open `https://github.com/agentserver/agentx/actions`. Both `ci` (on main)
and `release` (on v0.0.1 tag) green.

- [ ] **Step 3: Release artifacts list**

`https://github.com/agentserver/agentx/releases/tag/v0.0.1` shows:
- `agentx-aarch64-apple-darwin.tar.gz` + `.sha256` + `.dmg`
- `agentx-x86_64-apple-darwin.tar.gz` + `.sha256` + `.dmg`
- `agentx-x86_64-unknown-linux-musl.tar.gz` + `.sha256`
- `agentx-aarch64-unknown-linux-musl.tar.gz` + `.sha256`
- `install.sh`

- [ ] **Step 4: Brand-strip check**

```bash
cd /root/agentx-work/agentx
bash scripts/verify-no-codex-refs.sh   # OK
```

- [ ] **Step 5: Smoke-test against agentserver staging gateway**

This is the **end-to-end behaviour gate** for Part 1. We confirm that
the fixed allow-list reaches a working register call. agentserver in cs.ac.cn
is still running 0.69.5 (noise mode), so we can't test full bridge here —
just the register HTTP call shape.

If you have access to staging where noise is OFF:

```bash
export AGENTX_ACCESS_TOKEN=<a real JWT minted from codex-auth>
export AGENTX_AGENT_IDENTITY_ALLOWED_BASE_URLS=https://codex-auth.agent.cs.ac.cn
agentx --remote https://staging-gateway/ \
       --environment-id exe_test --name smoke-test \
       --use-agent-identity-auth \
       --agent-identity-authapi-base-url https://codex-auth.agent.cs.ac.cn
```

Expected: register call succeeds (logs show 200 from /cloud/...); does NOT
fail with "Agent Identity only supports production and staging ChatGPT
environments". (Full bridge will fail because staging gateway still expects
noise — that's normal and resolved in Part 2.)

If no noise-off staging exists, defer this check to Part 3 post-cutover.
Note in the sign-off below which path was taken.

- [ ] **Step 6: Sign-off**

Stop here. Part 1 is complete. Confirm with the user before starting Part 2.

Status to report:
- Repo URL
- Tag (v0.0.1)
- Release URL
- Smoke-test outcome (or deferred)

Then: get **explicit user OK** before opening Part 2
(`docs/superpowers/plans/2026-06-23-agentx-extraction-part2-agentserver-pr.md`).

## Rollback notes (Part 1)

Part 1 changes **no production system**. Rollback is trivial:

- Delete the tag: `gh release delete v0.0.1 --yes` + `git push origin :v0.0.1`.
- Delete the repo: `gh repo delete agentserver/agentx --yes`.
- Delete local: `rm -rf /root/agentx-work`.

Cost of full rollback: ~5 minutes. agentserver users are unaffected because
nothing in cs.ac.cn references agentx yet.
