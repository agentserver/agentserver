# ADR 0007：v2 产品前端、OpenAPI 客户端与设计系统

- 状态：Accepted
- 日期：2026-08-04
- 影响范围：Platform SPA、Browser SPA、public/edge API 契约、CI、生产 service 镜像

## 背景

当前生产 v2 的 `platform-web` 与 `a2ui-web` 是直接嵌入 Go binary 的无依赖 HTML/CSS/JavaScript。
它们验证了 OAuth、workspace resource、session 和 AG-UI 链路，但 API path、`fetch`、request/response
shape 与 DOM 状态机大多手写，不能形成稳定的产品前端基线。根目录已有的 `web/` 与 `browserweb/`
属于 v1 authority 和旧 token profile，不能把其业务代码接入 v2；它们只能作为 React/Vite 构建经验参考。

产品前端还需要统一满足：

- Platform 与 Browser 继续是两个独立应用和 OAuth client；
- API 契约来自 OpenAPI，而不是由前端重新声明 path 或 response interface；
- 视觉采用 OpenAI 控制台式的 shadcn/ui application shell；
- 同时支持 light、dark、system theme 以及中文、英文运行时切换；
- 生产镜像仍是无 CDN、无 Node runtime、由 Go binary 闭合托管的静态资源。

## 决策

### 1. v2 内建立独立前端 workspace

`v2/` 使用一个锁定版本的 pnpm workspace：

```text
v2/
├─ web-shared/       # generated clients、OAuth、theme、i18n、shadcn/ui primitives
├─ platform-web/     # Platform React/Vite app + Go static asset package
└─ a2ui-web/         # Browser React/Vite app + Go static asset package
```

两套 app 独立构建和生成 bundle，不共享 access token、React state 或浏览器存储。`web-shared` 只共享
没有产品 authority 的实现：API transport、协议校验、样式 token、基础组件、theme 和 locale provider。

React、Vite、TypeScript、Tailwind 和依赖版本由 `v2/pnpm-lock.yaml` 固定。shadcn/ui 不是运行时远程
组件库；所需组件源码保存在仓库内，并使用受控的 Radix primitives。浏览器不从 CDN 加载脚本、CSS、
字体或图片，现有 CSP 的 `script-src 'self'` / `style-src 'self'` 边界保持不变。

### 2. 所有前端网络访问经过 OpenAPI 生成客户端

OpenAPI 契约分成三类：

- `api/openapi/public.yaml`：Platform / Browser 可调用的 Core resource API；
- `api/openapi/web-edge.yaml`：`/auth/config`、Browser AG-UI stream 等 gateway edge API；
- `api/openapi/oauth-public.yaml`：public-client code exchange 所需的标准 `/oauth2/token` profile。

锁定的 `openapi-typescript` 从三份契约生成 path、parameter、body、response 和 error 类型；锁定的
`openapi-fetch` 只接受这些 generated path map。React feature 只能调用按 operation 暴露的 SDK function，
不得直接调用 `fetch`、拼接 `/v2/...` path 或手写另一份 response interface。OAuth authorize 是浏览器
顶层 navigation，不是 API fetch；其 URL 仍由经过运行时校验的公开 `/auth/config` 与 PKCE transaction
构造。

OpenAPI 生成类型不是运行时验证器。高熵 state/nonce/verifier、canonical UUID、OAuth authority、
token lifetime、scope subset、AG-UI event envelope 和跨 workspace projection 仍执行手写的 fail-closed
语义校验；这些 validator 消费 generated type，而不重新拥有 endpoint 或 JSON shape。

生成物与静态 bundle 均提交到仓库。CI 从锁文件安装依赖，重新生成和构建后检查 drift，因此普通 Go
构建不需要联网或携带 Node runtime。production image 继续只包含 Go service binary 及系统 CA。

### 3. 产品信息架构

Platform 使用固定左侧 application sidebar：顶部 workspace switcher 和折叠按钮，其下是 Search、Home、
Workspaces；选中 workspace 后显示 Overview、Members、Executors、LLM Gateways。底部 account menu 提供
theme、language、Platform/Browser 跳转与 sign out。主要内容区负责 workspace 与资源管理，LLM Gateway
定义和当前用户 grant 只存在于 Platform。

Browser 使用同一 application shell 密度和交互：顶部显示当前 workspace，下面是 Search、New chat 和
按更新时间分组的 session 历史；底部 account menu 与 Platform 一致。主内容区是对话，不保留面向调试的
常驻第三列；tool lifecycle、approval 和 A2UI surface 作为 assistant timeline 中的 shadcn card 展示。
Browser 不提供 executor 或 LLM Gateway 配置旁路；缺少模型 grant 时只显示返回 Platform 的明确操作。

桌面 sidebar 默认宽 260px，可折叠到 icon rail；窄屏变为 drawer。Command/Ctrl+K 打开同一个命令搜索
入口。keyboard focus、aria label、dialog focus trap 和 reduced-motion 都属于验收范围。

### 4. theme 与 i18n

theme 值为 `light | dark | system`，locale 值为 `zh-CN | en-US`。它们是非敏感用户偏好，可以写入
`localStorage`；首屏脚本不内联，应用挂载后立即应用选择，HTML `color-scheme` 与 shadcn CSS variables
同步。access token、PKCE verifier、workspace grant 和任何 Gateway credential 不得进入这些存储。

所有用户可见产品字符串通过 i18n key 获取；中文与英文 catalog 在同一提交中保持 key 集合完全一致，
缺 key 的 CI 测试失败。API/provider 原始错误先映射稳定 error code；只在无映射时显示经过边界限制的
安全 fallback，不把 token、header 或 response body直接渲染。

### 5. 静态资源与路由

Vite 分别输出到 `platform-web/dist` 与 `a2ui-web/dist`，Go 使用 `embed.FS` 托管闭合集合。hashed asset
可以长期缓存；HTML 与 OAuth callback 继续 `no-store`。静态 handler 只对已登记的产品 route 返回 SPA
index，未知 `/v2`、`/auth` 或 asset path 保持可见 404。

Platform 产品 route 位于 `/workspaces/...`；Browser workspace route 位于
`/workspaces/{canonicalWorkspaceId}`。OAuth redirect URI 仍固定为各 app 的 `/`，callback 完成后从一次性
PKCE transaction 恢复产品 route。生产 HTTPRoute 只增加这两个精确产品 path prefix，不把任意 host path
都回退到 SPA。

## 后果

- UI 迭代需要 Node/pnpm 构建步骤，但生产容器和运行时依旧不包含 Node；
- OpenAPI 变更会同时触发 Go contract test、TypeScript generation drift 和 app compile failure；
- bundle 体积大于当前手写 JavaScript，需要设置预算并保持 code splitting；
- 根目录 v1 前端与 v2 产品前端在迁移期并存，但生产 v2 只嵌入 `v2/*-web/dist`；
- durable transcript 仍需要独立后端 projection API；UI 不伪造历史内容，也不把浏览器内存当事实源。
