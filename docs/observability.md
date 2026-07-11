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

`OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` takes precedence over `OTEL_EXPORTER_OTLP_ENDPOINT`. StormRelay currently exports traces with OTLP over gRPC. The endpoint must be an absolute `http` or `https` URL containing only a host and optional port. Userinfo, paths, query strings, and fragments are rejected.

For an in-cluster collector using plaintext gRPC, use an `http://` endpoint. Use `https://` when the collector terminates TLS.

## Sampling

`STORMRELAY_OTEL_TRACE_SAMPLE_RATIO` accepts a number from `0` to `1` and defaults to `0.10`.

StormRelay uses parent-based ratio sampling:

- a valid sampled parent remains sampled;
- a valid unsampled parent remains unsampled;
- a new root trace is sampled according to the configured ratio.

A value of `0` disables recording for new root traces while preserving propagation. A value of `1` records every new trace and is intended mainly for development or controlled diagnostics.

## Trace continuity

The HTTP server extracts W3C `traceparent` and `baggage` headers. If no valid parent exists, it creates a new server span. The active trace context is returned in the response.

Before JetStream publication, StormRelay creates a producer span and stores that span's W3C `traceparent` in the normalized event envelope. The worker extracts it after durable delivery and creates a consumer span. Only `traceparent` crosses the queue boundary; baggage is deliberately not persisted in the event.

The connected event path contains these operation boundaries:

1. HTTP server ingress;
2. JetStream producer publication;
3. JetStream consumer processing;
4. PostgreSQL event transaction;
5. deduplication and duplicate audit handling;
6. incident correlation and initial transition;
7. policy evaluation;
8. notification enqueueing;
9. audit append and transaction commit;
10. JetStream acknowledgement;
11. notification claim, provider delivery, and durable completion;
12. DLQ publication after poison-message exhaustion.

Structured logs prefer the active OpenTelemetry trace ID and also include the StormRelay request ID where one exists.

## Export behavior

The exporter uses bounded batching:

- maximum queue size: 2048 spans;
- maximum export batch: 512 spans;
- batch delay: 2 seconds;
- maximum OTLP request size: 4 MiB;
- export timeout: `STORMRELAY_OTEL_EXPORT_TIMEOUT`, default 10 seconds.

Server and worker shutdown attempt to flush the provider with a bounded shutdown context. Collector outages are reported through structured logs and do not change incident-processing semantics.

## Data policy

Trace attributes contain bounded operational metadata only. User-controlled event ID, type, and source values are represented by fixed-length SHA-256 digests rather than exported in clear text. Other attributes are limited to route patterns, response status, internal destination names, domain-state enums, booleans, and bounded counters.

StormRelay does not attach these values to spans:

- authorization headers or bearer tokens;
- webhook signatures, source credentials, plugin credentials, or API keys;
- raw request or event payloads;
- SQL statements or query parameters;
- tenant identifiers;
- source, plugin, or notification URLs;
- email addresses or Telegram chat identifiers;
- notification contents;
- arbitrary URL query strings;
- unbounded user-controlled labels;
- OpenTelemetry baggage persisted across JetStream.

This boundary must be preserved when adding future instrumentation. Tests assert that secret-looking URLs and token fragments do not appear in event span attributes.

## Existing metrics

The existing Prometheus endpoint remains unchanged. Tracing does not replace metrics or readiness checks.

Important metric families include event ingress/rejection/deduplication, event-processing latency, runbook duration/failures, plugin and notification failures, open incidents, PostgreSQL pool usage, and JetStream consumer lag.

## Production collector guidance

Keep the collector outside the StormRelay process boundary. Configure bounded queues, retry-on-failure, memory limiting, and backend authentication in the collector rather than embedding backend credentials in StormRelay. Use network policy so server and worker can reach only the collector OTLP endpoint, not the trace backend directly.

Trace retention belongs to the selected backend. Match retention and sampling to the incident/audit retention policy, but do not treat traces as the authoritative audit record. Audit entries remain the durable security and compliance record.

## Troubleshooting

If spans are missing:

1. Confirm both server and worker received the OTLP endpoint.
2. Confirm the collector listens for OTLP/gRPC on the configured port.
3. Check `STORMRELAY_OTEL_TRACE_SAMPLE_RATIO`; `0` records no new root traces.
4. Check structured logs for `opentelemetry error` or `flush traces failed`.
5. Confirm firewalls and service policies allow the server and worker to reach the collector.
6. Avoid placing an HTTP `/v1/traces` path in the endpoint; that path belongs to OTLP/HTTP, not the gRPC exporter used here.
