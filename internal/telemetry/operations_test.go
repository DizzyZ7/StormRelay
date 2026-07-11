package telemetry

import (
	"context"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

func TestPersistedTraceParentContinuesExactParent(t *testing.T) {
	recorder, cleanup := installSpanRecorder(t)
	defer cleanup()

	rootCtx, root := otel.Tracer("test").Start(context.Background(), "root")
	traceParent := TraceParentFromContext(rootCtx)
	if traceParent == "" || strings.Contains(traceParent, "secret") {
		t.Fatalf("unexpected traceparent %q", traceParent)
	}
	rootSpanID := root.SpanContext().SpanID()
	rootTraceID := root.SpanContext().TraceID()
	root.End()

	restored := ContextWithTraceParent(context.Background(), traceParent)
	_, child := StartOperationSpan(restored, "child", trace.SpanKindInternal)
	child.End()

	spans := recorder.Ended()
	if len(spans) != 2 {
		t.Fatalf("ended spans=%d", len(spans))
	}
	var gotChild bool
	for _, span := range spans {
		if span.Name() != "child" {
			continue
		}
		gotChild = true
		if span.Parent().SpanID() != rootSpanID {
			t.Fatalf("parent=%s want=%s", span.Parent().SpanID(), rootSpanID)
		}
		if span.SpanContext().TraceID() != rootTraceID {
			t.Fatalf("trace=%s want=%s", span.SpanContext().TraceID(), rootTraceID)
		}
	}
	if !gotChild {
		t.Fatal("child span was not recorded")
	}
}

func TestOperationAttributesAreBoundedAndSafeErrorHasNoExceptionEvent(t *testing.T) {
	recorder, cleanup := installSpanRecorder(t)
	defer cleanup()

	long := strings.Repeat("sensitive-value-", 100)
	_, span := StartOperationSpan(context.Background(), "safe-operation", trace.SpanKindClient, StringAttribute("test.value", long))
	MarkSpanError(span)
	SetSpanOutcome(span, "failed")
	span.End()

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("ended spans=%d", len(spans))
	}
	got := spans[0]
	if got.Status().Code != codes.Error {
		t.Fatalf("status=%v", got.Status().Code)
	}
	if len(got.Events()) != 0 {
		t.Fatalf("safe error created %d exception events", len(got.Events()))
	}
	value := stringAttribute(got.Attributes(), "test.value")
	if len(value) > maxSpanAttributeBytes {
		t.Fatalf("attribute length=%d", len(value))
	}
}
