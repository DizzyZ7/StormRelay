package storage

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/DizzyZ7/StormRelay/internal/id"
	"github.com/DizzyZ7/StormRelay/internal/runbooks"
	"github.com/DizzyZ7/StormRelay/internal/telemetry"
	"github.com/jackc/pgx/v5"
)

func (s *Store) StartExecution(ctx context.Context, in StartExecutionInput) (Execution, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Execution{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var versionID, document string
	var version int
	err = tx.QueryRow(ctx, `SELECT rv.id,r.active_version,rv.document_yaml FROM runbooks r JOIN runbook_versions rv ON rv.runbook_id=r.id AND rv.version=r.active_version WHERE r.tenant_id=$1 AND r.runbook_key=$2 AND r.enabled=true`, in.TenantID, in.RunbookKey).Scan(&versionID, &version, &document)
	if err == pgx.ErrNoRows {
		return Execution{}, errNoRows
	}
	if err != nil {
		return Execution{}, err
	}
	doc, err := runbooks.Parse([]byte(document))
	if err != nil {
		return Execution{}, fmt.Errorf("stored runbook is invalid: %w", err)
	}
	var incident any
	if in.IncidentID != "" {
		var value Incident
		err = tx.QueryRow(ctx, `SELECT id,tenant_id,correlation_key,title,state,severity,COALESCE(service,''),COALESCE(environment,''),first_event_at,last_event_at,acknowledged_at,resolved_at,created_at,updated_at,version FROM incidents WHERE tenant_id=$1 AND id=$2`, in.TenantID, in.IncidentID).Scan(&value.ID, &value.TenantID, &value.CorrelationKey, &value.Title, &value.State, &value.Severity, &value.Service, &value.Environment, &value.FirstEventAt, &value.LastEventAt, &value.AcknowledgedAt, &value.ResolvedAt, &value.CreatedAt, &value.UpdatedAt, &value.Version)
		if err == pgx.ErrNoRows {
			return Execution{}, errNoRows
		}
		if err != nil {
			return Execution{}, err
		}
		incident = value
	}
	executionID, err := id.New()
	if err != nil {
		return Execution{}, err
	}
	inputSnapshot, err := json.Marshal(map[string]any{
		"incident":        incident,
		"parameters":      in.Parameters,
		"runbook_key":     in.RunbookKey,
		"runbook_version": version,
		"traceparent":     telemetry.TraceParent(ctx),
	})
	if err != nil {
		return Execution{}, err
	}
	var out Execution
	err = tx.QueryRow(ctx, `INSERT INTO executions(id,tenant_id,incident_id,runbook_version_id,status,dry_run,requested_by,input_snapshot,started_at) VALUES($1,$2,NULLIF($3,'')::uuid,$4,'running',$5,$6,$7,now()) RETURNING id,tenant_id,COALESCE(incident_id::text,''),runbook_version_id,status,dry_run,requested_by,input_snapshot,started_at,finished_at,paused_at,cancel_requested_at,COALESCE(last_error,''),created_at,updated_at,version`, executionID, in.TenantID, in.IncidentID, versionID, in.DryRun, defaultText(in.RequestedBy, "unknown"), inputSnapshot).Scan(&out.ID, &out.TenantID, &out.IncidentID, &out.RunbookVersionID, &out.Status, &out.DryRun, &out.RequestedBy, &out.InputSnapshot, &out.StartedAt, &out.FinishedAt, &out.PausedAt, &out.CancelRequested, &out.LastError, &out.CreatedAt, &out.UpdatedAt, &out.Version)
	if err != nil {
		return Execution{}, err
	}
	out.RunbookKey = in.RunbookKey
	out.RunbookVersion = version
	for position, step := range doc.Spec.Steps {
		stepID, err := id.New()
		if err != nil {
			return Execution{}, err
		}
		timeout, err := runbooks.TimeoutSeconds(step)
		if err != nil {
			return Execution{}, err
		}
		retry, err := runbooks.ParsedRetry(step.Retry)
		if err != nil {
			return Execution{}, err
		}
		snapshot, err := json.Marshal(runbooks.Snapshot{RunbookID: doc.Metadata.ID, RunbookVersion: doc.Metadata.Version, Step: step, TimeoutSeconds: timeout, Retry: retry})
		if err != nil {
			return Execution{}, err
		}
		retryJSON, _ := json.Marshal(retry)
		var rollback json.RawMessage
		if step.Rollback != nil {
			rollback, err = json.Marshal(step.Rollback)
			if err != nil {
				return Execution{}, err
			}
		}
		correlationID := executionID + ":" + step.ID
		idempotencyKey := "runbook:" + executionID + ":step:" + step.ID
		_, err = tx.Exec(ctx, `INSERT INTO execution_steps(id,execution_id,position,step_key,name,step_type,immutable_input,status,timeout_seconds,retry_policy,correlation_id,idempotency_key,rollback_definition) VALUES($1,$2,$3,$4,NULLIF($5,''),$6,$7,'pending',$8,$9,$10,$11,$12)`, stepID, executionID, position, step.ID, step.Name, step.Type, snapshot, timeout, retryJSON, correlationID, idempotencyKey, rollback)
		if err != nil {
			return Execution{}, err
		}
	}
	if err := appendAudit(ctx, tx, AuditInput{TenantID: in.TenantID, ActorType: "api-key", ActorID: defaultText(in.RequestedBy, "unknown"), Action: "execution.started", ResourceType: "execution", ResourceID: executionID, RequestID: in.RequestID, TraceID: in.TraceID, After: out, Metadata: map[string]any{"runbook_key": in.RunbookKey, "runbook_version": version, "dry_run": in.DryRun, "step_count": len(doc.Spec.Steps)}}); err != nil {
		return Execution{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Execution{}, err
	}
	return s.GetExecution(ctx, in.TenantID, executionID)
}

func (s *Store) GetExecution(ctx context.Context, tenantID, executionID string) (Execution, error) {
	var out Execution
	err := s.pool.QueryRow(ctx, `SELECT e.id,e.tenant_id,COALESCE(e.incident_id::text,''),e.runbook_version_id,r.runbook_key,rv.version,e.status,e.dry_run,e.requested_by,e.input_snapshot,e.started_at,e.finished_at,e.paused_at,e.cancel_requested_at,COALESCE(e.last_error,''),e.created_at,e.updated_at,e.version FROM executions e JOIN runbook_versions rv ON rv.id=e.runbook_version_id JOIN runbooks r ON r.id=rv.runbook_id WHERE e.tenant_id=$1 AND e.id=$2`, tenantID, executionID).Scan(&out.ID, &out.TenantID, &out.IncidentID, &out.RunbookVersionID, &out.RunbookKey, &out.RunbookVersion, &out.Status, &out.DryRun, &out.RequestedBy, &out.InputSnapshot, &out.StartedAt, &out.FinishedAt, &out.PausedAt, &out.CancelRequested, &out.LastError, &out.CreatedAt, &out.UpdatedAt, &out.Version)
	if err == pgx.ErrNoRows {
		return Execution{}, errNoRows
	}
	if err != nil {
		return Execution{}, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id,execution_id,position,step_key,COALESCE(name,''),step_type,immutable_input,status,timeout_seconds,retry_policy,attempt_count,COALESCE(output,'null'::jsonb),COALESCE(sanitized_error,''),correlation_id,idempotency_key,started_at,finished_at,next_attempt_at,wait_until,COALESCE(lease_owner,''),lease_expires_at,COALESCE(rollback_definition,'null'::jsonb),is_rollback,COALESCE(rollback_of::text,''),version FROM execution_steps WHERE execution_id=$1 ORDER BY is_rollback,position`, executionID)
	if err != nil {
		return Execution{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var step ExecutionStep
		if err := scanExecutionStep(rows, &step); err != nil {
			return Execution{}, err
		}
		out.Steps = append(out.Steps, step)
	}
	return out, rows.Err()
}

func (s *Store) ListExecutions(ctx context.Context, tenantID string, limit int) ([]Execution, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `SELECT e.id,e.tenant_id,COALESCE(e.incident_id::text,''),e.runbook_version_id,r.runbook_key,rv.version,e.status,e.dry_run,e.requested_by,e.input_snapshot,e.started_at,e.finished_at,e.paused_at,e.cancel_requested_at,COALESCE(e.last_error,''),e.created_at,e.updated_at,e.version FROM executions e JOIN runbook_versions rv ON rv.id=e.runbook_version_id JOIN runbooks r ON r.id=rv.runbook_id WHERE e.tenant_id=$1 ORDER BY e.created_at DESC,e.id DESC LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Execution{}
	for rows.Next() {
		var item Execution
		if err := rows.Scan(&item.ID, &item.TenantID, &item.IncidentID, &item.RunbookVersionID, &item.RunbookKey, &item.RunbookVersion, &item.Status, &item.DryRun, &item.RequestedBy, &item.InputSnapshot, &item.StartedAt, &item.FinishedAt, &item.PausedAt, &item.CancelRequested, &item.LastError, &item.CreatedAt, &item.UpdatedAt, &item.Version); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
