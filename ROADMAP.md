# Roadmap

## Milestone 0 — repository foundation

Implemented: architecture, threat model, ADRs, community files, CI, Compose development environment, OpenAPI contract, and developer commands.

## Milestone 1 — working vertical slice

Implemented: generic and structured CloudEvents webhook ingestion, payload limits, HMAC/bearer authentication, replay checks, rate limiting, JetStream publish acknowledgement, raw and normalized storage, concurrent deduplication, correlation, incident state machine, explainable policies, mock/Telegram notification outbox, acknowledgement, audit export, CLI, health/readiness, metrics, integration tests, and DLQ handling.

## Milestone 2 — durable runbook engine

Implemented in the Milestone 2 branch and subject to merge gates: immutable versioned definitions, PostgreSQL execution snapshots and leases, HTTP/wait/approval/process-plugin steps, bounded retries and timeouts, persisted timers, crash recovery, pause/resume/cancel, dry-run, explicit operator retry for ambiguous outcomes, and reverse-order rollback.

## Milestone 3 — developer platform and identity

Not implemented. Go/Python SDKs, OIDC, tenant-aware RBAC, service accounts, and broader plugin developer tooling remain separate work after Milestone 2 is merged.

## Milestone 4 — production operations

Not implemented. OpenTelemetry SDK/exporters, dashboards, complete failure injection, benchmark harness, backup/restore drill, and upgrade tests.

## Milestone 5 — Kubernetes and release

Not implemented. Helm, kind smoke tests, signed multi-architecture images, SBOM, provenance, attestations, release binaries, and v0.1.0 release automation.
