# Production deployment artifacts

- `config.example.json`：SG-only closed-world 生产输入；已写入核验过的 amd64 镜像 digest、
  SG ClusterIP/CoreDNS、bootstrap UUID、runtime artifact、owner、平台 OIDC、内置 Hydra、S3 与
  当前 TOS 内网 egress 地址；这些环境值变化时必须重新生成和发布 Chart。
- `build-images.sh`：生产发布只使用 `linux-amd64`，并逐文件验证 service/harness 镜像。
- `service.Containerfile`、`harness.Containerfile`：digest-pinned base、scratch runtime 镜像。
- `agentserver-deploy chart`：从生产配置生成环境锁定的 Helm Chart。

当前 SG profile 由 Istio 终止公网 TLS，prompt/checkpoint 以明文写入 S3-compatible bucket；
TOS 使用 `https://tos-s3-sg.byted.org` 作为 SDK base endpoint，并以
`agentserver-sg.tos-s3-sg.byted.org` virtual-host 访问 bucket（`s3UsePathStyle=false`）；core 和
harness-pool 的 NetworkPolicy 同时固定该 endpoint 当前解析出的 IPv4/IPv6 地址。
内部 CA、workload 证书、签名/对称密钥和 Kubernetes Secret 由 `../k8s-byted` 的 Pulumi 模块
生成；同一模块还会创建 3 实例 CloudNativePG Cluster、随机 owner 密码和应用 DSN，不需要外部
`databaseUrl`。构建脚本保留其他平台参数只用于历史 conformance/开发，不代表该平台可部署到生产。
运行时Secret以只读单文件`subPath`直接挂载，不经过materialize init；Helm release为non-atomic，
部署失败时保留Pod和日志供排障。

完整远程安装、Secret key 集合、升级和回滚说明见
[`../../docs/PRODUCTION_DEPLOYMENT.md`](../../docs/PRODUCTION_DEPLOYMENT.md)。
