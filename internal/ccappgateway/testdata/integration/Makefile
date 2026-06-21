.PHONY: build up down logs test

# Build BOTH images: cc-app-gateway main + test-tools.
build:
	cd ../../../../ && docker build -f Dockerfile.cc-app-gateway -t cc-app-gateway:dev .
	cd ../../../../ && docker build -f internal/ccappgateway/testdata/integration/Dockerfile.test-tools -t cc-app-gateway-test-tools:dev .

up: build
	docker compose -f docker-compose.yml up -d

down:
	docker compose -f docker-compose.yml down --volumes --remove-orphans

logs:
	docker compose -f docker-compose.yml logs --tail 200
