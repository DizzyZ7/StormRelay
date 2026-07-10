# Process plugin runtime

Milestone 2 implements an out-of-process plugin runtime using `stormrelay.plugin/v1`. Plugin code is never loaded into the server or worker address space. Registration performs manifest discovery through the configured exact host allowlist; action calls enforce deadlines, bounded strict JSON, declared actions, protocol compatibility, and idempotency-key echo.

See `docs/plugin-development.md` and the side-effect-free `examples/plugins/echo-python` implementation. Generic shell plugins are not supported.
