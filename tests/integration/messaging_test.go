//go:build integration

package integration

import (
	"fmt"
	"testing"
	"time"

	"github.com/DizzyZ7/StormRelay/internal/messaging"
	"github.com/nats-io/nats.go"
)

func TestJetStreamConsumerMaxDeliveriesCanBeReconciled(t *testing.T) {
	suffix := fmt.Sprint(time.Now().UnixNano())
	stream := "TEST_RECONCILE_" + suffix
	subject := "stormrelay.test.reconcile." + suffix
	consumer := "consumer_reconcile_" + suffix
	bus, err := messaging.Connect(natsURL(), stream, subject, consumer, "integration-consumer-reconcile")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bus.Close)

	first, err := bus.SubscriptionWithMaxDeliveries(5)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Unsubscribe(); err != nil {
		t.Fatal(err)
	}

	nc, err := nats.Connect(natsURL(), nats.Timeout(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := nc.JetStream()
	if err != nil {
		t.Fatal(err)
	}
	info, err := js.ConsumerInfo(stream, consumer)
	if err != nil {
		t.Fatal(err)
	}
	if info.Config.MaxDeliver != 5 {
		t.Fatalf("initial max deliveries=%d, want 5", info.Config.MaxDeliver)
	}

	second, err := bus.SubscriptionWithMaxDeliveries(3)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Unsubscribe() })
	info, err = js.ConsumerInfo(stream, consumer)
	if err != nil {
		t.Fatal(err)
	}
	if info.Config.MaxDeliver != 3 {
		t.Fatalf("reconciled max deliveries=%d, want 3", info.Config.MaxDeliver)
	}
}
