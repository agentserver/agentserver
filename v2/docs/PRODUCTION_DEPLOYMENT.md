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

- 5 个常驻 Deployment：core、browser-gateway、executor-gateway、harness-pool、llmproxy；
- executor-gateway 固定单副本、`Recreate`、无 HPA/PDB；
- 4 个固定 ClusterIP Service、3 条 HTTPRoute、8 条 NetworkPolicy；
- migration `pre-install,pre-upgrade` Job；
- 幂等 bootstrap `post-install,post-upgrade` Job。

Job 只用于数据库 migration/bootstrap。每个 run 的 harness 仍由 harness-pool 在本 Pod 内普通
`fork/exec` 一个短命 harness-worker，不创建 Kubernetes Job 或 Pod。

`../k8s-byted/apps/agentserver.ts` 只在 `sg` stack 生效，并负责创建：

- `agentserver` Namespace；
- 内部 ECDSA CA、6 个带精确 SPIFFE URI 的 workload 证书；
- run-capability 与 run-manifest 两套 Ed25519 signer/keyring；
- executor enrollment、login transaction、run cursor 和 workspace LLM Gateway grant sealing 四个独立的
  256-bit key/keyring；
- 32-byte 随机 PostgreSQL owner 密码、`kubernetes.io/basic-auth` Secret；
- `agentserver-postgres` CloudNativePG Cluster：3 个实例，每实例 50Gi Longhorn；
- CNPG 1.30.0 支持的 PostgreSQL 17.6 system-trixie amd64 manifest（tag + digest 双重固定）；
- 指向 `agentserver-postgres-rw.agentserver.svc.cluster.local:5432` 的应用 DSN；
- 7 个应用 Secret 和一个 registry pull Secret；
- 环境锁定的 OCI Helm release。

证书和密钥由 Pulumi `tls`/`random` provider 生成，用户不需要手工生成文件、选择 Secret 名称
或执行 `kubectl create secret`。Deployment 带 Reloader annotation，受引用 Secret 更新时会
触发 rollout。

以下值是外部系统已经认可的授权，不能用随机字节替代；Pulumi 只负责安全接入并组装最终
Kubernetes Secret：

| Pulumi 接入值 | 来源 |
| --- | --- |
| `externalOidcClientSecret` | 外部 OIDC 中已注册的 AgentServer client |
| `s3AccessKeyId` / `s3SecretAccessKey` | 目标 S3-compatible bucket 的真实 credential |
| `registryDockerConfigJson` | SG registry 可用的 Docker config JSON |

如果这些外部系统由 Pulumi provider 管理，应直接把对应资源 Output 传给 AgentServer 模块；当前
模块也保留加密 Pulumi config 接口作为接入点。不要生成一个外部系统从未注册的随机密码并把它
当成可用 credential。

PostgreSQL 不再是外部输入。Pulumi 自动生成：

```text
postgres://agentserver:<generated-hex-password>@agentserver-postgres-rw.agentserver.svc.cluster.local:5432/agentserver?sslmode=require
```

同一密码 Secret 同时用于 CNPG `initdb` 和 declarative role reconciliation，DSN 只写入
`agentserver-core-secrets/database-url`。用户不需要填写或读取密码。`../k8s-byted` 仍需先安装
CNPG operator 和 Longhorn；AgentServer 模块管理 Cluster 声明，但不替代数据库备份与恢复策略。

`externalOidcClientSecret` 只属于 **AgentServer 平台登录** 的 confidential client。workspace LLM
Gateway 使用 workspace 自己在第三方 IdP 注册的 public client ID + PKCE，不向 Pulumi 或 AgentServer
上传 client secret。

Chart/Pulumi 不管理 Hydra、外部 OIDC IdP、S3 bucket、第三方 LLM Gateway、Istio Gateway、DNS 和
用户机器上的 agentx。

## 3. 部署前必须准备的真实输入

发布前必须确认：

1. SG 集群只有 `linux/amd64` 目标节点，且容量足够；
2. CNI 实际执行 Kubernetes NetworkPolicy；集群已安装 Gateway API CRD、Istio、Reloader、
   CloudNativePG operator 和 Longhorn；
3. `https-byted-bps` listener 的证书和 `allowedRoutes` 已覆盖三个固定域名/Namespace；
4. Longhorn 至少能为 3 个 PostgreSQL 实例各提供 50Gi 卷，并有跨节点调度容量；
5. Hydra Admin/Public/introspection/issuer/browser client 已配置；
6. 外部 OIDC issuer/client 已配置，redirect URI 精确为
   `https://agent.byted.bps.dev/auth/oidc/callback`，并取得 owner 的精确 `sub`；
7. S3 endpoint、signing region、bucket、prefix、path-style 和 credential 已确定；
8. S3-compatible 服务支持 single-part `PutObject` 的 `If-None-Match: *`，并把已存在对象明确返回
   为 `412 PreconditionFailed`；
9. 至少一个待接入的第三方 Gateway 支持公网、系统信任 HTTPS 的精确 `/v1/responses` endpoint，
   streaming 和 stock Codex 所需 tool-call 语义；其 OIDC public client 已允许精确 callback
   `https://agent.byted.bps.dev/auth/llm-gateway/callback`；
10. SG registry credential 可被集群节点用于拉取两个私有 amd64 镜像；
11. CoreDNS ClusterIP、Gateway Pod label selector、Service CIDR 中 4 个空闲固定 ClusterIP，以及
    S3/平台 OIDC/Hydra 的实际 IPv4 CIDR/port 已确定。

这些非 Secret 参数写入 `production.json`，不是 Pulumi Secret：

| 外部系统 | `production.json` 字段 |
| --- | --- |
| 外部 OIDC | `oauth.externalOidc.issuer`、`clientId`；`redirectUrl` 固定为上述回调地址 |
| S3-compatible | `objectStore.s3Endpoint`、`s3Region`、`s3Bucket`、`prefix`、`s3UsePathStyle` |
| Hydra | `oauth.hydra` 下的 issuer、Admin/Public/introspection URL 与 browser client ID |

第三方模型 Gateway **不写入** `production.json` 或 Pulumi config。部署完成后由 workspace owner 通过
前端/API 写入 Gateway URL、OIDC issuer、public client ID、scopes、bearer 类型和默认 model；每个成员
只关联自己的 OIDC grant。

对应 Secret 的精确含义：

- `externalOidcClientSecret` 是与 `oauth.externalOidc.clientId` 同一注册项的 client secret；
- `registryDockerConfigJson` 是只包含目标 registry 的最小 Docker `config.json`，顶层只能有
  `auths`，且只能包含 `auths["registry-sg.byted.cs.ac.cn"].auth`；该值是 canonical base64 编码的非空
  `username:password`。Pulumi 将完整 JSON 写入 `kubernetes.io/dockerconfigjson` Secret 供 kubelet
  拉镜像，同时把解析出的 username/password 作为 secret inputs 交给 Helm provider 拉私有 OCI Chart。
  只有本机 credential helper、却没有上述 `auth` 项的 config 不能用于无人值守部署。

不存在 `upstreamCaPem`、`upstreamCredential` 或 llmproxy 静态上游 Secret。Core 用 Pulumi 生成的
`llm-gateway-sealing-keyring.json` 加密每个 `(workspace, gateway, user)` token set；这个 key 只能解密
数据库 grant，本身不能调用模型。

外部系统的 NetworkPolicy 使用 CIDR，不会在 DNS 变化时自动更新。PostgreSQL 不使用外部 CIDR，
固定按 `cnpg.io/cluster=agentserver-postgres` Pod selector 放行 TCP 5432。至少需要：

- core：CNPG PostgreSQL、Hydra Admin、平台 OIDC、S3，以及动态 workspace Gateway OIDC 的公网 443；
- browser-gateway：Hydra Public；
- harness-pool：S3；
- llmproxy：动态 workspace Gateway `/v1/responses` 的公网 443；
- migration/bootstrap：CNPG PostgreSQL。

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

从远端 registry 重新查询两个 amd64 manifest digest，并把 `production.json` 中镜像写成：

```text
registry-sg.byted.cs.ac.cn/agentserver/v2-service@sha256:<remote-amd64-digest>
registry-sg.byted.cs.ac.cn/agentserver/v2-harness@sha256:<remote-amd64-digest>
```

不要写 tag、本地 image ID、arm64 digest 或未经核验的 multi-arch index digest。

## 5. 准备并校验 SG production.json

复制 [`deploy/production/config.example.json`](../deploy/production/config.example.json) 到一个绝对、
不可被 group/other 写入的安全路径。模板已经写入本轮远端 amd64 manifest digest、四个已 dry-run
确认可分配的 SG ClusterIP、CoreDNS `192.168.0.10`、固定 bootstrap UUID、runtime manifest 和
`harness-final-exec` 摘要。以下字段已经固定，不能修改：

- `region=sg`、`namespace=agentserver`、`platform=linux-amd64`；
- `spiffeTrustDomain=agentserver.byted.bps.dev`；
- 三个域名及 Istio Gateway/listener；
- `run-capability-sg-v1`、`run-manifest-sg-v1`；
- 7 个 Secret 名称和 `agentserver-registry-pull`；
- `objectStore.mode=s3-plaintext-v1`；
- 镜像仓库只能是 `registry-sg.byted.cs.ac.cn/agentserver/v2-service` 和 `v2-harness`，且必须使用 digest。

在发布当前提交时，只需替换 owner 精确 `sub`、Hydra/平台 OIDC、S3 endpoint/region/bucket/prefix/
path-style、外部系统 egress CIDR；资源配额可按最终容量审计调整。发布后续代码版本时还必须重新构建、
远端核验并替换两个镜像 digest 及对应 runtime artifact。S3 endpoint 是必填项，不能省略后回退到
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
pulumi config set --stack sg --secret agentserver:registryDockerConfigJson '<docker-config-json>'
```

最后一项的精确形状是：

```json
{"auths":{"registry-sg.byted.cs.ac.cn":{"auth":"<canonical-base64-of-username:password>"}}}
```

发布 Chart 的操作者仍需先登录 registry 执行 `helm push`。部署阶段不依赖另一次交互式
`helm registry login`：Pulumi 会从上述 Docker config 的精确 registry `auth` 项获得 OCI Chart
拉取凭据，并以 secret 传播；若该项不存在，preview/up 会在创建任何 AgentServer 资源前失败。

不要设置 `agentserver:databaseUrl`。CNPG owner 密码和 DSN 在 `pulumi up` 时自动生成；模块会等待
CNPG `Cluster` 的 `Ready` condition，再启动 Helm migration/bootstrap hooks。

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
  clusters.postgresql.cnpg.io,deployments,pods,services,httproutes,networkpolicies

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
数据库备份、PVC retention 和恢复演练；Namespace 删除可能导致数据库数据不可恢复。

## 10. 常见故障

- HTTPRoute `Accepted=False`：检查 listener `allowedRoutes`、三个 hostname 和 `sectionName`；
- 公网 503：检查 Istio Gateway Pod selector、NetworkPolicy 和后端 HTTP `8080`；
- executor 公网 `/mcp` 非 404：立即停止发布，公网路由面发生越界；
- CNPG Cluster 不 Ready：检查 operator、Longhorn PVC、节点容量以及 CNPG Pod/事件；
- migration 失败：检查 `agentserver-postgres-rw`、owner Secret、TLS 和 CNPG Pod selector egress；
- S3 写失败：重点检查 `If-None-Match: *`、`412 PreconditionFailed` 语义、endpoint/path-style/region；
- Pod 卡在 init：检查 Pulumi Secret 是否齐全、证书 SPIFFE URI、keyring 和私钥格式；
- ImagePullBackOff：检查 `agentserver-registry-pull` 的 Docker config 和远端 amd64 digest；
- 外部请求 timeout：更新正确 egress CIDR/port 后重新生成 Chart，不能在线 patch；
- Helm guard 失败：Chart 与 `deploymentConfigSHA256` 不匹配，必须选择同一份生成制品。
