# 托管 sandbox + 出口凭据替换：PoC 实施方案

> 历史 PoC：本文验证目标仍有参考价值，但实现拓扑已由 TAE managed backend 与原生 Policy
> Webhook 取代。当前计划与验收门禁见 [`TAE_MANAGED_EXECUTOR.md`](TAE_MANAGED_EXECUTOR.md)。

> 状态：PoC（不是生产设计）
>
> 生产设计见 [ADR 0008](adr/0008-managed-sandbox-executor.md) / [0009](adr/0009-sandbox-egress-credential-proxy.md) /
> [0010](adr/0010-tool-packs.md) 与 [`MANAGED_SANDBOX.md`](MANAGED_SANDBOX.md)、[`LARK_SLICE.md`](LARK_SLICE.md)。
> **本文只描述为了尽快跑通端到端而有意做的偏离**，每一处偏离都在 §2 显式列出，
> 不得把本文的任何简化当作生产结论。

## 1. 目标与验收

一句话：**在对话里让模型发一条飞书消息，消息真的送达，而沙箱内自始至终只有假凭据。**

六条断言，全部满足才算 PoC 通过：

**凭据替换（P0a）**

1. 模型自主发出 `shell` 调用，argv 为 `["lark-cli", ...]`，在 sandbox 内执行；
2. 飞书侧真实收到该请求并成功（消息送达 / 接口返回成功）；
3. 进入 sandbox 扫描环境变量、`/proc/*/environ`、文件系统与 CLI 配置，
   **只能找到占位 token，找不到真实 token**。

**起 sandbox（P0b）**

4. 新 session 的首个 run **自动创建** sandbox 并完成接入，全程无人工预置；
5. 两个并发 session 得到**两个不同的** sandbox，彼此工作树不可见；
6. session 结束或闲置后 sandbox 被回收（`shutdownTime` 兜底 TTL 可见效）。

第 3 条是凭据设计的意义所在；第 4–6 条是"起 sandbox"这个功能本身。
**只做 1–3 不构成本 PoC 的通过**——那只证明了"预先起好的 sandbox 能用"，
与现有 BYO executor 路径没有本质区别。

## 2. 相对生产设计的偏离（有意为之）

| 生产设计 | PoC 做法 | 恢复时机 |
|---|---|---|
| 跨集群供应 | **同集群**。程序化供应本身**保留**（见 §3.1），只是 controller 用 in-cluster config，不做远端集群的密封凭据与 SSRF 约束 | P3 |
| 完整 sandbox-controller（reconcile、孤儿回收、多 pool 调度、lease） | **最小供应器**：建 executor → 签 enrollment token → 建 Secret + `Sandbox` CR → 等就绪；回收靠 `shutdownTime` + 显式删除 | P3 |
| managed executor attestation（Ed25519 持有证明） | 程序化调用**现有** enrollment API 签发一次性 token 并注入 Secret | P3 |
| runtimeClass = Kata/Firecracker | **runc（共享宿主内核）**，因当前无 KVM。安全含义见 [ADR 0008 §7](adr/0008-managed-sandbox-executor.md) | 上线不可信多租户前 |
| tool pack 注册表 + Platform 配置入口 | **硬编码配置**（一段 skill 文本 + 一条出口规则） | P1/P2 |
| 飞书 OAuth grant（每人授权自己） | **Secret 里一个静态 token** | P1（这是用户成功指标 M1，必须补） |
| 审批 + effect class + 审计表 | 全 `allow` + 结构化日志 | P2 |
| tool pack 与 checkpoint 的组合摘要 | 已取消，不计算 | — |
| agentx loopback 代理持 capability | **占位 token 直接进子进程环境**（[ADR 0009 §2](adr/0009-sandbox-egress-credential-proxy.md) 里记录的简化变体） | P2 |
| 分 UID nftables 出口管控 | P0 先只靠 NetworkPolicy | P2 |
| 无状态池 / suspend-resume / warm pool | 不做 | 视需要 |

**关键结论：P0 不需要改 agentx。** 见 §4.1。这去掉了整个跨仓协作依赖，是 PoC 能快的主要原因。

## 3. 组成

| # | 件 | 说明 | 量级 |
|---|---|---|---|
| 1 | sandbox 镜像 | `linux/amd64`：agentx + pinned stock Codex 0.146.0 + `codex-resources/bwrap` + tini + **lark-cli** + egress CA 证书 | 镜像构建 |
| 2 | **`Sandbox` CR** | agent-sandbox `agents.x-k8s.io/v1beta1`，同集群。`spec.podTemplate` 内：非 root、只读 rootfs（`/workspace` 与 tmpfs 除外）、`seccompProfile: RuntimeDefault`、drop 全部非必需 capability；配 `lifecycle.shutdownTime` + `shutdownPolicy: Delete` 作为兜底 TTL。另配默认拒绝的 NetworkPolicy（只放行 executor-gateway 与 egress-gateway） | 配置 |
| 3 | enrollment | 容器入口 `agentx enroll --token-stdin` + `agentx connect`，复用现有流程，零新代码 | 配置 |
| 3b | **最小供应器** | session 首个 run 时自动建 executor/token/Secret/CR 并等就绪；结束时删除。见 §3.2 | ~250 行 Go |
| 4 | **egress-gateway PoC** | 新的小服务：CONNECT 代理 → 临时 CA 签叶证书终止 TLS → 命中飞书 host 则替换 `Authorization: Bearer` → 其余 host 一律拒绝 | ~300 行 Go |
| 5 | shell mapper env 注入 | 在 `internal/executorgateway/shell_mapper.go:178` 合并平台注入的 env | ~30 行 + 配置 |
| 6 | skill 注入 | worker 在 `thread/start` 填 `baseInstructions`（wire 字段已存在，见 `internal/harnessworker/runner.go:246`） | ~50 行 |

真正的新代码是第 4 项与第 3b 项，其余都是接线。

### 3.1 保留 agent-sandbox，只砍 sandbox-controller

这两者容易混为一谈，但砍的边界完全不同：

- **agent-sandbox（CRD/抽象）保留。** 它就是"怎么起 sandbox"的答案。`Sandbox.spec.podTemplate`
  是一个完整 PodSpec，所以写 CR 与写 Pod 的 YAML 工作量相同，却白拿生命周期管理：
  `lifecycle.shutdownTime` + `shutdownPolicy: Delete` 提供兜底 TTL（避免 PoC 期间漏掉的
  sandbox 长期占资源）、`operatingMode: Suspended` 提供有状态 sandbox 的暂停/恢复、
  PVC 绑定与统一的 status/conditions。
- **sandbox-controller 不砍，但收缩为"最小供应器"。** "起 sandbox"是本次要交付的功能本身，
  手工 `kubectl apply` 一个静态 sandbox 只能证明"预先起好的 sandbox 能用"，
  与现有 BYO executor 路径没有本质区别，且会让 session↔sandbox 绑定、并发隔离、
  生命周期回收三件事全部失去验证。收缩掉的是 reconcile 循环、孤儿回收、多 pool 调度、
  owner lease 与跨集群密封凭据，这些放 P3。

还有一个决定性理由：**`runtimeClassName` 只是 `podTemplate` 里的一个字段。**
现在留空即 runc，将来具备 KVM 后改一行就切到 Kata/Firecracker，不是重新架构。
现在绕开 CRD 反而会让那次迁移变成改造。

### 3.2 最小供应器做什么

一条直线，不含 reconcile：

```text
触发（session 首个 run 且该 session 无就绪 sandbox）
  → Core: 创建 executor
  → Core: 签发一次性 enrollment token（复用现有 API，非 attestation）
  → k8s: 创建 Secret（token）+ 创建 Sandbox CR（ownerRef 指向它，随 CR 回收）
  → 等待 Sandbox Ready 且该 executor 在 Core 中变为 online
  → 把 (session → executor/env) 绑定写回 Core
回收（session 结束或闲置）
  → 删除 Sandbox CR；`shutdownTime` + `shutdownPolicy: Delete` 作为兜底
```

sandbox 内的容器入口沿用现有流程：`agentx enroll --token-stdin` 读取挂载的 token，
随后 `agentx connect` 出站拨到 executor-gateway。**这一段零新代码。**

**触发点是一个需要拍板的小决策。** 两个选项：

- **A（推荐）**：在 session 创建时供应。改动最小，完全避开 run 热路径，
  与 [`MANAGED_SANDBOX.md`](MANAGED_SANDBOX.md) 讨论过的"run 热路径不做 Kubernetes 写操作"一致。
  代价是 session 创建后即占资源。
- **B**：在 CreateRun 冻结 launch state 时写一条 outbox（生产设计的做法），
  供应与 harness 启动并行，首个 run 需要有界等待 sandbox 就绪。更接近终态，Core 改动稍大。

PoC 建议先做 A 跑通验收，B 留到 P3 与真正的 controller 一起做。

前提：目标集群需安装 agent-sandbox controller 与 CRD（若尚未安装）。
PoC 只使用核心 `Sandbox` 资源，**不使用** `extensions.agents.x-k8s.io` 组的
`SandboxTemplate`/`SandboxWarmPool`/`SandboxClaim`——那组迭代更快、破坏性变更史更多
（v0.5.0 曾改 `replicas` → `operatingMode`），且 PoC 用不到预热池。
版本按 [ADR 0008 §8](adr/0008-managed-sandbox-executor.md) pin 住，但 CRD drift 门禁 PoC 阶段可从简。

## 4. 关键实现细节

### 4.1 env 注入点在我们自己的仓库里

`internal/executorgateway/shell_mapper.go:178-193` 在网关侧构造 `explicitEnvironment` 并写入
`process/start` 参数。PoC 在此处合并三个平台注入项（并覆盖模型可能提供的同名 key）：

```
HTTPS_PROXY                      → egress-gateway 的 ClusterIP:port
SSL_CERT_FILE                    → 镜像内 egress CA 路径
LARKSUITE_CLI_USER_ACCESS_TOKEN  → 占位 token
```

生产设计里这些应由 agentx 按受信运行时策略注入（[D22](ARCHITECTURE.md#14-关键决策)）；
PoC 放在网关侧是为了避开跨仓改动。

**这是 P0 唯一的重大未知**：agentx 会把远端 env policy 收紧到本地 allowlist
（[`ARCHITECTURE.md`](ARCHITECTURE.md) §8.3），可能把这三个变量剥掉。
**第一步就要实测这一条**；若被剥掉则只能回到 agentx 侧注入（跨仓，工期显著变长）。

### 4.2 lark-cli 是 Go 二进制，这让 MITM 很便宜

`lark-cli` 是 Go 编译的，因此：

- `net/http` 默认 transport 走 `ProxyFromEnvironment` → 认 `HTTPS_PROXY`/`NO_PROXY`；
- `crypto/x509` 在 Linux 上认 `SSL_CERT_FILE` → 可直接信任我们的临时 CA。

两条都需实测确认（有的 CLI 自建 transport 不读环境变量），但都是分钟级验证。
成立的话，egress-gateway PoC 就是标准库 `CONNECT` + 动态签叶证书，无需任何额外依赖。

### 4.3 占位 token 的三档方案

沿用 [`LARK_SLICE.md` §5](LARK_SLICE.md) 的判定顺序，PoC 先试最省事的一档：

1. CLI 把环境变量 token 当不透明字符串透传 → 占位 token 可以是任意随机串；
2. CLI 会做结构/过期校验 → 构造结构合法、`exp` 远期的占位值；
3. CLI 仍坚持走刷新/认证端点 → egress-gateway 一并接管这些端点返回合成响应（最后手段）。

### 4.4 真凭据的来源

P0 从 Kubernetes Secret 读一个静态飞书 token，egress-gateway 启动时加载。
**P1 必须替换为 Core 托管的 OAuth grant**——用户的成功指标明确包含"在 Platform 上配置凭据"，
静态 Secret 只是为了先把替换链路跑通。

## 5. 顺序

按"最早暴露未知"排序，不是按依赖排序：

1. **验证 §4.1 的 env 注入不被 agentx 剥掉**，以及 §4.2 的两个环境变量是否被 lark-cli 认。
   这两项决定 PoC 的形态，半天内应有结论，**先做**。
2. egress-gateway PoC，本地起 + `curl` 直连验证凭据替换（不依赖任何集群）。
3. sandbox 镜像 + **手工** apply 一个 `Sandbox` CR + enrollment，跑通 `shell` 执行
   `lark-cli --version`。（若集群尚未安装 agent-sandbox，这一步先装 controller/CRD。）
   这里手工是**脚手架**，只为尽快打通链路，不是交付形态。
4. 接上 env 注入，跑通带占位凭据的真实飞书调用。
5. skill 注入，让模型自己决定调用 → **断言 1–3 达成（P0a）**。
6. 最小供应器（§3.2）替换掉第 3 步的手工 apply，加上回收。
7. 起两个并发 session 验证各自独立 → **断言 4–6 达成（P0b）**，PoC 完成。

第 2 步完全可以脱离集群做，和第 1、3 步并行。

## 6. 风险

- **runc 的安全边界**：共享宿主内核，模型生成的代码与节点之间只隔 namespace/cgroup/seccomp。
  受控环境与可信用户可接受；面向不可信输入上线前必须回到 runtimeClass 问题
  （[ADR 0008 §7](adr/0008-managed-sandbox-executor.md)）。**PoC 不得对外表述为已隔离。**
- **agentx env policy 可能剥掉注入变量**（§4.1）——P0 最大未知，第一步验证。
- **lark-cli 的本地自检可能拒绝占位 token**（§4.3）——有三档退路，但会影响工期。
- **静态 token 在 Secret 里**：PoC 期间该 token 的权限应尽量收窄（专用测试应用/测试群），
  不要用个人高权限凭据。

## 7. PoC 之后

- **P1**：飞书 OAuth grant 接入 Platform，补齐成功指标 M1。复用
  `internal/coreserver/workspace_llm_gateway_service.go` 的 grant 生命周期，
  需先把 provider 从 OIDC discovery 抽象出来（[`LARK_SLICE.md` §3.1](LARK_SLICE.md)）。
- **P2**：审批与 effect class、审计、agentx 侧注入与 loopback 代理、分 UID 网络管控。
- **P3**：tool pack 注册表（接入第二个 CLI 时才真正体现价值）、sandbox-controller、
  attestation 身份、跨集群、runtimeClass 复评。
