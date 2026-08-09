# ADR 0008：托管 sandbox executor 使用跨集群 agent-sandbox + 出站 attestation 身份

> 历史状态：managed transport 结论已由
> [ADR 0012](0012-unified-execution-gateway-tae-backend.md) 取代。managed executor 不再在 sandbox
> 中运行 agentx；本文保留为约束与方案演进记录。

- 状态：Partially Superseded
- 日期：2026-08-04
- 影响范围：Core、executor-gateway、agentx（独立仓库）、新增 sandbox-controller、生产部署配置

## 背景

Phase 1 只交付 BYO executor：用户必须在自己的机器上安装并运行 agentx，产品才有"双手"。
这带来两个无法回避的问题：

1. **产品上无法开箱即用。** 新 workspace 在用户完成 enrollment 之前没有任何执行能力，
   `executor-mcp/1.1` 的 `shell`/`read_file` 全部无处可去。
2. **harness 侧的本地 backend 已经把话说死。** [`ARCHITECTURE.md`](../ARCHITECTURE.md) §7.2 明确：
   本地 `fork/exec` backend 的前提是"harness 本地不执行任意用户代码"，
   "一旦未来开放本地任意代码、用户脚本、stdio MCP 或未信任 plugin，该 backend 必须 fail closed，
   改用经过独立安全评审的 sandbox backend"。因此任意代码的执行位置必须在 harness 之外。

[D6](../ARCHITECTURE.md#14-关键决策) 已经预留了结论：`managed` 是"复用同一 agentx 协议的 Phase 2 扩展"，
当时未做的原因是"managed sandbox 生命周期和存储尚未完整设计"。本 ADR 补齐该设计。

选定的 sandbox 实现是 [kubernetes-sigs/agent-sandbox](https://github.com/kubernetes-sigs/agent-sandbox)
（`agents.x-k8s.io/v1beta1`）。关键约束是：**该 sandbox 集群未必与 agentserver 部署在同一个
Kubernetes 集群**，因此不能假设集群内 DNS、ClusterIP、Service FQDN 或 NetworkPolicy 可以跨越两侧。

## 决策

### 1. sandbox 是 executor，不是 harness

托管 sandbox 作为一个 `kind=managed` 的 executor 接入，复用既有的 agentx WSS 协议、
`process-v1` exec profile、executor MCP 工具面和 `execution → operation` 副作用状态机。
sandbox 内运行的是 agentx + pinned stock `codex exec-server --listen stdio`，与 BYO executor 完全同构。

明确不做的事：不把 harness-worker、stock app-server 或 llmproxy capability 放进 sandbox。
大脑仍在 agentserver 集群内，由 harness-pool 本地 `fork/exec`。这保证：

- 模型凭证边界不变（sandbox 永远拿不到 `aud=llmproxy` capability）；
- checkpoint finalizer 的受信本地读取边界不变；
- 审批、catalog 冻结、generation fencing、unknown 语义全部无需重新设计。

反过来，harness 侧的本地 backend 因此**继续满足**其"不执行任意用户代码"前提：任意代码从来
不在 pool Pod 内执行，而是在 sandbox 里。

### 2. 连接方向仍然是 agentx 出站，不使用 agent-sandbox 的入站面

agentx 从 sandbox 内主动拨出 WSS 到 `executor-gateway.byted.bps.dev`。因此跨集群拓扑的
**数据面要求只有一条：sandbox 集群允许出站 TCP/443 到两个固定公网 host**
（executor-gateway 与 [ADR 0009](0009-sandbox-egress-credential-proxy.md) 的 egress-gateway）。

明确不使用 agent-sandbox 的入站交互面：不使用官方 `python-runtime-sandbox` 的 8888 FastAPI
runtime，不使用 sandbox-router，不使用 Gateway API/云 LB，不依赖 `status.serviceFQDN`，
不使用 `kubectl exec`/`port-forward`。这些都需要 caller 与 sandbox 同集群或额外建设公网入站，
正是跨集群场景下最脆弱的部分。出站模型把这一整类问题消掉了。

agentserver 与 sandbox 集群之间因此只剩**一条控制面依赖**：创建/暂停/删除 Sandbox 对象需要
访问该集群的 kube-apiserver。这条依赖被收敛到单一组件（§3），且不在 run 的数据热路径上。

### 3. 新增 sandbox-controller，是唯一持有 sandbox pool 凭据的组件

`sandbox-controller` 是 agentserver 集群内的新 Deployment：

- 持有 `sandbox_pools` 中每个目标集群的 API 凭据（sealed，见 §5），**不下发给任何其他组件**；
- 按 Core 的 provisioning 指令创建 `Sandbox`、切换 `operatingMode`、删除对象；
- 以 reconcile 模型工作：进程重启后通过 label selector 列举远端 Sandbox 重建视图，
  不依赖内存状态；
- 负责孤儿回收：远端存在但 Core 无对应活跃记录的 Sandbox 必须被 GC。

sandbox pool 的注册是**平台管理员操作，不是 workspace owner 操作**。理由是它需要访问私网
kube-apiserver，与 [ADR 0005](0005-workspace-llm-gateway-oidc.md) §5 对 workspace 可配置 URL
"首版只支持公网、拒绝私网/元数据/集群地址"的 SSRF 约束直接冲突。把两类配置放进同一权限面
会让 workspace 用户获得对内网的探测能力。

### 4. managed executor 使用 attestation 身份，不为每个 sandbox 创建 Hydra client

[ADR 0003](0003-executor-enrollment-machine-proof.md) 的 enrollment 为每个 executor 创建一个
确定性 Hydra client。对生命周期以 session 计、频繁创建销毁的 sandbox，这会带来 Hydra client
数量膨胀和删除失败即泄漏的 GC 问题，而其收益（人工持有的长期机器身份）在托管场景并不存在。

managed executor 因此使用独立的 attestation 流程：

1. Core 预建 `kind=managed` 的 executor 行，并生成一次性 256-bit `attestation_secret`
   （只存 SHA-256），绑定 workspace、session、pool 与预期的 sandbox 对象标识；
2. sandbox-controller 在远端集群创建 Sandbox 的同时，创建一个以该 Sandbox 为 ownerRef 的
   Secret，内容为 `{executorId, attestationSecret, executorGatewayURL, caBundle}`；
   ownerRef 保证 Sandbox 删除时 Secret 一并回收；
3. agentx 以 managed 模式启动，在 **tmpfs** 中生成一次性 Ed25519 机器密钥，读取 attestation
   secret，向 executor-gateway `POST /internal/v2/agentx/attestations` 提交公钥与对
   `(attestation secret, canonical request)` 的持有证明；
4. gateway 有界中继给 Core；Core 原子消费该 secret，绑定公钥，返回短期
   `aud=executor-gateway`、`scope=executor:connect` 的 Core 签发 token（域前缀 `asv2mgd1`，
   与 Hydra opaque token 在验证入口上完全分离）；
5. agentx 立即删除 secret 文件，随后进入**与 BYO 完全相同**的 challenge + `executor-wss-proof/ed25519-v1`
   WSS 持有证明流程。

安全性质与 ADR 0003 对齐：注入的 attestation secret 单独不足以建立执行通道（还需要 Ed25519
持有证明），单次消费、短 TTL、绑定单一 executor，且该 executor 本身就是这个 sandbox——
即使泄漏也不构成越权。私钥只存在于 tmpfs，随 Pod 销毁而消失，不需要轮换协议。

后续 Phase B 可用 Kubernetes ServiceAccount projected token 做联邦身份（Core 通过 pool 凭据对
远端集群做 TokenReview），彻底去掉注入的 bearer。该演进不改变 WSS 层协议，因此不阻塞首版。

### 5. sandbox pool 凭据与 workspace 凭据使用彼此独立的 sealing 域

pool 的 kube-apiserver 凭据以 AES-256-GCM 密封存于 Core，使用**独立的 keyring**，AAD 绑定
`pool id + credential version`。它不得复用登录事务 key、run capability key、LLM gateway
sealing key、对象存储凭据或 ADR 0009 的 workspace credential sealing key。密钥由 Pulumi 随机
生成，支持 active + 旧 key overlap 以便轮换。

### 6. 生命周期绑定 session，首版不使用 WarmPool/Claim

sandbox 按 session 供应：首次 run 时创建，run 之间通过 `operatingMode: Suspended` 挂起，
下一个 run 恢复，session 结束或 TTL 到期后删除。用户工作树（git checkout、构建缓存）保存在
sandbox 的 PVC 上，跨 run 保留。

这与既有状态所有权表一致：工作树的权威在 executor 环境，**集群内没有副本**。托管形态不改变
这一点，只是 executor 环境现在由平台供应。因此必须显式承认并向用户表达：sandbox 删除即丢失
未提交的工作。TTL、idle-suspend 时限和删除必须是可见的产品语义，不能是隐式后台行为。

首版只使用核心 `Sandbox` CRD，不使用 `extensions.agents.x-k8s.io` 的 `SandboxWarmPool`/
`SandboxClaim`。理由：扩展 API 迭代更快（v0.5.0 曾发生 `replicas → operatingMode` 的破坏性
变更），而首版的冷启动延迟尚未测量，不能先用未验证的复杂度换未证实的收益。WarmPool 作为
Phase 2 在冷启动 SLO 有实测缺口时再引入。

### 7. runtimeClass 是生产必需项，且必须先验证 bwrap 可用性

harness 的本地 backend 依赖"固定代码"威胁模型；sandbox 明确执行任意代码，因此**面向不可信
多租户的生产部署**需要真实的内核级隔离边界：`podTemplate.spec.runtimeClassName` 应为
Kata、Firecracker 或等价 runtime。

**但当前环境不具备 KVM/嵌套虚拟化，因此 PoC 与首期实现使用默认 runc（共享宿主内核）。**
这是一个知情的、有时限的取舍，必须准确表述它的含义：

- **仍然是提升。** 相比现状（任意代码若开放将在 harness-pool Pod 内 `fork/exec`），
  独立 Pod 带来独立的 NetworkPolicy、独立 PVC、独立 UID 域和限定在该 Pod 的爆炸半径。
  [`ARCHITECTURE.md`](../ARCHITECTURE.md) §7.2 要求的"改用 sandbox backend"在形式上成立。
- **但不满足其实质。** 该条同时要求"经过独立安全评审的 sandbox backend"。共享宿主内核意味着
  沙箱内的模型生成代码与节点之间只隔着 namespace/cgroup/seccomp，一个内核提权漏洞即可逃逸。
- **因此边界是：受控环境、可信用户、内部验证可以用；面向不可信输入的多租户生产上线前
  必须重新回到 runtimeClass 问题**（届时若仍无 KVM，需要独立评审并可能引入节点池隔离、
  每租户独占节点等补偿控制）。

runc 形态下仍应默认启用的低成本加固（PoC 阶段即可落实）：`seccompProfile: RuntimeDefault`、
drop 全部非必需 capability、非 root 运行、只读 rootfs（`/workspace` 与 tmpfs 除外）、
默认拒绝的 NetworkPolicy，以及按 workspace 划分 namespace。

好消息是宿主内核消除了全部内核能力缺口：`openat2` 及所需 `RESOLVE_*`、cgroup v2 完整事务
（含 `cgroup.freeze`）、bubblewrap user namespace、nftables `meta skuid` 均原生可用，
agentx 的生产 Linux 模型可原样运行，§7.1 的 managed profile 不再是前置阻塞项。

**S-01 调查结论（2026-08-04，对 gVisor master `901ffefc3a7b` 与 `release-20260727.0` 核对）：
排除 gVisor，生产选用真实内核的轻量 VM（Kata，或 agent-sandbox v0.5.4 起支持的 Firecracker）。**

依据是本设计依赖的内核能力在 gVisor 上有三处不可回避的缺失：

| 依赖 | gVisor | 后果 |
|---|---|---|
| `openat2` + `RESOLVE_BENEATH/NO_SYMLINKS/NO_MAGICLINKS/NO_XDEV` | **完全未实现**（syscall 437 在表中缺失，无存根、无常量、**无 tracking issue**） | agentx 启动探测失败即**不发布 production filesystem capability**（[`ARCHITECTURE.md`](../ARCHITECTURE.md) §9.2），`read_file` 能力线不可用；若改为回退普通 `openat`，则直接丢掉 `066acf6`/`5d40b6b` 两个门禁关闭的 TOCTOU 边界 |
| bubblewrap `--unshare-net` / `--unshare-all` | **损坏**：gVisor 新建 netns 时预先把 loopback 配成 UP+127.0.0.1（真实内核是 DOWN 无地址），bwrap 的 `loopback_setup()` 直接 abort。修复 PR google/gvisor#13532 自 2026-06-20 开启至今未合并 | **pinned stock Codex 的 Linux sandbox 起不来**，而这是我们不控制的 upstream 代码；`shell-v1` 又硬编码 `enforceManagedNetwork=true`（`internal/executorgateway/shell_mapper.go:228,297`） |
| cgroup v2（`CLONE_INTO_CGROUP`、`cgroup.kill`、`cgroup.freeze`） | 沙箱内 cgroup2fs 于 2026-07-06 才落地，藏在默认关闭的 `--mount-cgroup-v2` 后，官方标注 `EXPERIMENTAL / Do not use for production workloads`；且 **`cgroup.freeze` 未实现**（PR #13683 无人 review） | 默认配置下 `clone3(CLONE_INTO_CGROUP)` 返回 **EINVAL**，而 Go 的 `UseCgroupFD` 路径**没有回退分支**（`syscall/exec_linux.go:315-344`），runner 直接起不来 |

其中 `openat2` 一项没有上游路线图，bwrap 一项的修复 PR 悬置逾一月，而这两处恰好都落在
已经通过门禁、不应重新打开的安全边界上。把一个专门用来运行不可信代码的沙箱建立在
"实验特性 + 未合并补丁 + 自行重写内核安全原语"之上，风险与收益不成比例。

Kata 使用真实 upstream 内核（当前 pin v6.18.35），上述能力全部原生可用，且不需要修改
agentx 的 cgroup 事务协议。代价是必须具备 KVM/嵌套虚拟化，冷启动约 150–300ms
（gVisor 为毫秒级）——这个代价由 §6 的 session 级 sandbox 生命周期摊薄，可以接受。

两项附带结论，即使选定 Kata 也应记录：

- **`meta skuid` 的可用性需实测确认。** Kata 的内核 fragment 中未能直接确认 `CONFIG_NFT_META`，
  需在 guest 内实跑 `nft add rule inet t out meta skuid 1000 accept` 验证。若不可用，
  `internal/networkguard` 的原生 netlink nftables 实现需要一条 `iptables-legacy -m owner --uid-owner`
  的等价路径（该路径在 gVisor 上反而是完整支持且有 CI 覆盖的）。
- **仍然需要 agentx 的 managed 模式。** 见下条。

### 7.1 托管形态必须引入 agentx managed profile

agentx 现有的 Linux 生产模型是为 BYO executor 设计的：它跑在用户的真实机器上，爆炸半径是
用户的整个系统，因此 cgroup 事务、进程树回收、safe-open 都是**主控制**。在一次性、每 session
独立的 sandbox 里，外层沙箱边界已经提供了其中相当一部分，这些机制更接近纵深防御。

但这个判断本身不足以让方案跑通，因为 **agentx 是 fail-closed 设计**：能力缺失时它会拒绝启动
或拒绝发布对应 capability，而不是自行降级。

**在 runc + 宿主内核下这不是问题**：所有内核能力原生可用，agentx 生产 Linux 模型原样运行，
不需要 managed profile。该工作项仅在未来改用 gVisor 这类缺失内核能力的 runtime 时才成为前置
阻塞项；改用 Kata/Firecracker 同样不需要它。

仓库已有可复用的表达方式：`internal/networkguard` 对非 Linux 平台直接 fail closed，
文档把 macOS 标为"只用于非特权开发测试"——"平台不具备生产管控后端就不算生产"这一建模
可以直接沿用到 runtimeClass 维度。

### 7.2 若将来重新评估 gVisor

需要同时满足：`openat2` 及所需 `RESOLVE_*` 实现并进入 release；bubblewrap loopback 修复合并
（google/gvisor#13532）；cgroup v2 脱离 `--mount-cgroup-v2` 实验门禁且 `cgroup.freeze` 合并
（#13683）。在此之前不得在任何对外材料中把 gVisor 描述为受支持的生产 runtimeClass。
另可跟踪 google/gvisor#13796——该 tracking issue 的目标场景（不可信 AI 生成代码 + egress
白名单）与本设计高度重合，其四个子任务合并后 nftables 侧的结论需要重新评估。

### 8. agent-sandbox 版本锁定

`agents.x-k8s.io/v1beta1` 的 CRD schema 必须 pin 到精确版本，并保存 golden fixture；
CI 对 fixture 做 drift 检查。升级 agent-sandbox 走与 Codex runtime lock 相同的流程：
先跑 schema diff 与供应/暂停/恢复/删除的行为回归，不允许自动跟随 latest。
该项目当前节奏为 1–2 周一个 release 且有破坏性变更史，这条约束不是形式主义。

## 后果与限制

- 产品获得零安装的执行能力；BYO executor 保持不变，两种 `kind` 共用同一执行链路与审计面。
- 跨集群依赖被收敛为"一个组件 + 一条控制面凭据 + 两条出站 443"，数据面不依赖任何跨集群
  入站连通性。
- sandbox-controller 成为新的单点：它不可用时无法供应新 sandbox，但**已连接的 sandbox 不受
  影响**（数据面走 WSS，不经过 controller）。这是有意的故障域切分。
- managed executor 引入了与 BYO 不同的第二条身份路径。两条路径在 token 前缀、验证入口和
  Core 授权分支上必须完全分离，任何一条都不得接受另一条的凭据。
- 远端集群不可达时的语义必须是"run 留在 queued"，不是"run 失败"。provisioning 失败发生在
  `turn/start` 被接受之前，因此走既有的 `AbandonAttempt` 仲裁路径 requeue，不产生新的收口语义。
- 本 ADR 不解决多副本 executor-gateway owner routing（仍是 [D15](../ARCHITECTURE.md#14-关键决策)
  的 Phase 1 单副本约束）。托管 sandbox 数量增长会更早触及该瓶颈，需要在容量规划中显式跟踪。
- gVisor/bwrap 的兼容性结论在 S-01 门禁通过前，本 ADR 的生产可部署性不成立。
