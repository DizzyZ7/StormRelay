package policies

import (
	"testing"
	"time"

	"github.com/DizzyZ7/StormRelay/internal/events"
)

var benchmarkDocumentSink Document
var benchmarkDecisionSink Decision

var benchmarkPolicyYAML = []byte(`apiVersion: stormrelay.io/v1
kind: Policy
metadata:
  id: benchmark-critical-production
  version: 1
spec:
  match:
    all:
      - field: severity
        in: [error, critical]
      - field: environment
        equals: production
      - any:
          - field: labels.service
            equals: checkout
          - field: labels.team
            equals: payments
      - not:
          field: labels.maintenance
          equals: "true"
  actions:
    requireApproval: true
    assignTeam: sre
    escalationPolicy: production-critical
    notificationChannels: [local-mock, primary-oncall]
    runbook: checkout-recovery
`)

func BenchmarkParsePolicy(b *testing.B) {
	b.ReportAllocs()
	b.SetBytes(int64(len(benchmarkPolicyYAML)))
	for i := 0; i < b.N; i++ {
		document, err := Parse(benchmarkPolicyYAML)
		if err != nil {
			b.Fatal(err)
		}
		benchmarkDocumentSink = document
	}
}

func BenchmarkEvaluatePolicy(b *testing.B) {
	document, err := Parse(benchmarkPolicyYAML)
	if err != nil {
		b.Fatal(err)
	}
	tests := []struct {
		name  string
		event events.Event
	}{
		{
			name: "matched",
			event: events.Event{
				ID: "benchmark-event", Source: "urn:benchmark", Type: "com.example.alert", Subject: "checkout latency",
				Time: time.Unix(1_700_000_000, 0).UTC(), Severity: events.SeverityCritical,
				Labels: map[string]string{"service": "checkout", "team": "payments", "environment": "production", "maintenance": "false"},
			},
		},
		{
			name: "unmatched",
			event: events.Event{
				ID: "benchmark-event", Source: "urn:benchmark", Type: "com.example.alert", Subject: "checkout latency",
				Time: time.Unix(1_700_000_000, 0).UTC(), Severity: events.SeverityWarning,
				Labels: map[string]string{"service": "catalog", "team": "storefront", "environment": "staging", "maintenance": "true"},
			},
		},
	}
	for _, test := range tests {
		b.Run(test.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				benchmarkDecisionSink = Evaluate(document, test.event)
			}
		})
	}
}
