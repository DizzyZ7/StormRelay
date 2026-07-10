# Process plugin protocol v1

Plugins run as separate processes or containers. StormRelay never loads plugin code into the server or worker address space.

A plugin exposes:

- `GET /stormrelay/plugin/v1/manifest`
- `POST /stormrelay/plugin/v1/actions/{action}`

The manifest declares plugin ID, plugin version, protocol version `stormrelay.plugin/v1`, actions, permissions, and optional JSON schemas. Action names are strict identifiers and undeclared actions fail closed.

The action is selected by the validated URL path segment. Every request body contains protocol version, execution ID, step ID, request ID, optional trace context, deadline, immutable input, and an idempotency key. A valid response echoes the protocol version and idempotency key and returns `succeeded` or `failed` with bounded output or a sanitized error.

StormRelay enforces exact host allowlists, DNS/IP checks, request deadlines, response size limits, strict JSON decoding, declared actions, and persisted retry policy. A timeout, crash, malformed response, incompatible version, or idempotency mismatch fails the step without affecting unrelated workers.

See `examples/plugins/echo-python` for a harmless reference implementation.
