# Upgrade guide

The project is pre-1.0. The `/api/v1`, event schema `1.0`, and policy DSL `stormrelay.io/v1` are versioned, but additive changes and explicitly documented pre-1.0 incompatibilities may occur.

Never downgrade the binary against a database that has received writes from a newer incompatible schema. Prefer a forward fix. For rollback, restore the database backup taken before the upgrade and restore compatible JetStream state.

The current expected migration version is exposed through readiness checks. An instance with missing migrations is not ready.
