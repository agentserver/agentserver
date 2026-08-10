# Agentserver v2 SG 生产部署

本文只描述 SG 生产部署。当前生产配置是封闭的，不支持多地域或多架构复用：

- Pulumi stack 必须为 `sg`；
- Kubernetes 节点必须为 `linux/amd64`；
- Namespace 固定为 `agentserver`，Helm release 固定为 `agentserver-v2`；
- 发布源固定为 `ghcr.io/agentserver/*`，SG 集群经 registry mirror
  `registry-sg.byted.cs.ac.cn/ghcr/agentserver/*` 匿名拉取；
- 公网 TLS 由 `istio-ingress/istio-gateway` 的 `https-byted-bps` listener 终止；
- 应用的公网后端使用集群内明文 HTTP，组件之间的内部控制面继续使用 TLS/mTLS；
- prompt/checkpoint 以明文对象写入 S3-compatible bucket，应用仍逐次校验 pointer、size 和 SHA-256。

生产 Chart 不是通用 `values.yaml`。`agentserver-deploy chart` 会把一份通过 closed-world
校验的 `production.json` 生成为环境锁定 Chart。镜像、网络、平台登录 OIDC 或对象存储配置发生
变化时，必须重新生成并发布 Chart，不能在 Helm/Pulumi 安装时临时覆盖。

这里的 closed-world 只约束平台基础设施。模型路由不再锁进 Chart：workspace owner 在部署后关联
Responses-compatible 的第三方 LLM Gateway，每个成员再用 OIDC Authorization Code + PKCE 授权自己的
grant。平台没有统一的模型 API key。

`managedExecutor` 有三个显式且不兼容回退的 stage：`disabled`、`policy-bootstrap`、`active`。
Lark credential delivery mode 不属于 release 或 Chart；它是每个 workspace 自己的配置，workspace owner
通过 Platform/API 显式选择 `webhook_swap` 或 `process_env`。`policy-bootstrap` 只部署生产 TLS 的 deny-only
Policy Webhook；`active` 才会部署 TAE managed executor vertical slice：
`harness-pool → executor-gateway → sandbox-gateway → TAE`，同时保留统一的
`TAE Agent Gateway → egress-authorizer → Core` 请求授权链。`process_env` 由 executor-gateway 在精确进程
启动前向 Core 解析真实 Lark token，并为后续请求附带 operation-bound proof；它不会绕过 Policy Webhook。
sandbox 内运行 digest-pinned Linux/amd64 `lark-cli` 和只读 skill；模型仍
只看到既有 `list_environments`、`shell`、`read_file` MCP catalog。TAE policy 不通过 CreateSession 参数
伪造，而由已发布的 PSM/网络策略 binding digest 校验，并在 egress-authorizer 端逐请求复验。
代码和 provider-linked binary 已接入本地生产渲染，但真实 SG ByteCloud 应用 JWT/TAE/Lark、Webhook 可达性、IPv4/IPv6
与 zero-secret 门禁仍须在目标集群执行，本文不把本地测试当作上线证据。

## 1. 固定公网拓扑

| Host | 公开路径 | 后端 |
| --- | --- | --- |
| `agent.byted.bps.dev` | Platform SPA、`/auth/config`、LLM Gateway callback、Platform `/v2*`、`/readyz` | platform-gateway HTTP `8080` |
| `browser.byted.bps.dev` | Browser SPA、`/reference*`、`/auth/config`、`/readyz` | browser-gateway HTTP `8080` |
| `browser-gateway.byted.bps.dev` | `/v2*` | browser-gateway HTTP `8080` |
| `executor-gateway.byted.bps.dev` | `/internal/v2/agentx/enrollments`、`/internal/v2/agentx/challenges`、`/internal/v2/agentx/connect` | executor-gateway HTTP `8080` |
| `auth-sg.byted.bps.dev` | Hydra public issuer：`/oauth2/*`、discovery、JWKS；以及三个精确 login/consent/callback 路径 | 协议路径到 Hydra public HTTP `4444`；`/auth/hydra/login`、`/auth/hydra/consent`、`/auth/oidc/callback` 到 platform-gateway HTTP `8080` |

六条 `HTTPRoute`（五个 host；auth host 按路径使用两条 Route）都挂到：

```text
namespace:   istio-ingress
gateway:     istio-gateway
sectionName: https-byted-bps
```

Chart 不创建 Gateway、证书、LoadBalancer 或 external-dns 资源。现有 Istio listener 必须：

1. 持有覆盖上述五个域名的公网证书；
2. 允许 `agentserver` Namespace 中的 HTTPRoute attach；
3. 将 TLS 终止后的 HTTP 请求转发给 ClusterIP Service。

Browser API 只允许 `https://browser.byted.bps.dev` 作为 Origin，并跨域访问
`https://browser-gateway.byted.bps.dev/v2`。Platform 和 Browser SPA 都直接跳转到
`https://auth-sg.byted.bps.dev/oauth2/auth`，并直接向同一 authority 的 `/oauth2/token` 做
Authorization Code + PKCE exchange；Hydra public CORS 只允许这两个精确 SPA origin，且不允许
credentials。Platform 不代理 `/oauth2/*`。Hydra 的 CSRF/session cookie、authorize continuation、
login、外部 OIDC callback、consent 和 `302/303` 跳转全部在 `auth-sg.byted.bps.dev` 闭环；只有上述
三个精确 `/auth/*` 路径由 Gateway API 分流到 login bridge。executor 的公网 listener
绝不注册 `/mcp`；`/mcp` 只存在于 executor-gateway 的内部 TLS `8443` listener，并只允许
harness-pool Pod 访问。

## 2. Chart 与 Pulumi 的管理边界

Chart 管理：

- managed executor 关闭时有 7 个常驻 Deployment：core、platform-gateway、browser-gateway、executor-gateway、harness-pool、llmproxy、Hydra；
- `policy-bootstrap` 只增加 deny-only `egress-authorizer`，共 8 个常驻 Deployment；它没有
  Core egress、workspace credential、ZTI verifier、TAE provider 或 sandbox authority；
- `active` 增加 `sandbox-gateway` 与完整 `egress-authorizer`，共 9 个常驻 Deployment；
- executor-gateway 固定单副本、`Recreate`、无 HPA/PDB；
- managed executor 关闭时有 6 个固定 ClusterIP Service、6 条 HTTPRoute；bootstrap/active 都增加
  egress-authorizer Service、公开 Webhook HTTPRoute 与 BackendTLSPolicy，active 再增加 sandbox-gateway Service；
- managed NetworkPolicy 始终包含 egress-authorizer 的闭合 ingress/egress；workspace mode 不改变部署拓扑；
- Hydra SQL migration（weight `-20`）和 AgentServer migration（weight `-10`）
  `pre-install,pre-upgrade` Job；
- Hydra Platform/Browser 两个 public OAuth client setup（weight `-10`）和幂等 AgentServer bootstrap（weight `0`）
  `post-install,post-upgrade` Job。

Job 只用于控制面安装期的数据库 migration、Hydra client setup 和 bootstrap。每个 run 的 harness 仍由 harness-pool 在本 Pod 内普通
`fork/exec` 一个短命 harness-worker，不创建 Kubernetes Job 或 Pod。

`../k8s-byted/apps/agentserver.ts` 只在 `sg` stack 生效，并负责创建：

- `agentserver` Namespace；
- 内部 ECDSA CA、8 个带精确 SPIFFE URI 的 workload 证书（包含独立 platform-gateway identity 与 Hydra server identity）；
- run-capability 与 run-manifest 两套 Ed25519 signer/keyring；
- executor enrollment、login transaction、run cursor 和 workspace LLM Gateway grant sealing 四个独立的
  256-bit key/keyring；
- AgentServer/Hydra 两个独立的 32-byte 随机 PostgreSQL owner 密码与
  `kubernetes.io/basic-auth` Secret；
- `agentserver-postgres` CloudNativePG Cluster：3 个实例，每实例 50Gi Longhorn；
- 独立 `hydra` role 和由 CNPG `Database` CR 管理的 `hydra` database；
- CNPG 1.30.0 支持的 PostgreSQL 17.6 system-trixie amd64 manifest（tag + digest 双重固定）；
- 以 `kubernetes.io/hostname` 为 topology key 的 required Pod anti-affinity，三个实例不能落在同一节点；
- 指向 `agentserver-postgres-rw.agentserver.svc.cluster.local:5432` 的 AgentServer/Hydra 独立 DSN；
- Hydra system/cookie secret；
- base profile 使用 9 个应用 Secret；managed profile 额外使用 sandbox、egress-authorizer、Core workspace
  credential keyring 与 placeholder/proof signer 相关 Secret；这些只描述组件身份、
  keyring 和基础设施，不承载任何 workspace Lark/ByteCloud/GitHub credential；
- 环境锁定的 OCI Helm release。Release显式使用`atomic=false`和`cleanupOnFail=false`：失败资源会保留供排障，修复仍由下一次Pulumi更新收敛，不自动卸载现场。

证书和密钥由 Pulumi `tls`/`random` provider 生成，用户不需要手工生成文件、选择 Secret 名称
或执行 `kubectl create secret`。Deployment 带 Reloader annotation，受引用 Secret 更新时会
触发 rollout。Namespace、CNPG Cluster/Database、PostgreSQL owner Secret 和 9 个托管 Secret 都设置
`protect: true`：正常轮换仍可原地更新，但误关模块或普通 `pulumi destroy` 不能删除数据库和密钥。

以下值是外部系统已经认可的授权，不能用随机字节替代；Pulumi 只负责安全接入并组装最终
Kubernetes Secret：

| Pulumi 接入值 | 来源 |
| --- | --- |
| `externalOidcClientSecret` | 外部 OIDC 中已注册的 AgentServer client |
| `s3AccessKeyId` / `s3SecretAccessKey` | 目标 S3-compatible bucket 的真实 credential |
| `byteCloudAccessKeyId` / `byteCloudSecretAccessKey` | 仅用于一次性 TAE 网络探针和 active sandbox-gateway 调 TAE control/data plane 的基础设施应用账号；workspace ByteCloud credential 不在 Pulumi 中配置 |

如果这些外部系统由 Pulumi provider 管理，应直接把对应资源 Output 传给 AgentServer 模块；当前
模块也保留加密 Pulumi config 接口作为接入点。不要生成一个外部系统从未注册的随机密码并把它
当成可用 credential。模块会拒绝空值、首尾空白、控制字符和超出协议上限的 credential。

PostgreSQL 不再是外部输入。Pulumi 自动生成：

```text
postgres://agentserver:<generated-hex-password>@agentserver-postgres-rw.agentserver.svc.cluster.local:5432/agentserver?sslmode=require
postgres://hydra:<generated-hex-password>@agentserver-postgres-rw.agentserver.svc.cluster.local:5432/hydra?sslmode=require
```

两个密码 Secret 分别用于 CNPG role reconciliation；AgentServer owner 还用于 `initdb`。DSN 只写入
`agentserver-core-secrets/database-url` 和 `agentserver-hydra-secrets/database-url`。用户不需要填写或读取密码。`../k8s-byted` 仍需先安装
CNPG operator 和 Longhorn；AgentServer 模块管理 Cluster 声明，但不替代数据库备份与恢复策略。

`externalOidcClientSecret` 只属于 **AgentServer 平台登录** 的 confidential client。workspace LLM
Gateway 使用 workspace 自己在第三方 IdP 注册的 public client ID + PKCE，不向 Pulumi 或 AgentServer
上传 client secret。

Hydra 26.2.0 由同一 Chart 管理。Chart 固定 issuer `https://auth-sg.byted.bps.dev/`、Admin/Public endpoint、
PKCE、opaque access token、双副本 Deployment 和 browser public client profile；Pulumi 管理它的
database、DSN、内部 TLS 与 system/cookie secret。Hydra public `4444` 在 Pod 内使用明文 HTTP，由
Istio 终止公网 TLS；Admin `4445` 继续使用集群内 TLS，且不创建公网路由。Chart/Pulumi 不管理外部
OIDC IdP、S3 bucket、第三方 LLM Gateway、Istio
Gateway、DNS 和用户机器上的 agentx。

运行时不会再启动`materialize-*` init container或复制Secret。Chart把每个closed-world Secret key
以只读`subPath`文件挂载；该文件在单个Pod生命周期内保持固定，Secret轮换通过Reloader创建新Pod接收。
harness-pool只保留目录准备和network guard两个init container。

`disabled` 和初始 `policy-bootstrap` 不要求 ByteCloud AK/SK，缺失时不能阻塞普通 `pulumi up`。
安全审批完成后，运行一次性网络探针之前必须把 AK/SK 作为一对 Pulumi secret 原子接入；只配置其中
一个会 fail closed。`active` stage 的 `agentserver-sandbox-secrets` 必须精确包含 `ca.crt`、`tls.crt`、`tls.key`、
`sandbox-capability-keyring.json`、`bytecloud-access-key-id` 和 `bytecloud-secret-access-key`。最后两个 key
只挂载到 `sandbox-gateway`，通过文件路径传入；Chart 不把其内容写入环境变量。它们只用于 TAE
基础设施调用，不能与 workspace credential binding 复用。workspace ByteCloud AK/SK 只能由 Platform
写入 Core workspace credential service 的 canonical binding。provider 用官方
ByteCloud Auth SDK 换取短期 JWT，并以 `X-Jwt-Token` 同时访问 TAE control-plane 与 sandboxd data-plane。
SG production 的 Terminal Sandbox PSM 固定为 `bytedance.sandbox.agentserver`，并在 provider 与 policy
binding 做 exact-match 校验，并始终在 egress-authorizer 复验。Policy Webhook 使用
`https://egress-authorizer-sg.byted.bps.dev/v1/policy`；Istio Gateway 终止公网 TLS，Gateway 到
egress-authorizer Service 使用 BackendTLSPolicy 和 `agentserver-egress-backend-ca` ConfigMap 做 TLS
校验。
SG 生产 exchange origin 固定为 `https://cloud-i18n-sg.bytedance.net`，通过
`AGENTSERVER_V2_TAE_BYTECLOUD_JWT_ENDPOINT` 显式注入并精确校验；JWT exchange、TAE control-plane 与
sandboxd data-plane 的 transport 全部固定使用
`socks5h://ssh-egress-merlin-i18nbd-syd2a-83092.ssh-egress.svc.cluster.local:1080`，由
`AGENTSERVER_V2_TAE_PROXY_URL` 精确校验。标准 `HTTP_PROXY` / `HTTPS_PROXY` / `ALL_PROXY` 仍被拒绝。
control/data transport 在 dial 前分别校验固定 control host 和 canonical per-session data host，DNS 在
syd2a 侧完成。sandbox-gateway NetworkPolicy 只允许访问 `ssh-egress` namespace 中
`app=ssh-egress-merlin-i18nbd-syd2a-83092` 的 TCP 1080；`sandboxExternalEgress` 为空，不放行任何 TAE
origin 直连 CIDR。
启动时会强制刷新一次作为 readiness，之后由 SDK 缓存并自动刷新；TAE 明确返回 401 时只刷新下一次请求
使用的身份，不重放已经写出的 create/process 操作。

### 2.1 Workspace Managed Lark credential mode

mode 的唯一事实源是 Core 数据库的 `workspaces.managed_lark_credential_mode`。创建 workspace 时必须显式提交；
只有 workspace owner 可以用当前 workspace version 做 CAS 更新。每次 mode 变化都会推进 workspace version 并写
`workspace_managed_credential_mode_events` 审计。生产配置、Helm values/schema、Pulumi 和 workload environment
均不存在 mode 字段或默认/fallback。

| Workspace mode | 进程启动 | TAE Policy Webhook | 安全边界 |
| --- | --- | --- | --- |
| `webhook_swap` | 只向精确 `lark-cli` 注入短期签名 placeholder | 验证 placeholder、live operation/mode/binding/version 后把 Authorization 换成真实 token | sandbox 中没有真实 token；每次请求强制 host/path/method 与 live revoke |
| `process_env` | Core 复核 workspace mode 与 exact live `shell + lark-cli + TAE process_start` 后返回 access token，并同时注入短期 operation proof | 验证 proof、live workspace mode/binding/version，并常量时间比较请求 bearer；放行时清洗 trace header | 真实 token 只进入目标 `lark-cli` 及子进程；每次请求仍受相同 host/path/method policy 与 live revoke 约束 |

两种 mode 使用同一个 TAE PSM、同一份 `publicWebhookRequired=true` policy、同一个 egress-authorizer、同一套
credential sealing 与 placeholder/proof key material。没有兼容、自动探测或跨 mode fallback。owner 切换 mode
不需要修改 production document、重发 Chart 或重做 TAE evidence；executor-gateway 在每次进程启动时重新查询
Core。切换会立即阻止旧 mode 的后续 resolve/请求：数据库 live-authority 查询同时匹配 workspace 当前 mode、
binding authority version 和 credential version。

`process_env` 的 direct resolve 接口只允许 executor-gateway mTLS identity，响应强制
`Cache-Control: no-store`，只返回 access token，不返回 refresh token。operation proof 不超过 1024 字节，
绑定 workspace/session/run/attempt/execution/operation/sandbox/binding/version/policy/expiry；Webhook 与 Core
都会验签。审计失败、Core 不可达、proof 缺失或篡改、mode/binding/version 不匹配均 fail closed。

### 2.2 TAE policy 两阶段引导

TAE 要求 Webhook URL 先可达，才能在控制台发送测试请求、发布网络策略并发起安全
审批；正式 managed execution 又必须在策略发布和审批完成后才能启动。发布流程因此使用同一个 Helm
release 的两个严格阶段：

1. 从 active 模板生成 `policy-bootstrap` 配置。该转换会清空 policy 的
   `published/approved/evidenceRef`、全部 network evidence、runtime profile 与 pack lock；
2. CI 发布并由 Pulumi 升级到 bootstrap Chart。此时公开 URL 返回合法的 `200 + v1 deny`，且只挂载
   egress-authorizer 的 `tls.crt/tls.key`；
3. 在 TAE 控制台测试 URL、发布并完成安全审批；
4. 通过 Pulumi 把 TAE 基础设施 AK/SK 原子写入 `agentserver-sandbox-secrets`，并把实际 policy
   revision 传给已部署的 bootstrap Chart。Chart 默认不渲染探针；显式启用后由同一个 Pulumi release
   创建一次性 `tae-network-probe`，只挂载 AK/SK，并只放行 DNS 与 syd2a TCP 1080；
5. 保存探针生成的 canonical JSON，上传到不可变 evidence store。activation 命令直接解析报告、核对
   Pod/ServiceAccount、bootstrap 配置 SHA、policy revision、20 次 JWT/control 尝试、完整 lifecycle、
   42MB pinned CLI 下载摘要与资源清理，再自行计算 report SHA-256；
6. 使用审批引用和报告引用执行唯一的 activation 命令；该命令原子计算 policy、network、runtime 和
   pack binding；
7. CI 发布 active Chart，Pulumi 对同一 release 做普通升级。只有这一步才会启动 sandbox-gateway，
   并向 Core/executor/harness 开启 managed authority。

bootstrap 请求无论内容、ZTI 或 credential 如何都不能返回 allow。它不访问 Core，也不读取 placeholder
keyring。不得用伪造 `published=true` 或模板 evidence 绕过这个阶段。

所有 workspace mode 共用这条 `policy-bootstrap → active` promotion 和同一份 TAE host/Webhook/no-bypass
证据。bootstrap Chart 始终部署 deny-only Webhook，active Chart 始终部署完整 egress-authorizer。

## 3. 部署前必须准备的真实输入

发布前必须确认：

1. SG 集群只有 `linux/amd64` 目标节点，且容量足够；
2. CNI 实际执行 Kubernetes NetworkPolicy；集群已安装 Gateway API CRD（含 `BackendTLSPolicy`）、Istio、Reloader、
   CloudNativePG operator 和 Longhorn；
3. `https-byted-bps` listener 的证书和 `allowedRoutes` 已覆盖五个固定域名/Namespace；用户已为
   `auth-sg.byted.bps.dev` 配置与现有 Istio 公网入口相同的 DNS 记录；
4. Longhorn 至少能为 3 个 PostgreSQL 实例各提供 50Gi 卷，并有跨节点调度容量；
5. 外部 OIDC issuer/client 已配置，redirect URI 精确为
   `https://auth-sg.byted.bps.dev/auth/oidc/callback`，并取得 owner 的精确 `sub`；旧 Platform host callback
   不能替代这一登记，因为 `__Host-` login binding cookie 不跨域；
6. S3 endpoint、signing region、bucket、prefix、path-style 和 credential 已确定；
7. S3-compatible 服务支持 single-part `PutObject` 的 `If-None-Match: *`，并把已存在对象明确返回
   为 `412 PreconditionFailed`；
8. 至少一个待接入的第三方 Gateway 支持公网、系统信任 HTTPS 的精确 `/v1/responses` endpoint，
   streaming 和 stock Codex 所需 tool-call 语义；其 OIDC public client 已允许精确 callback
   `https://agent.byted.bps.dev/auth/llm-gateway/callback`；
9. GHCR 上 service、harness、Hydra 与 Chart 四个 package 均为 public，SG mirror 可由集群节点匿名
   拉取三个 digest-pinned amd64 镜像和环境锁定 Chart；
10. CoreDNS ClusterIP、Gateway Pod label selector、Service CIDR 中 6 个空闲固定 ClusterIP，以及
    S3/平台 OIDC 的实际 IPv4 CIDR/port 已确定；若启用 managed executor，还必须确认 syd2a SOCKS Pod
    selector/TCP 1080、经代理的 JWT/control/data-plane、Policy Webhook URL/PSM 可达性，以及目标路径的
    PMTU/MTU/MSS。
11. `egress-authorizer-sg.byted.bps.dev` 已解析到现有 SG Istio Gateway，wildcard
    `*.byted.bps.dev` 证书覆盖该 hostname，且 Gateway API `BackendTLSPolicy` 能使用 Pulumi 创建的
    `agentserver-egress-backend-ca` ConfigMap 校验 egress-authorizer 后端证书；所有 workspace mode 都使用该入口。
12. 若部署 `policy-bootstrap`，TAE policy 必须仍是 unpublished/unapproved 且 evidence 为空；若部署
    `active`，TAE policy 必须已经发布并完成安全审批，`open.feishu.cn` 精确 whitelist、policy SHA-256 与
    binding SHA-256 必须和 `managedExecutor.tae.policy` 完全一致，并核对 `/v1/policy` Webhook binding。
    CreateSession 不携带这些网络策略字段。

这些非 Secret 参数写入 `production.json`，不是 Pulumi Secret：

| 外部系统 | `production.json` 字段 |
| --- | --- |
| 外部 OIDC | `oauth.externalOidc.issuer`、`clientId`；`redirectUrl` 固定为上述回调地址 |
| S3-compatible | `objectStore.s3Endpoint`、`s3Region`、`s3Bucket`、`prefix`、`s3UsePathStyle` |
| Chart 内置 Hydra | `oauth.hydra` 字段为固定合同，不是用户提供的外部 endpoint |

第三方模型 Gateway **不写入** `production.json` 或 Pulumi config。部署完成后由 workspace owner 通过
前端/API 写入 Gateway URL、OIDC issuer、public client ID、scopes、bearer 类型和默认 model；每个成员
只关联自己的 OIDC grant。

对应 Secret 的精确含义：

- `externalOidcClientSecret` 是与 `oauth.externalOidc.clientId` 同一注册项的 client secret；

`registry-sg.byted.cs.ac.cn/ghcr/agentserver` 的 GHCR mirror 拉取面是公开的。生产配置拒绝
`pullSecret`，生成的 Pod 不含
`imagePullSecrets`，Pulumi 也不接收 `registryDockerConfigJson`。发布镜像或 Chart 所需的写权限只属于
发布工作站，不进入 SG stack、Kubernetes Secret 或运行时 Pod。

不存在 `upstreamCaPem`、`upstreamCredential` 或 llmproxy 静态上游 Secret。Core 用 Pulumi 生成的
`llm-gateway-sealing-keyring.json` 加密每个 `(workspace, gateway, user)` token set；这个 key 只能解密
数据库 grant，本身不能调用模型。

外部系统的 NetworkPolicy 使用 CIDR，不会在 DNS 变化时自动更新。PostgreSQL 不使用外部 CIDR，
固定按 `cnpg.io/cluster=agentserver-postgres` Pod selector 放行 TCP 5432。至少需要：

- core：CNPG PostgreSQL、集群内 Hydra Admin、平台 OIDC、S3，以及动态 workspace Gateway OIDC 的公网 443；
- platform-gateway：Core；
- browser-gateway：Core；
- harness-pool：S3；
- llmproxy：动态 workspace Gateway `/v1/responses` 的公网 443；
- sandbox-gateway：集群内 Core，以及 `ssh-egress` namespace 固定 syd2a Pod 的 TCP 1080；不含外部 CIDR；
- egress-authorizer（仅 `webhook_swap`）：集群内 Core；公网 Webhook 入口只按 Istio Gateway namespace/Pod selector 放行；
- Hydra/AgentServer migration 与 bootstrap：CNPG PostgreSQL；Hydra client setup：集群内 Hydra Admin。

## 4. 通过 GitHub Actions 发布 SG amd64 制品

`main` 上影响 `v2/**` 或发布 workflow 的提交会触发
`.github/workflows/v2-production.yml`。流水线是唯一生产发布入口，它会：

1. 运行 `make -C v2 check`；
2. 下载并校验固定的 stock Codex `0.146.0` 与 bwrap amd64 artifact；
3. 使用 Docker buildx 构建 service/harness 的 `linux/amd64` OCI archive，并调用
   `agentserver-image verify-oci` 校验 manifest、descriptor、diff ID 以及镜像文件合同；
4. 推送 `ghcr.io/agentserver/v2-service`、`ghcr.io/agentserver/v2-harness` 和 managed-sandbox；
5. 将固定的 Hydra 26.2.0 amd64 manifest 镜像到 `ghcr.io/agentserver/hydra`，并要求 digest 精确为
   `sha256:f59c2f7f4969269b154fa34c57bc4b849263ebedbcaf8114aaeb1658a3007b4b`；
6. 仅从 GitHub Environment Secret `V2_SG_PRODUCTION_CONFIG_B64` 解码真实 SG 配置；仓库内的
   `config.example.json` 只用于 schema/renderer 测试。`lock-release` 会校验 TAE/网络 evidence
   引用和 canonical network binding digest，发现 `REPLACE`、`TODO`、`TBD` 或 `EXAMPLE` 等模板值时
   fail closed；Secret 缺失时已发布的镜像保留，但 Chart 不发布；
7. 用刚发布的四个 digest 生成环境锁定 Chart，并发布到
   `ghcr.io/agentserver/agentserver-v2`；
8. 在 workflow summary 和 artifact 中记录四个镜像 digest、Chart version、完整
   `deploymentConfigSHA256`、生产配置和镜像验证报告。

SG 配置始终引用 mirror 路径：

```text
registry-sg.byted.cs.ac.cn/ghcr/agentserver/v2-service@sha256:<ci-amd64-digest>
registry-sg.byted.cs.ac.cn/ghcr/agentserver/v2-harness@sha256:<ci-amd64-digest>
registry-sg.byted.cs.ac.cn/ghcr/agentserver/hydra@sha256:f59c2f7f4969269b154fa34c57bc4b849263ebedbcaf8114aaeb1658a3007b4b
```

首次发布后必须把四个 GHCR package 的 visibility 设为 public，并分别从 GHCR 和 SG mirror 做匿名
manifest 查询。service/harness/Hydra 必须都是 `linux/amd64`，mirror 返回的子 manifest digest必须与
workflow summary 一致。不要把 tag、本地 image ID、arm64 digest 或 multi-arch index digest 写入
production config，也不要再从开发机手工构建上传生产镜像。

## 5. 准备并校验 SG production.json

复制 [`deploy/production/config.example.json`](../deploy/production/config.example.json) 到一个绝对、
不可被 group/other 写入的安全路径。模板中的镜像和网络值只用于本地 renderer/schema 测试；正式发布
必须使用真实 SG evidence-backed 配置，并将其以 base64 写入 GitHub Environment Secret
`V2_SG_PRODUCTION_CONFIG_B64`。模板同时写入六个已 dry-run 确认可分配的 SG ClusterIP、CoreDNS
`192.168.0.10`、固定 bootstrap UUID、runtime manifest 和 `harness-final-exec` 摘要。以下字段已经固定，
不能修改：

- `region=sg`、`namespace=agentserver`、`platform=linux-amd64`；
- `spiffeTrustDomain=agentserver.byted.bps.dev`；
- 五个域名及 Istio Gateway/listener；
- `run-capability-sg-v1`、`run-manifest-sg-v1`；
- 9 个应用 Secret 名称；
- `objectStore.mode=s3-plaintext-v1`；
- 镜像仓库只能是 `registry-sg.byted.cs.ac.cn/ghcr/agentserver/v2-service`、`v2-harness`、
  `v2-managed-sandbox` 和 `hydra`，
  且必须使用 digest。

当前 SG 模板已经锁定 owner `sub=3506220589`、平台 OIDC、S3 endpoint/region/bucket/prefix/path-style、
Chart 内置 Hydra 合同，以及从 SG Pod 解析得到的 TOS 地址 `10.8.103.160/32` 和
`fdbd:dc51:fe:200d::1/128`。平台 OIDC 当前解析为公网地址，由受 SSRF 保留网段约束的
public-HTTPS egress 规则覆盖；TOS 内网地址分别显式写入 core 和 harness-pool 的 TCP 443 白名单。
这里必须保持 `s3Endpoint=https://s3-sg.byted.cs.ac.cn`、`s3Region=sg-devbox-1` 与
`s3UsePathStyle=true`，以 Pulumi 管理的 SeaweedFS bucket-scoped 身份访问 `agentserver` bucket。
DNS 地址变化时必须重新生成 Chart。新代码提交后仍须重新构建、远端核验和
替换 service/harness digest 及对应 runtime artifact；Hydra digest 只在升级 Hydra 版本时变化。资源配额
可按最终容量审计调整。S3 endpoint 是必填项，不能省略后回退到
AWS 默认 endpoint。S3 对象是明文；bucket、credential、备份、retention 和访问审计
必须按明文数据边界管理。S3 不是 PostgreSQL 或完整用户 session 数据库的替代品，只保存
prompt/checkpoint 对象，数据库保存状态与 pointer。

managed executor 的 `active` 配置还必须包含：

- `managedExecutor.tae.networkEvidence.reportSha256`：activation 从 canonical SG 网络测试报告字节自动计算的不可变摘要；
- `managedExecutor.tae.networkEvidence.evidenceRef`：变更单/安全评审/制品报告引用；
- `managedExecutor.tae.networkEvidence.bindingSha256`：由上述报告和当前配置的 syd2a proxy authority/
  Pod selector/port、空的 sandbox/egress-authorizer 外部 CIDR 与额外 Webhook source CIDR、TAE region/PSM
  及 Webhook mode/PSM/URL 共同计算的 canonical digest。

修改任意这些网络事实后必须重新执行 SG 实测、生成报告并更新摘要；不能只手工改 CIDR。`LockRelease`
会再次计算并核对摘要，并拒绝模板 sentinel 或明显的重复字符伪摘要。

第一次发布先从经过普通配置校验的 active 模板生成无权限 bootstrap 文件；输出必须是新路径：

```bash
cd /absolute/agentserver/v2
go run ./cmd/agentserver-deploy prepare-policy-bootstrap \
  --config=/absolute/active-template.json \
  --output=/absolute/policy-bootstrap.json
```

TAE 审批完成后，通过 Pulumi 在**当前已部署且已锁镜像**的 bootstrap release 中启用一次性探针；不能拿
active 模板、另一版配置或手工 manifest 代替。Chart 的默认值为 disabled，只有 bootstrap Chart 的
closed values schema 接受实际 revision。Pulumi 会在同一次更新中先写入 AK/SK，再创建 immutable
ConfigMap、专用 ServiceAccount、仅放行 DNS+syd2a 的 NetworkPolicy 和 `backoffLimit=0` 的 Job：

```bash
cd /absolute/k8s-byted
pulumi config set --stack sg agentserver:taeNetworkProbePolicyRevision '<TAE实际发布revision>'
pulumi up --stack sg

probe_job=$(kubectl --context '<sg-context>' --namespace agentserver get jobs \
  --selector='app.kubernetes.io/name=tae-network-probe' \
  --output=jsonpath='{.items[0].metadata.name}')
test -n "${probe_job}"
kubectl --context '<sg-context>' --namespace agentserver logs "job/${probe_job}" \
  | sed -n '/^{"schemaVersion":1,"kind":"agentserver.tae.sg-network-report"/p' \
  > /absolute/sg-tae-network-report.json
test "$(wc -l < /absolute/sg-tae-network-report.json | tr -d ' ')" = 1
chmod 0600 /absolute/sg-tae-network-report.json
```

探针默认执行 20 次 bypass-cache JWT 强制刷新、20 次不存在 metadata 的 exact Search，以及一次
Create→唯一 Search→Ready→TTL→`lark-cli --version`→Stat→完整读取并校验 42MB CLI→读取并校验
Skill→Delete→Get/cleanup 确认。任一步失败都会输出 `passed=false` 并以非零退出；报告只含固定配置、
耗时和稳定错误类别，不含 AK/SK/JWT、响应 body 或 Session ID。先保存失败报告和 Pod event 排障，不能
把它送入 activation。报告本身必须先上传到不可变 evidence store。保存报告和所需诊断信息后，由
Pulumi 关闭探针并删除这些临时资源；不要直接 `kubectl delete`：

```bash
cd /absolute/k8s-byted
pulumi config rm --stack sg agentserver:taeNetworkProbePolicyRevision
pulumi up --stack sg
```

取得不可变报告引用后，从同一 bootstrap 配置执行唯一 activation 边。命令会读取 canonical 报告并自行
计算 `reportSha256`，设置实际 revision/审批引用，再同时重算 policy binding、network binding、
`runtimeProfileSha256` 和 `packSetSha256`；不再接受人工填写的 report SHA：

```bash
go run ./cmd/agentserver-deploy activate-managed-executor \
  --config=/absolute/policy-bootstrap.json \
  --output=/absolute/active-production.json \
  --network-report=/absolute/sg-tae-network-report.json \
  --policy-revision=<TAE实际发布revision> \
  --policy-evidence-ref=<安全工单或审批的不可变引用> \
  --network-evidence-ref=<不可变报告引用>
```

后续 active 网络事实、镜像或 policy revision 发生变化时，不允许直接替换摘要或重绑旧报告。必须先从
当前有效 active 配置生成新的 deny-only bootstrap，部署并重新执行上述真实 SG probe，再通过同一个
`activate-managed-executor` 晋级。这样每次 active 配置都只能来自一份内容经过完整核对的 canonical
报告，不存在只相信人工填写 SHA-256 的旁路。

校验：

```bash
chmod 0600 /absolute/production.json

cd /absolute/agentserver/v2
go run ./cmd/agentserver-deploy validate \
  --config=/absolute/production.json

# 供 GitHub Environment Secret 使用；不要把解码内容写入仓库或日志。
base64 < /absolute/production.json | tr -d '\\n'
```

## 6. 核验并选择 CI 发布的 OCI Chart

Chart version 固定为 `0.1.0-config.d<config-sha256-first-12>`，由第 4 节的同一流水线生成和发布，不能
拿其他提交的镜像或本地 Chart 混用。完整摘要记录在 Chart annotation、`values.yaml`、
`files/checksums.json` 与 workflow summary 中。

Pulumi 通过 SG mirror 使用：

```text
oci://registry-sg.byted.cs.ac.cn/ghcr/agentserver/agentserver-v2:<chart-version>
```

部署前从 workflow artifact 取得完整 `deploymentConfigSHA256`，并匿名执行 `helm show chart` 验证 mirror
可拉取对应 version。若 mirror 尚未同步、package 不是 public、Chart 摘要与 workflow 不同，停止部署，
不要临时改回手工仓库或本地 Chart。

## 7. 用 Pulumi 部署

`../k8s-byted/index.ts` 只在 `pulumi.getStack() === "sg"` 时调用 AgentServer，并等待 Istio
Gateway 创建完成。Chart version 从完整配置摘要自动推导，不再单独配置 Namespace、release、
Chart version、Secret 名称或 Secret 内容文件。CN/SG 集群的所有实际变更都必须经过这套 Pulumi
资源；不要用 `helm install/upgrade` 或 `kubectl apply/create/patch` 绕过 Pulumi state。

在外部资源 Output 尚未直接接线时，可把它们写入 Pulumi 的加密 config；这些值会进入加密
Pulumi state 和 Kubernetes Secret，不会出现在 Chart：

```bash
cd /absolute/k8s-byted

pulumi config set --stack sg agentserver:deploymentConfigSHA256 '<64-lowercase-hex>'
pulumi config set --stack sg --secret agentserver:externalOidcClientSecret '<oidc-secret>'
pulumi config set --stack sg --secret agentserver:s3AccessKeyId '<s3-access-key-id>'
pulumi config set --stack sg --secret agentserver:s3SecretAccessKey '<s3-secret-access-key>'
```

初次部署 `policy-bootstrap` 时不要设置 TAE AK/SK；它们缺失不会阻塞 `pulumi up`，也不会被填入空值。
安全审批完成、准备运行一次性网络探针时，在操作者终端交互输入这对基础设施凭据。不要把值放进命令
历史、文档、聊天或 `production.json`：

```bash
pulumi config set --stack sg --secret agentserver:byteCloudAccessKeyId
pulumi config set --stack sg --secret agentserver:byteCloudSecretAccessKey
pulumi up --stack sg
```

模块要求两个配置同时存在，把它们放进同一个 SecretPatch 更新，并且只写入
`agentserver-sandbox-secrets/bytecloud-access-key-id` 与 `bytecloud-secret-access-key`。探针和 active
`sandbox-gateway` 只读文件挂载；Core、executor、harness、可选 egress-authorizer、TAE Session env/metadata
均拿不到这对凭据。Pulumi backend 必须已经配置加密 secrets provider 和受控 state ACL；未确认这一
持久化边界前不得设置这两个值。

发布 Chart 的操作者可能仍需写权限才能执行 `helm push`；该凭据只保留在发布工作站。部署阶段由
Pulumi 匿名拉取 OCI Chart，kubelet 匿名拉取镜像，不依赖交互式 `helm registry login`。

不要设置 `agentserver:databaseUrl` 或 Hydra DSN。两个 CNPG owner 密码和 DSN 在 `pulumi up` 时自动
生成；模块会等待 CNPG `Cluster` 的 `Ready` condition 和 Hydra `Database.status.applied=true`，再启动
Helm migration/client-setup/bootstrap hooks。

先 preview，确认只操作 SG：

```bash
pulumi preview --stack sg
```

确认 OCI Chart、CNPG Cluster/owner Secret、应用 Secret、Namespace、HTTPRoute 和固定 ClusterIP
都正确后，最后启用并部署：

```bash
pulumi config set --stack sg agentserver:enabled true
pulumi up --stack sg
```

不要修改 `Pulumi.cn.yaml`，也不要在 `cn` stack 设置 AgentServer。模块会主动拒绝非 `sg` stack。

## 8. 部署后验证

```bash
kubectl --context '<sg-context>' --namespace agentserver get \
  clusters.postgresql.cnpg.io,databases.postgresql.cnpg.io,deployments,pods,services,httproutes,networkpolicies

kubectl --context '<sg-context>' --namespace agentserver \
  wait --for=condition=Ready cluster/agentserver-postgres --timeout=15m
kubectl --context '<sg-context>' --namespace agentserver \
  rollout status deployment/agentserver-core --timeout=10m
kubectl --context '<sg-context>' --namespace agentserver \
  rollout status deployment/platform-gateway --timeout=10m
kubectl --context '<sg-context>' --namespace agentserver \
  rollout status deployment/browser-gateway --timeout=10m
kubectl --context '<sg-context>' --namespace agentserver \
  rollout status deployment/executor-gateway --timeout=10m
kubectl --context '<sg-context>' --namespace agentserver \
  rollout status deployment/harness-pool --timeout=10m
kubectl --context '<sg-context>' --namespace agentserver \
  rollout status deployment/llmproxy --timeout=10m
kubectl --context '<sg-context>' --namespace agentserver \
  rollout status deployment/hydra --timeout=10m

# managedExecutor.stage=active 时核对 provider boundary
kubectl --context '<sg-context>' --namespace agentserver \
  rollout status deployment/sandbox-gateway --timeout=10m

# 所有 workspace credential mode 都必须具备以下 Webhook 资源
kubectl --context '<sg-context>' --namespace agentserver \
  rollout status deployment/egress-authorizer --timeout=10m
kubectl --context '<sg-context>' --namespace agentserver \
  get service/sandbox-gateway service/egress-authorizer \
  networkpolicy/sandbox-gateway networkpolicy/egress-authorizer
```

公网入口：

```bash
curl --fail --show-error https://agent.byted.bps.dev/readyz
curl --fail --show-error https://agent.byted.bps.dev/
curl --fail --show-error https://browser.byted.bps.dev/readyz
curl --fail --show-error https://browser.byted.bps.dev/
curl --fail --show-error https://auth-sg.byted.bps.dev/.well-known/openid-configuration

# /mcp 不得从公网 executor 域名暴露，期望 404。
curl --output /dev/null --write-out '%{http_code}\n' \
  https://executor-gateway.byted.bps.dev/mcp
```

还需先在 `agent.byted.bps.dev` 用 `agentserver-platform` 完成 OIDC Code + PKCE 登录，并在 Platform
应用中创建 Gateway；随后 owner/developer 分别完成第三方 Gateway OIDC + PKCE。个人
`grantStatus=active` 后，从 Platform 进入 `browser.byted.bps.dev`，Browser 再为选中的 workspace 使用
`agentserver-browser` 独立授权，最后经 `browser-gateway.byted.bps.dev/v2` 创建 run。Core 在 run
创建时冻结 Gateway/config version/user/model；llmproxy 每次请求向 Core live-authorize，不缓存 bearer。

完整 shell/read_file E2E 要求用户机器安装与本
Chart 相同 runtime manifest 的 agentx，完成 enrollment，并连接
`wss://executor-gateway.byted.bps.dev/internal/v2/agentx/connect`。服务端不会在集群内伪造 executor。

`policy-bootstrap` 部署后先验证公网 URL 返回 `200`、`version=v1`、`result=deny` 且
`application_info=policy_bootstrap_inactive`；此时不应存在 sandbox-gateway Deployment。

切换到 `active` 后，managed executor 的 golden path 还必须单独验证：

1. `sandbox-gateway` 已用 `i18n-tt` 应用 AK/SK 成功换取 JWT并完成 TAE readiness；AK/SK/JWT 不出现在
   execution gateway、harness、sandbox/TAE metadata、env、proc、文件、stdout/stderr、checkpoint 或日志；
2. Platform 为测试 workspace 创建 active `kind=lark` binding，Core workspace credential service 能按
   operation 解析并 materialize；当前 static bearer 的轮换由 Platform 发起，不存在后台 OAuth refresh。无需
   Core bootstrap Lark grant、Pulumi expiry 或 Helm release。`webhook_swap` 下 sandbox/TAE metadata、env、
   proc、文件、stdout/stderr、checkpoint 和日志都不能出现真实 token；`process_env` 下只允许目标
   `lark-cli`/子进程 env 与内存出现，其他进程、metadata、文件、result、checkpoint 和日志仍必须为零；
3. `harness-worker` 依据既有 Lark skill 通过 MCP `shell` 调用 pinned `lark-cli`，不新增模型可见工具；
4. TAE Agent Gateway 能从 SG 实际调用 `egress-authorizer` 的 PSM/URL Webhook，Webhook 在 500ms 内只返回
   allow/deny；`webhook_swap` 完成 placeholder → real Authorization，`process_env` 完成 proof 验签、Core
   live recheck、bearer 常量时间比较和 `X-Agent-Trace=agentserver-managed` 清洗。非 `lark-cli` operation
   不触发 direct resolve，缺失/篡改/跨 workspace proof 与 mode/version 变化全部拒绝；
5. 真实 SG 环境分别测 ByteCloud JWT endpoint、TAE control/data-plane 和 Lark 路径的 IPv4、IPv6、DNS、
   redirect、CONNECT、IP literal bypass、PMTU/MTU/MSS 和错误率，
   超时/非 200/策略漂移全部 fail closed。

上述证据尚未由本地 `go test` 或 Helm template 产生；发布前应将抓包、延迟、错误率和 zero-secret 扫描
报告作为 release artifact 保存。

## 9. 升级、轮换与卸载

升级顺序固定为：新代码提交 → 构建/推送新 amd64 digest → 更新 `production.json` → 生成并
发布新 Chart → 更新 `deploymentConfigSHA256` → `pulumi preview` → `pulumi up`。

数据库 migration 只向前。Helm rollback 不回滚数据库，只有确认旧应用兼容当前 schema 时才
执行。Pulumi 生成的 TLS/随机资源会稳定保存在 SG stack state 中，不会每次 `pulumi up` 重建；
轮换资源会更新 Secret，并由 Reloader 触发工作负载重启。workspace Gateway sealing key 不能直接替换：
先发布 active 新 key + 旧 key 的 overlap keyring，完成数据库 grant 重封装后才能删除旧 key。签名密钥轮换仍要先发布包含新旧公钥
的 overlap keyring，再切 signer，最后删除旧公钥，不能一次替换 signer 和 verifier。

ByteCloud AK/SK 必须作为一对原子更新 `agentserver-sandbox-secrets`，随后等待所有 `sandbox-gateway`
新 Pod readiness 通过；旧 Pod 退出后再撤销旧 key。不要在一个运行中 Pod 内分别替换两个 subPath 文件，
也不要把 JWT 当作需要持久化轮换的 Secret。

CNPG Cluster、PVC 和 S3 bucket 都是数据资源，不能把 `pulumi destroy` 或 Namespace 删除当作普通
应用回滚。删除 Cluster/Namespace、Pulumi random state、外部 credential 或 bucket 前，必须先验证
数据库备份、PVC retention 和恢复演练；Namespace 删除可能导致数据库数据不可恢复。正式退役必须
先在一次受审计的 Pulumi 变更中显式移除对应 `protect`，完成 preview/up 后再发起第二次删除变更，
不能用 `kubectl` 强删绕过保护。

## 10. 常见故障

- HTTPRoute `Accepted=False`：检查 listener `allowedRoutes`、五个 hostname 和 `sectionName`；
- 公网 503：检查 Istio Gateway Pod selector、NetworkPolicy 和后端 HTTP `8080`；
- executor 公网 `/mcp` 非 404：立即停止发布，公网路由面发生越界；
- CNPG Cluster 不 Ready：检查 operator、Longhorn PVC、节点容量以及 CNPG Pod/事件；
- Hydra Database 未 applied：检查 CNPG `Database` CR、`hydra` role reconciliation 和 owner Secret；
- migration 失败：检查 `agentserver-postgres-rw`、对应 owner Secret、TLS 和 CNPG Pod selector egress；
- Hydra client setup 失败：检查 Hydra readiness、内部 CA/SAN、Admin `4445` NetworkPolicy 和 client profile；
- Hydra 登录在 continuation 阶段报 CSRF：确认浏览器始终直接访问 `auth-sg.byted.bps.dev`，Platform
  HTTPRoute 没有 `/oauth2`，Hydra public route 没有被额外反向代理改写或丢弃 `Set-Cookie`；
- S3 写失败：重点检查 `If-None-Match: *`、`412 PreconditionFailed` 语义、endpoint/path-style/region；
- harness-pool 卡在 init：检查目录权限、network guard配置和节点nftables能力；普通服务不应再有Secret materialize init；
- Helm/Pulumi更新失败：失败Pod会因non-atomic release保留，先读取对应container日志和Events，再通过下一次`pulumi up`修复；
- ImagePullBackOff：检查公开仓库匿名拉取策略、网络连通性和远端 amd64 digest；
- 外部请求 timeout：普通外部系统更新正确 egress CIDR/port 后重新生成 Chart；TAE 路径检查 syd2a Pod、
  Service、NetworkPolicy、SOCKS remote DNS 和 SSH 链路，不能临时恢复 origin 直连或在线 patch；
- Helm guard 失败：Chart 与 `deploymentConfigSHA256` 不匹配，必须选择同一份生成制品。
- managed sandbox ensure 卡住：先检查 sandbox-gateway readiness、Core mTLS、syd2a SOCKS、ByteCloud 应用
  JWT exchange、`i18n-tt` site、TAE PSM/ACL、session metadata
  digest 和 TTL；不要手工在 CreateSession 请求里添加未支持的 network-policy 字段；
- TAE Policy Webhook 无回调或 403：确认 TAE policy 已发布/审批、`open.feishu.cn` 精确 whitelist、
  `/v1/policy`、PSM/URL authority 和两端 binding SHA-256 一致，并检查 SG IPv4/IPv6/PMTU；
- managed Lark 请求被拒绝：查看 egress deny reason class、grant version/fence 和 placeholder JTI，
  不要把真实 access/refresh token 写入 sandbox env 或临时放宽 NetworkPolicy。
