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
AUTH='Authorization: Bearer local-development-only-change-me'
curl -fsS -H "$AUTH" http://localhost:8080/api/v1/version

# Register and exercise the side-effect-free process plugin over the versioned protocol.
PLUGIN=$(curl -fsS -H "$AUTH" -H 'Content-Type: application/json' \
  -d '{"key":"echo","endpoint":"http://echo-plugin:8090","auth_mode":"none","timeout_seconds":10}' \
  http://localhost:8080/api/v1/plugins)
printf '%s' "$PLUGIN" | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["plugin_key"] == "echo" and d["protocol_version"] == "stormrelay.plugin/v1"'
curl -fsS -H "$AUTH" -H 'Content-Type: application/json' \
  -d '{"action":"echo","input":{"smoke":true}}' \
  http://localhost:8080/api/v1/plugins/echo/test | \
  python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["status"] == "succeeded" and d["output"]["echo"]["smoke"] is True'

# Milestone 2: runbook -> persisted wait -> approval -> success.
RUNBOOK_JSON=$(curl -fsS -H "$AUTH" -H 'Content-Type: application/yaml' --data-binary @examples/runbooks/approval-demo.yaml http://localhost:8080/api/v1/runbooks)
RUNBOOK_ID=$(printf '%s' "$RUNBOOK_JSON" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d.get("runbook_key") or d.get("runbook",{}).get("runbook_key") or "")')
test -n "$RUNBOOK_ID"
EXEC_JSON=$(curl -fsS -H "$AUTH" -H 'Content-Type: application/json' -d '{"dry_run":false}' "http://localhost:8080/api/v1/runbooks/$RUNBOOK_ID/run")
EXEC_ID=$(printf '%s' "$EXEC_JSON" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d.get("id") or d.get("execution_id") or d.get("execution",{}).get("id") or "")')
test -n "$EXEC_ID"

APPROVAL_ID=''
for i in $(seq 1 40); do
  APPROVALS=$(curl -fsS -H "$AUTH" "http://localhost:8080/api/v1/approvals?execution_id=$EXEC_ID")
  APPROVAL_ID=$(printf '%s' "$APPROVALS" | python3 -c 'import json,sys; d=json.load(sys.stdin); xs=d.get("items",d if isinstance(d,list) else []); print(next((x.get("id","") for x in xs if x.get("status")=="pending"),""))')
  test -n "$APPROVAL_ID" && break
  sleep 1
done
test -n "$APPROVAL_ID"
curl -fsS -H "$AUTH" -H 'Content-Type: application/json' -d '{"reason":"compose smoke"}' "http://localhost:8080/api/v1/approvals/$APPROVAL_ID/approve" >/dev/null

FINAL=''
for i in $(seq 1 40); do
  EXECUTION=$(curl -fsS -H "$AUTH" "http://localhost:8080/api/v1/executions/$EXEC_ID")
  FINAL=$(printf '%s' "$EXECUTION" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d.get("status") or d.get("execution",{}).get("status") or "")')
  case "$FINAL" in completed) break;; failed|canceled|ambiguous) echo "$EXECUTION"; exit 1;; esac
  sleep 1
done
test "$FINAL" = completed
