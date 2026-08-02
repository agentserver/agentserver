# ADR 0003: executor enrollment and machine-key possession

- Status: accepted
- Date: 2026-08-02

## Context

Phase 1 needs one independently revocable identity per executor. An executor
connection must not trust an `executor_id` sent in the agentx hello, and a
stolen OAuth bearer must not be sufficient to open the WebSocket. Credentials
must remain in the agentx connector and must never enter the runner, stock
exec-server, or a process started by exec-server.

Hydra is already the OAuth authority. Its client-credentials token is useful
for audience, scope, expiry, and revocation, but a plain bearer does not prove
possession at the WSS upgrade. The connection therefore needs both OAuth and a
fresh proof made by an enrolled machine key.

The originally proposed single Ed25519 key cannot implement both jobs against
the locked Hydra version. Hydra v26.2.0's `private_key_jwt` client validator and
the corresponding Fosite verification switch accept RSA/PSS and
ES256/ES384/ES512, but do not accept EdDSA. This was checked against the exact
v26.2.0 source tarball
`85aa8265eb6149d8bb70dd70867e27886e0d6d4d49fa9587b2bb53b3bb4a5607`
and `spec/api.json`
`aa95a73bb14a90b8f2ce1f852cc41418cdbef3ac3bdedc09a48df0bc97a40540`.
Declaring EdDSA here would therefore produce an enrollment flow that could
never acquire a production token from the selected OAuth authority.

## Decision

### Enrollment

1. A current workspace owner creates an `enrolling` executor and requests a
   short-lived enrollment token using an idempotency key.
2. Core issues `asv2enr1` tokens. They contain a closed-world canonical claim
   set binding issuer, token ID, workspace, executor, issuing actor, issued-at,
   and expiry. The token is MACed in a separate cryptographic domain. Core
   stores the token ID and lifecycle state, never the bearer.
3. `agentx enroll --token-stdin` generates two independent private keys
   locally: Ed25519 for enrollment/WSS machine proof and P-256 for Hydra client
   authentication. It submits both public keys, pinned runtime/protocol
   metadata and environment/root metadata. Both public keys are fields in one
   closed-world canonical enrollment request and therefore contribute to the
   same request SHA-256.
4. Agentx proves possession of both keys over the enrollment-token SHA-256 and
   that common request SHA-256, using distinct cryptographic domains:
   `agentserver-v2/executor-enrollment-proof/ed25519-v2\0` and
   `agentserver-v2/executor-enrollment-proof/es256-v1\0`. The ES256 proof is
   exactly 64-byte IEEE P1363 `r || s`, not ASN.1 DER, and Core requires a
   canonical low-S signature.
5. The executor-gateway bounds the request and relays it to Core over its mTLS
   workload identity. On that internal hop it injects the gateway's configured
   executor ID in `X-Agentserver-Expected-Executor-Id`; this header is never
   copied from agentx input. Core compares it with the verified token claims
   before any database or Hydra mutation, then verifies and consumes the
   enrollment authority. This makes the Phase 1 single-executor deployment
   binding a precondition rather than a response-time consistency check.
6. Core registers one deterministic Hydra client for the executor. The client
   permits only `client_credentials`, `scope=executor:connect`, and
   `audience=executor-gateway`; it authenticates with `private_key_jwt`,
   `alg=ES256`, and the enrolled P-256 public JWK. Access tokens are opaque and
   the per-client client-credentials access-token lifetime is exactly five
   minutes. The JWK `kid` is its RFC 7638 SHA-256 thumbprint. Create/retry
   reconciles the exact client document and never overwrites a conflicting
   Hydra client.
7. Core changes the executor from `enrolling` to `offline` only after exact
   Hydra reconciliation. Ambiguous failures leave a retryable enrolling record;
   a later token may resume only the identical machine/enrollment digest.
8. Agentx stores both private keys and non-secret enrollment metadata in an
   owner-only credential directory (or a stronger OS keychain). It deletes the
   enrollment bearer immediately. Neither private key may enter the runner,
   stock exec-server, or any process started by exec-server.

### OAuth token acquisition

Agentx signs a standard RFC 7523 `private_key_jwt` client assertion with its
P-256 key and `alg=ES256`, then sends it to Hydra's token endpoint. The
assertion has exact `iss=sub=client_id`, the exact token endpoint as `aud`, a
unique `jti`, and a lifetime no longer than five minutes. The token request
asks for exactly `grant_type=client_credentials`, `scope=executor:connect`, and
`audience=executor-gateway`.

Core's online authorization accepts an introspection result only when it is
active and unexpired; has the exact configured issuer, sole audience and sole
scope; reports `token_type=Bearer` and `token_use=access_token`; has
`sub=client_id`, `nbf=iat`, and an issuance window no longer than the configured
five-minute client lifetime; and its `client_id` maps to one current,
non-revoked enrolled executor with the same stored P-256 JWK thumbprint. Every
live authorization also reads the Hydra Admin client and requires the complete
closed-world client document to remain identical to the enrolled authority;
deletion, key drift, grant drift, or an unavailable read fails closed. The
executor ID is derived only from this mapping and is never accepted from agentx
input.

### WSS proof

1. Agentx presents the OAuth bearer to
   `POST /internal/v2/agentx/challenges` on executor-gateway.
2. Gateway asks Core to live-authorize the bearer, then creates a random
   256-bit, process-local, single-use challenge with a maximum 30-second TTL.
   It binds the challenge to the executor, enrolled public-key fingerprint,
   bearer SHA-256, gateway instance, and exact WSS request target.
3. Agentx signs the versioned, length-delimited challenge transcript with the
   enrolled Ed25519 key and supplies the challenge ID and signature on the WSS
   upgrade. The P-256 OAuth key is not reused for this protocol.
4. Gateway live-authorizes the bearer again, atomically consumes the challenge,
   verifies every binding and the signature, and only then returns the derived
   executor identity to the WSS handler. A challenge cannot be replayed, moved
   to another gateway process/path, or paired with another bearer.

The exact proof version is `executor-wss-proof/ed25519-v1`. The signed bytes
start with `agentserver-v2/executor-wss-proof/ed25519-v1\0`; each following
field is `uint32be(length) || bytes`, in this order: version, challenge ID,
decoded 32-byte nonce, executor ID, decoded 32-byte machine-key SHA-256,
SHA-256 of the exact OAuth bearer bytes, gateway instance ID, literal target
`/internal/v2/agentx/connect`, issued-at Unix milliseconds and expires-at Unix
milliseconds. Both timestamps are emitted at whole-millisecond UTC precision.
The Ed25519 signature is encoded as canonical unpadded base64url. The machine
contract and golden transcript digest live in
`api/openapi/executor-gateway.yaml`; upgrade headers and session transport live
in `api/asyncapi/agentx-wss.yaml`.

The bearer is therefore necessary but not sufficient. This is the nonce
challenge option already allowed by the agentx AsyncAPI; Phase 1 does not rely
on provider-specific DPoP support.

## Failure and deployment boundaries

- Core or Hydra introspection unavailable: the challenge/upgrade endpoint fails
  closed with a non-cacheable `503`. A transient second-authorization failure
  does not consume the challenge, so the same proof can be retried within its
  original deadline. An authorization rejection returns `401`; once gateway
  reaches proof verification, an invalid proof or changed authority consumes
  the challenge.
- Gateway restart: all outstanding challenges and process-local resume journals
  disappear. Agentx obtains a new challenge and starts a fresh connection;
  cross-process resume is not claimed.
- Executor revoked or its OAuth client no longer exact: new challenges and WSS
  upgrades fail immediately.
- Hydra v26.2.0 create may generate a client secret and dynamic-registration
  access token even for a `private_key_jwt` client. The Core adapter permits
  these values only in the bounded create response and immediately discards
  them; a subsequent client read containing any provisioning credential fails
  reconciliation. Production Hydra must disable dynamic client registration,
  and deployment verification must prove it is disabled.
- Hydra is a locked compatibility dependency. An upgrade must first pass the
  complete closed-world client response, ES256 token exchange, introspection,
  and capability-smuggling gates; unknown client fields fail reconciliation.
- Phase 1 executor-gateway remains one replica. HPA is not enabled until a
  durable challenge/journal owner-routing design exists.
- Kubernetes Secret projections may use the usual `key -> ..data/key` symlink
  layout. At startup the gateway opens the resolved target, requires a bounded
  regular private-key file with no group/other permission, and re-stats the
  same open file after reading. Key/certificate rotation requires a process
  rollout in Phase 1; the process does not hot-reload an atomically swapped
  `..data` target.
- Enrollment/OAuth credentials are connector-only. Runner startup is performed
  through an inherited credential-free IPC boundary, and child environments
  are independently scrubbed.

## Verification

The opt-in `make hydra-live-test` gate exercises a real Hydra process through
its public and Admin HTTP interfaces. It creates and reads the exact client,
performs an ES256 RFC 7523 token exchange, introspects the resulting opaque
token, passes it through Core's production executor authorizer, proves extra
scope and audience requests fail without a token, and proves dynamic client
registration returns not found.

On 2026-08-02 the gate passed against a Darwin/arm64 binary built with Go
1.26.5 from the exact v26.2.0 source tarball named above. The binary SHA-256 was
`c70ae1a7314b3a5af6171b11aa29d76de65b83d1e40744deaafaa07f2943effe`
and its size was `56966178` bytes. The first live run found that Hydra
normalizes the configured duration `5m` to `5m0s`; the production client
document now sends and reconciles the canonical `5m0s` representation, and the
introspected lifetime remains exactly 300 seconds. This local evidence does
not replace the pinned Linux image, production TLS/database configuration, or
deployment-level dynamic-registration gate still required before release.

The executor-gateway package also has a process-level production gate. It
starts `executor-gateway serve` with TLS 1.3, an exact single-URI SPIFFE client
identity, a production run-capability public keyring and a mutually
authenticated fake Core. It exercises deployment-bound enrollment, challenge
issuance, a signed WebSocket upgrade, proof rejection and consumption, replay,
Core outage/retry, revocation, and bounded shutdown. Unit and contract gates
separately cover expiry/capacity, timestamp canonicalization, bearer secrecy,
duplicate JSON/header rejection, the OpenAPI/AsyncAPI field set and Kubernetes
projected-Secret resolution. These gates prove the gateway side; production
agentx credential storage and connector behavior remain a separate required
gate.

## Consequences

This adds one bounded challenge round trip before WSS, two connector-only
private keys, and exact Hydra client reconciliation. In return, the design is
compatible with the selected Hydra release, identity is independently
revocable, bearer theft alone is insufficient, and no shared workspace service
token is introduced. Phase 1 key rotation revokes/re-enrolls the executor as a
single dual-key identity; independent in-place rotation requires a separately
versioned protocol. The design also keeps an explicit future migration path to
DPoP: the Core client mapping and Ed25519 enrollment identity remain usable
while the gateway proof transport can be versioned.
