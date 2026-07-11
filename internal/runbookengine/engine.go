package runbookengine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/DizzyZ7/StormRelay/internal/config"
	"github.com/DizzyZ7/StormRelay/internal/id"
	"github.com/DizzyZ7/StormRelay/internal/networkguard"
	pluginprotocol "github.com/DizzyZ7/StormRelay/internal/plugins"
	"github.com/DizzyZ7/StormRelay/internal/runbooks"
	"github.com/DizzyZ7/StormRelay/internal/storage"
	"github.com/DizzyZ7/StormRelay/internal/telemetry"
	"go.opentelemetry.io/otel/trace"
)

const maxHTTPResponseBytes = 1 << 20

type Engine struct {
	cfg          config.Config
	store        *storage.Store
	httpGuard    *networkguard.Guard
	pluginClient *pluginprotocol.Client
	metrics      *telemetry.Metrics
	logger       *slog.Logger
	workerID     string
}

func New(cfg config.Config, store *storage.Store, metrics *telemetry.Metrics, logger *slog.Logger) *Engine {
	workerID, err := id.New()
	if err != nil {
		workerID = fmt.Sprintf("worker-%d", time.Now().UnixNano())
	}
	return &Engine{
		cfg:          cfg,
		store:        store,
		httpGuard:    networkguard.New(cfg.RunbookHTTPAllowedHosts),
		pluginClient: pluginprotocol.NewClient(cfg.PluginAllowedHosts),
		metrics:      metrics,
		logger:       logger,
		workerID:     "runbook-worker-" + workerID,
	}
}

func (e *Engine) Run(ctx context.Context) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	sem := make(chan struct{}, e.cfg.RunbookConcurrency)
	var wg sync.WaitGroup
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return nil
		case <-ticker.C:
			if _, err := e.store.RecoverExpiredStepLeases(ctx, e.workerID); err != nil {
				e.logger.Error("recover runbook leases", "error", err)
			}
			if _, err := e.store.AdvanceRunbookTimers(ctx, e.workerID); err != nil {
				e.logger.Error("advance runbook timers", "error", err)
			}
			available := cap(sem) - len(sem)
			if available < 1 {
				continue
			}
			steps, err := e.store.ClaimExecutionSteps(ctx, e.workerID, available, 30*time.Second)
			if err != nil {
				e.logger.Error("claim runbook steps", "error", err)
				continue
			}
			for _, step := range steps {
				step := step
				sem <- struct{}{}
				wg.Add(1)
				go func() {
					defer func() { <-sem; wg.Done() }()
					e.executeTraced(ctx, step)
				}()
			}
		}
	}
}

func (e *Engine) executeTraced(parent context.Context, claimed storage.ClaimedStep) {
	parent = telemetry.ContextWithTraceParent(parent, executionTraceParent(claimed.ExecutionInput))
	stepCtx, span := telemetry.StartOperationSpan(parent, "stormrelay.runbook.step", trace.SpanKindConsumer,
		telemetry.StringAttribute("stormrelay.runbook.step_type", claimed.Step.StepType),
		telemetry.IntAttribute("stormrelay.runbook.attempt", claimed.Step.AttemptCount),
		telemetry.BoolAttribute("stormrelay.runbook.rollback", claimed.Step.IsRollback),
		telemetry.BoolAttribute("stormrelay.runbook.dry_run", claimed.DryRun),
	)
	e.execute(stepCtx, claimed)

	outcome := "unknown"
	execution, err := e.store.GetExecution(stepCtx, claimed.TenantID, claimed.Step.ExecutionID)
	if err != nil {
		telemetry.MarkSpanError(span)
		outcome = "status_unavailable"
	} else {
		for _, step := range execution.Steps {
			if step.ID == claimed.Step.ID {
				outcome = step.Status
				if step.Status == "failed" || step.Status == "ambiguous" {
					telemetry.MarkSpanError(span)
				}
				break
			}
		}
	}
	telemetry.SetSpanOutcome(span, outcome)
	span.End()
}

func executionTraceParent(input json.RawMessage) string {
	var snapshot struct {
		TraceParent string `json:"traceparent"`
	}
	if json.Unmarshal(input, &snapshot) != nil {
		return ""
	}
	return snapshot.TraceParent
}

func (e *Engine) execute(parent context.Context, claimed storage.ClaimedStep) {
	started := time.Now()
	var snapshot runbooks.Snapshot
	if err := json.Unmarshal(claimed.Step.ImmutableInput, &snapshot); err != nil {
		e.completeFailure(parent, claimed, fmt.Errorf("decode immutable step snapshot: %w", err))
		return
	}
	if snapshot.Step.When != nil {
		inputs, err := e.store.ExecutionConditionInputs(parent, claimed.Step.ExecutionID)
		if err != nil {
			e.completeFailure(parent, claimed, err)
			return
		}
		if !conditionMatches(*snapshot.Step.When, inputs) {
			output, _ := json.Marshal(map[string]any{"skipped": true, "reason": "condition did not match"})
			if err := e.store.CompleteExecutionStep(parent, claimed, e.workerID, "skipped", output, nil); err != nil {
				e.logger.Error("complete skipped runbook step", "step_id", claimed.Step.ID, "error", err)
			}
			return
		}
	}
	if claimed.DryRun {
		output, _ := json.Marshal(map[string]any{"dry_run": true, "step_type": claimed.Step.StepType, "would_execute": true})
		status := "completed"
		if claimed.Step.IsRollback {
			status = "rolled_back"
		}
		if err := e.store.CompleteExecutionStep(parent, claimed, e.workerID, status, output, nil); err != nil {
			e.logger.Error("complete dry-run step", "step_id", claimed.Step.ID, "error", err)
		}
		return
	}

	timeout := time.Duration(snapshot.TimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	var output json.RawMessage
	var err error
	ambiguous := false

	switch snapshot.Step.Type {
	case "wait":
		duration, parseErr := time.ParseDuration(snapshot.Step.With.Duration)
		if parseErr != nil {
			err = parseErr
			break
		}
		err = e.store.ScheduleWait(ctx, claimed, time.Now().UTC().Add(duration), e.workerID)
		if err == nil {
			return
		}
	case "approval":
		var expires *time.Time
		if snapshot.Step.With.ExpiresIn != "" {
			duration, parseErr := time.ParseDuration(snapshot.Step.With.ExpiresIn)
			if parseErr != nil {
				err = parseErr
				break
			}
			value := time.Now().UTC().Add(duration)
			expires = &value
		}
		_, err = e.store.OpenApproval(ctx, claimed, e.workerID, snapshot.Step.With.Prompt, expires)
		if err == nil {
			return
		}
	case "http":
		output, ambiguous, err = e.executeHTTP(ctx, claimed, snapshot.Step.With)
	case "plugin":
		output, err = e.executePlugin(ctx, claimed, snapshot.Step.With)
	default:
		err = fmt.Errorf("unsupported step type %q", snapshot.Step.Type)
	}

	if ambiguous {
		if markErr := e.store.MarkExecutionStepAmbiguous(parent, claimed, e.workerID, err); markErr != nil {
			e.logger.Error("mark runbook step ambiguous", "step_id", claimed.Step.ID, "error", markErr)
		} else {
			e.observeTerminal(parent, claimed)
		}
		e.metrics.RunbookFailures.Add(1)
		return
	}
	if err != nil {
		e.completeFailure(parent, claimed, err)
		return
	}
	status := "completed"
	if claimed.Step.IsRollback {
		status = "rolled_back"
	}
	if err := e.store.CompleteExecutionStep(parent, claimed, e.workerID, status, output, nil); err != nil {
		e.logger.Error("complete runbook step", "step_id", claimed.Step.ID, "error", err)
		return
	}
	e.observeTerminal(parent, claimed)
	e.logger.Info("runbook step completed", "execution_id", claimed.Step.ExecutionID, "step_id", claimed.Step.ID, "step_key", claimed.Step.StepKey, "duration_ms", time.Since(started).Milliseconds())
}

func (e *Engine) executeHTTP(ctx context.Context, claimed storage.ClaimedStep, input runbooks.Input) (output json.RawMessage, ambiguous bool, err error) {
	method := strings.ToUpper(strings.TrimSpace(input.Method))
	if method == "" {
		method = http.MethodPost
	}
	actionCtx, span := telemetry.StartOperationSpan(ctx, "stormrelay.runbook.http", trace.SpanKindClient,
		telemetry.StringAttribute("http.request.method", method),
		telemetry.BoolAttribute("stormrelay.runbook.idempotent", input.IdempotencyHeader != ""),
	)
	defer func() {
		if err != nil {
			telemetry.MarkSpanError(span)
			telemetry.SetSpanOutcome(span, "failed")
		} else {
			telemetry.SetSpanOutcome(span, "succeeded")
		}
		span.End()
	}()

	client, safeURL, err := e.httpGuard.Client(actionCtx, input.URL, time.Until(deadlineOr(actionCtx, time.Now().Add(30*time.Second))))
	if err != nil {
		return nil, false, err
	}
	body, err := marshalOptional(input.Body)
	if err != nil {
		return nil, false, err
	}
	req, err := http.NewRequestWithContext(actionCtx, method, safeURL.String(), bytes.NewReader(body))
	if err != nil {
		return nil, false, err
	}
	for name, value := range input.Headers {
		req.Header.Set(name, value)
	}
	if len(body) > 0 && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if input.IdempotencyHeader != "" {
		req.Header.Set(input.IdempotencyHeader, claimed.Step.IdempotencyKey)
	}
	req.Header.Set("X-StormRelay-Execution-ID", claimed.Step.ExecutionID)
	req.Header.Set("X-StormRelay-Step-ID", claimed.Step.ID)
	req.Header.Set("X-StormRelay-Correlation-ID", claimed.Step.CorrelationID)
	telemetry.InjectHTTPTrace(actionCtx, req.Header)
	resp, err := client.Do(req)
	if err != nil {
		return nil, input.IdempotencyHeader == "", fmt.Errorf("HTTP action failed: %w", err)
	}
	defer resp.Body.Close()
	span.SetAttributes(telemetry.IntAttribute("http.response.status_code", resp.StatusCode))
	responseBody, err := readBounded(resp.Body, maxHTTPResponseBytes)
	if err != nil {
		return nil, input.IdempotencyHeader == "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, input.IdempotencyHeader == "", fmt.Errorf("HTTP action returned status %d", resp.StatusCode)
	}
	var response any
	if len(responseBody) > 0 && json.Unmarshal(responseBody, &response) != nil {
		response = string(responseBody)
	}
	return mustJSON(map[string]any{"status_code": resp.StatusCode, "response": response}), false, nil
}

func (e *Engine) executePlugin(ctx context.Context, claimed storage.ClaimedStep, input runbooks.Input) (json.RawMessage, error) {
	plugin, bearer, err := e.store.PluginCredentialForAction(ctx, claimed.TenantID, input.Plugin, input.Action)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(input.Input)
	if err != nil {
		return nil, err
	}
	requestID, _ := id.New()
	response, err := e.pluginClient.Call(ctx, plugin.Endpoint, input.Action, bearer, pluginprotocol.ActionRequest{
		ProtocolVersion: pluginprotocol.ProtocolVersion,
		ExecutionID:     claimed.Step.ExecutionID,
		StepID:          claimed.Step.ID,
		RequestID:       requestID,
		TraceParent:     telemetry.TraceParentFromContext(ctx),
		Deadline:        deadlineOr(ctx, time.Now().Add(time.Duration(plugin.TimeoutSeconds)*time.Second)),
		IdempotencyKey:  claimed.Step.IdempotencyKey,
		Input:           payload,
	}, time.Duration(plugin.TimeoutSeconds)*time.Second)
	if err != nil {
		return nil, err
	}
	return mustJSON(map[string]any{"status": response.Status, "output": json.RawMessage(response.Output)}), nil
}

func (e *Engine) completeFailure(ctx context.Context, claimed storage.ClaimedStep, err error) {
	e.metrics.RunbookFailures.Add(1)
	if completeErr := e.store.CompleteExecutionStep(ctx, claimed, e.workerID, "failed", nil, err); completeErr != nil {
		e.logger.Error("complete failed runbook step", "step_id", claimed.Step.ID, "error", completeErr)
		return
	}
	e.observeTerminal(ctx, claimed)
}

func (e *Engine) observeTerminal(ctx context.Context, claimed storage.ClaimedStep) {
	execution, err := e.store.GetExecution(ctx, claimed.TenantID, claimed.Step.ExecutionID)
	if err != nil || execution.StartedAt == nil || execution.FinishedAt == nil {
		return
	}
	switch execution.Status {
	case "completed", "failed", "ambiguous", "canceled", "rolled_back":
		e.metrics.ObserveRunbookDuration(execution.FinishedAt.Sub(*execution.StartedAt))
	}
}

func conditionMatches(condition runbooks.Condition, inputs map[string]string) bool {
	actual := inputs[condition.Field]
	matched := false
	if condition.Equals != nil {
		matched = actual == *condition.Equals
	} else {
		for _, value := range condition.In {
			if actual == value {
				matched = true
				break
			}
		}
	}
	if condition.Not {
		return !matched
	}
	return matched
}

func marshalOptional(value any) ([]byte, error) {
	if value == nil {
		return nil, nil
	}
	return json.Marshal(value)
}
func mustJSON(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}
func readBounded(reader io.Reader, max int64) ([]byte, error) {
	limited := &io.LimitedReader{R: reader, N: max + 1}
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("response exceeds %d bytes", max)
	}
	return data, nil
}
