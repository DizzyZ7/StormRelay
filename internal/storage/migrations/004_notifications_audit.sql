CREATE TABLE IF NOT EXISTS notification_channels (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    channel_key text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('mock','telegram','webhook','email')),
    config jsonb NOT NULL,
    encrypted_secret bytea,
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version bigint NOT NULL DEFAULT 1,
    UNIQUE (tenant_id, channel_key)
);

CREATE TABLE IF NOT EXISTS delivery_attempts (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    incident_id uuid NOT NULL REFERENCES incidents(id),
    channel_id uuid REFERENCES notification_channels(id),
    channel_kind text NOT NULL,
    dedupe_key text NOT NULL,
    status text NOT NULL CHECK (status IN ('pending','delivering','delivered','failed','ambiguous')),
    attempt integer NOT NULL DEFAULT 0,
    payload jsonb NOT NULL,
    ack_token_ciphertext bytea,
    provider_reference text,
    sanitized_error text,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    lease_expires_at timestamptz,
    delivered_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, dedupe_key)
);
CREATE INDEX IF NOT EXISTS idx_delivery_pending ON delivery_attempts(status, next_attempt_at);

CREATE TABLE IF NOT EXISTS audit_entries (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    actor_type text NOT NULL,
    actor_id text NOT NULL,
    action text NOT NULL,
    resource_type text NOT NULL,
    resource_id text NOT NULL,
    request_id text,
    trace_id text,
    before_hash bytea,
    after_hash bytea,
    metadata jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_audit_tenant_created ON audit_entries(tenant_id, created_at DESC, id DESC);

CREATE OR REPLACE FUNCTION prevent_audit_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'audit_entries is append-only';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS audit_entries_no_update ON audit_entries;
CREATE TRIGGER audit_entries_no_update BEFORE UPDATE OR DELETE ON audit_entries
FOR EACH ROW EXECUTE FUNCTION prevent_audit_mutation();

INSERT INTO notification_channels(id, tenant_id, channel_key, kind, config)
VALUES ('00000000-0000-4000-8000-000000000010','00000000-0000-4000-8000-000000000001','local-mock','mock','{}')
ON CONFLICT (id) DO NOTHING;
