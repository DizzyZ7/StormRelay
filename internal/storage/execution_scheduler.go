package storage

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/DizzyZ7/StormRelay/internal/runbooks"
	"github.com/jackc/pgx/v5"
)

func (s *Store) ClaimExecutionSteps(ctx context.Context, workerID string, limit int, lease time.Duration) ([]ClaimedStep, error) {
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	if lease < 5*time.Second {
		lease = 30 * time.Second
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	rows, err := tx.Query(ctx, `WITH candidates AS (
		SELECT es.id
		FROM execution_steps es
		JOIN executions e ON e.id=es.execution_id
		WHERE e.status IN ('running','rolling_back')
		  AND e.cancel_requested_at IS NULL
		  AND es.status IN ('pending','retrying')
		  AND es.next_attempt_at <= now()
		  AND ((e.status='running' AND es.is_rollback=false) OR (e.status='rolling_back' AND es.is_rollback=true))
		  AND NOT EXISTS (
			SELECT 1 FROM execution_steps previous
			WHERE previous.execution_id=es.execution_id
			  AND previous.is_rollback=es.is_rollback
			  AND previous.position < es.position
			  AND previous.status NOT IN ('completed','skipped','rolled_back')
		  )
		ORDER BY e.created_at,es.position
		FOR UPDATE OF es SKIP LOCKED
		LIMIT $1
	)
	UPDATE execution_steps es
	SET status=CASE WHEN es.is_rollback THEN 'rolling_back' ELSE 'running' END,
	    lease_owner=$2,lease_expires_at=now()+$3::interval,
	    attempt_count=attempt_count+1,started_at=COALESCE(started_at,now()),version=version+1
	FROM candidates c,executions e
	WHERE es.id=c.id AND e.id=es.execution_id
	RETURNING es.id,es.execution_id,es.position,es.step_key,COALESCE(es.name,''),es.step_type,es.immutable_input,es.status,es.timeout_seconds,es.retry_policy,es.attempt_count,COALESCE(es.output,'null'::jsonb),COALESCE(es.sanitized_error,''),es.correlation_id,es.idempotency_key,es.started_at,es.finished_at,es.next_attempt_at,es.wait_until,COALESCE(es.lease_owner,''),es.lease_expires_at,COALESCE(es.rollback_definition,'null'::jsonb),es.is_rollback,COALESCE(es.rollback_of::text,''),es.version,e.tenant_id,COALESCE(e.incident_id::text,''),e.dry_run,e.input_snapshot`, limit, workerID, durationInterval(lease))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ClaimedStep{}
	for rows.Next() {
		var item ClaimedStep
		if err := scanExecutionStepWithContext(rows, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) RecoverExpiredStepLeases(ctx context.Context, actor string) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	rows, err := tx.Query(ctx, `SELECT es.id,es.execution_id,es.step_type,es.immutable_input,es.attempt_count,es.retry_policy,e.tenant_id,es.is_rollback FROM execution_steps es JOIN executions e ON e.id=es.execution_id WHERE es.status IN ('running','rolling_back') AND es.lease_expires_at<=now() FOR UPDATE OF es SKIP LOCKED`)
	if err != nil {
		return 0, err
	}
	type expired struct {
		id, executionID, stepType, tenantID string
		input, retry                        json.RawMessage
		attempt                             int
		rollback                            bool
	}
	items := []expired{}
	for rows.Next() {
		var x expired
		if err := rows.Scan(&x.id, &x.executionID, &x.stepType, &x.input, &x.attempt, &x.retry, &x.tenantID, &x.rollback); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, x)
	}
	rows.Close()
	for _, x := range items {
		var snapshot runbooks.Snapshot
		if err := json.Unmarshal(x.input, &snapshot); err != nil {
			return 0, err
		}
		safeReplay := x.stepType == "plugin" || (x.stepType == "http" && snapshot.Step.With.IdempotencyHeader != "")
		status := "ambiguous"
		executionStatus := "ambiguous"
		message := "worker lease expired; remote outcome is unknown"
		if safeReplay && x.attempt < snapshot.Retry.MaxAttempts {
			status = "retrying"
			executionStatus = "running"
			message = "worker lease expired; safely scheduled retry using idempotency protection"
		}
		_, err = tx.Exec(ctx, `UPDATE execution_steps SET status=$2,lease_owner=NULL,lease_expires_at=NULL,next_attempt_at=now(),sanitized_error=$3,version=version+1 WHERE id=$1`, x.id, status, message)
		if err != nil {
			return 0, err
		}
		_, err = tx.Exec(ctx, `UPDATE executions SET status=$2,last_error=$3,updated_at=now(),version=version+1 WHERE id=$1`, x.executionID, executionStatus, message)
		if err != nil {
			return 0, err
		}
		if err := appendAudit(ctx, tx, AuditInput{TenantID: x.tenantID, ActorType: "service", ActorID: defaultText(actor, "runbook-recovery"), Action: "execution_step.lease_expired", ResourceType: "execution_step", ResourceID: x.id, Metadata: map[string]any{"execution_id": x.executionID, "safe_replay": safeReplay, "status": status}}); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return int64(len(items)), nil
}

func (s *Store) ScheduleWait(ctx context.Context, step ClaimedStep, until time.Time, actor string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	command, err := tx.Exec(ctx, `UPDATE execution_steps SET status='waiting',wait_until=$2,next_attempt_at=$2,lease_owner=NULL,lease_expires_at=NULL,version=version+1 WHERE id=$1 AND lease_owner=$3 AND status='running'`, step.Step.ID, until, actor)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("wait step lease lost")
	}
	return tx.Commit(ctx)
}

func (s *Store) AdvanceRunbookTimers(ctx context.Context, actor string) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	rows, err := tx.Query(ctx, `UPDATE execution_steps SET status='completed',finished_at=now(),output='{"wait":"completed"}'::jsonb,wait_until=NULL,version=version+1 WHERE status='waiting' AND wait_until<=now() RETURNING id,execution_id`)
	if err != nil {
		return 0, err
	}
	type pair struct{ stepID, executionID string }
	items := []pair{}
	for rows.Next() {
		var item pair
		if err := rows.Scan(&item.stepID, &item.executionID); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, item)
	}
	rows.Close()
	for _, item := range items {
		if err := finalizeExecutionIfDone(ctx, tx, item.executionID); err != nil {
			return 0, err
		}
	}
	expired, err := tx.Query(ctx, `SELECT a.id,a.tenant_id,a.execution_step_id,es.execution_id FROM approvals a JOIN execution_steps es ON es.id=a.execution_step_id WHERE a.status='pending' AND a.expires_at IS NOT NULL AND a.expires_at<=now() FOR UPDATE OF a,es SKIP LOCKED`)
	if err != nil {
		return 0, err
	}
	for expired.Next() {
		var approvalID, tenantID, stepID, executionID string
		if err := expired.Scan(&approvalID, &tenantID, &stepID, &executionID); err != nil {
			expired.Close()
			return 0, err
		}
		_, err = tx.Exec(ctx, `UPDATE approvals SET status='expired',decided_at=now(),reason='approval timeout' WHERE id=$1`, approvalID)
		if err != nil {
			return 0, err
		}
		_, err = tx.Exec(ctx, `UPDATE execution_steps SET status='failed',finished_at=now(),sanitized_error='approval expired',version=version+1 WHERE id=$1`, stepID)
		if err != nil {
			return 0, err
		}
		_, err = tx.Exec(ctx, `UPDATE executions SET status='failed',finished_at=now(),last_error='approval expired',updated_at=now(),version=version+1 WHERE id=$1`, executionID)
		if err != nil {
			return 0, err
		}
		if err := appendAudit(ctx, tx, AuditInput{TenantID: tenantID, ActorType: "service", ActorID: defaultText(actor, "runbook-timer"), Action: "approval.expired", ResourceType: "approval", ResourceID: approvalID, Metadata: map[string]any{"execution_id": executionID, "step_id": stepID}}); err != nil {
			return 0, err
		}
	}
	expired.Close()
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return int64(len(items)), nil
}

func (s *Store) CompleteExecutionStep(ctx context.Context, step ClaimedStep, workerID string, status string, output json.RawMessage, executionErr error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var currentStatus string
	var attempt int
	var retryJSON json.RawMessage
	var tenantID string
	err = tx.QueryRow(ctx, `SELECT es.status,es.attempt_count,es.retry_policy,e.tenant_id FROM execution_steps es JOIN executions e ON e.id=es.execution_id WHERE es.id=$1 FOR UPDATE OF es`, step.Step.ID).Scan(&currentStatus, &attempt, &retryJSON, &tenantID)
	if err != nil {
		return err
	}
	if currentStatus != "running" && currentStatus != "rolling_back" {
		return fmt.Errorf("step is not running")
	}
	if executionErr == nil {
		if status != "completed" && status != "skipped" && status != "rolled_back" {
			status = "completed"
		}
		_, err = tx.Exec(ctx, `UPDATE execution_steps SET status=$2,output=$3,finished_at=now(),sanitized_error=NULL,lease_owner=NULL,lease_expires_at=NULL,version=version+1 WHERE id=$1 AND lease_owner=$4`, step.Step.ID, status, nullJSON(output), workerID)
		if err != nil {
			return err
		}
		if err := finalizeExecutionIfDone(ctx, tx, step.Step.ExecutionID); err != nil {
			return err
		}
	} else {
		var retry runbooks.RetryParsed
		if err := json.Unmarshal(retryJSON, &retry); err != nil {
			return err
		}
		message := truncate(executionErr.Error(), 1000)
		if attempt < retry.MaxAttempts {
			backoff := retryBackoff(retry, attempt, step.Step.ID)
			_, err = tx.Exec(ctx, `UPDATE execution_steps SET status='retrying',sanitized_error=$2,next_attempt_at=now()+$3::interval,lease_owner=NULL,lease_expires_at=NULL,version=version+1 WHERE id=$1 AND lease_owner=$4`, step.Step.ID, message, durationInterval(backoff), workerID)
			if err != nil {
				return err
			}
		} else {
			_, err = tx.Exec(ctx, `UPDATE execution_steps SET status='failed',sanitized_error=$2,finished_at=now(),lease_owner=NULL,lease_expires_at=NULL,version=version+1 WHERE id=$1 AND lease_owner=$3`, step.Step.ID, message, workerID)
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `UPDATE executions SET status='failed',finished_at=now(),last_error=$2,updated_at=now(),version=version+1 WHERE id=$1`, step.Step.ExecutionID, message)
			if err != nil {
				return err
			}
		}
	}
	action := "execution_step.completed"
	if executionErr != nil {
		action = "execution_step.failed"
	}
	if err := appendAudit(ctx, tx, AuditInput{TenantID: tenantID, ActorType: "service", ActorID: workerID, Action: action, ResourceType: "execution_step", ResourceID: step.Step.ID, After: map[string]any{"status": status, "attempt": attempt}, Metadata: map[string]any{"execution_id": step.Step.ExecutionID, "step_key": step.Step.StepKey}}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func finalizeExecutionIfDone(ctx context.Context, tx pgx.Tx, executionID string) error {
	var remaining int
	var status string
	err := tx.QueryRow(ctx, `SELECT e.status,COUNT(*) FILTER (WHERE es.status NOT IN ('completed','skipped','rolled_back','canceled')) FROM executions e JOIN execution_steps es ON es.execution_id=e.id WHERE e.id=$1 GROUP BY e.status`, executionID).Scan(&status, &remaining)
	if err != nil {
		return err
	}
	if remaining != 0 || (status != "running" && status != "rolling_back") {
		return nil
	}
	final := "completed"
	if status == "rolling_back" {
		final = "rolled_back"
	}
	_, err = tx.Exec(ctx, `UPDATE executions SET status=$2,finished_at=now(),updated_at=now(),version=version+1 WHERE id=$1`, executionID, final)
	return err
}

func retryBackoff(retry runbooks.RetryParsed, attempt int, seed string) time.Duration {
	base := float64(retry.InitialBackoffSeconds) * math.Pow(2, float64(maxInt(0, attempt-1)))
	if base > float64(retry.MaxBackoffSeconds) {
		base = float64(retry.MaxBackoffSeconds)
	}
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", seed, attempt)))
	jitter := 0.75 + (float64(hash[0])/255.0)*0.5
	return time.Duration(base*jitter) * time.Second
}
func durationInterval(value time.Duration) string { return fmt.Sprintf("%f seconds", value.Seconds()) }
func nullJSON(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	return value
}
func scalarString(value any) (string, bool) {
	switch value.(type) {
	case string, bool, float64, nil:
		return fmt.Sprint(value), true
	default:
		return "", false
	}
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

type executionStepScanner interface{ Scan(...any) error }

func scanExecutionStep(row executionStepScanner, step *ExecutionStep) error {
	return row.Scan(&step.ID, &step.ExecutionID, &step.Position, &step.StepKey, &step.Name, &step.StepType, &step.ImmutableInput, &step.Status, &step.TimeoutSeconds, &step.RetryPolicy, &step.AttemptCount, &step.Output, &step.SanitizedError, &step.CorrelationID, &step.IdempotencyKey, &step.StartedAt, &step.FinishedAt, &step.NextAttemptAt, &step.WaitUntil, &step.LeaseOwner, &step.LeaseExpiresAt, &step.RollbackDefinition, &step.IsRollback, &step.RollbackOf, &step.Version)
}
func scanExecutionStepWithContext(row executionStepScanner, item *ClaimedStep) error {
	return row.Scan(&item.Step.ID, &item.Step.ExecutionID, &item.Step.Position, &item.Step.StepKey, &item.Step.Name, &item.Step.StepType, &item.Step.ImmutableInput, &item.Step.Status, &item.Step.TimeoutSeconds, &item.Step.RetryPolicy, &item.Step.AttemptCount, &item.Step.Output, &item.Step.SanitizedError, &item.Step.CorrelationID, &item.Step.IdempotencyKey, &item.Step.StartedAt, &item.Step.FinishedAt, &item.Step.NextAttemptAt, &item.Step.WaitUntil, &item.Step.LeaseOwner, &item.Step.LeaseExpiresAt, &item.Step.RollbackDefinition, &item.Step.IsRollback, &item.Step.RollbackOf, &item.Step.Version, &item.TenantID, &item.IncidentID, &item.DryRun, &item.ExecutionInput)
}

func (s *Store) MarkExecutionStepAmbiguous(ctx context.Context, step ClaimedStep, workerID string, executionErr error) error {
	message := "remote outcome is ambiguous"
	if executionErr != nil {
		message = truncate(executionErr.Error(), 1000)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	command, err := tx.Exec(ctx, `UPDATE execution_steps SET status='ambiguous',sanitized_error=$2,finished_at=now(),lease_owner=NULL,lease_expires_at=NULL,version=version+1 WHERE id=$1 AND lease_owner=$3 AND status IN ('running','rolling_back')`, step.Step.ID, message, workerID)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return fmt.Errorf("step lease lost")
	}
	_, err = tx.Exec(ctx, `UPDATE executions SET status='ambiguous',last_error=$2,updated_at=now(),version=version+1 WHERE id=$1`, step.Step.ExecutionID, message)
	if err != nil {
		return err
	}
	if err := appendAudit(ctx, tx, AuditInput{TenantID: step.TenantID, ActorType: "service", ActorID: workerID, Action: "execution_step.ambiguous", ResourceType: "execution_step", ResourceID: step.Step.ID, Metadata: map[string]any{"execution_id": step.Step.ExecutionID, "step_key": step.Step.StepKey, "error": message}}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
