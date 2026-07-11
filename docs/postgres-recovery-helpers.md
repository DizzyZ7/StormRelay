# PostgreSQL recovery helpers

StormRelay includes two small operator-facing wrappers around PostgreSQL's native logical-backup tools:

- `scripts/postgres-backup.sh` creates and validates a custom-format archive plus a SHA-256 sidecar.
- `scripts/postgres-restore.sh` verifies the checksum and archive, requires explicit confirmation, refuses non-empty targets, and restores in one transaction.

These helpers do not replace an organization's backup platform, encryption, retention, access-control, or disaster-recovery policy. They make the dangerous edges of a manual logical restore explicit and testable.

## Prerequisites

Use PostgreSQL client tools with the same major version as the server whenever possible. Required commands are:

- `pg_dump` and `pg_restore` for backup;
- `psql`, `pg_restore`, and `sha256sum` for restore.

The scripts accept a PostgreSQL connection URL through `STORMRELAY_DATABASE_URL`. Connection URLs may contain credentials. Prefer a protected PostgreSQL service file, `.pgpass`, workload identity, or secret injection rather than placing credentials in shell history.

Preserve `STORMRELAY_MASTER_KEY` separately through the approved secret manager. Restoring PostgreSQL without the matching key preserves ciphertext but cannot recover encrypted source and integration credentials.

## Backup

Quiesce application writes or use a database-native consistent snapshot procedure. When the database archive is expected to cover every accepted StormRelay event, stop ingress and workers and verify JetStream consumer lag is zero.

```bash
export STORMRELAY_DATABASE_URL='postgres://user:password@database:5432/stormrelay?sslmode=require'
sh scripts/postgres-backup.sh /secure/backups/stormrelay-$(date -u +%Y%m%dT%H%M%SZ).dump
```

The backup helper:

- sets `umask 077`;
- refuses to overwrite an existing archive or checksum;
- writes to temporary files and publishes them by rename;
- uses PostgreSQL custom format with ownership and privileges omitted;
- validates the archive catalog with `pg_restore --list`;
- writes `<archive>.sha256` beside the archive.

Encrypt and replicate both files using the organization's approved controls. Keep the master key separate from them.

## Restore

Create a new, empty target database. The helper deliberately has no `--clean` behavior and refuses any target containing user tables.

```bash
export STORMRELAY_DATABASE_URL='postgres://user:password@new-database:5432/stormrelay?sslmode=require'
export STORMRELAY_RESTORE_CONFIRM=YES
sh scripts/postgres-restore.sh /secure/backups/stormrelay-20260711T070000Z.dump
```

Before writing, the restore helper:

1. requires the exact confirmation value `YES`;
2. requires and verifies the SHA-256 sidecar;
3. validates the archive catalog;
4. queries the target and refuses it unless the user-table count is zero.

The restore runs with `--single-transaction` and `--exit-on-error`. Any restore error rolls back the transaction instead of leaving a partially restored schema.

## Recovery boundaries

A PostgreSQL archive does not include unprocessed NATS JetStream messages. Establish and record one of these recovery models:

- quiesce ingress and workers, verify zero consumer lag, then create the database backup; or
- capture and test a separate JetStream recovery point coordinated with PostgreSQL.

If JetStream is ahead of the restored database, newer messages are processed as new work. Redelivery of messages whose deduplication history exists in the restored database is recorded as duplicate work rather than creating another canonical event. Never purge the stream merely to reduce lag.

## Verification

After restore, follow the full procedure in `docs/operations.md`. At minimum verify migrations, readiness, raw-payload hashes, event/incident relationships, audit immutability, restored encrypted credentials, JetStream state, DLQ state, notifications, plugins, and tracing/metrics connectivity.

The workflow `.github/workflows/backup-restore.yml` runs two complementary checks:

- `tests/failure/backup-restore-helpers.sh` verifies helper guardrails, checksum handling, no-overwrite behavior, non-empty-target refusal, and an empty-target restore.
- `tests/failure/backup-restore.sh` verifies StormRelay data fingerprints, payload hashes, foreign keys, audit immutability, JetStream redelivery deduplication, and restored encrypted credentials.

CI artifacts contain sanitized logs only. Database archives and credentials are never uploaded.
