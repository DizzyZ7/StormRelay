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
  if curl -fsS http://localhost:8080/readyz >/dev/null && curl -fsS http://localhost:8081/readyz >/dev/null; then break; fi
  sleep 2
done
curl -sS --fail-with-body http://localhost:8080/readyz
curl -sS --fail-with-body http://localhost:8081/readyz
AUTH='Authorization: Bearer local-development-only-change-me'
curl -sS --fail-with-body -H "$AUTH" http://localhost:8080/api/v1/version

# Prometheus must load the checked StormRelay rules, and Grafana must provision the datasource/dashboard.
for i in $(seq 1 60); do
  if curl -fsS http://localhost:9090/-/ready >/dev/null && curl -fsS http://localhost:3000/api/health >/dev/null; then break; fi
  sleep 2
done
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

# Register and exercise the side-effect-free process plugin over the versioned protocol.
PLUGIN=$(curl -sS --fail-with-body -H "$AUTH" -H 'Content-Type: application/json' \
  -d '{"key":"echo","endpoint":"http://echo-plugin:8090","auth_mode":"none","timeout_seconds":10}' \
  http://localhost:8080/api/v1/plugins)
printf '%s' "$PLUGIN" | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["plugin_key"] == "echo" and d["protocol_version"] == "stormrelay.plugin/v1"'
curl -sS --fail-with-body -H "$AUTH" -H 'Content-Type: application/json' \
  -d '{"action":"echo","input":{"smoke":true}}' \
  http://localhost:8080/api/v1/plugins/echo/test | \
  python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["status"] == "succeeded" and d["output"]["echo"]["smoke"] is True'

# Milestone 2: runbook -> persisted wait -> approval -> success.
RUNBOOK_JSON=$(curl -sS --fail-with-body -H "$AUTH" -H 'Content-Type: application/yaml' --data-binary @examples/runbooks/approval-demo.yaml http://localhost:8080/api/v1/runbooks)
RUNBOOK_ID=$(printf '%s' "$RUNBOOK_JSON" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d.get("runbook_key") or d.get("runbook",{}).get("runbook_key") or "")')
test -n "$RUNBOOK_ID"
EXEC_JSON=$(curl -sS --fail-with-body -H "$AUTH" -H 'Content-Type: application/json' -d '{"dry_run":false}' "http://localhost:8080/api/v1/runbooks/$RUNBOOK_ID/run")
EXEC_ID=$(printf '%s' "$EXEC_JSON" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d.get("id") or d.get("execution_id") or d.get("execution",{}).get("id") or "")')
test -n "$EXEC_ID"

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
