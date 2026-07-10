CREATE TABLE IF NOT EXISTS incidents (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    correlation_key text NOT NULL,
    title text NOT NULL,
    state text NOT NULL CHECK (state IN ('detected','acknowledged','investigating','mitigated','resolved','closed','reopened')),
    severity text NOT NULL CHECK (severity IN ('info','warning','error','critical')),
    service text,
    environment text,
    assigned_team_id uuid REFERENCES teams(id),
    first_event_at timestamptz NOT NULL,
    last_event_at timestamptz NOT NULL,
    acknowledged_at timestamptz,
    resolved_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version bigint NOT NULL DEFAULT 1
);
CREATE INDEX IF NOT EXISTS idx_incidents_open ON incidents(tenant_id, state, last_event_at DESC);
CREATE INDEX IF NOT EXISTS idx_incidents_correlation ON incidents(tenant_id, correlation_key, last_event_at DESC);

CREATE TABLE IF NOT EXISTS incident_events (
    incident_id uuid NOT NULL REFERENCES incidents(id),
    event_id uuid NOT NULL REFERENCES normalized_events(id),
    attached_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (incident_id, event_id)
);

CREATE TABLE IF NOT EXISTS incident_transitions (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    incident_id uuid NOT NULL REFERENCES incidents(id),
    from_state text,
    to_state text NOT NULL,
    actor_type text NOT NULL,
    actor_id text NOT NULL,
    reason text,
    request_id text,
    trace_id text,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_incident_transitions_incident ON incident_transitions(incident_id, created_at);

CREATE TABLE IF NOT EXISTS incident_ack_tokens (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    incident_id uuid NOT NULL REFERENCES incidents(id),
    token_hash bytea NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    used_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS policies (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    policy_key text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    active_version integer NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version bigint NOT NULL DEFAULT 1,
    UNIQUE (tenant_id, policy_key)
);

CREATE TABLE IF NOT EXISTS policy_versions (
    id uuid PRIMARY KEY,
    policy_id uuid NOT NULL REFERENCES policies(id),
    version integer NOT NULL,
    document_yaml text NOT NULL,
    document_hash bytea NOT NULL,
    created_by text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (policy_id, version)
);
