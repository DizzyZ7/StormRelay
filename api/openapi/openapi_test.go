package openapi

import (
	"os"
	"strings"
	"testing"
)

func TestContractContainsImplementedCriticalPaths(t *testing.T) {
	data, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, required := range []string{
		"openapi: 3.1.0",
		"/api/v1/webhooks/{sourceId}:",
		"/api/v1/incidents/{incidentId}/ack:",
		"/api/v1/audit/export:",
		"/api/v1/runbooks:",
		"/api/v1/runbooks/{runbookKey}/run:",
		"/api/v1/executions/{executionId}:",
		"/api/v1/executions/{executionId}/steps/{stepId}/retry:",
		"/api/v1/approvals/{approvalId}/{decision}:",
		"/api/v1/plugins/{pluginKey}/test:",
		"ErrorEnvelope:",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("contract missing %q", required)
		}
	}
}
