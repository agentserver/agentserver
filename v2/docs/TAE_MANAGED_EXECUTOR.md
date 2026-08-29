# agentserver v2 — TAE Managed Executor 实施设计

> 状态：canonical design；provider-neutral contract/fake 垂直切片、SG TAE provider module、Core-owned
> workspace credential CRUD/materialization、workspace 级 credential mode、direct process_env 生产渲染已接入并通过
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
- 当前 SG direct Sandbox profile 只支持 `process_env`，真实 Lark token 只注入精确的 `lark-cli` 进程及其子进程；
- `webhook_swap` 需要未来独立的 webhook-enabled profile，不能复用 direct Sandbox；
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
| **egress-authorizer** | 仅 webhook-enabled profile 使用的可选 TAE Policy Webhook adapter；当前 direct profile 不部署 |
| **Core-owned credential service** | v2 Core 内的 workspace credential control/data plane library；按 provider registry 解析 binding、live-authorize、解封并生成注入 mutation，不是独立进程 |

以下不变量必须由数据库约束、API 校验和测试共同保证：

1. worker 的 execution capability 只能用于 execution gateway 的 MCP endpoint；
2. 模型触发的 commands/files 调用只能从 execution gateway 到 sandbox-gateway；
3. 每个 operation 在 `BeginOperationDispatch` 后固定一个 target generation；
4. 同一 `(workspace, session, environment)` 最多一个 active managed sandbox generation；
5. provider session ID、binding ID 和真实 credential 不进入模型、app-server、worker manifest、MCP result 或 checkpoint；`process_env` 唯一例外是 access token 会进入精确 `lark-cli` 进程及其子进程环境；
6. sandbox-gateway 不能自行把 operation 从 prepared 推到 terminal；
7. TAE 网络规则未命中或进程启动授权事实不完整时一律 deny；direct profile 不配置 Policy Webhook；
8. BYO 的 agentx wire、catalog schema、approval 和 recovery fixture 在 managed 功能关闭时字节级不变。

## 3. 目标拓扑与职责

下图是当前 SG direct `process_env` 数据面。execution gateway 通过 mTLS 向 Core 解析 token，只注入目标
`lark-cli`；TAE 直接使用系统预置 `*.feishu.cn` 白名单，不经过 egress-authorizer。

```text
┌──────────────────────────── agentserver SG region ───────────────────────────┐
│                                                                              │
│  Core ◄──────── state/CAS/audit ─────────┐                                   │
│    ▲                                      │                                   │
│    │ launch state                         │                                   │
│    │                                      ▼                                   │
│ harness-pool ── starts harness-worker     │                              │     │
│                 │                         │                              │     │
│                 └─ MCP ─► execution-gateway ─ lifecycle/backend calls ───┤     │
│                                ├─ agentx backend ─ WSS ─► BYO             │     │
│                                └─ TAE backend ───────────┘                │     │
│  Core + sealed credential bindings ── live resolve ──► execution-gateway     │
└──────────────────────────────────────────────────────────────────────────────┘

┌────────────────────────────── TAE SG region ─────────────────────────────────┐
│ managed image: approved CLI + skill runtime; exact process env               │
│                                                                              │
│ lark-cli ─ HTTPS with real bearer ─► system *.feishu.cn allowlist ─► Feishu  │
└───────────────────────────────────────────────────────────────────────────────┘
```

### 3.1 组件职责矩阵

| 职责 | Core | harness-pool | execution gateway | sandbox-gateway | egress-authorizer |
|---|---:|---:|---:|---:|---:|
| session/run/attempt authority | ✓ | 读取/持 lease | 读取/校验 | 读取/校验 | webhook profile only |
| managed sandbox desired state/generation | ✓ | — | 首次工具调用时 ensure/renew/release；读取 target | 提交观察/CAS | — |
| TAE SDK、PSM、region、SSE | — | — | — | ✓ | — |
| MCP catalog/tool schema | — | — | ✓ | — | — |
| tool policy/approval | ✓ authority | approval bridge | ✓ orchestration | — | egress rule |
| execution/operation 状态迁移 | ✓ | — | ✓ caller | — | — |
| provider backend routing | — | — | ✓ | provider adapter | — |
| workspace credential CRUD/metadata | ✓（含 provider schema） | — | 只读 binding ref | — | — |
| credential 密封/解封/materialize | ✓（内置 service） | — | `process_env` 只接单个 access token | — | 不持 sealing key，只接 Core mutation |
| egress request allow/deny/header | webhook profile only | — | — | — | webhook profile only |

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
3. harness-pool 直接启动 attempt 和 stock app-server，不创建 sandbox。纯模型对话到此不会访问
   sandbox-gateway，也不会产生 TAE Session。
4. 模型首次实际调用 `list_environments`、`shell` 或 `read_file` 时，execution gateway 才以 lifecycle
   capability 调用 `EnsureSandbox`。sandbox-gateway 向 Core reserve `(workspace, session, environment)`；若已有 ready generation 则用 TAE
   `GetSession` 核实，否则用稳定 idempotency key 创建 TAE Session，再 CAS 为 ready。
5. managed image 已包含固定 hash 的 Linux/amd64 `lark-cli`；session runtime projection 只写入非敏感
   pack 配置。execution gateway 在每个已授权 `shell + lark-cli + TAE process_start` 前查询 workspace 当前 mode 并获取
   reserved process value：direct profile 只接受 `process_env`，通过 mTLS 调 Core 的窄接口，
   在 Core 再次校验 live operation/binding version 后注入真实 access token。若 workspace 是
   `webhook_swap` 则 fail closed。模型不能覆盖 reserved env，
   覆盖；任何值都不进入 TAE create 参数、session 文件、manifest、result 或 checkpoint。
6. worker 启动 stock app-server，`baseInstructions` 含现有 Lark skill 文本。MCP catalog 仍精确是
   `list_environments`、`shell`、`read_file`。
7. 模型根据 skill 发出 `shell(argv=["lark-cli", ...read doc...])`。worker 只把原 tool call 交给
   execution gateway。
8. execution gateway 校验 capability/catalog/environment，完成 mapper、policy 与 Core
   `PrepareExecution → PrepareOperation → BeginOperationDispatch(target=tae/ID/generation)`。
9. TAE backend 调 sandbox-gateway `RunCommand`。后者用 TAE Terminal API 的 path+args 形式启动进程，
   不通过 shell 字符串重新解析 argv；provider 接受后 gateway 向 Core ACK。
10. `process_env` 下 `lark-cli` 携真实 bearer，经 TAE 系统 `*.feishu.cn` 白名单直连 Feishu；没有 webhook、
    header mutation 或 `X-Agent-Trace` proof。
11. stdout/stderr SSE 被有界、按序映射；进程 exit 映射为 operation terminal，再完成 execution。
    文档内容作为现有 MCP `shell` result 返回模型。
12. Core 能关联 run → execution → operation → target generation → credential-use → egress audit；任何
    日志、result、rollout、checkpoint 和 TAE metadata 扫描都不得命中真实 token。

如果 workspace 尚未配置 `kind=lark` binding，步骤 1 仍可启用 pack，部署、Core readiness、sandbox ensure
与纯本地命令全部正常；只有步骤 5 的 credential-required operation 以 `credential_not_configured` 拒绝。

未知 host 必须被 TAE 系统 policy 拒绝。目标 `lark-cli` 及其子进程可以读取真实 token；direct profile 不做
逐请求 live revoke，因此依赖短进程、最小 scope、短期 token，以及每次进程启动前的 live mode/version 校验。

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

### 5.2 execution gateway 的按需生命周期 API

这些调用不进入模型 catalog，语义属于 `e2b-semantic-subset/v1`：

```text
EnsureSandbox(workspace, session, environment, requested_ttl)
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

execution gateway 的 lifecycle capability 至少绑定：audience、workspace、session、run/attempt、environment、允许 action、过期时间
和 nonce。它不能调用 commands/files，也不能选择任意 template/PSM/region；这些由 Core environment
profile 决定。

### 5.3 Session working directory 映射

TAE managed environment 与 BYO agentx environment 在协议层共享 workspace authority 的表示，但当前 TAE profile 不具备
承载通用 session workspace binding 的资格。session 只保存环境 UUID、环境 root 下的相对目录和独立 CAS 版本；TAE
provider 的 session/ref、宿主路径和 provider PSM 不进入 manifest 或模型上下文。
Run launch 时 Core 冻结 environment generation 与 root descriptor digest，gateway 在每个 `list_environments`、`shell`、
`read_file` 调用重新核对它们。`shell` 省略 `cwd` 时使用 frozen directory，显式 `cwd` 只能向下进入；`read_file.path`
会在 gateway 内前缀 frozen directory 后再转换成 TAE 的 clean absolute path。TAE sandbox root 仍是 provider 分配的
`/workspace`，因此命令内部的任意 `cd`/symlink 语义必须继续由 TAE profile 和 backend sandbox enforcement 负责，不能把
相对路径校验误称为 provider root 隔离。

当前 TAE Terminal `/api/process/start` 合同没有 per-process filesystem access 字段；adapter 会把 run 的
`WorkspaceAccess` 原样保留到 `StartProcessInput`，但这本身不是只读 enforcement。只有经过单独 runtime/镜像门禁、能够
证明 `read` 与 `write` 边界的 TAE profile 才能承载通用 workspace 读写；现有固定 managed-CLI profile 的网络/命令策略不能
被当作任意 workspace 的 OS 级只读证明。当前 Core 因而拒绝把 TAE environment 写入 session working-directory binding，
executor gateway 也拒绝旧数据或伪造 authority 形成的 TAE workspace projection；未绑定 session 仍可按既有固定
managed-CLI profile 使用 TAE。

如果用户的本地目录是 `../rtm-aihub`，部署时应把 executor environment root 注册为共同父目录并选择 `rtm-aihub`；API
明确拒绝 `..`，不会把宿主路径或 root 外目录写入 authority。workspace skills 只按 worker 的固定 roots（`skills`、
`.agents/skills`、`.codex/skills`、`.dsh/skills`）查找精确 `SKILL.md`，代码和 skill 内容通过 executor MCP 读写，
不会复制到 harness 的本地 cwd。

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

### 6.6 TAE provider 应用身份与地域路由

SG production workload 支持四个独立 TAE profile，但所有 Terminal Sandbox PSM 都固定为
`bytedance.sandbox.agentserver`。它同时是 provider scope 和 policy binding 的 `sandboxPsm`，不能由
workspace、session 请求或运行时环境覆盖。当前 direct profiles 精确使用系统 `*.feishu.cn` 白名单，
创建 Sandbox/revision/Session 时均不配置 Webhook。

每个 region-specific sandbox-gateway 使用无浏览器基础设施身份，不依赖个人账号或本机 ZTI。AK/SK 只
用于该 profile 的 TAE control/data plane，与 workspace 管理员配置的 `kind=bytecloud` credential 完全
不同；后者只能由 Core credential service 读取。地域映射固定为：

| TAE region | Network route |
| --- | --- |
| `cn` | `merlin-hl-1` |
| `boe` | direct，必须有明确 IPv4/IPv6 allowlist |
| `i18n-bd` | `merlin-useast14a-1` |
| `i18n-tt` | `merlin-i18nbd-syd2a` |

Merlin 名称是逻辑 profile，不编码实际 Service 地址。production config 必须显式给出完整、无 userinfo 的
`socks5h://` URL、namespace、exact Pod selector 与 port；provider 和 NetworkPolicy 使用同一份经过
binding 的值。BOE 的 `proxyProfile` 必须为空；其基础设施 AK/SK 按官方 SDK 别名使用 `site=cn`，其他地域
分别固定为 `cn`、`i18n-bd`、`i18n-tt`，不得使用 direct fallback。每个 TAE profile 还显式绑定 official SDK control-plane URL、data-plane
suffix、ByteCloud site 和 JWT endpoint，provider 在启动时独立解析并 exact-match。

- AK/SK 只作为两个只读 Secret 文件挂载到对应 sandbox-gateway/probe，不得投影到 Executor、Harness、
  TAE Session metadata、sandbox env、Core DB、日志或错误；
- `cloud-sdk-go` 使用 profile 的 site/JWT endpoint 换取短期 JWT，control 与 data plane 共用同一个
  `HeaderSource`，请求只注入 `X-Jwt-Token`；
- proxy profile 的 JWT/control/data transport 全部使用该 profile 的 SOCKS5H route 与 remote DNS；BOE
  只允许配置中的双栈 direct CIDR。标准 proxy 环境变量、proxy userinfo、第二条 egress 和跨地域 fallback
  都 fail closed；
- control transport 只允许 profile 固定 control host，data transport 只允许 canonical session DNS label
  加固定 suffix；
- readiness 使用一次 bypass-cache 强制刷新；TAE 明确返回 401 时只刷新下一次请求的身份，不重放已写出的
  create/process-start；
- SDK exchange/TAE/readiness 日志只输出稳定 reason class，不输出 AK、SK、JWT 或响应体；
- Secret 轮换通过 Reloader rollout。单 Pod 生命周期不热读新凭据，避免 AK/SK 跨代组合。

workspace credential 的 secret、expiry、binding ID 和 refresh state 不得出现在上述 Secret、TAE Session、
sandbox env、deployment document 或 Pulumi state；它们走 Platform → Core credential API/materialization。

2026-08-09 的 syd2a/useast14a 采样只记录当时 i18n-tt 单 profile 的历史网络证据，不能推导新 catalog 的
Merlin 地址，也不能替代 `cn/boe/i18n-bd/i18n-tt` 各自的发布探针。当前 release 对每个已安装地域生成
独立 Job、NetworkPolicy 和 canonical report，并要求 all-profile activation。

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
environment_id
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
  create_idempotency_key
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

1. execution gateway 在首个 executor tool call 上带 session/run-attempt identity 调 sandbox-gateway；
2. gateway 向 Core reserve/读取 active row；
3. ready row：调用 TAE GetSession，核对存在、状态和 expiry；
4. reserved/creating：由持有 lifecycle lease 的 gateway 执行 create；其他 caller 等待 Core 观察；
5. create 成功：提交 provider ref/expiry/runtime digest，Core CAS ready；
6. create timeout：按 idempotency key/metadata 查询；能唯一找到则 adopt，确认不存在才重建；否则 unknown；
7. 返回的是 agentserver sandbox ID + generation，不是裸 TAE Session ID。

### 9.2 TTL

- requested TTL 由部署冻结的 Core environment policy 给出，execution gateway 只能请求该受控值；
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
- Core 暂时不可达：sandbox-gateway 不接受新 operation；`process_env` 无法解析新 token、不得启动新
  `lark-cli`；已运行进程输出可以有界缓冲，但不能在没有
  Core ACK/terminal authority 时宣称成功。

## 11. Managed egress 与 Core-owned workspace credential

### 11.1 Credential mode、runtime projection 与 binding reference

tool pack 只声明 provider requirement，不携带 credential 实例。run/session 冻结：skill text、runtime
artifact digest、`credentialKind`、可选 `bindingId`、`authorityVersion` 和 egress rule digest；冻结对象中
永远没有 sealed secret、access/refresh token、AK/SK、PAT 或 provider session ID。

credential kind 和 CLI 是两个独立维度。Platform 按 workspace 管理 `lark`、`bytecloud`、`github` 等
provider binding；`bkectl` 不是 credential kind，而是 `bytecloud` 的一个 consumer。后续 `bytedcli` 等 CLI
可以复用同一个 active/default `bytecloud` binding，但必须各自增加固定版本、argv policy 和环境变量投影，
不能复制 credential 或重新发起登录。

Lark mode 的唯一事实源是 Core 的 workspace row；`WorkspaceState`、create/update API 与 Platform owner 设置页
显式暴露 `managedLarkCredentialMode`。新 workspace 必须为 Lark 选择 `webhook_swap` 或 `process_env`，owner
切换时使用 workspace version CAS 并写独立审计。ByteCloud 不读取这个 Lark 设置：managed CLI consumer
固定使用 `process_env`。production config、Helm values/schema/guard、Pulumi 和 workload environment 都没有
用户 credential 或 refresh token。

- `webhook_swap`：execution gateway 解析 binding reference 并签发短期 placeholder；Policy Webhook 在真实
  HTTP 请求上完成 token swap；仅未来 webhook-enabled profile 可用；
- `process_env`：execution gateway 在精确 `shell + managed CLI + TAE process_start` 前先调用 Core
  `/internal/v2/execution/credentials:resolve-authority`，再调用
  `/internal/v2/execution/credentials:resolve`；Core 会复核 live operation、workspace binding、credential
  version、可执行文件和 argv policy 后返回对应 credential。`lark-cli` 注入
  `LARKSUITE_CLI_USER_ACCESS_TOKEN`，`bkectl` 注入 `BKECTL_JWT_TOKEN`；credential 只存在于该目标进程。
  当前 direct profile 使用 TAE 预置网络策略，不经过 egress-authorizer。

ByteCloud 授权由 Platform 的 `bytecloud` provider device flow 发起和轮询。Core 密封存储 OIDC JWT/access
token、refresh token 及各自 expiry，并在解析 binding 时于 Core 内刷新；sandbox 既不执行 `bkectl auth`，
也永远看不到 refresh token。一次合法 `bkectl` 进程只得到当前 access JWT 的副本。

Lark 的两种 mode 不复用 Sandbox profile。没有 `auto`、部署默认或运行时 fallback。当前 direct profile
遇到 Lark `webhook_swap` 必须拒绝；ByteCloud/bkectl 不支持 webhook。未来切换 profile 需要发布对应 TAE
policy/network evidence，不能运行时给 direct Sandbox 添加 webhook。

managed image/session runtime projection 只包含固定版本和 hash 的 CLI、非敏感 endpoint/config 与 TAE façade
地址。execution gateway 在合并完模型允许的 env 后最后注入 reserved value，名称冲突直接拒绝；相关
request/body/log/trace 必须 redact，不使用 TAE ZTI `user` 替代 agentserver actor。

没有所需 binding 时仍可创建 sandbox 和启动 run；`lark-cli` 可以在无 token 环境中返回未配置认证，
需要 ByteCloud JWT 的 `bkectl` 业务查询则在 process start 前 fail closed。Core direct resolve 用
`configured=false` 表达正常未配置状态，不回退到另一 mode。
binding 的 secret rotation 只推进 `credentialVersion`，不要求重建 TAE Session；revoke/owner/policy 变化推进
`authorityVersion`。`process_env` 会拒绝后续 process start；已经进入已启动进程内存的 token 字节不能远程
擦除或逐请求撤销。

### 11.2 `process_env` 的窄接口与安全边界

direct resolve 路由随 managed execution 一直挂载，只接受 executor-gateway 的精确 mTLS SPIFFE identity，
并要求：

1. 请求工具必须是 `shell`，executable/argv 必须命中固定策略：`lark-cli` 或只读 `bkectl` leaf command；
   target 必须是配置的 TAE PSM；
2. Core 按 consumer 映射选择 binding：`lark-cli -> kind=lark`，`bkectl -> kind=bytecloud`；ByteCloud binding
   必须来自 Platform OIDC device flow，不能用 `bkectl` kind、sandbox 本地登录或 managed AK/SK 代替；
3. Core 以该 binding/version 重做一次
   workspace membership、session/run/attempt lease、execution、`process_start`、sandbox generation 和 activity 校验；
4. provider adapter 只能返回对应的单个 header：Lark 为 `Authorization: Bearer`，ByteCloud 为
   `X-Jwt-Token`；Core 提取 token 后用
   `Cache-Control: no-store` 的响应返回 executor-gateway；
5. executor-gateway 只把 access token/JWT 合并进该次 TAE StartProcess env，禁止调用方覆盖保留变量；
6. Core 写 `stage=process_env` 的最小化 audit，audit 失败则不启动进程；
7. direct profile 不签发 `LARKSUITE_CLI_AGENT_TRACE`，也不投射 placeholder signer/keyring。

真实 token 会对目标进程及其子进程可见，这是该模式的明确安全边界。TAE 系统 policy 使用预置的
Lark/ByteCloud 网络边界；长运行进程中的 token 字节无法擦除，因此 access token 必须短期、最小 scope，
命令应保持短生命周期。refresh token 始终只驻留 Core。

### 11.3 独立 webhook-enabled profile（未来）

以下逻辑只属于未来 `webhook_swap` profile，当前 direct profile 不部署或调用它。egress-authorizer 在总预算内依次执行：

1. 限制 body/header 大小，严格解析 TAE Webhook v1；
2. 用官方 SDK 验证 `X-Zti-Token`，校验 SG trust domain 和允许的固定 TAE PSM；
3. 从原始 request headers 读取签名 placeholder，严格验签、expiry 与 operation scope；
4. canonicalize host/path/method，拒绝 IP literal、混淆 host、未知 method、redirect 扩权和未审核 provider host；
5. 对匿名 host 按平台+workspace egress policy 判定；对带 credential requirement 的 host，通过 mTLS 调
   Core：placeholder 使用 `/internal/v2/egress/credentials:resolve`；
6. Core 再验证 placeholder，并 live-authorize workspace 当前 mode、membership、run/attempt/operation、
   binding `authorityVersion`/`credentialVersion` 与 pack policy；
7. Core 从 workspace binding store 解封 credential，由 provider adapter 生成
   closed-world header mutation（例如 `Authorization`、`X-Jwt-Token`），绝不返回任意 header 名；
8. egress-authorizer 只返回合法的 `allow|deny` wire 和 mutation，Core 写入 credential-use/egress audit；
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
| `lark` | `Authorization: Bearer` / `LARKSUITE_CLI_USER_ACCESS_TOKEN` | Platform device flow；Core 密封并刷新 access/refresh token |
| `bytecloud` | `X-Jwt-Token` / `BKECTL_JWT_TOKEN` | Platform OIDC device flow；Core 密封并刷新 JWT/refresh token；`bkectl` 只是 consumer |
| `github` | bearer/PAT | 当前 production registry 只启用 static；App installation 需要配置显式 minter 后才会出现在 schema |

provider adapter 不能扩大网络 host；新增 host 需要平台审核 TAE policy/egress zone。workspace 上传的
arbitrary `server_url` 只能作为经过 SSRF、zone 和 provider allowlist 校验后的 metadata，不能成为放行条件。

### 11.5 审计与秘密驻留

direct profile 的 credential-use event 记录 workspace/run/execution/operation/target/provider kind/binding ID、
policy/credential versions 和 process-start decision；webhook profile 另记录 host/method/path/latency。
绝不记录 placeholder/真实 credential、Authorization 原文、AK/SK、JWT、PAT、body 或响应内容。
`webhook_swap` 的真实值只短暂存在于 sealed DB（密文）、Core/egress-authorizer/TAE header mutation 与
上游连接内存；`process_env` access token 会存在于目标 `lark-cli` 或 `bkectl` 进程及子进程的 env/内存，
refresh token 不离开 Core。两种模式都不得把值
写入 TAE Session metadata、文件、MCP result、日志或 checkpoint。

### 11.6 SG 硬门禁

当前 direct profile 上线前逐项产生自动化/只读证据：

| Gate | 通过条件 |
|---|---|
| G1 system policy | TAE system policy readback 覆盖审核过的 Lark/ByteCloud endpoint，Sandbox/revision/Session 无 webhook |
| G2 exact process | 只有 exact live `shell + approved managed CLI argv + TAE process_start` 能 direct resolve |
| G3 profile fence | Lark `webhook_swap` 在 direct profile 明确拒绝；ByteCloud 只允许 `process_env`，无自动 fallback |
| G4 host boundary | 非审核目标被 TAE 系统 policy 拒绝，CLI/credential 不能扩大网络边界 |
| G5 CLI acceptance | pinned Linux/amd64 lark-cli 与 bkectl artifact 校验通过；真实 workspace 验收包含 ByteCloud 查询 |
| G6 fail-close | Core/credential/audit 依赖故障时不启动新进程 |
| G7 secret residency | 除目标进程/子进程 env/内存外，fs/stdout/stderr/TAE metadata/Pulumi/Helm/日志/checkpoint 零 token |

任何门禁失败都不得自动切到另一 mode。未来 webhook profile 需要另行关闭 placeholder/header mutation、
ZTI、逐请求 revoke、latency 和 no-bypass 门禁，不能引用 direct profile 的证据。

### 11.7 写操作

当前 direct profile 只授权固定的只读 Lark CLI 与 bkectl leaf command。未来写操作必须在命令发出前由 execution
gateway/Core 完成交互审批，并设计可验证的 request-level authority；在此之前不得宣称支持受控写操作。

## 12. 安全边界

### 12.1 Capability 分离

| Capability | 持有者 | Audience / scope |
|---|---|---|
| execution MCP | worker | execution gateway；冻结 run/attempt/environment/catalog |
| lifecycle | execution gateway | sandbox-gateway ensure/renew/release/delete；绑定当前 MCP run-attempt，不含任意 provider 参数 |
| backend dispatch | execution gateway | sandbox-gateway 单 operation/target generation/action |
| egress placeholder（仅 `webhook_swap`） | sandbox process | egress-authorizer 单 actor/pack/grant/version/host class；无真实 secret |
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
- credentials：记录 direct resolve 的 reason class、Core latency、credential-not-ready；未来 webhook profile
  另记录逐请求 allow/deny 与 Webhook latency；
- capacity/cost：active TAE sessions、CPU/memory profile、idle duration、create cold-start。

trace 使用 run/execution/operation/internal sandbox ID/target generation/provider request log ID 关联；provider
ID 仅进入访问受限字段。日志 sanitizer 在单测中用 canary secret 验证。

stock app-server 或 worker cleanup 失败时，user-visible `turn_terminal` 只携带分类和诊断 SHA，避免把模型
内容或 credential 写入 durable transcript；但 SHA 不能成为唯一诊断。worker 必须在 terminal ACK 前写一条
结构化 ERROR 日志，包含 workspace/session/run/attempt/thread/turn、失败 phase、原始 `turn.error`、每个失败的
cleanup stage 和 bounded app-server stderr。文本按字段限制长度并标记 `*_bytes`、`*_log_truncated`、
`*_redacted`，credential/JWT/Bearer 必须脱敏。harness-pool 必须显式转发 worker stderr 到容器日志；不得依赖
`os/exec` 的 nil stderr（该配置会把唯一详细诊断丢弃）。

首批 SLO 候选（上线前用 SG PoC 数据定最终值）：

- ready sandbox 上 command dispatch begin→ack P99；
- cold ensure ready P50/P95/P99；
- command terminal success/error 正确率与 unknown rate；
- orphan 最长存活时间；
- `process_env` direct resolve 必须在 process-start budget 内 fail closed；未来 webhook profile 单独制定 P99；
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
- workspace mode create/update/CAS/audit、profile mismatch、无 fallback、部署中无全局 mode 与 BYO no-op。

### 15.2 Integration/fault injection

至少注入：

- create 请求发送前失败、响应丢失、重复 provider resource；
- Core reserve 后 sandbox-gateway crash；provider create 后 Core CAS 前 crash；
- begin-dispatch commit 后 execution gateway crash；
- process accepted 前/后断流、`process/connect` 重放/丢失、畸形 SSE、terminal 丢失；
- timeout 与自然 exit 竞态、terminate unknown；
- TTL renew 响应丢失、delete 404/5xx/timeout、reconciler 多副本抢 lease；
- target generation 被替换后的迟到 ACK/output/terminal；
- Core/credential cache 不可用；direct resolve timeout；未来 webhook profile 的 500ms 与非 200；
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
- `lark-readonly` runtime projection 只作为首个 provider fixture，direct process env 与动态 binding 选择；
- direct credential-use 审计；未来 webhook profile 再接 egress-authorizer/egress 审计；
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
4. harness-pool 不再持有 sandbox lifecycle 密钥，也不在 managed launch 前创建资源；execution gateway
   仅在该 MCP session 第一次实际使用 executor tool 时 ensure/renew，并在 MCP session 结束时 release activity；
   worker 仍只连接 execution gateway；
5. sandbox-gateway 已提供 provider-neutral lifecycle/commands/files HTTP contract 和 fake TAE provider，
   `cmd/sandbox-gateway --insecure-dev` 可运行完整本地链路；
6. 首个 Lark slice 已实现 workspace 级 mode 与 `process_env` exact process direct injection；当前 direct
   profile 遇到 `webhook_swap` 明确拒绝，且无部署默认或自动 fallback。真实凭据只进入目标
   `lark-cli`/子进程，不进入文件或 provider metadata；
7. fake-provider golden E2E 已覆盖 managed shell、process env 注入、TAE HTTP backend、sandbox-gateway、
   stdout/terminal 回传，同时保留 BYO agentx contract/regression tests。

生产 provider 的依赖边界仍然有意保持在独立 module，但它已经接入发布构建：

- `providers/tae` 通过官方 Sandbox SDK 固定每个 profile 的 CN/BOE/I18N-BD/I18N-TT 控制面，并以严格
  TLS、region-scoped ByteCloud 应用 JWT、HTTP/SSE 数据面适配
  实现 Create/Get/Search/TTL/Delete、进程流、terminate 和受限文件读取；process start 使用
  `x-tt-logid` 透传内部关联 ID，
  断流重连后的输出不会被误标为完整；
- `cmd/egress-authorizer` binary 保留供未来 webhook-enabled profile 使用；当前 direct profile 的 renderer
  不部署它或其 Service/Route/TLS/NetworkPolicy；
- 生产镜像构建会编译 `providers/tae/cmd/sandbox-gateway`，把该 binary 与 core/executor/egress
  service binary 一起放入 service image；主 module 中的 provider-neutral `cmd/sandbox-gateway`
  仍只允许 fake/insecure-dev，避免 production 意外退化到 fake provider；
- production renderer 不读取 workspace mode：direct active 只生成 sandbox-gateway，不生成 egress-authorizer
  authority；production schema/Helm/Pulumi 明确拒绝全局 mode 字段。
- production release 使用 `disabled` / fail-closed `policy-bootstrap` / `active` 三阶段；readback 后为每个
  已安装地域生成独立 `tae-network-probe` Job/NetworkPolicy，通过该 profile 的 Merlin 或 BOE direct route
  执行 20 次 JWT/control 检查、完整 lifecycle、pinned CLI/Skill 摘要校验与资源清理。
  `activate-managed-sandbox-profiles` 必须一次提交全部地域报告并核对 policy revision 与显式 profile
  authority；报告文件 SHA 由命令读取文件后计算，不生成组合摘要。

因此当前完成度应表述为“代码与 provider-linked production vertical slice 已完成，本地门禁通过；真实 SG
TAE/provider/credential deployment gates 尚未关闭”。在第 18 节证据齐全前，不得宣称 production-ready，也不得宣称
G1–G7 已通过真实网络和凭据链路验证。

## 18. 上线前待验证项

以下不是开放架构选择，而是需要在目标集群/控制面产生证据后才能关闭的实施门禁。代码接入和本地
contract tests 不替代这些实测：

- 四个 TAE region/PSM、SDK authority、应用账号 AK/SK 权限和 ByteCloud JWT/TAE ACL；
- TAE Terminal Sandbox 默认仅支持 IPv6；必须在 SG 实测 `open.feishu.cn` 的 AAAA/DNS、系统 policy
  转发、PMTU/MSS 和长响应链路，不能用办公网 IPv4 或 fake provider 结果替代；
- CreateSession 幂等/metadata 查询能力以及 provider request ID 语义；
- streaming start 的精确 ACK 边界、process query/terminate 能力和 SDK retry 默认值；
- TTL 最小/最大/更新语义、delete 后 GetSession 行为、list/orphan 能力；
- Terminal file read 对 symlink/special file 的实际处理；
- direct profile 关闭 G1–G7，验证 direct-injection、target-process residency、无 proof/header mutation、
  system policy readback 与 live mode/version fencing；
- pinned `lark-cli` 的安装，以及所选 mode 的 placeholder 或真实 token 配置；
- regional credential materialization/cache 在 P99/撤销延迟/secret residency 之间的最终参数；
- SG 容量、冷启动、并发 session 配额和成本基线。

还必须补齐本次发布的运行证据：

- PostgreSQL migration `0020`→`0027` 在真实库执行并可重复 bootstrap；
- 每个已安装地域的 TAE create/adopt/reconcile/TTL/delete 在丢响应、重复资源、generation fence 和超时下的结果；
- Sandbox/revision/Session readback 无 webhook，TAE system policy 包含 `*.feishu.cn`；`process_env` 证明
  direct resolve 只发生于 exact live `lark-cli` start；
- IPv4/IPv6、DNS、redirect、CONNECT、IP literal bypass、PMTU/MTU/MSS 和错误率复测；
- CN/BOE/I18N-BD/I18N-TT sandbox-gateway 分别按 profile site/JWT endpoint 换取 JWT、强制刷新、Secret
  轮换和 AK/SK/JWT 零泄漏扫描；CN/i18n-bd/i18n-tt 必须证明只走指定 Merlin，BOE 必须证明只走 direct
  双栈 allowlist；
- 所选 mode 的延迟/fail-close、TAE/Lark golden E2E 与 secret residency 扫描；`process_env` 允许目标
  `lark-cli`/子进程 env 命中，除此之外 env/proc/fs/stdout/stderr/metadata/checkpoint/log 必须零 token。

任何一项未知都应在对应 phase fail closed；不能用 provider 文档中的示例代替生产区域实测。

## 19. 官方参考

- [TAE SDK 参考](https://cloud.bytedance.net/docs/tae/docs/6889db56aefa690547a42112/69ae8d2a7358a0054a3e2532)
- [使用 Terminal Sandbox](https://cloud.bytedance.net/api/v1/cloud_developer/docs/tae/cn/68901fd9c95c14097673e2f6.md)
- [TAE 网络策略](https://cloud.bytedance.net/api/v1/cloud_developer/docs/tae/cn/69df71dd772b3e050be7e4e7.md)
- [Policy Webhook 接入规范](https://cloud.bytedance.net/api/v1/cloud_developer/docs/tae/cn/6a1018f89814db04f9e85ef2.md)
