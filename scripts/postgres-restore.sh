#!/bin/sh
set -eu

usage() {
  echo "usage: STORMRELAY_DATABASE_URL=postgres://... STORMRELAY_RESTORE_CONFIRM=YES sh scripts/postgres-restore.sh BACKUP.dump" >&2
  exit 2
}

[ "$#" -eq 1 ] || usage
: "${STORMRELAY_DATABASE_URL:?STORMRELAY_DATABASE_URL is required}"
[ "${STORMRELAY_RESTORE_CONFIRM:-}" = "YES" ] || {
  echo "restore refused: set STORMRELAY_RESTORE_CONFIRM=YES after verifying the target database is disposable and empty" >&2
  exit 2
}

archive=$1
[ -f "$archive" ] || {
  echo "backup archive not found: $archive" >&2
  exit 1
}

for command_name in psql pg_restore sha256sum; do
  command -v "$command_name" >/dev/null 2>&1 || {
    echo "$command_name is required" >&2
    exit 1
  }
done

checksum="${archive}.sha256"
if [ -f "$checksum" ]; then
  checksum_dir=$(dirname "$checksum")
  checksum_name=$(basename "$checksum")
  (
    cd "$checksum_dir"
    # The short option is supported by both GNU coreutils and BusyBox.
    sha256sum -c "$checksum_name"
  )
else
  echo "restore refused: checksum sidecar not found: $checksum" >&2
  exit 1
fi

pg_restore --list "$archive" >/dev/null

user_table_count=$(psql "$STORMRELAY_DATABASE_URL" -X -A -t -v ON_ERROR_STOP=1 -c \
  "SELECT count(*) FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace WHERE c.relkind IN ('r','p') AND n.nspname NOT IN ('pg_catalog','information_schema') AND n.nspname !~ '^pg_toast';")

case "$user_table_count" in
  ''|*[!0-9]*)
    echo "restore refused: could not determine whether the target database is empty" >&2
    exit 1
    ;;
  0) ;;
  *)
    echo "restore refused: target database contains $user_table_count user tables" >&2
    exit 1
    ;;
esac

pg_restore \
  --dbname="$STORMRELAY_DATABASE_URL" \
  --exit-on-error \
  --single-transaction \
  --no-owner \
  --no-privileges \
  "$archive"

printf 'PostgreSQL restore completed from: %s\n' "$archive"
