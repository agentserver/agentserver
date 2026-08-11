# TAE managed sandbox 镜像与启动契约

本文记录 2026-08-11 对 TAE 官方 Terminal Sandbox 镜像和 SG 线上 FaaS revision 的核验结果，并定义
agentserver 自有 managed-sandbox 镜像真正需要实现的最小契约。结论不是继承官方镜像，而是理解其启动
边界后，在自有镜像中实现可审计的 FaaS keeper；进程和文件能力继续使用 TAE 注入的 SandboxD。

## 1. 官方镜像证据

控制台 source 中的 `1.0.0.5` 是 FaaS source revision，不是 OCI tag。对应的真实 OCI 引用为：

```text
aliyun-sin-hub.byted.org/faas/bytedance.sandbox.terminal_faas:c141b650bccfe51a483a343d2abee0a4
```

本次核验的 linux/amd64 platform manifest 为：

```text
sha256:ac8362273c1bad617a370db4fe0e02fe09687e68c0d8951d309989166b06bb08
```

镜像包含 36 个 rootfs layer、66 条 history，压缩 layer 总大小为 1,819,809,940 bytes（约 1.74 GiB）。
最终 config 使用 root，`WorkingDir=/root/.sandbox`，`Cmd=["python3"]`，镜像内 Python 为 3.12.4。

可用以下只读命令复核 descriptor、diff ID、history 和 config；生产判断必须使用 manifest digest，不能只看
可变 tag：

```bash
skopeo inspect --raw \
  docker://aliyun-sin-hub.byted.org/faas/bytedance.sandbox.terminal_faas@sha256:ac8362273c1bad617a370db4fe0e02fe09687e68c0d8951d309989166b06bb08
skopeo inspect --config \
  docker://aliyun-sin-hub.byted.org/faas/bytedance.sandbox.terminal_faas@sha256:ac8362273c1bad617a370db4fe0e02fe09687e68c0d8951d309989166b06bb08
```

36 层的职责如下。这里按 image history 和 layer payload 归纳用途；它说明官方镜像为何很大，也说明这些
开发环境组件不应无差别进入 agentserver 镜像。

| 层 | 主要内容 |
|---:|---|
| 1 | Debian Bookworm rootfs |
| 2 | 基础网络工具、locale、`tiger` 用户和 pip bootstrap |
| 3 | systemd、rsyslog、unscd |
| 4 | systemd 运行配置（第一组） |
| 5 | systemd 运行配置（第二组） |
| 6 | 平台辅助二进制与启动辅助文件 |
| 7 | shell/bashrc 初始化配置 |
| 8 | systemd 精简和 Python `requests` |
| 9 | Python 3.12 runtime 安装阶段一 |
| 10 | Python 3.12 runtime 安装阶段二 |
| 11 | pip/bootstrap packaging 组件 |
| 12 | Python 3.12 与 pip 安装收口 |
| 13 | 大量开发、网络、SSH 和 VNC 工具 |
| 14 | locale 数据与配置 |
| 15 | 时区数据与配置 |
| 16 | pip 配置 |
| 17 | `uv` |
| 18 | Tango |
| 19 | Go toolchain 和 goimports |
| 20 | Node.js 18/22 |
| 21 | ByteOpenJDK 8 |
| 22 | Maven |
| 23 | Chrome 和字体 |
| 24 | code-server |
| 25 | noVNC |
| 26 | apt cache 清理 |
| 27 | `/root/.sandbox` 工作目录准备 |
| 28 | ripgrep |
| 29 | `sandbox/core` Python 包 |
| 30 | `sandbox/terminal` Python 包 |
| 31 | Terminal Python requirements |
| 32 | code-server extension |
| 33 | code-server settings |
| 34 | noVNC settings |
| 35 | health probe |
| 36 | `faas-init.sh`：复制 runtime、安装 mise，并生成 `/opt/tiger/run.sh` |

## 2. 官方启动链

官方 session/revision 选择了 `/opt/tiger/run.sh`，所以 OCI `Cmd=["python3"]` 不是其线上主启动入口。
这是官方镜像的可配置启动命令，不是 TAE 规定的固定路径。镜像内最终链路为：

```text
/opt/tiger/run.sh
  -> /root/.config/.terminal_sandbox/terminal_sandbox_server.sh
  -> PYTHONPATH=/root/opt/site-packages
  -> python -m terminal.main
```

`terminal.main` 从 `_BYTEFAAS_RUNTIME_PORT` 读取监听端口；官方脚本的缺省值是 9002，当前 SG FaaS
revision 注入并声明的 runtime container port 是 8080。它提供 `/v1/ping`、`/run_bash`、
`/process/list` 和 `/process/log`。

这些 endpoint 不能和 TAE SandboxD 混为一谈。agentserver 的 provider 实际调用：

```text
/api/process/start
/api/process/connect
/api/process/signal
/api/fs/*
```

这些 `/api/*` 能力由 TAE 在 session 中注入并维护的 SandboxD 提供，不来自 `terminal.main`。因此复制官方
Python Terminal server，或者重新实现一套 `/api/process/*`，都会形成第二个执行面，且不能解决 FaaS
主进程缺失的问题。

## 3. agentserver 自有实现

managed-sandbox 不继承 1.74 GiB 的 `faas/bytedance.sandbox.terminal_faas`。它使用 agentserver registry
中 digest-pinned 的单层 Debian Trixie rootfs，并把以下内容作为 closed-world managed overlay 构建进
自有镜像：

- 静态 `agentserver-tae-runtime`；
- pinned `lark-cli`、只读 skill pack、CA bundle 和 image manifest。

镜像和启动入口在 TAE Terminal Sandbox 的管理面 revision 中发布。生产配置同时锁定 Sandbox ID 和
revision ID；sandbox-gateway 通过官方 SDK 创建 Session 时只发送：

```json
{"revision_id":"<pinned-terminal-revision-id>"}
```

请求不包含 `image` 或 `command`。管理面 revision 固定 agentserver 镜像及
`run_cmd=/usr/local/bin/agentserver-tae-runtime`，SDK transport 同时把所有生命周期请求限制在配置的
Sandbox ID，避免 PSM 查询把操作重定向到其他资源。runtime 使用 IPv6 wildcard 监听
`_BYTEFAAS_RUNTIME_PORT`，变量缺失时使用当前 revision 的 8080，并且只暴露：

TAE 的 Session create/get/search 响应不保证回显可选的 `command` 字段。空值因此只表示“控制面未报告”，
不能反推实际入口为空；任何非空且不同的值仍立即失败。入口证据由三条独立门禁组成：SDK adapter 在发出
请求前固定 Sandbox ID/revision ID 且只填 `revision_id`，发布流程锁定管理面 revision、镜像 tag、OCI
`Cmd` 和 runtime profile，真实 session smoke 再通过 SandboxD 执行命令并核对镜像内的 CLI/skill 摘要。
不能用缺失的响应字段替代数据面验证。

```text
GET  /v1/ping -> 200 application/json "pong"
HEAD /v1/ping -> 200 application/json
```

它不提供 `/run_bash`、`/process/list`、`/process/log` 或任何 `/api/process/*`、`/api/fs/*`。所有命令执行
仍经统一 execution gateway、sandbox-gateway 和 TAE SandboxD，runtime 本身没有通用命令执行入口，也不
读取 workspace credential。

最终 OCI runtime contract 固定为：

```text
User=0:0
WorkingDir=/workspace
Cmd=["/usr/local/bin/agentserver-tae-runtime"]
StopSignal=SIGTERM
```

这里的 OCI `Cmd` 是安全 fallback；TAE 运行时以 pinned Terminal Sandbox revision 的 `run_cmd` 为准。
Sandbox ID、revision ID、镜像和 runtime contract 一起进入 canonical managed runtime profile digest，
避免管理面选择的入口与镜像/环境锁脱节。

使用 root 与官方镜像保持一致，并为 TAE 注入和管理 SandboxD 留出它需要的进程权限；安全边界由 TAE
sandbox、统一 execution gateway 和 egress policy 提供，不能靠把 keeper 改成 `nobody` 冒充。镜像
verifier 同时锁定 base layer descriptor/diff ID/history、root/Cmd、静态 runtime ELF、managed 文件集合
以及每层 owner/mode/size/SHA-256；SDK adapter 另行锁定 Sandbox ID、revision ID 和 Session create 字段
集合。任何入口漂移都会在发布前失败。

## 4. 上线验证

镜像级单测只证明静态契约。发布后还必须用一次性 terminal session 验证以下事实，并在结束后删除
session：

1. CreateSession 请求只携带 pinned `revision_id`，明确不含 `image`/`command`；响应若回显 command 则必须精确一致，session 进入 ready，而不是 `function_exited`；
2. `sandboxd_enabled=true`，`/api/process/start` 可执行 `printf terminal-ok` 并取得有序终态；
3. `lark-cli` 与 skill pack 存在，workspace 选择的 credential mode 能完成只读 smoke；
4. SIGTERM、TTL/delete 和失败清理不会留下 session 或额外环境副作用。
