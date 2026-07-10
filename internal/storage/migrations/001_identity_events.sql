
CREATE TABLE IF NOT EXISTS schema_migrations (
    version bigint PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS tenants (
    id uuid PRIMARY KEY,
    slug text NOT NULL UNIQUE,
    name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    version bigint NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS users (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    email text NOT NULL,
    display_name text NOT NULL,
    oidc_subject text,
    created_at timestamptz NOT NULL DEFAULT now(),
    version bigint NOT NULL DEFAULT 1,
    UNIQUE (tenant_id, email),
    UNIQUE (tenant_id, oidc_subject)
);

CREATE TABLE IF NOT EXISTS teams (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    slug text NOT NULL,
    name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    version bigint NOT NULL DEFAULT 1,
    UNIQUE (tenant_id, slug)
);

CREATE TABLE IF NOT EXISTS memberships (
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    user_id uuid NOT NULL REFERENCES users(id),
    team_id uuid REFERENCES teams(id),
    role text NOT NULL CHECK (role IN ('viewer','operator','responder','runbook-editor','integration-admin','tenant-admin')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, user_id, team_id, role)
);

CREATE TABLE IF NOT EXISTS api_keys (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    name text NOT NULL,
    key_prefix text NOT NULL,
    key_hash bytea NOT NULL,
    permissions jsonb NOT NULL DEFAULT '[]',
    expires_at timestamptz,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, key_prefix)
);

CREATE TABLE IF NOT EXISTS event_sources (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    name text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('generic','cloudevents','alertmanager','grafana','sentry','github')),
    auth_mode text NOT NULL CHECK (auth_mode IN ('none','hmac-sha256','bearer')),
    encrypted_secret bytea,
    bearer_hash bytea,
    enabled boolean NOT NULL DEFAULT true,
    rate_limit_per_second integer NOT NULL DEFAULT 20 CHECK (rate_limit_per_second > 0),
    rate_limit_burst integer NOT NULL DEFAULT 40 CHECK (rate_limit_burst > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version bigint NOT NULL DEFAULT 1,
    UNIQUE (tenant_id, name)
);
CREATE INDEX IF NOT EXISTS idx_event_sources_tenant ON event_sources(tenant_id, created_at DESC);

CREATE TABLE IF NOT EXISTS raw_events (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    source_id uuid NOT NULL REFERENCES event_sources(id),
    content_type text NOT NULL,
    payload bytea NOT NULL,
    payload_sha256 bytea NOT NULL,
    received_at timestamptz NOT NULL,
    request_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_raw_events_tenant_received ON raw_events(tenant_id, received_at DESC);

CREATE TABLE IF NOT EXISTS normalized_events (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    source_id uuid NOT NULL REFERENCES event_sources(id),
    raw_event_id uuid NOT NULL REFERENCES raw_events(id),
    ce_id text NOT NULL,
    source_event_id text,
    idempotency_key text,
    dedupe_key text NOT NULL,
    dedupe_bucket timestamptz NOT NULL,
    fingerprint text NOT NULL,
    ce_source text NOT NULL,
    ce_type text NOT NULL,
    subject text,
    event_time timestamptz NOT NULL,
    data_content_type text NOT NULL,
    schema_version text NOT NULL,
    trace_parent text,
    labels jsonb NOT NULL DEFAULT '{}',
    severity text NOT NULL CHECK (severity IN ('info','warning','error','critical')),
    duplicate_count bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, source_id, dedupe_key, dedupe_bucket)
);
CREATE INDEX IF NOT EXISTS idx_normalized_events_tenant_time ON normalized_events(tenant_id, event_time DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_normalized_events_fingerprint ON normalized_events(tenant_id, fingerprint, event_time DESC);
CREATE INDEX IF NOT EXISTS idx_normalized_events_labels ON normalized_events USING gin(labels);

CREATE TABLE IF NOT EXISTS event_duplicates (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    canonical_event_id uuid NOT NULL REFERENCES normalized_events(id),
    raw_event_id uuid NOT NULL REFERENCES raw_events(id),
    duplicate_number bigint NOT NULL,
    reason text NOT NULL,
    received_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (canonical_event_id, raw_event_id)
);
CREATE INDEX IF NOT EXISTS idx_event_duplicates_canonical ON event_duplicates(canonical_event_id, created_at DESC);

INSERT INTO tenants(id, slug, name)
VALUES ('00000000-0000-4000-8000-000000000001','default','Default tenant')
ON CONFLICT (id) DO NOTHING;

INSERT INTO event_sources(id, tenant_id, name, kind, auth_mode, enabled, rate_limit_per_second, rate_limit_burst)
VALUES ('00000000-0000-4000-8000-000000000020','00000000-0000-4000-8000-000000000001','manual-api','generic','none',true,20,40)
ON CONFLICT (id) DO NOTHING;
