#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT_DIR"

PROJECT_NAME=${STORMRELAY_FAILURE_STATE_PROJECT:-stormrelay-failure-state}
COMPOSE=(docker compose --project-name "$PROJECT_NAME" -f deploy/compose/docker-compose.yml)
STATE_ENV_ACTIVE=0

cleanup() {
  status=$?
  if [[ $STATE_ENV_ACTIVE -eq 1 && $status -ne 0 ]]; then
    echo '--- failure state dependency logs ---' >&2
    "${COMPOSE[@]}" logs --no-color postgres nats >&2 || true
  fi
  "${COMPOSE[@]}" down -v >/dev/null 2>&1 || true
  exit "$status"
}
trap cleanup EXIT INT TERM

wait_postgres() {
  for ((i = 1; i <= 60; i++)); do
    if "${COMPOSE[@]}" exec -T postgres pg_isready -q -U stormrelay -d stormrelay; then
      return 0
    fi
    sleep 1
  done
  echo 'PostgreSQL did not become ready for failure tests' >&2
  return 1
}

wait_nats() {
  for ((i = 1; i <= 60; i++)); do
    if python3 -c 'import socket; s=socket.create_connection(("127.0.0.1",4222),1); data=s.recv(1024); s.close(); raise SystemExit(0 if b"INFO " in data else 1)' >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  echo 'NATS did not become ready for failure tests' >&2
  return 1
}

echo 'Running poison-message and runbook recovery failure tests'
"${COMPOSE[@]}" down -v >/dev/null 2>&1 || true
"${COMPOSE[@]}" up -d postgres nats
STATE_ENV_ACTIVE=1
wait_postgres
wait_nats
STORMRELAY_TEST_DATABASE_URL='postgres://stormrelay:stormrelay@localhost:5432/stormrelay?sslmode=disable' \
STORMRELAY_TEST_NATS_URL='nats://localhost:4222' \
  go test -tags=failure -count=1 -v ./tests/failure
"${COMPOSE[@]}" down -v
STATE_ENV_ACTIVE=0

echo 'Running dependency outage failure drill'
bash ./tests/failure/dependency-outages.sh

echo 'All failure-injection foundation checks passed.'
