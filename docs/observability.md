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

The HTTP server extracts W3C `traceparent` and `baggage` headers. If no valid parent exists, it creates a new server span. The active trace context is returned in the response and persisted in the normalized event.

The worker extracts that event trace context after JetStream delivery and creates a consumer span. This connects the original webhook/API request to event processing and incident creation even though the work crosses an asynchronous queue.

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

Trace attributes contain operational metadata only, such as route pattern, response status, event ID, event type, and event source.

StormRelay does not attach these values to spans:

- authorization headers or bearer tokens;
- webhook, plugin, or service-account credentials;
- raw request or event payloads;
- notification contents;
- arbitrary URL query strings;
- unbounded user-controlled labels.

This boundary must be preserved when adding future instrumentation.

## Existing metrics

The existing Prometheus endpoint remains unchanged. Tracing does not replace metrics or readiness checks.

Important metric families include event ingress/rejection/deduplication, event-processing latency, runbook duration/failures, plugin and notification failures, open incidents, PostgreSQL pool usage, and JetStream consumer lag.

## Troubleshooting

If spans are missing:

1. Confirm both server and worker received the OTLP endpoint.
2. Confirm the collector listens for OTLP/gRPC on the configured port.
3. Check `STORMRELAY_OTEL_TRACE_SAMPLE_RATIO`; `0` records no new root traces.
4. Check structured logs for `opentelemetry error` or `flush traces failed`.
5. Confirm firewalls and service policies allow the server and worker to reach the collector.
6. Avoid placing an HTTP `/v1/traces` path in the endpoint; that path belongs to OTLP/HTTP, not the gRPC exporter used here.
