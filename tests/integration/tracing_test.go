//go:build integration

package integration

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/DizzyZ7/StormRelay/internal/config"
	"github.com/DizzyZ7/StormRelay/internal/events"
	"github.com/DizzyZ7/StormRelay/internal/ingestion"
	"github.com/DizzyZ7/StormRelay/internal/messaging"
	"github.com/DizzyZ7/StormRelay/internal/notifications"
	"github.com/DizzyZ7/StormRelay/internal/storage"
	"github.com/DizzyZ7/StormRelay/internal/telemetry"
	"github.com/DizzyZ7/StormRelay/internal/worker"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestConnectedEventToNotificationTraceHasSafeAttributes(t *testing.T) {
	recorder, cleanupTracing := installIntegrationSpanRecorder(t)
	defer cleanupTracing()

	store := openStore(t)
	suffix := fmt.Sprint(time.Now().UnixNano())
	created, err := store.CreateSource(context.Background(), storage.CreateSourceInput{
		TenantID: tenantID,
		Name:     "trace-source-" + suffix,
		Kind:     "generic",
		AuthMode: ingestion.AuthNone,
	})
	if err != nil {
		t.Fatal(err)
	}
	bus, err := messaging.Connect(natsURL(), "TRACE_"+suffix, "stormrelay.trace."+suffix, "trace_consumer_"+suffix, "trace-integration")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bus.Close)

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	processor := worker.New(config.Config{
		WorkerConcurrency:   1,
		EventMaxDeliveries:  3,
		EventRetryBaseDelay: 10 * time.Millisecond,
		EventRetryMaxDelay:  50 * time.Millisecond,
		DedupeWindow:        15 * time.Minute,
		CorrelationWindow:   30 * time.Minute,
		PublicBaseURL:       "http://localhost:8080",
	}, store, bus, notifications.New(logger, ""), &telemetry.Metrics{}, logger)
	workerCtx, cancelWorker := context.WithCancel(context.Background())
	workerDone := make(chan error, 1)
	go func() { workerDone <- processor.Run(workerCtx) }()
	defer func() {
		cancelWorker()
		select {
		case err := <-workerDone:
			if err != nil {
				t.Errorf("worker stopped with error: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("worker did not stop")
		}
	}()

	const secretMarker = "never-export-this-sensitive-marker"
	body := []byte(`{"type":"com.stormrelay.trace_test","title":"trace integration alert","severity":"critical","service":"trace-service-` + suffix + `","environment":"integration","resource":"trace-resource","labels":{"secret":"` + secretMarker + `"},"authorization":"` + secretMarker + `"}`)
	event, err := events.Normalize(events.NormalizeInput{
		TenantID:      tenantID,
		SourceID:      created.Source.ID,
		SourceName:    created.Source.Name,
		ContentType:   "application/json",
		Body:          body,
		SourceEventID: "trace-event-" + suffix,
		ReceivedAt:    time.Now().UTC(),
		RequestID:     "trace-request-" + suffix,
	})
	if err != nil {
		t.Fatal(err)
	}

	rootCtx, root := otel.Tracer("stormrelay.integration").Start(context.Background(), "trace-test.ingress", trace.WithSpanKind(trace.SpanKindServer))
	rootSpanID := root.SpanContext().SpanID()
	rootTraceID := root.SpanContext().TraceID()
	if err := bus.PublishEvent(rootCtx, event, "trace-message-"+suffix); err != nil {
		root.End()
		t.Fatal(err)
	}
	root.End()

	required := []string{
		"trace-test.ingress",
		"stormrelay.nats.publish",
		"stormrelay.event.process",
		"stormrelay.event.transaction",
		"stormrelay.event.persist_raw",
		"stormrelay.event.deduplicate",
		"stormrelay.incident.correlate",
		"stormrelay.policy.evaluate",
		"stormrelay.notification.enqueue",
		"stormrelay.audit.append",
		"stormrelay.db.commit",
		"stormrelay.nats.ack",
		"stormrelay.notification.deliver",
		"stormrelay.notification.complete",
	}
	spans := waitForTraceSpanNames(t, recorder, rootTraceID, required, 12*time.Second)
	byName := map[string]sdktrace.ReadOnlySpan{}
	for _, span := range spans {
		if _, exists := byName[span.Name()]; !exists {
			byName[span.Name()] = span
		}
		assertSafeSpanData(t, span, secretMarker)
	}

	assertParent(t, byName, "stormrelay.nats.publish", rootSpanID)
	assertParent(t, byName, "stormrelay.event.process", byName["stormrelay.nats.publish"].SpanContext().SpanID())
	assertParent(t, byName, "stormrelay.event.transaction", byName["stormrelay.event.process"].SpanContext().SpanID())
	for _, child := range []string{
		"stormrelay.event.persist_raw",
		"stormrelay.event.deduplicate",
		"stormrelay.incident.correlate",
		"stormrelay.policy.evaluate",
		"stormrelay.notification.enqueue",
		"stormrelay.audit.append",
		"stormrelay.db.commit",
	} {
		assertParent(t, byName, child, byName["stormrelay.event.transaction"].SpanContext().SpanID())
	}
	assertParent(t, byName, "stormrelay.nats.ack", byName["stormrelay.event.process"].SpanContext().SpanID())
	assertParent(t, byName, "stormrelay.notification.deliver", byName["stormrelay.notification.enqueue"].SpanContext().SpanID())
	assertParent(t, byName, "stormrelay.notification.complete", byName["stormrelay.notification.enqueue"].SpanContext().SpanID())
}

func installIntegrationSpanRecorder(t *testing.T) (*tracetest.SpanRecorder, func()) {
	t.Helper()
	oldProvider := otel.GetTracerProvider()
	oldPropagator := otel.GetTextMapPropagator()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(recorder),
	)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	return recorder, func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(oldProvider)
		otel.SetTextMapPropagator(oldPropagator)
	}
}

func waitForTraceSpanNames(t *testing.T, recorder *tracetest.SpanRecorder, traceID trace.TraceID, required []string, timeout time.Duration) []sdktrace.ReadOnlySpan {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		spans := traceSpans(recorder.Ended(), traceID)
		found := map[string]bool{}
		for _, span := range spans {
			found[span.Name()] = true
		}
		complete := true
		for _, name := range required {
			if !found[name] {
				complete = false
				break
			}
		}
		if complete {
			return spans
		}
		time.Sleep(50 * time.Millisecond)
	}
	names := []string{}
	for _, span := range traceSpans(recorder.Ended(), traceID) {
		names = append(names, span.Name())
	}
	t.Fatalf("required spans were not exported for trace %s; got %v", traceID, names)
	return nil
}

func traceSpans(spans []sdktrace.ReadOnlySpan, traceID trace.TraceID) []sdktrace.ReadOnlySpan {
	out := make([]sdktrace.ReadOnlySpan, 0, len(spans))
	for _, span := range spans {
		if span.SpanContext().TraceID() == traceID {
			out = append(out, span)
		}
	}
	return out
}

func assertParent(t *testing.T, spans map[string]sdktrace.ReadOnlySpan, child string, want trace.SpanID) {
	t.Helper()
	span := spans[child]
	if span == nil {
		t.Fatalf("missing span %q", child)
	}
	if span.Parent().SpanID() != want {
		t.Fatalf("span %q parent=%s want=%s", child, span.Parent().SpanID(), want)
	}
}

func assertSafeSpanData(t *testing.T, span sdktrace.ReadOnlySpan, secretMarker string) {
	t.Helper()
	prohibitedKeys := []string{"authorization", "cookie", "signature", "token", "payload", "chat_id", "tenant_id", "incident_id", "url"}
	for _, item := range span.Attributes() {
		key := strings.ToLower(string(item.Key))
		value := attributeValueString(item)
		if strings.Contains(value, secretMarker) {
			t.Fatalf("span %q attribute %q leaked marker", span.Name(), item.Key)
		}
		for _, prohibited := range prohibitedKeys {
			if strings.Contains(key, prohibited) {
				t.Fatalf("span %q exported prohibited attribute key %q", span.Name(), item.Key)
			}
		}
	}
	for _, event := range span.Events() {
		for _, item := range event.Attributes {
			if strings.Contains(attributeValueString(item), secretMarker) {
				t.Fatalf("span %q event leaked marker", span.Name())
			}
		}
	}
}

func attributeValueString(item attribute.KeyValue) string {
	return fmt.Sprint(item.Value.AsInterface())
}
