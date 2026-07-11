package messaging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/DizzyZ7/StormRelay/internal/events"
	"github.com/DizzyZ7/StormRelay/internal/telemetry"
	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel/trace"
)

const defaultMaxDeliveries = 5

type Bus struct {
	nc                        *nats.Conn
	js                        nats.JetStreamContext
	stream, subject, consumer string
}

func Connect(url, stream, subject, consumer, service string) (*Bus, error) {
	nc, err := nats.Connect(url, nats.Name(service), nats.Timeout(5*time.Second), nats.MaxReconnects(-1), nats.ReconnectWait(time.Second))
	if err != nil {
		return nil, fmt.Errorf("connect NATS: %w", err)
	}
	js, err := nc.JetStream(nats.PublishAsyncMaxPending(256))
	if err != nil {
		nc.Close()
		return nil, err
	}
	b := &Bus{nc: nc, js: js, stream: stream, subject: subject, consumer: consumer}
	if err := b.ensureStream(); err != nil {
		nc.Close()
		return nil, err
	}
	return b, nil
}
func (b *Bus) ensureStream() error {
	cfg := &nats.StreamConfig{Name: b.stream, Subjects: []string{b.subject, b.subject + ".dlq"}, Storage: nats.FileStorage, Retention: nats.LimitsPolicy, MaxAge: 7 * 24 * time.Hour, Duplicates: 15 * time.Minute, Discard: nats.DiscardOld}
	info, err := b.js.StreamInfo(b.stream)
	if err == nats.ErrStreamNotFound {
		_, err = b.js.AddStream(cfg)
		return err
	}
	if err != nil {
		return err
	}
	if !contains(info.Config.Subjects, b.subject) {
		info.Config.Subjects = append(info.Config.Subjects, b.subject, b.subject+".dlq")
		_, err = b.js.UpdateStream(&info.Config)
	}
	return err
}
func (b *Bus) Close() { _ = b.nc.Drain(); b.nc.Close() }
func (b *Bus) Ready() error {
	if !b.nc.IsConnected() {
		return fmt.Errorf("NATS is not connected")
	}
	_, err := b.js.StreamInfo(b.stream)
	return err
}
func (b *Bus) PublishEvent(ctx context.Context, e events.Event, msgID string) error {
	publishCtx, span := telemetry.StartOperationSpan(ctx, "stormrelay.nats.publish", trace.SpanKindProducer,
		telemetry.StringAttribute("messaging.system", "nats"),
		telemetry.StringAttribute("messaging.operation.name", "publish"),
		telemetry.StringAttribute("messaging.destination.name", b.subject),
	)
	defer span.End()

	// Continue the producer span in the persisted envelope so the worker consumer
	// is a direct child of the actual JetStream publish operation.
	e.TraceParent = telemetry.TraceParentFromContext(publishCtx)
	data, err := json.Marshal(e)
	if err != nil {
		telemetry.MarkSpanError(span)
		telemetry.SetSpanOutcome(span, "marshal_failed")
		return err
	}
	msg := nats.NewMsg(b.subject)
	msg.Data = data
	msg.Header.Set(nats.MsgIdHdr, msgID)
	msg.Header.Set("Content-Type", "application/json")
	msg.Header.Set("X-StormRelay-Schema", events.SchemaVersion)
	ack, err := b.js.PublishMsg(msg, nats.Context(publishCtx))
	if err != nil {
		telemetry.MarkSpanError(span)
		telemetry.SetSpanOutcome(span, "publish_failed")
		return fmt.Errorf("publish event: %w", err)
	}
	if ack == nil || ack.Stream != b.stream {
		telemetry.MarkSpanError(span)
		telemetry.SetSpanOutcome(span, "invalid_ack")
		return fmt.Errorf("invalid JetStream publish acknowledgement")
	}
	telemetry.SetSpanOutcome(span, "accepted")
	return nil
}
func (b *Bus) Subscription() (*nats.Subscription, error) {
	return b.SubscriptionWithMaxDeliveries(defaultMaxDeliveries)
}
func (b *Bus) SubscriptionWithMaxDeliveries(maxDeliveries int) (*nats.Subscription, error) {
	if maxDeliveries < 2 {
		maxDeliveries = defaultMaxDeliveries
	}
	cfg := &nats.ConsumerConfig{
		Durable:       b.consumer,
		AckPolicy:     nats.AckExplicitPolicy,
		AckWait:       30 * time.Second,
		MaxDeliver:    maxDeliveries,
		MaxAckPending: 256,
		FilterSubject: b.subject,
		ReplayPolicy:  nats.ReplayInstantPolicy,
	}
	if err := b.reconcileConsumer(cfg); err != nil {
		return nil, fmt.Errorf("reconcile JetStream consumer: %w", err)
	}
	return b.js.PullSubscribe(b.subject, b.consumer, nats.Bind(b.stream, b.consumer))
}
func (b *Bus) reconcileConsumer(cfg *nats.ConsumerConfig) error {
	if _, err := b.js.UpdateConsumer(b.stream, cfg); err == nil {
		return nil
	} else if !errors.Is(err, nats.ErrConsumerNotFound) {
		return err
	}
	if _, err := b.js.AddConsumer(b.stream, cfg); err == nil {
		return nil
	} else if !errors.Is(err, nats.ErrConsumerNameAlreadyInUse) {
		return err
	}
	// Another worker may create the durable between the failed update and add.
	// Re-run the update so all replicas converge on the configured retry policy.
	_, err := b.js.UpdateConsumer(b.stream, cfg)
	return err
}
func (b *Bus) PublishDLQ(ctx context.Context, original *nats.Msg, reason string) error {
	publishCtx, span := telemetry.StartOperationSpan(ctx, "stormrelay.nats.publish_dlq", trace.SpanKindProducer,
		telemetry.StringAttribute("messaging.system", "nats"),
		telemetry.StringAttribute("messaging.operation.name", "publish"),
		telemetry.StringAttribute("messaging.destination.name", b.subject+".dlq"),
	)
	defer span.End()

	msg := nats.NewMsg(b.subject + ".dlq")
	msg.Data = original.Data
	msg.Header.Set("X-StormRelay-Failure", truncate(reason, 500))
	if metadata, err := original.Metadata(); err == nil {
		msg.Header.Set("X-StormRelay-Deliveries", fmt.Sprint(metadata.NumDelivered))
	}
	_, err := b.js.PublishMsg(msg, nats.Context(publishCtx))
	if err != nil {
		telemetry.MarkSpanError(span)
		telemetry.SetSpanOutcome(span, "publish_failed")
		return err
	}
	telemetry.SetSpanOutcome(span, "accepted")
	return nil
}
func (b *Bus) ConsumerLag() uint64 {
	info, err := b.js.ConsumerInfo(b.stream, b.consumer)
	if err != nil {
		return 0
	}
	return info.NumPending
}
func contains(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}
func truncate(v string, n int) string {
	if len(v) > n {
		return v[:n]
	}
	return v
}
