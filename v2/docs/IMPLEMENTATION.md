# agentserver v2 — 实施设计

> 状态：实施基线（draft）
>
> 上位设计：ARCHITECTURE.md
>
> 本文回答“按什么顺序、以什么代码边界、通过什么验收门槛把 v2 做出来”。架构语义以上位设计为准；本文发现的实施级歧义已经同步回 ARCHITECTURE.md。

## 0. 实施结论

v2 不适合按组件横向铺开后再联调。正确顺序是先验证最可能推翻架构的 stock Codex 假设，再建设 core 状态内核，随后分别打通 executor 和 harness 两条纵向链路：

1. 建立 Codex conformance lab，锁定 stock release、wire schema、MCP-only、elicitation 和 checkpoint。
2. 实现 core 的 run、attempt、lease、event、execution operation 和 outbox 状态内核。
3. 先打通无模型的 executor 垂直切片：executor MCP → gateway → WSS → agentx → stock exec-server stdio。
4. 再打通 harness 垂直切片：harness-pool → per-run worker → stock app-server stdio → executor MCP。
5. 最后接 browser-gateway、AG-UI/A2UI、Hydra 与完整审批。
6. 用故障注入、安全隔离测试和 Kubernetes 部署门槛完成收口。

首个端到端目标不是完整 Web 产品，而是一条可证明不会隐式重放副作用的命令链路：

~~~text
scripted model
  → stock app-server
  → executor MCP shell(argv[])
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
| MCP | pinned 官方 Go MCP SDK | executor-gateway 提供 Streamable HTTP MCP；版本由 Phase 0 spike 固定 |
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
│  ├─ core/
│  │  ├─ workspace/
│  │  ├─ session/
│  │  ├─ run/
│  │  ├─ event/
│  │  ├─ execution/
│  │  ├─ approval/
│  │  ├─ executor/
│  │  └─ capability/
│  ├─ store/
│  │  ├─ migrations/
│  │  ├─ query/
│  │  └─ sqlc.yaml
│  ├─ browsergateway/
│  ├─ harnesspool/
│  ├─ harnessworker/
│  │  ├─ appserver/
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
      "helpers": {
        "codex-linux-sandbox": {
          "path": "bin/codex-linux-sandbox",
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
2. inner runtime manifest 对每个平台记录固定 HTTPS 下载来源、bundle 内相对路径、精确大小、二进制 SHA-256 和所需 helper；拒绝 symlink、路径逃逸、未知字段和临时 query URL。manifest 本身及外层 agentx/harness release bundle 使用 detached signature，外层 release metadata 记录签名、SBOM 与 manifest digest，避免在被签名文件内部产生自引用签名。
3. 运行 codex app-server generate-json-schema，保存原始生成物；已验证 stock generator 的合并 schema 可能随机排列 JSON object keys，因此 manifest 使用显式版本化的 `canonical-json-tree-v1`：逐文件解析 JSON、保留数组顺序、按 key 重编码 object，再按相对路径树 hash。连续生成两次的 canonical digest 必须一致；不能 pin 不可复现的 raw tree digest。
4. exec-server 没有等价的稳定 schema generator时，保存 protocol 源文件 digest、录制 fixture 和语义探针结果。
5. harness image 与 agentx release bundle 必须引用同一个 manifest digest。
6. 启动时发现 version、digest、helper 或 schema 不匹配，worker/agentx 均 fail closed。
7. Codex 升级单独提交：先更新 lock，再更新 fixture，最后通过全部 conformance；不能顺手随业务版本升级。

当前开发机显示的 Codex 版本或本地源码 HEAD 只能帮助调研，不能写成生产 lock。

## 4. Phase 0：Codex conformance lab

### 4.1 测试运行方式

conformance test 是 Go subprocess test，不依赖 v1 gateway：

- 通过 AGENTSERVER_CODEX_BIN 指向待验证的绝对路径；
- 每个用例创建独立临时 CODEX_HOME、cwd 和环境变量 allowlist；
- fake model server 捕获实际 Responses 请求并返回 scripted response/tool call；
- fake MCP server 分阶段实现并保持请求/响应上限；当前 A03/A05 fixture 支持 initialize、initialized、带可配置 annotations 的 tools/list 和 tools/call，A06 又增加了受限 SSE、由 MCP server 发起的 `elicitation/create`、独立 response POST 和原 tool result 收口，其他方法仍 fail closed；MCP progress 的 app-server投影尚未作为通过事实固定；
- child stdout 只能解析 JSONL，stderr 单独采集并做 secret scan；
- 所有 wire message 保存为 scrubbed golden fixture；
- live binary tests 与 fixture-only tests分开，普通单元测试不依赖外网或真实模型；
- CI 使用 manifest 下载并校验的 stock artifact，不使用 runner PATH。

### 4.2 app-server 必过探针

| 编号 | 探针 | 通过条件 |
|---|---|---|
| A01 | stdio lifecycle | initialize → initialized → thread/start → turn/start → turn/completed；wire 省略 jsonrpc |
| A02 | experimental gating | initialize 开启 experimentalApi 后 environments: [] 被接受；未开启时明确失败 |
| A03 | MCP-only tool surface | fake model 捕获的 tools 只有批准的远程 MCP tools；无 shell、exec、fs、apply_patch、view_image、Web、browser、computer、plan、user-input、multi-agent 或未授权的通用 MCP resource handler |
| A04 | endpoint allowlist | requirements.toml 只允许 manifest 中 MCP name + exact HTTPS identity；新增用户/项目 MCP 被禁用 |
| A05 | 无双重审批 | executor MCP tool 在 default_tools_approval_mode=approve 下不产生 app-server 通用 tool prompt |
| A06 | elicitation | granular policy 只允许 mcp_elicitations；fake MCP elicitation 能到达 app-server client并由 client 决定，never policy 的自动拒绝作为反例固定 |
| A07 | interrupt | turn/interrupt 产生 terminal interrupted，清除 pending server request，不再发新 tool call |
| A08 | graceful shutdown | turn terminal 与 outstanding reverse request清空后关闭 stdin，child 有界正常退出；rollout、SQLite/WAL 状态稳定，无固定 sleep |
| A09 | checkpoint round-trip | 从 allowlist 文件生成 checkpoint，在全新目录恢复，thread/resume 后第二个 turn保留首 turn 的模型可见 tool result |
| A10 | mid-turn crash | kill child 后不能 resume 原 turn；只能恢复上一个 terminal checkpoint |
| A11 | secret exclusion | config、requirements、token、auth、log、env dump 和临时 transport buffer 不进入 checkpoint |
| A12 | child isolation | app-server 看不到 worker mTLS credential/FD，cwd 无工作树，网络只能到 llmproxy/批准 MCP egress |

A03 不能通过“配置看起来正确”判断。测试必须检查实际发送给模型的 tool schema，并让 scripted model尝试调用一个禁止工具，确认 app-server不能执行。

A04 的 managed layer 不能通过普通临时 `CODEX_HOME` 注入。official release 固定从 Unix `/etc/codex/requirements.toml` 读取 system requirements；源码中的 `CODEX_APP_SERVER_MANAGED_CONFIG_PATH` 是 `debug_assertions` 专用测试钩子，0.146.0 official artifact 的负向 live probe 确认 release build 会忽略它。因此 A04 正向 job 必须运行在一次性 image/mount namespace：预装 exact-string HTTPS identity，配置同 URL 错名称、同名称错 URL、额外 user MCP 和 trusted project MCP，最终从 MCP bootstrap、状态和模型 tool surface 证明只有 manifest entry 启用。不得改开发机 `/etc`，也不得用 debug build 代替 stock artifact。`configRequirements/read` 可验证其实际投影的 managed 字段，但当前 response 不包含 MCP allowlist，不能单独作为 A04 证据。这个 image-level 正向 job 尚未完成，所以 A04 仍为 open gate。

A05 已在 0.146.0-alpha.14 与 stable 0.146.0 上通过。主用例把 fake executor tool 明确标为 destructive/open-world，在只允许 `mcp_elicitations` 的 granular thread 下，`default_tools_approval_mode = "approve"` 不产生任何 reverse request并直接到达 `tools/call`。正向控制只把 mode 改为 `prompt`，即可捕获 `_meta.codex_approval_kind = "mcp_tool_call"` 的 `mcpServer/elicitation/request`；回复 cancel 后 MCP server 不收到 `tools/call`。这证明测试确实能发现双重审批，也证明 `approve` 只关闭 Codex 通用 tool prompt；executor-gateway 主动发起的产品审批仍由 A06 单独验证。

A06 已在 0.146.0-alpha.14 与 stable 0.146.0 上分别通过。fake MCP 在真实模型工具调用的 Streamable HTTP response中先发标准 `elicitation/create`，等待 Codex用独立 POST回 JSON-RPC response 后才返回原 `tools/call` result。granular policy 下 app-server reverse request精确携带 thread/turn/server、typed boolean form schema和 execution `_meta`；client 的 `accept`（带结构化 content）、`decline`、`cancel` 三种决定都到达 MCP，随后正常 response路径的 `serverRequest/resolved` 先于 turn terminal，action对应的工具结果进入下一次模型请求。反例使用相同的非空 form 和 `approval_policy = "never"`，普通 collector确认没有任何 reverse request，而 MCP收到 `decline`。

A07 也已在 alpha.14 与 stable 0.146.0 分别通过。测试在 app-server client持有未决 `mcpServer/elicitation/request` 时调用 `turn/interrupt`：RPC返回成功，terminal status为 `interrupted`，pending request随后发出 `serverRequest/resolved`，fake MCP收到 `cancel`，Responses endpoint和 MCP都没有第二次调用。当前两个 release重复观察到的顺序都是 terminal在先、resolved在后，所以 worker不能在收到 `turn/completed` 时立即关闭 stdio；它要维护 outstanding server-request set，并在 terminal、set清空和 process收口三者都满足后才能结束 finalization。timeout、control-stream断线和 child crash仍是后续独立 fault probe，不能因 A07通过而视为覆盖。

当前 0.145.0 candidate 的 bootstrap probe 进一步确认：assistant 内容在 `item/completed` 上到达，而 terminal `turn/completed` 是 `itemsView: notLoaded` 的空内容终态，harness-worker 必须持续归并 item 事件，不能只保存 terminal payload。`environments: []` 能去掉 shell/fs；固定本地 model catalog，并显式关闭 Web、goals、multi-agent、orchestrator skills、user-input 及其他已知 feature 后，实际 Responses request 的工具面可收敛到仅剩 `update_plan`。但官方 `rust-v0.145.0` tag（peeled commit `25af12f7e61572b0bc18ddb1008be543b91519b0`）的 `add_core_utility_tools` 无条件注册 `PlanHandler`，该版本没有对应 config 或 requirements 开关；scripted model 调用后实际收到成功的 `Plan updated` result，client 同时收到 `turn/plan/updated`。因此 0.145.0 明确不通过 A03，不能成为 production runtime pin。

官方 `rust-v0.146.0-alpha.14` tag（commit `9d84cad281364eb7f6be75e23067b0adc5e26106`）新增真实的 `[tools.update_plan] enabled = false`。它的 A01 terminal projection 也变为 `itemsView: summary` 并携带 completed agent item；测试按 release 分别锁定该形状与 0.145.0 的 `notLoaded` 空数组，但 harness-worker 仍以归并 item 事件为内容权威。对该官方 artifact 的 A03 live probe 验证：无 MCP server 时模型工具面为空；配置 fake executor MCP 且 `enabled_tools = ["approved_echo"]` 后，批准工具能到达 `tools/call`，fake server 同时公布但未批准的 `blocked_echo` 与模型伪造的 `exec_command` 都在 Codex 路由层收到 `unsupported call`，不会到达 MCP。可是同一模型请求仍额外包含 `list_mcp_resources`、`list_mcp_resource_templates`、`read_mcp_resource`，并且调用第一个 handler 会真实发出 `resources/list`，不受 `enabled_tools` 约束。因此该 alpha 仍明确不通过 A03；Phase 0 继续停止业务组件建设，直到 stock release 能从实际 Responses tool schema 中移除这些通用 handler，或产品显式批准一项经过重新评审的架构变更。

本次 macOS arm64 candidate binary SHA-256 为 `e4ca03a3f3682647eb5aab2546647ed963354611b42a9daa332ae9d0366a1204`，官方 artifact archive SHA-256 为 `245d877dea7abc520487b5186f9e17d4fb10548f77da9ebf2b02cb3dee137d96`。这些 hash 只绑定本轮 alpha candidate 证据，不是 production runtime manifest。

随后发布的 official stable `rust-v0.146.0`（annotated tag object `be449751a978f02e5bbba886999662956c7f38f5`，peeled commit `e363b08c9175ac1cbe5893615dd2cb9ddf95043b`）已经独立跑完现有 live suite。其 A01 terminal projection 仍为 `summary`；A03 的精确 surface、批准/未批准 dispatch 和可执行 `resources/list` blocker 与 alpha.14 完全一致，因此 stable 也明确被拒绝。测试的 macOS arm64 binary SHA-256 为 `ae1d3ffe6d48aec6a4dc3f50e7eb8e0d11962485a6a9406c5a7012139383da02`，官方 npm platform archive SHA-256 为 `279ec3460c5b8068daab2a4f5bcf057483303b3595f4a24ade6ceb4d02674935`，canonical app-server schema tree SHA-256 为 `834975f055f4dc0bf25231ab23f446f4bfef63fd3f7832bc9b0c5fe8a32363bb`。它们同样只是 rejected candidate evidence，不生成 production runtime manifest。

### 4.3 checkpoint 探针算法

1. 创建全新 CODEX_HOME-A。
2. 启动 app-server，完成包含一次 fake MCP result 的 turn。
3. 收到 turn/completed 后继续排空 stdio，等待 outstanding reverse request全部 resolved，再关闭 stdin并等待 child正常退出。
4. 枚举 CODEX_HOME-A 变化，拒绝 symlink、绝对路径和父目录跳转。
5. 从候选文件中排除 config、requirements、auth、token、log 和 cache。
6. 复制候选到全新 CODEX_HOME-B，启动同一 build。
7. 调用 thread/resume，再运行第二 turn。
8. fake model 捕获第二 turn 上下文，确认第一 turn 及 MCP result 完整可见。
9. 对逐文件 hash、缺文件、额外文件、损坏 SQLite/WAL 分别做负向测试。
10. 将最小成功集合固化为与 Codex build digest绑定的 checkpoint allowlist。

如果只有打包整个 CODEX_HOME 才能 resume，Phase 0 失败；不能把敏感配置一并持久化来绕过。

### 4.4 exec-server 必过探针

| 编号 | 探针 | 通过条件 |
|---|---|---|
| E01 | stdio lifecycle | codex exec-server --listen stdio --strict-config；initialize → initialized；wire 省略 jsonrpc |
| E02 | deterministic process | argv[]、cwd URI、env、PTY/pipe、output sequence、exit/close 与 fixture一致 |
| E03 | stdin 与 terminate | write、signal、terminate 竞态有明确结果；未知 processId 返回显式非变更状态或 RPC error，不能伪装成功 |
| E04 | filesystem | read/open/readBlock/close/canonicalize 等允许方法与 pinned schema一致 |
| E05 | network reverse request | network/policyRequest 能被 agentx client allow/deny；未知 reverse method 默认拒绝 |
| E06 | stdio EOF | stdin EOF 后 server shutdown并清理 managed process；新的 agentx 不能 attach旧 child |
| E07 | child crash | process group/cgroup 回收后代；无法确认退出时结果为 ambiguous/unknown |
| E08 | environment isolation | 空 CODEX_HOME、固定 runtime cwd、清洗 env；不能读取用户 Codex auth/config或 agentx credential |
| E09 | binary/helper lock | codex 和 sandbox/fs helper digest 不匹配时启动失败，不回退到 PATH |
| E10 | bounds | 最大 frame、argv/env、output buffer、retained output 和 exited-process retention 被测量并写入 manifest |

Phase 0 的 exit criterion 是 A01–A12、E01–E10 全部可重复通过。任何 MCP-only、elicitation、checkpoint 或 stdio 假设失败，都先修改架构，不能继续写业务服务。

当前 probe 已确认但尚未构成完整 Phase 0 放行的 exec-server 事实：`process/start` response 可与早期 `process/output` 竞态，agentx 必须单消费者收包并按 request id/一基 event seq 整理；带 `maxBytes` 的 `process/read.nextSeq` 只越过本次返回的最后一个 output chunk，不保证同时越过 terminal event，不带该限制的 terminal read 才能给出 `closed` 后游标。E02 已覆盖 argv/arg0、file-URI cwd 到 host canonical path、缺省 `envPolicy` 时 child env 精确等于 request `env`、pipe 与 PTY 合流输出。E08 的当前 slice 证明隔离 `CODEX_HOME` 不读取毒化的用户 `~/.codex`，exec-server 自身持有的 sentinel credential 也不会进入缺省策略 child。E10 的当前 slice 实测 retained replay 只保留大输出最后约 1 MiB；frame、argv/env、write-id cache 和 exited-process retention 的完整 bound matrix 仍未完成。stdio EOF 会关闭唯一 connection、shutdown session 并回收 managed child，不能把它描述成可 detach/resume。

stable 0.146.0 同时新增两项明确拒绝证据。第一，`process/signal` 对 missing、delivered、already-exited 都返回不可区分的 `{}`，因此 E03 原验收失败。第二，根进程退出但后代继续持有 pipe 时，server 发出 `process/exited` 但不发 `process/closed`；随后 `process/terminate` 返回 `running: false` 且后代继续存活，直到整条 stdio connection 关闭才被回收，因此 E07 原验收失败。负向 conformance test 的 PASS 只表示稳定复现该缺口，不表示 E03/E07 放行。完整 Phase 0 目前至少被 A03、E03、E07 三项阻断。

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
| run_events | run_id + seq PK、event_id unique、producer key unique、schema_version、payload/object pointer |
| outbox | id、kind、aggregate_id、payload、available_at、lock_owner、lock_until、attempts、completed_at |
| checkpoints | id、session_id、run_id、attempt_generation、thread_id、turn_id、manifest hash、object_id、Codex build |
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

所有幂等/审批 hash先按对应 JSON Schema验证，再使用 RFC 8785 JSON Canonicalization Scheme生成字节，最后用带 domain separator的 SHA-256计算。不能直接依赖 Go map遍历、普通 json.Marshal偶然顺序或拼接字符串。hash记录 canonicalizer version；升级 canonicalizer必须走兼容迁移，不能让已有 idempotency key失效。

### 5.3 必须原子的 domain command

首批 store/service API 不是 CRUD，而是以下带 expected version 的命令：

- CreateRun
- ClaimQueuedRun
- RenewSessionLease
- RenewAttemptLease
- MarkTurnAccepted
- AppendAttemptEvents
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

### 5.6 migration

v2/cmd/agentserver-core migrate：

1. 获取固定 advisory lock。
2. 创建/读取 schema_migrations。
3. 对已应用 migration 比较内嵌 SHA-256。
4. 每个新 SQL 文件使用独立 transaction执行。
5. 写入 version/name/hash。
6. 失败即停止，不执行 down migration。

Helm 先运行 migration Job，成功后再 rollout服务。core runtime identity没有 DDL 权限时更佳。

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

1. 读取签名 runtime manifest，解析当前平台允许的绝对 codex/helper路径。
2. 校验 version、digest、文件权限和签名；失败即退出。
3. 取得机器身份并建立出站 WSS，但尚不宣布 env online。
4. 为 env 创建一次性 runtime dir和空 CODEX_HOME。
5. 构造最小 config，固定 exec-server cwd，清洗所有 secret/model/proxy变量。
6. 创建平台 process containment。
7. 启动绝对路径 codex exec-server --listen stdio --strict-config。
8. 本地完成 initialize → initialized → environment/info/status。
9. 记录 local_exec_instance_id。
10. 远端 hello/initialize成功后才宣布 online。

远端 lifecycle由 agentx处理，不重复转发给已经初始化的 stdio child。业务 RPC 重新分配 local request id；method/params按 pinned dialect转发，context只进入 agentx ownership/audit。

### 7.5 agentx 安全进程模型

stock exec-server及其命令树绝不能获得 agentx机器 credential。仅靠清洗 process/start.env 不够，因为 child可能从 exec-server父环境继承变量。

生产安装必须把 connector credential域和执行树分开：

- connector拥有机器 key、OAuth、WSS；
- runner只拥有一个预先建立的本地 IPC和目标 worktree权限；
- stock exec-server是 runner child；
- connector/runner IPC对 stock child close-on-exec；
- runner和命令树看不到 keychain、token文件、connector socket或环境；
- connector根据可信 process ownership关联 child notification；
- child crash由 containment整体 kill-tree。

Linux使用 system service身份 + 独立 runner uid/cgroup/user namespace完成首个生产实现。macOS必须通过签名 launchd/Keychain/hardened runtime隔离测试后才能标为 production；同 UID开发模式必须在 enrollment metadata中声明 insecure_dev，生产 workspace默认拒绝。平台支持不能只由“能启动命令”判断。

### 7.6 WSS resume 的准确承诺

Phase 1：

- 短时网络断开且原 gateway进程仍存活：相同 exec_session_id、generation、sessionSeq/ACK恢复，agentx保留 stdio child。
- gateway进程重启：resume_rejected；prepared operation保持未发送，core中未证实终态的 dispatching/acknowledged operation转 unknown；agentx在 grace后关闭 stdin并回收 child。
- agentx进程或 stdio child重启：新的 local_exec_instance_id；所有旧 process handle失效。

跨 pod resume、durable frame journal和 owner routing属于 Phase 2。

## 8. Harness 垂直切片

### 8.1 harness-pool controller

controller 循环：

1. 通过 core long-poll claim queued run。
2. 获取 session lease与 attempt lease。
3. 生成不可变、签名 run manifest，其中 controller_callback绑定当前 holder_instance_id和该实例的直连地址。
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
- MCP endpoint、server name、tool/schema hash和 audience；
- executor/env/tool policy；
- max_run_duration、approval/tool timeout；
- event/control buffer上限；
- checkpoint allowlist version；
- worker image digest和 expected service account。

worker验证签名后不能从 prompt、模型输出或 MCP响应修改 manifest。

### 8.3 app-server 配置

Phase 0根据 pinned schema生成两份只读文件：

- CODEX_HOME/config.toml：只包含模型 provider、批准 MCP、granular elicitation和显式关闭的工具/feature。
- /etc/codex/requirements.toml：管理员约束，精确 allowlist MCP identity、只允许 user approvals reviewer，并禁止用户/project层放宽。

关键语义必须为：

~~~toml
approval_policy = { granular = { sandbox_approval = false, rules = false, skill_approval = false, request_permissions = false, mcp_elicitations = true } }
approvals_reviewer = "user"

# 仅在所 pin release 的 schema 与 tool capture 均证明该键真实生效时加入。
[tools.update_plan]
enabled = false

[tools.experimental_request_user_input]
enabled = false

[mcp_servers.executor]
url = "https://executor-gateway.internal/v2/mcp"
bearer_token_env_var = "AGENTSERVER_EXECUTOR_CAPABILITY"
required = true
default_tools_approval_mode = "approve"
enabled_tools = ["process_start", "process_read", "process_write", "process_terminate"]
~~~

对应的 managed requirements 使用字符串 exact identity，不使用上游同样支持的 prefix/regex matcher：

~~~toml
allowed_approvals_reviewers = ["user"]

[mcp_servers.executor]
identity = { url = "https://executor-gateway.internal/v2/mcp" }
~~~

manifest/template validator 还必须独立要求 `https` scheme、规范 host/port/path，并拒绝 stdio identity、userinfo、fragment、prefix/regex matcher。Codex requirements 负责“名称 + 配置 URL 字符串”匹配，不负责 DNS、证书、redirect 或最终连接目标校验，后者仍由受控 egress proxy 执行。文件必须在 app-server 启动前位于真实 system path；release binary 不接受将 system requirements path 作为 `-c`/CLI 参数，debug-only 环境变量也不得进入生产环境。

这只是关键字段示意；完整 feature/requirements键必须由所 pin 版本的 config schema、其可投影字段的 configRequirements/read、实际 MCP bootstrap 和模型 tool capture共同验证，不能复制示例后假定生效。0.145.0 不认识上述 `update_plan` 禁用机制，因此被 Phase 0 拒绝；0.146.0-alpha.14 与 stable 0.146.0 虽证明该键和 A05 `approve` 语义真实生效，仍因通用 MCP resource handler 绕过 `enabled_tools` 而被拒绝。`required` 也只改变 MCP 初始化失败语义，不是能力 allowlist。

worker先把 checkpoint allowlist恢复到全新 CODEX_HOME，再写入本 attempt的新 config；checkpoint无权覆盖配置。随后以 manifest中的绝对路径启动 codex app-server --listen stdio:// --strict-config。strict-config只拒绝未知字段，不能替代 MCP-only tool capture和 OS/网络隔离。

thread/start、thread/resume和turn/start都显式发送 environments: []；不发送 dynamicTools和 selectedCapabilityRoots。cwd指向空、只读的非工作树目录。

### 8.4 worker 状态机

~~~text
booting
  → restoring
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
- 对 allowlist server request返回明确 response。

worker不能调用 app-server的 command/exec、process/spawn、fs、marketplace、plugin、skills或其他宿主 API。未知 server request fail closed并中断 run。

### 8.5 finalizing 与 checkpoint

收到 terminal turn/completed 后进入 finalizing，但 terminal不是 transport cleanup barrier：

1. 停止接受新的 control decision；继续排空 app-server stdout。
2. 等待本 attempt已登记的所有 server request ID收到 `serverRequest/resolved`；terminal后新出现未知 server request一律 fail closed。
3. 确认所有 execution/process已 terminal或明确 unknown。
4. 向 core写 BeginRunFinalization。
5. 关闭 app-server stdin。
6. 等待 child在固定 grace内正常退出。
7. timeout时先 TERM、再 KILL；该 attempt不得提交 checkpoint。
8. child正常退出后按 pinned allowlist安全打开文件，拒绝 symlink和路径逃逸。
9. 生成逐文件 hash和 manifest。
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

1. executor-gateway PrepareExecution冻结完整 context hash。
2. policy=ask时创建 approval。
3. MCP server在原 tool call中发起 elicitation。
4. app-server向 worker发 server request。
5. worker经 pool/core产生 canonical approval event。
6. browser提交 decide API。
7. core校验当前 RBAC、TTL、nonce、hash和 attempt generation。
8. gateway消费 approval并在 dispatch前再次校验。
9. cancel、超时、worker control断线、elicitation清理全部 fail closed。

app-server针对 executor MCP已设 approve，因此这里不会再出现第二张通用 Codex tool approval卡。

Hydra login/consent bridge和完整 Web UI放在 executor+harness主链稳定后实现，避免身份 UI掩盖运行状态机问题。

## 10. 部署与安全

### 10.1 Kubernetes

- core可多副本；所有写入经 PostgreSQL CAS。
- browser-gateway可多副本且无状态。
- harness-pool可多副本；每个 claim有 holder/generation lease。
- executor-gateway Phase 1 replicas=1。
- harness Job每 attempt一个，restartPolicy=Never、backoffLimit=0。
- worker与 app-server使用不同 UID/文件权限域。
- rootfs只读；CODEX_HOME与 staging位于有配额 tmpfs/emptyDir。
- app-server没有 Kubernetes API token。
- worker service account只能连接 harness-pool；不能访问对象存储。
- harness-pool拥有创建/删除目标 Job和上传 checkpoint的最小权限。

### 10.2 网络

- app-server只到 llmproxy和 egress proxy。
- egress proxy按 run manifest验证 DNS、IP、SNI、证书、端口、redirect、响应大小和 timeout。
- approved MCP使用不同 audience capability。
- worker control网络与 app-server egress网络分开。
- executor机器 credential在 llmproxy必须被拒绝。
- exec-server默认无 http/request capability。

### 10.3 secret

- capability只覆盖 max_run_duration + 短 grace。
- child env中只允许目标 audience的短期 capability。
- worker mTLS、对象存储和 Kubernetes credential不进入 child。
- checkpoint/event/log统一 secret scan。
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
- MCP-only、elicitation和 checkpoint round-trip；
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
- stock exec-server stdio；
- list_environments、shell、read_file；
- operation journal和 30 秒同进程 resume。

退出条件：无模型 scripted client可执行确定性 argv，四个 dispatch边界 crash不重放。

### Phase 3 — Harness vertical slice

交付：

- harness-pool claim/controller；
- per-run Job与 worker；
- stock app-server stdio；
- MCP-only config；
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

- egress proxy；
- K8s security context/NetworkPolicy；
- agentx平台隔离；
- chaos、fuzz、secret scan；
- SLO/告警/runbook；
- Codex/agentx升级兼容流程。

退出条件：ARCHITECTURE.md Phase 0 gate与本文件安全/故障矩阵全部有自动化证据。

## 13. 建议的首批 PR

1. v2 module、Makefile、CI、禁止 v1 import。
2. contract目录和 runtime manifest schema。
3. app-server stdio probe + fake model/MCP。
4. MCP-only + elicitation probe。
5. checkpoint graceful-shutdown round-trip。
6. exec-server stdio process/fs/EOF probe。
7. core migration runner +最小 session/run schema。
8. lease/event/outbox并发状态机。
9. execution_operations + crash-injection store tests。
10. agentx-wss与 executor MCP contract。
11. agentx v2独立仓库 bootstrap。
12. shell executor vertical slice。

前 6 个 PR只建立事实和门槛，不写五个服务的空壳。第 7 个 PR后才开始业务 runtime。

## 14. 尚未锁定但有明确决策点的事项

以下事项不能在实现中静默默认：

1. 具体 stock Codex release/tag：由 Phase 0结果决定。
2. macOS production agentx隔离方式：必须通过 signed launchd/Keychain/ptrace/FD gate；否则只标 dev。
3. KMS与对象存储供应商：接口固定为 envelope encryption + S3-compatible，部署实现需单独 ADR。
4. 外部 OIDC IdP claim mapping：Hydra bridge实现前需确定 issuer/sub、组织和 workspace映射规则。
5. Phase 2 多副本 executor owner routing：只有业务 SLO要求时启动，不能混入 Phase 1。

这些都不阻塞 v2 module和 Codex conformance lab；实际 Codex pin是进入 core runtime前唯一必须先完成的选择。
