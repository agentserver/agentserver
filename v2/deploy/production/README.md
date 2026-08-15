# Production deployment artifacts

- `config.example.json`：SG 平台的 closed-world v6 渲染/Schema fixture。SG 是 Kubernetes 部署位置，
  不再等同于 TAE managed sandbox 地域；`sandboxRegions`、`sandboxProfiles` 和
  `proxyProfiles` 独立描述可安装的 TAE 地域目录。
- `build-images.sh`：生产发布只使用 `linux-amd64` 和 Apple Container `1.2.2`，并逐文件验证
  service/harness/managed-sandbox 镜像。service/harness 固定为两层；managed-sandbox 固定为四层：
  digest/size/diff ID/history 全部锁定的 AgentServer 自有单层 Debian base、closed-world managed
  rootfs、固定 CA，以及 `WORKDIR /workspace` 的 canonical 空层。
- `service.Containerfile`、`harness.Containerfile`：digest-pinned build base 与 scratch runtime；
  `managed-sandbox.Containerfile` 基于受审计的 TAE sandbox rootfs。TAE Terminal Sandbox revision
  固定镜像和 `run_cmd=/usr/local/bin/agentserver-tae-runtime`；CreateSession 只引用固定 revision，
  不接受 image/command 覆盖。审计边界见
  [TAE_SANDBOX_RUNTIME.md](../../docs/TAE_SANDBOX_RUNTIME.md)。
- `agentserver-deploy prepare-policy-bootstrap`：一次性把所有已安装地域切到 fail-closed bootstrap，
  同时清空每个 profile 的 policy、network、runtime 和 pack evidence。
- `agentserver-deploy activate-managed-sandbox-profiles`：读取一个全地域 evidence manifest；只有每个
  已安装地域都有匹配的 canonical passing report、policy revision 和不可变 evidence 引用时才原子晋级。
- `agentserver-deploy chart`：从生产配置生成环境锁定 Helm Chart。

## Managed sandbox region catalog

workspace owner 可在设置中选择以下已安装地域；选择只影响新 Run。Core 在创建 Run 时固化
`settingVersion + region + profileId + bindingSha256 + environmentId`，Harness 与 Executor 只能按这份
不可变 binding 路由。普通对话不会创建 TAE sandbox；首次实际调用 Executor 工具时才 lazy acquire。
`sandboxRegions.defaultRegion` 固定为 `i18n-tt`，catalog 必须安装该 profile；这是数据库迁移与新 workspace
setting 的统一初值，owner 之后仍可切换到其他已安装地域。

| TAE region | Route |
| --- | --- |
| `cn` | `merlin-hl-1` |
| `boe` | direct；必须配置明确的 IPv4 与 IPv6 CIDR allowlist |
| `i18n-bd` | `merlin-useast14a-1` |
| `i18n-tt` | `merlin-i18nbd-syd2a` |

Merlin 名称只是受审计的逻辑 profile。完整 `socks5h://` URL、namespace、exact Pod selector 和 port
全部来自 `proxyProfiles`，代码不会从名称推导地址，也不提供跨地域 fallback。BOE 不配置 proxy；
其他地域不得偷用 direct CIDR。标准 `HTTP_PROXY` / `HTTPS_PROXY` / `ALL_PROXY` 继续 fail closed。

每个 `sandboxProfiles[]` 都有独立的：

- TAE Sandbox ID/revision、official SDK control/data authority、ByteCloud site/JWT endpoint；
- environment ID、runtime/pack lock；
- sandbox-gateway Service/Deployment/PDB/NetworkPolicy、TLS server name 与 Secret；
- region-specific policy/network evidence。

Core、Harness、Executor 接收的是同一份经过校验的 catalog 投影；每个地域另有独立 managed-environment
bootstrap Job。bootstrap Helm values 默认关闭探针：

```yaml
taeNetworkProbe:
  enabled: false
  policyRevisions:
    cn: ""
    boe: ""
    i18n-bd: ""
    i18n-tt: ""
```

启用后，每个已安装地域分别生成 ConfigMap、Job 与 NetworkPolicy。proxy profile 只允许 DNS 加它自己的
Merlin Pod；BOE 只允许 DNS 加配置中的双栈 direct CIDR。报告覆盖强制 JWT 刷新、control/data lifecycle、
pinned CLI/skill 摘要读取和资源清理，且不输出凭据。

激活 manifest 示例：

```json
{
  "schemaVersion": 1,
  "profiles": [
    {
      "region": "cn",
      "policyRevision": "<published-revision>",
      "policyEvidenceRef": "artifact://policy/cn",
      "networkReportPath": "/absolute/cn-report.json",
      "networkEvidenceRef": "artifact://network/cn/report.json"
    }
  ]
}
```

manifest 必须恰好覆盖配置中安装的全部地域。任何缺失、重复、跨地域报告、配置 SHA/authority 不匹配或
失败检查都会拒绝整个 activation。

`config.example.json` 只安装一个 i18n-tt fixture，便于保持已知 evidence digest；它不是四地域生产值
清单。真实部署必须提供三个 Merlin 的完整连接信息、四套 TAE Sandbox/revision 与 JWT endpoint、
四个 Gateway ClusterIP/TLS/Secret，以及 BOE 双栈 CIDR。BOE 基础设施 AK/SK 的签名 site 固定为官方
SDK 别名 `cn`；JWT endpoint 仍禁止猜测。

对象存储、内部 CA、workload 证书、签名/对称密钥、Kubernetes Secret 和 CloudNativePG 仍由
`../k8s-byted` 的 Pulumi 模块管理。Helm release 为 non-atomic，失败现场会保留供排障。完整安装、
探针、激活、升级和回滚步骤见
[PRODUCTION_DEPLOYMENT.md](../../docs/PRODUCTION_DEPLOYMENT.md)。
