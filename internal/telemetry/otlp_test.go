package telemetry

import (
	"context"
	"net"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
)

type traceReceiver struct {
	collectortrace.UnimplementedTraceServiceServer
	requests chan *collectortrace.ExportTraceServiceRequest
}

func (r *traceReceiver) Export(_ context.Context, request *collectortrace.ExportTraceServiceRequest) (*collectortrace.ExportTraceServiceResponse, error) {
	select {
	case r.requests <- request:
	default:
	}
	return &collectortrace.ExportTraceServiceResponse{}, nil
}

func TestOTLPExporterFlushesSpansOnShutdown(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	receiver := &traceReceiver{requests: make(chan *collectortrace.ExportTraceServiceRequest, 1)}
	server := grpc.NewServer()
	collectortrace.RegisterTraceServiceServer(server, receiver)
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	defer func() {
		server.Stop()
		_ = listener.Close()
		<-serveDone
	}()

	oldProvider := otel.GetTracerProvider()
	oldPropagator := otel.GetTextMapPropagator()
	defer func() {
		otel.SetTracerProvider(oldProvider)
		otel.SetTextMapPropagator(oldPropagator)
	}()
	tracing, err := SetupTracing(context.Background(), TracingConfig{
		ServiceName:   "stormrelay-otlp-test",
		Version:       "test",
		Endpoint:      "http://" + listener.Addr().String(),
		SampleRatio:   1,
		ExportTimeout: 2 * time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, span := otel.Tracer("stormrelay-otlp-test").Start(context.Background(), "exported-span")
	span.End()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := tracing.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}

	select {
	case request := <-receiver.requests:
		if !containsExportedSpan(request, "stormrelay-otlp-test", "exported-span") {
			t.Fatalf("OTLP request did not contain expected resource and span: %v", request)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for OTLP export")
	}
}

func containsExportedSpan(request *collectortrace.ExportTraceServiceRequest, serviceName, spanName string) bool {
	for _, resourceSpans := range request.ResourceSpans {
		resourceMatches := false
		if resourceSpans.Resource != nil {
			for _, item := range resourceSpans.Resource.Attributes {
				if item.Key == "service.name" && item.Value.GetStringValue() == serviceName {
					resourceMatches = true
					break
				}
			}
		}
		if !resourceMatches {
			continue
		}
		for _, scopeSpans := range resourceSpans.ScopeSpans {
			for _, span := range scopeSpans.Spans {
				if span.Name == spanName {
					return true
				}
			}
		}
	}
	return false
}
