package config

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func setRequiredRedeliveryConfig(t *testing.T) {
	t.Helper()
	t.Setenv("STORMRELAY_MASTER_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("STORMRELAY_BOOTSTRAP_API_KEY", "test-bootstrap-key")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("STORMRELAY_EVENT_MAX_DELIVERIES", "")
	t.Setenv("STORMRELAY_EVENT_RETRY_BASE_DELAY", "")
	t.Setenv("STORMRELAY_EVENT_RETRY_MAX_DELAY", "")
}

func TestLoadEventRedeliveryDefaults(t *testing.T) {
	setRequiredRedeliveryConfig(t)
	cfg, err := Load("test", "dev")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EventMaxDeliveries != 5 || cfg.EventRetryBaseDelay != time.Second || cfg.EventRetryMaxDelay != 30*time.Second {
		t.Fatalf("unexpected event retry defaults: deliveries=%d base=%s max=%s", cfg.EventMaxDeliveries, cfg.EventRetryBaseDelay, cfg.EventRetryMaxDelay)
	}
}

func TestLoadRejectsInvalidEventRedeliveryPolicy(t *testing.T) {
	tests := []struct {
		name      string
		key       string
		value     string
		wantError string
	}{
		{name: "too few deliveries", key: "STORMRELAY_EVENT_MAX_DELIVERIES", value: "1", wantError: "between 2 and 100"},
		{name: "invalid base duration", key: "STORMRELAY_EVENT_RETRY_BASE_DELAY", value: "soon", wantError: "Go duration"},
		{name: "max below base", key: "STORMRELAY_EVENT_RETRY_MAX_DELAY", value: "500ms", wantError: "at least the base delay"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setRequiredRedeliveryConfig(t)
			t.Setenv(test.key, test.value)
			_, err := Load("test", "dev")
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error=%v, want substring %q", err, test.wantError)
			}
		})
	}
}

// Misconfigured security and reliability settings must never silently fall back.
func TestLoadRejectsMalformedEnvironment(t *testing.T) {
	tests := []struct{ name, key, value, want string }{
		{"payload size", "STORMRELAY_MAX_PAYLOAD_BYTES", "unlimited", "must be an integer"},
		{"payload negative", "STORMRELAY_MAX_PAYLOAD_BYTES", "-1", "between 1"},
		{"payload excessive", "STORMRELAY_MAX_PAYLOAD_BYTES", "67108865", "between 1"},
		{"replay duration", "STORMRELAY_REPLAY_WINDOW", "forever", "Go duration"},
		{"replay disabled", "STORMRELAY_REPLAY_WINDOW", "0s", "must be positive"},
		{"dedupe duration", "STORMRELAY_DEDUPE_WINDOW", "invalid", "Go duration"},
		{"correlation disabled", "STORMRELAY_CORRELATION_WINDOW", "-1s", "must be positive"},
		{"worker count", "STORMRELAY_WORKER_CONCURRENCY", "many", "must be an integer"},
		{"delivery count", "STORMRELAY_EVENT_MAX_DELIVERIES", "many", "must be an integer"},
		{"runbook count", "STORMRELAY_RUNBOOK_CONCURRENCY", "many", "must be an integer"},
		{"auto migration", "STORMRELAY_AUTO_MIGRATE", "sometimes", "must be a boolean"},
		{"unauthenticated sources", "STORMRELAY_ALLOW_UNAUTHENTICATED_SOURCES", "maybe", "must be a boolean"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setRequiredRedeliveryConfig(t)
			t.Setenv(tc.key, tc.value)
			_, err := Load("test", "dev")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load() error = %v; want %q", err, tc.want)
			}
		})
	}
}

func TestLoadAcceptsExplicitEnvironmentValues(t *testing.T) {
	setRequiredRedeliveryConfig(t)
	t.Setenv("STORMRELAY_MAX_PAYLOAD_BYTES", "2097152")
	t.Setenv("STORMRELAY_REPLAY_WINDOW", "8m")
	t.Setenv("STORMRELAY_DEDUPE_WINDOW", "20m")
	t.Setenv("STORMRELAY_CORRELATION_WINDOW", "40m")
	t.Setenv("STORMRELAY_AUTO_MIGRATE", "false")
	t.Setenv("STORMRELAY_ALLOW_UNAUTHENTICATED_SOURCES", "true")
	cfg, err := Load("test", "dev")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxPayloadBytes != 2097152 || cfg.ReplayWindow != 8*time.Minute || cfg.DedupeWindow != 20*time.Minute || cfg.CorrelationWindow != 40*time.Minute || cfg.AutoMigrate || !cfg.AllowUnauthenticatedSources {
		t.Fatalf("unexpected parsed config: %+v", cfg)
	}
}
