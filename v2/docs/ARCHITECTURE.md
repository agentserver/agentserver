# agentserver v2 — 架构设计

> 状态：设计基线（draft）
>
> v2 从零开始开发，不复用 v1 的运行时组件；集群侧代码位于本仓库 `v2/` 下。用户侧 agentx 继续在 `github.com/agentserver/agentx` 独立开发和发行，本仓库保存它所实现的 wire contract、兼容 fixture 与 runtime lock manifest。
>
> 本文中 stock Codex 有两个严格分离的运行角色：大脑由 per-run `harness-worker` 通过 stdio 驱动 stock app-server；双手由 agentx 监管本地 `codex exec-server --listen stdio`。app-server 子进程只运行模型循环并调用批准的 MCP；exec-server 只处理确定性的 process/fs JSON-RPC，不运行模型。两侧的宿主进程都不替模型推理。

## 0. 架构约束

以下约束是 v2 的设计前提，不是实现选项：

1. **大脑与双手分离**
   - harness 是大脑：运行 stock Codex 的模型循环。
   - executor 是双手：只执行结构化、确定性的 process/fs 指令。
   - 两者只通过 MCP 工具边界连接。
2. **双手没有模型**
   - agentx 不接受自然语言 prompt，不做推理，不访问 llmproxy，也没有任何模型凭证。
   - 标准 process/fs 执行由 pinned stock `codex exec-server --listen stdio` 完成；agentx 不在 Go 中重写这套 handler。
   - executor 侧禁止使用 `codex exec`、app-server、`exec-server --remote` 或任何需要模型/ChatGPT 身份的路径。
3. **harness 无状态、无本地工具能力**
   - `harness-worker` 是 app-server 的本地 JSON-RPC client、supervisor 和控制面适配器；它不运行模型、不选择工具、不改写 prompt，也不拥有持久状态。
   - app-server 子进程的本地磁盘和进程都不是权威状态，销毁后必须可以从 completed-turn checkpoint 重建。
   - 除模型调用外，app-server 子进程唯一允许的工具能力是调用批准的远程 MCP server。
   - 禁止 app-server 子进程使用内建 shell、文件系统、apply-patch、浏览器、Web 搜索或任意未批准网络能力。
4. **core 是状态事实源**
   - session、run、审批和规范化事件由 core 持久化。
   - 大输出与 Codex 会话制品存对象存储；本地缓存不能成为恢复前提。
5. **身份凭证不跨越信任边界**
   - 用户 access token 止于 browser-gateway/core。
   - harness 使用短期、受众绑定的 run capability。
   - 每个 executor 使用独立机器身份；executor 凭证不能被 llmproxy 接受。
6. **副作用不能被隐式重放**
   - execution 必须在发送副作用前持久化；断线可以恢复连接和输出游标，但不能自动重试结果不明的写操作、进程启动或 stdin 写入。

## 1. 目标与非目标

### 1.1 目标

- 保留“托管大脑 + 用户环境双手”的产品形态，同时把协议和安全边界钉死。
- 将集群内产品组件收敛为 5 个：`agentserver-core`、`browser-gateway`、`harness-pool`、`executor-gateway`、`a2ui-web`。
- 使用 stock Codex，不维护 Codex fork：harness 使用 app-server；executor 使用本地 stdio exec-server。
- harness workload 使用无状态 `harness-worker` 驱动 app-server stdio；worker 属于 harness-pool 的 per-run 数据面，不增加常驻产品组件。
- executor-gateway 向大脑提供 MCP 工具，并将工具参数确定性地映射为 exec-server JSON-RPC。
- 支持 BYO executor：用户只启动 agentx；agentx 校验/监管随发行包提供或显式安装的 pinned stock Codex，连接方向始终由 agentx 向集群拨出。
- 给会话、运行、执行、事件、审批和凭证各自明确的状态所有者与恢复语义。

### 1.2 非目标

- Phase 1 不提供托管 executor；`managed` 仅作为复用同一 agentx 协议的 Phase 2 扩展。
- 不提供 IM、notebook/Jupyter 或其他 harness 实现。
- 不把工作区文件系统挂载到 harness，也不建设共享 RWX workspace drive。
- 不复用 `codex exec` CLI 作为双手。该 CLI 接收 prompt 并运行模型，不符合确定性执行端的定义。
- 不复用 `exec-server --remote` 的 ChatGPT 注册、认证和 Noise 链路。agentx 只在本机通过 stdio 驱动 exec-server，远程接入由 agentserver 自己实现。
- 不在 agentx 中重写 upstream 已提供的 process/fs、PTY 和 sandbox handler；agentx 只实现策略代理、连接生命周期和明确命名的少量扩展。

## 2. 总体架构

```text
                                     ┌──────────────────┐
                                     │ 外部 OIDC IdP     │
                                     └────────▲─────────┘
                                              │ 身份认证
┌──────────────┐  Code + PKCE  ┌──────────┐  challenge  ┌──────────────────┐
│ a2ui-web     │◄──────────────►│ Hydra    │◄───────────►│ core login bridge│
│ SPA/AG-UI UI │                │ OAuth2   │              └────────┬─────────┘
└──────┬───────┘                └──────────┘                       │
       │ REST / AG-UI                                              │
       ▼                                                           │
┌─────────────────┐  authorize / create run / event cursor         │
│ browser-gateway │◄───────────────────────────────────────────────►│
└─────────────────┘                                                │
                                                           ┌───────▼────────┐
                                                           │agentserver-core│
                                                           └───┬────────┬───┘
                                        queued run/outbox/lease│        ├──► Postgres
                                                              │        └──► Object Storage
                                                              ▼
                                                    ┌─────────────────────┐
                                                    │ harness-pool        │
                                                    │ controller          │
                                                    └──────────┬──────────┘
                                                               │ 创建隔离 workload
                                                               ▼
                                                    ┌─────────────────────┐
                                                    │ harness workload    │
                                                    │ harness-worker      │
                                                    │   └─stdio app-server│
                                                    │ Codex：仅模型 + MCP │
                                                    └─────┬────────┬──────┘
                                                模型调用  │        │ MCP
                                                          ▼        ▼
                                                   ┌────────┐  ┌──────────────────┐
                                                   │llmproxy│  │executor-gateway  │
                                                   └────────┘  │MCP → JSON-RPC    │
                                                               └────────┬─────────┘
                                                        WSS（agentx 主动拨出）
                                                                        ▼
                                                               ┌──────────────────────┐
                                                               │ agentx               │
                                                               │ 注册/策略/连接/代理   │
                                                               └──────────┬───────────┘
                                                                          │ stdio
                                                               ┌──────────▼───────────┐
                                                               │ stock exec-server    │
                                                               │ process/fs，无模型    │
                                                               └──────────┬───────────┘
                                                                          ▼
                                                                 用户本地工作环境
```

除了 5 个产品组件，部署还依赖 Hydra、Postgres、对象存储、llmproxy，以及按 run 动态创建的 harness workload。`harness-worker` 是 harness-pool 的 per-run 数据面进程，不是第六个常驻服务。外部 OIDC IdP 和用户侧 agentx 不计入集群组件数。a2ui-web 的 workspace/resource REST 直连 core；图中经过 browser-gateway 的业务链路仅指 AG-UI run/事件接口。

## 3. 组件职责

| 组件 | 唯一职责 | 明确不负责 |
|---|---|---|
| **a2ui-web** | 静态 SPA；OIDC Authorization Code + PKCE；AG-UI client；A2UI 渲染 | 不保存服务端会话状态，不持久化 access token |
| **agentserver-core** | workspace/RBAC；session/run；事件；审批；executor/credential/LLM authorization 控制面；Hydra login/consent bridge | 不运行 Codex，不代理模型，不托管 SPA |
| **browser-gateway** | workspace 显式的 AG-UI/SSE 边缘；鉴权委托；规范事件到 AG-UI/A2UI 的映射 | 不创建权威 session，不持久化浏览器会话，不拥有运行状态 |
| **harness-pool controller** | 从 core 的 durable run queue/outbox 领取任务；持有 session/run-attempt lease；调度和回收隔离 workload；汇聚并向 core 提交事件与 checkpoint | 不承载多租户 Codex 子进程池，不拥有 session/run/event 事实 |
| **harness-worker**（per-run） | 作为 app-server stdio client 完成初始化、thread/turn 驱动、原始事件与 server request 转接、cancel/fence、child 监管和 checkpoint manifest 生成 | 不推理、不选工具、不改写 prompt、不直接执行工具、不拥有持久状态 |
| **stock app-server**（worker 子进程） | 运行模型循环；调用 llmproxy 和批准的远程 MCP server | 不访问工作树，不执行本地工具，不直接访问 core/对象存储/harness-pool 控制接口 |
| **executor-gateway** | 向 harness 暴露 executor MCP；鉴权/策略/审批；MCP 到 exec-server RPC 的确定性翻译；连接路由 | 不推理，不改写自然语言，不直接执行 OS 操作 |
| **agentx** | executor enrollment；出站 WSS；owner policy；监管/初始化本地 exec-server；RPC 转发、id 映射、重连缓冲和少量 agentserver 扩展 | 不含模型、不访问 llmproxy、不接受 prompt、不重写标准 process/fs handler |
| **stock exec-server**（用户侧子进程） | 通过 stdio 接收确定性 JSON-RPC；执行 upstream process/fs/PTY/sandbox handler | 不使用 `--remote`，不读用户 Codex 登录态，不调用模型 |
| **llmproxy** | 模型路由、配额、上游凭证注入 | 不接受 executor 机器凭证 |
| **Hydra** | OAuth2/OIDC 授权服务器和 token 签发方 | 不提供用户登录 UI，不直接完成外部身份认证 |

stock Codex 在两处使用同一个 pinned 构建，但能力不同：harness 启动 app-server 并可访问 llmproxy；agentx 只启动 `exec-server --listen stdio`，使用隔离的空 Codex home 且没有任何模型 credential。两者不能复用配置、身份或网络策略。

## 4. 核心术语与状态所有权

### 4.1 标识符

| 标识符 | 含义 | 所有者 |
|---|---|---|
| `workspace_id` | 多租户与授权根 | core |
| `session_id` | 一段用户对话，可包含多个 run | core |
| `run_id` | 一次用户输入触发的大脑运行 | core |
| `run_attempt_id` | core 为一次 harness 执行尝试分配的标识；同一 run 仅在确认未跨过副作用边界时才能创建后续 attempt | core |
| `run_attempt_generation` | 同一 run 内单调递增的 fencing generation；attempt-scoped 事件、capability 和控制指令都必须绑定它 | core |
| `brain_thread_id` | stock Codex app-server thread；仅属于大脑 | core（由 harness-pool 上报），制品外存 |
| `execution_id` | 一次 executor MCP 工具调用 | core（由 executor-gateway 上报） |
| `executor_id` | 一台注册的 agentx 机器身份 | core |
| `env_id` | executor 暴露的一个受控执行环境/工作根 | core + agentx |
| `exec_session_id` | agentx 协议连接的临时会话，用于短时断线恢复 | agentx/executor-gateway |
| `local_exec_instance_id` | agentx 为一个 env/security domain 监管的一次 stock exec-server 子进程生命周期 | agentx |
| `process_id` | 一次确定性 process/start 的句柄 | stock exec-server（agentx 记录绑定） |

executor 侧不存在 Codex thread，也不存在“大脑 thread 与 executor thread 对齐”的关系。`exec_session_id` 是 agentx 的远程恢复会话，`local_exec_instance_id` 是 stdio child 生命周期；二者都不是模型会话。

### 4.2 权威状态

| 状态 | 权威存储 | 可丢弃缓存 |
|---|---|---|
| workspace、成员、角色、资源 | Postgres | gateway 鉴权缓存 |
| session/run 状态 | Postgres | harness affinity |
| prompt、对话上下文、恢复快照 | 加密的 DB 记录或对象存储，受 workspace retention policy 管理 | harness 本地上下文 |
| run attempt、run-attempt lease、generation | Postgres | harness-worker 本地心跳/控制流 |
| 小型规范事件 | Postgres | SSE 发送缓冲 |
| 模型可见 completed-turn checkpoint | 加密对象存储，DB 原子提交 manifest/hash | worker 临时 `CODEX_HOME` |
| 大型 stdout/stderr、UI 制品 | 对象存储，DB 保存摘要与指针 | pod 本地临时文件 |
| executor 注册与环境声明 | Postgres | 在线状态缓存 |
| 活跃 WSS、短时远程输出 ring buffer | agentx + owning gateway pod | 无；只在恢复窗口内有效 |
| process 句柄与 PTY 状态 | stock exec-server 子进程 | agentx 的 ownership index |
| 用户工作树 | 用户 executor 环境 | 无集群副本 |

核心原则是：harness-worker 销毁不丢失已提交的 completed-turn checkpoint；mid-turn attempt 不能被伪装为可继续执行的 thread。executor 断线超过恢复窗口后，运行中进程的结果可以变为 `unknown`，系统同样不能伪造“已恢复”。

## 5. 身份、授权与凭证

### 5.1 用户登录

1. a2ui-web 是 Hydra 的 public OIDC client，使用 Authorization Code + PKCE。
2. Hydra 的 `URLS_LOGIN` 和 `URLS_CONSENT` 指向 core 的 bridge endpoint。
3. core 接收 Hydra challenge，跳转外部 OIDC IdP，并将 `(issuer, sub)` 映射为本地 user。
4. core 接受 Hydra login/consent challenge；Hydra 签发带正确 `aud` 和 `scope` 的 token。
5. core 不保存或验证用户密码；身份认证仍由外部 IdP 完成。

Hydra token 只证明用户身份和授权受众。workspace 角色必须在每次敏感操作时从 core 的当前成员关系校验，不能信任 token 中可能过期的角色声明。

### 5.2 四类 principal

| principal | 获取方式 | 使用范围 |
|---|---|---|
| 用户 access token | Authorization Code + PKCE；Phase 1 使用共享浏览器 API audience `aud=agentserver-api` | a2ui-web → core/browser-gateway |
| 集群 workload token | workload identity、mTLS 或 Hydra client credentials | 产品组件之间的内部调用 |
| per-executor access token | 每个 executor 独立 OAuth client，`aud=executor-gateway` | agentx WSS 连接 |
| 短期 run capability | core 按 run 和 audience 签发 | harness → llmproxy 或指定 MCP server |

run capability 至少绑定 `workspace_id`、`session_id`、`run_id`、`run_attempt_id`、`run_attempt_generation`、`user_id`、允许的 executor/env/tool、`aud`、过期时间和 `jti`。llmproxy、executor MCP 和每个受支持的外部 MCP 使用不同 audience，不能把一枚 token 同时用于模型与执行。共享的 `agentserver-api` audience 只覆盖两个浏览器入口，不得被任何内部服务或 executor 接受。

用户 bearer 不进入 harness、executor-gateway 或 agentx。browser-gateway 使用内部身份调用 core 的 authorize API，并将目标 workspace/action 一并提交；core 只向 browser-gateway 返回 actor context 与 run handle。run capability 在 harness-pool 持有效 session lease 和 run-attempt lease 后签发给目标 workload，不能经浏览器链路转交。

Phase 1 不尝试修改已启动 app-server 的环境变量来轮换 capability。系统必须定义并强制 `max_run_duration`；每个 capability 的 TTL 覆盖该上限与很短的收尾 grace，但 llmproxy 和 MCP gateway 在每次请求时仍校验 live run-attempt lease/generation，因此取消、fence 或成员移除可以立即生效。超过硬上限的 attempt 必须被中断，不能带过期 token 继续运行。若未来需要无硬中断的长 turn，必须先引入由 harness-worker 持有的本地 credential broker，再允许透明轮换。

### 5.3 凭证存储

LLM 上游 key、外部服务 credential 和 executor 机器材料都必须：

- 使用 KMS envelope encryption；
- 每条记录使用随机 nonce；
- AAD 至少包含 workspace、credential type 和 record id；
- 保存 key version，支持轮换和旧版本重加密；
- 记录读取、更新和使用审计；
- 永不写入日志、规范事件 payload 或 Codex rollout；运行时传递必须最小化、短期化且不可持久化。

llmproxy 根据受众正确的 run capability 注入上游模型凭证；harness 不直接获得真实上游 key。per-executor token 必须在 llmproxy 处被拒绝。

stock Codex 访问 llmproxy/MCP 所需的短期 capability 优先通过 tmpfs/受限文件描述符投射；若所 pin 版本只能从进程环境读取，则仅注入该 audience-bound capability，并保证不进入 rollout、诊断 dump 或持久化配置。harness-worker 的 workload/control credential 使用独立文件权限或 non-exportable identity，相关 FD 必须 close-on-exec，不能进入 app-server 子进程。executor 连接 credential 仍严禁进入任何被执行子进程。

## 6. 主流程

### 6.1 创建并运行对话

1. a2ui-web 调 core 创建或选择 `session_id`。
2. 用户向 `POST /api/workspaces/{workspace_id}/agui` 提交 prompt，可带已有 `session_id` 和客户端生成的 `Idempotency-Key`。
3. browser-gateway 将用户 token、目标 action、幂等键和规范化请求 hash 交给 core；core 完成鉴权，并在同一事务中写入 `run_id`、第一条规范事件和 durable outbox。同一 user/workspace/session 下重复的幂等键只有在请求 hash 相同时才返回原 `run_id`，不同 payload 必须报冲突。
4. browser-gateway 将第一条事件立即映射为 `RUN_STARTED`，之后按 event cursor 订阅该 run。
5. harness-pool 领取 queued run，并以 CAS 获取 `session_lease`，同时创建新的 `run_attempt_id/run_attempt_generation` 与对应的 run-attempt lease。session lease 保证任何时刻一个 session 最多只有一个 active run；run-attempt lease fence 同一 run 的旧 worker。
6. harness-pool 创建隔离 workload。per-run harness-worker 接收不可变、签名的 run manifest，恢复最近一个已提交的 completed-turn checkpoint，创建清洗后的临时 `CODEX_HOME`，再以 stdio 启动并初始化 stock app-server。
7. harness-worker 调用 `thread/resume` 或 `thread/start`，然后以原始用户输入调用 `turn/start`；它不改写 prompt。app-server 通过 llmproxy 调模型，需要工具时只调用 run manifest 中固定的远程 MCP。
8. harness-worker 为原始 app-server 消息附加 `run_attempt_id/generation + producer_instance_id/producer_seq`，通过唯一 mTLS control stream 发给 harness-pool；harness-pool 完成规范事件映射并提交 core。core 拒收旧 generation，browser-gateway 从已提交事件映射 AG-UI/A2UI。
9. 收到 `turn/completed` 后，run 先进入 `finalizing`，但该 notification不是 transport cleanup barrier。worker继续排空 stdio，直到本 attempt已登记的 reverse request ID全部收到 `serverRequest/resolved`，并确认 execution/process收口；随后才关闭 app-server stdin，等待其优雅退出，使 thread rollout 完整且字节稳定。只有 child 在有界时间内正常退出后才能按 pinned allowlist 生成 checkpoint manifest；SQLite 主库及其 WAL/SHM 等运行时派生文件不进入 checkpoint。harness-pool 先上传加密对象，再由 core 以 CAS 在同一状态事务中提交 checkpoint 指针与 run terminal event；未提交的对象由后台清理。
10. core 确认 terminal state 和 checkpoint 后，harness-pool 删除 workload 与临时目录。mid-turn crash 不生成可恢复 checkpoint，也不能继续原 turn。

浏览器断开不自动取消 run。取消必须通过显式 API/action，并产生规范事件；重新连接使用 cursor 继续读取。

Phase 1 在一个 session 已有 active run 时不接受第二个新 run，返回带当前 `run_id` 的 `409 active_run`；不隐式映射为 `turn/steer`。未来支持 steer 时必须新增显式 API、绑定 `expected_turn_id` 并进入同一 fencing/审计链路。

### 6.2 执行 MCP 工具

1. Codex 发起带结构化参数的 executor MCP tool call。
2. executor-gateway 校验 run capability、当前 run-attempt generation、workspace live RBAC、executor/env 归属和工具策略，并按 `(run_id, app_server_tool_call_id)` 向 core 调用 `PrepareExecution`。core 持久化 `execution_id`、规范化参数 hash、tool/schema/policy/mapper version 与确定性 `operation_plan_hash`；重复 tool call id 只有完整 context hash 相同时才返回原 execution。approval hash 同样覆盖该 plan。一个工具若需要多个确定性步骤，gateway 还必须在各步骤发送前分别 `PrepareOperation`，为每个 operation 分配唯一 `mutation_key`。
3. 若策略为 `deny`，execution 进入 `denied`；若为 `ask`，gateway 通过标准 MCP elicitation 暂停该工具调用，harness-worker 将 app-server 的 server request 转成规范 approval 事件。core 创建一次性 approval；拒绝、过期、取消或 control stream 断开时不得 dispatch。
4. 批准后，gateway 重新校验 live RBAC、attempt generation 和批准 hash，以 CAS 消耗 approval，并将 execution 置为 `dispatching`。每个有副作用的 operation 也必须在对应网络发送前以 CAS 从 `prepared` 置为 `dispatching`；从该 operation 跨过边界起，崩溃后的默认结果是 `unknown`，不能自动重放。
5. gateway 将已批准 MCP 参数机械映射为一个或多个 exec-server JSON-RPC 请求；每个请求携带既有 `execution_id/operation_id/mutation_key`。agentx 以本地 owner policy 校验 workdir、路径、用户、网络和 sandbox；远程策略只能收紧，不能放宽本地策略。
6. 对 upstream 标准方法，agentx 只做 request id/ownership 映射并转发给本地 stdio exec-server；exec-server 执行并返回带序号的输出/结果。agentx 自己只处理协商过的 agentserver 扩展。
7. agentx 接收并去重 mutation 后返回 dispatch ACK；gateway 先把对应 operation 置为 `acknowledged`，再按聚合规则把 execution 置为 `running`。operation 与 execution 的 terminal result 必须先写 core，再作为 MCP progress/result 返回大脑。若原 MCP transport 已断开，结果仍保留在 execution 中，但当前 turn 进入 `interrupted`，不能伪造原调用已恢复。

这条路径中不存在“把用户指示改写成 prompt”“executor 再思考一次”或“恢复 executor 模型会话”。

WSS/session 恢复只恢复 gateway ↔ agentx 通道，不能恢复已断开的 app-server ↔ MCP HTTP 调用。后续 run 只能读取并显式注入已持久化的 `succeeded|failed|unknown` execution 结果，不能重新调用同一副作用工具来“确认”。

### 6.3 故障与恢复

- harness workload 在 app-server 接受 `turn/start` 之前失败，可以从最近 completed-turn checkpoint 创建新的 run attempt；旧 attempt 被 fence。`turn/start` 一旦被接受，任何 worker/app-server mid-turn crash 都使当前 run 进入 `interrupted`，即使尚未 dispatch executor 副作用也不能自动重跑该 turn；这避免重复模型调用和已流式输出分叉。
- 一旦 execution 已进入 `dispatching`，harness/gateway 崩溃后不得自动重放；run 标记为 `interrupted`，无法从 agentx journal/core terminal record确认的 execution 标记为 `unknown`，由用户决定下一步。
- harness-worker 与 harness-pool 的 control stream 短时断开时，worker 只做有界事件缓冲且不接受新的控制决策；approval 一律失败关闭。grace period 到期、缓冲溢出、ACK 出现不可恢复缺口或 lease 无法确认时，worker 调 `turn/interrupt` 并终止 app-server。重连必须携带 attempt generation 和 producer ACK，旧 generation 的消息全部丢弃。
- agentx 与 gateway 短时断线时，agentx 保持本地 stdio pipe 和 exec-server 子进程存活，并用 `exec_session_id` 与 output sequence 恢复；Phase 1 默认 grace period 为 30 秒。
- grace period 到期后，agentx 主动关闭 stdio。upstream stdio transport 会尝试终止其 managed process；已确认退出的 process 正常收口，未收到终态的 execution 标记为 `unknown`，绝不自动重新执行。
- exec-server 自身崩溃时不能假定孙进程随之退出。agentx 必须把 exec-server 及其后代放入可整体回收的 cgroup/process group/job object，异常时执行 kill-tree 并核对结果；在确认前 execution 为 `unknown`。
- 每次 stdio 断开或 child 退出都使 `local_exec_instance_id` 失效；gateway 不能把旧 process handle 绑定到新子进程。
- 显式 cancel 先将 run 置为 `cancelling`，由 harness-pool 向当前 generation 的 worker 发送 `turn/interrupt`，并对所有 run-scoped process 发 terminate。收到 app-server `turn/completed(interrupted)`、所有已登记 reverse request的 resolved和进程退出确认后才能记为 `cancelled`；未确认的 execution 记为 `unknown`。
- 正常完成 run 前也必须确认所有 process 已 closed，或显式 terminate 并收到确认。存在未确认 process 时 run 进入 `interrupted`，不能记为 `completed`。

## 7. Harness 设计

### 7.1 harness-worker / appserver-host

stock app-server 是双向 JSON-RPC server，不会从环境变量自动取得 prompt 并独立完成一次 run。每个动态 workload 因而包含一个无状态 `harness-worker`，它作为 PID 1/可信宿主，通过本地 stdio 驱动一个 stock app-server child：

```text
harness-pool controller ──mTLS control stream──► harness-worker ──stdio──► stock app-server
                                                        │                      ├─► llmproxy
                                                        │                      └─► approved MCP
                                                        └─ 临时 CODEX_HOME/checkpoint manifest
```

harness-worker 只负责：

- 校验不可变 run manifest 和 attempt generation，恢复已提交 checkpoint，创建清洗后的临时 `CODEX_HOME`；
- 以绝对路径启动 pinned `codex app-server --listen stdio:// --strict-config`，完成 `initialize → initialized → thread/start|resume → turn/start`；`--strict-config` 只用于拒绝未知配置字段，能力隔离仍由下述组合与测试承担；
- 按 pinned schema 语义无损转接允许的 app-server notification 和 server-initiated request：保留 method/params payload，在 control envelope 中关联 request id、producer sequence 和 ACK，并做有界缓冲；未列入 allowlist 的 server request 一律 fail closed；
- 将 MCP elicitation/approval request 转给 harness-pool/core，收到明确决定后再答复 app-server；
- 接收 cancel/fence，调用 `turn/interrupt`，监管 child/stderr/退出并清理临时目录；
- 在 `turn/completed`、outstanding reverse-request set清空且 child正常退出后生成 checkpoint manifest，交由 harness-pool 上传和提交。

worker 不调用模型、不选择工具、不解释或改写 prompt、不执行 shell/fs、不直接访问 executor，也不拥有 session/run/event 的权威状态。控制流中断时它不得自行重试 turn 或 MCP 副作用。实现上可以复用 harness-pool 代码库的 worker subcommand；它不是新的常驻产品服务。

MCP-only 不是 prompt 约定，而是 pinned Codex build 上必须同时成立的能力配置：

- `initialize` 显式开启所需 experimental API；`thread/start` 与每次 `turn/start` 都传 `environments: []`，使环境支持开启时也没有默认本地 environment；当前 schema 的 `thread/resume` 没有该字段，cold resume 固定传 app-server 返回的 rollout path 和 `excludeTurns: true`，以成功 RPC response 而不是并不存在的 `thread/started` notification 作为恢复屏障，随后由 `turn/start` 再固定空 environments；
- 清洗后的 `config.toml` 禁用所有目标 stock release 实际支持关闭的非 MCP tool source，包括 `request_user_input`、Web、apps、plugins、multi-agent、browser/computer use、hooks；不提供 dynamic tools 或 capability roots；对 `update_plan` 这类 stock 内建 utility，所 pin release 也必须提供经过 tool capture 验证的真实禁用机制，不能在文档中虚构配置键；
- MCP server 的 `enabled_tools` 必须约束模型可见的完整 MCP 派生工具面，而不只是过滤该 server 的 `tools/list` 结果。stock 自动注册的 `list_mcp_resources`、`list_mcp_resource_templates`、`read_mcp_resource` 等通用 handler 也必须能被真实移除或纳入显式产品授权；仅让 executor-gateway 对 `resources/list`、`resources/templates/list`、`resources/read` 返回错误，不能满足“模型只看到 manifest 批准工具”的约束；
- workload 在 child 启动前只读挂载管理员控制的 `/etc/codex/requirements.toml`，以 `mcp_servers.<name>.identity = { url = "https://..." }` 的字符串形式精确 allowlist MCP server 名称与 HTTPS URL，并固定所有安全相关 feature；模板生成器拒绝 `http`、stdio identity 以及 `prefix|regex` matcher，run config 只能进一步收紧；
- executor-gateway MCP 配置 `default_tools_approval_mode = "approve"`，避免 app-server 再产生一层通用工具审批；thread 使用 granular approval、`approvals_reviewer = "user"`，仅允许 `mcp_elicitations`，其余内建 approval 类别关闭；managed requirements 也只允许 user reviewer。不能用 `approval_policy = "never"`，因为它会自动拒绝需要产品审批的 MCP elicitation；0.146.0 live probe 已用 destructive/open-world tool 和 `prompt` 正向控制证明 `approve` 确实不会产生 Codex 通用审批；
- conformance test 使用 fake model endpoint 捕获实际 Responses 请求，断言模型可见工具集合只包含 run manifest 批准的远程 MCP tools。只检查配置文件内容不构成隔离证明。

官方 release binary 没有可安全重定向 system `requirements.toml` 的 CLI：源码中的 `CODEX_APP_SERVER_MANAGED_CONFIG_PATH` 只在 `debug_assertions` build 生效，0.146.0 official artifact 的 live probe 也确认它被忽略。因此 A04 正向测试必须在一次性 image/mount namespace 内把文件预装到真实 system path，再启动未经修改的 stock artifact；不能改宿主 `/etc`、依赖 debug build 或把临时 user config 冒充 managed requirements。该测试还必须从 MCP bootstrap/tool surface 观察 wrong-name、wrong-URL、user/project additions 被禁用，因为 `configRequirements/read` 不投影 MCP allowlist 本身。

以上字段包含 experimental contract，必须与 Codex binary、app-server schema 和测试 fixture 一起锁定。任一升级导致工具面扩大、elicitation 被自动处理或配置字段失效时，harness 镜像不得发布。

已验证的 0.145.0 candidate 不满足该前提：固定 model catalog 并关闭全部已知非 MCP 开关后，`update_plan` 仍被无条件注册；scripted model 能成功执行它并触发 `turn/plan/updated`。因此该版本必须在 A03 被拒绝，不能作为 production runtime pin，也不能用 prompt、忽略 notification 或仅在 harness 侧过滤事件来冒充能力隔离。

官方 `rust-v0.146.0-alpha.14` candidate（commit `9d84cad281364eb7f6be75e23067b0adc5e26106`）新增了真实的 `[tools.update_plan] enabled = false`，无 MCP server 时实际模型工具面可收敛为空；但只要配置一个 executor MCP，stock 又会在批准的 namespaced MCP tool 之外无条件注册三个通用 MCP resource handler。live probe 进一步证明 `list_mcp_resources` 会越过该 server 的 `enabled_tools`，真实发出 `resources/list`。因此这个 alpha 只修复了前一个 blocker，仍不通过 A03，也不能成为 production runtime pin。当前架构下必须等待 stock release 提供可验证的 resource-handler 禁用/精确暴露机制；如果产品要改为接受这些 handler，必须另行修改 A03、manifest 授权模型和 executor MCP 协议边界，不能把 fail-closed 响应静默当成原设计已经满足。

官方 stable `rust-v0.146.0`（annotated tag object `be449751a978f02e5bbba886999662956c7f38f5`，peeled commit `e363b08c9175ac1cbe5893615dd2cb9ddf95043b`）的 release-bound live probe 得到相同结论：无 MCP 时可见工具为空；加入只批准 `approved_echo` 的 executor MCP 后仍额外暴露三个通用 resource handler，且 `list_mcp_resources` 仍真实到达 `resources/list`。因此“alpha 可能尚未收敛”已经不是解释，当前正式版同样明确不通过 A03。

### 7.2 隔离与网络

- harness-pool 是常驻 controller；“scale to zero”只表示删除或归零无 active run 的动态 workload 容量，不能把 controller 自身设为 `replicas: 0`。
- 每个 active run 使用独立 workload；不在共享 pod 内以多个子进程承载不同租户。
- workload 位于 workspace namespace，并使用 service account、seccomp、只读 rootfs、tmpfs、资源限额和 NetworkPolicy。
- app-server child 的整个 mount view 都没有 workspace worktree、用户代码卷或 Kubernetes service-account token，不能只把进程 `cwd` 指向空目录却仍让仓库在别处可读；worker 也只能访问该 attempt 的临时控制目录和 checkpoint staging volume。
- app-server 使用新建且清洗过的临时 `CODEX_HOME`；auth、config、MCP secret 或整个目录均不能原样持久化。
- worker 只允许建立到 harness-pool 的 mTLS control stream；app-server child 只允许访问 llmproxy 和内部 egress proxy。两者使用不同 UID/权限域，worker credential/control文件对子进程 UID 不可读，控制 socket和所有非 stdio FD都以 `O_CLOEXEC` 打开。production launch trampoline在最终 exec前还必须从显式 allowlist执行 close-all（Linux优先 `close_range`），不能仅依赖 Go `exec.Cmd` 没有填写 `ExtraFiles`；A12负向 probe已经证明一个被清掉 `CLOEXEC` 的未列出 FD会进入 stock child。
- Kubernetes NetworkPolicy 是 Pod粒度，不能单独实现同一 workload内“worker可到 harness-pool、child不可到 harness-pool”的进程级差异。Phase 1 的 Linux pod在 runtime container启动前，由唯一持有 `NET_ADMIN` 的 init container安装默认拒绝的 nftables OUTPUT规则：按固定 worker/app-server UID分别只允许 harness-pool和 `llmproxy + egress proxy`的已固定 ClusterIP/端口；runtime worker与 child均不持有 `NET_ADMIN/NET_RAW`。受支持内核若不能执行并验证 `meta skuid` owner规则，该 image直接不通过 A12，不能退化为只有 NetworkPolicy。Pod级 NetworkPolicy仍按两者 destination并集做第二层限制。
- child不直接访问外部 MCP或通用 DNS。内部服务名在启动前被固定到只读 hosts视图，child UID禁止 DNS egress；外部 MCP的 DNS、目标连接和 redirect都由受控 egress proxy代办。proxy按 run manifest校验 endpoint allowlist、DNS结果、IP类别、SNI/证书、端口、每次 redirect、响应大小与超时；不能把域名策略只写在 Codex config或 requirements中。
- MCP endpoint 和 tool allowlist 由 core 生成后固定进 run manifest，模型输出、prompt 或 skill 不能动态增加 endpoint。每个 endpoint 必须校验 TLS 身份并使用独立 audience capability；缺少凭证时 endpoint 必须拒绝，不能回退为匿名调用。
- Phase 1 executor MCP 不实现 resources/prompts 协议；任何 `resources/list`、`resources/templates/list`、`resources/read`、`prompts/*` 请求都必须 fail closed 并进入安全审计。但这是纵深防御，不替代 A03 对模型工具面的精确约束。
- Phase 1 只有 executor-gateway 可以暴露副作用工具。第三方 MCP 只允许管理员固定 endpoint、tool/schema hash 并独立验证为只读的工具；不能把第三方自报的 `readOnly/destructive` annotation 当作安全事实。未来支持第三方副作用前，必须统一经过可执行 core policy/approval 的 MCP policy proxy。
- skills/context 只能是只读的声明性提示与配置；引用本地脚本、stdio MCP 或其他可执行载荷的 skill 必须被拒绝。
- 必须以 fail-closed 配置和集成测试证明内建 shell、fs、apply-patch、浏览器、Web、hooks、dynamic tools 和未列出的本地能力均不可调用；只在文档中声明禁用不算完成。
- 如果所 pin 的 stock Codex 版本无法可靠进入上述 MCP-only 工具模式，则实现被阻塞，不能用 system prompt 代替能力隔离。

### 7.3 会话 checkpoint

恢复状态与 UI/审计事件是两种不同投影：

- **模型可见 checkpoint**：加密保存恢复 thread 所必需的完整、模型可见历史，包括后续 turn 需要的 MCP tool result、compaction/rollout 元数据；不能为了 UI 脱敏而从中任意删除模型已经看到的内容。
- **规范/UI/审计事件**：按 secret/prompt policy 过滤，只用于展示、审计和事件恢复，不能反向拼成模型上下文。

checkpoint 只能在 app-server 发出 terminal `turn/completed`、所有已登记 reverse request随后 resolved、worker关闭 stdin、child完成有界优雅退出且 rollout 达到完整、字节稳定状态后生成。不能在仍运行的 `CODEX_HOME` 上取文件，也不能用固定 sleep猜测稳定。对已验证的 0.146.0-alpha.14 与 stable 0.146.0，pinned allowlist 是每个 `brain_thread_id` 恰好一个由 app-server thread response 返回的 rollout JSONL；worker必须验证它是 `CODEX_HOME` 内的非 symlink 普通文件，checkpoint staging也必须严格只有该 manifest entry。manifest 至少包含 `brain_thread_id`、terminal `turn_id`、`run_id`、`run_attempt_id/generation`、Codex commit/build、schema/allowlist version、相对路径、大小和文件 hash。`state_5.sqlite`、所有 SQLite WAL/SHM、goals/logs/memories DB均为运行时派生状态，不进入 checkpoint；配置、requirements、token、环境变量、诊断日志、cache和临时 transport缓冲同样禁止进入。每个新 Codex build都必须重新通过 native `thread/resume` round-trip才能获得 allowlist，禁止打包整个 `CODEX_HOME`。harness-pool先上传加密对象，core再以 CAS原子提交 checkpoint pointer与 run terminal state；未引用对象可清理。

A08 已在 alpha.14 与 stable 0.146.0 上验证上述退出屏障：completed terminal 后立即关闭 stdin，stock app-server在有界时间内零退出；退出后两次有界遍历得到完全相同的相对路径、mode、大小和 SHA-256，thread报告的 rollout是完整 JSONL，`state_5.sqlite`具有合法 SQLite header。两个 release 都仍保留 `state_5.sqlite-wal/-shm`，以及 goals/logs/memories数据库的 WAL/SHM sidecar；因此 A08 只证明完整退出后的字节稳定，不负责判断 checkpoint 文件集合。

A09 已在同两个 release 上把 allowlist 收敛为单个 rollout JSONL。probe只把该文件按 manifest相对路径复制到全新 `CODEX_HOME`，在写入新 attempt config前断言 staging没有额外文件，并把原 `CODEX_HOME` 改名使旧绝对路径失效。随后用 rollout绝对新路径和 `excludeTurns: true` cold resume；`thread/resume` 成功 response就是恢复屏障，它不会另发 `thread/started`。第二 turn的真实模型请求仍包含第一、第二轮用户输入、原 MCP call ID及完整 tool result，且没有重放 MCP副作用。缺失 rollout则在模型调用和 MCP初始化之前以 `-32600` fail closed。因此 SQLite主库/WAL/SHM以及 config都不属于恢复事实；config必须在恢复文件落盘后重新生成。

A10 也已在同两个 release 上验证 mid-turn hard-crash边界。probe先把一个 completed turn的 rollout复制为独立、密封的 checkpoint，再从它启动第二个 app-server；scripted model在确认第二个 `turn/start` 已被接受、真实模型请求已包含本轮输入后保持 HTTP response未决，此时硬杀 child。该进程非正常退出，不重试模型请求，也无法改动独立 checkpoint。crash runtime随后被丢弃；第三个全新 `CODEX_HOME` 只恢复密封 rollout并显式创建新 turn。新 turn ID与被放弃 turn不同，模型上下文包含 crash前的 completed历史和新的继续指示，但不含被放弃输入。这证明的是系统允许的安全恢复路径，不是假设 stock Codex会拒绝调用方错误传入的未提交 crash rollout；core的 committed checkpoint pointer、attempt generation和 worker文件来源校验必须让该文件从一开始就不具备恢复资格。

A11 已在同两个 release 上验证 runtime secret排除。source attempt在 config、auth、token、requirements decoy、log、env dump和 transport buffer文件中放入不同 sentinel，并通过 `bearer_token_env_var` 让一次性 MCP capability真实出现在每个 MCP bootstrap/tool HTTP request中。clean exit后这些源文件及 sentinel仍存在，但模型请求 body、stderr和 rollout均不含九个 runtime secret值；rollout仍完整保留用户、assistant、MCP call ID和安全 tool result。checkpoint staging严格只有该 rollout。恢复 attempt重新生成 config并换用新的 capability，native resume成功且不重放副作用，旧/新 capability都没有进入恢复模型上下文或新 rollout。这里的 requirements只是挑战 `CODEX_HOME` allowlist的 decoy；真实 system requirements仍由 A04 image-level gate负责。

A11只检测非模型事实的意外渗漏。若 secret scan在 rollout中发现本不应进入模型历史的 runtime credential，必须拒绝/quarantine整个 checkpoint并把 run标为不可恢复，不能原位替换字节后继续声称 native resume。相反，用户输入或 MCP result中本来就模型可见的敏感内容不能由展示层过滤器擅自删除；应依靠输入/工具策略预防、加密存储、访问控制、retention和删除流程处理。

A12 的 Darwin host-level probe已在同两个 release上确认一项正向事实和两项负向边界。model provider被故意配置为“若 worker mTLS sentinel环境变量存在就发到 HTTP header”；sensitivity control把值显式注入 child并观察到 exact header，随后 parent-only case的显式 child env使真实模型请求、body和 stderr都看不到该值。app-server thread返回的 cwd也是 source tree之外始终为空的临时目录，但这不证明 mount namespace中别处没有工作树。FD反例清掉一个父 pipe的 `CLOEXEC`，即使 Go启动代码未配置 `ExtraFiles`，stock child仍持有该 writer，说明 runner必须实现上述 close-all trampoline，并在 production Linux image中重跑相同陷阱。网络反例让配置的 llmproxy返回跨 origin `307`，stock client会把同一模型 request送到第二个未配置 origin并从那里完成 turn，说明 base URL、managed requirements和 capability都不是网络可达性控制。

A12 因而仍是 image-level open gate。正向 job必须在 production Linux image中同时验证：不同 UID下 worker credential/control path、`/proc`和 sentinel FD对子进程不可读/不可见；child mount view没有任何 workspace或 service-account token；允许 llmproxy和 approved MCP egress的请求成功；直接指向 forbidden sink、允许端点返回到 forbidden sink的 redirect以及非允许 DNS都失败且 sink计数保持零。只有 config级负向结果、401/403或 sink返回错误都不算网络隔离证明。

worker 不持有对象存储 credential。它通过与 harness-pool 的同一 mTLS 连接内、有界的 checkpoint data substream 分块发送 manifest 和 staging 内容；harness-pool 施加大小/速率限制、复算逐块与整对象 hash 后上传，再请求 core 提交。上传或提交失败时对象不得成为可恢复 checkpoint，未引用对象按 retention job 清理。

mid-turn crash 时只允许恢复上一个已由 core提交指针的 completed-turn checkpoint，当前 run 进入 `interrupted`；不能重放同一 `turn/start`，也不能扫描 crash runtime并把其中较新的 rollout冒充 checkpoint。后续用户明确继续时创建新 run/turn，并将已持久化的 execution terminal/unknown 结果作为显式上下文注入。

`brain_thread_id` 是优化和追踪标识，不是 session 的权威主键。native `thread/resume` 只适用于相同 pinned schema 的完整 checkpoint；不兼容时可从受保护的模型可见 conversation record 创建新 thread，但这是语义降级，必须生成审计事件，不能声称与原生 resume 等价。

### 7.4 Warm pool

Phase 1 优先使用镜像预拉取、节点运行时缓存和预建只读层降低冷启动，不做跨租户 Codex 进程池。

若后续引入 warm workload：

- 只能是在目标 workspace namespace 内创建、干净、未分配且未启动 Codex thread 的 sandbox；不能跨 workspace 转手；
- 分配后绑定一个 workspace/run，回收前彻底销毁；
- 不能复用前一租户的进程、临时目录、网络身份或 capability。

## 8. Executor 设计

### 8.1 agentx + stock exec-server

agentx 是 Go 编写的连接与策略代理；标准执行引擎直接复用 stock exec-server。Phase 1 默认一个 executor 只暴露一个 env；若以后允许多个 env，不同 root、OS uid 或 owner policy 的 env 必须各自使用独立 exec-server child，不能共享执行状态。每个 env 的启动顺序钉死为：

1. 从发行包 manifest 解析允许的 Codex version、平台二进制和 SHA-256/签名；禁止静默使用 `PATH` 中的任意 Codex。
2. 创建只属于该 env/child 的临时 runtime dir 和空 `CODEX_HOME`，写入最小配置并禁用 analytics；不读取用户已有的 Codex config/auth。
3. 将 exec-server 自身的 cwd 固定到 runtime dir（不是用户 worktree），清洗父进程环境，再以该 env 配置的执行身份启动绝对路径的 `codex exec-server --listen stdio --strict-config`。防止读取用户/项目配置依赖空 `CODEX_HOME`、固定 cwd 与清洗环境；`--strict-config` 只负责对未知配置字段 fail closed，不能单独承担隔离。Linux 发行包同时提供该构建所需的匹配 sandbox helper。
4. agentx 作为本地 client 向 stdio 子进程发送一次不带 `resumeSessionId` 的 `initialize → initialized`，再调用 `environment/info`、`environment/status`。完整方法集来自 pinned build 的 manifest/schema，不能假设 child 会动态枚举全部 capability；握手结果只用于确认当前实现实际公开的字段。agentx 记录新的 `local_exec_instance_id` 和仅供本地诊断的 child session id。
5. 本地执行面就绪后，agentx 才向 executor-gateway 声明 online。

agentx 自己负责：

- enrollment、机器身份、出站 WSS 和 connection generation；
- 声明 platform、capability 和一个或多个 env/workdir root；
- 在转发前实施 root/sandbox/network/owner policy；
- 将远端 request id 映射为本地 stdio request id，并维护 process ownership index；
- WSS 断线期间保持 stdio 连接、持续排空 child stdout/stderr，并按外层 session sequence/ACK 缓存和恢复 response/notification；每个 process 的 output sequence 只用于该进程补读，不能替代外层顺序；
- 监管 exec-server 子进程，检测非预期退出并使旧 handle 失效；
- 以平台支持的 cgroup/process group/job object 监管并回收 exec-server 的完整后代进程树；
- 实现经过 capability negotiation 的 agentserver 命名空间扩展；
- 对连接、策略决定和执行生成本地审计日志。

process、PTY、stdin、signal、filesystem 和 upstream sandbox handler 由 stock exec-server 完成，不在 agentx 中复制。executor 侧不需要模型配置或模型 credential。agentx 的机器连接 credential 保存在 OS keychain/受限文件中，只由 agentx 使用，永远不注入 exec-server 或它启动的子进程。

exec-server 可能依据 `envPolicy` 从自身环境继承变量，因此只过滤 `process/start.env` 不足以保护 credential。child 自身必须从一开始就运行在无 agentx secret 的环境中，agentx 还必须将远端 env policy 收紧到本地 allowlist；远端不能请求重新继承被剥离的宿主变量。

stable 0.146.0 的 live probe 还暴露了两个不能由 JSON-RPC“成功响应”掩盖的执行语义缺口：

- `process/signal` 对不存在、信号已送达和根进程已退出三种状态都返回相同的空对象 `{}`。它最多表示 server 已处理请求，不能证明 signal 到达目标；execution 只能依据后续 terminal evidence 收口，缺少证据时必须是 `unknown`。
- 根进程退出并发出 `process/exited` 后，只要后代继续持有 pipe，process 尚未 `closed`；此时 `process/terminate` 返回 `running: false` 且不会杀该后代。关闭整条 stdio connection 会通过 session drop 回收进程组，但这不是 operation-scoped recovery，并可能影响同一 local exec instance 中的其他操作。

所以 `process/exited` 只能视为根进程退出证据，`process/closed` 才表示 output streams 已关闭；二者之间不能提前宣布 execution 成功。当前 E03/E07 验收不变时，stable 0.146.0 仍被这两项额外阻断。若未来产品明确批准 agentx adapter workaround，至少必须在每个 local exec instance 串行化 operation，并在 `exited` 后有界等待 `closed`；超时则回收整个 exec-server tree、将结果标记为 `unknown`，且不能把空 signal response 映射成“已送达”。这属于显式架构/验收变更，不能在实现中静默放宽。

upstream 将 `codex exec-server` 标记为 experimental，因此不能假设 semver wire compatibility。agentx 发行包必须锁定精确 commit/build，升级走 schema diff、fixture、sandbox 与故障恢复回归，不允许自动跟随 latest。

### 8.2 MCP 工具面

初始工具面保持小而确定：

| MCP tool | 必需的确定性输入 | RPC 映射 |
|---|---|---|
| `list_environments` | 可选 executor filter | core/gateway registry；不触发远端执行 |
| `shell` | `env_id`、`argv[]`、`cwd`、timeout、tty、显式 env policy；`lifecycle=run` | `process/start` + `process/read`/通知 |
| `unified_exec` | 同上，另含调用方提供的 `process_id` 和输出模式 | run 内长生命周期 `process/start` |
| `write_stdin` | `env_id`、`process_id`、`write_id`、bytes | `process/write` |
| `read_output` | `env_id`、`process_id`、from sequence | `process/read` |
| `terminate` | `env_id`、`process_id`、reason | `process/terminate` |
| `read_file` | `env_id`、path、offset/limit | `fs/readFile` 或 block API |
| `apply_patch` | `env_id`、单文件 unified diff、目标文件 precondition hash | gateway 读取并确定性应用；agentx 以条件写扩展原子提交 |

除 `list_environments` 外，每个工具都必须携带或由 run capability 无歧义地解析出 `env_id`。不得依赖“当前 executor”这种隐式全局状态。

`argv` 是字符串数组，不是自然语言。若调用者确实需要 shell 语法，必须显式传入例如 `["/bin/zsh", "-lc", "..."]`，并受独立策略控制。

`timeout` 不是当前 stock `process/start` 的字段；gateway/agentx 必须用受 fencing 约束的本地计时器实现，并在到期后发送 `process/terminate`、等待真实终态。不能把 timeout 悄悄塞入 upstream params，也不能在未确认进程退出时只返回“超时成功终止”。

MCP 的 `cwd/path` 使用 env-relative 表示。gateway 根据已登记 root 做语法映射并编码为 upstream 要求的 `file:` URI，但 agentx 的本地 root 才是授权事实：它必须按 URI 语义重新解码、canonicalize 并校验。禁止只用字符串拼接检查路径，`..`、percent encoding、Windows drive/UNC 和 symlink 都必须进入跨平台逃逸测试。

`apply_patch` 不是 exec-server 的原生方法。Phase 1 每次调用只允许修改一个文件：gateway 先读取、验证 precondition hash 并确定性应用 patch，再调用协商后的 agentserver 条件写扩展，由 agentx 在本地原子验证 hash 后提交。该提交必须降权到目标 env 的执行身份，或调用随发行包签名的固定 fs helper；持机器凭证的控制面不能以自身高权限任意写工作树。未协商条件写 capability 时不暴露该工具；不能用普通“read 后 write”、生成自然语言或启动模型冒充 CAS。多文件 patch 必须拆成多个 execution，并明确不具备跨文件原子性。

Phase 1 不支持跨 run 的 detached process。每个 process handle 都绑定 `executor_id/env_id/run_id/execution_id`；`write_stdin`、`read_output`、signal 和 terminate 必须重新验证该绑定。未来若支持常驻进程，必须新增独立 resource、owner lease、配额和显式审批，不能复用已结束 run 的 capability。

### 8.3 本地安全边界

agentx 在请求进入 stdio exec-server 前执行第一层校验，exec-server 的 sandbox/network policy 再执行第二层约束。至少包括：

- `cwd` 和所有路径先 canonicalize，再验证位于注册 root 内；
- 防 symlink/TOCTOU，能使用 `openat`/no-follow 时不只依赖字符串前缀；
- 环境变量按 allowlist 合并，剥离 executor credential、代理 secret、exec-server `CODEX_HOME`/runtime path 和宿主敏感变量；
- 执行用户、sandbox 和网络权限取“远程 run policy ∩ 本地 owner policy”的交集；
- 限制最大 argv/env/frame、输出 buffer、进程数、运行时间和并发数；
- 默认禁用可选 `http/request`；开启时必须声明目标网络策略并经过审批。
- exec-server 自身必须运行在清洗后的环境与隔离 Codex home 中；模型 key、用户 Codex 登录态、agentx OAuth material 和 llmproxy 地址一律不可见；
- binary/helper 的版本、签名或 checksum 不匹配时 agentx 必须 fail closed，不能回退到系统中另一个 Codex。
- agentx 控制面与 exec-server/命令执行树应使用不同 OS uid、container/user namespace 或等价权限域；机器私钥优先使用 OS keychain/TPM 的 non-exportable key；
- agentx 的 credential、WSS、keychain 和内部 pipe/file descriptor 必须 close-on-exec 且对子进程不可访问；需测试 ptrace、`/proc`、signal、继承 FD 和本地 socket 攻击；
- BYO 机器的 root/本机 owner 能控制 agentx，属于明确的信任边界；但工作树中的不可信代码不能因此获得 executor 机器身份。

## 9. agentx 接入与 exec-server 转接协议

### 9.1 Enrollment

1. owner/maintainer 在 core 创建 executor resource。
2. core 返回绑定 `executor_id`、短期、单次使用的 enrollment token；推荐通过 `agentx enroll --token-stdin` 输入，避免写入 shell history。
3. agentx 本地生成机器密钥，提交 token、公钥、平台、workdir roots，以及通过 manifest/本地握手确认的 exec-server version、binary digest 和 capability。
4. core 消耗 token，并为该 executor 建立唯一机器身份。优先使用 Hydra `private_key_jwt` client；若只能使用 client secret，必须单机唯一、可轮换、可吊销并存入 OS keychain。
5. agentx 获取短期 `aud=executor-gateway`、`scope=executor:connect` access token。
6. 删除 enrollment token；后续只使用机器身份换短期 token。

workspace 共享的永久 service token 不得用于 executor enrollment 或连接。

### 9.2 连接与恢复

1. agentx 主动拨出 WSS，不要求用户环境开放入站端口。
2. executor-gateway 校验 audience/scope、executor 状态、公钥绑定和实时 workspace 归属。`executor_id` 从已验证 token subject 取得，不能信任客户端声明。
3. agentx 使用登记私钥完成 DPoP/`cnf` proof；若 Hydra 无法签发 key-bound token，则在 WSS 建连后完成 gateway nonce 签名挑战。只有 bearer token 而没有私钥证明不能建立执行通道。
4. agentx 先发送签名的 lifecycle hello，声明 agentx protocol、各 env 的 pinned exec-server build/schema、`environment/info` 摘要和 `localExecInstanceId`，并携带可选的上一个 `resumeSessionId`。这是恢复提示，不是可信身份；gateway 必须用注册 manifest 核对 build/schema，并同时从 connection lease 读取上次 session id，冲突时要求重新建 session。
5. gateway 在已建立的双向通道上发送：

   ```json
   {
     "jsonrpc": "2.0",
     "id": "init-...",
     "method": "initialize",
     "params": {
       "protocolVersion": "...",
       "clientName": "agentserver-executor-gateway",
       "resumeSessionId": "optional"
     }
   }
   ```

6. agentx 在远程层处理 `initialize`，返回 `sessionId`、协商后的 protocol version 和“pinned schema allowlist ∩ child 已确认字段 ∩ 本地 owner policy”后的 capabilities，随后接收 `initialized`。这组 lifecycle 消息不会再次转发给已经初始化过的 stdio child，远程 `resumeSessionId` 也不能传给 stdio child。
7. 每个 executor 同时只有一个有效 connection generation。新连接用 CAS 增加 generation，并 fence/关闭旧连接；connection lease 同时记录最近的 `exec_session_id`。
8. 完成远程 `initialized` 后，标准 process/fs 请求由 agentx 做策略检查和 request-id 映射；其 method/params 按 pinned schema 做语义等价转发，不能声称整个 envelope 或 request id 原样不变。child notification/response 反向映射回 gateway。
9. 断线按 1、2、4、8……30 秒指数退避重连；在 30 秒 grace period 内携带 `resumeSessionId`，且不关闭 stdio child。access token 到期前必须重新认证或重连；executor 被吊销后，gateway 主动 fence 连接，agentx 结束本地 exec session。
10. resume 失败、grace period 到期或 gateway 要求全新 session 时，agentx 关闭旧 stdio、等待 child 清理 managed process，再启动并初始化新的 exec-server；旧 `process_id` 不得迁移。

WSS 上的每个业务 RPC 使用 agentserver routing envelope 包住 Codex exec-server dialect。`sessionSeq` 在每个发送方向独立单调递增，`ack` 是对端已连续处理的最大 sequence；stock 内层省略 `"jsonrpc":"2.0"`：

```json
{
  "type": "rpc",
  "sessionId": "exec-session-...",
  "sessionSeq": 1042,
  "ack": 991,
  "generation": 7,
  "context": {
    "workspaceId": "...",
    "runId": "...",
    "executionId": "...",
    "envId": "...",
    "mutationKey": "..."
  },
  "rpc": {
    "id": "...",
    "method": "process/start",
    "params": {}
  }
}
```

agentx 校验 envelope 的 session sequence、generation、workspace/env 绑定和本地 policy，以 `env_id` 选择对应的 local exec instance，将 context 记入 ownership/audit，分配新的本地 request id，再编码为 stock dialect 写入 child。child 返回的 notification 根据 `process_id` 由 agentx 补回可信 context；不能信任远端为已有 process 任意声明另一个 run/execution。

远端基础 RPC 的 method/params/result/notification schema 必须与 pinned exec-server 对齐：

- `environment/info`、`environment/status`；
- `capabilityRoots/discoverV1`（Phase 1 默认不向远端公布）；
- `process/start`、`process/read`、`process/write`、`process/signal`、`process/terminate`；
- `process/output`、`process/exited`、`process/closed` 通知；
- child → client 反向请求 `network/policyRequest`；
- `fs/readFile`、`fs/open`、`fs/readBlock`、`fs/close`、`fs/writeFile`；
- `fs/createDirectory`、`fs/getMetadata`、`fs/canonicalize`、`fs/readDirectory`、`fs/walk`、`fs/remove`、`fs/copy`；
- 可选 `http/request`，v2 默认不协商该 capability。

`process/start` 必须携带调用方生成的稳定 `processId`、`argv`、`cwd`、env policy、TTY、sandbox 和 network policy。输出使用单调 sequence；stdin 写入使用 `writeId`。

`capabilityRoots/discoverV1` 可枚举本地 skill/plugin manifest，不是双手执行任务的必要能力，Phase 1 必须在 agentx 层拒绝；未来只有在 owner 显式授权具体 root 与文件类型后才能开放。`http/request` 同样不能仅因 child 支持就出现在远程 capability 中。

stock child 发出的 `network/policyRequest` 参数实际为 `{processId, request: {protocol, host, port}}`，由 agentx 作为本地 client 处理：

- agentx 先用 `process_id` 找回可信 run/execution/env ownership，未知 process 一律 deny；
- 本地 owner policy 为 deny 时立即 deny；远程策略不能覆盖；
- 本地 owner policy 为 ask 时必须通过本机 owner approval channel；Phase 1 未实现该 channel 前按 deny 处理，远程 workspace 用户不能替本机 owner 批准；
- 需要远程 `ask` 时，agentx 以反向 routing envelope 发给 gateway，由同一 approval 流程决定；断线、超时、审批过期或参数不合法一律 fail closed；
- 只有本地与远程策略的交集允许时才返回 `allow`；
- 未协商或未来新增的 child → client request method 默认拒绝，不能由 agentx 自动回答 allow。

agentserver 特有能力必须通过远程 initialize capability negotiation 显式协商，并使用独立命名空间。例如 `agentserver/fsWriteFileIfMatch` 由 agentx 本地实现，为 `apply_patch` 提供原子条件写；它不发送给 stock child。不支持扩展的 agentx 仍可转发本地 policy 允许的 upstream 基础方法。

upstream `exec-server --remote` 使用 ChatGPT environment registry、rendezvous 和 Noise。v2 不进入该代码路径：exec-server 只连接本机 agentx 的 stdio；agentx 使用 TLS/WSS + OAuth key binding 连接可信 executor-gateway。gateway 是授权、审批、审计和协议翻译边界，必须读取 RPC。若未来把它降级为不可信 relay，必须重新设计端到端加密。

### 9.3 传输可靠性

JSON-RPC request id 只用于关联响应，不等于副作用幂等。协议还必须定义：

- mutation dedupe key 和有限的去重窗口；
- process id 冲突语义；
- fs 条件写/precondition hash；
- 每连接最大 inflight、frame size、输出 buffer 和 backpressure；
- cancel、signal、terminate 及其竞态结果；
- output cursor 缺口和 buffer overflow 的显式错误；
- 外层双向 `sessionSeq/ack`、连接 generation/lease 和 resume 窗口；
- 对结果不明的副作用返回 `ambiguous`，禁止 gateway 自动重试。

在恢复窗口内，两个方向的发送方都必须保留未 ACK 的外层 frame；恢复时只以相同 `sessionSeq` 重传，接收方对重复 sequence 重新 ACK 而不重复投递。副作用 request 还必须复用原 `mutationKey`。若任一方已丢失对端所需的 sequence 范围，则返回 `resume_gap` 并终止该 exec session，不能从当前最新帧继续冒充完整恢复。

agentx 在一个 `exec_session_id` 的 grace period 内维护有界的 `mutationKey → pending/completed response` journal。重复 key 返回同一结果或 pending 状态，不再转发给 child；journal 丢失、child crash 或无法判断是否已执行时返回 `ambiguous`。该 journal 不是跨 agentx 重启的“恰好一次”承诺。

外层 session sequence 覆盖 response、notification、reverse request 和 lifecycle，不只覆盖 process output。agentx 必须持续读取 child stdout，不能因为 WSS backpressure 停止排空 pipe；缓存超限时产生带丢失范围的 `output_gap/buffer_overflow`，不能静默截断。当前 pinned stock child 的 retained output 上限和 exited-process retention 必须写入 manifest/conformance fixture；不能把 upstream 的有限 `process/read` replay 当作无限恢复日志。

stdio child 只服务一条本地连接，pipe EOF 后 processor shutdown，不能由新的 agentx 重新 attach。`resumeSessionId` 只属于 agentx 外层 WSS；agentx 或 child crash 后必须创建新的 `local_exec_instance_id`，旧 process/fs handle 全部失效。

WSS lifecycle/routing envelope、JSON-RPC schema 和错误码必须在 `docs/protocols/` 下提供机器可读 JSON Schema/AsyncAPI，并用录制 fixture 做 gateway ↔ agentx ↔ stock child 双向兼容测试。envelope 只承载连接、路由、审计和幂等元数据；确定性 process/fs 指令仍位于未经语义改写的内层 JSON-RPC。

### 9.4 多副本路由

WSS 是有状态连接，executor-gateway 不能只依赖普通 Service 负载均衡宣称高可用。

Phase 1 明确把 executor-gateway 部署为单副本。它只承诺在同一 gateway 进程存活期间恢复短时网络断线；gateway 进程重启后拒绝旧 `resumeSessionId`，仍为 `prepared` 的 operation 保持未发送，已处于 `dispatching|acknowledged` 且尚未由 core 或 agentx journal 证明终态的 operation 标记 `unknown`，agentx 在 grace period 后回收旧 stdio child。数据库中的 connection generation 能 fence 旧写入，但不能替代丢失的双向 frame journal，因此 Phase 1 不宣称跨 pod 恢复。该故障域必须进入 SLO、告警和故障注入测试。

Phase 2 若需要多副本和跨 pod resume，必须先实现以下 owner routing 与可恢复 frame/session journal：

- owning pod 写入 `{executor_id, pod_id, generation, expires_at}` lease；
- MCP 请求先解析 executor owner，再通过内部认证 RPC 路由到 owning pod；
- heartbeat 续租；新连接以 CAS 增加 generation，旧 generation 的响应全部丢弃；
- pod 失效后等待 agentx 重连并重新取得 owner，不能把旧 process 请求盲发到新连接。

## 10. 事件、前端与审批

### 10.1 规范事件模型

所有可恢复事件使用追加写模型：

```json
{
  "event_id": "uuid",
  "schema_version": 1,
  "seq": 42,
  "workspace_id": "...",
  "session_id": "...",
  "run_id": "...",
  "run_attempt_id": "...",
  "run_attempt_generation": 3,
  "producer_instance_id": "...",
  "producer_seq": 81,
  "source": "brain|executor|system|approval",
  "type": "...",
  "timestamp": "...",
  "payload": {}
}
```

- `seq` 在一个 run 内单调递增，由 core/event writer 分配。
- `run_attempt_id/run_attempt_generation` 对 attempt-scoped 事件必填；run 创建、排队等发生在 attempt 之前的 run-scoped 事件为 `null`。core 必须拒绝 attempt-scoped producer 缺少或携带旧 generation 的事件。
- 每个 producer 在本实例内持有单调 `producer_seq`，并在进程重启或 lease generation 变化时生成新的全局唯一 `producer_instance_id`；core 对 `(run_id, producer_instance_id, producer_seq)` 建唯一约束并返回已分配的 run `seq`。仅依赖随机 `event_id` 无法覆盖“发送后、保存原 id 前崩溃”的重试场景，也不能依赖 PostgreSQL 对 nullable generation 的默认唯一性语义。
- `type + schema_version` 决定 payload schema；消费者必须忽略未知的可选字段，并显式拒绝不支持的破坏性版本。
- agentx 的 process output sequence 是每个 process 的传输/恢复游标，与本节的 run event `seq` 相互独立，不能直接复用。
- ingestion 至少一次，`event_id` 和 producer key 双重去重；消费者不能依赖恰好一次投递。
- 小 payload 存 Postgres；大 stdout/stderr、图片或制品先加密上传临时对象，事件事务只保存已验证的 hash、大小、media type 和内部对象 id。提交失败的 orphan 异步清理；不能持久化长期 presigned URL，读取仍需经过 core 授权与 retention policy。
- 规范事件 payload 在持久化前做 secret/prompt policy 过滤。过滤后的事件不是模型恢复事实；完整、模型可见内容进入独立加密 checkpoint/conversation record，遵循 retention/删除策略。checkpoint secret scan只用于发现不应出现的 runtime credential并拒绝/quarantine对象，不能对模型可见 rollout做字节替换后冒充等价恢复。
- 浏览器使用 `run_id + cursor` 重连；browser-gateway 因而可以无持久状态。cursor 已过 retention window 时返回明确 `cursor_expired` 和授权后的 run snapshot/rebase cursor，不能返回空流冒充无新事件。

### 10.2 两套上游协议不得混用

- 大脑只使用 stock Codex app-server v2 的 thread/turn/item 协议。
- executor 只使用 stock exec-server process/fs JSON-RPC；agentx 远程 lifecycle 和命名扩展另行版本化。
- app-server 的 `mcpToolCall` 与 `item/mcpToolCall/progress` 映射为规范 MCP 事件，再映射到 AG-UI `TOOL_CALL_*` 和 A2UI 卡片。
- executor 的 process output 是 MCP progress/result 的内容，不伪装成大脑 command item。

### 10.3 Approval

策略值为 `deny | ask | allow`，至少按 workspace、executor、env、tool、tool schema/version、path root、network、run actor 和 policy version 配置。Phase 1 的副作用入口只有 executor-gateway；第三方 MCP 只开放经管理员固定并验证的只读工具，未知、错误标注或无法验证的工具一律 `deny`。未来若增加第三方副作用，必须先让调用经过同一 MCP policy proxy/core approval，不能依赖 server 自报 annotation。

executor-gateway/core 是产品审批的唯一策略权威。app-server 针对 executor MCP 配置为不再自行产生第二套通用 tool prompt；需要用户决定时，由受信任的 executor-gateway 发起标准 MCP elicitation，harness-worker 只负责把 app-server server request 与 core 的 approval record 双向关联。approval record 必须持久化绝对 `expires_at`，core 用 CAS 决定 `approved|declined|expired|cancelled`，worker 依据同一 deadline 设置本地兜底 timer，不能让 reverse request 无限悬挂。

approval TTL 与 MCP `tool_timeout_sec` 是两个独立时钟。已刻画的 stock 0.146.0 在 pending elicitation 期间暂停 tool active-time timeout，所以无论 TTL 是否小于 `tool_timeout_sec`，Codex 都不会替产品自动清理无人回答的审批。core 到期 CAS 成功后必须主动向 worker 下发 `expired`，worker 将该 canonical outcome 作为 `decline` 回复 app-server；若 control stream、app-server response path 或 core expiry确认不可用，则在有界 cleanup grace 内调用 `turn/interrupt`，等待 reverse request 清空后退出。显式 run cancel 同样使用 interrupt。`tool_timeout_sec` 只约束 elicitation 结束后恢复的 MCP active execution；任何超时、interrupt、断线或清理路径都不得 dispatch，并须保留彼此不同的审计原因。

对 official stable 0.146.0 的 A05 probe 已确认这一配置语义：同一个明确标注 `readOnlyHint=false`、`destructiveHint=true`、`openWorldHint=true` 的工具，在 granular policy 下使用 `approve` 会直接到达 MCP `tools/call`，全程没有 app-server reverse request；仅把默认值改为 `prompt` 时，会出现 `_meta.codex_approval_kind = "mcp_tool_call"` 的 `mcpServer/elicitation/request`，取消后不会 dispatch。该结论只消除了 Codex 自己的第二层通用审批，不能替代 A06 对 gateway 主动 elicitation、client 决策、超时和取消语义的验证。

同一 stock 0.146.0 的 A06 probe 已验证相反方向的标准协议链路：executor fake MCP 在模型触发的原 `tools/call` Streamable HTTP SSE 中发出真正的 `elicitation/create`，app-server 将 form schema、execution `_meta`、thread/turn 和 server identity 原样关联给 client；client 的 `accept|decline|cancel` 分别回到 MCP，且正常 client response路径中 `serverRequest/resolved` 先于 turn terminal，tool result随后进入下一次模型请求。相同非空 form 在 `approval_policy = "never"` 下不会上浮 reverse request，而是直接向 MCP 返回 `decline`，因此生产 thread 不能误用 `never`。追加的 timeout probe 把 `tool_timeout_sec` 设为 0.5 秒并让 client 持有 form 1.5 秒，期间没有 resolved、terminal 或第二次模型请求，显式 `cancel` 后才继续，证明该 timeout 在 elicitation 中暂停。这些 probe 仍不构成 core approval nonce、主动 TTL expiry 和 generation 校验。

A07 在 alpha.14 与 stable 0.146.0 上还确认了 interrupt清理，但暴露了不能忽略的事件顺序：client尚未回答 form时调用 `turn/interrupt`，app-server会返回成功、以 `interrupted` 结束 turn、清除 pending reverse request并向 MCP回 `cancel`，也不会产生第二次模型请求或 MCP call；可是实际 wire 是 `turn/completed(interrupted)` 先于 `serverRequest/resolved`。因此 harness-worker 的 finalization barrier必须同时等待 terminal turn、所有已登记 reverse request ID resolved，以及 process收口；看到 terminal就关闭 stdin会丢失清理事件。timeout、control stream断线与 child crash仍分别 fail closed，不能从这个 interrupt probe外推为已验证。

`ask` 流程必须：

1. `PrepareExecution` 在 core 创建 execution，并冻结规范化参数、tool/schema version、workspace/run/attempt generation、executor/env、policy version 和目标资源；
2. 对上述完整上下文生成 hash、审批 id、过期时间和一次性 nonce，不能只 hash 模型提供的 MCP arguments；
3. gateway 发起 MCP elicitation，harness-worker 将它映射为规范事件和 AG-UI interrupt/A2UI 审批卡；
4. core 校验 approver 当前 workspace 角色、自批规则、run 状态、审批 TTL 和参数 hash；
5. gateway 在 dispatch 前再次检查 live RBAC 与 generation，并以 CAS 消耗 nonce、将 execution 置为 `dispatching`；同一 approval 只能消费一次；
6. 将 requester、approver、时间、决定、参数 hash、execution id、最终结果或 unknown 写入审计。

即使服务端批准，agentx 本地策略仍可拒绝。

### 10.4 Web 安全

- a2ui-web 只在内存中持 `aud=agentserver-api` token；刷新使用 Authorization Code + PKCE/refresh-token rotation 或重新授权，不使用 localStorage。若未来让 core 与 browser-gateway 使用不同 audience，必须显式取得两个 token 或做标准 token exchange，不能把一枚 token 跨 audience 接受。
- AG-UI/SSE 使用支持 `Authorization` header 的 `fetch` streaming，并显式携带 event cursor；不能依赖无法设置 bearer header 的原生 `EventSource`。如果改用 HttpOnly BFF cookie，则必须把该 cookie session、CSRF 和注销语义建模，不能同时宣称不存在浏览器会话。
- Hydra login/consent bridge 为每次 challenge 保存短期、单次使用且绑定 state/nonce/PKCE 的登录事务；回调、重放、账户映射和 logout/revocation 都必须有明确状态机，不能把 SPA 的“token 只在内存”误当成 bridge 无需会话保护。
- 配置严格 CSP、可信回调 URL、SameSite/CSRF 防护和最小 CORS。
- 前端日志走 RUM/telemetry，并做隐私过滤；浏览器没有“输出 JSON 到 stdout”的保证。

## 11. Core 数据模型

建议的一等资源：

```text
users ──< workspace_members >── workspaces
                                   ├─ sessions ──< runs
                                   │      │         ├─ run_attempts / attempt_leases
                                   │      │         ├─ run_events
                                   │      │         ├─ executions
                                   │      │         │   ├─ execution_operations
                                   │      │         │   └─ approvals
                                   │      │         ├─ conversation_checkpoints
                                   │      │         └─ run_outbox
                                   │      └─ session_leases
                                   ├─ executors ──< executor_environments
                                   │             └─ executor_connections / audit
                                   ├─ llm_authorizations
                                   ├─ credentials
                                   └─ quotas
```

原设计中的 `browser`（页面 + token + session）不再是一等控制面资源：SPA 是全局静态应用，token 属于浏览器登录态，session 已独立建模。如果未来需要保存用户 UI 偏好，应定义为 user/workspace preference，而不是 browser runtime。

关键状态机：

- run：`queued → starting|cancelled`；`starting → running|failed|cancelled|interrupted`；`running → finalizing|failed|cancelling|interrupted`；`cancelling → finalizing|cancelled|failed|interrupted`；`finalizing → completed|cancelled|failed|interrupted`。`finalizing` 覆盖 child 优雅退出、process 收口、checkpoint 上传和 CAS 提交；这些步骤完成前不能对外宣布 completed。cancel 与自然完成竞态时允许以已确认的真实 terminal result 收口，不能强行覆盖为 cancelled；
- run attempt：`created → leased → starting → running → finalizing → succeeded|failed|interrupted|fenced`。只有旧 attempt 尚未让 app-server 接受 `turn/start` 时才可自动创建新 attempt；任何 mid-turn 失败都使 run 进入 `interrupted`；
- execution：`created → pending_approval|approved|denied|cancelled`；`pending_approval → approved|denied|expired|cancelled`；`approved → dispatching|expired|cancelled`；`dispatching → running|failed|cancelling|unknown`；`running → succeeded|failed|cancelling|unknown`；`cancelling → cancelled|succeeded|failed|unknown`。跨过 `dispatching` 后，只有收到 agentx/child 的确定拒绝或退出确认才能记为 `failed|cancelled`，否则必须为 `unknown`；
- execution operation：一次 execution 的每个确定性 RPC/副作用步骤各有一行，例如 process start、stdin write、timeout terminate 或条件写。状态为 `prepared → dispatching → acknowledged → succeeded|failed|cancelled|unknown`；每行拥有独立 `mutation_key`、参数 hash 和 effect class。execution 只是 MCP 工具级聚合，不能用一个 mutation key 覆盖多个可独立发生的副作用；
- approval：`pending → approved|denied|expired|cancelled`；`approved → consumed|expired|cancelled`。只有未过期的 approved 可通过唯一 CAS 进入 `consumed`，消费时同时推进对应 execution；
- executor：`enrolling → offline ↔ online`；任一非终态可进入 `revoked`。

所有状态变更带 expected version/fencing generation，防止旧 harness-worker 或旧 WSS 连接回写新状态。`dispatching` 是副作用不确定性边界，不允许通过普通重试回退到 `created|approved`。

## 12. 仓库布局建议

```text
v2/
├─ cmd/
│  ├─ agentserver-core/
│  ├─ browser-gateway/
│  ├─ harness-pool/
│  ├─ harness-worker/            # per-run app-server stdio host；与 harness-pool 同属一个产品组件
│  └─ executor-gateway/
├─ internal/
│  ├─ core/
│  ├─ browsergateway/
│  ├─ harnesspool/
│  ├─ harnessworker/
│  ├─ executorgateway/
│  └─ shared/
│     ├─ auth/
│     ├─ capability/
│     ├─ credential/
│     ├─ events/
│     ├─ execprotocol/
│     ├─ mcp/
│     ├─ tenancy/
│     └─ obs/
├─ api/
│  ├─ openapi/                   # REST
│  └─ asyncapi/                  # SSE/WSS
├─ a2ui-web/
├─ deploy/helm/
├─ images/harness/               # harness-worker + pinned stock Codex app-server
├─ packaging/agentx/
│  ├─ runtime-manifest.json      # agentx 独立发行包必须消费的 stock Codex/helper 版本、签名与 digest
│  └─ compatibility-fixtures/    # server ↔ agentx 跨仓兼容 fixture
└─ docs/
   ├─ ARCHITECTURE.md
   └─ protocols/
      ├─ canonical-events.md
      ├─ harness-worker-control.asyncapi.yaml
      ├─ conversation-checkpoint.md
      ├─ executor-mcp.md
      ├─ exec-server.schema.json
      └─ agentx-wss.asyncapi.yaml
```

agentx 的实现不放入上述 Go module。`github.com/agentserver/agentx` v2 以独立仓库从零实现 connector、owner policy、stdio proxy 与 child supervisor；旧的“从 Codex hard-fork exec-server/remote”实现不复用。两仓以本仓库发布的 versioned schema、fixture 和 runtime manifest 对齐，并在 release CI 跑交叉版本兼容矩阵。

## 13. 工程与可观测性约定

### 13.1 API 契约

- 控制面 REST 采用 contract-first：提交的 OpenAPI 3.x 是 source of truth，生成 Go strict server/client 与前端类型，CI 重新生成并检查 drift；handler annotation 不能反向成为 v2 契约源。
- AG-UI/SSE、规范事件、harness-worker control stream 和 agentx WSS 同样以提交的 AsyncAPI/JSON Schema 为 source of truth；手写语义校验补充 JSON Schema 无法表达的 generation、sequence 和状态约束。
- protocol version、capability negotiation 和错误码必须显式版本化。
- 实现前 pin 一个 Codex commit/version，并保存与 upstream exec-server 的 conformance fixtures；升级 Codex 时先跑协议差异检查。
- 同一 pinned Codex build 还必须生成 app-server schema fixture，验证 initialize、thread start/resume、turn start/interrupt、MCP tool call、elicitation/server request、terminal event 与 rollout checkpoint 布局。

### 13.2 日志、指标和审计

后端与 agentx 使用结构化 JSON 日志。公共关联字段至少包括：

`trace_id`、`workspace_id`、`session_id`、`run_id`、`run_attempt_id`、`run_attempt_generation`、`harness_worker_id`、`brain_thread_id`、`execution_id`、`operation_id`、`executor_id`、`env_id`、`local_exec_instance_id`、`process_id`。

执行审计至少记录 actor、workspace、run、executor/env、tool、清洗后的参数摘要、参数 hash、approval、开始/结束时间、结果和错误分类。不得记录 token、credential、完整环境变量或未经过滤的用户内容。

必须建设的指标包括：

- run queue/start latency、harness cold start、worker/app-server child start/crash、control-stream reconnect/fence、active run 和 attempt 数；
- completed-turn checkpoint upload/commit/restore latency、hash/schema failure 和 orphan object 数；
- MCP latency、approval latency、execution success/failure/unknown；
- pre-dispatch persistence latency、execution/operation `dispatching` stranded/ambiguous 数、重复 tool-call/mutation 命中数；
- executor online、reconnect、generation fence、RPC inflight；
- exec-server child start/crash/restart、process-tree cleanup、reverse network-policy decision/timeout；
- output dropped bytes、buffer overflow、SSE lag、event persist latency；
- llmproxy 用量、限流和上游错误，且不能以 executor credential 作为维度。

## 14. 关键决策

| # | 决定 | 原因 |
|---|---|---|
| D1 | per-run harness-worker 通过 stdio 驱动 stock app-server；双手运行 stock exec-server stdio | 两个 upstream server 都有明确本地 client/supervisor，双手仍没有模型、模型凭证或 ChatGPT remote auth |
| D2 | agentx 监管/代理 exec-server，不在 Go 中重写标准 handler | 复用 upstream PTY/fs/sandbox 行为并减少协议漂移与安全重实现 |
| D3 | executor-gateway 同时是 MCP server 和 agentx 远程 client/router | 工具授权、审批和执行协议在一个清晰边界完成 |
| D4 | 大脑只用 app-server v2，executor 只用 process/fs JSON-RPC | 避免 thread/item 与 process/output 事件语义混淆 |
| D5 | core 持有 session/run/event 事实 | browser gateway 和 harness 均可无状态恢复 |
| D6 | Phase 1 只做 remote/BYO executor | managed sandbox 生命周期和存储尚未完整设计 |
| D7 | 用户 token 不沿内部链透传 | 降低 bearer 泄漏与 confused-deputy 风险 |
| D8 | 不自动重放结果不明的副作用 | 网络重试不能保证进程/fs 操作恰好一次 |
| D9 | Phase 1 warmup 采用镜像/运行时缓存 | 避免跨租户复用 Codex 进程和临时状态 |
| D10 | execution 在副作用 dispatch 前持久化，`dispatching` 是不可回退边界 | gateway/transport crash 后才能诚实区分未发送、已确认与 unknown，兑现不重放承诺 |
| D11 | native checkpoint 只在 completed turn 提交，UI/审计事件与模型可见历史分离 | 过滤展示内容不能破坏 app-server thread 恢复语义，mid-turn 不能伪恢复 |
| D12 | Phase 1 只有 executor-gateway 暴露副作用 MCP；第三方直连工具仅允许验证后的只读集合 | 第三方 annotation 不是可信授权事实，直连无法强制 core approval |
| D13 | executor-gateway/core 是唯一产品审批权威，使用 MCP elicitation 与 harness-worker 转接 | 避免 app-server 与 gateway 双重审批，并给 timeout/cancel/fence 明确语义 |
| D14 | app-server 外部流量经过受控 egress proxy，Pod内再以 app-server UID默认拒绝 OUTPUT | NetworkPolicy 既无法区分同 Pod worker/child，也无法按域名、TLS 身份和 redirect实施 run endpoint allowlist |
| D15 | Phase 1 executor-gateway 单副本，resume 只覆盖同进程短时断线 | 跨 pod resume 需要 durable frame journal 与 owner routing，不能只凭 connection lease 声称恢复 |
| D16 | agentx 保持独立仓库并从零改写为 stock exec-server supervisor | 现有 hard-fork 把执行引擎复制进 agentx，与 v2 的 stock stdio 边界冲突 |
| D17 | MCP-only 使用 `environments: []`、managed requirements、显式工具禁用与模型请求捕获共同证明 | system prompt 和单一配置开关都不能构成能力隔离 |
| D18 | execution 下增加 execution operation | 一个 MCP 工具可能触发多个独立副作用，每个步骤都需要自己的 mutation 与 unknown 边界 |
| D19 | v2 API 采用 contract-first | core、gateway、worker、agentx 和 Web 多消费者需要在实现之前共享稳定、可生成的协议源 |

## 15. 设计审查结论与实现门槛

原设计存在的主要问题已经在本文中修正：

- 把双手错误设计成第二个模型运行时；
- 将 `codex exec` 的模型事件与 `exec-server` 的确定性 RPC 混为一谈，且遗漏了可直接复用的本地 `--listen stdio` transport；
- harness 一边宣称无状态，一边拥有会话持久化和工作区能力；
- 缺少作为 app-server client 的 per-run host，无法实际发起 thread/turn、响应 server request 或完成 cancel/checkpoint；
- browser-gateway、harness-pool 和 core 同时声称拥有 session；
- 用户 bearer、workspace 机器 token 和模型凭证跨信任域复用；
- Hydra 缺少实际的 login/consent bridge；
- executor 重连缺少 generation fencing，多副本缺少连接 owner routing；
- process/fs 副作用缺少审批、幂等边界和 ambiguous 结果；
- execution 只在结果阶段记录，无法判断 gateway crash 是否已经 dispatch；
- 允许直连第三方 MCP，同时又宣称所有副作用都经过统一审批；
- 把已脱敏 UI 事件、选择性 rollout 和原生 thread resume 混为一谈；
- capability 续期、外部 endpoint egress 和浏览器 SSE bearer 传输缺少可执行机制；
- warm pool 的 controller、pod、process 和租户隔离层次冲突；
- app-server、executor RPC 和 AG-UI 之间缺少规范事件层。

进入实现前必须完成以下 Phase 0 gate：

- [ ] 固定 Codex 版本与 binary/helper digest，完成 stock exec-server stdio 启动、RPC fixture、正常关闭与崩溃时的 process-tree 回收、agentx 代理兼容测试。
- [ ] 完成 harness-worker → stock app-server stdio conformance：initialize、thread start/resume、turn start/interrupt、notification/server request、MCP elicitation、terminal event、child crash 和 control-stream fence。
- [ ] 验证 app-server child 无内建工具、整个 mount view无工作树；worker credential/FD不可见且 non-`CLOEXEC` sentinel也被 final-exec close-all；child UID只经 egress proxy访问 llmproxy + approved MCP，direct/redirect sink均为零请求，第三方副作用工具不可枚举/调用。
- [ ] 完成 completed-turn checkpoint 原生 round-trip、hash/schema 校验、对象原子提交和 mid-turn crash 不恢复原 turn 测试；模型可见 tool result 与脱敏 UI 事件分别验证。
- [ ] 完成 session lease、run-attempt lease/generation、producer idempotency、cursor-expired snapshot 和大 payload 临时对象/孤儿清理原型。
- [ ] 完成 `PrepareExecution → approval → dispatching → ACK/running → terminal` 状态机，并在 DB commit、WSS send、agentx ACK、MCP response 各边界注入 crash，证明未知副作用不会自动重放。
- [ ] 验证 executor 侧空 Codex home、清洗环境、无模型 credential、禁用 `--remote`/`http/request`，且不能回退到未校验系统 Codex。
- [ ] 完成 executor enrollment、独立机器身份、吊销、30 秒 WSS 恢复和 stdio child 跨重连存活测试。
- [ ] 完成 child → agentx `network/policyRequest` 的 allow/deny/ask、断线超时 fail-closed 和审批审计测试。
- [ ] 完成路径逃逸、symlink/TOCTOU、环境变量泄漏和 sandbox policy 安全测试。
- [ ] 完成 agentx 控制面与执行树的 uid/namespace、ptrace/`/proc`、继承 FD、signal 和 non-exportable key 隔离测试。
- [ ] 完成 MCP elicitation → core approval 的参数/上下文冻结、独立 approval expiry 主动清理、MCP active-time timeout、nonce 单次消费、cancel/断线 fail-closed 和审计闭环，证明不会出现 app-server/gateway 双重审批。
- [ ] 验证 Phase 1 executor-gateway 单副本部署、同进程 30 秒 resume，以及 gateway 重启后 fail-closed 拒绝 resume/operation 进入 unknown；Phase 2 owner routing 不进入首版。
- [ ] 验证 harness-worker/app-server crash 后不会自动重放已发出的 MCP 副作用，原 MCP transport 丢失时 execution 可独立收口而 run 明确 interrupted。
- [ ] 验证 `aud=agentserver-api`、fetch-streaming bearer、AG-UI cursor/rebase、显式 cancel、Hydra challenge 防重放和浏览器断线不取消 run。
- [ ] 验证 capability TTL 覆盖并不超过强制 `max_run_duration + grace`，且 lease fence/RBAC 变更能在 llmproxy 和所有 MCP 入口即时拒绝后续请求。

## 附录 A：Codex 对齐基线

本设计基于以下 upstream 实现语义，而不是 `codex exec` CLI：

- `codex-rs/exec-server-protocol/src/protocol.rs`：确定性 JSON-RPC 类型；
- `codex-rs/exec-server-protocol/src/rpc.rs`：exec-server Codex JSON-RPC dialect；wire 上省略 `jsonrpc` 字段；
- `codex-rs/exec-server/README.md`：`--listen stdio`、远程模式与连接 lifecycle；
- `codex-rs/exec-server/src/server/transport.rs`：stdio/WebSocket transport；stdio EOF 后关闭 processor；
- `codex-rs/exec-server/src/server/handler.rs`：process/fs handler；
- `codex-rs/exec-server/src/server/registry.rs`：方法路由；
- `codex-rs/exec-server/src/server/session_registry.rs`：upstream WebSocket session 保留语义；stdio 本身不可 detach/resume；
- `codex-rs/exec-server/src/remote.rs`：注册、WSS 和指数退避的参考；v2 不调用该 remote 路径；
- `codex-rs/cli/src/main.rs`：`codex exec-server` CLI、runtime path 和 remote auth 限制；
- Codex App Server 官方手册与 `codex-rs/app-server-protocol`：大脑 initialize、thread/turn/item、MCP tool call、elicitation/server request、interrupt 与 persisted rollout 语义；
- `codex app-server generate-json-schema`/对应 pinned schema artifact：harness-worker 的实际 wire 与 checkpoint conformance 基线。

实现时必须记录所对齐的 Codex commit 和发行包 digest。若 upstream wire schema 变化，以显式 protocol version、manifest 与兼容层处理，不能让 agentx 随 Codex 最新版本或用户 `PATH` 静默漂移。
