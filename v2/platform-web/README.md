# Platform frontend

The management UI at `agent.byted.bps.dev` is built from `platform-web/src` and
`web-shared/src`. It is independent of the pinned DSH frontend.

From `v2`, use the package-manager version in `package.json`:

```sh
pnpm install --frozen-lockfile
pnpm web:check
AGENTSERVER_REQUIRE_PLATFORM_ASSETS=1 go test ./platform-web ./cmd/platform-gateway
```

`web:check` regenerates API types, typechecks, tests and builds the production
bundle. `platform-web/dist` is generated and ignored by Git (except `.gitkeep`).
Build it **before** compiling `platform-gateway`, which embeds these files.
Missing assets fail explicitly at production startup; ordinary Go-only unit
tests may skip asset tests, but release tests require them via the environment
variable above. Kubernetes and production publication workflows run the frontend
build before Go tests and image packaging. Chart-only releases reuse the existing
service image and do not change frontend code.

The LLM Gateway API returns `authType`, `baseUrl` and `apiKeyConfigured` for both
OIDC and API-key profiles. API keys are write-only and must never be returned or
stored in browser storage. The client retains exact-field and workspace checks;
do not relax those checks to hide a stale frontend/backend contract. Regression
tests exercise lists for both profiles and API-key create/reload, and the embedded-bundle
test verifies that the built page includes the current Gateway contract and UI.

The stale-bundle repair was deployed on 2026-10-08 as SG Helm revision 169,
Chart `0.1.0-config.d21b9cf41bcc7`, publication run `37732003749`. That release's
index referenced `platform-BVn6KCHd.js` instead of `platform-pJf3x_fy.js`; its
script included the three current Gateway fields and the API-key UI. Existing
Gateway metadata was preserved. Frontend, service and PostgreSQL tests passed.
No production Gateway or API key was created for validation; user-specific
upstream authentication still requires the owner's real form submission.

Model gateway traffic no longer rejects a hostname just because its DNS answer
is RFC1918/ULA. This applies to the llmproxy upstream client only; URL shape,
HTTPS certificate verification, run/workspace authority and write-only API keys
remain enforced. Loopback, link-local, metadata and other special-use addresses
are still rejected. Other uses of the public-only HTTPS client, including OIDC
discovery, retain their default policy.

Private connectivity is controlled by deployment egress. SG currently allows
`axonhub-cn.byted.bps.dev` on TCP 443, tracking its DNS addresses and requiring
that exact TLS SNI. The DNS rule preserves resolution for existing public model
gateways; it does not grant general private-network HTTPS access. Changing the
LLM Gateway in the UI does not itself edit cluster network policy.

The private-address change and `llmproxy-cn-axonhub-egress` policy were deployed
on 2026-10-08 as SG revision 170, Chart `0.1.0-config.d86895a8eb584`, publication
run `37738658639`. Cilium reported policy validation success. Restricted,
credential-free probes in both llmproxy replicas reached AxonHub CN over IPv6
and IPv4 with successful TLS verification, returning the expected HTTP 401
without a bearer. This verifies network/TLS access, not the user's API key or
model authorization. No Gateway settings or stored secrets were changed.

The curl probe image requires an explicit numeric UID with Kubernetes'
`runAsNonRoot` check. A first named-user probe did not start; replacement probes
used UID/GID 65534, RuntimeDefault seccomp, no capabilities, no privilege
escalation, read-only root filesystems and no credential mounts. Successful
probes exited; their metadata remains until the normal Pod lifecycle replaces
them. Do not restart an active proxy merely to remove diagnostic metadata.
