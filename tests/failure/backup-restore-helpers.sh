#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT_DIR"

PROJECT_NAME=${STORMRELAY_HELPER_DRILL_PROJECT:-stormrelay-backup-helpers}
COMPOSE=(docker compose --project-name "$PROJECT_NAME" -f deploy/compose/docker-compose.yml)
DATABASE_URL='postgres://stormrelay:stormrelay@127.0.0.1:5432/stormrelay?sslmode=disable'
ARCHIVE='/tmp/stormrelay-helper-contract.dump'

cleanup() {
  status=$?
  if [[ $status -ne 0 ]]; then
    echo '--- PostgreSQL helper contract logs ---' >&2
    "${COMPOSE[@]}" logs --no-color postgres >&2 || true
  fi
  "${COMPOSE[@]}" down -v >/dev/null 2>&1 || true
  exit "$status"
}
trap cleanup EXIT INT TERM

postgres_exec() {
  "${COMPOSE[@]}" exec -T postgres "$@"
}

psql_exec() {
  local database=$1
  local query=$2
  postgres_exec psql -X -qAt -v ON_ERROR_STOP=1 -U stormrelay -d "$database" -c "$query"
}

echo 'Starting isolated PostgreSQL helper contract environment'
"${COMPOSE[@]}" down -v >/dev/null 2>&1 || true
"${COMPOSE[@]}" up -d postgres

for _ in $(seq 1 60); do
  if postgres_exec pg_isready -U stormrelay -d stormrelay >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
postgres_exec pg_isready -U stormrelay -d stormrelay >/dev/null

"${COMPOSE[@]}" cp scripts/postgres-backup.sh postgres:/tmp/postgres-backup.sh
"${COMPOSE[@]}" cp scripts/postgres-restore.sh postgres:/tmp/postgres-restore.sh
postgres_exec rm -f "$ARCHIVE" "${ARCHIVE}.sha256" "${ARCHIVE}.sha256.saved"

psql_exec stormrelay "CREATE TABLE recovery_helper_fixture (id integer PRIMARY KEY, value text NOT NULL); INSERT INTO recovery_helper_fixture (id, value) VALUES (1, 'verified');"

postgres_exec env STORMRELAY_DATABASE_URL="$DATABASE_URL" sh /tmp/postgres-backup.sh "$ARCHIVE"
postgres_exec test -s "$ARCHIVE"
postgres_exec test -s "${ARCHIVE}.sha256"

if postgres_exec env STORMRELAY_DATABASE_URL="$DATABASE_URL" sh /tmp/postgres-backup.sh "$ARCHIVE"; then
  echo 'backup helper unexpectedly overwrote an existing archive' >&2
  exit 1
fi

if postgres_exec env STORMRELAY_DATABASE_URL="$DATABASE_URL" sh /tmp/postgres-restore.sh "$ARCHIVE"; then
  echo 'restore helper unexpectedly ran without explicit confirmation' >&2
  exit 1
fi

postgres_exec mv "${ARCHIVE}.sha256" "${ARCHIVE}.sha256.saved"
if postgres_exec env STORMRELAY_DATABASE_URL="$DATABASE_URL" STORMRELAY_RESTORE_CONFIRM=YES sh /tmp/postgres-restore.sh "$ARCHIVE"; then
  echo 'restore helper unexpectedly accepted a missing checksum' >&2
  exit 1
fi
postgres_exec mv "${ARCHIVE}.sha256.saved" "${ARCHIVE}.sha256"

if postgres_exec env STORMRELAY_DATABASE_URL="$DATABASE_URL" STORMRELAY_RESTORE_CONFIRM=YES sh /tmp/postgres-restore.sh "$ARCHIVE"; then
  echo 'restore helper unexpectedly overwrote a non-empty database' >&2
  exit 1
fi

psql_exec postgres "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = 'stormrelay' AND pid <> pg_backend_pid(); ALTER DATABASE stormrelay RENAME TO stormrelay_before_helper_restore; CREATE DATABASE stormrelay OWNER stormrelay;"

postgres_exec env STORMRELAY_DATABASE_URL="$DATABASE_URL" STORMRELAY_RESTORE_CONFIRM=YES sh /tmp/postgres-restore.sh "$ARCHIVE"

restored=$(psql_exec stormrelay "SELECT value FROM recovery_helper_fixture WHERE id = 1")
if [[ "$restored" != 'verified' ]]; then
  echo "restored fixture mismatch: ${restored:-<empty>}" >&2
  exit 1
fi

schema_count=$(psql_exec stormrelay "SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'recovery_helper_fixture'")
if [[ "$schema_count" != '1' ]]; then
  echo 'restored fixture table was not found exactly once' >&2
  exit 1
fi

echo 'PostgreSQL helper contract passed: archive, checksum, confirmation, no-overwrite, empty-target, and restore checks succeeded.'
