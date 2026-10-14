# Managed sandbox activity lease 修复（2026-10-14）

## 证据与诊断修正

2026-10-10 的会话 `291a8085-2fcd-44fb-aae4-32f18413c620` 出现了两段不同故障（以下时间均为 UTC）：

| 时间 | 已确认事实 | 对应问题 |
|---|---|---|
| 07:50:54 | 仓库准备完成；同一 attempt 有两个 MCP transport session | 两个 transport 原本各自创建 keepalive，却操作 Core 中同一条 activity |
| 07:51:25–07:51:59 | scoped environment 查询为空，shell 没有创建 execution | `PrepareRepository` 两次以 30 秒 TTL 续租，覆盖了原来的 5 分钟；原 keepalive 间隔为 100 秒 |
| 07:53:11 | 同一 sandbox/generation 又能执行 | 后续 keepalive 重新延长了 activity；不是环境被切到另一 executor |
| 07:56:14 | execution `5cb1e204-7e7a-4fe8-95c4-31090d30618e` 终态 unknown，紧接着 sandbox 被删除 | unknown 后的既有 fencing 路径；不是 activity 自然到期：数据库记录的到期时间为 08:00:54 |
| 07:56:23–07:56:31 | 后续 shell 又查不到环境 | gateway 仍缓存已删除目标的 lease，直到下一次心跳才发现失效 |
| 07:57:32 / 07:57:34 | 两条 keepalive 均报续租失败 | 删除已发生约 78 秒 |
| 07:58:12 | 新 generation 上执行恢复 | 下一条独立调用重新获取 sandbox |

不能仅凭两个 MCP session 的存在，就断言上述空窗均由 transport 关闭造成。多 transport 互相释放是代码中可重现的独立缺陷；这次数据库证据和回归测试还确认了短 TTL 覆盖，以及 fencing 后旧 lease 缓存失效不及时。

## 修复后的约束

- Core 对仍未释放的 activity 采用 `max(现有 expiry, 此次续租 expiry)`，短 TTL 只能补足期限，不能缩短已经取得的期限。显式 release 后重新 acquire 使用新 TTL，不恢复旧期限。
- Executor Gateway 按 workspace/session/run/attempt/generation 共享 lease；同一 attempt 的 MCP transport 仅持引用，最后一个引用关闭才释放。对 capability、holder、仓库、权限或 deadline 不一致的请求拒绝共享；每个 HTTP 请求仍走原有实时鉴权。
- 获取、最后释放与 fencing 之间设串行化屏障。旧引用晚到的清理不能释放新 lease；等待者可取消，底层清理仍受 15 秒上限约束。
- fencing 开始即通知所有旧引用失效；新的 acquire 等 fencing/清理完成。只允许后续独立工具调用获取目标，绝不重放已执行、结果 unknown 的命令。
- 无法确认 release 的结果时保留失败状态，不让新 acquire 超过可能尚在执行的旧 release。失败记录在签名 authority 到期后移除。
- 新日志 `managed sandbox target fenced` 仅记录身份、目标代次、固定原因码、fencing 结果以及是否失效了共享 lease，不记录命令、输出或密钥。

引用管理是进程内实现，依赖现有 **executor-gateway 单副本 + Recreate** 部署约束；不能在无 owner routing / 分布式租约设计的情况下直接扩为多副本。Core 的租约、权限、attempt fencing 与 sandbox generation 校验均保留。

## 验证

使用仓库 / CI 固定的 `GOTOOLCHAIN=go1.26.5`：

- `go -C v2 test ./...`
- `go -C v2 test -race ./internal/executorgateway -count=10`
- 独立临时 PostgreSQL 17 上运行 `TestPostgreSQLManagedSandbox*` 与 `TestPostgreSQLKubernetesSharedProfile*`，覆盖 TAE / K8s 生命周期、短续租、release 后重新 acquire 和 workspace 隔离。
- 反向验证：恢复旧 `lease_expires_at = EXCLUDED.lease_expires_at` 语义时，Kubernetes 生命周期回归报错 `repository renewal shortened activity ...`；恢复修复后通过。
- CI 的真实 PostgreSQL 步骤已加入上述 managed sandbox 测试，避免只编译但跳过集成用例。

这次不把已有的 unknown 执行改判成功，也不取消安全 fencing。最初那次执行为什么得到 unknown，仍需独立分析该 execution 的运行结果；本修复消除的是它之后连续工具调用被旧 lease 阻断的问题。
