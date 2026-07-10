# Upgrade guide

The project is pre-1.0. The `/api/v1`, event schema `1.0`, policy DSL `stormrelay.io/v1`, runbook DSL `stormrelay.io/v1`, and plugin protocol `stormrelay.plugin/v1` are versioned, but additive changes and explicitly documented pre-1.0 incompatibilities may occur.

Never downgrade the binary against a database that has received writes from a newer incompatible schema. Prefer a forward fix. For rollback, restore the database backup taken before the upgrade and restore compatible JetStream state.

Milestone 2 advances the expected schema to migration 5. Back up PostgreSQL and the encryption key before applying it. Existing event and incident data remains in place; the migration extends runbook, execution, approval, and plugin runtime state.

The current expected migration version is exposed through readiness checks. An instance with missing migrations is not ready.
