# AgentServer v2 用户权限与 Hydra authority

> 状态：设计基线（draft）
>
> 本文只描述用户通过 Platform / Browser 前端调用公网 API 的权限。executor、harness-pool、
> llmproxy 等 workload identity、run capability 和 executor machine token 仍使用各自的内部合同，
> 不进入这里的用户 OAuth scope。

## 1. 原则

1. 所有前端 API 请求都携带 Hydra 签发的 opaque access token。
2. gateway 不把用户 token 换成自定义 session，也不自行扩权；它把原始 bearer 传给 Core。
3. Core 对每个请求调用 Hydra introspection，并只接受当前 `active` 的权限快照。首版不缓存
   introspection 结果。
4. endpoint 的授权结果只来自 introspection 返回的 client、audience、scope 和 AgentServer resource
   grant；不得根据前端页面、URL 来源或 gateway 自报角色放行。
5. `scope` 表示动作，例如 `sessions:create`；workspace 是独立的资源绑定。禁止把 UUID 拼成
   `workspace:<uuid>:sessions:create` 之类的动态 scope。
6. Hydra 是外部请求的 token / grant authority；Core 的 workspace、成员、session 等业务事实仍在
   AgentServer 数据库。Core consent policy 把这些事实编译成发给 Hydra 的权限快照。Hydra 本身不是
   workspace RBAC 数据库。
7. token 中不写 `owner`、`developer`、`viewer` 角色。API 只检查展开后的最小权限，避免未来角色含义
   变化时旧 token 被隐式扩权。

第 5 条采用的是 GitHub App installation 类似的模型：动作权限和可访问资源是两个维度。GitHub 的
OAuth App scope 比较宽，官方也建议需要细粒度权限时使用 GitHub App；GitHub App installation token
同时受 installation 可访问的 repository 与 permission 集合约束，而且调用成功还受用户自身权限约束。
参考：

- [Scopes for OAuth apps](https://docs.github.com/apps/oauth-apps/building-oauth-apps/scopes-for-oauth-apps)
- [Choosing permissions for a GitHub App](https://docs.github.com/apps/creating-github-apps/registering-a-github-app/choosing-permissions-for-a-github-app)
- [Generating an installation access token](https://docs.github.com/apps/creating-github-apps/authenticating-with-a-github-app/generating-an-installation-access-token-for-a-github-app)

## 2. OAuth client 与 token profile

### 2.1 `agentserver-platform`

- 类型：Authorization Code + PKCE public client；无 client secret。
- audience：精确单值 `agentserver-platform-api`。
- 用途：workspace 及其成员、executor、LLM Gateway 和当前用户的第三方 Gateway grant 管理。
- token 可含少量全局权限和零到多个 workspace management grant。
- 不得包含 session、run 或 approval 权限。

### 2.2 `agentserver-browser`

- 类型：Authorization Code + PKCE public client；无 client secret。
- audience：精确单值 `agentserver-browser-api`。
- 用途：一个选定 workspace 内、当前用户自己的 session / run / approval。
- 每枚 token 必须精确绑定一个 workspace；缺少 workspace、包含多个 workspace 或 workspace 不规范时
  introspection authority 无效。
- 不得包含 workspace 配置、成员、executor 或 LLM Gateway 管理权限。

两个前端不共享 access token。Platform 进入 Browser 时只传 workspace ID；Browser 为该 workspace
独立发起 Hydra authorization。两者都直接使用独立 authority
`https://auth-sg.byted.bps.dev/oauth2/auth` 与 `/oauth2/token`；Platform 不代理 Hydra public endpoint。
Hydra login、consent 和外部身份 callback 也位于同一 auth authority：只有
`/auth/hydra/login`、`/auth/hydra/consent`、`/auth/oidc/callback` 三个精确路径由 HTTPRoute 分流到
login bridge，其他协议路径仍直达 Hydra。外部 OIDC client 的 redirect URI 必须登记为
`https://auth-sg.byted.bps.dev/auth/oidc/callback`。

## 3. Hydra introspection 合同

Core 要求 introspection 至少返回以下 authority：

```json
{
  "active": true,
  "iss": "https://auth-sg.byted.bps.dev/",
  "sub": "10000000-0000-4000-8000-000000000001",
  "client_id": "agentserver-browser",
  "aud": ["agentserver-browser-api"],
  "scope": "openid sessions:read sessions:create runs:read runs:create runs:cancel approvals:decide",
  "exp": 1785753600,
  "token_type": "Bearer",
  "ext": {
    "agentserver": {
      "version": 1,
      "authority": "browser",
      "global_permissions": [],
      "workspace_grants": [
        {
          "workspace_id": "20000000-0000-4000-8000-000000000002",
          "generation": 7,
          "permissions": [
            "sessions:read",
            "sessions:create",
            "runs:read",
            "runs:create",
            "runs:cancel",
            "approvals:decide"
          ]
        }
      ]
    }
  }
}
```

规范校验：

- `active=true`，`exp` 在未来，issuer 精确匹配部署合同；
- `sub` 是 active AgentServer user 的 canonical UUID；
- `client_id`、唯一 audience 与 authority 类型三者必须匹配；
- `scope` 是对应 client 注册的最大 scope 集合的子集，无重复、未知或混合 client scope；
- `ext.agentserver.version=1`，未知版本 fail closed；
- endpoint 所需 permission 必须同时存在于顶层 `scope` 和相应的 global/workspace grant；
- URL 中的 `workspaceId` 必须与选中的 workspace grant 精确相等；
- Platform grant 可以有多个 workspace；Browser 必须有且仅有一个；
- permission 数组规范排序、无重复；每个 grant 都有单调 `generation`，禁止两个 grant 指向同一
  workspace；
- role、email、前端传入的 permission、ID token 和 gateway header 都不是 API authority。

Platform introspection 使用同一结构：`authority=platform`，`global_permissions` 包含全局动作，
`workspace_grants` 分别列出用户在各 workspace 的管理权限。顶层 scope 是所有实际 grant permission
的并集。这样 Hydra client 的允许 scope 是静态集合，而资源权限仍可按 workspace 细分。

首版每个 HTTP 请求都 introspect。AG-UI/event 长连接不得无限持有一次鉴权结果：服务端把单次流限制为
最多 30 秒，在继续读取 cursor 前重新建立请求并重新 introspect；token 失效后不能继续收到事件。

## 4. Platform permission 注册表

下表是 `agentserver-platform` 允许注册的完整业务 scope。`openid` 只用于登录，不授权业务 endpoint。

| Permission | 资源范围 | 含义 | 初始状态 |
|---|---|---|---|
| `workspaces:read` | global + workspace | 列出当前用户可见 workspace、读取指定 workspace 元数据 | 待实现 |
| `workspaces:create` | global | 创建 workspace，并把当前用户设为 owner | 待实现 |
| `workspaces:update` | workspace | 修改名称、描述、默认 executor 等 workspace 配置 | 待实现 |
| `workspaces:archive` | workspace | 归档 workspace；不物理删除历史数据 | 待实现 |
| `members:read` | workspace | 查看成员及当前角色 | 待实现 |
| `members:add` | workspace | 添加成员 | 待实现 |
| `members:update` | workspace | 修改成员角色 | 待实现 |
| `members:remove` | workspace | 移除成员 | 待实现 |
| `executors:read` | workspace | 查看 executor、enrollment/connection/environment 状态 | 待补齐 list API |
| `executors:create` | workspace | 创建 enrolling executor identity | 已有 Core 能力，待迁移入口 |
| `executors:enroll` | workspace | 签发或替换短期 executor enrollment token | 已有 Core 能力，待迁移入口 |
| `executors:update` | workspace | 修改显示名、默认环境或可变元数据 | 待实现 |
| `executors:archive` | workspace | 禁用并归档 executor | 待实现 |
| `llm-gateways:read` | workspace | 查看 Gateway 配置和当前用户 grant 状态 | 已有 Core 能力，待迁移入口 |
| `llm-gateways:create` | workspace | 创建新的 Gateway，并可设为 default | 已有 Core 能力，待迁移入口 |
| `llm-gateways:update` | workspace | 修改 active Gateway 配置；递增配置版本并要求所有 active grant 重新授权 | 已有 |
| `llm-gateways:disable` | workspace | 禁用 Gateway、清除 default 并 fence 旧版本 run | 已有 Core 能力，待迁移入口 |
| `llm-gateway-grants:authorize` | workspace + self | 为当前 token subject 发起/完成第三方 OIDC grant | 已有 Core 能力，待迁移入口 |
| `llm-gateway-grants:revoke` | workspace + self | 撤销当前 token subject 自己的第三方 grant | 已有 Core 能力，待迁移入口 |

`self` 权限永远从 token `sub` 取得 user ID；请求 body/path 不允许指定另一个 grant owner。

## 5. Browser permission 注册表

下表是 `agentserver-browser` 允许注册的完整业务 scope：

| Permission | 资源范围 | 含义 | 初始状态 |
|---|---|---|---|
| `sessions:read` | workspace + self | 列出和读取当前用户自己的 session、标题与 durable transcript | session 元数据已实现；durable transcript 待实现 |
| `sessions:create` | workspace + self | 新建当前用户的 session | 已有 |
| `sessions:update` | workspace + self | 重命名当前用户的 session | 已有 |
| `sessions:archive` | workspace + self | 归档当前用户的 session；不物理删除 | 已有 |
| `runs:read` | workspace + self | 读取 session 的 run 状态、历史和 canonical event projection | 已有 event API，待补历史 API |
| `runs:create` | workspace + self | 在 session 没有 active run 时创建一次 run | 已有 |
| `runs:cancel` | workspace + self | 取消当前用户 session 中的 queued/running run | 已有 |
| `approvals:decide` | workspace + self | 对当前用户 run 的 live approval 作 approve/deny | 已有 |

`self` 是对话隐私边界：workspace owner 不因 owner 身份自动获得其他成员的 session、prompt、event 或
approval 读取能力。平台侧如未来需要合规审计，应设计独立 audit permission 和脱敏投影，不能复用
Browser scope。

## 6. 初始角色到 permission 的编译

角色只存在于 Core workspace membership，作为 Hydra consent policy 的输入。最终 token 只含 permission。

### 6.1 Platform grant

| Workspace role | Workspace grant permissions |
|---|---|
| owner | 第 4 节全部 workspace-scoped permission |
| developer | `workspaces:read`、`members:read`、`executors:read`、`llm-gateways:read`、`llm-gateway-grants:authorize`、`llm-gateway-grants:revoke` |
| viewer | `workspaces:read` |

每个 active user 的 Platform global permission 为 `workspaces:read workspaces:create`。`workspaces:read`
的 global 形式只能列出 token 内已有 grant 的 workspace，不能读取任意 workspace ID。

### 6.2 Browser grant

| Workspace role | Browser token permissions |
|---|---|
| owner | 第 5 节全部 permission |
| developer | 第 5 节全部 permission |
| viewer | `sessions:read runs:read`，且仍只限当前用户自己的历史 |

如果产品不希望 viewer 继续看降级前的历史，可以把 viewer 的 Browser permission 集合改为空；实现前应
由产品决策确认。当前表延续现有“viewer 可读、不可创建/取消/批准”的数据库语义。

## 7. Endpoint 权限矩阵

### 7.1 Platform API

| Method / path | Required permission | 额外资源约束 |
|---|---|---|
| `GET /v2/workspaces` | global `workspaces:read` | 只返回 token workspace grants |
| `POST /v2/workspaces` | global `workspaces:create` | owner 固定为 token subject |
| `GET /v2/workspaces/{workspaceId}` | `workspaces:read` | grant workspace 精确匹配 |
| `PATCH /v2/workspaces/{workspaceId}` | `workspaces:update` | grant workspace 精确匹配 |
| `POST /v2/workspaces/{workspaceId}:archive` | `workspaces:archive` | active run/资源按归档合同收口 |
| `GET /v2/workspaces/{workspaceId}/members` | `members:read` | grant workspace 精确匹配 |
| `POST /v2/workspaces/{workspaceId}/members` | `members:add` | 不能移除/降级最后一个 owner |
| `PATCH /v2/workspaces/{workspaceId}/members/{userId}` | `members:update` | 不能移除/降级最后一个 owner |
| `DELETE /v2/workspaces/{workspaceId}/members/{userId}` | `members:remove` | 不能移除最后一个 owner |
| `GET /v2/workspaces/{workspaceId}/executors` | `executors:read` | grant workspace 精确匹配 |
| `POST /v2/workspaces/{workspaceId}/executors` | `executors:create` | 新 executor 归属该 workspace |
| `POST /v2/workspaces/{workspaceId}/executors/{executorId}:enrollmentToken` | `executors:enroll` | executor 必须归属该 workspace |
| `PATCH /v2/workspaces/{workspaceId}/executors/{executorId}` | `executors:update` | executor 必须归属该 workspace |
| `POST /v2/workspaces/{workspaceId}/executors/{executorId}:archive` | `executors:archive` | executor 必须归属该 workspace |
| `GET /v2/workspaces/{workspaceId}/llm-gateways` | `llm-gateways:read` | 只投影当前用户 grant 状态 |
| `POST /v2/workspaces/{workspaceId}/llm-gateways` | `llm-gateways:create` | Gateway 归属该 workspace |
| `PATCH /v2/workspaces/{workspaceId}/llm-gateways/{gatewayId}` | `llm-gateways:update` | owner-only；OIDC discovery、乐观锁、版本和 grant fence |
| `POST .../{gatewayId}:disable` | `llm-gateways:disable` | Gateway 归属该 workspace |
| `POST .../{gatewayId}:authorize` | `llm-gateway-grants:authorize` | grant user 固定为 token subject |
| `POST .../{gatewayId}:completeAuthorization` | `llm-gateway-grants:authorize` | transaction user 与 token subject 相等 |
| `POST .../{gatewayId}:revoke` | `llm-gateway-grants:revoke` | grant user 固定为 token subject |

### 7.2 Browser API

| Method / path | Required permission | 额外资源约束 |
|---|---|---|
| `GET /v2/workspaces/{workspaceId}/sessions` | `sessions:read` | token 绑定 workspace；只返回 token subject 的 session |
| `POST /v2/workspaces/{workspaceId}/sessions` | `sessions:create` | creator 固定为 token subject |
| `GET /v2/workspaces/{workspaceId}/sessions/{sessionId}` | `sessions:read` | session creator 为 token subject |
| `PATCH /v2/workspaces/{workspaceId}/sessions/{sessionId}` | `sessions:update` | session creator 为 token subject |
| `POST /v2/workspaces/{workspaceId}/sessions/{sessionId}/actions/archive` | `sessions:archive` | session creator 为 token subject |
| `GET /v2/workspaces/{workspaceId}/sessions/{sessionId}/transcript` | `sessions:read` + `runs:read` | session creator 为 token subject |
| `POST /v2/workspaces/{workspaceId}/sessions/{sessionId}/runs` | `runs:create` | session creator 为 token subject |
| `GET /v2/workspaces/{workspaceId}/runs/{runId}/events` | `runs:read` | run actor 为 token subject |
| `POST /v2/workspaces/{workspaceId}/runs/{runId}:cancel` | `runs:cancel` | run actor 为 token subject |
| `POST /v2/workspaces/{workspaceId}/approvals/{approvalId}:decide` | `approvals:decide` | approval 属于 token subject 的 live run |
| `POST /v2/workspaces/{workspaceId}/sessions/{sessionId}/agui` | 按内部动作分别检查 | create/read/cancel/decide 不因共用 AG-UI endpoint 合并成一个 scope |

最后一行要求 browser-gateway 不能仅因 `/agui` 请求带了一个宽泛 `runs:write` 就允许所有操作。AG-UI
请求中的具体动作必须映射到上表对应 permission。

## 8. Hydra consent 与 workspace 绑定

两个 client 在 Hydra 注册时声明各自完整的最大 scope 集合。authorization request 可以请求其中一个
子集；Core consent policy 最终只 grant：

```text
requested scopes
∩ client 最大 scope
∩ 当前 subject 在目标资源上的角色权限
```

Platform 登录不指定单个 workspace。consent policy 枚举当前用户的 active membership，生成
`global_permissions` 和有界的 `workspace_grants`。若创建 workspace 或角色变化使快照变化，前端重新
走一次 Hydra authorization；外部 IdP 登录通常可复用现有登录态。

Browser authorization 必须携带一个规范 workspace resource，而不是让 callback 后再补 workspace。
首选标准形式是 OAuth resource indicator：

```text
resource=urn:agentserver:workspace:<canonical-uuid>
```

实现前必须针对固定的 Hydra 26.2.0 做 live conformance，确认 authorization request 中的 resource 能在
login/consent Admin request 中无损读取，并最终由 consent session 写入 opaque token introspection 的
`ext.agentserver`。如果 Hydra 不支持这条标准链路，必须先设计有签名、一次性、绑定 client/redirect/
workspace/expiry 的 authorization-context receipt；不能偷偷回退为浏览器可修改的 header 或 callback
参数。

## 9. 权限变更与撤销

opaque token + 每请求 introspection 的价值是 Hydra revoke 后立即返回 `active=false`。以下操作必须触发
撤销：

- 成员移除、角色降级；
- workspace 归档；
- user 禁用；
- client profile 或 permission registry 版本升级。

Phase 1 可以安全地采用较粗粒度撤销：撤销该 subject 的全部 `agentserver-platform` 和
`agentserver-browser` grant/token，再让用户重新授权。后续只有在 Hydra 能按 workspace resource 精确
定位 grant/token 时才缩小撤销范围。

权限降低操作必须与 consent 串行化：锁定 membership authority、阻止新的 consent，调用 Hydra revoke，
再提交角色/状态变化。Hydra 不可用时权限降低操作 fail closed，不能先修改数据库却留下仍 active 的
高权限 token。这里需要为 Hydra 26.2.0 的 revoke API 建立 live conformance；在证明撤销语义前，不能
声称“权限完全由 Hydra 控制”已经实现。

## 10. 实施门槛

在继续拆 gateway 和开发 UI 前，授权实现必须先满足：

1. permission 常量与 endpoint 映射成为代码中的单一注册表；
2. 两个 Hydra client 只允许自己的最大 scope 和 audience；
3. consent 可以 grant requested scope 的权限子集，不再要求/授予一个固定全量 scope；
4. Hydra consent session 能把 versioned workspace grant 写入 introspection `ext`；
5. Core authorizer 精确校验 client/audience/scope/resource grant，不读取前端自报 role；
6. Browser token 不能访问第二个 workspace，Platform token 不能调用 Browser endpoint，反之亦然；
7. Hydra revoke live test 证明权限降低后 introspection 立即 inactive；
8. 每一个用户 API endpoint 都有 permission matrix contract test，新增 endpoint 未登记权限时构建失败。
