#!/bin/sh
set -eu
COMPOSE="docker compose -f deploy/compose/docker-compose.yml"
cleanup() {
  status=$?
  if [ "$status" -ne 0 ]; then
    echo "--- docker compose ps ---" >&2
    $COMPOSE ps >&2 || true
    echo "--- docker compose logs ---" >&2
    $COMPOSE logs --no-color >&2 || true
  fi
  $COMPOSE down -v >/dev/null 2>&1 || true
  exit "$status"
}
trap cleanup EXIT INT TERM

$COMPOSE up --build -d
for i in $(seq 1 60); do
  if curl -fsS http://localhost:8080/readyz >/dev/null; then
    break
  fi
  sleep 2
done
curl -sS --fail-with-body http://localhost:8080/readyz >/dev/null

BOOTSTRAP='Authorization: Bearer local-development-only-change-me'
ACCOUNT=$(curl -sS --fail-with-body -H "$BOOTSTRAP" -H 'Content-Type: application/json' \
  -d '{"name":"compose-viewer","roles":["viewer"]}' \
  http://localhost:8080/api/v1/service-accounts)
ACCOUNT_ID=$(printf '%s' "$ACCOUNT" | python3 -c 'import json,sys; print(json.load(sys.stdin)["id"])')
test -n "$ACCOUNT_ID"

KEY_RESPONSE=$(curl -sS --fail-with-body -H "$BOOTSTRAP" -H 'Content-Type: application/json' \
  -d '{}' "http://localhost:8080/api/v1/service-accounts/$ACCOUNT_ID/keys")
KEY_ID=$(printf '%s' "$KEY_RESPONSE" | python3 -c 'import json,sys; print(json.load(sys.stdin)["key"]["id"])')
CREDENTIAL=$(printf '%s' "$KEY_RESPONSE" | python3 -c 'import json,sys; print(json.load(sys.stdin)["credential"])')
test -n "$KEY_ID"
test -n "$CREDENTIAL"
VIEWER="Authorization: Bearer $CREDENTIAL"

curl -sS --fail-with-body -H "$VIEWER" http://localhost:8080/api/v1/version >/dev/null
curl -sS --fail-with-body -H "$VIEWER" http://localhost:8080/api/v1/incidents >/dev/null

DENIED_CODE=$(curl -sS -o /tmp/denied.json -w '%{http_code}' -H "$VIEWER" -H 'Content-Type: application/json' \
  -d '{"title":"viewer must not create this incident","severity":"warning"}' \
  http://localhost:8080/api/v1/incidents)
test "$DENIED_CODE" = "403"
python3 -c 'import json; d=json.load(open("/tmp/denied.json")); assert d["error"]["code"] == "forbidden" and d["error"]["details"]["required_permission"] == "incidents:write"'

curl -sS --fail-with-body -H "$BOOTSTRAP" http://localhost:8080/api/v1/audit/export | \
  grep -q '"action":"authorization.denied"'

curl -sS --fail-with-body -H "$BOOTSTRAP" -H 'Content-Type: application/json' -d '{}' \
  "http://localhost:8080/api/v1/service-account-keys/$KEY_ID/revoke" >/dev/null
REVOKED_CODE=$(curl -sS -o /tmp/revoked.json -w '%{http_code}' -H "$VIEWER" http://localhost:8080/api/v1/version)
test "$REVOKED_CODE" = "401"
python3 -c 'import json; d=json.load(open("/tmp/revoked.json")); assert d["error"]["code"] == "unauthorized"'
