GO ?= go
GOFMT ?= gofmt
NODE ?= node
PNPM ?= pnpm

.PHONY: check fmt fmt-check vet test web-check web-test postgres-test hydra-live-test import-boundary contract-check conformance conformance-live conformance-image-a04 conformance-image-a12 conformance-image-e09

check: fmt-check vet test web-check import-boundary contract-check

fmt:
	find . -name '*.go' -type f -exec $(GOFMT) -w {} +

fmt-check:
	@unformatted="$$(find . -name '*.go' -type f -exec $(GOFMT) -l {} +)"; \
	if [ -n "$$unformatted" ]; then \
		printf 'gofmt required:\n%s\n' "$$unformatted"; \
		exit 1; \
	fi

vet:
	$(GO) vet ./...

test:
	$(GO) test ./... -count=1

web-check:
	$(PNPM) web:check

web-test:
	$(PNPM) web:test

postgres-test:
	@test -n "$$AGENTSERVER_V2_TEST_DATABASE_URL" || \
		(printf '%s\n' 'AGENTSERVER_V2_TEST_DATABASE_URL is required'; exit 2)
	@AGENTSERVER_RUN_POSTGRES_TESTS=1 $(GO) test ./internal/coredb -run '^TestPostgreSQL' -count=1 -v

hydra-live-test:
	@test -n "$$AGENTSERVER_V2_TEST_HYDRA_ADMIN_ORIGIN" || \
		(printf '%s\n' 'AGENTSERVER_V2_TEST_HYDRA_ADMIN_ORIGIN is required'; exit 2)
	@test -n "$$AGENTSERVER_V2_TEST_HYDRA_PUBLIC_ORIGIN" || \
		(printf '%s\n' 'AGENTSERVER_V2_TEST_HYDRA_PUBLIC_ORIGIN is required'; exit 2)
	@AGENTSERVER_RUN_HYDRA_LIVE_TESTS=1 $(GO) test ./internal/coreserver \
		-run '^TestHydraV262(ExecutorPrivateKeyJWT|UserResourceAuthority)Live$$' -count=1 -v

import-boundary:
	$(GO) test ./internal/buildguard -run '^TestRepositoryHasNoV1RuntimeImports$$' -count=1

contract-check:
	$(GO) test ./internal/corecontract -run '^Test(InternalOpenAPIPathsMatchClientContract|PublicOpenAPIMatchesBrowserRunContract)$$' -count=1
	$(GO) test ./internal/runtimelock -run '^TestRuntimeManifestJSONSchemaContract$$' -count=1
	$(GO) test ./internal/runmanifest -run '^TestRunManifestJSONSchemaAcceptsSignedEnvelope$$' -count=1
	$(GO) test ./internal/runmanifest -run '^TestRunManifestVerificationKeyringJSONSchema$$' -count=1
	$(GO) test ./internal/runcapability -run '^TestProductionCapabilityKeyringJSONSchemaContract$$' -count=1
	$(GO) test ./internal/execprofile ./internal/executorgateway/... -run 'Contract|Schema|Profile' -count=1
	$(GO) test ./internal/harnesscontrol -run 'Contract|Schema|AsyncAPI' -count=1
	$(GO) test ./internal/harnessbootstrap -run 'Contract|Schema|Protocol' -count=1
	$(GO) test ./internal/runevent -run 'Contract|Schema|AsyncAPI' -count=1
	$(GO) test ./internal/devstack ./internal/devfixtures -run 'SchemaContract' -count=1
	$(GO) test ./internal/productiondeploy -run 'Schema|Topology|Contracts' -count=1
	$(GO) test ./cmd/agentserver-core ./cmd/platform-gateway ./cmd/browser-gateway ./cmd/executor-gateway \
		./cmd/harness-pool ./cmd/harness-worker ./cmd/llmproxy -run '^TestProductionRenderer' -count=1

conformance:
	$(GO) test ./conformance/codex/... -count=1

conformance-live:
	@test -n "$(AGENTSERVER_CODEX_BIN)" || \
		(printf '%s\n' 'AGENTSERVER_CODEX_BIN must be an absolute path to a pinned stock Codex binary'; exit 2)
	AGENTSERVER_RUN_LIVE_CODEX=1 AGENTSERVER_CODEX_BIN="$(AGENTSERVER_CODEX_BIN)" \
		$(GO) test ./conformance/codex/... -count=1 -v

conformance-image-a04:
	./conformance/image/a04/run.sh

conformance-image-a12:
	./conformance/image/a12/run.sh

conformance-image-e09:
	./conformance/image/e09/run.sh
