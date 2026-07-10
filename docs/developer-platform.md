# Developer platform

StormRelay Milestone 3 provides supported client and plugin surfaces instead of requiring consumers to reconstruct internal HTTP contracts.

## API clients

The Go and Python API clients under `sdk/` support bearer authentication, bounded response parsing, structured API errors, request identifiers, cancellation/timeouts, incident workflows, runbooks, approvals, and identity administration. The Python client uses no external runtime dependencies.

## Machine identity

Tenant-scoped service accounts use one-time 256-bit API keys. StormRelay stores only the full-credential hash and public prefix. Keys support expiry, revocation, and last-used metadata. Backend authorization maps every protected route family to an explicit permission and fails closed for unmapped routes.

## Federated identity

Tenant administrators may register guarded OIDC providers and explicitly map stable provider subjects to local users and roles. Discovery and JWKS traffic uses public-only DNS/IP validation and pinning. Tokens are accepted only after signature, issuer, audience, expiry, and asymmetric-algorithm verification. Email claims never create or link accounts.

## Process-plugin development

Official Go and Python server SDKs implement `stormrelay.plugin/v1` routing, validation, authentication, limits, deadlines, idempotency-key echo, and safe failure handling. A standalone `stormrelay-plugin-conformance` binary exercises final plugin images through StormRelay's production client.

The repository includes:

- Go plugin server SDK and race-tested HTTP behavior;
- standard-library Python plugin server SDK and live HTTP tests;
- an SDK-based Python echo plugin container;
- a containerized conformance runner;
- a live cross-language Docker conformance workflow;
- protocol compatibility and plugin release policies.

See `docs/identity.md`, `docs/plugin-sdk.md`, `plugins/COMPATIBILITY.md`, and the examples under `examples/plugins/`.

## Remaining future work

Milestone 3 does not claim browser session management, refresh-token storage, automatic account linking, arbitrary shell plugins, or a hosted plugin marketplace. Production observability, Kubernetes packaging, signed release artifacts, SBOM publication, and provenance remain later milestones.
