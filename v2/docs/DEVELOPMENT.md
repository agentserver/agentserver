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
- objects、harness runtime、checkpoint staging 和 agentx runtime 四个本地状态目录；
- 不含 secret 值的 metadata、服务端点和明确的运行限制说明。

输出目录必须是一个尚不存在的绝对路径。命令不会 merge、覆盖或复用旧 authority；任何生成失败都会清理本次新建的不完整目录。所有目录固定为 `0700`，所有文件固定为 `0600`。CA 私钥不会落盘，生成结束后不能用这套 bundle 继续签发新身份。

run-capability HMAC 只进入 executor-gateway 和 harness-pool 环境，cursor key 只进入 Core 环境，manifest signing seed 只由 harness-pool 引用。harness-worker deployment 只引用公钥 keyring和自己的 mTLS identity；agentx launch JSON 不含任何模型或集群 secret。

机器输入 schema 位于 `api/schema/insecure-dev-stack.schema.json`。Go loader还会检查 schema 无法表达的文件类型、权限、symlink、runtime digest、当前平台 artifact、stock Codex release、workspace containment、loopback和身份分离约束。

## 2. 前置材料

先准备以下绝对路径：

1. 已验证的 stock Codex `0.146.0` runtime bundle和与其精确匹配的 runtime manifest；
2. 本仓编译出的 `harness-worker`、`harness-final-exec`；
3. 独立 agentx 仓库编译出的 agentx；
4. 一个已经存在、无 symlink component 的本地 workspace；
5. PostgreSQL URL，以及尚未被其他进程占用的 loopback 端口。

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

`platform`、Codex release/commit/digest、exec protocol digest 和 checkpoint allowlist不能在这里另填一份；命令从 runtime manifest 的原始字节派生它们。当前 harness config profile只接受 stock `0.146.0`。四个服务监听地址必须互不相同、端口非零且显式指向 loopback。Hydra 开发 endpoint固定为 cleartext loopback HTTP；llmproxy endpoint固定为 loopback HTTPS。

`workerUid/workerGid` 是运行 harness-worker 的 Linux identity，`appUid/appGid` 是 stock app-server 的固定 Linux identity；UID 和 GID都必须分别不同。开发 attempt anchor现在使用 `0701`，只给 app identity execute-only traversal，不允许列目录或读文件。

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
│   ├── run-capability.key
│   ├── run-cursor.key
│   └── run-manifest.seed
└── state/
    ├── objects/
    ├── harness-runtime/
    ├── checkpoint-staging/
    └── agentx-runtime/
```

生成测试会把这些文件直接交给现有的 Core bootstrap loader、Core TLS loader、browser/executor/pool TLS与配置 loader，以及完整 harness-worker deployment loader；不是只检查“JSON 能解析”。

## 5. 启动顺序

当前启动顺序如下。每个服务应在独立 shell/subshell 中 source自己的环境，不能把四份环境合并后继承给所有进程。

先启动 PostgreSQL和开发 Hydra introspection endpoint，再执行一次 Core bootstrap：

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

浏览器 bearer仍只进入 browser-gateway/Core 链路。harness-pool为每个 attempt动态签发 `aud=executor-mcp` 与 `aud=llmproxy` 两枚不同 capability；前端不接触它们。

## 6. 尚未形成一键可用闭环的部分

prepare只解决“所有已实现组件使用同一份开发 authority和密钥材料”，不会伪造尚不存在的依赖。目前仍有以下硬前置：

- 真实 PostgreSQL；
- 固定 opaque bearer映射到 `actorId` 的 loopback Hydra introspection fixture；
- 校验动态 `aud=llmproxy` capability 的 scripted Responses/llmproxy fixture；
- Linux privileged harness runtime，包括固定 UID/GID切换、process-group cleanup和 app-server egress policy；
- 把生成的开发 CA加入 stock app-server 可见的系统 trust store。app-server child 的 closed-world环境故意不接受 ambient `SSL_CERT_FILE`/代理变量；
- 当前平台精确匹配、已经通过门禁的 stock Codex artifact；
- 在线 agentx/executor environment。

因此 macOS上可以验证生成、配置、PKI、agentx/exec-server和控制面前置边界，但不能把一次真实模型 turn当作已经部署成功。下一实施段会补 Hydra 与 scripted llmproxy fixture，然后在 PostgreSQL + Linux runtime 上做真实 AG-UI → checkpoint smoke。
