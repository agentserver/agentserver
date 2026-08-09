# agentserver v2 — Managed Sandbox Executor 设计

> 历史方案：本文的 agent-sandbox/agentx 拓扑已被 TAE managed backend 取代。当前 canonical 设计见
> [`TAE_MANAGED_EXECUTOR.md`](TAE_MANAGED_EXECUTOR.md) 与
> [ADR 0012](adr/0012-unified-execution-gateway-tae-backend.md)。

> 状态：设计草案（proposal，未进入实现）
>
> 本文落地 [ARCHITECTURE.md](ARCHITECTURE.md) 关键决策 D6 预留的 Phase 2 扩展：`managed` executor。
> 被采纳后，本文对 ARCHITECTURE.md §8（Executor 设计）、§5.3（凭证存储）与 IMPLEMENTATION.md
> §12（分阶段交付）构成增量修订；在此之前，两份基线文档维持现状，Phase 1 语义不变。
>
> 评审输入：ARCHITECTURE.md / IMPLEMENTATION.md、ADR 0002/0003/0005、
> v1 `internal/credentialproxy` 设计（`docs/superpowers/specs/2026-04-11-credentialproxy-design.md`，
> 仅作先例参考，不复用代码）、kubernetes-sigs/agent-sandbox v0.5.4（`agents.x-k8s.io/v1beta1`）。

## 0. 需求

两条外部需求：

- **R1 跨集群 sandbox**：sandbox 基于 [kubernetes-sigs/agent-sandbox](https://github.com/kubernetes-sigs/agent-sandbox)
  提供的 `Sandbox` CRD 创建；sandbox 所在的 Kubernetes 集群未必是 agentserver 部署集群。
- **R2 凭证不进 sandbox**：sandbox 内不能接触用户真实凭证。例如在 sandbox 内运行 `gh` 时，
  发出的请求只携带内部占位凭证；请求被 agentserver 基础设施拦截后，替换为用户预先留存的
  真实凭证再转发上游。

由 v2 既有约束推导出的附加需求：

- **R3 不分叉执行协议**：sandbox 必须复用 Phase 1 已钉死的 executor 信任链——agentx 出站 WSS、
  双钥 enrollment（ADR 0003）、audience 分离 run capability（ADR 0002）、execution/operation
  副作用边界与审批链路。sandbox 不引入第二种"harness 直连远端执行"的协议。
- **R4 fail-closed 与不重放**：sandbox 生命周期、凭证替换的每个失败路径都必须收敛到
  v2 已有的 `unknown/interrupted/fail closed` 语义，不新增自动重放。
- **R5 逐请求在线授权**：真实凭证的每一次使用都经过 Core live-authorize；吊销、成员移除、
  policy 变化对下一个请求立即生效。Core 不可达时 fail closed。
- **R6 run 热路径不创建 Kubernetes workload**：沿用 ARCHITECTURE.md §7.5 原则。创建 Sandbox CR
  只发生在 session/环境 provisioning 路径，run 创建与工具调用不等待任何 Kubernetes 写操作。

非目标：

- 不把 harness（大脑，harness-pool/worker/app-server）搬进 sandbox；大脑仍在 agentserver 集群内
  按 Phase 1 本地 `fork/exec` 运行。sandbox 只承载"双手"。
- 不使用 agent-sandbox 官方的入站数据面（python-runtime FastAPI:8888、sandbox-router、Gateway LB）。
  v2 的数据面方向相反：agentx 从 sandbox 内出站拨到 executor-gateway，零入站需求。
- Phase 首版不做 SSH/任意 TCP 的凭证注入（git 一律走 HTTPS）；不做全局 TLS MITM CA（见 §6.4）。
- 不在本文解决 executor-gateway 多副本 owner routing（D15/§9.4 既有边界；见 §10.4 容量风险）。

## 1. 结论综述

**sandbox 就是 managed executor 的宿主。** 一个 managed executor = 一个 agent-sandbox `Sandbox` CR，
Pod 内运行与 BYO executor 完全相同的 agentx 发行包 + pinned stock `codex exec-server`。agentx 从
Pod 内出站拨 WSS 到 `executor-gateway.byted.bps.dev`，enrollment、capability、审批、execution
状态机全部复用 Phase 1 已实现、已过门禁的链路。

这个选型直接消解 R1：跨集群只需要两条出站连接——

- 控制面：新增组件 `sandbox-controller` 从 agentserver 集群出站访问目标集群 kube-apiserver
  （namespace-scoped 凭证），创建/回收 Sandbox CR 并注入一次性 enrollment token；
- 数据面：sandbox 集群只需放行 sandbox Pod 到 `executor-gateway.byted.bps.dev` 与
  `egress-proxy.byted.bps.dev`（新增）两个 host 的出站 443。

sandbox 集群不需要任何入站通道、不需要与 agentserver 集群网络打通、不需要共享数据库或对象存储。

R2 由新增组件 `egress-proxy` 承担：sandbox 内的工具只持有 **egress capability**（Ed25519 短期
占位凭证，离开 egress-proxy 无效）；工具的网络出口被四层机制（Pod NetworkPolicy、exec-server
managed network、agentx owner policy、工具配置重写）收敛到 egress-proxy 的 per-provider façade；
egress-proxy 逐请求向 Core live-authorize，Core 解封 workspace 预存的真实凭证并以
egress-proxy 专用响应返回，代理剥掉占位凭证、注入真实凭证后转发上游。真实凭证只存在于
Core（AES-256-GCM 密封，沿用 ADR 0005 的 sealer 模式）与 egress-proxy 的单请求内存中，
永不进入 sandbox 集群、Pod、env、rollout 或 checkpoint。

新增面收敛为：两个组件（`sandbox-controller`、`egress-proxy`）、一个 capability audience
（`aud=egress-proxy`）、一组 Core 表（sandbox 集群注册、managed executor 绑定、egress 凭证与
policy）、一条 Core 内部授权路由（`authorize-egress`）、agentx 仓库的一个 managed 运行模式。
executor wire 协议（agentx-wss、process-v1、executor-mcp catalog）不变；harness 侧零改动。

## 2. 总体架构

```text
        agentserver 集群                                目标 sandbox 集群（可以是另一朵集群）
┌────────────────────────────────┐            ┌──────────────────────────────────────┐
│ core ◄─mTLS─ sandbox-controller│──kube API─►│ agent-sandbox controller (pinned)    │
│  │  ▲              (新增)      │  (出站)    │   └─ Sandbox CR / Secret / NetPol    │
│  │  │ authorize-egress          │            │        │                             │
│  │  └────────┐                 │            │        ▼                             │
│  │           │                 │            │ ┌──────────────────────────────────┐ │
│ harness-pool │                 │            │ │ sandbox Pod（managed executor）  │ │
│   └─ worker ─┼── MCP ──┐       │            │ │  agentx connector(root,3 caps)   │ │
