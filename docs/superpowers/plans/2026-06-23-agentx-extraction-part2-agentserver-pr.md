# agentx Extraction — Part 2: agentserver Single PR (Phase B)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **Rev 2 (2026-06-23 post-review)**: addressed 3 findings — chart version
> baseline corrected throughout from 0.69.5 → **0.69.9** (the actual main
> state); T8 step 3 CI workflow specified as `.github/workflows/build.yml`
> (only workflow in this repo, not the guessed `test.yml`/`ci.yml`); T7
> step 2 helper function location nailed down (`internal/server/codex_executors.go`,
> same package).

**Goal:** Single agentserver PR that deletes noise / Bedrock-era helm + `/cloud/*` paths,
swaps to `/agentx/*` paths and host `x.agent.cs.ac.cn`, updates the ConnectCommands
template to emit the agentx connect command, and bumps the helm chart 0.69.9 →
0.70.0. End state: PR merged to main; chart 0.70.0 image built and available in the
container registry. **Production is NOT touched** until Part 3.

**Architecture:** Pure subtraction + path/host rename, no new behaviour added. Wire
protocol stays plaintext JSON-RPC over WebSocket, which Go side already speaks
(noise was the recent addition we're removing). Single PR keeps main branch from
having a half-cut intermediate state.

**Tech Stack:** Go 1.22, chi router, Helm 3, Pulumi (TypeScript). No new dependencies.

**Spec:** `docs/superpowers/specs/2026-06-23-agentx-extraction-design.md` rev 2,
commit `1d1b98a` on branch `spec/agentx-extraction`. Sections §6 + §7 + §9.2.

**Parts:** This is **Part 2 of 3**.
- Part 1 (`...-part1-agentx-repo.md`): Phase A — agentx repo, v0.0.1 release. **MUST BE DONE FIRST and user-signed-off**.
- Part 2 (this file): Phase B — single agentserver PR.
- Part 3 (`...-part3-pulumi-cutover.md`): Phase C — DNS + pulumi hard-cut + Phase D cleanup.

## Global Constraints

- **Single PR**. Commits C1..C11 + C2b go into one PR (`feat(agentx): drop
  noise+cloud, adopt /agentx/* paths, new gateway host`). Reviewer can squash
  on merge or land as 12 commits — both work; spec accepts either.
- **Hard cut**. No coexistence between `/cloud/*` and `/agentx/*` paths. Old
  paths gone in the same commit that adds new ones. No deprecation layer.
- **Chart bumps 0.69.9 → 0.70.0** in the same PR (commit C9).
- **Reality check** (discovered during plan writing, not in spec): Noise is more
  woven into `codexexecgateway` than the spec implied. **8 production .go files**
  reference noise/relaypb (not 2): `bridge.go` (39 refs), `inbound.go` (3),
  `auth.go` (1), `server.go` (20), `config.go` (5), plus the wholesale-deleted
  `noise_handlers.go`, `noise_router.go` (598 LOC), `noise_store.go` (140 LOC).
  C1 plan below accounts for all of them.
- **Prerequisite**: Part 1 sign-off. agentx v0.0.1 release MUST exist at
  `https://github.com/agentserver/agentx/releases/tag/v0.0.1` because C5
  integration test downloads it. Verify before Task 1 starts.
- **Branch**: this PR lives on its own feature branch off `main`, NOT off
  `spec/agentx-extraction`. Branch name: `feat/agentx-cutover`. Spec branch
  is review-only; should be merged to main first (separate PR) or rebased
  into the feat branch — confirm with user.
- **No production impact during Part 2**. Even after merge, chart 0.70.0
  just sits in the registry; Pulumi in `/root/k8s` hasn't been edited yet.
  Cs.ac.cn keeps running 0.69.9 unchanged.

## File Structure

### Files DELETED (10 files)

- `internal/codexexecgateway/noise/` (entire subdir, 15 files, ~2300 LOC)
- `internal/codexexecgateway/noise_handlers.go` (379 LOC)
- `internal/codexexecgateway/noise_router.go` (598 LOC)
- `internal/codexexecgateway/noise_store.go` (140 LOC)
- `internal/codexexecgateway/noise_handlers_test.go`
- `internal/codexexecgateway/noise_router_test.go`
- `internal/codexexecgateway/noise_router_live_test.go`
- `internal/codexexecgateway/noise_router_bridge_live_test.go`
- `internal/codexexecgateway/noise_server_wiring_test.go`
- `internal/codexexecgateway/noise_live_codex_test.go`
- `internal/codexexecgateway/bridge_noise_live_test.go`
- `internal/relaypb/` (entire dir: relay.proto + relay.pb.go)

### Files MODIFIED (subtractive — noise removal)

- `internal/codexexecgateway/server.go` (delete noise wiring 47-50, 148-168, route logic 266-275)
- `internal/codexexecgateway/config.go` (delete `NoiseRelayHMACKey` field 57-63 + env read line 93)
- `internal/codexexecgateway/bridge.go` (delete `useNoise` fork 90-96, 150-151; delete `serveBridgeViaNoise` helper ~328-420; simplify imports)
- `internal/codexexecgateway/inbound.go` (delete 3 noise refs — identify with grep)
- `internal/codexexecgateway/auth.go` (1 noise comment — delete or rephrase)

### Files MODIFIED (path swap)

- `internal/codexexecgateway/server.go` (`/cloud/{executor,environment}/{id}/register` → `/agentx/environment/{env_id}/register`, ws `/codex-exec/{exe_id}` → `/agentx/{exe_id}`)
- `internal/codexexecgateway/handlers/cloud_register.go` → **renamed** to `agentx_register.go`; `CloudRegister` fn → `AgentxRegister`; comments updated
- `internal/codexexecgateway/handlers/cloud_register_test.go` → renamed accordingly
- `internal/codexececdge/server.go` (delete `/cloud/*` proxy routes lines 58-59)

### Files MODIFIED (template + integration test)

- `internal/server/codex_executors.go` (lines 161-179 ConnectCommands template)
- `internal/codexauth/integration_test.go` (line 30 + 64-69 — switch from `codex` to `agentx` binary)

### Files MODIFIED (Helm / Pulumi-driven config)

- `deploy/helm/agentserver/values.yaml` (delete `codexExecGateway.noiseRelayEnabled` line 395 + comment 388-394; delete `codexGateway.noiseRelayHmacKey` line 293 + comment 292; default `publicHost` for codexExecGateway → `x.<ingress.host>`)
- `deploy/helm/agentserver/templates/codex-gateway-secret.yaml` (delete noise-relay-hmac-key field 44 + preservation logic lines 24, 28, 32 + header comment 10-14)
- `deploy/helm/agentserver/templates/codex-exec-gateway.yaml` (delete entire `{{- if .Values.codexExecGateway.noiseRelayEnabled }}` block at lines 102-114; update comment at lines 124-125, 128)
- `deploy/helm/agentserver/Chart.yaml` (version 0.69.9 → 0.70.0)
- `README.md` / `README.zh.md` (sections mentioning codex exec-server, connect command, codex-exec hostname)

### Files NOT modified (Pulumi)

- `/root/k8s/stacks/agentserver.ts` — covered by **Part 3**, not this PR. Don't touch in Part 2.

---

### Task 1: Branch + prerequisite check

**Files:** none (setup)

**Interfaces:** none

- [ ] **Step 1: Confirm Part 1 is shipped**

```bash
curl -sSL https://github.com/agentserver/agentx/releases/tag/v0.0.1 | grep -q 'agentx-x86_64-unknown-linux-musl.tar.gz' && echo OK || echo MISSING
```

Expected: `OK`. If `MISSING`, STOP. Go back, finish Part 1, get user sign-off.

- [ ] **Step 2: Confirm spec branch state**

```bash
cd /root/agentserver
git fetch origin
git log --oneline origin/main | head -3
git log --oneline spec/agentx-extraction -5
```

Confirm with user: should `spec/agentx-extraction` (3 spec/plan commits) be
merged to main first as a docs-only PR, or rebased into the feature branch?
Default: merge spec/agentx-extraction first via a docs PR, then branch
`feat/agentx-cutover` off the post-merge main.

- [ ] **Step 3: Create the feature branch off main**

```bash
git checkout main
git pull --ff-only
git checkout -b feat/agentx-cutover
```

- [ ] **Step 4: Confirm clean working tree**

```bash
git status
```

Expected: `nothing to commit, working tree clean`. If not, stash or commit
unrelated changes elsewhere first.

---

### Task 2 (C1): Delete noise infrastructure — files

**Files:**
- Delete: `internal/codexexecgateway/noise/` (subdir, 15 files)
- Delete: `internal/codexexecgateway/noise_handlers.go`
- Delete: `internal/codexexecgateway/noise_router.go`
- Delete: `internal/codexexecgateway/noise_store.go`
- Delete: `internal/codexexecgateway/noise_handlers_test.go`
- Delete: `internal/codexexecgateway/noise_router_test.go`
- Delete: `internal/codexexecgateway/noise_router_live_test.go`
- Delete: `internal/codexexecgateway/noise_router_bridge_live_test.go`
- Delete: `internal/codexexecgateway/noise_server_wiring_test.go`
- Delete: `internal/codexexecgateway/noise_live_codex_test.go`
- Delete: `internal/codexexecgateway/bridge_noise_live_test.go`
- Delete: `internal/relaypb/`

**Interfaces:**
- Produces: a package that does NOT build (bridge.go / server.go / config.go
  still reference deleted symbols). C1b unwires those.

- [ ] **Step 1: Inventory + delete**

```bash
cd /root/agentserver

git rm -r internal/codexexecgateway/noise/
git rm internal/codexexecgateway/noise_handlers.go
git rm internal/codexexecgateway/noise_router.go
git rm internal/codexexecgateway/noise_store.go
git rm internal/codexexecgateway/noise_handlers_test.go
git rm internal/codexexecgateway/noise_router_test.go
git rm internal/codexexecgateway/noise_router_live_test.go
git rm internal/codexexecgateway/noise_router_bridge_live_test.go
git rm internal/codexexecgateway/noise_server_wiring_test.go
git rm internal/codexexecgateway/noise_live_codex_test.go
git rm internal/codexexecgateway/bridge_noise_live_test.go
git rm -r internal/relaypb/
```

- [ ] **Step 2: Build to confirm expected failures**

```bash
go build ./... 2>&1 | tail -30
```

Expected: errors in `bridge.go`, `server.go`, `config.go`, `auth.go`,
`inbound.go` — referencing `noise.*`, `NoiseHandlers`, `NoiseRouter`,
`NoiseRelayHMACKey`, `relaypb.*`, `LookupNoiseExecutorRegistrationByEnv`,
etc. Capture this list — Task 3 fixes each.

- [ ] **Step 3: Commit (intentionally non-building intermediate)**

> Note: this commit leaves main broken IF merged solo. The PR as a whole
> stays consistent because Tasks 2 + 3 + 4 sit in the same PR. If your team
> insists on every commit building, rebase Tasks 2 + 3 into one commit
> before opening the PR.

```bash
git commit -m "C1a: delete noise relay implementation files (intermediate)

Removes noise/, noise_handlers.go, noise_router.go, noise_store.go,
relaypb/, all *_noise_*_test.go. Package does NOT build at this commit;
C1b (next) unwires references in bridge.go / server.go / config.go /
auth.go / inbound.go."
```

---

### Task 3 (C1 continued): Unwire noise references in remaining .go files

**Files:**
- Modify: `internal/codexexecgateway/server.go`
- Modify: `internal/codexexecgateway/config.go`
- Modify: `internal/codexexecgateway/bridge.go`
- Modify: `internal/codexexecgateway/inbound.go`
- Modify: `internal/codexexecgateway/auth.go`

**Interfaces:**
- Consumes: package state after Task 2.
- Produces: `go build ./...` and `go vet ./...` succeed for `codexexecgateway`. Bridge serves plaintext WS only (no noise branch).

- [ ] **Step 1: `config.go` — delete `NoiseRelayHMACKey` field + env read**

Open `internal/codexexecgateway/config.go`. Locate `NoiseRelayHMACKey`:

```go
// lines ~57-63
// NoiseRelayHMACKey is the shared HMAC secret ...
NoiseRelayHMACKey []byte

// line ~93
NoiseRelayHMACKey:         []byte(os.Getenv("CXG_NOISE_RELAY_HMAC_KEY")),
```

Delete both the field definition (including its 6-line comment block) and the
struct-initializer line. Save.

```bash
go build ./internal/codexexecgateway/ 2>&1 | grep -i "noise\|relaypb" | head
```

`NoiseRelayHMACKey`-related errors should be gone; others remain.

- [ ] **Step 2: `server.go` — delete noise wiring block + route logic**

Locate three regions in `internal/codexexecgateway/server.go`:

1. Struct field (`noiseHandlers *NoiseHandlers`, lines ~41-46): delete.
2. Constructor block (lines ~148-168): the entire `// Noise relay endpoints —`
   comment + `if len(cfg.NoiseRelayHMACKey) > 0 { ... } else { ... }` block.
   Delete wholesale.
3. Route mounting (lines ~266-275): the `if s.noiseHandlers != nil { ... } else { ... }` block. Replace with the single line from the else branch:

```go
r.Post("/cloud/environment/{env_id}/register", cloudRegister)
```

(Path swap to `/agentx/...` happens in Task 4 — leave `/cloud/...` for now
so the diff stays reviewable.)

Also delete the import: `"github.com/agentserver/agentserver/internal/codexexecgateway/noise"` (line ~14).

```bash
go build ./internal/codexexecgateway/ 2>&1 | grep -iE "noise|relaypb" | head
```

`server.go` errors gone.

- [ ] **Step 3: `bridge.go` — delete noise fork + helper**

Open `internal/codexexecgateway/bridge.go`. Three regions:

1. **Top import** (line 12): `"github.com/agentserver/agentserver/internal/relaypb"` → delete.

2. **Noise-fork block** (lines ~86-101): the entire path-selection block
   that sets `useNoise := false`, looks up `LookupNoiseExecutorRegistrationByEnv`,
   and reads `s.noiseHandlers`. The simplification: if the legacy path
   `legacyOK` is false, immediately bail. Replace lines ~86-101 with:

```go
   if !legacyOK {
       // legacyOK is set higher up by the auth/session check. Without
       // a valid bridge session, reject the WS upgrade.
       http.Error(w, "no session", http.StatusUnauthorized)
       return
   }
```

   (Adjust to match actual return shape in your code — read surrounding
   context to keep the existing rejection style.)

3. **`if useNoise { ... }` branch** (lines ~146-151) — delete the entire
   block (the `// Noise-mode branch:` comment too).

4. **`serveBridgeViaNoise` helper** (lines ~318-420 approximately — search
   with `grep -n "func.*serveBridgeViaNoise\|func.*noiseHandlers"`) — delete the
   entire function body, including any helper functions only it calls.

5. **`firstFrame` `relaypb.RelayMessageFrame` peek** (lines ~128-140): this
   was code that read the first WS frame to decide noise vs legacy. With
   noise gone there's no decision to make. Delete the protobuf peek; the
   WS first frame is now consumed directly as plaintext JSON-RPC by the
   existing legacy path. Verify by reading the function from top to bottom
   after deletions; what remains should be the linear plaintext path.

```bash
go build ./internal/codexexecgateway/ 2>&1 | tail -20
```

- [ ] **Step 4: `inbound.go` — delete 3 noise refs**

```bash
grep -n "noise\|Noise\|relaypb" internal/codexexecgateway/inbound.go
```

For each hit, classify:
- Import → delete.
- Function call / type use → delete the surrounding block.

Test build after.

- [ ] **Step 5: `auth.go` — one noise comment**

```bash
grep -n "noise" internal/codexexecgateway/auth.go
```

Single comment at ~line 36 mentioning "noise" as English word ("recording
would be noise"). Either keep (it's not the noise protocol) or rephrase
to "recording would be redundant". Either choice is fine; the verify-no-codex-refs
gate in agentx doesn't apply here.

- [ ] **Step 6: Build + vet whole repo**

```bash
go build ./... 2>&1 | tail
go vet ./... 2>&1 | tail
```

Expected: green on both.

- [ ] **Step 7: Run tests in codexexecgateway pkg**

```bash
go test ./internal/codexexecgateway/... 2>&1 | tail
```

Some tests will fail because they constructed noise-mode bridge sessions
or asserted noise routes were registered. Fix:

- Tests asserting `/cloud/relay/...` route exists → delete the assertion or test.
- Tests setting `cfg.NoiseRelayHMACKey` → delete the field set.
- Tests using `relaypb.RelayMessageFrame` → delete or rewrite as plaintext WS test.

Iterate until tests pass.

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "C1b: unwire noise references from production code

server.go: delete noiseHandlers field, noise constructor block, noise
route dispatch logic. config.go: delete NoiseRelayHMACKey field + env
read. bridge.go: delete useNoise fork, RelayMessageFrame peek, and
serveBridgeViaNoise helper; bridge is now plaintext-only.
inbound.go / auth.go: minor cleanups.

Package builds + tests green. Wire is plaintext JSON-RPC over WS,
matching what agentx (Part 1) speaks."
```

---

### Task 4 (C2): Path swap on gateway — `/cloud/*` → `/agentx/*`

**Files:**
- Modify: `internal/codexexecgateway/server.go` (HTTP routes ~247, 265, ws ~247)

**Interfaces:**
- Produces: HTTP POST `/agentx/environment/{env_id}/register` and WS upgrade `/agentx/{exe_id}` (the old `/cloud/*` and `/codex-exec/{exe_id}` paths are gone). Existing handlers (`CloudRegister`, `handleInbound`) are reused — rename to `AgentxRegister` happens in Task 6.

- [ ] **Step 1: Find current route mounts**

```bash
grep -n "r\\.Post(\\|r\\.Get(\\|r\\.Route(" internal/codexexecgateway/server.go | head
```

Expected line range: ~245-275.

- [ ] **Step 2: Replace `/cloud/executor/{exe_id}/register` and `/cloud/environment/{env_id}/register`**

In `server.go`, find:

```go
r.Post("/cloud/executor/{exe_id}/register", cloudRegister)
// /cloud/environment/{env_id}/register comment block
r.Post("/cloud/environment/{env_id}/register", cloudRegister)
```

Replace with single line:

```go
r.Post("/agentx/environment/{env_id}/register", cloudRegister)
```

(Old `/cloud/executor/...` for codex ≤0.132 is deleted — agentx uses
`/agentx/environment/...` only.)

- [ ] **Step 3: Replace `/codex-exec/{exe_id}` ws upgrade**

In `server.go`, find:

```go
r.Get("/codex-exec/{exe_id}", s.handleInbound)
```

Replace with:

```go
r.Get("/agentx/{exe_id}", s.handleInbound)
```

- [ ] **Step 4: Update inline comments referring to old paths**

```bash
grep -n "/cloud/\|/codex-exec/" internal/codexexecgateway/server.go
```

For each comment hit, update to `/agentx/...`. Code hits should be zero
after Steps 2-3.

- [ ] **Step 5: Search elsewhere in package for hardcoded old paths**

```bash
grep -rn '"/cloud/\|"/codex-exec/' internal/codexexecgateway/ internal/codexececdge/
```

- `internal/codexexecgateway/handlers/cloud_register.go:204`: `wsURL := base + "/codex-exec/" + ...` — patch to `"/agentx/"`.
- Any other handlers / response builders that embed the path in URLs.

Update each. (The `cloud_register.go` file rename to `agentx_register.go`
happens in Task 6.)

- [ ] **Step 6: Build + test**

```bash
go build ./... 2>&1 | tail
go test ./internal/codexexecgateway/... 2>&1 | tail
```

Test failures: any test that POSTed to `/cloud/...` or GET `/codex-exec/{id}`.
Update the test URLs to `/agentx/...`. Iterate.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "C2: swap /cloud/* + /codex-exec/{id} → /agentx/* paths

Gateway HTTP routes: /cloud/{executor,environment}/{id}/register replaced
by a single /agentx/environment/{env_id}/register. WS upgrade
/codex-exec/{exe_id} replaced by /agentx/{exe_id}. Internal URL builders
in handlers/ updated accordingly. No backward compat — hard cut."
```

---

### Task 5 (C2b): Delete `/cloud/*` proxy routes in codex-exec-edge

**Files:**
- Modify: `internal/codexececdge/server.go` lines 57-59

**Interfaces:**
- Produces: edge no longer proxies `/cloud/*` register requests. agentx clients
  bypass edge entirely for register (they have their own 1→30 s exponential
  backoff retry); WS bridge `/codex-exec/{exe_id}` → also gone per same logic
  (verify).

- [ ] **Step 1: Examine current edge routes**

```bash
sed -n '50,62p' internal/codexececdge/server.go
```

Confirm three routes:

```go
r.Get("/codex-exec/{exe_id}", s.handleWSProxy)
r.Post("/cloud/executor/{exe_id}/register", s.handleRegisterProxy)
r.Post("/cloud/environment/{env_id}/register", s.handleRegisterProxy)
```

- [ ] **Step 2: Decide WS proxy fate**

Spec C2b says: "Also delete the corresponding wsproxy route if it carries
`/codex-exec/` only (verify; bridge stays direct to gateway)."

Check whether the WS proxy adds value beyond what the new gateway path
`/agentx/{exe_id}` provides:

```bash
grep -A 30 "func.*handleWSProxy" internal/codexececdge/wsproxy.go 2>/dev/null | head -40
```

If `handleWSProxy` does only path-passthrough (no auth tweaks, no
buffering not present in gateway), delete it. Otherwise leave it and just
rename `/codex-exec/` to `/agentx/`.

For the agentx project we want minimum surface. Delete it and let agentx
WS-dial the gateway directly. The 40-70 s Recreate-strategy gap is covered
by agentx's own register retry (1→30 s exponential backoff) and WS reconnect.

- [ ] **Step 3: Delete the routes + handlers**

Edit `internal/codexececdge/server.go`:

```go
// DELETE these three lines:
r.Get("/codex-exec/{exe_id}", s.handleWSProxy)
r.Post("/cloud/executor/{exe_id}/register", s.handleRegisterProxy)
r.Post("/cloud/environment/{env_id}/register", s.handleRegisterProxy)
```

Then delete the handler implementations they pointed to. Search:

```bash
grep -rn "handleRegisterProxy\|handleWSProxy" internal/codexececdge/
```

`git rm` any file that becomes empty after deleting the handler bodies.
Common candidates: `registerproxy.go`, `wsproxy.go`.

- [ ] **Step 4: Decide edge's purpose post-cut**

If the only remaining routes in `internal/codexececdge/server.go` are
healthcheck / readiness, the whole edge service may be deletable. Check:

```bash
sed -n '1,100p' internal/codexececdge/server.go
```

For Part 2 scope, keep the edge service running (with reduced routes) so
the deployment / k8s service stays intact — deleting it would expand PR
scope into deployment templates. If edge becomes empty after this task,
add a TODO note pointing to a follow-up task to retire the service entirely.

- [ ] **Step 5: Build + test**

```bash
go build ./... 2>&1 | tail
go test ./internal/codexececdge/... 2>&1 | tail
```

Update / delete tests that exercised the deleted handlers.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "C2b: delete /cloud/* and /codex-exec/{id} proxy routes in edge

codex-exec-edge no longer proxies register or bridge for clients. agentx
has its own register-retry loop (1→30 s exponential backoff in
agentx exec-server/src/remote.rs:427) and connects directly to the
gateway. Edge service kept running for healthcheck routes; full retirement
deferred to a follow-up."
```

---

### Task 6 (C3): Rename `cloud_register.go` → `agentx_register.go`; delete `security_profile`

**Files:**
- Rename: `internal/codexexecgateway/handlers/cloud_register.go` → `agentx_register.go`
- Rename: `internal/codexexecgateway/handlers/cloud_register_test.go` → `agentx_register_test.go`
- Modify: rename `CloudRegister` fn → `AgentxRegister`; rename `CloudRegisterStore` → `AgentxRegisterStore`; delete `security_profile` field from any response struct

**Interfaces:**
- Consumes: `Server.cloudRegister` call site in `server.go` (Task 4).
- Produces: `handlers.AgentxRegister` is the only register handler.

- [ ] **Step 1: `git mv` the files**

```bash
git mv internal/codexexecgateway/handlers/cloud_register.go \
       internal/codexexecgateway/handlers/agentx_register.go
git mv internal/codexexecgateway/handlers/cloud_register_test.go \
       internal/codexexecgateway/handlers/agentx_register_test.go
```

- [ ] **Step 2: Rename symbols in those files**

```bash
sed -i \
  -e 's/\bCloudRegister\b/AgentxRegister/g' \
  -e 's/\bCloudRegisterStore\b/AgentxRegisterStore/g' \
  internal/codexexecgateway/handlers/agentx_register.go \
  internal/codexexecgateway/handlers/agentx_register_test.go
```

- [ ] **Step 3: Update comments in those files**

```bash
sed -i \
  -e 's|/cloud/executor/{exe_id}/register|/agentx/environment/{env_id}/register|g' \
  -e 's|/cloud/environment/{env_id}/register|/agentx/environment/{env_id}/register|g' \
  internal/codexexecgateway/handlers/agentx_register.go
```

Open the file and review the doc comment on `AgentxRegister`; rewrite to
say "agentx register" not "upstream-compat" or "codex 0.132".

- [ ] **Step 4: Update call sites**

```bash
grep -rn "CloudRegister\b\|CloudRegisterStore" internal/codexexecgateway/
```

Expected: `server.go` calls `handlers.CloudRegister(...)`. Patch:

```bash
sed -i 's/handlers\.CloudRegister\b/handlers.AgentxRegister/g' \
  internal/codexexecgateway/server.go
sed -i 's/handlers\.CloudRegisterStore\b/handlers.AgentxRegisterStore/g' \
  internal/codexexecgateway/server.go
```

Repeat grep to confirm zero hits left.

- [ ] **Step 5: Delete `security_profile` field if present in response struct**

```bash
grep -rn "security_profile\|securityProfile\|SecurityProfile" internal/codexexecgateway/
```

Expected hits (already deleted in Task 2 / Task 3):
- Anything still in `agentx_register.go` response struct → delete the field
  + the assignment line that sets it.
- Tests asserting the field → delete the assertion.

If grep returns empty, this step is a no-op — confirm and move on.

- [ ] **Step 6: Build + test**

```bash
go build ./... 2>&1 | tail
go test ./internal/codexexecgateway/... 2>&1 | tail
```

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "C3: rename CloudRegister → AgentxRegister + drop security_profile

Renames handlers/cloud_register.go → agentx_register.go; CloudRegister →
AgentxRegister; CloudRegisterStore → AgentxRegisterStore. Updates the
single call site in server.go. Deletes any security_profile field that
survived the noise removal."
```

---

### Task 7 (C4): Rewrite ConnectCommands template

**Files:**
- Modify: `internal/server/codex_executors.go` lines 161-179

**Interfaces:**
- Consumes: existing `aiResult.JWT`, `s.CodexExecGatewayPublicHost`, `s.CodexAuthIssuerURL`, `reg.ExeID`, `req.Name`.
- Produces: `resp.ConnectCommands.AgentIdentity` is the agentx connect command from spec §6.5.

- [ ] **Step 1: Write the failing test**

Add to `internal/server/codex_executors_test.go` (create the file if not
present in the directory — verify with `ls internal/server/`):

```go
func TestConnectCommands_EmitsAgentxCommand(t *testing.T) {
    s := &Server{
        CodexExecGatewayPublicHost: "x.agent.cs.ac.cn",
        CodexAuthIssuerURL:         "https://codex-auth.agent.cs.ac.cn",
    }
    cmd := buildConnectCommand(s, "fake-jwt", "exe_xxx", "my-laptop")

    if !strings.HasPrefix(cmd, "export AGENTX_ACCESS_TOKEN='fake-jwt'") {
        t.Errorf("missing AGENTX_ACCESS_TOKEN export; got: %s", cmd)
    }
    if !strings.Contains(cmd, "agentx --remote 'https://x.agent.cs.ac.cn'") {
        t.Errorf("missing agentx --remote invocation; got: %s", cmd)
    }
    if !strings.Contains(cmd, "--agent-identity-authapi-base-url 'https://codex-auth.agent.cs.ac.cn'") {
        t.Errorf("missing --agent-identity-authapi-base-url; got: %s", cmd)
    }
    if strings.Contains(cmd, "codex -c chatgpt_base_url") {
        t.Errorf("still emitting legacy codex command; got: %s", cmd)
    }
    if strings.Contains(cmd, "CODEX_ACCESS_TOKEN") {
        t.Errorf("still emitting legacy CODEX_ACCESS_TOKEN env; got: %s", cmd)
    }
}
```

(If `internal/server/codex_executors_test.go` doesn't exist yet, this test
file's `package server` declaration may need adjustment; use `package server`
to match the production file.)

- [ ] **Step 2: Extract `buildConnectCommand` helper from inline template**

In `internal/server/codex_executors.go`, the current code inlines the
`fmt.Sprintf` at lines 173-176. Add this helper as a **top-level
function in the same file** (`internal/server/codex_executors.go`,
`package server` — NOT in the test file):

```go
// buildConnectCommand returns the one-line shell command shown to users
// in the "Add Executor" UI; the connect_command field of registerExecutorResp.
func buildConnectCommand(s *Server, jwt, exeID, name string) string {
    gatewayURL := "https://" + s.CodexExecGatewayPublicHost
    issuer := s.CodexAuthIssuerURL
    return fmt.Sprintf(
        "export AGENTX_ACCESS_TOKEN='%s'\nexport AGENTX_AGENT_IDENTITY_ALLOWED_BASE_URLS='%s'\nagentx --remote '%s' --environment-id '%s' --name '%s' --use-agent-identity-auth --agent-identity-authapi-base-url '%s'",
        jwt, issuer, gatewayURL, exeID, name, issuer)
}
```

Then in the `handleRegisterExecutor` body, replace lines 173-178:

```go
// OLD:
//   resp.ConnectCommands = ConnectCommands{
//       AgentIdentity: fmt.Sprintf("export CODEX_ACCESS_TOKEN=... codex ...", ...),
//   }
//   resp.ConnectCommand = resp.ConnectCommands.AgentIdentity
// NEW:
resp.ConnectCommands = ConnectCommands{
    AgentIdentity: buildConnectCommand(s, aiResult.JWT, reg.ExeID, req.Name),
}
resp.ConnectCommand = resp.ConnectCommands.AgentIdentity
```

- [ ] **Step 3: Update the docstring at line 162-170 above the template**

```go
// Replace the old comment block (lines 162-170 reference the codex
// `exec-server --remote` contract) with:
// agentx connect command:
//   1. POST <base_url>/agentx/environment/{env_id}/register with
//      the Agent Identity JWT (Authorization: AgentAssertion ...).
//   2. Server validates the JWT, returns {executor_id, url} with a
//      short-lived HMAC ticket in ?token=.
//   3. agentx ws-dials url; inbound verifies the HMAC ticket.
//
// AGENTX_AGENT_IDENTITY_ALLOWED_BASE_URLS env unlocks the
// agent-identity auth flow against a non-chatgpt.com issuer (we use
// codex-auth.agent.cs.ac.cn).
```

- [ ] **Step 4: Run the test to verify it passes**

```bash
go test -run TestConnectCommands_EmitsAgentxCommand ./internal/server/... -v
```

Expected: PASS.

- [ ] **Step 5: Build + full test**

```bash
go build ./... 2>&1 | tail
go test ./internal/server/... 2>&1 | tail
```

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "C4: ConnectCommands emits agentx command, not codex

Extract buildConnectCommand helper. Template now produces:
  export AGENTX_ACCESS_TOKEN='<jwt>'
  export AGENTX_AGENT_IDENTITY_ALLOWED_BASE_URLS='<issuer>'
  agentx --remote '<gateway>' --environment-id '<exe>' --name '<name>' \\
         --use-agent-identity-auth \\
         --agent-identity-authapi-base-url '<issuer>'

Replaces the codex exec-server --remote command shown to users in the
'Add Executor' UI. Frontend RemoteExecutorsPanel.tsx consumes this
verbatim and will display the new command on day 1 with no further
changes."
```

---

### Task 8 (C5): Integration test switches `codex` binary → `agentx`

**Files:**
- Modify: `internal/codexauth/integration_test.go` lines 21, 30, 54, 64-72
- Modify: agentserver CI workflow (likely `.github/workflows/test.yml` or `ci.yml` — locate)

**Interfaces:**
- Consumes: agentx v0.0.1 release tarball from GitHub (verified to exist in Task 1 step 1).
- Produces: integration test exercises agentx, not codex.

- [ ] **Step 1: Read current test setup**

```bash
sed -n '20,80p' internal/codexauth/integration_test.go
```

Confirm: `exec.LookPath("codex")` at line 30, `exec.CommandContext(ctx, "codex", ...)` at line 64, env `CODEX_ACCESS_TOKEN` at line 72.

- [ ] **Step 2: Swap binary + env names in test**

```bash
sed -i \
  -e 's/exec\.LookPath("codex")/exec.LookPath("agentx")/g' \
  -e 's/exec\.CommandContext(ctx, "codex"/exec.CommandContext(ctx, "agentx"/g' \
  -e 's/"CODEX_ACCESS_TOKEN=/"AGENTX_ACCESS_TOKEN=/g' \
  -e 's/"CODEX_AGENT_IDENTITY_AUTHAPI_BASE_URL=/"AGENTX_AGENT_IDENTITY_AUTHAPI_BASE_URL=/g' \
  internal/codexauth/integration_test.go
```

Then open the file and review:

- Any `-c chatgpt_base_url=` arg (codex used `-c key=val` config override
  syntax) → agentx doesn't have `-c`. Replace with the agentx flag form.
- Any `exec-server` subcommand arg in argv → drop (agentx flattens it).
- Any `--executor-id` flag → confirm agentx accepts the same name
  (`--environment-id` in v0.142 — verify what we kept in Part 1).
- Doc comment at line 21 (`// invokes the real \`codex exec-server --remote ...\``)
  → rewrite to reference agentx.

Final argv should look like:

```go
cmd := exec.CommandContext(ctx, "agentx",
    "--remote", url,
    "--environment-id", exeID,
    "--name", "test-agent",
    "--use-agent-identity-auth",
    "--agent-identity-authapi-base-url", issuer,
)
cmd.Env = append(os.Environ(),
    "AGENTX_ACCESS_TOKEN="+mint.JWT,
    "AGENTX_AGENT_IDENTITY_ALLOWED_BASE_URLS="+issuer,
)
```

- [ ] **Step 3: Locate agentserver CI workflow**

The single CI workflow in this repo is `.github/workflows/build.yml`.
The `Test` job at ~line 34-35 runs `go test ./... -count=1 -timeout 5m`,
which covers the codexauth integration test. There is no separate `test.yml`
or `ci.yml`.

```bash
# Sanity check before editing:
sed -n '30,45p' .github/workflows/build.yml
```

- [ ] **Step 4: Update CI to download agentx tarball**

In the CI job that runs `go test ./internal/codexauth/...`, add a setup
step before the test:

```yaml
- name: Install agentx (for codexauth integration test)
  run: |
    set -euo pipefail
    curl -fsSL https://github.com/agentserver/agentx/releases/latest/download/install.sh -o /tmp/install-agentx.sh
    AGENTX_INSTALL_DIR=/tmp/bin sh /tmp/install-agentx.sh
    echo "/tmp/bin" >> $GITHUB_PATH
    agentx --version 2>&1 | head
```

(Use `latest` rather than pinning to `v0.0.1`. If reproducibility matters,
pin to `v0.0.1` here for now.)

If a previous step installed codex binary, delete that step.

- [ ] **Step 5: Run the integration test locally (smoke)**

If you have `agentx` installed locally (from Part 1 install.sh), and a
running staging codexauth instance:

```bash
go test -run TestAgentIdentityIntegration ./internal/codexauth/... -v
```

If you don't have agentx locally, skip — CI will exercise it.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "C5: integration test switches from codex to agentx binary

Updates internal/codexauth/integration_test.go to LookPath('agentx'),
exec agentx instead of codex, set AGENTX_* env vars. argv aligned to
agentx CLI shape (no exec-server subcommand, --agent-identity-authapi-base-url
flag).

CI workflow installs agentx via the official install.sh from
github.com/agentserver/agentx/releases/latest before running the test."
```

---

### Task 9 (C6): values.yaml + default publicHost

**Files:**
- Modify: `deploy/helm/agentserver/values.yaml`

**Interfaces:**
- Produces: chart no longer accepts `noiseRelayEnabled` / `noiseRelayHmacKey` keys; default `publicHost` for codexExecGateway points at the new host pattern.

- [ ] **Step 1: Locate noise-related fields**

```bash
grep -n "noiseRelay\|noise-relay\|publicHost" deploy/helm/agentserver/values.yaml
```

Expected hits:
- ~line 292: comment block for `noiseRelayHmacKey`
- ~line 293: `noiseRelayHmacKey: ""`
- ~lines 388-394: comment block for `noiseRelayEnabled`
- ~line 395: `noiseRelayEnabled: false`
- somewhere: `publicHost: "codex-exec.{{ .Values.ingress.host }}"` or similar

- [ ] **Step 2: Delete noise fields + their comment blocks**

Open `deploy/helm/agentserver/values.yaml`. Delete:

1. The comment block immediately above `noiseRelayHmacKey: ""` (the multi-line
   comment that describes what it's for) AND the line itself.
2. The comment block immediately above `noiseRelayEnabled: false` AND the line itself.

- [ ] **Step 3: Update default `publicHost` for codexExecGateway**

Find the `codexExecGateway:` block and its `publicHost:` field. Change the
default from `codex-exec.<ingress.host>` to `x.<ingress.host>` (or whatever
templating syntax this chart uses — match the existing style for other
gateways like `codex-app.<ingress.host>`).

If the chart's `publicHost` field uses an explicit string default like
`"codex-exec.example.com"`, change it to `"x.example.com"`. If empty default
(operator overrides per stack), update the inline comment to reference the
new pattern.

- [ ] **Step 4: Helm-lint the chart**

```bash
helm lint deploy/helm/agentserver/
```

Expected: green.

- [ ] **Step 5: Render the chart with defaults to confirm no template breakage**

```bash
helm template test deploy/helm/agentserver/ > /tmp/agentserver-rendered.yaml
grep -i "noise\|publicHost" /tmp/agentserver-rendered.yaml | head
```

Expected: no `noise` mentions; `publicHost` reflects the new pattern.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "C6: values.yaml — delete noise fields, default publicHost → x.<host>

codexGateway.noiseRelayHmacKey and codexExecGateway.noiseRelayEnabled
removed (no longer recognized by chart 0.70.0). publicHost default
pattern changed from codex-exec.<ingress.host> to x.<ingress.host>.
Operator overrides per-stack in Pulumi (Part 3)."
```

---

### Task 10 (C7): Secret template — drop `noise-relay-hmac-key`

**Files:**
- Modify: `deploy/helm/agentserver/templates/codex-gateway-secret.yaml`

**Interfaces:**
- Produces: rendered Secret has 3 keys (inbound, captok, intern) instead of 4 (no noise).

- [ ] **Step 1: Read current template**

```bash
cat deploy/helm/agentserver/templates/codex-gateway-secret.yaml
```

- [ ] **Step 2: Delete noise-related lines**

Delete:
- The header comment block listing `noise-relay-hmac-key` (lines 10-14 mention the noise key).
- Line ~24: `{{- $noise := default "" .Values.codexGateway.noiseRelayHmacKey }}`
- Line ~28: the `{{- if and (not $noise) ... }}` block for noise preservation
- Line ~32: the `{{- if not $noise }}{{- $noise = randAlphaNum 48 }}{{- end }}` line
- Line ~44: `noise-relay-hmac-key: {{ $noise | quote }}`

Also remove any other `$noise` references that become unreachable.

- [ ] **Step 3: Helm-template to verify**

```bash
helm template test deploy/helm/agentserver/ | grep -A 20 'name:.*codex-gateway' | head -30
```

Expected: rendered Secret has `inbound-hmac-secret`, `cap-token-hmac-secret`,
`internal-shared-secret`. No `noise-relay-hmac-key`.

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "C7: codex-gateway-secret.yaml — drop noise-relay-hmac-key

Deletes the noise key field from stringData and the preservation logic
(lookup() block + randAlphaNum 48 fallback). Updates the header comment
to list the remaining 3 keys. Upgrading from chart 0.69.x to 0.70.0
leaves the now-orphaned key in the live Secret (k8s won't auto-prune);
that's harmless — nothing reads it."
```

---

### Task 11 (C8): codex-exec-gateway.yaml — drop noise env

**Files:**
- Modify: `deploy/helm/agentserver/templates/codex-exec-gateway.yaml`

**Interfaces:**
- Produces: rendered Deployment env block has no `CXG_NOISE_RELAY_HMAC_KEY`.

- [ ] **Step 1: Read the noise block**

```bash
sed -n '95,130p' deploy/helm/agentserver/templates/codex-exec-gateway.yaml
```

Expected: the block lines 102-114 wrapped in `{{- if .Values.codexExecGateway.noiseRelayEnabled }}` / `{{- end }}`.

- [ ] **Step 2: Delete the entire conditional**

In `deploy/helm/agentserver/templates/codex-exec-gateway.yaml`, delete:

```yaml
{{- if .Values.codexExecGateway.noiseRelayEnabled }}
... (comment lines 107-108)
- name: CXG_NOISE_RELAY_HMAC_KEY
  valueFrom:
    secretKeyRef:
      name: {{ .Release.Name }}-codex-gateway
      key: noise-relay-hmac-key
{{- end }}
```

Then update the comment block at lines 124-125 / 128 referencing
`/cloud/executor/{id}/register` → `/agentx/environment/{env_id}/register`.

- [ ] **Step 3: Helm-template to verify**

```bash
helm template test deploy/helm/agentserver/ | grep -A 5 "name:.*CXG_" | head
```

Expected: no `CXG_NOISE_RELAY_HMAC_KEY` entries.

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "C8: codex-exec-gateway.yaml — drop noise env block

Removes the {{- if .Values.codexExecGateway.noiseRelayEnabled }} ... {{- end }}
block that mounted CXG_NOISE_RELAY_HMAC_KEY into the pod env. Updates the
comment that referenced /cloud/* paths to /agentx/*."
```

---

### Task 12 (C9): Chart.yaml — bump 0.69.9 → 0.70.0

**Files:**
- Modify: `deploy/helm/agentserver/Chart.yaml`

**Interfaces:**
- Produces: chart version 0.70.0, app version 0.70.0.

- [ ] **Step 1: Edit Chart.yaml**

```yaml
# deploy/helm/agentserver/Chart.yaml
version: 0.70.0
appVersion: "0.70.0"
```

- [ ] **Step 2: Verify**

```bash
grep -E "^version|^appVersion" deploy/helm/agentserver/Chart.yaml
```

Expected: both at 0.70.0.

- [ ] **Step 3: Commit**

```bash
git add -A
git commit -m "C9: Chart.yaml bump 0.69.9 → 0.70.0

Breaking version bump: chart 0.70.0 contains hard-cut path swap
(/cloud/* → /agentx/*), noise relay removal, and renamed register
handler. Pulumi will pin this version in Part 3."
```

---

### Task 13 (C11): README + README.zh.md update

**Files:**
- Modify: `README.md`
- Modify: `README.zh.md` (if it exists)

**Interfaces:**
- Produces: connect command shown in README matches what the UI emits (§6.5).

- [ ] **Step 1: Locate codex / codex-exec mentions in READMEs**

```bash
grep -n "codex exec-server\|codex-exec-gateway\|codex-exec\.agent\.cs\.ac\.cn\|exec-server --remote" README.md README.zh.md 2>/dev/null
```

- [ ] **Step 2: Replace connect-command examples**

For each section that shows the user a `codex exec-server --remote ...`
command, replace with the agentx form:

```bash
export AGENTX_ACCESS_TOKEN='<jwt>'
export AGENTX_AGENT_IDENTITY_ALLOWED_BASE_URLS='https://codex-auth.agent.cs.ac.cn'
agentx --remote 'https://x.agent.cs.ac.cn' \
  --environment-id 'exe_xxx' --name 'my-laptop' --use-agent-identity-auth \
  --agent-identity-authapi-base-url 'https://codex-auth.agent.cs.ac.cn'
```

And the install one-liner:

```bash
curl -fsSL https://github.com/agentserver/agentx/releases/latest/download/install.sh | sh
```

Also update hostname references: `codex-exec.agent.cs.ac.cn` →
`x.agent.cs.ac.cn`.

- [ ] **Step 3: Find broader codex mentions**

```bash
grep -n "codex-exec-gateway\|codex-exec " README.md README.zh.md 2>/dev/null
```

These are SERVICE NAMES (internal k8s service names) which we're NOT
renaming in this PR — they're still called `codex-exec-gateway` /
`codex-exec-edge` in the helm chart and k8s manifests. So README mentions
of those names as service references can stay. Only update USER-FACING
hostnames + connect commands.

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "C11: README updates — agentx connect command + new host

Replaces 'codex exec-server --remote codex-exec.agent.cs.ac.cn ...'
with the agentx form + new gateway host x.agent.cs.ac.cn. Adds the
GitHub Releases install one-liner. Internal service names
(codex-exec-gateway, codex-exec-edge) left unchanged — those are
cluster-internal and not affected by the user-facing rename."
```

---

### Task 14: Full repo build + test

**Files:** none (verification)

**Interfaces:** confirms PR is buildable end to end before opening.

- [ ] **Step 1: Clean build**

```bash
cd /root/agentserver
go build ./... 2>&1 | tail -10
```

Expected: no errors.

- [ ] **Step 2: Vet**

```bash
go vet ./... 2>&1 | tail -10
```

Expected: no errors.

- [ ] **Step 3: Full test suite (excluding integration tests that need network)**

```bash
go test ./... 2>&1 | tail -40
```

Expected: green except possibly the codexauth integration test that wants
the agentx binary on PATH (this runs in CI; not gated locally).

If other tests fail, fix the test (likely a stale `/cloud/...` URL or noise
type reference). Iterate.

- [ ] **Step 4: Helm-lint**

```bash
helm lint deploy/helm/agentserver/
```

Expected: green.

- [ ] **Step 5: Helm template render full chart**

```bash
helm template test deploy/helm/agentserver/ > /tmp/full-render.yaml
grep -ci "noise\|security_profile" /tmp/full-render.yaml
```

Expected: 0 (or only inside comments — verify each remaining hit).

- [ ] **Step 6: grep entire repo for stale refs**

```bash
grep -rnE '"/cloud/|"/codex-exec/|security_profile|NoiseRelay|CXG_NOISE' \
  --include='*.go' --include='*.yaml' --include='*.ts' . | \
  grep -v '\.worktrees\|node_modules\|vendor/\|\.git/'
```

Expected: 0 hits (or only inside `docs/superpowers/` spec files which intentionally describe the removed surfaces).

If non-zero, classify each and patch.

---

### Task 15: Open the PR

**Files:** none (publish)

**Interfaces:**
- Produces: PR open against `main` with title `feat(agentx): drop noise+cloud, adopt /agentx/* paths, new gateway host`, ready for review.

- [ ] **Step 1: Push the branch**

```bash
git push -u origin feat/agentx-cutover
```

- [ ] **Step 2: Create PR via gh CLI**

```bash
gh pr create \
  --base main \
  --head feat/agentx-cutover \
  --title "feat(agentx): drop noise+cloud, adopt /agentx/* paths, new gateway host" \
  --body "$(cat <<'EOF'
Implements spec docs/superpowers/specs/2026-06-23-agentx-extraction-design.md
(Phase B of three; see plans/2026-06-23-agentx-extraction-part2-agentserver-pr.md).

## Summary

Switches agentserver's codex-exec-gateway off the noise relay path and onto
plaintext JSON-RPC over WebSocket at new paths and hostname. Removes the
upstream-codex-only register/bridge shape; the agentx binary (Part 1 of
this project, released at github.com/agentserver/agentx v0.0.1) is the only
supported client going forward.

## Commits

- C1a: delete noise/, noise_handlers.go, noise_router.go, noise_store.go,
       relaypb/, all _noise_*_test.go (intermediate non-building commit)
- C1b: unwire noise references in server.go / config.go / bridge.go /
       inbound.go / auth.go
- C2:  /cloud/{exec,env}/{id}/register → /agentx/environment/{env_id}/register;
       ws /codex-exec/{exe_id} → /agentx/{exe_id}
- C2b: delete /cloud/* + /codex-exec/{id} proxy routes in codex-exec-edge
- C3:  rename handlers/cloud_register.go → agentx_register.go;
       CloudRegister → AgentxRegister; drop security_profile field
- C4:  ConnectCommands template emits agentx command (not codex)
- C5:  codexauth integration test invokes agentx binary; CI downloads
       agentx latest tarball
- C6:  values.yaml drops noiseRelayEnabled / noiseRelayHmacKey; default
       publicHost → x.<ingress.host>
- C7:  codex-gateway-secret.yaml drops noise-relay-hmac-key field +
       preservation logic
- C8:  codex-exec-gateway.yaml drops {{- if noiseRelayEnabled }} env block
- C9:  Chart.yaml bump 0.69.9 → 0.70.0
- C11: README updates with new connect command + host

## NOT in this PR

- Pulumi changes in /root/k8s (covered by Part 3)
- DNS A record for x.agent.cs.ac.cn (Part 3 manual step C10)

## Reviewer notes

- Reality check (vs spec): noise touched 8 production .go files, not 2.
  Bridge.go had a useNoise fork + serveBridgeViaNoise helper that needed
  removal; spec C1 description simplified what's actually a ~250 LOC
  surgery.
- One commit (C1a) intentionally leaves the package non-building so the
  delete diff stays readable. C1b restores buildability. Squash on merge
  if you prefer linear history.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 3: Wait for CI to pass**

Open the PR URL printed by `gh pr create`. Wait for the agentserver CI
workflow to complete. The codexauth integration test step will pull agentx
v0.0.1 from GitHub Releases — verify this step runs and passes.

If CI fails, fix and push again. Do not request review until CI is green.

- [ ] **Step 4: Request review**

```bash
gh pr edit --add-reviewer @<reviewer-handles>
```

(Set reviewer per team convention.)

---

### Task 16: Merge + verify chart artifact

**Files:** none (post-merge verification)

**Interfaces:**
- Produces: chart `agentserver-0.70.0.tgz` published to OCI registry; image
  `ghcr.io/agentserver/agentserver:0.70.0` (and sibling images) built.

- [ ] **Step 1: After review approval, merge the PR**

Either squash or merge — both fine per spec. (`gh pr merge --squash` or
`--merge`.)

- [ ] **Step 2: Wait for the release pipeline**

agentserver's release workflow triggers on merge to main (or on chart bump
— verify which by reading `.github/workflows/release.yml` or equivalent).
Wait for chart 0.70.0 to appear in the OCI registry:

```bash
# Adjust to actual registry URL pattern:
crane ls oci://ghcr.io/agentserver/charts/agentserver | grep 0.70.0
# OR
helm pull oci://ghcr.io/agentserver/charts/agentserver --version 0.70.0 -d /tmp/
```

Expected: 0.70.0 available.

- [ ] **Step 3: Verify image tags**

```bash
crane ls ghcr.io/agentserver/agentserver | grep 0.70.0
crane ls ghcr.io/agentserver/codex-exec-gateway | grep 0.70.0
crane ls ghcr.io/agentserver/codex-app-gateway | grep 0.70.0
crane ls ghcr.io/agentserver/codex-exec-edge | grep 0.70.0
crane ls ghcr.io/agentserver/envmcp-public-gateway | grep 0.70.0
```

Expected: 0.70.0 tag present on each.

- [ ] **Step 4: Sign off Part 2**

Stop here. Confirm with user before starting Part 3.

Status to report:
- PR URL + merge commit hash
- Chart 0.70.0 + image tags verified available
- CI green confirmation
- Any reviewer feedback that was addressed

Get **explicit user OK** before opening Part 3
(`docs/superpowers/plans/2026-06-23-agentx-extraction-part3-pulumi-cutover.md`).

## Rollback notes (Part 2)

Part 2 changes the agentserver main branch and produces chart 0.70.0
artifacts. Nothing in cs.ac.cn changes (Pulumi hasn't been edited).
Rollback is straightforward:

- **Pre-merge**: just close the PR. Branch can be deleted or kept.
- **Post-merge, pre-Pulumi**: `git revert <merge-sha>`, push another PR
  with the revert, merge. Chart 0.70.0 artifacts stay in the registry —
  they're harmless if not deployed.
- **If chart 0.70.0 was published but never deployed**: leave it; or
  manually delete from registry (low priority).

True rollback cost: ~10 minutes for the revert PR + CI. agentserver users
unaffected because nothing in cs.ac.cn references 0.70.0 yet.

The high-risk rollback window opens in Part 3 (post pulumi up). Part 2
itself is reversible.
