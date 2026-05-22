<h1 align="center">agentserver</h1>

<p align="center">
  <strong>你的个人算力网 —— 随时随地，在微信聊天窗口指挥分布在世界各地的设备。</strong>
</p>

<p align="center">
  <a href="README.md">English</a> &nbsp;·&nbsp; 简体中文
</p>

<p align="center">
  <a href="https://agent.cs.ac.cn"><img src="https://img.shields.io/badge/立即体验-agent.cs.ac.cn-blue?style=for-the-badge" alt="立即体验"></a>
</p>

<p align="center">
  <a href="https://github.com/agentserver/agentserver/actions"><img src="https://github.com/agentserver/agentserver/actions/workflows/build.yml/badge.svg" alt="Build"></a>
  <a href="https://github.com/agentserver/agentserver/blob/main/LICENSE"><img src="https://img.shields.io/github/license/agentserver/agentserver" alt="License"></a>
  <a href="https://github.com/agentserver/agentserver/releases"><img src="https://img.shields.io/github/v/release/agentserver/agentserver" alt="Release"></a>
</p>

---

<p align="center">
  <img src="assets/screenshot-1.png" alt="agentserver Web 控制台" width="800">
</p>
<p align="center">
  <img src="assets/screenshot-2.png" alt="agentserver 编程智能体" width="800">
</p>

> 📖 完整愿景请见：[Overview of agentserver](Overview%20of%20agentserver.pdf)（演示稿，2026 年 4 月）

**agentserver** 把你散落在生活各处的笔记本、台式机、云端沙箱乃至手机，组装成 **同一张个人算力网**：一个统一的工作区，你可以通过浏览器、CLI、Jupyter notebook 或微信聊天窗口去指挥它。每台设备运行一个编程智能体（codex、opencode 或 Claude Code），agentserver 是把它们注册起来、托管凭证、路由请求的控制平面，让你（和你的协作者）从一个入口驱动所有设备。

它回答了 Addy Osmani 提出的一个问题：从 L1（不用 AI）走到 L8（自建编排器）的路径\*。当你同时管理 10+ 个跨设备的智能体时，你已经不再是 *指挥者*（conductor），而是 *编排者*（orchestrator）。agentserver 就是这一层编排底座。

<sub>\* Addy Osmani，Google · Gemini & Cloud AI 总监 —— <a href="https://talks.addy.ie/oreilly-codecon-march-2026">talks.addy.ie/oreilly-codecon-march-2026</a></sub>

### 它和现有工具有何不同

| 工具 | 本地多智能体 | 云端沙箱 | 跨设备组网 | 聊天软件通道 |
|------|:---:|:---:|:---:|:---:|
| OpenClaw / Claude Code Remote | 单实例 | — | — | — |
| Claude Code on the web | — | ✅ | — | — |
| Claude Code Agent Teams | — | ✅（子智能体） | — | — |
| **agentserver** | **✅ 多实例** | **✅** | **✅** | **✅（微信 / Telegram）** |

## 为什么选择 agentserver？

- **口袋里就能指挥算力** —— 通过微信 / Weixin 或 Telegram 聊天驱动你的智能体，离开桌面时也不必再打开终端。
- **一个工作区，统管所有设备** —— 云端沙箱、本地笔记本/台式机、IM 接入的智能体共享同一份工作区注册表，全部并排出现在 Web UI 中。
- **本地穿透，无需公网 IP** —— 本地运行的 opencode / Claude Code / codex 通过 WebSocket 反向连接 agentserver，自动以"沙箱"的身份出现在控制台。不用配端口转发，不用借助第三方隧道。
- **沙箱可暂停、可恢复** —— 每任务一容器，空闲自动暂停；单机用 Docker，集群用 Kubernetes + [Agent Sandbox](https://github.com/kubernetes-sigs/agent-sandbox) + gVisor。
- **同时欢迎"古法编程"** —— 内置 Jupyter notebook 环境，让偏好亲自写代码的用户也能接入同一个工作区，使用智能体所用的文件系统与凭证。
- **多人协作** —— 邀请朋友或同事一起进入你的个人算力网；基于角色的访问控制（owner / maintainer / developer / guest）决定谁能做什么。
- **凭证 & LLM 代理** —— 沙箱永远不接触真实的厂商密钥；每工作区的 RPD 配额与用量统计在服务端强制执行。
- **支持 SSO** —— GitHub OAuth 及通用 OIDC（Keycloak、Authentik 等）。
- **部署方式自由** —— 直接使用托管实例 [agent.cs.ac.cn](https://agent.cs.ac.cn)，或自托管：预编译二进制、Homebrew、Docker Compose、Helm 任选。

## 托管实例使用指南（共 7 步）

最快感受 agentserver 的方式，是直接使用托管实例 **[agent.cs.ac.cn](https://agent.cs.ac.cn)**。自托管用户可在自有域名下走完全相同的流程。

**1. 注册账号。** 访问 [https://agent.cs.ac.cn](https://agent.cs.ac.cn) 完成个人算力网账号注册。

**2. 选购或绑定大模型账号。** 在平台中绑定你自有的 ChatGPT / Anthropic / API Key，或选择平台提供的托管模型账号。

**3. 把设备接入算力网。** 在每台希望加入的设备上安装 codex（或 opencode）—— 笔记本、台式机、家庭服务器、云主机都可以：

```bash
# macOS
brew install codex

# 其他操作系统
npm i -g @openai/codex
```

在 Web UI 中生成接入凭证，复制到设备上运行。建议放在 `tmux`、`systemd` 等 detached 会话中，确保用户注销后智能体仍然在线。完成后，设备会作为一个沙箱出现在你的工作区中。

**4. 选定"指挥机"。** 选一台日常用的设备作为指挥机（通常是你的主力笔记本），把它的接入凭证粘贴到该机器，它就拥有了向算力网中其他设备分派任务的能力。

**5. （可选）创建 Jupyter 编程接口。** 不想全程用 AI，喜欢手写代码？在 Web UI 中开启一个 notebook 环境：每个 kernel 都已预注入 `ctx`，可直接访问与智能体相同的文件系统、凭证与工具。我们称之为 **"古法编程"** ——同一个工作区，是否引入 LLM 完全由你决定。

**6. 接入微信个人账号。** 在平台扫码绑定你的个人微信，把对应智能体切换到 codex 模式后，你就可以直接在任意微信聊天中用自然语言下达指令，由对应设备执行。这是 agentserver 的招牌能力：**手机有信号的地方，就有你的算力。**

**7. 开展多人协作。** 把朋友或同事加入工作区，共享设备、沙箱与凭证，并按角色控制权限。

## 路线图：三个阶段

agentserver 的演进分为三个阶段。完整的图示与论证见 [Overview of agentserver.pdf](Overview%20of%20agentserver.pdf)。

| 阶段 | 主题 | 状态 | 交付物 |
|-------|-------|:---:|------------|
| **1** | 编程智能体的 `code-server` | ✅ 已上线 | 沙箱编排、智能体注册表、凭证 / LLM 代理、智能体接入网关、Web 控制台 |
| **2** | OpenClaw 的浮现 | 🚧 进行中 | NanoClaw（沙箱化的 Claude Code）、`imbridge`（微信 / Telegram）、智能体消息总线 |
| **3** | 中心化的 Agent Loop | 🔭 设计中 | 无状态 `cc` 工作进程池、`cc-broker` 编排器、工具路由、持久化记忆 / 上下文存储、智能体收件箱 |

### Stage 3 的核心洞察

- **无状态 Harness** —— 把 *大脑*（Claude + harness）与 *双手*（沙箱与工具）解耦。会话是 append-only 的事件日志，活在上下文窗口之外。Worker 是 *牛，不是宠物* ——一个 worker 在 turn 中挂掉不会丢任何东西。
- **云-本地混合 Mesh** —— 云端与本地智能体共享同一份工作区注册表。通过 agent card 进行发现；LLM 选工具，工具路由器决定调用落到哪台机器。*要的是 agent 发现，不是网络 mesh。*
- **基于收件箱的异步协作** —— 智能体通过持久化存储中的收件箱互相交接工作。发件时收件方可以不在线。**收件箱就是事实来源。**

## 架构

当前部署形态（Stage 1，Stage 2 服务陆续上线中）：

```
                  外部世界 (Anthropic、OpenAI、GitHub …)
                          ▲
                          │ 出口流量
              ┌───────────┴────────────┐
              │  credentialproxy /     │
              │  llmproxy (:8081)      │
              │  • 凭证注入             │
              │  • RPD 配额 / 用量      │
              └───────────┬────────────┘
                          │
微信 / Telegram ──▶ imbridge ──▶ ┐
浏览器        ──▶ agentserver ──┤    ┌──────────────────┐
                   (:8080)       │    │ 沙箱 Pod /       │
                   • REST API    ├───▶│ 容器             │
                   • 管理 UI      │    │ └─ opencode /   │
                   • 注册表       │    │    nanoclaw /   │
                   • 隧道         │    │    codex        │
                          │      │    └──────────────────┘
                          │      │
                          │      └──▶ 本地笔记本 / 台式机 / 手机
                          │            └─ agentserver-agent (WS 隧道)
                          ▼
                     PostgreSQL
                  (用户、工作区、
                   沙箱、配额、
                   会话、收件箱)

浏览器    ──▶ sandboxproxy (:8082)       ─▶ 按子域名路由到沙箱内服务
Jupyter   ──▶ codex-app-gateway (:8086)  ─▶ 每工作区一个 codex app-server 子进程
codex CLI ──▶ codex-exec-gateway (:6060) ─▶ `codex exec --remote` 执行器的会合端点
```

| 服务 | 默认端口 | 角色 |
|---------|-------------|------|
| **agentserver** | `:8080` | 主 API、Web UI、智能体注册表、隧道端点 |
| **llmproxy** | `:8081` | LLM API 代理，按工作区限速并统计用量 |
| **sandboxproxy** | `:8082` | 基于子域名的沙箱内服务路由 |
| **credentialproxy** | — | 服务端注入厂商凭证 |
| **imbridge** | — | IM 通道桥（微信 / Weixin、Telegram） |
| **codex-app-gateway** | `:8086` | 每工作区一个 codex app-server 子进程 + ws 桥，服务 codex desktop / Jupyter 等客户端 |
| **codex-exec-gateway** | `:6060` | 用户本机 `codex exec --remote` 执行器的会合端点 |

## 行为准则

agentserver 遵守四条贯穿全部变更的家规：

- ❌ **不接受人工编写的代码。** 所有生产代码均由 AI 智能体生成。
- ✅ **第一天起就开源。** 仓库自诞生即公开，不存在闭源阶段。
- ✅ **完全自动化的 DevOps。** 构建、测试、发布、部署全链路自动化。
- ✅ **吃自家狗粮。** agentserver 本身（部分）由 agentserver 构建 —— 每个特性都先被我们自己的智能体用过，才会发布。

## 自托管

### Docker Compose（推荐本地使用）

```bash
git clone https://github.com/agentserver/agentserver.git && cd agentserver
docker build -f Dockerfile.opencode -t agentserver-agent:latest .
export ANTHROPIC_API_KEY="sk-ant-..."
docker compose up -d
```

浏览器打开 `http://localhost:8080`。

### Helm（Kubernetes）

```bash
helm install agentserver oci://ghcr.io/agentserver/charts/agentserver \
  --namespace agentserver --create-namespace \
  --set database.url="postgres://user:pass@postgres:5432/agentserver?sslmode=disable" \
  --set anthropicApiKey="sk-ant-..." \
  --set ingress.enabled=true \
  --set ingress.host="cli.example.com" \
  --set baseDomain="cli.example.com"
```

### 预编译二进制

到 [GitHub Releases](https://github.com/agentserver/agentserver/releases) 下载，或通过 Homebrew 安装：

```bash
brew install agentserver/tap/agentserver
```

## 本地设备隧道

把本机运行的 opencode / codex 接入 agentserver —— 不需要公网 IP，也不需要任何第三方隧道。

1. 在 Web UI 中点击 "Sandboxes" 旁的笔记本图标生成接入凭证。

2. 在本机执行：

```bash
# 首次接入 —— 注册到服务器
agentserver connect \
  --server https://cli.example.com \
  --code <接入凭证> \
  --name "我的 MacBook"

# 之后的运行 —— 自动使用已保存的凭证重连
agentserver connect
```

3. Web UI 中会出现一个 **本地** 沙箱 —— 点击 "Open" 即可在浏览器中操作本机的智能体。

### 同机多实例

允许在同一台机器上注册多个智能体，分别对应不同的目录与工作区：

```bash
# 列出全部已注册智能体
agentserver list

# 删除一项注册
agentserver remove --workspace <workspace-id>
```

智能体凭证保存在 `~/.agentserver/registry.json`。

**隧道特性：** 零配置组网、按退避自动重连、二进制 WebSocket 协议（无 base64 开销）、实时 SSE 流、离线检测与自动恢复。

## 配置

完整端点文档见 [API 参考](docs/api-reference.md)。

<details>
<summary><strong>Helm Values</strong></summary>

| 参数 | 说明 | 默认值 |
|-----------|-------------|---------|
| `image.repository` | 服务端镜像 | `ghcr.io/agentserver/agentserver` |
| `image.tag` | 服务端镜像 tag | `latest` |
| `opencode.image` | 沙箱 Pod 使用的 opencode 智能体镜像 | `ghcr.io/agentserver/opencode-agent:latest` |
| `opencode.runtimeClassName` | 沙箱 Pod 的 RuntimeClass（如 `gvisor`） | `""` |
| `openclaw.image` | OpenClaw 网关镜像 | `""` |
| `openclaw.port` | OpenClaw 网关端口 | `18789` |
| `database.url` | PostgreSQL 连接串 | （必填） |
| `anthropicApiKey` | Anthropic API Key | （必填） |
| `anthropicBaseUrl` | 自定义 Anthropic API Base URL | `""` |
| `anthropicAuthToken` | Anthropic auth token（与 API Key 二选一） | `""` |
| `backend` | 沙箱后端：`docker` 或 `k8s` | `docker` |
| `baseDomain` | 子域名路由的基础域名 | `""` |
| `baseScheme` | 生成 URL 用的协议 | `https` |
| `idleTimeout` | 空闲沙箱自动暂停时长 | `30m` |
| `persistence.sessionStorageSize` | 单沙箱临时存储 | `5Gi` |
| `persistence.userDriveSize` | 工作区共享盘大小 | `10Gi` |
| `persistence.storageClassName` | PVC 的 storage class | `""`（集群默认） |
| `workspace.resources` | 沙箱 Pod 的资源请求/限制 | `1Gi/1cpu` limits |
| `agentSandbox.install` | 安装 Agent Sandbox 控制器 | `true` |
| `ingress.enabled` | 启用 Nginx Ingress | `false` |
| `ingress.host` | Ingress 主机名 | `agentserver.example.com` |
| `ingress.tls` | 启用 TLS（cert-manager） | `false` |
| `gateway.enabled` | 启用 Gateway API HTTPRoute | `false` |

</details>

<details>
<summary><strong>环境变量（主服务）</strong></summary>

| 变量 | 说明 | 默认值 |
|----------|-------------|---------|
| `DATABASE_URL` | PostgreSQL 连接串 | （必填） |
| `ANTHROPIC_API_KEY` | Anthropic API Key | （必填） |
| `ANTHROPIC_BASE_URL` | 自定义 API Base URL | `https://api.anthropic.com` |
| `ANTHROPIC_AUTH_TOKEN` | Anthropic auth token（与 API Key 二选一） | - |
| `OPENCODE_CONFIG_CONTENT` | 沙箱 Pod 的 opencode JSON 配置 | - |
| `BASE_DOMAIN` | 子域名路由的基础域名 | - |
| `BASE_SCHEME` | URL 协议（`http` / `https`） | `https` |
| `IDLE_TIMEOUT` | 自动暂停时长（如 `30m`） | `30m` |
| `AGENT_IMAGE` | 沙箱智能体的容器镜像 | `ghcr.io/agentserver/opencode-agent:latest` |
| `LLMPROXY_URL` | LLM 代理服务的 Base URL | - |
| `PASSWORD_AUTH_ENABLED` | 启用账号密码登录 | `true` |
| `OIDC_REDIRECT_BASE_URL` | OIDC 回调使用的外部 URL | - |
| `GITHUB_CLIENT_ID` | GitHub OAuth client ID | - |
| `GITHUB_CLIENT_SECRET` | GitHub OAuth client secret | - |
| `OIDC_ISSUER_URL` | 通用 OIDC issuer URL | - |
| `OIDC_CLIENT_ID` | 通用 OIDC client ID | - |
| `OIDC_CLIENT_SECRET` | 通用 OIDC client secret | - |
| `SANDBOX_NAMESPACE_PREFIX` | K8s 命名空间前缀 | `agent-ws` |
| `NETWORKPOLICY_ENABLED` | 启用 K8s NetworkPolicy 隔离 | `false` |
| `NETWORKPOLICY_DENY_CIDRS` | 网络策略禁止的 CIDR 段 | - |
| `AGENTSERVER_NAMESPACE` | agentserver 自身所在的 K8s 命名空间 | - |
| `STORAGE_CLASS` | PVC 的 K8s storage class | （集群默认） |
| `USER_DRIVE_SIZE` | 工作区存储大小 | `10Gi` |
| `USER_DRIVE_STORAGE_CLASS` | 工作区盘的 storage class | 继承 `STORAGE_CLASS` |
| `CC_BROKER_URL` | cc-broker 服务 URL（TUI 流程必填） | - |
| `EXECUTOR_REGISTRY_URL` | executor-registry 服务 URL（TUI 流程必填） | - |
| `INTERNAL_API_SECRET` | 内部端点共享密钥（推荐配置） | - |

</details>

<details>
<summary><strong>环境变量（LLM Proxy）</strong></summary>

| 变量 | 说明 | 默认值 |
|----------|-------------|---------|
| `LLMPROXY_LISTEN_ADDR` | HTTP 监听地址 | `:8081` |
| `LLMPROXY_DATABASE_URL` | 代理自身的 PostgreSQL 连接 URL | - |
| `LLMPROXY_AGENTSERVER_URL` | 用于校验 token 的 agentserver 内部 API URL | （必填） |
| `ANTHROPIC_API_KEY` | Anthropic API Key | （必填\*） |
| `ANTHROPIC_AUTH_TOKEN` | Anthropic auth token（与 API Key 二选一） | （必填\*） |
| `ANTHROPIC_BASE_URL` | 上游 Anthropic API URL | `https://api.anthropic.com` |
| `LLMPROXY_DEFAULT_MAX_RPD` | 工作区默认每日最大请求数（0 = 不限） | `0` |

</details>

<details>
<summary><strong>环境变量（Sandbox Proxy）</strong></summary>

| 变量 | 说明 | 默认值 |
|----------|-------------|---------|
| `DATABASE_URL` | PostgreSQL 连接串 | （必填） |
| `LISTEN_ADDR` | HTTP 监听地址 | `:8082` |
| `BASE_DOMAIN` | 子域名路由基础域名 | （必填） |
| `OPENCODE_SUBDOMAIN_PREFIX` | opencode 沙箱的子域名前缀 | `code` |
| `OPENCLAW_SUBDOMAIN_PREFIX` | openclaw 沙箱的子域名前缀 | `claw` |
| `OPENCODE_ASSET_DOMAIN` | opencode 静态资源域名 | `opencodeapp.{BASE_DOMAIN}` |

</details>

<details>
<summary><strong>OIDC 认证</strong></summary>

**GitHub OAuth：**

```bash
helm upgrade agentserver oci://ghcr.io/agentserver/charts/agentserver \
  --reuse-values \
  --set oidc.redirectBaseUrl="https://cli.example.com" \
  --set oidc.github.enabled=true \
  --set oidc.github.clientId="你的-client-id" \
  --set oidc.github.clientSecret="你的-client-secret"
```

回调地址：`https://cli.example.com/api/auth/oidc/github/callback`

**通用 OIDC（Keycloak、Authentik 等）：**

```bash
helm upgrade agentserver oci://ghcr.io/agentserver/charts/agentserver \
  --reuse-values \
  --set oidc.redirectBaseUrl="https://cli.example.com" \
  --set oidc.generic.enabled=true \
  --set oidc.generic.issuerUrl="https://idp.example.com/realms/main" \
  --set oidc.generic.clientId="agentserver" \
  --set oidc.generic.clientSecret="你的-secret"
```

</details>

<details>
<summary><strong>Kubernetes 后端</strong></summary>

用于生产级多租户部署 + gVisor 隔离：

```bash
helm upgrade agentserver oci://ghcr.io/agentserver/charts/agentserver \
  --reuse-values \
  --set backend=k8s \
  --set opencode.runtimeClassName=gvisor \
  --set sandbox.namespace=agentserver
```

</details>

## 从源码构建

```bash
# 前置依赖：Go 1.26、Node.js、pnpm、bun

# 全量构建（前端 + 后端）
make build

# 单独构建
make backend          # Go 二进制 → bin/agentserver
make frontend         # React 前端 → web/dist/
make agent            # 本地 agent 二进制 → bin/agentserver-agent
make agent-all        # 全平台 agent（linux/darwin/windows，amd64/arm64）
make llmproxy         # LLM 代理二进制 → bin/llmproxy

# Docker 镜像
make docker           # 主服务镜像
make docker-agent     # Agent 容器镜像
make docker-llmproxy  # LLM 代理镜像
make docker-all       # 全部镜像
```

## 参与贡献

```bash
# 终端 1：启动后端
go run . serve --db-url "postgres://..." --backend docker

# 终端 2：启动前端开发服务器
cd web && pnpm install && pnpm dev
```

按照 [行为准则](#行为准则)，生产代码由 AI 智能体生成。欢迎由智能体撰写、人类审阅的 PR；项目本身就在用自己构建自己。

## 社区与联系

- **托管实例** —— [agent.cs.ac.cn](https://agent.cs.ac.cn)（内测中，注册后我们会逐步开放）
- **问题反馈与功能请求** —— [github.com/agentserver/agentserver/issues](https://github.com/agentserver/agentserver/issues)
- **商务与合作咨询** —— [agentserver@mryao.org](mailto:agentserver@mryao.org)
- **喜欢这个项目？** ⭐ 一颗星可以让更多人发现它。

## 许可证

[MIT](LICENSE)
