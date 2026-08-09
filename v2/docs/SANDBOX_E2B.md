# 基于 agent-sandbox 的 E2B 兼容 sandbox 接口

> 历史方案：统一 execution gateway 与 E2B semantic subset 的方向被保留，但自建
> agent-sandbox/sandboxd 数据面已由 TAE backend 取代。当前 canonical 设计见
> [`TAE_MANAGED_EXECUTOR.md`](TAE_MANAGED_EXECUTOR.md) 与
> [ADR 0012](adr/0012-unified-execution-gateway-tae-backend.md)。

> 状态：实施方案（draft）
>
> 目标：在 kubernetes-sigs/agent-sandbox 之上，向 harness 提供一套 E2B 兼容的 sandbox 接口，
> 由 harness 管理 sandbox 生命周期。
>
> 关系说明：本文**取代** [`SANDBOX_POC.md`](SANDBOX_POC.md) 与 [`MANAGED_SANDBOX.md`](MANAGED_SANDBOX.md)
> 中"托管 sandbox 复用 agentx 作为接入方式"的部分（原因见 §1.1）。
> [ADR 0009](adr/0009-sandbox-egress-credential-proxy.md)（出口凭据替换）与
> [ADR 0010](adr/0010-tool-packs.md)（工具包）继续有效，本文直接复用。
> [ADR 0008](adr/0008-managed-sandbox-executor.md) 的 runtimeClass 结论（§7）继续有效。

## 0. 用户故事与验收

**故事**：用户在对话中要求查阅某篇飞书文档的内容。模型决定调用 sandbox 中的 `lark-cli`
去读取该文档；沙箱内只有占位凭据，真实凭据在出口网关被替换；文档内容返回给模型。

选这个故事作为首个切片有三个好处：它是**只读**的（不触发写审批，先把链路跑通）；
它必须走真实凭据（能证明替换链路）；它天然有界（文档内容可截断）。

**验收断言**

| # | 断言 |
|---|---|
| A1 | 模型看到的是 E2B 形状的工具（`commands_run` 等），不是现在的 `shell`/`argv` |
| A2 | harness 在 run 开始时创建 sandbox、结束时回收，全程无人工介入 |
| A3 | `lark-cli` 在 sandbox 内以**占位凭据**发出请求 |
| A4 | egress-gateway 完成替换，飞书返回真实文档内容并回到模型 |
| A5 | 扫描 sandbox 的 env、`/proc/*/environ`、文件系统、CLI 配置，**零真实凭据** |
| A6 | 两个并发 run 得到两个不同 sandbox，互不可见 |
| A7 | 该次调用在 Core 中有完整的 execution/operation 记录与出口审计 |

## 1. 关键决策

### 1.1 沙箱内跑 `sandboxd`，不跑 agentx

托管 sandbox **不复用** agentx。理由是 agentx 的复杂度全部来自一组我们在这里不具备的前提：

| agentx 的机制 | 存在的原因 | 在托管 sandbox 中 |
|---|---|---|
| enrollment + 双钥机器身份 + Hydra client | executor 是**用户自己的机器**，平台不可信任它 | sandbox 是我们创建的 pod，身份可以直接注入 |
| 出站 WSS + 30 秒 resume journal + generation fencing | 用户家里的网络会断 | 同集群 Service，可靠得多 |
| cgroup v2 guardian 事务 + 进程树回收 | 爆炸半径是用户整台机器 | 爆炸半径是这个 pod，pod 删除即清零 |
| owner policy 与远程策略取交集 | 机器主人有独立意志 | 没有第二个 owner |

E2B 的 `envd` 才是这个位置的正确形态：一个常驻沙箱内、暴露 process/filesystem 的小 daemon，
由控制面主动连入。我们写一个等价物 `sandboxd`。

**代价必须说清**：BYO executor 路径（agentx）与 sandbox 路径（sandboxd）从此是两条并行的
执行后端，长期都要维护。这是有意的——它们服务的信任模型本就不同。

### 1.2 MCP 面仍由 executor-gateway 提供

worker 连接的是**单一** MCP endpoint（`internal/harnessworker/worker.go:247-255` 从
`manifest.ExecutorMCP` 取 endpoint/namespace/catalogDigest，run-manifest schema 中
`executorMcp` 也是单个对象）。给 sandbox 新开一个 MCP server 就必须改 manifest schema、
worker 的 MCP 装配和 catalog 冻结逻辑，而这些都在 A11/A12 已关闭的门禁路径上。

因此：**sandbox 工具仍由 executor-gateway 通过同一个 MCP endpoint 暴露**，
executor-gateway 按环境类型把调用分发到 agentx（BYO）或 sandbox-gateway（托管）。
这样 worker、manifest、catalog 冻结机制**一行不改**，只有分发层增加一个后端。

### 1.3 生命周期由 harness-pool 管理，模型看不到

"harness 管理生命周期"落实为：**harness-pool** 在 run 边界调用 sandbox-gateway 的
E2B 兼容控制 API。模型侧的工具目录**不包含** `create`/`kill`/`pause`——
sandbox 是运行环境而不是模型的操作对象，让模型 kill 掉自己的环境只会丢工作。

pool 而不是 worker 持有这项权限，因为 pool 本来就是常驻的、持有 lease 的一侧，
而 worker 是每 attempt 新建的短命进程，不应获得可变更集群状态的能力。

### 1.4 首版 sandbox 是 run 作用域

若要 sandbox 跨 run 存活（真正的 pause/resume），下一个 run 的 pool holder 必须知道它存在，
这就需要 Core 持久化 session→sandbox 绑定。首版**不做**：sandbox 随 run 创建、随 run 删除，
`shutdownTime` 作为兜底 TTL。这让 harness 能独立管理生命周期，不需要新的 Core 状态。

session 作用域 + pause/resume 是明确的下一步，届时增加 Core 的绑定表并改用
`operatingMode: Suspended`。

## 2. 组件与拓扑

```text
                      harness-pool ──(2) 控制 API: 建/删 sandbox──┐
                           │                                      │
                    fork/exec                                     │
                           ▼                                      ▼
  模型 ──item/tool/call──► harness-worker ──MCP tools/call──► executor-gateway
                                                                  │
                                          ┌───────────────────────┴────────────────┐
                                   env kind=executor                        env kind=sandbox
                                          ▼                                        ▼
                                    agentx (WSS)                          sandbox-gateway
                                                                          ├─ 控制：agent-sandbox CR CRUD
                                                                          └─ 数据：HTTP → sandboxd
                                                                                   │
                                                              ┌────────────────────▼─────────────────┐
                                                              │ Sandbox（agents.x-k8s.io/v1beta1）   │
                                                              │  sandboxd :8080                      │
                                                              │  lark-cli / 工具链                    │
                                                              │  占位凭据 + HTTPS_PROXY + CA          │
                                                              └────────────────┬─────────────────────┘
                                                                               │ 出站 443
                                                                               ▼
                                                                   egress-gateway（ADR 0009）
                                                                     替换为真实凭据
                                                                               ▼
                                                                        open.feishu.cn
```

| 组件 | 新增/改动 | 职责 |
|---|---|---|
| **sandboxd** | 新增（沙箱内） | E2B `envd` 等价物：`commands.run`、`files.*`、`/health`、`/init`。无凭据、无集群访问 |
| **sandbox-gateway** | 新增（集群内） | 控制面：E2B 兼容的 sandbox 生命周期 API → agent-sandbox CR CRUD（唯一持 kube 凭据者）。数据面：把执行请求转给 sandboxd，并负责 Core 的 execution/operation 状态机 |
| **executor-gateway** | 改动 | 目录中增加 sandbox 工具；分发层按 env kind 选择 agentx 或 sandbox-gateway |
| **harness-pool** | 改动 | run 边界调用控制 API；持 `aud=sandbox-api` capability |
| **egress-gateway** | 按 ADR 0009 | 凭据替换，本文不重复 |

## 3. E2B 兼容面

### 3.1 命名对齐原则

对齐 **JS/TS SDK** 的命名（`cmd`、`cwd`、`envs`、`timeoutMs`、`background`、`user`），
因为 Python SDK 的 `timeout` 是秒、JS 的 `timeoutMs` 是毫秒，后者无歧义。
MCP 工具名用下划线形式（`commands_run`），namespace 取 `sandbox`，
于是模型看到的是 `sandbox.commands_run`，与它熟悉的 `sandbox.commands.run` 只差分隔符。

### 3.2 模型可见的工具目录（`sandbox-mcp/1.0`）

| 工具 | 入参 | 出参 | 落到 |
|---|---|---|---|
| `commands_run` | `cmd`(string, 必填)、`cwd`、`envs`、`timeoutMs`、`user` | `{stdout, stderr, exitCode, error, stdoutTruncated, stderrTruncated}` | sandboxd `POST /commands/run` |
| `files_read` | `path`、`format`(`text`\|`base64`) | `{content, truncated, sizeBytes, sha256}` | sandboxd `GET /files` |
| `files_write` | `path`、`data` | `{path, sizeBytes}` | sandboxd `PUT /files` |
| `files_list` | `path`、`depth` | `{entries:[{name,type,path}]}` | sandboxd `GET /files/list` |

首版**不含**：`background: true`、`commands_send_stdin`、`commands_kill`、`commands_list`、
`pty_*`、`files_watch`。它们需要进程句柄与 ownership/run-finalization 门禁
（[`ARCHITECTURE.md`](ARCHITECTURE.md) §8.2 对此有明确要求），与 `unified_exec` 一批做。

**刻意偏离 E2B 的四处**，每处都要写进工具 description，避免模型按 E2B 语义误判：

1. **无生命周期工具**（§1.3）。
2. **`stdout`/`stderr` 会截断。** E2B 是流式返回完整输出；我们的 harness result 上限为 2 MiB，
   超限时返回 `stdoutTruncated: true` 与字节数/SHA-256，不冒充完整内容。
3. **`cmd` 与 `argv` 二选一（`oneOf`）。** `cmd` 由冻结映射展开为 `["/bin/bash","-l","-c",cmd]`。
   注意这会使 `argv[0]` 恒为 bash，**任何基于 argv[0] 的二进制白名单策略从此失效**；
   保留 `argv` 入口是为了需要收紧时仍有精确形式。
4. **`user` 受限。** 执行身份由 environment 固定，只接受与其一致的值，其余拒绝。

### 3.3 harness 侧的控制 API（sandbox-gateway 提供）

E2B 兼容子集，供 harness-pool 调用，**不进入模型工具目录**：

```
POST   /sandboxes                 {templateId, timeoutMs, metadata, envs} → {sandboxId, state}
GET    /sandboxes/{id}            → {sandboxId, state, startedAt, endAt}
POST   /sandboxes/{id}/timeout    {timeoutMs}
DELETE /sandboxes/{id}            kill
POST   /sandboxes/{id}/pause      （预留，首版返回 501，见 §1.4）
POST   /sandboxes/{id}/resume     （同上）
```

映射到 agent-sandbox：`POST /sandboxes` → 创建 `Sandbox` CR（`spec.podTemplate` 由
sandbox profile 渲染，`lifecycle.shutdownTime` 由 `timeoutMs` 推导，
`shutdownPolicy: Delete`）；`DELETE` → 删除 CR；`GET` → 读 `status.conditions`
（`Ready`/`Suspended`/`Finished`）与 `status.serviceFQDN`。

## 4. 生命周期时序

```text
run 开始
  harness-pool: 若 run launch state 启用了 sandbox
    → POST /sandboxes {templateId, timeoutMs}            [aud=sandbox-api capability]
    → sandbox-gateway 创建 Sandbox CR + 注入配置的 Secret
    → 轮询至 status.conditions[Ready]=True
    → sandbox-gateway 调 sandboxd POST /init 下发运行时配置
       （占位凭据、HTTPS_PROXY、CA 路径、工作目录）
    → 把 (runId → sandboxId/envId) 交给 executor-gateway

run 期间
  模型 → commands_run → worker → MCP → executor-gateway
    → 解析 env kind=sandbox → sandbox-gateway → sandboxd

run 结束（正常/取消/失败，任一路径）
  harness-pool: DELETE /sandboxes/{id}
  兜底：Sandbox CR 的 shutdownTime 到期自动删除
```

**回收必须走 pool 已有的收口路径**，与 attempt 目录清理同一位置：正常完成、取消、
lease 丢失、holder 崩溃四条路径都要能删掉 sandbox。`shutdownTime` 是兜底而不是主手段——
仅靠 TTL 会在 pool 崩溃时留下最长一个 TTL 周期的资源占用。

## 5. 用户故事完整时序

```text
用户："帮我看看这篇飞书文档写了什么 https://xxx.feishu.cn/docx/doccnAbCd"

模型（已通过 baseInstructions 载入 lark skill，知道有 lark-cli）
  → item/tool/call: sandbox.commands_run({
        cmd: "lark-cli docx get --token doccnAbCd --format md",
        timeoutMs: 60000 })

worker → MCP tools/call → executor-gateway
  env kind = sandbox
  → PrepareExecution(runId, toolCallId)           [Core 持久化 execution]
  → 策略：read → allow（首版不弹审批）
  → PrepareOperation + BeginOperationDispatch      [Began=true 才允许发送]
  → sandbox-gateway POST /commands/run

sandbox-gateway → sandboxd（Service FQDN，mTLS）
sandboxd 执行：/bin/bash -l -c "lark-cli docx get ..."
  子进程环境（由 /init 下发，非模型可控）：
     LARKSUITE_CLI_USER_ACCESS_TOKEN = <占位：aud=egress-proxy capability>
     HTTPS_PROXY = egress-gateway
     SSL_CERT_FILE = /etc/ssl/agentserver/egress-ca.pem

lark-cli → HTTPS open.feishu.cn → 被 HTTPS_PROXY 导向 egress-gateway
  egress-gateway:
     验签 capability → Core authorize-egress（逐请求，不可达即拒绝）
     → host 命中 lark pack 规则（injector=bearer_swap）
     → 自有 CA 终止 TLS → 剥离占位凭据 → 注入 Core 解封的真实 user token
     → 系统信任根重连 open.feishu.cn（禁重定向）
     → 写 egress_audit_events

文档内容 → stdout → sandboxd 返回 {stdout, exitCode:0}
  → sandbox-gateway：CompleteOperation + execution terminal   [Core]
  → executor-gateway → MCP result（有界，超限截断并标记）
  → worker → app-server → 模型看到文档内容
```

## 6. 副作用边界不能因为换了后端就丢

这是本方案最容易出错的地方。sandbox-gateway **必须**复用 Core 已有的执行状态机，
而不是自己发明一套：

- 每次工具调用先 `PrepareExecution`，按 `(run_id, tool_call_id)` 幂等；
- 每个真实副作用步骤 `PrepareOperation` → `BeginOperationDispatch`，
  **只有一次性返回 `Began=true` 才允许向 sandboxd 发送**；
- 发送后崩溃/超时/连接不明 → operation 收敛为 `unknown`，**绝不自动重放**；
- 终态先写 Core，再作为 MCP result 返回 worker。

这些命令 Core 侧已经存在（`/internal/v2/executions:prepare`、`operations:prepare`、
`:begin-dispatch`、`:acknowledge`、`:complete`），sandbox-gateway 是新增的调用方，
不是新增的语义。审批（`policy=ask`）走同一条 `CreateApproval → elicitation → ConsumeApproval`
链路，首版对只读工具默认 `allow`，写工具接入时再打开。

## 7. sandboxd 设计

**定位**：沙箱内唯一常驻服务，是 E2B `envd` 的最小等价物。

- **协议**：HTTP/JSON over mTLS，固定端口。首版不做 Connect RPC / 流式——
  同步命令 + 有界输出即可满足验收，流式随 `background` 一起做。
- **表面**：`POST /commands/run`、`GET /files`、`PUT /files`、`GET /files/list`、
  `GET /health`、`POST /init`。
- **`/init`**：由 sandbox-gateway 在 Ready 后调用一次，下发运行时配置
  （占位凭据、代理地址、CA、默认 cwd）。**配置不写进 PodSpec env**，
  避免出现在 `kubectl describe`、审计日志与 etcd 中。这一点直接借鉴 E2B——
  它连沙箱自己的 access token 都只把 hash 经 MMDS 传进 guest，明文不入 guest。
- **不持有**：kube 凭据、Core 凭据、对象存储凭据、真实第三方凭据。它唯一拿到的敏感物
  是那枚绑定 attempt 的占位 capability，离开 egress-gateway 无效。
- **进程管理**：容器内需真实 PID 1 child reaper（pinned `tini`），
  sandboxd 负责把每个命令放进独立进程组并在超时时回收整棵树。
- **版本门控**：借鉴 E2B 的做法，sandboxd 每次行为变更 bump 版本，
  sandbox-gateway 按镜像记录的 sandboxd 版本做特性 gating——
  否则"沙箱内的 daemon 无法随控制面同步升级"会变成长期痛点。

## 8. 安全边界

- **runc / 共享宿主内核**（当前无 KVM，见 [ADR 0008 §7](adr/0008-managed-sandbox-executor.md)）。
  相比在 harness pod 内执行是明确提升（独立 pod、独立 NetworkPolicy、独立 PVC、
  爆炸半径限于该 pod），但不满足"经过独立安全评审的 sandbox backend"的实质要求。
  **受控环境与可信用户可用；面向不可信输入的多租户上线前必须回到 runtimeClass 问题。**
- **默认拒绝出网**：NetworkPolicy 只放行 egress-gateway。注意这里比 E2B 更严——
  E2B 的 nftables 默认 chain policy 是 ACCEPT（默认可上网），我们的威胁模型不允许。
- **入站**：只允许 sandbox-gateway 访问 sandboxd 端口，mTLS 双向认证。
- **低成本加固**（首版即落实）：非 root、只读 rootfs（`/workspace` 与 tmpfs 除外）、
  `seccompProfile: RuntimeDefault`、drop 全部非必需 capability、
  `automountServiceAccountToken: false`。
- **凭据不进 PodSpec**：见 §7 的 `/init` 说明。

## 9. 与既有约束的关系

本方案修改了两条 Phase 1 的表述，必须显式记录而不是默默偏离：

1. [`ARCHITECTURE.md`](ARCHITECTURE.md) §7.2 "Phase 1 只有 executor-gateway 可以向 worker 暴露工具"
   —— 仍然成立：sandbox 工具依然由 executor-gateway 的同一个 MCP endpoint 暴露（§1.2）。
2. [D6](ARCHITECTURE.md#14-关键决策) "`managed` 仅作为复用同一 agentx 协议的 Phase 2 扩展"
   —— **本方案改变了这一点**：托管 sandbox 不复用 agentx，改用 sandboxd（§1.1）。
   需要一份 ADR 记录该变更及其理由。

`tool_catalog_digest` 会因新增 sandbox 工具而变化；`thread/resume` 不重发 `dynamicTools`，
因此存量 thread 沿用旧目录，新目录只对新 thread 生效。目录改动应一次做完，避免多次 bump。

## 10. 分期

| 阶段 | 内容 | 验收 |
|---|---|---|
| **E0** | 事实核查：`lark-cli` 是否认 `HTTPS_PROXY`/`SSL_CERT_FILE`；占位凭据用哪一档（[`LARK_SLICE.md` §5](LARK_SLICE.md)）；agent-sandbox 是否已装 | 结论明确，pack 定义可写 |
| **E1** | sandboxd + 镜像 + Sandbox CR（手工 apply）→ `commands_run` 打通 | A1、A3 |
| **E2** | sandbox-gateway 控制面 + pool 生命周期接线 | A2、A6 |
| **E3** | egress-gateway 凭据替换 + lark pack | A4、A5 |
| **E4** | Core execution/operation 接线 + 出口审计 | A7 |
| **E5** | 端到端验收；再接一个 pack 验证扩展性 | 全部 |

E1 与 E3 可并行：egress-gateway 能脱离集群用 `curl` 独立验证。

## 11. 未决问题

1. **首个 sandbox 的冷启动延迟**：pod 调度 + 镜像拉取 + sandboxd 就绪的实测值未知。
   若不可接受，引入 agent-sandbox 的 `SandboxWarmPool`（属 `extensions` 组，迭代较快，
   需单独 pin）。
2. **跨集群**：本方案是入站模型，sandbox-gateway 需要能访问 sandboxd。同集群直接走
   Service FQDN；跨集群则需要在 sandbox 集群侧建入站网关（E2B 的做法是
   client-proxy + `E2b-Sandbox-Id` 路由头），或者由该集群部署一个出站拨号的 relay
   以保持零入站。这是最初需求之一，必须在 E2 之后给出明确设计。
3. **session 作用域与 pause/resume**：需要 Core 持久化 session→sandbox 绑定（§1.4）。
4. **两条执行后端的长期维护成本**（§1.1），以及是否最终要把 BYO 也迁到 sandboxd 形态。
