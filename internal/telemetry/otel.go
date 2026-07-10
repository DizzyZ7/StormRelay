package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const instrumentationName = "github.com/DizzyZ7/StormRelay"

type TracingConfig struct {
	ServiceName   string
	Version       string
	Endpoint      string
	SampleRatio   float64
	ExportTimeout time.Duration
}

type Tracing struct {
	provider *sdktrace.TracerProvider
	once     sync.Once
	err      error
}

func SetupTracing(ctx context.Context, cfg TracingConfig, logger *slog.Logger) (*Tracing, error) {
	if strings.TrimSpace(cfg.ServiceName) == "" {
		return nil, fmt.Errorf("telemetry service name is required")
	}
	if cfg.SampleRatio < 0 || cfg.SampleRatio > 1 {
		return nil, fmt.Errorf("trace sample ratio must be between 0 and 1")
	}
	if cfg.ExportTimeout <= 0 || cfg.ExportTimeout > time.Minute {
		return nil, fmt.Errorf("trace export timeout must be greater than zero and at most one minute")
	}

	endpoint := strings.TrimSpace(cfg.Endpoint)
	providerOptions := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(resource.NewSchemaless(
			attribute.String("service.name", cfg.ServiceName),
			attribute.String("service.version", cfg.Version),
		)),
	}

	if endpoint == "" {
		providerOptions = append(providerOptions, sdktrace.WithSampler(sdktrace.NeverSample()))
	} else {
		if err := validateOTLPEndpoint(endpoint); err != nil {
			return nil, err
		}
		exporter, err := otlptracegrpc.New(ctx,
			otlptracegrpc.WithEndpointURL(endpoint),
			otlptracegrpc.WithTimeout(cfg.ExportTimeout),
			otlptracegrpc.WithMaxRequestSize(4<<20),
		)
		if err != nil {
			return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
		}
		providerOptions = append(providerOptions,
			sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRatio))),
			sdktrace.WithBatcher(exporter,
				sdktrace.WithBatchTimeout(2*time.Second),
				sdktrace.WithExportTimeout(cfg.ExportTimeout),
				sdktrace.WithMaxQueueSize(2048),
				sdktrace.WithMaxExportBatchSize(512),
			),
		)
	}

	provider := sdktrace.NewTracerProvider(providerOptions...)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	if logger != nil {
		otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
			logger.Error("opentelemetry error", "error", err)
		}))
	}
	return &Tracing{provider: provider}, nil
}

func (t *Tracing) Shutdown(ctx context.Context) error {
	if t == nil || t.provider == nil {
		return nil
	}
	t.once.Do(func() {
		t.err = t.provider.Shutdown(ctx)
	})
	return t.err
}

func validateOTLPEndpoint(endpoint string) error {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Hostname() == "" {
		return fmt.Errorf("OTLP trace endpoint must be an absolute URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("OTLP trace endpoint scheme must be http or https")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return fmt.Errorf("OTLP trace endpoint must not contain userinfo, path, query, or fragment")
	}
	return nil
}
