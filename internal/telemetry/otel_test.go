package telemetry

import (
	"context"
	"encoding/hex"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

const testTraceParent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

func TestHTTPMiddlewareContinuesTraceAndRecordsRoute(t *testing.T) {
	recorder, cleanup := installSpanRecorder(t)
	defer cleanup()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /hello/{name}", func(w http.ResponseWriter, r *http.Request) {
		if TraceID(r.Context()) != "4bf92f3577b34da6a3ce929d0e0e4736" {
			t.Errorf("trace_id=%q", TraceID(r.Context()))
		}
		w.WriteHeader(http.StatusCreated)
	})
	request := httptest.NewRequest(http.MethodGet, "http://stormrelay.test/hello/operator", nil)
	request.Header.Set("traceparent", testTraceParent)
	response := httptest.NewRecorder()
	HTTPMiddleware(mux, nil).ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d", response.Code)
	}
	traceID, ok := ParseTraceParent(response.Header().Get("traceparent"))
	if !ok || traceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("response traceparent=%q", response.Header().Get("traceparent"))
	}
	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("ended spans=%d", len(spans))
	}
	span := spans[0]
	if span.Name() != "GET /hello/{name}" || span.SpanKind() != trace.SpanKindServer {
		t.Fatalf("span name=%q kind=%v", span.Name(), span.SpanKind())
	}
	if span.Parent().SpanID().String() != "00f067aa0ba902b7" {
		t.Fatalf("parent span_id=%s", span.Parent().SpanID())
	}
	if got := intAttribute(span.Attributes(), "http.response.status_code"); got != http.StatusCreated {
		t.Fatalf("status attribute=%d", got)
	}
	if got := stringAttribute(span.Attributes(), "http.route"); got != "/hello/{name}" {
		t.Fatalf("route attribute=%q", got)
	}
}

func TestEventConsumerSpanContinuesIngressTrace(t *testing.T) {
	recorder, cleanup := installSpanRecorder(t)
	defer cleanup()

	_, span := StartEventConsumerSpan(context.Background(), testTraceParent, "event-1", "security.alert", "siem")
	span.End()
	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("ended spans=%d", len(spans))
	}
	got := spans[0]
	if got.SpanKind() != trace.SpanKindConsumer || got.Parent().SpanID().String() != "00f067aa0ba902b7" {
		t.Fatalf("kind=%v parent=%s", got.SpanKind(), got.Parent().SpanID())
	}
	if got.SpanContext().TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("trace_id=%s", got.SpanContext().TraceID())
	}
}

func TestProducerTraceParentConnectsConsumer(t *testing.T) {
	recorder, cleanup := installSpanRecorder(t)
	defer cleanup()

	rootCtx, root := otel.Tracer("test").Start(context.Background(), "ingress")
	producerCtx, producer := StartEventProducerSpan(rootCtx, "stormrelay.events", "event-1", "security.alert")
	traceParent := TraceParent(producerCtx)
	if traceParent == "" {
		t.Fatal("producer traceparent is empty")
	}
	producerSpanID := producer.SpanContext().SpanID()
	producerTraceID := producer.SpanContext().TraceID()
	producer.End()

	_, consumer := StartEventConsumerSpan(context.Background(), traceParent, "event-1", "security.alert", "siem")
	if consumer.SpanContext().TraceID() != producerTraceID {
		t.Fatalf("consumer trace=%s producer trace=%s", consumer.SpanContext().TraceID(), producerTraceID)
	}
	consumer.End()
	root.End()

	var consumerFound bool
	for _, span := range recorder.Ended() {
		if span.Name() == "stormrelay.event.process" {
			consumerFound = true
			if span.Parent().SpanID() != producerSpanID {
				t.Fatalf("consumer parent=%s producer=%s", span.Parent().SpanID(), producerSpanID)
			}
		}
	}
	if !consumerFound {
		t.Fatal("consumer span was not recorded")
	}
}

func TestEventSpansHashUserControlledAttributes(t *testing.T) {
	recorder, cleanup := installSpanRecorder(t)
	defer cleanup()

	secret := "https://token@example.test/hook?signature=top-secret"
	invalidUTF8 := secret + string([]byte{0xff, 0xfe})
	_, producer := StartEventProducerSpan(context.Background(), "stormrelay.events", invalidUTF8, invalidUTF8)
	producer.End()
	_, consumer := StartEventConsumerSpan(context.Background(), "", invalidUTF8, invalidUTF8, invalidUTF8)
	consumer.End()

	spans := recorder.Ended()
	if len(spans) != 2 {
		t.Fatalf("ended spans=%d", len(spans))
	}
	for _, span := range spans {
		for _, item := range span.Attributes() {
			value := item.Value.Emit()
			if strings.Contains(value, "top-secret") || strings.Contains(value, "example.test") {
				t.Fatalf("span %q leaked user-controlled data in %s=%q", span.Name(), item.Key, value)
			}
		}
		for _, key := range []string{"stormrelay.event.id_hash", "stormrelay.event.type_hash"} {
			value := stringAttribute(span.Attributes(), key)
			if len(value) != spanDigestBytes*2 {
				t.Fatalf("span %q %s length=%d", span.Name(), key, len(value))
			}
			if _, err := hex.DecodeString(value); err != nil {
				t.Fatalf("span %q %s is not hexadecimal: %v", span.Name(), key, err)
			}
		}
	}
	sourceHash := stringAttribute(spans[1].Attributes(), "stormrelay.event.source_hash")
	if len(sourceHash) != spanDigestBytes*2 {
		t.Fatalf("source hash length=%d", len(sourceHash))
	}
}

func TestBoundedSpanAttributePreservesValidUTF8(t *testing.T) {
	longValue := strings.Repeat("я", maxSpanAttributeBytes)
	invalidUTF8 := longValue + string([]byte{0xff, 0xfe})
	value := boundedSpanAttribute(invalidUTF8)
	if len(value) > maxSpanAttributeBytes {
		t.Fatalf("length=%d", len(value))
	}
	if !utf8.ValidString(value) {
		t.Fatal("value is not valid UTF-8")
	}
}

func TestSetupTracingValidationAndDisabledProvider(t *testing.T) {
	for name, cfg := range map[string]TracingConfig{
		"missing service": {ExportTimeout: time.Second},
		"invalid ratio":   {ServiceName: "test", SampleRatio: 1.1, ExportTimeout: time.Second},
		"NaN ratio":       {ServiceName: "test", SampleRatio: math.NaN(), ExportTimeout: time.Second},
		"infinite ratio":  {ServiceName: "test", SampleRatio: math.Inf(1), ExportTimeout: time.Second},
		"invalid timeout": {ServiceName: "test", SampleRatio: 0.1},
		"invalid scheme":  {ServiceName: "test", Endpoint: "ftp://collector:4317", SampleRatio: 0.1, ExportTimeout: time.Second},
		"endpoint path":   {ServiceName: "test", Endpoint: "http://collector:4317/v1/traces", SampleRatio: 0.1, ExportTimeout: time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := SetupTracing(context.Background(), cfg, nil); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}

	oldProvider := otel.GetTracerProvider()
	oldPropagator := otel.GetTextMapPropagator()
	defer func() {
		otel.SetTracerProvider(oldProvider)
		otel.SetTextMapPropagator(oldPropagator)
	}()
	tracing, err := SetupTracing(context.Background(), TracingConfig{
		ServiceName: "stormrelay-test", Version: "test", SampleRatio: 0.1, ExportTimeout: time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, span := otel.Tracer("test").Start(context.Background(), "disabled-exporter")
	if !span.SpanContext().IsValid() || TraceID(ctx) == "" {
		t.Fatal("disabled exporter must still create valid local trace context")
	}
	span.End()
	if err := tracing.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := tracing.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func installSpanRecorder(t *testing.T) (*tracetest.SpanRecorder, func()) {
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

func stringAttribute(attributes []attribute.KeyValue, key string) string {
	for _, item := range attributes {
		if string(item.Key) == key {
			return item.Value.AsString()
		}
	}
	return ""
}

func intAttribute(attributes []attribute.KeyValue, key string) int {
	for _, item := range attributes {
		if string(item.Key) == key {
			return int(item.Value.AsInt64())
		}
	}
	return 0
}
