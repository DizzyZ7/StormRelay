package telemetry

import (
	"context"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func StartHTTPServerSpan(ctx context.Context, headers http.Header, method string) (context.Context, trace.Span) {
	ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.HeaderCarrier(headers))
	return otel.Tracer(instrumentationName+"/http").Start(
		ctx,
		"HTTP "+method,
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attribute.String("http.request.method", method)),
	)
}

func InjectHTTPTrace(ctx context.Context, headers http.Header) {
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(headers))
}

func FinishHTTPServerSpan(span trace.Span, status int, route string, duration time.Duration) {
	if span == nil {
		return
	}
	span.SetName("HTTP " + route)
	span.SetAttributes(
		attribute.String("http.route", route),
		attribute.Int("http.response.status_code", status),
		attribute.Int64("http.server.duration_ms", duration.Milliseconds()),
	)
	if status >= http.StatusInternalServerError {
		span.SetStatus(codes.Error, http.StatusText(status))
	}
	span.End()
}

func StartEventConsumerSpan(ctx context.Context, traceParent, eventID, eventType, source string) (context.Context, trace.Span) {
	if traceParent != "" {
		ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier{"traceparent": traceParent})
	}
	return otel.Tracer(instrumentationName+"/worker").Start(
		ctx,
		"stormrelay.event.process",
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("messaging.message.id", eventID),
			attribute.String("event.type", eventType),
			attribute.String("event.source", source),
		),
	)
}

func RecordSpanError(span trace.Span, err error) {
	if span == nil || err == nil {
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, "operation failed")
}
