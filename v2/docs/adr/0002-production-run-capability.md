# ADR 0002：生产 run capability 使用 audience 分离的 Ed25519 短期令牌

- 状态：Accepted
- 日期：2026-08-02

## 背景

harness-pool 在持有精确 session lease 与 run-attempt lease 后，需要把本次 attempt 的有限运行权限交给 harness-worker。worker 调 executor MCP，stock app-server 只调 llmproxy；两条链路不能共享权限，也不能获得 Core 的数据库、对象存储或上游模型凭证。

本地联调使用 `asv2dev1` HMAC token。共享 HMAC 使每个持 key 的进程都能签发任意 token，无法满足生产环境中“只有 Core 能签发、gateway/llmproxy 只能验证”的边界。纯离线签名也不能反映 lease fence、run generation、成员关系或策略撤销，因此签名验证不能成为最终授权。

## 决策

### Token 与签名

生产 token 使用四段紧凑格式：

```text
asv2cap1.<base64url(key_id)>.<base64url(canonical_claims)>.<base64url(signature)>
```

claims 必须是 closed-world RFC 8785 canonical JSON。签名算法固定为 Ed25519，输入固定为：

```text
agentserver-v2/production-run-capability/ed25519-v1\0
|| uint16be(len(key_id)) || key_id || canonical_claims
```

prefix、签名域与 keyring 中的 `ed25519-v1` 共同固定协议版本；生产 verifier 不接受 `asv2dev1`。token 只携带 key ID，不携带公钥或算法选择。key ID 必须精确命中已配置公钥，未知 ID 不会遍历或 fallback 到其他 key。

### 公共 authority

两类 token 都必须绑定：

- issuer 与唯一 capability ID（jti）；
- workspace、session、run、run-attempt 和 attempt generation；
- 发起用户 actor 与当前 pool holder；
- issued-at、强制 run deadline 与 capability expiry。

`aud=executor-mcp` 只增加 executor ID、冻结 tool catalog digest、预期 run/run-attempt version 和最大 approval TTL。具体 environment 仍由请求参数选择，并由 executor-gateway 与 Core 对 workspace/executor/environment 归属和当前策略逐次复核。

`aud=llmproxy` 只增加精确 model/provider route。它不得携带 executor、catalog、version 或 approval authority。executor token 同样不得携带 model/provider authority。

capability expiry 可以覆盖强制 run deadline 后很短的 transport/收尾 grace，但不得授权 deadline 后的新模型或工具操作。Core 的在线授权以数据库时间、生产 policy 和 live attempt 状态为准。

### 签发与验证

生产私钥只装配到 agentserver-core。harness-pool 在持有双 lease 后以 workload identity 请求 Core 为同一 attempt 分别签发 executor-MCP 和 llmproxy token，再通过本 attempt 的 inherited bootstrap pipe交给 worker。私钥不得进入 pool、worker、executor-gateway、llmproxy、agentx 或 stock Codex。

executor-gateway 与 llmproxy 先本地完成格式、canonical JSON、Ed25519、issuer、audience 和时间窗口验证；随后每个实际请求都必须带完整 claims 调 Core live-authorize。Core 至少重新检查：

- workspace/session/run/attempt/holder/generation 一致；
- session lease 与 attempt lease 仍 live；
- run 与 attempt 的预期版本和状态允许该动作；
- actor 当前仍是 workspace 成员且 RBAC 允许；
- executor、environment、冻结 catalog、model/provider 与当前生产 policy 精确匹配。

本地验签只降低无效请求进入 Core 的成本，不能在 Core 不可达时 fallback 为离线授权。取消、lease fence、成员移除、executor 下线或 policy 变化必须让后续请求 fail closed。

### 私钥与轮换

Core 从绝对、clean、解析为 regular file 且 group/other 无权限的 Secret 文件加载 active Ed25519 key。支持 raw 32-byte seed、canonical 64-byte private key或单个未加密 PKCS#8 `PRIVATE KEY` PEM；不支持把私钥、静态 bearer 或 inline base64 secret放进普通配置。

verifier 使用 closed-world `api/schema/run-capability-keyring.schema.json`，最多显式配置 32 枚 Ed25519 公钥。轮换顺序为：

1. 先把新公钥加入所有 verifier，与旧 key overlap；
2. Core 切换 active signing key ID；
3. 等待旧 token 的最大 expiry 与时钟/发布 grace 全部结束；
4. 再移除旧公钥和旧私钥。

发布期间任何组件看不到 token 指定 key ID 都必须拒绝请求，不能用另一枚 key 尝试验签。

## 结果与限制

- 开发 HMAC 与生产 Ed25519 在 prefix、签名域、issuer和装配入口上完全隔离。
- executor 与 model authority 不能跨 audience 复用。
- 公钥 overlap 支持无停机轮换；Core 仍是唯一签发者。
- token 被窃取后的有效范围受 attempt、holder、generation、route、deadline 与在线授权共同限制，但 bearer token 本身仍必须只经受限内存/pipe/header传递并避免日志和 checkpoint。
- codec、key loader、keyring和schema合同已经固定；后续 Core authority 切片又完成了签发、executor-MCP/llmproxy分路live-authorize和production `agentserver-core serve`装配。pool/gateway/llmproxy消费者的生产装配及真实部署门禁仍必须在后续 Phase 5 切片完成，不能因为Core端点已经存在就开放其余组件的production serve。
