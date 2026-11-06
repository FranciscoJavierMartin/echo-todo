SHELL := /bin/bash
DATABASE_URL ?= postgres://todo:todo@localhost:5432/todo?sslmode=disable
export DATABASE_URL

.PHONY: help run build migrate migrate-down migrate-status sqlc test test-short cover fmt vet lint db-up db-down

help: ## list targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk -F':.*?## ' '{printf "  %-16s %s\n", $$1, $$2}'

run: ## run the API (needs DATABASE_URL and JWT_SECRET)
	go run ./cmd/api

build: ## build static binaries into bin/
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/api ./cmd/api
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/migrate ./cmd/migrate

migrate: ## apply all migrations
	go run ./cmd/migrate up

migrate-down: ## roll back the last migration
	go run ./cmd/migrate down

migrate-status: ## show migration status
	go run ./cmd/migrate status

sqlc: ## regenerate sqlc code
	sqlc generate

db-up: ## start a local postgres + redis
	docker compose up -d postgres redis

db-down: ## stop local services
	docker compose down

test: ## all tests with race detector (needs docker or TEST_DATABASE_URL)
	go test -race -count=1 ./...

test-short: ## tests without containers
	go test -short ./...

cover: ## coverage report
	go test -count=1 -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -n 1

fmt:
	gofmt -l -w .

vet:
	go vet ./...
