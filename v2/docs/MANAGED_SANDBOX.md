# agentserver v2 — 托管 sandbox 实施设计

> 历史方案：本文基于“sandbox 内运行 agentx”的旧拓扑。当前 canonical 设计见
> [`TAE_MANAGED_EXECUTOR.md`](TAE_MANAGED_EXECUTOR.md) 与
> [ADR 0012](adr/0012-unified-execution-gateway-tae-backend.md)；本文仅保留早期约束与推演。

> 状态：设计基线（draft）
>
> 本文是 [ADR 0008](adr/0008-managed-sandbox-executor.md)（托管 sandbox executor）与
> [ADR 0009](adr/0009-sandbox-egress-credential-proxy.md)（出站凭据替换）的实施细化。
> 两份 ADR 定义"为什么"和"边界"，本文定义"建什么"和"怎么验收"。
>
> 阅读前提：[`ARCHITECTURE.md`](ARCHITECTURE.md) §8（Executor 设计）、§9（agentx 接入协议）。
> 本文不重复既有 executor 链路，只描述增量。

## 0. 一句话概括

托管 sandbox 是一个 `kind=managed` 的 executor：agentx 跑在远端集群的 agent-sandbox Pod 里，
出站 WSS 反连 executor-gateway，因此 agentserver 与 sandbox 集群之间不需要任何入站连通性；
sandbox 内的工作负载没有直接外网能力，所有第三方请求经 agentx loopback 代理送到集群内的
egress-gateway，由后者用用户预存的真实凭据替换掉内部短期凭据后再发往真实上游。

## 1. 组件与拓扑

```text
┌──────────────────── agentserver 集群 ────────────────────┐
│                                                          │
│  agentserver-core ──┬── sandbox_pools / credentials      │
│        ▲            │   （sealed，独立 keyring）          │
│        │ mTLS       │                                    │
│  ┌─────┴──────────┐ │  ┌──────────────────┐              │
│  │ sandbox-       │ │  │ egress-gateway   │              │
│  │ controller     │ │  │ TLS 终止 + 凭据注入│              │
│  │ (唯一持 pool   │ │  │ 自有 CA 私钥      │              │
│  │  凭据者)       │ │  └────────▲─────────┘              │
│  └─────┬──────────┘ │           │                        │
│        │            │  ┌────────┴─────────┐              │
│        │            └──┤ executor-gateway │              │
│        │               │ MCP + agentx WSS │              │
│        │               └────────▲─────────┘              │
└────────┼────────────────────────┼────────────────────────┘
         │ ① kube-apiserver       │ ② 出站 WSS 443
         │   （控制面，非热路径）  │ ③ 出站 CONNECT 443
┌────────▼────────────────────────┼────────────────────────┐
│              sandbox 集群（可以是另一个集群）              │
│  ┌──────────────────────────────┼──────────────────────┐ │
│  │ Sandbox Pod (runtimeClass: gvisor/kata)             │ │
│  │  initContainer: 装 nftables 分 UID egress（NET_ADMIN）│ │
│  │  ┌────────────────┐                                 │ │
│  │  │ agentx (UID A) ├── loopback CONNECT proxy ───┐   │ │
│  │  │  持 capability │                             │   │ │
│  │  └───────┬────────┘                             │   │ │
│  │          │ stdio                                │   │ │
│  │  ┌───────▼──────────────┐   HTTPS_PROXY=127.0.0.1│   │ │
│  │  │ stock exec-server     │◄──────────────────────┘   │ │
│  │  │ + 用户命令 (UID W)     │  UID W 只能访问 loopback   │ │
│  │  └───────────────────────┘                          │ │
│  │  PVC: /workspace（用户工作树，跨 run 保留）           │ │
│  └─────────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────┘
```

三条跨集群链路的方向和用途必须分清：① 只由 sandbox-controller 发起，用于创建/暂停/删除
Sandbox 对象，不在 run 热路径上；② ③ 都由 sandbox 侧出站发起。**没有任何一条是 agentserver
向 sandbox 的入站连接。**

### 1.1 新增组件

| 组件 | 唯一职责 | 明确不负责 |
|---|---|---|
| **sandbox-controller** | 消费 `sandbox.*` outbox；持 pool 凭据调远端 kube-apiserver 创建/挂起/删除 Sandbox 与其 Secret/PVC；reconcile 与孤儿回收 | 不参与 run 数据面，不接触 workspace 第三方凭据，不代理任何流量 |
| **egress-gateway** | 校验 `aud=egress-proxy` capability 并逐请求 Core live-authorize；host allowlist；TLS 终止与重发起；凭据注入；effect class 分类与审批挂起；审计与配额 | 不做协议转换，不改写 body，不跟随重定向，不缓存授权结果，不持久化 body |

### 1.2 既有组件的增量

- **Core**：新增 pool/sandbox/credential/egress-policy 的状态与命令；`run_launch_states` 冻结
  sandbox profile；新增 `authorize-egress` 内部路由（只接受 egress-gateway 的 SPIFFE identity）；
  attestation 的签发与原子消费。
- **executor-gateway**：新增 `POST /internal/v2/agentx/attestations` 有界中继；在 WSS
  `welcome` 之后向 managed agentx 下发 attempt 作用域的 egress capability。
- **agentx（独立仓库）**：新增 managed 模式（attestation 启动、tmpfs 临时密钥、loopback CONNECT
  代理、向子进程注入 proxy 与 CA 配置、按受信策略应答 `network/policyRequest`）。
- **harness / harness-worker / llmproxy**：**无变更**。这是本设计的关键性质。

## 2. 数据模型

新增三个 migration（当前最新为 `0019_user_sessions.sql`）。

### 2.1 `0020_managed_sandbox.sql`

- `sandbox_pools`：`id`、`display_name`、`status(active|draining|disabled)`、`api_server_url`、
  `sealed_credential`、`credential_key_version`、`ca_bundle`、`target_namespace`、
  `runtime_class`、`agent_sandbox_api_version`、`max_concurrent_sandboxes`、`version`。
  凭据用独立 keyring 密封，AAD 绑定 `pool id + credential version`（ADR 0008 §5）。
- `sandbox_profiles`：`id`、`workspace_id`（NULL 表示平台全局）、`pool_id`、`image_digest`、
  `cpu/memory/storage` 请求与上限、`idle_suspend_seconds`、`max_lifetime_seconds`、
  `egress_policy_id`、`status`、`version`。profile 是 run launch 冻结的对象，必须版本化。
- `managed_sandboxes`：`id`、`workspace_id`、`session_id`、`executor_id`、`pool_id`、
  `profile_id`、`profile_version`、`persistence`（见 §2.3）、
  `status(provisioning|ready|suspending|suspended|resuming|deleting|deleted|failed)`、
  `remote_namespace`、`remote_object_name`、`remote_uid`、`pvc_name`、
  `attestation_secret_sha256`、`attestation_expires_at`、`last_active_at`、`shutdown_at`、
  `version`。`(session_id)` 上有唯一部分索引，保证一个 session 最多一个活跃 sandbox。
- 扩展 `executors`：新增 `kind text NOT NULL DEFAULT 'byo' CHECK (kind IN ('byo','managed'))`；
  状态 CHECK 增加 `'provisioning'`。既有的 `executors_connected_metadata_present` 等约束
  需要按 kind 放宽——managed executor 在 attestation 完成前没有 `machine_key_sha256`。

### 2.2 `0021_workspace_credential_bindings.sql`

- `workspace_credential_bindings`：`id`、`workspace_id`、`kind(github|gitlab|generic_bearer|…)`、
  `host`、`display_name`、`owner_scope(workspace|user)`、`owner_user_id`（owner_scope=user 时非空）、
  `auth_type(static|oauth_grant|app_installation)`、`sealed_secret`、`sealed_key_version`、
  `status(active|reauth_required|revoked)`、`version`。
- `workspace_egress_policies` / `workspace_egress_rules`：规则按 `(host_pattern, method, path_pattern)`
  匹配，产出 `effect_class(read|write)` 与 `decision(allow|ask|deny)` 以及要使用的 `binding_id`。
  规则集是 closed-world 且版本化；**未命中任何规则 = deny**，命中但方法未知的写方法归为 write。
- `egress_audit_events`：`id`、`workspace_id`、`run_id`、`execution_id`、`binding_id`、
  `host`、`method`、`path_digest`、`effect_class`、`decision`、`approval_id`、`status_code`、
  `request_bytes`、`response_bytes`、`started_at`、`ended_at`。带 retention。

**审计不进 canonical run event。** 一次 `npm install` 可以产生上千个 HTTP 请求，逐条写入
`run_events` 会淹没事件流与浏览器投影，并撑爆 outbox。设计上分成两层：逐请求事实进
`egress_audit_events`（可查询、有 retention）；每个 execution 结束时只产生**一条有界的聚合
canonical event**（触达的 host 集合、请求数、字节数、使用的 binding、审批 id 列表、拒绝数）。
只有触发审批的写请求才额外产生独立的 approval 事件——它本来就需要用户交互。

### 2.3 有状态与无状态：首版只做有状态，但现在就留缝

sandbox 在原理上有两类：**有状态**（绑 session、持有 PVC 工作树、pause/resume，承载连续长程任务）
与**无状态**（一次性、用完即弃、可从 warm pool 租用，承载轻量任务）。首版**只实现有状态**。

判断依据是无状态那一类的收益比直觉中窄：它的价值是**成本与延迟优化，而不是一道新的安全边界**。
最典型的"不可信代码"——`npm install` 的 postinstall 脚本、构建、测试——本身就必须运行在有状态的
工作树里，无法挪进一次性盒子；真正约束它们的是 egress-gateway 的 host 策略与写操作审批
（[ADR 0009](adr/0009-sandbox-egress-credential-proxy.md) §5），不是沙箱的生命周期形态。

留缝的做法（成本接近零，但能避免后续痛苦改造）：

1. `managed_sandboxes` 与 environment 投影从第一天就带 `persistence` 类别字段
   （`session | ephemeral`），并由 `list_environments` 投影给模型，使语义对模型可见；
2. 数据模型与授权路径**不得写死"一个 run = 一个 executor = 一个 env"**。特别注意两处：
   `managed_sandboxes` 上 `(session_id)` 的唯一索引应限定为 `persistence='session'`；
   以及 `aud=executor-mcp` capability 当前绑定**单个** `executorId`
   （[ADR 0002](adr/0002-production-run-capability.md)），无状态池要求把它放宽为绑定 `poolId`
   并由 Core 在 live-authorize 时解析当前租约——这是对 v2 门禁最密契约之一的改动，
   必须在引入无状态池时单独评审，不能顺手加字段。

无状态池排在 §10 的 S-E，且是**条件性的**：只有当有状态链路跑通、冷启动延迟有实测数据、
并且确实出现了它能解决的负载时才启动。它依赖的 `SandboxWarmPool`/`SandboxClaim` 属于
agent-sandbox 迭代更快的 `extensions` 组，也是推迟的理由之一。

**webfetch 不属于这个话题。** 抓取网页不需要沙箱：SSRF 靠 URL 校验与出口策略解决，
提示注入靠沙箱完全无效（内容照样进模型上下文），而纯 HTTP 拉取不执行任何代码。它应实现为
egress-gateway 内部 listener 上的一个第一方 fetch 端点（SPIFFE 门禁，复用 `internal/publichttps`
的私网/环回/元数据地址拒绝规则），由 executor-gateway 的目录条目做薄客户端调用，
冻结为恰好一个 `effect_class=read` 的 operation。带 JS 渲染的抓取是另一项独立能力，
到时候需要的是"浏览器 sandbox"这个具体形态，不是"无状态"这个抽象。

注意 webfetch 会是**第一个 workspace 作用域而非 executor 作用域的工具**，需要与
`list_environments` 同样的 `env_id` 豁免；这与上面 `poolId` 是同一形状的问题，
提示契约里可能需要显式的 `tool_scope ∈ {executor, workspace}` 概念，而不是逐个特例。

### 2.4 capability claim 扩展（契约变更，非 migration）

按 [ADR 0002](adr/0002-production-run-capability.md) 的 audience 分离原则新增第三种受众：

- `aud=egress-proxy`：在公共 run/attempt/actor/holder/deadline claim 之外，只增加
  `executorId`、`envId`、`egressPolicyId`、`egressPolicyVersion`。
- 它**不得**携带 model/provider authority（那是 `aud=llmproxy`），也不得携带
  `toolCatalogDigest`/approval TTL（那是 `aud=executor-mcp`）。三枚 token 互不通用。

## 3. 供应与运行时序

```text
① CreateRun（既有）
   Core 在同一事务冻结 run_launch_state：LLM gateway 绑定（既有）
                                      + sandbox_profile_id/version（新增）
   若该 session 需要 managed sandbox 且无 ready sandbox：
      同事务写 outbox kind='sandbox.provision'
   同事务写 outbox kind='run.queued'（既有）

② 两条链路并行推进
   harness-pool ← run.queued          （既有，完全不变）
   sandbox-controller ← sandbox.provision（新增）

③ sandbox-controller
   Core: 预建 kind=managed executor（status=provisioning）
       + 生成一次性 attestation secret（只存 SHA-256，TTL 5 分钟）
   远端集群: 创建 PVC → 创建 Sandbox（labels 见 §3.2）
           → 创建 ownerRef 指向该 Sandbox 的一次性 Secret

④ sandbox 内 agentx（managed 模式）
   tmpfs 生成临时 Ed25519 → 读 attestation secret
   → POST /internal/v2/agentx/attestations（executor-gateway 有界中继到 Core）
   → Core 原子消费 secret，绑定公钥，返回短期 asv2mgd1 连接 token
   → 立即删除 secret 文件
   → challenge + executor-wss-proof/ed25519-v1（与 BYO 完全相同）
   → hello/welcome → 声明 environment → Core 置 executor online / sandbox ready

⑤ 首个 tools/call 到达 executor-gateway
   若 sandbox 尚未 ready：在有界 readiness deadline 内等待
   超时 → execution 干净地进入 failed（未 dispatch，不是 unknown）
```

关键点：供应与 harness 启动**并行**，不串行叠加延迟；供应发生在 `turn/start` 被接受之前，
因此失败走既有的 `AbandonAttempt` 仲裁路径 requeue，不引入新的收口语义。

### 3.1 idle suspend 与 resume

`last_active_at` 超过 profile 的 `idle_suspend_seconds` 且该 session 无活跃 run 时，
controller 把 `operatingMode` 置为 `Suspended`。下一个 run 的 `sandbox.provision` 发现已有
suspended sandbox 时改为 resume。**挂起前必须确认无活跃 run 与无活跃 lease**，不能只看时间戳。

### 3.2 标签、所有权与孤儿回收

每个远端 Sandbox 必须带全套 label：`agentserver.io/deployment-id`（区分不同 agentserver 部署，
防止误删他人对象）、`workspace-id`、`session-id`、`executor-id`、`sandbox-id`。

reconcile 循环按 `deployment-id` 列举远端对象，与 Core 记录做双向差集：

- 远端有、Core 无（或 Core 记录已 terminal）→ 超过 grace period 后删除；
- Core 有、远端无 → sandbox 置 `failed`，executor 置 revoked，绑定其上的 execution 收口为
  `unknown`（如果曾经 dispatch 过）。

孤儿回收是硬性要求：泄漏的 sandbox 既产生成本，也持有一个能连回 executor-gateway 的身份。

## 4. sandbox 镜像与 Pod 规格

### 4.1 镜像分层

镜像内必须区分两棵树，因为它们的变更节奏和验收要求完全不同：

- **runtime 树（安全关键，锁定）**：agentx、pinned stock Codex、`codex-resources/bwrap`、
  pinned `tini`（真实 PID 1 child reaper，[`ARCHITECTURE.md`](ARCHITECTURE.md) §8.1 要求）、
  egress CA bundle。必须满足 agentx `5d40b6b` 的安装合同：从 `/` 到 runtime root 的完整路径
  由 `0/0` 持有、不可 group/other 写、无 symlink/setuid/setgid。digest 逐文件校验。
- **toolchain 树（面向用户，可迭代）**：git、gh、node、python、构建工具等。它的更新不应
  重新打开 runtime 门禁，但每次变更仍需重新跑镜像级 provenance 检查。

生产目标平台是 `linux/amd64`。既有的 E09/A12 门禁只在 `linux-arm64` 上关闭，
[`ARCHITECTURE.md`](ARCHITECTURE.md) §7.2/§8.1 明确要求 amd64 必须在 native worker 重跑——
sandbox 镜像门禁正好承担这次 amd64 验收，不是额外负担。

### 4.2 Pod 规格要点

- `spec.podTemplate.spec.runtimeClassName` 必须显式设置（gVisor/Kata），生产禁止缺省 runc；
- initContainer（唯一持 `NET_ADMIN`）安装 §5 的分 UID nftables 策略后退出；
- 主容器需要 agentx Linux 生产模型要求的 `CAP_CHOWN|CAP_SETUID|CAP_SETGID`
  （root connector）与 cgroup v2 委派，其余 capability 全部 drop；
- `/workspace` 挂 PVC（用户工作树，跨 run 保留）；其余路径尽量只读或 tmpfs；
- `spec.lifecycle.shutdownTime` 设为 profile 的 `max_lifetime_seconds`，
  `shutdownPolicy: Delete`，作为 controller 失联时的兜底 TTL；
- 不注入任何 ServiceAccount token（首版），`automountServiceAccountToken: false`。

## 5. 网络边界（三层）

| 层 | 机制 | 约束 |
|---|---|---|
| L1 进程身份 | Pod 内 nftables 分 UID（复用 `internal/networkguard` 的 `UIDPolicy`） | exec-server/子进程 UID 只允许 loopback；agentx UID 允许出站 443；两个 UID 的 IPv6 全 drop |
| L2 Pod | NetworkPolicy（可由 `SandboxTemplate.spec.networkPolicy` 托管） | 只放行到 executor-gateway / egress-gateway 的 443；显式排除远端集群 apiserver、云元数据地址与私网段 |
| L3 应用 | egress-gateway 的 host allowlist + 规则集 | 默认拒绝；命中规则才放行；写操作按策略进审批 |

`internal/networkguard` 现有的 `Endpoint` 是显式 IPv4 地址，适合 harness 的固定 ClusterIP 场景。
sandbox 侧 agentx 需要访问的是公网 host，其 IP 可能变化，因此 L1 的职责收敛为
**"把工作负载 UID 钉死在 loopback"**，agentx UID 的目的地约束交给 L2/L3。这个分工要在实现中
明确，不能假装 L1 能做 FQDN 级管控。

## 6. 凭据替换的请求路径

```text
sandbox 内: git push https://github.com/org/repo
  → HTTPS_PROXY=127.0.0.1:PORT
  → CONNECT github.com:443（无凭据）
agentx: 附加 Proxy-Authorization: Bearer <aud=egress-proxy capability>
  → CONNECT 到 egress-gateway（TLS）
egress-gateway（github.com 在策略中带 binding_id，因此走模式 B）:
  1. 本地 Ed25519 验签 capability（asv2cap1 / ADR 0002 keyring）
  2. Core authorize-egress（逐请求，无缓存，不可达即 fail closed）
  3. host allowlist 校验 → 用自有 CA 签发 github.com 叶证书，终止 TLS
  4. 解析请求 → 规则匹配 → effect_class=write → 触发 Core approval，挂起等待
  5. 获批后：剥离入站全部认证 header，Set 注入真实凭据
     （static / 刷新后的 oauth token / 现铸的 App installation token）
  6. 系统信任根重新发起到 github.com 的 TLS，正常校验证书链与主机名
  7. 禁止 3xx 跟随；请求/响应 header 白名单；body 不落盘不记日志
  8. 写入 egress_audit_events
```

sandbox 侧看到的凭据自始至终只有那枚绑定 attempt 的短期 capability；真实凭据只在
Core（密文）与 egress-gateway（请求期内存）中存在。

`registry.npmjs.org`、`pypi.org` 这类只需要匿名网络的 host 走 ADR 0009 §3 的**模式 A**：
只做 CONNECT 层 host allowlist 与配额，不终止 TLS、不看明文、不需要 CA。
只有策略中显式带 `binding_id` 的少数 host 才进入上面的模式 B 流程。

### 6.1 GitHub 的推荐形态

三种 `auth_type` 都支持，但 GitHub 应优先推荐 **App installation**：权限可按仓库收敛、
令牌天然 1 小时过期、撤销即时生效。static PAT 作为兜底，必须在 UI 上标注其风险更高。

## 7. 工具面扩展：`executor-mcp/1.2`

当前 `executor-mcp/1.1` 只有 `list_environments | shell | read_file`。托管 sandbox 让
"执行更多操作"成为产品目标，按 [`ARCHITECTURE.md`](ARCHITECTURE.md) §8.2 的既定规划扩展：

| 新增工具 | 依赖 |
|---|---|
| `unified_exec` | run 内长生命周期 `process/start`；必须与下面三项同版本发布 |
| `write_stdin` / `read_output` / `terminate` | §8.2 明确它们"不能在没有 handle-producing tool 时作为孤立工具加入"，且需要 ownership 与 run-finalization 门禁 |
| `apply_patch` | 需要协商 `agentserver/fsWriteFileIfMatch` 条件写扩展；单文件、precondition hash |

managed 环境相对 BYO 的策略差异只有两点，且都必须是 profile 上的显式字段而非隐式行为：
网络经代理放行（BYO 默认仍拒绝），`shell` 默认 `allow`（BYO 默认 `ask`，见 ADR 0009 §5）。

**catalog digest 变更即新 thread。** `thread/resume` 没有 `dynamicTools` override，
所以升到 1.2 之后，已有 checkpoint 的 session 必须继续用其冻结的旧 catalog，
新 catalog 只对新 thread 生效。这是既有约束，不是新增限制，但升级节奏必须据此规划。

## 8. 失败与收口

| 场景 | 语义 |
|---|---|
| 远端集群不可达 / 供应超时 | 发生在 `turn/start` 前 → `AbandonAttempt` → run 回 `queued`；有界重试后置 `failed` 并给出明确原因，**不是** unknown |
| sandbox 中途被驱逐/OOM/删除 | WSS 断开 → 既有 30 秒 resume 窗口 → 超时后已 dispatch 的 execution 收口为 `unknown`，与 BYO executor 完全同构 |
| 新 sandbox 顶替旧 sandbox | 是**新的 executor 身份**，绝不继承旧 process handle；旧 execution 保持 unknown |
| egress-gateway 不可用 | 出站请求失败 → shell 命令拿到真实非零退出码 → execution 有真实终态。不产生 operation 层 unknown |
| 审批中的写请求超时/断连 | egress-gateway 必须记为 ambiguous 并写审计；不得自动重发 |
| Core 不可达 | egress 与 MCP 均 fail closed |

需要特别标注的既有风险：`run` 失败路径的收口在当前实现中已有缺口（accepted turn 之后 pool
无终止命令、checkpoint 上传失败会让 run 永久 `finalizing`）。托管 sandbox 新增了
"供应超时"这一失败模式，它必须走既有的 lease/generation 仲裁而不是新开一条路径；
在实现前应先确认该收口缺口已修复，否则会放大既有问题。

## 9. 测试门禁

沿用仓库既有的编号门禁风格，新增 S 系列。**S-01 是其余全部工作的前置条件。**

- **S-01 runtimeClass 兼容性**：文献调查部分**已完成**，结论是排除 gVisor、选用 Kata/Firecracker，
  依据与三处不可回避的缺失见 [ADR 0008 §7](adr/0008-managed-sandbox-executor.md)。
  剩余的实测部分：在目标 Kata 环境的 guest 内验证 (a) bubblewrap 的 user namespace + seccomp
  实际可用（含 `--unshare-net`）；(b) cgroup v2 完整事务链（`CLONE_INTO_CGROUP` →
  `cgroup.freeze` → `cgroup.kill` → `populated=0`）；(c) `openat2` 全部所需 `RESOLVE_*`；
  (d) `close_range`；(e) **`nft ... meta skuid` 规则可实际装载**——这一项在 Kata 的内核
  fragment 中未能直接确认，若不可用则 `internal/networkguard` 需要一条
  `iptables-legacy -m owner --uid-owner` 的等价路径。
- **S-02 跨集群供应**：在与 agentserver 不同的集群上完成 provision → attestation → WSS →
  `shell` 往返 → delete；全程 agentserver 侧无任何入站连接。
- **S-03 attestation 安全性**：secret 单次消费、过期拒绝、重放拒绝、无 Ed25519 持有证明时
  仅凭 secret 无法建立 WSS、executor revoke 后立即失败。
- **S-04 凭据不泄漏**：扫描 sandbox 内全部环境变量、进程 `/proc/*/environ`、文件系统、
  exec-server 配置与 stdout/stderr，断言真实凭据零出现；只允许 capability 出现在 agentx
  进程内存。参照既有 A11 的扫描方法论。
- **S-05 网络边界**：工作负载 UID 对公网、对 egress-gateway 直连、对远端集群 apiserver、
  对云元数据地址全部零命中；仅 loopback 可达。含 IPv6 与 DNS 形状的 UDP 负向用例。
- **S-06 凭据替换正确性**：真实 GitHub（或高保真 fixture）上完成 clone/push/PR 创建；
  断言上游看到的是真实凭据、sandbox 侧发出的是 capability、3xx 不被跟随、
  header 白名单生效。同时断言**模式 A 的 host 不被 TLS 终止**——sandbox 侧观察到的
  证书链必须是上游真实证书而非 egress CA 签发，防止实现把两种模式混同。
- **S-07 审批链路**：write effect class 触发审批；批准后执行且只执行一次；拒绝/过期/取消
  路径不产生副作用；断连收口为 ambiguous。
- **S-08 孤儿回收**：Core 记录删除后远端对象被回收；带其他 `deployment-id` 的对象不被误删；
  controller 重启后从 label 重建视图。
- **S-09 收口矩阵**：供应超时、sandbox 中途删除、WSS resume 过期、egress-gateway 挂掉，
  各自产生上表规定的确切状态，且不自动重放副作用。
- **S-10 版本锁定**：agent-sandbox CRD golden fixture 的 drift 检查；镜像内 runtime 树的
  逐文件 digest 与安装合同校验（root-owned、无 group/other 写、无 symlink/setuid）。

## 10. 分期

| 阶段 | 内容 | 退出条件 |
|---|---|---|
| **S-A 可行性** | S-01 的剩余实测（Kata guest 内逐项验证）；**agentx managed profile 的设计与安全评审**（ADR 0008 §7.1，跨仓，原不在计划内） | Kata 上五项实测全通过；managed profile 明确哪些机制由外层沙箱承担、哪些仍强制 |
| **S-B 供应链路** | sandbox-controller、pool/sandbox 数据模型、attestation 身份、Pod 规格与镜像；**无外网** | S-02/S-03/S-05/S-08/S-10 通过；现有 1.1 工具在 sandbox 内可用 |
| **S-C 凭据替换** | egress-gateway、credential bindings、egress policy、GitHub 只读 | S-04/S-06 通过 |
| **S-D 写与审批** | effect class 分类、写操作审批、App installation token、更多 provider | S-07/S-09 通过 |
| **S-E 体验与容量** | 工具面 1.2、idle suspend/resume、WarmPool（若冷启动 SLO 有实测缺口）、配额与成本控制 | 冷启动与并发容量有 SLO 实测数据 |

S-A 不通过则整个方案的形态需要重新评估，因此不要在 S-A 之前并行开工 S-C/S-D。

首个具体目标（飞书 CLI 端到端）的验收定义、逐组件工作分解与里程碑见
[`LARK_SLICE.md`](LARK_SLICE.md)；它把上面的 S-B/S-C/S-D 落到一条可验收的产品链路上，
并给出扩展到 `bytecloud-cli` 等后续工具包所需的分诊表。

## 11. 可观测性与配额

日志关联字段在既有集合上增加：`sandbox_id`、`pool_id`、`binding_id`、`egress_policy_version`。

必须建设的指标：供应延迟分段（`provision 请求 → Sandbox 创建 → Ready → WSS online`，
分段记录，不用单一总值掩盖瓶颈）、suspend/resume 延迟、供应失败率按原因分类、
孤儿回收计数、egress 请求 QPS/字节/拒绝数/审批等待时长、凭据刷新失败数、
每 workspace 活跃 sandbox 数与生命周期分布。

配额必须在首版就有，否则托管形态的成本不可控：每 workspace 并发 sandbox 上限、
sandbox 最大生命周期、idle suspend 时限、每 run 与每 workspace 的 egress 字节配额。

## 12. 未决问题

1. ~~S-01 的结论~~ —— 已闭合：排除 gVisor，选 Kata/Firecracker（[ADR 0008 §7](adr/0008-managed-sandbox-executor.md)）。
   派生出两项新工作：**agentx managed profile**（§7.1，跨仓）与 **KVM/嵌套虚拟化的可得性**——
   后者是新的部署前提，需确认目标集群的节点类型支持嵌套虚拟化，这会影响 sandbox pool 的选型与成本。
2. **PVC 的持久化边界**：session 删除后工作树保留多久？用户能否下载？跨 session 复用同一
   工作树是否是产品需求？这直接影响存储成本模型。
3. **executor-gateway 单副本瓶颈**（[D15](ARCHITECTURE.md#14-关键决策)）：托管 sandbox 会显著
   增加常驻 WSS 连接数，Phase 2 的 owner routing 何时必须启动需要容量数据支撑。
4. **egress CA 轮换**与 sandbox 镜像内 CA bundle 的更新节奏、过渡窗口。
5. **多 pool 调度策略**：按 region、按容量还是按 workspace 亲和；首版可固定单 pool。
6. **agentx 跨仓协同**：managed 模式是 agentx 仓库的重大增量，需要按
   [`ARCHITECTURE.md`](ARCHITECTURE.md) §13.1 先冻结 versioned schema 与 fixture 再并行开发。
