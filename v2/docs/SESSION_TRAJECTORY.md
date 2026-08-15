# Session Trajectory 设计方案

> 状态：MVP 已在代码中实现（2026-08-15，尚未生产发布），后续 read model / OpenTelemetry 演进见本文 roadmap
>
> 目标：为 Browser 中的每个 session 提供可持久化、可分页、可实时更新的运行轨迹，并让一次失败能够直接定位到 model、credential、managed sandbox、execution、operation 或 checkpoint 阶段。
>
> 参考：`../deepseek-harness/packages/client/ui-trajectory` 及其 session projection/query/telemetry 设计。本文借鉴其 lifecycle projection、稳定行身份、尾部加载、虚拟化和 timeline 交互，但不复制其实现。

## 1. 背景与问题

当前排查一条失败消息通常需要在 Browser、Core、harness-pool、harness-worker、executor-gateway、sandbox-gateway 和 TAE 多处日志之间人工拼接。虽然 Core 已经有 `runs`、`run_attempts`、`run_events`、`executions`、`execution_operations`、`managed_sandboxes` 等权威状态，但目前存在几个明显缺口：

1. Browser transcript 只投影 user/assistant message，不展示 attempt、模型请求、工具调用、managed sandbox 和 operation 生命周期。
2. 公共 event API 以单个 run 为单位向前读取，不适合一个 session 内多个 run 的尾部加载和向上分页。
3. 一些终态错误只留下 category 和 SHA-256，缺少可阅读的安全错误消息、发生阶段和组件，导致 hash 能聚类但不能诊断。
4. managed sandbox 表主要保存最新状态，不能完整说明 `ensure -> create -> readiness -> dispatch -> stream` 的历史。
5. 组件之间尚无完整 trace context，日志里的 ID 也没有统一约定。

Trajectory 的目标是让用户或获得诊断权限的运维人员在一个页面中回答：

- 当前 run 卡在哪个阶段，已经持续多久；
- 模型是否过载、是否发生重试、TTFT 和 token usage 如何；
- harness 选择了哪个工具，参数和返回值是什么；
- 使用了哪一种 workspace credential、是否按 `process_env` 注入；
- session 专属 TAE sandbox 是否 ready，执行落在哪个 sandbox/session；
- operation 是未 dispatch、已 acknowledged、命令非零退出，还是结果不明；
- 最后失败发生在工具执行、模型继续生成还是 checkpoint/finalization。

### 1.1 非目标

- Trajectory 不替代 Loki、Tempo、Prometheus，也不是一个任意日志查看器。
- MVP 不提供重试、取消以外的运维写操作；面板首先是只读的事实投影。
- 不把 access token、refresh token、JWT、AK/SK、Authorization header、capability token 或原始 process env 放入事件、trace、日志或 API。
- 不用 trace 作为产品事实源。trace 可以采样、丢失和过期，不能决定一个 run 是否成功。
- 不通过扫描 Chat DOM、A2UI surface 或 transcript 猜测工具层级和耗时。

## 2. 从 DeepSeek Trajectory 借鉴什么

DeepSeek 的实现有四个值得保留的核心约束：

1. **Trajectory 是 session event 的独立 projection，不是第二套日志。** 各业务域通过 Definition 把 started/delta/completed 事件归并成一个 lifecycle record。
2. **稳定身份优先。** Snapshot Builder 以稳定 key 做 replace/apply；streaming 更新不会改变行 key、选择状态或虚拟列表测量结果。
3. **长会话从尾部进入。** 首次只读最近窗口，向上触顶后用 cursor 加载历史；prepend 后保持 scroll anchor；只有用户仍在尾部时才跟随实时数据。
4. **时间不能被伪造。** 未完成记录不展示虚构的 duration；未加载的历史不在 timeline 中假装存在；TTFT 与 decoding 分开显示。

AgentServer 需要作一处有意的调整：DeepSeek 可以在共享 client session event window 上做纯前端 projection；AgentServer 的 canonical ledger 按 run 存储，并且 session 内容受 creator-only 授权、工具输出和凭据元数据还需要服务端脱敏。因此这里采用 **Core 侧 Definition + 可重建 read model**，Browser 不接触未审查的 canonical payload。

## 3. 总体架构

### 3.1 当前已落地的 vertical slice

今晚落地的是不增加 migration 和常驻 projector 的完整 vertical slice：Core 在每次查询时开启一个 repeatable-read 事务，从现有 canonical 表读取一个有界 session window，然后在服务端完成 closed-world projection 和脱敏。这样可以先让生产故障可见，同时避免为首版引入双写、projection lag 和额外部署组件。

```text
runs / run_attempts / run_events / executions / operations
managed_sandboxes / activities / credential audit / checkpoints
                              │
                              │ bounded repeatable-read（最多 32 runs）
                              ▼
                  Core safe trajectory projection
                              │
                     HMAC backward cursor
                              │
                              ▼
                    browser-gateway（GET only）
                              │
                 strict Web contract validation
                              │
                              ▼
              Conversation | Trajectory（active run polling）
```

关键性质：

- Core 每次查询都重新校验 workspace membership、session creator 和 actor/run scope；cursor 不是授权凭据。
- SQL 不读取 sealed credential、token 或 process environment value。工具 input/output 和错误再经过统一安全诊断 redactor 与字节上限。
- 数据源查询有硬上限；任一来源超限时返回 `truncated=true`，UI 明确提示，而不是静默漏行。
- Core 和 Browser Gateway 都只接受 `before`、`limit` 各一个值；空 cursor、未知/重复参数、越界 limit 均 fail closed。
- active run 期间 Browser 每 1.2 秒刷新尾页；已加载历史按稳定 record ID 合并，用户离开尾部后不会被自动抢回。

### 3.2 目标架构

Trajectory 分为两个严格分离的层：

1. **Product Trajectory**
   - 来源是 Core 的 canonical domain event 和权威状态。
   - 不采样，随 session 生命周期保留，可重放、可重建。
   - session creator 可见，展示安全的输入/输出预览、状态、耗时和错误。
2. **Operator Diagnostics**
   - 来源是 OpenTelemetry trace 和结构化日志。
   - 有独立权限和较短 retention，允许查看脱敏后的组件错误、provider request ID、Pod 和 trace 链接。
   - 即使 Tempo/Loki 不可用，Product Trajectory 仍必须正确工作。

```text
Core / harness / llmproxy / execution gateway / sandbox gateway
          │
          │ canonical safe events（按 run 严格排序）
          ▼
      Core run_events ────────► coalesced projection queue
          │                              │
          │ source of truth              ▼
          │                    embedded trajectory projector
          │                              │ Definition.apply
          │                              ▼
          │                    session_trajectory_records
          │                              │
          │                 Core session trajectory API
          │                              │
          │                       browser-gateway
          │                              │
          │                              ▼
          │                    Conversation | Trajectory
          │
          ├── OTLP spans ─► central Collector ─► Tempo
          └── JSON logs  ─► Alloy             ─► Loki
```

当查询量或 session 历史规模证明 on-read projection 不再满足延迟目标时，再把相同 API 后端替换为 Core 内嵌的增量 read model。目标 projector 通过 coalesced queue 每个 run 只保留一个待处理目标 seq，多副本使用 lease/`SKIP LOCKED` 竞争。该演进不改变 Browser contract；projection 失败不得阻塞 run event 提交或用户执行，并必须暴露 lag/error 指标。

## 4. 统一关联标识

所有 canonical event、结构化日志和 span 使用同一组名称：

```text
workspace_id
session_id
run_id
run_attempt_id
run_attempt_generation
app_server_tool_call_id
execution_id
operation_id
sandbox_id
target_generation
provider_session_ref
trace_id
span_id
```

规则如下：

- 产品 API 使用 camelCase；数据库、日志和 OTel attributes 使用 snake_case。
- ID 在 Loki 中作为 structured metadata，不作为 indexed label，避免高基数索引。
- `provider_session_ref` 只进入受限诊断字段；普通用户优先看到 AgentServer `sandboxId`。
- trace ID 不是授权凭证。知道 trace ID 不能绕过 session 或 diagnostics 权限。

## 5. Canonical event 扩展

现有 assistant、tool、approval、execution 和 operation event 继续使用；缺失的阶段增加闭集 schema。每种 event 必须有版本化 payload decoder，未知 kind 可以被旧 projector 跳过，但不能把未经审查的原始 payload 直接返回给 Browser。

### 5.1 建议事件族

| 领域 | 事件 | 主要生产者 | Trajectory record |
|---|---|---|---|
| Run | `run.queued/claimed/running/finalizing/completed/failed/cancelled` | Core / harness-pool | Run |
| Attempt | `attempt.leased/starting/running/finalizing/succeeded/failed/fenced` | Core / harness-pool | Attempt |
| Runtime | `runtime.preparing/ready/failed` | harness-worker | Runtime |
| Model | `model.request.started/first_output/completed/failed` | harness-worker；后续由 llmproxy 补上 upstream 细节 | Model request |
| Model retry | `model.request.retrying` | 能确认 retry 关系的请求层 | 同一 Model request 的 retry 子行 |
| Assistant | 现有 `assistant.message.*`、`assistant.reasoning.*` | harness-worker | Assistant message / reasoning summary |
| Tool | 现有 `tool.call.*` | harness-worker | Tool call |
| Credential | `credential.resolve.started/succeeded/failed` | Core / execution gateway | Credential resolution |
| Execution | 现有 `execution.*` | Core / executor-gateway | Execution |
| Operation | 现有 `operation.*` | Core / executor-gateway | Operation |
| Sandbox | `sandbox.ensure.started/creating/ready/failed/released` | sandbox-gateway / Core | Sandbox ensure |
| Checkpoint | `checkpoint.uploading/committed/failed` | harness-pool / Core | Checkpoint |

`model.request.retrying` 只能在生产者具有明确 request group ID 时发出，不能按相邻时间猜测。MVP 若只能观察到多个 llmproxy HTTP 请求，则把它们显示为独立的 model attempt，不冒充一个已确认的 retry chain。

### 5.2 时间语义

- `run_events.created_at` 是 Core 接收并提交事件的 wall-clock 时间，用于稳定排序和大致位置。
- 同一组件内的精确耗时由 monotonic clock 计算后写入 `durationMs`，不能用两台机器的 wall clock 相减。
- model completion 可以携带 `ttftMs`、`decodeMs`；operation 可以携带 `queueMs`、`dispatchMs`、`runMs`。
- 记录仍在 running 时，`completedAt` 和 `durationMs` 必须为空。UI 可以显示“running”，但不能每秒制造一个并不存在的权威 duration。

### 5.3 错误 envelope

所有 failed/unknown 终态使用统一的安全错误结构：

```json
{
  "code": "model_overloaded",
  "category": "model_overloaded",
  "message": "Selected model is at capacity. Please try a different model.",
  "component": "harness-worker",
  "phase": "model_request",
  "retryable": true,
  "fingerprint": "sha256:..."
}
```

约束：

- `code`、`category`、`message`、`component` 和 `phase` 对失败记录都是必填项。
- `message` 是经过 allow-list/secret redaction 的可读信息，最大 4 KiB；fingerprint 只用于聚类，绝不能成为唯一错误信息。
- 已知上游错误必须保留安全语义，例如 `model_overloaded`、`credential_unavailable`、`sandbox_not_ready`、`tae_transport`、`command_exit_nonzero`、`output_incomplete`、`checkpoint_failed`。
- 若原始错误可能含 secret，产品 message 使用明确但安全的描述，完整的脱敏 cause chain 进入 operator log，并通过 trace ID 关联。

这意味着当前类似“stock app-server confirmed that the turn failed; category=unclassified; sha256=...”的情况，在 Trajectory 中至少应显示失败阶段、组件、分类和安全的上游 message，而不是只显示两个 hash。

## 6. Trajectory projection

### 6.1 当前 on-read projector

当前 reducer 位于 `internal/coreserver/user_session_trajectory.go`，以稳定领域 ID 聚合事件，并与权威状态表补充的 lifecycle record 合并：

- assistant/reasoning 的 started/delta/completed 合成同一行；
- tool arguments/progress/completion/result 合成同一行，并分别限制 input/output preview；
- attempt、execution、operation、managed sandbox activity、credential use audit 和 checkpoint 从现有权威表投影；
- run 已终止但 assistant/tool 缺少 completion 时，显式生成 `output_incomplete`；
- `process_env` credential record 明确输出 `webhookUsed=false`、`egressAuthorizerUsed=false`，且永不返回 env value；
- 记录按 `(run_created_at, run_id, anchor_seq, rank, record_id)` 稳定排序，分页 cursor 使用同一位置定义。

这套 projection 是由 canonical 数据重建的安全视图。它不写回 canonical 表，也不影响 run 执行路径。

### 6.2 未来 Definition 接口

若后续切换为增量 read model，将把当前 reducer 拆到独立的 `internal/trajectory` 包，并让每个业务域注册 Definition。概念接口如下：

```go
type Definition interface {
    Match(runevent.Event) bool
    RecordKey(runevent.Event) (string, error)
    ParentKey(runevent.Event) (string, error)
    Apply(current *Record, event runevent.Event) (Record, error)
}
```

Definition 必须满足：

- `RecordKey` 只由稳定领域 ID 构造，不能使用数组下标、显示文本或时间戳。
- `Apply` 是确定性的；同一个 `(run_id, seq)` 重放两次结果不变。
- started/delta/completed 更新同一 record，不新增闪烁行。
- 子行的 `parentId` 由领域关系产生，前端不得通过相邻行猜测。
- schema 不认识的字段 fail closed；新 event kind 需要显式 Definition 和泄密测试。

建议 key：

```text
run:<run_id>
attempt:<run_attempt_id>:<generation>
model:<run_attempt_id>:<request_id>
assistant:<run_id>:<message_id>
tool:<run_id>:<app_server_tool_call_id>
credential:<run_attempt_id>:<resolution_id>
execution:<execution_id>
operation:<operation_id>
sandbox:<sandbox_id>:ensure:<run_attempt_id>
checkpoint:<checkpoint_id>
```

### 6.3 未来 Read model

MVP **没有**新增下列表或 migration。未来若因规模引入 read model，`run_events` 仍是唯一 canonical ledger；新增表只是可删除、可重建的索引，不允许产生 canonical event 中不存在的新事实。

建议 migration 增加：

```sql
ALTER TABLE sessions
    ADD COLUMN trajectory_version bigint NOT NULL DEFAULT 0;

CREATE TABLE session_trajectory_records (
    workspace_id uuid NOT NULL,
    session_id uuid NOT NULL,
    record_id text NOT NULL,
    parent_record_id text,
    run_id uuid NOT NULL,
    run_attempt_id uuid,
    run_attempt_generation bigint,
    execution_id uuid,
    operation_id uuid,
    sandbox_id uuid,
    kind text NOT NULL,
    subtype text NOT NULL,
    status text NOT NULL,
    run_created_at timestamptz NOT NULL,
    anchor_event_seq bigint NOT NULL,
    rank integer NOT NULL,
    revision bigint NOT NULL,
    first_projection_version bigint NOT NULL,
    last_event_seq bigint NOT NULL,
    started_at timestamptz,
    first_output_at timestamptz,
    completed_at timestamptz,
    duration_ms bigint,
    safe_summary text NOT NULL,
    safe_details jsonb NOT NULL,
    trace_id bytea,
    span_id bytea,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (workspace_id, session_id, record_id),
    FOREIGN KEY (session_id, workspace_id)
        REFERENCES sessions(id, workspace_id) ON DELETE CASCADE
);

CREATE INDEX session_trajectory_records_page_idx
    ON session_trajectory_records (
        workspace_id, session_id,
        run_created_at DESC, run_id DESC,
        anchor_event_seq DESC, rank DESC, record_id DESC
    );
```

正式 migration 还需补齐所有 bounded/enum/generation/time/hash CHECK，风格与现有 Core schema 一致。`safe_details` 在写入前必须通过 Go 侧 closed-world validator；不能因为它是 JSONB 就允许生产者塞任意键。

另外增加：

- `trajectory_projection_offsets`：每个 run 的 `applied_seq`、projection schema version、最近错误和更新时间。
- `trajectory_projection_queue`：每个 run 一行的 coalesced `target_seq`、lease owner 和 lease expiry；事件事务只做 upsert，不为每个 event 再复制一份 queue row。

projector 按 run 内 seq 严格递增应用，upsert 条件为 `last_event_seq < incoming.seq`。一批 record 更新与 session `trajectory_version + 1` 在同一事务提交。projection schema 升级时可以清空指定 version 的 read model，再从 `run_events` 重放。

### 6.4 历史数据

- 已有 assistant/tool/run event 可以完整回放。
- execution/operation 以现有事件为主，权威表只用于补充静态元数据，不能用当前状态伪造过去的 transition 时间。
- 旧 managed sandbox 只有当前快照时，显示一个 `timingIncomplete=true` 的 Sandbox record，并明确标注历史阶段不可用。
- backfill 与实时 projector 使用同一 Definition；不能维护两套映射逻辑。

## 7. API 设计

### 7.1 尾部读取和向上分页

Browser/Core 已提供同形只读接口：

```http
GET /v2/workspaces/{workspaceId}/sessions/{sessionId}/trajectory
    ?before=<opaque-cursor>
    &limit=200
```

默认 `limit=100`，最大 200。无 `before` 时返回 session 尾部；返回记录按稳定时间顺序排列，便于直接 prepend/append。

```json
{
  "schemaVersion": 1,
  "workspaceId": "...",
  "sessionId": "...",
  "activeRunId": "...",
  "records": [],
  "nextBefore": "v1....",
  "hasMore": true,
  "truncated": false,
  "readAt": "2026-08-15T20:00:00Z"
}
```

`nextBefore` 仅在 `hasMore=true` 时存在。`truncated=true` 表示某个 canonical source 触及服务端硬上限，需要把当前页视为不完整诊断，而不能误认为不存在其他记录。

cursor 使用 HMAC 签名并绑定：

```text
workspace_id + session_id + actor_id + order_tuple
```

order tuple 为 `(run_created_at, run_id, anchor_event_seq, rank, record_id)`，不使用 SQL offset。token 使用 canonical base64url 编码，拒绝篡改、跨 workspace/session/actor 复用、空值、控制字符和超长 payload。即使 cursor 有效，Core 仍在同一个读取事务内重新检查 creator/membership。

### 7.2 Record contract

```json
{
  "id": "operation:...",
  "parentId": "execution:...",
  "kind": "operation",
  "status": "failed",
  "title": "lark-cli process",
  "summary": "Command exited with status 1",
  "runId": "...",
  "runAttemptId": "...",
  "runAttemptGeneration": 1,
  "executionId": "...",
  "operationId": "...",
  "sandboxId": "...",
  "targetGeneration": 2,
  "startedAt": "...",
  "completedAt": "...",
  "durationMillis": 1834,
  "input": "[\"lark-cli\",\"skills\",\"read\",...]",
  "output": "...",
  "inputTruncated": false,
  "outputTruncated": true,
  "details": [{"name": "backend", "value": "managed"}],
  "failure": {
    "code": "command_exit_nonzero",
    "category": "command_exit_nonzero",
    "message": "lark-cli exited with status 1",
    "component": "sandbox-gateway",
    "phase": "process_wait",
    "retryable": false,
    "fingerprint": "sha256:..."
  }
}
```

字段规则：

- 所有类型 closed-world 解码，Browser client 拒绝 unknown/missing field。
- 单条 input/output 各限制为 16 KiB；单页 record 内容总量限制为 1 MiB，HTTP response 另有 2 MiB 硬上限。
- ID/cursor 拒绝 NUL、CR/LF 和超长 UTF-8；input/output 允许普通换行但拒绝 NUL。
- credential record 只显示 provider kind、binding display name、credential version、`injectionMode=process_env`，并明确 `webhookUsed=false`、`egressAuthorizerUsed=false`；不显示 secret/env value。
- reasoning 只显示协议明确标为 user-visible 的 reasoning summary，不保存或展示 provider 隐藏 chain-of-thought。

### 7.3 实时更新

MVP 不新增第二条 SSE durability 路径。Trajectory tab 在 session 有 active run，或本地 conversation 仍处于 connecting/running/cancelling 时，每 1.2 秒重新读取尾部；相同 record ID 原位替换，新 record 追加，已向前加载的历史保留。run settled 后停止轮询。

未来如果查询压力要求 push，可在不改变 record contract 的前提下增加 `trajectory.upsert/reset` SSE。SSE 仍只作为低延迟通知，canonical state 和 GET snapshot 始终承担恢复。

## 8. Browser UI

Trajectory 属于对话 session，不属于 Platform credentials 页面。在当前 session header 增加：

```text
[ Conversation ] [ Trajectory ]
```

MVP 页面结构：

```text
┌───────────────────────────────────────────────────────────────┐
│ Session title     Conversation | Trajectory      Run: Failed  │
├───────────────────────────────────────────────────────────────┤
│ Overview timeline: model ▬▬ tool ▬ sandbox ▬ process ▬ model  │
├──────────────────────────────────────┬────────────────────────┤
│ Ledger                               │ Inspector              │
│ Run #12                              │ Overview / Input       │
│  ├─ Attempt #1             failed    │ Output / Timing        │
│  ├─ Model request #1       failed    │ Usage / Options        │
│  └─ error: model overloaded          │ Diagnostics            │
│ Run #13                              │                        │
│  ├─ Tool lark-doc                    │                        │
│  ├─ Credential lark/process_env      │                        │
│  ├─ TAE sandbox ready                │                        │
│  └─ lark-cli process       running   │                        │
└──────────────────────────────────────┴────────────────────────┘
```

### 8.1 Ledger

- 行类型：Run、Attempt、Model、Assistant、Reasoning、Tool、Credential、Execution、Operation、Sandbox、Approval、Checkpoint 和已审查的 generic lifecycle event。
- stable `parentId` 优先决定层级；父记录不在当前窗口时使用固定 kind depth 展示，不按相邻行猜测关系。
- 状态同时显示文字、图标和颜色；`failed`/`unknown` 都会进入高注意力样式，文字保留两者语义差异。
- 点击行打开右侧 Inspector；窄屏改为上下分栏。

### 8.2 Inspector

当前 Inspector 提供：

- Overview：状态、开始/完成时间、权威 duration 和相关 run/attempt/execution/operation/sandbox ID。
- Failure：code、category、可读安全 message、component、phase、retryable 和 fingerprint。
- Input / Output：16 KiB 安全预览和截断标记。
- Details 与稳定 record identity。

usage、细分 TTFT、object fetch 和 Tempo 下钻保留为后续扩展；当前 contract 不返回这些字段。

### 8.3 Timeline 与长会话

- 首次加载最近 100 条，点击 “Load earlier” 使用 HMAC cursor 向前加载 100 条并按 ID 去重 prepend。
- 只有用户距离尾部小于 48 px 时自动 follow；用户向上滚动后，轮询不会抢走当前位置。
- 顶部 overview 以 run 为单位展示 duration 比例和 active 状态；running bar 使用本次 snapshot 的 `readAt` 计算观察到的 elapsed，不写回权威 duration。
- Inspector 对 running record 显示 “in progress”，不伪造 `completedAt` / `durationMillis`。
- 虚拟化、筛选、折叠、TTFT/decode 分段和新记录计数属于大规模会话的后续增强。

## 9. OpenTelemetry 与结构化日志

本节是后续 operator diagnostics 设计，不是 MVP 上线依赖。今晚已落地统一 `safediagnostic` redaction 和可读 durable failure；尚未新增 Tempo/Collector，也没有把 trace ID 暴露给普通 Browser 用户。

### 9.1 Span 层级

```text
session.run
└─ run.attempt
   ├─ runtime.prepare
   ├─ model.request
   │  ├─ model.upstream_attempt
   │  └─ model.stream
   ├─ tool.call
   │  ├─ credential.resolve
   │  └─ execution
   │     └─ operation
   │        ├─ sandbox.ensure
   │        ├─ tae.dispatch
   │        └─ tae.stream
   └─ checkpoint.commit
```

传播规则：

- HTTP 使用 W3C `traceparent`/`tracestate`，Go server/client middleware 负责 extract/inject。
- run outbox、attempt lease 和 execution dispatch 这类异步边界保存非敏感 trace carrier，新的 consumer span 使用 parent 或 span link。
- executor-gateway 到 sandbox-gateway、sandbox-gateway 到 TAE 都继续传递 trace context。
- agentx WSS/TAE operation protocol 在版本化 envelope 中增加 trace carrier；旧端没有 carrier 时创建 span link，不伪造 parent。
- stock app-server stdio 若不能传 trace context，worker 先以 attempt correlation 建 span link。只有 conformance test 证明 provider header 可安全配置后，才向 llmproxy 传静态 per-attempt `traceparent`。

span attributes 只放 ID、状态、region、model alias、tool name、字节数、耗时和错误分类；prompt、tool output、Authorization、process env 永远不进入 attribute/event。

### 9.2 日志规范

所有组件输出统一 JSON：

```json
{
  "level": "error",
  "message": "TAE session search request failed",
  "component": "sandbox-gateway",
  "failure_category": "tae_transport",
  "error_code": "network_unreachable",
  "error_type": "*url.Error",
  "error_message": "Post ...: connect: network is unreachable",
  "error_causes": ["..."],
  "error_fingerprint": "...",
  "workspace_id": "...",
  "session_id": "...",
  "run_id": "...",
  "execution_id": "...",
  "operation_id": "...",
  "trace_id": "...",
  "span_id": "..."
}
```

`error_message` 和 cause chain 先经过集中 redactor，再做 16 KiB 上限；hash 保留用于聚类，但日志也不能只写 hash。Loki label 仅使用 cluster、namespace、component、level、failure_category 等低基数字段。

### 9.3 SG 观测组件

`../k8s-byted` 当前已有 Grafana、Loki、Prometheus、Pyroscope 和日志用 Alloy，但没有 trace backend。实施 OTel 阶段建议新增：

- `apps/tempo.ts`：Tempo，使用 TOS/S3 object storage，初始建议 7 天 retention；
- `apps/otel-collector.ts`：中心 OTLP gateway Deployment，配置 memory limiter、batch、retry 和 tail sampling；
- Grafana Tempo datasource，以及 Loki derived field 到 trace ID 的关联；
- NetworkPolicy 仅允许 AgentServer namespace workload 向 OTLP receiver 写入。

不让现有每节点 Alloy DaemonSet 同时充当一个无稳定入口的 OTLP gateway。初期可 100% 收集以校验覆盖率；容量明确后，collector 对 error/unknown/慢 trace 100% 保留，对成功 trace 按比例采样。Product Trajectory 不受采样策略影响。

## 10. 授权、隐私与保留策略

### 10.1 产品授权

- Core action `sessions.trajectory` 复用并同时要求现有 `sessions:read + runs:read`，不扩大 Browser token 的 scope 集合。
- Core 继续强制 `session.creator_id == token subject`；不能因为 actor 是 workspace owner 就自动获得其他用户的 prompt/tool result。
- Browser Gateway 只接受 browser audience，不接受 Platform token。
- API 响应设置 `Cache-Control: private, no-store`，禁止跨 session 缓存。

### 10.2 Operator diagnostics

- `sessions.diagnostics:read` 是独立高权限，不包含在普通 workspace owner 角色中。
- 未来若支持 workspace 管理员协助排查，需要用户显式分享/support grant，并产生审计记录；不能暗中扩大现有 creator-only 边界。
- Grafana/Tempo/Loki 使用其自己的 OIDC/RBAC。产品 API 不代理任意 LogQL/TraceQL。

### 10.3 数据分类

| 分类 | 例子 | 存储位置 |
|---|---|---|
| Metadata | 状态、ID、耗时、tool name、region | Trajectory / trace / log |
| Session content | prompt、tool args/result、stdout | canonical object + Product Trajectory 安全预览 |
| Diagnostic | 脱敏 cause chain、provider request ID | Loki/Tempo 或短 TTL 加密对象 |
| Secret | token、JWT、AK/SK、header、env value | 仅 credential authority；其他位置禁止 |

Projection API 与 session 同 retention。trace 建议 7 天、日志按当前集群策略或 14 天；若需要保存超过预览上限的诊断 payload，使用单独加密 object、最多 7 天 TTL 和审计读取，不能塞进 Loki label 或 span attribute。

## 11. 失败分类与根因选择

先建立闭集 failure taxonomy：

```text
model_overloaded
model_authentication
model_transport
credential_unavailable
credential_expired
sandbox_not_ready
sandbox_provider_unavailable
tae_transport
executor_dispatch_unavailable
command_exit_nonzero
output_incomplete
approval_denied
checkpoint_failed
cancelled
unknown
```

Root cause 按因果关系而不是最后时间排序：

1. 具有明确 cause record ID 的终态沿 parent/cause 链回溯。
2. model/provider 明确失败优先于随后出现的 app-server cleanup/EOF。
3. operation 未 acknowledged 时是 clean failure；已 dispatch 但无法确认结果时是 `unknown`，不能降级成普通 failed。
4. checkpoint 失败发生在模型和工具成功之后，应独立显示为 finalization failure。
5. 无法分类时显示实际安全 message、component 和 phase，并同时产生 `unknown_failure_total` 告警，不能退化为 hash-only 文案。

## 12. 性能、可用性与容量

设计目标：

- MVP 不写 Trajectory 表或 queue，因此不在模型/工具关键写路径增加延迟，也不存在 projector lag。
- 单次 snapshot 最多读取 32 个 run，并对 event/execution/operation/sandbox/credential/checkpoint 分别设置硬上限；超限显式返回 `truncated=true`。
- 尾部最多 200 records，Browser Gateway response 硬上限 2 MiB；生产目标 p95 < 250 ms，超标后再评估增量 read model。
- Browser 首次只挂载 100 条；用户主动加载历史。支持 10,000+ record 和虚拟化是后续容量阶段目标。
- active run polling 中断不影响执行；恢复后重新读取 canonical snapshot。
- Tempo、Loki、Collector 故障不影响 run 和 Product Trajectory。

需要监控：

```text
trajectory_query_duration_seconds
trajectory_query_response_bytes
trajectory_query_truncated_total{source}
trajectory_projection_failures_total{kind}
run_failures_total{category,phase}
sandbox_ready_duration_seconds
operation_unknown_total{backend}
model_request_duration_seconds{status,model_alias}
```

## 13. 实施顺序

### Phase 0：立即可诊断的错误（已完成）

- 把 `serverOverloaded` / `Selected model is at capacity` 分类为 `model_overloaded`。
- model request 仅在尚未建立 response stream 前做有界 retry；一旦有部分 stream 就不重放。
- 终态事件和日志必须带安全 message，不再只带 SHA-256。

### Phase 1：可用的 session Trajectory vertical slice（已完成）

1. 增加 flat record/error contract、HMAC cursor、集中 redactor 和 on-read projector；首版无需 migration/backfill。
2. Core 增加 session 级 tail/backward query、repeatable-read snapshot 与 creator-only 授权。
3. browser-gateway 精确代理 GET，更新 public/web-edge OpenAPI 和生成 client。
4. Browser 增加 Conversation/Trajectory tab、run overview、ledger、Inspector、尾部加载和向上分页。
5. 覆盖 Run/Attempt/Model failure/Assistant/Reasoning/Tool/Approval/Execution/Operation/Sandbox/Credential/Checkpoint/terminal error。
6. active run 使用 1.2 秒 tail polling 和稳定 ID merge；不要求 Tempo 才上线。

### Phase 2：补齐高精度时序和大规模会话能力（下一阶段）

- 增加原生 Runtime、Credential resolution、Sandbox ensure/readiness、Checkpoint phase event，替代只能从当前权威状态推导的粗粒度 timing。
- 增加 model usage、TTFT、decode、明确 retry/upstream attempt。
- 加入 timeline、折叠、搜索、虚拟化和截断 object fetch。
- 按容量数据决定是否把 on-read reducer 无 API 变化地迁移到增量 read model。

### Phase 3：OTel/Tempo operator drill-down

- 贯通 Browser Gateway -> Core -> pool -> worker -> execution gateway -> sandbox gateway -> TAE 的 trace context。
- 在 `../k8s-byted` 增加 Tempo、中心 Collector 和 Grafana datasource。
- Trajectory record 保存 trace ID；有 diagnostics 权限的 Inspector 提供下钻。
- 建立 stuck run、sandbox readiness、operation unknown、model overload 告警。

### Phase 4：运营能力

- 跨 session 的错误/耗时搜索与 failure fingerprint 聚类。
- 版本间延迟回归比较。
- 导出脱敏诊断包；不导出 secret 或 raw environment。

## 14. 代码落点

当前落点：

```text
internal/trajectorycursor/                    # HMAC scope-bound cursor
internal/safediagnostic/                      # redaction / ANSI cleanup / UTF-8 bounds
internal/corecontract/session_trajectory.go   # public record contract
internal/coredb/state_user_trajectory.go      # repeatable-read bounded source
internal/coreserver/user_session_trajectory.go# server-side safe projection
internal/browsergateway/session_resources.go  # reviewed GET proxy
api/openapi/{public,web-edge}.yaml             # transport contract
web-shared/src/api.ts                          # strict response validator/client
a2ui-web/src/browser-app.tsx                   # tabs、polling、ledger、timeline、inspector
a2ui-web/src/browser.css                       # responsive presentation
docs/SESSION_TRAJECTORY.md
```

不要把 projection reducer 塞进现有 transcript reducer；Conversation 和 Trajectory 必须读取同一个 canonical source，但分别维护自己的 projection 和 UI state。

## 15. 测试与验收

### 15.1 自动化测试

- cursor 单测：round-trip、防篡改、跨 scope 使用和非法 position 拒绝。
- redactor 单测：ANSI、Bearer/JWT/token/secret-shaped value 脱敏和 UTF-8 安全截断。
- projector 单测：assistant/tool 聚合、稳定 parent/key、managed sandbox -> execution -> operation、`process_env` credential、model overload 和 `output_incomplete`。
- PostgreSQL integration：creator scope、membership、cursor-bound run window 和所有数据源 join。该测试要求 `AGENTSERVER_V2_TEST_DATABASE_URL`；未配置时明确 skip。
- API/Gateway contract：GET-only、unknown/duplicate/empty query fail closed、2 MiB response bound、scope-bound response。
- Web contract：closed-world schema、200-record/1-MiB 内容上限、timestamp/status/timing/pagination 一致性、ID/cursor 控制字符拒绝。
- Browser：tab、tail polling、history prepend/dedupe、follow-tail、run overview、failure/input/output Inspector 和 production build。

后续 read model/SSE 上线时，再补 lease takeover、rebuild 一致性、stream gap reset、虚拟化和 projector crash failure-injection 测试。

### 15.2 端到端主验收场景

以真实目标作为发布门槛：

> 用户在某个 session 发送“查询某飞书文档”；harness 选择 lark-cli；managed executor 在该 session 专属 TAE sandbox 内，以 workspace lark credential 的 `process_env` 模式执行。

Trajectory 必须依次显示：

```text
Run queued
Attempt leased
Runtime ready
Model request #1
Tool lark-doc selected
Credential lark resolved: process_env
  webhookUsed=false
  egressAuthorizerUsed=false
Execution created
Operation dispatching
TAE sandbox ready
lark-cli process acknowledged
stdout/result received
Operation succeeded
Tool result returned to model
Model response completed
Checkpoint committed
Run completed
```

同时验证：

- 同一 session 后续 run 复用其 active sandbox；另一个 session 使用不同 `sandboxId`。
- model capacity 错误显示 `model_overloaded` 和原始安全 message。
- sandbox readiness 超时明确停在 Sandbox record，未 dispatch 的 operation 不标成 unknown。
- TAE 已 dispatch 后 transport 中断标成 unknown，并显示最后 acknowledgement/output cursor。
- `unknown (output incomplete)` 能指出缺失的是 acknowledgement、terminal frame、stdout drain 还是 gateway finalization。
- Browser 刷新、Core/pool Pod 重启后仍能从 canonical ledger 恢复同一轨迹。
- 没有 diagnostics 权限的用户看不到 provider ref、Pod、内部 URL 或脱敏前错误；任何用户都看不到 token/env value。

## 16. 发布与回滚

MVP 没有数据库 migration、后台 projector或新基础设施组件。发布时应在同一版本中 rollout Core、Browser Gateway 和 Browser 静态 bundle；旧 Browser 不会调用新增接口，短暂版本交错期间新 Browser 若命中旧 Core 只会显示可重试的 Trajectory 加载错误，不影响 Conversation/run。

上线前必须在生产同版本 schema 的临时 PostgreSQL 上运行 integration test，并观察 Core trajectory query latency、`truncated` 比例、5xx 和 Browser Gateway response bound。OTel/Tempo 独立 rollout，不与 Product Trajectory 首次上线绑定。

回滚只需恢复旧的 Core/Browser Gateway/Browser 镜像；没有新表或异步状态需要清理。整个实现不改变 executor routing、credential injection mode、TAE sandbox 生命周期或 Pulumi 的资源 ownership。
