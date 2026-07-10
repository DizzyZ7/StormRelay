# Threat model

## Assets

- source HMAC and bearer credentials;
- API keys and future OIDC identities;
- original webhook payloads;
- normalized event and incident history;
- policy and future runbook definitions;
- acknowledgement tokens;
- notification-provider credentials;
- append-only audit records.

## Trust boundaries

1. Untrusted event producers to the ingestion gateway.
2. Gateway to JetStream.
3. Worker to PostgreSQL.
4. Worker to external notification providers.
5. Operator API clients to the control plane.
6. Future process plugins and shell workers to the core system.

## Primary threats and controls

### Forged or replayed webhooks

HMAC sources sign the timestamp and exact raw body. The gateway enforces a bounded clock window and an in-memory replay cache. JetStream message IDs and database deduplication protect durable effects after process restart. Bearer values are constant-time compared against stored hashes.

### Payload exhaustion and parser abuse

The gateway applies `http.MaxBytesReader` before reading. JSON parsing has bounded input. Policy YAML has a separate limit, known-field decoding, and no arbitrary expression evaluation. Fuzz targets cover event and policy parsing.

### Secret disclosure

Recoverable source secrets and acknowledgement tokens use AES-256-GCM with resource-bound additional authenticated data. Bearer credentials are hashed. Tokens, signatures, authorization headers, full payloads, and acknowledgement paths are excluded from structured logs and audit metadata. Source credentials are returned once.

### Cross-tenant access

Milestone 1 exposes one configured development tenant and a bootstrap API key. It must not be treated as multi-tenant production authentication. Full OIDC and tenant-aware RBAC enforcement are blocked on Milestone 3 and tracked separately. The database schema already carries tenant IDs and foreign keys to prevent accidental global records.

### Concurrent duplicate processing

A database unique constraint selects one canonical event. Losers increment a counter and insert immutable duplicate observations. Correlation uses an advisory transaction lock derived from the tenant-scoped key.

### External-provider ambiguity

Notification calls are made outside the event transaction. Delivery rows are leased. An expired lease for an external provider becomes `ambiguous`, requiring inspection, instead of automatic blind replay.

### Audit tampering

The application writes audit records through inserts only. A database trigger rejects update and delete. This is not protection against a PostgreSQL superuser; production deployments must separate application and administrative credentials and export audit records to independent storage.

### SSRF and command execution

Milestone 1 has no generic outbound webhook adapter, plugin runtime, or shell action. Future implementations must apply destination allowlists, DNS/IP revalidation, network policy, image and command allowlists, read-only filesystems, resource limits, and human approval. Shell actions remain disabled by default.

## Residual risks

- Development bootstrap authentication is not suitable for hostile multi-user environments.
- In-memory rate and replay state is per replica; durable deduplication still protects effects, but a distributed limiter is future work.
- Database administrators can alter stored data and audit unless external immutability controls are deployed.
- Telegram does not provide a general idempotency key, so network ambiguity cannot be eliminated.
- Raw payload retention can contain regulated data; operators must configure retention, encryption at rest, access controls, and deletion policy appropriate to their jurisdiction.

Unauthenticated webhook sources are rejected unless `STORMRELAY_ALLOW_UNAUTHENTICATED_SOURCES=true` is explicitly configured. The built-in manual source is reachable only through the authenticated incident API in the default configuration.
