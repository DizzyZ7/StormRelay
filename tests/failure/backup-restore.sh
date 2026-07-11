#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT_DIR"

PROJECT_NAME=${STORMRELAY_DRILL_PROJECT:-stormrelay-backup-restore}
COMPOSE=(docker compose --project-name "$PROJECT_NAME" -f deploy/compose/docker-compose.yml)
TMP_DIR=$(mktemp -d)
AUTH_HEADER='Authorization: Bearer local-development-only-change-me'
SOURCE_ID=''
SOURCE_SECRET=''
PRIMARY_IDEMPOTENCY_KEY='backup-restore-drill-v1'
PRIMARY_EVENT_ID='backup-restore-event-v1'
PRIMARY_BODY='{"title":"Backup and restore drill","type":"stormrelay.operations.backup_restore","severity":"critical","service":"backup-drill","environment":"ci","labels":{"drill":"backup-restore"}}'
POST_RESTORE_IDEMPOTENCY_KEY='backup-restore-post-restore-v1'
POST_RESTORE_EVENT_ID='backup-restore-post-restore-event-v1'
POST_RESTORE_BODY='{"title":"Post-restore source credential check","type":"stormrelay.operations.backup_restore.verify","severity":"warning","service":"backup-drill","environment":"ci","labels":{"drill":"backup-restore","phase":"post-restore"}}'

cleanup() {
  status=$?
  if [[ $status -ne 0 ]]; then
    echo '--- docker compose ps ---' >&2
    "${COMPOSE[@]}" ps >&2 || true
    echo '--- server and worker logs ---' >&2
    "${COMPOSE[@]}" logs --no-color server worker postgres nats >&2 || true
    if [[ -f "$TMP_DIR/before.snapshot" ]]; then
      echo '--- pre-restore snapshot keys ---' >&2
      cut -d= -f1 "$TMP_DIR/before.snapshot" >&2 || true
    fi
    if [[ -f "$TMP_DIR/after.snapshot" ]]; then
      echo '--- post-restore snapshot keys ---' >&2
      cut -d= -f1 "$TMP_DIR/after.snapshot" >&2 || true
    fi
  fi
  "${COMPOSE[@]}" down -v >/dev/null 2>&1 || true
  rm -rf "$TMP_DIR"
  exit "$status"
}
trap cleanup EXIT INT TERM

wait_http() {
  local url=$1
  local label=$2
  local attempts=${3:-60}
  for ((i = 1; i <= attempts; i++)); do
    if curl -fsS "$url" >/dev/null; then
      return 0
    fi
    sleep 2
  done
  echo "$label did not become ready: $url" >&2
  return 1
}

sql() {
  local database=$1
  local query=$2
  "${COMPOSE[@]}" exec -T -e PGTZ=UTC postgres \
    psql -X -qAt -v ON_ERROR_STOP=1 -U stormrelay -d "$database" -c "$query"
}

wait_for_sql_equals() {
  local database=$1
  local query=$2
  local expected=$3
  local label=$4
  local value=''
  for ((i = 1; i <= 90; i++)); do
    value=$(sql "$database" "$query")
    if [[ "$value" == "$expected" ]]; then
      return 0
    fi
    sleep 1
  done
  echo "$label: expected $expected, got ${value:-<empty>}" >&2
  return 1
}

wait_for_sql_at_least() {
  local database=$1
  local query=$2
  local minimum=$3
  local label=$4
  local value=0
  for ((i = 1; i <= 90; i++)); do
    value=$(sql "$database" "$query")
    if [[ "$value" =~ ^[0-9]+$ ]] && ((value >= minimum)); then
      return 0
    fi
    sleep 1
  done
  echo "$label: expected at least $minimum, got ${value:-<empty>}" >&2
  return 1
}

digest_query() {
  local database=$1
  local query=$2
  sql "$database" "$query" | sha256sum | awk '{print $1}'
}

snapshot_database() {
  local database=$1
  local output=$2
  {
    echo "schema_migrations.count=$(sql "$database" 'SELECT count(*) FROM schema_migrations')"
    echo "tenants.count=$(sql "$database" 'SELECT count(*) FROM tenants')"
    echo "event_sources.count=$(sql "$database" 'SELECT count(*) FROM event_sources')"
    echo "raw_events.count=$(sql "$database" 'SELECT count(*) FROM raw_events')"
    echo "normalized_events.count=$(sql "$database" 'SELECT count(*) FROM normalized_events')"
    echo "event_duplicates.count=$(sql "$database" 'SELECT count(*) FROM event_duplicates')"
    echo "incidents.count=$(sql "$database" 'SELECT count(*) FROM incidents')"
    echo "incident_events.count=$(sql "$database" 'SELECT count(*) FROM incident_events')"
    echo "incident_transitions.count=$(sql "$database" 'SELECT count(*) FROM incident_transitions')"
    echo "audit_entries.count=$(sql "$database" 'SELECT count(*) FROM audit_entries')"

    echo "schema_migrations.sha256=$(digest_query "$database" "SELECT row_to_json(t)::text FROM (SELECT version, applied_at FROM schema_migrations ORDER BY version) t")"
    echo "tenants.sha256=$(digest_query "$database" "SELECT row_to_json(t)::text FROM (SELECT id, slug, name, created_at, version FROM tenants ORDER BY id) t")"
    echo "event_sources.sha256=$(digest_query "$database" "SELECT row_to_json(t)::text FROM (SELECT id, tenant_id, name, kind, auth_mode, encode(encrypted_secret, 'hex') AS encrypted_secret, encode(bearer_hash, 'hex') AS bearer_hash, enabled, rate_limit_per_second, rate_limit_burst, created_at, updated_at, version FROM event_sources ORDER BY id) t")"
    echo "raw_events.sha256=$(digest_query "$database" "SELECT row_to_json(t)::text FROM (SELECT id, tenant_id, source_id, content_type, encode(payload_sha256, 'hex') AS payload_sha256, octet_length(payload) AS payload_bytes, received_at, request_id, created_at FROM raw_events ORDER BY id) t")"
    echo "normalized_events.sha256=$(digest_query "$database" "SELECT row_to_json(t)::text FROM (SELECT id, tenant_id, source_id, raw_event_id, ce_id, source_event_id, idempotency_key, dedupe_key, dedupe_bucket, fingerprint, ce_source, ce_type, subject, event_time, data_content_type, schema_version, trace_parent, labels, severity, duplicate_count, created_at FROM normalized_events ORDER BY id) t")"
    echo "event_duplicates.sha256=$(digest_query "$database" "SELECT row_to_json(t)::text FROM (SELECT id, tenant_id, canonical_event_id, raw_event_id, duplicate_number, reason, received_at, created_at FROM event_duplicates ORDER BY id) t")"
    echo "incidents.sha256=$(digest_query "$database" "SELECT row_to_json(t)::text FROM (SELECT id, tenant_id, correlation_key, title, state, severity, service, environment, assigned_team_id, first_event_at, last_event_at, acknowledged_at, resolved_at, created_at, updated_at, version FROM incidents ORDER BY id) t")"
    echo "incident_events.sha256=$(digest_query "$database" "SELECT row_to_json(t)::text FROM (SELECT incident_id, event_id, attached_at FROM incident_events ORDER BY incident_id, event_id) t")"
    echo "incident_transitions.sha256=$(digest_query "$database" "SELECT row_to_json(t)::text FROM (SELECT id, tenant_id, incident_id, from_state, to_state, actor_type, actor_id, reason, request_id, trace_id, created_at FROM incident_transitions ORDER BY id) t")"
    echo "audit_entries.sha256=$(digest_query "$database" "SELECT row_to_json(t)::text FROM (SELECT id, tenant_id, actor_type, actor_id, action, resource_type, resource_id, request_id, trace_id, encode(before_hash, 'hex') AS before_hash, encode(after_hash, 'hex') AS after_hash, metadata, created_at FROM audit_entries ORDER BY id) t")"
  } >"$output"
}

verify_payload_hashes() {
  local database=$1
  sql "$database" "SELECT encode(payload, 'hex') || '|' || encode(payload_sha256, 'hex') FROM raw_events ORDER BY id" |
    python3 -c '
import hashlib
import sys

checked = 0
for line in sys.stdin:
    payload_hex, expected = line.rstrip("\n").split("|", 1)
    actual = hashlib.sha256(bytes.fromhex(payload_hex)).hexdigest()
    if actual != expected:
        raise SystemExit("raw payload SHA-256 mismatch")
    checked += 1
if checked == 0:
    raise SystemExit("no raw events were available for payload verification")
'
}

verify_no_orphans() {
  local database=$1
  local orphans
  orphans=$(sql "$database" "
    SELECT
      (SELECT count(*) FROM normalized_events n LEFT JOIN raw_events r ON r.id = n.raw_event_id WHERE r.id IS NULL) +
      (SELECT count(*) FROM event_duplicates d LEFT JOIN normalized_events n ON n.id = d.canonical_event_id WHERE n.id IS NULL) +
      (SELECT count(*) FROM event_duplicates d LEFT JOIN raw_events r ON r.id = d.raw_event_id WHERE r.id IS NULL) +
      (SELECT count(*) FROM incident_events ie LEFT JOIN incidents i ON i.id = ie.incident_id WHERE i.id IS NULL) +
      (SELECT count(*) FROM incident_events ie LEFT JOIN normalized_events n ON n.id = ie.event_id WHERE n.id IS NULL) +
      (SELECT count(*) FROM incident_transitions it LEFT JOIN incidents i ON i.id = it.incident_id WHERE i.id IS NULL) +
      (SELECT count(*) FROM audit_entries a LEFT JOIN tenants t ON t.id = a.tenant_id WHERE t.id IS NULL)
  ")
  if [[ "$orphans" != '0' ]]; then
    echo "restored database contains $orphans orphaned records" >&2
    return 1
  fi
}

send_signed_event() {
  local body=$1
  local idempotency_key=$2
  local event_id=$3
  local response_file=$4
  local timestamp signature status
  timestamp=$(date +%s)
  signature=$(TIMESTAMP="$timestamp" EVENT_BODY="$body" SOURCE_SECRET="$SOURCE_SECRET" python3 -c '
import hashlib
import hmac
import os
message = (os.environ["TIMESTAMP"] + "." + os.environ["EVENT_BODY"]).encode()
print(hmac.new(os.environ["SOURCE_SECRET"].encode(), message, hashlib.sha256).hexdigest())
')
  status=$(curl -sS -o "$response_file" -w '%{http_code}' \
    -H "X-StormRelay-Timestamp: $timestamp" \
    -H "X-StormRelay-Signature: sha256=$signature" \
    -H "Idempotency-Key: $idempotency_key" \
    -H "X-Event-ID: $event_id" \
    -H 'Content-Type: application/json' \
    --data-binary "$body" \
    "http://localhost:8080/api/v1/webhooks/$SOURCE_ID")
  if [[ "$status" != '202' ]]; then
    echo "webhook returned HTTP $status" >&2
    cat "$response_file" >&2
    return 1
  fi
}

publish_core_nats() {
  local subject=$1
  local payload=$2
  NATS_SUBJECT="$subject" EVENT_ENVELOPE="$payload" python3 -c '
import os
import socket

subject = os.environ["NATS_SUBJECT"]
payload = os.environ["EVENT_ENVELOPE"].encode("utf-8")
with socket.create_connection(("127.0.0.1", 4222), timeout=5) as sock:
    sock.settimeout(5)
    greeting = sock.recv(65536)
    if b"INFO " not in greeting:
        raise SystemExit("NATS did not send an INFO greeting")
    sock.sendall(b"CONNECT {\"verbose\":false,\"pedantic\":false}\r\n")
    sock.sendall(f"PUB {subject} {len(payload)}\r\n".encode("ascii"))
    sock.sendall(payload + b"\r\nPING\r\n")
    received = b""
    while b"PONG\r\n" not in received:
        chunk = sock.recv(65536)
        if not chunk:
            raise SystemExit("NATS closed the connection before PONG")
        received += chunk
'
}

echo 'Starting isolated StormRelay backup/restore drill environment'
"${COMPOSE[@]}" down -v >/dev/null 2>&1 || true
"${COMPOSE[@]}" up --build -d postgres nats server worker
wait_http 'http://localhost:8080/readyz' 'server'
wait_http 'http://localhost:8081/readyz' 'worker'

SOURCE_JSON=$(curl -fsS \
  -H "$AUTH_HEADER" \
  -H 'Content-Type: application/json' \
  -d '{"name":"backup-restore-drill","kind":"generic","auth_mode":"hmac-sha256"}' \
  http://localhost:8080/api/v1/sources)
SOURCE_ID=$(printf '%s' "$SOURCE_JSON" | python3 -c 'import json,sys; print(json.load(sys.stdin)["source"]["id"])')
SOURCE_SECRET=$(printf '%s' "$SOURCE_JSON" | python3 -c 'import json,sys; print(json.load(sys.stdin)["credential"])')
if [[ -z "$SOURCE_ID" || -z "$SOURCE_SECRET" ]]; then
  echo 'source creation did not return an ID and one-time credential' >&2
  exit 1
fi

send_signed_event "$PRIMARY_BODY" "$PRIMARY_IDEMPOTENCY_KEY" "$PRIMARY_EVENT_ID" "$TMP_DIR/primary-response.json"
wait_for_sql_equals stormrelay "SELECT count(*) FROM normalized_events WHERE idempotency_key = '$PRIMARY_IDEMPOTENCY_KEY'" 1 'primary event persistence'
wait_for_sql_equals stormrelay "SELECT count(*) FROM incident_events ie JOIN normalized_events n ON n.id = ie.event_id WHERE n.idempotency_key = '$PRIMARY_IDEMPOTENCY_KEY'" 1 'incident correlation'
wait_for_sql_at_least stormrelay "SELECT count(*) FROM audit_entries" 2 'audit persistence'

# Stop application writes. pg_dump is transactionally consistent, but the drill also
# establishes a clear recovery point and prevents background delivery updates.
"${COMPOSE[@]}" stop server worker
snapshot_database stormrelay "$TMP_DIR/before.snapshot"
verify_payload_hashes stormrelay

"${COMPOSE[@]}" exec -T postgres pg_dump \
  --format=custom \
  --no-owner \
  --no-privileges \
  --file=/tmp/stormrelay-backup-restore.dump \
  --username=stormrelay \
  stormrelay
"${COMPOSE[@]}" exec -T postgres sh -c 'test -s /tmp/stormrelay-backup-restore.dump'

# Keep the original database only for comparison. Restore into a newly created database
# with the production database name so the unchanged server/worker configuration can reconnect.
sql postgres "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = 'stormrelay' AND pid <> pg_backend_pid()"
sql postgres 'ALTER DATABASE stormrelay RENAME TO stormrelay_before_restore'
sql postgres 'CREATE DATABASE stormrelay OWNER stormrelay'
"${COMPOSE[@]}" exec -T postgres pg_restore \
  --exit-on-error \
  --no-owner \
  --no-privileges \
  --username=stormrelay \
  --dbname=stormrelay \
  /tmp/stormrelay-backup-restore.dump

snapshot_database stormrelay "$TMP_DIR/after.snapshot"
if ! diff -u "$TMP_DIR/before.snapshot" "$TMP_DIR/after.snapshot"; then
  echo 'restored database does not match the pre-backup invariant snapshot' >&2
  exit 1
fi
verify_payload_hashes stormrelay
verify_no_orphans stormrelay

if sql stormrelay "UPDATE audit_entries SET action = 'tampered' WHERE id = (SELECT id FROM audit_entries ORDER BY created_at LIMIT 1)" >/dev/null 2>&1; then
  echo 'append-only audit trigger did not reject a mutation after restore' >&2
  exit 1
fi

EVENT_ENVELOPE=$(sql stormrelay "
  SELECT json_build_object(
    'id', n.ce_id,
    'source', n.ce_source,
    'type', n.ce_type,
    'subject', n.subject,
    'time', n.event_time,
    'datacontenttype', n.data_content_type,
    'schema_version', n.schema_version,
    'tenant_id', n.tenant_id,
    'source_id', n.source_id,
    'traceparent', n.trace_parent,
    'labels', n.labels,
    'severity', n.severity,
    'raw_payload', convert_from(r.payload, 'UTF8')::json,
    'source_event_id', n.source_event_id,
    'idempotency_key', n.idempotency_key,
    'fingerprint', n.fingerprint,
    'received_at', r.received_at,
    'request_id', r.request_id
  )::text
  FROM normalized_events n
  JOIN raw_events r ON r.id = n.raw_event_id
  WHERE n.idempotency_key = '$PRIMARY_IDEMPOTENCY_KEY'
  ORDER BY n.created_at
  LIMIT 1
")
if [[ -z "$EVENT_ENVELOPE" ]]; then
  echo 'could not reconstruct the persisted event envelope for redelivery verification' >&2
  exit 1
fi

NORMALIZED_BEFORE=$(sql stormrelay 'SELECT count(*) FROM normalized_events')
INCIDENTS_BEFORE=$(sql stormrelay 'SELECT count(*) FROM incidents')
DUPLICATES_BEFORE=$(sql stormrelay 'SELECT count(*) FROM event_duplicates')

"${COMPOSE[@]}" start server worker
wait_http 'http://localhost:8080/readyz' 'server after restore'
wait_http 'http://localhost:8081/readyz' 'worker after restore'

# Publish the exact persisted envelope without a new Nats-Msg-Id. This models a JetStream
# redelivery against the restored deduplication history rather than a new HTTP delivery.
publish_core_nats 'stormrelay.events.v1' "$EVENT_ENVELOPE"
wait_for_sql_at_least stormrelay 'SELECT count(*) FROM event_duplicates' "$((DUPLICATES_BEFORE + 1))" 'post-restore duplicate audit'
wait_for_sql_equals stormrelay 'SELECT count(*) FROM normalized_events' "$NORMALIZED_BEFORE" 'canonical event count after redelivery'
wait_for_sql_equals stormrelay 'SELECT count(*) FROM incidents' "$INCIDENTS_BEFORE" 'incident count after redelivery'

# The restored encrypted source credential must still decrypt with the separately retained
# master key. A fresh signed event proves the source remains usable after recovery.
send_signed_event "$POST_RESTORE_BODY" "$POST_RESTORE_IDEMPOTENCY_KEY" "$POST_RESTORE_EVENT_ID" "$TMP_DIR/post-restore-response.json"
wait_for_sql_equals stormrelay "SELECT count(*) FROM normalized_events WHERE idempotency_key = '$POST_RESTORE_IDEMPOTENCY_KEY'" 1 'post-restore signed event persistence'

echo 'Backup/restore drill passed: data fingerprints, payload hashes, foreign keys, audit immutability, dedupe redelivery, and encrypted source credentials were verified.'
