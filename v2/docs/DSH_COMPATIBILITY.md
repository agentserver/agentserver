# DSH Web API compatibility

AgentServer v2 can expose a deliberately bounded DeepSeek Harness (DSH) Web API facade from `browser-gateway`. The facade is disabled unless `AGENTSERVER_V2_DSH_WORKSPACE_ID` is configured, so existing v2 deployments keep their `/v2` contract unchanged.

## Transport

The facade implements the DSH Connection protocol:

- `POST /api/<namespace>/<method>` accepts a `client-request` envelope and returns a `server-response` envelope with the DSH `{ ok, value | error }` result form.
- `GET /api/remote.mux` upgrades to the DSH multiplexed Remote stream protocol (`open`, `item`, `end`, and `cancel` frames).
- A bearer in `Authorization` is forwarded to Core for every operation. After a successful unary request the facade also sets an `HttpOnly` `agentserver-bearer` cookie so a browser WebSocket handshake can authenticate without a custom WebSocket header. The cookie is marked `Secure` when the request is HTTPS or carries `X-Forwarded-Proto: https`.

The facade never accepts credentials in a query parameter. It accepts the normal bearer header and the narrowly named bearer-cookie forms needed for a browser WebSocket (`agentserver-bearer`, `agentserver_access_token`, and `access_token`).

The production v2 chart runs a dedicated `dsh-frontend` browser-gateway workload at `https://dsh.byted.bps.dev/`. Its `/api/*` HTTP and `/api/remote.mux` WebSocket routes are same-origin with the embedded page. The embedded page performs its own v2 browser OAuth PKCE flow and projects the resulting access token into bearer headers; the first unary call mints the HttpOnly bearer cookie used by the WebSocket.

Because a browser WebSocket cannot set `Authorization`, a DSH browser page connecting to a separately hosted AgentServer must provision `agentserver-bearer` before opening its Remote stream (for example, by making one credentialed unary request through the same origin or by having the hosting auth adapter set the cookie). Cross-origin browser transports must use a credentialed fetch configuration; a Node/Desktop DSH carrier may send the bearer header directly on the WebSocket handshake.

## Resource mapping

The configured workspace id is the DSH workspace presented to the browser. DSH session operations map to Core user-session and user-run operations:

- `session/list`, `session/create`, `session/rename`, `session/cancel`, `session/prompt`, `session/page`, `session/follow`, `session/projections`, and `session/modelCatalog` are backed by Core session/run resources. The model catalog projects the active default workspace LLM gateway as the DSH `workspace-gateway` provider and advertises its configured default model; it no longer exposes the old placeholder `codex/codex` route.
- `commands/list` and `commands/execute` expose the `permission` command and update Core's versioned Codex permission mode (`read-only`, `auto`, or `full-access`).
- Core approval-request events are projected through the DSH `approval/request` Remote waterfall; an `allowed-once` answer is translated back to the Core approval CAS command, while cancellation and non-allow outcomes fail closed.
- `workspace/follow` presents the configured workspace and its Core sessions; workspace ordering, pinning, and archive mutations are compatibility no-ops until a durable DSH workspace projection is introduced, while workspace deletion is rejected as immutable.
- `$events` emits the DSH generation-ready frame. Session and projection state are delivered through the dedicated streams and unary calls.

Session ids supplied by the DSH client must be canonical UUIDs, matching the Core v2 resource contract; when omitted, the facade allocates one.

Canonical Core run events, committed prompts, and permission changes are replayed from the authenticated, paginated Core session journal. Migration 0034 adds a transactionally allocated per-session cursor: permission mutations and their journal entries commit together, including before the first prompt. DSH `permission/preset` entries advance the same replayable event sequence used by `session/control`, follow snapshots and reconnects. Repeated changes cannot reuse a previous projection watermark. Control polls current authorized session versions so a mutation through another gateway or the platform API is visible even without an open message follower.

Live output and reconnect recovery use the same projection. Each independently settled model item has a distinct DSH step, and Assistant tool-call blocks own their execution lifecycle records. Migration 0034 backfills the existing run/prompt prefix unchanged and appends a current-permission checkpoint at its tail; it does not fabricate historical permission changes. Core's internal journal pagination now uses `cursor` rather than the old `runId`/`after` pair. Deploy Core before the new browser gateways (or roll the service release together); the old internal journal client is not compatible with the new pagination contract. The external DSH frontend protocol is unchanged.

The embedded frontend and projection are v2-only. The release does not migrate legacy DSH sessions. Frontend source is pinned by the DSH submodule and compiled during release; see [the frontend build](../dsh-web/README.md).

## Configuration

Set these variables on the v2 `browser-gateway` process to enable the facade:

| Variable | Required | Meaning |
| --- | --- | --- |
| `AGENTSERVER_V2_DSH_WORKSPACE_ID` | yes | Existing Core workspace UUID used for all DSH calls. |
| `AGENTSERVER_V2_DSH_WORKSPACE_PATH` | no | Display-only path shown in the DSH workspace view; set it when the DSH picker will call `workspace/create`, because the configured workspace is immutable. |
| `AGENTSERVER_V2_DSH_WORKSPACE_TITLE` | no | Display title; defaults to `AgentServer`. |
| `AGENTSERVER_V2_DSH_HOME` | no | Host home fact included in the DSH `$events` ready frame. |

The configured workspace is an authority decision, not a client-selected path. Session working-directory changes remain subject to Core's executor binding and CAS policy; the compatibility facade does not create or bind a TAE filesystem environment.

## Automatic session titles

For a fresh Codex thread, the worker starts a separate `ephemeral: true` thread after the primary turn is authorized. It uses the same approved model/provider and LLM capability as that run, not a different model or a second set of credentials. Like upstream TUI's `temporary_structured_request.rs`, it uses `config/read`, disables effective MCP servers and built-in tools, empties environment/capability roots, and requests a JSON-schema title of at most 36 characters. The bounded title prompt is independent of the primary conversation and has no workspace/skill access. The profile is tested against pinned Codex 0.146.0 (not all newest upstream config keys exist in that version).

The primary response never waits for title inference. A deterministic first-message summary is submitted first. Title inference is limited to 30 seconds; error, timeout or an earlier primary-turn completion retains that fallback and interrupts/unsubscribes the temporary thread. Hidden notifications and reverse requests never enter the primary transcript, tool bridge or checkpoint. Unexpected title tool/interaction requests are rejected. Failures log a stage only, not prompt text or credentials.

The worker sends `session_title` control facts through the existing accepted-attempt channel. The pool appends canonical `session.title.proposed` facts under the same holder/generation authority; Core atomically accepts metadata only for the session's first committed run. Migration 0035 introduces `title_source` and an independent `title_version`; accepted title revisions append durable `session/title` entries and update the DSH `title` projection. Saved titles also appear in cached session-list hints. Manual rename pins the title even if its text equals the fallback; late generated output and retries cannot overwrite it. Existing custom titles are retained as manual, and old conversations are not mass-regenerated.

Release requires both the **service** and **harness** images plus migrations 0034/0035. The Kubernetes publisher's `[k8s harness]` application-repair mode rebuilds these two images and retains the qualified sandbox images. Roll Core/pool before enabling the new worker image, with existing attempts drained as usual; do not deploy only the frontend or only the worker against an older pool. The title migration also journals renames made by an older Core during the rolling upgrade as manual. The DSH frontend/submodule has no production changes for this feature.

## Other compatibility limits

The facade intentionally returns explicit DSH errors for file attachments, fork, and unimplemented plugin-owned namespaces. Queue and steer prompts are accepted in a process-local FIFO while a Core run is active; pending prompts are not durable across gateway restarts. Committed history and active runs can be restored from the Core journal. Core Assistant deltas are projected to opted-in `session/follow` subscribers as DSH `assistant-stream` start/chunk/end frames; completed messages embed that stream for replay. Missing retained events fail explicitly rather than resetting cursor positions. Unsupported capabilities need corresponding Core resources, not frontend fallbacks or a wider reverse proxy.
