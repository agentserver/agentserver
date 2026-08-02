# Agentserver v2 远程 Helm 部署

本文描述当前 Phase 5 的可部署边界和远程 Kubernetes 安装流程。生产部署不是通用
`values.yaml` 拼装：`agentserver-deploy chart` 会先按 closed-world schema 校验一份
环境配置，再生成一份环境锁定、镜像 digest 锁定、Namespace 锁定的 Helm Chart。
修改镜像、网络、身份或资源配置时必须重新生成 Chart，不能在安装命令中临时覆盖。

## 1. Chart 管理边界

一个 release 管理：

- 5 个 Deployment：core、browser-gateway、executor-gateway、harness-pool、llmproxy；
- executor-gateway 固定单副本和 `Recreate`，其余副本数来自生产配置；
- Service、ServiceAccount、ConfigMap、PDB 和 8 条 NetworkPolicy；
- `pre-install,pre-upgrade` migration Job；
- `post-install,post-upgrade` 幂等 bootstrap Job。

以下资源故意不归 Chart 管理，卸载 release 时也不会删除：

- Namespace；
- 六组 Kubernetes Secret；
- PostgreSQL、Hydra、外部 OIDC IdP；
- S3 bucket、KMS key、AWS workload identity/IAM role；
- DNS zone、证书颁发系统和外部 LoadBalancer 控制器；
- 运行在用户机器上的 `agentx`。

migration 使用 namespace 已存在的 `default` ServiceAccount，且显式关闭 ServiceAccount
token；这样 Helm 可以在创建普通资源前完成迁移。首次安装时 Chart 自身的
NetworkPolicy 尚未创建，migration 二进制仍只有读取数据库 URL 并执行内嵌迁移的确定性
能力；对首次 hook 也要求严格网络隔离的集群，应在 Namespace 层预先配置基线 egress
策略。升级时上一版 migration NetworkPolicy 已经存在。

## 2. 必备条件

部署前必须同时满足：

1. 构建机安装 Go `1.26.5`、Apple container CLI `1.2.0`、Helm 3/4 和 kubectl。
2. 目标集群存在可调度的 `linux/arm64` 节点；当前生产 profile 不接受 amd64。
3. CNI 实际执行 Kubernetes NetworkPolicy；集群支持 projected ServiceAccount token、
   PDB、LoadBalancer Service 和固定 ClusterIP。
4. 目标 OCI registry 能被构建机推送、被 arm64 节点拉取，并能返回不可变 digest。
5. PostgreSQL 已创建数据库；migration 身份有 DDL 权限，运行期 core 身份至少有 DML
   权限。当前只接受一条 `database-url`，若暂未拆分身份则该 URL 同时供 migration、
   bootstrap 和 core 使用。
6. Hydra Admin/Public endpoint 和浏览器 OAuth client 已配置；外部 OIDC redirect URI 必须
   精确等于 `https://<browser public hostname>/auth/oidc/callback`。
7. S3、KMS 和两个 AWS role 已创建。S3 保存加密后的 prompt/checkpoint 对象，不是数据库
   或任意用户上下文的替代品；对象明文密钥由 KMS envelope encryption 保护。
8. 两个 public hostname 的 DNS、TLS 证书和入口 CIDR 已确定。

先显式选择目标，禁止依赖当前 context：

```bash
export KUBE_CONTEXT='replace-me'
export NAMESPACE='agentserver'
export RELEASE='agentserver-v2'

kubectl --context "${KUBE_CONTEXT}" cluster-info
kubectl --context "${KUBE_CONTEXT}" auth can-i create deployments.apps --namespace "${NAMESPACE}"
kubectl --context "${KUBE_CONTEXT}" get nodes \
  --selector='kubernetes.io/os=linux,kubernetes.io/arch=arm64'
```

在任何写操作前，再人工核对 `KUBE_CONTEXT` 指向的 cluster server 和账号。不要把真实生产
集群仅凭本机的 current-context 当作部署目标。

## 3. 构建并推送两个生产镜像

构建脚本只接受官方 stock Codex `0.146.0` 的已审核 linux/arm64 Codex 和 bwrap 文件，
不会联网下载或接受开放文件列表。`v2/` 必须先提交且与 `HEAD` 一致。

```bash
cd /absolute/agentserver/v2

./deploy/production/build-images.sh \
  --codex=/absolute/codex-aarch64-unknown-linux-musl \
  --bwrap=/absolute/bwrap-aarch64-unknown-linux-musl \
  --service-image=registry.example.com/agentserver/v2-service:<git-sha> \
  --harness-image=registry.example.com/agentserver/v2-harness:<git-sha> \
  --output-dir=/absolute/new-image-evidence
```

脚本会交叉编译九个 arm64 Go executable，组装两个 scratch 镜像，再通过
`container image save` 读取 OCI archive：校验唯一 `linux/arm64` manifest、所有 descriptor
digest、锁定的运行配置、两层 diff ID，以及 closed-world image manifest 中声明的逐文件
mode/owner/size/SHA-256。校验不使用运行后容器的 rootfs，避免把 runtime 注入的
`dev/proc/sys`、hostname 或 hosts 文件误认为镜像内容。只有脚本打印
`verified production images` 后才可推送：

```bash
container registry login registry.example.com
container image push registry.example.com/agentserver/v2-service:<git-sha>
container image push registry.example.com/agentserver/v2-harness:<git-sha>
```

从 registry 查询两个远端 digest，并把配置中的镜像写成完整
`repository@sha256:<64 hex>`。不要把本地 image ID、tag 或 manifest-list digest 与目标
arm64 manifest digest 混用。`new-image-evidence/` 应随部署记录保存，但不得放入 Secret。

## 4. 准备生产配置

复制 [`deploy/production/config.example.json`](../deploy/production/config.example.json) 到安全、
不可被 group/other 写入的绝对路径，然后替换所有示例值。关键输入如下：

| 区域 | 必须确认的内容 |
| --- | --- |
| `images` | 两个远端、arm64、digest-pinned 镜像 |
| `services` | 四个未占用且属于集群 Service CIDR 的固定 ClusterIP、两个 public hostname |
| `bootstrap` | 首个 owner、workspace、session、executor UUID，以及 IdP 的精确 `sub` |
| `oauth` | Hydra issuer/admin/public/introspection、browser client、外部 OIDC issuer/client/redirect |
| `runtime` | 两个签名 key ID、模型路由、审批策略、并发和 timeout；runtime manifest digest 固定 |
| `objectStore` | bucket/prefix、S3/KMS region/endpoint、KMS key、core/pool role ARN |
| `secrets` | 六个互不相同且已经规划好的 Secret 名称 |
| `network` | CoreDNS Service IP/Pod selector、数据库和所有外部依赖的实际 IPv4 CIDR/port |
| `resources` | request/limit 以及每个 harness Pod 的三个 tmpfs 上限 |

NetworkPolicy 使用 IP/CIDR，不会因域名变化自动更新。至少把以下真实目的地址归入对应
egress：

- core：PostgreSQL、Hydra Admin、外部 OIDC discovery/JWKS、S3/KMS/STS；
- browser：Hydra Public；
- harness：S3/KMS/STS；
- llmproxy：模型 `/v1/responses` endpoint；
- migration/bootstrap：PostgreSQL。

校验配置：

```bash
cd /absolute/agentserver/v2
go run ./cmd/agentserver-deploy validate \
  --config=/absolute/production.json
```

## 5. 准备 Namespace 与 Secret

先创建 Namespace；安装 Helm 时不要再传 `--create-namespace`，Chart 也不会接管已有
Namespace：

```bash
kubectl --context "${KUBE_CONTEXT}" create namespace "${NAMESPACE}"
```

若 Namespace 已存在，上述命令会失败，此时应检查其 owner、Pod Security、ResourceQuota、
LimitRange 和基线 NetworkPolicy，而不是用 `--force` 或 `--take-ownership`。

六组 Secret 的精确 key 集合如下。表中的文件名就是 Kubernetes Secret key：

| Secret profile | 必须包含的 key |
| --- | --- |
| core | `database-url`, `ca.crt`, `tls.crt`, `tls.key`, `run-capability.key`, `run-capability-keyring.json`, `executor-enrollment.key`, `external-oidc-client-secret`, `login-transaction-key`, `run-cursor-key` |
| browser-gateway | `ca.crt`, `tls.crt`, `tls.key` |
| executor-gateway | `ca.crt`, `tls.crt`, `tls.key`, `run-capability-keyring.json` |
| harness-pool | `ca.crt`, `tls.crt`, `tls.key`, `run-manifest.key` |
| harness-worker | `ca.crt`, `tls.crt`, `tls.key`, `run-manifest-keyring.json` |
| llmproxy | `ca.crt`, `tls.crt`, `tls.key`, `run-capability-keyring.json`, `upstream-ca.crt`, `upstream-credential` |

材料合同：

- 内部证书使用 TLS 1.3，leaf 包含配置推导出的唯一 SPIFFE URI；core、executor 和
  llmproxy 证书还要包含 `core.agentserver.internal`、`executor.agentserver.internal`、
  `llmproxy.agentserver.internal` 等对应 DNS SAN，public gateway 证书包含 public hostname。
- `run-capability.key` 和 `run-manifest.key` 是当前 active Ed25519 私钥：接受 raw 32-byte
  seed、canonical 64-byte private key 或单个未加密 PKCS#8 PEM。
- 两个 keyring 遵循 `api/schema/run-capability-keyring.schema.json` 和
  `api/schema/run-manifest-keyring.schema.json`，公钥为 32-byte Ed25519 的无 padding
  base64url；active key ID 必须出现在对应 keyring 中。
- `executor-enrollment.key`、`login-transaction-key` 和 `run-cursor-key` 是 canonical、无
  padding base64url 编码的 256-bit 随机值，且三者互不复用。
- `upstream-credential` 是完整 HTTP header value；OpenAI Authorization 示例语义为
  `Bearer <token>`，文件末尾不能带换行。所有 env 型 Secret 值也不要带尾随换行。

把每个 profile 放在独立的受限目录后创建 Secret，避免把值放入命令行或 shell history：

```bash
kubectl --context "${KUBE_CONTEXT}" --namespace "${NAMESPACE}" \
  create secret generic agentserver-core-secrets \
  --from-file=/absolute/secure-materials/core

kubectl --context "${KUBE_CONTEXT}" --namespace "${NAMESPACE}" \
  create secret generic agentserver-browser-secrets \
  --from-file=/absolute/secure-materials/browser-gateway

kubectl --context "${KUBE_CONTEXT}" --namespace "${NAMESPACE}" \
  create secret generic agentserver-executor-secrets \
  --from-file=/absolute/secure-materials/executor-gateway

kubectl --context "${KUBE_CONTEXT}" --namespace "${NAMESPACE}" \
  create secret generic agentserver-pool-secrets \
  --from-file=/absolute/secure-materials/harness-pool

kubectl --context "${KUBE_CONTEXT}" --namespace "${NAMESPACE}" \
  create secret generic agentserver-worker-secrets \
  --from-file=/absolute/secure-materials/harness-worker

kubectl --context "${KUBE_CONTEXT}" --namespace "${NAMESPACE}" \
  create secret generic agentserver-llmproxy-secrets \
  --from-file=/absolute/secure-materials/llmproxy
```

Secret 名称必须和 `production.json` 完全一致。若使用 External Secrets/CSI，应保证最终
Kubernetes Secret 的 key 集合相同，并在 Helm hook 开始前已经 Ready。

## 6. 生成并验证 Helm Chart

输出目录必须不存在，父目录必须是不可被 group/other 写入的直接目录。生成结果为只读
Chart，可被精确重试验证但不会被覆盖：

```bash
mkdir -m 0700 /absolute/helm-artifacts

go run ./cmd/agentserver-deploy chart \
  --config=/absolute/production.json \
  --output=/absolute/helm-artifacts/agentserver-v2

helm lint --strict \
  --namespace "${NAMESPACE}" \
  /absolute/helm-artifacts/agentserver-v2

helm template "${RELEASE}" \
  /absolute/helm-artifacts/agentserver-v2 \
  --namespace "${NAMESPACE}" \
  > /absolute/helm-artifacts/rendered.yaml
```

Chart 内的 `values.schema.json` 和 template guard 同时锁定配置 SHA-256 与 Namespace。
`files/production-config.json`、四段静态 manifest 和 `files/checksums.json` 用于审计。
manifest 通过 `.Files.Get` 返回，配置中的文本不会被 Helm 当作模板再次执行。

建议在安装前保存 dry-run 结果并确认没有意外删除或变更：

```bash
helm upgrade --install "${RELEASE}" \
  /absolute/helm-artifacts/agentserver-v2 \
  --kube-context "${KUBE_CONTEXT}" \
  --namespace "${NAMESPACE}" \
  --dry-run=server \
  --debug
```

## 7. 远程安装

确认 dry-run、Secret、固定 ClusterIP 和镜像拉取权限后执行：

```bash
helm upgrade --install "${RELEASE}" \
  /absolute/helm-artifacts/agentserver-v2 \
  --kube-context "${KUBE_CONTEXT}" \
  --namespace "${NAMESPACE}" \
  --atomic \
  --wait \
  --timeout 15m \
  --history-max 10
```

Helm 会等待 pre-install migration 成功，再创建常规资源、等待 rollout，最后等待
post-install bootstrap。成功的 hook Job 会自动删除；失败 Job 保留用于检查。不要使用
`--no-hooks`、`--skip-schema-validation`、`--take-ownership` 或 `--force`。

## 8. 安装后验证

```bash
helm status "${RELEASE}" \
  --kube-context "${KUBE_CONTEXT}" \
  --namespace "${NAMESPACE}"

helm get hooks "${RELEASE}" \
  --kube-context "${KUBE_CONTEXT}" \
  --namespace "${NAMESPACE}"

kubectl --context "${KUBE_CONTEXT}" --namespace "${NAMESPACE}" \
  rollout status deployment/agentserver-core --timeout=10m
kubectl --context "${KUBE_CONTEXT}" --namespace "${NAMESPACE}" \
  rollout status deployment/browser-gateway --timeout=10m
kubectl --context "${KUBE_CONTEXT}" --namespace "${NAMESPACE}" \
  rollout status deployment/executor-gateway --timeout=10m
kubectl --context "${KUBE_CONTEXT}" --namespace "${NAMESPACE}" \
  rollout status deployment/harness-pool --timeout=10m
kubectl --context "${KUBE_CONTEXT}" --namespace "${NAMESPACE}" \
  rollout status deployment/llmproxy --timeout=10m

kubectl --context "${KUBE_CONTEXT}" --namespace "${NAMESPACE}" get pods,svc,pdb,networkpolicy
kubectl --context "${KUBE_CONTEXT}" --namespace "${NAMESPACE}" get events \
  --sort-by='.lastTimestamp'
```

从本机验证 browser-gateway TLS/health，可先 port-forward，再让 curl 保持 public hostname 的
SNI：

```bash
kubectl --context "${KUBE_CONTEXT}" --namespace "${NAMESPACE}" \
  port-forward service/browser-gateway 18443:8443

curl --fail --show-error \
  --resolve '<browser-hostname>:18443:127.0.0.1' \
  --cacert /absolute/internal-ca.crt \
  'https://<browser-hostname>:18443/healthz'
```

随后用真实浏览器完成 OIDC Code + PKCE 登录，并通过 AG-UI `/agui` 创建一个 run。要完成
shell/read_file 端到端链，还必须在用户机器部署与本 Chart 同一
`packaging/stockruntime/runtime-manifest.json` 的 agentx、完成 executor enrollment 并保持
WSS online；server release 本身不会在集群内伪造 executor。

## 9. 升级、回滚、密钥轮换和卸载

升级流程是“构建并推送新 digest → 修改生产配置 → 生成全新 Chart 目录 → lint/server
dry-run → `helm upgrade`”。pre-upgrade migration 在新 Pod rollout 前执行。数据库迁移只向前，
因此所有 migration 必须保持与上一版应用兼容。

`helm rollback` 只回滚 Kubernetes release，不回滚数据库。只有确认旧应用能读取当前 schema
时才执行：

```bash
helm history "${RELEASE}" --kube-context "${KUBE_CONTEXT}" --namespace "${NAMESPACE}"
helm rollback "${RELEASE}" <revision> \
  --kube-context "${KUBE_CONTEXT}" \
  --namespace "${NAMESPACE}" \
  --wait --timeout 15m
```

Secret 更新后必须重启对应 Deployment，因为 init container 会把 projected Secret 固化到
Pod 内私有 tmpfs。签名 key 轮换顺序为：先向所有 verifier keyring 加入新公钥并 rollout，
再切换 signer 的私钥/key ID 并 rollout，观察稳定后才从 keyring 删除旧公钥。TLS、OIDC
secret 和数据库 credential 也应按“新旧 overlap → rollout → 撤旧”处理。

卸载只删除 release 管理的工作负载和网络资源：

```bash
helm uninstall "${RELEASE}" \
  --kube-context "${KUBE_CONTEXT}" \
  --namespace "${NAMESPACE}" \
  --wait --timeout 10m
```

Namespace、六组 Secret、PostgreSQL 数据、S3 对象和 KMS key 会保留。是否删除这些外部
资源必须作为单独的、经确认的数据生命周期操作处理。

## 10. 常见故障定位

- migration hook 失败：用 `kubectl get jobs` 找保留的 Job，检查日志、`database-url`、
  NetworkPolicy 和数据库 DDL 权限；不要跳过 hook 继续 rollout。
- Pod 卡在 init：检查 Secret 是否缺 key、证书/私钥是否匹配、SPIFFE URI、keyring JSON 和
  私钥权限；materialize init 会 fail closed。
- Pod Pending：确认 arm64 节点容量、tmpfs limit、ResourceQuota 和 PDB。
- Service 创建失败：固定 ClusterIP 已占用或不属于 Service CIDR；修改配置并重新生成
  Chart，不要在线 patch。
- ImagePullBackOff：节点无法读取 registry 或配置使用了错误平台/错误 digest。
- 外部请求 timeout：把实际目的 IPv4/CIDR 加到正确的 egress 分组并重新生成；同时检查
  CoreDNS Service IP 和 selector。
- Helm Namespace/config guard 失败：使用生成 Chart 对应的 Namespace 和原始
  `deploymentConfigSHA256`；不要关闭 schema validation。
