# Production deployment artifacts

- `config.example.json`：closed-world 生产环境输入示例。
- `build-images.sh`：构建并逐文件验证 service/harness 两个 linux/arm64 镜像。
- `service.Containerfile`、`harness.Containerfile`：digest-pinned base、scratch runtime 镜像。
- `agentserver-deploy chart`：从生产配置生成环境锁定的 Helm Chart。

完整远程安装、Secret key 集合、升级和回滚说明见
[`../../docs/PRODUCTION_DEPLOYMENT.md`](../../docs/PRODUCTION_DEPLOYMENT.md)。
