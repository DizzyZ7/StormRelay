# Roadmap

## Milestone 0 — repository foundation

Implemented: architecture, threat model, ADRs, community files, CI, Compose development environment, OpenAPI contract, and developer commands.

## Milestone 1 — working vertical slice

Implemented: generic and structured CloudEvents webhook ingestion, payload limits, HMAC/bearer authentication, replay checks, rate limiting, JetStream publish acknowledgement, raw and normalized storage, concurrent deduplication, correlation, incident state machine, explainable policies, mock/Telegram notification outbox, acknowledgement, audit export, CLI, health/readiness, metrics, integration tests, and DLQ handling.

## Milestone 2 — runbook engine

Not implemented. Durable leases, HTTP/wait/approval/plugin steps, recovery, retries, cancellation, dry-run, and explicit rollback are tracked beginning with issue #5.

## Milestone 3 — developer platform and identity

Not implemented. Go/Python SDKs, process plugin runtime, version compatibility, OIDC, tenant-aware RBAC, and service accounts.

## Milestone 4 — production operations

Not implemented. OpenTelemetry SDK/exporters, dashboards, complete failure injection, benchmark harness, backup/restore drill, and upgrade tests.

## Milestone 5 — Kubernetes and release

Not implemented. Helm, kind smoke tests, signed multi-architecture images, SBOM, provenance, attestations, release binaries, and v0.1.0 release automation.
