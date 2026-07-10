CREATE TABLE oidc_providers (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 200),
    issuer text NOT NULL CHECK (issuer LIKE 'https://%'),
    audience text NOT NULL CHECK (char_length(audience) BETWEEN 1 AND 500),
    jwks_uri text NOT NULL CHECK (jwks_uri LIKE 'https://%'),
    supported_signing_algs text[] NOT NULL DEFAULT ARRAY['RS256']::text[],
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version bigint NOT NULL DEFAULT 1,
    UNIQUE (tenant_id, name),
    UNIQUE (issuer, audience)
);
CREATE INDEX oidc_providers_tenant_idx ON oidc_providers(tenant_id, created_at DESC);

CREATE TABLE oidc_identities (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    provider_id uuid NOT NULL REFERENCES oidc_providers(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    subject text NOT NULL CHECK (char_length(subject) BETWEEN 1 AND 1000),
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version bigint NOT NULL DEFAULT 1,
    UNIQUE (provider_id, subject),
    UNIQUE (provider_id, user_id)
);
CREATE INDEX oidc_identities_tenant_idx ON oidc_identities(tenant_id, created_at DESC);

CREATE TABLE oidc_identity_roles (
    identity_id uuid NOT NULL REFERENCES oidc_identities(id) ON DELETE CASCADE,
    role text NOT NULL CHECK (role IN ('viewer','operator','responder','runbook-editor','integration-admin','tenant-admin')),
    PRIMARY KEY (identity_id, role)
);
