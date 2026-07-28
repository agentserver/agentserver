GO ?= go
GOFMT ?= gofmt

.PHONY: check fmt fmt-check vet test import-boundary contract-check conformance conformance-live

check: fmt-check vet test import-boundary contract-check

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

import-boundary:
	$(GO) test ./internal/buildguard -run '^TestRepositoryHasNoV1RuntimeImports$$' -count=1

contract-check:
	$(GO) test ./internal/runtimelock -run '^TestRuntimeManifestJSONSchemaContract$$' -count=1

conformance:
	$(GO) test ./conformance/codex/... -count=1

conformance-live:
	@test -n "$(AGENTSERVER_CODEX_BIN)" || \
		(printf '%s\n' 'AGENTSERVER_CODEX_BIN must be an absolute path to a pinned stock Codex binary'; exit 2)
	AGENTSERVER_RUN_LIVE_CODEX=1 AGENTSERVER_CODEX_BIN="$(AGENTSERVER_CODEX_BIN)" \
		$(GO) test ./conformance/codex/... -count=1 -v
