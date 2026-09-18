.PHONY: build test vet run up down logs

build:
	go build ./...

test:
	go test ./...

vet:
	go vet ./...

run:
	RUN_MIGRATIONS=true \
	POSTGRES_DSN=postgres://signalyard:signalyard@localhost:5432/signalyard \
	NATS_URL=nats://localhost:4222 \
	JWT_SIGNING_SECRET=dev-jwt-secret-change-me \
	HEC_TOKEN_SALT=dev-hec-salt-change-me \
	DEV_ADMIN_TOKEN=dev-admin-token \
	go run ./cmd/core-api-gateway

up:
	docker compose -f deployments/docker-compose.yml up -d --build

down:
	docker compose -f deployments/docker-compose.yml down

logs:
	docker compose -f deployments/docker-compose.yml logs -f core-api-gateway
