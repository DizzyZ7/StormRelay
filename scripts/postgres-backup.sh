#!/bin/sh
set -eu

usage() {
  echo "usage: STORMRELAY_DATABASE_URL=postgres://... sh scripts/postgres-backup.sh OUTPUT.dump" >&2
  exit 2
}

[ "$#" -eq 1 ] || usage
: "${STORMRELAY_DATABASE_URL:?STORMRELAY_DATABASE_URL is required}"

output=$1
case "$output" in
  ''|*/../*|../*|*/..|*'
'*)
    echo "backup output path is invalid" >&2
    exit 2
    ;;
esac

if [ -e "$output" ] || [ -e "${output}.sha256" ]; then
  echo "backup refused: output or checksum already exists" >&2
  exit 1
fi

for command_name in pg_dump pg_restore sha256sum awk; do
  command -v "$command_name" >/dev/null 2>&1 || {
    echo "$command_name is required" >&2
    exit 1
  }
done

umask 077
tmp="${output}.tmp.$$"
checksum_tmp="${output}.sha256.tmp.$$"
cleanup() {
  rm -f "$tmp" "$checksum_tmp"
}
trap cleanup EXIT INT TERM

mkdir -p "$(dirname "$output")"
pg_dump \
  --dbname="$STORMRELAY_DATABASE_URL" \
  --format=custom \
  --compress=6 \
  --no-owner \
  --no-privileges \
  --file="$tmp"

# Publish only an archive whose catalog can be read by pg_restore.
pg_restore --list "$tmp" >/dev/null
hash=$(sha256sum "$tmp" | awk '{print $1}')
printf '%s  %s\n' "$hash" "$(basename "$output")" >"$checksum_tmp"

mv "$tmp" "$output"
mv "$checksum_tmp" "${output}.sha256"
trap - EXIT INT TERM

printf 'PostgreSQL backup created: %s\n' "$output"
printf 'Checksum created: %s.sha256\n' "$output"
