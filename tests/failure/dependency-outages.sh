#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT_DIR"

PROJECT_NAME=${STORMRELAY_FAILURE_PROJECT:-stormrelay-failure-dependencies}
COMPOSE=(docker compose --project-name "$PROJECT_NAME" -f deploy/compose/docker-compose.yml)
TMP_DIR=$(mktemp -d)
AUTH_HEADER='Authorization: Bearer local-development-only-change-me'
SOURCE_ID=''
SOURCE_SECRET=''

cleanup() {
  status=$?
  "${COMPOSE[@]}" unpause worker >/dev/null 2>&1 || true
  "${COMPOSE[@]}" start postgres nats >/dev/null 2>&1 || true
  if [[ $status -ne 0 ]]; then
    echo '--- docker compose ps ---' >&2
    "${COMPOSE[@]}" ps >&2 || true
    echo '--- server, worker, PostgreSQL, and NATS logs ---' >&2
    "${COMPOSE[@]}" logs --no-color server worker postgres nats >&2 || true
  fi
  "${COMPOSE[@]}" down -v >/dev/null 2>&1 || true
  rm -rf "$TMP_DIR"
  exit "$status"
}
trap cleanup EXIT INT TERM

wait_http() {
  local url=$1
  local label=$2
  local attempts=${3:-90}
  for ((i = 1; i <= attempts; i++)); do
    if curl -fsS "$url" >/dev/null; then
      return 0
    fi
    sleep 1
  done
  echo "$label did not become ready: $url" >&2
  return 1
}

wait_postgres() {
  for ((i = 1; i <= 90; i++)); do
    if "${COMPOSE[@]}" exec -T postgres pg_isready -q -U stormrelay -d stormrelay; then
      return 0
    fi
    sleep 1
  done
  echo 'PostgreSQL did not become ready' >&2
  return 1
}

sql() {
  local query=$1
  "${COMPOSE[@]}" exec -T postgres psql -X -qAt -v ON_ERROR_STOP=1 -U stormrelay -d stormrelay -c "$query"
}

wait_for_event() {
  local idempotency_key=$1
  local label=$2
  local count=''
  for ((i = 1; i <= 90; i++)); do
    count=$(sql "SELECT count(*) FROM normalized_events WHERE idempotency_key = '$idempotency_key'")
    if [[ "$count" == '1' ]]; then
      return 0
    fi
    sleep 1
  done
  echo "$label: expected one canonical event, got ${count:-<empty>}" >&2
  return 1
}

signature_for() {
  local timestamp=$1
  local body=$2
  TIMESTAMP="$timestamp" EVENT_BODY="$body" SOURCE_SECRET="$SOURCE_SECRET" python3 -c '
import hashlib
import hmac
import os
message = (os.environ["TIMESTAMP"] + "." + os.environ["EVENT_BODY"]).encode()
print(hmac.new(os.environ["SOURCE_SECRET"].encode(), message, hashlib.sha256).hexdigest())
'
}

send_signed_event() {
  local body=$1
  local idempotency_key=$2
  local event_id=$3
  local timestamp=$4
  local signature=$5
  local expected_status=$6
  local response_file=$7
  local status
  status=$(curl -sS --max-time 12 -o "$response_file" -w '%{http_code}' \
    -H "X-StormRelay-Timestamp: $timestamp" \
    -H "X-StormRelay-Signature: sha256=$signature" \
    -H "Idempotency-Key: $idempotency_key" \
    -H "X-Event-ID: $event_id" \
    -H 'Content-Type: application/json' \
    --data-binary "$body" \
    "http://localhost:8080/api/v1/webhooks/$SOURCE_ID")
  if [[ "$status" != "$expected_status" ]]; then
    echo "webhook returned HTTP $status, expected $expected_status" >&2
    cat "$response_file" >&2 || true
    return 1
  fi
}

new_signed_event() {
  local body=$1
  local idempotency_key=$2
  local event_id=$3
  local response_file=$4
  local timestamp signature
  timestamp=$(date +%s)
  signature=$(signature_for "$timestamp" "$body")
  send_signed_event "$body" "$idempotency_key" "$event_id" "$timestamp" "$signature" 202 "$response_file"
}

echo 'Starting isolated dependency failure environment'
"${COMPOSE[@]}" down -v >/dev/null 2>&1 || true
"${COMPOSE[@]}" up --build -d postgres nats server worker
wait_postgres
wait_http 'http://localhost:8080/readyz' 'server'
wait_http 'http://localhost:8081/readyz' 'worker'

SOURCE_JSON=$(curl -fsS \
  -H "$AUTH_HEADER" \
  -H 'Content-Type: application/json' \
  -d '{"name":"dependency-failure-drill","kind":"generic","auth_mode":"hmac-sha256"}' \
  http://localhost:8080/api/v1/sources)
SOURCE_ID=$(printf '%s' "$SOURCE_JSON" | python3 -c 'import json,sys; print(json.load(sys.stdin)["source"]["id"])')
SOURCE_SECRET=$(printf '%s' "$SOURCE_JSON" | python3 -c 'import json,sys; print(json.load(sys.stdin)["credential"])')
if [[ -z "$SOURCE_ID" || -z "$SOURCE_SECRET" ]]; then
  echo 'source creation did not return an ID and one-time credential' >&2
  exit 1
fi

# Boundary 1: an HTTP 503 means JetStream did not acknowledge durable acceptance.
# The identical signed request must be retryable after NATS recovers; replay
# protection is committed only after a successful JetStream publish acknowledgement.
NATS_RETRY_BODY='{"title":"NATS ingress retry","type":"stormrelay.failure.nats_ingress","severity":"warning","service":"failure-drill","environment":"ci"}'
NATS_RETRY_IDEM='failure-nats-ingress-retry-v1'
NATS_RETRY_EVENT='failure-nats-ingress-event-v1'
NATS_RETRY_TIMESTAMP=$(date +%s)
NATS_RETRY_SIGNATURE=$(signature_for "$NATS_RETRY_TIMESTAMP" "$NATS_RETRY_BODY")
"${COMPOSE[@]}" stop nats
send_signed_event "$NATS_RETRY_BODY" "$NATS_RETRY_IDEM" "$NATS_RETRY_EVENT" "$NATS_RETRY_TIMESTAMP" "$NATS_RETRY_SIGNATURE" 503 "$TMP_DIR/nats-unavailable.json"
"${COMPOSE[@]}" start nats
wait_http 'http://localhost:8080/readyz' 'server after NATS restart'
wait_http 'http://localhost:8081/readyz' 'worker after NATS restart'
send_signed_event "$NATS_RETRY_BODY" "$NATS_RETRY_IDEM" "$NATS_RETRY_EVENT" "$NATS_RETRY_TIMESTAMP" "$NATS_RETRY_SIGNATURE" 202 "$TMP_DIR/nats-retry.json"
wait_for_event "$NATS_RETRY_IDEM" 'NATS ingress retry recovery'

# Boundary 2: once the server returns 202, the message is in JetStream. Stopping
# PostgreSQL before the worker consumes it must result in NAK/redelivery, not loss.
POSTGRES_BODY='{"title":"PostgreSQL worker outage","type":"stormrelay.failure.postgres","severity":"critical","service":"failure-drill","environment":"ci"}'
POSTGRES_IDEM='failure-postgres-worker-v1'
"${COMPOSE[@]}" pause worker
new_signed_event "$POSTGRES_BODY" "$POSTGRES_IDEM" 'failure-postgres-event-v1' "$TMP_DIR/postgres-accepted.json"
"${COMPOSE[@]}" stop postgres
"${COMPOSE[@]}" unpause worker
sleep 3
"${COMPOSE[@]}" start postgres
wait_postgres
wait_http 'http://localhost:8080/readyz' 'server after PostgreSQL restart'
wait_http 'http://localhost:8081/readyz' 'worker after PostgreSQL restart'
wait_for_event "$POSTGRES_IDEM" 'confirmed event after PostgreSQL outage'

# Boundary 3: a confirmed message remains in the file-backed JetStream volume
# while NATS is stopped. The durable consumer resumes after reconnect and acks only
# after the PostgreSQL transaction succeeds.
NATS_WORKER_BODY='{"title":"NATS worker outage","type":"stormrelay.failure.nats_worker","severity":"critical","service":"failure-drill","environment":"ci"}'
NATS_WORKER_IDEM='failure-nats-worker-v1'
"${COMPOSE[@]}" pause worker
new_signed_event "$NATS_WORKER_BODY" "$NATS_WORKER_IDEM" 'failure-nats-worker-event-v1' "$TMP_DIR/nats-worker-accepted.json"
"${COMPOSE[@]}" stop nats
"${COMPOSE[@]}" unpause worker
sleep 3
"${COMPOSE[@]}" start nats
wait_http 'http://localhost:8080/readyz' 'server after second NATS restart'
wait_http 'http://localhost:8081/readyz' 'worker after second NATS restart'
wait_for_event "$NATS_WORKER_IDEM" 'confirmed event after NATS outage'

for idempotency_key in "$NATS_RETRY_IDEM" "$POSTGRES_IDEM" "$NATS_WORKER_IDEM"; do
  canonical=$(sql "SELECT count(*) FROM normalized_events WHERE idempotency_key = '$idempotency_key'")
  incidents=$(sql "SELECT count(*) FROM incident_events ie JOIN normalized_events n ON n.id=ie.event_id WHERE n.idempotency_key = '$idempotency_key'")
  if [[ "$canonical" != '1' || "$incidents" != '1' ]]; then
    echo "recovery invariant failed for $idempotency_key: canonical=$canonical incident_links=$incidents" >&2
    exit 1
  fi
done

echo 'Dependency outage drill passed: failed NATS acceptance remained retryable, and confirmed events survived PostgreSQL and NATS outages without duplicate canonical records.'
