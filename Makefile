.PHONY: build test test-integration test-e2e lint migrate-up migrate-down docker run-dev clean

APP_NAME := cockpit
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  := -X main.buildVersion=$(VERSION) -X main.buildTime=$(BUILD_TIME)

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/$(APP_NAME) ./cmd/cockpit

test:
	go test -race -count=1 ./internal/...

test-integration:
	go test -race -count=1 -tags=integration ./...

test-e2e:
	go test -race -count=1 -tags=e2e ./tests/e2e/...

lint:
	golangci-lint run ./...

migrate-up:
	goose -dir internal/store/migrations postgres "$(DATABASE_URL)" up

migrate-down:
	goose -dir internal/store/migrations postgres "$(DATABASE_URL)" down

docker:
	docker build -f deployments/Dockerfile -t composecockpit:$(VERSION) .

run-dev:
	docker compose -f deployments/docker-compose.yml up --build

clean:
	rm -rf bin/

seed:
	@echo "Creating admin user..."
	@go run ./scripts/seed/main.go
