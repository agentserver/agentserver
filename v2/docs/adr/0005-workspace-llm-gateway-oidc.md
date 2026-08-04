# ADR 0005：workspace 自主管理第三方 LLM Gateway 与用户 OIDC grant

- 状态：Accepted
- 日期：2026-08-02
- 影响范围：Core、llmproxy、harness-pool、platform-gateway、生产部署配置

> ADR 0006 后续取代了本文第 6 节的前端/API 归属和宽泛 OAuth scope：LLM Gateway 配置与个人 grant
> 全部属于 Platform，使用细分的 `llm-gateways:*` / `llm-gateway-grants:*` permission；Browser 只在缺少
> active grant 时跳转 Platform。本文的数据所有权、第三方 OIDC grant、存储和 llmproxy 转发决策不变。

## 背景

旧的生产 profile 在部署时固定一个 `/v1/responses` 上游，并把一个平台级静态
credential 文件挂载进 llmproxy。这样虽然 harness 看不到真实 key，但平台仍然成为统一模型
凭据的 owner，也无法保留第三方 Gateway 的逐用户身份、撤销、MFA 和条件访问语义。

目标形态参考 [Claude Desktop 的第三方 Gateway SSO](https://claude.com/docs/third-party/claude-desktop/gateway)：workspace 关联自己的 Gateway 和 OIDC
issuer，每个用户通过 Authorization Code + PKCE 完成授权，Gateway 在每次推理请求中收到该用户
自己的 OIDC bearer。平台不维护可调用模型的共享 API key，只转发 workspace 已配置的 Gateway。

AgentServer 与桌面客户端存在一个不可忽略的区别：模型进程和 run 位于服务端，并且 run 可在浏览器
关闭后继续。若平台完全不保管 OAuth grant，则短期 bearer 过期后无人值守 run 必然中断。因此这里的
“没有平台统一模型 key”不等于“Core 不接触任何 token”：Core 会加密保管每个
`(workspace, gateway, user)` 的 OAuth grant，并只在该用户自己的 run 上使用。

## 决策

### 1. 所有权和数据模型

Core 是以下状态的唯一权威来源：

- `workspace_llm_gateways`：workspace 管理的 Gateway 配置，包括精确 Responses endpoint、OIDC
  issuer、public client ID、scopes、要转发的 bearer 类型、默认 model、状态和单调递增版本；
- `workspace_llm_gateway_grants`：每个 workspace 成员自己的授权，绑定 Gateway、用户、OIDC
  immutable subject，保存经独立 key 加密的 token set，并记录 active / reauth-required / revoked；
- `workspace_llm_gateway_auth_transactions`：有界、一次性的 state / nonce / PKCE / browser binding
  事务；明文只保存查找所需的 SHA-256，secret 全部密封；
- `run_launch_states` 中的模型 authority：Gateway ID、Gateway config version、grant user 和 model。

首版允许一个 workspace 配置多个 Gateway，但同一时刻只有一个 active default。创建 run 时不从
harness 或部署配置选择路由，而是把当时的 default Gateway、当前 config version、发起用户和默认
model 原子冻结到 run launch authority。精确幂等重试恢复已冻结的绑定，不跟随后来变更的 default。

只有 owner 可以创建（可设为 default）和禁用 Gateway；owner 和 developer 必须分别授权自己的 grant。viewer、
service principal、owner token 共享和 workspace 级共享 grant 首版均不支持。

### 2. OAuth/OIDC profile

Gateway 授权使用 public-client Authorization Code + PKCE，不要求 workspace 向 AgentServer 提供
OIDC client secret。配置字段为：

- issuer；
- client ID；
- scopes，最小默认值为 `openid offline_access`；provider-specific scope 由 workspace 显式添加，且第三方 client 必须允许最终的完整 scope 集合；
- `bearerTokenType = id_token | access_token`；
- 精确 redirect URI，由部署固定为 AgentServer 前端 callback；
- 精确 `/v1/responses` endpoint 和默认 model。

Core 必须通过 discovery 验证 metadata 中的 issuer 完全相等，并拒绝 metadata、authorization、token、
JWKS redirect。code exchange 必须验证 PKCE、nonce、ID token signature、`iss` 和 `aud=client_id`。
Gateway 自身也必须验证收到 bearer 的 signature、issuer 和 audience；只验签名或 issuer 不足以隔离同一
tenant 下签发给其他 client 的 token。

`id_token` 模式把经验证的 ID token 原样转发；`access_token` 模式把 token endpoint 返回的 access
token 原样转发。Core 不增删 claim，不把 AgentServer 用户 bearer 换成 Gateway bearer，也不允许自定义
认证 header。首次 code exchange 必须实际返回 refresh token，否则不创建 active grant，不能仅凭申请了
`offline_access` 就假设已有离线授权。刷新在 bearer 到期前由 Core 完成；刷新失败、没有可用 refresh
token、subject 改变或新 token 校验失败时，grant 进入 `reauth_required` 并 fail closed。

长期 grant 使用独立的 AES-256-GCM keyring 和带 workspace/gateway/user/version 的 AAD。这个 sealing
key 由 Pulumi 随机生成，只能解密数据库中的 grant，不能直接调用任何模型，因此不属于统一模型
credential。它不得复用登录事务 key、run capability key 或对象存储凭据。密封格式必须支持 active key
和旧 key overlap，后续才能无停机轮换。

### 3. run 和 capability 绑定

`aud=llmproxy` 的 run capability 除公共 run/attempt/actor/holder/deadline claim 外，必须绑定：

- Gateway ID；
- Gateway config version；
- model；
- 固定 provider `workspace-gateway`。

harness-pool 从 Core 的 run launch projection 得到该绑定，只负责把它机械放入签名 manifest。内部
llmproxy endpoint、SPIFFE identity 和 audience 仍来自部署 profile；harness 不能看到第三方 endpoint
或 OAuth token。

每个模型请求中，llmproxy 先本地验证 capability，再携带该 bearer 向 Core 做 live-authorize。Core 在
同一个只读 authority snapshot 中重新检查：

- run/attempt/lease/generation 仍有效；
- actor 仍是 owner/developer；
- run 冻结的 Gateway ID、config version、grant user 和 model 与 capability 完全一致；
- Gateway 仍 active 且版本未变化；
- 该用户 grant 仍 active。

之后 Core 才刷新或解封当前短期 bearer，并向 **llmproxy 专用** response 返回精确 endpoint 和
`Authorization: Bearer ...`。secret 字段不能加入 executor 共用的 authorization response。该 internal
endpoint 只接受 llmproxy SPIFFE identity，返回 `Cache-Control: no-store`；llmproxy 不做正向授权或 token
缓存。禁用 Gateway、修改配置、撤销 grant、移除成员、取消/fence run 或 refresh 失败都会使下一次请求
立即失败。

### 4. llmproxy 是闭合转发器，不是协议转换器

stock Codex 当前调用 OpenAI Responses API，所以首版 Gateway 必须实现：

- `POST /v1/responses`；
- streaming；
- stock Codex 使用到的 tool-call 语义。

参考文档描述的是 Anthropic `/v1/messages`，不能据此在 llmproxy 内隐式做 Messages ↔ Responses
转换。未来若需要 Messages，应新增独立、显式版本化的 provider adapter；当前 llmproxy 只转发规范化
后的原始 Responses body、有限 request/response header 和 streaming body。

第三方 endpoint 每次都来自 Core live-authorize。llmproxy 必须再次校验它是无 userinfo、query、fragment
和 redirect 的精确 system-trusted HTTPS `/v1/responses` URL，永不跟随 3xx，并覆盖而不是转发 run
capability header。

### 5. SSRF 与 egress

workspace 可配置 URL 意味着旧的“部署时一个固定 CIDR”模型不再成立。首版只支持公网 Gateway 和公网
OIDC issuer：

- URL 只允许 system-trusted HTTPS，默认端口 443；
- DNS 解析和实际 dial 必须是同一次受控操作；拒绝 literal IP、loopback、link-local、multicast、
  unspecified、RFC1918、ULA、CGNAT、metadata、cluster/service/pod 和其他 special-use 地址；
- redirect 一律拒绝；
- OIDC discovery、JWKS 和 token HTTP response 在进入第三方 OIDC decoder 前统一限制为 2 MiB；
- Kubernetes egress 只开放公网 TCP/443，并用 `ipBlock.except` 排除私网和 special-use CIDR；
- Core 和 llmproxy 分别执行应用层目的地址校验，NetworkPolicy 是第二层约束。

需要访问企业内网 Gateway 时，不能让 workspace 用户自行放开私网。后续应增加平台管理员维护的
Gateway egress zone / egress proxy，按 zone ID 选择受审计的出口；在该能力完成前内网 endpoint 被拒绝。

### 6. Platform API 和浏览器流程

platform-gateway 转发的 Platform resource API 提供：

- `POST/GET /v2/workspaces/{workspaceId}/llm-gateways`；
- `PATCH /v2/workspaces/{workspaceId}/llm-gateways/{gatewayId}`；
- `POST /v2/workspaces/{workspaceId}/llm-gateways/{gatewayId}:authorize`；
- `POST /v2/workspaces/{workspaceId}/llm-gateways/{gatewayId}:completeAuthorization`；
- `POST /v2/workspaces/{workspaceId}/llm-gateways/{gatewayId}:revoke`；
- `POST /v2/workspaces/{workspaceId}/llm-gateways/{gatewayId}:disable`。

对应权限拆分为 `llm-gateways:read`、`llm-gateways:create`、`llm-gateways:update`、`llm-gateways:disable`、
`llm-gateway-grants:authorize` 和 `llm-gateway-grants:revoke`，完整角色编译见 ADR 0006 与
`AUTHORIZATION.md`。个人 authorize / complete / revoke 的 grant owner 固定为 Hydra token subject；
disable 原子清除 default、把状态设为 disabled 并递增配置版本，因此旧版本 run 在下一次 live-authorize
时立即失败。active Gateway 支持 owner-only 原地修改：Core 在任何写入前重新执行安全 OIDC discovery，
PostgreSQL 以 `expectedVersion` 乐观锁提交配置并递增版本，同时把该 Gateway 的所有 active grant 标为
`reauth_required`；旧 sealed token 和旧版本 run 随即 fail closed。disabled Gateway 仍不能修改或重新启用。

前端在当前页面内存中保存随机 browser binding、workspace、Gateway ID 和 popup 引用，不写入
`sessionStorage`。OIDC callback 落到前端 origin 的最小静态页面；它只把 state/code/error 通过限定
target origin 的 `window.opener.postMessage` 交回原页面，并同时用固定版本名的同源
`BroadcastChannel` 作为 COOP 切断 opener 后的回退；原页面必须再次匹配服务端 authorization URL 中的
高熵 state，不能只凭广播来源完成事务。callback 不接触 AgentServer bearer，原页面再带内存中的
AgentServer bearer 和 browser binding 调 completion API。Core 要求 API 用户与 auth transaction 中的 user
完全相同。第三方页面的 `Cross-Origin-Opener-Policy` 可能让仍打开的 popup 在原页面表现为 closed，因此
前端不以 `popup.closed` 提前销毁 PKCE 事务，而以服务端签发的过期时间为权威上界。AgentServer 页面使用
`Cross-Origin-Opener-Policy: same-origin-allow-popups` 保留兼容的 opener 快速路径；callback 仍使用
no-store、no-referrer 和 hash-locked CSP。整个流程不依赖跨
`agent.byted.bps.dev` 与 `browser-gateway.byted.bps.dev` 的 cookie。

### 7. 部署配置变化

删除以下平台级模型输入和 llmproxy Secret material：

- `runtime.upstreamResponsesUrl`；
- `runtime.upstreamAuthHeader`；
- `runtime.model` / `runtime.provider`；
- `upstreamCaPem`；
- `upstreamCredential`；
- `upstream-ca.crt` / `upstream-credential`。

生产部署只新增 Core 的随机 `llm-gateway-sealing-key` 和固定 public callback URL。llmproxy Secret 仅保留其
workload TLS identity 与 run-capability public keyring。

## 后果

- 平台不再拥有一个能代表所有 workspace 调用模型的 key；第三方 Gateway 能保留逐用户审计和 IdP
  撤销语义。
- Core 成为 OAuth grant custodian，数据库泄漏与 sealing key 泄漏必须同时发生才能恢复 token；Core 和
  llmproxy 内存仍属于敏感面，需要禁止 body/header/token 日志。
- Gateway 配置变更会 fence 绑定旧 config version 的未完成 run。这是刻意的 fail-closed 语义，UI 修改前
  必须提示影响。
- 首版只能使用 Responses-compatible、公网、system-trusted Gateway。Messages adapter、内网 egress zone、
  service-principal grant、model discovery 和多 Gateway 运行时选择作为后续独立功能实现。
