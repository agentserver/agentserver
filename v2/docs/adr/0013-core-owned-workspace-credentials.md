# ADR 0013：v2 Core-owned workspace credentials

状态：accepted
范围：Platform、v2 Core、execution gateway、TAE Sandbox profile、可选 TAE egress-authorizer、生产部署

## 决策

workspace credential 的配置、权限、密封存储和运行时 materialization 都属于
v2 Core 的能力。项目不新增、部署或调用独立的 `credentialproxy` 进程。

Platform 只提供一个薄的 workspace credential 路由层：它转发已认证的用户
请求到 Core，不保存或解析 secret。Core 根据内置 provider registry 校验上传
的 auth type 和 secret，使用 Core keyring 密封后写入
`workspace_credential_bindings`，并以 CAS 版本管理轮换、撤销和默认绑定。

## 数据流

```text
Platform user
    │  credential CRUD (user bearer)
    ▼
Platform gateway ───────────────► v2 Core
                                  │ sealed binding + audit
execution gateway ──────────────►│ resolve authority + process token
                                  │ (process_env direct profile)
                                  ▼
                         exact managed CLI process ─► TAE preset/private network policy ─► upstream

future webhook profile:
TAE Agent Gateway ─► egress-authorizer ─► Core resolve ─► one-hop header mutation
```

Core 是唯一持有 credential sealing keyring、读取 sealed secret、执行 provider
adapter 的组件。当前 SG direct profile 不部署 egress-authorizer；未来 webhook profile 中，
egress-authorizer 只持有 placeholder verification keyring，负责 ZTI、TAE policy 和最终请求 tuple 校验，
不会持有 workspace secret。

Managed Lark credential delivery 是 workspace 级配置。mode 的唯一事实源是 Core workspace row；创建
workspace 时必须显式选择，owner 通过 workspace version CAS 切换并产生独立审计：

- `webhook_swap` 只允许在独立 webhook-enabled Sandbox profile 中使用 placeholder → Policy Webhook → Core header mutation 数据流；
- `process_env` 由 executor-gateway 在精确 TAE `lark-cli` process start 前通过 mTLS 调 Core，Core
  live-authorize、解封后把真实 access token 返回给 executor-gateway，由后者只注入该进程环境。网络请求
  直接使用 TAE 系统预置的 `*.feishu.cn` 白名单，不配置 webhook、不经过 egress-authorizer，也不签发
  `LARKSUITE_CLI_AGENT_TRACE` proof。

两种模式互斥，不存在部署默认、自动探测或 fallback，而且不能复用同一个 Sandbox profile。当前 SG
direct profile 只支持 `process_env`；如果 workspace 被切成 `webhook_swap`，进程启动必须 fail closed，直到
它被路由到未来独立的 webhook-enabled profile。direct profile 不部署 egress-authorizer，也不装载
placeholder/proof 签名与验签材料。

## API 边界

Platform 使用以下资源路由：

- `GET /v2/credential-providers`
- `GET|POST /v2/workspaces/{workspaceId}/credentials/{kind}`
- `PATCH /v2/workspaces/{workspaceId}/credentials/{kind}/{bindingId}`
- `POST ...:rotate|:revoke|:delete|:setDefault`

内部运行时只开放 workload mTLS 路由：

- executor gateway 调 `...:resolve-authority`，取得 binding/version reference；
- egress-authorizer 调 `...:resolve`，取得 closed-world provider header mutation；
- egress-authorizer 调 `credential-use-events`，写入最小化审计事件。
- workspace 当前为 `process_env` 时 executor-gateway 调
  `POST /internal/v2/execution/credentials:resolve`，只为通过 provider-specific argv policy 的 live
  `shell + managed CLI + TAE process_start` 取得真实 credential，并以 `Cache-Control: no-store` 传输。
  当前支持 `lark-cli` 的 user access token 和 `bkectl` 的 ByteCloud JWT；Core 会再次拒绝 `bkectl auth`、
  write/risky、未知命令以及 `--debug`、`--confirm-write`。

egress resolve response 不包含 token、AK/SK、refresh token、sealed bytes 或 provider response body；direct
execution credential resolve 是唯一返回真实 process credential 的窄接口，绝不返回 refresh token、sealed
bytes 或任意未声明 provider header。缺少 binding 以 `configured=false` 表达，不阻塞 managed sandbox 或
部署启动，也不回退到另一 mode。

## Provider 规则

provider registry 是 closed-world。默认 SG 安装包含：

- `lark`：`open.feishu.cn`，静态 bearer；
- `github`：`api.github.com`，静态 PAT；
- `bytecloud`：固定 SG JWT exchange endpoint，workspace AK/SK 只在 Core 内
  换取短期 JWT，并支持带界的 cache bypass/force refresh。

每个 provider 声明 auth types、允许的 host、允许注入的 header。未知 provider、
host、method、path 或 header 一律拒绝；header mutation 只允许 provider 声明的
字段。增加 OAuth refresh 或 GitHub App 时，必须增加显式 adapter 和 schema，不能
通过部署环境变量绕过 registry。

## 部署约束

生产 deployment config 只包含 Core sealing keyring 和 provider policy lock；只有 webhook-enabled profile
才包含 placeholder/proof keyring Secret 名称。配置不包含任何 workspace token、client secret、AK/SK、grant
或 expiry。managed executor 的 Helm bundle 不创建 Lark bootstrap Job/Secret；
管理员在 Platform 配置 credential 后即可动态生效。

production schema、generated Helm values/schema/guard、Pulumi 与 workload environment 都不接受 workspace
mode。execution gateway 在每次进程启动时从 Core 获取当前值；mode 与当前 Sandbox profile 不匹配时拒绝，
不能用运行时 fallback 改写 TAE policy。

## 安全与故障语义

- authority 和 resolve 都重新校验 workspace membership、session/run/attempt
  lease、execution/operation、sandbox generation、binding authority version；
- 任何 Core、audit、provider exchange 或 policy 依赖超时都 fail closed；
- 审计只记录 scope、provider、target、decision/reason，不记录 Authorization、
  token、AK/SK、sealed secret 或响应体；
- secret 轮换推进 `credentialVersion`；撤销、owner/policy authority 变化推进 `authorityVersion`。下一次
  process start 必须重新解析 live authority；
- `process_env` 的真实 token 对目标 `lark-cli` 及其子进程可见，已经进入进程内存的字节无法远程擦除；
  已启动进程不会被逐请求远程撤销，因此 direct profile 依赖短进程、最小 scope、短期 token、TAE 系统
  `*.feishu.cn` host 白名单和每次启动前的 live authorization。

这使凭据配置成为 v2 Core 的一部分，同时保留统一 execution gateway 和现有
egress gateway 的职责分离。
