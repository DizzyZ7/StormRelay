package storage

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/DizzyZ7/StormRelay/internal/id"
	"github.com/DizzyZ7/StormRelay/internal/incidents"
	"github.com/DizzyZ7/StormRelay/internal/telemetry"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/trace"
)

func (s *Store) ListIncidents(ctx context.Context, f IncidentFilter) ([]Incident, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	cursorTime := time.Now().UTC().Add(100 * 365 * 24 * time.Hour)
	if f.CursorTime != nil {
		cursorTime = *f.CursorTime
	}
	cursorID := "ffffffff-ffff-ffff-ffff-ffffffffffff"
	if f.CursorID != "" {
		cursorID = f.CursorID
	}
	rows, err := s.pool.Query(ctx, `SELECT i.id,i.tenant_id,i.correlation_key,i.title,i.state,i.severity,COALESCE(i.service,''),COALESCE(i.environment,''),i.first_event_at,i.last_event_at,i.acknowledged_at,i.resolved_at,i.created_at,i.updated_at,i.version,COALESCE(SUM(e.duplicate_count),0),COUNT(ie.event_id)
	FROM incidents i LEFT JOIN incident_events ie ON ie.incident_id=i.id LEFT JOIN normalized_events e ON e.id=ie.event_id
	WHERE i.tenant_id=$1 AND ($2='' OR i.state=$2) AND ($3='' OR i.severity=$3) AND ($4='' OR i.service=$4) AND (i.last_event_at,i.id)<($5,$6)
	GROUP BY i.id ORDER BY i.last_event_at DESC,i.id DESC LIMIT $7`, f.TenantID, f.State, f.Severity, f.Service, cursorTime, cursorID, f.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Incident{}
	for rows.Next() {
		var x Incident
		if err := scanIncidentWithCounts(rows, &x); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) GetIncident(ctx context.Context, tenantID, incidentID string) (Incident, error) {
	var x Incident
	err := s.pool.QueryRow(ctx, `SELECT i.id,i.tenant_id,i.correlation_key,i.title,i.state,i.severity,COALESCE(i.service,''),COALESCE(i.environment,''),i.first_event_at,i.last_event_at,i.acknowledged_at,i.resolved_at,i.created_at,i.updated_at,i.version,COALESCE(SUM(e.duplicate_count),0),COUNT(ie.event_id)
	FROM incidents i LEFT JOIN incident_events ie ON ie.incident_id=i.id LEFT JOIN normalized_events e ON e.id=ie.event_id WHERE i.tenant_id=$1 AND i.id=$2 GROUP BY i.id`, tenantID, incidentID).Scan(&x.ID, &x.TenantID, &x.CorrelationKey, &x.Title, &x.State, &x.Severity, &x.Service, &x.Environment, &x.FirstEventAt, &x.LastEventAt, &x.AcknowledgedAt, &x.ResolvedAt, &x.CreatedAt, &x.UpdatedAt, &x.Version, &x.DuplicateCount, &x.EventCount)
	if err == pgx.ErrNoRows {
		return Incident{}, errNoRows
	}
	return x, err
}

func (s *Store) ListIncidentEvents(ctx context.Context, tenantID, incidentID string) ([]EventRecord, error) {
	rows, err := s.pool.Query(ctx, `SELECT e.id,e.ce_id,e.source_id,e.ce_type,COALESCE(e.subject,''),e.severity,e.labels,e.event_time,e.duplicate_count,e.fingerprint FROM normalized_events e JOIN incident_events ie ON ie.event_id=e.id JOIN incidents i ON i.id=ie.incident_id WHERE i.tenant_id=$1 AND i.id=$2 ORDER BY e.event_time,e.id`, tenantID, incidentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EventRecord{}
	for rows.Next() {
		var x EventRecord
		if err := rows.Scan(&x.ID, &x.CloudEventID, &x.SourceID, &x.Type, &x.Subject, &x.Severity, &x.Labels, &x.EventTime, &x.DuplicateCount, &x.Fingerprint); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

type TransitionInput struct {
	TenantID, IncidentID                           string
	To                                             incidents.State
	ExpectedVersion                                int64
	ActorType, ActorID, Reason, RequestID, TraceID string
}

func (s *Store) TransitionIncident(ctx context.Context, in TransitionInput) (out Incident, err error) {
	transitionCtx, span := telemetry.StartOperationSpan(ctx, "stormrelay.incident.transition", trace.SpanKindInternal,
		telemetry.StringAttribute("stormrelay.incident.to_state", string(in.To)),
		telemetry.StringAttribute("stormrelay.incident.method", "api"),
	)
	defer func() {
		if err != nil {
			telemetry.MarkSpanError(span)
			telemetry.SetSpanOutcome(span, "failed")
		} else {
			telemetry.SetSpanOutcome(span, "committed")
		}
		span.End()
	}()

	tx, err := s.pool.Begin(transitionCtx)
	if err != nil {
		return Incident{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var current Incident
	err = tx.QueryRow(transitionCtx, `SELECT id,tenant_id,correlation_key,title,state,severity,COALESCE(service,''),COALESCE(environment,''),first_event_at,last_event_at,acknowledged_at,resolved_at,created_at,updated_at,version FROM incidents WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, in.TenantID, in.IncidentID).Scan(&current.ID, &current.TenantID, &current.CorrelationKey, &current.Title, &current.State, &current.Severity, &current.Service, &current.Environment, &current.FirstEventAt, &current.LastEventAt, &current.AcknowledgedAt, &current.ResolvedAt, &current.CreatedAt, &current.UpdatedAt, &current.Version)
	if err == pgx.ErrNoRows {
		return Incident{}, errNoRows
	}
	if err != nil {
		return Incident{}, err
	}
	span.SetAttributes(telemetry.StringAttribute("stormrelay.incident.from_state", string(current.State)))
	if in.ExpectedVersion > 0 && in.ExpectedVersion != current.Version {
		return Incident{}, fmt.Errorf("version conflict: current=%d", current.Version)
	}
	if err := incidents.ValidateTransition(current.State, in.To); err != nil {
		return Incident{}, err
	}
	before := current
	transitionID, err := id.New()
	if err != nil {
		return Incident{}, err
	}
	var ack, resolved *time.Time
	now := time.Now().UTC()
	ack = current.AcknowledgedAt
	resolved = current.ResolvedAt
	if in.To == incidents.Acknowledged {
		ack = &now
	}
	if in.To == incidents.Resolved {
		resolved = &now
	}
	if in.To == incidents.Reopened {
		resolved = nil
	}
	err = tx.QueryRow(transitionCtx, `UPDATE incidents SET state=$2,acknowledged_at=$3,resolved_at=$4,updated_at=now(),version=version+1 WHERE id=$1 RETURNING id,tenant_id,correlation_key,title,state,severity,COALESCE(service,''),COALESCE(environment,''),first_event_at,last_event_at,acknowledged_at,resolved_at,created_at,updated_at,version`, current.ID, in.To, ack, resolved).Scan(&current.ID, &current.TenantID, &current.CorrelationKey, &current.Title, &current.State, &current.Severity, &current.Service, &current.Environment, &current.FirstEventAt, &current.LastEventAt, &current.AcknowledgedAt, &current.ResolvedAt, &current.CreatedAt, &current.UpdatedAt, &current.Version)
	if err != nil {
		return Incident{}, err
	}
	_, err = tx.Exec(transitionCtx, `INSERT INTO incident_transitions(id,tenant_id,incident_id,from_state,to_state,actor_type,actor_id,reason,request_id,trace_id) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),NULLIF($9,''),NULLIF($10,''))`, transitionID, in.TenantID, in.IncidentID, before.State, in.To, defaultText(in.ActorType, "user"), defaultText(in.ActorID, "unknown"), in.Reason, in.RequestID, in.TraceID)
	if err != nil {
		return Incident{}, err
	}
	if err := appendAudit(transitionCtx, tx, AuditInput{TenantID: in.TenantID, ActorType: defaultText(in.ActorType, "user"), ActorID: defaultText(in.ActorID, "unknown"), Action: "incident.transition", ResourceType: "incident", ResourceID: in.IncidentID, RequestID: in.RequestID, TraceID: in.TraceID, Before: before, After: current, Metadata: map[string]any{"from": before.State, "to": in.To, "reason": in.Reason}}); err != nil {
		return Incident{}, err
	}
	if err := tx.Commit(transitionCtx); err != nil {
		return Incident{}, err
	}
	return current, nil
}

func (s *Store) AcknowledgeByToken(ctx context.Context, rawToken, requestID, traceID string) (out Incident, err error) {
	ackCtx, span := telemetry.StartOperationSpan(ctx, "stormrelay.incident.transition", trace.SpanKindInternal,
		telemetry.StringAttribute("stormrelay.incident.to_state", string(incidents.Acknowledged)),
		telemetry.StringAttribute("stormrelay.incident.method", "ack_token"),
	)
	defer func() {
		if err != nil {
			telemetry.MarkSpanError(span)
			telemetry.SetSpanOutcome(span, "failed")
		} else {
			telemetry.SetSpanOutcome(span, "committed")
		}
		span.End()
	}()

	hash := sha256.Sum256([]byte(rawToken))
	tx, err := s.pool.Begin(ackCtx)
	if err != nil {
		return Incident{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var tokenID, tenantID, incidentID string
	var usedAt *time.Time
	err = tx.QueryRow(ackCtx, `SELECT id,tenant_id,incident_id,used_at FROM incident_ack_tokens WHERE token_hash=$1 AND expires_at>now() FOR UPDATE`, hash[:]).Scan(&tokenID, &tenantID, &incidentID, &usedAt)
	if err == pgx.ErrNoRows {
		return Incident{}, errNoRows
	}
	if err != nil {
		return Incident{}, err
	}
	var current Incident
	err = tx.QueryRow(ackCtx, `SELECT id,tenant_id,correlation_key,title,state,severity,COALESCE(service,''),COALESCE(environment,''),first_event_at,last_event_at,acknowledged_at,resolved_at,created_at,updated_at,version FROM incidents WHERE id=$1 FOR UPDATE`, incidentID).Scan(&current.ID, &current.TenantID, &current.CorrelationKey, &current.Title, &current.State, &current.Severity, &current.Service, &current.Environment, &current.FirstEventAt, &current.LastEventAt, &current.AcknowledgedAt, &current.ResolvedAt, &current.CreatedAt, &current.UpdatedAt, &current.Version)
	if err != nil {
		return Incident{}, err
	}
	span.SetAttributes(telemetry.StringAttribute("stormrelay.incident.from_state", string(current.State)))
	if usedAt != nil || current.State == incidents.Acknowledged || current.State == incidents.Investigating {
		telemetry.SetSpanOutcome(span, "already_acknowledged")
		return current, tx.Commit(ackCtx)
	}
	if err := incidents.ValidateTransition(current.State, incidents.Acknowledged); err != nil {
		return Incident{}, err
	}
	before := current
	now := time.Now().UTC()
	transitionID, err := id.New()
	if err != nil {
		return Incident{}, err
	}
	_, err = tx.Exec(ackCtx, `UPDATE incident_ack_tokens SET used_at=$2 WHERE id=$1`, tokenID, now)
	if err != nil {
		return Incident{}, err
	}
	err = tx.QueryRow(ackCtx, `UPDATE incidents SET state='acknowledged',acknowledged_at=$2,updated_at=now(),version=version+1 WHERE id=$1 RETURNING id,tenant_id,correlation_key,title,state,severity,COALESCE(service,''),COALESCE(environment,''),first_event_at,last_event_at,acknowledged_at,resolved_at,created_at,updated_at,version`, incidentID, now).Scan(&current.ID, &current.TenantID, &current.CorrelationKey, &current.Title, &current.State, &current.Severity, &current.Service, &current.Environment, &current.FirstEventAt, &current.LastEventAt, &current.AcknowledgedAt, &current.ResolvedAt, &current.CreatedAt, &current.UpdatedAt, &current.Version)
	if err != nil {
		return Incident{}, err
	}
	_, err = tx.Exec(ackCtx, `INSERT INTO incident_transitions(id,tenant_id,incident_id,from_state,to_state,actor_type,actor_id,reason,request_id,trace_id) VALUES($1,$2,$3,$4,'acknowledged','ack-token',$5,'notification acknowledgement link',$6,$7)`, transitionID, tenantID, incidentID, before.State, tokenID, requestID, traceID)
	if err != nil {
		return Incident{}, err
	}
	if err := appendAudit(ackCtx, tx, AuditInput{TenantID: tenantID, ActorType: "ack-token", ActorID: tokenID, Action: "incident.acknowledged", ResourceType: "incident", ResourceID: incidentID, RequestID: requestID, TraceID: traceID, Before: before, After: current, Metadata: map[string]any{"method": "notification_link"}}); err != nil {
		return Incident{}, err
	}
	if err := tx.Commit(ackCtx); err != nil {
		return Incident{}, err
	}
	return current, nil
}

type rowScanner interface{ Scan(...any) error }

func scanIncidentWithCounts(r rowScanner, x *Incident) error {
	return r.Scan(&x.ID, &x.TenantID, &x.CorrelationKey, &x.Title, &x.State, &x.Severity, &x.Service, &x.Environment, &x.FirstEventAt, &x.LastEventAt, &x.AcknowledgedAt, &x.ResolvedAt, &x.CreatedAt, &x.UpdatedAt, &x.Version, &x.DuplicateCount, &x.EventCount)
}
func defaultText(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
