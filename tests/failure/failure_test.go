//go:build failure

package failure

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DizzyZ7/StormRelay/internal/config"
	"github.com/DizzyZ7/StormRelay/internal/cryptox"
	"github.com/DizzyZ7/StormRelay/internal/messaging"
	"github.com/DizzyZ7/StormRelay/internal/notifications"
	"github.com/DizzyZ7/StormRelay/internal/storage"
	"github.com/DizzyZ7/StormRelay/internal/telemetry"
	"github.com/DizzyZ7/StormRelay/internal/worker"
	"github.com/jackc/pgx/v5"
	"github.com/nats-io/nats.go"
)

const failureTenantID = "00000000-0000-4000-8000-000000000001"

func failureDatabaseURL() string {
	if value := os.Getenv("STORMRELAY_TEST_DATABASE_URL"); value != "" {
		return value
	}
	return "postgres://stormrelay:stormrelay@localhost:5432/stormrelay?sslmode=disable"
}

func failureNATSURL() string {
	if value := os.Getenv("STORMRELAY_TEST_NATS_URL"); value != "" {
		return value
	}
	return "nats://localhost:4222"
}

func openFailureStore(t *testing.T) *storage.Store {
	t.Helper()
	box, err := cryptox.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	store, err := storage.Open(context.Background(), failureDatabaseURL(), box, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestPoisonMessageReachesDLQWithSanitizedMetadata(t *testing.T) {
	store := openFailureStore(t)
	suffix := fmt.Sprint(time.Now().UnixNano())
	stream := "FAILURE_" + suffix
	subject := "stormrelay.failure." + suffix
	consumer := "failure_worker_" + suffix
	bus, err := messaging.Connect(failureNATSURL(), stream, subject, consumer, "failure-poison-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bus.Close)

	nc, err := nats.Connect(failureNATSURL(), nats.Timeout(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := nc.JetStream()
	if err != nil {
		t.Fatal(err)
	}
	dlqConsumer := "failure_dlq_" + suffix
	dlq, err := js.PullSubscribe(subject+".dlq", dlqConsumer, nats.BindStream(stream), nats.ManualAck(), nats.AckExplicit())
	if err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg := config.Config{
		WorkerConcurrency:   1,
		EventMaxDeliveries:  3,
		EventRetryBaseDelay: 10 * time.Millisecond,
		EventRetryMaxDelay:  25 * time.Millisecond,
		DedupeWindow:        15 * time.Minute,
		CorrelationWindow:   30 * time.Minute,
		PublicBaseURL:       "http://localhost",
	}
	processor := worker.New(cfg, store, bus, notifications.New(logger, ""), &telemetry.Metrics{}, logger)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- processor.Run(ctx) }()

	poison := []byte(`{"secret_marker":"must-not-appear-in-failure-metadata"`)
	if _, err := js.Publish(subject, poison); err != nil {
		cancel()
		t.Fatal(err)
	}
	messages, err := dlq.Fetch(1, nats.MaxWait(10*time.Second))
	if err != nil {
		cancel()
		t.Fatalf("fetch DLQ message: %v", err)
	}
	message := messages[0]
	if string(message.Data) != string(poison) {
		cancel()
		t.Fatalf("DLQ did not preserve the original payload")
	}
	failure := message.Header.Get("X-StormRelay-Failure")
	if !strings.Contains(failure, "decode event") {
		cancel()
		t.Fatalf("unexpected sanitized failure metadata: %q", failure)
	}
	if strings.Contains(failure, "must-not-appear-in-failure-metadata") {
		cancel()
		t.Fatal("failure metadata leaked the poison payload")
	}
	if deliveries := message.Header.Get("X-StormRelay-Deliveries"); deliveries != "3" {
		cancel()
		t.Fatalf("deliveries=%q, want 3", deliveries)
	}
	if err := message.AckSync(); err != nil {
		cancel()
		t.Fatal(err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not stop after cancellation")
	}
}

func TestExpiredRunbookLeaseRequiresIdempotencyForAutomaticReplay(t *testing.T) {
	store := openFailureStore(t)
	ctx := context.Background()
	db, err := pgx.Connect(ctx, failureDatabaseURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })

	nonIdempotentKey := fmt.Sprintf("failure-non-idem-%d", time.Now().UnixNano())
	nonIdempotent := fmt.Sprintf(`apiVersion: stormrelay.io/v1
kind: Runbook
metadata:
  id: %s
  version: 1
spec:
  steps:
    - id: mutate
      type: http
      timeout: 10s
      retry:
        maxAttempts: 3
        initialBackoff: 1s
        maxBackoff: 5s
      http:
        method: POST
        url: https://automation.example.net/v1/mutate
        body:
          operation: irreversible
`, nonIdempotentKey)
	if _, err := store.ApplyRunbook(ctx, storage.ApplyRunbookInput{TenantID: failureTenantID, DocumentYAML: nonIdempotent, ActorID: "failure-test"}); err != nil {
		t.Fatal(err)
	}
	nonIdempotentExecution, err := store.StartExecution(ctx, storage.StartExecutionInput{TenantID: failureTenantID, RunbookKey: nonIdempotentKey, RequestedBy: "failure-test"})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimExecutionSteps(ctx, "crashed-worker", 1, 5*time.Second)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim non-idempotent step: len=%d err=%v", len(claimed), err)
	}
	nonIdempotentStepID := claimed[0].Step.ID
	if _, err := db.Exec(ctx, `UPDATE execution_steps SET lease_expires_at=now()-interval '1 second' WHERE id=$1`, nonIdempotentStepID); err != nil {
		t.Fatal(err)
	}
	if recovered, err := store.RecoverExpiredStepLeases(ctx, "recovery-worker"); err != nil || recovered != 1 {
		t.Fatalf("recover non-idempotent lease: recovered=%d err=%v", recovered, err)
	}
	nonIdempotentExecution, err = store.GetExecution(ctx, failureTenantID, nonIdempotentExecution.ID)
	if err != nil {
		t.Fatal(err)
	}
	if nonIdempotentExecution.Status != "ambiguous" || len(nonIdempotentExecution.Steps) != 1 || nonIdempotentExecution.Steps[0].Status != "ambiguous" {
		t.Fatalf("non-idempotent outcome was not fail-closed: %+v", nonIdempotentExecution)
	}
	if next, err := store.ClaimExecutionSteps(ctx, "another-worker", 10, 5*time.Second); err != nil {
		t.Fatal(err)
	} else {
		for _, step := range next {
			if step.Step.ID == nonIdempotentStepID {
				t.Fatal("ambiguous non-idempotent step was automatically replayed")
			}
		}
	}

	idempotentKey := fmt.Sprintf("failure-idem-%d", time.Now().UnixNano())
	idempotent := fmt.Sprintf(`apiVersion: stormrelay.io/v1
kind: Runbook
metadata:
  id: %s
  version: 1
spec:
  steps:
    - id: reconcile
      type: http
      timeout: 10s
      retry:
        maxAttempts: 3
        initialBackoff: 1s
        maxBackoff: 5s
      http:
        method: POST
        url: https://automation.example.net/v1/reconcile
        idempotent: true
        body:
          operation: safe-replay
`, idempotentKey)
	if _, err := store.ApplyRunbook(ctx, storage.ApplyRunbookInput{TenantID: failureTenantID, DocumentYAML: idempotent, ActorID: "failure-test"}); err != nil {
		t.Fatal(err)
	}
	idempotentExecution, err := store.StartExecution(ctx, storage.StartExecutionInput{TenantID: failureTenantID, RunbookKey: idempotentKey, RequestedBy: "failure-test"})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err = store.ClaimExecutionSteps(ctx, "crashed-worker", 1, 5*time.Second)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim idempotent step: len=%d err=%v", len(claimed), err)
	}
	originalIdempotencyKey := claimed[0].Step.IdempotencyKey
	idempotentStepID := claimed[0].Step.ID
	if _, err := db.Exec(ctx, `UPDATE execution_steps SET lease_expires_at=now()-interval '1 second' WHERE id=$1`, idempotentStepID); err != nil {
		t.Fatal(err)
	}
	if recovered, err := store.RecoverExpiredStepLeases(ctx, "recovery-worker"); err != nil || recovered != 1 {
		t.Fatalf("recover idempotent lease: recovered=%d err=%v", recovered, err)
	}
	idempotentExecution, err = store.GetExecution(ctx, failureTenantID, idempotentExecution.ID)
	if err != nil {
		t.Fatal(err)
	}
	if idempotentExecution.Status != "running" || idempotentExecution.Steps[0].Status != "retrying" {
		t.Fatalf("idempotent step was not scheduled for safe replay: %+v", idempotentExecution)
	}
	claimed, err = store.ClaimExecutionSteps(ctx, "recovery-worker", 1, 5*time.Second)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("reclaim idempotent step: len=%d err=%v", len(claimed), err)
	}
	if claimed[0].Step.ID != idempotentStepID || claimed[0].Step.IdempotencyKey != originalIdempotencyKey || claimed[0].Step.AttemptCount != 2 {
		t.Fatalf("safe replay changed the step identity: %+v", claimed[0].Step)
	}
	if err := store.CompleteExecutionStep(ctx, claimed[0], "recovery-worker", "completed", json.RawMessage(`{"replayed":true}`), nil); err != nil {
		t.Fatal(err)
	}

	var auditCount int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM audit_entries WHERE action='execution_step.lease_expired' AND resource_id IN ($1,$2)`, nonIdempotentStepID, idempotentStepID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 2 {
		t.Fatalf("lease recovery audit count=%d, want 2", auditCount)
	}
}

func TestPersistedWaitContinuesAfterControlPlaneRestart(t *testing.T) {
	first := openFailureStore(t)
	ctx := context.Background()
	runbookKey := fmt.Sprintf("failure-restart-%d", time.Now().UnixNano())
	document := fmt.Sprintf(`apiVersion: stormrelay.io/v1
kind: Runbook
metadata:
  id: %s
  version: 1
spec:
  steps:
    - id: settle
      type: wait
      wait:
        duration: 1s
    - id: approve
      type: approval
      approval:
        prompt: Continue after restart?
        expiresAfter: 15m
`, runbookKey)
	if _, err := first.ApplyRunbook(ctx, storage.ApplyRunbookInput{TenantID: failureTenantID, DocumentYAML: document, ActorID: "failure-test"}); err != nil {
		t.Fatal(err)
	}
	execution, err := first.StartExecution(ctx, storage.StartExecutionInput{TenantID: failureTenantID, RunbookKey: runbookKey, RequestedBy: "failure-test"})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := first.ClaimExecutionSteps(ctx, "worker-before-restart", 1, 30*time.Second)
	if err != nil || len(claimed) != 1 || claimed[0].Step.StepKey != "settle" {
		t.Fatalf("claim wait before restart: %+v err=%v", claimed, err)
	}
	if err := first.ScheduleWait(ctx, claimed[0], time.Now().UTC().Add(-time.Second), "worker-before-restart"); err != nil {
		t.Fatal(err)
	}

	// A second Store has no process-local execution state. Advancing through this
	// handle models a newly started control plane/worker after the first process exits.
	second := openFailureStore(t)
	if advanced, err := second.AdvanceRunbookTimers(ctx, "worker-after-restart"); err != nil || advanced < 1 {
		t.Fatalf("advance persisted wait after restart: advanced=%d err=%v", advanced, err)
	}
	claimed, err = second.ClaimExecutionSteps(ctx, "worker-after-restart", 10, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, step := range claimed {
		if step.Step.ExecutionID == execution.ID && step.Step.StepKey == "approve" {
			found = true
			if _, err := second.OpenApproval(ctx, step, "worker-after-restart", "Continue after restart?", nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !found {
		t.Fatal("approval step was not resumed from persisted state after restart")
	}
}
