package storage

import (
	"encoding/json"
	"time"

	"github.com/DizzyZ7/StormRelay/internal/events"
	"github.com/DizzyZ7/StormRelay/internal/incidents"
	"github.com/DizzyZ7/StormRelay/internal/ingestion"
)

type Source struct {
	ID                 string             `json:"id"`
	TenantID           string             `json:"tenant_id"`
	Name               string             `json:"name"`
	Kind               string             `json:"kind"`
	AuthMode           ingestion.AuthMode `json:"auth_mode"`
	Enabled            bool               `json:"enabled"`
	RateLimitPerSecond int                `json:"rate_limit_per_second"`
	RateLimitBurst     int                `json:"rate_limit_burst"`
	CreatedAt          time.Time          `json:"created_at"`
	Version            int64              `json:"version"`
}
type SourceCredentials struct {
	Source     Source
	HMACSecret []byte
	BearerHash []byte
}
type CreateSourceInput struct {
	TenantID, Name, Kind               string
	AuthMode                           ingestion.AuthMode
	RateLimitPerSecond, RateLimitBurst int
}
type CreateSourceResult struct {
	Source     Source `json:"source"`
	Credential string `json:"credential,omitempty"`
}

type Incident struct {
	ID             string          `json:"id"`
	TenantID       string          `json:"tenant_id"`
	CorrelationKey string          `json:"correlation_key"`
	Title          string          `json:"title"`
	State          incidents.State `json:"state"`
	Severity       events.Severity `json:"severity"`
	Service        string          `json:"service,omitempty"`
	Environment    string          `json:"environment,omitempty"`
	FirstEventAt   time.Time       `json:"first_event_at"`
	LastEventAt    time.Time       `json:"last_event_at"`
	AcknowledgedAt *time.Time      `json:"acknowledged_at,omitempty"`
	ResolvedAt     *time.Time      `json:"resolved_at,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	Version        int64           `json:"version"`
	DuplicateCount int64           `json:"duplicate_count,omitempty"`
	EventCount     int64           `json:"event_count,omitempty"`
}
type ProcessResult struct {
	EventID         string
	Incident        Incident
	Duplicate       bool
	DuplicateNumber int64
	NotificationIDs []string
}
type IncidentFilter struct {
	TenantID   string
	State      string
	Severity   string
	Service    string
	Limit      int
	CursorTime *time.Time
	CursorID   string
}
type EventRecord struct {
	ID             string            `json:"id"`
	CloudEventID   string            `json:"cloudevent_id"`
	SourceID       string            `json:"source_id"`
	Type           string            `json:"type"`
	Subject        string            `json:"subject,omitempty"`
	Severity       events.Severity   `json:"severity"`
	Labels         map[string]string `json:"labels"`
	EventTime      time.Time         `json:"event_time"`
	DuplicateCount int64             `json:"duplicate_count"`
	Fingerprint    string            `json:"fingerprint"`
}

type PolicyRecord struct {
	ID, PolicyKey, DocumentYAML string
	Version                     int
}
type Delivery struct {
	ID, TenantID, IncidentID, ChannelID, Kind, DedupeKey string
	Payload                                              json.RawMessage
	Config                                               json.RawMessage
	Attempt                                              int
}
type AuditEntry struct {
	ID, TenantID, ActorType, ActorID, Action, ResourceType, ResourceID, RequestID, TraceID string
	BeforeHash, AfterHash                                                                  []byte
	Metadata                                                                               json.RawMessage
	CreatedAt                                                                              time.Time
}
