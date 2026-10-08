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
Chart `0.1.0-config.d21b9cf41bcc7`, publication run `37732003749`. The live index
now references `platform-BVn6KCHd.js` instead of `platform-pJf3x_fy.js`; the live
script includes the three current Gateway fields and the API-key UI. Existing
Gateway metadata was preserved. Frontend, service and PostgreSQL tests passed.
No production Gateway or API key was created for validation; user-specific
upstream authentication still requires the owner's real form submission.
