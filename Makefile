SHELL := /bin/sh
GO ?= go
COMPOSE ?= docker compose -f deploy/compose/docker-compose.yml

.PHONY: fmt test test-race vet build demo-up demo-down smoke backup-restore-drill
fmt:
	@test -z "$$($(GO) fmt ./...)"

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

build:
	mkdir -p bin
	$(GO) build -o bin/stormrelay-server ./cmd/stormrelay-server
	$(GO) build -o bin/stormrelay-worker ./cmd/stormrelay-worker
	$(GO) build -o bin/stormrelay ./cmd/stormrelay-cli

demo-up:
	$(COMPOSE) up --build -d

demo-down:
	$(COMPOSE) down -v

smoke:
	./tests/smoke-compose.sh

backup-restore-drill:
	./tests/failure/backup-restore.sh
