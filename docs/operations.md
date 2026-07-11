# Operations

## Configuration

Required environment variables:

- `STORMRELAY_MASTER_KEY`: base64-encoded 32-byte encryption key. Back it up separately; losing it makes encrypted source and acknowledgement secrets unrecoverable.
- `STORMRELAY_BOOTSTRAP_API_KEY`: local bootstrap API key. Treat this mode as development-only.
- `STORMRELAY_DATABASE_URL`: pgx PostgreSQL URL.
- `STORMRELAY_NATS_URL`: NATS connection URL.

The server binds `:8080`; the worker health server binds `:8081`. Override with `STORMRELAY_HTTP_ADDRESS` and `STORMRELAY_WORKER_HTTP_ADDRESS`.

Runbook and plugin settings:

- `STORMRELAY_RUNBOOK_CONCURRENCY`: bounded concurrent leased steps.
- `STORMRELAY_RUNBOOK_HTTP_ALLOWED_HOSTS`: comma-separated exact host allowlist for HTTP steps.
- `STORMRELAY_PLUGIN_ALLOWED_HOSTS`: comma-separated exact host allowlist for process-plugin discovery and actions.

An empty allowlist denies every outbound runbook or plugin destination.

## Health and readiness

`/healthz` proves the process can serve HTTP. `/readyz` verifies PostgreSQL, JetStream, and the expected migration version. Remove an instance from traffic when readiness is non-200.

## Logs and sensitive data

Logs are structured JSON. Request and trace IDs are included. Do not enable reverse-proxy body logging on webhook paths. Authorization, signatures, source credentials, acknowledgement tokens, raw payloads, Telegram token, and secret URLs must not be forwarded to log attributes.

## Metrics, dashboards, and alerts

Server and worker expose Prometheus text at `/metrics`. Current metrics include ingress, rejection, duplicates, event-processing latency totals, open incidents, notification failures, runbook failures and duration totals, JetStream lag, and database pool gauges.

Avoid adding source ID, incident ID, tenant ID, URL, or arbitrary labels as metric labels; they create unbounded cardinality.

The development Compose stack automatically provisions:

- Prometheus at `http://localhost:9090`;
- Grafana at `http://localhost:3000`;
- Tempo at `http://localhost:3200`;
- OpenTelemetry Collector OTLP/gRPC at `localhost:4317` and health at `localhost:13133`;
- the `StormRelay Prometheus` and `StormRelay Tempo` datasources;
- the `StormRelay Operations` dashboard in the `StormRelay` folder;
- availability, pipeline, capacity, and integration-failure alert rules.

The Compose Grafana credentials are `admin` / `admin` and are development-only. Replace credentials and configure the intended authentication mechanism before exposing Grafana outside localhost or a trusted development network.

The dashboard covers target availability, open incidents, event rates, processing latency, JetStream lag, PostgreSQL pool utilization, runbook duration, and operational failure rates. Provisioned dashboards are immutable in the UI; edit the version-controlled JSON instead.

Alert thresholds are conservative development defaults. Establish a workload baseline before tuning backlog, pool-utilization, rejection-ratio, or open-incident thresholds. Do not increase a threshold merely to silence a real capacity or reliability problem.

Every rule links to a dedicated procedure in `docs/alert-runbooks.md`. Prometheus validates the rule expressions in CI, and the Compose smoke test verifies that Prometheus loads the rules and Grafana loads the datasource and dashboard.

## Distributed tracing

Optional OTLP/gRPC tracing connects inbound HTTP requests to JetStream publication and consumption, PostgreSQL event transactions, deduplication, correlation, policy decisions, notification outbox delivery, runbook attempts, HTTP actions, and process-plugin calls. Only W3C `traceparent` is persisted across asynchronous boundaries; baggage is not persisted.

Production configuration requires the same collector endpoint on server and worker:

```bash
OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=https://otel-collector.example:4317
STORMRELAY_OTEL_TRACE_SAMPLE_RATIO=0.10
STORMRELAY_OTEL_EXPORT_TIMEOUT=10s
```

Keep the collector outside the StormRelay process boundary. Restrict server and worker egress to the collector, and collector egress to the selected backend. Configure backend authentication, TLS, retry, memory limiting, and storage credentials in the collector rather than in StormRelay.

The Compose stack samples every trace and retains local Tempo blocks for 24 hours. Those settings are for demonstration only. Production sampling and retention must be based on measured event volume, investigation requirements, privacy policy, and storage cost. Trace access remains operationally sensitive even though StormRelay excludes payloads, credentials, provider error bodies, and user identifiers.

Use the `traceparent` returned in an API response to query the exact trace. Traces are diagnostic evidence, not the authoritative audit record; append-only PostgreSQL audit entries remain the security and compliance source of truth.

See `docs/observability.md` for the complete span map, exporter bounds, data policy, live verification, and troubleshooting procedure.

## Backup and restore

PostgreSQL is the source of truth for canonical events, deduplication history, incidents, runbook state, notification delivery state, identity, and audit. JetStream is a durable transport and replay source; it is not a replacement for a database backup.

Back up `STORMRELAY_MASTER_KEY` through a separate encrypted secret-management path. A database dump without the matching master key preserves encrypted bytes but cannot recover source credentials or acknowledgement secrets. Never place the key in the dump archive, CI artifact, command history, or operator ticket.

### Tested local drill

Run the destructive, isolated Compose drill from the repository root:

```bash
make backup-restore-drill
```

The drill uses its own Compose project and volumes. It:

1. creates a real HMAC source and processes a signed event into raw-event, normalized-event, incident, notification, and audit state;
2. stops server and worker writes at a defined recovery point;
3. creates a custom-format logical PostgreSQL backup;
4. restores into a newly created database rather than restoring over live tables;
5. compares deterministic counts and SHA-256 fingerprints for migrations, tenants, encrypted source configuration, raw-event metadata, normalized events, duplicates, incidents, transitions, and audit entries;
6. recomputes every stored raw-payload SHA-256 without printing payloads;
7. checks key foreign-key relationships and verifies that the append-only audit trigger still rejects mutation;
8. publishes the exact persisted event envelope again to model JetStream redelivery and verifies one canonical event plus an auditable duplicate;
9. submits a fresh signed webhook using the restored encrypted source credential.

The workflow `.github/workflows/backup-restore.yml` executes the same drill for storage, messaging, worker, migration, and Compose changes. Its uploaded diagnostic log contains no source credential, raw payload, database dump, or invariant values.

### Production backup procedure

Choose logical or physical backups according to database size and recovery objectives. Test the exact mechanism against a representative environment; the Compose drill validates StormRelay invariants, not the throughput or retention characteristics of a production backup system.

Before backup:

1. record the StormRelay version, expected migration version, PostgreSQL version, JetStream stream/consumer identity, and intended recovery timestamp;
2. establish a consistent recovery point by quiescing ingress and stopping workers, or use a database-native snapshot that is transactionally consistent while writes continue;
3. confirm no schema migration is running;
4. back up the master key separately and verify that both backup sets have independent access controls;
5. create the database backup without embedding credentials in shell history or process arguments; use a protected PostgreSQL service file, `.pgpass`, workload identity, or the platform's secret injection mechanism;
6. encrypt the archive at rest, retain checksums and immutable retention metadata, and test decryption before declaring the backup usable.

A logical backup should use custom or directory format with ownership and privilege handling chosen for the restore target. Capture global objects separately only when the deployment relies on database roles or tablespaces not managed by the platform.

### Production restore order

1. keep all StormRelay server and worker processes stopped or removed from traffic;
2. restore the matching master key through the secret manager, without exposing it to restore logs;
3. restore PostgreSQL into a fresh database or fresh cluster and fail on restore errors;
4. verify migration history, row counts, raw-payload hashes, incident/event links, audit triggers, and application ownership before switching traffic;
5. start one server instance, verify `/readyz`, the version endpoint, and a read-only API request;
6. start workers with bounded concurrency and monitor JetStream lag, duplicate counters, notification failures, runbook ambiguity, and the dead-letter subject;
7. re-enable ingress only after restored credentials and representative signed test events succeed;
8. retain the pre-restore database or snapshot until the recovery has passed the operational acceptance window.

Do not run destructive down migrations after a newer application version has written data. Rollback means restoring a compatible recovery point or deploying a forward fix.

### RPO, RTO, and JetStream redelivery

The recovery point objective is bounded by the age of the PostgreSQL backup and the separately retained master key. The recovery time objective includes archive retrieval, database creation, restore, invariant verification, application startup, and controlled queue drain. Measure both with production-sized data; the repository does not publish invented RPO or RTO values.

JetStream may contain messages newer than the restored PostgreSQL recovery point. Redelivery is safe when the restored database contains the matching deduplication bucket: StormRelay records a duplicate instead of creating another canonical event. If JetStream is ahead of PostgreSQL, messages after the database recovery point are processed as new work. If PostgreSQL is restored from before an earlier event's deduplication history, the same message can create a new canonical event outside the restored window. Record both recovery points, inspect consumer state and `<subject>.dlq`, and never purge the stream merely to make lag disappear.

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
