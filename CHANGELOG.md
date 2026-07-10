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
- Supported Go and Python clients with typed errors, bounded responses, and dedicated SDK tests.
- Guarded OIDC federation with exact issuer/audience trust, public-only discovery and JWKS access, signed-token verification, explicit subject mappings, and provider/identity disable controls.

No v0.1.0 release has been claimed yet.
