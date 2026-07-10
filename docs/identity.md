# Identity and authorization

StormRelay Milestone 3 provides tenant-scoped service accounts, backend-enforced role-based access control, and guarded OpenID Connect federation. The bootstrap key remains an initial provisioning and emergency credential for the configured tenant; routine automation should use a narrowly scoped service account, while interactive users may authenticate through an explicitly configured OIDC provider.

## Service-account credentials

A generated credential has the form `srk_<16-hex-prefix>_<base64url-secret>`. The secret contains 256 bits of random material. StormRelay returns the complete credential once. PostgreSQL stores only the globally unique public prefix and SHA-256 of the complete credential, so a database read cannot recover usable keys.

Keys may expire and can be revoked independently. Disabling the service account invalidates all its keys without deleting audit history. Authentication updates `last_used_at` only after a constant-time hash comparison succeeds.

## OIDC trust model

A tenant administrator registers an exact HTTPS issuer and API audience. StormRelay fetches the issuer discovery document once during registration and stores the discovered JWKS URI and supported asymmetric signing algorithms.

Discovery and JWKS endpoints use the public-only outbound guard:

- only exact allowlisted hostnames are dialed;
- DNS results are pinned for each request;
- proxies and redirects are disabled;
- loopback, link-local, metadata, RFC 1918/private, unique-local IPv6, and carrier-grade NAT destinations are rejected;
- userinfo, URL fragments, and query-bearing trust URLs are rejected;
- discovery `issuer` must exactly equal the configured issuer.

At request time, unverified `iss` and `aud` claims are used only to select an already configured provider. StormRelay grants no access until the token signature, issuer, audience, allowed asymmetric algorithm, and expiry are verified. HMAC JWT algorithms and `alg=none` are never accepted.

## Explicit subject mapping

StormRelay never creates or links a local account from an email claim. A tenant administrator must explicitly provision a mapping from `(provider, subject)` to a local user and assign roles. The subject is the stable external identity key; email and display name are profile metadata only.

A duplicate email therefore fails instead of silently linking a second external subject. Disabling either the provider or the individual mapping immediately prevents authentication while retaining audit history.

## Roles

- `viewer`: read incidents, policies, runbooks, executions, integrations, and audit.
- `operator`: viewer access plus incident state changes.
- `responder`: operator access plus runbook execution control and approval decisions.
- `runbook-editor`: read incidents and manage policies/runbooks, including starting executions.
- `integration-admin`: read operational state and manage sources, channels, and plugins.
- `tenant-admin`: every current permission, including service-account and OIDC federation administration.

Every protected route maps to an explicit permission in backend middleware. Unmapped future routes fail closed. The tenant ID always comes from the authenticated principal, never from request JSON or query parameters. A valid credential without permission receives `403 forbidden`; the denial is appended to audit with route, method, and required permission, but not the credential or request body.

## Service-account provisioning example

```bash
ADMIN='Authorization: Bearer local-development-only-change-me'
ACCOUNT=$(curl -fsS -H "$ADMIN" -H 'Content-Type: application/json' \
  -d '{"name":"incident-reader","roles":["viewer"]}' \
  http://localhost:8080/api/v1/service-accounts)
ACCOUNT_ID=$(printf '%s' "$ACCOUNT" | jq -r .id)

curl -fsS -H "$ADMIN" -H 'Content-Type: application/json' -d '{}' \
  "http://localhost:8080/api/v1/service-accounts/$ACCOUNT_ID/keys" | jq
```

Store the returned `credential` in a secret manager immediately. It is not returned by list APIs.

## OIDC provisioning example

Provider creation performs live guarded discovery:

```bash
PROVIDER=$(curl -fsS -H "$ADMIN" -H 'Content-Type: application/json' \
  -d '{"name":"company-sso","issuer":"https://login.example.com","audience":"stormrelay-api"}' \
  http://localhost:8080/api/v1/oidc/providers)
PROVIDER_ID=$(printf '%s' "$PROVIDER" | jq -r .id)

curl -fsS -H "$ADMIN" -H 'Content-Type: application/json' \
  -d '{"subject":"00u-stable-subject","email":"responder@example.com","display_name":"Incident Responder","roles":["responder"]}' \
  "http://localhost:8080/api/v1/oidc/providers/$PROVIDER_ID/identities" | jq
```

The access token is then sent through the same bearer header as a service-account credential:

```text
Authorization: Bearer <signed OIDC access token>
```

StormRelay validates only tokens intended for the configured API audience. Browser login redirects, refresh-token storage, and interactive session cookies remain outside this server-side federation slice.
