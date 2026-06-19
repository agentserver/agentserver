# codex-exec-gateway Noise Relay design

> Status: draft 2026-06-18 (rev 2 — post-review fixes)
> Author: claude + mryao
> Related: `2026-05-05-codex-app-gateway-and-exec-gateway-design.md`,
>          `2026-05-17-codex-exec-gateway-bridge-multiplexing.md`,
>          `2026-05-23-codex-exec-gateway-audit-design.md`
>
> Rev 2 changes: addressed 7 review findings — sandbox auth (§3.4a),
> upstream-plaintext-bridge risk elevated (§8), Phase 1 milestone + FFI
> fallback (§7), single-replica constraint locked-in (§6 D-3), bridge-
> session/tool-call mapping clarified (§4.0), key-rotation grace period
> spelled out (§3.1.7), legacy mode EOL date set (§6 D-1).

## 0. Problem

Upstream codex (main 2026-06 onward) introduced "Noise Relay" — when
an executor registers via `codex exec-server --remote {gateway} --environment-id {id}`
(the only way an executor without a public IP can be reached), the
executor and harness do end-to-end Noise Hybrid IK (Curve25519 +
ML-KEM-768 + AES-GCM + SHA-256). The relay sees only ciphertext.

Concretely two things broke against today's `codex-exec-gateway`:

1. **Executor registration fails** — Gateway's `POST /cloud/executor/{id}/register`
   doesn't return the `security_profile` / `executor_registration_id` fields
   that codex now requires. Symptom (user-reported):
   ```
   Error: environment registry request failed: error decoding response body
   missing field `security_profile` at line 1 column 364
   ```

2. **Bridge stops being usable on noise-mode executors** — once an executor
   has registered as noise, its inbound loop (`run_multiplexed_environment`)
   rejects plaintext `RelayData` frames (`stream_id not found, send_reset`).
   Today's bridge code in `codex-exec-gateway` forwards plain JSON-RPC
   bytes; that path fails the moment executor is in noise mode.

Audit (`internal/codexexecgateway/audit/rpcparser.go`) reads plaintext
JSON-RPC bytes from the bridge path. If we just transparently pass
ciphertext, audit silently goes blank.

### Constraints

- We can't change codex itself (upstream binary, used directly by users on
  their own machines as executors).
- We can change `codex-exec-gateway`, the codex-app-gateway spawn flow,
  `envmcp-public-gateway`, executor sandbox boot scripts.
- We must keep audit working (operator policy: every shell call into a
  registered executor is logged).
- Single-tenant trust model: gateway is in the same trust boundary as
  the user (we deploy it, they grant us OAuth to act on their workspace).
  Gateway holding cleartext is no worse than today's PAT / OAuth model.

## 1. Goals

| # | Goal |
|---|------|
| G1 | `codex exec-server --remote ... --environment-id ...` executors work end-to-end (both cluster-internal sandboxes and user-macbook reverse-dial executors). |
| G2 | Three harness/client surfaces all work transparently: (a) codex-app-gateway-spawned codex processes, (b) envmcp-public-gateway MCP tool invocations from local Claude Code / Codex / Claude Desktop, (c) any SDK or 3rd-party client hitting bridge through OAuth bearer. |
| G3 | Audit pipeline (`bridge.go` + `rpcparser.go`) continues to see plaintext JSON-RPC for every tool call routed through gateway. |
| G4 | Legacy executors (`codex exec-server --listen ws://...`, no noise registration) keep working unchanged. |
| G5 | One Go-side Noise implementation, owned by `codex-exec-gateway`, used for all noise traffic. No noise code in `envmcp-public-gateway`, `codex-app-gateway`, or anywhere else. |
| G6 | Minimal user-facing change: no new CLI flags, no extra commands for end-users. |

### Non-goals

- Implementing other Noise patterns (XX, NK, etc.) — IK Hybrid only.
- Supporting non-codex MCP servers — this design is specific to the codex
  exec-server protocol over our relay.
- Replacing OAuth / PAT (the bearer/auth layer is orthogonal).

## 2. High-level architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│ Cluster                                                              │
│                                                                      │
│  [Browser user]                                                      │
│     ↓ ws (codex-app protocol)                                        │
│  codex-app-gateway                                                   │
│     ↓ spawn `codex app-server` subprocess (per-turn harness)         │
│     │   config_dir written with:                                     │
│     │     [environments.devbox]                                      │
│     │     url = "wss://codex-exec-gateway/bridge/exe_xxx?role=harness"│
│     │                                                                │
│  [codex harness subprocess]                                          │
│     ↓ ws "?role=harness" (PLAINTEXT bridge, no noise in harness)     │
│     │                                                                │
│  [Local user's Claude Code / Codex CLI]                              │
│     ↓ HTTPS Bearer (MCP OAuth)                                       │
│  envmcp-public-gateway                                               │
│     ↓ internal ws (PLAINTEXT bridge)                                 │
│     │                                                                │
│  [Other SDK / 3rd-party client]                                      │
│     ↓ HTTPS Bearer / mTLS (whatever the bridge access policy is)     │
│     ↓ ws "?role=harness" (PLAINTEXT bridge)                          │
│     │                                                                │
│     ▼                                                                │
│  codex-exec-gateway                                                  │
│  ┌──────────────────────────────────────────────────────────┐       │
│  │ Bridge entry (existing): /bridge/{exe_id}?role=harness   │       │
│  │   ↓ rpcparser audit (plaintext JSON-RPC)                 │       │
│  │   ↓                                                       │       │
│  │ Noise wrapper (NEW)                                       │       │
│  │   1. lookup executor by exe_id                            │       │
│  │   2. mode = legacy (--listen)? → plain RelayData pass-thru│       │
│  │   3. mode = noise (--remote --environment-id)? →          │       │
│  │      a. open new noise virtual stream on physical WS      │       │
│  │      b. run noise IK initiator handshake (with executor)  │       │
│  │      c. encrypt each plaintext frame as RelayData         │       │
│  │      d. decrypt incoming RelayData back to plaintext      │       │
│  │      e. forward decrypted to bridge client                │       │
│  └──────────────────────────────────────────────────────────┘       │
│     │ physical ws to executor (RelayMessageFrame protobuf)            │
│     │                                                                 │
└─────┼────────────────────────────────────────────────────────────────┘
      ↓ wss (executor dials in)
┌─────────────────────────┐    ┌─────────────────────────────┐
│ Cluster sandbox          │    │ User's macbook              │
│ codex exec-server        │    │ codex exec-server           │
│   --remote ...           │ or │   --remote ...              │
│   --environment-id ...   │    │   --environment-id ...      │
│   (noise responder)      │    │   (noise responder)         │
└─────────────────────────┘    └─────────────────────────────┘
```

Two key shifts vs today:

1. **All harness/client surfaces are plaintext bridge clients to gateway**.
   The codex-app-gateway-spawned subprocess uses the legacy
   `?role=harness` bridge URL (codex still supports this — `is_rendezvous_harness_url()`
   returns true; the code path uses `harness_connection_from_websocket`
   which sends plain RelayData payloads, not noise frames).

2. **Gateway is the only Noise endpoint on our side**. Gateway speaks
   Noise IK with the executor. From gateway's POV it is "the harness"
   for noise purposes. Bridge clients don't know noise exists.

## 3. Component-by-component changes

### 3.1 codex-exec-gateway (the big one)

#### 3.1.1 Replace executor registration

Today: `POST /cloud/executor/{exe_id}/register`, returns plain
`{id, executor_id, environment_id, url}`.

Replace with: `POST /cloud/environment/{env_id}/register` matching
upstream codex's `EnvironmentRegistryRegistrationRequest` /
`EnvironmentRegistryRegistrationResponse`.

```go
type RegisterRequest struct {
    SecurityProfile   string `json:"security_profile"`   // "noise_hybrid_ik_v1"
    ExecutorPublicKey string `json:"executor_public_key"` // base64
}
type RegisterResponse struct {
    EnvironmentID          string `json:"environment_id"`
    URL                    string `json:"url"`                       // wss://...gateway/cloud/relay/{registration_id}
    SecurityProfile        string `json:"security_profile"`          // "noise_hybrid_ik_v1"
    ExecutorRegistrationID string `json:"executor_registration_id"`  // uuid
}
```

Side effect on registration:
- Store `(env_id, executor_public_key, executor_registration_id)` in DB
  (replaces today's `codex_executors` table, or extends it).
- Mint a `registration_id` (UUID); persist.
- Return `url` pointing at a WebSocket endpoint on our gateway that
  this `registration_id` will dial in to (next step).

Keep the legacy `POST /cloud/executor/{exe_id}/register` route alive
for backwards compat with `--listen`-mode executors, OR drop it and
require all executors to use `--remote` mode (decision below in §6).

#### 3.1.2 Physical WS endpoint for executor

New: `WSS /cloud/relay/{registration_id}` — executor dials this URL
(received in registration response), gateway upgrades and starts a
goroutine pumping `RelayMessageFrame`s in both directions.

This is the "physical" relay WS in noise terminology. Multiple noise
virtual streams will be multiplexed over it (one per bridge client
session).

State:

```go
type ExecutorRegistration struct {
    EnvID               string
    RegistrationID      string
    ExecutorPubKey      noise.PublicKey   // {Curve25519, ML-KEM-768}
    PhysicalWS          *websocket.Conn   // long-lived after executor dials
    InboundFrames       chan RelayFrame   // from executor → gateway dispatcher
    OutboundFrames      chan RelayFrame   // gateway → executor
    NoiseStreams        map[string]*NoiseStream  // stream_id → state
    NoiseStreamsLock    sync.RWMutex
}
```

Gateway maintains a global `map[envID]*ExecutorRegistration`.

#### 3.1.3 Noise IK initiator (Go)

New package `internal/codexexecgateway/noise`.

Implementing **noise_hybrid_ik_v1** as defined by codex (= `clatter`
crate with the `HybridHandshake<X25519, MlKem768, MlKem768, AesGcm, Sha256>`
profile).

Build on:
- [github.com/flynn/noise](https://github.com/flynn/noise) — classic
  Noise (provides X25519 + AES-GCM + SHA-256 IK pattern, mature
  library, used by Tailscale).
- [github.com/cloudflare/circl/kem/mlkem/mlkem768](https://github.com/cloudflare/circl)
  — ML-KEM-768 (Kyber) post-quantum KEM.
- Custom glue layer to integrate ML-KEM into the IK handshake message
  layout the same way clatter does (KEM ephemeral encapsulation
  interleaved between the `e` and `s` DH operations).

Key API surface:

```go
package noise

// PublicKey is the wire format codex uses: {dh: 32B Curve25519, kem: 1184B ML-KEM-768}.
type PublicKey struct {
    DH  [32]byte
    KEM [1184]byte
}

// Identity is a (DH, KEM) keypair belonging to one endpoint.
type Identity struct {
    DH       [32]byte // X25519 private
    KEM      [2400]byte // ML-KEM-768 secret key
    Public   PublicKey
}

// InitiatorHandshake runs the IK handshake from initiator side.
// `responderPubKey` is the peer (executor) static pub key.
// `prologue` matches codex's noise_channel_prologue.
// `payload` is the in-band data (harness_key_authorization).
type InitiatorHandshake struct { ... }

func NewInitiator(id *Identity, responderPubKey *PublicKey,
                  prologue []byte, payload []byte) (*InitiatorHandshake, error)

// WriteMessage1 produces the first IK message (initiator → responder)
// containing encrypted (s, payload) and KEM ciphertext.
func (h *InitiatorHandshake) WriteMessage1() ([]byte, error)

// ReadMessage2 consumes the responder's reply and finalises the handshake.
// On success returns a Transport.
func (h *InitiatorHandshake) ReadMessage2(in []byte) (*Transport, error)

// Transport is an active session after handshake.
type Transport struct { ... }

// Encrypt produces ciphertext (with implicit nonce-incrementing).
func (t *Transport) Encrypt(plaintext []byte) ([]byte, error)

// Decrypt consumes ciphertext (with implicit nonce-incrementing).
func (t *Transport) Decrypt(ciphertext []byte) ([]byte, error)
```

**Bit-compatibility is the hard part.** Testing strategy:
- Run `cargo test` in codex's `noise_channel_tests.rs` to capture
  fixture inputs/outputs.
- Write Go-side tests that exercise the same fixtures.
- Manual end-to-end test against a real codex executor before merging.

Budget: **2-3 weeks** of focused implementation + bit-compat debugging.

#### 3.1.4 Per-bridge-session noise virtual stream lifecycle

When a bridge client opens `ws /bridge/{exe_id}?role=harness`:

```
1. Bridge handler accepts WS, runs auth (existing OAuth/bearer code).
2. Bridge handler audits inbound JSON-RPC frames via rpcparser (existing).
3. Bridge handler calls NoiseWrapper.AttachStream(envID, bridgeWS):
   a. NoiseWrapper looks up ExecutorRegistration for envID.
   b. If executor mode = legacy (no registration) → falls back to
      today's plain RelayData passthrough.
   c. If executor mode = noise:
      - Generate fresh stream_id (UUID).
      - Generate fresh per-session noise Identity (Gateway's static
        identity is shared per-process; per-session ephemeral keys
        come from the handshake itself).
      - Sign a harness_key_authorization HMAC:
          token = HMAC-SHA256(gateway_secret,
                              env_id || stream_id ||
                              executor_pubkey || expiry_unix)
        Embed expiry; default 5 min.
      - Build InitiatorHandshake with:
          identity = gateway's noise Identity (per-process, persistent)
          responderPubKey = executor's registered public key
          prologue = codex's noise_channel_prologue(env_id, exec_reg_id, stream_id)
          payload = harness_key_authorization bytes
      - Call WriteMessage1 → ciphertext bytes.
      - Wrap into RelayMessageFrame{stream_id, body: RelayHandshake{payload: ciphertext}}.
      - Send on ExecutorRegistration.OutboundFrames.
      - Wait for executor's reply (RelayHandshake on inbound side
        with same stream_id).
      - Call ReadMessage2 → Transport.
      - Register NoiseStream{stream_id, transport, bridge_ws} in
        ExecutorRegistration.NoiseStreams.
   d. Start two pumps:
      - Frame pump A: read plain JSON-RPC from bridge WS → audit
        → Transport.Encrypt → wrap as RelayData{stream_id, seq++, ciphertext}
        → push to OutboundFrames.
      - Frame pump B: receive RelayData from inbound (matched by stream_id)
        → Transport.Decrypt → audit (optional response audit) → write
        plain JSON-RPC to bridge WS.
4. On bridge WS close: send RelayReset{stream_id, reason} on outbound,
   delete from NoiseStreams.
5. On physical WS close: cancel all NoiseStreams for that registration,
   send disconnect to all bridge WSs, mark registration as down (executor
   will re-dial; new registration when it does).
```

#### 3.1.5 Validation endpoint

Executor calls `POST /cloud/environment/{env_id}/validate` during its
own handshake processing to check harness_key_authorization is real.

```go
type ValidateRequest struct {
    ExecutorRegistrationID  string `json:"executor_registration_id"`
    HarnessPublicKey        string `json:"harness_public_key"`         // base64
    HarnessKeyAuthorization string `json:"harness_key_authorization"`  // our HMAC string
}
type ValidateResponse struct {
    Valid bool `json:"valid"`
}
```

Implementation: parse the HMAC token (env_id || stream_id || executor_pubkey || expiry),
recompute HMAC with gateway_secret, constant-time compare, check expiry not passed.

#### 3.1.6 Audit pipeline

No change. `rpcparser` already sees plaintext JSON-RPC frames at the
bridge boundary, before they go into the noise encrypt pump.

Optionally also audit decrypted responses on the way back. Today we
audit only request direction (to keep storage cost down); same here.

#### 3.1.7 Gateway noise Identity lifecycle

Gateway has ONE long-lived noise Identity (DH + KEM keypair) for the
whole codex-exec-gateway process. Stored in a K8s Secret (encrypted
at rest), mounted as files at boot, loaded into memory once.

Rotation: at most quarterly. Rotation procedure:
1. Mint new keypair, store in `gateway-noise-key-v2` secret.
2. Deploy: gateway loads both old and new, prefers new for new sessions,
   keeps old usable for sessions in flight.
3. After 24h, drop old.

Per-session, the noise handshake derives ephemeral keys; the static
identity only matters for executor's optional pinning (which it doesn't
do today since gateway is provisioned at registration time, not
out-of-band).

#### 3.1.8 Legacy `--listen`-mode executor compatibility

Executors that don't call `/register` with noise (`codex exec-server
--listen`) are recognised by the absence of a registration entry. Bridge
handler falls back to today's behavior: dial executor directly,
forward plain RelayData. No noise wrapping. Audit unaffected.

This means `codex-exec-gateway` simultaneously supports:
- Noise-mode executors (via registration + relay WS + noise wrapping)
- Legacy plain-mode executors (via direct dial)

within the same binary.

### 3.2 codex-app-gateway

Today: spawns `codex app-server --listen ws://127.0.0.1:0` and uses it
via JSON-RPC over the listened WS.

For tool calls, the spawned codex consults its config dir's
`environment.toml` to know how to reach `devbox` or whichever named
environment. We need to write that toml so its `url=` points at our
plain bridge URL.

Change in `internal/codexappgateway/supervisor/spawn.go`:
- Compute per-spawn `config_dir` (already happens).
- In that dir, write `environment.toml`:

```toml
[environments.devbox]
url = "wss://codex-exec-gateway.agentserver.svc/bridge/{exe_id}?role=harness&token={short_lived_bearer}"
```

Where:
- `{exe_id}` is resolved by app-gateway (knows the workspace's executor).
- `{short_lived_bearer}` is a freshly minted cap-token (matches the
  existing bridge auth; expires after the turn).

The `?role=harness` query param triggers codex's
`harness_connection_from_websocket` (plaintext, RelayData payload =
JSON-RPC bytes) path on the harness side, exactly what we want.

### 3.3 envmcp-public-gateway

**No change.** It already dials `ws://codex-exec-gateway/bridge/{exe_id}`
(per `internal/mcppublic/bridge_backend.go`). The `?role=harness` query
param is already set today (because of how MCP tool calls translate to
exec-server JSON-RPC). Verify and add `role=harness` if missing.

### 3.4 Executor (cluster sandbox)

User survey confirms: actual users mostly run **self-hosted** executors
(macbook reverse-dial), not cluster sandboxes. Cluster sandbox executor
migration is therefore second-priority — get user-macbook noise working
first, then unify cluster sandbox onto the same path.

When we DO migrate, sandbox boot scripts switch from `--listen` to:

```bash
codex exec-server --remote https://codex-exec-gateway/ \
  --environment-id $EXE_ID \
  --use-agent-identity-auth
```

That requires §3.4a below.

### 3.4a Sandbox executor service-account auth (new)

Reverse-dial mode needs a `CODEX_ACCESS_TOKEN` Bearer for `/register`.
For user macbook, the token comes from interactive `codex login
--experimental_issuer=https://codex-auth.agent.cs.ac.cn` (already wired
via our `agentserver-agent-cli` Hydra client). For sandbox, there is
no interactive flow — we need automated issuance.

Design:

1. **Per-sandbox Hydra OAuth client** — at sandbox creation time,
   agentserver calls Hydra admin API to mint a client_credentials
   grant client named `sandbox-{sandbox_id}` with scope `agent:register`
   and `audience https://agent.cs.ac.cn`. Store client_secret in
   K8s Secret mounted into the sandbox pod.

2. **Sandbox boot script** fetches a fresh token:
   ```bash
   TOKEN=$(curl -s -X POST https://hydra-public/oauth2/token \
     -d "grant_type=client_credentials" \
     -d "client_id=sandbox-${SANDBOX_ID}" \
     -d "client_secret=${SANDBOX_CLIENT_SECRET}")
   export CODEX_ACCESS_TOKEN=$(echo "$TOKEN" | jq -r .access_token)
   export CODEX_AGENT_IDENTITY_AUTHAPI_BASE_URL=https://codex-auth.agent.cs.ac.cn
   codex exec-server --remote ... --environment-id ... --use-agent-identity-auth
   ```

3. **codex-exec-gateway token validation** — gateway needs to verify
   incoming `Bearer` against Hydra. Use `internal/auth/hydra.go`
   `IntrospectToken` (already in agentserver, factor into shared lib
   or HTTP-call into agentserver `/internal/codex-auth/validate`).

4. **Sandbox deletion cleanup** — when workspace tears down sandbox,
   delete the Hydra client (`hydra delete oauth2-client
   sandbox-{sandbox_id}`) to avoid client-table accumulation.

5. **Token rotation inside sandbox** — Hydra access tokens default to
   1h TTL. Sandbox boot script writes a cron / supervisor that
   refreshes the token every 45min and signals codex to pick up
   (codex re-registers on token expiry naturally).

Estimated: **1 week** (Phase 5a, prerequisite for Phase 5).

### 3.5 User's macbook executor

No change — user already runs `codex exec-server --remote ... --environment-id ...`
which is the trigger for noise mode. Today the registration fails with
the missing-`security_profile` error; after this design ships, gateway
returns the right shape and the executor settles into noise mode.

## 4. Scenario walkthroughs

### 4.0 Latency budget per tool call

Per-stream noise handshake cost is paid ONCE per bridge session (per
spawned harness lifetime, typically ≥30 minutes). Per-frame encrypt
cost is paid on every tool-call request and response. Concrete budget
breakdown for cluster-local executor (sandbox in same region):

```
Per tool-call request (harness → executor):
  bridge accept + audit parse        ~0.5ms (existing today)
  NoiseWrapper.Encrypt(plaintext)    ~0.05ms (AES-GCM, 1KB)
  WS write to executor               ~0.5ms RTT
  executor decrypt + execute         varies (shell ≈ 5-50ms)
  encrypt response                   ~0.05ms
  WS back to gateway                 ~0.5ms
  gateway decrypt + forward          ~0.05ms
  ─────────────────────────────────
  Added overhead vs today plaintext: ~0.2ms per round-trip
  Per-session one-time handshake:    ~4ms (network RTT × 2 + KEM)

Per tool-call for user-macbook (reverse-dial) executor:
  + WAN RTT user↔cluster             50-200ms (dominates)
  + same encrypt costs               ~0.2ms (noise)
  ─────────────────────────────────
  Added overhead vs today: irrelevant (<0.5% of WAN RTT)
```

Bottom line: noise overhead is negligible vs existing baselines. The
real cost is the **per-session handshake** (~4ms). Since a harness
session typically issues 10-100+ tool calls, amortized handshake cost
is well under 0.05ms/call.

Bridge-session to noise-stream mapping (single physical WS per
executor, many virtual noise streams):

```
                            ┌─ stream S-1 (harness #1, session age 30min)
exec-gateway ⇄ executor:    ├─ stream S-2 (harness #2, age 5min)
  ONE physical WS,          ├─ stream S-3 (harness #3, age 2s, mid-handshake)
  many noise streams        └─ ...
```

Each new harness bridge → one new noise stream on the existing physical
WS. Stream lifecycle bound to bridge WS lifecycle. Stream reset
propagates to executor for cleanup.

### Scenario A: codex-app-gateway-spawned harness, cluster-sandbox executor

```
T0   User browser: "list documents in workspace"
T0+  codex-app-gateway spawns codex app-server subprocess
      writes config_dir/environment.toml with
        url = wss://exec-gateway/bridge/exe_xxx?role=harness&token=Z
T1   spawned codex evaluates user prompt, plans a tool call (shell ls ~/Documents)
T1+  codex needs to invoke that tool on `devbox` environment
T1+  codex reads environment.toml, sees url, opens WS to the URL
      → codex internal: is_rendezvous_harness_url returns true,
        uses harness_connection_from_websocket (PLAIN RelayData payload)
T2   exec-gateway bridge handler accepts WS, auths token=Z, creates session B-1
T2+  exec-gateway NoiseWrapper looks up exe_xxx — already registered (noise mode)
T2+  NoiseWrapper opens noise virtual stream S-1 on physical WS to executor
       - generates harness_key_auth HMAC for this stream
       - sends RelayHandshake{S-1, encrypted IK msg1 with auth in payload}
T2+  Executor's run_multiplexed_environment receives handshake
       - decrypts payload, gets auth string
       - POSTs /validate to gateway with the auth
       - gateway verifies HMAC, returns valid=true
       - executor sends RelayHandshake{S-1, IK msg2} back
T2+  Gateway completes handshake, derives session_key_S1
T3   codex sends JSON-RPC tool call as RelayData{S-1, seq=0, payload=plain JSON}
T3+  exec-gateway bridge handler:
       - audit: rpcparser sees {"method":"tools/call",...} → records to WAL
       - NoiseWrapper.Encrypt(plaintext) → ciphertext
       - wraps as RelayData{S-1, seq=0, payload=ciphertext}
       - sends on physical WS
T3+  Executor receives, decrypts (NoiseTransport on its side), executes shell
T3+  Executor sends back encrypted RelayData{S-1, seq=0, response payload}
T3+  Gateway decrypts → forwards plain JSON-RPC to spawned codex via bridge WS
T4   codex finishes turn, exits
T4+  bridge WS closes → gateway sends RelayReset{S-1} to executor
T4+  executor cleans up its NoiseVirtualStream S-1
```

### Scenario B: user's local Claude Code via public MCP, macbook executor

```
T0   user@laptop: claude code session, "please list my home directory"
T0+  Claude Code translates to MCP tool call: mcp__agentserver__shell
T1   Claude Code → POST https://mcp.agent.cs.ac.cn/mcp
      Bearer: <user's OAuth token from agentserver-mcp-claude-code client>
T2   envmcp-public-gateway authenticates, looks up workspace,
      finds env "devbox" → exe_yyy (user's macbook executor)
T3   envmcp internal: opens ws://exec-gateway/bridge/exe_yyy?role=harness&token=W
T4   Same NoiseWrapper flow as Scenario A:
       - look up exe_yyy → noise mode (executor is user's macbook)
       - open new stream S-2, handshake, derive session_key_S2
T5   envmcp synthesises codex JSON-RPC for the tool call:
       {"method":"tools/call","params":{"name":"shell","args":["ls","~/"]}}
T5+  Gateway encrypts, forwards to executor on user's macbook
T5+  Executor decrypts, runs shell, encrypts response back
T6   envmcp receives plaintext response, translates to MCP response JSON
T7   Claude Code receives MCP response, renders for user
```

### Scenario C: harness dies mid-turn → new harness spawned

```
T0   spawned codex (harness #1) attached as stream S-1
T1   T-O-T-A-L harness #1 OOM-killed by codex-app-gateway watchdog
T1+  bridge WS to gateway closes (TCP RST or EOF)
T1+  gateway sends RelayReset{S-1} to executor → executor drops stream S-1
T2   user re-issues request (or codex-app-gateway retries the turn)
T2+  new harness #2 spawned, new config_dir, new environment.toml,
      same URL (just a fresh bridge token)
T3   harness #2 opens new bridge WS → gateway creates session B-2
T3+  NoiseWrapper opens stream S-2 (fresh stream_id, fresh handshake,
      fresh session_key_S2, completely independent from S-1)
T4   continues from there
```

The executor's `physical WS` to gateway is NEVER torn down across these
events. Codex's virtual stream multiplexing handles harness churn natively.

### Scenario D: multiple parallel harnesses on one executor

```
   harness_A (turn A for user X) ─→ bridge WS B-A ─→ stream S-A
   harness_B (turn B for user X) ─→ bridge WS B-B ─→ stream S-B
   envmcp call (user Y MCP)      ─→ bridge WS B-C ─→ stream S-C
   SDK call (user Z SDK)         ─→ bridge WS B-D ─→ stream S-D
                                                          │
                                                          ▼
                                                physical WS to exe_xxx
                                                (multiplexed)
```

All four streams share one physical WS. Each has independent session_key,
sequence numbers, RelayReset lifecycle. Audit sees 4 distinct
session_ids in the WAL.

### Scenario E: legacy executor (`--listen` mode), unchanged behavior

```
T0   sandbox boots `codex exec-server --listen ws://0.0.0.0:7777`
T0+  no registration → no entry in gateway's ExecutorRegistration map
T1   bridge client comes via /bridge/{exe_id}?role=harness
T1+  NoiseWrapper.AttachStream sees no registration → falls back to legacy
      passthrough: gateway dials ws://sandbox:7777 directly, pipes plain
      RelayData both ways.
```

Audit still sees plaintext because both ends are plaintext.

## 5. Protocol detail: harness_key_authorization

Codex's noise IK initiator embeds an opaque payload in handshake
message 1. The responder (executor) decrypts it, extracts the bytes,
and asks the registry to validate them. The registry decides if this
harness is authorized to talk to this executor.

Our token format (gateway-internal, opaque to codex):

```
HARNESS_KEY_AUTH := base64url( v1 || env_id_len:u8 || env_id ||
                                stream_id_len:u8 || stream_id ||
                                exec_pubkey_hash:32B ||
                                expiry_unix:u64be ||
                                hmac:32B )
```

Where `hmac` = HMAC-SHA256(gateway_secret, everything-before-hmac).

The `exec_pubkey_hash` binds the token to a specific executor (= SHA-256
of executor's noise PublicKey). This prevents a leaked token from being
replayed against a different executor.

Verification:
- Constant-time HMAC check
- Expiry not passed (default 5 min from issue)
- exec_pubkey_hash matches the executor calling /validate
- (Optional) stream_id matches what's claimed in the validate request

## 6. Decisions & open questions

### Decision 1: keep legacy `--listen` mode? (D-1)

**Keep for 6 weeks then EOL.** Lower initial migration cost — existing
sandbox boot scripts get a deprecation banner, not an immediate break.
After 6 weeks (timed to one Helm release after Phase 5a ships), remove
the `LegacyListen` config flag and reject startup if anyone still passes
`--listen` mode. Rationale: keeping two code paths indefinitely doubles
the audit surface and the integration test matrix forever; the cost of
the second path compounds with every change to noise wrapper, audit
pipeline, or bridge logic.

Mechanism:
- Helm chart `legacyExecListen.enabled: true` for 6 weeks (default true)
- Banner in gateway logs + Slack notification each time legacy session
  starts: `[DEPRECATED] executor X using --listen; EOL 2026-08-15`
- Week 4: chart default flips to `false` (users override to extend)
- Week 6: code path deleted, chart key removed

### Decision 2: `gateway_secret` source (D-2)

**Use a K8s Secret named `codex-exec-gateway-noise-secrets`** mounted
at `/etc/exec-gateway/noise/`. Two files:
- `hmac_key` — 32B random, for harness_key_authorization HMAC.
- `noise_static_dh` + `noise_static_kem` — gateway's per-process static
  identity.

Generated via `kubectl create secret` once at install; rotation via
helm hook or operator action (procedure in §3.1.7).

**Key rotation grace period.** Both keys live in two slots in the
mounted volume: `current/` and `previous/`. On rotation, ops writes new
key to `current/`, demotes existing to `previous/`. Gateway:
- Signs HMAC with `current/` only.
- Validates HMAC accepting either `current/` or `previous/`.
- Initiates new noise handshakes with `current/` static identity.
- Continues honoring in-flight noise sessions established under
  `previous/` until they close (session lifetime = harness lifetime,
  typically <30 min).

Grace period: **24 hours + max session lifetime (6h hard cap)** = 30h
window after rotation before `previous/` may be removed. Tooling: a
`rotate-and-wait` operator command writes new key, sleeps 30h with
periodic check that no session is still bound to `previous/`, then
clears `previous/`. Documented in §3.1.7.

### Decision 3: Gateway noise Identity scope

**One identity per gateway process**, shared across all sessions, all
executors. Simpler than per-session or per-executor identities. Codex
doesn't require harness identity pinning by executors.

### Decision 4: `RelayData.segment_*` reassembly

Codex's RelayData supports segmentation for large frames
(`segment_index`, `segment_count`). Gateway must reassemble before
decrypting (Noise records have max size).

Implementation: per (stream_id, seq_base) collect segments, decrypt
when complete count arrives. Stream gets reset on out-of-order /
duplicate / missing segments (same policy as codex itself).

### Open question A: ML-KEM-768 Go library

`cloudflare/circl/kem/mlkem/mlkem768` is the only mainstream choice as
of 2026-06. Confirm:
- API exposes raw encapsulate/decapsulate (need for IK integration).
- Wire format (3168-byte public key, 1184-byte ciphertext) matches what
  clatter produces.

### Open question B: Mock executor for testing

For CI we need a fake executor that does noise responder side. Options:
- Embed `codex exec-server` binary in CI image, run against it (real
  fidelity but heavy).
- Port clatter's responder side to Go test helper (lighter).

Decide before phase 2 starts.

### Decision 5: Single replica only (D-3)

**Gateway runs as `replicaCount: 1` in Helm chart, full stop.**
Multi-replica defer to follow-up spec.

Reasons:
- Executor registration → relay WS dial must hit the same pod (state
  is per-process). Sticky service routing works at HTTP level but
  WS upgrade complicates LB choices.
- Noise session decryption state lives in process memory; not
  serializable across pods without an L7 coordinator.
- Single-replica gives us a clean v1 ship target. Today's
  codex-exec-gateway already runs single-replica (no shared state) so
  no operational regression.

Constraints this imposes:
- Hard upper bound on concurrent sessions ≈ 200 (memory: 4MB/session
  noise buffers × 200 = 0.8GB, plus base 200MB).
- Pod restart = all sessions reset (harness reconnects fresh). Same
  blast radius as today.

When we need multi-replica:
- Postgres-backed `executor_registrations` (cold path lookup OK on
  registration; hot path stays in-process via per-pod LRU).
- Sticky service via pod-identity-encoded `url` in registration
  response (executor dials specific pod by hostname).
- Defer entire design to follow-up spec
  `2026-XX-XX-codex-exec-gateway-multi-replica-design.md` after v1
  operational data.

## 7. Implementation plan

### Phase 1: Noise Hybrid IK Go library (3 weeks, gated milestone)

- Create `internal/codexexecgateway/noise` package.
- Wire flynn/noise + circl ML-KEM-768 together for IK Hybrid.
- Implement test vectors by extracting from codex's
  `noise_channel_tests.rs`.
- Bit-compat verification with a small Rust binary that does the same
  handshake server-side.
- Deliverable: package + tests passing, ability to handshake against
  real `codex exec-server` started locally with `--remote` registered
  to a mock gateway.

**Hard milestone at end of week 3** (no slip allowed): Go library
completes one full noise hybrid IK handshake + 10 round-trip frames
against unmodified `codex exec-server` from cluster image. If miss:

**FFI fallback plan (week 4 fork):**
- Build a Rust shim `clatter-shim` exposing C ABI:
  `init_initiator(static_pub, ephemeral_seed) -> handle`,
  `read_message(handle, bytes) -> plaintext`,
  `write_message(handle, plaintext) -> bytes`.
- Distributed as a per-platform static library bundled with the gateway
  image (`linux/amd64`, `linux/arm64`).
- Go side uses `cgo` for invocation; per-session handle lifecycle
  managed by Go (`runtime.SetFinalizer` for safety).
- Trade: cgo crossing cost (~100ns/call, ~10µs/message at worst) is
  irrelevant next to network RTT. Build complexity worse (CI matrix
  needs Rust toolchain). Eliminates bit-compat risk entirely.

The fallback decision happens at end of week 3 review with a 3-day
"go or pivot" call. Phases 2-7 are unchanged either way (only the
internal noise package implementation differs).

### Phase 2: New HTTP endpoints (3-5 days)

- `POST /cloud/environment/{env_id}/register` — new shape.
- `WS /cloud/relay/{registration_id}` — physical WS for executor.
- `POST /cloud/environment/{env_id}/validate` — harness_key validation.
- DB schema: `executor_registrations` table (env_id, registration_id,
  executor_pubkey, created_at, last_seen_at).
- Idempotent re-registration (existing exe_id → return same record).

### Phase 3: NoiseWrapper + bridge integration (1 week)

- New goroutine model in `bridge.go`:
  - On bridge WS accept, hand off to NoiseWrapper.AttachStream.
  - NoiseWrapper either passthrough (legacy) or wraps with noise.
- Per-stream handshake, encryption, sequence number management.
- Segmentation/reassembly for large frames.
- Reset/disconnect propagation.

### Phase 4: codex-app-gateway spawn change (1-2 days)

- Modify `internal/codexappgateway/supervisor/spawn.go` to write
  `environment.toml` with the bridge URL.
- Wire workspace executor lookup so URL substitution works.

### Phase 5: sandbox executor migration (2-3 days)

- Update sandbox boot scripts (or whatever launches `codex exec-server`
  in the sandbox image) to use `--remote --environment-id` mode.
- Mint the required `CODEX_ACCESS_TOKEN` from workspace identity.

### Phase 6: end-to-end testing (1-2 weeks)

- Test fixtures:
  - Cluster sandbox + spawn'd codex (Scenario A).
  - Local Claude Code → MCP → cluster executor (Scenario B-cluster).
  - Local Claude Code → MCP → macbook executor (Scenario B-macbook).
  - Harness death + restart (Scenario C).
  - Multi-harness multiplex (Scenario D).
  - Legacy `--listen` executor (Scenario E).
- Audit verification: every tool call shows up in `exec_audit` table.
- Bit-compat soak: 24h run with mixed noise + legacy traffic, no
  decrypt failures.

### Phase 7: docs + release (3-5 days)

- Spec doc finalized.
- Update integration docs (codex-cli.md etc) — mostly unchanged but
  call out the executor registration change.
- Helm chart bump, release notes (callout the breaking change for
  users who had explicit `--listen` mode workarounds).

**Total: 7-10 weeks calendar time** (Phase 1 3 weeks + Phase 5a 1 week
+ Phase 5 1 week + others as estimated, assuming one person full-time
with bit-compat debug overhead). Worst case 12 weeks if FFI fallback
triggers + CI integration churn.

## 8. Risks

### 8.1 SINGLE POINT OF FAILURE: upstream codex removes plaintext bridge path

Today our entire architecture rests on `harness_connection_from_websocket`
(plaintext path triggered by `?role=harness` URL query) staying alive
in upstream codex. If they remove it — and 2026's trajectory has been
"tighten security, remove legacy paths" (chat completions removed in
Feb 2026, etc.) — **every harness, including our spawned codex
subprocess and external SDK clients, is forced into noise mode**.

If that happens we need:
- Gateway-as-noise-RESPONDER too (in addition to initiator), faking
  executor identity to harness side. Full **two-leg MITM**.
- Estimated additional work: 4-6 weeks on top of current plan.

**Mitigation**:
- Subscribe `notify` on `is_rendezvous_harness_url` function in codex
  source; alert if removed.
- Maintain a draft two-leg MITM spec in `docs/superpowers/specs/` with
  a `-draft` suffix so we can spin up immediately.
- File / +1 upstream issue if signs of deprecation appear.

This is the largest unmitigated risk in the design. Explicitly accept it.

### 8.2 Other risks

| Risk | Likelihood | Mitigation |
|---|---|---|
| ML-KEM-768 wire format diverges between circl and clatter | Medium-high | Test vectors from codex source; bit-compat tests gating. See §7 Phase 1 milestone + FFI fallback |
| Bit-compat Noise IK takes longer than 3 weeks | **High** | Phase 1 week-3 milestone = "tiny Go demo handshakes with real codex". Miss → Rust FFI fallback (see §7) |
| Codex upstream changes noise version (`v1 → v2`) | Low (likely months out) | Spec the version negotiation: gateway reads `security_profile`, dispatches; v2 = new implementation under same arch |
| Codex adds harness attestation (server-side check of harness identity) | Low-medium | Would invalidate our "gateway is harness" model. Watch upstream. If happens: switch to two-leg MITM or push for protocol change |
| RelayData segmentation reassembly bugs | Medium | Bound reassembly buffer per stream; reset stream on timeout / OOM-protect |
| Inter-pod state coordination | **Resolved**: single-replica only, see §6 D-3 |
| Validation HMAC key compromise | Low | Cluster secret; rotate quarterly; key is per-gateway-pod scope (not user-scope), so blast radius = "all sessions until rotation" |
| Audit pipeline buffer pressure | Low | Same shape as today, just on plaintext-after-decrypt; existing throttles apply |

## 9. Backwards compatibility

| User-visible change | Impact |
|---|---|
| Executor users on `--remote --environment-id` mode | Fixed (today's bug → works after deploy) |
| Executor users on `--listen` mode | Unchanged |
| codex-app-gateway-spawned harness | Transparent (spawn flow change is internal) |
| envmcp-public-gateway / MCP client users | Transparent (no client config change) |
| SDK users dialing bridge directly | Unchanged (still `?role=harness` plain bridge) |
| Auditor / SRE consuming audit WAL | Unchanged (same plaintext records) |

No CLI flag changes, no documentation breaking. The whole shift is
infrastructure-level.

## 10. Out of scope / follow-ups

- Mutual harness attestation (gateway proving its identity to harness).
  Not requested by codex today; could be added if upstream does.
- Per-tenant gateway noise Identity (today one per gateway pod, shared
  across tenants). Reasonable for single-org deploys; revisit if we
  go multi-tenant.
- Hot key rotation without restart (requires session-key versioning).
  Quarterly rotation with restart window is acceptable for now.
- Replay protection on validate (currently any unused token within
  expiry is valid). Bind to nonce / one-time-use if abuse appears.
