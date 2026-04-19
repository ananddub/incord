.PHONY: all build build-server build-voice run run-voice generate proto sqlc migrate test lint clean docker-up docker-down chrome openfga
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

# Open a fresh Chrome window (Flatpak). Used as the dependency for
# any target that wants to pop a dev URL in the browser.
chrome:
	flatpak run com.google.Chrome --new-window >/dev/null 2>&1 &

# Launch the OpenFGA Playground in Firefox. The /playground page iframes
# the hosted https://play.fga.dev/sandbox, which calls back into our
# 127.0.0.1:8080 API. Chrome's Private Network Access policy blocks that
# HTTPS→loopback call; Firefox doesn't enforce PNA, so it just works.
# Use `make openfga-chrome` if you specifically need Chrome and have
# toggled chrome://flags/#block-insecure-private-network-requests to
# Disabled.
openfga:
	flatpak run org.mozilla.firefox --new-window http://localhost:3000/playground >/dev/null 2>&1 &

# Chrome variant. Requires `chrome://flags/#block-insecure-private-network-requests`
# to be set to Disabled *persistently* in Chrome (the --disable-features
# flag is ignored by Flatpak Chrome due to sandbox restrictions).
openfga-chrome:
	flatpak run com.google.Chrome --new-window http://localhost:3000/playground >/dev/null 2>&1 &

# CLI-first alternative. The OpenFGA Playground is deprecated anyway,
# so prefer the `fga` CLI — no browser, no CORS, no PNA headache.
# Install once:  go install github.com/openfga/cli/cmd/fga@latest
openfga-stores:
	fga store list --api-url http://localhost:8090

# Print the current authorization model tuples for the ndiscord store.
# Usage:  make openfga-tuples
openfga-tuples:
	@STORE_ID=$$(fga store list --api-url http://localhost:8090 | awk '/ndiscord/ {print $$1}') ; \
	fga tuple read --store-id $$STORE_ID --api-url http://localhost:8090
