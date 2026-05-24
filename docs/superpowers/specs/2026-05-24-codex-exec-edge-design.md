# codex-exec-edge: 接入层组件设计

**Date:** 2026-05-24
**Status:** Draft, awaiting review
**Author:** mryao (with assistance)

## 1. 背景与问题

`codex-exec-gateway` 每次发版都会让所有 connector（用户机器上运行的
`codex exec-server --remote`）断开。表象是 WS 中断，根因在 codex CLI 的重连
循环本身：

```rust
// codex/codex-rs/exec-server/src/remote.rs:134-168
loop {
    let response = client.register_environment(...).await?;   // ← ① ?? 直接 propagate
    match connect_async(...).await {
        Ok((ws, _)) => {
            backoff = Duration::from_secs(1);
            run_multiplexed_environment(ws, ...).await;        // ← ② 返回时 WS 已断
        }
        Err(err) => warn!(...)
    }
    sleep(backoff).await;
    backoff = (backoff * 2).min(Duration::from_secs(30));
}
```

- `connect_async` 失败 codex 只 `warn!()` 然后 sleep + retry，**不退出**
- `register_environment().await?` 失败（5xx / connection refused / RST）**会**沿
  `?` 链一路 propagate 到 `cli/src/main.rs:1498` 的 `await?`，**整个 codex 进程退出**

`codex-exec-gateway` 在生效 audit PVC（默认开）时部署策略是 `Recreate`
（`deploy/helm/agentserver/templates/codex-exec-gateway.yaml:21-28`，因为 RWO
audit PVC 不允许多 pod 共存）—— 旧 pod 完全死掉之后新 pod 才起，期间
Ingress 转 `/cloud/.../register` 必返回 503，落入上述 ① 路径，于是 connector
进程退出。这就是用户感知到的"每次发版断开"。

WS 断开本身（路径 ②）不是问题；register HTTP 请求被 codex 当成 fatal 才是。
所以解决方案必须围绕 **如何让 `/cloud/{}/register` 在 gateway 重启窗口内不
返回 5xx 给 client**。

## 2. 方案选择

考虑过的三个方向：

| | 自研 edge（本方案） | Istio VirtualService retry | 修 codex 上游 |
|---|---|---|---|
| 改动面 | 新增 Go 二进制 + Deployment + HTTPRoute 拆分 | 替换 2 个 HTTPRoute 为 VirtualService | fork codex 加 register retry |
| 迁移风险 | edge 上线后再切流量，可独立 rollback | HTTPRoute → VS 是 Pulumi delete+create，~10s 路由空窗 | 长期 fork 维护成本 |
| 与现有约定一致性 | 高（与 codex-exec-gateway / codex-app-gateway 同档） | 中（仓库已有 DR/VS 但 codex 路径全是 HTTPRoute） | 低（已明确放弃 fork codex） |
| 未来扩展 | 可加 WS 级假活、可加更多 connector 侧逻辑 | 仅 retry，无扩展空间 | — |

选 **自研 edge**：与既有架构对齐、可独立演进、迁移风险可控。

## 3. 架构

```
                                  ┌──────────────────────────────────────┐
                                  │  Istio gateway (Gateway API)         │
                                  │  hostnames: codex-exec.agent.cs.ac.cn│
                                  │             codex-exec.agentserver.dev│
                                  └──────────────────────────────────────┘
                                       │                          │
              /codex-exec/{id} + /cloud/{}/register               │  /bridge/*  /relay/*  /healthz  /api/codex-exec/*
                                       ▼                          ▼
                          ┌─────────────────────────┐  ┌──────────────────────────┐
                          │  codex-exec-edge        │  │  codex-exec-gateway       │
                          │  (Deployment, 2 replica)│  │  (unchanged)              │
                          │                         │  │                           │
                          │  • verify WS ticket     │──┤  ws /codex-exec/{id}      │
                          │  • bidir WS pipe        │  │  http /cloud/.../register │
                          │  • http revproxy +      │──┤  ws /bridge/{id}          │
                          │    upstream-503 retry   │  │  http /relay/*            │
                          │  • keepalive on both    │  └──────────────────────────┘
                          │    sides                │
                          │  • set X-Forwarded-For  │
                          │    + X-Real-IP          │
                          └─────────────────────────┘
```

**关键不变量：**

- edge 完全无状态；scale 任意 N；崩了仍由 codex CLI 的 reconnect 循环兜底
- gateway 协议零变化；edge 透传 ticket，gateway 用同一 HMAC secret 再验一次
- 共享 `internal.apiSecret`（K8s Secret）：gateway 用它 mint ws ticket，edge 用它 verify

## 4. 行为时序

### 4.1 正常请求

```
codex CLI: POST /cloud/{env_id}/register
    ↓ (Istio HTTPRoute /cloud/* → edge)
edge: 透传 + 设置 X-Forwarded-For
    ↓ HTTP
gateway: validate auth → mint ticket → 200 {url: "wss://.../codex-exec/{id}?token=..."}
    ↑ (edge 透传)
codex CLI: 拿到 url
    ↓ WS dial
edge: verify ticket → dial upstream WS → accept downstream WS → bidir pipe
```

### 4.2 gateway Recreate 窗口（典型 10–20s）

```
t=0    gateway 旧 pod 收到 SIGTERM, preStop sleep 10s
t=0    edge 检测上游 WS 关闭 → 关闭下游 WS
t=1    codex CLI: run_multiplexed_environment 返回 → sleep(1s)
t=2    codex CLI: POST /cloud/{id}/register → 命中 edge
t=2    edge → upstream gateway Service：拒绝/503（旧 pod 走了，新 pod 还没 Ready）
t=2..  edge in-process retry：500ms ±25% jitter, 1s, 2s, 4s, 8s (cap，总 30s)
t=15   新 gateway pod Ready → edge 这次 RoundTrip 成功
t=15   register 200 OK 返回 codex CLI → 重新 connect_async → 新连接挂到新 gateway
```

connector 进程不退出；in-flight 工具调用（已经在 `/bridge/*` 上的）会失败，
但这是 gateway 路由表丢失的固有结果，本方案不解决（未来若需要可加 edge 侧
WS 假活，作为增量）。

## 5. 代码结构

### 5.1 准备工作（必须先做）

**抽出 ws ticket 子包：** 新建 `internal/codexexecgateway/wsticket/`，把
`internal/codexexecgateway/handlers/ws_ticket.go` 的 `MintWSTicket` /
`VerifyWSTicket` 整体迁移过去，原文件改 import 转 re-export（或直接重命名所有
调用方）。

理由：`handlers` 包同时含 `cloud_register.go`、`internal_api.go`、
`workspace_binding.go` 等，依赖 `clientmeta`、`execmodel`、`chi`。edge 只需要
ticket 验签，不该把 gateway 的整个 handler 编进 edge 二进制。`wsticket` 子包
只用 stdlib + crypto，约 70 LOC。

### 5.2 新增二进制 + 包

```
cmd/codex-exec-edge/
└── main.go                            ~50 LOC

internal/codexexececdge/
├── config.go                          ~60 LOC
├── server.go                          ~70 LOC
├── wsproxy.go                         ~120 LOC
├── registerproxy.go                   ~100 LOC   (合并 retrytransport.go)
├── wsproxy_test.go                    ~150 LOC
└── registerproxy_test.go              ~150 LOC

Dockerfile.codex-exec-edge             ~12 LOC
```

**复用：**
- `internal/codexexecgateway/wsticket.VerifyWSTicket` — 上面抽包后的目标
- `internal/wsbridge.{KeepAlive, ListenWithKeepAlive}`
- `internal/clientmeta.ClientIP`（**只在 edge 端读**；gateway 端已经会读 XFF，零修改）

**不引：** `internal/codexexecgateway` 主包、`audit`、`relay`、`sdk`、store、PG。

### 5.3 Config

`CXE_` 前缀与 gateway 的 `CXG_` 区分：

```go
type Config struct {
    Port                        string        // CXE_PORT, default "6061"
    UpstreamBaseURL             string        // CXE_UPSTREAM_BASE_URL
    AgentserverInternalSecret   string        // CXE_AGENTSERVER_INTERNAL_SECRET
                                              //   (same value as CXG_AGENTSERVER_INTERNAL_SECRET)
    RegisterRetryTotalTimeout   time.Duration // CXE_REGISTER_RETRY_TIMEOUT, default 30s
    RegisterRetryInitialBackoff time.Duration // CXE_REGISTER_RETRY_BASE,    default 500ms
    UpstreamDialTimeout         time.Duration // CXE_UPSTREAM_DIAL_TIMEOUT,  default 5s
    LogLevel                    slog.Level    // CXE_LOG_LEVEL
}

func (c Config) Validate() error {
    if c.UpstreamBaseURL == "" { return errors.New("CXE_UPSTREAM_BASE_URL required") }
    if c.AgentserverInternalSecret == "" { return errors.New("CXE_AGENTSERVER_INTERNAL_SECRET required") }
    return nil
}
```

### 5.4 Routes

```go
func (s *Server) Routes() http.Handler {
    r := chi.NewRouter()
    r.Use(middleware.Recoverer)
    r.Get("/healthz", okHandler)
    r.Get("/codex-exec/{exe_id}", s.handleWSProxy)
    r.Post("/cloud/executor/{exe_id}/register", s.handleRegisterProxy)
    r.Post("/cloud/environment/{env_id}/register", s.handleRegisterProxy)
    return r
}
```

## 6. 实现细节

### 6.1 WS 代理（`wsproxy.go`）

```
handleWSProxy:
  ① wsticket.VerifyWSTicket(token, exeID, secret) → 401 on fail
  ② dial upstream:
       url = UpstreamBaseURL + "/codex-exec/" + exeID + "?token=" + token
       headers:
         X-Forwarded-For: clientmeta.ClientIP(r)      ← MUST set
         X-Real-IP:       clientmeta.ClientIP(r)      ← MUST set
         User-Agent:      r.Header.Get("User-Agent")  ← forward UA for codex version parsing
       timeout = UpstreamDialTimeout
       fail → 502 (client not yet upgraded; plain HTTP error fine)
  ③ websocket.Accept(client)
     ws.SetReadLimit(-1)  // codex exec-server 会发大 response (process/read)
  ④ launch 2 pump goroutines + 2 KeepAlive goroutines:
       pumpClientToUpstream:  for { mt, data := client.Read; upstream.Write(mt, data) }
       pumpUpstreamToClient:  for { mt, data := upstream.Read; client.Write(mt, data) }
       wsbridge.KeepAlive(client,   30s)
       wsbridge.KeepAlive(upstream, 30s)
  ⑤ 任一 pump 返回 → 关另一边（close code 用 nhooyr.io/websocket.CloseStatus
     从对端读，缺省 1011 InternalError）
```

**关键点：**

- byte-level 代理，**不解析 RelayMessageFrame**
- `ReadLimit = -1`（与 gateway inbound 对齐，否则大 response 被截断）
- `X-Forwarded-For` / `X-Real-IP` **必须设**：gateway 的 `clientmeta.ClientIP`
  已支持 XFF 链解析（`internal/clientmeta/clientmeta.go:18-35`），不设的话
  store 里 `last_client_ip` 全变成 edge pod IP，影响 audit 准确性 + UI
- ticket 透传，**edge 不重新 mint**

### 6.2 Register 代理（`registerproxy.go`）

不用 `httputil.ReverseProxy`：手写更短更清晰，body replay 不用操心 `GetBody`
契约。

```go
func (s *Server) handleRegisterProxy(w http.ResponseWriter, r *http.Request) {
    // 1MB body cap：防异常 payload；register POST 实际 <1KB
    body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
    if err != nil { http.Error(w, "payload too large", 413); return }

    upstreamURL := s.cfg.UpstreamBaseURL + r.URL.Path
    deadline := time.Now().Add(s.cfg.RegisterRetryTotalTimeout)
    backoff  := s.cfg.RegisterRetryInitialBackoff

    var lastResp *http.Response
    var lastErr  error
    for {
        req, _ := http.NewRequestWithContext(r.Context(), r.Method,
            upstreamURL, bytes.NewReader(body))
        copyHeaders(req.Header, r.Header)
        req.Header.Set("X-Forwarded-For", clientmeta.ClientIP(r))  // MUST
        req.Header.Set("X-Real-IP",       clientmeta.ClientIP(r))  // MUST

        resp, err := s.httpClient.Do(req)
        lastResp, lastErr = resp, err

        if err == nil && !isRetryable(resp.StatusCode) {
            writeResponse(w, resp)
            return
        }
        if resp != nil {
            io.Copy(io.Discard, resp.Body)
            resp.Body.Close()
        }
        // 带 jitter 的指数退避，分散 thundering herd
        sleep := backoff + jitter(backoff, 0.25)
        if time.Now().Add(sleep).After(deadline) { break }
        select {
        case <-time.After(sleep):
        case <-r.Context().Done(): return
        }
        backoff = min(backoff * 2, 8 * time.Second)
    }
    // 重试耗尽 → 透传最后一次的 response（让 codex 看到真实错误）
    if lastErr != nil {
        http.Error(w, "upstream unreachable: "+lastErr.Error(), 502)
        return
    }
    writeResponse(w, lastResp)
}

func isRetryable(status int) bool {
    return status == 502 || status == 503 || status == 504
}
```

**关键点：**

- 4xx **不重试**（鉴权失败不是上游不可用）
- 5xx + 网络错误（dial refused / RST / timeout）才重试
- `jitter(base, frac)` 加 ±25% 抖动，分散同时间到的多 connector 重试洪峰
- 重试耗尽透传**真实**最后状态（让 codex 看到 503 而非 200）
- body cap 1MB 是防御性的，正常 register payload <1KB
- 幂等性确认：`handlers.CloudRegister`（`cloud_register.go:102-141`）走
  `UpdateClientMetaFromRegister`（UPDATE，非计数）+ `MintWSTicket`（纯函数），
  重试 N 次效果等于成功 1 次

### 6.3 错误处理矩阵

**WS 代理：**

| 场景 | 行为 |
|---|---|
| 没带 `?token=` 或验签失败 | 401，不 dial upstream |
| ticket TTL 过期 | 401 + `"token expired"` |
| upstream dial 失败 | 502，**不重试** |
| dial 成功握手中被关 | 502 |
| 运行中 client 关 | 关 upstream，close code 用 client 给的 |
| 运行中 upstream 关 | 关 client，close code 用 upstream 给的 |
| pump panic | recover + 关两边 + log error |
| 单帧超大 | 透传，`ReadLimit = -1` |

**Register 代理：**

| 场景 | 行为 |
|---|---|
| 2xx | 立刻透传，return |
| 4xx | 立刻透传，**不重试** |
| 5xx (502/503/504) | 退避重试，最长 30s |
| connection refused / RST / dial timeout | 退避重试 |
| body > 1MB | 413，不缓冲 |
| client 主动断 | 取消正在进行的 RoundTrip + 退出循环 |
| 重试耗尽 | 透传最后一次 response，让 codex 看到真实错误 |
| edge SIGTERM 中途 | 取消 retry；正在进行的 request 透传当前 response 或 502 |

### 6.4 边界 case

- **edge 自身升级：** `RollingUpdate`，`maxUnavailable=0`，`maxSurge=1`，
  `preStop sleep 5`。Istio xDS endpoint 移除 sub-second，影响最小。
- **edge ↔ gateway 网络分区：** 30s retry 内透传 503 给 codex；codex 进程退出。
  这是合理失败模式。
- **upstream Ready 但 endpoint 未就绪：** Service 只 route 给 Ready endpoint，
  edge 收到 connection refused，正常 retry。
- **多 connector 同时重连（thundering herd）：** retry backoff 加 ±25% jitter
  分散；gateway 侧加 observability（见 §8）。
- **edge 单 pod 崩：** 该 pod 上的 WS 全断；codex 的 reconnect 循环兜底，
  跨到其他 edge pod 后复连。
- **publicWSBaseURL 不动：** gateway 仍返回 `wss://codex-exec.host/codex-exec/...`，
  Ingress 路径分发命中 edge。

## 7. 部署

### 7.1 改动清单

**agentserver 仓库：**

- 新文件：
  - `cmd/codex-exec-edge/main.go`
  - `internal/codexexececdge/` 整个包
  - `internal/codexexecgateway/wsticket/` 抽出来的子包（先做）
  - `Dockerfile.codex-exec-edge`
  - `deploy/helm/agentserver/templates/codex-exec-edge.yaml`
- 改动：
  - `deploy/helm/agentserver/values.yaml`：新增 `codexExecEdge` section
  - `deploy/helm/agentserver/Chart.yaml`：appVersion / version bump
  - `.github/workflows/build.yml`：新增 `build-codex-exec-edge` 作业，并加入
    `release-helm-chart` 的 `needs` 列表
  - `internal/codexexecgateway/handlers/ws_ticket.go`：迁移到 `wsticket` 子包，
    `handlers` 包改 re-export 或调用方全部更新

**k8s 仓库（Pulumi）：**

- 改 `stacks/agentserver.ts` 的 `codexExecRouteCN` (L496-514) 和 `codexExecRoute`
  (L566-584)，把 `/codex-exec/*` 和 `/cloud/*` 切到 edge backend，其他路径
  保持到 gateway。

### 7.2 Helm template（`deploy/helm/agentserver/templates/codex-exec-edge.yaml`）

```yaml
{{- if .Values.codexExecEdge.enabled }}
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ .Release.Name }}-codex-exec-edge
  labels:
    app: {{ .Release.Name }}-codex-exec-edge
spec:
  replicas: {{ .Values.codexExecEdge.replicaCount }}
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxUnavailable: 0
      maxSurge: 1
  selector:
    matchLabels:
      app: {{ .Release.Name }}-codex-exec-edge
  template:
    metadata:
      annotations:
        agentserver.x-k8s.io/chart-version: {{ .Chart.AppVersion | quote }}
      labels:
        app: {{ .Release.Name }}-codex-exec-edge
    spec:
      terminationGracePeriodSeconds: 30
      containers:
        - name: codex-exec-edge
          image: "{{ .Values.codexExecEdge.image.repository }}:{{ .Values.codexExecEdge.image.tag }}"
          imagePullPolicy: {{ .Values.codexExecEdge.image.pullPolicy }}
          ports:
            - containerPort: {{ .Values.codexExecEdge.port }}
              name: http
              protocol: TCP
          env:
            - name: CXE_PORT
              value: {{ .Values.codexExecEdge.port | quote }}
            - name: CXE_UPSTREAM_BASE_URL
              value: "http://{{ .Release.Name }}-codex-exec-gateway.{{ .Release.Namespace }}.svc:{{ .Values.codexExecGateway.port }}"
            - name: CXE_AGENTSERVER_INTERNAL_SECRET
              value: {{ required "internal.apiSecret is required when codexExecEdge.enabled is true" .Values.internal.apiSecret | quote }}
            - name: CXE_REGISTER_RETRY_TIMEOUT
              value: {{ .Values.codexExecEdge.registerRetryTimeout | default "30s" | quote }}
            - name: CXE_REGISTER_RETRY_BASE
              value: {{ .Values.codexExecEdge.registerRetryBase | default "500ms" | quote }}
            - name: CXE_UPSTREAM_DIAL_TIMEOUT
              value: {{ .Values.codexExecEdge.upstreamDialTimeout | default "5s" | quote }}
            {{- if .Values.codexExecEdge.logLevel }}
            - name: CXE_LOG_LEVEL
              value: {{ .Values.codexExecEdge.logLevel | quote }}
            {{- end }}
          livenessProbe:
            httpGet: { path: /healthz, port: {{ .Values.codexExecEdge.port }} }
            initialDelaySeconds: 5
            periodSeconds: 10
          readinessProbe:
            httpGet: { path: /healthz, port: {{ .Values.codexExecEdge.port }} }
            initialDelaySeconds: 2
            periodSeconds: 5
          lifecycle:
            preStop:
              exec:
                command: ["sh", "-c", "sleep 5"]
          {{- with .Values.codexExecEdge.resources }}
          resources: {{- toYaml . | nindent 12 }}
          {{- end }}
---
apiVersion: v1
kind: Service
metadata:
  name: {{ .Release.Name }}-codex-exec-edge
spec:
  type: ClusterIP
  ports:
    - port: {{ .Values.codexExecEdge.port }}
      targetPort: {{ .Values.codexExecEdge.port }}
      protocol: TCP
      name: http
  selector:
    app: {{ .Release.Name }}-codex-exec-edge
{{- end }}
```

**`values.yaml` 新增：**

```yaml
# codex-exec-edge: thin WS auth/proxy + register-retry layer in front of
# codex-exec-gateway. Decouples connector lifecycle from gateway upgrades.
codexExecEdge:
  enabled: true
  image:
    repository: ghcr.io/agentserver/codex-exec-edge
    tag: "0.64.26"        # bump in lockstep with chart appVersion
    pullPolicy: IfNotPresent
  replicaCount: 2
  port: 6061
  registerRetryTimeout: "30s"
  registerRetryBase: "500ms"
  upstreamDialTimeout: "5s"
  logLevel: "info"
  resources:
    requests: { cpu: "50m",  memory: "64Mi" }
    limits:   { cpu: "500m", memory: "256Mi" }
```

### 7.3 Pulumi HTTPRoute 改动

`/root/k8s/stacks/agentserver.ts` 里有 **两份** codex-exec HTTPRoute 需要同时
改动：

- `codexExecRouteCN` (L496-514)，hostname `codex-exec.agent.cs.ac.cn`
- `codexExecRoute`   (L566-584)，hostname `codex-exec.agentserver.dev`

抽出一个 helper 避免重复：

```ts
const codexExecRouteRules = [
    // /codex-exec/{id} (ws) 和 /cloud/{}/register (http) → edge
    {
        matches: [
            { path: { type: "PathPrefix", value: "/codex-exec/" } },
            { path: { type: "PathPrefix", value: "/cloud/" } },
        ],
        backendRefs: [{ name: `${name}-codex-exec-edge`, port: 6061 }],
    },
    // 其他所有路径（/bridge/* ws、/relay/* http、/api/codex-exec/* http、
    // /healthz、/internal/sdk/connected）保持原样到 gateway
    {
        matches: [{ path: { type: "PathPrefix", value: "/" } }],
        backendRefs: [{ name: `${name}-codex-exec-gateway`, port: 6060 }],
    },
];

const makeCodexExecRoute = (resourceKey: string, hostname: string) =>
    new k8s.apiextensions.CustomResource(
        resourceKey,
        {
            apiVersion: "gateway.networking.k8s.io/v1",
            kind: "HTTPRoute",
            metadata: { name: resourceKey.replace(/^crd-/, ""), namespace: ns.metadata.name },
            spec: {
                parentRefs: [{ name: "istio-gateway", namespace: "istio-ingress" }],
                hostnames: [hostname],
                rules: codexExecRouteRules,
            },
        },
        { provider: k8sProvider, dependsOn: [rel] },
    );

const codexExecRouteCN = makeCodexExecRoute(`crd-${name}-codex-exec-cn`, "codex-exec.agent.cs.ac.cn");
const codexExecRoute   = makeCodexExecRoute(`crd-${name}-codex-exec`,    "codex-exec.agentserver.dev");
```

资源 logical name 保持 (`crd-${name}-codex-exec-cn` / `crd-${name}-codex-exec`)，
Pulumi 会 in-place update（只改 spec.rules）而非 delete+create。HTTPRoute spec
变更对 Istio 来说是 xDS 推送，秒级生效，已建立的 WS 连接不受影响。

**Gateway API v1 longest-prefix-match 规则：** 同一 HTTPRoute 内多 rule，更长的
PathPrefix 优先匹配。`/codex-exec/`（13 字符）和 `/cloud/`（7 字符）都长于
`/`，因此前者优先。详见
<https://gateway-api.sigs.k8s.io/api-types/httproute/#matches>。Istio 1.21+ 实现
符合规范，但 §8 e2e 必须显式验证。

### 7.4 上线顺序（硬约束）

1. **PR 1（agentserver）：** 抽 `wsticket` 子包 + 加 edge 代码 + Dockerfile +
   helm template + CI 作业 + chart 版本 bump → merge → 自动构建并推送
   `ghcr.io/agentserver/codex-exec-edge:<tag>` → push `v<tag>` git tag
   触发 chart 打包
2. **Helm 升级（k8s 仓库 pulumi up）：** chart 升级到新版本，edge Deployment +
   Service 创建。HTTPRoute **不动**。edge 此时空载。
3. **必须 gate：** `kubectl -n agentserver get deploy/agentserver-codex-exec-edge \
   -o jsonpath='{.status.readyReplicas}'` 必须返回 `2`，且
   `curl -k https://codex-exec.agent.cs.ac.cn/healthz` 仍走 gateway（HTTPRoute
   没变）应返回 200 ok。
4. **PR 2（k8s 仓库）：** 改 HTTPRoute，把 `/codex-exec/*` + `/cloud/*` 切到
   edge backend → pulumi up → 流量切换。
5. **回归 + 混沌测试**（§8）。
6. **回退方案：** PR 2 出问题就 revert 该 commit + pulumi up，流量切回直
   连 gateway；edge Deployment 仍在，旁路无影响。

## 8. 测试与验证

### 8.1 单元测试

- `wsticket.VerifyWSTicket` 已有测试（迁移时同步迁过去），覆盖签名 / TTL /
  exe_id 错配
- `wsproxy_test.go`：
  - 验签失败返 401
  - upstream dial 失败返 502
  - 正常 bidir 字节透传（fake upstream echo）
  - close code 透传
- `registerproxy_test.go`：
  - 2xx 立刻透传
  - 4xx 不重试
  - 5xx 重试到成功
  - 5xx 重试到 deadline 后透传最后状态
  - body > 1MB 返 413
  - client cancel 中断重试
  - X-Forwarded-For 正确设置

### 8.2 集成测试（在 `internal/codexexececdge/`）

- 起真实 edge（in-process）+ fake gateway（httptest），跑完整 WS 升级 +
  bidir 帧 + 关闭

### 8.3 e2e 验证（手工 / 上线前）

- `curl https://codex-exec.host/healthz` → 200，hit gateway（HTTPRoute 路径
  优先级正确）
- `curl https://codex-exec.host/bridge/foo` → hit gateway（注意 405，因为
  bridge 是 GET 升级，但 backend 命中正确就够）
- `curl -X POST https://codex-exec.host/cloud/environment/test/register` → hit
  edge → hit gateway → 401（auth 失败但路径正确）
- 起 codex CLI 注册 → 看到 WS 建立
- `kubectl rollout restart deployment/agentserver-codex-exec-gateway` →
  observe：
  - codex CLI **不**退出
  - codex CLI 日志可见一次 WS 断 + sleep + register（这次被 edge hold）+
    register 200 + WS reconnect
  - 总下线窗口 < 30s

### 8.4 Observability

新增 / 关注指标：

- edge：register retry attempts 直方图（按是否 retry / 总耗时）
- edge：upstream dial 失败计数
- gateway：cloud_register handler 处理延迟（baseline + p99）
- gateway：inbound WS 接入速率（用于发现 thundering herd）

具体 metric exporter 实现不在本 spec 范围（edge 至少 slog JSON 输出 +
基础 process metrics 即可，详细 Prometheus 接入作为后续增量）。

## 9. 风险与未来扩展

### 9.1 已知遗留风险

- **in-flight 工具调用仍会失败：** gateway 重启时，正在 `/bridge/*` 上的 WS 会
  断，对应工具调用对 codex 来说是错误。本方案不解决；codex CLI 仍然存活，
  下一次工具调用 OK。
- **gateway 长时间不可恢复：** 超过 30s edge retry 上限，仍透传 503，codex
  进程退出。这与现状无异。
- **edge 自身 bug 导致下游全断：** 通过 `replicas: 2` + RollingUpdate 缓解；
  edge 二进制极简，bug 面小。

### 9.2 未来扩展方向

- **B 计划（增量）：** edge 做 WS 假活，gateway 重启 connector 完全无感。
  ~600 LOC，与 gateway inbound 帧格式耦合。等到 A 上线后看实际诉求。
- **edge → multi-region gateway 路由：** 若未来 gateway 按 workspace 分片，
  edge 可成为分片路由层。

## 10. 实施估算

- 抽 `wsticket` 子包：0.5 天
- edge 代码 + 单测：2 天
- Helm + CI 改动：0.5 天
- 集成测试 + e2e 验证：1 天
- Pulumi PR + 上线 + 混沌验证：0.5 天

**总：~4.5 工作日。**
