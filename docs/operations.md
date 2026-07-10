# Operations

## Configuration

Required environment variables:

- `STORMRELAY_MASTER_KEY`: base64-encoded 32-byte encryption key. Back it up separately; losing it makes encrypted source and acknowledgement secrets unrecoverable.
- `STORMRELAY_BOOTSTRAP_API_KEY`: local bootstrap API key. Treat this mode as development-only.
- `STORMRELAY_DATABASE_URL`: pgx PostgreSQL URL.
- `STORMRELAY_NATS_URL`: NATS connection URL.

The server binds `:8080`; the worker health server binds `:8081`. Override with `STORMRELAY_HTTP_ADDRESS` and `STORMRELAY_WORKER_HTTP_ADDRESS`.

Milestone 2 settings:

- `STORMRELAY_RUNBOOK_CONCURRENCY`: bounded concurrent leased steps.
- `STORMRELAY_RUNBOOK_HTTP_ALLOWED_HOSTS`: comma-separated exact host allowlist for HTTP steps.
- `STORMRELAY_PLUGIN_ALLOWED_HOSTS`: comma-separated exact host allowlist for process-plugin discovery and actions.

An empty allowlist denies every outbound runbook or plugin destination.

## Health and readiness

`/healthz` proves the process can serve HTTP. `/readyz` verifies PostgreSQL, JetStream, and the expected migration version. Remove an instance from traffic when readiness is non-200.

## Logs and sensitive data

Logs are structured JSON. Request and trace IDs are included. Do not enable reverse-proxy body logging on webhook paths. Authorization, signatures, source credentials, acknowledgement tokens, raw payloads, Telegram token, and secret URLs must not be forwarded to log attributes.

## Metrics

Server and worker expose Prometheus text at `/metrics`. Current metrics include ingress, rejection, duplicates, event-processing latency totals, open incidents, notification failures, runbook failures and duration totals, JetStream lag, and database pool gauges. Avoid adding source ID, incident ID, tenant ID, URL, or arbitrary labels as metric labels; they create unbounded cardinality.

## Backup and restore

For PostgreSQL, use consistent logical or physical backups with encryption. Stop application writes or use a database-native consistent snapshot. Restore PostgreSQL before starting workers. Preserve the master key independently.

JetStream contains messages that may be redelivered after a database restore. This is safe only if the restored database includes the corresponding deduplication history. Restoring JetStream to a newer point than PostgreSQL can recreate canonical events outside the restored dedupe window. Document the recovery point and inspect the dead-letter subject.

A complete tested backup/restore drill is tracked in issue #3.

## Migration strategy

Migrations are forward-only and run under a PostgreSQL advisory lock. Each file is committed in a transaction and recorded in `schema_migrations`. Do not automatically execute destructive down migrations after new application versions have written data. Rollback means restoring a compatible database backup or deploying a forward fix.

Before upgrade:

1. Back up PostgreSQL and the encryption key.
2. Check release notes and compatibility matrix.
3. Stop workers before schema changes that alter consumer writes.
4. Run the new server or a dedicated migration job once.
5. Confirm `/readyz` and migration version.
6. Start workers and monitor lag, failures, and DLQ traffic.

## Dead-letter handling

After five failed deliveries, the worker publishes the original event to `<subject>.dlq` with bounded sanitized failure metadata and acknowledges the original. Do not replay DLQ messages until the root cause is fixed. Replays must preserve or deliberately replace the idempotency key.

## Notification ambiguity

A provider call can succeed remotely and fail locally before the result is saved. StormRelay records `ambiguous` when a delivery lease expires for an external provider. Operators should verify the provider before retrying. This is a deliberate rejection of fictional exactly-once notification semantics.

## Runbook recovery and ambiguous outcomes

Execution steps use expiring PostgreSQL leases. Persisted waits and approvals survive worker restarts. An expired plugin or explicitly idempotent HTTP attempt can be retried with the same idempotency key when attempts remain. An uncertain non-idempotent HTTP result becomes `ambiguous`; verify the remote system, then retry the exact step explicitly with `force=true` only when operationally justified. Pause/resume does not clear ambiguity.
