package events

import (
	"encoding/json"
	"time"
)

const SchemaVersion = "1.0"

type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityError    Severity = "error"
	SeverityCritical Severity = "critical"
)

type Event struct {
	ID              string            `json:"id"`
	Source          string            `json:"source"`
	Type            string            `json:"type"`
	Subject         string            `json:"subject,omitempty"`
	Time            time.Time         `json:"time"`
	DataContentType string            `json:"datacontenttype"`
	SchemaVersion   string            `json:"schema_version"`
	TenantID        string            `json:"tenant_id"`
	SourceID        string            `json:"source_id"`
	TraceParent     string            `json:"traceparent,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
	Severity        Severity          `json:"severity"`
	RawPayload      json.RawMessage   `json:"raw_payload"`
	SourceEventID   string            `json:"source_event_id,omitempty"`
	IdempotencyKey  string            `json:"idempotency_key,omitempty"`
	Fingerprint     string            `json:"fingerprint"`
	ReceivedAt      time.Time         `json:"received_at"`
	RequestID       string            `json:"request_id"`
}

type CloudEvent struct {
	SpecVersion     string            `json:"specversion"`
	ID              string            `json:"id"`
	Source          string            `json:"source"`
	Type            string            `json:"type"`
	Subject         string            `json:"subject,omitempty"`
	Time            *time.Time        `json:"time,omitempty"`
	DataContentType string            `json:"datacontenttype,omitempty"`
	Data            json.RawMessage   `json:"data"`
	Severity        string            `json:"severity,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
}
