# Identity and authorization

StormRelay Milestone 3 introduces tenant-scoped service accounts and backend-enforced role-based access control. The bootstrap key remains an initial provisioning and emergency credential for the configured development tenant; routine automation should use a narrowly scoped service account.

## Credential format and storage

A generated credential has the form `srk_<16-hex-prefix>_<base64url-secret>`. The secret contains 256 bits of random material. StormRelay returns the complete credential once. PostgreSQL stores only the public prefix and SHA-256 of the complete credential, so a database read cannot recover usable keys.

Keys may expire and can be revoked independently. Disabling the service account invalidates all its keys without deleting audit history. Authentication updates `last_used_at` only after a constant-time hash comparison succeeds.

## Roles

- `viewer`: read incidents, policies, runbooks, executions, integrations, and audit.
- `operator`: viewer access plus incident state changes.
- `responder`: operator access plus runbook execution control and approval decisions.
- `runbook-editor`: read incidents and manage policies/runbooks, including starting executions.
- `integration-admin`: read operational state and manage sources, channels, and plugins.
- `tenant-admin`: every current permission, including service-account provisioning.

Every protected route maps to an explicit permission in backend middleware. The tenant ID always comes from the authenticated principal, never from request JSON or query parameters. A valid key without permission receives `403 forbidden`; the denial is appended to audit with route, method, and required permission, but not the credential or request body.

## Provisioning example

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

OIDC federation and interactive user sessions are deliberately outside this PR and remain the next identity milestone.
