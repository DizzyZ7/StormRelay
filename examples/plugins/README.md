# Process-plugin examples

## Recommended

Use [`echo-python-sdk`](./echo-python-sdk/) as the starting point for new Python plugins. It uses the supported `stormrelay_plugin` server SDK, inherits protocol validation and security limits, packages the SDK into a non-root container, and is exercised by the live Plugin Conformance workflow.

For Go, start with the example in [`docs/plugin-sdk.md`](../../docs/plugin-sdk.md) and import `github.com/DizzyZ7/StormRelay/sdk/go/plugin`.

## Low-level protocol example

`echo-python` predates the server SDK and implements the HTTP wire contract manually. It remains useful for understanding the protocol, but new plugins should not copy it: manual implementations can easily diverge on strict JSON, deadlines, response bounds, idempotency echo, authentication, and safe failure handling.

Before publishing any plugin image, run `stormrelay-plugin-conformance` against the final container and follow [`plugins/COMPATIBILITY.md`](../../plugins/COMPATIBILITY.md).
