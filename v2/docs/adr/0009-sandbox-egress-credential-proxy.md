# ADR 0009：sandbox 出站流量经 egress-gateway 做凭据替换，真实凭据永不进入 sandbox

> 历史状态：对 TAE managed sandbox，自建透明代理数据面已由
> [ADR 0012](0012-unified-execution-gateway-tae-backend.md) 的 TAE Agent Gateway + Policy Webhook
> 取代。“真实凭据不得进入 sandbox”的原则、tool-pack 授权模型及 BYO 适用部分继续有效。

- 状态：Partially Superseded
- 日期：2026-08-04
- 影响范围：Core、agentx（独立仓库）、新增 egress-gateway、Platform API、生产部署配置

## 背景

[ADR 0008](0008-managed-sandbox-executor.md) 让托管 sandbox 成为 executor。要让它真正有用，
agent 必须能执行 `git clone` / `gh pr create` / `npm install` / `pip install` 这类需要第三方
凭据的操作。

直接把用户的 GitHub PAT 注入 sandbox 是不可接受的：sandbox 内运行的是模型驱动的任意代码，
凭据一旦落到该环境，其作用域、有效期和使用记录就完全脱离控制——被提示注入诱导后可以推到
任意仓库，可以外传，也无法回答"这次 push 是哪个 run 做的"。

因此约束是：**sandbox 发出的请求只携带内部短期凭据，由 agentserver 基础设施在出站路径上
替换为用户预先留存的真实凭据。**

v1 已经验证过这一模式的两个变体：`internal/credentialproxy` 用"路径寻址反向代理 + kubeconfig
里填 proxy_token"服务 Kubernetes；`internal/llmproxy` 用"header 重写反向代理"服务模型 API
（`ANTHROPIC_API_KEY` 实际填的是 proxy_token）。两者都证明了替换模式可行，但都要求
**逐个上游改写客户端配置**。v1 设计文档给 GitHub 的草图也是这个形状（`gh/hosts.yml` + `.netrc`
指向代理）。这在只有一两个上游时可行，面对 sandbox 里可能出现的任意工具与任意 URL 则不可行。

## 决策

### 1. sandbox 内的 workload UID 没有任何直接外网能力

sandbox Pod 内复用 harness 已验证的分 UID egress 模型（`internal/networkguard` 的
`UIDPolicy`，由唯一持有 `NET_ADMIN` 的 initContainer 安装，运行时进程不持有该 capability）：

- **exec-server / 子进程 UID**：只允许 loopback，其余全部拒绝，IPv6 全 drop；
- **agentx UID**：允许出站到 executor-gateway 与 egress-gateway。

Pod 级 NetworkPolicy（可由 `SandboxTemplate.spec.networkPolicy` 托管）是第二层，限制整个
Pod 的目的地并集，并显式排除远端集群自身的 apiserver、云元数据地址与私网段。

### 2. agentx 提供 loopback CONNECT 代理，capability 不进入子进程环境

agent 的工具链通过 `HTTPS_PROXY=http://127.0.0.1:<port>` 访问网络。该 listener 由 agentx 持有：

- agentx 在内存中持有 attempt 作用域的 `aud=egress-proxy` capability，转发到 egress-gateway
  时才附加 `Proxy-Authorization`；
- 子进程环境里**没有任何 bearer**，只有 loopback 地址与 CA bundle 路径；
- agentx 对该 relay 只做 CONNECT 字节转发、配额与计量，不解密、不解析 TLS 内容。

这条注入路径是既有契约允许的：[D22](../ARCHITECTURE.md#14-关键决策) 已经规定
"stock 可选的 `managedNetwork`、`networkProxy` 不是远端可控字段；需要的本地 proxy 启动信息
只能由 agentx 根据受信 enrollment/runtime policy 注入"。因此 `process-v1` outer profile
无需变更，`shell_mapper` 侧继续固定 `enforceManagedNetwork=true`
（`internal/executorgateway/shell_mapper.go:228,297`），stock 仍然发出
`network/policyRequest` 反向询问，只是在 managed 环境下由 agentx 依受信策略应答 `allow`
并把决定记入审计。

相比"把 capability 放进子进程 env"的简单做法，本方案多一跳 relay，换来两个性质：
凭据不出现在任何可被 `env` 读到的位置，且工作负载 UID 在内核层面就无法直连代理端点。

### 3. egress-gateway 分两种模式：透传放行与选择性凭据注入

egress-gateway 是 agentserver 集群内的新组件，公网 host 独立（`egress.byted.bps.dev`）。

必须先区分两个**不同**的集合，这是本节的核心：

- **需要网络**的 host：`registry.npmjs.org`、`pypi.org`、`crates.io`、发行版软件源……
  它们绝大多数是匿名读取，不需要任何凭据；
- **需要凭据**的 host：`github.com`、`api.github.com` 等，数量少且是显式配置的。

把这两个集合用同一种机制处理是过度设计。因此：

**模式 A — 透传放行（默认）。** 对只需要网络的 host，egress-gateway 只做 CONNECT 层的
host allowlist 与配额，**不终止 TLS**，不看明文。sandbox 与上游之间是端到端 TLS。
这条路径不需要 CA，也不扩大明文可见面。

**模式 B — 选择性凭据注入。** 只对策略中标注了 `binding_id` 的 host 才终止 TLS：

1. 校验 `Proxy-Authorization` 中的 run capability：本地 Ed25519 验签（同
   [ADR 0002](0002-production-run-capability.md) 的 `asv2cap1` 格式与 keyring），
   再对每个请求调用 Core `authorize-egress` 做在线授权。Core 不可达即 fail closed，
   **无正向缓存、无离线降级**，与 llmproxy 完全一致；
2. 用自有 CA 为该 host 签发叶证书并终止 TLS。**CA 私钥只存在于 egress-gateway**，
   sandbox 镜像只安装 CA 公钥。sandbox 因此无法伪造任何站点，也无法从它信任的证书里
   提取出任何可用秘密；
3. 剥离入站请求中的全部认证 header，按绑定注入真实凭据（`Set` 覆盖而非追加，语义与
   `internal/llmproxy/handler.go` 的凭据替换一致）；
4. 以系统信任根重新发起到真实上游的 TLS 连接，正常校验证书链与主机名；
5. **禁止跟随任何 3xx 重定向**（A12 已证明跟随重定向会把凭据送到未配置的 origin），
   请求/响应 header 走白名单，body 不落盘、不记日志。

两种模式都默认拒绝：未命中 allowlist 的 host 一律拒绝，不存在"未配置即放行"。

**模式 B 不一定需要 TLS 终止。** 若目标 CLI 自带业务 API host 覆盖开关，
可以直接把流量指向 egress-gateway（[ADR 0010](0010-tool-packs.md) §3 的 `host_remap`），
此时凭据注入照常进行，但不需要 CA、不需要终止 TLS。**能用 `host_remap` 就不要用 `proxy_mitm`。**
TLS 终止只保留给没有覆盖开关的 CLI，以及 `git clone https://...` 这类 URL 会从 prompt、
submodule 或脚本中任意冒出来、无法靠配置收敛的场景。

对需要凭据的少数 host 选择 TLS 终止而非 v1 的端点重映射（`gh/hosts.yml` + `.netrc` 指向代理），
理由是覆盖面：`git clone https://github.com/...` 这类 URL 会直接出现在用户 prompt、仓库
submodule 和脚本里，端点重映射对它们无效，而重写 remote URL 会污染用户的工作树。
TLS 终止对任意来源的 URL 一致生效。代价是明文可见与一把 CA 私钥，但这个代价被限制在
显式配置的少数 host 上，而不是全部出站流量。

### 4. 凭据存储复用 ADR 0005 已验证的密封模型

新增 `workspace_credential_bindings`：绑定 workspace、provider kind、host、作用域、
状态与单调版本号，secret 用**独立的** AES-256-GCM keyring 密封，AAD 绑定
`workspace/binding/user/version`。支持三种凭据形态：

- **static**：用户粘贴的 PAT/token，直接密封保存；
- **oauth-grant**：Authorization Code + PKCE 授权后保存 refresh token，由 Core 在到期前刷新，
  与 LLM Gateway grant 同构；
- **app-installation**：GitHub App 私钥密封保存，按需铸造 1 小时安装令牌。这是三者中最好的
  形态——权限可按仓库收敛，令牌天然短期，撤销即时生效——应作为 GitHub 的推荐路径。

凭据的 owner 可以是 workspace（团队共享的机器人身份）或个人（用户自己的 grant）。个人绑定
只能在该用户自己发起的 run 上使用，与 ADR 0005 对 LLM grant 的约束相同。

### 5. 审批边界从"执行 shell"移到"产生外部副作用"

BYO executor 上 `shell` 默认 `ask`，因为命令直接作用于用户的真实机器。托管 sandbox 里
`shell` 是隔离且可丢弃的，继续对每条命令弹审批既昂贵又会训练用户盲目点同意。

因此 managed 环境的默认策略调整为：**`shell` 在 sandbox 内可以 `allow`，而通过真实凭据
产生外部副作用需要 `ask`。** egress-gateway 按 `(host, method, path)` 把请求分类为
read/write effect class：

- `GET`/`HEAD` 等读操作按策略放行并计入审计；
- `git push`、`POST /repos/*/pulls`、`npm publish` 等写操作触发既有的 Core approval authority
  （`pending → approved|denied|expired|cancelled → consumed`），获批前请求挂起，超时按拒绝处理。

这让审批发生在语义清晰、用户能看懂的位置（"允许向 org/repo 推送分支吗"），而不是在
"允许执行 `bash -lc ...` 吗"这种用户无法评估的位置。effect class 表必须是版本化的
closed-world 配置，未知路径的写方法默认归为 write。

### 6. 明确不声称的性质

- **sandbox 内任意代码都能使用 loopback 代理。** 无法阻止，也不试图阻止：真正的执行点是
  egress-gateway。缓解手段是 capability 绑定 attempt 且短期、host 默认拒绝、写操作需审批、
  全量审计。凡是策略允许的操作，被提示注入的 agent 同样能做——这是策略设计问题，不是本
  机制能消除的。
- **不防御用户自己的凭据在允许范围内被滥用。** 例如允许了 `github.com` 的写入，就无法阻止
  agent 推送一个内容不好的 commit。这属于凭据最小权限与审批粒度的范畴。
- **不做协议转换。** egress-gateway 只做凭据注入与策略执行，不改写 body、不做 API 适配，
  与 llmproxy"闭合转发器"的定位一致。

## 后果与限制

- 真实凭据的存在范围收敛为 Core（密文）与 egress-gateway（请求期内存），永不进入 sandbox、
  agentx、exec-server 或任何被执行的子进程；数据库泄漏与 sealing key 泄漏必须同时发生
  才能还原凭据。
- 每一次外部 API 调用都天然绑定 `workspace/run/execution`，产生可查询的审计事实。这是相对
  "直接给 sandbox 一个 token"的主要收益，应作为产品能力对外表达。
- run 结束后 capability 失效，sandbox 即失去出站能力。长驻进程在 run 之间无网络，
  这是有意的（阻断后台外传），必须在产品语义中写明。
- egress-gateway 位于所有第三方调用的关键路径上，其可用性直接决定 sandbox 可用性；
  它同时是高价值攻击目标，必须有独立的最小权限、审计与告警。
- CA 私钥是新增的高价值秘密，但其作用范围被 §3 收窄到显式配置了 `binding_id` 的少数 host，
  其余出站流量保持端到端 TLS。轮换流程、sandbox 镜像内 CA bundle 的更新节奏，以及
  "旧镜像信任已轮换 CA"的过渡窗口仍必须在部署门禁中显式处理。
- 首版只支持公网上游，私网/内部服务的访问沿用 ADR 0005 §5 的结论：需要平台管理员维护的
  egress zone，在该能力完成前拒绝。
