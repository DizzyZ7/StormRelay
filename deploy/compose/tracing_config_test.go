package compose

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestTracePipelineConfigurationIsBoundedAndWired(t *testing.T) {
	collectorData := mustRead(t, "otel-collector.yml")
	var collector map[string]any
	if err := yaml.Unmarshal(collectorData, &collector); err != nil {
		t.Fatal(err)
	}
	collectorText := string(collectorData)
	for _, required := range []string{
		"endpoint: 0.0.0.0:4317",
		"memory_limiter:",
		"limit_mib: 128",
		"batch:",
		"send_batch_max_size: 1024",
		"otlp/tempo:",
		"endpoint: tempo:4317",
		"queue_size: 2048",
		"max_elapsed_time: 60s",
		"health_check:",
	} {
		if !strings.Contains(collectorText, required) {
			t.Errorf("collector config is missing %q", required)
		}
	}
	for _, prohibited := range []string{"debug:", "logging:", "file:"} {
		if strings.Contains(collectorText, prohibited) {
			t.Errorf("collector config contains prohibited exporter %q", prohibited)
		}
	}

	tempoText := string(mustRead(t, "tempo.yml"))
	for _, required := range []string{
		"http_listen_port: 3200",
		"endpoint: 0.0.0.0:4317",
		"block_retention: 24h",
		"backend: local",
		"path: /var/tempo/wal",
		"path: /var/tempo/blocks",
	} {
		if !strings.Contains(tempoText, required) {
			t.Errorf("Tempo config is missing %q", required)
		}
	}

	datasource := string(mustRead(t, "grafana/provisioning/datasources/tempo.yml"))
	for _, required := range []string{"uid: stormrelay-tempo", "type: tempo", "url: http://tempo:3200", "datasourceUid: stormrelay-prometheus"} {
		if !strings.Contains(datasource, required) {
			t.Errorf("Tempo datasource is missing %q", required)
		}
	}

	compose := string(mustRead(t, "docker-compose.yml"))
	for _, required := range []string{
		"otel/opentelemetry-collector-contrib:0.156.0",
		"grafana/tempo:3.0.2",
		"./otel-collector.yml:/etc/otelcol-contrib/config.yaml:ro",
		"./tempo.yml:/etc/tempo/tempo.yml:ro",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT: http://otel-collector:4317",
		"STORMRELAY_OTEL_TRACE_SAMPLE_RATIO: \"1.0\"",
		"tempo-data:/var/tempo",
	} {
		if !strings.Contains(compose, required) {
			t.Errorf("Compose tracing wiring is missing %q", required)
		}
	}
}
