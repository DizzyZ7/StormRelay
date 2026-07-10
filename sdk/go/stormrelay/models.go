package stormrelay

import (
	"encoding/json"
	"time"
)

type Version struct {
	Version     string `json:"version"`
	API         string `json:"api"`
	EventSchema string `json:"event_schema"`
}

type Incident struct {
	ID             string     `json:"id"`
	Title          string     `json:"title"`
	State          string     `json:"state"`
	Severity       string     `json:"severity"`
	Service        string     `json:"service,omitempty"`
	Environment    string     `json:"environment,omitempty"`
	FirstEventAt   time.Time  `json:"first_event_at"`
	LastEventAt    time.Time  `json:"last_event_at"`
	AcknowledgedAt *time.Time `json:"acknowledged_at,omitempty"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
	Version        int64      `json:"version"`
}

type IncidentList struct {
	Items      []Incident `json:"items"`
	NextCursor string     `json:"next_cursor"`
}

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

type ExecutionStep struct {
	ID             string          `json:"id"`
	Position       int             `json:"position"`
	StepKey        string          `json:"step_key"`
	Name           string          `json:"name,omitempty"`
	StepType       string          `json:"step_type"`
	Status         string          `json:"status"`
	AttemptCount   int             `json:"attempt_count"`
	Output         json.RawMessage `json:"output,omitempty"`
	SanitizedError string          `json:"sanitized_error,omitempty"`
	IdempotencyKey string          `json:"idempotency_key"`
}

type Execution struct {
	ID             string          `json:"id"`
	TenantID       string          `json:"tenant_id"`
	IncidentID     string          `json:"incident_id,omitempty"`
	RunbookKey     string          `json:"runbook_key,omitempty"`
	RunbookVersion int             `json:"runbook_version,omitempty"`
	Status         string          `json:"status"`
	DryRun         bool            `json:"dry_run"`
	RequestedBy    string          `json:"requested_by"`
	InputSnapshot  json.RawMessage `json:"input_snapshot"`
	StartedAt      *time.Time      `json:"started_at,omitempty"`
	FinishedAt     *time.Time      `json:"finished_at,omitempty"`
	LastError      string          `json:"last_error,omitempty"`
	Version        int64           `json:"version"`
	Steps          []ExecutionStep `json:"steps,omitempty"`
}

type Approval struct {
	ID              string     `json:"id"`
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

type ServiceAccount struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	Name      string    `json:"name"`
	Enabled   bool      `json:"enabled"`
	Roles     []string  `json:"roles"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Version   int64     `json:"version"`
}

type ServiceAccountKey struct {
	ID               string     `json:"id"`
	TenantID         string     `json:"tenant_id"`
	ServiceAccountID string     `json:"service_account_id"`
	KeyPrefix        string     `json:"key_prefix"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
	RevokedAt        *time.Time `json:"revoked_at,omitempty"`
	LastUsedAt       *time.Time `json:"last_used_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

type CreatedServiceAccountKey struct {
	Key        ServiceAccountKey `json:"key"`
	Credential string            `json:"credential"`
}
