# Process-plugin compatibility policy

## Supported wire protocol

StormRelay currently supports `stormrelay.plugin/v1`.

A plugin is compatible when:

- its manifest reports exactly `stormrelay.plugin/v1`;
- every invoked action is declared in the manifest;
- requests and responses satisfy the documented required fields and bounds;
- every response echoes the request idempotency key;
- action status is either `succeeded` or `failed`;
- output and safe error text remain valid bounded JSON.

## Changes allowed within v1

The following changes may be made without changing the protocol version:

- adding a new plugin action;
- adding a new permission declaration;
- changing an action description;
- fixing SDK validation or security behavior without changing accepted valid messages;
- adding optional fields only when older SDKs and the conformance runner safely ignore or reject them according to the published contract.

## Changes requiring a new protocol version

A new version is required before:

- renaming or removing required fields;
- changing route paths or HTTP methods;
- changing status values or idempotency semantics;
- accepting a different serialization;
- weakening request, response, authentication, or deadline invariants;
- changing the meaning of an existing field in a wire-incompatible way.

StormRelay and the official SDKs fail closed on unknown protocol versions. They do not guess compatibility.

## Plugin release checklist

Before publishing a plugin image:

1. Pin the official SDK version or repository commit used for the build.
2. Run language unit tests, linters, and dependency scanning.
3. Run `stormrelay-plugin-conformance` against the final container image.
4. Test every declared action, including safe failure paths.
5. Confirm side effects are idempotent using the supplied idempotency key.
6. Confirm all external calls honor the request context and deadline.
7. Verify logs and responses do not expose bearer tokens, credentials, personal data, or raw upstream failures.
8. Document required permissions and outbound destinations.
9. Sign the image and publish an SBOM when release tooling is available.

The repository's `Plugin Conformance` workflow demonstrates this process with the SDK-based Python echo plugin.
