# Process-plugin SDK and conformance

StormRelay process plugins implement the versioned `stormrelay.plugin/v1` HTTP protocol. Supported Go and Python server SDKs remove the need to hand-write manifest routing, request validation, deadline handling, idempotency echo, authentication, limits, and error sanitization.

## Protocol endpoints

Every plugin exposes exactly these routes:

- `GET /stormrelay/plugin/v1/manifest`
- `POST /stormrelay/plugin/v1/actions/{action}`

The manifest declares a stable lowercase plugin identifier, plugin version, protocol version, actions, descriptions, and permissions. Action names and permissions use `^[a-z][a-z0-9._-]{0,63}$`.

The server SDKs enforce:

- strict JSON request fields;
- a 1 MiB request limit and 1 MiB response limit;
- required execution, step, request, deadline, and idempotency fields;
- deadlines in the future and no more than 24 hours away;
- exact protocol-version matching;
- declared-action routing only;
- idempotency-key echo in every action response;
- optional bearer authentication;
- generic sanitization of unexpected errors and explicit safe operator errors;
- no-store and nosniff response headers.

## Python plugin

The Python SDK uses only the standard library.

```python
from stormrelay_plugin import Action, Plugin, PluginRequest, fail


def restart(request: PluginRequest) -> dict:
    if not isinstance(request.input, dict) or "service" not in request.input:
        raise fail("input.service is required")
    # Perform a bounded, idempotent integration call here.
    return {"service": request.input["service"], "accepted": True}


Plugin(
    "operations-python",
    "1.0.0",
    [Action("restart", restart, permissions=("service.restart",))],
).serve(port=8090)
```

Use `PYTHONPATH=sdk/python` during local development or package `stormrelay_plugin` into the plugin image. `examples/plugins/echo-python-sdk` shows a complete containerized example.

Unexpected exceptions are converted to `plugin action failed`; their original messages are not returned to StormRelay. Raise `fail("safe operator message")` only for text intentionally suitable for audit and operator display. Never include credentials, tokens, raw upstream responses, or personal data.

## Go plugin

```go
package main

import (
    "context"
    "log"

    pluginsdk "github.com/DizzyZ7/StormRelay/sdk/go/plugin"
)

func main() {
    server, err := pluginsdk.NewServer("operations-go", "1.0.0", []pluginsdk.Action{{
        Name: "restart",
        Permissions: []string{"service.restart"},
        Handler: func(ctx context.Context, request pluginsdk.Request) (any, error) {
            return map[string]any{"accepted": true}, nil
        },
    }})
    if err != nil {
        log.Fatal(err)
    }
    log.Fatal(server.ListenAndServe(":8090"))
}
```

Handlers receive a context bounded by the StormRelay request deadline. Respect context cancellation in every external call. Use `pluginsdk.Fail(...)` for an explicit safe failure; ordinary errors are sanitized.

## Conformance runner

Build and run the standalone verifier before registering a plugin:

```bash
go build -o stormrelay-plugin-conformance ./cmd/stormrelay-plugin-conformance

./stormrelay-plugin-conformance \
  --endpoint http://plugin.internal:8090 \
  --action echo \
  --input '{"hello":"world"}'
```

For authenticated plugins, add `--bearer-token` through a secret-aware invocation mechanism. Do not place real credentials in shell history or CI logs.

The runner uses StormRelay's production plugin client. It validates the manifest, requires the selected action to be declared, sends a bounded request with a deadline and unique idempotency key, verifies the protocol response and idempotency echo, and emits a JSON report. The live GitHub Actions conformance job runs the SDK-based Python example and the runner in an isolated Docker network.

## Compatibility policy

- The protocol version changes only for wire-incompatible behavior.
- Plugin implementation versions are independent from StormRelay releases.
- Servers must reject unsupported protocol versions rather than guessing compatibility.
- New optional manifest or request fields may be introduced only with explicit SDK and conformance coverage.
- Existing required fields, status values, limits, and idempotency semantics remain stable within `stormrelay.plugin/v1`.
- Plugin authors should run the conformance runner against every image intended for release.

## Security checklist

- Keep actions narrowly scoped and explicitly declared.
- Make side effects idempotent using the supplied idempotency key.
- Do not execute arbitrary shell text from plugin input.
- Bound all network calls and honor the request context/deadline.
- Validate input types and allowed values inside each action.
- Store bearer tokens in a secret manager and rotate them independently.
- Return only sanitized output required by the runbook.
- Avoid logging request bodies, authorization headers, or unredacted upstream errors.
