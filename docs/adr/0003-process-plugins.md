# ADR-0003: Plugins run out of process

Status: accepted for Milestone 3; runtime not implemented yet

Plugins will run as separate processes or containers and communicate over a versioned HTTP contract. The control plane will enforce action declarations, permissions, request and response size limits, deadlines, retries, trace context, and idempotency keys. A plugin crash must not crash the server or worker. In-process Go plugins are rejected because they couple ABI, memory safety, and failure domains.
