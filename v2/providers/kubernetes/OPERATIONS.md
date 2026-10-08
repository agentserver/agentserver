# SG/CN managed CLI sandbox operations

The operator selected **bubblewrap** and the **default RuntimeClass**. The
template omits `runtimeClassName`; it does not install Kata or gVisor.

## Prerequisites and ownership

- Agent Sandbox controller/CRDs: upstream v1.0.5, owned by targeted Pulumi
  resources in `../k8s-byted/apps/agent-sandbox.ts`.
- Sandbox namespace: `agentserver-sandboxes`; demand-only warm pool
  `managed-cli-v1`; no general persistent source-workspace support is implied.
- Runtime TLS has its own CA. Runtime containers have no Kubernetes token,
  Core certificate, or ambient user CLI credentials.
- Core remains the workspace/session/run/operation authority. Runtime identity
  additionally binds Claim/Sandbox/Pod/container/boot identities.

## Node profile installation

The user explicitly approved one-time root/MAC_ADMIN installer jobs, with
Unconfined AppArmor **only for the installer**. The application runtime remains
non-root, drops all capabilities, uses no host mounts and cannot escalate.
Only these host locations are mounted into the installer:

- `/var/lib/kubelet/seccomp/agentserver`
- `/etc/apparmor.d/agentserver-bwrap-v1`
- `/sys/kernel/security/apparmor`

The seccomp generator starts with containerd v2.3.3's capability-free default,
removes ptrace/process_vm/modify_ldt, and permits bounded namespace creation plus
mount/umount2/pivot_root. It does not grant host `SYS_ADMIN` or permit `setns`.
The named AppArmor policy retains proc/sys protections and permits the mounts
needed inside bubblewrap's own user/mount namespace. Node defaults are unchanged.

Debian AppArmor parser 4.1.0 uses `-f` / `--subdomainfs`; newer manual pages call
the long option `--apparmorfs`. Use the tested short `-f` option. The installer
script is supplied from the versioned ConfigMap, and the job uses the immutable
installer image's parser. Changing the job template replaces only that installer
job; it never deletes nodes or other workloads.

Set `agentserver:kubernetesSandboxQualifiedNodes` only after live CLI, read/write,
credential-hiding and detached-child cleanup tests pass on each node. Pulumi then
adds `agentserver.byted.bps.dev/bwrap-profile=v1`; runtime scheduling requires it.
All four current SG nodes passed these tests on 2026-10-07, including the exact
`lark-cli skills read lark-doc references/lark-doc-fetch.md` command.

## Networking and publication

ByteCloud resolves to private addresses that are not directly reachable from SG
Pods. Only bkectl receives the configured internal SOCKS5 route. Lark remains on
public HTTPS: routing its public CDN through that internal proxy timed out in the
live test. Both paths subsequently returned verified TLS/HTTP 200 without any
credential in the diagnostic requests. The existing bkectl SOCKS route remains
a connectivity route, not a namespace network allowlist. Command inputs cannot
override the deployment-owned proxy variables.

The operator subsequently requested **unrestricted sandbox networking in both
SG and CN**. Production release preparation now sets
`managedExecutor.kubernetes.unrestrictedNetwork=true`. The SG Chart stops
rendering `sandbox-default-deny`, `sandbox-runtime` and `sandbox-cli-egress`, so
Helm removes those three policies on upgrade. CN already has no sandbox namespace
policies. Both templates retain `networkPolicyManagement: Unmanaged`; the Agent
Sandbox controller must not recreate restrictions. Control-plane policies in
`agentserver` (Core, executor, llmproxy, gateways) remain separate and unchanged.
This does not enable host networking, privileged containers, Kubernetes tokens
or unauthenticated runtime commands. Actual connectivity still depends on routing
and the destination's authentication/firewalls, not just this namespace policy.

SG Cilium excludes node identities from ordinary CIDR matching. The gateway's
API-server IP/port `ipBlock` alone timed out, even after adding the Service IP.
An identical-SA diagnostic returned HTTP 200 only after allowing the precise
`kube-apiserver` entity on TCP 443/6443. The production Chart now owns that rule;
remove any temporary Pulumi release-bridge rule only after Chart convergence.
It must select only `sandbox-gateway-k8s`, never the runtime namespace or all Pods.

Use `.github/workflows/v2-kubernetes.yml`. Full publication builds the services,
harness overlay, runtime, gateway and installer. A `[k8s chart]` commit or the
`chart_only` input explicitly reuses `kubernetes-published-images.json`.
A `[k8s service]` commit rebuilds only the service image, retaining the qualified
harness, runtime, gateway and node-profile installer references.
GitHub Actions builds these images and pushes directly to `ghcr.io/agentserver`;
there is no ICM build, login or mirror-publication step. SG's deployment registry
mirror is only a pull path for GHCR images, not a second build service.
Do not confuse published images/Chart with an activated production deployment.
The gateway image must contain `/usr/local/bin/agentserver-probe`, used by the
unchanged startup/readiness/liveness TCP probes.

Only use targeted Pulumi updates on `sg`, context `k8s-sg-prod`, API endpoint
`https://k8s-sg.byted.cs.ac.cn`. The infrastructure tree contains unrelated drift;
never use a full-stack update or `--target-dependents` for this release.

## Acceptance

### Regional credential authority

Core and executor consume the same deployment-owned
`AGENTSERVER_V2_MANAGED_CREDENTIAL_SCOPE_BINDINGS` JSON array. Each environment
has exactly one scope; an unknown environment fails closed without falling back
to SG. The existing `taePsm`/`provider_psm` wire/database names carry this provider
scope, not an actual TAE PSM:

| Environment | Scope | Gateway |
| --- | --- | --- |
| SG `4e0e31cf-a8c9-4ef9-8197-71be586532df` | `sg-managed-cli` | SG in-cluster mTLS |
| CN `73cd7602-c0be-4d9a-96a9-d0c09bf6f689` | `cn-managed-cli` | `https://sandbox-gateway-cn.byted.bps.dev` |

Scope is part of immutable sandbox reservation identity. Do not append a region
suffix or rename an existing reservation. Core still checks the exact live
workspace/session/run/operation/environment/sandbox generation, credential
version and stored provider scope before materializing a credential. A successful
`bkectl --json version` alone is not a credential test: discovery commands bypass
credential injection. Acceptance also needs a real credentialed read-only query.

CN uses ordinary cross-cluster HTTPS plus application authentication and signed
capabilities; runtime mTLS remains inside CN. The operator explicitly chose no
CN namespace NetworkPolicies for this rollout. SG's policies are unchanged.

### Product-path verification

Check migrations and managed-environment bootstrap, Ready deployments, SG
workspace defaults and registered backend kind `k8s`. Then use a genuine
authenticated user session to enumerate environments and run the CLI through
Core → executor → sandbox gateway → runtime. Do not manufacture user tokens or
use diagnostic `kubectl exec` as evidence for the complete product request path.
Never relabel historical TAE rows or automatically retry ambiguous commands.

## DSH history and replica changes

DSH followers replay Core's authenticated, paginated session journal, including
committed user prompts, tool events and failed turns. Live projection and restart
recovery use the same ordered source; the bounded text transcript is not a cursor
source. Each connected replica follows new Core commits even if another replica
handled the prompt. Read failures are surfaced, not cached as empty history.
The journal uses the existing combined `sessions.transcript` user/workload
authority and rechecks membership on each page. Missing retained events fail
explicitly; retention rebases are not silently converted to a different journal.
No database migration or deletion of existing session history is needed.

DSH keys Assistant settlements by `(turn, step)`. Each independently settled
Codex message/reasoning/tool item gets its own projected step. A tool request
also emits the Assistant `tool-call` content block that owns the matching
`tool/call` and `tool/result`; tool results retain their original turn/step even
when parallel calls finish out of order. Omitting the owning block makes DSH
prepend orphan results to Turn 1. The Go regression runs the shipped DSH
definitions and Trajectory layout in Node, including a negative control that
reproduces that failure; it does not substitute a custom frontend sort.

Managed bkectl needs a default ByteCloud **AK/SK** binding. A retained legacy
`device_oauth` binding is not convertible into AK/SK and is not valid for this
delivery mode. The Platform credentials page offers a password-masked AK/SK
form; values go only into the authenticated HTTPS create request and Core's
sealed storage, never browser storage or chat. Creating a new default retains
the old binding. `bytecloud_aksk_required` means no process was dispatched.
Migration 0037 records explicit `dispatch_not_sent` evidence: a managed shell
environment-injection failure closes as `failed`, with `dispatch_outcome=not_sent`
and a safe reason code. It has no backend acknowledgement or subprocess exit
code. DSH/model text explains that the command never started. Unknown/ambiguous
post-dispatch outcomes still remain `unknown`; historical results are not rewritten
and commands are not automatically replayed.

`credential_unauthorized` does not prove AK/SK is invalid. First check the
environment-to-scope mapping against the sandbox reservation and live operation
authority; do not ask the user to re-enter credentials solely on that error.

The scope/error-reporting repair was deployed on 2026-10-08 as Helm revision
167, Chart `0.1.0-config.d5eac249f9cd7`, publication run `37725949914` attempt 2.
Migration 0037, both scope mappings, all workload readiness, DSH HTTP 200 and the
workspace's retained CN selection were verified. PostgreSQL regressions passed
in CI. The subsequent CN user run `aa71e5e8-f8a0-4251-922c-4e17ec5b3c5b`
confirmed credential resolution, injection and runtime acknowledgement succeed.
It exposed a separate output-stream fault, described below; credential injection
success alone still does not establish full query success.

### Quiet HTTP/2 command streams

The original runtime armed a 15-second response write deadline for each NDJSON
frame but did not clear it after flushing. On HTTP/2, the timer actively resets
the stream even while the process is quietly waiting, rather than merely timing
out a blocked write. The real CN query lost its stream about 15 seconds after
ACK, leading to `invalid_stream_json`, a conservative `unknown` outcome and
sandbox fencing. Later calls in that turn then saw an unavailable environment.

The runtime now bounds only each Encode/Flush and clears the write deadline on
both success and failure. Process deadlines, output limits and fencing remain;
never solve a lost stream by claiming success or replaying an uncertain command.
Tests cover real HTTP/1.1 and HTTP/2 connections, an actual quiet process, deadline
cleanup on failed writes, and a 20-second quiet command using the unmodified
production deadline (`AGENTSERVER_RUN_RUNTIME_STREAM_TESTS=1`).

Use `application_repair=runtime` in the Kubernetes publication workflow for this
fix. Its image overlay changes only `agentserver-k8s-runtime`, retaining the
qualified CLI/bubblewrap artifacts, non-root user and entrypoint. Update both the
SG Chart's runtime image and CN's `agentserver-cn-sandbox-template` using targeted
Pulumi updates. Template changes apply to newly allocated sandboxes; do not
replace live user runtimes or change their boot identity underneath active runs.

The runtime repair was published by run `37729403569` and deployed on
2026-10-08: SG Helm revision 168 (`0.1.0-config.d537bf3bac1db`) and CN's separately
owned template both use runtime `c00766a94db72ec0020b9ba46f3b8ff729ffe2463b0c75e96cf1ef74208eba6b`.
All application deployments were Ready. Fresh credential-free smoke claims
started the actual new image on CN `n37-104-065` and SG `n251-239-167`, both Ready
with zero restarts. Full CI, real HTTP/2 20-second quiet-process tests and database
regressions passed. The user-session bkectl retry remains the final product-path
check; do not describe template/readiness checks as that end-to-end result.

### Long MCP tool calls and worker header deadlines

Run `75901713-c365-452c-bcd5-e88474ede085` exposed a separate worker-side bug:
the CN bkectl process was authorized and acknowledged, but the worker's MCP
HTTP client stopped waiting for response headers after 30 seconds, before the
shell's 60-second deadline. Terminal-only tools may not send headers until their
result is ready. This is not a sandbox network denial.

The executor MCP HTTP client now has no separate response-header/whole-response
timer. Its request context remains bounded by the signed maximum run duration,
and the executor still enforces the command timeout and cleanup grace. The
control client retains its 30-second header bound; dial/TLS bounds are unchanged.
Tests exercise an actual 31-second authenticated HTTP/2 MCP call, reproduce the
old cutoff with a shortened bound, verify cancellation and forbid automatic
tool-call replay (`AGENTSERVER_RUN_LONG_MCP_TESTS=1`). Runner-only tool transport
timeouts are reported as `tool_transport_timeout` / worker execution failure,
not a model timeout or a cleanup failure when cleanup actually succeeded.

Both changes were deployed on 2026-10-08 as revision 171, Chart
`0.1.0-config.d60f184367834`, publication run `37745479080`. Full service/provider,
31-second MCP, PostgreSQL and native stock Codex regressions passed. All workloads
were Ready, and both SG/CN sandbox namespaces contained zero NetworkPolicies or
CiliumNetworkPolicies. The old SG runtime policy removal is a Helm-owned,
versioned configuration change; control-plane policies were retained.

### Direct bkectl diagnostic (2026-10-08)

The user explicitly authorized one credentialed, read-only `kubectl exec` test:
`bkectl k8s pod observe --name dp-19a21c185f-556d96c944-56hwj --region i18nbd --json`.
A CN sandbox from the live template first returned `auth.credentials: not logged
in` without process credentials. With the workspace's saved AK/SK injected only
into the diagnostic process (`BKECTL_AUTH_MODE=app_only`), it ran for 30.367s,
exited 1 and returned observation status `unknown`: source `cache` timed out
waiting for HTTP headers from `http://tce-status-nontt.byted.org/api/v1/pods/name`.
No current identity/Pod facts were obtained. This was a completed CLI failure,
not the outer 120-second diagnostic deadline.

Credential-free checks from that sandbox established TCP connections to the same
host on both 80 and 443, but an HTTP request on port 80 received no response header
within 8s. This does not establish that the Pod is unhealthy; the remaining issue
is the TCE-status HTTP service/access path from CN. Removing the worker's outer
30-second cutoff exposes this real CLI error instead of masking it.

The diagnostic used a short-lived, ingress-denied reader Pod with only the
credential-sealing file, a database Secret reference and PostgreSQL/DNS egress.
Its hard-scoped helper handed credentials to the sandbox encrypted for an
ephemeral in-memory key, never as plaintext tool output or credential files.
The temporary reader, its policy, sandbox claims and diagnostic executables were
removed. No workspace binding was edited and normal Core live-authority checks
were not modified. Direct exec is diagnostic evidence, not a substitute for the
authenticated product execution path.
