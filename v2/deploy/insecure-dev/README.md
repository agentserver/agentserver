# Single-container insecure development stack

This directory turns the current v2 components into one locally runnable Linux
environment. It is deliberately a development deployment, not a production
image:

- PostgreSQL and every v2 control-plane service stay in one long-lived
  container;
- harness-pool starts one fixed-code harness-worker with ordinary `fork/exec`
  for each attempt, and the worker starts one short-lived stock Codex
  app-server over stdio;
- independent agentx supervises stock `codex exec-server --listen stdio` and
  never runs a model;
- only browser-gateway port 17444 is published on host loopback. Apple
  `container` forwards to the container NIC, so the deployment overrides only
  browser-gateway's bind address to `0.0.0.0`; all internal services remain on
  container loopback. The browser boundary is AG-UI with A2UI carried in AG-UI
  custom events.

The image build does not download Codex or bwrap. It accepts only the exact
official Linux arm64 0.146.0 artifacts pinned by `internal/devruntime`; a
digest or size mismatch fails the image build. agentx remains an independent
source tree and is copied into the temporary build context only as a compiled
binary.

Build from `v2/`:

```sh
./deploy/insecure-dev/build.sh \
  --codex=/absolute/codex-aarch64-unknown-linux-musl \
  --bwrap=/absolute/bwrap-aarch64-unknown-linux-musl \
  --agentx-source=/absolute/agentx-v2
```

Start it with a VM-backed persistent state volume and the executor workspace:

```sh
./deploy/insecure-dev/run.sh \
  --workspace=/absolute/workspace \
  --state-volume=agentserver-v2-dev-state
```

The default workspace is the repository root; the default state volume is
`agentserver-v2-dev-state`. A VM volume is intentional: Apple `container`
bind mounts do not permit the container to assign the fixed worker UID/GID to
private inputs. The executor workspace remains a normal host bind mount and
never contains the authority state. The run command grants the capabilities
needed by PostgreSQL initialization and the pool's fixed UID/GID fork backend.
Only `SETUID` and `SETGID` are delegated to the worker; final-exec clears all
capabilities and sets `no_new_privs` before stock app-server starts.

Run the real deterministic smoke from `v2/`:

```sh
./deploy/insecure-dev/smoke.sh
```

The browser-gateway also serves the dependency-free reference web at `/`.
Print a local URL whose fragment contains the insecure development bearer:

```sh
./deploy/insecure-dev/browser-url.sh
```

The reference page removes the fragment immediately and keeps the bearer and
AG-UI reconnect cursor only in page memory. The URL still exposes the fixture
bearer to terminal history, so it is strictly an `INSECURE DEV` convenience.
The page uses same-origin `fetch` streaming rather than `EventSource`, renders
AG-UI message/tool lifecycles, and accepts only the display-only A2UI v0.9
Card/Column/Text subset emitted by browser-gateway. It never connects to stock
app-server or exec-server directly. The deterministic development turn first
discovers its single environment, then executes exact argv `["/bin/pwd"]` so
the page receives a real command-result A2UI surface from stock exec-server.

The smoke first checks the reference-web marker and CSP, then sends an HTTPS
AG-UI request through the published browser-gateway, waits for `RUN_FINISHED`,
checks the scripted assistant message, and verifies that a checkpoint exists
in PostgreSQL. Inspect and stop the stack with:

```sh
container logs agentserver-v2-dev
container stop --time 15 agentserver-v2-dev
```

The 15-second stop grace lets the supervisor request PostgreSQL fast shutdown
and reap every service process before the VM is stopped. A normal restart
therefore does not rely on PostgreSQL crash recovery.

The database password, browser bearer, generated CA, and all other authority
material are insecure local fixtures. Delete the chosen volume with
`container volume delete` to discard that authority only after its container
has been stopped and removed.
