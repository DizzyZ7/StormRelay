package events

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/DizzyZ7/StormRelay/internal/id"
)

var ErrInvalidCloudEvent = errors.New("invalid CloudEvent")

type NormalizeInput struct {
	TenantID       string
	SourceID       string
	SourceName     string
	ContentType    string
	Body           []byte
	SourceEventID  string
	IdempotencyKey string
	TraceParent    string
	RequestID      string
	ReceivedAt     time.Time
}

func Normalize(input NormalizeInput) (Event, error) {
	if input.ReceivedAt.IsZero() {
		input.ReceivedAt = time.Now().UTC()
	}
	if input.TenantID == "" || input.SourceID == "" || len(input.Body) == 0 {
		return Event{}, fmt.Errorf("tenant, source and body are required")
	}

	if strings.Contains(strings.ToLower(input.ContentType), "application/cloudevents+json") {
		return normalizeStructuredCloudEvent(input)
	}
	return normalizeGeneric(input)
}

func normalizeStructuredCloudEvent(input NormalizeInput) (Event, error) {
	var ce CloudEvent
	if err := json.Unmarshal(input.Body, &ce); err != nil {
		return Event{}, fmt.Errorf("%w: decode: %v", ErrInvalidCloudEvent, err)
	}
	if ce.SpecVersion != "1.0" || ce.ID == "" || ce.Source == "" || ce.Type == "" {
		return Event{}, fmt.Errorf("%w: specversion, id, source and type are required", ErrInvalidCloudEvent)
	}
	when := input.ReceivedAt
	if ce.Time != nil {
		when = ce.Time.UTC()
	}
	severity := parseSeverity(ce.Severity)
	labels := cloneLabels(ce.Labels)
	fingerprint := Fingerprint(ce.Source, ce.Type, ce.Subject, labels, ce.Data)
	return Event{
		ID: ce.ID, Source: ce.Source, Type: ce.Type, Subject: ce.Subject,
		Time: when, DataContentType: defaultString(ce.DataContentType, "application/json"),
		SchemaVersion: SchemaVersion, TenantID: input.TenantID, SourceID: input.SourceID,
		TraceParent: input.TraceParent, Labels: labels, Severity: severity,
		RawPayload: append(json.RawMessage(nil), input.Body...), SourceEventID: ce.ID,
		IdempotencyKey: input.IdempotencyKey, Fingerprint: fingerprint,
		ReceivedAt: input.ReceivedAt, RequestID: input.RequestID,
	}, nil
}

func normalizeGeneric(input NormalizeInput) (Event, error) {
	if !json.Valid(input.Body) {
		return Event{}, fmt.Errorf("payload must be valid JSON")
	}
	var data map[string]any
	if err := json.Unmarshal(input.Body, &data); err != nil {
		return Event{}, err
	}
	eventID := strings.TrimSpace(input.SourceEventID)
	if eventID == "" {
		var err error
		eventID, err = id.New()
		if err != nil {
			return Event{}, err
		}
	}
	typeName := stringValue(data, "type", "event_type", "alert_type")
	if typeName == "" {
		typeName = "com.stormrelay.generic"
	}
	subject := stringValue(data, "subject", "alertname", "title")
	labels := extractLabels(data)
	severity := parseSeverity(stringValue(data, "severity", "level", "status"))
	fingerprint := Fingerprint(input.SourceName, typeName, subject, labels, input.Body)
	return Event{
		ID: eventID, Source: "urn:stormrelay:source:" + input.SourceID, Type: typeName,
		Subject: subject, Time: input.ReceivedAt, DataContentType: defaultString(input.ContentType, "application/json"),
		SchemaVersion: SchemaVersion, TenantID: input.TenantID, SourceID: input.SourceID,
		TraceParent: input.TraceParent, Labels: labels, Severity: severity,
		RawPayload: append(json.RawMessage(nil), input.Body...), SourceEventID: input.SourceEventID,
		IdempotencyKey: input.IdempotencyKey, Fingerprint: fingerprint,
		ReceivedAt: input.ReceivedAt, RequestID: input.RequestID,
	}, nil
}

func Fingerprint(source, eventType, subject string, labels map[string]string, raw []byte) string {
	h := sha256.New()
	_, _ = h.Write([]byte(source))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(eventType))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(subject))
	_, _ = h.Write([]byte{0})
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		_, _ = h.Write([]byte(k))
		_, _ = h.Write([]byte{'='})
		_, _ = h.Write([]byte(labels[k]))
		_, _ = h.Write([]byte{0})
	}
	if len(keys) == 0 && subject == "" {
		_, _ = h.Write(raw)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func DedupeKey(e Event) string {
	if e.IdempotencyKey != "" {
		return "idem:" + e.IdempotencyKey
	}
	if e.SourceEventID != "" {
		return "source:" + e.SourceEventID
	}
	return "fingerprint:" + e.Fingerprint
}

func CorrelationKey(e Event) string {
	parts := []string{e.TenantID, e.Labels["service"], e.Labels["environment"], e.Labels["resource"], e.Labels["alertname"]}
	if strings.Join(parts[1:], "") == "" {
		parts = append(parts, e.Fingerprint)
	}
	return strings.Join(parts, "|")
}

func parseSeverity(value string) Severity {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "critical", "fatal", "emergency":
		return SeverityCritical
	case "error", "high":
		return SeverityError
	case "warning", "warn", "medium":
		return SeverityWarning
	default:
		return SeverityInfo
	}
}

func extractLabels(data map[string]any) map[string]string {
	labels := map[string]string{}
	if raw, ok := data["labels"].(map[string]any); ok {
		for k, v := range raw {
			if s, ok := v.(string); ok && len(k) <= 64 && len(s) <= 256 {
				labels[k] = s
			}
		}
	}
	for _, key := range []string{"service", "environment", "resource", "alertname"} {
		if value := stringValue(data, key); value != "" {
			labels[key] = value
		}
	}
	return labels
}

func stringValue(data map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := data[key].(string); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
func cloneLabels(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
func defaultString(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
