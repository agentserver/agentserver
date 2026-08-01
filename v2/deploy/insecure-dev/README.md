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
app-server or exec-server directly. Approval controls read nonce, context
digest, and version only from the canonical `agentserver.approval` custom
event, then call the separate same-origin
`POST /v2/workspaces/{workspaceId}/approvals/{approvalId}:decide` command;
the A2UI approval card remains display-only. The deterministic development
turn first discovers its single environment. Its shell policy is `ask`, so it
waits for that canonical decision and a successful Core consume before it
executes exact argv `["/bin/pwd"]` and emits a real command-result A2UI surface
from stock exec-server. Its Cancel button calls the separate same-origin
`POST /v2/workspaces/{workspaceId}/runs/{runId}:cancel` command. Disconnecting
the event stream does not cancel a run. A held attempt first reports
`cancelling`; harness-pool keeps both leases alive while interrupting the stock
turn and stopping the workload. Cancellation stops the turn/MCP context, while
the worker control stream remains alive through the interrupted-terminal ACK
and the pool lifecycle-command context remains alive through workload cleanup.
Only the exact holder can then commit terminal `cancelled/interrupted`.

The insecure-development run manifest signs a 10-second maximum approval TTL;
this deliberately short value makes the real database-time expiry path
repeatable and is not a production recommendation. The smoke checks the
reference-web marker and CSP, then runs five deterministic requests. The first
reads canonical approval authority, approves through the independent command
endpoint, and verifies that the real shell command proceeds only after Core
consumes the approval. The second does the same, pauses after execution, then
sends explicit run cancellation and observes `RUN_ERROR` with
`code=user_cancelled`. The remaining requests deny the pending approval, let
it expire without a decision, and cancel the run while approval is still
pending. Denial and expiry return bounded tool failures to the scripted model,
which completes without a command-result surface; pending cancellation ends
as `user_cancelled`.

The host-side database gate checks all five approval records and the three
pre-dispatch failures individually. Their approval/execution pairs must be
`denied/denied`, `expired/expired`, and `cancelled/cancelled`, each with
`dispatched_at IS NULL` and zero `execution_operations`. Across the five runs,
only the two approved commands may create operations. Each approved shell
freezes a two-row `process_start + timeout_terminate` operation plan, while
only its `process_start` row may have `dispatched_at`; the expected delta is
therefore four plan rows and two dispatched rows, not four dispatches. Normal
completion, denial, and expiry commit three checkpoints in total; both
cancellation paths commit none. The same smoke can be rerun against the same
persistent volume: scripted scenario markers are read only from the newest
user message and are not inherited from checkpoint history. A2UI remains
display-only throughout.

Inspect and stop the stack with:

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
