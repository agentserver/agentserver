# ADR 0006：Platform 与 Browser 分离为独立应用和 OAuth authority

- 状态：Accepted
- 日期：2026-08-03
- 影响范围：前端、platform-gateway、browser-gateway、Core、Hydra、生产部署

## 背景

当前 reference web 同时承担对话、executor 注册和 LLM Gateway 配置，并使用一个
`agentserver-browser` public OAuth client。它请求单一 `aud=agentserver-api`，token 同时拥有
`runs:write`、`executors:write` 和 `llm-gateways:write`。这能验证底层 run 链路，但不是最终产品边界：

- workspace、成员和凭据管理是平台控制面，不应出现在对话应用中；
- 用户进入某个 workspace 的 Browser 后，只需要 session、run、event 和 approval 权限；
- 当前 web 需要手填 workspace/session UUID，也没有历史 session 导航，不能作为可用产品前端。

这次拆分的首要原因是产品职责不同：Platform 管理 workspace 本身及其成员、executor、LLM Gateway
和个人凭据授权；Browser 管理某个 workspace 内的 session 历史、新建对话、消息、run、工具过程、
审批和取消。目标是把 AgentServer 呈现为一个平台应用和一个共享、多租户的 Browser 应用。Browser
不是每个 workspace 单独部署的服务；URL 中的 `workspaceId` 只是 Core 授权根。

Core 继续作为两类数据的统一状态事实源，不复制数据库或业务状态。前端、入口 gateway 和 API
authority 随产品职责分开；双 OAuth client 是让这个职责边界在后端也成立的手段，而不是拆分本身的
目的。如果两个页面共享 access token，仅拆 UI 仍会留下跨职责的后端旁路。

## 决策

### 1. 五个公网 host 的职责

| Host | 职责 | 后端 |
|---|---|---|
| `agent.byted.bps.dev` | Platform SPA、Hydra login/consent bridge、外部 OIDC callback、Platform OAuth callback | `platform-gateway` |
| `browser.byted.bps.dev` | Browser SPA；通过路径选择 workspace；只托管静态资源和 Browser OAuth callback | `browser-gateway`（静态前端入口） |
| `browser-gateway.byted.bps.dev` | Browser REST / AG-UI API | `browser-gateway` |
| `executor-gateway.byted.bps.dev` | agentx enrollment、challenge 和 WSS connect | `executor-gateway` |
| `auth-sg.byted.bps.dev` | Hydra public issuer、authorize/token、discovery 与 JWKS | Hydra public listener |

公网 TLS 继续由 Istio Gateway 终止。组件到 Core 的身份使用各自的 mTLS SPIFFE identity；公网 host
不参与集群内部路由。Hydra issuer 固定为 `https://auth-sg.byted.bps.dev/`；两个 SPA 共享这一授权
服务器，但 Platform 不再代理 Hydra public 流量。Hydra Admin listener 仍只在集群内开放。

### 2. 两个 public OAuth client

两个 SPA 都使用 Authorization Code + PKCE，`token_endpoint_auth_method=none`，不配置 client secret：

| Client | Audience | 权限范围 | 可调用能力 |
|---|---|---|---|
| `agentserver-platform` | `agentserver-platform-api` | Platform permission 注册表的子集 | workspace / member、executor、LLM Gateway 定义与个人 grant 管理 |
| `agentserver-browser` | `agentserver-browser-api` | 绑定单一 workspace 的 Browser permission 子集 | session、run、event、cancel、approval |

完整 permission 名称、角色编译、workspace resource grant 和 endpoint 映射见
[用户权限与 Hydra authority](../AUTHORIZATION.md)。动作使用静态 scope，例如 `sessions:create`；
workspace ID 是 Hydra token 中独立的 resource grant，不拼进动态 scope 名。

Core login bridge 按 Hydra request 的 `client_id` 选择一个闭合 OAuth profile，并要求 requested scope 是
该 profile 最大权限集合的规范子集、audience 精确匹配。consent policy 再按当前 subject 的 workspace
authority 只 grant 允许的交集。数据库中已有的 `hydra_client_id` 是 transaction authority，callback、
resume 和 consent 都必须重新按它选择同一 profile。未知 client、混合 audience、额外 scope 或 profile
漂移一律 fail closed。

Core 为两类 API 使用独立 authorizer。每个前端请求都以 Hydra introspection 返回的 active token、
精确 client/audience/scope 和 versioned workspace grant 判断权限。Core 数据库中的 membership 是 consent
policy 的业务输入，不把角色声明直接当 endpoint authority；数据库仍负责校验 workspace/resource 归属、
session/run 属于 token subject 等业务不变量。

### 3. token 和跳转边界

Platform 与 Browser 不共享 access token，也不把 token 放进 URL、cookie、`localStorage` 或
`sessionStorage`。两个 SPA 各自在页面内存中保存自己的 token：

1. 用户登录 Platform，得到 `agentserver-platform-api` token；
2. 用户从 Platform 进入 `https://browser.byted.bps.dev/workspaces/{workspaceId}`，跳转只携带
   canonical workspace ID；
3. Browser 为该 workspace 独立执行 PKCE，得到仅绑定该 workspace 的
   `agentserver-browser-api` token；
4. Core 每次 introspect，并要求 token resource grant 与 URL workspace 精确相等。

两个 SPA 的 authorize/token endpoint 都是 `auth-sg.byted.bps.dev` 上的绝对 URL。Authorization 是顶层
导航；Hydra token exchange 只允许精确的 `https://agent.byted.bps.dev` 与
`https://browser.byted.bps.dev` CORS origin，且不允许 credentials。Hydra 自己维护 CSRF/session cookie和
authorize continuation，避免反向代理丢失 `Set-Cookie` 或误判 `302/303`。Browser 业务 API 继续位于
`browser-gateway.byted.bps.dev`，只允许精确 Browser frontend origin。

外部身份认证仍复用一个部署级 confidential OIDC client。它只服务 Hydra login bridge，redirect URI
保持 `https://agent.byted.bps.dev/auth/oidc/callback`，不等同于两个 SPA 的 Hydra OAuth callback。

### 4. gateway 与 Core 路由边界

新增独立的 `platform-gateway` Deployment、Service、ServiceAccount、NetworkPolicy 和 SPIFFE identity。
Platform 与 Browser 可以复用 HTTP、OAuth 和静态资源库，也可以位于同一 service 镜像，但运行时
identity 和 Core route authority 必须分开：

- `platform-gateway` 只代理 Platform resource API 和 login bridge，不注册 `/oauth2/*`；
- `browser-gateway` 只代理 Browser session/run/approval API，并投影 AG-UI/A2UI；
- Core 的 Platform handler 只接受 platform-gateway identity 和 Platform token；
- Core 的 Browser handler 只接受 browser-gateway identity 和 Browser token；
- executor 与 LLM Gateway 管理路由从 browser-gateway 删除，不保留兼容旁路。

Hydra 的 login/consent URL 只指向 platform-gateway，因此 Core login bridge 的 workload caller 是
platform-gateway。browser-gateway 不需要访问 Hydra Admin，也不能调用 Platform resource handlers。

### 5. workspace、executor 与 session 生命周期

Platform 首版补齐：

- workspace list/create/update/archive；
- 成员 list/add/change-role/remove；
- workspace 默认 executor 绑定；
- executor enrollment 和环境状态；
- LLM Gateway 定义以及当前成员自己的 OAuth grant。

新建 workspace 时必须由 Core 在一个事务中创建 owner membership。workspace 删除首版是归档，不物理
删除 session、run、event、grant 或审计记录。归档 workspace 禁止创建新 session/run，并使现有授权
fail closed。

每个 workspace 首版绑定一个默认 executor。创建 run 时 Core 把当时的 executor ID 与 env authority
冻结到 run launch state；harness、capability 和 executor-gateway 从该 authority 读取，不能继续使用部署级
固定 executor ID。切换默认 executor 只影响后续 run。

Browser 首版补齐：

- session list/create/rename/archive；
- 左侧历史 session 与新建 session；
- 右侧对话、tool lifecycle、approval 和 cancel；
- 刷新页面后从 Core 的 durable transcript/projection 恢复已提交消息与事件。

session 删除同样先归档。一个 session 同时最多一个 active run 的 Phase 1 约束保持不变。

### 6. workspace LLM Gateway 的归属

ADR 0005 的 authority 和 grant 模型保持不变，但所有配置与授权入口迁到 Platform。用户进入 Browser
且 workspace 没有 active default Gateway 或本人没有 active grant 时，browser-gateway 返回稳定的
`llm_gateway_authorization_required` 结果和 Platform workspace URL；Browser 只负责展示并跳转，不接触
Gateway 配置或 OAuth completion API。

### 7. 迁移和发布顺序

该边界按以下顺序落地，避免产生持有错误 authority 的半成品前端：

1. 固化 permission 注册表；Core login bridge 支持闭合的多 OAuth profile、权限子集和 workspace resource
   grant；Hydra setup 同时幂等维护两个 client；
2. 增加 platform-gateway identity、handler 和部署拓扑，同时保留旧 reference web 仅作开发诊断；
3. 增加 workspace/session 数据模型和 API，并把 executor 选择改为 workspace authority；
4. 发布 Platform SPA 与 Browser SPA，切换五条公网 HTTPRoute，其中 Hydra public route 直接指向
   Hydra Service；
5. 删除 browser-gateway 上的 executor / LLM Gateway 管理兼容路由和旧单 audience 合同。

发布期间不接受一个 token 同时拥有两类 audience，也不通过临时 scope 扩大 Browser 权限。若某个阶段
尚未具备对应 UI，API 可以暂时不可达，但不能回退为共享 token。

## 后果

- Platform 凭据和 workspace 控制面从对话应用中移出，XSS 或 token 泄漏的 blast radius 明显缩小；
- 用户从 Platform 进入 Browser 会发生一次独立 OAuth 流程，Hydra 可利用现有登录记忆降低交互成本，
  但两枚 token 的 authority 始终不同；
- 集群新增一个常驻 gateway 和一张 workload certificate，生产拓扑、NetworkPolicy、证书生成与 Chart
  测试都需要更新；
- 现有 reference frontend 不能直接演进为两套产品页面，需要拆出共享 AG-UI/OAuth 库；
- 动态 workspace 在 executor 仍为部署级常量时没有真实可用性，因此 workspace CRUD 与动态 executor
  authority 必须作为同一产品里程碑完成。
