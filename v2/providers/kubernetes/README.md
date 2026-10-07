# Kubernetes Agent Sandbox provider (implementation in progress)

This module is **not yet a deployable replacement for TAE**. No production
AgentServer release has been switched. SG controller/CRDs, namespace, quota
and runtime TLS were installed through targeted Pulumi updates on 2026-10-07.
Lifecycle, runtime, gateway entry point,
Core/Gateway credential dispatch, production Chart and independent image
publication are implemented; SG runtime qualification and rollout remain.

## Implemented

- `adapter.Provider` implements the existing `sandboxgateway.Provider` lifecycle
  with a namespace-scoped client-go dynamic client and Agent Sandbox v1beta1
  `SandboxClaim` resources. The reviewed release line is v1.0.x; target controller
  version is `v1.0.5` (distinct from the upstream Helm chart version).
- Deterministic claim names, complete create-identity comparison, exact GET after
  ambiguous creation, UID/resourceVersion deletion preconditions, foreground
  deletion, and explicit TTL renewal without resurrecting expired claims.
- Ready requires a current Sandbox Ready condition, a Ready runtime container,
  and verified Claim -> Sandbox -> Pod/Service ownership. Routing uses the owned
  Service name, never a caller URL or untrusted `status.podIPs/serviceFQDN`.
- The first observed Sandbox UID, Pod UID and container ID are persisted on the
  claim. A gateway restart must not adopt a new container into the same Core
  target generation. Dispatch verifies workspace/session/environment/target
  identity before handing off to `RuntimeClient`.
- Explicit `k8s` execution kind, backend-aware sandbox references, shared
  authenticated NDJSON transport, Core migration 0033, registered Kubernetes
  environment validation and managed execution lifecycle/lease fencing.
- `deploy.Resources` renders a template, demand-only pool (zero prewarmed
  replicas), ServiceAccount, namespace-scoped gateway RBAC and NetworkPolicies.
  `RuntimeClassName=""` **omits** `runtimeClassName`, selecting the default
  container runtime as requested. It does not create a class named `default`.
- `cmd/k8s-runtime` implements authenticated binding, one-shot process start,
  streaming output/exit, terminate/kill and bounded filesystem reads. It uses
  a nested bubblewrap user/PID/mount namespace, clean environment, read-only
  system/skill mounts and per-command `/workspace` read/write projection. No
  host procfs is exposed. Runtime credentials are absent from the command view.
- Process environment is delivered through a sealed anonymous descriptor to
  bubblewrap, not helper argv. Core workspace ByteCloud AK/SK injection is
  reused and tested for `k8s`; runtime receives no Kubernetes or Core credential.
- Runtime boot identity is pinned alongside Pod/container identity. A restarted
  process cannot accept old binds/operations even before Pod status reconciles.
- `cmd/sandbox-gateway` supplies in-cluster lifecycle authentication, runtime
  mTLS and the existing Core capability/live-authority server. It has no TAE SDK.
- Independent runtime/gateway Containerfiles and `build-kubernetes-images.sh`
  prepare Linux/amd64 images without a TAE keeper/base or new size/hash gates.

## Deployment boundary

The rendered template is a component, not an installation command. It requires
an existing controller/CRDs, a dedicated namespace, a real runtime image and a
runtime-only TLS Secret. Never substitute the TAE keeper image: its `/v1/ping`
does not implement process/files operations.

The default-runtime template is ordinary container isolation, **not** Kata,
gVisor or VM isolation. It disables ServiceAccount token automount and service
links, runs non-root, drops capabilities, disables privilege escalation, uses
a read-only root filesystem, applies resource limits, and admits ingress only
from `sandbox-gateway-k8s`. The production renderer adds public HTTPS egress
(excluding private/metadata ranges); private endpoints require explicit CIDRs.
Real CLI network smoke is still required before activation.

The current template's `/workspace` is **ephemeral** (`emptyDir`). It is suitable
only for the initial managed-CLI profile. Persistent source workspaces need a
separate storage lifecycle, and must not lose PVCs when an idle claim expires.
Do not advertise general session working-directory support until real per-process
filesystem access enforcement is implemented and tested on the chosen runtime.

## Remaining before activation

1. Apply the provider-specific launch/profile catalog, audited SG workspace
   setting migration, and Pulumi controller/TLS wiring. The internal
   `taePsm`/`providerPsm` legacy field names currently carry an explicit provider
   scope for `k8s`; production uses `AGENTSERVER_V2_MANAGED_SANDBOX_SCOPE`, never
   a fake TAE PSM. Scope/operation/provider identity still must match in Core.
2. Resolve SG's default seccomp namespace restriction, then exercise real
   bkectl/Lark. The runtime probe refuses uncontained startup. A 2026-10-07
   non-root, capability-free probe on `n251-224-152` failed with bubblewrap's
   "No permissions to create a new namespace". A read-only diagnostic found
   `kernel.unprivileged_userns_clone=1`, `user.max_user_namespaces=1031465`
   and seccomp filter mode enabled. The active AppArmor profile was also the
   containerd default, whose source denies mount. The operator selected
   bubblewrap and explicitly approved one-time root/MAC_ADMIN profile loading,
   with Unconfined AppArmor only for that bounded installer. Runtime Pods remain
   non-root, drop all capabilities and use named Localhost profiles. The
   dedicated profile omits ptrace/process_vm/chroot and keeps default-deny.
   It must be verified on one SG node before labeling any node eligible.
3. Publish images and complete Pulumi/Helm assembly, including controller,
   namespace, quota, admission constraints, TLS and egress configuration.
4. SG canary: list environments through the real executor, run bkectl/Lark with
   workspace credentials, exercise filesystem read/write policy and streaming,
   cancel, disconnect, restart and deletion recovery. Then switch defaults.

No `active-k8s` state string or TAE revision placeholder is introduced: provider
selection and the existing rollout stage must be separate configuration axes.
Historical TAE rows are retained with their original type; new Kubernetes
profiles require new environment IDs. No fallback to TAE or AgentX is permitted.

## Publication

`.github/workflows/v2-kubernetes.yml` runs on the `k8s-sg` runner and publishes
five images (including the node profile installer) plus an environment-specific
Chart. It does not apply the Chart.
`publish-kubernetes-images.sh` rebuilds Core/gateways/harness pool/worker/init;
the stock Codex bundle and unchanged final-exec binary come from the last
published harness, with matching deployment metadata. bkectl and lark-cli are
copied as individual artifacts into a clean Debian runtime (no TAE keeper).
There are no new size or content-hash verification release gates.

The initial Kubernetes profile ID is `4e0e31cf-a8c9-4ef9-8197-71be586532df`.
With `allWorkspaces`, bootstrap changes only future-run region settings;
Core still enforces exact workspace/session/run/target authorization. Database
tests cover cross-workspace environment isolation and idempotent audit events.

## Verification

```sh
make -C v2 kubernetes-provider-check
cd v2
go test -race ./internal/executionbackend ./internal/sandboxcontract \
  ./internal/sandboxgateway ./internal/sandboxgatewayapp ./internal/k8sruntime ./internal/executorgateway ./internal/corecontract \
  ./internal/coredb ./internal/coreserver ./cmd/agentserver-core
AGENTSERVER_RUN_POSTGRES_TESTS=1 \
  AGENTSERVER_V2_TEST_DATABASE_URL='<disposable local database URL>' \
  go test ./internal/coredb \
  -run '^TestPostgreSQLManaged(Sandbox|Environment)' -count=1 -v
```

Provider tests use the Kubernetes fake client (not a live API server). Gateway
stream tests use the real JSON/NDJSON protocol with a fake execution provider.
PostgreSQL tests exercise actual migrations, concurrent reservations, environment
listing, operation transitions and generation fences; they do not prove runtime
process execution or live SG networking.

Linux runtime live tests (`AGENTSERVER_K8S_RUNTIME_LIVE_BWRAP`, `TestLinuxLive*`)
passed locally inside a Linux/arm64 container running UID/GID 10000, all
capabilities dropped and no-new-privileges. They prove read/write projection,
secret-FD launch, invisible runtime credentials and detached-child cleanup in
that environment, **not** Linux/amd64 SG acceptance.

The existing Lark PostgreSQL fixture's duplicate event record was corrected
(`seed+220` was used twice). TAE/Lark and Kubernetes ByteCloud process credential
database tests now pass together on a disposable PostgreSQL 17 instance.

Host Go 1.27.1 reports harness canonical JSON and legacy production-image
gzip-fixture mismatches; both packages pass with the project's Go 1.26.5.
Use `GOTOOLCHAIN=go1.26.5` for release verification. Focused tests and the local
Linux runtime tests do not constitute SG production acceptance.
The complete root `GOTOOLCHAIN=go1.26.5 go test ./...` subsequently passed;
PostgreSQL tests were run separately as described above (root defaults skip
external-service integration tests).
