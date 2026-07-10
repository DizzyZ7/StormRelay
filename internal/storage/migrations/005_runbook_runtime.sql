ALTER TABLE runbooks
    ADD COLUMN IF NOT EXISTS description text;

ALTER TABLE executions
    ADD COLUMN IF NOT EXISTS input_snapshot jsonb NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS paused_at timestamptz,
    ADD COLUMN IF NOT EXISTS last_error text;

ALTER TABLE execution_steps
    ADD COLUMN IF NOT EXISTS position integer,
    ADD COLUMN IF NOT EXISTS name text,
    ADD COLUMN IF NOT EXISTS next_attempt_at timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN IF NOT EXISTS wait_until timestamptz,
    ADD COLUMN IF NOT EXISTS rollback_definition jsonb,
    ADD COLUMN IF NOT EXISTS is_rollback boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS rollback_of uuid REFERENCES execution_steps(id);

UPDATE execution_steps SET position = 0 WHERE position IS NULL;
ALTER TABLE execution_steps ALTER COLUMN position SET NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_execution_steps_position
    ON execution_steps(execution_id, position, is_rollback)
    WHERE is_rollback = false;
CREATE INDEX IF NOT EXISTS idx_execution_steps_runnable
    ON execution_steps(status, next_attempt_at, position);
CREATE INDEX IF NOT EXISTS idx_execution_steps_lease
    ON execution_steps(status, lease_expires_at)
    WHERE lease_expires_at IS NOT NULL;
ALTER TABLE approvals
    ADD COLUMN IF NOT EXISTS prompt text;

CREATE UNIQUE INDEX IF NOT EXISTS idx_approvals_step
    ON approvals(execution_step_id);
CREATE INDEX IF NOT EXISTS idx_approvals_pending
    ON approvals(tenant_id, status, expires_at);

ALTER TABLE plugins
    ADD COLUMN IF NOT EXISTS auth_mode text NOT NULL DEFAULT 'none',
    ADD COLUMN IF NOT EXISTS encrypted_secret bytea,
    ADD COLUMN IF NOT EXISTS bearer_hash bytea,
    ADD COLUMN IF NOT EXISTS manifest_hash bytea,
    ADD COLUMN IF NOT EXISTS last_seen_at timestamptz;

ALTER TABLE executions DROP CONSTRAINT IF EXISTS executions_status_check;
ALTER TABLE executions ADD CONSTRAINT executions_status_check CHECK (
    status IN ('running','waiting_approval','paused','canceling','canceled','completed','failed','ambiguous','rolling_back','rolled_back')
);

ALTER TABLE execution_steps DROP CONSTRAINT IF EXISTS execution_steps_status_check;
ALTER TABLE execution_steps ADD CONSTRAINT execution_steps_status_check CHECK (
    status IN ('pending','running','waiting','waiting_approval','retrying','completed','skipped','failed','ambiguous','canceled','rolling_back','rolled_back')
);
