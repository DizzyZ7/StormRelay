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

// StartOperationSpan creates a bounded internal span. Callers should pass only
// low-cardinality operational attributes. User-controlled strings must be built
// with StringAttribute so invalid UTF-8 and oversized values are normalized.
func StartOperationSpan(ctx context.Context, name string, kind trace.SpanKind, attributes ...attribute.KeyValue) (context.Context, trace.Span) {
	return otel.Tracer(instrumentationName+"/operations").Start(
		ctx,
		name,
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

func Int64Attribute(key string, value int64) attribute.KeyValue {
	return attribute.Int64(key, value)
}

func BoolAttribute(key string, value bool) attribute.KeyValue {
	return attribute.Bool(key, value)
}

// MarkSpanError marks an operation failed without exporting an exception message.
// External adapter errors can contain provider-controlled text, URLs, identifiers,
// or secrets and therefore must not be recorded as exception events.
func MarkSpanError(span trace.Span) {
	if span == nil {
		return
	}
	span.SetStatus(codes.Error, "operation failed")
}

func SetSpanOutcome(span trace.Span, outcome string) {
	if span == nil {
		return
	}
	span.SetAttributes(StringAttribute("stormrelay.operation.outcome", outcome))
}

// TraceParentFromContext serializes only the W3C traceparent header. Baggage is
// deliberately excluded from persisted event envelopes because it may contain
// user-controlled or sensitive values.
func TraceParentFromContext(ctx context.Context) string {
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	return strings.TrimSpace(carrier.Get("traceparent"))
}
