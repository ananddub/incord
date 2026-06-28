.PHONY: all build build-server build-voice run run-voice generate proto sqlc migrate test lint clean docker-up docker-down chrome
all: generate build

# Build
build: build-server build-voice build-init

build-server:
	go build -o bin/server ./cmd/server

build-voice:
	go build -o bin/voice ./cmd/voice

build-init:
	go build -o bin/init ./cmd/init

up:
	@docker compose down
	@docker compose build
	@docker compose up -d
# Run
run: build-server
	./bin/server

run-voice: build-voice
	./bin/voice

# Code generation
generate: proto sqlc

proto:
	buf generate

sqlc:
	sqlc generate -f db/timescale/sqlc.yaml

# Database migrations
migrate-up:
	goose -dir db/timescale/migrations postgres "postgres://ndiscord:ndiscord@localhost:5432/ndiscord?sslmode=disable" up

migrate-down:
	goose -dir db/timescale/migrations postgres "postgres://ndiscord:ndiscord@localhost:5432/ndiscord?sslmode=disable" down

migrate-create:
	goose -dir db/timescale/migrations create $(name) sql

# Testing
test:
	go test ./... -v -count=1

test-integration:
	go test ./... -v -count=1 -tags=integration

test-coverage:
	go test ./... -v -count=1 -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html

# Lint
lint:
	golangci-lint run ./...

# Docker
docker-up:
	docker compose up -d
	@echo "Init container will auto-setup databases and topics"

docker-down:
	docker compose down

docker-logs:
	docker compose logs -f

# Database
db-reset:
	./scripts/db-reset.sh

db-seed:
	./scripts/db-seed.sh

# Clean
clean:
	rm -rf bin/ coverage.out coverage.html

# Open a fresh Chrome window (Flatpak). Used as the dependency for
# any target that wants to pop a dev URL in the browser.
chrome:
	flatpak run com.google.Chrome --new-window >/dev/null 2>&1 &
