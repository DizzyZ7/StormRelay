# Upgrade testing and procedure

StormRelay applies embedded PostgreSQL migrations in ascending numeric order. Every migration runs in its own transaction while a PostgreSQL advisory lock prevents two application instances from applying schema changes concurrently.

## Supported upgrade boundary

The current automated fixture verifies **schema v6 to v7**:

1. a clean PostgreSQL database receives embedded migrations 1 through 6 only;
2. the fixture creates a real encrypted HMAC source, service account, hashed one-time API key, roles, and audit records through the v6 storage API;
3. the legacy storage process is closed;
4. a new process with the same master key runs the production `Store.Migrate` path;
5. the test verifies migration history, credential decryption, API-key authentication, roles, audit continuity, new OIDC tables and constraints, and a second idempotent migration pass.

The test intentionally fails when `ExpectedMigrationVersion` advances beyond 7. A new schema migration must add or update an explicit upgrade fixture rather than silently continuing to test only an older boundary.

Run the drill against an isolated PostgreSQL database:

```bash
export STORMRELAY_TEST_DATABASE_URL='postgres://stormrelay:stormrelay@localhost:5432/stormrelay?sslmode=disable'
make upgrade-drill
```

The drill drops and recreates the target database's `public` schema. Never point it at a shared, staging, or production database.

## Production upgrade procedure

Before upgrading:

1. Read the release notes and migration-specific operator notes.
2. Complete and verify PostgreSQL and, where required, JetStream recovery points.
3. Preserve the exact `STORMRELAY_MASTER_KEY`; schema recovery without the matching key cannot decrypt existing integration credentials.
4. Confirm the current database migration version and application version.
5. Verify sufficient PostgreSQL storage, lock visibility, and maintenance-window capacity.
6. Stop or drain old application instances so only the approved upgrader can start schema work.

During the upgrade:

1. Start one current-version server or worker with automatic migration enabled.
2. Watch structured logs for every `migration applied` record.
3. Do not terminate the process while a migration transaction is active unless required for safety; PostgreSQL will roll back an incomplete transaction.
4. Wait for `/readyz` to report the exact expected migration version before starting the remaining replicas.
5. Roll out workers and servers gradually while observing database saturation, JetStream lag, failed deliveries, runbook failures, and authentication errors.

After the upgrade, verify:

- server and worker readiness;
- exact migration version and one row per migration;
- source credential decryption through a side-effect-free test;
- service-account and OIDC authentication;
- incident, runbook, approval, plugin, notification, and audit access;
- JetStream lag and DLQ;
- Prometheus, Grafana, alerts, and OpenTelemetry export;
- the organization’s acceptance checks and recovery objective.

## Rollback boundary

StormRelay migrations are forward-only. Do not run an older application binary against a database after a newer schema migration unless that exact downgrade path is explicitly documented and tested.

If post-migration validation fails:

- stop the new application processes;
- preserve logs and database state for diagnosis;
- restore the verified pre-upgrade PostgreSQL recovery point and matching master key;
- recover coordinated JetStream state according to the selected recovery point;
- restart the previously approved application version;
- confirm readiness and data invariants before reopening ingress.

A container image rollback without a database restore is not a schema rollback.
