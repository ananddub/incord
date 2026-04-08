.PHONY: all build build-server build-voice run run-voice generate proto sqlc migrate test lint clean docker-up docker-down

all: generate build

# Build
build: build-server build-voice

build-server:
	go build -o bin/server ./cmd/server

build-voice:
	go build -o bin/voice ./cmd/voice

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
	migrate -path db/timescale/migrations -database "postgres://ndiscord:ndiscord@localhost:5432/ndiscord?sslmode=disable" up

migrate-down:
	migrate -path db/timescale/migrations -database "postgres://ndiscord:ndiscord@localhost:5432/ndiscord?sslmode=disable" down 1

migrate-create:
	migrate create -ext sql -dir db/timescale/migrations -seq $(name)

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
