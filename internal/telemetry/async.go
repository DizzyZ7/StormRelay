package telemetry

import (
	"context"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// ContextWithTraceParent restores only a persisted W3C traceparent. Baggage is
// intentionally excluded from durable execution and notification records.
func ContextWithTraceParent(ctx context.Context, traceParent string) context.Context {
	traceParent = strings.TrimSpace(traceParent)
	if traceParent == "" {
		return ctx
	}
	return propagation.TraceContext{}.Extract(ctx, propagation.MapCarrier{"traceparent": traceParent})
}

func StartOperationSpan(ctx context.Context, name string, kind trace.SpanKind, attributes ...attribute.KeyValue) (context.Context, trace.Span) {
	return otel.Tracer(instrumentationName+"/operations").Start(
		ctx,
		boundedSpanAttribute(name),
		trace.WithSpanKind(kind),
		trace.WithAttributes(attributes...),
	)
}

func StringAttribute(key, value string) attribute.KeyValue {
	return attribute.String(key, boundedSpanAttribute(value))
}

func IntAttribute(key string, value int) attribute.KeyValue {
	return attribute.Int(key, value)
}

func BoolAttribute(key string, value bool) attribute.KeyValue {
	return attribute.Bool(key, value)
}

// EndSafeSpan marks a failure without recording provider-controlled exception
// text. External errors may contain URLs, identifiers, payload fragments, or
// credentials and therefore must not become span events.
func EndSafeSpan(span trace.Span, err error) {
	if span == nil {
		return
	}
	if err != nil {
		span.SetStatus(codes.Error, "operation failed")
	}
	span.End()
}

func SetSpanOutcome(span trace.Span, outcome string) {
	SetSpanString(span, "stormrelay.operation.outcome", outcome)
}
