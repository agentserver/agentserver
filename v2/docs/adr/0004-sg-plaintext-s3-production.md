# ADR 0004：SG 生产使用显式 credential 的明文 S3 profile

- 状态：Accepted
- 日期：2026-08-02
- 取代：[ADR 0001](0001-aws-reference-object-provider.md) 的当前生产装配部分

## 背景

当前目标对象服务只提供 S3-compatible data API，不能提供与既有 envelope protocol 对应的
KMS、STS 或 workload identity。部署范围也已收敛为单一 SG Kubernetes 集群。为了尽快形成
可验证的端到端版本，用户明确选择把 prompt/checkpoint 明文保存到该 S3-compatible 服务。

这不是把 S3 当成完整 session 数据库。PostgreSQL 仍保存 workspace/session/run/lease/event
等权威状态及对象 pointer；S3 只保存 pointer 指向的 user prompt 和 Codex checkpoint 字节。

## 决策

SG production profile 固定为 `s3-plaintext-v1`：

- S3 收到与对象 pointer 中 size/SHA-256 描述完全一致的原始 prompt/checkpoint 字节；
- 应用保留 immutable key、`PutIfAbsent`、exact retry、size/SHA-256 校验和跨 workspace/kind
  scope；
- 每次 Open 都验证后端 `Content-Length`，并在 EOF/Close 前完成 SHA-256 与尾随字节检查；
- 写入只接受 single-part conditional `PutObject`：`If-None-Match: *`；
- 只有明确的 `412 PreconditionFailed` 表示对象已经存在；409、timeout、断线和 5xx 保持为
  歧义错误，不能伪装为幂等成功；
- S3 SDK 关闭自动重试，重试语义留在应用的同 pointer exact retry；
- credential 来自 `agentserver-object-store-secrets` 的 `access-key-id` 和
  `secret-access-key`，不读取 ambient profile、metadata、STS 或 WebIdentity；
- Chart 不渲染 KMS、IAM role、ServiceAccount token projection 或相关环境变量。

配置固定包含：

```text
AGENTSERVER_V2_OBJECT_PREFIX
AGENTSERVER_V2_S3_BUCKET
AGENTSERVER_V2_S3_REGION
AGENTSERVER_V2_S3_ENDPOINT            # 可选 HTTPS origin
AGENTSERVER_V2_S3_USE_PATH_STYLE
AGENTSERVER_V2_S3_ACCESS_KEY_ID       # Secret
AGENTSERVER_V2_S3_SECRET_ACCESS_KEY   # Secret
```

`internal/objectstore.PlainStore` 与加密 Store 共同实现同一个 authority-preserving `Protocol`；
Core/pool adapter 不根据实现类型改变数据库 pointer 或 run manifest。

## 安全边界

对象服务、bucket 管理员、备份系统和 credential 持有方现在都位于明文数据可信边界内。必须用
bucket ACL、独立最小权限 credential、访问审计、备份保护、retention 和删除流程弥补没有应用层
加密的风险。日志、错误、Chart、ConfigMap 和 Pulumi 普通输出不得包含 S3 credential 或对象
正文。

Pulumi 可以生成 AgentServer 自己拥有的 CA、workload TLS、签名和对称密钥，但不能随机生成
一个对象服务从未授权的 access key。S3 credential 必须由对应服务创建，再以 secret Output
接入 Pulumi 管理的 Kubernetes Secret。

## 结果与限制

- 当前 SG 部署不需要 KMS、STS、AWS role 或跨 Pod credential recovery；
- 明文对象不能被描述为 envelope-encrypted，旧文档/日志中的 `encrypted store` 名称只是历史
  类型名，不改变当前 wire bytes；
- S3-compatible 服务在上线前必须实测 conditional PUT、412 分类、Content-Length、断线歧义和
  credential 权限；
- 将来恢复加密必须新建明确版本/profile和迁移流程，不能原地把同一 object key 的明文字节
  替换为密文；
- bucket 中已有对象的加密迁移需要新 object ID、重新写入和 Core pointer CAS，不能覆盖 immutable
  key。
