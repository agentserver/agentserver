# Workspace repository implementation plan

Status: in progress. This document is a working implementation plan, not a claim of deployed support.

## Publication audit (2026-10-09)

The original `818ae24e` artifacts were built but not deployed: the publication
audit found missing source projections, pointer-address authority comparisons,
an unregistered preparation capability action, a missing production preparer,
and a worker that ignored MCP project instructions. Passing the old unit suite
was not proof of an operational repository session.

The follow-up corrects those seams and adds tests for project context reaching
the model before any tool, rejection of missing context, stable authority
across JSON decoding, repository-root reads for ancestor skills, and live
PostgreSQL checks for the exact frozen repository/attempt/sandbox/lease before
Git credential materialization. Both allow/deny credential decisions have a
dedicated audit table without fake tool-call/execution IDs. Root descriptor
comparison for the new repository profile survives JSONB/HTTP whitespace.

A temporary SG Pod using the deployed non-root seccomp/AppArmor/bubblewrap
profile passed real Linux read-only/write enforcement, secret-descriptor and
runtime-file isolation, background-process cleanup, managed CLI smoke tests,
and repository file projection tests. It contained only synthetic test data and
was deleted after the checks. Production services have not yet been upgraded.

## Requested behavior

- A workspace can configure a Git repository, for example `https://code.byted.org/tce/rtm-aihub`, a ref and a relative working directory.
- Sessions inherit the workspace default and can select their own working directory without changing an active run.
- The executor owns the checkout and all source writes. Each managed session has independent persistent files; an idle sandbox expiry must not delete them.
- Core stores Git credentials through the existing sealed workspace credential service. The user chose web-based Git credential configuration and automatic cloning; no secrets belong in repository URLs, logs or harness prompts.
- The harness deterministically receives project instructions and a skill index before model work. It must not depend on its empty local cwd or an instruction that merely asks the model to discover files later.

## Planned implementation order

1. Finish the already requested removal of the stale managed read-only instructions and keep rebuilt harness/runtime packs synchronized.
2. Add repository/default-directory validation, versioned Core storage, API and Platform settings, and a Git credential provider.
3. Add executor-owned, session-isolated checkout preparation and persistent managed storage. Preserve edits on resume; never reset an existing dirty checkout during automatic preparation.
4. Add bounded remote context discovery: repository root through selected cwd for `AGENTS.override.md` / `AGENTS.md`; exact project skill manifests in supported roots; deterministic ordering and explicit truncation/errors.
5. Pass source-attributed project instructions and skill metadata to fresh/resumed Codex turns. Full skill content/scripts remain on the executor and are read or executed on demand.
6. Verify validation/CAS, private clone credential handling, session isolation, persistence across sandbox recreation, context freshness and the end-to-end tool cwd/permissions projection.

## Starting-point gaps verified in source

- `harnessworker/workspace_instructions.go` currently provides skill-search guidance only; it does not pre-read project `AGENTS.md` or index skills.
- `coredb/state_workspace_binding.go` originally qualified only AgentX for session working-directory bindings.
- Managed Kubernetes `/workspace` was an `emptyDir` and the runtime image did not include Git.
- Upstream claim-generated PVCs are owned by the Sandbox. They cannot be used without a separate storage lifecycle if deleting an idle claim would delete the checkout.

## Implemented locally (not deployed)

- Versioned workspace repository GET/PATCH, owner-only CAS updates and audit
  events; an explicit `source: null` clears only the setting. Missing PATCH
  fields are rejected rather than accidentally clearing a setting.
- Sealed `git` / `https-token` credential provider bound to `code.byted.org`;
  manual Platform credential entry and repository settings UI, with generated
  OpenAPI types and strict response validation. The UI explains inheritance and
  next-run semantics directly.
- `repositorycheckout`: real Git initial fetch/checkout into private staging,
  host-scoped auth only during fetch, disabled redirects/helpers/hooks/global
  Git config, sanitized errors, atomic publication, and dirty-checkout resume
  without executing Git or requiring a token. Only the checkout tree is meant
  to be mounted into model processes; metadata stays outside it.
- `workspacecontext`: bounded root-to-cwd AGENTS loading, override precedence,
  deterministic skill metadata indexing, contained skill symlinks with cycle
  detection, explicit limit/invalid-skill diagnostics and lazy skill bodies.
  More-specific directories override same-name root skills; `.agents/skills`
  wins over compatibility roots at the same directory. Global service-home
  files are never scanned. Cwd symlink components are rejected as ambiguous.
- Runtime mTLS preflight endpoints `/internal/runtime/repository/prepare` and
  `/internal/runtime/repository/context`; session/incarnation checks and
  exclusion against active tools. They do not fabricate Codex tool-call IDs.
  After preparation, reads and bubblewrap use the checkout tree at the stable
  guest `/workspace` path. Preparation is disabled unless the dedicated
  `/var/lib/agentserver/repositories` volume is configured.
- Runtime image recipes now include Git. No image build or release was done.
- Gateway MCP initialization now prepares a repository-backed managed sandbox
  before the first model tool call. The preparation capability is separate from
  model/tool call IDs; it is scoped to the signed session/run target, and its
  bounded AGENTS/skill index is exposed as server instructions while skill
  bodies remain lazy executor reads. A repository session without a configured
  provider preparation path fails closed before the model can run.
- The sandbox gateway exposes a lifecycle-authorized repository preparation
  endpoint; the Kubernetes provider routes it to the runtime, which uses the
  persistent checkout and never exposes the host path. Git credentials are
  carried only for that preparation request and cleared immediately afterward.
- Core mounts a dedicated repository-credential endpoint. It checks the active
  run/session, session repository binding, environment and active workspace Git
  binding before opening the sealed token; executor-gateway calls it through
  its existing Core mTLS client and passes the result only to preparation.
- Session creation now snapshots configured repository defaults into
  `session_repositories` and initializes its bound environment/cwd. A later
  workspace default update does not change the old session; the existing
  working-directory API can change its repository-relative cwd. Generic
  environment changes cannot detach/rebind a persistent repository silently.
- Run creation freezes the full repository source document alongside cwd and
  includes `repositoryId` in workspace launch authority, run manifests and
  executor capabilities. The repository ID is constrained to the session and
  its CN/SG managed environment. MCP initialization consumes this frozen
  authority before model work.
- Kubernetes provider now has opt-in `repositoryStorageClass` /
  `repositoryStorageSize` configuration. It creates an independent per-session
  PVC with `ReadWriteOncePod`, derives a private template/zero-spare pool from
  the deployment-selected base, and mounts the PVC outside model-visible
  `/workspace`. PVCs have no ephemeral GC owner; derived pools/templates are
  owned by the PVC. All reused resources require matching session/workspace
  ownership and expected specs. Normal release preparation preserves an
  existing storage configuration; no live config was changed.
- Deployment rendering adds PVC/template/pool create/get rights only when
  repository storage is enabled. The gateway receives no PVC delete rights;
  runtime service accounts still have no API credentials or pods/exec path.

## Verified local evidence

- Go unit and race tests cover checkout resume with dirty files and an invalid
  user-edited `.git/config`, secret-free argv/config, failed-clone cleanup,
  preflight authority, busy rejection before operation acceptance, lazy skill
  reads, symlink escape/cycle rejection and AGENTS ordering/UTF-8 limits.
- Web: 48 tests, TypeScript checks and Platform Vite production build passed.
- Real PostgreSQL 17.11 in a disposable local Unix-socket instance: migration,
  owner/member checks, normalization, no-op, stale/concurrent CAS, audit rows,
  clearing and credential-kind/scope/revocation tests passed. The instance was
  stopped after the test; no production database was accessed.
- Real PostgreSQL tests also cover new-session inheritance, existing-session
  immutability/idempotency, per-session cwd overrides and a running turn's
  frozen cwd/source after a settings change. Repository manifest/capability
  tests reject cross-session and mismatched managed-environment authority.
- A second disposable PostgreSQL run after source-snapshot propagation passed
  the inheritance/CAS suite; the temporary server was stopped and no live
  database was touched.
- Kubernetes provider fake-client/HTTP tests passed; Linux runtime tests were
  cross-compiled. This does not replace a real Linux bubblewrap/PVC canary.
- Persistent-provider tests verify claim deletion/recreation reuses the same
  PVC, rejects foreign workspace ownership and tampered volume/pool specs,
  preserves the base template, and never relaxes the RWOP attachment fence.
- The managed instruction skill passed skill-creator's validator using a
  temporary PyYAML environment; build/publish shell syntax checks passed.
- Read-only scan of local `/Users/bytedance/projects/rtm-aihub` found seven
  valid skills. `check`, `document`, and `incident` have invalid YAML plain
  descriptions containing `: `, and are explicitly diagnosed. `.codex/skills`
  is an in-root symlink and is supported. The project was not modified.
- SG/CN StorageClass readback: both provide `longhorn` and `longhorn-static`,
  provisioner `driver.longhorn.io`, reclaim `Delete`, binding `Immediate`.

## Remaining integration work (completion requires all of this)

1. Finish session-facing integration: expose the repository binding to the UI,
   support explicit adoption by an existing unbound session, and surface cwd
   selection through DSH/Platform. New-session inheritance and DB/run freezing
   are implemented. A setting alone must not be represented as a successfully
   prepared working directory. Ensure the user-run service selects a pinned
   repository region before constructing any region-dependent Lark authority.
2. Finish integration and live validation of per-session PVC reattachment. The
   existing claim-generated PVC path is unsuitable because it follows Sandbox
   garbage collection. Derive an isolated template/zero-spare pool from the
   deployment's fixed template, and mount only that session's PVC at the
   private repository storage path (provider implementation is present). Validate resource ownership and fence old
   runtime incarnations before reuse; never return a user tree to a warm pool.
   PVC expiry/region migration semantics must be explicit and must not silently
   erase or replace edits. Existing CN/SG region selection must still work.
   An optional user question is outstanding: should existing Git sessions stay
   in their storage region when workspace default region changes (recommended),
   or must the first version implement cross-region data migration?
3. Extend the Core repository resolver's audit event and add a production
   canary for private Codebase cloning. Git credentials are authorized only
   through the dedicated repository endpoint, never arbitrary shell
   `PROCESS_ENV` calls.
4. Verify production deployments load the Core-backed resolver into
   `ManagedSandboxProvisioningSpec.RepositoryCredential`; profiled
   executor-gateway wiring now does this. Source credentials must never be
   attached to a normal tool call.
5. Make ancestor skill paths usable for body/reference/script reads against
   the frozen repository root (current executor reads are cwd-relative and
   prohibit `..`). Do not advertise ancestor paths that tools cannot access.
6. Extend real PostgreSQL tests to session/run freezing and add private HTTP
   Git auth tests, plus a Linux canary for persistent reattachment, two-session isolation,
   read-only/full-access enforcement, context freshness and cancellation.
7. Remove any remaining deployment preview/feature flags only after the
   credential resolver and a real Linux canary pass. Actual Codebase cloning requires the user's saved web credential. No
   production credential has been read, and no live workspace was rebound.

## Invariants

- Keep repository URL, executor filesystem path, and harness cwd distinct.
- Freeze repository/environment/directory versions per run. Never silently switch the root during an active run.
- Repo files are project instructions, not a way to overwrite platform identity, credentials or permissions.
- Canonical relative paths only; enforce containment and symlink boundaries at the executor, not just in UI validation.
- Continue to reject general TAE workspaces because its filesystem enforcement is not qualified.
- No production rollout, repository binding for an existing workspace, or destructive checkout operation is implied by this implementation work.
