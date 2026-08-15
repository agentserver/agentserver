# Agentserver v2 SG 生产部署

本文只描述 SG Kubernetes 生产部署。平台基础设施仍是单集群、closed-world、`linux/amd64`；TAE managed
sandbox 则在同一部署中支持显式安装 `cn`、`boe`、`i18n-bd`、`i18n-tt` 四个地域 profile：

- Pulumi stack 必须为 `sg`；
- Kubernetes 节点必须为 `linux/amd64`；
- Namespace 固定为 `agentserver`，Helm release 固定为 `agentserver-v2`；
- 发布源固定为 `ghcr.io/agentserver/*`，SG 集群经 registry mirror
  `registry-sg.byted.cs.ac.cn/ghcr/agentserver/*` 匿名拉取；
- 公网 TLS 由 `istio-ingress/istio-gateway` 的 `https-byted-bps` listener 终止；
- 应用的公网后端使用集群内明文 HTTP，组件之间的内部控制面继续使用 TLS/mTLS；
- prompt/checkpoint 以明文对象写入 S3-compatible bucket，应用仍逐次校验 pointer、size 和 SHA-256。

SG 是平台工作负载所在地域，不是 TAE provider region 的隐式默认值。所有 TAE authority、代理、Gateway、
environment 和 evidence 都由 v6 `sandboxProfiles` catalog 指定，运行时禁止根据 SG 猜测或跨地域 fallback。

生产 Chart 不是通用 `values.yaml`。`agentserver-deploy chart` 会把一份通过 closed-world
校验的 `production.json` 生成为环境锁定 Chart。镜像、网络、平台登录 OIDC 或对象存储配置发生
变化时，必须重新生成并发布 Chart，不能在 Helm/Pulumi 安装时临时覆盖。

这里的 closed-world 只约束平台基础设施。模型路由不再锁进 Chart：workspace owner 在部署后关联
Responses-compatible 的第三方 LLM Gateway，每个成员再用 OIDC Authorization Code + PKCE 授权自己的
grant。平台没有统一的模型 API key。

`managedExecutor` 有三个显式且不兼容回退的 stage：`disabled`、`policy-bootstrap`、`active`。
`sandboxRegions` 列出本次 release 实际安装的地域；`defaultRegion` 固定为 `i18n-tt`，并且 catalog 必须
安装该地域，以匹配数据库迁移和新 workspace 的初始 setting。workspace owner 可在 Platform/API 中选择
其他已安装地域。修改只影响新 Run：Core 在 CreateRun 事务中冻结
`settingVersion + region + profileId + bindingSha256 + environmentId`，后续 Harness/Executor 必须逐字段匹配。
普通问候或纯模型回合不会创建 TAE 资源；只有首次 Executor tool call 才 lazy acquire 对应 profile 的
sandbox，并在同一 session/environment generation 上复用。

Lark credential delivery mode 不属于 release 或 Chart；它是每个 workspace 自己的配置。当前 direct
Sandbox profiles 只支持 `process_env`：`policy-bootstrap` 不部署 provider authority，`active` 才部署
`harness-pool → executor-gateway → region-specific sandbox-gateway → TAE`。executor-gateway 在精确进程
启动前向 Core 解析真实 Lark token，只注入目标 `lark-cli`；direct profile 使用系统预置
`*.feishu.cn` policy，不配置 webhook，也不经过 egress-authorizer。若 workspace 选择 `webhook_swap`，
direct profile 必须 fail closed；未来需使用独立 webhook-enabled profile。
sandbox 内运行 digest-pinned Linux/amd64 `lark-cli` 和只读 skill；模型仍
只看到既有 `list_environments`、`shell`、`read_file` MCP catalog。TAE policy 不通过 CreateSession 参数
伪造，而由 PSM/system policy readback 与网络策略 binding digest 校验。
代码和 provider-linked binary 已接入本地生产渲染，但每个已安装地域的真实 ByteCloud JWT/TAE/Lark、
IPv4/IPv6、代理或直连路径与 zero-secret 门禁仍须在目标集群执行；本文不把本地测试当作上线证据。

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
- `policy-bootstrap` 不增加常驻 provider Deployment；它没有可供 Run 使用的 sandbox authority；
- `active` 为每个已安装地域增加一套独立的 sandbox-gateway Deployment、Service、PDB 和 NetworkPolicy；
- executor-gateway 固定单副本、`Recreate`、无 HPA/PDB；
- managed executor 关闭和 bootstrap 都有 6 个固定 ClusterIP Service、6 条 HTTPRoute；active 额外增加
  `len(sandboxProfiles)` 个 region-specific sandbox-gateway Service；
- direct profile 不渲染 egress-authorizer Deployment/Service/Route/BackendTLSPolicy/NetworkPolicy；
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
- base profile 使用固定平台 Secret；active 为每个已安装地域使用独立 sandbox-gateway TLS/ByteCloud Secret，
  Core 另持有 workspace credential keyring。webhook profile 才额外需要 egress-authorizer 与 placeholder/proof
  signer Secret；这些只描述组件身份、keyring 和基础设施，不承载 workspace Lark/ByteCloud/GitHub credential；
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
| Lark device-flow app ID / app secret | 平台用于发起 workspace 用户授权的 Lark OAuth 应用身份；不是 workspace token。SG 可从 Connect 已注册的 Lark 应用接入，但该应用必须开通 managed Lark 所需 scopes |

如果这些外部系统由 Pulumi provider 管理，应直接把对应资源 Output 传给 AgentServer 模块；当前
模块也保留加密 Pulumi config 接口作为接入点。不要生成一个外部系统从未注册的随机密码并把它
当成可用 credential。模块会拒绝空值、首尾空白、控制字符和超出协议上限的 credential。

active SG Core 必须配置成对出现的 Lark device-flow app ID/app secret；缺失时生产启动 fail closed，
不能静默把 provider catalog 降级为 `manual`。用户通过 Platform 完成 device flow 后得到的 access/refresh
token 仍只按 workspace binding 加密存入 Core，不写入 Pulumi、Kubernetes Secret 或 deployment document。
ByteCloud workspace device flow 同样不需要 Pulumi 保存用户 token；Core 在 `site=i18n-tt` 的生产网络中固定
请求 `https://paas-gw-i18n.byted.org`。`https://cloud.tiktok-row.net` 是办公网入口，不得作为集群内默认值。

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

`disabled` 和初始 `policy-bootstrap` 不要求 ByteCloud AK/SK，缺失时不能阻塞普通部署。安全审批完成后，
运行一次性网络探针之前必须把基础设施 AK/SK 作为一对 Secret 原子接入；只配置其中一个会 fail closed。
每个 profile 的 `gateway.secret` 必须精确包含 `ca.crt`、`tls.crt`、`tls.key`、
`sandbox-capability-keyring.json`、`bytecloud-access-key-id` 和 `bytecloud-secret-access-key`。最后两个 key
只挂载到该地域的 probe/sandbox-gateway，通过只读文件路径传入；Core、Executor、Harness 和 TAE Session
均不可见。它们只用于 TAE 基础设施调用，不能与 workspace credential binding 复用。

provider 用官方 ByteCloud Auth SDK 换取短期 JWT，并以 `X-Jwt-Token` 同时访问该 profile 固定的 TAE
control-plane 与 sandboxd data-plane。所有 profile 的 PSM 固定为 `bytedance.sandbox.agentserver`；direct
policy 固定 `publicHost=*.feishu.cn`、`publicAccess=system_default`、`publicWebhookRequired=false`，所有
webhook 字段为空。地域与路由映射为：

| TAE region | Required route |
| --- | --- |
| `cn` | `merlin-hl-1` |
| `boe` | direct；`sandboxExternalEgress` 必须显式提供 IPv4 与 IPv6 CIDR |
| `i18n-bd` | `merlin-useast14a-1` |
| `i18n-tt` | `merlin-maliva-1` |

Merlin 名称不携带网络地址。完整 credential-free `socks5h://` URL、namespace、exact Pod selector 和 port
全部由 `proxyProfiles` 配置，URL port 必须与显式 port 相等；未使用或重复 proxy 会被拒绝。每个
`sandboxProfiles[].tae` 还必须显式给出并匹配官方 SDK 的 control-plane URL、data-plane suffix、
ByteCloud site 和 JWT endpoint。BOE 的基础设施 AK/SK 使用官方 SDK 的 `site=cn` 别名；JWT endpoint 仍须
由运营方提供并通过该地域探针确认。
标准 `HTTP_PROXY` / `HTTPS_PROXY` / `ALL_PROXY` 继续被拒绝。

proxy profile 的 NetworkPolicy 只允许 Core、DNS 与该 profile 指定的 namespace/Pod/port；BOE direct
profile 只允许 Core、DNS 与配置中的双栈 CIDR。任何 region/authority/proxy/CIDR 变化都会改变 profile
binding 和 network evidence binding，必须重新探针与激活，不能在线 patch 或 fallback。
启动时会强制刷新一次作为 readiness，之后由 SDK 缓存并自动刷新；TAE 明确返回 401 时只刷新下一次请求
使用的身份，不重放已经写出的 create/process 操作。

### 2.1 Workspace managed sandbox region

唯一事实源是 Core 数据库的 workspace managed sandbox setting。Platform 使用
`GET/PATCH /v2/workspaces/{workspaceId}/managed-sandbox-settings`：

- GET 对 workspace member 可见，并返回当前 setting 与 deployment 实际安装的 `availableRegions`；
- PATCH 仅 owner 可用，必须携带 `expectedVersion` 做 CAS；并发更新返回 409，前端会重新读取权威值；
- region 只能从 deployment catalog 选择；未知、未安装或大小写不规范的值都拒绝；
- 更新写审计事件并推进 setting version，不修改已有 Run。

CreateRun 在同一授权事务内读取 setting 并解析 deployment catalog，把 region/profile/binding/environment
写入 immutable run launch state。Run manifest、Core catalog、Harness launch profile 和 Executor Gateway
profile 必须一致；任一缺失或 digest 漂移都在创建 sandbox 前 fail closed。Trajectory 的 Run 详情展示
`managedSandboxRegion`、`managedSandboxProfile`、`managedSandboxEnvironment`、
`managedSandboxBinding` 和 `managedSandboxSettingVersion`，便于确认新旧 Run 的实际路由。

### 2.2 Workspace Managed Lark credential mode

mode 的唯一事实源是 Core 数据库的 `workspaces.managed_lark_credential_mode`。创建 workspace 时必须显式提交；
只有 workspace owner 可以用当前 workspace version 做 CAS 更新。每次 mode 变化都会推进 workspace version 并写
`workspace_managed_credential_mode_events` 审计。生产配置、Helm values/schema、Pulumi 和 workload environment
均不存在 mode 字段或默认/fallback。

| Workspace mode | 进程启动 | Sandbox profile | 安全边界 |
| --- | --- | --- | --- |
| `webhook_swap` | 只向精确 `lark-cli` 注入短期签名 placeholder | 未来独立 webhook-enabled profile；当前 direct profile 明确拒绝 | sandbox 中没有真实 token；逐请求策略属于未来 profile |
| `process_env` | Core 复核 workspace mode 与 exact live `shell + lark-cli + TAE process_start` 后返回 access token | 当前 direct profile：TAE 系统 `*.feishu.cn` 白名单，无 webhook | 真实 token 只进入目标 `lark-cli` 及子进程；每次启动重新 live-authorize |

两种 mode 不复用 Sandbox profile，没有兼容、自动探测或跨 mode fallback。executor-gateway 在每次进程启动
时重新查询 Core；当前 direct profile 遇到 `webhook_swap` 会明确拒绝。未来切换 profile 必须发布对应的
production document 和 TAE evidence，不能在运行时给 direct Sandbox 加 webhook。

`process_env` 的 direct resolve 接口只允许 executor-gateway mTLS identity，响应强制
`Cache-Control: no-store`，只返回 access token，不返回 refresh token。审计失败、Core 不可达或
mode/binding/version 不匹配均 fail closed。direct profile 不签发 operation proof，也不存在逐请求 Webhook
live revoke；已启动短进程中的 token 依赖其自身过期时间，后续新进程启动会重新检查 live authority。

### 2.3 TAE managed sandbox catalog 两阶段引导

正式 managed execution 必须在每个已安装地域的 TAE system policy readback 和网络验证完成后才能启动。
发布流程使用同一个 Helm release 的两个严格阶段：

1. 从 active v6 模板生成 `policy-bootstrap`。转换会遍历全部 `sandboxProfiles`，清空每个 profile 的
   `published/approved/evidenceRef`、network evidence、runtime profile 和 pack lock；
2. 发布 bootstrap Chart。此时没有可供 Run 使用的 sandbox-gateway authority；
3. 对每个地域从 TAE 控制面只读回查该 Sandbox/PSM 的 `*.feishu.cn` system policy；
4. 原子写入各 profile Secret 的 TAE 基础设施 AK/SK，并在 Helm values 的
   `taeNetworkProbe.policyRevisions.<region>` 中提交每个地域的实际 revision。Chart 为每个已安装地域生成
   独立 ConfigMap、Job 和 NetworkPolicy：CN/i18n-bd/i18n-tt 只走各自 Merlin，BOE 只走双栈 direct CIDR；
5. 分别保存 canonical JSON report 并上传不可变 evidence store。每份报告都绑定 region、profile authority、
   bootstrap config SHA、policy revision、20 次 JWT/control 尝试、完整 lifecycle、pinned artifact 摘要与清理；
6. 用一份 manifest 执行 `activate-managed-sandbox-profiles`。命令要求恰好覆盖全部已安装地域；任一报告
   缺失、重复、串线或不匹配都会使整个 activation 失败。成功时原子重算每个 profile 的 policy、network、
   runtime、pack 和 profile binding；
7. 发布 active Chart。只有这一步才启动所有 region-specific sandbox-gateway，并向 Core/Harness/Executor
   注入一致的 profile catalog。

bootstrap 不提供 managed execution，也不读取 placeholder keyring。不得用伪造 `published=true` 或模板
evidence 绕过这个阶段。

当前四地域 catalog 都使用 direct system policy，因此 bootstrap/active 都不部署 egress-authorizer；未来
webhook profile 必须独立生成并验证 host/Webhook/no-bypass 证据，而且所有已安装 profile 必须使用同一套
deployment-wide webhook topology。

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
10. CoreDNS ClusterIP、Gateway Pod label selector、基础 Service 及每个 sandbox-gateway 的空闲固定
    ClusterIP，以及 S3/平台 OIDC 的实际 IPv4 CIDR/port 已确定；
11. 当前 direct profile 不要求或创建 `egress-authorizer-sg.byted.bps.dev` Route；该入口仅属于未来
    webhook-enabled profile，并需单独验证 DNS、证书、Gateway attach 与 backend TLS。
12. `sandboxRegions.regions` 与 `sandboxProfiles` 必须一一对应并按 `cn, boe, i18n-bd, i18n-tt` 的稳定子序
    排列；`defaultRegion` 必须是 `i18n-tt`，该 profile 始终安装。每个 profile 使用唯一 profile ID、environment ID、Gateway ClusterIP、
    Secret 和 TLS server name；
13. 三个 Merlin profile 的完整 SOCKS5H URL、namespace、Pod selector 和 port 已从实际资源确认；BOE 的
    权威 ByteCloud JWT endpoint 及 control/data origin 的 IPv4/IPv6 CIDR 已确认；
14. 四个地域各自的 TAE Sandbox ID/revision、官方 control/data authority、ByteCloud JWT endpoint、Gateway
    certificate SAN 与基础设施 Secret ownership 已确认。不能把 i18n-tt 值复制到其他地域；
15. 若部署 `policy-bootstrap`，所有 profile 的 policy 必须 unpublished/unapproved 且 evidence/locks 为空；
    若部署 `active`，每个 profile 的 system policy readback 必须包含 `*.feishu.cn`，policy/network/profile
    binding 必须完全一致，且全部 webhook 字段为空。CreateSession 不携带这些网络策略字段。

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
- 每个 proxy sandbox-gateway：集群内 Core、DNS，以及它自己的 Merlin namespace/Pod/port；不含 TAE origin
  direct CIDR；
- BOE sandbox-gateway：集群内 Core、DNS，以及配置中经过验证的 IPv4/IPv6 direct CIDR；不允许 proxy；
- egress-authorizer（仅 `webhook_swap`）：集群内 Core；公网 Webhook 入口只按 Istio Gateway namespace/Pod selector 放行；
- Hydra/AgentServer migration 与 bootstrap：CNPG PostgreSQL；Hydra client setup：集群内 Hydra Admin。

## 4. 通过 GitHub Actions 发布 SG amd64 制品

`main` 上影响 `v2/**` 或发布 workflow 的提交会触发
`.github/workflows/v2-production.yml`。流水线是唯一生产发布入口，它会：

1. 运行 `make -C v2 check`；
2. 下载并校验固定的 stock Codex `0.146.0` 与 bwrap amd64 artifact；
3. 使用 Docker buildx 构建 service/harness/managed-sandbox 的 `linux/amd64` OCI archive，并调用
   `agentserver-image verify-oci` 校验 manifest、descriptor、diff ID 以及镜像文件合同。managed-sandbox
   不继承官方 `terminal_faas`，而以 digest-pinned 的 agentserver 自有单层 Debian 镜像为第一层；校验器
   精确锁定其 compressed digest、size、diff ID 和 debuerreotype history，后续自有 FaaS keeper、
   managed runtime、CA 与 canonical WORKDIR 空层继续 closed-world 校验。TAE Terminal Sandbox revision
   在管理面固定镜像和 `run_cmd=/usr/local/bin/agentserver-tae-runtime`；Session create 只发送固定
   `revision_id`，不发送 `image`/`command`，也不依赖官方 `/opt/tiger/run.sh`。官方启动链和自有镜像边界见
   [TAE_SANDBOX_RUNTIME.md](TAE_SANDBOX_RUNTIME.md)；
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

开发阶段的 service-only 变更使用同一个 workflow 的 `service_only` 通道（`main` 上的 v2 push 默认走
该通道）。它只运行改动包测试并重建 service 镜像，复用当前 active 配置中已经发布和验证过的
harness、managed-sandbox、Hydra、runtime/pack lock 与 TAE evidence；不会安装 pnpm、下载
Codex/bwrap/Lark 或重建未变化镜像。service 构建使用 GHA BuildKit cache。该通道只允许修改
`images.service`，并在发布 Chart 前对删除该字段后的 JSON 做逐字节比较。最终生产晋级仍需通过
`workflow_dispatch(release_mode=full)` 执行上述完整 closed-world 门禁。

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
不可被 group/other 写入的安全路径。示例是显式 v6、只安装 i18n-tt 的 renderer/schema fixture；它保留
已知 evidence digest，不是四地域生产值清单。正式发布必须填入真实 evidence-backed profile catalog，并
将最终配置以 base64 写入 GitHub Environment Secret `V2_SG_PRODUCTION_CONFIG_B64`。以下平台字段固定：

- `region=sg`、`namespace=agentserver`、`platform=linux-amd64`；
- `spiffeTrustDomain=agentserver.byted.bps.dev`；
- 五个域名及 Istio Gateway/listener；
- `run-capability-sg-v1`、`run-manifest-sg-v1`；
- 基础平台应用 Secret 名称；每个额外 sandbox profile 的 Secret 名称必须与对应 Pulumi PKI/Secret 资源一致；
- `objectStore.mode=s3-plaintext-v1`；
- 镜像仓库只能是 `registry-sg.byted.cs.ac.cn/ghcr/agentserver/v2-service`、`v2-harness`、
  `v2-managed-sandbox` 和 `hydra`，
  且必须使用 digest。

当前 SG 模板已经锁定 owner `sub=3506220589`、平台 OIDC、S3 endpoint/region/bucket/prefix/path-style
以及 Chart 内置 Hydra 合同。平台 OIDC `connect.byted.bps.dev` 在 SG 内同时解析到
DevBox-SG4 的三个轮转地址 `10.251.224.152`、`10.251.239.167`、`10.251.244.51` 和公网 IPv6；
public-HTTPS 规则覆盖公网地址，Core 另以三个 `/32:443` 精确放行私网地址，不允许扩大为
`10.0.0.0/8`。Harness 访问 `s3-sg.byted.cs.ac.cn` 时按 `istio-ingress` Namespace 与
Gateway Pod selector 放行 TCP 443；不把域名的轮转 LoadBalancer/节点地址写入 CIDR 白名单。
这里必须保持 `s3Endpoint=https://s3-sg.byted.cs.ac.cn`、`s3Region=sg-devbox-1` 与
`s3UsePathStyle=true`，以 Pulumi 管理的 SeaweedFS bucket-scoped 身份访问 `agentserver` bucket。
Gateway Pod 扩缩容、换节点或入口地址变化不需要重新生成 Chart。新代码提交后仍须重新构建、远端核验和
替换 service/harness digest 及对应 runtime artifact；Hydra digest 只在升级 Hydra 版本时变化。资源配额
可按最终容量审计调整。S3 endpoint 是必填项，不能省略后回退到
AWS 默认 endpoint。S3 对象是明文；bucket、credential、备份、retention 和访问审计
必须按明文数据边界管理。S3 不是 PostgreSQL 或完整用户 session 数据库的替代品，只保存
prompt/checkpoint 对象，数据库保存状态与 pointer。

v6 managed sandbox catalog 必须包含：

- `sandboxRegions.defaultRegion/regions`：`defaultRegion` 固定为 `i18n-tt`；`regions` 仅列出本 release 实际
  安装、可供 workspace 选择的地域，且必须包含 `i18n-tt`；
- `proxyProfiles`：`merlin-hl-1`、`merlin-useast14a-1`、`merlin-maliva-1` 中实际使用项的完整 URL、namespace、
  Pod selector 和 port；BOE 不在这里配置；
- 每个 `sandboxProfiles[]`：唯一 profile/environment/Gateway authority、TAE Sandbox/revision、官方
  control/data authority、ByteCloud site/JWT endpoint、proxy name 或 BOE 双栈 CIDR，以及独立 policy/network
  evidence；
- legacy `managedExecutor.environment/tae`、`services.sandboxGateway`、`secrets.sandboxGateway` 和
  `network.sandboxExternalEgress` 必须逐字匹配 default profile，避免旧 consumer 与 catalog 分叉。

active profile 的 `networkEvidence.reportSha256` 由 activation 从该地域 canonical report 字节计算；
`evidenceRef` 指向不可变报告；`bindingSha256` 绑定 report、region、TAE authority、JWT endpoint、proxy 的
完整 Kubernetes authority或 BOE direct CIDR、DNS、Gateway 和 policy shape。修改任何事实后必须对受影响
catalog 重新生成全地域 bootstrap、执行每地域实测并原子激活；不能手工替换 digest 或复用旧报告。
`LockRelease` 会再次核对每个 profile，并拒绝模板 sentinel 或明显伪摘要。

第一次发布先从经过普通配置校验的 active 模板生成无权限 bootstrap 文件；输出必须是新路径：

```bash
cd /absolute/agentserver/v2
go run ./cmd/agentserver-deploy prepare-policy-bootstrap \
  --config=/absolute/active-template.json \
  --output=/absolute/policy-bootstrap.json
```

`retarget-direct-terminal-sandbox` 只用于单 profile 兼容配置的 default profile 原子轮换；它不是多地域
catalog 编辑器。四地域模板必须先完整声明所有 profile 并通过 `validate`，再统一进入 bootstrap/探针/激活。
单 profile 轮换示例：

```bash
go run ./cmd/agentserver-deploy retarget-direct-terminal-sandbox \
  --config=/absolute/policy-bootstrap.json \
  --output=/absolute/retargeted-bootstrap.json \
  --expected-sandbox-id=<current-sandbox-id> \
  --sandbox-id=<new-sandbox-id> \
  --revision-id=<published-terminal-revision-id> \
  --environment-id=<fresh-canonical-uuid> \
  --managed-sandbox-image=registry-sg.byted.cs.ac.cn/ghcr/agentserver/v2-managed-sandbox@sha256:<digest>
```

不能用 `jq` 或文本替换分别修改 identity/binding 字段；正式探针和 activation 必须以经过 loader 校验的
bootstrap 输出为唯一输入。

TAE 审批完成后，通过 Pulumi 在**当前已部署且已锁镜像**的 bootstrap release 中启用一次性探针；不能拿
active 模板、另一版配置或手工 manifest 代替。Chart 默认值如下，只有 bootstrap Chart 的 closed values
schema 接受非空 revision；每个已安装地域都必须填写：

```yaml
taeNetworkProbe:
  enabled: false
  policyRevisions:
    cn: ""
    boe: ""
    i18n-bd: ""
    i18n-tt: ""
```

Pulumi 在同一次更新中先写入 AK/SK，再为每个地域创建 immutable ConfigMap、共用的专用 ServiceAccount、
独立 NetworkPolicy 和 `backoffLimit=0` Job：

```bash
cd /absolute/k8s-byted
pulumi config set --stack sg --path 'agentserver:taeNetworkProbePolicyRevisions.cn' '<cn-revision>'
pulumi config set --stack sg --path 'agentserver:taeNetworkProbePolicyRevisions.boe' '<boe-revision>'
pulumi config set --stack sg --path 'agentserver:taeNetworkProbePolicyRevisions.i18n-bd' '<i18n-bd-revision>'
pulumi config set --stack sg --path 'agentserver:taeNetworkProbePolicyRevisions.i18n-tt' '<i18n-tt-revision>'
pulumi up --stack sg

for region in cn boe i18n-bd i18n-tt; do
  probe_job=$(kubectl --context '<sg-context>' --namespace agentserver get jobs \
    --selector="app.kubernetes.io/name=tae-network-probe-${region}" \
    --output=jsonpath='{.items[0].metadata.name}')
  test -n "${probe_job}"
  kubectl --context '<sg-context>' --namespace agentserver logs "job/${probe_job}" \
    | sed -n '/^{"schemaVersion":5,"kind":"agentserver.tae.network-report"/p' \
    > "/absolute/${region}-tae-network-report.json"
  test "$(wc -l < "/absolute/${region}-tae-network-report.json" | tr -d ' ')" = 1
  chmod 0600 "/absolute/${region}-tae-network-report.json"
done
```

上例按四地域完整 catalog 展示；若 release 只安装包含 `i18n-tt` 的子集，只能设置和采集该子集，不能为未安装地域注入
revision。反过来，少任何一个已安装地域也会被 Helm guard 或 activation 拒绝。

探针默认执行 20 次 bypass-cache JWT 强制刷新、20 次不存在 metadata 的 exact Search，以及一次
Create→唯一 Search→Ready→TTL→`lark-cli --version`→Stat→完整读取并校验 42MB CLI→读取并校验
Skill→Delete→Get/cleanup 确认。任一步失败都会输出 `passed=false` 并以非零退出；报告只含固定配置、
耗时和稳定错误类别，不含 AK/SK/JWT、响应 body 或 Session ID。先保存失败报告和 Pod event 排障，不能
把它送入 activation。报告本身必须先上传到不可变 evidence store。保存报告和所需诊断信息后，由
Pulumi 关闭探针并删除这些临时资源；不要直接 `kubectl delete`：

```bash
cd /absolute/k8s-byted
pulumi config rm --stack sg agentserver:taeNetworkProbePolicyRevisions
pulumi up --stack sg
```

如果旧 stack 仍有单值 `taeNetworkProbePolicyRevision`，先删除它；新 map 与旧单值同时存在必须 fail
closed。取得不可变报告引用后创建受 Schema 约束的 activation manifest：

```json
{
  "schemaVersion": 1,
  "profiles": [
    {"region":"cn","policyRevision":"<cn-revision>","policyEvidenceRef":"artifact://policy/cn","networkReportPath":"/absolute/cn-tae-network-report.json","networkEvidenceRef":"artifact://network/cn"},
    {"region":"boe","policyRevision":"<boe-revision>","policyEvidenceRef":"artifact://policy/boe","networkReportPath":"/absolute/boe-tae-network-report.json","networkEvidenceRef":"artifact://network/boe"},
    {"region":"i18n-bd","policyRevision":"<i18n-bd-revision>","policyEvidenceRef":"artifact://policy/i18n-bd","networkReportPath":"/absolute/i18n-bd-tae-network-report.json","networkEvidenceRef":"artifact://network/i18n-bd"},
    {"region":"i18n-tt","policyRevision":"<i18n-tt-revision>","policyEvidenceRef":"artifact://policy/i18n-tt","networkReportPath":"/absolute/i18n-tt-tae-network-report.json","networkEvidenceRef":"artifact://network/i18n-tt"}
  ]
}
```

从同一 bootstrap 配置执行唯一 activation 边。命令读取全部 canonical reports，自行计算每个
`reportSha256`，并同时重算 policy/network/runtime/pack/profile bindings；不接受人工填写 report SHA：

```bash
go run ./cmd/agentserver-deploy activate-managed-sandbox-profiles \
  --config=/absolute/policy-bootstrap.json \
  --output=/absolute/active-production.json \
  --evidence-manifest=/absolute/managed-sandbox-evidence.json
```

后续 active 网络事实、镜像或 policy revision 发生变化时，不允许直接替换摘要或重绑旧报告。必须先从
当前有效 active 配置生成新的全地域 bootstrap，部署并重新执行全部已安装地域 probe，再通过同一个
`activate-managed-sandbox-profiles` 晋级。activation 是 all-or-nothing，不允许某个地域继承 default
profile 的 evidence 或继续使用旧 lock。

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

模块要求两个配置同时存在，并把同一对基础设施凭据作为原子 SecretPatch 写入每个已安装 profile 的
`gateway.secret`；各 Secret 中的 TLS identity 仍独立。probe 和 active sandbox-gateway 只读文件挂载；
Core、Executor、Harness、可选 egress-authorizer、TAE Session env/metadata 均拿不到这对凭据。Pulumi
backend 必须配置加密 secrets provider 和受控 state ACL；未确认这一边界前不得设置。

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

# managedExecutor.stage=active 时逐个核对已安装 provider boundary；按实际 catalog 调整列表。
for component in sandbox-gateway-cn sandbox-gateway-boe sandbox-gateway-i18n-bd sandbox-gateway; do
  kubectl --context '<sg-context>' --namespace agentserver \
    rollout status "deployment/${component}" --timeout=10m
  kubectl --context '<sg-context>' --namespace agentserver \
    get "service/${component}" "networkpolicy/${component}"
done

# 当前 direct catalog 不应存在任何 Webhook 资源。
test "$(kubectl --context '<sg-context>' --namespace agentserver get \
  deployment/egress-authorizer service/egress-authorizer \
  httproute/agentserver-egress-authorizer-webhook \
  backendtlspolicy/agentserver-egress-authorizer-backend-tls \
  networkpolicy/egress-authorizer --ignore-not-found --output=name)" = ""
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

direct `policy-bootstrap` 部署后不应存在 sandbox-gateway 或 egress-authorizer Deployment/Service/Route。

切换到 `active` 后，managed executor 的 golden path 还必须单独验证：

1. 每个 region-specific sandbox-gateway 都已用该 profile 配置的 ByteCloud site/JWT endpoint 成功换取
   JWT 并完成 TAE readiness；AK/SK/JWT 不出现在 Executor、Harness、sandbox/TAE metadata、env、proc、
   文件、stdout/stderr、checkpoint 或日志；
2. Platform 为测试 workspace 创建 active `kind=lark` binding，并显式设置 `process_env`；Core workspace
   credential service 能按 operation 解析并 materialize。当前 static bearer 的轮换由 Platform 发起，不存在
   后台 OAuth refresh。无需 Core bootstrap Lark grant、Pulumi expiry 或 Helm release。只允许目标
   `lark-cli`/子进程 env 与内存出现，其他进程、metadata、文件、result、checkpoint 和日志仍必须为零；
3. `harness-worker` 依据既有 Lark skill 通过 MCP `shell` 调用 pinned `lark-cli`，不新增模型可见工具；
4. 新 direct Sandbox 的创建 payload/revision readback 没有 webhook 或自定义 Feishu policy；TAE 系统 policy
   readback 包含 `*.feishu.cn`。非 `lark-cli` operation 不触发 direct resolve，workspace 被切到
   `webhook_swap` 时明确拒绝，不允许自动创建 webhook；
5. 对 `cn/boe/i18n-bd/i18n-tt` 每个已安装地域分别测 ByteCloud JWT endpoint、TAE control/data-plane 与
   Lark 路径的 IPv4、IPv6、DNS、redirect、CONNECT、IP literal bypass、PMTU/MTU/MSS 和错误率；同时确认
   CN/i18n-bd/i18n-tt 只走配置的 Merlin，BOE 只走 direct allowlist，
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

ByteCloud AK/SK 必须作为一对原子更新所有已安装 profile 的 Gateway Secret，随后等待所有
sandbox-gateway 新 Pod readiness 通过；旧 Pod 退出后再撤销旧 key。不要在一个运行中 Pod 内分别替换
两个 subPath 文件，也不要把 JWT 当作需要持久化轮换的 Secret。

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
- 外部请求 timeout：普通外部系统更新正确 egress CIDR/port 后重新生成 Chart；TAE proxy profile 检查
  对应 Merlin Pod/Service/NetworkPolicy、SOCKS remote DNS 和 SSH 链路；BOE 检查双栈 direct CIDR，不能
  临时切换另一地域、扩大 `0.0.0.0/0`/`::/0` 或在线 patch；
- Helm guard 失败：Chart 与 `deploymentConfigSHA256` 不匹配，必须选择同一份生成制品。
- managed sandbox ensure 卡住：从 Run 的 Trajectory 读取冻结的 `managedSandboxRegion`、
  `managedSandboxProfile`、`managedSandboxEnvironment`、`managedSandboxBinding`，再检查对应
  sandbox-gateway readiness、Core mTLS、该 profile 的 proxy/direct route、ByteCloud
  site/JWT endpoint、TAE PSM/ACL、session metadata digest 和 TTL；不要手工改 CreateSession 字段；
- TAE Policy Webhook 无回调或 403：确认 TAE policy 已发布/审批、`open.feishu.cn` 精确 whitelist、
  `/v1/policy`、PSM/URL authority 和两端 binding SHA-256 一致，并检查 SG IPv4/IPv6/PMTU；
- managed Lark 请求被拒绝：查看 egress deny reason class、grant version/fence 和 placeholder JTI，
  不要把真实 access/refresh token 写入 sandbox env 或临时放宽 NetworkPolicy。
