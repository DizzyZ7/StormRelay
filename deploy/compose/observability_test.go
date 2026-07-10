package compose

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var metricPattern = regexp.MustCompile(`stormrelay_[a-z0-9_]+`)

var exportedMetrics = map[string]struct{}{
	"stormrelay_ingress_events_total":                   {},
	"stormrelay_rejected_events_total":                  {},
	"stormrelay_duplicate_events_total":                 {},
	"stormrelay_runbook_failures_total":                 {},
	"stormrelay_runbook_duration_seconds_count":         {},
	"stormrelay_runbook_duration_seconds_sum":           {},
	"stormrelay_plugin_failures_total":                  {},
	"stormrelay_notification_delivery_failures_total":   {},
	"stormrelay_open_incidents":                         {},
	"stormrelay_database_pool_acquired":                 {},
	"stormrelay_database_pool_max":                      {},
	"stormrelay_jetstream_consumer_lag":                 {},
	"stormrelay_event_processing_latency_seconds_count": {},
	"stormrelay_event_processing_latency_seconds_sum":   {},
}

type alertFile struct {
	Groups []struct {
		Name  string `yaml:"name"`
		Rules []struct {
			Alert       string            `yaml:"alert"`
			Expr        string            `yaml:"expr"`
			For         string            `yaml:"for"`
			Labels      map[string]string `yaml:"labels"`
			Annotations map[string]string `yaml:"annotations"`
		} `yaml:"rules"`
	} `yaml:"groups"`
}

type dashboard struct {
	UID    string `json:"uid"`
	Title  string `json:"title"`
	Panels []struct {
		ID         int `json:"id"`
		Datasource struct {
			UID string `json:"uid"`
		} `json:"datasource"`
		Targets []struct {
			Expr string `json:"expr"`
		} `json:"targets"`
	} `json:"panels"`
}

func TestAlertRulesUseKnownMetricsAndOperationalMetadata(t *testing.T) {
	data := mustRead(t, "alerts.yml")
	var rules alertFile
	if err := yaml.Unmarshal(data, &rules); err != nil {
		t.Fatal(err)
	}
	if len(rules.Groups) < 3 {
		t.Fatalf("alert groups=%d", len(rules.Groups))
	}
	seen := map[string]struct{}{}
	for _, group := range rules.Groups {
		if strings.TrimSpace(group.Name) == "" || len(group.Rules) == 0 {
			t.Fatalf("invalid alert group %+v", group)
		}
		for _, rule := range group.Rules {
			if rule.Alert == "" || rule.Expr == "" || rule.For == "" {
				t.Fatalf("incomplete alert rule %+v", rule)
			}
			if _, duplicate := seen[rule.Alert]; duplicate {
				t.Fatalf("duplicate alert %q", rule.Alert)
			}
			seen[rule.Alert] = struct{}{}
			severity := rule.Labels["severity"]
			if severity != "warning" && severity != "critical" {
				t.Fatalf("alert %s severity=%q", rule.Alert, severity)
			}
			if !strings.HasPrefix(rule.Annotations["runbook_url"], "https://") {
				t.Fatalf("alert %s has no HTTPS runbook URL", rule.Alert)
			}
			assertKnownMetrics(t, rule.Alert, rule.Expr)
		}
	}
	for _, required := range []string{
		"StormRelayTargetDown",
		"StormRelayEventPipelineStalled",
		"StormRelayJetStreamBacklogHigh",
		"StormRelayDatabasePoolSaturated",
	} {
		if _, ok := seen[required]; !ok {
			t.Errorf("required alert %q missing", required)
		}
	}
}

func TestDashboardUsesProvisionedDatasourceAndKnownMetrics(t *testing.T) {
	data := mustRead(t, "grafana/dashboards/stormrelay-operations.json")
	var board dashboard
	if err := json.Unmarshal(data, &board); err != nil {
		t.Fatal(err)
	}
	if board.UID != "stormrelay-operations" || board.Title != "StormRelay Operations" {
		t.Fatalf("dashboard identity uid=%q title=%q", board.UID, board.Title)
	}
	if len(board.Panels) < 8 {
		t.Fatalf("dashboard panels=%d", len(board.Panels))
	}
	seenIDs := map[int]struct{}{}
	for _, panel := range board.Panels {
		if panel.ID < 1 {
			t.Fatalf("invalid panel id=%d", panel.ID)
		}
		if _, duplicate := seenIDs[panel.ID]; duplicate {
			t.Fatalf("duplicate panel id=%d", panel.ID)
		}
		seenIDs[panel.ID] = struct{}{}
		if panel.Datasource.UID != "stormrelay-prometheus" {
			t.Fatalf("panel %d datasource=%q", panel.ID, panel.Datasource.UID)
		}
		if len(panel.Targets) == 0 {
			t.Fatalf("panel %d has no queries", panel.ID)
		}
		for _, target := range panel.Targets {
			if strings.TrimSpace(target.Expr) == "" {
				t.Fatalf("panel %d has empty query", panel.ID)
			}
			assertKnownMetrics(t, "dashboard panel", target.Expr)
		}
	}
}

func TestPrometheusAndGrafanaProvisioningAreWired(t *testing.T) {
	prometheus := string(mustRead(t, "prometheus.yml"))
	if !strings.Contains(prometheus, "/etc/prometheus/alerts.yml") {
		t.Fatal("Prometheus does not load alerts.yml")
	}
	datasource := string(mustRead(t, "grafana/provisioning/datasources/prometheus.yml"))
	if !strings.Contains(datasource, "uid: stormrelay-prometheus") || !strings.Contains(datasource, "url: http://prometheus:9090") {
		t.Fatal("Grafana datasource provisioning is incomplete")
	}
	provider := string(mustRead(t, "grafana/provisioning/dashboards/stormrelay.yml"))
	if !strings.Contains(provider, "path: /var/lib/grafana/dashboards") {
		t.Fatal("Grafana dashboard provider path is missing")
	}
	compose := string(mustRead(t, "docker-compose.yml"))
	for _, mount := range []string{
		"./alerts.yml:/etc/prometheus/alerts.yml:ro",
		"./grafana/provisioning:/etc/grafana/provisioning:ro",
		"./grafana/dashboards:/var/lib/grafana/dashboards:ro",
	} {
		if !strings.Contains(compose, mount) {
			t.Errorf("Compose mount %q missing", mount)
		}
	}
}

func assertKnownMetrics(t *testing.T, owner, expression string) {
	t.Helper()
	for _, metric := range metricPattern.FindAllString(expression, -1) {
		if _, ok := exportedMetrics[metric]; !ok {
			t.Errorf("%s references unknown metric %q", owner, metric)
		}
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
