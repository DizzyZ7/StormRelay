# Changelog

All notable changes will be documented here. The format follows Keep a Changelog and releases use Semantic Versioning.

## [Unreleased]

### Added

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
- Version-controlled Prometheus alert rules for availability, event-pipeline stalls, JetStream backlog, PostgreSQL pool saturation, incident load, ingress rejection, runbook, plugin, and notification failures.
- Automatically provisioned Grafana datasource and StormRelay operations dashboard.
- Alert-specific operator runbooks with diagnosis, safe mitigation, and closure criteria.
- Dedicated `promtool`, Compose-model, dashboard-JSON, contract, and live provisioning checks.
- Automated PostgreSQL backup/restore drill with deterministic data fingerprints, raw-payload SHA-256 verification, foreign-key and append-only audit checks, restored encrypted credential validation, and JetStream redelivery deduplication coverage.
- Reusable guarded PostgreSQL backup/restore helpers with atomic archive publication, SHA-256 verification, explicit confirmation, no-overwrite behavior, non-empty-target refusal, and a dedicated helper contract test.
- Dedicated failure-injection workflow for PostgreSQL/NATS outages, concurrent duplicates, poison messages and DLQ, runbook lease recovery, process restart, plugin timeout/malformed responses, and notification-provider failures.
- Configurable event delivery limit and capped exponential backoff with deterministic jitter.
- Reproducible Go, PostgreSQL, and k6 benchmark harness with versioned correctness/full profiles, separate HTTP acceptance and event-to-incident percentiles, environment capture, schema-validated result artifacts, and explicit no-claims guidance for hosted CI.

### Fixed

- Release HMAC replay reservations when normalization or JetStream durable acceptance fails, allowing an identical signed request to be retried after a `503` response while retaining replay protection after `202 Accepted`.

No v0.1.0 release has been claimed yet.
