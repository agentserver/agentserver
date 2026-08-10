# Production deployment artifacts

- `config.example.json`：SG-only closed-world 渲染/Schema fixture；包含 base service/harness/Hydra 和 managed
  sandbox 镜像 digest、SG ClusterIP/CoreDNS、bootstrap UUID、runtime artifact、owner、平台 OIDC、
  内置 Hydra、TAE policy/proxy binding、S3 与当前 TOS egress 地址；这些环境值变化时必须重新生成和
  发布 Chart。
- `build-images.sh`：生产发布只使用 `linux-amd64` 和 Apple Container `1.2.2`，并逐文件验证
  service/harness/managed-sandbox 镜像。service/harness 固定为两层；managed-sandbox 额外只接受
  `1.2.2` 为 `WORKDIR /workspace` 生成的 canonical 空层，所有非空文件仍属于相同 closed-world 清单。
- `service.Containerfile`、`harness.Containerfile`、`managed-sandbox.Containerfile`：digest-pinned
  base、scratch runtime 镜像；service image 内含 provider-linked `sandbox-gateway` 和
  `egress-authorizer` binary，managed-sandbox image 只含 pinned `lark-cli`/skill runtime。
- `agentserver-deploy prepare-policy-bootstrap`：生成只暴露生产 TLS deny-only Webhook、没有 TAE/sandbox
  authority 的预审批 Chart 配置。
- bootstrap Chart 内含默认关闭、仅能由 closed values schema 启用的 SG 探针资源；审批后由 Pulumi
  注入实际 policy revision，并管理一次性 ConfigMap、ServiceAccount、DNS+syd2a-only NetworkPolicy
  和 Job。报告覆盖强制 JWT 刷新、control/data lifecycle、42MB pinned CLI 读取和资源清理，且不输出凭据。
- `agentserver-deploy activate-managed-executor`：只接受 bootstrap 输入和 canonical passing probe 报告，
  核对实际 policy revision/配置/镜像/检查集合后自行计算报告 SHA，并将 policy 审批与 SG
  JWT/control/data evidence 原子绑定为 active 配置。
- `agentserver-deploy chart`：从生产配置生成环境锁定的 Helm Chart。

当前 SG profile 由 Istio 终止公网 TLS，prompt/checkpoint 以明文写入 S3-compatible bucket；managed
executor 额外部署 `sandbox-gateway`（TAE SDK boundary）和 `egress-authorizer`（TAE Policy Webhook），
两者只接受生产 mTLS/capability，TAE 网络策略和 policy binding 必须在控制面先发布并核验。
SG Terminal Sandbox PSM 固定为 `bytedance.sandbox.agentserver`；配置渲染器会拒绝其他 PSM。
TAE Policy Webhook 固定为 `https://egress-authorizer-sg.byted.bps.dev/v1/policy`，由现有 Istio
HTTPS listener 暴露，后端 TLS 由 `agentserver-egress-backend-ca` ConfigMap 验证。
`sandbox-gateway` 独占 `agentserver-sandbox-secrets` 中的 `bytecloud-access-key-id` /
`bytecloud-secret-access-key`，以 `i18n-tt` 应用身份换取短期 JWT；execution gateway、harness 和 TAE
sandbox 均不接触 AK/SK。JWT exchange origin 固定为 `https://cloud-i18n-sg.bytedance.net`；JWT exchange、
TAE control-plane 与 sandboxd data-plane 全部只通过
`socks5h://ssh-egress-merlin-i18nbd-syd2a-83092-headless.ssh-egress.svc.cluster.local:1080` 访问，分别由
`AGENTSERVER_V2_TAE_BYTECLOUD_JWT_ENDPOINT` 和 `AGENTSERVER_V2_TAE_PROXY_URL` 锁定。
`sandboxExternalEgress` 为空；跨 namespace Pod selector 只精确放行 syd2a TCP 1080。
不能依赖 SDK 跨地域 fallback，也不能设置全局 proxy 环境变量。
对象存储使用 SG 自建 SeaweedFS 的 `https://s3-sg.byted.cs.ac.cn` 作为 SDK base endpoint，
以 region `sg-devbox-1` 和 path-style 访问 `agentserver` bucket（`s3UsePathStyle=true`）；core 和
harness-pool 的 NetworkPolicy 同时固定该 endpoint 当前解析出的 IPv4/IPv6 地址。
内部 CA、workload 证书、签名/对称密钥、BackendTLSPolicy CA ConfigMap 和 Kubernetes Secret 由 `../k8s-byted` 的 Pulumi 模块
生成；同一模块还会创建 3 实例 CloudNativePG Cluster、随机 owner 密码和应用 DSN，不需要外部
`databaseUrl`。构建脚本保留其他平台参数只用于历史 conformance/开发，不代表该平台可部署到生产。
运行时Secret以只读单文件`subPath`直接挂载，不经过materialize init；Helm release为non-atomic，
部署失败时保留Pod和日志供排障。

active Chart 只接受带 `networkEvidence` canonical digest 的受保护 SG 配置；预审批 Chart 只能运行
无条件 deny 的 policy bootstrap。代码、provider module 和本地渲染门禁已经接入；真实 SG ByteCloud 应用 JWT/TAE/Lark 网络、MTU/IPv4/IPv6、凭据抓包和
zero-secret 证据尚未在本文档环境执行，因此不能仅凭本地 Chart 绿灯宣称已上线。完整远程安装、Secret
key 集合、升级和回滚说明见
[`../../docs/PRODUCTION_DEPLOYMENT.md`](../../docs/PRODUCTION_DEPLOYMENT.md)。
