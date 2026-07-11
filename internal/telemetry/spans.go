package telemetry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const (
	maxSpanAttributeBytes = 256
	spanDigestBytes       = 16
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

// TraceParent returns a W3C traceparent value for the current span. It contains
// only trace identifiers and sampling flags; baggage is intentionally excluded.
func TraceParent(ctx context.Context) string {
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	return carrier["traceparent"]
}

func FinishHTTPServerSpan(span trace.Span, method string, status int, route string, duration time.Duration) {
	if span == nil {
		return
	}
	if route == "" {
		route = "unmatched"
	}
	if routeMethod, routePath, found := strings.Cut(route, " "); found && routeMethod == method {
		route = routePath
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
		InjectHTTPTrace(ctx, r.Header)
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

func StartEventProducerSpan(ctx context.Context, destination, eventID, eventType string) (context.Context, trace.Span) {
	return otel.Tracer(instrumentationName+"/messaging").Start(
		ctx,
		"stormrelay.event.publish",
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			attribute.String("messaging.system", "nats"),
			attribute.String("messaging.destination.name", boundedSpanAttribute(destination)),
			attribute.String("stormrelay.event.id_hash", spanDigest(eventID)),
			attribute.String("stormrelay.event.type_hash", spanDigest(eventType)),
		),
	)
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
			attribute.String("messaging.system", "nats"),
			attribute.String("stormrelay.event.id_hash", spanDigest(eventID)),
			attribute.String("stormrelay.event.type_hash", spanDigest(eventType)),
			attribute.String("stormrelay.event.source_hash", spanDigest(source)),
		),
	)
}

func StartDatabaseSpan(ctx context.Context, operation string) (context.Context, trace.Span) {
	return otel.Tracer(instrumentationName+"/storage").Start(
		ctx,
		"stormrelay.db."+boundedSpanAttribute(operation),
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("db.system.name", "postgresql"),
			attribute.String("db.operation.name", boundedSpanAttribute(operation)),
		),
	)
}

func StartInternalSpan(ctx context.Context, name string) (context.Context, trace.Span) {
	return otel.Tracer(instrumentationName+"/application").Start(
		ctx,
		boundedSpanAttribute(name),
		trace.WithSpanKind(trace.SpanKindInternal),
	)
}

func StartNotificationSpan(ctx context.Context, operation, kind string) (context.Context, trace.Span) {
	return otel.Tracer(instrumentationName+"/notifications").Start(
		ctx,
		"stormrelay.notification."+boundedSpanAttribute(operation),
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("stormrelay.notification.kind", boundedSpanAttribute(kind))),
	)
}

func SetSpanBool(span trace.Span, key string, value bool) {
	if span != nil {
		span.SetAttributes(attribute.Bool(key, value))
	}
}

func SetSpanInt(span trace.Span, key string, value int) {
	if span != nil {
		span.SetAttributes(attribute.Int(key, value))
	}
}

func SetSpanString(span trace.Span, key, value string) {
	if span != nil {
		span.SetAttributes(attribute.String(key, boundedSpanAttribute(value)))
	}
}

func EndSpan(span trace.Span, err error) {
	RecordSpanError(span, err)
	if span != nil {
		span.End()
	}
}

func RecordSpanError(span trace.Span, err error) {
	if span == nil || err == nil {
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, "operation failed")
}

func boundedSpanAttribute(value string) string {
	value = strings.ToValidUTF8(value, "\uFFFD")
	if len(value) <= maxSpanAttributeBytes {
		return value
	}
	value = value[:maxSpanAttributeBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func spanDigest(value string) string {
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:spanDigestBytes])
}
