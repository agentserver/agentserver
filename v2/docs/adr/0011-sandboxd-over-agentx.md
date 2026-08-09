# ADR 0011：托管 sandbox 使用 sandboxd 与入站调用，不复用 agentx

> 状态：Superseded by [ADR 0012](0012-unified-execution-gateway-tae-backend.md)。TAE 已提供
> Terminal Sandbox/SandboxD，agentserver 不再自建 sandboxd；统一 execution gateway 与 provider
> boundary 的结论以 ADR 0012 为准。

- 状态：Superseded
- 日期：2026-08-05
- 影响范围：executor-gateway、harness-pool、新增 sandbox-gateway 与 sandboxd
- 取代：[D6](../ARCHITECTURE.md#14-关键决策) 中"`managed` 仅作为复用同一 agentx 协议的 Phase 2 扩展"

## 背景

[D6](../ARCHITECTURE.md#14-关键决策) 与 [ADR 0008](0008-managed-sandbox-executor.md) 假定托管 sandbox
就是"把 agentx 放进一个 pod"，从而复用已经过门禁的 executor 接入链路。这个假定在当时是合理的：
它让协议层零改动。

但在把目标收敛为"在 agent-sandbox 之上向 harness 提供 E2B 兼容接口、且由 harness 管理生命周期"
之后，这个复用变得不划算。agentx 的复杂度全部来自一组托管场景不具备的前提。

## 决策

托管 sandbox 内运行新的 `sandboxd`，由控制面**入站**调用；不运行 agentx。

理由是逐项对照后，agentx 的每个核心机制在托管场景下都失去了它的成立前提：

| agentx 机制 | 存在的原因 | 托管 sandbox 中 |
|---|---|---|
| enrollment、双钥机器身份、每 executor 一个 Hydra client | executor 是用户自己的机器，平台不能信任它自报身份 | pod 由我们创建，身份可直接注入；且短命 sandbox 会造成 Hydra client churn 与 GC 泄漏 |
| 出站 WSS、30 秒 resume journal、双向 sequence/ACK、generation fencing | 用户网络会断，且平台无法主动重连 | 同集群 Service，控制面可随时重连；断开即失败重来的代价很低 |
| cgroup v2 guardian 事务、进程树回收、runtime safe-exec | 爆炸半径是用户整台机器 | 爆炸半径是该 pod，删除 pod 即清零 |
| 本地 owner policy 与远程策略取交集 | 机器主人有独立于平台的意志 | 不存在第二个 owner |

同时，E2B 的 `envd` 已经验证了这个位置的正确形态——常驻沙箱、暴露 process/filesystem、
由控制面入站调用——`sandboxd` 是它的最小等价物。

**明确保留的部分**：Core 的 `execution → operation` 状态机、`dispatching` 不可回退边界、
`unknown` 语义、审批链路与冻结工具目录全部不变。sandbox-gateway 作为新的调用方接入既有
Core 内部命令，不新增副作用语义。换掉的只是"指令如何抵达执行端"这一层。

## 后果与限制

- **两条执行后端并存**：BYO executor 走 agentx/WSS，托管 sandbox 走 sandboxd/HTTP。
  两者长期都要维护，这是有意接受的代价——它们服务的信任模型本就不同。
  是否最终把 BYO 也迁到入站形态是一个独立问题，不在本 ADR 范围。
- **失去零入站性质**：agentx 出站模型的一个真实优点是 sandbox 集群完全不需要入站通路。
  改为入站后，跨集群部署必须在 sandbox 集群侧建立入站网关（E2B 的做法是 client-proxy
  加 `E2b-Sandbox-Id` 路由头），或者部署一个出站拨号的 relay 来保持零入站。
  **跨集群是最初的需求之一，因此这项设计不能省略**，见
  [`SANDBOX_E2B.md`](../SANDBOX_E2B.md) §11。
- agentx 已关闭的那批门禁（filesystem safe-open、cgroup containment、runtime pinned-exec）
  **不能外推到 sandboxd**。sandboxd 需要自己的、与其威胁模型相称的门禁；
  它比 agentx 简单得多，但"简单"不等于"已验证"。
- [ADR 0008](0008-managed-sandbox-executor.md) 中关于跨集群拓扑与 attestation 身份的部分，
  在改为入站模型后需要重写；其 §7 的 runtimeClass 结论（排除 gVisor；当前无 KVM 故用 runc
  并承认其安全含义）不受影响，继续有效。
