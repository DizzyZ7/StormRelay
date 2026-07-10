package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/DizzyZ7/StormRelay/internal/id"
	"github.com/jackc/pgx/v5"
)

func (s *Store) OpenApproval(ctx context.Context, step ClaimedStep, workerID, prompt string, expiresAt *time.Time) (Approval, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Approval{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	approvalID, err := id.New()
	if err != nil {
		return Approval{}, err
	}
	var out Approval
	err = tx.QueryRow(ctx, `INSERT INTO approvals(id,tenant_id,execution_step_id,status,expires_at,prompt) VALUES($1,$2,$3,'pending',$4,$5) ON CONFLICT (execution_step_id) DO UPDATE SET execution_step_id=EXCLUDED.execution_step_id RETURNING id,tenant_id,execution_step_id,status,requested_at,expires_at,decided_at,COALESCE(decided_by,''),COALESCE(reason,'')`, approvalID, step.TenantID, step.Step.ID, expiresAt, prompt).Scan(&out.ID, &out.TenantID, &out.ExecutionStepID, &out.Status, &out.RequestedAt, &out.ExpiresAt, &out.DecidedAt, &out.DecidedBy, &out.Reason)
	if err != nil {
		return Approval{}, err
	}
	out.ExecutionID = step.Step.ExecutionID
	out.StepKey = step.Step.StepKey
	out.Prompt = prompt
	command, err := tx.Exec(ctx, `UPDATE execution_steps SET status='waiting_approval',output=$2,lease_owner=NULL,lease_expires_at=NULL,version=version+1 WHERE id=$1 AND lease_owner=$3`, step.Step.ID, map[string]any{"approval_id": out.ID, "status": "pending", "prompt": prompt}, workerID)
	if err != nil {
		return Approval{}, err
	}
	if command.RowsAffected() != 1 {
		return Approval{}, fmt.Errorf("approval step lease lost")
	}
	_, err = tx.Exec(ctx, `UPDATE executions SET status='waiting_approval',updated_at=now(),version=version+1 WHERE id=$1`, step.Step.ExecutionID)
	if err != nil {
		return Approval{}, err
	}
	if err := appendAudit(ctx, tx, AuditInput{TenantID: step.TenantID, ActorType: "service", ActorID: workerID, Action: "approval.requested", ResourceType: "approval", ResourceID: out.ID, After: map[string]any{"status": "pending", "expires_at": expiresAt}, Metadata: map[string]any{"execution_id": step.Step.ExecutionID, "step_id": step.Step.ID, "prompt": truncate(prompt, 500)}}); err != nil {
		return Approval{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Approval{}, err
	}
	return out, nil
}

func (s *Store) ListApprovals(ctx context.Context, tenantID, status, executionID string, limit int) ([]Approval, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `SELECT a.id,a.tenant_id,a.execution_step_id,es.execution_id,es.step_key,COALESCE(a.prompt,''),a.status,a.requested_at,a.expires_at,a.decided_at,COALESCE(a.decided_by,''),COALESCE(a.reason,'') FROM approvals a JOIN execution_steps es ON es.id=a.execution_step_id WHERE a.tenant_id=$1 AND ($2='' OR a.status=$2) AND ($3='' OR es.execution_id=$3) ORDER BY a.requested_at DESC LIMIT $4`, tenantID, status, executionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Approval{}
	for rows.Next() {
		var item Approval
		if err := rows.Scan(&item.ID, &item.TenantID, &item.ExecutionStepID, &item.ExecutionID, &item.StepKey, &item.Prompt, &item.Status, &item.RequestedAt, &item.ExpiresAt, &item.DecidedAt, &item.DecidedBy, &item.Reason); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) DecideApproval(ctx context.Context, tenantID, approvalID, decision, actor, reason, requestID, traceID string) (Execution, error) {
	if decision != "approved" && decision != "rejected" {
		return Execution{}, fmt.Errorf("decision must be approved or rejected")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Execution{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var stepID, executionID string
	var current string
	err = tx.QueryRow(ctx, `SELECT a.execution_step_id,es.execution_id,a.status FROM approvals a JOIN execution_steps es ON es.id=a.execution_step_id WHERE a.tenant_id=$1 AND a.id=$2 FOR UPDATE OF a,es`, tenantID, approvalID).Scan(&stepID, &executionID, &current)
	if err == pgx.ErrNoRows {
		return Execution{}, errNoRows
	}
	if err != nil {
		return Execution{}, err
	}
	if current != "pending" {
		return Execution{}, fmt.Errorf("approval is already %s", current)
	}
	_, err = tx.Exec(ctx, `UPDATE approvals SET status=$2,decided_at=now(),decided_by=$3,reason=NULLIF($4,'') WHERE id=$1`, approvalID, decision, defaultText(actor, "unknown"), reason)
	if err != nil {
		return Execution{}, err
	}
	stepStatus := "completed"
	if decision == "rejected" {
		stepStatus = "failed"
	}
	_, err = tx.Exec(ctx, `UPDATE execution_steps SET status=$2,output=$3,finished_at=now(),version=version+1 WHERE id=$1`, stepID, stepStatus, map[string]any{"approval_id": approvalID, "status": decision, "decided_by": actor, "reason": reason})
	if err != nil {
		return Execution{}, err
	}
	if decision == "approved" {
		_, err = tx.Exec(ctx, `UPDATE executions SET status='running',updated_at=now(),version=version+1 WHERE id=$1`, executionID)
		if err == nil {
			err = finalizeExecutionIfDone(ctx, tx, executionID)
		}
	} else {
		_, err = tx.Exec(ctx, `UPDATE executions SET status='failed',finished_at=now(),last_error='approval rejected',updated_at=now(),version=version+1 WHERE id=$1`, executionID)
	}
	if err != nil {
		return Execution{}, err
	}
	if err := appendAudit(ctx, tx, AuditInput{TenantID: tenantID, ActorType: "user", ActorID: defaultText(actor, "unknown"), Action: "approval." + decision, ResourceType: "approval", ResourceID: approvalID, RequestID: requestID, TraceID: traceID, Metadata: map[string]any{"execution_id": executionID, "step_id": stepID, "reason": truncate(reason, 500)}}); err != nil {
		return Execution{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Execution{}, err
	}
	return s.GetExecution(ctx, tenantID, executionID)
}
