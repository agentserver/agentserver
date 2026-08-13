# Production deployment artifacts

- `config.example.json`：SG-only closed-world 渲染/Schema fixture；包含 base service/harness/Hydra 和 managed
  sandbox 镜像 digest、SG ClusterIP/CoreDNS、bootstrap UUID、runtime artifact、owner、平台 OIDC、
  内置 Hydra、TAE policy/proxy binding、S3 与当前 TOS egress 地址；这些环境值变化时必须重新生成和
  发布 Chart。
- `build-images.sh`：生产发布只使用 `linux-amd64` 和 Apple Container `1.2.2`，并逐文件验证
  service/harness/managed-sandbox 镜像。service/harness 固定为两层；managed-sandbox 固定为四层：
  digest/size/diff ID/history 全部锁定的 agentserver 自有单层 Debian base、closed-world managed rootfs、固定 CA，
  以及 `1.2.2` 为 `WORKDIR /workspace` 生成的 canonical 空层。base 作为 opaque trusted layer，不会
  被误拿去和 managed 文件清单比较；后面三层仍逐 entry 校验 owner/mode/size/SHA-256。
- `service.Containerfile`、`harness.Containerfile`：digest-pinned build base 与 scratch runtime；
  `managed-sandbox.Containerfile` 则基于
  `aliyun-sin-hub.byted.org/agentserver/tae-sandbox@sha256:e4255f02c1feceb168848fc6b7ea934cdc3f944ebc8dda51d2b77d00fbf28f6f`
  的单层 Debian Trixie rootfs；它不是官方 `terminal_faas`。managed overlay 增加自有静态 FaaS
  keeper、pinned `lark-cli`、skill、manifest 和 CA。TAE Terminal Sandbox revision 在管理面固定该镜像
  和 `run_cmd=/usr/local/bin/agentserver-tae-runtime`；创建 Session 时只发送固定 `revision_id`，不发送
  `image` 或 `command`。同一命令也是 OCI fallback `CMD`，平台继续自动注入 SandboxD。官方镜像审计和边界见
  [`../../docs/TAE_SANDBOX_RUNTIME.md`](../../docs/TAE_SANDBOX_RUNTIME.md)。service image 内含
  provider-linked `sandbox-gateway` 和 `egress-authorizer` binary。
- `agentserver-deploy prepare-policy-bootstrap`：生成没有 TAE/sandbox runtime authority 的预审批 Chart
  配置；webhook profile 会保留 deny-only Webhook，direct profile 不渲染任何 Webhook 资源。
- `agentserver-deploy retarget-direct-terminal-sandbox`：原子替换 Sandbox/revision/environment/image，并将
  profile 固定为 TAE 系统 `*.feishu.cn` 白名单、无 webhook；用于当前 `process_env` 发布。
- bootstrap Chart 内含默认关闭、仅能由 closed values schema 启用的 SG 探针资源；审批后由 Pulumi
  注入实际 policy revision，并管理一次性 ConfigMap、ServiceAccount、DNS+syd2a-only NetworkPolicy
  和 Job。报告覆盖强制 JWT 刷新、control/data lifecycle、`printf terminal-ok`、`lark-cli --version`、
  42MB pinned CLI/skill 摘要读取和资源清理，且不输出凭据。
- `agentserver-deploy activate-managed-executor`：只接受 bootstrap 输入和 canonical passing probe 报告，
  核对实际 policy revision/配置/镜像/检查集合后自行计算报告 SHA，并将 policy 审批与 SG
  JWT/control/data evidence 原子绑定为 active 配置。
- `agentserver-deploy chart`：从生产配置生成环境锁定的 Helm Chart。

当前 SG profile 由 Istio 终止公网 TLS，prompt/checkpoint 以明文写入 S3-compatible bucket；managed
executor 部署 `sandbox-gateway`（TAE SDK boundary）。当前 direct profile 只支持 workspace
`process_env`，使用 TAE 系统预置的 `*.feishu.cn` 白名单，不部署 `egress-authorizer`、Service、Route、
BackendTLSPolicy 或对应 NetworkPolicy，也不配置 TAE webhook。TAE policy binding 必须从系统 policy
只读回查并核验。未来 `webhook_swap` 必须使用另一个 webhook-enabled Sandbox profile。
SG Terminal Sandbox PSM 固定为 `bytedance.sandbox.agentserver`；配置渲染器会拒绝其他 PSM。
生产配置还固定并交叉校验 `sandboxId` 与 `sandboxRevisionId`；SDK 被限制在该 Sandbox ID 下，Session
创建请求只能引用配置中的 revision，不能把工作负载提供的 image/command 变成第二条发布路径。
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
