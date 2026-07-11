# Observability

StormRelay exposes Prometheus metrics at `/metrics`, writes structured JSON logs to stdout, and can export distributed traces over OTLP/gRPC.

Tracing is optional. When no OTLP endpoint is configured, StormRelay keeps a local no-export trace provider so request and trace correlation remain valid without opening network connections.

## Enable OTLP traces

Set the same collector endpoint on the server and worker:

```bash
OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://otel-collector:4317
STORMRELAY_OTEL_TRACE_SAMPLE_RATIO=0.10
STORMRELAY_OTEL_EXPORT_TIMEOUT=10s
```

`OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` takes precedence over `OTEL_EXPORTER_OTLP_ENDPOINT`. StormRelay exports traces with OTLP over gRPC. The endpoint must be an absolute `http` or `https` URL containing only a host and optional port. Userinfo, paths, query strings, and fragments are rejected.

For an in-cluster collector using plaintext gRPC, use an `http://` endpoint. Use `https://` when the collector terminates TLS.

## Demo trace stack

Docker Compose includes a complete local trace pipeline:

- OpenTelemetry Collector Contrib receives OTLP/gRPC on port `4317` and exposes health on `13133`;
- Grafana Tempo is available at `http://localhost:3200`;
- Grafana provisions a `StormRelay Tempo` datasource;
- server and worker export to the collector with sampling set to `1.0` for the demo only.

Start the environment with:

```bash
make demo-up
```

Open Grafana at `http://localhost:3000`, then select **Explore → StormRelay Tempo**. The development credentials are `admin` / `admin` and must be replaced before exposing Grafana outside a trusted network.

Collector and Tempo images are pinned in the Compose file. Upgrades must update the static configuration contract and live Compose smoke test together.

## Sampling

`STORMRELAY_OTEL_TRACE_SAMPLE_RATIO` accepts a number from `0` to `1` and defaults to `0.10`.

StormRelay uses parent-based ratio sampling:

- a valid sampled parent remains sampled;
- a valid unsampled parent remains unsampled;
- a new root trace is sampled according to the configured ratio.

A value of `0` disables recording for new root traces while preserving propagation. A value of `1` records every new trace and is intended for development or controlled diagnostics. Do not copy the Compose value into production without sizing storage and collector capacity.

## Trace continuity

The HTTP server extracts W3C `traceparent` and `baggage` headers. If no valid parent exists, it creates a new server span. The active trace context is returned in the response.

Before JetStream publication, StormRelay creates `stormrelay.event.publish` and stores that producer span's W3C `traceparent` in the normalized event envelope. The worker extracts it after durable delivery and creates `stormrelay.event.process`. Only `traceparent` crosses durable boundaries; baggage is deliberately not persisted.

The connected event path contains these operation boundaries:

1. HTTP server ingress;
2. `stormrelay.event.publish`;
3. `stormrelay.event.process`;
4. `stormrelay.db.process_event`;
5. `stormrelay.event.deduplicate`;
6. `stormrelay.incident.correlate`;
7. `stormrelay.policy.evaluate`;
8. `stormrelay.notification.enqueue`;
9. `stormrelay.audit.append` and `stormrelay.db.commit_event`;
10. `stormrelay.event.ack`;
11. `stormrelay.notification.deliver`;
12. optional `stormrelay.notification.provider_request`;
13. `stormrelay.db.complete_delivery`;
14. DLQ publication after poison-message exhaustion.

Notification outbox records persist `traceparent` inside the existing internal JSON payload. Claiming a delivery extracts and removes it before the payload reaches Telegram, email, webhook, or mock adapters. The trace context is not part of notification content.

Runbook execution persists the originating `traceparent` in the immutable execution input snapshot. A worker restart therefore does not break continuity. Each claimed attempt creates `stormrelay.runbook.step`; HTTP actions create `stormrelay.runbook.http`, and process-plugin actions create `stormrelay.plugin.call`. Plugin discovery uses `stormrelay.plugin.discover`.

Plugin and HTTP action calls propagate W3C context in outbound headers. Process plugins also receive the same `traceparent` in the versioned action contract.

Structured logs prefer the active OpenTelemetry trace ID and also include the StormRelay request ID where one exists.

## Reliability semantics represented by traces

The trace follows the real acknowledgement boundaries:

- HTTP `202 Accepted` follows JetStream publish acknowledgement;
- the worker acknowledges a message only after the PostgreSQL event transaction commits;
- database failures lead to negative acknowledgement and redelivery;
- provider delivery and durable outbox completion are distinct operations;
- non-idempotent runbook actions can become `ambiguous` instead of being replayed automatically.

Spans do not claim exactly-once processing.

## Export behavior

The SDK uses bounded batching:

- maximum queue size: 2048 spans;
- maximum export batch: 512 spans;
- batch delay: 2 seconds;
- maximum OTLP request size: 4 MiB;
- export timeout: `STORMRELAY_OTEL_EXPORT_TIMEOUT`, default 10 seconds.

The demo Collector adds a 128 MiB memory limiter, batches capped at 1024 spans, a bounded 2048-item sending queue, and retry-on-failure capped at 60 seconds elapsed time.

Server and worker shutdown attempt to flush the provider with a bounded shutdown context. Collector outages are reported through structured logs and do not change incident-processing semantics.

## Data policy

Trace attributes contain bounded operational metadata only. User-controlled event ID, type, and source values are represented by fixed-length SHA-256 digests rather than exported in clear text. Other attributes are limited to route patterns, response status, internal destination names, domain-state enums, booleans, and bounded counters.

StormRelay does not attach these values to spans or span events:

- authorization headers, cookies, bearer tokens, or API keys;
- webhook signatures, source credentials, plugin credentials, or secret URLs;
- raw request, event, execution, plugin, or notification payloads;
- SQL statements or query parameters;
- tenant, source, incident, execution, step, delivery, chat, or email identifiers;
- Telegram chat IDs, email addresses, notification contents, or acknowledgement URLs;
- arbitrary URL query strings, labels, or persisted baggage;
- provider-controlled error bodies or exception messages.

External-provider failures use a fixed error status without recording provider text as an exception event.

Unit tests verify persisted parent continuity and safe external errors. PostgreSQL/NATS integration verifies exact parent-child relationships through delayed notification delivery and completion. Compose smoke injects secret markers into event and plugin payloads and rejects any marker found in live Tempo trace JSON.

Traces are diagnostic data, not the authoritative audit record. Append-only PostgreSQL audit entries remain the security and compliance source of truth.

## Existing metrics

The Prometheus endpoint remains unchanged. Tracing does not replace metrics or readiness checks.

Important metric families include event ingress/rejection/deduplication, event-processing latency, runbook duration/failures, plugin and notification failures, open incidents, PostgreSQL pool usage, and JetStream consumer lag.

## Production collector guidance

Keep the collector outside the StormRelay process boundary. Configure bounded queues, retry-on-failure, memory limiting, backend authentication, and backend TLS in the collector rather than embedding backend credentials in StormRelay. Use network policy so server and worker can reach only the collector OTLP endpoint, not the trace backend directly.

The demo Tempo backend uses local storage and 24-hour retention. Production retention belongs to the selected backend and must be chosen from measured event volume, investigation needs, privacy requirements, and cost.

## Verification

Static tests verify Collector bounds, Tempo retention, pinned images, Grafana datasource provisioning, and Compose wiring.

The integration trace contract proves one trace across producer, consumer, PostgreSQL transaction stages, acknowledgement, delayed notification delivery, and durable completion.

The live Compose smoke test proves:

- Collector, Tempo, Prometheus, and Grafana are usable;
- Grafana provisions the Tempo datasource;
- a signed webhook appears in Tempo with the asynchronous event and notification path;
- a process-plugin call appears in the originating API trace;
- persisted wait and approval runbook steps remain connected to the run request;
- secret markers do not appear in exported event or plugin traces.

## Troubleshooting

If spans are missing:

1. Confirm both server and worker received the same OTLP endpoint.
2. Confirm the collector listens for OTLP/gRPC on `4317` and health on `13133`.
3. Confirm Tempo is ready and the collector can resolve `tempo:4317`.
4. Check `STORMRELAY_OTEL_TRACE_SAMPLE_RATIO`; `0` records no new root traces.
5. Check structured logs for `opentelemetry error`, exporter retry, or flush failures.
6. Confirm network policy permits server/worker → collector and collector → backend.
7. Avoid appending `/v1/traces`; that path belongs to OTLP/HTTP, not the gRPC exporter used here.
8. Use the trace ID from the response `traceparent` to query Tempo directly at `/api/traces/<trace-id>`.
