<h1 align="center">agentserver</h1>

<p align="center">
  <strong>Your Personal Compute Network — command devices anywhere, from your WeChat chat window.</strong>
</p>

<p align="center">
  English &nbsp;·&nbsp; <a href="README.zh.md">简体中文</a>
</p>

<p align="center">
  <a href="https://agent.cs.ac.cn"><img src="https://img.shields.io/badge/Try%20Now-agent.cs.ac.cn-blue?style=for-the-badge" alt="Try Now"></a>
</p>

<p align="center">
  <a href="https://github.com/agentserver/agentserver/actions"><img src="https://github.com/agentserver/agentserver/actions/workflows/build.yml/badge.svg" alt="Build"></a>
  <a href="https://github.com/agentserver/agentserver/blob/main/LICENSE"><img src="https://img.shields.io/github/license/agentserver/agentserver" alt="License"></a>
  <a href="https://github.com/agentserver/agentserver/releases"><img src="https://img.shields.io/github/v/release/agentserver/agentserver" alt="Release"></a>
</p>

---

<p align="center">
  <img src="assets/step-3-device-connected.png" alt="agentserver Connectors view — nine devices online across Nanjing, ByteDance, Singapore, Xi'an, Kunshan, Zhengzhou" width="820">
</p>
<p align="center"><sub><em>Nine of one user's personal devices — across data centers and laptops in multiple cities — all online in one workspace.</em></sub></p>

> 📖 Read the full vision: [Overview of agentserver](Overview%20of%20agentserver.pdf) (slide deck, Apr 2026)

agentserver turns the laptops, desktops, cloud sandboxes, and even the phones scattered across your life into **one Personal Compute Network** — a single workspace you can command from a browser, a [codex](https://developers.openai.com/codex/cli) CLI, a Jupyter notebook, or a WeChat chat window. Each enrolled machine becomes a *Connector*; each session you drive from is a *Browser*. agentserver is the control plane that registers them, brokers their credentials, routes your prompts, and lets you (and your collaborators) drive everything from one place.

It is the answer to a question Addy Osmani frames as the path from L1 (no AI) to L8 (build your own orchestrator)*: once you are juggling 10+ agents across machines, you stop being a *conductor* and become an *orchestrator*. agentserver is the orchestration layer.

<sub>* Addy Osmani, Director, Google · Gemini & Cloud AI — <a href="https://talks.addy.ie/oreilly-codecon-march-2026">talks.addy.ie/oreilly-codecon-march-2026</a></sub>

### How it differs from what already exists

| Tool | Local agents | Cloud sandboxes | Cross-device peering | Chat-app channel |
|------|:---:|:---:|:---:|:---:|
| OpenClaw / Claude Code Remote | one at a time | — | — | — |
| Claude Code on the web | — | ✅ | — | — |
| Claude Code Agent Teams | — | ✅ (subagents) | — | — |
| **agentserver** | **✅ many** | **✅** | **✅** | **✅ (WeChat / Telegram / Matrix)** |

## Why agentserver?

- **Command from your pocket** — Drive your agents from a WeChat / Weixin, Telegram, or Matrix chat. No terminal required when you are away from the desk.
- **One workspace, every device** — Cloud sandboxes, local laptops/desktops, and IM-bound agents all register into the *same* workspace and show up side-by-side in the Web UI.
- **Codex-native** — Built around the [OpenAI codex](https://developers.openai.com/codex/cli) CLI: devices enroll with `codex exec-server --remote`, you drive them from `codex --remote`. No custom client to install on each machine.
- **Sandboxes that pause and resume** — Per-task containers with idle auto-pause, running under Kubernetes with [Agent Sandbox](https://github.com/kubernetes-sigs/agent-sandbox) + gVisor for true multi-tenant isolation.
- **"Old-school" coding still welcome** — A built-in Jupyter notebook lets users who prefer hand-written code talk to the same workspace, the same files, and the same credentials the agents use.
- **Multi-user collaboration** — Invite friends or teammates into your Personal Compute Network; role-based access (owner / maintainer / developer / guest) decides who can do what.
- **Credential & LLM proxy** — Connectors never see real provider keys; per-workspace RPD quotas and usage tracking are enforced server-side.
- **SSO ready** — GitHub OAuth and generic OIDC (Keycloak, Authentik, …).

## Using the hosted instance (7 steps)

The fastest way to feel what agentserver does is to use the managed instance at **[agent.cs.ac.cn](https://agent.cs.ac.cn)**. Self-hosters get the same flow against their own domain.

### 1. Register an account

Sign up at [https://agent.cs.ac.cn](https://agent.cs.ac.cn).

### 2. Link a model account

Bring your own ChatGPT / Anthropic / API-key credential, or pick one of the managed model providers offered on-platform.

<p align="center">
  <img src="assets/step-2-model-binding.png" alt="LLM Provider tab — connect ModelServer or a custom provider" width="780">
</p>

### 3. Plug devices into the network

Install codex on every machine you want to enroll — laptop, desktop, home server, cloud VM:

```bash
# macOS
brew install codex

# everywhere else
npm i -g @openai/codex
```

In the Web UI, generate a registration command from the **Connectors** tab and run it on the device under `tmux`, `systemd`, or any detached supervisor so the connector survives logout:

<p align="center">
  <img src="assets/step-3-device-connect.png" alt="codex exec-server --remote registering as a Connector" width="780">
</p>

The device shows up as **Online** alongside everything else in your workspace:

<p align="center">
  <img src="assets/step-3-device-connected.png" alt="Nine connectors online across cities" width="780">
</p>

### 4. Pick a "command machine" (Browser)

A *Browser* is a codex client you actually type into — usually your daily-driver laptop. Generate a Browser token from the **Browsers** tab and the printed `codex --remote …` command turns that machine into a command center that can dispatch work to any Connector:

<p align="center">
  <img src="assets/step-4-command-machine.png" alt="Browsers tab — Token generated dialog with codex --remote command" width="780">
</p>

### 5. (Optional) Open a Jupyter notebook

Prefer writing code by hand? Spin up a notebook environment from the Web UI. `ctx` is pre-injected into every kernel and gives you the same Connectors, files, and credentials the agents use:

<p align="center">
  <img src="assets/step-5-jupyter.png" alt="Jupyter notebook using ctx.env('debian-devbox-sg').shell(…) and read_file" width="780">
</p>

We call this the "**old-school**" path — same workspace, no LLM in the loop unless you want one.

### 6. Bind WeChat / Weixin

Scan a QR code on the platform to attach your personal WeChat account, switch the bound agent to **Codex (via codex-app-gateway)** mode, and you can now type instructions — in natural language — into any WeChat chat and have them executed on the right device:

<p align="center">
  <img src="assets/step-6-wechat.png" alt="IM Channels — WeChat bot bound, agent set to Codex via codex-app-gateway, Telegram and Matrix also available" width="780">
</p>

This is the headline experience: **command compute from anywhere your phone has signal.**

### 7. Invite collaborators

Add friends or teammates to your workspace so they can share Connectors, Browsers, and credentials with role-scoped permissions:

<p align="center">
  <img src="assets/step-7-collaboration.png" alt="Members tab — owner and maintainer roles" width="780">
</p>

## Architecture

```
                  World (OpenAI, Anthropic, GitHub, …)
                          ▲
                          │ egress
              ┌───────────┴────────────┐
              │  credentialproxy /     │
              │  llmproxy (:8081)      │
              │  • key injection       │
              │  • RPD quota / usage   │
              └───────────┬────────────┘
                          │
WeChat / Telegram ──▶ imbridge ──▶ ┐
Web Console ──────▶ agentserver  ──┤    ┌──────────────────┐
                     (:8080)       │    │ sandbox pod /    │
                     • REST API    ├───▶│ container        │
                     • Web UI      │    │ └─ codex         │
                     • registry    │    └──────────────────┘
                          │        │
                          │        └──▶ local Connector (laptop, desktop, HPC, …)
                          │              └─ codex exec-server --remote
                          ▼
                     PostgreSQL
                  (users, workspaces,
                   connectors, browsers,
                   quotas, sessions)

Browser (codex)  ──▶ codex-app-gateway  (:8086) ─▶ per-workspace codex app-server subprocess
Jupyter notebook ──▶ codex-app-gateway  (:8086) ─▶ same path, shared `ctx` runtime
Connector (codex)──▶ codex-exec-gateway (:6060) ─▶ rendezvous for `codex exec --remote` executors
Sandbox URLs     ──▶ sandboxproxy       (:8082) ─▶ subdomain routing to sandbox services
```

| Service | Default Port | Role |
|---------|-------------|------|
| **agentserver** | `:8080` | Main API, Web UI, connector / browser / member registry |
| **llmproxy** | `:8081` | LLM API proxy with per-workspace rate limiting and usage tracking |
| **sandboxproxy** | `:8082` | Subdomain-based routing to sandbox services |
| **credentialproxy** | — | Server-side injection of provider credentials |
| **imbridge** | — | IM channel bridge (WeChat / Weixin, Telegram, Matrix) |
| **codex-app-gateway** | `:8086` | Per-workspace codex app-server subprocess + ws bridge for Browser sessions and Jupyter clients |
| **codex-exec-gateway** | `:6060` | Rendezvous endpoint for `codex exec-server --remote` Connectors |

### Where this is heading

- **Stateless harness** — Decouple the *brain* (model + harness) from the *hands* (Connectors and tools). Sessions are append-only event logs that live outside the context window. Workers are *cattle, not pets* — a worker that dies mid-turn loses nothing.
- **Hybrid cloud–local mesh** — Cloud and local Connectors share one workspace registry. Discovery happens through agent cards; the LLM picks a tool and a router decides where the call goes. *Agent discovery, not network mesh.*
- **Async collaboration via mailboxes** — Agents hand off work through inboxes in durable storage. The receiver does not need to be alive when the message is sent. The mailbox is the source of truth.

## Self-Hosting

### Helm (Kubernetes — recommended)

```bash
helm install agentserver oci://ghcr.io/agentserver/charts/agentserver \
  --namespace agentserver --create-namespace \
  --set database.url="postgres://user:pass@postgres:5432/agentserver?sslmode=disable" \
  --set ingress.enabled=true \
  --set ingress.host="cli.example.com" \
  --set baseDomain="cli.example.com"
```

### Pre-built Binaries

Download from [GitHub Releases](https://github.com/agentserver/agentserver/releases), or install via Homebrew:

```bash
brew install agentserver/tap/agentserver
```

## Configuration

See the [API reference](docs/api-reference.md) for full endpoint documentation.

<details>
<summary><strong>Helm Values</strong></summary>

| Parameter | Description | Default |
|-----------|-------------|---------|
| `image.repository` | Server image | `ghcr.io/agentserver/agentserver` |
| `image.tag` | Server image tag | `latest` |
| `database.url` | PostgreSQL connection string | (required) |
| `backend` | Sandbox backend | `k8s` |
| `baseDomain` | Base domain for subdomain routing | `""` |
| `baseScheme` | URL scheme for generated URLs | `https` |
| `idleTimeout` | Auto-pause idle sandboxes after | `30m` |
| `persistence.sessionStorageSize` | Per-sandbox ephemeral storage | `5Gi` |
| `persistence.userDriveSize` | Per-workspace shared disk size | `10Gi` |
| `persistence.storageClassName` | Storage class for PVCs | `""` (cluster default) |
| `workspace.resources` | Resource limits/requests for sandbox pods | `1Gi/1cpu` limits |
| `agentSandbox.install` | Install Agent Sandbox controller | `true` |
| `ingress.enabled` | Enable Nginx Ingress | `false` |
| `ingress.host` | Ingress hostname | `agentserver.example.com` |
| `ingress.tls` | Enable TLS (cert-manager) | `false` |
| `gateway.enabled` | Enable Gateway API HTTPRoute | `false` |

</details>

<details>
<summary><strong>Environment Variables (Main Server)</strong></summary>

| Variable | Description | Default |
|----------|-------------|---------|
| `DATABASE_URL` | PostgreSQL connection string | (required) |
| `BASE_DOMAIN` | Base domain for subdomain routing | - |
| `BASE_SCHEME` | URL scheme (`http` or `https`) | `https` |
| `IDLE_TIMEOUT` | Auto-pause timeout (e.g. `30m`) | `30m` |
| `LLMPROXY_URL` | Base URL of the LLM proxy service | - |
| `PASSWORD_AUTH_ENABLED` | Enable password-based auth | `true` |
| `OIDC_REDIRECT_BASE_URL` | External URL for OIDC callbacks | - |
| `GITHUB_CLIENT_ID` | GitHub OAuth client ID | - |
| `GITHUB_CLIENT_SECRET` | GitHub OAuth client secret | - |
| `OIDC_ISSUER_URL` | Generic OIDC issuer URL | - |
| `OIDC_CLIENT_ID` | Generic OIDC client ID | - |
| `OIDC_CLIENT_SECRET` | Generic OIDC client secret | - |
| `SANDBOX_NAMESPACE_PREFIX` | K8s namespace prefix | `agent-ws` |
| `NETWORKPOLICY_ENABLED` | Enable K8s NetworkPolicy isolation | `false` |
| `NETWORKPOLICY_DENY_CIDRS` | CIDRs to deny in network policies | - |
| `AGENTSERVER_NAMESPACE` | agentserver's own K8s namespace | - |
| `STORAGE_CLASS` | K8s storage class for PVCs | (cluster default) |
| `USER_DRIVE_SIZE` | Per-workspace storage size | `10Gi` |
| `USER_DRIVE_STORAGE_CLASS` | Storage class for workspace drives | inherits `STORAGE_CLASS` |
| `INTERNAL_API_SECRET` | Shared secret for internal endpoints (recommended) | - |

</details>

<details>
<summary><strong>Environment Variables (LLM Proxy)</strong></summary>

| Variable | Description | Default |
|----------|-------------|---------|
| `LLMPROXY_LISTEN_ADDR` | HTTP listen address | `:8081` |
| `LLMPROXY_DATABASE_URL` | Proxy's own PostgreSQL connection URL | - |
| `LLMPROXY_AGENTSERVER_URL` | agentserver internal API URL for token validation | (required) |
| `LLMPROXY_DEFAULT_MAX_RPD` | Default max requests per day per workspace (0 = unlimited) | `0` |

</details>

<details>
<summary><strong>OIDC Authentication</strong></summary>

**GitHub OAuth:**

```bash
helm upgrade agentserver oci://ghcr.io/agentserver/charts/agentserver \
  --reuse-values \
  --set oidc.redirectBaseUrl="https://cli.example.com" \
  --set oidc.github.enabled=true \
  --set oidc.github.clientId="your-client-id" \
  --set oidc.github.clientSecret="your-client-secret"
```

Callback URL: `https://cli.example.com/api/auth/oidc/github/callback`

**Generic OIDC (Keycloak, Authentik, etc.):**

```bash
helm upgrade agentserver oci://ghcr.io/agentserver/charts/agentserver \
  --reuse-values \
  --set oidc.redirectBaseUrl="https://cli.example.com" \
  --set oidc.generic.enabled=true \
  --set oidc.generic.issuerUrl="https://idp.example.com/realms/main" \
  --set oidc.generic.clientId="agentserver" \
  --set oidc.generic.clientSecret="your-secret"
```

</details>

## Building from Source

```bash
# Prerequisites: Go 1.26, Node.js, pnpm, bun

# Build everything (frontend + backend)
make build

# Build individual components
make backend          # Go binary → bin/agentserver
make frontend         # React frontend → web/dist/
make llmproxy         # LLM proxy binary → bin/llmproxy
```

## Contributing

```bash
# Terminal 1: Start backend
go run . serve --db-url "postgres://..."

# Terminal 2: Start frontend dev server
cd web && pnpm install && pnpm dev
```

Pull requests welcome — the repo is dogfooded against itself.

## Community & Contact

- **Hosted instance** — [agent.cs.ac.cn](https://agent.cs.ac.cn) (closed beta — sign up and we'll let you in)
- **Issues & feature requests** — [github.com/agentserver/agentserver/issues](https://github.com/agentserver/agentserver/issues)
- **Business / partnership inquiries** — [agentserver@mryao.org](mailto:agentserver@mryao.org)
- **Like the project?** ⭐ a star on GitHub helps more people find it.

## License

[MIT](LICENSE)
