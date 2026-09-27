# Threat model

## Assets

- source HMAC and bearer credentials;
- API keys and future OIDC identities;
- original webhook payloads;
- normalized event and incident history;
- policy and runbook definitions plus immutable execution snapshots;
- acknowledgement tokens;
- notification-provider credentials;
- append-only audit records.

## Trust boundaries

1. Untrusted event producers to the ingestion gateway.
2. Gateway to JetStream.
3. Worker to PostgreSQL.
4. Worker to external notification providers.
5. Operator API clients to the control plane.
6. Out-of-process plugins and outbound HTTP actions to the core system.

## Primary threats and controls

### Forged or replayed webhooks

HMAC sources sign the timestamp and exact raw body. The gateway enforces a bounded clock window and an in-memory replay cache. JetStream message IDs and database deduplication protect durable effects after process restart. Bearer values are constant-time compared against stored hashes.

### Payload exhaustion and parser abuse

The gateway applies `http.MaxBytesReader` before reading. Ordinary control-plane JSON is capped at 1 MiB and requires exactly one valid document with known fields. Invalid or oversized caller-provided request IDs are replaced with generated UUIDs before logging, tracing, audit, or response reflection. JSON parsing has bounded input. Policy YAML has a separate limit, known-field decoding, and no arbitrary expression evaluation. Fuzz targets cover event and policy parsing.

### Notification configuration disclosure

The integrations-read role can list notification-channel metadata but must not receive stored configuration. Channel configuration is excluded from JSON serialization and is not loaded by the list query; historical rows containing unknown fields are also redacted. New mock and Telegram channels reject unsupported config keys so arbitrary credentials cannot be introduced through the supported HTTP API. Workers continue to load stored configuration internally for delivery; pre-existing config records still require database-level access control and a separate migration if per-channel secrets are supported in future.

### Resource creation and audit consistency

API-created event sources and notification channels are inserted in the same PostgreSQL transaction as their creation audit records. One-time HMAC/bearer source credentials are returned only after commit. Audit metadata excludes plaintext credentials and notification-channel configuration; an audit failure rolls back the resource insert instead of creating an untracked resource with a lost credential. Direct internal storage creation methods are not automatically audited and must not be exposed to untrusted HTTP clients.

### Secret disclosure

Recoverable source secrets and acknowledgement tokens use AES-256-GCM with resource-bound additional authenticated data. Bearer credentials are hashed. Tokens, signatures, authorization headers, full payloads, and acknowledgement paths are excluded from structured logs and audit metadata. Source credentials are returned once.

### Cross-tenant access

Tenant-scoped service accounts and guarded OIDC bearer authentication are implemented. Incident, source, policy and audit APIs use the authenticated tenant. Manual incident creation provisions a reserved internal source for each tenant on first use; `manual-api` is not available via the public webhook or source-test routes, even when unauthenticated webhook sources are enabled. Tenant-scoped service accounts and guarded OIDC bearer authentication are implemented. Incident, source, policy and audit APIs use the authenticated tenant. Manual incident creation provisions a reserved internal source for each tenant on first use; `manual-api` is not available via the public webhook or source-test routes, even when unauthenticated webhook sources are enabled. Cross-tenant regression tests cover source visibility and provisioning. Authenticated source-test requests constrain source ownership in the PostgreSQL lookup before loading or decrypting the source credential; public signed webhooks instead resolve their tenant from the globally unique source ID. A complete tenant isolation audit of every API, storage operation and plugin remains open; do not treat this as a claim of production multi-tenant isolation. A complete tenant isolation audit of every API, storage operation and plugin remains open; do not treat this as a claim of production multi-tenant isolation.

### Concurrent duplicate processing

A database unique constraint selects one canonical event. Losers increment a counter and insert immutable duplicate observations. Correlation uses an advisory transaction lock derived from the tenant-scoped key.

### External-provider ambiguity

Notification calls are made outside the event transaction. Delivery rows are leased. An expired lease for an external provider becomes `ambiguous`, requiring inspection, instead of automatic blind replay.

### Audit tampering

The application writes audit records through inserts only. A database trigger rejects update and delete. This is not protection against a PostgreSQL superuser; production deployments must separate application and administrative credentials and export audit records to independent storage.

### SSRF and command execution

Milestone 2 HTTP and process-plugin actions require exact configured host allowlists. The client resolves the hostname, rejects loopback, link-local, multicast, unspecified, and metadata addresses, pins the permitted IP set for the request, disables proxies, and rejects redirects, URL userinfo, and fragments. Plugin actions must be declared in a versioned manifest and use bounded strict JSON. Generic shell execution is not implemented.

## Residual risks

- Development bootstrap authentication is not suitable for hostile multi-user environments.
- In-memory rate and replay state is per replica; durable deduplication still protects effects, but a distributed limiter is future work.
- Database administrators can alter stored data and audit unless external immutability controls are deployed.
- Telegram does not provide a general idempotency key, so network ambiguity cannot be eliminated.
- Raw payload retention can contain regulated data; operators must configure retention, encryption at rest, access controls, and deletion policy appropriate to their jurisdiction.

Unauthenticated webhook sources are rejected unless `STORMRELAY_ALLOW_UNAUTHENTICATED_SOURCES=true` is explicitly configured. The built-in manual source is reachable only through the authenticated incident API in the default configuration.
