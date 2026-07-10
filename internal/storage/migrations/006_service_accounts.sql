CREATE TABLE service_accounts (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 200),
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version bigint NOT NULL DEFAULT 1,
    UNIQUE (tenant_id, name)
);

CREATE TABLE service_account_roles (
    service_account_id uuid NOT NULL REFERENCES service_accounts(id) ON DELETE CASCADE,
    role text NOT NULL CHECK (role IN ('viewer','operator','responder','runbook-editor','integration-admin','tenant-admin')),
    PRIMARY KEY (service_account_id, role)
);

CREATE TABLE service_account_api_keys (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    service_account_id uuid NOT NULL REFERENCES service_accounts(id) ON DELETE CASCADE,
    key_prefix text NOT NULL UNIQUE CHECK (char_length(key_prefix) = 16),
    key_hash bytea NOT NULL UNIQUE CHECK (octet_length(key_hash) = 32),
    expires_at timestamptz,
    revoked_at timestamptz,
    last_used_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX service_account_api_keys_active_idx ON service_account_api_keys(key_prefix) WHERE revoked_at IS NULL;
CREATE INDEX service_accounts_tenant_idx ON service_accounts(tenant_id, created_at DESC);
