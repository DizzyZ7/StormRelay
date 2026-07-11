package worker

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
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

const (
	defaultEventMaxDeliveries  = 5
	defaultEventRetryBaseDelay = time.Second
	defaultEventRetryMaxDelay  = 30 * time.Second
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
	sub, err := w.bus.SubscriptionWithMaxDeliveries(w.eventMaxDeliveries())
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
			telemetry.Log(ctx, w.logger, slog.LevelError, "fetch events failed", "error", err)
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
		w.failMessage(ctx, msg, fmt.Errorf("decode event: %w", err))
		return
	}
	messageCtx, span := telemetry.StartEventConsumerSpan(ctx, event.TraceParent, event.ID, event.Type, event.Source)
	if event.RequestID != "" {
		messageCtx = telemetry.WithRequestID(messageCtx, event.RequestID)
	}
	defer span.End()
	result, err := w.store.ProcessEvent(messageCtx, event, storage.ProcessOptions{DedupeWindow: w.cfg.DedupeWindow, CorrelationWindow: w.cfg.CorrelationWindow, ActorID: "stormrelay-worker", PublicBaseURL: w.cfg.PublicBaseURL})
	if err != nil {
		telemetry.RecordSpanError(span, err)
		w.failMessage(messageCtx, msg, err)
		return
	}
	telemetry.SetSpanBool(span, "stormrelay.event.duplicate", result.Duplicate)
	telemetry.SetSpanInt(span, "stormrelay.notification.enqueued_count", len(result.NotificationIDs))
	if result.Duplicate {
		w.metrics.DuplicateEvents.Add(1)
	} else {
		w.metrics.OpenIncidents.Store(max64(1, w.metrics.OpenIncidents.Load()))
	}
	w.metrics.ObserveEventLatency(time.Since(started))
	_, ackSpan := telemetry.StartInternalSpan(messageCtx, "stormrelay.event.ack")
	ackErr := msg.AckSync()
	telemetry.EndSpan(ackSpan, ackErr)
	if ackErr != nil {
		telemetry.RecordSpanError(span, ackErr)
		telemetry.Log(messageCtx, w.logger, slog.LevelWarn, "event processed but acknowledgement failed", "event_id", event.ID, "error", ackErr)
		return
	}
	telemetry.Log(messageCtx, w.logger, slog.LevelInfo, "event processed", "event_id", event.ID, "normalized_event_id", result.EventID, "incident_id", result.Incident.ID, "duplicate", result.Duplicate, "duration_ms", time.Since(started).Milliseconds())
}
func (w *Worker) failMessage(ctx context.Context, msg *nats.Msg, err error) {
	metadata, metaErr := msg.Metadata()
	deliveries := uint64(1)
	if metaErr == nil {
		deliveries = metadata.NumDelivered
	}
	if deliveries >= uint64(w.eventMaxDeliveries()) {
		_, dlqSpan := telemetry.StartInternalSpan(ctx, "stormrelay.event.publish_dlq")
		dlqErr := w.bus.PublishDLQ(msg, err.Error())
		telemetry.EndSpan(dlqSpan, dlqErr)
		if dlqErr != nil {
			telemetry.Log(ctx, w.logger, slog.LevelError, "publish DLQ failed", "error", dlqErr)
			_ = msg.NakWithDelay(w.eventRetryMaxDelay())
			return
		}
		_ = msg.Ack()
		telemetry.Log(ctx, w.logger, slog.LevelError, "poison event moved to DLQ", "deliveries", deliveries, "error", sanitize(err.Error()))
		return
	}
	delay := retryDelay(msg.Data, deliveries, w.eventRetryBaseDelay(), w.eventRetryMaxDelay())
	_ = msg.NakWithDelay(delay)
	telemetry.Log(ctx, w.logger, slog.LevelWarn, "event processing failed; scheduled retry", "deliveries", deliveries, "retry_in", delay.String(), "error", sanitize(err.Error()))
}

func (w *Worker) deliveryLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			claimCtx, claimSpan := telemetry.StartDatabaseSpan(ctx, "claim_deliveries")
			deliveries, err := w.store.ClaimDeliveries(claimCtx, 20)
			telemetry.SetSpanInt(claimSpan, "stormrelay.notification.claimed_count", len(deliveries))
			telemetry.EndSpan(claimSpan, err)
			if err != nil {
				telemetry.Log(ctx, w.logger, slog.LevelError, "claim deliveries failed", "error", err)
				continue
			}
			for _, delivery := range deliveries {
				deliveryCtx, deliverySpan := telemetry.StartNotificationSpan(ctx, "deliver", delivery.Kind)
				telemetry.SetSpanInt(deliverySpan, "stormrelay.notification.attempt", delivery.Attempt)
				ref, deliverErr := w.notifier.Deliver(deliveryCtx, delivery)
				completeCtx, completeSpan := telemetry.StartDatabaseSpan(deliveryCtx, "complete_delivery")
				completeErr := w.store.CompleteDelivery(completeCtx, delivery.ID, ref, deliverErr)
				telemetry.EndSpan(completeSpan, completeErr)
				telemetry.EndSpan(deliverySpan, errors.Join(deliverErr, completeErr))
				if completeErr != nil {
					telemetry.Log(ctx, w.logger, slog.LevelError, "update delivery failed", "delivery_id", delivery.ID, "error", completeErr)
				}
				if deliverErr != nil {
					w.metrics.NotificationFailures.Add(1)
					telemetry.Log(ctx, w.logger, slog.LevelWarn, "notification delivery failed", "delivery_id", delivery.ID, "kind", delivery.Kind, "error", sanitize(deliverErr.Error()))
				}
			}
		}
	}
}

func (w *Worker) eventMaxDeliveries() int {
	if w.cfg.EventMaxDeliveries < 2 {
		return defaultEventMaxDeliveries
	}
	return w.cfg.EventMaxDeliveries
}
func (w *Worker) eventRetryBaseDelay() time.Duration {
	if w.cfg.EventRetryBaseDelay <= 0 {
		return defaultEventRetryBaseDelay
	}
	return w.cfg.EventRetryBaseDelay
}
func (w *Worker) eventRetryMaxDelay() time.Duration {
	base := w.eventRetryBaseDelay()
	if w.cfg.EventRetryMaxDelay < base {
		return defaultEventRetryMaxDelay
	}
	return w.cfg.EventRetryMaxDelay
}

// retryDelay applies capped exponential backoff with deterministic jitter. The
// deterministic seed keeps failure tests reproducible while preventing a fleet
// of workers handling the same delivery number from retrying in lockstep.
func retryDelay(payload []byte, deliveries uint64, base, maximum time.Duration) time.Duration {
	if base <= 0 {
		base = defaultEventRetryBaseDelay
	}
	if maximum < base {
		maximum = defaultEventRetryMaxDelay
	}
	exponent := deliveries
	if exponent > 20 {
		exponent = 20
	}
	delay := base
	for i := uint64(1); i < exponent && delay < maximum; i++ {
		if delay > maximum/2 {
			delay = maximum
			break
		}
		delay *= 2
	}
	if delay > maximum {
		delay = maximum
	}
	seedInput := make([]byte, len(payload)+8)
	copy(seedInput, payload)
	binary.BigEndian.PutUint64(seedInput[len(payload):], deliveries)
	sum := sha256.Sum256(seedInput)
	permille := int64(800 + binary.BigEndian.Uint16(sum[:2])%401)
	jittered := time.Duration(int64(delay) * permille / 1000)
	if jittered > maximum {
		return maximum
	}
	if jittered < time.Millisecond {
		return time.Millisecond
	}
	return jittered
}
func sanitize(v string) string {
	if len(v) > 500 {
		return v[:500]
	}
	return v
}
func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
