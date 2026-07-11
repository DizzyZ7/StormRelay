package config

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func setRequiredConfig(t *testing.T) {
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
	setRequiredConfig(t)
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
			setRequiredConfig(t)
			t.Setenv(test.key, test.value)
			_, err := Load("test", "dev")
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error=%v, want substring %q", err, test.wantError)
			}
		})
	}
}
