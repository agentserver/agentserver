# 首个垂直切片：飞书 CLI 经托管 sandbox 与出口凭据替换

> 历史切片：保留现有 Lark skill、`shell(argv=["lark-cli", ...])` 和 zero-secret 验收事实；拓扑、
> egress 数据面与实施顺序以 [`TAE_MANAGED_EXECUTOR.md`](TAE_MANAGED_EXECUTOR.md) 和
> [ADR 0012](adr/0012-unified-execution-gateway-tae-backend.md) 为准。本文下方的 per-user OAuth grant、
> sandbox 内 agentx、MITM proxy 和独立 credential data plane 均为被取代的历史推演，不能据此实现或部署；
> workspace credential 的 canonical 决策见 [ADR 0013](adr/0013-core-owned-workspace-credentials.md)。

> 状态：实施方案（draft）
>
> 本文把 [ADR 0008](adr/0008-managed-sandbox-executor.md)（托管 sandbox）、
> [ADR 0009](adr/0009-sandbox-egress-credential-proxy.md)（出口凭据替换）与
> [ADR 0010](adr/0010-tool-packs.md)（工具包）收敛到一个可验收的目标上，
> 并明确它如何扩展到 `bytecloud-cli` 等后续 pack。
>
> 通用机制见 [`MANAGED_SANDBOX.md`](MANAGED_SANDBOX.md)；本文只写这个切片的增量与验收。

## 1. 验收定义

只有下面七条同时成立才算完成。每条都必须有自动化证据，不接受人工演示。

| | 验收项 |
|---|---|
| **M1** | workspace owner 在 Platform 创建/轮换/选择 `kind=lark` credential binding；Core 密封保存，Platform 只能看到 metadata |
| **M2** | session 启用飞书 pack 后，run 启动时 `thread/start` 的 `baseInstructions` 实际包含飞书 skill 文本，且 `pack_set_digest` 冻结进 run manifest |
| **M3** | 模型自主决定调用飞书能力时，发出的是 `shell` 工具调用，落到该 session 的托管 sandbox 上执行 |
| **M4** | sandbox 内 `lark-cli` 使用**占位凭据**发出请求；沙箱内不存在任何真实飞书 token |
| **M5** | TAE Agent Gateway 调 egress-authorizer，后者经 Core 取得一次性 header mutation；飞书 OpenAPI 返回成功，结果回到模型 |
| **M6** | 全链路审计闭合：execution/operation/target generation + credential-use/egress decision；写能力未来另加审批 |
| **M7** | 对 sandbox 做全面扫描（env、`/proc/*/environ`、文件系统、CLI 配置、stdout/stderr、rollout、checkpoint）**零真实凭据命中** |

M7 是这套设计存在的理由，权重最高。M4 与 M7 合起来才是"凭据不进沙箱"的完整证明：
前者证明请求确实带的是假凭据，后者证明真凭据无处可寻。

### 1.1 一个关键的省力结论

**这个切片不需要改动模型可见的工具目录。** 当前 `executor-mcp/1.1` 的
`list_environments | shell | read_file`（`internal/executorgateway/mcpcontract/catalog.go:9-18`）
已经够用：模型调 `shell`，argv 为 `["lark-cli", "im", "send", ...]`。

因此 `tool_catalog_digest` 不变，不触发存量 thread 的目录迁移，也不需要重跑 A03/A09 这类
围绕模型工具面的门禁。切片的全部工作落在 Core、egress-gateway、sandbox 镜像和 instructions
注入上——这几处都不在 stock Codex 的能力隔离边界上，验收面小得多。

（`pack_set_digest` 变更仍会要求启用 pack 的 session 开新 thread，但那是用户显式操作的结果。）

## 2. 端到端时序

```text
【配置期，Platform】
owner: 注册 lark pack 凭据配置（应用 client_id / scopes / 回调）
成员:  POST .../tool-packs/lark:authorize → 飞书 OAuth 授权页 → 回调
       → Core 交换 token，密封保存 (workspace, pack, user) 的 grant

【会话期】
session 启用 lark pack
CreateRun → Core 同一事务冻结:
    LLM gateway 绑定（既有） + pack 集合 {lark@v3} 与 pack_set_digest（新增）
    + sandbox profile（新增）
  → outbox: run.queued（既有） + sandbox.provision（新增，若无就绪 sandbox）

【运行期】
harness-pool 领 run → 从 Core 取 pack 的 skill 文本 → 放入签名 run manifest
  → worker 经 FD 3 收到 → thread/start(baseInstructions=飞书 skill 文本, dynamicTools=冻结目录)
  → 模型看到"可以用 lark-cli"，决定调用
  → item/tool/call: shell(argv=["lark-cli","im","send",...], env_id=<sandbox env>)
  → worker → MCP tools/call → executor-gateway
       PrepareExecution → 策略 → （写操作时）审批
  → WSS → sandbox 内 agentx → stock exec-server → 启动 lark-cli
       agentx 按受信运行时策略注入: HTTPS_PROXY=127.0.0.1:P、CA bundle、
                                    lark-cli 配置目录（内含占位凭据）
  → lark-cli → HTTPS 请求 open.feishu.cn
  → agentx loopback 代理（附加 aud=egress-proxy capability）
  → egress-gateway:
       capability 验签 → Core authorize-egress（逐请求）
       → host 命中 lark pack 规则（injector=bearer_swap）
       → 自有 CA 终止 TLS → 剥离占位凭据 → 注入 Core 解封/刷新后的真实 user token
       → 系统信任根重连 open.feishu.cn（禁重定向）
  → 响应回流 → lark-cli 输出 → shell 结果 → MCP result → 模型
```

## 3. 逐组件工作分解

### 3.1 Core

- **pack 注册表与 grant**：`tool_packs`、`tool_pack_versions`、`workspace_tool_pack_enablements`、
  `workspace_tool_pack_grants`（密封 token set）、`tool_pack_auth_transactions`。
  grant 的状态机、密封与刷新**直接复用** ADR 0005 的模型：
  `internal/coreserver/workspace_llm_gateway_service.go` 已有
  `BeginAuthorization`/`CompleteAuthorization`/`RevokeGrant`/`refreshGrant`/`sealTokenSet`/`openTokenSet`
  的完整生命周期，`llm_gateway_sealer.go:23-62` 已有域分离的 AAD 与 keyring overlap。
- **必须做的一处抽象**：现有实现绑定在 **OIDC discovery + ID token 验签**上
  （`llm_gateway_oidc.go` 的 `Discover`/`verifyIDToken`）。飞书是自定义 OAuth2，
  不保证有 `.well-known/openid-configuration`。因此要把 provider 抽成接口
  （`AuthorizationURL` / `Exchange` / `Refresh`），保留 OIDC 实现，新增飞书实现。
  这是把 ADR 0005 的机制从"OIDC 专用"泛化为"凭据 grant 通用"的必要一步，
  也是后续所有 pack 的共用底座。
- **run launch 冻结**：`run_launch_states` 增加 pack 集合与 `pack_set_digest`。
- **新内部路由 `authorize-egress`**：只接受 egress-gateway 的 SPIFFE identity，
  在同一只读快照内复核 run/attempt/lease/generation/membership/pack 版本/grant 状态，
  然后刷新或解封 bearer，返回**仅供 egress-gateway 的**响应（`Cache-Control: no-store`）。
  secret 字段不得混入 executor 共用的授权响应——这是 ADR 0005 §3 已经定下的规矩。
- **权限**：按既有命名新增 `tool-packs:read|enable|disable`、
  `tool-pack-grants:authorize|revoke`。角色编译沿用现有划分：owner 拿全部，
  developer 拿 `read` 与自己的 grant 授权/撤销（对照 `AUTHORIZATION.md` 第 4/6 节的
  `llm-gateways:*` 与 `llm-gateway-grants:*`）。

### 3.2 harness 侧（唯一改动点：instructions 注入）

- run manifest 增加 pack 集合与 skill 文本的 object pointer + `pack_set_digest`
  （`api/schema/run-manifest.schema.json` 的 manifest 定义；大文本走对象存储 pointer，
  与 `prompt` 同样由 pool 按签名 size/SHA-256 双端校验，不进 argv/env）。
- worker 在 `thread/start` 填入 `baseInstructions` / `developerInstructions`
  （`internal/harnessworker/runner.go:246-247` 的 wire 字段已存在，目前仅测试使用）。
- `previousCheckpoint` 并列增加 `packSetDigest`；恢复时不一致即 fail closed 并开新 thread
  （ADR 0010 §5）。
- **明确不做**：不打开 stock Codex 自带的 skills 机制。
  `[skills.bundled]`、`skill_search`、`skill_mcp_dependency_install` 在
  `internal/harnessworker/local_runtime.go:354-382` 中被显式关闭，那是能力隔离的一部分。

### 3.3 egress-gateway

首版只需实现两种 injector：`none`（透传放行）与 `bearer_swap`（飞书用）。
`resign` 留接口不实现（ADR 0010 §3）。

其余是 ADR 0009 已定的公共能力：capability 本地验签 + 逐请求 Core 在线授权、
host allowlist、自有 CA 的选择性 TLS 终止、禁重定向、header 白名单、effect class 分类与审批、
`egress_audit_events` 写入与配额。SSRF 校验直接复用 `internal/publichttps`
（`ValidateURL`/`IsPublicAddress`/`ForbiddenPrefixes`/`NewClient`）。

### 3.4 sandbox 镜像

toolchain 层新增 `lark-cli`（绝对路径 + digest 锁定）。runtime 层（agentx、pinned Codex、
bwrap、tini、egress CA）不受影响——这正是 [`MANAGED_SANDBOX.md`](MANAGED_SANDBOX.md) §4.1
把两棵树分开的价值：加一个 CLI 不需要重开 runtime 门禁。

### 3.5 agentx（独立仓库）

在 managed 模式的受信运行时策略中，按 pack 的运行时投影注入子进程环境：
`HTTPS_PROXY` 指向 loopback、CA bundle 路径、CLI 配置目录（内含占位凭据文件）。

这条注入路径是 [D22](ARCHITECTURE.md#14-关键决策) 明确允许的（"需要的本地 proxy 启动信息
只能由 agentx 根据受信 enrollment/runtime policy 注入"），因此
`process-v1` outer profile 与 `shell-v1` 的 clean-env 约定都不需要变更——
注入的是 agentx 本地策略产物，不是模型或远端可控字段。

## 4. lark pack 的定义

按 ADR 0010 的四部分：

**skill**：声明式 markdown，说明可用的 `lark-cli` 子命令、参数形态与典型用法。
纯文本，不含脚本、不声明 MCP server、不引用可执行载荷。

**凭据规格**：`auth_type = oauth2`（设备码授权），保存 `user_access_token` + refresh token。
grant 按 `(workspace, pack, user)` 保存，**每个成员授权自己的飞书账号**——
这样消息以真实用户身份发出，保留飞书侧的审计与撤销语义，与 ADR 0005 拒绝"平台统一 key"
的理由一致。

**出口规则**：允许 `open.feishu.cn`，injector 为 `bearer_swap`（替换 `Authorization: Bearer`）；
按 method/path 分类 effect class（查询类 `GET` 为 read；发消息、改文档这类为 write，需审批）。

**运行时投影**：`lark-cli` 的绝对路径与 digest；以**环境变量**形式注入的占位 token；
不写任何本地登录态文件。`interception` 暂定 `proxy_mitm`——目前**没有证据**表明 `lark-cli`
提供业务 API host 的覆盖开关（`auth login --domain` 是能力域如 im/docs，不是 API 主机），
S-L0 需确认；若存在这样的开关则改用更简单的 `host_remap`。

### 4.1 已实测确认的事实

以下来自对本机 `@larksuite/cli` v1.0.69 二进制的取证（飞书官方开源 CLI，
`github.com/larksuite/cli`），可作为 pack 定义的依据：

| 项 | 事实 | 对设计的影响 |
|---|---|---|
| 认证头 | `Authorization: Bearer <token>`；`AWS4-HMAC`/`CanonicalRequest`/`sigv4` 命中数**全为 0** | `bearer_swap` 成立，网关不需要签名算法 |
| 主域 | `open.feishu.cn`（二进制内 584 次命中）；国际版 `open.larksuite.com`/`open.larkoffice.com` | 出口 allowlist 的 host 集合 |
| token 类型 | `user_access_token`（用户身份，新版为 JWT 形态）、`tenant_access_token`（应用身份，`t-` 前缀） | 选 user token，保留逐用户审计 |
| 有效期 | user 约 6900 秒；tenant/app 最长 2 小时 | Core 后台刷新的周期依据 |
| 刷新端点 | `/open-apis/authen/v1/refresh_access_token`、`/open-apis/authen/v2/oauth` 等 | §5 的关键风险点 |
| **非交互注入** | 支持 `LARKSUITE_CLI_USER_ACCESS_TOKEN` / `LARKSUITE_CLI_TENANT_ACCESS_TOKEN` 环境变量 | **占位凭据可以纯环境变量注入，沙箱内不需要任何登录态文件或 keychain** |
| 能力域 | 22 个（im、docs、sheets、base、calendar、task、wiki 等） | skill 文本的覆盖范围 |

最后一行是本节最有价值的发现：它把"运行时投影"从"渲染配置文件模板 + 处理 keychain"
简化成"设一个环境变量"，同时消除了一整类失败模式（配置格式漂移、文件权限、
keychain 在容器内不可用）。

> **仍需在目标镜像中复核（S-L0）**：上述事实来自本机 macOS arm64 上的该版本。
> sandbox 镜像使用的是 `linux/amd64` 且版本会随时间漂移，pack 定义锁定的版本必须在
> 目标镜像内重跑同一组断言，尤其是环境变量名与 host 集合。

## 5. 占位凭据与 token 刷新（这一节是本切片最大的实现风险）

**基本方案**：用环境变量 `LARKSUITE_CLI_USER_ACCESS_TOKEN` 注入占位 token，
其值就是那枚绑定 attempt 的 `aud=egress-proxy` capability——它离开 egress-gateway 一文不值、
随 run 结束失效、每次使用都被审计。沙箱内不写任何登录态文件、不碰 keychain。

真实 token 的刷新**完全由 Core 在后台完成**（复用 ADR 0005 的 `refreshGrant`：
bearer 到期前刷新，失败则 grant 进入 `reauth_required` 并 fail closed）。沙箱内的 CLI
既没有 refresh token 也不知道真实 token，因此它根本没有刷新的能力——这正是我们要的。

**但实测暴露了两类会让请求在到达网关之前就失败的行为，必须逐一验证：**

1. **CLI 的本地自检。** `lark-cli` 有 `auth status --verify`、`whoami` 这类会在本地判定
   凭据有效性的路径；ByteCloud CLI 有 `ByteCloud Auth is not initialized on this machine`
   这类前置阻断。如果 CLI 在发业务请求前先做本地校验，假凭据会让它自行判定失效，
   业务流量根本不会产生。
2. **token 的结构校验。** 飞书新版 `user_access_token` 是 JWT 形态。如果 CLI 会解析它的
   结构或 `exp` 字段，一个随机的 capability 字符串会被本地拒绝。

**对应的处理顺序**（从最省事到最重）：

- 先验证 CLI 是否把环境变量里的 token 当作**不透明字符串**原样透传。若是，直接可用；
- 若 CLI 会做结构/过期校验，则把占位 token 构造成**结构合法、`exp` 为远期**的形态
  （内容仍是无意义的、只对网关有效的标识）；
- 只有当 CLI 仍然坚持走刷新或认证端点时，才让 egress-gateway 一并接管这些端点并返回
  合成响应。这会引入"代理伪造 OAuth 响应"这一额外行为，需要单独评审，是最后手段。

这三档必须在 S-L0 里用真实 CLI 跑通并确定走哪一档，因为它直接决定占位凭据的构造方式，
而构造方式又写死在 pack 的运行时投影里。

## 6. 审批与 effect class

沿用 ADR 0009 §5 的边界迁移：sandbox 内执行 `shell` 本身可以 `allow`（隔离且可丢弃），
而**通过真实凭据产生外部副作用需要 `ask`**。

对飞书这意味着：查询类调用直接放行并记审计；发消息、修改文档这类触发审批，
用户看到的是"允许以你的身份向某个会话发送消息吗"——一个他能真正评估的问题，
而不是"允许执行 `bash -lc lark-cli ...` 吗"。

分类规则是 pack 的一部分、随 pack 版本化，且**必须有测试覆盖**：
把写操作误判为读会直接绕过审批，这是本设计里分类错误代价最高的地方（ADR 0010 后果节）。

## 7. 扩展到 bytecloud-cli 需要什么

### 7.1 凭据形态：比预想的简单

对本机 `@bytedance-dev/bytecloud-cli` v0.0.64 二进制的取证与 `--dry-run` 实测显示，
**它不是 AK/SK 请求签名**：

- 业务请求的唯一鉴权头是 `X-Jwt-Token`，没有 body 摘要头、时间戳头或签名头；
- `AWS4-HMAC-SHA256`、`SignedHeaders`、`CanonicalRequest`、`StringToSign`、`X-Amz-Signature`
  命中数**全为 0**；
- `--dry-run` 直接输出可原样重放的 `curl`——若签名覆盖 body，重放/改写必然失效，
  这种设计不可能存在。

AK/SK 确实存在（`BYTECLOUD_AUTH_ACCESS_KEY_ID` / `BYTECLOUD_AUTH_SECRET_ACCESS_KEY`），
但只用于**换取那个 JWT**，不参与逐请求签名。因此它属于 `header_set`，
egress-gateway 不需要实现任何签名算法。

它还提供 `BYTECLOUD_CLI_CLOUD_HOST` 覆盖业务 API 域名，且按其文档只影响业务出流量、
不影响认证链路——这正是 ADR 0010 §3 `host_remap` 拦截模式的理想目标，比 TLS 终止更省事。

### 7.2 真正的障碍：它是内网端点

`--dry-run` 生成的重放命令形如 `curl -k --resolve 'cloud.bytedance.net:443:10.27.206.255'`
——目标解析到 **RFC1918 私网地址**。

这与两条既有约束直接冲突：[ADR 0009](adr/0009-sandbox-egress-credential-proxy.md) 后果节的
"首版只支持公网上游"，以及 [ADR 0005](adr/0005-workspace-llm-gateway-oidc.md) §5 的 SSRF 规则
（拒绝 literal IP、loopback、RFC1918、ULA、CGNAT、metadata、cluster 地址，
Kubernetes egress 只开放公网 TCP/443 并用 `ipBlock.except` 排除私网）。

**所以接入 bytecloud 不是"写一个 pack"，它需要先建设 ADR 0005 §5 结尾预留但推迟的那项能力：
平台管理员维护的 egress zone**——按 zone ID 选择受审计的出口，让特定 pack 能在受控前提下
访问指定内网目的地，而不是把私网访问权放给 workspace 用户。这是一项独立的、需要单独安全评审的
基础设施工作，必须排在 bytecloud pack 之前。

另外那条 `curl -k` 说明该 CLI 对证书宽容：一方面 MITM 插入网关的阻力更小，
另一方面固定 IP 解析意味着单靠 DNS 劫持可能拦不住流量，接入时要用 `--dry-run` 复核
真实 Go client 是否也做 IP pin。

### 7.3 任何新 pack 的接入分诊

| 维度 | 取值 | 成本 |
|---|---|---|
| **拦截** | CLI 有 host 覆盖开关 → `host_remap` | 配置级，无需 CA |
| | 无覆盖开关或 URL 任意 → `proxy_mitm` | 需 CA 与 TLS 终止 |
| **注入** | Bearer / PAT → `bearer_swap` | 配置级 |
| | 自定义认证 header → `header_set` | 配置级 |
| | AK/SK 或 HMAC-over-body 签名 → `resign` | 需实现签名算法 + 单独安全评审 |
| | mTLS 客户端证书 | 需扩展连接层 |
| | 凭据用于非 HTTP 协议或本地加密 | 不适用本方案 |
| **网络** | 公网上游 | 首版即可 |
| | **内网上游** | **需先建设 egress zone 能力** |
| **本地自检** | CLI 是否在发请求前本地校验凭据（见 §5） | 决定占位凭据的构造方式 |

已实测的两个目标都落在最省事的一档（注入维度都是配置级、都不需要 `resign`），
但 bytecloud 卡在"网络"这一行。这张表应作为未来任何 pack 接入的第一道分诊——
**注意最容易低估的不是签名，而是网络可达性与 CLI 的本地自检行为。**

## 8. 门禁

在 [`MANAGED_SANDBOX.md`](MANAGED_SANDBOX.md) §9 的 S 系列之外，本切片新增：

- **S-L0 CLI 行为核查**（前置）：在**目标 `linux/amd64` 镜像内**用锁定版本的 `lark-cli` 确认
  §4.1 的断言（环境变量名、host 集合、认证头），并判定 §5 的三档占位凭据方案走哪一档——
  即 CLI 是否把环境变量 token 当作不透明字符串透传、是否做结构/过期校验、
  是否在发业务请求前本地自检。同时确认是否存在业务 API host 覆盖开关以决定
  `interception` 取 `host_remap` 还是 `proxy_mitm`。这是写 pack 定义的前置条件。
- **S-L1 grant 生命周期**：授权、刷新、撤销、`reauth_required`；provider 抽象后
  OIDC 与飞书两种实现都通过同一套状态机测试。
- **S-L2 skill 注入与冻结**：`thread/start` 实际携带 skill 文本；`pack_set_digest` 进入
  manifest 与 checkpoint；摘要不一致时 resume fail closed 并开新 thread。
- **S-L3 端到端替换**：M3→M5 全链路；断言飞书侧收到真实 token、沙箱侧发出的是 capability、
  3xx 不被跟随、header 白名单生效。
- **S-L4 零凭据泄漏**（= M7）：沿用 A11 的扫描方法论，覆盖 env、`/proc/*/environ`、
  文件系统、CLI 配置、stderr、rollout 与 checkpoint。
- **S-L5 审批分类**：写操作触发审批且只执行一次；拒绝/过期/取消路径零副作用；
  effect class 规则表有正负用例。

## 9. 里程碑

| 里程碑 | 内容 | 依赖 |
|---|---|---|
| **L0** | S-L0 事实核查；S-01 剩余实测（Kata guest 内逐项验证）；**agentx managed profile 设计与安全评审** | S-01 的选型调查已闭合：排除 gVisor，选 Kata/Firecracker（[ADR 0008 §7](adr/0008-managed-sandbox-executor.md)） |
| **L1** | 托管 sandbox 供应链路跑通（无外网） | L0 的 S-01 |
| **L2** | Core pack 注册表 + grant provider 抽象 + Platform 配置/授权入口（M1） | L0 的 S-L0 |
| **L3** | skill 注入链路（M2） | L2 |
| **L4** | egress-gateway：`none` + `bearer_swap` + 审计（M4、M5、M7） | L1、L2 |
| **L5** | effect class 与审批（M6） | L4 |
| **L6** | 端到端验收（M1–M7 全绿）+ 用同一套机制接入第二个 pack 验证扩展性 | 全部 |

**L0 必须先做完**：`S-01` 的选型部分已闭合，但它派生出的 **agentx managed profile** 是一项
原不在计划内的跨仓工作，且 Kata 所需的 KVM/嵌套虚拟化是新的部署前提；
`S-L0`（飞书认证的真实形态）可能改变凭据替换的机制。在这两个结论出来之前动手实现
L1–L5，返工风险很高。
