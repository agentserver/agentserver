# cc-app-gateway Phase 4 — IM intake (agentserver side)

**Status:** draft v2 (self-audit revisions applied 2026-06-22, see § Audit revisions)
**Date:** 2026-06-22
**Owner:** agentserver / cc integration
**Builds on:** Phase 1 (PR #279) and Phase 2 (PR #280, stacked). Cannot ship until both merge first.
**Resolves the user-visible question:** "Can I talk to claude from WeChat now?" — yes, after this lands.

## Goal

End-to-end IM → cc-app-gateway → claude → reply path: when a WeChat message routes through imbridge with `routing_mode=managed_cc`, agentserver allocates/looks-up a claude session, forwards the message to cc-app-gateway, waits for claude's reply, and posts the assistant text back to imbridge's `/send` endpoint.

After this lands, `kubectl rollout` of agentserver + the IM channel's `routing_mode` set to `managed_cc` is the only operational step needed for production WeChat ↔ claude.

## Non-goals (deferred)

- In-process MCP tools (Phase 3 — independent track; cc-app-gateway runs without tools, claude only chats).
- Multi-modal IM input (image / file) routing to claude via Anthropic's vision API. Phase 4 forwards `text` only; `media_*` fields are received but dropped with a log warning. Phase 5+ pivots to vision once we wire MCP tool `attach_image` or similar.
- Quoted-message context (`quoted_text`, `quoted_sender`) — same: received, dropped, follow-up.
- Backfill / migration of existing TUI sessions into IM sessions.
- Per-workspace LLM model override beyond `CCAPPGW_DEFAULT_MODEL`. Phase 4 hardcodes the gateway default model for all IM turns; per-channel model selection is Phase 5+.
- Bidirectional handoff between codex and managed_cc (a user switching their channel's routing_mode mid-conversation). Phase 4 isolates the two; switching = new session.

## Architecture decision: synchronous, not callback

Phase 2 spec speculated cc-app-gateway might need a `callbackUrl` async mode for IM turns. **The codex IM path (`codex_im_inbound.go`) is fully synchronous** — it calls `codex.RunTurn()` with a 61-minute HTTP client timeout and blocks the dispatcher worker until completion. cc-app-gateway in Phase 1 already returns the assistant text synchronously from `POST /api/turns`. Phase 4 calls it the same way codex does. The `callbackUrl` field on `CcTurnRequest` shipped in Phase 2 → 501 remains unused (Phase 5+ may revisit if streaming SSE arrives).

**Why this works** even though IM turns can take 10+ seconds:
- HTTP keepalive holds the connection during the long claude `--print` run.
- The dispatcher serializes per-(channel, user), so concurrent users don't pile up on the same worker.
- claude's wall-clock cap (`CCAPPGW_TURN_TIMEOUT`, default 10m) is well below the HTTP client's 61-min cap, so client never times out before runner does.
- If cc-app-gateway is unreachable, the HTTP error fires immediately; we send a user-facing error via `sendError()` (matching codex's pattern).

**Network-path assumption (documented; mirrors codex's production reality):**
agentserver and cc-app-gateway communicate **pod-to-pod inside the K8s cluster** via Service DNS — no ingress controller, no LB, no Cloudflare tunnel in the middle. Cluster-internal Service IPs honor HTTP keepalive without idle-timeout-killing intermediate proxies. If a deployment ever exposes this path through an ingress / LB with a shorter idle timeout (typically 60s), the 10-minute HTTP hold will be killed mid-turn and a user will see "connection reset by peer." Codex's `codex-app-gateway` runs in the same posture and has been operationally stable; Phase 4 inherits that assumption. Document in the helm chart and operator runbook: do NOT route cc-app-gateway traffic through an ingress.

**Concurrency ceiling:** with 50 simultaneous IM users hitting 5-minute turns, agentserver holds 50 open HTTP connections + 50 dispatcher worker goroutines. cc-app-gateway side spawns 50 concurrent `claude --print` subprocesses (Phase 1 has no semaphore). Each `claude` is ~300-400 MB RSS → 50 × 350 MB = ~17.5 GB on the cc-app-gateway pod. Phase 1 Open Risk #7 documented this; Phase 4 inherits the bound. Operators sizing for >10 concurrent active IM channels should raise pod memory limits or scale `ccAppGateway.replicaCount`.

## Architecture diagram

```
WeChat user types "What's my favorite color?"
       │
       ▼
imbridge polls/receives, calls Bridge.forwardToManagedCC()  ← NEW [P4]
       │  POST http://agentserver:8080/api/internal/imbridge/cc/turn
       │  Header: X-Internal-Secret
       │  Body: {channel_id, workspace_id, wechat_user_id, wechat_sender,
       │         text, media_*, quoted_*}
       ▼
agentserver: internal/server/cc_im_inbound.go ServeHTTP    ← NEW [P4]
       ├─ Verify X-Internal-Secret
       ├─ ccDispatcher.Enqueue(req)         (per (channel_id, wechat_user_id) FIFO, cap 5, drop-oldest)
       └─ 202 + {"queued":true}
              │
              ▼ (background dispatcher worker)
       processTurn(req):
       ├─ sess := sessions.GetSessionByExternalID(workspace_id, wechat_user_id)
       │     ├─ hit AND sess.ClaudeSessionID != "":  reuse both
       │     ├─ hit AND sess.ClaudeSessionID == "":  EXISTING codex/nanoclaw session migrating to managed_cc
       │     │                                       — mint fresh UUID, SetSessionClaudeSessionID(),
       │     │                                       continue with new claude_session_id (codex history
       │     │                                       is NOT migrated; claude starts fresh — documented
       │     │                                       Open Risk #6 user-visible behavior on routing_mode switch)
       │     └─ miss: sessions.CreateSession() mints "cse_" + uuid.NewString() as agent_sessions.id
       │              + ALSO mints a fresh UUID for ClaudeSessionID (cc-app-gateway needs UUID-format
       │              per Phase 1 turn_api.go uuidRe validation)
       │              + stores external_id = wechat_user_id, im_channel_id, claude_session_id
       ├─ ccClient.RunTurn(ctx, CcTurnRequest{
       │     workspaceId:  req.WorkspaceID,
       │     sessionId:    sess.ClaudeSessionID,
       │     userMessage:  req.Text,
       │     // model omitted → uses cc-app-gateway's CCAPPGW_DEFAULT_MODEL
       │   })
       │     POST http://cc-app-gateway:8087/api/turns         (61-min client timeout)
       │     Header: X-Internal-Secret
       │     Body: CcTurnRequest JSON
       │     ← returns CcTurnResponse {sessionId, assistantText, isError, ...}
       ▼
       Reply path (same as codex's):
       ├─ h.postSend(channel_id, wechat_user_id, response.assistantText)
       │     POST http://agentserver:8080/api/internal/imbridge/send
       │     Header: X-Internal-Secret
       │     Body: {channel_id, to_user_id, text}
       ▼
imbridge.handleSend → provider.Send() → WeChat reply
```

## The session ID complication

agentserver's existing `agent_sessions.id` is `"cse_" + uuid.NewString()` (codex pattern). cc-app-gateway's Phase 1 `turn_api.go` validates sessionId via `uuidRe = ^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-...$` (pure UUID format).

So `agent_sessions.id` ("cse_xxx") CANNOT be sent as cc-app-gateway's sessionId. Three options:

| Option | Description | Trade-off |
|---|---|---|
| **A** | Strip the `"cse_"` prefix when sending to cc-app-gateway | Fragile — couples cc-app-gateway's sessionId format to agentserver's id format forever |
| **B** | Add new `claude_session_id` column to `agent_sessions`; mint pure UUID at session-creation time; store both | Clean separation, Phase 4 sets the pattern for any future agent backend |
| **C** | Change cc-app-gateway's uuidRe to accept "cse_xxx" too | Couples cc-app-gateway to agentserver's ID format; Phase 1 chose strict UUID for a reason (path safety, log readability, no fake prefixes) |

**Choose B.** This was the Phase 4 prerequisite documented in spec Phase 2's § Migration. Add `claude_session_id TEXT` column in a new migration (036_agent_sessions_claude_session_id.sql); populate it in `CreateSession()` alongside `id`; read it in `processTurn`.

## Component changes

### `internal/db/migrations/036_agent_sessions_claude_session_id.sql` (NEW)

```sql
-- 036_agent_sessions_claude_session_id.sql
-- Phase 4: cc-app-gateway requires pure-UUID sessionId per Phase 1's
-- turn_api.go uuidRe validation. agent_sessions.id uses "cse_<uuid>"
-- format; we need a separate column for the cc-app-gateway-compatible
-- session identifier. Nullable; only populated for rows that use the
-- managed_cc routing mode.
ALTER TABLE agent_sessions ADD COLUMN IF NOT EXISTS claude_session_id TEXT;

-- Index for lookup-by-claude-session-id, if Phase 5+ needs reverse lookup.
-- (Not used in Phase 4; lookups go via id or (workspace_id, external_id).)
CREATE INDEX IF NOT EXISTS idx_agent_sessions_claude_session_id
    ON agent_sessions (claude_session_id)
    WHERE claude_session_id IS NOT NULL;
```

### `internal/db/agent_sessions.go` (MODIFY)

Note: `sessionView` and `dbSessionStore.CreateSession` live in
`internal/server/codex_im_inbound.go` (lines 604–619), NOT in
`internal/db/agent_sessions.go`. The DB-layer changes are smaller than they look.

Concrete changes:

1. **`agent_sessions.go` row struct + SELECT lists** — add `ClaudeSessionID *string` (nullable pointer) to the row type so SELECT * queries pick up the new column. Add `CreateAgentSessionWithClaude(sessionID, sandboxID, workspaceID, title, externalID, claudeSessionID string)` if existing `CreateAgentSession` doesn't accept the column; or amend the existing function signature.
2. **Add `SetSessionClaudeSessionID(ctx, sessionID, claudeSessionID string) error`** — mirrors `SetSessionCodexThreadID`. **NOT** dead code per Audit Revision #2 below: used by the codex→managed_cc migration path in `processTurn` when an existing session is found with NULL claude_session_id (mints + persists fresh UUID).

Existing `dbSessionStore.GetSessionByExternalID` in `codex_im_inbound.go:560` returns a `sessionView{ID, CodexThreadID}` — extend to also include `ClaudeSessionID`. Update the SELECT in that function's underlying query to include the new column. Codex's tests pass `sessionView{}` with zero-value `ClaudeSessionID` — they continue to compile (zero value is `""`); they're unaffected functionally because codex doesn't read the field.

### `internal/server/cc_im_inbound.go` ccDbSessionStore (NEW, alongside the handler)

The cc handler needs its own `ccDbSessionStore` (mirroring codex's `dbSessionStore` at codex_im_inbound.go:578) with a `CreateSession(workspaceID, externalID, title, imChannelID string) (sessionView, error)` that mints BOTH `agent_sessions.id` ("cse_" + uuid) AND `claude_session_id` (pure uuid). This sits alongside the handler, not inside `internal/db/`.

The shared `sessionView` struct (with `ClaudeSessionID string` field added) is in `codex_im_inbound.go` — cc handler uses the SAME struct type. (Adding a new struct would be over-engineering — the field is just `""` for codex sessions.)

### `internal/server/cc_im_inbound.go` (NEW)

Mirror of `codex_im_inbound.go` — 90% same shape. Key differences:
- Request struct = `ccInboundRequest` (same fields as codex's, JSON keys identical)
- Process function calls `ccClient.RunTurn` (not codex)
- No `ThreadID` concept — cc-app-gateway is keyed on `sessionId` (Phase 2 resume); ID lives on `agent_sessions.claude_session_id`
- ccDispatcher copy of codexDispatcher (~100 LOC, deliberately NOT extracted to generic package per spec Phase 1 § Audit revisions Rev #2)
- No thread-not-found retry needed — cc-app-gateway's resume is automatic (workspace.Setup hits or misses S3 transparently)
- Error handling: timeout, runner_failed, runner_timeout, isError=true (returned in 200 body, not as transport error)

### `internal/ccappgateway/turn_api.go` (MODIFY, cc-app-gateway side, NOT agentserver)

**Phase 4 amends Phase 1's `CcTurnResponse`** to include the error message string when `IsError==true`. Phase 1 only carried `IsError bool`, which makes the agentserver-side error matrix unable to distinguish context-window-exceeded from other Anthropic-side errors. Add one field:

```go
type CcTurnResponse struct {
    SessionID     string                       `json:"sessionId"`
    AssistantText string                       `json:"assistantText"`
    IsError       bool                         `json:"isError"`
    ErrorMessage  string                       `json:"errorMessage,omitempty"` // NEW (Phase 4)
    DurationMs    int64                        `json:"durationMs"`
    TotalCostUSD  float64                      `json:"totalCostUsd"`
    ModelUsage    map[string]runner.ModelUsage `json:"modelUsage,omitempty"`
}
```

Populate in `ServeHTTP` from the runner's `ResultMeta.ErrorMessage` (which Phase 1 already extracts from the claude `result/error` SDKMessage — see `runner/events.go`):

```go
if result.Meta != nil {
    resp.IsError = result.Meta.IsError
    resp.ErrorMessage = result.Meta.ErrorMessage  // NEW line
    resp.TotalCostUSD = result.Meta.TotalCostUSD
    resp.ModelUsage = result.Meta.ModelUsage
}
```

Update existing `turn_api_test.go` Phase 2 test `TestServeHTTP_IsErrorReturned200` (the one that returns 200 with `isError=true`) to assert `errorMessage` field in the response body too.

### `internal/server/cc_client.go` (NEW, agentserver side)

Mirror of `codex_client.go`. Key shape:

```go
type CcClient struct {
    baseURL string
    secret  string
    http    *http.Client  // 61-minute timeout, same as codex
}

type CcTurnRequest struct {
    WorkspaceID string `json:"workspaceId"`
    SessionID   string `json:"sessionId"`
    UserMessage string `json:"userMessage"`
    Model       string `json:"model,omitempty"`
    TimeoutMs   int    `json:"timeoutMs,omitempty"`
}

type CcTurnResponse struct {
    SessionID     string         `json:"sessionId"`
    AssistantText string         `json:"assistantText"`
    IsError       bool           `json:"isError"`
    ErrorMessage  string         `json:"errorMessage,omitempty"` // Mirrors cc-app-gateway's response
    DurationMs    int64          `json:"durationMs"`
    TotalCostUSD  float64        `json:"totalCostUsd"`
    ModelUsage    map[string]any `json:"modelUsage,omitempty"`
}

func (c *CcClient) RunTurn(ctx context.Context, req CcTurnRequest) (*CcTurnResponse, error)
```

`resolveCCAppGatewayRESTURL()` env-var resolution: `CC_APP_GATEWAY_REST_URL` (primary), no `_URL` fallback variant since cc-app-gateway has no notebook/ws history.

### `internal/server/server.go` (MODIFY)

- Add `CcAppGatewayURL string` field on `Server` struct (mirrors `CodexAppGatewayURL`).
- Register `/api/internal/imbridge/cc/turn` route ONLY if `CC_APP_GATEWAY_REST_URL` is set (mirrors codex's gating at server.go:372-390).
- Construct `ccInboundHandler` with `ccClient`, `sessions`, `imbridgeSendURL`, `internalSecret`.
- **Add `ccHandler.Close()` call to `Server.Close()`** (mirrors codex's `codexHandler.Close()` wiring at server.go:162). Without this, in-flight cc dispatcher turns get orphaned on graceful shutdown.

### `internal/imbridge/bridge.go` (MODIFY)

The actual function is `Bridge.forwardMessage()` at `bridge.go:414` (NOT `routeMessage()` — spec v1 used the wrong name). The switch at lines 422-427 has `case "codex"` explicit and `default` for nanoclaw. Add `case "managed_cc": b.forwardToManagedCC(ctx, msg, binding)` before `default`.

Add `forwardToManagedCC()` method that mirrors `forwardToCodex()` (bridge.go:435-483) — same payload shape (channel_id, workspace_id, wechat_user_id, text, media_*, quoted_*), POSTed to `/api/internal/imbridge/cc/turn` on agentserver. **MUST explicitly set `X-Internal-Secret` header from `os.Getenv("INTERNAL_API_SECRET")`** — matches codex bridge.go:470. (Not implied; explicit code line.)

Also update the stale doc comment on `BridgeBinding.RoutingMode` (bridge.go:51, currently says `// "nanoclaw" (default) or "codex"`) to include `managed_cc`.

### Misconfiguration safeguard: managed_cc set + ccAppGateway disabled

When a channel has `routing_mode=managed_cc` but cc-app-gateway is disabled in helm (or `CC_APP_GATEWAY_REST_URL` is empty), agentserver never registers the inbound route → imbridge POST returns 404 → `forwardMessage` returns error → imbridge retries every 2s forever, flooding logs and blocking cursor advancement.

Mitigation: **add startup-time validation** in agentserver. On boot, if `CC_APP_GATEWAY_REST_URL` is empty, log a warning AND query DB for any `workspace_im_channels` with `routing_mode='managed_cc'`. If any exist, log a HIGH-severity warning naming the affected channels. Operators see "cc-app-gateway disabled but N channels expect it" instead of a flood of 404s in logs.

### `internal/imbridgesvc/handlers.go` (MODIFY)

The `routing_mode` validator at L977 currently rejects anything that isn't `nanoclaw` or `codex`. Add `managed_cc` to the allow list:

```go
if mode != "nanoclaw" && mode != "codex" && mode != "managed_cc" {
    http.Error(w, `invalid routing_mode: must be nanoclaw, codex, or managed_cc`, http.StatusBadRequest)
    return
}
```

### `deploy/helm/agentserver/templates/agentserver.yaml` (MODIFY)

Add env var on agentserver pod. **Wrap the ENTIRE `- name:` block inside the conditional** (mirrors codex pattern at deployment.yaml:184-196). Rendering an empty-string env var (`CC_APP_GATEWAY_REST_URL=""`) instead of omitting it is silently equivalent but inconsistent with how codex does it and surprises operators inspecting pod env:

```yaml
{{- if .Values.ccAppGateway.enabled }}
- name: CC_APP_GATEWAY_REST_URL
  value: "http://{{ .Release.Name }}-cc-app-gateway.{{ .Release.Namespace }}.svc:{{ .Values.ccAppGateway.port }}"
{{- end }}
```

The conditional ensures agentserver doesn't try to route to a non-existent cc-app-gateway when it's disabled (Phase 1 default). When disabled, the env var is ABSENT (not empty-string) — `os.Getenv` returns `""` in both cases, but absence is the clearer signal in `kubectl exec ... env | grep CC_APP_GATEWAY` output.

### Tests

- `cc_im_inbound_test.go` — table-driven test covering: new session creation, existing session reuse, ccClient call mocking, send-back happy path, transport error path, isError=true in response body, dispatcher serialization for same (channel, user), distinct (channel, user) parallelism.
- `cc_client_test.go` — httptest server returning canned CcTurnResponse; verify RunTurn URL, headers, body shape, response decoding.
- `imbridgesvc/handlers_test.go` — routing_mode validator accepts `managed_cc`.
- Integration smoke (NEW `integration/cc_im_test.go` in agentserver): docker-compose with imbridge mock + agentserver + cc-app-gateway:dev + fakes from Phase 2; sends a fake IM message via the imbridge mock's `/api/internal/imbridge/cc/turn` endpoint, asserts the reply comes back via the imbridge mock's `/send` endpoint within timeout.

## Concurrency

The ccDispatcher (per (channel_id, wechat_user_id) FIFO) gives natural serialization. Combined with Phase 2's per-session mutex on cc-app-gateway side (which serializes by (workspace_id, session_id)), the same user's WeChat messages are processed strictly in order, with cross-pod safety still bounded by sessionAffinity (Phase 2 § Open Risks #2 unchanged).

Dispatcher cap = 5 (mirrors codex). Drop-oldest semantics: if a user spams 6 messages while the dispatcher is busy, the oldest queued message is silently dropped to keep the latest. Phase 4 inherits this without modification — codex's reasoning (users care more about their latest message than their oldest queued one) applies equally here.

## Error handling matrix

Mirror codex's mapping verbatim:

| Failure | User-facing message (中文, matches codex) |
|---|---|
| `ccClient.RunTurn` returns transport error (network) | `"cc-app-gateway 暂时无法访问，请稍后再试"` |
| `RunTurn` returns timeout (61min exceeded — should be impossible since runner cap is 10min) | `"对话超时，请重新发送"` |
| `response.IsError == true && Meta.ErrorMessage contains "context"` | `"上下文已满，请新开对话（管理员请清理 session）"` |
| `response.IsError == true` generic | `"Claude 返回错误：{ErrorMessage}"` |
| `response.AssistantText == ""` (no text in completed turn) | `"Claude 返回为空，请重新发送"` |
| ccDispatcher.Enqueue dropped oldest (queue full) | NO user-visible message — codex doesn't notify either (intentional; user sees their newer message processed, doesn't notice the dropped one) |
| Workspace IM channel not bound to a workspace | Should never reach this code path (imbridge already filters); if it does, log and 400 |

## Phase 4 vs prior contracts

**Wire-level breaking changes from Phase 1/2:** None. cc-app-gateway's `POST /api/turns` contract unchanged. `CcTurnRequest`/`CcTurnResponse` shapes from Phase 1 stay.

**Behavioral changes:**
- agentserver gains a new internal HTTP route `/api/internal/imbridge/cc/turn`.
- agent_sessions table gains a new column `claude_session_id` (nullable; only populated by ccInboundHandler).
- imbridge accepts new `routing_mode=managed_cc` value.

**New required env vars:** `CC_APP_GATEWAY_REST_URL` on agentserver pod. Helm template adds this conditional on `ccAppGateway.enabled`.

## Migration / rollout

After both Phase 1+2+4 PRs merge:

1. Bump Chart.yaml minor version, push `v<version>` git tag.
2. (Operator) Set `ccAppGateway.enabled=true` + `s3.bucket` + `s3.region` in flux config.
3. (Operator) Set the target IM channel's `routing_mode` from `nanoclaw` (or `codex`) to `managed_cc`:
   ```sql
   UPDATE workspace_im_channels SET routing_mode='managed_cc' WHERE id='<channel-id>';
   ```
   Or via the existing PATCH endpoint on imbridgesvc.
4. (Operator) Next message in that channel routes via cc-app-gateway → claude → reply.

**Rollback:** revert routing_mode to `nanoclaw` or `codex`. Conversation history (in S3) preserved — if the channel is later re-enabled to `managed_cc`, prior turns resume.

## Open risks

1. **`agent_sessions.id` vs `claude_session_id` confusion.** Two IDs for the same row. Future readers might wonder which one to use where. Mitigation: clear naming, doc comments on both columns, only `claude_session_id` ever leaves the agentserver process (cc-app-gateway only sees this one; UI/audit code uses `id`).

2. **No idempotency on duplicate IM messages.** imbridge may redeliver a message (rare, but possible during reconnect). cc-app-gateway sees two requests for the same session, second one resumes from first's S3 state — claude sees the message twice in its jsonl and may produce two replies. Mitigation: imbridge already has dedupe at delivery layer; Phase 4 doesn't add a second layer. Document as known.

3. **Quoted message + media fields silently dropped — REGRESSION vs codex.** Phase 4 forwards only `text` to claude. A user replying to an image with a question loses the image context entirely. This is a functional REGRESSION for any channel migrating from `codex` to `managed_cc` — codex's `buildCodexInput` builds an ordered `input[]` array with quoted text and base64 images. Mitigation: emit log line + Prometheus counter `cc_im_inbound_dropped_media_total` per dropped field; document in operator runbook that switching codex → managed_cc loses image / quote context.

   Phase 4.5 (NOT Phase 5+) — claude supports vision NATIVELY via the Messages API; no MCP tool required. The block is at the `claude --print` interface (only takes text on the CLI), not at the model. Phase 4.5 is a small follow-up: extend `cc-app-gateway`'s `RunInput` to carry `[]InputBlock` instead of `UserMessage string`, then pass through to stream-json's `SDKUserMessage.message.content[]` array (already supports `{type:"image",source:{...}}` per the Anthropic Messages schema). ~200 LOC; worth doing before declaring Phase 4 production-ready for image-heavy channels.

4. **Dispatcher cap = 5 / drop-oldest is silent.** A user spamming 10 messages would have 5 silently dropped. The user sees the latest reply but the dropped messages are lost. Codex accepts this; Phase 4 inherits. Mitigation: only relevant for high-frequency-spam scenarios, which IM users rarely hit.

5. **No per-channel model override.** All `managed_cc` channels share `CCAPPGW_DEFAULT_MODEL`. Power users wanting opus per-channel get a Phase 5 follow-up.

6. **Cross-channel session collision risk:** if two different IM channels are bound to the same `workspace_id` AND a user has the same `wechat_user_id` in both (e.g. same person in two groups in the same workspace), `GetSessionByExternalID(workspace_id, wechat_user_id)` returns the SAME session — so the conversation history bleeds across channels. Codex has the same behavior. Mitigation: document as known; if it's a problem in practice, the `external_id` formula needs to include channel_id (Phase 5+).

7. **ccDispatcher is a copy of codexDispatcher, not a shared package.** Two ~100-LOC implementations with subtle differences (request struct type, dispatcher key composition). If codex fixes a bug (e.g. drop-oldest semantics, channel cap), cc needs a parallel fix. Mitigation deferred — Phase 5+ may extract a generic `internal/imdispatch/` if a third backend appears or if drift causes actual bugs. Until then, two copies are cheaper than one premature abstraction.

8. **codex→managed_cc migration loses conversation history.** When operators switch a channel's routing_mode, claude starts fresh — codex's prior history is invisible. Documented in Migration § Rollback. Users see "new conversation" with no warning; consider adding a one-time admin notification mechanism in Phase 5+ if migrations become common.

## Acceptance

A developer can:

1. (Setup) Spin up the docker-compose harness from Phase 2 (`internal/ccappgateway/testdata/integration/`), then ADDITIONALLY start an agentserver instance pointing at the running cc-app-gateway:
   ```bash
   # NOTE: AGENTSERVER_INTERNAL_URL is cc-app-gateway's env var (it calls
   # agentserver for /internal/workspace-token). It must be set on the
   # cc-app-gateway PROCESS, not on agentserver. Phase 2 docker-compose
   # already sets it correctly inside the cc-app-gateway container.
   INTERNAL_API_SECRET=secret123 \
   CC_APP_GATEWAY_REST_URL=http://localhost:8087 \
   IMBRIDGE_URL=http://localhost:8090 \  # mock imbridge listener for the test
   go run ./cmd/agentserver-go
   ```

2. Simulate an IM bridge call:
   ```bash
   curl -sX POST http://localhost:8080/api/internal/imbridge/cc/turn \
     -H "X-Internal-Secret: secret123" \
     -H "Content-Type: application/json" \
     -d '{
       "channel_id":     "ch_test",
       "workspace_id":   "ws_test",
       "wechat_user_id": "wxid_alice",
       "wechat_sender":  "Alice",
       "text":           "Remember code DELTA-9."
     }'
   # Expected: 202 + {"queued":true}
   ```

3. Within ~5 seconds, the mock imbridge listener at port 8090 receives:
   ```json
   POST /api/internal/imbridge/send
   {"channel_id":"ch_test","to_user_id":"wxid_alice","text":"<claude's reply containing DELTA-9 acknowledgement>"}
   ```

4. Send a second turn for the SAME (channel, user):
   ```bash
   curl ... -d '{... "text":"What's the code?"}'
   # Mock imbridge receives reply containing "DELTA-9" — proving resume across IM turns works end-to-end
   ```

5. Verify minio (via Phase 2 harness) shows the session tarball:
   ```bash
   docker compose -f internal/ccappgateway/testdata/integration/docker-compose.yml exec minio \
       mc ls local/cc-app-gateway-test/cc-app-gateway/ws_test/
   # → <uuid>.tar.gz exists; same UUID as what agent_sessions.claude_session_id stores
   ```

Integration test `TestIntegration_IMToCcEndToEnd` automates steps 2-5; included in Phase 4.

## Naming: why `managed_cc` not `cc`?

routing_mode values are immutable wire-level strings (stored in DB rows, referenced by operators). The `managed_` prefix distinguishes this gateway's "we run claude on the server side" mode from a future possible "user runs cc TUI locally and we proxy" mode (see [[cc_v2_1_185_gateway_feasibility]] for why the local-TUI path is technically blocked today, but it's a real product surface we don't want to permanently foreclose). Bare `cc` would force a rename if that ever ships. The `managed_` prefix is one extra word per DB row and operator command in exchange for keeping the namespace open.

## Audit revisions (2026-06-22, post-self-review)

Spec v1 was self-audited adversarially. Three critical + three important bugs found and patched.

### Revision 1 (Critical) — `CcTurnResponse` had no `ErrorMessage` field

**v1 error matrix referenced `Meta.ErrorMessage` in cc-app-gateway's HTTP response body.** That field doesn't exist in the Phase 1 contract — `CcTurnResponse` carries only `IsError bool`. The context-window-exceeded detection branch was un-implementable as written.

**Patched:** Spec now amends `CcTurnResponse` (Phase 1 turn_api.go) to add `ErrorMessage string` populated from `ResultMeta.ErrorMessage` (which runner/events.go already extracts from claude's result/error SDKMessage). Phase 4 PR touches cc-app-gateway code, not only agentserver code. Updated existing Phase 2 test `TestServeHTTP_IsErrorReturned200` to assert the new field.

### Revision 2 (Critical) — codex→managed_cc routing switch silently failed

**v1 said:** "Non-goal: switching = new session."

**Reality:** When a channel migrates `codex` → `managed_cc`, `GetSessionByExternalID` returns the existing codex row WITH `claude_session_id IS NULL`. processTurn would pass NULL to cc-app-gateway, triggering uuidRe validation 400 → user sees generic error → no clean migration path.

**Patched:** processTurn flow now has explicit hit-but-NULL branch: mint fresh UUID, call `SetSessionClaudeSessionID` to persist it, continue with new claude_session_id. Codex's conversation history does NOT carry over (different agent stack), but the user gets a clean start instead of a failure. `SetSessionClaudeSessionID` setter is consequently NOT dead code.

### Revision 3 (Critical) — `CreateSession()` doesn't exist on `db.DB`

**v1 said:** "Modify `CreateSession()` to also mint a UUID and write `claude_session_id`."

**Reality:** `CreateSession` lives on `dbSessionStore` in `codex_im_inbound.go:604-619`, NOT in `internal/db/`. Modifying a method that doesn't exist would have stalled the implementer.

**Patched:** spec now specifies cc handler gets its own `ccDbSessionStore` (alongside codex's), with its own `CreateSession` mints both id and claude_session_id. The shared `sessionView` struct (in codex_im_inbound.go) gains `ClaudeSessionID string` field — zero-value `""` is fine for codex's use (it never reads the field). DB-layer changes are smaller: just add the column to the row struct + SELECT lists + add a `SetSessionClaudeSessionID` setter.

### Revision 4 (Important) — Helm env-var was empty-string instead of absent

**v1 used:** `value: {{ if .Values.ccAppGateway.enabled -}} "url" {{- end }}` — when disabled, renders empty string env var.

**Reality:** codex's pattern wraps the entire `- name: ... value: ...` block inside `{{- if -}}` so the var is ABSENT when disabled. Operators inspecting `kubectl exec ... env` see clearer signal. `os.Getenv` returns `""` for both, so functionally equivalent, but inconsistent with project convention.

**Patched:** spec now wraps entire block in conditional, matching codex pattern.

### Revision 5 (Important) — `Server.Close()` lifecycle for cc dispatcher was missing

**v1 didn't mention:** wiring `ccHandler.Close()` into agentserver's `Server.Close()`.

**Reality:** codex does this at server.go:162. Without the analogous wiring, in-flight cc dispatcher turns get orphaned on graceful shutdown — workers blocked in `processTurn` get killed mid-S3-upload (via cc-app-gateway's own Shutdown path) but never report back to imbridge.

**Patched:** spec now explicitly lists `ccHandler.Close()` in `Server.Close()` wiring.

### Revision 6 (Important) — acceptance step pointed env vars at wrong process

**v1 acceptance step:** passed `AGENTSERVER_INTERNAL_URL` to `go run ./cmd/agentserver-go`.

**Reality:** that env var is cc-app-gateway's (it's how cc-app-gateway finds agentserver for workspace-token fetch). Setting it on agentserver itself is a no-op. Phase 2's docker-compose already sets it correctly inside the cc-app-gateway container.

**Patched:** acceptance step removes the misplaced env var with an explanatory note.

### Minor doc fixes also applied

- `Bridge.routeMessage()` → corrected to `Bridge.forwardMessage()` at bridge.go:414.
- `BridgeBinding.RoutingMode` doc comment update is explicit.
- `X-Internal-Secret` header on forwardToManagedCC is explicit (not implied by "mirrors forwardToCodex").
- Network-path assumption (pod-to-pod, no ingress) documented.
- Concurrency ceiling (50 users × 350 MB) documented as inheriting Phase 1 Open Risk #7.
- Misconfiguration safeguard added: startup-time DB check for managed_cc channels when ccAppGateway is disabled.
- Media/quote drop is now explicitly labeled REGRESSION (not just "drop") with Prometheus counter + runbook note + Phase 4.5 path forward.
- `managed_cc` naming justified.
- Vision support correctly framed as Phase 4.5 (Anthropic Messages API native, NOT requiring MCP).

### Audit findings NOT applied (deliberate)

- **ccDispatcher generic extraction:** still deferred. Audit argued Phase 4 is the right moment (second consumer added). Decision: extraction can come in Phase 5+ when (a) a third backend would clearly benefit, or (b) one of the two copies diverges in a way that costs us actual bugs. Until then, two ~100-LOC copies are cheaper than one shared abstraction with two callers. Documented as Open Risk #7.
- **Cross-channel session collision (same user in two channels in same workspace):** acknowledged as Open Risk #6 (same behavior as codex; not Phase 4's job to fix).
- **No idempotency on duplicate IM messages:** acknowledged as Open Risk #2 (codex has same).
- **Per-channel model override:** Phase 5+ — Open Risk #5 (all managed_cc channels share `CCAPPGW_DEFAULT_MODEL`).

## Plan files

- `docs/superpowers/plans/2026-06-22-cc-app-gateway-phase4.md` (TDD task breakdown — to be written next).
