# Agentserver v2 本地开发启动材料

本文描述当前可执行的 `insecure-dev` 启动边界。它用于把已经实现的 Core、browser-gateway、executor-gateway、harness-pool、harness-worker 和独立 agentx 接成同一套开发 authority，不是生产部署方案。

这里没有常驻的 Codex app-server 产品服务。harness-pool按 attempt本地 fork一个短命 harness-worker，worker再通过stdio启动 stock `codex app-server`完成模型循环；前端只连接 browser-gateway 的 AG-UI/A2UI 边界。executor侧则是 agentx监管 stock `codex exec-server --listen stdio`，只接收确定性 process/fs 指示。

## 1. 当前能准备什么

`agentserver-dev prepare` 从一份 closed-world JSON 一次性生成：

- 一枚短期开发 CA，以及 Core、browser-gateway、executor-gateway、harness-pool、harness-worker、llmproxy 的独立证书；每张 leaf 只有一个对应的 SPIFFE URI SAN，同时包含 loopback hostname/IP SAN；
- 互不相同的 run-capability HMAC key、run-event cursor key 和 Ed25519 run-manifest signing seed；
- 与 signing seed 匹配、只含公钥的 worker verification keyring；
- Core bootstrap JSON 和 harness-worker deployment JSON；
- Core、browser-gateway、executor-gateway、harness-pool 四份可由 POSIX shell `source` 的环境文件；
- agentx 的确定性 program/argv JSON；
- 外部 OIDC client secret、Core 登录事务 AES-256-GCM key、兼容旧 introspection 测试的固定 opaque browser bearer，以及只引用 secret 文件路径的 fixture launch 配置；
- objects、harness runtime、checkpoint staging 和 agentx runtime 四个本地状态目录；
- 不含 secret 值的 metadata、服务端点和明确的运行限制说明。

输出目录必须是一个尚不存在的绝对路径。命令不会 merge、覆盖或复用旧 authority；任何生成失败都会清理本次新建的不完整目录。所有目录固定为 `0700`，所有文件固定为 `0600`。CA 私钥不会落盘，生成结束后不能用这套 bundle 继续签发新身份。

run-capability HMAC 只进入 executor-gateway 和 harness-pool 环境，并由开发 llmproxy fixture 从独立 `0600` 文件加载；cursor key和登录事务 AES key只进入 Core 环境，manifest signing seed只由 harness-pool引用。外部 OIDC client secret由 Core环境和IdP fixture的独立`0600`文件共享。兼容用固定browser bearer只存在于`secrets/browser-bearer.token`，reference web和host smoke都不再读取它。harness-worker deployment只引用公钥keyring和自己的mTLS identity；agentx launch JSON、fixture config和metadata都不含任何secret值。

prepare 输入 schema 位于 `api/schema/insecure-dev-stack.schema.json`，生成的 fixture 进程合同位于 `api/schema/insecure-dev-fixtures.schema.json`。Go loader 还会检查 schema 无法表达的文件类型、权限、symlink、runtime digest、当前平台 artifact、stock Codex release、workspace containment、loopback 和身份分离约束。

## 2. 前置材料

先准备以下绝对路径：

1. 已验证的 stock Codex `0.146.0` runtime bundle和与其精确匹配的 runtime manifest；
2. 本仓编译出的 `harness-worker`、`harness-final-exec`；
3. 独立 agentx 仓库编译出的 agentx；
4. 一个已经存在、无 symlink component 的本地 workspace；
5. PostgreSQL URL，以及尚未被其他进程占用的 loopback 端口。

对已经通过本仓 native image gate 的官方 stable `0.146.0` Linux arm64 artifact，可以从解包后的 executable 生成仅限开发使用的 runtime package：

```bash
go run ./cmd/agentserver-dev runtime --insecure-dev \
  --platform=linux-arm64 \
  --codex=/absolute/codex-aarch64-unknown-linux-musl \
  --bwrap=/absolute/bwrap-aarch64-unknown-linux-musl \
  --output-dir=/absolute/new-stock-runtime
```

命令不下载文件，只接受仓库已经记录的 exact SHA-256/size，输出 `runtime-manifest.json` 与最小 `bundle/bin/codex + bundle/codex-resources/bwrap`。manifest中的 app-server schema digest、exec-server protocol source digest、release commit和行为 bounds 都来自同一 `rust-v0.146.0` 证据；protocol digest覆盖官方 peeled commit中明确列出的五个 production source文件，使用排序的 repo-relative tree record。该命令不会生成 detached signature、SBOM或 production runtime lock，输出必须继续以 `INSECURE DEV` 对待。

本仓二进制可以从 `v2/` 目录构建，例如：

```bash
go build -o /absolute/dev-bin/harness-worker ./cmd/harness-worker
go build -o /absolute/dev-bin/harness-final-exec ./cmd/harness-final-exec
```

agentx 仍从独立的 `github.com/agentserver/agentx` 仓库构建。它只监管 stock `codex exec-server --listen stdio` 并执行确定性 process/fs RPC，不运行模型，也不需要 Codex/ChatGPT 登录态。

## 3. 输入配置

配置文件含数据库 authority，权限必须为 `0600` 或更严格。示例：

```json
{
  "version": 1,
  "databaseUrl": "postgres://agentserver:development@127.0.0.1:5432/agentserver?sslmode=disable",
  "authority": {
    "workspaceId": "40000000-0000-4000-8000-000000000004",
    "sessionId": "50000000-0000-4000-8000-000000000005",
    "actorId": "10000000-0000-4000-8000-000000000001",
    "executorId": "20000000-0000-4000-8000-000000000002",
    "environmentId": "60000000-0000-4000-8000-000000000006",
    "agentxVersion": "0.1.0-dev",
    "workspaceRoot": "/absolute/workspace",
    "displayName": "Local workspace",
    "description": "insecure development executor",
    "defaultCwd": "."
  },
  "runtime": {
    "manifestFile": "/absolute/runtime-manifest.json",
    "bundleRoot": "/absolute/runtime-bundle",
    "agentxBinary": "/absolute/dev-bin/agentx",
    "harnessWorkerBinary": "/absolute/dev-bin/harness-worker",
    "harnessFinalExecBinary": "/absolute/dev-bin/harness-final-exec"
  },
  "network": {
    "coreListenAddress": "127.0.0.1:17443",
    "browserGatewayListenAddress": "127.0.0.1:17444",
    "executorGatewayListenAddress": "127.0.0.1:17445",
    "harnessPoolListenAddress": "127.0.0.1:17446",
    "hydraIntrospectionUrl": "http://127.0.0.1:17447/oauth2/introspect",
    "llmproxyEndpoint": "https://127.0.0.1:17448/v1"
  },
  "model": {
    "name": "gpt-5",
    "provider": "llmproxy"
  },
  "policy": {
    "version": "dev-v1",
    "allowedTools": ["list_environments", "shell", "read_file"]
  },
  "harness": {
    "maxConcurrentAttempts": 2,
    "maxRunDuration": "30m",
    "maxApprovalTtl": "10s",
    "codexPermissionMode": "read-only"
  },
  "identities": {
    "workerUid": 65531,
    "workerGid": 65531,
    "appUid": 65532,
    "appGid": 65532
  }
}
```

`platform`、Codex release/commit/digest、exec protocol digest 和 checkpoint allowlist不能在这里另填一份；命令从 runtime manifest 的原始字节派生它们。当前 harness config profile只接受 stock `0.146.0`。`maxApprovalTtl` 会进入签名 run manifest，且不能超过 `maxRunDuration`；insecure-dev 用 10 秒让真实 expiry smoke 可重复完成，生产值不由这个开发配置决定。四个服务和两个 fixture 监听地址必须全部不同、端口非零且显式指向规范的 loopback host。Hydra 开发 endpoint 固定为 cleartext loopback HTTP；llmproxy endpoint 固定为 loopback HTTPS。当前确定性模型脚本会调用 `executor.list_environments`，因此 `policy.allowedTools` 必须包含 `list_environments`。

`codexPermissionMode` 是 launch source 没有提供显式 Core authority 时的 deployment fallback，使用 Codex 的三个内置 permission preset ID，默认是 v2 为了 fail-closed 选择的 `read-only`。正常的 Core-backed 用户链路以 session 为权威：新 session 固定从 `read-only` / version `1` 开始，浏览器可随时调用 `PATCH /v2/workspaces/{workspaceId}/sessions/{sessionId}/permission-mode` 修改下一轮偏好。该接口使用独立的 `expectedPermissionModeVersion` CAS，不复用也不推进通用 `session.version`。

创建 run 时，Core 在同一事务中锁住 session、核对可选的 `expectedPermissionModeVersion`，并把当时的 mode/version 冻结进 immutable run launch state；pool 再把它写入签名 manifest。因此 active run 不会被中途切换，mode 更新只影响下一轮，丢失响应后的同 idempotency retry 仍恢复原 run authority。AG-UI prompt、模型输出和 worker wire payload 都不能直接选择 mode。

Codex app-server v2 本身没有一个名为 `permissionMode` 的 wire 字段，worker 会按 pinned release 的 `thread/start` 与 `turn/start` 原生字段做机械投影。

| 值 | Codex app-server 投影 |
|---|---|
| `read-only` | `approvalPolicy: on-request`、`approvalsReviewer: auto_review`、`sandbox: read-only` |
| `auto` | `approvalPolicy: on-request`、`approvalsReviewer: auto_review`、`sandbox: workspace-write` |
| `full-access` | `approvalPolicy: never`、`sandbox: danger-full-access` |

`auto_review` 是 v2 无交互 app-server 的 reviewer 选择；它不改变 Codex preset 的 approval/sandbox 语义。相同的冻结 mode 也会传给 executor-gateway：`read-only` 保留 shell 的产品审批，`auto` 和 `full-access` 对受 backend sandbox/capability 约束的 executor 工具自动放行；deployment 中显式的 `deny` 仍优先。这样 Codex 与 AgentServer 不会对同一个 shell 操作各自再弹一层不一致的询问。文件系统、网络、身份和 Core live-authority 校验始终保留。旧 run launch row 和旧 signed manifest 如果没有 permission authority，worker 继续使用历史的 `approvalPolicy: never` + `read-only` 严格投影，gateway 也使用部署基线；deployment fallback 只服务于没有显式 Core authority 的开发或兼容 launch source。

其中 `auto` 的无交互投影等价于 Codex CLI 的 `--approve-for-me`，`full-access` 对应 Codex 的 full-access / `--dangerously-bypass-approvals-and-sandbox` 组合；配置面仍只接受上表的 canonical preset ID。

### 3.1 Session 工作目录与 workspace skills

session 的工作目录是一个 executor-backed 的逻辑绑定，而不是 harness 容器里的本地目录：

- `environmentId` 必须指向该 workspace 已注册的 AgentX executor environment；`workingDirectory` 是环境 root 下的规范相对路径，`.` 表示 root。绝对路径、反斜杠、`.`/`..` segment、空 segment 和控制字符都会被 API、数据库约束和 executor gateway 拒绝。当前 TAE Terminal API 没有逐进程 filesystem-access enforcement，因此固定 managed-CLI 环境不能被 session 绑定为通用工作目录；Core 和 executor gateway 都会 fail closed。
- 例如本机目录是 `../rtm-aihub` 时，不能把这个宿主绝对/父级路径直接写入 session。应把 executor environment 的 root 注册为 `.../projects`（共同父目录），再把 session 的 `workingDirectory` 设为 `rtm-aihub`。这样 authority 中永远没有宿主路径，也不会通过 `..` 越过 root。
- insecure-dev 场景可以用 `./deploy/insecure-dev/run.sh --workspace=/absolute/path/to/projects` 把共同父目录挂载为 `/workspace`；随后在 Browser 的工作目录控件中填入已注册的 `environmentId` 和 `rtm-aihub`。不要把 `../rtm-aihub` 直接填入 API，`..` 会被拒绝。
- 浏览器通过 `PATCH /v2/workspaces/{workspaceId}/sessions/{sessionId}/working-directory` 更新环境和路径，使用独立的 `expectedWorkingDirectoryVersion` CAS。发起下一次 Run 时，AG-UI 同时携带该版本；如果另一个标签页刚切换了目录，Core 会在同一事务中返回 `version_conflict`，不会在未确认的目录中启动新 Run。
- 例如把 session 绑定到 `rtm-aihub`：`{"environmentId":"<registered-environment-uuid>","workingDirectory":"rtm-aihub","expectedWorkingDirectoryVersion":1}`。`environmentId` 必须是该 workspace 已注册的 executor environment；不填写它时只能选择根目录 `.`（用于解除绑定）。
- Run 创建时 Core 冻结 environment version、root descriptor SHA-256、相对目录和目录版本；之后的 session 修改只影响下一次 Run。executor gateway 每次工具调用重新校验这些值，`shell` 的默认 cwd 和 `read_file` 的路径前缀都来自 frozen binding。写入只能经 `executor.shell`，并受该 Run 的 Codex permission mode 和 backend sandbox access 控制；read-only Run 不能写。
- workspace skills 不通过 stock Codex 的本地 skill 搜索或新的 MCP `skills.*` 工具注入。worker 注入受控 developer instructions，只允许 agent 在 frozen working directory 下检查固定 roots：`skills`、`.agents/skills`、`.codex/skills`、`.dsh/skills`，并且只把精确的 `SKILL.md` 当作候选说明。skill 文本和脚本都是不可信项目数据，不能提升权限或要求泄露凭证；脚本执行仍须显式经过 `executor.shell` 和当前 permission mode。

这套设计保持“手脑分离”：app-server/harness 只持有短生命周期的控制与 capability，代码读取和写入在 executor environment 内完成；工作目录中的源码、`.agents/skills` 等内容不会被复制进 worker 的本地 cwd。

当 session 冻结到 BYO AgentX environment 时，executor MCP 不会为无关的 managed TAE profile 申请 sandbox activity；只有实际指向 managed environment 的 run 才会建立对应的 TAE lease。

`workerUid/workerGid` 是运行 harness-worker 的 Linux identity，`appUid/appGid` 是 stock app-server 的固定 Linux identity；UID 和 GID都必须分别不同。生成的 harness-pool 环境显式启用 privileged-fork backend：常驻 pool保留 `CHOWN/DAC_OVERRIDE/SETUID/SETGID`，每个 attempt直接 fork固定worker identity，worker再 fork固定app identity并在启动后封死自身 capability；热路径不创建容器、Job或Pod。开发 attempt anchor使用execute-only traversal，不允许app列目录或读pool/worker文件。

## 4. 生成

从 `v2/` 目录执行：

```bash
go run ./cmd/agentserver-dev prepare --insecure-dev \
  --config=/absolute/dev-stack.json \
  --output-dir=/absolute/new-agentserver-v2-dev
```

输出结构为：

```text
new-agentserver-v2-dev/
├── metadata.json
├── agentx/
│   └── launch.json
├── config/
│   ├── core-bootstrap.json
│   ├── dev-fixtures.json
│   ├── harness-worker.json
│   └── run-manifest-keyring.json
├── env/
│   ├── agentserver-core.env
│   ├── browser-gateway.env
│   ├── executor-gateway.env
│   └── harness-pool.env
├── pki/
│   ├── ca.pem
│   └── <service>.crt / <service>.key
├── secrets/
│   ├── browser-bearer.token
│   ├── external-oidc-client.secret
│   ├── run-capability.key
│   ├── run-cursor.key
│   └── run-manifest.seed
└── state/
    ├── objects/
    ├── harness-runtime/
    ├── checkpoint-staging/
    └── agentx-runtime/
```

生成测试会把这些文件直接交给现有的 Core bootstrap loader、Core TLS loader、browser/executor/pool TLS 与配置 loader、完整 harness-worker deployment loader和 fixture bundle loader；不是只检查“JSON 能解析”。fixture 集成测试还会使用真实 Core introspection client 和显式信任开发 CA 的 TLS client 验证两条网络合同；禁止 loopback socket 的测试沙箱只跳过该网络层，handler 与认证负向测试仍会执行。

## 5. 启动顺序

当前启动顺序如下。每个服务应在独立 shell/subshell 中 source自己的环境，不能把四份环境合并后继承给所有进程。

先启动 PostgreSQL，再在独立终端启动同一进程内的两个开发 fixture：

```bash
go run ./cmd/agentserver-dev fixtures --insecure-dev \
  --bundle=/absolute/new-agentserver-v2-dev
```

这个进程只绑定配置中的两个 loopback listener：

- Hydra/OIDC 侧实现开发所需的最小 Authorization Code + PKCE 合同：Hydra public authorize/token、Hydra Admin login/consent、外部 IdP discovery/authorize/token/JWKS 和动态 opaque access-token introspection。authorization code、login verifier 和 consent verifier 都只能消费一次；外部 IdP 使用 Ed25519 签发带 nonce 的 ID token，Core 使用真实 discovery/JWKS verifier。Platform 通过 platform-gateway 同源访问 `/oauth2/*` 和 `/auth/*`；Browser 使用 Platform 的绝对 authorize/token endpoint，其中 token CORS 只放行 Browser origin。IdP token/JWKS 和 Hydra Admin 仍是 Core 的私有调用。fixture 同时实现 `agentserver-platform` 与 `agentserver-browser` 两个 profile，动态 token 具有互斥的 client/audience/scope/resource grant；Browser token 精确绑定 bootstrap workspace。兼容用 `secrets/browser-bearer.token` 只服务旧 introspection 单元夹具，两个 reference SPA 与 smoke 都不读取它；模糊 method/path/header/form 输入一律拒绝。
- llmproxy 侧使用生成的 TLS identity，只接受 `POST <llmproxyEndpoint>/responses` 和唯一 `Authorization: Bearer ...`。每次请求都校验 HMAC、`aud=llmproxy`、有效期、workspace/session/actor 以及 model/provider route；`aud=executor-mcp`、过期、篡改和跨 route token 都会被拒绝。
- 模型脚本按 `capability + run + attempt + generation` 分别维护有界状态，不使用进程级全局序号。第一轮要求模型可见 catalog 中精确存在 `executor.list_environments` 与 `executor.shell`，先发出参数 `{}` 的 environment call；第二轮只从同一 `call_id` 的结构化结果中取得唯一 environment ID，再发出固定 `argv=["/bin/pwd"]` 的 shell call；第三轮收到匹配 shell output 后才返回最终 assistant message。第四次请求、缺工具、环境歧义和错误顺序均失败。这样开发演示会真实经过 stock exec-server，并产生可由 reference web 渲染的 command A2UI card。

随后执行一次 Core bootstrap：

```bash
(
  . /absolute/new-agentserver-v2-dev/env/agentserver-core.env
  go run ./cmd/agentserver-core bootstrap --insecure-dev \
    --config=/absolute/new-agentserver-v2-dev/config/core-bootstrap.json
)
```

完全相同的 bootstrap 重试为零写入；任何既有 authority 不同都会整笔失败，不会覆盖。随后分别启动服务：

```bash
(
  . /absolute/new-agentserver-v2-dev/env/agentserver-core.env
  exec go run ./cmd/agentserver-core serve --insecure-dev
)

(
  . /absolute/new-agentserver-v2-dev/env/browser-gateway.env
  exec go run ./cmd/browser-gateway serve
)

(
  . /absolute/new-agentserver-v2-dev/env/executor-gateway.env
  exec go run ./cmd/executor-gateway serve --insecure-dev
)

(
  . /absolute/new-agentserver-v2-dev/env/harness-pool.env
  exec go run ./cmd/harness-pool serve --insecure-dev
)
```

最后按 `agentx/launch.json` 的 `program` 和 `arguments` 原样执行 agentx。它主动拨出到 executor-gateway；服务端不向用户机器发起入站连接。

前端使用 browser-gateway 的 AG-UI endpoint：

```text
https://127.0.0.1:17444/v2/workspaces/{workspaceId}/sessions/{sessionId}/agui
```

显式取消使用同一origin上的独立command endpoint：

```text
POST https://127.0.0.1:17444/v2/workspaces/{workspaceId}/runs/{runId}:cancel
```

空body、同一短期OAuth access token。无holder的queued run会直接返回terminal `cancelled`；已有attempt时先返回非terminal `cancelling`，harness-pool通过成对lease heartbeat观察取消，在worker/app-server/MCP与进程组清理期间继续续租。取消只终止turn/MCP runtime context；worker control要继续到interrupted terminal获得ACK，pool lifecycle command context也要继续到workload cleanup完成，避免terminal前已经排队的runtime event因取消而丢失。最后由exact holder提交`cancelled/interrupted`。启动前失败已经停止workload时，pool使用内部`AbandonAttempt`在一个Core事务中仲裁requeue与并发cancel，不能用一次状态查询后再release来替代。SSE断开本身不触发取消。

审批决定使用同一origin上的另一条独立command endpoint：

```text
POST https://127.0.0.1:17444/v2/workspaces/{workspaceId}/approvals/{approvalId}:decide
```

body必须精确包含`decision=approve|deny`、canonical `nonce`、Core投影的`approval-context/rfc8785-v1` digest和`expectedApprovalVersion`；这些值只能来自`CUSTOM agentserver.approval`，不能从display-only A2UI卡片读取或由浏览器改写。Core会同时复核browser-gateway mTLS identity、原始OAuth access token的当前RBAC、数据库时间expiry与live attempt generation。`approve`响应只表示approval已批准，execution仍为`pending_approval`；精确gateway后续成功consume才产生dispatch authority。开发栈的shell policy固定为`ask`，executor-gateway会在原MCP `tools/call`上发起真实elicitation，worker通过harness-control 1.3等待Core canonical outcome；reference按钮可人工决定，smoke则从同一`CUSTOM`事件读取authority后自动批准。任何一侧都不能从A2UI display card推导批准。

开发前端先从无敏感信息的`GET /auth/config`读取同origin OAuth配置，再使用Authorization Code + PKCE登录。PKCE state/verifier/nonce和workspace/session关联只在`sessionStorage`保留十分钟以内，callback时单次消费；Platform/Browser access token 分别写入各自 origin 的版本化`localStorage`记录，连同scope、workspace binding、绝对过期时间和配置指纹严格恢复，并通过`storage`事件同步同应用标签页。token仍不得进入URL、metadata或服务环境。harness-pool为每个attempt动态签发`aud=executor-mcp`与`aud=llmproxy`两枚不同capability；前端不接触它们，app-server也永远拿不到executor capability。

## 6. 单容器可运行开发栈

`deploy/insecure-dev` 已经把当前组件接成一套可启动的 Linux arm64 开发环境。它在一个容器中启动 PostgreSQL、fixtures、Core、browser-gateway、executor-gateway、harness-pool 和独立 agentx；attempt 热路径使用普通 `fork/exec`。stock `codex app-server` 仍由 harness-worker 按 attempt 通过 stdio 启动，是短命的模型 harness 进程，不是给前端连接的常驻产品服务。前端只连接 browser-gateway 的 AG-UI endpoint，A2UI payload 通过 AG-UI custom event 承载。

镜像构建不联网下载 artifact，只接受 `internal/devruntime` 固定的官方 stable `0.146.0` Linux arm64 Codex 和 bwrap。agentx 继续来自独立仓库。以本仓库根目录作为 executor workspace 时，从 `v2/` 执行：

```bash
./deploy/insecure-dev/build.sh \
  --codex=/absolute/codex-aarch64-unknown-linux-musl \
  --bwrap=/absolute/bwrap-aarch64-unknown-linux-musl \
  --agentx-source=/absolute/agentx-v2

./deploy/insecure-dev/run.sh
./deploy/insecure-dev/browser-url.sh
./deploy/insecure-dev/smoke.sh
```

`browser-url.sh`只输出`https://127.0.0.1:<port>/`，不读取或输出任何bearer。reference web在同一HTTPS origin完成Hydra Authorization Code + PKCE登录，使用带`Authorization` header的`fetch` streaming，不使用`EventSource`，也不连接app-server或exec-server。它渲染AG-UI message/reasoning/tool lifecycle、`a2ui.operations`中当前display-only A2UI v0.9 Card/Column/Text子集，以及显式Cancel按钮和`Cancelling/Cancelled`状态。审批区只从`agentserver.approval`保存nonce/digest/version并调用独立Approve/Deny API；A2UI approval surface仍然display-only。canonical terminal到达后，迟到的网络尾部错误不会把页面降级成disconnected。

状态使用 Apple `container` 的 VM-backed named volume，默认名为 `agentserver-v2-dev-state`。这是因为宿主 bind mount 不能可靠地把私有 harness 输入改属固定 worker UID/GID；executor workspace 仍是普通的宿主 bind mount，不与 authority 状态混放。Apple `container` 的端口发布转发到容器 NIC，因此部署入口只把 browser-gateway 覆盖为监听 `0.0.0.0:17444`，宿主仍只发布 `127.0.0.1:17444`；Core、executor-gateway、harness-pool 和 fixtures 保持容器 loopback 监听。

真实smoke会先用TLS 1.3 client和Cookie Jar读取`/auth/config`，完整经过Hydra authorize、Core login bridge、开发外部IdP、callback、consent与浏览器code exchange，再验证捕获原事务Cookie后callback仍不可重放、consent不可重放、浏览器authorization code不可二次兑换。它只把本次动态access token留在进程内，随后验证reference web与CSP并连续执行五条独立run：

```text
AG-UI → Core → harness-worker → stock app-server
      → executor MCP(policy=ask) → canonical approval → POST :decide
      → Core ConsumeApproval → agentx → stock exec-server
      → RUN_FINISHED → durable checkpoint

AG-UI → approval/consume → deterministic post-execution hold → POST :cancel
      → run.cancelling → stock turn interrupted/workload stopped
      → run.cancelled → RUN_ERROR(user_cancelled) → no checkpoint

AG-UI → pending approval → deny | database-time expiry
      → bounded tool failure → scripted final message → RUN_FINISHED
      → zero dispatch → durable checkpoint

AG-UI → pending approval → POST :cancel
      → approval.cancelled + execution.cancelled → turn/interrupt
      → RUN_ERROR(user_cancelled) → zero dispatch → no checkpoint
```

脚本只从`CUSTOM agentserver.approval`读取nonce/digest/version并调用独立`:decide` API，不读取或触发A2UI action。它在运行前后直接查询PostgreSQL：五条run必须恰好新增3个checkpoint和5个approval；两个获批shell各冻结`process_start + timeout_terminate`两条operation-plan记录，所以总operation增加4，但只有两个`process_start`拥有`dispatched_at`。deny、expiry和pending-cancel对应的execution必须分别为`denied|expired|cancelled`、没有dispatch timestamp且各自拥有0条operation；两个取消run都必须拥有0个checkpoint。这同时验证批准点击本身不直接dispatch、防止pre-dispatch失败越过agentx发送边界，也防止把被取消turn的临时rollout误提交为可恢复历史。

2026-08-01包含完整Code + PKCE登录链的实跑镜像manifest digest为`24c44fe44872962a828df84d3ff67ae2d541e076fad1e4101f9dc0dca5d8bf21`。它在全新状态卷上通过后，又在同一容器和同一卷上立即完整复跑一次；两轮都先通过登录与三项replay gate，累计计数再从`checkpoints=3 approvals=5 operations=4 dispatched_operations=2`增长为`6/10/8/4`。fixture只从最新user message读取场景marker，checkpoint resume不会把历史deny/expiry/cancel行为继承给后续run。该轮实跑同时关闭了harness control累计ACK的三个线序缺口：pool写approval outcome、worker写带piggyback ACK的event时都将cursor冻结与socket write置于同一顺序屏障；resume时worker先重放携带旧ACK的不可变journal frame，再发送刚接收完peer replay后的新standalone ACK。确定性阻塞写测试、重复测试和race detector共同守住这些边界。

正常停止应给 supervisor 足够时间让 PostgreSQL fast shutdown 并回收子进程：

```bash
container stop --time 15 agentserver-v2-dev
```

这仍是明确的`insecure-dev`部署：数据库口令、兼容用固定browser bearer、外部OIDC client secret、登录事务key、开发CA和fixture模型响应都只适用于本地开发；所有控制面服务与PostgreSQL仍同容器、executor-gateway仍是单副本，也没有生产级secret分发、网络策略、升级迁移、监控告警或高可用保证。它的用途是验证协议和真实执行闭环，并作为reference web体验的后端，不应直接作为生产拓扑。

## 7. executor-gateway production 切片验证

`executor-gateway serve`现在默认装配production authority；只有显式
`serve --insecure-dev`才进入上文开发路径。production启动除通用listen、
TLS、Core mTLS和tool policy配置外，还必须提供：

```text
AGENTSERVER_V2_EXECUTOR_GATEWAY_SPIFFE_ID
AGENTSERVER_V2_EXECUTOR_ID
AGENTSERVER_V2_RUN_CAPABILITY_ISSUER
AGENTSERVER_V2_RUN_CAPABILITY_KEYRING_FILE
```

SPIFFE值必须与gateway server/client leaf中唯一URI SAN完全一致；私钥路径
必须是absolute clean path，并解析为group/other不可读的有界regular file。
Kubernetes projected Secret常见的`key -> ..data/key`符号链接允许使用，检查
的是启动时打开的最终target；Phase 1不hot reload，Secret轮换必须触发Pod
rollout。公钥keyring可读但仍要求有界、closed-world且读前后file identity
稳定。

production identity的快速门禁从`v2/`运行：

```bash
go test ./internal/corecontract ./internal/coreserver ./internal/executorgateway ./cmd/executor-gateway \
  -run 'ExecutorEnrollment|ExecutorIdentity|MachineIdentity|ProductionMachineIdentity|InternalOpenAPI' \
  -count=1

go test -race ./cmd/executor-gateway ./internal/executorgateway -count=1
```

进程级测试会临时启动TLS/mTLS Core和真实production gateway，覆盖gateway
注入且Core在副作用前校验的单executor enrollment binding、challenge、两次
live authorization、signed WSS upgrade、bad proof、replay、Core unavailable
retry、revoke及shutdown。它不需要外部Hydra；真实Hydra v26.2.0的ES256
`private_key_jwt`兼容性由`make hydra-live-test`单独验证。

production gateway不会先监听再异步恢复。它在创建listener和发布`/readyz`
之前生成新的gateway identity，并调用
`POST /internal/v2/executor-connections/{executorId}:recover-gateway`直到Core返回
`remaining=false`。任何错误或响应不明都使进程直接退出；同一进程不会重试
模糊结果。Core先提交旧connection generation的fence，再恢复execution，因此
即使第二阶段遇到无法安全聚合的operation，旧gateway也已经不能继续写入。

真实PostgreSQL恢复与独立进程hard-kill门禁需要显式测试数据库：

```bash
AGENTSERVER_RUN_POSTGRES_TESTS=1 \
AGENTSERVER_V2_TEST_DATABASE_URL='postgres://...' \
go test ./internal/coredb -run '^TestPostgreSQLExecutorGatewayRecovery' -count=1 -v
```

该矩阵逐点kill实际Go子进程，并验证Begin、不可逆发送、ACK、operation
terminal和execution terminal前后的恢复结果、零redispatch、stale generation
拒绝及重复startup零重复event。测试中的不可逆发送使用append+fsync marker固定
事务外边界；真实agentx WSS断线语义另由executor-gateway纵向测试覆盖，最终
Kubernetes跨仓门禁仍不能省略。

这一切仍不是可部署整栈：独立agentx现已完成owner-only双钥、RFC 7523
token exchange、每次物理连接的production challenge/WSS接线、connector与
降权runner的credential隔离、Linux filesystem safe-open、cgroup v2
不可逃逸process-tree containment，以及`5d40b6b`的runtime pinned-exec。
production runtime安装链要求从`/`到Codex与bundled bwrap全部root-owned且
不可group/other写，Codex以固定descriptor在cgroup Commit后执行。cgroup
containment另要求root connector只保留`CHOWN/SETUID/SETGID`、拥有writable
delegated cgroup v2 subtree，并由真实PID 1 init/reaper监管；普通insecure-dev
启动不宣称这些生产边界。gateway restart的应用层fence/unknown恢复已经完成，
但仍未完成Kubernetes/S3/KMS/Hydra/IAM清单与全拓扑故障注入。Phase 1必须保持
executor-gateway单副本并使用`Recreate`；进程重启会丢失challenge与resume
journal，不能打开HPA、使用rolling overlap或宣称跨Pod恢复。
