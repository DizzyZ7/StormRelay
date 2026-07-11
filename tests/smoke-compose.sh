#!/bin/sh
set -eu
COMPOSE="docker compose -f deploy/compose/docker-compose.yml"
TMP_DIR=$(mktemp -d)
cleanup() {
  status=$?
  if [ "$status" -ne 0 ]; then
    echo "--- docker compose ps ---" >&2
    $COMPOSE ps >&2 || true
    echo "--- docker compose logs ---" >&2
    $COMPOSE logs --no-color >&2 || true
  fi
  $COMPOSE down -v >/dev/null 2>&1 || true
  rm -rf "$TMP_DIR"
  exit "$status"
}
trace_id_from_headers() {
  awk 'BEGIN{IGNORECASE=1} /^traceparent:/ {gsub("\r", "", $2); print $2}' "$1" | tail -n 1 | awk -F- '{print $2}'
}
wait_for_tempo_span() {
  tempo_trace_id=$1
  tempo_span_name=$2
  tempo_output=$3
  for tempo_attempt in $(seq 1 60); do
    if curl -fsS "http://localhost:3200/api/traces/$tempo_trace_id" >"$tempo_output.tmp"; then
      if grep -F "$tempo_span_name" "$tempo_output.tmp" >/dev/null 2>&1; then
        mv "$tempo_output.tmp" "$tempo_output"
        return 0
      fi
    fi
    sleep 1
  done
  echo "trace $tempo_trace_id did not contain $tempo_span_name" >&2
  return 1
}
trap cleanup EXIT INT TERM
$COMPOSE up --build -d
for i in $(seq 1 60); do
  if curl -fsS http://localhost:8080/readyz >/dev/null && curl -fsS http://localhost:8081/readyz >/dev/null; then break; fi
  sleep 2
done
curl -sS --fail-with-body http://localhost:8080/readyz
curl -sS --fail-with-body http://localhost:8081/readyz
AUTH='Authorization: Bearer local-development-only-change-me'
curl -sS --fail-with-body -H "$AUTH" http://localhost:8080/api/v1/version

# Collector, Tempo, Prometheus, and Grafana must all be usable, not merely started.
for i in $(seq 1 60); do
  if curl -fsS http://localhost:13133/ >/dev/null && curl -fsS http://localhost:3200/ready >/dev/null && curl -fsS http://localhost:9090/-/ready >/dev/null && curl -fsS http://localhost:3000/api/health >/dev/null; then break; fi
  sleep 2
done
curl -sS --fail-with-body http://localhost:13133/ >/dev/null
curl -sS --fail-with-body http://localhost:3200/ready >/dev/null
curl -sS --fail-with-body http://localhost:9090/-/ready >/dev/null
curl -sS --fail-with-body http://localhost:3000/api/health | \
  python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["database"] == "ok"'

RULES=''
for i in $(seq 1 30); do
  RULES=$(curl -sS --fail-with-body http://localhost:9090/api/v1/rules)
  if printf '%s' "$RULES" | python3 -c 'import json,sys; d=json.load(sys.stdin); names={r.get("name") for g in d["data"]["groups"] for r in g.get("rules",[])}; raise SystemExit(0 if {"StormRelayTargetDown","StormRelayEventPipelineStalled","StormRelayDatabasePoolSaturated"} <= names else 1)'; then break; fi
  sleep 1
done
printf '%s' "$RULES" | python3 -c 'import json,sys; d=json.load(sys.stdin); names={r.get("name") for g in d["data"]["groups"] for r in g.get("rules",[])}; assert {"StormRelayTargetDown","StormRelayEventPipelineStalled","StormRelayDatabasePoolSaturated"} <= names'

DASHBOARD=''
for i in $(seq 1 30); do
  if DASHBOARD=$(curl -fsS --user admin:admin http://localhost:3000/api/dashboards/uid/stormrelay-operations); then break; fi
  sleep 1
done
printf '%s' "$DASHBOARD" | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["dashboard"]["uid"] == "stormrelay-operations" and d["dashboard"]["title"] == "StormRelay Operations" and len(d["dashboard"]["panels"]) >= 8'
curl -sS --fail-with-body --user admin:admin http://localhost:3000/api/datasources/uid/stormrelay-prometheus | \
  python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["uid"] == "stormrelay-prometheus" and d["url"] == "http://prometheus:9090"'
curl -sS --fail-with-body --user admin:admin http://localhost:3000/api/datasources/uid/stormrelay-tempo | \
  python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["uid"] == "stormrelay-tempo" and d["url"] == "http://tempo:3200"'

# Send a signed event and prove the response trace crosses the asynchronous event
# transaction and delayed notification outbox before arriving in Tempo.
SOURCE_JSON=$(curl -sS --fail-with-body -H "$AUTH" -H 'Content-Type: application/json' \
  -d '{"name":"compose-trace-source","kind":"generic","auth_mode":"hmac-sha256"}' \
  http://localhost:8080/api/v1/sources)
SOURCE_ID=$(printf '%s' "$SOURCE_JSON" | python3 -c 'import json,sys; print(json.load(sys.stdin)["source"]["id"])')
SOURCE_SECRET=$(printf '%s' "$SOURCE_JSON" | python3 -c 'import json,sys; print(json.load(sys.stdin)["credential"])')
SECRET_MARKER='compose-trace-secret-must-not-export'
EVENT_BODY='{"type":"com.stormrelay.compose.trace","title":"compose trace alert","severity":"critical","service":"compose-trace","environment":"smoke","resource":"trace-resource","labels":{"sensitive":"compose-trace-secret-must-not-export"}}'
EVENT_TIMESTAMP=$(date +%s)
EVENT_SIGNATURE=$(TIMESTAMP="$EVENT_TIMESTAMP" BODY="$EVENT_BODY" SECRET="$SOURCE_SECRET" python3 -c 'import hashlib,hmac,os; print(hmac.new(os.environ["SECRET"].encode(), (os.environ["TIMESTAMP"]+"."+os.environ["BODY"]).encode(), hashlib.sha256).hexdigest())')
EVENT_STATUS=$(curl -sS -D "$TMP_DIR/event-headers" -o "$TMP_DIR/event-response" -w '%{http_code}' \
  -H "X-StormRelay-Timestamp: $EVENT_TIMESTAMP" \
  -H "X-StormRelay-Signature: sha256=$EVENT_SIGNATURE" \
  -H 'X-Event-ID: compose-trace-event' \
  -H 'Content-Type: application/json' \
  --data-binary "$EVENT_BODY" \
  "http://localhost:8080/api/v1/webhooks/$SOURCE_ID")
test "$EVENT_STATUS" = 202
EVENT_TRACE_ID=$(trace_id_from_headers "$TMP_DIR/event-headers")
test "${#EVENT_TRACE_ID}" -eq 32
wait_for_tempo_span "$EVENT_TRACE_ID" stormrelay.notification.complete "$TMP_DIR/event-trace.json"
for span_name in stormrelay.nats.publish stormrelay.event.process stormrelay.event.transaction stormrelay.event.persist_raw stormrelay.event.deduplicate stormrelay.incident.correlate stormrelay.policy.evaluate stormrelay.notification.enqueue stormrelay.notification.deliver stormrelay.notification.complete; do
  grep -F "$span_name" "$TMP_DIR/event-trace.json" >/dev/null
done
if grep -F "$SECRET_MARKER" "$TMP_DIR/event-trace.json" >/dev/null; then
  echo 'sensitive event marker leaked into exported trace' >&2
  exit 1
fi

# Register and exercise the side-effect-free process plugin over the versioned protocol.
PLUGIN=$(curl -sS --fail-with-body -H "$AUTH" -H 'Content-Type: application/json' \
  -d '{"key":"echo","endpoint":"http://echo-plugin:8090","auth_mode":"none","timeout_seconds":10}' \
  http://localhost:8080/api/v1/plugins)
printf '%s' "$PLUGIN" | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["plugin_key"] == "echo" and d["protocol_version"] == "stormrelay.plugin/v1"'
PLUGIN_SECRET_MARKER='plugin-trace-secret-must-not-export'
curl -sS --fail-with-body -D "$TMP_DIR/plugin-headers" -o "$TMP_DIR/plugin-response" \
  -H "$AUTH" -H 'Content-Type: application/json' \
  -d '{"action":"echo","input":{"smoke":true,"secret":"plugin-trace-secret-must-not-export"}}' \
  http://localhost:8080/api/v1/plugins/echo/test
python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); assert d["status"] == "succeeded" and d["output"]["echo"]["smoke"] is True' "$TMP_DIR/plugin-response"
PLUGIN_TRACE_ID=$(trace_id_from_headers "$TMP_DIR/plugin-headers")
test "${#PLUGIN_TRACE_ID}" -eq 32
wait_for_tempo_span "$PLUGIN_TRACE_ID" stormrelay.plugin.call "$TMP_DIR/plugin-trace.json"
if grep -F "$PLUGIN_SECRET_MARKER" "$TMP_DIR/plugin-trace.json" >/dev/null; then
  echo 'sensitive plugin input leaked into exported trace' >&2
  exit 1
fi

# Milestone 2: runbook -> persisted wait -> approval -> success. The runbook
# worker restores the traceparent persisted by the run request.
RUNBOOK_JSON=$(curl -sS --fail-with-body -H "$AUTH" -H 'Content-Type: application/yaml' --data-binary @examples/runbooks/approval-demo.yaml http://localhost:8080/api/v1/runbooks)
RUNBOOK_ID=$(printf '%s' "$RUNBOOK_JSON" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d.get("runbook_key") or d.get("runbook",{}).get("runbook_key") or "")')
test -n "$RUNBOOK_ID"
curl -sS --fail-with-body -D "$TMP_DIR/runbook-headers" -o "$TMP_DIR/execution-response" \
  -H "$AUTH" -H 'Content-Type: application/json' -d '{"dry_run":false}' \
  "http://localhost:8080/api/v1/runbooks/$RUNBOOK_ID/run"
EXEC_ID=$(python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); print(d.get("id") or d.get("execution_id") or d.get("execution",{}).get("id") or "")' "$TMP_DIR/execution-response")
test -n "$EXEC_ID"
RUNBOOK_TRACE_ID=$(trace_id_from_headers "$TMP_DIR/runbook-headers")
test "${#RUNBOOK_TRACE_ID}" -eq 32

APPROVAL_ID=''
for i in $(seq 1 40); do
  APPROVALS=$(curl -sS --fail-with-body -H "$AUTH" "http://localhost:8080/api/v1/approvals?execution_id=$EXEC_ID")
  APPROVAL_ID=$(printf '%s' "$APPROVALS" | python3 -c 'import json,sys; d=json.load(sys.stdin); xs=d.get("items",d if isinstance(d,list) else []); print(next((x.get("id","") for x in xs if x.get("status")=="pending"),""))')
  test -n "$APPROVAL_ID" && break
  sleep 1
done
test -n "$APPROVAL_ID"
curl -sS --fail-with-body -H "$AUTH" -H 'Content-Type: application/json' -d '{"reason":"compose smoke"}' "http://localhost:8080/api/v1/approvals/$APPROVAL_ID/approve" >/dev/null

FINAL=''
for i in $(seq 1 40); do
  EXECUTION=$(curl -sS --fail-with-body -H "$AUTH" "http://localhost:8080/api/v1/executions/$EXEC_ID")
  FINAL=$(printf '%s' "$EXECUTION" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d.get("status") or d.get("execution",{}).get("status") or "")')
  case "$FINAL" in completed) break;; failed|canceled|ambiguous) echo "$EXECUTION"; exit 1;; esac
  sleep 1
done
test "$FINAL" = completed
wait_for_tempo_span "$RUNBOOK_TRACE_ID" stormrelay.runbook.step "$TMP_DIR/runbook-trace.json"
