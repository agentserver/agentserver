# codex-exec-gateway 完整审计 — Design

**Date**: 2026-05-23
**Status**: Draft
**Author**: mryao (with Claude Opus 4.7)
**Supersedes**: 2026-05-05-codex-app-gateway-and-exec-gateway-design.md 的 Plan 2 "operation log" 部分（整套 `internal/codexappgateway/oplog/` + `operations` 表）。

## Goal

让所有经过 codex-exec-gateway 流向 codex-exec 的"指示"和对应"响应"都被完整、有结构地记录下来，作为可查询的审计源。每条记录要回答：

- **谁**：哪个 user_id、哪个 workspace_id。
- **从哪儿来**：是 codex spawned 的 env-mcp（envmcp 路径），还是 SDK 直连的 REST 路径（rest 路径），还是 file relay 路径。
- **到哪儿去**：目标 exe_id（codex-exec 执行器 ID）。
- **是什么**：JSON-RPC method（如果可解析），原始 payload 字节（必要时去对象存储）。
- **何时**：started_at（gateway 收到请求帧）、completed_at（gateway 收到匹配响应帧）、duration_ms。
- **结果**：is_error、响应 payload、字节数。

并且：

- **同时删除** 现有的 `internal/codexappgateway/oplog/` + `operations` 表 + `/api/workspaces/{id}/operations` 端点 + 前端 `OperationsPanel`。原系统只覆盖 `mcpServer/tool/call` 这一条窄路径，在生产实际流量里命中率为零（见 2026-05-23 session 内的实证调查），保留它只会让维护方混淆。

## Non-goals

- 不审计 codex-exec 内部的子操作（执行器主机上跑了什么 shell 命令、写了什么文件）。审计边界严格止于 gateway。
- 不审计 codex-app-gateway → codex 子进程之间的 MCP 流量。那条路径是 LLM 转译层，不是"对 codex-exec 的指示"。
- 不做实时告警 / 流式订阅。Audit 是事后查询，不是实时事件总线。
- 不做敏感信息 redaction。Payload 里可能出现用户写在脚本里的 API key，v1 接受这个事实（同租户可见，跨租户不可见，靠 workspace 边界隔离）；redaction 留待 v2。
- 不为审计落库提供 100% 强一致语义。在 gateway pod 被硬杀（OOMKill、节点丢失）的瞬间，最后一个未 fsync 的 batch 可能丢失。可接受这个失败模式；正常 SIGTERM 优雅关闭路径会 flush。

## Scope of deletion（先讲删，再讲建）

下面这些文件 / 表 / 配置整体删除：

| 路径 | 动作 | 备注 |
|---|---|---|
| `internal/codexappgateway/oplog/` | 整个目录删除 | client.go / interceptor.go / doc.go / 测试 |
| `internal/codexappgateway/server.go` 中 `oplogClient`、`oplogList` 字段 + 初始化 + Close | 删除 | 见 server.go:73-75, 138-140, 276-277, 285-286 |
| `internal/codexappgateway/config.go` 中 `OperationLogURL`、`OperationLogSecret`、`OperationLogChan` | 删除 | 见 config.go:70-75, 118-124 |
| `cmd/codex-app-gateway/serve_args.go` 中 `-oplog-url` / `-oplog-secret` / `-oplog-chan` 标志和 env 解析 | 删除 | |
| `cmd/codex-app-gateway/serve_args_test.go` 相关 case | 删除 | |
| `internal/db/operations.go` | 整个文件删除 | |
| `internal/server/operations.go` | 整个文件删除 | |
| `internal/server/operations_retention.go` | 整个文件删除 | |
| `internal/server/operations_test.go` | 整个文件删除 | |
| `internal/server/server.go` 中 `/internal/operations` POST/GET + `/api/workspaces/{id}/operations` GET 路由 | 删除 | 见 server.go:326-336 + protected group |
| `internal/server/api_types.go` 中 `WorkspaceOperationsResponse`、`OperationRecord` | 删除 | 见 api_types.go:541 附近 |
| `cmd/serve.go` 中 retention loop 启动 + `AGENTSERVER_OPERATIONS_RETENTION_DAYS` 环境变量解析 | 删除 | 见 serve.go:283-293, 333 |
| `deploy/helm/agentserver/values.yaml` 中 `operations:` 配置块（行 243-253） | 删除 | |
| `deploy/helm/agentserver/templates/codex-app-gateway.yaml` 中 `CXG_OPLOG_URL` / `CXG_OPLOG_SECRET` / `CXG_OPLOG_CHAN` env 注入（行 130-136） | 删除 | |
| `web/src/components/OperationsPanel.tsx` | 整个文件删除 | |
| `web/src/lib/api.ts` 中 operations 相关 fetch + `WorkspaceOperationsResponse` 类型 | 删除 | 见 api.ts:923 |
| `web/src/components/ManageWorkspaces.tsx` + `WorkspaceDetail.tsx` 中对 OperationsPanel 的 import 和挂载 | 删除调用，UI 入口由新的 exec-audit panel 替代 | |
| `web/src/lib/api-generated/schema.d.ts` 中 operations 类型 | 由 swagger 重新生成时自然消失 | |

**数据库迁移**：新增 migration `internal/db/migrations/0NN_drop_operations.sql`：

```sql
DROP TABLE IF EXISTS operations;
```

迁移号取当前最大号 +1。生产里 `operations` 表实际是空的（已实证），无数据丢失风险。

## Architecture

```
                       ┌────────────────────────────────┐
                       │  agentserver  (Postgres)       │
                       │                                │
                       │  exec_audit_sessions           │
                       │  exec_audit_calls              │
                       │  exec_audit_payloads           │
                       └──────────────▲─────────────────┘
                                      │
                                      │  POST /internal/exec-audit/batch
                                      │  (X-Internal-Secret, idempotent)
                                      │
                       ┌──────────────┴─────────────────┐
                       │  agentserver  (Go HTTP)        │
                       │  - exec-audit ingester         │
                       │  - exec-audit query API:       │
                       │    /api/workspaces/{id}/       │
                       │       exec-audit/...           │
                       └──────────────▲─────────────────┘
                                      │
                                      │  HTTP (batched, exponential backoff)
                                      │
            ┌─────────────────────────┴───────────────────────────┐
            │  codex-exec-gateway pod                              │
            │                                                      │
            │   bridge.go runBridgePump  ←tap→  ┐                  │
            │   inbound.go runInboundReader←tap→├→ audit.Recorder ─┤
            │   handlers/sdk_*.go (REST)   ←tap→┘                  │
            │   handlers_relay.go (PUT/GET)←tap→┘                  │
            │                                       │              │
            │                                       ▼              │
            │                              ┌─────────────────┐     │
            │                              │ audit.WAL       │     │
            │                              │ append-only     │     │
            │                              │ /var/cxg-audit/ │     │
            │                              │ wal-YYYYMMDD-HH │     │
            │                              └────────┬────────┘     │
            │                                       │              │
            │                              ┌────────▼────────┐     │
            │                              │ audit.Uploader  │     │
            │                              │ goroutine       │     │
            │                              │ reads WAL,      │     │
            │                              │ batches, POSTs  │     │
            │                              └─────────────────┘     │
            └──────────────────────────────────────────────────────┘
                                      ▲
                                      │ WS (RelayMessageFrame)
                                      │
                            ┌─────────┴──────────┐
                            │  codex-exec        │
                            │  (执行器)           │
                            └────────────────────┘

  上游入口（不画在内部）：
  - env-mcp ─ws→ /bridge/{exe_id}            (envmcp 来源)
  - SDK     ─https→ /api/sdk/*               (rest 来源, 内部走 bridge.Pool 复用 WS)
  - SDK     ─https→ /relay/{ticket}          (relay 来源, file transfer)
```

### 三类来源（source）

| source | 入口路径 | 触发器 | 用户身份来源 | 是否走 bridge.Pool |
|---|---|---|---|---|
| `envmcp` | `/bridge/{exe_id}` WS | codex 子进程 spawn 的 env-mcp 子进程 | cap token 里新增的 `user_id` 字段（见下） | 不走 pool — 每个 env-mcp 自己拉一条 WS |
| `rest` | `/api/sdk/*` HTTPS | Jupyter / 外部 SDK 直接调 | proxyToken 验证后返回的 `{workspace_id, user_id}` | 走 pool（共享/复用到 executor 的 WS） |
| `relay` | `/relay/{ticket}` PUT/GET | SDK 调 `copy_path` 类工具触发的文件传输 | ticket 关联的 source/dest workspace | 不走 bridge（独立 HTTPS 字节流到 codex-exec 上游 / 下游） |

不审计的来源（明确排除）：

- `/api/exec-gateway/connected` `/api/exec-gateway/relay/create` — 这些是控制平面，不下达指示到 codex-exec，仅靠 slog 日志即可。
- `/api/codex-exec/register` `/cloud/executor/{id}/register` — 执行器自己向 gateway 注册的 handshake，不是"指示"。
- `/codex-exec/{exe_id}` 的 inbound WS handshake 本身 — handshake 之后的每一帧才进审计。

## Interception points

### 1. envmcp 路径（WS bridge）

**入口**：`internal/codexexecgateway/bridge.go` `handleBridge` (line 28)。修改点：

- `handleBridge` 在 `VerifyCapabilityToken` 成功后构造 `bridgeSession` 时，附加 `originMeta`：
  ```go
  type bridgeOrigin struct {
      Source   string // "envmcp"
      UserID   string // 从扩展后的 CapPayload.UserID 取
      ClientIP string
  }
  ```
- WS upgrade 完成后、读取首个 Resume 帧后（line 130 附近），向 `audit.Recorder` 提交 `SessionOpen` 事件，拿到 `auditSessionID`，挂在 bridgeSession 上。
- `runBridgePump`（line 170-205）每次准备 forward 帧给 inbound 之前，调用 `recorder.RecordFrame(auditSessionID, "to_backend", frame)`。
- `runInboundReader`（inbound.go line 80-123）在 `routes[stream_id].write(frame)` 之前，调用 `recorder.RecordFrame(auditSessionID, "to_client", frame)`。
  - 注：inbound reader 只知道 stream_id，需要从 `inboundConn.routes[stream_id]` 反查出 `bridgeSession.auditSessionID`，这个映射本来就存在。
- bridge 关闭时（handleBridge defer / runBridgePump 退出路径），调用 `recorder.RecordSessionClose(auditSessionID, reason)`。

**Cap token 扩展**：`internal/codexexecgateway/auth.go` 中 `CapPayload` 增加：

```go
type CapPayload struct {
    TurnID      string `json:"turn_id"`
    WorkspaceID string `json:"workspace_id"`
    UserID      string `json:"user_id,omitempty"`   // 新增，向后兼容
    IAT         int64  `json:"iat"`
    EXP         int64  `json:"exp"`
}
```

签发方是 codex-app-gateway，它在 spawn env-mcp 时知道 user_id；签发处需要把 user_id 填入。验证侧已存在的 token（无 user_id 字段）继续有效，UserID 为 "" 即可，审计记录里 user_id 为 NULL，不阻塞流量。

### 2. rest 路径（SDK 直连）

**入口**：`internal/codexexecgateway/handlers/sdk_*.go`（具体文件以 2026-05-20-sdk-rest-via-exec-gateway-design.md 落地后的实际文件为准）。

每个 SDK handler 在 `envtools.Registry[tool].Call(args, bridge)` 调用前后包一层：

```go
auditCallID := recorder.RecordCallStart(ctx, audit.CallStart{
    Source:      "rest",
    WorkspaceID: wsID,           // proxyToken 验证后得到
    UserID:      userID,
    ExeID:       exeID,           // URL 参数
    Method:      "shell",         // tool name
    Request:     argsJSON,        // 原始 JSON
    StartedAt:   time.Now(),
})
result, err := envtools.Registry[tool].Call(ctx, args, bridge)
recorder.RecordCallEnd(auditCallID, audit.CallEnd{
    CompletedAt: time.Now(),
    IsError:     err != nil,
    Response:    resultJSON,      // err 或 result 序列化
})
```

注：rest handler 内部 `bridge.Pool.Get(exeID)` 拿到的 WS 也会被 `runBridgePump` / `runInboundReader` 拦截，但 pool-managed bridge 的 `bridgeOrigin.Source = "internal_pool"`，frame-level 记录会被 dropped（避免重复记录同一个语义请求）。语义层只在 SDK handler 这一层记录。

### 3. relay 路径（file transfer）

**入口**：`internal/codexexecgateway/handlers_relay.go`（猜测，实际以代码为准；不存在则在 `relay/` 包内）。

`PUT /relay/{ticket}` 收到字节流时，记录：
- source = "relay"
- workspace_id = ticket.SourceWorkspaceID
- user_id = ticket.CreatorUserID
- exe_id = ticket.DestExeID
- method = "relay_put" / "relay_get"
- payload_size = Content-Length 或 累计字节数
- 不记录 payload 本身（可能是 GB 级二进制文件），只记 sha256

## Data model

三张新表，全部在 agentserver 的 postgres 里：

### `exec_audit_sessions`

一行 = 一次 envmcp WS bridge 会话；rest 和 relay 路径**不创建** session 行（它们是单次 call 模型）。

```sql
CREATE TABLE exec_audit_sessions (
  id              UUID PRIMARY KEY,
  workspace_id    TEXT NOT NULL,
  user_id         TEXT,                          -- 可能为 NULL（旧 cap token）
  exe_id          TEXT NOT NULL,
  turn_id         TEXT,                          -- 来自 cap token
  stream_id       TEXT NOT NULL,                 -- 第一个 Resume 帧带的 ID
  client_ip       INET,
  cap_iat         TIMESTAMPTZ,
  cap_exp         TIMESTAMPTZ,
  opened_at       TIMESTAMPTZ NOT NULL,
  closed_at       TIMESTAMPTZ,
  close_reason    TEXT,                          -- "reset" | "client_disconnect" | "backend_disconnect" | "shutdown"
  frames_to_backend  INT NOT NULL DEFAULT 0,
  frames_to_client   INT NOT NULL DEFAULT 0,
  bytes_to_backend   BIGINT NOT NULL DEFAULT 0,
  bytes_to_client    BIGINT NOT NULL DEFAULT 0
);

CREATE INDEX exec_audit_sessions_ws_time ON exec_audit_sessions(workspace_id, opened_at DESC);
CREATE INDEX exec_audit_sessions_exe_time ON exec_audit_sessions(exe_id, opened_at DESC);
CREATE INDEX exec_audit_sessions_user_time ON exec_audit_sessions(user_id, opened_at DESC) WHERE user_id IS NOT NULL;
CREATE INDEX exec_audit_sessions_turn ON exec_audit_sessions(turn_id) WHERE turn_id IS NOT NULL;
```

### `exec_audit_calls`

一行 = 一个 logical call。三种 source 都写这张表。envmcp 路径下，对 JSON-RPC 的 request/response 配对成对写入一行（按 RPC id 配对）；不能配对的（notification、Resume/Reset、非 JSON 帧）单独成行，response_payload_id 为 NULL。

```sql
CREATE TABLE exec_audit_calls (
  id                  UUID PRIMARY KEY,
  session_id          UUID REFERENCES exec_audit_sessions(id) ON DELETE CASCADE,  -- NULL for rest/relay
  workspace_id        TEXT NOT NULL,
  user_id             TEXT,
  exe_id              TEXT NOT NULL,
  source              TEXT NOT NULL CHECK (source IN ('envmcp','rest','relay')),

  rpc_id              TEXT,                       -- JSON-RPC id（如果可解析）
  rpc_method          TEXT,                       -- 工具名或 RPC method
  rpc_kind            TEXT,                       -- 'request'|'notification'|'frame' 等

  request_payload_id  UUID REFERENCES exec_audit_payloads(id),
  request_size        INT NOT NULL DEFAULT 0,
  request_sha256      TEXT,

  response_payload_id UUID REFERENCES exec_audit_payloads(id),
  response_size       INT NOT NULL DEFAULT 0,
  response_sha256     TEXT,

  is_error            BOOLEAN NOT NULL DEFAULT FALSE,
  error_summary       TEXT,                       -- 截断到 512 字节

  started_at          TIMESTAMPTZ NOT NULL,       -- gateway 收到请求帧 / handler 入口
  completed_at        TIMESTAMPTZ,                -- 收到响应帧 / handler 出口；不成对时为 NULL
  duration_ms         INTEGER
);

CREATE INDEX exec_audit_calls_ws_time   ON exec_audit_calls(workspace_id, started_at DESC);
CREATE INDEX exec_audit_calls_exe_time  ON exec_audit_calls(exe_id, started_at DESC);
CREATE INDEX exec_audit_calls_user_time ON exec_audit_calls(user_id, started_at DESC) WHERE user_id IS NOT NULL;
CREATE INDEX exec_audit_calls_method    ON exec_audit_calls(rpc_method) WHERE rpc_method IS NOT NULL;
CREATE INDEX exec_audit_calls_source    ON exec_audit_calls(source, started_at DESC);
CREATE INDEX exec_audit_calls_session   ON exec_audit_calls(session_id) WHERE session_id IS NOT NULL;
CREATE INDEX exec_audit_calls_errors    ON exec_audit_calls(workspace_id, started_at DESC) WHERE is_error;
```

### `exec_audit_payloads`

Payload 字节，按 sha256 去重，zstd 压缩。

```sql
CREATE TABLE exec_audit_payloads (
  id              UUID PRIMARY KEY,
  sha256          TEXT NOT NULL UNIQUE,
  compressed      BYTEA NOT NULL,                 -- zstd level 3
  original_size   INT NOT NULL,
  compressed_size INT NOT NULL,
  ref_count       INT NOT NULL DEFAULT 0,          -- 引用计数，retention 清理时用
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX exec_audit_payloads_created ON exec_audit_payloads(created_at);
```

**Payload 大小策略**：

- `<= 4 KiB`：直接存进 `exec_audit_payloads.compressed`（zstd 后大概率还更小）。
- `> 4 KiB 且 <= 4 MiB`：仍然存表，但走 zstd 压缩。
- `> 4 MiB`：不存原始字节，只在 `exec_audit_calls` 上记 sha256 + size + 一行 `error_summary`：`"payload truncated: size=N, sha256=..."`；`request_payload_id` / `response_payload_id` 留空。这是硬上限，避免单条记录把表撑爆。配置项 `CXG_AUDIT_PAYLOAD_MAX_BYTES` 默认 4194304。

去重：每次写入前 `INSERT ... ON CONFLICT (sha256) DO UPDATE SET ref_count = ref_count + 1 RETURNING id`。删除时由 retention job 处理（见下）。

### 为什么不直接复用 operations 表

旧 schema 假设 "一次工具调用 = 一行" 且 result_summary <= 4KiB。我们这次要捕获 RelayData payload，可能动辄几十 KiB 到 MiB；也要明确区分 source / direction / session 维度。强行往 operations 表里塞会让那张表既不像旧用法也不像新用法。所以新建专用表，旧表整体删除（前面 deletion 清单已列）。

## Local WAL 与 Uploader

### WAL 文件格式

路径：`/var/cxg-audit/wal-YYYYMMDD-HHMMSS.log`，按小时滚动。

记录格式（append-only，无锁多写者通过单 goroutine 串行）：

```
[4 bytes BE uint32: length] [length bytes: protobuf-encoded WALRecord]
```

protobuf：

```proto
// internal/codexexecgateway/audit/walpb/wal.proto
message WALRecord {
  string id = 1;                       // UUID, 上传时即 audit_calls.id 或 audit_sessions.id
  oneof body {
    SessionOpen  session_open  = 2;
    SessionClose session_close = 3;
    CallStart    call_start    = 4;
    CallEnd      call_end      = 5;
    FrameRecord  frame         = 6;
  }
  int64 written_at_unix_nano = 7;
}

message SessionOpen  { /* workspace_id, user_id, exe_id, turn_id, stream_id, client_ip, cap_iat, cap_exp, opened_at */ }
message SessionClose { /* session_id, closed_at, close_reason, frames_to_backend, frames_to_client, bytes_to_backend, bytes_to_client */ }
message CallStart    { /* call_id, session_id (optional), workspace_id, user_id, exe_id, source, rpc_id, rpc_method, rpc_kind, request_bytes (inline), request_size, request_sha256, started_at */ }
message CallEnd      { /* call_id, completed_at, is_error, error_summary, response_bytes (inline), response_size, response_sha256 */ }
message FrameRecord  { /* session_id, seq, direction, frame_type, ts, payload_bytes (inline up to 4 MiB), payload_size, payload_sha256 */ }
```

**Fsync 策略**：

- 写一条记录后 `os.File.Write`，**不**立刻 fsync。
- 每 100 ms 或每 256 条记录中任一先到，触发一次 `Sync()`。
- 优雅关闭路径（SIGTERM handler）做一次 final flush + sync。
- 极端故障（OOMKill）下最后 100 ms 的记录可能丢；接受。

**WAL 文件大小上限**：单文件 1 GiB，超出立刻 rotate；总目录大小 10 GiB（`CXG_AUDIT_WAL_DISK_QUOTA_BYTES`），超出时：

- 默认行为：log FATAL + 拒绝新的 bridge 握手（"fail closed"），保证"完整记录"承诺。这对 ops 是高优告警。
- 可选行为（`CXG_AUDIT_WAL_OVERFLOW=drop`）：删除最旧的已上传 WAL 文件，给新记录腾地方；如果最旧的还未上传，则丢弃新记录并增 metric。生产推荐第一种。

### Cursor

`/var/cxg-audit/cursor.json`：

```json
{
  "files": [
    { "name": "wal-20260523-150000.log", "uploaded_offset": 31457280 },
    { "name": "wal-20260523-160000.log", "uploaded_offset": 0 }
  ]
}
```

Uploader 进程在每次成功 ack 一个 batch 后，原子性地（写临时文件 + rename）更新此文件。
旧 WAL 文件 `uploaded_offset == file_size` 后，再等 24 小时（grace）再删除（便于事后排查）。

### Uploader goroutine

```
loop:
  - 读 cursor，找到当前未上传 offset 的 WAL 文件
  - 从该 offset 流式读 protobuf 记录
  - 凑齐 200 条或 1 MiB 之一，HTTP POST 到 agentserver /internal/exec-audit/batch
  - 成功 (200 OK)：cursor.uploaded_offset += batch_bytes
  - 失败（5xx / network / 4xx 但 not 401）：指数退避 1s → 2s → 5s → 30s → 60s → 5min（封顶），不动 cursor
  - 4xx (401/403)：log FATAL，不重试（密钥配置错误）
```

POST body：

```json
{
  "gateway_id": "cxg-pod-xxxx",
  "records": [ <protobuf-Json-encoded WALRecord>, ... ]
}
```

或者直接 `Content-Type: application/x-protobuf`，body 是 `BatchRecords { repeated WALRecord records = 1; }`。protobuf 更紧凑、不需要 base64 转 bytes 字段，**推荐 protobuf**。

### Agentserver 接收端：`/internal/exec-audit/batch`

Handler：`internal/server/exec_audit.go` 新建。

```go
func (s *Server) postInternalExecAuditBatch(w http.ResponseWriter, r *http.Request) {
    // X-Internal-Secret check
    // Content-Type: application/x-protobuf
    // Unmarshal BatchRecords
    // 单事务内逐条写入：
    //   - SessionOpen → INSERT INTO exec_audit_sessions ... ON CONFLICT (id) DO NOTHING
    //   - SessionClose → UPDATE exec_audit_sessions SET closed_at=..., close_reason=..., 
    //                    frames_to_backend=..., bytes_to_backend=... WHERE id=$1
    //   - CallStart → INSERT INTO exec_audit_calls ... ON CONFLICT (id) DO NOTHING
    //                 同时如有 request_bytes，先 upsert payloads，把 id 写到 calls.request_payload_id
    //   - CallEnd → UPDATE exec_audit_calls SET completed_at=..., is_error=...,
    //               response_payload_id=..., response_size=..., response_sha256=...
    //               WHERE id=$1
    //   - Frame → 如果开启 frame-level capture，写一张 exec_audit_frames 表（v1 不开，见下）
    // 返回 200 + {processed: N, skipped: M}
}
```

幂等性：所有 INSERT 走 `ON CONFLICT (id) DO NOTHING` 或 `ON CONFLICT DO UPDATE`，保证重传安全。

### Frame-level 表（v1 不开，v1.5 可选）

`exec_audit_calls` 已经把 JSON-RPC 请求/响应配对了，能回答用户的核心问题（指示是什么、响应是什么）。但 envmcp 路径下，有些 RelayData payload 不是 JSON-RPC（比如 codex 早期自定义帧），不能 RPC-pair；这种情况 v1 单独成行 `rpc_kind='frame'`，把 raw bytes 当作 request 存。

如果将来要 100% 帧粒度回放（含 Resume/Reset/Heartbeat），可以新增 `exec_audit_frames` 表，按 (session_id, seq) PK。v1 不做。

## Recorder + Pump 集成

新包 `internal/codexexecgateway/audit/`：

```
internal/codexexecgateway/audit/
├── recorder.go         # Recorder interface + 实现
├── wal.go              # WAL writer
├── uploader.go         # WAL reader + batch poster
├── rpcparser.go        # JSON-RPC frame ↔ request/response pairing
├── walpb/
│   └── wal.proto
└── recorder_test.go
```

`Recorder` 接口：

```go
type Recorder interface {
    SessionOpen(SessionMeta) (sessionID string)
    SessionClose(sessionID, reason string, counters Counters)
    OnFrameToBackend(sessionID string, frame *relaypb.RelayMessageFrame, rawBytes []byte)
    OnFrameToClient(sessionID string, frame *relaypb.RelayMessageFrame, rawBytes []byte)
    CallStart(meta CallStartMeta) (callID string)
    CallEnd(callID string, meta CallEndMeta)
    Close(ctx context.Context) error  // flush + stop uploader
}

type noopRecorder struct{} // 用于 audit disabled 时
```

集成位置：

- `bridge.go runBridgePump`：在 `inbound.write(frame)` 调用之前插 `recorder.OnFrameToBackend(...)`，错误**不阻塞** forward（在 recorder 内做有界 channel，channel 满了走 overflow 策略而不是阻塞 pump）。
- `inbound.go runInboundReader`：同上，在 `bridgeSession.write(frame)` 之前插 `recorder.OnFrameToClient(...)`.
- `handleBridge` 完成 Resume 握手后插 `recorder.SessionOpen(...)`；defer 内插 `recorder.SessionClose(...)`.
- SDK REST handlers：调用前后插 `recorder.CallStart/CallEnd`.
- Relay handler：PUT/GET 入口插 `CallStart`，出口插 `CallEnd`.

RPC 配对（`rpcparser.go`）：

- 解析 RelayData.payload 为 JSON-RPC 消息。
- 维护 per-session 的 `pendingRequests map[rpcID]CallStartMeta`。
- 看到 request frame：emit `CallStart` 并把 (rpcID, callID) 入 map。
- 看到 response frame：从 map 取出 callID，emit `CallEnd`。
- 30s 内未配对的 request 标记 `is_error=true, error_summary="response timeout"`，emit `CallEnd`。

## Read API

新增 routes（替换被删的 `/api/workspaces/{id}/operations`）：

```
GET  /api/workspaces/{id}/exec-audit/sessions
       ?exe_id=&user_id=&turn_id=&since=&until=&limit=
       → 200 { sessions: [<SessionSummary>], next_cursor }

GET  /api/workspaces/{id}/exec-audit/sessions/{session_id}
       → 200 { session: <SessionDetail>, first_calls: [...] }

GET  /api/workspaces/{id}/exec-audit/calls
       ?source=envmcp|rest|relay&exe_id=&user_id=&method=&is_error=&since=&until=&limit=
       → 200 { calls: [<CallSummary>], next_cursor }

GET  /api/workspaces/{id}/exec-audit/calls/{call_id}
       → 200 { call: <CallDetail>, request_preview, response_preview }
         preview = utf8 decode of first 8 KiB; >8KiB → "(truncated)"

GET  /api/workspaces/{id}/exec-audit/calls/{call_id}/payload?side=request|response
       → 200 application/octet-stream <decompressed bytes>
       (>4MiB never available — 404 with reason="too_large")
```

所有 endpoint 走现有 `requireWorkspaceMember` 中间件，workspace_id 从 URL 强制覆盖 query 参数（同 operations 端点旧做法）。

Internal 镜像端点（X-Internal-Secret）用于跨 workspace ops：`/internal/exec-audit/{sessions,calls,calls/{id}}`。

## Configuration

### codex-exec-gateway 新增 env

```
CXG_AUDIT_ENABLED              bool, default true
CXG_AUDIT_WAL_DIR              string, default "/var/cxg-audit"
CXG_AUDIT_WAL_FSYNC_INTERVAL   duration, default 100ms
CXG_AUDIT_WAL_FSYNC_RECORDS    int, default 256
CXG_AUDIT_WAL_FILE_MAX_BYTES   int, default 1073741824 (1 GiB)
CXG_AUDIT_WAL_DISK_QUOTA_BYTES int, default 10737418240 (10 GiB)
CXG_AUDIT_WAL_OVERFLOW         enum, default "fail" ("fail"|"drop")
CXG_AUDIT_PAYLOAD_MAX_BYTES    int, default 4194304 (4 MiB)
CXG_AUDIT_UPLOAD_URL           string, default "http://agentserver.svc/internal/exec-audit/batch"
CXG_AUDIT_UPLOAD_SECRET        string, agentserver X-Internal-Secret
CXG_AUDIT_UPLOAD_BATCH_BYTES   int, default 1048576 (1 MiB)
CXG_AUDIT_UPLOAD_BATCH_RECORDS int, default 200
CXG_AUDIT_UPLOAD_FLUSH_INTERVAL duration, default 1s
CXG_AUDIT_RPC_PAIR_TIMEOUT     duration, default 30s
CXG_AUDIT_PVC_REQUIRED         bool, default true (dev override only)
```

### Helm values

```yaml
execAudit:
  enabled: true
  payloadMaxBytes: 4194304
  walOverflow: fail
  pvc:
    enabled: true
    storageClass: ""
    size: 20Gi
  retention:
    sessionDays: 90
    callDays: 90
    payloadDays: 90   # 受 ref_count 保护
```

`deploy/helm/agentserver/templates/codex-exec-gateway.yaml`：
- 新增 PVC（同 storageClass 模式作 namespace-scoped），挂到 `/var/cxg-audit`。
- 注入上述 env。
- ServiceAccount 不需要新增权限（只是 outbound HTTP）。

### agentserver retention

新文件 `internal/server/exec_audit_retention.go`，参考已删除的 `operations_retention.go` 形状：

```
每小时一次：
  DELETE FROM exec_audit_calls WHERE started_at < now() - INTERVAL '90 days';
  DELETE FROM exec_audit_sessions WHERE opened_at < now() - INTERVAL '90 days';
  -- 然后清理 orphan payloads:
  DELETE FROM exec_audit_payloads
    WHERE id NOT IN (
      SELECT request_payload_id FROM exec_audit_calls WHERE request_payload_id IS NOT NULL
      UNION
      SELECT response_payload_id FROM exec_audit_calls WHERE response_payload_id IS NOT NULL
    )
    AND created_at < now() - INTERVAL '1 day';   -- 给 in-flight 上传留 1 天 grace
```

env：`AGENTSERVER_EXEC_AUDIT_RETENTION_DAYS` 默认 90。

## Frontend

`web/src/components/OperationsPanel.tsx` 删除。新建 `web/src/components/ExecAuditPanel.tsx`，挂在原 OperationsPanel 的位置（WorkspaceDetail.tsx）。功能：

- Tab：Sessions / Calls
- Calls 表格列：time / source / exe_id / user / method / status / duration
- 点击行展开：request preview + response preview + 下载完整 payload 按钮（命中大小限制时灰）
- 筛选：source / user / exe_id / method / is_error / 时间范围

API client（`web/src/lib/api.ts`）新增 fetcher，替换被删的 operations 函数。

## Performance & cost budget

| 维度 | 估算 |
|---|---|
| 单条 RelayData 帧 payload P50 | 2-8 KiB（JSON-RPC tool call/response） |
| P99 | 200 KiB（read_file 大文件、shell 长 stdout） |
| 帧速率（peak per gateway pod） | ~200 frames/sec across all sessions |
| WAL 写带宽（峰值） | ~10 MiB/s before zstd, ~3 MiB/s after |
| Postgres 写带宽 | 同 ~3 MiB/s 压缩后 |
| 单 workspace 单天数据 | 50 turns × 30 frames × 8 KiB ≈ 12 MiB raw, ~4 MiB on disk |
| 100 workspace × 90 天 | ~36 GiB on Postgres |

如果 Postgres 体积成为瓶颈，下一步是把 `exec_audit_payloads.compressed` 迁出到 S3 / MinIO，表里只留 URI；schema 变化是局部的（拆 column），不影响 audit_calls / audit_sessions 主表。

## Security & redaction

v1 不做 redaction。理由：

1. Payload 是用户租户内的工具调用 I/O，本来就只对该 workspace 成员可见（`requireWorkspaceMember`）；同租户内审计权限和原始数据访问权限相当。
2. 静态 redaction（regex 匹配 `sk-` `AKIA` 之类的 prefix）误杀率高、漏杀率也高，做半吊子比不做更危险。
3. 跨租户隔离靠 workspace_id 边界严格执行（API 层 + DB 层都加 workspace_id 过滤）。

v2 可以考虑：在 read API 出口做基于策略的 mask（例如非 workspace owner 看 payload 时自动 mask credential-like substrings）。

cap token 本身（HMAC、bcrypt ticket）**不**进审计 — 它们只在 `slog` 控制流日志里出现，不进 WAL。

## Rollout plan

阶段 0：删除旧 oplog/operations 系统
- 一个 PR：所有 deletion 清单内的文件 / 路由 / 配置 / 前端组件，加上 migration `DROP TABLE operations`。CI 跑通即合。

阶段 1：新表 + agentserver 接收端 + read API
- migrations `exec_audit_*` 三表
- `/internal/exec-audit/batch` ingester（接收 protobuf batch，做 upsert）
- `/api/workspaces/{id}/exec-audit/*` 查询 API
- retention job
- 没有写入方 → 表是空的，对生产无影响

阶段 2：codex-exec-gateway audit 包 + WAL + uploader
- `internal/codexexecgateway/audit/` 新包
- Recorder 接口 + noopRecorder
- WAL writer + protobuf schema
- Uploader goroutine + cursor 持久化
- Helm PVC + env wiring
- 默认 `CXG_AUDIT_ENABLED=false` 出闸；一个 workspace 灰度开启验证

阶段 3：CapPayload 加 user_id
- `codex-app-gateway` 签发处带上 user_id
- `codex-exec-gateway` 验证侧解析（旧 token 仍兼容）
- 一个 release cycle 之后，envmcp 路径的 audit 行才会有 user_id；之前的 NULL 是预期的

阶段 4：Frontend
- 新 ExecAuditPanel + API client
- 删除 OperationsPanel 引用

阶段 5：默认开启 audit
- `CXG_AUDIT_ENABLED=true` 在生产 values 里固化
- 监控 WAL 磁盘占用、上传延迟、失败计数；建告警

## Observability

新增 Prometheus metrics（gateway pod 暴露）：

```
cxg_audit_records_written_total{type="session_open|session_close|call_start|call_end|frame"}
cxg_audit_records_uploaded_total
cxg_audit_records_dropped_total{reason="wal_full|payload_too_large|recorder_chan_full"}
cxg_audit_wal_bytes
cxg_audit_wal_files
cxg_audit_upload_lag_seconds  (now - oldest unuploaded record ts)
cxg_audit_upload_errors_total{kind="network|5xx|4xx"}
cxg_audit_rpc_pair_pending  (gauge)
cxg_audit_rpc_pair_timeout_total
```

agentserver 侧：

```
agentserver_exec_audit_ingest_records_total{type=...}
agentserver_exec_audit_ingest_errors_total{kind=...}
agentserver_exec_audit_retention_deleted_total{table=...}
```

告警建议：

- `cxg_audit_upload_lag_seconds > 300` for 5min → ops alert
- `cxg_audit_records_dropped_total` rate > 0 → ops alert
- `cxg_audit_wal_bytes > 0.8 * disk_quota` → warn
- `cxg_audit_wal_overflow="fail"` 触发拒绝 bridge → page

## Open questions

1. **JSON-RPC 配对在 envmcp 路径下到底能配多少**？需要先抓一段实际 RelayData payload 来验证 — 如果实际帧大多是非 JSON-RPC 的 codex 内部协议（`thread/*`、`turn/*` 之类），那么"按 RPC 配对"作为主索引方式就站不住，得退回到 frame-level 表。**抓样验证**应在阶段 2 第一周做完。
2. **PVC 还是 emptyDir**？生产 PVC 必要，但成本 / 配额哪里出？需要 ops 拉一个 storageClass。dev / preview 环境用 emptyDir 即可。
3. **cap token 加 user_id 会不会触发 codex-app-gateway 那边的密钥轮换**？不会 — 字段添加是 in-place 的，HMAC 签名算法不变。但需要 codex-app-gateway 升级先于 codex-exec-gateway 切换强制要求 user_id；本 spec 不要求强制（NULL 容忍），所以可以并行升级。
4. **是否要写一个 admin endpoint 把指定 session 的所有 payload 打包成 tar 下载**？v2 feature，先不做。
5. **审计本身的 audit log**？谁查了什么 — 暂不记录。一般不做"meta-audit"。

## Self-review

- 用户问题"指示来自哪个用户、来自 envmcp 还是 rest、发到哪个 codex-exec、响应是什么、起止时间"对应字段：`user_id` / `source` / `exe_id` / `response_payload_id` + `response_sha256` / `started_at` + `completed_at` + `duration_ms`。覆盖。
- "完整记录"对应 WAL + 4 MiB payload cap + fail-closed 溢出策略。覆盖（除极端 OOMKill 边角案例，已声明）。
- "把之前的删掉"对应阶段 0 + deletion 清单。覆盖。
- relay 路径的大文件传输不会撑爆表（>4 MiB 只记 sha256）。覆盖。
- 跨租户隔离：workspace_id 在 URL 路径强制覆盖 + retention 不跨 workspace。覆盖。
