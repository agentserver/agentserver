# agentserver v2 — 实施设计

> 状态：实施基线（draft）
>
> 上位设计：ARCHITECTURE.md
>
> 本文回答“按什么顺序、以什么代码边界、通过什么验收门槛把 v2 做出来”。架构语义以上位设计为准；本文发现的实施级歧义已经同步回 ARCHITECTURE.md。

## 0. 实施结论

v2 不适合按组件横向铺开后再联调。正确顺序是先验证最可能推翻架构的 stock Codex 假设，再建设 core 状态内核，随后分别打通 executor 和 harness 两条纵向链路：

1. 建立 Codex conformance lab，锁定 stock release、wire schema、dynamic-tool-only、typed callback cleanup 和 checkpoint。
2. 实现 core 的 run、attempt、lease、event、execution operation 和 outbox 状态内核。
3. 先打通无模型的 executor 垂直切片：executor MCP → gateway → WSS → agentx → stock exec-server stdio。
4. 建立 harness 垂直切片的权威控制边界：harness-pool → per-run worker（dynamicTools/MCP bridge）→ stock app-server stdio，并由worker调用executor MCP。
5. 在 app-server production child装配完成前，先前置 browser-gateway 的协议切片：canonical event合同、AG-UI/A2UI投影、SSE/cursor/rebase和可替换core backend；它不伪装真实模型链已经可部署。
6. 再完成 app-server child、checkpoint/finalization、Hydra、完整审批与 Web UI部署。
7. 用故障注入、安全隔离测试和 Kubernetes 部署门槛完成收口。

首个端到端目标不是完整 Web 产品，而是一条可证明不会隐式重放副作用的命令链路：

~~~text
scripted model
  → stock app-server
  → item/tool/call shell(argv[])
  → stateless harness-worker MCP client
  → executor-gateway
  → agentx
  → stock exec-server process/start
  → terminal MCP result
  → completed-turn checkpoint
~~~

在这条链路通过前，不并行建设完整产品前端、managed executor、可复用 Codex 进程池或多副本 executor owner routing；但可以先完成不依赖真实模型链的 browser协议合同和纯投影器，以尽早固定前后端边界。harness-pool 的本地 process launcher 不是“复用 Codex 进程”；worker 和 app-server 仍每 attempt 新建并销毁。

## 1. 已固定的工程决策

| 主题 | Phase 1 选择 | 说明 |
|---|---|---|
| 服务端语言 | Go，独立 v2 module | module 为 github.com/agentserver/agentserver/v2，不复用 v1 runtime package |
| 用户侧 agentx | 独立仓库、独立发行，重新实现 | 现有 hard-fork exec-server 的 agentx v1 不符合“agentx + stock exec-server stdio”边界 |
| HTTP 路由 | chi + pinned oapi-codegen v2 strict server/client | OpenAPI 是 source of truth；Web 类型由 pinned openapi-typescript生成 |
| 数据库 | PostgreSQL + pgx + sqlc | 不用 ORM；状态迁移由显式 domain command 完成 |
| migration | forward-only SQL、内嵌 checksum、单独 migrate command | Helm migration Job 持 PostgreSQL advisory lock；服务副本不自行迁移 |
| durable queue | PostgreSQL outbox + FOR UPDATE SKIP LOCKED | LISTEN/NOTIFY 只唤醒，不作为事实源；Phase 1 不引入 Redis/Kafka |
| MCP | pinned 官方 Go MCP SDK | executor-gateway提供Streamable HTTP MCP，harness-worker是client；app-server不配置MCP |
| agentx transport | 出站 WSS + versioned JSON envelope | 内层 stock exec-server dialect省略 jsonrpc 字段 |
| harness control | mTLS WebSocket control stream | JSON control frame + 有界 binary checkpoint chunk；独立于 app-server stdio |
| 对象存储 | S3-compatible API | 数据加密、hash 校验和 DB pointer CAS 必须由应用协议保证 |
| harness runtime | 常驻 harness-pool + per-attempt 本地 `fork/exec` | run 热路径不创建 Job/Pod；每次启动全新 worker/app-server 进程组和临时目录，是否创建新 attempt 仍只能由 core/harness-pool 决定 |
| executor-gateway | 单副本 | 只承诺同一 gateway 进程内的 30 秒 WSS resume |
| API 方法 | contract-first | OpenAPI、AsyncAPI、JSON Schema 先于 handler；CI 检查生成物 drift |

v2/go.mod 使用独立 module 只能减少误复用，不能单独证明与 v1 隔离。CI 还必须检查 v2 依赖图，拒绝任何 github.com/agentserver/agentserver/internal/... 的 v1 import。根目录 go test ./... 会跳过 nested module，因此根 CI 必须显式执行 go -C v2 test ./...。

## 2. 代码所有权与仓库布局

### 2.1 agentserver 仓库

~~~text
v2/
├─ go.mod
├─ go.sum
├─ tools.go
├─ Makefile
├─ cmd/
│  ├─ agentserver-core/
│  ├─ browser-gateway/
│  ├─ harness-pool/
│  ├─ harness-worker/
│  └─ executor-gateway/
├─ api/
│  ├─ openapi/
│  │  ├─ public.yaml
│  │  └─ internal.yaml
│  ├─ asyncapi/
│  │  ├─ canonical-events.yaml
│  │  ├─ harness-control.yaml
│  │  └─ agentx-wss.yaml
│  └─ schema/
│     ├─ canonical-run-event.schema.json
│     ├─ harness-control.schema.json
│     ├─ harness-bootstrap.schema.json
│     ├─ agentx-envelope.schema.json
│     └─ executor-mcp.schema.json
├─ gen/
│  ├─ publicapi/
│  ├─ internalapi/
│  └─ contracts/
├─ internal/
│  ├─ bootstrap/                 # config、lifecycle、health、graceful shutdown
│  ├─ coredb/                    # migration与显式PostgreSQL domain command边界
│  │  ├─ migrations/
│  │  └─ query/                  # 稳定后由sqlc生成的查询片段
│  ├─ core/
│  │  ├─ workspace/
│  │  ├─ session/
│  │  ├─ run/
│  │  ├─ event/
│  │  ├─ execution/
│  │  ├─ approval/
│  │  ├─ executor/
│  │  └─ capability/
│  ├─ browsergateway/
│  ├─ harnesspool/
│  ├─ harnessworker/
│  │  ├─ appserver/
│  │  ├─ toolcatalog/
│  │  ├─ mcpbridge/
│  │  ├─ checkpoint/
│  │  └─ control/
│  ├─ executorgateway/
│  │  ├─ mcpserver/
│  │  ├─ execution/
│  │  ├─ agentxconn/
│  │  └─ routing/
│  ├─ codexwire/                 # pinned dialect codec；不承载业务状态
│  ├─ objectstore/
│  ├─ cryptobox/
│  └─ telemetry/
├─ conformance/
│  ├─ codex/
│  │  ├─ appserver/
│  │  ├─ execserver/
│  │  ├─ fakemodel/
│  │  └─ fakemcp/
│  └─ fixtures/
├─ packaging/
│  └─ agentx/
│     ├─ runtime-manifest.json
│     └─ compatibility-fixtures/
├─ images/
│  └─ harness/
├─ deploy/
│  └─ helm/
└─ docs/
   ├─ ARCHITECTURE.md
   ├─ IMPLEMENTATION.md
   └─ protocols/
~~~

shared 目录只放真正跨 domain 的稳定基础类型；不能把业务逻辑堆入 utils。core 是唯一可写 PostgreSQL 的产品组件，其他组件通过 internal API 调用 core，不能链接 store/query 后绕过状态机。

### 2.2 agentx 仓库

github.com/agentserver/agentx 保持独立发布，但 v2 代码从零开始，不复用旧 Rust hard-fork 的执行引擎：

~~~text
agentx/
├─ go.mod
├─ cmd/agentx/
├─ internal/
│  ├─ enrollment/
│  ├─ identity/
│  ├─ connector/                # OAuth/key binding、WSS、resume
│  ├─ policy/                   # owner policy 与远端策略求交集
│  ├─ supervisor/               # child、process tree、graceful cleanup
│  ├─ execstdio/                # stock dialect codec 与 request-id 映射
│  ├─ ownership/                # process/env/run 绑定
│  ├─ journal/                  # 30 秒 mutation/output 恢复窗口
│  └─ platform/
│     ├─ linux/
│     └─ darwin/
├─ contracts/                   # 从 agentserver tag 同步的 schema/fixture
├─ packaging/
│  ├─ runtime-manifest.json
│  └─ release/
└─ tests/
   └─ compatibility/
~~~

旧文档 docs/superpowers/specs/2026-06-23-agentx-extraction-design.md 描述的是“把 exec-server hard-fork 进 agentx”。它只代表 v1 历史，不是 v2 实现输入；v2 agentx 必须以一份新的 superseding design 为准。

## 3. Codex runtime lock

app-server 与 exec-server 都属于 experimental integration surface。不能只 pin 语义版本，也不能使用开发机 PATH 上恰好存在的二进制。

Phase 0 生成并提交 packaging/agentx/runtime-manifest.json，至少包含：

~~~json
{
  "manifestVersion": 1,
  "codexRelease": "待 Phase 0 固定",
  "codexCommit": "upstream commit",
  "appServerSchemaSha256": "...",
  "appServerSchemaDigestAlgorithm": "canonical-json-tree-v1",
  "execProtocolSourceSha256": "...",
  "execServerBounds": {
    "maxStdioFrameBytes": 67108864,
    "maxJsonValues": 262144,
    "argvEnvLimit": "transport-and-platform-only",
    "retainedOutputBytesPerProcess": 1048576,
    "retainedOutputChunksPerProcess": 50000,
    "retainedStdinWriteIdsPerProcess": 4096,
    "exitedProcessRetentionMilliseconds": 30000
  },
  "agentxLimits": {
    "maxFrameBytes": 8388608,
    "maxJsonValues": 65536,
    "maxArgvElements": 256,
    "maxArgvBytes": 16384,
    "maxEnvVariables": 256,
    "maxEnvBytes": 16384,
    "maxWriteIdBytes": 128,
    "maxOutputBufferBytesPerProcess": 8388608
  },
  "checkpointAllowlistVersion": 1,
  "agentxProtocolVersion": "2.0",
  "artifacts": {
    "linux-amd64": {
      "codex": {
        "path": "bin/codex",
        "sourceUrl": "https://固定发布地址",
        "sha256": "...",
        "sizeBytes": 123
      },
      "externalExecutables": {
        "bwrap": {
          "path": "codex-resources/bwrap",
          "sourceUrl": "https://固定发布地址",
          "sha256": "...",
          "sizeBytes": 123
        }
      }
    }
  }
}
~~~

锁定流程：

1. 只接受可追溯到官方 stock release/tag 的构建；本地 Codex checkout 的自定义 commit 不能作为 release pin。
2. inner runtime manifest 对每个平台记录固定 HTTPS 下载来源、bundle 内相对路径、精确大小、Codex SHA-256 和所有独立外部 executable；拒绝 symlink、路径逃逸、未知字段和临时 query URL。`--codex-run-as-fs-helper`、arg0 exec helper 与 Linux `codex-linux-sandbox` alias 都重入当前 Codex executable，不是三份可独立校验的文件，其可信字节由 Codex digest 覆盖。Linux 真正独立的执行资源是 `codex-resources/bwrap`；Windows command runner/setup 等未来进入支持面时也必须逐文件列出。manifest 本身及外层 agentx/harness release bundle 使用 detached signature，外层 release metadata 记录签名、SBOM 与 manifest digest，避免在被签名文件内部产生自引用签名。
3. 运行 codex app-server generate-json-schema，保存原始生成物；已验证 stock generator 的合并 schema 可能随机排列 JSON object keys，因此 manifest 使用显式版本化的 `canonical-json-tree-v1`：逐文件解析 JSON、保留数组顺序、按 key 重编码 object，再按相对路径树 hash。连续生成两次的 canonical digest 必须一致；不能 pin 不可复现的 raw tree digest。
4. exec-server 没有等价的稳定 schema generator时，保存 protocol 源文件 digest、录制 fixture 和语义探针结果。
5. harness image 与 agentx release bundle 必须引用同一个 manifest digest。
6. 启动时发现 version、digest、外部 executable 或 schema 不匹配，worker/agentx 均 fail closed。校验器必须先验证当前平台的完整 artifact set，再生成只含绝对 Codex 路径和受控 runtime PATH 的 launch plan；任一失败都不能调用 process starter。
7. 每个 Linux release/architecture 组合还必须在 native disposable image 中，用只读 root、非 root 用户、零 capability、无网络和 poisoned ambient PATH 跑真实 sandbox 请求；跨架构仿真结果不能代替该平台证据。
8. Codex 升级单独提交：先更新 lock，再更新 fixture，最后通过全部 conformance；不能顺手随业务版本升级。

当前开发机显示的 Codex 版本或本地源码 HEAD 只能帮助调研，不能写成生产 lock。

## 4. Phase 0：Codex conformance lab

### 4.1 测试运行方式

conformance test 是 Go subprocess test，不依赖 v1 gateway：

- 通过 AGENTSERVER_CODEX_BIN 指向待验证的绝对路径；
- 每个用例创建独立临时 CODEX_HOME、cwd 和环境变量 allowlist；
- fake model server 捕获实际 Responses 请求并返回 scripted response/tool call；
- fake MCP server分阶段实现并保持请求/响应上限；direct-Codex-MCP fixture保留为负向/历史characterization，新production fixture由reference harness MCP client执行initialize、tools/list、tools/call、受限SSE/elicitation和cancel，其他方法仍fail closed；
- child stdout 只能解析 JSONL，stderr 单独采集并做 secret scan；
- 所有 wire message 保存为 scrubbed golden fixture；
- live binary tests 与 fixture-only tests分开，普通单元测试不依赖外网或真实模型；
- CI 使用 manifest 下载并校验的 stock artifact，不使用 runner PATH。

### 4.2 app-server 必过探针

| 编号 | 探针 | 通过条件 |
|---|---|---|
| A01 | stdio lifecycle | initialize → initialized → thread/start → turn/start → turn/completed；wire 省略 jsonrpc |
| A02 | experimental gating | initialize 开启 experimentalApi 后 environments: [] 被接受；未开启时明确失败 |
| A03 | dynamic-tool-only surface | 无Codex MCP配置；fake model捕获的tools精确等于冻结`dynamicTools`；builtin、未发布tool和通用MCP resource handler不可见/不可dispatch，真实批准call成为`item/tool/call` |
| A04 | Codex MCP deny-all | system requirements固定`mcp_servers = {}`；direct executor、user和trusted-project MCP均零请求，dynamic tool surface不受影响 |
| A05 | 无双重审批 | production thread使用`approvalPolicy=never`仍产生批准dynamic callback，且不产生Codex通用approval request；产品审批只在worker MCP client侧 |
| A06 | worker MCP elicitation | reference/real worker调用fake gateway MCP；`elicitation/create`经pool/core决定并回到gateway，覆盖accept/decline/cancel、主动TTL、nonce/generation和断线，不经过app-server |
| A07 | typed interrupt cleanup | 单reader/writer loop中`turn/interrupt`产生terminal interrupted；未回复dynamic call以所属turn terminal清理并取消MCP，正常call以response写入清理；有界event overflow fail closed，两者都不等待`serverRequest/resolved` |
| A08 | graceful shutdown | turn terminal、typed callback cleanup及execution/process收口后关闭stdin，child有界正常退出；rollout、SQLite/WAL状态稳定，无固定sleep |
| A09 | dynamic checkpoint round-trip | 每个brain thread只保存单个rollout JSONL并绑定tool catalog digest；cold resume保留dynamic call/result和原schema，不重放executor MCP副作用；schema变化创建新thread |
| A10 | mid-turn crash | 模型请求 in-flight时 hard-kill child；丢弃 crash runtime，只从上一个已提交 checkpoint创建不同的新 turn，且模型上下文不含被放弃 turn |
| A11 | secret exclusion | MCP bearer只在worker→gateway request中实际使用并轮换；child env/config/FD/rollout均无bearer，runtime-only sentinel不进checkpoint，dynamic结果仍完整 |
| A12 | child isolation | 真实worker→app UID链证明worker credential/proc/FD/工作树不可见；worker IPv4只到pool+executor MCP，app IPv4只到llmproxy，app对MCP/direct/redirect/DNS/IPv6均零命中 |

A03 不能通过“配置看起来正确”判断。测试必须检查实际发送给模型的 tool schema，并让 scripted model尝试调用一个禁止工具，确认 app-server不能执行。

A04 的 managed layer不能通过普通临时`CODEX_HOME`注入。official release固定从Unix `/etc/codex/requirements.toml`读取system requirements；源码中的`CODEX_APP_SERVER_MANAGED_CONFIG_PATH`是`debug_assertions`专用测试钩子，0.146.0 official artifact会忽略它。因此A04 job必须在一次性image/mount namespace预装`mcp_servers = {}`，同时配置direct executor、额外user MCP和trusted-project MCP，最终从managed sentinel、三类endpoint零请求和精确dynamic tool surface证明Codex MCP deny-all。不得改开发机`/etc`，也不得用debug build代替stock artifact。`configRequirements/read`只证明layer加载；`mcpServerStatus/list`也不是enablement名单。

`conformance/image/a04`已实现并实跑该disposable job：Go test被交叉编译进scratch image，rootfs只读、外网关闭，`/etc/codex`必须是空的`nodev,nosuid,noexec` tmpfs；测试自身在写真实system path前复核mountinfo，且要求调用方提供独立可信的release/SHA/size。runner同时支持Docker-compatible runtime与Apple `container`。HTTPS MCP fixtures使用临时CA，image用例确认system requirements sentinel、direct/user/project endpoint零请求，以及client-supplied `executor.approved_echo`是唯一模型工具。

official stable 0.146.0 Linux amd64 musl的正式deny-all Make target已在Apple `container` 1.2.0通过。输入archive SHA-256为`5ba3b9405543953081f661d0854d266f76e2abbe51d41349355a36de7673776a`；image内再次核对release `0.146.0`、解包binary SHA-256 `2e863156ed35ecc5253b1e2f907a9143077b9f7cb51942070c61996471ff6e04`和size `311001136`。因此修订后的A04对该exact artifact关闭；production pin仍取决于其余修订门禁。

A05旧direct-MCP probe已在0.146.0-alpha.14与stable 0.146.0上固定`approve|prompt`差异，继续作为回归characterization，但不再代表生产配置。修订后的production probe在stable 0.146.0与0.147.0-alpha.2上使用`approvalPolicy=never`和唯一`executor.approved_echo` dynamic tool：真实调用直接产生`item/tool/call`，没有Codex通用approval request；未发布tool也不会产生callback。因产品approval发生在worker MCP client侧，`never`不会自动拒绝它。A05据此关闭app-server这一半；gateway主动elicitation仍由A06单独验证。

A06旧probe证明标准MCP elicitation协议本身可工作，但它由stock app-server充当MCP client，新架构不依赖该路径。`internal/harnessworker`使用official Go MCP SDK建立有界连接、分页读取并逐字节核对冻结catalog，收到dynamic call后发唯一一次`tools/call`；gateway在该调用内发`elicitation/create`，worker校验execution `_meta`与可信run/call/generation/catalog/execution/approval/nonce/context/version关联。当前链路已经继续接入harness-control 1.3、pool/Core long-poll、持久化approval CAS和gateway consume；测试覆盖ACK早于用户决定、Core主动TTL expiry、nonce单次消费、stale generation、control journal/replay、MCP取消以及非批准分支零dispatch。pinned Linux arm64整栈smoke又验证两条真实shell execution均在approval进入`consumed`后才到stock exec-server，并分别收口为正常checkpoint与取消零checkpoint，A06据此关闭。pending期间app-server只持有原dynamic callback，看不到elicitation；gateway的active execution deadline由自身状态机暂停，不能依赖Codex `tool_timeout_sec`。

reference client显式锁定MCP `2025-11-25` stateful Streamable HTTP profile。当前official Go SDK v1.7.0的新`2026-07-28` stateless profile不能在`tools/call`内承载本设计使用的server-originated `elicitation/create`，所以连接若协商到其他版本会在`tools/list`前fail closed；其他版本的标准`_meta`也不能被静默忽略。未来升级必须先更换approval transport设计并增加独立conformance，不能随SDK latest漂移。

A07的dynamic probe已在stable 0.146.0和0.147.0-alpha.2上固定关键wire事实：pending `item/tool/call`时`turn/interrupt`成功并产生`interrupted` terminal，不发第二次模型请求，也没有`serverRequest/resolved`；正常dynamic response同样没有resolved。reference `DynamicBridge`按request type维护有界outstanding set，`AppServerRunner`则已把它接入一条one-shot Codex wire事件循环：唯一reader pump只向主循环交付消息，生命周期request、interrupt和dynamic response全部由主循环这个唯一writer串行写入；callback request id按JSON值去重，result必须claim后写入，只有write成功才`ResponseWritten`，partial/unknown write直接终止attempt且不得重调MCP。runner对notification使用非阻塞有界sink，缓冲满、未知server request、MCP失败或caller cancel都会cancel turn callbacks、发唯一`turn/interrupt`并在固定grace内等待匹配terminal；`thread/resume`在第一字节stdio I/O前核对checkpoint catalog digest且绝不发送`dynamicTools` override。net.Pipe wire fixture与race测试已覆盖上述路径，stock app-server live gate也已在macOS arm64 stable binary SHA-256 `ae1d3ffe6d48aec6a4dc3f50e7eb8e0d11962485a6a9406c5a7012139383da02`、size `271056976`上通过。新的非live组合测试已用official SDK和真实HTTP session贯通`AppServerRunner → DynamicBridge → MCPClient → gateway`，并拒绝bearer出现在任何app-server wire frame。强制断开in-flight MCP HTTP后，runner会interrupt、收到匹配terminal并清空callback；pending elicitation门禁还会在decision context确认由transport取消后故意返回迟到`accept`，结果仍是一次call、零dispatch。`MCPClient.Close`现跟踪所有HTTP request，先等有界graceful grace，超时后abort私有transport并有界返回forced-close error，不再卡死于SDK的session DELETE。健康连接上的外层取消仍会在approval expiry前抵达gateway并退出嵌套elicitation handler；已断transport无法把cancel送到已dispatch的远端handler，所以新增纵向测试继续使用真实MCP handler、`ShellExecutor`和agentx WebSocket：在`process/start`确实被peer收到、Core operation已为`dispatching`后断开，恢复窗口内不提前terminal或ACK；提前触发已排定的生产expiry callback后，start/timeout/execution分别收为`unknown/skipped/unknown`，连接holder被fence，MCP返回unknown且没有重发。真实control mTLS的pending-approval journal/replay门禁也已独立完成。提交`cc02131`的四条精确gate已在OCI index digest `24c44fe44872962a828df84d3ff67ae2d541e076fad1e4101f9dc0dca5d8bf21`的pinned Linux arm64容器中各连续通过20次，A07与Phase 4的transport-fault复核据此关闭。

A08 已在 alpha.14 与 stable 0.146.0 上证明“typed outstanding set 已为空后立即关闭 stdin”能让 app-server 有界零退出且文件树字节稳定，不需要固定 sleep。旧用例没有正在转接的dynamic/MCP call，所以只关闭进程退出与稳定快照子结论；修订后的A08还要组合A07 typed cleanup与execution/process收口。两个release在clean exit后仍保留state/goals/logs/memories的`.sqlite-wal/.sqlite-shm`，因此A08不判断checkpoint文件集合。

A09旧direct-MCP用例已证明每个brain thread只需一个rollout JSONL、cold `thread/resume`的RPC response就是恢复屏障、tool result可恢复且副作用不重放，这些checkpoint事实保留。修订A09的worker-runner gate固定为stable 0.146.0：首attempt通过dynamic bridge执行一次worker-owned callback，只checkpoint app-server返回的rollout JSONL；移走源`CODEX_HOME`后，新attempt在不传`dynamicTools` override的情况下cold resume，并从第二turn的真实模型请求校验两轮用户输入、原dynamic call ID/result与完整tool schema都被保留，两attempt的executor副作用总计恰好一次。catalog digest不匹配在任何stdio I/O前fail closed已由runner单测覆盖；变更catalog必须创建新thread。`state_5.sqlite`、全部WAL/SHM、goals/logs/memories DB与config仍不进入checkpoint。新live gate已在macOS arm64 stable binary SHA-256 `ae1d3ffe6d48aec6a4dc3f50e7eb8e0d11962485a6a9406c5a7012139383da02`、size `271056976`上通过，因此A09对该exact artifact关闭；未来build必须重新通过才能复用allowlist。

A10 已在同两个 release 上通过。probe先密封一个 completed-turn rollout，再从其副本启动第二个 app-server。scripted model的 hold-open response让测试无需 sleep就能精确停在“`turn/start` 已接受、模型请求已到达且包含本轮输入、模型尚未响应”的位置，然后 hard-kill child。进程非零退出，Responses request没有重试，独立密封 checkpoint的 mode/size/hash保持不变。测试不把 crash runtime交给 `thread/resume`，而是在第三个全新 `CODEX_HOME` 中只恢复密封 rollout并发起显式新 turn；新 turn ID不同，真实模型上下文包含 crash前 completed历史和新的继续输入，不含被放弃输入。该结论要求 core committed checkpoint pointer和 attempt fencing成为恢复文件来源的权威；不能推断 stock app-server会替系统识别并拒绝任意未提交 rollout。

A11旧用例已证明多类runtime sentinel不进入Responses body、stderr或单-rollout checkpoint，但它没有检查HTTP header，且其MCP bearer由app-server环境读取，该证据不能关闭新边界。修订A11的worker-owned live gate已固定并实跑stable 0.146.0：app-server config和env完全没有executor MCP endpoint/bearer，源worker用旧capability实际认证MCP initialize/list/call并执行一次dynamic副作用；只checkpoint app-server返回的rollout并移走源`CODEX_HOME`后，恢复worker用新capability验证同catalog再cold resume，工具副作用总计恰好一次。gate扫描显式child env、config、有界`CODEX_HOME`全文件、stderr、model request headers/body、rollout和单文件checkpoint，旧/新executor bearer都不得出现；model/llmproxy auth sentinel只允许作为模型transport的exact `Authorization`值，仍不得进入body、rollout或checkpoint，而原dynamic call ID/result和完整tool schema必须恢复。stateful gateway fixture和runner/MCP组合已在普通CI实跑，stock round-trip也已在macOS arm64 stable binary SHA-256 `ae1d3ffe6d48aec6a4dc3f50e7eb8e0d11962485a6a9406c5a7012139383da02`、size `271056976`上通过。修订A12又在exact Linux arm64 image中证明close-all、UID、mount和分UID网络隔离，并扫描真实worker-owned MCP场景的child与rollout。因此A11对该stable artifact set关闭。

A11不允许对 rollout做 lossy脱敏。若 scan发现本不应模型可见的 runtime credential，checkpoint整体 fail closed/quarantine，run进入不可恢复状态；用户输入或 MCP result中已经模型可见的敏感内容则必须依靠前置策略、加密、访问控制、retention和删除处理，不能删除字节后仍称为 native resume。

A12 的 Darwin host-level边界已在 alpha.14和 stable 0.146.0上重复固定。model provider配置 `env_http_headers`主动尝试把 worker mTLS sentinel发往 Responses endpoint；sensitivity control显式注入 child env并观察到 exact header，随后 parent-only case的显式 child环境没有该变量，真实 request header/body与 stderr也没有该值。thread报告的 cwd是 source tree外的空临时目录，turn后仍为空。父 pipe被故意清掉 `CLOEXEC` 后，即使 Go `exec.Cmd.ExtraFiles`为空，writer仍被 stock child继承；配置的 scripted llmproxy返回跨 origin `307` 时，stock Codex也会把同一 Responses POST发送到另一个未配置 origin并完成 turn。这些反例证明显式 env、普通 Go spawn、base URL、requirements exact URL和 bearer audience都不能替代 image isolation。

`conformance/image/a12` 已按修订架构在 official stable 0.146.0 `linux-arm64` 上原生通过 production-profile gate。scratch image只包含 Go init/conformance fixture、锁定的 Codex binary和 mount anchor；rootfs只读，`/tmp`与`/run/agentserver`是自检后的 `nosuid,nodev,noexec` tmpfs，不存在 workspace或 service-account路径。每个场景都由真实 worker UID 65531进程直接监督 app UID 65532 child；worker的 supplementary groups为空，启动窗口中的能力精确只有 `SETUID/GID`，先读取`0700` credential并建立official-SDK executor MCP session，再fork final-exec child并立即all-thread seal为零capability。child在任何文件/proc/socket/network探测前复核real/effective/saved UID/GID，清空ambient/inheritable/permitted/effective capability，设置`no_new_privs`与non-dumpable，证明worker credential/staging/result/control、`/proc`和signal不可达，再由`close_range(3, UINT_MAX, 0)`关闭故意继承的non-`CLOEXEC` trap后exec绝对stock Codex。sealed worker作为唯一stdio owner运行`AppServerRunner → DynamicBridge → MCPClient`，排空事件、关闭stdin并等待child，而不是旁路sentinel。受信init的`DAC_READ_SEARCH`不下放给任一runtime UID，只在child退出后读取app-owned rollout做secret scan。

修订image的`meta skuid`规则让worker IPv4只到harness-pool fixture与executor-gateway MCP，app IPv4只到一个exact llmproxy tuple，两个UID IPv6全drop。正常场景由worker真实认证initialize/list/call，dynamic result进入下一模型request和rollout；direct model与llmproxy cross-origin redirect都在sink零命中的情况下失败。app对MCP/worker endpoint、worker对llmproxy/direct/redirect、两个UID对DNS-shaped UDP/IPv6均零命中，root sensitivity control先证明各sink存活。app config无`mcp_servers`/endpoint/bearer，显式env、stderr、model headers/body和全部scenario rollout也无worker bearer。Codex SHA-256为`cb5e8cb8a333a408ce6adbe0d4fad1845c69772c2216af7c1f88c98a11460dc6`、size为`269098800`；因此A12对该exact `linux-arm64` artifact关闭，`linux-amd64`及真实Kubernetes部署仍需独立门禁。

当前 0.145.0 candidate 的 bootstrap probe 进一步确认：assistant 内容在 `item/completed` 上到达，而 terminal `turn/completed` 是 `itemsView: notLoaded` 的空内容终态，harness-worker 必须持续归并 item 事件，不能只保存 terminal payload。`environments: []` 能去掉 shell/fs；固定本地 model catalog，并显式关闭 Web、goals、multi-agent、orchestrator skills、user-input 及其他已知 feature 后，实际 Responses request 的工具面可收敛到仅剩 `update_plan`。但官方 `rust-v0.145.0` tag（peeled commit `25af12f7e61572b0bc18ddb1008be543b91519b0`）的 `add_core_utility_tools` 无条件注册 `PlanHandler`，该版本没有对应 config 或 requirements 开关；scripted model 调用后实际收到成功的 `Plan updated` result，client 同时收到 `turn/plan/updated`。因此 0.145.0 明确不通过 A03，不能成为 production runtime pin。

官方 `rust-v0.146.0-alpha.14` tag（commit `9d84cad281364eb7f6be75e23067b0adc5e26106`）新增真实的`[tools.update_plan] enabled = false`。它的A01 terminal projection也变为`itemsView: summary`并携带completed agent item；harness-worker仍以归并item事件为内容权威。对该artifact的旧direct-MCP probe验证：无MCP时工具面为空，配置executor MCP后却额外包含三个通用resource handler，且`list_mcp_resources`绕过`enabled_tools`。因此direct-Codex-MCP路径被永久拒绝；修订架构不再等待stock修复该handler。

本次 macOS arm64 candidate binary SHA-256 为 `e4ca03a3f3682647eb5aab2546647ed963354611b42a9daa332ae9d0366a1204`，官方 artifact archive SHA-256 为 `245d877dea7abc520487b5186f9e17d4fb10548f77da9ebf2b02cb3dee137d96`。这些 hash 只绑定本轮 alpha candidate 证据，不是 production runtime manifest。

随后发布的official stable `rust-v0.146.0`（annotated tag object `be449751a978f02e5bbba886999662956c7f38f5`，peeled commit `e363b08c9175ac1cbe5893615dd2cb9ddf95043b`）在direct-MCP路径复现同一失败，但dynamic bridge通过修订A03。测试的macOS arm64 binary SHA-256为`ae1d3ffe6d48aec6a4dc3f50e7eb8e0d11962485a6a9406c5a7012139383da02`，官方npm platform archive SHA-256为`279ec3460c5b8068daab2a4f5bcf057483303b3595f4a24ade6ceb4d02674935`，canonical app-server schema tree SHA-256为`834975f055f4dc0bf25231ab23f446f4bfef63fd3f7832bc9b0c5fe8a32363bb`。它现在是修订门禁的stable candidate，但A06/A07/A08完成前仍不生成production runtime manifest。

official `rust-v0.147.0-alpha.2`（annotated tag object `cff73291c5dd427cb305c8791c89ece30a11c61e`，peeled commit `1d12a16dd9bcbd37bda22a71a1ae8ac2a49f0aba`）接受了定向复验。direct-MCP resource bypass以及stock signal/descendant负向事实都未变化，dynamic bridge则与stable 0.146.0相同通过。测试的macOS arm64 binary SHA-256为`8e9f6e95320ea2360a07e7716cccea1292d67a2ba47d93bc81d601814abe7135`、size为`276983200`，官方npm platform archive SHA-256为`dfe3db5f1f32b19cf1b2875fe9347f970e3750b764a0dba483ee3b43375da4f7`，canonical app-server schema tree SHA-256为`4393de9e38501330e39c433b43af7a58d3e0008e159f464845472ab66a6e7561`。它仍是alpha研究证据，不是production pin。

production-shape probe验证了stock `thread/start.dynamicTools` client bridge。stable 0.146.0与0.147.0-alpha.2在完全不配置Codex MCP server时，实际模型工具面都精确只有`executor.approved_echo`；真实namespaced function call变成带原thread/turn/call id、tool和结构化参数的`item/tool/call`，client回包结果进入下一次模型请求。模型伪造未公布executor tool或`exec_command`都只得到`unsupported call`，不会产生callback。pending dynamic call可由`turn/interrupt`结束，但正常回包和interrupted terminal都不产生`serverRequest/resolved`。A03、A04、A05、A09、A11与A12已据此改门禁并通过；A06/A07/A08按本文列出的新边界继续实施。

### 4.3 checkpoint 探针算法

1. 创建全新CODEX_HOME-A；reference worker校验fake executor MCP catalog，把它机械映射为`thread/start.dynamicTools`，完成一个包含真实dynamic callback → MCP result的非ephemeral turn。
2. 收到`turn/completed`后继续排空stdio；按类型确认dynamic callback已由response写入或所属turn terminal清理，其他需resolved的request已清空，并确认execution/process收口；再关闭stdin并等待child有界正常退出。
3. 读取 app-server thread response返回的绝对 rollout path，将它相对 CODEX_HOME-A规范化；拒绝 symlink、非普通文件、规范化后的绝对/父目录逃逸和不在 CODEX_HOME-A内的路径，并校验完整 JSONL。
4. 为该`brain_thread_id`生成只有一个rollout entry的manifest，记录相对路径、mode、size、SHA-256、Codex build/schema、checkpoint allowlist version与冻结tool catalog digest；manifest不纳入、checkpoint也不复制其他`CODEX_HOME`文件。
5. 按 manifest把 rollout复制到全新 CODEX_HOME-B并复算 hash；在生成新 config前断言 staging严格只有该文件。SQLite主库/WAL/SHM、goals/logs/memories DB、config、requirements、auth、token、log和cache一律不恢复。
6. 改名或移走CODEX_HOME-A，证明旧rollout绝对路径不可访问；worker用新bearer重新读取executor catalog，要求digest与checkpoint相同，再在B生成本attempt新config并启动同一build。
7. 发送`thread/resume { threadId, path: <B中的rollout>, excludeTurns: true }`，不发送`dynamicTools`（schema不支持override）。cold resume不发送`thread/started`；成功RPC response就是生命周期屏障。
8. 发送带`environments: []`的第二个`turn/start`。fake model捕获请求，确认第一、第二turn、原dynamic call id/result和同一tool schema完整可见；fake executor MCP确认旧副作用没有重放。
9. 缺失rollout必须在模型调用前由`thread/resume` fail closed。manifest loader另行覆盖hash/size不符、额外文件、路径逃逸、build/schema/allowlist/catalog digest不匹配；catalog变化走新thread，不得native resume原thread。
10. 将该单 rollout集合固化为与 Codex build digest绑定的 checkpoint allowlist；升级 build必须重跑整套正反例，不能继承结论。

A10 的 crash负向路径固定为：先把上一个 completed checkpoint复制到独立 staging并记录 hash；从副本启动一个新 attempt，在 hold-open model request已到达后 hard-kill app-server；确认 child非零退出、模型请求未重试且独立 staging未变化；删除整个 crash runtime。恢复时只能按 core已经提交的 checkpoint pointer重建第三个全新 `CODEX_HOME`，调用 `thread/resume`后创建不同的新 turn，并从下一次真实模型请求证明被放弃 turn不在历史中。禁止从 crash runtime扫描“看起来更新”的 rollout；A10不依赖 stock Codex替系统判断该文件是否已提交。

A11的secret负向路径固定为：为每类runtime-only来源生成不重叠sentinel；MCP credential必须只由worker读取，并从worker实际MCP request header证明已使用。启动前后检查child env/config/FD/`CODEX_HOME`均无executor bearer，再扫描模型request headers/body、stderr和rollout；model/llmproxy capability只能出现在发往llmproxy的认证header，不得进入模型可见body或持久化状态。manifest/staging只能含单rollout且复算hash；恢复attempt由worker换用新executor capability并重建config，再次扫描模型上下文与新rollout。scan命中不应模型可见的runtime secret时拒绝整个checkpoint，禁止原位改写JSONL。

A12分两层验收且均已对上述artifact set实跑。host probe用`env_http_headers`把worker sentinel变成可观察的exfiltration trap，保留非`CLOEXEC` FD和cross-origin redirect负向characterization。image job使用真实supervisor链：worker/app-server不同UID，worker credential/control/MCP capability/staging对子进程不可读，final-exec先清capability再close-all；child mount view没有workspace或service-account token。网络正向分别验证worker可到pool+executor MCP、app可到llmproxy；app对executor MCP、worker-only endpoint、direct与redirect sink、UDP/DNS和IPv6全部零命中，worker对llmproxy以外未列目标也零命中。这里以live sink counter判定，不把HTTP 401/403误当网络不可达。

如果只有打包整个 CODEX_HOME 才能 resume，Phase 0 失败；不能把敏感配置一并持久化来绕过。

### 4.4 exec-server 必过探针

| 编号 | 探针 | 通过条件 |
|---|---|---|
| E01 | stdio lifecycle | codex exec-server --listen stdio --strict-config；initialize → initialized；wire 省略 jsonrpc |
| E02 | deterministic process | argv[]、cwd URI、env、PTY/pipe、output sequence、exit/close 与 fixture一致 |
| E03 | outer stdin/terminate profile | `process/write`的writeId去重、stdinClosed/unknownProcess与`process/terminate`结果明确；agentx outer schema/capability不公布也不转发`process/signal` |
| E04 | bounded filesystem read | 固定stock `fs/open(sandbox=null) → fs/readBlock → fs/close`形状、block边界与cleanup；证明platform-sandboxed `fs/open`被stock拒绝，远端排除无界`fs/readFile` |
| E05 | network reverse request | network/policyRequest 能被 agentx client allow/deny 并正确阻断 ask；非法参数、未知 reverse method、RPC error、未知 decision、超时和断线默认拒绝 |
| E06 | stdio EOF | stdin EOF 后 server shutdown并清理 managed process；新的 agentx 不能 attach旧 child |
| E07 | dedicated-instance cleanup | 每个受管process独占stdio exec-server instance；`exited`后迟迟不`closed`则关闭该instance并验证process tree回收，不影响其他process；无法确认时为unknown |
| E08 | environment isolation | 空 CODEX_HOME、固定 runtime cwd、清洗 env；不能读取用户 Codex auth/config或 agentx credential |
| E09 | executable/runtime lock | Codex 与所有独立外部 executable digest 不匹配时启动失败；隐藏 fs/arg0/sandbox 模式只重入已验证 Codex；ambient PATH 中的 codex/bwrap/rg 均不可被选择；每个 Linux release/architecture 以 native image 的真实 sandbox 请求证明 bundled bwrap 选择与权限收敛 |
| E10 | bounds | 最大 frame、argv/env、output buffer、retained output 和 exited-process retention 被测量并写入 manifest |

Phase 0的exit criterion是修订后的A01–A12、E01–E10全部可重复通过。dynamic bridge、worker MCP elicitation、checkpoint或dedicated stdio instance假设失败，都先修改架构，不能用旧direct-MCP或共享instance结果冒充通过。

当前 probe 已确认但尚未构成完整 Phase 0 放行的 exec-server 事实：`process/start` response 可与早期 `process/output` 竞态，agentx 必须单消费者收包并按 request id/一基 event seq 整理；带 `maxBytes` 的 `process/read.nextSeq` 只越过本次返回的最后一个 output chunk，不保证同时越过 terminal event，不带该限制的 terminal read 才能给出 `closed` 后游标。E02 已覆盖 argv/arg0、file-URI cwd 到 host canonical path、缺省 `envPolicy` 时 child env 精确等于 request `env`、pipe 与 PTY 合流输出。

E05 用真实 child → executor-local HTTP proxy → bounded origin 链路固定了反向请求的 `{processId, request:{protocol,host,port}}` 形状：allow 恰好命中 origin 一次，deny/ask、RPC error、未知 decision、controller timeout 和 stdio EOF 均为零命中；reference agentx client 还对非法 known params 回复 `deny(not_allowed)`、对未知 reverse method 回复 `-32601`。stock 返回 `ask` 会立即得到 HTTP 403，不会暂停请求；`policyDecisionTimeoutMs` 之外还有固定 5 秒 transport margin，所以 agentx 必须在自己的 approval deadline 主动 allow/deny。

E08 的当前 slice 证明隔离 `CODEX_HOME` 不读取毒化的用户 `~/.codex`，exec-server 自身持有的 sentinel credential 也不会进入缺省策略 child。E09 的 reference launch boundary 已经验证完整 artifact set 全部通过后才调用 starter、Codex 只使用绝对路径、ambient PATH 被替换、Codex/bwrap digest 或布局失败时零启动。上游实现同时确认 fs helper、arg0 exec helper 和 Linux sandbox alias 都重入当前 Codex；不能为它们虚构独立 digest。stock Linux launcher 却明确优先搜索 PATH 中的 system `bwrap`，再使用 bundled resource，因此 agentx profile 采用无 `codex-package.json` 的最小 bundle、不可存在的 PATH 目录和固定 `codex-resources/bwrap`，让 stock 只能落到已验证 bundled resource。

E04在exact stable 0.146.0上进一步固定了filesystem事实：无界`fs/readFile`可返回整个文件，但不能进入远程profile；`fs/open`只有`sandbox=null`时可配合`fs/readBlock`形成请求级上限，携带managed/restricted platform sandbox会明确失败并报告`streaming file reads do not support platform sandboxing`。因此`read_file`不能把远端sandbox直接转发给stock。实现采用组合环境profile `process-v1+filesystem-read-v1`和唯一outer方法`agentx/fs/readFileBlock`，每次请求在一次性fs-only instance内完成`open(null) → 一次readBlock → close → instance cleanup`；`len`最大1 MiB，`offset`最大`2^53-1`。显式insecure-dev继续在stock调用前后复核registered-root canonical path；production Linux已由agentx `066acf6`改为`openat2`固定root/target并只向stock传递只读fd 3，不再让stock解析caller path。同UID insecure-dev仍不声称解决symlink TOCTOU。

stable 0.146.0 的 `linux-arm64` 正向 image gate 已在 native Apple `container` Linux VM 中通过：scratch image 以 uid/gid 65532、只读 root、零 capability、无网络运行，runtime bundle 只有锁定的 `bin/codex` 与 `codex-resources/bwrap`，不存在 package metadata 或 compatibility shim。门禁先确认 bundled bwrap 的 `--argv0` 语义，再通过 verified launch plan 发出真实 read-only 与 workspace-write `process/start`；前者可读 fixture，后者只可写声明的 workspace，同一 writable `/tmp` 上的 sibling path 被拒绝，ambient poison bwrap 未执行，运行时生成的 Linux sandbox alias 解析回同一份已验证 Codex。因此 E09 的这一精确 release/architecture/artifact set 已关闭；`linux-amd64` 仍须在 native amd64 worker 跑同一 target。Apple Silicon 的 amd64 仿真会重写 inner argv0 并拒绝 seccomp filter，不能作为门禁证据。独立agentx `5d40b6b`又关闭最终安装路径的safe-open/exec TOCTOU：production Linux只接受从`/`起完整root-owned且不可group/other写的runtime树，以`openat2`打开并通过同一descriptor校验artifact，launch plan保留Codex inode，cgroup Commit后只执行`/proc/self/fd/N`；stock bwrap路径仍受root-owned树和0.146.0内置digest双重约束。Codex文件替换、runtime root rename+symlink swap与bwrap替换攻击各连续20轮，前两者保持固定inode，后者精确fail closed且poison零执行。stdio EOF 会关闭唯一 connection、shutdown session 并回收 managed child，不能把它描述成可 detach/resume。

E10 已在 alpha.14 与 stable 0.146.0 上固定完整 stock bound matrix：stdio payload 恰好 64 MiB 可接受，增加一个 byte 会断开整条连接；JSON 恰好 262,144 个 value 可接受，第 262,145 个 value 只产生 message-scoped `-32600`，连接仍可继续；retained output 为每进程 1 MiB 并另有 50,000 chunk 上限；stdin dedupe 为每进程 4,096 个 write ID 的 FIFO；`process/closed` 后约 30 秒仍可 read，随后返回 unknown process 且同一 ID 可复用。`ExecParams`、`prepare_exec_request` 和 spawn 路径没有独立 argv/env count 或 byte guard，只有 transport 与宿主 process API 限制，因此本机 `E2BIG` 不是 wire bound。live negative control 也证明 stock 会接受超过产品上限但仍低于宿主上限的 argv/env。

runtime manifest 因而分别保存 `execServerBounds` 与更小的 `agentxLimits`。首版 agentx 必须在转发前拒绝：inner frame 大于 8 MiB、JSON value 多于 65,536、argv 加可选 arg0 多于 256 项或 UTF-8 总计大于 16 KiB、最终物化且不继承的 env 多于 256 项或按 `name=value` 总计大于 16 KiB、write ID 大于 128 bytes；每进程 WSS delivery/resume raw-output buffer 为 8 MiB，溢出必须报告带 sequence range 的 `output_gap/buffer_overflow`。stock 约 1 MiB replay 不能替代该 buffer，也不能恢复已经溢出的外层序列。最坏响应无法装入较小 envelope 的 method，在具有请求级上限或分页协议前不得协商。reference input validator 已覆盖 argv/env/write ID 的每个恰好边界与第一个拒绝；真实 agentx 仍须在 Phase 2 compatibility suite 复用同一 fixture 证明 frame/JSON/input/output 限制执行在写入 child stdin 或耗尽 buffer 之前。

stable 0.146.0固定了两项产品profile输入。第一，`process/signal`对missing、delivered、already-exited都返回不可区分的`{}`，所以修订E03在outer schema中排除该方法，只验已证明的stdin/terminate。第二，root退出但descendant持有pipe时不会`closed`，`process/terminate`也不会杀该descendant，直到整条stdio connection关闭；因此修订E07要求每process独占instance并以connection shutdown做无旁路cleanup。现有负向probe长期保留；新增的reference adapter只有一个reader、重分配local request id、逐process核对ownership，单instance拒绝第二次start，并在转发前拒绝signal、foreign process和超限writeId。fake-wire/race gate覆盖正常closed、forced cleanup、核验失败转unknown及双instance无连带；stock live gate又在exact stable 0.146.0 macOS arm64 binary（SHA-256 `ae1d3ffe6d48aec6a4dc3f50e7eb8e0d11962485a6a9406c5a7012139383da02`、size `271056976`）上同时运行两个真实stdio instance：第一条root crash后只关闭自己的connection并确认descendant消失，第二条仍存活且可独立terminate/closed。E03 outer-profile与E07 reference composition因而对该artifact关闭；真实agentx的production WSS fresh/resume兼容与Linux cgroup v2 containment现已完成，非Linux平台等价边界和全量native bounds仍属于Phase 2。当前A03/A04/A05/A06/A09/A11/A12已按dynamic架构关闭；A07–A08仍开放。E09 `linux-amd64` native gate、真实agentx bounds enforcement、E05 ownership/approval与审计也仍未完成。

## 5. Core 状态内核

### 5.1 数据库所有权

只有 agentserver-core：

- 持有 PostgreSQL credential；
- 执行 migration；
- 分配 run event seq；
- 推进权威状态；
- 签发 run capability；
- 提交 object pointer；
- 判定 stale generation、unknown 和 terminal。

harness-pool、browser-gateway 与 executor-gateway 使用内部 workload identity 调 core。它们可以有短期内存 cache，但不能直接写表。

### 5.2 首批表与关键约束

| 表 | 关键字段/约束 |
|---|---|
| schema_migrations | version PK、name、sha256、applied_at；已应用文件 checksum 变化时拒绝启动 migration |
| workspaces | id、status、version |
| workspace_members | workspace_id + user_id PK、role、version |
| sessions | id、workspace_id、active_run_id、latest_checkpoint_id、version |
| runs | id、session_id、actor_id、status、request_hash、idempotency_key、current_attempt_generation、next_event_seq、version |
| run_launch_states | run_id PK、workspace/session、不可变prompt完整object pointer、executor policy version/context digest；与CreateRun同事务写入 |
| run_launch_allowed_tools | run_id + ordinal PK、tool name；每run最多64项，name在run内唯一，按ordinal保存规范化排序 |
| run_attempts | id、run_id、generation、status、turn_started_at、holder_id、version；unique(run_id,generation) |
| session_leases | session_id PK、run_id、holder_id、generation、expires_at |
| attempt_leases | run_attempt_id PK、holder_id、generation、expires_at |
| run_events | run_id + seq PK、event_id unique、producer key unique、source、schema_version、inline payload或完整object pointer metadata |
| outbox | id、kind、aggregate_id、payload、available_at、lock_owner、lock_until、attempts（claim generation）、completed_at |
| checkpoints | id、workspace/session/run/attempt/generation、brain_tool_catalog_id、thread_id、turn_id、manifest hash、完整object pointer、Codex runtime manifest digest、checkpoint allowlist version；每run至多一项，session.latest_checkpoint_id只能指向同session记录 |
| brain_tool_catalogs | id、workspace/session、创建它的run/attempt/generation/holder与版本、thread_id nullable unique、contract/canonicalizer version、exact canonical catalog bytes、catalog digest、policy version/context digest、version；每attempt最多冻结一份，新thread启动前冻结，成功后CAS绑定thread_id，同一thread不可更新schema |
| executions | id、run/attempt/generation、tool_call_id、executor/env、tool/schema/policy/mapper version、policy decision、operation_count、arguments/tool schema/operation plan/policy context hash、canonicalizer version、status/terminal result hash、version |
| execution_operations | id、execution_id、ordinal、kind、effect_class、mutation_key unique、params hash、canonicalizer version、status、connection generation、ACK/terminal evidence hash、timestamps、version |
| approvals | id、execution_id、context hash、nonce unique、status、expires_at、requester、approver、version |
| executors | id、workspace_id、status、machine key、protocol/build metadata、version |
| executor_environments | id、executor_id、root descriptor、owner policy digest、status、version |
| executor_connection_attempts | connection_id PK、executor_id + generation unique、session_id、gateway_instance_id、build/schema/environment-set digest、不可改写的结束原因；防止旧 connectionId 在更新 generation 后重新取得连接 |
| executor_connections | executor_id PK、generation、connection_id、session_id、gateway_instance_id、`connecting|online|fenced`、expires_at、build/schema/environment-set digest；FK 到对应 attempt |

sessions.active_run_id 通过行锁/CAS实现“一个 session 只有一个 active run”。不要仅依赖一个包含状态字符串的应用侧查询。run idempotency 建唯一约束：

~~~text
(workspace_id, actor_id, session_id, idempotency_key)
~~~

命中同一 key 时必须比较 request_hash；hash 不同返回 idempotency_conflict。

execution 的唯一调用身份为：

~~~text
(run_id, app_server_tool_call_id)
~~~

重复调用只有 arguments、tool schema、mapper/operation plan 和 policy context hash 全部相同时才能返回原 execution。approval context hash也覆盖 operation_plan_hash，防止批准后换成另一组确定性步骤。

`FreezeBrainToolCatalog`只在准备新brain thread时执行：它从版本化executor MCP contract取当前policy允许的tool子集，规范化name/description/inputSchema并保存canonical bytes/digest，初始`thread_id=null`；`thread/start`成功后由`BindBrainThreadCatalog`以CAS绑定返回的thread id。恢复已有thread只能读取该记录，不能因gateway升级或RBAC变化重写schema；RBAC收紧由每次`tools/call`实时拒绝。需要改变模型可见catalog时显式创建新thread。

所有幂等/审批 hash先按对应 JSON Schema验证，再使用 RFC 8785 JSON Canonicalization Scheme生成字节，最后用带 domain separator的 SHA-256计算。不能直接依赖 Go map遍历、普通 json.Marshal偶然顺序或拼接字符串。hash记录 canonicalizer version；升级 canonicalizer必须走兼容迁移，不能让已有 idempotency key失效。

### 5.3 必须原子的 domain command

首批 store/service API 不是 CRUD，而是以下带 expected version 的命令：

- CreateRun
- ClaimQueuedRun
- RenewSessionLease
- RenewAttemptLease
- MarkTurnAccepted
- AppendAttemptEvents
- FreezeBrainToolCatalog
- BindBrainThreadCatalog
- PrepareExecution
- PrepareOperation
- CreateApproval
- DecideApproval
- ConsumeApprovalAndAuthorizeExecution
- BeginOperationDispatch
- AcknowledgeOperation
- CompleteOperation
- SkipOperation
- CompleteExecution
- BeginRunFinalization
- CommitCheckpointAndTerminalRun
- InterruptAttempt
- AbandonAttempt
- CancelRun
- AcquireExecutorConnection
- RenewExecutorConnection
- ActivateExecutorConnection
- FenceExecutorConnection

每个命令以单个 PostgreSQL transaction完成其全部权威写入。run aggregate命令必须在同一事务完成状态、规范事件与outbox，禁止handler先写状态、提交后再“顺便”写事件。

executor connection lifecycle不是run aggregate，不能为心跳伪造run event。它的原子边界是`executor_connection_attempts + executor_connections + executor/environment在线投影`：fresh acquire只进入`connecting`并让旧generation离线，完成远端`initialize → initialized`后`ActivateExecutorConnection`才进入`online`；renew只能延长exact live holder，fence同时关闭attempt并离线投影。connection attempt本身是不可复用的连接审计事实，后续结构化安全审计可以消费它，但不能拆成另一个先后提交的写入。

PR 9 reference command store固定以下首批语义：

- `CreateRun`先锁session，按`(workspace_id, actor_id, session_id, idempotency_key)`查重；相同request hash还必须逐字段匹配prompt object pointer及规范化后的executor policy/allowlist才能返回原run，任一不同均返回`idempotency_conflict`。只有没有active run且expected session version匹配时，才在同一事务写run、不可变launch authority、`run.queued` event/outbox并设置`active_run_id`，不能先入队再补prompt或policy。
- `ClaimQueuedRun`把`queued`推进为`starting`并创建attempt、session lease和attempt lease。两条lease都使用数据库时钟；任一仍存活时不能换holder。只有尚未接受turn、attempt仍为`leased|starting`且两条lease都过期时，才可fence旧attempt并以更高generation重领；mid-turn禁止自动重领。
- `RenewSessionLease`与`RenewAttemptLease`都要求另一条同holder/generation lease仍存活，避免只续住半条lease后无限阻塞reclaim。
- `CancelRun`先在同一事务重新校验workspace membership、锁run/session并写规范事件与outbox。尚无attempt的`queued` run直接进入`cancelled`并清空`active_run_id`；已有holder的`starting|running|finalizing` run只进入`cancelling`，不能在调用线程里假装远端workload已经停止。terminal run和已经进入`cancelling`的exact retry均为零写入。
- harness-pool的成对lease heartbeat同时返回当前run/attempt authority。观察到`cancelling`后，它只取消驱动turn/MCP的attempt context；worker control context必须继续到`turn_terminal(interrupted)`累计ACK，pool侧`AttemptLifecycle` command context必须绑定仍在续租的holder lifecycle并继续到supervisor/workload cleanup完成，不能随attempt context一起取消。这样terminal前已经排队的notification/progress仍可跨`AppendAttemptEvents`边界，且cleanup期间不会被另一holder误领。已接受turn通常必须取得stock `turn/completed(interrupted)`并清空typed callbacks；若attempt已在`finalizing`，则沿已经确认的terminal cleanup边界收口。只有exact live holder证明所有execution terminal/unknown且workload停止后，`InterruptAttempt`才在一个事务中把attempt置为`interrupted`、run置为`cancelled`、清session active run、删除双lease并写`run.cancelled` event/outbox。
- pre-turn启动失败不能用“观察一次run状态，再释放dispatch/等待lease TTL”的两步协议。workload完全停止且双lease仍live时，exact holder调用`AbandonAttempt`：若run仍为`starting`，同事务执行`attempt → failed`、`run → queued`、删除双lease并写`attempt.abandoned`，原dispatch随后release即可立即重试；若`CancelRun`已先把run推进为`cancelling`，同一命令改为`attempt → interrupted`、`run → cancelled`、清session active run、删除双lease并写`run.cancelled`。若abandon先提交，随后取消面对的是无holder的`queued` run并直接终止，因此两种提交顺序都不会留下永久`cancelling`。
- `MarkTurnAccepted`是不可逆的mid-turn边界：原子推进run/attempt为`running`、记录`turn_started_at`并写event/outbox。提交后的同holder重试返回原结果；不同holder或旧generation失败。
- `AppendAttemptEvents`一次最多256项、inline JSON object最多64 KiB；同一batch只允许一个producer且producer seq严格递增。新事件要求live双lease和当前generation；exact producer-key重试即使随后被fence也只返回原run seq，不写新行，而同key不同内容返回`event_conflict`。
- outbox一次最多claim 100项，使用`FOR UPDATE SKIP LOCKED`。每次claim增加`attempts`，consumer必须携带`owner + attempts`完成或释放；旧claim即使owner字符串复用也不能完成新generation的工作。

PR 10 reference command store继续固定以下执行语义：

- `PrepareExecution`先锁run/attempt，以`(run_id, app_server_tool_call_id)`查重；相同调用只有attempt generation、executor/env、tool/schema/mapper/policy version、policy decision、operation count及四类domain-separated hash全部一致才返回原execution。`execution_id`不是第二个幂等键，同一tool call并发提交不同候选ID时只保留一个权威execution。
- execution预先冻结`operation_count`。`PrepareOperation`只允许在execution尚未dispatch时写入，ordinal必须落在`1..operation_count`，`(execution_id, ordinal)`与全局`mutation_key`分别唯一；第一个operation跨dispatch边界前，数据库中必须已经存在完整的冻结operation集合。
- `BeginOperationDispatch`首次以CAS把operation推进到`dispatching`并提交event/outbox后，只有该次调用返回`Began=true`，它是唯一一次外部发送许可。transaction wrapper在`Commit`返回任何错误时强制丢弃已计算结果并返回零值，不能把内存中的`Began=true`泄露给调用方；commit后响应丢失时，exact retry只返回既有状态和`Began=false`。调用方必须查询可信journal或收口为`unknown`，不得重发。
- `AcknowledgeOperation`冻结agentx ACK evidence hash并把execution推进到`running`。未ACK的`dispatching` operation只能通过`CompleteOperation(... unknown)`收口；ACK后才允许`succeeded|failed|cancelled|unknown`。connection generation不同的迟到证据被fence。
- `SkipOperation`不伪装成dispatch后的完成证据。它只允许live run holder把仍为`prepared`、位于冻结计划末尾的`timeout_terminate`推进到`skipped`，且execution必须已跨dispatch边界、所有前序operation均已terminal。该转换保持`connection_generation/dispatched_at/ACK`为空，冻结独立result hash并写`operation.skipped` event/outbox；exact retry只接受相同result hash。deadline timer与skip竞态时，若skip先提交，迟到的`BeginOperationDispatch`只能观察`skipped + Began=false`，不能取得发送许可。
- `CompleteExecution`要求冻结计划中的每个operation都已terminal，再按状态聚合：任一`unknown`优先得到execution `unknown`，其次为`failed`、`cancelled`；`skipped`是中性结果，至少一个已dispatch operation成功且其余均为`succeeded|skipped`时才能得到`succeeded`。任何残留`prepared|dispatching|acknowledged`都拒绝terminal，不能用前序失败或取消隐式吞掉未发送步骤。
- 每个命令与对应canonical run event、outbox在同一transaction内提交。outbox唯一键等事务尾部故障会回滚operation、execution version和run event sequence；terminal exact retry只接受相同status与evidence hash。

所有execution hash均通过`ValidateAndHashCanonicalJSON`这一构造边界：拒绝重复JSON key与超限输入，先调用版本化schema validator，再做RFC 8785 JCS，最后以`agentserver-v2/<domain>/rfc8785-v1\0`作为domain separator计算SHA-256。Go command类型不接受裸`[32]byte`替代这些typed hash，数据库同时记录并约束canonicalizer version。

0001已经发布到migration catalog，不能为状态命名修订其checksum。0002因而以前向migration把临时的run状态`claimed`改为架构定义的`starting`，并加入event source与object size/media type。PR 8期间尚无产品runtime可合法写event；如果数据库里存在手工插入的旧run_events，0002明确失败并保持version 1，不猜测其source或伪造object metadata。

### 5.4 operation 是真实副作用边界

一次 MCP execution 可能包含多个 operation：

| MCP 工具 | operation 示例 |
|---|---|
| shell | process_start；必要时 timeout_terminate |
| write_stdin | process_write |
| terminate | process_terminate |
| read_file | fs_read（effect_class=read；Phase 1仍禁止自动重发） |
| apply_patch | fs_read；fs_write_if_match |

每个 operation 都有独立 mutation_key。发送前顺序固定：

1. PrepareOperation 写入 prepared。
2. gateway 校验 live lease、generation、RBAC、approval context。
3. BeginOperationDispatch 以 CAS 写入 dispatching。
4. transaction commit。
5. 才能向 agentx 发送。
6. agentx mutation journal 返回 accepted/pending/completed。
7. core 写 acknowledged/terminal。

预分配但条件未发生的可选步骤不走上述发送链。当前只允许尾部`timeout_terminate`在前序process operation取得terminal后通过`SkipOperation`从`prepared`直接进入`skipped`；它从未获得外部发送许可，也不能带connection generation或伪造ACK。

gateway 在步骤 4 后、步骤 6 前崩溃时，operation 默认 unknown。只有 agentx journal 或 stock child 的可信 terminal event 能把它收口；不能因为“没看到 ACK”就重发。

`BeginOperationDispatch`返回的`Began`不是“当前状态是否为dispatching”，而是“本次transaction是否首次跨过边界”。只有`Began=true`的正常返回允许步骤5；commit结果不明或任何`Began=false`都不允许发送。read operation也使用同一边界，`effect_class=read`只为未来显式安全重试策略分类，Phase 1不据此偷偷重放。

### 5.5 event 与 outbox

AppendAttemptEvents：

- 验证 attempt lease 与 generation；
- 以 producer_instance_id + producer_seq 去重；
- 在 run 行内一次分配所需 seq block；
- 小 payload 原事务写入；
- 大 payload 只接受已经 hash 校验的临时 object id；
- 返回每个 producer key 对应的权威 run seq。

outbox claim 使用 SKIP LOCKED 与 lock_until。consumer crash 后可以再次 claim outbox，但消费动作仍必须依赖 aggregate CAS 保证幂等。PostgreSQL NOTIFY 只提示“可能有工作”，丢通知后 poll 仍能继续。

`attempts`同时是outbox claim generation。claim、complete与release都以数据库时钟判断`lock_until`；complete/release要求exact owner和generation，过期claim在另一consumer重领后只能得到`outbox_claim_lost`。已经完成的row再次complete是无副作用成功，不重新投递。

outbox row是单消费者delivery，不是广播订阅。每类产品consumer必须通过core暴露的kind-scoped领域接口领取，不能直接使用无过滤claim：`run.queued`专属于harness scheduler lane，其他canonical event notification保留给event relay。浏览器的首个`RUN_STARTED`来自CreateRun响应，重连事实来自run event cursor；不能为了广播同一outbox row让pool与relay竞态消费。未来若同一事实确实需要多个异步subscriber，必须显式写多条destination delivery或引入subscription表，不能让多个consumer共享一个`completed_at`。

这里的core所有权只覆盖canonical run event ledger：harness、executor-gateway和用户命令产生候选事件，core负责校验lease/generation、去重、分配权威run seq，并在需要时与aggregate状态和outbox原子提交。debug log、metric、trace和不参与恢复的内部notification不进入run_events。大stdout、长模型内容或大MCP结果先写对象存储，core只提交已验证hash的pointer；高频canonical event必须批量append，瞬时progress可以合并或限频。

事件投递不是core的权威写职责。browser-gateway或可独立扩缩容的relay负责缓存、SSE fan-out和下游投影；它们只能消费已经提交的event/outbox，不能自行签发run seq或修正历史。Phase 1先保留清晰的event domain/API边界，不新增第二个权威event service，避免为state + event引入分布式事务。

### 5.6 migration

v2/cmd/agentserver-core migrate：

1. 获取固定 advisory lock。
2. 创建/读取 schema_migrations。
3. 对已应用 migration 比较内嵌 SHA-256。
4. 每个新 SQL 文件使用独立 transaction执行。
5. 写入 version/name/hash。
6. 失败即停止，不执行 down migration。

Helm 先运行 migration Job，成功后再 rollout服务。core runtime identity没有 DDL 权限时更佳。

当前PR 8 reference slice已实现独立`agentserver_v2` schema、内嵌连续catalog、已应用name/SHA-256校验、未知新版本降级拒绝、固定advisory lock、每文件独立transaction以及`agentserver-core migrate`。首个migration创建workspaces、sessions、runs、run_attempts、session_leases、attempt_leases、run_events和outbox；真实PostgreSQL 17.6 Linux arm64门禁已覆盖重复运行、两个并发runner只应用一次、checksum篡改拒绝、失败文件完整rollback及关键FK/unique/check/index。普通单测不静默依赖本机数据库；CI以`AGENTSERVER_V2_TEST_DATABASE_URL`显式执行`make postgres-test`。

## 6. 契约与 API

### 6.1 契约源

- api/openapi/public.yaml：浏览器可调用资源 API。
- api/openapi/internal.yaml：组件间 command API。
- api/asyncapi/canonical-events.yaml：SSE/AG-UI 事实流。
- api/asyncapi/harness-control.yaml：pool ↔ worker。
- api/asyncapi/agentx-wss.yaml：gateway ↔ agentx。
- api/schema/executor-mcp.schema.json：模型可见工具 schema。

OpenAPI 生成 Go strict server/client；前端从同一 public spec生成 TypeScript。AsyncAPI/JSON Schema 用于 fixture validation与文档，Go 结构体必须通过 round-trip/golden test证明一致。generation、lease、hash、sequence monotonicity等语义由手写 validator补充。

### 6.2 最小 public API

~~~text
POST /v2/workspaces/{workspaceId}/sessions
POST /v2/workspaces/{workspaceId}/sessions/{sessionId}/runs
GET  /v2/workspaces/{workspaceId}/runs/{runId}
POST /v2/workspaces/{workspaceId}/runs/{runId}:cancel
GET  /v2/workspaces/{workspaceId}/runs/{runId}/events?after={cursor}
POST /v2/workspaces/{workspaceId}/approvals/{approvalId}:decide

POST /v2/workspaces/{workspaceId}/executors
POST /v2/workspaces/{workspaceId}/executors/{executorId}:enrollmentToken
GET  /v2/workspaces/{workspaceId}/executors
~~~

run create 必须要求 Idempotency-Key。事件接口返回 next_cursor；过期返回 cursor_expired + 授权后的 snapshot/rebase_cursor。

### 6.3 最小 internal API

~~~text
POST /internal/v2/run-attempts:claim
POST /internal/v2/run-attempts/{attemptId}:renew
POST /internal/v2/run-attempts/{attemptId}:turnAccepted
POST /internal/v2/run-attempts/{attemptId}/events:append
POST /internal/v2/run-attempts/{attemptId}:beginFinalization
POST /internal/v2/run-attempts/{attemptId}:commitCheckpoint
POST /internal/v2/run-attempts/{attemptId}:interrupt
POST /internal/v2/run-attempts/{attemptId}:abandon
GET  /internal/v2/run-attempts/{attemptId}/toolCatalog

POST /internal/v2/run-dispatches:claim
POST /internal/v2/run-dispatches/{dispatchId}:complete
POST /internal/v2/run-dispatches/{dispatchId}:release

POST /internal/v2/executions:prepare
POST /internal/v2/executions/{executionId}/operations:prepare
POST /internal/v2/operations/{operationId}:beginDispatch
POST /internal/v2/operations/{operationId}:ack
POST /internal/v2/operations/{operationId}:complete

POST /internal/v2/capabilities:issue
POST /internal/v2/capabilities:introspect
POST /internal/v2/executor-connections:acquire
POST /internal/v2/executor-connections/{executorId}:renew
POST /internal/v2/executor-connections/{executorId}:activate
POST /internal/v2/executor-connections/{executorId}:fence
~~~

所有 attempt-scoped 请求携带 run_attempt_id、generation 和 expected_version。stale_generation、lease_expired、version_conflict、idempotency_conflict、dispatch_ambiguous 是稳定错误码，不能压成 500。

## 7. Executor 垂直切片

### 7.1 gateway 内部结构

executor-gateway Phase 1 一个进程包含：

- MCP HTTP server；
- policy evaluator；
- core command client；
- 单副本 agentx connection registry；
- WSS lifecycle/sessionSeq/ACK；
- stock exec-server RPC codec；
- output/progress assembler；
- approval/elicitation coordinator；
- structured audit emitter。

registry 只保存活连接。权威 executor/env/generation 在 core。gateway重启后旧session的完整resume cursor一律拒绝，不能仅凭DB session id伪造frame journal仍存在；prepared operation保持未发送，dispatching/acknowledged且无可信终态的operation才进入unknown。

当前PR 12第一段已经实现这个边界：`0004_executor_connection_kernel.sql`保存enrollment/build、environment、不可复用connection attempt和当前holder；core internal OpenAPI + mTLS handler提供acquire/renew/activate/fence；gateway把reference registry接入真实WebSocket，执行`hello → welcome → initialize → initialized → activate`，并以ping + core renew维持lease。同进程断线会按双向cursor重放；新fresh generation会关闭旧socket；新gateway进程即使看见DB holder也没有journal，必须拒绝resume。真实socket和PostgreSQL 17.6并发门禁已经覆盖这些语义。

第二段已经加入有界的process request table和gateway-originated `process/start` dispatch入口：发送前按session generation、完整routing context、canonical request id与process id登记；匹配RPC response与严格连续的`process/output → process/exited → process/closed`证据分别进入有界通道；同进程transport resume保留原call，fresh generation、resume expiry、terminal protocol failure和gateway shutdown则统一失败。WSS累计ACK仍只释放frame journal，不能被当成operation ACK；`DispatchProcess`在frame进入session journal后发生write错误会明确返回ambiguous，调用方无权重发。

第三段已经接通只读environment registry和首个MCP handler。core新增`executor-environments:list`内部查询，只返回workspace匹配、executor/environment/connection均为online且按数据库时钟lease仍有效的最多256项；gateway再严格解析versioned local root descriptor，只向模型投影environment-relative `default_cwd`，不暴露host root，并拒绝超过512 KiB的聚合投影而不静默截断。`/mcp`使用official Go SDK的stateful Streamable HTTP，显式拒绝除`2025-11-25`以外的initialize；input/output schema直接取自`mcpcontract`。每次HTTP请求都重新鉴权，MCP session还绑定不可变capability identity，另一枚bearer即使知道session id也不能接管。当前loopback insecure-dev入口除workspace/executor与至少32字节的bearer外，还显式要求run ID、attempt ID/generation、holder、run/attempt expected version与冻结catalog digest；它不再伪造隐式全局run，但仍不是生产run capability。

第四段已经把PR 10的execution/operation kernel暴露为七个mTLS internal command：`PrepareExecution`、`PrepareOperation`、`BeginOperationDispatch`、`AcknowledgeOperation`、`CompleteOperation`、`SkipOperation`和`CompleteExecution`。请求必须携带原始JSON value，不能提交裸digest；core会拒绝重复key/超限输入，解析受支持的tool input schema并用它重新验证arguments，再按固定domain + RFC 8785计算所有hash。operation plan、policy context、params、ACK和result当前至少受canonical JSON object边界约束，shell mapper仍须按其versioned contract生成具体结构。path/body execution与operation identity必须一致；gateway client还会核对返回digest domain/canonicalizer，并且只有`Began=true`同时返回同一connection generation的`dispatching` operation时才接受一次性发送许可。`0005_optional_operation_skip.sql`为未触发的尾部timeout增加非dispatch终态，数据库约束禁止其他operation kind伪装为`skipped`；stable冲突响应保留`code/currentVersion/currentGeneration`，不靠错误字符串做恢复决策。

第五段已经闭合timeout的底层传输门禁：`DispatchProcess`现在要求调用方提供expected connection generation，并支持仅供`process/start`使用的outer directive与同一process owner下的`process/terminate`独立response correlation。call table把`agentx/timeoutDue`与start时预分配的timeout routing context严格关联，不把它混入带`seq`的stock process event。`ProcessTimeoutCoordinator`以同一select合并gateway deadline与可信agentx signal，随后只走一次core `BeginOperationDispatch`；只有返回`Began=true`且execution/operation/mutation/generation全部匹配才构造并发送stock `process/terminate`，core不可用、`Began=false`或process先terminal均不发送。agentx v2侧同时实现了必填canonical `--workspace-root`的本地URI重解码/symlink复核与monotonic timer；directive不会进入stock params。

第六段已经闭合开发模式的terminal-only shell垂直切片。MCP principal绑定不可变run/attempt/generation/holder/expected version/catalog digest，`tools/call`还必须逐字段匹配worker提供的run/call/generation/progress token；进程级并发安全allocator为每个core transition生成单调producer sequence及独立event/outbox ID。`shell-v1`从原始arguments确定性生成clean env、environment-relative file URI、`special:minimal(read) + registered-root(write)`的managed/restricted sandbox、两项冻结operation plan与outer timeout directive；`special:root`及其他special path在gateway outer contract和agentx本地policy两层均被拒绝。exact stock 0.146.0 macOS live gate证明minimal profile可在clean env下执行绝对系统命令，workspace-only path负向探测则证明它缺少必需的platform runtime。orchestrator先持久化execution和全部operation，再按core返回version推进start/timeout各自的Begin、RPC ACK、terminal或Skip；start/terminate发送歧义只收口为unknown且不重发，terminate response本身不冒充进程终态，只有真实`exited → closed`才令output complete。MCP handler完成后，实际`serve --insecure-dev` endpoint才同时广告`list_environments|shell`。

第七段先冻结read-file的跨组件contract。core和内部environment projection接受`process-v1`或`process-v1+filesystem-read-v1`，旧process-only enrollment不被破坏；hello按environment声明组合profile，而远端session lifecycle仍只协商基础`process-v1`。agentx WSS schema只新增有界的`agentx/fs/readFileBlock(path, offset, len)`，静态wire validator继续拒绝stock `fs/readFile`。实际dispatch还必须用目标environment的profile做第二次gate，不能因为连接上另一个environment支持filesystem就越权发送。

第八段已经闭合开发模式的read-file垂直切片。`executor-mcp/1.1`新增严格schema和确定性mapper：caller path必须是无dot segment、无反斜杠和父目录逃逸的registered-root-relative path，offset上限为`2^53-1`，limit默认为且最大为1 MiB；每次分配execution/operation/mutation/RPC四个独立identity，并冻结单一`fs_read/read`计划。orchestrator依次执行`PrepareExecution → PrepareOperation → BeginOperationDispatch`，随后由独立filesystem unary table按holder/session、request id和完整routing关联response；发送前同时检查core environment projection和当前hello中目标environment的组合profile。matching RPC error在紧凑ACK后确定为failed；pre-send、ambiguous write、malformed response、断线或fence不重发并收口为unknown。成功response必须精确为`{chunk,eof}`与canonical base64，core只保留response/content hash、字节数和eof，实际内容只走MCP result。有效UTF-8在最终JSON不超过2 MiB时按文本返回，否则使用canonical base64；harness result/text默认上限同步提升到2 MiB并覆盖1,398,104字节最大base64 block。实际`serve --insecure-dev`只有在完整shell与read-file executor都构造成功后才广告`list_environments|shell|read_file`。

这仍不是完整的生产executor交付，但机器身份链已不再局限于gateway。Core双钥enrollment/Hydra OAuth authority、run-capability签发与在线撤销已经实现；`executor-gateway serve`默认进入production，要求TLS/mTLS、SPIFFE、自身唯一executor ID、production公钥keyring，并对MCP逐请求执行本地验签与Core live-authorize。它还提供deployment-bound enrollment、进程内nonce challenge与Ed25519 WSS proof；显式`serve --insecure-dev`才使用开发authority。独立agentx的`548619a`生产切片已经接入owner-only双钥、崩溃可恢复的enrollment、detached release trust、RFC 7523 token cache/refresh、每次物理fresh/resume连接的新challenge proof和connector/runner密钥隔离；`066acf6`关闭production filesystem safe-open，`b4fb3eb`关闭Linux cgroup v2 process-tree containment，`5d40b6b`关闭production Linux runtime executable safe-open/execute。剩余缺口是gateway崩溃后的`dispatching|acknowledged`恢复审计及生产部署manifest，因此仍不能据此声称Phase 2生产交付完成。

### 7.2 第一批工具

按以下顺序实现并逐 catalog version 广告：

1. `executor-mcp/1.0`：list_environments只读 core registry，不连接 agentx；shell使用固定 argv[]、env-relative cwd、固定clean-env mapper、timeout和tty，并在返回前取得terminal evidence。
2. `executor-mcp/1.1` read_file：只在注入完整handler且目标environment声明`process-v1+filesystem-read-v1`时广告/执行；验证path/root后映射为单个`fs_read(effect_class=read)` operation，再发送`agentx/fs/readFileBlock`。每次最多读取1 MiB，远端永不使用无界`fs/readFile`，也不暴露stock handle。
3. 后续版本才把unified_exec与read_output、write_stdin、terminate作为同一handle/ownership切片加入；不能先暴露三个没有handle来源的控制工具。
4. apply_patch：只有 fsWriteFileIfMatch 扩展和跨平台 CAS 测试通过后才暴露。

unified_exec、跨 run detached process、任意 http/request 和 capabilityRoots不进入第一个shell slice；其中unified_exec未来也只允许run-scoped process，不因此开放跨run detached语义。

实现期间`tools/list`遵循“只广告已有handler”：未注入shell/read-file executor的测试或只读endpoint只返回`list_environments`，只注入其中一个时也不得广告另一个；实际`serve --insecure-dev`在完整shell与bounded read链同时装配成功后返回`executor-mcp/1.1`的三工具catalog。core冻结的catalog digest必须与run capability及worker每次调用的metadata一致，不能因gateway升级静默改变已有thread。

### 7.3 MCP 工具映射

#### 7.3.1 shell

~~~text
MCP shell
  → PrepareExecution
  → policy allow/ask/deny
  → PrepareOperation(process_start)
  → PrepareOperation(timeout_terminate)
  → BeginOperationDispatch
  → WSS rpc process/start
  → agentx mutation journal accepted / matching RPC evidence
  → operation acknowledged
  → process/output / process/exited / process/closed
  → operation terminal
  → SkipOperation(timeout_terminate, process_terminal_before_deadline)
  → execution terminal
  → MCP result
~~~

timeout 不伪装成 process/start 参数。gateway 在启动进程前同时预分配 timeout_terminate operation/mutation key，通过outer `directives.processTimeout={afterMs,operationId,mutationKey}`把计时策略交给 agentx；agentx不得把directive复制进stock params。本地monotonic timer到期后，agentx以timeout operation的routing context发送有序`agentx/timeoutDue(processId)`，它与gateway timer汇入同一orchestrator路径，但本身不授权副作用。gateway仍须先调用core `BeginOperationDispatch`，只有首次提交返回`Began=true`才能发送stock `process/terminate`；连接或core不可用时agentx不能持凭证越权直发，只能依赖同gateway进程resume journal，最终按unknown与runner cleanup收口。任何一侧都必须等待真实 process terminal。若process在deadline前取得terminal，gateway必须以`SkipOperation`明确关闭尚未dispatch的timeout operation；不能让它停留在`prepared`，也不能把它伪装成`succeeded|cancelled`。

这里的agentx ACK不是WSS累计`ack`字段。WSS ACK只证明某个传输frame已连续处理并允许释放内存journal；core `AcknowledgeOperation`需要相同mutation key、operation id和connection generation下的agentx journal接受证据、匹配RPC response或可信terminal evidence。

#### 7.3.2 read_file

~~~text
MCP read_file
  → Resolve environment + core profile gate
  → PrepareExecution
  → PrepareOperation(fs_read, effect_class=read)
  → BeginOperationDispatch
  → current WSS hello profile gate
  → WSS rpc agentx/fs/readFileBlock
  → matching unary RPC response/error
  → compact response-hash AcknowledgeOperation
  → compact operation/execution terminal evidence
  → UTF-8 or canonical-base64 MCP result
~~~

一个调用只有一个operation和一个mutation key。gateway绝不把agentx内部的`fs/open/readBlock/close`拆成三个core operation，也不把stock handle暴露给MCP；组合细节完全留在一次性fs-only lane内。caller path只以根目录相对形式进入MCP，gateway生成`file:` URI，agentx仍按本地registered root重新授权并在stock close后复核file identity。

filesystem call table不复用process event assembler：它只接受一个与holder/session、canonical request id和完整routing context同时匹配的response或error。RPC error是确定响应，可以ACK后把operation/execution置为failed；畸形result、连接丢失、fresh generation、shutdown和frame入journal后的ambiguous write都置为unknown。虽然effect class是read，当前版本没有任何自动retry；未来要启用也必须先有明确的reconciliation与版本化策略。

1 MiB原始内容的canonical base64最多1,398,104字节，不能原样写入core的1 MiB canonical JSON边界。core ACK只包含response kind/request id/response SHA-256/response byte count；terminal evidence只包含content SHA-256、bytesRead、eof和状态。MCP result才携带内容：有效UTF-8且marshal后不超过2 MiB时返回文本，否则返回canonical base64；harness worker用2 MiB result和result-text边界接收，仍保留单次read的1 MiB decoded上限。

### 7.4 agentx 启动顺序

1. 读取并先验证 detached signature，再解析 runtime manifest；解析当前平台允许的 Codex 和独立外部 executable。
2. 校验完整 artifact set 的 version、digest、大小、可执行权限、regular-file 和无 symlink 路径；任何一个失败都在创建 child 前退出。隐藏 fs/arg0/sandbox 模式不作为伪造的独立文件处理，它们由 Codex digest 覆盖。
3. 取得机器身份并建立出站 WSS，但尚不宣布 env online。
4. 为capability probe instance创建一次性runtime dir和空CODEX_HOME。
5. 构造最小 config，固定 exec-server cwd，清洗所有 secret/model/proxy变量；用 runtime lock 生成的受控 PATH 替换 ambient PATH，而非追加。
6. 创建平台 process containment。
7. 通过 verified launch boundary 启动绝对路径 `bin/codex exec-server --listen stdio --strict-config`。Phase 1 exec profile 不携带 `codex-package.json`，防止 stock 自动把未审计的 `codex-path` 插入 PATH；Linux 只在固定 `codex-resources/bwrap` 放置已验证资源。
8. 本地完成 initialize → initialized → environment/info/status。
9. 记录probe结果并正常关闭该instance，确认EOF cleanup；probe的`local_exec_instance_id`不用于业务。
10. 远端hello/initialize成功后才宣布env online。每个业务`process/start`随后重复步骤4–8，分配新的`local_exec_instance_id`，并在该instance拒绝第二个`process/start`；filesystem-read环境另用串行的一次性fs-only lane，每个outer请求启动新instance并且绝不接受`process/start`。

fs-only lane在显式insecure-dev中先把file URI解析到registered root，canonicalize并复核owner policy，再向stock发送canonical URI、恰好一个有界`fs/readBlock`和`fs/close`；响应返回前再次复核原路径，随后关闭stdio instance并验证cleanup。production Linux runner则在启动时以`O_PATH`固定registered root并探测`openat2`与`/proc/self/fd`，每次读取用`RESOLVE_BENEATH|RESOLVE_NO_MAGICLINKS|RESOLVE_NO_SYMLINKS`固定target；先以`O_PATH`确认regular file以避免FIFO/device副作用，再从固定inode取得`O_RDONLY` fd。stock fs-only child的唯一`ExtraFiles`项固定成为fd 3，`fs/open`只收到`file:///proc/self/fd/3`，caller path不会进入child。root/target替换、symlink、open/read/close、descriptor identity或cleanup任一失败都fail closed且不缓存handle。该机制关闭filesystem path TOCTOU；独立的process-tree回收与runtime executable safe-exec也已分别由agentx `b4fb3eb`和`5d40b6b`在production Linux关闭。

远端lifecycle由agentx处理，不转发给任何业务stdio child。`process/start`创建专属instance，后续process RPC按ownership路由；业务RPC重新分配local request id，method/params按pinned dialect转发，context只进入agentx ownership/audit。outer capability不包含`process/signal`。

### 7.5 agentx 安全进程模型

stock exec-server及其命令树绝不能获得 agentx机器 credential。仅靠清洗 process/start.env 不够，因为 child可能从 exec-server父环境继承变量。

生产安装必须把 connector credential域和执行树分开：

- connector拥有机器 key、OAuth、WSS；
- runner只拥有一个预先建立的本地 IPC和目标 worktree权限；
- 每个受管process的stock exec-server instance都是runner child；fs lane另行隔离；
- connector/runner IPC对 stock child close-on-exec；
- runner和命令树看不到 keychain、token文件、connector socket或环境；
- connector根据可信 process ownership关联 child notification；
- 某个instance crash由其独立containment整体kill-tree，不影响其他process instance。

Linux使用 system service身份 + 独立 runner uid/cgroup/user namespace完成首个生产实现。macOS必须通过签名 launchd/Keychain/hardened runtime隔离测试后才能标为 production；同 UID开发模式必须在 enrollment metadata中声明 insecure_dev，生产 workspace默认拒绝。平台支持不能只由“能启动命令”判断。

### 7.6 WSS resume 的准确承诺

Phase 1：

- 短时网络断开且原gateway进程仍存活：hello携带相同gateway_instance_id、exec_session_id、generation和双方cursor；双方journal覆盖所有缺口时才按原sessionSeq恢复，agentx保留全部活跃stdio instances。
- gateway进程重启：resume_rejected；prepared operation保持未发送，core中未证实终态的dispatching/acknowledged operation转unknown；agentx在grace后逐个关闭stdin并回收各instance。
- agentx进程重启时全部旧process handle失效；单个stdio child重启只使对应`local_exec_instance_id/process_id`失效，其他instance继续。

`hello/welcome/ack/session_error`是无sequence控制帧；只有`lifecycle/rpc`占双向独立sequence。独立ACK不占sequence，避免ack-of-ack循环。fresh连接先通过core CAS取得generation；resume复用原generation且必须命中同一gateway进程内registry，进程内registry本身不是generation权威。

跨 pod resume、durable frame journal和 owner routing属于 Phase 2。

## 8. Harness 垂直切片

### 8.1 harness-pool controller

controller 循环：

1. 通过 core long-poll claim queued run。
2. 获取 session lease与 attempt lease。
3. 从core读取已冻结的brain tool catalog/canonical digest（新thread先执行`FreezeBrainToolCatalog`），生成不可变、签名run manifest；`controller_callback`绑定当前holder instance直连地址。
4. 在当前 holder 副本内创建 pool-owned、worker-group-writable 的 attempt 临时根和独立进程组；control server生成control capability，capability issuer再按manifest audience为该generation签发executor-MCP/llmproxy两枚分离token。签名manifest与三枚token写入只继承给worker的bootstrap pipe，pool从对象存储读取prompt并按完整pointer复算size/hash后写入FD 4；仅当签名manifest存在previous checkpoint时，再以同样的发送侧校验将对象写入FD 5，然后本地 `fork/exec harness-worker`。run 热路径不调用 Kubernetes API。
5. 接受 worker mTLS control stream并核对 attempt/generation。
6. 从claim开始持续成对续租session/attempt lease，并从每次heartbeat返回值观察权威run/attempt状态；提交事件，转发cancel/fence/approval。收到cancel后先驱动worker/MCP/app-server与进程组清理，workload停止前不停止heartbeat。
7. 收到completed terminal并确认worker/app-server整个进程组clean exit后，先请求core冻结terminal thread/turn，再由pool从固定attempt目录的受信FD边界打开唯一rollout；pool不通过worker传checkpoint chunks，也不让worker读取rollout内容。
8. 在pool-owned `0700/0600` staging内校验JSONL、固定size与SHA-256，生成确定性artifact，以完整不可变pointer上传并复核返回pointer，最后请求core原子提交checkpoint + terminal。
9. 只有core commit明确成功或某一步明确失败后才删除本次attempt临时目录；连续transport歧义必须保留runtime供同holder审计/恢复，不能把结果不明当成功清理。

worker 进程退出后 launcher 不自动重启相同 attempt。pre-turn失败时，exact holder必须在workload已经停止且双lease仍live时调用`AbandonAttempt`，由core在run锁内原子决定requeue还是完成并发cancel；只有requeue后的新claim才会创建更高generation。`turn/start` 已接受后不重放。

Phase 1 明确接受共享 pool Pod 的故障域：一个 Pod 崩溃/OOM 可同时中断该副本上的多个 attempt。因此每副本并发必须有小而硬的上限，pool 只在本地槽位可用时 claim 新 dispatch；超出容量的 run 留在 durable queue。这是基于“harness 不执行本地任意用户代码”的有意取舍，不冒充 per-run Pod 隔离。

Phase 1 的 worker control resume也只覆盖原 harness-pool holder进程仍存活的短时断线。worker直连 manifest中的 holder实例，不经普通 Service随机换 pod；holder崩溃、callback不可达或 lease过期后，worker在 grace内 interrupt并退出。若 turn尚未被 app-server接受，core可创建新 attempt；否则 run进入 interrupted。跨 controller接管现有 worker需要独立 owner-routing设计，不在首版承诺。

Phase 3第一段先建立了pool不能绕过的core command边界：内部OpenAPI和typed client/handler提供`run-attempts:claim`、成对续租、`turnAccepted`与有界事件批量append。claim接收一个由已提交`run.queued`事实定位的明确run ID与权威run version；该outbox payload同时携带`workspaceId`、`sessionId`、`runId`和初始`runVersion`，controller不能自行猜测版本。session lease与attempt lease在同一数据库transaction续期，任一holder/generation/expiry检查失败会回滚另一半，避免半续租。每批事件只由core分配权威run seq，inline payload保持64 KiB单项和256项批量上限，大对象仍只传已hash的pointer。

第二段已经加入scheduler-only的`run-dispatches:claim|complete|release`边界。claim只对`run.queued`做`FOR UPDATE SKIP LOCKED`，同时返回入队版本以及同一transaction读取的当前run status/version；最多30秒的有界long-poll以数据库poll为事实源，未来LISTEN/NOTIFY只能优化唤醒。dispatch的`owner + claim generation`使用数据库时钟fence，pool无权领取或释放其他outbox kind。dispatch在run仍为`queued|starting`时不能complete：pool/worker process在turn接受前崩溃后，lock与attempt lease过期会让同一事实再次可见；一旦run进入`running`或terminal，迟到consumer可安全清理残留delivery。

pool侧reference controller每次只领一项，以一次分配的attempt/event/outbox身份执行exact claim；普通transport歧义只用同一组身份立即重试一次，不能分配第二组身份猜测第一次是否提交。`lease_held|version_conflict`会generation-fenced release并等待新投影，`running|terminal`残留项只调用由core状态守卫的complete。controller、control supervisor、本地process launcher、finalizer、开发对象存储和动态capability issuer现已装配进可运行的常驻`cmd/harness-pool serve --insecure-dev`；该入口是本地联调边界，不等于生产controller已经部署。

这些入口使用独立harness-pool SPIFFE identity；executor-gateway identity不能调用这些命令，core配置相同identity会拒绝启动，携带多个URI workload identity的证书也会fail closed。

#### 8.1.1 当前开发启动入口

开发authority、PKI、共享开发key、外部OIDC client secret、登录事务AES key、兼容用browser bearer、worker deployment、四份服务环境、agentx argv和fixture launch config现在由单一入口生成：

```bash
go run ./cmd/agentserver-dev prepare --insecure-dev \
  --config=/absolute/dev-stack.json \
  --output-dir=/absolute/new-agentserver-v2-dev
```

输入是closed-world `api/schema/insecure-dev-stack.schema.json`；输出目录必须事先不存在，命令绝不merge或覆盖。它从runtime manifest原始字节派生platform、Codex/runtime digest和checkpoint allowlist，生成独立服务SPIFFE证书、run capability/cursor/login-transaction key、外部OIDC client secret、兼容用browser bearer、Ed25519 signing seed及public worker keyring。目录固定`0700`、文件固定`0600`；worker deployment、agentx launch、fixture config和metadata均不含secret值。生成物由Core bootstrap/TLS、browser、executor、pool、完整worker和fixture bundle现有loader交叉加载测试。完整输入、输出树、source方式、启动顺序和剩余依赖见[`DEVELOPMENT.md`](DEVELOPMENT.md)。

开发外部依赖入口固定为：

```bash
go run ./cmd/agentserver-dev fixtures --insecure-dev \
  --bundle=/absolute/new-agentserver-v2-dev
```

单进程只绑定生成配置中的 cleartext loopback Hydra/OIDC endpoint 和 TLS loopback Responses 端点。Hydra/OIDC fixture 实现 public authorize/token、Admin login/consent、外部 IdP discovery/authorize/token/JWKS、Code + PKCE 单次消费、Ed25519 ID token 和动态 opaque access-token introspection；它同时实现 `agentserver-platform` 与 `agentserver-browser` 两个闭合 profile，按 consent 写入互斥 client/audience/scope 以及 versioned workspace grant，Browser authorization 精确绑定一个 workspace resource。兼容用固定 browser bearer 仍只服务旧 introspection 测试，两个 reference SPA 和整栈 smoke 均不读取。Responses 端逐请求验证动态 HMAC capability 的 `aud=llmproxy`、时间、authority 和 model/provider route，明确拒绝 executor token；脚本状态按 capability/run/attempt/generation 隔离，先调用 `executor.list_environments {}`，再要求对应 `function_call_output` 后返回 terminal assistant message。该 fixture 是可复现联调依赖，不是生产 Hydra、外部 IdP 或 llmproxy 实现。

harness-pool运行入口固定为：

```bash
go run ./cmd/harness-pool serve --insecure-dev
```

在首次启动Core前，先以同一份agentx runtime manifest建立本地开发authority：

```bash
AGENTSERVER_V2_DATABASE_URL='postgres://...' \
  go run ./cmd/agentserver-core bootstrap --insecure-dev \
  --config=/absolute/path/to/bootstrap.json
```

bootstrap配置是closed-world JSON：

```json
{
  "version": 1,
  "workspaceId": "40000000-0000-4000-8000-000000000004",
  "sessionId": "50000000-0000-4000-8000-000000000005",
  "actorId": "10000000-0000-4000-8000-000000000001",
  "executor": {
    "executorId": "20000000-0000-4000-8000-000000000002",
    "environmentId": "60000000-0000-4000-8000-000000000006",
    "agentxVersion": "0.1.0-dev",
    "platform": "darwin-arm64",
    "runtimeManifestFile": "/absolute/path/to/runtime-manifest.json",
    "workspaceRoot": "/absolute/path/to/workspace",
    "displayName": "Local workspace",
    "description": "insecure development executor",
    "defaultCwd": "."
  }
}
```

命令先验证配置和runtime manifest，再执行schema migration，最后在一个事务中插入active workspace、session、owner membership、offline executor和offline environment五条基础记录。完全相同的重试是零写入；任何既有identity、membership、runtime digest、root descriptor或profile不同都会回滚整个事务，绝不覆盖已有authority。`actorId`必须与开发Hydra introspection返回的canonical UUID subject一致；executor/environment ID还必须原样传给executor-gateway与agentx。这个命令会写入明确标记为`insecure_dev`的占位machine-key fingerprint，不执行真实enrollment，也不能用于生产数据初始化。

它不读取隐式默认credential。通常由`agentserver-dev prepare`生成以下配置；手工部署仍必须逐项显式提供：

- control与身份：`AGENTSERVER_V2_HARNESS_POOL_LISTEN_ADDR`（必须是显式loopback地址）、`AGENTSERVER_V2_HARNESS_POOL_TLS_CERT_FILE`、`AGENTSERVER_V2_HARNESS_POOL_TLS_KEY_FILE`、`AGENTSERVER_V2_HARNESS_POOL_WORKER_CA_FILE`、`AGENTSERVER_V2_HARNESS_POOL_SPIFFE_ID`、`AGENTSERVER_V2_HARNESS_WORKER_SPIFFE_ID`；
- Core：`AGENTSERVER_V2_CORE_URL`、`AGENTSERVER_V2_CORE_CA_FILE`，以及证书需要显式hostname时的可选`AGENTSERVER_V2_CORE_SERVER_NAME`；
- 开发authority与本地存储：`AGENTSERVER_V2_DEV_EXECUTOR_ID`、`AGENTSERVER_V2_DEV_RUN_CAPABILITY_KEY`、`AGENTSERVER_V2_DEV_PROMPT_OBJECT_DIR`、`AGENTSERVER_V2_HARNESS_RUNTIME_DIR`、`AGENTSERVER_V2_CHECKPOINT_STAGING_DIR`；三个目录都必须预先存在且仅owner可访问；
- worker与manifest：`AGENTSERVER_V2_HARNESS_WORKER_BIN`、`AGENTSERVER_V2_HARNESS_WORKER_CONFIG_FILE`、`AGENTSERVER_V2_RUN_MANIFEST_SIGNING_KEY_ID`、`AGENTSERVER_V2_RUN_MANIFEST_SIGNING_KEY_FILE`、`AGENTSERVER_V2_CODEX_RUNTIME_MANIFEST_SHA256`、`AGENTSERVER_V2_CHECKPOINT_ALLOWLIST_VERSION`、`AGENTSERVER_V2_HARNESS_WORKER_SERVICE_ACCOUNT`、`AGENTSERVER_V2_HARNESS_APP_UID`、`AGENTSERVER_V2_HARNESS_APP_GID`；
- executor与模型路由：`AGENTSERVER_V2_EXECUTOR_MCP_ENDPOINT`、`AGENTSERVER_V2_EXECUTOR_GATEWAY_SPIFFE_ID`、`AGENTSERVER_V2_LLMPROXY_ENDPOINT`、`AGENTSERVER_V2_LLMPROXY_SPIFFE_ID`；仅本地 development profile 还显式使用`AGENTSERVER_V2_MODEL`和`AGENTSERVER_V2_MODEL_PROVIDER`，production 的 model/Gateway authority 来自 Core 冻结的 workspace 配置；
- 可选容量边界：`AGENTSERVER_V2_HARNESS_MAX_CONCURRENT_ATTEMPTS`（默认2，最大64）和`AGENTSERVER_V2_MAX_RUN_DURATION`（默认30分钟）。

进程每次启动都会生成新的pool instance/holder/producer UUID；control只监听loopback TLS，健康检查允许没有客户端证书，但attempt control handler仍要求精确worker mTLS SPIFFE身份和本attempt bearer。pool从Core long-poll dispatch，为每个attempt冻结并签名manifest、分别签发executor-MCP与llmproxy capability，通过FD 3/4/5启动全新本地worker进程组，并在退出时先取消和等待attempt，再关闭control registry与HTTP server。开发launcher创建的attempt anchor为`0701`：pool/worker owner可读写，固定app UID只有execute-only traversal；此前`0700`与worker deployment的app traversal gate矛盾，会使真实worker在读取bootstrap前失败，现已由launcher与交叉loader门禁修正。

该模式刻意使用共享HMAC和`0600` plaintext本地对象，不提供生产加密、撤销或多Pod安全语义。Core/browser-gateway与harness-pool必须指向同一个开发对象目录，executor-gateway必须使用同一开发HMAC key；完成真实模型turn还依赖Linux worker runtime、已锁定的stock Codex artifact、能验证llmproxy capability的代理、在线executor/agentx以及PostgreSQL/Core。缺少任一依赖时该命令只能验证到相应的前置边界。

第三段已经加入`brain_tool_catalogs:freeze`与`{catalogId}:bindThread`边界。catalog canonicalizer已从worker抽到中立的`braincatalog`包，core与worker共同使用同一份RFC 8785校验、schema约束和domain-separated digest实现。freeze只允许live、pre-turn的`starting + leased|starting` attempt，核对workspace/session、holder、generation、双lease和run/attempt expected version；同一catalog ID的exact retry可在lease失效后读回，不同fingerprint报`idempotency_conflict`，同一attempt不能冻结第二份catalog。bind只允许未绑定记录以expected catalog version做一次CAS；同thread retry成功，换thread失败，已绑定记录没有任何schema更新命令。

pool侧新增launch-preparer边界：先从版本化executor MCP contract按显式policy allowlist构建catalog、解析prompt/checkpoint/runtime/model/endpoint等launch输入并生成完整manifest，随后以cluster Ed25519 key对RFC 8785 canonical bytes做domain-separated签名，最后才用同一catalog ID调用core freeze；只有freeze成功才返回`PreparedRunLaunch`。普通transport歧义只用完全相同请求重试一次。签名envelope和manifest均有严格类型、unknown-field拒绝、HTTPS/SPIFFE检查、固定`2025-11-25` MCP profile、`deferLoading=false`、逐tool/catalog一致性校验与`api/schema/run-manifest.schema.json`。该类型本身仍只是可测试的launch准备边界，不能据此宣称harness vertical slice已经可部署。

下一段补上了部署配置与动态authority state的组合resolver，以及manifest key的装载/轮换边界。`RunLaunchProfile`只持有deployment-owned runtime/model/MCP endpoint/limits/image/service-account/callback配置；`RunLaunchStateSource`只返回core/policy派生的prompt、checkpoint和tool allowlist，组合结果在进入sign/freeze前按完整manifest重新校验并复制所有slice/pointer。active Ed25519私钥只能从绝对路径、group/other零权限的regular Secret文件读取，支持raw seed、canonical private key或单个unencrypted PKCS#8块，并拒绝零seed、非canonical private key和读取中替换。worker侧`run-manifest-keyring.schema.json`与`VerificationKeyring`支持最多32枚旧/新公钥的显式overlap，按`keyId`精确选择且未知key不fallback。

随后加入的`0008_run_launch_authority`把prompt完整object pointer与规范化executor policy/allowlist作为不可变run authority，要求与`CreateRun + run.queued`同事务提交；同request hash的重试若launch authority不同仍报`idempotency_conflict`。该migration同时建立不可变checkpoint读模型及`sessions.latest_checkpoint_id`同session外键。harness-pool通过独立mTLS `run-launch-states:resolve`查询，core在同一事务锁定run/attempt，逐项核对workspace/session、expected versions、`starting + leased|starting + pre-turn`、active run及两条数据库时钟live lease后，才返回prompt、latest checkpoint、其绑定的完整frozen catalog和policy。`CoreClient`对所有UUID/digest/size/text/catalog canonical bytes重新验真；deployment resolver还要求checkpoint的Codex runtime manifest digest、allowlist version及catalog/policy全部与当前profile一致。新thread才分配catalog ID并freeze；已有thread恢复严格复用checkpoint绑定的catalog，allocator与freeze命令均为零调用，避免给同一thread制造第二份catalog authority。

pool侧随后加入常驻监督骨架：只在有并发槽位时long-poll下一项dispatch；每个attempt从claim起即以固定间隔成对续租，普通transport歧义只用完全相同请求重试一次，任何明确lease/generation失败都会取消该attempt runtime。新thread必须先由runtime同步上报thread ID并完成catalog CAS绑定，随后同一thread的turn accepted才能使用一次性transition identity推进core；恢复thread只核对并复用原catalog，零bind调用。turn accepted一旦由core确认，dispatch清理失败不反向否定已接受的turn，迟到consumer仍可按run状态安全complete；接受前的prepare/runtime失败则先完全停止workload，在live lease下用`AbandonAttempt`原子requeue/取消，再release原dispatch，不再依赖lease TTL。所有attempt有界并发、单项失败隔离上报，pool shutdown取消并等待在途supervisor。

其后补入了contract-first的harness worker control stream。`api/schema/harness-control.schema.json`与`api/asyncapi/harness-control.yaml`当前为1.3，冻结fresh/resume hello、welcome、独立双向sequence、非sequenced cumulative ACK、`thread_ready|turn_accepted|turn_terminal|app_server_notification|executor_mcp_progress|approval_request`以及pool单向`interrupt|approval_outcome`；Go decoder同时执行有界JSON token/depth、duplicate key、unknown/missing/null、safe integer与方向校验。完整hello绑定worker instance、workspace/session/run/attempt/generation、holder和domain-separated manifest digest。每端先把完整不可变frame放入有界journal再交给transport；duplicate必须命中保留的同frame digest，future sequence、ACK回退/越界或缺失重放区间都会关闭session。resume只接受原pool instance、原control session和原worker instance，且必须在原holder进程内journal仍覆盖缺口的30秒窗口内完成，不承诺跨Pod恢复。

pool `ControlServer`在WebSocket升级前同时要求TLS栈已验证且只有一个精确worker SPIFFE URI SAN，以及canonical 256-bit per-attempt bearer；registry只保存bearer SHA-256，不保存明文。注册时再次核对签名manifest与本holder的callback/TLS audience/service-account profile；首个hello才绑定worker instance，后续fresh连接不能替换原journal。worker事件按序调用`AttemptLifecycle`；pool先prepare frame，只有catalog bind、turn accepted或runtime `AppendAttemptEvents`的core边界成功后才commit receive cursor并ACK。approval request则在完成scope、expiry、重复ID和并发上限校验并登记后先commit/ACK，再由独立goroutine执行Core long-poll；outcome必须命中同一request的全部关联字段，并作为pool发送journal中的原frame交付。runtime append结果不明时保留同一control sequence对应的完整core request，同holder resume复用原event/producer/outbox身份；ACK本身仍不授权core transition。worker用独立于turn/MCP的context维持control stream，pool则用独立于attempt runtime、但受holder lease与pool shutdown约束的context执行这些lifecycle command；两者都不能在取消信号到达时抢先切断terminal证据链。`ControlAttemptSupervisor`再把该注册、workload启动/完全停止、startup timeout、lease/cancel interrupt和terminal分类组合到原`AttemptSupervisor`接口。普通、mTLS WebSocket和race测试已覆盖完整生命周期、乱序拒绝、core提交前不ACK、审批等待不阻塞ACK与断线后的原帧重放。

根据修订后的威胁模型，pool侧已加入本地 process launcher 和 `api/schema/harness-bootstrap.schema.json` v2：launcher使用绝对路径、显式空环境、独立进程组与 Linux parent-death signal。签名manifest和control/executor-MCP/llmproxy capability只经 inherited FD 3 传递；prompt由pool-owned object source读取，经FD 4流式发送，发送侧与worker侧都按签名pointer独立复算size/SHA-256。单测已覆盖非规范/unknown bootstrap、profile drift在fork前拒绝、错误prompt hash导致整个launch失败、capability不进入argv/env、正常清理和强制回收带孙进程的整个process group。app UID拥有的`0700`树不再依赖普通`RemoveAll`侥幸成功：production credential模式要求runtime根为execute-only traversal的`0711`，并要求Linux pool在构造launcher时证明effective `CHOWN|SETUID|SETGID|DAC_OVERRIDE`及受限的production cleaner；清理目标只能是该根下规范的随机attempt直接子目录。macOS和无credential路径明确只是开发模式。S3/KMS参考provider、共享production runtime config/factory及Core production装配已经完成；pool也能构造同一`EncryptedRunObjectStore`。production run-capability codec/key loader/keyring/schema及Core issuance/live-authorize已经完成，但pool production命令仍在其capability source接入前fail closed。真实bucket/KMS integration、完整consumer调用链及真实Linux Pod capability/image gate仍待完成。

worker侧现在会从 inherited pipe 一次读完closed-world bootstrap、立即关闭FD、按overlap keyring验签并生成新的worker instance UUID；bootstrap将三枚capability保留在各自权限域，prompt loader消费独立pipe并在任何app-server I/O前核对media type、size、SHA-256和UTF-8。control client完成fresh握手、mTLS与精确pool SPIFFE校验、bearer不跨redirect、interrupt有界投递、累计ACK及原holder 30秒精确resume。pool在生命周期core authority完成后才ACK；ACK字节丢失时resume cursor确认已提交事件，不重复调用authority。真实mTLS WebSocket与race测试覆盖完整生命周期、错误SPIFFE/bearer/redirect以及提交后ACK丢失。app-server runner也已在thread ready与turn accepted处同步跨越该control边界，turn acceptance失败会走`turn/interrupt`收口。

app-server真实child/final-exec进程层已经接入one-shot worker主状态机：`cmd/harness-worker`只接受固定`run --config=<absolute>`与FD 3/4/可选5，endpoint、model、command都不能从argv或ambient env覆盖。worker按`bootstrap验签 → prompt校验 → fresh runtime/checkpoint恢复 → control → exact-SPIFFE executor MCP与冻结catalog → stock app-server → runner → stdin EOF/child Wait → MCP/runtime close → terminal ACK`执行一次attempt；turn尚未被holder权威接受时不伪造terminal。child等待超时也不发送terminal，而是断开并让holder核验整个进程组。deployment loader锁定stable 0.146.0 config profile、runtime manifest与final-exec hash/size，mTLS私钥必须是group/other不可读的直接regular file。executor MCP HTTPS在已有CA/hostname验证之外要求leaf恰好一个与manifest相同的SPIFFE URI SAN，禁止redirect和`InsecureSkipVerify`。组合、失败顺序、artifact drift和TLS正负测试均已覆盖。

这一one-shot链现在已接通真实runtime event路径：worker把allowlist内stock notification与已关联MCP progress流水写入有界control journal，dynamic progress会等待匹配的`item/started`先入journal；terminal累计ACK保证全部前序runtime frame已经被pool处理。pool按接受的thread/turn和冻结catalog执行closed-world映射，重新校验dynamic tool JSON Schema与完成前后参数一致性，向core写入message、公开reasoning summary及tool lifecycle canonical event；browser-gateway再把progress投影成`CUSTOM{name:"agentserver.tool_progress"}`。大文本使用明确的size/SHA-256 omission摘要，raw reasoning不投影。stock在interrupt/failure时可能发送terminal却不为active item补`item/completed`；mapper现对`interrupted|failed`按ID排序生成仅用于projection的message completed、reasoning done及tool completed/result，tool result明确说明turn未完成且永远没有command presentation。它不会伪造executor结果；`completed` terminal仍严格拒绝任何未闭合item。mapper、control client及真实pending-approval cancel均覆盖这条路径，worker随后可以继续发送并等待`turn_terminal(interrupted)`的累计ACK。集成测试已贯通worker→pool→core，projector测试覆盖core committed event→AG-UI。

one-shot worker现已把实际MCP `ElicitationHandler`固定为已连接control client的`AwaitApproval`，不再接受外部固定决定。worker先journal并发送`approval_request`，pool异步观察Core canonical state，再以同session的`approval_outcome`返回；worker严格关联run/call/generation/catalog/execution/approval/nonce/context/version，将approved映射成九字段evidence的`accept`，denied/expired映射成`decline`，cancelled/consumed或control关闭映射成`cancel`。terminal、close和resume expiry都会取消outstanding approval，pending waiter默认上限64、硬上限256。真实mTLS断线门禁会在approval pending时关闭active WebSocket并冻结第二次握手：断线期间就绪的Core outcome只进入pool的有界journal，worker不会提前返回；释放同holder resume后只收到原frame一次，随后仍可发送terminal并获得累计ACK。session kernel为此提供仅`disconnected`状态可用的`QueueForResume`，而已冻结welcome cursor、尚未ready的握手窗口仍禁止新序号。对象存储应用协议、内部adapter、S3/KMS参考provider、production capability密码学合同、Core签发/在线授权、pool/gateway/llmproxy三条production consumer、真实agentx production connector、Linux process-tree containment及runtime pinned-exec都已装配；当前剩余的是具体云资源/IAM和Kubernetes全拓扑部署门禁，而不是consumer library缺失。core finalization边界现已实现：migration 0011在attempt上冻结成对的terminal thread/turn；`BeginRunFinalization`在live holder/generation、绑定catalog thread、active session和全部execution terminal的条件下以单事务写`running → finalizing`与`run.finalizing`；`CommitCheckpointAndTerminalRun`以单事务校验完整checkpoint/catalog指纹，写checkpoint、`finalizing → completed/succeeded`、session resume pointer、`run.completed`并删除双lease。fresh attempt使用本attempt冻结的catalog；resume attempt复用session latest checkpoint绑定的原thread/catalog，不虚构第二份catalog authority。

pool侧completed-turn finalizer也已接入supervisor：只有`completed` terminal与整个process tree的clean `Wait`同时成立才进入`Begin → safe open → JSONL/size/hash staging → deterministic artifact → immutable object put → Commit`；failed/interrupted或unclean wait不读取rollout。Begin、object put与Commit的普通transport歧义都只用原transition/checkpoint/object身份精确重试一次，Core返回还要逐项匹配完整checkpoint指纹；连续歧义通过显式retention error阻止runtime cleanup。pool-owned staging无论结果都清除，但attempt runtime只在commit明确成功或明确失败后清除。`turn/completed`和worker的`completed` terminal本身仍只证明stock turn及本次child/runtime有界收口，不会提前写`run.completed`。

checkpoint v1现有独立`checkpoint-manifest.schema.json`和确定性framing：16-byte magic/version、big-endian manifest长度、RFC 8785 manifest与唯一rollout JSONL，不引入tar/zip的链接、目录与额外entry语义。run manifest升为v2并冻结源run/attempt/generation/turn、runtime与allowlist；pool仅在previous checkpoint存在时继承FD 5，pool/worker双端均校验外层object pointer，worker还在创建rollout前校验manifest digest、所有authority字段、单普通文件、路径包含、size/hash和JSONL。负向测试覆盖错误外层hash、source generation、runtime digest、symlink路径、多entry、错误file type、尾随字节及整个进程组回收。

### 8.2 run manifest

manifest至少冻结：

- workspace/session/run/attempt/generation；
- prompt object id/hash；
- previous checkpoint id/object pointer/manifest digest、源run/attempt/generation、terminal turn、runtime digest与allowlist version；
- Codex runtime manifest digest；
- model/llmproxy audience和 endpoint；
- executor MCP endpoint/TLS identity/audience与显式protocol profile，以及冻结的namespace/description、tool name/description/input schema、固定`deferLoading=false`、逐tool hash与catalog digest；
- executor/env/tool policy；
- max_run_duration、max_approval_ttl、gateway active execution timeout、MCP transport/cleanup grace；
- event/control buffer上限；
- checkpoint allowlist version；
- 常驻 pool/worker runtime image digest 和 pool Pod 的 expected service account；这两项用于 launcher 核对当前部署 profile，不表示每 attempt 创建新 Pod。

reference `runmanifest`实现把上述字段编码为closed-world JSON：未知字段、独立manifest的非canonical bytes、非canonical base64url签名、非HTTPS endpoint、非SPIFFE TLS identity、catalog projection漂移或callback holder漂移都会fail closed。签名算法固定`ed25519-v1`，签名输入为`agentserver-v2/run-manifest/ed25519-v1\0 || canonical_manifest`；manifest digest另用`agentserver-v2/run-manifest/rfc8785-v1\0`域隔离。manifest嵌入签名envelope后，验签方会从其JSON值重建RFC 8785 bytes再验签，避免普通serializer对`<|>|&|U+2028|U+2029`的等价转义破坏签名；core catalog command的双方encoder同样关闭HTML转义以原样传输canonical catalog。签名envelope只携带key ID，不携带私钥或capability secret。

worker验证签名后不能从 prompt、模型输出或 MCP响应修改 manifest。

### 8.3 app-server 配置

Phase 0根据pinned schema生成两份只读文件；两者都不包含executor endpoint或bearer：

- `CODEX_HOME/config.toml`：只包含model provider和显式关闭的builtin tool/feature。
- `/etc/codex/requirements.toml`：管理员约束，固定`mcp_servers = {}`，禁止user/project层注入任何Codex MCP。

关键语义必须为：

~~~toml
approval_policy = "never"

# 仅在所 pin release 的 schema 与 tool capture 均证明该键真实生效时加入。
[tools.update_plan]
enabled = false

[tools.experimental_request_user_input]
enabled = false

~~~

对应的managed requirements显式deny all：

~~~toml
mcp_servers = {}
~~~

worker的run-manifest validator对executor MCP独立要求`https` scheme、规范host/port/path、固定TLS identity和独立audience，拒绝stdio、userinfo与fragment；网络guard只允许该内部tuple。Codex完全看不到这个endpoint。requirements文件必须在app-server启动前位于真实system path；release binary不接受把system requirements path作为`-c`/CLI参数，debug-only环境变量也不得进入生产环境。

这只是关键字段示意；完整feature/requirements键必须由所pin版本的config schema、`configRequirements/read`、零MCP bootstrap和实际model tool capture共同验证，不能复制示例后假定生效。0.145.0不认识`update_plan`禁用机制，因此仍被A03拒绝；stable 0.146.0已经通过dynamic-tool-only与deny-all gate。direct-MCP resource handler bypass继续作为负向回归，不能把executor重新配置进Codex。

worker先把checkpoint allowlist恢复到全新`CODEX_HOME`，断言staging严格匹配manifest，再写入本attempt新config；checkpoint无权覆盖配置。对当前已验证build，allowlist就是该brain thread的单个rollout JSONL，SQLite/WAL/SHM均不恢复。worker随后用自身credential初始化executor MCP，规范化`tools/list`并与manifest/catalog digest逐字节比较；不一致则在启动turn前失败。最后以manifest中的绝对路径启动`harness-final-exec`，由它固定exec `codex app-server --listen stdio:// --strict-config`。strict-config只拒绝未知字段，不能替代tool capture、final-exec和OS/网络隔离。

新thread的`thread/start`显式发送`environments: []`、`approvalPolicy: "never"`、从冻结catalog机械生成的`dynamicTools`和空`selectedCapabilityRoots`；每次`turn/start`也发送空environments。当前`ThreadResumeParams`没有environments或dynamicTools字段，cold resume固定发送rollout path与`excludeTurns: true`，收到RPC response后直接进入turn/start，不等待`thread/started` notification。resume前必须确认catalog digest与checkpoint相同；变化时创建新thread。cwd指向空、只读的非工作树目录。

### 8.4 tool catalog 与 MCP bridge

worker不做tool选择。它只实现以下确定性映射：

1. 使用绑定`run_attempt_generation + tool_catalog_digest`的worker-only capability建立一个有界Streamable HTTP MCP session，声明`elicitation`与所需progress能力；只允许manifest中的exact endpoint/TLS identity和显式protocol profile。reference profile固定为`2025-11-25` stateful；当前SDK协商到`2026-07-28` stateless或其他版本时，在读取catalog前fail closed。
2. 调用`tools/list`直至分页完成；拒绝重复名称、未知schema关键字段、过大的description/schema、非JSON Schema object或catalog上限溢出。
3. 按版本化canonicalizer规范化`{name, description, inputSchema}`并计算逐tool/catalog digest，要求与签名run manifest及checkpoint（resume时）完全一致。
4. 将每个 MCP tool 机械映射为固定 namespace 下、`deferLoading=false` 的 dynamic function；namespace/name 映射必须可逆，并拒绝非法 dynamic name、归一化后重名或 namespace 碰撞，映射表随 catalog 一起冻结。description 和 inputSchema 不由 worker 改写，MCP annotation 不投影也不作为授权事实。
5. 收到`item/tool/call`时校验thread/turn、callback request id、call id唯一性、namespace/tool和arguments schema；以`(run_id, call_id)`作为gateway幂等上下文发MCP `tools/call`。模型提供的`_meta`、endpoint或身份字段全部忽略/拒绝。
6. MCP progress只生成有界execution event。Phase 1 executor result只接受有界text/structured JSON；resource、image、audio和embedded executable content一律拒绝。worker按冻结的顺序与JSON序列化规则把允许内容确定性转换为app-server `inputText` contentItems与`success`，response成功写入stdio后删除normal outstanding entry，不等待`serverRequest/resolved`。
7. gateway 发出的 `elicitation/create` 必须引用 gateway 已在 core 创建的 execution/approval；worker 经 control stream 核对关联并只转接 canonical outcome，不创建 approval、不自行批准。cancel/fence/turn terminal 会取消 MCP context；turn terminal 与 MCP result 竞态时由同一 call 状态机只允许一方获胜，terminal 获胜后不得再向 app-server 写 response，未回复 dynamic callback 随后删除。
8. MCP transport为该worker私有并跟踪每个active HTTP request。关闭时先取消pending call并在manifest/runtime profile固定的grace内尝试SDK graceful session close；超时后必须abort全部tracked request、拒绝新request并有界返回forced-close error，不能因session DELETE等待远端in-flight handler而卡死。若transport已断，worker无法保证cancel到达gateway；gateway自身的connection grace、execution deadline和unknown/terminal转移才是远端收口屏障。

同一个call的app-server response最多写一次。若MCP terminal已持久化但response写入是否成功不明，run进入interrupted，不能重新调用MCP；后续run只能读取core中的execution结果。worker进程内mapping和buffer可丢弃，不是恢复状态。

### 8.5 worker 状态机

~~~text
booting
  → restoring
  → connecting_mcp
  → verifying_catalog
  → starting_appserver
  → initializing
  → starting_thread
  → starting_turn
  → running
  → finalizing
  → uploading_checkpoint
  → awaiting_commit
  → done
~~~

任意状态可因 cancel/fence进入 interrupting；turn接受后 crash不能创建同 run的自动重试。

worker只允许调用以下 app-server client methods：

- initialize / initialized；
- thread/start或thread/resume；
- turn/start；
- turn/interrupt；
- 对allowlist server request（Phase 1主要是`item/tool/call`）返回明确response。

worker不能调用 app-server的 command/exec、process/spawn、fs、marketplace、plugin、skills或其他宿主 API。未知 server request fail closed并中断 run。

reference runner按以下不变量实现这一状态机：

- 每个runner只服务一个run attempt，内部只启动一个持续reader pump；除该pump外没有第二个`Receive`调用者，所有`Send`只发生在主事件循环。
- client request id由worker生成并按JSON值匹配response；app-server反向request id拥有独立方向的相关空间。生命周期response、notification和reverse request不得由不同collector竞争读取。
- 新thread只从已验证catalog机械生成`dynamicTools`；resume要求checkpoint digest与当前verified catalog相同，并固定`excludeTurns=true`，在任何stdio写入前不一致即失败。
- `thread/started`（仅新thread）、`turn/started`和`turn/completed`必须与response中的thread/session/turn identity一致；terminal status只接受`completed|interrupted|failed`。
- notification sink同时有条目数、单条retained bytes与总retained bytes硬上限且不阻塞reader；任一overflow是run故障，不是丢事件继续。进入interrupting后停止新MCP dispatch并停止扩张错误/事件缓存，但继续排空到terminal或cleanup timeout。
- writer对dynamic response的成功返回是normal cleanup barrier。write失败可能已经产生partial bytes，所以attempt立即失败，bridge清理本地lease，且任何层都不得重试同一MCP副作用。

### 8.6 finalizing 与 checkpoint

收到 terminal turn/completed 后进入 finalizing，但 terminal不是 transport cleanup barrier：

1. 停止接受新的 tool dispatch/control decision；继续排空 app-server stdout。
2. 对dynamic callback逐项满足“response已写入”或“所属turn已terminal且MCP已取消”；只有协议明确定义会resolved的其他server request才等待`serverRequest/resolved`。terminal后新出现未知server request一律fail closed。
3. 确认所有 execution/process已 terminal或明确 unknown。
4. 关闭 app-server stdin，等待 child在固定 grace内正常退出。
5. timeout时worker不得发送terminal；它断开control并退出，由holder对整个attempt进程组先TERM、再KILL并核验，该 attempt不得提交 checkpoint。
6. child正常退出后，worker把app-server thread response中的rollout绝对路径规范化为相对已验证`CODEX_HOME`的locator，与thread/turn terminal一起上报；worker不读取app UID私有文件。
7. pool ACK该locator后worker退出；holder确认整个attempt进程组已经停止，并在删除runtime之前向core写BeginRunFinalization。
8. pool的Linux受信finalizer从固定attempt目录FD出发，以`openat2(RESOLVE_BENEATH|RESOLVE_NO_SYMLINKS)`、expected app UID和pinned allowlist打开rollout；当前build每个brain thread只允许这一项，路径/owner/type不符均拒绝。
9. pool流式生成逐文件hash、manifest和确定性artifact，记录冻结tool catalog digest并上传，再复算整对象hash。
10. core以expected run/attempt version提交checkpoint pointer和terminal event。
11. 收到commit ACK或明确失败后，pool才用同一受信清理边界删除整个attempt树；app runtime和pool finalization staging不能提前消失，worker-only restore staging可在恢复完成后尽早删除。

如果用户已经看到完整模型输出但 checkpoint最终无法提交，run进入 interrupted(checkpoint_commit_failed)，不能标 completed。用户可看到输出，但系统必须明确该 session不能从该 turn原生恢复。

## 9. Browser、AG-UI 与审批

browser-gateway只做：

- 验证用户 bearer的基本格式/受众后委托 core authorize；
- 创建 run并获取 run handle；
- 转发显式run cancel命令；
- 转发显式approval decide命令；
- 读取 canonical event cursor；
- 映射为 AG-UI/A2UI；
- fetch streaming与 backpressure；
- cursor_expired snapshot/rebase。

它不保存 session、run或 approval事实。浏览器断开只停止投影，不调用 cancel。

当前把 Phase 4 中不依赖真实模型运行时的协议子阶段前置：`internal/runevent`与`api/schema/canonical-run-event.schema.json`冻结canonical envelope及首批message/reasoning/tool/run/approval schema-v1 payload；未来未知kind可在完成envelope/sequence校验后跳过，已知kind的未知schema version必须fail closed。`internal/browsergateway`实现`POST /v2/workspaces/{workspaceId}/sessions/{sessionId}/agui`，要求用户bearer和`Idempotency-Key`，只接受一条新的user message，拒绝客户端历史messages/state/tools/context；另以彼此独立的command backend实现`POST /v2/workspaces/{workspaceId}/runs/{runId}:cancel`与`POST /v2/workspaces/{workspaceId}/approvals/{approvalId}:decide`。投影覆盖AG-UI message/reasoning/tool lifecycle、`run.cancelling → CUSTOM{name:"agentserver.run_status",status:"cancelling"}`、`run.cancelled → RUN_ERROR{code:"user_cancelled"}`、仅`run.completed → RUN_FINISHED`，以及`CUSTOM{name:"a2ui.operations"}`承载的display-only A2UI v0.9 command/file-change/approval审计卡。真正的审批按钮只读取`CUSTOM{name:"agentserver.approval"}`中的run/attempt/execution scope、nonce、core-derived context digest和approval version，再调用独立鉴权API；A2UI action或data model永远不能成为dispatch authority。SSE断开不调用cancel或decide，command失败也不能覆盖event stream已经观察到的canonical状态。

Core的两阶段approval authority现已落地：migration 0012创建每execution唯一、nonce全局唯一的approval；context digest由Core从完整冻结execution fingerprint派生，覆盖arguments/schema、operation plan、policy context、executor/environment、tool/mapper/policy版本及attempt generation。用户`approve`只把approval置为`approved`，execution仍保持`pending_approval`；只有持有精确live holder/generation的gateway在重新检查nonce、digest、expiry和当前approver RBAC后单次消费，execution才进入`approved`。`deny|expired|cancelled`直接终结execution；批准后approver被撤权或降为viewer时，消费必须fail closed为cancelled。所有状态变化与`approval.requested|approved|denied|expired|cancelled|consumed` canonical event及outbox在一个PostgreSQL事务提交，event payload携带CAS所需version。gateway/worker拥有有界outcome transport grace，只允许observer在`expires_at`之后取得数据库时钟确认的`expired`；decision/consume/dispatch不延长，consume仍按数据库expiry拒绝。`run.cancelling`期间同一live holder可继续读该attempt的canonical outcome，错误holder/generation仍为`lease_lost`。PostgreSQL写路径统一按`run → attempt → session lease → attempt lease`取锁，避免1秒续租与approval observation互相deadlock。真实PostgreSQL全套门禁已覆盖并发唯一决定、database-time expiry、取消期间观察、续租/观察并发锁序、capability mismatch零写入、消费时RBAC复核和outbox冲突整事务回滚。

core现已实现真实`CreateRun`与授权event-read API：browser-gateway 的 mTLS SPIFFE identity 和原始 Browser bearer 必须同时成立；Hydra introspection 强制 `active`、未过期、canonical UUID subject、`client_id=agentserver-browser`、单一 `aud=agentserver-browser-api`，并要求 endpoint permission 同时出现在顶层 scope 与 URL workspace 对应的唯一 versioned grant。Platform client/audience/scope 或第二个 workspace grant 都会被拒绝。HMAC opaque cursor 绑定 workspace/session/run/sequence，事件页返回 per-event cursor，`limit=0` 只解析重连位置。浏览器通过 `forwardedProps.agentserver.eventCursor` 续接；gateway 只在初始 queued 位置、完整 AG-UI lifecycle boundary 或 snapshot rebase 后发布 `CUSTOM{name:"agentserver.event_cursor"}`。retention gap 没有已物化的完整 lifecycle snapshot 时不能虚构 rebase。

`cmd/browser-gateway`现已提供真实HTTPS入口、到core的mTLS client、`/healthz`、`/readyz`、header/read timeout和SSE有界优雅关闭；用户bearer禁止跟随redirect，也不会进入harness/executor/agentx。core的`AGENTSERVER_V2_DEV_PROMPT_OBJECT_DIR`后端只为本地联调提供`0600` plaintext immutable object与稳定幂等pointer，它不是加密、共享或多副本安全的生产对象存储。生产部署必须替换为前文定义的加密S3-compatible实现，不能把该目录挂载后冒充生产闭环。

Hydra登录链现已落地。migration 0013增加独立`users`、`(issuer, subject) → user` identity映射、一次性`oidc_login_transactions`和`hydra_consent_transactions`；旧workspace principal在加外键前物化为active user。Core只明文保存challenge/state/browser-binding SHA-256索引，Hydra challenge、OIDC state/nonce、外部PKCE verifier、browser binding和成功redirect由AES-256-GCM密封，AAD绑定transaction与purpose。callback必须同时命中state与`Secure; HttpOnly; SameSite=Lax`的`__Host-agentserver-oidc` binding并以PostgreSQL CAS从`pending`单次claim；外部identity必须命中active user和active mapping。login acceptance和consent各自有独立receipt；成功redirect会密封为状态与审计证据，但当前不会用它猜测或恢复一次结果不明的Hydra Admin写入。客户端callback/challenge本身不能再次消费。

Core使用真实`go-oidc` discovery/JWKS/ID-token verifier和OAuth Code + PKCE exchange，严格核对issuer、audience、nonce及可选`at_hash`；Hydra Admin client禁止redirect并限制method、JSON和response bounds。Hydra continuation只允许同origin exact `/oauth2/auth`及单个、类型匹配的login/consent verifier，禁止fragment、RawPath和额外query；consent fingerprint覆盖consent/login challenge与login session。生产`platform-gateway`只把三个login/consent/callback GET通过mTLS送到Core，并在Platform origin代理Hydra public authorize/token；Browser SPA也使用这一中心授权端点，但拿到的是独立Browser client、audience和workspace resource authority。public代理的transport仍连接内部mTLS upstream，但HTTP `Host`、`X-Forwarded-Host`和`X-Forwarded-Proto`固定来自已校验的public origin，防止Hydra把内部service authority写入`request_url`并在Admin accept后返回不可用的内部continuation；它允许中断后遗留的浏览器Cookie，但用全新request并禁用client Cookie Jar，Cookie不会进入Hydra upstream。login bridge对浏览器继续返回统一错误，同时在服务端记录`operation/stage/status/error_type/error`结构化诊断；日志不读取request query、事务Cookie、code、state、challenge或token，并对下层错误中的URL query再次脱敏。insecure-dev另以显式环境变量启用唯一`GET /auth/idp/authorize`代理，使宿主浏览器可到达容器loopback IdP；该代理同样接受同origin事务Cookie但绝不向fixture转发，discovery/token/JWKS仍由Core私下访问。生产未显式配置时这条开发路由为404。

reference SPA从无敏感信息的`GET /auth/config`启动Authorization Code + PKCE；state/verifier/nonce和workspace/session关联只短期保存在`sessionStorage`并在callback时先删除后校验，access token只留页面内存。旧的手填bearer和URL-fragment入口已删除，`browser-url.sh`只输出origin。host smoke使用一个TLS 1.3 Cookie Jar走相同完整链，主动验证携带原binding的callback replay、consent replay和浏览器authorization-code replay都失败，再把本次动态token复用于五条AG-UI run。

纯协议测试仍不需要启动codex app-server，因为输入可以是确定的canonical fixture；真实开发栈则已经由harness按attempt启动stock app-server负责模型循环并产出候选事件。`deploy/insecure-dev`当前已贯通AG-UI → Core → harness-worker → stock app-server → executor MCP → agentx → stock exec-server → durable checkpoint。browser-gateway同时在同一HTTPS origin的`/`提供dependency-free reference web：OAuth access token和cursor只驻留页面内存，SSE使用带Authorization header的fetch streaming，A2UI只渲染服务端已校验的display-only v0.9 Card/Column/Text子集；页面的Cancel按钮调用独立command endpoint并显示`Cancelling/Cancelled`，Approve/Deny按钮则只从canonical approval projection构造带nonce/digest/version的decision command。开发shell policy固定为`ask`；五run smoke分别验证批准完成、批准执行后取消、拒绝、数据库时钟过期和pending时取消。正常、deny和expiry三条run提交checkpoint；两个取消run均不得提交checkpoint。deny/expiry/pending-cancel没有agentx dispatch或operation，两个批准shell各冻结两条operation-plan但只有`process_start`实际dispatch。2026-08-01新Linux arm64镜像manifest digest `24c44fe44872962a828df84d3ff67ae2d541e076fad1e4101f9dc0dca5d8bf21`已在全新卷及同卷立即复跑通过：每轮先完成Code + PKCE登录与callback/consent/code重放门禁，累计数据库计数从`3/5/4/2`增长到`6/10/8/4`（checkpoint/approval/operation/dispatched operation）。fixture场景marker只读取最新user message，不会由checkpoint历史污染后续run。该验证还补齐pool approval outcome写、worker event写及双向resume replay三处ACK线序：旧piggyback cursor对应的不可变frame总在更新的standalone ACK之前上链，测试以人为阻塞socket write稳定复现并由重复/race门禁覆盖。该闭环仍是fixture identity、scripted Responses和单容器拓扑，不能据此宣称完整产品链已经生产部署。

browser-gateway运行配置为：public listener使用`AGENTSERVER_V2_BROWSER_GATEWAY_LISTEN_ADDR`、`AGENTSERVER_V2_BROWSER_GATEWAY_TLS_CERT_FILE`、`AGENTSERVER_V2_BROWSER_GATEWAY_TLS_KEY_FILE`；core origin与mTLS使用`AGENTSERVER_V2_CORE_URL`、`AGENTSERVER_V2_CORE_CA_FILE`、`AGENTSERVER_V2_CORE_CLIENT_CERT_FILE`、`AGENTSERVER_V2_CORE_CLIENT_KEY_FILE`，可选`AGENTSERVER_V2_CORE_SERVER_NAME`覆盖证书名。Hydra同origin代理和前端public-client配置使用`AGENTSERVER_V2_HYDRA_PUBLIC_UPSTREAM`、`AGENTSERVER_V2_BROWSER_OAUTH_CLIENT_ID`、`AGENTSERVER_V2_BROWSER_OAUTH_AUDIENCE`、`AGENTSERVER_V2_BROWSER_OAUTH_SCOPES`；`AGENTSERVER_V2_DEVELOPMENT_OIDC_AUTHORIZATION_UPSTREAM`只在insecure-dev设置。core URL必须是无credential/path/query/fragment的HTTPS origin。

审批链路：

1. app-server发`item/tool/call`，worker调用executor-gateway MCP；gateway `PrepareExecution`冻结完整context hash。
2. policy=ask时创建带绝对`expires_at`和一次性nonce的approval；gateway在原`tools/call`上发`elicitation/create`，worker同步设置本地兜底timer。
3. worker经pool/core产生canonical approval event；app-server只保持原dynamic callback pending，不接收form。
4. browser提交decide API。
5. core校验当前RBAC、`expires_at`、nonce、hash和attempt generation，并以CAS固化唯一outcome。
6. worker把canonical outcome回给gateway elicitation；gateway消费approval并在dispatch前再次校验live RBAC/generation。
7. core到期CAS为`expired`后主动下发，worker以`decline`回复pending MCP elicitation；若无法确认或送达，则取消MCP并在cleanup grace内`turn/interrupt`。
8. 显式cancel、worker control/MCP断线和elicitation异常清理同样cancel MCP、interrupt并按typed outstanding规则收口；浏览器断线本身不取消。

app-server使用`approvalPolicy=never`，因此不会出现第二张Codex tool approval卡。gateway active-execution deadline在pending approval期间由我们自己的状态机暂停；MCP transport timeout不能充当approval expiry timer。

Hydra login/consent bridge和reference Web的Code + PKCE入口现已在executor+harness主链稳定后接入；后续产品Web UI仍复用同一browser-gateway协议边界，不引入codex app-server直连。

## 10. 部署与安全

### 10.1 Kubernetes

- core可多副本；所有写入经 PostgreSQL CAS。
- browser-gateway可多副本且无状态。
- harness-pool可多副本；每个 claim有 holder/generation lease，每个副本有小而硬的本地 attempt 并发上限，`minReplicas >= 1`。
- executor-gateway Phase 1 replicas=1。
- run 热路径不创建 Kubernetes Job/Pod/Secret/ConfigMap/NetworkPolicy；pool 在本地为每 attempt 新建 worker/app-server 进程组，退出后不自动重启同 generation。
- pool、worker与 app-server使用固定的不同 UID/文件权限域；child禁止 ptrace/process-vm和读取 worker/pool `/proc`状态。固定 worker 代码可以共享 Pod，但不把这种边界宣称为对任意代码安全。
- rootfs只读；CODEX_HOME与 staging位于有配额 tmpfs/emptyDir；child mount view不含 workspace或 service-account token，worker-only staging依靠不同 UID和 `0700`目录不可读。
- 只有 init-network-guard持有短期 `NET_ADMIN`并安装按 UID默认拒绝的 nftables OUTPUT规则；runtime pool/worker/app-server都丢弃 `NET_ADMIN/NET_RAW`。
- network init在任何runtime container启动前replace同名IPv4/IPv6 table；若规则已经提交而init进程在退出前被杀，Kubernetes重试会先清理旧table再原子发布完整新规则集，不会因`EEXIST`卡死Pod。
- runtime不再复制或物化Kubernetes Secret。renderer把closed-world profile中的每个key以只读`subPath`单文件挂载为Pod生命周期内固定的regular-file snapshot；普通组件以`0440 + fsGroup`读取，独立GID的fork worker只读取其Pod内`0444`的worker client identity/public keyring。pool signing material仍为`0440`，worker不加入pool group。Secret更新由新Pod rollout接收，不做原地热切换。
- final-exec trampoline只保留 stdin/stdout/stderr，fd 3以上 close-all；worker control/credential FD同时必须为 `O_CLOEXEC`。
- app-server没有 Kubernetes API token。
- worker 只使用 pool 签发的 per-attempt control/MCP capability；不可读 pool service account、对象存储或签名凭证。
- harness-pool拥有上传 checkpoint 和调用 core 的最小权限，不需要动态创建/删除 Kubernetes workload 的 RBAC。

### 10.2 网络

- Pod NetworkPolicy只限制pool+worker+child destination并集，不能冒充进程隔离；按UID的OUTPUT规则允许pool到core/对象存储等固定内部端点、worker到pod-local control+executor-gateway，app-server只到llmproxy。
- app-server禁止MCP、直接DNS、外部连接和cross-origin redirect目标；只读hosts固定llmproxy identity。
- worker禁止通用DNS，只能连接run manifest固定、TLS校验的executor-gateway；Phase 1不直连第三方MCP。未来外部MCP必须先经内部policy/egress proxy。
- llmproxy与executor MCP使用不同audience capability，后者只在worker域。
- worker control网络与 app-server egress网络分开。
- executor机器 credential在 llmproxy必须被拒绝。
- exec-server默认无 http/request capability。

### 10.3 secret

- capability只覆盖 max_run_duration + 短 grace。
- child env中只允许`aud=llmproxy`的短期capability。
- worker mTLS、executor MCP bearer、对象存储和Kubernetes credential不进入child。
- checkpoint/event/log统一执行 secret detection，但处置不同：event/log可按 policy过滤；checkpoint命中非模型 runtime credential时整体拒绝/quarantine，不能改写 rollout后冒充 native resume。
- DB credential字段使用 KMS envelope encryption与 AAD。
- presigned URL不持久化。

## 11. 测试与 CI

### 11.1 CI jobs

| Job | 内容 |
|---|---|
| v2-unit | go vet、go test、race-sensitive package、禁止 v1 import |
| v2-contract | OpenAPI/AsyncAPI/schema生成与 drift；fixture validation |
| v2-postgres | real PostgreSQL migration、CAS、lease、SKIP LOCKED、crash boundary |
| v2-codex-appserver | manifest stock binary上的 A01–A12 |
| v2-harness-image | 真实system requirements MCP deny-all、不同UID/mount/FD、worker→pool/MCP与app→llmproxy per-UID egress、direct/redirect/MCP deny上的A04/A12 gate |
| v2-codex-execserver | manifest stock binary上的 E01–E10 |
| v2-agentx-compat | 当前 server schema/fixture × pinned agentx release |
| v2-e2e | fake model + fake IdP + real Postgres/MinIO + real Codex/agentx |
| v2-chaos-nightly | kill core/gateway/pool/worker/agentx和断网故障矩阵 |
| v2-security | path/symlink/TOCTOU、env/FD/ptrace、schema fuzz、secret scan |

根 Makefile增加 v2-test、v2-contract-check、v2-conformance、v2-e2e目标；GitHub Actions与 GitLab CI都必须显式调用，不能假定根 module测试包含 v2。

### 11.2 故障注入矩阵

至少在以下边界 kill进程：

- CreateRun DB commit前/后；
- outbox claim前/后；
- local worker fork 前/后、bootstrap pipe 写入中、worker exec 成功但 control fresh hello 前；
- turn/start发送前、response后、首事件后；
- PrepareExecution commit后；
- BeginOperationDispatch commit前/后；
- WSS send前/后；
- agentx child写入前/后；
- agentx ACK前/后；
- process exited notification前/后；
- operation terminal DB commit前/后；
- MCP result返回前/后；
- turn/completed后、app-server退出前；
- checkpoint upload前/后；
- checkpoint pointer CAS前/后。

验收不是“服务能重启”，而是：

- 不重复副作用；
- stale generation不能写入；
- 无法证明的结果是 unknown；
- run/event/cursor对用户可解释；
- checkpoint pointer永不指向未验证对象。

## 12. 分阶段交付

### Phase 0 — Conformance lab

交付：

- 独立 v2 module与 CI骨架；
- runtime-manifest；
- app-server/exec-server probe；
- dynamic-tool-only、reference worker MCP bridge/elicitation和dynamic checkpoint round-trip；
- scrubbed fixture。

退出条件：A01–A12、E01–E10全部通过。

### Phase 1 — Core state kernel

交付：

- migration runner；
- session/run/attempt/lease/event/outbox；
- execution/execution_operations/approval；
- capability issuer；
- public/internal contract与 generated types；
- real PostgreSQL并发测试。

退出条件：所有 domain command的 CAS、幂等、stale generation与 crash test通过。

### Phase 2 — Executor vertical slice

交付：

- executor enrollment和机器 key binding；
- 单副本 executor-gateway；
- agentx v2 connector/supervisor；
- 每个受管process独占的stock exec-server stdio instance与独立fs lane；
- outer capability排除`process/signal`；
- list_environments、shell、read_file；
- operation journal和 30 秒同进程 resume。

退出条件：无模型 scripted client可执行确定性 argv，四个 dispatch边界 crash不重放。

### Phase 3 — Harness vertical slice

交付：

- harness-pool claim/controller；
- per-attempt local process launcher、bootstrap pipe、进程组回收与 worker；
- stock app-server stdio；
- Codex MCP deny-all、冻结dynamicTools和worker MCP bridge；
- canonical model/MCP events；
- finalizing/checkpoint commit。

退出条件：scripted model完成 shell调用和第二 turn native resume；mid-turn crash不重跑。

### Phase 4 — Browser 与 approval（已满足退出条件）

交付：

- browser-gateway；
- AG-UI/SSE cursor/rebase；
- A2UI execution/approval卡；
- Hydra Code + PKCE login/consent bridge；
- MCP elicitation完整闭环；
- 显式 cancel。

退出条件：浏览器断线/重连不取消 run，approval TTL/cancel/断线全部 fail closed。

其中canonical event → AG-UI/A2UI、SSE handler、core public CreateRun/event cursor/cancel/approval-decide、两阶段Core approval authority、真实backend、两阶段holder清理、reference Web按钮与单容器部署已作为Phase 3期间的前置协议子阶段实现；executor-gateway `policy=ask → CreateApproval/ConsumeApproval`、worker MCP elicitation及harness-control decision/expiry/cancel也已接成开发运行链。migration 0013、Core Hydra Admin +外部OIDC bridge、browser-gateway同origin OAuth边界、reference SPA Code + PKCE和动态token smoke现已实现并由协议/组合测试覆盖。新pinned Linux arm64镜像又在全新卷及同卷复跑中先通过登录和三项replay gate，再覆盖approve、执行后cancel、deny、database-time expiry和pending-approval cancel，并对后三者逐项断言`dispatched_at IS NULL`及零`execution_operations`。该复跑发现并关闭control双端live write及worker resume tail的累计ACK线序缺口；旧cursor frame不再可能被新standalone ACK超车。四条进程级故障门禁又在同一pinned Linux arm64环境各连续通过20次，分别覆盖control mTLS pending-approval断线/journal/replay、MCP HTTP pending elicitation断线后迟到accept零dispatch、runner interrupt/typed cleanup，以及真实MCP→shell→agentx WebSocket恢复窗口到期后的Core unknown收口。Phase 4已满足退出条件。S3/KMS参考provider、共享production factory、Core与pool production装配、production capability wire/keyring、Core issuance/live-authorize、pool/gateway/llmproxy consumer生产入口、agentx生产身份连接、Linux cgroup v2 containment及`5d40b6b` runtime pinned-exec已经完成；真实provider/IAM integration、Kubernetes部署与全拓扑故障注入门禁仍是Phase 5工作，不能把insecure-dev smoke计作生产退出证据。

### Phase 5 — Hardening 与生产门槛

交付：

- 供应商无关的加密对象协议、具体S3-compatible/KMS provider、rotation/retention和部署门禁；
- 未来第三方MCP所需的内部policy/egress proxy（Phase 1 executor-only不依赖外部MCP）；
- K8s security context/NetworkPolicy；
- agentx平台隔离与runtime executable immutable/safe-exec（production Linux已由`066acf6`、`b4fb3eb`、`5d40b6b`关闭，其他目标平台须独立验收）；
- chaos、fuzz、secret scan；
- SLO/告警/runbook；
- Codex/agentx升级兼容流程。

退出条件：ARCHITECTURE.md Phase 0 gate与本文件安全/故障矩阵全部有自动化证据。

Phase 5首个切片已经完成应用层对象协议与内部接入，但没有静默选择供应商。`internal/objectstore`固定明文authority、KMS `GenerateDataKey/DecryptDataKey` encryption-context边界、1 MiB分块AES-256-GCM、有界header、随机per-object nonce prefix、immutable `PutIfAbsent`及existing-object完整解密复核。Core prompt store把稳定幂等pointer写入`user-prompt` scope；harness-pool按签名manifest中的workspace/kind/pointer打开prompt与previous checkpoint，并把finalizer产出的exact pointer写入`checkpoint` scope。launcher和finalizer接口因此显式携带workspace与closed-world object kind，worker仍不获得对象存储或KMS能力。

测试覆盖chunk边界、并发exact put、明文size/digest漂移、同object ID不同authority、跨workspace/kind密文替换、header/ciphertext篡改、截断、尾随数据、未读尾部在`Close`时强制认证、Core幂等冲突映射、pool scope转换及malformed pointer零底层调用。相关包race各连续通过5轮，完整`make check`通过；当前源码交叉编译出的三个Linux arm64测试二进制又在OCI digest `24c44fe44872962a828df84d3ff67ae2d541e076fad1e4101f9dc0dca5d8bf21`容器中各通过5轮。

第二个切片按[`ADR 0001`](adr/0001-aws-reference-object-provider.md)增加`internal/objectstore/awsprovider`参考实现。S3 client固定5 GiB以内single-part conditional `PutObject`、精确content length、`WhenRequired` checksum和`NopRetryer`；只有明确`412 PreconditionFailed`映射为existing，`409 ConditionalRequestConflict`与transport错误继续交给上层exact retry，`NoSuchKey`则单独映射not-found。KMS client固定`GenerateDataKey(AES_256)`，把protocol与完整authority SHA-256放入encryption context，解封时使用对象头保存的KeyId，并要求/清零exact 32-byte plaintext buffer。S3/KMS region与HTTPS endpoint独立，配置没有静态credential字段，只使用SDK workload/default chain。fake client与真实SDK serializer/error decoder单测共同覆盖输入wire、412/409/404/500分类、未消费body、content length/body关闭、KMS context/KeyId/32-byte边界、buffer清零和无自动S3 retry。

最终provider包race连续通过5轮，完整`make check`通过。交叉编译的Linux arm64静态测试二进制SHA-256为`2b46ba9a4433747bb90a08ca7381ea25321e75b22a65163f7017503339adb2bb`、size为`14515151`；它在OCI index digest `24c44fe44872962a828df84d3ff67ae2d541e076fad1e4101f9dc0dca5d8bf21`的arm64 variant中，以network none、read-only root、UID/GID 65534及drop ALL capabilities连续运行5轮通过。

这些证据仍不是具体生产S3/KMS部署：Core与pool的production factory/config选择路径已经接线，但真实bucket/KMS endpoint与IAM workload identity、credential/rotation、retention/orphan cleanup、故障注入和Kubernetes wiring尚未完成，因此Phase 5尚未满足退出条件，也不能把本切片描述成生产对象存储已上线。

第三个切片按[`ADR 0002`](adr/0002-production-run-capability.md)固定生产run capability的密码学与轮换合同。`internal/runcapability`新增与开发`asv2dev1`域完全隔离的`asv2cap1` token：Core-only Ed25519 signer对包含精确key ID长度和RFC 8785 canonical claims的domain-separated消息签名；verifier按key ID精确选择公钥，拒绝未知key、错误issuer/audience、非canonical或open-world claims、时间窗外token和开发HMAC token。executor-MCP与llmproxy claims彼此排斥，公共authority绑定workspace/session/run/attempt/generation/actor/holder/deadline/expiry，生产文本额外拒绝控制字符。

Core私钥loader只接受绝对clean路径中group/other零权限的regular Secret文件，支持raw seed、canonical private key或单个未加密PKCS#8 Ed25519块，并在读取前后复核文件identity/size/mtime。closed-world `run-capability-keyring.schema.json`允许最多32枚公钥做显式rotation overlap；未知key ID不fallback。codec、私钥格式/权限、轮换overlap、tamper/cross-audience/dev-token拒绝和JSON Schema均有单测及race门禁。该切片当时只完成密码学合同；后续切片已经完成Core签发/live-authorize、pool调用和gateway/llmproxy逐请求本地验签加在线授权，production serve不再因此阻塞。

第四个切片完成Core authority。内部OpenAPI新增三条精确路由：只有harness-pool可调用`run-capabilities:issue`，只有executor-gateway可调用`authorize-executor-mcp`，只有llmproxy可调用`authorize-llmproxy`；后两条把token放在`Authorization: Bearer`且所有响应`Cache-Control: no-store`，workload identity不能交叉使用。Core以attempt `created_at`和active key稳定派生两枚token及UUIDv8 capability ID，executor claims冻结下一次turn-acceptance版本、catalog和approval TTL，model claims只冻结route。签发和live-authorize同时受本机与数据库deadline约束，expiry grace不授权deadline后的新操作。

数据库authority使用repeatable-read/read-only snapshot逐次复核active workspace、owner/developer membership、session active run、双live lease、holder/generation、`starting/leased` pre-turn或`running/running` accepted状态、production executor/connection/non-insecure environment，以及fresh attempt catalog或session latest checkpoint catalog与launch policy一致。真实PostgreSQL测试覆盖fresh、thread bind后的exact issuance、cold resume、pre-turn/accepted、成员降权/移除、lease expiry、executor offline、connection expiry、insecure-dev-only environment、catalog/policy drift、cancel与finalizing fence；完整既有PostgreSQL套件同时通过。production `agentserver-core serve`现在要求独立llmproxy SPIFFE ID、issuer、active key ID、受限私钥文件、公钥keyring、精确executor/model/provider和duration/grace配置，并且仅production mode注册这三条路由；insecure-dev不读取这些生产secret。pool capability source、executor-gateway、llmproxy consumer和agentx production connector现均已接线并有各自进程级门禁；runtime executable safe-exec已由agentx `5d40b6b`关闭，完整调用链仍等待实际Kubernetes/S3/KMS/Hydra部署门禁。

后续consumer切片依次关闭三条生产数据面。`harness-pool serve`在production从Core按exact holder/generation签发两枚audience分离的capability，并选择共享object store；`llmproxy serve`以TLS入口接收模型请求，本地验证`aud=llmproxy`后逐请求向Core在线授权，Core 按 run 冻结的 Gateway version 与逐用户 OIDC grant 动态返回 exact upstream route 和当次 bearer；`executor-gateway serve`以`aud=executor-mcp`本地验签、逐请求Core授权并拒绝insecure environment。生产 llmproxy 不再读取静态 model/provider、上游 URL、CA 或 credential 文件。三个入口都把显式development模式与production配置分开，均有TLS/mTLS、错误authority和进程级组合门禁。

executor identity切片增加migration 0014、`asv2enr1`短期token、Ed25519/P-256双持有证明、Hydra v26.2.0 exact ES256 `private_key_jwt` client、opaque五分钟token、browser executor管理API与Core在线introspection/revoke。gateway身份切片进一步增加独立OpenAPI、进程内最多4096项的256-bit challenge、`executor-wss-proof/ed25519-v1` transcript和production WSS authenticator。gateway在内部enrollment请求上注入自身唯一executor ID，Core在验证token后、任何DB/Hydra副作用前比对；agentx不能覆盖该header。实际production serve E2E覆盖mTLS SPIFFE、enrollment relay、两次online authorize、bad proof消费、replay、Core 503保留challenge并重试、revoke与shutdown；timestamp固定whole-millisecond，Secret loader明确支持解析为受限regular target的Kubernetes projected symlink。独立agentx后续切片已经完成owner-only Ed25519/P-256存储与崩溃恢复、RFC 7523 exchange/cache、fresh challenge proof、production WSS重连、detached runtime release trust、Linux connector/runner密钥隔离及filesystem safe-open；`b4fb3eb`进一步完成cgroup v2 process-tree containment，`5d40b6b`又完成root-owned runtime安装验证、Codex pinned-inode exec和bwrap fail-closed门禁。gateway进程丢失恢复审计现也已经完成；当前下一门槛是Kubernetes/IAM/Secret/NetworkPolicy清单和跨仓生产拓扑故障注入。

agentx cgroup切片由connector在自身delegated unified hierarchy下创建随机root，独立guardian持有parent/root FD和唯一privileged控制面；runner通过`UseCgroupFD`原子进入root。per-instance `Prepare → Commit/Abort`事务在开放parent/child迁移权限前冻结既有workload，runner以`CLONE_INTO_CGROUP`启动blocked launcher，guardian逐项核验PID/PPID/UID/GID/精确cgroup path后撤权解冻；正常terminal也无条件`cgroup.kill`并确认`populated=0`。guardian二阶段self re-exec后所有runtime thread只保留`CAP_CHOWN`，runner与stock为零capability。client mismatch立即fail closed，ForceCleanup只在root确认删除后释放descriptor并支持有界重试。Linux arm64、无网络、只读rootfs、仅`CHOWN/SETUID/SETGID`且显式PID 1 reaper的真实内核门禁中，迁移冻结/并发remove/`setsid`回收、runner停止触发connector无`CAP_KILL` fallback、runner/guardian/connector SIGKILL、官方stock Codex 0.146.0 safe-open+cgroup链三组测试各连续通过20次。生产Kubernetes镜像必须提供真实child reaper和writable delegated cgroup v2 subtree；普通只读cgroup mount不能冒充该证据。

gateway restart切片增加migration 0015及startup-only recovery API。production `executor-gateway`在listener/readiness之前用全新gateway identity调用Core；Core先在独立事务中fence当前connection generation并把executor/environment下线，再在第二个事务中按确定性顺序恢复最多64个execution。拆成两个事务是为了避免recovery采用`connection → run`而旧operation写路径采用`run → connection`的反向锁序；第二个事务持有executor锁阻止fresh generation，并且Begin/ACK/terminal写路径现在都在实际变化前重新锁定、核对online/current/unexpired connection。完全未发送的approved operation保留prepared，`dispatching|acknowledged`收为unknown，合法尾部timeout收为skipped，已有可信operation终态则补交execution聚合终态；其他未发送必执行operation使恢复fail closed，但不能撤销已经提交的fence。每个恢复execution只产生一条带reason、generation和changes的canonical event/outbox；响应不明时进程退出，不在原producer identity内重试。

真实PostgreSQL 17全量套件已经覆盖该迁移、connection liveness、恢复聚合、先fence后fail-closed和重复调用。额外的独立OS进程矩阵在Begin前/后、WSS不可逆发送marker前/后、ACK前/后、operation terminal前/后、execution terminal前/后逐点hard-kill，随后用新gateway/producer identity执行与production相同的`RecoverGatewayStartup`，证明send marker不增加、旧generation不能dispatch、重复恢复无第二条event。最大256-operation恢复evidence/event也已验证不超过64 KiB inline boundary；相关Core/gateway包的race detector通过。这关闭的是应用层单副本restart合同，不能替代尚未完成的真实Kubernetes、agentx跨仓和网络故障部署门禁。

生产部署初始化切片新增`agentserver-core bootstrap --config=/absolute/path`。它不隐式执行migration；部署顺序固定为`migrate → bootstrap → runtime`。bootstrap在单个PostgreSQL transaction和独立advisory lock内创建首个workspace/session/owner user、外部`(issuer, subject)`映射、owner membership及固定`enrolling` executor；完全相同的重试零写入，任一既有authority不一致整笔回滚且绝不覆盖。配置为bounded、closed-world canonical JSON，允许Kubernetes projection解析到稳定regular target，但拒绝group/other可写target。

同一切片曾在生产镜像内加入`agentserver-init materialize`，但真实集群验证发现它把所有服务绑定到同一个不必要的init失败域，且`fsGroup`对`emptyDir`根权限的处理会使该路径拒绝自己的destination parent。该materialize子命令、实现和service镜像副本现已删除；renderer改用Kubernetes Secret只读单文件`subPath`挂载。`agentserver-init`只保留harness目录准备与`install-network-guard`：后者只接受closed-world JSON中的显式IPv4 TCP tuple和固定UID；worker/app的其他IPv4及全部IPv6均拒绝。A12 Linux gate连续安装同一规则集两次，覆盖network init commit后被杀的重试形状。

后续renderer切片新增`agentserver-deploy validate|render`和closed-world `production-deployment.schema.json`。输入只接受`linux-arm64`、digest-pinned service/harness镜像、固定ClusterIP、显式CoreDNS Service与Pod selector、AWS workload role、六个互异Secret和bounded resource/egress；Core/browser/llmproxy至少双副本，executor-gateway固定单副本`Recreate`且无HPA/PDB。renderer按`foundation → migrate → bootstrap → runtime`输出五个只读、原子、exact-retry文件，其中Job仅用于migration/bootstrap，run热路径仍由harness-pool本地fork worker。五个runtime Deployment固定linux/arm64 node selector、只读rootfs、tmpfs、最小capability、默认拒绝NetworkPolicy、精确内部Pod egress、DNAT前后DNS目标、固定hostAliases和AWS projected workload token；harness另装配目录初始化和UID nftables guard。

示例配置已经同时通过Go loader、JSON Schema、两次immutable render和`kubectl create --dry-run=client`。renderer生成的worker文档由`cmd/harness-worker`真实validator复核，harness-pool/llmproxy环境通过真实production loader，Core/browser/executor环境名与命令合同逐项比对且全bundle禁止`AGENTSERVER_V2_DEV_*`和静态AWS credential；完整`make check`通过。这关闭的是确定性Kubernetes清单生成和本地client结构门禁，尚未替代生产镜像构建、真实IAM/Secret/Hydra/OIDC配置、PostgreSQL bootstrap真库、Kubernetes server-side admission、全拓扑E2E及故障注入，因此Phase 5仍未满足退出条件。

后续部署打包切片已经补齐两个scratch `linux/arm64`生产镜像和环境锁定的Helm Chart。镜像构建固定Go 1.26.5、Apple container 1.2.0、stock Codex 0.146.0与审核过的bwrap，保存OCI archive后递归验证唯一platform manifest、descriptor digest、runtime config、两层diff ID及closed-world逐文件owner/mode/size/SHA-256；不再用会注入`dev/proc/sys`与host文件的运行后container export冒充镜像内容。Chart把Namespace和workload Secret外置，锁定Namespace、生产配置摘要、镜像digest、migration `pre-install,pre-upgrade`与bootstrap `post-install,post-upgrade`顺序。SG registry后来确认为公开拉取面，因此closed-world配置拒绝`pullSecret`，两个hook和五个Deployment均不生成`imagePullSecrets`；发布写凭据不进入Pulumi或运行时。完整Go/Node门禁、真实镜像构建、`helm lint --strict`和`helm template`已经通过。该切片形成了可执行的部署产物与说明，但真实远程安装的完成证据仍必须包含目标registry远端digest、实际IAM/OIDC/Hydra/PostgreSQL/S3/KMS/Secret材料、Kubernetes server-side dry-run、rollout、浏览器登录/AG-UI以及agentx端到端；没有这些环境证据时不能把本地Chart绿灯表述为生产已经上线。

## 13. 建议的首批 PR

1. v2 module、Makefile、CI、禁止 v1 import。
2. contract目录和 runtime manifest schema。
3. app-server stdio + fake model + exact dynamicTools/callback probe。
4. A04 Codex MCP deny-all image gate。
5. reference worker MCP client/catalog/elicitation/typed-cleanup probe。
6. dynamic checkpoint graceful-shutdown/resume round-trip。
7. exec-server stdio process/fs/EOF负向与dedicated-instance adapter probe。
8. core migration runner +最小 session/run schema。
9. lease/event/outbox并发状态机。
10. execution_operations + crash-injection store tests。
11. agentx-wss、outer profile与executor MCP contract。
12. agentx v2独立仓库bootstrap + shell executor vertical slice。

前 6 个 PR只建立事实和门槛，不写五个服务的空壳。第 7 个 PR后才开始业务 runtime。

第11项已经完成：`process-v1`精确冻结`process/start|read|write|terminate`并排除`process/signal`，组合profile另只增加`agentx/fs/readFileBlock`；`executor-mcp/1.0`最初只广告`list_environments|shell`，当前`executor-mcp/1.1`在完整handler装配后加入`read_file`；agentx WSS拥有JSON Schema/AsyncAPI机器契约。Go reference kernel实现双向独立sequence、非sequenced ACK、generation fencing、同gateway进程30秒resume、bounded frame/receive journal和`mutationKey + request hash`的pending/completed/ambiguous门禁；运行时validator还逐method严格校验process request/notification、bounded filesystem request与`network/policyRequest`参数。stable 0.146.0源码复核确认clean-env wire、managed sandbox字段和`windowsSandboxLevel=restricted-token`枚举，contract/race门禁将继续防止schema与实现漂移。

第12项目前已完成connection kernel、真实WSS路由，以及独立agentx仓库中的connector/runner IPC、远端lifecycle、registered-root/cwd本地复核、monotonic timeout signal、每process独占的stock `codex exec-server --listen stdio --strict-config`监管和一次性fs-only bounded-read lane；真实stock纵向门禁已通过。本仓已完成online environment registry、三工具stateful MCP链、七个execution/operation mTLS command/client，以及shell-v1和read-file-v1两条`Prepare → Begin → dispatch → ACK/unknown → operation/execution terminal → MCP result`链；开发模式的ask approval command和consume-before-dispatch也已接入。Core/Hydra/gateway真实enrollment与key binding、gateway production入口，以及独立agentx的owner-only双钥、RFC 7523、per-connection challenge proof、WSS重连、runner credential isolation、Linux filesystem safe-open、cgroup v2 process-tree containment和`5d40b6b` runtime pinned-exec现已实现。gateway进程丢失后的unknown/aggregate恢复审计、startup fail-closed及hard-kill矩阵也已完成；部署manifest和真实跨仓拓扑仍未完成，因此尚不能作为Phase 2生产交付证据。

Phase 3当前从第13项开始：已有run/attempt/event component API、独立harness-pool workload identity、原子双lease续期、`run.queued`专用long-poll delivery、pool侧单项claim controller内核、brain catalog冻结/线程绑定core API、签名manifest launch-preparer、受限私钥装载/公钥轮换keyring、deployment profile resolver、由`CreateRun`原子持久化并由live attempt fence保护的core-backed launch-state source，以及带有界并发、lease心跳、thread/turn顺序门和dispatch清理语义的常驻监督骨架；checkpoint恢复会复用原thread绑定catalog。per-attempt control 1.3机器合同、同holder进程resume journal、双向control server/client、mTLS+bearer入口、具体`ControlAttemptSupervisor`、本地process launcher、v2 closed-world bootstrap/runtime-capability合同、prompt object双端流式校验、checkpoint v1确定性artifact、FD 5双端对象校验及worker安全恢复入口也已完成。真实app-server child/final-exec、fresh runtime、exact-SPIFFE executor MCP、notification/progress→canonical event以及approval request/outcome链现已装配进可运行的one-shot `cmd/harness-worker`，production本地清理权限也改为启动前fail-closed。completed terminal现在携带经过`CODEX_HOME`词法包含校验的rollout locator；本地workload又把“进程组已停止”和“runtime已删除”拆成两个边界，并从启动前固定的attempt目录FD以Linux `openat2(BENEATH|NO_SYMLINKS)`及expected app UID/GID安全暴露唯一rollout。pool finalizer现已流式生成并验证artifact、以同一identity精确重试上传/commit、完整核对Core结果，并在连续歧义时保留attempt runtime；fresh/resume catalog authority均有PostgreSQL路径覆盖。常驻`cmd/harness-pool serve`已装配production capability source与encrypted object store，显式`--insecure-dev`才选择开发HMAC/本地对象；开发路径仍由真实dispatch集成测试覆盖到worker bootstrap与有界release。`cmd/agentserver-dev prepare --insecure-dev`又把同一runtime authority派生为不可覆盖的开发PKI、secrets、Core bootstrap、worker deployment、分服务env和无secret agentx argv，并通过所有现有配置loader；开发attempt anchor的app traversal mode矛盾也已修正。供应商无关生产对象格式、Core/pool内部adapter、S3/KMS参考provider、共享runtime factory、三项production服务入口、capability签发/消费、agentx production connector、Linux containment及runtime pinned-exec均已完成；后续仍须实现真实provider/IAM integration和生产部署门禁。开发命令和library测试都不能替代这些生产边界。

Phase 4已完成canonical run-event schema/AsyncAPI、AG-UI SDK pin、A2UI v0.9 display builders、严格projector、public CreateRun/event cursor/cancel/approval-decide、Hydra token introspection、HMAC cursor、真实core backend及browser-gateway command/health/config；worker↔pool control中的app-server notification/MCP progress也已成为core committed canonical event并接入AG-UI投影。显式cancel覆盖`queued → cancelled`和有holder时`cancelling → cancelled`两阶段收口；heartbeat在workload cleanup期间保持，pre-turn `AbandonAttempt`又在run锁内仲裁startup failure与并发cancel。migration 0012及Core commands提供`pending → approved|denied|expired|cancelled → consumed`两阶段authority，executor-gateway per-tool `ask`、真实MCP elicitation、harness-control 1.3 decision/主动expiry/cancel及consume-before-dispatch均已完成。migration 0013与Core login bridge又加入一次性login/consent状态、AES密封、真实外部OIDC验证和active identity mapping；browser-gateway/SPA只使用同origin Code + PKCE，URL不再携带bearer。开发smoke先验证登录callback/consent/code重放失败，再按五个独立run覆盖批准执行、执行后取消、用户拒绝、数据库时钟过期和pending时取消；包含该链的新pinned Linux镜像已在全新状态卷及同卷复跑通过。harness-control live write和resume replay的ACK单调性已有确定性阻塞写、重复与race门禁；control/MCP/gateway四条进程级断线故障测试也已在同一pinned Linux arm64环境各连续通过20次。Phase 4据此满足退出条件。Phase 5已完成供应商无关对象格式、Core/pool内部adapter、S3/KMS参考provider、production capability wire/keyring合同、Core issuance/live-authorize、三项consumer生产入口、Core/Hydra/gateway executor enrollment与WSS机器证明，以及agentx生产credential/connector、Linux runner降权、filesystem safe-open、cgroup v2 process-tree containment与runtime pinned-exec；真实provider/IAM、Kubernetes清单和全拓扑部署门禁仍是必做项。当前plaintext对象目录、共享开发HMAC、legacy fixture bearer和scripted Responses只提供联调能力，仍不等于生产可用。

## 14. 尚未锁定但有明确决策点的事项

以下事项不能在实现中静默默认：

1. 具体stock Codex release/tag：stable 0.146.0是当前修订门禁candidate，只有A07–A08及目标平台E09全部通过后才写production manifest；E03/E07 reference adapter已对上述macOS artifact通过，真实agentx兼容仍在Phase 2验收。
2. macOS production agentx隔离方式：必须通过 signed launchd/Keychain/ptrace/FD gate；否则只标 dev。
3. 参考provider已由[`ADR 0001`](adr/0001-aws-reference-object-provider.md)锁定为AWS SDK v2的S3-compatible + AWS KMS窄adapter；实际生产endpoint、workload identity/IAM和服务供应商仍必须在部署档案中显式选择并通过同一兼容门禁。
4. 外部 OIDC IdP claim mapping：`(issuer, subject) → active local user`和每次敏感操作实时workspace membership复核已经固定；生产组织claim、自动provisioning/邀请与identity-linking策略仍需单独确定，当前实现不会静默创建用户或成员关系。
5. Phase 2 多副本 executor owner routing：只有业务 SLO要求时启动，不能混入 Phase 1。

这些都不阻塞v2 module和Codex conformance/reference bridge建设；实际Codex pin仍是进入production runtime前必须完成的选择。
