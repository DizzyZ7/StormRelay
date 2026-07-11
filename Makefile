SHELL := /bin/sh
GO ?= go
COMPOSE ?= docker compose -f deploy/compose/docker-compose.yml

.PHONY: fmt test test-race vet build demo-up demo-down smoke backup-restore-drill failure-test dependency-outage-drill failure benchmark-correctness benchmark-full benchmark-validate
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
	bash ./tests/failure/backup-restore.sh

failure-test:
	$(GO) test -tags=failure -count=1 -v ./tests/failure

dependency-outage-drill:
	bash ./tests/failure/dependency-outages.sh

failure:
	bash ./tests/failure/run.sh

benchmark-correctness:
	bash ./tests/load/run-profile.sh correctness

benchmark-full:
	bash ./tests/load/run-profile.sh full

benchmark-validate:
	python3 ./tests/load/validate_result.py ./tests/load/result.schema.json $(RESULT)
