package config

import (
	"testing"
	"time"
)

const testMasterKey = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="

func TestLoadOpenTelemetryConfiguration(t *testing.T) {
	setRequiredConfig(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://fallback:4317")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "https://traces.example.com:4317")
	t.Setenv("STORMRELAY_OTEL_TRACE_SAMPLE_RATIO", "0.75")
	t.Setenv("STORMRELAY_OTEL_EXPORT_TIMEOUT", "3s")

	cfg, err := Load("stormrelay-test", "test")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OTLPTraceEndpoint != "https://traces.example.com:4317" {
		t.Fatalf("endpoint=%q", cfg.OTLPTraceEndpoint)
	}
	if cfg.OTelTraceSampleRatio != 0.75 || cfg.OTelTraceExportTimeout != 3*time.Second {
		t.Fatalf("ratio=%v timeout=%s", cfg.OTelTraceSampleRatio, cfg.OTelTraceExportTimeout)
	}
}

func TestLoadUsesGenericOTLPEndpointFallback(t *testing.T) {
	setRequiredConfig(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector:4317")

	cfg, err := Load("stormrelay-test", "test")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OTLPTraceEndpoint != "http://collector:4317" {
		t.Fatalf("endpoint=%q", cfg.OTLPTraceEndpoint)
	}
}

func TestLoadRejectsInvalidOpenTelemetryBounds(t *testing.T) {
	for name, environment := range map[string]map[string]string{
		"ratio below zero": {"STORMRELAY_OTEL_TRACE_SAMPLE_RATIO": "-0.1"},
		"ratio above one":  {"STORMRELAY_OTEL_TRACE_SAMPLE_RATIO": "1.1"},
		"zero timeout":     {"STORMRELAY_OTEL_EXPORT_TIMEOUT": "0s"},
		"long timeout":     {"STORMRELAY_OTEL_EXPORT_TIMEOUT": "61s"},
	} {
		t.Run(name, func(t *testing.T) {
			setRequiredConfig(t)
			for key, value := range environment {
				t.Setenv(key, value)
			}
			if _, err := Load("stormrelay-test", "test"); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func setRequiredConfig(t *testing.T) {
	t.Helper()
	t.Setenv("STORMRELAY_MASTER_KEY", testMasterKey)
	t.Setenv("STORMRELAY_BOOTSTRAP_API_KEY", "test-bootstrap-key")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("STORMRELAY_OTEL_TRACE_SAMPLE_RATIO", "")
	t.Setenv("STORMRELAY_OTEL_EXPORT_TIMEOUT", "")
}
