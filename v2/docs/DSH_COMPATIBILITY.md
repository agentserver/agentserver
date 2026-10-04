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

- `session/list`, `session/create`, `session/rename`, `session/cancel`, `session/prompt`, `session/page`, `session/follow`, `session/projections`, and `session/modelCatalog` are backed by Core session/run resources.
- `commands/list` and `commands/execute` expose the `permission` command and update Core's versioned Codex permission mode (`read-only`, `auto`, or `full-access`).
- Core approval-request events are projected through the DSH `approval/request` Remote waterfall; an `allowed-once` answer is translated back to the Core approval CAS command, while cancellation and non-allow outcomes fail closed.
- `workspace/follow` presents the configured workspace and its Core sessions; workspace ordering, pinning, and archive mutations are compatibility no-ops until a durable DSH workspace projection is introduced, while workspace deletion is rejected as immutable.
- `$events` emits the DSH generation-ready frame. Session and projection state are delivered through the dedicated streams and unary calls.

Session ids supplied by the DSH client must be canonical UUIDs, matching the Core v2 resource contract; when omitted, the facade allocates one.

Canonical Core run events are translated to DSH session events in a process-local session projection. Completed transcript messages are reconstructed from Core's transcript endpoint; newly started runs are projected from the Core run-event cursor while the gateway remains alive.

The embedded frontend and projection are v2-only. The release does not migrate or restore legacy DSH sessions; a browser-gateway restart does not recreate the old process-local active-run stream.

## Configuration

Set these variables on the v2 `browser-gateway` process to enable the facade:

| Variable | Required | Meaning |
| --- | --- | --- |
| `AGENTSERVER_V2_DSH_WORKSPACE_ID` | yes | Existing Core workspace UUID used for all DSH calls. |
| `AGENTSERVER_V2_DSH_WORKSPACE_PATH` | no | Display-only path shown in the DSH workspace view; set it when the DSH picker will call `workspace/create`, because the configured workspace is immutable. |
| `AGENTSERVER_V2_DSH_WORKSPACE_TITLE` | no | Display title; defaults to `AgentServer`. |
| `AGENTSERVER_V2_DSH_HOME` | no | Host home fact included in the DSH `$events` ready frame. |

The configured workspace is an authority decision, not a client-selected path. Session working-directory changes remain subject to Core's executor binding and CAS policy; the compatibility facade does not create or bind a TAE filesystem environment.

## Current compatibility limits

The facade intentionally returns explicit DSH errors for file attachments, fork, and unimplemented plugin-owned namespaces. Queue and steer prompts are accepted in a process-local FIFO while a Core run is active; they are not durable across gateway restarts. Session event projection is process-local; after a browser-gateway restart, the durable Core transcript is available but an already-running run cannot be resumed through DSH until a new prompt establishes a fresh cursor. Assistant deltas are committed to the DSH journal at message completion rather than exposed as a separate incremental assistant stream. These limits keep the adapter from inventing durable state or bypassing Core's run authority. Expanding them requires a corresponding Core resource or durable projection, not a wider reverse proxy.
