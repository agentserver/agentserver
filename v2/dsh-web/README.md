# DSH frontend source and build

The source is the `third_party/deepseek-harness` submodule, from
`https://github.com/agentserver/deepseek-harness` branch `feat/agentserver-web`.
The gitlink pins the reviewed commit; builds never update to a moving branch.
The branch is based on upstream `0.2.1-alpha.1` and contains the deployment
preview-notice option plus its tests. Conversation ordering stays upstream;
AgentServer's Go facade owns protocol projection and compatibility.

Deployment differences are limited to AgentServer authentication, disabling the
preview notice, and the `agentserver` title. `export-overlay.yml` disables the
native directory picker and development HMR; all remaining plugins come from
the official Web profile. The browser picker can render, but the backend does
not expose a local filesystem.

## Build

Use Node 24+, npm, a C compiler and rsync:

```sh
git submodule update --init third_party/deepseek-harness
bash v2/dsh-web/build.sh
AGENTSERVER_REQUIRE_DSH_ASSETS=1 GOTOOLCHAIN=go1.26.5 \
  go -C v2 test ./dsh-web ./internal/browsergateway ./cmd/browser-gateway
```

The build sets the official `DSH_CLIENT_TITLE=agentserver` option, builds the
pinned source and launches an isolated loopback-only `dsh web` profile to export
its exact plugin graph. The local export cookie/token never enters the output.
The Go static server serves official combo and lazy-chunk URLs from the generated
resource map; it does not rewrite frontend sorting, models, or session logic.

`dist/` is ignored except for its empty-directory marker. Generated HTML, JS,
CSS, fonts and maps must not be committed. CI builds the assets before compiling
service binaries: `.github/workflows/dsh-frontend.yml` uses a separate hosted
runner, then the SG publisher downloads its Actions artifact and pushes the
service image directly to GHCR. No ICM build or push is involved. A source-only checkout can run backend tests; asset regressions
are explicitly skipped unless `AGENTSERVER_REQUIRE_DSH_ASSETS=1`. Production DSH
startup refuses an unbuilt bundle instead of serving a placeholder application.

For an update, commit source changes to the DSH fork first, move the submodule
gitlink deliberately, build, run the facade/Trajectory consumer tests, then
publish the service image. Do not modify generated JavaScript or maintain a
second copy of upstream patches in this repository.
