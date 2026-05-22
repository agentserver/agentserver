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
  <img src="assets/step-3-device-connected.png" alt="agentserver Connectors 视图 —— 南京、字节、新加坡、西安、昆山、郑州九台设备同时在线" width="820">
</p>
<p align="center"><sub><em>同一名用户的九台个人设备 —— 跨多地数据中心与笔记本 —— 全部聚拢在同一个工作区中。</em></sub></p>

> 📖 完整愿景请见：[Overview of agentserver](Overview%20of%20agentserver.pdf)（演示稿，2026 年 4 月）

**agentserver** 把你散落在生活各处的笔记本、台式机、云端沙箱乃至手机，组装成 **同一张个人算力网**：一个统一的工作区，你可以通过浏览器、[codex](https://developers.openai.com/codex/cli) CLI、Jupyter notebook，或微信聊天窗口去指挥它。每台被接入的机器称为一个 *Connector*，每个你实际去敲命令的会话称为一个 *Browser*。agentserver 就是把它们注册起来、托管凭证、路由请求的控制平面，让你（和你的协作者）从一个入口驱动所有设备。

它回答了 Addy Osmani 提出的一个问题：从 L1（不用 AI）走到 L8（自建编排器）的路径\*。当你同时管理 10+ 个跨设备的智能体时，你已经不再是 *指挥者*（conductor），而是 *编排者*（orchestrator）。agentserver 就是这一层编排底座。

<sub>\* Addy Osmani，Google · Gemini & Cloud AI 总监 —— <a href="https://talks.addy.ie/oreilly-codecon-march-2026">talks.addy.ie/oreilly-codecon-march-2026</a></sub>

### 它和现有工具有何不同

| 工具 | 本地多智能体 | 云端沙箱 | 跨设备组网 | 聊天软件通道 |
|------|:---:|:---:|:---:|:---:|
| OpenClaw / Claude Code Remote | 单实例 | — | — | — |
| Claude Code on the web | — | ✅ | — | — |
| Claude Code Agent Teams | — | ✅（子智能体） | — | — |
| **agentserver** | **✅ 多实例** | **✅** | **✅** | **✅（微信 / Telegram / Matrix）** |

## 为什么选择 agentserver？

- **口袋里就能指挥算力** —— 通过微信 / Weixin、Telegram 或 Matrix 聊天驱动你的智能体，离开桌面时也不必再打开终端。
- **一个工作区，统管所有设备** —— 云端沙箱、本地笔记本/台式机、IM 接入的智能体共享同一个工作区，全部并排出现在 Web UI 中。
- **原生面向 Codex** —— 围绕 [OpenAI codex](https://developers.openai.com/codex/cli) CLI 构建：设备用 `codex exec-server --remote` 接入，指挥机用 `codex --remote` 指挥；不需要在每台机器上额外装客户端。
- **沙箱可暂停、可恢复** —— 每任务一容器，空闲自动暂停；基于 Kubernetes + [Agent Sandbox](https://github.com/kubernetes-sigs/agent-sandbox) + gVisor，提供真正的多租户隔离。
- **同时欢迎"古法编程"** —— 内置 Jupyter notebook，让偏好亲自写代码的用户也能接入同一个工作区，使用智能体所用的文件系统与凭证。
- **多人协作** —— 邀请朋友或同事一起进入你的个人算力网；基于角色的访问控制（owner / maintainer / developer / guest）决定谁能做什么。
- **凭证 & LLM 代理** —— Connector 永远不接触真实的厂商密钥；每工作区的 RPD 配额与用量统计在服务端强制执行。
- **支持 SSO** —— GitHub OAuth 及通用 OIDC（Keycloak、Authentik 等）。

## 托管实例使用指南（共 7 步）

最快感受 agentserver 的方式，是直接使用托管实例 **[agent.cs.ac.cn](https://agent.cs.ac.cn)**。自托管用户可在自有域名下走完全相同的流程。

### 1. 注册账号

访问 [https://agent.cs.ac.cn](https://agent.cs.ac.cn) 完成账号注册。

### 2. 绑定大模型账号

在平台中绑定你自有的 ChatGPT / Anthropic / API Key，或选择平台提供的托管模型账号。

<p align="center">
  <img src="assets/step-2-model-binding.png" alt="LLM Provider 页 —— 连接 ModelServer 或自定义 Provider" width="780">
</p>

### 3. 把设备接入算力网

在每台希望加入的设备上安装 codex —— 笔记本、台式机、家庭服务器、云主机都可以：

```bash
# macOS
brew install codex

# 其他操作系统
npm i -g @openai/codex
```

在 Web UI 的 **Connectors** 页生成接入命令，复制到设备上、放进 `tmux`、`systemd` 等 detached 会话中运行，确保用户注销后 Connector 仍然在线：

<p align="center">
  <img src="assets/step-3-device-connect.png" alt="codex exec-server --remote 注册为一个 Connector" width="780">
</p>

设备会以 **Online** 状态出现在工作区列表中，与其它设备并排：

<p align="center">
  <img src="assets/step-3-device-connected.png" alt="九台 Connector 在线，分布多地" width="780">
</p>

### 4. 选定"指挥机"（Browser）

*Browser* 是你实际去敲命令的 codex 客户端 —— 通常是你的主力笔记本。在 **Browsers** 页生成一个 Browser token，按提示运行 `codex --remote …`，这台机器就变成了一个指挥中心，可以把任务下发到任意 Connector：

<p align="center">
  <img src="assets/step-4-command-machine.png" alt="Browsers 页 —— Token generated 对话框带有 codex --remote 命令" width="780">
</p>

### 5. （可选）创建 Jupyter 编程接口

不想全程用 AI，喜欢手写代码？在 Web UI 中开启一个 notebook 环境：每个 kernel 都已预注入 `ctx`，可直接访问与智能体相同的 Connector、文件系统与凭证：

<p align="center">
  <img src="assets/step-5-jupyter.png" alt="Jupyter notebook 使用 ctx.env('debian-devbox-sg').shell(…) 与 read_file" width="780">
</p>

我们称之为 **"古法编程"** —— 同一个工作区，是否引入 LLM 完全由你决定。

### 6. 接入微信个人账号

在平台扫码绑定你的个人微信，把对应智能体切换到 **Codex (via codex-app-gateway)** 模式后，你就可以直接在任意微信聊天中用自然语言下达指令，由对应设备执行：

<p align="center">
  <img src="assets/step-6-wechat.png" alt="IM Channels —— 微信 bot 已绑定，agent 切换到 Codex via codex-app-gateway；同时支持 Telegram、Matrix" width="780">
</p>

这是 agentserver 的招牌能力：**手机有信号的地方，就有你的算力。**

### 7. 开展多人协作

把朋友或同事加入工作区，共享 Connector、Browser 与凭证，并按角色控制权限：

<p align="center">
  <img src="assets/step-7-collaboration.png" alt="Members 页 —— owner 与 maintainer 角色" width="780">
</p>

## 架构

```
                  外部世界 (OpenAI、Anthropic、GitHub …)
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
Web 控制台    ──▶ agentserver  ──┤    ┌──────────────────┐
                   (:8080)       │    │ 沙箱 Pod /       │
                   • REST API    ├───▶│ 容器             │
                   • Web UI      │    │ └─ codex         │
                   • 注册表       │    └──────────────────┘
                          │      │
                          │      └──▶ 本地 Connector（笔记本、台式机、HPC …）
                          │            └─ codex exec-server --remote
                          ▼
                     PostgreSQL
                  (用户、工作区、
                   connectors、browsers、
                   配额、会话)

Browser (codex)  ──▶ codex-app-gateway  (:8086) ─▶ 每工作区一个 codex app-server 子进程
Jupyter notebook ──▶ codex-app-gateway  (:8086) ─▶ 同路径，共享 `ctx` 运行时
Connector (codex)──▶ codex-exec-gateway (:6060) ─▶ `codex exec-server --remote` 的会合端点
沙箱 URL          ──▶ sandboxproxy       (:8082) ─▶ 按子域名路由到沙箱内服务
```

| 服务 | 默认端口 | 角色 |
|---------|-------------|------|
| **agentserver** | `:8080` | 主 API、Web UI、Connector / Browser / 成员注册表 |
| **llmproxy** | `:8081` | LLM API 代理，按工作区限速并统计用量 |
| **sandboxproxy** | `:8082` | 基于子域名的沙箱内服务路由 |
| **credentialproxy** | — | 服务端注入厂商凭证 |
| **imbridge** | — | IM 通道桥（微信 / Weixin、Telegram、Matrix） |
| **codex-app-gateway** | `:8086` | 每工作区一个 codex app-server 子进程 + ws 桥，服务 Browser 会话与 Jupyter 客户端 |
| **codex-exec-gateway** | `:6060` | `codex exec-server --remote` Connector 的会合端点 |

### 后续方向

- **无状态 Harness** —— 把 *大脑*（模型 + harness）与 *双手*（Connector 与工具）解耦。会话是 append-only 的事件日志，活在上下文窗口之外。Worker 是 *牛，不是宠物* —— 一个 worker 在 turn 中挂掉不会丢任何东西。
- **云-本地混合 Mesh** —— 云端与本地 Connector 共享同一个工作区注册表。通过 agent card 进行发现；LLM 选工具，路由器决定调用落到哪台机器。*要的是 agent 发现，不是网络 mesh。*
- **基于收件箱的异步协作** —— 智能体通过持久化存储中的收件箱互相交接工作。发件时收件方可以不在线。**收件箱就是事实来源。**

## 自托管

### Helm（推荐 Kubernetes 部署）

```bash
helm install agentserver oci://ghcr.io/agentserver/charts/agentserver \
  --namespace agentserver --create-namespace \
  --set database.url="postgres://user:pass@postgres:5432/agentserver?sslmode=disable" \
  --set ingress.enabled=true \
  --set ingress.host="cli.example.com" \
  --set baseDomain="cli.example.com"
```

### 预编译二进制

到 [GitHub Releases](https://github.com/agentserver/agentserver/releases) 下载，或通过 Homebrew 安装：

```bash
brew install agentserver/tap/agentserver
```

## 配置

完整端点文档见 [API 参考](docs/api-reference.md)。

<details>
<summary><strong>Helm Values</strong></summary>

| 参数 | 说明 | 默认值 |
|-----------|-------------|---------|
| `image.repository` | 服务端镜像 | `ghcr.io/agentserver/agentserver` |
| `image.tag` | 服务端镜像 tag | `latest` |
| `database.url` | PostgreSQL 连接串 | （必填） |
| `backend` | 沙箱后端 | `k8s` |
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
| `BASE_DOMAIN` | 子域名路由的基础域名 | - |
| `BASE_SCHEME` | URL 协议（`http` / `https`） | `https` |
| `IDLE_TIMEOUT` | 自动暂停时长（如 `30m`） | `30m` |
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
| `INTERNAL_API_SECRET` | 内部端点共享密钥（推荐配置） | - |

</details>

<details>
<summary><strong>环境变量（LLM Proxy）</strong></summary>

| 变量 | 说明 | 默认值 |
|----------|-------------|---------|
| `LLMPROXY_LISTEN_ADDR` | HTTP 监听地址 | `:8081` |
| `LLMPROXY_DATABASE_URL` | 代理自身的 PostgreSQL 连接 URL | - |
| `LLMPROXY_AGENTSERVER_URL` | 用于校验 token 的 agentserver 内部 API URL | （必填） |
| `LLMPROXY_DEFAULT_MAX_RPD` | 工作区默认每日最大请求数（0 = 不限） | `0` |

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

## 从源码构建

```bash
# 前置依赖：Go 1.26、Node.js、pnpm、bun

# 全量构建（前端 + 后端）
make build

# 单独构建
make backend          # Go 二进制 → bin/agentserver
make frontend         # React 前端 → web/dist/
make llmproxy         # LLM 代理二进制 → bin/llmproxy
```

## 参与贡献

```bash
# 终端 1：启动后端
go run . serve --db-url "postgres://..."

# 终端 2：启动前端开发服务器
cd web && pnpm install && pnpm dev
```

欢迎提交 PR —— 项目本身就在用自己构建自己。

## 社区与联系

- **托管实例** —— [agent.cs.ac.cn](https://agent.cs.ac.cn)（内测中，注册后我们会逐步开放）
- **问题反馈与功能请求** —— [github.com/agentserver/agentserver/issues](https://github.com/agentserver/agentserver/issues)
- **商务与合作咨询** —— [agentserver@mryao.org](mailto:agentserver@mryao.org)
- **喜欢这个项目？** ⭐ 一颗星可以让更多人发现它。

## 许可证

[MIT](LICENSE)
