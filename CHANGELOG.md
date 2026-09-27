# Changelog

All notable changes will be documented here. The format follows Keep a Changelog and releases use Semantic Versioning.

## [Unreleased]

### Added

- On-demand, concurrency-safe tenant-private manual event sources, allowing authenticated manual incidents in non-default tenants without sharing the bootstrap source.

### Security

- Reserve the `manual-api` source name and reject webhook and source-test access to internal manual sources regardless of the unauthenticated webhook flag.


- Repository foundation and production-oriented architecture documentation.
- Generic JSON and structured CloudEvents ingestion with HMAC/bearer authentication.
- JetStream at-least-once transport and PostgreSQL transactional processing.
- Raw payload preservation, deduplication records, correlation, incident state machine, policy DSL, notification outbox, acknowledgement, audit export, API, CLI, Compose environment, and tests.
- Durable runbook execution with immutable snapshots, leased steps, persisted wait and approval states, retries, cancellation, dry-run, explicit rollback, and crash recovery.
- SSRF-guarded HTTP steps and a versioned out-of-process plugin protocol with manifest discovery, bounded responses, deadlines, and idempotency-key verification.
- Tenant-scoped service accounts with one-time 256-bit API keys, revocation, expiry, optimistic updates, and stable audit JSONL.
- Fail-closed backend RBAC with explicit route permissions and authorization-denial audit.
- Supported Go and Python API clients with typed errors, bounded responses, and dedicated SDK tests.
- Guarded OIDC federation with exact issuer/audience trust, public-only discovery and JWKS access, signed-token verification, explicit subject mappings, and provider/identity disable controls.
- Supported Go and Python process-plugin server SDKs, a production-client conformance runner, cross-language Docker compatibility testing, and protocol compatibility guidance.
- Optional OpenTelemetry tracing with OTLP/gRPC export, W3C HTTP-to-event-to-worker propagation, parent-based sampling, bounded batching, graceful shutdown flush, and trace-correlated structured logs.
- Real HTTP propagation, JetStream consumer propagation, configuration-boundary, and in-process OTLP receiver tests.
- Persisted `traceparent` continuity across notification outbox and durable runbook execution, process-plugin/provider spans, and a bounded Collector/Tempo/Grafana demo with live trace-safety smoke tests.
- Version-controlled Prometheus alert rules for availability, event-pipeline stalls, JetStream backlog, PostgreSQL pool saturation, incident load, ingress rejection, runbook, plugin, and notification failures.
- Automatically provisioned Grafana datasource and StormRelay operations dashboard.
- Alert-specific operator runbooks with diagnosis, safe mitigation, and closure criteria.
- Dedicated `promtool`, Compose-model, dashboard-JSON, contract, and live provisioning checks.
- Automated PostgreSQL backup/restore drill with deterministic data fingerprints, raw-payload SHA-256 verification, foreign-key and append-only audit checks, restored encrypted credential validation, and JetStream redelivery deduplication coverage.
- Reusable guarded PostgreSQL backup/restore helpers with atomic archive publication, SHA-256 verification, explicit confirmation, no-overwrite behavior, non-empty-target refusal, and a dedicated helper contract test.
- Dedicated failure-injection workflow for PostgreSQL/NATS outages, concurrent duplicates, poison messages and DLQ, runbook lease recovery, process restart, plugin timeout/malformed responses, and notification-provider failures.
- Configurable event delivery limit and capped exponential backoff with deterministic jitter.
- Explicit schema v6-to-v7 upgrade drill preserving encrypted source credentials, service-account authentication, audit records, and migration history while validating new OIDC constraints and migration idempotency.
- Reproducible Go, PostgreSQL, and k6 benchmark harness with versioned correctness/full profiles, separate HTTP acceptance and event-to-incident percentiles, environment capture, schema-validated result artifacts, and explicit no-claims guidance for hosted CI.

### Fixed

- Enforce tenant ownership in SQL before retrieving or decrypting HMAC source credentials for authenticated source tests; preserve unscoped source-ID lookup exclusively for inbound webhooks and cover cross-tenant access using deliberately mismatched encryption keys.


- Make acknowledgement links GET/HEAD read-only and require an explicit POST from a confirmation page; add strict no-referrer, no-store, CSP and anti-framing response headers to mitigate accidental acknowledgement by link previews and scanners.

- Enforce authenticated tenant scoping for incident reads and transitions, source management, policy, notification-channel and audit APIs; reject cross-tenant source tests and prevent non-default tenants from publishing through the bootstrap-only manual source.

- Bind all development Docker Compose published ports to the host loopback interface instead of every network interface; add a Compose YAML regression test to prevent unsafe exposure of demo credentials and internal observability endpoints.

- Fail closed on malformed event, payload, replay, deduplication, correlation, concurrency, migration, and unauthenticated-source environment settings; reject non-positive windows and payloads above 64 MiB rather than silently using defaults.

- Release HMAC replay reservations when normalization or JetStream durable acceptance fails, allowing an identical signed request to be retried after a `503` response while retaining replay protection after `202 Accepted`.
- Canonicalize accepted HMAC replay identities and fail closed at replay-cache capacity without evicting active reservations.

No v0.1.0 release has been claimed yet.
