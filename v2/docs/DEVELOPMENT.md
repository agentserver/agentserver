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
- 固定 opaque browser bearer、只引用 secret 文件路径的 fixture launch 配置；
- objects、harness runtime、checkpoint staging 和 agentx runtime 四个本地状态目录；
- 不含 secret 值的 metadata、服务端点和明确的运行限制说明。

输出目录必须是一个尚不存在的绝对路径。命令不会 merge、覆盖或复用旧 authority；任何生成失败都会清理本次新建的不完整目录。所有目录固定为 `0700`，所有文件固定为 `0600`。CA 私钥不会落盘，生成结束后不能用这套 bundle 继续签发新身份。

run-capability HMAC 只进入 executor-gateway 和 harness-pool 环境，并由开发 llmproxy fixture 从独立 `0600` 文件加载；cursor key 只进入 Core 环境，manifest signing seed 只由 harness-pool 引用。browser bearer 只存在于 `secrets/browser-bearer.token`。harness-worker deployment 只引用公钥 keyring 和自己的 mTLS identity；agentx launch JSON、fixture config 和 metadata 都不含任何 secret 值。

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
    "maxRunDuration": "30m"
  },
  "identities": {
    "workerUid": 65531,
    "workerGid": 65531,
    "appUid": 65532,
    "appGid": 65532
  }
}
```

`platform`、Codex release/commit/digest、exec protocol digest 和 checkpoint allowlist不能在这里另填一份；命令从 runtime manifest 的原始字节派生它们。当前 harness config profile只接受 stock `0.146.0`。四个服务和两个 fixture 监听地址必须全部不同、端口非零且显式指向规范的 loopback host。Hydra 开发 endpoint 固定为 cleartext loopback HTTP；llmproxy endpoint 固定为 loopback HTTPS。当前确定性模型脚本会调用 `executor.list_environments`，因此 `policy.allowedTools` 必须包含 `list_environments`。

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

- Hydra 侧只接受 Core 实际发送的 exact form POST。正确的 `secrets/browser-bearer.token` 映射到 bootstrap `actorId`、单一 `aud=agentserver-api` 和 `runs:write`；其他规范 bearer 返回 `active:false`，模糊 method/path/header/form 输入直接拒绝。`exp` 在每次正确 introspection 时按当前时间向后生成。
- llmproxy 侧使用生成的 TLS identity，只接受 `POST <llmproxyEndpoint>/responses` 和唯一 `Authorization: Bearer ...`。每次请求都校验 HMAC、`aud=llmproxy`、有效期、workspace/session/actor 以及 model/provider route；`aud=executor-mcp`、过期、篡改和跨 route token 都会被拒绝。
- 模型脚本按 `capability + run + attempt + generation` 分别维护有界状态，不使用进程级全局序号。第一轮要求模型可见 catalog 中精确存在 `executor.list_environments` 并发出参数 `{}` 的 dynamic call；只有第二个请求带回同一 `call_id` 的 `function_call_output` 后，才返回最终 assistant message。第三次请求和错误顺序均失败。

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
  exec go run ./cmd/agentserver-core serve
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

开发前端从 `secrets/browser-bearer.token` 读取 bearer 并只发送给 browser-gateway；该值不会出现在 metadata 或任何服务 env 中。harness-pool 为每个 attempt 动态签发 `aud=executor-mcp` 与 `aud=llmproxy` 两枚不同 capability；前端不接触它们，app-server 也永远拿不到 executor capability。

## 6. 单容器可运行开发栈

`deploy/insecure-dev` 已经把当前组件接成一套可启动的 Linux arm64 开发环境。它在一个容器中启动 PostgreSQL、fixtures、Core、browser-gateway、executor-gateway、harness-pool 和独立 agentx；attempt 热路径使用普通 `fork/exec`。stock `codex app-server` 仍由 harness-worker 按 attempt 通过 stdio 启动，是短命的模型 harness 进程，不是给前端连接的常驻产品服务。前端只连接 browser-gateway 的 AG-UI endpoint，A2UI payload 通过 AG-UI custom event 承载。

镜像构建不联网下载 artifact，只接受 `internal/devruntime` 固定的官方 stable `0.146.0` Linux arm64 Codex 和 bwrap。agentx 继续来自独立仓库。以本仓库根目录作为 executor workspace 时，从 `v2/` 执行：

```bash
./deploy/insecure-dev/build.sh \
  --codex=/absolute/codex-aarch64-unknown-linux-musl \
  --bwrap=/absolute/bwrap-aarch64-unknown-linux-musl \
  --agentx-source=/absolute/agentx-v2

./deploy/insecure-dev/run.sh
./deploy/insecure-dev/smoke.sh
```

状态使用 Apple `container` 的 VM-backed named volume，默认名为 `agentserver-v2-dev-state`。这是因为宿主 bind mount 不能可靠地把私有 harness 输入改属固定 worker UID/GID；executor workspace 仍是普通的宿主 bind mount，不与 authority 状态混放。Apple `container` 的端口发布转发到容器 NIC，因此部署入口只把 browser-gateway 覆盖为监听 `0.0.0.0:17444`，宿主仍只发布 `127.0.0.1:17444`；Core、executor-gateway、harness-pool 和 fixtures 保持容器 loopback 监听。

真实 smoke 已验证以下闭环：

```text
AG-UI → Core → harness-worker → stock app-server
      → executor MCP → agentx → stock exec-server
      → RUN_FINISHED → durable checkpoint
```

正常停止应给 supervisor 足够时间让 PostgreSQL fast shutdown 并回收子进程：

```bash
container stop --time 15 agentserver-v2-dev
```

这仍是明确的 `insecure-dev` 部署：数据库口令、browser bearer、开发 CA 和 fixture 模型响应都只适用于本地开发；所有控制面服务与 PostgreSQL 仍同容器、executor-gateway 仍是单副本，也没有生产级 secret 分发、网络策略、升级迁移、监控告警或高可用保证。它的用途是验证协议和真实执行闭环，并作为后续 reference web 体验的后端，不应直接作为生产拓扑。
