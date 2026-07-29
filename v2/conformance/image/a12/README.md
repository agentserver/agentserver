# A12 disposable Linux isolation gate

This target exercises the production-profile boundary around stock app-server
for one exact native Linux artifact. The scratch image contains only the Go
conformance/init fixture, the pinned Codex executable, and empty mount anchors;
it contains no source tree, workspace, service-account token, shell, package
manager, firewall binary, compatibility wrapper, or model credential.

The container starts as a bounded init fixture with `NET_ADMIN`, `CHOWN`,
`SETUID`, and `SETGID`: it installs the owner rules and creates fixed-UID tmpfs
state before any child starts. Apple `container` additionally supplies
`SYS_ADMIN` only so this fixture can remount its otherwise optionless `/tmp`
and `/run/agentserver`
tmpfs mounts as `nosuid,nodev,noexec`, then verify the resulting mount table.
It uses Go's netfilter netlink protocol implementation to install an IPv4
nftables output chain plus an IPv6 deny chain with `meta skuid` rules:

- the worker UID can reach only its harness-control fixture;
- the app-server UID can reach only the exact llmproxy, approved-MCP, and
  redirect-source address/port tuples;
- every other TCP destination is rejected and all remaining traffic, including
  DNS-shaped UDP, is dropped for those UIDs;
- all IPv6 traffic is dropped for both managed UIDs, so an IPv4-only endpoint
  manifest cannot be bypassed through dual-stack or loopback routing;
- the root fixture is outside those rules only so it can host and prove the
  sensitivity of the in-image sinks. It is not the runtime worker.

Each scenario starts a real worker as UID/GID 65531 with empty supplementary
groups and exactly `CAP_SETUID` plus `CAP_SETGID`; that process is the direct
supervisor and parent of the app-server launcher. The worker proves that its
`0700` credential/staging tree, Unix control socket, `O_CLOEXEC` file/socket
descriptors, private environment sentinel, and worker-only HTTP endpoint are
live. It then removes the worker secret from the child environment, maps stdio,
creates an intentionally non-`CLOEXEC` trap, and starts fixed app UID/GID 65532
with empty supplementary groups and a parent-death signal. Immediately after
the child starts, the one-child worker applies the same all-thread identity
seal to itself and supervises with no remaining capabilities.

Before any filesystem, procfs, socket, or network preflight, the final-exec
trampoline verifies real/effective/saved IDs, then clears ambient, inheritable,
permitted, and effective capabilities and sets `no_new_privs` across every Go
runtime OS thread. It disables dumpability and verifies the sealed identity,
then proves that the real parent PID is the worker, worker paths and `/proc`
state are inaccessible, the worker cannot be signalled, and known
workspace/service-account paths are absent. Finally it calls Linux
`close_range(3, UINT_MAX, 0)`, proves the inherited trap closed, and execs the
absolute stock Codex path with an explicit environment. The worker waits for
that child; it is not a sidecar sentinel.

The application-level scenarios prove:

- a real model turn plus approved MCP tool call succeeds through the allowlist;
- a direct model URL at a live forbidden sink fails and the sink sees zero
  requests;
- an allowed model endpoint can return a cross-origin 307, but the redirected
  live sink still sees zero requests and the turn fails;
- app-server cannot reach the worker-only HTTP endpoint, while the worker can;
- a root sensitivity packet reaches the DNS-shaped UDP fixture before policy,
  while all app-UID probes leave its post-reset count at zero;
- a root sensitivity request reaches an IPv6 loopback sink, while all app-UID
  probes leave its post-reset count at zero.

Run it with independently trusted release pins and a native architecture:

```sh
AGENTSERVER_CONTAINER_RUNTIME=container \
AGENTSERVER_A12_GOARCH=arm64 \
AGENTSERVER_CODEX_LINUX_BIN=/absolute/path/to/codex-aarch64-unknown-linux-musl \
AGENTSERVER_EXPECTED_CODEX_RELEASE=0.146.0 \
AGENTSERVER_EXPECTED_CODEX_SHA256=cb5e8cb8a333a408ce6adbe0d4fad1845c69772c2216af7c1f88c98a11460dc6 \
AGENTSERVER_EXPECTED_CODEX_SIZE_BYTES=269098800 \
make -C v2 conformance-image-a12
```

`AGENTSERVER_CONTAINER_RUNTIME` defaults to a Docker-compatible `docker` CLI.
Apple `container` uses a workspace-backed build context and requires
`AGENTSERVER_A12_GOARCH` to match the host. Cross-architecture emulation is not
accepted as platform evidence. The command above passed natively for stable
0.146.0 `linux-arm64`, SHA-256
`cb5e8cb8a333a408ce6adbe0d4fad1845c69772c2216af7c1f88c98a11460dc6`,
size `269098800`. That closes only the A12 production-profile image gate for
this exact artifact and platform. `linux-amd64` still requires a native worker;
the result does not itself close A03, E03, E07, any E09 platform gate, or real
Kubernetes NetworkPolicy/egress-proxy deployment tests.
