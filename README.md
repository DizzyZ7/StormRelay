# StormRelay

StormRelay is a self-hosted event-correlation and incident-response control plane for small product teams, SRE, NOC, DevOps, and security operations.

It accepts authenticated webhooks, preserves the original payload, normalizes events into a CloudEvents-compatible model, deduplicates concurrent deliveries, correlates events into incidents, evaluates explainable policies, notifies responders, runs durable response automation, and records an append-only audit trail.

> **Current status:** Milestones 0–3 are implemented on `main`: ingestion, incident lifecycle, policy evaluation, notification delivery, durable runbooks, process plugins, tenant-scoped service accounts, fail-closed RBAC, guarded OIDC federation, supported API SDKs, process-plugin SDKs, and a live conformance runner. Milestone 4 includes OpenTelemetry tracing, provisioned Prometheus alerts, Grafana operations dashboards, alert-specific runbooks, an automated PostgreSQL backup/restore drill, and deterministic failure-injection coverage for PostgreSQL, NATS, duplicates, poison messages, runbook recovery, plugins, and notification providers. The web UI, Kubernetes packaging, and release automation remain later work.

## Why not only Alertmanager or a webhook router?

Alertmanager is excellent at grouping and routing Prometheus alerts. A webhook router forwards requests. StormRelay sits after or beside them when a team needs durable raw-event retention, cross-source deduplication, incident state, human acknowledgement, explainable policy decisions, recovery-aware automation, and an audit history shared by operations and security.

StormRelay uses **at-least-once delivery**. It does not claim exactly-once processing. JetStream may redeliver; PostgreSQL uniqueness constraints and transactional consumers convert redelivery into an explicit duplicate record.

## Run the demo

Requirements: Docker with Compose, `curl`, `jq`, and `openssl`.

```bash
make demo-up
curl -fsS http://localhost:8080/readyz | jq
```

The development API key is `local-development-only-change-me`. It is intentionally limited to the local Compose file and must never be reused outside the demo.

The Compose stack also exposes:

- Prometheus at `http://localhost:9090` with StormRelay alert rules loaded;
- Grafana at `http://localhost:3000` with the `StormRelay Operations` dashboard pre-provisioned;
- development-only Grafana credentials `admin` / `admin`.

Replace the Grafana credentials and authentication configuration before exposing it outside a trusted development network.

Create a generic HMAC source:

```bash
SOURCE_JSON=$(curl -fsS \
  -H 'Authorization: Bearer local-development-only-change-me' \
  -H 'Content-Type: application/json' \
  -d '{"name":"checkout-alerts","kind":"generic","auth_mode":"hmac-sha256"}' \
  http://localhost:8080/api/v1/sources)

SOURCE_ID=$(printf '%s' "$SOURCE_JSON" | jq -r '.source.id')
SOURCE_SECRET=$(printf '%s' "$SOURCE_JSON" | jq -r '.credential')
```

Send a signed event:

```bash
BODY='{"title":"Checkout error rate increased","severity":"critical","service":"checkout","environment":"production","labels":{"region":"eu-west"}}'
TIMESTAMP=$(date +%s)
SIGNATURE=$(printf '%s.%s' "$TIMESTAMP" "$BODY" | openssl dgst -sha256 -hmac "$SOURCE_SECRET" -binary | xxd -p -c 256)

curl -i \
  -H "X-StormRelay-Timestamp: $TIMESTAMP" \
  -H "X-StormRelay-Signature: sha256=$SIGNATURE" \
  -H 'Content-Type: application/json' \
  --data "$BODY" \
  "http://localhost:8080/api/v1/webhooks/$SOURCE_ID"
```

List incidents:

```bash
curl -fsS \
  -H 'Authorization: Bearer local-development-only-change-me' \
  http://localhost:8080/api/v1/incidents | jq
```

## Configuration

All server and worker settings use the `STORMRELAY_` prefix, except the standard OpenTelemetry endpoint variables. See `.env.example`, `docs/operations.md`, `docs/observability.md`, and `docs/failure-testing.md` for the complete development and operations configuration.

Important security and reliability settings include:

- `STORMRELAY_MASTER_KEY`: base64-encoded 32-byte key used for encrypted integration secrets.
- `STORMRELAY_BOOTSTRAP_API_KEY`: initial tenant-admin key; replace it outside local development.
- `STORMRELAY_EVENT_MAX_DELIVERIES`: attempts before an event is moved to DLQ, default `5`.
- `STORMRELAY_EVENT_RETRY_BASE_DELAY`: initial event retry delay, default `1s`.
- `STORMRELAY_EVENT_RETRY_MAX_DELAY`: capped event retry delay, default `30s`.
- `STORMRELAY_RUNBOOK_HTTP_ALLOWED_HOSTS`: exact hosts available to runbook HTTP steps.
- `STORMRELAY_PLUGIN_ALLOWED_HOSTS`: exact hosts available to process plugins.

Optional tracing settings include:

- `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`: OTLP/gRPC collector URL; leave empty to disable export.
- `STORMRELAY_OTEL_TRACE_SAMPLE_RATIO`: parent-based root sampling ratio from `0` to `1`, default `0.10`.
- `STORMRELAY_OTEL_EXPORT_TIMEOUT`: bounded export timeout, default `10s`.

OIDC providers are registered through the tenant-admin API rather than environment variables. Registration performs guarded discovery and stores the exact issuer, API audience, JWKS URI, and allowed asymmetric signing algorithms. See `docs/identity.md`.

## Command-line client

```bash
go build -o stormrelay ./cmd/stormrelay-cli
./stormrelay login --url http://localhost:8080 --api-key local-development-only-change-me
./stormrelay server version
./stormrelay doctor
./stormrelay incidents list
./stormrelay policies validate examples/demo/policy.yaml
./stormrelay policies apply examples/demo/policy.yaml
./stormrelay audit export --output audit.jsonl
./stormrelay runbooks apply examples/runbooks/approval-demo.yaml
./stormrelay runbooks run approval-demo
./stormrelay approvals list
./stormrelay plugins register --key echo --endpoint http://echo-plugin:8090
./stormrelay plugins test echo --action echo --input '{"hello":"world"}'
```

The CLI stores its configuration with mode `0600` in the operating system user configuration directory.

## SDKs

Supported Go and Python API clients live under `sdk/`. Both use bearer authentication, bounded response parsing, and typed API errors. The Python client has no external runtime dependencies. OIDC access tokens may be supplied through the same bearer credential surface.

Supported Go and Python process-plugin server SDKs implement `stormrelay.plugin/v1`, including strict request validation, deadlines, bounded responses, safe errors, and idempotency-key echo. See `docs/plugin-sdk.md` and `plugins/COMPATIBILITY.md`.

## Security boundaries

- Arbitrary shell execution is not supported.
- Runbook and plugin outbound requests use exact host allowlists, DNS pinning, redirect rejection, disabled proxies, payload bounds, and deadlines.
- OIDC trust endpoints additionally require public IP destinations and reject private/loopback/link-local/metadata/CGNAT ranges.
- OIDC email claims are not used for account linking; provider subjects require explicit tenant-admin mappings.
- The server never logs service-account credentials, OIDC tokens, webhook secrets, or raw authorization headers.
- Protected API routes are fail-closed: new route families require an explicit permission mapping.
- Trace spans exclude authorization headers, credentials, raw request/event payloads, notification bodies, and unbounded labels.

See `SECURITY.md`, `docs/threat-model.md`, `docs/identity.md`, `docs/observability.md`, `docs/failure-testing.md`, and `docs/alert-runbooks.md` for details.

## Development

```bash
make test
make test-race
make build
make smoke
make backup-restore-drill
make failure
```

Integration and failure tests require Docker, PostgreSQL, and NATS. GitHub Actions runs dependency-lock verification, formatting, vet, the race detector, binary builds, PostgreSQL/NATS integration, Compose E2E, SDK tests, Identity Smoke, Plugin Conformance, Observability Config validation, the PostgreSQL backup/restore drill, dedicated failure injection, and CodeQL.

## Project status and releases

StormRelay has not claimed a `v0.1.0` release yet. The repository tracks completed and remaining work in `ROADMAP.md` and records unreleased changes in `CHANGELOG.md`.
