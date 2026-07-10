package telemetry

import (
	"context"
	"log/slog"
	"os"
)

type contextKey string

const (
	requestIDKey contextKey = "request_id"
	traceIDKey   contextKey = "trace_id"
)

func NewLogger(service, version string) *slog.Logger {
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	return slog.New(h).With("service", service, "version", version)
}
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}
func RequestID(ctx context.Context) string { v, _ := ctx.Value(requestIDKey).(string); return v }
func WithTraceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, traceIDKey, id)
}
func TraceID(ctx context.Context) string { v, _ := ctx.Value(traceIDKey).(string); return v }
func Log(ctx context.Context, l *slog.Logger, level slog.Level, msg string, args ...any) {
	attrs := []any{"request_id", RequestID(ctx), "trace_id", TraceID(ctx)}
	attrs = append(attrs, args...)
	l.Log(ctx, level, msg, attrs...)
}
