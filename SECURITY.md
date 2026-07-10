# Security policy

## Supported versions

Until the first tagged release, only the latest commit on `main` receives security fixes. After v0.1.0, the latest minor line will be supported according to release notes.

## Reporting

Do not open a public issue. Use GitHub private vulnerability reporting for this repository. Include affected commit/version, impact, reproduction with synthetic data, and suggested mitigation if known. Never include real credentials, bearer tokens, signing keys, or customer payloads.

The maintainers aim to acknowledge a report within five business days, establish severity and remediation ownership, and coordinate disclosure after a fix is available. Timelines vary with complexity and active exploitation.

## Scope reminders

Development bootstrap authentication is an initial provisioning and emergency mechanism, not the recommended routine credential. Tenant-scoped service accounts, one-time hashed API keys, fail-closed backend RBAC, and authorization audit are implemented. Generic shell execution remains disabled and unimplemented.

Runbook and plugin outbound destinations require explicit host allowlists; redirects and proxies are disabled, DNS results are pinned, and loopback/link-local/metadata addresses are prohibited. Explicit internal allowlists may target private service networks. OIDC discovery and JWKS endpoints use a stricter public-only mode that also rejects RFC 1918/private, unique-local IPv6, and carrier-grade NAT destinations.

OIDC federation accepts only configured exact HTTPS issuers and API audiences. It verifies signatures, issuer, audience, expiry, and an allowlist of asymmetric algorithms before resolving an explicitly provisioned provider-subject mapping. Email claims do not create or link accounts. Browser session management, refresh-token storage, and generic identity-provider administration are outside the current server-side bearer-token slice.
