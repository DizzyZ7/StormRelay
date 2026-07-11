# StormRelay benchmark harness

The benchmark harness measures distinct parts of the event path without presenting a single misleading throughput number.

No benchmark result is committed to the README or release notes. A result is publishable only when the generated `stormrelay.benchmark-result/v1` document is attached with its commit SHA, profile, hardware, runtime versions, configuration, dataset, concurrency, warm-up, percentiles, and error rate.

## Measured paths

### Go microbenchmarks

- generic JSON normalization at 1 KiB and 8 KiB;
- structured CloudEvents normalization;
- event fingerprinting;
- policy YAML parsing;
- matched and unmatched policy evaluation.

These benchmarks do not access the network or database.

### PostgreSQL processing benchmarks

- `BenchmarkProcessEventNewIncident`: raw event, normalized event, new incident, transition, notification enqueueing, and audit in one transaction;
- `BenchmarkProcessEventCorrelated`: a new canonical event correlated into an existing incident;
- `BenchmarkProcessEventDuplicate`: raw delivery retention, duplicate counter, duplicate record, and duplicate audit.

Each benchmark creates a dedicated source. Generated IDs prevent state from one benchmark mode from changing another mode.

### k6 end-to-end benchmark

The k6 scenario creates real HMAC sources, sends signed generic webhook events, waits for incident visibility through the authenticated API, and records two separate latency distributions:

- `stormrelay_http_acceptance_ms`: request start to HTTP `202 Accepted`, meaning JetStream returned a publish acknowledgement;
- `stormrelay_event_to_incident_ms`: request start to the incident becoming visible through `/api/v1/incidents`.

The latter includes JetStream delivery, PostgreSQL processing, correlation, audit, and API visibility. It intentionally does not pretend that HTTP acceptance means incident persistence is complete.

The profile's duplicate ratio reuses upstream source event IDs with a fresh valid HMAC request. It exercises ingress and JetStream duplicate handling. The PostgreSQL duplicate transaction and audit path is measured independently by `BenchmarkProcessEventDuplicate` so transport-level suppression is not confused with database deduplication cost.

## Profiles

`profiles/correctness.json` is a small CI profile. It proves that the benchmark code, metrics, environment capture, and result schema work. It is not a capacity result and must not be quoted as one.

`profiles/full.json` is an explicit operator-run profile. It has a longer warm-up, longer measurement phase, more sources, and more virtual users. Run it on a dedicated, identified machine when producing a comparable result.

Both profiles define:

- event shape and payload size;
- tenant and source count;
- duplicate ratio;
- concurrency;
- warm-up and measurement duration;
- incident visibility timeout and poll interval;
- Go benchmark duration/count;
- PostgreSQL, NATS, JetStream, and k6 versions/configuration.

StormRelay v1 currently uses one tenant in the benchmark profile. Multi-tenant workload mixes must be introduced as a new profile version rather than silently changing this dataset.

## Run

Requirements: Go, Docker with Compose, Python 3, curl, and Git.

```bash
make benchmark-correctness
make benchmark-full
```

The runner uses an isolated Compose project and fresh volumes, then performs a warm-up before recording k6 custom metrics. Therefore the generated result declares `cache_state: warmed` and `cold_start: false`. Cold-start and empty-cache tests require a separate future profile; they must not be mixed with steady-state values.

Generated files are written to:

```text
.benchmark-results/<profile>/
  go-benchmark.txt
  k6.log
  k6-summary.json
  result.json
```

The directory is ignored by Git. Override it with `STORMRELAY_BENCHMARK_OUTPUT_DIR`.

## Result contract

`result.schema.json` is the versioned result schema. `capture_result.py` combines raw measurements and environment metadata. `validate_result.py` validates the generated document without downloading a runtime dependency.

Validate an existing result:

```bash
make benchmark-validate RESULT=.benchmark-results/full/result.json
```

A valid result includes:

- p50, p95, and p99 for HTTP acceptance and event-to-incident latency;
- error rate and measured sample counts;
- accepted, durable, and duplicate request counts;
- parsed Go benchmark records;
- commit SHA and dirty-worktree state;
- CPU model, logical CPUs, memory, OS, architecture, Go, Docker, Compose, k6, PostgreSQL, and NATS identifiers;
- complete profile and phase metadata.

Database and NATS connection strings in the report are fixed redacted targets. Secrets, source credentials, HMAC signatures, raw payloads, and API keys are never written to benchmark artifacts.

## Comparing results

Compare only results with the same schema version, profile, dependency versions, hardware class, and cache state. Hosted CI runners are suitable for correctness, not stable regression thresholds. Before adding a performance gate, collect repeated results on a dedicated runner, quantify variance, define an allowed regression budget, and document the baseline commit.
