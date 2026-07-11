#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT_DIR"

CLUSTER_NAME=${STORMRELAY_KIND_CLUSTER:-stormrelay}
NAMESPACE=${STORMRELAY_KIND_NAMESPACE:-stormrelay}
RELEASE=${STORMRELAY_KIND_RELEASE:-stormrelay}
NODE_IMAGE=${STORMRELAY_KIND_NODE_IMAGE:-kindest/node:v1.35.0@sha256:d98a69f4720ab889052267d9b26f2a79e4242e1c5d24f003f37d00ea5155f459}
KEEP_CLUSTER=${STORMRELAY_KIND_KEEP_CLUSTER:-false}
PORT_FORWARD_PID=''
TMP_DIR=$(mktemp -d)

cleanup() {
  status=$?
  if [[ -n "$PORT_FORWARD_PID" ]]; then
    kill "$PORT_FORWARD_PID" >/dev/null 2>&1 || true
  fi
  if [[ $status -ne 0 ]]; then
    echo '--- kind diagnostics ---' >&2
    kubectl -n "$NAMESPACE" get all,pvc,networkpolicy,poddisruptionbudget -o wide >&2 || true
    kubectl -n "$NAMESPACE" get events --sort-by=.lastTimestamp >&2 || true
    kubectl -n "$NAMESPACE" logs -l app.kubernetes.io/instance="$RELEASE" --all-containers --tail=200 >&2 || true
    helm -n "$NAMESPACE" get all "$RELEASE" >&2 || true
  fi
  if [[ "$KEEP_CLUSTER" != "true" ]]; then
    kind delete cluster --name "$CLUSTER_NAME" >/dev/null 2>&1 || true
  fi
  rm -rf "$TMP_DIR"
  exit "$status"
}
trap cleanup EXIT INT TERM

for command in docker kind kubectl helm curl python3; do
  command -v "$command" >/dev/null 2>&1 || { echo "required command is unavailable: $command" >&2; exit 2; }
done

kind delete cluster --name "$CLUSTER_NAME" >/dev/null 2>&1 || true

echo 'Building StormRelay server and worker images'
docker build --build-arg TARGET=stormrelay-server --build-arg VERSION=kind-smoke -t stormrelay-server:kind-smoke .
docker build --build-arg TARGET=stormrelay-worker --build-arg VERSION=kind-smoke -t stormrelay-worker:kind-smoke .

echo "Creating kind cluster $CLUSTER_NAME"
kind create cluster --name "$CLUSTER_NAME" --image "$NODE_IMAGE" --config deploy/kind/kind-config.yaml --wait 120s
kind load docker-image --name "$CLUSTER_NAME" stormrelay-server:kind-smoke stormrelay-worker:kind-smoke

kubectl create namespace "$NAMESPACE"
kubectl -n "$NAMESPACE" create secret generic stormrelay-app \
  --from-literal=master-key='AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=' \
  --from-literal=bootstrap-api-key='kind-smoke-api-key'
kubectl -n "$NAMESPACE" create secret generic stormrelay-demo \
  --from-literal=postgres-user='stormrelay' \
  --from-literal=postgres-password='stormrelay' \
  --from-literal=postgres-database='stormrelay' \
  --from-literal=database-url='postgres://stormrelay:stormrelay@stormrelay-postgres:5432/stormrelay?sslmode=disable'

# Validate both external-dependency production rendering and the demo install.
helm lint deploy/helm/stormrelay \
  --set secrets.existingSecret=external-app \
  --set database.url='postgres://user:password@postgres.example:5432/stormrelay?sslmode=require' \
  --set nats.url='nats://nats.example:4222'
helm template external deploy/helm/stormrelay \
  --set secrets.existingSecret=external-app \
  --set database.existingSecret=external-database \
  --set nats.existingSecret=external-nats \
  --set networkPolicy.additionalEgress[0].to[0].ipBlock.cidr='10.0.0.0/8' \
  >"$TMP_DIR/external-render.yaml"
grep -F 'kind: NetworkPolicy' "$TMP_DIR/external-render.yaml" >/dev/null
if grep -F 'kind: Secret' "$TMP_DIR/external-render.yaml" >/dev/null; then
  echo 'chart rendered a Secret instead of referencing an existing Secret' >&2
  exit 1
fi

helm lint deploy/helm/stormrelay -f deploy/kind/values.yaml
helm template "$RELEASE" deploy/helm/stormrelay -n "$NAMESPACE" -f deploy/kind/values.yaml >"$TMP_DIR/kind-render.yaml"
kubectl apply --dry-run=server -f "$TMP_DIR/kind-render.yaml" >/dev/null

helm upgrade --install "$RELEASE" deploy/helm/stormrelay \
  --namespace "$NAMESPACE" \
  --values deploy/kind/values.yaml \
  --wait --timeout 8m

kubectl -n "$NAMESPACE" rollout status deployment/stormrelay-server --timeout=180s
kubectl -n "$NAMESPACE" rollout status deployment/stormrelay-worker --timeout=180s
kubectl -n "$NAMESPACE" rollout status statefulset/stormrelay-postgres --timeout=180s
kubectl -n "$NAMESPACE" rollout status statefulset/stormrelay-nats --timeout=180s
kubectl -n "$NAMESPACE" get networkpolicy stormrelay >/dev/null
kubectl -n "$NAMESPACE" get serviceaccount stormrelay -o jsonpath='{.automountServiceAccountToken}' | grep -Fx 'false' >/dev/null

# Assert the production application containers remain non-root, read-only, and
# without privilege escalation or Linux capabilities.
for component in server worker; do
  kubectl -n "$NAMESPACE" get deployment "stormrelay-$component" -o json | python3 -c '
import json, sys
obj = json.load(sys.stdin)
pod = obj["spec"]["template"]["spec"]
container = pod["containers"][0]
psc = pod["securityContext"]
sc = container["securityContext"]
assert psc["runAsNonRoot"] is True
assert sc["allowPrivilegeEscalation"] is False
assert sc["readOnlyRootFilesystem"] is True
assert sc["privileged"] is False
assert sc["capabilities"]["drop"] == ["ALL"]
assert container["readinessProbe"]["httpGet"]["path"] == "/readyz"
assert container["livenessProbe"]["httpGet"]["path"] == "/healthz"
'
done

kubectl -n "$NAMESPACE" port-forward service/stormrelay-server 18080:8080 >"$TMP_DIR/port-forward.log" 2>&1 &
PORT_FORWARD_PID=$!
for attempt in $(seq 1 60); do
  if curl -fsS http://127.0.0.1:18080/readyz >/dev/null; then break; fi
  sleep 1
done
curl -fsS http://127.0.0.1:18080/readyz >/dev/null
curl -fsS -H 'Authorization: Bearer kind-smoke-api-key' http://127.0.0.1:18080/api/v1/version | \
  python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["version"] == "kind-smoke"'

SOURCE_JSON=$(curl -fsS \
  -H 'Authorization: Bearer kind-smoke-api-key' \
  -H 'Content-Type: application/json' \
  -d '{"name":"kind-smoke","kind":"generic","auth_mode":"hmac-sha256"}' \
  http://127.0.0.1:18080/api/v1/sources)
SOURCE_ID=$(printf '%s' "$SOURCE_JSON" | python3 -c 'import json,sys; print(json.load(sys.stdin)["source"]["id"])')
SOURCE_SECRET=$(printf '%s' "$SOURCE_JSON" | python3 -c 'import json,sys; print(json.load(sys.stdin)["credential"])')
BODY='{"title":"kind smoke alert","type":"stormrelay.kind.smoke","severity":"critical","service":"kind-smoke","environment":"kind","resource":"kind-cluster"}'
TIMESTAMP=$(date +%s)
SIGNATURE=$(TIMESTAMP="$TIMESTAMP" BODY="$BODY" SECRET="$SOURCE_SECRET" python3 -c 'import hashlib,hmac,os; print(hmac.new(os.environ["SECRET"].encode(), (os.environ["TIMESTAMP"]+"."+os.environ["BODY"]).encode(), hashlib.sha256).hexdigest())')
STATUS=$(curl -sS -o "$TMP_DIR/webhook-response.json" -w '%{http_code}' \
  -H "X-StormRelay-Timestamp: $TIMESTAMP" \
  -H "X-StormRelay-Signature: sha256=$SIGNATURE" \
  -H 'X-Event-ID: kind-smoke-event' \
  -H 'Content-Type: application/json' \
  --data-binary "$BODY" \
  "http://127.0.0.1:18080/api/v1/webhooks/$SOURCE_ID")
test "$STATUS" = 202

for attempt in $(seq 1 90); do
  INCIDENTS=$(curl -fsS -H 'Authorization: Bearer kind-smoke-api-key' 'http://127.0.0.1:18080/api/v1/incidents?service=kind-smoke&limit=10')
  if printf '%s' "$INCIDENTS" | python3 -c 'import json,sys; d=json.load(sys.stdin); raise SystemExit(0 if any(x.get("service")=="kind-smoke" for x in d.get("items",[])) else 1)'; then
    break
  fi
  sleep 1
done
printf '%s' "$INCIDENTS" | python3 -c 'import json,sys; d=json.load(sys.stdin); assert any(x.get("service")=="kind-smoke" for x in d.get("items",[]))'

# Re-run the release to verify an idempotent upgrade against persisted demo data.
helm upgrade --install "$RELEASE" deploy/helm/stormrelay \
  --namespace "$NAMESPACE" \
  --values deploy/kind/values.yaml \
  --wait --timeout 5m
curl -fsS http://127.0.0.1:18080/readyz >/dev/null

helm uninstall "$RELEASE" --namespace "$NAMESPACE" --wait
if kubectl -n "$NAMESPACE" get deployment stormrelay-server >/dev/null 2>&1; then
  echo 'Helm uninstall left the server deployment behind' >&2
  exit 1
fi

echo 'Helm/kind smoke passed: render, server-side validation, install, signed event flow, upgrade, security assertions, and uninstall succeeded.'
