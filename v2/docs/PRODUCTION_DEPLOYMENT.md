# Agentserver v2 SG 生产部署

本文只描述 SG 生产部署。当前生产配置是封闭的，不支持多地域或多架构复用：

- Pulumi stack 必须为 `sg`；
- Kubernetes 节点必须为 `linux/amd64`；
- Namespace 固定为 `agentserver`，Helm release 固定为 `agentserver-v2`；
- OCI registry 固定在 `registry-sg.byted.cs.ac.cn/agentserver/`；
- 公网 TLS 由 `istio-ingress/istio-gateway` 的 `https-byted-bps` listener 终止；
- 应用的公网后端使用集群内明文 HTTP，组件之间的内部控制面继续使用 TLS/mTLS；
- prompt/checkpoint 以明文对象写入 S3-compatible bucket，应用仍逐次校验 pointer、size 和 SHA-256。

生产 Chart 不是通用 `values.yaml`。`agentserver-deploy chart` 会把一份通过 closed-world
校验的 `production.json` 生成为环境锁定 Chart。镜像、网络、平台登录 OIDC 或对象存储配置发生
变化时，必须重新生成并发布 Chart，不能在 Helm/Pulumi 安装时临时覆盖。

这里的 closed-world 只约束平台基础设施。模型路由不再锁进 Chart：workspace owner 在部署后关联
Responses-compatible 的第三方 LLM Gateway，每个成员再用 OIDC Authorization Code + PKCE 授权自己的
grant。平台没有统一的模型 API key。

## 1. 固定公网拓扑

| Host | 公开路径 | 后端 |
| --- | --- | --- |
| `agent.byted.bps.dev` | `/`、`/index.html`、`/reference*`、`/auth*`、`/oauth2*`、`/readyz` | browser-gateway HTTP `8080` |
| `browser-gateway.byted.bps.dev` | `/v2*` | browser-gateway HTTP `8080` |
| `executor-gateway.byted.bps.dev` | `/internal/v2/agentx/enrollments`、`/internal/v2/agentx/challenges`、`/internal/v2/agentx/connect` | executor-gateway HTTP `8080` |

三条 `HTTPRoute` 都挂到：

```text
namespace:   istio-ingress
gateway:     istio-gateway
sectionName: https-byted-bps
```

Chart 不创建 Gateway、证书、LoadBalancer 或 external-dns 资源。现有 Istio listener 必须：

1. 持有覆盖上述三个域名的公网证书；
2. 允许 `agentserver` Namespace 中的 HTTPRoute attach；
3. 将 TLS 终止后的 HTTP 请求转发给 ClusterIP Service。

前端只允许 `https://agent.byted.bps.dev` 作为浏览器 Origin，并跨域访问
`https://browser-gateway.byted.bps.dev/v2`。CORS 不允许 credentials。executor 的公网 listener
绝不注册 `/mcp`；`/mcp` 只存在于 executor-gateway 的内部 TLS `8443` listener，并只允许
harness-pool Pod 访问。

## 2. Chart 与 Pulumi 的管理边界

Chart 管理：

- 6 个常驻 Deployment：core、browser-gateway、executor-gateway、harness-pool、llmproxy、Hydra；
- executor-gateway 固定单副本、`Recreate`、无 HPA/PDB；
- 5 个固定 ClusterIP Service、3 条 HTTPRoute、11 条 NetworkPolicy；
- Hydra SQL migration（weight `-20`）和 AgentServer migration（weight `-10`）
  `pre-install,pre-upgrade` Job；
- Hydra browser OAuth client setup（weight `-10`）和幂等 AgentServer bootstrap（weight `0`）
  `post-install,post-upgrade` Job。

Job 只用于控制面安装期的数据库 migration、Hydra client setup 和 bootstrap。每个 run 的 harness 仍由 harness-pool 在本 Pod 内普通
`fork/exec` 一个短命 harness-worker，不创建 Kubernetes Job 或 Pod。

`../k8s-byted/apps/agentserver.ts` 只在 `sg` stack 生效，并负责创建：

- `agentserver` Namespace；
- 内部 ECDSA CA、7 个带精确 SPIFFE URI 的 workload 证书（包含 Hydra server identity）；
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
- 8 个应用 Secret；
- 环境锁定的 OCI Helm release。

证书和密钥由 Pulumi `tls`/`random` provider 生成，用户不需要手工生成文件、选择 Secret 名称
或执行 `kubectl create secret`。Deployment 带 Reloader annotation，受引用 Secret 更新时会
触发 rollout。Namespace、CNPG Cluster/Database、PostgreSQL owner Secret 和 8 个托管 Secret 都设置
`protect: true`：正常轮换仍可原地更新，但误关模块或普通 `pulumi destroy` 不能删除数据库和密钥。

以下值是外部系统已经认可的授权，不能用随机字节替代；Pulumi 只负责安全接入并组装最终
Kubernetes Secret：

| Pulumi 接入值 | 来源 |
| --- | --- |
| `externalOidcClientSecret` | 外部 OIDC 中已注册的 AgentServer client |
| `s3AccessKeyId` / `s3SecretAccessKey` | 目标 S3-compatible bucket 的真实 credential |

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

Hydra 26.2.0 由同一 Chart 管理。Chart 固定 issuer、Admin/Public endpoint、PKCE、opaque access token、
双副本 Deployment 和 browser public client profile；Pulumi 管理它的 database、DSN、内部 TLS 与
system/cookie secret。Chart/Pulumi 不管理外部 OIDC IdP、S3 bucket、第三方 LLM Gateway、Istio
Gateway、DNS 和用户机器上的 agentx。

## 3. 部署前必须准备的真实输入

发布前必须确认：

1. SG 集群只有 `linux/amd64` 目标节点，且容量足够；
2. CNI 实际执行 Kubernetes NetworkPolicy；集群已安装 Gateway API CRD、Istio、Reloader、
   CloudNativePG operator 和 Longhorn；
3. `https-byted-bps` listener 的证书和 `allowedRoutes` 已覆盖三个固定域名/Namespace；
4. Longhorn 至少能为 3 个 PostgreSQL 实例各提供 50Gi 卷，并有跨节点调度容量；
5. 外部 OIDC issuer/client 已配置，redirect URI 精确为
   `https://agent.byted.bps.dev/auth/oidc/callback`，并取得 owner 的精确 `sub`；
6. S3 endpoint、signing region、bucket、prefix、path-style 和 credential 已确定；
7. S3-compatible 服务支持 single-part `PutObject` 的 `If-None-Match: *`，并把已存在对象明确返回
   为 `412 PreconditionFailed`；
8. 至少一个待接入的第三方 Gateway 支持公网、系统信任 HTTPS 的精确 `/v1/responses` endpoint，
   streaming 和 stock Codex 所需 tool-call 语义；其 OIDC public client 已允许精确 callback
   `https://agent.byted.bps.dev/auth/llm-gateway/callback`；
9. SG 公开 registry 可由集群节点匿名拉取 service、harness、Hydra 三个 digest-pinned amd64 镜像
   和环境锁定 Chart；
10. CoreDNS ClusterIP、Gateway Pod label selector、Service CIDR 中 5 个空闲固定 ClusterIP，以及
    S3/平台 OIDC 的实际 IPv4 CIDR/port 已确定。

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

`registry-sg.byted.cs.ac.cn/agentserver` 的拉取面是公开的。生产配置拒绝 `pullSecret`，生成的 Pod 不含
`imagePullSecrets`，Pulumi 也不接收 `registryDockerConfigJson`。发布镜像或 Chart 所需的写权限只属于
发布工作站，不进入 SG stack、Kubernetes Secret 或运行时 Pod。

不存在 `upstreamCaPem`、`upstreamCredential` 或 llmproxy 静态上游 Secret。Core 用 Pulumi 生成的
`llm-gateway-sealing-keyring.json` 加密每个 `(workspace, gateway, user)` token set；这个 key 只能解密
数据库 grant，本身不能调用模型。

外部系统的 NetworkPolicy 使用 CIDR，不会在 DNS 变化时自动更新。PostgreSQL 不使用外部 CIDR，
固定按 `cnpg.io/cluster=agentserver-postgres` Pod selector 放行 TCP 5432。至少需要：

- core：CNPG PostgreSQL、集群内 Hydra Admin、平台 OIDC、S3，以及动态 workspace Gateway OIDC 的公网 443；
- browser-gateway：Hydra Public；
- harness-pool：S3；
- llmproxy：动态 workspace Gateway `/v1/responses` 的公网 443；
- Hydra/AgentServer migration 与 bootstrap：CNPG PostgreSQL；Hydra client setup：集群内 Hydra Admin。

## 4. 构建并发布 SG amd64 镜像

旧 registry digest 不包含本轮 SG、双 listener、分域和明文 S3 改动，不能继续使用。构建前
`v2/` 必须已经提交且工作树干净。构建脚本只接受锁定的 stock Codex `0.146.0` amd64 artifact
和审核过的 amd64 bwrap：

```bash
cd /absolute/agentserver/v2

./deploy/production/build-images.sh \
  --platform=linux-amd64 \
  --codex=/absolute/codex-x86_64-unknown-linux-musl \
  --bwrap=/absolute/bwrap-x86_64-unknown-linux-musl \
  --service-image=registry-sg.byted.cs.ac.cn/agentserver/v2-service:<git-sha> \
  --harness-image=registry-sg.byted.cs.ac.cn/agentserver/v2-harness:<git-sha> \
  --output-dir=/absolute/new-agentserver-image-evidence
```

脚本会验证 OCI manifest、`linux/amd64` platform、descriptor/diff ID，以及镜像中每个文件的
owner/mode/size/SHA-256。只有输出 `verified production images` 后才可推送：

```bash
container registry login registry-sg.byted.cs.ac.cn
container image push registry-sg.byted.cs.ac.cn/agentserver/v2-service:<git-sha>
container image push registry-sg.byted.cs.ac.cn/agentserver/v2-harness:<git-sha>
```

Hydra 不从生产集群直接拉 Docker Hub。把官方 v26.2.0 的 `linux/amd64` manifest 原样镜像到 SG
公开仓库：

```bash
container image pull --platform linux/amd64 \
  docker.io/oryd/hydra@sha256:f59c2f7f4969269b154fa34c57bc4b849263ebedbcaf8114aaeb1658a3007b4b
container image tag \
  docker.io/oryd/hydra@sha256:f59c2f7f4969269b154fa34c57bc4b849263ebedbcaf8114aaeb1658a3007b4b \
  registry-sg.byted.cs.ac.cn/agentserver/hydra:v26.2.0
container image push --platform linux/amd64 \
  registry-sg.byted.cs.ac.cn/agentserver/hydra:v26.2.0
```

回查目标仓库的 amd64 manifest digest；只有目标 digest 仍为
`sha256:f59c2f7f4969269b154fa34c57bc4b849263ebedbcaf8114aaeb1658a3007b4b` 才能使用模板中的
Hydra reference。不能用 arm64 或 multi-arch index digest 替代。

从远端 registry 重新查询 service/harness 两个 amd64 manifest digest，并把 `production.json` 中镜像写成：

```text
registry-sg.byted.cs.ac.cn/agentserver/v2-service@sha256:<remote-amd64-digest>
registry-sg.byted.cs.ac.cn/agentserver/v2-harness@sha256:<remote-amd64-digest>
```

不要写 tag、本地 image ID、arm64 digest 或未经核验的 multi-arch index digest。

当前已发布并从远端回拉核验的 runtime source revision 为
`5f0c43b388669f33e54a55959c21b02fafd49539`：

| 镜像 | 远端 linux/amd64 manifest digest |
| --- | --- |
| `registry-sg.byted.cs.ac.cn/agentserver/v2-service` | `sha256:aa50b3718e3689409613f8a9e9f765a08eed38466bb8d60da995d3fb394b6718` |
| `registry-sg.byted.cs.ac.cn/agentserver/v2-harness` | `sha256:7607137412935aa7ced2afc7ed42318fa6457d16a9bd97d7dcb1cfbb5c59d0b7` |

service/harness 均为 `linux/amd64`，OCI revision label 与上述 source revision 完全一致。不要把远端 index digest
`sha256:69bf4bc16c6dd41804c5850be7fc5197735a7b4dd5d3572d76319a539a40446d` /
`sha256:f7fb74fd8b1d85d5bfea9d0771d5837fed65d30196143bbbf0e7eea11ceb7be4` 写入 production config。

## 5. 准备并校验 SG production.json

复制 [`deploy/production/config.example.json`](../deploy/production/config.example.json) 到一个绝对、
不可被 group/other 写入的安全路径。模板已经写入本轮远端 amd64 manifest digest、五个已 dry-run
确认可分配的 SG ClusterIP、CoreDNS `192.168.0.10`、固定 bootstrap UUID、runtime manifest 和
`harness-final-exec` 摘要。以下字段已经固定，不能修改：

- `region=sg`、`namespace=agentserver`、`platform=linux-amd64`；
- `spiffeTrustDomain=agentserver.byted.bps.dev`；
- 三个域名及 Istio Gateway/listener；
- `run-capability-sg-v1`、`run-manifest-sg-v1`；
- 8 个应用 Secret 名称；
- `objectStore.mode=s3-plaintext-v1`；
- 镜像仓库只能是 `registry-sg.byted.cs.ac.cn/agentserver/v2-service`、`v2-harness` 和 `hydra`，
  且必须使用 digest。

当前 SG 模板已经锁定 owner `sub=3506220589`、平台 OIDC、S3 endpoint/region/bucket/prefix/path-style
和 Chart 内置 Hydra 合同。发布前仍须替换示例 egress CIDR，并在新代码提交后重新构建、远端核验和
替换 service/harness digest 及对应 runtime artifact；Hydra digest 只在升级 Hydra 版本时变化。资源配额
可按最终容量审计调整。S3 endpoint 是必填项，不能省略后回退到
AWS 默认 endpoint。S3 对象是明文；bucket、credential、备份、retention 和访问审计
必须按明文数据边界管理。S3 不是 PostgreSQL 或完整用户 session 数据库的替代品，只保存
prompt/checkpoint 对象，数据库保存状态与 pointer。

校验：

```bash
chmod 0600 /absolute/production.json

cd /absolute/agentserver/v2
go run ./cmd/agentserver-deploy validate \
  --config=/absolute/production.json
```

## 6. 生成、验证并发布 OCI Chart

输出目录必须不存在：

```bash
mkdir -m 0700 /absolute/helm-artifacts

go run ./cmd/agentserver-deploy chart \
  --config=/absolute/production.json \
  --output=/absolute/helm-artifacts/agentserver-v2

helm lint --strict \
  --namespace agentserver \
  /absolute/helm-artifacts/agentserver-v2

helm template agentserver-v2 \
  /absolute/helm-artifacts/agentserver-v2 \
  --namespace agentserver \
  --set-string deploymentConfigSHA256='<full-config-sha256>'
```

Chart version 固定为 `0.1.0-config.d<config-sha256-first-12>`。完整摘要同时记录在
`Chart.yaml` annotation、`values.yaml` 和 `files/checksums.json` 中。确认后发布：

```bash
helm package \
  /absolute/helm-artifacts/agentserver-v2 \
  --destination /absolute/helm-artifacts/packages

helm push \
  /absolute/helm-artifacts/packages/agentserver-v2-0.1.0-config.d<first-12>.tgz \
  oci://registry-sg.byted.cs.ac.cn/agentserver
```

最终 Chart 地址为：

```text
oci://registry-sg.byted.cs.ac.cn/agentserver/agentserver-v2:<chart-version>
```

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
  rollout status deployment/browser-gateway --timeout=10m
kubectl --context '<sg-context>' --namespace agentserver \
  rollout status deployment/executor-gateway --timeout=10m
kubectl --context '<sg-context>' --namespace agentserver \
  rollout status deployment/harness-pool --timeout=10m
kubectl --context '<sg-context>' --namespace agentserver \
  rollout status deployment/llmproxy --timeout=10m
kubectl --context '<sg-context>' --namespace agentserver \
  rollout status deployment/hydra --timeout=10m
```

公网入口：

```bash
curl --fail --show-error https://agent.byted.bps.dev/readyz
curl --fail --show-error https://agent.byted.bps.dev/

# /mcp 不得从公网 executor 域名暴露，期望 404。
curl --output /dev/null --write-out '%{http_code}\n' \
  https://executor-gateway.byted.bps.dev/mcp
```

还需用真实浏览器完成平台 OIDC Code + PKCE 登录。在 “LLM Gateway” 设置中，workspace owner 创建
Gateway；随后 owner/developer 分别点击 Authorize，在 popup 中完成第三方 Gateway OIDC + PKCE。列表中
个人 `grantStatus=active` 后，才从前端经 `browser-gateway.byted.bps.dev/v2` 创建 run。Core 在 run
创建时冻结 Gateway/config version/user/model；llmproxy 每次请求向 Core live-authorize，不缓存 bearer。

完整 shell/read_file E2E 要求用户机器安装与本
Chart 相同 runtime manifest 的 agentx，完成 enrollment，并连接
`wss://executor-gateway.byted.bps.dev/internal/v2/agentx/connect`。服务端不会在集群内伪造 executor。

## 9. 升级、轮换与卸载

升级顺序固定为：新代码提交 → 构建/推送新 amd64 digest → 更新 `production.json` → 生成并
发布新 Chart → 更新 `deploymentConfigSHA256` → `pulumi preview` → `pulumi up`。

数据库 migration 只向前。Helm rollback 不回滚数据库，只有确认旧应用兼容当前 schema 时才
执行。Pulumi 生成的 TLS/随机资源会稳定保存在 SG stack state 中，不会每次 `pulumi up` 重建；
轮换资源会更新 Secret，并由 Reloader 触发工作负载重启。workspace Gateway sealing key 不能直接替换：
先发布 active 新 key + 旧 key 的 overlap keyring，完成数据库 grant 重封装后才能删除旧 key。签名密钥轮换仍要先发布包含新旧公钥
的 overlap keyring，再切 signer，最后删除旧公钥，不能一次替换 signer 和 verifier。

CNPG Cluster、PVC 和 S3 bucket 都是数据资源，不能把 `pulumi destroy` 或 Namespace 删除当作普通
应用回滚。删除 Cluster/Namespace、Pulumi random state、外部 credential 或 bucket 前，必须先验证
数据库备份、PVC retention 和恢复演练；Namespace 删除可能导致数据库数据不可恢复。正式退役必须
先在一次受审计的 Pulumi 变更中显式移除对应 `protect`，完成 preview/up 后再发起第二次删除变更，
不能用 `kubectl` 强删绕过保护。

## 10. 常见故障

- HTTPRoute `Accepted=False`：检查 listener `allowedRoutes`、三个 hostname 和 `sectionName`；
- 公网 503：检查 Istio Gateway Pod selector、NetworkPolicy 和后端 HTTP `8080`；
- executor 公网 `/mcp` 非 404：立即停止发布，公网路由面发生越界；
- CNPG Cluster 不 Ready：检查 operator、Longhorn PVC、节点容量以及 CNPG Pod/事件；
- Hydra Database 未 applied：检查 CNPG `Database` CR、`hydra` role reconciliation 和 owner Secret；
- migration 失败：检查 `agentserver-postgres-rw`、对应 owner Secret、TLS 和 CNPG Pod selector egress；
- Hydra client setup 失败：检查 Hydra readiness、内部 CA/SAN、Admin `4445` NetworkPolicy 和 client profile；
- S3 写失败：重点检查 `If-None-Match: *`、`412 PreconditionFailed` 语义、endpoint/path-style/region；
- Pod 卡在 init：检查 Pulumi Secret 是否齐全、证书 SPIFFE URI、keyring 和私钥格式；
- ImagePullBackOff：检查公开仓库匿名拉取策略、网络连通性和远端 amd64 digest；
- 外部请求 timeout：更新正确 egress CIDR/port 后重新生成 Chart，不能在线 patch；
- Helm guard 失败：Chart 与 `deploymentConfigSHA256` 不匹配，必须选择同一份生成制品。
