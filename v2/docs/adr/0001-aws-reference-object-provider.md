# ADR 0001：AWS SDK v2 作为加密对象协议的参考 provider

- 状态：Accepted
- 日期：2026-08-02

## 背景

agentserver v2 已经在应用层固定了供应商无关的加密对象格式。Core 提交的明文 pointer 是权威；对象后端只提供 immutable `PutIfAbsent/Open`，KMS 只提供每对象 data key 的生成与解封。对象格式不能依赖 S3 metadata、ETag、presigned URL、SSE header 或某个云厂商的密文结构。

生产装配仍需要一个真实 transport 实现，同时必须避免在通用协议中静默加入某个供应商的重试、幂等或 key identity 语义。

## 决策

参考实现使用锁定版本的 AWS SDK for Go v2，并把它限制在 `internal/objectstore/awsprovider` 两个窄 adapter 后面：

- S3 adapter 可连接 AWS S3 或通过独立 HTTPS endpoint 连接兼容 S3 API 的服务；
- KMS adapter 使用 AWS KMS API，endpoint 与 region 独立于 S3；
- 应用层 ciphertext/header、object key、明文 pointer 和 DB 提交协议保持不变；替换其他云的 KMS 或对象 transport 不改变已固定的对象格式。

### S3 写边界

每个对象使用单次 `PutObject`，显式提交：

- `If-None-Match: *`；
- 精确 ciphertext `Content-Length`；
- 应用 ciphertext body。

adapter把该路径限制在S3 single-part PUT的5 GiB上限内；当前prompt与checkpoint协议上限远低于它。未来若增加更大的object kind，必须先为multipart完成时的conditional publish、分片残留和歧义恢复另行设计协议，不能无界复用当前实现。

S3 client 固定 `aws.NopRetryer`。SDK 不能在调用方看不见的情况下重放一次可能已经提交的 immutable 写；应用层在同一明文 pointer 下执行 exact retry，并通过重新打开和完整解密已有对象来收敛结果。

只有同时带有 HTTP 412 和 `PreconditionFailed` code 的响应映射为 `Created=false`。`409 ConditionalRequestConflict`、连接中断、timeout、5xx 及其他响应全部保留为歧义错误，不能伪装成“对象已存在”或用户幂等冲突。成功响应只有在 SDK 已消费声明的全部 body 字节后才映射为 `Created=true`。

请求 checksum calculation 与响应 checksum validation 固定为 SDK 的 `WhenRequired` 模式，避免 ambient SDK 配置改变 wire profile；应用协议仍以逐块 AES-GCM 和明文 SHA-256 为端到端完整性权威。

### S3 读边界

`GetObject` 的 `NoSuchKey` 映射为协议的 `ErrBlobNotFound`；`NoSuchBucket`、泛化 404 和其他错误不做该映射。adapter 必须取得非空 body 和正数 `Content-Length`，后者成为 `objectstore.Blob.Size`，再由应用协议与 header 推导的 ciphertext size 做精确比对。任何 malformed/error output 中已经返回的 body 都立即关闭。

### KMS 边界

新对象调用 `GenerateDataKey(KeySpec=AES_256)`。对象头保存 KMS 返回的 KeyId 和 wrapped key；不保存配置时使用的 alias。打开对象调用 `Decrypt` 时必须回传对象头中的 KeyId，而不是当前 write key，从而允许新旧 key 并存。

KMS encryption context 固定两项非秘密值：

- `agentserver-protocol=encrypted-object-v1`；
- `agentserver-authority-sha256=<完整明文 authority 的 SHA-256>`。

生成和解封必须使用完全相同的 context。raw workspace/object authority 不进入可能被 CloudTrail 等系统明文记录的 context。SDK 返回的 plaintext 必须恰好为 32 bytes；adapter 复制后立即清零 SDK buffer，应用层完成 AEAD 初始化后继续清零自己的 data-key buffer。

### 配置与身份

provider 配置只包含 bucket、两个 region、两个可选 HTTPS endpoint、path-style 选择和 KMS key ID。没有 access key、secret key 或 session token 字段。凭证只通过 AWS SDK workload/default credential chain 获取；生产部署应使用 workload identity，不能把静态云密钥写入 ConfigMap、命令行或 v2 自定义配置。

两个持有对象权限的组件复用同一组显式环境变量；prefix 必须配置，不能用代码默认值静默分裂已有对象namespace：

- `AGENTSERVER_V2_OBJECT_PREFIX`；
- `AGENTSERVER_V2_S3_BUCKET`、`AGENTSERVER_V2_S3_REGION`；
- 可选`AGENTSERVER_V2_S3_ENDPOINT`与`AGENTSERVER_V2_S3_USE_PATH_STYLE=true|false`；
- `AGENTSERVER_V2_KMS_REGION`、`AGENTSERVER_V2_KMS_KEY_ID`；
- 可选`AGENTSERVER_V2_KMS_ENDPOINT`。

`agentserver-core serve`只装配这套加密S3/KMS store；本地plaintext目录只在显式`agentserver-core serve --insecure-dev`下可用。harness-pool已经具备同一production store factory和`EncryptedRunObjectStore`装配路径；production capability wire/keyring合同见[`ADR 0002`](0002-production-run-capability.md)，但在Core issuance/live-authorize和完整consumer装配完成前，命令入口继续只接受`serve --insecure-dev`，不能因为对象后端或codec已完成就回退使用开发HMAC并伪装成production。

显式 endpoint 是应用 authority。adapter 清除 SDK 从 ambient endpoint 环境或 shared config 解析出的 endpoint，只使用这里的配置；空 endpoint 使用 SDK 的标准 AWS endpoint resolution。Core 与 harness-pool 的 workload identity可以调用 S3/KMS，worker 和 stock app-server 不获得这些凭证。

## Key rotation 与 retention

配置的 KMS key 只决定新对象的 `GenerateDataKey`。旧对象继续使用其 header 中保存的 KeyId 解封，因此 rotation 必须在旧对象 retention 窗口内保留对应 `kms:Decrypt` 权限。

对象 header 本身进入每块 AEAD 的 AAD，且后端 key immutable，不能只原地替换 wrapped key。若安全事件要求在 retention 到期前迁移旧对象，必须完整解密、以新 object ID 重新密封，并通过 Core 的 pointer/CAS 流程迁移引用；不能覆盖原 key 或绕过 DB authority。未被引用的旧对象再由 retention/orphan cleanup 删除。

## 结果与限制

- AWS SDK 是参考 transport，不是对象格式的一部分。
- S3-compatible 产品必须通过 conditional put、错误分类、body consumption、content length 和断线歧义集成门禁后才能声明兼容。
- 当前 adapter单测、race、锁定Linux arm64镜像门禁及命令级装配测试不证明真实bucket/KMS IAM、endpoint TLS、rotation、orphan cleanup或provider故障注入已经完成；这些仍属于Phase 5部署门禁。
- 其他云 provider 必须实现同一窄接口及等价门禁，并另写 provider ADR；不得为了迁就 provider 修改已经提交的明文 pointer 或对象格式。
