package telemetry

import (
	"context"
	"log/slog"
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

func FinishHTTPServerSpan(span trace.Span, method string, status int, route string, duration time.Duration) {
	if span == nil {
		return
	}
	if route == "" {
		route = "unmatched"
	}
	span.SetName(method + " " + route)
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

func HTTPMiddleware(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		ctx, span := StartHTTPServerSpan(r.Context(), r.Header, r.Method)
		ctx = WithTraceID(ctx, TraceID(ctx))
		r = r.WithContext(ctx)
		InjectHTTPTrace(ctx, w.Header())
		recorder := &statusResponseWriter{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			route := r.Pattern
			FinishHTTPServerSpan(span, r.Method, recorder.status, route, time.Since(started))
			if logger != nil {
				Log(ctx, logger, slog.LevelInfo, "http request", "method", r.Method, "route", route, "status", recorder.status, "duration_ms", time.Since(started).Milliseconds())
			}
		}()
		next.ServeHTTP(recorder, r)
	})
}

type statusResponseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusResponseWriter) Write(body []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (w *statusResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

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
