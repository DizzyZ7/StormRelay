package policies

import (
	"github.com/DizzyZ7/StormRelay/internal/events"
	"testing"
)

func TestPolicyEvaluationExplanation(t *testing.T) {
	doc, err := Parse([]byte(`apiVersion: stormrelay.io/v1
kind: Policy
metadata:
  id: critical-production
  version: 1
spec:
  match:
    all:
      - field: severity
        equals: critical
      - field: environment
        in: [production]
  actions:
    requireApproval: true
    notificationChannels: [oncall]
`))
	if err != nil {
		t.Fatal(err)
	}
	d := Evaluate(doc, events.Event{Severity: events.SeverityCritical, Labels: map[string]string{"environment": "production"}})
	if !d.Matched || !d.Actions.RequireApproval || len(d.Explanation) == 0 {
		t.Fatalf("unexpected decision: %#v", d)
	}
}

func FuzzPolicyParse(f *testing.F) {
	f.Add([]byte("apiVersion: stormrelay.io/v1\nkind: Policy"))
	f.Fuzz(func(t *testing.T, data []byte) { _, _ = Parse(data) })
}
