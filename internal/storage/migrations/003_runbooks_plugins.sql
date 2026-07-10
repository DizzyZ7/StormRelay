CREATE TABLE IF NOT EXISTS runbooks (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    runbook_key text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    active_version integer NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version bigint NOT NULL DEFAULT 1,
    UNIQUE (tenant_id, runbook_key)
);

CREATE TABLE IF NOT EXISTS runbook_versions (
    id uuid PRIMARY KEY,
    runbook_id uuid NOT NULL REFERENCES runbooks(id),
    version integer NOT NULL,
    document_yaml text NOT NULL,
    document_hash bytea NOT NULL,
    created_by text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (runbook_id, version)
);

CREATE TABLE IF NOT EXISTS executions (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    incident_id uuid REFERENCES incidents(id),
    runbook_version_id uuid NOT NULL REFERENCES runbook_versions(id),
    status text NOT NULL,
    dry_run boolean NOT NULL DEFAULT false,
    requested_by text NOT NULL,
    started_at timestamptz,
    finished_at timestamptz,
    cancel_requested_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version bigint NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS execution_steps (
    id uuid PRIMARY KEY,
    execution_id uuid NOT NULL REFERENCES executions(id),
    step_key text NOT NULL,
    step_type text NOT NULL,
    immutable_input jsonb NOT NULL,
    status text NOT NULL,
    timeout_seconds integer NOT NULL,
    retry_policy jsonb NOT NULL,
    attempt_count integer NOT NULL DEFAULT 0,
    output jsonb,
    sanitized_error text,
    correlation_id text NOT NULL,
    idempotency_key text NOT NULL,
    started_at timestamptz,
    finished_at timestamptz,
    lease_owner text,
    lease_expires_at timestamptz,
    version bigint NOT NULL DEFAULT 1,
    UNIQUE (execution_id, step_key),
    UNIQUE (idempotency_key)
);

CREATE TABLE IF NOT EXISTS approvals (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    execution_step_id uuid NOT NULL REFERENCES execution_steps(id),
    status text NOT NULL CHECK (status IN ('pending','approved','rejected','expired')),
    requested_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz,
    decided_at timestamptz,
    decided_by text,
    reason text
);

CREATE TABLE IF NOT EXISTS plugins (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    plugin_key text NOT NULL,
    endpoint text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    active_version text NOT NULL,
    permissions jsonb NOT NULL,
    timeout_seconds integer NOT NULL,
    retry_policy jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version bigint NOT NULL DEFAULT 1,
    UNIQUE (tenant_id, plugin_key)
);

CREATE TABLE IF NOT EXISTS plugin_versions (
    id uuid PRIMARY KEY,
    plugin_id uuid NOT NULL REFERENCES plugins(id),
    version text NOT NULL,
    protocol_version text NOT NULL,
    actions jsonb NOT NULL,
    discovered_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (plugin_id, version)
);
