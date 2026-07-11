# Roadmap

## Milestone 0 — repository foundation

Implemented: architecture, threat model, ADRs, community files, CI, Compose development environment, OpenAPI contract, and developer commands.

## Milestone 1 — working vertical slice

Implemented: generic and structured CloudEvents webhook ingestion, payload limits, HMAC/bearer authentication, replay checks, rate limiting, JetStream publish acknowledgement, raw and normalized storage, concurrent deduplication, correlation, incident state machine, explainable policies, mock/Telegram notification outbox, acknowledgement, audit export, CLI, health/readiness, metrics, integration tests, and DLQ handling.

## Milestone 2 — durable runbook engine

Implemented: immutable versioned definitions, PostgreSQL execution snapshots and leases, HTTP/wait/approval/process-plugin steps, bounded retries and timeouts, persisted timers, crash recovery, pause/resume/cancel, dry-run, explicit operator retry for ambiguous outcomes, and reverse-order rollback.

## Milestone 3 — developer platform and identity

Implemented:

- supported Go and Python API clients;
- tenant-scoped service accounts and one-time hashed API keys;
- backend-enforced, fail-closed RBAC and authorization audit;
- guarded OIDC issuer/JWKS trust, signed bearer-token verification, and explicit provider-subject mappings;
- supported Go and Python process-plugin server SDKs;
- production-client conformance runner, cross-language Docker compatibility gate, SDK example, and protocol compatibility policy.

Interactive browser-session flows remain optional future product work. StormRelay's API does not require browser sessions; direct OIDC bearer tokens are supported.

## Milestone 4 — production operations

In progress.

Implemented foundation:

- optional OpenTelemetry trace provider and OTLP/gRPC exporter;
- W3C HTTP-to-event-to-worker trace propagation;
- parent-based ratio sampling, bounded batching, and graceful shutdown flush;
- request/trace-correlated structured logs;
- real in-process OTLP receiver and propagation tests;
- existing Prometheus metrics retained unchanged;
- provisioned Prometheus alert rules for availability, pipeline, capacity, and integration failures;
- provisioned Grafana operations dashboard and datasource;
- alert-specific diagnosis, mitigation, and closure runbooks;
- static `promtool`/Compose/dashboard validation and live provisioning smoke tests;
- automated PostgreSQL backup/restore drill with data fingerprints, raw-payload hash verification, audit-trigger validation, restored credential checks, and JetStream redelivery deduplication coverage;
- deterministic failure injection for PostgreSQL and NATS outages, concurrent duplicate delivery, poison messages and DLQ, runbook lease recovery, control-plane restart, plugin failures, and notification-provider failures;
- configurable capped event redelivery with deterministic jitter and fail-closed HMAC replay reservations around the JetStream acknowledgement boundary;
- explicit PostgreSQL schema v6-to-v7 upgrade drill with legacy encrypted credentials, service-account authentication, OIDC constraint verification, migration-history checks, and future-version fixture enforcement.

Remaining: broader application spans and benchmark harness.

## Milestone 5 — Kubernetes and release

Not implemented. Helm, kind smoke tests, signed multi-architecture images, SBOM, provenance, attestations, release binaries, and v0.1.0 release automation.
