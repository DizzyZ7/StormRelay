# Observability

StormRelay exposes Prometheus metrics at `/metrics`, writes structured JSON logs to stdout, and exports distributed traces over OTLP/gRPC when configured.

Tracing is optional. Without an OTLP endpoint, StormRelay uses a no-export provider: request and trace correlation remain valid, but the process opens no exporter connection.

## Enable OTLP traces

Set the same collector endpoint on server and worker:

```bash
OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://otel-collector:4317
STORMRELAY_OTEL_TRACE_SAMPLE_RATIO=0.10
STORMRELAY_OTEL_EXPORT_TIMEOUT=10s
```

`OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` takes precedence over `OTEL_EXPORTER_OTLP_ENDPOINT`. The endpoint must be an absolute `http` or `https` URL containing only a host and optional port. Userinfo, paths, query strings, and fragments are rejected.

Use `http://` for plaintext gRPC inside a trusted cluster network. Use `https://` when the collector terminates TLS. Server and worker should reach the collector only; they do not need direct network access to the trace backend.

## Demo trace stack

Docker Compose includes a complete local pipeline:

- OpenTelemetry Collector Contrib on OTLP/gRPC port `4317` and health port `13133`;
- Grafana Tempo on `http://localhost:3200`;
- a provisioned `StormRelay Tempo` datasource in Grafana;
- server and worker configured to export to the collector;
- sampling set to `1.0` only for the local demo and smoke contract.

Start it with:

```bash
make demo-up
```

Open Grafana at `http://localhost:3000`, then select **Explore → StormRelay Tempo**. The development credentials are `admin` / `admin`; replace them before exposing Grafana outside a trusted development network.

The repository pins Collector and Tempo image versions. Upgrades must update the Compose model, static configuration tests, and the live Compose smoke test together.

## Sampling

`STORMRELAY_OTEL_TRACE_SAMPLE_RATIO` accepts a finite number from `0` to `1` and defaults to `0.10`.

StormRelay uses parent-based ratio sampling:

- a sampled parent remains sampled;
- an unsampled parent remains unsampled;
- a new root is sampled according to the configured ratio.

A value of `0` disables recording for new roots while preserving propagation. A value of `1` records every new trace and is intended for development, controlled diagnostics, or a carefully sized environment. Do not copy the Compose `1.0` setting into production without a storage and cost review.

## Connected operation boundaries

The HTTP server accepts W3C `traceparent` and `baggage`. The response contains the active `traceparent` so an operator can query the exact trace. Only W3C `traceparent` is persisted across asynchronous boundaries; baggage is deliberately excluded.

The event path contains these spans in one connected trace:

1. HTTP server ingress;
2. `stormrelay.nats.publish` after JetStream publish acknowledgement;
3. `stormrelay.event.process` on durable consumer delivery;
4. `stormrelay.event.transaction`;
5. `stormrelay.event.persist_raw`;
6. `stormrelay.event.deduplicate`;
7. `stormrelay.incident.correlate`;
8. `stormrelay.policy.evaluate`;
9. `stormrelay.notification.enqueue`;
10. `stormrelay.audit.append`;
11. `stormrelay.db.commit`;
12. `stormrelay.nats.ack`;
13. `stormrelay.notification.deliver`;
14. optional `stormrelay.notification.provider_request`;
15. `stormrelay.notification.complete`.

Poison-message exhaustion creates `stormrelay.nats.publish_dlq` before acknowledging the original message.

Incident API and acknowledgement-link transitions use `stormrelay.incident.transition`, with state enums and transition method only.

Runbook execution persists the originating `traceparent` in the immutable execution input snapshot. A worker restart therefore does not break trace continuity. Each claimed attempt creates `stormrelay.runbook.step`; HTTP and process-plugin actions create `stormrelay.runbook.http` and `stormrelay.plugin.call` children. Plugin discovery uses `stormrelay.plugin.discover`.

Notification outbox records persist `traceparent` inside the existing internal JSON payload. Claiming a delivery extracts and removes it before the payload reaches Telegram, email, webhook, or mock adapters. The trace context is not part of notification content.

Structured logs prefer the active OpenTelemetry trace ID and also include the StormRelay request ID where one exists.

## PostgreSQL and messaging semantics

The trace reflects the real reliability boundary:

- HTTP `202 Accepted` follows a JetStream publish acknowledgement;
- the worker acknowledges a message only after the PostgreSQL event transaction commits;
- a database failure is followed by negative acknowledgement and redelivery;
- notification delivery and durable completion are separate spans because a provider outcome can be ambiguous;
- non-idempotent runbook actions can become `ambiguous` instead of being replayed automatically.

Spans do not claim exactly-once processing.

## Export behavior

The SDK uses bounded batching:

- maximum SDK queue: 2048 spans;
- maximum SDK export batch: 512 spans;
- SDK batch delay: 2 seconds;
- maximum OTLP request size: 4 MiB;
- exporter timeout: `STORMRELAY_OTEL_EXPORT_TIMEOUT`, default 10 seconds.

The demo Collector adds:

- a 128 MiB memory limiter with a 32 MiB spike limit;
- batches capped at 1024 spans;
- a bounded 2048-item sending queue;
- exponential exporter retry capped at 60 seconds elapsed time.

Server and worker shutdown attempt to flush the provider with a bounded context. Collector outages are logged and do not change event-processing semantics.

## Trace data policy

Trace attributes contain bounded, low-cardinality operational metadata only. User-controlled event ID, type, and source are represented by fixed-length SHA-256 digests rather than clear text.

Allowed examples include:

- route patterns and HTTP status codes;
- fixed internal destination names;
- operation and domain-state enums;
- channel or step types;
- attempt, match, and enqueue counts;
- booleans such as duplicate, suppressed, rollback, or dry-run.

StormRelay does not attach these values to spans or span events:

- authorization headers, cookies, bearer tokens, or API keys;
- webhook signatures or source credentials;
- plugin credentials, secret URLs, or full endpoints;
- raw request, event, execution, plugin, or notification payloads;
- SQL statements or query parameters;
- tenant, source, incident, execution, step, delivery, chat, or email identifiers;
- Telegram chat IDs, email addresses, notification contents, or acknowledgement URLs;
- arbitrary labels, baggage, query strings, or provider error bodies.

External-provider errors are represented with a fixed `operation failed` status and a bounded outcome. Provider-controlled error text is not recorded as an exception event.

Unit tests verify bounded attributes and safe error behavior. The integration trace contract injects a secret marker into a real event and rejects any exported attribute or event containing it. Compose smoke retrieves live traces from Tempo and applies the same marker check to event and plugin traces.

Traces are diagnostic data, not the authoritative audit record. Append-only PostgreSQL audit entries remain the security and compliance source of truth.

## Retention

The demo Tempo backend retains local trace blocks for 24 hours and uses a Docker volume. It is not a production storage design.

Production retention belongs to the selected backend. Choose sampling and retention from measured event volume, incident investigation needs, privacy requirements, and storage cost. Protect trace access as operationally sensitive even though StormRelay excludes payloads and credentials.

## Existing metrics

Tracing does not replace metrics or readiness checks. Important Prometheus families cover ingress, rejection, duplicates, event-processing latency, open incidents, acknowledgement time, runbook duration/failures, plugin and notification failures, JetStream lag, and PostgreSQL pool utilization.

Metric labels follow a separate low-cardinality budget. Do not add tenant IDs, incident IDs, URLs, arbitrary labels, or source names as labels.

## Verification

Static configuration tests verify the Collector, Tempo, Grafana datasource, pinned images, bounded queues, memory limiter, retry policy, and retention.

The PostgreSQL/NATS integration test proves exact parent-child relationships across producer, consumer, transaction stages, acknowledgement, and delayed notification delivery/completion.

The Compose smoke test proves that:

- Collector and Tempo are healthy;
- Grafana provisions the Tempo datasource;
- an ingested signed webhook appears in Tempo with the complete asynchronous event/notification path;
- a process-plugin call appears in the originating API trace;
- persisted wait and approval runbook steps remain connected to the run request after asynchronous execution;
- injected secret markers do not appear in exported trace JSON.

## Troubleshooting

When spans are missing:

1. Confirm server and worker use the same OTLP endpoint.
2. Confirm the collector listens for OTLP/gRPC on `4317` and its health endpoint is ready.
3. Confirm Tempo is ready and the Collector exporter can resolve `tempo:4317`.
4. Check `STORMRELAY_OTEL_TRACE_SAMPLE_RATIO`; `0` records no new roots.
5. Check structured logs for `opentelemetry error`, exporter retry, or flush failures.
6. Confirm network policy permits server/worker → collector and collector → backend.
7. Do not append `/v1/traces`; that path belongs to OTLP/HTTP, while StormRelay exports OTLP/gRPC.
8. Use the response `traceparent` trace ID to query Tempo directly at `/api/traces/<trace-id>` during diagnosis.
