.PHONY: dev build clean frontend backend agent agent-all llmproxy credentialproxy test docker docker-agent docker-llmproxy docker-credentialproxy docker-openclaw docker-all openapi openapi-check

# Development: run frontend dev server + Go backend
dev:
	@echo "Start backend: go run . serve --password test"
	@echo "Start frontend: cd web && pnpm dev"

# Build frontend then Go binary
build: frontend backend

frontend:
	cd web && pnpm install && pnpm build

backend:
	go build -o bin/agentserver .

agent:
	CGO_ENABLED=0 go build -o bin/agentserver-agent ./cmd/agentserver-agent

llmproxy:
	CGO_ENABLED=0 go build -o bin/llmproxy ./cmd/llmproxy

credentialproxy:
	CGO_ENABLED=0 go build -o bin/credentialproxy ./cmd/credentialproxy

astool:
	CGO_ENABLED=0 go build -o bin/astool ./cmd/astool

test:
	go vet ./...
	go test ./... -count=1

agent-all:
	GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build -o bin/agentserver-linux-amd64        ./cmd/agentserver-agent
	GOOS=linux   GOARCH=arm64 CGO_ENABLED=0 go build -o bin/agentserver-linux-arm64        ./cmd/agentserver-agent
	GOOS=darwin  GOARCH=amd64 CGO_ENABLED=0 go build -o bin/agentserver-darwin-amd64       ./cmd/agentserver-agent
	GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 go build -o bin/agentserver-darwin-arm64       ./cmd/agentserver-agent
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o bin/agentserver-windows-amd64.exe  ./cmd/agentserver-agent

clean:
	rm -rf bin/ web/dist/

docker:
	docker build -t agentserver .

docker-agent:
	docker build -f Dockerfile.agent -t agentserver-agent:latest .

docker-llmproxy:
	docker build -f Dockerfile.llmproxy -t llmproxy:latest .

docker-credentialproxy:
	docker build -f Dockerfile.credentialproxy -t credentialproxy:latest .

docker-openclaw:
	docker build -f Dockerfile.openclaw -t openclaw-agent:latest .

docker-all: docker docker-agent docker-llmproxy docker-credentialproxy

sdk-test:
	cd sdk/python && .venv/bin/pytest -v

sdk-lint:
	cd sdk/python && .venv/bin/ruff check . && .venv/bin/ruff format --check .

jupyter-image:
	docker build -f Dockerfile.jupyter -t agentserver-jupyter:dev .

jupyter-smoke: jupyter-image
	mkdir -p notebook/smoke-workspace
	docker compose -f notebook/docker-compose.smoke.yml up --build

# OpenAPI: generate docs/api/openapi.{yaml,json} from swaggo annotations
# on internal/server handler funcs. Source of truth for the frontend
# TypeScript codegen.
SWAG ?= $(shell go env GOPATH)/bin/swag

# Generates Swagger 2.0 from swaggo annotations, then upconverts to
# OpenAPI 3.0 via swagger2openapi (openapi-typescript v7 requires 3.x).
openapi:
	$(SWAG) init -g internal/server/swagger.go --parseDependency --outputTypes yaml,json -o docs/api/ -d ./
	@npx --yes -p swagger2openapi swagger2openapi --yaml --outfile docs/api/openapi.yaml docs/api/swagger.yaml
	@npx --yes -p swagger2openapi swagger2openapi             --outfile docs/api/openapi.json docs/api/swagger.json
	@rm -f docs/api/swagger.yaml docs/api/swagger.json

# Drift check: regenerate to a temp dir and diff. CI uses this to
# catch handler annotations that weren't re-swagged before commit.
openapi-check:
	@rm -rf /tmp/openapi-check && mkdir -p /tmp/openapi-check
	@$(SWAG) init -g internal/server/swagger.go --parseDependency --outputTypes yaml,json -o /tmp/openapi-check/ -d ./ >/dev/null
	@npx --yes -p swagger2openapi swagger2openapi --yaml --outfile /tmp/openapi-check/openapi.yaml /tmp/openapi-check/swagger.yaml >/dev/null
	@npx --yes -p swagger2openapi swagger2openapi             --outfile /tmp/openapi-check/openapi.json /tmp/openapi-check/swagger.json >/dev/null
	@diff -u docs/api/openapi.yaml /tmp/openapi-check/openapi.yaml || (echo "FAIL: docs/api/openapi.yaml is stale — run 'make openapi' and commit"; exit 1)
	@diff -u docs/api/openapi.json /tmp/openapi-check/openapi.json || (echo "FAIL: docs/api/openapi.json is stale — run 'make openapi' and commit"; exit 1)
	@echo "openapi-check: spec matches handler annotations"
