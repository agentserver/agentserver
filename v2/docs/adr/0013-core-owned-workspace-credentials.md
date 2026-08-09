# ADR 0013：v2 Core-owned workspace credentials

状态：accepted
范围：Platform、v2 Core、execution gateway、TAE egress-authorizer、生产部署

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
execution gateway ──────────────►│ resolve authority (operation scope)
                                  │ short-lived placeholder
TAE Agent Gateway ─► egress-authorizer ─► Core resolve
                                      │ one-hop header mutation
                                      ▼
                                    upstream
```

Core 是唯一持有 credential sealing keyring、读取 sealed secret、执行 provider
adapter 的组件。egress-authorizer 只持有 placeholder verification keyring，
负责 ZTI、TAE policy 和最终请求 tuple 校验；它不会持有 workspace secret。

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

resolve response 不包含 token、AK/SK、refresh token、sealed bytes 或 provider
response body。缺少 binding 是正常 deny（`credential_not_configured`），不阻塞
managed sandbox 或部署启动。

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

生产 deployment config 只包含 Core sealing/placeholder keyring 的 Secret 名称
和 provider policy lock，不包含任何 workspace token、client secret、AK/SK、grant
或 expiry。managed executor 的 Helm bundle 不创建 Lark bootstrap Job/Secret；
管理员在 Platform 配置 credential 后即可动态生效。

## 安全与故障语义

- authority 和 resolve 都重新校验 workspace membership、session/run/attempt
  lease、execution/operation、sandbox generation、binding authority version；
- 任何 Core、audit、provider exchange 或 policy 依赖超时都 fail closed；
- 审计只记录 scope、provider、target、decision/reason，不记录 Authorization、
  token、AK/SK、sealed secret 或响应体；
- secret 轮换推进 `credentialVersion`，已有短期 placeholder 会 materialize 最新版本；撤销、owner/
  policy authority 变化推进 `authorityVersion`，立即 fence 旧 placeholder。

这使凭据配置成为 v2 Core 的一部分，同时保留统一 execution gateway 和现有
egress gateway 的职责分离。
