package openapi

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestContractContainsImplementedCriticalPaths(t *testing.T) {
	data, err := os.ReadFile("openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		OpenAPI    string                    `yaml:"openapi"`
		Paths      map[string]map[string]any `yaml:"paths"`
		Components struct {
			Schemas map[string]any `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse OpenAPI contract: %v", err)
	}
	if document.OpenAPI != "3.1.0" {
		t.Fatalf("openapi=%q", document.OpenAPI)
	}
	for _, path := range []string{
		"/api/v1/webhooks/{sourceId}",
		"/api/v1/incidents/{incidentId}/ack",
		"/api/v1/audit/export",
		"/api/v1/runbooks",
		"/api/v1/runbooks/{runbookKey}/run",
		"/api/v1/executions/{executionId}",
		"/api/v1/executions/{executionId}/steps/{stepId}/retry",
		"/api/v1/approvals/{approvalId}/{decision}",
		"/api/v1/plugins/{pluginKey}/test",
		"/api/v1/service-accounts",
		"/api/v1/service-account-keys/{keyId}/revoke",
		"/api/v1/oidc/providers",
		"/api/v1/oidc/providers/{providerId}/identities",
		"/api/v1/oidc/identities/{identityId}",
	} {
		if _, ok := document.Paths[path]; !ok {
			t.Errorf("contract missing path %q", path)
		}
	}
	for _, schema := range []string{"ErrorEnvelope", "ServiceAccount", "ServiceAccountKey", "OIDCProvider", "OIDCIdentity"} {
		if _, ok := document.Components.Schemas[schema]; !ok {
			t.Errorf("contract missing schema %q", schema)
		}
	}
}
