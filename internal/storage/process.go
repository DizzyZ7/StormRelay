package storage

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/DizzyZ7/StormRelay/internal/events"
	"github.com/DizzyZ7/StormRelay/internal/id"
	"github.com/DizzyZ7/StormRelay/internal/policies"
	"github.com/DizzyZ7/StormRelay/internal/telemetry"
	"github.com/jackc/pgx/v5"
)

type ProcessOptions struct {
	DedupeWindow, CorrelationWindow time.Duration
	ActorID, PublicBaseURL          string
}

func (s *Store) ProcessEvent(ctx context.Context, e events.Event, opt ProcessOptions) (result ProcessResult, err error) {
	ctx, processSpan := telemetry.StartDatabaseSpan(ctx, "process_event")
	defer func() { telemetry.EndSpan(processSpan, err) }()

	if opt.DedupeWindow <= 0 {
		opt.DedupeWindow = 15 * time.Minute
	}
	if opt.CorrelationWindow <= 0 {
		opt.CorrelationWindow = 30 * time.Minute
	}
	if opt.ActorID == "" {
		opt.ActorID = "worker"
	}
	rawID, err := id.New()
	if err != nil {
		return ProcessResult{}, err
	}
	normalizedID, err := id.New()
	if err != nil {
		return ProcessResult{}, err
	}
	payloadHash := sha256.Sum256(e.RawPayload)
	bucket := timeBucket(e.ReceivedAt, opt.DedupeWindow)
	dedupeKey := events.DedupeKey(e)
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return ProcessResult{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	_, err = tx.Exec(ctx, `INSERT INTO raw_events(id,tenant_id,source_id,content_type,payload,payload_sha256,received_at,request_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, rawID, e.TenantID, e.SourceID, e.DataContentType, []byte(e.RawPayload), payloadHash[:], e.ReceivedAt, e.RequestID)
	if err != nil {
		return ProcessResult{}, fmt.Errorf("insert raw event: %w", err)
	}

	dedupeCtx, dedupeSpan := telemetry.StartInternalSpan(ctx, "stormrelay.event.deduplicate")
	var eventID string
	err = tx.QueryRow(dedupeCtx, `INSERT INTO normalized_events(id,tenant_id,source_id,raw_event_id,ce_id,source_event_id,idempotency_key,dedupe_key,dedupe_bucket,fingerprint,ce_source,ce_type,subject,event_time,data_content_type,schema_version,trace_parent,labels,severity)
	VALUES($1,$2,$3,$4,$5,NULLIF($6,''),NULLIF($7,''),$8,$9,$10,$11,$12,NULLIF($13,''),$14,$15,$16,NULLIF($17,''),$18,$19)
	ON CONFLICT (tenant_id,source_id,dedupe_key,dedupe_bucket) DO NOTHING RETURNING id`, normalizedID, e.TenantID, e.SourceID, rawID, e.ID, e.SourceEventID, e.IdempotencyKey, dedupeKey, bucket, e.Fingerprint, e.Source, e.Type, e.Subject, e.Time, e.DataContentType, e.SchemaVersion, e.TraceParent, e.Labels, e.Severity).Scan(&eventID)
	if err == pgx.ErrNoRows {
		telemetry.SetSpanBool(dedupeSpan, "stormrelay.event.duplicate", true)
		telemetry.EndSpan(dedupeSpan, nil)
		return s.recordDuplicate(dedupeCtx, tx, e, rawID, dedupeKey, bucket, opt.ActorID)
	}
	if err != nil {
		telemetry.EndSpan(dedupeSpan, err)
		return ProcessResult{}, fmt.Errorf("insert normalized event: %w", err)
	}
	telemetry.SetSpanBool(dedupeSpan, "stormrelay.event.duplicate", false)
	telemetry.EndSpan(dedupeSpan, nil)

	incident, created, err := s.correlate(ctx, tx, e, eventID, opt.CorrelationWindow)
	if err != nil {
		return ProcessResult{}, err
	}
	channels, suppressed, err := s.evaluatePolicies(ctx, tx, e, incident.ID, opt.ActorID)
	if err != nil {
		return ProcessResult{}, err
	}
	notificationIDs := []string{}
	if !suppressed {
		if len(channels) == 0 {
			channels = []string{"local-mock"}
		}
		notifyCtx, notifySpan := telemetry.StartNotificationSpan(ctx, "enqueue", "configured")
		ids, enqueueErr := s.enqueueNotifications(notifyCtx, tx, incident, channels, opt.PublicBaseURL)
		telemetry.SetSpanInt(notifySpan, "stormrelay.notification.channel_count", len(channels))
		telemetry.SetSpanInt(notifySpan, "stormrelay.notification.delivery_count", len(ids))
		telemetry.EndSpan(notifySpan, enqueueErr)
		if enqueueErr != nil {
			return ProcessResult{}, enqueueErr
		}
		notificationIDs = ids
	}

	auditCtx, auditSpan := telemetry.StartInternalSpan(ctx, "stormrelay.audit.append")
	if auditErr := appendAudit(auditCtx, tx, AuditInput{TenantID: e.TenantID, ActorType: "service", ActorID: opt.ActorID, Action: "event.accepted", ResourceType: "event", ResourceID: eventID, RequestID: e.RequestID, TraceID: traceIDFromParent(e.TraceParent), After: map[string]any{"id": eventID, "type": e.Type, "severity": e.Severity, "fingerprint": e.Fingerprint}, Metadata: map[string]any{"source_id": e.SourceID, "raw_event_id": rawID}}); auditErr != nil {
		telemetry.EndSpan(auditSpan, auditErr)
		return ProcessResult{}, auditErr
	}
	action := "incident.updated"
	if created {
		action = "incident.created"
	}
	if auditErr := appendAudit(auditCtx, tx, AuditInput{TenantID: e.TenantID, ActorType: "service", ActorID: opt.ActorID, Action: action, ResourceType: "incident", ResourceID: incident.ID, RequestID: e.RequestID, TraceID: traceIDFromParent(e.TraceParent), After: incident, Metadata: map[string]any{"event_id": eventID, "correlation_key": incident.CorrelationKey}}); auditErr != nil {
		telemetry.EndSpan(auditSpan, auditErr)
		return ProcessResult{}, auditErr
	}
	telemetry.SetSpanInt(auditSpan, "stormrelay.audit.entry_count", 2)
	telemetry.EndSpan(auditSpan, nil)

	commitCtx, commitSpan := telemetry.StartDatabaseSpan(ctx, "commit_event")
	err = tx.Commit(commitCtx)
	telemetry.EndSpan(commitSpan, err)
	if err != nil {
		return ProcessResult{}, err
	}
	telemetry.SetSpanBool(processSpan, "stormrelay.event.duplicate", false)
	telemetry.SetSpanBool(processSpan, "stormrelay.incident.created", created)
	telemetry.SetSpanBool(processSpan, "stormrelay.notification.suppressed", suppressed)
	return ProcessResult{EventID: eventID, Incident: incident, NotificationIDs: notificationIDs}, nil
}

func (s *Store) recordDuplicate(ctx context.Context, tx pgx.Tx, e events.Event, rawID, dedupeKey string, bucket time.Time, actor string) (result ProcessResult, err error) {
	ctx, span := telemetry.StartInternalSpan(ctx, "stormrelay.event.record_duplicate")
	defer func() { telemetry.EndSpan(span, err) }()

	var canonical string
	var count int64
	err = tx.QueryRow(ctx, `UPDATE normalized_events SET duplicate_count=duplicate_count+1 WHERE tenant_id=$1 AND source_id=$2 AND dedupe_key=$3 AND dedupe_bucket=$4 RETURNING id,duplicate_count`, e.TenantID, e.SourceID, dedupeKey, bucket).Scan(&canonical, &count)
	if err != nil {
		return ProcessResult{}, fmt.Errorf("locate duplicate canonical event: %w", err)
	}
	duplicateID, err := id.New()
	if err != nil {
		return ProcessResult{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO event_duplicates(id,tenant_id,canonical_event_id,raw_event_id,duplicate_number,reason,received_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, duplicateID, e.TenantID, canonical, rawID, count, "dedupe_key_within_window", e.ReceivedAt)
	if err != nil {
		return ProcessResult{}, err
	}
	if err = appendAudit(ctx, tx, AuditInput{TenantID: e.TenantID, ActorType: "service", ActorID: actor, Action: "event.duplicate", ResourceType: "event", ResourceID: canonical, RequestID: e.RequestID, TraceID: traceIDFromParent(e.TraceParent), Metadata: map[string]any{"duplicate_id": duplicateID, "duplicate_number": count, "reason": "dedupe_key_within_window"}}); err != nil {
		return ProcessResult{}, err
	}
	commitCtx, commitSpan := telemetry.StartDatabaseSpan(ctx, "commit_duplicate")
	err = tx.Commit(commitCtx)
	telemetry.EndSpan(commitSpan, err)
	if err != nil {
		return ProcessResult{}, err
	}
	telemetry.SetSpanBool(span, "stormrelay.event.duplicate", true)
	telemetry.SetSpanInt(span, "stormrelay.event.duplicate_number", int(count))
	return ProcessResult{EventID: canonical, Duplicate: true, DuplicateNumber: count}, nil
}

func (s *Store) correlate(ctx context.Context, tx pgx.Tx, e events.Event, eventID string, window time.Duration) (incident Incident, created bool, err error) {
	ctx, span := telemetry.StartInternalSpan(ctx, "stormrelay.incident.correlate")
	defer func() {
		if err == nil {
			telemetry.SetSpanBool(span, "stormrelay.incident.created", created)
			telemetry.SetSpanString(span, "stormrelay.incident.state", incident.State)
		}
		telemetry.EndSpan(span, err)
	}()

	key := events.CorrelationKey(e)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, key); err != nil {
		return Incident{}, false, err
	}
	var in Incident
	err = tx.QueryRow(ctx, `SELECT id,tenant_id,correlation_key,title,state,severity,COALESCE(service,''),COALESCE(environment,''),first_event_at,last_event_at,acknowledged_at,resolved_at,created_at,updated_at,version FROM incidents WHERE tenant_id=$1 AND correlation_key=$2 AND state NOT IN ('resolved','closed') AND last_event_at >= $3 ORDER BY last_event_at DESC LIMIT 1 FOR UPDATE`, e.TenantID, key, e.ReceivedAt.Add(-window)).Scan(&in.ID, &in.TenantID, &in.CorrelationKey, &in.Title, &in.State, &in.Severity, &in.Service, &in.Environment, &in.FirstEventAt, &in.LastEventAt, &in.AcknowledgedAt, &in.ResolvedAt, &in.CreatedAt, &in.UpdatedAt, &in.Version)
	if err == pgx.ErrNoRows {
		created = true
		incidentID, genErr := id.New()
		if genErr != nil {
			return Incident{}, false, genErr
		}
		transitionID, genErr := id.New()
		if genErr != nil {
			return Incident{}, false, genErr
		}
		title := strings.TrimSpace(e.Subject)
		if title == "" {
			title = e.Type
		}
		err = tx.QueryRow(ctx, `INSERT INTO incidents(id,tenant_id,correlation_key,title,state,severity,service,environment,first_event_at,last_event_at) VALUES($1,$2,$3,$4,'detected',$5,NULLIF($6,''),NULLIF($7,''),$8,$8) RETURNING id,tenant_id,correlation_key,title,state,severity,COALESCE(service,''),COALESCE(environment,''),first_event_at,last_event_at,acknowledged_at,resolved_at,created_at,updated_at,version`, incidentID, e.TenantID, key, title, e.Severity, e.Labels["service"], e.Labels["environment"], e.ReceivedAt).Scan(&in.ID, &in.TenantID, &in.CorrelationKey, &in.Title, &in.State, &in.Severity, &in.Service, &in.Environment, &in.FirstEventAt, &in.LastEventAt, &in.AcknowledgedAt, &in.ResolvedAt, &in.CreatedAt, &in.UpdatedAt, &in.Version)
		if err != nil {
			return Incident{}, false, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO incident_transitions(id,tenant_id,incident_id,from_state,to_state,actor_type,actor_id,reason,request_id,trace_id) VALUES($1,$2,$3,NULL,'detected','service','correlation-engine','first correlated event',$4,$5)`, transitionID, e.TenantID, in.ID, e.RequestID, traceIDFromParent(e.TraceParent))
		if err != nil {
			return Incident{}, false, err
		}
	} else if err != nil {
		return Incident{}, false, err
	} else {
		severity := maxSeverity(in.Severity, e.Severity)
		err = tx.QueryRow(ctx, `UPDATE incidents SET last_event_at=GREATEST(last_event_at,$2),severity=$3,updated_at=now(),version=version+1 WHERE id=$1 AND version=$4 RETURNING id,tenant_id,correlation_key,title,state,severity,COALESCE(service,''),COALESCE(environment,''),first_event_at,last_event_at,acknowledged_at,resolved_at,created_at,updated_at,version`, in.ID, e.ReceivedAt, severity, in.Version).Scan(&in.ID, &in.TenantID, &in.CorrelationKey, &in.Title, &in.State, &in.Severity, &in.Service, &in.Environment, &in.FirstEventAt, &in.LastEventAt, &in.AcknowledgedAt, &in.ResolvedAt, &in.CreatedAt, &in.UpdatedAt, &in.Version)
		if err != nil {
			return Incident{}, false, err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO incident_events(incident_id,event_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, in.ID, eventID)
	if err != nil {
		return Incident{}, false, err
	}
	return in, created, nil
}

func (s *Store) evaluatePolicies(ctx context.Context, tx pgx.Tx, e events.Event, incidentID, actor string) (resultChannels []string, suppressed bool, err error) {
	ctx, span := telemetry.StartInternalSpan(ctx, "stormrelay.policy.evaluate")
	evaluated := 0
	matched := 0
	defer func() {
		telemetry.SetSpanInt(span, "stormrelay.policy.evaluated_count", evaluated)
		telemetry.SetSpanInt(span, "stormrelay.policy.matched_count", matched)
		telemetry.SetSpanBool(span, "stormrelay.policy.suppressed", suppressed)
		telemetry.SetSpanInt(span, "stormrelay.notification.channel_count", len(uniqueStrings(resultChannels)))
		telemetry.EndSpan(span, err)
	}()

	rows, err := tx.Query(ctx, `SELECT p.policy_key,p.active_version,pv.document_yaml FROM policies p JOIN policy_versions pv ON pv.policy_id=p.id AND pv.version=p.active_version WHERE p.tenant_id=$1 AND p.enabled=true ORDER BY p.policy_key`, e.TenantID)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	channels := []string{}
	matchedAny := false
	for rows.Next() {
		var key, document string
		var version int
		if err = rows.Scan(&key, &version, &document); err != nil {
			return nil, false, err
		}
		evaluated++
		doc, parseErr := policies.Parse([]byte(document))
		if parseErr != nil {
			return nil, false, fmt.Errorf("stored policy %s is invalid: %w", key, parseErr)
		}
		decision := policies.Evaluate(doc, e)
		if decision.Matched {
			matched++
			matchedAny = true
			suppressed = suppressed || decision.Actions.Suppress
			channels = append(channels, decision.Actions.NotificationChannels...)
		}
		if err = appendAudit(ctx, tx, AuditInput{TenantID: e.TenantID, ActorType: "service", ActorID: actor, Action: "policy.evaluated", ResourceType: "incident", ResourceID: incidentID, RequestID: e.RequestID, TraceID: traceIDFromParent(e.TraceParent), Metadata: map[string]any{"policy_id": decision.PolicyID, "policy_version": decision.PolicyVersion, "matched": decision.Matched, "actions": decision.Actions, "explanation": decision.Explanation, "inputs": decision.Inputs}}); err != nil {
			return nil, false, err
		}
	}
	if err = rows.Err(); err != nil {
		return nil, false, err
	}
	if !matchedAny {
		if err = appendAudit(ctx, tx, AuditInput{TenantID: e.TenantID, ActorType: "service", ActorID: actor, Action: "policy.default", ResourceType: "incident", ResourceID: incidentID, RequestID: e.RequestID, Metadata: map[string]any{"action": "notify", "channel": "local-mock", "explanation": "no enabled policy matched; safe local default"}}); err != nil {
			return nil, false, err
		}
	}
	resultChannels = uniqueStrings(channels)
	return resultChannels, suppressed, nil
}

func timeBucket(t time.Time, window time.Duration) time.Time {
	seconds := int64(window.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	return time.Unix((t.Unix()/seconds)*seconds, 0).UTC()
}
func maxSeverity(a, b events.Severity) events.Severity {
	rank := map[events.Severity]int{events.SeverityInfo: 0, events.SeverityWarning: 1, events.SeverityError: 2, events.SeverityCritical: 3}
	if rank[b] > rank[a] {
		return b
	}
	return a
}
func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range in {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
func traceIDFromParent(value string) string {
	parts := strings.Split(value, "-")
	if len(parts) == 4 {
		return parts[1]
	}
	return ""
}
