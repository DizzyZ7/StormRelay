#!/bin/sh
set -eu
COMPOSE="docker compose -f deploy/compose/docker-compose.yml"
cleanup() { $COMPOSE down -v >/dev/null 2>&1 || true; }
trap cleanup EXIT INT TERM
$COMPOSE up --build -d
for i in $(seq 1 60); do
  if curl -fsS http://localhost:8080/readyz >/dev/null && curl -fsS http://localhost:8081/readyz >/dev/null; then break; fi
  sleep 2
done
curl -fsS http://localhost:8080/readyz
curl -fsS http://localhost:8081/readyz
curl -fsS -H 'Authorization: Bearer local-development-only-change-me' http://localhost:8080/api/v1/version
