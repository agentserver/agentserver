# ADR 0010：工具包（tool pack）是 skill、凭据、出口策略与运行时投影的统一版本化单元

- 状态：Superseded（组合摘要与冻结方案已取消）
- 日期：2026-08-04
- 影响范围：Core、Platform API、egress-gateway、harness-worker（instructions 注入）、sandbox 镜像构建

## 背景

首个目标场景是：用户在 Platform 上配置飞书凭据，session 把飞书 skill 载入 Codex harness，
模型决定调用时由 sandbox 执行 `lark-cli`，沙箱内只持有假凭据，真凭据由
[egress-gateway](0009-sandbox-egress-credential-proxy.md) 替换。后续还要以同样方式接入
`bytecloud-cli` 等。

把这件事拆开会发现它不是一个功能，而是**四件必须同时成立、且互相依赖的配置**：

1. 模型要知道这个 CLI 存在、能做什么、怎么调用 —— 一段只读的声明式 skill 文本；
2. 平台要知道怎么向用户索取并保管这个 CLI 的凭据 —— OAuth 配置或静态密钥规格；
3. 出口网关要知道放行哪些 host、用什么方式把假凭据换成真凭据、哪些请求算写操作 —— 出口规则；
4. sandbox 里真的要有这个 CLI 二进制，并且它的配置文件里要预先写好假凭据 —— 运行时投影。

这四件事任何一件单独变更都会静默破坏整条链路：skill 文本说"用 `lark-cli im send`"，
但镜像里没装 `lark-cli`；或者装了 CLI 但出口规则没放行 `open.feishu.cn`；
或者放行了 host 但注入方式配成了 Bearer 替换，而该 API 实际用的是请求签名。
这些错误都不会在配置时报错，只会在模型真正调用时失败，而且失败点离根因很远。

## 决策

### 1. 四件事捆为一个 pack，共享单调版本号

定义 **tool pack**：一个平台管理的、版本化的、closed-world 的声明单元，同时包含上述四部分。
pack 有单调递增的 `version`；任何一部分变更都必须递增整个 pack 的版本。

run 创建时按 session 当前配置解析 `(packId, packVersion)` 集合。Core 不再计算、存储或校验 pack
集合组合摘要，也不通过摘要 fence run 或 thread。

### 2. pack 的四个部分各自的约束

**skill（声明式文本）。** 必须是纯只读文本，这是既有硬约束——
[`ARCHITECTURE.md`](../ARCHITECTURE.md) §7.2 规定"skills/context 只能是只读的声明性提示与配置；
引用本地脚本、stdio MCP 或其他可执行载荷的 skill 必须被拒绝"。因此 pack 的 skill 部分
不得包含脚本、不得声明 MCP server、不得引用可执行文件路径作为载荷。

注入路径是 `thread/start` 的 `baseInstructions` / `developerInstructions`
（`internal/harnessworker/runner.go:246-247` 已有 wire 字段，当前仅测试使用）。
**明确不使用 stock Codex 自带的 skills 机制**——`[skills.bundled]`、`skill_search`、
`skill_mcp_dependency_install` 在 harness runtime 配置中是被显式关闭的
（`internal/harnessworker/local_runtime.go:354-382`），那是能力隔离的一部分，不能为了
装载 skill 把它打开。

**凭据规格。** 声明 `auth_type` 与其参数。Core 据此生成 Platform 上的配置与授权入口，
并复用 [ADR 0005](0005-workspace-llm-gateway-oidc.md) 已验证的密封与刷新模型：
配置由 owner 维护，grant 由每个成员各自授权自己的账号。这一点对飞书尤其重要——
消息应当以真实用户身份发出，而不是一个共享机器人身份。

**出口规则。** 声明允许的 host 集合，以及每个 host 上的注入方式与 effect class 分类规则。
它直接编译成 egress-gateway 的策略（[ADR 0009](0009-sandbox-egress-credential-proxy.md) §3/§5）。

**运行时投影。** 声明该 pack 需要 sandbox 镜像中的哪个二进制（绝对路径 + digest）、
启动时要写入哪些占位配置文件（模板）、要设置哪些环境变量。占位配置与环境变量由
**agentx 按受信运行时策略注入**，不经模型、不经 MCP 参数——这是 [D22](../ARCHITECTURE.md#14-关键决策)
已经规定的边界（"需要的本地 proxy 启动信息只能由 agentx 根据受信 enrollment/runtime policy 注入"）。

### 3. 拦截方式与注入方式是两个独立的轴

早期草稿把这两件事混为一谈，导致得出"必须做全量 TLS 终止"的过度设计。它们是正交的：
**拦截**决定流量怎么到达网关，**注入**决定凭据怎么被替换。

**拦截轴（`interception`）**，由 pack 的运行时投影声明：

| 模式 | 做法 | 前提 |
|---|---|---|
| `host_remap` | 用 CLI 自带的 host 覆盖开关把业务流量直接指向 egress-gateway | CLI 提供该开关，且它只影响业务流量、不影响认证链路 |
| `proxy_mitm` | 走 loopback 代理 + 网关自有 CA 终止 TLS | CLI 没有覆盖开关，或 URL 由用户/仓库任意提供 |

**优先 `host_remap`**：不需要 CA、不需要终止 TLS、不引入明文可见面，是严格更简单也更安全的路径。
只有当目标 CLI 没有可靠的 host 覆盖能力，或者要拦截的是 `git clone https://...` 这类
会从 prompt、submodule、脚本里任意冒出来的 URL 时，才使用 `proxy_mitm`。

**注入轴（`injector`）**，由 pack 的出口规则逐 host 声明：

| injector | 语义 | 适用 |
|---|---|---|
| `none` | 只做 host allowlist 与配额 | 匿名读取的镜像源、文档站 |
| `bearer_swap` | 剥离入站 `Authorization`，换成真实 Bearer | OAuth/PAT 类 API |
| `header_set` | 写入指定名字的自定义认证 header | 使用非标准认证 header 的 API |
| `resign` | 代理持真实密钥，按供应商算法**重新签名整个请求** | AK/SK、HMAC-over-body 类 API |

`resign` 是唯一一种"真正按供应商计工作量"的注入方式：签名覆盖 body 时，换密钥就必须重算签名，
需要在网关内实现该供应商的规范化与签名算法，并单独做安全评审。

**首版只实现 `none`、`bearer_swap` 与 `header_set`。** 已实测的两个目标 CLI 都不需要 `resign`：
飞书用 `Authorization: Bearer`，ByteCloud 业务请求只带单个 `X-Jwt-Token`（其 AK/SK 仅用于
换取该 JWT，不参与逐请求签名）。`resign` 仍保留在契约中，因为它是真实存在的一类
（部分云厂商 OpenAPI 确实如此），接口留出后未来接入不必改动整个策略模型——
但不应再把它当作这个抽象存在的主要理由。

### 4. pack 是平台管理的，不是 workspace 自由编辑的

pack 的定义（尤其是出口 host 与注入方式）由平台管理员维护并随版本发布；
workspace 只能**启用/禁用**已发布的 pack，并授权自己的凭据。

理由与 [ADR 0008](0008-managed-sandbox-executor.md) §3 拒绝让 workspace 注册 sandbox pool 相同：
出口 host allowlist 是安全边界，让 workspace 用户自由添加等于把 SSRF 与数据外泄的决策权
交给最终用户；而运行时投影引用的二进制必须与镜像 digest 对齐，本来就不是 workspace 能提供的。

后续若要支持 workspace 自定义 pack，必须先有独立的审核流程与受限的 host 分类，不在首版。

### 5. skill 集合不使用组合摘要绑定

`thread/start` 发送实际 skill 文本；run manifest 与 checkpoint 不携带 pack 集合摘要。恢复逻辑不再进行
pack 摘要 exact-match，也不会因为摘要变化创建新 thread。

## 后果与限制

- 接入一个新 CLI 的工作量被收敛为"写一个 pack"；不再增加组合摘要、摘要迁移或摘要 fencing。
- `resign` 类 pack 的接入成本显著高于 `bearer_swap`，且要求 egress-gateway 持有真实签名密钥
  并实现供应商算法。这类 pack 必须逐个做安全评审，不能视为配置工作。
- pack 的运行时投影与 sandbox 镜像强耦合：新增 pack 通常意味着重建镜像并递增 digest。
  这使 pack 发布与镜像发布必须联动，需要在发布流程中显式建模，不能各自为政。
- 平台管理 pack 意味着平台承担了"这个 CLI 的出口策略是否正确"的责任。分类错误
  （把写操作误判为读）会绕过审批，因此 effect class 规则必须有测试覆盖并纳入评审。
