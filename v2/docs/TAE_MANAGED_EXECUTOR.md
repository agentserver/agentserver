# agentserver v2 — TAE Managed Executor 实施设计

> 状态：canonical design；provider-neutral contract/fake 垂直切片、SG TAE provider module、Core-owned
> workspace credential CRUD/materialization、workspace 级双 credential mode、生产渲染和 egress-authorizer production shell 已接入并通过
> 本地门禁；真实 SG TAE/ByteCloud 应用身份与 provider 网络门禁仍待执行，当前不能把本地绿灯表述为
> 真实生产已上线
>
> 架构决策见 [ADR 0012](adr/0012-unified-execution-gateway-tae-backend.md) 与
> [ADR 0013](adr/0013-core-owned-workspace-credentials.md)。本文是 managed
> executor 的实施真源；旧的 `SANDBOX*.md`、`MANAGED_SANDBOX.md`、`LARK_SLICE.md` 与 ADR
> 0008/0009/0011 只保留历史推演和仍被明确引用的事实。
>
> 兼容级别：`e2b-semantic-subset/v1`。这不是官方 E2B SDK/HTTP wire compatibility 声明。

## 1. 目标

交付一个开箱即用的 managed executor，同时保持当前 BYO executor 行为不变：

- `harness-worker` 仍只看见一个 MCP server 和同一份工具目录；
- managed execution 由 TAE Terminal Sandbox 承载；
- execution gateway 统一完成 tool mapping、policy/approval、Core 状态迁移和 backend routing；
- sandbox-gateway 隔离 TAE SDK 与 provider 差异；
- sandbox 生命周期按 agentserver session 管理并有 TTL/fencing/reconcile；
- credential delivery 由每个 workspace 显式选择 `webhook_swap` 或 `process_env`，每次进程启动重新查询 Core，
  不存在部署默认或自动回退；
- `webhook_swap` 中 sandbox 只有 placeholder，`process_env` 中真实 Lark token 只注入精确的 `lark-cli` 进程及其子进程；
- 首个垂直切片让模型使用已有 Lark skill，通过 `shell` 运行 `lark-cli` 读取飞书文档。

### 1.1 首切片不做什么

- 不重写 Lark skill，也不把所有 `lark-cli` 子命令改成 typed MCP tools；
- 不让 worker、app-server 或模型直接连接 sandbox-gateway；
- 不在 TAE 里运行 agentx 或 agentserver 自建 sandboxd；
- 不提供官方 E2B SDK 的 wire-compatible endpoint；
- 不提供 write-file/upload、PTY、桌面、浏览器、任意端口或后台 daemon attach；
- 不在 Policy Webhook 中等待人工审批或刷新 OAuth token；
- 不立即删除现有 Core agentx 字段或重命名生产 `executor-gateway` binary。

## 2. 命名与不变量

| 名称 | 含义 |
|---|---|
| **execution gateway** | 统一执行策略与状态边界；当前实现仍是 `cmd/executor-gateway` / `internal/executorgateway` |
| **backend** | execution gateway 后方的 provider-neutral process/files adapter；v1 有 `agentx`、`tae` |
| **sandbox-gateway** | TAE provider boundary；提供 lifecycle/commands/files 内部接口，无 MCP catalog、policy 或 execution DB |
| **managed sandbox** | Core 中 session-scoped 的逻辑资源，拥有稳定 ID 和递增 generation |
| **TAE Session** | managed sandbox 某一 generation 对应的 provider 资源 |
| **dispatch target** | `{kind, target_id, generation}`，对一次 operation 做 fencing |
| **egress-authorizer** | 两种 workspace mode 共用的 TAE Policy Webhook adapter，负责 ZTI、请求规范化、operation proof、网络 policy 与最终 allow/deny/header wire |
| **Core-owned credential service** | v2 Core 内的 workspace credential control/data plane library；按 provider registry 解析 binding、live-authorize、解封并生成注入 mutation，不是独立进程 |

以下不变量必须由数据库约束、API 校验和测试共同保证：

1. worker 的 execution capability 只能用于 execution gateway 的 MCP endpoint；
2. 模型触发的 commands/files 调用只能从 execution gateway 到 sandbox-gateway；
3. 每个 operation 在 `BeginOperationDispatch` 后固定一个 target generation；
4. 同一 `(workspace, session, environment)` 最多一个 active managed sandbox generation；
5. provider session ID、binding ID 和真实 credential 不进入模型、app-server、worker manifest、MCP result 或 checkpoint；`process_env` 唯一例外是 access token 会进入精确 `lark-cli` 进程及其子进程环境；
6. sandbox-gateway 不能自行把 operation 从 prepared 推到 terminal；
7. TAE 网络规则未命中、proof/授权事实不完整或 Policy Webhook 出错时一律 deny；
8. BYO 的 agentx wire、catalog schema、approval 和 recovery fixture 在 managed 功能关闭时字节级不变。

## 3. 目标拓扑与职责

下图是两种 workspace mode 共用的数据面。`process_env` 额外由 execution gateway 通过 mTLS 向 Core
解析 token，并向目标 `lark-cli` 注入短期 operation proof；TAE Agent Gateway → egress-authorizer → Core
逐请求授权边不会删除。

```text
┌──────────────────────────── agentserver SG region ───────────────────────────┐
│                                                                              │
│  Core ◄──────── state/CAS/audit ─────────┐                                   │
│    ▲                                      │                                   │
│    │ launch state                         │                                   │
│    │                                      ▼                                   │
│ harness-pool ─ lifecycle ─────────► sandbox-gateway ── TAE SDK/JWT ─────┐     │
│    │                                      ▲                              │     │
│    └─ starts harness-worker               │ backend calls                │     │
│                 │                         │                              │     │
│                 └─ MCP ─► execution-gateway                              │     │
│                                ├─ agentx backend ─ WSS ─► BYO             │     │
│                                └─ TAE backend ───────────┘                │     │
│                                                                              │
│  Core + sealed credential bindings ◄────────────── egress-authorizer ◄───────┐    │
│       live auth/provider materialize              TAE Policy Webhook  │    │
└──────────────────────────────────────────────────────────────────────────┼────┘
                                                                           │
┌────────────────────────────── TAE SG region ─────────────────────────────┼────┐
│ managed image: approved CLI + skill runtime; per-process reserved env    │    │
│                                                                          │    │
│ lark-cli ─ HTTPS ─► TAE Agent Gateway ───────────────────────────────────┘    │
│                          │ allow + injected real Authorization                 │
│                          └──────────────────────────────► open.feishu.cn       │
└───────────────────────────────────────────────────────────────────────────────┘
```

### 3.1 组件职责矩阵

| 职责 | Core | harness-pool | execution gateway | sandbox-gateway | egress-authorizer |
|---|---:|---:|---:|---:|---:|
| session/run/attempt authority | ✓ | 读取/持 lease | 读取/校验 | 读取/校验 | 验 placeholder 或 process proof |
| managed sandbox desired state/generation | ✓ | ensure/release | 读取 target | 提交观察/CAS | — |
| TAE SDK、PSM、region、SSE | — | — | — | ✓ | — |
| MCP catalog/tool schema | — | — | ✓ | — | — |
| tool policy/approval | ✓ authority | approval bridge | ✓ orchestration | — | egress rule |
| execution/operation 状态迁移 | ✓ | — | ✓ caller | — | — |
| provider backend routing | — | — | ✓ | provider adapter | — |
| workspace credential CRUD/metadata | ✓（含 provider schema） | — | 只读 binding ref | — | — |
| credential 密封/解封/materialize | ✓（内置 service） | — | `process_env` 只接单个 access token | — | 不持 sealing key，只接 Core mutation |
| egress request allow/deny/header | live recheck/有界 mutation | — | — | — | 两种 mode 均逐请求执行 |

`sandbox-gateway` 可以返回 provider observations，但不能以自身数据库作为事实真源。Core 不 import TAE
SDK；execution gateway 只 import provider-neutral contracts。

## 4. 防偏移的 Golden Path：Lark CLI 查文档

以下链路是所有阶段的端到端验收主线。

1. workspace owner 在 Platform 启用 `lark-readonly@v1` pack，并通过 Platform
   credential binding API 创建或选择一条 `kind=lark` binding。Core 只执行权限、provider parser、CAS 和
   audit；canonical sealed binding 只由 Core 内置 credential service 读取、解封和 materialize。这里不新增
   TAE 专用 grant 表，也不要求 Helm/Pulumi 更新 workspace secret。
2. session 选择 `execution_mode=managed` 与该 pack。Core launch state 冻结 environment、pack digest、
   skill digest 和 egress policy digest。
3. harness-pool 在启动 attempt 前，以 lifecycle capability 调用 sandbox-gateway `EnsureSandbox`。
4. sandbox-gateway 向 Core reserve `(workspace, session, environment)`；若已有 ready generation 则用 TAE
   `GetSession` 核实，否则用稳定 idempotency key 创建 TAE Session，再 CAS 为 ready。
5. managed image 已包含固定 hash 的 Linux/amd64 `lark-cli`；session runtime projection 只写入非敏感
   pack 配置。execution gateway 在每个已授权 `shell + lark-cli + TAE process_start` 前查询 workspace 当前 mode 并获取
   reserved process value：`webhook_swap` 注入短期 placeholder；`process_env` 通过 mTLS 调 Core 的窄接口，
   在 Core 再次校验 live operation/binding version 后注入真实 access token 和短期 operation proof。两种模式都拒绝模型 env
   覆盖；任何值都不进入 TAE create 参数、session 文件、manifest、result 或 checkpoint。
6. worker 启动 stock app-server，`baseInstructions` 含现有 Lark skill 文本。MCP catalog 仍精确是
   `list_environments`、`shell`、`read_file`。
7. 模型根据 skill 发出 `shell(argv=["lark-cli", ...read doc...])`。worker 只把原 tool call 交给
   execution gateway。
8. execution gateway 校验 capability/catalog/environment，完成 mapper、policy 与 Core
   `PrepareExecution → PrepareOperation → BeginOperationDispatch(target=tae/ID/generation)`。
9. TAE backend 调 sandbox-gateway `RunCommand`。后者用 TAE Terminal API 的 path+args 形式启动进程，
   不通过 shell 字符串重新解析 argv；provider 接受后 gateway 向 Core ACK。
10. `webhook_swap` 下，`lark-cli` 携 placeholder Authorization 请求 `open.feishu.cn`；egress-authorizer
    验证 TAE ZTI、只读 path 和 live workspace authority 后从 Core 取得真实 Authorization mutation。
    `process_env` 下请求携真实 bearer 与 `X-Agent-Trace` operation proof；egress-authorizer 与 Core 验签、
    重查 workspace mode/binding/version、常量时间比较 bearer，放行时把 trace 清洗为 `agentserver-managed`。
11. stdout/stderr SSE 被有界、按序映射；进程 exit 映射为 operation terminal，再完成 execution。
    文档内容作为现有 MCP `shell` result 返回模型。
12. Core 能关联 run → execution → operation → target generation → credential-use → egress audit；任何
    日志、result、rollout、checkpoint 和 TAE metadata 扫描都不得命中真实 token。

如果 workspace 尚未配置 `kind=lark` binding，步骤 1 仍可启用 pack，部署、Core readiness、sandbox ensure
与纯本地命令全部正常；只有步骤 5 的 credential-required operation 以 `credential_not_configured` 拒绝。

未知 host 在两种模式下都必须被 TAE whitelist 拒绝；egress-authorizer 对两者强制相同的 method/path
只读策略与 live revoke。`process_env` 的额外安全差异是目标 `lark-cli` 及其子进程可以读取真实 token；
proof 缺失、过期、篡改、跨 operation/workspace 或 mode/version 变化仍会在网络层拒绝请求。

## 5. 对 harness 暴露的能力

### 5.1 MCP catalog 不变

首切片不新增模型可见工具：

| Tool | managed 映射 |
|---|---|
| `list_environments` | Core environment registry 同时投影 BYO 与已授权 managed environment |
| `shell` | 现有 mapper/policy 后路由到 backend `StartProcess`；timeout 使用独立 terminate operation |
| `read_file` | 现有路径/大小策略后路由到 backend `ReadFile` |

managed environment 的 display metadata 可以说明它是托管环境，但 tool schema、namespace 和调用方式不
因 backend 改变。run manifest 冻结所选 environment ID 和 catalog digest，不冻结 TAE provider ID。

### 5.2 harness-pool 的生命周期 API

这些调用不进入模型 catalog，语义属于 `e2b-semantic-subset/v1`：

```text
EnsureSandbox(workspace, session, environment, requested_ttl, pack_set_digest)
  -> sandbox_id, target_generation, state, expires_at, root

RenewSandboxActivity(sandbox_id, target_generation, attempt_id, lease_ttl)
  -> expires_at, lease_expires_at

ReleaseSandboxActivity(sandbox_id, target_generation, attempt_id)
  -> idle_expires_at

DeleteSandbox(sandbox_id, target_generation, reason)
  -> accepted/current_state
```

`EnsureSandbox` 是幂等 ensure，不等价于每次都 create。`Release` 只释放 attempt activity lease；session
资源进入 idle TTL，不立即删除。显式 session reset/delete 可以调用 `DeleteSandbox`。

pool capability 至少绑定：audience、workspace、session、run/attempt、environment、允许 action、过期时间
和 nonce。它不能调用 commands/files，也不能选择任意 template/PSM/region；这些由 Core environment
profile 决定。

## 6. Provider-neutral execution backend contract

contract 放在主模块 `internal/executionbackend`，不得出现 agentx RPC 或 TAE SDK 类型。

### 6.1 Target

```go
type Target struct {
    Kind         Kind   // agentx | tae
    ID           string // agentserver internal ID
    Generation   int64  // fencing token
    EnvironmentID string
}
```

每次 backend 调用都带 target 和 Core operation ID。adapter 不得自行选择另一个 generation。

### 6.2 Process/files operations

第一版 contract 覆盖：

- `StartProcess`: executable、argv、clean absolute cwd、显式 env allowlist、timeout hint、output limit；
- `SignalProcess`: provider process handle、signal/terminate reason；
- `ReadFile`: clean absolute path、offset、最大字节数；
- `Exchange`: `AwaitAcknowledgement`、有序 `NextEvent`、`AwaitTerminal`、`Done`；
- event stream：stdout、stderr、file bytes，单调 sequence；
- terminal：succeeded/failed/cancelled/unknown、可选 exit code、稳定内部 reason。

`AwaitAcknowledgement` 代表 provider 已接受具体 operation，不代表命令成功。返回的 provider operation
handle 是 opaque observation，只能在 gateway/sandbox-gateway 内使用和审计，不能覆盖 Core operation ID。

### 6.3 错误分类

所有 adapter 必须把错误归入以下 dispatch outcome：

| Outcome | 含义 | Core 收敛 |
|---|---|---|
| `not_sent` | 在任何 provider byte 发送前失败 | operation failed；允许由上层重新发起新的 tool call |
| `rejected` | provider 明确未接受 | ACK 不产生；operation failed |
| `accepted` | provider 给出可验证 handle/ack | Core ACK；继续等待 terminal/reconcile |
| `unknown` | 请求可能送达但没有可验证结果 | query/reconcile；仍不确定则 operation/execution unknown |

网络 timeout 本身不是 `not_sent` 证据。adapter 只有在 transport 明确报告请求尚未写出时才可使用
`not_sent`。provider 返回 5xx 也不能一概重试：必须结合 provider request ID/idempotency contract 判断。
TAE 数据面允许调用方用 `x-tt-logid` 关联请求；adapter 会传递内部 request ID，但该 header 只用于
观测，不构成 provider 幂等键，也不能把已写出但丢失响应的 start 安全降级成 `not_sent`。

### 6.4 Agentx adapter

现有 `ProcessDispatcher`、`FilesystemDispatcher` 和 `ProcessExchange` 先由薄 adapter 包装：

- target kind 必须是 `agentx`；ID/generation 映射现有 executor/connection generation；
- 原 RPC JSON、session sequence、timeout coordinator、disconnect recovery 保持不变；
- 所有现有 agentx tests 继续直接测试旧实现，另加 adapter contract tests；
- adapter 稳定后，shell/read-file orchestration 才切到通用 interface。

### 6.5 TAE adapter

TAE backend 位于 execution gateway，但只调用 sandbox-gateway 的 provider-neutral HTTP/gRPC contract。
真正 import 私有 TAE SDK 的代码位于 `v2/providers/tae` 并只链接到 sandbox-gateway。

映射原则：

- lifecycle → TAE Create/Get/Update TTL/Delete Session；
- `StartProcess` → Terminal Sandbox `POST /api/process/start`/SDK streaming 方法；
- stdout/stderr → 保序有界 events；
- `ReadFile` → Terminal FSDownload/read API，严格执行 gateway 已批准的 offset/limit；
- terminate → provider process terminate/signal 能力；若目标版本不支持，首切片不得用“删除共享 session”
  冒充正常 terminate，必须先过能力门禁并定义 fence/delete 的异常恢复；
- SDK 自动或默认重试必须关闭/审计，不能绕过 Core one-shot dispatch permission。

### 6.6 TAE provider 应用身份

SG production 的 Terminal Sandbox PSM 固定为 `bytedance.sandbox.agentserver`。它同时是
provider scope、TAE policy binding 的 `sandboxPsm`，并始终作为 egress-authorizer 允许
PSM；不能由 session 请求或运行时环境覆盖。Policy Webhook 使用 HTTPS URL
`https://egress-authorizer-sg.byted.bps.dev/v1/policy`，不把 K8s egress-authorizer 冒充成 TCE/FaaS PSM。
Istio Gateway 终止公网 TLS，再通过 BackendTLSPolicy 使用 namespace 内
`agentserver-egress-backend-ca` ConfigMap 对 egress-authorizer 的 workload TLS 做校验。

SG 的 sandbox-gateway 无浏览器 provider workload 固定使用基础设施身份 `bytecloud-app-aksk-v1`，不依赖个人账号或本机 ZTI。
这组 AK/SK 只用于调用 TAE control/data plane，和 workspace 管理员在 Platform 配置的 `kind=bytecloud`
credential 完全不同；后者只能由 v2 Core 内置 credential service 读取：

- AK/SK 只作为两个只读 Secret 文件挂载到 `sandbox-gateway`，不得投影到 execution gateway、
  harness、TAE Session metadata、sandbox env、Core DB、日志或错误；
- ByteCloud site 固定为 `i18n-tt`，与 SDK 的 `AIPaaSGatewayRegionI18nProd` 选择一致；
- `cloud-sdk-go` 使用 AK/SK 换取短期 JWT，control-plane 与 sandboxd data-plane 共用同一个
  `HeaderSource`，请求只注入 `X-Jwt-Token`；官方 SDK 负责按 AK+site 缓存和并发刷新；
- SG JWT exchange host 固定为 `https://cloud-i18n-sg.bytedance.net`，通过
  `AGENTSERVER_V2_TAE_BYTECLOUD_JWT_ENDPOINT` 显式注入并在启动时精确校验，避免 SDK 默认 I18N host
  列表先访问跨地域地址导致 SG exchange deadline 被耗尽；
- JWT exchange、TAE control-plane 与 sandboxd data-plane 的 provider transport 全部固定使用
  `socks5h://ssh-egress-merlin-i18nbd-syd2a-83092-headless.ssh-egress.svc.cluster.local:1080`，并由
  `AGENTSERVER_V2_TAE_PROXY_URL` exact-match；不能配置 proxy userinfo、其他 egress 或直连 fallback。
  标准 proxy 环境变量继续 fail closed。control-plane transport 只允许
  `controlplane.sg.ai-sandbox-i18n.byted.org:443`，data-plane transport 只允许一个 canonical session DNS
  label 加 `.sg.ai-sandbox-i18n.byted.org:443`，目标 DNS 均在 syd2a 侧解析；
- 启动 readiness 使用一次 bypass-cache 强制刷新验证凭据；正常流量使用 SDK 缓存。TAE 明确返回 401 时只
  刷新下一次请求使用的身份，不能盲目重放已经写出的 create/process-start；
- SDK exchange 错误、TAE 错误和 readiness 日志只输出稳定 reason class，不输出 AK、SK、JWT 或响应体；
- Secret 轮换通过更新 Kubernetes Secret 触发 Reloader rollout。单个 Pod 生命周期内不热读新凭据，
  避免 AK/SK 两个文件跨代组合。

workspace credential 的 secret、expiry、binding ID 和 refresh state 不得出现在上述 sandbox-gateway
Secret、TAE Session metadata、sandbox env、Core deployment document 或 Pulumi state 中；它们走
Platform gateway → v2 Core credential API/内置 materialization 数据面。

2026-08-09 在 SG `default/dev` 中对 JWT origin 做 50 次独立连接采样：直连 IPv4 31/50、IPv6 25/50
成功，其余均在 connect 阶段超时；经 syd2a i18nbd SOCKS5H 为 50/50，useast14a 也是 50/50，
但 syd2a 的 TTFB P50 为 747ms，明显优于 useast14a 的 1.59s。因此生产只选择 syd2a。
同日对 I18N production control-plane 再做 50 次独立 TLS 连接：直连 IPv4 31/50、IPv6 26/50；
通过 syd2a 做 20 次 SOCKS5H TCP connect 为 20/20。这证明故障不局限于 JWT origin，不能保留
control/data-plane 直连。

`sandbox-gateway` NetworkPolicy 只允许向 `ssh-egress` namespace 内
`app=ssh-egress-merlin-i18nbd-syd2a-83092` Pod 的 TCP 1080 建立 TAE proxy 连接；
`sandboxExternalEgress` 必须为空。JWT、control-plane 和 per-session data-plane 均不配置直连 CIDR，
也没有 `0.0.0.0/0`、`::/0` 或跨地域 fallback。

## 7. sandbox-gateway contract

### 7.1 兼容声明

`e2b-semantic-subset/v1` 表示 create/connect/set-timeout/kill、commands.run、files.read 的语义相似，
不表示以下内容兼容：

- 官方 E2B URL、SDK class、JSON 或 error wire；
- command string 的 shell parsing；
- E2B template/build/desktop/filesystem 全量功能；
- provider-specific metadata 与进程 handle。

内部请求使用版本字段和严格 JSON；未知输入字段拒绝，provider 响应允许忽略明确标记的向后兼容字段。

### 7.2 Commands/files 请求边界

execution gateway 调用至少绑定：

```text
operation_id
workspace_id / session_id / run_id / attempt_id
target_id / target_generation
environment_id / pack_set_digest
executable + argv + cwd + explicit env
deadline + stdout/stderr/result byte limits
```

backend capability 绑定相同 identity 和单个 operation ID，只允许一个 action；sandbox-gateway 重新向
Core introspect live attempt、target generation 和 operation dispatching 状态。它不信任 gateway 传入的
provider session ID，也不接受任意 PSM/region/template。

### 7.3 响应和流控

- TAE 当前文档没有为 process SSE 定义 event ID、resume cursor 或可验证的重放边界；adapter 只在收到
  stdout/stderr 后为内部 event 分配单调 sequence。`process/connect` 后无法证明没有丢失或重放输出，
  因此即使最终收到成功 exit，也必须返回 `outputComplete=false`，不得按内容猜测去重；
- stdout 与 stderr 保留 channel，但不承诺跨 channel 的 provider 纳秒级顺序；adapter 给出统一接收序；
- 单 event、总输出、单行和 terminal metadata 都有硬上限；超过时主动终止进程并返回 truncated 标记；
- client cancel 触发 terminate operation，不等同于已确认 provider 停止；
- stream 断开后先按 provider process handle 查询；无查询能力或结果不确定时 fail unknown。

## 8. Core 数据模型与迁移

### 8.1 通用 target 字段

建议 expand migration：

```text
executor_environments / 后续 execution_environments
  backend_kind             agentx | tae
  dispatch_target_id       nullable during migration
  dispatch_target_generation
  root_descriptor          local | managed

executions
  target_kind
  target_id
  target_generation        frozen at prepare/begin boundary as finalized by API design

execution_operations
  target_kind
  target_id
  target_generation
```

最终精确放在 execution 还是 operation 的 generation 以“一次 execution 是否允许跨 generation”决定。
首切片规定不允许：execution prepare 后 target 被替换则原 execution fail/unknown，由新 tool call 建新
execution。因此 execution 和 operation 都保存投影，并用约束保证一致，便于查询与审计。

现有 `executor_id`、`connection_generation` 不原地改名。先加 nullable 列，agentx dual-write/backfill，
验证一致性后再让 managed 写入。所有 API 在过渡期同时返回旧投影和新 target，contract tests 防止旧
client 静默读到零值。

### 8.2 managed sandbox authority

Core 新增逻辑表（最终命名可随 migration 规范调整）：

```text
managed_sandboxes
  id, workspace_id, session_id, environment_id
  provider_kind = tae
  generation
  desired_state, observed_state
  provider_region, provider_psm, provider_session_ref
  create_idempotency_key, runtime_profile_digest, pack_set_digest
  expires_at, idle_expires_at, last_observed_at
  lease_holder, lease_generation, lease_expires_at
  version, created_at, updated_at, deleted_at
  last_error_code, last_error_digest
```

约束：

- active partial unique `(workspace_id, session_id, environment_id)`；
- generation、version、lease_generation 为正；
- provider ref 只有 ready/deleting/unknown 等已发送 create 的状态可出现；
- secret、placeholder 原文与第三方 token 不落此表；
- error 只存稳定 code、provider request/log ID 的安全摘要，不存 response header/body secret。

建议状态：`reserved → creating → ready → deleting → deleted`，旁路 `failed` 与 `unknown`。
只有 Core command 能迁移；sandbox-gateway 提交带 expected version/generation 的 observation。

### 8.3 live target 校验

`BeginOperationDispatch` 从 `requireLiveExecutorConnection` 泛化为：

```text
requireLiveDispatchTarget(kind, id, generation):
  agentx -> live connection holder、gateway owner、lease、generation
  tae    -> managed sandbox desired/observed ready、session binding、TTL、lease、generation
```

ACK/complete 同样比较 operation 保存的 target，而不再只比较 connection generation。managed target 被
delete/fence 后的迟到 response 只能作为审计 observation，不能改变新 generation。

## 9. 生命周期、TTL 与 reconcile

### 9.1 Ensure 流程

1. pool 带 session launch identity 调 sandbox-gateway；
2. gateway 向 Core reserve/读取 active row；
3. ready row：调用 TAE GetSession，核对存在、状态和 expiry；
4. reserved/creating：由持有 lifecycle lease 的 gateway 执行 create；其他 caller 等待 Core 观察；
5. create 成功：提交 provider ref/expiry/runtime digest，Core CAS ready；
6. create timeout：按 idempotency key/metadata 查询；能唯一找到则 adopt，确认不存在才重建；否则 unknown；
7. 返回的是 agentserver sandbox ID + generation，不是裸 TAE Session ID。

### 9.2 TTL

- requested TTL 由 Core environment policy 给出，pool 只能请求更短值；
- provider 支持范围通过 TAE adapter capability discovery/config 校验，不在主模块写死；
- activity lease 心跳与 TAE TTL 更新分开：Core lease 是并发 authority，TAE TTL 是资源兜底；
- 在 expiry 前带 jitter 续 TTL，只在 Core row 仍 desired ready 且有活动/未到 idle deadline 时执行；
- renew 结果未知时 GetSession reconcile；在确认续期前不把 Core expiry 乐观后移；
- session delete/reset、安全 kill switch 立即把 desired state 置 deleting 并 fence generation。

### 9.3 删除与孤儿

Delete 必须幂等。Core 先 fence generation/置 deleting，阻止新 begin-dispatch；sandbox-gateway 再调
TAE Delete，GetSession 404 或明确 deleted 后提交 deleted。删除请求未知时保持 deleting/unknown 并重试
查询，不能把 row 清掉。

regional reconciler 定期：

- 扫描 Core creating/deleting/unknown/过期 row；
- 对 active row 调 TAE get，修正 observed state；
- 在 provider list 能力与安全审核允许时，用 agentserver metadata 找到无 Core owner 的 orphan 并删除；
- 只处理本 region/PSM shard，使用 lease 避免多实例并发；
- 每个决定写 transition/outbox event，支持重放和告警。

## 10. Execution 状态与歧义处理

### 10.1 正常 command

```text
execution gateway                 Core                 sandbox-gateway/TAE
       │ PrepareExecution/Operation │                           │
       ├────────────────────────────►│                           │
       │ BeginOperationDispatch      │                           │
       ├────────────────────────────►│ -- one-shot Began=true -->│
       │ RunCommand --------------------------------------------►│
       │◄---------------- provider accepted(handle) -------------│
       │ AcknowledgeOperation    │                               │
       ├────────────────────────►│                               │
       │◄---------------- stdout/stderr -------------------------│
       │◄---------------- terminal ------------------------------│
       │ CompleteOperation/Execution                             │
       ├────────────────────────►│                               │
```

只有拿到 `Began=true` 的事务调用方可执行外部 send。相同 operation 的 retry 若 `Began=false`，必须
reconcile 原 send，不得再次调用 `RunCommand`。

### 10.2 Timeout/cancel

保持当前“timeout terminate 是独立 operation”的语义：原 process operation 已 ACK/running 后，gateway
prepare timeout operation、begin-dispatch 到同一 target generation，再发 provider terminate。只有 provider
确认停止或查询到 terminal 才完成 cancelled/failed；control context cancel 不能提前冒充停止成功。

如果 terminate 无法确认，原 process 与 terminate operation 都进入 unknown，并 fence/delete 该 managed
sandbox generation 作为安全恢复；不能继续在可能仍运行命令的 session 上接收新操作。

### 10.3 Gateway/provider 重启

- execution gateway 重启：沿用 Core recovery，按 dispatching/acknowledged 和 target kind 分派 reconciler；
- sandbox-gateway 重启：无 durable execution state，从 Core target + provider handle/query 恢复；
- TAE stream 断开：保留最后 sequence/handle，查询进程；不能证明 terminal 时 unknown；
- Core 暂时不可达：sandbox-gateway 不接受新 operation；egress-authorizer 对两种 mode 都 deny，
  `process_env` 同时无法解析新 token、不得启动新 `lark-cli`；已运行进程输出可以有界缓冲，但不能在没有
  Core ACK/terminal authority 时宣称成功。

## 11. Managed egress 与 Core-owned workspace credential

### 11.1 Credential mode、runtime projection 与 binding reference

tool pack 只声明 provider requirement，不携带 credential 实例。run/session 冻结：skill text、runtime
artifact digest、`credentialKind`、可选 `bindingId`、`authorityVersion` 和 egress rule digest；冻结对象中
永远没有 sealed secret、access/refresh token、AK/SK、PAT 或 provider session ID。

mode 的唯一事实源是 Core 的 workspace row；`WorkspaceState`、create/update API 与 Platform owner 设置页
显式暴露 `managedLarkCredentialMode`。新 workspace 必须选择 `webhook_swap` 或 `process_env`，owner 切换时
使用 workspace version CAS 并写独立审计。production config、Helm values/schema/guard、Pulumi 和 workload
environment 都没有 mode 字段。

- `webhook_swap`：execution gateway 解析 binding reference 并签发短期 placeholder；Policy Webhook 在真实
  HTTP 请求上完成 token swap；
- `process_env`：execution gateway 在精确 `shell + lark-cli + TAE process_start` 前调用 Core
  `/internal/v2/execution/credentials/lark:resolve`，Core live-authorize 后返回真实 access token；execution
  gateway 同时签发 operation-bound proof，Policy Webhook 再做请求级 live authorization。

两种 mode 始终共用 placeholder/proof signer、Core verifier、egress-authorizer、Service/Route/TLS/NetworkPolicy
和同一份 TAE policy。没有 `auto`、部署默认或运行时 fallback。切换 workspace mode 不改变 policy/network
evidence/runtime profile/pack set/owner policy digest，也不要求发布新 Chart；下一次 process start 和后续
HTTP 请求会从 Core 当前 workspace mode 重新判定并 fence 旧 authority。

managed image/session runtime projection 只包含固定版本和 hash 的 CLI、非敏感 endpoint/config 与 TAE façade
地址。execution gateway 在合并完模型允许的 env 后最后注入 reserved value，名称冲突直接拒绝；相关
request/body/log/trace 必须 redact，不使用 TAE ZTI `user` 替代 agentserver actor。

没有所需 binding 时仍可创建 sandbox 和启动 run；execution gateway 不注入 token/placeholder，具体 CLI
会得到未配置认证的失败。Core direct resolve 用 `configured=false` 表达正常未配置状态，不回退到另一 mode。
binding 的 secret rotation 只推进 `credentialVersion`，不要求重建 TAE Session；revoke/owner/policy 变化推进
`authorityVersion`。`webhook_swap` 会立即 fence 旧 placeholder；`process_env` 会拒绝后续 process start，
并在每个 HTTP 请求上因 proof/live mode/version 不匹配而拒绝已启动进程的新出网请求。已经进入进程内存的
token 字节本身不能远程擦除。

### 11.2 `process_env` 的窄接口与安全边界

direct resolve 路由随 managed execution 一直挂载，但只有 workspace 当前选择 `process_env` 时才成功；
它只接受 executor-gateway 的精确 mTLS SPIFFE identity，并要求：

1. 请求工具必须是 `shell`、executable 必须是裸 `lark-cli`，target 必须是配置的 TAE PSM；
2. Core 先从 live operation 选择 default `kind=lark` binding，再以该 binding/version 重做一次
   workspace membership、session/run/attempt lease、execution、`process_start`、sandbox generation 和 activity 校验；
3. provider adapter 只允许得到单个 `Authorization: Bearer`，Core 提取 token 后用
   `Cache-Control: no-store` 的响应返回 executor-gateway；
4. executor-gateway 只把 token 合并进该次 TAE StartProcess env，禁止调用方覆盖保留变量；
5. Core 写 `stage=process_env` 的最小化 audit，audit 失败则不启动进程。
6. executor-gateway 签发不超过 1024 字节的 Ed25519 proof，通过 `LARKSUITE_CLI_AGENT_TRACE` 只注入目标
   进程；proof 绑定完整 operation、binding/version、policy 和短期 expiry。

真实 token 会对目标进程及其子进程可见，这是该模式的明确安全边界。TAE 仍强制 host whitelist，且
Policy Webhook 对每个请求验证 proof、method/path 与 live revoke。长运行进程中的 token 字节无法擦除，
但 proof 过期或 authority 变化后不能继续经 managed egress 使用它。

### 11.3 共用 Policy Webhook 与 Core 请求判定

egress-authorizer 在总预算内依次执行：

1. 限制 body/header 大小，严格解析 TAE Webhook v1；
2. 用官方 SDK 验证 `X-Zti-Token`，校验 SG trust domain 和允许的固定 TAE PSM；
3. 从原始 request headers 区分签名 placeholder 与真实 bearer + `X-Agent-Trace` proof；两种 framing 都
   严格验签、expiry 与 operation scope，畸形 placeholder 不得落入 process path；
4. canonicalize host/path/method，拒绝 IP literal、混淆 host、未知 method、redirect 扩权和未审核 provider host；
5. 对匿名 host 按平台+workspace egress policy 判定；对带 credential requirement 的 host，通过 mTLS 调
   Core：placeholder 使用 `/internal/v2/egress/credentials:resolve`，process proof 使用
   `/internal/v2/egress/credentials:authorize-process-env`；
6. Core 再验证 placeholder/proof，并 live-authorize workspace 当前 mode、membership、run/attempt/operation、
   binding `authorityVersion`/`credentialVersion` 与 pack policy；process path 还常量时间比较真实 bearer；
7. Core 从 workspace binding store 解封 credential，由 provider adapter 生成
   closed-world header mutation（例如 `Authorization`、`X-Jwt-Token`），绝不返回任意 header 名；
8. egress-authorizer 只返回合法的 `allow|deny` wire 和 mutation；process path 将 proof trace 清洗成
   `X-Agent-Trace: agentserver-managed`，Core
   写入 credential-use/egress audit；
9. 任何依赖超时、版本不符、binding 缺失、refresh 未就绪或 audit 失败都 fail closed。

TAE native Webhook 路径不把真实 secret 放到 sandbox。没有 TAE header mutation 能力的 provider 若需要
reverse-proxy/host-remap，必须单独设计 egress-edge，但仍直接向 Core 请求有界 mutation，不能复制 binding
store。OAuth refresh、AK/SK 换 JWT、交互审批和无界重试不得无约束占用 500ms Webhook 预算；只允许有界、
按 binding/version 隔离的 provider materialization，超预算即 deny。

### 11.4 Provider registry 与首批 provider

provider-neutral registry 至少提供 `kind`、上传解析/校验、runtime projection、请求分类、credential
materialization/refresh 和审计 redaction 接口。首批实现：

| kind | 注入方式 | 说明 |
|---|---|---|
| `lark` | `Authorization: Bearer` | 当前 adapter 接受 Platform 写入的静态 bearer；OAuth refresh 需显式 adapter 后才能宣称支持 |
| `bytecloud` | `X-Jwt-Token` | workspace AK/SK 仅在 Core 内换 JWT；TAE control-plane AK/SK 不复用 |
| `github` | bearer/PAT | 当前 production registry 只启用 static；App installation 需要配置显式 minter 后才会出现在 schema |

provider adapter 不能扩大网络 host；新增 host 需要平台审核 TAE policy/egress zone。workspace 上传的
arbitrary `server_url` 只能作为经过 SSRF、zone 和 provider allowlist 校验后的 metadata，不能成为放行条件。

### 11.5 审计与秘密驻留

egress decision event 记录 workspace/run/execution/operation/target/provider kind/binding ID、policy and
credential versions、host/method/path、decision/reason/latency；credential-use event 与其一一关联。
绝不记录 placeholder/真实 credential、Authorization 原文、AK/SK、JWT、PAT、body 或响应内容。
`webhook_swap` 的真实值只短暂存在于 sealed DB（密文）、Core/egress-authorizer/TAE header mutation 与
上游连接内存；`process_env` 还会存在于目标 `lark-cli` 进程及子进程的 env/内存。两种模式都不得把值
写入 TAE Session metadata、文件、MCP result、日志或 checkpoint。

### 11.6 SG 硬门禁

上线前逐项产生自动化/抓包证据：

| Gate | 通过条件 |
|---|---|
| G1 public policy | 已审核的 provider host（首个为 `open.feishu.cn`）可绑定并实际调用 Webhook |
| G2 original header | Webhook body/header 可读取完整 placeholder Authorization |
| G3 overwrite | allow response 的 provider header 精确替换原值，上游只收到真实 credential |
| G4 no bypass | DNS/IP/IPv4/IPv6/redirect/CONNECT 等路径都不能绕开 Agent Gateway |
| G5 CLI acceptance | pinned Linux/amd64 lark-cli 接受 placeholder 并成功发出请求 |
| G6 latency/fail-close | P99 < 300ms；500ms、非 200、畸形 JSON、Core/secret 依赖故障全部 deny |
| G7 zero-secret | env、proc、fs、stdout/stderr、TAE metadata、Pulumi/Helm、日志、rollout/checkpoint 零 workspace secret |

G1–G7 对两种 workspace mode 都是硬门禁。`process_env` 还必须验证：非 `lark-cli`/BYO operation 零 direct
resolve、exact live-operation 双检、目标进程成功读取 token、同一 sandbox 其他进程环境不可见、proof
缺失/篡改/跨 workspace/mode 切换/binding rotate 全部 deny、trace 被清洗，日志/metadata/result/checkpoint
零 token。任何门禁失败都不得自动切到另一 mode。

### 11.7 写操作

首切片的共用 policy 只包含只读 Lark API，write method/path 在两种 mode 下一律由 Webhook deny。未来写操作
必须在命令发出前由 execution gateway/Core 完成交互审批，并把已消费 approval 绑定进对应 placeholder 或
process proof；在新增并验证该 versioned request-level authority 前，两种 mode 都不得宣称支持受控写操作。

## 12. 安全边界

### 12.1 Capability 分离

| Capability | 持有者 | Audience / scope |
|---|---|---|
| execution MCP | worker | execution gateway；冻结 run/attempt/environment/catalog |
| lifecycle | harness-pool | sandbox-gateway ensure/renew/release/delete；不含 commands/files |
| backend dispatch | execution gateway | sandbox-gateway 单 operation/target generation/action |
| egress placeholder（仅 `webhook_swap`） | sandbox process | egress-authorizer 单 actor/pack/grant/version/host class；无真实 secret |
| process egress proof（仅 `process_env`） | 精确 `lark-cli` 及其子进程 | egress-authorizer/Core 单 operation/binding/version/policy/expiry；无真实 secret |
| Lark access token（仅 `process_env`） | 精确 `lark-cli` 及其子进程 | 单次 live-authorized process start；是真实 secret，不是 capability |
| TAE provider JWT | sandbox-gateway | ByteCloud application identity；不能替代产品用户身份 |

任何 capability 都不能跨 audience 使用；日志、panic、HTTP dump、trace attribute 默认 redact
Authorization、cookie、ZTI、provider JWT 和 placeholder 原文。

### 12.2 Sandbox profile

首切片 profile 固定 Linux/amd64、非 root、最小 filesystem、默认拒绝 egress、无 cloud metadata、无
agentserver control-plane credential。允许写入的 workspace/tmp 路径、process 数、CPU/memory、TTL、输出
和文件读取上限由 versioned runtime profile 冻结。镜像必须 SBOM、漏洞扫描、签名和 digest pin。

### 12.3 路径与命令

- execution gateway 继续做 argv/path/CWD mapper 与 policy；
- sandbox-gateway 再做结构/上限校验，但不重新解释 policy；
- 禁止把 argv `strings.Join` 后交给 `/bin/sh -c`；
- read-file 使用 clean absolute sandbox path，拒绝 NUL、symlink escape、特殊文件和超限结果；
- v1 explicit env 只能来自 runtime profile/pack allowlist，模型不能任意覆盖 proxy/token/loader 相关变量。

## 13. SDK 与仓库边界

建议目录：

```text
v2/
  internal/executionbackend/       # provider-neutral contract
  internal/sandboxcontract/        # e2b-semantic-subset/v1 DTO/validation
  internal/executorgateway/...     # agentx adapter、routing/orchestration
  internal/sandboxgateway/         # provider-neutral handler/auth/Core client（后续）
  providers/tae/
    go.mod                         # 私有 SDK 依赖隔离
    adapter/                       # TAE lifecycle/process/files/SSE mapping
    cmd/sandbox-gateway/           # 只在 provider module 装配并构建的 binary
```

依赖方向：

```text
Core/harness/execution-gateway ─► neutral contracts
                                           ▲
provider-neutral server handlers ──────────┤
providers/tae cmd/adapter ─► TAE private SDK + handlers/contracts
```

若 Go internal/module 可见性妨碍独立 module 引用，将 neutral contracts 放到不含 provider 依赖的
`v2/pkg/...` 或单独小 module；不能反向让主模块 import provider SDK。主 `v2/go.mod` 不 require provider
module，sandbox-gateway 从 `providers/tae` module 构建；版本锁定、replace 与私服认证只存在于 provider
module/CI job。

## 14. 可观测性与 SLO

所有 label 必须有界，provider session ID、path、argv、token 不作为 metrics label。

建议指标：

- lifecycle：ensure/create/get/renew/delete latency、state、unknown、orphan、active/idle sandbox；
- dispatch：按 backend/tool/outcome 的 begin→ack、ack→terminal、unknown、fenced late response；
- stream：bytes/events/truncation/gap/reconnect；
- egress：按 workspace credential mode 区分 placeholder swap/process proof，记录 direct resolve 与逐请求
  Webhook 的 allow/deny reason class、Core/Webhook latency、credential-not-ready；
- capacity/cost：active TAE sessions、CPU/memory profile、idle duration、create cold-start。

trace 使用 run/execution/operation/internal sandbox ID/target generation/provider request log ID 关联；provider
ID 仅进入访问受限字段。日志 sanitizer 在单测中用 canary secret 验证。

首批 SLO 候选（上线前用 SG PoC 数据定最终值）：

- ready sandbox 上 command dispatch begin→ack P99；
- cold ensure ready P50/P95/P99；
- command terminal success/error 正确率与 unknown rate；
- orphan 最长存活时间；
- 两种 mode 的 Webhook P99 < 300ms、timeout deny = 100%；`process_env` direct resolve 还必须在 process-start budget 内 fail closed；
- secret residency gate = 100%：`webhook_swap` sandbox 零真实 secret；`process_env` 除目标进程/子进程外零 token。

## 15. 测试策略

### 15.1 Contract/unit

- neutral target/kind/request/event/terminal validation；
- fake backend 覆盖 not-sent/rejected/accepted/unknown、重复/断流 event、cancel race；
- sandbox contract 的 TTL/ID/path/argv/env/size 边界；
- agentx adapter 与现有 wire fixture 对照；
- TAE adapter 用 fake SDK/HTTP/SSE 验证映射、上限和 retry 关闭；
- Core generic target 状态迁移、generation fencing、dual-write/backfill constraints；
- egress webhook strict parser、ZTI verifier fake、placeholder/grant/path matrix、redaction；
- direct resolve 的 executor mTLS、exact tool/executable/TAE target、live authority 双检、no-store、audit fail-close；
- workspace mode create/update/CAS/audit、动态切换、proof/bearer/Webhook 矩阵、无 fallback、部署中无全局 mode 与 BYO no-op。

### 15.2 Integration/fault injection

至少注入：

- create 请求发送前失败、响应丢失、重复 provider resource；
- Core reserve 后 sandbox-gateway crash；provider create 后 Core CAS 前 crash；
- begin-dispatch commit 后 execution gateway crash；
- process accepted 前/后断流、`process/connect` 重放/丢失、畸形 SSE、terminal 丢失；
- timeout 与自然 exit 竞态、terminate unknown；
- TTL renew 响应丢失、delete 404/5xx/timeout、reconciler 多副本抢 lease；
- target generation 被替换后的迟到 ACK/output/terminal；
- Core/credential cache 不可用；两种 mode 下 egress-authorizer/Webhook 500ms 与非 200；
- IPv4/IPv6、DNS rebinding、redirect、IP literal、unknown Lark path 的 egress bypass 尝试。

### 15.3 Golden E2E

固定一个只读测试文档和最小权限用户 grant，运行完整模型决策或确定性 app-server fixture：

1. skill 被真实注入；
2. 模型/fixture 产生现有 `shell` tool call；
3. TAE 内 pinned `lark-cli` 成功读取；
4. Core execution/operation/egress audit 完整；
5. 输出有界且回到模型；
6. secret canary 全面扫描为零；
7. write/unknown/bypass control 全部 deny；
8. 同 session 第二个 run 复用同 generation，reset 后使用更高 generation 且旧响应被 fence。

## 16. 分阶段实施

### Phase A：不改行为的抽象层

- 新增 `internal/executionbackend` 和 `internal/sandboxcontract`；
- 提供 fake、validation 和 contract tests；
- 包装 agentx dispatcher，但默认 routing 仍只选择 agentx；
- BYO 全量与 race tests 必须通过。

### Phase B：Core target expand

- migration、internal contract、dual-write/backfill；
- generic live-target checker 和 recovery projection；
- 生产 shadow read 对比旧/新字段，无 managed 流量。

### Phase C：TAE provider/lifecycle

- 独立 TAE module、sandbox-gateway、fake provider；
- Core managed sandbox authority、ensure/TTL/delete/reconciler；
- SG create/get/update/delete 与故障注入。

### Phase D：只读 execution

- TAE backend routing、shell/read-file、stream/timeout/unknown；
- managed environment catalog 与 session launch state；
- 不启用第三方 egress时先跑纯本地命令 fixture。

### Phase E：workspace credential egress

- 通用 credential binding/reference、Core-owned TAE `ResolveInjection` endpoint 与 provider registry；
- `lark-readonly` runtime projection 只作为首个 provider fixture，placeholder 与动态 binding 选择；
- egress-authorizer、credential-use/egress 审计；
- 完成 G1–G7 和 golden E2E，并用 ByteCloud/GitHub fake provider 验证无 Lark 特例。

### Phase F：灰度

- workspace allowlist + session opt-in；
- 分 region/PSM 小流量，独立 kill switch 禁止新 ensure/dispatch/egress；
- 达标后扩容；写操作和更多 pack 另立门禁。

## 17. 当前实现状态与生产模块边界

当前主模块已完成可运行的 provider-neutral dev/fake 垂直切片，不再只是 Phase A 骨架：

1. Core schema 和 internal API 已扩展为 `{kind, id, generation, environmentId}` target，并保留 agentx
   legacy projection；
2. Core 已提供 managed sandbox reserve/create observation/activity/delete/reconcile authority；
3. execution gateway 已接入 agentx/TAE backend router，`shell`/`read_file` 仍使用原 MCP catalog 和 policy
   路径，ambiguous dispatch 不会换 provider fallback 或 replay；
4. harness-pool 已在 managed launch 前 ensure/renew，并在 attempt 结束时 release activity；worker 仍只连接
   execution gateway；
5. sandbox-gateway 已提供 provider-neutral lifecycle/commands/files HTTP contract 和 fake TAE provider，
   `cmd/sandbox-gateway --insecure-dev` 可运行完整本地链路；
6. 首个 Lark slice 已实现 workspace 级 `webhook_swap` placeholder 与 `process_env` exact process direct
   injection；两者共用 Policy Webhook/Core live authorization，且无部署默认或自动 fallback。真实凭据在
   `process_env` 中只进入目标 `lark-cli`/子进程，不进入文件或 provider metadata；
7. fake-provider golden E2E 已覆盖 managed shell、placeholder 注入、TAE HTTP backend、sandbox-gateway、
   stdout/terminal 回传，同时保留 BYO agentx contract/regression tests。

生产 provider 的依赖边界仍然有意保持在独立 module，但它已经接入发布构建：

- `providers/tae` 通过官方 Sandbox SDK 固定 SG/I18N 控制面，并以严格 TLS、ByteCloud 应用 JWT、HTTP/SSE 数据面适配
  实现 Create/Get/Search/TTL/Delete、进程流、terminate 和受限文件读取；TAE policy binding digest
  同时写入并校验 session metadata，漂移会 fail closed；process start 使用 `x-tt-logid` 透传内部关联 ID，
  断流重连后的输出不会被误标为完整；
- `cmd/egress-authorizer` 的 production binary 已接入官方 ROW ZTI verifier、严格的 `/v1/policy` Webhook
  parser 和 audit sink；credential materialization 必须经 mTLS 直接调用 Core，不再读取 Lark token；
- 生产镜像构建会编译 `providers/tae/cmd/sandbox-gateway`，把该 binary 与 core/executor/egress
  service binary 一起放入 service image；主 module 中的 provider-neutral `cmd/sandbox-gateway`
  仍只允许 fake/insecure-dev，避免 production 意外退化到 fake provider；
- production renderer 不读取 workspace mode：managed active 始终生成 sandbox-gateway、egress-authorizer、
  共用 key material、Service/Route/TLS/NetworkPolicy；production schema/Helm/Pulumi 明确拒绝全局 mode 字段。
- production release 使用 `disabled` / deny-only `policy-bootstrap` / `active` 三阶段；审批后的一次性
  `tae-network-probe` 不扩大 bootstrap Chart 权限，只用专用 ServiceAccount 和临时 NetworkPolicy 经
  syd2a 执行 20 次 JWT/control 检查、完整 lifecycle、42MB pinned CLI/Skill 摘要校验与资源清理。
  activation 直接解析 canonical report 并绑定 bootstrap config SHA 和实际 policy revision，不接受人工
  填写的报告摘要。

因此当前完成度应表述为“代码与 provider-linked production vertical slice 已完成，本地门禁通过；真实 SG
TAE/provider/credential deployment gates 尚未关闭”。在第 18 节证据齐全前，不得宣称 production-ready，也不得宣称
G1–G7 已通过真实网络和凭据链路验证。

## 18. 上线前待验证项

以下不是开放架构选择，而是需要在目标集群/控制面产生证据后才能关闭的实施门禁。代码接入和本地
contract tests 不替代这些实测：

- TAE SG region/PSM、SDK 版本、应用账号 AK/SK 权限和 ByteCloud JWT/TAE ACL；
- TAE Terminal Sandbox 默认仅支持 IPv6；必须在 SG 实测 `open.feishu.cn` 的 AAAA/DNS、Agent Gateway
  转发、PMTU/MSS 和长响应链路，不能用办公网 IPv4 或 fake provider 结果替代；
- CreateSession 幂等/metadata 查询能力以及 provider request ID 语义；
- streaming start 的精确 ACK 边界、process query/terminate 能力和 SDK retry 默认值；
- TTL 最小/最大/更新语义、delete 后 GetSession 行为、list/orphan 能力；
- Terminal file read 对 symlink/special file 的实际处理；
- 两种 mode 的 TAE Agent Gateway/Webhook 行为都关闭 G1–G7；`process_env` 另验 direct-injection、
  target-process residency、operation proof、bearer 比较、trace 清洗与 live mode/version fencing；
- pinned `lark-cli` 的安装，以及所选 mode 的 placeholder 或真实 token 配置；
- regional credential materialization/cache 在 P99/撤销延迟/secret residency 之间的最终参数；
- SG 容量、冷启动、并发 session 配额和成本基线。

还必须补齐本次发布的运行证据：

- PostgreSQL migration `0020`→`0027` 在真实库执行并可重复 bootstrap；
- TAE SG create/adopt/reconcile/TTL/delete 在丢响应、重复资源、generation fence 和超时下的结果；
- egress-authorizer HTTPS Webhook 从 TAE Agent Gateway 实际可达，且两端 policy binding digest 一致；
  `webhook_swap` 完成 placeholder → real `Authorization` 抓包，`process_env` 证明 direct resolve 只发生于
  exact live `lark-cli` start，并完成 proof + bearer → sanitized trace 的逐请求抓包；
- IPv4/IPv6、DNS、redirect、CONNECT、IP literal bypass、PMTU/MTU/MSS 和错误率复测；
- sandbox-gateway 从 SG 以 `i18n-tt` 应用身份换取 JWT、强制刷新、Secret 轮换和 AK/SK/JWT 零泄漏扫描；
- 所选 mode 的延迟/fail-close、TAE/Lark golden E2E 与 secret residency 扫描；`process_env` 允许目标
  `lark-cli`/子进程 env 命中，除此之外 env/proc/fs/stdout/stderr/metadata/checkpoint/log 必须零 token。

任何一项未知都应在对应 phase fail closed；不能用 provider 文档中的示例代替生产区域实测。

## 19. 官方参考

- [TAE SDK 参考](https://cloud.bytedance.net/docs/tae/docs/6889db56aefa690547a42112/69ae8d2a7358a0054a3e2532)
- [使用 Terminal Sandbox](https://cloud.bytedance.net/api/v1/cloud_developer/docs/tae/cn/68901fd9c95c14097673e2f6.md)
- [TAE 网络策略](https://cloud.bytedance.net/api/v1/cloud_developer/docs/tae/cn/69df71dd772b3e050be7e4e7.md)
- [Policy Webhook 接入规范](https://cloud.bytedance.net/api/v1/cloud_developer/docs/tae/cn/6a1018f89814db04f9e85ef2.md)
