# StormRelay

StormRelay is a self-hosted event-correlation and incident-response control plane for small product teams, SRE, NOC, DevOps, and security operations.

It accepts authenticated webhooks, preserves the original payload, normalizes events into a CloudEvents-compatible model, deduplicates concurrent deliveries, correlates events into incidents, evaluates explainable policies, notifies responders, and records an append-only audit trail.

> **Current status:** Milestones 0 and 1 are released on `main`; Milestone 2 is under review. The current branch adds durable versioned runbooks, persisted wait/approval state, retries, crash recovery, explicit rollback, and the process-based plugin protocol. OIDC, the web UI, full OpenTelemetry exporters, SDKs, Kubernetes packaging, and release automation remain later milestones.

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

Send an event. The signature is `HMAC-SHA256(secret, timestamp + "." + raw_body)`:

```bash
BODY='{"type":"latency.alert","subject":"Checkout database latency","severity":"critical","labels":{"service":"checkout","environment":"production","resource":"db-primary","alertname":"HighLatency"}}'
TS=$(date +%s)
SIG=$(printf '%s.%s' "$TS" "$BODY" | openssl dgst -sha256 -hmac "$SOURCE_SECRET" -hex | awk '{print $2}')

curl -fsS \
  -H "X-StormRelay-Timestamp: $TS" \
  -H "X-StormRelay-Signature: sha256=$SIG" \
  -H 'Idempotency-Key: demo-checkout-latency-1' \
  -H 'Content-Type: application/json' \
  --data-binary "$BODY" \
  "http://localhost:8080/api/v1/webhooks/$SOURCE_ID" | jq
```

Inspect and acknowledge the incident:

```bash
curl -fsS \
  -H 'Authorization: Bearer local-development-only-change-me' \
  http://localhost:8080/api/v1/incidents | jq

# Use the returned id and version.
curl -fsS \
  -H 'Authorization: Bearer local-development-only-change-me' \
  -H 'Content-Type: application/json' \
  -d '{"version":1,"reason":"Responder accepted ownership"}' \
  http://localhost:8080/api/v1/incidents/INCIDENT_ID/ack | jq
```

Prometheus is available at `http://localhost:9090`; Grafana is available at `http://localhost:3000` with local credentials `admin` / `admin`.

## Event lifecycle

```mermaid
flowchart LR
    S[Webhook source] -->|HMAC or bearer| G[Ingestion gateway]
    G -->|publish ack| J[(NATS JetStream)]
    J --> W[Bounded worker]
    W --> R[(Raw event)]
    W --> D{Dedupe constraint}
    D -->|duplicate| DD[(Duplicate record + audit)]
    D -->|canonical| C[Correlation lock]
    C --> I[(Incident)]
    I --> P[Policy evaluation]
    P --> O[(Notification outbox)]
    O --> N[Mock or Telegram]
    N --> A[Acknowledgement]
    A --> AU[(Append-only audit)]
```

The HTTP gateway returns `202 Accepted` only after JetStream confirms publication. The worker acknowledges the JetStream message only after the PostgreSQL transaction commits.

## Architecture at a glance

```mermaid
flowchart TB
    subgraph ControlPlane
      API[stormrelay-server]
      Worker[stormrelay-worker]
      CLI[stormrelay CLI]
    end
    API --> JS[(JetStream)]
    Worker --> JS
    API --> PG[(PostgreSQL)]
    Worker --> PG
    Worker --> Providers[Notification providers]
    CLI --> API
```

Important decisions:

1. PostgreSQL is the source of truth for events, incidents, policy versions, delivery state, and audit.
2. JetStream provides durable at-least-once transport and bounded backpressure.
3. Raw payloads are stored separately from normalized fields.
4. Secrets that must be recovered are encrypted with AES-256-GCM; bearer credentials are stored only as SHA-256 hashes.
5. Policy evaluation uses a versioned YAML DSL with `all`, `any`, `not`, `equals`, and `in`; arbitrary evaluation is prohibited.
6. External notification calls happen after the event transaction through a durable outbox.

Read [the architecture document](docs/architecture.md), [threat model](docs/threat-model.md), and [operations guide](docs/operations.md).

## Repository layout

```text
cmd/                    server, worker, and CLI entry points
internal/api/           HTTP API and middleware
internal/events/        CloudEvents-compatible model and normalization
internal/ingestion/     signatures, replay checks, and rate limits
internal/storage/       pgx repositories, transactions, and SQL migrations
internal/policies/      safe declarative policy DSL
internal/worker/        JetStream consumer and recovery behavior
internal/notifications/ mock and Telegram delivery adapters
api/openapi/            OpenAPI 3.1 contract
deploy/compose/         runnable local environment
docs/                   architecture, threat model, ADRs, and operations
examples/               demo policies and payloads
tests/integration/      PostgreSQL, concurrency, and JetStream tests
```

## CLI

```bash
go build -o stormrelay ./cmd/stormrelay-cli
./stormrelay login --url http://localhost:8080 --api-key local-development-only-change-me
./stormrelay doctor
./stormrelay sources list
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

## Policy example

```yaml
apiVersion: stormrelay.io/v1
kind: Policy
metadata:
  id: critical-production
  version: 1
spec:
  match:
    all:
      - field: severity
        equals: critical
      - field: environment
        in: [production]
  actions:
    notificationChannels: [local-mock]
    requireApproval: true
```

Every evaluation writes the policy ID, version, outcome, explanation, and the bounded normalized inputs that affected the decision to audit. Raw event payloads are never copied into policy audit metadata.

## Reliability boundaries

- Delivery is at least once from gateway to worker.
- Concurrent duplicates produce one canonical event plus explicit duplicate records.
- Correlation is serialized per correlation key with a PostgreSQL advisory transaction lock.
- Incident updates use version columns and validate the state machine in domain code.
- A worker crash before commit causes redelivery. A crash after commit but before ack causes a duplicate record, not a second canonical event.
- Notification rows use a unique dedupe key and delivery leases. An expired external-provider lease becomes `ambiguous` rather than being blindly repeated.
- After five poison-message deliveries, the worker publishes the payload to the dead-letter subject and acknowledges the original message.

## Development

```bash
make fmt
make vet
make test
make test-race
make build
```

Integration tests require PostgreSQL and NATS JetStream:

```bash
go test -tags=integration -count=1 ./tests/integration
```

See [DEVELOPMENT.md](DEVELOPMENT.md) and [CONTRIBUTING.md](CONTRIBUTING.md).

## Compatibility

| Surface | Current contract |
|---|---|
| HTTP API | `/api/v1`; additive changes preferred before v1.0 |
| Internal event schema | `1.0` |
| Policy DSL | `stormrelay.io/v1` |
| PostgreSQL | 16+; demo uses 17 |
| NATS | JetStream-capable 2.10+; demo uses 2.11 |
| Go | 1.26.x |

## Security

Do not report vulnerabilities in public issues. Follow [SECURITY.md](SECURITY.md). The current local authentication mode is explicitly a development bootstrap mode; OIDC and full tenant-aware RBAC enforcement are Milestone 3 work and are not claimed as implemented.

## License

Apache License 2.0. See [LICENSE](LICENSE).
