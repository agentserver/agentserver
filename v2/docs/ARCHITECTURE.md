# agentserver v2 — 架构设计

> 状态：设计基线（draft）
>
> v2 从零开始开发，不复用 v1 的运行时组件；集群侧代码位于本仓库 `v2/` 下。用户侧 agentx 继续在 `github.com/agentserver/agentx` 独立开发和发行，本仓库保存它所实现的 wire contract、兼容 fixture 与 runtime lock manifest。
>
> 本文中 stock Codex 有两个严格分离的运行角色：大脑由 per-run `harness-worker` 通过 stdio 驱动 stock app-server；双手由 agentx 监管本地 `codex exec-server --listen stdio`。app-server 子进程只运行模型循环，从 `dynamicTools` 看见冻结的工具目录，并以 `item/tool/call` 把结构化调用交还 worker；worker 才是 executor MCP client。exec-server 只处理确定性的 process/fs JSON-RPC，不运行模型。两侧的宿主进程都不替模型推理。

## 0. 架构约束

以下约束是 v2 的设计前提，不是实现选项：

1. **大脑与双手分离**
   - harness 是大脑：运行 stock Codex 的模型循环。
   - executor 是双手：只执行结构化、确定性的 process/fs 指令。
   - 产品工具边界是 MCP，但 stock app-server 不直接连接 MCP：harness-worker 把冻结的 MCP tool catalog 映射为 `dynamicTools`，再把 `item/tool/call` 机械转接为 MCP `tools/call`。
2. **双手没有模型**
   - agentx 不接受自然语言 prompt，不做推理，不访问 llmproxy，也没有任何模型凭证。
   - 标准 process/fs 执行由 pinned stock `codex exec-server --listen stdio` 完成；agentx 不在 Go 中重写这套 handler。
   - executor 侧禁止使用 `codex exec`、app-server、`exec-server --remote` 或任何需要模型/ChatGPT 身份的路径。
3. **harness 无状态、无本地工具能力**
   - `harness-worker` 是 app-server 的本地 JSON-RPC client/supervisor、executor MCP client 和控制面适配器；它不运行模型、不选择工具、不改写 prompt，也不拥有持久状态。
   - app-server 子进程的本地磁盘和进程都不是权威状态，销毁后必须可以从 completed-turn checkpoint 重建。
   - app-server 子进程除调用 llmproxy 外没有网络能力，也不持有任何 MCP endpoint 或 MCP bearer；它只可发出 client-hosted dynamic tool callback。
   - worker 除按冻结目录调用批准的 MCP server 外没有工具能力；禁止 worker 和 app-server 执行本地 shell、文件系统、apply-patch、浏览器、Web 搜索或任意未批准网络能力。
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
- harness-pool 以本地 `fork/exec` 启动无状态的 per-attempt `harness-worker`，由 worker 驱动 app-server stdio；worker 是短命数据面进程，不增加常驻产品组件。
- executor-gateway 向 harness-worker 提供 MCP 工具；worker 把冻结目录投影成 app-server `dynamicTools`，gateway 再把调用参数确定性地映射为 exec-server JSON-RPC。
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
                                                    │ controller/launcher │
                                                    └──────────┬──────────┘
                                                               │ fork/exec per-attempt process
                                                               ▼
                                                    ┌─────────────────────┐
                                                    │ harness-worker      │
                                                    │ attempt process     │
                                                    │ MCP client/bridge   │
                                                    │   └─stdio app-server│
                                                    │ Codex：模型+dynamic │
                                                    └─────┬────────┬──────┘
                                          app模型调用     │        │ worker MCP
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

除了 5 个产品组件，部署还依赖 Hydra、Postgres、对象存储和 llmproxy。`harness-worker` 是 harness-pool 在本地按 attempt 启动的短命数据面进程，不是第六个常驻服务，也不会为每个 run 创建 Kubernetes Job/Pod。外部 OIDC IdP 和用户侧 agentx 不计入集群组件数。a2ui-web 的 workspace/resource REST 直连 core；图中经过 browser-gateway 的业务链路仅指 AG-UI run/事件接口。

## 3. 组件职责

| 组件 | 唯一职责 | 明确不负责 |
|---|---|---|
| **a2ui-web** | 静态 SPA；OIDC Authorization Code + PKCE；AG-UI client；A2UI 渲染 | 不保存服务端会话状态，不持久化 access token |
| **agentserver-core** | workspace/RBAC；session/run；事件；审批；executor/credential/LLM authorization 控制面；Hydra login/consent bridge | 不运行 Codex，不代理模型，不托管 SPA |
| **browser-gateway** | workspace 显式的 AG-UI/SSE 边缘；鉴权委托；规范事件到 AG-UI/A2UI 的映射 | 不创建权威 session，不持久化浏览器会话，不拥有运行状态 |
| **harness-pool controller** | 从 core 的`run.queued`专用durable delivery lane领取任务；持有 session/run-attempt lease；有界地 fork/exec 并回收 per-attempt worker 进程；汇聚事件，并在进程组停止后以受信本地finalizer生成、上传和提交 checkpoint | 不消费其他event outbox kind，不复用已运行过 turn 的 worker/app-server 进程，不拥有 session/run/event 事实 |
| **harness-worker**（per-run） | 作为 app-server stdio client 驱动 thread/turn；校验冻结的 executor tool catalog并生成 `dynamicTools`；把 `item/tool/call` 转成 MCP `tools/call`；转接 MCP elicitation；执行 cancel/fence和child监管；child退出后上报受限rollout locator | 不推理、不选工具、不改写 prompt、不在本地执行工具、不读取app UID私有rollout、不拥有持久状态 |
| **stock app-server**（worker 子进程） | 运行模型循环；调用 llmproxy；对 client-hosted `dynamicTools` 发出结构化 callback | 不访问 MCP、工作树、core、对象存储或 harness-pool 控制接口，不执行本地工具 |
| **executor-gateway** | 向 harness-worker 暴露 executor MCP；鉴权/策略/审批；MCP 到 exec-server RPC 的确定性翻译；连接路由 | 不推理，不改写自然语言，不直接执行 OS 操作 |
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
| `tool_catalog_digest` | 一个 brain thread 冻结的 dynamic tool 名称/description/input schema 集合摘要 | core（版本化 catalog 记录），checkpoint 绑定 |
| `execution_id` | 一次 dynamic callback 经 worker 转接后的 executor MCP 工具调用 | core（由 executor-gateway 上报） |
| `executor_id` | 一台注册的 agentx 机器身份 | core |
| `env_id` | executor 暴露的一个受控执行环境/工作根 | core + agentx |
| `exec_session_id` | agentx 协议连接的临时会话，用于短时断线恢复 | agentx/executor-gateway |
| `local_exec_instance_id` | agentx 为一个受管 process 或一次性 fs lane 监管的一次 stock exec-server stdio 子进程生命周期 | agentx |
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
| 模型可见 completed-turn checkpoint | 加密对象存储，DB 原子提交 manifest/hash | pool持有的attempt临时`CODEX_HOME`，进程组停止后读取 |
| brain thread tool catalog、canonical bytes 与 digest | Postgres/受版本控制的 contract artifact | worker 的解析结果 |
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
| 短期 run capability | core 按 run 和 audience 分别签发 | app-server child → llmproxy；harness-worker → executor MCP |

每枚 run capability 的公共 claim 至少绑定 `workspace_id`、`session_id`、`run_id`、`run_attempt_id`、`run_attempt_generation`、`user_id`、`aud`、过期时间和 `jti`。`aud=llmproxy` 只增加允许的 model/provider route；`aud=executor-gateway` 才增加允许的 executor/env/tool 和 `tool_catalog_digest`，不能把执行权限塞进模型 token，也不能把一枚 token 同时用于模型与执行。共享的 `agentserver-api` audience 只覆盖两个浏览器入口，不得被任何内部服务或 executor 接受。executor-gateway 的 `tools/list` 按 capability 中的 catalog digest 返回 core 已冻结的精确 catalog；未知或不匹配 digest 直接拒绝。

用户 bearer 不进入 harness、executor-gateway 或 agentx。browser-gateway 使用内部身份调用 core 的 authorize API，并将目标 workspace/action 一并提交；core 只向 browser-gateway 返回 actor context 与 run handle。run capability 在 harness-pool 持有效 session lease 和 run-attempt lease 后签发给目标 worker 进程，不能经浏览器链路转交。

Phase 1 不尝试修改已启动 app-server 的环境变量来轮换 llmproxy capability。系统必须定义并强制 `max_run_duration`；每个 capability 的 TTL 覆盖该上限与很短的收尾 grace，但 llmproxy 和 executor-gateway 在每次请求时仍校验 live run-attempt lease/generation，因此取消、fence 或成员移除可以立即生效。executor MCP capability 只由 worker 持有，可以在不触碰 app-server 环境的情况下轮换。超过硬上限的 attempt 必须被中断，不能带过期 token 继续运行。

### 5.3 凭证存储

LLM 上游 key、外部服务 credential 和 executor 机器材料都必须：

- 使用 KMS envelope encryption；
- 每条记录使用随机 nonce；
- AAD 至少包含 workspace、credential type 和 record id；
- 保存 key version，支持轮换和旧版本重加密；
- 记录读取、更新和使用审计；
- 永不写入日志、规范事件 payload 或 Codex rollout；运行时传递必须最小化、短期化且不可持久化。

llmproxy 根据受众正确的 run capability 注入上游模型凭证；harness 不直接获得真实上游 key。per-executor token 必须在 llmproxy 处被拒绝。

stock app-server 访问 llmproxy 所需的短期 capability 优先通过 tmpfs/受限文件描述符投射；若所 pin 版本只能从进程环境读取，则仅注入 `aud=llmproxy` 的 capability，并保证不进入 rollout、诊断 dump或持久化配置。executor MCP capability、harness-worker 的 workload/control credential 使用独立文件权限或 non-exportable identity，只由 worker 进程读取；相关环境和 FD 必须在 final exec 前移除/close，不能进入 app-server 子进程。executor 连接 credential 仍严禁进入任何被执行子进程。

## 6. 主流程

### 6.1 创建并运行对话

1. a2ui-web 调 core 创建或选择 `session_id`。
2. 用户向 `POST /v2/workspaces/{workspaceId}/sessions/{sessionId}/agui` 提交 AG-UI `RunAgentInput` 和客户端生成的 `Idempotency-Key`；`threadId`必须为空或等于path中的`sessionId`，请求必须且只能包含一条新的user message，客户端不得提交历史messages、state、tools或context。重连游标只允许放在`forwardedProps.agentserver.eventCursor`，其他客户端权威字段一律拒绝。
3. browser-gateway 以自己的mTLS workload identity调用core的`POST /v2/workspaces/{workspaceId}/sessions/{sessionId}/runs`，原样转交用户bearer与幂等键；core introspect得到actor、检查`active/exp/aud/scope`，并在写事务中再次检查当前workspace membership。core在同一事务中写入`run_id`、第一条规范事件和durable outbox。同一user/workspace/session下重复的幂等键只有在请求hash相同时才返回原`run_id`，不同payload必须报冲突。
4. browser-gateway 将CreateRun结果映射为`RUN_STARTED`，并立即发布第一条`CUSTOM{name:"agentserver.event_cursor"}`。之后它通过core的`GET /v2/workspaces/{workspaceId}/runs/{runId}/events`只读取已提交事件；每次long-poll都重新验证用户token与当前membership，browser-gateway不读取PostgreSQL。
5. harness-pool领取queued run，并以CAS获取`session_lease`，同时创建新的`run_attempt_id/run_attempt_generation`与对应lease。core为将创建的新brain thread从版本化executor MCP contract与当前policy计算允许子集，先保存未绑定thread id的canonical catalog/digest；`thread/start`成功后再以CAS绑定返回的`brain_thread_id`。已有checkpoint必须沿用原digest。catalog决定模型可见schema，但不是永久授权：gateway每次调用仍检查live RBAC/capability。session lease保证任何时刻一个session最多一个active run，attempt lease fence旧worker。
6. harness-pool 在当前 holder 进程内为该 attempt 创建新的临时目录和进程组，以本地 `fork/exec` 启动一个全新 harness-worker，不调用 Kubernetes API。pool先按manifest中的完整object pointer从对象存储读取prompt并复算size/hash；worker通过仅本次启动继承的独立pipe接收不可变签名manifest、control/executor-MCP/llmproxy三枚受众分离的capability以及prompt原始字节，并再次按签名pointer复算prompt。若存在core已提交的previous checkpoint，pool再通过可选FD 5流式发送其精确对象；worker先在staging中复算外层size/hash，再校验checkpoint manifest digest与签名的source run/attempt/generation、runtime、allowlist和catalog，任一不符都不创建rollout。capability和prompt均不进入argv、worker环境或临时磁盘；checkpoint只允许进入本attempt的有界staging。worker随后创建清洗后的临时 `CODEX_HOME`，以 MCP client 初始化 executor-gateway，并要求协商到manifest固定的protocol profile；reference profile是可承载嵌套server-originated elicitation的`2025-11-25` stateful Streamable HTTP，其他版本在`tools/list`前fail closed。随后worker读取`tools/list`，并要求规范化后的名称、description、input schema 与签名 manifest 中冻结的 catalog/hash 完全一致；不一致时在启动 turn 前 fail closed。
7. harness-worker 以 stdio 启动并初始化 stock app-server。新 thread 的 `thread/start.dynamicTools` 由冻结 catalog 机械生成；native resume 没有 dynamicTools override，所以只允许恢复 catalog digest 相同的 thread，catalog 变化必须创建新 thread。worker 随后以原始用户输入调用 `turn/start`，不改写 prompt。app-server 只通过 llmproxy 调模型；需要工具时发出 `item/tool/call`，worker 校验 thread/turn/call/tool/arguments 后转成 executor MCP `tools/call`。
8. harness-worker 将允许的原始 app-server notification和已经与dynamic call关联的executor MCP progress写入唯一mTLS control stream；frame只携带当前attempt generation与单调control sequence，不让无状态worker分配canonical producer身份。harness-pool按冻结catalog和已接受thread/turn做closed-world映射，为候选canonical event分配`event_id + producer_instance_id/producer_seq + outbox_id`，并同步提交core；core提交成功后pool才推进control receive cursor并累计ACK。core拒收旧generation，browser-gateway从已提交事件映射AG-UI/A2UI。
9. 收到 `turn/completed` 后，run 先进入 `finalizing`，但该 notification不是统一的 transport cleanup barrier。worker按 request 类型清理：已回复的 `item/tool/call` 以对应 JSON-RPC response 写入完成为准；未回复的 dynamic call 以所属 turn terminal 为准，并同时取消 worker→MCP 请求；只有 app-server 明确定义会发 resolved 的其他 server request 才等待 `serverRequest/resolved`。worker还要确认 execution/process收口，随后才关闭 app-server stdin并等待优雅退出。只有 child 在有界时间内确认退出后，worker才能在terminal中附带由app-server thread response得到、且已按`CODEX_HOME`做纯路径包含校验的rollout locator；若等待超时，worker不得用failed terminal冒充清理屏障，而应断开control并退出，由holder回收和核验整个进程组，当前已接受turn按crash语义进入interrupted。holder确认整个进程组停止后，pool的受信本地finalizer才以pinned allowlist和安全`openat2`边界读取app UID私有rollout、生成checkpoint manifest；worker不持有读该树的DAC能力。SQLite 主库及其 WAL/SHM 等运行时派生文件不进入 checkpoint。harness-pool 先上传加密对象，再由 core 以 CAS 在同一状态事务中提交 checkpoint 指针与 run terminal event；未提交的对象由后台清理。
10. harness-pool确认worker/app-server进程组已退出后完成checkpoint finalization；core确认terminal state和checkpoint后，pool才删除本次attempt临时目录。mid-turn crash不生成可恢复checkpoint，也不能继续原turn。

浏览器断开不自动取消 run。取消必须通过`POST /v2/workspaces/{workspaceId}/runs/{runId}:cancel`显式发起，并产生规范事件；重新连接使用cursor继续读取。尚无holder的queued run在授权事务中直接进入`cancelled`。已有attempt时先写`run.cancelling`，holder通过成对lease heartbeat观察该状态，取消MCP并interrupt stock turn，同时在整个workload/process cleanup期间继续续租。turn/MCP runtime context与control/lifecycle command context必须分离：取消只立即结束前者；worker control必须活到`turn_terminal(interrupted)`获得累计ACK，pool lifecycle authority则必须和heartbeat一起活到supervisor确认workload cleanup完成，使terminal前已经接收的runtime fact仍能同步提交core。只有exact live holder确认typed callback、execution和进程组已经收口后，core才原子写`run.cancelled`、清session active run并删除双lease。browser-gateway将中间态投影为`CUSTOM agentserver.run_status`，将终态投影为`RUN_ERROR code=user_cancelled`。

Phase 1 在一个 session 已有 active run 时不接受第二个新 run，返回带当前 `run_id` 的 `409 active_run`；不隐式映射为 `turn/steer`。未来支持 steer 时必须新增显式 API、绑定 `expected_turn_id` 并进入同一 fencing/审计链路。

### 6.2 执行 dynamic tool / MCP 工具

1. 模型调用一个冻结的 namespaced dynamic tool；stock app-server 向 worker 发出带 thread/turn/call id、tool 和结构化 arguments 的 `item/tool/call`。
2. worker 要求该调用精确命中冻结 catalog，记录 app-server request id与 call id的映射，并以自己的 MCP connection/capability调用 executor-gateway。模型不能提供 endpoint、credential、tool schema或可信执行上下文；worker从签名 manifest和 callback envelope补齐这些值。
3. executor-gateway 校验 run capability、当前 run-attempt generation、workspace live RBAC、executor/env 归属和工具策略，并按 `(run_id, app_server_tool_call_id)` 向 core 调用 `PrepareExecution`。core 持久化 `execution_id`、规范化参数 hash、tool/schema/policy/mapper version 与确定性 `operation_plan_hash`；重复 tool call id 只有完整 context hash 相同时才返回原 execution。approval hash 同样覆盖该 plan。一个工具若需要多个确定性步骤，gateway 还必须在各步骤发送前分别 `PrepareOperation`，为每个 operation 分配唯一 `mutation_key`。
4. 若策略为 `deny`，execution 进入 `denied`；若为 `ask`，gateway 先调用 core 创建绑定完整 context hash、绝对 `expires_at` 和一次性 nonce 的 approval，再在原 MCP `tools/call` 上向 worker 这个 MCP client 发起标准 `elicitation/create`。worker 只校验该 request 与既有 execution/approval 的关联，并经 control stream 投影规范 approval 事件和转接最终决定，不创建或改写 approval。拒绝、过期、取消、MCP/control stream 断开时不得 dispatch。
5. 批准后，gateway 重新校验 live RBAC、attempt generation 和批准 hash，以 CAS 消耗 approval，并将 execution 置为 `dispatching`。每个有副作用的 operation 也必须在对应网络发送前以 CAS 从 `prepared` 置为 `dispatching`；只有首次成功提交该CAS的命令返回一次性`Began=true`并允许网络发送，commit结果不明或exact retry返回`Began=false`都不得发送。从该 operation 跨过边界起，崩溃后的默认结果是 `unknown`，不能自动重放。
6. gateway 将已批准 MCP 参数机械映射为一个或多个 exec-server JSON-RPC 请求；每个请求携带既有 `execution_id/operation_id/mutation_key`。agentx 以本地 owner policy 校验 workdir、路径、用户、网络和 sandbox；远程策略只能收紧，不能放宽本地策略。
7. 对 upstream 标准方法，agentx 只做 request id/ownership 映射并转发给本地 stdio exec-server；exec-server 执行并返回带序号的输出/结果。agentx 自己只处理协商过的 agentserver 扩展。
8. agentx 接收并去重 mutation 后返回 dispatch ACK；gateway 先把对应 operation 置为 `acknowledged`，再按聚合规则把 execution 置为 `running`。operation 与 execution 的 terminal result 必须先写 core，再作为 MCP progress/result返回 worker；worker把有界、规范化的结果写成原 `item/tool/call` JSON-RPC response。若 worker→MCP或worker→app-server transport已断开，execution仍独立保留在core，但当前 turn进入 `interrupted`，不能伪造原调用已恢复。

这条路径中不存在“把用户指示改写成 prompt”“executor 再思考一次”或“恢复 executor 模型会话”。

WSS/session 恢复只恢复 gateway ↔ agentx 通道，不能恢复已断开的 worker ↔ MCP HTTP调用或 app-server dynamic callback。后续 run只能读取并显式注入已持久化的 `succeeded|failed|unknown` execution结果，不能重新调用同一副作用工具来“确认”。

### 6.3 故障与恢复

- harness attempt进程在app-server接受`turn/start`之前失败时，holder必须先确认本地workload已经停止，再在双lease仍live时调用`AbandonAttempt`。core持run锁原子仲裁：普通startup failure执行`attempt → failed`、`run: starting → queued`并删除双lease，允许原dispatch释放后立即重领；若显式cancel已先进入`cancelling`，则执行`attempt → interrupted`、`run → cancelled`、清session active run并删除双lease。若abandon先提交，随后cancel会直接终止无holder的queued run。不能用“观察状态后再release”的两步协议，否则两步之间的cancel可能永久停在`cancelling`。`turn/start`一旦被接受，任何worker/app-server mid-turn crash都使当前run进入`interrupted`，即使尚未dispatch executor副作用也不能自动重跑该turn；这避免重复模型调用和已流式输出分叉。
- 一旦 execution 已进入 `dispatching`，harness/gateway 崩溃后不得自动重放；run 标记为 `interrupted`，无法从 agentx journal/core terminal record确认的 execution 标记为 `unknown`，由用户决定下一步。
- harness-worker 与 harness-pool 的 control stream 短时断开时，worker 只做有界事件缓冲且不接受新的控制决策；approval 一律失败关闭。grace period 到期、缓冲溢出、ACK 出现不可恢复缺口或 lease 无法确认时，worker 调 `turn/interrupt` 并终止 app-server。重连必须携带 attempt generation 和 producer ACK，旧 generation 的消息全部丢弃。
- agentx 与 gateway 短时断线时，agentx 保持所有活跃 stdio pipes/exec-server instances 存活，并用 `exec_session_id` 与外层 sequence/ACK 恢复；Phase 1 默认 grace period 为 30 秒。
- grace period 到期后，agentx 主动关闭每个活跃 process独占的 stdio instance。upstream connection shutdown会尝试终止该 instance唯一的 managed process；已确认退出的 process正常收口，未收到终态的 execution标记为 `unknown`，绝不自动重新执行。
- exec-server 自身崩溃时不能假定孙进程随之退出。agentx 必须把 exec-server 及其后代放入可整体回收的 cgroup/process group/job object，异常时执行 kill-tree 并核对结果；在确认前 execution 为 `unknown`。
- 每次 stdio 断开或 child 退出都使对应 `local_exec_instance_id` 失效；gateway 不能把旧 process handle绑定到新子进程。
- 显式cancel先将有holder的run置为`cancelling`，由harness-pool通过heartbeat观察后向当前generation的worker发送`turn/interrupt`；worker取消未完成的MCP请求，gateway/agentx对所有run-scoped process发terminate。cleanup期间holder继续成对续租，避免另一副本接管尚未停止的workload。收到app-server `turn/completed(interrupted)`、所有dynamic callback按“response已写入或所属turn已terminal”清理、其他需resolved的server request已清空且进程退出确认后，exact holder才调用`InterruptAttempt`记为`cancelled`；未确认的execution记为`unknown`。若cancel与已进入finalizing的自然完成竞态，先提交的权威边界决定结果：completed已经提交则cancel为terminal幂等读取；cancel先提交则不得再产生checkpoint。
- 正常完成 run 前也必须确认所有 process 已 closed，或显式 terminate 并收到确认。存在未确认 process 时 run 进入 `interrupted`，不能记为 `completed`。

## 7. Harness 设计

### 7.1 harness-worker / appserver-host

stock app-server 是双向 JSON-RPC server，不会从环境变量自动取得 prompt 并独立完成一次 run。每个 attempt 因而由常驻 harness-pool 以本地 `fork/exec` 启动一个全新的无状态 `harness-worker`，worker 作为 app-server 的真实父进程和 stdio supervisor：

```text
harness-pool controller ──mTLS control stream──► harness-worker ──stdio──► stock app-server
                                                        ├─MCP client──► executor-gateway
                                                        ├─ 临时 CODEX_HOME/rollout locator
                                                        └─────────────────────► app-server只到llmproxy
```

harness-worker 只负责：

- 校验不可变 run manifest 和 attempt generation，恢复已提交 checkpoint，创建清洗后的临时 `CODEX_HOME`；
- 作为受限 MCP client连接 executor-gateway，校验 `tools/list` 与冻结 catalog/hash，机械生成 `thread/start.dynamicTools`；
- 以绝对路径启动 pinned 的小型 `harness-final-exec` trampoline；trampoline在固定 app UID 下清 capability、设置`no_new_privs`/non-dumpable、关闭fd 3以上，再原子替换为固定的`codex app-server --listen stdio:// --strict-config`。worker随后完成 `initialize → initialized → thread/start|resume → turn/start`；`--strict-config` 只用于拒绝未知配置字段，能力隔离仍由下述组合与测试承担；
- 按 pinned schema 语义无损转接允许的 app-server notification 和 server-initiated request：保留method/params payload，在control envelope中关联attempt generation、control sequence和累计ACK，并做有界缓冲；canonical producer sequence只由pool分配，未列入allowlist的server request一律fail closed；
- 将 `item/tool/call` 按冻结映射转成 MCP `tools/call`，把有界 MCP result写回原 app-server JSON-RPC request；
- 将 executor-gateway 发给 MCP client的 elicitation/approval request转给 harness-pool/core，收到明确决定后再答复 gateway；
- 接收 cancel/fence，调用 `turn/interrupt`，监管 child/stderr/退出并清理worker-owned staging；app UID私有树在整个进程组停止后由pool的受信清理边界删除；
- 按 request type清理 outstanding set，在 `turn/completed`、dynamic callback已回复或由所属turn terminal取消、execution/process收口且child正常退出后，上报受限rollout locator；checkpoint字节读取与manifest生成由进程组停止后的pool finalizer完成。

worker不调用模型、不选择工具、不解释或改写prompt、不执行本地shell/fs，也不直接连接agentx；它唯一的工具数据面能力是按冻结目录调用MCP server，且不拥有session/run/event的权威状态。控制流中断时它不得自行重试turn或MCP副作用。实现上可以复用harness-pool代码库的worker subcommand；它不是新的常驻产品服务。

`harness-control/1.3`冻结两类worker→pool runtime fact：`app_server_notification`保留allowlist内的stock method/params，`executor_mcp_progress`保留已由worker MCP client核对run/generation/call token的进度；同时增加worker→pool `approval_request`与pool→worker `approval_outcome`。runtime frame可以在worker的有界内存journal中流水发送，但`turn_terminal`必须等待覆盖全部前序frame的累计ACK。pool对notification/progress frame先`PrepareReceive`，完成catalog/schema/lifecycle校验及core `AppendAttemptEvents`后才`CommitReceive`并ACK；core调用结果不明时，同holder resume必须重放完全相同的control frame，并复用内存中保留的event、producer和outbox身份。approval request完成关联、去重和有界登记后即CommitReceive并ACK，Core long-poll在独立goroutine中等待，不得占住worker发送窗；canonical outcome作为同一control session内的journaled command发送，写入不明时只重放原frame。该恢复仍不跨pool进程或Pod。

当前映射覆盖assistant message、可公开reasoning summary、dynamic tool start/arguments/progress/completion/result。raw reasoning text不进入浏览器投影；worker只转发投影allowlist内的notification，任何越过control边界的未知method、builtin tool item、scope漂移、未冻结tool、参数schema不匹配、完成前后参数漂移及progress越过tool lifecycle全部由pool fail closed。超过inline展示限额的文本只产生带byte size与SHA-256的明确omission摘要，不能冒充完整内容；完整模型恢复事实仍只来自rollout/checkpoint。stock `turn/completed`只关闭attempt内的item生命周期，不直接伪造`run.completed`；后者仍须等待进程组收口、checkpoint与core finalization。

dynamic-tool-only不是prompt约定，而是pinned Codex build上必须同时成立的能力配置：

- `initialize` 显式开启所需 experimental API；`thread/start` 与每次 `turn/start` 都传 `environments: []`，使环境支持开启时也没有默认本地 environment；当前 schema 的 `thread/resume` 没有该字段，cold resume 固定传 app-server 返回的 rollout path 和 `excludeTurns: true`，以成功 RPC response 而不是并不存在的 `thread/started` notification 作为恢复屏障，随后由 `turn/start` 再固定空 environments；
- 清洗后的 `config.toml` 禁用所有目标 stock release实际支持关闭的builtin tool source，包括 `request_user_input`、Web、apps、plugins、multi-agent、browser/computer use、hooks；不提供 capability roots；对 `update_plan`这类stock内建utility，所pin release也必须提供经过tool capture验证的真实禁用机制，不能在文档中虚构配置键；
- stock app-server不配置任何MCP server。pool 镜像在任何 child启动前已把管理员控制的`/etc/codex/requirements.toml`以只读 mount 固定为至少`mcp_servers = {}`，使user/project配置也不能注入MCP；A04必须从零MCP bootstrap请求与精确dynamic tool surface证明deny-all真实生效；
- run manifest冻结MCP protocol profile、namespace及其description、tool name/description/input schema、固定`deferLoading=false`、逐tool schema hash和catalog digest。MCP annotation不作为授权事实也不投影给模型。worker从executor-gateway `tools/list`得到的catalog必须与其完全一致，再机械生成`dynamicTools`；模型伪造未发布tool、builtin或另一个namespace时必须在stock路由层得到`unsupported call`，不能到达worker/MCP；
- production thread使用`approvalPolicy = "never"`。这只关闭stock app-server自身的通用审批；executor产品审批发生在worker作为MCP client收到的`elicitation/create`上，不经过app-server，所以不会被`never`自动拒绝；
- `thread/resume`没有`dynamicTools` override，native rollout会恢复原tool schema。checkpoint必须绑定catalog digest；同一brain thread的schema不可变，catalog变化时创建新thread。gateway仍在每次`tools/call`实时校验RBAC、capability和policy，目录冻结不等于永久授权；
- conformance test使用fake model endpoint捕获实际Responses请求，断言模型可见工具集合精确等于冻结dynamic catalog。只检查配置文件内容不构成隔离证明。

官方 release binary 没有可安全重定向 system `requirements.toml` 的 CLI：源码中的 `CODEX_APP_SERVER_MANAGED_CONFIG_PATH` 只在 `debug_assertions` build 生效，0.146.0 official artifact 的 live probe 也确认它被忽略。因此 A04 正向测试必须在一次性 image/mount namespace 内把 `mcp_servers = {}` 预装到真实 system path，再启动未经修改的 stock artifact；不能改宿主 `/etc`、依赖 debug build 或把临时 user config 冒充 managed requirements。该测试必须从 direct executor、user、trusted-project 三类 endpoint 都收到零 MCP request，以及 dynamic tool surface 仍精确可控来证明 deny-all；`configRequirements/read` 不投影 MCP allowlist 本身，不能单独作为证据。

reference A04 runner 固定为 networkless scratch image、只读 rootfs 和独立的 `/etc/codex` hardened tmpfs；没有这些 mount 事实时测试在写文件前失败。runner 要求 release、unpacked binary SHA-256 和 size 由外部 artifact intake 提供，不能在同一步现算现信；其临时 CA 只信任同一 image 内的 loopback HTTPS fixture。测试源码存在或能交叉编译都不是放行证据，必须保存 exact artifact 上的实际 image run 结果。

A04 deny-all gate 已在 official stable 0.146.0 Linux amd64 musl artifact 上通过：archive SHA-256 为 `5ba3b9405543953081f661d0854d266f76e2abbe51d41349355a36de7673776a`，解包 binary SHA-256 为 `2e863156ed35ecc5253b1e2f907a9143077b9f7cb51942070c61996471ff6e04`、size 为 `311001136`。gate 先用 `configRequirements/read` 的无害 sentinel 证明真实 system requirements 已加载，再要求 direct executor、user 与 trusted-project endpoint 全部零 MCP 请求，并确认 client-supplied `executor.approved_echo` 仍是唯一模型工具。该 Make target 已在 Apple `container` 1.2.0 实跑。`mcpServerStatus/list` 不是 enablement oracle；gate 直接统计 endpoint 请求。

以上字段包含 experimental contract，必须与 Codex binary、app-server schema 和测试 fixture 一起锁定。任一升级导致工具面扩大、elicitation 被自动处理或配置字段失效时，harness 镜像不得发布。

已验证的 0.145.0 candidate 不满足该前提：固定 model catalog 并关闭全部已知非 MCP 开关后，`update_plan` 仍被无条件注册；scripted model 能成功执行它并触发 `turn/plan/updated`。因此该版本必须在 A03 被拒绝，不能作为 production runtime pin，也不能用 prompt、忽略 notification 或仅在 harness 侧过滤事件来冒充能力隔离。

官方 `rust-v0.146.0-alpha.14` candidate（commit `9d84cad281364eb7f6be75e23067b0adc5e26106`）新增了真实的 `[tools.update_plan] enabled = false`，无 MCP server 时实际模型工具面可收敛为空；但旧的 direct-Codex-MCP 方案一旦配置 executor MCP，stock 仍会额外注册三个通用 MCP resource handler，`list_mcp_resources` 还会越过 `enabled_tools` 真实发出 `resources/list`。这条证据继续拒绝 direct MCP 方案，但不再阻塞本文采用的 dynamic bridge。

官方 stable `rust-v0.146.0`（annotated tag object `be449751a978f02e5bbba886999662956c7f38f5`，peeled commit `e363b08c9175ac1cbe5893615dd2cb9ddf95043b`）在 direct MCP probe 上得到相同失败；但 production-shape dynamic bridge probe 在完全不配置 Codex MCP 时把模型工具面精确收敛为 `executor.approved_echo`。真实 namespaced function call 会成为带原 thread/turn/call id 与结构化 arguments 的 `item/tool/call`，client result 进入下一次模型请求；未发布 executor tool 和 `exec_command` 均为 `unsupported call` 且不产生 callback。0.147.0-alpha.2 重复了同一正向 dynamic 结果。因此修订后的 A03 对 stable 0.146.0 通过；是否形成 production runtime pin 仍取决于其余修订门禁，不能沿用旧 A03 结论直接放行。

### 7.2 进程边界与网络

- Phase 1 的明确威胁模型是：harness-worker 和 pinned stock app-server 都是管理员提供的固定代码；模型只能调用 worker 投影的 dynamic tools，harness 本地不执行用户 shell/fs/任意代码。在这个前提下，per-attempt Kubernetes Job/Pod 的启动成本大于它提供的边际收益，默认 backend 固定为本地 `fork/exec`。一旦未来开放本地任意代码、用户脚本、stdio MCP 或未信任 plugin，该 backend 必须 fail closed，改用经过独立安全评审的 sandbox backend。
- harness-pool Pod 是常驻容量单元，以有界并发在同一 Pod 中承载多个 attempt 进程组；每个 attempt 都创建全新 worker、app-server、临时目录和 `CODEX_HOME`，绝不复用已处理过另一 attempt 的进程或可变目录。这是进程清理边界，不冒充 per-run Pod 级安全隔离。
- 放宽隔离的真实代价是：一个 pool Pod 崩溃或 OOM 可以中断该副本上的所有 attempt。每副本必须设置较小的有界并发上限、Pod 级资源 limit 和 attempt 级 deadline/rlimit；可用时再为进程树分配 cgroup v2。任何副本故障仍按现有 generation/lease 语义中断 run，不在另一 Pod 自动重放已接受的 turn。
- pool launcher 为每个 attempt 创建 pool 拥有、只向固定 worker group 开放的临时根和独立进程组，使用绝对路径启动 pinned worker，不继承 ambient env；worker 在其中再创建只对 app UID 开放的 `CODEX_HOME`。production runtime根固定为`0711`，只允许worker/app UID穿越而不能列目录；128-bit随机attempt目录固定为`0771`，worker staging仍为`0700`，不能把execute-only父目录误写成数据可读。签名 manifest与三枚runtime capability只通过一次性bootstrap pipe传递；prompt对象由pool读取并在FD 4中按签名size/hash传递，worker再校验一次。只有签名manifest存在previous checkpoint时才继承FD 5，同样由pool/worker双端复算外层object pointer；没有checkpoint的worker不得收到该FD。这些输入均不进入argv、环境或磁盘配置，只有checkpoint对象可暂存在本attempt staging并在恢复后删除。正常、cancel、lease loss 和 holder shutdown 都必须有界地回收整个进程组后再删除目录。
- app UID可以合法创建worker/pool无法按普通DAC遍历的`0700`后代，因此“pool拥有attempt父目录”本身不构成可靠清理。production local backend要求Linux pool在启动前证明effective `CHOWN|SETUID|SETGID|DAC_OVERRIDE`，并使用只接受runtime根直接`attempt-<128-bit hex>`子目录的固定清理器；缺少任一能力时launcher必须fail closed。清理只发生在完整进程组确认停止后。macOS不提供这一production backend，只用于非特权开发测试。
- harness-pool、worker 和 app-server 使用不同 UID/权限域；pool 的 core/对象存储/签名凭证对 worker/app UID 不可读，worker credential/control/MCP capability 对 app UID 不可读。worker 必须是 app-server 的真实父进程，以空 supplementary groups 运行，并且只在创建固定 app UID child 所需的窗口持有 `SETUID/SETGID`。child final-exec 在任何 preflight 前清除全部 capability、设置 `no_new_privs` 和 non-dumpable，再验证身份。
- app-server child 的 mount view 没有 workspace worktree、用户代码卷、pool secret 或 Kubernetes service-account token；pool Pod 只读 rootfs 也不挂载用户工作区。worker 只能访问本次 attempt 的临时控制目录和 checkpoint staging，app-server 只能访问它的新建 `CODEX_HOME`。auth、config、MCP secret 或整个 runtime 目录均不能原样持久化。
- 控制 socket 和所有非 stdio FD 都以 `O_CLOEXEC` 打开；production launch trampoline 在最终 exec app-server 前必须执行 close-all（Linux 优先 `close_range(3, UINT_MAX, 0)`），不能仅依赖 Go `exec.Cmd` 没有填写 `ExtraFiles`。A12 负向 probe 已经证明一个被清掉 `CLOEXEC` 的未列出 FD 会进入 stock child。
- Kubernetes NetworkPolicy 只能对整个 pool Pod 限制 destination 并集。Phase 1 Linux Pod 在 runtime 启动前，由唯一持有 `NET_ADMIN` 的 init container 安装按 UID 默认拒绝的 nftables OUTPUT 规则：pool UID 可访问 core/对象存储等必需内部 endpoint，worker UID 只可访问本 Pod control endpoint 与 executor-gateway MCP，app UID 只可访问 exact llmproxy tuple。runtime 进程均不持有 `NET_ADMIN/NET_RAW`，两个受管数据面 UID 的 IPv6 全部 drop。
- DinD 不是 production 默认 backend：它需要高权限 daemon/套接字、额外镜像和容器生命周期，却不消除 holder/lease 问题。它可作为本地开发的可选 launcher，不能让主路径依赖 privileged Docker socket。
- 固定 worker/app UID 和共享 Pod 不防御“某个 worker 或 stock app-server 二进制已被攻陷后读取同副本兄弟 attempt”。这是本进程 backend 在当前固定代码威胁模型下接受的边界，不能被宣称为强多租户 sandbox；需要防御该类 compromise 的 deployment 必须选用 per-attempt sandbox backend。
- app-server child 不访问 MCP、外部网络或通用 DNS。内部 llmproxy identity 在启动前固定到只读 hosts 视图，child UID 禁止 DNS egress 和 cross-origin redirect 目标。worker 同样禁止通用 DNS，只连接 manifest 固定并经 TLS 校验的 executor-gateway；未来外部 MCP 必须先经过受控内部 policy proxy，不能给 app UID 新增直连能力。
- MCP endpoint 和 tool allowlist 由 core 生成后固定进 run manifest，模型输出、prompt 或 skill 不能动态增加 endpoint。每个 endpoint 必须校验 TLS 身份并使用独立 audience capability；缺少凭证时 endpoint 必须拒绝，不能回退为匿名调用。
- Phase 1 executor MCP 不实现 resources/prompts 协议；任何 `resources/list`、`resources/templates/list`、`resources/read`、`prompts/*` 请求都必须 fail closed 并进入安全审计。但这是纵深防御，不替代 A03 对模型工具面的精确约束。
- Phase 1 只有 executor-gateway 可以向 worker 暴露工具，不支持 app-server 或 worker 直连第三方 MCP。未来第三方工具也必须先经过内部 policy proxy、冻结 schema 并使用同一 dynamic bridge；不能把第三方自报的 `readOnly/destructive` annotation 当作安全事实。
- skills/context 只能是只读的声明性提示与配置；引用本地脚本、stdio MCP 或其他可执行载荷的 skill 必须被拒绝。
- 必须以 fail-closed 配置和集成测试证明内建 shell、fs、apply-patch、浏览器、Web、hooks 和未列出的 dynamic/local 能力均不可调用；只在文档中声明禁用不算完成。
- 如果所 pin 的 stock Codex 版本无法可靠进入上述 dynamic-tool-only 模式，则实现被阻塞，不能用 system prompt 代替能力隔离。

### 7.3 会话 checkpoint

恢复状态与 UI/审计事件是两种不同投影：

- **模型可见 checkpoint**：加密保存恢复 thread 所必需的完整、模型可见历史，包括后续 turn 需要的 dynamic tool call/result、冻结 tool catalog、compaction/rollout 元数据；不能为了 UI 脱敏而从中任意删除模型已经看到的内容。
- **规范/UI/审计事件**：按 secret/prompt policy 过滤，只用于展示、审计和事件恢复，不能反向拼成模型上下文。

checkpoint 只能在 app-server 发出 terminal `turn/completed`、所有 dynamic callback 已按“response 写入完成或所属 turn terminal”清理、其他需要 resolved 的 server request 已清空、worker 关闭 stdin、child完成有界优雅退出且holder确认整个进程组停止后生成。不能在仍运行的 `CODEX_HOME` 上取文件，也不能用固定 sleep 猜测稳定。对已验证的 0.146.0-alpha.14 与 stable 0.146.0，pinned allowlist 是每个 `brain_thread_id` 恰好一个由 app-server thread response 返回的 rollout JSONL。worker只把该绝对路径规范化为相对其已验证`CODEX_HOME`的locator，不读取文件；pool finalizer以固定attempt目录FD、`openat2(RESOLVE_BENEATH|RESOLVE_NO_SYMLINKS)`和expected app UID打开，验证普通文件、相对路径、大小和hash，checkpoint staging也必须严格只有该manifest entry。manifest 至少包含 `brain_thread_id`、terminal `turn_id`、run/attempt generation、Codex build/schema、checkpoint allowlist version、dynamic tool catalog digest、相对路径、大小和文件 hash。`state_5.sqlite`、所有 SQLite WAL/SHM、goals/logs/memories DB 均为运行时派生状态，不进入 checkpoint；配置、requirements、token、环境变量、诊断日志、cache 和临时 transport 缓冲同样禁止进入。每个新 Codex build 都必须重新通过 native `thread/resume` round-trip 才能获得 allowlist，禁止打包整个 `CODEX_HOME`。harness-pool 先上传加密对象，core 再以 CAS 原子提交 checkpoint pointer 与 run terminal state；未引用对象可清理。

checkpoint artifact v1 不使用 tar/zip，其字节布局固定为 `16-byte magic/version || uint32be manifest_length || RFC 8785 canonical manifest || rollout bytes`，media type固定为`application/vnd.agentserver.codex-checkpoint.v1`，总大小上限为67,174,420字节。manifest只允许一个`purpose=codex-rollout, fileType=regular, mode=0600`的`sessions/.../*.jsonl`文件；实现同时拒绝非规范路径、`.`/`..`、反斜杠、symlink类型、额外entry和尾随字节，并逐行校验有界JSONL。manifest digest使用`agentserver-v2/checkpoint-manifest/rfc8785-v1\0`域隔离，与外层object SHA-256分开存储；run manifest v2还冻结源run/attempt/generation、terminal turn、runtime manifest digest和allowlist version，恢复时不只凭object ID或thread ID。

A08 已在 alpha.14 与 stable 0.146.0 上验证上述退出屏障：completed terminal 后立即关闭 stdin，stock app-server在有界时间内零退出；退出后两次有界遍历得到完全相同的相对路径、mode、大小和 SHA-256，thread报告的 rollout是完整 JSONL，`state_5.sqlite`具有合法 SQLite header。两个 release 都仍保留 `state_5.sqlite-wal/-shm`，以及 goals/logs/memories数据库的 WAL/SHM sidecar；因此 A08 只证明完整退出后的字节稳定，不负责判断 checkpoint 文件集合。

A09 旧 probe 已在同两个 release 上把 checkpoint allowlist 收敛为单个 rollout JSONL，并证明 direct-MCP tool result 能跨 cold resume 保留且副作用不重放；修订后的 A09 worker-runner gate 已把这些checkpoint事实与新dynamic架构组合，并固定为stable 0.146.0：首个 attempt 通过 worker-owned dynamic callback 产生唯一一次 executor 副作用，只复制 app-server 返回的 rollout，并在移走源 `CODEX_HOME` 后从新 home cold resume；resume 不发 `dynamicTools` override，第二 turn 的真实模型请求保留两轮用户输入、原 call ID/result 和完整不变的模型 tool schema，两个 attempt 总副作用仍为一次。runner 单测另外证明 catalog digest 不一致会在任何 stdio I/O 前 fail closed；catalog 变化必须创建新 thread，不能在原 thread 静默换 schema。缺失 rollout在模型调用前以 `-32600` fail closed 的旧证据仍保留。新 gate 已在macOS arm64 stable binary SHA-256 `ae1d3ffe6d48aec6a4dc3f50e7eb8e0d11962485a6a9406c5a7012139383da02`、size `271056976`上通过，因此修订A09对该exact artifact关闭；未来build仍须重新实跑才能获得同一allowlist。

A10 也已在同两个 release 上验证 mid-turn hard-crash边界。probe先把一个 completed turn的 rollout复制为独立、密封的 checkpoint，再从它启动第二个 app-server；scripted model在确认第二个 `turn/start` 已被接受、真实模型请求已包含本轮输入后保持 HTTP response未决，此时硬杀 child。该进程非正常退出，不重试模型请求，也无法改动独立 checkpoint。crash runtime随后被丢弃；第三个全新 `CODEX_HOME` 只恢复密封 rollout并显式创建新 turn。新 turn ID与被放弃 turn不同，模型上下文包含 crash前的 completed历史和新的继续指示，但不含被放弃输入。这证明的是系统允许的安全恢复路径，不是假设 stock Codex会拒绝调用方错误传入的未提交 crash rollout；core的 committed checkpoint pointer、attempt generation和 worker文件来源校验必须让该文件从一开始就不具备恢复资格。

A11 旧 probe 已证明 config/auth/token/log 等 runtime-only sentinel 不进入模型可见request body、stderr或单-rollout checkpoint，但它既没有检查HTTP header，又把 MCP bearer 注入了 app-server，该旧证据不能代表新边界。修订A11的worker-owned gate已固定并实跑stable 0.146.0：Codex配置没有MCP endpoint、`mcp_servers`、bearer env引用或bearer值，只接收冻结dynamic catalog；源worker用旧capability实际认证initialize/list/call并执行唯一一次副作用，恢复worker换用新capability验证同catalog后cold resume，零重放。gate扫描显式child env、config、有界`CODEX_HOME`全文件、stderr、模型request headers/body、rollout和单文件checkpoint，要求旧/新executor bearer均不出现；独立的model/llmproxy auth sentinel只允许成为模型transport的exact `Authorization`值，仍不得进入body、rollout或checkpoint，而原dynamic call/result与完整tool schema必须恢复。定制stateful gateway fixture和runner→bridge→MCP client组合已在普通CI实跑；stock round-trip也已在macOS arm64 stable binary SHA-256 `ae1d3ffe6d48aec6a4dc3f50e7eb8e0d11962485a6a9406c5a7012139383da02`、size `271056976`上通过。修订A12又在exact Linux arm64 image中证明继承FD、UID、mount和分UID网络边界，并从真实worker-owned MCP call到post-exit rollout扫描确认bearer不进入child。因此A11对该stable artifact set关闭；host证据与image证据仍不可互相替代。

A11只检测非模型事实的意外渗漏。若 secret scan在 rollout中发现本不应进入模型历史的 runtime credential，必须拒绝/quarantine整个 checkpoint并把 run标为不可恢复，不能原位替换字节后继续声称 native resume。相反，用户输入或 MCP result中本来就模型可见的敏感内容不能由展示层过滤器擅自删除；应依靠输入/工具策略预防、加密存储、访问控制、retention和删除流程处理。

A12 的 Darwin host-level probe已在同两个 release上确认一项正向事实和两项负向边界。model provider被故意配置为“若 worker mTLS sentinel环境变量存在就发到 HTTP header”；sensitivity control把值显式注入 child并观察到 exact header，随后 parent-only case的显式 child env使真实模型请求、body和 stderr都看不到该值。app-server thread返回的 cwd也是 source tree之外始终为空的临时目录，但这不证明 mount namespace中别处没有工作树。FD反例清掉一个父 pipe的 `CLOEXEC`，即使 Go启动代码未配置 `ExtraFiles`，stock child仍持有该 writer；网络反例让配置的 llmproxy返回跨 origin `307`，stock client会把同一 model request送到第二个未配置 origin并完成 turn。因此 runner必须实现 close-all与真实网络边界，base URL、managed requirements和 capability都不是网络可达性控制。

A12 production-profile image gate 已在 official stable 0.146.0 `linux-arm64` exact artifact 上按修订架构原生通过。真实worker UID在fork前读取`0700` capability、用official SDK认证executor-gateway MCP并冻结catalog，随后作为stdio supervisor运行`AppServerRunner → DynamicBridge → MCPClient`；app UID只收到dynamic catalog且只能访问单一exact llmproxy tuple。worker IPv4只可达harness-pool fixture与executor MCP，app对MCP/worker endpoint/direct/redirect全部零命中；两个UID对DNS-shaped UDP和IPv6也零命中，root sensitivity control证明sink存活。不同UID、空supplementary groups、capability seal、`/proc`/signal、只读root/tmpfs、mount缺失、non-`CLOEXEC` trap与`close_range`证据仍同时成立。受信init仅在child退出后用不下放的`DAC_READ_SEARCH`扫描app-owned rollout；child env/config/FD、stderr、model request和全部scenario rollout均无executor bearer。锁定Codex SHA-256为`cb5e8cb8a333a408ce6adbe0d4fad1845c69772c2216af7c1f88c98a11460dc6`、size为`269098800`。因此修订A12对该exact artifact/platform关闭；`linux-amd64`与真实Kubernetes网络部署仍是独立门禁。

该结果关闭的是 worker→app-server 的network/secret/FD 子边界，只绑定上述exact `linux-arm64` artifact；`linux-amd64`仍需在native worker运行同一target。它没有覆盖新的 pool→worker 本地 bootstrap pipe、pool credential 权限、多 attempt 并发或整个进程组回收，这些必须在 pool 部署镜像上追加独立 gate。in-image nftables证明也不外推为真实Kubernetes NetworkPolicy、Service/ClusterIP固定或部署权限已经正确。只有config级负向结果、401/403或sink返回错误都不算部署网络隔离证明。

worker 不持有对象存储 credential，也不读取app UID私有rollout。harness-pool finalizer在完整进程组停止后从本地attempt FD流式生成确定性artifact，施加大小/速率限制并上传，再请求core提交；本地树只有在上传/提交完成或明确失败后才由同一受信清理器删除。上传或提交失败时对象不得成为可恢复 checkpoint，未引用对象按 retention job 清理。

mid-turn crash 时只允许恢复上一个已由 core提交指针的 completed-turn checkpoint，当前 run 进入 `interrupted`；不能重放同一 `turn/start`，也不能扫描 crash runtime并把其中较新的 rollout冒充 checkpoint。后续用户明确继续时创建新 run/turn，并将已持久化的 execution terminal/unknown 结果作为显式上下文注入。

`brain_thread_id` 是优化和追踪标识，不是 session 的权威主键。native `thread/resume` 只适用于相同 pinned schema 的完整 checkpoint；不兼容时可从受保护的模型可见 conversation record 创建新 thread，但这是语义降级，必须生成审计事件，不能声称与原生 resume 等价。

### 7.4 启动延迟与容量

Phase 1 不在 run 热路径创建 Kubernetes Job、Pod、Secret 或 NetworkPolicy。harness-pool 保持至少一个常驻副本，接到已 claim 的 attempt 后只执行本地目录初始化、`fork/exec harness-worker` 和 `fork/exec stock app-server`。启动 SLO 分开记录 `claim→worker exec`、`worker exec→control ready`、`control ready→turn accepted` 三段，不用首个模型 token 掩盖 launcher 延迟。

harness-pool 可根据 queued attempts、活跃进程数和 CPU/内存扩容，但 `minReplicas` 不为 0。常态容量内的 run 不等待 HPA；突发超过本地并发槽位时留在 durable queue，不在已 claim 后无界等待子进程容量。

常驻的是 pool/launcher，不是 Codex thread。worker 和 app-server 仍然每 attempt 新建并在结束后销毁；禁止把已运行过 turn 的 worker、app-server、`CODEX_HOME`、网络 capability 或可变配置放回池中复用。

## 8. Executor 设计

### 8.1 agentx + stock exec-server

agentx 是 Go 编写的连接与策略代理；标准执行引擎直接复用 stock exec-server。Phase 1 默认一个 executor 只暴露一个 env，但不在该 env 内让多个受管进程共享一条 stock stdio connection：每次 `process/start` 使用独立 `codex exec-server --listen stdio` instance，该 instance 最多管理一个远端 process；fs 操作使用不允许 `process/start` 的独立串行/一次性 lane。这样在 root 已退出而后代仍持有 pipe 时，agentx 可以关闭整个该 instance 回收后代，而不会影响其他 operation。每个 instance 的启动顺序钉死为：

1. 从发行包 manifest 解析允许的 Codex version、平台 Codex 和所有独立外部 executable 的 SHA-256/签名；禁止静默使用 `PATH` 中的任意 Codex。
2. 创建只属于该 instance 的临时 runtime dir 和空 `CODEX_HOME`，写入最小配置并禁用 analytics；不读取用户已有的 Codex config/auth。
3. 将 exec-server 自身的 cwd 固定到 runtime dir（不是用户 worktree），清洗父进程环境，再以该 env 配置的执行身份启动绝对路径的 `codex exec-server --listen stdio --strict-config`。防止读取用户/项目配置依赖空 `CODEX_HOME`、固定 cwd 与清洗环境；`--strict-config` 只负责对未知配置字段 fail closed，不能单独承担隔离。Phase 1 agentx 使用无 `codex-package.json` 的最小 runtime bundle，并把 ambient PATH 替换为 bundle 内一个确认不存在的目录；Linux bundle 同时固定 `codex-resources/bwrap`。
4. agentx 作为本地 client 向 stdio 子进程发送一次不带 `resumeSessionId` 的 `initialize → initialized`，再调用 `environment/info`、`environment/status`。完整方法集来自 pinned build 的 manifest/schema，不能假设 child 会动态枚举全部 capability；握手结果只用于确认当前实现实际公开的字段。agentx 记录新的 `local_exec_instance_id` 和仅供本地诊断的 child session id。
5. agentx 启动时先用同一流程运行并关闭一个 capability probe instance；artifact、sandbox和握手全部通过后才向executor-gateway声明env online。业务instance仍逐个重新初始化，不能复用probe的session。

agentx 自己负责：

- enrollment、机器身份、出站 WSS 和 connection generation；
- 声明 platform、capability 和一个或多个 env/workdir root；
- 在转发前实施 root/sandbox/network/owner policy；
- 为每个 `process/start` 分配独立 local exec instance，将远端 request id映射为本地stdio request id，并维护`process_id → local_exec_instance_id` ownership index；
- WSS断线期间保持所有活跃stdio instance、持续排空各child stdout/stderr，并按外层session sequence/ACK缓存和恢复response/notification；每个process的output sequence只用于该进程补读，不能替代外层顺序；
- 监管全部exec-server instance，检测非预期退出并只使该instance的handle失效；
- 以平台支持的 cgroup/process group/job object 监管并回收 exec-server 的完整后代进程树；
- 实现经过 capability negotiation 的 agentserver 命名空间扩展；
- 对连接、策略决定和执行生成本地审计日志。

process、PTY、stdin、terminate、filesystem和upstream sandbox handler由stock exec-server完成，不在agentx中复制。Phase 1外层capability明确不协商`process/signal`。executor侧不需要模型配置或模型credential。agentx的机器连接credential保存在OS keychain/受限文件中，只由agentx使用，永远不注入exec-server或它启动的子进程。

stock 0.146.0 的 helper 结构不是“Codex + 若干同名独立二进制”。fs helper 通过绝对 `codex_self_exe --codex-run-as-fs-helper` 重入，arg0 exec helper同样重入当前 executable；Linux `codex-linux-sandbox` 是运行时在受保护 `CODEX_HOME` 下创建、指向当前 Codex 的 alias，创建失败时回退到 `current_exe`。这些路径不应在 manifest 中伪造三份 digest。真正独立的 Linux资源是 `codex-resources/bwrap`，但 stock launcher会先搜索 PATH中的 system `bwrap`，只有未找到或 capability probe不通过才尝试 bundled resource。agentx因此必须同时满足：绝对路径启动已验证 Codex；空且受保护的 `CODEX_HOME`；无 package metadata的最小 bundle；用确认不存在的 bundle内目录完整替换 ambient PATH；校验 bundled bwrap后才启动。Codex随后只会把自己生成的 arg0 alias目录前置到这个受控 PATH。若部署决定使用 system bwrap，它就不再属于该最小 profile，必须把固定绝对文件、镜像 digest/SBOM和真实选择结果作为新的 image-level gate，不能仅写一行 manifest便宣称锁定。

这条选择链必须按 release 和 native platform 分别验收。当前 disposable production-profile image 已关闭 official stable 0.146.0 `linux-arm64` 的正向 gate：最小 bundle 中没有 package metadata/compatibility shim，进程在非 root、只读 root、零 capability、无网络条件下启动；真实 read-only 与 workspace-write sandbox 请求成功，workspace 外写入被拒绝，poisoned ambient bwrap 未被探测或执行，运行时 sandbox alias 解析回已验证 Codex。该证据不外推到 `linux-amd64`；后者必须在 native amd64 worker 跑同一门禁。跨架构仿真重写 bwrap inner argv0 且无法安装相同 seccomp filter，因此不得作为替代证据。manifest hash 校验与 image gate 也不解决 mutable install path 上校验后替换文件的 TOCTOU；agentx 仍须使用 immutable install 与平台 safe-open/execute。

exec-server 可能依据 `envPolicy` 从自身环境继承变量，因此只过滤 `process/start.env` 不足以保护 credential。child 自身必须从一开始就运行在无 agentx secret 的环境中，agentx 还必须将远端 env policy 收紧到本地 allowlist；远端不能请求重新继承被剥离的宿主变量。

stable 0.146.0 的 live probe 暴露了两个必须由产品 profile 显式收口的事实：

- `process/signal` 对不存在、信号已送达和根进程已退出三种状态都返回相同的空对象 `{}`，无法形成产品可审计的送达语义。因此 Phase 1 agentx outer schema/capability 不公布也不转发该方法；产品只提供已验证的 `process/write` 与 `process/terminate`。未来若要 signal，必须新增有独立 terminal evidence 的 agentserver 扩展，不能转译空对象为“已送达”。
- 根进程退出并发出 `process/exited` 后，只要后代继续持有 pipe，process 尚未 `closed`；此时 `process/terminate` 返回 `running: false` 且不会杀该后代。关闭该 stdio connection 会触发 session drop 回收进程组，所以 Phase 1 用“每 process 独占 instance”把连接级回收变成该 process 的有界 cleanup，不波及其他 operation。

所以 `process/exited` 只能视为根进程退出证据，`process/closed` 才表示 output streams 已关闭；二者之间不能提前宣布 execution 成功。agentx 在 `exited` 后有界等待 `closed`，超时就关闭该 process 专属 stdio instance，并用 cgroup/process group/job object 验证执行树清空；该路径返回 `unknown` 或明确的 `cleanup_forced` 失败，不能返回成功。修订后的 E03 验证 outer profile 不暴露 signal，E07 验证 dedicated-instance cleanup；stock负向probe仍保留，防止以后误改回共享instance。

reference adapter已把这两项约束变成正向门禁：outer method集合精确为`process/start|read|write|terminate`，signal、foreign ownership、第二次start和超限writeId均在写stock stdio前拒绝；每个adapter只有一个reader并重分配local request id。fake-wire/race用例证明normal closed、forced cleanup、tree verifier失败转unknown和双instance无连带；exact stable 0.146.0 macOS arm64 live gate同时启动两个stock exec-server，第一条root退出但descendant持pipe时只关闭第一条connection并确认descendant消失，第二条保持存活且随后独立terminate/closed。该证据关闭E03/E07的reference composition，不替代真实agentx WSS adapter和Linux cgroup/macOS job/process-group containment gate。

upstream 将 `codex exec-server` 标记为 experimental，因此不能假设 semver wire compatibility。agentx 发行包必须锁定精确 commit/build，升级走 schema diff、fixture、sandbox 与故障恢复回归，不允许自动跟随 latest。

### 8.2 MCP 工具面

规划工具面保持小而确定，但不能在 handler 尚未实现时一次性全部广告。首个冻结 catalog `executor-mcp/1.0` 只包含 `list_environments` 与同步、terminal-only 的 `shell`；`executor-mcp/1.1` 在完整 bounded-read handler 装配后加入 `read_file`。`tools/list` 必须按实际注入的 handler 收缩，不能提前暴露下表其余工具。后续 catalog version 再按实现和 conformance 证据扩展：

| MCP tool | 必需的确定性输入 | RPC 映射 |
|---|---|---|
| `list_environments` | 可选 executor filter | core/gateway registry；不触发远端执行 |
| `shell` | `env_id`、`argv[]`、env-relative cwd、timeout、tty、显式 env；固定 `shell-v1` clean-env policy，`lifecycle=run` | `process/start` + `process/read`/通知 |
| `unified_exec` | 同上，另含调用方提供的 `process_id` 和输出模式 | run 内长生命周期 `process/start` |
| `write_stdin` | `env_id`、`process_id`、`write_id`、bytes | `process/write` |
| `read_output` | `env_id`、`process_id`、from sequence | `process/read` |
| `terminate` | `env_id`、`process_id`、reason | `process/terminate` |
| `read_file` | `env_id`、registered-root-relative path、offset/limit | 单个 `agentx/fs/readFileBlock` outer RPC |
| `apply_patch` | `env_id`、单文件 unified diff、目标文件 precondition hash | gateway 读取并确定性应用；agentx 以条件写扩展原子提交 |

除 `list_environments` 外，每个工具都必须携带或由 run capability 无歧义地解析出 `env_id`。不得依赖“当前 executor”这种隐式全局状态。

`read_output`、`write_stdin` 和 `terminate` 不能在没有 handle-producing tool 时作为孤立工具加入 catalog。`shell-v1` 在返回 MCP result 前等待 process terminal，超时也执行预分配 terminate 并等待真实终态，因此不会留下可供下一次工具调用使用的 process handle。只有后续 `unified_exec`（或另一份明确版本化的 start contract）与 ownership、run-finalization 门禁一起实现后，三项 process-control 工具才可同版本开放。首个垂直切片明确不包含它们。

`argv` 是字符串数组，不是自然语言。若调用者确实需要 shell 语法，必须显式传入例如 `["/bin/zsh", "-lc", "..."]`，并受独立策略控制。`shell-v1` 不允许调用方选择 ambient inheritance；未提供的 env 为空，提供的 env 只是显式条目，gateway 与 agentx 的变量都不得继承。

`timeout` 不是当前 stock `process/start` 的字段。gateway把`afterMs + 预分配timeout operation_id/mutation_key`放在agentx outer frame的`directives.processTimeout`，该字段由agentx消费且绝不复制进stock params。agentx本地monotonic timer到期时只在同一有序journal上发送`agentx/timeoutDue(processId)`，其routing context使用预分配timeout operation；gateway本地timer也进入同一处理路径。`timeoutDue`只是“期限已到”的可信信号，不是外部发送许可：gateway仍须先以core `BeginOperationDispatch`跨过唯一CAS边界，只有`Began=true`才能发送stock `process/terminate`，随后等待真实终态。这样agentx不需要core凭证，也不会绕过pre-dispatch持久化；Phase 1 gateway不可恢复时，signal保留在同进程resume journal内，fresh gateway不能据此虚构恢复，相关operation最终按unknown/runner cleanup收口。不能把 timeout 悄悄塞入 upstream params，也不能在未确认进程退出时只返回“超时成功终止”。

MCP 的 `cwd/path` 使用 env-relative 表示。gateway 根据已登记 root 做语法映射并编码为 upstream 要求的 `file:` URI，但 agentx 的本地 root 才是授权事实：它必须按 URI 语义重新解码、canonicalize 并校验。禁止只用字符串拼接检查路径，`..`、percent encoding、Windows drive/UNC 和 symlink 都必须进入跨平台逃逸测试。

`read_file-v1`把一次MCP调用冻结为恰好一个`fs_read(effect_class=read)` operation/mutation。gateway先用core environment projection检查组合profile，跨过`BeginOperationDispatch`后还必须以当前WSS hello中目标environment的profile再检查一次，再把根目录相对路径编码为平台正确的`file:` URI。unary response table按holder/session、canonical request id和完整routing context关联唯一响应；transport disconnect、fresh generation、resume expiry和shutdown都会把pending read收口为`unknown`。`effect_class=read`只是审计分类，Phase 1即使发生pre-send或ambiguous write也不自动重发。

成功response必须精确为`{chunk,eof}`，其中chunk是canonical base64且decoded bytes不超过调用方limit。core operation ACK只保存response kind、request id、完整response SHA-256和字节数；operation/execution terminal result只保存content hash、bytesRead与eof等紧凑证据，文件内容仅返回当前MCP调用。有效且最终JSON不超过2 MiB时返回`encoding=utf-8`，否则返回canonical `encoding=base64`；1 MiB block的base64上界为1,398,104字节，harness result/text默认上限因此是2 MiB而不是1 MiB。

`apply_patch` 不是 exec-server 的原生方法。Phase 1 每次调用只允许修改一个文件：gateway 先读取、验证 precondition hash 并确定性应用 patch，再调用协商后的 agentserver 条件写扩展，由 agentx 在本地原子验证 hash 后提交。该提交必须降权到目标 env 的执行身份，或调用随发行包签名的固定 fs helper；持机器凭证的控制面不能以自身高权限任意写工作树。未协商条件写 capability 时不暴露该工具；不能用普通“read 后 write”、生成自然语言或启动模型冒充 CAS。多文件 patch 必须拆成多个 execution，并明确不具备跨文件原子性。

Phase 1 不支持跨 run 的 detached process。每个 process handle 都绑定 `executor_id/env_id/run_id/execution_id`；`write_stdin`、`read_output` 和 `terminate` 必须重新验证该绑定。未来若新增 signal 或支持常驻进程，必须新增可审计的 terminal evidence、独立 resource、owner lease、配额和显式审批，不能复用已结束 run 的 capability。

### 8.3 本地安全边界

agentx 在请求进入 stdio exec-server 前执行第一层校验，exec-server 的 sandbox/network policy 再执行第二层约束。至少包括：

- `cwd` 和所有路径先 canonicalize，再验证位于注册 root 内；
- 防 symlink/TOCTOU，能使用 `openat`/no-follow 时不只依赖字符串前缀；
- 环境变量按 allowlist 合并，剥离 executor credential、代理 secret、exec-server `CODEX_HOME`/runtime path 和宿主敏感变量；
- 执行用户、sandbox 和网络权限取“远程 run policy ∩ 本地 owner policy”的交集；
- 限制最大 argv/env/frame、输出 buffer、进程数、运行时间和并发数；
- 默认禁用可选 `http/request`；开启时必须声明目标网络策略并经过审批。
- exec-server 自身必须运行在清洗后的环境与隔离 Codex home 中；模型 key、用户 Codex 登录态、agentx OAuth material 和 llmproxy 地址一律不可见；
- Codex 或任一独立外部 executable 的版本、签名、大小或 checksum 不匹配时 agentx 必须在启动 child 前 fail closed。fs helper、arg0 exec helper 和 Linux sandbox alias 都重入当前 Codex，其字节由 Codex digest 覆盖；Linux `bwrap` 才是单独校验的外部资源。不得回退到系统中另一个 Codex 或 ambient PATH 中的 helper。
- agentx 控制面与 exec-server/命令执行树应使用不同 OS uid、container/user namespace 或等价权限域；机器私钥优先使用 OS keychain/TPM 的 non-exportable key；
- agentx 的 credential、WSS、keychain 和内部 pipe/file descriptor 必须 close-on-exec 且对子进程不可访问；需测试 ptrace、`/proc`、signal、继承 FD 和本地 socket 攻击；
- BYO 机器的 root/本机 owner 能控制 agentx，属于明确的信任边界；但工作树中的不可信代码不能因此获得 executor 机器身份。

这些限制不能从 stock 的宿主失败反推。当前 pinned `ExecParams` 和 launch 路径没有独立 argv/env count/byte guard；64 MiB stdio frame、262,144 JSON value 与宿主 `exec`/`CreateProcess` 限制是三种不同边界，本机 `E2BIG` 不是可移植协议。runtime manifest 分开记录 stock `execServerBounds` 和 agentx `agentxLimits`。首版 agentx 在写 child stdin 前强制 inner frame ≤ 8 MiB、JSON value ≤ 65,536、argv 加可选 arg0 ≤ 256 项且 UTF-8 内容总计 ≤ 16 KiB、最终物化且不继承的 env ≤ 256 项并按 UTF-8 `name=value` 总计 ≤ 16 KiB、write ID ≤ 128 bytes。remote/gateway 还可施加更小 workspace policy，但不能放宽 manifest 上限。

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
4. 完成第3步的key proof后，agentx先发送不占`sessionSeq`的`hello`连接前导，声明agentx protocol、各env的pinned exec-server build/schema、capability probe摘要，以及恢复窗口内仍活跃的`{processId, localExecInstanceId}`集合。可选resume cursor精确为`{gatewayInstanceId, sessionId, generation, agentxSentThrough, agentxReceivedThrough}`。hello由已认证WSS与第3步proof绑定；它仍只是恢复提示，不是可信身份，gateway必须用注册manifest和既有ownership核对，不能接受客户端凭空声明process。
5. fresh连接必须先由core CAS取得新的connection generation，再创建进程内session journal；resume则必须命中同一个`gatewayInstanceId/sessionId/generation`和仍完整的journal，不能增加generation或创建空journal冒充旧session。gateway随后发送不占sequence的`welcome`，明确`fresh|resumed`、双向cursor和固定30秒窗口。resume不满足任一条件时返回terminal `resume_rejected|resume_gap|resume_expired`，agentx先清理旧instance，不能在同一连接静默降级为fresh。
6. fresh session由gateway发送第一条有序`type=lifecycle` frame；其`rpc`使用标准JSON-RPC 2.0：

   ```json
   {
     "type": "lifecycle",
     "sessionId": "...",
     "sessionSeq": 1,
     "ack": 0,
     "generation": 7,
     "rpc": {
       "jsonrpc": "2.0",
       "id": "init-...",
       "method": "initialize",
       "params": {
         "protocolVersion": "2.0",
         "clientName": "agentserver-executor-gateway",
         "outerProfileVersion": "process-v1",
         "processMethods": ["process/start", "process/read", "process/write", "process/terminate"]
       }
     }
   }
   ```

7. agentx在远程层处理`initialize`，返回相同`sessionId`、协商版本和“pinned schema allowlist ∩ capability probe证据 ∩ 本地owner policy”的结果，随后接收有序`initialized`。resume恢复既有远程lifecycle，不重复initialize。这组消息绝不转发给业务stdio child；stock child仍由agentx本地独立完成自己的`initialize → initialized → environment/info/status`。
8. 每个executor同时只有一个有效connection generation。fresh CAS成功后立即fence/关闭旧连接；connection lease记录最近`exec_session_id`。gateway进程内registry只是第二道fence，不能自行发明权威generation。
9. 完成远程`initialized`后，`process/start`由agentx创建并初始化一个专属stdio instance再转发；后续process请求按ownership路由到同一instance。fs请求进入不允许`process/start`的独立lane。method/params按pinned schema做语义等价转发，不能声称整个envelope或request id原样不变；child notification/response反向映射回gateway。
10. 断线按1、2、4、8……30秒指数退避重连；在30秒grace period内携带完整resume cursor，且不关闭活跃stdio instances。access token到期前必须重新认证或重连；executor被吊销后，gateway主动fence连接，agentx结束全部本地exec instances。resume失败、grace到期或gateway要求fresh session时，agentx逐个关闭旧stdio并验证各instance清理其唯一managed process；旧`process_id`不得迁移。

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
    "runAttemptId": "...",
    "runAttemptGeneration": 3,
    "executionId": "...",
    "operationId": "...",
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

wire message分为两层：`hello/welcome/ack/session_error`是连接/会话控制，不占`sessionSeq`；只有`lifecycle/rpc`是有序frame并进入双向journal。独立`ack`没有`sessionSeq`，否则空闲连接会形成无限ack-of-ack；它只释放对端已连续处理frame的内存journal，绝不是core的operation ACK，也不授权`AcknowledgeOperation`。operation ACK必须来自agentx mutation journal接受记录、匹配RPC response或可信terminal evidence并绑定相同connection generation。

agentx 校验 envelope 的 session sequence、generation、workspace/env 绑定和本地 policy。`process/start`先创建专属local exec instance并原子登记ownership；后续process方法按`process_id`选择既有instance，fs方法进入独立lane。agentx将context记入ownership/audit，分配新的本地request id，再编码为stock dialect写入child。child返回的notification根据`process_id`由agentx补回可信context；不能信任远端为已有process任意声明另一个run/execution。

所有转发RPC的method/params/result/notification schema必须与pinned exec-server对齐，但“child实现了”不等于“outer已协商”：

- `environment/info`、`environment/status`只用于agentx本地probe并投影到hello，不进入Phase 1远程业务profile；
- `process-v1`精确只有`process/start`、`process/read`、`process/write`、`process/terminate`，以及反向的`process/output|exited|closed`和`network/policyRequest`；明确排除`process/signal`；
- `capabilityRoots/discoverV1`和`http/request`不协商；
- filesystem read以组合环境profile `process-v1+filesystem-read-v1`加入；旧环境继续合法地只声明`process-v1`。组合profile只增加一个outer方法`agentx/fs/readFileBlock(path, offset, len)`：`1 <= len <= 1 MiB`，`offset <= 2^53-1`，一个outer RPC只对应一个core `fs_read` operation/mutation。远端永不协商stock无界`fs/readFile`，也不允许gateway把`fs/open/readBlock/close` handle暴露成可跨请求复用的能力。

stable 0.146.0已实测拒绝携带platform sandbox的流式读取，错误为`streaming file reads do not support platform sandboxing`。因此agentx为每个outer block read启动不允许`process/start`的一次性fs-only stock instance，在本地授权后执行恰好一次`fs/open(sandbox=null) → fs/readBlock → fs/close`，随后关闭该instance并验证containment cleanup；任一步失败都不得遗留可复用handle。`sandbox=null`只是已验证stock限制下的内部调用形状，不是授权依据。agentx必须在调用前、返回后都按registered root重新canonicalize并复核路径，生产安全边界还必须包含runner OS containment、immutable install和platform safe-open。当前同UID `insecure_dev`无法消除symlink TOCTOU，不能据此声明filesystem profile可生产部署。

`process/start` 必须携带调用方生成的稳定 `processId`、`argv`、`cwd`、env policy、TTY、sandbox 和 network policy。agentx 为它创建一个最多容纳该 process 的 local exec instance；输出使用单调 sequence，stdin 写入使用 `writeId`。

首个`process-v1/shell-v1` outer contract故意比stock 0.146.0更窄：`envPolicy`固定为`inherit=none`且其余filter为空，最终child环境只由显式`env`组成；sandbox固定为managed + restricted filesystem/network，只允许只读的`special:minimal`和落在registered workspace内的`path`读写条目，明确拒绝`special:root`及其他special path，并要求`enforceManagedNetwork=true`。`special:minimal`不是全盘读：在stock 0.146.0中它由pinned runtime本地展开为系统命令/动态库等平台运行根；Linux是`/bin|/sbin|/usr|/etc|/lib*`等只读mount，macOS还包含upstream固定的system runtime与scratch-temp规则。因此每次Codex升级都必须diff并在native平台重跑该profile，不能把它当成稳定的抽象名称。agentx只认可精确的`minimal + read`组合，并继续对所有显式path做canonical registered-root复核。stock可选的`managedNetwork`、`networkProxy`和`windowsSandboxProxySettingsMode`不是远端可控字段；需要的本地proxy启动信息只能由agentx根据受信enrollment/runtime policy注入。对stable 0.146.0源码的逐字段核对确认Windows枚举是`disabled|restricted-token|elevated`，不是`standard`。未来Codex字段或枚举变化必须提升outer profile/manifest并重跑contract gate，不能由宽松反序列化静默穿透。

`capabilityRoots/discoverV1` 可枚举本地 skill/plugin manifest，不是双手执行任务的必要能力，Phase 1 必须在 agentx 层拒绝；未来只有在 owner 显式授权具体 root 与文件类型后才能开放。`http/request` 同样不能仅因 child 支持就出现在远程 capability 中。

stock child 发出的 `network/policyRequest` 参数实际为 `{processId, request: {protocol, host, port}}`，由 agentx 作为本地 client 处理：

- agentx 先用 `process_id` 找回可信 run/execution/env ownership，未知 process 一律 deny；
- 本地 owner policy 为 deny 时立即 deny；远程策略不能覆盖；
- 本地 owner policy 为 ask 时必须通过本机 owner approval channel；Phase 1 未实现该 channel 前按 deny 处理，远程 workspace 用户不能替本机 owner 批准；
- 需要远程 `ask` 时，agentx 以反向 routing envelope 发给 gateway，由同一 approval 流程决定；断线、超时、审批过期或参数不合法一律 fail closed；
- 只有本地与远程策略的交集允许时才返回 `allow`；
- 未协商或未来新增的 child → client request method 默认拒绝，不能由 agentx 自动回答 allow。

这里的 `ask` wire value 不是“让 stock proxy 继续等待审批”。实测 0.146.0-alpha.14 和 stable 0.146.0 都把 client 返回的 `ask` 立即落实为 HTTP 403；因此 agentx 在等待本地或远程审批期间必须保持原 reverse RPC 未决，在自己的 approval deadline 到期时主动回复 `deny`，批准后才回复 `allow`。`policyDecisionTimeoutMs` 只是 controller decision budget，stock 还会额外增加 5 秒 transport margin；它是最后一道断线保护，不是产品 approval TTL，也不能代替 agentx 的主动超时与审计。

当前 pinned client contract 只接受 `http`、`https_connect`、`socks5_tcp`、`socks5_udp`，并限制 `processId` 1–256 bytes、host 1–253 bytes 且无 control/whitespace、port 非零、deny/ask reason 1–1024 bytes 且无 control character。已知 method 的非法参数回复 `deny(not_allowed)`；未知 method 回复 `-32601`。stock 对 RPC error、未知 decision variant、callback 超时、process shutdown 和 connection 关闭同样收敛为 `deny(not_allowed)`。这些只是 protocol fail-closed 规则；agentx 仍必须先校验可信 process ownership 和策略上下文，不能因参数合法就允许。

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

在恢复窗口内，两个方向的发送方都必须保留未ACK的有序frame；恢复时只重传保存的完整frame，保持相同`sessionSeq`、原始piggyback `ack`和payload。接收方对相同sequence重新发送当前独立ACK而不重复投递；若保留窗口内发现相同sequence字节不同，立即以`sequence_conflict`终止。超出bounded receive-digest history的旧duplicate已经无法验证原字节，也必须fail closed，不能仅因sequence较旧就当作安全重复。重复旧frame携带的旧ack不能让本地ACK状态回退，也不能借重复frame推进新的ACK；新的累计进度使用独立`ack`控制帧或后续新sequence承载。副作用request还必须复用原`mutationKey`。若任一方已丢失对端所需范围、peer cursor倒退到已释放journal之前或出现sequence缺口，则返回`resume_gap`并终止该exec session，不能从当前最新帧继续冒充完整恢复。

agentx在一个`exec_session_id`的grace period内维护有界的`mutationKey → request hash → pending|completed|ambiguous response` journal。首个相同key只有一次返回外部执行许可；重复key+相同hash返回同一结果或pending，不再转发child；相同key+不同hash为`mutation_conflict`。journal达到上限时必须在接收新副作用前拒绝，不能驱逐旧key后把重试当新请求。journal丢失意味着旧session只能`resume_rejected`；child crash或无法判断是否执行时记录/返回`ambiguous`。该journal不是跨agentx重启的“恰好一次”承诺。

外层 session sequence 覆盖 response、notification、reverse request 和 lifecycle，不只覆盖 process output。agentx 必须持续读取 child stdout，不能因为 WSS backpressure 停止排空 pipe；缓存超限时产生带丢失范围的 `output_gap/buffer_overflow`，不能静默截断。当前 pinned stock child 的 retained output 上限和 exited-process retention 必须写入 manifest/conformance fixture；不能把 upstream 的有限 `process/read` replay 当作无限恢复日志。

当前两个 characterized release 的 stock retained replay 是每进程 1 MiB，并有 50,000 chunk 的次级上限；stdin dedupe cache 是每进程 4,096 个 write ID 的 FIFO；closed process 约保留 30 秒后删除并允许复用 process ID。agentx 的外层 per-process raw-output delivery/resume buffer 独立设为 8 MiB。超过它不允许退回 stock replay后假装 session sequence 连续：必须标出确切 lost sequence range，并让依赖完整输出的 operation 进入明确的 incomplete/overflow 终态。还必须设置每 connection/global output quota，避免多个 process 各自合法地耗尽节点内存。

stock 能生成的响应不天然受 8 MiB/65,536-value 产品 envelope 约束。agentx capability negotiation 必须排除最坏响应可能超限且没有请求级 cap/分页的 method；不能先请求，再以关闭本地 stdio 作为正常的限流策略。

每个 stdio child 只服务一条本地连接，pipe EOF 后 processor shutdown，不能由新的 agentx 重新 attach。resume cursor只属于agentx外层WSS session；agentx或某个child crash后必须为受影响的lane创建新的`local_exec_instance_id`，该lane的旧process/fs handle全部失效，其他独立instance不受牵连。

WSS lifecycle/routing envelope、JSON-RPC schema 和错误码以`api/schema/agentx-envelope.schema.json`与`api/asyncapi/agentx-wss.yaml`为机器事实源，并用录制fixture做gateway ↔ agentx ↔ stock child双向兼容测试。envelope只承载连接、路由、审计和幂等元数据；确定性process/fs指令仍位于未经语义改写的内层JSON-RPC。JSON Schema不能表达的方向所有权、双向cursor、generation、重复投递和journal覆盖由reference `internal/executorgateway/agentxconn`语义门禁补充。

### 9.4 多副本路由

WSS 是有状态连接，executor-gateway 不能只依赖普通 Service 负载均衡宣称高可用。

Phase 1 明确把 executor-gateway 部署为单副本。它只承诺在同一 gateway 进程存活期间恢复短时网络断线；gateway 进程重启后拒绝旧session的完整resume cursor，仍为 `prepared` 的 operation 保持未发送，已处于 `dispatching|acknowledged` 且尚未由 core 或 agentx journal 证明终态的 operation 标记 `unknown`，agentx 在 grace period 后回收旧 stdio child。数据库中的 connection generation 能 fence 旧写入，但不能替代丢失的双向 frame journal，因此 Phase 1 不宣称跨 pod 恢复。该故障域必须进入 SLO、告警和故障注入测试。

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
- 浏览器使用`run_id + cursor`重连；browser-gateway因而可以无持久状态。cursor是core以HMAC签发、同时绑定`workspace_id/session_id/run_id/after_seq`的opaque position，不是授权凭证；每次读取仍重新检查bearer和当前workspace membership，不能跨scope复用。
- core的事件页为每条event返回同位置cursor；`limit=0,waitMs=0`只验证并解析一个重连cursor，不消费事件。browser-gateway不会在message/reasoning/tool lifecycle中间向浏览器发布进度cursor，只在初始`run.queued`、已提交的lifecycle-safe boundary或授权snapshot rebase后发布`CUSTOM{name:"agentserver.event_cursor",value:{version,runId,cursor,lastEventSequence}}`。这样断线后最多重放一个未完成生命周期，不会从缺少`*_START`的delta中间恢复。
- cursor已过retention window时，只有core事先在同一个lifecycle boundary物化完整snapshot、该边界对应的run status/version/timestamp与rebase position，才可返回明确`cursor_expired`；不能把读取时更晚的current run元数据拼到旧snapshot上。browser-gateway依次发`STATE_SNAPSHOT`和新的cursor。没有已提交snapshot时必须报内部一致性错误，不能返回空流或任意剩余事件冒充安全恢复。

### 10.2 两套上游协议不得混用

- 大脑只使用 stock Codex app-server v2 的 thread/turn/item 协议。
- executor 只使用 stock exec-server process/fs JSON-RPC；agentx 远程 lifecycle 和命名扩展另行版本化。
- app-server 的 `dynamicToolCall` item与`item/tool/call` callback映射为规范tool-call事件，再映射到AG-UI `TOOL_CALL_*`和A2UI卡片。
- worker MCP client收到的progress/elicitation/result与executor process output进入同一execution投影，但不伪装成app-server command item；call id与execution id的映射由worker/gateway显式记录。

### 10.3 Approval

策略值为 `deny | ask | allow`，至少按workspace、executor、env、tool、tool schema/version、path root、network、run actor和policy version配置。Phase 1的唯一工具入口是executor-gateway MCP，worker不直连第三方MCP；未来第三方工具也必须先经过同一policy proxy/core approval，不能依赖server自报annotation。

executor-gateway/core是产品审批的唯一策略权威。app-server以`approvalPolicy=never`运行，只产生dynamic callback，不产生executor通用approval prompt。需要用户决定时，executor-gateway在worker发起的原MCP `tools/call`上发送标准`elicitation/create`；harness-worker作为MCP client把该request与core approval record双向关联。approval record必须持久化绝对`expires_at`，core用CAS决定`approved|denied|expired|cancelled`，worker依据同一deadline设置本地兜底timer，不能让MCP elicitation或对应dynamic callback无限悬挂。

批准本身不是dispatch authority。Core从完整冻结execution fingerprint派生`approval-context` digest，并为每个execution只允许一个approval、每个nonce全局只允许一个record。浏览器决定只执行`pending → approved|denied`：`approve`后execution仍为`pending_approval`；精确live gateway必须携带同一approval/execution/run/attempt/generation/nonce/digest和各自CAS version调用consume，Core在该事务中重新检查expiry、holder/generation及approver当前RBAC，成功后才原子执行`approval → consumed`与`execution → approved`。批准后撤权、降为viewer、attempt被fence或context变化都必须fail closed，不能把曾经的UI点击当成永久能力。

canonical ledger记录`approval.requested|approved|denied|expired|cancelled|consumed`，payload绑定run/attempt/execution scope、tool、nonce、context digest、绝对expiry、decision/approver和approval version。browser-gateway把同一事实投影为两条互不混淆的载体：`CUSTOM{name:"agentserver.approval"}`携带前端decision command所需authority；A2UI v0.9 approval card只做审计展示。Approve/Deny必须调用独立、同时校验browser-gateway workload identity与原始用户bearer的`POST /v2/workspaces/{workspaceId}/approvals/{approvalId}:decide`；A2UI action、surface data或SSE连接本身都不能批准执行。

approval TTL、gateway active-execution deadline和run hard deadline是独立时钟。gateway在`pending_approval`期间暂停自己的active-execution计时；worker的HTTP/MCP transport timeout不能充当产品approval TTL。core到期CAS成功后必须主动向worker下发`expired`，worker将canonical outcome作为`decline`回复gateway的elicitation；若control stream、MCP response path或core expiry确认不可用，worker取消MCP request，并在cleanup grace内调用`turn/interrupt`。显式run cancel同样先取消MCP再interrupt。任何超时、interrupt、断线或清理路径都不得从`pending_approval` dispatch，并须保留彼此不同的审计原因。

修订后的A05由production-shape dynamic probe支撑：stable 0.146.0和0.147.0-alpha.2在`approvalPolicy=never`下仍会把已发布工具精确变成`item/tool/call`，没有Codex通用approval request；未发布工具不会产生callback。旧的direct-MCP `approve|prompt` probe保留为历史characterization，但不再是生产配置。A05只证明没有第二层Codex审批，不能替代A06对worker MCP client elicitation的验证。

A06旧probe证明stock app-server direct-MCP client能转接标准`elicitation/create`，但新架构刻意不走该路径。当前reference运行链已经接通gateway→worker MCP client→harness-control 1.3→pool/Core observe→canonical outcome→gateway consume：worker逐字段关联run/call/generation/catalog/execution/approval/nonce/context/version，只把`approved`映射为带九字段canonical evidence的`accept`，`denied|expired`映射为`decline`，`cancelled|consumed`及control关闭映射为`cancel`。`accept`本身仍不授权dispatch，只有gateway随后成功执行Core `ConsumeApproval`才可跨越发送边界。门禁已经覆盖ACK不等待用户决定、journal/replay、database-time expiry、nonce单次消费、stale generation、MCP/control取消和非accept零dispatch。pinned Linux arm64整栈happy-path也已在stock Codex SHA-256 `cb5e8cb8a333a408ce6adbe0d4fad1845c69772c2216af7c1f88c98a11460dc6`、bwrap SHA-256 `c547cbdc762a70ed216789ffaa4c6c0e7d2beabe32245a498f8e365a9fc8dab4`及agentx commit `3da6e4a`上通过：两条shell approval均为`consumed`后才执行，正常run提交一个checkpoint，显式取消run提交零checkpoint。A06据此关闭；真实TTL/断线故障注入仍是Phase 4退出门禁，不由happy-path代替。

A07 dynamic probe已经确认：pending `item/tool/call`时调用`turn/interrupt`会以`interrupted`结束turn，不发第二次模型请求，也永远不发`serverRequest/resolved`。正常dynamic response同样没有resolved。因此worker必须按类型清理：已回复call以JSON-RPC response写入完成为准，未回复call以所属turn terminal为准，并取消对应MCP request。reference `AppServerRunner`现已把bridge接入单reader、单writer的真实Codex wire事件循环：它固定`initialize → initialized → thread/start|resume → turn/start`参数面，以有界notification sink转发原消息，严格核对`thread/started`、`turn/started`与匹配的terminal，只允许`item/tool/call`反向请求；MCP result必须先claim再由同一writer写response，写成功后才释放，取消/bridge失败/缓冲溢出则先取消MCP、再`turn/interrupt`并有界等待terminal。net.Pipe wire fixture与race门禁已经覆盖正常写屏障、terminal竞态、取消、未知request、写失败、事件溢出和resume catalog digest。新的非live组合门禁使用official SDK和真实HTTP session贯通runner→bridge→MCP client→gateway，并逐帧拒绝bearer进入app-server wire；强制断开in-flight MCP HTTP连接后，runner会interrupt、等到匹配terminal并清空callback。`MCPClient.Close`现在跟踪每个HTTP request，先等待可配置且有硬上限的graceful grace，再abort私有transport并有界返回，不会无限卡在SDK session DELETE。但已断的transport不可能把cancel送到已dispatch的远端handler；gateway必须自己执行connection grace、execution deadline和unknown/terminal收口，不能依赖worker的最后一帧。同一runner驱动stock app-server的live gate也已在macOS arm64 stable binary SHA-256 `ae1d3ffe6d48aec6a4dc3f50e7eb8e0d11962485a6a9406c5a7012139383da02`、size `271056976`上通过。完整A07还没有关闭：已跨`dispatching`的execution独立收口、真实control断线和gateway断线deadline/unknown转移仍待完成。

`ask` 流程必须：

1. `PrepareExecution` 在 core 创建 execution，并冻结规范化参数、tool/schema version、workspace/run/attempt generation、executor/env、policy version 和目标资源；
2. 对上述完整上下文生成 hash、审批 id、过期时间和一次性 nonce，不能只 hash 模型提供的 MCP arguments；
3. gateway向harness-worker这个MCP client发起elicitation，worker将它映射为规范事件和AG-UI interrupt/A2UI审批卡；
4. core 校验 approver 当前 workspace 角色、自批规则、run 状态、审批 TTL 和参数 hash；
5. gateway 在 dispatch 前再次检查 live RBAC 与 generation，并以 CAS 消耗 nonce、将 execution 置为 `dispatching`；同一 approval 只能消费一次；
6. 将 requester、approver、时间、决定、参数 hash、execution id、最终结果或 unknown 写入审计。

即使服务端批准，agentx 本地策略仍可拒绝。

### 10.4 Web 安全

- a2ui-web 只在内存中持 `aud=agentserver-api` token；刷新使用 Authorization Code + PKCE/refresh-token rotation 或重新授权，不使用 localStorage。若未来让 core 与 browser-gateway 使用不同 audience，必须显式取得两个 token 或做标准 token exchange，不能把一枚 token 跨 audience 接受。
- AG-UI/SSE 使用支持`Authorization` header的`fetch` streaming，并在`forwardedProps.agentserver.eventCursor`显式携带最近一条`agentserver.event_cursor`；不能依赖无法设置bearer header的原生`EventSource`，也不能把SSE `Last-Event-ID`误当core cursor。如果改用HttpOnly BFF cookie，则必须把该cookie session、CSRF和注销语义建模，不能同时宣称不存在浏览器会话。
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
                                   │      │         ├─ brain_tool_catalogs
                                   │      │         └─ run_outbox
                                   │      └─ session_leases
                                   ├─ executors ──< executor_environments
                                   │             ├─ executor_connection_attempts
                                   │             └─ executor_connections
                                   ├─ llm_authorizations
                                   ├─ credentials
                                   └─ quotas
```

原设计中的 `browser`（页面 + token + session）不再是一等控制面资源：SPA 是全局静态应用，token 属于浏览器登录态，session 已独立建模。如果未来需要保存用户 UI 偏好，应定义为 user/workspace preference，而不是 browser runtime。

关键状态机：

- run：`queued → starting|cancelled`；`starting → queued|running|cancelling|failed|interrupted`；`running → finalizing|failed|cancelling|interrupted`；`cancelling → finalizing|cancelled|failed|interrupted`；`finalizing → completed|cancelled|failed|interrupted|cancelling`。`starting → queued`只允许exact holder在workload已停止后通过原子`AbandonAttempt`重排；不是普通错误回退。`finalizing`覆盖child优雅退出、process收口、checkpoint上传和CAS提交；这些步骤完成前不能对外宣布completed。cancel与自然完成竞态由先提交的权威状态决定，不能用迟到意图覆盖已经确认的terminal；
- run attempt：主路径为`created → leased → starting → running → finalizing → succeeded|failed|interrupted|fenced`；pre-turn abandon允许`leased|starting → failed`（requeue）或`leased|starting → interrupted`（并发cancel），已接受turn的取消允许`running|finalizing → interrupted`。只有旧attempt尚未让app-server接受`turn/start`且workload已确认停止时才可创建新attempt；任何mid-turn失败都使run进入`interrupted`；
- execution：`created → pending_approval|approved|denied|cancelled`；`pending_approval → approved|denied|expired|cancelled`；`approved → dispatching|expired|cancelled`；`dispatching → running|failed|cancelling|unknown`；`running → succeeded|failed|cancelling|unknown`；`cancelling → cancelled|succeeded|failed|unknown`。跨过 `dispatching` 后，只有收到 agentx/child 的确定拒绝或退出确认才能记为 `failed|cancelled`，否则必须为 `unknown`；
- execution operation：一次 execution 的每个确定性 RPC/副作用步骤各有一行，例如 process start、stdin write、timeout terminate 或条件写。execution先冻结`operation_count`，全部`1..operation_count` ordinal必须在首次dispatch前持久化；已发送分支为`prepared → dispatching → acknowledged → succeeded|failed|cancelled|unknown`，其中未收到ACK的`dispatching`只能直接进入`unknown`。未触发的条件步骤必须走显式非发送分支；首版只允许冻结计划末尾的`timeout_terminate`在全部前序operation terminal后从`prepared → skipped`，并保持connection generation、dispatch时间和ACK为空。`skipped`只作为execution聚合的中性终态，任何其他残留`prepared`都阻止execution terminal。每行拥有独立`mutation_key`、参数hash和effect class。execution只是MCP工具级聚合，不能用一个mutation key覆盖多个可独立发生的副作用；
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
│  ├─ asyncapi/                  # SSE/WSS；含 harness-control.yaml 与 agentx-wss.yaml
│  └─ schema/                    # closed-world JSON Schema；含 harness-control/bootstrap schema
├─ a2ui-web/
├─ deploy/helm/
├─ images/harness/               # harness-worker + pinned stock Codex app-server
├─ packaging/agentx/
│  ├─ runtime-manifest.json      # agentx 独立发行包必须消费的 stock Codex/外部 executable 版本、签名与 digest
│  └─ compatibility-fixtures/    # server ↔ agentx 跨仓兼容 fixture
└─ docs/
   ├─ ARCHITECTURE.md
   └─ IMPLEMENTATION.md
```

agentx 的实现不放入上述 Go module。`github.com/agentserver/agentx` v2 以独立仓库从零实现 connector、owner policy、stdio proxy 与 child supervisor；旧的“从 Codex hard-fork exec-server/remote”实现不复用。两仓以本仓库发布的 versioned schema、fixture 和 runtime manifest 对齐，并在 release CI 跑交叉版本兼容矩阵。

## 13. 工程与可观测性约定

### 13.1 API 契约

- 控制面 REST 采用 contract-first：提交的 OpenAPI 3.x 是 source of truth，生成 Go strict server/client 与前端类型，CI 重新生成并检查 drift；handler annotation 不能反向成为 v2 契约源。
- AG-UI/SSE、规范事件、harness-worker control stream 和 agentx WSS 同样以提交的 AsyncAPI/JSON Schema 为 source of truth；手写语义校验补充 JSON Schema 无法表达的 generation、sequence 和状态约束。
- protocol version、capability negotiation 和错误码必须显式版本化。
- 实现前 pin 一个 Codex commit/version，并保存与 upstream exec-server 的 conformance fixtures；升级 Codex 时先跑协议差异检查。
- 同一 pinned Codex build 还必须生成 app-server schema fixture，验证 initialize、thread start/resume、`dynamicTools`、turn start/interrupt、`item/tool/call`、typed cleanup、terminal event 与 rollout checkpoint 布局。

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
| D3 | executor-gateway 是 harness-worker 的 MCP server，同时是 agentx 远程 client/router | app-server 不直接连接 MCP；工具授权、审批和执行协议仍在一个清晰边界完成 |
| D4 | 大脑只用 app-server v2，executor 只用 process/fs JSON-RPC | 避免 thread/item 与 process/output 事件语义混淆 |
| D5 | core 持有 session/run/event 事实 | browser gateway 和 harness 均可无状态恢复 |
| D6 | Phase 1 只做 remote/BYO executor | managed sandbox 生命周期和存储尚未完整设计 |
| D7 | 用户 token 不沿内部链透传 | 降低 bearer 泄漏与 confused-deputy 风险 |
| D8 | 不自动重放结果不明的副作用 | 网络重试不能保证进程/fs 操作恰好一次 |
| D9 | Phase 1 harness 采用常驻 pool + per-attempt 本地 fork/exec，run 热路径不创建 Kubernetes workload | harness 本地不执行任意用户代码；以小的共享 Pod 故障域换取稳定的毫秒级 launcher 延迟，同时仍不复用 Codex 进程和临时状态 |
| D10 | execution 在副作用 dispatch 前持久化，`dispatching` 是不可回退边界 | gateway/transport crash 后才能诚实区分未发送、已确认与 unknown，兑现不重放承诺 |
| D11 | native checkpoint 只在 completed turn 提交，UI/审计事件与模型可见历史分离 | 过滤展示内容不能破坏 app-server thread 恢复语义，mid-turn 不能伪恢复 |
| D12 | Phase 1只有executor-gateway向worker暴露工具，不允许app-server或worker直连第三方MCP | 第三方annotation不是可信授权事实；未来必须先进入内部policy proxy再走同一dynamic bridge |
| D13 | executor-gateway/core 是唯一产品审批权威；gateway向作为MCP client的worker发elicitation，app-server使用`approvalPolicy=never` | 避免app-server与gateway双重审批，并给timeout/cancel/fence明确语义 |
| D14 | Pod内按UID默认拒绝OUTPUT：worker只到pool+executor MCP，app-server只到llmproxy | NetworkPolicy无法区分同Pod worker/child；MCP bearer和可达性都不能进入app-server域 |
| D15 | Phase 1 executor-gateway 单副本，resume 只覆盖同进程短时断线 | 跨 pod resume 需要 durable frame journal 与 owner routing，不能只凭 connection lease 声称恢复 |
| D16 | agentx 保持独立仓库并从零改写为 stock exec-server supervisor | 现有 hard-fork 把执行引擎复制进 agentx，与 v2 的 stock stdio 边界冲突 |
| D17 | dynamic-tool-only 使用`environments: []`、Codex MCP deny-all、显式builtin禁用、冻结`dynamicTools`与模型请求捕获共同证明 | system prompt和单一配置开关都不能构成能力隔离；绕开stock通用MCP resource handler |
| D18 | execution 下增加 execution operation | 一个 MCP 工具可能触发多个独立副作用，每个步骤都需要自己的 mutation 与 unknown 边界 |
| D19 | v2 API 采用 contract-first | core、gateway、worker、agentx 和 Web 多消费者需要在实现之前共享稳定、可生成的协议源 |
| D20 | Phase 1每个受管process独占一个stock exec-server stdio instance；outer profile不暴露`process/signal` | connection shutdown可无旁路地回收该process后代；stock空signal响应不具备可审计语义 |
| D21 | worker MCP reference profile固定`2025-11-25` stateful；其他协商版本在catalog读取前拒绝 | 当前approval依赖`tools/call`内的server-originated `elicitation/create`，official SDK的新stateless profile不能承载该反向请求；协议升级必须连同approval transport重新设计和门禁 |
| D22 | `process-v1/shell-v1`固定clean-env与managed/restricted sandbox，stock可选proxy启动字段由agentx本地受信策略生成 | 双手只执行确定性任务；远端不能恢复ambient env、选择任意proxy或借upstream新增字段扩大能力 |
| D23 | filesystem read使用组合profile和一次性fs-only `open(null) → readBlock → close`，远端不开放stock handle或`fs/readFile` | stock流式读取不支持platform sandbox；有界outer请求、agentx双重root复核与runner containment共同形成可审计边界 |
| D24 | run cancel采用`cancelling`两阶段协议；pre-turn停止用`AbandonAttempt`在run锁内仲裁requeue/cancel | API调用不能证明远端workload已经停止；原子交接消除“最后观察”与holder释放之间的竞态，并让session、双lease、event/outbox一致收口 |

## 15. 设计审查结论与实现门槛

原设计存在的主要问题已经在本文中修正：

- 把双手错误设计成第二个模型运行时；
- 将 `codex exec` 的模型事件与 `exec-server` 的确定性 RPC 混为一谈，且遗漏了可直接复用的本地 `--listen stdio` transport；
- harness 一边宣称无状态，一边拥有会话持久化和工作区能力；
- 缺少作为 app-server client 的 per-run host，无法实际发起 thread/turn、响应 server request 或完成 cancel/checkpoint；
- 让stock app-server直连executor MCP，无法移除通用resource handler，也把MCP bearer和endpoint带入模型子进程；现改为worker-owned dynamic bridge与Codex MCP deny-all；
- browser-gateway、harness-pool 和 core 同时声称拥有 session；
- 用户 bearer、workspace 机器 token 和模型凭证跨信任域复用；
- Hydra 缺少实际的 login/consent bridge；
- executor 重连缺少 generation fencing，多副本缺少连接 owner routing；
- 假设stock `process/signal`成功响应可证明送达，并让多个process共享一个无法operation-scoped回收的stdio instance；现排除signal并让每process独占instance；
- process/fs 副作用缺少审批、幂等边界和 ambiguous 结果；
- execution 只在结果阶段记录，无法判断 gateway crash 是否已经 dispatch；
- 允许直连第三方 MCP，同时又宣称所有副作用都经过统一审批；
- 把已脱敏 UI 事件、选择性 rollout 和原生 thread resume 混为一谈；
- capability 续期、外部 endpoint egress 和浏览器 SSE bearer 传输缺少可执行机制；
- harness 以 per-run Job 获得了不符合当前威胁模型的高启动成本；现改为诚实的共享 pool Pod + per-attempt 进程边界，不再声称 per-run Pod 隔离；
- app-server、executor RPC 和 AG-UI 之间缺少规范事件层。

PR 11已把executor contract部分落成机器事实源：`agentx-envelope.schema.json`、`agentx-wss.yaml`、`executor-mcp.schema.json`、稳定`process-v1` profile，以及补充JSON Schema无法表达之方向、sequence/ACK、generation/resume、bounded journal和mutation幂等语义的Go reference kernel。

PR 12已把gateway/core连接和首个executor shell纵向切片变成可运行代码：forward-only `0004`保存executor/environment、不可复用connection attempt和当前generation holder，`0005`增加只适用于尾部`timeout_terminate`的非dispatch `skipped`终态；mTLS internal command API原子执行acquire/renew/activate/fence以及七个execution/operation命令；真实WebSocket server完成`hello/welcome`、远端`initialize/initialized`、ACK、bounded replay、同进程30秒resume和fresh generation fence。fresh acquire只得到`connecting`，远端lifecycle成功后才把env发布为`online`；旧connectionId留在attempt表中，更新generation后重试只能被fence，不能反向夺回owner。gateway拥有按generation和完整routing context关联的有界process exchange；独立agentx仓库已实现connector/runner IPC、registered-root重复复核、outer timeout signal及每process独占的stock exec-server stdio监管。core的online environment查询以数据库时钟检查lease，gateway的stateful MCP `/mcp`固定`2025-11-25`，实际开发serve只在shell terminal链装配完成后发布`list_environments|shell`。execution transport不接受调用方提供的digest：core从原始JSON按tool schema/domain重新验证、JCS和hash；gateway以单调transition allocator和core返回version推进两项operation，只有一次性`Began=true`才发送start/terminate，匹配RPC response才写各自ACK，真实`exited/closed`才写terminal/output complete，deadline前终态则用`SkipOperation`关闭预分配timeout。发送歧义不重发并收为unknown。shell mapper固定生成`special:minimal(read) + registered-root(write)`；exact stock 0.146.0 macOS live gate已证明该profile可在clean env下执行绝对系统命令，而workspace-only path负向探测以exit 134失败，防止再次删掉必要的platform runtime。相关mapper、MCP组合、socket和race门禁已通过；DB-clock lease过滤case仍必须由配置PostgreSQL执行integration gate。

该状态仍不能称为生产可部署executor：loopback insecure-dev已经把per-tool `ask`、Core Create/Observe/Decide/Expire/Cancel/Consume、真实MCP elicitation、harness-control outcome和consume-before-dispatch接通，并通过pinned Linux整栈happy-path；但生产agentx OAuth/机器key proof、enrollment、core签发/在线撤销的短期run capability、gateway进程丢失后的dispatching/acknowledged恢复审计、平台containment、故障注入门禁和部署manifest仍未实现。当前gateway命令只提供显式loopback `--insecure-dev`，并要求静态开发bearer加完整run/attempt/version/catalog scope；生产serve模式刻意不存在。任何未关联business RPC与不匹配MCP call metadata仍会fail closed。

进入实现前必须完成以下 Phase 0 gate：

- [ ] 固定Codex版本与Codex/外部executable digest，完成stock exec-server stdio启动/RPC fixture、outer capability排除`process/signal`、每process独占instance、正常关闭与root/descendant异常时无旁路process-tree回收、agentx代理兼容测试（reference adapter已在exact stable macOS arm64通过；真实agentx兼容和目标平台containment仍待完成）。
- [ ] 完成harness-worker → stock app-server stdio conformance：initialize、thread start/resume、冻结`dynamicTools`、`item/tool/call` response/interrupt cleanup、terminal event、child crash和control-stream fence。
- [ ] 验证本地 process launcher：签名 manifest/control capability 只经 inherited pipe 传递且不进入 argv/env/临时磁盘；worker/app UID 不可读 pool credential；正常退出、startup failure、cancel、lease loss、holder crash 都回收完整进程组；并发 attempt 的目录、FD、capability 不串扰，并量化共享 Pod OOM/崩溃的故障域。
- [x] 验证app-server child无内建工具/Codex MCP、整个mount view无工作树；worker credential/MCP bearer/FD不可见且non-`CLOEXEC` sentinel也被final-exec close-all；worker UID只访问pool+executor MCP，child UID只访问llmproxy，direct/redirect/MCP sink均为零child请求（exact stable 0.146.0 `linux-arm64`；其他平台与真实Kubernetes部署另验）。
- [ ] 完成 completed-turn checkpoint 原生 round-trip、hash/schema 校验、对象原子提交和 mid-turn crash 不恢复原 turn 测试；模型可见 tool result 与脱敏 UI 事件分别验证。
- [ ] 完成 session lease、run-attempt lease/generation、producer idempotency、cursor-expired snapshot 和大 payload 临时对象/孤儿清理原型。
- [ ] 完成 `PrepareExecution → approval → dispatching → ACK/running → terminal` 状态机，并在 DB commit、WSS send、agentx ACK、MCP response 各边界注入 crash，证明未知副作用不会自动重放。
- [ ] 验证 executor 侧空 Codex home、清洗环境、无模型 credential、禁用 `--remote`/`http/request`，且不能回退到未校验系统 Codex。
- [ ] 完成 executor enrollment、独立机器身份、吊销、30 秒 WSS 恢复和 stdio child 跨重连存活测试。
- [ ] 完成 child → agentx `network/policyRequest` 的 allow/deny/ask、断线超时 fail-closed 和审批审计测试。
- [ ] 完成路径逃逸、symlink/TOCTOU、环境变量泄漏和 sandbox policy 安全测试。
- [ ] 完成 agentx 控制面与执行树的 uid/namespace、ptrace/`/proc`、继承 FD、signal 和 non-exportable key 隔离测试。
- [ ] 完成worker MCP client收到的elicitation → core approval链路：参数/上下文冻结、独立approval expiry主动清理、gateway active-time deadline、nonce单次消费、cancel/断线fail-closed和审计闭环，证明app-server不参与第二套审批。
- [ ] 验证 Phase 1 executor-gateway 单副本部署、同进程 30 秒 resume，以及 gateway 重启后 fail-closed 拒绝 resume/operation 进入 unknown；Phase 2 owner routing 不进入首版。
- [ ] 验证harness-worker/app-server crash后不会自动重放已发出的MCP副作用，worker MCP transport或dynamic callback丢失时execution可独立收口而run明确interrupted。
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
- Codex App Server 官方手册与 `codex-rs/app-server-protocol`：大脑 initialize、thread/turn/item、`dynamicTools`、`item/tool/call`、interrupt 与 persisted rollout 语义；
- `codex app-server generate-json-schema`/对应 pinned schema artifact：harness-worker 的实际 wire 与 checkpoint conformance 基线。

实现时必须记录所对齐的 Codex commit 和发行包 digest。若 upstream wire schema 变化，以显式 protocol version、manifest 与兼容层处理，不能让 agentx 随 Codex 最新版本或用户 `PATH` 静默漂移。
