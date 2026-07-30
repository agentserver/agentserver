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
4. 再打通 harness 垂直切片：harness-pool → per-run worker（dynamicTools/MCP bridge）→ stock app-server stdio，并由worker调用executor MCP。
5. 最后接 browser-gateway、AG-UI/A2UI、Hydra 与完整审批。
6. 用故障注入、安全隔离测试和 Kubernetes 部署门槛完成收口。

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

在这条链路通过前，不并行建设完整前端、managed executor、warm process pool 或多副本 executor owner routing。

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
| workload | Kubernetes Job，backoffLimit=0、restartPolicy=Never | 是否创建新 attempt 只能由 core/harness-pool 决定，不能交给 Job 自动重跑 |
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
│     ├─ canonical-event.schema.json
│     ├─ harness-control.schema.json
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

A06旧probe证明标准MCP elicitation协议本身可工作，但它由stock app-server充当MCP client，新架构不依赖该路径。`internal/harnessworker`现已实现reference bridge-side子门禁：worker使用official Go MCP SDK建立有界连接、分页读取并逐字节核对冻结catalog，收到dynamic call后发唯一一次`tools/call`；fake gateway在该调用内发`elicitation/create`，worker校验execution `_meta`与可信run/call/generation/catalog关联，并把`accept|decline|cancel`原样回给gateway。该测试不经过app-server，也验证未批准分支零dispatch。它尚未接入真实pool/core，因此A06仍未关闭：还必须覆盖core主动TTL expiry、nonce单次消费、stale generation、真实control/MCP断线和持久化approval CAS。pending期间app-server只持有原dynamic callback，看不到elicitation；gateway的active execution deadline由自身状态机暂停，不能依赖Codex `tool_timeout_sec`。

reference client显式锁定MCP `2025-11-25` stateful Streamable HTTP profile。当前official Go SDK v1.7.0的新`2026-07-28` stateless profile不能在`tools/call`内承载本设计使用的server-originated `elicitation/create`，所以连接若协商到其他版本会在`tools/list`前fail closed；其他版本的标准`_meta`也不能被静默忽略。未来升级必须先更换approval transport设计并增加独立conformance，不能随SDK latest漂移。

A07的dynamic probe已在stable 0.146.0和0.147.0-alpha.2上固定关键wire事实：pending `item/tool/call`时`turn/interrupt`成功并产生`interrupted` terminal，不发第二次模型请求，也没有`serverRequest/resolved`；正常dynamic response同样没有resolved。reference `DynamicBridge`按request type维护有界outstanding set，`AppServerRunner`则已把它接入一条one-shot Codex wire事件循环：唯一reader pump只向主循环交付消息，生命周期request、interrupt和dynamic response全部由主循环这个唯一writer串行写入；callback request id按JSON值去重，result必须claim后写入，只有write成功才`ResponseWritten`，partial/unknown write直接终止attempt且不得重调MCP。runner对notification使用非阻塞有界sink，缓冲满、未知server request、MCP失败或caller cancel都会cancel turn callbacks、发唯一`turn/interrupt`并在固定grace内等待匹配terminal；`thread/resume`在第一字节stdio I/O前核对checkpoint catalog digest且绝不发送`dynamicTools` override。net.Pipe wire fixture与race测试已覆盖上述路径，stock app-server live gate也已在macOS arm64 stable binary SHA-256 `ae1d3ffe6d48aec6a4dc3f50e7eb8e0d11962485a6a9406c5a7012139383da02`、size `271056976`上通过。新的非live组合测试已用official SDK和真实HTTP session贯通`AppServerRunner → DynamicBridge → MCPClient → gateway`，并拒绝bearer出现在任何app-server wire frame。强制断开in-flight MCP HTTP后，runner会interrupt、收到匹配terminal并清空callback；`MCPClient.Close`现跟踪所有HTTP request，先等有界graceful grace，超时后abort私有transport并有界返回forced-close error，不再卡死于SDK的session DELETE。健康连接上的外层取消仍会在approval expiry前抵达gateway并退出嵌套elicitation handler；但已断transport无法把cancel送到已dispatch的远端handler，gateway必须以自身connection grace、execution deadline和unknown/terminal状态机收口。A07仍未完全关闭：已dispatch execution独立收口、真实control断线和gateway断线deadline/unknown转移仍待完成。

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
| E04 | filesystem | read/open/readBlock/close/canonicalize 等允许方法与 pinned schema一致 |
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

stable 0.146.0 的 `linux-arm64` 正向 image gate 已在 native Apple `container` Linux VM 中通过：scratch image 以 uid/gid 65532、只读 root、零 capability、无网络运行，runtime bundle 只有锁定的 `bin/codex` 与 `codex-resources/bwrap`，不存在 package metadata 或 compatibility shim。门禁先确认 bundled bwrap 的 `--argv0` 语义，再通过 verified launch plan 发出真实 read-only 与 workspace-write `process/start`；前者可读 fixture，后者只可写声明的 workspace，同一 writable `/tmp` 上的 sibling path 被拒绝，ambient poison bwrap 未执行，运行时生成的 Linux sandbox alias 解析回同一份已验证 Codex。因此 E09 的这一精确 release/architecture/artifact set 已关闭；`linux-amd64` 仍须在 native amd64 worker 跑同一 target。Apple Silicon 的 amd64 仿真会重写 inner argv0 并拒绝 seccomp filter，不能作为门禁证据；最终 agentx 安装路径的 immutable safe-open/exec TOCTOU 仍是独立实施项。stdio EOF 会关闭唯一 connection、shutdown session 并回收 managed child，不能把它描述成可 detach/resume。

E10 已在 alpha.14 与 stable 0.146.0 上固定完整 stock bound matrix：stdio payload 恰好 64 MiB 可接受，增加一个 byte 会断开整条连接；JSON 恰好 262,144 个 value 可接受，第 262,145 个 value 只产生 message-scoped `-32600`，连接仍可继续；retained output 为每进程 1 MiB 并另有 50,000 chunk 上限；stdin dedupe 为每进程 4,096 个 write ID 的 FIFO；`process/closed` 后约 30 秒仍可 read，随后返回 unknown process 且同一 ID 可复用。`ExecParams`、`prepare_exec_request` 和 spawn 路径没有独立 argv/env count 或 byte guard，只有 transport 与宿主 process API 限制，因此本机 `E2BIG` 不是 wire bound。live negative control 也证明 stock 会接受超过产品上限但仍低于宿主上限的 argv/env。

runtime manifest 因而分别保存 `execServerBounds` 与更小的 `agentxLimits`。首版 agentx 必须在转发前拒绝：inner frame 大于 8 MiB、JSON value 多于 65,536、argv 加可选 arg0 多于 256 项或 UTF-8 总计大于 16 KiB、最终物化且不继承的 env 多于 256 项或按 `name=value` 总计大于 16 KiB、write ID 大于 128 bytes；每进程 WSS delivery/resume raw-output buffer 为 8 MiB，溢出必须报告带 sequence range 的 `output_gap/buffer_overflow`。stock 约 1 MiB replay 不能替代该 buffer，也不能恢复已经溢出的外层序列。最坏响应无法装入较小 envelope 的 method，在具有请求级上限或分页协议前不得协商。reference input validator 已覆盖 argv/env/write ID 的每个恰好边界与第一个拒绝；真实 agentx 仍须在 Phase 2 compatibility suite 复用同一 fixture 证明 frame/JSON/input/output 限制执行在写入 child stdin 或耗尽 buffer 之前。

stable 0.146.0固定了两项产品profile输入。第一，`process/signal`对missing、delivered、already-exited都返回不可区分的`{}`，所以修订E03在outer schema中排除该方法，只验已证明的stdin/terminate。第二，root退出但descendant持有pipe时不会`closed`，`process/terminate`也不会杀该descendant，直到整条stdio connection关闭；因此修订E07要求每process独占instance并以connection shutdown做无旁路cleanup。现有负向probe长期保留；新增的reference adapter只有一个reader、重分配local request id、逐process核对ownership，单instance拒绝第二次start，并在转发前拒绝signal、foreign process和超限writeId。fake-wire/race gate覆盖正常closed、forced cleanup、核验失败转unknown及双instance无连带；stock live gate又在exact stable 0.146.0 macOS arm64 binary（SHA-256 `ae1d3ffe6d48aec6a4dc3f50e7eb8e0d11962485a6a9406c5a7012139383da02`、size `271056976`）上同时运行两个真实stdio instance：第一条root crash后只关闭自己的connection并确认descendant消失，第二条仍存活且可独立terminate/closed。E03 outer-profile与E07 reference composition因而对该artifact关闭；真实agentx的WSS兼容、平台containment和全量bounds仍属于Phase 2。当前A03/A04/A05/A09/A11/A12已按dynamic架构关闭；A06–A08仍开放。E09 `linux-amd64` native gate、真实agentx bounds enforcement、E05 ownership/approval与审计也仍未完成。

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
| run_attempts | id、run_id、generation、status、turn_started_at、holder_id、version；unique(run_id,generation) |
| session_leases | session_id PK、run_id、holder_id、generation、expires_at |
| attempt_leases | run_attempt_id PK、holder_id、generation、expires_at |
| run_events | run_id + seq PK、event_id unique、producer key unique、source、schema_version、inline payload或完整object pointer metadata |
| outbox | id、kind、aggregate_id、payload、available_at、lock_owner、lock_until、attempts（claim generation）、completed_at |
| checkpoints | id、session_id、run_id、attempt_generation、thread_id、turn_id、manifest hash、object_id、Codex build |
| brain_tool_catalogs | id、session_id、thread_id nullable unique、contract version、canonical catalog bytes/object id、catalog digest、created policy context；新thread启动前冻结，成功后CAS绑定thread_id，同一thread不可更新schema |
| executions | id、run_id、tool_call_id、tool/schema/policy/mapper version、arguments hash/ciphertext、operation_plan_hash、status、version |
| execution_operations | id、execution_id、ordinal、kind、effect_class、mutation_key unique、params hash、status、connection generation、timestamps、version |
| approvals | id、execution_id、context hash、nonce unique、status、expires_at、requester、approver、version |
| executors | id、workspace_id、status、machine key、protocol/build metadata、version |
| executor_environments | id、executor_id、root descriptor、owner policy digest、status、version |
| executor_connections | executor_id PK、generation、session_id、gateway_instance_id、expires_at、build/schema digest |

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
- CompleteExecution
- BeginRunFinalization
- CommitCheckpointAndTerminalRun
- InterruptAttempt
- CancelRun
- FenceExecutorConnection

每个命令以单个 PostgreSQL transaction完成全部状态、事件与 outbox 写入。禁止 handler 先写状态、提交后再“顺便”写事件。

PR 9 reference command store固定以下首批语义：

- `CreateRun`先锁session，按`(workspace_id, actor_id, session_id, idempotency_key)`查重；相同request hash返回原run，不同hash返回`idempotency_conflict`。只有没有active run且expected session version匹配时，才原子写run、`run.queued` event/outbox并设置`active_run_id`。
- `ClaimQueuedRun`把`queued`推进为`starting`并创建attempt、session lease和attempt lease。两条lease都使用数据库时钟；任一仍存活时不能换holder。只有尚未接受turn、attempt仍为`leased|starting`且两条lease都过期时，才可fence旧attempt并以更高generation重领；mid-turn禁止自动重领。
- `RenewSessionLease`与`RenewAttemptLease`都要求另一条同holder/generation lease仍存活，避免只续住半条lease后无限阻塞reclaim。
- `MarkTurnAccepted`是不可逆的mid-turn边界：原子推进run/attempt为`running`、记录`turn_started_at`并写event/outbox。提交后的同holder重试返回原结果；不同holder或旧generation失败。
- `AppendAttemptEvents`一次最多256项、inline JSON object最多64 KiB；同一batch只允许一个producer且producer seq严格递增。新事件要求live双lease和当前generation；exact producer-key重试即使随后被fence也只返回原run seq，不写新行，而同key不同内容返回`event_conflict`。
- outbox一次最多claim 100项，使用`FOR UPDATE SKIP LOCKED`。每次claim增加`attempts`，consumer必须携带`owner + attempts`完成或释放；旧claim即使owner字符串复用也不能完成新generation的工作。

0001已经发布到migration catalog，不能为状态命名修订其checksum。0002因而以前向migration把临时的run状态`claimed`改为架构定义的`starting`，并加入event source与object size/media type。PR 8期间尚无产品runtime可合法写event；如果数据库里存在手工插入的旧run_events，0002明确失败并保持version 1，不猜测其source或伪造object metadata。

### 5.4 operation 是真实副作用边界

一次 MCP execution 可能包含多个 operation：

| MCP 工具 | operation 示例 |
|---|---|
| shell | process_start；必要时 timeout_terminate |
| write_stdin | process_write |
| terminate | process_terminate |
| read_file | fs_read（effect_class=read，可按策略安全重试） |
| apply_patch | fs_read；fs_write_if_match |

每个 operation 都有独立 mutation_key。发送前顺序固定：

1. PrepareOperation 写入 prepared。
2. gateway 校验 live lease、generation、RBAC、approval context。
3. BeginOperationDispatch 以 CAS 写入 dispatching。
4. transaction commit。
5. 才能向 agentx 发送。
6. agentx mutation journal 返回 accepted/pending/completed。
7. core 写 acknowledged/terminal。

gateway 在步骤 4 后、步骤 6 前崩溃时，operation 默认 unknown。只有 agentx journal 或 stock child 的可信 terminal event 能把它收口；不能因为“没看到 ACK”就重发。

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
GET  /internal/v2/run-attempts/{attemptId}/toolCatalog

POST /internal/v2/executions:prepare
POST /internal/v2/executions/{executionId}/operations:prepare
POST /internal/v2/operations/{operationId}:beginDispatch
POST /internal/v2/operations/{operationId}:ack
POST /internal/v2/operations/{operationId}:complete

POST /internal/v2/capabilities:issue
POST /internal/v2/capabilities:introspect
POST /internal/v2/executor-connections:acquire
POST /internal/v2/executor-connections/{executorId}:renew
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

registry 只保存活连接。权威 executor/env/generation 在 core。gateway 重启后旧 resumeSessionId 一律拒绝，不能仅凭 DB session id伪造 frame journal仍存在；prepared operation保持未发送，dispatching/acknowledged且无可信终态的 operation才进入 unknown。

### 7.2 第一批工具

按以下顺序实现：

1. list_environments：只读 core registry，不连接 agentx。
2. shell：固定 argv[]、cwd、env policy、timeout、tty。
3. read_file：验证 path/root 后映射 stock fs read。
4. read_output、write_stdin、terminate：绑定已有 process ownership。
5. apply_patch：只有 fsWriteFileIfMatch 扩展和跨平台 CAS 测试通过后才暴露。

unified_exec、跨 run detached process、任意 http/request 和 capabilityRoots不进入第一个 slice。

### 7.3 shell 映射

~~~text
MCP shell
  → PrepareExecution
  → policy allow/ask/deny
  → PrepareOperation(process_start)
  → BeginOperationDispatch
  → WSS rpc process/start
  → agentx ACK
  → operation acknowledged
  → process/output / process/exited / process/closed
  → operation terminal
  → execution terminal
  → MCP result
~~~

timeout 不伪装成 process/start 参数。gateway 在启动进程时同时预分配 timeout_terminate operation/mutation key，并把计时策略交给 agentx；gateway timer 与 agentx 本地 monotonic timer触发的是同一个预分配 terminate语义，任何一侧都必须等待真实 process terminal。

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
10. 远端hello/initialize成功后才宣布env online。每个业务`process/start`随后重复步骤4–8，分配新的`local_exec_instance_id`，并在该instance拒绝第二个`process/start`；fs请求使用独立lane。

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

- 短时网络断开且原gateway进程仍存活：相同exec_session_id、generation、sessionSeq/ACK恢复，agentx保留全部活跃stdio instances。
- gateway进程重启：resume_rejected；prepared operation保持未发送，core中未证实终态的dispatching/acknowledged operation转unknown；agentx在grace后逐个关闭stdin并回收各instance。
- agentx进程重启时全部旧process handle失效；单个stdio child重启只使对应`local_exec_instance_id/process_id`失效，其他instance继续。

跨 pod resume、durable frame journal和 owner routing属于 Phase 2。

## 8. Harness 垂直切片

### 8.1 harness-pool controller

controller 循环：

1. 通过 core long-poll claim queued run。
2. 获取 session lease与 attempt lease。
3. 从core读取已冻结的brain tool catalog/canonical digest（新thread先执行`FreezeBrainToolCatalog`），生成不可变、签名run manifest；`controller_callback`绑定当前holder instance直连地址。
4. 创建 per-attempt ConfigMap/Secret、NetworkPolicy和 Job。
5. 接受 worker mTLS control stream并核对 attempt/generation。
6. 续租、提交事件、转发 cancel/fence/approval。
7. 接收 checkpoint chunks、复算 hash并上传对象存储。
8. 请求 core原子提交 checkpoint + terminal。
9. 删除 Job和临时对象。

Job backoffLimit=0。worker容器崩溃后 Kubernetes不自动重启相同 attempt。

Phase 1 的 worker control resume也只覆盖原 harness-pool holder进程仍存活的短时断线。worker直连 manifest中的 holder实例，不经普通 Service随机换 pod；holder崩溃、callback不可达或 lease过期后，worker在 grace内 interrupt并退出。若 turn尚未被 app-server接受，core可创建新 attempt；否则 run进入 interrupted。跨 controller接管现有 worker需要独立 owner-routing设计，不在首版承诺。

### 8.2 run manifest

manifest至少冻结：

- workspace/session/run/attempt/generation；
- prompt object id/hash；
- previous checkpoint id/hash；
- Codex runtime manifest digest；
- model/llmproxy audience和 endpoint；
- executor MCP endpoint/TLS identity/audience与显式protocol profile，以及冻结的namespace/description、tool name/description/input schema、固定`deferLoading=false`、逐tool hash与catalog digest；
- executor/env/tool policy；
- max_run_duration、max_approval_ttl、gateway active execution timeout、MCP transport/cleanup grace；
- event/control buffer上限；
- checkpoint allowlist version；
- worker image digest和 expected service account。

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

worker先把checkpoint allowlist恢复到全新`CODEX_HOME`，断言staging严格匹配manifest，再写入本attempt新config；checkpoint无权覆盖配置。对当前已验证build，allowlist就是该brain thread的单个rollout JSONL，SQLite/WAL/SHM均不恢复。worker随后用自身credential初始化executor MCP，规范化`tools/list`并与manifest/catalog digest逐字节比较；不一致则在启动turn前失败。最后以manifest中的绝对路径启动`codex app-server --listen stdio:// --strict-config`。strict-config只拒绝未知字段，不能替代tool capture和OS/网络隔离。

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
4. 向 core写 BeginRunFinalization。
5. 关闭 app-server stdin。
6. 等待 child在固定 grace内正常退出。
7. timeout时先 TERM、再 KILL；该 attempt不得提交 checkpoint。
8. child正常退出后按 pinned allowlist安全打开 app-server返回的 rollout文件，拒绝 symlink和路径逃逸；当前 build每个 brain thread只允许这一项，任何额外文件都拒绝。
9. 生成逐文件hash和manifest，并记录冻结tool catalog digest。
10. 经 control channel分块发送给 pool。
11. pool上传、复算整对象 hash。
12. core以 expected run/attempt version提交 checkpoint pointer和 terminal event。
13. 收到 commit ACK后 worker/pool才删除 staging。

如果用户已经看到完整模型输出但 checkpoint最终无法提交，run进入 interrupted(checkpoint_commit_failed)，不能标 completed。用户可看到输出，但系统必须明确该 session不能从该 turn原生恢复。

## 9. Browser、AG-UI 与审批

browser-gateway只做：

- 验证用户 bearer的基本格式/受众后委托 core authorize；
- 创建 run并获取 run handle；
- 读取 canonical event cursor；
- 映射为 AG-UI/A2UI；
- fetch streaming与 backpressure；
- cursor_expired snapshot/rebase。

它不保存 session、run或 approval事实。浏览器断开只停止投影，不调用 cancel。

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

Hydra login/consent bridge和完整 Web UI放在 executor+harness主链稳定后实现，避免身份 UI掩盖运行状态机问题。

## 10. 部署与安全

### 10.1 Kubernetes

- core可多副本；所有写入经 PostgreSQL CAS。
- browser-gateway可多副本且无状态。
- harness-pool可多副本；每个 claim有 holder/generation lease。
- executor-gateway Phase 1 replicas=1。
- harness Job每 attempt一个，restartPolicy=Never、backoffLimit=0。
- worker与 app-server使用固定的不同 UID/文件权限域；child禁止 ptrace/process-vm和读取 worker `/proc`状态。
- rootfs只读；CODEX_HOME与 staging位于有配额 tmpfs/emptyDir；child mount view不含 workspace或 service-account token，worker-only staging依靠不同 UID和 `0700`目录不可读。
- 只有 init-network-guard持有短期 `NET_ADMIN`并安装按 UID默认拒绝的 nftables OUTPUT规则；runtime worker/app-server都丢弃 `NET_ADMIN/NET_RAW`。
- final-exec trampoline只保留 stdin/stdout/stderr，fd 3以上 close-all；worker control/credential FD同时必须为 `O_CLOEXEC`。
- app-server没有 Kubernetes API token。
- worker service account/workload identity只能连接harness-pool并换取受众绑定的executor MCP capability；不能访问对象存储或其他内部服务。
- harness-pool拥有创建/删除目标 Job和上传 checkpoint的最小权限。

### 10.2 网络

- Pod NetworkPolicy只限制worker+child destination并集，不能冒充进程隔离；按UID的OUTPUT规则允许worker到harness-pool+executor-gateway，app-server只到llmproxy。
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
- Job create前/后；
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
- per-run Job与 worker；
- stock app-server stdio；
- Codex MCP deny-all、冻结dynamicTools和worker MCP bridge；
- canonical model/MCP events；
- finalizing/checkpoint commit。

退出条件：scripted model完成 shell调用和第二 turn native resume；mid-turn crash不重跑。

### Phase 4 — Browser 与 approval

交付：

- browser-gateway；
- AG-UI/SSE cursor/rebase；
- A2UI execution/approval卡；
- Hydra Code + PKCE login/consent bridge；
- MCP elicitation完整闭环；
- 显式 cancel。

退出条件：浏览器断线/重连不取消 run，approval TTL/cancel/断线全部 fail closed。

### Phase 5 — Hardening 与生产门槛

交付：

- 未来第三方MCP所需的内部policy/egress proxy（Phase 1 executor-only不依赖外部MCP）；
- K8s security context/NetworkPolicy；
- agentx平台隔离；
- chaos、fuzz、secret scan；
- SLO/告警/runbook；
- Codex/agentx升级兼容流程。

退出条件：ARCHITECTURE.md Phase 0 gate与本文件安全/故障矩阵全部有自动化证据。

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

当前第9项已经通过真实PostgreSQL并发与race门禁；下一实现切片是第10项`execution_operations + crash-injection store tests`。这只表示run/lease/event/outbox状态内核可继续承载execution状态机，不表示Phase 1整体完成或服务已经可部署运行。

## 14. 尚未锁定但有明确决策点的事项

以下事项不能在实现中静默默认：

1. 具体stock Codex release/tag：stable 0.146.0是当前修订门禁candidate，只有A06–A08及目标平台E09全部通过后才写production manifest；E03/E07 reference adapter已对上述macOS artifact通过，真实agentx兼容仍在Phase 2验收。
2. macOS production agentx隔离方式：必须通过 signed launchd/Keychain/ptrace/FD gate；否则只标 dev。
3. KMS与对象存储供应商：接口固定为 envelope encryption + S3-compatible，部署实现需单独 ADR。
4. 外部 OIDC IdP claim mapping：Hydra bridge实现前需确定 issuer/sub、组织和 workspace映射规则。
5. Phase 2 多副本 executor owner routing：只有业务 SLO要求时启动，不能混入 Phase 1。

这些都不阻塞v2 module和Codex conformance/reference bridge建设；实际Codex pin仍是进入production runtime前必须完成的选择。
