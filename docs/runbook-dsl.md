# Runbook DSL

Runbook execution is Milestone 2 work. The schema tables reserve immutable versions and execution snapshots, but the engine is not implemented in the current vertical slice.

Planned `stormrelay.io/v1` steps are HTTP, wait, manual approval, condition, plugin action, notification, and disabled-by-default isolated shell action. Every step version will define timeout, retry policy, idempotency behavior, permissions, and optional explicit rollback. Arbitrary evaluation is prohibited.

A shell action will require a feature flag, registered action definition, image and command allowlists, non-root user, resource limits, read-only root filesystem, no privileged mode, bounded network policy, and a hard timeout.
