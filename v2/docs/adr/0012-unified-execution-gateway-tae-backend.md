# ADR 0012：统一 execution gateway，以 TAE backend 提供 managed executor

- 状态：Accepted
- 日期：2026-08-06
- 影响范围：Core、executor-gateway（逻辑角色改称 execution gateway）、harness-pool、harness-worker、sandbox-gateway、egress-authorizer、生产部署配置
- 取代：ADR 0011；ADR 0008 中“managed 复用 agentx”的传输结论；ADR 0009 中 managed sandbox 自建代理数据面的结论
- 保留：ADR 0009 的“真实凭据不得进入 sandbox”原则与 BYO 适用部分；ADR 0010 的 tool pack 决策
- 后续修订：[ADR 0013](0013-core-owned-workspace-credentials.md) 将本 ADR §7 的凭据配置、密封存储和 materialization 明确收归 v2 Core；不部署独立 credential proxy

## 背景

当前 v2 只实现 BYO executor：`harness-worker` 通过一个受限 MCP endpoint 调用
`executor-gateway`，后者完成目录冻结、run capability 校验、策略/审批、Core
execution/operation 状态迁移，再通过 agentx WSS 把 process/filesystem RPC 发到用户机器。

产品还需要 managed executor。目标运行时已经确定为 TAE Terminal Sandbox。TAE 自带 session
生命周期、SandboxD/Terminal API、进程流式输出、文件接口、网络策略与 Agent Gateway，因而不能继续把
它假装成一条 agentx 连接，也不应在 TAE 内再部署一份 agentx 或 sandboxd。

本决策以如下垂直场景约束架构，避免为了抽象而偏离真实产品目标：

1. session 启用 managed execution 与 `lark-readonly` tool pack；
2. harness 把现有 Lark skill 文本放进 `baseInstructions`；
3. 模型仍调用已有 `shell(argv=["lark-cli", ...])`，不要求把 Lark CLI 全量改造成 typed MCP tools；
4. worker 只连接统一 execution gateway；
5. gateway 把 shell operation 路由到 TAE，并把 stdout/stderr/exit 映射回原有 MCP 结果；
6. sandbox 只能持有 agentserver placeholder capability，真实 Lark token 永不进入 sandbox；
7. TAE Agent Gateway 在出站请求上调用 agentserver Policy Webhook，授权后才注入真实 token；
8. Core 保留完整 execution、operation、dispatch target generation 与 egress 审计历史。

## 约束

- `harness-worker` 只有一个工具入口；不能根据环境类型持有两套 capability、目录与重试语义。
- Core 现有 `PrepareExecution → PrepareOperation → BeginOperationDispatch → Acknowledge/Complete`
  是副作用的唯一权威状态机，managed 路径不得复制一份。
- `BeginOperationDispatch` 之后的超时或断线可能意味着请求已送达；不能用盲重试把一次操作变成两次。
- managed sandbox 要保留 session 级工作目录，不能为每次 `shell` 新建临时容器。
- TAE Go SDK 位于内部私有模块，不能让 provider 类型污染主模块的 Core/MCP 合约。
- 首个切片只支持 Linux/amd64、只读 Lark 请求与有界 shell/read-file 能力。

## 决策

### 1. 保留一个统一 execution gateway

`harness-worker` 继续只连接当前 `executor-gateway` 的 MCP endpoint。组件在架构中的逻辑角色称为
**execution gateway**；迁移期不要求立即重命名现有 binary、包名、域名或部署对象。

```text
model
  │ dynamic tool call
  ▼
harness-worker ── MCP ──► execution-gateway ──► agentx backend ── WSS ──► BYO
                                  │
                                  └────────────► TAE backend ──► sandbox-gateway ──► TAE

harness-pool ── lifecycle capability ─────────► sandbox-gateway ──► TAE session API
```

execution gateway 独占以下职责：

- MCP catalog 与冻结目录校验；
- run capability、workspace/session/run/attempt/environment 绑定校验；
- tool arguments 映射、policy、approval 与结果上限；
- Core execution/operation 状态迁移；
- dispatch backend 选择、ACK/terminal/unknown 归一化；
- 对 worker 隐藏 provider 差异。

`harness-pool` 可以直接调用 sandbox-gateway，但仅限不可见于模型的生命周期操作：ensure、renew
activity lease、release 与 delete。它不能直接调用 commands/files API。这样既避免 gateway 成为 run
调度器，又确保所有模型触发的副作用仍经过一个策略与审计边界。

### 2. 引入 provider-neutral dispatch target 与 backend

Core 与 execution gateway 使用通用 target，而不是把 TAE Session 填进 `executor_id` 或
`connection_generation`：

```text
DispatchTarget {
  kind:       agentx | tae
  target_id:  agentserver 内部稳定 ID
  generation: 正整数 fencing token
}
```

- `agentx`：`target_id` 对应 executor，`generation` 对应 live connection generation；
- `tae`：`target_id` 对应 Core 中的 managed sandbox，`generation` 每次创建新的 TAE Session 时递增；
- provider session ID、PSM、region 等只存在于 Core/sandbox-gateway 内部投影，不进入 harness manifest、
  模型上下文或 MCP tool result；
- 每个 operation 从 begin-dispatch 到 terminal 都绑定同一个 target kind/id/generation。旧 generation
  的迟到 ACK、输出或完成必须被 fencing。

execution gateway 内增加 provider-neutral backend contract，至少覆盖：

- start process，并区分“发送前失败”“provider 已接受”“发送结果未知”；
- signal/terminate process；
- read file；
- 有序 stdout/stderr/file chunk、acknowledgement 与 terminal result；
- provider 错误到稳定内部错误码的映射。

现有 agentx dispatcher 通过 adapter 实现该 contract；第一阶段只新增 contract 与 TAE adapter，不改
agentx wire protocol 或现有 BYO fixture。

### 3. sandbox-gateway 只是 provider boundary

新增 `sandbox-gateway`，负责：

- 校验来自 harness-pool 的 lifecycle capability，或来自 execution gateway 的 backend capability；
- 把 provider-neutral lifecycle/commands/files 调用映射为 TAE SDK；
- 处理 TAE region/PSM、ByteCloud 应用 JWT、HTTP/SSE、超时、响应上限与 provider 错误；
- 把 lifecycle 观察结果提交给 Core。

它不负责 MCP catalog、tool policy、approval，也不拥有第二套 execution/operation 状态机。它没有独立的
durable authority database；重启后从 Core 取 managed sandbox 记录，再用 TAE `GetSession` reconcile。

TAE 已经注入并维护 Terminal Sandbox/SandboxD。managed image 安装被批准的 CLI、skill pack runtime
文件与支持 per-process placeholder 的非敏感配置，并包含只负责 FaaS 存活和 `/v1/ping` 的最小 keeper；
placeholder 值按 operation 短期注入、不写入 session 文件。镜像不运行 agentx，不复制官方 Python
Terminal server，也不构建 agentserver 自有 sandboxd。镜像启动边界见
[TAE_SANDBOX_RUNTIME.md](../TAE_SANDBOX_RUNTIME.md)。

### 4. 只承诺 E2B semantic subset

对上层提供的兼容级别命名为 `e2b-semantic-subset/v1`：生命周期类似 create/connect/set-timeout/kill，
执行类似 `commands.run`，文件读取类似 `files.read`。它保留 E2B 对 harness 有价值的心智模型，但：

- 不承诺官方 E2B SDK、HTTP path、JSON 字段或错误对象的 wire compatibility；
- 不暴露 TAE SDK 类型；
- shell 入口继续使用现有 argv contract，adapter 不把 argv 拼成一条未经转义的 shell 字符串；
- v1 不提供 PTY、桌面、任意端口、upload/write-file、后台进程 attach 或完整 E2B filesystem API。

以后若确实要接官方 E2B SDK，应在这一稳定语义层之外新增 wire adapter，而不是让 harness 或 Core
直接依赖某个 provider SDK。

### 5. managed sandbox 是 session-scoped resource

默认每个 `(workspace_id, session_id, environment_id)` 同时只有一个 active managed sandbox generation。
它跨同一 session 的多个 run/attempt 保留工作目录；harness-pool 在 attempt 启动前调用 ensure，在活动时
续租，在 attempt 结束时 release activity lease，而不是立即销毁。

Core 记录期望状态、target generation、provider reference、创建幂等键、TTL、lease、最后观察时间与
失败摘要。sandbox-gateway 负责执行 I/O，Core 负责 CAS 与唯一性。idle TTL 到期、session 删除、显式
reset 或安全事件会触发 delete；另有孤儿回收器以 Core 期望状态和 TAE list/get 结果做双向 reconcile。

创建 TAE Session 时使用稳定幂等键/metadata（若 provider 能力支持）；如果 create 响应丢失，先按该键
reconcile，不能直接再创建。只有确认不存在旧实例后才创建新 generation。

### 6. Core 状态机泛化，但保持现有一次性 dispatch authority

数据库与 internal API 采用 expand/migrate/contract：

1. 给 environment、execution 与 execution operation 增加 `target_kind`、`target_id`、
   `target_generation`；旧字段暂时保留；
2. agentx 路径 dual-write，并校验通用字段与旧 `executor_id/connection_generation` 一致；
3. 回填已有记录，所有 reader 切到通用字段；
4. managed environment/target 上线；
5. 待所有生产 reader 和离线任务迁移后再删除或降级旧字段为兼容投影。

`BeginOperationDispatch` 仍是唯一一次外部发送许可。它调用通用的
`requireLiveDispatchTarget(kind, id, generation)`：agentx 检查 live connection holder；TAE 检查 Core 中
managed sandbox 为 ready、generation 相等、未过期且未被 fence。

provider ACK 后才进入 acknowledged。begin 已提交但无法确认 provider 是否收到时：

- 若有 provider operation ID 或可查询的 idempotency key，先 reconcile；
- 能证明未发送才可回到明确 failed；
- 能证明 provider 已接受则补 ACK 并继续收敛；
- 无法证明时标记 operation/execution `unknown`，不得自动重放 mutation；
- read-only operation 也默认遵循同一规则，只有未来显式声明并审计的安全重试策略可以例外。

### 7. managed egress 使用 TAE Agent Gateway + Policy Webhook + v2 Core

TAE 网络策略采用平台审核的 provider host 白名单且默认拒绝。命中需要动态判断的规则时，TAE Agent
Gateway 调用 agentserver 的 `egress-authorizer` Policy Webhook；`egress-authorizer` 再通过 mTLS 直接调用
v2 Core 的窄 `ResolveEgressCredential` contract。`egress-authorizer` 负责 TAE ZTI、网络请求与
allow/deny wire；Core 负责 workspace binding、密封存储、live authorization 和 provider-specific
materialization。不存在第三个 credential service 或反向代理进程。

CLI 发出的认证值不是第三方真实 credential，而是短期、单受众、单用途 placeholder capability，绑定
workspace/session/run/actor/operation/tool-pack/provider-kind/binding-id/authority-version。请求链路：

1. egress-authorizer 验证 TAE `X-Zti-Token` 的签名、有效期、trust domain 与允许的 sandbox PSM；
2. 验证 placeholder 基本签名和 expiry，并把它绑定到规范化后的原请求 host/path/method；
3. 对匿名 allowlist 请求直接按 policy 判定；对需要 credential 的请求，以 mTLS 调 Core；
4. Core 再验 placeholder，并在同一权威数据面 live-authorize session/run/actor/operation、workspace
   membership、binding authority version、pack policy 与 approval proof；
5. Core 从 `workspace_credential_bindings` 读取同 workspace/kind/id 的 active binding，解封并由
   provider adapter 生成 closed-world header mutation；需要刷新或 exchange 的 provider 必须通过显式、
   有界 adapter 完成，不能读取部署环境中的 workspace secret；
6. egress-authorizer 只允许 tool pack 中已编译的 host/path/method 和 header 集，在 allow response 中返回
   mutation，并与 credential-use event 使用同一审计 ID；未知或写请求默认 deny。

workspace credential 由 workspace 管理员在 Platform 动态创建、轮换、设默认和撤销。Pulumi、Helm、
production deployment 与 sandbox image 不得携带 workspace/binding/user ID、Lark token、workspace
ByteCloud AK/SK、GitHub PAT/App private key 或 expiry。没有任何 binding 是合法空状态：不阻塞部署、Core
readiness、sandbox ensure 或纯本地命令；只有真正需要该 kind 的 operation 以
`credential_not_configured` fail closed。

Policy Webhook 必须始终在 500ms 内返回 HTTP 200 的合法 allow/deny JSON；目标 P99 小于 300ms。
交互授权和无界远程 refresh 不允许出现在热路径。后台预刷新是优化，按需 JWT mint/refresh 必须有界并按
binding/credential version 隔离；任何超时、校验失败或 credential 未就绪都 fail closed。

下列事实必须在 SG PoC 中验证后才能启用真实 Lark 流量，文档描述本身不算证据：

1. 公网 `open.feishu.cn` 的 TAE policy 支持绑定 Webhook；
2. Webhook 能收到原始 placeholder `Authorization`；
3. allow response 返回的 provider header（例如 `Authorization` 或 `X-Jwt-Token`）会覆盖而非追加原值；
4. sandbox 无法绕过 TAE Agent Gateway 直连目标；
5. Linux/amd64 `lark-cli` 接受 placeholder token 配置且不会在本地提前拒绝。

若其中任一项不成立，只能启用单独设计和安全评审的 SG egress-edge/host-remap fallback。回退不改变
execution gateway、backend、workspace binding 或 Core target 设计，也绝不把真实 credential 注入
sandbox；它同样必须向 Core 请求 materialization，不能复制 credential store。

### 8. 隔离 TAE 私有 SDK

TAE adapter 放入独立 internal Go module（目标路径 `v2/providers/tae`），由 sandbox-gateway binary
依赖。主 `v2/go.mod`、Core、execution gateway、harness 与 provider-neutral contract 不 import
`code.byted.org/inf/bytedai-go/sandbox`。CI 分别验证：

- 主模块可在没有内部 SDK 凭据的环境编译和测试；
- provider 模块锁定 SDK 版本并运行 contract tests；
- 只有 sandbox-gateway 镜像包含 TAE SDK、ByteCloud Auth SDK 及其依赖；TAE Policy Webhook 的 ZTI
  校验仍属于 egress-authorizer 边界，不是 sandbox provider 的应用身份。

## 被否决的方案

### harness-worker 直接连接 sandbox-gateway

这会让 worker 持有两套 endpoint/capability/catalog，把 policy、approval、状态迁移、错误映射和未知结果
恢复分叉到两个数据面。生命周期直连已经足够避免 execution gateway 承担资源调度，执行直连没有对应
收益。

### 把 TAE Session 伪装成 agentx connection

TAE 是入站 SDK/API，agentx 是用户机器主动建立的有租约 WSS。伪装会制造不存在的 connection
heartbeat/resume 语义，且无法正确表达 TAE create/delete/TTL 与 provider ambiguity。

### 在 TAE 内再运行 agentx 或自建 sandboxd

这重复 TAE 已提供的 Terminal Sandbox，并扩大镜像、凭据和网络攻击面，也让故障归属变得模糊。

### 把全部 Lark CLI 转为 typed MCP tools

现有 skill 和 CLI 已覆盖大量命令，首切片只需保留 `shell`。全量转换成本大、容易与 CLI 版本漂移，
也不是 managed executor 的必要条件。高风险写操作以后可按收益逐个增加 typed tool，不阻塞本方案。

### 直接承诺官方 E2B wire compatibility

它会过早锁定第三方字段、错误与流式协议，同时仍无法消除 TAE 与 E2B 生命周期差异。先稳定内部语义
子集，再根据真实接入方增加 wire adapter。

## 结果与代价

正向结果：worker 与模型工具合同不变；BYO 与 managed 共用授权、审批、审计和未知结果语义；TAE
差异被限制在 provider module；Lark skill 无需重写；workspace credential 动态配置且真实值不进入
sandbox 或部署配置。

代价：Core 需要通用 target 与 credential binding 的数据库迁移；execution gateway 需要从
agentx-specific dispatcher 抽出 backend contract；新增 sandbox-gateway、egress-authorizer、生命周期
reconciler 与 SG 网络策略审批；TAE Webhook 500ms 预算要求专门的容量与缓存设计。

## 上线顺序

1. 落 provider-neutral contract、fake 与 agentx adapter，BYO 行为和 wire fixture 必须零变化；
2. Core expand migration 与 dual-write，生产只读验证通用 target；
3. sandbox-gateway + fake TAE contract tests，再接隔离的真实 TAE SDK module；
4. session lifecycle、TTL/reconcile 与 managed environment 目录；
5. shell/read-file 只读执行，故障注入覆盖 create/dispatch/stream/delete ambiguity；
6. Platform 动态 binding、Core-owned materialization、SG egress 五项硬门禁与真实 `lark-cli`/`bkectl` read-only 垂直测试；
7. 小流量 opt-in，按 workspace/session kill switch 回退到 BYO；
8. 达到 SLO、成本与安全门禁后再考虑 write approval、更多 tool pack 与更多 E2B 语义。
