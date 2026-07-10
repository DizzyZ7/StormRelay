package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/DizzyZ7/StormRelay/internal/config"
	"github.com/DizzyZ7/StormRelay/internal/events"
	"github.com/DizzyZ7/StormRelay/internal/messaging"
	"github.com/DizzyZ7/StormRelay/internal/notifications"
	"github.com/DizzyZ7/StormRelay/internal/storage"
	"github.com/DizzyZ7/StormRelay/internal/telemetry"
	"github.com/nats-io/nats.go"
)

type Worker struct {
	cfg      config.Config
	store    *storage.Store
	bus      *messaging.Bus
	notifier *notifications.Notifier
	metrics  *telemetry.Metrics
	logger   *slog.Logger
}

func New(cfg config.Config, store *storage.Store, bus *messaging.Bus, notifier *notifications.Notifier, metrics *telemetry.Metrics, logger *slog.Logger) *Worker {
	return &Worker{cfg: cfg, store: store, bus: bus, notifier: notifier, metrics: metrics, logger: logger}
}

func (w *Worker) Run(ctx context.Context) error {
	sub, err := w.bus.Subscription()
	if err != nil {
		return fmt.Errorf("create subscription: %w", err)
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, w.cfg.WorkerConcurrency)
	deliveryDone := make(chan struct{})
	go func() { defer close(deliveryDone); w.deliveryLoop(ctx) }()
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			<-deliveryDone
			return nil
		default:
		}
		messages, err := sub.Fetch(w.cfg.WorkerConcurrency, nats.MaxWait(time.Second))
		if err != nil {
			if errors.Is(err, nats.ErrTimeout) {
				continue
			}
			if ctx.Err() != nil {
				continue
			}
			w.logger.Error("fetch events failed", "error", err)
			time.Sleep(time.Second)
			continue
		}
		for _, msg := range messages {
			sem <- struct{}{}
			wg.Add(1)
			go func(m *nats.Msg) { defer func() { <-sem; wg.Done() }(); w.handleMessage(ctx, m) }(msg)
		}
	}
}

func (w *Worker) handleMessage(ctx context.Context, msg *nats.Msg) {
	started := time.Now()
	var event events.Event
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		w.failMessage(msg, fmt.Errorf("decode event: %w", err))
		return
	}
	result, err := w.store.ProcessEvent(ctx, event, storage.ProcessOptions{DedupeWindow: w.cfg.DedupeWindow, CorrelationWindow: w.cfg.CorrelationWindow, ActorID: "stormrelay-worker", PublicBaseURL: w.cfg.PublicBaseURL})
	if err != nil {
		w.failMessage(msg, err)
		return
	}
	if result.Duplicate {
		w.metrics.DuplicateEvents.Add(1)
	} else {
		w.metrics.OpenIncidents.Store(max64(1, w.metrics.OpenIncidents.Load()))
	}
	w.metrics.ObserveEventLatency(time.Since(started))
	if err := msg.AckSync(); err != nil {
		w.logger.Warn("event processed but acknowledgement failed", "event_id", event.ID, "error", err)
		return
	}
	w.logger.Info("event processed", "event_id", event.ID, "normalized_event_id", result.EventID, "incident_id", result.Incident.ID, "duplicate", result.Duplicate, "duration_ms", time.Since(started).Milliseconds())
}
func (w *Worker) failMessage(msg *nats.Msg, err error) {
	metadata, metaErr := msg.Metadata()
	deliveries := uint64(1)
	if metaErr == nil {
		deliveries = metadata.NumDelivered
	}
	if deliveries >= 5 {
		if dlqErr := w.bus.PublishDLQ(msg, err.Error()); dlqErr != nil {
			w.logger.Error("publish DLQ failed", "error", dlqErr)
			_ = msg.NakWithDelay(30 * time.Second)
			return
		}
		_ = msg.Ack()
		w.logger.Error("poison event moved to DLQ", "deliveries", deliveries, "error", sanitize(err.Error()))
		return
	}
	delay := time.Duration(1<<min(int(deliveries), 6)) * time.Second
	_ = msg.NakWithDelay(delay)
	w.logger.Warn("event processing failed; scheduled retry", "deliveries", deliveries, "retry_in", delay.String(), "error", sanitize(err.Error()))
}

func (w *Worker) deliveryLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			deliveries, err := w.store.ClaimDeliveries(ctx, 20)
			if err != nil {
				w.logger.Error("claim deliveries failed", "error", err)
				continue
			}
			for _, delivery := range deliveries {
				ref, deliverErr := w.notifier.Deliver(ctx, delivery)
				if err := w.store.CompleteDelivery(ctx, delivery.ID, ref, deliverErr); err != nil {
					w.logger.Error("update delivery failed", "delivery_id", delivery.ID, "error", err)
				}
				if deliverErr != nil {
					w.metrics.NotificationFailures.Add(1)
					w.logger.Warn("notification delivery failed", "delivery_id", delivery.ID, "kind", delivery.Kind, "error", sanitize(deliverErr.Error()))
				}
			}
		}
	}
}
func sanitize(v string) string {
	if len(v) > 500 {
		return v[:500]
	}
	return v
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
