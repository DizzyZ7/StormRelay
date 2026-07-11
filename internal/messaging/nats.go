package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/DizzyZ7/StormRelay/internal/events"
	"github.com/nats-io/nats.go"
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
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	msg := nats.NewMsg(b.subject)
	msg.Data = data
	msg.Header.Set(nats.MsgIdHdr, msgID)
	msg.Header.Set("Content-Type", "application/json")
	msg.Header.Set("X-StormRelay-Schema", events.SchemaVersion)
	ack, err := b.js.PublishMsg(msg, nats.Context(ctx))
	if err != nil {
		return fmt.Errorf("publish event: %w", err)
	}
	if ack == nil || ack.Stream != b.stream {
		return fmt.Errorf("invalid JetStream publish acknowledgement")
	}
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
	if _, err := b.js.AddConsumer(b.stream, cfg); err != nil {
		return nil, fmt.Errorf("reconcile JetStream consumer: %w", err)
	}
	return b.js.PullSubscribe(b.subject, b.consumer, nats.Bind(b.stream, b.consumer))
}
func (b *Bus) PublishDLQ(original *nats.Msg, reason string) error {
	msg := nats.NewMsg(b.subject + ".dlq")
	msg.Data = original.Data
	msg.Header.Set("X-StormRelay-Failure", truncate(reason, 500))
	if metadata, err := original.Metadata(); err == nil {
		msg.Header.Set("X-StormRelay-Deliveries", fmt.Sprint(metadata.NumDelivered))
	}
	_, err := b.js.PublishMsg(msg)
	return err
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
