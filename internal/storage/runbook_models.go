package storage

import (
	"encoding/json"
	"time"
)

type Runbook struct {
	ID            string    `json:"id"`
	TenantID      string    `json:"tenant_id"`
	RunbookKey    string    `json:"runbook_key"`
	Enabled       bool      `json:"enabled"`
	ActiveVersion int       `json:"active_version"`
	Description   string    `json:"description,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	Version       int64     `json:"version"`
}

type Execution struct {
	ID               string          `json:"id"`
	TenantID         string          `json:"tenant_id"`
	IncidentID       string          `json:"incident_id,omitempty"`
	RunbookVersionID string          `json:"runbook_version_id"`
	RunbookKey       string          `json:"runbook_key,omitempty"`
	RunbookVersion   int             `json:"runbook_version,omitempty"`
	Status           string          `json:"status"`
	DryRun           bool            `json:"dry_run"`
	RequestedBy      string          `json:"requested_by"`
	InputSnapshot    json.RawMessage `json:"input_snapshot"`
	StartedAt        *time.Time      `json:"started_at,omitempty"`
	FinishedAt       *time.Time      `json:"finished_at,omitempty"`
	PausedAt         *time.Time      `json:"paused_at,omitempty"`
	CancelRequested  *time.Time      `json:"cancel_requested_at,omitempty"`
	LastError        string          `json:"last_error,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
	Version          int64           `json:"version"`
	Steps            []ExecutionStep `json:"steps,omitempty"`
}

type ExecutionStep struct {
	ID                 string          `json:"id"`
	ExecutionID        string          `json:"execution_id"`
	Position           int             `json:"position"`
	StepKey            string          `json:"step_key"`
	Name               string          `json:"name,omitempty"`
	StepType           string          `json:"step_type"`
	ImmutableInput     json.RawMessage `json:"immutable_input"`
	Status             string          `json:"status"`
	TimeoutSeconds     int             `json:"timeout_seconds"`
	RetryPolicy        json.RawMessage `json:"retry_policy"`
	AttemptCount       int             `json:"attempt_count"`
	Output             json.RawMessage `json:"output,omitempty"`
	SanitizedError     string          `json:"sanitized_error,omitempty"`
	CorrelationID      string          `json:"correlation_id"`
	IdempotencyKey     string          `json:"idempotency_key"`
	StartedAt          *time.Time      `json:"started_at,omitempty"`
	FinishedAt         *time.Time      `json:"finished_at,omitempty"`
	NextAttemptAt      time.Time       `json:"next_attempt_at"`
	WaitUntil          *time.Time      `json:"wait_until,omitempty"`
	LeaseOwner         string          `json:"lease_owner,omitempty"`
	LeaseExpiresAt     *time.Time      `json:"lease_expires_at,omitempty"`
	RollbackDefinition json.RawMessage `json:"rollback_definition,omitempty"`
	IsRollback         bool            `json:"is_rollback"`
	RollbackOf         string          `json:"rollback_of,omitempty"`
	Version            int64           `json:"version"`
}

type Approval struct {
	ID              string     `json:"id"`
	TenantID        string     `json:"tenant_id"`
	ExecutionStepID string     `json:"execution_step_id"`
	ExecutionID     string     `json:"execution_id,omitempty"`
	StepKey         string     `json:"step_key,omitempty"`
	Prompt          string     `json:"prompt,omitempty"`
	Status          string     `json:"status"`
	RequestedAt     time.Time  `json:"requested_at"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"`
	DecidedAt       *time.Time `json:"decided_at,omitempty"`
	DecidedBy       string     `json:"decided_by,omitempty"`
	Reason          string     `json:"reason,omitempty"`
}

type Plugin struct {
	ID              string          `json:"id"`
	TenantID        string          `json:"tenant_id"`
	PluginKey       string          `json:"plugin_key"`
	Endpoint        string          `json:"endpoint"`
	Enabled         bool            `json:"enabled"`
	ActiveVersion   string          `json:"active_version"`
	ProtocolVersion string          `json:"protocol_version"`
	Permissions     json.RawMessage `json:"permissions"`
	TimeoutSeconds  int             `json:"timeout_seconds"`
	RetryPolicy     json.RawMessage `json:"retry_policy"`
	Actions         json.RawMessage `json:"actions"`
	AuthMode        string          `json:"auth_mode"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	LastSeenAt      *time.Time      `json:"last_seen_at,omitempty"`
	Version         int64           `json:"version"`
}

type ClaimedStep struct {
	Step           ExecutionStep   `json:"step"`
	TenantID       string          `json:"tenant_id"`
	IncidentID     string          `json:"incident_id,omitempty"`
	DryRun         bool            `json:"dry_run"`
	ExecutionInput json.RawMessage `json:"execution_input"`
}
