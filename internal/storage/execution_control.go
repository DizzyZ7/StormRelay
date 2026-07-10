package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/DizzyZ7/StormRelay/internal/id"
	"github.com/DizzyZ7/StormRelay/internal/runbooks"
	"github.com/jackc/pgx/v5"
)

func (s *Store) ChangeExecutionState(ctx context.Context, tenantID, executionID, action, actor, requestID, traceID string) (Execution, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Execution{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM executions WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenantID, executionID).Scan(&status)
	if err == pgx.ErrNoRows {
		return Execution{}, errNoRows
	}
	if err != nil {
		return Execution{}, err
	}
	newStatus := status
	switch action {
	case "pause":
		if status != "running" {
			return Execution{}, fmt.Errorf("only running execution can be paused")
		}
		newStatus = "paused"
		_, err = tx.Exec(ctx, `UPDATE executions SET status='paused',paused_at=now(),updated_at=now(),version=version+1 WHERE id=$1`, executionID)
	case "resume":
		if status != "paused" {
			return Execution{}, fmt.Errorf("only paused execution can be resumed")
		}
		newStatus = "running"
		_, err = tx.Exec(ctx, `UPDATE executions SET status='running',paused_at=NULL,last_error=NULL,updated_at=now(),version=version+1 WHERE id=$1`, executionID)
	case "cancel":
		if status == "completed" || status == "canceled" || status == "rolled_back" {
			return Execution{}, fmt.Errorf("execution is already terminal")
		}
		newStatus = "canceled"
		_, err = tx.Exec(ctx, `UPDATE executions SET status='canceled',cancel_requested_at=now(),finished_at=now(),updated_at=now(),version=version+1 WHERE id=$1`, executionID)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE execution_steps SET status='canceled',finished_at=now(),lease_owner=NULL,lease_expires_at=NULL,version=version+1 WHERE execution_id=$1 AND status IN ('pending','retrying','waiting','waiting_approval')`, executionID)
		}
	default:
		return Execution{}, fmt.Errorf("unsupported execution action %q", action)
	}
	if err != nil {
		return Execution{}, err
	}
	if err := appendAudit(ctx, tx, AuditInput{TenantID: tenantID, ActorType: "api-key", ActorID: defaultText(actor, "unknown"), Action: "execution." + action, ResourceType: "execution", ResourceID: executionID, RequestID: requestID, TraceID: traceID, Before: map[string]any{"status": status}, After: map[string]any{"status": newStatus}}); err != nil {
		return Execution{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Execution{}, err
	}
	return s.GetExecution(ctx, tenantID, executionID)
}

func (s *Store) RetryExecutionStep(ctx context.Context, tenantID, executionID, stepID, actor, requestID, traceID string, force bool) (Execution, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Execution{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var status, stepType string
	var snapshotJSON json.RawMessage
	err = tx.QueryRow(ctx, `SELECT es.status,es.step_type,es.immutable_input FROM execution_steps es JOIN executions e ON e.id=es.execution_id WHERE e.tenant_id=$1 AND e.id=$2 AND es.id=$3 FOR UPDATE OF es,e`, tenantID, executionID, stepID).Scan(&status, &stepType, &snapshotJSON)
	if err == pgx.ErrNoRows {
		return Execution{}, errNoRows
	}
	if err != nil {
		return Execution{}, err
	}
	if status != "failed" && status != "ambiguous" {
		return Execution{}, fmt.Errorf("only failed or ambiguous steps can be retried")
	}
	var snapshot runbooks.Snapshot
	if err := json.Unmarshal(snapshotJSON, &snapshot); err != nil {
		return Execution{}, err
	}
	idempotent := stepType == "plugin" || (stepType == "http" && snapshot.Step.With.IdempotencyHeader != "")
	if status == "ambiguous" && !idempotent && !force {
		return Execution{}, fmt.Errorf("force=true is required to retry an ambiguous non-idempotent step")
	}
	_, err = tx.Exec(ctx, `UPDATE execution_steps SET status='retrying',next_attempt_at=now(),finished_at=NULL,lease_owner=NULL,lease_expires_at=NULL,sanitized_error=NULL,version=version+1 WHERE id=$1`, stepID)
	if err != nil {
		return Execution{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE executions SET status='running',finished_at=NULL,last_error=NULL,updated_at=now(),version=version+1 WHERE id=$1`, executionID)
	if err != nil {
		return Execution{}, err
	}
	if err := appendAudit(ctx, tx, AuditInput{TenantID: tenantID, ActorType: "api-key", ActorID: defaultText(actor, "unknown"), Action: "execution_step.retry_requested", ResourceType: "execution_step", ResourceID: stepID, RequestID: requestID, TraceID: traceID, Metadata: map[string]any{"execution_id": executionID, "force": force, "previous_status": status, "idempotent": idempotent}}); err != nil {
		return Execution{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Execution{}, err
	}
	return s.GetExecution(ctx, tenantID, executionID)
}

func (s *Store) StartExecutionRollback(ctx context.Context, tenantID, executionID, actor, requestID, traceID string) (Execution, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Execution{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM executions WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenantID, executionID).Scan(&status)
	if err == pgx.ErrNoRows {
		return Execution{}, errNoRows
	}
	if err != nil {
		return Execution{}, err
	}
	if status != "completed" && status != "failed" && status != "ambiguous" {
		return Execution{}, fmt.Errorf("rollback requires completed, failed, or ambiguous execution")
	}
	var existingRollback int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM execution_steps WHERE execution_id=$1 AND is_rollback=true`, executionID).Scan(&existingRollback); err != nil {
		return Execution{}, err
	}
	if existingRollback > 0 {
		return Execution{}, fmt.Errorf("rollback is already initialized")
	}
	rows, err := tx.Query(ctx, `SELECT id,position,step_key,rollback_definition FROM execution_steps WHERE execution_id=$1 AND is_rollback=false AND status='completed' AND rollback_definition IS NOT NULL ORDER BY position DESC FOR UPDATE`, executionID)
	if err != nil {
		return Execution{}, err
	}
	type source struct {
		id, key  string
		position int
		action   json.RawMessage
	}
	sources := []source{}
	for rows.Next() {
		var item source
		if err := rows.Scan(&item.id, &item.position, &item.key, &item.action); err != nil {
			rows.Close()
			return Execution{}, err
		}
		sources = append(sources, item)
	}
	rows.Close()
	if len(sources) == 0 {
		return Execution{}, fmt.Errorf("execution has no completed steps with rollback definitions")
	}
	for position, item := range sources {
		var action runbooks.Action
		if err := json.Unmarshal(item.action, &action); err != nil {
			return Execution{}, err
		}
		step := runbooks.Step{ID: "rollback-" + item.key, Name: "Rollback " + item.key, Type: action.Type, Timeout: action.Timeout, Retry: action.Retry, With: action.With}
		timeout, err := runbooks.TimeoutSeconds(step)
		if err != nil {
			return Execution{}, err
		}
		retry, err := runbooks.ParsedRetry(step.Retry)
		if err != nil {
			return Execution{}, err
		}
		snapshot, _ := json.Marshal(runbooks.Snapshot{Step: step, TimeoutSeconds: timeout, Retry: retry})
		retryJSON, _ := json.Marshal(retry)
		rollbackID, err := id.New()
		if err != nil {
			return Execution{}, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO execution_steps(id,execution_id,position,step_key,name,step_type,immutable_input,status,timeout_seconds,retry_policy,correlation_id,idempotency_key,is_rollback,rollback_of) VALUES($1,$2,$3,$4,$5,$6,$7,'pending',$8,$9,$10,$11,true,$12)`, rollbackID, executionID, position, step.ID, step.Name, step.Type, snapshot, timeout, retryJSON, executionID+":"+step.ID, "runbook:"+executionID+":"+step.ID, item.id)
		if err != nil {
			return Execution{}, err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE executions SET status='rolling_back',finished_at=NULL,last_error=NULL,updated_at=now(),version=version+1 WHERE id=$1`, executionID)
	if err != nil {
		return Execution{}, err
	}
	if err := appendAudit(ctx, tx, AuditInput{TenantID: tenantID, ActorType: "api-key", ActorID: defaultText(actor, "unknown"), Action: "execution.rollback_started", ResourceType: "execution", ResourceID: executionID, RequestID: requestID, TraceID: traceID, Metadata: map[string]any{"rollback_steps": len(sources)}}); err != nil {
		return Execution{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Execution{}, err
	}
	return s.GetExecution(ctx, tenantID, executionID)
}

func (s *Store) ExecutionConditionInputs(ctx context.Context, executionID string) (map[string]string, error) {
	var dryRun bool
	var input json.RawMessage
	err := s.pool.QueryRow(ctx, `SELECT dry_run,input_snapshot FROM executions WHERE id=$1`, executionID).Scan(&dryRun, &input)
	if err != nil {
		return nil, err
	}
	out := map[string]string{"execution.dry_run": fmt.Sprint(dryRun)}
	var snapshot map[string]any
	if err := json.Unmarshal(input, &snapshot); err == nil {
		if incident, ok := snapshot["incident"].(map[string]any); ok {
			for _, key := range []string{"id", "state", "severity", "service", "environment", "title"} {
				if value, exists := incident[key]; exists {
					out["incident."+key] = fmt.Sprint(value)
				}
			}
		}
	}
	rows, err := s.pool.Query(ctx, `SELECT step_key,status,COALESCE(output,'null'::jsonb) FROM execution_steps WHERE execution_id=$1 ORDER BY position`, executionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, status string
		var output json.RawMessage
		if err := rows.Scan(&key, &status, &output); err != nil {
			return nil, err
		}
		out["steps."+key+".status"] = status
		var values map[string]any
		if json.Unmarshal(output, &values) == nil {
			for name, value := range values {
				if scalar, ok := scalarString(value); ok {
					out["steps."+key+".output."+name] = scalar
				}
			}
		}
	}
	return out, rows.Err()
}
