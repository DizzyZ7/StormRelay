# Plugin development

The process-based plugin protocol is designed but not implemented in Milestone 1. Do not deploy a plugin and assume StormRelay will execute it yet.

Protocol v1 will use HTTP JSON over a private network boundary:

- `GET /stormrelay/plugin/v1/manifest` returns plugin ID, version, protocol version, declared actions, permissions, and input/output schemas.
- `POST /stormrelay/plugin/v1/actions/{action}` receives an immutable input snapshot, execution ID, step ID, request ID, W3C trace context, deadline, and idempotency key.
- responses contain status, bounded structured output, and a sanitized error; secrets must never be echoed.

The control plane will reject undeclared actions, incompatible protocol versions, oversized bodies, missing permissions, and calls beyond the configured deadline. Retry is controlled by StormRelay, not by hidden plugin loops.

See ADR-0003 and issue #4 for acceptance criteria.
