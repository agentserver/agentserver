# SG managed CLI sandbox operations

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
credential in the diagnostic requests. Network policy permits the exact proxy
Pod selector/port, not all private networks. Command inputs cannot override the
deployment-owned proxy variables.

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
Do not confuse published images/Chart with an activated production deployment.
The gateway image must contain `/usr/local/bin/agentserver-probe`, used by the
unchanged startup/readiness/liveness TCP probes.

Only use targeted Pulumi updates on `sg`, context `k8s-sg-prod`, API endpoint
`https://k8s-sg.byted.cs.ac.cn`. The infrastructure tree contains unrelated drift;
never use a full-stack update or `--target-dependents` for this release.

## Acceptance

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
